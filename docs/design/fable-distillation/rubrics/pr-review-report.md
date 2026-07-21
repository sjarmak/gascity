# Judge Rubric — Maintainer PR Review Report

**Task type:** reviewing an external contributor's PR against a real read-only checkout and producing a maintainer decision report (APPROVE / REQUEST CHANGES / CLOSE) with evidence.

**Authored by:** Claude Fable 5, 2026-07-06, calibrated against the 2026-07-07 golden run (task-04, `outputs/task-04/fable.md`). This rubric is generic to the task type: it scores any maintainer review report on any repo and PR. Facts from the calibration PR (#4006) appear only inside quoted anchor examples.

**Judge setup:** you score a candidate report given (a) the candidate's text, (b) the task's frozen inputs (PR metadata + diff), and (c) the same pinned checkout the candidate had. Several checks below require you to open the repo; a judge who scores from the candidate's prose alone will systematically overrate confident fabrication. Every score you assign must cite either the candidate's own text or repo evidence you gathered.

---

## Dimensions

Weights in parentheses. Score each 1–5; 2 and 4 are earned by mixed evidence between the anchored levels.

### D1. Claim-vs-diff audit (weight 0.15)

Does the report check the PR's description, title, comments, and added doc-comments against what the diff actually does, and flag every gap?

- **5** — Systematically reconciles description against diff: confirms file counts and scope, verifies each factual claim in the PR body against the checkout (referenced commits exist and say what the author says they say; referenced mechanisms exist at the cited locations), and flags overstatements even when they favor the author's argument, including claims embedded in _newly added comments or docs_ (the easiest place for a false claim to hide, because it ships into the codebase). Distinguishes cosmetic looseness from substantive misdescription.
- **3** — Confirms the diff matches the description at the file/scope level and catches at least one real description-vs-code gap if present, but does not chase the PR body's factual claims into the checkout (takes "commit X introduced Y" or "mechanism Z covers this" on faith).
- **1** — Restates the PR description as fact. No independent reconciliation; the section titled "what it changes" is a paraphrase of the diff hunks or the author's own summary.

**Judge repo check:** pick the strongest factual claim in the PR body (a referenced issue/commit/mechanism) and verify it yourself; then check whether the candidate did.

### D2. Evidence-grounded correctness verification (weight 0.25)

Did the reviewer trace the changed code paths in the checkout and state concretely what breaks or what the fix provably repairs, with file:line evidence, rather than reasoning from the diff text alone?

- **5** — Traces the changed behavior end-to-end through the _unchanged_ surrounding code (callers, call sites, downstream consumers), with file:line citations that resolve in the checkout. Enumerates the input/deployment shapes the change can encounter and states the behavior of each, including the unchanged ones. Runs commands where a claim is checkable (targeted tests on baseline, greps proving absence of a mechanism) and reports concrete results. Negative claims ("no X exists on this path") are backed by a search, not asserted.
- **3** — Correct high-level analysis with some real file:line anchors, but the trace stops at the edited function; call sites and downstream consumers are asserted rather than followed, and no commands were run against the checkout. Case enumeration is partial (the happy path and the reported bug, nothing else).
- **1** — "The logic looks correct" reasoning from the diff hunk. Citations, if any, are to the diff itself, are wrong, or are decorative (file named, behavior not derived from it). No evidence the checkout was opened.

**Judge repo check:** resolve 3–5 of the candidate's file:line citations against the pinned checkout. A citation that does not contain what the candidate says it contains caps this dimension at 2 regardless of prose quality. If the candidate claims to have run a command, its claimed result must be reproducible.

### D3. Blast-radius coverage (weight 0.20)

Does the report surface impacts the author did not mention: other callers of the changed code, restored old gaps, interactions with adjacent mechanisms, operational/deployment behavior changes?

- **5** — Finds at least the material unmentioned impacts a strong maintainer would find: paths sharing the changed helper that behave differently post-change, safety mechanisms the author's argument leans on that do not actually cover all affected paths, and fleet/operational consequences for existing deployments. Each item is graded honestly (new regression vs restored pre-existing gap vs needs-a-sentence-in-the-PR-body) rather than uniformly alarmed.
- **3** — Names one real unmentioned impact but misses others of similar weight, or lists impacts without grading severity, so the decision section cannot use them.
- **1** — Blast radius section is empty, restates the author's own caveats, or lists hypotheticals untethered from the actual call graph ("this could affect other users of this function" with no function named).

**Judge repo check:** independently list the callers/consumers of the changed symbol(s) in the checkout and compare against the candidate's coverage. Missing a caller whose behavior changes is a concrete, checkable omission.

### D4. Test adequacy judgment (weight 0.15)

Does the report determine whether the shipped tests pin the bug — specifically, would they fail on unfixed main — and identify what the test suite still does not cover?

- **5** — For each new/flipped test, establishes RED-on-main status either by running it on the baseline or by tracing the test's assertion path to show what value main would produce. Checks that flipped tests encode the new contract rather than trivially passing. Identifies untested compositions the PR's own safety argument depends on, and distinguishes blocking gaps from nice-to-haves. Verifies protective existing tests survive unchanged.
- **3** — Correctly states the new test covers the bug, but "would it fail on main" is asserted, not established; test gaps are noted generically ("could use more edge cases") without naming the specific missing case or why it matters.
- **1** — "Tests were added" treated as adequacy. No engagement with what the tests assert or whether they could pass on broken code; or claims a test pins the bug when tracing shows it would pass on unfixed main.

**Judge repo check:** trace (or run, if the harness permits) the headline new test against unfixed main yourself. If the candidate's RED/GREEN claim is wrong, this dimension scores 1.

### D5. Decision quality and actionability (weight 0.15)

Is the verdict a single unambiguous decision, and can the contributor act on every change request without a follow-up question?

- **5** — One decision (APPROVE / REQUEST CHANGES / CLOSE), stated up front, with the blocking items enumerated and each traceable to evidence earlier in the report. Every change request names the file, the exact defect, and the acceptance shape (what a passing fix looks like: the test case's configuration and assertion, or the minimum acceptable rewording), and separates blocking from optional. Where a larger fix exists, the report says which one it is requiring.
- **3** — Clear decision, but at least one change request needs a follow-up question to act on ("improve the test coverage", "clarify the comment") or the blocking/optional boundary is fuzzy.
- **1** — Hedged or plural verdict ("approve, but maybe request changes"), decision unconnected to the findings, or change requests that are restated findings with no requested action.

**Judge check (text only):** for each change request, ask "could I implement this right now with only this sentence?" Any "no" caps at 3.

### D6. Calibration and restraint (weight 0.10)

Does the report know what it verified versus inferred, avoid gating on the unverifiable, and stay out of style noise?

- **5** — Explicit uncertainty section distinguishing verified (with the command/trace), inferred (with the reasoning), and unverifiable-from-here (with the exact step that would resolve it). Does not gate the decision on claims it cannot check (e.g., the author's production anecdote) when independent tracing supports the mechanism. No style/naming/formatting requests unless they hide a defect. Cosmetic issues in the PR are named as cosmetic and waved through.
- **3** — Has an uncertainty section but it is generic ("further testing recommended") or mixes verified and inferred claims in the body with uniform confidence; one or two low-value nitpicks appear among the change requests.
- **1** — Uniform confidence throughout; claims verification it did not perform ("I ran the suite" with no results, in a setting where it could not have); or change requests dominated by style preferences while a semantic gap goes unmentioned.

**Judge repo check:** cross-reference D2/D4 findings — any claim of performed verification the judge could not reproduce is a calibration failure here in addition to the evidence failure there.

---

## Failure signatures (weaker-model patterns and how to detect them)

For each, the detection method; items marked **[repo]** require the judge to open the checkout.

1. **Diff summarization posing as audit.** The "what it changes" section paraphrases hunks in diff order; no sentence exists that could be falsified by the checkout. _Detect:_ strike every sentence derivable from the diff text alone; if nothing load-bearing remains, D1 and D2 are at floor.
2. **Approving because the description sounds right.** The verdict echoes the author's rationale; correctness section cites the PR body ("as the author explains...") instead of code. _Detect:_ check whether any claim in the PR body was independently confirmed or refuted. **[repo]** Verify the body's strongest claim yourself; if it is wrong or overstated and the candidate repeated it, this signature is confirmed.
3. **Fabricated or decorative citations.** file:line references that don't resolve, point at unrelated code, or name real locations whose content the candidate never used. _Detect:_ **[repo]** resolve a sample of citations. Decorative citation (right file, claim not derivable from it) counts the same as wrong.
4. **Claimed verification without execution.** "The tests pass" / "I confirmed X fails on main" in a report showing no command output, especially where the setup (read-only checkout, diff-only artifact) makes the claim impossible. _Detect:_ map each verification claim to a runnable step; **[repo]** attempt reproduction of at least the RED-on-main claim.
5. **Style noise displacing the semantic gap.** Change requests about naming, comment phrasing, or formatting while the untested composition, the uncovered path, or the false claim in an added comment goes unflagged. _Detect:_ classify each change request as semantic/behavioral vs cosmetic; **[repo]** independently look for one material gap (callers of the changed symbol, coverage of the PR's own safety argument) and check whether the candidate found it.
6. **Hedged decision.** "Approve with comments, though request-changes would also be defensible", or a REQUEST CHANGES whose listed items are all marked optional. _Detect:_ text only; the decision and the blocking list must be mutually consistent.
7. **Uniform alarm (miscalibrated blast radius).** Every finding presented as blocking; restored pre-existing gaps reported as new regressions; hypothetical impacts with no code anchor. _Detect:_ check each blast-radius item for (a) a code anchor and (b) a severity grade; **[repo]** spot-check whether a "regression" actually predates the PR.
8. **Test-count adequacy.** "The PR includes tests for the change" as the whole of section 4. _Detect:_ absence of any assertion-level engagement; no statement about behavior on unfixed main.
9. **Uncertainty theater.** A closing uncertainty section listing only trivia while the body asserted unverified load-bearing claims with full confidence. _Detect:_ compare the confidence of body claims against what the uncertainty section admits; the load-bearing unverified items must be the ones listed.

---

## Anchor examples (5-level behavior, from the 2026-07-07 golden run)

1. > "Baseline actively verified: `go test ./internal/config/ -run TestPreferredDeterministicControlDispatcher` and `./internal/graphroute/ -run TestControlDispatcherBinding` pass on unfixed main (6 and 14 tests), confirming the tests the PR flips currently pass in city-preferring form."

   Why: verification claims are executed, with concrete counts a judge can reproduce; the report establishes the baseline the flipped tests are measured against instead of asserting it. (D2, D4)

2. > "`controlDispatcherTargetForExecutionTarget` (`internal/dispatch/control.go:1044`) calls the static helper directly; `grep -rn RuntimeMissing internal/dispatch/` returns nothing. The PR's replacement comment reads 'liveness of an asleep rig-local dispatcher is a downstream (#3454) concern' — but #3454's demotion lives only in `graphroute.ControlDispatcherBinding` and never runs at attempt time."

   Why: a negative claim (no coverage on this path) is proven with a search command, and the target is a false claim _inside a comment the PR adds_ — the audit extends to what the PR writes into the codebase, not just what the body says. (D1, D3)

3. > "This is a _restored pre-#3765 gap_, not a new regression, and demand-spawn/reconciler repair may eventually cover it — but the comment as written claims protection that isn't there."

   Why: severity is graded honestly against history rather than uniformly alarmed, and the graded finding still converts into a precise blocking item. (D3, D6)

4. > "In `internal/graphroute/graphroute_test.go`, a `ControlDispatcherBinding` case with BOTH deterministic dispatchers configured (city `Dir=""` + rig `Dir="fixture"`, both with `ControlDispatcherStartCommandFor` start commands), `rigContext="fixture"`, and `ControlDispatcherRuntimeMissing` returning true for `fixture/core.control-dispatcher`: assert the binding demotes to `core.control-dispatcher` with `ControlFallbackFrom == "fixture/core.control-dispatcher"`."

   Why: the change request specifies file, fixture configuration, and exact assertion — the contributor can write the test from this sentence with zero follow-up questions. (D5)

5. > "**Post-PR suite is unverified by me.** The checkout is read-only and the artifact is a diff, so I could not apply and run it. Before merge I would apply the diff in a scratch worktree and run `go test ./internal/config/... ./internal/graphroute/... ./internal/dispatch/...`, then revert the `config.go` hunk alone and confirm the new instantiation test goes RED."

   Why: the load-bearing unverified item leads the uncertainty section, with the exact resolving procedure — calibration that admits the real limit instead of listing trivia. (D6)

---

## Scoring procedure

1. **Read the candidate report once end-to-end**, then perform the **[repo]** checks: resolve a sample of 3–5 citations (D2), verify the PR body's strongest claim (D1), list callers of the changed symbols (D3), and establish the headline test's behavior on unfixed main (D4). Record what you found; these observations are citable evidence.
2. **Score each dimension 1–5** using the anchors. For every score, cite the candidate's own text (quoted) or your repo evidence. A score with no citation is invalid.
3. **Apply hard caps** where triggered: non-resolving or decorative citations cap D2 at 2; a wrong RED/GREEN claim sets D4 to 1; a claimed-but-impossible verification sets D6 to at most 2.
4. **Overall score** = Σ(weight × dimension score), weights: D1 0.15, D2 0.25, D3 0.20, D4 0.15, D5 0.15, D6 0.10. Report to one decimal.
5. **Verdict bands:** ≥4.5 golden-equivalent; 3.5–4.4 acceptable maintainer output; 2.5–3.4 needs the listed deficiencies fixed before it could be trusted for a merge decision; <2.5 not a review, regardless of surface polish.
6. **Decision-agreement note:** separately record whether the candidate's APPROVE/REQUEST CHANGES/CLOSE matches the decision the evidence supports (use the golden report's decision when scoring the calibration task; otherwise your own repo-grounded judgment). A right decision reached without evidence does not rescue low D2/D4 scores; a defensibly argued different decision is not automatically penalized.
7. **Judge confidence** (high / medium / low) with one line of reasoning: high only if you completed all four **[repo]** checks; medium if the repo was partially checkable; low if you scored from text alone — and in that case say so explicitly, and treat any score above 4 on D2/D3/D4 as unavailable.

## Report format for judges

```
D1 claim-vs-diff:      X/5 — <citation + one line>
D2 verification:       X/5 — <citation + one line>
D3 blast radius:       X/5 — <citation + one line>
D4 test adequacy:      X/5 — <citation + one line>
D5 decision/action:    X/5 — <citation + one line>
D6 calibration:        X/5 — <citation + one line>
Failure signatures observed: <numbers from the list, or "none">
Overall (weighted):    X.X — <band>
Decision agreement:    <match / defensible-divergence / wrong>
Judge confidence:      <high|medium|low> — <which repo checks were performed>
```
