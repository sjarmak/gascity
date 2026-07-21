# CE Judge Report — Task 05 (Epic Decomposition, dr-2vydrm)

**Judge:** Opus 4.8, blind CE. Scored only against the frozen epic
(`inputs/epic-dr-2vydrm.txt`) and the rubric
(`rubrics/epic-decomposition.md`). No guess at harness condition.

---

## Candidate X

| Dim                     | Score | Cap fired | Citation                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| ----------------------- | ----- | --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1 Structure (25%)      | **5** | —         | Tool/sweep genuinely split into two dispatchable beads: **W8a** "Build the lint tool only" vs **W8b** "CSB verifier-lint remediation sweep." Critical path named: "codeprobe-voxa (external) → W1 (LIB) → W1 REVIEW → W4 (codeprobe adapter) → W7 or W13c → W15." Real parallelism, stated and self-audited: "wave 0 has 4 independent units, wave 1 has 7, wave 2 has 7, wave 3 has 1." Subtle correct data-dependency call: "lint (W8a/W8b) and the class-B analogs (W9/W12) have zero data dependency on the ScoreResult contract … Fairness (W10/W11), by contrast, genuinely depends on W1." Waves re-derive cleanly from the per-bead deps (checked: no bead scheduled at/before a dependency). |
| D2 Testability (25%)    | **5** | —         | Verifiable escape hatch matching the golden ceiling anchor: **W8b** — "0 failures OR only files in a committed exceptions list where each entry carries a filed follow-up bead ID (verifier cross-checks each via `bd show`)." Negative fixtures throughout: **W1** — "A planted fixture with a dangling oracle path returns a class-A finding; a clean fixture with a valid oracle path returns none." Residual judgment named, not faked: **W9/W12** acceptance left explicitly "cannot be fully specified from epic text as given — pin required" rather than inventing a rule.                                                                                                                    |
| D3 Traceability (15%)   | **5** | —         | Every unit carries a `Trace:` line to epic clause/acceptance item. Out-of-scope honored and restated in a dedicated section: "A CSB-specific fairness-scan bead — literal reading of A5 excludes it." Public-push and irrecoverable-re-mining boundaries restated as non-units. Bidirectional coverage: A1–A6 plus the original corpus-run and response-doc acceptance items all mapped.                                                                                                                                                                                                                                                                                                              |
| D4 Routing (10%)        | **5** | —         | Tiering by where judgment lives, not size: **W8b** "mechanical, prescriptive — semantics decided upstream in W8a"; **W1** "deep reasoning … wrong A/D/E semantics propagate silently to every rig." Cheap tier actually used (W8b/W10/W11/W13) and mitigated by sampling gates. Budget self-check: "3 of 16 new units (~19%) sit at the top tier."                                                                                                                                                                                                                                                                                                                                                    |
| D5 Gate placement (15%) | **4** | —         | Hits the QA-epic "checker that passes everything" anchor: **W8a review** "must run the lint tool against a corpus known to contain violations and confirm the report isn't a vacuous `0/781`." Fan-out gate names failure + blast radius: W1 REVIEW gates six consumers, "every downstream rig reports a clean corpus that isn't." Bulk gates sample ≥10% for behavior preservation. **Held to 4, not 5:** no gate targets calibration-fixture _quality_ — the rubric's own anchor-2 poison ("a weak adversarial fixture blesses gameable verifiers," which gates sjarmak's upstream push). W5's triad only threshold-checks reward ≤0.5; a gameable adversarial fixture passes silently.             |
| D6 Underspec (10%)      | **5** | —         | Ten load-bearing pins, each with a "pinned by" gate/wave and stated consequence: e.g. EB class-A gap — "if wrong, class-A coverage for EB is void even though W3 'passes.'" Catches the epic's structural ambiguity (Section 0: only .3/.6/.9 exist as live children vs .1–.11 narrated). Distinguishes narrative sequencing from data dependency (item 9, lint-vs-fairness depth) — the golden anchor-3 move.                                                                                                                                                                                                                                                                                        |

**Weighted overall (X):**
0.25·5 + 0.25·5 + 0.15·5 + 0.10·5 + 0.15·4 + 0.10·5 = **4.85** → **golden-equivalent** (≥4.5).

---

## Candidate Y

| Dim                     | Score | Cap fired                       | Citation                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| ----------------------- | ----- | ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| D1 Structure (25%)      | **3** | D1=3 (conflate two dispatches)  | Lint is one bead that fuses build + remediation, no separate reusable checker: **dr-2vydrm.8** — "Size class: needs-split … split into a scripted bulk-fix pass … plus a manual-remainder pass." The split lives in a size note, not in two dispatchable beads, and there is no tool bead to gate against planted violations. No critical path is named. Parallel waves are otherwise genuine and derivable (wave 2/3 three-way by rig), and size classes vary — so above the linear-chain floor, but the tool/sweep conflation is the explicit D1=3 anchor. |
| D2 Testability (25%)    | **3** | D2=3 (vacuous-testable, sig #2) | Fires on **dr-2vydrm.11**: "the acceptance is 'post-fix run shows 0 findings,' not a specific number" — a stubbed/broken lint tool that finds nothing passes this check. Most other criteria are strong and discriminating (**.10**: "seeds one deliberately-leaked oracle path … and confirms the scan catches it (true-positive check, not just 'ran clean')"; .1/.2/.3 use known-bad + known-good fixtures), which is why the substance is near-4 — but the single vacuous criterion caps the dimension at 3 under strict application.                    |
| D3 Traceability (15%)   | **4** | —                               | Coverage complete and inferred beads flagged honestly: **.12** "required by acceptance item A6, not explicitly numbered in the epic text"; adapters marked "(inferred number …)". Does not gold-plate CSB fairness (underspec §1 defers it). Below 5 only because there is no dedicated out-of-scope restatement and traces are looser (e.g. "implied by 'adapters (.2/.3/.4) port'") than X's per-unit clause citations.                                                                                                                                    |
| D4 Routing (10%)        | **4** | —                               | Reasons grounded in blast radius: **voxa** "a wrong shape here silently poisons .1, .4, .7"; cheap tier really used — **.8/.11** "Cheap tier for the scripted bulk-fix pass; mid-tier for the manual remainder." Slightly hedgy (several beads split a tier within one row, e.g. ".1 top-tier for API/schema design, mid-tier acceptable for the … implementation"), but calibrated by judgment, not size.                                                                                                                                                   |
| D5 Gate placement (15%) | **4** | —                               | Seam gates that name failure + blast radius: gate 2 "a defect found after all three adapters land triples the rework"; gate 3 "a triad that 'passes' against a malformed contract is a false green." Not theater. Below 5: no gate targets the "checker that passes everything" mode (Y has no separate checker to validate), gate cost is fairly uniform (mostly closure/shape verification), and no bulk-edit sampling mechanism.                                                                                                                          |
| D6 Underspec (10%)      | **4** | —                               | Seven load-bearing pins with provisional assumptions, including one X does not raise as sharply: item 2 — "Whether the original acceptance items … still apply after the 2026-04-30 tightening," noting "If they were meant to be dropped, that removes two waves of work." Also catches missing adapter file paths. Below 5: fewer pins, and consequence-if-resolved-otherwise is stated less consistently than X.                                                                                                                                          |

**Weighted overall (Y):**
0.25·3 + 0.25·3 + 0.15·4 + 0.10·4 + 0.15·4 + 0.10·4 = **3.50** → **dispatchable with edits** (bottom of band).

---

## Failure signatures

| #   | Signature                     | Verdict                          | Evidence                                                                                                                                                                                           |
| --- | ----------------------------- | -------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Restated-scope criteria       | **Neither**                      | Both give commands/observables, not passive-voice scope restatements.                                                                                                                              |
| 2   | Vacuous-testable              | **Y**                            | Y `.11`: "the acceptance is 'post-fix run shows 0 findings,' not a specific number" — passes on a stub. X defers unspecifiable criteria (W9/W12) instead of writing a vacuous one. Caps Y D2 at 3. |
| 3   | Linear chain dressed as graph | **Neither**                      | Both have multi-unit waves with independent tracks (X wave 0 = 4 units; Y waves 2/3 three-way by rig).                                                                                             |
| 4   | Waves inconsistent with deps  | **Neither**                      | Both re-derive cleanly; no bead scheduled at/before a dependency.                                                                                                                                  |
| 5   | Uniform top-tier routing      | **Neither**                      | Both use mid + cheap tiers on the bulk mechanical work.                                                                                                                                            |
| 6   | Invented beads / gold-plating | **Neither**                      | Every bead traces to epic text; both deliberately decline CSB fairness rather than invent it.                                                                                                      |
| 7   | Silent ambiguity resolution   | **Neither**                      | Both surface the .1–.11 vs live-children discrepancy and the lint/fairness sequencing softness as flagged assumptions.                                                                             |
| 8   | Gate theater                  | **Neither**                      | Both name the failure and downstream blast radius per gate.                                                                                                                                        |
| 9   | Missing negative verification | **Neither (X clean, Y partial)** | X has planted-defect + clean-fixture pairs throughout. Y has them on .1/.2/.3/.10 but not on .8/.11 — not pervasive enough to fire the global cap.                                                 |
| 10  | Size-class monoculture        | **Neither**                      | Both mix one-session / multi-session / needs-split.                                                                                                                                                |

---

## Verdict

**X 4.85 vs Y 3.50.** X is the clearly stronger decomposition, by ~1.35 weighted
points — roughly one full band.

**Specifically, what X does that Y does not:**

1. **Splits the linter tool from the remediation sweep into two dispatchable
   beads** (W8a build-tool / W8b sweep-to-zero). Y fuses both into `.8` with the
   split demoted to a size note, so Y has no standalone checker to validate — and
   therefore cannot place the "run the checker against known-bad input, confirm
   it isn't a vacuous 0/781" gate that X puts on W8a. This one structural choice
   propagates into both D1 and D5.
2. **Makes the escape hatch machine-verifiable** — W8b's exceptions list where
   "each entry carries a filed follow-up bead ID (verifier cross-checks each via
   `bd show`)," the rubric's ceiling anchor. Y has no equivalent verifiable
   escape hatch and ships one outright vacuous criterion (`.11` = "0 findings").
3. **Names the critical path and self-audits wave shape** ("wave 0 has 4 …
   wave 3 has 1"); Y provides waves but no critical path.
4. **Ties every pin to a gate/wave with a stated consequence** ("if wrong,
   class-A coverage for EB is void even though W3 'passes'"), against Y's shorter,
   looser-consequence list.

**Where Y is competitive:** routing (both ground tiers in blast radius and
actually use the cheap tier) and one underspec pin Y raises more sharply than X
(does the pre-tightening acceptance still bind after 2026-04-30). Neither
candidate places the highest-leverage gate the rubric calls out — protecting the
_quality_ of the adversarial calibration fixture that gates the upstream push
(anchor 2). That single omission is the main thing keeping X off a clean 5.

**Judge confidence:** high on the ranking and margin (the gap is multi-dimensional
— structure, testability, and gate design all favor X, not one borderline cap).
Medium on Y's exact D2 value: it rests on strict application of the vacuous-criterion
cap to the single `.11` bead; without that cap Y D2 is a 4 and Y's overall rises to
3.75, still well behind X and still the same verdict.
