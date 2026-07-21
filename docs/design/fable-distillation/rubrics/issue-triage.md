# Judge Rubric — Issue Triage Ranking

**Task type:** classifying a repository's open issues by contributor-actionability (GRAB NOW / GOOD CANDIDATE / INVESTIGATE / SKIP) and ranking the top picks to start today, from a frozen issue snapshot with no live tools.

**Authored:** 2026-07-06, by Claude Fable 5. Calibrated against the 2026-07-07 golden run on the gastownhall/gascity 40-issue snapshot. All snapshot-specific facts in this document appear only inside quoted anchor examples; the rubric itself applies to any repo and any snapshot.

**Judge instructions:** Score each dimension 1–5 (anchors given at 5/3/1; use 2 and 4 for in-between performance). Every score must cite at least one verbatim quote from the _candidate's_ output as evidence. Then compute the weighted verdict and state your judge confidence. Do not reward length; this task explicitly punishes padding.

---

## Dimension 1 — Coverage and accounting (weight 10%)

Did the candidate classify every issue in the snapshot exactly once, with the buckets reconciling to the snapshot total?

- **5:** Every issue number in the snapshot appears in exactly one bucket. Bucket counts are stated and sum to the snapshot size. Structural features of the snapshot are noticed and used — e.g. a batch of issues filed by one reporter on one day is identified as a family and triaged with a shared caveat rather than 18 independent verdicts. Related issues are cross-linked (parent/child, duplicate-of, same-subsystem).
- **3:** All or nearly all issues classified once; one or two silently dropped or listed twice. Counts present but not reconciled, or families handled issue-by-issue with no recognition of the shared provenance.
- **1:** Multiple issues missing with no acknowledgment, or the same issue in two buckets with contradictory verdicts, or the output triages only a subset ("the most interesting issues") when the prompt said every issue.

**Judge detection:** count issue numbers in the candidate output against the snapshot; a mechanical diff. Double-classification and dropped issues are hard failures on this dimension regardless of prose quality.

**Anchor (5-level):** the golden output opens with a snapshot-structure observation before any verdict: _"issues #3960–#3977 are one downstream operator's 18-item findings bundle filed the same morning. They are well-evidenced but written against gc 1.3.2/1.3.3 … Anything from that batch needs a 'does this still reproduce on main?' check as step zero, even the GRAB NOW picks."_ — Why: it converts a coverage-level pattern (one reporter, one morning, stale versions) into a triage rule applied across a third of the snapshot, instead of pretending the 18 issues are independent evidence.

---

## Dimension 2 — Actionability-grounded prioritization (weight 25%)

Is the ranking driven by _contributor-actionability_ — mechanism already pinned, small blast radius, mergeable without maintainer design decisions — rather than by surface severity, label priority, or how alarming the title sounds?

- **5:** GRAB NOW entries each name the reason the work is bounded: root cause located (file/function cited from the issue body), fix shape stated in one clause, and any residual judgment call named. The top-ranked picks are the ones where "the issue quality does most of the investigation" — high-severity issues that need maintainer design decisions are explicitly ranked _below_ smaller pinned bugs, with that trade stated. The ranking has an articulated rationale, not just an ordered list.
- **3:** Classification is broadly sensible but the ranking logic is implicit; some GRAB NOW entries justify themselves by importance ("this is a P1, users are affected") rather than by boundedness. High-severity/high-design issues drift toward the top without the design-risk being weighed.
- **1:** Rank order tracks severity labels or emotional weight of the titles. Issues requiring architecture decisions, cross-repo work, or maintainer-owned subsystems appear in GRAB NOW. No entry says _why this is startable today_ in terms a contributor could act on.

**Judge detection:** for each top-ranked pick, ask "what sentence makes this startable?" If the justification is impact vocabulary (critical, severe, blocks users) with no mechanism/scope content, the candidate is severity-ranking. Also check whether any large multi-incident or design-fork issue landed in GRAB NOW — that is the signature miss.

**Anchor (5-level):** the golden ordering rationale: _"1–3 are confirmed bugs with the mechanism already pinned to code, where the issue quality does most of the investigation for me; 4 is triaged and precise but carries one semantic decision; 5 is the smallest and safest but also the lowest value, included because it is a guaranteed same-day merge-quality PR."_ — Why: every rank position is justified by actionability gradient (mechanism-pinned > one-decision > small-and-safe), and the low-value pick is included with its trade-off stated rather than dressed up.

**Anchor (5-level):** a design-fork issue kept out of GRAB NOW despite high value: _"High value (this exact mismatch is a known operational blocker in our own city), but the fix is a design fork — widen the worker claim query vs convoy-wrap at dispatch — touching core routing … Needs maintainer direction on which side to fix."_ — Why: personal pain and value do not override the structural fact that the fix shape is undecided; the entry names the fork explicitly instead of picking a side and calling it grab-able.

---

## Dimension 3 — Restraint and category discipline (weight 15%)

Are SKIP and INVESTIGATE real, populated buckets with specific reasons, or did the candidate inflate everything into the two positive buckets?

- **5:** SKIP entries give the _category_ of skip (maintainer-owned process/infra, duplicate, umbrella with children already split out, explicitly deferred by maintainer, wontfix-shaped) and, where applicable, redirect to the durable alternative. GOOD CANDIDATE entries each name their specific risk or missing scope decision — no entry is GOOD CANDIDATE merely because the model didn't want to commit. Issues that are trivially fixable but institutionally not contributor-shaped (release process, packaging, maintainer-deferred) are SKIPped despite being easy.
- **3:** Buckets are populated but some SKIP reasons are generic ("low priority", "old"), or a few GOOD CANDIDATE entries carry no named risk and read as hedged GRAB NOWs.
- **1:** SKIP and/or INVESTIGATE are empty or near-empty on a snapshot of meaningful size; nearly everything is GRAB NOW or GOOD CANDIDATE; "easy" is conflated with "appropriate for a contributor".

**Judge detection:** count the bucket distribution first — a 35/5/0/0 split on a 40-issue snapshot is a padding signature before reading a word of the prose. Then spot-check SKIP reasons: do they name _who owns it_ or _what it duplicates_, or just adjectives?

**Anchor (5-level):** _"The fix is a version pin in the Homebrew tap formula — maintainer-owned packaging/release infra, one line, not contributor-shaped."_ — Why: the issue is explicitly trivial ("one line") and is still SKIPped, because the deciding axis is ownership, not difficulty; the entry then redirects to the contributor-shaped sibling improvement.

---

## Dimension 4 — Calibration and uncertainty accounting (weight 20%)

Do stated confidences carry information, and are the _systematic_ limits of the snapshot acknowledged and propagated into the verdicts?

- **5:** Confidence varies with evidence quality (not uniform), is stated per classification as the prompt demands, and is sometimes _split_ — confidence in the classification distinguished from confidence in the fix scope. The snapshot's structural blind spots (no PR/assignee data, no repro against current main, truncated bodies) are named once and then visibly discount specific verdicts. Stale self-reports are treated as claims to re-verify, not ground truth — issues filed against old versions, or issues that self-report partial fixes, get an explicit re-verification step or an INVESTIGATE.
- **3:** Confidences present but clustered (everything "medium"/"high") so they carry little signal; snapshot limits mentioned in passing but no verdict actually changes because of them; stale versions noticed for some issues but not applied consistently.
- **1:** No per-classification confidence despite the prompt requiring it, or uniform "high" everywhere, or reporter claims from months-old versions restated as current fact ("this crashes on startup") with no re-verification demand. Confident assertion of facts the snapshot cannot contain (open PRs, assignee state, current main behavior).

**Judge detection:** histogram the confidence labels — a flat distribution is the calibration-collapse tell. Then check whether any sentence in the output admits what the snapshot _cannot show_, and whether that admission is attached to concrete verdicts or is a disclaimer paragraph that touches nothing.

**Anchor (5-level):** _"The snapshot has no PR/assignee data, so 'collision with maintainer work' is inferred from labels and issue age only — that is the main systematic uncertainty in every confidence figure here."_ — Why: the single largest epistemic limit of the input format is identified up front and explicitly wired into every confidence figure, rather than confidences being presented as if derived from complete information.

**Anchor (5-level, split confidence):** _"Confidence: medium on the classification, low on any single fix scope without that confirmation."_ — Why: classification-confidence and scope-confidence are different quantities, and separating them is exactly what makes a confidence label usable by the reader.

---

## Dimension 5 — Collision-awareness (weight 10%)

Does the candidate reason about colliding with maintainer work and with its _own other picks_?

- **5:** Fresh high-priority issues are flagged as likely to have in-flight maintainer fixes, with "check open PRs first" as a stated precondition. Picks that touch the same code region are cross-referenced with a coordination note. Maintainer-active families (release engineering, versioning policy, core invariants under active decomposition) are identified as elevated collision risk even when the individual fix looks easy. Ideally the output names the collision sweep as the true first action for the whole list.
- **3:** Collision mentioned generically ("might conflict with maintainer work") on one or two issues, but no mechanism (labels, age, priority, family) for _why_ those issues specifically; no awareness that two of its own picks overlap.
- **1:** No collision reasoning anywhere; a days-old P1 with heavy maintainer attention is treated as free for the taking; two top-5 picks that modify the same subsystem carry no coordination note.

**Judge detection:** look for any sentence conditioning a pick on maintainer state. Then check the top picks pairwise: if two touch the same command/file family (per the candidate's own descriptions) and no note connects them, the candidate is not modeling its own blast-radius overlap.

**Anchor (5-level):** _"(Same code neighborhood as #3944 — coordinate if doing both.)"_ and, closing the whole ranking: _"everything here assumes no in-flight maintainer PRs, which the snapshot cannot show — the real first action on each pick is a duplicate/PR sweep."_ — Why: collision is modeled at both scales — pick-vs-pick overlap and list-vs-maintainer — and the second quote demotes the entire ranking to conditional status rather than presenting it as final.

---

## Dimension 6 — Evidence-demands for INVESTIGATE (weight 10%)

Are INVESTIGATE calls precise instruments rather than deferrals?

- **5:** Each INVESTIGATE names (a) the exact missing evidence, (b) the concrete action that would produce it (re-run X against current main; obtain the full error text; determine which repo owns the write path), and (c) how the answer resolves the issue into another bucket (including "if it's the other repo's, re-file there / SKIP"). Competing hypotheses are enumerated where the evidence would discriminate between them.
- **3:** INVESTIGATE entries name a general direction ("needs a repro", "needs more detail from the reporter") but not the discriminating evidence or the resolution paths.
- **1:** "Needs more information" with nothing specific, or INVESTIGATE used as a dumping ground for issues the model didn't read closely — detectable when the stated question is already answered in the issue body the candidate itself summarized.

**Judge detection:** for each INVESTIGATE, ask "could a junior contributor execute the evidence-gathering step tomorrow from this text alone?" If not, it is a deferral. Also check for the reverse failure: issues whose own bodies invalidate part of their content should land here or carry the re-check, not sit in GRAB NOW at high confidence.

**Anchor (5-level):** _"The snapshot shows the failing query but not the underlying error ('error on line 3' — of what kind?). Evidence needed: the full Dolt error text and whether it reproduces on current dolt/bd versions … Could be a Dolt engine limitation (giant `NOT IN`), a NULL-semantics bug in the SQL, or already moot."_ — Why: it pinpoints the exact missing artifact, notes the reporter's stale versions, and pre-enumerates three hypotheses the evidence would discriminate between — the INVESTIGATE is a designed experiment, not a shrug.

---

## Dimension 7 — Execution-plan quality (top picks) (weight 10%)

For each ranked pick, are the four required elements (first step, blast radius, proving test, disconfirmer) concrete enough to act on?

- **5:** First step names a specific artifact to read or locate (a function, a file, a diff between two code paths), not "understand the issue". Blast radius names the subsystems touched _and_ the ones deliberately untouched. The proving test is stated at assertion level — what fixture, what action, what asserted outcome, including the regression direction — not "add unit tests". The disconfirmer ("what could make this pick wrong") is a genuine alternative explanation of the observed behavior (the behavior might be intentional; the correct fix might live on the other side of the boundary), not a generic risk ("might be more complex than expected").
- **3:** Elements present for every pick but some are template-grade: first steps like "reproduce the bug", tests like "verify the fix works", disconfirmers that restate schedule risk instead of naming an alternative hypothesis.
- **1:** Elements missing for some picks, or uniformly generic across all picks — the same four sentences with the issue number swapped.

**Judge detection:** the disconfirmer is the highest-signal element. Grade it first: a real disconfirmer proposes a different _causal story_ under which the planned fix is wrong. Then check tests for assertion-level content (does the test description contain an expected observable outcome?).

**Anchor (5-level):** _"What could make this wrong: the zero-lastRun skip may be a deliberate fire-on-install guard, in which case the correct fix is stamping lastRun at install instead — the same test pins either implementation."_ — Why: the disconfirmer is an alternative design intent, it comes with the correct fix under that alternative, and the proving test is chosen to be valid under both hypotheses — the plan survives its own disconfirmation.

---

## Failure signatures (weaker-model patterns and how to detect them)

1. **Confident misclassification.** "Confidence: high" on issues whose own summaries (in the candidate's text) contain no located mechanism, or on issues from stale versions with no re-verification demand. _Detect:_ for each "high", find the supporting clause; if it is impact language rather than mechanism language, flag it. The task prompt states a wrong confident classification is worse than an INVESTIGATE — penalize under Dimensions 2 and 4 simultaneously.
2. **Severity-ranking.** The top-5 tracks priority labels or scariest titles; large design-decision issues rank above small pinned bugs. _Detect:_ justifications built from "critical/P1/blocks users" with no first-step concreteness; a design-fork or multi-incident umbrella issue inside GRAB NOW.
3. **Stale self-reports as ground truth.** Reporter claims filed against old releases restated as facts about current main; issues whose bodies self-report partial fixes still classified at high confidence without a re-check step. _Detect:_ candidate never mentions version drift, never demands "reproduce on main", or classifies a self-invalidating issue without quoting the invalidation.
4. **Bucket padding.** Nearly every issue lands in GRAB NOW or GOOD CANDIDATE; GOOD CANDIDATE entries carry no named risk. _Detect:_ bucket histogram first, then scan GOOD CANDIDATE for entries missing a "risk:"/"needs" clause — those are hedged GRAB NOWs, i.e. the model avoiding a decision while appearing decisive.
5. **INVESTIGATE as shrug.** "Needs more info / needs reproduction" with no named artifact, action, or resolution path. _Detect:_ apply the junior-contributor test from Dimension 6.
6. **Template execution plans.** The four per-pick elements are grammatically present but interchangeable across picks. _Detect:_ swap-test — if the disconfirmer of pick 2 reads correctly under pick 4, it is template output.
7. **Snapshot overreach (hallucination).** Claims about open PRs, assignees, code internals, or current-main behavior that a frozen issue snapshot cannot contain, presented as fact rather than inference. _Detect:_ any unhedged claim about repository state outside the issue bodies; correct behavior is to mark such claims as inference ("inferred from labels and issue age only") or as a to-verify step.
8. **Calibration collapse.** All confidences identical (whether all-high or all-medium), so the labels are decoration. _Detect:_ histogram; also check for the absence of any split between classification-confidence and fix-scope-confidence on the genuinely mixed cases.
9. **Coverage leak.** Issues silently omitted, double-bucketed, or counts that don't reconcile. _Detect:_ mechanical count against the snapshot.
10. **Ownership blindness.** Trivial-but-maintainer-owned items (release process, packaging pins, explicitly deferred work) classified GRAB NOW because they are easy. _Detect:_ any GRAB NOW justified purely by smallness in a subsystem the issue text marks as maintainer process.

---

## Scoring procedure

1. **Verify coverage mechanically** (Dimension 1) before reading for quality — count issue numbers against the snapshot manifest.
2. **Score each dimension 1–5.** For every score, quote at least one verbatim passage from the candidate output as evidence; for scores of 2 or below, quote the failing passage (or state which required content is absent and where it should have appeared). Judges must not paraphrase-and-score; the quote is the audit trail.
3. **Check failure signatures.** Any confirmed instance of signatures 1, 3, or 7 (confident misclassification, stale-report trust, snapshot overreach) caps Dimension 4 at 2, because these are exactly the miscalibrations the task exists to catch.
4. **Compute the weighted overall score:**

   | Dimension                                 | Weight |
   | ----------------------------------------- | ------ |
   | 1. Coverage and accounting                | 10%    |
   | 2. Actionability-grounded prioritization  | 25%    |
   | 3. Restraint and category discipline      | 15%    |
   | 4. Calibration and uncertainty accounting | 20%    |
   | 5. Collision-awareness                    | 10%    |
   | 6. Evidence-demands for INVESTIGATE       | 10%    |
   | 7. Execution-plan quality                 | 10%    |

5. **Verdict bands:** ≥4.5 exemplary (golden-comparable); 3.5–4.4 strong (usable triage, minor gaps); 2.5–3.4 mixed (usable only with human re-check of every GRAB NOW); 1.5–2.4 weak (systematic failure signatures present); <1.5 unusable.
6. **Judge confidence field (required):** state high / medium / low with one sentence of reason. Lower it when the judge lacks the snapshot manifest to verify coverage, when the candidate's claims would require repo access to falsify, or when two dimensions pulled in opposite directions and the weighting decided the verdict.
7. **Output format:** per-dimension score + evidence quotes, failure signatures observed (numbered list, possibly empty), weighted score, verdict band, judge confidence + reason.
