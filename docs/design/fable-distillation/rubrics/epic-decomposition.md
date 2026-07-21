# Judge Rubric — Epic Decomposition

**Task type:** Breaking an epic into dispatchable work items ("beads") for independent
mid-tier agents, with testable acceptance criteria, a dependency graph with parallel
waves, model-tier routing, review-gate placement, and explicit underspecification
handling.

**Authored:** 2026-07-06, by Claude Fable 5, calibrated against the 2026-07-07 golden
run (`docs/evals/fable-baselines/outputs/task-05/fable.md`, epic dr-2vydrm). This
rubric is generic to the task type; dr-2vydrm facts appear only inside quoted anchor
examples. Judges (Opus 4.8 panels) apply it to any epic-decomposition output, scoring
the candidate against the frozen epic input it was given.

**Prime directive for judges:** score only what is in the candidate's text, checked
against the epic input. Every score must cite the candidate's own words. A confident,
well-formatted output that fails these checks is a low score; formatting is not a
dimension.

---

## Dimensions

Weights in parentheses. Score each dimension 1–5 (integers; 2 and 4 are "between the
anchors"). The 5/3/1 anchors describe observable signatures, not vibes.

### D1 — Decomposition structure (25%)

Granularity, ordering, and dependency correctness. Does the breakdown reflect how the
work actually flows, and is real parallelism extracted?

- **5:** Bead boundaries follow reasons-to-dispatch (one worker, one verifiable
  outcome). Tool-building is split from bulk application of the tool (a linter bead
  vs. a remediation-sweep bead). The dependency graph encodes true _data_
  dependencies, not narrative order; independent tracks start in wave 0 rather than
  queuing behind the critical path; the critical path is named. Waves are consistent
  with the stated per-bead dependencies (a judge can re-derive the waves from the
  deps and get the same answer). Large mechanical work is marked shardable.
- **3:** Beads are individually sensible but some conflate two dispatches (build the
  tool AND run the sweep in one bead) or are too coarse to hand to an independent
  agent. Waves exist and are mostly derivable from the deps, but at least one
  ordering is stricter than the data requires, or one wave assignment contradicts a
  stated dependency. Some available parallelism is left on the table without comment.
- **1:** The "graph" is a numbered list with each item depending on the previous one
  (a linear chain), or a flat list with no dependencies at all where the epic clearly
  has ordering constraints. Waves absent, or waves that contradict the per-bead
  dependency statements. Bead scopes overlap so two workers would collide on the same
  files.

### D2 — Acceptance-criterion testability (25%)

The task demands criteria a verification agent can TEST: a command or observable,
never "looks correct."

- **5:** Every bead's criterion names a concrete command, exit-code assertion, file
  check, or numeric threshold, AND the check discriminates: it would fail on a
  plausible wrong implementation, not just on a missing one. Negative fixtures appear
  (a planted-defect input must be flagged; a clean input must pass). Escape hatches
  are themselves verifiable (an exceptions list where each entry must reference a
  filed follow-up item the verifier can look up). Where a criterion cannot be fully
  mechanical, the residual judgment is named explicitly rather than hidden.
- **3:** Most criteria are commands or observables, but several are vacuous-testable:
  "the file exists," "tests pass" with no statement of what the tests must assert,
  "the report is generated." Form is mechanical; content wouldn't catch a wrong
  result. No negative/planted-defect checks.
- **1:** Criteria restate the scope in the passive voice ("the adapter is
  implemented," "calibration is complete," "works correctly"), or defer to human
  judgment ("reviewed and approved," "looks correct"). A verification agent given
  only the criterion could not produce a pass/fail.

### D3 — Traceability and scope fidelity (15%)

Every bead maps to something the epic actually requires; nothing required is dropped.

- **5:** Each bead is traceable to specific epic language (the output cites the
  clause, acceptance item, or child-listing entry it implements). Explicit
  out-of-scope boundaries in the epic are honored and restated (deferred work is
  filed, not silently done or silently dropped). No invented infrastructure,
  dashboards, CI systems, refactors, or "hardening" the epic never asked for. A judge
  cross-checking epic requirements against beads finds full coverage in both
  directions.
- **3:** Coverage is mostly complete but one epic requirement has no bead, or one to
  two beads are plausible-but-unrequested additions justified only by "best
  practice." Out-of-scope rules acknowledged but a bead quietly crosses one.
- **1:** Multiple beads with no anchor in the epic text (gold-plating: observability
  stacks, migration frameworks, documentation suites the epic never mentions), or
  whole epic acceptance items missing from the breakdown. The decomposition is of an
  imagined project, not the given one.

### D4 — Routing calibration (10%)

Model-tier assignment per bead, with the reason.

- **5:** Tiering follows where the judgment lives: top tier reserved for items whose
  errors propagate (contract/schema definition, semantic detection logic,
  externally-facing synthesis); mid tier for well-scoped execution against a frozen
  interface; cheap tier for mechanical application where the semantics were decided
  upstream. Every assignment carries a reason grounded in blast radius or judgment
  content, not bead size. Cheap-tier use is real (bulk mechanical work is actually
  routed cheap), and its known risk is mitigated by a gate rather than by promoting
  the tier.
- **3:** Tiers assigned with reasons, but the reasons are generic ("complex" /
  "simple") rather than about error propagation, or the cheap tier is nearly unused
  despite obvious mechanical work, or one high-blast-radius item sits at mid tier
  without justification.
- **1:** Everything routed top-tier "to be safe," or tiering by task length rather
  than judgment content, or no reasons at all. Routing that ignores the stated
  premise that mid-tier workers execute the items.

### D5 — Gate placement (15%)

Review gates positioned so a wrong early result cannot silently poison later waves.

- **5:** Gates sit at fan-out points (before a contract or shared library is imported
  by many consumers) and before results that downstream decisions consume, and each
  gate names the specific failure it intercepts and what gets poisoned without it.
  Gate cost is calibrated: mechanical/automated conformance checks where automation
  suffices, human or top-tier review only where the failure is semantic
  (false-negative checkers, weak adversarial fixtures, behavior-changing bulk edits —
  each with a sampling or cross-check mechanism stated). At least one gate targets
  the "checker that passes everything" failure mode, since a QA epic's worst outcome
  is a green report on a defective corpus.
- **3:** Gates exist at roughly the right seams but are generic ("code review after
  each phase") without naming the failure intercepted, or every gate is a human
  review with no automated tier, or the highest-leverage poison point (the shared
  contract, the fixture quality) has the same weight as routine ones.
- **1:** No gates; or a single "final review" at the end, after every wave has
  already consumed potentially-poisoned intermediate results; or gates listed with no
  connection to the dependency structure.

### D6 — Underspecification handling (10%)

Surfacing what must be asked before dispatch, with a provisional assumption per item.

- **5:** The gaps identified are load-bearing: each one, if resolved the other way,
  would change beads, dependencies, or acceptance criteria — and the output says
  which. Every question carries a provisional assumption specific enough to dispatch
  under, and assumptions that must be pinned by a certain gate or wave say so.
  Ambiguities in the epic's own structure (conflicting IDs, sequencing language that
  may be priority rather than dependency, undefined deliverable shapes) are caught,
  not paved over.
- **3:** A real list of questions with assumptions, but padded with trivia (questions
  whose answer changes nothing) or missing one gap the plan visibly depends on
  (readable in the output: a bead's scope quietly resolves an ambiguity that never
  appears in the underspecification section).
- **1:** Section absent or perfunctory ("requirements seem clear"); or ambiguities
  silently resolved inline — the plan commits to one reading of ambiguous epic
  language with no flag, no assumption, no question. Silent resolution scores worse
  than a short honest list.

---

## Failure signatures (weaker-model tells, and how to detect each)

Judges should actively hunt for these; each maps to a dimension it caps.

1. **Restated-scope acceptance criteria** (caps D2 at 2). The criterion is the scope
   paragraph in different words. _Detection:_ delete the bead's scope paragraph and
   read only the criterion — if it contains no command, path, threshold, exit code,
   or observable a machine could evaluate, it is restated scope. Phrases: "is
   implemented correctly," "works as expected," "has been integrated."

2. **Vacuous-testable criteria** (caps D2 at 3). Mechanically checkable but unable to
   catch a wrong result: "file exists," "script exits 0" for a script the same bead
   authors, "tests pass" with unspecified tests. _Detection:_ ask "would this check
   pass on a stub implementation?" If yes, it is vacuous.

3. **Linear chain dressed as a graph** (caps D1 at 2). Every bead depends on exactly
   the previous bead; "waves" of size one. _Detection:_ count beads with more than
   one dependent and beads with zero dependencies beyond wave 0. A real epic almost
   always has independent tracks; a chain means the model narrated its own writing
   order as a dependency structure. The inverse tell — no dependencies at all where
   the epic states sequencing — is the same failure.

4. **Waves inconsistent with the stated deps** (caps D1 at 3). _Detection:_ re-derive
   waves from the per-bead dependency lists. Any bead scheduled in a wave at or
   before one of its dependencies is an inconsistency; more than one is structural.

5. **Uniform top-tier routing** (caps D4 at 2). All or nearly all beads routed to the
   top tier, reasons like "important" or "to ensure quality." _Detection:_ tier
   distribution plus reason text. If under ~20% of beads are mid/cheap on an epic
   containing bulk mechanical work, the model is spending the caller's budget to buy
   itself safety margin.

6. **Invented beads / gold-plating** (caps D3 at 2). Beads for CI setup, monitoring,
   documentation passes, refactors, or "phase 2 improvements" with no anchor in the
   epic. _Detection:_ for each bead, demand the epic clause it traces to; the
   golden-level behavior is that the output itself provides this trace. A bead whose
   justification appeals to general best practice rather than epic text is invented.

7. **Silent ambiguity resolution** (caps D6 at 2). The epic is ambiguous (conflicting
   numbering, unclear deliverable shape, sequencing that may be priority) and the
   plan just picks a reading inline. _Detection:_ cross-check the underspecification
   section against the bead scopes — every interpretive commitment made in a scope
   should either be epic-literal or appear as a flagged assumption. Interpretive
   commitments with no flag are silent resolutions.

8. **Gate theater** (caps D5 at 3). Gates that exist to have gates: "review after
   each step," all-human, none naming what wrong result they intercept or which
   downstream consumers they protect. _Detection:_ for each gate, look for (a) the
   named failure mode and (b) the named downstream blast radius. Gates missing both
   are theater. Also check the end-loaded variant: a single terminal review cannot
   prevent poisoning by construction.

9. **Missing negative verification** (caps D2 at 4). All checks are positive-path;
   nothing asserts that a planted defect is caught. For QA/checker-building epics
   this is the difference between verifying the tool runs and verifying it works.
   _Detection:_ search the criteria for any planted-bad-fixture or known-defective
   input assertion.

10. **Size-class monoculture** (minor; deduct within D1). Every bead "one-session"
    with no shardability or needs-split judgment. _Detection:_ size column has one
    value across a heterogeneous epic.

---

## Anchor examples (5-level behavior, from the 2026-07-07 golden run)

1. > "`.8a` lint report shows 0 failures, OR shows only files enumerated in a
   > committed exceptions list where each entry carries a filed bead ID (verifier
   > cross-checks each listed ID exists via `bd show`)."

   **Why 5:** the escape hatch (some files are irrecoverable) is itself made
   machine-verifiable instead of becoming a judgment call that swallows failures —
   D2 at its ceiling.

2. > "a weak adversarial fixture yields a passing calibration that blesses gameable
   > verifiers — the most damaging silent poisoning available in this epic, because
   > triad results gate sjarmak's upstream push."

   **Why 5:** the gate names the exact wrong result it intercepts and traces the
   blast radius to the downstream decision that would consume it; gate cost (top-tier
   review of fixtures specifically) is proportional to that radius — D5.

3. > "No data dependency forces it. _Assumption:_ it expresses priority (voxa/lib are
   > the critical path), not a dependency; the wave plan starts lint tooling in
   > wave 0. If sjarmak intended a hard order, waves 0–1 lose .8a/.8b/.11 and the
   > schedule stretches."

   **Why 5:** distinguishes the epic's narrative sequencing from true data
   dependency, flags the interpretation as an assumption, and states the concrete
   schedule consequence of the other reading — D1 and D6 working together.

4. > "**Routing:** cheap-tier — mechanical invocation of lib logic; the semantic
   > judgment lives in the lib."

   **Why 5:** tier assignment reasoned by where the judgment lives, not by bead
   size; the cheap tier is actually used because the semantics were decided upstream
   — D4.

5. > "cheap-tier for the mechanical fixes, with the G5 sampling gate below — bulk
   > shell edits are exactly where a cheap model silently changes verifier
   > semantics."

   **Why 5:** routes cheap where cheap is correct, names the specific risk that
   creates, and mitigates with a targeted sampling gate instead of promoting the
   tier — D4 and D5 coupled, which is the shape of real calibration.

---

## Scoring procedure

1. **Read the epic input first**, then the candidate output. D3 and D6 cannot be
   scored from the candidate alone.
2. **Score each dimension 1–5.** For every dimension score, quote at least one
   passage of the candidate's own text as evidence (two quotes when scoring 1 or 5 —
   the extremes need the strongest support). A score with no citation is invalid and
   the panel must not accept it.
3. **Apply failure-signature caps.** If a signature is detected, the corresponding
   dimension score may not exceed its cap regardless of other evidence; record which
   signature fired and the detecting quote.
4. **Compute the weighted overall:**
   `0.25·D1 + 0.25·D2 + 0.15·D3 + 0.10·D4 + 0.15·D5 + 0.10·D6`
   Verdict bands: **≥4.5 golden-equivalent** · **3.5–4.4 dispatchable with edits**
   (name the edits) · **2.5–3.4 needs rework** (usable skeleton, criteria/gates must
   be rewritten) · **<2.5 reject** (do not dispatch from this plan).
5. **Judge confidence:** record `high | medium | low` with one line of reason. Use
   `low` when the epic input was itself too thin to check traceability, or when two
   dimensions landed on a cap by a borderline detection; `medium` when one did.
   Panels should discount low-confidence scores when aggregating.
6. **Report format:** per-dimension table (score, cap-if-any, citation), fired
   failure signatures with quotes, weighted overall, verdict band, confidence line.
