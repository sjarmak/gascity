# Evals-as-nightly-CI — task corpus (gc-xd77u)

Report-only pilot. The nightly `evals-nightly` order runs `bin/eval-runner`,
which judges candidate traces against the task corpus and writes a report to
`.gc/evals-report.log`. **No promotion gate is wired yet** — the gate flips on
only after a stable shadow week (gc-xd77u phase 5).

## Task schema (`evals/tasks/*.yaml`)

```yaml
id: gascity-mol-green-build          # unique, <source>-<slug>
source: gascity-mol | mem-membench | scix-retrieval
description: one line — what capability this scenario exercises
candidate: path or query that yields the artifact to judge (a recorded trace,
           a command's exit+output, a bead event). For the pilot, an example
           trace file under evals/candidates/.
rule_judge:                          # deterministic (ZFC-allowed structural checks)
  max_tool_calls: 40                 # "solved within N tool calls"
  require_build_green: true          # build/test exited 0
  no_new_violations: true            # violations count <= committed baseline
rubric_judge:                        # LLM rubric (delegated reasoning)
  criteria: |
    Did the run actually solve the stated task (not just exit green)?
    Score PASS only if the artifact demonstrates the capability end-to-end.
```

A task **passes** when the rule judge passes AND the rubric judge returns PASS.
The rule judge is deterministic (my floor). The rubric judge delegates the
quality call to a model (ZFC) — `bin/eval-runner` invokes it via `claude -p`;
if no model is reachable it records `rubric: SKIPPED` and the run still completes
(report-only never blocks).

## Guardrails (non-negotiable — gc-xd77u)

- **Private held-out set** lives in `evals/golden/` — a protected path. Its
  contents are NEVER injected into an eval-agent prompt and never synced to the
  practices repo.
- **No agent edits to golden data.** `bin/eval-runner` hashes `evals/golden/`
  before and after each run; a mismatch aborts with a loud error. Eval agents get
  read-only visibility.
- **Network allow-list during runs** — scenario execution (the HELD heavy phase)
  runs under a restricted network. Not exercised by the report-only pilot;
  mechanism is decision #3 (Stephanie's ledger).

## Scope split

- **In-floor (here):** the runner, the corpus, the judges, the golden set, the
  order, the promotion-gate comparator.
- **External / gated:** syncing task or skill versions to the practices repo —
  Stephanie's gate, not done here.

## Status

Pilot: schema + seed tasks + report-only runner + golden guard + nightly order.
HELD (box-health / green-light): the trace-mining + judge-authoring ultracode
workflow that scales the corpus to 50–200 real tasks (gc-xd77u phases 1–3).
