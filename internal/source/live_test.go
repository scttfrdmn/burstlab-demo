package source

import (
	"testing"
	"time"

	"github.com/scttfrdmn/burstlab-demo/internal/state"
)

func liveWith(insts map[string]ec2Instance, alive []string, nodes ...state.Node) *Live {
	l := &Live{ec2: &EC2{Cluster: "burstlab-gen2"}, insts: insts, alive: map[string]bool{}}
	for _, id := range alive {
		l.alive[id] = true
	}
	l.slurm.nodes = nodes
	return l
}

func burstRow(t *testing.T, l *Live, name string) state.Node {
	t.Helper()
	for _, n := range l.Snapshot().Burst {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("no row %s", name)
	return state.Node{}
}

func TestTerminatedShownOnlyIfSeenAlive(t *testing.T) {
	old := map[string]ec2Instance{"aws-burst-0": {ID: "i-old", State: "terminated", LaunchTime: time.Now().Add(-time.Hour)}}
	off := state.Node{Name: "aws-burst-0", Role: state.RoleBurst, SlurmState: "IDLE+CLOUD+POWERED_DOWN"}

	// e.g. left over from a previous deployment of the same cluster name
	if got := burstRow(t, liveWith(old, nil, off), "aws-burst-0").EC2State; got != "" {
		t.Errorf("never-seen terminated instance shown as %q", got)
	}
	// watched it run, then terminate: show it so billing visibly stops
	if got := burstRow(t, liveWith(old, []string{"i-old"}, off), "aws-burst-0").EC2State; got != state.EC2Terminated {
		t.Errorf("seen-alive terminated instance = %q, want terminated", got)
	}
	// Slurm is launching a replacement; the old instance is irrelevant
	up := off
	up.SlurmState = "ALLOCATED+CLOUD+POWERING_UP"
	if got := burstRow(t, liveWith(old, []string{"i-old"}, up), "aws-burst-0").EC2State; got != "" {
		t.Errorf("terminated shown over a powering-up slot: %q", got)
	}
}
