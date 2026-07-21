# Architecture Review: gascity-dashboard

Date: 2026-07-10 · Scope: /home/ds/gascity-dashboard (main tree only; embedded worktrees excluded) · Read-only

## Executive summary

The system is a three-plane npm-workspaces app (shared wire contract → Express security/proxy backend → React SPA) with an unusually disciplined security posture and a genuinely good module-registry design — but that registry is only one-third adopted, so every new view still edits four hand-maintained lists. The attention subsystem is the main structural inversion: a 1,097-line core file hardcodes all seven domains and imports internals of the opt-in `maintainer` module, defeating the module boundary the rest of the codebase built. The hottest data path (run-summary) is protected by a backend proxy cache whose validity depends on magic numbers duplicated by hand between frontend and backend with no shared constant or drift test. `shared/` has drifted from "wire-shape types" into a 4k-line frontend domain engine whose barrel is the single most-churned file in the repo, and the typecheck pipeline compensates with a mid-pipeline `build:shared` because there are no TS project references. Top ROI: finish the registry migration, SSOT the cache contract, and re-invert the attention dependency — all reversible, none a rewrite.

## Architecture summary

Three workspaces: `shared/` (wire DTOs, run/convoy/link domain derivation, generated OpenAPI supervisor client under `shared/src/generated/gc-supervisor-client/`), `backend/` (Express, 127.0.0.1 posture: host-header allowlist, origin check, CSRF, default-deny read-only proxy mode), `frontend/` (React + Vite + Tailwind, "Reading Room" DESIGN.md visual contract).

Three request planes, deliberately split (`backend/src/app.ts:58-101`):
1. `/gc-supervisor/*` — transport proxy to the gc supervisor with hop-by-hop/cookie stripping, read-only allowlist, and a TTL single-flight cache for two expensive city-wide reads.
2. `/api/city/:cityName/*` — per-city request plane; `cityDispatch` middleware resolves a `CityRuntime` (own `GcClient`, own samplers, own module routers) so cities cannot bleed in-flight coalescing.
3. `/api/*` — host-global dashboard-local routes (git, builds, health samplers, client-error telemetry).

The frontend mirrors this with two typed clients: `frontend/src/api/client.ts` (backend-owned DTOs from the shared barrel) and `frontend/src/supervisor/client.ts` (generated client via the proxy), plus SSE hooks. A module system (`shared` ViewDescriptor/BackendModule contract, backend `bind<D>()` existential wrapper in `backend/src/views/types.ts`, frontend `views/registry.ts` + pure `resolve.ts`) makes views registry-driven with core/firstParty opt-in semantics — currently 3 views registered (activity, health, maintainer) while 7+ routes remain hand-mounted.

## Major strengths (protect these)

- **Security architecture**: default-deny read allowlist (`backend/src/routes/supervisor-read-allowlist.ts`), untrusted-supervisor-path discipline (`backend/src/city/runtime.ts` cityPath vs cityDataDir separation), header stripping in the proxy. Coherent and commented with intent.
- **Module contract design**: `bind<D>()` Deps erasure with no `as never`, single-function enable-set resolution (`views/enabled.ts`) consulted by boot log, wire mirror, and mount loop alike.
- **City isolation**: per-city `GcClient` + runtime prevents cross-city coalescing bugs by construction.
- **Wire-contract typing**: workspace-shared DTOs turn drift into compile errors; OpenAPI client generation has a `--check` CI gate.
- **Degradation discipline**: `coreRead.ts` bounded retry, partial-list signals, per-view error boundaries keyed to reset correctly (`App.tsx:39-52`).

## Ranked findings (leverage ÷ effort, risk-discounted)

### 1. Finish the view-registry migration; delete the four hand-maintained route lists
- **Evidence**: `frontend/src/components/Header.tsx:26` — "Hand-maintained routes for the views that PR-A has NOT yet ported to the modular registry"; `EXPLICIT_ROUTES` (6 entries, Header.tsx:31-43) + `NAV_ATTENTION_DOMAINS` (Header.tsx:45-53) + 9 hardcoded lazy imports and `<Route>` elements in `frontend/src/App.tsx:21-141` + `ROUTES` in `scripts/snap.mjs:24`. Registry (`frontend/src/views/registry.ts`) holds only 3 of ~10 views.
- **Why it matters**: the registry was designed as the single design-review checkpoint for new views (its own header says so). Half-adopted, it is the worst of both worlds: every route addition/rename touches App.tsx, Header (twice), and the snap harness, and readers must understand two registration mechanisms. Migrating Agents/Beads/Runs/Convoy/Mail/AmbientHome as `core` descriptors deletes three hand lists outright and makes `nav`, attention-domain binding, and route mounting one edit. Unblocks every future view, and finding #2 composes onto the same descriptor.
- **Effort**: M (mechanical, one view at a time; each step shippable). **Risk**: low — pure-function resolvers already tested; snap harness pins visual parity.

### 2. Frontend↔proxy cache contract is duplicated magic numbers with no drift test
- **Evidence**: `backend/src/routes/supervisor-transport-proxy.ts:66` `MOLECULE_HISTORY_FETCH_LIMIT = '500'` and exact-param-set matchers (`MOLECULE_HISTORY_PARAMS`, `CACHEABLE_CITY_WIDE_READS`) hand-mirror `frontend/src/supervisor/runSummary.ts:39` `RUNS_FETCH_LIMIT = 500` and its query shapes. No shared constant; no test ties the two sides together.
- **Why it matters**: the cache exists because these reads are a ~7-10s, 340k-row upstream scan re-fired on every SSE bead burst (proxy comments cite live timings). The match is deliberately EXACT for safety, which means any frontend change (limit 500→600, added param) silently disqualifies the cache — no error, just the supervisor melting again under the run-summary subscription. This is the highest-traffic path in the system guarded by convention alone.
- **Fix shape**: hoist the query shape (params + limit) into `shared/` as the SSOT both sides consume; add a test asserting the frontend's actual request matches the proxy's cacheable spec.
- **Effort**: S. **Risk**: low; fully reversible.

### 3. Attention subsystem hardcodes all domains and inverts the core→module boundary
- **Evidence**: `frontend/src/attention/registry.ts` (1,097 lines) contains `derive*Attention` for all 7 domains and imports maintainer-module internals at `registry.ts:29-30` (`../views/modules/maintainer/attentionKeys`, `needsYou`); `frontend/src/attention/compose.ts:3-11` hardcodes `'maintainer'` in `ATTENTION_DOMAINS`; `frontend/src/attention/liveContributors.ts:78` `enabledModules?.includes('maintainer')`; `frontend/src/views/resolve.ts:18` imports `NEEDS_YOU_VIEW_PARAM` from the module.
- **Why it matters**: the module system's whole point is that `firstParty` modules are opt-in and core never depends on them — the backend side honors this (registry + `bind()`), but the frontend attention layer reaches into the maintainer module from four core files. A second firstParty module must edit `compose.ts`, `liveContributors.ts`, and grow the 1,097-line registry further; the "disabled module" story is faked by string checks instead of absence. Adding an `attention` contributor slot to `ViewDescriptor` (fact-fetcher + item-deriver per module/domain) re-inverts the dependency and decomposes registry.ts along lines it already has (one `derive*` per domain).
- **Effort**: M. **Risk**: medium — attention is the operator's alarm surface; mitigated by the existing 1,330-line `registry.test.ts` and per-domain test files.

### 4. No TS project references — typecheck pipeline hand-builds shared dist mid-run
- **Evidence**: root `package.json` `typecheck:src` = `shared typecheck && build:shared && backend typecheck && frontend typecheck`; no `composite`/`references` in any of `backend/tsconfig.json`, `frontend/tsconfig.json`, `shared/tsconfig.json`; backend/frontend resolve `gas-city-dashboard-shared` via `dist/`.
- **Why it matters**: every backend/frontend typecheck (local, CI, editor cold start) depends on a *built artifact* of shared, creating a stale-dist failure class and serializing the pipeline. Project references (or `exports` pointing at source with bundler resolution for the frontend) let `tsc -b` track staleness and drop the explicit mid-pipeline build. Pays out on every single typecheck run and every wire-shape iteration — the most frequent loop in the repo (shared churn, see #5).
- **Effort**: S-M. **Risk**: low; config-only, reversible.

### 5. `shared/` has drifted from wire contract to frontend domain engine; barrel is the #1 churn file
- **Evidence**: `shared/src/runs/phaseMapping.ts` (974 lines), `runs/summary.ts` (608), `links/`, `convoy/projection.ts` — view-model derivation consumed almost exclusively by the frontend (backend's non-test shared imports are the views contract, constants, and DTO types). `shared/src/index.ts` is the top churn hotspot (73 touches in 3 months, ~2x the next file), a 60-line mixed value/type barrel that every DTO or helper addition edits.
- **Why it matters**: "shared = wire shapes" (the repo's own framing) no longer describes reality, so placement decisions are ad hoc — the next run-derivation helper could defensibly land in three places, and each shared edit taxes both downstream workspaces (see #4). Two cheap moves: (a) write the boundary rule down (ADR: shared = wire shapes + isomorphic derivations *by name*, or move `runs/` view-model code under `frontend/src/domain/`), (b) split the barrel into per-domain subpath exports (the `./gc-supervisor` and `./fixtures/test-city` subpaths already prove the pattern) so churn localizes.
- **Effort**: M (rule + barrel split S; any relocation M). **Risk**: low-medium — import-path churn only; behavior unchanged.

### 6. Read-only allowlist is manually synced to the frontend read surface with no conformance test
- **Evidence**: `backend/src/routes/supervisor-read-allowlist.ts:6-10` — "see the `getV0*` / SSE calls in `frontend/src/supervisor/*`. A new read view must be added here to work under read-only mode". 19 templates maintained by eye against ~25 generated-client call sites in `frontend/src/supervisor/client.ts`.
- **Why it matters**: fail-closed is the right posture, so this breaks safe — but it breaks *in the read-only deployment only*, the one least exercised in dev. A test that enumerates the generated operations the frontend imports and asserts each is either allowlisted or on an explicit deny list (with the side-effecting-GET rationale attached) turns a deploy-time surprise into a compile-time diff, and preserves the deliberate exclusions (e.g. `agent/{base}/prime`).
- **Effort**: S. **Risk**: low.

### 7. Playwright harness re-hand-rolled across six scripts (~2.4k lines)
- **Evidence**: `scripts/snap.mjs` (291), `snap-formula-run-detail.mjs` (949), `snap-test-city.mjs` (286), `snap-peek.mjs` (253), `snap-beads-board.mjs` (72), `dashboard-tester.mjs` (492) each own chromium launch/context/viewport/theme-injection/console-error-collection boilerplate; three are CI (`browser:test`).
- **Why it matters**: the snap harness is this repo's visual-contract enforcement (DESIGN.md), so it will keep growing per-view. A small `scripts/lib/harness.mjs` (launch, themed context, error drain, pass/fail reporter) makes the next per-view snap script ~50 lines instead of ~300 and fixes error-collection semantics in one place. Rule-of-three is well past satisfied.
- **Effort**: S. **Risk**: low — dev/CI tooling only.

### 8. Working-tree hygiene: 25+ untracked agent worktrees inside the repo root
- **Evidence**: `git status --porcelain` shows 28 untracked `gascity-dashboard-*` / `wt-*` / `worktrees/` directories at the repo root, not gitignored (`git check-ignore` exit 1). They also pollute naive sweeps (an unscoped `wc -l` counts every file 6-7x).
- **Why it matters**: one `git add .` away from committing whole worktrees; every agent operating here pays a filtering tax; tooling (grep/knip/coverage) must know to exclude them. `.gitignore` entries plus a relocate-outside-root convention (matching the gas-city `worktrees/` pattern) removes the trap.
- **Effort**: S. **Risk**: none (ignore-file only).

## Leave unchanged

- **Three-plane request architecture** (`/api`, `/api/city/:cityName`, `/gc-supervisor`): looks like duplication, is actually a trust-boundary decision (host-local CLI shelling vs supervisor-owned resources) — documented at each mount point.
- **Per-city `CityRuntime` + own `GcClient`**: prevents cross-city coalescing bugs; keep.
- **Generated OpenAPI client pipeline** with `openapi:gc-supervisor:check` drift gate: working as designed.
- **Backend maintainer module internals** (`triage.ts`, classifier, sling-dispatch): large but cohesive, well-tested, and correctly isolated behind the module contract.
- **Test co-location and volume** (~1:1 test:source in hot areas): a strength, not a smell.
