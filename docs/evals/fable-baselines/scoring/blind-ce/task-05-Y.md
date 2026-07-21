# Bead Decomposition — dr-2vydrm (Three-benchmark QA framework)

Source: epic snapshot only. Bead numbers follow the epic's own dr-2vydrm.N numbering where the epic names them explicitly; where the epic implies a bead but does not name it, I've assigned the next unused number and flagged it as inferred.

## 1. Child-bead breakdown

### Prerequisite (external to this epic's own numbering, but gates everything)

**Bead: codeprobe-voxa (reopened) — reward vs IR diagnostics separation**
Scope: Land the reference `ScoreResult` contract implementation in codeprobe — the reward/scorer_family/sub_scores/diagnostics shape that dr-2vydrm.1, .4, and .7 all depend on. The epic states this bead was reopened specifically because the recall-only reward it originally shipped doesn't satisfy the broadened acceptance (unified cross-rig contract), so this is not just "land it," it's "land the corrected version."
Acceptance criterion (testable): Run codeprobe's scorer against a golden fixture and pipe the result through a JSON-schema check for the exact shape in the epic — `reward: float[0,1]`, `scorer_family: string`, `sub_scores: object`, `diagnostics.task_time_seconds: float`, `diagnostics.token_cost_usd: float`, `diagnostics.ir_metrics.{precision,recall,f1}` all present. A verification agent runs `jq` (or equivalent) against the emitted JSON and asserts every key path exists with the correct type — not "the reward looks right."
Dependencies: none (this is the root of the graph).
Size class: multi-session (it's a reopen/rework of prior work, not a fresh scoped task).

### Foundational lib

**dr-2vydrm.1 — benchmark_qa_core lib**
Scope: Build the shared, schema-agnostic Python library (~300 lines) of pure functions `(instruction, oracle, repo_path, aux_files) → findings`, housing defect classes A (oracle-instruction coherence), D (scoring-method honesty), and E (agent-agnostic fairness). Also defines the schema constants for the unified `ScoreResult` contract, absorbed from the codeprobe-voxa reference implementation, so all three rigs import the same constants rather than re-declaring the shape.
Acceptance criterion (testable): `python -c "import benchmark_qa_core; assert benchmark_qa_core.SCHEMA_KEYS == {...}"` matches the exact contract keys from the epic; a unit test per class (A/D/E) run against a known-good and known-bad fixture returns the expected findings list — `pytest tests/test_benchmark_qa_core.py -v` exits 0 with at least one test per class asserting non-empty findings on the bad fixture and empty findings on the good one.
Dependencies: codeprobe-voxa (must land first — the lib absorbs its schema).
Size class: one-session for the core implementation; treat test coverage across all three classes as part of the same bead, not a follow-up (per tests-ship-with-fixes).

### Per-rig adapters (class A/D/E integration)

**dr-2vydrm.2 — CSB adapter** (inferred number; not named explicitly in the epic text, only implied by "adapters (.2/.3/.4) port")
Scope: Wire CSB's existing task-validation path to call `benchmark_qa_core` for classes A/D/E, replacing or supplementing whatever ad-hoc checks CSB currently runs, and to emit the unified `ScoreResult` contract.
Acceptance criterion (testable): Run the adapter against one known-broken CSB task (oracle path that doesn't exist) and assert the returned findings list is non-empty and flags class A; run against a clean task and assert the emitted result validates against the lib's schema constants.
Dependencies: dr-2vydrm.1 (lib).
Size class: one-session.

**dr-2vydrm.3 — EB adapter: extend `lib/eb_verify/schema_validator.py` to call benchmark_qa_core (classes A/D/E)**
Scope: Exactly as named in the epic — extend EB's existing `schema_validator.py` to delegate classes A/D/E to the shared lib, and emit the unified contract.
Acceptance criterion (testable): `python -m eb_verify.schema_validator --task <known-bad-fixture>` exits non-zero (or returns findings) flagging the injected defect; `--task <golden-fixture>` returns clean and the output JSON matches the `ScoreResult` schema.
Dependencies: dr-2vydrm.1 (lib).
Size class: one-session.

**dr-2vydrm.4 — codeprobe adapter** (inferred number; implied by "adapters (.2/.3/.4) port")
Scope: Wire codeprobe's task pipeline to call `benchmark_qa_core` for classes A/D/E and emit the unified contract, reconciling with the already-closed zat9 oracle-curator work (class A) so the two don't duplicate or conflict.
Acceptance criterion (testable): Run the adapter against a codeprobe task with a known symbol-reference oracle gap and assert class A findings fire; confirm no regression against zat9's existing test suite (`pytest tests/ -k zat9` or equivalent still passes).
Dependencies: dr-2vydrm.1 (lib); read-only dependency on codeprobe-zat9's existing output (already closed, not re-opened by this bead).
Size class: one-session.

### Per-rig calibration triad (class C)

**dr-2vydrm.5 — CSB calibration triad** (inferred number, implied by ".5/.6/.7 cover all 3 rigs")
Scope: Implement/extend CSB's calibration check so the null fixture scores ≤0.1, the golden fixture scores ≥0.9, and the adversarial-keyword-dump fixture scores ≤0.5 — run against the new `ScoreResult` contract, not the old pass/fail scheme.
Acceptance criterion (testable): A single command runs all three fixtures and prints three reward values; a verification agent asserts `null.reward <= 0.1`, `golden.reward >= 0.9`, `adversarial.reward <= 0.5` from the actual command output, and that all three outputs validate against the contract schema (not just print a bare float).
Dependencies: dr-2vydrm.2 (CSB adapter must emit the contract first).
Size class: one-session.

**dr-2vydrm.6 — EB calibration triad: `eb_verify.calibrate` subcommand (null/golden/adversarial gates)**
Scope: Exactly as named — add a `calibrate` subcommand to `eb_verify` implementing the same three-fixture gate against the unified contract.
Acceptance criterion (testable): `python -m eb_verify calibrate` exits 0 and prints three JSON blocks; assert `null.reward <= 0.1`, `golden.reward >= 0.9`, `adversarial.reward <= 0.5`, each validating against the lib's schema constants.
Dependencies: dr-2vydrm.3 (EB adapter).
Size class: one-session.

**dr-2vydrm.7 — codeprobe calibration triad** (inferred number, implied by ".5/.6/.7 cover all 3 rigs")
Scope: Same three-fixture gate for codeprobe, against the unified contract inherited from codeprobe-voxa.
Acceptance criterion (testable): Same pattern — run the three fixtures, assert the three reward thresholds and schema validation on the actual command output.
Dependencies: dr-2vydrm.4 (codeprobe adapter); codeprobe-voxa (reference contract).
Size class: one-session.

### Verifier mechanical correctness (class B)

**dr-2vydrm.8 — CSB verifier-lint**
Scope: Bring CSB's `test.sh` verifiers to mechanical-correctness compliance: `set -euo pipefail` present, no `grep|head` anti-pattern, shellcheck clean. The epic states 42% of 781 files (≈328 files) currently fail.
Acceptance criterion (testable): `shellcheck **/test.sh` (or the rig's actual glob) exits 0 across all 781 files; a grep for `grep.*\|.*head` across the corpus returns zero matches; a grep confirming `set -euo pipefail` is present in every file's header returns 781/781.
Dependencies: none technical (class B is per-benchmark, doesn't touch the lib) — but should not be dispatched before the corpus is stable, i.e., not concurrently with adapter work that touches the same files.
Size class: needs-split — 328 failing files is too large for one session; split into a scripted bulk-fix pass (mechanical, e.g. auto-inserting the `set` line, auto-rewriting `grep|head`) plus a manual-remainder pass for whatever shellcheck flags that scripting can't safely auto-fix.

**dr-2vydrm.11 — codeprobe verifier-honesty lint (class B analog)**
Scope: Equivalent mechanical-correctness pass for codeprobe's verifiers (whatever their runtime shape is — the epic doesn't specify codeprobe's verifier language, only that this is "class B analog").
Acceptance criterion (testable): Whatever the codeprobe verifier lint tool reports, assert 0 findings across the full corpus (the epic gives no file count for codeprobe, so the concrete pass/fail baseline must be established as this bead's first action, then driven to zero — the acceptance is "post-fix run shows 0 findings," not a specific number).
Dependencies: none technical.
Size class: one-session, pending confirmation of corpus size (see Underspecified §5).

### Agent-agnostic fairness (class E)

**dr-2vydrm.10 — codeprobe agent-agnostic fairness scan**
Scope: Scan codeprobe's CLAUDE.md/AGENTS.md/README files (and equivalents) for oracle-path leakage — content that would let an agent infer the answer from repo docs rather than solving the task.
Acceptance criterion (testable): A scan script run against the full codeprobe task corpus returns 0 flagged files; a verification agent seeds one deliberately-leaked oracle path into a test fixture doc and confirms the scan catches it (true-positive check, not just "ran clean").
Dependencies: dr-2vydrm.1 (lib houses class E logic) and dr-2vydrm.4 (codeprobe adapter) if the scan is implemented via the adapter path rather than standalone.
Size class: one-session.

### Cross-rig diagnostics

**dr-2vydrm.9 — Diagnostics plumbing — task_time_seconds + token_cost_usd surfaced per-trial across codeprobe/EB/CSB**
Scope: Instrument each rig's trial runner to capture and surface `task_time_seconds` and `token_cost_usd` per trial, populating the `diagnostics` block of the unified contract (the `ir_metrics` sub-block is covered separately by each rig's own scoring logic, not this bead).
Acceptance criterion (testable): Run one trial per rig and assert the emitted `ScoreResult.diagnostics.task_time_seconds` and `.token_cost_usd` are both present, numeric, and non-zero (a zero/null value on a real trial is a bug, not a valid state).
Dependencies: none on dr-2vydrm.1 strictly required (this is trial-runner instrumentation, not schema logic) — but the _values_ only mean something once the adapters (.2/.3/.4) are emitting the containing contract, so land the instrumentation any time, verify end-to-end only after adapters exist.
Size class: multi-session (touches three different codebases/runners with presumably different languages/frameworks).

### Cross-rig aggregation

**dr-2vydrm.12 — Cross-rig dashboard / aggregate.json** (inferred number; required by acceptance item A6, not explicitly numbered in the epic text)
Scope: Produce an `aggregate.json` (or equivalent) that rolls up reward + scorer_family + sub_scores across all three rigs, applying CSB's reporting policy so mean-reward aggregates carry a scorer_family caveat whenever rigs with different scorer families are mixed together.
Acceptance criterion (testable): Run the aggregator against fixture results from all three rigs where two share a scorer_family and one differs; assert the output JSON contains a caveat field/flag on the mixed aggregate and does NOT silently average across incompatible scorer families without flagging it.
Dependencies: dr-2vydrm.2/.3/.4 (adapters, for reward/scorer_family/sub_scores) and dr-2vydrm.9 (diagnostics).
Size class: one-session.

### Corpus QA sweep + follow-up filing (carried over from original acceptance — see Underspecified §2)

**dr-2vydrm.13-csb / .13-eb / .13-codeprobe — Run new QA on existing corpus, file follow-up beads for failures**
Scope: Per rig, run the fully-landed QA pipeline (lib + adapter + triad + lint/fairness) against that rig's existing task corpus and file a follow-up bead for every failure found, mirroring the structure of the original audit (16% usable as-is / 88% usable after fixes / 12% broken).
Acceptance criterion (testable): For CSB specifically, a run against all 781 tasks produces a results file with one row per task and a disposition (pass/fixable/broken); count of filed follow-up beads equals count of non-passing tasks (a verification agent counts bead-tracker entries against the results file's failure count — they must match, not just "some beads got filed").
Dependencies: CSB sub-bead depends on .2, .5, .8; EB sub-bead depends on .3, .6; codeprobe sub-bead depends on .4, .7, .10, .11.
Size class: needs-split, especially CSB (781 tasks) — split into automated sweep + manual triage-and-file pass.

### Public response doc

**dr-2vydrm.14 — Public response doc draft** (inferred number; required by original acceptance, not explicitly numbered)
Scope: Draft (private repo, not published) a response document summarizing remediation status against the original VERIFICATION_REPORT's findings, covering all five defect classes across all three rigs.
Acceptance criterion (testable): The doc exists at a defined path, contains one section per defect class (A-E) with a status line referencing the actual closed/open bead IDs for each rig, and every referenced bead ID resolves to a real tracker entry (a verification agent greps the doc for bead-ID references and checks each against the tracker — not "reads well").
Dependencies: dr-2vydrm.13-* (all three corpus sweeps, since the doc summarizes actual remediation results, not planned work).
Size class: one-session.

## 2. Dependency graph and parallel waves

```
Wave 0: codeprobe-voxa (reopened)                         [prerequisite, already in flight]
Wave 1: dr-2vydrm.1 (lib)         || dr-2vydrm.9 (diagnostics plumbing)   || dr-2vydrm.8 (CSB lint) || dr-2vydrm.11 (codeprobe lint)
Wave 2: dr-2vydrm.2 (CSB adapter) || dr-2vydrm.3 (EB adapter) || dr-2vydrm.4 (codeprobe adapter)
Wave 3: dr-2vydrm.5 (CSB triad)   || dr-2vydrm.6 (EB triad)   || dr-2vydrm.7 (codeprobe triad)  || dr-2vydrm.10 (codeprobe fairness)
Wave 4: dr-2vydrm.12 (aggregate dashboard)
Wave 5: dr-2vydrm.13-csb || dr-2vydrm.13-eb || dr-2vydrm.13-codeprobe
Wave 6: dr-2vydrm.14 (public response doc)
```

Notes on placement that deviate from a naive reading:

- `.9` (diagnostics plumbing) and `.8`/`.11` (lint) don't structurally depend on the lib or voxa — they're per-rig mechanical/instrumentation work. I've pulled them forward into Wave 1 to maximize parallelism, rather than stranding them in the epic's stated "lint + fairness in parallel" tail wave. This is a deviation from the epic's literal sequencing text and should be confirmed (see Underspecified §4) — the epic may have grouped them at the end for a reason not stated (e.g., wanting adapter churn to settle on the same files lint touches).
- `.10` (fairness scan) does depend on the lib/adapter (class E logic lives in `benchmark_qa_core`), so it's Wave 3, not Wave 1, even though it's grouped with "lint" in the epic's prose.
- Everything in Wave 2 and Wave 3 is three-way parallel by rig, matching the epic's explicit "adapters (.2/.3/.4) port... triad (.5/.6/.7) calibrate" sequencing.

## 3. Model-tier routing

| Bead                                    | Tier                                                                                                               | Reason                                                                                                                                                                                                                 |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| codeprobe-voxa                          | Top-tier                                                                                                           | Defines the reference contract every other rig inherits; a wrong shape here silently poisons .1, .4, .7, and eventually .12.                                                                                           |
| dr-2vydrm.1 (lib)                       | Top-tier for the API/schema design, mid-tier acceptable for the class A/D/E implementation once the shape is fixed | Highest blast radius in the graph — every rig imports it.                                                                                                                                                              |
| .2/.3/.4 (adapters)                     | Mid-tier                                                                                                           | Well-scoped integration work against an already-defined lib interface; no open design decisions.                                                                                                                       |
| .5/.6/.7 (triad)                        | Mid-tier                                                                                                           | Mechanical once the contract exists — three threshold checks against three fixtures.                                                                                                                                   |
| .8 (CSB lint), .11 (codeprobe lint)     | Cheap tier for the scripted bulk-fix pass; mid-tier for the manual remainder                                       | Shellcheck/pipefail fixes are pattern-mechanical at volume; only the shellcheck findings that resist scripting need judgment.                                                                                          |
| .9 (diagnostics plumbing)               | Mid-tier                                                                                                           | Touches three different runtimes/languages; needs consistent design across them but no deep architectural reasoning.                                                                                                   |
| .10 (fairness scan)                     | Cheap tier for the scan itself, mid-tier for triaging ambiguous hits                                               | Oracle-path leakage detection is largely mechanical (path/string matching against known oracle locations), but borderline cases (is this a legitimate doc reference or a leak?) need judgment.                         |
| .12 (aggregate dashboard)               | Mid-tier                                                                                                           | Requires correctly implementing the scorer_family-mixing caveat policy — a real (if narrow) piece of logic, not pure plumbing.                                                                                         |
| .13-* (corpus sweep + follow-up filing) | Mid-tier, CSB sub-bead leans top-tier                                                                              | Judging pass/fixable/broken across 781 tasks reproduces the original audit's judgment calls; CSB's volume and precedent-setting nature (the 16/88/12% split came from this exact exercise) warrant the stronger model. |
| .14 (public response doc)               | Top-tier                                                                                                           | Externally-oriented synthesis document; accuracy and framing matter even pre-publication, and it's the artifact most likely to eventually need to withstand outside scrutiny.                                          |

## 4. Review gates

1. **After codeprobe-voxa, before .1 starts.** Verify the emitted contract shape matches the epic's JSON block exactly (key names, types, nesting) before the lib absorbs it as its schema constants. This is the single highest-leverage gate in the graph — every downstream bead trusts this shape without re-deriving it.
2. **After .1, before Wave 2 (adapters) starts.** Verify the lib's public function signature and per-class (A/D/E) test coverage independently of any one rig's adapter code, since three adapters will each build on whatever the lib exposes — a defect found after all three adapters land triples the rework.
3. **After Wave 2 (adapters), before Wave 3 (triad) starts.** Verify each adapter actually emits the _full_ contract (not a partial shape that happens to pass a shallow test), since the triad's whole job is asserting fixture rewards against contract-shaped output — a triad that "passes" against a malformed contract is a false green.
4. **Before dispatching dr-2vydrm.4 (codeprobe adapter).** Confirm it doesn't duplicate or conflict with the already-closed codeprobe-zat9 oracle-curator work — read zat9's diff/output first, since the epic explicitly calls out zat9 as prior art for class A on this rig.
5. _*Before Wave 5 (.13-* corpus sweeps) dispatch._* Confirm Wave 3 and any lint/fairness beads that rig depends on are genuinely closed (not just marked closed) — a sweep run against a partially-working QA pipeline produces a corpus disposition that's wrong at scale (781 tasks for CSB), and re-running it is expensive.
6. **Before dr-2vydrm.14 (public response doc) is considered done.** A reviewer (not the same agent that wrote it) cross-checks every bead-ID reference in the doc against the actual tracker state — this is the artifact most likely to eventually go external, so even in its private/pre-publication state it should be held to the same bar as a public-facing document per the standing writing-voice / preview-before-execute rules.
7. **Scope-completeness gate before Wave 3/4 dispatch, not a quality gate but a coverage gate:** a human must resolve whether CSB and EB need their own class-E fairness-scan beads and class-B lint analogs (see Underspecified §1) before those waves are considered fully planned — dispatching Wave 3/4 as currently scoped would silently leave 2 of 3 rigs uncovered for those defect classes if the gap is real.

## 5. Underspecified — needs an answer before dispatch

1. **The defect-class-coverage table (epic lines ~113-122) is truncated/garbled in the source** ("EB has no upstream zat…", "EB no shell verifiers;…", "new bead per rig (code…"). I cannot determine whether EB and CSB are meant to get their own class-E fairness-scan beads and class-B lint analogs, or whether those gaps are intentionally out of scope for this epic. **Provisional assumption:** only codeprobe gets explicit new beads (.10 for E, .11 for B-analog); EB/CSB gaps for classes B and E are NOT covered by this epic's children and would need separate beads filed later. This should be confirmed with whoever wrote the update before Wave 3/4 dispatch, since it changes the total bead count.
2. **Whether the original acceptance items — "each rig has run the new QA on its existing corpus and filed follow-up beads for failures" and "public response doc drafted" — still apply after the 2026-04-30 tightening.** The "Updated acceptance" (A1-A6) doesn't explicitly restate either item; the update frames itself as broadening scope ("the actual gap is broader"), not dropping items. **Provisional assumption:** both remain binding, hence .13-* and .14 are kept as beads. If they were meant to be dropped, that removes two waves of work.
3. **EB's class-A gap** ("EB has no upstream zat9 analog"). Unclear whether this means EB needs its own oracle-curation _tool_ (mirroring zat9's symbol-reference-trace mining) beyond what the schema-validator extension (.3) provides, or whether the gap is just "EB lacks pre-curated oracle data" (a corpus problem, not a code problem) that .3's coherence-check logic already handles once applied. **Provisional assumption:** .3 is sufficient; no separate EB oracle-curation bead is needed. Needs confirmation — if wrong, EB is under-scoped for class A.
4. **Placement of dr-2vydrm.9 (diagnostics plumbing) and the lint beads (.8/.11) relative to the epic's stated sequence.** The epic's explicit sequencing text ("voxa → lib → adapters → triad → lint+fairness in parallel") doesn't mention .9 at all, and groups .8/.11 with fairness in a trailing wave. I moved .9 and the lint beads forward to Wave 1 for parallelism since they don't structurally depend on the lib. **Provisional assumption:** this reordering is safe. If the epic's trailing-wave grouping was deliberate (e.g., to avoid touching the same files that adapter work is mid-flight on), moving lint earlier could create merge conflicts with .2-.4.
5. **Size baseline for dr-2vydrm.11 (codeprobe lint).** The epic gives CSB's exact failure count (42% of 781) but no equivalent number for codeprobe. Sizing (.11) as one-session assumes a materially smaller corpus than CSB's 781 — unconfirmed. If codeprobe's verifier corpus is comparably large, .11 needs the same needs-split treatment as .8.
6. **Whether codeprobe-zat9 and EnterpriseBench-0rv.25 (both already closed) need re-verification against the new unified contract**, since they closed under acceptance criteria that predate the 2026-04-30 tightening. **Provisional assumption:** no rework needed — zat9's class-A coverage is structurally contract-agnostic (oracle existence checks don't care about the reward schema), and EB-0rv.25 was explicitly scoped as a point-fix, not the structural framework. This should be a quick confirmation, not a re-open, given the "out of scope: re-mining/re-authoring" epic boundary.
7. **No concrete file paths are given for the CSB or codeprobe adapters** (only EB's `lib/eb_verify/schema_validator.py` is named in the epic). The acceptance criteria for .2 and .4 above are written against an assumed analogous module; the actual path/entry-point needs to be confirmed against each rig's real layout before a worker can be dispatched with a concrete verification command.
