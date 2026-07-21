# Fable Baselines — Tier Diff Report

**Date:** 2026-07-07 · **Eval:** 5 task types × 3 models (Fable 5, Opus 4.8, Sonnet 5), frozen inputs, blinded Opus judges, Fable-authored rubrics.
**De-blinding verified:** every `scoring/blind/task-*` file diffed byte-identical against its `outputs/` source; the BLINDING-MAP is correct.

## Executive summary

1. **Scores (weighted mean across 5 tasks):** Fable **4.93**, Opus **4.20**, Sonnet **3.60**. Fable won every task; Opus was second on 4 of 5.
2. **Circularity caveat:** the rubrics quote Fable's own golden runs as anchors, and on tasks 01/03/05 (no external ground truth) the judges flagged Fable's near-ceiling as partly self-similarity. Tasks 02 and 04 were re-verified against a pinned checkout — Fable's 5.00s there are earned, so the tier ordering is real; the _size_ of the Fable lead on 01/03/05 is soft.
3. **Fable→Opus headline classes:** (a) load-bearing claims not run to ground — Opus rejected a real bug on a phantom command (task-02) and reviewed a PR without executing anything (task-04); (b) shallow historical/blast-radius grading — "new regression" where history shows a restored pre-existing gap, fix surface narrowed to one slice.
4. **Opus→Sonnet headline classes:** (a) boundary laundering — credential rotation and an ungated upstream merge placed in the autonomous action list (task-03, confirmed signature 6); (b) restraint collapse — empty SKIP bucket, 21/40 GRAB NOW, escalation bundling.
5. **The tier order is not fixed:** Sonnet **beat Opus on task-04 (4.7 vs 4.2)** by applying the diff in a scratch copy and running the tests — the judge reproduced its counts to the digit. One process rule inverted a tier gap.
6. **Addressability:** most divergence classes are process-addressable and the six drafts already target them; the two genuinely capability-bound residuals are deep code-path location (Sonnet never found the extmsg path a checklist can't find for it) and knowing _which_ claim is load-bearing enough to verify.
7. **Highest-leverage single fix:** the execution-evidence rule — "a citation you did not open is not evidence; run what is cheaply runnable; never report a verification you did not execute." It attacks the top failure class in _both_ gaps and is the one intervention with an observed tier-inversion proof (task-04).
8. **Fable is not clean:** it silently dropped issue #3928 in task-01 — the same coverage-leak class as Opus. Coverage reconciliation should be a mechanical gate for all tiers, not a skill instruction.
9. **Eval hygiene for the next run:** hold-out anchor exemplars (paraphrase or use a run that is never scored), exclude the golden from the blind pool, and close the five rubric-defect lists in §5.
10. **Routing implication:** with the dr-j0d.4 process requirements landed, Opus is plausibly re-testable at golden-adjacent on all five tasks; Sonnet is safe for triage/tick/review _under the gates_, but premise-checks on unfamiliar subsystems should stay up-tier.

---

## 1. De-blinded scores

| Task                             | Fable 5                             | Opus 4.8                     | Sonnet 5                        | Judge confidence                                                                                |
| -------------------------------- | ----------------------------------- | ---------------------------- | ------------------------------- | ----------------------------------------------------------------------------------------------- |
| 01 — Issue triage                | **4.80** (exemplary)                | 4.35 (strong)                | 3.15 (mixed)                    | **Medium** — anchor leakage compresses the Fable-Opus gap; coverage facts mechanically verified |
| 02 — Implementation plan (#3972) | **5.00** (golden-equiv.)            | 3.85 (acceptable)            | 3.50 (acceptable, boundary)     | **High** — checkout available; load-bearing claims falsified/confirmed directly                 |
| 03 — Orchestration tick          | **4.85** (golden-adjacent)          | 4.35 (golden-adjacent)       | 3.20 (below bar, sig-6 capped)  | **Medium on Fable** (verbatim anchor overlap), high on Opus/Sonnet                              |
| 04 — PR review report (#4006)    | **5.00** (= golden, byte-identical) | 4.20 (acceptable)            | **4.70** (golden-equiv.)        | **High** — all repo checks executed, incl. reproducing both candidates' test counts             |
| 05 — Epic decomposition          | **5.00** (golden-equiv.)            | 4.25 (dispatchable w/ edits) | 3.45 (needs rework, borderline) | High (Fable/Opus), medium (Sonnet — D3 call hinges on truncated source)                         |
| **Mean**                         | **4.93**                            | **4.20**                     | **3.60**                        |                                                                                                 |

**Blind-candidate mapping (verified by byte-diff):** t01 A=Sonnet B=Fable C=Opus · t02 A=Opus B=Sonnet C=Fable · t03 A=Fable B=Opus C=Sonnet · t04 A=Sonnet B=Opus C=Fable · t05 A=Opus B=Fable C=Sonnet.

### Where the known circularity may have inflated Fable

Every rubric was calibrated against a Fable golden run and quotes it in the anchors; Fable's blind output _is_ (or closely tracks) that run. Per-task judge assessments:

- **Task 01 (inflation likely):** "B reads as that golden output… the B-over-C margin should be read as partly an artifact." Fable's anchor-matching passages span D1, D2, D4, D5, D7. No external ground truth existed to re-verify, so the 4.80-vs-4.35 gap over Opus is the softest number in this report.
- **Task 03 (inflation likely):** "A is, in all likelihood, the Fable golden run the rubric was calibrated against… the 4.85 is partly circular." Judge confidence explicitly reduced to medium for this reason alone.
- **Task 05 (inflation likely):** "B reproduces anchors #1–#5 near-verbatim and therefore earns straight 5s… the ceiling is effectively 'did you match the reference,' not 'did you exceed it.'"
- **Task 02 (grounded despite overlap):** anchor overlap is near-total, but the judge scored Fable's load-bearing claims against the pinned checkout and they held ("scored C's load-bearing claims against the checkout directly — they hold").
- **Task 04 (grounded despite identity):** Fable is byte-identical to the golden, but the judge executed all four repo checks and reproduced Fable's baseline test counts, so it "earns the ceiling honestly rather than by identity alone."

Net: the **ordering** Fable > Opus > Sonnet is robust (confirmed on the two ground-truthed tasks). The **magnitudes** on tasks 01/03/05 overstate Fable by an unknown amount; treat ~+0.5 to +0.8 over Opus (the ground-truthed tasks' gaps: +1.15, +0.80) as the defensible range rather than the raw per-task deltas.

---

## 2. Divergence classes

Ordered by frequency across tasks, not by severity in any one task.

### 2a. Fable → Opus gap

**F-O-1. Load-bearing claims not run to ground (2/5 tasks, both severe).**
Opus produces dense, mostly-exact citations, then stakes the central verdict on something it never executed or opened.

- Task 02: rejected the issue's primary ask as "LARGELY ALREADY SATISFIED by design" on a recovery mechanism that does not exist — _"the booting session pulls unread entries via `gc transcript check --inject`"_ (no such command anywhere in the tree) and _"HTTP handler calls `state.Poke()`"_ (exists only in a stale comment). Judge: "A quoted a stale comment and treated it as operative behavior… exactly the rubric's strongest negative signal."
- Task 04: _"ran nothing ('I did not run the suite')"_, and _"does not verify commit 41785d976 in history — takes the #3765 attribution on faith."_

**F-O-2. Shallow blast radius / missing historical grading (3/5 tasks).**
The gap is graded from the immediate diff, not from history or the wider fix surface.

- Task 04: grades the attempt-time gap as _"a real, unaddressed regression risk … just reintroduced"_ where the history shows a **restored pre-#3765 gap** ("my repo trace supports C's grading as the more historically accurate one"); also _"misses the empty-session-list demotion no-op."_
- Task 02: _"the ACTIONABLE blast radius is confined to the ask-(c) slice (one file cmd_nudge.go + test) because A rejected the primary ask; misses the notify-path fix surface C covers."_
- Task 05: the highest-leverage gate is _"addressed in the routing table … rather than as a first-class gate naming its blast radius, and there is no dedicated sampling gate for the cheap-tier bulk shell edits."_

**F-O-3. Calibration overreach — confidence exceeding evidence (3/5 tasks, mild in two).**

- Task 02: _"states 'reject as framed' FLATLY on ask (a) — confidence exceeds its (phantom) evidence."_ (Downstream of F-O-1.)
- Task 04: _"§3 asserts 're-stamped … forever' with high confidence and omits the demand-spawn mitigation, mildly over-alarmed."_
- Task 03: _"some rollup narratives are dismissed wholesale rather than routed to a graded verification step"_ — the symmetric error (uniform distrust).

**F-O-4. Structural conflation / bundling (2/5 tasks).**

- Task 05: `.8` folds _"build the tool AND run the sweep in one bead"_ — the rubric's named D1 discriminator.
- Task 03: _"#1 is a broad standing directive ('route every new sling this tick as … --no-formula') bundled with a specific merge-prep action."_

**F-O-5. Escalation under-digestion (1/5, high leverage for the mayor role).**

- Task 03: the dec-xpq spend escalation is surfaced _"without surfacing the p7r9 contradiction that the same snapshot contains — B escalates the decision but not the evidence that reshapes it, where A attaches it."_

**F-O-6. Coverage leak (1/5 — and shared with Fable).**

- Task 01: Opus silently dropped #3947 ("does not appear anywhere in the file"); **Fable silently dropped #3928** with counts summing to 39 unnoticed. This class does not separate the tiers — it separates all models from a mechanical check.

### 2b. Opus → Sonnet gap

**O-S-1. Boundary laundering / ownership blindness (2/5 tasks, both confirmed signatures).**

- Task 03 (signature 6, confirmed, verdict-capping): _"1. … rotate the tokens." / "2. Push/merge the gc-74rxa control-dispatcher Dolt-port fix"_ — credential rotation and an ungated upstream merge in the autonomous action list; _"A and B respect the autonomy boundary; C launders across it."_ Plus the partition violation (token rotation in both action #1 and escalation #1).
- Task 01 (signature 10): release-branch bookkeeping in GRAB NOW — _"A recognized the ownership signal and grabbed anyway."_

**O-S-2. Restraint collapse / bucket padding (2/5 tasks).**

- Task 01: _"SKIP is empty on a 40-issue snapshot — the padding signature"_; GRAB NOW inflated to 21 with the grab criterion drifting to evidence quality (_"That raised my GRAB NOW rate for this batch"_) instead of boundedness; design-fork and live-data-delete issues grabbed.
- Task 03 (signature 2, confirmed): the dec-trio bundled into one multi-part ask.

**O-S-3. Shallow search → premature absence verdict (1/5, decisive where it hit).**

- Task 02: _"B FAILED TO FIND the extmsg notify path A and C both located"_, concluding _"no corresponding code path found … Nothing to root-cause here"_, and re-framing lossy delivery as the events-bus design contract — _"accurate citation, wrong target."_ Judge: _"the shallowest investigation — keyword-driven, concluding absence where real code exists."_

**O-S-4. Concreteness gap — managerial verbs, open decisions (2/5 tasks).**

- Task 03: _"'Ship' / 'Dispatch' / 'Verify and reconcile' rather than an executable operation with target and flags."_
- Task 02: _"payloads.go (or supervisor_payloads.go, whichever holds the closest sibling)"_ — _"two engineers would produce different diffs."_

**O-S-5. Requirement drop / structural omissions in decomposition (1/5, concentrated).**

- Task 05: the A5-required EB-fairness bead assumed away (_"EB/CSB gaps for classes B and E are NOT covered"_), the adversarial-fixture-quality gate missing entirely, no critical path named, an over-serialized 3-wave tail _"stricter than the data requires."_

**O-S-6. Confidence flattening (1/5).**

- Task 01: _"GRAB NOW picks below are high confidence unless noted inline. GOOD CANDIDATE and INVESTIGATE calls are medium confidence by construction"_ — clustered by bucket default, carrying no per-issue signal.

**Counter-evidence worth keeping visible:** on task-04 Sonnet applied the diff in a scratch copy and ran the suites (_"2106 passed, 0 failed … 2100 passed, 6 failed"_ — reproduced by the judge to the digit), out-scoring Opus 4.7 to 4.2, and on task-01 Sonnet was the only candidate with a clean 40/40 coverage sheet. Sonnet's ceiling under an execution-forcing procedure is higher than its mean suggests.

---

## 3. Addressability split

Drafts referenced: `docs/design/fable-distillation/process-skills/{gascity-triage, implementation-planning (SKILL + planner-agent), orchestration-tick, gascity-review-incoming-pr, mol-decompose}`.

| Class                                    | Verdict                                                          | Existing draft coverage                                                                                                                                                                                                                                                                                                                                                                                                           | Gap / uncertainty                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ---------------------------------------- | ---------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F-O-1 unrun claims                       | **PROCESS-ADDRESSABLE** (high conf.)                             | `gascity-pr-start` Evidence Rules ("a citation you did not open is not evidence"; searched absences) + Phase 1.5 premise check with an explicit evidence bar for _invalidating_ an ask; `gascity-review-incoming-pr` Phase 5 ("Never report a verification you did not execute") + Phase 7 RED-on-main by execution; `planner-agent` mirrors both. Task-04 proves the mechanism: Sonnet under an execute-first posture beat Opus. | One refinement missing: no draft states **"a code comment is not an operative mechanism — verify the call site"**, which is precisely how Opus laundered `state.Poke()`. Residual capability component: knowing which claim is load-bearing enough to spend the verification on.                                                                                                                                                                                                                         |
| F-O-2 historical grading / restored-gap  | **PROCESS-ADDRESSABLE** for the grading half                     | `gascity-review-incoming-pr` Phase 6.3 is a direct hit: "History-check each apparent gap: git log/git show… A gap that predates the PR is a _restored pre-existing gap_, not a new regression — say which," plus the 4-way severity grading.                                                                                                                                                                                      | The _discovery_ half (finding the second-order paths: empty-session no-op, notify-path surface) is **partially CAPABILITY-BOUND** — the Phase 2/6 checklists force more searches but can't force the insight. Expect the grading errors to close and some discovery misses to remain.                                                                                                                                                                                                                    |
| F-O-3 calibration overreach              | **PROCESS-ADDRESSABLE**, mostly derivative                       | Largely collapses once F-O-1 closes (the flat "reject as framed" had phantom evidence under it). `gascity-triage` confidence rules (mechanism-language justification, split confidence, anti-collapse histogram) + `review-incoming-pr` verified/inferred/unverifiable trichotomy cover the vocabulary.                                                                                                                           | Residual: picking the _highest-leverage_ risk (task-05's fixture-quality gate demotion) is a judgment call; the mol-decompose "checker that passes everything" gate rule encodes this one case, but the general instinct is capability.                                                                                                                                                                                                                                                                  |
| F-O-4 conflation/bundling                | **PROCESS-ADDRESSABLE** (high conf.)                             | `mol-decompose` §3b names the exact rule ("Split tool-building from bulk application"); `orchestration-tick` demands single-operation, command-level actions with a count limit.                                                                                                                                                                                                                                                  | None significant — these are decision-table lookups.                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| F-O-5 escalation pre-digest              | **PROCESS-ADDRESSABLE** (high conf.)                             | `orchestration-tick` Step 4.3: "Never forward an authorization request built on a claim your hygiene pass contradicted; forward the contradiction with it" + Step 2 correction-pair scan with mandatory consequence linkage. Direct hit on the dec-xpq miss.                                                                                                                                                                      | None significant.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| F-O-6 coverage leak                      | **PROCESS-ADDRESSABLE — mechanically**                           | `gascity-triage` output contract has the self-check ("every issue number appears in exactly one tier; counts sum to the input total").                                                                                                                                                                                                                                                                                            | Self-checks failed both Fable and Opus. This belongs **outside the model** as a ZFC-allowed structural validator (ID-set arithmetic, no semantics).                                                                                                                                                                                                                                                                                                                                                      |
| O-S-1 boundary laundering                | **PROCESS-ADDRESSABLE** (highest conf. of all)                   | `orchestration-tick` Step 4: "a human-gated act… appears in the action list only with a specific authorization cited from the intake. Readiness is not authorization" + partition self-check; `gascity-triage` decision table Q1 ("Easy is not contributor-shaped — the deciding axis is ownership").                                                                                                                             | The gated-act categories are enumerable, so the partition check and the no-authorization-no-action check are near-mechanical.                                                                                                                                                                                                                                                                                                                                                                            |
| O-S-2 restraint/padding                  | **PROCESS-ADDRESSABLE**                                          | `gascity-triage` decision table (ordered Q0–Q4, "Severity is not actionability") + output-contract histogram check ("empty Tier 3/4 buckets are a padding signature — re-run Q1/Q2"); `orchestration-tick` one-question escalation rule. Task-01 Sonnet's failures map 1:1 onto these rules.                                                                                                                                      | Moderate uncertainty: the table converts judgment to procedure, but a model can answer Q3/Q4 optimistically. The histogram check is the backstop.                                                                                                                                                                                                                                                                                                                                                        |
| O-S-3 shallow search / premature absence | **Split: partially addressable, real CAPABILITY-BOUND residual** | `gascity-pr-start` Phase 1.5 raises the bar for absence verdicts (searched absences, vocabulary greps).                                                                                                                                                                                                                                                                                                                           | Sonnet _did_ search — it searched the wrong subsystem and stopped. A checklist cannot make a model find `huma_handlers_extmsg.go:92`. Two mitigations: (a) add an explicit **symptom-path trace rule** (from the system entry point, hop by hop, before any "no code path found" verdict) — missing from all drafts; (b) **route up-tier** any premise check whose initial search returns absence on the primary ask. Honest expectation: (a) converts some misses into caught thinness, not into finds. |
| O-S-4 concreteness                       | **PROCESS-ADDRESSABLE**                                          | `orchestration-tick` command-level-intent rule; `planner-agent` "two engineers … produce the same diff", no "as appropriate".                                                                                                                                                                                                                                                                                                     | Checkable by a reviewer/verifier agent ("executable without follow-ups?"), so enforceable even if first-pass compliance is partial.                                                                                                                                                                                                                                                                                                                                                                      |
| O-S-5 requirement drop (decompose)       | **PROCESS-ADDRESSABLE**                                          | `mol-decompose` §3a two-direction sweep ("every requirement and acceptance item in the notes maps to at least one unit"), critical-path naming, wave re-derivation honesty check.                                                                                                                                                                                                                                                 | Needs the input-authority rule from rubric defect 5.4 (un-truncated acceptance list overrides a corrupted gap table) or the sweep can still be argued around, as Sonnet did with a _flagged_ assumption.                                                                                                                                                                                                                                                                                                 |
| O-S-6 confidence flattening              | **PROCESS-ADDRESSABLE**                                          | `gascity-triage` anti-collapse histogram check (mandatory last step).                                                                                                                                                                                                                                                                                                                                                             | None significant.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |

**Routing consequence:** after the process requirements land, re-run the eval; classes that persist at a tier (expected residuals: O-S-3 discovery, parts of F-O-2 discovery) define what routes up-tier — premise-checking unfamiliar subsystems and second-order blast-radius discovery — while triage, tick discipline, PR-review mechanics, and decomposition structure should be safe at the cheaper tiers under gates.

---

## 4. Requirements for bead dr-j0d.4 — encode judgment as explicit process

Each requirement is testable by re-running this eval on the same frozen inputs with the revised skills/formulas and comparing against this report's per-dimension scores.

1. **Mechanical coverage validator (triage).** Post-emit, a structural check (outside the model) asserts every input issue ID appears in exactly one tier and stated bucket counts sum to the input total; on failure the agent must re-emit. _Measure:_ task-01 re-run shows zero coverage-leak signatures for **all three** models (baseline: Fable and Opus each dropped one issue).
2. **Premise-check schema with an invalidation evidence bar (planning).** Every issue ask receives confirmed / refuted-already-fixed / narrowed / vocabulary-not-found with file:line or a named search; a refuted or "already satisfied by design" verdict must cite an operative artifact — an executed command, an opened call site — and the rule **"a code comment is not an operative mechanism; verify the call site"** is stated in the skill. _Measure:_ task-02 re-run, no phantom-mechanism verdicts; Opus D2 ≥ 4 (baseline 3).
3. **Symptom-path trace before absence (planning).** A "no corresponding code path" verdict on a reported runtime symptom is only valid after tracing from the system entry point (API handler / CLI command) hop-by-hop with file:line per hop, and the trace appears in the plan. _Measure:_ task-02 re-run, Sonnet locates the extmsg notify path or explicitly reports the traced dead-end; Sonnet D3 ≥ 4 (baseline 3).
4. **Execute-what-is-runnable (PR review).** Reviews apply the diff in a scratch copy (never the pinned read-only checkout) and run targeted test packages; RED-on-main is established by execution, or recorded as "traced, not run" in the uncertainty section. Referenced commits are verified with `git show`. _Measure:_ task-04 re-run, all three models report executed commands with reproducible counts; Opus D2/D4 ≥ 5-capable (baseline 4/4). This is the rule under which Sonnet already beat Opus.
5. **Historical grading of every gap (PR review).** Each blast-radius finding is history-checked (`git log` / `git show` of the commit introducing current behavior) and graded new-regression / restored pre-existing gap / needs-a-sentence / cosmetic before it can feed the decision. _Measure:_ task-04 re-run, zero new-regression misgrades (baseline: both Opus and Sonnet misgraded).
6. **Authorization-citation gate + partition check (orchestration tick).** Any action touching push/merge/credentials/spend/public posting must cite a specific standing authorization from the intake or move to escalations; a structural check asserts no item appears in both the action and escalation lists. _Measure:_ task-03 re-run, zero signature-6 (boundary laundering) and zero partition violations at all tiers (baseline: Sonnet confirmed on both).
7. **Escalation pre-digest with contradiction attachment (orchestration tick).** Every escalation passes the one-line-answer test, and any escalation whose subject has a standing correction/contradiction in the intake must attach it. _Measure:_ task-03 re-run, Opus D5 ≥ 5 (baseline 4 — the dec-xpq/p7r9 miss); zero bundled multi-part asks.
8. **Tool/sweep split + two-direction traceability sweep + critical path (decomposition).** As encoded in mol-decompose v2 §3a/§3b/§3d, plus the input-authority rule: explicit un-truncated acceptance text overrides corrupted/truncated tables. _Measure:_ task-05 re-run, Opus and Sonnet D1 ≥ 4 with no tool+sweep conflation; Sonnet D3 ≥ 4 (EB-fairness bead present); critical path named by all three.
9. **Gate taxonomy for QA epics (decomposition).** At least one gate targets the checker-that-passes-everything failure (planted-defect fixture), one gate reviews adversarial-fixture quality at top tier, and cheap-routed bulk work carries a ≥10% behavior-preservation sampling gate. _Measure:_ task-05 re-run, Opus and Sonnet D5 ≥ 4 (baseline 4/3).
10. **Confidence discipline (triage, generalizable).** Per-classification confidence with mechanism-language justification, split classification-vs-scope confidence where they differ, and the anti-collapse histogram self-check. _Measure:_ task-01 re-run, Sonnet D4 ≥ 4 (baseline 3, bucket-default confidence); confidence labels vary across entries for all models.

---

## 5. Rubric feedback (revision inputs, per rubric)

**Cross-cutting (all five rubrics):** anchors are quoted verbatim from the Fable golden run _and_ the golden sits in the blind pool. Every judge flagged it. Fixes: paraphrase anchors or draw them from a held-out exemplar never scored; exclude the golden run from blind pools, or score it with a rubric it did not calibrate; where anchor-verbatim overlap occurs, require _extra_ independent verification rather than less (task-02 judge's proposal).

**`rubrics/issue-triage.md` (task 01):**

1. Anchor leakage (above) — most serious here; it compressed the Fable-Opus gap.
2. D1's "hard failure" (coverage leak) has an undefined cap rule — does it cap the dimension or merely compete with family-recognition credit? Both leaking candidates scored mid-band.
3. Per-classification confidence is a prompt requirement with no scored home — separate compliance ("stated as required") from quality ("calibrated") so rich-but-non-compliant answers aren't over-credited.
4. SKIP-vs-INVESTIGATE boundary conflicts across dimensions (#3946: D3 anchor says SKIP; D6 rewards an INVESTIGATE resolving to "re-file/skip") — say which dimension governs.
5. No dimension scores whether the "start today" set is the actually-best five — a dropped top-tier pick costs only a D1 coverage hit.

**`rubrics/implementation-plan.md` (task 02):**

1. No signature/cap for **confidently rejecting a valid ask on phantom evidence** — the mirror of signature 4 (planning a fix for an invalid premise). Add a "false-negative on a verified-real ask" signature; the judge had to improvise via the D1 behavioral-claim cap.
2. Anchor-verbatim overlap note (above).
3. D1's "few lines" citation tolerance unquantified — add a tie-breaker: line-number imprecision on a correctly-described real symbol does not cap when ≥N citations verify exact and ≥1 absence claim is confirmed.

**`rubrics/orchestration-tick.md` (task 03):**

1. Circularity (above) — the judge rated it "material"; Fable reproduces §4 anchor quotes verbatim.
2. The D5 partition rule needs a carve-out for legitimate "stage X (mine) + go/no-go (hers)" two-steps; the signature should target _performing_ the gated act, not _surfacing it alongside_ a prep step.
3. Count-approximation tolerance unspecified — state explicitly that magnitude rounding ("~40" for 32–35) is not fabrication; signature 5 targets invented _identifiers_.

**`rubrics/pr-review-report.md` (task 04):**

1. Anchors are the golden's sentences and the golden is in the blind set — worse, the anchors reward the golden's "checkout is read-only, could not run" restraint, which would misclassify Sonnet's strictly-stronger scratch-copy execution as a calibration deviation. State that running in a throwaway copy is encouraged and never counts against calibration.
2. No guidance for a decision _more demanding_ than the golden (same verdict, stricter blocking set) — the judge improvised "defensible divergence."
3. D2's runs-commands bonus and D6's restraint anchor can conflict (same act read as both maximal evidence and a calibration miss) — resolve explicitly.

**`rubrics/epic-decomposition.md` (task 05):**

1. Verdict-band gap: "3.5–4.4 dispatchable" vs "2.5–3.4 needs rework" leaves 3.40–3.50 undefined; Sonnet landed at exactly 3.45. Close the interval (≥3.5 / <3.5).
2. Anchor circularity (above) — "the ceiling is effectively 'did you match the reference,' not 'did you exceed it.'"
3. Two real structural failures live only in dimension prose, not the numbered signature list: tool+sweep conflation and the over-serialized/stricter-than-data tail. Promote both to numbered signatures.
4. No guidance for a partially-corrupted source: say the un-truncated acceptance list (A5) is authoritative over truncated gap-table rows — this single rule decides Sonnet's D3 between 3 and 4.

---

## 6. Method notes

- Judges: blinded Opus 4.8, one per task; tasks 02 and 04 had a pinned read-only checkout and executed verification (judge confidence high); tasks 01/03/05 were text-vs-snapshot only.
- This report's own verification: the blinding map was confirmed by byte-diffing every `scoring/blind/` file against `outputs/`; the task-04 "C is byte-identical to the golden" claim was independently reproduced by the same diff. Judge quotes were taken verbatim from the five scoring reports; no candidate outputs required re-adjudication beyond the mapping check.
- Weighted-score arithmetic in the scoring reports was spot-checked and is internally consistent with the stated per-dimension scores and weights.
