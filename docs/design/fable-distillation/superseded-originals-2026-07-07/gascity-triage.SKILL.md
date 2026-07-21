---
name: gascity-triage
description: Gas City issue triage and work queue. Scans open issues, classifies by contributor-actionability (grab now / good candidate / investigate / skip), cross-references with open PRs and maintainer assignments, and recommends what to work on next. Use when deciding which issue to tackle.
---

# Gas City Issue Triage

> **MAINTAINER CONTEXT (2026-05-04 onward):** sjarmak holds maintainer privileges on `gastownhall/gascity`. Julian Knutsen (`julianknutsen`) is owner; sjarmak is the only other maintainer. Triage now has TWO output modes — see "Maintainer-tier triage actions" below. The default contributor-shaped mode (recommend a next-pick) is unchanged. The new maintainer mode lets us act on triage findings (label / close / comment) but every action requires per-action user approval before the API call goes out. See `feedback_maintainer_status.md` for the full guardrail set.

Dispatch to the `gascity-triage` agent which scans the full issue tracker in its own context window — keeping the raw API output out of the main conversation.

## How to execute

Spawn the agent:

```
Agent({
  description: "Gas City issue triage",
  subagent_type: "gascity-triage",
  prompt: "Run a full triage of gastownhall/gascity open issues. Pull all open issues and PRs, classify each issue into actionability tiers (Tier 1 GRAB NOW, Tier 2 GOOD CANDIDATE, Tier 3 INVESTIGATE, Tier 4 SKIP), and recommend the best next pick. Focus on milestone 1.0 bugs at p0/p1 that aren't assigned.

OUR OPEN PRs: Discover dynamically — run `gh pr list --repo gastownhall/gascity --author sjarmak --state open --json number,title,body` instead of using a hardcoded list. Extract issue numbers from PR titles and bodies.

COMPETING-WORK DETECTION (CRITICAL — get this right):
For every Tier 1 and Tier 2 candidate, you MUST check for competing PRs using ALL of these methods:
1. Search by issue number: `gh pr list --repo gastownhall/gascity --search '<issue-number>' --state open`
2. Search by issue title keywords: `gh pr list --repo gastownhall/gascity --search '<key phrases from issue title>' --state open`
3. Check the issue timeline for linked PRs: `gh api repos/gastownhall/gascity/issues/<number>/timeline --paginate` and look for cross-referenced events with pull_request source
If ANY method finds an open PR targeting the issue, demote to Tier 4. Do NOT rely on a single search — PRs may reference issues by number in the body, by title keywords, or via GitHub's cross-reference linkage. A miss here wastes contributor time on already-contested issues.

WASTELAND CHECK: For every Tier 1/2 issue, query the local Dolt clone referenced by ~/.hop/config.json for any `wanted` row whose description mentions the issue number, and treat any row with claimed_by set to another rig as competing work (demote to Tier 4). Annotate each Tier 1/2 issue with its wasteland status (open / claimed by <rig> / in_review / not listed).

CROSS-REPO CONTEXT: For Tier 1 and high-value Tier 2 issues, use Sourcegraph MCP to investigate cross-repo context: search gastownhall/gastown for original behavior (extraction bug vs inherited), trace interfaces across gastownhall/beads for bead/provider issues, and use deepsearch for p0 architectural issues. Tag each Tier 1/2 issue with its origin: [extraction bug], [inherited], [cross-repo], or [gascity-only].

MAINTAINER-DECISION GATES (CRITICAL — run on every Tier 1/2 candidate):
Competing-work checks catch duplicate PRs but miss issues where the fix requires a maintainer product/design decision before any PR is realistic. Apply ALL four blocking gates defined in the agent spec (section 'Maintainer-decision gates'):
1. Design-doc conflict — grep engdocs/design/*.md for issue symbols; if an Accepted/Implementing doc contradicts the issue's proposed direction, demote to Tier 3
2. Author uncertainty — issue body ends with '?' or contains 'intended topology/behavior', 'is this a bug or', 'Or at least', 'Is X supposed to', or proposes 2+ alternatives → demote to Tier 3
3. Maintainer-ambivalent comment — COLLABORATOR/MEMBER/OWNER comment with 'great feedback'/'good point'/'we should' but no directive and no linked PR → demote to Tier 3
4. Product-decision disguised as bug — 'Expected/Actual' sections argue product opinions ('should be light', 'feels counter-intuitive') rather than pointing at a concrete defect → demote to Tier 3
Also flag (do not demote) Gate 5: comment-thread scope drift (retracted repros, adjacent follow-ups, >3 clarifying exchanges without maintainer commitment).
Emit a 'Gates: [DD? AU? MA? PD?]' line on every Tier 1/2 entry and explain any fire in the Tier 3 demotion note. The recommended next pick MUST pass all four blocking gates.

Any size fix is fine; the default deliverable is a code PR."
})
```

## What the agent produces

A structured triage report with:

- Issue counts by milestone and priority
- Our open PR status
- **Tier 1 (GRAB NOW)**: Unassigned, clear bugs, milestone 1.0, p0/p1, no wasteland, no open PR
- **Tier 2 (GOOD CANDIDATE)**: p1/p2 bugs, solid fix opportunity, may need investigation
- **Tier 3 (INVESTIGATE FIRST)**: Unclear scope, needs discussion
- **Tier 4 (SKIP)**: Assigned, wasteland, design, post-release, covered by PR
- **Cross-repo origin tags** on Tier 1/2: `[extraction bug]`, `[inherited]`, `[cross-repo]`, `[gascity-only]`
- **Sourcegraph findings** for top candidates (gastown original behavior, beads interface surface, upstream divergence)
- **Wasteland cross-reference** — each Tier 1/2 issue is annotated with its federated wasteland status (`open` / `claimed by <rig>` / `in_review` / `not listed`). Items claimed by another rig are demoted to Tier 4.
- **Competing work detection** — flags issues covered by an open GitHub PR (found via issue number search, keyword search, AND timeline cross-references) OR claimed on the wasteland board (do NOT recommend these)
- **Maintainer-decision gates** — per-issue `Gates: [DD AU MA PD]` line; fires demote to Tier 3 to prevent picking issues that need maintainer input before a PR is realistic (design-doc conflict, author uncertainty, maintainer-ambivalent comment, product decision disguised as bug)
- **Recommended next pick** with rationale

## Workflow integration

There are now two pipeline shapes, depending on what the triage report surfaces:

**Contributor pipeline** (we author the fix):
```
/gascity-triage  →  pick issue  →  /gascity-pr-start  →  write code  →  /gascity-ship
```

**Maintainer pipeline** (someone else's PR fixes the issue):
```
/gascity-triage  →  surfaces \"PR #N covers this\"  →  /gascity-review-incoming-pr <N>
```

After triage, tell me which issue you want to work on, OR which incoming PR you want to review. I'll route to the matching next-step skill.

## Maintainer-tier triage actions

The triage agent's findings (Tier 1-4 classifications, Maintainer-decision-gate fires, competing-PR detection, wasteland status) are now actionable from the maintainer side. Each of the following requires **explicit per-action user approval** before the API call:

| Triage finding | Maintainer action available | Approval shape |
|---|---|---|
| Tier 4 (claimed by another rig / covered by open PR) | Close issue with link to covering work | Surface `proposed close: #N → linked to PR #M` and wait for `yes close` |
| Maintainer-decision gate fires (DD/AU/MA/PD) | Comment on issue with the gate finding to nudge the author | Surface the proposed comment body and wait for approval |
| Stale issue (no activity 60+ days, no maintainer comment) | Add `needs-info` label or close with stale-policy comment | Surface and wait |
| Mislabeled priority (P0/P1 evidence vs current label) | Re-label with rationale comment | Surface and wait |
| Triaged PR without `kind/*` or `priority/*` labels | Add classification labels | Surface and wait |

**Hard rules (apply to every maintainer-tier action):**

- Surface the EXACT text/labels/operation before sending; do not paraphrase what's about to happen
- Wait for explicit confirmation. "looks good" is not approval — wait for `yes`, `do it`, `close it`, etc.
- Never close, label, or comment in bulk — each item gets its own approval round
- Never act on PRs we authored (those go through Julian) — even just labeling our own PR is ask-first
- Never act on a PR mid-review by another maintainer — check `gh pr view <N> --json reviews` first; if there's an `approved`/`changes_requested` review pending action, defer to that maintainer
- After any maintainer action, log it in the conversation so the user can audit later

For PR-specific maintainer review (read PR, run blast-radius + 29-rule audit + multi-model review, produce a decision), use the dedicated `/gascity-review-incoming-pr <N>` skill — that's the right surface for incoming PR review.

## When to re-triage

- After shipping a PR (landscape changes)
- After main gets new merges from maintainers
- When you want to switch focus areas
- Weekly, to catch newly filed issues

## Scope

`.claude/` is gitignored at the repo root. This skill and its agent will never be pushed upstream.
