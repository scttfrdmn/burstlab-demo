#!/bin/bash
# Stage 1 — clog the on-prem cluster.
#
# 24 tasks × 4 CPUs onto the 'local' partition only. 4 nodes × 8 CPUs = 32,
# so 8 run (2 per node) and 16 pile up as Pending (Resources). They run for
# 30 minutes so on-prem stays full for the whole demo.
export PATH=/opt/slurm/bin:$PATH SLURM_CONF=${SLURM_CONF:-/opt/slurm/etc/slurm.conf}
mkdir -p "$HOME/demo/out"
cd "$(dirname "$0")"
sbatch --partition=local --job-name=clog \
  --array=0-23 --cpus-per-task=4 --mem-per-cpu=1G --time=40:00 \
  --output="$HOME/demo/out/clog-%A_%a.out" \
  burn.sh "${CLOG_SECONDS:-1800}"
