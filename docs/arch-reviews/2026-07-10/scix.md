# Architecture Review — scix (scix_experiments)

Date: 2026-07-10 · Scope: /home/ds/projects/scix_experiments (read-only) · Method: repo-architecture-review skill

## Executive summary

The system is a well-governed retrieval stack (Postgres 16 + pgvector + Qdrant dense lane, 20-tool MCP surface) mid-way through a deliberate de-godification of `mcp_server.py`; the extraction to `mcp_runtime` + `mcp_handlers/` is real but incomplete, leaving 3 import cycles through the server wiring module, which is also the #1 churn file (96 commits since Jan). `search.py` (4,849 lines, 66 defs, 86 SELECTs, #2 churn) has become the entire read-side data layer, not a search module. A byte-identical dead copy of the extract pipeline (`src/scix/extract.py`, shadowed by the `extract/` package since 2026-04-25) is an edit-landmine. Schema truth has forked: `schema.sql` frozen at 2026-04-16 while migrations run to 072, and `setup_db.sh` applies only the stale file with errors swallowed. The stated 15-tool cap vs 20 advertised tools is the main intended-vs-actual drift; everything else (db.py, contract conformance machinery, ADR discipline) is in good shape and worth protecting.

## Architecture as found

- **Package** `src/scix/` — 164 modules (73 flat in the root), subpackages: `mcp_handlers/`, `extract/`, `claims/`, `eval/`, `sources/`, `jit/`, `viz/`, `embeddings/`, `graph_experiment/`.
- **MCP layer** — `mcp_server.py` (wiring: pool, dispatch, self-test, health) → `mcp_runtime.py` (extracted neutral helper layer, bead 2qx3) → `mcp_handlers/*` (per-domain tool handlers) → `search.py` et al. (data access) → `db.py` (DSN guards, IndexManager, IngestLog; fan-in 21 in src, used by 124 of 201 scripts).
- **Contract machinery** — `mcp_tool_specs.py` + `mcp_contract.py` + `contract/scix_mcp_v1.json` + conformance test; regenerated via `scripts/gen_mcp_contract.py`.
- **Dependency graph** — one clean hub (`scix.db`), one messy hub (`scix.mcp_server`, fan-out 14, fan-in 5), 3 cycles all passing through `mcp_server`:
  1. `mcp_server ↔ claim_blame`
  2. `mcp_server → viz → mcp_handlers → mcp_server`
  3. `mcp_server → viz → mcp_handlers → find_replications → mcp_server`

## Strengths to protect

- `db.py` is small (260 lines), cohesive, and is the genuine shared substrate (DSN guards, `is_production_dsn`, IndexManager) — the safety architecture around prod DSNs is unusually good.
- ADR-pinned retrieval decisions (Qdrant dense lane, no binary quantization, 768d) are enforced in code (`QDRANT_URL` gate in `vector_search()`), not just docs.
- The contract conformance test (`tests/test_mcp_contract_conformance.py`) makes tool-surface drift mechanically detectable.
- `mcp_runtime.py` extraction shows the right instinct and explicitly documents the target layering ("depends on neither mcp_server nor mcp_handlers; both import FROM it").

---

## Ranked findings (ROI = reach / effort, risk-discounted)

### 1. Dead byte-identical duplicate: `src/scix/extract.py` shadowed by `extract/` package
- **Evidence:** `diff -q src/scix/extract.py src/scix/extract/__init__.py` → identical (1,478 lines each). Package created 2026-04-25 (ff097ef); verified `import scix.extract` resolves to `extract/__init__.py`. `extract.py` has received zero effective edits since — but still shows in git churn (8 commits listed pre-split).
- **Why it matters:** Any future edit to `extract.py` silently no-ops (the package wins import resolution). This is a guaranteed future lost-afternoon, and it corrupts churn/coverage metrics. Deleting a concept for free.
- **Effort:** S (delete file, run test suite). **Risk:** minimal — imports cannot reach it.

### 2. Finish the mcp_server extraction: move `_get_pool` / `_inject_coverage_note` / `_qdrant_tools` into `mcp_runtime`, killing all 3 cycles
- **Evidence:** `mcp_handlers/paper.py:18`, `claim.py:24`, `entity.py:27` import `_inject_coverage_note` from `scix.mcp_server`; `search.py:27` imports `_qdrant_tools`; `find_replications.py:407` and `claim_blame.py:597` do `from scix.mcp_server import _get_pool` inside functions (the classic cycle-dodging deferred import). `entity.py`/`search.py`/`sections.py` hold a live `from scix import mcp_server as _srv` handle. PEP 562 `__getattr__` lazy re-exports at `mcp_server.py:1273` exist purely to service this tangle.
- **Why it matters:** `mcp_server.py` is the #1 churn file (96 commits since 2026-01). Every tool change risks import-order breakage; the cycles are why handlers must use function-local imports and module-handle indirection. `mcp_runtime` was explicitly built as the neutral layer — the pool and coverage-note cache are the last squatters. Once moved, `mcp_server` becomes pure wiring and the handler subpackage becomes independently testable without importing the server.
- **Effort:** S–M (mechanical moves + keep re-export shims for the historical patch surface the docstrings already promise). **Risk:** low — the re-export pattern is already established; tests patch via `scix.mcp_server.<helper>` and shims preserve that.

### 3. Split `search.py` (4,849 lines) along the seams `mcp_handlers/` already established
- **Evidence:** 66 top-level defs, 86 SELECTs, #2 churn (60 commits since Jan). Contents span at least five domains: search lanes + RRF + reranker (`lexical_search`, `vector_search`, `hybrid_search`, `CrossEncoderReranker`), citation graph (`get_citations*`, `co_citation_analysis`, `bibliographic_coupling`, `citation_chain`), communities (`explore_community`, `_fetch_communities_for_paper`), concepts/UAT (`concept_search`, `_lookup_concepts_unified`), and fulltext/section reading (`read_paper_section`, `search_within_paper`, `read_fulltext*` — lines 3719–4849).
- **Why it matters:** This is the read-side DAL for the whole system wearing a search costume. The handler layer is already split by domain (`search/citation/sections/entity/paper`); the data layer underneath is not, so every domain change collides in one file (60-commit churn is the symptom). Splitting into `retrieval/lanes.py`, `retrieval/citation_graph.py`, `retrieval/communities.py`, `retrieval/fulltext.py` (with `search.py` as a re-export facade during transition) mirrors an existing, proven seam rather than inventing one.
- **Effort:** M (mostly mechanical; the facade keeps `scix.search.X` patch surfaces alive). **Risk:** moderate — many tests monkeypatch `scix.search` attributes; do it with the facade and delete the facade later.

### 4. Schema truth has forked: `schema.sql` (2026-04-16) vs migrations → 072; `setup_db.sh` applies only the stale file and swallows errors
- **Evidence:** `git log -1 -- schema.sql` → 2026-04-16; `migrations/` now at `072_indus_qdrant_synced.sql` (068–072 all post-date the dump, incl. the OA-gating function CLAUDE.md depends on). `scripts/setup_db.sh:54` runs `psql -f schema.sql > /dev/null 2>&1` — no migrations, errors discarded.
- **Why it matters:** A fresh environment (the scixmuse mirror is an explicitly planned target) bootstraps to an April schema with no error signal, then fails mysteriously at runtime (`papers_is_oa_or_preprint` missing, embedding-outbox tables absent). Two sources of truth with no conformance check is the same failure class the MCP contract test already solved for the tool surface — apply the same medicine: regenerate `schema.sql` on migration merge (or have setup_db.sh apply migrations) plus a test that fails on drift.
- **Effort:** S. **Risk:** minimal, fully reversible.

### 5. Tool-surface drift vs the stated 15-tool cap: 20 advertised tools, 28 registry entries
- **Evidence:** CLAUDE.md/AGENTS.md: "Don't add an MCP tool past the 15-tool cap." `mcp_tool_specs.py` advertises 20 tools; `_build_handler_registry()` (`mcp_server.py:1218`) routes 28 names (consolidated + legacy passthroughs: 4 session tools, `get_author_papers`, `get_citation_context`, `entity_profile`, health, removed-tool stub).
- **Why it matters:** The cap exists for a measured reason (agent tool-selection accuracy). Either the cap was re-ratified somewhere post-`docs/mcp_tool_audit_2026-04.md` and the standing rule is stale, or the surface has quietly grown 33% past a premortem-driven limit. Both readings are drift between intended and actual architecture; unresolved, every future tool proposal negotiates against a fictional number.
- **Effort:** S (ADR that either re-baselines the cap at the audited number or schedules consolidation of the legacy passthroughs, which already have `_AliasTransform` machinery pointing at their successors). **Risk:** low for the decision; consolidation itself is governed by the existing deprecation path.

### 6. Two coexisting claim-extraction concepts (`claim_extractor.py` regex/M4 vs `claims/` nanopub-LLM)
- **Evidence:** `src/scix/claim_extractor.py` (426 lines, regex cosmology claims, used by `scripts/run_claim_extractor.py`, `scripts/extract_claims_batch.py`) alongside `src/scix/claims/extract.py` (759 lines, LLM pipeline into `paper_claims`, feeds the MCP `claim_search`/`find_claims` tools). `scripts/extract_claims_batch.py` imports both. Adjacent: `claim_blame.py` (738 lines) is a third claim-adjacent module wired directly into the server.
- **Why it matters:** Two extraction concepts writing claim data means every downstream consumer must know which lane produced a row. If M4-regex is a superseded experiment, retire it (or fold it in as a deterministic pre-pass of `claims/`); if it is a distinct product lane, name the boundary. One decision removes a standing "which claims?" question from all future claim work.
- **Effort:** S (decision + ADR) to M (consolidation). **Risk:** low; both lanes are test-covered.

### 7. Invert the server→viz dependency with an event hook
- **Evidence:** `mcp_server.py:184-203` — the server layer optionally imports `scix.viz.trace_stream` (try/except ImportError) to emit TraceEvents; `viz/api.py`/`viz/server.py` import `mcp_server` back (cycles 2 and 3 above ride this edge).
- **Why it matters:** The dispatch core knowing about the visualization subsystem is the wrong direction; a registered-callback hook (`mcp_runtime.register_trace_sink(fn)`) makes viz a pure consumer and removes the last structural excuse for the viz↔server cycle after finding 2 lands.
- **Effort:** S. **Risk:** minimal — the ImportError fallback already proves the server runs without viz.

### 8. Flat-namespace sprawl (lower priority, do opportunistically)
- **Evidence:** 73 modules flat in `src/scix/` root (citation_* ×6, entity_* ×4, link_* ×3, section_* ×3 are obvious cohesion clusters); 268 test files flat in `tests/`; `scripts/` has 201 entries vs README's claimed 114 (153 touched since May, so mostly alive — the README number is simply stale).
- **Why it matters:** Discovery cost, not correctness. Fold moves into findings 2/3 rather than running a standalone big-bang re-org.
- **Effort:** M spread over time. **Risk:** low with re-export shims.

## Leave unchanged

- `db.py` and the DSN-guard/prod-safety architecture — exemplary; do not "improve".
- The Qdrant/pgvector split and ADR-013 validation rules — encode hard-won operational constraints.
- `mcp_contract.py` + conformance test + `_AliasTransform` deprecation machinery — this is the pattern findings 4 and 5 should copy, not replace.
- `sources/`, `jit/`, `eval/` subpackages — coherent boundaries, no action.
- `openalex.py` vs `sources/openalex.py` and `sync.py` vs `sync_manager.py` look like duplicates by name but are distinct concerns (API linking vs S3 snapshot ingest; ADS fetch vs freshness tracking); verified, not duplication.

## Risks of acting

Findings 1, 4, 7 are reversible and near-zero risk. Findings 2–3 touch the monkeypatch surface many tests rely on — the re-export facade pattern (already used post-`mcp_runtime` extraction) is the mitigation; keep shims one release, then delete. Finding 5 is a governance decision; the only risk is not making it.
