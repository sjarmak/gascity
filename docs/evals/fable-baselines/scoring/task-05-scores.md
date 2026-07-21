# Task 05 — Epic Decomposition — Scoring Report

**Rubric:** `docs/design/fable-distillation/rubrics/epic-decomposition.md`
**Task spec:** `docs/evals/fable-baselines/task-05-decompose.md`
**Frozen input:** `inputs/epic-dr-2vydrm.txt` (dr-2vydrm — three-benchmark QA framework)
**Candidates:** `scoring/blind/task-05-{A,B,C}.md`
**Panel:** single judge (Opus 4.8), scored blind. Model identities not inferred.

Weighting: `0.25·D1 + 0.25·D2 + 0.15·D3 + 0.10·D4 + 0.15·D5 + 0.10·D6`.

## Overall

| Candidate | D1  | D2  | D3  | D4  | D5  | D6  | Weighted | Verdict                   | Confidence |
| --------- | --- | --- | --- | --- | --- | --- | -------- | ------------------------- | ---------- |
| **A**     | 4   | 4   | 5   | 4   | 4   | 5   | **4.25** | dispatchable with edits   | high       |
| **B**     | 5   | 5   | 5   | 5   | 5   | 5   | **5.00** | golden-equivalent         | high       |
| **C**     | 3   | 4   | 3   | 4   | 3   | 4   | **3.45** | needs rework (borderline) | medium     |

Ranking: **B > A > C**, and the gaps are real, not stylistic. B reproduces the
golden-run anchors almost verbatim (tool/sweep split, adversarial-fixture gate,
cheap-tier-with-sampling routing, sequencing-as-priority underspec). A is a strong
plan whose two structural shortfalls (an unsplit lint bead, gates a notch less
sharp than B's). C is a competent skeleton that drops a required bead, omits the
highest-leverage gate, and over-serializes its tail.

---

## Epic ground truth used for traceability (D3/D6 anchors)

Beads the epic explicitly numbers or names: `.1` lib (A2), `.3` EB adapter +
`.6` EB triad + `.9` diagnostics (materialized in CHILDREN), `.8` CSB verifier-lint
(class-B row), `.5/.6/.7` triads (class-C row + sequencing), `.2/.3/.4` adapters
(sequencing; only `.3`=EB is pinned, `.2`/`.4` are an assumption), `.10` codeprobe
fairness, `.11` codeprobe verifier-honesty, plus reopened `codeprobe-voxa`.

Required-but-unnumbered (from acceptance): **EB fairness scan** (A5 lists "new
beads `.10` / **EB analog**"), cross-rig aggregate/dashboard (A6), per-rig corpus
QA runs + follow-up filing (Acceptance bullet 4), public response doc (bullet 5).

Live ambiguities a strong plan must surface: (a) tracker shows 3 open children but
the body enumerates `.1`–`.11`; (b) the gap table rows are **truncated in the
source** ("EB has no upstream zat…", "new bead per rig (code…"); (c) sequencing
"then lint + fairness in parallel" may be priority, not a data dependency;
(d) `.2`/`.4` adapter-ID mapping; (e) where `benchmark_qa_core` physically lives.

---

## Candidate A

### D1 — Decomposition structure: 4

Bead boundaries are clean and the graph encodes real data dependencies with named
slack: _"Critical path: `voxa → .1 → adapter → triad → corpus run → response doc`
(6 serial stages). Everything else fans out inside waves 2–4."_ Independent tracks
start early: _"Wave 0: `voxa`. (`.8` CSB shell-lint can also start here — it touches
the verifier runtime only, not the lib… pulling it forward shortens the critical
path.)"_ Waves re-derive consistently from the stated deps.

Capped below 5 by the rubric's own D1-5 requirement that _"Tool-building is split
from bulk application of the tool (a linter bead vs. a remediation-sweep bead)."_
A folds both into one bead: `.8 — CSB verifier-lint (class B)` scope is _"Bring CSB
`test.sh` verifiers to mechanical correctness… 42% of 781 files currently fail"_
with size _"needs-split (781 files, batch by task directory)"_ — exactly the
"build the tool AND run the sweep in one bead" conflation the D1-3 anchor names.
One conflation against otherwise 5-level structure → **4**.

### D2 — Acceptance-criterion testability: 4

Strong and frequently discriminating. A's `voxa` criterion is the **best of the
three candidates** on the actual defect voxa was reopened to fix: _"`jq -e '.reward
!= .diagnostics.ir_metrics.recall and (.scorer_family|type=="string")' trial.json`
exits 0"_ — this fails on the recall-only-reward defect, not merely on absence.
Negative fixtures are present: `.1` requires _"at least one fixture per class
exercising a known-defective input and asserting a finding is returned"_; `.10` /
`.11` each plant a defect (_"a planted-leak fixture is detected in unit test"_;
_"a fixture verifier that rewards keyword-presence-only is flagged"_). The `.14`
escape hatch is verifiable (_"`bd list --parent dr-2vydrm --label followup` returns
≥1 per rig… or the report explicitly records zero failures"_).

Held at 4 (not 5) because the D2-5 ceiling demands _every_ criterion discriminate,
and two do not: `.9` accepts a stub (_"`(float ≥0)`… `jq -e '.diagnostics.
task_time_seconds and .diagnostics.token_cost_usd'` exits 0"_ passes on `0.0`), and
`.8` lacks the golden verifiable-exceptions escape hatch. No failure signature
fires hard (negative verification is present, so #9 does not cap).

### D3 — Traceability and scope fidelity: 5

Opens by tracing the tracker-vs-body gap and asserts _"Every bead below traces to a
stated requirement."_ Implied beads carry explicit acceptance anchors: `.12` EB
fairness → _"the 'EB analog' A5 names alongside `.10`"_, `.13` → A6, `.14a/b/c` →
bullet 4, `.15` → bullet 5. Out-of-scope honored and restated (publishing deferred
to sjarmak; re-mining filed separately per line 79). The CSB class-E ambiguity is
**flagged, not silently resolved**: Q4 — _"A5 names only `.10`… and an 'EB analog';
no CSB fairness bead… Assumption: CSB + EB fairness are exercised through their
adapters calling lib E."_ Full coverage both directions, no gold-plating → **5**.

### D4 — Routing calibration: 4

Cheap tier is real and gate-mitigated: `.8` = _"cheap (bulk) + mid (spot review)…
Cheap tier for the sweep; mid-tier reviews the batches where shellcheck fixes change
semantics"_; `.14a/b/c` = _"cheap (run) + mid (triage)"_. Reasons are grounded in
blast radius and where judgment lives, not size. The one miscalibration vs the
golden anchor: fairness `.10/.12` routed **mid** (_"leak judgment has semantic
edges"_) where the golden run routes it cheap (semantics live in the lib). A minor
over-provision → **4**.

### D5 — Gate placement: 4

Gates sit at the fan-out points and name what they intercept. Gate 2 targets the
checker-that-passes-everything: _"a false-negative A/D/E function silently corrupts
all three rigs. No adapter dispatches until green."_ Gate 3 ties the adversarial
leg to the upstream-push decision. Held at 4 because the adversarial-**fixture-
quality** poison (the single most damaging failure in a QA epic) is addressed in
the routing table (_"gate fixture design with top-tier review"_) rather than as a
first-class gate naming its blast radius, and there is no dedicated sampling gate
for the cheap-tier bulk shell edits (contrast B's G5). Sharp, but a notch below the
golden anchor's crispness.

### D6 — Underspecification handling: 5

Eleven load-bearing questions, each with a dispatchable assumption. Q8 reproduces
the golden sequencing-vs-dependency insight: _"The epic sequences lint 'last'…, but
`.8` has no dependency on the lib or contract… If the 'last' ordering is a
resourcing choice… respect that — but it is not a true dependency."_ Q1 (tracker
vs body), Q2 (lib host location), Q9 (CSB migration blast radius), Q10 (adversarial
fixture construction) are each interpretation-changing. Structural ambiguities are
caught, not paved over → **5**.

**Weighted: 4.25 — dispatchable with edits.** Edits: split `.8` into a lint-tool
bead and a remediation-sweep bead; tighten `.9`'s criterion to require non-zero on
a real trial; add a verifiable exceptions-list escape hatch to `.8`; promote the
adversarial-fixture review to a named gate; consider routing fairness cheap.

---

## Candidate B

### D1 — Decomposition structure: 5

The tool/sweep split the rubric explicitly rewards is present and correct:
_".8a — CSB verifier-lint tool… Tool only — remediation is .8b"_ and _".8b — CSB
verifier remediation sweep… Shardable across parallel workers by directory."_ Real
parallelism is extracted into wave 0 to fill capacity off the critical path:
_"Wave 0 (3 parallel): V, .8a, .11 — the lint track has no dependency on the
contract and fills capacity while voxa is the critical path."_ Dependency
correctness is a cut above the others on `.11`: because the epic states _"B/C stay
per-benchmark (they touch the verifier runtime, not just static schema),"_ B
correctly makes `.11` (class-B analog) independent of the lib and dispatches it in
wave 0, whereas A chained `.11` to `.4`+`.1`. The critical path is named
(_"V → .1 → adapter → triad → corpus run → .R (6 hops)"_) and every wave
re-derives from the stated deps without contradiction. No conflation, no monoculture
→ **5**.

### D2 — Acceptance-criterion testability: 5

Reproduces the golden D2 ceiling anchor verbatim in `.8b`: _"`.8a` lint report shows
0 failures, OR shows only files enumerated in a committed exceptions list where each
entry carries a filed bead ID (verifier cross-checks each listed ID exists via `bd
show`)."_ Negative fixtures are pervasive and discriminating — `.8a`: _"a planted
known-bad fixture (missing pipefail + `grep|head`) is flagged with both finding
codes; a known-clean fixture passes"_; `.2`: _"Adapter unit test feeds one known-
defective CSB task and asserts a class-A finding surfaces"_; `.1`: per-class planted
defects with expected finding codes. Escape hatches are machine-verifiable (`.Q1–3`:
_"verifier cross-checks report finding count against filed-bead count"_). Residual
judgment is named and routed (G4). The single soft spot — `V`'s criterion checks
shape (_"`jq -e '.reward|type=="number" and .scorer_family|type=="string"'`"_)
rather than asserting `reward != recall`, the exact defect voxa reopened for — is
one soft criterion inside an otherwise ceiling-level set anchored by the golden
`.8b` escape hatch. Ceiling behavior dominates → **5** (fix `V` to assert
reward≠recall, as A did).

### D3 — Traceability and scope fidelity: 5

Each bead is traced (_"per the child listing"_, _"per the update's new children"_,
_"required by A5… and the gap table"_). The EB fairness bead A5 requires is present
as `.E` (_"required by A5 ('new beads .10 / EB analog')"_) — B does **not** drop it.
The CSB class-E conflict between the gap table and A5 is flagged in U3 and resolved
to an assumption, not silently. Out-of-scope honored: `.8b` files irrecoverables
separately; `.R` _"No publish action taken (no PR, no public push — verifier
confirms git log shows working-branch commit only)"_. No gold-plating; full
coverage both directions → **5**.

### D4 — Routing calibration: 5

Matches golden anchors #4 and #5 directly. Cheap tier is real and its risk is
mitigated by a gate rather than by promoting the tier: _"Cheap | .8b (sharded),
.10, .E | Mechanical: apply lint fixes, invoke lib scan functions. Semantic judgment
already lives upstream (in .8a's rules and .1's class-E logic)."_ Top tier is
reserved for propagating error (V, `.1`, `.R`) with blast-radius reasons
(_"imported by everything; wrong semantics propagate to all rigs"_). Every
assignment carries a judgment-content reason, not a size reason → **5**.

### D5 — Gate placement: 5

Six gates, each naming a failure and its downstream blast radius, cost calibrated to
the radius. G2 targets the checker-that-passes-everything: _"a false-negative defect
checker here makes every corpus run downstream report a clean corpus that isn't."_
G4 reproduces the golden fixture-quality anchor: _"top-tier review of the
adversarial-keyword-dump fixtures specifically… a weak adversarial fixture yields a
passing calibration that blesses gameable verifiers — the most damaging silent
poisoning available in this epic, because triad results gate sjarmak's upstream
push."_ G5 is the cheap-edit sampling gate A lacked: _"mid-tier reviews a random
≥10% of cheap-tier shell fixes per shard, checking behavior preservation… A
behavior-changed test.sh silently corrupts the golden leg of the CSB calibration."_
→ **5**.

### D6 — Underspecification handling: 5

Ten questions, load-bearing, each with an assumption; pinning gates are named
(U4 ir_metrics optionality _"Must be settled at G1"_; U7 adversarial threshold
_"must be pinned at G4"_). U5 reproduces the golden sequencing anchor verbatim:
_"No data dependency forces it. Assumption: it expresses priority… If sjarmak
intended a hard order, waves 0–1 lose .8a/.8b/.11 and the schedule stretches."_
Structural ambiguities (adapter-ID mapping U1, lib host U2, gap-table-vs-A5 U3) are
all surfaced → **5**.

**Weighted: 5.00 — golden-equivalent.** The only refinement worth making is
tightening `V`'s acceptance to assert `reward != recall` rather than shape alone.

---

## Candidate C

### D1 — Decomposition structure: 3

Correct three-way rig parallelism for adapters and triad, and lint pulled forward
(_"I've pulled them forward into Wave 1 to maximize parallelism"_). But three D1-3
signatures fire together: (a) the lint bead is **not split** — `.8` is one bead
whose _"Size class: needs-split — 328 failing files… split into a scripted bulk-fix
pass… plus a manual-remainder pass"_ (tool + sweep in one dispatch, same conflation
as A); (b) **no critical path is named** anywhere (A and B both name it); (c) the
tail is **over-serialized** — Wave 4 `.12` (aggregate) → Wave 5 `.13-*` (corpus
sweeps) → Wave 6 `.14`, yet C's own dep list has `.13-csb` depending on _".2, .5,
.8"_ and never on `.12`, so `.12` and `.13` are parallelizable and the 3-wave tail
is stricter than the data requires. That is the D1-3 anchor almost exactly ("at
least one ordering is stricter than the data requires… some available parallelism
left on the table") → **3**.

### D2 — Acceptance-criterion testability: 4

Broad, discriminating negative fixtures: `.1` — _"asserting non-empty findings on
the bad fixture and empty findings on the good one"_; `.2` — _"run… against one
known-broken CSB task (oracle path that doesn't exist) and assert the returned
findings list is non-empty and flags class A"_; `.4` adds a regression check against
zat9; `.10` seeds a leak for a true-positive check. `.9` is stronger than A's
(_"non-zero (a zero/null value on a real trial is a bug)"_). Cross-check escape
hatches are verifiable (`.13-csb`: _"count of filed follow-up beads equals count of
non-passing tasks… they must match"_; `.14`: bead-ID references must each resolve).
Held at 4, not 5: `.8` has no verifiable-exceptions escape hatch, and `.11`'s
criterion is _"post-fix run shows 0 findings, not a specific number"_ with no
planted-bad fixture — a stub linter that reports nothing would pass (vacuous-testable
per signature #2 for that bead). Negative verification is present overall, so #9
does not cap the dimension → **4**.

### D3 — Traceability and scope fidelity: 3

Tracing is otherwise good (inferred numbers flagged, A6/bullet-4/bullet-5 anchors
cited, re-mining out-of-scope repeatedly honored, no gold-plating). Capped at 3 by
one **required bead with no coverage**: A5 lists _"new beads .10 / EB analog,"_ but
C's §5.1 assumes it away — _"Provisional assumption: only codeprobe gets explicit
new beads (.10 for E, .11 for B-analog); EB/CSB gaps for classes B and E are NOT
covered by this epic's children."_ No EB fairness bead is created, where A made
`.12` and B made `.E`. This is honestly **flagged** (which keeps it out of the
silent-resolution D3-1 territory and off the signature-7 cap), and C's caution is
partly defensible because the gap-table rows are genuinely truncated in the source —
but A5 itself is not truncated and names the EB analog, so the miss is against
explicit acceptance text. One required requirement unbeaded → D3-3 anchor → **3**.

### D4 — Routing calibration: 4

Cheap tier used correctly for bulk mechanical work with a manual-remainder split:
_".8 (CSB lint), .11 | Cheap tier for the scripted bulk-fix pass; mid-tier for the
manual remainder | Shellcheck/pipefail fixes are pattern-mechanical at volume."_
`.1` carries a nuanced split (_"Top-tier for the API/schema design, mid-tier
acceptable for the class A/D/E implementation once the shape is fixed"_). Reasons are
about judgment content and blast radius. Slightly below B because the cheap-tier
risk mitigation is asserted in prose rather than bound to a named sampling gate → **4**.

### D5 — Gate placement: 3

Gates are above theater — most name a failure and its blast radius. Gate 3 is close
to the key one: _"a triad that 'passes' against a malformed contract is a false
green."_ Gate 7 adds a useful coverage gate. But the **highest-leverage poison the
rubric singles out is missing**: no gate reviews the adversarial-keyword-dump
**fixture quality** (the "checker that passes everything" / weak-adversarial-fixture
failure that both A and B target and the rubric calls _"a QA epic's worst outcome"_).
Gate 3 guards contract _shape_, not fixture _strength_, and there is no sampling gate
on the cheap-tier `.8` bulk edits. The shared-contract gate (gate 1) is strong, but
fixture quality has no protection at all — the D5-3 anchor's _"the highest-leverage
poison point (…the fixture quality) has the same weight as routine ones."_ → **3**.

### D6 — Underspecification handling: 4

Genuinely load-bearing list: §1 (gap-table truncation → coverage/bead-count), §2
(do the original corpus-run/response-doc acceptance items survive the tightening),
§4 (lint placement vs stated sequencing, with a concrete consequence — _"moving lint
earlier could create merge conflicts with .2-.4"_), §7 (unknown adapter file paths
block dispatch). Structural ambiguities are caught. Held at 4 by a broken internal
cross-reference — the intro says the `.2`=CSB/`.4`=codeprobe mapping is _"an
assumption (see U1),"_ but §5 item 1 is the gap table, not the adapter mapping, so
the flagged assumption is not where the pointer sends the reader — and by §6
(re-verifying already-closed zat9/EB-0rv.25) leaning toward trivia. Solid, just
below A/B's crispness → **4**.

**Weighted: 3.45 — needs rework (borderline dispatchable).** Required edits before
dispatch: add the EB fairness bead (A5's "EB analog"); split `.8` into lint-tool +
remediation-sweep beads; add a top-tier adversarial-fixture review gate and a
cheap-edit sampling gate; name the critical path and de-serialize the `.12`→`.13`
tail; give `.11` a planted-bad fixture.

---

## Comparative gaps (quote both sides)

**Tool/sweep split (D1).** B splits it — _".8a — CSB verifier-lint tool… Tool only —
remediation is .8b."_ A and C do not: A's `.8` is one bead sized _"needs-split (781
files…)"_; C's `.8` is one bead sized _"needs-split — 328 failing files… split into
a scripted bulk-fix pass… plus a manual-remainder pass."_ This is the rubric's
named D1-5 discriminator, and it separates B (5) from A/C on structure.

**Critical path (D1).** A: _"Critical path: `voxa → .1 → adapter → triad → corpus
run → response doc`."_ B: _"Critical path: V → .1 → adapter → triad → corpus run →
.R (6 hops)."_ C: none stated — a D1 omission.

**Adversarial-fixture gate (D5).** B: _"top-tier review of the adversarial-keyword-
dump fixtures specifically… a weak adversarial fixture yields a passing calibration
that blesses gameable verifiers… because triad results gate sjarmak's upstream
push."_ A addresses it but demotes it into routing (_"gate fixture design with
top-tier review"_). C omits it — its nearest gate guards contract _shape_
(_"a triad that 'passes' against a malformed contract is a false green"_), not
fixture strength. This is the sharpest D5 separation: B(5) > A(4) > C(3).

**EB fairness coverage (D3).** A creates it — `.12` _"the 'EB analog' A5 names
alongside `.10`."_ B creates it — `.E` _"required by A5."_ C assumes it away —
_"EB/CSB gaps for classes B and E are NOT covered by this epic's children."_ One
required bead present in two candidates, absent (though flagged) in the third.

**voxa criterion (D2).** A is the only candidate to make voxa's criterion
discriminate the actual defect — _"`.reward != .diagnostics.ir_metrics.recall`"_ —
where B (_"`.reward|type=="number"`"_) and C (_"every key path exists with the
correct type"_) check shape only. A shape check passes even if `reward == recall`,
which is precisely the recall-only-reward defect voxa was reopened to fix. A wins
this specific criterion over both B and C.

## Failure signatures fired

- **A:** none of the ten numbered signatures fires hard. The unsplit `.8` is a D1-3
  conflation (caps D1 at 4 here via the anchor prose, not a numbered signature);
  `.9`'s `float ≥0` is a lone vacuous-testable criterion but negative fixtures
  elsewhere keep #9 (missing negative verification) and #2 from capping the
  dimension.
- **B:** no failure signature fires. The `V` shape-only criterion is a single soft
  spot, not a dimension-capping signature.
- **C:** no numbered signature fires hard. #6 (invented beads) does **not** apply —
  C invents nothing; #9 does **not** apply — negative fixtures are present; #7
  (silent ambiguity resolution) does **not** apply — the EB-fairness assumption is
  flagged in §5.1. The material issues are a D3 coverage miss and a D5 missing-gate,
  both scored through the dimension anchors rather than the signature caps.

## Judge confidence

- **A — high.** Traceability and gate reasoning are fully checkable against the
  epic; the one borderline call (D2 4 vs 5) does not change the verdict band.
- **B — high.** Matches multiple golden anchors verbatim; scoring is unambiguous.
- **C — medium.** The decisive D3 call (is the EB fairness bead "clearly required"?)
  hinges on reading the un-truncated A5 as overriding the **truncated** gap-table
  rows in the source. A reasonable panelist could score C's D3 a 4 on the grounds
  that C's caution is justified by the garbled input, which would lift C to ~3.6.
  The rest of C's scores are firm.

## Rubric defects encountered

1. **Verdict-band gap.** The bands are _"3.5–4.4 dispatchable"_ and _"2.5–3.4 needs
   rework,"_ leaving 3.4–3.5 undefined. C lands at 3.45, exactly in the gap. Suggest
   closing the interval (e.g. `≥3.5` / `<3.5`).
2. **Anchors are drawn from the golden run, so a golden-matching candidate scores 5
   almost mechanically.** B reproduces anchors #1–#5 near-verbatim and therefore
   earns straight 5s. The rubric cannot distinguish "independently reached golden
   quality" from "is (or closely tracks) the golden output" — fine for scoring, but
   panels aggregating across a distillation program should be aware the ceiling is
   effectively "did you match the reference," not "did you exceed it."
3. **Two real structural failures live only in the dimension prose, not the numbered
   signature checklist:** the "tool+sweep in one bead" conflation (hit A and C) and
   the "over-serialized tail / stricter-than-data ordering" (hit C) are described in
   the D1 anchors but are not in the ten-item failure-signature list. A judge working
   the checklist alone would miss both. Consider promoting them to numbered
   signatures.
4. **D3/D6 traceability presumes a clean epic, but this frozen input has genuinely
   truncated gap-table rows.** That ambiguity is load-bearing for the EB-fairness
   coverage call and legitimately lowers judge confidence on any candidate that
   leans on that table. The rubric's traceability check gives no guidance for scoring
   against a partially-corrupted source; it should say whether the un-truncated
   acceptance list (A5) is authoritative over the truncated table.
