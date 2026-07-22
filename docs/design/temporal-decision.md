# Temporal in Gas City — the decision record

**STATUS: DRAFT** — gc-4zf.5 (Track E) skeleton, 2026-07-21. Verdicts marked
SETTLED are backed by evidence already in the record; slots marked **TBD** wait
on the 07-23 soak gate (gc-372), the observe-mode metrics (gc-4zf.7), and the
Track D case study (gc-4zf.3). This document decides; it does not plan an
adoption. Whatever survives gets a follow-on epic. On finalization this record
supersedes dec-bvs (the original option-1 record, written under the retired
promotion framing) via a new dec-* entry in the decisions rig.

The epic's obligation carries into every row below: "adopt nowhere beyond the
case studies" and "adopt only for X" are both legitimate outcomes, the
negatives stay in the record because they are the most valuable output, and
every "don't" states what would change our mind.

## Constraints on any "adopt" (the non-negotiables, restated as gates)

1. Temporal watches, never owns. Beads/dolt is the source of record.
2. No workflow-per-bead. Workflows are per orchestration episode, keyed off
   bead identity (naming convention: gc-4zf.6).
3. Never block an Activity on an agent tmux session. Dispatch, return the bead
   id, wait on Signal or reconcile.
4. A reliability layer is a query, not a memory: re-derive state from SQL each
   tick.

Two hard gates precede any execute-path expansion: the memory gate (gc-qaid —
`gascity.slice` at 31.1/32G, dolt memcg-killed 3x on 07-21, swap 100%; audit
§0) and the soak gate (gc-372 clean week, ~07-23). Observe-only work (gc-4zf.7)
is exempt from the memory gate.

## Verdict table

| Candidate | Verdict | Evidence | Status |
|---|---|---|---|
| Order floor (Schedules replacing orders) | **don't** | gc-372 soak record: 44s work/120m window, SkippedOverlap 0; audit §6 | SETTLED (maintenance-cycle disposition TBD at 07-23 gate) |
| Molecule engine (MoleculeSupervisorWorkflow) | **don't** | compile.go:325 source bug + missing SQL scans; audit §6 "No, despite being strongest on paper" | SETTLED |
| Dispatchers (heartbeat-supervised) | **don't** | dispatcher-watchdog already kills and respawns; audit §6 | SETTLED |
| Watchdog / scan-of-scans substrate | **don't** (systemd --user timer instead) | Track A §9.2 names the gap; audit §6 prices the timer ahead: zero new state, zero new ops | SETTLED |
| External-mutation boundary (gc-4zf.10) | **adopt-candidate** | audit §6 "where lean-in cashes out"; gc-372 chaos test proves crash-survivable exactly-once; mol-pr-merge-only unguarded write | PENDING final gc-4zf.5 synthesis + both gates |
| Signal bridges | **defer — moot unless proven needed** | never-wired metadata contract (gc-4zf.7 notes); decision rule below | TBD (gc-4zf.7 metrics at 07-23 gate) |
| PRIterateWorkflow (Track D, gc-4zf.3) | **defer** (soak-gated) | durable-execution-walkthrough-pr-state-poller.md; defect 3 is the shape bash cannot fix | TBD (case-study result) |

## Order floor — don't (SETTLED)

The maintenance-cycle cutover is the measured negative. The workflow does 44
seconds of work per 120-minute window; SkippedOverlap has fired zero times and
at that duty cycle essentially never will; there is no long-lived state and no
event wait. On this workload Temporal reduces to cron plus a lockfile
(gc-4zf epic description; gc-372 notes, cycles of 2026-07-16).

The order system's two silent failures in the record were both caught by scans
or humans, not by anything Temporal escalated: the 07-16 crash-mid-sling orphan
(gc-4zf.4 chaos test — workflow FAILED, orphan bead, poisoned pending claim,
zero escalation; detection latency was infinite until a human read journals)
and the 07-21T10:00Z dolt-outage cycle failure, surfaced by
`bin/temporal-soak-check`, a bash scan on a cooldown order
(`orders/temporal-soak-check.toml`). The epic's own sentence stands: the
engine made the failure loud; a scan made it converge. OpenCortex
(gc-4zf.5 notes) is the external counter-example of the naive opposite design,
and the course curriculum's orders-first expansion path is explicitly rejected
against this evidence (gc-4zf.5 notes, 2026-07-21).

**TBD — maintenance-cycle disposition at the 07-23 gate.** The one live
Temporal order stays a separate question from expanding to the floor. Decide
on the soak-check record (at draft time: 12 cycles/24h dispatching, one FAILED
cycle 07-21 attributed to the dolt memcg-kill storm, root cause fixed and
redeployed as gc-372.1 / commit 6024920 on 07-22 02:37Z). Options at the gate:
keep Temporal as sole driver and retire legacy per the P5 plan, or revert to
the order and record the whole chain as case-study evidence only.
<!-- TBD: fill from the 07-23 soak verdict -->

**What would change our mind:** an order workload appearing whose duty cycle
actually exercises durability (hours-long, event-waiting, crash-exposed), or
the order-substrate death class (gc-qo3: 10 days dormant, unnoticed) proving
unfixable by a non-order scan — see the watchdog row before reaching here.

## Molecule engine — don't (SETTLED)

Strongest on paper, and the observed failures are not durability failures. The
stuck-state inventory (mol-focus-review `gc.needs_recovery=true` with zero
consumers, partial re-arm crash, closed-root-open-steps, mol-epic-review
in-flight flag with no expiry) traces to a compiler bug at
`compile.go:325` (graph.v2 vapor RootOnly strand), a missing idempotency gate,
and missing SQL scans — source bugs and query questions, not lost workflow
state (audit §6; Track A §7). A supervisor workflow over a buggy compiler
would durably supervise the bug. Fix source, add scans; a supervisor workflow
is the fallback only if a scan cannot express the cover.

**What would change our mind:** a stuck-molecule class that survives the
source fixes and cannot be expressed as a level-triggered SQL scan — i.e. one
that genuinely requires durable per-episode timers rather than "any molecule
in state X older than N?".

## Dispatchers — don't (SETTLED)

Heartbeats are attractive and redundant here: `dispatcher-watchdog` already
kills and respawns a wedged dispatcher, and the observed failure modes
(conversion stall, port-0 freeze) were fixed at source or by the watchdog. The
remaining tuning item is the 5h idle threshold, not a substrate change
(audit §6).

**What would change our mind:** a dispatcher failure mode the kill-and-respawn
model cannot repair — one requiring durable in-flight state across the
respawn, which today does not exist because dispatch state re-derives from the
store.

## Watchdog / scan-of-scans — don't; use a systemd --user timer (SETTLED)

This is the one candidate where the substrate argument is real: every cover in
the city is an order, orders are what silently die (gc-qo3), and there is no
non-order watchdog over "did each order fire within 2x its cadence"
(Track A §9.2 — the map's #1 recommendation). Track A allowed that this is the
single place a Temporal Schedule is defensible. Priced against a
`systemd --user` timer running the existing scan logic, the timer wins: same
failure-domain independence from the order system, zero new state, zero new
ops surface, no memory-budget entry on a box already at its ceiling
(audit §6, §0). Failure-domain independence was Temporal's one uniquely proven
property here (P4: survives supervisor restart); a timer on a different
substrate has the same property for this job.

**What would change our mind:** the scan-of-scans needing durable per-episode
state a stateless timer tick cannot carry (it should not — invariant #4 says
re-derive from SQL), or systemd --user itself joining the shared failure
domain (the 07-15 host-death class kills both, but it kills Temporal too).

## External-mutation boundary — adopt-candidate (PENDING)

The one lane where the two demonstrated properties cash out together:
crash-survivable exactly-once external mutation, and failure-domain
independence. The candidates are the gated GitHub mutations — the merge step
of `mol-pr-merge-only` (today an unguarded external write),
`approved-pr-automerge`, and the push steps of `mol-pr-from-issue` — a handful
of mutations per day, but exactly the worst loss class (audit §6; scoped as
gc-4zf.10). The gc-372 chaos test proves the semantics work: at-most-once held
under SIGKILL mid-side-effect (no duplicate bead, no duplicate sling), and
gc-372.1 (commit 6024920) established the boundary discipline that makes it
livable — fail-closed at-most-once on the mutation only, pre-claim reads in a
Preflighter that stamps nothing and stays retryable, permanent validation
defects as PermanentPreflightError recorded terminally
(`real_adapter.go`, `maintenance_runner.go`).

The two-layer thesis holds and bounds the design: work layer = at-most-once
fail-closed (a duplicate PR is worse than a skipped cycle); watchdog layer =
at-least-once + idempotent repairs. RealAdapter/execstore semantics belong
only to the work layer — a watchdog built on them goes silent exactly when
things break (gc-4zf.4). The boundary sits at the external side effect itself,
nowhere upstream of it.

Not final until: gc-4zf.5 synthesis completes, the memory gate (gc-qaid)
closes, and the 07-23 soak verdict lands. Known residues carried into the
lane's scope if adopted: 9.5a (orphan unnamed), 9.5b (tombstone never swept),
and the versioning gate (gc-4zf.9 — current GA Worker Versioning API +
workflow.GetVersion, not the experimental path being removed ~March 2026).
<!-- TBD: confirm or downgrade after gc-4zf.5 synthesis + gates -->

## Signal bridges — defer; the decision rule is moot-unless-proven-needed

The rule, confirmed with Stephanie 2026-07-22 (gc-4zf.7 notes): the bridges
stay off and get deleted-and-recorded at the 07-23 gate unless observe-mode
data shows both (a) events missed by the bridge that the scan mesh does not
see and (b) a latency advantage a real consumer needs. The null hypothesis is
"moot": the metadata contract the bridges depend on
(`gc.metadata.temporal.repo` + `temporal.cycle_key`) was never fulfilled by
the armed runSelection, so both disabled bridge orders
(`temporal-maintenance-signal-*.toml.disabled`) are no-ops today, and
dispatch-only workflows complete immediately after selection with no waiting
phase to signal. The contract rotted silently precisely because nothing
level-triggered reconciled it — which is itself evidence for "signals advance,
queries repair."

**TBD — gc-4zf.7's five observe-mode metrics** (events missed, observed-vs-
derived state diff, history growth, e2e latency, duplicate-event frequency)
plus the two null-hypothesis counters (beads carrying temporal.* metadata,
workflows observed waiting).
<!-- TBD: fill from gc-4zf.7 report at the 07-23 gate -->

## PRIterateWorkflow (Track D) — defer until the soak closes

The first workload with the right shape: long-lived, event-waiting,
human-gated, multi-step. The walkthrough
(docs/design/durable-execution-walkthrough-pr-state-poller.md) documents what
the 243 lines of bash actually cost — two idempotency mechanisms because
neither suffices, 174 cache files for 8 open PRs, 60m22s review-to-dispatch
latency, ~72 GitHub API calls/hour at steady state — and the one defect bash
cannot fix without becoming a workflow engine: fire-and-forget past dispatch
(a wedged mol-pr-iterate leaves the review "handled" forever, the original
leak returning by another door). A workflow ID per review
(`pr-iterate/{repo}/{pr}/{review_id}`) deletes both idempotency mechanisms and
the crash window; a durable timer closes defect 3. Continue-As-New required at
the wait boundaries (survey extraction, gc-4zf.5 notes). Latency stays at
roughly the poll interval until webhook intake exists; claiming otherwise
would repeat the maintenance-cycle overstatement.

Execution is blocked by the gc-372 soak (two live cutovers at once is how a
controlled experiment stops being one) and, as execute-path work, by the
memory gate.

**TBD — Track D case-study result:** does the built workflow deliver the
walkthrough's predicted deletions and the defect-3 escalation in practice, and
does the demoted poll (dispatcher → reconciler) hold completeness?
<!-- TBD: fill from gc-4zf.3 when it runs -->

## Costs and rollback (bead questions 4 and 5)

Honest cost of the currently adopted surface: a dev-server (SQLite
persistence) + one worker under systemd --user, worker cgroup at
MemoryHigh=1536M/MemoryMax=2G after the 07-16 ceiling incident (gc-372 notes:
the 256M/384M shadow ceiling thrashed gc-sling past its Activity deadline),
localhost-only bind with no authorizer (safe only while 127.0.0.1 — README
note required), schedule drift risk on respawn, and the replay-safety gate
before any workflow-definition change (gc-4zf.9). Any expansion adds its
worker memory to a slice already at 31.1/32G; audit §6's budget line stands:
no expansion until the memory arithmetic is fixed.

Rollback for the one live surface is documented and exercised:
`schedule-disarm.sh` + revert to DryRunAdapter + re-arm the paused order
(deploy/CUTOVER.md; performed cleanly on 07-16). Anything adopted at the
external-mutation boundary must ship the same shape: a disarm script and a
still-intact legacy path until its own clean week passes.

<!-- TBD: final synthesis paragraph answering bead question 2 (is
failure-domain independence alone worth the operational weight) across the
whole table, after the three TBD slots above fill. Draft answer from current
evidence: no for scans and timers (a timer substrate is independence enough);
yes only where exactly-once external mutation compounds with it. -->
