#!/usr/bin/env bash
# capacity-snapshot.sh — read-only capacity dashboard for the ds-research city.
# Prints every scattered capacity fact in one pass. Makes NO changes.
# Safe to run any time; touches no gc state, no dolt, no beads.
set -u
CITY=/home/ds/gas-city

echo "== capacity snapshot: $(date -u +%Y-%m-%dT%H:%M:%SZ) =="

echo
echo "-- disk (root) --"
df -h / | tail -1

echo
echo "-- memory --"
free -h | sed -n '1,2p'

echo
echo "-- supervisor cgroup (memory/CPU) --"
systemctl --user status gascity-supervisor --no-pager 2>/dev/null \
  | grep -E "Active:|Memory:|CPU:" || echo "(supervisor status unavailable)"

echo
echo "-- wake budget ([daemon] in city.toml) --"
grep -E "^(patrol_interval|max_wakes_per_tick|max_restarts|restart_window)" "$CITY/city.toml"

echo
echo "-- pool floors/ceilings (agents/*/agent.toml) --"
grep -H -E "^(min|max)_active_sessions" "$CITY"/agents/*/agent.toml \
  | sed "s|$CITY/agents/||; s|/agent.toml||" | sort

echo
echo "-- runtime rig suspension state (wins over city.toml suspended_on_start) --"
cat "$CITY/.gc/runtime/suspension-state.json" 2>/dev/null \
  || echo "(no suspension-state.json — city.toml declarations apply)"

echo
echo "-- order overrides in city.toml (paused orders) --"
grep -A2 "\[\[orders.overrides\]\]" "$CITY/city.toml" || echo "(none)"

echo
echo "-- unreaped growth files --"
du -sh "$CITY/.gc/beads.json" "$CITY"/.gc/beads.json.bak-janitor-* \
  "$CITY/.beads/backup" 2>/dev/null

echo
echo "-- live tmux session count (socket ds-research) --"
tmux -L ds-research list-sessions 2>/dev/null | wc -l
