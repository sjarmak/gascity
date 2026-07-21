# Upstream draft — control-dispatcher namespace regression (9fa6b7fec)

> City-infra-pl analysis for dr-fi5eb. **Text-only drafts for Stephanie's review.**
> Filing the issue + slinging the fix PR are external/gascity-rig actions —
> out of city-infra-pl's floor; the mayor executes after approval.
> Source verified against `/home/ds/gascity-main` @ HEAD `32ca47acd`
> (9fa6b7fec confirmed ancestor = in the deployed binary).
> No internal bead IDs appear in the issue/PR text (upstream hygiene).

---

## Refined root cause (source-confirmed)

Commit **9fa6b7fec** "fix(config): ship control dispatcher in core pack" (2026-06-16,
live in our city since the 2026-06-22 16:38 gcsync) moved the control-dispatcher
agent from per-rig *implicit injection* into the **core pack**
(`internal/bootstrap/packs/core/agents/control-dispatcher/agent.toml`). Core-pack
agents are imported V2 agents, so the dispatcher now carries **`BindingName="core"`**
and its `QualifiedName()` is `core.control-dispatcher` (city-level, `Dir=""`) or
`<rig>/core.control-dispatcher` (rig-scoped materialization).

This breaks the control-dispatcher in **two distinct places**, both traceable to
the binding:

### Fault 1 — bare-name resolution no longer matches the bound agent

`graphroute.ControlDispatcherBinding` (`internal/graphroute/graphroute.go:264`)
resolves the dispatcher by the **bare** constant
`config.ControlDispatcherAgentName = "control-dispatcher"`
(`internal/config/config.go:37`):

```go
agentCfg, ok := deps.Resolver.ResolveAgent(cfg, config.ControlDispatcherAgentName, rigContext)
if !ok {
    agentCfg, ok = configuredControlDispatcherForScope(cfg, rigContext)
}
if !ok {
    return GraphRouteBinding{}, fmt.Errorf("control-dispatcher agent %q not found", config.ControlDispatcherAgentName)
}
```

- `ResolveAgent` (`internal/agentutil/resolve.go:41`) Step 2 (literal) calls
  `findAgentByQualified` → `AgentMatchesIdentity` (`internal/config/config.go:158`),
  which **explicitly refuses the V1 `dir+name` fallback for bound agents**
  ("imported V2 agents must be addressed by their qualified name"). `QualifiedName()`
  is `core.control-dispatcher ≠ "control-dispatcher"` → miss.
- Step 3 (bare-name scan) matches `a.Name == "control-dispatcher"`, but a real city
  has a **fleet** of dispatcher agents (city + one per rig — 21 in ours), so
  `len(matches) > 1` → ambiguous → miss.
- So `ResolveAgent` returns `false`, and resolution survives **only** via the
  `configuredControlDispatcherForScope` fallback (`graphroute.go:274`), which matches
  `IsDeterministicControlDispatcher(&a) && a.Dir == rigContext` — a **Dir-exact**
  match.

The `"control-dispatcher agent \"control-dispatcher\" not found"` error fires
whenever the cook's `rigContext` matches **no** dispatcher's `Dir` — i.e. a city
that has only the core-pack singleton (`Dir=""`) but cooks a formula with a
non-empty `rigContext`. That is the original report's city state.

### Fault 2 — rig-scoped routing vs the city-singleton session (the live wedge)

The core pack ships a **singleton** (`max_active_sessions = 1`; pack.toml: "the
singleton control-dispatcher pool"). In practice **only the city-level
`core.control-dispatcher` session runs** (one session, `Dir=""`). But the
`configuredControlDispatcherForScope` fallback resolves a rig cook to the
**rig-scoped** dispatcher (`Dir=<rig>`), so
`GraphRouteBinding.QualifiedName = "<rig>/core.control-dispatcher"` and that target
is stamped onto the control bead.

The running city-level session's workflow-serve claim query
(`cmd/gc/dispatch_runtime.go:786-819`) claims only:

- `core.control-dispatcher` (`GC_CONTROL_TARGET`),
- bare `control-dispatcher` (`GC_CONTROL_BARE_TARGET`, via `controlDispatcherBareRoute`),
- legacy `workflow-control` (`GC_CONTROL_LEGACY_TARGET`).

It does **not** claim `<rig>/core.control-dispatcher`. So a control bead stamped
with a rig-scoped target has **no running session that claims it** →
routed-but-unconverted → the routed-bead nudger re-wakes the dispatcher every tick
→ the session presents as **active-but-idle, nudges looping** (the exact wedge
symptom; band-aided by repeated dispatcher resets).

**Live evidence (this city):** the event stream carries distinct route-target
spellings — bare `control-dispatcher`, `core.control-dispatcher`, and rig-scoped
`enterprisebench/core.control-dispatcher` — while `gc session list` shows exactly
one dispatcher session: `core.control-dispatcher` (city-level). The codebase
already anticipated bare/legacy/city-qualified route forms
(`controlDispatcherBareRoute`, `legacyWorkflowControlQualifiedName`,
`isWorkflowServeControlDispatcherAgent` suffix matching) but **missed the
rig-scoped-qualified form** in the claim query.

### Answer to the mayor's wedge question

**Same regression, two faces.** The formula-COOK "not found" is currently *masked*
in our city (a per-rig dispatcher exists for every routed scope, so the
`configuredControlDispatcherForScope` Dir-exact fallback resolves; the error is
absent from the live supervisor log). The recurring **session wedge** on
`core.control-dispatcher` is the *live* face of the same 9fa6b7fec binding change:
cooks resolve+stamp **rig-scoped** dispatcher targets that the lone city-singleton
session never claims. The mayor's resets are a band-aid; the namespace/singleton
fix is the cure. (Confidence: high on resolution + single-session facts and the
route-target spellings; the one link to confirm in the AM is that the stranded
rig-scoped control beads are the specific ones the nudger keeps re-waking —
checkable by watching whether a reset drains the `<rig>/core.control-dispatcher`
routed beads or they immediately re-accumulate.)

---

## DRAFT — Issue body (gastownhall/gascity)

**Title:** control-dispatcher unresolvable / control beads strand after it moved
into the core pack (9fa6b7fec)

**Body:**

### Summary

Since `9fa6b7fec` ("fix(config): ship control dispatcher in core pack") the
control-dispatcher agent is a core-pack (bound) agent named
`core.control-dispatcher`. Two regressions follow:

1. **Resolution:** `graphroute.ControlDispatcherBinding` resolves the dispatcher by
   the bare constant `ControlDispatcherAgentName = "control-dispatcher"`. For a
   bound agent, `AgentMatchesIdentity` refuses the V1 `dir+name` fallback and the
   bare-name scan is ambiguous across the per-rig dispatcher fleet, so
   `ResolveAgent` misses. Resolution survives only via the
   `configuredControlDispatcherForScope` `Dir==rigContext` fallback; a city whose
   routed scope has no `Dir`-matching dispatcher gets
   `control-dispatcher agent "control-dispatcher" not found` on **every** v2-formula
   cook (e.g. the default `mol-focus-review`).

2. **Routing/claim mismatch:** where the scope fallback *does* resolve, it returns
   the **rig-scoped** dispatcher, so control beads are stamped
   `gc.run_target=<rig>/core.control-dispatcher`. But the core pack ships a
   **singleton** and only the city-level `core.control-dispatcher` session runs; its
   claim query (`workflowServe…`) claims `core.control-dispatcher` + bare
   `control-dispatcher` + legacy `workflow-control`, **not**
   `<rig>/core.control-dispatcher`. Rig-scoped control beads therefore strand
   (routed-but-unconverted); the routed-bead nudger re-wakes the dispatcher each
   tick and it sits active-but-idle.

### Affected paths

- `internal/config/config.go:37` — `ControlDispatcherAgentName = "control-dispatcher"` (bare).
- `internal/config/config.go:158` — `AgentMatchesIdentity` refuses V1 fallback for bound agents.
- `internal/graphroute/graphroute.go:257-285` — `ControlDispatcherBinding` / `configuredControlDispatcherForScope` (Dir-exact).
- `cmd/gc/dispatch_runtime.go:786-819` — claim query handles bare/legacy/city-qualified but not `<rig>/`-qualified.
- `internal/bootstrap/packs/core/agents/control-dispatcher/agent.toml`, `…/core/pack.toml` — singleton dispatcher shipped by the core pack.

### Impact

City-wide for cities whose `[agent_defaults].default_sling_formula` is a v2 formula
(`mol-focus-review`). Symptom A: every default-formula cook errors "not found"
(cities without a per-scope dispatcher). Symptom B: control-dispatcher session sits
active-but-idle while control beads strand (cities with rig-scoped dispatchers).
Workaround in the field: `gc sling … --no-formula` (degraded — drops the
mol-focus-review structure).

### Expected

A v2-formula cook in any rig resolves the control-dispatcher and routes its control
beads to the dispatcher session that actually runs.

### Repro

In a multi-rig city on a build ≥ 9fa6b7fec with
`[agent_defaults].default_sling_formula = "mol-focus-review"`, sling the default
formula to a rig pool. Either the cook errors `control-dispatcher agent
"control-dispatcher" not found`, or it succeeds but the resulting control beads are
never claimed (dispatcher active-but-idle).

### Related

- formulas-v2 deprecation #2941 — `mol-focus-review` still references removed
  `vars.issue` / `steps[].description: issue` (separate migration, not this bug).

---

## DRAFT — PR body (the forward fix)

**Title:** fix(control-dispatcher): namespace-aware resolution + singleton-consistent routing

**Body:**

### Problem

After `9fa6b7fec` the control-dispatcher is a bound core-pack agent
(`core.control-dispatcher`). Bare-name resolution in
`graphroute.ControlDispatcherBinding` no longer matches it, and where the
`Dir==rigContext` scope fallback resolves, it returns a **rig-scoped** dispatcher
whose qualified name is stamped onto control beads — but only the city-level
singleton session runs and never claims `<rig>/core.control-dispatcher` routes.
(Full root cause: see issue.)

### Fix

1. **Singleton-consistent routing (cure for the wedge).** In
   `ControlDispatcherBinding`, prefer the **city-level singleton** dispatcher
   (`Dir==""`, the running session) for every scope; only fall back to a rig-scoped
   instance when no city-level dispatcher exists. This makes the stamped route
   (`core.control-dispatcher`) match the running session's claim query, so control
   beads stop stranding.

2. **Namespace-aware resolution (cure for "not found").** Resolve the dispatcher by
   its canonical identity even though it is pack-bound — resolve the unique
   `IsDeterministicControlDispatcher` agent regardless of binding (or resolve via the
   binding-qualified `core.control-dispatcher`). This restores resolution in cities
   that lack a `Dir`-matching per-scope dispatcher.

3. **Claim-query completeness (defensive).** If rig-scoped control lanes are
   intended, extend the workflow-serve claim query
   (`cmd/gc/dispatch_runtime.go`) to also claim `<rig>/core.control-dispatcher`
   (mirroring the existing bare/legacy/city-qualified handling). Otherwise, stop
   materializing per-rig dispatchers and route every scope to the singleton (matches
   the pack's `max_active_sessions = 1` intent). **Maintainer call:** is the
   intended model one city-singleton dispatcher, or per-rig control lanes? Fix (1)
   assumes singleton (matches the shipped pack); (3) is the alternative if per-rig
   is intended.

### Test plan

- Unit: `ControlDispatcherBinding` resolves to the city-singleton for `rigContext=""`
  **and** a non-empty `rigContext`, in a city with only the core-pack dispatcher and
  in a city with a per-rig fleet. Add a case asserting the **stamped route equals a
  target the running session's claim query matches**.
- Unit: bare-name resolution of the bound dispatcher succeeds (regression for the
  `AgentMatchesIdentity` bound-agent path).
- Integration: default-`mol-focus-review` cook in a rig pool → cook succeeds **and**
  the control bead is claimed by the dispatcher session (no strand).
- Regression: existing `cmd/gc/cmd_convoy_dispatch_test.go` / `graph_dispatch_mem_test.go`
  control-dispatcher cases stay green.

### Notes

- Separate follow-up: migrate `mol-focus-review` off removed `vars.issue` /
  `steps[].description: issue` (#2941).

---

## Fix-PR sling spec (for the mayor / gascity-maintenance-pl to dispatch)

- **Target:** a gascity polecat (gascity rig has no live workers — route to `polecat`
  per the city's gascity dispatch convention).
- **Scope:** `internal/graphroute/graphroute.go` (primary), plus the resolver change
  in `internal/agentutil/resolve.go` / `internal/config/config.go`, and (if per-rig
  is intended) `cmd/gc/dispatch_runtime.go`. Tests ship in the same PR.
- **Gate:** PR builds locally; **merge is per-action gated to Stephanie.**

---

## FINALIZED — as-implemented (branch-ready 2026-06-26)

**Branch:** `fix/control-dispatcher-namespace` (off origin/main) in worktree
`/home/ds/gascity-worktrees/control-dispatcher-namespace-fix`. **Not pushed; no PR opened** —
maintainer (mayor) handles the gated GitHub actions after reviewing the diff.

**Commits:**
- `9370d148a` fix(control-dispatcher): namespace-aware singleton-consistent resolution in graphroute
- `804feb38f` fix(control-dispatcher): apply singleton preference to attempt-time route

**What shipped (refines the draft Fix above):**
- New `config.PreferredDeterministicControlDispatcher(cfg, rigContext) (Agent, bool)` — selects the
  control-dispatcher **binding-agnostically** via `IsDeterministicControlDispatcher` (not bare-name
  string match, which the bound `core.control-dispatcher` defeats) and **prefers the city-level
  singleton** (`Dir == ""`, the session that actually runs given `max_active_sessions=1`), falling
  back to a rig-scoped instance only when no city-level deterministic dispatcher exists. No import
  cycle (`internal/config` is imported by both consumers, imports neither).
- `internal/graphroute/graphroute.go` `ControlDispatcherBinding` and `internal/dispatch/control.go`
  `controlDispatcherTargetForExecutionTarget` (the **attempt-time** path: Check/Retry/Fanout/
  ScopeCheck/WorkflowFinalize/Ralph) both delegate to the helper. This cures BOTH the `"control-
  dispatcher agent not found"` instantiation failure AND the stranded-control-bead (both paths now
  stamp the singleton `core.control-dispatcher` route the running session claims). String fallbacks
  (`<rig>/control-dispatcher`, bare) preserved for configs with no deterministic dispatcher.
- Draft Fix item 3 (claim-query completeness / per-rig lanes) was **not needed** — routing every
  scope to the singleton makes the existing claim query match; the **maintainer call** resolves to
  *singleton* (matches the shipped pack's `max_active_sessions = 1`).

**Tests (ship in the same commits):** `TestPreferredDeterministicControlDispatcher` (table-driven,
5 cases); graphroute binding tests (city-singleton-preference + city-only-bound + rig-scoped
fallback, both `rigContext=""` and non-empty, asserting the singleton qualified name + a regression
guard that `AgentMatchesIdentity` rejects the bare name for the bound agent); attempt-time
integration test asserting `gc.routed_to == "core.control-dispatcher"` (was the strand). Updated 2
convoy-dispatch tests that encoded the pre-fix strand.

**Verification (independently re-run):** `make build` PASS; `go test ./internal/config/...
./internal/graphroute/... ./internal/dispatch/...` PASS; `go test ./cmd/gc/...` PASS (full suite,
agent-run); gofmt / `go vet` / `golangci-lint fmt` clean; pre-commit `go vet ./...` passed.
