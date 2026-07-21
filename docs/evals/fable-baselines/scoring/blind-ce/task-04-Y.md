Gas City review — PR #4006: fix(control-dispatcher): prefer rig-scoped dispatcher for its own scope (revert #3765's static city-preference)
Author: csauer02-personal-user (Chris Sauer) — external contributor
Files: internal/config/config.go, internal/config/config_test.go, internal/dispatch/control.go, internal/dispatch/control_integration_test.go, internal/graphroute/graphroute.go, internal/graphroute/graphroute_test.go (+121/-55)
Linked issues: none closed via keyword; refs #3765, #3454 (both verified to exist — see below)
CI / existing reviews: not present in the provided metadata JSON (no `statusCheckRollup` or `reviews` field was supplied) — unverifiable from here, flagged in Uncertainty.

Decision: REQUEST_CHANGES

## 1. Claim-vs-diff audit (Phase 4)

Scope reconciliation is clean: the 6 files and the +121/-55 line counts in the metadata match the diff exactly (config.go +20/-17, config_test.go +8/-7, control.go +5/-5, control_integration_test.go +10/-9, graphroute.go +7/-4, graphroute_test.go +71/-13 — sums to 121/55). No undisclosed touched files.

Referenced commit `41785d976` (#3765, "fix(control-dispatcher): namespace-aware resolution + singleton-consistent routing") exists on this checkout's history and its diff matches the PR body's description of it: it introduced `PreferredDeterministicControlDispatcher` and made it hard-prefer `Dir == ""` (city singleton) for every scope, exactly as described. Verified with `git show 41785d976`.

**Blocking defect — a newly added comment makes a claim that is false for the code path it's attached to.** The PR adds this comment to `internal/dispatch/control.go` (in `controlDispatcherTargetForExecutionTarget`, ~line 1052 post-PR):

> "...liveness of an asleep rig-local dispatcher is a downstream (#3454) concern, not conflated into this static ownership selection."

This claims the #3454 `ControlDispatcherRuntimeMissing` demotion covers this function's callers. It does not. `grep -rn "RuntimeMissing" internal/dispatch/` returns nothing — the mechanism exists only in `internal/graphroute/graphroute.go` (`ControlDispatcherBinding`, lines 290-311), `internal/sling/`, and `internal/api/handler_sling.go`. `controlDispatcherTargetForExecutionTarget` (dispatch/control.go:1044) calls `config.PreferredDeterministicControlDispatcher` directly with zero wrapping liveness check, and its caller `applyAttemptControlStepRoute` is invoked from `internal/dispatch/control.go:500` and `internal/dispatch/fanout.go:318` — the attempt-time control re-route path has no #3454 protection at all, before or after this PR. The near-identical comment added to `internal/graphroute/graphroute.go` (~line 328, inside `resolveControlDispatcherBinding`) makes the same claim and _is_ accurate there, because that function's exported wrapper `ControlDispatcherBinding` performs the demotion check (graphroute.go:296-302) before returning. The doc comment on the shared helper itself, `config.PreferredDeterministicControlDispatcher` (config.go:95-104), also asserts the downstream-demotion story without qualifying that only one of its two call sites has it — same root inaccuracy, lower severity since it's on the shared function rather than asserting coverage for a specific unprotected path.

This is exactly the kind of claim the audit is meant to catch: it ships into the codebase as documentation future maintainers will trust, and it is wrong for the path it's attached to.

Everything else in the body — file-level mechanics, the `QualifiedName()` prefixing behavior, both call-sites delegating to the one helper — checks out against the code.

## 2. Correctness (Phase 5)

Traced `PreferredDeterministicControlDispatcher` (config.go:105-128, baseline) against the diff across every deployment shape the helper can encounter:

- **city-only, any rigContext** (original #3764 shape): unaffected. New code only takes the rig-scoped branch when `rigContext != "" && dir == rigContext`; with no rig-scoped agent configured, the loop always falls through to collecting the city-scoped entry. Confirmed by reading the untouched (not in this diff) protective test `TestControlDispatcherBinding_CityOnlyBoundDispatcher` (graphroute_test.go:781) and tracing it against the new logic by hand — still resolves to `core.control-dispatcher` for both `rigContext=""` and `rigContext="fixture"`.
- **rig-with-own, matching rigContext** (the DIP bug): now resolves to the rig-scoped instance (`dir == rigContext` matches and returns immediately, before the city entry is ever reached). This is the intended fix and is correct — the rig-scoped agent's `QualifiedName()` (`Dir+"/"+Name` per config.go's `QualifiedName`) is the route the rig-local dispatcher session actually claims.
- **pure singleton, `rigContext==""`**: unaffected — the `rigContext != ""` guard on the rig branch means empty scope always falls to the city-scoped collection, same as baseline.
- **rig-with-own, non-matching rigContext** (multi-rig, request for a different rig than the one configured): correctly falls through to city, since only the matching `dir==rigContext` triggers early return.
- **rig-scoped dispatcher configured but asleep (runtime-missing)** — the case the PR's own safety argument leans on:
  - Instantiation path (`graphroute.ControlDispatcherBinding` → `DecorateGraphWorkflowRecipeWithDefaultBinding` at graphroute.go:555): traced correctly. `resolveControlDispatcherBinding` now returns the rig-scoped binding; the wrapper then calls `deps.ControlDispatcherRuntimeMissing(binding.QualifiedName)` and, if true, re-resolves with `rigContext=""`, which — per the trace above — now correctly falls to the city-scoped agent regardless of which path (deterministic-lookup or resolver-fallback) produced the original binding. No bug found in this composition by code reading; see Test Adequacy below for why it is nonetheless unverified.
  - Attempt-time path (`dispatch.controlDispatcherTargetForExecutionTarget`): **no demotion at all**, confirmed above. If a rig's own dispatcher is configured but dead, attempt-time control re-route will now stamp the (dead) rig-scoped route unconditionally.

**RED-on-main, traced (not executed — see Uncertainty):**

- `config_test.go` flip ("singleton preferred..." → "rig copy preferred..."): with baseline logic, agents `[rigCopy(Dir="fixture"), citySingleton(Dir="")]`, `rigContext="fixture"` — the loop hits `rigCopy` first (records it as the fallback), then reaches `citySingleton` and returns it immediately since `Dir==""`. Baseline result: `core.control-dispatcher`. The new assertion wants `fixture/core.control-dispatcher`. Confirmed RED.
- `graphroute_test.go` new instantiation test (`TestDecorateGraphWorkflowRecipe_ControlStepPrefersRigScopedDispatcher`): on baseline, `PreferredDeterministicControlDispatcher(cfg, "fixture")` returns the city singleton (same reasoning), and since the test doesn't wire `ControlDispatcherRuntimeMissing`, the wrapper skips the demotion check entirely (`deps.ControlDispatcherRuntimeMissing == nil` short-circuits at graphroute.go:297) and returns the city binding unchanged. Baseline produces `finalize.gc.routed_to = "core.control-dispatcher"`; test wants `"fixture/core.control-dispatcher"`. This matches the exact failure string the PR body itself reports ("finalize gc.routed_to = \"core.control-dispatcher\", want fixture/core.control-dispatcher") — strong corroboration the author actually ran this. Confirmed RED.
- `control_integration_test.go` and the second `graphroute_test.go` table case follow the identical helper logic; RED confirmed by the same trace.

## 3. Blast radius unmentioned (Phase 6, graded)

- **NEW REGRESSION — none found beyond the mis-claiming comment itself.** The routing logic change is correct for every deployment shape enumerated above; I found no case where this PR breaks previously-correct routing.
- **RESTORED PRE-EXISTING GAP** — the attempt-time path's total absence of liveness checking. `git show 41785d976^:internal/dispatch/control.go` (pre-#3765) shows `controlDispatcherTargetForExecutionTarget` already matched `Dir == rigContext` directly with no liveness gate at all — #3765 didn't add a liveness check to this path either, it just replaced the unconditional rig-preference with an unconditional city-preference, which incidentally masked the gap (a _dead_ rig-scoped dispatcher was never selectable anyway, since the code always preferred the always-alive city singleton). This PR's inversion re-exposes a gap that predates both #3765 and this PR, rather than introducing a new one. The comment's _false claim of coverage_ is the actual defect (item 1 above), not the gap itself — a correct comment describing this as an unaddressed pre-existing gap would have been fine.
- **CALLERS FULLY ACCOUNTED FOR.** `grep -rln "PreferredDeterministicControlDispatcher"` (excluding tests) returns exactly `internal/config/config.go` (definition), `internal/dispatch/control.go`, `internal/graphroute/graphroute.go` — both non-test call sites are touched by this PR. No third consumer was missed.
- **NEEDS A SENTENCE IN THE PR BODY** — operational impact for existing deployments. Any city that has configured a per-rig deterministic control-dispatcher agent alongside the city singleton, but where that rig agent's session has never been needed (all control routing previously went to the city singleton under #3765's logic), will now have its control beads routed to the rig-scoped agent, which — depending on this codebase's session-pool spawn semantics — likely triggers a session spawn for an agent that was previously configured-but-dormant. I was not able to independently verify the spawn-on-route trigger mechanism in this session (see Uncertainty); this is an inference, not a proven behavior change, but it's a plausible operational surprise for existing multi-rig deployments and belongs in the PR body regardless of which way it resolves.

## 4. Test adequacy (Phase 7)

**RED-on-main: established by trace for all four flipped/new assertions** (see Correctness above); not executed in this session because applying the PR's test diff to the pinned checkout would require a file write, which is outside the read-only scope of this review.

**Baseline the flipped tests:** the four _unchanged_ cases in `TestPreferredDeterministicControlDispatcher` (config_test.go) — empty-scope→city, rig-only→rig, non-deterministic→none, wrong-rig→none — are semantically unaffected by the diff (confirmed by hand-tracing the new branch conditions against each), consistent with the PR body's claim that "the other four cases... unchanged and green."

**Protective existing test survives:** `TestControlDispatcherBinding_CityOnlyBoundDispatcher` (graphroute_test.go:781, not touched by this diff) pins the original #3764 bug this whole helper exists to prevent. Traced against the new logic by hand: still resolves correctly for both `rigContext=""` and `rigContext="fixture"` when only a city-level dispatcher is configured. #3764 stays fixed.

**Blocking gap — the untested composition the PR's own safety argument depends on.** The PR's stated safety story is: static preference (this change) + runtime demotion (#3454) together handle both ownership and liveness. But the _only_ existing tests that exercise `ControlDispatcherRuntimeMissing` demotion (`TestControlDispatcherBinding_FallsBackToCityWhenRigRuntimeMissing`, `_NoFallbackWhenRigHealthy`, `_NoFallbackWhenCheckerNil`, `_NoFallbackWhenNoDistinctCityDispatcher` — graphroute_test.go:982-1044) use `dispatcherFallbackCfg()`, whose agents are `{Name: "control-dispatcher"}` and `{Name: "control-dispatcher", Dir: "gc-contrib"}` — **neither has a `StartCommand`**. `config.IsDeterministicControlDispatcher` requires `strings.Contains(agent.StartCommand, "convoy control --serve")` (config.go:86-92), so both fixture agents fail that check, `PreferredDeterministicControlDispatcher` returns `ok=false` for them, and `resolveControlDispatcherBinding` falls through to the _resolver-based_ fallback path (graphroute.go:337) — never touching the helper this PR changed. The demotion tests exercise the resolver fallback, not the deterministic-preference path whose behavior this PR just inverted. I traced the composition by hand (Correctness §2 above) and found no bug, but the PR's central safety claim — "the static/runtime split is safe because #3454 catches liveness" — has zero test coverage for the actual shape it will now hit in production (both dispatchers deterministic, rig-scoped one preferred-then-demoted). This is the acceptance-shape gap that must be closed before merge.

**Perf-claim check:** not applicable — this PR is a correctness fix, no performance claim is made.

## 5. Change requests

**Blocking:**

1. **Fix the false-coverage comment in `internal/dispatch/control.go`** (`controlDispatcherTargetForExecutionTarget`, the comment block immediately above the `if agentCfg, ok := config.PreferredDeterministicControlDispatcher(...)` line). Minimum acceptable rewording: state plainly that this attempt-time re-route path performs static ownership selection only and has **no** #3454 runtime-missing demotion, unlike the graph.v2 instantiation path in `internal/graphroute`. Do not claim or imply liveness is handled "downstream" for this specific function. Threading the demotion into the attempt-time path is a larger fix and is explicitly not required for this PR to merge — but the comment must stop asserting it exists.
2. **Add a composed regression test in `internal/graphroute/graphroute_test.go`** that exercises `ControlDispatcherBinding` with BOTH a city-level and a rig-scoped agent configured as genuinely deterministic (both with a `StartCommand` containing `"convoy control --serve"`, per `IsDeterministicControlDispatcher`'s requirements — e.g. `config.ControlDispatcherStartCommandFor(...)` as used in the PR's own new `TestDecorateGraphWorkflowRecipe_ControlStepPrefersRigScopedDispatcher` fixture), with `deps.ControlDispatcherRuntimeMissing` returning `true` for the rig-scoped agent's qualified name. Assert the binding demotes to the city-level agent with `ControlFallbackFrom` set to the rig-scoped agent's qualified name. This proves the demotion the PR's safety argument relies on actually fires for the deterministic-dispatcher shape, not just the resolver-fallback shape the existing tests cover.

**Optional:**

3. Tighten the doc comment on `config.PreferredDeterministicControlDispatcher` itself (config.go:95-104) to note that only the `internal/graphroute` caller has downstream runtime-liveness handling; the `internal/dispatch` caller does not. Same underlying accuracy issue as item 1, lower severity since it's on the shared helper rather than asserting coverage for a specific path.
4. Add one sentence to the PR body naming the operational implication for existing multi-rig deployments that configure but don't currently use a per-rig deterministic dispatcher: control routing for that rig will shift to the rig-scoped agent post-merge, which may trigger session activity for a previously-dormant agent. Not required for correctness; it's a heads-up for anyone doing the upgrade.

## 6. Uncertainty

**Verified (command + result shown above):**

- Commit `41785d976` (#3765) exists and matches the PR's description of it, both post- and pre-state of `internal/dispatch/control.go`.
- `grep -rn "RuntimeMissing" internal/dispatch/` returns nothing — the attempt-time path has no liveness check, before or after this PR.
- File/line-count scope reconciliation against the metadata is exact.
- `IsDeterministicControlDispatcher`'s `StartCommand` requirement, and that `dispatcherFallbackCfg()`'s fixture agents fail it — the existing runtime-missing demotion tests do not exercise the deterministic-preference path.
- The untouched protective test `TestControlDispatcherBinding_CityOnlyBoundDispatcher` remains correct against the new logic (hand-traced).
- All four RED-on-main assertions, hand-traced against the baseline code actually present in this checkout; one traced result matches the PR body's own self-reported failure string verbatim.

**Inferred (reasoning shown, not independently executed):**

- The demotion composition (rig-scoped-preferred-then-demoted-on-runtime-missing) is logically correct by code reading; I did not run the test because doing so would require writing the PR's test files into the read-only checkout. This is exactly the gap change request #2 closes — I'm confident in the trace, not in an executed proof.
- The "demand-spawn" operational-impact claim (finding 6.4) is a plausible inference about session-pool behavior, not something I traced through the pool/session-spawn code in this session.

**Unverifiable from here:**

- The PR's live-corroboration anecdote (manually re-prefixing 70 stranded routes un-stuck a production `dip` rig program) — no way to verify a production incident from a static checkout. My independent trace supports the mechanism the PR describes, so I am not gating the decision on this anecdote either way, per the calibration rule against gating on unverifiable production claims when the trace holds up independently.
- CI status and existing review state — the metadata JSON supplied for this review did not include `statusCheckRollup` or `reviews` fields. Resolving step: `gh pr view 4006 --repo gastownhall/gascity --json statusCheckRollup,reviews` before any merge action.
- Whether `41785d976` (#3765) is genuinely absent from the `v1.3.3` release tag, as the PR body claims ("edge-only — not in v1.3.3"). Resolving step: `git tag --contains 41785d976` or `git log v1.3.3..main --oneline | grep 41785d976` against a full (non-pinned) clone. Immaterial to the merge decision either way — it's context, not a claim the fix's correctness depends on.
- Whether `go build ./...`, `go vet`, and the full `go test ./internal/config/... ./internal/graphroute/... ./internal/dispatch/...` run actually pass as claimed — not executed in this session (would require applying the diff to a writable worktree, out of scope for this read-only review). Resolving step: apply the PR branch in a scratch worktree and run the exact commands the PR body cites before merge.

## Memory cross-reference

No memory in this session's context references PR #4006, the DIP rig, or `control-dispatcher` routing specifically. No overlap with in-flight PRs or documented baseline flakes was available to check (not part of the provided inputs).

## Suggested actions

None — this is an internal report only, no GitHub actions are being taken in this session.
