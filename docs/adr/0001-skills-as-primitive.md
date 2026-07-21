# ADR-0001: Skills as a First-Class Primitive

**Date**: 2026-05-12
**Status**: retired (2026-07-07 audit)

> **2026-07-07 audit:** never implemented; the shipped approach is per-agent
> filesystem materialization (`internal/materialize/skills.go`), which this ADR
> considered and rejected as Alt 1, and `AGENTS.md` lists "No skills system" as
> a permanent exclusion. Retired. Full reasoning:
> `docs/design/adr-ratify-or-retire-2026-07-07.md`.
> **Deciders**: sjarmak, mayor

## Context

Gas City has five primitives: Session, Bead, Molecule (formula), Pool, Routing. Skills — invokable expertise that an agent calls at runtime (`/focus`, `/diverge-prototype`, `/verification-loop`, etc.) — are NOT a primitive. They live as filesystem entries under `~/.claude/skills/` (or per-account claude-homes), are discovered by Claude Code's built-in skill loader, and surface to the agent as an opaque "available-skills" system-reminder list.

Concrete pain points surfaced during v1.5 work:

- All 5 account homes have identical 69-skill sets — per-agent narrowing is not possible without project-level overrides
- Formulas can't reference skills in their step contracts (e.g. "this step requires `/verification-loop` to pass close")
- PLs can't query "what skills does the target worker have?" before slinging — `requires_skills` checks would catch missing-skill failures at dispatch time instead of mid-step
- Skills aren't typed: no declared input/output, no introspectable contract for `mayor-pattern-miner` or formula-composition logic
- The "skills as portable expertise" half of the formula-vs-skill rule (memory `feedback_formula_vs_skill`) only delivers half its value because skills aren't first-class

## Decision

Promote Skills to a first-class SDK primitive alongside Session/Bead/Molecule/Pool/Routing. A `skill.Skill` declares: name, description, **typed input schema, typed output schema**, trigger phrases, source-pack. A `skill.Registry` is per-agent (not per-account), enforced by the SDK at session start.

Formulas gain a `requires_skills = ["..."]` field at the step level; the SDK fails the sling at dispatch time if the target's registry doesn't satisfy the contract. Discovery surface: `gc skill list --agent <name>` + a typed query API for formulas.

## Alternatives Considered

### Alt 1: Keep skills as user-level filesystem; add per-agent allowlist files (`skills.toml`)

- **Pros**: minimal change; reuses existing Claude Code skill machinery; informational allowlists already shipped in v1.5
- **Cons**: skills stay opaque to formulas + SDK; per-agent narrowing requires fighting Claude Code's home-dir conventions; no typing means no contract enforcement
- **Why not**: doesn't address the core problem — formulas can't compose around skill availability

### Alt 2: Skills as a special case of Pack imports

- **Pros**: reuses pack-import infrastructure; familiar to existing contributors
- **Cons**: packs are coarse-grained bundles of formulas/configs; skills are individual invocable units; conflating them complicates both
- **Why not**: wrong granularity; obscures the formula-vs-skill distinction we want to clarify

### Alt 3: Status quo + better documentation

- **Pros**: zero migration cost
- **Cons**: doesn't unblock formula composition, dispatch-routing negotiation, or per-agent narrowing — the three v2 pillars that depend on this one
- **Why not**: defers the foundational decision; future v2 work piles up against this missing primitive

## Consequences

### Positive

- `mol-ad-hoc-from-mayor` and future formulas can declare skill prerequisites; sling-time check catches missing skills early
- PL dispatch-table generation becomes possible (capability negotiation per ADR-006 dependency)
- Per-agent context budget improves: workers see only their relevant skills, not all 69
- Skills become testable artifacts (declared input/output enables fuzzing, regression tests)
- Mayor-pattern-miner can detect skill drift (declared registry vs. actual invocations)

### Negative

- Migration cost: 69 existing skills need typed metadata. Most have `SKILL.md` frontmatter that can auto-derive
- Breaking change for SDK consumers pre-v2: formulas using `requires_skills` won't run on v1
- New SDK surface adds calls (`gc skill list`, registry queries) — performance budget needs measuring

### Risks

- **Skill typing becomes a burden** — every new skill needs schema work. Mitigation: auto-derive from `SKILL.md` frontmatter; accept untyped skills as `input: free-text, output: free-text` defaults; require typing only when a formula references the skill
- **Per-agent scoping fights Claude Code's user-level discovery** — Mitigation: materialize via project-level `.claude/skills/` per agent workdir at session start (matches existing Claude Code override mechanism)
- **`requires_skills` brittle to skill renames** — Mitigation: registry supports aliases; rename migrations bump SDK minor version
