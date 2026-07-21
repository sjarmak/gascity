# jn73.11 Results-Section Design: EnterpriseBench Retrieval Paper

Date: 2026-07-11. Author: research-writing strategist pass (read-only survey).
Scope: design only; no paper edits. Target artifact: `paper/paper.md` Results
section (currently §5 on main; §6 on the jn73.10 methodology branch
`worktree-agent-a4783f06e62cc54fd @57cfe8e9`, which renumbers and adds the
three `TODO(jn73.11)` placeholders this design fills).

## 0. Evidence state (grounding, verified today)

- **Metric axis (jn73.1):** recall/precision/F1@k, MRR, nDCG, MAP,
  file_recall, context_efficiency landed on main (`42763fe`); per-config
  rollup with matched-telemetry filtering landed (`286101d`).
  localization@1/@k + TTFR (turns, tokens, seconds) are branch-ready on
  `worktree-agent-a2d746ee838a4052a @27f81da`, awaiting Stephanie sign-off
  (scoring-axis change). 87 tests pass; hand-verified on 3 real trials.
- **Back-compute over existing runs:** 82 trials non-null recall; 74 TTFR ok;
  8 `not_found`, all with `file_recall == 0.0` (consistent misses, not
  extraction failures).
- **Coverage gap:** 86/212 (41%) `ground_truth.json` files use the older
  mining schema (`fix_files`/`producer_changed_files`, no
  `required_files`/`sufficient_files`) and correctly yield `None`. Retrieval
  coverage is partial until GT backfill.
- **ewr8 (P0, open):** `cost_tracker.py` sums usage per content-block line
  with no requestId dedup; overcount 1.34x–3.03x, **arm-correlated and
  direction-inconsistent**. All published cost/efficiency figures (including
  current §5.6's "MCP modes use 23–33% fewer tokens") inherit the inflation.
  Reward figures are unaffected. jn73.1's TTFR dedup took FIRST-per-request;
  correct cost basis is LAST/max-output; reconciliation pending. Until then,
  token-TTFR and all token/cost comparisons are embargoed.
- **Run set:** 326 results, 108 tasks matched across baseline/mcp_only/hybrid.
  `mcp_only` is a no-local-checkout regime by construction (empty workspace,
  MCP mandatory), not "baseline plus MCP"; hybrid is the parity-matched arm.
- **Levers (.4–.8):** none run yet. Stratum labeling (.2, Gates A–F) pending;
  it defines the tier-1 (b_grep ≥ 20) / tier-2 (12–19) / excluded (≤ 7)
  regimes and the 3-arm study (baseline / stock-MCP default endpoint /
  CLI via src-cli OAuth), n ≥ 3, split by {investigation, execution} × tier.
- **CSB priors (motivating, not EB results):** r = −0.57 (cost delta vs
  grep-breadth); sgx −30.9% cost at reward parity on the enterprise stratum
  (t = −2.89, 9/9 at b_grep ≥ 20); batch-v1 +0.042 reward on grep-trivial
  only; leak-bucket → retrieval-win monotonic 0/4/28/32%; +24% fair-basis
  cost gap. Source: CSB `MORNING_LEDGER_2026-07-10.md`. These enter the EB
  paper only as stated predictions, clearly attributed to CSB.

---

## 1. The claim ladder

Two orthogonal gates cut across all tiers:

- **Gate E (ewr8):** unlocks every token/cost column and token-TTFR at
  whatever tier is current. Before it: seconds-TTFR, turns-TTFR, recall,
  localization, and reward are the only reportable quantities.
- **Gate B (GT backfill):** unlocks full-corpus N. Before it: every retrieval
  metric carries its own N and a coverage line (126/212 GT-scoreable).

### Tier 0 — axis-validated (available at submission of the branch, needs sign-off + merge)

The paper can assert:

1. **The instrument exists and is internally consistent.** Localization@1/@k
   and TTFR are computed against EB's oracle on EB's native traces;
   hand-verified on real trials; the TTFR "found" predicate shares recall's
   path-alignment, so the two metrics cannot disagree; the 8/82 `not_found`
   cases all have `file_recall == 0.0`, exactly the co-occurrence a correct
   implementation predicts.
2. **Distributional facts on the existing 108-matched run set** (matched
   telemetry only, per-metric N): per-arm file_recall, localization@1/@5,
   MRR, context_efficiency, turns-TTFR, seconds-TTFR. These are descriptive:
   "arm X localizes GT files in fewer turns," not "retrieval helps."
3. **The unstratified aggregate is signed but uninterpretable.** The existing
   §5.4 negative MCP deltas (−0.111 hybrid, −0.116 mcp_only, both p < 0.003)
   stand as reward facts, reframed per §3 methodology: a corpus-wide number
   on a corpus whose leakage census and grep-breadth strata are not yet cut
   answers the wrong question. This is the motivating exhibit for the
   regime-conditional design, not a finding about retrieval.
4. **Stated predictions, attributed to CSB:** cost delta should track
   grep-breadth (r = −0.57 prior); interface form (CLI vs MCP vs batch)
   should matter more than index access; wins should concentrate at
   b_grep ≥ 20. Written as predictions before EB numbers arrive.

The paper can NOT assert at this tier: any cost/efficiency comparison (ewr8),
any token-TTFR number (ewr8), any per-regime effect (no strata cut), any
lever lift (no runs), full-corpus retrieval coverage (backfill).

### Tier 1 — levers run (.2 strata cut + .4/.5 evaluated; .6–.8 optional adds)

Adds, always per-regime and paired:

1. **The EB arm × regime matrix** (fills TODO #3, §3.7): reward columns
   immediately; cost columns only post-ewr8. Grep-breadth cut points and
   per-stratum N (fills TODO #1, §3.2) come from the .2 calibration run.
2. **Per-lever lifts** with the acceptance-criteria framing each bead
   already fixes: .5 hybrid-RRF recall@k + paired reward on the poorly-named
   stratum; .4 working-set cost at reward parity (post-ewr8) with
   tokens-in-context as the pre-ewr8 proxy denominated in deduped counts
   only; .6 rerank localization@k delta with p-value and an explicit
   ship/no-ship record; .7 outline/graph-nav cost + recall on the
   cross-repo stratum; .8 forced-isolation tokens-in-main-context.
3. **The routing rule**, the section's deliverable: which arm minimizes cost
   at reward parity within each regime, and where the crossover falls.
4. **Mediation evidence:** within-task, retrieval-quality deltas
   (localization, TTFR-turns) regressed against reward/cost deltas. This is
   the claim that turns "retrieval quality moved" into "retrieval quality
   matters."

### Tier 2 — backfill done + ewr8 reconciled

Adds: full-corpus per-metric N (212-scoreable target); token-TTFR alongside
turns/seconds; the per-regime cost split and cache-creation/read/output
attribution (fills TODO #2, §3.4) on a verified subagent-inclusive basis;
re-computed replacements for every previously published cost figure, with a
one-line erratum note that pre-correction figures were inflated 1.34x–3.03x
non-uniformly by arm.

---

## 2. Table and figure inventory

Conventions binding every entry: matched telemetry only (task present in
every compared arm; repeats averaged per (config, task) cell first, per
`retrieval_rollup.aggregate_retrieval`); per-metric N printed in the table,
never only in a footnote; paired per-task deltas, arm means never compared
directly; every effect carries its regime; `None` never coerced to 0.

**T1 — Retrieval quality by arm (Tier 0).**
Rows: baseline, mcp_only, hybrid. Columns: file_recall, localization@1,
localization@5, MRR, context_efficiency, TTFR-turns (median), TTFR-seconds
(median), N-recall, N-TTFR-ok / not_found / unavailable.
Feeds: back-compute over the existing 108-matched run set (needs 27f81da
merged; needs `retrieval_rollup.MeanRetrieval` extended to surface
localization/TTFR, a known deferred item on jn73.1).
Supports thesis if: MCP/CLI-tooled arms show higher localization@1 and lower
TTFR-turns than baseline on the same tasks.
Falsifies if: baseline matches localization/TTFR (tooling confers no
localization ability, so any downstream effect is not retrieval-mediated), or
tooled arms over-fetch (context_efficiency drops with no localization gain).

**T2 — Coverage and accounting (Tier 0; permanent fixture, updated per tier).**
Rows: GT files total (212), new-schema scoreable (126), old-schema None (86),
trials with non-null recall (82), TTFR ok (74), TTFR not_found (8, all
file_recall 0.0), TTFR unavailable (no_tool_calls), matched-3-arm task N
(108). Not a thesis table; it is the instrument-integrity exhibit and the
anchor for the limitations paragraph. Supports nothing; its job is to make
cherry-picking structurally impossible.

**T3 — Grep-breadth strata (Tier 1; fills TODO #1).**
Columns: stratum (excluded ≤ 7 / tier-2 12–19 / tier-1 ≥ 20), cut-point
justification, task N per stratum, N per suite, investigation vs execution
split. Feeds: .2 baseline calibration run (turns-TTFR from the axis is the
grep-breadth measure, so this table is downstream of T1's machinery).
Supports if: EB's corpus has enough tier-1 mass (target per Gate C/D) to
power per-regime pairs. Falsifies nothing; if tier-1 N is small, the paper
says so and scales claims to "directional."

**T4 — EB arm × regime matrix (Tier 1; fills TODO #3; the headline table).**
Rows: arms actually run in the 3-arm study (baseline / stock-MCP default
endpoint / sgx-CLI OAuth; hybrid as parity-matched reference where
available). Columns, per regime {grep-trivial excluded-for-reference, tier-2,
tier-1} × {investigation, execution}: paired reward delta (mean, Wilcoxon p,
win/loss/tie counts), paired cost delta (post-ewr8 only; column printed as
"embargoed, ewr8" until then), N pairs.
Supports if: some retrieval arm reaches cost reduction at reward parity
within tier-1 (the CSB prediction: sgx-like arm, 9/9 at b_grep ≥ 20), while
grep-trivial shows the known baseline advantage.
Falsifies if: no arm beats baseline on either axis even in tier-1 execution
AND investigation; then the honest conclusion is that in EB's regime the
conditional story does not transfer, and the paper reports that.
Note the arm-design caveat inline: mcp_only deltas are ablation deltas
(no-local-checkout), not interface deltas; the CLI and hybrid rows carry the
interface claim.

**T5 — Per-lever lift table (Tier 1; one row per lever, grows as runs land).**
Columns: lever, target regime, primary metric (from the bead's acceptance
criteria), paired delta, p, N, verdict (ship / no-ship / mechanistically
explained). Rows: .4 working-set (investigation; cost at reward parity;
pre-registered ceiling: retrieved payload ~12% of cache_read, so a null here
confirms the reasoning-re-anchoring mechanism rather than embarrassing the
thesis; write that down before the run); .5 hybrid-RRF (poorly-named;
recall@k lift AND positive paired reward, the only lever predicted to move
BOTH cost and reward); .6 rerank (localization@k delta with p; SciX
cross-encoder null is the stated expected-failure prior; gate, do not ship
on faith); .7 structured nav (cross-repo stratum; cost + recall vs raw-read;
regex-outline regression is the stated prior); .8 isolation (investigation;
tokens-in-main-context; the lean-subagent 3-run null is the stated prior,
forced isolation is what is new).
Supports if: ≥ 2 levers clear their gates in their target regimes (the
acceptance bar on jn73.11 is ≥ 2 product-improvement lifts).
Falsifies if: recall/localization lifts occur without reward or cost
movement anywhere; that pattern says retrieval quality is epiphenomenal to
effectiveness in EB, the strongest possible falsification of the thesis, and
the section must be structured so it could print it.

**F1 — Localization/recall distributions by arm (Tier 0).**
Violin or ECDF of file_recall and localization@5 per arm, matched telemetry,
N in caption. The distribution shape matters: a bimodal recall (0 or 1)
supports the "GT-miss semantics" reading and motivates TTFR as the
finer-grained signal.

**F2 — Paired delta vs grep-breadth (Tier 1; the r = −0.57 replication).**
Scatter: x = baseline grep-breadth (turns-TTFR), y = paired cost delta
(post-ewr8) or paired turns delta (pre-ewr8 proxy), one point per task, arm
color-coded, crossover marked. This is the single figure that carries the
conditional story. Supports if the slope is negative and the crossover falls
inside the corpus range; falsifies if flat.

**F3 — TTFR by arm per regime (Tier 0 descriptive, Tier 1 inferential).**
Box plots of turns-TTFR and seconds-TTFR per arm within regime. Token-TTFR
joins only at Tier 2. Supports if tooled arms reach first relevant file
earlier specifically in tier-1; a uniform TTFR advantage that does not
concentrate in tier-1 weakens the regime story even if it flatters the tool.

**T6 — Cost decomposition (Tier 2; fills TODO #2).**
Per regime: cache-creation / cache-read / output / uncached-input attribution
of the paired cost delta, subagent-inclusive basis verified first (jn73.11
notes: `rglob('agent_trace.jsonl')` subagent inclusion is an open question;
resolve before printing). Supports if the delta driver replicates CSB's
accumulated-reasoning re-creation (cache-creation dominant); a
payload-dominant delta would instead support the working-set lever (.4) as
the right fix. Either outcome is informative; say so in the section.

Existing §5.6 cost text and the "MCP modes use 23–33% fewer tokens" claim are
retracted/flagged at Tier 0 and replaced only at Tier 2.

---

## 3. Honest-limitations paragraph (content, draft-ready)

Write as one subsection with four numbered limitations; each states the
fact, the direction of bias it could introduce, and the remediation status.

1. **Retrieval coverage is partial.** 86 of 212 ground-truth files (41%)
   predate the current mining schema and carry no required/sufficient file
   sets; the metric axis correctly yields None for them, so every retrieval
   metric in this paper is computed over the 126-file scoreable subset and
   reports its own N. The old-schema subset is not a random sample (it
   correlates with authoring era and suite mix), so per-suite retrieval
   comparisons are directional until the GT backfill completes. No retrieval
   number in this paper silently treats a missing oracle as zero.
2. **Cost figures are embargoed pending a token-accounting correction.**
   EB's trace logs one line per assistant content block with repeated
   per-request usage; naive summation inflates token counts 1.34x–3.03x,
   non-uniformly across arms within the same task, which distorts arm-to-arm
   cost deltas in inconsistent directions. Reward results are unaffected.
   All cost, efficiency, and tokens-to-first-relevant comparisons are
   deferred until the deduplication fix (last-record-per-request basis) is
   reconciled across the cost tracker and the metric axis, and previously
   circulated EB cost figures should be considered superseded.
   Turns-to-first-relevant and seconds-to-first-relevant do not depend on
   token accounting and are reported now.
3. **GT-miss semantics.** 8 of 82 back-computed trials return TTFR
   `not_found`; all 8 also have file_recall 0.0. We read these as genuine
   retrieval misses (the agent never touched a ground-truth file), not
   instrumentation gaps: the TTFR found-predicate shares recall's path
   alignment by construction, and trials with no tool calls are flagged
   `ttfr_unavailable`, a distinct class. Misses are reported in every N
   breakdown rather than dropped.
4. **Arm design limits interpretation.** `mcp_only` is a no-local-checkout
   ablation (empty workspace, MCP mandatory), so its deltas measure
   MCP-as-sole-access, not MCP-added-to-a-checkout; hybrid is the
   parity-matched comparison, and the CLI arm uses the customer-realistic
   OAuth device flow. Regime effects are reported per arm design and never
   pooled across designs.

---

## 4. Regime-conditional framing contract (the writing rules for §Results)

1. **No universal sentence.** The section never contains "retrieval
   helps/hurts agents" without a regime qualifier. The unit of assertion is
   (arm, regime, metric, N, p).
2. **The deliverable is a routing rule, not a winner.** Frame the headline
   as: which arm to route to, keyed on localization difficulty, with the
   crossover point. "Baseline remains correct for grep-trivial work" is a
   finding, printed with the same prominence as any tool win.
3. **Matched complete telemetry only.** Every comparison uses tasks present
   in all compared arms; repeats collapse to one cell per (config, task); no
   subset is reported without its selection rule stated in the caption. The
   rollup's matched filter is the mechanical enforcement; the text never
   quotes a number computed outside it.
4. **Aggregate as motivation.** The existing negative MCP deltas appear once,
   early, as the exhibit for why stratification is required, with the §3
   methodology cross-reference. They are not re-litigated per subsection.
5. **Predictions before numbers.** Each table's support/falsify pattern
   (Section 2 above) is written into the draft as a short pre-registration
   sentence ("this table supports X if..., and we report the contrary
   pattern if it appears") so the section is complete before runs land and
   no result forces a framing rewrite.
6. **Priors are attributed.** CSB numbers (r = −0.57, −31%, +24%, leak
   monotonicity) are always "CSB measured," never restated as EB findings.

---

## 5. Executive summary

1. The results section is built as a claim ladder: Tier 0 (axis merged) gets
   instrument validity + per-arm retrieval-quality descriptives + the
   negative aggregate reframed as motivation; Tier 1 (.2 strata + .4/.5
   runs) gets the arm × regime matrix, per-lever lifts, and the routing
   rule; Tier 2 (ewr8 + GT backfill) gets all cost/token columns and
   full-corpus N.
2. Two gates cut across tiers: ewr8 embargoes every token/cost number
   (token-TTFR included; turns/seconds-TTFR, localization, recall, reward
   are clean now), and the 86/212 old-schema GT gap forces per-metric N in
   every table until backfill.
3. Six tables + three figures are specified with metrics, axes, N sources,
   and feeding runs; each carries a pre-registered support-vs-falsify
   pattern so the section is writable today without prejudging numbers.
4. The limitations paragraph has four fixed planks: partial coverage (with
   non-random-subset caveat), the dedup embargo (with supersession of prior
   cost figures, §5.6 included), GT-miss semantics (8/8 recall-0
   consistency), and the mcp_only ablation-not-interface caveat.
5. Framing contract: every claim is (arm, regime, metric, N, p); the
   headline is a routing rule with a crossover, not a winner; matched
   telemetry is mechanically enforced by the rollup, never hand-selected.

**The one decision Stephanie must make:** whether the Results section leads
with the regime-conditional routing rule and demotes the existing aggregate
negative MCP delta (current §5.2–5.4) to a single motivating exhibit. Saying
yes commits the paper's headline to Tier 1 evidence (the .2 stratum labeling
and at least the .4/.5 runs become publication-blocking) and fixes the
canonical arm set for every table (baseline / stock-MCP default endpoint /
sgx-CLI OAuth, hybrid as parity reference); saying no lets the paper ship at
Tier 0 as a benchmark-plus-instrument paper with the conditional story as
stated predictions. Everything in the inventory hangs off that choice; it
should be made before jn73.11 drafting starts.
