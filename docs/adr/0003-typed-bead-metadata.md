# ADR-0003: Typed Bead Metadata + Close-Gate Contracts

**Date**: 2026-05-12
**Status**: proposed — partially implemented, needs revision (2026-07-07 audit)

> **2026-07-07 audit:** the typed-metadata half shipped as a key registry
> (`internal/beadmeta/`), but the declarative per-issue-type schemas validated
> at close-time did not; `orders/close-gate-reaper.toml` still runs. Revise as
> a pair with 0005. See `docs/design/adr-ratify-or-retire-2026-07-07.md`.
> **Deciders**: sjarmak, mayor

## Context

Bead metadata is free-form JSON today. Conventions accumulate (`evidence.artifact_path`, `evidence.reviewer_verdict`, `evidence.reviewer_agent`, `gc.outcome`, `originating_slack.*`, `summary_for_human`, `loop_close_*`, `gc.step_ref`, `gc.routed_to`, `molecule_id`, `gate_surfaced_at`, etc.) but nothing in the SDK validates them. Enforcement lives in separate cron scanners:

- `close-gate-reaper` reopens closes lacking required `evidence.*` per YAML rules
- `codeprobe-drain-without-commit-guard` reopens drain-without-commit closes
- `stale-claim-reaper` unclaims stale in-progress beads

Concrete pain points from v1.5 + drain work:

- `gc-ja2x` closed without structured `evidence.*` — only `close_reason` text. Caught by mayor reading the bead; not caught by any automation
- `gpk-3bi` closed without `evidence.*` — required gpk-24c as a separate follow-up bead just to add structured evidence
- `mol-pr-triage` closes don't write `summary_for_human` — caught by loop-close handler at fallback time, not at close time
- `zeldascension-1u70` closed with `close_reason: "Closed"` — terse string passed enforcement because no schema enforces "informative close_reason"
- `originating_slack.*` is per-provider; pillar conflicts with ADR-0002's Conversation primitive
- Every reaper duplicates the same shape: scan recent updates → filter → reopen with notes. The right home for these constraints is the type system

## Decision

Promote bead metadata schemas to first-class SDK concepts. For each `issue_type` (`task`, `feature`, `bug`, `epic`, `step`, `convoy`, `wisp`, `conversation`, `message`, custom types), declare a metadata schema with:

- **Required fields on close** (`evidence.artifact_path`, etc.)
- **Required fields on status transition** (e.g. `originating_slack.*` set at create-time for slack-driven beads)
- **Typed field shapes** (artifact_path is `git:<sha>` | `path:<filepath>` | `url:<u>` | `github-pr:<repo>/<n>` — typed enum of pointer kinds)
- **Validation rules** (close_reason min length, etc.)

`bd update --status=closed` validates the schema at write-time and refuses transitions that violate the contract. Close-gate-reaper retires. Schema lives in TOML alongside the issue type's pack (e.g. `gascity-packs/pr-review/schemas/task.toml`); SDK enforces.

## Alternatives Considered

### Alt 1: Status quo — convention + cron reapers

- **Pros**: shipped; each rig can have its own rules without SDK changes
- **Cons**: enforcement is hours-late (reaper runs hourly); violations land in production and get reopened; every new constraint needs a new reaper script; schemas are scattered across YAML files with no introspection surface
- **Why not**: the cost compounds. Five reapers shipped, more coming (`pl-loop-close-fallback` is half-reaper-shape too). Type system is the right home

### Alt 2: Lightweight schema TOMLs, still enforced by reaper (no SDK changes)

- **Pros**: minimal SDK change; gets schema centralization without typing the bead model
- **Cons**: still hours-late enforcement; still scattered runtime checks; doesn't unblock formula-composition gates (ADR-0004) which need to inspect typed evidence at runtime
- **Why not**: half-measure. Doesn't address the "validation happens at write-time" problem that's core to closing the drain-without-commit + missing-evidence + missing-summary loop

### Alt 3: Full code-gen typed metadata structs in Go

- **Pros**: maximum type safety; IDE autocomplete for bead authors using Go SDK
- **Cons**: code-gen tooling burden; every schema change is a build artifact; doesn't help formula TOML authors (which is most consumers)
- **Why not**: wrong layer. Most bead-metadata authors are formula step bodies (bash + jq), not Go code. Schema must be runtime-introspectable, not compile-time

### Alt 4: Validate at bd-server side only, leave SDK clients lenient

- **Pros**: clients don't need schema awareness; one validation surface
- **Cons**: clients build invalid payloads, get errors at write — bad DX. Formulas particularly affected (step bodies fail mid-execution instead of at sling time)
- **Why not**: validate close to authoring; sling-time + dispatch-time checks need schema visibility on the client too

## Consequences

### Positive

- Closes that lack required evidence fail at write-time, not hours later via reaper
- Five reapers retire (`close-gate-reaper`, `codeprobe-drain-without-commit-guard`, partial logic in `pl-loop-close` fallback path, `slack-binding-reaper` evidence portion, future ones)
- ADR-0004 formula composition gates can inspect typed evidence — pivotal for halt-on-flag semantics
- New issue types (e.g. `conversation` from ADR-0002) ship with schemas from day 1
- Schema introspection enables `gc bd describe <issue_type>` showing "what fields does close require?"
- Pattern miner can detect drift between declared schema and observed metadata shape

### Negative

- Migration: existing beads have heterogeneous metadata. Need lenient mode for legacy beads + strict mode for new. Configurable per-rig `schema_enforcement: lenient | strict`
- SDK surface grows (schema types, validation, introspection). Estimated ~3-5 new exported types
- Schema authoring becomes a contributor burden. Mitigation: most rigs inherit standard schemas; only customize when needed

### Risks

- **Schema rigidity blocks legitimate edge cases** — Mitigation: schema declares `additionalMetadata: allowed | forbidden` (default allowed); enforcement targets only declared fields, doesn't restrict extras
- **Migration breaks live workflows** — Mitigation: phased rollout — schemas in advisory mode (warnings) for 1 minor SDK release before enforcement; `gc doctor` reports schema violations as info, not errors
- **Performance overhead** — every `bd update` runs validation. Mitigation: cache parsed schemas; benchmark + budget at <5ms per update; skip validation for `--status` unchanged updates
