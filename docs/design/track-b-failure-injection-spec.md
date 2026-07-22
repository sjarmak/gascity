# Track B — 12-case failure-injection spec (gc-4zf.8)

_2026-07-21 · spec for bead gc-4zf.8, adopting the course 12-case matrix recorded on
gc-4zf.5 into Track B (gc-4zf.2). Doc only: nothing here has been run._

> **EXECUTION BLOCKED.** Every case in this spec is execution-blocked on the
> **gc-372 clean-week soak gate (~2026-07-23)** — Track B execution stays parked
> until that gate closes. In addition, every case marked **MEMORY-GATED** below
> touches the Temporal execute path (starts/signals/cancels live workflows, arms
> a `.disabled` order, manipulates the armed worker, or dispatches real beads)
> and is **also blocked on the memory gate, bead gc-qaid**. Observe-only work
> (replay against recorded histories, dev-server rehearsals, the already-proven
> throwaway-bead injection) is exempt from the memory gate but still waits on the
> soak gate. Do not build or run gated cases before both applicable gates close.

## 1. Scope and sources

The course matrix ("Temporal for Gas City" Module 7, recorded on gc-4zf.5) lists
12 injection cases. gc-4zf.4's chaos test and the ~90-test `-race` suite in
`services/temporal-maintenance/` already exercise most of the single-worker
surface; the bead calls out **#6, #7, #9, #12** as the gaps a single-worker soak
cannot reach — those four are marked **[ADDITION]** and go into the harness
first. This spec binds every case to real components of this install, current as
of commit `6024920` (the gc-372.1 Preflighter fix: pre-claim reads are retryable
and stamp nothing; permanent validation defects surface as
`PermanentPreflightError` and record terminally; fail-closed at-most-once is
reserved for the mutation).

Non-negotiables the cases test against (epic gc-4zf):

1. Temporal watches, never owns — beads/dolt is the source of record.
2. No workflow-per-bead — workflows are per orchestration episode
   (`gascity-maintenance/{repo}/{cycleKey}`), keyed off bead identity.
3. Never block an Activity on an agent tmux session — dispatch, return the bead
   id, wait on Signal or reconcile.
4. A reliability layer is a query, not a memory — re-derive state from SQL each
   tick; no edge-triggered state accumulation.

Course-term mapping: the course's "molecule" is this install's selection bead +
`gc-sling` (the maintenance path slings `--no-formula`; no gc molecule is
involved). "Completion signal" maps to the typed Signals in `workflow.go`
(`bead.closed`, `ci.completed`, `review.completed`, `agent.escalation`), whose
production carriers are the **still-`.disabled`** orders
`orders/temporal-maintenance-signal-*.toml.disabled` — the live dispatch-only
soak uses no signals at all (deploy/CUTOVER.md), so cases #3 and #6 exercise the
fanout path that Track D inherits, not the current soak path.

## 2. Component inventory (the real names)

| Component | Where | Role in injections |
|---|---|---|
| `temporal-server.service` | systemd --user; `start-dev`, sqlite `~/.local/share/temporal-maintenance/state.sqlite`, 127.0.0.1:7233, ns `maintenance`, MemoryHigh=768M/Max=1G | durable store under test; never stopped by the harness |
| `temporal-maintenance-worker.service` (+ `deploy/worker-armed.conf` drop-in) | armed: `TEMPORAL_MAINT_ARMED=1`, execstore `~/.local/share/temporal-maintenance/execstore`, MemoryHigh=1536M/Max=2G, Restart=always/5s | SIGKILL / SIGSTOP / memcg / config-change target |
| Schedule `maintenance-cycle` | 120m interval, overlap-policy **Skip**, dispatch-only input | overlap-suppression surface (a parked or wedged cycle silently skips every later fire) |
| `MaintenanceCycleWorkflow` | `workflow.go`; task queue `temporal-maintenance-shadow`; per-fire cycleKey from `workflow.Now` | the episode under injection |
| `Activities.DispatchSelection` | StartToClose 5m; retry 5s / ×2 / 60s cap / 8 attempts (~4m coverage) | the crash/timeout window |
| `RealAdapter` + `KeyStore` | `real_adapter.go`, `execstore.go`; atomic exclusive-link claim; pending/done/failed; `Quarantine`; `Preflighter` pre-claim read | at-most-once ledger; poison semantics |
| `FileEscalator` | fsync'd JSONL append, `…/execstore/escalations.jsonl` | the durable detection artifact for poisoned-pending |
| `bin/temporal-soak-check` + `orders/temporal-soak-check.toml` | checks A schedule-paused/missing, B workflow-failed, C no-recent-fire, D orphan-bead, E nothing-flowing, plus temporal-unreachable; 2h cadence; fingerprinted dedup; audit log `.gc/temporal-soak-check.log` | the production detector every case must RED-prove |
| `bin/temporal-fault-inject` | RED-proof triad (DETECT / LIVE-SAFE / RESTORE), throwaway `soak-shadow-throwaway` bead, isolated state file, dry-run soak-check, ms latencies | the harness skeleton all cases extend |
| Signal bridge | `signaler.go`, `reconciler.go`, `cmd/temporal-signal`, `cmd/maintenance-reconcile`, `bin/temporal-maintenance-signal-*` (orders `.disabled`) | cases #3/#6 carriers |
| Canonical dolt sql-server | port 29620, currently cgroup-nondeterministic (gc-qaid), memcg-killed repeatedly on 2026-07-21; supervisor circuit breaker | case #10's real outage source — observed, never induced |
| tmux socket `ds-research` / `gascity-supervisor` | agent sessions; supervisor | case #7 agent-side death (harness-owned session only); supervisor restart is the standing control |

## 3. Method (applies to every case)

- **RED-proof triad, non-negotiable** (gc-4zf.2): each detection assertion must
  be shown to (1) FIRE against the broken state, (2) NOT fire in live-safe mode
  (`TEMPORAL_SOAK_INCLUDE_SHADOW=0`, what the real `--push` order runs), and
  (3) go green after restoration. An unproven negative is vacuous — three
  soak-check assertions were caught passing against broken code this way.
- **Kill on observed state, never on a sleep** (gc-4zf.2 notes): poll for the
  precondition that defines the window (first pending execstore record; first
  new `maintenance-cycle` bead), then act. A fixed sleep banks false passes —
  proven on the first 07-17 run (killed 37ms after claim, before the bead
  existed, criterion passed trivially).
- **Throwaway-only blast radius** (mayor ruling 2026-07-17): throwaway cycles
  with harness-owned cycleKeys and `soak-shadow-throwaway`-labeled beads;
  never kill a real polecat, never restart the live city, never inject against
  upstream work; deliberate chaos workflows absorbed into the soak-check
  fingerprint; unconditional cleanup on exit; disclosure of residue per run.
- **Measurement definition** — the epic's headline metric, three timestamps:
  - **V** (violation): the instant the injected fault holds (observed state, not
    the command's issue time).
  - **D** (durable detection): first *durable* artifact naming the fault — an
    `escalations.jsonl` append, or a `red` event in `.gc/temporal-soak-check.log`.
    A journal line or an in-memory workflow error is not durable detection.
  - **R** (verified restoration): the detector green again on the same subject
    (RECOVERED transition / clean re-run) **and** artifact-level truth restored
    (bead closed or landed, key adjudicated, cycle dispatching).
  Report **V→D and D→R separately** — on 07-17, V→D collapsed from infinite to
  5m00s while D→R stayed 0% automated; the second number is the honest one.
  Each case records both the *mechanism* latency (immediate dry-run soak-check,
  `ORPHAN_MIN=0`, isolated state) and the *production-path bound* (2h order
  cadence for soak-check findings; 5m01s StartToClose for escalations).
- **Substrate precondition**: soak-check is an order, and orders are what dies
  silently (gc-qo3). A measured D is only valid if the order actually fired —
  verify via `gc order history temporal-soak-check` or the audit log's `tick`
  events, not by running the script by hand and calling it the production path.
- **Standing control (run each harness session, cheap):** `systemctl --user
  restart gascity-supervisor` while a throwaway cycle is parked — the P4-proven
  failure-domain-independence positive. Expected: server, worker, and workflow
  untouched; **zero** soak-check findings. This proves the harness can also
  detect a non-failure (no false red), the other half of non-vacuousness.

## 4. The 12 cases

### Case 1 — worker crash during each Activity

- **Invariant violated:** fail-closed at-most-once must survive a crash at any
  point in an Activity, **and the resulting drop must be loud** (gc-4zf.4: the
  refusal was correct; the silence was the defect).
- **Injection:** `kill -9 $(systemctl --user show -p MainPID --value
  temporal-maintenance-worker.service)` on an observed window, parameterized:
  (a) first pending execstore record (pre-create crash — no bead, no orphan);
  (b) first NEW `maintenance-cycle` bead (mid-sling crash — orphan; the 07-16
  repro); (c) during `ProposeExternalAction` (gated path; dev-server only —
  `restart_test.go` shape). Variant (d), the realistic vector: transient
  `systemctl --user set-property temporal-maintenance-worker.service
  MemoryMax=64M` to take the kill from the kernel memcg instead of the shell;
  restore the property afterward.
- **Durable detection:** on Temporal redelivery (~5m01s), `RealAdapter` finds
  the pending claim, `Quarantine` flips it pending→failed, `FileEscalator`
  appends to `escalations.jsonl`; workflow FAILED; soak-check **B**
  (workflow-failed) and, for window (b), **D** (orphan-bead) go red within one
  2h tick.
- **Verified restoration:** poisoned key reads `failed` with the quarantine
  reason; throwaway orphan closed; next cycle dispatches; soak-check RECOVERED
  on both keys. Restoration is manual today (Track A residue 9.5a/b) — measure
  it, do not paper over it.
- **Measurement:** V = kill on observed window; D = `escalated_at` in
  escalations.jsonl (measured 5m00s on 07-17) and the soak-check red tick
  (bound ≤2h); R = orphan closed + RECOVERED. Known datapoints: V→D infinite
  (07-16, broken) → 5m00s (07-17, fixed); D→R manual.
- **Gates:** soak + **MEMORY-GATED** (kills the live armed worker mid-real-dispatch).
- **Status:** proven twice (gc-4zf.4 acceptance); harness re-run generalizes the
  window parameter. Known residue: escalation `bead_ref` is the synthetic
  target, not the real bead id — assert on the cycle key, not the id.

### Case 2 — duplicate Activity execution

- **Invariant violated:** at-most-once per idempotency key — a redelivery must
  answer from the `KeyStore`, never re-run `gc bd create`/`gc-sling` (a
  duplicate polecat against real upstream work is the worst outcome the design
  accepts trading a skipped cycle to avoid).
- **Injection:** force redelivery with the first attempt's outcome already
  recorded: (a) dev-server duplicate `Propose` with an armed adapter against a
  throwaway store (the `staging_test.go` shape, `TEMPORAL_MAINT_STAGING=1`);
  (b) live: SIGSTOP the worker after `Complete` is on disk but before the
  activity result reaches the server, wait past StartToClose, SIGCONT — the
  retry must return the recorded mutation with `created=false`. Also re-assert
  the 6024920 property: a duplicate delivery answers from the store **while the
  preflight backend (dolt) is down** (pair with case 10's shim).
- **Durable detection:** the violation direction is a **second bead** — count
  `gc bd --rig gascity list --label 'maintenance-cycle:<half>'` before/after.
  **Gap:** no production check detects a duplicate bead (soak-check has no
  count-invariant); the harness asserts it directly, and the gap is Track E
  evidence.
- **Verified restoration:** exactly one bead per (repo, cycle, branch) key;
  execstore shows one record, `done`, with the real bead id as `result_ref`.
- **Measurement:** V = redelivery observed (attempt 2 scheduled); D = harness
  assertion (immediate); R = n/a when the invariant holds — record
  duplicate-side-effect count (target 0) as the secondary metric.
- **Gates:** soak; variant (a) observe-only (dev-server/staging store), variant
  (b) **MEMORY-GATED**.

### Case 3 — duplicate + out-of-order signals

- **Invariant violated:** #4 — workflow state must converge from durable truth
  regardless of signal ordering or duplication; a signal is at-least-once by
  re-drive, and re-application must be idempotent.
- **Injection:** harness-owned throwaway fanout-path cycle (NOT dispatch-only)
  on the shadow task queue; drive with `Signaler`: deliver `review.completed`
  before `ci.completed`, duplicate each, interleave a duplicate `bead.closed`,
  and run `bin/temporal-maintenance-signal-bead-closed` twice for the same bead
  (its unlocked state file makes the duplicate a documented workflow-idempotent
  no-op — verify that claim). Dev-server first
  (`TestIntegration_Bridge_SignalerDrivesWorkflow` is the base); live variant
  requires arming the `.disabled` signal orders.
- **Durable detection:** `state` Query — each verdict applied once, `bead_ids`
  without duplicates, phase advances exactly once past AwaitingEvents. The
  violation direction (double-applied verdict / duplicate bead id in state) has
  no production detector — harness-asserted; a Temporal-vs-beads divergence
  check is the standing gap (secondary metric of gc-4zf.2).
- **Verified restoration:** cycle completes with the same terminal state a
  clean-ordered run produces (state diff empty).
- **Measurement:** V = first out-of-order/duplicate delivery; D = harness state
  assertion (immediate); R = terminal-state equivalence.
- **Gates:** soak; dev-server variant observe-only; live variant
  **MEMORY-GATED** (arming a signal order is execute-path expansion).

### Case 4 — bead already closed before the workflow starts

- **Invariant violated:** #1 — beads/dolt is the source of record; the episode
  must re-derive in-flight truth from the store at start (the `Preflighter`
  pre-claim read), not from any remembered edge.
- **Injection:** three prongs against the preflight read
  (`ExecRunner.halfInflight`, `gc bd list --status open --label
  maintenance-cycle:<half>`): (a) create a same-half labeled throwaway bead and
  **close** it before the next fire — the cycle must NOT skip (closed beads are
  excluded from the open scan); (b) leave it **open** — the cycle must skip,
  recording `skipped-inflight` durably (the RED direction for the guard);
  (c) close it between the preflight read and the claim — the accepted TOCTOU,
  bounded by Skip-overlap; record the observed behavior as evidence, not a
  failure. Signal-path prong: `bead.closed` for a bead whose workflow already
  completed → the exec must fail-soft no-op (also case 6's late half).
- **Durable detection:** (a) violation = a false `skipped-inflight` execstore
  record + `DispatchSelectionResult.Skipped=true` with no open bead in the
  store; (b) correct skip = the same record, asserted present. Soak-check E
  would surface systematic false-skips only as zero-dispatch after ≥2 cycles —
  the per-cycle assertion lives in the harness.
- **Verified restoration:** close/remove the throwaway; next cycle dispatches
  both halves (two real `gc-*` ids in the workflow result).
- **Measurement:** V = bead state set; D = execstore record read (immediate);
  R = next-fire dispatch bound (≤120m production, immediate via a manual
  throwaway-cycle start in the harness).
- **Gates:** soak + **MEMORY-GATED** for live cycle starts; prong (b)'s
  detection half is bead-only and observe-only.

### Case 5 — selection created but routing fails (bead created, never slung)

- **Invariant violated:** no silent half-dispatch — a created-but-unrouted bead
  is a dropped dispatch (Track A §1.1, the gc-4qz/gc-s5c/gc-q4s class).
- **Injection:** (a) **already built and green**: `bin/temporal-fault-inject
  orphan-bead` — throwaway open bead, `routed_to` unset, `soak-shadow-throwaway`
  label, `ORPHAN_MIN=0`, isolated state, dry-run soak-check; RED-proof triad
  passed live 2026-07-18 (gc-gwu7: detect ~7.5s, restore ~2.3s, zero leak).
  (b) live-flavor: a PATH shim `gc-sling` that exits nonzero for one call —
  `runSelection` returns the bead id **with** an error, the key records
  `failed` with the partial `result_ref` (bead id), the bead is orphaned.
- **Durable detection:** soak-check **D** names the bead; execstore `failed`
  record carries the structured `result_ref`; live `--push` order escalates to
  #gascity-maintenance + mayor nudge (excluded for shadow artifacts —
  LIVE-SAFE proven).
- **Verified restoration:** throwaway force-deleted (trap) → detector green;
  for (b), orphan closed + shim removed + next cycle slings.
- **Measurement:** V = orphan exists; D = check-D red (measured 7.5s forced;
  ≤2h production); R = detector green (measured 2.3s forced).
- **Gates:** (a) observe-only, memory-gate-exempt — but buildable and
  runnable only once the soak gate closes; the 2026-07-18 run is history
  under gc-4zf.2's then-authorized scope, not standing authorization to
  re-run. (b) **MEMORY-GATED**.

### Case 6 — agent finishes before the completion signal arrives [ADDITION]

- **Invariant violated:** #4 — an event-shaped completion is lossy at the
  boundary; the layer must reconcile from durable truth, not depend on the
  edge. A completion that lands before the workflow waits (or after it stops
  waiting) must not strand the cycle.
- **Injection:** throwaway fanout-path cycle; three timing prongs using
  `bin/temporal-maintenance-signal-bead-closed` / `Signaler` against
  harness-owned beads: (a) close the tracking bead while the workflow is still
  in Selecting (signal precedes the wait — Temporal buffers it in history;
  verify the select loop drains it); (b) suppress the signal entirely (order
  exec "crashed") and let the cycle park in AwaitingEvents — the loss window;
  (c) deliver after workflow completion — exec must fail-soft. Then run
  `cmd/maintenance-reconcile` from ground truth for (b).
- **Durable detection:** for (b), the parked cycle plus **Skip-overlap** means
  every later Schedule fire is skipped — soak-check **C** (no-recent-fire)
  reds at 2× cadence (240m) because no new cycle *starts*. **Gap:** nothing
  detects "cycle parked in AwaitingEvents past N hours" directly (C is a
  proxy via start-recency; B/E never see a RUNNING workflow); the reconcile
  *sweep* order deferred at P4/P5 is the intended cover — name it in Track E.
- **Verified restoration:** `Reconciler.Reconcile` returns a non-empty repaired
  list (e.g. `review:review`), the cycle advances and completes; subsequent
  fires resume.
- **Measurement:** V = truth exists with no signal observed (bead closed /
  verdict posted, workflow still AwaitingEvents); D = check-C red (bound
  ~240m — a poor bound; the gap above is the finding); R = reconcile-repair to
  cycle completion (seconds once run; production cadence for the sweep is
  unset — that unset number is itself Track E input).
- **Gates:** soak + **MEMORY-GATED** (fanout cycle start + signal delivery);
  dev-server rehearsal (`TestIntegration_Bridge_ReconcilerRepairsDroppedEvent`
  extends) is observe-only.

### Case 7 — cancellation during an agent run [ADDITION]

- **Invariant violated:** #3 — the Activity never owns the agent session, so a
  Temporal-side cancel must not reach into agent work invisibly, and an
  agent-side death must not corrupt workflow state. Also: a cancel must not be
  a *silent* drop (the gc-4zf.4 shape via a different exit).
- **Injection:** two prongs, both throwaway-only (mayor ruling — never a real
  polecat): (a) Temporal-side: `temporal workflow cancel` on a throwaway cycle
  (i) mid-`DispatchSelection` — **cancel never reaches the running Activity.**
  This package registers no heartbeats (no `HeartbeatTimeout` in the
  ActivityOptions, no `activity.RecordHeartbeat` anywhere), and the Go SDK
  delivers activity cancellation only in heartbeat responses — so the
  activity ctx is never canceled, `exec.CommandContext` never signals the
  `gc-sling` child, and the abandoned attempt runs to completion: bead
  created AND slung, key finishing `done`, while the workflow unblocks with a
  canceled error and ends CANCELED. The observable under test is exactly that
  divergence — a real dispatched bead behind a CANCELED cycle — NOT an
  orphan and NOT a poisoned claim. (Adding a `HeartbeatTimeout` + heartbeat
  loop would flip this semantics: cancel would then propagate to ctx and
  SIGKILL the child, re-opening case 1(b)'s orphan window via cancel. That is
  a design decision this case's evidence feeds, not current behavior.)
  (ii) parked post-dispatch — the dispatched bead and its agent must be
  untouched (Temporal watches, never owns). (b) Agent-side: dispatch the
  throwaway bead to a harness-owned tmux session on the `ds-research` socket,
  then `tmux -L ds-research kill-session` on it after claim — the workflow
  (dispatch-only, already done) must be unaffected; the stall belongs to the
  city's query layer (stale-claim-reaper class), not to Temporal.
- **Durable detection:** (a-i) **two stacked gaps, and they are the case's
  point.** The completed side effect leaves no orphan (bead is routed →
  check D stays green) and no poison (key is `done` → no escalation), and
  soak-check **B** filters `FAILED|TERMINATED|TIMED_OUT` and does **not**
  count `CANCELED` — the canceled cycle itself is invisible to every
  production check (G4). The slung-bead-vs-CANCELED-cycle divergence is
  computable only by diffing execstore/`gc bd` truth against workflow status,
  which nothing in production does (G2). The harness asserts both directly:
  the `done` record + routed bead present, no escalation and no check-D red
  fired (the absence is part of the assertion), production D reported as
  infinite. Fix B's filter (or record the exclusion as deliberate) before
  this case runs; that decision is itself an output. (b) detection is
  explicitly out of Temporal's scope — assert the boundary: workflow state
  unchanged, bead still open+routed, city-side reaper class named.
- **Verified restoration:** canceled cycle followed by a normal next fire
  (CANCELED is not RUNNING, so Skip-overlap does not suppress); the throwaway
  bead the abandoned attempt dispatched closed; harness session reaped.
- **Measurement:** V = cancel delivered / session killed on observed state;
  D = (a-i) harness divergence assertion (immediate; production bound
  **infinite** — the G4 + G2 finding); (b) boundary assertion (immediate);
  R = throwaway bead closed + next-fire dispatch + cleanup.
- **Gates:** soak + **MEMORY-GATED** (live cancel + real dispatch to a
  harness session).

### Case 8 — timeout then late completion

- **Invariant violated:** at-most-once when the attempt outlives its
  StartToClose — the documented accepted risk in `real_adapter.go` `settled()`:
  a redelivery observing `pending` while the original attempt is still
  mid-`Run` quarantines a claim whose command may still land.
- **Injection:** primary vector: a **whole-worker reclaim-throttle stall** —
  transiently tighten `MemoryHigh` on `temporal-maintenance-worker.service`
  while a sling is in flight (the measured 07-16 incident: 12k+
  reclaim-throttle events, per the `worker-armed.conf` comment). The stall
  freezes the worker's own goroutines — including the `Fail`-recording path
  after the deadline kill — so the redelivery observes `pending` →
  quarantines + escalates; when the throttle lifts, the late original's
  `Complete`/`Fail` must hit `KeyStore.finish`'s "already failed, refusing to
  overwrite" refusal. A child-only stall (PATH-shim `gc-sling` sleeping past
  5m, or SIGSTOP on the child PID) does **not** reach this window and is kept
  only as a control: the activity ctx carries the StartToClose deadline, so
  `exec.CommandContext` SIGKILLs the child at ~5m (SIGKILL takes even a
  SIGSTOPped process), `Run` returns, and `Fail` is on disk ~5s before the
  first redelivery — which then reads `failed` and takes the terminal
  `settled()` path: no quarantine, no divergence. Residual true-late vector
  to log per run: a **detached `gc-sling` grandchild** — `CommandContext`
  kills only the direct child, and a detached grandchild holding the
  inherited output pipe keeps `CombinedOutput` (and therefore the `Fail`
  record) blocked past the kill while it can still land the side effect late.
- **Durable detection:** `escalations.jsonl` quarantine record; workflow
  FAILED → soak-check **B**; if the late sling landed, the bead is *routed*
  while its key reads `failed` — Temporal-vs-beads **state divergence**, which
  no production check computes (secondary-metric gap; harness asserts by
  diffing execstore against `gc bd` metadata).
- **Verified restoration:** human adjudication of the diverged pair (close or
  adopt the bead), refused-overwrite error captured as evidence, next cycle
  clean.
- **Measurement:** V = StartToClose expiry with the sling still in flight
  under the stall; D = `escalated_at` (~5m01s from activity start); R =
  adjudication (manual — measure it). Control (child-only stall): assert
  `failed`-at-redelivery with NO quarantine — this case's LIVE-SAFE
  direction. Secondary: divergence count, duplicate side effects (target 0).
- **Gates:** soak + **MEMORY-GATED**.

### Case 9 — config change during execution [ADDITION]

- **Invariant violated:** an in-flight episode must not silently change
  semantics when worker config changes under it; determinism holds because
  config reaches the workflow only via Activity results — the *observable* risk
  is a half-armed cycle.
- **Injection:** between the review-half and author-half `DispatchSelection`
  of a throwaway cycle (kill-on-observed-state: first half's `done` record),
  change worker config and restart: (a) remove
  `~/.config/systemd/user/temporal-maintenance-worker.service.d/armed.conf` +
  `daemon-reload` + restart — the retry/second half runs under
  **DryRunAdapter** and records a synthetic `temporal-shadow/...` id: one real
  bead, one shadow — a half-armed cycle; (b) change
  `TEMPORAL_MAINT_POLECAT`/prompt paths mid-cycle — second half dispatches to a
  different target; (c) `temporal schedule update` interval/overlap mid-window.
  Also capture the 6024920 Codex MINOR as evidence: already-scheduled
  activities keep the **old** RetryPolicy until resolved — a policy change does
  not reach in-flight timers.
- **Durable detection:** **gap** — soak-check **E** counts beads dispatched
  (>0 → green), so a half-armed cycle passes every production check. The
  harness must assert per-cycle "both halves resolved to real `gc-*` ids" from
  the workflow result (`bead_ids` length + prefix); promoting that assertion
  into soak-check E is a recommended follow-on.
- **Verified restoration:** armed.conf restored + restart + next cycle
  dispatches two real halves; stray shadow/mistargeted bead cleaned.
- **Measurement:** V = restart with changed config while a cycle is in flight;
  D = harness per-cycle assertion (immediate; production bound today:
  **infinite** — report it); R = next full dispatch.
- **Gates:** soak + **MEMORY-GATED** (armed-worker manipulation mid-real-cycle).

### Case 10 — Beads backend unavailable, then restored

- **Invariant violated:** the gc-372.1 discipline — a transient dolt outage on
  a pre-claim read must retry, stamp nothing, and never terminally fail the
  cycle; fail-closed is reserved for the mutation; duplicates stay answerable
  from the store while dolt is down.
- **Injection:** never stop the canonical dolt (23-rig store; hard Don't).
  Instead: (a) PATH-shim `gc` for the worker that returns the circuit-breaker
  error for a window **shorter** than the retry budget (~4m: 5s/×2/60s cap/8
  attempts) → cycle must complete after retries with a clean execstore (no
  record from the read failures); (b) window **longer** than the budget →
  retries exhaust, workflow FAILED, but **no poisoned pending** (nothing was
  claimed) — the failure mode is loud-and-clean, not poison; (c) opportunistic,
  not induced: the real memcg-kill storms (gc-qaid: dolt killed twice on
  2026-07-21; breaker cooldown 5s) — instrument the next storm and measure the
  production behavior of the deployed 6024920 code against it.
- **Durable detection:** (b) soak-check **B**; (a) nothing should fire —
  LIVE-SAFE direction; harness asserts execstore untouched during the outage
  window and cycle latency stretched by the retry backoff.
- **Verified restoration:** shim removed / breaker closed → in-window retries
  complete the same cycle (a), or the next fire dispatches cleanly (b).
- **Measurement:** V = first failed preflight read; D = cycle completion delta
  (a) or B red (b, ≤2h); R = first clean dispatch after restore. Secondary:
  terminal keys created during outage (target 0).
- **Gates:** soak + **MEMORY-GATED** for shim variants (live worker); prong (c)
  is pure observation, exempt.

### Case 11 — workflow replay against recorded histories

- **Invariant violated:** determinism — current code must replay every recorded
  history cleanly (`branchOrder` fixed slice, `workflow.Now`, no map ranging).
- **Injection:** (a) existing `replay_test.go` against
  `testdata/maintenance_cycle_history.json`; (b) extend the corpus with LIVE
  soak histories: `temporal workflow show --workflow-id <id> --output json`
  (read-only) for a completed dispatch-only cycle, a FAILED chaos cycle, and a
  skipped-inflight cycle, replayed via the SDK replayer; (c) RED proof: a
  scratch build with a deliberate nondeterminism (range a map for branch
  iteration) must FAIL replay — never deployed, build-and-test only.
- **Durable detection:** replayer error is the detection; wiring the replay run
  into `city-selftest` (6h) makes it durable and recurring — recommended
  follow-on, since replay only fires at code-change time otherwise.
- **Verified restoration:** scratch change reverted; full corpus replays green.
- **Measurement:** V = nondeterministic build present; D = replay failure
  (seconds); R = green corpus. Production bound: detection currently happens
  only when someone runs the suite — record that as the gap this case closes.
- **Gates:** **soak gate only — fully observe-only/read-only; exempt from the
  memory gate.** With 5(a), first in line once the soak gate closes; nothing
  in it builds or runs before then.

### Case 12 — old workflow code alongside a new worker [ADDITION]

- **Invariant violated:** an episode started under old code must complete
  correctly under a newly deployed worker binary — the versioning discipline
  gc-4zf.9 gates on (current Worker Versioning API / `workflow.GetVersion`,
  not the pre-2025 experimental path being removed ~March 2026). Corollary
  invariant from gc-4zf.4: the *deployed binary* is the unit under test —
  "unit tests cannot tell you which binary is live."
- **Injection:** park a throwaway cycle at a wait boundary (fanout
  AwaitingEvents; or mid-retry backoff), then: (a) benign rebuild — `make
  build` + `systemctl --user restart temporal-maintenance-worker.service` with
  unchanged workflow code → cycle resumes and completes (the `restart_test.go`
  property, proven on the deployed path); (b) a workflow-definition change
  WITHOUT `GetVersion` (insert/reorder a command) → resume → expect a
  NonDeterministicError workflow-task failure: the workflow does not FAIL, it
  retries the workflow task forever and shows RUNNING; (c) the same change
  guarded by `workflow.GetVersion` → clean resume. Dev-server first; live
  variant second. Every run asserts binary identity via `strings
  /proc/$(systemctl --user show -p MainPID --value
  temporal-maintenance-worker.service)/exe | grep <new-symbol>`.
- **Durable detection:** **gap, and it is the case's point:** a
  workflow-task-retrying cycle is RUNNING — invisible to soak-check B (not
  failed), E (not completed), and D (bead may be fine); C only fires once
  Skip-overlap has suppressed 2× cadence of new starts (~240m proxy). A
  "RUNNING cycle older than N× expected duration" check does not exist; this
  case produces the evidence for adding it.
- **Verified restoration:** roll back the binary (rollback copy is kept
  pre-flight per the 07-17 deploy practice) or ship the `GetVersion`-guarded
  build → workflow task succeeds, cycle completes; corpus replay (case 11)
  green against the new code.
- **Measurement:** V = new binary serving the old history (first workflow-task
  failure); D = check-C proxy (~240m) vs the direct check (absent — report
  infinite until built); R = rollback/fix to cycle completion.
- **Gates:** soak; dev-server variants observe-only; live restart against a
  real parked cycle **MEMORY-GATED**.

## 5. Detection-gap register (harness output feeding Track E)

Found by writing this spec against the deployed detector set; each is evidence
about where the production detection surface is thin, which is Track B's actual
product:

| # | Gap | Surfaced by case |
|---|---|---|
| G1 | No duplicate-bead count invariant anywhere in production | 2 |
| G2 | No Temporal-vs-beads state-divergence check (execstore vs `gc bd` truth) | 3, 7, 8 |
| G3 | No "cycle parked in AwaitingEvents > N" check; check C is a ~240m proxy via Skip-overlap | 6 |
| G4 | soak-check B ignores `CANCELED` executions | 7 |
| G5 | Half-armed / half-dispatched cycle passes check E (beads>0 = green) | 9 |
| G6 | Replay runs only at code-change time; not wired into city-selftest | 11 |
| G7 | No "RUNNING cycle older than N× expected duration" check (stuck workflow task invisible) | 12 |

## 6. Latency ledger (bounds to measure against)

| Path | Bound / measured | Source |
|---|---|---|
| Claim written after trigger | ~40ms | gc-4zf.2 notes |
| Bead created after trigger | ~1s | gc-4zf.2 notes |
| systemd worker restart | ~5–6s | Restart=always/5s, measured |
| Temporal redelivery (StartToClose) | ~5m01s | measured 07-17 |
| Escalation V→D (poisoned pending) | 5m00s (was ∞ on 07-16) | escalations.jsonl |
| soak-check V→D, forced (dry-run, ORPHAN_MIN=0) | ~7.5s detect / ~2.3s restore | temporal-fault-inject live run 07-18 |
| soak-check V→D, production | ≤2h (order cadence) + ~0.6s runtime | orders/temporal-soak-check.toml |
| Check-C proxy for parked/stuck cycles | ~240m (2× cadence) | cases 6, 12 |
| Selection retry budget (dolt outage ride-out) | ~4m (5s/×2/60s/8) | workflow.go ActivityOptions |
| Restoration | 0% automated today — measure per case | gc-4zf.2 headline note |

## 7. Acceptance (mirrors gc-4zf.8)

The Track B harness enumerates all 12 cases with pass/fail plus the measured
violation → durable detection → verified restoration latency per case (V→D and
D→R reported separately). Cases #6/#7/#9/#12 go into the harness first — they
are exactly what a single-worker soak cannot exercise. `bin/temporal-fault-inject`
is the skeleton (one injection per invocation, RED-proof triad, throwaway-only,
unconditional cleanup); `bin/temporal-soak-check` is the production detector
every case must RED-prove, and its planned retirement (~07-23) folds its five
checks into this harness as their permanent home. Blocked-by posture unchanged:
nothing in this spec executes until the gc-372 soak gate closes, and
MEMORY-GATED cases additionally wait on gc-qaid.
