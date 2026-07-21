#!/usr/bin/env bash
# csu_pick.sh: pick the best Claude account based on csu utilization data.
#
# Reads ~/.claude-usage/usage_cache.json (refreshing via `csu` if older than
# CSU_PICK_MAX_AGE_MIN minutes). Prints the chosen account number to stdout.
#
# Selection rule (mechanical):
#   1. Skip accounts where 7d utilization >= 95% (treated as exhausted).
#   2. Skip accounts listed in CSU_PICK_EXCLUDE (comma-separated names or
#      account numbers, e.g. "claude-2" or "2").
#   3. Rot-preference: if any remaining account's OAuth token is still
#      valid but within CSU_PICK_EXPIRY_PRESSURE_HOURS of expiring, route
#      this launch to the most-urgent one (least hours left). A real
#      session landing there refreshes the token before it rots. This is
#      the keepalive that stops a low-traffic account silently expiring.
#   4. Otherwise: sort remaining by (7d_util, 5h_util, account_num)
#      ascending and pick uniformly at random from the top CSU_PICK_TOP_K.
#      Default top-K=2 spreads burst spawns across the two least-loaded
#      accounts instead of herding onto a single one (csu cache only
#      refreshes every ~15min, so without this every concurrent spawn
#      would see the same "best" account and pick it).
#
# Fallback: if the cache is unreadable or no candidate qualifies, prints
# CSU_PICK_FALLBACK (default "1") so callers always get a valid account.
#
# Tunables (env):
#   CSU_PICK_CACHE      - path to cache (default ~/.claude-usage/usage_cache.json)
#   CSU_PICK_MAX_AGE_MIN - refresh if oldest fetched_at > this many min (default 15)
#   CSU_PICK_EXHAUSTED_PCT - 7d util threshold to skip (default 95)
#   CSU_PICK_EXCLUDE    - comma-separated names/numbers to skip (default: none)
#   CSU_PICK_FALLBACK   - account number returned on error (default "1")
#   CSU_PICK_REFRESH_CMD - command to refresh cache (default "csu")
#   CSU_PICK_TOP_K      - number of top candidates to randomize among (default 2)
#   CSU_PICK_EXPIRY_PRESSURE_HOURS - prefer an account whose token expires
#                         within this many hours, to keep it warm (default 2)
#
# Designed for use from `claude-account auto`.

set -euo pipefail

CACHE="${CSU_PICK_CACHE:-$HOME/.claude-usage/usage_cache.json}"
MAX_AGE_MIN="${CSU_PICK_MAX_AGE_MIN:-15}"
EXHAUSTED_PCT="${CSU_PICK_EXHAUSTED_PCT:-95}"
EXCLUDE="${CSU_PICK_EXCLUDE-}"
FALLBACK="${CSU_PICK_FALLBACK:-1}"
REFRESH_CMD="${CSU_PICK_REFRESH_CMD:-csu}"
TOP_K="${CSU_PICK_TOP_K:-2}"
PRESSURE_H="${CSU_PICK_EXPIRY_PRESSURE_HOURS:-2}"

# Refresh cache if missing or stale.
needs_refresh=0
if [ ! -f "$CACHE" ]; then
  needs_refresh=1
elif command -v python3 >/dev/null 2>&1; then
  age_min=$(python3 - "$CACHE" "$MAX_AGE_MIN" <<'PYEOF' 2>/dev/null || echo 999
import json, sys, datetime
path, max_age = sys.argv[1], int(sys.argv[2])
try:
    with open(path) as f:
        d = json.load(f)
    fetched = [a.get('fetched_at') for a in d.get('accounts', []) if a.get('fetched_at')]
    if not fetched:
        print(999); sys.exit(0)
    oldest = min(fetched)
    dt = datetime.datetime.fromisoformat(oldest.replace('Z', '+00:00'))
    now = datetime.datetime.now(datetime.timezone.utc)
    age = (now - dt).total_seconds() / 60
    print(int(age))
except Exception:
    print(999)
PYEOF
)
  if [ "$age_min" -gt "$MAX_AGE_MIN" ]; then
    needs_refresh=1
  fi
fi

if [ "$needs_refresh" = "1" ] && command -v "$REFRESH_CMD" >/dev/null 2>&1; then
  "$REFRESH_CMD" >/dev/null 2>&1 || true
fi

# Pick the best account.
if [ ! -f "$CACHE" ] || ! command -v python3 >/dev/null 2>&1; then
  echo "$FALLBACK"
  exit 0
fi

python3 - "$CACHE" "$EXHAUSTED_PCT" "$EXCLUDE" "$FALLBACK" "$TOP_K" "$PRESSURE_H" <<'PYEOF'
import json, random, sys
from datetime import datetime, timezone

path, exhausted_pct_s, exclude_s, fallback, top_k_s, pressure_h_s = sys.argv[1:7]
exhausted_pct = float(exhausted_pct_s)
exclude = {e.strip() for e in exclude_s.split(',') if e.strip()}
top_k = max(1, int(top_k_s))
pressure_h = float(pressure_h_s)

def excluded(name):
    if name in exclude:
        return True
    # Accept "claude-2" matching "2" and vice versa.
    if name.startswith('claude-') and name.split('-', 1)[1] in exclude:
        return True
    return False

def hours_to_expiry(a):
    """Hours until this account's OAuth token expires, or None if unknown."""
    iso = a.get('token_expires_at')
    if not iso:
        return None
    try:
        dt = datetime.fromisoformat(iso.replace('Z', '+00:00'))
        return (dt - datetime.now(timezone.utc)).total_seconds() / 3600
    except Exception:
        return None

try:
    with open(path) as f:
        data = json.load(f)
    candidates = []
    for a in data.get('accounts', []):
        name = a.get('name', '')
        if not name.startswith('account'):
            continue
        num = name[len('account'):]
        if not num.isdigit():
            continue
        # Match exclusion against the canonical claude-N name.
        if excluded(f'claude-{num}') or excluded(num):
            continue
        seven = (a.get('seven_day') or {}).get('utilization')
        five = (a.get('five_hour') or {}).get('utilization')
        if seven is None:
            continue
        if seven >= exhausted_pct:
            continue
        candidates.append({
            'num': int(num),
            'seven': float(seven),
            'five': float(five or 0),
            'hte': hours_to_expiry(a),
        })
    if not candidates:
        print(fallback)
        sys.exit(0)
    # Rot-preference: if any candidate's token is still valid but within
    # pressure_h of expiring, route this launch there so a real session
    # refreshes it before it rots. Most-urgent (least hours left) wins,
    # ties broken by account number. Deterministic override of the
    # load-based pick — safe because exhausted accounts are already
    # filtered out and every account has Max-tier headroom.
    rotting = [c for c in candidates
               if c['hte'] is not None and 0 < c['hte'] <= pressure_h]
    if rotting:
        rotting.sort(key=lambda c: (c['hte'], c['num']))
        print(rotting[0]['num'])
        sys.exit(0)
    # Otherwise: least-loaded pick, randomized across the top-K coolest.
    candidates.sort(key=lambda c: (c['seven'], c['five'], c['num']))
    pool = candidates[:min(top_k, len(candidates))]
    print(random.choice(pool)['num'])
except Exception:
    print(fallback)
PYEOF
