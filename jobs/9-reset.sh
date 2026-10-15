#!/bin/bash
# Cancel all of alice's demo jobs. Burst nodes then idle out after SuspendTime.
export PATH=/opt/slurm/bin:$PATH SLURM_CONF=${SLURM_CONF:-/opt/slurm/etc/slurm.conf}
mkdir -p "$HOME/demo/out"
scancel --user="$USER" --name=clog,burst,warm
squeue
