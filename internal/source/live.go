package source

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/scttfrdmn/burstlab-demo/internal/state"
)

var genFromName = regexp.MustCompile(`gen\d+`)

var genTitles = map[string]string{
	"gen1": "Gen 1 · Rocky 8",
	"gen2": "Gen 2 · Rocky 9",
	"gen3": "Gen 3 · Rocky 10",
	"gen4": "Gen 4 · Ubuntu 22.04",
	"gen5": "Gen 5 · Ubuntu 24.04",
	"gen6": "Gen 6 · Amazon Linux 2023",
}

// Live polls Slurm (over ssh) and EC2 concurrently and merges the results.
type Live struct {
	ssh SSH
	ec2 *EC2

	mu      sync.Mutex
	slurm   slurmResult
	slurmAt time.Time
	insts   map[string]ec2Instance
	slurmEr error
	ec2Er   error
}

func NewLive(ctx context.Context, s SSH, e *EC2) *Live {
	l := &Live{ssh: s, ec2: e}
	go l.loop(ctx, 2*time.Second, func() {
		r, err := pollSlurm(ctx, l.ssh)
		l.mu.Lock()
		defer l.mu.Unlock()
		l.slurmEr = err
		if err == nil {
			l.slurm, l.slurmAt = r, time.Now()
		}
	})
	go l.loop(ctx, 3*time.Second, func() {
		m, err := l.ec2.Poll(ctx)
		l.mu.Lock()
		defer l.mu.Unlock()
		l.ec2Er = err
		if err == nil {
			l.insts = m
		}
	})
	return l
}

func (l *Live) loop(ctx context.Context, every time.Duration, f func()) {
	for {
		f()
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (l *Live) Snapshot() state.Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()

	snap := state.Snapshot{
		Taken:       time.Now(),
		Jobs:        l.slurm.jobs,
		Running:     l.slurm.running,
		Pending:     l.slurm.pending,
		SuspendTime: l.slurm.suspendTime,
		SlurmErr:    l.slurmEr,
		EC2Err:      l.ec2Er,
	}

	// The head/compute Generation tag is hard-coded to gen1 in BurstLab's
	// Terraform modules, so prefer the cluster name (burstlab-gen2), then a
	// burst node's tag (set correctly from the launch template).
	gen := genFromName.FindString(l.ec2.Cluster)
	snap.Head = state.Node{Name: "head", Role: state.RoleHead, CPULoad: l.slurm.headLoad,
		CPUTot: l.slurm.headCPUs, SlurmState: "CONTROLLER"}
	l.attach(&snap.Head)

	// Slurm is authoritative for which rows exist; EC2 decorates them.
	seen := map[string]bool{"head": true}
	for _, n := range l.slurm.nodes {
		seen[n.Name] = true
		l.attach(&n)
		if n.Role == state.RoleBurst {
			snap.Burst = append(snap.Burst, n)
		} else {
			snap.Compute = append(snap.Compute, n)
		}
		if i, ok := l.insts[n.Name]; ok && gen == "" && n.Role == state.RoleBurst {
			gen = i.Generation
		}
	}
	// If Slurm is unreachable, still show what EC2 knows.
	for name := range l.insts {
		if seen[name] {
			continue
		}
		n := state.Node{Name: name, Role: state.RoleBurst}
		if strings.HasPrefix(name, "compute") {
			n.Role = state.RoleCompute
		}
		l.attach(&n)
		if n.Role == state.RoleBurst {
			snap.Burst = append(snap.Burst, n)
		} else {
			snap.Compute = append(snap.Compute, n)
		}
	}
	sortNodes(snap.Compute)
	sortNodes(snap.Burst)

	title := "BurstLab"
	if t, ok := genTitles[gen]; ok {
		title += " " + t
	}
	if v := l.slurm.version; v != "" {
		title += " · Slurm " + v
	}
	if l.ec2 != nil && l.ec2.Region != "" {
		title += " · " + l.ec2.Region
	}
	snap.Title = title
	if l.slurmEr != nil && !l.slurmAt.IsZero() {
		snap.SlurmErr = fmt.Errorf("%v (last good %s ago)", l.slurmEr,
			time.Since(l.slurmAt).Round(time.Second))
	}
	return snap
}

func (l *Live) attach(n *state.Node) {
	i, ok := l.insts[n.Name]
	if !ok {
		return
	}
	n.EC2State, n.InstanceID, n.LaunchTime = i.State, i.ID, i.LaunchTime
}

// sortNodes orders aws-burst-2 before aws-burst-10.
func sortNodes(ns []state.Node) {
	sort.Slice(ns, func(a, b int) bool {
		pa, na := splitNum(ns[a].Name)
		pb, nb := splitNum(ns[b].Name)
		if pa != pb {
			return pa < pb
		}
		return na < nb
	})
}

func splitNum(s string) (string, int) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	n := 0
	fmt.Sscanf(s[i:], "%d", &n)
	return s[:i], n
}
