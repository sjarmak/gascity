---
name: gc-flows
description: Use when you are unsure which skill, formula, agent, or compass to reach for — the top-level router over this workspace's operational surface. Maps intent to entry point, and the flows between them (incoming PR, our own change, dispatch, diagnose, plan, review, handoff). Start here when lost; summon the specific skill it points you at.
---

# gc-flows — which skill/flow do I reach for

Intent on the left, entry point on the right. Follow the arrow chains; each is a
flow, not a single step. When a row names another skill, summon that skill.

## Incoming contributor PR (gastownhall/gascity or the dashboard)

Flow: **triage → review → take-the-good → ship + merge.**

- `/gascity-triage` or `/gascity-queue` — scan open issues/PRs, rank the work.
- `/gascity-review-incoming-pr` — maintainer review of one PR (blast-radius +
  29-rule audit + multi-model). Dashboard variant: `/gascity-dashboard-review-pr`.
- Take-the-good is the default: complete the fix ourselves, credit the
  contributor, merge. `request-changes` is a last resort (see
  `rules/.../feedback` — scope creep / can't-execute only).
- `/gascity-ship` (or `/gascity-dashboard-ship`) — simplify → multi-model review
  → mechanical checks → writes the ship-pass sentinel the push gate requires.
  Never pushes on its own.

## Our own change (issue → PR)

Flow: **pr-start → implement → ship.**

- `/gascity-pr-start` (or `/gascity-dashboard-pr-start`) — blast-radius +
  convention audit + test plan **before** any code.
- `gascity-blast-radius` — map the full impact surface of a proposed change.
- `gascity-check` — the 29-rule contributor audit + mechanical gates.
- `/gascity-issue-write` — maintainer-grade upstream issue with dup-search +
  source verification before filing.
- `/gascity-ship` — the pre-push pipeline. Push/merge stays gated per-action.

## Dispatch work (mayor / project-lead)

- `gc-dispatch` and the `gc-sling` wrapper — route a bead to an agent
  (auto-nudge + formula rules). `compass-bead-dispatch` for the mechanics,
  claim handoff, and formula attach.
- Formulas (dispatch contracts): `mol-do-work` (small, ≤3 files) /
  `mol-scoped-work` (multi-package DAG) / `mol-pr-iterate` (address PR feedback)
  / `mol-pr-from-issue` (issue → PR) / `mol-epic-review` / `mol-research`.
  `mol-focus-review` implements-then-self-reviews but has known reliability
  issues (orphaned commits, zero-diff cycles) — prefer `mol-do-work`.
- `gc-work` (find/create/claim/close beads), `gc-agents` (pools), `gc-rigs`
  (rigs), `gc-mail` (inter-agent mail), `gc-city` (lifecycle).

## Diagnose something broken

- `mechanic` — trace the root cause of gc / beads / dolt / supervisor / config /
  pack behavior **from source**, before any per-bead or per-config workaround.
- `diagnosing-bugs` — build a `red`-capable reproduction loop **before**
  hypothesizing; composes with `mechanic` (mechanic reads the source, this pins
  the repro).
- `compass-*` — subsystem file-indexes: `compass-dolt`, `compass-tmux-supervisor`,
  `compass-bead-dispatch`, `compass-capacity`, `compass-scanners`,
  `compass-gc-binary`.

## Plan or think through a problem

- `grill-me` — interview until shared understanding on an ambiguous spec.
- `brainstorm` — divergent options with shape-uniqueness enforcement.
- `deep-research` / `exa-search` — multi-source web research with citations.
- `planner` / `architect` agents — implementation plans and system design.

## Review quality and prose

- `writing-voice` — anti-slop pass. Mandatory on every external artifact: PR
  body, issue, public reply, release notes, longer docs.
- `slop-check` — erosion/verbosity LLM-judge on a diff (the accumulated-cruft
  lens, complements `/review`).
- `/review`, `/review-pr` — multi-model code review (Claude + Codex).
- `/simplify` — reuse/simplify/efficiency cleanup (quality, not bug-hunting).

## Handoff and continuity

- `gascity-handoff-write` — a self-cleaning handoff prompt prefilled from memory.
- `gc handoff "<summary>" "<context>"` — mail-to-self + session restart carrying
  full context to the next incarnation.

## Authoring skills

- `rules/reference/skill-authoring.md` — the discipline: the two loads, leading
  words, positive targets, checkable completion criteria, progressive disclosure.
- `skill-create` / `skill-health` / `skill-stocktake` — scaffold and audit.

**Maintenance:** when you add, rename, or remove a skill or formula, update this
router in the same change. A router that lies is worse than none.
