#!/usr/bin/env bash
#
# nudge-city-infra-pl.sh — wake the city-infra-pl for a triage/work tick.
#
# Pure plumbing: find the active city-infra-pl session and send the standard
# tick nudge. The decision of what to fix or surface stays entirely with the
# city-infra-pl (informed by its prompt + open city beads). This script never
# reads beads, never decides anything.

set -euo pipefail

template="city-infra-pl"

mapfile -t session_ids < <(
  gc session list --json \
    | jq -r --arg t "$template" '
        (if type == "object" then .sessions else . end)
        | .[]
        | select(.template == $t and .state == "active")
        | .id
      '
)

if [[ ${#session_ids[@]} -eq 0 ]]; then
  echo "no active city-infra-pl session"
  exit 0
fi

nudged=0
for sid in "${session_ids[@]}"; do
  if gc session nudge "$sid" "City-infra tick: gc mail count, survey open city-infra beads + gc doctor, do ready work in-place, surface Tier-2 decisions to mayor." >/dev/null 2>&1; then
    nudged=$((nudged + 1))
  else
    echo "nudge failed for $sid" >&2
  fi
done

echo "nudged $nudged city-infra-pl session(s)"
