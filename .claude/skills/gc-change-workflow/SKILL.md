---
name: gc-change-workflow
description: >-
  How a change lands in gastownhall/gascity without getting reverted:
  scope discipline (one contract per PR), the human-review routing rule
  for new core-platform contracts, staged landing instead of big-bang
  rewrites, adopt/take-the-good etiquette for incoming contributor PRs,
  tests-ship-with-fixes, docs rules, and the fence around parked dead
  limbs. Load this skill when planning a PR, deciding whether a change
  needs a maintainer hold, splitting a large change into landable
  pieces, adopting or superseding an outside PR, or when a reviewer
  flags bundled scope. Do NOT load it for running build/test gates
  (use gc-build-verify) or for how to write tests (use
  gc-test-authoring).
---

# gc-change-workflow — landing changes in Gas City

Tier: 1 (single-session reference runbook; spawns no subagents, needs no
interactivity).

This skill teaches the **process** rules that decide whether a Gas City
change survives on `main`. The repo's revert history shows the costliest
failures were process failures, not code failures: sound implementations
got reverted because they bundled scope, skipped human routing, or landed
big-bang. Every rule below is grounded in a real commit you can inspect.

All commit hashes cited here are on `origin/main` of
`github.com/gastownhall/gascity` (verified 2026-07-06, main at
`f828bbe4b`). Verify any of them yourself:

```bash
git merge-base --is-ancestor <hash> origin/main && echo on-main
git show -s --format='%h %ad %an%n%s%n%b' --date=short <hash>
```

## When NOT to use this skill

| You need                                                         | Use instead                                                                            |
| ---------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| Make targets, test tiers, local-vs-CI gaps, `env -i` traps       | sibling skill **gc-build-verify**; `TESTING.md`                                        |
| Which of the five test kinds to write, fakes, conformance suites | sibling skill **gc-test-authoring**; `TESTING.md`                                      |
| ZFC / Bitter Lesson / zero-roles violation patterns in your diff | sibling skill **gc-doctrine**; `AGENTS.md`                                             |
| The codegen ritual for API/schema/dashboard changes              | sibling skill **gc-generated-artifacts**; `engdocs/contributors/huma-usage.md`         |
| Running a multi-model review of a PR                             | repo skill **review-pr** (`.claude/skills/review-pr/`)                                 |
| Contribution mechanics (fork, hooks, branch naming, PR template) | `CONTRIBUTING.md` — the canonical home; this skill only adds the judgment layer on top |

Sibling `gc-*` skills are part of the same departure library and live in
`.claude/skills/`; if one is missing in your checkout, fall back to the
repo doc named next to it.

## Definitions (used throughout)

| Term                       | Meaning                                                                                                                                                                                             |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Contract**               | A shape other code or agents depend on: an event payload, bead/molecule schema or metadata key, provider protocol, formula format, config schema, public CLI or HTTP surface.                       |
| **Core-platform contract** | A NEW contract, or a change to an existing one, in the SDK core (`internal/`, `cmd/gc` surfaces). These require a human maintainer decision before merge (see Rule 2).                              |
| **Staged landing**         | Landing a large feature as a sequence: flag-gated infrastructure first (off by default), then behavior, then default-on — instead of one big merge.                                                 |
| **Adopt / take-the-good**  | Maintainer pattern for incoming contributor PRs: preserve the contributor's fix commits, add what's missing (usually tests) in maintainer commits, land as a superseding PR crediting the original. |
| **Dead limb**              | A built-but-reverted or built-but-never-merged feature. Parked, not deleted; re-landing one is a maintainer decision, not a contributor initiative.                                                 |
| **Human hold**             | A review flag meaning "do not auto-merge; a human must decide." The automated review pipeline merging past one is a process bug (it happened; see Rule 1).                                          |
| **bead**                   | A work unit in the beads task store (`bd` CLI). Follow-up work gets a bead, not a TODO comment.                                                                                                     |

## The change lifecycle (runbook)

`CONTRIBUTING.md` owns the mechanics; this is the ordered skeleton with
the judgment gates inserted. Steps 3, 4, and 8 are where reverts are
born.

1. **Start from an issue or bead.** If none exists for the work, file
   one. PR→issue linkage is an advisory review finding, not a CI gate
   (the blocking gate was deliberately removed — see "Change-control
   applies to change-control" below), but authored work should still
   link `Closes #N`.
2. **Branch per PR, never from your fork's `main`:**

   ```bash
   git checkout -b fix/session-startup upstream/main
   ```

   Prefixes: `fix/*`, `feat/*`, `refactor/*`, `docs/*`. (In a clone
   where the gastownhall repo is `origin`, substitute `origin/main`.)

3. **Scope check before writing code.** Ask: how many contracts does
   this change touch? The answer must be **one** (Rule 1). If the
   design introduces or changes a core-platform contract, plan for a
   maintainer hold (Rule 2). If the change is large, plan the stages
   (Rule 3).
4. **Dead-limb check.** If your plan resurrects anything in the
   dead-limbs table at the bottom, stop and get a maintainer decision
   first.
5. **Write the failing test first, then the fix.** The test ships in
   the same PR as the source change (see "Tests ship with fixes").
6. **Run the gates.** Minimum: `make check`; add `make check-docs` for
   docs/link changes and `make dashboard-check` for anything touching
   `internal/api/`, OpenAPI specs, or the dashboard. Full gate map and
   the local-vs-CI traps: **gc-build-verify** / `TESTING.md` /
   `AGENTS.md` "Code quality gates".
7. **Write the PR so its title and body cover 100% of the diff.** The
   repo's costliest incident was a diff that exceeded its description
   (Rule 1). Fill the PR template checklist honestly.
8. **Route for review.** If any Rule 2 trigger fires, say so explicitly
   in the PR body ("this introduces a new core-platform contract:
   ...") and expect a human hold. Green CI is necessary, never
   sufficient, for contract changes.
9. **After merge, watch main.** Reverts here are fast and blameless
   (the #3194 revert landed ~15 minutes after the bad merge). A revert
   of your PR is usually a process statement, not a code judgment —
   read the revert message; it will say what to do next.

---

## Rule 1 — One contract per PR; the diff never exceeds the description

A PR's title and description must account for everything in its diff.
Bundling a feature under a fix title is the single fastest way to get
the whole PR reverted, including its legitimate parts.

### Worked example (failure): the #3194 smuggling incident

All on 2026-06-07, on `main`:

- `0905fa33a` — PR #3194 lands, titled _"fix(ci): lazy UNIT_COVER_PKGS +
  15m timeout for make test (ga-uzijoh)"_. The title describes a 4-line
  Makefile fix. The diff also contains a complete, undocumented
  **~700-line order-tracking-retention feature** (watchdog + doctor
  check + bounded sweeps + schema + tests). A reviewer flagged the
  bundled scope with a human hold, but the PR **auto-merged on green
  CI before the concern was resolved**.
- `19e34ab71` — ~15 minutes later, the **entire PR is reverted**
  (#3209): _"unbundle order-tracking-retention feature merged under a
  CI-fix title."_ Note what got reverted: the legitimate 4-line CI fix
  too. Bundling doesn't just risk the smuggled part; it takes the good
  part down with it.
- `9ac732cd8` — same day, the 4-line CI fix re-lands **as its own clean
  PR** (#3214), with a proper root-cause writeup.
- The retention feature went back through design + focused review and
  landed reviewed over the following week (e.g. `71b94ff4e` retention
  visibility #3202; `29bed0116` retention default hardening #3424).

Costs of the bundle: one revert, two re-land PRs, a design doc, and a
week of latency — versus two small PRs up front.

**Apply it:** before opening a PR, diff your branch against its base and
read the file list. Anything a stranger wouldn't predict from your
title gets split out:

```bash
git diff --stat origin/main...HEAD
```

## Rule 2 — New core-platform contracts are held for a human

> **PROVISIONAL (2026-07-07):** this routing rule is derived from the
> revert record (`b8120d697`, `19e34ab71`) and the maintainer's standing
> autonomy boundary; the maintainer has not yet ratified the exact
> wording. Treat the trigger list as the floor, not the ceiling: when
> unsure, route to a human.

Hold a PR for explicit maintainer review — regardless of green CI and
regardless of code quality — when it:

- **introduces or changes a cross-subsystem contract**: event payload
  shape, bead/molecule schema or metadata keys (e.g. new `gc.*` bead
  metadata), runtime provider protocol, formula format, config schema,
  public CLI or HTTP/SSE surface; **or**
- **touches change-control or automation-gating itself**: CI required
  checks, auto-merge conditions, review-pipeline behavior, branch
  protection.

### Worked example (failure and recovery): per-dispatch model selection

- `b8120d697` (2026-06-07) — reverts PRs #3055/#3068, which applied a
  per-dispatch model/effort selection read from bead metadata. The
  revert message is the canonical statement of this rule, and is worth
  reading in full (`git show -s b8120d697`). Key lines:

  > _"This is on us, not the contributor. [The] implementation is
  > sound. The issue is that these PRs introduce a new core-platform
  > contract — per-dispatch model/effort selection via bead metadata —
  > and that is an architectural decision that needs maintainer review
  > before it lands. Our automated review pipeline merged them without
  > routing that decision to a human."_

- `331c66ceb` (2026-06-07) — the revert is itself reverted once the
  maintainer review happened. **The code came back unchanged.** The
  entire cost of the incident was the missing routing step, not the
  implementation.

**Apply it:** if any trigger fires, write in your PR body: _"This
introduces/changes a core-platform contract: `<name it>`. Holding for
maintainer review."_ Sound code merged without that routing gets
reverted first and reviewed second.

## Rule 3 — Staged landing beats big-bang

Large features land as a gated sequence, not one merge. The pattern
that survives: **flag-gated infrastructure (off by default) → trimmed
big merge → months of soak → default-on**.

### Worked example (success after two failures): the orders-v2 / formula-v2 saga

- `85783a923` (2026-03-24) — formula v2 lands as one large feature
  commit. **Reverted the same day** (`6450f8869`).
- `c748f4828` (2026-03-25) — the re-approach starts by landing the
  gate first: _"gate formula v2 infrastructure behind
  daemon.graph_workflows flag."_ Infrastructure can now land dark.
- `3b805d0c0` (2026-03-29) — orders v2 mega-merge (#196). **Reverted
  next day** (`25d3da6d7`).
- `948e12c87` (2026-03-30) — the successful re-land. Read its message:
  branch-first merge strategy (started from the feature branch, merged
  `main` into it to preserve 20 upstream contributor PRs), all v2
  infrastructure behind the flag, and — critically — **scope trimmed**:
  _"Excluded: contracts/, personal formulas, contract CI/sync."_
  Finalized as `2e2df0ed5` (#210, 2026-03-31).
- `bb824a86d` (2026-06-18) — only now, ~11 weeks later, formula v2
  becomes default-on (`DaemonConfig.FormulaV2 *bool`, effective value
  via `FormulaV2Enabled()`); one interim release even shipped with
  `formula_v2` deliberately omitted from generated configs while UX
  issues were fixed.

Two big-bang attempts died in under 24 hours each. The staged version
of the _same feature_ is now core, on by default.

**Apply it:** for anything above a few hundred lines touching the SDK
core, propose the stage sequence in the PR/issue up front: (1) flag or
config-presence gate, (2) infrastructure dark, (3) behavior behind the
gate, (4) default flip as its own reviewed PR. Config **presence** is
Gas City's activation mechanism (`AGENTS.md`, progressive capability
model) — you rarely need a new boolean flag; a new config section that
activates the feature when present is the idiomatic gate.

## Adopt / take-the-good etiquette (incoming contributor PRs)

Maintainers do not merge outside PRs raw and do not rewrite them from
scratch. The pattern is **adopt**: keep the contributor's commits, add
the missing pieces as maintainer commits, land as a superseding PR that
credits and links the original.

### Worked example (success): adopting #3362

`d326a5f1f` (2026-06-28) — _"fix(test-isolation): resolve symlinks in
discovery (adopts #3362)"_ (#3784). Anatomy, from its commit message:

- **Contributor's fix preserved** as their own commit inside the
  adopting branch, named and credited: _"@bourgois's fix preserved
  (commit ed48e402e)."_
- **Maintainer added what was missing** — a regression test proven
  _"RED pre-fix, GREEN post-fix."_ The review verdict was literally
  _"take-the-good — fix is correct ... the only gap was test evidence,
  now closed."_
- **Re-homing explained**: the PR moved to a maintainer branch only
  because pushing to the contributor's fork was blocked (403), and the
  message says so.
- **Gates listed** in the message (gofmt, vet, new suite green).

**Apply it when adopting:** never squash away the contributor's
authorship; state exactly what you added and why; run the same gates
you'd demand of your own PR; link the superseded PR with "adopts #N".
If you're the _contributor_ being adopted: this is the good outcome —
your code lands with credit and better tests.

## Tests ship with fixes

The regression test lands **in the same PR as the source change**, not
as a follow-up. This is both a repo norm (`CONTRIBUTING.md`: "Add tests
for behavior changes"; `AGENTS.md`: TDD, tests cover happy path AND
edge cases) and the observed adoption bar: the only gap that kept
#3362 from merging as-is was missing test evidence. A fix without its
RED→GREEN test is an incomplete PR here. How to choose the test kind:
sibling skill **gc-test-authoring** / `TESTING.md`.

## Docs rules for changes

- **Tutorials win over architecture docs** (`AGENTS.md`, settled
  decisions). If your change makes a tutorial wrong, fixing the
  tutorial-facing behavior or the tutorial is part of the change.
- Architecture docs describe **current** behavior; design docs describe
  **proposed** behavior; archive keeps history out of the onboarding
  path (`CONTRIBUTING.md`, Docs Workflow).
- `docs/` is published via Mintlify with **extensionless** links; a
  `docs/` link that looks broken on GitHub is often correct on the live
  site. Do not "fix" docs paths for GitHub rendering — that silently
  breaks the published site. Full table and the two enforcing checks:
  `CONTRIBUTING.md` "Docs link conventions" (added 2026-06; verify it
  exists in your checkout before relying on the exact check names).
- Run `make check-docs` for any docs/nav/link change.

## Change-control applies to change-control

Changes to CI gates, merge automation, and review pipelines are
themselves core-platform contract changes (Rule 2) — and the repo
deliberately trades gate strictness for contributor throughput:

- `0903b265d` (2026-06-28) — a CI gate requiring PR→issue linkage
  (`Closes #N` or a `no-issue` label) is added by the maintainer.
- `bfdb30777` (**same day**) — the maintainer reverts her own gate:
  _"The contributor-facing CI gate adds friction to outside
  contributions."_ Linkage moved into the review process as an
  advisory finding; the merge-watcher (auto-close linked issues on
  merge) — the part that actually solved the problem — stayed.

Two lessons. First: when proposing a new blocking check, separate the
**lever** (what fixes the problem) from the **friction** (what
contributors pay), and expect only the lever to survive. Second: no
skill, agent, or automation may route around change-control — the
#3194 and #3055 incidents were exactly automation routing around a
human hold, and both ended in reverts.

## Dead limbs — do not resurrect without a maintainer decision

> **PROVISIONAL (2026-07-07):** the "parked, not dead" status of each
> limb is a provisional maintainer position, not a ratified roadmap.
> The commits and current-state facts are verified.

| Limb                                 | Evidence                                                                                                                                                                           | State (2026-07-06)                                                                                                                 |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| Idle-session nudger                  | Reverted `3bc34e0db` (#468, 2026-04-08): _"causing too much churn ... back it out until the wake/nudge architecture is redesigned"_; redesign tracked as follow-up bead `test-5il` | Not re-landed under that name. Check for the redesign before building anything that wakes idle sessions.                           |
| Delivery-phase state machine (#3177) | Reverted `ca4b888fb` (#3334, 2026-06-10)                                                                                                                                           | Not re-landed as of 2026-07-06.                                                                                                    |
| Oversight-rig / Slack pack           | Never merged; exists as fork branch work (e.g. `feat/oversight-rig-pack` on the maintainer's fork)                                                                                 | Fully built, security-hardened, parked outside `main`.                                                                             |
| ~~Formula v2~~                       | `948e12c87` staged re-land; **default-on since `bb824a86d` (2026-06-18)**                                                                                                          | **Not a dead limb** — landed and enabled by default. Listed here because older notes call it parked; the code is the ground truth. |

If your issue or plan overlaps a limb: comment on the relevant issue or
open one asking for a maintainer decision **before** writing code. The
limbs are parked precisely because their landing shape is undecided;
independently re-landing one guarantees rework.

## Pre-PR checklist

- [ ] Branch is `fix/*`|`feat/*`|`refactor/*`|`docs/*` off `main`, one PR per branch
- [ ] `git diff --stat <base>...HEAD` read end to end; every file is explained by the PR title/body (Rule 1)
- [ ] Zero or one contract touched; if one, it is named in the PR body with an explicit maintainer-review flag (Rule 2)
- [ ] Large change has a stated stage plan; infrastructure lands dark behind config presence (Rule 3)
- [ ] No dead limb resurrected without a maintainer decision
- [ ] Regression test in this PR, RED before the fix, GREEN after
- [ ] `make check` green; `make check-docs` if docs touched; `make dashboard-check` if API/dashboard touched (details: gc-build-verify)
- [ ] Docs updated per "docs rules" above; issue linked or absence explained
- [ ] PR template checklist filled honestly (it asks for exactly these)

## Provenance and maintenance

Authored 2026-07-06 against `gastownhall/gascity` `origin/main` @
`f828bbe4b`, from the repo's own git history, `AGENTS.md`,
`CONTRIBUTING.md`, `TESTING.md`, and the fable-distillation discovery
report (machine-local, non-load-bearing:
`gas-city/docs/design/fable-distillation/discovery-gascity.md`).
Items marked **PROVISIONAL** stand in for unratified maintainer answers
(morning ledger 2026-07-07) and should be re-checked against her actual
answers.

Re-verification one-liners for facts that can drift:

```bash
# Cited commits still on main (any drift = history rewrite, re-audit all):
for c in 0905fa33a 19e34ab71 9ac732cd8 b8120d697 331c66ceb 85783a923 \
         c748f4828 3b805d0c0 25d3da6d7 948e12c87 2e2df0ed5 bb824a86d \
         d326a5f1f 3bc34e0db ca4b888fb 0903b265d bfdb30777; do \
  git merge-base --is-ancestor $c origin/main || echo "GONE: $c"; done

# Formula v2 still default-on:
git grep -n "FormulaV2Enabled" origin/main -- internal/config/config.go

# Dead limbs still un-re-landed (empty output = still parked):
git log origin/main --oneline --grep='idle.nudg' -i --since=2026-04-09
git log origin/main --oneline --grep='delivery.phase' -i --since=2026-06-11

# Workflow mechanics unchanged (branch naming, gates, docs-link rules):
git log origin/main -1 --format='%h %ad' -- CONTRIBUTING.md AGENTS.md .github/pull_request_template.md

# Issue-linkage gate still absent (output = gate re-added, update §change-control):
git show origin/main:.github/workflows/pr-issue-linkage.yml 2>&1 | head -1
```
