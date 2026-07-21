# Scheduler-dispatchability contract (dr-zkmc, 2026-07-18).
#
# `bd ready` remains the canonical dependency-ready set. This module defines
# the narrower set an unattended scheduler may dispatch. Feeder, feeder
# summary, and patrol all import this predicate so their counts cannot drift.
# Remove this city-local contract only after an upstream API exposes the same
# predicate and every consumer has migrated to it.

def dispatch_gate_labels:
  [
    "needs-decision",
    "needs-human",
    "needs/stephanie",
    "deferred",
    "icebox",
    "gated",
    "blocked-external",
    "upstream-gated",
    "branch-ready",
    "parked",
    "dispatch-blocked"
  ];

def dispatch_structural:
  (((.labels // []) | index("rollup")) != null)
  or (((.labels // []) | index("epic")) != null)
  or (((.issue_type // "") | IN("epic", "convoy", "rollup")));

def has_dispatch_gate:
  (.labels // []) as $labels
  | [dispatch_gate_labels[] as $gate | ($labels | index($gate)) != null]
  | any;

def scheduler_dispatchable:
  ((.status // "open") == "open")
  and ((.assignee // "") == "")
  and ((.metadata."gc.routed_to" // "") == "")
  and ((.metadata."gc.outcome" // "") != "branch-ready")
  and (dispatch_structural | not)
  and (has_dispatch_gate | not);
