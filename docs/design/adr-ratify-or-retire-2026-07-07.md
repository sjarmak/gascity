# ADR ratify-or-retire dossier — 2026-07-07

Prepared for Stephanie's decision pass (morning-ledger flag; audited by a
read-only agent from the stephanie-adhoc session, all 9 ADRs against
`/home/ds/gascity-main` code and `/home/ds/gas-city` operations).

**One finding frames the batch:** of the nine, only ADR-0009 (dated
2026-06-18) was actually built into the `gc` binary. The other eight (all
2026-05-12, the original "v2 pillars" batch) never left `proposed`, and the
city still runs on the v1.5 workaround each one named — every
reaper/surfacer/wake-order they promised to retire is still live in
`orders/` + `bin/` (`close-gate-reaper` cron `5 * * * *`,
`pl-human-gate-surface`, `wake-mayor-on-blocker-close`,
`mail-redirect-to-mayor`, `slack-binding-reaper`, `pl-loop-close`). Two ADRs
are also contradicted by settled exclusions in `gascity-main/AGENTS.md`:
"No skills system" and "No capability flags." The code cites `ADR-0009`
(14×) and an unrelated upstream `ADR-0013` (provider-health-gate),
confirming the code's ADR namespace diverged from this city-side series;
only 0009 bridged both.

---

## ADR-0001 — Skills as a First-Class Primitive — RETIRE

- _Impl:_ ABSENT as decided. No `skill.Registry`, no typed I/O schema;
  `requires_skills` appears nowhere in Go or any city formula. What shipped
  is the ADR's own rejected Alt 1 — per-agent filesystem materialization
  (`internal/materialize/skills.go:1-40`, `gc skill list`).
  `gascity-main/AGENTS.md` lists "No skills system — the model IS the skill
  system" as a permanent exclusion.
- _Ops:_ City surfaces skills via materialized `.claude/skills/` sinks; no
  skill-presence gates.
- _Reason:_ Unbuilt and contradicted by a settled exclusion; the approach it
  rejected is what's actually used.

## ADR-0002 — Conversation as a First-Class Primitive — REVISE

- _Impl:_ Realized in spirit but DIFFERENTLY. `internal/extmsg/` gives a
  provider-neutral `AdapterRegistry` keyed by `(Provider, AccountID)` over a
  `TransportAdapter` interface (`adapter_registry.go:5-22`), plus
  `ConversationRef`, participant beads, `EnsureChildConversation`. But no
  `internal/conversation` package, no bead-backed `issue_type: conversation`,
  no `voice`/`participants[]` primitive.
- _Ops:_ City still runs `slack-binding-reaper` + `mail-redirect-to-mayor`.
- _Reason:_ The provider-neutral adapter goal was met under a different shape
  (extmsg); rewrite the ADR to ratify what exists rather than the unbuilt
  `conversation.Conversation`. Root of the 0006/0008 dependency chain.

## ADR-0003 — Typed Bead Metadata + Close-Gate Contracts — REVISE

- _Impl:_ PARTIAL. Typed-metadata half exists as a centralized key registry
  (`internal/beadmeta/keys.go`, `values.go`, `kindsets.go`) — but explicitly
  data-only (`work_record_gate.go:41-42`). The decided mechanism —
  declarative TOML schemas per `issue_type`, validated at
  `bd update --status=closed` write-time, close-gate-reaper retires — did
  NOT happen. Only write-time close gate is the consumer-specific, warn-only
  `cmd/gc/work_record_gate.go` (that's 0009's).
- _Ops:_ `orders/close-gate-reaper.toml` still fires hourly — the reaper 0003
  meant to retire is load-bearing.
- _Reason:_ Typed-keys shipped; declarative-schema + SDK write-time
  validation did not. Still wanted (0005 depends on it) but needs rescoping
  to the consumer-gate pattern that actually emerged.

## ADR-0004 — Agent Activation Model (Subscriptions + Goals) — RETIRE

- _Impl:_ ABSENT. No `Subscription`/`Goal`/`EventPattern` types, no
  `gc subscription`/`gc goal` (zero grep hits).
- _Ops:_ Every wake mechanism it targeted is still a live cron order:
  `wake-mayor-on-blocker-close`, `wake-mayor-on-slung-close`,
  `mail-redirect-to-mayor`, `nudge-on-route`, `routed-bead-nudger`,
  `pl-loop-close`, `pl-loop-close-timeout`.
- _Reason:_ 6-10-type speculative surface, zero adoption after ~2 months;
  city runs fine on orders; standing-`Goal` intent sits uneasily against
  "keep judgment out of Go." Re-propose narrowly (event subscriptions only)
  if a need recurs.

## ADR-0005 — Multi-step Formula Composition with Typed Gates — REVISE

_(depends on 0003)_

- _Impl:_ ABSENT as designed. No `[[gates]]`, no
  `on_match: halt_to_conversation`, no `halt_summary_template`.
  `internal/formula/condition.go` handles optional-step filtering +
  loop-until; `types.go:670-682` `Gate` is an async wait
  (`gh:run`/`timer`/`human`/`mail`), not an evidence-predicate halt-routing
  gate.
- _Ops:_ Flagship `formulas/mol-pr-from-issue.formula.toml` is now 810 lines
  (from the ADR's cited 524) with ~103 inline bash/jq/if-gate lines — the
  workaround grew, not shrank. No city formula uses `[[gates]]`.
- _Reason:_ Pain is real and worsening, so the decision is still wanted —
  but gated on 0003's unrealized typed-evidence half. Revise as a pair with 0003.

## ADR-0006 — Human-Gate as a First-Class Step Type — REVISE

_(depends on 0002 + 0004)_

- _Impl:_ PARTIAL/different. `Gate{Type:"human"}` async gate exists
  (`types.go:673-682`) creating a blocking gate issue — but as a gate-type,
  not `step.type="human_gate"` with auto-surface/auto-resume. Both deps
  (0002, 0004) are unrealized, so neither auto-behavior can exist.
- _Ops:_ City still runs `bin/pl-human-gate-surface` +
  `orders/pl-human-gate-surface.toml` + `-recheck.toml`.
- _Reason:_ A human-gate primitive partially exists, but its value
  (auto-surface + auto-resume) is structurally blocked on 0002/0004. Revise
  to reflect the gate-type that shipped.

## ADR-0007 — Formula Introspection + Dispatch-Routing Negotiation — RETIRE

_(depends on 0001 + 0004)_

- _Impl:_ ABSENT. No `intent_patterns`, no `[capability]` block, no
  `gc dispatch suggest`. `gc formula list` exists only as a plain lister
  (`internal/sling/sling.go:918`). Contradicted by AGENTS.md "No capability
  flags — a sentence in the prompt is sufficient."
- _Ops:_ PL dispatch stays hand-written brief tables +
  `mol-ad-hoc-from-mayor` escape valve.
- _Reason:_ Contradicted by a settled exclusion; both deps dead (0001
  retire, 0004 absent).

## ADR-0008 — Scoped Agent Memory / Knowledge Primitive — RETIRE

_(depends on 0002)_

- _Impl:_ ABSENT. No `MemoryScope`/`memory.Entry`, no `gc memory`. Actual
  persistence is `bd remember` (per AGENTS.md); cross-agent propagation met
  by per-rig `AGENTS.md`/`CLAUDE.md` + `mem-digest`/`memory-audit-issues`
  orders.
- _Ops:_ Rig docs still hand-maintained; mayor memory still account-local.
- _Reason:_ Large speculative surface with no movement; genuine need already
  served by `bd remember` + digests.

## ADR-0009 — Work Record: One Claim, Structured Outcome — RATIFY

_(ledger status is stale)_

- _Impl:_ FULLY IMPLEMENTED and self-citing. Claim-lock + `gc.work_branch`
  stamp + `bead.claim_rejected` event in `cmd/gc/cmd_hook_claim.go` (guard
  :225-228, `stampHookWorkBranch` :420-438, `hookEmitClaimRejected`
  :524-545). Structured close gate `cmd/gc/work_record_gate.go` — typed
  `gc.work_outcome ∈ {shipped,no-op,blocked,abandoned}`, `shipped` requires
  a commit reachable on the stamped branch (`gitCommitReachableOnBranch`
  :108-116), warn-only→enforce via `GC_WORK_RECORD_ENFORCE` — wired into the
  real close path at `cmd/gc/cmd_bd.go:290`. One faithful deviation: key is
  `gc.work_outcome`, not the ADR's `gc.outcome`, renamed to avoid colliding
  with the pre-existing control-plane `gc.outcome` (pass/fail) — documented
  at `internal/beadmeta/values.go:56-66`.
- _Ops:_ City relies on it (claims stamp branches, closes are gated);
  `close-gate-reaper` still runs as belt-and-suspenders since the SDK gate
  defaults warn-only — expected, not a contradiction.
- _Reason:_ Implemented, wired, tested, actively guiding work; `proposed` is
  stale. Ratify; amend the ADR text to record the `gc.work_outcome` key
  name.

---

## RFC-formula-registry-architecture.md — ALIVE but parked

Status still "Draft (RFC, awaiting comment)," authored 2026-05-12 (same
batch). Its core pain — formulas stored as code on `bd-<bead>` rig branches,
so config edits are invisible across worktrees and need manual
multi-worktree merge recovery — is unaddressed and still real
(`mol-pr-from-issue.formula.toml` is an 810-line file living on rig
branches). It's orthogonal in mechanism to ADR-0005 (RFC = _where_ formulas
live; 0005 = _how_ gates compose) but shares the same victim file, which is
why **dr-j0d.4's `mol-pr-*` instruction-block consolidation is blocked**:
consolidating is unsafe while formulas are branch-coupled (RFC) and gate
logic can't be lifted out of inline bash (0005). Treat the RFC + 0005 + 0003
as one parked cluster; none should be actioned in isolation.

---

## Suggested decision summary

Ratify 0009 (flip to `accepted`, note the key rename). Retire 0001, 0004,
0007, 0008 (unbuilt + contradicted/dead-dep). Revise 0002, 0003, 0005, 0006
as a dependency-ordered cluster — 0002 and 0003 are the roots; 0005 and 0006
can't advance until they land — rewriting each to describe what actually
emerged (`extmsg`, `beadmeta` key registry, async `Gate`) instead of the
05-12 designs.
