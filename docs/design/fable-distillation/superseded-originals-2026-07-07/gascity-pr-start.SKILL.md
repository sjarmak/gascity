---
name: gascity-pr-start
description: Gas City PR planning and scaffolding. Use when starting a new fix/feature branch. Reads the issue, maps blast radius (via agent), identifies codebase conventions that apply, plans the test strategy, and produces a structured plan. Prevents common adoption-review findings before they happen.
---

# Gas City PR Start

> **MAINTAINER CONTEXT (2026-05-04 onward):** sjarmak holds maintainer privileges on `gastownhall/gascity`. Julian Knutsen (`julianknutsen`) is owner; sjarmak is the only other maintainer. This skill stays contributor-shaped — when WE author a PR, Julian is the external reviewer. Maintainer status does not exempt us from any gate below; if anything our own PRs should be MORE rigorous because there's only one external reviewer to bounce off. Push target stays `fork`. See `feedback_maintainer_status.md` for the full guardrail set.

Use when beginning work on a new issue or feature. Front-loads the analysis that the maintainer's adoption review will check, so we address concerns before submitting.

## Phase 1: Issue analysis

Read the issue and extract what's broken, where it lives, and why it matters:

```bash
gh issue view <number> --json title,body,labels,comments
```

Check for related issues and prior art:

```bash
gh issue list --search "<keywords>" --state all --limit 10
gh pr list --search "<keywords>" --state all --limit 10
```

### Competing PR gate (BLOCKING)

Before proceeding, explicitly check whether any open PR already targets this issue:

```bash
gh pr list --repo gastownhall/gascity --state open --search "<issue number>" --json number,title,author,createdAt
```

Also check if the issue is already closed (meaning a fix was merged):

```bash
gh issue view <number> --repo gastownhall/gascity --json state
```

**If a competing PR exists or the issue is closed, STOP.** Report the competing PR/closure to the user and ask whether to proceed, pivot to a different issue, or review the competing PR instead. Do not silently start work on an issue that someone else is already fixing — this wastes effort and creates merge conflicts.

### Architectural-refactor gate (BLOCKING)

Competing-PR check catches *issue-level* duplicates; it misses *area-level* ones. Before scoping a fix, check whether the file/package you plan to touch sits inside an active architectural refactor. If it does, a narrow fix will be superseded and the time is wasted.

```bash
# 1. Active design docs with Accepted / Implementing / Implemented-with-boundary-hardening status:
ls /home/ds/gascity/engdocs/design/
grep -l "^| Status |.*\(Accepted\|Implementing\|Implemented\)" engdocs/design/*.md

# 2. Open maintainer PRs touching the area:
gh pr list --repo gastownhall/gascity --state open --author julianknutsen --json number,title,body
gh pr list --repo gastownhall/gascity --state open --search "unification refactor phase boundary" --json number,title

# 3. Recently-merged PRs that used Supersedes (indicates the area is being consolidated):
gh pr list --repo gastownhall/gascity --state merged --search "supersedes in:body" --limit 5 --json number,title,body
```

**If your area has an Accepted/Implementing design doc OR an open maintainer consolidation PR touching it, STOP.** Three options:

- **Point-fix** with a single-line change the refactor can absorb. Ask the maintainer in the issue whether this is wanted.
- **Wait** for the refactor to land, then rebase.
- **Pivot** to an issue outside the refactor area.

This pattern is inherited from the gastown workflow (`c29396d9` "Close stale MRs superseded by ZFC rewrite", `3fa6d9e2` "auto-supersede old MRs"). Recent gascity precedents where narrow fixes were superseded by architectural consolidation:

- PR #666 (session-model-unification) superseded #573, #614, #691, #713, #574, #654, #688, #745
- PR #790 (env projection layer) superseded #554, #687

Read the relevant design doc before writing any code. The doc's `Status` field and the `## Phase N` headings tell you which parts are live and which are next. If your fix touches a phase that's already merged, you're fine; if it touches one that's queued for a later phase, your narrow fix won't survive rebase.

## Phase 2: Blast radius (delegate to agent)

Spawn the blast radius agent to map the impact surface:

```
Agent({
  description: "Gas City blast radius for PR planning",
  subagent_type: "gascity-blast-radius",
  prompt: "Analyze the blast radius for a proposed change to <files/functions>. I'm working on issue #<N>: <title>. The change will touch <describe what you'll modify>. Map callers, execution contexts, config field chains, domain boundaries, and concurrency. Return the structured risk report."
})
```

## Phase 3: Convention alignment

Before writing code, verify the plan follows these patterns:

### Architecture patterns

- **do*()/cmd*() split**: `cmdFoo()` (wiring) + `doFoo()` (pure logic with injected deps)
- **Provider interfaces**: New implementations must pass conformance suite in `*test/conformance.go`
- **Nil-guard tracker**: Optional subsystems use `if tracker != nil` — nil means disabled
- **Config override chain**: `city.toml` → `[agent_defaults]` → `[[agent]]` → `AgentPatch` → `AgentOverride`

### Testing strategy (plan BEFORE writing code)

1. **Which tier?**
   - "Does the store handle corrupt X?" → Unit test
   - "Does `gc foo` print the right output?" → Testscript (.txtar)
   - "Does this work with real tmux?" → Integration test
   - "Does the provider shim handle this correctly?" → Acceptance test (`test/acceptance/`)
   - "Are components called in the right order?" → Coordination test

2. **Fakes, not mocks**: `runtime.NewFake()`, `beads.MemStore`, `fsys.NewFake()`. No gomock. New fakes next to interface with `var _ Interface = (*Fake)(nil)`.

3. **Error injection**: Per-path errors (`f.Errors["/path"] = err`) or modal (`f.Broken = true`).

4. **Testscript env vars**: Only `GC_SESSION`, `GC_BEADS`, `GC_DOLT`. >2 env vars → unit test.

5. **Regression depth**: Bug fixes test through full write path. Assertions discriminate exact bug path.

### Branch setup

```bash
git fetch origin
git checkout -b <prefix>/<issue-number>-<short-desc> origin/main
# prefix: fix/, feat/, refactor/, docs/
#
# Note: this repo's canonical remote is `origin` (gastownhall/gascity);
# `fork` is the user's fork (sjarmak/gascity). Branch from origin/main
# directly — never `git checkout main` first, because main is claimed by
# the /home/ds/gascity-main worktree (used to run the binary against rigs).
```

## Phase 3.5: Design-capture decision (the lever QUAD/Box/Sells use)

Profiling the four highest-signal contributors on `gastownhall/gascity` (Julian, QUAD/Wordelman, Don Box, Chris Sells) surfaced the one discipline our own PRs skip: **architectural work lands with a durable design artifact attached.** Julian authors the `engdocs/design/` canon (36 of 44 design docs); QUAD's biggest features each *implement against* a design doc; Don Box touches docs in 82% of his commits, Chris Sells 65%. Our own PRs cite a design doc in **4 of 124** commits. Closing that gap is exactly what lets a PR clear adoption review in one pass instead of a "what's the intent here?" round-trip.

### Does this change need a design doc?

Write (or update) an `engdocs/design/<name>.md` when ANY of these is true:

- Introduces or changes a **subsystem boundary or cross-cutting mechanism** — read-path/routing, store topology, supervisor or session lifecycle, a provider/worker interface, the config override chain, an event/wire format, the endpoint model.
- Adds a **new package** or a **new public contract / schema** that other components or packs consume.
- Changes **behavior other components depend on** — `events.jsonl` shape, a CLI contract a pack reads, the bd+Dolt contract.
- The work is the **`mol-scoped-work` class** — spans multiple packages / is a cohesive feature, not a point fix.

Skip the doc (a code-only PR is correct) when the change is:

- A **single-function bug fix / point fix** (the `mol-do-work` ≤3-file class).
- **Test-only or docs-only.**
- A **behavior-preserving mechanical refactor.**

When the trigger fires but scope is uncertain, default to a **short** design note over nothing — but respect KISS/YAGNI: never write a design doc for a one-liner. The trigger test above already excludes those.

### Two capture mechanisms exist — know which is which

- `engdocs/design/*.md` — forward-looking **design proposal**. This is the one **we** author (below).
- `release-gates/*.md` — per-change **acceptance contract + evidence**, tied to a builder-fleet deploy bead. 84 of 91 are QUAD's; we don't run that pipeline, so we don't author these. But if your area already has a release-gate contract, treat it the same as a design doc for the "cite it" move below, and `gascity-ship` Stage 0 will recognize either.

### If the area ALREADY has a design doc or release-gate

You found it in the Phase 1 architectural-refactor gate (`engdocs/design/`, status `Accepted` / `Implementing`) or under `release-gates/`. Do the QUAD move: **implement against it and cite it.** Reference the artifact by path in the PR body and in the commit that lands the core change (`implements engdocs/design/<name>.md`). Do not start a competing doc.

### If it needs a NEW doc, draft the stub now — before code

Author it as part of this plan, in the branch, so it is reviewed *with* the diff rather than bolted on after. Match the repo's current convention (status table, registered in `index.md` — confirm the live shape against any file in `engdocs/design/` before writing):

```bash
cat > engdocs/design/<short-kebab-name>.md <<'MD'
---
title: "<Title Case Name>"
---

| Field | Value |
|---|---|
| Status | Proposed |
| Date | <YYYY-MM-DD> |
| Author(s) | sjarmak |
| Issue | #<number> |
| Supersedes | N/A |

## Summary

<2-4 sentences: what this changes and the one-line reason it is worth doing.>

## Problem

<What is broken or missing today, in terms a future maintainer with no context can follow.>

## Design

<The approach. Name the subsystem boundaries it touches and the contract it
establishes — the "why this shape" the adoption review otherwise reverse-engineers from the diff.>

## Alternatives considered

<At least one rejected option and why. Pre-empts the "did you consider X?" round-trip.>
MD
```

Register it so it is discoverable — add one row to the **Current Design Set** table in `engdocs/design/index.md`:

```
| `<short-kebab-name>` | Proposed | <one-line note> |
```

Open at `Status: Proposed`; let review move it to `Accepted` (direction approved) and the landing PR move it to `Implemented`.

## Phase 4: Plan output

Produce this structured plan and wait for user confirmation:

```
Issue: #<number> — <title>

Root cause: <one sentence>

Files to change:
  <file>:<function> — <what changes>

Blast radius: <summary from agent report>

Design capture:
  Change class: <point-fix | test/docs-only | refactor | architectural>
  [ ] Trigger fired? (subsystem boundary / new package / new contract / mol-scoped-work)
  [ ] Existing engdocs/design doc to cite, OR new stub drafted at engdocs/design/<name>.md (Proposed)
  [ ] Registered in engdocs/design/index.md
  (point-fix / test-only / docs-only / refactor → "N/A, code-only PR" + one-line why)

Convention triggers:
  [ ] Config field sync (B11, B15)
  [ ] Store write error propagation / retain-and-retry (B12)
  [ ] Timeout isolation (B13)
  [ ] do*()/cmd*() split (B19)
  [ ] Test doubles (B20)
  [ ] Map key separation (B18)
  [ ] Startup vs reload (B16)
  [ ] Goroutine lifecycle (B17)
  [ ] Config snapshot safety (B21)
  [ ] Dead code audit (B22)
  [ ] Fix scope completeness (B23)
  [ ] Verify-before-delete (B24)
  [ ] Constant grep radius (B25)
  [ ] Golden snapshot / doc output drift (B26)
  [ ] Env projection layer (B29)
  [ ] Package-level race-safety (B30)
  [ ] Hard-fail examples audit (B31)
  [ ] Test save/restore of pkg state (B33)

Test plan:
  Unit: <what to test, which fakes>
  Testscript: <which .txtar to add/modify>
  Integration: <if needed>

Risks:
  <what could go wrong, what the maintainer will scrutinize>
```

**WAIT for user CONFIRM before writing any code.**

## Phase 5: What the maintainer will check

Based on all adoption reviews (PRs #386, #480, #524, #526, #540, #543, #584, #650, #653, #656, #658, #687, plus rileywhite's #501, #510, #511, #528, #531), the `/adopt-pr` workflow runs multi-model review (Claude + Codex, sometimes + Gemini) and consistently flags:

1. **Silent error handling** — `_ = store.Write()` or `bool` return that doesn't fire `false` on write error
2. **Shared code paths** — timeouts/semaphores bleeding into unintended subsystems
3. **Nil/empty distinction** — config fields where "not set" vs "explicitly empty" matters
4. **Infrastructure agent contamination** — loops applying work config to control_dispatcher
5. **Goroutine lifecycle** — fire-and-forget goroutines in CLI paths; callers must defer-block done channels on ALL return paths
6. **Startup vs reload safety** — one-time operations wired into reload paths
7. **Test specificity** — assertions that could pass for the wrong reason
8. **Raw string literals** — constants defined but not used everywhere; grep the ENTIRE codebase for raw string occurrences
9. **Dead code introduced by the PR** — helper methods/functions added "for completeness" but never called (e.g. `Len()` on a wrapper). Grep new `func` lines for zero callers
10. **Incomplete state coverage** — fix only handles one state (e.g. Pending) but the bug also manifests in other states (InFlight, Dead). Enumerate all states and verify each path
11. **Verify-before-delete** — pruning/closing records without confirming the successor state actually exists in the store. Fail-open: store errors retain the record
12. **Missing regression test branches** — fix introduces multiple code paths (early-exit, timeout, error, success) but only tests the happy path
13. **Parallel code path siblings** (#1 finding) — fixing one call site while identical sibling call sites have the same bug. Grep all callers of modified functions AND all instances of the fixed pattern across the codebase
14. **Env var hermeticity** — tests using `os.Setenv` instead of `t.Setenv()`, causing failures for devs with env vars exported
15. **Golden snapshot / doc drift** — changing CLI output, log lines, or error messages without updating tutorial golden files (`docs/tutorials/*.md`) and test fixtures (`.txtar` files) that contain hardcoded old output
16. **Package-level mutable state races** (B30) — `var Foo bool` at package scope written by config reload and read by request paths. Must be `atomic.*` with `Set*`/`Is*` accessors. Canonical: `internal/formula/compile.go:403-425`
17. **Hard-fail conversions without examples/release-note audit** (B31) — converting silent degradation to a hard error breaks any example config that relied on the old behavior. Grep `examples/` for configs that would newly fail
18. **Test cleanup that hardcodes state instead of restoring it** (B33) — `t.Cleanup(func() { SetFoo(false) })` corrupts subsequent tests. Use `prev := IsFoo(); SetFoo(x); t.Cleanup(func() { SetFoo(prev) })`
19. **Env projection inlined into command handlers** (B29) — subprocess env construction belongs in `cmd/gc/bd_env.go`, not `cmd_foo.go`. Credit gastown's `internal/beads/beads.go` as origin

Address these proactively.

## Scope

`.claude/` is gitignored at the repo root. This skill will never be pushed upstream.
