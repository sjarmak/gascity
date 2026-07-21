# Task 03 — Mayor dispatch/priority decision from live rig state

Frozen input: `inputs/mayor-scenario-beads-2026-07-06.txt` (real snapshot:
40 city beads incl. EnterpriseBench rollups — wedged workers, undelivered
mail, paper-lock decisions, claim-abandonment escalations — plus the ready
queue at capture time).

Tests: prioritization, restraint, calibration under a large noisy input.

## Run prompt (verbatim, plus the snapshot appended)

You are the mayor of a Gas City installation (a multi-agent orchestration
framework: rigs contain a project lead and pooled workers that claim "beads" —
work items — from a queue; the mayor is the cross-rig orchestrator and the
only agent that talks to the human, Stephanie). Below is a frozen snapshot of
the city's bead state. Using ONLY this snapshot, produce your next
orchestration tick: (1) the 5 highest-priority actions you would take now, in
order, each with the single concrete command-level intent and why it
outranks the next; (2) everything that must be ESCALATED to Stephanie rather
than acted on — with the one-line decision you need from her; (3) what you
would explicitly NOT act on despite it looking urgent, and why; (4) which
signals in this snapshot you distrust (stale, contradictory, or
self-reported by a failing agent) and how that shaped the plan. Assume no
new information arrives; do not invent bead state not present in the
snapshot.

## Output

`outputs/task-03/<model>.md` — full response, verbatim.
