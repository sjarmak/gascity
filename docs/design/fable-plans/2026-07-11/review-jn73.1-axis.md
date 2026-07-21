# Adversarial review: jn73.1 retrieval-metrics axis (localization@1/@k + TTFR)

Target: branch `worktree-agent-a2d746ee838a4052a` @ `27f81da`, worktree
`/home/ds/projects/EnterpriseBench/.claude/worktrees/agent-a2d746ee838a4052a`.
NOT merged to main (main HEAD `8a826b2` does not contain it). Repo untouched;
all probes under `/tmp/claude-1000/-home-ds-gas-city/850a60c5-e442-425e-a67b-6f76145c17b1/scratchpad/`
(`probe_ttfr_dedup.py`, `probe_notfound.py`, `probe_bash_split.py`,
`probe_skew.py`, `probe_metrics_norm.py`).

Test suite: 87/87 pass
(`PYTHONPATH=lib:/home/ds/projects/codeprobe/src python3 -m pytest tests/eb_metrics/ -q`
inside the worktree). The 82-trial back-computation reproduces exactly:
82 scorable, 74 `ok`, 8 `not_found`.

Verdict: **do not merge as-is, and do not put the current back-computed
numbers in the paper.** Two extraction bugs silently misscore 9+ trials in an
arm-correlated direction, the "8 not_found = genuine miss, zero anomalies"
validation claim is false for at least 3 of the 8, and the ewr8 landing
condition (LAST-per-request token dedup) is unmet.

---

## CONFIRMED findings (severity order)

### C1 (HIGH, silent misscore) — `_bash_read_files` splits inside quotes; `grep "a\|b" file` loses the file

`_SHELL_SPLIT_RE = re.compile(r"\|\||&&|[|;&]")`
(`lib/eb_metrics/retrieval_extraction.py:129`) is applied to the raw command
string before tokenization (`:142-170`), so a quoted grep alternation is
severed mid-pattern and the file argument lands in a sub-command whose
"program" is the pattern tail — dropped.

Unit repro:

```
_bash_read_files('grep -n "create_public_stream_policy\\|can_create_public_channel_group" /workspace/zulip/zerver/models/realms.py | head -40')
→ ['"create_public_stream_policy\\']        # realms.py LOST
```

Empirical blast radius (probe_bash_split.py / probe_skew.py, all 82 trials):
**18/82 trials lose verified required-file reads** through this channel.
Rescoring with quote-aware tokenization (shlex first, then split on operator
tokens):

- `file_recall` changes on 7 of them, e.g. `schema-evolution-010/hybrid`
  0.000 → 1.000, `schema-evolution-003/baseline` 0.000 → 0.500,
  `ccx-incident-032/baseline` 0.200 → 0.600.
- `localization@1` flips 0 → 1 on 4 trials.
- TTFR turns wrong on 8 trials (worst `schema-evolution-008/baseline`
  36 → 15), coverage flips `not_found` → `ok` on 2.

Repro: `python3 probe_skew.py`. This channel predominantly hits the
**baseline/hybrid** arms (grep-heavy), so the error is arm-correlated.

### C2 (HIGH, silent misscore) — MCP `read_file` drops the `repo` input field; multi-repo GT paths can never match

`_extract_paths_from_tool_use` takes only `tool_input["path"]` for
`_MCP_READ_TOOLS` (`retrieval_extraction.py:233-236`). Tri-repo tasks carry
GT paths prefixed with the repo directory (`requests/setup.cfg`), while MCP
reads are repo-relative (`repo="github.com/sg-evals/requests--v2.31.0"`,
`path="setup.cfg"`). `_path_matches_required` requires the *required* path to
be a component-suffix of the *retrieved* one — the retrieved path is shorter,
so the hit is structurally impossible.

Repro (probe_bash_split.py, `dep-graph-tri-boto3-urllib3-requests-001/mcp_only`):
the agent read `urllib3 CHANGES.rst`, `requests setup.cfg` (and more) via
`mcp__sourcegraph__read_file`; every one scored `counted as hit: False`.
Trial scored `file_recall 0.0` / `not_found`; correct is `file_recall 1.0`,
coverage `ok`, turns 7. This channel hits only the **mcp_only/hybrid** arms —
the opposite arm from C1, i.e. both arms are wrong in different directions.

### C3 (falsified validation claim) — "8 not_found, every one a genuine GT-miss, zero anomalies" is wrong for ≥3 of 8

The commit message and `test_compute_ttfr_not_found_is_a_genuine_miss` frame
`not_found`+`file_recall==0.0` correlation as validation. It is circular:
TTFR's found-predicate deliberately mirrors recall's extraction
(`_path_matches_required`), so a shared extraction blind spot produces a
consistent (0.0, not_found) pair that *looks* anomaly-free. Per-trial audit
(probe_notfound.py):

| trial | verdict |
|---|---|
| `runs/schema-evolution-003/baseline` | **extraction failure** (C1: grep'd `zerver/models/realms.py`, `zerver/lib/events.py`, `zerver/lib/event_schema.py` directly) |
| `runs/schema-evolution-010/hybrid` | **extraction failure** (C1: grep'd all 3 required files) |
| `runs/dep-graph-tri-boto3-urllib3-requests-001/mcp_only` | **extraction failure** (C2: MCP-read 3/3 required files) |
| `runs/camel-routing-arch-001/baseline` | miss-by-definition: required files located via Explore subagent + `find`; final `answer.json` lists exactly the required paths. Not a *retrieval* extraction bug, but "the agent never found the file" is untrue. |
| `runs/schema-evolution-tri-supabase-001/hybrid` | similar: required files read inside Explore subagents (invisible in the parent trace) |
| remaining 3 | plausibly genuine misses (required basenames appear only in search-result noise / answer text) |

Combined C1+C2 skew over the 82-trial back-computation: mean `file_recall`
0.6494 → 0.7020 (+5.3 points), and the per-arm deltas move in *different*
directions per arm — this directly distorts the paper's MCP-vs-baseline
retrieval comparison.

### C4 (HIGH by landing condition; small numerically for TTFR) — token dedup is FIRST-per-request; ewr8 requires LAST/max-output; the tests cannot detect the difference

- Landed logic (`retrieval_extraction.py:646-656`) counts a `requestId`'s
  usage on its **first** line. ewr8 (P0, open) prescribes **last/max-output**
  per request and explicitly names this branch as needing reconciliation on
  one shared helper with `scripts/cost_tracker.py`.
- The docstring premise at `retrieval_extraction.py:542-558` — lines of one
  request carry "the identical (non-incremental) usage snapshot" — is
  **empirically false** for `output_tokens`. Across all real traces
  (probe_ttfr_dedup.py): input/cache identical within group in 100% of
  multi-line groups (0 violations), but `output_tokens` streams upward and is
  max on the last line in 100% of groups (0 exceptions) — exactly ewr8's
  model, and the 140-vs-2589 example.
- Quantified skew of FIRST vs LAST at the first-relevant step:
  `tokens_to_first_relevant` differs on **24/74** ok-coverage trials,
  undercount ≤ **2.6%** (median 0%) — small because input+cache dominate at
  that point. Whole-trace totals: FIRST undercounts LAST by ≤2.0%; naive
  per-line summation overcounts LAST by 1.22–4.30x (median 2.54x), confirming
  the dedup itself was necessary and directionally right.
- Turns and seconds are **unaffected** by dedup choice: identical on all 74
  trials under both variants (they never depended on usage).
- Test gap: both `test_compute_ttfr_dedupes_tokens_by_request_id`
  (`tests/eb_metrics/test_retrieval_extraction.py:467`) and the hand-verified
  integration trial (`:570`) construct requestId groups with *identical*
  snapshots, so FIRST and LAST produce the same expected values — the suite
  passes under either implementation. ewr8's acceptance criterion (a
  regression test on a streaming multi-content-block group) is not met.

Condition status: **unmet.** The fix is one-line-ish (track last-seen usage
per requestId, or max output) plus the discriminating test, plus extraction
into a helper shared with `cost_tracker.parse_trace`.

### C5 (MEDIUM) — no aggregation path exists for the new axis; None policy undefined

`retrieval_rollup.py` `_SCALAR_FIELDS`/`_DICT_FIELDS` (lines 45-46) exclude
`localization` and all four TTFR fields; no script in the repo consumes
`detail['retrieval_metrics']` or any `ttfr_*`/`tokens_to_first_relevant`
field (grep over `scripts/`, `lib/`, codeprobe's `trace_quality.py` — the
latter is per-row only). So the "authoritative headline measurement" has no
committed aggregation: any paper mean is hand-computed, and the
None-propagation question (do the 8 `not_found` Nones drop out of the TTFR
mean, or count in the denominator?) is unanswered *anywhere in code*.
Given C1–C3, a mean over the `ok` subset inherits an arm-correlated selection
bias (the excluded trials are disproportionately baseline-arm C1 victims and
mcp-arm C2 victims). Whoever computes the headline needs: (a) fixed
extraction first, (b) an explicit reported-coverage denominator
(e.g. "TTFR over N=74/82 trials with ok coverage, per arm").

### C6 (MEDIUM-LOW) — `normalize_path` lead-strip manufactures cross-directory equality

`ir_metrics.py:96-99` strips any dot-free first segment not in `_CODE_DIRS`.
Synthetic repro (probe_metrics_norm.py):
`compute_ir_scores(["frontend/actions/x.py"], ["zerver/actions/x.py"], ...)`
→ `file_recall 1.0`, `localization@1 1.0` — a plausible-but-wrong perfect
score from a path in the wrong tree. Also collapses required-set entries:
`["appl/actions/x.py", "zerver/actions/x.py"]` → `n_ground_truth == 1`.
Real-data incidence today: **5/183** hits rest solely on lead-strip equality
(inspected: all look like true matches accidentally rescued from C2's repo
drop — e.g. `rules/alerting.go`, `tonic/Cargo.toml`), and **0** GT
normalized-key collisions in current benchmarks. So: no wrong number in the
82 trials today, but the primitive is a standing false-positive channel and
currently load-bearing as an accidental C2 mitigation. Fixing C2 properly
removes the need to lean on it.

---

## PLAUSIBLE concerns (not blocking, should be worded/decided)

- **localization@k semantics vs docstring**: retrieved lists include paths
  *scraped from MCP search-result payloads*, not only opened files. The
  docstring's "did the agent open a correct file within its first k reads"
  overstates; a search result that merely lists a GT file counts as a hit at
  its scrape rank. Fine as a definition, but the paper text must say
  "surfaced," not "opened."
- **`a/`/`b/` diff-prefix strip** (`ir_metrics.py:101-102`) eats real
  top-level directories named `a` or `b` (demo: `mrr(["b/y.py"], {"b/y.py"})`
  → 0.0 because the retrieved side normalizes to `y.py`). No such directory
  in current GT; latent only.
- **TTFR seconds anchor**: hits registered via a tool_result use the issuing
  tool_use's timestamp, slightly undercounting wall time to the result; t0 is
  the first timestamped line of any type. Consistent across arms; harmless.
- **`_result_text` on dict payloads** produces Python repr (single quotes),
  which the `"path": "..."` regexes never match — a small scrape-observability
  loss for tool_result content delivered as a bare dict.

---

## All-clear (actively tested, sound)

- **Metric definitions** (probe_metrics_norm.py): `localization_at_1 ≡
  localization_at_k(k=1) ≡ precision_at_k(k=1)` over 20,000 fuzz trials, 0
  mismatches. `localization_at_k` monotone non-decreasing in k (0 violations),
  and at k > len(retrieved) equals the any-hit indicator. The
  `mrr >= 1/k` implementation has no float-precision failure for
  rank==k up to k=2000. MRR uses the correct 1-based rank; nDCG's ideal DCG
  uses `min(k, |relevant|)`; `k<=0` and empty-relevant guarded (localization
  0.0, documented divergence from recall/MAP's vacuous 1.0 — unreachable via
  `compute_run_ir_scores`, which returns None on empty GT).
- **None-not-zero discipline**: missing trace → `no_trace`; empty GT →
  `no_ground_truth`; zero tool calls → `no_tool_calls` (+`ttfr_unavailable`
  flag in the adapter); no usage telemetry → `tokens_to_first_relevant is
  None` (not 0); no timestamps → `ttfr_seconds is None`. All verified by the
  suite and honored on real traces. `compute_run_ir_scores` returns None (not
  vacuous scores) on no-signal; GT resolution succeeded for all 82 scorable
  trials, misses are skipped with a counted drop reason, never zero-scored.
- **Malformed input**: garbage JSONL lines, non-dict entries, string
  tool_input, bool-typed usage values are all tolerated without fabricating
  numbers (tests + code paths read).
- **Dedup necessity and direction**: naive per-line usage summation
  overcounts by 1.22–4.30x (median 2.54x) on real traces — the requestId
  dedup itself is correct and required; only the FIRST-vs-LAST choice and the
  shared-helper reconciliation are wrong/missing (C4).
- **Turns/seconds**: unaffected by any dedup variant (identical on 74/74).
- **Rollup arithmetic** (pre-existing fields): equal-weight cell → config
  means, stable k-key union; matched-telemetry filtering behaves as
  documented.

## Recommended remediation order

1. Fix C1 (tokenize-then-split in `_bash_read_files`) and C2 (join
   `read_file`'s `repo` tail — segment before `--` — with `path` as a second
   candidate). Both have ready-made regression cases from the trials above.
2. Fix C4 per ewr8: last/max-output-per-request in ONE helper shared with
   `cost_tracker.parse_trace`; add a streaming-usage requestId-group test
   (differing `output_tokens` across lines) that fails under FIRST.
3. Re-run the 82-trial back-computation; restate the not_found table (expect
   ~5/82, not 8/82) and regenerate any recall/localization/TTFR numbers.
4. Decide and implement the aggregation denominator policy (C5) before any
   headline mean leaves the repo.
