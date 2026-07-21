# codeprobe: mine_cmd.py decomposition + run_cmd/api dual-path unification

_Execution plan, 2026-07-11. Target repo: `/home/ds/projects/codeprobe` (read-only during planning). Source review: `/home/ds/gas-city/docs/arch-reviews/2026-07-10/codeprobe.md`, findings 7 (primary), 3 (secondary). All line numbers verified against the working tree on 2026-07-11._

---

## 0. Preconditions and interaction with already-beaded fixes

Two smaller findings are beaded separately and **must land before any PR in this plan**:

- **P0a — error taxonomy relocation (review finding 1).** `cli/errors.py` → `codeprobe/errors.py`, with `cli/errors.py` kept as a re-export shim. Every module this plan moves out of `cli/` currently imports `DiagnosticError`/`PrescriptiveError` from `codeprobe.cli.errors` (`mine_cmd.py:22`, `run_cmd.py` header). **Hard rule for every step below: moved code imports `from codeprobe.errors import DiagnosticError, PrescriptiveError`. A module under `mining/` or `core/` importing `codeprobe.cli.errors` recreates the inversion finding 1 removes.** If a step is executed before P0a merges, the step is blocked — do not work around it with a lazy import.
- **P0b — reward-classification policy relocation (review finding 2).** `PASS_THRESHOLD` + `is_quota_casualty`/`is_scorable_run`/`partition_reward_population` move from `analysis/stats.py` down to a core-tier module (review names `core/scoring/result.py` or a new leaf `reward_policy.py`; the bead decides the exact home, with `analysis.stats` re-exporting). This plan touches only *callers* of that policy (`run_cmd.py:18` imports `partition_reward_population`; `api.py:31` imports `task_passed`). **Rule: any file this plan creates or rewrites imports these symbols from their post-P0b canonical home, not the `analysis.stats` re-export.** Nothing in this plan moves those symbols itself, so there is no merge conflict either way — only an import-path choice.

Neither track below modifies `core/executor.py` (review finding 8, planned separately). The R-track deliberately builds the seam *around* `execute_config` so the later executor decomposition swaps internals without touching this plan's modules.

---

## 1. Current state (verified)

### 1.1 `cli/mine_cmd.py` — 3,499 lines, one Click-era module, six pipelines

Top-level symbol map (all verified):

| Lines | Symbols | Concern |
|---|---|---|
| 32–189 | `_GIT_URL_PATTERN`, `_ACCEPTED_GIT_URL_SCHEMES`, `_is_git_url`, `_normalize_url`, `_validate_git_url_shape`, `_validate_clone_url`, `_clone_repo` | URL validation + shallow clone |
| 206–261 | `_EVAL_GOALS`, `_NUMERIC_GOAL_KEYS`, `_COUNT_PRESETS`, `_SOURCE_OPTIONS` | goal/preset policy tables |
| 264–357, 560–631, 797–827 | `_is_interactive`, `_ask_eval_goal`, `_ask_task_count`, `_ask_source`, `_show_preflight`, `_discover_and_select`, `_interactive_config` | interactive wizard (TTY prompts) |
| 359–553 | `_quality_review`, `_show_results_table`, `_REJECTION_HINTS`, `_show_shortfall_notice`, `_show_next_steps` | quality policy + terminal rendering |
| 639–794 | `_CURRENT_TASKS_DIR`, `_clear_tasks_dir`, `_record_task_ids_in_experiment`, `_suggest_path`, `_validate_git_repo`, `_looks_like_url`, `_resolve_repo_path` | tasks-dir lifecycle, repo resolution |
| 830–867 | `_was_llm_used`, `_enrich_sdlc_tasks` | LLM enrichment gating |
| 870–958 | `_log`, `_MINE_START_TIME`, `_COMPREHENSION_CONSENSUS` (module state), `_format_elapsed`, `_enrichment_status`, `_print_summary_block` | summary rendering + module-global state |
| 960–1132 | `_cold_start_check`, `_comprehension_generator_available`, `_suitability_warnings`, `_run_suitability_check`, `_resolve_task_type` | suitability / task-type fallback policy |
| 1135–1386 | `_CLI_DEFAULTS`, `_PRESET_ALIASES`, `_PROFILE_KEYS`, `_user_profiles_path`, `_project_profiles_path`, `_load_profiles_from`, `load_all_profiles`, `load_profile`, `save_profile`, `list_profiles`, `resolve_effective_config` | config precedence engine + profile store |
| 1394–1616 | `_dispatch_by_task_type`, `_dispatch_cross_repo` | routing + cross-repo flow |
| 1618–1884 | `_apply_dual_verification`, `_mine_tasks_with_progress`, `_resolve_narrative_source`, `_resolve_sdlc_sg_repo`, `_stamp_sg_repo` | task post-processing policy |
| 1886–2354 | `_dispatch_sdlc`, `_dispatch_probes`, `_dispatch_comprehension`, `_consensus_backend_names`, `_quarantine_comprehension_tasks`, `_echo_consensus_split`, `_dispatch_mixed` | the four single-repo pipelines |
| 2356–2399 | `_finish_mine_output` | shared completion rendering |
| 2402–2829 | `run_mine` | top-level orchestrator: tenant lock, v0.7 defaults, mutex validation, goal resolution, interactive branch, Ctrl-C cleanup, envelope emission |
| 2837–3314 | `_run_org_scale_mine`, `_build_curation_backends`, `_run_curation`, `_interactive_family_selection`, `_run_validation`, `_show_org_scale_results` | org-scale pipeline + curation |
| 3321–3498 | `_resolve_refresh_commit`, `_run_refresh` | refresh flow |

External consumers of `mine_cmd` symbols (must keep working, verified by grep):

- `cli/__init__.py:590` imports `list_profiles`, `load_profile`, `run_mine`, `save_profile`; calls `run_mine` at `:798`.
- Tests importing/patching `codeprobe.cli.mine_cmd.*`: `test_mine_goals.py` (imports + ~20 patch targets), `test_mine_profiles.py` (`_load_profiles_from`, `list_profiles`, `load_all_profiles`, `load_profile`, `save_profile`), `test_mine_presets.py` (`_PRESET_ALIASES`), `test_experiment_core.py:352,371` (`_record_task_ids_in_experiment`), `test_cli.py:300-302` (`_resolve_repo_path`, `_run_org_scale_mine`, `_resolve_task_type`), `test_lint_zfc.py:273` (asserts the file path is in a ZFC allowlist — that entry must be updated when the file shrinks).

Module state to eliminate: `_CURRENT_TASKS_DIR` (Ctrl-C cleanup), `_MINE_START_TIME`, `_COMPREHENSION_CONSENSUS` (envelope payload). All three become fields on the pipeline outcome/context object (§3.2).

### 1.2 The dual execution path (run_cmd vs api), divergence inventory

`run_cmd.run_eval` (`run_cmd.py:417-1083`, with nested `_run_config` at `:726-1010`) vs `api.run_experiment` (`api.py:90-230`). Shared leaf: `core.executor.execute_config`. Verified divergence table — this is the exact contract the shared extraction must encode:

| # | Concern | `run_cmd` (`_run_config`) | `api.run_experiment` | Unified decision (no executor judgment) |
|---|---|---|---|---|
| D1 | Model-token validation | `validate_model` up front for every config (`run_cmd.py:589-596`) | none — invalid model flows to the agent and scores 0.0 | Validation moves into shared `resolve_run_config`; **api gains fail-fast** (bug-fix class; the silent-0.0 path is the review's stated correctness liability). New api test pins the raise. |
| D2 | `permission_mode == "default"` | upgraded to `dangerously_skip` + `CODEPROBE_SANDBOX` refcount (`:737-740`, helpers `:270-284`) | passed through unchanged | `resolve_run_config(..., upgrade_default_permission: bool)`. run_cmd passes `True`, api passes `False`. Both behaviors preserved byte-for-byte; alignment is a separate follow-up bead, not this plan. |
| D3 | Invalid permission error type | `PrescriptiveError("INVALID_PERMISSION_MODE")` (`:743-756`) | `ValueError` (`api.py:145-149`) | Core raises `PrescriptiveError` (post-P0a `codeprobe.errors`). `api.run_experiment` wraps the resolve call in one `try/except PrescriptiveError` and re-raises `ValueError(str(exc))` to preserve the documented library contract (`api.py:119-120` docstring). |
| D4 | Adapter resolution | `resolve(exp_config.agent or agent)` (`:758`) | `resolve(exp_config.agent)` with `"claude"` defaulted in `_build_experiment_config` (`api.py:53,151`) | Shared: `resolve(exp_config.agent or fallback_agent)`; run_cmd passes its `--agent` flag, api passes `"claude"`. Identical observable behavior on both sides. |
| D5 | timeout | CLI `--timeout` > `extra["timeout_seconds"]` > 3600 (`:762`) | `extra` > 3600 (`api.py:153`) | Shared with `timeout_override: int \| None`; api passes `None`. |
| D6 | max_turns + `config_max_turns_source` | CLI > field > extra; source `"cli"`/`"experiment"`/`""` (`:766-783`) | field > extra; source `"experiment"`/`""` (`api.py:156-164`) | **The named divergence dies here.** One implementation with `max_turns_override: int \| None`; api passes `None` and mechanically gets the `"experiment"|""` subset. |
| D7 | `AgentConfig.cwd` | `str(repo_root)` (git toplevel, `:598-610,808`) | `str(experiment_dir.resolve())` (`api.py:175`) | Explicit `cwd: Path` parameter; each caller keeps its current value. Reconciling is a semantic change to library runs — separate bead. |
| D8 | `check_parallel_auth` | always called, passes `parallel` (`:819-823`) | never called | Shared path always calls it (when the adapter has it) with the caller's `parallel`; api passes `parallel=1`. Any resulting message surfaces through the warnings channel (log-only on api). |
| D9 | `checkpoint_store.close()` | never closed | closed in `finally` (`api.py:207-208`) | Shared `run_one_config` closes in `finally`. CLI gains the close (resource-hygiene fix; the store is per-config so this is safe after results return). |
| D10 | `instruction.resolved.md` persistence | written per task before execution (`:900-925`) | absent | Shared `run_one_config` always writes it. api gains the artifact (additive on-disk output; pinned by a new test, called out in the PR body). |
| D11 | Task discovery | `_find_tasks` (`run_cmd.py:184-198`): returns `[]` on missing dir (caller probes two roots `:615-619`) | `_discover_task_dirs` (`api.py:67-87`): raises `FileNotFoundError` | Shared `discover_task_dirs(d, task_ids=..., missing_ok=...)`; run_cmd calls with `missing_ok=True`, api with `missing_ok=False`. The two current bodies are otherwise identical (sorted, `instruction.md` sentinel, task_ids filter). |
| D12 | Post-run summary stats | `partition_reward_population` excludes quota casualties/errors (`:979-1008`) | `task_passed` over **all** results including errored (`api.py:212-215`, log-only) | Presentation, stays per-caller unchanged in this plan. Filed as a follow-up bead (api's logged pass-rate counts quota casualties as failures; the returned `Report` is generated from raw results either way). |

Stays CLI-only (not extracted): tenant lock, offline preflight, v0.7 default resolution, `.evalrc.yaml` deprecation warning, experiment auto-discovery/auto-config-persist (`run_cmd.py:509-680`), suite filtering (`_filter_tasks_by_suite`), dry-run, Rich/NDJSON/envelope listener wiring, `TraceRecorder` construction, config-parallel thread pool, `KeyboardInterrupt`→`DiagnosticError` translation, budget-error surfacing. Stays api-only: `_build_experiment_config` dict validation, `generate_report`.

---

## 2. Target module layout

### 2.1 Mining pipeline (finding 7)

New package `src/codeprobe/mining/pipeline/` — the orchestration layer the review says is locked in the CLI. Sits **inside** `codeprobe.mining` (it sequences mining primitives; dependency direction stays mining→core/models, CLI→mining). Mirrors the `snapshot_cmd.py` → `codeprobe.snapshot` exemplar (`snapshot_cmd.py` is 655 lines of pure argument-binding).

```
src/codeprobe/mining/pipeline/
    __init__.py            public surface: MineRequest, MineOutcome, Reporter,
                           run_mine_pipeline, resolve_effective_config,
                           load_profile/save_profile/list_profiles/load_all_profiles
    outcome.py             MineRequest, MineOutcome, Reporter, PipelineContext        (~150 loc)
    config.py              goal/preset tables, _CLI_DEFAULTS, profile store,
                           resolve_effective_config                                   (~340 loc)
    repo.py                URL validation, clone, repo-path resolution                (~290 loc)
    prepare.py             suitability, task-type fallback, narrative-source,
                           sg_repo stamping, enrichment, dual verification            (~430 loc)
    tasks_output.py        tasks-dir lifecycle, experiment task_ids recording,
                           quality_review                                             (~180 loc)
    single_repo.py         task-type routing + sdlc/probes/mixed flows                (~420 loc)
    comprehension_flow.py  comprehension flow + quarantine                            (~210 loc)
    cross_repo_flow.py     cross-repo flow + resolver selection                       (~170 loc)
    org_scale_flow.py      org-scale mine + curation + validation                     (~340 loc)
    refresh_flow.py        single-task refresh                                        (~190 loc)

src/codeprobe/cli/
    mine_cmd.py            Click glue only: tenant lock, envelope, v0.7 defaults,
                           interactive branch, Ctrl-C translation                     (~350 loc)
    mine_wizard.py         TTY prompts (phases 0-2, subsystem/family selection)       (~280 loc)
    mine_display.py        tables, shortfall notice, summary block, next steps,
                           consensus split, org-scale results                         (~330 loc)
```

Every file lands under the 800-line house cap; most sit in the 200–400 band.

**Core datatypes (`outcome.py`):**

```python
@dataclass(frozen=True)
class MineRequest:            # everything run_mine() currently takes, minus CLI-only knobs
    repo_path: Path           # already resolved (repo.resolve_repo_path runs first)
    task_type: str
    goal: str | None
    goal_name: str
    bias: str
    count: int; source: str; min_files: int; min_quality: float
    subsystems: tuple[str, ...]
    no_llm: bool; enrich: bool; dual_verify: bool
    narrative_source: tuple[str, ...]; sg_repo: str
    cross_repo: tuple[str, ...]; backend: str
    org_scale: bool; families: tuple[str, ...]; repos: tuple[str, ...]
    scan_timeout: int; validate: bool; curate: bool
    curation_backends: tuple[str, ...]; verify_curation: bool
    mcp_families: bool; sg_discovery: bool
    consensus_backends: str; consensus_threshold: float
    consensus_mode: str; no_consensus: bool
    selected_families: tuple[TaskFamily, ...] | None = None   # wizard pre-selection

@dataclass
class MineOutcome:
    tasks: tuple[Task, ...]
    tasks_dir: Path | None                  # replaces _CURRENT_TASKS_DIR
    task_ids: tuple[str, ...]
    suite_path: Path | None
    task_types: tuple[str, ...]
    llm_enriched: bool
    shortfall: tuple[int, int, RejectionBreakdown | None] | None   # requested, mined, rejections
    quality_warnings: tuple[str, ...]
    consensus: dict | None                  # replaces _COMPREHENSION_CONSENSUS
    quarantined_count: int
    quarantine_dir: Path | None
    curation_backends_used: tuple[str, ...]
    started_at: float                       # replaces _MINE_START_TIME
    relaxed_min_files: int | None
    scan_results_summary: tuple[tuple[str, int], ...]   # org-scale echo data

@dataclass(frozen=True)
class Reporter:               # IO seam; no click import below cli/
    info: Callable[[str], None]   # CLI: click.echo;      library default: logger.info
    warn: Callable[[str], None]   # CLI: click.echo(err=True); default: logger.warning
    progress: Callable[[int], None] | None = None   # CLI: click.progressbar.update
```

Pipeline functions never call `click.*`; interactive decisions (confirm/prompt) never enter the pipeline — the wizard resolves them **before** building `MineRequest`. This is the ZFC-compatible mechanical split: pipeline = IO + validation + state, wizard/display = terminal, no reasoning added anywhere.

### 2.2 Shared run-config core (finding 3)

```
src/codeprobe/core/run_config.py                                             (~380 loc)
    @dataclass(frozen=True) ResolvedRunConfig:
        adapter: AgentAdapter
        agent_config: AgentConfig
        permission_mode: str
        owns_sandbox: bool
        config_max_turns_source: str        # "cli" | "experiment" | ""
        warnings: tuple[str, ...]           # tool-policy + preflight + parallel-auth msgs

    def resolve_run_config(
        exp_config: ExperimentConfig, *,
        fallback_agent: str = "claude",
        model_override: str | None = None,
        timeout_override: int | None = None,
        max_turns_override: int | None = None,
        cwd: Path,
        parallel: int = 1,
        upgrade_default_permission: bool,
    ) -> ResolvedRunConfig
        # order: permission (D2/D3) → resolve adapter (D4) → validate_model (D1)
        # → model/timeout/max_turns layering (D5/D6) → resolve_tool_policy
        # → AgentConfig build (D7 via cwd) → adapter.preflight → check_parallel_auth (D8)

    def discover_task_dirs(
        d: Path, *, task_ids: tuple[str, ...] = (), missing_ok: bool = False,
    ) -> list[Path]                          # D11

    def run_one_config(
        resolved: ResolvedRunConfig, *,
        exp_config: ExperimentConfig,
        task_dirs: list[Path], repo_path: Path, exp_dir: Path,
        max_cost_usd: float | None,
        parallel: int = 1, repeats: int = 1,
        clean_excludes: tuple[str, ...] = (),
        event_dispatcher: EventDispatcher | None = None,
        preamble_resolver: PreambleResolver | None = None,
        trace_recorder: TraceRecorder | None = None,
    ) -> list[CompletedTask]
        # checkpoint store (from_legacy_path, close in finally — D9)
        # → instruction.resolved.md persistence (D10) → execute_config
        # → save_config_results

    def acquire_sandbox() -> None            # moved from run_cmd.py:270-284
    def release_sandbox() -> None
```

`resolve_run_config` raises only `codeprobe.errors.PrescriptiveError` (post-P0a) and returns warnings as data — callers decide echo vs log. `run_one_config` passes every kwarg through to `execute_config` unchanged; when the finding-8 executor decomposition later restructures `execute_config`, this file is the single call site to update outside tests.

---

## 3. Characterization-test strategy (global)

This repo scores experiments; a silent behavior change corrupts comparison data. Three characterization layers, built **before** the moves they protect:

1. **Mined-artifact golden (M0).** `tests/characterization/test_mine_golden.py`: build a deterministic fixture git repo in tmp (scripted commits with merge messages — reuse the repo-builder helpers already in `test_pipeline_integration.py`), run `codeprobe mine <repo> --no-llm --source local ...` through `CliRunner` for each dispatch path (sdlc, probes, comprehension, mixed), then snapshot-assert: (a) the sorted file listing under `.codeprobe/tasks/`, (b) parsed `metadata.json` per task (full dict equality), (c) parsed `suite.toml`, (d) `experiment.json.task_ids`, (e) full stdout with elapsed-time line masked by regex. Golden files live in `tests/characterization/golden/`. This suite must pass **unchanged** through M1–M8; any diff is a behavior change, not a refactor.
2. **Resolution-matrix pins (R0).** Extend `tests/test_run_config_resolution.py` into an explicit matrix: for each of {model, timeout, max_turns} × {CLI flag, experiment field, extra-dict, default} assert the exact value **and** `config_max_turns_source` reaching `execute_config` (capture kwargs via `FakeAdapter` + `patch("...executor.execute_config")`). Add the same matrix for `api.run_experiment`. Pin the divergences as named tests: `test_api_current_no_model_validation` (D1, marked `# flips in R2`), `test_api_permission_default_not_upgraded` (D2), `test_api_invalid_permission_raises_valueerror` (D3), `test_api_no_resolved_instruction_artifact` (D10, flips in R3), `test_run_cmd_missing_tasks_dir_probes_repo_root` vs `test_api_missing_tasks_dir_raises` (D11).
3. **Error-envelope pins.** For the mine track, every `PrescriptiveError`/`DiagnosticError` raise site being moved gets a test asserting `code`, `next_try_flag`, and `exit` behavior through `CliRunner` (`INVALID_GIT_URL` ×6 sites, `CLONE_FAILED` ×2, `MUTEX_FLAGS` ×3, `UNKNOWN_BACKEND` ×3, `NARRATIVE_SOURCE_UNDETECTABLE` ×2, `METADATA_MISSING` ×2, `METADATA_INVALID`, `INTERRUPTED`). Many exist already in `test_mine_cli.py`/`test_mine_goals.py`; M0 audits coverage and fills gaps.

Per-step rule: the step's PR may **add** tests and **update import paths / patch targets** in tests, but may not change any golden file or any pinned assertion value. A step that needs a golden change is mis-scoped — stop and re-plan.

---

## 4. PR sequence — mine_cmd decomposition (primary track)

Each step is one PR, lands green on `pytest tests/`, and leaves `mine_cmd.py` importing/re-exporting whatever later steps still need. Symbol lists are exhaustive; "move" means cut from `mine_cmd.py`, paste to target, fix imports (P0a paths), update the named test patch-targets in the same commit.

### M0 — characterization baseline (tests only, no `src/` change)

- Add `tests/characterization/test_mine_golden.py` + `golden/` per §3.1.
- Audit and fill error-envelope pins per §3.3 (extend `tests/test_mine_cli.py`).
- Add one api-parity placeholder: mine via CLI on the fixture repo, assert `_record_task_ids_in_experiment` effect on `experiment.json` (locks the mine→run contract the R-track relies on).

### M1 — config precedence + profiles → `mining/pipeline/config.py`

- Create `src/codeprobe/mining/pipeline/{__init__.py,config.py}`.
- Move from `mine_cmd.py`: `_EVAL_GOALS` (206), `_NUMERIC_GOAL_KEYS` (240), `_COUNT_PRESETS` (247), `_SOURCE_OPTIONS` (253), `_CLI_DEFAULTS` (1135), `_PRESET_ALIASES` (1150), `_PROFILE_KEYS` (1160), `_user_profiles_path` (1178), `_project_profiles_path` (1183), `_load_profiles_from` (1189), `load_all_profiles` (1202), `load_profile` (1217), `save_profile` (1245), `list_profiles` (1258), `resolve_effective_config` (1264). Rename underscore-private table names to public in the new module (`EVAL_GOALS`, `CLI_DEFAULTS`, `PRESET_ALIASES`, `PROFILE_KEYS`, `COUNT_PRESETS`, `SOURCE_OPTIONS`); keep function names as-is.
- `mine_cmd.py` keeps aliases (`_EVAL_GOALS = EVAL_GOALS`, etc.) so `run_mine` internals and not-yet-moved wizard code compile without edits.
- Rewire `cli/__init__.py:590-595` to import `list_profiles/load_profile/save_profile` from `codeprobe.mining.pipeline.config` (keep `run_mine` from `mine_cmd`).
- Test updates (same PR): `test_mine_profiles.py` imports → `codeprobe.mining.pipeline.config`; `test_mine_presets.py` `_PRESET_ALIASES` → `PRESET_ALIASES`; `test_mine_goals.py` imports of `resolve_effective_config`/`_EVAL_GOALS` → new module (patch targets that patch `mine_cmd._cold_start_check` etc. are untouched — those symbols haven't moved yet).
- Gate: M0 golden unchanged; `test_mine_goals` precedence matrix green.

### M2 — repo acquisition → `mining/pipeline/repo.py`

- Move: `_GIT_URL_PATTERN` (32), `_ACCEPTED_GIT_URL_SCHEMES` (39), `_is_git_url` (44), `_normalize_url` (49), `_validate_git_url_shape` (56), `_validate_clone_url` (114), `_clone_repo` (137), `_suggest_path` (695), `_validate_git_repo` (712), `_looks_like_url` (745), `_resolve_repo_path` (761). Public names: `is_git_url`, `clone_repo`, `resolve_repo_path`, `validate_git_repo`, `suggest_path`.
- `_clone_repo`'s two `click.echo(..., err=True)` calls (150, 188) become `reporter.warn(...)` via a `reporter: Reporter | None = None` kwarg (default: module logger). Add `outcome.py` now with only the `Reporter` dataclass (rest of the file arrives in M5).
- Errors import from `codeprobe.errors` (P0a). All six `INVALID_GIT_URL` raise sites and both `CLONE_FAILED` sites move verbatim.
- `mine_cmd.py` re-exports `_resolve_repo_path = resolve_repo_path` etc. (patch target `codeprobe.cli.mine_cmd._resolve_repo_path` in `test_cli.py:300` and `test_mine_goals.py` keeps working through the alias; update `test_cli.py` to patch the new path anyway — aliases are for `run_mine` internals, tests should point at the real home).
- Gate: error-envelope pins (§3.3, `INVALID_GIT_URL`/`CLONE_FAILED`) unchanged.

### M3 — preparation policy → `mining/pipeline/prepare.py` + `tasks_output.py`

- `prepare.py` gets: `_cold_start_check` (960), `_comprehension_generator_available` (975), `_suitability_warnings` (985), `_resolve_task_type` (1097), `_resolve_narrative_source` (1757), `_resolve_sdlc_sg_repo` (1846), `_stamp_sg_repo` (1865), `_was_llm_used` (830), `_enrich_sdlc_tasks` (839), `_apply_dual_verification` (1618). Public names drop the underscore. `_enrich_sdlc_tasks`' three `click.echo` sites become `reporter.info`; `_apply_dual_verification`'s two echoes likewise.
- `_apply_dual_verification` imports `extractor._build_oracle_ground_truth` / `_oracle_discrimination_passed` (1633-1635): promote both to public names in `mining/extractor.py` (`build_oracle_ground_truth`, `oracle_discrimination_passed`; keep `_`-aliases in extractor for any stragglers) — the cross-module private reach dies here.
- `tasks_output.py` gets: `_clear_tasks_dir` (642) → `clear_tasks_dir(repo_path) -> Path` **without** the `_CURRENT_TASKS_DIR` global (returns the path; context tracking arrives in M5), `_record_task_ids_in_experiment` (660) → `record_task_ids_in_experiment`, `_quality_review` (359) → `quality_review` (pure, returns warning strings).
- Stays in CLI: `_run_suitability_check` (1070) — it prompts; it now calls `prepare.suitability_warnings`.
- Test updates: `test_mine_goals.py` patch targets for `_cold_start_check`/`_comprehension_generator_available`/`_resolve_task_type` → `codeprobe.mining.pipeline.prepare.*` (and `mine_cmd` keeps thin aliases so `run_mine` body is untouched this PR); `test_experiment_core.py:352,371` import → `tasks_output`.
- Gate: M0 golden unchanged; `NARRATIVE_SOURCE_UNDETECTABLE` pins unchanged; new unit test pinning `apply_dual_verification` output on a fixture `MineResult` (oracle vs all-test-files vs no-ground-truth branches, all four early-continue paths at 1651/1656/1661/1670-1677).

### M4 — display split → `cli/mine_display.py` + `cli/mine_wizard.py` (CLI-internal, zero pipeline change)

- `mine_display.py`: `_show_results_table` (440), `_REJECTION_HINTS` + `_show_shortfall_notice` (464/476), `_show_next_steps` (508), `_format_elapsed` (887), `_enrichment_status` (895), `_print_summary_block` (925), `_echo_consensus_split` (2200), `_show_org_scale_results` (3212), `_print`-side of `_finish_mine_output` (2356). Public names, no behavior change; `_finish_mine_output` stays in `mine_cmd.py` for now but delegates rendering.
- `mine_wizard.py`: `_is_interactive` (264), `_ask_eval_goal` (269), `_ask_task_count` (300), `_ask_source` (314), `_show_preflight` (332), `_discover_and_select` (560), `_interactive_config` (797), `_interactive_family_selection` (3118), `_run_suitability_check` (1070). Wizard imports tables from `pipeline.config`.
- Gate: M0 golden stdout byte-identical (this PR is the highest-risk one for stdout drift — the golden is the whole gate).

### M5 — single-repo flows → `pipeline/single_repo.py` + `comprehension_flow.py`, introduce `MineOutcome`

- Complete `outcome.py` (`MineRequest`, `MineOutcome`, `PipelineContext` holding `tasks_dir`/`started_at`/`consensus` — kills the three module globals).
- `single_repo.py`: `_dispatch_by_task_type` (1394) → `dispatch_by_task_type(request, reporter) -> MineOutcome`; `_dispatch_sdlc` (1886) → `run_sdlc_flow`; `_dispatch_probes` (2000) → `run_probes_flow`; `_dispatch_mixed` (2220) → `run_mixed_flow`; `_mine_tasks_with_progress` (1706) → `mine_tasks_with_progress` (progress bar injected via `Reporter.progress`; the TTY check moves to the CLI caller).
- `comprehension_flow.py`: `_dispatch_comprehension` (2050) → `run_comprehension_flow`, `_consensus_backend_names` (2157), `_quarantine_comprehension_tasks` (2166).
- Mechanical transformation rule per flow function: every `click.echo` line either (a) moves data into the returned `MineOutcome` field named in §2.1 and is rendered afterward by `mine_cmd.py` via `mine_display` (`_show_results_table`, `_show_shortfall_notice`, `_echo_consensus_split`, `_finish_mine_output` calls at 1986-1997, 2144-2154, 2305-2353), or (b) is an in-progress notice ("Generating instructions via LLM...", relax-min-files warning at 1963) and becomes `reporter.info/warn`. No third option; the executor makes no wording or ordering choices — output order is pinned by the M0 golden.
- `run_mine` body edits: the `_dispatch_by_task_type(...)` call at 2764 becomes `outcome = dispatch_by_task_type(request, reporter)` followed by the display calls; Ctrl-C handler (2780-2799) reads `context.tasks_dir` instead of `_CURRENT_TASKS_DIR`; envelope block (2804-2827) reads `outcome.consensus`/`outcome.tasks_dir`.
- Test updates: `test_mine_goals.py` dispatch patch targets (`_dispatch_sdlc`/`_dispatch_probes`/`_dispatch_comprehension`/`_dispatch_mixed`) → `codeprobe.mining.pipeline.single_repo.*`.
- Gate: full M0 golden (all four dispatch paths), `INTERRUPTED` pin, consensus-split stdout pin.

### M6 — cross-repo + org-scale flows → `pipeline/cross_repo_flow.py` + `pipeline/org_scale_flow.py`

- `cross_repo_flow.py`: `_dispatch_cross_repo` (1482) → `run_cross_repo_flow(request, reporter) -> MineOutcome` (resolver-selection block 1530-1581 moves verbatim; echoes → `reporter.info`; `MISSING_SG_AUTH` raise moves verbatim).
- `org_scale_flow.py`: `_run_org_scale_mine` (2837) → `run_org_scale_flow`, `_build_curation_backends` (3012), `_run_curation` (3045), `_run_validation` (3174). The `--repos` path-resolution loop in `run_mine` (2679-2701) moves into this flow (it duplicates M2's `resolve_repo_path` error shape — reuse `repo.resolve_repo_path` per entry, preserving the `--repos path does not exist` message text). Family pre-selection stays a `MineRequest.selected_families` input filled by the CLI wizard (`_interactive_family_selection`), removing the TTY check at 2910 from the pipeline.
- `_show_org_scale_results` already lives in `mine_display` (M4); `run_org_scale_flow` returns the data it needs on `MineOutcome` (`scan_results_summary`, `quarantined_count`, `quarantine_dir`, `curation_backends_used`).
- Test updates: `test_cli.py:301` patch `_run_org_scale_mine` → `codeprobe.mining.pipeline.org_scale_flow.run_org_scale_flow`; `test_pipeline_integration.py` patch targets likewise.
- Gate: `test_org_scale.py`, `test_curator_*` suites, org-scale golden stdout.

### M7 — refresh flow → `pipeline/refresh_flow.py`

- Move: `_resolve_refresh_commit` (3321), `_run_refresh` (3342) → `resolve_refresh_commit`, `run_refresh_flow`. `METADATA_MISSING`/`METADATA_INVALID`/`INVALID_GIT_URL` raises move verbatim; final echoes (3488-3498) → returned `MineOutcome`-lite (`RefreshOutcome(task_id, renumbered, history)`) rendered by CLI.
- Gate: refresh error pins; `test_scaffold_upgrade.py`/refresh tests if present (audit in M0).

### M8 — thin `run_mine`, public pipeline entry, `api.mine()`

- Add `pipeline/__init__.py:run_mine_pipeline(request: MineRequest, reporter: Reporter | None = None) -> MineOutcome`: the non-CLI middle of today's `run_mine` — mutex validation (2551-2565), cross-repo goal default (2568-2570), `resolve_effective_config` application (2575-2596), goal→(name,bias,task_type) derivation (2601-2619, reading `config.EVAL_GOALS`), `no_llm`+agent-backend mutex (2622-2632), `resolve_task_type` fallback, subsystem normalization (2752), then route: refresh / cross-repo / org-scale / single-repo.
- `cli/mine_cmd.py` shrinks to: tenant lock (2462), output-mode + envelope (2465-2467, 2801-2827), v0.7 defaults block (2469-2537 — imports `config.defaults`, CLI-only), interactive prompts (2638-2647, 2723-2761 via `mine_wizard`), `KeyboardInterrupt` translation, display rendering of the returned `MineOutcome`. Target ≤350 lines; delete every alias that no longer has an internal consumer.
- Add `api.mine(repo_path: Path, **options) -> MineOutcome` in `src/codeprobe/api.py`: builds a `MineRequest` (non-interactive defaults, `Reporter` = logging), calls `run_mine_pipeline`. This closes the review's "the in-process `api.py` cannot mine" gap.
- Update `test_lint_zfc.py:273` allowlist entry for the shrunken file.
- Gate: full M0 golden; **new parity test**: `api.mine()` and `CliRunner` mine on the same fixture repo produce identical `tasks/` trees (file-list + metadata.json equality).

---

## 5. PR sequence — run_cmd/api unification (secondary track)

Independent files from the M-track (`core/run_config.py`, `run_cmd.py`, `api.py`); R0–R3 may run in parallel with M1–M8. Only ordering rule: R2 lands before M8 (so `api.py` grows `mine()` after its run path is already on the shared core, avoiding two concurrent rewrites of `api.py`).

### R0 — characterization matrix (tests only)

Per §3.2. Deliverables: the {model,timeout,max_turns}×{cli,experiment,extra,default} kwarg-capture matrix on **both** paths, `config_max_turns_source` asserted per cell, and the seven named divergence pins (D1–D3, D9–D11) with `# flips in R2/R3` markers.

### R1 — `core/run_config.py`: resolution + discovery; `run_cmd` adopts

- Create `core/run_config.py` with `ResolvedRunConfig`, `resolve_run_config`, `discover_task_dirs`, `acquire_sandbox`/`release_sandbox` (moved from `run_cmd.py:266-284`) per §2.2. Imports: `codeprobe.errors` (P0a), `adapters.models.validate_model`, `adapters.protocol.{ALLOWED_PERMISSION_MODES, AgentConfig}`, `core.registry.resolve`, `core.mcp_policy.resolve_tool_policy`. No click, no analysis imports.
- `run_cmd._run_config` (726-833) replaces its inline blocks: permission handling (737-756), adapter resolve (758), layered resolution (761-783), tool policy + AgentConfig (795-810), preflight (812-815), parallel-auth (819-823) with one `resolve_run_config(exp_config, fallback_agent=agent, model_override=model, timeout_override=timeout, max_turns_override=max_turns, cwd=repo_root, parallel=parallel, upgrade_default_permission=True)` call; echoes each `resolved.warnings` entry with the existing `f"  [{label}] Warning: ..."` format. The up-front all-configs `validate_model` loop (589-596) stays (it validates before any task discovery; `resolve_run_config` re-validates per config — idempotent).
- `_find_tasks` (184-198) body moves to `discover_task_dirs`; `run_cmd` keeps `_find_tasks = discover_task_dirs` alias for its two call sites (615, 617) and existing tests.
- Gate: R0 matrix green with zero assertion edits (behavior-preserving by construction); `test_run_config_resolution.py`, `test_executor_events.py`, `test_ctrlc_integration.py`.

### R2 — `api.run_experiment` adopts `resolve_run_config` (the fork dies)

- Delete from `api.py`: permission check (144-149), adapter resolve (151), timeout/max_turns/`config_max_turns_source` blocks (153-164), tool policy + AgentConfig (165-177), preflight loop (179-181), `_discover_task_dirs` (67-87).
- Replace with: `discover_task_dirs(tasks_dir, task_ids=experiment.task_ids, missing_ok=False)`; per config, `try: resolved = resolve_run_config(exp_config, fallback_agent="claude", cwd=<experiment_dir.resolve()>, parallel=1, upgrade_default_permission=False) except PrescriptiveError as exc: raise ValueError(str(exc)) from exc` (D3 contract); log each warning via `logger.warning("[%s] %s", label, w)`.
- Behavior changes shipped in this PR, each with its flipped pin from R0: api now raises on invalid model token (D1 — flip `test_api_current_no_model_validation` to assert the raise); api may log parallel-auth warnings (D8, log-only). D2 (`upgrade_default_permission=False`) and D7 (`cwd=experiment_dir`) explicitly preserved — assert both in the R0 matrix.
- File follow-up beads (created in the PR body, not implemented): align D2, D7, D12 across paths as deliberate semantic decisions.
- Gate: `test_api.py` green with only the D1 pin flipped; R0 matrix cells for api identical except D1.

### R3 — `run_one_config` extraction (checkpoint / instruction persistence / execute / save)

- Move into `core/run_config.run_one_config`: checkpoint-store setup (`run_cmd.py:825-833` ≡ `api.py:184-191`), instruction pre-resolution + `instruction.resolved.md` write (`run_cmd.py:878-925` — including the `resolve_instruction_variant`/`load_instruction`/`compose_instruction` wiring; `DefaultPreambleResolver` construction stays with callers since it takes CLI-visible dirs), the `execute_config` call (`run_cmd.py:927-943` ≡ `api.py:196-206`), `checkpoint_store.close()` in `finally` (D9), `save_config_results` (`run_cmd.py:972` ≡ `api.py:210`).
- `run_cmd._run_config` keeps: sandbox acquire/release ordering around the call, dispatcher lifecycle (`dispatcher.shutdown()` in its own `finally`), `KeyboardInterrupt` handling, the pretty-mode summary (partition/pass-rate block 979-1008), `_results_by_config` bookkeeping. `api.run_experiment` keeps: `task_passed` logging, `ConfigResults` accumulation, `generate_report`.
- Behavior changes, pinned: CLI now closes the checkpoint store per config (D9); api now writes `instruction.resolved.md` per task (D10 — flip its R0 pin, and note the new artifact in the PR body since downstream tooling may glob `runs/`).
- Gate: R0 matrix + flipped D9/D10 pins; `test_checkpoint.py`, `test_checkpoint_scoring.py`, `test_ctrlc_integration.py` (interrupt still resumes from checkpoint), `test_api.py` end-to-end with `FakeAdapter`.

After R3, every future change to "how a config becomes an `AgentConfig` and reaches the executor" has exactly one home, reachable identically from CLI, notebooks, and the future `api.mine()`→`api.run_experiment()` chain.

---

## 6. Risk register

| Risk | Where | Mitigation |
|---|---|---|
| stdout drift breaking agent drivers that parse mine output | M4, M5, M6 | M0 golden asserts full stdout (elapsed-time masked); envelope JSON asserted as parsed dict |
| test patch-target churn masking real regressions | M2, M3, M5, M6 | patch targets updated in the same commit as each move, never batched separately; aliases only for `run_mine`-internal callers, deleted in M8 |
| `Reporter` swallowing a message that used to hit stderr | M2–M6 | Reporter default sinks are module loggers; CLI wiring passes click-echo shims; golden covers the TTY=false path CI runs in |
| api behavior changes leaking silently to library users | R2, R3 | each change is a named, deliberately flipped pin (D1, D9, D10) called out in the PR body; D2/D7/D12 explicitly frozen with tests |
| conflict with executor decomposition (finding 8) | R3 | `run_one_config` passes `execute_config` kwargs through verbatim; no executor-internal knowledge encoded |
| conflict with P0a/P0b beads | all | this plan only consumes their post-move import paths; hard-gated in §0 |

---

## Executive summary

1. `mine_cmd.py` (3,499 lines) decomposes along six verified cohesion clusters into a new `codeprobe/mining/pipeline/` package (config, repo, prepare, four flow modules, outcome types) plus CLI-only `mine_wizard.py`/`mine_display.py`, leaving `mine_cmd.py` as ~350 lines of Click glue and giving `api.mine()` the mining engine the review said was unreachable.
2. The run_cmd/api dual path dies via one shared `core/run_config.py` — `resolve_run_config` + `discover_task_dirs` + `run_one_config` — with all 12 verified divergences (including `config_max_turns_source`) resolved by explicit parameters, no executor judgment.
3. Sequencing: M0–M8 (mine) and R0–R3 (run) are independent tracks; both hard-depend on the already-beaded error-taxonomy (P0a) and reward-policy (P0b) relocations landing first, and R2 lands before M8.
4. Safety: characterization-first throughout — mined-artifact goldens, a kwarg-capture resolution matrix on both execution paths, and per-error-code envelope pins; any golden diff means the step is mis-scoped, because behavior changes here are silent data corruption.
5. Deliberate, pinned behavior changes are limited to three bug-fix-class items (api gains model validation, CLI gains checkpoint close, api gains `instruction.resolved.md`); permission-upgrade, cwd, and stats divergences stay frozen behind follow-up beads.

**First PR to cut: M0 + R0 together (tests only, zero `src/` change)** — the mine golden harness and the run/api resolution matrix. Everything after it is a mechanical move guarded by tests that already exist.
