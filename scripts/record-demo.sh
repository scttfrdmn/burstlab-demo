#!/bin/bash
# record-demo.sh — record the full live demo with asciinema.
#
# Opens the burstlab-demo layout in a fresh tmux session at 140×40 under
# `asciinema rec --headless`, then plays presenter: types each stage into the
# shell pane (at typing speed) and waits for the dashboard to show its
# outcome before moving on. Ends once every burst node has terminated.
#
# Output (in recordings/):
#   burstlab-demo.cast          real time (~20 min)
#   burstlab-demo.markers       seconds-into-recording of each milestone
#
# Usage: AWS_PROFILE=aws scripts/record-demo.sh [extra burstlab-demo flags]
# Then:  scripts/speedup-cast.py recordings/burstlab-demo.cast   (see that file)
set -euo pipefail
cd "$(dirname "$0")/.."
: "${AWS_PROFILE:?set AWS_PROFILE}"
SESSION=burstlab-rec
OUT=recordings/burstlab-demo
mkdir -p recordings
go build -o bin/burstlab-demo .

markers=$OUT.markers; : > "$markers"
t0=0
mark() { echo "$(( $(date +%s) - t0 )) $*" | tee -a "$markers"; }

# Wait (up to $2 s, default 30 min) until the dashboard pane matches regex $1.
D= S=
screen() { tmux capture-pane -p -t "$D"; }
wait_for() {
  local deadline=$(( $(date +%s) + ${2:-1800} ))
  until screen | grep -Eq "$1"; do
    [ "$(date +%s)" -lt "$deadline" ] || { echo "timed out waiting for: $1" >&2; exit 1; }
    sleep 1
  done
}
count() { screen | grep -Ec "$1" || true; }

# Type a command into the shell pane like a person would.
type_cmd() {
  local s=$1 i
  for (( i=0; i<${#s}; i++ )); do
    tmux send-keys -t "$S" -l "${s:i:1}"; sleep 0.06
  done
  sleep 0.6; tmux send-keys -t "$S" Enter
}

driver() {
  # wait for the session the recorder launches
  until tmux has-session -t "=$SESSION" 2>/dev/null; do sleep 0.2; done
  D=$(tmux list-panes -t "$SESSION" -F '#{pane_id}' | head -1)
  S=$(tmux list-panes -t "$SESSION" -F '#{pane_id}' | tail -1)
  t0=$(date +%s)
  wait_for ' (LIVE|SIM) ' 60;           sleep 1
  case " $* " in *" --sim "*) ;; *)   # simulator shell is local, not alice@head
    until tmux capture-pane -p -t "$S" | grep -q 'alice@'; do sleep 0.5; done ;; esac
  sleep 4;  type_cmd 'clear'
  sleep 2;  type_cmd '~/demo/9-reset.sh';                        sleep 5
  type_cmd 'clear'; sleep 1
  mark "stage 1: clog";        type_cmd '~/demo/1-clog.sh'
  until [ "$(count 'compute0. +● running +alloc .* 8/8')" -ge 4 ]; do sleep 1; done
  mark "on-prem full";         sleep 15
  mark "stage 2: burst";       type_cmd '~/demo/2-burst.sh'
  wait_for 'powering up' 120;  mark "powering up"
  until [ "$(count 'aws-burst-. ● running +alloc .* 8/8')" -ge 4 ]; do sleep 1; done
  mark "burst nodes alloc"
  until [ "$(count 'idle ⏲')" -ge 4 ]; do sleep 1; done
  mark "idle countdown";       sleep 30
  mark "stage 3: warm";        type_cmd '~/demo/3-warm.sh'
  wait_for 'warm +R' 120;      mark "warm job running"
  until [ "$(count 'terminating|terminated')" -ge 1 ]; do sleep 2; done
  mark "stage 4: scale to zero"
  until [ "$(count '○ terminated')" -ge 4 ]; do sleep 2; done
  mark "all terminated";       sleep 12
  tmux kill-session -t "=$SESSION"     # ends the recording
}

# a failed wait must end the recording too, not leave it running
( trap 'tmux kill-session -t "=$SESSION" 2>/dev/null || true' EXIT; driver "$@" ) & drv=$!
asciinema rec --headless --window-size 140x40 --overwrite --title "BurstLab cloud bursting demo" \
  -c "env AWS_PROFILE=$AWS_PROFILE $PWD/bin/burstlab-demo up --session $SESSION --fresh $*" "$OUT.cast"
wait "$drv" || { echo "driver failed — recording is partial" >&2; exit 1; }
echo "recorded $OUT.cast"; cat "$markers"
