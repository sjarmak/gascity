# ADR-0005: Multi-step Formula Composition with Typed Gates

**Date**: 2026-05-12
**Status**: proposed — not implemented, needs revision (2026-07-07 audit)

> **2026-07-07 audit:** no `[[gates]]` evidence-predicate halt-routing shipped;
> the `Gate` type that exists is an async wait, and `mol-pr-from-issue` grew to
> ~810 lines of inline bash gates. Decision still wanted but gated on 0003's
> unrealized typed-evidence half; revise the pair together.
> See `docs/design/adr-ratify-or-retire-2026-07-07.md`.
> **Deciders**: sjarmak, mayor
> **Depends on**: ADR-0003

## Context

Today a multi-step pipeline (triage → pick → pr-start → ship → open-PR) is either three independent formulas stitched together by Slack messages OR one monolithic formula. No SDK mechanism for **composing formulas with typed per-step gates**. `gpk-g5d` (mol-pr-from-issue) is the v1.5 workaround: a 524-line formula that hard-codes the chain with bash-and-jq gates. Every macro chain reimplements the same shape: read prior step's evidence, decide halt or continue, write summary on halt.

The gate logic depends on **structured evidence** (verdict, artifact path, halt reason). With ADR-0003 typed metadata enforced at write-time, gates can inspect typed fields rather than re-parsing close_reason text.

## Decision

Formulas gain a `[[gates]]` block at the step boundary. Each gate declares:

- `after`: the step whose evidence is inspected
- `condition`: a typed predicate over the prior step's evidence (e.g. `evidence.reviewer_verdict == "pass"`, `metadata.gc.halt_chain != "true"`)
- `on_match`: `continue` (default) | `halt_to_conversation` | `halt_to_mayor` | `branch_to_step`
- `halt_summary_template`: when halting, template a `summary_for_human` for surfacing via ADR-0002 conversation primitive

Gates are first-class formula constructs, not bash-and-jq inside step bodies. SDK enforces gate evaluation between steps using the typed evidence schema (ADR-0003).

## Alternatives Considered

### Alt 1: Status quo — gates as bash-and-jq in step bodies (mol-pr-from-issue pattern)

- **Pros**: shipped; works for one-off macros
- **Cons**: every macro reimplements the same shape; gate logic is opaque to the SDK; typed-evidence guarantees (ADR-0003) don't flow into gate decisions
- **Why not**: doesn't compose. mol-pr-from-issue is 524 lines mostly because gates are inlined

### Alt 2: Programmatic formula composition (Go SDK builder pattern)

- **Pros**: maximum expressiveness; reuses Go type system
- **Cons**: formulas-as-TOML is the established contributor surface; switching to Go-only blocks formula authoring by polecats (which can't run Go-build)
- **Why not**: wrong contributor surface. TOML stays primary; SDK provides the gate primitive in TOML

### Alt 3: External orchestrator (Temporal, Airflow, Step Functions)

- **Pros**: battle-tested workflow engines
- **Cons**: heavy infra; doesn't model the agent-reasoning lifecycle that's core to Gas City
- **Why not**: wrong scope; we want a small SDK primitive that composes with Subscriptions (ADR-0004)

## Consequences

### Positive

- mol-pr-from-issue rewrites as ~150 lines instead of 524 (gate logic moves to declarative `[[gates]]`)
- Halt-and-surface becomes uniform across all macros (no per-formula bash drift)
- Gates inspect typed evidence (ADR-0003) — no re-parsing close_reason text
- Halt actions integrate with ADR-0002 conversation primitive — `halt_to_conversation` posts to the root bead's `conversation_ref`
- New macros (e.g. triage→author→ship per dispatch escape valve, codeprobe research chains) become declarative

### Negative

- Migration: existing formulas with inline gate logic (mol-pr-from-issue, mol-adopt-pr) refactor to use the primitive
- Limits gate expressiveness — predicate language must be deliberately constrained (Lua/CEL/JSON-logic, not full Bash). Mitigation: support escape hatch `exec` predicate for one-off cases

### Risks

- **Predicate language sprawl** — gate conditions grow into a mini-DSL. Mitigation: pick CEL (Common Expression Language) — proven, sandboxed, typed; rejects ad-hoc DSL design
- **Gate failure observability** — when a gate halts, debugging requires knowing WHICH gate, WHICH predicate, WHICH evidence. Mitigation: gate evaluation logs to a structured `gate.evaluated` event per ADR-0004
