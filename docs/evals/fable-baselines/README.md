# Fable golden baselines — planning/orchestration A/B protocol

Bead: dr-j0d.1 (Phase 0 of the post-Fable harness-optimization epic dr-j0d).
Drafted 2026-07-06. Fable access ends soon; the Fable runs must happen first.

## Purpose

Capture Fable 5's outputs on five representative planning/orchestration tasks
as golden references, then run the identical frozen inputs on Opus 4.8 and
Sonnet 5. The diff classes (missed blast radius, shallow decomposition, wrong
prioritization, skipped verification, premature action) become the
requirements spec for Phase 3 (dr-j0d.4) and the acceptance measure for
dr-j0d.7 — we tune docs/structure until the diffs converge, not by vibes.

## Method

- Each task spec (`task-0N-*.md`) contains the exact run prompt and points at
  frozen inputs under `inputs/`. No live `gh`/`bd` calls during a run — frozen
  inputs only, so later runs see identical state. Tasks 02 and 04 allow
  read-only repo access at pinned SHA `ee616a7e41c74285d37283e9ca0022db120e9f14`
  (gastownhall/gascity main as of 2026-07-06) via a detached worktree.
- Run each task as a FRESH subagent (Claude Code Agent tool) with the model
  pinned: `fable` now; `opus` (4.8) and `sonnet` (5) for comparison runs. The
  subagent gets the spec's run prompt verbatim and nothing else — no session
  memory, no extra briefing. Same effort setting across models.
- Operational detail (must be identical across models): where a spec says the
  frozen input is "appended", the harness instead gives the agent the absolute
  input path(s) with the instruction to Read them as its first action and
  treat the contents as part of the prompt. Tasks 01/03/05: the agent may use
  no tools except Read on exactly those files and one Write for its output.
  Tasks 02/04: the agent additionally gets read-only access to its pinned
  worktree; no modifications, no mutating git commands.
- The agent Writes its complete response verbatim to
  `outputs/task-0N/<model>.md` and returns only a one-line confirmation.
- Three runs per model per task is ideal (variance matters); one is acceptable
  if quota is tight. Fable runs take priority over everything else.

## Scoring the diffs

Compare per task along these dimensions (rubrics to be formalized in the
rubric bank, bead dr-i4v.4):

1. Coverage — blast radius, affected subsystems, risks and edge cases named.
2. Decomposition — step granularity, ordering, dependency correctness.
3. Prioritization — does the ranking match Fable's, and where it differs, why.
4. Verification — does every step carry a concrete check; are gates placed
   where failures are cheap to catch.
5. Restraint — knowing what NOT to do: actions deferred to a human, work
   correctly declined, escalations flagged.
6. Calibration — confidence proportional to evidence; no invented facts.

## Status

- [x] Specs drafted, inputs frozen (2026-07-06)
- [x] Task selection approved by Stephanie (2026-07-06)
- [x] Fable runs captured, 1 per task (2026-07-07): outputs/task-0{1..5}/fable.md
      (16.5K / 20.2K / 12.0K / 12.0K / 20.5K). Optional: repeat runs for
      variance if Fable quota allows, highest value on tasks 01 and 03.
- [ ] Opus 4.8 / Sonnet 5 comparison runs
- [ ] Diff report → dr-j0d.4 requirements
