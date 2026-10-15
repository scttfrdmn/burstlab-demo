package source

import (
	"testing"
	"time"

	"github.com/scttfrdmn/burstlab-demo/internal/state"
)

// Representative Slurm 23.11 output (Gen 2). Note OS= contains spaces and
// CfgTRES= contains '=' — both must not confuse the key/value split.
const probeOut = `@@UTIL
compute01 7.91
head 0.35
@@NOW
2026-10-14T13:00:30
@@NODES
NodeName=aws-burst-0 CoresPerSocket=1 CPUAlloc=0 CPUEfctv=8 CPUTot=8 CPULoad=0.12 AvailableFeatures=(null) ActiveFeatures=(null) Gres=(null) NodeAddr=10.0.2.33 NodeHostName=aws-burst-0 Version=23.11.10 OS=Linux 5.14.0-427.13.1.el9_4.x86_64 #1 SMP PREEMPT_DYNAMIC Wed May 1 19:11:28 UTC 2024 RealMemory=31000 AllocMem=0 FreeMem=30500 Sockets=8 Boards=1 State=IDLE+CLOUD ThreadsPerCore=1 TmpDisk=0 Weight=1 Owner=N/A MCS_label=N/A Partitions=aws BootTime=2026-10-14T12:50:00 SlurmdStartTime=2026-10-14T12:51:00 LastBusyTime=2026-10-14T13:00:00 ResumeAfterTime=None CfgTRES=cpu=8,mem=31000M,billing=8 AllocTRES= CapWatts=n/a
NodeName=aws-burst-1 CoresPerSocket=1 CPUAlloc=0 CPUEfctv=8 CPUTot=8 CPULoad=N/A AvailableFeatures=(null) ActiveFeatures=(null) Gres=(null) NodeAddr=aws-burst-1 NodeHostName=aws-burst-1 RealMemory=31000 AllocMem=0 FreeMem=N/A Sockets=8 Boards=1 State=IDLE+CLOUD+POWERED_DOWN ThreadsPerCore=1 TmpDisk=0 Weight=1 Owner=N/A MCS_label=N/A Partitions=aws BootTime=None SlurmdStartTime=None LastBusyTime=Unknown ResumeAfterTime=None CfgTRES=cpu=8,mem=31000M,billing=8 AllocTRES=
NodeName=compute01 Arch=x86_64 CoresPerSocket=1 CPUAlloc=8 CPUEfctv=8 CPUTot=8 CPULoad=7.98 AvailableFeatures=(null) ActiveFeatures=(null) Gres=(null) NodeAddr=10.0.1.10 NodeHostName=compute01 Version=23.11.10 OS=Linux 5.14.0 #1 SMP RealMemory=31000 AllocMem=32768 Sockets=8 Boards=1 State=ALLOCATED ThreadsPerCore=1 Partitions=local LastBusyTime=2026-10-14T13:00:29 CfgTRES=cpu=8,mem=31000M,billing=8 AllocTRES=cpu=8,mem=32G
@@JOBS
1001_[8-23]|alice|local|clog|PD|0:00|(Resources)
1001_0|alice|local|clog|R|3:01|compute01
1002_0|alice|aws|burst|CF|0:40|aws-burst-0
@@COUNTS
PD
PD
R
CF
@@LOAD
0.42 0.30 0.20 2/300 12345
8
@@CONF
SLURM_VERSION           = 23.11.10
SuspendTime             = 650 sec
`

func TestParseProbe(t *testing.T) {
	r := parseProbe([]byte(probeOut))
	if len(r.nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(r.nodes))
	}
	b0, b1, c1 := r.nodes[0], r.nodes[1], r.nodes[2]
	if b0.Role != state.RoleBurst || b0.SlurmState != "IDLE+CLOUD" || b0.CPUTot != 8 || b0.CPULoad != 0.12 {
		t.Errorf("aws-burst-0 = %+v", b0)
	}
	// idle 30 s per the remote clock, regardless of the local clock or zone
	if idle := time.Since(b0.LastBusy); idle < 29*time.Second || idle > 32*time.Second {
		t.Errorf("aws-burst-0 idle for %v, want ~30s", idle)
	}
	if b1.SlurmState != "IDLE+CLOUD+POWERED_DOWN" || b1.CPULoad != 0 || !b1.LastBusy.IsZero() {
		t.Errorf("aws-burst-1 = %+v", b1)
	}
	if c1.Role != state.RoleCompute || c1.CPUAlloc != 8 || c1.SlurmState != "ALLOCATED" || c1.CPULoad != 7.91 {
		t.Errorf("compute01 = %+v", c1)
	}
	if len(r.jobs) != 3 || r.jobs[0].Nodes != "(Resources)" {
		t.Errorf("jobs = %+v", r.jobs)
	}
	if r.running != 1 || r.pending != 3 {
		t.Errorf("running/pending = %d/%d, want 1/3", r.running, r.pending)
	}
	if r.headLoad != 0.35 || r.headCPUs != 8 { // sampled value wins over loadavg
		t.Errorf("head load = %v/%d", r.headLoad, r.headCPUs)
	}
	if r.suspendTime != 650*time.Second || r.version != "23.11.10" {
		t.Errorf("conf = %v %q", r.suspendTime, r.version)
	}
}

func TestBetterPrefersLiveThenNewest(t *testing.T) {
	now := time.Now()
	dead := ec2Instance{State: "terminated", LaunchTime: now}
	live := ec2Instance{State: "running", LaunchTime: now.Add(-time.Hour)}
	if !better(live, dead) || better(dead, live) {
		t.Error("live instance should win over a newer terminated one")
	}
	newer := ec2Instance{State: "terminated", LaunchTime: now.Add(time.Minute)}
	if !better(newer, dead) {
		t.Error("newer terminated should beat older terminated")
	}
}
