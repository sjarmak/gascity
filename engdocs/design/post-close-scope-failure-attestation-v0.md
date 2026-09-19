---
title: "Post-Close Scope Failure Must Not Invert a Delivered Root's Attestation"
---

| Field | Value |
|---|---|
| Status | Proposed |
| Date | 2026-09-19 |
| Author(s) | gascity-worker (gc-le45hl) |
| Issue | gc-le45hl |
| Supersedes | N/A |

## Summary

`resolveFinalizeOutcome` demotes an otherwise-passing `mol-scoped-work` root to
`gc.outcome=fail` whenever any direct member with `gc.on_fail=abort_scope`
terminally failed (`terminalAbortScopeFailureMember`). This is correct when the
member's failure reflects real unfinished or broken work. It is wrong when the
member is a `workspace-setup` (or similar) step that reran *after* the target
bead was already closed `deliverable` by the same root's own worker, found the
target already done, and hard-failed `duplicate_dispatch` out of correct
conservatism. In that case the work landed and only the attestation is
inverted (gc-le45hl, instances 2026-09-04 and 2026-09-18).

This doc narrows the demotion condition: a terminal abort-scope failure no
longer demotes the outcome when the failing member's target bead already
carries a recorded producer disposition of `deliverable`.

## The narrowed condition

`terminalAbortScopeFailureMember` still finds the same candidate member exactly
as before. What changes is what `resolveFinalizeOutcome` does with a positive
match: before demoting, it resolves the failing member's target bead (the bead
named by the member's `gc.var.issue` formula variable, the standard way a step
bead references the work item it operates on) and checks whether that target
bead is `closed` with
`gc.coordinator_outcome.producer_disposition=deliverable` already recorded. If
so, the member's failure is treated as moot for the purpose of the root's
outcome, and the outcome is NOT demoted (stays `pass`). Otherwise, behavior is
unchanged: the terminal abort-scope failure demotes the outcome to `fail`.

This only affects the OUTCOME computed in `resolveFinalizeOutcome`. It does not
touch `resolveFinalizeFailureDiagnostics` beyond it no longer being consulted
in the (now-passing) case, does not change the `duplicate_dispatch` refusal
itself, does not touch retry counts or worktree-ensure logic, and does not
change how producer dispositions are written.

## What this deliberately does NOT demote-protect

Demotion is skipped ONLY when a producer disposition of `deliverable` is
already recorded for the specific target bead the failing member names. It is
never skipped on:

- the target bead merely being `closed` (closed non-deliverable, e.g.
  `non_deliverable` or no disposition at all, still demotes to `fail`);
- the failing member's own closed status or its failure class/reason;
- any inference from title, timing, or "the step failed late" — there is no
  timestamp comparison here, only presence of the recorded disposition at
  finalize-evaluation time.

Concrete counter-example this must keep failing (`gc-3myte3`, 2026-09-19): an
`implement` step lands its own commits on a self-named branch instead of the
formula's `work/<bead>` branch, the scope's self-review step correctly finds
nothing ahead of `origin/main` on the expected branch, and fails terminally.
The step's target bead is still OPEN, with no producer disposition recorded.
Under the narrowed condition this case is untouched: no disposition is
recorded, so the demotion still applies and the root still resolves `fail`.

## Discriminator

- **Ladder tripping over its own success**: one root, its own worker closes
  the target `deliverable`, a later step in the SAME root re-observes that
  closed state and refuses. Target carries the disposition. Demotion is
  skipped.
- **Genuine duplicate dispatch** (`gc-e8lim`'s shape): two independent roots
  race the same target bead. The failing root's member fails while the target
  is still open (no disposition yet), or the target's disposition was written
  by the OTHER root's worker before this root's failing member is evaluated —
  either way, whichever root did not produce the recorded disposition still
  demotes normally; only the delivering root's own re-observation of its own
  delivery is protected.

## Alternatives considered

**Fix the `workspace-setup` step itself to treat "target already closed
deliverable" as a no-op success**, per the bead's first candidate fix. This is
the more surgical fix, but it lives in `mol-scoped-work.toml`, which is owned
by the city repo (`~/gas-city`), not this repo. Out of reach from here; left
for a city-side change.

**Timestamp-order comparison** (demote only if the member's failure closed
strictly after the target's close). Rejected: the target's close time is not
reliably comparable to the member's close time across store backends without
introducing a new metadata contract, and the producer-disposition check alone
already satisfies the fence in section 5 of the bead's dispatch contract
without needing one.

## Impact

`internal/dispatch/runtime.go`: `resolveFinalizeOutcome` gains a target-bead
lookup guarded by the recorded producer disposition. No other caller of
`terminalAbortScopeFailureMember` changes behavior;
`resolveFinalizeFailureDiagnostics` is unaffected because it is only reached
when the outcome is failing.
