# Autonomous PR Ship — Staging for Mayor-Side Edits

Pre-drafted edit content for mechanical application once the polecat formula work (gpk-fdq, gpk-lmm, gpk-l36, gpk-6uk, gpk-bnk) closes. Tracking: dr-1l9hc4.

Apply order: PL prompt edit → polecat prompt edit → kill-switch flag file. PL and polecat each cause one respawn event; do them sequentially with a few seconds between to keep telemetry clean.

---

## 1. PL prompt edit

**File:** `/home/ds/gas-city/agents/gascity-maintenance-pl/prompt.template.md`

**Section:** Dispatch Table (currently lines ~104-129)

### Replace existing table (lines 109-117) with:

```markdown
| Ask shape (paraphrased) | Formula | Required vars | Polecat target |
|---|---|---|---|
| "triage / kick off triage / find me N issues / what should I author next / prioritize the queue" | `mol-pr-triage` | `limit` (default 10), `category` (default "all") | `polecat` |
| "plan but do not ship / give me a scaffold for issue N / draft a plan for issue N" | `mol-pr-start` | `issue` | `polecat` |
| "open / ship / sling-PRs-for / author end-to-end / take this issue all the way to a PR" | `mol-pr-from-issue` | `issue_number` (required), `auto_push` (optional bool, default false — see classifier below), `skip_open_pr` (optional bool, default false) | `polecat` |
| "iterate on PR N feedback / address codecov gaps / address copilot comments / respond to review on PR N" | `mol-pr-iterate` | `pr`, `feedback_source` (codecov / copilot / review-comment / text), `feedback_ref` (optional) | `polecat` |
| "why is CI failing on PR N / diagnose CI for PR N / what's broken on PR N" | `mol-pr-ci-diagnose` | `pr` | `polecat` |
| "rebase + merge PR N skip review / fast-merge PR N / land PR N" | `mol-pr-merge-only` | `pr` | `polecat` |
| "blast radius for X / what does changing X touch" | `mol-pr-blast-radius` | `scope` (free-text) | `polecat` |
| "self-review my PR N / review outgoing PR N" | `mol-pr-review` | `pr` | `polecat` |
| "ship / pre-push gate / final check before push" | `mol-pr-ship` | `branch` (optional) | `polecat` |
| "review incoming PR N / adopt PR N" | `mol-adopt-pr` | `pr` | `polecat` |
```

### Insert immediately after the table (new subsection):

```markdown
### Auto-push classifier — mol-pr-from-issue only

When the ask shape maps to `mol-pr-from-issue`, classify the issue **before**
slinging to decide whether to pass `auto_push=true`:

1. **straightforward** → `auto_push=true`. Polecat ships end-to-end through the
   eligibility gate. Criteria (ALL must hold; PL checks mechanically):
   - Issue body implies single-package or sibling-file diff (no cross-cutting refactor language)
   - No label in: `breaking-change`, `requires-design`, `requires-discussion`,
     `epic`, `tracking`, `arch`, `discussion-needed`, `policy`
   - Issue is OPEN
   - No open PR already references it via `Fixes #N` / `Closes #N`
   - No design-doc reference in the issue body (ADR, RFC, "see [doc]")
   - Acceptance criteria count ≤ 3 (or absent — short issues OK)

2. **bounded** → `auto_push=false` (omit the var; defaults to halt). Polecat
   ships to branch-ready. Use when the issue is well-scoped but:
   - Touches `cmd/gc/` (but not protected files)
   - Has 4+ acceptance criteria
   - Mentions multiple subsystems
   - Yellow flag the PL can articulate in the bead body

3. **needs-decision** → DO NOT SLING. Escalate to mayor via `severity:escalate`
   rollup. Use when:
   - Issue references a design doc / ADR / RFC
   - Issue has any `requires-*` or `breaking-change` label
   - First-time-contributor-style language ("would like to discuss",
     "thoughts?", "considering refactoring X")
   - The PL can't classify confidently after one read

Default if classification is ambiguous: **bounded**. Never sling `auto_push=true`
on uncertainty — that's what the gate exists to catch, but the classifier is the
first line.
```
---

## 2. Polecat prompt edit (gascity-side only)

**File:** `/home/ds/gas-city/agents/polecat/prompt.template.md`

**Section:** Hard rules (currently lines 12-15ish, the "NEVER git push" block)

### Replace lines 13-14:

```markdown
- **NEVER `git push`.** Stephanie pushes manually after reviewing your work.

  **Scoped exception (gastownhall/gascity only, 2026-05-12+):** When `dispatch.formula == "mol-pr-from-issue"` OR `dispatch.formula == "mol-pr-revert"`, AND `dispatch.vars.auto_push == "true"`, AND the `gate-auto-push-eligibility` step has written `evidence.gate_passed=true` on the root bead, you MAY run `git push` exactly once within the `open-pr` step body. Anywhere else — including any other formula, any other rig, or this formula with auto_push=false or gate_passed!=true — the hard rule applies.

- **NEVER `gh pr create` or `gh pr edit`.** PR opening is human-gated.

  **Scoped exception (gastownhall/gascity only, 2026-05-12+):** Same condition as above (mol-pr-from-issue OR mol-pr-revert + auto_push=true + gate_passed=true). You MAY run `gh pr create` exactly once within the `open-pr` step body. `gh pr edit` remains forbidden.
```

The gascity-packs polecat (`/home/ds/gas-city/agents/gascity-packs-polecat/prompt.template.md`) gets NO carve-out in this pass — Stephanie's ask was scoped to gascity. If we want to extend later, mayor files a follow-up.

---

## 3. Kill-switch flag

**Path:** `/home/ds/.gc/auto-push-armed.flag`

Initial state: **absent**. First 3 auto-push dispatches will halt at the eligibility gate with `evidence.review_required=true` and `gc.escalate_to=mayor`. Mayor reviews each manually:
- If approve: mayor writes `evidence.reviewer_verdict=pass` + `evidence.reviewer_agent=mayor` on the root bead, then runs the push + gh pr create steps directly (bypassing the formula because the polecat already drained).
- If reject: mayor closes the bead with a halt reason.

After 3 successful approvals:

```bash
date -u +"%Y-%m-%dT%H:%M:%SZ" > /home/ds/.gc/auto-push-armed.flag
```

The flag content is a timestamp for audit. Once present, subsequent gate-pass dispatches push autonomously without mayor approval.

To disarm (emergency):

```bash
rm /home/ds/.gc/auto-push-armed.flag
```

---

## 4. MEMORY.md index update

Already done: `feedback_no_remote_ops.md` body amended with carve-out. The MEMORY.md index entry stays the same (description still accurate as the top-level rule — carve-out is detail in the body).

Consider adding a NEW memory entry pointing at dr-1l9hc4 + this design doc:

```markdown
- [Autonomous PR ship carve-out (dr-1l9hc4)](reference_autonomous_pr_ship.md) — polecat may push + open-PR on gastownhall/gascity when mol-pr-from-issue + auto_push=true + eligibility gate passes; tracking: dr-1l9hc4
```

Defer this until the implementation actually lands. Memory is for stable state, not in-flight design.
