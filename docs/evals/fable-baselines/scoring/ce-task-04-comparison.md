# CE Task-04 comparison — PR #4006 maintainer review (blind X vs Y)

Judge: Claude (Opus 4.8), 2026-07-07. Rubric: `rubrics/pr-review-report.md`.
Inputs: `inputs/pr-4006-meta.json`, `inputs/pr-4006.diff`. Repo: read-only pinned
checkout `/tmp/fable-baseline-task04` @ `ee616a7e4` (verified).

Both candidates decide **REQUEST CHANGES**, which matches the evidence, and both
correctly locate the central unmentioned finding: the attempt-time re-route path
(`dispatch.controlDispatcherTargetForExecutionTarget`) has no `#3454`
runtime-missing demotion, while the graph.v2 instantiation path
(`graphroute.ControlDispatcherBinding`) does. The scoring separation is entirely
in how each treats that finding — its history, its test coverage, and its
remediation.

---

## Repo checks performed (all four)

1. **Callers of the changed symbol.** `grep -rn PreferredDeterministicControlDispatcher --include=*.go` (non-test) → exactly `config/config.go` (def), `dispatch/control.go:1058`, `graphroute/graphroute.go:331`. Both non-test call sites are the two the PR touches; no third consumer. `applyAttemptControlStepRoute` callers resolve at `control.go:500` and `fanout.go:318`. **Both candidates got this right.**
2. **Strongest PR-body claim.** `git show 41785d976` confirms #3765 exists, dated 2026-06-26, titled exactly as the body says, and its diff introduced the city-preference. `grep -rn RuntimeMissing internal/dispatch/` returns nothing (exit 1) — the attempt-time path has no liveness check. **Both candidates verified both; Y ran `git show` explicitly, X confirmed via `git log`.**
3. **Only behavioral change is `config.go`.** The diff's `control.go` and `graphroute.go` hunks are comment-only; the sole logic change is the loop inversion in `PreferredDeterministicControlDispatcher`. X implicitly relies on this (reverts only `config.go`); Y states the two comment-paths explicitly.
4. **Headline test on unfixed main.** `go test ./internal/config/ -run TestPreferredDeterministicControlDispatcher -count=1` → `ok` on baseline (city-preferring form). Because the sole behavioral change is `config.go`, flipping the four assertions to rig-preferring is genuinely RED-on-main. **Both candidates' RED claims are corroborated.**
5. **Y's sharpest finding, spot-verified.** `dispatcherFallbackCfg()` (graphroute_test.go:976) agents are `{Name:"control-dispatcher"}` and `{Name:"control-dispatcher", Dir:"gc-contrib"}` — **neither carries a StartCommand**, so both fail `IsDeterministicControlDispatcher` (config.go:87-92, requires `"convoy control --serve"`). The existing `#3454` demotion tests (`FallsBackToCityWhenRigRuntimeMissing` et al., graphroute_test.go:983-1044) therefore drive the **resolver** fallback (graphroute.go:337), never the deterministic-preference path this PR inverts. **Confirmed exactly as Y states.**
6. **Y's restored-gap claim, spot-verified.** `git show 41785d976^:internal/dispatch/control.go` shows the pre-#3765 attempt-time function already matched `Dir == rigContext` directly with **no liveness gate**. #3765 replaced that with the city-preference, incidentally masking the gap; this PR re-exposes it. **Confirmed — it is a restored pre-existing gap, not a new regression.**

Citations resolved for both candidates (X: control.go:1010/1058, graphroute.go:290-311/331, cmd/API #3454 wiring; Y: config.go:95-104/86-92, graphroute.go:296-302/337, graphroute_test.go:781/976/983-1044). All resolve and contain what each says.

---

## Candidate X

```
D1 claim-vs-diff:      4/5 — verifies 41785d976 in git log, confirms both call sites are the only two, checks the "why" narrative; but frames the false-comment issue downstream in §3 rather than foregrounding it as an added-code audit finding, and confirms scope without the arithmetic file/line reconciliation.
D2 verification:       5/5 — "I then hand-applied the full diff... and ran the actual Go toolchain... 2106 passed, 0 failed... reverted only the source change in config.go... 2100 passed, 6 failed" with matching symptom strings; plus traces #3454's real wiring at cmd_sling.go:583, handler_sling.go:112, sling.go:143-177, engdocs. Execution + end-to-end trace; baseline-green reproduced, nothing contradicts the counts.
D3 blast radius:       4/5 — finds the attempt-time gap and grades the secondary "every rig with a config entry" note as lower-severity, but mis-grades the primary: "the exact stranding failure mode this PR exists to fix, just reintroduced" / "a real, unaddressed regression risk" — it is a restored pre-#3765 gap, not a new regression (repo check 6).
D4 test adequacy:      4/5 — establishes RED-on-main by execution (the D4-5 gold behavior) and names the untested attempt-time runtime-missing case; but misses that the existing graphroute demotion tests never exercise the deterministic path (repo check 5) and does not explicitly verify a protective test survives.
D5 decision/action:    4/5 — unambiguous REQUEST CHANGES, each request has file + defect + acceptance shape; but blocking item #1 mandates wiring the full demotion into the attempt-time path — a larger fix the evidence grades as a restored gap, over-scoping beyond the golden's minimal remediation.
D6 calibration:        4/5 — clean uncertainty section, treats the live anecdote as the author's account (does not gate on it), execution-backed claims; docked for the new-vs-restored mis-grade and the larger-fix mandate (partial uniform-alarm).
Failure signatures observed: 7 (partial) — restored pre-existing gap reported as a new regression, driving a larger-than-necessary blocking fix.
Overall (weighted):    4.25 — acceptable maintainer output.
Decision agreement:    match (REQUEST CHANGES).
```

X's distinctive strength is real execution: it copied the diff to a writable
scratch and ran `go build` + the three-package `go test`, then reverted only
`config.go` to show the four assertions go RED with the exact symptom string the
diff's own test asserts. That is the anchor-1 behavior the rubric prizes, and it
is the one thing X does that Y does not. Residual: I reproduced baseline-green
and the symptom strings but not the exact `2106`/`2100+6` counts (medium
confidence on the counts specifically; everything checkable is consistent).

## Candidate Y

```
D1 claim-vs-diff:      5/5 — arithmetic file/line reconciliation ("sums to 121/55"), git-show verification that 41785d976 matches the body's description of it, and — the D1-5 centerpiece — makes the false claim in the *newly added* dispatch/control.go comment ("liveness... is a downstream (#3454) concern") the blocking audit finding, distinguishing it from the accurate graphroute comment and the lower-severity shared-helper comment.
D2 verification:       4/5 — traces every deployment shape end-to-end through both paths including the runtime-missing composition, backs the negative claim with grep, verifies IsDeterministicControlDispatcher's StartCommand gate; RED-on-main established by trace (one traced result matches the PR's own reported failure string). Declined to run the test suite, which was within read-only scope — the one D2-5 command it left on the table.
D3 blast radius:       5/5 — grades each item honestly: "NEW REGRESSION — none found," "RESTORED PRE-EXISTING GAP" (proven with `git show 41785d976^`), "CALLERS FULLY ACCOUNTED FOR" (grep), "NEEDS A SENTENCE IN THE PR BODY" (demand-spawn impact, flagged as inference). Matches golden anchor #3 in substance.
D4 test adequacy:      5/5 — establishes RED for all four assertions by trace, verifies the protective TestControlDispatcherBinding_CityOnlyBoundDispatcher survives, and finds the blocking coverage hole: the PR's safety argument has zero test coverage for the deterministic-preference shape because the existing demotion tests use non-deterministic fixtures (repo check 5). Change request #2 specifies the exact missing test.
D5 decision/action:    5/5 — single REQUEST_CHANGES; blocking = fix the false comment (minimal reword) + add the composed regression test (both deterministic dispatchers, runtime-missing on rig, assert demotion with ControlFallbackFrom) — the latter essentially the golden's D5 anchor; explicitly scopes the larger demotion fix OUT; clean optional tier.
D6 calibration:        5/5 — Verified / Inferred / Unverifiable ledger with a resolving command per item, admits the un-executed test as "confident in the trace, not in an executed proof," refuses to gate on the live anecdote per the stated rule, flags the missing CI/reviews metadata.
Failure signatures observed: none.
Overall (weighted):    4.75 — golden-equivalent.
Decision agreement:    match (REQUEST CHANGES).
```

---

## Verdict

**Y is better, by ~0.5 (4.75 golden-equivalent vs 4.25 acceptable).** Both nail
the central finding and the correct decision; the gap is calibration and depth.

What Y does that X does not:

1. **Grades the gap's history correctly.** Y proves with `git show 41785d976^`
   that the attempt-time path never had a liveness gate, so this is a _restored
   pre-existing gap_, and requires only that the false comment stop asserting
   coverage. X calls it a reintroduced _regression_ and makes wiring the full
   demotion into the attempt-time path a blocking requirement — a larger fix the
   evidence does not support.
2. **Finds the deterministic-path test-coverage hole.** Y shows the existing
   `#3454` demotion tests use non-deterministic fixtures (`dispatcherFallbackCfg`,
   no StartCommand) and so never exercise the path this PR changes, leaving the
   PR's central safety claim untested; its change request #2 specifies the exact
   composed test. X misses this and does not verify a protective test survives.
3. **Foregrounds the false comment in newly-added code** as the primary blocking
   defect with minimal correct remediation, and keeps a cleaner
   Verified/Inferred/Unverifiable ledger.

What X does that Y does not: **actually executes the test suite** (copy-out +
`go build`/`go test`, revert-config RED demonstration) and traces `#3454`'s
wiring across the cmd/API layers — the anchor-1 executed-verification behavior Y
chose to establish by trace instead. This is X's one clear edge and is why its
D2 outscores Y's; it is not enough to offset Y's stronger D1/D3/D4/D5/D6.

**Judge confidence: high** — all four repo checks completed (callers listed,
strongest body claim verified via git, headline test run on baseline, plus
spot-verification of both candidates' load-bearing citations and Y's two
distinctive findings).
