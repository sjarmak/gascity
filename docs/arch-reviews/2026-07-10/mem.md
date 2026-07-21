# Architecture Review — mem (/home/ds/projects/mem)

Date: 2026-07-10 · HEAD: c3dad5c · Read-only review (repo-architecture-review skill)

## Executive summary

Two-half system: a TypeScript work-audit-graph builder (`src/`, 78 files, ~10.6k LOC, zod + better-sqlite3 only) and a Python eval harness (`memory-bench/membench`, 192 files, ~29k LOC, plus 41 scripts / 9.5k LOC). The TS half has clean acyclic layering and a genuinely shared CLI framework; the Python half has a healthy memory-system ABC/registry and one exemplary subprocess seam (`mem_cli.py`). Structural complexity concentrates in three places: the hand-synced WorkRecord column-projection cluster (the repo's six highest-churn files), the cross-language WorkRecord contract that has a zod schema on the TS side but zero Python model (35 stringly-typed access sites in 22 files), and a `scripts/` directory that has become a second, CI-gate-exempt package with four forked grid runners. The top three fixes are all S–M effort and each removes a whole class of future breakage rather than one instance.

---

## Architecture as found

- **Pipeline (TS):** `ingest/ → parse/ → store/ (SQLite+FTS5 projection of WorkRecord JSON) → retrieve/`, orchestrated by `cli/commands/build-store.ts`. Dependency arrows point down; `schemas/` is a pure leaf; no cycles; no `store→cli` or `ingest→retrieve` violations. Minor edges: `store→parse/recurrence` (signature algo shared with persistence) and `ingest/provenance-from-log.ts→store` (read-first path).
- **Eval harness (Python):** ~20 subpackages; `schemas` is the god-hub (117 intra-package imports), `memory_systems` (68) and `grading` (49) secondary; `harbor/` is the widest consumer (pulls 7 subpackages). Two runtime cycles exist (`schemas↔bundle`, `report↔generators`).
- **Cross-language boundary:** Python shells the TS CLI via a single owned seam (`membench/mem_cli.py`) unwrapping the `{apiVersion, cmd, ok, data, errors}` envelope; zero seam violations found.
- **Docs:** ARCHITECTURE.md is unusually candid (planned items labeled planned); LikeC4 model + daily-regenerated orient.md exist. One genuine overclaim found (memory-types "represented in the store").
- Repo is 414 commits old, all within ~5 weeks — high churn is expected; the findings below are where churn is *structural*, not incidental.

---

## Ranked findings (ROI = reach ÷ effort, discounted by risk)

### 1. WorkRecord has no Python-side model — 35 stringly-typed access sites against a cross-language contract
- **Evidence:** No `class WorkRecord` anywhere in `memory-bench/`. `record["work_id"]` (15×), `record["rig"]` (11×), plus `outcome/title/metadata/latent_rule/episodes/fork/task_id` string-key access across ~22 files / 35 sites; even `membench/*/workrecord_adapter.py` does raw dict access. Contrast: the envelope is parsed in exactly one place (`membench/mem_cli.py`), and `schemas/` already mirrors the eval-spec and codeprobe task model — WorkRecord is the one boundary object left unmodeled. TS side is fully typed (`src/schemas/workrecord.ts:218-257`, zod, parsed on write `src/store/writer.ts:286` and read `src/store/reader.ts:20-22`).
- **Why it matters:** Every TS-side field rename/removal fails as scattered runtime `KeyError`s in the eval harness with no single validation point. A pydantic `WorkRecord` parsed once at the `mem_cli` seam turns cross-language drift into one loud, typed failure and gives mypy --strict leverage over the harness's core data object.
- **Effort:** S–M (one model + parse at the seam; migrate call sites incrementally). **Risk:** Low — additive, reversible; strict-parse can start permissive (`extra="allow"`).

### 2. WorkRecord projection cluster is shotgun surgery — one queryable field = 6 files / ~9 edit sites, hand-synced SQL in 4 places
- **Evidence:** Adding a queryable field touches `src/schemas/workrecord.ts`, `src/store/schema.ts` (DDL column list :31-91 + `SCHEMA_VERSION` bump :28), `src/store/writer.ts` **three times** (INSERT :21-27, VALUES :29-34, ON CONFLICT SET :37-51) plus `toRow()` :88-124, `src/store/reader.ts` filter wiring :36-70, `src/cli/commands/build-store.ts` counters/notes :42-96/:351-369, and the ingest stage. Co-change proof: 5 commits in 10 weeks touched 3+ of {writer, reader, workrecord, build-store}; 4 hit 4-of-5 in one commit (`ff04f32`, `2461c52`, `3f89344`, …). The six top-churn files in the repo (writer 18, build-store 16, store/index 14, schema 13, workrecord 13, reader 11 changes / 6 wk) are exactly this cluster. Each promotion also forces a schema-version bump → full rebuild + export/import round-trip for the append-only tables.
- **Why it matters:** This is the hottest edit path in the repo and it is entirely hand-synchronized. A single declarative column-spec table (name, SQL type, JSON extractor, filterable?) from which DDL, INSERT/VALUES/ON-CONFLICT, `toRow()`, and reader filters are derived collapses ~9 edit sites to 1–2 and removes the silent-desync failure mode between `schema.ts` and `writer.ts`.
- **Effort:** M. **Risk:** Medium-low — store is a rebuildable projection by design (rebuild is already routine per version bump); behavior is verifiable against the existing 358 TS tests.

### 3. `scripts/` is a second package outside the quality gate, with four forked grid runners
- **Evidence:** CI runs `ruff check membench tests`, `black --check membench tests`, `mypy --strict membench` (`.github/workflows/ci.yml:25-28`) — **9.5k LOC in `scripts/` escapes lint, format, and strict typing entirely**, while tests load them via `importlib.util.spec_from_file_location` file hacks (`tests/test_run_grid_3arm.py:22-28`). The grid runners are experiment-flavor forks of one runner: `run_grid.py` (187 LOC) / `run_grid_3arm.py` (474) / `run_grid_3arm_ftp.py` (298) / `run_grid_3arm_graded.py` (478); each re-declares the same 6–7 constants (`PROJECT_ROOT`…`DEFAULT_CLI_VERSION`; e.g. `run_grid_3arm.py:77-90` vs `run_grid_3arm_graded.py:94-111`); 115 identical lines between 3arm and graded; and the pin-and-classify exec sequence is forked — `graded` calls `probe_gate.pinned_stream_exec` (`:301`) while `ftp` hand-rolls its own `exec_stream()` (`run_grid_3arm_ftp.py:194`). `build_headline_report.py` (646 LOC, 25 defs) hosts report tabulation parallel to the `membench/report/` package.
- **Why it matters:** Every new experiment flavor currently means forking another 300–500 LOC runner that then drifts (the ftp/graded exec fork is the drift already happening), and none of it is type-checked despite carrying gating/validity logic the headline numbers depend on. Moving the shared orchestration into `membench` (one parameterized runner + one defaults module) and adding `scripts` to the CI gate converts scripts back into thin entrypoints.
- **Effort:** M (gate extension itself is S and can land first). **Risk:** Low — scripts are already exercised by tests; consolidation is behavior-preserving.

### 4. Subprocess boundaries un-owned for git/docker/claude while `mem` shows the working pattern
- **Evidence:** `mem_cli.py` is a genuinely clean single seam (zero violations found; 3 importers). But: git/worktree shelling in **13 files** with `_add_worktree`/`_remove_worktree` logic triplicated across `harbor/probe_gate.py`, `harbor/repro_live.py`, `harbor/ftp_repro.py`; claude/harbor agent invocation in **13 files**; docker in 3 (`harbor/base_image.py`, `harbor/ftp_curate.py`, `harbor/harbor_exec.py`). 24 `subprocess.run/Popen` sites across 19 files total.
- **Why it matters:** The `mem` seam exists precisely because bare subprocess failure modes (missing binary, hang, exit-0-garbage) shouldn't be re-derived per call site — the same argument applies to worktree lifecycle (leak-prone: stale worktrees already need `sweep_probe_worktrees`) and agent execution. One `git_worktree.py` seam alone removes the triplication.
- **Effort:** S–M (worktree seam S; agent-exec seam M). **Risk:** Low — mechanical extraction with an in-repo template to copy.

### 5. Two dependency cycles puncture the schemas leaf — each breakable by moving one symbol
- **Evidence:** `membench/schemas/bundle.py:34` imports `from membench.bundle.replay import ReplayResult` while `bundle/` imports `schemas` throughout → the 117-importer god-hub is not actually a leaf. `report↔generators`: `report/factorial_behavioral.py:38` imports generators; `generators/memory_necessity_gate.py:23`, `pilot_filter.py:18`, `retrieval_discrimination_gate.py:27` import back `report.comparison.EPSILON` / `build_comparison`.
- **Why it matters:** With `schemas` imported 117×, a true-leaf guarantee is what keeps import order, refactors, and future package splits trivial; both cycles are one-symbol relocations (`ReplayResult` down into schemas; `EPSILON`/`build_comparison` into a shared leaf).
- **Effort:** S. **Risk:** Minimal, fully reversible.

### 6. Ingest pipeline DAG is implicit in one flag-branched block of a CLI command
- **Evidence:** 17 `src/ingest/` files expose ad-hoc `attachX(records) → records` functions with no stage interface or registry; stage order and its constraints live as comments inside `src/cli/commands/build-store.ts:293-345` ("Runs before typing/traces so the resolved repo is present"), with the effective pipeline shape depending on `--with-traces`/`--with-provenance` flags. `build-store.ts` imports 12 ingest modules directly.
- **Why it matters:** Every new ingest source (the fastest-growing module: 17 files and counting) is wired by editing a 50-line nested-call block whose ordering invariants are unenforced. A declared stage list (name, deps/ordering, enabled-by flag) makes the DAG inspectable and makes mis-ordering a startup error instead of silent bad data.
- **Effort:** M. **Risk:** Low-medium — pure restructuring of composition; covered by the existing build-store/ingest tests.

### 7. `harbor/` re-implements `runner/`'s core vocabulary; `probe_gate.py` (1061 LOC) mixes 5 concerns
- **Evidence:** Two parallel "condition" models — `runner/conditions.py` uses `schemas.conditions.Condition` (`:192-238`) while harbor defines its own (`harbor/control_conditions.py` `ControlPayload`/payload builders, `shuffled_condition.py`) — and two execution seams (`runner/headless_agent.py` vs `harbor_stream_exec` at `probe_gate.py:741`). `probe_gate.py` bundles probe-task construction/leak-stripping, run I/O + pin validation, the exec seam, git-worktree lifecycle, and scoring/statistics (~35 top-level symbols).
- **Why it matters:** "Condition" is the eval's central concept; two vocabularies means every new arm/experiment picks a side and cross-comparisons need adapters. The probe_gate split (5 natural files) plus a single Condition model is the consolidation that stops harbor from becoming a parallel harness.
- **Effort:** L. **Risk:** Medium — this code is mid-experiment; do it after findings 3–4 shrink harbor's surface, and stage it (split first, unify vocabulary second).

### 8. `src/bench/` is a library with no runtime consumer
- **Evidence:** 5 files / ~830 LOC of leakage-gate predicates (temporal wall, diff-overlap, LOO-dedup, session fan-out); imported by nothing outside `tests/bench*.test.ts` — zero CLI commands, and the Python harness that was meant to consume it never wired it (`bench/index.ts:1-10` says "to be composed by mem-wanz.9").
- **Why it matters:** Built-ahead code either gets wired (exposed via a `mem bench-gate` CLI command the Python side calls through the existing seam) or drifts from the Python-side validity logic it duplicates conceptually (membench has its own leak_guard/validity modules). Decide; don't carry both indefinitely.
- **Effort:** S (decision + either one CLI command or deletion). **Risk:** Low.

### 9. Doc-vs-code drift: memory-type taxonomy claimed in the store, present only in harness enums
- **Evidence:** ARCHITECTURE.md:189-191 ("The store represents these distinctly and logs which types a query retrieved") — but `src/store/schema.ts` (v11) has no memory_type/representation/storage_tier column anywhere; the taxonomy exists only as pydantic enums in `membench/schemas/candidate_memory.py:13-26`. Also stale: README/docs say "schema v6"; code is v11 (`src/store/schema.ts:28`). Everything else checked was honestly labeled (MCP controller doc-only but marked "Designed"; OpenRath adapter built+tested, unwired, marked "in progress"; OTel/ATIF telemetry actually implemented).
- **Why it matters:** ARCHITECTURE.md's candor is a real asset for a rig where agents orient from docs; the one overclaim sits exactly on the store contract agents rely on. One-paragraph fix.
- **Effort:** S. **Risk:** None.

### 10. Working-tree litter: ~69 abandoned agent-worktree stub dirs at repo root
- **Evidence:** `git worktree list` shows 88 worktrees, all real ones siblings at `/home/ds/projects/mem-*`; the ~69 in-repo `mem-*/` dirs are strays holding only gitignored `.claude/`+`.gc/` (no `.git` file), invisible to `git status` but dominating every `ls` of the root. Both `worktrees/` and `.worktrees/` exist, empty. 86 MB untracked `.mem/store.db` is fine (ignored by design); the stubs are not.
- **Why it matters:** Operational rather than architectural, but every agent (and every future review) pays an orientation tax on a root listing that is 70% junk; the stubs also mask genuinely new top-level dirs. A sweep + whichever launcher is creating in-repo stubs fixed at the source.
- **Effort:** S. **Risk:** Low (verify each stub has no un-pushed content before removal — read-only this pass; flagged only).

---

## Major strengths (protect these)

- **TS layering is clean and acyclic** — `schemas/` a pure leaf, arrows point one way, no cli/store/ingest violations.
- **`mem_cli.py` seam** — the model subprocess boundary; zero violations in the whole harness.
- **`memory_systems/` design** — one ABC (`base.py:77`), a shared semantic-arm base, a single-dict registry whose 18×/6wk churn is purely additive registration.
- **CLI framework** — registry + centralized envelope/error handling (`src/cli/index.ts:72-105`) + store-lifecycle wrappers (`src/cli/store.ts`); command bodies genuinely thin.
- **Eval-validity discipline as code** — strict `closedBefore` in `src/store/reader.ts`, exclusions in `src/retrieve/exclusions.ts`, write-time+read-time zod parsing.
- **Honest docs + full CI gates in both languages**; no generated artifacts committed.

## Leave unchanged

- Flat test directories (43 TS / 166 py files): a discoverability cost only; filename convention works and CI coverage is complete. Not worth a reshuffle now.
- `store→parse/recurrence` edge: deliberate (projections computed at write time); a signature-format change touching both layers is acceptable coupling.
- `assemble_batch.py` / `admit_batch_guarded.py`: orchestration over membench functions, not duplication — fine as scripts once finding 3's gate extension covers them.
- The denormalized projection design itself (JSON source-of-truth + rebuilt columns): sound; finding 2 targets the hand-sync mechanics, not the design.
