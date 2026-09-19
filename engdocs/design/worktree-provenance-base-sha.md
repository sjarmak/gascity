---
title: "Worktree Provenance: What BaseSHA Names"
---

| Field | Value |
|---|---|
| Status | Implemented |
| Date | 2026-09-19 |
| Author(s) | sjarmak |
| Issue | gc-ryekpd |
| Supersedes | N/A |

## Summary

`internal/worktree` publishes a provenance record for every managed worktree,
and its `BaseSHA` field is consumed across the formula fleet as
`gc.worktree_base_sha`. Before this change `BaseSHA` recorded the commit the
caller *asked for* rather than the commit the tree was *built from*, and
`Verify` compared that value only against the same spec that produced it, so a
wrong value always verified clean. This document fixes the field's meaning to
"a commit the worktree provably descends from" and makes `Verify` assert it.

## Problem

`Ensure` has two creation paths and they disagree about what the base is.

When the branch does not exist, `git worktree add -b <branch> <path> <base>`
creates it at the resolved base, so the resolved base is the truth.

When the branch already exists, `git worktree add <path> <branch>` attaches the
branch **as it stands**. `spec.Base` is never passed to git and never consulted.
The branch may sit at an older tip, may carry commits of its own, or may have
been cut from something else entirely. The pre-change code recorded
`resolvedBase` in both cases, so on the branch-exists path it published a commit
the tree might not descend from at all.

`Verify` did not catch this. `verifyProvenance` recomputed the expected record
with `plannedProvenance(spec, got.BaseSHA)` — feeding the observed value back in
as the expected one — and compared fields. Field equality proves the record is
internally consistent; it says nothing about the tree. A record naming a commit
the worktree never descended from passed every check.

The consumers are the reason this matters. `gc.worktree_base_sha` is written by
`mol-scoped-work`, `mol-focus-review`, `mol-pr-iterate`, `mol-pr-merge-only`,
`mol-pr-revert`, `mol-adopt-pr` and `mol-polecat-commit`, and read back to
decide whether a tree is current, to compute a merge base, and to scope a review
diff. Every one of those reads a base the tree was never built from if the value
is wrong, and none of them can tell.

## Decision

1. **`BaseSHA` names the commit the tree was built from, not the one requested.**
   `resolveCreationState` now returns a separate `provenanceBase`. On the
   branch-exists path it is the branch's own current tip, resolved at creation
   time — the one commit provably true of the tree at that moment.
   `resolvedBase` keeps its existing role in the create-and-verify path, so the
   spec-validation error (`base ref %q resolves to %s, want recorded base SHA
   %s`) is unchanged.

2. **`Verify` asserts ancestry, not just field equality.** `verifyProvenance`
   takes the report's `Head` and requires `merge-base --is-ancestor BaseSHA
   HEAD`. A record that does not describe the tree now fails with
   `HEAD %s does not descend from recorded base SHA %s`.

## Consequences

- **`Verify` is strictly stronger and can now fail on trees that previously
  passed.** That is the point, but it means a worktree carrying a genuinely
  false provenance record surfaces as a verification failure rather than
  silently feeding a wrong base downstream. Callers that treat any `Verify`
  error as fatal will start failing lanes that used to proceed on bad data.
- **On the branch-exists path the recorded base is no longer comparable to
  `spec.Base`.** A consumer that assumed `gc.worktree_base_sha` equals the
  resolved base ref (for example, to answer "is this tree current with main?")
  must now compare against the base ref directly instead of against this field.
  Distance-from-base is a separate question from build provenance, and this
  field only answers the second.
- **It composes with, and does not replace, the formula-level guard.** City
  commit `2394685` made `mol-scoped-work` refuse a reused work branch left at a
  stale tip, naming both SHAs. That guard is a policy decision about whether a
  stale branch is acceptable; this change is about the record being true. Both
  are wanted: the formula decides, `Verify` can no longer be lied to.
- **`DryRun` output changes.** `planDryRun` reports the provenance it would
  publish, so a dry run against an existing branch now shows the branch tip.

## Alternatives considered

- **Keep recording `resolvedBase` and add the ancestry check only.** Rejected:
  the check would then fail routinely on the branch-exists path, which is the
  normal retry shape, turning a correctness fix into a lane-killer.
- **Refuse to attach an existing branch whose tip is not the resolved base.**
  Rejected here as the wrong layer. Refusing is a policy call about whether to
  proceed, and it already lives in the formula (`2394685`); `internal/worktree`
  records what is true and lets the caller decide.

## Verification

`internal/worktree/worktree_test.go` adds two tests, both red before this change:

- `TestEnsureExistingBranchRecordsActualBuildBase` — attaching an existing
  branch whose tip differs from `spec.Base` records the tip.
- `TestVerifyFailsWhenBaseSHAIsNotAncestorOfHead` — a provenance record naming a
  non-ancestor fails verification instead of passing.
