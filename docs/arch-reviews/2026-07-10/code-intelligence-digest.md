# Architecture Review — code-intelligence-digest

Date: 2026-07-10 · Scope: read-only structural review of /home/ds/projects/code-intelligence-digest (Next.js App Router + TS, ~76K LOC in src/+app/, 285 source files, 85 API routes)

## Executive summary

The load-bearing boundaries are mostly healthy: one LLM gateway with enforced usage accounting, auth centralized in middleware, DB access through typed repositories, and a fresh LikeC4 model (2026-06-22). The structural debt concentrates in three places: (1) scoring formulas have re-diverged — four recency implementations exist and the canonical `scoring-utils.ts` the refactoring PRD created is now dead code with zero importers; (2) the agents subsystem, which dominates both size (content-ideas.ts at 5,003 lines) and churn (top 3 churn files), runs through two parallel orchestrators that each hardcode a per-agent dispatch ladder; (3) top-level generation orchestration (retrieve → rank → rerank → select → generate) lives copy-pasted inside the four biggest API routes instead of `src/lib/pipeline`, so every new content type costs a copied 400-line route. The two dependency cycles (`config/feeds ⇄ db/feeds`, `db/ads-papers ⇄ pipeline/section-summarization`) are both symptoms of single misplaced responsibilities and are cheap to fix. Highest ROI: re-unify scoring (S), collapse the agent dispatch ladders (S/M), then extract route orchestration into the pipeline layer (M).

## Architecture summary (as built)

Layers, roughly as the LikeC4 model in `architecture/model.c4` intends: **ingestion/sync** (`src/lib/sync/*`, Inoreader + NASA ADS, budget guard, circuit breaker) → **scoring pipeline** (`src/lib/pipeline/*`: normalize/categorize, BM25, LLM scorer, compute-scores writes persisted scores; rank/select serve reads) → **Postgres store** (`src/lib/db/*` typed repositories over a single driver; pgvector for embeddings) → **LLM gateway** (`src/lib/llm/completion.ts:221 createChatCompletion`, sole chat entry point, usage accounting inline) → **GTM agent layer** (`src/lib/agents/*`: three report generators + job runner) → **media renderers** (newsletter/podcast/audio digest) → **Next.js routes** (85 `route.ts`) and React UI. Two worlds share the pipeline vocabulary but not code: the "category" lineage (retrieval/rank/select, serving digest/newsletter/podcast routes) and the "agent" lineage (agentRetrieval/agentRank/agentShortlist, serving the GTM agents). Cron/systemd drive daily sync and agent jobs; MCP server exposes the corpus to coding agents.

## Major strengths (protect these)

- **Single chat-LLM gateway**: only 1 SDK import outside `src/lib/llm/` in all of src/ (and it's TTS, a different modality). `recordLlmUsage` fires on both provider paths (`completion.ts:241,293`); `run-job.ts:344` wraps runs in budget accounting. Zero bypass paths for text generation.
- **Auth centralized in `middleware.ts`** (401 for unauthenticated `/api/*`, admin bearer path); `src/lib/auth/guards.ts` is an orthogonal prod-blocking guard applied consistently to admin routes. No hand-rolled auth found in a 20-route sample.
- **Repository discipline**: raw SQL from routes is confined to 3 files, all in the `items` family (`app/api/items/route.ts:569,588,777`; `items/[id]/fulltext/route.ts:79`; `items/[id]/libraries/route.ts:22,32`). Everything else goes through `src/lib/db/*`.
- **Versioned prompt store** (`src/lib/agents/prompt-store.ts:112` registry, env-override rollback) — ~90% of agent system prompts externalized.
- **Fresh intended-architecture model** (`architecture/*.c4`, updated 2026-06-22) and real test coverage (68 test files, concentrated on lib).

## Ranked findings (leverage ÷ effort, risk-discounted)

### 1. Scoring formulas re-diverged; the canonical module is dead code

**Evidence:** `src/lib/pipeline/scoring-utils.ts` exports `computeRecencyScore` (:82), `computeBoostMultiplier` (:128) — grep finds **zero importers** anywhere in src/app/scripts. Meanwhile recency is privately redefined in `rank.ts:23` (`2^(-age/hl)`, no floor), `compute-scores.ts:25` (`0.2 + 0.8·e^(-ln2·age/hl)`), inlined a third way in `agentRank.ts:39-42`, and a step function in `goalFeatures.ts:126-133`. BM25 + product-boost + weight combination exist in both the write path (`compute-scores.ts:83,167`) and the read path (`rank.ts:284,343,417`), with stored scores trusted only in a narrow special case (`rank.ts:412`). `tasks/prd-codebase-refactoring.md` US-001 mandated exactly this unification; it regressed.
**Why it matters:** this is correctness-grade drift — the same item can rank differently per code path, and any scoring tune must be found and applied in 3–4 places. It also poisons evals: you can't attribute a ranking change to a config tweak when formulas differ per lane. The intended single source of truth already exists; the work is re-pointing callers at it.
**Effort:** S. **Risk:** low-moderate (score values shift slightly when formulas converge; the PRD's own tolerance test pattern covers this). Fully reversible.

### 2. Two agent orchestrators, each hardcoding a per-agent dispatch ladder

**Evidence:** `src/lib/agents/run-job.ts:144` (`runAgentJobBody`) branches on `competitive_intel`/`icp_market`/`gtm_content` (:156, :196, :218) and persists to Postgres; `src/lib/agents/generate-reports.ts:98` (`runAgentReportImpl`) branches on `market_brief`/`content_ideas`/`competitor_intel` (:119, :123, :136) and persists to filesystem + `saveReport`. Both duplicate the generate → write-with-LLM → format-markdown triad and both re-spell competitor budget config (`run-job.ts:157-168` vs `generate-reports.ts:137-147`). Churn confirms cost: run-job 11, generate-reports 21 commits in the last 300.
**Why it matters:** the agents subsystem is the #1 churn zone in the repo (competitor-intel 33, content-ideas 30, market-brief 17 commits). Adding a fourth agent today means editing two ladders and keeping two persistence stories consistent. A single `AgentDefinition` interface (generate/postProcess/write/format handles, already the de-facto shape of all three generators) collapses both ladders into a registry lookup and makes agent #4 a one-file addition.
**Effort:** S/M. **Risk:** low — mechanical extraction; both ladders already call identical trios, so behavior is preserved by construction.

### 3. Generation orchestration lives copy-pasted in the biggest routes, not the pipeline

**Evidence:** the `loadItems → rankCategory → rerankWithPrompt → selectWithDiversity → generate*Content → save` block is cloned across `app/api/newsletter/generate/route.ts:526-672`, `app/api/podcast/generate/route.ts:505-684`, and **four times** in `app/api/audio-digest/generate/route.ts` (rank at :441, :527, :881, :970 — twice per code path within one file). Routes are 80%+ domain logic by line fraction. Newsletter and podcast **bypass existing lib orchestrators** (`pipeline/newsletter.ts:73 generateNewsletterContent`, `pipeline/podcast.ts:194 generatePodcastContent`), which are now stale variants; audio-digest and `app/api/resources/ask/route.ts:78` (596-line RAG handler touching 7 db modules) have no lib home at all.
**Why it matters:** this is the choke point for the product's main axis of growth (new digest/media formats). Today a new content type = a copied 400-line route + a forked ranking function (that is literally how `rankCategoryWithoutRecency` was born). One `generatePublication(type, profile)` orchestrator in `src/lib/pipeline` turns routes into parse→auth→call→respond shells and removes the stale-duplicate-orchestrator trap. Also un-forks testing: lib orchestrators are unit-testable; route bodies are not.
**Effort:** M (do audio-digest first — it removes an intra-file 2× duplication for free). **Risk:** moderate — behavior differences between route and stale lib versions must be reconciled deliberately; do one format per PR.

### 4. `content-ideas.ts` is a 5,003-line god-module (8 responsibilities, 150 private functions, 3 exports)

**Evidence:** `src/lib/agents/content-ideas.ts` fuses retrieval (:484, :4429), rubric ranking (:2030, :3152-3322), ~30 source-quality predicates (:395, :543-634, :1894-1949), heuristic idea templates (:1441-1596, :2321), ~15 chained diversity enforcers (:2503-3047), LLM synthesis with an inline system prompt (:3809, :3901 — the one prompt that bypasses prompt-store), output parsing (:777, :867), and post-processing (:4169). 30 commits of churn in the last 300. Helper families (`stripBoilerplateNoise`, `canonicalizeUrl`, dedup) are copy-pasted across all three generators with no shared `agents/util`.
**Why it matters:** highest-churn + largest file = maximum merge-conflict and regression surface exactly where iteration is fastest. The 150:3 private:public ratio means the seams already exist; splitting along the 8 responsibilities (filtering, rubric, diversity, synthesis, parsing) makes each independently testable and shrinks every future GTM tune's blast radius. Migrate `:3901` into prompt-store while at it. (House note: the ~30 keyword/domain predicate families are heuristic semantic classification in code — a candidate for model delegation per ZFC, but that's a product decision, not required for the split.)
**Effort:** L (incremental: extract shared `agents/util` S, then per-concern modules). **Risk:** moderate — no behavior change intended, but large mechanical moves in a high-churn file need a quiet window; do it in slices.

### 5. `config/feeds.ts` is a runtime service in the config layer (causes cycle #1)

**Evidence:** `src/config/feeds.ts` (393 lines) imports `createInoreaderClient`, `initializeDatabase`, `fs` (:6-17), exports `async getFeeds` (:285), `forceRefreshFeedsCache` (:338) — live Inoreader fetches + DB caching. It statically imports `lib/db/feeds` (:10-15) while `lib/db/feeds.ts:6` imports `FeedConfig` back, and dodges the cycle at :347 with a lazy `await import`. Broader pattern: `src/config/products.ts` (848 lines) ships 15 functions + 71 regexes (`findProductMentions:609`, `computeProductBoost:793`); config/ totals 2,973 lines with several files exporting classifiers.
**Why it matters:** "config" no longer means data — readers can't tell what's safe to edit vs what executes IO at import time, and the config→db→config cycle blocks any future config-loading or build-time-constant story. Fix: move the service half of feeds.ts to `src/lib/feeds/`, leave the `FeedConfig` type + static list in config. Products.ts logic can move to `lib/pipeline/product-matching` when next touched.
**Effort:** S (feeds split; products opportunistic). **Risk:** low — import-path mechanical, both cycles verified by madge before/after.

### 6. DB layer reaches up into the pipeline (cycle #2, layering inversion)

**Evidence:** `src/lib/db/ads-papers.ts:211` and `:395` lazily `await import('../pipeline/section-summarization')` to trigger `processPaperSections` after fetch/store; `section-summarization.ts:6` imports `getPaper` back. The dynamic import exists solely to keep the load-time cycle from crashing.
**Why it matters:** repositories calling orchestration inverts the one-direction layering the rest of the db/ layer respects, and it hides an LLM-summarization side effect inside a data accessor (surprising cost/latency from a "get"). Fix: delete both lazy imports; make the two pipeline/route callers invoke `processPaperSections` after the repo call returns.
**Effort:** S. **Risk:** low — two call sites; behavior preserved by moving, not changing, the invocation.

### 7. No shared structured-LLM helper: guard/call/parse skeleton hand-rolled 10+ times

**Evidence:** the `hasLLMConfigured` guard → inline prompt → `createChatCompletion({response_format:{type:"json_object"}})` → `JSON.parse` + validate + fallback sequence is re-implemented in `digest.ts:105`, `newsletter.ts:144,153,353,608`, `podcast.ts:221`, `podcastDigest.ts:125,191,200`, `podcastScript.ts:111`, `podcastRundown.ts:107,184,193`, and 4+ times inside `audioDigest.ts` (:135, :233/242, :331, :454/463, :589).
**Why it matters:** every generator re-decides error handling, fallback shape, and parse robustness; fixes to JSON-parse hardening (a known LLM failure mode — content-ideas already grew its own `extractJsonValue:777` recovery layer) don't propagate. One `generateStructured<T>(prompt, schema, fallback)` on top of the (already excellent) gateway gives zod-validated outputs everywhere and shrinks each generator.
**Effort:** S/M (adopt incrementally per generator). **Risk:** low; each adoption is an isolated diff.

### 8. Two parallel retrieve→rank→select lineages with no shared abstraction — decide, then ADR it

**Evidence:** category world (`retrieval.ts:21`, `rank.ts:41/475`, `select.ts:55`, ~1,215 lines) vs agent world (`agentRetrieval.ts:732`, `agentRank.ts:55`, `agentShortlist.ts:59`, ~1,068 lines) share only leaf utilities. Both call `loadScoresForItems` then re-derive orderings with different weightings. Inside rank.ts, `rankCategory` (:41-474) and `rankCategoryWithoutRecency` (:475-826) are ~90% clones (~350 lines) differing mainly at :417 vs :782.
**Why it matters:** the intra-file rank.ts clone is pure debt — parameterize recency weight and delete ~350 lines (fold into finding 1's work). The cross-lineage split may be a legitimate seam (different formulas, different goals) — but today nothing records that intent, so the next feature will guess. Either converge on shared stages or write the ADR that declares the two lanes and their contract. Don't force-merge: the formulas differ deliberately.
**Effort:** S for the rank.ts de-clone; M/L for lineage convergence (only if the ADR decision says so). **Risk:** rank.ts de-clone low; lineage merge moderate (score-behavior sensitive).

### 9. Schema managed by boot-time DDL in three scattered sites

**Evidence:** `src/lib/db/index.ts:40` (`initializeDatabase`) execs `schema-postgres.ts` + `ensurePostgresUserIdColumns` + `initializeADSTables` (ads-papers.ts) + `initializeAnnotationTables` (paper-annotations.ts) on every boot; `CREATE TABLE` lives in 3 modules. schema-postgres.ts has 15 commits of churn.
**Why it matters:** no ordered migration history means schema changes are append-only `IF NOT EXISTS` patches, column renames/drops are effectively impossible, and prod/local drift is invisible (the repo already carries sync/mirror scripts to paper over it). Fine at current single-instance scale; becomes the bottleneck the first time a destructive change is needed. Adopt a minimal ordered-migration runner when the next schema change lands, don't retrofit history.
**Effort:** M. **Risk:** low if introduced additively (baseline = current schema).

### 10. Dead/orphan modules from superseded iterations

**Evidence:** `src/lib/sync/inoreader-sync-optimized.ts` has no consuming route (the `sync-optimized` route path in the tree doesn't exist as a real consumer); `pipeline/newsletter.ts:73` and `podcast.ts:194` high-level orchestrators are stale bypassed variants (see finding 3); `scoring-utils.ts` dead (finding 1); `.reports/dead-code-analysis.md` already lists 61 unused exports awaiting review. `sync-48h` and `sync-starred` routes self-orchestrate inline (`sync-48h/route.ts:92-126`, `sync-starred/route.ts:27-78`) instead of living in `src/lib/sync`.
**Why it matters:** each stale twin is a landmine for the next editor (fix applied to the dead copy). Cheap deletions with outsized confusion savings; fold into findings 1 and 3 rather than a standalone pass.
**Effort:** S. **Risk:** low (verify with ts-prune/knip before deleting).

## Leave unchanged

- **LLM gateway** (`src/lib/llm/`) — single entry, usage accounting enforced; build on it, don't touch it.
- **Auth model** — middleware + guards split is deliberate and consistent.
- **DB repository layer** — boundary respected in 82 of 85 routes; the 3 items-route leaks can wait for the next items-feature touch.
- **prompt-store.ts** — well-designed versioned registry; only needs the one migration (content-ideas.ts:3901).
- **Sync layer core** (daily-sync, budget guard, circuit breaker) — thin-wrapper routes for 4 of 6 sync endpoints; only sync-48h/sync-starred need rehoming.
- **Embeddings subsystem** — legitimately separate vector lane, not a gateway bypass.
- **architecture/*.c4** — fresh and broadly accurate; drift is at the route-orchestration level (finding 3), not the container level.

## Suggested sequencing

1 (scoring unify + rank.ts de-clone) → 2 (agent registry) → 6 (db inversion) → 5 (feeds split) → 3 (route orchestration, one format per PR, audio-digest first) → 7 (generateStructured, adopted during 3) → 4 (content-ideas split, in slices) → 8 ADR → 9/10 opportunistic.
