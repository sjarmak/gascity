<!--
Target live path: /home/ds/.claude/skills/gascity-pr-start/SKILL.md
Date: 2026-07-06
Status: draft pending dr-i4v.5 consumer eval
Changes vs live skill (5):
1. New top-level Evidence Rules: a citation you did not open is not evidence; absence claims must be searched absences; verified vs inferred stated differently.
2. New Phase 1.5 premise check — per-claim adjudication with an explicit evidence bar for declaring an issue (partly) invalid, incl. vocabulary greps and already-fixed-upstream detection.
3. Phase 2 blast radius upgraded from "delegate to agent" to a mandatory boundary-type search checklist (callers/siblings, config+reload, goroutines+locks, registries/serialized formats/layering, double-transformation), plus an untouched-surfaces declaration.
4. Phase 4 plan output replaced with a mandatory 7-part schema: premise verdicts, root cause (must contain a link the issue does not mention), blast radius by boundary, >=2 architecturally distinct candidates (portability test on rejections), per-step verification that could fail, regression-test-in-same-commit naming THE symptom test, maintainer-rejection pre-mortem with step back-links.
5. Added one worked example distilled from the issue-3972 golden run; Phase 5 adoption-review list kept intact.
-->

---

name: gascity-pr-start
description: Gas City PR planning and scaffolding. Use when starting a new fix/feature branch. Premise-checks the issue against the checkout, maps blast radius by boundary type, identifies codebase conventions that apply, plans the test strategy, and produces a structured plan. Prevents common adoption-review findings before they happen.
---

# Gas City PR Start

> **MAINTAINER CONTEXT (2026-05-04 onward):** sjarmak holds maintainer privileges on `gastownhall/gascity`. Julian Knutsen (`julianknutsen`) is owner; sjarmak is the only other maintainer. This skill stays contributor-shaped — when WE author a PR, Julian is the external reviewer. Maintainer status does not exempt us from any gate below; if anything our own PRs should be MORE rigorous because there's only one external reviewer to bounce off. Push target stays `fork`. See `feedback_maintainer_status.md` for the full guardrail set.

Use when beginning work on a new issue or feature. Front-loads the analysis that the maintainer's adoption review will check, so we address concerns before submitting.

The quality bar for the whole plan: **derived from the checkout, not from the issue text.** A plan that fluently elaborates the issue's own framing, with citations decorating it, is a failure even if every sentence sounds right. The plan must visibly collide with the code and be reshaped by what it finds there.

## Evidence rules (apply to every phase)

- **A citation you did not open is not evidence.** Every `file:line` in the plan is one you read in this checkout, in this session. If you cannot remember opening it, open it now or delete the claim.
- **Cite ranges and quote literals** where the claim is load-bearing: "the string at `:176` reads `notify %s failed`" is falsifiable; "errors are logged around there" is not. Real investigation produces line ranges and quoted strings; single round-numbered lines everywhere is the tell of pattern-completion.
- **State absences as searched absences.** "Nothing dead-letters this" must be written as the search that produced it: "zero references to `nudgequeue` in `cmd_nudge.go`" / "symbol X has exactly one caller (grep `X(` across the repo)". An absence claim with no named search is an assertion, not evidence.
- **Verified facts flatly, inferences marked.** If you read the code, state it plainly. If you are inferring ("presumably the reload path re-runs this"), say "inferred, not verified" — or go verify it.

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
gh issue view <number> --repo gastownhall/gascity --json state
```

**If a competing PR exists or the issue is closed, STOP.** Report the competing PR/closure to the user and ask whether to proceed, pivot to a different issue, or review the competing PR instead.

### Architectural-refactor gate (BLOCKING)

Competing-PR check catches _issue-level_ duplicates; it misses _area-level_ ones. Before scoping a fix, check whether the file/package you plan to touch sits inside an active architectural refactor. If it does, a narrow fix will be superseded and the time is wasted.

```bash
# 1. Active design docs with Accepted / Implementing / Implemented status:
ls /home/ds/gascity/engdocs/design/
grep -l "^| Status |.*\(Accepted\|Implementing\|Implemented\)" engdocs/design/*.md

# 2. Open maintainer PRs touching the area:
gh pr list --repo gastownhall/gascity --state open --author julianknutsen --json number,title,body
gh pr list --repo gastownhall/gascity --state open --search "unification refactor phase boundary" --json number,title

# 3. Recently-merged PRs that used Supersedes (area being consolidated):
gh pr list --repo gastownhall/gascity --state merged --search "supersedes in:body" --limit 5 --json number,title,body
```

**If your area has an Accepted/Implementing design doc OR an open maintainer consolidation PR touching it, STOP.** Three options: point-fix the refactor can absorb (ask the maintainer in the issue), wait for the refactor and rebase, or pivot. Precedents: PR #666 superseded #573, #614, #691, #713, #574, #654, #688, #745; PR #790 superseded #554, #687. Read the relevant design doc's `Status` field and `## Phase N` headings before writing any code.

## Phase 1.5: Premise check (MANDATORY, BLOCKING)

Do not plan against the issue as a whole. Split it into its separable claims/asks and adjudicate **each one** against the checkout before any fix thinking. Per claim, one of four verdicts, each with evidence:

- **Confirmed** — file:line showing the claimed behavior exists.
- **Refuted / already fixed** — file:line showing the code does not do what's claimed, or the commit that already fixed it. Run `git log --oneline -- <area>` / `git log -S '<symbol>'` around the cited mechanism; issues routinely describe last month's code. Anything already fixed upstream is **excluded from scope and the plan says so** — do not touch it.
- **Narrowed** — partially right; state exactly which half survives and why (e.g. "the data IS surfaced, but only pull-based and only within a 1-hour retention — so the operator-facing claim holds").
- **Vocabulary not found** — grep every load-bearing noun the issue uses. A term with zero hits in the checkout is downstream/reporter vocabulary; **translate the ask onto the repo's real concept** (name it, with file:line) or drop it with the searched absence stated. Never plan against a phantom.

**Evidence bar for declaring the issue invalid:** the same bar as for planning a fix. You need file:line showing behavior contradicts the claim, a searched absence, or the already-landed fix commit — "I couldn't see how this happens" is not a verdict. If the premise fails, the deliverable is the refutation with evidence, not a plan.

## Phase 2: Blast radius

Spawn the blast radius agent to map the impact surface:

```
Agent({
  description: "Gas City blast radius for PR planning",
  subagent_type: "gascity-blast-radius",
  prompt: "Analyze the blast radius for a proposed change to <files/functions>. I'm working on issue #<N>: <title>. The change will touch <describe what you'll modify>. Map callers, execution contexts, config field chains, domain boundaries, and concurrency. Return the structured risk report."
})
```

Whether or not the agent runs, the plan's blast-radius section must be built from **this search checklist** — one grep per line, findings recorded per boundary type. "Check callers" is not a blast radius; the output of these searches is.

- [ ] **Callers + siblings**: grep every symbol you modify for ALL callers; then grep the _pattern_ you're fixing across the repo — the #1 adoption finding is an identical sibling call site with the same bug.
- [ ] **Config paths**: does any touched value participate in `city.toml` → `[agent_defaults]` → `[[agent]]` → patch/override, or in a reload path? Startup-only logic reachable from reload is a standing rejection reason.
- [ ] **Goroutine boundaries + locks**: which goroutines spawn or consume the touched path (`grep -n "go " <files>`); which locks/flock transactions enclose it — and therefore what new work must NOT run inside them (emit after commit, never inside the lock).
- [ ] **Cross-subsystem contracts**: registries/sealed types that reject unregistered entries downstream; serialized formats (`events.jsonl`, JSON status output, `.txtar` goldens) whose consumers assume a stable shape; import-layering seams the fix crosses (e.g. `internal/api` must reach `cmd/gc` machinery only via the existing `State` interface).
- [ ] **Double-transformation**: if the fix routes data through a second existing path, check whether both paths apply the same transformation (wrapping, framing, sanitization) — the double-apply bug is found here or shipped.
- [ ] **Untouched surfaces, stated**: list what the fix deliberately does NOT touch (no new config keys, wire shapes unchanged, live-path latency unchanged) — this is a claim the plan must make explicitly, not an omission.

## Phase 3: Convention alignment

Before writing code, verify the plan follows these patterns:

### Architecture patterns

- _*do*()/cmd_() split**: `cmdFoo()` (wiring) + `doFoo()` (pure logic with injected deps)
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

Profiling the four highest-signal contributors on `gastownhall/gascity` (Julian, QUAD/Wordelman, Don Box, Chris Sells) surfaced the one discipline our own PRs skip: **architectural work lands with a durable design artifact attached.** Julian authors the `engdocs/design/` canon (36 of 44 design docs); QUAD's biggest features each _implement against_ a design doc. Our own PRs cite a design doc in **4 of 124** commits.

### Does this change need a design doc?

Write (or update) an `engdocs/design/<name>.md` when ANY of these is true:

- Introduces or changes a **subsystem boundary or cross-cutting mechanism** — read-path/routing, store topology, supervisor or session lifecycle, a provider/worker interface, the config override chain, an event/wire format, the endpoint model.
- Adds a **new package** or a **new public contract / schema** that other components or packs consume.
- Changes **behavior other components depend on** — `events.jsonl` shape, a CLI contract a pack reads, the bd+Dolt contract.
- The work is the **`mol-scoped-work` class** — spans multiple packages / is a cohesive feature, not a point fix.

Skip the doc (a code-only PR is correct) when the change is a **single-function point fix** (the `mol-do-work` ≤3-file class), **test-only or docs-only**, or a **behavior-preserving mechanical refactor**. When the trigger fires but scope is uncertain, default to a **short** design note over nothing — but never write a design doc for a one-liner.

### Two capture mechanisms exist — know which is which

- `engdocs/design/*.md` — forward-looking **design proposal**. This is the one **we** author (below).
- `release-gates/*.md` — per-change **acceptance contract + evidence**, tied to a builder-fleet deploy bead. We don't run that pipeline, so we don't author these — but if your area already has one, treat it like a design doc for the "cite it" move.

### If the area ALREADY has a design doc or release-gate

Do the QUAD move: **implement against it and cite it** — reference the artifact by path in the PR body and the landing commit (`implements engdocs/design/<name>.md`). Do not start a competing doc.

### If it needs a NEW doc, draft the stub now — before code

Author it in the branch so it is reviewed _with_ the diff. Match the repo's current convention (status table, registered in `index.md` — confirm the live shape against any file in `engdocs/design/`):

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

Register it — add one row to the **Current Design Set** table in `engdocs/design/index.md`. Open at `Status: Proposed`; review moves it to `Accepted`, the landing PR to `Implemented`.

## Phase 4: Plan output (MANDATORY SCHEMA)

Every plan has these seven parts, in this order. A plan missing a part is not done. Produce it and wait for user confirmation.

```
Issue: #<number> — <title>

## 0. Premise check
Per claim/ask: confirmed | refuted/already-fixed | narrowed | vocabulary-not-found,
each with its file:line or searched-absence evidence (Phase 1.5 verdicts).

## 1. Root cause (verified)
The causal chain, every link cited file:line from THIS checkout. The chain must
contain at least one load-bearing fact the issue does not mention — a constant's
actual value, an existing-but-unwired mechanism, a second failure path. If every
fact in this section appears in the issue text, you have restated the issue, not
found the root cause: go back to the code.

## 2. Blast radius
Findings per boundary type from the Phase 2 checklist (callers+siblings, config/
reload, goroutines+locks, cross-subsystem contracts, double-transformation),
plus the explicit untouched-surfaces list.

## 3. Fix candidates (>=2, genuinely weighed)
Architecturally different mechanisms — not the same fix at two sizes. Each
rejection cites repo-specific mechanics (which existing function it would call,
what it costs in this codebase). Portability test: if the rejection paragraph
could be pasted into another repo's plan unchanged, you have not weighed it.
Real-but-out-of-scope work is a named deferred candidate with a reason, not
silently dropped.

## 4. Implementation steps
Split into commits along ask/risk boundaries; each commit independently
shippable, carrying its own tests. Every step names the file, the symbol, the
in-repo precedent to follow (file:line), and the decision already made — no
"as appropriate", no "handle locking correctly" left to the implementer.
Every step maps back to a numbered ask or a named pre-mortem risk; a step that
maps to neither is scope creep — delete it.
Per step, a verification command that could FAIL if the step is wrong: a
specific test invocation with a run-filter, a build of the touched package, an
observable consequence (byte-identical output on the unchanged path, a marker
substring absent, an item surviving in state X). "Verify the event is emitted"
as the check for "emit the event" is not a check. Final step: make build &&
make check (+ make check-docs if docs touched), failures classified against a
clean baseline run on the unmodified base commit.

## 5. Test strategy (same commit as the source change)
Which tier, which fakes (Phase 3). Name the ONE test that is THE regression
test for the reported symptom — it asserts the user-visible symptom is gone,
not internal bookkeeping. Tests live in the same commit as the code they cover;
"tests in a follow-up" is a rejected plan.

## 6. Maintainer-rejection pre-mortem
The specific objections Julian's adoption review would raise for THIS change
(latency on a hot path, output-shape stability, layering, feed noise, a
previously-hidden failure class becoming visible and looking like a regression).
Each risk points at the exact plan step or test that pre-empts it; a risk with
no pre-emption, or a pre-emption with no risk, means the section is decorative.

## Design capture
  Change class: <point-fix | test/docs-only | refactor | architectural>
  [ ] Trigger fired? (subsystem boundary / new package / new contract / mol-scoped-work)
  [ ] Existing engdocs/design doc to cite, OR new stub drafted (Proposed) + registered in index.md
  (point-fix / test-only / docs-only / refactor → "N/A, code-only PR" + one-line why)

## Convention triggers
  [ ] Config field sync (B11, B15)        [ ] Store write error propagation (B12)
  [ ] Timeout isolation (B13)             [ ] do*()/cmd*() split (B19)
  [ ] Test doubles (B20)                  [ ] Map key separation (B18)
  [ ] Startup vs reload (B16)             [ ] Goroutine lifecycle (B17)
  [ ] Config snapshot safety (B21)        [ ] Dead code audit (B22)
  [ ] Fix scope completeness (B23)        [ ] Verify-before-delete (B24)
  [ ] Constant grep radius (B25)          [ ] Golden snapshot drift (B26)
  [ ] Env projection layer (B29)          [ ] Package-level race-safety (B30)
  [ ] Hard-fail examples audit (B31)      [ ] Test save/restore of pkg state (B33)
```

**WAIT for user CONFIRM before writing any code.**

## Worked example (distilled from the issue-3972 golden run)

Issue: "session event delivery is lossy and its failures are silent" — four asks (a)–(d).

**Premise check applied.** Two asks did not survive contact with the checkout:

- Ask (b) claimed submitted messages lose their Enter keystroke. `submitEnterAndConfirm` exists at `internal/runtime/tmux/tmux.go:1705` and is wired into `NudgeSession` at `:1820-1825` — already fixed upstream. Verdict: refuted/already-fixed; excluded from scope, stated in the plan.
- Ask (d) was phrased in terms of `pending_thread`. Zero hits across the checkout — downstream-shim vocabulary. Verdict: reframed onto the repo's real durable cursor (`ConversationMembershipRecord.LastReadSequence`, `internal/extmsg/types.go:284-292`) and deferred to a follow-up PR with a reason.

**One blast-radius search and its finding.** The chosen fix routes notify text through the queued-nudge drain path. Double-transformation check: read the drain path's formatting. Finding: drain applies its own framing (`formatNudgeInjectOutput` at `cmd_nudge.go:500-505`) while the live path already wraps the reminder in `<system-reminder>` — routing one through the other double-wraps the message. That became an explicit plan step (split body-builder from wrapper, sanitize exactly once) plus tests asserting both framings, instead of a bug caught in review or shipped.

**One fix-candidate weighing.**

- **A (picked):** fall back to the existing queued-nudge state machine for any not-running/busy/errored delivery — it already owns flock'd persistence, retry/backoff, 24h TTL, quiescence-gated dispatch, and audit trail.
- **B (rejected):** call the existing-but-unwired `WaitForRuntimeReady` (`tmux.go:2871`) inside the notify goroutine with a cold-boot-sized timeout. Rejected on repo-specific costs: pins one goroutine per member for minutes inside the API server; still fire-and-forget across a crash between accept and paste (which is the reported symptom); duplicates delivery logic the queue+poller already own; does nothing for the alerting ask. "It patches the window; A removes the class."

What makes this a plan and not an issue-echo: the root cause held links the issue never mentioned — the 10s `NudgeReadyTimeout` constant against the repo's own comment that a cold wake "can legitimately take a couple of minutes", and a tmux paste that succeeds at the tmux layer while the booting TUI silently swallows it.

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
9. **Dead code introduced by the PR** — helpers added "for completeness" but never called. Grep new `func` lines for zero callers
10. **Incomplete state coverage** — fix only handles one state (e.g. Pending) but the bug also manifests in others (InFlight, Dead). Enumerate all states and verify each path
11. **Verify-before-delete** — pruning/closing records without confirming the successor state exists in the store. Fail-open: store errors retain the record
12. **Missing regression test branches** — fix introduces multiple code paths (early-exit, timeout, error, success) but only tests the happy path
13. **Parallel code path siblings** (#1 finding) — fixing one call site while identical sibling call sites have the same bug. Grep all callers of modified functions AND all instances of the fixed pattern across the codebase
14. **Env var hermeticity** — tests using `os.Setenv` instead of `t.Setenv()`
15. **Golden snapshot / doc drift** — changing CLI output, log lines, or error messages without updating tutorial golden files (`docs/tutorials/*.md`) and `.txtar` fixtures
16. **Package-level mutable state races** (B30) — `var Foo bool` at package scope written by reload and read by request paths. Must be `atomic.*` with accessors. Canonical: `internal/formula/compile.go:403-425`
17. **Hard-fail conversions without examples/release-note audit** (B31) — converting silent degradation to a hard error breaks example configs. Grep `examples/`
18. **Test cleanup that hardcodes state instead of restoring it** (B33) — `prev := IsFoo(); SetFoo(x); t.Cleanup(func() { SetFoo(prev) })`
19. **Env projection inlined into command handlers** (B29) — subprocess env construction belongs in `cmd/gc/bd_env.go`, not `cmd_foo.go`

Address these proactively — each one you can trigger belongs in the Phase 4 pre-mortem with the step that answers it.

## Scope

`.claude/` is gitignored at the repo root. This skill will never be pushed upstream.
