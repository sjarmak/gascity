#!/usr/bin/env bash
#
# deliver-rollup.sh — POST severity:escalate rollup beads to extmsg
# and mark them delivered. Idempotent.
#
# Required environment (set in city.toml or shell before `gc start`):
#   GC_API_BASE_URL              e.g. http://127.0.0.1:7777
#   GC_CITY_NAME                 the city name (matches [workspace].name)
#   GC_OVERSIGHT_SESSION_ID      session id pre-bound to a conversation
#                                via POST /v0/city/{city}/extmsg/bind
#
# Why this script (not a CLI): the v0 SDK has no `gc extmsg send`
# command. Outbound is HTTP-only. This script encapsulates the curl so
# the rest of the pack stays declarative.
#
# The mapping from rollup bead → extmsg payload preserves enough
# context that the inbound reply path can find the right bead:
#   - Title becomes the message header
#   - Description's "Smallest ask" line becomes the call-to-action
#   - Bead id is included as `in_reply_to_bead` metadata in the
#     idempotency_key so the chief-of-staff can route human replies
#     back to the right escalation.

set -euo pipefail

: "${GC_API_BASE_URL:?GC_API_BASE_URL must be set}"
: "${GC_CITY_NAME:?GC_CITY_NAME must be set}"
: "${GC_OVERSIGHT_SESSION_ID:?GC_OVERSIGHT_SESSION_ID must be set}"

api="${GC_API_BASE_URL%/}/v0/city/${GC_CITY_NAME}/extmsg/outbound"

mapfile -t bead_ids < <(
  gc bd list --label rollup --label severity:escalate --status open --json \
    | jq -r '.[] | select((.labels // []) | index("delivered") | not) | .id'
)

if [[ ${#bead_ids[@]} -eq 0 ]]; then
  exit 0
fi

for id in "${bead_ids[@]}"; do
  bead_json=$(gc bd show "$id" --json)
  title=$(jq -r '.[0].title' <<<"$bead_json")
  body=$(jq -r '.[0].description // ""' <<<"$bead_json")
  rig=$(jq -r '.[0].labels[] | select(startswith("rig:")) | sub("^rig:"; "")' <<<"$bead_json" | head -1)

  text=$(printf '*%s*\n\n%s\n\n_(rollup bead %s · rig: %s)_\n_Reply to this message to respond to %s._' \
    "$title" "$body" "$id" "$rig" "$id")

  payload=$(jq -n \
    --arg sid "$GC_OVERSIGHT_SESSION_ID" \
    --arg text "$text" \
    --arg key "rollup-$id" \
    '{session_id: $sid, text: $text, idempotency_key: $key}')

  if curl --silent --show-error --fail \
       --max-time 30 \
       --header "Content-Type: application/json" \
       --data "$payload" \
       "$api" >/dev/null; then
    gc bd update "$id" --add-label delivered
    echo "delivered $id"
  else
    echo "delivery failed for $id; will retry next tick" >&2
  fi
done
