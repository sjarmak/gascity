# Task 01 — Issue Triage — Blind Scoring Report

**Rubric:** `docs/design/fable-distillation/rubrics/issue-triage.md`
**Task:** `docs/evals/fable-baselines/task-01-triage.md`
**Frozen input:** `inputs/issues-snapshot-2026-07-06.json` (40 open issues)
**Candidates:** `scoring/blind/task-01-{A,B,C}.md`
**Judge:** anonymous; did not author any candidate; scored text against rubric only.

## Mechanical coverage check (Dimension 1 pre-read)

Snapshot manifest (40): 3862, 3868, 3869, 3872, 3877, 3878, 3879, 3887, 3891, 3892, 3898, 3907, 3914, 3924, 3925, 3926, 3927, 3928, 3929, 3934, 3937, 3944, 3946, 3947, 3962, 3964, 3965, 3966, 3967, 3968, 3969, 3970, 3971, 3972, 3973, 3974, 3975, 3976, 3977, 3986.

Primary-classification membership (leading issue number of each bullet/row, cross-reference mentions excluded):

| Candidate | Classified once | Dropped            | Double-bucketed | SKIP populated |
| --------- | --------------- | ------------------ | --------------- | -------------- |
| A         | 40 / 40         | —                  | none            | **0 (empty)**  |
| B         | 39 / 40         | **#3928** (silent) | none            | 5              |
| C         | 39 / 40         | **#3947** (silent) | none            | 4              |

- **A**: clean 40/40, no drops, no double-bucketing. SKIP bucket empty.
- **B**: #3928 appears only as a cross-reference inside the #3975 entry ("_slight overlap with the larger lifecycle-observability asks in #3928_"), never given its own verdict. B's stated bucket counts (GRAB 12 / GOOD 18 / INVESTIGATE 4 / SKIP 5 = 39) sum to 39 without B noticing the shortfall.
- **C**: #3947 (never-run cron bootstrap) does not appear anywhere in the file — a genuine drop of an issue both other candidates rank in their same-day top 5.

Neither B nor C acknowledges its drop. This is failure signature 9 (coverage leak) for both.

---

## Candidate A — per-dimension scores

**D1 Coverage (10%) — 4.** Every issue classified exactly once, no double-bucketing, and the findings-bundle family is named up front: _"most issues #3962 and below down through #3862 in this range carry the marker 'downstream Gas City deployment... operator-approved findings bundle (18 items)'"_. Below 5 because A uses the family to _raise_ its grab rate — _"That raised my GRAB NOW rate for this batch"_ — rather than converting it into the shared "reproduce on main?" caveat the anchor wants, and it never states per-bucket counts or reconciles them to 40.

**D2 Prioritization (25%) — 3.** The top-5 picks are individually bounded and each names a fix shape (e.g. #3969 _"Align the two"_, #3962 _"Textbook 'add an exec.LookPath fallback' fix"_). But the GRAB NOW bucket is inflated to 21 and admits items with real design/blast risk that the other two candidates hold back: #3927 (_"builtin:claude hardcodes --effort max"_, a flag-ordering change to every claude session), #3926 (prune that _"deletes data on live Dolt stores"_), #3914 (reconciler-core). The grab criterion is partly evidence-quality rather than boundedness — A literally justifies the bucket by the reporter's evidence bar, not by low collision or small radius. There is no articulated cross-pick ordering rationale (the top 5 is an ordered list with no "why this order" statement).

**D3 Restraint (15%) — 2.** SKIP is empty on a 40-issue snapshot — the padding signature — and A says so: _"I found zero confident SKIPs — I'd rather mark something INVESTIGATE than assert staleness/duplication I can't verify."_ Release-branch bookkeeping lands in GRAB NOW despite being maintainer process: _"#3879 — back-merge the 1.3.1-1.3.3 CHANGELOG sections... it targets a release branch, which some maintainers keep to themselves — flagged, not disqualifying."_ A recognized the ownership signal and grabbed anyway (ownership blindness). Not a 1 because INVESTIGATE (6) and GOOD CANDIDATE (13) are genuinely populated and most GOOD entries carry a named risk.

**D4 Calibration (20%) — 3.** Confidence is assigned by bucket default rather than per classification: _"GRAB NOW picks below are high confidence unless noted inline. GOOD CANDIDATE and INVESTIGATE calls are medium confidence by construction."_ That is clustered-by-design and carries little per-issue signal. Stale handling is partly right — #3869 is held for _"whether the specific DELETE... failure still reproduces on current versions"_ — but the systematic "snapshot has no PR/assignee data" limit is never foregrounded and wired into the confidence figures. No cap triggered (no confident-misclassification / overreach).

**D5 Collision (10%) — 3.** Fresh-P1 collision is flagged on the top pick: _"worth checking whether this was already fixed upstream since discovery (2026-06-25) given the priority/p1 label — a maintainer would likely have moved fast."_ A cross-links one family: _"#3914... Related to #3964 below."_ But there is no global "PR sweep is the real first action" statement and no systematic maintainer-active-family framing.

**D6 INVESTIGATE (10%) — 4.** INVESTIGATE calls name the missing artifact and a resolution path, e.g. #3974: _"whether bd (the CLI) lives in this repo or is vendored from the separate steveyegge/beads upstream — if the setter belongs in bd itself, this may be partly out of scope... and would need a companion issue upstream."_ Below 5 because most entries do not enumerate the competing hypotheses the way the anchor's Dolt example does.

**D7 Execution plans (10%) — 4.** First steps cite artifacts (#3944: _"read cmd/gc/cmd_formula.go:888-889 ... and internal/graphroute/graphroute.go:490"_), tests are assertion-level (#3862: _"assert .hooks.PreToolUse count stays at 1 ... assert a second, genuinely distinct hook entry survives the dedupe"_), and several disconfirmers are genuine alternative causal stories (#3892: _"the 5s cap exists specifically because raw bd writes publish no city event — if such writes happen more often in some deployments... extending the cap could introduce a real latency regression"_). Below 5 because a couple of disconfirmers restate scope risk rather than an alternative hypothesis (#3947: _"firing once vs. firing for every missed window needs a decision"_).

**Weighted:** 4(.10)+3(.25)+2(.15)+3(.20)+3(.10)+4(.10)+4(.10) = **3.15 — mixed** (usable only with human re-check of every GRAB NOW).

**Failure signatures:** 4 (bucket padding — empty SKIP, 21 GRAB NOW), 10 (ownership blindness — release-branch items in GRAB NOW), partial 8 (confidence clustered by bucket-default construction).

---

## Candidate B — per-dimension scores

**D1 Coverage (10%) — 3.** Excellent family recognition — _"issues #3960–#3977 are one downstream operator's 18-item findings bundle filed the same morning... Anything from that batch needs a 'does this still reproduce on main?' check as step zero, even the GRAB NOW picks"_ — and cross-links (#3986: _"Same code neighborhood as #3944 — coordinate if doing both"_). But #3928 is silently dropped and the stated counts (12/18/4/5) sum to 39 with no notice. A silent drop plus unreconciled counts is exactly the level-3 definition; the family work cannot lift a dimension whose core is once-and-only-once accounting.

**D2 Prioritization (25%) — 5.** Design-fork issues are kept out of GRAB NOW with the trade named: #3968 _"the fix is a design fork — widen the worker claim query vs convoy-wrap at dispatch — touching core routing... Needs maintainer direction on which side to fix."_ The ordering carries an explicit actionability-gradient rationale: _"1–3 are confirmed bugs with the mechanism already pinned to code, where the issue quality does most of the investigation for me; 4 is triaged and precise but carries one semantic decision; 5 is the smallest and safest but also the lowest value, included because it is a guaranteed same-day merge-quality PR."_ GRAB NOW is a disciplined 12.

**D3 Restraint (15%) — 5.** Real SKIP bucket keyed on ownership, not difficulty: #3946 _"a version pin in the Homebrew tap formula — maintainer-owned packaging/release infra, one line, not contributor-shaped"_, and it redirects to the durable sibling (_"The durable contributor-side improvement is #3977's observability"_). Umbrella recognized: #3924 _"Analysis umbrella; its actionable children are already split out (#3929, #3925)."_ GOOD CANDIDATE entries each carry a named risk.

**D4 Calibration (20%) — 5.** The systematic limit is stated once and wired into every figure: _"The snapshot has no PR/assignee data, so 'collision with maintainer work' is inferred from labels and issue age only — that is the main systematic uncertainty in every confidence figure here."_ Confidence is split where it matters: #3872 _"Confidence: medium on the classification, low on any single fix scope without that confirmation."_ Stale self-reports are re-verification demands, not facts (the step-zero reproduce-on-main rule above).

**D5 Collision (10%) — 5.** Modeled at both scales. Pick-vs-pick: #3986 _"(Same code neighborhood as #3944 — coordinate if doing both.)"_ and its disconfirmer _"this touches the same decorate plumbing as #3944, so doing both without coordination risks conflicting patches."_ List-vs-maintainer, demoting the whole ranking to conditional: _"everything here assumes no in-flight maintainer PRs, which the snapshot cannot show — the real first action on each pick is a duplicate/PR sweep."_

**D6 INVESTIGATE (10%) — 5.** Each is a designed experiment. #3869: _"the full Dolt error text and whether it reproduces on current dolt/bd versions (reporter is on gc 1.2.1, dolt 2.1.4 — old). Could be a Dolt engine limitation (giant NOT IN), a NULL-semantics bug in the SQL, or already moot."_ Exact artifact + version caveat + three discriminating hypotheses. #3974 and #3973 both name the resolution path ("re-file there" / "SKIP").

**D7 Execution plans (10%) — 5.** The #3947 disconfirmer survives its own disconfirmation: _"the zero-lastRun skip may be a deliberate fire-on-install guard, in which case the correct fix is stamping lastRun at install instead — the same test pins either implementation."_ #3986 proposes an alternative design intent (_"v2 might intend a different source-close mechanism (convoy linkage rather than metadata)"_). First steps and tests are concrete throughout.

**Weighted:** 3(.10)+5(.25)+5(.15)+5(.20)+5(.10)+5(.10)+5(.10) = **4.80 — exemplary** (see rubric-defect caveat below).

**Failure signatures:** 9 (coverage leak — #3928 dropped, counts unreconciled). No others.

---

## Candidate C — per-dimension scores

**D1 Coverage (10%) — 3.** Batch cluster noted with a collision framing — _"issues 3962–3973 (the 06:03–06:04Z cluster) are one '18-item findings bundle'... grabbing one mid-bundle carries a small risk of being reclassified"_ — but #3947 is dropped entirely and no per-bucket counts are reconciled to 40. The dropped issue is one both other candidates place in their same-day top 5, so the leak is consequential, not incidental.

**D2 Prioritization (25%) — 5.** Explicit, sophisticated ordering rationale that weighs value against collision: _"Ranking favors: confirmed root cause, small blast radius, provable oracle, low collision — over raw value. The two highest-value items (3862, 3986) are held just out of the top slots only for collision risk, and I say so."_ Design-fork excluded from GRAB NOW: #3968 _"Core claim path; demand-count vs claim-key divergence is architectural — high value, real risk"_. GRAB NOW is the most disciplined of the three (9).

**D3 Restraint (15%) — 4.** Real ownership-keyed SKIPs (#3879 _"Maintainer release bookkeeping; high collision with the release process"_; #3878 _"belongs to the next 1.3.x hotfix cut, maintainer-owned"_). But the canonical packaging SKIP is misfiled to GOOD CANDIDATE — #3946 _"Fix lives in the Homebrew tap formula... likely a different repo than gascity core"_ sits in GOOD, where the rubric's own D3 anchor treats it as a clear SKIP — and GOOD CANDIDATE swells to 24 of 40, a mild inflate-into-positive-buckets tendency (offset by every GOOD row carrying a named risk).

**D4 Calibration (20%) — 4.** The best systematic-limit analysis of the three, operationalized into a heuristic: _"status/needs-triage = maintainer has not scoped it → lower collision risk... A priority/p1|p2 label = maintainer has seen and ranked it → higher chance it is already owned."_ Split confidence appears (#3862 _"High on the bug; Med on collision (p1 → likely owned)"_). Held below 5 because the task requires confidence _per classification_ and the GOOD CANDIDATE / INVESTIGATE / SKIP tables carry no confidence column — per-issue confidence is only supplied for GRAB NOW plus an end-of-doc summary, so ~28 verdicts have no stated confidence.

**D5 Collision (10%) — 4.** List-vs-maintainer sweep foregrounded as a concrete command: _"the one cheap check that changes everything is `gh issue view <n> --json assignees,state` + a PR search for the issue number. I flag this per-pick rather than pretending the snapshot settles it."_ Family and fresh-P1 collision handled well (#3986 _"filed today = highest collision"_). Below 5 because C never surfaces the #3944↔#3986 pick-pair code overlap that B caught — it ranks #3944 first and discusses #3986 separately without noting they share the graph.v2 decorate plumbing.

**D6 INVESTIGATE (10%) — 5.** Each names the discriminating evidence and both resolution paths. #3964: _"Is this a bug or working-as-designed?... Need a maintainer intent call: suppress the advisory for self-owned singletons, or wontfix."_ #3869: _"DELETE ... NOT IN (subquery) may be a Dolt dialect limitation, which changes the fix entirely."_ Routing #3964 to INVESTIGATE (both others hedged it as GOOD CANDIDATE) is the sharper call for a genuine bug-vs-by-design question.

**D7 Execution plans (10%) — 5.** Disconfirmers are genuine alternative causal stories with the correct-fix-under-the-alternative attached. #3944: _"if sling and cook --attach legitimately carry different rig semantics (attach targets an existing bead that may belong to another rig), then copying sling's context is wrong and the real fix is 'resolve from the attached bead's rig.'"_ #3966's test is chosen to survive disconfirmation: _"The test above already encodes that boundary, so the risk is low."_ First steps cite artifacts and tests are assertion-level with golden comparisons.

**Weighted:** 3(.10)+5(.25)+4(.15)+4(.20)+4(.10)+5(.10)+5(.10) = **4.35 — strong** (usable triage, minor gaps).

**Failure signatures:** 9 (coverage leak — #3947 dropped). Borderline: GOOD CANDIDATE at 24/40 leans toward inflation, but each entry carries a named risk so it is not a confirmed padding fail; missing per-classification confidence on ~28 verdicts is a prompt-compliance gap folded into D4.

---

## Comparative section

**Overall:** B 4.80 (exemplary) > C 4.35 (strong) > A 3.15 (mixed).

### Dimension-by-dimension gaps

- **D1 Coverage.** A is the only clean sheet: _"40 issues total"_ with every number placed once. Both B and C leak — B drops #3928 (present only as _"asks in #3928"_ inside another entry), C drops #3947 (absent entirely). Ironically A, the coverage winner, loses overall on the heavier dimensions where it is weakest.
- **D2 Prioritization (heaviest, 25%).** B and C both articulate an ordering rationale and exclude design-forks; A does neither. Contrast A's grab justification — _"That raised my GRAB NOW rate for this batch"_ (evidence quality) — against C's — _"Ranking favors: confirmed root cause, small blast radius, provable oracle, low collision — over raw value"_ (actionability gradient). A grabs #3927/#3926/#3914 (behavior-change / live-data-delete / reconciler-core) while B and C route the same issues to GOOD CANDIDATE with the design risk named (B on #3927: _"flag-ordering changes affect every claude session everywhere; behavior-change blast radius wants maintainer sign-off"_).
- **D3 Restraint (15%).** The Homebrew pin (#3946) is the discriminator. B SKIPs it on ownership grounds (_"maintainer-owned packaging/release infra, one line, not contributor-shaped"_) — the rubric's own anchor. C files it GOOD CANDIDATE (_"likely a different repo than gascity core"_) — correct diagnosis, wrong bucket. A never reaches it as a SKIP at all (INVESTIGATE) and leaves SKIP empty. Clear ordering B > C > A.
- **D4 Calibration (20%).** B and C both name the no-PR/no-assignee limit and both split classification-vs-scope confidence; C's operationalization of the label heuristic is arguably richer (_"priority/p1|p2 = maintainer has seen and ranked it → higher chance it is already owned"_). C's deduction is compliance: its GOOD/INVESTIGATE/SKIP rows omit the per-classification confidence the prompt demands, whereas B labels every entry. A trails both with bucket-default confidence (_"medium confidence by construction"_).
- **D5 Collision (10%).** B is the only candidate to model both scales — pick-vs-pick (_"Same code neighborhood as #3944 — coordinate if doing both"_) and list-vs-maintainer (_"the real first action on each pick is a duplicate/PR sweep"_). C matches the list-scale superbly (the `gh issue view` gate) but misses the pick-pair link. A handles collision only sporadically.
- **D6 / D7.** B and C are near-indistinguishable and both exemplary; A is a step behind on hypothesis enumeration (D6) and on one or two scope-flavored disconfirmers (D7). C's #3944 disconfirmer ("resolve from the attached bead's rig") is marginally sharper than B's ("explicit fallback for empty rig context") — the one place C edges B.

### Failure signatures by candidate

- **A:** 4 (bucket padding — empty SKIP, 21/40 GRAB NOW), 10 (ownership blindness — #3878/#3879 release-branch items in GRAB NOW), partial 8 (confidence clustered by construction). Partial 2 (grab-ability conflated with evidence quality).
- **B:** 9 (coverage leak — #3928 dropped, counts unreconciled). No others.
- **C:** 9 (coverage leak — #3947 dropped). Borderline padding (24/40 GOOD CANDIDATE) not confirmed; per-classification confidence missing on GOOD/INVESTIGATE/SKIP.

---

## Rubric defects encountered

1. **Anchor leakage inflates B and compresses the B–C gap (most serious).** The rubric's 5-level anchors are reproduced near-verbatim in candidate B across D1 (_"18-item findings bundle filed the same morning... does this still reproduce on main? check as step zero"_), D2 (the _"1–3 are confirmed bugs... 5 is the smallest and safest but also the lowest value"_ ordering rationale), D4 (_"The snapshot has no PR/assignee data, so 'collision with maintainer work' is inferred from labels and issue age only"_ and the split-confidence line), D5 (_"Same code neighborhood as #3944"_ and _"the real first action on each pick is a duplicate/PR sweep"_), and D7 (the zero-lastRun _"deliberate fire-on-install guard... the same test pins either implementation"_ disconfirmer). The rubric states it was _"calibrated against the 2026-07-07 golden run"_, and B reads as that golden output. Scoring against anchors therefore partly measures resemblance to the calibration source rather than independent quality. B genuinely satisfies the criteria, so I did not deflate its dimension scores, but the B-over-C margin should be read as partly an artifact. **Fix:** paraphrase anchors or draw them from a held-out exemplar that is never itself a scored candidate.
2. **Dimension 1 has an undefined cap rule.** Coverage leak is called a _"hard failure... regardless of prose quality"_, yet the same dimension rewards family recognition and cross-linking. B and C each drop an issue but score mid-band on the strength of family work. The rubric does not say whether a hard-failure sub-criterion caps the dimension or merely competes with the others.
3. **Per-classification confidence is a prompt requirement with no scored home.** The task prompt says _"State your confidence per classification"_, but the rubric folds confidence into D4 as calibration quality only. C omits per-row confidence on ~28 verdicts yet still scores well on D4 because its systematic-limit analysis is strong. Compliance ("confidence stated as required") and quality ("confidence is calibrated") should be separable so a rich-but-non-compliant answer is not over-credited.
4. **SKIP-vs-INVESTIGATE boundary conflicts across dimensions.** For cross-repo/packaging items (#3946, #3974), D3's anchor treats the Homebrew pin as a clear SKIP (ownership), while D6 rewards an INVESTIGATE that resolves with _"re-file there / SKIP"_. A candidate is penalized under D3 for INVESTIGATE-ing #3946 but could be credited under D6 for the identical call. The rubric does not say which dimension governs a given issue.
5. **No dimension scores whether the "start today" set is the actually-best five.** "What to start today" is the task's headline deliverable, but a candidate that drops a top-tier pick from its start-today set (C omitting #3947, which A and B both rank) is docked only via the D1 coverage hit, not for the weaker same-day plan itself.

---

## Judge confidence

**Medium.** Coverage facts (D1) are mechanically verified against the 40-number manifest, so the drops and the empty-SKIP finding are certain. Confidence is held at medium, not high, because (a) the anchor leakage means B's lead is partly self-similarity to the rubric author rather than demonstrated superiority over C, and (b) several quality calls — whether a given GRAB NOW item is truly design-forky, whether a disconfirmer is a genuine alternative cause — rest on the candidates' own descriptions of a repository I cannot inspect, which the rubric itself flags as a confidence-lowering condition. The verdict order B > C > A is robust; the size of the B-C gap is the soft part.
