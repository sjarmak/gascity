# Single-Agent Baseline Honesty Gate + Multi-vs-Single A/B — Spec & Instrumentation Plan

**Status:** DRAFT for Stephanie review (routed via gc-pl, tracking gc-9poky)
**Date:** 2026-06-13
**Author:** mayor
**Design records:** `dr-apxhk4` (gate semantics) · `dr-ji4q3v` (A/B method + pass-bar)
**Beads:** authors the missing spec for `gc-viqje` (P0 gate) and `gc-vqfhf` (P1 A/B)
**Source program:** `docs/gascity_improvement_program_2026-05-29.md` §1.2 / §2 / §3 (Area 2, Area 3) / §5
**Feasibility input:** `~/.gc/research/gc-vqfhf-retro-ab-feasibility.md`

---

## 0. Why this doc exists

The program named two coupled moves:

- **gc-viqje (P0) — the honesty gate:** any new or changed formula ships
  `enabled=false` until it is shown to beat its single-agent baseline. The
  premortem's one durable survivor; cheapest defense against unverified
  fan-out cost.
- **gc-vqfhf (P1) — the A/B:** the empirical record the gate reads. Multi-agent
  molecule vs single-agent, on resolution-rate and cost, by bead type,
  stratified by model.

Both beads cited program sections (`dr-apxhk4` §1.2/§3/§5, `dr-ji4q3v` §2/§5)
that were never written as standalone design records — the one-line bead WAS
the entire spec. This doc writes them.

**Stephanie's decision (2026-06-13, sequence d):** build the A/B measurement
first, author this spec, attach it, *then* turn the gate on. The gate stays
`BLOCKED` behind the A/B.

**The hard finding (feasibility study):** the A/B is **not computable
retrospectively**. All four data axes it needs are absent or degenerate in the
current store (no cost field anywhere, no model field, no separable
single-agent population, ~96% of closes are mechanical lifecycle bookkeeping).
Producing a "molecule X% vs single-agent Y%" number from today's data would be
**fabrication dressed as measurement** — and it would feed the very gate it is
meant to back. That is an anti-slop / ZFC violation. So "build the A/B" means
**build forward instrumentation**, then measure. There is no shortcut.

---

## Part A — Gate semantics (`dr-apxhk4`, program §1.2 / §3)

### A.1 The rule

> A new or changed formula ships with `enabled=false`. It may flip to
> `enabled=true` only when a passing A/B record exists for that formula
> version, showing it beats the single-agent baseline on the agreed metric
> (Part B).

"Changed" = any formula whose version increments (ties to the P1 "pack
version-increment + golden-replay CI" item). A formula that changes behaviour
without a version bump is a separate bug the version-increment CI catches; the
gate assumes versioning is honest.

### A.2 Why (evidence, not taste)

MAD — Chun et al. 2025, *"Is Multi-Agent Debate the Silver Bullet?"*
(`2025arXiv250312029C`, corpus-verified) — found structured multi-agent debate
yields **minimal-to-inconsistent gains over single-agent baselines** on
software-engineering tasks. Default-on fan-out therefore spends N× the tokens
for a benefit that is unproven per-formula. The gate forces the proof to exist
before the spend is normalized.

### A.3 ZFC discipline (this is the whole point)

The gate itself performs **zero semantic judgment**. It is a mechanical lookup:

```
enabled := exists(passing A/B record for this formula@version)
```

It does not score quality, classify difficulty, or keyword-scan anything. All
judgment lives in the A/B's resolution-verdict (Part B), which is produced by
the triadic/eval verifier — not by the gate, and not by a regex over NOTES.
(The program explicitly flags `IsFailureClose` / `FailureCloseKeywords`
keyword-scanning as regex-for-meaning — forbidden here.)

### A.4 Scope, exemptions, strictness — **RESOLVED (Stephanie, 2026-06-13)**

- **Enforcement is forward-only.** Only **new or changed** formulas get the
  `enabled=false` gate (hard block until a passing record exists).
- **The grandfathered set is measured, not gated.** All existing formulas
  (≈48) ARE instrumented and have their A/B data collected — but there is
  **no block and no warn** on them. Data-gathering only. This avoids a
  self-inflicted outage on the live dispatch formulas while still building
  the empirical record that later justifies (or retires) each one.
- **Exempt from the gate logic itself:** single-agent formulas (no fan-out to
  justify → N/A), and formulas where a single-agent control is structurally
  infeasible (enumerate explicitly, don't wave through).

### A.5 Implementation prerequisite — the per-formula lever does not exist yet

Verified against `gascity-main`: **there is no per-formula `enabled` field
today.** The `Formula` struct (`internal/formula/types.go:64-132`) has
`Formula`/`Type`/`Contract`/`Catalog`/`Phase`/`Pour` — no `Enabled`. The only
toggle is the **city-wide** `[daemon] formula_v2` flag, read via the global
atomic `formulaV2Enabled` (`internal/formula/compile.go:599-607`,
`IsFormulaV2Enabled()`). So "ship `enabled=false`" is not yet expressible.

gc-viqje therefore has a build prerequisite ahead of the gate logic itself:

1. Add an `enabled` field to the formula TOML + `Formula` struct.
2. Read it at dispatch (where `isGraphWorkflow()` / catalog load decides
   whether a formula runs) and refuse to dispatch a disabled formula.
3. Default new/changed formulas to `false`.

This is small and ZFC-clean (a boolean field + one read site), but it must be
scoped as part of gc-viqje, not assumed present.

---

## Part B — A/B method + pass-bar (`dr-ji4q3v`, program §2 / §3-Area-2 / §5)

### B.1 What it measures

For each **bead type**, compare two arms:

- **multi-agent arm:** beads resolved inside a molecule (fan-out).
- **single-agent arm:** comparable beads dispatched to one agent, no molecule
  decomposition.

…on two outcomes:

1. **Resolution rate** = genuinely-resolved / attempted (verdict-based, B.3).
2. **Cost** = tokens (or $ / model-time) per attempted bead.

**Stratified by model** — the bead asks for it explicitly, and without it the
comparison confounds bead-type and model with arm.

### B.2 The four required axes (all currently absent — see Part C)

| Axis | Needed for | Current state |
|---|---|---|
| arm label (multi vs single) | separable populations | absent — single-agent pop ⊆ molecule pop (100% overlap) |
| resolution verdict | the "rate" numerator | absent — ~96% closes are mechanical lifecycle |
| cost (tokens/$) | the cost outcome | absent — zero cost field on any of 5216 beads |
| model | stratification | absent — no model field on any bead |

### B.3 Resolution verdict — the load-bearing definition

The verdict is `resolved | failed | abandoned`, set by the eval/verifier, and
stored **distinct from** the auto-closer's `close_reason`. It must NOT be
derived by keyword-scanning NOTES or close-reason strings (ZFC; program §3).

The **drain-without-commit structural proxy** (closed, non-failure, empty
NOTES, no commit by the resolving session) was proposed as the cheap stand-in.
gc-0o2ub reproduced its true rate from Dolt:

- **genuine code-bead drain ≈ 0.4%** (1/243), upper bound **4.1%** (10/243).
- the oft-cited **"21%" is lore** — 21.5% (89/413) naive, inflated by
  scaffolding beads mistyped as `task`.

**Consequence for the pass-bar:** do **not** set a "<5% drain-without-commit"
bar (the program's CP1/CP2 draft bar derived from 21% is dead). At a genuine
rate of ~0.4%, the drain proxy is far too rare to discriminate one formula from
another — it is a **floor sanity-check, not the primary metric**. The primary
metric is the verdict-based resolution rate above.

### B.4 The pass-bar — relative, not absolute — **RESOLVED (mayor's concrete pick, 2026-06-13)**

Program §3 settled the *philosophy*: **relative-regression over absolute-SLA**
(matches the verified scix zpm4 / ADR-010 anti-target lesson — absolute bars
Goodhart). Stephanie delegated the concrete numbers to mayor. They are:

A formula's multi-agent arm **beats baseline** for a given
`(bead-type × model)` stratum when **both** hold:

1. **Resolution (not significantly worse):** the 95% CI lower bound of
   `(rate_multi − rate_single)` is `≥ 0`. (Two-proportion test or bootstrap.)
   The arm does not need to *win* on resolution — it must not *lose*.
2. **Cost (bounded by the quality it buys):** with token-count cost (C.1),
   `cost_multi / cost_single ≤ C`, where the allowed ratio `C` scales with
   the resolution gain:
   - `C = 1.5` at parity (no resolution gain) — the default cost-neutral band.
   - `+1.5` of headroom per `+10pp` absolute resolution-rate gain,
   - **capped at `C = 3.0`.**

So a formula that is not significantly better on resolution **and** costs
`>1.5×` is a **FAIL** — that is exactly the MAD failure mode (pay N× for no
gain) the gate exists to stop.

**Concrete knobs:**

- **Min sample:** `≥ 30` resolved attempts **per arm, per
  `(bead-type × model)` stratum** before a verdict is allowed. Below that the
  verdict is `insufficient-data` (a new/changed formula stays gated; the
  grandfathered set just keeps gathering).
- **Significance:** 95% (two-sided) on the resolution-rate difference.
- **Cost exchange:** the linear `1.5 → 3.0` ramp above.

These are a tunable starting point, not graven — they live in `dr-ji4q3v` and
move if the first real strata show they're mis-calibrated.

---

## Part C — Instrumentation plan (forward capture)

No retrospective path exists; capture going forward, then measure after a few
weeks of accrual. Anchors below are **verified against `gascity-main`
(origin/main)**.

**The shape we need already exists, unpopulated.** The `worker.operation` event
payload (`internal/worker/operation_events.go:40-71`) already declares exactly
the fields the A/B wants: `SessionID`, `SessionName`, `Provider`, **`Model`**,
`Operation`, `Result`, `DurationMs`, `LatencyMs`, **`CostUSDEstimate`**. Three
of them (`Model`, `CostUSDEstimate`, and the bead linkage) are never filled in.
So instrumentation is **"populate the empty fields + attribute the operation to
the bead it resolved"**, not "build a new telemetry stream." And bead-side
write is fully wired: `BdStore.SetMetadata()` / `SetMetadataBatch()`
(`internal/beads/bdstore.go:1347-1382`, via `bd update --set-metadata`).

### Current-state map (verified)

| Axis | Carrier today | Status | Gap to close |
|---|---|---|---|
| attribution (session→bead) | no `closed_by_session` on `Bead` (`internal/beads/beads.go:40-75`); `molecule_id` is metadata only | **ABSENT** | join the resolving session to the bead at close |
| state-transition record | `events.Event{Seq,Type,Ts,Actor,Subject,Payload}` (`internal/events/events.go:211-220`); `BeadClosed` type exists | **ABSENT** | no `(issue_id,from_status,to_status,actor,session,ts)` record |
| cost | OTel `gc.agent.invocation.cost_usd` wired (`internal/telemetry/recorder_invocation.go:26,126`, keyed {agent,model,provider}); event `CostUSDEstimate` field exists | **PARTIAL** | event field unpopulated pending #1255; not attributed to a bead |
| model | `session.Info{Provider,...}` has **no `Model`** (`internal/session/manager.go:73-99`); `operationEventPayload.Model` defined but never set (`operation_events.go:136-173`) | **ABSENT** | add `Model` to `session.Info`, populate at identity step |
| verdict | only `close_reason` metadata, ~96% mechanical | **ABSENT** | explicit verdict, eval-set |
| formula enabled | global `[daemon] formula_v2` only (`internal/formula/compile.go:599-607`) | **ABSENT** | per-formula flag (see A.5) |
| interactions.jsonl | does not exist in tree | **ABSENT** | the Area-4 "interactions.jsonl substrate" is not built |

### C.0 Attribution backbone — state-transition record (program P0, §1.4)

Add a `(issue_id, from_status, to_status, actor, session, ts)` record. The
`events` writer already exists (`internal/events/events.go`, `BeadClosed`
event type) — extend it (or its payload) to carry the status transition + the
resolving session. This is the join the whole A/B rests on: today the
`worker.operation` event has `SessionID` but no bead, and the bead has
`molecule_id` but no session. Closing that join is the single highest-leverage
piece. (Confirmed absent in `bin/`, `orders/`, and the Go event types.)

### C.1 Cost per session→bead

Populate `operationEventPayload.CostUSDEstimate` (already defined,
`operation_events.go:70`) at operation finish, and attribute to the bead via
C.0. The OTel counter `gc.agent.invocation.cost_usd` already aggregates by
{agent, model, provider} — the missing piece is per-bead attribution + the
event-field population gated on upstream #1255 (pricing-registry consumer).

- **Risk:** `CostUSDEstimate` may stay null until #1255 lands.
- **Fallback (recommend approving up front):** capture raw token counts
  (input/output) as the cost proxy. Tokens are available pre-pricing, are
  model-comparable within a stratum, and don't block the A/B on #1255.

### C.2 Model label per bead

Two-step: (1) add a `Model` field to `session.Info`
(`internal/session/manager.go:73-99`) and populate it where the runtime
resolves the live model (the same `populateOperationEventIdentity()` site that
already sets `Provider`, `operation_events.go:136-173`); (2) stamp it onto the
bead at close via `SetMetadata(id, "resolved_by_model", …)`. The live model
matters (post rebalance/failover), not just the static `agent.toml` pin.

### C.3 Resolution verdict

Write `resolution_verdict ∈ {resolved, failed, abandoned}` via
`SetMetadata()`, produced by the eval/verifier — **separate** from
`close_reason`. Note: `interactions.jsonl` does **not** exist, so the verdict
cannot "ride" an Area-4 substrate that isn't built; it must be written
directly by the verifier (or as a new verdict event). This is the axis that
makes "rate" mean agent success, not auto-closer activity. NOT a keyword scan
of NOTES/close_reason (ZFC).

### C.4 Single-agent control arm

A **designed experiment**, not a query. Dispatch a labelled subset of
comparable beads single-agent (no molecule), tagged via
`SetMetadata(id, "arm", "single-agent-control")`, matched by bead type to the
molecule population. Needs: selection rule (which types, how sampled), volume
(enough per stratum — B.4.2), and a run window. This is the only piece that
consumes live dispatch capacity.

---

## Part D — Sequencing

```
gc-0o2ub  ✓ (drain number settled: ~0.4% genuine; 21% is lore)
   │
   ├─► C.0 state-transition view  (P0, shared)
   ├─► C.1 cost capture (+ token fallback for #1255)
   ├─► C.2 model stamp
   ├─► C.3 verdict capture (rides triadic scorer)
   │
   └─► C.4 labelled single-agent control dispatch  ── run ~few weeks ──┐
                                                                        ▼
                                              gc-vqfhf: compute the A/B on real data
                                                                        │
                                                                        ▼
                                  gc-viqje: (A.5) add per-formula `enabled` flag,
                                            then flip the gate on, backed by record
```

The gate-lever build (A.5) is independent of the A/B and can land in parallel
with C.0–C.4 — it just must not *enforce* until a record exists to enforce
against.

gc-vqfhf cannot lead this chain — it is the **consumer** of instrumentation
that does not yet exist. It stays `blocked` pending C.0–C.4. gc-viqje stays
`blocked` behind gc-vqfhf.

---

## Part E — Decisions — **ALL RESOLVED (Stephanie, 2026-06-13, via gc-pl)**

1. **Pass-bar knobs (B.4):** "go with concrete numbers, mayor picks" →
   baked into B.4 (not-worse-on-resolution at 95% CI + token-cost ramp
   1.5→3.0, min 30/stratum).
2. **Cost proxy (C.1):** **APPROVED** — token counts as the cost measure; do
   not block the A/B on upstream #1255; swap to `cost_usd` if/when it lands.
3. **Gate scope (A.4):** **forward-only enforcement**; the ≈48 existing
   formulas are instrumented + measured but **not gated** (data-only, no block,
   no warn); only new/changed formulas get `enabled=false`.
4. **Control-arm (C.4):** **minimal stratified design APPROVED**; runs on our
   oauth accounts; capacity spend pre-approved (no further sign-off). Mayor
   sets selection rule / volume-per-stratum / run window.

C.0–C.4 + A.5 are now filed as build beads (see gc-vqfhf/gc-viqje notes).

---

## §C.0 RESOLVED — join architecture (2026-06-13)

The §C.0 text above left the *join direction* underdetermined; a worker
escalation (gc-mbwey) flagged that "a session does not know its work-bead."
Resolved against `gascity-main` (architect grounding):

**The session does not need to know its bead — the bead already knows its
session.** `stampRunSessionIdentity` (`cmd/gc/build_desired_state.go:3228-3292`)
stamps `gc.session_name` + `gc.work_dir` onto the step each reconcile;
`stampRunRootFromStep` (3299-3327) propagates them onto the molecule **root**
(#2843), specifically because the transient `Assignee` is cleared on close.
`graphroute.go:174-209` additionally stamps `gc.session_id` for direct-session
bindings.

**Mechanism = Hybrid B+** (reject A, reject C):
- **A** (populate `operationEventPayload.BeadID`) — the worker `SessionHandle`
  has no "current work bead" source (tmux lifecycle ops only,
  `operation_events.go:16-31`); parked since #1252. Rejected.
- **C** (post-hoc projection via `molecule_id`) — redundant; the root→session
  correlation is already materialized by `stampRunRootFromStep`. Rejected.
- **B+** — emit a **new** typed event `molecule.resolved` from the
  molecule-autoclose Go close site, reading the session from the root's stamped
  metadata. Do **not** overload `BeadClosed`.

**Change set:** (1) `internal/events/events.go` — add `MoleculeResolved =
"molecule.resolved"` const + add to `KnownEventTypes`. (2)
`internal/api/event_payloads.go` — `MoleculeResolvedPayload{IssueID, FromStatus,
ToStatus, Actor, SessionName, SessionID, WorkDir, CloseReason string; Ts
time.Time}` + `RegisterPayload`. (3) `cmd/gc/molecule_autoclose.go
announceClosedMolecule` (226-239) — capture `fromStatus := mol.Status` before
`closeMoleculeWithReason`; resolve session from `mol.Metadata` via
`beadmeta.{SessionName,SessionID,WorkDir}MetadataKey`; emit additive
`MoleculeResolved`; keep the existing `BeadClosed` emit. (4) test in same commit;
un-stamped root degrades to empty session, not a crash.

**bd-CLI subprocess path is avoided by design** — event fires only via the Go
`gc molecule autoclose` entrypoint (`hooks.go:137-169`). Manual non-molecule
closes produce `bead.closed` but no `molecule.resolved` — exactly the filter the
A/B wants.

**Downstream:** C.3 verdict = direct join; C.2 model = join session→model; C.1
cost = join `MoleculeResolved.session_id` → that session's `worker.operation`
cost events. **C.1 caveat** (not a C.0 defect): root carries the last stamping
session (last-writer-wins); full-lifecycle multi-session cost needs C.1 to walk
`gc.phase_history`/retry metadata — a C.1 scoping decision.
