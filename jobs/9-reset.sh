#!/bin/bash
# Cancel all of alice's demo jobs. Burst nodes then idle out after SuspendTime.
export PATH=/opt/slurm/bin:$PATH SLURM_CONF=${SLURM_CONF:-/opt/slurm/etc/slurm.conf}
# one name per call: scancel --name takes a single job name
for name in clog burst warm; do
  scancel --user="$USER" --name="$name"
done
sleep 1
squeue --user="$USER"
