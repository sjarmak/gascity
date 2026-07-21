# ADR-0009: Work Record — One Work ID, One Claim, Structured Outcome

**Date**: 2026-06-18
**Status**: accepted (2026-07-07 — implemented and in use)

> **2026-07-07 audit:** fully implemented and self-citing (claim-lock +
> `gc.work_branch` stamp in `cmd/gc/cmd_hook_claim.go`, structured close gate in
> `cmd/gc/work_record_gate.go`, wired at `cmd/gc/cmd_bd.go`). One faithful
> deviation: the key is `gc.work_outcome`, not the ADR's `gc.outcome`, to avoid
> a collision (see `internal/beadmeta/values.go`). Ratified.
> See `docs/design/adr-ratify-or-retire-2026-07-07.md`.
> **Deciders**: sjarmak, mayor

## Context

Every task in Gas City is already a bead with a unique ID — that _is_ the work ID. Each work bead carries some structured linkage in typed metadata (`gc.session_name` = who claimed it, `gc.work_dir` = the worktree it ran in), and its outcome lives in the close-reason. There is a `close-gate-reaper` that can enforce evidence gates on close. So the skeleton of work-tracking exists.

But the linkage **work ID → work done → outcome** is held together by _convention_, not by an enforced invariant, and three gaps surfaced concretely on 2026-06-18:

1. **No one-active-claim invariant.** Bead `gc-typpc` ended up _concurrently claimed by four polecat slots_, three of them editing the _same_ shared branch (`bd-gc-typpc`) with divergent uncommitted work — a lost-work / first-commit-clobbers hazard. The root enabler is the graph.v2 finalize-gap (ADR-adjacent fix `gc-8h5ls`): a completed workflow's root never closes, re-routes to the pool hook, and a fresh worker re-claims the same bead. The work ID was singular; the dispatch layer fragmented it into N uncoordinated claims.

2. **Work is not durably bound to the bead.** A worker stages changes in its worktree but the _branch_ is not recorded on the bead at claim time, and "drain-without-commit" closes (observed repeatedly, e.g. the codeprobe worker class) leave **no artifact at all**. There is no machine-checkable link from a closed bead back to the commit that satisfied it.

3. **Outcome is free-text, not typed.** A close-reason of "did the thing" and one of "shipped, `commit=4419182` on `bd-gc-8h5ls`, gates green" are indistinguishable to the system. Nothing can aggregate "how many beads shipped a verified commit vs were abandoned vs no-op'd."

This matters most for **observability and eval**: without a typed, enforced work record, we cannot reliably answer "what work was done, by whom, with what outcome, tied to what artifact" — which is the substrate every eval, audit, and throughput metric needs. The mail system has the same bead substrate (`type="message"` beads in `.gc/beads.json` for city, Dolt per-rig), so the same record discipline generalizes.

## Decision

Introduce a **Work Record** contract enforced by the SDK at the claim and close boundaries. A work bead's lifecycle gains three hard invariants:

1. **One active claim per bead (claim-lock).** At `cmd/gc/cmd_hook_claim.go` claim time, a bead transitions to claimed only if it has no other live claimant. A second claim attempt on a live-claimed bead is rejected (idempotent no-op + structured event `WorkBeadClaimRejected{bead, existing_claimant, attempted_claimant}`), not silently fan-dispatched. The finalize-gap fix (`gc-8h5ls`) removes the dominant fan-out _source_; this makes single-claim a _hard invariant_ regardless of source.

2. **Bind the artifact at claim time.** Alongside the existing `gc.work_dir`, stamp **`gc.work_branch`** on the bead when a worker claims it. The branch is the durable handle from the bead to its work; reviewers and the close gate read it.

3. **Structured, gated outcome on close.** Closing a work bead requires a typed outcome, not free text:

   ```
   gc.outcome = shipped | no-op | blocked | abandoned
   gc.commit  = <sha>            # required when outcome=shipped
   gc.branch  = <gc.work_branch> # required when outcome=shipped
   gc.verification = <gate result / "manual" / link>
   ```

   A close with `outcome=shipped` but no commit on the stamped branch is **rejected** by an extension of the existing `close-gate-reaper` (or, preferably, an SDK pre-close check). `no-op` / `blocked` / `abandoned` require a one-line reason but no artifact. The human-readable close-reason stays, but the typed fields are the source of truth for tooling.

Scope is deliberately narrow: **claim-lock + work-branch stamp + structured-close gate.** No new primitive, no new store, no workflow-engine redesign — it rides the existing typed-bead-metadata mechanism (ADR-0003) and the existing close-gate machinery.

## Alternatives Considered

- **Status quo (convention only).** Rejected — today's gc-typpc race and the recurring drain-without-commit closes are the direct cost; eval/observability cannot be built on convention.
- **Enforce in the reconciler.** Rejected — the reconciler is forbidden from closing/owning work beads (invariant pinned by `session_reconciler_test.go:1302`); recovery belongs to pack-side subscribers and the claim/close hooks, not the reconcile loop.
- **Full provenance/event-sourcing of every work step.** Deferred as over-scope (YAGNI) — the three invariants above capture the work→outcome link that eval needs; richer provenance can layer on later if a concrete need appears.
- **Make Work a new first-class primitive.** Rejected — a bead already _is_ the work ID; this is a contract on the bead lifecycle, not a new primitive.

## Consequences

- **Positive:** every task → one ID → one claim → a stamped branch → a typed outcome. Fan-out races become structurally impossible; drain-without-commit becomes a rejected close; eval/audit/throughput tooling gets a machine-checkable substrate. Pairs with and reinforces `gc-8h5ls` (finalize-gap).
- **Cost:** a claim that legitimately needs hand-off must release before re-claim (`bd update --unassign` / explicit reassign) — already the documented norm. Workers must stamp a branch and a typed outcome; the formulas/`mol-*` close steps need a one-time update to emit the typed fields.
- **Migration:** additive metadata; existing open beads are unaffected until their next claim/close. The close gate ships in warn-only mode first (logs violations), then enforces.

## Implementation surface (for the dispatched work)

- `cmd/gc/cmd_hook_claim.go` — single-claim guard + `gc.work_branch` stamp + `WorkBeadClaimRejected` event.
- close path (`cmd/gc/*` close + `bin/close-gate-reaper`) — typed-outcome validation; warn-only → enforce.
- `internal/beadmeta` — register `gc.work_branch`, `gc.outcome`, `gc.commit`, `gc.branch`, `gc.verification` keys (ADR-0003 typed metadata).
- `mol-*` formula close steps — emit the typed outcome fields.
- Tests: a second-claim is rejected; a `shipped` close with no commit-on-branch is rejected; a `no-op` close passes.
