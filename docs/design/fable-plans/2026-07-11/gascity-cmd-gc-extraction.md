# cmd/gc extraction plan — next 3 tranches

Date: 2026-07-11 · Target repo: `/home/ds/gascity-main` (`github.com/gastownhall/gascity`) · Author: architect session (read-only analysis)
Builds on: arch review `/home/ds/gas-city/docs/arch-reviews/2026-07-10/gascity.md` (findings #1, #2) and the maintainer-owned extraction doc `engdocs/design/session-lifecycle-domain-cleanup-plan.md` (Status: implemented with boundary hardening). This plan continues that migration; it does not restart it.

---

## 0. Ground truth (verified 2026-07-11)

**What has already landed.**

- `internal/session` is substantially extracted: 10,396 non-test LOC / 39 files, including `lifecycle_projection.go`, `lifecycle_transition.go`, `manager.go`, `names.go`, `named_config.go`, `resolve.go`. The lifecycle-cleanup plan's Phases 1–7 are done; its hardening audit names exactly which cmd/gc files retain intentional raw-metadata access.
- `internal/supervisor` (2,173 LOC) is the machine-wide **registry + Dolt maintenance loop** package, not a reconciler home. The reconciler brain (~16.3K LOC) is still `package main`.
- `internal/orderdispatch` exists as a **74-line seam contract** (`Dispatcher`, `DispatchRequest`, `DispatchResult`, `Source`). Its package doc states the problem this plan fixes: *"The concrete Dispatcher lives in cmd/gc … this package holds only the contract so downstream packages (internal/webhooksink, internal/api) can depend on it without importing package main."* Same acknowledgment in `internal/api/state.go:244-259` (`WebhookDispatchProvider`: "the dispatch machine lives in cmd/gc … which internal/api cannot import").
- **Shim mechanism is proven** and uses four forms, often combined: thin wrapper funcs (`func samePath(a,b string) bool { return pathutil.SamePath(a,b) }`), pure type aliases (`type ConvoyFields = convoy.ConvoyFields`), var-func delegations (`var controllerStateOpenRigStoreAtForCity = beads.OpenStoreAtForCity`), and const re-exports. Because cmd/gc is one flat package, an unexported alias to an exported symbol (`type slingOpts = sling.SlingOpts`) gives **zero call-site churn** across all 309 files.
- Shim-count correction: the review's "53 shims" is not reproducible under a strict definition. Measured: **14 shim-dominant files** (≥50% of funcs are one-line delegations; 7 are pure shims ≤27 LOC) and **116 files** that import an internal package and forward at least one call. The pattern is proven either way; the exact count does not change this plan.
- Extraction-commit precedent (last 90 days): the repo lands these as **move + typed seam + alias collapse in a single squashed PR** (e.g. #3305 "collapse graph-routing alias seams onto internal/graphroute", #3829 importsvc extraction, #4056 typed session.WaitInfo codec). This plan follows that shape: one tranche = one PR.

**Config-reload topology (verified, load-bearing for risk sections).** Only `controller.go` re-reads config at runtime: socket `reload:` → `handleReloadSocketCmd` → `reloadReqCh` → `tryReloadConfig` (controller.go:902) → `config.LoadWithIncludes`; plus fsnotify via `watchConfigTargets`. Every file this plan moves receives `*config.City` **by argument** — none re-reads config. The one reload-sensitive spot we touch is the order-dispatcher swap in `city_runtime.go:1338-1339` (state carry from `prev` to `next` `*memoryOrderDispatcher` across reload).

**Test corpus reality.** The reconciler-adjacent cluster carries 43 test files / ~59K test LOC, all white-box `package main` against unexported symbols. Tests must move with their code in every tranche; only `compute_awake_set_test.go` already exercises an exported API.

**PR-traffic embargo (as of 2026-07-11).** `session_reconciler.go` is open in **three** PRs (4137, 4081, 4095); `build_desired_state.go` + `city_runtime.go` in 4149; `api_state.go` in 4153 + 4154; `session_reconcile.go` in 4155 + 4095; `bd_env.go` in 4154 + 4111; `order_store.go` + `order_dispatch_test.go` in 4111; `pool_session_name.go` + `session_affinity_metadata.go` in 4151; `bead_policy_store.go` in 4153. Consequence: **the reconciler mainline (finding #1's core) is temporarily un-extractable without guaranteed conflicts.** These three tranches clear the periphery, unblock the API seam (finding #2), and establish the landing zones so the reconciler moves cleanly in tranche 4+ once the queue drains.

---

## Tranche 1 — pure reconciler decision cores → new `internal/reconcile`

*The first PR to cut. Shippable today; zero open-PR overlap.*

### Files and symbols to move

| From (cmd/gc) | LOC | To | Symbols |
|---|---|---|---|
| `compute_awake_set.go` | 796 | `internal/reconcile/awake.go` | `ComputeAwakeSet`, `AwakeInput`, `AwakeAgent`, `AwakeNamedSession`, `AwakeSessionBead`, `AwakeWorkBead`, `AwakeDecision`, `defaultOnDemandIdleTimeout`, unexported helpers (all already effectively exported-style) |
| `reconcile_tick.go` | 83 | `internal/reconcile/tick.go` | `reconcileTick` → export as `Tick`; `newReconcileTick` → `NewTick`; methods `apply`/`applyResult`/`markClosed` → `Apply`/`ApplyResult`/`MarkClosed` |

**Stays in cmd/gc:** `compute_awake_bridge.go` (286 LOC). The hardening audit explicitly designates it a controller adapter with allowed raw-metadata access; it is the input assembler, not the decision core. Moving it would drag bead-metadata assembly into the new package for no leverage.

### Target package rationale

New leaf `internal/reconcile`, not `internal/supervisor`. The review's shorthand ("reconciler → internal/supervisor") predates looking inside that package: `internal/supervisor` is the machine registry + maintenance loop, a different reason-to-change. `internal/reconcile` imports only `internal/session` and `internal/beads` (verified from the two files' import blocks) — a clean leaf that becomes the declared destination for `session_reconciler.go`, `session_reconcile.go`'s evaluators, and `build_desired_state.go` in tranches 4+.

### Shim mechanism

One new file `cmd/gc/reconcile_shims.go` (~40 lines):

```go
type AwakeInput = reconcile.AwakeInput          // + the 5 sibling input/decision aliases
type reconcileTick = reconcile.Tick
var newReconcileTick = reconcile.NewTick
func ComputeAwakeSet(in AwakeInput) map[string]AwakeDecision { return reconcile.ComputeAwakeSet(in) }
```

Method renames (`apply` → `Apply`) are the only call-site edits; they touch `session_reconciler.go` at its ~3 fold sites. Keep those edits to the bare rename so the three open reconciler PRs rebase trivially.

### Expected diff shape

- ~880 prod LOC moved (net-zero), ~2,800 test LOC moved, +~40-line shim file, +~10 method-rename lines inside `session_reconciler.go`/`session_reconcile.go`.
- No behavior change; no new deps; `go.mod` untouched.

### Test strategy

- **Pinning (existing, moves with code):** `compute_awake_set_test.go` (2,316 LOC, already exported-API — near-mechanical move), `compute_awake_set_min_active_test.go` (339), `reconcile_tick_test.go` (139, includes the property test `TestReconcileTickApplyMatchesRawFold`).
- **Guard-test split (the one subtlety):** `TestReconcileTickFoldFrontDoor` is a textual guard that forbids bare `infoByID[...] =` writes outside `reconcile_tick.go`. Its *scan target* is cmd/gc (where the reconciler's fold call sites remain), so that guard **stays in cmd/gc** repointed at the shim + reconciler files; the property test moves with `Tick`.
- **Characterization to add first:** none needed beyond what exists — `ComputeAwakeSet` has 138 direct test invocations against the exported symbol. This is the best-pinned code in the whole cluster.

### Risk

- Config reload: none (pure functions; config arrives pre-parsed in `AwakeInput`).
- Goroutines: none (verified — no spawns, tickers, or locks in either file).
- Residual: the front-door guard losing its teeth if repointed wrong. Mitigation: assert in the moved property test that `Tick` is the only writer, and keep the cmd/gc textual guard green in the same commit.

### Merge-conflict forecast

Near zero. `compute_awake_set.go`: 2 commits/30d; `reconcile_tick.go`: 0 commits/30d; neither file appears in any of the ~22 most-recently-updated open PRs. The rename edits inside `session_reconciler.go` are ~3 single lines; PRs 4137/4081/4095 rebase over them mechanically.

---

## Tranche 2 — order-dispatch machine → `internal/orderdispatch`

*The highest-value tranche for finding #2: it deletes the documented "internal/api cannot import the dispatcher" workaround. Land after PR #4111 merges.*

### Files and symbols to move

| From (cmd/gc) | LOC | To | Symbols |
|---|---|---|---|
| `order_dispatch.go` | 3,150 | `internal/orderdispatch/` split ≈3 files: `memory.go` (dispatcher core), `tracking.go` (tracking-index + sweeps), `launch.go` (exec/wisp dispatch) | `memoryOrderDispatcher` → `MemoryDispatcher`; `newMemoryOrderDispatcher` → `NewMemoryDispatcher`; `buildOrderDispatcher(WithSnapshot/FromOrderSet)` → `Build…`; `orderDispatcher` iface → `TickDispatcher`; `ExecRunner` (already an injection point), `lockedWriter`, `orderSetSnapshot`, `orderDispatchTrackingIndex`, `orderTrackingSummary/SweepResult/RetentionPolicy`, `dispatch/dispatchOne/dispatchExec/dispatchWisp/drain/cancel/addInflight/doneInflight/launchResolvedDispatch` |
| `order_store.go` | 658 minus ~90 | `internal/orderdispatch/store.go` | `orderStoreResolver`, `orderStoresResolver`, `resolveOrderStoreTarget`, `orderStoreTargetKey`, `orderTrackingSweepKey/Label`, trigger-option resolution — **except** `orderExecEnvWithError` (see seam below) |
| `order_dispatch_seam.go` | 67 | folds into `memory.go` | `Dispatch` (the `orderdispatch.Dispatcher` impl) — the contract and its implementation finally live in one package |
| `store_target_exec.go` (type only) | ~10 of 163 | `internal/orderdispatch/target.go` | `execStoreTarget` → exported `StoreTarget{ScopeRoot, ScopeKind, Prefix, RigName}`; the env-projection funcs in that file **stay in main** |

### Seams injected instead of moved (the load-bearing design decision)

1. **Exec-order env construction stays in cmd/gc.** `orderExecEnvWithError` (order_store.go:149) calls the bd-env projection family (`bdRuntimeEnvForRigWithError`, `bdRuntimeEnvWithError`, `applyOrderExecCanonicalDoltEnv`, `ensureProjectedDoltEnvExplicit`, `ensureProjectedPostgresEnvExplicit`, `mergeRuntimeEnv`) — all in `bd_env.go`, which is hot in PRs 4154 and 4111 (4111 lands GH_TOKEN projection *inside this exact function*). Inject it as one constructor field:
   `ExecEnv func(cityPath string, cfg *config.City, target StoreTarget, a orders.Order, vars map[string]string) ([]string, error)`
   One injection point; bd-env churn never touches the extracted package; PR 4111's semantic change composes cleanly.
2. **`ExecRunner`** already exists as an injected func type — unchanged.
3. **Events** already arrive via injected `events.Recorder` (`m.rec`) — unchanged.
4. `loadSuspensionState` is a 13-line wrapper over `internal/suspensionstate` — the moved code calls `suspensionstate` directly (2 call sites).
5. `closeBeadStoreHandle` and any other small cmd/gc helpers the compiler surfaces: move if ≤~30 LOC and dep-free, else inject. Decide at build time; the flat package hides nothing else material (coupling audit found order_dispatch to be the most self-contained large file in the cluster).

### Shim mechanism

`cmd/gc/order_dispatch_shims.go` (~60 lines): `type memoryOrderDispatcher = orderdispatch.MemoryDispatcher`, `type execStoreTarget = orderdispatch.StoreTarget` (keeps `cmd_bd.go`, `api_state.go`, `store_target_exec.go` compiling untouched), wrapper funcs `buildOrderDispatcherWithSnapshot`/`FromOrderSet` that close over the cmd/gc-side `orderExecEnvWithError` when constructing. Result: the three construction call sites in `city_runtime.go` (lines 279, 1366, 1981) and the reload handoff at 1338-1339 compile **with zero edits** — deliberate, because `city_runtime.go` is the #2 hottest file in the package (61 touches/60d, open in 4149).

Follow-up (not this PR): relocate `StoreTarget` to a neutral home if `cmd_bd.go` depending on `orderdispatch` for the type offends; the alias makes that a one-line change later.

### Expected diff shape

- ~3,800 prod LOC moved (net-zero, resliced into 4 files honoring file-size conventions), ~10,400 test LOC moved, +~60-line shim file, +~15 lines of seam wiring, −67 lines (seam adapter file folds away).
- Zero changes in `internal/api` this PR; the payoff there is unblocked, not spent.

### Test strategy

- **Pinning (existing, moves to `package orderdispatch`):** `order_dispatch_test.go` (9,579 — constructs `newMemoryOrderDispatcher` directly, mechanical rename), `order_dispatch_gate_test.go` (59), `order_dispatch_gate_policy_test.go` (340), `order_dispatch_close_race_test.go` (166 — pins the store-handle/goroutine close ordering from gascity#3157), `order_dispatch_tracking_index_race_test.go` (69), `order_dispatch_seam_test.go` (154).
- **Stays in cmd/gc (CLI-level):** `order_dynamic_integration_test.go`, `order_scan_contract_test.go`, `order_enabled_filter_test.go`, `order_args_channel_test.go`, plus the txtar scripts.
- **Characterization to add first (before moving anything):**
  1. *Env-seam equivalence:* golden test asserting the injected `ExecEnv` path produces byte-identical env to today's direct call for a fixture city (rig-scoped and city-scoped targets, `[order.env]` overlay, dispatch vars overlay, pack/dir keys). This is the one behavior boundary the extraction introduces.
  2. *Reload state-carry:* pin `city_runtime.go:1338-1339` — build dispatcher A, start an in-flight dispatch, swap to dispatcher B via the reload path, assert in-flight accounting and tracking-index carry-over. If `city_runtime_test.go` already covers it, cite it in the PR instead of duplicating.

### Risk

- **Goroutine boundaries (the real risk):** `launchDispatchOne` runs `dispatchOne` async; store handles must stay open until the goroutine's final tracking-bead write (`onDone` closer, #3157); `drain(ctx)`/`cancel()`/`addInflight`/`doneInflight` gate shutdown. All of this moves **as a unit** — the package boundary sits outside the goroutine lifecycle, which is why this cluster was chosen. The close-race and tracking-index-race tests move with it and pin exactly these properties.
- **Config reload:** the dispatcher never re-reads config (`m.cfg` is injected at construction; reload builds a *new* dispatcher and carries state across). The characterization test above pins the handoff.
- **Behavior drift in env building:** eliminated by keeping `orderExecEnvWithError` in main and pinning equivalence.

### Merge-conflict forecast

Medium, with a clear gate. `order_dispatch.go`: 10 commits/30d. Open-PR overlap: **#4111 only** (touches `order_store.go`'s env function — which stays in main — and `order_dispatch_test.go`). Sequencing rule: **land after #4111 merges**, then this PR's test-move rebases in one pass. #4149's `city_runtime.go` changes don't collide because this PR leaves that file byte-identical. If a new order-dispatch PR opens mid-flight, the alias shim means their diff still applies inside the moved files with only a path change.

---

## Tranche 3 — session identity + template substrate → `internal/session`

*The prerequisite both for the API session-create operation (finding #2's named parity gap) and for extracting `build_desired_state.go` in tranche 4+. Land after PR #4151 merges.*

### Files and symbols to move

| From (cmd/gc) | LOC | To | Symbols |
|---|---|---|---|
| `session_name_lookup.go` | 937 | `internal/session/name_lookup.go` | `normalizedSessionTemplate`, `normalizedSessionTemplateInfo`, `sessionBeadAgentName`, `explicitBeadIDStore`, `poolManagedMetadataKey`, lookup helpers (imports: agent, beads, config, session — no cmd/gc-only deps in import block) |
| `named_sessions.go` | 129 | **collapse, don't move** | Already shim-dominant (16/19 funcs delegate to `session.NamedSessionSpec` et al). Fold the 3 residual helpers into `internal/session/named_config.go`; delete the wrappers or keep the alias file — reviewer's choice |
| `session_hash.go` | 27 | `internal/session/config_hash.go` | `sessionCoreConfigForHash` (+ its 1 helper) — feeds config-drift detection |
| `session_bead_snapshot.go` | 412 | `internal/session/bead_snapshot.go` | `sessionBeadSnapshot`, `OpenInfos` |
| `agent_build_params.go` | 279 | `internal/session/build_params.go` | `agentBuildParams` (threaded through all of `build_desired_state.go` — exporting it here is what makes tranche 4 possible) |
| `template_resolve.go` | 850 | `internal/session/template_resolve.go` | `resolveTemplate`, `TemplateParams`, `DisplayName`, `ensureClaudeSettingsArgs` chokepoint. Per its own header, its output type exists "for session.Manager.CreateFromParams" — this is the type's documented home |

**Explicitly excluded** (PR #4151 territory): `pool_session_name.go`, `session_affinity_metadata.go`, and all `pool*.go`. Also excluded: `session_wake.go`/`session_sleep.go`/`session_beads.go` (reconciler-coupled, tranche 4+) and `cmd_start.go`'s `buildFingerprintExtra`/`mergeEnv` (inject as func params if `template_resolve.go` needs them; the compiler will rule).

### Pre-flight (first commit of the PR, before any move)

`template_resolve.go` performs one filesystem side effect at resolve time (projecting managed Claude settings to `.gc/settings.json` via `ensureClaudeSettingsArgs`) and participates in **session fingerprinting**. A fingerprint change is not a cosmetic bug: `relaunchAgentForLaunchDrift` treats fingerprint drift as config drift and restarts sessions fleet-wide. So: add a **fingerprint-stability characterization test first** — resolve a fixture city's agents, snapshot `TemplateParams` + the derived fingerprint inputs as goldens, and assert the moved code reproduces them byte-for-byte. This test is the tranche's safety net and stays after the move as a regression guard.

### Shim mechanism

`cmd/gc/session_template_shims.go` (~80 lines): type aliases (`type TemplateParams = session.TemplateParams`, `type agentBuildParams = session.AgentBuildParams`, `type sessionBeadSnapshot = session.BeadSnapshot`), wrapper funcs for `resolveTemplate`, `normalizedSessionTemplate*`, `sessionBeadAgentName`, `sessionCoreConfigForHash`, const re-export for `poolManagedMetadataKey`. Zero call-site churn in `build_desired_state.go` (13 uses), `session_reconciler.go` (~8 uses), and the open reconciler PRs — that is the point of doing this tranche via aliases while those files are hot.

Import-cycle check (the cleanup plan's own top risk): the moved files import `agent`, `beads`, `config`, `runtime` — `internal/session` already sits above all of these; none imports session back (spot-verified for agent/config). If the compiler finds a cycle via `runtime`, fall back to a sibling `internal/sessiontemplate` package with the same shims; the tranche shape is unchanged.

### Expected diff shape

- ~2,540 prod LOC moved, ~100 collapsed (named_sessions wrappers), +~80-line shim file, matching `*_test.go` files move to `package session` (enumerate at build; `session_name_lookup`/`template_resolve`/`agent_build_params`/`session_bead_snapshot` test files, plus the new golden test).
- `internal/session` grows ~2.5K to ~13K LOC — acceptable; it is the designated domain home and the files are already split small.

### Test strategy

- **Pinning:** the moved files' white-box tests (rename to `package session`); `build_desired_state_test.go` (11,732 LOC) stays in cmd/gc and keeps exercising the substrate *through the aliases* — it is the strongest behavioral pin for this tranche and requires zero edits.
- **Characterization added first:** the fingerprint-stability golden above; plus one guard extending the cleanup plan's Phase-7 pattern: a test asserting cmd/gc no longer *defines* (only aliases) the moved symbols, so the substrate cannot silently fork back into main.

### Risk

- **Config reload:** indirect but real — this substrate computes what the reconciler compares against on reload (config hash, template params). Behavior drift ⇒ spurious mass relaunch on the first reload after deploy. Mitigated by the golden test; verify additionally with one manual `gc reload` on a staging city and assert zero relaunches.
- **Goroutines:** none in the moved files (pure resolution + one file write).
- **Side effect placement:** `ensureClaudeSettingsArgs` writing `.gc/settings.json` moves into `internal/session` — acceptable (session manager already owns session-adjacent state), but call it out in the PR body since it makes the package non-pure.

### Merge-conflict forecast

Medium-low. `session_name_lookup.go` 6 commits/30d, `template_resolve.go` family ~12/30d, others ≤4/30d. No open PR touches the six moved files (4151 touches the excluded pool/affinity files). The hot neighbors (`session_reconciler.go`, `build_desired_state.go`) are consumers, untouched thanks to aliases — PRs 4137/4081/4095/4149 rebase without seeing this tranche.

---

## After these three (tranche 4+ queue, for orientation only)

1. `session_reconcile.go` evaluators (`evaluateWakeReasons`, `computeWorkSet`, `checkStability`, `topoOrder`) → `internal/reconcile` — once PRs 4155/4095 land.
2. `session_reconciler.go` + `session_lifecycle_parallel.go` (the goroutine-heavy start/stop waves: `asyncStartTracker`, wave executors) → `internal/reconcile` — once the 3-PR embargo clears; tranches 1–3 will have already exported most of what they call.
3. `build_desired_state.go` + `pool*.go` → `internal/reconcile` — after 4149/4151; tranche 3 exported its substrate.
4. `crashTracker` + `session_circuit_breaker.go` (953 LOC, 3 commits/30d) → `internal/session` — directly slims `api.State.IsQuarantined`/`ClearCrashHistory` in the fat adapter.
5. `mutateAndPoke`'s config-mutation helpers (`captureConfigMutationSnapshot`, `loadCityConfigWithBuiltinPacks`, `applyFeatureFlags`, `applyRuntimeCityIdentity`) + store construction (`buildStores`, `initDirIfReady`, `resolveStoreScopeRoot`) → `internal/citystate` — this is the tranche that lets `api_state.go` shrink from 1,920 LOC to a thin binder and opens finding #2's shared ops layer; blocked today by PRs 4153/4154 on `api_state.go`.

Standing conventions for every tranche: move + alias in one PR (repo precedent); tests move with their code; run the cleanup plan's gate `go test ./internal/config ./internal/session ./internal/api ./cmd/gc ./internal/reconcile ./internal/orderdispatch` plus `make build && make check`; no formatting churn outside moved files; each PR reversible by `git revert` (aliases mean no caller ever learned the new paths).

---

## Executive summary

1. The reconciler mainline is embargoed by 5 open PRs on its exact files, so the next three tranches clear the periphery: pure decision cores, the order-dispatch machine, and the session identity/template substrate — together ~7.2K prod + ~16K test LOC out of `package main`, each as one alias-shimmed, revertible PR.
2. Tranche 1 (`internal/reconcile`: `ComputeAwakeSet` + `Tick`, ~880 LOC) has zero open-PR overlap and creates the landing zone the reconciler moves into later.
3. Tranche 2 (`internal/orderdispatch`: `MemoryDispatcher` + store resolution, ~3.8K LOC) closes the API's documented "cannot import the dispatcher" seam (state.go:244-259) — the biggest single step for the service layer — gated only on PR #4111, with exec-env building kept in main behind one injected func.
4. Tranche 3 (`internal/session`: name lookup, build params, template resolution, ~2.5K LOC) unblocks both the API session-create operation and the tranche-4 `build_desired_state` extraction; its one hard risk (fingerprint drift ⇒ mass relaunch) is pinned by a golden test written before the move.
5. **First PR to cut: Tranche 1** — cut it today; while it reviews, watch #4111, and start Tranche 2 the day it merges.
