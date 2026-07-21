#!/usr/bin/env bash
# ⚠️ SUPERSEDED 2026-07-10 (gc-466214, mayor-approved): replaced by the durable
# systemd user timer `gascity-nudge-poll-reaper.timer` (2m, Persistent, no 12h
# cap, order-firing-independent) which runs bin/nudge-poll-reaper directly.
# Do NOT re-launch this self-expiring 12h bridge — its expiry is exactly how
# runaways recurred (563%+438% CPU on 2026-07-10). Kept for incident history.
#
# STOPGAP bridge (2026-07-06 incident): the order-firing floor is down because
# the supervisor demand-computation stalls (ComputePoolDesiredStates/session-
# snapshot O(n^2), order_dispatch.go:624-625), so the auto-firing nudge-poll-reaper
# can't run and runaway `gc nudge poll` procs re-form ~every 30min and re-peg CPU.
# This loop runs the reaper directly (bypasses the broken demand loop) every 20min.
# REMOVE once the gascity source fix (pprof ComputePoolDesiredStates) lands and
# order-firing recovers.  Max lifetime 12h as a safety stop.
set -u
CITY=/home/ds/gas-city
LOG="$CITY/.gc/nudge-poll-runaway-watcher.log"
DEADLINE=$(( $(date +%s) + 12*3600 ))
echo "$(date -Is) watcher-start pid=$$" >> "$LOG"
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  # belt-and-suspenders: reaper order run + explicit kill of high-CPU aged pollers
  out=$(cd "$CITY" && timeout 60 gc order run nudge-poll-reaper 2>&1 | tail -1)
  killed=""
  while read -r pid cpu etime _; do
    [ -z "${pid:-}" ] && continue
    kill -TERM "$pid" 2>/dev/null && killed="$killed $pid(${cpu}%)"
  done < <(ps -eo pid,%cpu,etime,cmd --no-headers 2>/dev/null \
            | grep 'gc nudge poll --city' | grep -v grep \
            | awk '$2>=80{print $1,$2,$3}')
  echo "$(date -Is) reaper='$out' extra_killed='${killed:- none}'" >> "$LOG"
  sleep 300
done
echo "$(date -Is) watcher-exit (deadline)" >> "$LOG"
