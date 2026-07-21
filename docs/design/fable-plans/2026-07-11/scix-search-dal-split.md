# scix `search.py` → read-side DAL decomposition plan

- Date: 2026-07-11 · Author: architect session (Fable 5)
- Target repo: `/home/ds/projects/scix_experiments` @ `452ab86` (2026-06-30). All line numbers below are pinned to that commit; re-derive with the grep commands given per step if the file has moved.
- Input: `/home/ds/gas-city/docs/arch-reviews/2026-07-10/scix.md`, finding 3 (also touches findings 1, 2, 7).
- Related beads: tracker `scix_experiments-vvsx` (extract.py duplicate deletion; mcp_server cycle fix). Interaction rules in §8.
- Executor profile: mid-tier worker agents. Every step is a pure-move PR with mechanical verification; the only PRs whose function bodies change at all are PR4, PR7, PR8, PR9, and each changed line is enumerated.

---

## 1. Baseline facts

`src/scix/search.py`: 4,849 lines, 66 top-level defs (64 functions + 2 classes), 37 module-level constants, 86 inline SELECTs, #2 churn file (60 commits since 2026-01). It is the read-side DAL for the whole system: search lanes, citation graph, communities, concepts/UAT, agent contexts, discovery orchestration, and fulltext/section reading.

Downward imports only (already at the bottom of the layer stack): `scix.db`, `scix.sources.ar5iv`, `scix.sources.licensing`, `scix.stubs`, plus function-local lazy imports (`scix.section_parser`, `scix.section_role`, `scix.ontology_query_parser`, `scix.alias_expansion`, `scix.coldtext.route`). **No `scix.mcp_*` import exists in search.py** — the review's 3 import cycles run through `mcp_handlers/search.py:28` (`from scix.mcp_server import _qdrant_tools`) and friends, not through this file. The split must preserve that property (enforced by a new layering test, PR1).

Consumer classes (who pins behavior):

| Consumer style | Who | Consequence for the split |
|---|---|---|
| Module-handle attribute calls (`from scix import search` … `search.hybrid_search(...)`) | `mcp_handlers/{search,paper,citation,entity,sections}.py`, `mcp_runtime.py` (lines 83, 393, 405–418), `mcp_server.py:57` (kept solely as patch surface), `scripts/measure_eager_enrichment_dryrun.py` | A facade module keeps every one of these working untouched, including `patch("scix.mcp_server.search.<fn>")` and `patch("scix.search.<fn>")` used by all `tests/test_mcp_*` files. |
| From-imports (`from scix.search import X`) | 27 scripts, `src/scix/benchmark.py`, `src/scix/eval/real_data.py`, `mcp_server.py:122` (`CrossEncoderReranker`), ~35 test files | Facade re-exports keep these working untouched. |
| Patches of an internal name while exercising a sibling function in the same module (relies on shared module globals) | `tests/test_search.py`, `tests/test_search_query_intent.py`, `tests/test_first_author_population.py`, `tests/test_snippet_wiring.py`, `tests/test_halfvec_migration.py`, `tests/test_mcp_server.py` (`_qdrant_dense_url` only) | **The only tests that ever need edits.** Each retarget is enumerated in the PR that moves the caller. |
| Logger pin | `tests/test_search.py` uses `caplog.at_level(..., logger="scix.search")` (2 sites) | Every new module hard-codes `logger = logging.getLogger("scix.search")` (see §5 rule L). |
| Direct module-global mutation | `tests/test_halfvec_migration.py:63-69` sets `search._HALFVEC_ENABLED` | Retarget to `scix.retrieval.dense` in PR3 (exact edit in PR3 table). |

Public surface actually imported outside the module (facade must carry these forever until retirement): `SearchResult`, `SearchFilters`, `lexical_search`, `lexical_search_body`, `vector_search`, `hybrid_search`, `rrf_fuse`, `community_expand_search`, `CrossEncoderReranker`, `concept_search`, `get_paper`, `get_citations`, `get_references`, `get_citations_batch`, `get_references_batch`, `get_author_papers`, `facet_counts`, `temporal_evolution`, `lit_review`, `citation_chain`, `co_citation_analysis`, `bibliographic_coupling`, `get_paper_metrics`, `explore_community`, `get_document_context`, `get_entity_context`, `get_citation_context`, `read_paper_section`, `search_within_paper`, `read_fulltext`, `read_fulltext_with_sibling_fallback`, `apply_snippet_budget_if_needed`, plus constants `LATEX_DERIVED_SOURCES` (re-export of `scix.sources.ar5iv`), `CONCEPT_DESCENDANT_MAX_DEPTH`, `CONCEPT_VOCABULARIES`, `SELECTIVITY_THRESHOLD`, `BGE_RERANKER_LARGE_{SHA,LOCAL_DIR}`, `INDUS_RANKER_{SHA,LOCAL_DIR}`, `_SEARCH_RERANK_MODEL_ALIASES`, `_LEXICAL_POOL_DEFAULT`, `_LEXICAL_POOL_UNBOUNDED`, `_LEXICAL_RANK_FLAG_DEFAULT`, `_TS_CONFIG_WHITELIST`, `_MAX_ALIAS_LEXICAL_LANES`, and test-touched internals `_elapsed_ms`, `_merge_filters`, `_normalize_vocabulary_arg`, `_positive_class_scores`, `_vector_search_qdrant`, `_qdrant_dense_url`, `_qdrant_dense_gated`, `_get_qdrant_dense_client`, `_estimate_filter_selectivity`, `_filter_first_vector_search`, `_resolve_lexical_pool`, `_resolve_lexical_rank_flag`, `_resolve_entity_ids_for_properties`, `_score_sections_ts_rank`, `_check_body_latex_provenance`, `_reset_section_rerank_cache`, `_get_arxiv_id_for_bibcode`, `_HALFVEC_ENABLED`. Decision: **the facade re-exports all 66 defs + all 37 constants** (rule F, §5) — no per-name judgment needed.

Never referenced outside the module (true internals, safe to drop from the facade at retirement, not before): `_normalize_seq`, `_citation_edge_query`, `_citation_edges_batch`, `_author_name_variants`, `_overlap_query`, `_fetch_query_mode_buckets`, `_community_column_for_signal`, `_taxonomic_community_id`, `_fetch_communities_for_paper`, `_lookup_concepts_unified`, `_fetch_uat_papers`, `_concept_search_lexical_fallback`, `_read_section_from_papers_fulltext`, `_section_snippet`, `_python_section_score`, `_positive_class_scores` (test-imported), `_resolve_section_rerank_model`, `_get_section_reranker`.

---

## 2. Cohesion clusters — all 66 defs mapped

Clustered by table/domain and by caller. Line ranges are `src/scix/search.py` @ 452ab86.

### C1 · core (types + shared plumbing) → `retrieval/core.py` (~200 lines)
| Def | Lines | Tables | Callers |
|---|---|---|---|
| `SearchResult` | 89–98 | — | everything; constructed in ~35 test files |
| `SearchFilters` | 99–196 | entities, document_entities_canonical (filter SQL builder) | all lanes, `mcp_runtime._parse_filters` |
| `_normalize_seq` | 197–227 | — | SearchFilters only |
| `_elapsed_ms` | 228–239 | — | every timed function; `scripts/eval_search_quality.py` |

Constants: `STUB_COLUMNS` (line 31; interpolated into SQL in every SELECT cluster).

### C2 · lexical lane → `retrieval/lexical.py` (~290 lines)
| Def | Lines | Tables |
|---|---|---|
| `_resolve_lexical_pool` | 275–320 | — |
| `_resolve_lexical_rank_flag` | 321–352 | — |
| `lexical_search` | 353–462 | papers (4 SELECTs) |
| `lexical_search_body` | 463–526 | papers |

Constants: `_TS_CONFIG_WHITELIST` (240), `_TSQUERY_MODES` (248), `_LEXICAL_POOL_DEFAULT` (267), `_LEXICAL_POOL_UNBOUNDED` (272), `_LEXICAL_RANK_FLAG_DEFAULT` (317), `_LEXICAL_RANK_FLAG_MASK` (318), `_BODY_TSVECTOR_MAX_BYTES` (460).
Callers: `hybrid_search`, `mcp_handlers/search.py:215`, `concept_search` fallback, 6 eval scripts, `tests/test_eval_lexical_recall_pool.py`.

### C3 · dense lane (pgvector + Qdrant, ADR-013) → `retrieval/dense.py` (~430 lines)
| Def | Lines | Tables |
|---|---|---|
| `_qdrant_dense_url` | 55–58 | — |
| `_qdrant_dense_gated` | 59–67 | — (called by `mcp_runtime.py:83`) |
| `_get_qdrant_dense_client` | 68–88 | — (Qdrant client cache) |
| `_vector_search_qdrant` | 527–618 | papers + Qdrant kNN |
| `vector_search` | 619–730 | paper_embeddings, papers |
| `_estimate_filter_selectivity` | 771–814 | papers, pg_class |
| `_filter_first_vector_search` | 815–904 | paper_embeddings, papers |

Constants: `_HALFVEC_ENABLED` (44), `_QDRANT_DENSE_COLLECTIONS` (51), `_qdrant_dense_client` mutable global (52), `SELECTIVITY_THRESHOLD` (768). Keeps the `configure_iterative_scan`/`IterativeScanMode` import from `scix.db`.
Pinning tests: `tests/test_search.py` Qdrant block (1507–1620), `tests/test_halfvec_migration.py`, `tests/test_filtered_hnsw.py` (script).

### C4 · fusion / hybrid orchestration → `retrieval/fuse.py` (~370 lines)
| Def | Lines | Notes |
|---|---|---|
| `rrf_fuse` | 731–770 | pure function |
| `_resolve_entity_ids_for_properties` | 905–937 | entities |
| `_merge_filters` | 938–976 | pure |
| `hybrid_search` | 977–1215 | calls C2+C3 (the one function with cross-module calls; PR4) |

Constants: `RRF_K` (34), `_MAX_ALIAS_LEXICAL_LANES` (898), `_MAX_PROPERTIES_FILTER_IDS` (902).
Pinning tests: `tests/test_search.py` (`TestHybridSearchLaneErrorHandling` ~line 640+), `tests/test_search_query_intent.py`, `tests/test_mcp_search*.py` (via facade).

### C5 · reranking → `retrieval/rerank.py` (~230 lines)
| Def | Lines |
|---|---|
| `_positive_class_scores` | 1542–1568 |
| `CrossEncoderReranker` | 1569–1670 |
| `_resolve_section_rerank_model` | 4139–4162 |
| `_get_section_reranker` | 4163–4180 |
| `_reset_section_rerank_cache` | 4181–4185 |

Constants: `BGE_RERANKER_LARGE_SHA` (1505), `BGE_RERANKER_LARGE_LOCAL_DIR` (1511), `INDUS_RANKER_SHA` (1518), `INDUS_RANKER_LOCAL_DIR` (1522), `_PINNED_RERANKERS` (1527), `_SUPPORTED_RERANKER_MODELS` (1535), `_SEARCH_RERANK_MODEL_ALIASES` (4132, the documented dup of the mcp_runtime alias table — see §8), `_section_rerank_cache` (4160).
Callers: `mcp_server.py:122` + `_get_default_reranker`, `search_within_paper`, 5 eval scripts, `tests/test_rerank.py`.

### C6 · paper lookup + aggregates → `retrieval/papers.py` (~250 lines)
| Def | Lines | Tables |
|---|---|---|
| `get_paper` | 1671–1715 | papers, citation_edges |
| `_author_name_variants` | 1847–1897 | — |
| `get_author_papers` | 1898–1958 | papers |
| `facet_counts` | 2431–2516 | papers (2 SELECTs) |

### C7 · citation graph → `retrieval/citations.py` (~360 lines)
| Def | Lines | Tables |
|---|---|---|
| `_citation_edge_query` | 1716–1752 | citation_edges, papers |
| `get_citations` / `get_references` | 1753–1776 | (thin wrappers) |
| `_citation_edges_batch` | 1777–1822 | citation_edges, papers |
| `get_citations_batch` / `get_references_batch` | 1823–1846 | (wrappers) |
| `_overlap_query` | 1959–2013 | citation_edges, papers |
| `co_citation_analysis` / `bibliographic_coupling` | 2014–2061 | (wrappers) |
| `citation_chain` | 2062–2181 | citation_edges, papers (recursive) |

Constants: `_EDGE_COLS` (1712), `_COUNT_ALIASES` (1713).

### C8 · communities → `retrieval/communities.py` (~640 lines)
| Def | Lines | Tables |
|---|---|---|
| `community_expand_search` | 1216–1541 | document_entities_canonical, entities, paper_metrics, papers (7 SELECTs) |
| `_community_column_for_signal` | 2806–2823 | — |
| `_taxonomic_community_id` | 2824–2835 | — |
| `_fetch_communities_for_paper` | 2836–2919 | communities, paper_metrics |
| `get_paper_metrics` | 2920–2959 | paper_metrics |
| `explore_community` | 2960–3092 | communities, paper_metrics, papers |

Constants: `_COOCCUR_NEIGHBOR_ECHO_CAP` (1206), `_COOCCUR_DEFAULT_SUPER_HUB_THRESHOLD` (1213), `_VALID_RESOLUTIONS` (2789), `_VALID_COMMUNITY_SIGNALS` (2796), `_RESOLUTIONS_BY_SIGNAL` (2799).
Pinning tests: `tests/test_search_community_expand.py`, `tests/test_mcp_community_signals.py`, `tests/test_mcp_search_community_expand.py`, `tests/test_eval_entity_value_props.py`.

### C9 · concepts / UAT → `retrieval/concepts.py` (~420 lines)
| Def | Lines | Tables |
|---|---|---|
| `_normalize_vocabulary_arg` | 3093–3118 | — |
| `_lookup_concepts_unified` | 3119–3255 | concepts, uat_concepts (8 SELECTs) |
| `_fetch_uat_papers` | 3256–3314 | paper_uat_mappings, papers, uat_relationships |
| `_concept_search_lexical_fallback` | 3315–3355 | (delegates to lexical lane) |
| `concept_search` | 3356–3489 | — (orchestrates the above) |

Constants: `CONCEPT_DESCENDANT_MAX_DEPTH` (3072), `CONCEPT_VOCABULARIES` (3077).
Pinning tests: `tests/test_concept_search_{router,fallback,descendants}.py`.

### C10 · agent contexts → `retrieval/contexts.py` (~230 lines)
| Def | Lines | Tables |
|---|---|---|
| `get_document_context` | 3490–3532 | agent_document_context |
| `get_entity_context` | 3533–3671 | agent_entity_context, entities, entity_relationships |
| `get_citation_context` | 3672–3718 | citation_contexts |

Pinning tests: `tests/test_mcp_citation_context.py`, `tests/test_mcp_entity_context_smoke.py`, `tests/test_mcp_paper_tools.py`.

### C11 · discovery orchestrators → `retrieval/discovery.py` (~545 lines)
| Def | Lines | Tables |
|---|---|---|
| `_fetch_query_mode_buckets` | 2182–2282 | communities, paper_metrics, papers |
| `temporal_evolution` | 2283–2430 | citation_edges, papers |
| `lit_review` | 2517–2805 | citation_contexts, communities, paper_metrics, papers (11 SELECTs); calls `hybrid_search`, references `get_references`/`get_citations` as first-class objects at ~2596 (`for fn in (get_references, get_citations):`) |

Constants: `ANCHORS_PER_BUCKET` (2177), `MAX_BUCKET_YEARS` (2178), `_NO_COMMUNITY_SENTINEL` (2179).
Pinning tests: `tests/test_search.py::TestLitReviewCitationExpansionErrorHandling`, `tests/test_first_author_population.py`, `tests/test_mcp_smoke.py`.

### C12 · section reading → `retrieval/sections.py` (~420 lines)
| Def | Lines | Tables |
|---|---|---|
| `_check_body_latex_provenance` | 3719–3765 | papers, papers_fulltext |
| `_read_section_from_papers_fulltext` | 3766–3904 | papers_fulltext |
| `read_paper_section` | 3905–4138 | papers (+ lazy `scix.section_parser`) |

### C13 · within-paper search → `retrieval/within_paper.py` (~340 lines)
| Def | Lines |
|---|---|
| `_section_snippet` | 4186–4216 |
| `_python_section_score` | 4217–4239 |
| `_score_sections_ts_rank` | 4240–4294 |
| `search_within_paper` | 4295–4522 |

### C14 · fulltext reading → `retrieval/fulltext.py` (~330 lines)
| Def | Lines | Tables |
|---|---|---|
| `_get_arxiv_id_for_bibcode` | 4523–4547 | papers |
| `apply_snippet_budget_if_needed` | 4548–4603 | papers_fulltext (via `enforce_snippet_budget`) |
| `read_fulltext` | 4604–4769 | papers_fulltext |
| `read_fulltext_with_sibling_fallback` | 4770–4849 | — (post-processes a prior result; does not call `read_fulltext`) |

Constants: `_NON_LATEX_FULLTEXT_SOURCES` (4767). Also re-exports `LATEX_DERIVED_SOURCES` from `scix.sources.ar5iv` (tests import it from `scix.search`; facade keeps it).
Pinning tests (C12–C14): `tests/test_body_adr006_guard.py`, `tests/test_search_within_paper_rerank.py`, `tests/test_section_role.py`, `tests/test_snippet_wiring.py`, `tests/test_sibling_fallback.py`, `tests/test_read_paper_response.py`, `tests/test_first_author_population.py`.

Def count check: 4+4+7+4+5+4+10+6+5+3+3+3+4+4 = **66** ✓. Module sizes all ≤ ~640 lines (house max 800).

---

## 3. Target package layout

```
src/scix/retrieval/
    __init__.py        # docstring ONLY — no re-exports (prevents a second god-surface;
                       # canonical import path is the submodule)
    core.py            # C1  SearchResult, SearchFilters, _elapsed_ms, _normalize_seq, STUB_COLUMNS
    lexical.py         # C2
    dense.py           # C3
    fuse.py            # C4  (imports lexical, dense as modules)
    rerank.py          # C5
    papers.py          # C6
    citations.py       # C7
    communities.py     # C8
    concepts.py        # C9  (imports lexical as module)
    contexts.py        # C10
    discovery.py       # C11 (imports fuse, citations as modules)
    sections.py        # C12 (imports fulltext as module)
    within_paper.py    # C13 (imports sections, fulltext, rerank as modules)
    fulltext.py        # C14

src/scix/search.py     # shrinks PR by PR into a pure re-export facade (~90 lines end-state)
```

Why a new `retrieval/` package + facade file, and NOT converting `search.py` into a `search/` package: the repo already has one byte-identical `extract.py` / `extract/` shadowing landmine (arch-review finding 1). Creating `search/` next to a lingering `search.py` reproduces that failure mode; a differently-named package makes it structurally impossible.

Intra-package dependency DAG (imports point down; enforced by test, §4c):

```
fuse ──▶ lexical, dense          discovery ──▶ fuse, citations
concepts ──▶ lexical             within_paper ──▶ sections, fulltext, rerank
sections ──▶ fulltext            everything ──▶ core
```

---

## 4. Characterization strategy (what pins zero-behavior-change)

Given 86 inline SELECTs, full golden-output DB tests for every function are not warranted — the moves are verbatim code motion, so the right gates are source-level identity plus the existing test pyramid. Three mechanical gates, all landing in PR1:

**(a) Pure-move AST gate — `scripts/check_pure_move.py`.** CLI: `check_pure_move.py --base <merge-base-rev> --map docs/refactor/search-dal/prN-moves.json`. The map file (checked in with each PR) lists `{"name": ..., "old": "src/scix/search.py", "new": "src/scix/retrieval/<mod>.py"}` for every moved def and constant. The script parses both trees (`git show <base>:<old>` vs worktree `<new>`), and asserts `ast.dump(node)` equality for each named top-level def/class/assignment. Functions with enumerated call-site rewrites (only: `hybrid_search` PR4, `_concept_search_lexical_fallback` PR7, `lit_review` PR8, `read_paper_section`+`search_within_paper` PR9) are passed via `--allow-changed <name>`; for those the reviewer checks the enumerated diff lines instead. This is a stronger guarantee than output snapshots and works with no DB.

**(b) SQL freeze snapshot — `tests/test_retrieval_sql_freeze.py`.** Walks the ASTs of `src/scix/search.py` + `src/scix/retrieval/*.py`, extracts every `str` constant and JoinedStr (f-string literal parts joined with `{}`) containing `SELECT`, normalizes whitespace, sorts, and compares to the checked-in snapshot `tests/data/retrieval_sql_freeze.txt` (generated once in PR1 with a `--regen` flag, never regenerated during the split). Any accidental SQL edit during a move fails CI. This is the "golden query" layer: it covers all 86 SELECTs at the query-text level, which is the actual regression surface of a move refactor. DB-output golden tests are NOT added — the existing `@pytest.mark.integration` tests in `tests/test_search.py` (lines 936, 988, 1094, 1132, 1287) already exercise the lanes against a live scix DB and must be run once per PR (see per-PR verify blocks).

**(c) Layering + facade tests.**
- `tests/test_retrieval_layering.py`: AST-walk every module in `scix/retrieval/`; assert no import of `scix.mcp_server`, `scix.mcp_runtime`, `scix.mcp_handlers`, `scix.viz`, or `scix.search`. This is what keeps the DAL split from ever joining the mcp import cycles.
- `tests/test_search_facade.py`: asserts (i) `scix.search` still exposes every name in the frozen list (all 66 defs + 37 constants + `LATEX_DERIVED_SOURCES` + `os`), and (ii) for already-moved names, identity: `scix.search.X is scix.retrieval.<mod>.X`. The name→module table is data in the test, extended each PR from §2.

**(d) Behavior-pinning test inventory per cluster** (run the listed files in the moving PR): C1→ `test_search.py`, all `test_mcp_*`; C2→ `test_search.py`, `test_eval_lexical_recall_pool.py`; C3→ `test_search.py`, `test_halfvec_migration.py`; C4→ `test_search.py`, `test_search_query_intent.py`, `test_mcp_search.py`, `test_mcp_search_unscoped_guard.py`, `test_mcp_search_disambig.py`; C5→ `test_rerank.py`; C6/C7→ `test_mcp_paper_tools.py`, `test_mcp_error_envelopes.py`, `test_mcp_tool_consolidation.py`, `test_session_working_set.py`; C8→ `test_search_community_expand.py`, `test_mcp_community_signals.py`, `test_mcp_search_community_expand.py`, `test_eval_entity_value_props.py`; C9→ `test_concept_search_{router,fallback,descendants}.py`; C10→ `test_mcp_citation_context.py`, `test_mcp_entity_context_smoke.py`, `test_mcp_paper_tools.py`; C11→ `test_search.py`, `test_first_author_population.py`, `test_mcp_smoke.py`; C12–C14→ `test_body_adr006_guard.py`, `test_search_within_paper_rerank.py`, `test_section_role.py`, `test_snippet_wiring.py`, `test_sibling_fallback.py`, `test_read_paper_response.py`.

---

## 5. Invariant rules for every PR (copy into each worker prompt)

- **F (facade):** `src/scix/search.py` keeps module docstring + `import os` (patch-string dependency of `tests/test_rerank.py:86,258`) + `from scix.sources.ar5iv import LATEX_DERIVED_SOURCES  # noqa: F401` + one `from scix.retrieval.<mod> import (...)  # noqa: F401` block per extracted cluster, listing every moved def AND constant. Nothing else is added; moved code is deleted from the file in the same commit.
- **T (types/constants):** cross-module use of types, constants, and non-patch-sensitive helpers (`SearchResult`, `SearchFilters`, `STUB_COLUMNS`, `_elapsed_ms`, `SELECTIVITY_THRESHOLD`, `PaperStub`, …) uses `from scix.retrieval.core import X` style from-imports so function bodies stay byte-identical.
- **M (module-attr calls):** cross-module calls to functions defined in another `retrieval/` module use module-object syntax (`from scix.retrieval import lexical` … `lexical.lexical_search(...)`). This gives every function exactly one patch point (its defining module) and preserves today's late-binding/monkeypatch semantics. The complete list of such call sites is enumerated in PR4/PR7/PR8/PR9 — no others exist.
- **L (logger):** every new module declares `logger = logging.getLogger("scix.search")  # historic name kept during DAL split (caplog + log-routing compat)`. Renaming to `__name__` is a post-split follow-up bead, not part of these PRs.
- **P (patch retargets):** a test patch string `"scix.search.X"` is rewritten to `"scix.retrieval.<mod>.X"` **only** when the function the test exercises has moved and resolves `X` outside the facade. Every such rewrite is enumerated per PR below; if a test not listed fails with "mock not called", stop and report — do not improvise.
- **V (verify block, every PR):** `python scripts/check_pure_move.py --base $(git merge-base HEAD origin/main) --map docs/refactor/search-dal/prN-moves.json [--allow-changed ...]` · `pytest -q -m "not integration"` (full suite) · `pytest -q -m integration tests/test_search.py` (needs live scix DB; run via `scix-batch` if heavy) · `pytest -q <cluster pinning files from §4d>`.
- **G (guards):** `grep -rn "from scix.mcp" src/scix/retrieval/` must be empty; `git diff --stat` must show `src/scix/search.py` shrinking; no edits under `src/scix/mcp_handlers/` in any PR of this plan.

---

## 6. PR sequence

Order is leaves-first so that not-yet-moved code in `search.py` keeps resolving moved names through the facade's from-imports (which preserves `patch("scix.search.X")` interception for still-unmoved callers — this is why retarget lists stay tiny until the caller itself moves).

### PR1 — scaffold, guard rails, core types (FIRST PR TO CUT)
- Create `src/scix/retrieval/__init__.py` (docstring only) and `src/scix/retrieval/core.py`: move `SearchResult`, `SearchFilters`, `_normalize_seq`, `_elapsed_ms`, `STUB_COLUMNS` (lines 31, 89–239) verbatim.
- Facade: replace moved code with `from scix.retrieval.core import STUB_COLUMNS, SearchFilters, SearchResult, _elapsed_ms, _normalize_seq  # noqa: F401`. All remaining search.py functions resolve these as bare globals — unchanged AST.
- Add `scripts/check_pure_move.py`, `tests/test_retrieval_sql_freeze.py` + `tests/data/retrieval_sql_freeze.txt`, `tests/test_retrieval_layering.py`, `tests/test_search_facade.py`, `docs/refactor/search-dal/pr1-moves.json` (specs in §4).
- Test retargets: **none**.
- Files touched: the 7 above + `src/scix/search.py`.

### PR2 — rerank (leaf, no SQL)
- Create `retrieval/rerank.py`: move defs `_positive_class_scores`, `CrossEncoderReranker` (1542–1670) and `_resolve_section_rerank_model`, `_get_section_reranker`, `_reset_section_rerank_cache` (4139–4185); constants `BGE_RERANKER_LARGE_SHA`, `BGE_RERANKER_LARGE_LOCAL_DIR`, `INDUS_RANKER_SHA`, `INDUS_RANKER_LOCAL_DIR`, `_PINNED_RERANKERS`, `_SUPPORTED_RERANKER_MODELS` (1505–1540), `_SEARCH_RERANK_MODEL_ALIASES` (4130–4137 incl. the keep-in-sync comment), `_section_rerank_cache` (4160).
- Facade block: all 13 names. `search_within_paper` (still in search.py) keeps calling bare `_get_section_reranker` via the facade import — works, and `patch`-free tests (`test_search_within_paper_rerank.py`, `test_section_role.py` use env vars + `search._reset_section_rerank_cache()` through the facade handle) pass unmodified.
- Test retargets: **none** (`test_rerank.py`'s `monkeypatch.setattr("scix.search.os.path.isdir", ...)` patches the real `os.path` through the facade's `import os`; still effective).
- Files: `retrieval/rerank.py`, `src/scix/search.py`, `docs/refactor/search-dal/pr2-moves.json`.

### PR3 — lexical + dense lanes
- Create `retrieval/lexical.py` (C2 defs+constants) and `retrieval/dense.py` (C3 defs+constants incl. mutable `_qdrant_dense_client`). Both import from `core`; `dense` keeps `from scix.db import IterativeScanMode, configure_iterative_scan`.
- Facade block: all 11 defs + 11 constants.
- `hybrid_search`, `concept_search`, `mcp_handlers`, and every script still resolve through the facade — no call-site edits anywhere in this PR (bodies stay AST-identical, including `hybrid_search`).
- Test retargets (complete list):
  1. `tests/test_search.py` Qdrant block (~1507–1620): `patch("scix.search._vector_search_qdrant")` ×1, `patch("scix.search._get_qdrant_dense_client")` ×6, `patch("scix.search._qdrant_dense_url")` ×2 (`localhost:6633` variants) → same name under `scix.retrieval.dense.` (these tests call `vector_search`/`_qdrant_dense_gated`, which now resolve within `dense.py`). The six `from scix.search import _vector_search_qdrant` local imports in the same block may stay (facade re-export).
  2. `tests/test_mcp_server.py`: `@patch("scix.search._qdrant_dense_url", return_value=None)` ×3 → `"scix.retrieval.dense._qdrant_dense_url"` (code under test reaches `_qdrant_dense_gated`, which resolves the URL fn in `dense.py`).
  3. `tests/test_halfvec_migration.py:63–69`: `search._HALFVEC_ENABLED` save/set/restore → operate on `scix.retrieval.dense` (add `from scix.retrieval import dense` and use `dense._HALFVEC_ENABLED`).
- Files: `retrieval/lexical.py`, `retrieval/dense.py`, `src/scix/search.py`, the 3 test files, `pr3-moves.json`.

### PR4 — fuse (hybrid orchestration) — first body-change PR
- Create `retrieval/fuse.py`: move `rrf_fuse`, `_resolve_entity_ids_for_properties`, `_merge_filters`, `hybrid_search` + constants `RRF_K`, `_MAX_ALIAS_LEXICAL_LANES`, `_MAX_PROPERTIES_FILTER_IDS`. Header: `from scix.retrieval import dense, lexical` + core from-imports + `from scix.retrieval.dense import SELECTIVITY_THRESHOLD`.
- Enumerated rewrites inside `hybrid_search` body only (all occurrences of each; counts at 452ab86): `lexical_search(` → `lexical.lexical_search(` ×2 · `lexical_search_body(` → `lexical.lexical_search_body(` ×1 · `vector_search(` → `dense.vector_search(` ×1 · `_filter_first_vector_search(` → `dense._filter_first_vector_search(` ×1 · `_estimate_filter_selectivity(` → `dense._estimate_filter_selectivity(` ×1 · `_qdrant_dense_gated(` → `dense._qdrant_dense_gated(` ×1. Same-module calls (`rrf_fuse`, `_merge_filters`, `_resolve_entity_ids_for_properties`) stay bare. Run gate with `--allow-changed hybrid_search`.
- Test retargets (complete list — tests that call `hybrid_search` directly while patching a lane):
  1. `tests/test_search.py::TestHybridSearchLaneErrorHandling` (~640–700): patches of `lexical_search` ×3 and `lexical_search_body` ×3 → `scix.retrieval.lexical.*`.
  2. `tests/test_search.py` filter-first/selectivity tests: `patch("scix.search._estimate_filter_selectivity")` ×3, `patch("scix.search._filter_first_vector_search")` ×1, `patch("scix.search.vector_search")` (only instances whose test calls `hybrid_search`; grep the file for `hybrid_search(` within each `with patch` scope) → `scix.retrieval.dense.*`; `patch("scix.search.lexical_search"...)` in the same scopes → `scix.retrieval.lexical.lexical_search`.
  3. `tests/test_search_query_intent.py`: `patch("scix.search.lexical_search")` ×~7 → `scix.retrieval.lexical.lexical_search`; `patch("scix.search._resolve_entity_ids_for_properties")` ×1 → `scix.retrieval.fuse._resolve_entity_ids_for_properties`.
  - NOT retargeted: every `test_mcp_*` patch of `hybrid_search`/`lexical_search` (handlers call through the facade handle) and `tests/test_search.py::TestLitReviewCitationExpansionErrorHandling` (`lit_review` still lives in search.py and resolves `hybrid_search` via the facade global).
- Files: `retrieval/fuse.py`, `src/scix/search.py`, 2 test files, `pr4-moves.json`.

### PR5 — papers + citation graph
- Create `retrieval/papers.py` (C6) and `retrieval/citations.py` (C7 + `_EDGE_COLS`, `_COUNT_ALIASES`). Pure moves; both import only `core` + `scix.stubs`.
- Facade block: 14 defs + 2 constants.
- Test retargets: **none** (all patches of `get_paper`/`get_citations`/`get_references`/`facet_counts`/batch variants live in `test_mcp_*`/`test_session_working_set.py` and exercise handlers through the facade; `lit_review`'s `for fn in (get_references, get_citations)` still resolves facade globals until PR8).
- Files: 2 new modules, `src/scix/search.py`, `pr5-moves.json`.

### PR6 — communities
- Create `retrieval/communities.py` (C8 defs + 5 constants). Pure moves; intra-module call `get_paper_metrics → _fetch_communities_for_paper` stays bare.
- Test retargets: **none** (`test_eval_entity_value_props.py` patches `scix.search.community_expand_search` but the code under test does a function-local `from scix.search import community_expand_search` at call time → facade attr, still intercepted; all others are handler-path).
- Files: 1 new module, `src/scix/search.py`, `pr6-moves.json`.

### PR7 — concepts (one-line body change)
- Create `retrieval/concepts.py` (C9 + 2 constants). Header: `from scix.retrieval import lexical`.
- Enumerated rewrite: in `_concept_search_lexical_fallback`, `lexical_search(` → `lexical.lexical_search(` ×1. Gate: `--allow-changed _concept_search_lexical_fallback`.
- Test retargets: **none** (`test_concept_search_fallback.py` patches nothing on scix.search; router/descendants tests import through the facade).
- Files: 1 new module, `src/scix/search.py`, `pr7-moves.json`.

### PR8 — contexts + discovery (two-site body change)
- Create `retrieval/contexts.py` (C10, pure move) and `retrieval/discovery.py` (C11 + 3 constants). Header of discovery: `from scix.retrieval import citations, fuse`.
- Enumerated rewrites in `lit_review` only: `hybrid_search(` → `fuse.hybrid_search(` ×1 (line ~2580 rel.) · `for fn in (get_references, get_citations):` → `for fn in (citations.get_references, citations.get_citations):` ×1 (line ~2596 rel.). `temporal_evolution → _fetch_query_mode_buckets` is same-module, stays bare. Gate: `--allow-changed lit_review`.
- Test retargets (complete list):
  1. `tests/test_search.py::TestLitReviewCitationExpansionErrorHandling` (~703–790): `patch("scix.search.hybrid_search")` ×2 → `scix.retrieval.fuse.hybrid_search`; `patch("scix.search.get_references")` ×2 and `patch("scix.search.get_citations")` ×1 → `scix.retrieval.citations.*`. The `caplog.at_level(..., logger="scix.search")` lines stay (rule L keeps the logger name).
  2. `tests/test_first_author_population.py`: `patch("scix.search.hybrid_search")` ×1 (exercises `lit_review`) → `scix.retrieval.fuse.hybrid_search`.
- Files: 2 new modules, `src/scix/search.py`, 2 test files, `pr8-moves.json`.

### PR9 — sections + within-paper + fulltext (final moves)
- Create `retrieval/fulltext.py` (C14; keeps `from scix.sources.ar5iv import _ARXIV_ID_RE, LATEX_DERIVED_SOURCES, _build_canonical_url` and `from scix.sources.licensing import enforce_snippet_budget`), `retrieval/sections.py` (C12; header `from scix.retrieval import fulltext`; also imports `LATEX_DERIVED_SOURCES` from ar5iv), `retrieval/within_paper.py` (C13; header `from scix.retrieval import fulltext, rerank, sections`).
- Enumerated rewrites — in `read_paper_section` only: `_get_arxiv_id_for_bibcode(` → `fulltext._get_arxiv_id_for_bibcode(` (all occurrences in that def) · `apply_snippet_budget_if_needed(` → `fulltext.apply_snippet_budget_if_needed(` (all) · same-module `_check_body_latex_provenance`/`_read_section_from_papers_fulltext` stay bare. In `search_within_paper` only: `_check_body_latex_provenance(` → `sections._check_body_latex_provenance(` ×1 · `_get_arxiv_id_for_bibcode(` → `fulltext._get_arxiv_id_for_bibcode(` ×1 · `apply_snippet_budget_if_needed(` → `fulltext.apply_snippet_budget_if_needed(` ×1 · `_get_section_reranker(` → `rerank._get_section_reranker(` (all, ×~2). `read_fulltext` and `read_fulltext_with_sibling_fallback` are pure moves (all their callees are same-module). Gate: `--allow-changed read_paper_section --allow-changed search_within_paper`.
- Test retargets (complete list):
  1. `tests/test_snippet_wiring.py`: `patch("scix.search._get_arxiv_id_for_bibcode")` ×1 (exercises `read_fulltext`, same module as callee) → `scix.retrieval.fulltext._get_arxiv_id_for_bibcode`.
  - NOT retargeted: `test_body_adr006_guard.py` (patches only `hybrid_search`, unrelated path), `test_search_within_paper_rerank.py`/`test_section_role.py` (env + `search._reset_section_rerank_cache()` facade calls, no patches), `test_sibling_fallback.py`, `test_read_paper_response.py` (from-imports only), `tests/test_mcp_paper_tools.py`/`test_mcp_smoke.py` (handler path via facade).
- After this PR `src/scix/search.py` is facade-only (~90 lines). Run the FULL verify block plus `pytest -q tests/test_mcp_server.py tests/test_mcp_smoke.py`.
- Files: 3 new modules, `src/scix/search.py`, 1 test file, `pr9-moves.json`.

### PR10 — seal + de-duplicate the rerank alias table (coordinate with cycle-fix bead; see §8)
- Preconditions: PR1–PR9 merged AND the `scix_experiments-vvsx` mcp_server cycle-fix bead merged.
- Make `retrieval/rerank.py:_SEARCH_RERANK_MODEL_ALIASES` the single source: in `mcp_runtime.py`, replace its `_RERANK_MODEL_ALIASES` definition with `from scix.retrieval.rerank import _SEARCH_RERANK_MODEL_ALIASES as _RERANK_MODEL_ALIASES` (direction is legal: runtime already imports the DAL; retrieval never imports runtime — layering test proves it). Delete the "Keep in sync" comment.
- Add deprecation docstring to `scix/search.py` ("compat facade; new code imports scix.retrieval.<module>"); update `CLAUDE.md`/`AGENTS.md` module map; file a follow-up bead "retire scix.search facade after one release" listing the 36 known importer files (grep output from §1 baked into the bead).
- No behavioral gates change; run full suite + contract conformance test.

Rollback story: every PR is a revert-safe single commit; reverting PR N restores facade-resolution for its cluster with no effect on later clusters (later PRs only depend on earlier modules existing — revert in reverse order only).

---

## 7. What was deliberately left alone

- `mcp_handlers/*` — zero edits in this plan; the handler seam (already split search/citation/sections/entity/paper) is what the DAL layout mirrors.
- Logger names, timing contract (`SearchResult.timing_ms`), `STUB_COLUMNS` SQL shape — frozen by rules L + gate (b).
- `scripts/` (27 importers) and `benchmark.py`/`eval/real_data.py` — untouched; facade covers them until the retirement bead.
- Renaming internals, merging thin wrappers, or any net-negative simplification inside moved functions — explicitly out of scope for PR1–PR9 (breaks the AST gate); collect candidates in the PR10 follow-up bead.

## 8. Interaction with the mcp_server cycle fix (arch-review finding 2, bead under scix_experiments-vvsx)

- **File-overlap: none.** The cycle fix edits `mcp_server.py`, `mcp_runtime.py`, `mcp_handlers/{search,paper,claim,entity,sections}.py`, `find_replications.py`, `claim_blame.py`. This plan edits `src/scix/search.py`, new `src/scix/retrieval/*`, and 8 test files (retarget lists). The two streams can land in any interleaving **except PR10**, which must land last.
- **Shared invariants:** (i) `mcp_runtime.py` may keep `from scix import search` and its `search.SearchFilters` / `search._qdrant_dense_gated` attribute calls throughout — the facade guarantees them; the cycle-fix bead must NOT "helpfully" repoint them to `scix.retrieval.*` mid-flight (that is PR10-adjacent cleanup, done after both streams settle). (ii) `mcp_server.py:122` `from scix.search import CrossEncoderReranker` stays — tests patch `mcp_server.CrossEncoderReranker`, and the facade keeps that import valid. (iii) The DAL never imports `scix.mcp_*` (gate 4c); therefore no PR here can create a fourth cycle, and killing the existing three is entirely the other bead's job.
- **extract.py deletion bead:** fully independent; land it whenever. Its lesson is already encoded here (§3: new package name, no `search/` shadow).
- **One deliberate hand-off:** the duplicated rerank alias table (`search.py:4130` "Keep in sync" ↔ runtime `_RERANK_MODEL_ALIASES`) is resolved in PR10 by inverting the dependency downward, which is only safe after the cycle fix has made `mcp_runtime` the settled neutral layer.

---

## Executive summary

1. `search.py` (4,849 lines / 66 defs / 86 SELECTs) splits into a 14-module `scix/retrieval/` package clustered by table+caller (core, lexical, dense, fuse, rerank, papers, citations, communities, concepts, contexts, discovery, sections, within_paper, fulltext), with `scix.search` kept as a full re-export facade so all 36 importers and every `patch("scix.search.*")`/`scix.mcp_server.search` surface keep working.
2. Zero-behavior-change is enforced mechanically: an AST pure-move gate (`check_pure_move.py`), an 86-SELECT SQL-freeze snapshot test, a layering test banning `scix.mcp_*` imports in the DAL, and a facade-surface test — plus the existing integration-marked lane tests; only 5 functions change bodies at all, each rewritten call site enumerated.
3. Nine move-PRs run leaves-first (core → rerank → lanes → fuse → papers/citations → communities → concepts → contexts/discovery → sections/fulltext) so unmoved code keeps resolving through the facade and test patch retargets stay tiny and fully enumerated (8 test files total).
4. The mcp_server cycle-fix bead and this split touch disjoint files by construction (handlers are never edited here); the only ordered dependency is PR10, which de-duplicates the rerank alias table downward after the cycle fix lands.
5. First PR to cut: **PR1** — `retrieval/` scaffold + `core.py` (SearchResult/SearchFilters/_elapsed_ms/_normalize_seq/STUB_COLUMNS) + all four guard rails, no test edits, immediately reviewable and revertible.
