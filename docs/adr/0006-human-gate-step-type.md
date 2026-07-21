# ADR-0006: Human-Gate as a First-Class Step Type

**Date**: 2026-05-12
**Status**: proposed — partially implemented, needs revision (2026-07-07 audit)

> **2026-07-07 audit:** a `Gate{Type:"human"}` async gate exists, but not the
> `step.type="human_gate"` auto-surface/auto-resume decided here; both deps
> (0002, 0004) are unrealized, so `bin/pl-human-gate-surface` still runs.
> Revise to reflect the gate-type that shipped.
> See `docs/design/adr-ratify-or-retire-2026-07-07.md`.
> **Deciders**: sjarmak, mayor
> **Depends on**: ADR-0002, ADR-0004

## Context

The human-gate pattern recurs across formulas: a step where the polecat halts and waits for a human decision (mol-adopt-pr.human-gate, mol-pr-from-issue's gate-after-pr-start, etc.). Today it's expressed as a "magic step bead" with a hand-written title like "Human review checkpoint" — the polecat creates the bead, doesn't claim it, mails "human", and idles.

This is fragile. Pain points:

- Detection regex (`*.human-gate` for `gc.step_ref`) is convention-based; new formulas can typo it
- Surfacing (`bin/pl-human-gate-surface`) is a separate handler that walks the parent chain to find originating_slack
- Resume mechanics: polecat doesn't auto-wake when the human closes the gate bead — relies on the polecat seeing the bead status change next tick OR mayor manually nudging
- Polecat needs to compose the "what I need from you" prompt manually each time

## Decision

New step type `step.type = "human_gate"`. Declared in formula TOML:

```toml
[[steps]]
id = "approve-merge"
type = "human_gate"
title = "Need your call on merge strategy"
prompt = """
Polecat completed review. Verdict: {{evidence.reviewer_verdict}}.
Options:
  1. Squash merge
  2. Merge commit
  3. Decline (close gate with --status=skipped)
"""
surface_to = "{{root.conversation_ref}}"   # uses ADR-0002 Conversation primitive
resume_on = "step.closed"                   # subscription per ADR-0004
```

The SDK handles:

- Bead creation with canonical title + structured metadata (`gc.step_type = "human_gate"`)
- Surfacing via Conversation adapter (no per-handler routing logic — uses ADR-0002)
- Resume: an implicit Subscription wakes the parent molecule when the gate closes (uses ADR-0004)

## Alternatives Considered

### Alt 1: Status quo — magic step beads + separate surfacing handler

- **Pros**: shipped (dr-xbs45x)
- **Cons**: every component (polecat, surfacing handler, resume nudge) reimplements the convention; new formulas drift
- **Why not**: convention-as-primitive belongs in the type system

### Alt 2: Human-gate as a regular step + formula-level metadata flag

- **Pros**: lighter primitive; reuses existing step lifecycle
- **Cons**: doesn't make the gate auto-surface or auto-resume — handlers still required
- **Why not**: doesn't fix the per-handler reimplementation problem

## Consequences

### Positive

- One canonical implementation of "polecat pauses for human input" — no per-formula drift
- Auto-surfacing via ADR-0002 — works with slack, mail, github comment threads uniformly
- Auto-resume via ADR-0004 subscription — gate close wakes the molecule's owning agent
- `bin/pl-human-gate-surface` + `bin/pl-human-gate-surface-recheck` retire
- New formulas declare human-gates declaratively; no risk of typo'd `gc.step_ref` missing the regex

### Negative

- Migration: mol-adopt-pr's existing human-gate (a regular step + convention) refactors to `type = "human_gate"`
- Step type system grows from implicit (any step is a "regular step") to explicit (types: regular, human_gate, ...)

### Risks

- **Step type explosion** — once one new type lands, pressure for more (auto_gate, scheduled_step, etc.). Mitigation: explicit type system; only add types with concrete recurring patterns
