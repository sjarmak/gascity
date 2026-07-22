# temporal-observe — first live pass (2026-07-22, bead gc-4zf.7)

One-shot observe-mode run at 2026-07-22T03:38Z against the live server
(127.0.0.1:7233, ns `maintenance`) and the gascity bead store. Read-only
throughout; record appended to `.gc/temporal-observe-metrics.jsonl`.
Schema reset (03:38Z): review fixes changed the metric semantics
(retention-bounded coverage, query-failure incompleteness instead of false
zeros, run-addressed state reads, identity-matched latency applicability), so
the JSONL envelope now carries `schema: 1` and the file holds only records of
that schema. The two earlier pre-schema records (02:57Z, 03:19Z) were
superseded by the 03:38Z re-run, which measured identical values on an
unchanged execution set — every number below survived the semantics change.
Question under test: are the disabled signal-bridge orders
(`orders/temporal-maintenance-signal-*.toml.disabled`) moot or forgotten?
Decision rule (Stephanie, 2026-07-22): MOOT UNLESS PROVEN NEEDED — enable only
if the data shows BOTH missed events the scan mesh doesn't see AND a latency
advantage a real consumer needs.

## Measured (window: 72h of beads, 24h of retained workflow history)

| Metric | Value | Reading |
|---|---|---|
| 1. Events missed by the bridge | **0** forwardable of 34 closed maintenance-cycle beads | No bead carries the `temporal.*` contract, in either shape (flat dotted keys or nested object) |
| 2. Beads vs workflow-state diff | **0** diffs both directions (12 executions queried, 0 query failures) | 33 beads predate the 24h execution retention and are counted, not diffed; 18 synthetic selection refs (see below) |
| 3. History growth | 11–17 events/execution, mean 16.5, flat | 17 = full dispatch-only cycle, 11 = the 07-21T10:00Z failed cycle; no growth anywhere |
| 4. End-to-end event latency | **n/a — structural** | Dispatch-only cycles complete at selection; no workflow ever enters a signal-waiting phase, so there is no consumer for an event to reach |
| 5. Duplicate-event frequency | **0** of 0 forwardable | The bridge state file has never recorded a signal (the orders never ran) |
| Expect-zero: beads with `temporal.*` metadata | **0** | Confirms the pre-build evidence: the armed `runSelection` stamps only loop-close metadata |
| Expect-zero: workflows in a waiting phase | **0** | All 12 retained executions are dispatch-only, phase `done` |

## Verdict on the historical evidence: MOOT (in the current regime)

Three independent structural facts, each alone sufficient:

1. **Nothing to forward.** The metadata contract the bridges scan for
   (`temporal.repo` + `temporal.cycle_key`, per the
   `bin/temporal-maintenance-signal-bead-closed` header) was never wired into
   the armed selection (`maintenance_runner.go` createArgs). Enabled or
   disabled, both orders are no-ops: 0 tagged beads across 34 candidates.
2. **Nobody to deliver to.** Every execution runs `DispatchOnly` and completes
   in the same second it starts (sub-second `RunTime` on skip cycles, ~1–4 min
   on armed create+sling cycles). The `AwaitingEvents` selector the signals
   feed is never reached.
3. **Wrong address anyway.** The signal path (`Signaler` → `WorkflowID()`)
   targets `gascity-maintenance/<repo>/<cycleKey>`, but the Schedule-started
   executions are named `maintenance-cycle-<ISO timestamp>`. Even a tagged
   bead and a waiting workflow would produce a signal to a nonexistent
   workflow ID. This divergence also affects `maintenance-reconcile` and
   `temporal-signal`, and should be recorded on the bead when the orders are
   deleted — it is a latent defect of the fanout path, not of dispatch-only.

Applying the decision rule: zero missed events and zero consumers means
neither enable-condition is met. Recommendation for the 07-23 gate, contingent
on the counts staying zero until then: **delete the two `.toml.disabled`
orders and record facts 1–3 on the closing bead.** If the fanout/gated path is
ever built (Track B/D, blocked on the gc-372 soak and the gc-qaid memory
gate), the signal question reopens as design work — with the workflow-ID
convention fixed first.

## Regime findings the metrics surfaced (side observations, not verdict inputs)

- **Skip-streak, not shadow.** All 18 synthetic `temporal-shadow/...`
  selection refs are armed `skipped-inflight` guard hits: 9 of the 12 retained
  cycles skipped both halves (9 × 2 = 18), in two stretches. Morning:
  07-21T04:00Z/06:00Z/08:00Z (run late at 05:20/07:40/08:17) skipped behind
  then-open overnight-cycle beads, all since closed. Evening: 16:00Z through
  02:00Z (6 cycles) skipped behind the midday beads and the streak is still
  live at observation time. Only two cycles dispatched anything — 12:00Z
  (gc-6vja review, gc-wj5s author) and 14:00Z (gc-y8zp review, gc-lehu
  author); 10:00Z failed (the circuit-breaker cycle, below). The open-bead set
  blocking the live streak is {gc-wj5s, gc-y8zp, gc-lehu}: gc-6vja closed, but
  gc-wj5s — the 12:00Z author bead — is still open and blocks the author half
  alongside gc-lehu, while gc-y8zp blocks review. (The guard counts
  `--status open` beads only, per `halfInflight`; that is how 14:00Z could
  dispatch a second author bead while gc-wj5s existed.) Workflow state alone
  cannot distinguish any of this from actual shadow mode
  (`DispatchSelectionResult.Skipped` is not persisted into `BranchState`);
  the `synthetic_selection_refs` count surfaces the ambiguity. Two follow-ons
  for the soak reading: (a) within the retained 24h only 2 of 12 cycles
  dispatched new work — no new dispatch 02:02Z→12:00Z (~10h, per the
  execstore) and again 14:03Z→03:38Z observation (~13.5h, ongoing), guard-
  correct but outcome-relevant to gc-372; (b) persisting the skip flag in
  `BranchState` would close the observability gap. Attribution (added
  03:2xZ after checking the claim path): the live streak's blocker beads
  cannot be claimed because the polecat pool (`/home/ds/gascity/polecat`)
  is SUSPENDED under the 2026-07-21 cleanup-pause quiesce —
  `routed-bead-nudger` logs `no-session-for-pool`/`dead-pool` every 15m.
  The streak is operator-pause collateral, not polecat slowness and not a
  Temporal defect; while the pause holds, every cycle will guard-skip.
- **Retention asymmetry is structural.** The namespace retains 24h
  (`WorkflowExecutionRetentionTtl 24h`); the bead store retains everything.
  Each observe run can therefore reconcile at most 24h of executions — the
  instrument bounds its diff to the covered span (`covered_from`) and counts
  the remainder instead of mis-reporting it. This is invariant 1 in action:
  beads/dolt is the record, workflow history is a working set.
- One failed cycle in the window (07-21T10:00Z, the dolt circuit-breaker
  cooldown case) — already root-caused and fixed as gc-372.1 (commit 6024920,
  worker redeployed 07-22T02:37Z).

## What continues collecting until the 07-23 gate

The order was promoted via change-control on 2026-07-22 (`orders/temporal-observe.toml`,
30m cadence) and appends one record per run. What accumulates that the
one-shot cannot show:

- **Tripwires.** `beads_with_temporal_metadata` and
  `workflows_in_waiting_phase` at every tick. A single non-zero voids the moot
  verdict and the delete recommendation. Records with `incomplete: true` (a
  failed state query, or a corrupt dedup state file) read as unknown, never as
  a clean zero.
- **A durable execution record past the 24h retention.** Today's "3 days"
  claims about executions rest on bead-store and execstore archaeology; the
  JSONL builds the retained series the namespace itself discards.
- **Skip-streak visibility** via `synthetic_selection_refs` per tick — whether
  the current streak clears when gc-wj5s/gc-y8zp/gc-lehu close, or persists
  into the gate reading.

What no amount of observe-mode collection can show: whether a future
fanout/gated regime would need event bridges over the reconcile-style scan
mesh. That is a design question for after the gates, and the honest answer
today is that dispatch-only never poses it.
