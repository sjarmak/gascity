#!/usr/bin/env bash
#
# has-undelivered-escalates.sh — condition check for escalate-rollups order.
#
# Exits 0 (fire the order) when there is at least one open rollup bead
# with severity:escalate that has not yet been labeled delivered.
# Exits non-zero otherwise.

set -euo pipefail

count=$(
  gc bd list --label rollup --label severity:escalate --status open --json \
    | jq '[.[] | select((.labels // []) | index("delivered") | not)] | length'
)

[[ "$count" -gt 0 ]]
