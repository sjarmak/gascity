# Task 02 — Implementation plan from issue (mol-pr-from-issue planning portion)

Frozen input: `inputs/issue-3972.json` (issue #3972: "bug: session event
delivery is lossy and its failures are invisible"). Repo access: read-only
detached worktree of gastownhall/gascity at pinned SHA
`ee616a7e41c74285d37283e9ca0022db120e9f14`.

Tests: coverage (blast radius), decomposition, verification planning.

## Setup (harness does this, not the model)

git -C /home/ds/gascity worktree add --detach /tmp/fable-baseline-task02 ee616a7e41c74285d37283e9ca0022db120e9f14

Point the agent at /tmp/fable-baseline-task02 as its working directory.
Remove the worktree after the run.

## Run prompt (verbatim, plus the issue JSON appended)

You are planning a fix for the GitHub issue below in this repository (you
have read-only access to the checkout; do not modify anything, do not run
mutating git commands). Produce an implementation plan a mid-level engineer
could execute without further judgment calls: (1) root-cause hypothesis with
the file:line evidence you verified in this checkout; (2) blast radius — every
caller, config path, goroutine boundary, and cross-subsystem effect the fix
touches; (3) at least two fix candidates with a reasoned pick; (4) step-by-step
implementation with per-step verification commands; (5) test strategy — the
regression test that ships in the same commit, and what it must assert;
(6) risks that would make a maintainer reject this, and how the plan
pre-empts each. If the issue's premise is wrong or the bug is not reproducible
from the code, say so with evidence instead of planning a fix.

## Output

`outputs/task-02/<model>.md` — full response, verbatim.
