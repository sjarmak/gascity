# Bead declaration rubric

Beads is the authoritative task graph and audit log. Prediction models and
high-volume telemetry live outside it. The provenance discipline that makes
scheduling improvable: **agents write structured facts; the estimation layer
predicts; the solver decides; Beads records all three with explicit
provenance.** No single mutable field may ambiguously represent a fact, a
prediction, and a decision at once (`due_at` is a fact, `predicted_duration`
is a model output, `solver_rank` is a decision).

This rubric defines what each party writes, at which lifecycle stage, using
which mechanism. It governs the inputs to `bd ready --sort hybrid` today and
to the graph-aware solver that succeeds it. Empirical grounding: the or-replay
run of 2026-07-13 (`docs/or-phase0-replay-results-2026-07-13.md`) showed
declared priority is the dominant signal on current traffic, and the deadline
feature has never been testable because no bead in 11 weeks carried one.

**Correction to the or-p1 spec (§1.3):** bd natively supports `--due`,
`--defer`, `--estimate`, and `--acceptance` (verified against bd v1.1.0
2026-07-13). The `gc.due` metadata convention in the spec is superseded; use
the native field. Scoring reads native `due` wherever the spec says `gc.due`.

## At creation or decomposition: native fields first

Use native Beads fields for workflow semantics, never prose encodings and
never metadata where a native field exists:

| field | rule |
|-------|------|
| description + `--acceptance` | precise scope, expected output, machine-verifiable completion conditions. A bead a verifier cannot check is not done being specified. |
| `--priority` (0 to 4) | cost of delay, not importance to author. Bands below. |
| `--due` | real external deadlines only: a demo, an access window closing, a commitment to a human, data that expires. Never set to jump the queue. Absence is neutral by construction. Clear it when the date moves or dies. |
| `--defer` | the opposite semantic: hide from `bd ready` until the date. Use for work that must not start early. |
| `--estimate` (minutes) | honest point estimate; distributions and confidence go in metadata (below). A false-precise estimate is worse than none plus a wide p90. |
| dependencies | `blocks` for genuine precedence only (X cannot start before Y lands). `parent-child`, `discovered-from`, `related` for structure and provenance; they do not affect readiness. Overusing `blocks` serializes the schedule for no reason. |
| labels | categorical properties: component, domain, release, review requirement, risk class. |

### Priority bands (calibration of the one human-declared value signal)

| band | meaning | test |
|------|---------|------|
| P0 | active damage | corrupt output being produced now, fleet-wide stall, untrusted verification gate, live security exposure |
| P1 | blocks other work now | a named bead, pool, or human is waiting; name what it unblocks in the description |
| P2 | default | scheduled work nobody is waiting on; when unsure, P2 |
| P3 | worth doing, no urgency | cleanups, hygiene; fine to age for weeks |
| P4 | parked | kept for the record; may never run |

P0/P1 must name their impact; a reviewer or reaper that cannot find it
demotes. Calibration is distributional: a source emitting mostly P0/P1 is
miscalibrated, not busy. Priority changes carry a note. Formulas inherit the
root's priority and never self-escalate. Business value is a policy input
declared by humans or the mayor; the executing agent never invents it.

### The `orchestrator` metadata contract (typed optimization inputs)

Facts and structured self-descriptions the native schema has no field for go
under one metadata key, versioned:

```json
{
  "orchestrator": {
    "schema_version": 1,
    "requirements": {
      "capabilities": ["go", "database-migrations"],
      "tools": ["network", "github"],
      "repositories": ["backend"],
      "exclusive_resources": ["production-schema-slot"],
      "expected_touches": [{ "path": "internal/storage/**", "confidence": 0.8 }]
    },
    "structure": {
      "splittable": true, "max_parallelism": 3,
      "preemptible": true, "checkpointable": true
    },
    "estimates": {
      "duration_minutes": { "p50": 45, "p90": 120, "confidence": 0.55 }
    },
    "artifacts": { "consumes": ["schema-contract:v3"], "produces": ["migration:v4"] },
    "execution": { "agent_type": "worker", "model_tier": "strong",
                   "reasoning_effort": "high", "mode": "isolated-worktree" }
  }
}
```

Fill what you honestly know; omit what you do not. `requirements` are
eligibility and resource facts the solver treats as hard constraints, never
score inputs. `expected_touches` carries confidence because it is a
prediction an agent is making about itself; it is logged at dispatch time so
it can be scored against `actual_touches` later (the leakage rule: only
decision-time information may feed a dispatch decision). If uncertainty is
too high to estimate at all, say so; "investigate this task enough to
estimate it" is itself a schedulable bead.

## During execution: record observations as they happen

Agents and runtime hooks continuously append (notes, status, metadata):

- claim, assignee, start time, heartbeats, current status
- newly discovered dependencies and subtasks (`discovered-from` edges)
- actual files and resources reserved
- revised remaining-time estimate when it materially changes
- checkpoint or handoff summary at any stop
- blocker category: needs another task, a human, or an external event
- attempt number and prior failure category on retries
- test, lint, benchmark, review status
- actual model and tool configuration used

## At completion or failure: capture what improves the next schedule

- wall time and active agent time; token and compute cost
- number of attempts; test and review result
- merged without conflict or not; rework generated
- `actual_touches` (scored against dispatch-time `expected_touches`)
- estimate error; whether decomposition and dependencies were correct
- failure taxonomy: implementation, environment, specification, dependency,
  merge, model capability, timeout

State transitions are immutable event records; compact current-state labels
and metadata act as the query cache (the Beads event-source/state-cache
pattern). Never rewrite history to look better; the estimate-error record is
the training signal.

## What the queue builder reads

`bd ready --json` determines legal dispatch now. It does not determine which
legal dispatch creates the best future state. The solver additionally reads
the full open dependency graph, in-progress work and predicted completions,
blocked successors and their downstream value, gates and external waits,
pins/claims/leases, per-agent capacity, capability and cost, and budget and
deadline state. The canonical example: task A (20 min, closes one low-value
leaf) and task B (35 min, unlocks five high-value concurrent tasks) are both
ready; a priority-only queue often takes A, a graph-aware solver takes B.

Feasibility is constraints, not score: precedence and gates, eligibility,
exclusive resources and file/schema conflicts, concurrency capacity,
worktree restrictions, freezes, reviewer separation, budget ceilings. The
objective is lexicographic, not one opaque weighted sum: avoid missing P0/P1
deadlines; then maximize completed value and newly unlocked work; then
minimize wall time and critical-path delay; then minimize conflict and rework
risk; then cost; prefer older work when otherwise equivalent. Re-solve on
meaningful events (create/close/block, agent availability, dependency or
estimate or deadline change), commit only the immediate assignments, and let
the rest of the horizon move.

Any feature added to the objective must be: available at decision time for
most candidates, structured rather than re-extracted from prose,
directionally stable, calibrated against observed outcomes, actionable,
incrementally useful, and explainable. Validation is replay plus ablation in
`bin/or-replay` against the standing baselines (priority/FIFO, EDF, SJF,
critical-path-first, greedy affinity, and whatever the fleet currently
runs); a feature that only looks useful because of historical assignment
bias does not ship without controlled exploration.

## Anti-gaming

The declaring agent must never be the optimization target.

- Business value and priority are policy inputs; executing agents never set
  their own.
- Agent self-assessments (success probability, confidence) are recorded but
  never authoritative; the estimation layer learns task-agent success rates
  from outcomes, not from claims.
- Mechanically derivable features are always derived (age from `created_at`,
  unblocking from the open-`blocks` graph), never declared.
- Declared facts are audited after the fact: `expected_touches` vs
  `actual_touches`, estimate vs wall time, declared priority vs measured
  unblocking. A source with repeated inflation findings gets the self-report
  discount in triage until re-calibrated.

## Who writes what

| party | writes |
|-------|--------|
| humans, mayor | priority, due, business value, acceptance criteria |
| declaring/decomposing agents | description, estimates with confidence, `orchestrator` metadata facts, dependency edges |
| executing agents | execution observations, discovered deps, outcomes, failure taxonomy |
| estimation layer | predictions (duration, success, conflict risk), outside Beads or clearly marked as model output |
| solver | decisions (assignments, ordering), written separately with provenance |
| PLs | weekly queue re-grade: demote stale P1s, clear dead due dates, prune false `blocks` edges |
