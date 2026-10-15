# burstlab-demo

A live terminal dashboard for presenting [BurstLab](https://github.com/scttfrdmn/burstlab)
cloud bursting to customers and conference audiences. Built for a 140×40 terminal:

```
 BurstLab Gen 2 · Rocky 9 · Slurm 23.11.10 · us-west-2                                                                  LIVE   14:56:17
╭─ ON-PREM · standing cluster ────── 4 nodes · 32/32 CPUs allocated ─╮╭─ CLOUD BURST ───────────────────── 4 up · 0 starting · 0 stopping ─╮
│ NODE        EC2           SLURM           LOAD       CPU   UP      ││ NODE        EC2           SLURM           LOAD       CPU   UP      │
│ head        ● running     controller      █░░░░░░░░░   —     1h03  ││ aws-burst-0 ● running     alloc           █████████░ 8/8     3:15  │
│ compute01   ● running     alloc           ██████████ 8/8     1h03  ││ aws-burst-1 ● running     idle ⏲ 1:47     ░░░░░░░░░░ 0/8     3:15  │
│ compute02   ● running     alloc           ██████████ 8/8     1h03  ││ aws-burst-2 ● running     idle ⏲ 1:47     ░░░░░░░░░░ 0/8     3:15  │
│ compute03   ● running     alloc           ██████████ 8/8     1h03  ││ aws-burst-3 ● running     idle ⏲ 1:47     ░░░░░░░░░░ 0/8     3:15  │
│ compute04   ● running     alloc           ██████████ 8/8     1h03  ││ aws-burst-4                                                        │
│ ·································································· ││ aws-burst-5                                                        │
│ QUEUE   running 9   pending 16                                     ││ aws-burst-6                                                        │
│  1001   alice  local     clog     R ×8      3:42  compute[01-04]   ││ aws-burst-7                                                        │
│  1001   alice  local     clog     PD ×16    0:00  (Resources)      ││ aws-burst-8                                                        │
│  1003   alice  aws       warm     R         0:13  aws-burst-0      ││ aws-burst-9                                                        │
╰───────────────────────────────────────────────── partition: local ─╯╰───── SuspendTime 10:50 · AWS Plugin for Slurm v2 · partition: aws ─╯
[alice@headnode ~]$ ~/demo/3-warm.sh
Submitted batch job 1003
[alice@headnode ~]$ █
```

The top 14 rows are the dashboard; the rest is a **real shell** (ssh to the head node as
`alice`) in a tmux pane, so `vim`, `less`, colours and resize behave normally.

Each row merges two views of the same node:

- **EC2** — `DescribeInstances` from your laptop (`AWS_PROFILE`), every 3 s
- **Slurm** — `scontrol show node` / `squeue` over ssh, every 2 s (sharing one
  ControlMaster connection with the shell pane)
- **LOAD** — real CPU utilisation: the head node samples `/proc/stat` on every up node
  0.5 s apart. (Slurm's own `CPULoad` is a 1-minute load average refreshed only every
  `SlurmdTimeout`/3 ≈ 100 s — far too slow on stage.) Needs the intra-cluster key that
  `push-jobs` installs.

Slurm's terse states are spelled out (`alloc#` → *powering up*, `idle%` → *powering down*).
A cloud slot with no instance is left blank; a terminated instance shows *terminated* in
gray (billing has stopped) until EC2 stops listing it, about an hour later. Idle burst nodes
count down to `SuspendTime`. Any row whose state changes flashes for 3 s so the audience knows where to look.

## Install

```bash
go build -o bin/burstlab-demo .     # needs tmux: brew install tmux
```

## Run

```bash
export AWS_PROFILE=aws
bin/burstlab-demo                      # discovers the head node, opens the tmux layout
bin/burstlab-demo push-jobs            # once per cluster: demo jobs in ~alice/demo + alice's
                                       #   intra-cluster ssh key (for live LOAD sampling)
bin/burstlab-demo --sim                # rehearse with no cluster (scripted, loops)
```

| Flag | Default | |
|---|---|---|
| `--cluster` | `burstlab-gen2` | Terraform `cluster_name` |
| `--region` | `$AWS_REGION` or `us-west-2` | |
| `--profile` | `$AWS_PROFILE` | passed explicitly to the dashboard pane (tmux panes don't reliably inherit your shell's env) |
| `--key` | first of `~/.ssh/burstlab-demo-key.pem`, `~/.ssh/burstlab-key.pem` | |
| `--user` | `alice` | |
| `--host` | discovered from EC2 tags | head node address |
| `--fresh` | | replace an existing `burstlab` tmux session |
| `--sim`, `--speed` | | simulator; `--speed 3` for a quick run-through |

`burstlab-demo dash` runs just the dashboard in the current terminal. In the simulator, click
the dashboard and use **space** (pause), **→** (skip 15 s), **r** (restart). **q** quits.

## The demo

Run from the shell pane as `alice`:

| Stage | Command | What the audience sees |
|---|---|---|
| 1. Clog on-prem | `~/demo/1-clog.sh` | 24 × 4-CPU tasks on `local`: all four compute nodes go to `alloc` with real (red) load; 16 tasks pile up *Pending (Resources)*. They run 30 min, so on-prem **stays clogged for the whole demo**. |
| 2. Burst | `~/demo/2-burst.sh` | 4 × 8-CPU tasks with `--partition=local,aws`. On-prem is full, so they land on burst slots: *powering up* → EC2 *pending* → *running* → slurmd registers → *alloc*. ~2–3 min. |
| 3. Warm reuse | `~/demo/3-warm.sh` | Run during the ⏲ idle countdown after stage 2 finishes. The job starts **in seconds** on an idle burst node — no launch, no boot. That node's countdown resets; the others power down first. |
| 4. Scale to zero | *(wait)* | After `SuspendTime` idle: *powering down* / EC2 *terminating* → EC2 *terminated* in gray: billing has stopped. |
| Reset | `~/demo/9-reset.sh` | Cancels the demo jobs. |

Durations can be tuned with `CLOG_SECONDS`, `BURST_SECONDS`, `WARM_SECONDS`, `BURST_NODES`.
Jobs burn CPU (not `sleep`) so the LOAD bars are real, and pass `--mem-per-cpu` because the
Gen 2 `slurm.conf` uses `CR_Core_Memory` with no `DefMemPerCPU` — without it each job would
claim a whole node's memory.

> Gen 2's `SuspendTime` is 650 s, so stage 4 takes ~11 min of idle. The simulator uses 150 s.

## Observed on a live Gen 2 cluster

- Burst: submit → *powering up* within one poll; EC2 *pending* → *running* in < 5 s (usually
  too fast to see); slurmd registers and the node goes *alloc* after ~3 min 20 s.
- `resume.py` tags `Name=aws-burst-N` right after CreateFleet, so rows match immediately.
- Burst node names aren't in DNS on the head node; the probe uses Slurm's `NodeAddr`.

## Layout

```
main.go                 CLI: up | dash | push-jobs
internal/source/        live (ssh + EC2) and simulated snapshot sources
internal/ui/            Bubble Tea dashboard
internal/launch/        tmux layout
jobs/                   demo job scripts (embedded; installed by push-jobs)
```
