#!/bin/bash
# Stage 2 — submit more work while on-prem is full.
#
# The only change for the user is --partition=local,aws: Slurm starts each
# job wherever it can start soonest. on-prem is full, so these land on the
# powered-off aws-burst-* nodes, and slurmctld calls resume.py →
# EC2 CreateFleet. Expect ~2-3 minutes for instances to boot and slurmd to
# register (watch: powering up → alloc).
export PATH=/opt/slurm/bin:$PATH SLURM_CONF=${SLURM_CONF:-/opt/slurm/etc/slurm.conf}
mkdir -p "$HOME/demo/out"
cd "$(dirname "$0")"
sbatch --partition=local,aws --job-name=burst \
  --array=0-$(( ${BURST_NODES:-4} - 1 )) --cpus-per-task=8 --mem-per-cpu=1G --time=20:00 \
  --output="$HOME/demo/out/burst-%A_%a.out" \
  burn.sh "${BURST_SECONDS:-120}"
