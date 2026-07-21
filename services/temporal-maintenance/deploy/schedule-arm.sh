#!/usr/bin/env bash
# schedule-arm.sh — create the 120m maintenance-cycle Schedule (Skip-overlap,
# dispatch-only). This is the live half of the cutover: once created, the armed
# worker will create+sling real review+author polecats every 2h. Reversible with
# schedule-disarm.sh.
set -euo pipefail

NS="${TEMPORAL_NAMESPACE:-maintenance}"
ADDR="${TEMPORAL_ADDRESS:-127.0.0.1:7233}"
REPO="${TEMPORAL_MAINT_REPO:-gastownhall-gascity}"

temporal schedule create \
  --namespace "$NS" --address "$ADDR" \
  --schedule-id maintenance-cycle \
  --interval 120m \
  --overlap-policy Skip \
  --workflow-id maintenance-cycle \
  --type MaintenanceCycleWorkflow \
  --task-queue temporal-maintenance-shadow \
  --input "{\"repo\":\"${REPO}\",\"dispatch_only\":true}"

echo "armed: schedule 'maintenance-cycle' every 120m (Skip-overlap, dispatch-only, repo=${REPO})"
echo "watch: temporal schedule describe --namespace ${NS} --schedule-id maintenance-cycle"
