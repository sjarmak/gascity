# Architecture Review — tom-swe (2026-07-10)

## Executive summary

tom-swe is a well-factored Claude Code plugin (~8.6K source / 14.2K test TS lines) with a flat module graph, a typed Zod core, and unusually good in-code rationale. The two structural risks are operational, not conceptual: the executed artifact is a checked-in `dist/` with **zero CI verifying it matches source**, and the Stop hook (`stop-analyze.ts`) has become the system's god orchestrator — 17 of ~25 production modules imported, top churn (20 changes since April), and a 1,967-line test file. Two design-vs-reality drifts persist from the original Feb-2026 PRD build: a dead 5-tool agent layer (`tom/agent/`) whose shipped agent prompt documents tools that cannot be called, and a global/project/merged scope + `projectOverrides` machinery that no production path ever writes. Top-ROI moves: (1) add build/test/dist-parity CI, (2) decompose the Stop pipeline into stages, (3) delete or rewire the dead agent-tool concept.

Ranked findings follow; rank = leverage per effort, discounted by risk.

---

## Architecture map (orientation)

- **Entry points**: 4 hooks + 6 skills, each esbuild-bundled self-contained into `dist/tom/{hooks,skills}/*.js`; `hooks/hooks.json` executes `dist/` directly via `${CLAUDE_PLUGIN_ROOT}`. `dist/` (4.8 MB) is committed; CLAUDE.md instructs "rebuild after changing any hook source".
- **Core**: `schemas.ts` (Zod strictObject, the single typed contract for all 3 memory tiers) ← `memory-io.ts` (atomic JSON store under `~/.claude/tom/`, sidecar-fold read) ← everything else. Graph is flat and acyclic; `routing.ts` (telemetry log + model routing) sits above IO.
- **LLM boundary**: exactly two headless `claude -p` spawn sites — `llm-analyze.ts` (session extraction, async) and `promotion-gate.ts` (derivability judge, sync). Consultation (`consult.ts` + `ambiguity.ts` + `bm25.ts`) is fully local.
- **Write topology**: hooks write only the `global` scope; Tier 3 is rebuilt from all Tier 2 on every qualifying Stop (idempotent-by-rebuild is the central design invariant).
- **History**: built by an autonomous PRD loop 2026-02 (28 commits), remediated 2026-06/07 (78 commits, worktree-agent merges). Churn concentrates in `tom/hooks/stop-analyze.ts` (20), `tom/llm-analyze.ts` (12), `tom/hooks/session-start.ts` (10).

## Major strengths (protect these)

- `schemas.ts` as the single typed core; strict schemas with explicit optional-for-legacy fields and read-boundary self-healing (`withoutDeprecatedClusters`, memory-io.ts:238-252).
- Rebuild-from-Tier-2 idempotency (rebuild.ts) — eliminates a whole class of double-count bugs; the sidecar O_APPEND design (schemas.ts:27-53) is a correct fix for concurrent capture loss.
- Telemetry discipline: versioned schema, no silent failures, every fallback logged with reason.
- LikeC4 architecture model with a CI drift guard on source links (`.github/workflows/likec4-pages.yml`, `architecture/site/check-links.mjs`).
- Decision rationale living in code comments at the exact site of each tradeoff.

---

## Ranked findings

### 1. No build/test/dist-parity CI — the executed artifact has no drift guard
- **Evidence**: `.github/workflows/` contains only `likec4-pages.yml`. `hooks/hooks.json` runs `node ${CLAUDE_PLUGIN_ROOT}/dist/tom/hooks/*.js`; `dist/` (4.8 MB) is committed and must be manually rebuilt (CLAUDE.md: "rebuild after changing any hook source"). No workflow runs `npm run typecheck`, `npm test`, or verifies `dist == build(source)`.
- **Why it matters**: `dist/` is what users actually execute; source is what gets reviewed. The repo is developed by autonomous worktree agents (merge commits `worktree-agent-*` throughout `git log`) — exactly the workflow where a forgotten rebuild ships silently divergent runtime behavior, and where a red test on main goes unnoticed. Every other finding's fix is only safe to land with this net in place. One workflow (`npm ci && npm run typecheck && npm test && npm run build && git diff --exit-code dist/`) closes it.
- **Effort**: S. **Risk**: minimal, fully reversible.

### 2. Stop-hook god orchestrator — `stop-analyze.ts` is the system's choke point
- **Evidence**: `tom/hooks/stop-analyze.ts` — 702 lines, imports 17 internal modules (lines 16-47), 7 inline sequential steps in one function (`analyzeCompletedSession`, lines 186-654: usage capture, debounce, watermark, O_EXCL lock, LLM extract + preserve-on-failure, rebuild + reconcile, correction/follow-through telemetry, promotion, snapshot, prune + rotation, index rebuild). Highest churn in the repo (20 changes since April; next is 12). Its test file is the largest file in the repo (1,967 lines).
- **Why it matters**: every new capability (watermark, in-flight lock, vocabulary echo, follow-through, snapshots, rotation — all added post-April) landed inside this one function; each addition raises the collision surface for the parallel-worktree workflow this repo uses. Ordering invariants (`carryPromotedFlags` → `reconcilePreferenceCategories` → `runPromotion`, documented at lines 95-98) live in comments, not structure. Decomposing into a pipeline of stages (`(ctx) => ctx`, each with its own telemetry and test file) encodes the ordering, splits the 2K-line test, and makes the next ten features land in new files instead of this one.
- **Effort**: M. **Risk**: moderate — the ordering constraints are subtle but explicitly documented; the pipeline order becomes their executable form. Land after finding 1.

### 3. Dead agent-tool layer — `tom/agent/` implements 5 tools with no caller, and the shipped agent prompt documents them
- **Evidence**: `tom/agent/tools.ts` (325 lines) exports `searchMemory`, `readMemoryFile`, `analyzeSession`, `initializeUserProfile`, `giveSuggestions` plus invocation-budget machinery (`agent/config.ts`); grep shows zero production consumers for all five — only `buildMemoryIndex` is used (by `stop-analyze.ts:27`, `tom-forget-export.ts:20`). `agents/tom-agent.md` instructs the shipped `tom-swe:tom-agent` to use "exactly 5 tools" that do not exist in its runtime (a Claude Code markdown agent cannot call these TS functions). README confirms the actual design: consultation is fully local; `models.consultation` is "reserved for future use".
- **Why it matters**: this is the clearest intended-vs-actual drift — the original spawned-sub-agent design was replaced by local BM25 consult, but the concept survived as ~780 lines of source+test dead weight and a user-facing agent that promises capabilities it lacks. Deleting a concept beats maintaining an illusion: move `buildMemoryIndex` next to `bm25.ts`, remove `tom/agent/` + `tools.test.ts`, and either rewrite `agents/tom-agent.md` to use real tools (Read/Grep over `~/.claude/tom/`) or drop the agent from the plugin.
- **Effort**: S. **Risk**: low (dead code); the agent-md rewrite needs a one-time product decision.

### 4. Speculative dual-scope machinery — `project` scope and `projectOverrides` have no production writer
- **Evidence**: `memory-io.ts` threads `scope: 'global' | 'project'` through all 6 read/write pairs plus a `'merged'` union in `readUserModel` (lines 271-315); grep shows hooks write only `'global'` (`stop-analyze.ts:442,483,565`). The only `'project'` writers are the dead agent tools (finding 3) and `tom-forget-export` (deletion path). `UserModel.projectOverrides` (`schemas.ts:169`) is spread-copied in `rebuild.ts:31,86`, `aggregation.ts:199`, and merged in `memory-io.ts:303-306`, but nothing ever populates it — so `'merged'` always equals `'global'` in practice, and `projectTomDir()` carries a hidden `process.cwd()` ambient dependency.
- **Why it matters**: every module pays a scope parameter and a merge semantics tax for a feature that doesn't exist; readers of `session-start.ts:138` (`readUserModel('merged')`) reasonably assume per-project models are live. Either delete the scope axis (keep fields optional for legacy parse — the store self-heals on write) or make project scope real; today it is the worst option, half-built.
- **Effort**: S-M (delete) / L (make real). **Risk**: low-moderate — confirm no external consumer of project-scope store files before deleting.

### 5. Stringly-typed telemetry contract, hand-rolled envelope at 26 call sites
- **Evidence**: 26 `logUsage({...})` production sites; 28 occurrences of the literal `timestamp: new Date().toISOString(),` envelope, most with `model: NO_MODEL, tokenCount: 0`. Operation names are free strings ("vocabulary documented in README"); external consumers (the mem-eval harness) join on `detail` fields via the untyped accessor `usageDetailStringArray` (`routing.ts:146-153`).
- **Why it matters**: usage.log is an external contract (eval harness, tom-status, effectiveness rollups) enforced only by convention. A discriminated event union (`type TomEvent = { op: 'session-analysis', detail: {...} } | ...`) plus an `emit()` that stamps the envelope makes the vocabulary compiler-checked, shrinks every site, and turns README's operation table into generated truth. Directly de-risks findings 2's decomposition (stages keep their exact event shapes).
- **Effort**: S-M. **Risk**: low — wire format unchanged, versioned schema already in place.

### 6. Telemetry/paths layering knot — the IO layer cannot log, by its own admission
- **Evidence**: `memory-io.ts:80-82`: "Corrupt lines are skipped with a stderr note (routing.ts imports this module, so logUsage would be an import cycle)". `routing.ts` is ~80% telemetry (logUsage, rotation, readUsageLog, prefKeyForTelemetry) and ~20% model routing, and imports `memory-io` only for `globalTomDir`.
- **Why it matters**: sidecar corruption — a real data-integrity event — bypasses the durable log, violating the repo's own no-silent-failure telemetry rule, purely because of module placement. Extracting `paths.ts` (the 8 path helpers already re-exported at `memory-io.ts:331-340`) and renaming routing's telemetry half breaks the cycle, lets IO log, and gives `routing.ts` back its name.
- **Effort**: S. **Risk**: low; mechanical.

### 7. Headless-claude spawn pattern duplicated across the two LLM sites
- **Evidence**: `llm-analyze.ts:349-425` (async `spawn`, prompt-over-stdin for E2BIG, `TOM_SWE_INTERNAL=1`, timeout, JSON-wrapper parse, token extraction) vs `promotion-gate.ts:113-120` (`spawnSync`, same stdin/E2BIG/env/timeout/parse concerns; already imports `extractTokensUsed` from llm-analyze).
- **Why it matters**: two instances is below rule-of-three, but the third is announced — README reserves `models.consultation` "for future use". Extracting `spawnHeadlessClaude(prompt, model, opts)` before that third site prevents a security-relevant guard (the recursion env, the zero-tools least-privilege flags) from being re-hand-rolled inconsistently.
- **Effort**: S. **Risk**: low. Do opportunistically or when the third call site appears.

---

## Leave unchanged (reviewed, deliberately not findings)

- **`ambiguity.ts` keyword heuristics**: a ZFC purist would flag keyword meaning-detection, but this runs synchronously in UserPromptSubmit with a <50 ms budget and no model available; the tradeoff is documented at the site. Correct as-is.
- **Rebuild-every-Stop over incremental aggregation**: looks expensive, is the load-bearing idempotency fix (confidence inflated 2-3x under incremental). Keep.
- **Sidecar + stub Tier 1 format**: solves a measured concurrent-append loss (10 captures → 4-5 kept). Keep.
- **Checked-in `dist/`**: correct for a plugin whose users don't `npm install`; the problem is the missing parity guard (finding 1), not the choice.
- **Mixed `./x.js` / `./x` import specifiers**: cosmetic under esbuild bundling; not worth a finding.
- **Test volume (14.2K lines, 1.6:1 test:src)**: healthy; the only issue is the monolithic `stop-analyze.test.ts`, addressed by finding 2.
