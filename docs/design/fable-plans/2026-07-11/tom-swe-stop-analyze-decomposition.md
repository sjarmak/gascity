# tom-swe: stop-analyze.ts decomposition plan

- **Date**: 2026-07-11. **Repo**: `/home/ds/projects/tom-swe` @ `5008a10`.
- **Source review**: `/home/ds/gas-city/docs/arch-reviews/2026-07-10/tom-swe.md`, findings 2 (god orchestrator), 5 (stringly telemetry), 6 (paths/telemetry cycle), 7 (duplicated headless spawn).
- **Related beads**: `tom-swe-dhb.1` (build/test/dist-parity CI, P1, open), `tom-swe-dhb.2` (delete dead agent layer, P2, open).
- **Standing constraint**: hooks execute committed `dist/` (`hooks/hooks.json` runs `dist/tom/hooks/*.js`). Until `tom-swe-dhb.1` lands, **every PR below must ship rebuilt `dist/` in the same commit** (`npm run typecheck && npm test && npm run build`, then commit the `dist/` diff). Each PR section states which bundles change.

## 1. Current state (verified against source)

`tom/hooks/stop-analyze.ts` is 702 lines, 17 internal-module imports (lines 16-47), one 469-line async function `analyzeCompletedSession` (lines 186-654) containing the whole Stop pipeline, plus `reconcilePreferenceCategories` (lines 100-169), `readRawSessionLog` (66-68), and `main` (658-697). Its test file `tom/hooks/stop-analyze.test.ts` is 1,969 lines with only 3 describes (`readRawSessionLog`, `analyzeCompletedSession` with ~50 its, `main` with 8 its). 17 of the repo's 26 production `logUsage({...})` sites live in this file, all hand-rolling the `timestamp/model: 'none'/tokenCount: 0` envelope.

None of the file's exports (`readRawSessionLog`, `reconcilePreferenceCategories`, `AnalysisResult`, `analyzeCompletedSession`) have production consumers outside the file and its test, so the decomposition is free to move and un-export symbols.

Inline sequence today (line ranges to be moved verbatim, comments included):

| # | Concern | Lines |
|---|---------|-------|
| a | Host-session usage capture from transcript | 193-225 |
| b | Debounce (90s mtime) + watermark (userMessage count) + O_EXCL in-flight lock | 227-329 |
| c | Tier 2 extraction: vocabulary anchoring, LLM call, truncation + echo telemetry, preserve-on-failure, watermark stamp, Tier 2 write, lock release | 331-456 |
| d | Tier 3 rebuild + `carryPromotedFlags` + `reconcilePreferenceCategories` + write; correction + follow-through telemetry | 458-553 (+ 100-169) |
| e | Promotion (derivability gate closure, `runPromotion`, conditional write, telemetry), error-isolated | 555-592 |
| f | Post-session user-model snapshot, error-isolated | 594-616 |
| g | Prune + usage-log rotation (error-isolated), then BM25 index rebuild | 618-645 |

## 2. Target layout

```
tom/stop-pipeline/
  types.ts                 # StopContext, AnalysisResult, GateResult, AnalysisLock, ExtractResult, AggregateResult
  capture-usage.ts         # S1  captureSessionUsage()
  gate.ts                  # S2  gateAnalysis(), ANALYSIS_DEBOUNCE_MS
  extract.ts               # S3  extractTier2(), anchoringVocabulary()
  aggregate.ts             # S4  aggregateTier3(), reconcilePreferenceCategories()
  promote.ts               # S5  promotePreferences()
  snapshot.ts              # S6  snapshotUserModel()
  housekeep.ts             # S7  housekeepStore()
  *.test.ts                # one test file per step (carved from stop-analyze.test.ts)

tom/hooks/stop-analyze.ts  # thin orchestrator: main() + analyzeCompletedSession() as a
                           # typed sequence over S1..S7 (~150 lines, ~9 imports)
tom/hooks/stop-analyze.test.ts  # orchestrator-level only: main describe + 2-3 integration its

tom/paths.ts               # 8 path helpers moved out of memory-io.ts (cycle break, finding 6)
tom/usage-log.ts           # telemetry IO half of routing.ts + TomEvent union + emit() (finding 5)
tom/routing.ts             # shrinks to model routing only: OperationType, getModelForOperation
```

Location rationale: the steps are domain logic, not hook plumbing, so they live in `tom/stop-pipeline/`, not `tom/hooks/`. esbuild bundles per entry point (`esbuild.config.mjs`), so only the `dist/tom/hooks/stop-analyze.js` bundle picks up step-module changes; core-module changes (paths, usage-log, memory-io, routing) rebundle all 10 entry points.

## 3. Step interface

Deliberate deviation from the review's generic `(ctx) => ctx` sketch: each step gets an **explicit typed signature**, and the data flow between steps is carried by result types (`GateResult` → `ExtractResult` → `AggregateResult`). A uniform ctx bag would let a mid-tier worker wire steps in the wrong order and still compile; explicit input types make the documented ordering invariants (stop-analyze.ts:95-98, 458-509) compile errors instead of comment lore. The orchestrator is the only place that sees more than one step.

All steps emit telemetry through `emit(event: TomEvent)` (section 4), never raw `logUsage`.

### S1 — capture-usage (moves lines 193-225)

```ts
export function captureSessionUsage(
  sessionId: string,
  cwd: string,
  transcriptPath: string | undefined
): void
```

- No-op when `transcriptPath` is undefined. Emits `session-usage` (with cwd/gitBranch join fields read via `readSessionLog(sessionId, 'global')`) or `session-usage-error`. Never throws; must run before any gate early-return (invariant: usage lands even when analysis skips or fails).
- Deps: `transcript-usage.readTranscriptUsage`, `memory-io.readSessionLog`, `usage-log.emit`.

### S2 — gate (moves lines 227-329, plus `readRawSessionLog` 66-68 and `ANALYSIS_DEBOUNCE_MS`)

```ts
export interface AnalysisLock {
  /** closeSync + unlink, swallowing already-swept-stale (current lines 448-455). */
  release(): void
}
export type GateResult =
  | { readonly kind: 'no-session-log' }     // → orchestrator returns success:false
  | { readonly kind: 'debounced' }
  | { readonly kind: 'no-new-evidence' }
  | { readonly kind: 'in-flight' }
  | {
      readonly kind: 'proceed'
      readonly sessionLog: SessionLog
      readonly priorModel: SessionModel | null   // Tier 2 as persisted (watermark source)
      readonly userMessageCount: number
      readonly lock: AnalysisLock                // O_EXCL already held
    }
export function gateAnalysis(sessionId: string): GateResult
```

- Emits `analysis-debounced`, `analysis-skipped-no-new-evidence`, `analysis-in-flight`. Encapsulates the Tier 1 read, the mtime debounce, the userMessage watermark, and O_EXCL lock acquisition. `readRawSessionLog` becomes module-internal.
- Deps: `memory-io.readSessionLog`, `memory-io.readSessionModel`, `paths.globalTomDir`, `usage-log.emit`, `node:fs`.

### S3 — extract (moves lines 331-456)

```ts
export interface ExtractResult {
  readonly sessionModel: SessionModel
  readonly path: 'llm' | 'preserved' | 'heuristic'
}
export async function extractTier2(input: {
  readonly sessionId: string
  readonly sessionLog: SessionLog
  readonly priorModel: SessionModel | null
  readonly userMessageCount: number
  readonly lock: AnalysisLock
  readonly model: string        // getModelForOperation('memoryUpdate'), resolved by orchestrator
}): Promise<ExtractResult>
```

- Internal helper `anchoringVocabulary(): readonly { category; key; value }[]` (the `readUserModel('global')` + `isLegacyGenericKey` filter, lines 340-342). Emits `analysis-log-truncated`, `session-analysis`, `analysis-vocabulary-echo`, `session-analysis-fallback`. Preserve-on-failure, watermark/endedAt stamping, skip-rewrite-on-preserve, and `finally { lock.release() }` all stay inside this step (invariants: watermark advances only on successful extraction; lock released as soon as Tier 2 is published).
- Deps: `llm-analyze.analyzeSessionWithLlm`, `session-extract.extractSessionModel`, `vocabulary-echo.computeVocabularyEcho`, `preferences.isLegacyGenericKey`, `memory-io.{readUserModel, writeSessionModel}`, `usage-log.emit`.

### S4 — aggregate (moves lines 458-553 and `reconcilePreferenceCategories` 100-169)

```ts
export interface AggregateResult {
  readonly userModel: UserModel                 // persisted Tier 3, post-reconcile
  readonly previousUserModel: UserModel | null
  readonly correctedKeys: readonly string[]
}
export function aggregateTier3(input: {
  readonly sessionId: string
  readonly sessionModel: SessionModel
  readonly config: TomConfig
}): AggregateResult
```

- One module, fixed internal order: `rebuildUserModelFromTier2` → `carryPromotedFlags` → `reconcilePreferenceCategories` → `writeUserModel` → correction telemetry (`dropRefiledCorrections` against `previousUserModel` canonical; keep the 25-line approximation comment at 489-509 verbatim) → follow-through telemetry (`readUsageLog({ sessionId })` + `assertedKeysForSession` + `splitFollowThrough`). Emits `preference-cross-category-collapse`, `preference-correction`, `preference-follow-through`.
- The carry-flags-before-reconcile and reconcile-before-promotion invariants (comment at 95-98) become structure: the first is line order inside this module, the second is S5's input type.
- Deps: `rebuild.{rebuildUserModelFromTier2, carryPromotedFlags}`, `preferences.{canonicalCategoryByKey, dropRefiledCorrections, reconcileCrossCategorySplits}`, `aggregation.deriveStyleSummaries`, `follow-through.{assertedKeysForSession, splitFollowThrough}`, `memory-io.{readUserModel, writeUserModel}`, `secrets.sanitizeValue`, `usage-log.{emit, prefKeyForTelemetry, readUsageLog}`.

### S5 — promote (moves lines 555-592)

```ts
export function promotePreferences(input: {
  readonly sessionId: string
  readonly userModel: UserModel   // AggregateResult.userModel: promotion never sees a split key
  readonly config: TomConfig
  readonly cwd: string
  readonly model: string
}): void
```

- Wraps the `judgeDerivability` gate closure, `runPromotion`, the same-reference write skip, and `preference-promotion` telemetry. Error isolation moves inside the step: catches everything, emits `promotion-error`, never throws (the "promotion failures must never break the pipeline" contract becomes the step's own signature).
- Deps: `promotion.runPromotion`, `promotion-gate.{judgeDerivability, GateCandidate}`, `memory-io.writeUserModel`, `usage-log.{emit, prefKeyForTelemetry}`.

### S6 — snapshot (moves lines 594-616)

```ts
export function snapshotUserModel(sessionId: string): void
```

- Re-reads the final persisted model (post-promotion flags) and writes `user-model-history/<sessionId>.json`. Emits `snapshot-error` on failure; never throws.
- Deps: `memory-io.readUserModel`, `paths.globalTomDir`, `fs-atomic.atomicWriteFileSync`, `usage-log.emit`.

### S7 — housekeep (moves lines 618-645)

```ts
export function housekeepStore(input: {
  readonly sessionId: string
  readonly config: TomConfig
}): boolean   // indexRebuilt
```

- Fixed internal order (invariant: prune before index build, so the single build reflects the post-prune store): `pruneOldSessions` + `rotateUsageLogIfNeeded` inside the existing try/catch emitting `prune-error`; then `buildMemoryIndex('global')` + atomic write of `bm25-index.json` outside it (an index-build throw propagates to the orchestrator's `main` catch, matching today's behavior).
- Deps: `pruning.pruneOldSessions`, `agent/tools.buildMemoryIndex` (see §6 coordination with `tom-swe-dhb.2`), `paths.globalTomDir`, `fs-atomic.atomicWriteFileSync`, `usage-log.{emit, rotateUsageLogIfNeeded}`.

### Orchestrator after decomposition

`tom/hooks/stop-analyze.ts` retains `AnalysisResult`, `analyzeCompletedSession`, `main`. `analyzeCompletedSession` becomes:

```ts
captureSessionUsage(sessionId, cwd, transcriptPath)
const gate = gateAnalysis(sessionId)
if (gate.kind !== 'proceed') return resultFor(gate)      // exhaustive switch
const extracted = await extractTier2({ ...gate, sessionId, model })
const aggregated = aggregateTier3({ sessionId, sessionModel: extracted.sessionModel, config })
promotePreferences({ sessionId, userModel: aggregated.userModel, config, cwd, model })
snapshotUserModel(sessionId)
const indexRebuilt = housekeepStore({ sessionId, config })
```

`config` (`readTomConfig()`) and `model` (`getModelForOperation('memoryUpdate')`) are resolved once at the top instead of mid-function. Import count drops from 17 modules to ~9 (7 steps + `config` + `routing` + `hook-input` + `usage-log.emit` for `session-analysis-error`).

## 4. Telemetry envelope and import-cycle fixes (findings 5, 6) as part of the step interface

These land **before** step extraction, so every step is born emitting typed events and no step PR touches telemetry twice.

**Cycle break (finding 6)**: new `tom/paths.ts` takes the 8 helpers defined at `memory-io.ts:20-49` and re-exported at 331-340 (`globalTomDir`, `projectTomDir`, `globalSessionPath`, `projectSessionPath`, `globalSessionModelPath`, `projectSessionModelPath`, `globalUserModelPath`, `projectUserModelPath`). `memory-io.ts` keeps its re-export block so its 11 importers stay untouched; `routing.ts:13` switches its `globalTomDir` import to `paths`. Resulting edges: `usage-log → paths, secrets`; `memory-io → paths, schemas, fs-atomic, usage-log`. No cycle, and the IO layer can finally log (the stderr-only sidecar-corruption note at `memory-io.ts:80-82` becomes a typed `sidecar-corrupt-lines` event).

**Envelope typing (finding 5)**: new `tom/usage-log.ts` receives routing's telemetry half verbatim (`TELEMETRY_SCHEMA_VERSION`, `UsageLogEntry`, `UsageLogEntrySchema`, `UsageLogReadResult`, `logUsage`, `USAGE_LOG_ROTATE_BYTES`, `rotateUsageLogIfNeeded`, `readUsageLog`, `prefKeyForTelemetry`, `usageDetailStringArray`) and defines the envelope **once**:

```ts
export type TomEvent =
  | { op: 'session-usage'; sessionId: string
      detail: { inputTokens: number; outputTokens: number; cacheCreationTokens: number
                cacheReadTokens: number; assistantMessages: number
                cwd: string; gitBranch: string | null } }
  | { op: 'session-analysis'; sessionId: string; model: string; tokenCount: number
      durationMs: number; detail: { path: 'llm' } }
  | { op: 'session-analysis-fallback'; sessionId: string; durationMs: number; reason: string
      detail: { path: 'preserved' | 'heuristic'; failure: string } }
  | { op: 'preference-follow-through'; sessionId: string
      detail: { asserted: readonly string[]; confirmed: readonly string[]
                corrected: readonly string[] } }
  | ...   // one variant per operation; full vocabulary below

export function emit(event: TomEvent): void
// stamps v + timestamp; defaults model: 'none' and tokenCount: 0 for variants
// that do not carry them; writes the identical JSONL wire format as logUsage.
```

Full operation vocabulary to encode (25 ops / 26 sites today, verified by grep): `session-usage`, `session-usage-error`, `analysis-debounced`, `analysis-skipped-no-new-evidence`, `analysis-in-flight`, `analysis-log-truncated`, `session-analysis`, `analysis-vocabulary-echo`, `session-analysis-fallback`, `preference-cross-category-collapse`, `preference-correction`, `preference-follow-through`, `preference-promotion`, `promotion-error`, `snapshot-error`, `prune-error`, `session-analysis-error` (17 in stop-analyze); `session-start-injection`, `prompt-hook`, `ambiguity-consultation`, `config-invalid`, `derivability-gate`, `promotion-file-created`, `promotion-skipped`, `usage-log-rotated` (elsewhere); plus new `sidecar-corrupt-lines`. The union becomes the generated-truth source for README's operation table. **Wire format is unchanged**; `UsageLogEntrySchema` remains the loose read-side validator, so old logs and external consumers (mem-eval harness, tom-status, effectiveness) are unaffected.

Migration: non-pipeline sites (9 sites in `config.ts`, `consult.ts`, `promotion.ts` x3, `promotion-gate.ts`, `hooks/session-start.ts`, `hooks/user-prompt-submit.ts`, `routing.ts`) convert in the same PR that creates `emit`; stop-analyze's 17 sites convert step-by-step as each step module is extracted. `logUsage` stays exported as a bridge until PR-8 un-exports it.

## 5. Test decomposition (1,969 lines → per-step files)

Every `it` in `stop-analyze.test.ts` maps to exactly one step; the split is a move, not a rewrite. Mapping by current test line number:

| New test file | its (current line numbers) |
|---|---|
| `capture-usage.test.ts` | 359, 399, 424, 501 |
| `gate.test.ts` | 92, 96, 103, 114 (readRawSessionLog describe), 152, 1548, 1566, 1663 |
| `extract.test.ts` | 161, 184, 295, 334, 537, 559, 577, 627, 646, 682, 732, 761, 785, 803, 1507, 1619 (watermark on failure), 1684 (lock release) |
| `aggregate.test.ts` | 204, 826, 873, 936, 960, 997, 1021, 1081, 1196, 1244, 1325, 1352, 1367, 1470, 1724 |
| `promote.test.ts` | 1413, 1454, 1694, 1708 |
| `snapshot.test.ts` | 438 |
| `housekeep.test.ts` | 239, 460 |
| `stop-analyze.test.ts` (kept) | `main` describe (1803-1969: 1864, 1872, 1896, 1912, 1925, 1943, 1960) + 2 new integration its: full happy path through all 7 steps; step failure surfaces as `session-analysis-error` |

Per-step tests call the step function directly with constructed inputs instead of driving the whole pipeline through the filesystem, which removes most of the per-it setup boilerplate that inflated the file to 1,969 lines. The two kept integration its preserve end-to-end coverage of the wiring.

## 6. PR sequence

Rules applied: one concern per PR; each PR executable by a mid-tier (Sonnet) worker from this document alone; tests move in the same commit as the code they cover; **every PR ships rebuilt `dist/` until `tom-swe-dhb.1` lands** (run `npm run typecheck && npm test && npm run build`, commit the `dist/` diff with the source). Land `tom-swe-dhb.1` first if at all possible; nothing below depends on it, but it converts the per-PR dist discipline from convention into a gate.

### PR-1 — `feat: extract tom/paths.ts, break the memory-io/telemetry import knot`
- **New**: `tom/paths.ts` (the 8 helpers from `memory-io.ts:20-49`, moved verbatim), `tom/paths.test.ts` (path-shape assertions).
- **Edit**: `tom/memory-io.ts` (import from `./paths`, keep the re-export block at 331-340; delete local definitions), `tom/routing.ts:13` (import `globalTomDir` from `./paths.js`).
- Pure mechanical move; zero behavior change; all existing tests pass unmodified.
- **Dist**: paths/memory-io/routing are bundled into all 10 entry points → all of `dist/tom/hooks/*.js` and `dist/tom/skills/*.js` change. Ship rebuilt `dist/` in this PR (CI bead not yet landed).

### PR-2 — `feat: split usage-log.ts out of routing.ts; typed TomEvent envelope + emit()`
- **New**: `tom/usage-log.ts` (telemetry symbols moved from routing per §4 + `TomEvent` union covering all 26 ops + `emit()`), `tom/usage-log.test.ts` (moved cases from `routing.test.ts` + new: emit stamps `v`/`timestamp`; per-variant golden test that `emit` output is byte-identical to the legacy `logUsage` line).
- **Edit**: `tom/routing.ts` (shrinks to `OperationType`, `getModelForOperation`; `rotateUsageLogIfNeeded` internal call sites move with the code), `tom/routing.test.ts` (routing-only remainder), import-path updates in the 12 routing importers (`telemetry.ts`, `follow-through.ts`, `effectiveness.ts`, `consult.ts`, `config.ts`, `promotion.ts`, `promotion-gate.ts`, `hooks/session-start.ts`, `hooks/user-prompt-submit.ts`, `hooks/stop-analyze.ts`, `skills/tom-status.ts`, `skills/tom-effectiveness.ts`).
- Migrate the 9 non-stop-analyze `logUsage` production sites to `emit()` (wire format unchanged; existing telemetry assertions pin this). Add the `sidecar-corrupt-lines` emit in `memory-io.readSidecarLines` (keep the stderr note), update the stale cycle comment at `memory-io.ts:80-82`, and add the op to README's operation table.
- **Dist**: core modules again → all 10 bundles change. Ship rebuilt `dist/`.

### PR-3 — `refactor: stop-pipeline scaffolding + leaf steps S1 capture-usage, S6 snapshot`
- **New**: `tom/stop-pipeline/types.ts` (`StopContext`, `AnalysisResult` moved from stop-analyze, `AnalysisLock`, result-type stubs are NOT included; each result type lands with its step), `tom/stop-pipeline/capture-usage.ts`, `tom/stop-pipeline/snapshot.ts`, their test files (its per §5).
- **Edit**: `tom/hooks/stop-analyze.ts` (delete lines 193-225 and 594-616; call the two steps; convert those steps' 3 telemetry sites to `emit`), `tom/hooks/stop-analyze.test.ts` (remove the 5 moved its).
- These two steps have no data flow into the pipeline result, so they prove the pattern with minimal blast radius.
- **Dist**: only `dist/tom/hooks/stop-analyze.js` changes. Ship it rebuilt.

### PR-4 — `refactor: stop-pipeline S2 gate (debounce + watermark + in-flight lock)`
- **New**: `tom/stop-pipeline/gate.ts` (`gateAnalysis`, `GateResult`, `AnalysisLock` impl, `ANALYSIS_DEBOUNCE_MS`, internal `readRawSessionLog`), `gate.test.ts` (8 its per §5).
- **Edit**: `stop-analyze.ts` (delete lines 66-68, 227-329; exhaustive switch over `GateResult`; `extractTier2` does not exist yet, so the proceed branch keeps the inline extract code, now consuming `gate.sessionLog` / `gate.priorModel` / `gate.lock.release()`), `stop-analyze.test.ts` (remove moved its).
- **Dist**: `dist/tom/hooks/stop-analyze.js` only. Ship it rebuilt.

### PR-5 — `refactor: stop-pipeline S3 extract (Tier 2, preserve-on-failure, vocabulary echo)`
- **New**: `tom/stop-pipeline/extract.ts` (`extractTier2`, `ExtractResult`, `anchoringVocabulary`), `extract.test.ts` (17 its per §5; the largest carve, ~600 lines).
- **Edit**: `stop-analyze.ts` (delete lines 331-456; convert 4 telemetry sites), `stop-analyze.test.ts`.
- **Dist**: `dist/tom/hooks/stop-analyze.js` only. Ship it rebuilt.

### PR-6 — `refactor: stop-pipeline S4 aggregate (Tier 3 rebuild + reconcile + outcome telemetry)`
- **New**: `tom/stop-pipeline/aggregate.ts` (`aggregateTier3`, `AggregateResult`, `reconcilePreferenceCategories` moved from stop-analyze:100-169 with its full doc comment, plus the correction-approximation comment at 489-509), `aggregate.test.ts` (15 its per §5).
- **Edit**: `stop-analyze.ts` (delete lines 100-169, 458-553; convert 3 telemetry sites), `stop-analyze.test.ts`.
- **Dist**: `dist/tom/hooks/stop-analyze.js` only. Ship it rebuilt.

### PR-7 — `refactor: stop-pipeline S5 promote`
- **New**: `tom/stop-pipeline/promote.ts` (`promotePreferences`; error isolation moves inside), `promote.test.ts` (4 its per §5; "promotion failure completes the pipeline" becomes "promotePreferences emits promotion-error and does not throw").
- **Edit**: `stop-analyze.ts` (delete lines 555-592; drop the try/catch, now inside the step), `stop-analyze.test.ts`.
- **Dist**: `dist/tom/hooks/stop-analyze.js` only. Ship it rebuilt.

### PR-8 — `refactor: stop-pipeline S7 housekeep; slim the orchestrator; un-export logUsage`
- **New**: `tom/stop-pipeline/housekeep.ts` (`housekeepStore`), `housekeep.test.ts` (2 its per §5).
- **Edit**: `stop-analyze.ts` (delete lines 618-645 and the now-dead `NO_MODEL` const; convert `main`'s `session-analysis-error` site to `emit`; final shape per §3, ~150 lines), `stop-analyze.test.ts` (final shape per §5: `main` describe + 2 integration its), `tom/usage-log.ts` (un-export `logUsage`; `emit` is the only write API; grep must show zero external `logUsage` references).
- **Dist**: `dist/tom/hooks/stop-analyze.js` (plus all 10 bundles if the `logUsage` un-export touches shared module text). Ship whatever `git status dist/` shows after rebuild.

### PR-9 (optional, decoupled) — `refactor: shared spawnHeadlessClaude (finding 7)`
- **New**: `tom/headless-claude.ts` (`spawnHeadlessClaude(prompt, model, opts)`: prompt-over-stdin for E2BIG, `TOM_SWE_INTERNAL=1`, zero-tools least-privilege flags, timeout, JSON-wrapper parse, `extractTokensUsed`), tests.
- **Edit**: `tom/llm-analyze.ts:349-425` (async site), `tom/promotion-gate.ts:113-120` (sync site). The review rates this "do opportunistically"; it can land any time after PR-2 and must land before the third spawn site (`models.consultation`) is built.
- **Dist**: `dist/tom/hooks/stop-analyze.js` only (both modules bundle only into the Stop hook). Ship it rebuilt.

**Bead coordination**: `tom-swe-dhb.1` (CI) first if possible; once it lands, the per-PR "ship rebuilt dist/" lines above are enforced mechanically and can stop being called out. `tom-swe-dhb.2` (agent-layer deletion) moves `buildMemoryIndex` out of `tom/agent/tools.ts` (review suggests next to `bm25.ts`); whichever of dhb.2 / PR-8 lands second updates one import line in `housekeep.ts` (or `stop-analyze.ts:27` if dhb.2 lands before PR-8). No ordering constraint beyond that one-line touch.

## 7. Invariants the decomposition must preserve (and where each now lives)

| Invariant (source today) | Encoding after decomposition |
|---|---|
| Usage capture lands even when analysis skips/fails (193) | S1 unconditional first call in orchestrator |
| Watermark advances only on successful extraction (269-275) | Inside S3's preserve path |
| Lock released as soon as Tier 2 published; downstream steps overlap-safe (444-456) | S3 `finally { lock.release() }`; `AnalysisLock` never escapes past S3 |
| `carryPromotedFlags` before `reconcilePreferenceCategories` (95-98) | Line order inside S4 (single module) |
| Reconcile before `runPromotion` (95-98) | S5 input type is `AggregateResult.userModel` |
| Promotion/snapshot/prune failures never break the pipeline (558, 598, 623) | try/catch inside S5/S6/S7 respectively; signatures are `void`/non-throwing |
| Prune before index rebuild (618-621) | Line order inside S7 |
| usage.log wire format frozen for external consumers | `emit` golden tests in PR-2; `UsageLogEntrySchema` unchanged |
| Loop guard / exclusion / enablement checks before any work (661-675) | Unchanged in `main` |

Primary risk: silent behavior drift while moving the extract/aggregate blocks (the two with real branching). Mitigation is mechanical: move line ranges verbatim including comments, keep the per-step tests as moves not rewrites, and rely on the two orchestrator integration its plus (once dhb.1 lands) CI dist-parity. Steps PR-3 through PR-8 each touch only one bundle, so a bad landing is revertible by reverting one commit including its dist.

## Executive summary

1. `stop-analyze.ts` (702 lines, 17 imports, 469-line function) becomes a ~150-line orchestrator over 7 typed steps in `tom/stop-pipeline/`, with data flow (`GateResult` → `ExtractResult` → `AggregateResult`) turning the comment-documented ordering invariants into compile-time structure.
2. The 1,969-line test splits into 7 per-step files plus a slim orchestrator test, by moving its (mapping in §5), not rewriting them.
3. Telemetry is fixed once, up front: PR-1 extracts `tom/paths.ts` (breaks the memory-io/telemetry cycle, finding 6), PR-2 splits `tom/usage-log.ts` out of routing and defines the single `TomEvent` union + `emit()` (finding 5), so every step is born with typed events and the wire format never changes.
4. Nine single-concern PRs (PR-9 optional spawn dedup), each mid-tier-executable from this document with exact file/symbol/test lists; PRs 1-2 rebundle all 10 dist entry points, PRs 3-9 touch only `dist/tom/hooks/stop-analyze.js`, and **every PR ships rebuilt `dist/` in the same commit until `tom-swe-dhb.1` lands**.
5. First PR to cut: **PR-1, `tom/paths.ts` extraction** — zero behavior change, unblocks the telemetry split, and lets the IO layer log its own corruption for the first time.
