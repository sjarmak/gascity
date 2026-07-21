# Decomposition: dr-2vydrm — Three-benchmark QA framework

## 0. Reconciling epic state before designing anything

The epic's notes narrate a full bead-numbering plan (.1 through .11) as though all of it already
exists, but the live CHILDREN section lists only three real beads right now: **dr-2vydrm.3** (EB
adapter), **dr-2vydrm.6** (EB calibration triad), **dr-2vydrm.9** (cross-rig diagnostics
plumbing) — "0/3 complete". Everything else referenced by ID in the defect-coverage table and the
"children added this update" list (.1, .2, .4, .5, .7, .8, .10, .11) is a **planned slot**, not a
confirmed bead. I'm treating only .3/.6/.9 as pre-existing and designing the rest fresh below. Two
external, already-in-flight beads outside this epic's own child list also gate work here:
**codeprobe-zat9** (closed — class-A oracle curator for codeprobe) and **codeprobe-voxa**
(reopened — reference scorer_family/reward-vs-diagnostics contract). Neither is a sibling bead I
create; voxa in particular is a hard blocking precondition for two of the units below.

---

## 1. Child-bead breakdown

Each unit below is new unless marked EXISTING (verify, don't recreate) or EXTERNAL (not part of
this epic's child set, wire as a blocking dependency only).

### W1 — LIB: benchmark_qa_core shared library + ScoreResult schema

**Trace:** Architecture section — "benchmark_qa_core: shared Python lib (~300 lines,
schema-agnostic)... Houses A/D/E logic" — plus A2 ("lib defines schema constants for the
contract; rigs import from lib") and the unified ScoreResult JSON shown in the notes.
**Scope:** Implement the pure-function interface `(instruction, oracle, repo_path, aux_files) →
findings` covering class A (oracle-instruction coherence), class D (declared scoring_method tier +
checklist/secondary-judge requirement), and class E (oracle-path leakage into
CLAUDE.md/AGENTS.md/READMEs). Define the ScoreResult schema (`reward`, `scorer_family`,
`sub_scores`, `diagnostics{task_time_seconds, token_cost_usd, ir_metrics{precision,recall,f1}}`)
as an importable, versioned artifact.
**Acceptance:**

- `pytest` exits 0 in the benchmark_qa_core package.
- A planted fixture with a dangling oracle path returns a class-A finding; a clean fixture with a
  valid oracle path returns none.
- A planted fixture with an oracle path leaked into a CLAUDE.md/AGENTS.md/README aux file returns a
  class-E finding; a clean fixture returns none.
- A planted fixture missing a declared `scoring_method` tier is flagged; a fixture with a valid
  tier + checklist + secondary-judge reference passes.
- `python -c "from benchmark_qa_core.schema import SCORE_RESULT_SCHEMA"` succeeds and the schema's
  top-level keys are exactly `{reward, scorer_family, sub_scores, diagnostics}`.
  **Deps:** codeprobe-voxa (EXTERNAL, blocking — voxa is the reference scorer_family contract this
  schema must absorb per the epic's sequencing note). No sibling-bead deps. **Size:**
  multi-session.
  **Routing:** deep reasoning, criteria-only — six-plus downstream units (three adapters, two
  fairness scans, diagnostics plumbing) import this schema; wrong A/D/E semantics propagate silently
  to every rig.
  **Gate:** fan-out poison point. Every downstream consumer blocks on W1's REVIEW bead, not the work
  bead. The review must name the failure it intercepts: a false-negative class-A detector here means
  every rig's adapter reports a clean oracle map even where one is broken, which then poisons the
  corpus-run follow-up filing and the response doc's remediation claims for all three rigs at once.

### W2 — CSB adapter

**Trace:** "Per-rig adapters: thin wrappers feeding native task shape into the lib" + A1 (contract
emitted by CSB) + A2 ("rigs import from lib") + the explicit instruction to drop
`pass_threshold`/`passed`/`scorable`.
**Scope:** Thin adapter in private sjarmak/CodeScaleBench mapping CSB's native task shape into
W1's interface and emitting the unified ScoreResult, with a CSB-specific non-empty `scorer_family`
value (this is the gap the coverage table flags as "needs scorer_family coverage" for class D
outside codeprobe).
**Acceptance:**

- Adapter QA entrypoint on one CSB sample task emits JSON that validates against W1's ScoreResult
  jsonschema (exit 0).
- A known-defective CSB fixture (planted dangling oracle path) surfaces a class-A finding; a clean
  fixture surfaces none.
- Emitted JSON contains no `pass_threshold`, `passed`, or `scorable` keys.
- Emitted `scorer_family` is a non-empty, CSB-specific string (not the codeprobe/voxa default).
  **Deps:** W1 REVIEW (fan-out). **Size:** multi-session.
  **Routing:** well-scoped execution, criteria-only — implementing against a frozen interface, no new
  architecture.

### W3 — EB adapter — **EXISTING: dr-2vydrm.3**

Already covers classes A/D/E for EB per its own description. Not recreated. Action item: verify
its dependency is (or gets updated to be) `blocks: dr-2vydrm.3 ← W1's REVIEW bead`, and add a
`scorer_family` non-empty/EB-specific check to its acceptance if not already present (same D-gap
reasoning as W2).

### W4 — codeprobe adapter

**Trace:** same architecture clause as W2, for codeprobe; sequencing note names this explicitly
("codeprobe-voxa... is the reference scorer_family contract that .1/.4/.7 depend on").
**Scope:** Thin adapter in private sjarmak/codeprobe, reusing voxa's scorer_family definition
rather than inventing a new one.
**Acceptance:** same shape as W2 (jsonschema-valid sample output, planted-defect fixture surfaces
class-A finding, clean fixture passes) plus: emitted `scorer_family` matches voxa's declared value
exactly (diff against voxa's contract fixture).
**Deps:** W1 REVIEW (fan-out) + codeprobe-voxa (EXTERNAL, blocking — named directly in the epic).
**Size:** multi-session.
**Routing:** well-scoped execution, criteria-only.

### W5 — CSB calibration triad

**Trace:** class C bright-line rule (null ≤0.1, golden ≥0.9, adversarial-keyword-dump ≤0.5) + A3.
**Scope:** `calibrate` subcommand (mirrors EB's existing `eb_verify.calibrate`) running the three
fixtures through W2's adapter.
**Acceptance:**

- Command exits 0, emits three ScoreResult-shaped JSON objects validating against W1's schema.
- null fixture `reward` ≤ 0.1.
- golden fixture `reward` ≥ 0.9.
- adversarial-keyword-dump fixture `reward` ≤ 0.5.
  **Deps:** W2 (single consumer, true data dependency — not fan-out). **Size:** one-session (flag
  needs-split if fixture authoring turns out heavier than eb_verify's precedent).
  **Routing:** well-scoped execution, criteria-only — thresholds are given by the epic; this is
  fixture-running against a frozen interface, not new semantics.

### W6 — EB calibration triad — **EXISTING: dr-2vydrm.6**

Already scoped as `eb_verify.calibrate`. Action item: verify it depends on W3 (dr-2vydrm.3), not
on nothing — single-consumer dependency, no fan-out gate needed here.

### W7 — codeprobe calibration triad

**Trace:** same class C rule + A3, for codeprobe; named directly in the sequencing note as
depending on voxa.
**Scope/Acceptance:** same shape as W5, against W4's adapter.
**Deps:** W4 (single consumer). **Size:** one-session.
**Routing:** well-scoped execution, criteria-only.

### W8a — CSB verifier-lint tool

**Trace:** class B bright-line rule (set -euo pipefail, no `grep|head`, shellcheck clean) +
"Verifier-lint clean on CSB (42% of 781 test.sh files currently fail)" + coverage table's ".8 (CSB
shellcheck)".
**Scope:** Build the lint tool only (architecture note: "B/C stay per-benchmark" — B is
independent of the shared lib). Tool vs. sweep split mirrors the formula's own worked example
almost exactly (781 files / ~42% failing here vs. 781/328 there).
**Acceptance:**

- Report's `total_files == 781`.
- A planted fixture missing `set -euo pipefail` is flagged.
- A planted fixture containing `grep|head` piping is flagged.
- A known-clean fixture (has `set -euo pipefail`, no piped grep/head, shellcheck-clean) passes.
  **Deps:** none — no dependency on W1/contract at all. **Size:** one-session.
  **Routing:** well-scoped execution, prescriptive — exact rule set is given by the epic.
  **Gate:** this review is the epic's "checker that passes everything" gate — it must run W8a
  against a corpus known to contain the fail patterns and confirm the report is NOT `0/781`.

### W8b — CSB verifier-lint remediation sweep

**Trace:** same acceptance-bullet as W8a, the remediation half.
**Acceptance:** W8a's report shows 0 failures OR only files in a committed exceptions list where
each entry carries a filed follow-up bead ID (verifier cross-checks each via `bd show`).
**Deps:** W8a (single consumer). **Size:** multi-session, shardable by directory.
**Routing:** mechanical, prescriptive — semantics decided upstream in W8a; review samples ≥10% of
fixed files for behavior preservation, not just re-running shellcheck.

### W9 — EB verifier mechanical-correctness analog

**Trace:** coverage table, class B row: "EB no shell verifiers;..." (text truncated in the epic
snapshot).
**Scope:** epic states the gap exists but never defines the bright-line rule for EB's (non-shell)
verifier runtime. I am not inventing one — see underspecification item 3. Provisional scope:
whatever mechanical-correctness rule EB's verifier owner specifies, applied the same tool-first
way as W8a.
**Acceptance:** **cannot be fully specified from epic text as given** — pin required (see §5,
item 3) before this unit's criteria can be written to the strip-the-scope/stub-test bar.
**Deps:** none (independent of contract, same reasoning as W8a). **Size:** needs-split (unknown
until the rule and current failure rate are known).
**Routing:** well-scoped execution, prescriptive, pending the rule definition.

### W10 — codeprobe agent-agnostic fairness scan

**Trace:** class E rule + A5 + "children added this update: dr-2vydrm.10... (class E)". Verify
not already created before dispatch (see §0).
**Scope:** Scan codeprobe's CLAUDE.md/AGENTS.md/README files for leaked oracle paths/symbols using
W1's class-E detector.
**Acceptance:**

- A planted fixture with an oracle path embedded in a CLAUDE.md returns a class-E finding; a clean
  fixture returns none.
- Scan report's task-directory count equals the codeprobe corpus's own manifest/count.
  **Deps:** W1 REVIEW (fan-out — shared with W2/W3/W4/W11 as consumers of the same detector).
  **Size:** one-session.
  **Routing:** mechanical, prescriptive — the semantic detector lives in W1; this is bulk
  application of it.

### W11 — EB agent-agnostic fairness scan

**Trace:** A5 "new beads .10 / EB analog" + class E rule.
**Scope/Acceptance/Deps/Size/Routing:** mirrors W10 for EB's doc/corpus files.

### W12 — codeprobe verifier-honesty lint (class B analog) — **verify dr-2vydrm.11 not already created**

**Trace:** "children added this update: dr-2vydrm.11: codeprobe verifier-honesty lint (class B
analog)".
**Scope:** same caveat as W9 — the epic names this unit but never spells out codeprobe's
verifier-honesty rule set, nor a current failure-rate baseline (CSB has "42% of 781"; codeprobe
has no equivalent number given).
**Acceptance:** **pin required** (see §5, item 3) before criteria can be written without
inventing scope.
**Deps:** none (independent of contract). **Size:** needs-split.
**Routing:** well-scoped execution, prescriptive, pending the rule definition.

### W13a/b/c — Per-rig corpus QA run + follow-up bead filing (CSB / EB / codeprobe)

**Trace:** original acceptance — "Each rig has run the new QA on its existing corpus and filed
follow-up beads for failures."
**Scope:** Run the full QA pipeline (W1's findings + adapter's ScoreResult) over each rig's
existing corpus; file a follow-up bead for every task with ≥1 finding. Kept as three units, not
one, because each has a different upstream blocker set (rig-specific adapter + rig-specific lint/
fairness units) that a single bead can't express — but they're the same tier and shape, not
narrative copy-paste.
**Acceptance (per rig):**

- QA report exists and its task count matches that rig's own corpus manifest/count command (exact
  command unspecified — see §5, item 7).
- Every task with ≥1 finding has a corresponding follow-up bead, cross-checked via `bd show <id>`
  referenced in the report.
  **Deps:**
- W13a (CSB): W2, W8b (or at minimum W8a), no CSB fairness unit exists (see §5, item 4).
- W13b (EB): W3 (dr-2vydrm.3), W9, W11.
- W13c (codeprobe): W4, W12 (dr-2vydrm.11), W10 (dr-2vydrm.10).
  **Size:** multi-session, shardable by directory/task-batch.
  **Routing:** mechanical, prescriptive — the finding-worthy judgment lives upstream in W1/adapters;
  review samples ≥10% of filed follow-up beads to confirm they're real findings, not lint noise.

### W14 — Cross-rig aggregate/dashboard

**Trace:** A6 — "Cross-rig dashboard / aggregate.json shows reward + scorer_family + sub_scores;
mean-reward aggregates carry scorer_family caveats when mixed."
**Scope:** Aggregation tool reading each rig's ScoreResult outputs into `aggregate.json`; computes
mean-reward per scorer_family grouping with an explicit caveat when scorer_families are mixed.
**Acceptance:**

- `aggregate.json` validates against a schema with top-level `reward`/`scorer_family`/`sub_scores`
  per entry.
- A fixture with two different scorer_family values present produces a non-empty `caveat` field on
  the mixed mean-reward output; a single-scorer_family fixture produces no caveat.
  **Deps:** W2, W3 (dr-2vydrm.3), W4 (fan-in, not fan-out — no special gate beyond depending on each
  adapter's work bead). **Size:** one-session.
  **Routing:** deep reasoning, criteria-only — this is the externally-visible synthesis surface the
  whole contract rewrite was justified by ("high-signal reward for tooling-decision-making"); a wrong
  caveat rule silently misleads every consumer of the dashboard.

### W15 — Public response doc (private draft)

**Trace:** original acceptance — "Public response doc drafted (private repo, not yet published)
summarizing remediation status."
**Scope:** Draft, in the private repo, a status summary against the original VERIFICATION_REPORT's
16%/88%/12% breakdown, referencing what landed for each of the five defect classes.
**Acceptance:**

- File exists at an agreed path (path itself unspecified — see §5, item 8) containing five
  required section headers, one per defect class A–E (grep-checkable).
- No corresponding public-repo commit/PR exists referencing this doc (process check, not just a
  code check — the epic is explicit this stays private/unpublished).
  **Deps:** W5, W6 (dr-2vydrm.6), W7, W8b, W9, W10, W11, W12, W13a/b/c, W14 — final synthesis unit,
  last wave by construction.
  **Size:** one-session.
  **Routing:** deep reasoning, criteria-only — synthesizes across every upstream result into a
  document that eventually feeds sjarmak's public communication; an overstated "fixed" claim here has
  reputational blast radius even before it's published.

---

## 2. Dependency graph and waves

```
EXTERNAL:  codeprobe-voxa (reopened, in flight)
EXISTING:  dr-2vydrm.3 (EB adapter), dr-2vydrm.6 (EB triad), dr-2vydrm.9 (diagnostics plumbing)

Wave 0 (no sibling deps):
  W1  LIB                     [blocked on EXTERNAL voxa]
  W8a CSB lint tool
  W9  EB verifier-analog       (rule pending — see §5.3)
  W12 codeprobe verifier-honesty lint (rule pending — see §5.3)

Wave 1 (deps resolved by wave 0's REVIEW beads / single-consumer work beads):
  W2  CSB adapter              ← W1 REVIEW
  W4  codeprobe adapter        ← W1 REVIEW, EXTERNAL voxa
  W3  EB adapter (EXISTING)    ← W1 REVIEW (verify wiring)
  W10 codeprobe fairness       ← W1 REVIEW
  W11 EB fairness              ← W1 REVIEW
  dr-2vydrm.9 diagnostics      ← W1 (schema authority — see §5.6, verify wiring)
  W8b CSB lint sweep           ← W8a

Wave 2 (single-consumer deps on wave 1):
  W5  CSB triad                ← W2
  W7  codeprobe triad          ← W4
  W6  EB triad (EXISTING)      ← W3 (verify wiring)
  W13a CSB corpus run          ← W2, W8b
  W13b EB corpus run           ← W3, W9, W11
  W13c codeprobe corpus run    ← W4, W12, W10
  W14 aggregate/dashboard      ← W2, W3, W4

Wave 3:
  W15 response doc             ← W5, W6, W7, W8b, W9, W10, W11, W12, W13a/b/c, W14
```

**Honesty check:** this is not a size-1 lockstep chain — wave 0 has 4 independent units, wave 1 has
7, wave 2 has 7, wave 3 has 1. That shape differs from the epic's own narrated sequencing
("voxa → lib → adapters → triad → then lint + fairness in parallel") in one concrete way worth
flagging: **lint (W8a/W8b) and the class-B analogs (W9/W12) have zero data dependency on the
ScoreResult contract** — the architecture note says B "stays per-benchmark," touching verifier
runtime, not the shared schema — so treating "then lint... in parallel" as sequential-after-triad
would waste two full waves for no reason. **Fairness (W10/W11), by contrast, genuinely depends on
W1** (class E logic is housed in the lib), so it can't be wave 0 even though the epic's phrase
bundles it with lint as if the two had the same dependency depth. Both lint and fairness land
earlier than the epic's literal phrasing implies, but not at the same depth as each other.

**Critical path:** codeprobe-voxa (external) → W1 (LIB) → W1 REVIEW → W4 (codeprobe adapter) → W7 or
W13c → W15 (response doc). Roughly 5 hops; the CSB path (voxa → W1 → W2 → W5/W13a → W15) is the
same length.

---

## 3. Routing summary

| Tier                                 | Units                                                    | Reason                                                                                                                            |
| ------------------------------------ | -------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Deep reasoning (criteria-only)       | W1 (LIB), W14 (aggregate), W15 (response doc)            | shared contract/schema, externally-visible synthesis, or reputational-blast-radius synthesis — errors propagate to every consumer |
| Well-scoped execution                | W2, W4, W3(existing), W5, W7, W6(existing), W8a, W9, W12 | implementation against a frozen interface (W1's schema, or the epic's own bright-line rules); code judgment, not architecture     |
| Mechanical (prescriptive, shardable) | W8b, W10, W11, W13a/b/c                                  | bulk application of a rule decided upstream (W8a's lint rule or W1's class-E detector)                                            |

3 of 16 new units (~19%) sit at the top tier — that's close to the "fewer than 1-in-5 below the top
tier" trigger from the formula, but this epic is genuinely schema/contract-heavy (one shared lib,
one cross-rig dashboard, one synthesis doc), so I'm not flagging it as over-budgeted; it would be
worth re-examining only if a fourth unit got pushed to deep-reasoning without a comparable
blast-radius justification.

Reviews and meta-reviews for every unit above are always prescriptive (per the formula's fixed
rule) and aren't counted in this table.

---

## 4. Gate placement

- **W1's REVIEW is the epic's primary fan-out poison point.** It gates W2, W3(existing), W4, W10,
  W11, and dr-2vydrm.9 — six consumers of one schema/detector set. The review must specifically
  test: does the class-A detector fire on a genuinely dangling oracle path? Does class-E fire on a
  genuinely leaked path? If either silently passes, every downstream rig reports a clean corpus
  that isn't, and that false-clean status is exactly what W15's response doc would then repeat
  publicly-adjacent.
- **W8a's review is the "checker that passes everything" gate** required for any QA/checker epic:
  it must run the lint tool against a corpus known to contain violations and confirm the report
  isn't a vacuous `0/781`.
- **Cheap-routed bulk gates:** W8b's review samples ≥10% of the ~328 remediated files for behavior
  preservation (not just re-running shellcheck — actual script behavior). W13a/b/c's reviews sample
  ≥10% of filed follow-up beads to confirm they're genuine findings, not lint/detector noise
  amplified across a large corpus.
- **W15's review** should re-verify at least 2 of the five defect-class status claims directly
  against upstream bead state (not just read the draft) — the standard verifier-role clamp, applied
  here because a synthesis document is exactly where an inaccurate but plausible-sounding claim
  survives a read-only review.

---

## 5. Underspecified — pinned assumptions

1. **Bead-existence discrepancy.** The notes reference .1–.11 as a settled plan; the live children
   list shows only .3/.6/.9 exist. **Assumption:** treat only those three as real; `bd show` each
   other referenced ID before creating a duplicate. **Pinned by:** whoever dispatches wave 0/1.

2. **EB class-A gap** ("EB has no upstream zat[9]-equivalent…", truncated in the epic snapshot).
   Unclear whether EB needs a new oracle-curation/mining tool analogous to codeprobe-zat9, or
   whether the existing `.3` adapter's oracle data is sufficient for class-A checking as-is.
   **Assumption:** no new curator bead this epic; W3's existing oracle data suffices. **Pinned
   by:** must be resolved before W3's REVIEW closes — if wrong, class-A coverage for EB is void
   even though W3 "passes."

3. **EB and codeprobe class-B "analog" rule sets** (W9, W12). The epic names both units but never
   states the bright-line rule for a non-shell verifier runtime, nor a current failure-rate
   baseline (CSB has "42% of 781"; the others have none given). **Assumption:** none — I've left
   both units' acceptance criteria explicitly unwritten rather than invent a rule not in the epic.
   **Pinned by:** the verifier-runtime owner for each rig, before W9/W12 dispatch.

4. **CSB fairness scan (class E).** A5's literal text names only codeprobe (.10) and "EB analog,"
   not CSB, even though class E is general and originated from the CSB audit that started this
   epic. **Assumption:** no CSB fairness bead this wave — deliberately following A5's literal
   scope rather than gold-plating. **Pinned by:** before W15 dispatches, since the response doc's
   remediation claim should state explicitly whether CSB fairness was checked or knowingly
   deferred, not go silent on it.

5. **Class D gap** ("needs scorer_family coverage…", truncated). **Assumption:** resolved by
   requiring W2/W3/W4 each emit a real, rig-specific `scorer_family` value (folded into their
   acceptance criteria above) rather than a separate scorer_family-registry bead. **Pinned by:**
   before W2/W4 dispatch — if the intended gap is a distinct registry unit, one is missing from
   this breakdown.

6. **Diagnostics plumbing (dr-2vydrm.9) vs. LIB (W1) schema ordering.** Both were introduced in the
   same notes update; unclear which is upstream. **Assumption:** W1 is schema authority (A2 names
   it explicitly); .9 depends on W1. **Pinned by:** checking .9's actual current dependency links
   before wave 1 — if .9 was created before W1 existed, that link is probably missing today.

7. **Corpus sizes per rig** for W13a/b/c's acceptance criteria. The epic gives 781 shell files for
   CSB's lint and "25 CSB tasks" for the original audited sample, but never states the full
   task-corpus size for any rig. **Assumption:** acceptance uses "matches the rig's own corpus
   manifest/count command" as a placeholder. **Pinned by:** before W13a/b/c dispatch — otherwise
   their pass/fail can't be verified against a real number.

8. **Response doc (W15) location and format.** "Private repo, not yet published" — which repo,
   which path. **Assumption:** sjarmak/CodeScaleBench (the repo the original VERIFICATION_REPORT
   concerns), path `docs/VERIFICATION_REPORT_RESPONSE.md`. **Pinned by:** before W15 dispatch.

9. **"Then lint + fairness in parallel" sequencing softness.** Treated as narrative/priority order,
   not a real dependency, for lint (zero contract dependency → wave 0). Fairness, unlike lint, does
   have a genuine dependency via W1's class-E detector, so it lands at wave 1, not wave 0 — flagged
   so the schedule consequence (fairness one wave later than lint, several waves earlier than the
   epic's literal last-step phrasing) is visible to the dispatcher.

10. **Where irrecoverable-task follow-ups get filed.** The epic's own out-of-scope line says
    re-mining/re-authoring irrecoverable tasks should be "file[d] separately as needed" but doesn't
    say whether "separately" means a new epic or a sibling bead under dr-2vydrm. **Assumption:**
    sibling beads under dr-2vydrm, tagged to distinguish from in-epic remediation beads. **Pinned
    by:** before W13a/b/c's acceptance criteria (which require each follow-up bead ID be
    cross-checkable) can specify the expected parent.

---

## Out of scope (epic-stated, not units)

- Public PRs/pushes to sourcegraph/CodeScaleBench — sjarmak owns, no bead created.
- Re-mining/re-authoring tasks discovered irrecoverable during W13a/b/c — filed as follow-up beads
  per those units' acceptance criteria, not remediated within this epic (see underspec item 10 for
  where exactly).
- A CSB-specific fairness-scan bead — literal reading of A5 excludes it; see underspec item 4.
