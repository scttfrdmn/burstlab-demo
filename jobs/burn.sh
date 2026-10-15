#!/bin/bash
# burn.sh SECONDS — keep every CPU Slurm gave this job busy for SECONDS.
# sleep-based demo jobs show zero load; this makes the LOAD bars real.
secs=${1:-60}
n=${SLURM_CPUS_PER_TASK:-${SLURM_CPUS_ON_NODE:-$(nproc)}}
echo "$(hostname): burning $n CPUs for ${secs}s (job ${SLURM_JOB_ID:-?})"
for _ in $(seq "$n"); do
  timeout "$secs" sh -c 'while :; do :; done' &
done
wait
echo "$(hostname): done"
