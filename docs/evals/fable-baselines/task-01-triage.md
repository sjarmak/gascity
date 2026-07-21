# Task 01 — Issue triage ranking (gascity-triage analog)

Frozen input: `inputs/issues-snapshot-2026-07-06.json` (40 open
gastownhall/gascity issues; number, title, labels, createdAt, body truncated
to 1500 chars).

Tests: prioritization, coverage, restraint, calibration.

## Run prompt (verbatim, plus the file content appended)

You are a senior contributor to gastownhall/gascity (a Go multi-agent
orchestration framework) deciding what to work on. Below is a frozen snapshot
of open issues. Using ONLY this snapshot (no web, no live tools), triage every
issue into: GRAB NOW (self-contained, high value, low risk of colliding with
maintainer work), GOOD CANDIDATE (valuable but needs scoping or carries
risk — say which), INVESTIGATE (cannot classify without more evidence — say
exactly what evidence), SKIP (say why: duplicate, maintainer-owned,
wontfix-shaped, or stale). Then rank the top 5 you would start today, each
with: the concrete first step, expected blast radius, the test that proves
the fix, and what could make this pick wrong. State your confidence per
classification. Do not pad; a wrong confident classification is worse than
an INVESTIGATE.

## Output

`outputs/task-01/<model>.md` — full response, verbatim.
