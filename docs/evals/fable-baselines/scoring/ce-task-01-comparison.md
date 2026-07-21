# CE Judge Report — Task 01 (Issue Triage Ranking)

**Judge:** blind CE comparison, same model tier, different harness conditions.
**Inputs:** rubric `rubrics/issue-triage.md`; task `task-01-triage.md`; frozen snapshot `inputs/issues-snapshot-2026-07-06.json` (40 issues); candidates `scoring/blind-ce/task-01-X.md` and `task-01-Y.md`.
**Method:** coverage verified mechanically against the 40-number manifest; each dimension scored 1–5 with verbatim evidence; failure signatures checked per candidate; weighted per the rubric table.

---

## Mechanical coverage check (Dimension 1 prerequisite)

Manifest (40): 3862, 3868, 3869, 3872, 3877, 3878, 3879, 3887, 3891, 3892, 3898, 3907, 3914, 3924, 3925, 3926, 3927, 3928, 3929, 3934, 3937, 3944, 3946, 3947, 3962, 3964, 3965, 3966, 3967, 3968, 3969, 3970, 3971, 3972, 3973, 3974, 3975, 3976, 3977, 3986.

| Candidate | GRAB | GOOD | INVESTIGATE | SKIP | Total | Unique | Missing | Dupes |
| --------- | ---- | ---- | ----------- | ---- | ----- | ------ | ------- | ----- |
| X         | 14   | 15   | 6           | 5    | 40    | 40     | none    | none  |
| Y         | 21   | 13   | 6           | 0    | 40    | 40     | none    | none  |

Both are clean on coverage: every issue classified exactly once, no leaks, no double-bucketing. The distributions already diverge sharply — X is 14/15/6/5, Y is 21/13/6/**0**. A 21-GRAB, 0-SKIP split on a 40-issue snapshot is the padding signature the rubric says to flag "before reading a word of the prose" (Dimension 3 judge detection).

---

## Candidate X — per-dimension

**D1 Coverage (10%) — 5.** Counts stated and reconciled: _"Bucket counts (40 total, sums to input) … 1 — GRAB NOW 14 / 2 — GOOD CANDIDATE 15 / 3 — INVESTIGATE 6 / 4 — SKIP 5."_ Families are identified and converted into triage rules, not treated as independent evidence: _"18-item batch, same reporter, same morning. #3964–#3977 … Treat this cluster as one shared-provenance family: … an environment doubt in one member is a doubt worth checking in siblings too."_ Plus umbrella (#3924→#3925/#3929), duplicate (#3914/#3964), and three named subsystem clusters. Matches the 5-anchor.

**D2 Actionability prioritization (25%) — 5.** GRAB NOW entries name the reason the work is bounded in mechanism language: _"#3969 — … root cause explicitly named (reply path assigns by `mail.to_session_id`, inbox listing filters by a different identity field) … mechanism stated in mechanism language, not impact language."_ Design-fork issues are deliberately kept **out** of GRAB NOW with the fork named: _"#3968 — … two viable fixes named in the issue itself … This is the calibration worked example in the skill itself; classification held."_ Ranking rationale is an explicit actionability gradient: _"Ranked by actionability gradient (mechanism-pinned-to-code > one-bounded-decision > small-and-safe)."_ Matches both 5-anchors.

**D3 Restraint (15%) — 5.** SKIP is populated and each entry names the ownership category, with the packaging exemplar SKIPped despite being trivial: _"#3946 — maintainer-owned (Homebrew formula's unversioned `depends_on \"beads\"` — packaging/release infra … 'one-line fix in maintainer-owned release/packaging infra is still Tier 4' regardless of how easy the fix looks)."_ This is the exact Dimension 3 anchor. GOOD CANDIDATE entries each carry a named risk/fork (e.g. #3986 _"a locate judgment remains before the fix is scoped"_).

**D4 Calibration (20%) — 4.5.** Confidence varies with evidence and is explicitly histogrammed: _"high (16), medium-high (11), medium (11), N/A/Tier-3 (6). Values vary and track real evidence differences."_ Split confidence is used repeatedly: _"#3927 — … Confidence: high on mechanism; medium-high on this exact fix shape."_ Blind spots are wired into concrete verdicts, not left as a disclaimer: _"Several issue bodies are truncated … Any classification of these carries an explicit 're-read full body' first step, not a confident verdict."_ Shaved half a point only for a few assertive reporter-repro restatements (_"reproduced on unmodified main, byte-identical function"_) that are attributed to the issue but phrased as fact.

**D5 Collision (10%) — 5.** Modeled at both scales. List-level: _"this entire ranking is conditional on a collision sweep this snapshot cannot perform. The true first action for the whole list … is `gh pr list --search '<issue-number>'` plus a timeline check."_ Fresh-P1 mechanism applied: _"#3862 (`priority/p1`, 5–6 days old) … carry an explicit 'check open PRs first' precondition."_ Pick-vs-pick overlap explicitly cleared with a swap test. Matches the anchor.

**D6 INVESTIGATE evidence (10%) — 5.** Each INVESTIGATE names artifact, action, and resolution paths including re-file/SKIP: _"#3974 — … if beads-repo-owned → Tier 4 for gascity (re-file there); if gc needs its own wrapper flag independent of the beads-side change → Tier 1/2 here."_ Stale #3869 gets a designed re-verification: _"re-run the exact quoted DELETE query against a current build … if it still fails identically → Tier 1; if it no longer reproduces → close as stale."_

**D7 Execution plan (10%) — 5.** Disconfirmers are genuine alternative causal stories with the correct fix under the alternative: _"#3862 — … if the reconciler's intended design is 'replace the whole hooks array from pack config each tick' rather than 'append if missing,' the correct fix is wholesale-replace, not dedupe-append."_ Tests are assertion-level with a tautology guard: _"assert the hardened assertion PASSES; separately assert it still correctly FAILS when 'reviewer'/'codex' is genuinely absent … so the fix doesn't collapse into an always-pass tautology."_ Explicit swap test included.

**Weighted X:** (5·10 + 5·25 + 5·15 + 4.5·20 + 5·10 + 5·10 + 5·10) / 100 = **4.90** → band **exemplary (golden-comparable)**.

---

## Candidate Y — per-dimension

**D1 Coverage (10%) — 4.** All 40 classified once; cross-links present (#3964↔#3914, #3924↔#3925, #3872↔#3928). The batch is noticed: _"most issues #3962 and below … carry the marker '… operator-approved findings bundle (18 items)'."_ Two gaps against the 5-anchor: bucket counts are never stated or reconciled, and the batch provenance is used in the **wrong direction** — _"That raised my GRAB NOW rate for this batch"_ — instead of attaching the "does this still reproduce on main?" re-verify caveat the anchor calls for.

**D2 Actionability prioritization (25%) — 3.** Many GRAB NOW entries are well-grounded in mechanism (#3969, #3966, #3944). But a design-call issue lands in GRAB NOW **and** in the top 5: #3947 is ranked #3 while Y's own disconfirmer for it admits _"firing once vs. firing for every missed window needs a decision that matches #2721's original intent"_ — a design decision, i.e. the signature miss. The ranking rationale is importance-adjacent rather than boundedness-driven: _"That raised my GRAB NOW rate for this batch; it also means I found zero confident SKIPs."_ No explicit actionability-gradient statement.

**D3 Restraint (15%) — 1.** SKIP is empty by declaration: _"SKIP … None with enough evidence to call confidently."_ The rubric's own SKIP exemplar (the Homebrew packaging pin) is demoted to INVESTIGATE — _"#3946 — … which repository hosts the … Homebrew formula … the fix … may not be actionable from a gascity-repo PR at all"_ — and maintainer-owned release-branch work is elevated into GRAB NOW: _"#3879 — back-merge the … CHANGELOG sections from `release/v1.3.0` into `main` … Only caveat: it targets a release branch, which some maintainers keep to themselves — flagged, not disqualifying"_ and _"#3878 — … Same release-branch caveat."_ This is the 1-anchor: SKIP near-empty on a meaningful snapshot, easy conflated with contributor-appropriate.

**D4 Calibration (20%) — 2.** Confidence is assigned by bucket construction, not by evidence: _"GRAB NOW picks below are high confidence unless noted inline. GOOD CANDIDATE and INVESTIGATE calls are medium confidence by construction."_ That is calibration collapse — a flat within-bucket distribution where the label is decoration. It compounds with confident (high) GRAB NOW classification of maintainer-owned #3878/#3879 (ownership-blind misclassification). Failure signatures 1, 8, and 10 are present, which the scoring procedure caps at 2. Y does handle stale #3869 and self-invalidating #3972/#3976 well, which keeps it off a 1.

**D5 Collision (10%) — 3.** Collision is mentioned per-issue (_"#3862 … worth checking whether this was already fixed upstream since discovery (2026-06-25) given the `priority/p1` label"_) and two own-picks are cross-referenced (#3964/#3914 "read both," #3872/#3928 "read that one first"). But there is no list-level "the collision sweep is the true first action," no fresh-P1 mechanism, and the top-5 carry no pairwise-overlap sweep. 3-level.

**D6 INVESTIGATE evidence (10%) — 4.** The genuine INVESTIGATEs are precise instruments: _"#3974 — … whether `bd` (the CLI) lives in this repo or is vendored from the separate `steveyegge/beads` upstream … if the setter belongs in `bd` itself, this may be partly out of scope."_ #3869 gets a real re-verification demand. Docked one point because #3946 and #3924 sit here as SKIP-avoidance rather than true evidence gaps (#3924 is an umbrella whose children are already filed).

**D7 Execution plan (10%) — 4.** All five have concrete first step, blast radius, assertion-level test, and a disconfirmer. Two disconfirmers are strong alternative-cause stories: _"#3892 — … raw `bd` writes publish no city event … extending the cap could introduce a real latency regression"_ and _"#3944 — … cook can be invoked from outside any rig directory … threading 'current rig' through could resolve to the wrong rig."_ Slightly below X because #3947's disconfirmer is scope-ambiguity and #3862's is partly schedule risk (_"worth checking whether this was already fixed upstream"_); no swap test.

**Weighted Y:** (4·10 + 3·25 + 1·15 + 2·20 + 3·10 + 4·10 + 4·10) / 100 = **2.80** → band **mixed (usable only with human re-check of every GRAB NOW)**.

---

## Failure-signature comparison (the comparison that matters)

| #   | Signature                          | X      | Y        | Evidence                                                                                                                                                                 |
| --- | ---------------------------------- | ------ | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1   | Confident misclassification        | **no** | **yes**  | Y assigns high confidence to maintainer-owned #3879/#3878 in GRAB NOW "by construction." X's highs are all file:line/named-function mechanism pins.                      |
| 2   | Severity-ranking                   | no     | **mild** | Y ranks design-call #3947 at #3 and raises its "GRAB NOW rate" on evidence-bar/importance grounds. X orders strictly by actionability gradient and demotes design-forks. |
| 3   | Stale self-reports as ground truth | no     | no       | Both flag #3869 staleness, both quote the #3976/#3972 re-grades and demand re-verification on current main.                                                              |
| 4   | Bucket padding                     | no     | **yes**  | Y is 21/13/6/**0** with SKIP empty by declaration. X is 14/15/6/5 with every GOOD CANDIDATE carrying a named risk/fork.                                                  |
| 5   | INVESTIGATE as shrug               | no     | no       | Both pass the junior-contributor test on their genuine INVESTIGATEs; Y's misuse of the bucket is a restraint issue (D3), not a content-free shrug.                       |
| 6   | Template execution plans           | no     | no       | Both survive a swap test (X runs one explicitly); disconfirmers are issue-specific in both.                                                                              |
| 7   | Snapshot overreach                 | no     | no       | Neither asserts open-PR/assignee state as fact. Shared file:line refs (`cmd/gc/cmd_formula.go:888-889`) appear in the issue body — both surface them independently.      |
| 8   | Calibration collapse               | no     | **yes**  | Y: "high … / medium … by construction" — flat within-bucket. X: histogram high(16)/mh(11)/medium(11) plus split classification-vs-scope confidence.                      |
| 9   | Coverage leak                      | no     | no       | Both 40/40, verified mechanically.                                                                                                                                       |
| 10  | Ownership blindness                | no     | **yes**  | Y puts release-branch #3878/#3879 in GRAB NOW and the packaging pin #3946 in INVESTIGATE. X SKIPs all three on ownership, redirecting to the contributor-shaped sibling. |

X exhibits zero failure signatures. Y exhibits 1, 4, 8, 10 outright and 2 mildly — and those four are precisely the miscalibrations this task exists to catch.

---

## Verdict

**X = 4.9 (exemplary, golden-comparable) vs Y = 2.8 (mixed).** Difference ≈ 2.1 points — decisive, not marginal.

**What the better candidate (X) does that Y does not:**

1. **Populates SKIP on ownership, not difficulty.** X SKIPs the Homebrew packaging pin (#3946), the release-branch CHANGELOG/deps.env work (#3879/#3878), the umbrella (#3924), and the duplicate (#3964) — each with a category and a redirect. Y declares SKIP empty and scatters those same items into GRAB NOW and INVESTIGATE, inflating GRAB NOW to 21/40. This is the single largest divergence.
2. **Keeps design-fork issues out of GRAB NOW / the top 5.** X ranks by an explicit actionability gradient and demotes #3968, #3898, #3934, #3947 with the undecided fix-shape named. Y promotes design-call #3947 into GRAB NOW and its top-5 while its own disconfirmer admits the design decision is unresolved.
3. **Calibrates per-issue on evidence, with split confidence.** X's confidence histogram varies and separates classification-confidence from fix-scope-confidence (#3927, #3986, #3937). Y assigns confidence by bucket "by construction," so within GRAB NOW the label carries no information.
4. **Models collision at the list level.** X names the duplicate/PR sweep as the true first action for the whole ranking and applies a fresh-P1 rule; Y only sprinkles per-issue collision notes.

Y is not a weak output in isolation — its mechanism grounding, INVESTIGATE instruments, and execution plans are solid. It loses on discipline: restraint (empty SKIP), calibration (by-construction confidence), and ownership blindness (maintainer-owned work graded grab-able). Those are exactly the axes the rubric weights toward catching a weaker harness.

**Judge confidence: high.** Coverage was verified mechanically; the decisive gaps (empty SKIP, by-construction confidence, release-branch work in GRAB NOW) are explicit in Y's own text and map directly onto named rubric anchors and failure signatures, so the verdict does not hinge on repo access or a close weighting call.
