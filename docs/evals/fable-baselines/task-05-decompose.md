# Task 05 — Epic decomposition (mol-decompose analog)

Frozen input: `inputs/epic-dr-2vydrm.txt` (real open epic: "Three-benchmark
QA framework — VERIFICATION_REPORT remediation across CSB / EB / codeprobe").

Tests: decomposition depth/ordering, verification planning, routing judgment.

## Run prompt (verbatim, plus the epic snapshot appended)

You are decomposing the epic below into dispatchable work items ("beads") for
a multi-agent system where mid-tier model workers execute items independently
and in parallel where dependencies allow. Using ONLY the epic content, produce:
(1) the full child-bead breakdown — for each: title, one-paragraph scope, an
acceptance criterion a verification agent can TEST (a command or observable,
never "looks correct"), dependencies on sibling beads, and a size class
(one-session / multi-session / needs-split); (2) the dependency graph and the
maximal parallel waves it permits; (3) routing per bead: which items need a
top-tier model (deep reasoning), which a mid-tier (well-scoped execution),
which a cheap tier (mechanical), with the reason; (4) where review gates
belong so a wrong early result cannot silently poison the later waves;
(5) what is UNDERSPECIFIED in the epic — questions that must be answered
before dispatch, and your provisional assumption for each. Do not gold-plate:
every bead must trace to something the epic actually requires.

## Output

`outputs/task-05/<model>.md` — full response, verbatim.
