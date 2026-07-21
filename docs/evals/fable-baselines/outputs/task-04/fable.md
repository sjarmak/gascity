# Maintainer decision report — PR #4006

`fix(control-dispatcher): prefer rig-scoped dispatcher for its own scope (revert #3765's static city-preference)`
Author: csauer02-personal-user. Reviewed against pinned main `ee616a7e4`.

## Decision: REQUEST CHANGES

The core fix is correct and I want it merged. Two precise items block approval: a comment the PR adds to `internal/dispatch/control.go` claims #3454 liveness coverage that provably does not exist on that path, and the layering argument the PR body rests on (rig-preferred statically, #3454 demotes when the rig dispatcher is dead) has no test composing the two mechanisms. Both are small.

## 1. What the PR changes vs. what it claims

The diff matches the description exactly. Six files, +121/−55, consistent with the per-file counts in the metadata. One behavioral edit — inverting the preference order in `config.PreferredDeterministicControlDispatcher` — plus comment updates at both call sites and test flips/additions. The "before" state in the diff is byte-identical to pinned main at `internal/config/config.go:105-129`, `internal/dispatch/control.go:1044-1067`, and `internal/graphroute/graphroute.go:315-345`. No hidden changes.

Claim verification against the checkout:

- `41785d976` is #3765 and is in main's history; its commit message confirms it introduced the city-preference and did so on the `max_active_sessions=1 ⇒ one dispatcher session city-wide` assumption, exactly as the PR characterizes.
- Pre-#3765, the attempt-time path used a strict `Dir == rigContext` match (`git show 41785d976 -- internal/dispatch/control.go`), so this PR's rig-preference restores pre-#3765 semantics for the both-configured case, plus a city fallback pre-#3765 lacked. The title's "(revert ...)" is loose — the body's "not a revert" framing is the accurate one — but that's cosmetic.
- The #3454 runtime demotion exists as claimed, at `internal/graphroute/graphroute.go:291-312` (`ControlDispatcherBinding` demotes rig→city when `deps.ControlDispatcherRuntimeMissing` fires), and is wired in production via `cliGraphrouteDeps` (`cmd/gc/dispatch_runtime.go:29-38`) for every CLI graph-routing call site (`cmd_convoy_dispatch.go:573,657,672,680,684,722`, `cmd_formula.go:889`, `cmd_sling.go:1309`) and via `internal/api/handler_sling.go:112` for the API path.

One overstatement, detailed in §3: the PR's new comment in `internal/dispatch/control.go` extends the #3454 story to a path #3454 does not cover.

## 2. Correctness

The helper is the single selection point: both stamp paths delegate to it (`internal/graphroute/graphroute.go:331`, `internal/dispatch/control.go:1058`), and the route string is just `Agent.QualifiedName()`, so the bug reduces to which agent is returned — as the PR says.

Traced the new selection against all deployment shapes:

- **Rig-with-own-dispatcher (the reported DIP regression):** both configured, `rigContext="dip"` → first `Dir == rigContext` match returns the rig agent → `dip/core.control-dispatcher` stamped. The stranding mechanism is architecturally coherent: dispatcher sessions follow only their own qualified name (`gc convoy control --serve --follow <qualifiedName>`, `internal/config/config.go:82`), and controller demand-spawn maps a routed bead to a dispatcher by exact qualified-name/bare-alias match (`openControlDispatcherDemand`, `cmd/gc/build_desired_state.go:1706-1745`). A bead stamped with the unprefixed route is invisible to the rig dispatcher, and the serve loop's work query is store/dir-scoped (`nextWorkflowServeBeads` shells the work query in the agent's dir, `cmd/gc/dispatch_runtime.go:866-887`), so a city session doesn't drain a rig store. The fix provably repairs this: the route lands on the agent whose session claims it, and demand-spawn wakes that agent if it's idle.
- **City-only + rig scope (#3764):** no `Dir == rigContext` match → city fallback returned. #3764 stays fixed. Pinned by the untouched `TestControlDispatcherBinding_CityOnlyBoundDispatcher` (`internal/graphroute/graphroute_test.go:781-811`), which runs both `rigContext=""` and `"fixture"` against a city-only config and passes unchanged under the new code.
- **Pure singleton (`rigContext==""`):** the rig branch is gated on `rigContext != ""` → city returned. Unchanged.
- **Rig-only + empty scope / wrong-rig scope:** returns `ok=false`, falling to the resolver/string fallbacks at both call sites — identical to main.

Baseline actively verified: `go test ./internal/config/ -run TestPreferredDeterministicControlDispatcher` and `./internal/graphroute/ -run TestControlDispatcherBinding` pass on unfixed main (6 and 14 tests), confirming the tests the PR flips currently pass in city-preferring form.

## 3. Blast radius the author did not mention

**(a) The attempt-time path has no #3454 demotion, and the PR's new comment says otherwise.** `controlDispatcherTargetForExecutionTarget` (`internal/dispatch/control.go:1044`) calls the static helper directly; `grep -rn RuntimeMissing internal/dispatch/` returns nothing. The PR's replacement comment reads "liveness of an asleep rig-local dispatcher is a downstream (#3454) concern" — but #3454's demotion lives only in `graphroute.ControlDispatcherBinding` and never runs at attempt time. After this PR, an attempt-kind control bead in a rig whose dispatcher sits runtime-missing (the weeks-long scenario #3454's own doc comment describes, `graphroute.go:283-289`) is stamped with the rig route and has no city fallback. This is a _restored pre-#3765 gap_, not a new regression, and demand-spawn/reconciler repair may eventually cover it — but the comment as written claims protection that isn't there, in a subsystem where every iteration (#3764 → #3765 → this) has leaned on these exact doc comments to justify itself.

**(b) The demotion never fires when the rig dispatcher has no session bead at all.** `session.RuntimeMissingInStore` (`internal/session/runtime_missing.go:20-40`) lists open session beads and returns false on an empty list. So a configured-but-never-spawned rig dispatcher does not demote; the bead waits on demand-spawn (`openControlDispatcherDemand` keys demand by the stamped qualified name, so a healthy controller spawns the rig dispatcher). Fine when the reconciler is healthy; if the rig session is genuinely unspawnable, the bead strands with no fallback. Worth one sentence in the PR body.

**(c) Fleet-shape change for existing deployments.** Any deployment that materializes per-rig dispatcher copies in config but has been running only the city singleton (the #3765-era steady state) will, after upgrade, see new control beads route per-rig and demand-spawn one dispatcher session per active rig instead of one city-wide. Almost certainly the intended ownership model given the store-scoped serve loop, but it is an operational/capacity behavior change that belongs in the PR body or release notes.

## 4. Test adequacy

- **The new instantiation test pins the bug and fails on unfixed main.** Traced: `DecorateGraphWorkflowRecipe(..., routedTo="fixture/worker", ...)` → `graphBindingRigContext` derives `"fixture"` (`graphroute.go:122-139`) → `ControlDispatcherBinding` → helper. On main the helper returns the city agent, stamping `core.control-dispatcher` on the finalize step (control-kind steps take `controlRoute` via `AssignGraphStepRoute(step, binding, &controlRoute)`, `graphroute.go:621-624`), which fails the `fixture/core.control-dispatcher` assertion. Fixtures used (`intPtr`, `testAgentResolver`) exist in the test package. The author's temporary-revert RED proof matches this trace.
- **All three layers are re-pinned in the new direction** (config unit table, graphroute binding, dispatch attempt-time), and the flipped tests would fail on main, so they genuinely encode the new contract rather than trivially passing.
- **The #3764 protection survives** via the untouched city-only binding test (§2).
- **Gap (blocking, see change request 2):** the PR body's central safety argument — "#3764 stays fixed via the runtime layer" — composes the new static rig-preference with the #3454 demotion, and no test exercises that composition. The existing demotion tests (`TestControlDispatcherBinding_FallsBackToCityWhenRigRuntimeMissing` and siblings, `graphroute_test.go:983-1056`) use `dispatcherFallbackCfg()` whose agents have **no StartCommand** (`graphroute_test.go:976-981`), so they exercise the resolver fallback path, not `PreferredDeterministicControlDispatcher`. Tracing says the composition works (demotion operates on `binding.QualifiedName` regardless of which lookup produced it, and the empty-context re-resolve returns the deterministic city agent), but the exact regression this PR risks reintroducing deserves a pinned test in the same commit.
- **Gap (optional):** the config unit table has no `city-only + rigContext="fixture" → city, ok=true` case. Covered indirectly by the binding-level test; adding it to the table would make the helper's own contract complete.

## 5. Change requests

1. **Fix the attempt-time comment in `internal/dispatch/control.go` (or thread the demotion).** The added text "liveness of an asleep rig-local dispatcher is a downstream (#3454) concern, not conflated into this static ownership selection" is false for this path: no `ControlDispatcherRuntimeMissing` check exists anywhere in `internal/dispatch`, so attempt-time re-stamps have no liveness fallback. Minimum acceptable fix: reword to state that the attempt-time path performs static ownership selection only and, unlike the graphroute instantiation path, has no #3454 runtime demotion (restoring pre-#3765 attempt-time behavior). Alternatively, thread the demotion through — but that's a larger change I'm not requiring for this PR.
2. **Add the composed regression test.** In `internal/graphroute/graphroute_test.go`, a `ControlDispatcherBinding` case with BOTH deterministic dispatchers configured (city `Dir=""` + rig `Dir="fixture"`, both with `ControlDispatcherStartCommandFor` start commands), `rigContext="fixture"`, and `ControlDispatcherRuntimeMissing` returning true for `fixture/core.control-dispatcher`: assert the binding demotes to `core.control-dispatcher` with `ControlFallbackFrom == "fixture/core.control-dispatcher"`. This pins the "static ownership + runtime liveness" layering the PR body argues from, on the deterministic-helper path the PR actually changes.
3. _(Optional, non-blocking)_ Add `{agents: []Agent{citySingleton}, rigContext: "fixture", wantQN: "core.control-dispatcher", wantOK: true}` to the `TestPreferredDeterministicControlDispatcher` table, and a sentence in the PR body noting the per-rig dispatcher session demand-spawn implication for deployments currently running only the singleton (§3c).

## 6. Remaining uncertainty and how I'd resolve it

- **Post-PR suite is unverified by me.** The checkout is read-only and the artifact is a diff, so I could not apply and run it. Before merge I would apply the diff in a scratch worktree and run `go test ./internal/config/... ./internal/graphroute/... ./internal/dispatch/...`, then revert the `config.go` hunk alone and confirm the new instantiation test goes RED with the exact `finalize gc.routed_to = "core.control-dispatcher"` failure the author reports.
- **The live DIP narrative (45 mis-routed steps, manual re-prefix un-sticking 70 routings) is unverifiable from here.** The code trace independently supports the mechanism, so I'm not gating on it; a two-rig integration city (city + rig dispatcher both running, compound build slung into the rig) would confirm end-to-end claim behavior on both sides of the fix.
- **Whether a city dispatcher session can ever drain rig-store control beads.** The dir-scoped work query strongly suggests it cannot (which makes rig ownership not just preferable but required), but I did not trace the work-query template itself. Same two-rig integration run resolves it.
