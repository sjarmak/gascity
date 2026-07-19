---
title: "Durable Disarmed Work"
---

| Field | Value |
|---|---|
| Status | Proposed |
| Scope | The `gc.disarmed` do-not-execute contract across work orchestration |
| Issue | `gc-u6an` |

## Decision

`gc.disarmed` is a durable, mechanical do-not-execute marker on a work bead.
When its effective value is true, Gas City must not claim, assign, continue,
count, wake for, or nudge execution of that bead. The marker is independent of
status, readiness, dependencies, routing, and assignee.

This is stronger than `status=blocked`. Status is workflow state and can change
through dependency resolution or reconciliation; it is not a durable safety
interlock. Disarm remains metadata on the bead until an operator clears it.
No code infers disarm from title, description, reason text, or other prose.

## Value contract

The canonical key is `gc.disarmed`.

- Boolean `true` and a case-insensitive, whitespace-trimmed string `"true"`
  are disarmed.
- An absent key, boolean `false`, an empty string, and string `"false"` are
  armed. Clearing the value therefore explicitly re-arms the bead.
- JSON `null` is armed because existing string-shaped metadata decoders
  collapse it to the same empty value as an explicit clear. Raw and string
  readers must agree rather than make claim and demand decisions differently.
- Any other present, non-empty value fails safe as disarmed. A deliberately
  written interlock that cannot be parsed must not permit execution.

All readers use one semantic predicate, including equivalent jq generated for
shell work queries. Tests must pin parity across raw JSON, string-shaped
metadata, and generated jq because bd may type-infer command-line metadata as a
JSON boolean.

## Required path coverage

Disarm is an execution eligibility predicate, not a filter that belongs to one
command. It applies at every point that can advertise or preserve executable
work:

| Path | Required behavior for a disarmed bead |
|---|---|
| Claim | Remove it from every hook candidate source before readiness or claim selection. A direct work-query result cannot bypass this check. |
| Demand | Exclude it from canonical routed work, legacy route fallbacks, ephemeral fallbacks, control-dispatch demand, named-session direct demand, and assigned-work pool demand. It contributes zero desired capacity. |
| Wake and retention | Assigned-work probes and wake-set inputs ignore it. It neither wakes an asleep session nor keeps a session awake or cancels a drain. |
| Continuation | Do not preassign a disarmed continuation sibling and do not select it as the next runnable step. Re-arming makes it eligible on a later observation; disarming does not rewrite graph structure. |
| Assignment | Exclude it from orchestration's executable assigned-work views, including first-work selection and session/pool scope filtering. Existing assignment metadata may remain for cleanup and diagnosis. |
| Nudge and serve | Do not issue startup, stalled-claim, or control-ready nudges for it, and do not serve it through a row-returning work query. |

The invariant is bidirectional: a bead that claim refuses must also contribute
no demand. Otherwise the controller can repeatedly spawn a worker that finds no
work, exits idle, and is spawned again. Filtering only the final claim site is
therefore insufficient.

Migration and compatibility tiers are part of the contract. A fallback query
is still an execution path; it may not return or count a disarmed row. Queries
that inspect multiple rows must retain an armed peer behind a disarmed head
rather than treating the first excluded row as an empty tier.

## Cache and reconciliation safety

The backing bead store is authoritative. Cache event payloads may contain only
changed metadata, so applying a partial event must not erase a cached
`gc.disarmed` value merely because the key is omitted. If a payload explicitly
contains the key, its value wins, including an explicit clear.

The current event wire shape cannot distinguish an omitted key from an unset
tombstone. Until that contract carries field-presence/tombstone information,
an absent key in a metadata patch preserves the cached interlock. This chooses
the fail-safe error: stale disarm delays execution, while stale re-arm executes
work an operator stopped. A full authoritative reconciliation must replace the
cached metadata and converge both setting and clearing the marker. Cache
conflict or verification failure must decline the cached eligibility result and
fall back to a live authoritative read; it must not synthesize an armed row.

Read or decode errors follow the same safety direction. A present but
unreadable value is disarmed. A store-query failure is not evidence of zero
work or of an armed bead; existing partial-store reconciliation rules continue
to defer destructive lifecycle decisions until an authoritative snapshot is
available.

## Re-arming

Re-arming is a metadata operation: clear/unset `gc.disarmed` or write false.
Gas City does not restore status, assignee, route, dependencies, continuation
position, or a session automatically. On the next authoritative observation,
the bead participates according to those ordinary fields. This keeps disarm
reversible without making it a hidden workflow transition.

## Non-goals

- Replacing `blocked`, dependency, cancellation, or close semantics.
- Deleting, closing, unassigning, or rerouting a bead when it is disarmed.
- Hiding disarmed work from listing, diagnostics, lifecycle cleanup, audit, or
  repair tools. The exclusion is from execution orchestration only.
- Inferring policy from a disarm reason or deciding whether work *should* run.
- Adding a new scheduler state, config flag, CLI command, event payload, or
  cache wire protocol in this slice.
- Guaranteeing immediate cross-process observation beyond the backing store,
  event delivery, and normal reconciliation model.
