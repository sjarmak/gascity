# PRD — Formula Observability + Testing/Eval Layer (single-vs-fan-out A/B)

**Status:** RISK-ANNOTATED PRD (diverge → converge → premortem complete) — for mayor → Stephanie
**Date:** 2026-06-19
**Builds on:** `docs/design/honesty-gate-and-ab-spec.md` (the gate semantics + A/B pass-bar are RESOLVED there; this PRD does NOT re-derive them)
**Pipeline:** `/diverge` (3 agents) → `/converge` (3 reviewers, §5b) → `/premortem` (3 narratives, §6b). Convergence verified all file:line claims against `gascity-main` HEAD `81579e6`.

**How to read:** §5b (convergence resolutions) and §6b (premortem risk register) are the load-bearing additions and override conflicting earlier prose. §5b.0 carries two verified CODE corrections (C1, C2). §5b.5 + §6b carry the hard blocks (HB-A…HB-E) that gate the build. **§8 (F4 — model-tier routing expansion)** folds a SECOND eval-consumer axis into the layer: per-formula-step tier downgrades (opus→sonnet→haiku) validated on the SAME A/B non-inferiority harness, with three new premortem risks (R4–R6) and a precedence hard block (HB-G).

---

## 0. Problem & framing (Stephanie's objection, encoded)

The motivating policy (bead `dr-apxhk4`) was: *ship new/changed formulas `enabled=false` until they beat a single-agent baseline on a golden set.* Stephanie's correct objection: **we have no golden sets and no single-vs-fan-out A/B harness, so the gate is un-decidable today, and defaulting single-agent formulas to OFF is backwards** — a sequential single-polecat pipeline has no fan-out cost to justify.

Therefore the ordering is inverted relative to the original policy:

1. **The observability + eval layer comes FIRST.** (this PRD)
2. **The enable-gate is strictly DOWNSTREAM** and scoped ONLY to *genuinely multi-agent fan-out* formulas — never the single-agent default.

**Ground truth on the formula population** (verified): almost all formulas are sequential single-polecat pipelines, NOT fan-out — `mol-focus-review` (1 worker / 6 steps), `mol-pr-from-issue` (1w/7), `mol-scoped-work` (1w/15). The ONLY genuine fan-out is `mol-epic-review` and the ad-hoc Workflow tool. The A/B has a real second arm only for those.

**Evidence the gate exists at all:** MAD/MAST literature (Chun et al. 2025, `2503.12029`) — structured multi-agent debate yields minimal-to-inconsistent gains over a strong single agent on SWE tasks. Default-on fan-out spends N× tokens for an unproven per-formula benefit. The expected A/B result is *null*; the harness must be able to report that honestly.

---

## 1. Goals / Non-goals

**Goals**
- G1. Capture per-formula-run telemetry (steps, tokens, wall-clock, tool-calls, outcome, cost) so single-agent and fan-out runs are comparable, landing in the existing `events.jsonl` + Dolt store (extend, don't reinvent).
- G2. Define/curate fixed, versioned golden task sets per formula class so runs are comparable.
- G3. Run the SAME task through single-agent vs fan-out arms and compare on resolution + cost with statistically honest (paired-where-possible) aggregation.
- G4. Let a downstream baseline gate consume these evals, scoped ONLY to multi-agent formulas, never the single-agent default.
- G5. ZFC compliance: orchestration is pure mechanism; all semantic judgment (the resolution verdict) is model-delegated. Audited explicitly.

**Non-goals**
- NG1. Re-deriving the gate semantics or pass-bar (RESOLVED in the design note).
- NG2. Manufacturing hypothetical fan-out variants of sequential pipelines just to feed the harness (YAGNI / unrequested-feature).
- NG3. Retrospective A/B on existing data (proven non-computable — design note §0).
- NG4. Flipping the gate ON in this phase. The gate *lever* may land inert; enforcement waits for a real record.

---

## 2. Architecture overview

```
DISPATCH ── mint formula_run_id ──► run (single bead OR molecule w/ N sub-sessions)
                │                                  │
        stamp arm/model/run_id              worker.operation events
        on bead metadata                          │
                │                          run-close (molecule.resolved
                │                          OR single-bead bead.closed w/ arm tag)
                ▼                                  ▼
        ┌───────────────────────────────────────────────┐
        │ formula.run summary event (events.jsonl SSOT)  │  ← mechanical
        │  run_id, formula, hash, arm, model, bead_type, │     aggregation
        │  sessions[], tokens, cost, wall_clock, tools   │     over the run's
        └───────────────────────────────────────────────┘     sessions
                │                                  ▲
                ▼                                  │ verdict back-filled
        Dolt projection: formula_runs        eval/verifier (MODEL) writes
        + formula_ab_verdict (per stratum)   gc.resolution_verdict ∈
                │                            {resolved,failed,abandoned}
                ▼
        gc-order scanner: B.4 arithmetic ──► per-stratum verdict
                │
                ▼
        DOWNSTREAM GATE (mechanical lookup, multi-agent formulas only)
```

---

## 3. F1 — Observability: per-formula-run telemetry & store

### 3.1 The run-identity correlation key (`formula_run_id`)

The design note's join (§C.0) only spans the *molecule* case. A single-agent run has no molecule root to carry the stamp. **Fix: mint `formula_run_id` at dispatch and make it the universal key.**

- Add `RunIDMetadataKey = "gc.formula_run_id"` to `internal/beadmeta/keys.go`.
- Mint once at dispatch, at the `stampRunSessionIdentity` site (`cmd/gc/build_desired_state.go:3228-3292`) that already stamps session identity.
- **Molecule arm:** mint on the root; `stampRunRootFromStep` (`build_desired_state.go:3299-3327`) already propagates root metadata to members → every sub-session's bead inherits the same `formula_run_id`. Rides the existing spine.
- **Single-agent arm:** mint on the dispatched bead directly (the bead *is* the run).

Result: both arms produce records keyed `(formula_name, formula_hash, formula_run_id, arm, model)`; the molecule-vs-single asymmetry vanishes at the identity layer.

### 3.2 Per-run field set + capturability

| Field | Source | Today | ZFC |
|---|---|---|---|
| `formula_run_id` | minted at dispatch (§3.1) | ABSENT — mint | mechanical |
| `formula_name` | `gc.formula_name` (`beadmeta/keys.go:98`) | YES | mechanical |
| `formula_hash` (version proxy) | `gc.formula_hash` (`keys.go:97`); no real `version` field exists | PARTIAL | mechanical |
| `arm` ∈ {multi, single} | `SetMetadata(id,"arm",…)` (design C.4) | ABSENT — designed label | mechanical (tag) |
| `model` (live) | `sessionlog msg.Model` (`internal/sessionlog/tail.go:296`); `session.Info` has no Model (`session/manager.go:73-99`) | PARTIAL — from transcript | mechanical |
| `tool_call_count` | transcript `tool_use` blocks (not in `worker.operation`) | ABSENT | mechanical |
| `input/output/cache tokens` | `sessionlog assistantMessage.Usage` (`tail.go:257-261`) — but only LAST msg today | PARTIAL — needs cumulative sum | mechanical |
| `wall_clock_ms` | run-close ts − dispatch ts | PARTIAL — derivable | mechanical |
| `cost_usd` | `pricing.Estimate(Usage)` (`internal/pricing/pricing.go:79-82`) over summed tokens | PARTIAL — math landed, needs Usage | mechanical (derived) |
| `resolution_verdict` | eval/verifier, `gc.resolution_verdict` | ABSENT — **MODEL-PRODUCED** | **model-delegated** |

**Dropped from the minimal set:** `steps_executed`. `worker.operation` events are tmux lifecycle ops (`internal/worker/operation_events.go:17-31`), not reasoning steps — counting them measures tmux churn. Tokens + wall-clock + verdict are the comparable axes; tool-calls is the effort proxy. (Open for converge.)

### 3.3 New discoveries that change the design note's "just populate the fields" premise

- The OTel token counter `RecordInvocationTokens` (`internal/telemetry/recorder_invocation.go:91`) has **zero non-test callers** — nothing feeds it.
- The only live token reader, `sessionlog.extractFromLines` (`tail.go:264-330`), keeps only the **last** assistant `usage` block (a context-window gauge) — NOT cumulative spend. The `Usage` struct also omits `output_tokens`.
- `pricing.Estimate` (`pricing.go:79-82`) HAS landed — the note's "#1255 pricing not landed" caveat is partly stale; the cost math exists, it just has no `Usage` fed to it.
- `gc.phase_history` (`keys.go:126`) has **NO Go reader** — "walk phase_history for multi-session cost" (design note §C.0 caveat) is not implementable as stated.

**Consequence — the single highest-leverage new mechanism:** add `sessionlog.SumUsage(path)` that walks a transcript FORWARD and accumulates all token types + a `tool_use` count in one pass. This unblocks tokens, cost, AND tool-calls at once, depends on nothing upstream. Add `OutputTokens` to the `Usage` struct.

### 3.4 Where telemetry lands

**A new `formula.run` summary event in `events.jsonl` (SSOT), projected to a Dolt `formula_runs` table by a `gc order` scanner.** NOT a back-populated `worker.operation` (rejected as Plan A in design note §C.0:343 — worker `SessionHandle` has no work-bead source).

- Add `FormulaRun = "formula.run"` to `internal/events/events.go` `KnownEventTypes`; register `FormulaRunPayload` in `internal/api/event_payloads.go` (run_id, formula, hash, arm, model, bead_type, root_issue_id, **session_ids[]**, tool_calls, token fields, cost_usd, wall_clock_ms, verdict, ts).
- Emit at run-close: molecule arm at the autoclose site (`cmd/gc/molecule_autoclose.go:226-239`, where `MoleculeResolved` already fires); single-agent arm at `bead.closed` when the bead carries `arm=single-agent-control`.
- `verdict` emitted `""`, **back-filled async** by the verifier via `SetMetadata` and joined on `run_id` by the projection scanner (verdict must not block emission).

### 3.5 Multi-session cost-walk (fan-out)

Don't walk `phase_history` (no reader). At run-close, enumerate **all** sessions in the run by filtering events for the shared `formula_run_id`, `SumUsage` each transcript, and **sum across all N sessions** → the payload token/cost fields. `session_ids[]` makes it auditable. Single-agent (N=1) and fan-out (N>1) use the **identical** aggregation path — the comparability the layer demands.

---

## 4. F2 — Golden sets + the paired A/B harness

### 4.1 Primary harness decision: PAIRED golden replay (not the note's observational arm)

The design note's Part C.4 is an *observational* control arm on live dispatch (tag real beads single-agent, accrue over weeks, unpaired two-proportion test). **Recommendation: make the fixed, versioned golden-set PAIRED replay the PRIMARY A/B; demote the live arm to a complementary realism check.**

- Paired (same task, both arms) **eliminates the bead-type confound by construction** and supports a matched-pair test (McNemar / paired bootstrap) that reaches the note's bar with far fewer attempts.
- The live arm keeps one thing the golden set can't give: external validity / staleness detection.
- **The pass-bar is UNCHANGED.** The note's B.4 — resolution-rate (verdict-based) + token cost, stratified by (bead-type × model), 95% CI lower bound of `(rate_multi − rate_single) ≥ 0`, cost ratio `C` ramp 1.5→3.0 capped at 3.0, ≥30 resolved attempts/arm/stratum — is honored verbatim. Only *how the two arms' samples are generated* (paired replay) and *which test consumes them* (paired) change. The ≥30 floor is reinterpreted as ≥30 *paired* attempts.

### 4.2 Golden set structure (lift codeprobe + migration-evals)

- **"Formula class" = (bead-type × pour-shape).** Pour-shape, not formula name — the A/B compares topologies.
- **First real A/B targets = genuine fan-out only:** `mol-epic-review`, ad-hoc Workflow, fan-out decomposers. For sequential pipelines there is NO second arm; their honest deliverable is the grandfathered *measurement* (design note A.4), not an A/B win.
- Store in-repo, versioned with the formula:
  ```
  formulas/_golden/<formula-class>/
    suite.toml         # set_version, formula_version_floor, task list
    task-NNN/
      task.toml        # id, repo_snapshot_ref, time_limit, [oracle]
      oracle/          # the task's OWN acceptance check — NEVER mounted into agent worktree
  ```
- **15–25 tasks/class** sized to clear ≥30 paired attempts/stratum with repeats. Selection = mechanical stratified sampling from real closed beads; **final "representativeness" inclusion vote delegated to a model**, then frozen (no hardcoded representativeness heuristic).
- **Anti-leakage / staleness (port migration-evals):** SHA-stamp golden set + oracle spec; a publication/gate-read gate refuses SHA mismatches; 12-month label half-life + re-anchor on oracle-spec / model-family change / formula-version bump (ties to the note's "version-increment + golden-replay CI"). Golden repos are content-addressed snapshots checked out fresh per run, kept out of any `gc prime`/`bd prime` path.

### 4.3 Paired execution

- **"Single-agent variant of a fan-out formula" = a real sibling formula `<name>-solo.formula.toml`** that collapses the fan-out into one worker's sequential step list (the dominant existing shape). NOT a hidden "disable fan-out" dispatch flag (implicit-default-parameter anti-pattern).
- Per (golden task, arm, repeat): **fresh isolated worktree from the pinned snapshot.** Randomize (task × arm × repeat) order (never all-A-then-all-B). Disable cross-arm prompt-cache reuse. **Model held fixed within a pair**; cross-model is a stratum, never compared within a pair. Record `correlation_id` + `harness_provenance` (model, prompt_version, timestamp, seed).
- The runner is **pure mechanism (ZFC):** checkout → dispatch → collect diff/tokens/transcript → teardown. No judgment.

### 4.4 Honest aggregation

- **Paired test:** McNemar exact on the discordance matrix (port codeprobe `analysis/dual.py DualMatrix` + `analysis/stats.py mcnemars_exact_test`) for resolution; paired bootstrap over per-task deltas for the 95% CI lower bound (the exact form the note's bar reads) and for cost.
- **Cost paired per task** (token counts, the note's approved C.1 proxy — don't block on #1255). Report the median paired ratio into the `C` gate; outlier-robust.
- **NULL/NEGATIVE is first-class:** verdicts `beats / not-worse-but-costlier (FAIL) / worse / insufficient-data`. "Not significantly better on resolution AND >1.5× cost" = **FAIL** (the MAD failure mode). No "partial pass" (port `mol-epic-review`'s rule). The harness never retries to manufacture a win.
- **`eval_broken` self-distrust gate (port migration-evals `gold_anchor.py`):** if golden-arm and live-arm diverge beyond CI, set `golden_set_stale=true` and refuse to let the gate read the record until re-anchor.
- **Repeats: 3 per (task, arm)** default (pair on per-task pass-rate over repeats, then bootstrap) — robust to single-run luck without exploding capacity.

### 4.5 Where the model verdict enters (tiered funnel — mechanical-first, model-last)

| Tier | Check | Kind |
|---|---|---|
| 0 | diff parses & applies (`git apply --check`) | mechanical |
| 1 | build/compile exit 0 | mechanical |
| 2 | task `test_cmd` / oracle script exit 0 | mechanical |
| 3 | **resolution verdict** — did the agent solve THIS task's goal | **MODEL-delegated** |

The verdict is the note's C.3 `resolution_verdict`, stored distinct from `close_reason`, **never** a keyword scan of NOTES (forbidden: `IsFailureClose`/`FailureCloseKeywords`). The verifier sees the diff, tier 0–2 results, and the task's acceptance text — **blind to which arm produced the run**. Where a deterministic oracle exists, tier 2 IS the verdict (no model call). **Dual-family judge** (port migration-evals): judge provider ≠ implementer provider — the cross-provider discipline `mol-epic-review` already encodes.

---

## 5. F3 — The downstream gate (multi-agent formulas only)

### 5.1 Structural fan-out classifier (the linchpin of Stephanie's objection)

`IsMultiAgentFanout(f *Formula) bool` — **mechanical/structural, NOT a model call and NOT a name allowlist.** A formula is fan-out iff its compiled graph materializes **≥2 concurrent sub-sessions**:

| Construct | Concurrent? | Fan-out? |
|---|---|---|
| `OnComplete` parallel (`!Sequential`) — `internal/formula/graph.go:31-46` | YES | YES |
| `OnComplete` sequential | NO | NO |
| `Drain` separate (`Context != "shared"`) — `internal/dispatch/drain.go:104-162` | YES | YES |
| `Drain` shared — `drain.go:391-460` | NO | NO |
| `Branch` (`len(Compose.Branch)>0`) — `internal/formula/controlflow.go:421-484` | YES | YES |
| `BondPoint.Parallel` (≥2 attach) | YES | YES |
| `WaitsFor` / `Loop` / `Ralph`/`Retry` | NO | NO |

One pure recursive function in `internal/formula`, mirroring `stepRequiresGraphCompiler` (`types.go:1015-1026`). **Classify on the COMPILED/resolved formula** (post `extends`/`compose`/`aspects`), not raw TOML, or a child games the gate by hiding fan-out in a parent. Result: sequential pipelines auto-exempt by construction; only real fan-out is gated; **no formula name appears anywhere** (Bitter Lesson holds).

### 5.2 Gate decision table (mechanical lookup at dispatch)

| `IsMultiAgentFanout` | lifecycle | A/B record | Decision |
|---|---|---|---|
| false (single-agent) | any | any | **DISPATCH** — never gated |
| true | grandfathered (≈48 pre-cutover) | any | **DISPATCH + MEASURE** — no block/warn |
| true | new/changed | passing | **DISPATCH** |
| true | new/changed | FAIL | **BLOCK** (`enabled=false`) |
| true | new/changed | insufficient-data / absent | **BLOCK** (`enabled=false`) |

- Gate reads at `internal/sling/sling_core.go:621-629` (`doStartGraphWorkflow`, pre-`PromoteWorkflowLaunchBead`).
- **Record store = a Dolt `formula_ab_verdict` table** keyed `(formula, version/hash)` — NOT formula metadata (would make the TOML a mutable data store).
- **"Grandfathered" is mechanical:** `(formula, ContentHash)` (`types.go:127-129`) existing in the catalog before the gate's activation timestamp. No hand-maintained allowlist.
- **Two decoupled levers:** `Enabled *bool` field on `Formula`+TOML (the *mechanism*, `*bool` nil-means-true so existing TOMLs are untouched) ships first/inert; the gate predicate (the *policy* that computes `enabled` for multi-agent new/changed) enforces only once a record exists.

### 5.3 ZFC audit (the whole layer)

The gate is clean (mechanical lookup, structurally identical to the existing `tallyVotes` reducer — pure arithmetic over model-produced votes). Tripwires in the accrual layer:

| # | Tripwire | Correct delegation |
|---|---|---|
| **T1** (load-bearing) | deriving verdict by keyword-scanning close_reason/NOTES | **model-produced** verdict at distinct `gc.resolution_verdict`; scanner only reads/aggregates (the `tallyVotes` shape) |
| T2 | classifier calling a model "is this fan-out?" | mechanical `IsMultiAgentFanout` (structure → code) |
| T3 | hardcoded formula-name allowlist / difficulty scores | derive from structure; names never appear |
| T4 | treating the pass-bar math as the model's job | KEEP mechanical — deterministic arithmetic over model verdicts; ZFC *forbids* delegating this |
| T5 | keyword-inferring bead-type for strata | read the structured `type` field; never infer |
| T6 | `insufficient-data` silently treated as pass | explicit state → stays BLOCKED (new) / keeps gathering (grandfathered) |
| T7 | scanner retry loop swallowing verdict-write failures | fail loud to audit log; never swallow |

### 5.4 Sequencing / integration (don't break the live ds-research city)

- **Track A — instrumentation** (F1): `molecule.resolved` join → model stamp → token/cost capture → verdict write. Purely additive, no enforcement.
- **Track B — gate lever, parallel & inert:** `Enabled *bool` (`*bool`, nil=true) + one read site + tests in the same commit. No gate logic; author-set only; zero live-dispatch risk.
- **Track C — accrual scanner on existing `gc order` machinery:** `orders/eval-accrual-verdict.toml` (`trigger="cooldown"`) + `bin/eval-accrual-verdict` reaper following `bin/epic-review-sweeper` shape, `.gc/eval-accrual-verdict.log` JSONL audit, `--apply`/dry-run. Reads strata, applies B.4 arithmetic, writes `formula_ab_verdict`.
- **Gate flips ON only after:** lever shipped + weeks of accrual + ≥1 real passing stratum proves the pipeline. At cutover, snapshot the catalog's `(formula, ContentHash)` set as grandfathered. **Nothing on the live city changes at flip** — only post-cutover multi-agent formulas can ever block.

### 5.5 Guardrails

- **Author override:** the gate computes the default `enabled`; an author may set `enabled = true` explicitly to force-ship with eyes open (auditable). Prevents the gate from becoming a hard outage. BLOCK must be a clear authored-affordance error, not a silent skip.
- **Goodhart resistance:** classify on the compiled formula (closes the "hide fan-out in a parent" hole); relative-regression bar (not absolute SLA) per ADR-010.
- **Provenance check:** a stratum verdict is valid only if every contributing bead has a verifier-produced `gc.resolution_verdict` (not derived/defaulted). No verifier verdict → `insufficient-data`, never a guess.

---

## 5b. CONVERGENCE RESOLUTIONS (3 reviewers: skeptic / statistician / maintainer)

The PRD was debated by three role-clamped reviewers. The maintainer verified every file:line claim against `gascity-main` HEAD `81579e6` (2026-06-19). Resolutions below are binding; they override conflicting text above.

### 5b.0 Two load-bearing CODE CORRECTIONS (maintainer, verified)

- **C1 — `stampRunRootFromStep` propagates member→ROOT (upward), NOT root→member.** Verified `cmd/gc/build_desired_state.go:3299-3327`: it writes session identity onto the root via `gc.root_bead_id`; there is **no** existing downward root→member metadata fan. **§3.1's "every sub-session inherits the same `formula_run_id` via stampRunRootFromStep" is WRONG.** Fix: mint `formula_run_id` at dispatch on the root AND on each member as they are created (member beads already carry their own `gc.session_name`); enumerate members at run-close by store query on `gc.root_bead_id`, not by assumed inheritance. The single-agent path (bead = run) is unaffected.
- **C2 — Fan-out is injected at COMPILE time; the raw-TOML population census is unsound, and the gate is INERT unless classified post-compile.** Verified: zero of 14 workspace formulas expose `on_complete`/`compose.branch`/separate-drain in raw TOML; `mol-epic-review` raw TOML is 3 sequential steps / single reviewer. Fan-out emerges from `applyGraphControls` (`internal/formula/graph.go:25`) writing `gc.kind=fanout` onto synthesized control steps, and from `mol-decompose`'s drain. **`IsMultiAgentFanout` MUST run on the compiled/resolved formula** (the PRD §5.1 "classify on compiled, not raw TOML" instruction is now load-bearing and confirmed correct). The §0 "only `mol-epic-review` is fan-out" claim must be re-derived against compiled recipes before anyone trusts that a real second arm exists. Risk: if mis-implemented on raw TOML the entire F3 gate is a silent no-op (safe for the live city, but zero value).

### 5b.1 Scope & sequencing — v1 vs v2 (skeptic position adopted, statistician concurs on v2 shape)

**v1 (build now):** F1 instrumentation + the design-note C.4 LIVE observational control arm (already RESOLVED + capacity-approved, design note Part E) + the inert gate lever (F3 Track B). This is the smallest thing that lets Stephanie make the gate decision.

**v2 (build only if the live arm contradicts the MAD null):** the full F2 paired golden-replay laboratory. The MAD literature predicts a null on the ~1-2 genuinely-fan-out formulas; do not build a migration-evals-grade laboratory to measure a near-certain null until the cheap live arm shows a signal worth pinning with paired precision. **Deferred to v2:** the `_golden/` tree, `<name>-solo` sibling formulas, SHA-anchor gate, half-life, McNemar harness, paired bootstrap, `eval_broken` gate, AND the `formula_ab_verdict` Dolt table + accrual scanner (Track C — don't build a projection scanner before any data accrues).

**When F2 IS built, it adopts the statistician's corrected stats (5b.3) — paired golden replay remains the right design; only the v1/v2 ordering is the skeptic's win.**

### 5b.2 Control-arm fairness — RESOLVED: equal TOTAL token budget (unanimous, HARD BLOCK)

The single-agent control arm gets a token ceiling equal to the **measured total token spend of its paired fan-out run** (set per-pair after the fan-out completes), recorded in `harness_provenance`. Per-agent budget rigs the fan-out win (N× resources → "discovers" fan-out resolves more — the exact MAD-skeptic inversion); wall-clock parity silently gives fan-out more total compute. Equal-total-budget is the only honest estimand because the gate's economic question IS denominated in tokens ("is N× spend via fan-out better than N× spend on one longer agent?"). **No A/B run executes until this is in the spec.** The live C.4 arm's single-agent population must likewise be budget-comparable, not throttled.

### 5b.3 Statistical corrections (statistician, binding for any A/B — v1 live arm AND v2 paired)

- **S1 — Replace the `≥0` superiority null with NON-INFERIORITY at a pre-registered margin δ.** Pass resolution iff 95% CI lower bound of `(rate_multi − rate_single) ≥ −δ` (e.g. δ = −2pp or −5pp), δ a `dr-ji4q3v` knob. The note's `≥0` bar is incoherent for a "not-worse" claim and degenerates to block-forever at low power. (This is surfacing one of the note's own "tunable, not graven" numbers, not re-deriving the bar.)
- **S2 — Power on DISCORDANT pairs, not pair count.** McNemar's signal is entirely in discordant pairs (`n01+n10`); under the expected MAD-null (arms agree ~90%), 30 pairs → ~3 discordant → near-zero power. Floor = ≥30 paired tasks **AND** ≥10 discordant pairs (formula-level). Pair on per-task pass-**rate** over **≥5 odd** repeats (not per-run binary outcomes — 3 binary repeats lets agent stochasticity manufacture discordant pairs → spurious significance). Gate on a within-task-variance diagnostic.
- **S3 — Pre-registration + formula-level pooled verdict as the decision unit.** Port migration-evals `pre_reg.py` + `publication_gate.py`: SHA-stamp the enumerated strata + δ/C thresholds; the accrual scanner refuses any verdict whose stratum-set/threshold SHA doesn't match. The **formula** passes iff its pooled estimate clears the bar — NOT iff any single stratum clears it (kills "one lucky stratum flips the gate" multiple-comparisons fishing — currently the PRD's largest unguarded hole). BH-FDR only if a per-stratum test is ever decision-bearing. Mechanical SHA check = ZFC-clean.
- **S4 — Hierarchical partial pooling within a formula** across its sparse `(bead-type × model)` strata (Beta-Binomial / random-effects), with an explicit formula-level discordance floor. Pool WITHIN a formula only — never across formulas (different topologies) or by merging the two stratification axes. This is the honest answer to starvation; **override-by-default is rejected as the standing policy** (it converts "couldn't measure" into "ship N× spend").
- **S5 — Cost verdict = upper 95% bootstrap-CI bound of the summed-token ratio ≤ C** (ratio of summed tokens, not mean of per-task ratios; median is diagnostic only). One fat expensive-tail task must not pass under a threshold the distribution violates.
- **S6 — Staleness gate = Phi-vs-gold-labels (port `gold_anchor.py`, trip on `point<0.7 OR ci_low<0.5`)**, NOT golden-arm-vs-live-arm divergence (that compares two different populations and doubles the noise). Arm divergence demoted to a non-gating realism diagnostic.

### 5b.4 Other tension resolutions

- **Tension #2 (version identity) — RESOLVED: explicit `version` field.** Verified: all 14 workspace formulas already carry `version = N` (mol-focus-review at `version = 2`), but the `Formula` struct has no `Version` field so `toml.Unmarshal` silently discards it. Wire up the field authors already write. `ContentHash` (`types.go:129`) is computed pre-`extends`/`compose` (`parser.go:149`) so a cosmetic edit churns it (resets accrual) AND a child's hash is blind to parent changes (Goodhart hole). Key the gate on `(formula, version)`; keep `ContentHash` as a tamper-evident audit fingerprint stored alongside.
- **Tension #5 (`steps_executed`) — RESOLVED: dropped (confirmed tmux churn). Tool-calls is also demoted** to diagnostic color, never a gate input (tokens subsume effort). Gate inputs = tokens (cost) + verdict (resolution) only.
- **Tension #6 (fan-out threshold) — RESOLVED: gate on PRESENCE of any concurrency, no threshold.** A `≥k` count is a hardcoded semantic knob inviting gaming (sit at k-1); a barely-fan-out formula trivially passes the cost ramp (C≈1.0) anyway, so the cost ramp — not a magic count — decides if fan-out was worth it.
- **`Enabled *bool` nil-means-true — confirmed safe.** Verified both parse paths use plain unmarshal with no `DisallowUnknownFields` (`parser.go:185-209`); existing TOMLs are untouched. This is the documented carve-out to the "prefer non-nullable" rule (nil = "author expressed no opinion → default-on"). Watch: run `make check` on the formula package for struct golden-comparison perturbation.
- **§4.2 representativeness "model inclusion vote" — CUT (skeptic, ZFC theater).** "Representative" has no defined contract; a model vote over set membership can silently bias the golden set toward tasks fan-out wins. Selection is mechanical stratified sampling matched to real bead-type×model frequencies, frozen by SHA. (v2 concern; flagged now.)

### 5b.5 Hard blocks that gate F1 (must resolve before F1 is "buildable")

- **HB-A (was tension #7) — transcript discoverability is a PRECONDITION, not a tension.** Maintainer confirmed the failure is real and plausible: `RecordInvocationTokens` is dead, `phase_history` has no reader, and `session-prune-dormant.toml` / `bead-prune-reaper.toml` cooldown reapers can prune an early-finishing member's transcript before molecule-autoclose → `SumUsage` reads **0 with no error** (silent-truncation → "fan-out looks free" → rigs the cost ratio). **Fix: capture per-session token totals AT SESSION-CLOSE (transcript provably present), stamp onto the member bead; the run-close aggregator SUMS the stamped values, not re-walks transcripts.** This is a real change to §3.3/§3.5 and makes the N=1/N>1 path cleaner. Missing/unmeasured cost → emit explicit `null` + `cost_incomplete=true`; the scanner treats it as `insufficient-data`, NEVER zero.
- **HB-B — verdict back-fill needs a terminal-timeout state.** A run emitted with `verdict=""` that the verifier never writes (crash, provider outage, dual-family judge down) sits in indefinite limbo — letting an advocate suppress losing runs by starving the verifier. Fix: every `formula.run` reaches a terminal verdict within a bounded window or is marked `verdict=verifier-timeout` and surfaced to the audit log; log WHICH judge produced each verdict.
- **HB-C — `MoleculeResolved` does not exist yet** (maintainer: autoclose site `molecule_autoclose.go:226-239` emits `BeadClosed`, not `MoleculeResolved`). Track A is net-new event plumbing per design note §C.0, NOT "populate existing fields." Low live-city risk (additive emit), but scope it honestly.
- **HB-D — Dolt write discipline + name collision.** An existing `formula_runs` API concept exists (`internal/api/handler_formulas.go` `buildFormulaRuns`) — pick a non-colliding table name. Any projection scanner MUST read the port from `.beads/dolt/.dolt/sql-server.info` and go through the live endpoint (per workspace CLAUDE.md: never `bd dolt start|stop|status` or `dolt sql` against `.beads/dolt/` while the server is up).

---

## 6. Residual open tensions (for premortem / Stephanie)

Resolved tensions #1–#7 moved to §5b. What remains genuinely open and needs Stephanie's call:

1. **Non-inferiority margin δ (S1).** Converge mandates replacing `≥0` with `≥−δ` but δ itself (−2pp? −5pp?) is a `dr-ji4q3v` knob requiring Stephanie's risk tolerance for tolerated regression on a fan-out formula.
2. **Is the gate worth building at all if the city has ~0-2 fan-out formulas (C2)?** Once the population is re-derived against *compiled* recipes, if genuine fan-out is vanishingly rare, F3 may be a near-no-op and the honest answer is "instrument + measure, don't gate." This is the load-bearing strategic call the C2 correction forces.
3. **v1→v2 trigger.** What live-arm signal magnitude justifies building the v2 paired laboratory? (Default: only if the live arm contradicts the MAD null beyond its CI.)
4. **Override audit semantics (S4).** Override is now a rare audited exception, not the default. Confirm the affordance: `enabled=true` explicit override on a blocked fan-out formula, stamped to the audit log with stratum counts at override time.

---

## 6b. PREMORTEM — risk register (3 prospective-failure narratives)

Three independent agents each wrote a 6-month "it shipped and failed" narrative. Each failure traces through an EXISTING PRD section and ends in a strengthening, not a new feature. Ordered by severity × likelihood.

### R1 — Fabricated cost measurement flips a bad formula to enabled (HIGH × MED-HIGH)

**Chain:** Under schedule pressure, HB-A's "stamp at session-close, sum stamped values" is downgraded to the cheaper "re-walk transcripts at run-close" (CI uses always-present synthetic transcripts, so the prune race never appears). The `session-prune-dormant` / `bead-prune-reaper` cooldown reapers prune early-finishing members' transcripts → `SumUsage` reads **0 with no error** → fan-out cost under-counted, biased toward the cheapest reviewers → cost ratio computes ~1.3 < 1.5 → `formula_ab_verdict=beats` written on a fabricated number → a later version flips `enabled=true`. This is precisely the "fabrication dressed as measurement" the design note §0 was built to prevent.
**Early warnings:** `len(session_ids[]) < known fan-out width`; ZERO `cost_incomplete=true` rows despite active pruning; solo-arm token totals clustered at the per-agent default ceiling (proves §5b.2 not wired); cost ratios implausibly near 1.0 on a 3-worker formula.
**Mitigation (strengthens §5b.5 HB-A):** HB-A is correctly specified but is a *precondition without an enforcing test*. Add (a) an integration test that runs the accrual path **with prune reapers active** and asserts `cost_incomplete=true`, NOT numeric 0 — test the race, not synthetic transcripts; (b) hard-couple §5b.2: the equal-total-budget ceiling MUST read the stamped total, so an unimplemented HB-A makes §5b.2 *refuse to run the A/B* rather than silently fall back to per-agent budget.

### R2 — Built but gated nothing; decision still unanswerable (HIGH × HIGH)

**Chain:** v1 ships clean (F1 + inert lever + C.4 arm). The compiled-population re-derivation (C2) finally runs and finds **exactly ~1** recurring genuine-fan-out formula (`mol-epic-review`, ~weekly). Under the MAD null (arms agree ~90%), ~30 paired attempts over 3 months → ~3 discordant pairs vs the S2 floor of ≥10 → verdict `insufficient-data` forever (~18+ months to clear at the live arrival rate). No new fan-out formula is authored, so the gate's only live behavior is row 1 ("DISPATCH, never gated") on 100% of traffic. In December Stephanie asks "fan-out on or off?" and the answer is still *we cannot say* — full build cost spent, zero decision enabled.
**Early warnings:** the compiled-population census filed as a *downstream task* rather than a go/no-go on v1; discordant-pair count flatlining near zero in month 1; the napkin arithmetic (≥10 discordant ÷ ~10% discordance ÷ ~1/week ≈ 100+ weeks) never done; v1→v2 trigger (§6 #3) left undefined; `formula.run` query-count for a *decision* = 0.
**Mitigation (NEW — promote to HB-E):** a one-page **gateable-population census + arrival-rate feasibility gate**, run on COMPILED recipes, as a hard precondition v1 must clear *before any gate-lever or C.4 dispatch build bead opens*. If (a) ≤1 gateable formula OR (b) arrival-rate math can't clear S2 within the v1 window → v1's honest deliverable collapses to **"instrument + measure + publish the population finding; do NOT build the gate lever or C.4 dispatch."** §5b.0/§5b.3/§6 name every ingredient but never force the multiplication — this makes it a blocking gate that can cancel ~80% of v1 scope before a line is written.

### R3 — The safety fix degrades the live city (HIGH × MED)

**Chain:** v1 was sold as "touches nothing live," but HB-A's fix writes a per-session token total via `SetMetadata` onto the member bead at session-close — a NEW high-frequency write to the **shared Dolt sql-server** on the session-teardown hot path, for every session. A fan-out burst overlapping a prune-reaper cooldown puts the stamp-write and the reaper mutating the same member beads (C1 last-writer-wins), driving lock contention on the one sql-server CLAUDE.md says never to touch wrong. `gc sling`/claim/close time out → looks like dolt drift → on-call runs `bd dolt status` (forbidden; kills the live server) → meanwhile C.4 dispatch on shared oauth accounts triggers failover load → oomd kills the mayor. The instrumentation meant to be *safe* became the blast-radius carrier.
**Early warnings:** session-close / reconcile p95 creep in `~/.gc/supervisor.log`; Dolt lock-wait / retry counts climbing during reaper cooldown windows; partially-stamped member beads after a prune cycle; control-arm share of oauth spend trending up; reaper audit logs overlapping session-close stamp spikes.
**Mitigation (strengthens §3.4 + §5b.5 HB-A + §5b.2):** **HB-A's per-session totals are emitted as a `session.tokens` EVENT to events.jsonl (the SSOT §3.4 already designates), NEVER stamped onto the bead.** The aggregator joins on `formula_run_id` from the event stream. This keeps F1 purely additive event-emission — zero new contended writes to the shared store, and it eliminates the C1 last-writer-wins race entirely. Separately, extend §5b.2: the C.4 live arm must run on a **capacity-isolated account set**, not just an equal token budget, or it competes with the city it measures. (Note: this resolves a latent contradiction — HB-A as written contradicts §3.4's events.jsonl-SSOT instinct; the event-stream form removes it.)

### R4 — Silent quality regression from an over-aggressive haiku downgrade the golden set didn't cover (HIGH × MED-HIGH)

**Chain:** A mol-scoped-work `implement` step clears its tier A/B at haiku on the §8.3 golden set — but the golden set was mechanically sampled (§5b.4) from *closed* beads, which skew toward the easy tasks haiku already handles; the long-tail hard task that opus quietly absorbed is under-represented. δ is set generously (§6 #1) "because it's just a downgrade." `routing.yaml` gets `mol-scoped-work/implement → haiku`. Three weeks later a class of subtle implementation bugs starts landing — haiku passes the mechanical tiers (diff applies, build green, tests green, §4.5 tier 0–2) but the *task's intent* was missed in ways the blind verifier's tier-3 sample didn't catch because those tasks weren't in the golden set. Because the FCTR keeps already-dispatched routing (`routing.yaml:10-13`), the regression is invisible in-flight and only shows in aggregate close-quality weeks later. This is the §4.4 `eval_broken` self-distrust failure applied to tiers: the harness reported `non-inferior` on a population that wasn't representative of production difficulty.
**Early warnings:** golden-set difficulty distribution not matched to production bead-type×model frequencies (§5b.4 sampling not actually frequency-matched); δ widened without a recorded risk decision; post-downgrade close-reopen / follow-up-bead rate creeping up on the downgraded step; the S6 Phi-vs-gold staleness gate never re-anchored after the downgrade; verdict pooled across strata where the hard stratum had <10 discordant pairs (S2 floor breached but verdict issued anyway).
**Mitigation (strengthens §8.2 + §5b.3 S2/S4/S6 + §4.2):** (a) the tier-downgrade golden set MUST be difficulty-stratified and frequency-matched to production (the §5b.4 mechanical-sampling rule is load-bearing here — flag it as a precondition for *any* tier A/B, not just fan-out); (b) **every shipped downgrade carries a standing post-ship Phi-vs-gold monitor (S6) on live close-quality**, and a rising follow-up/reopen rate on the downgraded step auto-reverts the `routing.yaml` line (cheap — one YAML revert, next compile) and re-opens the A/B; (c) tier downgrades on `keep-opus`/J-class steps (§8.3) require a *narrower* δ than M-class, recorded in pre-registration (S3) — the regression cost is asymmetric, so the bar must be.

### R5 — Tier downgrade silently interacts with CLAUDE_EFFORT, double-cutting capability (MED × MED)

**Chain:** An `implement` step A/Bs opus→sonnet and passes at default effort. Separately, a capacity-pressure change (compass-capacity) lowers `CLAUDE_EFFORT` on the same rig to shed reasoning-token cost — a knob the PRD treats as orthogonal (§8.1) but which is NOT orthogonal in *effect*: the A/B that validated sonnet was run at one effort level, and the live step now runs sonnet at *low* effort. The combined cut (cheaper model × less reasoning) lands below the validated quality, but no A/B ever tested that cell. The verdict store says `non-inferior`; production sees a regression the harness never measured because effort was held fixed in the A/B and varied in production.
**Early warnings:** `CLAUDE_EFFORT` changed on a rig carrying FCTR-downgraded steps without re-running their A/Bs; `tier_ab_verdict` rows that don't record the effort level under which they were measured; mayor=high effort masking the issue on mayor-run steps while worker steps silently run low.
**Mitigation (strengthens §8.6 T11 + §4.3 harness_provenance):** record `effort_level` in `harness_provenance` and key the `tier_ab_verdict` on `(formula, version, step, from_tier, to_tier, effort)`. A verdict is valid ONLY at the effort it was measured at; if production effort differs from the validated effort, the FCTR stamp for that step is treated as `insufficient-data` (KEEP-EXPENSIVE, §4.4) until re-A/B'd. Tier and effort are orthogonal *axes* but not orthogonal *risks* — the harness must pin both.

### R6 — mol-dispatch's criteria-only→opus path collides with an FCTR sonnet/haiku stamp on the same bead (MED × MED)

**Chain:** The coarse **mol-dispatch** router routes `gc.routing=criteria-only → Claude Opus` (§8.1, `mol-dispatch.formula.toml:6,56-71`) — a model-driven prose decision the dispatcher makes by reading bead metadata. A work bead flows through mol-dispatch (stamped "send to Opus, it's open-ended") AND is later compiled through a formula whose FCTR rule stamps that step `sonnet` (or `haiku`). Two routers now disagree on one bead: the type-router says opus, the difficulty-router says sonnet. Whichever stamp the worker reads last wins silently — and if the FCTR sonnet stamp wins, a deliberately-opus criteria-only task runs sonnet with no audit trail of the override. The two routing layers were never given a precedence rule.
**Early warnings:** beads carrying BOTH a mol-dispatch agent assignment AND an FCTR `routing.model_tier` stamp; criteria-only beads observed running on sonnet/haiku; no documented precedence between the type-router and the difficulty-router; the §8.1 "must not be conflated" caveat present in prose but unenforced in the compile path.
**Mitigation (NEW — promote to HB-G, a precedence rule):** **mol-dispatch's TYPE routing is the model selector of record for criteria-only/prescriptive work; the FCTR's difficulty TIER applies WITHIN the model family the type-router selected, and may only move DOWNWARD from it, never override it upward to a different provider.** Concretely: `criteria-only`'s opus is a *ceiling* the FCTR can lower (opus→sonnet→haiku, each behind an A/B per §8.2) but cannot replace with Codex; `prescriptive`'s Codex routing is provider-level and out of FCTR scope entirely (FCTR tiers are Claude-only, `profiles.go:147-152`). The compile-time stamp must record which router set the final model and why, so the precedence is auditable, not last-writer-wins. Until this rule is wired, a bead carrying both signals is `insufficient-data` and keeps the type-router's choice (the conservative, more-capable default).

### Cross-cutting premortem finding

All six chains share a root: **a "precondition", "orthogonality assumption", or "safety fix" stated in prose but not enforced by a test, a blocking gate, or a precedence rule becomes the failure carrier.** The PRD's specification quality is high; its weakness is enforcement. The mitigations are cheap and additive — a race-exercising integration test (R1), a one-page feasibility gate that can cancel scope (R2/HB-E), moving one write from the bead store to the event stream (R3), a frequency-matched golden set + standing post-ship monitor (R4), keying verdicts on effort (R5), and a two-router precedence rule (R6/HB-G). The F4 additions (R4–R6) reinforce the PRD's central inversion: **measurement precedes the lever** — for tiers exactly as for fan-out, the `routing.yaml` edit and the consumer flip are *outputs* of the eval harness, never inputs to it.

---

## 7. Test tiers (workspace convention)

- **Unit:** `IsMultiAgentFanout` per construct + composition; `sessionlog.SumUsage` cumulative sum; gate decision table; B.4 paired arithmetic (McNemar, bootstrap CI, cost ramp).
- **Integration:** `formula.run` emit at both close sites → Dolt projection → verdict back-fill join on `run_id`; accrual scanner reads strata → writes `formula_ab_verdict`; gate reads record at dispatch. **MANDATORY (premortem R1):** an integration test that runs the accrual path **with the prune reapers active** and asserts a pruned-member run emits `cost_incomplete=true`, never numeric 0 — exercise the race, not synthetic always-present transcripts.
- **E2E:** a golden-set paired replay (one fan-out formula + its `-solo` sibling) through fresh worktrees → tiered grading (mechanical + model verdict) → per-stratum verdict → gate decision, including a deliberately-null result that must report `not-worse-but-costlier (FAIL)` not a manufactured win.
- **F4 (model-tier) additions:**
  - **Unit:** the cost-ledger ranking (highest-opus-token steps); the tier-pair non-inferiority arithmetic reusing the §4.4 bar; the §8.6 ZFC assertion that `routing.yaml` is parsed as data only (no difficulty-classifier code path exists — grep the compile path for any keyword/name→tier mapping and assert none).
  - **Integration:** a (formula, step, tier-pair) A/B end-to-end → `tier_ab_verdict` row keyed on `(formula, version, step, from_tier, to_tier, effort)` (R5) → `routing.yaml` populated ONLY on a passing verdict → FCTR stamp reflects it at next compile. **MANDATORY (premortem R6/HB-G):** a bead carrying BOTH a mol-dispatch `criteria-only` assignment AND an FCTR tier stamp must resolve to the type-router's model ceiling, never a silent last-writer-wins downgrade.
  - **E2E:** a tier downgrade on a workhorse step (mol-scoped-work `implement`) through the full harness, including a deliberately-regressing haiku arm that must report `worse (KEEP-EXPENSIVE)` and a haiku arm that retries-and-overspends that must report `not-cheaper-enough (HOLD)` (§8.2) — neither may be silently shipped. Plus a standing post-ship Phi-vs-gold monitor (R4) that auto-reverts the YAML line on a rising reopen rate.

---

## 8. F4 — Model-tier routing expansion (haiku / sonnet / opus per formula-step)

This is an INTEGRATED workstream, not a sibling project. The single-vs-fan-out A/B is *one* axis the eval layer measures; **model-tier-within-a-step is a second axis on the identical statistical machinery** (§4.4, §5b.3). The verdict store, the non-inferiority bar, the golden replay, the ZFC verifier — all are reused verbatim with one substitution: the two arms are not `{single, multi}` but `{tier-A, tier-B}` of the *same* step on the *same* task. Where §4–5 ask "is N× spend via fan-out worth it?", F4 asks "is the cheaper tier non-inferior on this step?". Same estimand shape, same harness.

### 8.1 The thesis: the machinery exists and is half-wired — the gap is COVERAGE, not infrastructure

Verified ground truth (against the live workspace + `gascity-main`):

- A model-tier router already exists: the **Formula-Compile-Time Router (FCTR)**, driven by `/home/ds/gas-city/.gc/routing.yaml`. It stamps `routing.model_tier` (plus `routing.grounded_review`, `routing.human_gate`) onto every formula STEP bead at compile time, looked up most-specific-first: `(formula, step, var_match)` → `(formula, step)` → `(formula, "*")` → `defaults` (`routing.yaml:1-13`). Decisions ride the existing compile spine — the same stamp-on-step-bead mechanism the rest of this PRD instruments.
- The tier→model map is a structured table in `internal/worker/builtin/profiles.go:147-152`: `haiku=claude-haiku-4-5-20251001`, `sonnet=claude-sonnet-4-6`, `opus=claude-opus-4-8` (also `fable-5`, `opus-4-7`). There is no name-to-difficulty heuristic anywhere — tier is an explicit authored token. Orthogonal `--effort` knob (`profiles.go:129-141`, `CLAUDE_EFFORT`; mayor=high) controls reasoning effort independently of tier.
- **The FCTR is in PHASE 1 — measure-only.** Per `orders/route-decide-report.toml:5-9`, formulas do **not yet consume** the stamped tier; the stamp is computed and aggregated weekly by `bin/route-decide-report` (FCTR's per-formula-step telemetry, structurally the F1 of this axis) but the worker still launches on the account-default model. The documented Phase-2 ship gate is mechanical: *stamped Haiku share ≥ 25%* (`route-decide-report.toml:9`).

**Therefore the expansion is populating `routing.yaml` across the ~14 compiled formulas and then justifying the Phase-1→Phase-2 flip — NOT building a router.** Current coverage is thin and unmeasured:

- `defaults: model_tier: sonnet` (`routing.yaml:20-21`).
- Haiku is assigned to ~7 cheap mechanical steps, across **exactly two** formulas — `mol-focus-review` (`load-context`, `run-tests`, `finalize`) and `mol-pr-iterate` (`intake`, `parse-feedback`@codecov, `verify-clearance`, `report`) (`routing.yaml:29-67`).
- `opus` is assigned **NOWHERE** in `routing.yaml` — yet it is the *de facto* tier for the bulk of work.

The de-facto-opus bulk is the unmeasured liability. Every agent OUTSIDE an FCTR-tiered step — the mayor, all project-leads, the workhorse formulas `mol-scoped-work` (8 steps) and `mol-pr-from-issue` (7 steps), `mol-do-work`, and all ad-hoc workers — carries **no `--model` override** and therefore runs the **account-default (Opus)**. Confirmed empirically: sampled live sessions set no `ANTHROPIC_MODEL`/`--model`. This is the opposite of the FCTR's own default (`sonnet`): the router *defaults* sonnet, but un-routed steps run *opus* because no override is emitted. That gap — sonnet-intended vs opus-actual — is exactly what F1 telemetry + the Phase-1 stamp already lets us quantify before we change anything.

A second, COARSER router exists and must not be conflated: **mol-dispatch** routes by work TYPE, not difficulty — `gc.routing=criteria-only → Claude Opus`, `gc.routing=prescriptive → Codex` (`formulas/mol-dispatch.formula.toml:6,56-71`). It is a model-driven prose dispatcher (the dispatcher reads metadata and picks an agent), wholly distinct from the FCTR's mechanical per-step stamp. Their interaction is a premortem risk (§6b R6) and a precedence rule (§8.6 HB-G).

### 8.2 The hard dependency: no tier downgrade ships without an A/B non-inferiority verdict from THIS PRD's harness

**F4 is a first-class CONSUMER of the F1/F2 eval layer, not an independent optimizer.** The rule is absolute and mirrors the §5 gate's posture toward fan-out:

> You may not lower a step's tier (opus→sonnet, sonnet→haiku) until the harness proves quality holds at the lower tier on the golden set.

Each proposed downgrade is an A/B: **same step, same golden task, tier-A vs tier-B**, model held fixed within a pair (§4.3 — here the "model" being held is the *tier under test*; the pairing is task-level, the contrast is tier-level), graded by the blind ZFC verifier (§4.5 tier 0–3 funnel), aggregated under the SAME non-inferiority bar:

- **Resolution non-inferiority (S1):** 95% CI lower bound of `(rate_cheap − rate_expensive) ≥ −δ`, δ the pre-registered margin (§6 #1). A downgrade passes iff the cheaper tier is *not worse by more than δ*. The §4.4 verdict vocabulary applies unchanged: `non-inferior-and-cheaper (DOWNGRADE) / not-worse-but-not-cheaper-enough (HOLD) / worse (KEEP-EXPENSIVE) / insufficient-data (KEEP-EXPENSIVE)`.
- **Cost is the whole point, so it inverts (S5):** for fan-out the cost gate *caps* spend (ratio ≤ C); for a tier downgrade cost is the *expected win* — the verdict reports the realized token/cost ratio of cheap-vs-expensive, and a downgrade that does NOT actually save tokens (e.g. haiku retries 3× and burns more than one opus pass) is a `HOLD`, not a win. This is the haiku-false-economy guard.
- **Power on discordant pairs (S2), pre-registration + pooled formula-level verdict (S3), partial pooling across sparse strata (S4), Phi-vs-gold staleness (S6)** all carry over verbatim. The decision unit is the **(formula, step, tier-pair)** — pooled across the step's `(bead-type × …)` strata, never decided by one lucky stratum.

The verdict store is the SAME Dolt table family as §5b.0/HB-D (pick a non-colliding name, e.g. `tier_ab_verdict`, keyed `(formula, version, step, from_tier, to_tier)`). The accrual scanner is the SAME `gc order` reaper shape (§5.4 Track C / `bin/eval-accrual-verdict`); F4 adds rows, not a new pipeline. **This is the integration: tier routing does not get its own statistics — it consumes the harness this PRD already specifies.**

### 8.3 Per-formula-step coverage map (the expansion backlog)

Classification is provisional and **advisory only** — every cell marked "→cheaper" is a *hypothesis to be A/B'd* (§8.2), never an authored downgrade. The map's job is to **order the backlog** (§8.4), not to make tier calls. Steps are classified: **M** = mechanical (haiku-candidate), **S** = standard-implementation (sonnet-candidate), **J** = high-judgment (keep-opus). "Now" = current `routing.yaml` state; ⚠ = currently **unrouted → runs de-facto opus**.

| Formula (ver) | step | class | now | candidate | notes |
|---|---|---|---|---|---|
| **mol-focus-review** (2) | load-context | M | haiku | haiku | already routed |
| | focus | J | ⚠ sonnet-default→opus-actual | **A/B sonnet** | core implement step |
| | run-tests | M | haiku | haiku | already routed |
| | simplify | S | ⚠ | A/B sonnet | mechanical-ish cleanup |
| | review | J | ⚠ (+grounded) | keep-opus | hard gate; grounded_review on |
| | finalize | M | haiku | haiku | already routed |
| **mol-pr-iterate** (1) | intake | M | haiku | haiku | routed |
| | parse-feedback | M | haiku@codecov | extend haiku to all sources | var_match today; widen after A/B |
| | propose-patch | S | ⚠ | A/B sonnet | 200-LOC-capped plan |
| | apply | S/J | ⚠ (+grounded) | A/B sonnet | grounded_review already on |
| | verify-clearance | M | haiku | haiku | routed |
| | report | M | haiku+gate | haiku | routed |
| **mol-scoped-work** (2) | load-context | M | ⚠ | A/B haiku | inspect assignment |
| | workspace-setup | M | ⚠ | A/B haiku | git plumbing |
| | preflight-tests | M | ⚠ | A/B haiku | run checks |
| | implement | J | ⚠ | A/B sonnet | the load-bearing step |
| | self-review | J | ⚠ | A/B sonnet | verification |
| | submit | M | ⚠ | A/B haiku | finalize item |
| | cleanup-worktree | M | ⚠ | A/B haiku | teardown |
| **mol-pr-from-issue** (1) | pr-start | J | ⚠ | A/B sonnet | plan+scaffold+implement |
| | gate-after-pr-start | M | ⚠ | A/B haiku | mechanical flag gate |
| | ship | J | ⚠ | keep-opus | review-iterate gate |
| | gate-after-ship | M | ⚠ | A/B haiku | mechanical flag gate |
| | gate-auto-push-eligibility | M | ⚠ | A/B haiku | 9 hard checks |
| | open-pr | S | ⚠ | A/B sonnet | push/open or halt |
| | drain | M | ⚠ | haiku | controller signal |
| **mol-pr-ci-diagnose** (1) | intake | M | ⚠ | A/B haiku | read-only parse |
| | checks | M | ⚠ | A/B haiku | fetch+count |
| | classify | M | ⚠ | A/B haiku | signature match (mechanical) |
| | report | S | ⚠ | A/B sonnet | synthesize verdict |
| **mol-pr-revert** (1) | intake-pr / verify-revertable | M | ⚠ | A/B haiku | parse + SHA checks |
| | revert-branch | M | ⚠ | A/B haiku | git revert plumbing |
| | open-revert-pr | S | ⚠ | A/B sonnet | gated write |
| | comment-on-original | M | ⚠ | A/B haiku | best-effort comment |
| | report | S | ⚠ | A/B sonnet | summary |
| **mol-pr-merge-only** (1) | intake / rebase-check / finalize | M | ⚠ | A/B haiku | mostly git plumbing |
| **mol-epic-review** (1) | load-epic-context | M | ⚠ | A/B haiku | load+aggregate diff |
| | grade | J | ⚠ | keep-opus | per-criterion verdict |
| | act | S | ⚠ | A/B sonnet | close or follow-up |
| **mol-review-quorum** | review-lane-{one,two} | J | ⚠ (provider-configurable) | keep-opus / mixed | **genuine fan-out** (§8.5) |
| | synthesize-review-quorum | J | ⚠ | A/B sonnet | merge two lanes |
| **mol-decompose** | decompose | J | ⚠ | keep-opus | criteria + review chain |
| **mol-brainstorm / mol-research / mol-idle-rig-research** | (skill steps) | J | ⚠ | keep-opus | open-ended reasoning |
| **mol-patrol / mol-dispatch / mol-do-work / mol-ad-hoc-from-mayor / mol-skill-work** | drain & controller steps | M | ⚠ | haiku (drain) / A/B (work) | drain is pure signal |
| **mol-polecat-base/-commit/-report** (1–2) | load-context / workspace-setup / preflight-tests | M | ⚠ | A/B haiku | shared base steps |
| | implement / self-review | J | ⚠ | A/B sonnet | inherited by variants |

**Aggregate reading:** ~7 of ~70+ steps are tiered today; the rest are de-facto opus. The largest single block of unmeasured opus spend is the workhorse pair **mol-scoped-work + mol-pr-from-issue** (15 high-volume steps), which is where instrumentation should point first (§8.4). "keep-opus" cells are the high-judgment core (review gates, grading, decomposition, open-ended research) — the steps where a regression is least tolerable and least likely to pay off; they are *last* in the backlog, behind a wider δ if attempted at all.

### 8.4 Sequencing: instrument-first → highest-cost-opus-first → A/B → populate (cheapest-correct-first)

The ordering is the §5.4 instrument-first discipline applied to tiers, and it composes with v1/v2 (§5b.1):

1. **Instrument (already partly live).** F1 per-step telemetry (§3) + the existing FCTR Phase-1 stamp + `bin/route-decide-report` already give per-formula-step cost/outcome and a "what tier *would* have been routed" view. **No new infra — turn the existing measure-only telemetry into a ranked cost ledger.** First deliverable: rank steps by *total opus tokens spent* over a window (the de-facto-opus bulk, §8.1), surfacing the highest-cost targets. This is pure aggregation over data already flowing.
2. **Target highest-cost opus steps first.** Work the backlog (§8.3) in descending realized-cost order, NOT in difficulty order — a rarely-run opus step is not worth an A/B even if obviously mechanical. mol-scoped-work / mol-pr-from-issue lead by volume.
3. **A/B each downgrade behind the harness (§8.2).** One (formula, step, tier-pair) at a time; cheapest *demonstrable* win first (mechanical→haiku candidates clear non-inferiority most easily and save the most per step). Never batch multiple downgrades into one verdict — a pooled "we lowered 5 steps" hides which one regressed.
4. **Populate `routing.yaml` only on a passing verdict.** The edit is the *output* of an A/B, never its premise. Most-specific-first means a per-step rule overrides the formula default with no blast radius beyond that step; an already-dispatched workflow keeps its routing (`routing.yaml:10-13`), so a bad rule cannot retro-poison in-flight work — it only affects the next compile, and is reversible by reverting the YAML line.
5. **Flip Phase-1→Phase-2 (consumer on) only when the stamped distribution is evidence-backed.** The documented `≥25% haiku share` gate (`route-decide-report.toml:9`) is necessary but NOT sufficient: it must be ≥25% haiku share *whose downgrades each carry a passing `tier_ab_verdict`* — not 25% achieved by un-evidenced YAML edits. Add this provenance check to the Phase-2 gate so the consumer flip can't be gamed by bulk-editing the map.

### 8.5 Tier routing meets fan-out (mol-review-quorum is the one real intersection)

`mol-review-quorum` is the lone *genuine fan-out* in the population (parallel `review-lane-one` / `review-lane-two`, provider/model-configurable, synthesized at `synthesize-review-quorum`). It is therefore the **one formula where both axes apply at once** and they must not be confounded:

- A fan-out A/B (§4–5) varies *topology* (quorum vs `-solo`) with **tier held fixed** across both arms.
- A tier A/B (§8.2) varies *tier* with **topology held fixed**.
- Running both at once is a 2×2 the harness is not powered for; per §4.3/§5b.2 the held variable must be pinned per pair. **Rule: never co-vary tier and topology in one pair.** When `mol-review-quorum`'s lanes run different tiers (a deliberately mixed quorum), that is a *fixed configuration under test as one arm*, not a within-pair tier contrast.
- The §5b.2 equal-total-budget rule (HARD BLOCK) gains a tier dimension: a fan-out arm running haiku lanes vs a solo arm running opus is NOT a fair topology test — it confounds "fan-out helps" with "we spent less per lane." Equal-total-token-budget across arms already neutralizes this, but the harness must record `per_lane_tier` in `harness_provenance` so the confound is auditable.

### 8.6 ZFC: tier is an evidence-driven token, never a keyword-difficulty guess

The §5.3 ZFC discipline extends to F4 with one new load-bearing tripwire. The anti-pattern to forbid is the obvious one: **a "classify the step's difficulty from its name/prompt keywords → pick a tier" heuristic.** That is precisely the hardcoded semantic-scoring ZFC forbids (`patterns.md §ZFC`, T3 in §5.3) — it would break on every edge case a model handles naturally, and it would bake one author's difficulty intuition into the router.

The correct flow is identical to §5's gate: tier decisions are **mechanical lookups of a model-validated A/B verdict**, not inline judgments.

| # | Tripwire (F4) | Correct delegation |
|---|---|---|
| **T8** (load-bearing) | a classifier that reads a step's prompt/name and *scores its difficulty* to pick a tier | tier is set ONLY by a passing `tier_ab_verdict`; `routing.yaml` is a verdict-output, not an authored guess. The §8.3 map's M/S/J labels are *backlog-ordering hints for humans*, never code inputs |
| T9 | treating the non-inferiority math as a model's job | KEEP mechanical (§5.3 T4) — deterministic arithmetic over model verdicts |
| T10 | `insufficient-data` silently downgrading to save cost | explicit state → KEEP-EXPENSIVE (§4.4); absence of evidence never green-lights a downgrade |
| T11 | the FCTR stamp silently overriding a step's effort/grounded-review intent | tier, `--effort`, and `grounded_review` are orthogonal axes (§8.1); a tier change must not implicitly flip effort. Verify the compiled stamp sets only the tier it claims to |

The §8.3 classification table is the canonical example of the *allowed* use (human backlog ordering) vs the *forbidden* use (a runtime keyword classifier). It is data for people, not a function.

---
