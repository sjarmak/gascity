# Autonomous PR Ship — Polecat End-to-End for Straightforward Tier-1 Issues

**Status:** Draft, awaiting Stephanie sign-off
**Tracking bead:** dr-1l9hc4
**Author:** mayor, 2026-05-12
**Trigger:** Stephanie green-lit on 2026-05-12 the option to dispatch polecats to own issue → PR end-to-end when the solution is straightforward and does not require escalation to her.

## TL;DR

Today: `mol-pr-from-issue` already promises end-to-end but every polecat halts at the push step because its behavioral rule forbids `git push` / `gh pr create`. The PL frames this as a "contract conflict" needing architectural work. It isn't — it's the standing fork-wide rule (`feedback_no_remote_ops`) being enforced correctly. What's missing is a scoped, mechanically-gated carve-out that lets polecats push **only when** the dispatch carries an explicit pre-approval signal **and** a quantitative eligibility check passes.

This design adds that carve-out, leaves the standing rule intact everywhere else, and escalates failures to **mayor** (never to Stephanie) so the option Stephanie asked for actually works without re-introducing the "PL pings the human for every push" failure mode.

## Goals

1. Polecat owns issue → PR end-to-end when the work is small, mechanically clean, and within a defined safety envelope.
2. **Every** escalation surfaces to mayor first. Stephanie sees only what mayor cannot resolve.
3. Carve-out is **formula-scoped**, not agent-scoped. Polecats remain locked-down everywhere else.
4. Gate criteria are **mechanical** (counts, regex matches, exit codes). No judgment calls in the gate path — those belong to the polecat's `summary_for_human` field, which routes to mayor.
5. Loop-close visibility — every auto-pushed PR posts to `#gascity-maintenance` slack so Stephanie can audit after the fact.

## Non-goals

- Generalizing this to rigs other than gastownhall/gascity. (Other rigs each have their own contributor model; this is a gascity-fork carve-out.)
- Relaxing the polecat hard rule globally. (Anywhere outside this formula path, the polecat still cannot push.)
- Auto-merge. PRs land for review; Julian / reviewbot / Stephanie still own the merge decision.

## Where the existing formula already does the right thing

`mol-pr-from-issue` (`/home/ds/gascity-packs/pr-review/formulas/mol-pr-from-issue.formula.toml`) already chains: `pr-start → gate-after-pr-start → ship → gate-after-ship → open-pr → drain`. The `ship` step itself contains the four-stage validation pipeline:

| Stage | What it does | Mechanical? |
|---|---|---|
| simplify | Walks diff, removes dead code, consolidates duplicates, drops unused imports | Yes |
| review-iterate | 11-category scorecard + 7 recurring themes, caps at 3 iterations, fixes blockers/majors in-place | Mostly — review judgment is the polecat's |
| contributor-check | build / vet / tests / docs gates per repo stack | Yes |
| report | Verdict = READY iff simplify ok AND review converged AND no required gate failed | Yes |

`gate-after-ship` already halts the chain when `evidence.reviewer_verdict != "pass"`. **This is the existing mechanical gate** — the design below extends it, not replaces it.

## What's missing today

1. **No positive signal** that authorizes the push step. `skip_open_pr=false` is a double-negative default; polecat treats it as ambiguous and falls back to its hard rule.
2. **No diff-scope gate.** A 2-line fix and a 2000-line refactor both pass `ship` if tests are green. Auto-pushing the latter is wrong even if mechanically green.
3. **No path-protection check.** Touching `internal/api/` or `.github/workflows/` should always halt for human review regardless of test status.
4. **No issue-label check.** An issue labeled `breaking-change` or `requires-design` should never auto-push even if a polecat thinks the diff is small.
5. **No polecat scope-limited carve-out.** The behavioral rule "never git push or gh pr create" is universal; we need to scope it: "never push, unless `dispatch.auto_push=true` and the eligibility gate passed."

## Design

### 1. New formula var: `auto_push`

Add to `mol-pr-from-issue`:

```toml
[vars.auto_push]
description = "Positive signal authorizing the polecat to push + open PR after eligibility gate. Default false. Required-positive — absence is treated as 'halt at branch-ready' regardless of skip_open_pr."
default = "false"
```

Semantic precedence: `auto_push=true` is required for any push to happen. `skip_open_pr=true` still wins (early-exit at open-pr per the existing path). When `auto_push=false` (default), the polecat behaves as if `skip_open_pr=true` was passed — halts at branch-ready with a summary noting the absence of the positive signal.

This is intentionally redundant with `skip_open_pr`. The reason: `skip_open_pr` is a "stop short" flag (opt-out of a default that says go); `auto_push` is a "go" flag (opt-in to a default that says stop). The polecat reads the latter; the former is preserved for backward compat.

### 2. New step: `gate-auto-push-eligibility` (between `ship` and `open-pr`)

Insert after `gate-after-ship`. Skipped when `auto_push=false` (in which case the chain falls through to `open-pr` which honors the existing `skip_open_pr` early-exit). When `auto_push=true`, this step runs the **mechanical** eligibility checks below:

#### Hard-required gates (all must pass)

| Gate | Check | Halt action |
|---|---|---|
| **diff-size** | `git diff origin/main...HEAD --numstat` summed lines added+removed ≤ 500 | Halt; `summary_for_human` names the LOC count |
| **diff-files** | Number of files changed ≤ 8 | Halt; lists the file count |
| **path-protection** | No paths matching: `.github/**`, `cmd/gc/dispatch_runtime.go`, `internal/api/**`, `**/secrets/**`, `hooks/**`, `.beads/**`, `**/migrations/**` | Halt; lists offending paths |
| **issue-label** | Issue's labels do NOT include any of: `breaking-change`, `requires-design`, `requires-discussion`, `epic`, `tracking`, `arch`, `discussion-needed`, `policy` | Halt; lists the blocking labels |
| **issue-state** | Issue is OPEN, no open PR already references it via `Fixes #N` or `Closes #N` | Halt; lists conflicting PR if any |
| **base-ci** | `gh run list --branch main --status failure --limit 1` returns no fresh failures (within last 6h) | Halt; "main CI red, holding to avoid stacking on broken base" |
| **commit-count** | `git rev-list origin/main..HEAD --count` ≤ 5 commits | Halt; encourages squash before auto-push |
| **branch-name** | Branch name matches `^fix/.*-[0-9]+$` or `^feat/.*-[0-9]+$` (issue number suffix) | Halt; conventional naming required for auto-push class |

#### Soft halts (polecat can self-flag in addition)

The polecat's own `summary_for_human` carries a `confidence: low|medium|high` field. The gate halts when `confidence != high`. Polecat is instructed to default to `medium` unless it actively asserts high.

#### Gate halt = escalate to mayor, NOT to Stephanie

When any gate fires:

```bash
bd update "$ROOT_ID" \
  --set-metadata "gc.halt_chain=true" \
  --set-metadata "gc.escalate_to=mayor" \
  --set-metadata "summary_for_human=Auto-push eligibility halt for issue #$ISSUE: <gate-name>: <gate-detail>. Branch $BRANCH ready at $SHA for human review. Escalating to mayor for routing decision."
bd close "$ROOT_ID" --reason "auto-push eligibility halt: <gate-name>"
```

The `gc.escalate_to=mayor` metadata is read by the loop-close handler to route the halt to mayor's inbox first, not Stephanie's. Mayor decides: fix-and-retry, escalate-further, or abandon.

### 3. Polecat behavioral carve-out

The polecat agent's prompt template gets a scoped exception block:

> **Hard rule:** Never run `git push`, `git push -u`, `gh pr create`, or any equivalent remote-write operation.
>
> **Exception (scoped):** When `dispatch.formula == "mol-pr-from-issue"` AND `dispatch.vars.auto_push == "true"` AND the `gate-auto-push-eligibility` step has written `evidence.reviewer_verdict == "pass"` for this molecule, you MAY run `git push` and `gh pr create` exactly once each within the `open-pr` step body. Nowhere else. If the gate did not write `pass`, the hard rule applies.

Polecat reads `dispatch.formula` and `dispatch.vars.auto_push` from the bead metadata (already populated by `gc sling --on mol-pr-from-issue --var auto_push=true`). It reads the gate's verdict from the root bead's `evidence.reviewer_verdict` after the gate step closes.

### 4. PL classifier: which beads get `auto_push=true`?

The PL runs `/gascity-pr-start` against each Tier-1 issue **before** slinging. The classifier returns one of three labels:

| Label | Criteria | Sling action |
|---|---|---|
| **straightforward** | Single-package diff probable, no policy paths in blast radius, issue has clear test plan, no design-call language in the issue body, no `requires-*` labels, sibling issues not open | `gc-sling polecat <bead> --on mol-pr-from-issue --var issue_number=N --var auto_push=true` |
| **bounded** | Issue is well-scoped but the classifier sees a yellow flag (e.g., touches `cmd/gc/` but not the protected files, or has ≥ 3 acceptance criteria) | `gc-sling polecat <bead> --on mol-pr-from-issue --var issue_number=N` (auto_push omitted → defaults false → halt at branch-ready) |
| **needs-decision** | Architectural call, design doc referenced in issue, requires-* labels, or first-time-contributor-style refactor (cross-cutting renames, schema changes, etc.) | Do NOT sling. Mayor's queue. |

The classifier itself is **mechanical** — no LLM judgment in the routing. The polecat's actual implementation step is the AI-reasoning layer; PL's job is just file-globbing + label-reading.

### 5. `GASCITY_SHIP_BYPASS` bridge

The existing `gascity-ship-gate` PreToolUse hook (`feedback_gascity_ship_gate`) blocks `git push fork` / `gh pr create --repo gastownhall/gascity` unless a sentinel exists. The eligibility gate sets `GASCITY_SHIP_BYPASS=auto_push_validated:<bead-id>` in the polecat's environment when it writes `verdict=pass`. The hook accepts this bypass reason, logs it for audit (`.gc/gascity-ship-bypass.log` JSONL), and lets the push proceed.

This keeps the hook architecturally intact — bypasses are still explicit and audit-logged — while giving the gate a clean handoff to the push step.

### 6. Memory amendment

Update `feedback_no_remote_ops` with:

> **Carve-out (2026-05-12):** When `dispatch.formula == "mol-pr-from-issue"` AND `dispatch.vars.auto_push == "true"` AND the `gate-auto-push-eligibility` step has written `evidence.reviewer_verdict == "pass"`, polecats may run `git push` + `gh pr create` exactly once each within the `open-pr` step. Anywhere else, no remote ops. See dr-1l9hc4.

### 7. Loop-close visibility

Every auto-pushed PR (regardless of CI outcome) posts to `#gascity-maintenance`:

```
[auto-push] PR #N opened: <title>
  Issue: #<issue>
  Branch: <branch> @ <sha>
  Diff: <N> files, <M> LOC
  Gate: pass (diff-size=<N>, no protected paths, label-clean, confidence=high)
  CI: <status>
  URL: <pr-url>
```

Mayor's morning scan reviews these and can close/revert any without explanation needed.

## Implementation plan (5 child beads under dr-1l9hc4)

| # | Bead | Description | Owner |
|---|---|---|---|
| 1 | dr-XXXX | Edit `mol-pr-from-issue.formula.toml`: add `auto_push` var, insert `gate-auto-push-eligibility` step between `gate-after-ship` and `open-pr`, update `open-pr` to require gate verdict | polecat (with `auto_push=false` — meta!) |
| 2 | dr-XXXX | Polecat prompt-template edit: add the scoped exception block to `prompts/polecat.prompt.template.md` | mayor (touches `.claude/`; per `feedback_no_dotclaude_in_worker_scope`, can't sling) |
| 3 | dr-XXXX | PL classifier: add the straightforward/bounded/needs-decision step to `/gascity-pr-start` | polecat |
| 4 | dr-XXXX | `gascity-ship-gate` hook: accept `auto_push_validated:<bead-id>` bypass reason, add audit log | polecat |
| 5 | dr-XXXX | Memory amendment for `feedback_no_remote_ops` | mayor (memory is mayor-scope) |

Sequence: 1, 3, 4 in parallel; 2 + 5 are mayor work in series; final test is mayor manually slinging a known-straightforward Tier-1 with `auto_push=true` and watching the gate fire correctly.

## Open questions for Stephanie's review

1. **LOC threshold.** 500 LOC felt reasonable but is arbitrary. Should it be 300? 1000? Or expressed per-file (max 100 LOC per file)?
2. **Confidence default.** Should polecat default to `confidence=medium` (current spec) or `high` (more aggressive auto-push)? Lower default = more mayor escalations early, which is what you want to debug the gate calibration.
3. **First-N gate.** Should the first N (say, 3) auto-pushes route to mayor for explicit approval before pushing, as a calibration warm-up? Helpful for catching gate misses before they ship. Default in this draft: no warm-up. Auto-push goes live the moment the formula change lands.
4. **Revert path.** If a polecat auto-pushes a PR that turns out wrong, do we want a `mol-pr-revert` formula that takes a PR number and force-pushes a revert + comments on the PR? Or is `gh pr close` + manual branch delete sufficient?
5. **Path-protection list.** I picked `.github/**`, `cmd/gc/dispatch_runtime.go`, `internal/api/**`, `**/secrets/**`, `hooks/**`, `.beads/**`, `**/migrations/**` from memory. Need your sign-off on the full list — e.g., should `cmd/gc/session_lifecycle_*` be protected too? `internal/config/`?

## Tonight's scope (no implementation; design only)

This document is the entire output. No code edits, no formula changes, no prompt-template edits. Implementation child beads filed only after Stephanie morning review and sign-off on the open questions above.

The 6 in-flight Tier-1 beads (gc-h4jk, gc-pd7l, gc-ziup, gc-x45a, gc-t36n; gc-wpon closed as superseded) run under the current `auto_push=false` default → land at branch-ready by morning per Path A. Mayor manually re-routes them through the new gate post-implementation if Stephanie wants the precedent set.

gc-vqxh (already done, bundle of #1486+#1551) is a special case: predates the formula change. Mayor will sequence its push first manually so Stephanie can validate the new gate against a known-good branch before it goes live on subsequent dispatches.
