# Decomposition — dr-2vydrm: Three-benchmark QA framework (CSB / EB / codeprobe)

Scope note: this breakdown is derived from the epic DESCRIPTION, NOTES, and the
A1–A6 updated acceptance. The bd CHILDREN block only materializes three beads
(`.3`, `.6`, `.9`; 0/3 complete), but the body enumerates `.1`–`.11` plus the
reopened `codeprobe-voxa` and states additional acceptance-level deliverables
(corpus runs, public response doc, cross-rig aggregate). Every bead below traces
to a stated requirement; the tracker-vs-body gap is the first underspecified
item in §5.

---

## 1. Child-bead breakdown

Numbering follows the epic's own `.N` labels where it assigns them. Beads that
the acceptance criteria require but the epic never numbers are marked **(implied,
unnumbered)** and given provisional labels `.12`–`.15`.

### W0 — codeprobe-voxa (reopened) — reference `scorer_family` contract

- **Scope:** Separate reward from IR diagnostics for codeprobe and define the
  canonical `scorer_family` string + `sub_scores` breakdown that the unified
  `ScoreResult` contract is built on. Per the sequencing change, this is the
  reference the lib (`.1`) and the codeprobe adapter/triad (`.4`/`.7`) import.
  Covers class D for codeprobe.
- **Acceptance (testable):** A codeprobe trial emits JSON where `reward`,
  `scorer_family`, `sub_scores`, and `diagnostics.ir_metrics.{precision,recall,f1}`
  are distinct top-level/nested keys with `reward` no longer equal to `recall`
  (the recall-only-reward defect). Concretely:
  `jq -e '.reward != .diagnostics.ir_metrics.recall and (.scorer_family|type=="string")' trial.json`
  exits 0.
- **Depends on:** none (root).
- **Size:** multi-session.

### `.1` — benchmark_qa_core shared lib

- **Scope:** ~300-line schema-agnostic Python lib. Pure functions
  `(instruction, oracle, repo_path, aux_files) → findings` housing class A
  (oracle-instruction coherence), D (scoring honesty), and E (agent-agnostic
  fairness) logic. Defines the schema constants for the unified `ScoreResult`
  contract (`reward`, `scorer_family`, `sub_scores`, `diagnostics.*`) that all
  three rigs import (A2). Absorbs the schema shape fixed by voxa.
- **Acceptance (testable):** `python -c "import benchmark_qa_core as c; c.SCORE_RESULT_KEYS"`
  returns the exact contract keys; unit tests for the A/D/E pure functions pass
  (`pytest tests/ -q` green) with at least one fixture per class exercising a
  known-defective input and asserting a finding is returned.
- **Depends on:** voxa.
- **Size:** multi-session.

### `.2` — CSB adapter

- **Scope:** Thin wrapper feeding CSB native task shape into
  benchmark_qa_core (classes A/D/E) and emitting the unified `ScoreResult`.
  Drops CSB's `pass_threshold`/`passed`/`scorable` ceremony in favor of the
  reward contract. Includes migrating any CSB consumers of the dropped fields.
- **Acceptance (testable):** running the CSB scorer on a sample task emits the
  contract shape (`jq -e` over `SCORE_RESULT_KEYS`) and no longer emits
  `passed`/`pass_threshold`; `grep -rn "pass_threshold\|scorable" <csb scorer paths>`
  returns only intentional references (ideally none in the emit path).
- **Depends on:** `.1`.
- **Size:** multi-session (consumer migration widens it).

### `.3` — EB adapter _(materialized in tracker)_

- **Scope:** Extend `lib/eb_verify/schema_validator.py` to call
  benchmark_qa_core (classes A/D/E) and emit the unified contract.
- **Acceptance (testable):** `pytest lib/eb_verify/tests/test_schema_validator.py -q`
  green; an EB task run emits contract-shaped JSON validated against
  `SCORE_RESULT_KEYS`.
- **Depends on:** `.1`.
- **Size:** one-session.

### `.4` — codeprobe adapter

- **Scope:** Thin wrapper feeding codeprobe task shape into the lib and emitting
  the contract, consuming voxa's `scorer_family` directly.
- **Acceptance (testable):** codeprobe trial emits contract shape; adapter
  imports lib constants (`grep -n "from benchmark_qa_core"` present, no inlined
  duplicate key list).
- **Depends on:** `.1`, voxa.
- **Size:** one-session.

### `.5` — CSB calibration triad

- **Scope:** Calibration-triad runner for CSB producing null/golden/
  adversarial-keyword-dump fixtures, each returning the contract shape, and
  asserting the gates: null ≤0.1, golden ≥0.9, adversarial ≤0.5 (highest-leverage
  check). Results captured, NOT blocking public availability.
- **Acceptance (testable):** a `calibrate` run writes a results file where
  `null.reward ≤ 0.1`, `golden.reward ≥ 0.9`, `adversarial.reward ≤ 0.5`; each
  fixture output validates against `SCORE_RESULT_KEYS`. Command exits non-zero on
  gate breach but does not gate corpus existence (breach = upstream-push block).
- **Depends on:** `.2`.
- **Size:** one-session (multi if adversarial fixtures need authoring per task family).

### `.6` — EB calibration triad _(materialized in tracker)_

- **Scope:** `eb_verify.calibrate` subcommand with null/golden/adversarial gates,
  fixtures returning contract shape.
- **Acceptance (testable):** `python -m eb_verify calibrate` emits the three
  fixture results with the same gate thresholds and contract validation as `.5`.
- **Depends on:** `.3`.
- **Size:** one-session.

### `.7` — codeprobe calibration triad

- **Scope:** Calibration triad for codeprobe against the new contract.
- **Acceptance (testable):** triad run over codeprobe fixtures returns contract
  shape and enforces the three gates as in `.5`.
- **Depends on:** `.4`, voxa.
- **Size:** one-session.

### `.8` — CSB verifier-lint (class B)

- **Scope:** Bring CSB `test.sh` verifiers to mechanical correctness:
  `set -euo pipefail`, no `grep | head` fragility, shellcheck clean. 42% of 781
  files currently fail.
- **Acceptance (testable):** `shellcheck` clean across all CSB `test.sh`
  (`find <corpus> -name test.sh -print0 | xargs -0 shellcheck -S warning` exits 0),
  and `grep -rL "set -euo pipefail" <test.sh files>` is empty.
- **Depends on:** none (independent of lib — see §2 note).
- **Size:** needs-split (781 files, batch by task directory).

### `.9` — cross-rig diagnostics plumbing _(materialized in tracker)_

- **Scope:** Surface `diagnostics.task_time_seconds` and
  `diagnostics.token_cost_usd` per-trial across codeprobe / EB / CSB.
- **Acceptance (testable):** a trial from each rig emits non-null
  `diagnostics.task_time_seconds` (float ≥0) and `diagnostics.token_cost_usd`
  (float ≥0): `jq -e '.diagnostics.task_time_seconds and .diagnostics.token_cost_usd'`
  exits 0 for a sample trial per rig.
- **Depends on:** `.1` (contract keys).
- **Size:** one-session.

### `.10` — codeprobe agent-agnostic fairness scan (class E)

- **Scope:** Scan codeprobe `CLAUDE.md` / `AGENTS.md` / `READMEs` for leaked
  oracle paths/symbols; wire to lib's E logic.
- **Acceptance (testable):** scanner run over codeprobe agent-facing docs returns
  zero oracle-path findings (`benchmark_qa_core` E function returns empty findings
  list); a planted-leak fixture is detected in unit test.
- **Depends on:** `.1`, `.4`.
- **Size:** one-session.

### `.11` — codeprobe verifier-honesty lint (class B analog)

- **Scope:** Verifier-honesty lint for codeprobe verifiers (the codeprobe analog
  of CSB shell lint — codeprobe has no shell verifiers, so this checks the
  verifier's reward honestly reflects completion, not keyword presence).
- **Acceptance (testable):** lint run over codeprobe verifiers exits 0; a fixture
  verifier that rewards keyword-presence-only is flagged.
- **Depends on:** `.4` (and lib D logic from `.1`).
- **Size:** one-session.

### `.12` — EB agent-agnostic fairness (implied, unnumbered — A5 "EB analog")

- **Scope:** EB fairness scan (class E), the "EB analog" A5 names alongside `.10`.
- **Acceptance (testable):** scan over EB agent-facing docs returns zero
  oracle-path findings via lib E logic.
- **Depends on:** `.1`, `.3`.
- **Size:** one-session.

### `.13` — Cross-rig aggregate / dashboard (implied, unnumbered — A6)

- **Scope:** `aggregate.json` + dashboard showing `reward` + `scorer_family` +
  `sub_scores`; mean-reward aggregates carry `scorer_family` caveats when
  scorer families are mixed (per CSB reporting policy).
- **Acceptance (testable):** `aggregate.json` contains per-rig `reward`,
  `scorer_family`, `sub_scores`; a mixed-family aggregate row carries a
  `scorer_family_caveat` field (`jq -e` asserts its presence when
  `scorer_family` values differ across inputs).
- **Depends on:** `.2`, `.3`, `.4`, `.9`.
- **Size:** one-session (multi if the dashboard is a new UI, not just JSON).

### `.14a/b/c` — Per-rig corpus QA run + follow-up bead filing (implied — Acceptance bullet 4)

- **Scope:** Run the new QA (A/B/C/D/E as applicable) over each rig's existing
  corpus; file follow-up beads for each failure. One instance per rig.
- **Acceptance (testable):** a run report per rig lists pass/fail per task and a
  follow-up bead id per failure (`bd list --parent dr-2vydrm --label followup`
  returns ≥1 per rig with failures, or the report explicitly records zero
  failures).
- **Depends on:** that rig's adapter + triad + lint/fairness (`.2/.5/.8` for CSB;
  `.3/.6/.12` for EB; `.4/.7/.10/.11` for codeprobe).
- **Size:** one-session each (mechanical run; triage of which failures matter is
  the cognitive part).

### `.15` — Public response doc draft (implied — Acceptance bullet 5)

- **Scope:** Draft (in the private repo, NOT published) summarizing remediation
  status across the three rigs. Worker drafts only; publishing is out of scope
  (sjarmak owns public sync).
- **Acceptance (testable):** a markdown draft exists in the private repo
  covering all five defect classes × three rigs with remediation status;
  passes a de-slop / writing-voice review; contains no push/publish action.
- **Depends on:** `.14a/b/c` (summarizes their results).
- **Size:** one-session.

---

## 2. Dependency graph and parallel waves

```
voxa
 └─> .1 (lib) ──> .2 (CSB adapter) ──> .5 (CSB triad) ─┐
      │      └──> .3 (EB adapter)  ──> .6 (EB triad)  ─┤
      │      └──> .4 (cp adapter)* ──> .7 (cp triad)* ─┤   (*also depend on voxa)
      │      └──> .9 (diagnostics) ─────────────────────┤
      │                                                 │
      ├──> .10 (cp fairness)   [needs .4]               │
      ├──> .12 (EB fairness)   [needs .3]               │
      └──> .11 (cp verifier-honesty) [needs .4]         │
                                                        │
 .8 (CSB shell-lint)  — independent of lib —────────────┤
                                                        ▼
                        .13 aggregate  [needs .2/.3/.4/.9]
                                │
                 .14a/b/c corpus runs  [per-rig adapter+triad+lint/fairness]
                                │
                          .15 response doc
```

**Maximal parallel waves:**

- **Wave 0:** `voxa`. (`.8` CSB shell-lint can also start here — it touches the
  verifier runtime only, not the lib. The epic _sequences_ lint last, but it has
  no real dependency; pulling it forward shortens the critical path. See §5-Q8.)
- **Wave 1:** `.1` (lib).
- **Wave 2:** `.2`, `.3`, `.4`, `.9` (all import lib; `.4` also needs voxa, met).
- **Wave 3:** `.5`, `.6`, `.7` (triad per adapter); `.10`, `.11`, `.12`
  (fairness/honesty per adapter). All parallel.
- **Wave 4:** `.13` (aggregate); `.14a/b/c` (corpus runs per rig).
- **Wave 5:** `.15` (response doc).

Critical path: `voxa → .1 → adapter → triad → corpus run → response doc`
(6 serial stages). Everything else fans out inside waves 2–4.

---

## 3. Routing per bead

| Bead                      | Tier                                                             | Reason                                                                                                                                                                                                                                                                   |
| ------------------------- | ---------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| voxa                      | **top**                                                          | Defines the reference scoring contract; semantic scoring-honesty reasoning (reward vs recall, `scorer_family` taxonomy). A wrong contract poisons every downstream bead.                                                                                                 |
| `.1` lib                  | **top**                                                          | Schema-agnostic pure-function design + A/D/E semantic logic + the contract constants all rigs import. Architecture-defining, highest blast radius.                                                                                                                       |
| `.2` CSB adapter          | **mid**                                                          | Well-scoped wiring into a defined lib interface; the one judgment call (migrating dropped `pass_threshold` consumers) is bounded — flag for review, not top-tier authoring.                                                                                              |
| `.3` EB adapter           | **mid**                                                          | Concretely scoped ("extend schema_validator.py"). Execution against a fixed interface.                                                                                                                                                                                   |
| `.4` cp adapter           | **mid**                                                          | Wiring codeprobe shape + voxa `scorer_family` into the lib. Bounded execution.                                                                                                                                                                                           |
| `.5`/`.6`/`.7` triad      | **mid**, with a **top-tier review pass on adversarial fixtures** | Subcommand plumbing is mechanical; but the adversarial-keyword-dump fixture (≤0.5, the "highest-leverage check") requires judgment about what a keyword-dump-with-no-solution looks like per task family. Route execution mid, gate fixture design with top-tier review. |
| `.8` CSB shell-lint       | **cheap** (bulk) + **mid** (spot review)                         | Mechanical shell rewrites (`set -euo pipefail`, kill `grep                                                                                                                                                                                                               | head`, shellcheck) across 781 files. Cheap tier for the sweep; mid-tier reviews the batches where shellcheck fixes change semantics. |
| `.9` diagnostics          | **mid** (or cheap)                                               | Plumbing two float fields per-trial across rigs. Mechanical once the contract exists.                                                                                                                                                                                    |
| `.10`/`.12` fairness      | **mid**                                                          | Scan agent docs for oracle-path leaks. Scan is near-mechanical but leak judgment has semantic edges; lib E logic does the work, adapter feeds it.                                                                                                                        |
| `.11` cp verifier-honesty | **mid**                                                          | More judgment than `.8` (does reward honestly reflect completion vs keyword presence) — semantic-ish, but bounded by the lib's D logic.                                                                                                                                  |
| `.13` aggregate           | **mid**                                                          | Aggregation + the scorer_family-caveat reporting rule. Deterministic once inputs land.                                                                                                                                                                                   |
| `.14a/b/c` corpus runs    | **cheap** (run) + **mid** (triage)                               | Running the QA and collecting failures is mechanical; deciding which failures are real defects vs irrecoverable tasks (out of scope to re-author) is the cognitive part.                                                                                                 |
| `.15` response doc        | **mid** draft + **top/human** review                             | Public-facing prose (even unpublished). Needs the writing-voice/de-slop pass and a human gate before it can ever be published.                                                                                                                                           |

---

## 4. Review gates (so a wrong early result can't poison later waves)

1. **HARD GATE after voxa (Wave 0→1).** The `scorer_family` + `sub_scores`
   contract shape is imported by the lib and every adapter/triad. A verification
   agent must actively run the `jq` reward≠recall assertion AND confirm the
   `scorer_family` taxonomy is stable (not a placeholder). Nothing in Wave 1
   starts until this passes. This is the single highest-leverage gate.

2. **HARD GATE after `.1` lib (Wave 1→2).** `SCORE_RESULT_KEYS` and the A/D/E
   pure functions are the shared contract for all three adapters. Verify by
   importing the constants and running the per-class fixture unit tests; a wrong
   key set or a false-negative A/D/E function silently corrupts all three rigs.
   No adapter dispatches until green.

3. **Per-rig triad = the calibration gate (Wave 3).** The triad IS the
   correctness check on each adapter's contract emission. Per the epic Decision,
   a triad breach (null >0.1, golden <0.9, adversarial >0.5) **blocks the
   upstream push, not the private corpus's existence** — so the gate records a
   breach and halts _promotion_, but does not block later private waves. The
   adversarial-keyword-dump gate specifically catches an adapter that rewards
   keyword presence; treat a failing adversarial gate as a stop-and-fix on that
   rig's adapter before its corpus run.

4. **Aggregate reporting gate (Wave 4).** `.13` must be checked for the
   scorer_family-caveat rule: any mean-reward over mixed families that lacks the
   caveat is a reporting-policy violation. Verify with the mixed-input `jq`
   assertion.

5. **External-artifact gate (Wave 5, standing rule).** `.15` and any push are
   external. Workers commit to private working branches only — no public PRs, no
   pushes to `sourcegraph/CodeScaleBench` (out of scope; sjarmak owns sync). The
   response doc gets a writing-voice pass and stops at draft; publishing requires
   explicit human approval.

---

## 5. Underspecified — resolve before dispatch (with provisional assumptions)

**Q1. Tracker vs body mismatch.** bd shows only `.3`, `.6`, `.9` as open
children (0/3); the body enumerates `.1`,`.2`,`.4`,`.5`,`.7`,`.8`,`.10`,`.11` +
voxa. Are the others closed, tracked elsewhere, or not yet created?
_Assumption:_ they are not yet materialized and must be created before dispatch;
only three exist today.

**Q2. Where does benchmark_qa_core physically live?** It is "shared across 3
rigs" but each rig is a separate private repo (`sjarmak/CodeScaleBench`,
`/EnterpriseBench`, `/codeprobe`). Is the lib a 4th shared repo/package, or
vendored into each? This is load-bearing for `.1` and every adapter import.
_Assumption:_ a single installable package (own repo or a shared path) that all
three import; NOT copy-pasted per rig (copy-paste would violate the "rigs import
from lib" acceptance A2). Must confirm the host location.

**Q3. voxa status and tracker location.** Listed as `in_progress` (line 36) and
`REOPEN` (line 120). Is `codeprobe-voxa` a codeprobe-rig bead (cross-tracker
dependency), not a `dr-2vydrm.*` child? _Assumption:_ it is a codeprobe-rig bead,
currently reopened, and is a hard external predecessor treated as Wave 0.

**Q4. Class E coverage for CSB.** A5 names only `.10` (codeprobe) and an "EB
analog"; no CSB fairness bead. Does CSB fairness ride inside `.2` via the lib's E
logic? _Assumption:_ CSB + EB fairness are exercised through their adapters
calling lib E; codeprobe gets a dedicated `.10` because it has agent-facing docs
(`AGENTS.md`) most likely to leak. I still created `.12` for EB per A5; if CSB
needs a dedicated scan, add `.16`.

**Q5. Unnumbered acceptance deliverables.** A6 dashboard/aggregate, the corpus
runs (Acceptance bullet 4), and the response doc (bullet 5) have no bead numbers.
_Assumption:_ they need beads (`.13`, `.14a/b/c`, `.15` above); they are required
by acceptance, not gold-plating.

**Q6. EB class B.** Line 118 says "EB no shell verifiers." Is there an EB
verifier-lint at all, or is class B N/A for EB? _Assumption:_ no shell-lint bead
for EB; EB's verifier correctness is covered by `.3` + triad. Confirm B is
genuinely N/A rather than a different-runtime lint.

**Q7. EB class A without a zat9 equivalent.** Line 117: "EB has no upstream
zat9." Does `.3` need to also do symbol-reference-trace mining, or just call the
lib's A logic on existing EB tasks? _Assumption:_ `.3` calls lib A on existing
tasks; no separate mining bead (re-mining is out of scope, line 79).

**Q8. Shell-lint sequencing.** The epic sequences lint "last" (line 143), but
`.8` has no dependency on the lib or contract. _Assumption:_ lint is logically
independent and can run from Wave 0; I placed it early to shorten the critical
path. If the "last" ordering is a resourcing choice (do it after the higher-value
contract work), respect that — but it is not a true dependency.

**Q9. CSB contract migration blast radius.** "Drops CSB's
pass_threshold/passed/scorable ceremony" is a breaking change. Are there
downstream consumers (dashboards, gating scripts) of those old fields? _Assumption:_
`.2` must migrate the emit path and any in-repo consumers; consumers outside the
CSB repo are out of scope and flagged to sjarmak.

**Q10. Adversarial-keyword-dump fixture construction.** The triad's
highest-leverage check needs an "adversarial-keyword-dump" fixture per rig, but
its construction (which keywords, sourced how) is unspecified. _Assumption:_
build it from the oracle's expected keywords/symbols assembled into a response
that contains no working solution — testing that the verifier rewards substance,
not keyword presence (≤0.5). This is why the fixture design gets a top-tier
review pass in §3.

**Q11. Thresholds fixed vs per-rig.** null ≤0.1 / golden ≥0.9 / adversarial ≤0.5
— fixed constants or per-rig tunable? _Assumption:_ fixed as stated across all
three rigs; encode them as lib constants so the triad beads share one source.
