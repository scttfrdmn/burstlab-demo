// Package state defines the snapshot the dashboard renders. Sources (live
// AWS+Slurm, or the simulator) produce Snapshots; the UI only consumes them.
package state

import "time"

// Role distinguishes the standing (mock on-prem) cluster from burst capacity.
type Role int

const (
	RoleHead Role = iota
	RoleCompute
	RoleBurst
)

// EC2 instance states as reported by DescribeInstances. Empty means no
// instance exists (a burst slot that is powered off).
const (
	EC2None         = ""
	EC2Pending      = "pending"
	EC2Running      = "running"
	EC2Stopping     = "stopping"
	EC2Stopped      = "stopped"
	EC2ShuttingDown = "shutting-down"
	EC2Terminated   = "terminated"
)

// Node is one row in the dashboard.
type Node struct {
	Name string // Slurm node name (compute01, aws-burst-0) or "head"
	Role Role

	// EC2 view
	EC2State   string
	InstanceID string
	LaunchTime time.Time

	// Slurm view
	SlurmState string // raw scontrol State, e.g. "IDLE+CLOUD+POWERED_DOWN"
	CPUAlloc   int
	CPUTot     int
	CPULoad    float64   // load average reported by slurmd (or /proc/loadavg for head)
	LastBusy   time.Time // for the idle → power-down countdown
}

// Job is one line in the queue panel.
type Job struct {
	ID        string
	User      string
	Partition string
	Name      string
	State     string // compact: R, PD, CF, CG
	Time      string
	Nodes     string // nodelist, or pending reason in parentheses
}

// Snapshot is a complete point-in-time view of the cluster.
type Snapshot struct {
	Taken       time.Time
	Head        Node
	Compute     []Node
	Burst       []Node
	Jobs        []Job
	Running     int // job counts (array tasks expanded)
	Pending     int
	SuspendTime time.Duration
	Title       string // e.g. "BurstLab Gen 2 · Rocky 9 · Slurm 23.11 · us-west-2"

	// Per-source health, shown in the title bar when a poll fails.
	SlurmErr error
	EC2Err   error
}

// Source produces snapshots.
type Source interface {
	Snapshot() Snapshot
}
