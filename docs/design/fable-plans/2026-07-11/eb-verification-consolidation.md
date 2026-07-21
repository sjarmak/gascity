# EnterpriseBench Verification Consolidation — Migration Design

**Date:** 2026-07-11 · **Author:** fable-5 architect session · **Tracker:** EnterpriseBench-glka
**Source review:** /home/ds/gas-city/docs/arch-reviews/2026-07-10/enterprisebench.md (findings #1, #2, #3)
**Repo:** /home/ds/projects/EnterpriseBench (read-only during design; all line refs at 2026-07-11 HEAD)
**Constraint:** the jn73 paper batch (epic EnterpriseBench-jn73) depends on scoring stability — every milestone below is tagged SAFE-DURING-BATCH or WAIT.

---

## 0. The production scoring path, as verified

Every claim in this design was read from source, not from the arch review alone.

```
task.toml (checkpoints: verifier, weight, timeout_seconds)
  │  run_task.py::_setup_container (L550-663)
  │    checks/*.sh        → docker cp → /workspace/.verifiers/<name>.sh
  │    weight/timeout     → written as /workspace/.verifiers/<name>.meta   ← side-channel
  │    test_runner.sh     → docker cp → /workspace/test.sh                 ← bash aggregator
  │    lib/eb_verify      → docker cp → /workspace/.eb_verify              ← library copy, not install
  │    _chown_to_agent(L655-662): .verifiers, .task, .eb_verify, test.sh → agent:agent  ← TAMPER + LEAK
  ▼
agent session (full write access to every file above, for the whole session)
  ▼
run_task.py::_run_scoring (L868-899)
  docker exec (container default user) → bash /workspace/test.sh
    test_runner.sh: for .verifiers/*.sh → run → parse JSON with grep -oP →
    weight from <name>.meta → awk weighted sum → RESULT_JSON
  ▼
scorer_guard.guard_verifier_output(stdout, rc)   ← trust boundary (host-side, keep)
  ▼
run_task.py::_apply_llm_judge (L929-1080)
  reads agent artifacts via _docker_exec(["cat", path])                    ← docker weld
  min(grep_score, judge_score) per checkpoint, recompute task_score
  ▼
_save_results (L1082): results.json, verifier/output.json, agent_trace.jsonl
  (agent artifacts NOT persisted — only readable back via the container)   ← rescore gap
```

Three load-bearing facts the arch review understated:

1. **The tamper surface is the whole Tier-1 stack, not just weights.** `_chown_to_agent`
   (run_task.py:434-455, call at :655-662) gives the agent ownership of the check
   *scripts*, the `.meta` weight/timeout files, the `test.sh` aggregator itself, and the
   `/workspace/.eb_verify` library copy — and `_run_scoring` then executes `test.sh` as
   the container default user with no integrity re-check. An agent can rewrite any
   verifier to `echo '{"score":1.0,"passed":true}'` and nothing detects it.
2. **It is also an answer-leak channel.** Check scripts (including grep keyword lists
   that ARE the answer key for the ~grep-scored checks) sit agent-readable in
   `/workspace/.verifiers` for the entire session.
3. **Judge rescore is already being done offline, badly.** `scripts/analysis/
   rescore_baseline_aq8e.py:162` monkeypatches `run_task._docker_exec` and reconstructs
   agent artifact writes from `agent_trace.jsonl` — proof the run dir *almost* contains
   enough to rescore, and that artifacts must be persisted at run time to close the gap.

Design order below follows the safety-rail-first instruction: the conformance harness
(§1) is designed before any migration that could shift verdicts (§2-§4).

---

## 1. Conformance harness — the safety rail (design first)

### 1.1 Purpose and invariant

For any task whose verification is migrated (bash check → plugin call, or bash
aggregator → `CheckpointRunner`), we must be able to demonstrate, per task and per
checkpoint: **old path and new path produce the same verdict on the same workspace
state**, across (a) golden solved states, (b) golden unsolved/pristine states, and
(c) real locked-run agent states. A migration lands only with a green conformance
record; a red diff is a finding to adjudicate (sometimes the OLD side is the bug —
record which side is deemed correct, never silently adopt either).

### 1.2 What "same verdict" means

The comparable unit is the per-checkpoint verifier output already standardized by
`schemas/verifier_output.schema.json` and enforced by `test_runner.sh`'s parsing:

- `score` — compare with tolerance 1e-9 (both sides are deterministic; any drift is real)
- `passed` — exact match
- aggregate `task_score` — recomputed by both aggregators, compare 1e-6 (awk prints %.4f,
  so the harness compares the Python aggregate against the awk output at 1e-4 and
  against its own re-aggregation at 1e-9; both must hold)
- `detail` is NOT compared (free text), but is captured on both sides for diagnosis.

Infra-error semantics are part of the contract: if one side yields
`verifier_infra_error` (via `scorer_guard.guard_verifier_output`) and the other yields
a score, that is a verdict diff.

### 1.3 Where verdicts come from: three state sources, increasing cost

| State source | What it exercises | Cost | When required |
| --- | --- | --- | --- |
| S1: golden fixtures (tests/integrity corpus pattern: pristine + solved workspace trees) | check logic on known-answer states | seconds/task, CI-able | every migrated checkpoint |
| S2: locked-run replay — rebuild the task container, apply the persisted workspace diff (§3.4) or replay agent file-writes reconstructed from `agent_trace.jsonl` (aq8e technique, `_reconstruct_writes`) | check logic on real, messy agent output | ~1-3 min/task, docker | every migrated task with ≥1 locked run |
| S3: dual-scoring shadow mode — during live runs, score with BOTH paths, record both, publish OLD | end-to-end incl. container env, ordering, timeouts | ~free (one extra exec per run) | during the switchover window (§5 M9) |

S1 catches logic divergence; S2 catches "real agent output is weirder than fixtures";
S3 catches environment divergence. All three write the same diff record.

### 1.4 Component design

New module: `lib/eb_verify/conformance.py` + CLI subcommand `eb-verify conform`.
(Lives in the library so it survives the migration it polices; no `scripts/` sys.path
hacks per arch-review finding #7.)

```
eb-verify conform --task benchmarks/<suite>/<task> \
                  --state {golden|run:<run_dir>|workspace:<path>} \
                  [--checkpoint <name>] [--record results/conformance/<campaign>.jsonl]
```

Execution per task:

1. **Materialize state** — golden: copy fixture tree; run: container + diff replay;
   workspace: use as-is (this is what S3 shadow mode passes).
2. **OLD side** — run the *verbatim production path*: copy `checks/*.sh` + generated
   `.meta` files + `test_runner.sh` into the state root, execute `bash test.sh`,
   capture RESULT_JSON. No reimplementation — the point is to run the real bash.
3. **NEW side** — `CheckpointRunner` over the same state root driven by `task.toml`
   (declarative checkpoints where migrated, script checkpoints where not — the runner
   must support mixed mode, see gap G-RUNNER in §1.6).
4. **Guard both** — pass both raw outputs through `guard_verifier_output`; infra-error
   classification is compared too.
5. **Diff + record** — one JSONL row per checkpoint:

```json
{"task_id": "...", "checkpoint": "...", "state_source": "run:runs/official_runs/...",
 "old": {"score": 0.6, "passed": false, "infra": null},
 "new": {"score": 0.6, "passed": false, "infra": null},
 "verdict_match": true, "score_delta": 0.0,
 "old_impl": "checks/check_x.sh@<git-sha>", "new_impl": "plugin:file_extraction@<lib-version>",
 "adjudication": null}
```

`adjudication` is filled by a human/bead when `verdict_match=false`:
`{"correct_side": "new", "reason": "...", "bead": "EnterpriseBench-...."}`. Diff rows
with null adjudication block the migration PR (CI gate).

6. **Campaign roll-up** — `eb-verify conform --report` summarizes a JSONL: tasks
   covered, checkpoints compared, match rate, open adjudications. This artifact is
   attached to every migration bead and is the paper-defensible provenance record for
   jn73 ("scores unchanged under verifier consolidation: N=…, matches=…").

### 1.5 Golden-state fixtures (S1) — reuse, don't reinvent

`tests/integrity/` already holds the scoring-integrity corpus pattern (golden fixtures
+ mutation checks, un-skippable in CI). The conformance harness extends it:

- per migrated task, a `tests/integrity/conformance/<task_id>/` fixture with
  `solved/` and `pristine/` state trees (or a generator script where the state is
  a git checkout + patch, to avoid committing repo snapshots);
- CI job `conformance-migrated` runs S1 for every task listed in
  `migration_state.toml` (§5) — permanent regression net, not just a one-shot
  migration check. The bash originals are kept in-tree (moved to
  `checks/_legacy/`) until the suite-level milestone completes, so CI can keep
  re-running old-vs-new on fixtures the whole time.

### 1.6 Library API gaps the harness itself needs (subset of §2 gap list)

- **G-RUNNER (mixed-mode runner):** `CheckpointRunner` already shells checkpoint
  scripts to bash with a JSON-or-exit-code fallback (runner.py:189-196); it must
  additionally dispatch declarative plugin checkpoints (G1) in the same task, and
  its script fallback must match `test_runner.sh:79-95` byte-for-byte (non-JSON
  stdout + exit 0 → 1.0; timeout → exit 124 → 0.0 with the same detail string).
  Without exact fallback parity the harness reports false diffs.
- **G-STATEROOT:** all plugins and the runner must take an explicit workspace root
  (no hardcoded `/workspace`), so the harness can run host-side on materialized
  states. Verified need: production sets `WORKSPACE=/workspace` env for test.sh.
- **G-DIFFCMP:** tolerance-aware verifier-output comparison lives in
  `conformance.py`, shared by S1/S2/S3 — never re-implemented per campaign script.

---

## 2. Converging 705 bash checks onto eb_verify — without a big bang

### 2.1 Equivalence-family taxonomy (grounded by a ~30-script deep sample + full mechanical pass)

**Corrected population numbers** (the arch review's raw counts included archived
tasks and comment-only matches):

- 705 `checks/*.sh` total, of which **107 are under `benchmarks/_archived/`** —
  migration population is **598 live checks across 7 suites, 180 task.toml files**
  (customer_escalation 168, dependency_management 149, incident_response 101,
  technical_debt 67, feature_delivery 64, platform_engineering 42,
  security_operations 7; `mined/` is empty).
- Of 33 scripts containing `python3 -m eb_verify`, **31 are comment-only
  provenance headers; only 13 checks actually execute the library at runtime**
  (2 × file_extraction exec, 11 × topological_order import). The real library
  adoption is 13/598, worse than the review stated.
- **60 self-declared hand-ports**, not 29: 31 name `file_extraction`
  ("bash+jq+grep reimplementation of python3 -m eb_verify.plugins.file_extraction")
  + 29 generic ("reimplemented in bash+jq+grep"). The review's "29" was the
  generic subset.
- **`checks/*.meta` files do not exist in the repo.** Weights/timeouts live only
  in `task.toml [[checkpoints]]` blocks (weight sums audited to 1.0); the `.meta`
  files are *generated at container setup* by run_task.py (§0, §3). The
  side-channel is purely a runtime artifact — killing it (§3) requires no task
  edits at all.

**Uniform output contract** (verified across the sample): one JSON line on stdout
`{"score": <0..1>, "passed": <bool>, "detail"|"reason": "<str>"}`, exit 0 even on
zero score; exit 1 reserved for infra failure (missing ground_truth.json).
`passed` is usually `score >= 0.5`. One inconsistency to normalize during
migration: **306 scripts emit `detail`, 283 emit `reason`** — `test_runner.sh`
only parses `score`/`passed`/`detail`, so the `reason` family's diagnostic text is
currently *dropped* from verifier output (harmless to scores, but the migration
should map both to `detail`). Script sizes: median 34 lines, library-exec ~11,
hand-ports 40-65.

**Hand-port rationale is uniform and single-cause** — container image lacks
python3. Verbatim, e.g. benchmarks/technical_debt/dead-code-dual-django-wagtail-001/
checks/check_dead_code_locations.sh:6-9: "Reimplemented in bash+jq (no python3 in
container — the task image ships bash/grep/jq but not python3, so the previous
`python3 -c` body exited 127). Scoring is identical…". This confirms G2 (§2.2) as
the root cause and M7 as the unlock.

**Families** (primary-signature partition of the 598, each script counted once):

| # | Family | Count | Verifies | Migration target | Example |
| --- | --- | --- | --- | --- | --- |
| F1 | File-list coverage | 31 declared ports + a large share of F-PY below | agent-declared file paths vs `ground_truth.required_files`, suffix policy, found/total | **existing** `file_extraction` plugin (params: keys, policy, threshold) | customer_escalation/support-mapping-dual-httpx-httpcore-001/checks/check_error_source.sh (already migrated, 11-line exec — the model) |
| F2 | Keyword-coverage scoring | **324** grep-based (301 `grep -qE/-qiE` + 23 plain `grep -q` binary) | GT keyword/term/API-symbol presence in a markdown report or `json.dumps(answer)`; score = found/total | **new plugin needed** (`keyword_coverage`): GT-driven term list, md-vs-json input mode, grouped-keyword mode for F5 | dependency_management/api-contract-001/checks/check_source_identification.sh |
| F3 | JSON structural schema | **93** jq-based | `answer.json` array under named key, each entry's keys ⊇ required set; valid/total | **new plugin needed** (`json_structure`): array-key + required-key-set params | dependency_management/dep-graph-dual-nextjs-webpack-001/checks/check_dependency_chain.sh |
| F4 | Topological ordering | **11** | REFACTOR_PLAN.md order vs GT dependency graph | **existing** `topological_order` (already imported in-container by all 11 — model migrations) | technical_debt/refactor-orchestration-001/checks/check_topo_order.sh |
| F-PY | Inline-python scoring | **168** `python3 -c`/heredoc on python-base images | mixed: mostly F1/F2/F3 logic inlined in Python | reclassify each into F1/F2/F3 and migrate with those families (interpreter already present, so these can migrate before M7) | dependency_management/ccx-dep-trace-106/checks/check_source_files.sh (re-introduces the unanchored-substring soft-cheat file_extraction's suffix policy was built to kill) |
| F5 | Semantic-chain matching | small subset of F2 | per-GT-chain-step keyword groups, all-of-group substring match | F2 plugin, grouped mode | customer_escalation/support-mapping-dual-spring-kafka-rebalance-002/checks/check_error_chain.sh |
| F6 | Free-form quality heuristics | small tail | ad-hoc "≥N of M criteria" regex + min-length | stays a script checkpoint (allowed bespoke class), preflight-gated | feature_delivery/camel-routing-arch-001/checks/check_architecture.sh |

No meaningful test-suite-execution family exists (2 scripts repo-wide). F-PY is a
correctness hotspot in its own right: several inline-python file-list checks use
the unanchored `af.endswith(gt_f)` substring match — the exact soft-cheat the
file_extraction plugin's `suffix` policy (`af == gt_f or af.endswith('/'+gt_f)`)
was written to kill (plugin docstring, lib/eb_verify/plugins/file_extraction.py:8-34,
which already states the consolidation goal: "Replaces 37 copy-pasted inline-Python
check scripts… with a single auditable scorer"). Conformance diffs on these are
*expected* and adjudicate to the NEW side — each is a live scoring-inflation bug,
and every adjudicated instance should be logged for a possible rescore of affected
locked runs via §4.

### 2.2 Library API gaps that forced the hand-ports

Four facts together made bash reimplementation the path of least resistance. All
must be closed before the grep-keyword family migrates; G1+G2 alone unblock the
29 declared ports.

- **G1 — no `plugin`/`params` slot in the checkpoint schema.** `Checkpoint` =
  `{name, weight, verifier, description, timeout_seconds}` (task_parser.py:29-35,
  parsed :227-236). A checkpoint can only point at a bash script; "checkpoint =
  file_extraction with keys=X, policy=suffix, threshold=0.5" is inexpressible.
  Fix: optional `plugin = "<artifact_type>"` + `params = {...}` table, mutually
  exclusive with `verifier`; `CheckpointRunner` dispatches in-process (G-RUNNER,
  §1.6).
- **G2 — python3 is not in non-python images.** Base is `python:3.11-bookworm`
  only for python-language tasks (dockerfile_generator.py:112-128); node/rust/java/
  ubuntu bases install only `git curl ca-certificates jq xz-utils` + Node (:139).
  The docker-cp'd `/workspace/.eb_verify` + `PYTHONPATH` (run_task.py:632-635, :878)
  is dead weight without an interpreter. Every one of the 29 hand-ports carries a
  header comment saying exactly this ("the container ships no python3", e.g.
  benchmarks/customer_escalation/support-mapping-dual-vitest-vite-optimize-001/
  checks/check_error_source.sh:11-14). The 33 library-calling checks are the
  python-language tasks where the interpreter happened to exist.
- **G3 — plugins can't emit the checkpoint output contract.** `ValidationResult`
  is `{valid, detail}` with no score (plugins/__init__.py:16-19); `cli.py
  validate-artifact` (:108-124) prints "TYPE: VALID/INVALID", not the
  `{"score","passed","detail"}` JSON that `test_runner.sh:173` greps for. Only
  `file_extraction.py` privately implements the emit contract (`_emit`,
  `_emit_infra_error`) plus its own argparse CLI (env `ANSWER_FILE`/`GT_FILE`,
  `--keys/--policy/--pass-threshold`) — it is not even a registered validator.
  Fix: add `score: float | None` to `ValidationResult` and a shared
  `emit_checkpoint_result()` helper owning the round-2 / threshold / exit-code
  state machine that the 29 ports each re-implement in awk/jq.
- **G4 — parameterless CLI.** `cmd_validate_artifact` hard-calls
  `validate(workspace)` with zero configuration (cli.py:108-124); no threshold/
  keys/policy/grounding flags, and `validate` itself takes only `workspace`
  (Protocol, plugins/__init__.py:22-27; extensions discovered by reflection,
  runner.py:302-320). Fix: `eb-verify run-plugin <type> --params-json ...
  --workspace ...` mapped onto the same dispatch as G1.

- **G5 — two missing plugins for the two biggest families.** No plugin covers
  keyword-coverage scoring (F2/F5, 324 scripts) or JSON structural validation
  (F3, 93 scripts). `keyword_coverage` (term list from ground_truth or params;
  input mode md-report | answer-json; grouped-keyword mode; found/total with
  threshold) and `json_structure` (array key + required-key superset per entry;
  valid/total) are prerequisites for ~70% of the corpus. Both are deterministic
  mechanical transforms — no ZFC concern.

Secondary (bites during migration, not root causes): fixed artifact paths baked
into plugins (`_load_agent_output` looks only at agent_output/answer.json then two
hardcoded .md names, runner.py:56-75) → params must accept artifact-path
overrides; `fact_triples` needs numpy/sklearn which pyproject does not declare
(guarded import, plugins/__init__.py:113-131) → extras split `eb-verify[core]` /
`[fact-triples]` so minimal images install the dep-free core.

**Aggregator contract diffs the conformance harness must reconcile** (these are
pre-existing divergences between the two implementations — each needs an
adjudicated decision, recorded in the harness config, before the M9 switchover):

| Axis | bash test_runner.sh (prod) | eb_verify Python path | Decision |
| --- | --- | --- | --- |
| task_score | un-normalized `Σ score·weight` (:192,:234) | normalized `Σ/Σw` (scoring.py:56-64) | keep un-normalized semantics at switchover (weights audited to sum 1.0, so equal on well-formed tasks; conformance flags any task where they differ = weight-sum bug) |
| non-JSON stdout + exit 0 | score 1.0 fallback (:79-95) | same fallback in runner.py — verify exact parity incl. timeout-124 path | must match byte-for-byte |
| judge capping | absent (applied later by run_task) | built into runner (:368-375) | runner's scoring stage must be composable so judge stays a separate host-side stage (§4) |
| grounding gate | absent | zeroes total on failed required artifact (runner.py:398-411) | gate stays host-side, applied identically in both paths during shadow window |
| output | JSON `{task_score, all_passed, checkpoints[], repos[]}` | `reward.txt` text summary | Python path gains a `--json` emitter matching the bash schema exactly (schemas/verifier_output.schema.json extended to the aggregate) |

### 2.3 Migration mechanics

**Unit of migration = one checkpoint** (not one task, not one family-wide sweep).
A checkpoint migrates by:

1. adding a declarative block to its `task.toml` entry
   (`plugin = "<name>"`, `params = {...}`), keeping `verifier = "checks/x.sh"`
   present but renamed to `checks/_legacy/x.sh`;
2. running `eb-verify conform` S1 + S2 for that task; attaching the campaign JSONL;
3. only then deleting nothing — legacy scripts are deleted per-suite at the
   suite-completion milestone, after the S3 shadow window (§5 M9) shows zero drift.

**Family order** (safest first, each family gated by the previous family's
conformance record):

1. **F1 declared file_extraction ports (31)** — plugin exists, 2 reference
   migrations already in-tree, blocked only on M7 (no python3 in those images).
2. **F4 (11)** — already call the plugin; migration is purely mechanical
   (script → declarative checkpoint), the lowest-risk cohort to shake out the
   harness.
3. **F-PY inline-python (168)** — interpreter already present, so these do NOT
   wait for M7; reclassify per script into F1/F2/F3 targets. Contains known
   soft-cheat bugs; expect adjudicated diffs.
4. **F3 structural/jq (93)** — needs the new `json_structure` plugin (G5).
5. **F2/F5 keyword-coverage (324, largest)** — needs the new `keyword_coverage`
   plugin (G5) and per-task extraction of hardcoded grep keyword lists into
   `params`/ground_truth (this extraction is itself conformance-checked: the
   plugin fed the extracted list must reproduce the grep verdict on golden
   states).
6. **F6 tail** — legitimately remains script checkpoints. The invariant becomes
   "no per-task *reimplementation* of library logic": script checkpoints allowed
   only for genuinely bespoke verification, enforced by the preflight gate below.

The 29 generic declared ports distribute across F2/F3/F5 and migrate with their
families; their headers claim "scoring semantics identical" to prior python — the
conformance harness is what turns that claim into a checked property.

**Stop-the-bleeding gate (immediate, before any migration):**
`validate_tasks_preflight.py::validate_task` (scripts/validate_tasks_preflight.py:174)
gains a check: a NEW task (not in the grandfather list generated from current HEAD)
whose checkpoints carry bash logic matching a hand-port/grep-scoring signature fails
preflight with a pointer to the declarative form. Grandfather list =
`migration_state.toml`, which doubles as the migration ledger (§5).

### 2.4 Getting the library into the container for real

Replace docker-cp-at-setup (run_task.py:627-635) with a wheel installed at image
build: `dockerfile_generator.py` emits `COPY eb_verify-<ver>-py3-none-any.whl /tmp/ +
RUN pip install` (details conditioned on the generator findings below). The copy at
`_setup_container` is then deleted — and with it the agent-owned library copy.
This is a prerequisite for the aggregator switch (§5 M9) and is **WAIT** (image
change = environment change mid-batch).

Feasibility, verified against the generator: non-python bases need one added
`apt-get install python3 python3-pip` line in `_setup_lines`
(dockerfile_generator.py:139) plus `pip install /tmp/eb_verify-*.whl`. Packaging
prerequisites: declare the extras split (`eb-verify` core stays dep-free beyond
tomli/jsonschema; `[fact-triples]` pulls numpy/scikit-learn — the boundary
plugins/__init__.py:113-131 already anticipates), and build the wheel in CI so the
image build never depends on the source tree layout. The console entry point
(`eb-verify = eb_verify.cli:main`) already exists in lib/pyproject.toml.
Interpreter pinning: install the distro python3 (bookworm/jammy = 3.11/3.10, both
≥ the 3.10 floor); record `python3 --version` + wheel version in the image label
so runs are attributable.

---

## 3. Killing the agent-writable verification surface (.meta and everything else)

### 3.1 The actual mechanism to kill

The `.meta` files are the reviewed symptom; the mechanism is **verification material
injected pre-session into agent-owned paths**:

- `_verifier_meta_by_name` (run_task.py:529-548) serializes task.toml weights/timeouts
  into `/workspace/.verifiers/<name>.meta`;
- `test_runner.sh:150-166` reads them back with a numeric-regex guard — which
  validates *format*, not *provenance*: the agent can write any well-formed
  `weight=0.0`/`weight=1.0` it likes, re-weighting its own grade;
- worse, `test_runner.sh:141` iterates `"$VERIFIER_DIR"/*.sh` — the agent can ADD a
  passing verifier (unweighted default 1.0) or rewrite existing ones;
- `test.sh` and `.eb_verify/` are equally agent-owned (run_task.py:655-662).

### 3.2 Replacement: post-session injection, root-owned, weights never leave the host

**Mechanism:** move ALL verification material out of the agent session entirely.

1. `_setup_container` stops copying `checks/`, `.meta`, `test.sh`, `eb_verify` — the
   agent session sees none of it (kills tamper AND answer-leak in one move).
2. A new `_setup_verification(container_id, task_dir, task_data)` runs *after*
   `_run_agent` returns and *before* scoring: copies check scripts (fresh from the
   host task dir) to a root-owned `/opt/eb/verifiers/`, `chmod 555`, no chown to
   agent. Scoring execs as root (`_docker_exec(..., user="root")`).
3. **Weights and timeouts never enter the container at all.** The container-side
   runner emits per-checkpoint raw results only (name, score, passed, detail,
   duration, exit_code); the HOST aggregates using weights read directly from the
   parsed task.toml (`eb_verify.scoring.compute_score`). The `.meta` format, its
   parser, and `_verifier_meta_by_name` are deleted. This is the structural fix:
   an agent cannot tamper with data that only exists host-side.
4. Interim hardening (one small diff, milestone M0): keep the current layout but
   drop `/workspace/.verifiers` and `/workspace/test.sh` from the `_chown_to_agent`
   list, chmod 755 root-owned, and run `_run_scoring`'s exec as root. Agents only
   ever needed *read* access — and per the leak argument they shouldn't even have
   that; M0 removes write, M8 removes read. Container-visible, so it must land in
   a batch window (§5), not mid-batch.

### 3.3 What enforces it stays dead

- `test_runner.sh` is deleted at M9 (bash aggregator retirement); until then it
  refuses `.meta` files with a loud infra-error once M8 lands (transition guard:
  a `.meta` file present = tampering attempt or stale harness → `verifier_infra_error`,
  routed by `_route_verifier_infra_error` to the re-run channel, never a score).
- CI integrity test: a fixture agent that (a) rewrites a verifier, (b) drops a
  `.meta`, (c) adds an extra `.sh` — asserts all three produce infra-error/no-effect,
  not a score change. Lives in `tests/integrity/` (un-skippable corpus).

### 3.4 Persist what scoring consumed (feeds §4 rescore)

At scoring time, persist to the run dir: `verifier/inputs/` = the exact check
scripts (or plugin+params manifest) + resolved weights + library version; and
`workspace_diff/` = per-repo `git diff` + untracked-file tarball (bounded; multi-GB
repos stay in the image, only agent deltas are saved). This makes every scored run
re-verifiable offline and makes S2 replay cheap.

---

## 4. Offline-rescore seam — decouple judge + scoring from docker

### 4.1 Contract

```
eb-verify rescore <run_dir> [--tier {judge|scoring|all}] [--task-root benchmarks/...]
                  [--out <new_run_dir>] [--judge-model cc:haiku]
```

Rescore NEVER mutates a locked run dir; it writes a sibling
`<run_dir>.rescore-<tag>/` with full provenance (library version, task-def git sha,
judge model, artifact source used). Aggregate campaign tooling then consumes the
rescore dirs — replacing the `rescore_*_{aq8e,uu17,pt0n}.py` monkeypatch pattern.

### 4.2 The seam: ArtifactSource

The only reason judge+scoring touch docker is artifact acquisition
(`_apply_llm_judge` cats paths via `_docker_exec`, run_task.py:975-981). Extract:

```python
class ArtifactSource(Protocol):
    def read_text(self, path: str) -> str | None: ...   # None = not found
    def list_candidates(self) -> list[str]: ...

ContainerSource(container_id)          # production: docker exec cat  (current behavior)
RunDirSource(run_dir)                  # rescore: verifier/artifacts/ persisted at run time
TraceReconstructionSource(trace_path)  # historical runs: aq8e's _reconstruct_writes, promoted
                                       # from scripts/analysis into eb_verify.judge.artifacts
```

`_apply_llm_judge` moves to `eb_verify.judge.engine.apply_judge(scores, task_def,
artifacts: ArtifactSource, judge)` — pure function of its inputs, no docker import.
`run_task.py` keeps a thin call with `ContainerSource`. The three-way fallback for
historical runs: `RunDirSource` → `TraceReconstructionSource` → hard infra-error
(never silently skip the Tier-2 cap — preserves the apfp #3 semantics).

### 4.3 Tier-1 offline re-verification

With §3.4's `workspace_diff/` persisted, `rescore --tier scoring` materializes the
workspace (image + diff replay) and re-runs checkpoints via `CheckpointRunner` — the
same S2 path the conformance harness uses (one implementation, two consumers).
For historical runs without workspace_diff, Tier-1 rescore degrades to
artifact-only checkpoints (those reading `/workspace/agent_output/*` reconstructible
from trace) and reports per-checkpoint coverage honestly in the provenance record.

### 4.4 run_task.py extraction boundary

Only what §2-§4 force: scoring+judge move into `eb_verify` (this design);
`_docker_*` primitives move to `scripts/orchestration/docker.py` ONLY as needed to
give `ContainerSource` a home. The god-module decomposition beyond that is
explicitly out of scope (glka: HOLD run_task.py refactor for planning).

---

## 5. Milestones (single-worker-bead sized) and jn73-batch safety

Safety tags:
- **SAFE** — host-side / library-only / authoring-time; cannot move any number a
  live jn73 run produces. Run any time.
- **BOUNDARY** — touches `run_task.py` or the run-dir layout additively (no score
  path change). Land between batch waves (after a wave's last container exits,
  before the next launches), never mid-wave — one harness version per wave.
- **WAIT** — container-visible (image, permissions, injected files) or changes
  scoring semantics or task definitions in the jn73 task set. Lands only in a
  declared scoring-freeze window, with the batch either not started or completed,
  and always behind a conformance record.

| # | Milestone (one worker bead each unless split noted) | Deps | Tag |
| --- | --- | --- | --- |
| M1 | Conformance harness core: `lib/eb_verify/conformance.py`, `eb-verify conform` (golden + workspace state sources), diff JSONL record, `--report` roll-up, adjudication schema. Tests ship in the bead. | — | SAFE |
| M1b | Golden conformance fixtures for the first migration cohorts (F4's 11 + F1's 31 declared ports) + CI job `conformance-migrated` wired to `migration_state.toml` | M1 | SAFE |
| M2a | Checkpoint schema: `plugin`/`params` (G1), task_parser + schema + mixed-mode dispatch in `CheckpointRunner` (G-RUNNER), exit-code/timeout fallback parity tests against test_runner.sh | — | SAFE |
| M2b | `ValidationResult.score` + shared `emit_checkpoint_result()` (G3), `eb-verify run-plugin` (G4), artifact-path override params; register `file_extraction` properly; extras split in pyproject (`[fact-triples]`) | M2a | SAFE |
| M2c | New plugins (G5): `keyword_coverage` (md/json input modes, grouped mode) + `json_structure`, each with golden + adversarial fixtures in the integrity corpus | M2b | SAFE |
| M3 | Judge rescore seam: `ArtifactSource` protocol, `ContainerSource`/`RunDirSource`/`TraceReconstructionSource` (promote aq8e `_reconstruct_writes`), move `_apply_llm_judge` → `eb_verify.judge.engine`, `eb-verify rescore --tier judge`. Parity test: replay the aq8e campaign, byte-identical outcomes. | — | BOUNDARY |
| M4 | Run-dir persistence: `verifier/inputs/` (scripts-or-plugin manifest + resolved weights + lib version), `verifier/artifacts/` (judge candidates), `workspace_diff/` (per-repo git diff + untracked tarball, size-capped) | — | BOUNDARY |
| M5 | Preflight gate: reject new hand-rolled verifier logic outside `migration_state.toml` grandfather list; authoring-guide update | M1 | SAFE |
| M6 | Migrate the first two cohorts → declarative `plugin=` checkpoints: F4's 11 topo checks (no new deps) and F1's 31 declared file_extraction ports (needs M7), one suite-batch per bead (~3 beads), each landing with its S1+S2 conformance JSONL; legacy scripts to `checks/_legacy/` | M1b, M2b, M7 | WAIT* |
| M7 | Image guarantee: python3 + pip + eb_verify wheel in every generated image (dockerfile_generator `_setup_lines`), wheel built in CI, version label; delete docker-cp of the lib | — | WAIT |
| M8 | Kill the agent-writable surface: post-session `_setup_verification`, root-owned 555 `/opt/eb/verifiers`, scoring exec as root, weights host-side only, delete `.meta` writer/reader + `_verifier_meta_by_name`; `.meta`-present → infra-error transition guard; 3 new integrity-corpus tamper vectors | M7 | WAIT |
| M9 | Aggregator switchover: `eb-verify run --json` (container) emitting the bash aggregate schema; S3 shadow window (dual-score, publish old) over ≥1 full suite sweep; flip; delete `test_runner.sh` + `checks/_legacy/` for fully-migrated suites | M2, M7, M8 | WAIT |
| M10 | Family migrations per §2.3 order (F-PY 168 → F3 93 → F2/F5 324), each bead = one suite×family with conformance record; grep keyword lists extracted into `params`/ground_truth; F-PY does not depend on M7 | M6 (F-PY: only M1b, M2c) | WAIT* |
| M11 | Offline Tier-1 rescore: `eb-verify rescore --tier scoring|all` (workspace materialization from M4 diffs, honest per-checkpoint coverage for historical runs) | M1, M3, M4 | SAFE |
| M0 | Interim tamper hardening (drop `.verifiers`/`test.sh` from chown, root-owned 755, scoring exec as root) — only if a pre-batch window exists; superseded by M8, skip if M8 lands first | — | WAIT (window) |

*WAIT\* qualifier: task-definition migrations (M6, M10) are per-task safe once a
task is outside the jn73 run set or the jn73 arm using it has completed and
locked. Maintain the exclusion list in `migration_state.toml`; the preflight gate
refuses migration commits touching tasks in the active-batch list.*

**Recommended order during jn73:** M1 → M2a → M2b → M2c → M5 → M1b → M3/M4 (first
batch-wave boundary) → M11 — the entire safety rail, both new plugins, rescore
capability, and the authoring freeze land while the batch runs, without touching a
container. Then, in the post-batch scoring-freeze window: M7 → M8 → M6 → M9 → M10
(rolling; F-PY cohorts may start earlier for tasks outside the jn73 run set). If a
verifier bug surfaces mid-batch (the pt0n scenario), M3+M11 give the one-command
auditable rescore instead of a fourth monkeypatch script.

---

## Executive summary

1. Scoring truth lives in 598 live bash checks (13 actually call the library) plus an agent-tamperable runtime stack: the agent owns the check scripts, weights (`.meta`), aggregator (`test.sh`), and library copy for its whole session — tamper AND answer-leak, wider than the reviewed weight side-channel.
2. Safety rail first: `eb-verify conform` runs verbatim-old-bash vs new-lib per checkpoint over golden fixtures, locked-run replays, and a live shadow window, and blocks any migration on unadjudicated verdict diffs — this is the paper-defensible provenance record.
3. Root causes are five closable API gaps: no `plugin/params` checkpoint slot, no python3 in non-python images, score-less plugin results, parameterless CLI, and two missing plugins (`keyword_coverage`, `json_structure`) covering ~70% of the corpus.
4. `.meta` dies structurally: verification material is injected only after the agent session, root-owned, and weights never enter the container — the host aggregates from task.toml via `eb_verify.scoring`; offline rescore comes from an `ArtifactSource` seam (container | run_dir | trace) plus persisted workspace diffs, replacing the monkeypatch scripts with `eb-verify rescore <run_dir>`.
5. Milestones M1-M5, M2c, M11 (harness, API gaps, plugins, rescore seam, preflight freeze) are SAFE during the jn73 batch; anything container-visible or task-def-changing (M0, M6-M10) waits for a scoring-freeze window or batch-set exclusion.

**First bead to cut:** M1 — `lib/eb_verify/conformance.py` + `eb-verify conform`
(golden + workspace state sources, per-checkpoint diff JSONL with adjudication
schema, `--report` roll-up), tests in the same commit. Everything else gates on it.
