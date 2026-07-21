# Walkthrough: `bin/pr-state-poller` → a durable workflow

A line-by-line comparison of one hand-rolled orchestration loop against the
durable-execution primitives it reimplements. Written 2026-07-16 from a live
trace of the running poller, not from a reading of the source.

Companion to `temporal-maintenance-promotion-plan.md`, which already names this
file as the next cutover target: *"`bin/pr-state-poller` (243 ln, 15m poll of @me
PRs) + `orders/pr-state-poller.toml` → Webhook/event Signal bridge + narrow
reconciler (P5)."*

---

## Thesis

> **Hand-rolled orchestration reliably guarantees at-most-once, and silently
> abandons completion.**

Two independent loops in this city were found to have this exact shape on the
same day (2026-07-16):

| Loop | Guarantees | Silently drops |
| --- | --- | --- |
| `maintenance-cycle` (Temporal, dispatch-only) | no duplicate bead/sling | a worker crash mid-sling orphans the bead (`routed_to` never stamped), poisons the claim, and the next cycle looks healthy — see `gc-372` chaos test |
| `bin/pr-state-poller` (bash, 15m poll) | no duplicate iterate per review | a wedged `mol-pr-iterate` leaves the review marked handled forever, and the copilot comments sit unaddressed |

The second case is the sharper one, because `pr-state-poller` **was written to fix
exactly that leak**. Its order description: *"Closes the gap surfaced 2026-05-12:
copilot comments sitting unaddressed."* It closes the "review never noticed" half
and leaves the "iterate dispatched then died" half wide open.

---

## The subject

`bin/pr-state-poller`, 243 lines of bash, `orders/pr-state-poller.toml`
(`cooldown`, 15m, timeout 5m). Every tick it lists our open PRs in
`gastownhall/gascity` and `gastownhall/gascity-packs`, and for each one asks
GitHub whether copilot left a review with inline comments. If so, it slings a
`mol-pr-iterate` molecule at the rig's polecat pool.

It is healthy and running (verified: ticks at 18:53:55Z, 19:10:08Z, 19:26:18Z,
`scanned: 9`). Nothing here is a bug report about it being broken. It works. The
question is what it costs to make it work.

## The trace: PR #3958

Real data, 2026-07-16.

```
2026-07-06T01:40:15Z   copilot submits review 4632422302 (COMMENTED), 4 inline comments
2026-07-06T02:40:37Z   poller logs iterate_dispatched  (review_id=4632422302, comments=4)
                       → 60m22s from review to dispatch, on a 15-minute poll
2026-07-16T19:26:15Z   still polling this PR; cache and GitHub agree exactly;
                       nothing to do — and it has re-derived that same answer
                       every ~16 minutes for ten days
```

Steady-state cost: 9 PRs × 2 `gh api` calls × ~4 ticks/hour ≈ **72 GitHub API
calls per hour to learn nothing**.

Cache state for #3958 and live GitHub state are byte-identical:

```json
{ "copilot_comment_count": 4,
  "handled_review_ids": [4632422302],
  "reviews": [{"id": 4632422302, "state": "COMMENTED", "submitted_at": "2026-07-06T01:40:15Z"}] }
```

## Anatomy: every mechanism, and the primitive it reimplements

| `pr-state-poller` today | Reimplements |
| --- | --- |
| `.gc/pr-state-cache/*.json` → `handled_review_ids` | workflow state / idempotency |
| `existing_iterate_bead()` — scans open convoys, `gc bd show` per child, **string-matches bead titles** | workflow ID uniqueness |
| 15m `cooldown` poll | durable timer + Signals |
| `log_event` → `.gc/pr-state-poller.log` | event history |
| `orders/pr-state-poller.toml` | Schedule |

### Two idempotency mechanisms, because neither is sufficient

`handled_review_ids` is written **after** the sling. Crash in between and the next
tick re-dispatches. So a second guard was added — `existing_iterate_bead()` — whose
own comment states the reason:

> *"This guard covers the narrow window where a sling succeeded but the script died
> before the cache was written."*

It detects the duplicate by scanning every open convoy child and comparing bead
titles to a constructed marker string:

```
Iterate copilot review $review_id on $repo PR #$pr_num
```

That is a hand-rolled compensation for a lost update, implemented as title-string
matching, costing one `gc bd show` per convoy child. (`gc` peaks at ~455M RSS per
invocation — see `reference_gc_toolchain_memory_floor`.)

A workflow ID makes both mechanisms disappear. `pr-iterate/{repo}/{pr}/{review_id}`
either exists or it does not; a duplicate start is a no-op decided server-side.
There is no window to compensate for.

### The cache is unbounded and never collected

**174 cache files for 8 open PRs.** Once a PR closes it drops out of
`gh pr list --state open`, so its file is never touched again — 166 tombstones,
the oldest frozen since 2026-05-12. Nothing GCs them. This is *memory*, not a
query, and nothing reconciles it.

## Defects found by this trace

1. **`copilot_comment_count` was PR-wide but gated per-review** — **FIXED
   2026-07-16**, test `bin/pr-state-poller.test`.
   `poll_pr` fetched `/pulls/{pr}/comments`, which returns **all** copilot inline
   comments on the PR, then used that count to decide whether a *specific* review
   `$rid` had actionable feedback. On a PR where an earlier review left 4 comments,
   a later summary-only review saw `count=4` and dispatched a spurious iterate.
   The code comment said "Count inline comments per copilot review"; the code did
   not. Fix: emit each comment's `pull_request_review_id` (streaming `--jq`, so
   `--paginate` stays correct) and count per review.

   Proven by fault injection, not inspection. Fixture: PR #9001, review 111 with 4
   inline comments, review 222 summary-only. Against the **old** logic:

   ```
   {"event":"iterate_dispatched","pr":9001,"review_id":"111","comments":4}
   {"event":"iterate_dispatched","pr":9001,"review_id":"222","comments":4}   <-- spurious
   → 2 slings, the second: --var feedback_ref=review:222
   ```

   Review 222 inherited 111's count and would have woken a polecat to "apply fixes
   per the copilot review" against a review with nothing to fix. Post-fix: 1
   dispatch, 1 sling, review 222 logged `skip_no_inline_comments`. Verified against
   live GitHub data too — PR #3958's real comments yield 4× `4632422302`, so
   per-review and PR-wide coincide there (one review), which is precisely why this
   bug needed a two-review fixture to surface.

2. **Latent: a review seen before its comments land is dropped forever.** If
   GitHub exposes the review object before its inline comments, `poll_pr` takes the
   `copilot_comment_count == 0` branch, which marks the review handled
   *permanently* and skips it. No evidence this has occurred — both live
   0-comment cases (#4322, #4310) are genuine summary-only reviews. The race is
   real; the sighting is not. Filtering per-review (defect 1) narrows but does not
   close it; only a re-check would.

3. **Fire-and-forget past dispatch.** Once `dispatch_iterate` returns 0,
   `handled_ids` advances and nothing revisits. There is no timeout, no completion
   check, no state past dispatch. If `mol-pr-iterate` wedges (a known failure mode
   — `reference_mol_pr_iterate_pool_wedge`), the review stays handled and the
   comments sit unaddressed, which is the original leak returning by another door.

Defect 3 is the one bash cannot fix without becoming a workflow engine.

## The `after`

One workflow per review. **ID = `pr-iterate/{repo}/{pr}/{review_id}`.**

```
PRIterateWorkflow(repo, pr, review_id):
    comments = GetReviewComments(repo, pr, review_id)   # per-review; fixes defect 1
    if comments == 0: return skipped
    bead = DispatchIterate(...)        # returns bead id; never blocks on the agent
    await Signal(iterate.done) | timer(N hours)
    on timeout: reconcile bead state -> escalate        # closes defect 3
    record evidence
```

That ID alone deletes `handled_review_ids`, the 174-file cache,
`existing_iterate_bead()`'s convoy scan and title matching, and the crash window.

**Keep the poll — demote it.** It stops being the dispatcher and becomes the
reconciler: *"any copilot review with no workflow? start one."* Events give
latency; reconciliation gives completeness. Events get lost at integration
boundaries, so the scan must survive. What dies is the *memory*, not the scan.

### Three constraints

1. **Never block an activity on `mol-pr-iterate`.** It is a tmux agent session, not
   a function call. Dispatch, return the bead id, then wait on a Signal or
   reconcile. Sessions are external managed resources.
2. **Do not route this through `RealAdapter`/`execstore`.** Those are at-most-once
   fail-closed: a crash mid-side-effect leaves a `pending` claim that is refused
   forever (`TerminalExecError, retryable: false` — proven by the `gc-372` chaos
   test). Correct for slinging a maintenance bead; wrong here, where re-dispatch is
   idempotent by workflow ID and silence is the failure being replaced.
3. **No webhook intake exists.** Start with P3's `cishim` (gh-REST → Signal) and
   `reconciler.go` — both written, tested, and dormant because dispatch-only needed
   no signals. This workload is what justifies them.

### What this buys, honestly

| Win | Available when |
| --- | --- |
| Delete 2 idempotency mechanisms + 174 cache files + the crash window | immediately (workflow ID) |
| Escalate a wedged iterate instead of dropping it (defect 3) | immediately (durable timer) |
| Event history replaces `log_event` | immediately |
| 60min → seconds dispatch latency | **only with webhook intake**, which does not exist |

With the gh-REST shim and no webhooks, latency stays at roughly the poll interval.
The durability wins are real now; the latency win is not. Claiming otherwise would
be the same overstatement that made `maintenance-cycle` look like a Temporal win
when it was a 44-second job that needed cron and a lockfile.

---

## Re-verify

```bash
cd /home/ds/gas-city
# the trace
jq . .gc/pr-state-cache/gastownhall-gascity-3958.json
grep '"pr":3958' .gc/pr-state-poller.log
gh api repos/gastownhall/gascity/pulls/3958/reviews \
  --jq '[.[] | select(.user.login|test("copilot";"i")) | {id,state,submitted_at}]'
# cache growth vs open PRs
ls .gc/pr-state-cache/ | wc -l
gh pr list --repo gastownhall/gascity --author '@me' --state open --json number --jq '.[].number' | wc -l
# poller liveness
tail -3 .gc/pr-state-poller.log
gc order history pr-state-poller
# the per-review fix
bash bin/pr-state-poller.test
```
