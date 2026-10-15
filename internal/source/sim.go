package source

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/scttfrdmn/burstlab-demo/internal/state"
)

// Sim replays the canonical demo as a deterministic timeline so the dashboard
// can be rehearsed (or used as a fallback) with no cluster and no network:
//
//	clog on-prem → burst overflow → burst jobs finish → idle countdown →
//	warm job lands on an idle burst node instantly → nodes power down
//
// On-prem stays clogged the whole time. Times below are in sim-seconds at
// speed 1; SuspendTime is shortened from the cluster's 650 s to keep the
// rehearsal loop short.
type Sim struct {
	mu      sync.Mutex
	start   time.Time
	pausedT float64 // sim time when paused, <0 when running
	speed   float64
}

const (
	simCompute  = 4
	simBurst    = 10
	simCPUs     = 8
	simSuspend  = 150.0
	tClog       = 6.0
	tBurst      = 30.0
	fleetDelay  = 3.0  // resume.py → CreateFleet returns, instance exists
	ec2Boot     = 18.0 // pending → running
	slurmdUp    = 80.0 // after tBurst: cloud-init done, slurmd registered
	burstRun    = 75.0
	warmDelay   = 30.0 // operator submits the warm job this long into idle
	warmRun     = 45.0
	powerDown   = 25.0 // SuspendProgram → instance terminated
	burstJobs   = 4
	loadTau     = 6.0
	loopPadding = 20.0
)

var (
	tBurstUp   = tBurst + slurmdUp
	tBurstDone = tBurstUp + burstRun
	tWarm      = tBurstDone + warmDelay
	tWarmDone  = tWarm + warmRun
	tEnd       = tWarmDone + simSuspend + powerDown + 20
	tLoop      = tEnd + loopPadding
)

func NewSim(speed float64) *Sim {
	if speed <= 0 {
		speed = 1
	}
	return &Sim{start: time.Now(), pausedT: -1, speed: speed}
}

func (s *Sim) now() float64 {
	if s.pausedT >= 0 {
		return s.pausedT
	}
	return math.Mod(time.Since(s.start).Seconds()*s.speed, tLoop)
}

// TogglePause freezes or resumes the timeline.
func (s *Sim) TogglePause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pausedT >= 0 {
		s.start = time.Now().Add(-time.Duration(s.pausedT / s.speed * float64(time.Second)))
		s.pausedT = -1
	} else {
		s.pausedT = s.now()
	}
}

// Restart jumps back to the beginning of the timeline.
func (s *Sim) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.start = time.Now()
	if s.pausedT >= 0 {
		s.pausedT = 0
	}
}

// Skip moves the timeline forward by d sim-seconds.
func (s *Sim) Skip(d float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pausedT >= 0 {
		s.pausedT = math.Mod(s.pausedT+d, tLoop)
		return
	}
	s.start = s.start.Add(-time.Duration(d / s.speed * float64(time.Second)))
}

// ramp models slurmd's lagging CPULoad: approaches target after a change.
func ramp(from, to, since float64) float64 {
	if since < 0 {
		return from
	}
	return to + (from-to)*math.Exp(-since/loadTau)
}

func (s *Sim) Snapshot() state.Snapshot {
	s.mu.Lock()
	t := s.now()
	s.mu.Unlock()
	wall := time.Now()
	at := func(st float64) time.Time { // sim time → wall clock
		return wall.Add(-time.Duration((t - st) / s.speed * float64(time.Second)))
	}

	snap := state.Snapshot{
		Taken:       wall,
		Title:       "BurstLab Gen 2 · Rocky 9 · Slurm 23.11.10 · us-west-2",
		SuspendTime: time.Duration(simSuspend / s.speed * float64(time.Second)),
	}
	clogged := t >= tClog && t < tEnd

	snap.Head = state.Node{Name: "head", Role: state.RoleHead, EC2State: state.EC2Running,
		LaunchTime: at(-3600), SlurmState: "CONTROLLER", CPUTot: simCPUs, CPULoad: 0.3}
	if clogged {
		snap.Head.CPULoad = 0.6
	}

	for i := 1; i <= simCompute; i++ {
		n := state.Node{Name: fmt.Sprintf("compute%02d", i), Role: state.RoleCompute,
			EC2State: state.EC2Running, LaunchTime: at(-3600), CPUTot: simCPUs, SlurmState: "IDLE"}
		switch {
		case clogged:
			n.SlurmState, n.CPUAlloc = "ALLOCATED", simCPUs
			n.CPULoad = ramp(0.05, simCPUs, t-tClog)
		case t >= tEnd:
			n.CPULoad = ramp(simCPUs, 0.05, t-tEnd)
		default:
			n.CPULoad = 0.05
		}
		snap.Compute = append(snap.Compute, n)
	}

	for i := 0; i < simBurst; i++ {
		snap.Burst = append(snap.Burst, s.burstNode(i, t, at))
	}

	// Queue.
	if clogged {
		for k := 0; k < simCompute*2; k++ {
			snap.Jobs = append(snap.Jobs, state.Job{ID: fmt.Sprintf("1001_%d", k), User: "alice",
				Partition: "local", Name: "clog", State: "R", Time: mmss(t - tClog),
				Nodes: fmt.Sprintf("compute%02d", k/2+1)})
		}
		snap.Running += simCompute * 2
		snap.Jobs = append(snap.Jobs, state.Job{ID: "1001_[8-23]", User: "alice",
			Partition: "local", Name: "clog", State: "PD", Time: "0:00", Nodes: "(Resources)"})
		snap.Pending += 16
	}
	if t >= tBurst && t < tBurstDone {
		for k := 0; k < burstJobs; k++ {
			j := state.Job{ID: fmt.Sprintf("1002_%d", k), User: "alice", Partition: "aws",
				Name: "burst", State: "CF", Time: mmss(t - tBurst), Nodes: fmt.Sprintf("aws-burst-%d", k)}
			if t >= tBurstUp {
				j.State, j.Time = "R", mmss(t-tBurstUp)
				snap.Running++
			} else {
				snap.Pending++
			}
			snap.Jobs = append(snap.Jobs, j)
		}
	}
	if t >= tWarm && t < tWarmDone {
		snap.Jobs = append(snap.Jobs, state.Job{ID: "1003", User: "alice", Partition: "aws",
			Name: "warm", State: "R", Time: mmss(t - tWarm), Nodes: "aws-burst-0"})
		snap.Running++
	}
	return snap
}

func (s *Sim) burstNode(i int, t float64, at func(float64) time.Time) state.Node {
	n := state.Node{Name: fmt.Sprintf("aws-burst-%d", i), Role: state.RoleBurst,
		CPUTot: simCPUs, SlurmState: "IDLE+CLOUD+POWERED_DOWN"}
	if i >= burstJobs || t < tBurst {
		return n
	}
	lastBusy := tBurstDone
	if i == 0 {
		lastBusy = tWarmDone // the warm job keeps node 0 alive longer
	}
	tDown := lastBusy + simSuspend
	launched := tBurst + fleetDelay

	// EC2 lifecycle
	switch {
	case t < launched:
	case t < launched+ec2Boot:
		n.EC2State = state.EC2Pending
	case t < tDown:
		n.EC2State = state.EC2Running
	case t < tDown+powerDown:
		n.EC2State = state.EC2ShuttingDown
	default:
		n.EC2State = state.EC2Terminated // until the loop restarts
	}
	if n.EC2State != "" {
		n.LaunchTime = at(launched)
	}

	// Slurm lifecycle
	busy := func() bool {
		return (t >= tBurst && t < tBurstDone) || (i == 0 && t >= tWarm && t < tWarmDone)
	}
	switch {
	case t < tBurstUp:
		n.SlurmState, n.CPUAlloc = "ALLOCATED+CLOUD+POWERING_UP", simCPUs
	case busy():
		n.SlurmState, n.CPUAlloc = "ALLOCATED+CLOUD", simCPUs
	case t < tDown:
		n.SlurmState = "IDLE+CLOUD"
		n.LastBusy = at(tBurstDone)
		if i == 0 && t >= tWarmDone {
			n.LastBusy = at(tWarmDone)
		}
	case t < tDown+powerDown:
		n.SlurmState = "IDLE+CLOUD+POWERING_DOWN"
	}

	// Load follows allocation with lag.
	switch {
	case t < tBurstUp:
	case t < tBurstDone:
		n.CPULoad = ramp(0, simCPUs, t-tBurstUp)
	case i == 0 && t >= tWarm && t < tWarmDone:
		n.CPULoad = ramp(ramp(simCPUs, 0, tWarm-tBurstDone), simCPUs, t-tWarm)
	case i == 0 && t >= tWarmDone:
		n.CPULoad = ramp(simCPUs, 0, t-tWarmDone)
	default:
		n.CPULoad = ramp(simCPUs, 0, t-tBurstDone)
	}
	if n.EC2State != state.EC2Running {
		n.CPULoad = 0
	}
	return n
}

func mmss(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	s := int(sec)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
