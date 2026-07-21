---
name: gascity-triage
description: Gas City issue triage and work queue. Scans open issues, classifies by contributor-actionability (grab now / good candidate / investigate / skip), cross-references with open PRs and maintainer assignments, and recommends what to work on next. Use when deciding which issue to tackle.
---

<!--
Target: /home/ds/.claude/skills/gascity-triage/SKILL.md (live skill this draft replaces)
Date: 2026-07-06
Status: draft pending dr-i4v.5 consumer eval
Changed vs existing skill (calibrated against the 2026-07-06 golden run + issue-triage rubric):
1. Added mandatory Step Zero staleness/reproduction check — weaker models restate stale reporter claims as current fact (rubric failure signature 3).
2. Replaced implicit tier boundaries with an ordered decision table (Q0-Q4 discriminating questions) so classification is a procedure, not judgment (signatures 1, 2, 10).
3. Added confidence-stating rules: evidence-tied vocabulary, split classification-vs-fix-scope confidence, anti-collapse self-check (signatures 1, 8).
4. Added concrete collision heuristics (fresh-P1 rule, maintainer-active families, pick-vs-pick overlap) plus an output contract checklist covering all 10 rubric failure signatures (signatures 4-7, 9).
5. Added one worked example distilled from the golden run so the GRAB NOW / GOOD CANDIDATE boundary is learnable by demonstration. Existing execution machinery (agent dispatch, competing-work detection, wasteland check, cross-repo tags, maintainer gates, maintainer-tier actions) kept unchanged.
-->

# Gas City Issue Triage

> **MAINTAINER CONTEXT (2026-05-04 onward):** sjarmak holds maintainer privileges on `gastownhall/gascity`. Julian Knutsen (`julianknutsen`) is owner; sjarmak is the only other maintainer. Triage has TWO output modes — see "Maintainer-tier triage actions" below. The default contributor-shaped mode (recommend a next-pick) is unchanged. The maintainer mode lets us act on triage findings (label / close / comment) but every action requires per-action user approval before the API call goes out. See `feedback_maintainer_status.md` for the full guardrail set.

Dispatch to the `gascity-triage` agent which scans the full issue tracker in its own context window — keeping the raw API output out of the main conversation.

## How to execute

Spawn the agent. The prompt below is the base; **append the full text of the sections "Step zero", "Classification decision table", "Confidence rules", "Collision checks", "Ranking the top picks", "Output contract", and "Worked example" to the agent prompt verbatim** — the spawned agent does not see this file otherwise.

```
Agent({
  description: "Gas City issue triage",
  subagent_type: "gascity-triage",
  prompt: "Run a full triage of gastownhall/gascity open issues. Pull all open issues and PRs, classify EVERY issue into actionability tiers (Tier 1 GRAB NOW, Tier 2 GOOD CANDIDATE, Tier 3 INVESTIGATE, Tier 4 SKIP) using the decision table below, and recommend the best next pick. Focus on milestone 1.0 bugs at p0/p1 that aren't assigned. A wrong confident classification is worse than an INVESTIGATE.

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
Competing-work checks catch duplicate PRs but miss issues where the fix requires a maintainer product/design decision before any PR is realistic. Apply ALL four blocking gates:
1. Design-doc conflict — grep engdocs/design/*.md for issue symbols; if an Accepted/Implementing doc contradicts the issue's proposed direction, demote to Tier 3
2. Author uncertainty — issue body ends with '?' or contains 'intended topology/behavior', 'is this a bug or', 'Or at least', 'Is X supposed to', or proposes 2+ alternatives → demote to Tier 3
3. Maintainer-ambivalent comment — COLLABORATOR/MEMBER/OWNER comment with 'great feedback'/'good point'/'we should' but no directive and no linked PR → demote to Tier 3
4. Product-decision disguised as bug — 'Expected/Actual' sections argue product opinions ('should be light', 'feels counter-intuitive') rather than pointing at a concrete defect → demote to Tier 3
Also flag (do not demote) Gate 5: comment-thread scope drift (retracted repros, adjacent follow-ups, >3 clarifying exchanges without maintainer commitment).
Emit a 'Gates: [DD? AU? MA? PD?]' line on every Tier 1/2 entry and explain any fire in the Tier 3 demotion note. The recommended next pick MUST pass all four blocking gates.

Any size fix is fine; the default deliverable is a code PR.

[APPEND: Step zero, Classification decision table, Confidence rules, Collision checks, Ranking the top picks, Output contract, Worked example — verbatim from the skill file]"
})
```

If any check above cannot be run (no live tool access, frozen snapshot, API failure): do NOT skip it silently. Name what the input cannot show, up front, and discount the affected confidences. Example framing from the reference run: "The snapshot has no PR/assignee data, so 'collision with maintainer work' is inferred from labels and issue age only — that is the main systematic uncertainty in every confidence figure here."

## Step zero — staleness and structure (run BEFORE classifying anything)

1. **Version drift.** For each issue, compare the version it was filed against with current main. An issue filed against an old release is a _claim to re-verify_, not ground truth. Any classification of such an issue must carry "does this still reproduce on main?" as its stated first action — even Tier 1 picks.
2. **Self-invalidation scan.** If an issue's own body reports that part of its content is already fixed ("re-grade", "partially fixed in X", edits walking back the repro), it cannot sit in Tier 1 at high confidence. Either quote the invalidation and demand a re-check, or classify INVESTIGATE.
3. **Structural features.** Before per-issue verdicts, scan the whole set for structure: batches from one reporter filed the same day (triage as a family with one shared caveat, not N independent verdicts), umbrella issues whose actionable children are already split out (the umbrella is Tier 4; point at the children), duplicate/same-subsystem clusters (cross-link them). State these observations at the top of the report.

## Classification decision table

Apply these questions **in order** to every issue. The first terminal answer decides the tier.

| #   | Question                                                                                                                                                                                     | If NO / fires                                                                                                                                  | If YES / passes |
| --- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | --------------- |
| Q0  | Staleness resolved? (Step zero: current-version claim, or re-check named as first action)                                                                                                    | Cannot be resolved from available evidence → **Tier 3**                                                                                        | continue        |
| Q1  | Contributor-shaped? (Not maintainer-owned release/packaging/process, not explicitly deferred by a maintainer, not an umbrella whose children are split out, and the code lives in THIS repo) | → **Tier 4**, naming the ownership category and, where one exists, the contributor-shaped alternative                                          | continue        |
| Q2  | Fix knowable? (Enough evidence exists to say what the fix is)                                                                                                                                | If a named action would produce the missing evidence → **Tier 3** with that action spelled out; if not even that → **Tier 4** (wontfix-shaped) | continue        |
| Q3  | Fix shape decided? (No design fork between two viable fixes; no maintainer semantic/product call required beyond at most ONE bounded decision that a PR can propose)                         | Design fork or maintainer-owned judgment → **Tier 2**, naming the fork/decision explicitly. Never pick a side of a fork and call it grab-able  | continue        |
| Q4  | Mechanism pinned and bounded? (Root cause located to a file/function/code path — cited in the issue or trivially locatable; blast radius small; competing-work and gate checks clean)        | Something still needs scoping or carries a named risk → **Tier 2** with that risk stated                                                       | → **Tier 1**    |

Hard rules the table encodes — do not override them with impact reasoning:

- **Severity is not actionability.** A P0/P1 label, a scary title, or "blocks users" never moves an issue toward Tier 1. Only mechanism-and-scope facts do. High-severity issues that need maintainer design decisions rank BELOW small pinned bugs.
- **Easy is not contributor-shaped.** A one-line fix in maintainer-owned release/packaging/process infra is still Tier 4. The deciding axis at Q1 is ownership, not difficulty.
- **Every Tier 2 entry carries a named risk or missing decision** ("Risk: …" / "Needs: …"). A Tier 2 entry without one is a hedged Tier 1 — commit it or name the risk.
- **Every Tier 3 entry is a designed experiment**, not a shrug. It must name (a) the exact missing artifact (full error text, repro against current main, which repo owns the write path), (b) the concrete action that produces it, and (c) how each possible answer resolves the issue into another tier (including "if it's the other repo's, re-file there / Tier 4"). Where the evidence would discriminate between competing hypotheses, enumerate the hypotheses. Test: could a junior contributor execute the evidence-gathering step tomorrow from your text alone?
- **Slice when a slice stands alone.** If one ask inside a multi-ask issue passes Q0-Q4 and the rest don't, classify on the grab-able slice and say which ask you scoped to.

## Confidence rules

State confidence per classification. The labels must carry information:

- **high** — mechanism located (file/function cited), single fix shape, staleness clean. The supporting clause must be mechanism language ("root cause pinned to `cmd_formula.go:888`"), never impact language ("this is a P1 affecting users"). If your justification for "high" is impact vocabulary, the classification is wrong — re-run the table.
- **medium-high** — fix direction clear, one locate-or-design judgment remains (name it in the entry).
- **medium** — direction clear but scope or design undecided; or evidence is hypothesized-not-confirmed.
- **Split confidence when the quantities differ.** Confidence in the classification and confidence in the fix scope are different numbers; separate them on mixed cases: "Confidence: medium on the classification, low on any single fix scope without that confirmation."
- **Anti-collapse check (mandatory, last step before emitting):** histogram your own confidence labels. If they are all the same value, they are decoration — recalibrate against the evidence differences that actually exist between entries.
- **Never assert what the input cannot contain.** Claims about open PRs, assignees, or current-main behavior that you did not verify are inferences — label them so ("inferred from labels and issue age only") or convert them to a to-verify step.

## Collision checks

Run these concrete checks; "might conflict with maintainer work" without a mechanism is worth nothing.

1. **Fresh-high-priority rule.** Any P0/P1 filed within the last ~7 days is presumed to have a maintainer fix in flight. It may stay Tier 1 only with "check open PRs first" stated as a precondition in the entry itself.
2. **Maintainer-active families.** Release engineering, version-pairing policy, core invariants under active decomposition, anything a maintainer comment says they are working: elevated collision risk even when the individual fix looks easy. Say so in the entry.
3. **Pick-vs-pick overlap.** After selecting Tier 1 entries and the top picks, check them pairwise: two picks touching the same command/file/subsystem get an explicit cross-reference ("same code neighborhood as #NNNN — coordinate if doing both").
4. **The list-level caveat.** Close the report by naming the collision sweep as the true first action for the whole list when the input could not prove absence of in-flight work: the ranking is conditional, not final.

## Ranking the top picks

Rank by **actionability gradient**: mechanism-pinned-to-code > one-bounded-decision > small-and-safe. If a low-value pick is included because it is a guaranteed same-day merge, say that trade openly instead of dressing it up. End with an explicit ordering rationale paragraph — an ordered list with no stated rationale is severity-ranking waiting to happen.

Each ranked pick needs four elements, at this bar:

- **First step** names a specific artifact to read or locate — a function, a file, a diff between two code paths. Never "understand the issue" or "reproduce the bug".
- **Blast radius** names the subsystems touched AND the ones deliberately untouched.
- **Proving test** at assertion level: fixture, action, asserted observable outcome, including the regression direction. Never "add unit tests" / "verify the fix works". Best case: a test that pins correct behavior under BOTH candidate hypotheses, so the plan survives its own disconfirmation.
- **Disconfirmer** ("what could make this pick wrong") is a genuine alternative causal story — the behavior might be intentional, the correct fix might live on the other side of a boundary — ideally with the correct fix under that alternative. Never generic schedule risk ("might be more complex than expected").

**Swap test (mandatory):** read pick 2's disconfirmer against pick 4's issue. If it still reads correctly, it is template output — rewrite it with content specific to that issue's causal structure.

## Output contract (self-check before emitting)

- [ ] Every issue number appears in exactly one tier; bucket counts are stated and sum to the input total.
- [ ] Structural observations (families, umbrellas, clusters) stated up front and applied as shared caveats.
- [ ] Bucket histogram is plausible: on any meaningful snapshot, empty Tier 3 and Tier 4 buckets are a padding signature — re-run Q1/Q2 before shipping that shape.
- [ ] Every Tier 2 entry has a named risk/decision; every Tier 3 entry has artifact + action + resolution paths; every Tier 4 entry names its skip category (maintainer-owned / duplicate-of / umbrella / deferred / wontfix-shaped), not adjectives.
- [ ] Confidence labels vary, use mechanism language, and are split where classification- and scope-confidence differ.
- [ ] The input's systematic blind spots are named once and visibly discount specific verdicts — not a disclaimer paragraph that touches nothing.
- [ ] `Gates: [DD? AU? MA? PD?]` line on every Tier 1/2 entry; recommended next pick passes all four.
- [ ] Top picks pass the swap test; pick-vs-pick overlaps cross-referenced; list-level collision caveat present.
- [ ] No padding. A wrong confident classification is worse than an INVESTIGATE.

## Worked example (from the 2026-07-06 golden run)

Input excerpt (abridged issue): _"#3968 — pool worker can't claim work routed to its pool handle. Work slung to the pool's routing handle sits unclaimed; the worker's claim query matches on a different identity. Filed against gc 1.3.3, part of an 18-issue batch from one downstream operator that morning."_

Decision table applied:

- **Q0 staleness:** part of a same-morning batch filed against 1.3.2/1.3.3 → family caveat applies; "does this still reproduce on main?" is the stated first action. Resolvable → continue.
- **Q1 ownership:** core gascity routing code, contributor-shaped → continue.
- **Q2 fix knowable:** yes — the identity mismatch between the routing handle and the claim query is characterized in the issue → continue.
- **Q3 fix shape decided:** NO. Two viable fixes — widen the worker claim query, or convoy-wrap at dispatch — touching core routing plus the reconciler's assignee-revert behavior. That is a design fork needing maintainer direction on which side to fix.

Classification: **Tier 2 (GOOD CANDIDATE)** — despite being high value (this exact mismatch is a known operational blocker in our own city). Personal pain and value do not override the structural fact that the fix shape is undecided; the entry names the fork explicitly instead of picking a side and calling it grab-able.

Confidence: **medium** — mechanism characterized (not impact language), but scope is undecided pending the design call.

Contrast with a Tier 1 from the same run: _"#3944 — cook --attach drops rig context. Root cause pinned to `cmd/gc/cmd_formula.go:888` passing empty `routedTo`, reproduced on current main, and the working sling path is the reference implementation. Confidence: high."_ Same repo, smaller value — but mechanism pinned to a line, single fix shape, staleness clean. That is what moves an issue past Q3/Q4, not severity.

## Workflow integration

There are two pipeline shapes, depending on what the triage report surfaces:

**Contributor pipeline** (we author the fix):

```
/gascity-triage  →  pick issue  →  /gascity-pr-start  →  write code  →  /gascity-ship
```

**Maintainer pipeline** (someone else's PR fixes the issue):

```
/gascity-triage  →  surfaces "PR #N covers this"  →  /gascity-review-incoming-pr <N>
```

After triage, tell me which issue you want to work on, OR which incoming PR you want to review. I'll route to the matching next-step skill.

## Maintainer-tier triage actions

The triage agent's findings (Tier 1-4 classifications, Maintainer-decision-gate fires, competing-PR detection, wasteland status) are actionable from the maintainer side. Each of the following requires **explicit per-action user approval** before the API call:

| Triage finding                                            | Maintainer action available                                | Approval shape                                                          |
| --------------------------------------------------------- | ---------------------------------------------------------- | ----------------------------------------------------------------------- |
| Tier 4 (claimed by another rig / covered by open PR)      | Close issue with link to covering work                     | Surface `proposed close: #N → linked to PR #M` and wait for `yes close` |
| Maintainer-decision gate fires (DD/AU/MA/PD)              | Comment on issue with the gate finding to nudge the author | Surface the proposed comment body and wait for approval                 |
| Stale issue (no activity 60+ days, no maintainer comment) | Add `needs-info` label or close with stale-policy comment  | Surface and wait                                                        |
| Mislabeled priority (P0/P1 evidence vs current label)     | Re-label with rationale comment                            | Surface and wait                                                        |
| Triaged PR without `kind/*` or `priority/*` labels        | Add classification labels                                  | Surface and wait                                                        |

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
