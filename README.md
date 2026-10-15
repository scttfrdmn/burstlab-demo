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
- **LOAD** — current CPU utilisation, sampled from `/proc/stat` on each node via the head
  node (uses the intra-cluster key that `push-jobs` installs)

Slurm's terse states are spelled out (`alloc#` → *powering up*, `idle%` → *powering down*).
A cloud slot with no instance is left blank; a terminated instance shows *terminated* in
gray (billing has stopped) until EC2 stops listing it, about an hour later. Idle burst nodes
count down to `SuspendTime`. Any row whose state changes flashes for 3 s so the audience knows where to look.

## Install

```bash
brew install tmux go
go build -o bin/burstlab-demo .
bin/burstlab-demo --sim            # no cluster needed — check it looks right at 140×40
```

## Runbook: from zero to demo and back

Times are for BurstLab Gen 2 (Rocky 9 / Slurm 23.11) in us-west-2.

| Step | What | Time |
|---|---|---|
| 1 | Prerequisites (once) | — |
| 2 | One-time setup: key pair, BurstLab checkout, tfvars | 5 min |
| 3 | Deploy the cluster | ~5 min apply + ~4 min init |
| 4 | Validate and prepare | 2 min |
| 5 | Rehearse / present | ~20 min end to end |
| 6 | Tear down and verify | ~5 min |

The idle cluster costs about **$1.80/hr**, plus ~$0.46/hr per burst node while one is up.
Deploy close to when you need it and tear it down after.

### 1. Prerequisites

- AWS CLI profile with rights to deploy BurstLab (`AWS_PROFILE=aws` here), Terraform ≥ 1.5,
  Go, tmux, and bash ≥ 4 for BurstLab's scripts (`brew install bash` on macOS).
- A BurstLab Gen 2 AMI in the region. Build once (~15–20 min) if you don't have one:
  ```bash
  cd ~/src/burstlab-deploy
  packer init ami && packer build -var aws_profile=aws -var aws_region=us-west-2 ami/rocky9-slurm2311.pkr.hcl
  aws ec2 describe-images --owners self --filters 'Name=name,Values=burstlab-gen2-*' \
    --query 'sort_by(Images,&CreationDate)[-1].[ImageId,Name]' --output text
  ```
- Quota headroom: `bash scripts/check-quotas.sh --profile aws --region us-west-2` in the
  BurstLab checkout (needs ~40 vCPUs idle, 120 with all 10 burst nodes).

### 2. One-time setup

**SSH key.** Create a dedicated key pair and keep the private key — EC2 never shows it again.
burstlab-demo looks for `~/.ssh/burstlab-demo-key.pem` (then `~/.ssh/burstlab-key.pem`) by default.

```bash
export AWS_PROFILE=aws AWS_REGION=us-west-2
( umask 077; aws ec2 create-key-pair --key-name burstlab-demo-key --key-type ed25519 \
    --key-format pem --query KeyMaterial --output text > ~/.ssh/burstlab-demo-key.pem )
```

**BurstLab checkout.** Use a dedicated clone of `main` for deploying — Terraform state lives in
the checkout, so keep it separate from any working copy you edit.

```bash
git clone https://github.com/scttfrdmn/burstlab.git ~/src/burstlab-deploy
cd ~/src/burstlab-deploy/terraform/generations/gen2-slurm2311-rocky9
cat > terraform.tfvars <<'EOF'
aws_region    = "us-west-2"
aws_profile   = "aws"
cluster_name  = "burstlab-gen2"
key_name      = "burstlab-demo-key"
head_node_ami = "ami-XXXXXXXX"   # from step 1
EOF
```

`terraform.tfvars` is gitignored. Before each deploy, `git pull` to get the latest BurstLab
(config and Terraform changes apply at deploy time; only Packer changes need an AMI rebuild).

### 3. Deploy

```bash
cd ~/src/burstlab-deploy/terraform/generations/gen2-slurm2311-rocky9
terraform init
terraform plan -out=tfplan          # expect: 51 to add, 0 to change, 0 to destroy
terraform apply tfplan && rm tfplan # saved plans contain secrets; don't keep them
terraform output head_node_public_ip
```

Then wait for cloud-init on the head node (~4 min; it also brings up the compute nodes):

```bash
ssh -i ~/.ssh/burstlab-demo-key.pem rocky@<ip> 'sudo tail -f /var/log/burstlab-init.log'
# done when you see: === BurstLab head node init complete ===
```

### 4. Validate and prepare

```bash
ssh -i ~/.ssh/burstlab-demo-key.pem rocky@<ip> 'bash /opt/slurm/etc/validate-cluster.sh' | tail -3
bin/burstlab-demo push-jobs         # demo jobs → ~alice/demo, + alice's intra-cluster ssh key
```

Expect all PASSes and `sinfo` showing `local` = `compute[01-04]` and `aws` = `aws-burst-[0-9]`
as `idle~`. If compute nodes show `idle*` (not responding) they are still booting — give them a
minute. `push-jobs` is idempotent; re-run it after any redeploy (the key lives on the cluster's EFS).

### 5. Rehearse, then present

Rehearse without a cluster any time with `bin/burstlab-demo --sim --fresh` (`--speed 3` to skim).

Live:

```bash
export AWS_PROFILE=aws
bin/burstlab-demo --fresh            # 140×40 terminal; finds the head node from EC2 tags
```

The bottom pane is `alice@headnode`. Run the stages there:

| Stage | Command | What the audience sees |
|---|---|---|
| 0. Reset | `~/demo/9-reset.sh` | Clean queue (only needed between runs). |
| 1. Clog on-prem | `~/demo/1-clog.sh` | 24 × 4-CPU tasks on `local`: all four compute nodes go to `alloc` with full (red) load; 16 tasks pile up *Pending (Resources)*. They run 30 min, so on-prem **stays clogged for the whole demo**. |
| 2. Burst | `~/demo/2-burst.sh` | 4 × 8-CPU tasks with `--partition=local,aws`. On-prem is full, so they land on burst slots: *powering up* (within one poll) → EC2 *running* (seconds) → slurmd registers → *alloc* at full load (**~3 min 20 s**). Jobs run 2 min. |
| 3. Warm reuse | `~/demo/3-warm.sh` | Run during the ⏲ idle countdown after stage 2. The job is running on an idle burst node **within seconds** — no launch, no boot. That node's countdown resets. |
| 4. Scale to zero | *(talk)* | Exactly at the countdown: *powering down* / EC2 *terminating* → *terminated* in gray (billing has stopped). The warm-reused node goes last. |

Talking-point timing for a live run: stage 2's boot gap (~3 min) is the time to explain
Plugin v2 → `resume.py` → EC2 CreateFleet → cloud-init → slurmd registering. Stage 4 needs
`SuspendTime` (650 s ≈ 11 min) of idle after the last job — plan Q&A there, or end on stage 3.

Durations can be tuned with `CLOG_SECONDS`, `BURST_SECONDS`, `WARM_SECONDS`, `BURST_NODES`.
Jobs burn CPU (rather than `sleep`) so the LOAD bars show real work.

### 6. Tear down and verify

First, in the shell pane, cancel the demo jobs with `~/demo/9-reset.sh`. Then:

```bash
cd ~/src/burstlab-deploy/terraform/generations/gen2-slurm2311-rocky9
AWS_PROFILE=aws KEY=~/.ssh/burstlab-demo-key.pem \
  bash ../../../scripts/teardown.sh        # drain → terminate burst nodes → terraform destroy
```

Then confirm nothing is left billing:

```bash
export AWS_PROFILE=aws AWS_REGION=us-west-2
aws ec2 describe-instances --filters Name=tag:Project,Values=burstlab \
  Name=instance-state-name,Values=pending,running,stopping,stopped,shutting-down \
  --query 'Reservations[].Instances[].InstanceId' --output text          # expect empty
aws ec2 describe-vpcs --filters Name=tag:Project,Values=burstlab --query 'Vpcs[].VpcId' --output text
aws efs describe-file-systems --query 'FileSystems[?Tags[?Key==`Project`&&Value==`burstlab`]].[FileSystemId,Name]' --output text
```

The key pair and AMI are kept for next time. An EFS named `burstlab-ephemeral-*` is a leftover
from BurstLab's workload scenarios, not the base cluster — `scripts/cleanup-orphan-efs.sh` removes those.

### Troubleshooting

| Symptom | What to do |
|---|---|
| `no running head node found` | The cluster isn't deployed, or `--cluster` / `--region` don't match `terraform.tfvars`. |
| Title bar shows an `ec2:` error | AWS credentials are missing or expired for the profile — refresh them, then `bin/burstlab-demo --fresh`. |
| Title bar shows a `slurm: ssh:` error | The head node isn't reachable as alice: check `--key`, and that init has finished. |
| LOAD bars stay low while nodes are `alloc` | Run `bin/burstlab-demo push-jobs` for this cluster. |
| Compute nodes show `idle*` | They're still booting after a deploy; wait a minute. |
| `REMOTE HOST IDENTIFICATION HAS CHANGED` | A redeploy reused an IP address: `ssh-keygen -R <ip>`. |
| Panes still run an old build | `bin/burstlab-demo --fresh` replaces the tmux session. |

## Recording the demo

`scripts/record-demo.sh` runs the whole demo unattended under asciinema at 140×40: it opens
a fresh layout, types each stage into the shell pane, and waits for the dashboard to show each
outcome before moving on. It stops once every burst node has terminated.

```bash
brew install asciinema agg ffmpeg
AWS_PROFILE=aws scripts/record-demo.sh     # ~20 min against a deployed, idle cluster
scripts/render-mp4.sh                       # sped-up .cast + MP4 (waits compressed to seconds)
```

| File (in `recordings/`) | |
|---|---|
| `burstlab-demo.cast` | real time — `asciinema play -m recordings/burstlab-demo.cast` (`-m` holds the final frame instead of exiting; add `-s 2` for 2×) |
| `burstlab-demo-fast.cast` | boot, job runtime and idle waits squeezed to a few seconds each — also play with `-m` |
| `burstlab-demo-fast.mp4` | the sped-up version as video |

Add `--sim` to `record-demo.sh` to try the recording pipeline without a cluster.

## Reference

| Flag | Default | |
|---|---|---|
| `--cluster` | `burstlab-gen2` | Terraform `cluster_name` |
| `--region` | `$AWS_REGION` or `us-west-2` | |
| `--profile` | `$AWS_PROFILE` | AWS profile |
| `--key` | first of `~/.ssh/burstlab-demo-key.pem`, `~/.ssh/burstlab-key.pem` | |
| `--user` | `alice` | |
| `--host` | discovered from EC2 tags | head node address |
| `--fresh` | | replace an existing `burstlab` tmux session |
| `--sim`, `--speed` | | simulator; `--speed 3` for a quick run-through |
| `--dash-height` | `14` | dashboard rows; add one per burst slot beyond 10 |

`burstlab-demo dash` runs just the dashboard in the current terminal. In the simulator, click
the dashboard and use **space** (pause), **→** (skip 15 s), **r** (restart). **q** quits.

## Layout

```
main.go                 CLI: up | dash | push-jobs
internal/source/        live (ssh + EC2) and simulated snapshot sources
internal/ui/            Bubble Tea dashboard
internal/launch/        tmux layout
jobs/                   demo job scripts (embedded; installed by push-jobs)
scripts/                record-demo.sh, speedup-cast.py, render-mp4.sh
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).
