# Architecture Review — EnterpriseBench (2026-07-10)

## Executive summary

EnterpriseBench's stated core invariant — "one verifier library, no per-task verifier copies, ever" — has drifted badly: 705 per-task bash check scripts carry the actual scoring logic, 29 of them are explicit hand-ports of `eb_verify` plugins, and only 33 call the library at all. Checkpoint aggregation is implemented twice (bash `test_runner.sh` with grep/awk JSON parsing vs Python `eb_verify.scoring.compute_score`), and the production path is the bash one. Scoring and LLM-judge application are welded to docker inside the 2,121-line `run_task.py` (the #1 churn hotspot), so every rescore campaign requires monkeypatching `_docker_exec` — three such one-off scripts already exist. Supporting duplication follows the same gradient: 12 independent task.toml loaders, 22 independent results.json parsers, 30+ `sys.path.insert` hacks. The highest-ROI moves all pull verification and results-reading back into the installed library; the plugin registry, scorer trust boundary (`scorer_guard`), layered ground truth, and CI integrity corpus are strong foundations to consolidate onto.

## How the system is actually structured

- **Task corpus**: `benchmarks/<suite>/<task>/` — `task.toml` (canonical definition), `instruction.md`, `ground_truth.json`, `expected_solution.json`, `checks/*.sh` (per-checkpoint verifiers), `environment/Dockerfile{,.hybrid,.sg_only}` (committed generated artifacts, 372 total).
- **Library**: `lib/eb_verify` (5.7K LOC — task_parser, scoring, runner, scorer_guard, groundedness, 11 plugins, judge/) and `lib/eb_metrics` (1.4K LOC), packaged via `lib/pyproject.toml`, installed in CI.
- **Harness**: `scripts/` (25.5K LOC) — `orchestration/run_task.py` is the single-task engine (docker build/exec, MCP config, instruction assembly, scoring, judge, results serialization); `run_benchmark.py`/`run_sweep.py` fan out via subprocess; `chain_runner.py`/`event_replay.py` handle other session types.
- **Production scoring path**: task.toml → run_task copies `checks/` + weight `.meta` files + docker-cp's `lib/eb_verify` into `/workspace/.eb_verify` → bash `test_runner.sh` runs checks and aggregates weighted score in awk → `scorer_guard.guard_verifier_output` (trust boundary) → `_apply_llm_judge` caps grep score with judge score → results.json.
- **Analysis**: `Makefile` pipeline (`analyze → charts → report → paper-figures`) over `results/`, plus a growing pile of one-off rescore scripts in `scripts/analysis/`.

The Python `CheckpointRunner` (`lib/eb_verify/runner.py`, `cli.py`) exists as the intended verification engine but is exercised only by the CLI and tests — production runs the bash path.

## Major strengths (protect these)

- Plugin registry with a real `Protocol` contract and `safe_read` byte caps (`lib/eb_verify/plugins/__init__.py`).
- `scorer_guard` as an explicit scorer trust boundary — infra errors distinguished from legitimate 0.0 (commit d64dfbc closed 3 silent-misscore bugs).
- CI runs a named, un-skippable scoring-integrity corpus (`tests/integrity/`) plus `bash -n` on every check script.
- Layered ground truth (deterministic AST/manifest + LLM curator + solve-verification) and min(grep, judge) two-tier scoring — good eval hygiene.
- Rich validation gates: preflight, task-mix, CRNT, staleness — the authoring pipeline is well-guarded.
- 30K LOC of tests against 7K LOC of library code; docker/network tests marked and excluded in CI.

## Ranked findings (ROI = reach ÷ effort, discounted by risk)

### 1. Verification logic has escaped the library: 705 per-task bash checks, 29 of them hand-ports of eb_verify plugins

**Evidence**: `find benchmarks -path '*/checks/*.sh'` → 705 scripts; only 33 invoke `python3 -m eb_verify`; 29 self-describe as "bash+jq+grep port of / reimplementation of `python3 -m eb_verify.plugins.file_extraction`" (e.g. `benchmarks/customer_escalation/support-mapping-dual-vitest-vite-optimize-001/checks/check_error_source.sh:8`, `benchmarks/technical_debt/dead-code-dual-spring-hibernate-001/checks/check_removal_impact.sh:7`); ~338 score via grep keyword-matching (e.g. `benchmarks/dependency_management/api-contract-001/checks/check_classification.sh` — `grep -qE 'compile|compilation|...'` → FOUND/TOTAL). CLAUDE.md and `docs/ARCHITECTURE.md` principle #1 both state "no per-task verifier copies, ever."

**Why it matters**: This is the benchmark's product. Scoring semantics are forked across 705 files that cannot be mutation-tested, fixed, or audited centrally — the topo_order parser fix (commit 9d5e551) and pt0n rescore campaign show what one verifier bug costs (a locked-run rescore across arms). The bash ports exist because python3+eb_verify availability inside images was not guaranteed; the library is docker-cp'd at container setup (`run_task.py:627-635`) rather than baked into the image, and check scripts written before/around that mechanism defensively re-implemented the plugin. Every new task author now copies the dominant local pattern: hand-rolled grep scoring.

**Fix direction**: (a) make python3 + installed eb_verify a hard guarantee of every image via `dockerfile_generator.py` (pip install the wheel, not docker-cp); (b) make the declarative form the default — checkpoint = plugin name + params in `task.toml`, with `checks/*.sh` reduced to generated one-line shims; (c) migrate the 29 declared ports first, then the grep-keyword class per task type. Authoring guide + preflight gate reject new hand-rolled logic.

**Effort**: L overall, but S to stop the bleeding (image guarantee + preflight gate + migrate the 29 ports). **Risk**: Medium — rescoring semantics can shift per task; mitigate with the existing golden/mutation fixtures and score-diff runs on locked transcripts (the project already has this muscle).

### 2. Checkpoint aggregation is implemented twice: bash test_runner.sh vs eb_verify.scoring — and production uses the bash one

**Evidence**: `scripts/sandbox/test_runner.sh:143-180` parses verifier JSON with `grep -oP '"score"\s*:\s*\K[0-9.]+'` and computes the weighted score in awk, reading weights from a `.meta` side-channel that `run_task.py:529` (`_verifier_meta_by_name`) writes into the agent-writable `.verifiers/` dir (the script itself documents the injection hazard). Meanwhile `lib/eb_verify/scoring.py::compute_score` + `runner.py::CheckpointRunner` implement the same contract in Python, used only by `cli.py` and tests. `run_task.py:868-899` (`_run_scoring`) execs the bash path.

**Why it matters**: Two implementations of the scoring contract must agree forever; the one that runs in production is the one written in bash with regex JSON parsing, agent-writable weight files, and shell-quoting hazards (commit 8a8236f already patched escaping of agent-controlled values). Every scorer-trust incident in the log (beads s58f, hktt/pt0n, apfp) traversed this seam. Since eb_verify is already inside the container, `test.sh` can become `python3 -m eb_verify.cli run /workspace/.task/task.toml` — deleting the bash aggregator, the `.meta` side-channel, and the grep-JSON parsing in one move, and making the CLI/tests path the production path.

**Effort**: M. **Risk**: Low-medium — the Python path is already tested; validate with score-identical replay on a locked run set before switchover. Depends on finding #1(a) (guaranteed python in image).

### 3. No offline rescore path: judge + scoring welded to docker inside run_task.py

**Evidence**: `scripts/analysis/rescore_baseline_aq8e.py:162` and `rescore_mcp_only_uu17.py:130` define `fake_docker_exec` shims to import and re-invoke `run_task._apply_llm_judge` against locked transcripts; `rescore_topo_order_pt0n.py` and `recompute_headline_*.py` are further one-offs, some with absolute paths into sibling worktrees (`/home/ds/projects/EnterpriseBench-9awn/...`). `_apply_llm_judge` (`run_task.py:929`) reads artifacts via `_docker_exec`/`_docker_cp` rather than from the saved run directory.

**Why it matters**: This project rescopes and rescores constantly (4 of the last 15 commits are rescore/headline-regeneration work). Each campaign currently requires a bespoke script that monkeypatches the orchestrator's docker layer — high-friction, error-prone, and the reason `scripts/analysis/` is accumulating unreusable one-offs. The structural fix is small: `run_task` already saves the agent trace and artifacts; give eb_verify an artifact-source abstraction (container | run_dir) and an `eb-verify rescore <run_dir>` entry point. Every future verifier fix then gets a one-command, auditable rescore.

**Effort**: M (extract artifact acquisition behind an interface; move `_apply_llm_judge` into `eb_verify.judge.engine`). **Risk**: Low — additive; existing path unchanged until parity is shown.

### 4. run_task.py is a 2,121-line god module and the repo's #1 churn hotspot

**Evidence**: `scripts/orchestration/run_task.py` — 2,121 lines, 17 changes in the last 90 days (next-highest file: 6). It contains five separable concerns: docker client primitives (`_docker_build/_exec/_cp/_stop_rm`, lines 229-346), MCP configuration (`_configure_mcp`, `_verify_mcp_endpoint`, ~250 lines), instruction assembly (`_build_instruction_text`), scoring+judge (`_run_scoring`, `_apply_llm_judge`), and results serialization (`_save_results`).

**Why it matters**: Every feature — new tool-access mode, new artifact type, scoring fix, telemetry field — lands in this one file, which is why it churns 3x faster than anything else. Findings #2 and #3 already carve out scoring/judge; extracting a `docker.py` and `mcp_config.py` module within `scripts/orchestration/` finishes the decomposition and lets chain/event-replay runners share the container plumbing instead of subprocessing around it. Do this opportunistically as #2/#3 land, not as a standalone rewrite.

**Effort**: M (mostly mechanical moves; tests exist for MCP config). **Risk**: Low — same-process refactor, high test coverage (`tests/test_mcp_config.py` 704 lines).

### 5. Task-definition parsing duplicated 12x, including a hand-rolled TOML parser

**Evidence**: `lib/eb_verify/task_parser.py:211` (`parse_task` → typed `TaskDefinition`) is the canonical parser, yet 11 other loaders exist: `scripts/detect_duplicates.py:35`, `scripts/analyze_scores.py:118`, `scripts/audit_difficulty.py:112`, `scripts/audit_instructions.py:32`, `scripts/solve_verify.py:117`, `scripts/sandbox/sandbox_builder.py:109`, `scripts/orchestration/event_replay.py:49`, `scripts/orchestration/run_task.py:188`, etc. `sandbox_builder.py:36` (`parse_toml_minimal`) is a regex-based TOML parser fallback.

**Why it matters**: Schema evolution (new task.toml fields — session types, tool_access, ground-truth confidence) requires N synchronized edits, and the ad-hoc loaders silently ignore fields the canonical parser validates. The typed dataclasses in task_parser already exist; consumers just don't use them. Delete `parse_toml_minimal` outright (tomllib is stdlib on the pinned Python ≥3.10 with tomli fallback already declared in pyproject).

**Effort**: S. **Risk**: Low — behavior-preserving consolidation, preflight + `tests/test_all_tasks_valid.py` catch regressions.

### 6. results.json contract has no owning module — 22 independent parsers

**Evidence**: 22 files under `scripts/` and `lib/eb_metrics/` read `results.json` directly (grep count); `scripts/lib/shared.py` provides `discover_results_dirs`/`strip_mode_suffix`/`load_task_index` but has exactly 2 importers (`cost_tracker.py`, `reproducibility_check.py`). Layout knowledge (mode suffixes, `mcp_batch*`/`smoke_*` dirs, rep1-3 multi-rep max-score convention documented only in `rescore_baseline_aq8e.py`'s docstring) is re-encoded per script. `schemas/verifier_output.schema.json` exists but there is no results.json schema.

**Why it matters**: The results contract is the boundary between the run harness and the entire analysis/paper pipeline (`make analyze/charts/report/paper-figures`). Any field or layout change fans out to 22 call sites; the multi-rep scoring convention living in a one-off script's comment is exactly how headline numbers get miscomputed. One `eb_metrics.results` reader (discovery + record model + rep-selection rule) plus a results schema turns 22 parsers into 22 imports.

**Effort**: S-M. **Risk**: Low — incremental adoption; add the schema first, migrate readers as touched.

### 7. scripts/ is not a package: 30+ sys.path.insert hacks encode the import graph by hand

**Evidence**: 30+ `sys.path.insert` occurrences across `scripts/` (e.g. `run_sweep.py:38`, `chain_runner.py:22`, `mining/kg_task_miner.py:29-31`, `infra/generate_mcp_instructions.py:22-26`, `triage/triage_run.py:35-36`); `scripts/lib/` duplicates the role of `lib/` for "shared but unpackaged" code.

**Why it matters**: This is the mechanism that makes findings 5 and 6 the path of least resistance — importing shared code is harder than copy-pasting a loader, so people copy-paste. Making `scripts/` importable (a top-level package or moving genuinely shared code into the installed `lib/` distribution, which CI already `pip install -e`s) removes the friction that generates the duplication. Cheap enabler for everything above.

**Effort**: S. **Risk**: Low — mechanical; CI import errors surface immediately.

### 8. 372 committed generated Dockerfiles with no freshness gate

**Evidence**: `benchmarks/*/environment/Dockerfile{,.hybrid,.sg_only}` — 372 files generated by `scripts/sandbox/dockerfile_generator.py`; `run_task.py:212` regenerates the standard variant at run time, while preflight (`validate_tasks_preflight.py:305-310`) checks only that the files *exist*, not that they match what the generator would produce today.

**Why it matters**: Two sources of truth for the sandbox environment: the generator (used for `standard` at run time) and the committed files (hybrid/sg_only, and whatever external consumers read). Generator changes silently strand 372 files; a task can pass preflight with an environment that no longer matches the harness. Either stop committing them (generate all variants at run time) or add a regenerate-and-diff gate to `make verify-tasks`.

**Effort**: S. **Risk**: Low — the gate is read-only; choosing "stop committing" needs a check that nothing external consumes the committed files.

### 9. Housekeeping: stale library copies and unreusable one-offs accumulating inside the source tree

**Evidence**: `lib/build/lib/eb_verify/` — untracked but on-disk stale setuptools copy of the whole library (shadows greps, importable by accident); `lib/eb_verify/_vendor/benchmark_qa_core/` contains only `__pycache__` (ghost vendor dir, nothing tracked); `scripts/analysis/rescore_*_{aq8e,uu17,pt0n}.py` hardcode absolute paths to sibling worktrees (`/home/ds/projects/EnterpriseBench-9awn/...`).

**Why it matters**: Low individually, but these are the residue of findings #3 (no rescore API → one-off scripts) and packaging friction. Gitignore/clean `lib/build`, delete the ghost vendor dir, and adopt a convention: campaign scripts move to `docs/audits/` or `results/<campaign>/` once their numbers are locked, with #3's `rescore` command replacing the pattern.

**Effort**: S. **Risk**: None.

## Drift ledger (intended vs actual)

| Intent (docs/ARCHITECTURE.md, CLAUDE.md) | Actual |
| --- | --- |
| "One verifier library... no per-task verifier copies, ever" | 705 per-task bash checks; 29 explicit plugin ports; 33/705 call the library |
| "eb_verify is installed in every sandbox container" | docker-cp'd into `/workspace/.eb_verify` at setup, not installed in the image; some checks assume it may be absent |
| Verification flow runs through eb_verify runner | Production aggregation is bash `test_runner.sh`; `CheckpointRunner` used only by CLI/tests |
| Task mix 15/25/30/20/10 (ARCHITECTURE.md) | 12.5/25.9/25.0/14.3/11.6/10.7 (CLAUDE.md, validator-enforced) — ARCHITECTURE.md stale, validator is authoritative |
| ARCHITECTURE.md says 100 tasks | 112 active |

## Leave unchanged

- **Subprocess layering between run_benchmark/run_sweep and run_task** — process isolation per task is the right call for parallel multi-account runs and fault containment.
- **Plugin registry design** (`plugins/__init__.py`) — Protocol + explicit register, byte-capped reads; this is the consolidation target, not a problem.
- **scorer_guard trust boundary and the CI integrity corpus** — recently hardened, well-tested; build on it.
- **Benchmark suite organization** (suite/task/checks/environment layout) and the layered ground-truth model — coherent and validator-backed.
- **judge/ backends abstraction and eb_metrics → codeprobe adapter** — clean, single-purpose modules.
- **Root worktree clutter** (100+ `enterprisebench-*` dirs) — operational Gas City residue, untracked, not an architecture concern.
