package source

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/scttfrdmn/burstlab-demo/internal/state"
)

// SSH describes how to reach the head node. The dashboard shells out to the
// system ssh so it honours ~/.ssh/config and shares a ControlMaster socket
// with the interactive shell pane — one TCP connection, one auth.
type SSH struct {
	Host string
	User string
	Key  string
}

// ControlPath is shared by the poller and the interactive pane.
const ControlPath = "~/.ssh/burstlab-demo-%C"

// Args returns ssh arguments (without the command) for this target.
func (s SSH) Args(extra ...string) []string {
	a := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + ControlPath,
		"-o", "ControlPersist=30m",
		"-o", "ServerAliveInterval=15",
	}
	if s.Key != "" {
		a = append(a, "-i", s.Key)
	}
	a = append(a, extra...)
	return append(a, s.User+"@"+s.Host)
}

// One round trip collects everything the dashboard needs from Slurm.
// %R is the nodelist for running jobs and the pending reason otherwise.
//
// @@UTIL: Slurm's CPULoad is the 1-minute load average and is refreshed only
// every SlurmdTimeout/3 (~100 s on BurstLab) — useless on stage. Instead the
// head node samples /proc/stat on every up node twice, 0.5 s apart, and
// reports busy cores. This needs alice to have an intra-cluster ssh key
// (installed by push-jobs; /home is shared EFS so it reaches burst nodes
// too). Burst nodes are new instances under reused names, so host keys are
// not pinned — this is intra-VPC traffic on a lab cluster.
const slurmProbe = `export SLURM_CONF=/opt/slurm/etc/slurm.conf; B=/opt/slurm/bin
busy() { awk 'NR==1{for(i=2;i<=NF;i++)t1+=$i; i1=$5+$6} NR==2{for(i=2;i<=NF;i++)t2+=$i; i2=$5+$6} NR==3{n=$1} END{if(t2>t1) printf "%.2f\n", (1-(i2-i1)/(t2-t1))*n}'; }
sample='head -1 /proc/stat; sleep 0.5; head -1 /proc/stat; nproc'
echo @@UTIL
if [ -f ~/.ssh/id_ed25519 ]; then
  # ssh to NodeAddr (%o): burst node names aren't in DNS; resume.py sets the IP
  $B/sinfo -h -N -o '%N %o %t' | awk '$3 !~ /[~#%*]/ {print $1, $2}' | sort -u |
  { while read -r n a; do
    ( v=$(timeout 4 ssh -n -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
          -o LogLevel=ERROR -o ConnectTimeout=2 -o ControlMaster=auto -o ControlPath=/tmp/bld-%r@%h \
          -o ControlPersist=120 "$a" "$sample" 2>/dev/null | busy) && [ -n "$v" ] && echo "$n $v" ) &
  done; wait; }  # the loop runs in a pipeline subshell, so wait here
fi
echo "head $(sh -c "$sample" | busy)"
wait
echo @@NOW; date -u +%Y-%m-%dT%H:%M:%S
echo @@NODES; $B/scontrol -o show node
echo @@JOBS; $B/squeue -h -o '%i|%u|%P|%j|%t|%M|%R'
echo @@COUNTS; $B/squeue -h -r -o '%t'
echo @@LOAD; cat /proc/loadavg; nproc
echo @@CONF; $B/scontrol show config | grep -E '^(SuspendTime|SLURM_VERSION) '
`

type slurmResult struct {
	nodes       []state.Node
	jobs        []state.Job
	running     int
	pending     int
	headLoad    float64
	headCPUs    int
	suspendTime time.Duration
	version     string
	busy        map[string]float64 // busy cores per node from /proc/stat sampling
}

func pollSlurm(ctx context.Context, s SSH) (slurmResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := s.Args("-o", "BatchMode=yes", "-o", "ConnectTimeout=5")
	args = append(args, "TZ=UTC bash -s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(slurmProbe)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		return slurmResult{}, fmt.Errorf("ssh: %v %s", err, msg)
	}
	return parseProbe(out), nil
}

func parseProbe(out []byte) slurmResult {
	var r slurmResult
	var remoteNow time.Time
	section := ""
	var loadLines []string
	r.busy = map[string]float64{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "@@") {
			section = line[2:]
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch section {
		case "UTIL":
			if f := strings.Fields(line); len(f) == 2 {
				if v, err := strconv.ParseFloat(f[1], 64); err == nil {
					r.busy[f[0]] = v
				}
			}
		case "NOW":
			remoteNow, _ = time.Parse("2006-01-02T15:04:05", strings.TrimSpace(line))
		case "NODES":
			if n, ok := parseNodeLine(line, remoteNow); ok {
				r.nodes = append(r.nodes, n)
			}
		case "JOBS":
			f := strings.SplitN(line, "|", 7)
			if len(f) == 7 {
				r.jobs = append(r.jobs, state.Job{ID: f[0], User: f[1], Partition: f[2],
					Name: f[3], State: f[4], Time: f[5], Nodes: f[6]})
			}
		case "COUNTS":
			switch strings.TrimSpace(line) {
			case "R", "CG":
				r.running++
			case "PD", "CF":
				r.pending++
			}
		case "LOAD":
			loadLines = append(loadLines, line)
		case "CONF":
			k, v, _ := strings.Cut(line, "=")
			v = strings.TrimSpace(v)
			switch strings.TrimSpace(k) {
			case "SuspendTime":
				if f := strings.Fields(v); len(f) > 0 {
					if n, err := strconv.Atoi(f[0]); err == nil {
						r.suspendTime = time.Duration(n) * time.Second
					}
				}
			case "SLURM_VERSION":
				r.version = v
			}
		}
	}
	// Prefer sampled utilisation over Slurm's stale CPULoad / loadavg.
	for i := range r.nodes {
		if v, ok := r.busy[r.nodes[i].Name]; ok {
			r.nodes[i].CPULoad = v
		}
	}
	if len(loadLines) >= 2 {
		if f := strings.Fields(loadLines[0]); len(f) > 0 {
			r.headLoad, _ = strconv.ParseFloat(f[0], 64)
		}
		r.headCPUs, _ = strconv.Atoi(strings.TrimSpace(loadLines[1]))
	}
	if v, ok := r.busy["head"]; ok {
		r.headLoad = v
	}
	return r
}

// scontrol -o prints Key=Value pairs on one line, but some values (OS=, Reason=)
// contain spaces, so split on " Key=" boundaries rather than on whitespace.
var kvRe = regexp.MustCompile(`(?:^| )([A-Za-z_]+)=`)

func parseKV(line string) map[string]string {
	m := map[string]string{}
	idx := kvRe.FindAllStringSubmatchIndex(line, -1)
	for i, loc := range idx {
		end := len(line)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		m[line[loc[2]:loc[3]]] = strings.TrimSpace(line[loc[1]:end])
	}
	return m
}

func parseNodeLine(line string, remoteNow time.Time) (state.Node, bool) {
	kv := parseKV(line)
	name := kv["NodeName"]
	if name == "" {
		return state.Node{}, false
	}
	n := state.Node{Name: name, SlurmState: kv["State"], Role: state.RoleCompute}
	if strings.Contains(n.SlurmState, "CLOUD") {
		n.Role = state.RoleBurst
	}
	n.CPUAlloc, _ = strconv.Atoi(kv["CPUAlloc"])
	n.CPUTot, _ = strconv.Atoi(kv["CPUTot"])
	n.CPULoad, _ = strconv.ParseFloat(kv["CPULoad"], 64) // N/A → 0
	// LastBusyTime is printed in the controller's local time with no zone; we
	// ran the probe with TZ=UTC and fetched the remote clock in the same form,
	// so the difference is exact and immune to laptop/head clock skew.
	if lb, err := time.Parse("2006-01-02T15:04:05", kv["LastBusyTime"]); err == nil && !remoteNow.IsZero() {
		n.LastBusy = time.Now().Add(-remoteNow.Sub(lb))
	}
	return n, true
}
