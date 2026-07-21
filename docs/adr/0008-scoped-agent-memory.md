# ADR-0008: Scoped Agent Memory / Knowledge Primitive

**Date**: 2026-05-12
**Status**: retired (2026-07-07 audit)

> **2026-07-07 audit:** no MemoryScope/memory.Entry/`gc memory` built; the real
> need is served by `bd remember` + per-rig AGENTS.md/CLAUDE.md + digest orders.
> Retired. See `docs/design/adr-ratify-or-retire-2026-07-07.md`.
> **Deciders**: sjarmak, mayor
> **Depends on**: ADR-0002

## Context

Mayor's auto-memory lives at `~/.claude-homes/account5/.claude/projects/<dir>/memory/`. 60+ files capturing observed failure modes, user preferences, project state. **Workers and PLs can't read it.** v1.5 propagation hack: per-rig `AGENTS.md` / `CLAUDE.md` files maintained by hand (`dr-01zan7`, `gc-bc2c`, `gpk-k55`). Drift between mayor's memory and rig docs is inevitable.

Knowledge scope today is account-level (Claude Code home) or filesystem-level (project docs). Neither model captures:

- "This pitfall applies to all polecats working on gascity contributor PRs" (cross-cutting per role, not per home)
- "This decision applies to anyone working in this Slack thread" (per-conversation, not per-agent)
- "This fact is mine alone, do not propagate" (private)

## Decision

Memory becomes a first-class SDK primitive: `memory.Entry` with explicit scope:

```go
type MemoryScope struct {
    Kind       MemoryScopeKind   // global | agent | role | conversation | rig | private
    Targets    []string          // matches against agent names, roles, conversation_refs, rigs
    Lifetime   MemoryLifetime    // permanent | session | bead-scoped
}

type MemoryEntry struct {
    Scope  MemoryScope
    Name   string
    Type   MemoryType            // user | feedback | project | reference | observation
    Body   string                // markdown
    Author string                // who wrote it
    Refs   []string              // linked memories, beads, ADRs
}
```

Memories scoped to a Conversation (ADR-0002) auto-propagate to participants. Memories scoped to a role propagate to all agents in that role. The SDK enforces visibility — workers can't read `kind: private` entries from other agents.

`gc memory list --scope conversation:<ref>` returns scoped entries. `gc memory write --scope role:polecat --type feedback ...` writes role-scoped feedback that every polecat sees.

## Alternatives Considered

### Alt 1: Status quo — file-based per-home memory + manual propagation via AGENTS.md

- **Pros**: shipped; works for mayor-only
- **Cons**: every cross-agent knowledge transfer is manual (dr-01zan7 was filed exactly because the mayor↔polecat memory gap recurred); drift inevitable
- **Why not**: doesn't scale beyond mayor

### Alt 2: Shared filesystem memory (single dir all agents read)

- **Pros**: minimal change
- **Cons**: no scoping; every agent reads everything (context bloat); no per-role/per-conversation knowledge
- **Why not**: wrong granularity. Polecats don't need mayor's `feedback_codex_provider_resume_command` entry

### Alt 3: Embed memories in pack manifests (memories are pack assets)

- **Pros**: reuses pack distribution
- **Cons**: memories are per-instance observations, not per-pack defaults — wrong distribution model
- **Why not**: confuses pack-level static config with instance-level learned knowledge

## Consequences

### Positive

- AGENTS.md/CLAUDE.md hand-propagation retires — role-scoped memory propagates automatically
- Conversation-scoped memory enables "this thread's decisions" — useful for long PR threads where context matters
- Workers reading their rig's relevant pitfalls happens at session start via memory scope — no per-rig manual sync
- Mayor-pattern-miner can write role-scoped observations directly — no more rig-by-rig manual feedback files
- Private scope enables agents to keep working notes without leaking to peers

### Negative

- Migration: 60+ mayor memory files need scope annotation. Most are role:mayor or global; a subset (12 used for AGENTS.md content) become role:polecat
- SDK surface adds memory store + scope resolution. New API: `gc memory {list,read,write,delete} --scope ...`
- Storage: memory entries become bead-backed (`issue_type: memory`) for queryability. Estimated 100s of entries per workspace

### Risks

- **Scope visibility leaks** — bug in scope resolution shows polecats mayor's private notes. Mitigation: scope is enforced in the SDK at read-time; private entries are encrypted at rest with the agent's key
- **Memory bloat** — every observation becomes an entry. Mitigation: lifetime semantics (session, bead-scoped) auto-expire; mayor-pattern-miner consolidates redundant entries
- **Drift between scoped memory and physical CLAUDE.md** — during migration both exist. Mitigation: CLAUDE.md becomes a generated artifact from scoped memory (read-only), not the source of truth
