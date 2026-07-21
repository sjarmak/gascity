---
name: gascity-review-incoming-pr
description: Maintainer-side review of an incoming PR from another contributor on gastownhall/gascity. Composes blast-radius + 29-rule audit + multi-model review + memory cross-reference into a single decision report (approve / request changes / close). Surfaces every GitHub action (review submission, label, merge, close) for explicit user approval before sending. Never acts on PRs we authored — those go through Julian. Fork-local skill.
---

# Gas City Review Incoming PR

> **MAINTAINER CONTEXT (2026-05-04 onward):** sjarmak holds maintainer privileges on `gastownhall/gascity`. Julian Knutsen (`julianknutsen`) is owner; sjarmak is the only other maintainer. This skill is the maintainer-side counterpart to `gascity-ship` — `gascity-ship` is for PRs we author; this skill is for PRs we review. See `feedback_maintainer_status.md` for the full guardrail set.

## When to use

- A new PR comes in from another contributor (rileywhite, quad341, kab0rn, julianknutsen, etc.) and we want a maintainer-grade review before posting feedback or merging
- `/gascity-triage` surfaces "PR #N covers issue #M" and you want to evaluate the covering PR
- An existing PR has new commits and we need to re-review

## When NOT to use

- The PR is authored by sjarmak. Our own PRs go through Julian's review — never self-review or self-merge. Use `gascity-ship` for our authoring pipeline instead.
- The PR is mid-review by Julian (check `gh pr view <N> --json reviews` first; if there's a pending `approved` / `changes_requested` review, defer to him)

## Invocation

```
/gascity-review-incoming-pr <PR-number-or-URL>
```

## Pipeline

### Phase 1: Read the PR

```bash
gh pr view <N> --repo gastownhall/gascity --json number,title,body,author,state,files,additions,deletions,reviews,statusCheckRollup,headRefOid,baseRefName,labels
```

Extract:
- Author (must NOT be sjarmak — abort if so)
- Files touched and diff size
- Linked issue(s) from the body
- Existing reviews — defer if Julian has an active `approved` / `changes_requested`
- CI status — note any failures (separate baseline flakes from real regressions)

### Phase 2: Memory cross-reference

Before delegating to deeper analysis agents, consult memory for context the analysis won't otherwise see:

- Read `project_session_handoff_2026_05_04.md` (and any newer handoff): does this PR's area overlap with our in-flight PRs (#1661/#1662/#1673/#1688) or known baseline flakes?
- `feedback_mayor_filed_priority.md`: does the PR address a mayor-filed issue (`openclaw-dv` author)? Higher-impact merges deserve extra care.
- `feedback_pr_no_internal_tooling_refs.md`: are there bd/Stage-N/internal-tooling refs in the PR title/body the author could clean up before merge?
- Any `feedback_*` memory whose `description` matches a substring of the PR's file paths or title.

Surface relevant memory hits in the final report so the user can sanity-check.

### Phase 3: Blast radius (delegate)

```
Agent({
  description: "Blast radius for incoming PR #<N>",
  subagent_type: "gascity-blast-radius",
  prompt: "Analyze the blast radius of PR #<N> on gastownhall/gascity. Check out the branch locally if useful. Map callers, execution contexts, config field chains, domain boundaries, concurrency. Flag any HIGH-risk findings the author may not have caught. Return the structured risk report."
})
```

### Phase 4: 29-rule contributor audit (delegate)

```
Agent({
  description: "29-rule audit for incoming PR #<N>",
  subagent_type: "gascity-checker",
  prompt: "Run Part B (29-rule audit) of the Gas City contributor check on PR #<N>'s diff. Skip Part A (mechanical gates) — CI will surface those. Focus on B1-B29 violations the author may have missed. Return the structured report."
})
```

If CI is failing on the PR, note which checks are baseline flakes (`Integration / rest-full-7-of-8` `TestGastown_MultiRig_BeadIsolation` is the documented one — see handoff memory) vs real regressions.

**Perf-claim check (when the PR claims a speedup or optimizes a hot path):** hold the contributor's claim to the same Amdahl bar we hold ours — (1) is the bottleneck attributed from a *measurement*, not asserted? (2) are there before/after numbers? (3) does the *measured* speedup match the claim, or is it inflated (flag it)? (4) is the un-optimized residual named? (5) if a fast path replaces a slow-but-correct one, does it return the **same answer** (Phase 5 Codex is the backstop — a fast-but-wrong path is a request-changes, not a merge). Record the verdict in the Phase 6 report; an unmeasured perf claim is a finding, not a blocker by itself.

### Phase 5: Multi-model code review (delegate to existing skill)

```
Skill({ skill: "review-pr", args: "<PR-number-or-URL>" })
```

`review-pr` already produces a maintainer-grade decision report. We compose it here so all sources of signal land in one place.

### Phase 6: Combined decision report

```
Gas City review — PR #<N>: <title>

Author: <login>  (NOT sjarmak ✓)
Files: <count>, +<add>/-<del>
Linked issues: #<M>, #<K>
CI: <green | N failures, M baseline flakes>
Existing reviews: <none | Julian: approved / changes_requested / commented>

--- Memory cross-reference ---
<bullet list of relevant memory hits, e.g.:>
- Mayor-filed: yes (openclaw-dv) — extra care on merge
- Overlaps in-flight #1688 (gc mail thread fix) on internal/mail/beadmail/

--- Blast radius (gascity-blast-radius) ---
<paste agent report>

--- 29-rule audit (gascity-checker Part B) ---
<paste agent report>

--- Multi-model review (review-pr) ---
<paste decision report>

--- Verdict ---
APPROVE / REQUEST_CHANGES / CLOSE / NEEDS_DISCUSSION

Rationale: <2-3 sentences>

Suggested actions (each requires explicit user approval before sending):
  [ ] Submit review: <APPROVE | REQUEST_CHANGES | COMMENT> with body: <draft body, ≤300 chars or fenced>
  [ ] Add label(s): <list>
  [ ] Remove label(s): <list>
  [ ] Merge: <squash | merge | rebase>
  [ ] Close (if duplicate / superseded): <link to covering work>
```

### Phase 6b: Covered-issue emit (covered-map feeder)

When the review determines the PR fixes or covers issue(s) #N that it does NOT already close via a `Closes`/`Fixes`/`Resolves` keyword, write a structured line `covers_issue: #N` (the literal token `covers_issue`, a colon, then the issue number; one line per covered issue) into the review record `.gc/pr-pipeline/reviews/pr-<PR>.md`. The covered-map feeder ingests it so the merge-watcher auto-closes the issue when the PR merges. Emit only for genuine coverage you would stake a maintainer review on; omit when unsure.

### Phase 7: WAIT for per-action approval

**Do NOT submit any review, label change, merge, or close without explicit per-action user approval.**

For each suggested action, surface the EXACT API call and wait:

> About to submit a `REQUEST_CHANGES` review with body:
> > <draft body>
> Confirm? (`yes review`, `tweak: ...`, `skip`)

> About to add label `priority/p2` and remove `needs-info`. Confirm? (`yes label`, `skip`)

> About to merge PR #<N> with squash strategy and commit message:
> > <draft squash message>
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

## Hard rules

- **Never act on PRs sjarmak authored.** Even just labeling. Abort early if `gh pr view <N> --json author --jq .author.login` returns `sjarmak`.
- **Never merge a PR with active `changes_requested` from Julian.** Defer.
- **Never merge a PR before CI completes** (other than the documented baseline flakes — and only if those are the *only* failures).
- **Never bulk-action.** One PR, one item, one approval at a time.
- **Never use `--admin` to bypass branch protection** unless the user explicitly says so AND it's a documented emergency.
- **Never force-push** on any branch.
- **Never delete a contributor's fork branch.** When a PR is cross-repository (`isCrossRepository=true`), `gh pr merge --delete-branch` reaches into the contributor's fork (e.g. `jrimmer:docs/add-missing-readmes`) and deletes the branch there. That is their repo, not ours. Branch lifecycle on a fork belongs to the fork owner. Only use `--delete-branch` when `isCrossRepository=false` (the head branch lives in the same repo we're merging into). When in doubt, omit the flag — leaving a branch behind is harmless; deleting someone else's branch is not.
- **Never manually close another contributor's branch on `gastownhall/gascity`** as cleanup. Same reasoning.
- **If the PR author is Julian** and we're considering merging his PR — surface that explicitly. Two-maintainer setups can rubber-stamp each other; we should be MORE careful with his PRs to compensate, not less.

## Composition with other skills

This skill orchestrates existing components rather than re-implementing them:

- `gascity-blast-radius` (agent) — blast radius
- `gascity-checker` (agent) — 29-rule audit
- `review-pr` (skill) — multi-model code review
- Memory system — cross-reference

Update this skill when any of those components change shape.

## Scope

`.claude/` is gitignored at the repo root. This skill will never be pushed upstream.
