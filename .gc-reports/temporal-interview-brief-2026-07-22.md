# Temporal beneath the agent framework: interview brief

2026-07-22. Backing records: epic gc-4zf, docs/design/temporal-decision.md
(DRAFT), city audit 2026-07-21 §6, services/temporal-maintenance/.

## We ran the canary; the order path lost, the mutation boundary won

Temporal sits beneath the agent framework as a durable execution layer. It
does not replace the framework and it does not own state: beads on dolt stay
the source of record, and Temporal watches orchestration episodes. Rather than
argue this from architecture we ran a bounded canary, one production order
moved onto a Workflow with a chaos test and a soak week that closes 07-23,
and the result was a measured negative for the order path. The maintenance
cycle does 44 seconds of work per 120-minute window, skip-overlap has fired
zero times, and on that duty cycle Temporal reduces to cron plus a lockfile.
The positive landed at the external-mutation boundary: SIGKILL the worker
mid-side-effect and at-most-once holds, no duplicate bead, no duplicate
dispatch. That is crash-survivable exactly-once, and it is the property our
currently unguarded GitHub merge and push steps lack. These are measured
results, not hypotheses, and the negative is as load-bearing as the positive.

## Workflows are episodes; beads own the record

A workflow maps to an orchestration episode, never to a bead's lifecycle. A
bead already carries durable identity, dependencies, status, and history, so a
workflow-per-bead design would duplicate the record and then fight it.
Workflow IDs derive from bead identity instead:
`gc/{cityID}/molecule/{rootBeadID}/{operation}`. Epics stay pure bead
dependency graphs, never parent/child workflow trees; the dependency graph is
already the durable structure, and mirroring it in an engine would create a
second source of truth that drifts.

## Two disciplines, both paid for with live incidents

Fail-closed at-most-once belongs on the external mutation, never on a
pre-mutation read. We learned this live: a transient store outage during an
in-flight read got stamped as a terminal failure and killed the whole cycle
(gc-372.1, fixed today). The fix moved reads into a preflight that stamps
nothing and retries, while the mutation itself stays fail-closed, because a
duplicate PR is worse than a skipped cycle but a skipped cycle over a flaky
read is just a bug.

Signals advance, queries repair. Every Signal path needs a level-triggered
reconciler behind it, and our evidence is an absence: the metadata contract
our signal bridges depended on was never wired, and nothing noticed until an
audit pass read the store, because no query re-derived the expected state. An
event path without a reconciler rots silently. The corollary governs stall
detection too: fleet-wide it stays level-triggered SQL, re-derived each tick
so it cannot drift, and durable timers live only inside episodes, where "no
completion in N hours, escalate" genuinely needs to survive a process death.

## Where we diverge from Temporal's own agent patterns

Temporal's published agent integrations run the LLM loop inside a Workflow
and wrap each model call in an Activity. Our agents are external tmux
sessions we cannot reach into, so Activities dispatch work, return the bead
id, and wait on a Signal or a reconciler pass; nothing ever blocks on an
agent session. We take the outer-harness boundary deliberately and pay for
the lost internal visibility with the scan mesh. Worker Versioning is part of
the application model from day one, on the current GA API with
`workflow.GetVersion`, gated in change-control before the first production
definition change.

Next: the 07-23 soak gate decides the maintenance cycle's disposition, and
the external-mutation lane is the one adoption candidate still standing.
