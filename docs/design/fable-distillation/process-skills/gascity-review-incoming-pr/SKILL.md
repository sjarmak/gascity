<!--
Target live path: /home/ds/.claude/skills/gascity-review-incoming-pr/SKILL.md
Date: 2026-07-06 — draft pending dr-i4v.5 consumer eval
Changes vs live skill:
1. Added evidence phases 3-6 (pin checkout, claim-vs-diff audit, correctness trace, test adequacy) — the live skill delegated all analysis and had no procedure for the maintainer's own verification.
2. Added blast-radius-the-author-missed procedure with severity grading (new regression / restored gap / needs-a-sentence).
3. Replaced adjective-based verdict with three discriminating decision questions plus an actionability bar for change requests.
4. Added calibration rules (verified/inferred/unverifiable trichotomy; no claimed-but-unexecuted verification) and one worked example from the PR #4006 golden run.
5. Compressed memory cross-reference and delegated-agent phases; approval gates (Phase 9) and Hard rules preserved verbatim from the live skill.
-->

---

name: gascity-review-incoming-pr
description: Maintainer-side review of an incoming PR from another contributor on gastownhall/gascity. Composes blast-radius + 29-rule audit + multi-model review + memory cross-reference into a single decision report (approve / request changes / close). Surfaces every GitHub action (review submission, label, merge, close) for explicit user approval before sending. Never acts on PRs we authored — those go through Julian. Fork-local skill.
---

# Gas City Review Incoming PR

> **MAINTAINER CONTEXT (2026-05-04 onward):** sjarmak holds maintainer privileges on `gastownhall/gascity`. Julian Knutsen (`julianknutsen`) is owner; sjarmak is the only other maintainer. This skill is the maintainer-side counterpart to `gascity-ship` — `gascity-ship` is for PRs we author; this skill is for PRs we review. See `feedback_maintainer_status.md` for the full guardrail set.

## When to use

- A new PR comes in from another contributor and we want a maintainer-grade review before posting feedback or merging
- `/gascity-triage` surfaces "PR #N covers issue #M" and you want to evaluate the covering PR
- An existing PR has new commits and we need to re-review

## When NOT to use

- The PR is authored by sjarmak. Our own PRs go through Julian's review — never self-review or self-merge. Use `gascity-ship` instead.
- The PR is mid-review by Julian (check `gh pr view <N> --json reviews`; if there's a pending `approved` / `changes_requested` review, defer to him).

## Invocation

```
/gascity-review-incoming-pr <PR-number-or-URL>
```

## The standard the report must meet

Every load-bearing sentence in the report must be falsifiable against the checkout. Strike-test your own draft: delete every sentence derivable from the diff text alone; what remains must still carry the decision. A report that paraphrases hunks in diff order is a summary, not a review.

## Pipeline

### Phase 1: Read the PR

```bash
gh pr view <N> --repo gastownhall/gascity --json number,title,body,author,state,files,additions,deletions,reviews,statusCheckRollup,headRefOid,baseRefName,labels
```

Abort if author is sjarmak. Note linked issues, existing reviews (defer to an active Julian review), and CI status (separate documented baseline flakes from real regressions).

### Phase 2: Memory cross-reference

Check the newest session-handoff memory for overlap with our in-flight PRs and known baseline flakes; check `feedback_mayor_filed_priority.md` (mayor-filed issues get extra merge care) and any `feedback_*` memory whose description matches the PR's paths or title. Surface hits in the final report.

### Phase 3: Pin the revisions

All verification happens against pinned SHAs, never against a drifting local main.

```bash
cd ~/gascity && git fetch origin main && git fetch origin pull/<N>/head:pr-<N>-head
BASE=$(git merge-base origin/main pr-<N>-head)
git worktree add --detach /tmp/pr-<N>-base "$BASE"        # unfixed baseline: read + run tests here
git worktree add --detach /tmp/pr-<N>-head pr-<N>-head    # PR state: run new tests here
```

**Path-tracing requirement:** a correctness claim about a file requires having opened that file at the relevant revision in this session. Cite `file:line` only for content you actually read at that SHA. A diff hunk's context lines are not evidence of what the baseline contains — confirm against `/tmp/pr-<N>-base`. Historical claims use `git show <sha>` / `git show <sha>:<path>`, not memory.

### Phase 4: Claim-vs-diff audit

The description says X — verify the diff does X, and list what the diff does that the description omits.

1. Reconcile scope: file count and per-file +/- against the metadata; name any touched file the body never mentions.
2. Verify every factual claim in the PR body against the checkout: a referenced commit exists and its message/diff says what the author says it says (`git show <sha>`); a referenced mechanism ("issue #M's fallback covers this") exists at the claimed location and actually covers the claimed path.
3. **Audit claims inside newly added comments and doc text.** These ship into the codebase and are the easiest place for a false claim to hide. A comment asserting coverage that a search disproves is a blocking defect, not a wording nit.
4. Grade each gap: cosmetic looseness (title says "revert" when it's a partial revert — name it and move on) vs substantive misdescription (feeds the decision).

### Phase 5: Correctness trace

Trace the changed behavior end-to-end through the _unchanged_ surrounding code, in the base and head worktrees:

- Follow every caller and downstream consumer of each changed symbol; do not stop at the edited function.
- Enumerate the input/deployment shapes the change can encounter — including the shapes whose behavior is unchanged — and state the post-change behavior of each with `file:line` evidence.
- **Negative claims require a search.** "No liveness check exists on this path" is asserted only after `grep -rn <mechanism> <package>/` returns nothing; put the command in the report.
- Run what is cheaply runnable (targeted test packages, a grep, `git log -S`); report the actual command and its concrete result. Never report a verification you did not execute — if you could not run it, that fact goes in Phase 8's uncertainty section instead.

### Phase 6: Blast radius the author did not mention

1. List callers/consumers of each changed symbol (grep or codegraph) and diff that list against the PR body's coverage. Any caller whose behavior changes unmentioned is a finding.
2. Take each safety mechanism the PR body's argument leans on and prove, with a search, that it covers every affected path — the gap between "exists somewhere" and "runs here" is where regressions live.
3. History-check each apparent gap: `git log`/`git show` the commit that introduced the current behavior. A gap that predates the PR is a _restored pre-existing gap_, not a new regression — say which.
4. Ask what changes operationally for existing deployments (config shapes, upgrade behavior, capacity). A behavior change that is correct but unannounced is a needs-a-sentence-in-the-PR-body finding.
5. Grade every item: **new regression** / **restored pre-existing gap** / **needs a sentence in the PR body** / **cosmetic**. Uniform alarm is a review defect; only graded items can feed the decision.

### Phase 7: Test adequacy

The question is never "were tests added" — it is "would the shipped test fail on unfixed main."

- **Establish RED-on-main.** Cheap path: in `/tmp/pr-<N>-base`, copy only the PR's test files (`git checkout pr-<N>-head -- <test files>`) and run the targeted tests; the new test must FAIL with a message matching the bug. If the test needs non-test code from the PR to compile, trace the assertion path instead: derive the value main would produce and show it violates the assertion — and record in Phase 8 that RED was traced, not run.
- **Baseline the flipped tests.** Run the tests the PR flips, unmodified, in the base worktree; they must PASS in their old form. This proves the flips encode the new contract rather than fixing already-broken tests.
- **Confirm protective existing tests survive untouched** — name the test that pins the adjacent bug this PR must not reintroduce.
- **Find the untested composition.** If the PR body's safety argument composes two mechanisms ("A is safe because B catches the failure"), demand a test that exercises A and B together; existing tests that exercise B through a different path do not count — check their fixtures.
- Separate blocking gaps (the composition above) from nice-to-haves (a missing table row covered indirectly elsewhere).

**Perf-claim check (when the PR claims a speedup or optimizes a hot path):** hold the contributor's claim to the same Amdahl bar we hold ours — (1) is the bottleneck attributed from a _measurement_, not asserted? (2) are there before/after numbers? (3) does the _measured_ speedup match the claim, or is it inflated (flag it)? (4) is the un-optimized residual named? (5) if a fast path replaces a slow-but-correct one, does it return the **same answer** (a fast-but-wrong path is a request-changes, not a merge). An unmeasured perf claim is a finding, not a blocker by itself.

### Phase 8: Delegated reviews + decision report

Dispatch in parallel while Phases 4-7 run (or after, if context is tight):

- `gascity-blast-radius` agent — independent blast-radius pass on PR #<N>; cross-check against your Phase 6 list.
- `gascity-checker` agent — Part B (29-rule audit) only; CI surfaces the mechanical gates.
- `Skill({ skill: "review-pr", args: "<N>" })` — multi-model review.

Then decide. **One decision, stated up front.** Answer these in order; the first "yes" ends the sequence:

1. **CLOSE** — would this PR, brought to a polished state, still be unwanted? (Duplicate, superseded, architecturally wrong direction, contradicts a maintainer decision.)
2. **REQUEST CHANGES** — is there at least one blocking item: a false claim shipping into the codebase, a new regression, or a missing test the PR's own safety argument depends on — and can you state its acceptance shape? A REQUEST CHANGES whose items are all optional is a contradiction; downgrade the verdict or upgrade an item.
3. **APPROVE** — would you merge today with no further commits? Requires: correctness trace complete across the enumerated shapes, RED-on-main established, and every remaining finding graded cosmetic or optional.

`NEEDS_DISCUSSION` is reserved for genuine product-direction questions only the user or Julian can answer — never as a hedge between the three verdicts.

**Actionability bar for every change request:** the contributor must be able to act on the sentence alone, with zero follow-up questions. Each request names the file, the exact defect, and the acceptance shape — for a test, the fixture configuration and the exact assertion; for a comment or doc fix, the minimum acceptable rewording. Where a larger fix also exists, say which one you are requiring. Blocking and optional items are labeled as such. No style/naming/formatting requests unless they hide a defect; cosmetic issues are named cosmetic and waved through.

**Calibration:** the report ends with an uncertainty section that separates verified (command + result shown), inferred (reasoning shown), and unverifiable-from-here (exact resolving step named — the worktree to spin up, the test to run before merge). The load-bearing unverified item leads that section; listing trivia while the body asserts unverified claims with full confidence is uncertainty theater. Do not gate the decision on the author's unverifiable production anecdote when your independent trace supports the mechanism.

Report shape:

```
Gas City review — PR #<N>: <title>
Author / files / linked issues / CI / existing reviews
Decision: APPROVE | REQUEST_CHANGES | CLOSE | NEEDS_DISCUSSION  (first line after header)
1. Claim-vs-diff audit        (Phase 4)
2. Correctness                (Phase 5)
3. Blast radius unmentioned   (Phase 6, graded)
4. Test adequacy              (Phase 7, RED-on-main status explicit)
5. Change requests            (blocking first, then optional; each meets the actionability bar)
6. Uncertainty                (verified / inferred / unverifiable + resolving steps)
Memory cross-reference hits + delegated-review deltas
Suggested actions (each requires explicit user approval before sending)
```

### Worked example (distilled from the PR #4006 golden run)

The PR inverted a dispatcher-preference helper and its body argued safety by composition: "rig is preferred statically; issue #3454's runtime demotion falls back to city when the rig dispatcher is dead." The review's three highest-value findings, in procedure order:

_Phase 4 (claim in an added comment):_ the PR added a comment to `internal/dispatch/control.go` reading "liveness of an asleep rig-local dispatcher is a downstream (#3454) concern." Search: `grep -rn RuntimeMissing internal/dispatch/` → nothing. The #3454 demotion lives only in `graphroute.ControlDispatcherBinding` and never runs on the attempt-time path — the comment claims protection that does not exist, in text that ships into the codebase. Blocking.

_Phase 6 (grading):_ the resulting attempt-time gap was checked against history — `git show 41785d976` proved the pre-#3765 code had the same gap. Reported as a _restored pre-existing gap_, not a new regression; the blocking item is the false comment, not the gap itself. A third finding (per-rig dispatcher sessions now demand-spawn for deployments that ran only the city singleton) was graded "needs a sentence in the PR body," not blocking.

_Phase 7 (untested composition):_ the body's safety argument composes static preference with runtime demotion, but the existing demotion tests use a fixture whose agents have no StartCommand — they exercise the resolver fallback, not the changed helper. The composition the PR's argument depends on had no test. Baseline established by execution: `go test ./internal/config/ -run TestPreferredDeterministicControlDispatcher` and `./internal/graphroute/ -run TestControlDispatcherBinding` pass on unfixed main (6 and 14 tests), and the new instantiation test's RED-on-main was traced through the assertion path (`routedTo="fixture/worker"` → helper returns city agent on main → assertion on `fixture/core.control-dispatcher` fails).

_Resulting change requests_ (decision: REQUEST CHANGES):

1. Fix the attempt-time comment in `internal/dispatch/control.go`: minimum acceptable rewording states this path performs static ownership selection only and, unlike the graphroute instantiation path, has no #3454 runtime demotion. Threading the demotion through is the larger fix and is not required for this PR.
2. Add the composed regression test in `internal/graphroute/graphroute_test.go`: a `ControlDispatcherBinding` case with BOTH deterministic dispatchers configured (city `Dir=""` + rig `Dir="fixture"`, both with start commands), `rigContext="fixture"`, and `ControlDispatcherRuntimeMissing` returning true for `fixture/core.control-dispatcher`; assert demotion to `core.control-dispatcher` with `ControlFallbackFrom == "fixture/core.control-dispatcher"`.
3. _(Optional)_ one config-table row + one PR-body sentence on the demand-spawn implication.

Each request is implementable from its sentence alone; the uncertainty section led with "post-PR suite unverified by me" plus the exact scratch-worktree procedure to resolve it before merge.

### Phase 8b: Covered-issue emit (covered-map feeder)

When the review determines the PR fixes or covers issue(s) #N that it does NOT already close via a `Closes`/`Fixes`/`Resolves` keyword, write a structured line `covers_issue: #N` (the literal token `covers_issue`, a colon, then the issue number; one line per covered issue) into the review record `.gc/pr-pipeline/reviews/pr-<PR>.md`. The covered-map feeder ingests it so the merge-watcher auto-closes the issue when the PR merges. Emit only for genuine coverage you would stake a maintainer review on; omit when unsure.

### Phase 9: WAIT for per-action approval

**Do NOT submit any review, label change, merge, or close without explicit per-action user approval.**

For each suggested action, surface the EXACT API call and wait:

> About to submit a `REQUEST_CHANGES` review with body:
>
> > <draft body>
>
> Confirm? (`yes review`, `tweak: ...`, `skip`)

> About to add label `priority/p2` and remove `needs-info`. Confirm? (`yes label`, `skip`)

> About to merge PR #<N> with squash strategy and commit message:
>
> > <draft squash message>
>
> Confirm? (`yes merge`, `skip`)

Only after explicit confirmation per item, run the API call:

```bash
# Review submission:
gh pr review <N> --repo gastownhall/gascity --request-changes --body "..."
gh pr review <N> --repo gastownhall/gascity --approve --body "..."
gh pr review <N> --repo gastownhall/gascity --comment --body "..."

# Labels:
gh pr edit <N> --repo gastownhall/gascity --add-label "priority/p2" --remove-label "needs-info"

# Merge (only after CI green or only-baseline-flakes + reviews approve):
# CRITICAL: --delete-branch behavior depends on cross-repo status.
#   - Same-repo PR (sjarmak/<repo> → sjarmak/<repo>, or gastownhall fork branch → gastownhall): safe.
#   - Fork PR (contributor:branch → gastownhall/<repo>): DELETES THE CONTRIBUTOR'S BRANCH ON THEIR FORK.
#     Never do this — branch lifecycle on a contributor's fork belongs to them.
# Check first:
gh pr view <N> --repo gastownhall/gascity --json isCrossRepository,headRepositoryOwner,headRefName

# Default (safe for all PRs — never delete contributor branches):
gh pr merge <N> --repo gastownhall/gascity --squash

# Only add --delete-branch when isCrossRepository=false AND the head branch is in the same repo we're merging to:
gh pr merge <N> --repo gastownhall/gascity --squash --delete-branch

# Close (only for duplicates / superseded):
gh pr close <N> --repo gastownhall/gascity --comment "Superseded by #<M>"
```

After each action, log the API response in the conversation so the user can audit later.

Clean up the pinned worktrees when done: `git worktree remove /tmp/pr-<N>-base /tmp/pr-<N>-head`.

## Hard rules

- **Never act on PRs sjarmak authored.** Even just labeling. Abort early if `gh pr view <N> --json author --jq .author.login` returns `sjarmak`.
- **Never merge a PR with active `changes_requested` from Julian.** Defer.
- **Never merge a PR before CI completes** (other than the documented baseline flakes — and only if those are the _only_ failures).
- **Never bulk-action.** One PR, one item, one approval at a time.
- **Never use `--admin` to bypass branch protection** unless the user explicitly says so AND it's a documented emergency.
- **Never force-push** on any branch.
- **Never delete a contributor's fork branch.** When a PR is cross-repository (`isCrossRepository=true`), `gh pr merge --delete-branch` reaches into the contributor's fork (e.g. `jrimmer:docs/add-missing-readmes`) and deletes the branch there. That is their repo, not ours. Branch lifecycle on a fork belongs to the fork owner. Only use `--delete-branch` when `isCrossRepository=false` (the head branch lives in the same repo we're merging into). When in doubt, omit the flag — leaving a branch behind is harmless; deleting someone else's branch is not.
- **Never manually close another contributor's branch on `gastownhall/gascity`** as cleanup. Same reasoning.
- **If the PR author is Julian** and we're considering merging his PR — surface that explicitly. Two-maintainer setups can rubber-stamp each other; we should be MORE careful with his PRs to compensate, not less.

## Composition with other skills

This skill orchestrates existing components rather than re-implementing them:

- `gascity-blast-radius` (agent) — independent blast-radius pass
- `gascity-checker` (agent) — 29-rule audit
- `review-pr` (skill) — multi-model code review
- Memory system — cross-reference

Update this skill when any of those components change shape.

## Scope

`.claude/` is gitignored at the repo root. This skill will never be pushed upstream.
