# ADR-0011: Shared Agent Contract with an Amp-Native Runtime Adapter

**Date**: 2026-07-17
**Status**: accepted
**Deciders**: Stephanie, Codex

## Context

The workspace has accumulated valuable operating behavior in three different
runtime shapes: neutral engineering rules, Claude skills/agents/commands/hooks,
and Codex skills/hooks. Moving the always-on mayor to Amp without those assets
would remove safety and coordination behavior. Copying everything into a third
tree would instead create three drifting sources of truth and would bloat Amp's
prompt and tool surface.

Amp provides the primitives needed for a native integration: global and project
`AGENTS.md`, direct Claude-skill compatibility, additional skill paths,
long-lived resumable threads, built-in child agents, and plugin lifecycle
events. Gas City's built-in Amp provider declares thread resume support, but
Gas City does not yet install Amp hooks (upstream gap #672), so a fresh Amp
thread was not being bound back to the Gas City session bead.

## Decision

Use one runtime-neutral operating contract and thin runtime adapters:

- Amp always loads the shared `house-rules.md` through
  `~/.config/amp/AGENTS.md`.
- Amp reads Claude skills directly and adds `~/.codex/skills` through
  `amp.skills.path`; skill bodies remain on-demand.
- A global Amp plugin exposes existing Claude specialist prompts through one
  `delegate_specialist` child-thread tool and exposes legacy prompt commands in
  the command palette. The files remain authoritative in `~/.claude`.
- Amp carries the healthy global `codegraph` and Sourcegraph MCP servers.
  High-memory or repository-specific servers such as `scix` and `sg-local`
  remain project-scoped rather than becoming mayor-wide background processes.
- A Gas City project plugin binds the Amp thread ID as the session's resume key,
  injects mail/nudges at turn start, and ports worktree guards and verification
  hooks to Amp lifecycle events.
- New mayor threads start in Amp `high` mode and resume with
  `amp ... threads continue {{.SessionKey}}`. Amp fixes a thread's mode at
  creation, so the migrated medium-mode thread remains medium to preserve its
  complete context; deep planning and verification route to high-mode child
  threads.
- Tool permissions use first-match rules: publishing, destructive Git,
  privilege escalation, and recursive forced deletion require approval; a
  final catch-all allows routine internal orchestration without interruption.
- One Amp thread may be long-running only while it represents one coherent
  mission. Durable state and authorization remain in beads, repository
  documents, and established records rather than only in thread memory.

## Alternatives Considered

### Copy Claude and Codex configuration into Amp directories

- **Pros**: Straightforward initial migration.
- **Cons**: Immediate source-of-truth drift, duplicated maintenance, and stale
  runtime-specific tool instructions.
- **Why not**: It makes every future improvement a three-way synchronization
  problem.

### Use Amp with only the project `AGENTS.md`

- **Pros**: No adapter code.
- **Cons**: Loses global rules, Codex-only skills, specialist prompts, command
  workflows, lifecycle safety, and reliable thread resume binding.
- **Why not**: The mayor would have longer memory but weaker execution.

### Recreate every specialist as a custom fixed-model Amp agent

- **Pros**: Exact role names and dedicated models.
- **Cons**: Large tool surface and frozen model routing that bypasses Amp's
  built-in mode evolution.
- **Why not**: One role-selecting adapter over Amp's built-in modes preserves
  the prompts while retaining Amp's model selection.

## Consequences

### Positive

- Claude, Codex, and Amp share rules and skills without content duplication.
- Amp's long-lived thread survives Gas City restarts and compaction.
- Specialist delegation happens in child threads, keeping the mayor thread
  focused on synthesis and decisions.
- Existing external-action gates and worktree safety remain in force.

### Negative

- Amp plugin APIs are experimental and require a compatibility check after Amp
  updates.
- Claude hook behaviors that depend on Claude-only payloads need explicit
  adapters; they are not inherited automatically.
- Legacy commands are compatibility affordances, not native Amp skills.

### Risks

- **Plugin API drift**: `scripts/amp-compat-check.sh` verifies plugin loading,
  skills, MCP scoping, provider arguments, and resume configuration after
  changes.
- **Thread-key loss**: the project plugin persists `event.thread.id` through
  `gc prime --hook`; end-to-end restart testing proves the same thread resumes.
- **Context becoming the database**: global Amp guidance requires durable state
  to be externalized before relying on thread memory.
- **MCP secret logging**: the shared Sourcegraph wrapper redacts bearer tokens
  from child-process stderr before agent runtimes persist diagnostics.
