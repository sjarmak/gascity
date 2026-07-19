---
title: "Priority-Band Work Admission"
---

| Field | Value |
|---|---|
| Status | Proposed |
| Date | 2026-07-19 |
| Author(s) | sjarmak |
| Issue | #4322 |
| Supersedes | N/A |

## Summary

Gas City should admit ready work under one fixed policy: recovery of work already
owned by a session precedes fresh work; fresh work is ordered by numeric priority
band, then FIFO within a band. P0 is therefore admitted before P1 through P4 even
when candidates come from different pools or stores. A missing priority means P2.

This is a core scheduling contract and requires maintainer review. It is deliberately
not configurable: every reconciler, worker claim, and control-dispatch read must
agree on the same order.

## Contract and invariants

For fresh ready beads, the canonical order is:

1. priority ascending (`nil` is P2),
2. `created_at` ascending, and
3. bead ID ascending.

The following invariants apply end to end:

- Recovery is commitment, not new admission. In-progress work owned by the session
  ranks first, followed by open work already assigned to it; both precede every
  fresh band, including P0.
- P0 is globally visible and admitted before weaker fresh bands across templates
  and across the stores a worker can claim from. Provider-side bounded reads must
  be priority-first so their limit cannot hide P0.
- Once a federated claim attempt observes a fresh band, revalidation and lost-race
  fallback stay in that band. Losing a P0 race cannot silently admit P1.
- FIFO tie-breaking is deterministic within a band. Fair rotation may choose among
  templates whose next request is in the same band, but may not move a weaker band
  ahead of a stronger one.
- Existing capacity, configured minimum-session floors, nested caps, and the
  per-tick create budget remain hard constraints. The create budget restores
  recovery first, preserves floor guarantees and elastic progress, then compares
  the next fresh band across eligible templates.

## Cross-store identity

Bead IDs are unique only inside a physical store. Scheduling identity is therefore
the pair `(canonical store reference, bead ID)`, where the reference is `city` or
`rig:<name>`. Collection deduplicates aliases of the same physical store, but never
deduplicates independent stores merely because they contain the same bead ID or
payload. Priority, creation time, title, and launch binding must all come from the
same pair. Recovery carries the owning store reference through reconciliation so a
replacement session cannot bind to a same-ID bead in another store.

## Recovery precedence

Recovery has two ordered tiers: resume a known live session that owns in-progress
work, then wake a known configured identity for work already assigned to it. Fresh
unassigned routed work follows under priority-band FIFO. This precedence applies in
worker selection, nested-cap planning, and scarce session-create allocation; a P0
arrival does not revoke capacity already committed to recovery.

## Scope and non-goals

In scope are built-in pool-demand collection, reconciler admission and create-budget
allocation, federated `gc hook --claim`, and bounded control-dispatch readiness.
These paths share the priority/default helpers and the work-query predicates.

Out of scope are a configurable scheduler, weighted priorities, deadlines, aging or
priority promotion, preemption of running work, changing bd's priority values, and
changing cap, floor, or elastic-reserve policy. This design also does not make
unreachable stores available or turn partial reads into complete ones.

## Rollout and testing

Land the contract and behavior as one focused priority-admission change, held for
maintainer review. No config migration or default flip is required because the
policy is fixed. Rollback is a code revert; bead data remains compatible.

Focused tests must cover the canonical comparator and nil-as-P2 rule; mixed-band
bounded reads with P0 beyond weaker rows; cross-template scarce-budget admission;
recovery-before-fresh under nested caps and the create budget; floor and elastic
progress; federated cross-store P0 selection; same-ID independent stores versus
physical-store aliases; store-preserving recovery; and lost-claim revalidation that
cannot cross bands. Existing work-query golden fixtures pin provider query ordering.
Run focused Go tests during implementation, then `make check` and the full required
suite before shipping. Run `make check-docs` for this design capture.
