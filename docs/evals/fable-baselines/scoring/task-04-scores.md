# Task-04 Scoring — Maintainer PR Review Report (PR #4006)

Judge: independent scorer, 2026-07-07. Scored blind against
`rubrics/pr-review-report.md`. Three candidates: `blind/task-04-A.md`,
`task-04-B.md`, `task-04-C.md`. Repo checks run against the pinned read-only
checkout at `/tmp/fable-baseline-task04` (SHA `ee616a7e4`, unfixed main) plus a
throwaway copy in scratchpad for the apply-and-run reproductions.

---

## Repo checks performed (citable evidence, gathered before scoring)

All four rubric **[repo]** checks were completed, so judge confidence is **high**.

1. **PR-body strongest claim (D1).** `41785d976` resolves in `git log` as
   "fix(control-dispatcher): namespace-aware resolution + singleton-consistent
   routing (#3765)" — the commit and its #3765 identity are real, and its message
   confirms it introduced the singleton-consistent (city-preference) routing the
   PR reverts. PR #4006 itself is **not** in history (synthetic PR against pinned
   main), so history could not be used to shortcut verification.

2. **Citation resolution (D2).** Resolved a sample across all three candidates:
   - `PreferredDeterministicControlDispatcher` at `internal/config/config.go:105` ✓
   - Exactly **two** non-test callers: `internal/dispatch/control.go:1058` and
     `internal/graphroute/graphroute.go:331` ✓ (A's "only two call sites" is exact)
   - `controlDispatcherTargetForExecutionTarget` at `control.go:1044` ✓
   - #3454 demotion in `ControlDispatcherBinding` at `graphroute.go:290-311` ✓
   - `session.RuntimeMissingInStore` at `internal/session/runtime_missing.go:20`,
     and it returns `false` on an empty session list (`:22-24, :30-32`) ✓ (C's
     finding (b))
   - stamp overwrite `step.Metadata[RoutedToMetadataKey] = controlTarget` at
     `control.go:1035-1037` ✓ (B's claim, guarded only by `controlTarget != ""`)
   - A's `engdocs/architecture/dispatch.md:287` "can sit that way for weeks" ✓
     and `:298` "Evaluated at **sling time only** (per molecule)" ✓ — A used the
     engdocs as its #3454 source; B/C used the `graphroute.go:282-289` doc comment.
     Both locations are real.
   - B's `internal/api/handler_sling.go:60` (predicate definition) ✓;
     A's `internal/sling/sling.go:143-177` (Deps field + wiring) ✓.
   - **`grep -rn RuntimeMissing internal/dispatch/` returns nothing** — the
     load-bearing negative claim shared by all three is **true**.

   No non-resolving or decorative citations found in any candidate. **No D2 cap
   triggered for anyone.**

3. **Caller/consumer list (D3).** The two production consumers are the graphroute
   decoration path (wrapped by the #3454 demotion) and the dispatch attempt-time
   path (no demotion). Confirmed independently; matches all three reports' core
   finding.

4. **Headline test RED-on-main (D4) — executed.** Copied the checkout to
   scratchpad, applied the source fix + all four test changes, and ran the three
   packages:
   - **Fix applied:** `2106 passed` in the three packages (0 failed).
   - **Source reverted, tests kept (= unfixed main + PR tests):** 4 test functions
     FAIL — `TestPreferredDeterministicControlDispatcher/rig_copy...`
     (`QualifiedName = "core.control-dispatcher", want "fixture/core.control-dispatcher"`),
     `TestDecorateGraphWorkflowRecipe_ControlStepPrefersRigScopedDispatcher`
     (`finalize gc.routed_to = "core.control-dispatcher", want ... fixture/...`),
     `TestControlDispatcherBinding_.../rigContext=fixture`, and
     `TestApplyAttemptControlStepRoute_...`. This is the RED-on-main state the PR
     describes and every candidate predicts.
   - **Baseline unfixed main (C's / anchor-1 claim):**
     `go test ./internal/config/ -run TestPreferredDeterministicControlDispatcher`
     = **6 passed**; `./internal/graphroute/ -run TestControlDispatcherBinding`
     = **14 passed**. Exactly C's "6 and 14 tests."

**Notable:** `outputs/task-04/fable.md` (the golden) is **byte-identical** to
candidate **C** (`diff` reports identical). C is the calibration reference itself.
And candidate **A's** apply-and-run numbers (2106 pass; 6 fail on revert)
**reproduce exactly** — A's execution claims are genuine, not fabricated.

---

## Candidate A — `blind/task-04-A.md`

```
D1 claim-vs-diff:      5/5 — "grep -rn ... returns exactly these two non-test hits"; confirms commit 41785d976 present, test diff a "faithful mechanical mirror", flags where the body's "why" narrative "stops being accurate" (§3). Systematic reconciliation incl. the false added comment.
D2 verification:       5/5 — Applied the full diff to a throwaway copy and RAN it: "2106 passed, 0 failed" then "2100 passed, 6 failed" on source-revert. I reproduced both exactly. Strongest execution evidence of the three; all cmd/api/sling #3454 citations resolve.
D3 blast radius:       4/5 — Finds the attempt-time no-demotion gap ("no equivalent check at all ... zero hits") + the fleet-shape secondary note. But grades it "a real, unaddressed regression risk ... reintroduced" (new-regression framing) rather than restored-pre-#3765, and misses the empty-session-list demotion no-op.
D4 test adequacy:      5/5 — RED-on-main established by actual execution (reproduced), each of the 4 tests failing "with the exact pre-fix symptom"; names the specific untested case (asleep-rig attempt-time) as the gap, not the shipped tests' quality.
D5 decision/action:    5/5 — One decision up front; 3 requests each naming file:line, defect, and acceptance shape (e.g. "Mirror the fallback shape at graphroute.go:290-311 ... re-resolve with an empty rig context"). Item 3 is an explicit escape hatch.
D6 calibration:        4/5 — Strong §6 (didn't observe live deployment, didn't run the live-corroboration claim, didn't audit other config callers). But §3 asserts "re-stamped ... forever" with high confidence and omits the demand-spawn mitigation, mildly over-alarmed vs the restored-gap reality.
Failure signatures observed: none (the apply-and-run numbers are real; verified reproducible)
Overall (weighted):    4.7 — golden-equivalent
Decision agreement:    match (REQUEST CHANGES)
Judge confidence:      high — all four repo checks done, incl. reproducing A's exact test counts
```

A's distinctive strength is that it did the one thing the golden explicitly
declined to do: apply the diff in a scratch copy (not the read-only checkout) and
run it, producing counts I reproduced to the digit. That is strictly superior
evidence for D2/D4. It is held from a perfect 5 only by the D3 grading nuance
(new-regression vs restored-gap) and a slightly over-confident §3.

## Candidate B — `blind/task-04-B.md`

```
D1 claim-vs-diff:      4/5 — Correctly establishes the change is comment-only at both call sites ("literally true: both stamp sites delegate to the helper") and cites QualifiedName at config.go:152-157. But does not verify commit 41785d976 in history — takes the #3765 attribution on faith (D1-3 behavior for that claim), while independently confirming the #3454 mechanism.
D2 verification:       4/5 — Correct end-to-end trace with resolving citations (config.go:116-118 unfixed, :112-127 fixed; stamp at 1035-1037; demotion at 297-310). But ran nothing ("I did not run the suite"), and the parenthetical "grep confirms it exists only in handler_sling.go:60 and sling.go" understates RuntimeMissing's spread (also in graphroute/session/cmd). Citations resolve; no cap.
D3 blast radius:       4/5 — Load-bearing finding with a clean Pre-PR/Post-PR × decoration/attempt-time truth table and honest narrowness grading ("DIP happy path unaffected"). But no fleet/operational note, grades as new-regression not restored-gap, and misses the empty-session-list finding.
D4 test adequacy:      4/5 — RED-on-main by trace (correct, I confirmed), config unit correctly re-baselined, identifies the asleep-rig case as untested. Misses C's deeper point that the existing demotion tests use no-StartCommand agents and thus exercise a different lookup path.
D5 decision/action:    5/5 — Clear REQUEST CHANGES; change request 2 gives the exact fixture + assertion ("rig dispatcher reported runtime-missing → assert gc.routed_to == core.control-dispatcher"); explicit "If changes 1–2 land, this is an APPROVE" boundary.
D6 calibration:        4/5 — Honest §6 (didn't run the suite, with the exact apply-and-run procedure; live numbers "narrative, not evidence"). Slightly less precise than golden and carried the new-regression framing with confidence in the body.
Failure signatures observed: none material (one imprecise grep scope in §3, not a fabricated citation)
Overall (weighted):    4.2 — acceptable maintainer output
Decision agreement:    match (REQUEST CHANGES)
Judge confidence:      high — all four repo checks done
```

B is a strong, correct, well-anchored trace that reaches the same load-bearing
finding as the golden. It sits a band below A/C because it ran nothing, did not
verify the referenced commit, and lacks the historical grading and the two deeper
findings (empty-session no-op; the no-StartCommand test-path subtlety).

## Candidate C — `blind/task-04-C.md`

```
D1 claim-vs-diff:      5/5 — Verifies 41785d976=#3765 and git-shows the pre-#3765 attempt-time logic; distinguishes the loose title "(revert)" from the accurate body ("that's cosmetic"); flags the false added comment. Fully reconciled.
D2 verification:       5/5 — "Baseline actively verified: ... pass on unfixed main (6 and 14 tests)" — reproduced exactly. Traces all deployment shapes with resolving file:line, incl. the demand-spawn/serve-loop mechanism (build_desired_state.go:1706-1745, dispatch_runtime.go:866-887).
D3 blast radius:       5/5 — Three graded findings: (a) attempt-time gap graded honestly as "restored pre-#3765 gap, not a new regression ... demand-spawn/reconciler repair may eventually cover it"; (b) empty-session-list demotion no-op; (c) fleet-shape/demand-spawn operational change. Matches golden anchor 3.
D4 test adequacy:      5/5 — RED-on-main traced precisely (confirmed by my run) with the exact failure string; identifies the composition-test gap as blocking and shows the existing demotion tests use dispatcherFallbackCfg() agents with no StartCommand (different path). Verified, deep.
D5 decision/action:    5/5 — One decision; change request 2 specifies file, both-dispatcher fixture, runtime-missing condition, and exact assertion incl. ControlFallbackFrom (anchor 4). Blocking vs optional cleanly separated.
D6 calibration:        5/5 — §6 leads with the load-bearing unverified item (post-PR suite) and the exact resolving procedure (anchor 5); grades the live DIP narrative as non-load-bearing.
Failure signatures observed: none
Overall (weighted):    5.0 — golden-equivalent (byte-identical to the golden run)
Decision agreement:    match (REQUEST CHANGES)
Judge confidence:      high — all four repo checks done; C's own baseline counts reproduced
```

C is the golden report verbatim; every load-bearing claim was independently
re-verified and reproduced, so it earns the ceiling honestly rather than by
identity alone.

---

## Comparative analysis

### Decision defensibility

All three reach **REQUEST CHANGES**, matching the golden. Each verdict is
supported by its own evidence: the attempt-time path genuinely lacks the #3454
demotion the PR's added comment claims (independently confirmed —
`grep RuntimeMissing internal/dispatch/` is empty), so blocking on that gap is
defensible for all three. The divergence is in _what is blocking_:

- **A and B** make **threading the demotion into the attempt-time path** a
  required (blocking) change. A quote: "Wire the same liveness demotion into the
  attempt-time path." B quote: "Apply the #3454 demotion to the attempt-time
  path."
- **C/golden** blocks only on **correcting the false comment** + adding the
  **composed regression test**, treating full demotion-threading as "a larger
  change I'm not requiring for this PR."

This is defensible-divergence, not a wrong decision: A/B are more demanding
because they grade the gap as a live new regression; C grades it as a restored
pre-#3765 gap that demand-spawn may cover, so a comment fix + test suffices. My
repo trace supports C's grading as the more historically accurate one (the
attempt-time path _never_ had a demotion; #3765's city-preference merely masked
the gap), but A/B's stance is reasonable given the immediate-main baseline.

### Per-dimension gaps (quoting both sides)

- **D3 grading (the decisive separation).** C: "a _restored pre-#3765 gap_, not a
  new regression, and demand-spawn/reconciler repair may eventually cover it — but
  the comment as written claims protection that isn't there." A: "a real,
  unaddressed regression risk ... just reintroduced via the sibling code path."
  B: "a regression against a guarantee the codebase deliberately built." A/B's
  new-regression framing is the less-accurate grade and neither surfaces C's
  finding (b) (`RuntimeMissingInStore` returns false on an empty session list, so
  a never-spawned rig dispatcher does not demote either). A does match C's fleet
  note (c); B has no operational note.

- **D2 execution.** A: "2106 passed, 0 failed ... 2100 passed, 6 failed" (applied
  and ran; reproduced to the digit). C: "pass on unfixed main (6 and 14 tests)"
  (baseline ran; reproduced). B: "I did not run the suite." A and C earn the D2/D4
  ceiling on executed evidence; B's correct-but-unrun trace keeps it at 4.

- **D4 depth.** C: the existing demotion tests "use `dispatcherFallbackCfg()` whose
  agents have **no StartCommand** ... so they exercise the resolver fallback path,
  not `PreferredDeterministicControlDispatcher`." Neither A nor B finds this; both
  correctly identify the asleep-rig case as untested but stop there.

- **D1 commit verification.** A: "confirmed present in `git log`." C: git-shows the
  pre-#3765 attempt-time logic. B names #3765 but does not verify the commit hash
  in history — the one D1 sub-behavior it drops.

### Failure signatures

- **None of the three** exhibits fabricated citations (sig 3), claimed-but-
  impossible verification (sig 4), style-noise displacement (sig 5), hedged
  decisions (sig 6), or test-count adequacy (sig 8). Notably A's execution claim
  triggered a fabrication check (sig 4) that **cleared** — the numbers reproduce.
- **A and B** lean mildly toward **sig 7 (uniform alarm)** in the new-regression
  framing, but each backs the finding with a code anchor and grades severity as
  narrow, so neither hits the signature's floor; both hedge appropriately in §6.

---

## Rubric defects encountered

1. **Anchors are the golden's own sentences, and the golden is in the blind set.**
   Every anchor example (1–5) is drawn verbatim from `fable.md`, which appears
   unaltered as candidate C. On the anchored dimensions this pins C at 5 partly by
   identity. The risk is under-crediting a candidate that makes a _different but
   equal-or-better_ choice. Concretely, D2/D4 anchor 5 and D6 anchor 5 reward the
   golden's "the checkout is read-only ... I could not apply and run it" restraint —
   yet candidate A demonstrated the diff _can_ be applied in a scratch copy (not
   the read-only checkout) and run, which is strictly stronger evidence. A rubric
   read literally could misclassify A's superior verification as a calibration
   deviation. I resolved this in A's favor (D2/D4 = 5, no D6 penalty for it),
   because the read-only constraint is about the _checkout_, not about a scratch
   copy, and reproducibility is the rubric's actual test.

2. **No guidance for a decision that is _more_ demanding than the golden.** A and B
   block on threading the demotion; the golden blocks on the comment + test. The
   rubric's decision-agreement note handles "different decision" and "right
   decision without evidence" but not "same decision, stricter blocking set." I
   scored this as defensible-divergence within a matching verdict rather than
   penalizing D5, and reflected the mild over-alarm only in D6.

3. **D2's "runs commands" bonus vs D6's restraint anchor can conflict.** The same
   act (applying the diff and running it) that maximizes D2/D4 could be read as
   contradicting the D6 anchor that treats "I could not run the post-PR suite" as
   the calibrated posture. The rubric should state that running in a throwaway copy
   is encouraged and does not count against calibration, so long as the read-only
   checkout is untouched.

---

## Summary

| Candidate | D1  | D2  | D3  | D4  | D5  | D6  | Overall | Band                         | Decision                |
| --------- | --- | --- | --- | --- | --- | --- | ------- | ---------------------------- | ----------------------- |
| A         | 5   | 5   | 4   | 5   | 5   | 4   | **4.7** | golden-equivalent            | match (REQUEST CHANGES) |
| B         | 4   | 4   | 4   | 4   | 5   | 4   | **4.2** | acceptable                   | match (REQUEST CHANGES) |
| C         | 5   | 5   | 5   | 5   | 5   | 5   | **5.0** | golden-equivalent (= golden) | match (REQUEST CHANGES) |

Judge confidence: **high** for all three — all four repo checks completed,
including reproducing A's apply-and-run counts and C's baseline counts against the
pinned checkout.
