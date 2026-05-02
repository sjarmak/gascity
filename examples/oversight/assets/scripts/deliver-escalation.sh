#!/usr/bin/env bash
#
# deliver-escalation.sh — bridge bead-based escalations to the extmsg outbound API.
#
# Watches for open beads with label:escalation that are not yet labeled
# label:delivered, POSTs each one to the city's extmsg outbound endpoint,
# then labels them delivered. Idempotent — safe to retry.
#
# Required environment (set by the order runtime or the meta city.toml):
#   GC_API_BASE_URL   e.g. http://127.0.0.1:7777
#   GC_CITY_NAME      the meta city name (matches [workspace].name)
#   GC_OVERSIGHT_SESSION_ID  the chief-of-staff session id, pre-bound to a
#                             conversation via POST /v0/city/{city}/extmsg/bind
#
# This script does not create the binding — that is a one-time operator
# action. See examples/oversight/sample-meta-city.toml for instructions.
#
# Rationale: the v0 SDK has no `gc extmsg send` CLI; outbound is HTTP-only
# (POST /v0/city/{city}/extmsg/outbound). This script encapsulates the
# curl call so the formula author does not have to know the API shape.

set -euo pipefail

: "${GC_API_BASE_URL:?GC_API_BASE_URL must be set}"
: "${GC_CITY_NAME:?GC_CITY_NAME must be set}"
: "${GC_OVERSIGHT_SESSION_ID:?GC_OVERSIGHT_SESSION_ID must be set}"

api="${GC_API_BASE_URL%/}/v0/city/${GC_CITY_NAME}/extmsg/outbound"

# List open escalation beads that have not yet been delivered.
# We intentionally use the gc CLI (not the API) for bead queries so this
# script keeps working if the bead store moves behind a different API
# version. The script is forward-compatible with bd JSON shape changes
# because it only reads .id and .title.
mapfile -t bead_ids < <(
  gc bd list --label escalation --status open --json \
    | jq -r '.[] | select((.labels // []) | index("delivered") | not) | .id'
)

if [[ ${#bead_ids[@]} -eq 0 ]]; then
  exit 0
fi

for id in "${bead_ids[@]}"; do
  # gc bd show --json returns an array; first element is the bead.
  bead_json=$(gc bd show "$id" --json)
  title=$(jq -r '.[0].title' <<<"$bead_json")
  body=$(jq -r '.[0].description // ""' <<<"$bead_json")
  text=$(printf '*%s*\n\n%s\n\n_(bead %s)_' "$title" "$body" "$id")

  # Idempotency key uses the bead id so duplicate posts are deduped by
  # the extmsg adapter.
  payload=$(jq -n \
    --arg sid "$GC_OVERSIGHT_SESSION_ID" \
    --arg text "$text" \
    --arg key "escalation-$id" \
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
    # Do not label as delivered — let the next tick try again. The
    # idempotency_key on the API side prevents double delivery if the
    # POST actually succeeded but the response was lost.
  fi
done
