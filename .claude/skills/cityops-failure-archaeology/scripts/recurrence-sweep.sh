#!/usr/bin/env bash
# recurrence-sweep.sh — read-only sweep for known ds-research failure signatures.
# Part of the cityops-failure-archaeology skill. Prints ok/FLAG lines; mutates
# NOTHING, kills NOTHING. Every check maps to a catalogued incident — see
# SKILL.md for the archaeology behind each signature.
set -u

CITY=/home/ds/gas-city
FLAGS=0
flag() { FLAGS=$((FLAGS + 1)); printf 'FLAG  %s\n' "$*"; }
ok()   { printf 'ok    %s\n' "$*"; }

echo "== cityops recurrence sweep — $(date -Is) =="

# --- 1. Canonical dolt ground truth (sql-server.info is authoritative) ------
INFO="$CITY/.beads/dolt/.dolt/sql-server.info"
PID="" PORT=""
if [[ -f "$INFO" ]]; then
  PID=$(cut -d: -f1 "$INFO"); PORT=$(cut -d: -f2 "$INFO")
  if [[ -d "/proc/$PID" ]]; then
    ok "canonical dolt PID $PID alive (port $PORT per sql-server.info)"
  else
    flag "sql-server.info PID $PID is DEAD — server down or info stale"
  fi
  if ss -tln 2>/dev/null | grep -q "127.0.0.1:$PORT "; then
    ok "port $PORT listening on 127.0.0.1"
  else
    flag "nothing listening on canonical port $PORT"
  fi
else
  flag "missing $INFO — canonical dolt server not running/initialized"
fi

# --- 2. Poisoned dolt-state.json (2026-04-09 signature) ---------------------
STATE="$CITY/.gc/runtime/packs/dolt/dolt-state.json"
if [[ -f "$STATE" ]]; then
  DD=$(jq -r '.data_dir // empty' "$STATE" 2>/dev/null)
  case "$DD" in
    "$CITY/.beads/dolt") ok "dolt-state.json data_dir is canonical" ;;
    /tmp/*) flag "dolt-state.json data_dir=$DD — POISONED test artifact (Apr-09 signature). A 'gc dolt start' now could kill -9 the healthy server. Quarantine the file first." ;;
    *) flag "dolt-state.json data_dir=$DD — not the canonical path" ;;
  esac
else
  flag "missing $STATE"
fi

# --- 3. Non-canonical dolt sql-servers (ghost / leak signature) -------------
NONCANON=$(pgrep -af "dolt sql-server" 2>/dev/null | grep -v "recurrence-sweep" \
  | grep -vF "$CITY/.gc/runtime/packs/dolt/dolt-config.yaml" || true)
if [[ -n "$NONCANON" ]]; then
  N=$(printf '%s\n' "$NONCANON" | wc -l)
  flag "$N non-canonical dolt sql-server proc(s) — stale-read hazard. Do NOT kill without checking --config path AND cwd (the canonical server may be among lookalikes):"
  printf '%s\n' "$NONCANON" | sed 's/^/      /'
else
  ok "no non-canonical dolt sql-server processes"
fi

# --- 4. Ghost check: canonical server holding deleted dolt storage FDs ------
if [[ -n "$PID" && -d "/proc/$PID" ]]; then
  DELETED=$(readlink "/proc/$PID"/fd/* 2>/dev/null | grep -c '\.dolt.*(deleted)' || true)
  if [[ "${DELETED:-0}" -gt 0 ]]; then
    flag "canonical dolt PID $PID holds $DELETED deleted .dolt file(s) — ghost-server signature; it may be serving a stale snapshot"
  else
    ok "canonical dolt holds no deleted .dolt storage FDs"
  fi
fi

# --- 5. Runaway 'gc nudge poll' hot-loops (2026-06-24 / 2026-07-06) ---------
RUNAWAYS=$(ps -eo pid,pcpu,etimes,args --no-headers 2>/dev/null \
  | awk '$0 ~ /gc nudge poll/ && $0 !~ /awk/ && $2 > 50 && $3 > 60' || true)
if [[ -n "$RUNAWAYS" ]]; then
  flag "runaway 'gc nudge poll' proc(s) >50% CPU >60s — floor-down precursor; nudge-poll-reaper should catch these within 2m, verify it is firing:"
  printf '%s\n' "$RUNAWAYS" | sed 's/^/      /'
else
  ok "no runaway gc-nudge-poll processes"
fi

# --- 6. Supervisor health + CPU (pegged-supervisor signature) ---------------
SUP_STATE=$(systemctl --user is-active gascity-supervisor 2>/dev/null || true)
SUP_PID=$(systemctl --user show gascity-supervisor -p MainPID --value 2>/dev/null || true)
if [[ "$SUP_STATE" == "active" && -n "$SUP_PID" && "$SUP_PID" != "0" ]]; then
  SUP_CPU=$(ps -o pcpu= -p "$SUP_PID" 2>/dev/null | tr -d ' ' || echo 0)
  if awk -v c="${SUP_CPU:-0}" 'BEGIN{exit !(c>200)}'; then
    flag "supervisor PID $SUP_PID at ${SUP_CPU}% CPU — pegged-supervisor signature (spawn storm / demand-computation stall)"
  else
    ok "supervisor active, PID $SUP_PID, ${SUP_CPU}% CPU"
  fi
else
  flag "gascity-supervisor state=$SUP_STATE MainPID=${SUP_PID:-?}"
fi

# --- 7. Recent supervisor stops (stop-catcher archive) -----------------------
if [[ -f /tmp/supervisor-stop-caller.log ]]; then
  RECENT=$(grep -o '=== [0-9T:+-]* STOP' /tmp/supervisor-stop-caller.log 2>/dev/null | tail -3 | awk '{print $2}')
  STOPS=$(grep -c 'STOP triggered' /tmp/supervisor-stop-caller.log 2>/dev/null || echo 0)
  ok "supervisor stop-catcher: $STOPS total captures; last 3: $(echo $RECENT | tr '\n' ' ')"
else
  ok "no /tmp/supervisor-stop-caller.log — no captured stops since last /tmp clean"
fi

# --- 8. Order-firing floor proxy: any order log written recently? -----------
RECENT_LOGS=$(find "$CITY/.gc" -maxdepth 1 -name '*.log' -mmin -30 2>/dev/null | wc -l)
if [[ "$RECENT_LOGS" -eq 0 ]]; then
  flag "no order log under $CITY/.gc/ written in 30m — order-firing floor may be dark (2026-07-06 signature)"
else
  ok "$RECENT_LOGS order log(s) written in the last 30m — floor is firing"
fi

# --- 9. gascity-main pin (binary-freeze signature, 2026-05-25) ---------------
HEAD_REF=$(git -C /home/ds/gascity-main symbolic-ref -q HEAD 2>/dev/null || true)
if [[ "$HEAD_REF" == "refs/heads/main" ]]; then
  ok "/home/ds/gascity-main is on main — gcsync can rebuild"
else
  flag "/home/ds/gascity-main HEAD=$HEAD_REF (not main) — gcsync silently refuses; installed gc binary is FROZEN until pin-guard or a human restores main"
fi

# --- 10. Drop-in port vs live port (gc-74rxa adopt-path stopgap) -------------
DROPIN=/home/ds/.config/systemd/user/gascity-supervisor.service.d/10-dolt-port.conf
if [[ -f "$DROPIN" && -n "$PORT" ]]; then
  DPORT=$(grep -o 'GC_DOLT_PORT=[0-9]*' "$DROPIN" | head -1 | cut -d= -f2)
  if [[ "$DPORT" == "$PORT" ]]; then
    ok "10-dolt-port.conf ($DPORT) matches live port ($PORT)"
  else
    flag "10-dolt-port.conf says $DPORT but live port is $PORT — supervisor bd queries will resolve the WRONG port after its next restart (gc-74rxa)"
  fi
fi

# --- 11. Load sanity ----------------------------------------------------------
read -r L1 _ < /proc/loadavg
NCPU=$(nproc)
if awk -v l="$L1" -v n="$NCPU" 'BEGIN{exit !(l > n*1.5)}'; then
  flag "loadavg $L1 > 1.5x $NCPU cores — check for runaway procs (item 5) and spawn storms"
else
  ok "loadavg $L1 on $NCPU cores"
fi

echo "== sweep done: $FLAGS flag(s) =="
exit 0
