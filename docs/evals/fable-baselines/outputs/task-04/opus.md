# Maintainer decision — PR #4006

`fix(control-dispatcher): prefer rig-scoped dispatcher for its own scope (revert #3765's static city-preference)`
Author: Chris Sauer (`csauer02-personal-user`) · +121 / −55 · 6 files

Reviewed against `gastownhall/gascity` at pinned `ee616a7e4` (unfixed main; the base helper in the checkout matches the diff's removed lines byte-for-byte).

**Decision: REQUEST CHANGES.** The reported bug is real and the core fix provably repairs it, but the change ships an asymmetry it explicitly denies: the #3454 liveness demotion that makes rig-scoped routing safe exists on only _one_ of the two call-sites the PR touches. In the exact edge case #3454 was built to handle (a rig dispatcher configured-but-asleep), the attempt-time path now re-strands the control bead where pre-PR it did not. This is narrow but it is a regression against a guarantee the codebase deliberately built, and the PR's own added comment asserts a safety net that isn't there.

---

## 1. Description vs. actual change

Accurate. No gap between what the body claims and what the diff does.

- The only _behavioral_ change is in `config.PreferredDeterministicControlDispatcher` (`internal/config/config.go:105`). The diff inverts the preference: previously the city-level singleton (`Dir==""`) returned immediately for every scope and the rig-scoped instance was a fallback; now a rig-scoped instance whose `Dir==rigContext` returns immediately (guarded by `rigContext != ""`) and the city singleton is recorded as the fallback.
- The changes to `internal/dispatch/control.go:1052-1057` and `internal/graphroute/graphroute.go:322-330` are **comment-only** — the `PreferredDeterministicControlDispatcher(...)` calls themselves are unchanged. So the body's "one edit in the single shared helper fixes both call-sites" is literally true: both stamp sites delegate to the helper (`internal/graphroute/graphroute.go:331`, `internal/dispatch/control.go:1058`) and the rig prefix materializes purely through `Agent.QualifiedName()` prepending `Dir+"/"` (`internal/config/config.go:152-157`).
- The remaining lines are test updates.

## 2. Correctness of the repair (what it provably fixes)

The reported DIP failure is genuinely repaired for the deployment shape described (a rig running its own live dispatcher):

- Config `{city core.control-dispatcher (Dir=""), dip/core.control-dispatcher (Dir="dip")}`, `rigContext="dip"`.
- **Unfixed main** (`internal/config/config.go:116-118`): the loop hits the `Dir==""` agent first and `return a, true` → helper yields the city agent → `QualifiedName()=="core.control-dispatcher"` → control beads stamped unprefixed. The live dip dispatcher session claims only `dip/...` routes, so the beads strand. This matches the observed stall.
- **With the fix** (new `internal/config/config.go:112-127`): the `rigContext != "" && dir == rigContext` branch returns the dip agent → `QualifiedName()=="dip/core.control-dispatcher"`. Both stamp paths produce it. This is exactly the route the author's manual re-prefixing proved correct.

I confirmed all three shapes the body enumerates resolve correctly under the new loop, including order-independence (the city fallback is recorded but the loop continues, so a city agent appearing before the rig agent doesn't win) and the wrong-rig case (`dir != "" && dir != rigContext` matches neither branch → skipped).

## 3. Blast radius the author did not mention (the load-bearing finding)

**The #3454 liveness demotion protects the decoration path but not the attempt-time path — and the PR moves risk into the unprotected one.**

`PreferredDeterministicControlDispatcher` has two production consumers:

1. **Instantiation / graph.v2 decoration** — `resolveControlDispatcherBinding` (`internal/graphroute/graphroute.go:331`) is reached via `ControlDispatcherBinding` (`graphroute.go:290`, called at `graphroute.go:555`). `ControlDispatcherBinding` wraps the raw resolution in the #3454 demotion (`graphroute.go:297-310`): if the resolved rig-scoped dispatcher is `ControlDispatcherRuntimeMissing`, it re-resolves with empty rig context and demotes to the city dispatcher. **Protected.**
2. **Attempt-time re-route** — `controlDispatcherTargetForExecutionTarget` (`internal/dispatch/control.go:1058`) calls the helper **directly**. There is no `ControlDispatcherRuntimeMissing` anywhere in `internal/dispatch` (grep confirms it exists only in `internal/api/handler_sling.go:60` and `internal/sling/sling.go`, both wiring `graphroute.Deps`). **Not protected.**

Consequence for a rig dispatcher that is configured but runtime-missing — the precise scenario `ControlDispatcherBinding`'s own doc comment says "can sit runtime-missing for weeks, silently stranding every molecule's auto-injected workflow-finalize step" (`graphroute.go:285-288`):

|             | Decoration (path 1)                        | Attempt-time re-route (path 2)            |
| ----------- | ------------------------------------------ | ----------------------------------------- |
| **Pre-PR**  | helper→city; demotion no-op                | helper→city (always) — safe               |
| **Post-PR** | helper→rig, then #3454 demotes→city — safe | helper→**rig**, no demotion — **strands** |

And the attempt-time path _overwrites_ the stamp: `applyAttemptControlStepRoute` unconditionally sets `step.Metadata[gc.routed_to] = controlTarget` (`internal/dispatch/control.go:1035-1037`). So on any Attach/fanout re-route (`control.go:500`, `internal/dispatch/fanout.go:318`), a step that decoration had correctly demoted to the city dispatcher gets re-stamped back onto the dead rig dispatcher and re-strands.

The comment this PR _adds_ to `control.go:1055-1057` — "liveness of an asleep rig-local dispatcher is a downstream (#3454) concern, not conflated into this static ownership selection" — is false for this path. There is no downstream #3454 layer between `controlDispatcherTargetForExecutionTarget` and the stamp. The PR asserts symmetry ("keep them in lockstep") that holds for static ownership but breaks on liveness.

Severity: narrow (requires a configured-but-asleep rig dispatcher _and_ an attempt-time control re-route firing for that rig's molecules) but it directly re-opens the failure mode #3454 was written to close, so it is not merely cosmetic. The DIP happy path (live rig dispatcher) is unaffected.

## 4. Test adequacy

- **The new instantiation test pins the reported bug.** `TestDecorateGraphWorkflowRecipe_ControlStepPrefersRigScopedDispatcher` (`internal/graphroute/graphroute_test.go`, added) asserts `finalize gc.routed_to == "fixture/core.control-dispatcher"`. On unfixed main the helper returns the `Dir==""` agent → `core.control-dispatcher` → the test fails. It is a genuine RED-on-main regression test, consistent with the body's "reverting the fix makes the new instantiation test fail."
- **Config unit is correctly re-baselined.** I read `TestPreferredDeterministicControlDispatcher` at `internal/config/config_test.go:7880`: the diff flips only the "rig copy preferred over singleton for its own scope" case to `fixture/core.control-dispatcher`; the other four (`empty-scope→city`, `rig-only→rig`, `non-deterministic→none`, `wrong-rig→none`) are present and unchanged. The body's accounting is exact.
- **The gap is exactly where the bug is.** Both inverted "both-configured" tests (`TestControlDispatcherBinding_PrefersRigScopedOverCitySingleton`, `TestApplyAttemptControlStepRoute_PrefersRigScopedOverCitySingleton`) assert the **healthy-rig** route. Neither exercises a `ControlDispatcherRuntimeMissing` rig dispatcher on the attempt-time path — so the regression in §3 is untested and would ship green. The shipped suite proves the fix works when the rig dispatcher is alive; it does not defend the asleep-rig case the demotion asymmetry breaks.

## 5. Decision & change requests

**REQUEST CHANGES.** Merge is blocked on the attempt-time liveness gap, not on the core inversion (which is correct). Two required, one recommended:

1. **Apply the #3454 demotion to the attempt-time path.** `applyAttemptControlStepRoute` already receives `store` (`internal/dispatch/control.go:500, 1010`), and `session.RuntimeMissingInStore(store, qualifiedName)` (`internal/session/runtime_missing.go:20`) is the same predicate `graphroute` uses. In `controlDispatcherTargetForExecutionTarget`, after the helper returns a rig-scoped agent, if `session.RuntimeMissingInStore(store, agentCfg.QualifiedName())` is true and a distinct city-level deterministic dispatcher exists (`Dir==""`), stamp the city route instead. This restores the lockstep the comment claims. (Thread `store` into `controlDispatcherTargetForExecutionTarget`; it currently takes only `executionTarget, rigContext, cfg`.)

2. **Add a regression test for the asleep-rig attempt-time case.** Both dispatchers configured, `rigContext="fixture"`, the rig dispatcher reported runtime-missing → assert `applyAttemptControlStepRoute` stamps `gc.routed_to == "core.control-dispatcher"` (demoted), not `fixture/core.control-dispatcher`. This test must fail against the current diff and pass after change 1.

3. **(Recommended) Correct the comment.** If change 1 is applied, the `control.go:1055-1057` comment becomes true. If for some reason the team decides the attempt-time path is provably never reached for an asleep rig (I did not find evidence of that — see §6), the comment must instead state and justify that invariant rather than gesture at a #3454 layer that isn't on this path.

If changes 1–2 land, this is an **APPROVE**: the inversion is the right call, correctly scoped to a single shared helper, and it separates static ownership from runtime liveness cleanly _once both consumers actually get the runtime layer_.

## 6. What I remain uncertain about, and how I'd resolve it

- **Production frequency of the regression.** Whether an attempt-time control re-route realistically fires for a rig whose dispatcher is runtime-missing (vs. the molecule simply not being dispatched) I can't determine statically. I'd resolve it by tracing the callers of `applyAttemptControlStepRoute` (`control.go:500`, `fanout.go:318`) up to their dispatch triggers and checking whether an asleep-rig molecule can reach the Attach/fanout path. This changes severity, not direction — the fix in §5 is cheap and removes the question.
- **I did not run the suite.** The checkout is unfixed main, so the PR's new tests aren't present and running `go test` here proves nothing about the diff. To verify the author's "all ok" claim and my regression prediction I'd apply the patch to a scratch worktree and run `go test ./internal/config/... ./internal/graphroute/... ./internal/dispatch/...`, then add the §5.2 test and confirm it's RED before change 1 and GREEN after.
- **The live-corroboration numbers** (108 beads, 45 control steps, 70 re-prefixed routings) are unverifiable from the repo and I treat them as narrative, not evidence. They are consistent with the mechanism and not load-bearing for the decision.
