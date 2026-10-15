#!/bin/bash
# Stage 3 — run this while the burst nodes sit idle (the ⏲ countdown).
#
# The burst nodes are still up waiting out SuspendTime, so this job starts
# in seconds — no EC2 launch, no boot. Once they've been idle for SuspendTime
# the plugin terminates them and the next burst pays the boot cost again.
export PATH=/opt/slurm/bin:$PATH SLURM_CONF=${SLURM_CONF:-/opt/slurm/etc/slurm.conf}
mkdir -p "$HOME/demo/out"
cd "$(dirname "$0")"
sbatch --partition=local,aws --job-name=warm \
  --cpus-per-task=8 --mem-per-cpu=1G --time=10:00 \
  --output="$HOME/demo/out/warm-%j.out" \
  burn.sh "${WARM_SECONDS:-60}"
