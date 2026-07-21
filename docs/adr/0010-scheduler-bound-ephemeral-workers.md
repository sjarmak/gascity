# ADR-0010: Scheduler-Bound Ephemeral Workers — ready → bind → spawn, without agent-side claiming or nudges

**Date**: 2026-07-17
**Status**: proposed
**Deciders**: Stephanie (direction, 2026-07-16), mayor, city-infra-pl
**Bead**: dr-26g.1 (parent epic dr-26g — dispatch redesign)

## Context

Gas City's worker-dispatch model is **pull-based**. Persistent pool sessions
(`*-polecat`, `*-worker`) stay alive between beads, inspect ready work, race to
claim it via an assignee CAS, and depend on a nudge to notice newly routed
beads. Every load-bearing property of that model is held by a level-triggered
reconciler scan, not by the dispatch action itself, and the seams between
"routed", "claimed", and "running" leak in ways the city has catalogued
exhaustively (`docs/design/city-reliability-surface.md`, §3, §5, §10).

The concrete failure modes this model produces:

- **Fan-out races.** One bead concurrently claimed by four polecat slots, three
  editing the same shared branch with divergent uncommitted work (ADR-0009
  context, `gc-typpc`). ADR-0009 added a single-claim invariant at
  `cmd/gc/cmd_hook_claim.go`, but it is a *reject-the-second-claimer* guard on a
  path that still fundamentally lets N workers attempt the same bead.
- **No lease, no TTL, anywhere.** A claim orphaned by a dead worker is repaired
  only because `cmd/gc/pool_session_name.go:114-213` re-derives every reconciler
  tick whether the assignee still maps to a live session bead (`:533-573`) and
  reopens it. Grep-confirmed: no lease/TTL across `internal/{beads,dispatch,sling}`
  or `cmd/gc` (reliability-surface §5.6). Recovery works, but it is implicit in
  the session-liveness scan; there is no first-class binding to expire.
- **Nudge fragility.** A warm worker does not notice routed work until nudged.
  The nudge defaults to `wait-idle` (`cmd/gc/cmd_session.go:2374`), which
  *queues* on a busy session and reports success even when the queued item later
  dead-letters (`cmd/gc/cmd_nudge.go:37-41,720-748`) — a wake-up silently
  swallowed (upstream gascity#3924 item 2). `internal/nudgequeue` is a durable,
  flock'd `Pending/InFlight/Dead` queue with `Attempts/LeaseUntil/DeadAt`
  (`state.go:31-49`) — the city already has lease+fence semantics, but only on
  the *nudge* path, not on the *bind* path.
- **Routing partial-writes.** A sling that dies after routing but before
  attaching a formula leaves `routed_to` set with no assignee and no molecule —
  named in source (`internal/sling/sling_core.go:281-282`), healed only when a
  human re-runs `gc sling --on` (reliability-surface §3.2). A sling that dies
  before writing either field leaves a bead no scan can route (§3.1). The
  dispatch action is not atomic, so every intermediate state is reachable and
  some are unrecoverable.
- **Capacity is discovered after spawn, not reserved before it.** There is no
  slot reservation. The reconciler computes a desired awake set
  (`cmd/gc/compute_awake_set.go:118`) and tries to (re)spawn; a provider health
  gate (`cmd/gc/provider_health_gate.go:100`) only *then* red-lights a spawn onto
  an unhealthy account at reconcile time (`cmd/gc/session_reconciler.go:3361-3379`,
  fail-open when the registry is absent). Account selection proper (`csu_pick` /
  `claude-account`) lives in install tooling, outside the `gc` binary. Pool size
  is derived by polling a shell `scale_check` (`cmd/gc/pool.go:153-194`), and
  spawns are throttled to `DefaultMaxWakesPerTick = 5` per tick
  (`internal/config/config.go:2691`) — a rate limiter, not a queue-bound
  admission. So the fleet learns it is over capacity by failing to spawn, not by
  being unable to reserve.
- **Duplicate workflow roots.** No same-target/same-formula idempotency gate at
  instantiation; two review molecules fired 33s apart for one target
  (`gc-28jm`, open P0, §3.5). Uniqueness is convention, caught only by an
  incidental second-worker self-check.

The common root is that **discovery, admission, ordering, capacity binding, and
execution launch are collapsed into one racy pull performed by the worker
itself, after it is already running.** The worker is the scheduler, the claimant,
and the executor at once, and none of those roles has a durable record until
after the fact.

Stephanie's direction (2026-07-16): design the replacement control-plane
boundary. A durable readiness queue; a mechanical scheduler that atomically
binds one ready workload to one capacity slot *before* launch; an execution
controller that starts a worker already fenced to that exact workload. A worker
never runs `bd ready`, chooses work, races to claim, polls, or waits for a
nudge — it receives exactly one bead and executes it.

This is an architecture decision, not an implementation. It must respect two
hard-won findings from the reliability track that constrain every choice below:

1. **Every durable wakeup in this city reduces to (a durable timestamp in the
   store) + (a periodic scan)** (reliability-surface §10). The rate-limit
   backoff — the one wakeup that provably survives process death — is a
   `quarantined_until` field plus `healExpiredTimers` checking it against the
   clock each tick (`cmd/gc/session_reconcile.go:440-458`). Durable execution is
   good at *noticing* a stuck state; a *scan* is what makes the state right. The
   scheduler must be built as durable binding records reconciled by a level scan,
   not as an in-memory or external-engine timer service.
2. **The work layer wants at-most-once fail-closed** (a duplicate PR is worse
   than a skipped cycle) **and the recovery/watchdog layer wants at-least-once
   with idempotent repairs** (re-scan and re-enqueue are harmless). These two
   semantics cannot share a mechanism; a binder built on fail-closed execstore
   semantics would go silent exactly when it breaks (§10, `gc-4zf.4`). The bind
   is a work-layer action; the lease-expiry recovery is a watchdog-layer action.
   The ADR keeps them on separate paths.

## Decision

Introduce a **scheduling control plane** with four separated concerns and one
authoritative state machine over a durable **Binding** record. The bead remains
the unit of work and the dependency source; formulas remain the execution
recipe; **routing/eligibility classification remains model-owned** until it is
representable as structured fields. What changes is that *admission, ordering,
capacity reservation, atomic binding, fenced launch, and terminal detection
become mechanical operations on durable records*, and the worker becomes a pure
executor of a binding it did not choose.

The logical boundary has four roles. They are *logical* — Consequences and
"Process boundary" below make explicit that they may initially run inside the
existing reconciler process and are separated by module, not by deployment.

1. **Admission / readiness queue.** Decides which beads are *eligible* to be
   scheduled and materializes them into a durable ready queue. Structural
   feasibility only: dependencies satisfied, not already bound, pool/capability
   resolved, not quarantined, human-gate cleared. Semantic "should a human see
   this first" classification stays model-owned (a `needs-human` / `machine-ok`
   label produced upstream, dr-e83); admission reads the label, it does not
   decide it. Modeled on Kueue's two-phase admission (reserve under hard
   constraints, then admit).

2. **Scheduler / binder.** Reads only structured fields (priority class, enqueue
   time / aging, capability + pool, quotas, account health), selects one ready
   workload, reserves one capacity slot, and **atomically creates one Binding**
   that fences that workload to that slot. Ordering *policy* is deliberately
   minimal here (priority band + FCFS-within-band, which an 11-week replay showed
   captures nearly all the gain; `docs/research/agent-fleet-schedulers-survey-2026-07-13.md`);
   the richer lexicographic objective is out of scope for this ADR and tracked
   separately (dr-fol). The scheduler is deterministic math — it is inside the
   ZFC "allowed in code" set (policy enforcement, state/lifecycle, deterministic
   math); no model reasoning runs in the bind path.

3. **Capacity / account manager.** Owns the slot inventory: per-pool and global
   worker caps, per-account/provider quotas and health (rate-limit quarantine),
   warm-floor and scale-to-zero policy. Grants a **reservation** (a hold on one
   slot) to the scheduler *before* bind and reclaims it on terminal. Reservation
   is a durable record, not an in-memory counter, so a scheduler crash between
   reserve and bind cannot leak a slot (the reconciler reclaims unreferenced
   reservations).

4. **Execution controller.** Given a committed Binding, launches a worker
   process **already fenced to exactly its bead** — the bead id + fencing token
   are injected into the spawn environment alongside the identity vars the tmux
   provider already sets (`cmd/gc/template_resolve.go:276-296`), instead of the
   worker self-discovering via `work_query` + `gc hook --claim`. It tracks
   lifecycle, renews the lease while the worker is alive, and detects terminal
   state by **artifact movement**, not by bead status or nudge. Scale-to-zero by
   default; the controller is what a warm floor keeps pre-started.

### The authoritative state machine

One Binding record per (workload, generation) moves through exactly these
states. The Binding — not the bead's `status`, not the session's liveness — is
the source of truth for "is this work being executed, by what, under what
fence." The bead keeps its own `open/in_progress/closed` lifecycle; the Binding
is a *separate, additive* record that references the bead.

```
                   ┌─────────────────────────── retry (attempt++) ──────────┐
                   │                                                          │
  queued ──▶ ready ──▶ bound ──▶ starting ──▶ running ──▶ terminal            │
    │          │         │          │            │            │               │
    │          │         │          │            │       (succeeded|          │
    │          │         │          │            │        failed|             │
    │          │         │          │            │        no-op|              │
    │          │         │          │            │        abandoned)          │
    │          │         │          │            │                            │
    └──────────┴─────────┴──────────┴────────────┴──▶ retry ─────────────────┘
                (lease expiry / crash / launch failure, if attempts remain)
```

- **queued** — bead exists and is a dispatch candidate; dependencies may still be
  open. Not yet eligible. (Corresponds to today's "open, routed or unrouted".)
- **ready** — admission cleared: dependencies satisfied, pool/capability
  resolved, not quarantined, human-gate passed, not already bound. Materialized
  in the durable ready queue with an `enqueued_at` timestamp (the aging clock).
- **bound** — the scheduler has atomically committed a Binding: one workload, one
  reserved slot, a `generation`, `attempt`, a `fencing_token`, and a `lease_until`
  deadline. This transition is the single atomic admission point; it is what makes
  a second concurrent bind for the same workload impossible (see "Atomic bind").
- **starting** — the execution controller is launching the worker (tmux session /
  provider fork). The lease is held; the slot reservation is consumed.
- **running** — the worker is alive and executing; the controller renews
  `lease_until` on a heartbeat derived from session liveness.
- **terminal** — a terminal *outcome* is recorded, keyed off **artifact
  movement** (ADR-0009's typed close: `shipped` requires a commit on the stamped
  branch; `no-op`/`blocked`/`abandoned` require a reason). The slot reservation is
  released here, not before.
- **retry** — a non-terminal death (lease expired with the worker gone, launch
  failure, crash before running) re-enqueues the *workload* at `attempt+1` with a
  fresh `generation`, provided `attempt < max_attempts`; otherwise it goes to a
  durable **dead** outcome that escalates (never silently drops).

State lives in typed bead metadata (ADR-0003) so it is queryable in the same
store the reconciler already scans — consistent with §10's "put the pending state
in the store, then scan it." Proposed keys (subject to the `beadmeta` codegen
ritual): `gc.binding_state`, `gc.binding_generation`, `gc.binding_attempt`,
`gc.fencing_token`, `gc.lease_until`, `gc.slot_ref`, `gc.enqueued_at`. The
Binding may alternatively live in a dedicated `internal/binding` durable store
mirroring `internal/nudgequeue`'s flock'd `Pending/InFlight/Dead` shape; the
choice is an implementation decision for dr-26g.3, but the *fields* are fixed
here.

### Atomic bind

The bind must be a single atomic write that either fully commits a Binding or
changes nothing — no reachable partial state (the §3.1/§3.2 partial-route class
must be structurally impossible). Two mechanisms, either acceptable, decided in
dr-26g.3:

- **Store-atomic:** a conditional write in the bead store (Dolt) that sets
  `binding_state: bound` + all Binding fields **iff** `binding_state ∈ {ready}`
  and no live Binding exists for the workload — the same CAS shape as today's
  assignee claim, but performed by the scheduler once, not raced by N workers.
- **Binding-key uniqueness:** a Binding identity `bind/{workload}/{generation}`
  that is unique by construction (mirrors the durable-execution "workflow ID"
  pattern, `durable-execution-walkthrough-pr-state-poller.md`). A duplicate bind
  is a server-side no-op, which closes the duplicate-root class (§3.5, `gc-28jm`)
  at instantiation rather than by incidental detection.

The bind is **at-most-once fail-closed** (work-layer semantics): if the scheduler
cannot prove the slot is reserved and the workload is unbound, it does not bind.
A missed bind is a bead that stays `ready` and is re-evaluated next scheduler
pass — cheap and safe. A double bind is a fan-out race — forbidden.

### Lease + fencing

Every Binding carries a **fencing token** (monotonic per workload, e.g.
`generation` or a store sequence) and a **lease** (`lease_until`). Both cross the
launch boundary into the worker's environment. This gives two guarantees:

- **Stale-executor rejection.** If a lease expires and the workload is rebound at
  a higher fencing token, a resurrected zombie from the old Binding cannot commit
  a terminal outcome or a side effect: the close/commit path checks the token on
  the bead and refuses a write from a stale fence (the mechanism `internal/nudgequeue`
  already uses for "fence mismatch → dead-letter"). This is the durable
  generalization of ADR-0009's single-claim invariant — instead of rejecting a
  second *claimant*, we reject a stale *executor*.
- **Lease as the crash signal.** `lease_until` is a durable timestamp. The
  reconciler's existing `healExpiredTimers` scan (`cmd/gc/session_reconcile.go:440-458`)
  is the natural home for "lease expired and the session is gone → this Binding
  died." No timer service; the deadline is in storage and a scan enforces it,
  exactly as rate-limit backoff already does (§10). Lease renewal is derived from
  the same session-liveness signal `pool_session_name.go` already computes, so no
  new liveness source is introduced.

### Retry + crash recovery

Recovery is **watchdog-layer, at-least-once, idempotent** — the opposite semantic
from bind, and on a separate path. The reconciler scan finds Bindings whose
`lease_until < now` and whose executing session is not live, and:

1. records the death (durable event; never a silent drop — the anti-pattern
   `gc-4zf.4`/§9.5a exposed);
2. if `attempt < max_attempts`, re-enqueues the *workload* to `ready` at
   `attempt+1` with a new `generation` (which raises the fencing token, fencing
   out the zombie);
3. otherwise sets a terminal `abandoned`/`failed` outcome that escalates through
   the existing surfacing path.

Exactly-once *effect* is preserved not by preventing re-execution but by the
fence: a re-run is a fresh generation, and only the current-generation executor
can commit a terminal artifact. A crashed run that already shipped a commit is
detected as `shipped` by artifact movement (ADR-0009 / `work-landing-reaper`
re-derivation from `git merge-base`), so recovery does not double-shipped work —
it reconciles to what the artifact says, which is §10's core lesson.

Crash matrix (each row is a durable-record repair, not a live handler):

| Crash point | Durable state left | Reconciler repair |
|---|---|---|
| Scheduler dies after reserve, before bind | reservation with no Binding | reclaim unreferenced reservation → slot returns |
| Scheduler dies mid-bind | atomic write ⇒ either bound or not | none needed (no partial state) |
| Controller dies during `starting` | Binding `starting`, no live session, lease running | on lease expiry: no artifact ⇒ retry |
| Worker dies during `running`, no commit | Binding `running`, lease expires, session gone | retry at attempt+1, new fence |
| Worker dies during `running`, commit landed | commit on branch, lease expires | artifact scan ⇒ terminal `shipped`, no retry |
| Zombie worker resumes after rebind | old fencing token | terminal/side-effect write refused by fence |

### Capacity / account reservation and caps

Capacity is a **durable reservation**, granted before bind and released on
terminal:

- **Slot inventory** is per-pool and global. `pool_cap` (max concurrent workers
  in a pool) and `global_cap` (fleet-wide) are hard admission constraints checked
  at reservation time; the scheduler cannot bind beyond a cap because it cannot
  get a reservation.
- **Account/provider reservation.** A slot reservation names an account/provider
  (the `claude-N` home / OAuth slot; account selection stays with the install-side
  `csu_pick`/`claude-account` tooling, which the capacity manager calls, not
  reimplements) and is held for the Binding's life. Rate-limited accounts are
  excluded at reservation time (the existing `quarantined_until` quarantine — the
  account manager reads it, so a bind never lands on a throttled account). This
  **inverts** the current order: today the provider health gate
  (`provider_health_gate.go:100`) red-lights a spawn *at reconcile time*
  (`session_reconciler.go:3361-3379`), after the reconciler has already decided to
  spawn; here the account is reserved *before* the bind, so an over-capacity fleet
  cannot bind rather than failing to spawn. It also supersedes the
  `MaxWakesPerTick=5` throttle (`config.go:2691`) — concurrency is bounded by the
  reservation ledger (pool/global caps), not by a per-tick spawn rate limiter.
- **Reservation is durable** (mirrors the nudgequeue lease): a scheduler crash
  between reserve and bind leaves a reservation the reconciler reclaims; it never
  leaks a slot or double-books an account.
- **Warm floor / scale-to-zero.** `min_active` (warm floor, may be 0) and
  `max_active` (cap) are capacity policy. Scale-to-zero (`Min=0`) and slot
  affinity already exist as config concepts (`gc-09bs3`); this ADR moves their
  enforcement from the reconciler's session-count heuristic to the capacity
  manager's reservation ledger.

### Event-driven launch and optional prewarming

Following the pattern the dispatcher already proves correct (`cmd/gc/city_runtime.go:722-751`:
"Patrol scans every reconciler state authoritatively, so any pending
event-driven fires are redundant — drop them."; `internal/dispatch/control.go:427-499`):

- **The scan is the truth; events are a latency optimization.** The scheduler
  runs authoritatively on the reconciler tick (a bead reaching `ready` will be
  bound on the next pass regardless of any event). Bead-state-change events
  (`bead.closed` unblocking a dependent, a new `ready` bead) *may* trigger an
  early scheduler pass to cut latency, but a dropped event only delays, never
  loses — because the next scan re-derives the same decision. This is the
  explicit design constraint from §10 and §7.2: never make correctness depend on
  event delivery, which the event bus does not guarantee (§6.1/§6.2).
- **Optional prewarming.** The capacity manager may keep `min_active` workers
  pre-started (warm) to absorb bind latency; a warm worker is an *unbound* slot
  the controller holds, not a session that self-selects work. When the scheduler
  binds, it hands the fenced bead to a warm slot if one exists (skipping cold
  start) or cold-starts one. Prewarming is purely a latency/cost knob; it changes
  no correctness property, and `min_active=0` (no warm floor) must be a
  first-class, tested configuration (the scale-to-zero regression, `gc-09bs3`).

### Terminal detection by artifact movement

Terminal state is set by **artifact movement, not bead status or nudge**
(dr-26g.5). "Elapsed time" is not completion — a session alive and idle is
indistinguishable from one that shipped (reliability-surface §5.1,
`session_reconcile.go:787-798`). The controller derives terminal outcome from the
ADR-0009 typed work record: `shipped` iff a commit exists on the stamped
`gc.work_branch` (re-derived à la `work-landing-reaper`, §9.3);
`no-op`/`blocked`/`abandoned` from a typed reason. The Binding does not reach
`terminal` on session exit alone — it reaches terminal when the artifact scan
confirms an outcome, which also closes the "closed bead, code never merged" class
at the scheduler layer (§9.3).

## Alternatives Considered

### Alternative 1: Harden the pull model (keep claim-CAS, add a lease field)

- **Pros**: smallest diff; reuses `cmd/gc/cmd_hook_claim.go` and the reconciler;
  no new control-plane roles.
- **Cons**: leaves the worker as scheduler+claimant+executor. The N-workers-race
  shape survives (the lease only bounds orphan duration, it does not stop the
  race); routing partial-writes (§3.1/§3.2) survive because sling stays multi-step
  and non-atomic; nudge fragility survives; capacity is still discovered
  post-spawn. It patches symptoms, not the collapsed-roles root.
- **Why not**: the epic's premise is to move the claim order *to code* and stop
  agent-side claiming/nudges entirely. A lease on the pull path does not deliver
  "worker receives exactly one bead"; it just makes the race recoverable. Rejected
  as a half-measure that spends the migration budget without removing the class.

### Alternative 2: Adopt an external durable-execution engine (Temporal) as the scheduler substrate

- **Pros**: off-the-shelf workflow IDs (idempotent bind), leases (activity
  timeouts), retries, an independent failure domain; a Temporal-maintenance track
  already exists in this workspace (`temporal-maintenance-promotion-plan.md`).
- **Cons**: the reliability track's exhaustive finding is that the city's durable
  wakeups reduce to *durable timestamp + scan*, and on the one workload measured,
  Temporal reduced to "cron plus a lockfile" while its fail-closed adapter
  *destroyed liveness* on crash (a poisoned `pending` key refused forever,
  nothing escalated — §10, `gc-4zf.4`). Work-layer fail-closed semantics are
  exactly wrong for the recovery/watchdog layer. An engine also puts the scheduler
  in a separate process before we know the boundary is right.
- **Why not**: the bind belongs to the work layer (at-most-once), but recovery
  belongs to the watchdog layer (at-least-once, idempotent) — one engine cannot
  serve both, and Temporal's proven property (failure-domain independence) is
  *not exclusive* (a systemd timer provides it more cheaply). This ADR keeps the
  scheduler as durable records + the existing reconciler scan, which already
  provides the atomic-write and durable-timestamp primitives. An engine remains a
  *possible* later home for the controller's lease-heartbeat only if the scan
  cadence proves insufficient — priced against §10, not against "engine vs
  nothing." Deferred, not adopted.

### Alternative 3: LLM-as-scheduler (a mayor/PL reasons about what to bind)

- **Pros**: maximally flexible; handles semantic nuance without structured fields.
- **Cons**: non-deterministic, unauditable, slow, and expensive on the hot path;
  the survey found every product doing this treats it as a *planner UX* with no
  evidence of ordering gains.
- **Why not**: violates ZFC in the wrong direction. Dispatch ordering is
  deterministic-math territory; binding is state/lifecycle + policy enforcement.
  Semantic classification (what *should* be human-gated) stays model-owned as a
  structured label produced upstream, but the bind decision itself must be
  mechanical. Rejected.

### Alternative 4: Keep the reconciler as the binder but bind eagerly (no readiness queue)

- **Pros**: fewer moving parts; the reconciler already scans authoritatively.
- **Cons**: conflates admission (is this eligible?) with scheduling (which one,
  onto which slot?). Without a materialized ready queue there is no `enqueued_at`
  aging clock, no place to observe queue depth, and no clean seam for the future
  ordering objective (dr-fol) to plug into.
- **Why not**: the readiness queue is cheap (it is a query + a durable
  `enqueued_at`) and it is the observability and extension seam. Folding it away
  saves little and forecloses the ordering work. Rejected, but note the queue is
  *logical* — it may be a materialized view over the bead store, not a second
  store.

## Consequences

### Positive

- **Fan-out races become structurally impossible.** One workload → one Binding →
  one fenced executor. The N-claimants shape (ADR-0009's `gc-typpc`) cannot
  occur, and the duplicate-root class (§3.5, `gc-28jm`) is closed at bind by key
  uniqueness.
- **Workers get simpler and safer.** A worker receives its bead + fence in the
  environment and executes. It never runs `bd ready`, never races, never polls,
  never waits for a nudge. The whole nudge-fragility surface (§3.3, gascity#3924)
  leaves the critical path; nudges, if kept at all, become a pure latency
  optimization over the authoritative scan.
- **Capacity stops being discovered post-spawn.** Rate-limited accounts are
  excluded at reservation, not after a session boots onto a hot account; caps are
  enforced by the inability to reserve, not by a session-count heuristic.
- **Crash recovery is first-class, not implicit.** A durable lease + fence makes
  "this work died" a queryable fact with a defined repair, replacing the implicit
  dead-assignee reopen. Every crash-point has a named durable state and a
  reconciler repair (crash matrix), and nothing drops silently.
- **The ordering objective (dr-fol) gets a clean seam.** The scheduler reads
  structured fields today with a minimal band+FCFS policy; the lexicographic
  solver plugs into the same bind decision later without touching admission,
  capacity, or execution.

### Negative

- **New durable records and a codegen pass.** Binding fields go through the
  `beadmeta` typed-metadata ritual (ADR-0003), and a `internal/binding` store (or
  bead-metadata equivalent) must be built, migrated, and scanned. Additive, but
  real surface.
- **Migration is staged and dual-running.** The new plane must run shadow-parallel
  to the live pull pool before cutover (dr-26g.7), which means both models exist
  at once for a period, with a comparison harness — operational overhead and a
  window where two dispatch paths could disagree.
- **The reconciler tick does more.** Admission scan + lease-expiry scan + slot
  reclamation are added to a loop whose cost and cadence already matter
  (reliability-surface §4, the ~90s `gc order check` at scale). The scans must be
  indexed and bounded, or the scheduler becomes the substrate that fails.

### Migration compatibility

The design is **additive and backward-compatible** with current beads, formulas,
and `claim_sort`:

- **Beads.** Existing open beads are unaffected until they are admitted; a bead
  with no Binding is simply an un-admitted candidate. `gc.routed_to` remains
  meaningful (it becomes an admission input: which pool), and `route_recovery.go`
  stays as a compatibility shim during dual-run. Typed metadata is additive
  (ADR-0003), so no bead needs rewriting.
- **Formulas.** Formulas remain the execution recipe unchanged. Today a formula
  is attached at sling time and the worker runs it; under the new plane the
  execution controller launches the worker with the same formula + the fenced
  bead. `mol-*` close steps already emit ADR-0009 typed outcomes — those *are*
  the artifact-movement signal the controller reads, so no formula rewrite is
  required for terminal detection. The `--on <formula>` attach path is preserved.
- **`claim_sort`.** The current oldest-first claim ordering (`claim_sort`, the
  `dr-fk4` "priority discarded" concern) is **subsumed** by the scheduler's
  band+FCFS policy: the scheduler binds in priority-band then `enqueued_at` order,
  which is a strict superset of oldest-first and fixes the "priority discarded"
  bug (§3.7) rather than inheriting it. During dual-run, `claim_sort` still
  governs the legacy pull path; at cutover it is retired in favor of the scheduler
  policy. No config that references `claim_sort` breaks — it is read by the legacy
  path until that path is removed.
- **Cutover is reversible.** Shadow → arm-for-one-pool → fleet, each gated; the
  legacy pull path stays live and un-removed until the shadow comparison
  (dr-26g.7) and chaos suite (dr-26g.8) are clean, and rollback is "stop binding,
  re-enable pull."

### Observability

Every state transition emits a durable event and every Binding field is
queryable (the §10 discipline — pending state in the store, then scan):

- **Queue depth and aging** per pool/priority band (`ready` count, oldest
  `enqueued_at`) — the metric the current model cannot produce because there is no
  materialized queue.
- **Bind decisions** — which workload, which slot/account, which fencing token,
  why (which policy tiebreak). Enables the dr-26g.7 shadow comparison ("would the
  scheduler have bound what the pull pool claimed?").
- **Lease/retry telemetry** — leases expired, retries by attempt, dead/abandoned
  count, time-in-state histograms. Directly feeds the completion-telemetry bead
  (dr-0q3).
- **Reservation ledger** — slots reserved/consumed/reclaimed, per-account
  utilization, quarantine exclusions.
- **Outcome by artifact** — terminal outcomes typed (ADR-0009), so "shipped vs
  no-op vs abandoned" is aggregatable, closing the §5.1 "productive = elapsed
  time" blindness.

### Safety / evaluation plan

Cutover is gated by evidence, not assertion — the model the reliability track
demands ("test the covers, not the code"):

1. **Shadow-parallel decision comparison (dr-26g.7).** Run the scheduler in
   shadow: it computes bindings and records them, but launches **no real
   workers**; the live pull pool keeps executing. Compare, per tick, what the
   scheduler *would* bind against what the pool actually claimed. Cutover requires
   agreement (or explained divergence) across N cycles.
2. **Chaos / fault suite (dr-26g.8), one injection per acceptance criterion.**
   Each of the state-machine transitions gets a fault: kill the scheduler between
   reserve and bind (assert slot reclaimed, no leak); kill the controller in
   `starting` (assert retry, no double-launch); kill a worker mid-`running` with
   and without a landed commit (assert retry vs `shipped`, never double-shipped);
   resurrect a zombie after rebind (assert its terminal write is fenced out);
   drop a launch event (assert the scan still binds). Metric per the epic:
   violation → durable detection → verified restoration. This is the direct
   analogue of the `gc-4zf.4` chaos test that caught the fail-closed liveness
   destruction — the suite must prove the recovery layer stays *loud*.
3. **Scale-to-zero regression** (`min_active=0`): assert a pool drains to zero
   warm workers and still binds on the next candidate with no leaked reservation
   (`gc-09bs3`).
4. **Migration equivalence**: a corpus of recent real dispatches replayed through
   admission+scheduler must produce bindings consistent with what actually
   happened (no bead the pull path would have run is left unadmitted; no bead the
   pull path skipped is spuriously bound).

No pool is armed on the real scheduler until 1–3 are green in shadow.

## Process boundary (logical vs deployed) — explicit per acceptance criterion

The four roles above are a **logical** decomposition: admission, scheduling,
capacity, execution are separated **by module and by durable record**, so their
contracts are independently testable and independently replaceable. This ADR
does **not** decide that any of them runs as a separate process.

- **Initial deployment: in-process.** The recommended first implementation runs
  all four roles inside the existing reconciler/controller process
  (`cmd/gc` + `internal/dispatch` + `internal/session`), because the reconciler
  already provides the two primitives the design rests on — an authoritative
  level-triggered scan and store-atomic conditional writes — and because §10's
  evidence is that failure-domain independence, the only property that would argue
  for a separate process, is achievable more cheaply than a new service and is not
  yet demonstrated to be needed for the *scheduler* (as opposed to the
  scan-of-scans watchdog).
- **What the module boundary buys regardless of process:** the atomic-bind
  contract, the Binding state machine, the reservation ledger, and the
  fenced-launch interface are the same whether they run in one process or four. If
  a role later needs its own failure domain (most plausibly the lease-heartbeat /
  recovery watchdog, per §10's "one non-order scan-of-scans on an independent
  failure domain"), it can be lifted out behind its existing module interface — a
  `systemd --user` timer or, only if the scan cadence proves insufficient, a
  durable-execution engine — without redesigning the plane.
- **The decision this ADR fixes** is the *logical* boundary and the durable
  contracts across it. Where each role runs is an operational choice deferred to
  implementation (dr-26g.2–.7) and revisited only against measured need.

## Implementation surface (sequenced, maps to existing epic children — no new beads)

The epic already carries the implementation beads; this ADR is their design
spec. Sequence follows the existing dependency DAG:

```
dr-26g.1 (this ADR) ─unblocks─▶ all children

  dr-26g.2  capacity reservation      ─▶ dr-26g.3, dr-26g.7
  dr-26g.3  atomic binding            ─▶ dr-26g.4, dr-26g.5
  dr-26g.4  lease + fencing           ─▶ dr-26g.6
  dr-26g.5  artifact-based completion ─▶ dr-26g.7        (∥ with .4→.6)
  dr-26g.6  retry + crash recovery    ─▶ dr-26g.7
  dr-26g.7  shadow rollout            ─▶ dr-26g.8
  dr-26g.8  verification (chaos suite)
```

Critical path: **.1 → .2 → .3 → .4 → .6 → .7 → .8**, with **.5 parallel** to the
.4→.6 leg (both depend on .3, both gate .7). Each child is `city-infra`,
`needs-source-impl`, owned by city-infra-pl. The concrete source anchors the
children will touch (from the current-model map):

| Concern | Current-model anchor | Role in the new plane |
|---|---|---|
| Reconcile loop / desired-vs-actual | `cmd/gc/controller.go:1138` → `cmd/gc/city_runtime.go:363,725-760`; `cmd/gc/compute_awake_set.go:118` | host of the authoritative scheduler + lease-expiry scan |
| Authoritative-scan-over-events | `internal/dispatch/control.go:427-499`; `cmd/gc/city_runtime.go:722-751` | pattern to reuse: events are latency, scan is truth |
| Lease/fence prior art | `internal/nudgequeue/state.go:31-49` (`Attempts/LeaseUntil/DeadAt`, `Pending/InFlight/Dead`) | generalize into the Binding lease/fence store |
| Lease-expiry / durable-timer scan | `cmd/gc/session_reconcile.go:440-458` (`healExpiredTimers`) | home for "lease expired → retry/reclaim" |
| Worker-liveness signal | `cmd/gc/pool_session_name.go:114-213,533-573` | lease-renewal heartbeat source (no new signal) |
| The claim-CAS the bind supersedes | `cmd/gc/cmd_hook_claim.go:198`; `internal/beads/bdstore.go:1349`; ready order `internal/beads/doltlite_read_store.go:330` | replaced by one scheduler-side atomic bind |
| The multi-step sling the atomic bind replaces | `internal/sling/sling_core.go:56,135,281-282` | routing becomes an admission input, bind is atomic |
| Nudge path leaving the critical path | `cmd/gc/cmd_nudge.go`; `nudge_dispatcher.go:171-176`; tmux `NudgeNow` `adapter.go:519` | demoted to optional latency hint |
| Capacity gate → reservation | `cmd/gc/provider_health_gate.go:100`; `session_reconciler.go:3361-3379`; `pool.go:153-194`; wake budget `config.go:2691` | replaced by reserve-before-bind + caps |
| Spawn / identity injection | `internal/runtime/tmux/adapter.go:88,1103`; env at `cmd/gc/template_resolve.go:276-296` | inject fenced bead id + token here |
| Warm floor / scale-to-zero | `min_active_sessions` `config.go:786,3053`; `compute_awake_set.go:334-363` | moves to the capacity manager's ledger |
| Typed Binding fields | `internal/beadmeta` (ADR-0003) | `gc.binding_*`, `gc.lease_until`, `gc.fencing_token`, `gc.slot_ref` |

## Relationship to other ADRs / tracks

- **ADR-0009 (Work Record)** is a prerequisite substrate, not a conflict: its
  typed outcome (`gc.work_branch`, `gc.outcome`, `gc.commit`) *is* the
  artifact-movement signal terminal detection reads. Its single-claim invariant is
  generalized by the fence (reject stale executor, not just second claimant).
- **ADR-0003 (Typed Bead Metadata)** carries the Binding fields.
- **ADR-0004 (Agent Activation Model, retired)** tried to solve "pool sat idle
  while routed work waited" with subscriptions; this ADR solves the same pain
  mechanically (the scheduler binds; the worker never subscribes), which is why
  ADR-0004 can stay retired.
- **dr-fol (ordering objective)** plugs into the scheduler's bind decision; this
  ADR fixes the boundary and ships a minimal band+FCFS policy, deliberately
  leaving the lexicographic solver out of scope.
- **Temporal-maintenance track** (`temporal-maintenance-promotion-plan.md`)
  remains a *separate, optional* provider for the maintenance cycle; this ADR does
  not adopt it as the scheduler substrate (Alternative 2) and does not depend on
  it.
- **dr-e83 (backfill classification)** produces the `needs-human`/`machine-ok`
  labels admission reads; semantic classification stays model-owned there.
