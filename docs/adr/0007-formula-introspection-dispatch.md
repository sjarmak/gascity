# ADR-0007: Formula Introspection + Dispatch-Routing Negotiation

**Date**: 2026-05-12
**Status**: retired (2026-07-07 audit)

> **2026-07-07 audit:** no intent_patterns / `[capability]` block /
> `gc dispatch suggest` built; contradicted by `AGENTS.md` "No capability
> flags", and both deps (0001, 0004) are dead. Retired.
> See `docs/design/adr-ratify-or-retire-2026-07-07.md`.
> **Deciders**: sjarmak, mayor
> **Depends on**: ADR-0001, ADR-0004

## Context

PL dispatch tables today are hand-written rows mapping ask-shapes to formulas: "review PR N" → `mol-pr-review`, "triage" → `mol-pr-triage`, etc. Every new ask-shape requires editing the PL brief (which triggers respawn cascades) + writing a new formula. Lead time is 1-2 days per row (gc-mq4d's full saga).

Worse, the dispatch table is **opaque to the SDK**. PLs string-match Slack input against rows. The SDK has no way to:

- Tell a PL "here are the formulas in your installed packs that accept this ask shape"
- Validate that a slung formula's required skills (ADR-0001) exist on the target
- Route around capacity (no negotiation: PL slings to a pool whether or not the pool has capacity)
- Auto-generate dispatch tables from pack manifests

## Decision

Formulas declare a typed capability surface in their TOML:

```toml
[capability]
intent_patterns = ["review pr {pr}", "self-review my PR {pr}", "scorecard pr {pr}"]
input_schema = { pr = "integer" }
output_evidence = { artifact_path = "path:run_dir/synthesis.md" }
requires_skills = ["focus", "review-pr", "verification-loop"]
requires_target = { kind = "polecat", min_concurrency = 1 }
```

The SDK provides:

- `gc formula list --accepts "review pr 123"` — match Slack input against `intent_patterns` across installed packs
- `gc formula describe <name>` — show capability surface
- `gc dispatch suggest <input>` — returns ranked list of formulas + target pools that satisfy the input

PL dispatch becomes: classify ask → `gc dispatch suggest` → pick best match → sling. Dispatch tables become **generated reports** of installed capabilities, not hand-written rows.

## Alternatives Considered

### Alt 1: Status quo — hand-written dispatch tables in PL briefs

- **Pros**: shipped; works for stable ask-shapes
- **Cons**: every gap is a 1-2 day mayor cycle (gc-mq4d, gc-k7x4); briefs drift; respawn cascades on edit
- **Why not**: scales linearly in maintainer time; v2 wants logarithmic

### Alt 2: Capability YAML files per formula, parsed by city-side tooling

- **Pros**: lighter SDK change
- **Cons**: leaves matching logic in city scope; can't compose with skills (ADR-0001) or subscriptions (ADR-0004) without SDK awareness
- **Why not**: half-measure; the value is in SDK-level introspection

## Consequences

### Positive

- New formulas land via pack install; dispatch tables auto-update — no PL brief edits per new row
- `mol-ad-hoc-from-mayor` (the v1.5 escape valve) becomes one of several dispatch options, ranked alongside narrow formulas
- Pattern miner can detect "ask-shape X has no matching formula" → suggests filing one (closes the gc-mq4d feedback loop automatically)
- Skill prerequisite check (ADR-0001) integrates at dispatch time — sling fails fast when target lacks required skills
- Capacity-aware dispatch — `requires_target` matches against pool state via ADR-0004 subscription

### Negative

- Migration: every existing formula gains a `[capability]` block (or accepts defaults — `intent_patterns = []` means "explicit dispatch only")
- PL briefs lose dispatch tables — they become "you have access to `gc dispatch suggest`" — a leaner brief

### Risks

- **Intent pattern matching is fuzzy** — Slack asks are free-text. Mitigation: SDK uses simple parameterized templates (`{var}` placeholders); for complex matching, escape hatch to LLM-based classification
- **Capability declarations drift from actual formula behavior** — formula's `output_evidence` claim doesn't match what it writes. Mitigation: ADR-0003 typed metadata enforcement catches drift at close time
