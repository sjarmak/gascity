#!/usr/bin/env bash
# triage.sh — read-only first-move snapshot for debugging the ds-research
# Gas City (city root /home/ds/gas-city). Part of cityops-debugging-playbook.
# Mutates NOTHING: no restarts, no kills, no bead writes. Safe in a guest
# session. Every gc call is wrapped in `timeout` because gc itself hanging is
# one of the symptoms this snapshot exists to detect.
set -u

CITY=/home/ds/gas-city
INFO="$CITY/.beads/dolt/.dolt/sql-server.info"
STATE="$CITY/.gc/runtime/packs/dolt/dolt-state.json"

echo "== city triage snapshot $(date -Is) =="

echo
echo "-- supervisor --"
systemctl --user is-active gascity-supervisor.service
systemctl --user show gascity-supervisor.service \
  -p ActiveEnterTimestamp -p MemoryCurrent -p MemoryPeak -p NRestarts 2>/dev/null
oom_count=$(journalctl --user -u gascity-supervisor 2>/dev/null | grep -c "oom-kill")
echo "oom-kills in journal (all time): $oom_count"
journalctl --user -u gascity-supervisor 2>/dev/null | grep "oom-kill" | tail -3

echo
echo "-- dolt ground truth (pid:port:uuid, then gc's state view) --"
cat "$INFO" 2>/dev/null && echo || echo "!! $INFO missing"
cat "$STATE" 2>/dev/null || echo "!! $STATE missing"
if [ -r "$INFO" ]; then
  pid=$(cut -d: -f1 "$INFO"); port=$(cut -d: -f2 "$INFO")
  [ -d "/proc/$pid" ] && echo "canonical pid $pid: alive" || echo "!! canonical pid $pid: DEAD"
  ss -tln 2>/dev/null | grep -q "127.0.0.1:$port " \
    && echo "port $port: listening" || echo "!! port $port: NOT listening"
  timeout 10 dolt --host 127.0.0.1 --port "$port" --user root --no-tls \
    --password '' sql -q "SELECT 1;" >/dev/null 2>&1 \
    && echo "TCP SELECT 1: ok" || echo "!! TCP SELECT 1: FAILED"
fi
echo "every dolt sql-server on the host (canonical = --config under $CITY):"
pgrep -af "dolt sql-server" || echo "none found"

echo
echo "-- tmux + sessions --"
n_tmux=$(tmux -L ds-research list-sessions 2>/dev/null | wc -l)
echo "tmux sessions on socket ds-research: $n_tmux (0 = dead socket; see tmux-supervisor.md)"
echo "gc session states (45s timeout; a hang here is itself a finding):"
timeout 45 gc --city "$CITY" session list 2>&1 | awk 'NR>1 {print $3}' | sort | uniq -c \
  || echo "!! gc session list timed out or failed"

echo
echo "-- load + known runaway classes --"
uptime
echo "gc-nudge-poll sidecars: $(pgrep -fc "gc nudge poll" || true) (healthy poll ~0% CPU; runaways are the gc-b9w88 class)"
echo "top CPU consumers:"
ps -eo pcpu,pid,etimes,comm --sort=-pcpu | head -6

echo
echo "-- supervisor log tail (~/.gc/supervisor.log) --"
tail -5 "$HOME/.gc/supervisor.log" 2>/dev/null
echo "known bad signatures in last 500 lines:"
tail -500 "$HOME/.gc/supervisor.log" 2>/dev/null \
  | grep -cE "tmux state cache: refresh failed|session name already exists" \
  | sed 's/^/  matches: /'

echo
echo "== end snapshot =="
