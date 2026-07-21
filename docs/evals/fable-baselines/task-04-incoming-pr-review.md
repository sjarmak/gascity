# Task 04 — Incoming contributor PR review (maintainer decision report)

Frozen inputs: `inputs/pr-4006-meta.json` + `inputs/pr-4006.diff`
(PR #4006 by csauer02-personal-user, an external contributor:
"fix(control-dispatcher): prefer rig-scoped dispatcher..."). Repo access:
read-only detached worktree at pinned SHA
`ee616a7e41c74285d37283e9ca0022db120e9f14` (same setup as task 02, separate
worktree path).

Tests: coverage, verification, restraint (maintainer judgment), calibration.

## Run prompt (verbatim, plus meta JSON and diff appended)

You are a maintainer of this repository reviewing the external contributor PR
below (metadata + full diff; you also have a read-only checkout of main to
verify claims against — do not modify anything). Produce a maintainer decision
report, not a comment thread: (1) what the PR actually changes vs what its
description claims — flag any gap; (2) correctness: trace the changed code
paths against the checkout and state concretely what breaks or what the fix
provably repairs, with file:line evidence; (3) blast radius the author did
not mention; (4) test adequacy — does the shipped test pin the bug, would it
pass on unfixed main; (5) decision: APPROVE / REQUEST CHANGES / CLOSE, with
the exact change requests if any, each phrased so the contributor can act on
it without follow-up questions; (6) what you remain uncertain about and what
you would run to resolve it. Do not post anything anywhere; this is an
internal report.

## Output

`outputs/task-04/<model>.md` — full response, verbatim.
