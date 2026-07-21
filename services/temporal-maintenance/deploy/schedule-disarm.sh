#!/usr/bin/env bash
# schedule-disarm.sh — delete the maintenance-cycle Schedule (stops Temporal
# driving cycles). Part of the cutover rollback.
set -euo pipefail
NS="${TEMPORAL_NAMESPACE:-maintenance}"
ADDR="${TEMPORAL_ADDRESS:-127.0.0.1:7233}"
temporal schedule delete --namespace "$NS" --address "$ADDR" --schedule-id maintenance-cycle
echo "disarmed: schedule 'maintenance-cycle' deleted"
