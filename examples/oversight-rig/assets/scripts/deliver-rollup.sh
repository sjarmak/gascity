#!/usr/bin/env bash
#
# deliver-rollup.sh — POST severity:escalate rollup beads to extmsg
# and mark them delivered. Idempotent.
#
# Routing model
# -------------
#
# Each rollup bead carries a ``rig:<name>`` label. The script tries to
# deliver to that rig's own bound conversation first, falling back to a
# city-wide oversight DM if the rig has no per-rig binding yet.
#
# Resolution order (per bead, in priority order):
#
#   1. Rig-specific channel: locate the project-lead session for the
#      bead's ``rig:`` label, then read its most recent active extmsg
#      binding (created by ``gc slack bind-room`` or equivalent).
#      Publish through gc's ``/extmsg/outbound`` using that session +
#      conversation, so peer fanout fires for the other bound peers.
#
#   2. City-wide fallback: if the rig has no project-lead session, no
#      active binding, or no rig label, fall back to the legacy
#      ``GC_OVERSIGHT_*`` env vars (single DM for all rigs).
#
# Required environment
# --------------------
#
#   GC_API_BASE_URL              e.g. http://127.0.0.1:8372
#   GC_CITY_NAME                 the city name (matches [workspace].name)
#
# Required for the city-wide fallback only (no per-rig binding):
#
#   GC_OVERSIGHT_SESSION_ID      session id pre-bound to a conversation
#                                via POST /v0/city/{city}/extmsg/bind
#   GC_OVERSIGHT_PROVIDER        e.g. "slack"
#   GC_OVERSIGHT_ACCOUNT_ID      e.g. workspace id "T0B17700WUW"
#   GC_OVERSIGHT_CONVERSATION_ID e.g. DM channel "D0B0TTS550F"
#   GC_OVERSIGHT_KIND            "dm" | "room" | "thread"
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

api="${GC_API_BASE_URL%/}/v0/city/${GC_CITY_NAME}/extmsg/outbound"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
resolver="${script_dir}/resolve_rig_channel.py"

# resolve_target <rig>
#
# Stdout: JSON {session_id, conversation: {...}} on success.
# Returns the resolver's exit code:
#   0 — resolved per-rig channel (caller uses stdout)
#   2 — no project-lead session for this rig (caller falls back)
#   3 — project-lead exists but no active binding (caller falls back)
#   1 — resolver hit an error (caller logs and skips this bead)
resolve_target() {
  local rig="$1"
  if [[ -z "$rig" ]]; then
    return 2
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    echo "deliver-rollup: python3 not on PATH; cannot resolve per-rig channel" >&2
    return 1
  fi
  python3 "$resolver" "$rig"
}

mapfile -t bead_ids < <(
  gc bd list --label rollup --label severity:escalate --status open --json \
    | jq -r '.[] | select((.labels // []) | index("delivered") | not) | .id'
)

if [[ ${#bead_ids[@]} -eq 0 ]]; then
  exit 0
fi

# Whether the legacy city-wide fallback is configured. If not, beads
# whose rig has no binding will be left undelivered (and retried next
# tick) instead of silently disappearing.
fallback_configured=true
for var in GC_OVERSIGHT_SESSION_ID GC_OVERSIGHT_PROVIDER \
           GC_OVERSIGHT_ACCOUNT_ID GC_OVERSIGHT_CONVERSATION_ID \
           GC_OVERSIGHT_KIND; do
  if [[ -z "${!var:-}" ]]; then
    fallback_configured=false
    break
  fi
done

for id in "${bead_ids[@]}"; do
  bead_json=$(gc bd show "$id" --json)
  title=$(jq -r '.[0].title' <<<"$bead_json")
  body=$(jq -r '.[0].description // ""' <<<"$bead_json")
  rig=$(jq -r '.[0].labels[] | select(startswith("rig:")) | sub("^rig:"; "")' <<<"$bead_json" | head -1)

  text=$(printf '*%s*\n\n%s\n\n_(rollup bead %s · rig: %s)_\n_Reply to this message to respond to %s._' \
    "$title" "$body" "$id" "$rig" "$id")

  resolved_json=""
  if resolved_json=$(resolve_target "$rig" 2>/dev/null); then
    : # per-rig binding found; resolved_json populated
  else
    rc=$?
    case "$rc" in
      2|3)
        if ! $fallback_configured; then
          echo "deliver-rollup: rig=${rig:-<none>} has no per-rig binding and city-wide fallback is not configured; skipping bead $id" >&2
          continue
        fi
        resolved_json=""  # signal: use legacy env-var fallback
        ;;
      *)
        echo "deliver-rollup: resolver failed for rig=${rig:-<none>} (rc=$rc); skipping bead $id" >&2
        continue
        ;;
    esac
  fi

  if [[ -n "$resolved_json" ]]; then
    sid=$(jq -r '.session_id' <<<"$resolved_json")
    scope=$(jq -r '.conversation.scope_id' <<<"$resolved_json")
    prov=$(jq -r '.conversation.provider' <<<"$resolved_json")
    acct=$(jq -r '.conversation.account_id' <<<"$resolved_json")
    conv=$(jq -r '.conversation.conversation_id' <<<"$resolved_json")
    kind=$(jq -r '.conversation.kind' <<<"$resolved_json")
    target_label="rig:$rig"
  else
    sid="$GC_OVERSIGHT_SESSION_ID"
    scope="$GC_CITY_NAME"
    prov="$GC_OVERSIGHT_PROVIDER"
    acct="$GC_OVERSIGHT_ACCOUNT_ID"
    conv="$GC_OVERSIGHT_CONVERSATION_ID"
    kind="$GC_OVERSIGHT_KIND"
    target_label="city-wide"
  fi

  payload=$(jq -n \
    --arg sid "$sid" \
    --arg text "$text" \
    --arg key "rollup-$id" \
    --arg scope "$scope" \
    --arg prov "$prov" \
    --arg acct "$acct" \
    --arg conv "$conv" \
    --arg kind "$kind" \
    '{
       session_id: $sid,
       conversation: {
         scope_id: $scope,
         provider: $prov,
         account_id: $acct,
         conversation_id: $conv,
         kind: $kind
       },
       text: $text,
       idempotency_key: $key
     }')

  if curl --silent --show-error --fail \
       --max-time 30 \
       --header "Content-Type: application/json" \
       --header "X-GC-Request: deliver-rollup" \
       --data "$payload" \
       "$api" >/dev/null; then
    gc bd update "$id" --add-label delivered
    echo "delivered $id ($target_label, conv=$conv)"
  else
    echo "delivery failed for $id; will retry next tick" >&2
  fi
done
