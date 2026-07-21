# Decomposition: dr-2vydrm — Three-benchmark QA framework (CSB / EB / codeprobe)

Basis: epic description + 2026-04-30 tightened acceptance (A1–A6) + revised sequencing (voxa → lib → adapters → triad → lint+fairness). Bead IDs follow the epic's own numbering where it assigns them (.1–.11); the sequencing note "adapters (.2/.3/.4)" plus the existing .3 = EB fixes .3; the .2 = CSB / .4 = codeprobe mapping is an assumption (see U1). New items required by acceptance but unnumbered in the epic get letter IDs.

---

## 1. Child-bead breakdown

### V — codeprobe-voxa completion (reopened): reference scorer_family contract

**Scope:** Pre-existing bead, reopened per the update ("needs scorer_family contract"). Finish the reward-vs-IR-diagnostics separation in codeprobe so it emits the unified ScoreResult shape (reward, scorer_family, sub_scores, diagnostics.ir_metrics). This is the reference implementation of the contract that .1, .4, and .7 depend on. Not re-decomposed here; tracked as the wave-0 blocker.
**Acceptance (testable):** Run one codeprobe scoring trial; pipe output through a jsonschema check of the ScoreResult shape: `jq -e '.reward|type=="number" and .scorer_family|type=="string"'` and `jq -e '.diagnostics.ir_metrics | has("precision") and has("recall") and has("f1")'` both exit 0.
**Deps:** none. **Size:** multi-session (already in flight). **Routing:** top-tier — it defines the contract everything else imports; a subtly wrong reward/IR split here poisons all three rigs.

### .1 — benchmark_qa_core shared library

**Scope:** New schema-agnostic Python lib (~300 lines per epic) of pure functions `(instruction, oracle, repo_path, aux_files) → findings`, housing defect-class A (oracle-instruction coherence: every oracle path/symbol exists, lang/path constraints match), D (scoring-method honesty: declared scoring_method tier, checklist + secondary judge required), and E (agent-agnostic fairness: no oracle paths in CLAUDE.md/AGENTS.md/READMEs). Per A2, the lib also defines the ScoreResult schema constants (field names, types, [0..1] reward bound) that all rigs import. Includes packaging so three private repos can consume it (see U2) and a fixture-backed test suite per class.
**Acceptance (testable):** `pytest` green in the lib repo. A known-defective fixture per class returns the expected finding code (asserted in tests: planted dangling oracle path → class-A finding; undeclared scoring tier → class-D; oracle path planted in a fixture CLAUDE.md → class-E). `python -c "from benchmark_qa_core.schema import SCORE_RESULT_SCHEMA"` succeeds and a sample voxa output validates against it via jsonschema.
**Deps:** V (contract frozen). **Size:** multi-session. **Routing:** top-tier — semantic defect-detection logic plus the schema constants six downstream beads import; the highest-blast-radius item in the epic.

### .2 — CSB adapter

**Scope:** Thin wrapper feeding CSB's native task shape into benchmark_qa_core (classes A/D/E) and converting CSB scoring output to ScoreResult, dropping the pass_threshold/passed/scorable ceremony per the update. Lands in private sjarmak/CodeScaleBench; commits to a working branch only (no public push, per epic).
**Acceptance (testable):** Run the CSB QA entrypoint on one sample task: output validates against the lib's ScoreResult schema, and `jq -e '(has("pass_threshold") or has("passed") or has("scorable")) | not'` exits 0. Adapter unit test feeds one known-defective CSB task and asserts a class-A finding surfaces.
**Deps:** .1. **Size:** one-session. **Routing:** mid-tier — well-scoped port against a frozen lib API.

### .3 — EB adapter (existing open bead)

**Scope:** Per the child listing: extend `lib/eb_verify/schema_validator.py` in private sjarmak/EnterpriseBench to call benchmark_qa_core for classes A/D/E, and emit ScoreResult from EB verification runs.
**Acceptance (testable):** `python -m eb_verify` (or the repo's existing validator entrypoint) on one sample EB task emits ScoreResult-valid JSON (jsonschema check exits 0); unit test with a planted class-E violation (oracle path in a fixture README) asserts the finding is reported.
**Deps:** .1. **Size:** one-session. **Routing:** mid-tier.

### .4 — codeprobe adapter

**Scope:** Thin wrapper in private sjarmak/codeprobe feeding codeprobe's task shape into benchmark_qa_core (A/D/E). Aligns codeprobe's existing voxa-produced ScoreResult emission with the lib's schema constants (import from lib rather than local duplicates, per A2).
**Acceptance (testable):** codeprobe QA run on one sample task validates against the lib schema; `grep -rn "SCORE_RESULT" --include=*.py` in codeprobe shows imports from benchmark_qa_core, not a local copy (verifier asserts zero local redefinitions of the schema constants).
**Deps:** .1, V. **Size:** one-session. **Routing:** mid-tier.

### .5 — CSB calibration triad

**Scope:** Calibration harness + fixtures for CSB: a null submission (expected reward ≤0.1), a golden/oracle submission (≥0.9), and an adversarial-keyword-dump submission (≤0.5 — the epic calls this the highest-leverage check). Runs against the new ScoreResult contract per A3; all three fixture runs must return the contract shape. Results are captured, not gating public availability; per the epic decision they gate the upstream push, which sjarmak owns.
**Acceptance (testable):** A `calibrate` entrypoint exits 0 and emits per-gate JSON; verification command asserts `null_reward <= 0.1 && golden_reward >= 0.9 && adversarial_reward <= 0.5` from that JSON, and each of the three outputs passes the ScoreResult jsonschema check. Threshold failures exit nonzero with the failing gate named (captured as a result, filed as follow-up — not silently swallowed).
**Deps:** .2. **Size:** one-session (fixture authoring is the bulk; gated by review G4). **Routing:** mid-tier execution with top-tier review of the adversarial fixtures (see gates).

### .6 — EB calibration triad (existing open bead)

**Scope:** `eb_verify.calibrate` subcommand (per the child listing) implementing the same null/golden/adversarial gates for EB, against the contract.
**Acceptance (testable):** `python -m eb_verify calibrate` exits 0; same threshold-assertion command as .5; all three outputs ScoreResult-valid.
**Deps:** .3. **Size:** one-session. **Routing:** mid-tier.

### .7 — codeprobe calibration triad

**Scope:** Null/golden/adversarial-keyword-dump calibration for codeprobe against the voxa-derived contract (the epic notes triad "unblocks once voxa lands").
**Acceptance (testable):** codeprobe calibrate entrypoint exits 0; threshold assertions as in .5; ScoreResult-valid outputs including populated diagnostics.ir_metrics.
**Deps:** .4 (and transitively V). **Size:** one-session. **Routing:** mid-tier.

### .8a — CSB verifier-lint tool

**Scope:** Class-B linter for CSB's 781 test.sh files: checks `set -euo pipefail` present, flags `grep|head` piping, runs shellcheck; emits a machine-readable per-file report. Tool only — remediation is .8b. No dependency on the ScoreResult contract (class B touches the verifier runtime, not the schema).
**Acceptance (testable):** Lint run over the corpus reports total files == 781; a planted known-bad fixture (missing pipefail + `grep|head`) is flagged with both finding codes; a known-clean fixture passes. `jq '.total_files == 781'` on the report exits 0.
**Deps:** none. **Size:** one-session. **Routing:** mid-tier.

### .8b — CSB verifier remediation sweep

**Scope:** Fix the ~42% of 781 test.sh files (~328) failing lint until the corpus is clean per A4. Shardable across parallel workers by directory. Files that are irrecoverable (fix would require re-authoring the task) are filed as separate beads per the epic's out-of-scope rule, not silently patched.
**Acceptance (testable):** `.8a` lint report shows 0 failures, OR shows only files enumerated in a committed exceptions list where each entry carries a filed bead ID (verifier cross-checks each listed ID exists via `bd show`). `find … -name test.sh | xargs shellcheck` exits 0 for all non-excepted files.
**Deps:** .8a. **Size:** multi-session (shardable). **Routing:** cheap-tier for the mechanical fixes, with the G5 sampling gate below — bulk shell edits are exactly where a cheap model silently changes verifier semantics.

### .9 — cross-rig diagnostics plumbing (existing open bead)

**Scope:** Surface `task_time_seconds` and `token_cost_usd` per-trial in the diagnostics block across codeprobe, EB, and CSB. Three rig-local changes behind one contract field pair.
**Acceptance (testable):** One trial per rig; for each output, `jq -e '.diagnostics.task_time_seconds|type=="number"' && jq -e '.diagnostics.token_cost_usd|type=="number"'` exits 0, and task_time_seconds is > 0 for a real (non-fixture) trial.
**Deps:** .2, .3, .4 (needs each rig emitting the contract to have a diagnostics block to populate). **Size:** multi-session as one bead; acceptable to shard into three one-session rig beads at dispatch. **Routing:** mid-tier.

### .10 — codeprobe agent-agnostic fairness scan (class E)

**Scope:** Standalone scan (using the lib's class-E functions) over the codeprobe corpus: no oracle paths in CLAUDE.md / AGENTS.md / READMEs. Produces a findings report; findings become follow-up beads via the corpus-run bead.
**Acceptance (testable):** Scan exits nonzero on a fixture repo with a planted oracle path in CLAUDE.md and zero on a clean fixture; corpus scan produces a report file with per-file findings (may be empty).
**Deps:** .1. **Size:** one-session. **Routing:** cheap-tier — mechanical invocation of lib logic; the semantic judgment lives in the lib.

### .E — EB agent-agnostic fairness scan (class E, EB analog)

**Scope:** Same as .10 for sjarmak/EnterpriseBench, required by A5 ("new beads .10 / EB analog") and the gap table (class E: current children none).
**Acceptance (testable):** Same planted-fixture nonzero / clean-fixture zero test as .10; EB corpus report produced.
**Deps:** .1. **Size:** one-session. **Routing:** cheap-tier.

### .11 — codeprobe verifier-honesty lint (class B analog)

**Scope:** Per the update's new children: class-B analog for codeprobe's verifiers (codeprobe verifiers are not the CSB shell shape, so this is a separate rig-native lint, consistent with "B/C stay per-benchmark"). Flags verifier code whose mechanics can misreport (the codeprobe equivalent of pipefail/grep-head failure modes).
**Acceptance (testable):** Lint runs over all codeprobe verifiers and emits a per-verifier report; a planted known-bad verifier fixture is flagged; report file exists and parses (`jq . report.json`).
**Deps:** none hard; benefits from .8a's finding-report format for A4 consistency. **Size:** one-session. **Routing:** mid-tier — deciding what "honesty" means for codeprobe's verifier runtime is not mechanical.

### .Q1 / .Q2 / .Q3 — corpus QA runs (CSB / EB / codeprobe)

**Scope:** Per epic acceptance: each rig runs the full new QA (adapter A/D/E checks + lint + fairness scan) over its existing corpus, triages findings, and files follow-up beads for failures. Re-authoring irrecoverable tasks is explicitly out of scope — file, don't fix.
**Acceptance (testable):** A committed per-rig QA report exists listing every task with pass/finding status; for each finding class present, `bd list` shows filed follow-up beads carrying the rig's QA label (verifier cross-checks report finding count against filed-bead count, or the report explicitly records "0 findings").
**Deps:** .Q1 ← .2, .5, .8b; .Q2 ← .3, .6, .E; .Q3 ← .4, .7, .10, .11. **Size:** one-session each. **Routing:** mid-tier — the run is mechanical but triage (real defect vs checker false-positive, filing well-formed beads) needs judgment.

### .D — cross-rig dashboard / aggregate.json (A6)

**Scope:** Aggregate artifact showing reward + scorer_family + sub_scores across the three rigs; mean-reward aggregates carry scorer_family caveats when families are mixed, per CSB reporting policy. JSON artifact is the deliverable (see U8).
**Acceptance (testable):** aggregate.json exists; `jq -e '.rigs | has("csb") and has("eb") and has("codeprobe")'` exits 0; each rig entry has reward, scorer_family, sub_scores; a test aggregate mixing two scorer_family values yields a row where `jq -e '.caveat'` is non-null, and a single-family aggregate has no caveat.
**Deps:** .9 plus the triad/corpus outputs it aggregates (.5/.6/.7 minimum). **Size:** one-session. **Routing:** mid-tier.

### .R — public response doc draft

**Scope:** Per epic acceptance: draft (in the private repo, NOT published) a response summarizing remediation status against the external verification report — the five defect classes, per-rig status, calibration-triad results, verifier-lint status, and what was filed as follow-up. sjarmak owns publication.
**Acceptance (testable):** Doc file exists in the private repo; a check script greps for one section heading per defect class (A–E) and per rig, and confirms the triad numbers cited in the doc match the committed calibration JSON (numeric cross-check, not prose review). No publish action taken (no PR, no public push — verifier confirms git log shows working-branch commit only).
**Deps:** .Q1–.Q3, .D. **Size:** one-session. **Routing:** top-tier — externally-facing narrative synthesizing cross-rig results; errors here are reputational, and the numbers must be represented faithfully.

---

## 2. Dependency graph and parallel waves

```
V ──────────────► .1 ──┬─► .2 ─► .5 ──┬──────────► .Q1 ─┐
                       ├─► .3 ─► .6 ──┼─► (.E)───► .Q2 ─┼─► .R
                       ├─► .4 ─► .7 ──┼─► (.10,.11) .Q3 ─┤
                       ├─► .10        │                  │
                       └─► .E         └(.2/.3/.4)─► .9 ─► .D ─┘
.8a ─► .8b ────────────────────────────────────────► .Q1
.11 ───────────────────────────────────────────────► .Q3
```

Maximal parallel waves (true data dependencies; see U5 on the epic's stated serialization):

- **Wave 0 (3 parallel):** V, .8a, .11 — the lint track has no dependency on the contract and fills capacity while voxa is the critical path.
- **Wave 1 (2 parallel):** .1, .8b
- **Wave 2 (5 parallel):** .2, .3, .4, .10, .E
- **Wave 3 (4 parallel):** .5, .6, .7, .9
- **Wave 4 (4 parallel):** .Q1, .Q2, .Q3, .D
- **Wave 5 (1):** .R

Critical path: V → .1 → adapter → triad → corpus run → .R (6 hops). Everything else hangs off it with slack.

---

## 3. Routing summary

| Tier  | Beads                                             | Reason                                                                                                                                     |
| ----- | ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| Top   | V, .1, .R (+ review duty at G1/G2/G4)             | Contract and lib are imported by everything; wrong semantics propagate to all rigs. .R is externally-facing synthesis.                     |
| Mid   | .2, .3, .4, .5, .6, .7, .8a, .9, .11, .Q1–.Q3, .D | Well-scoped execution against a frozen API or an existing corpus; needs code judgment but not architecture.                                |
| Cheap | .8b (sharded), .10, .E                            | Mechanical: apply lint fixes, invoke lib scan functions. Semantic judgment already lives upstream (in .8a's rules and .1's class-E logic). |

---

## 4. Review gates (poison-prevention points)

- **G1 — after V, before .1 starts:** top-tier sign-off freezing the ScoreResult contract (field semantics, reward bounds, ir_metrics optionality per U4). Fan-out poison point #1: six beads import this schema.
- **G2 — after .1, before adapters dispatch:** code review of the lib plus a mechanical gate: the class-A/D/E fixture tests and schema round-trip must pass in CI, not just locally. Fan-out poison point #2: a false-negative defect checker here makes every corpus run downstream report a clean corpus that isn't.
- **G3 — after each adapter (mechanical, cheap):** automated schema-conformance check of one real task's output before the triad bead unblocks. Catches adapter drift without a human in the loop.
- **G4 — triad fixture review, before triad results are recorded:** top-tier review of the adversarial-keyword-dump fixtures specifically. The epic calls the triad the highest-leverage check; a weak adversarial fixture yields a passing calibration that blesses gameable verifiers — the most damaging silent poisoning available in this epic, because triad results gate sjarmak's upstream push.
- **G5 — .8b sampling gate:** mid-tier reviews a random ≥10% of cheap-tier shell fixes per shard, checking behavior preservation (same exit codes on a golden run), not style. A behavior-changed test.sh silently corrupts the golden leg of the CSB calibration.
- **G6 — pre-.R numbers audit:** before drafting, cross-check aggregate.json against raw triad and corpus-run outputs (scripted numeric diff). The response doc must not inherit an aggregation bug.

---

## 5. Underspecified — questions before dispatch, with provisional assumptions

- **U1 — Adapter bead-ID mapping.** Sequencing says "adapters (.2/.3/.4)" and only .3 (EB) exists in CHILDREN. _Assumption:_ .2 = CSB, .4 = codeprobe.
- **U2 — Where does benchmark_qa_core live?** The epic specifies the lib but not its home repo or how three private repos consume it (pip package, git submodule, vendored). This blocks .1's packaging work. _Assumption:_ standalone private repo, consumed as an installable package by all three rigs.
- **U3 — CSB class-E coverage.** The gap table says "new bead per rig" for class E, but A5 names only .10 and the EB analog. _Assumption:_ CSB's class-E check runs via the .2 adapter + .Q1 corpus run rather than a standalone bead; if a standalone CSB scan was intended, it is a one-session cheap-tier clone of .10.
- **U4 — ScoreResult field optionality.** Not every rig/task has IR metrics (CSB SDLC tasks) — is diagnostics.ir_metrics required? _Assumption:_ schema marks ir_metrics optional/nullable; reward, scorer_family, sub_scores, task_time_seconds, token_cost_usd are required. Must be settled at G1.
- **U5 — Is "then lint + fairness in parallel" (after triad) a hard ordering?** No data dependency forces it. _Assumption:_ it expresses priority (voxa/lib are the critical path), not a dependency; the wave plan starts lint tooling in wave 0. If sjarmak intended a hard order, waves 0–1 lose .8a/.8b/.11 and the schedule stretches.
- **U6 — Definition of "verifier-lint clean" (A4).** All 781 files, or all minus irrecoverables? _Assumption:_ all files pass, except an explicit exceptions list where each entry has a filed follow-up bead (consistent with the epic's out-of-scope rule).
- **U7 — Adversarial threshold semantics.** Is ≤0.5 asserted per adversarial fixture or on the mean across an adversarial set? _Assumption:_ mean across the set ≤0.5, with per-fixture results captured; must be pinned at G4 so all three rigs assert identically.
- **U8 — Dashboard deliverable shape.** "Cross-rig dashboard / aggregate.json" — visual dashboard or JSON artifact? _Assumption:_ aggregate.json is the acceptance artifact; any rendering is out of scope unless requested.
- **U9 — Class-A depth for EB.** The gap table notes EB lacks a zat9-style (mining-side) class-A analog. Is the .3 adapter's static oracle-existence check sufficient, or does EB need mining-side curation? _Assumption:_ static check suffices for this epic; mining-side defects found by .Q2 are filed separately per the re-mining out-of-scope rule.
- **U10 — voxa ownership boundary.** voxa is reopened and "in flight this session" — is completing it part of this epic's dispatch or an external blocker? _Assumption:_ external blocker owned by its current assignee; this plan schedules against its completion (wave 0) but does not re-decompose it.
