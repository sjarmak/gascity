# Architecture Decision Records

ADRs for Gas City v2 — foundational SDK primitives surfaced during v1.5
application work. Drafted city-side in this workspace; planned to PR
into `gastownhall/gascity:docs/adr/` once ratified.

Parent design context: city bead `dr-i06w09` (Lexler patterns →
Gas City primitives map). The "v2.0 focus areas" section of that bead
identifies seven architectural pillars; ADRs are sequenced from
foundational outward.

| ADR | Title | Status | Date |
|-----|-------|--------|------|
| [0001](0001-skills-as-primitive.md) | Skills as a First-Class Primitive | proposed | 2026-05-12 |
| [0002](0002-conversation-as-primitive.md) | Conversation as a First-Class Primitive | proposed | 2026-05-12 |
| [0003](0003-typed-bead-metadata.md) | Typed Bead Metadata + Close-Gate Contracts | proposed | 2026-05-12 |
| [0004](0004-agent-activation-model.md) | Agent Activation Model — Subscriptions and Standing Intent | proposed | 2026-05-12 |
| [0005](0005-formula-composition-typed-gates.md) | Multi-step Formula Composition with Typed Gates | proposed | 2026-05-12 |
| [0006](0006-human-gate-step-type.md) | Human-Gate as a First-Class Step Type | proposed | 2026-05-12 |
| [0007](0007-formula-introspection-dispatch.md) | Formula Introspection + Dispatch-Routing Negotiation | proposed | 2026-05-12 |
| [0008](0008-scoped-agent-memory.md) | Scoped Agent Memory / Knowledge Primitive | proposed | 2026-05-12 |
| [0009](0009-work-record-claim-lock-structured-outcome.md) | Work Record — One Work ID, One Claim, Structured Outcome | accepted | 2026-06-18 |
| [0010](0010-scheduler-bound-ephemeral-workers.md) | Scheduler-Bound Ephemeral Workers — ready → bind → spawn | proposed | 2026-07-17 |
| [0011](0011-amp-agent-runtime-adapter.md) | Shared Agent Contract with an Amp-Native Runtime Adapter | accepted | 2026-07-17 |

## Sequencing

Four foundational pillars are independent of each other (designed in parallel):

- ADR-0001 (Skills)
- ADR-0002 (Conversation)
- ADR-0003 (Typed Bead Metadata)
- ADR-0004 (Agent Activation Model)

Four dependent pillars come after:

- ADR-0005 (Formula Composition + Gates) depends on ADR-0003
- ADR-0006 (Human-Gate Step Type) depends on ADR-0002 + ADR-0004
- ADR-0007 (Formula Introspection + Dispatch) depends on ADR-0001 + ADR-0004
- ADR-0008 (Scoped Memory) depends on ADR-0002

## Lifecycle

```
proposed → accepted → [deprecated | superseded by ADR-NNNN]
```

- **proposed**: under discussion, not committed
- **accepted**: in effect; reflected in SDK + city operating model
- **deprecated**: no longer relevant
- **superseded**: replaced by a newer ADR (link the replacement)
