# Evals-as-nightly-CI — implementation plan (gc-xd77u, P1)

Approved by Stephanie 2026-07-03 (AIEWF-2026 distillation; ~/brain/AIEWF 2026/
Concepts/Action Plan - Our Setup.md). city-infra-pl owns. Blocks gc-ez3x2
(autoresearch loop).

## Goal

A small task corpus (50–200 YAML scenarios distilled from real gascity mol-run /
mem membench / scix retrieval traces), each with a **deterministic rule judge**
(solved within N tool calls; build green; no new violations vs the committed
baseline) + an **LLM rubric judge**, run **nightly via a `gc order`**, with a
**promotion gate**: a harness change (skill edit, model swap, hook change) is
adopted only when the candidate beats the prod baseline on the suite — a
one-point delta suffices (73% vs 72%). Weekly agent-as-judge pass files beads for
patterns rubrics miss.

## Floor boundaries (what I build in-place vs surface)

- **IN-FLOOR** (`/home/ds/gas-city`, my domain, land here):
  - `orders/evals-nightly.toml` — the nightly order (report-only first).
  - `bin/eval-runner` + `bin/eval-judge-*` — runner, rule judge, rubric judge,
    promotion-gate comparator, weekly agent-as-judge dispatcher.
  - `evals/tasks/*.yaml` — the scenario corpus (versioned here).
  - `evals/golden/` (held-out) + a protected-path guard.
  - Reading trace SOURCES is in-floor read-only (gascity mol transcripts, mem
    membench, scix logs via their stores).
- **EXTERNAL / gated (HARD FLOOR — surface, do not do)**: pushing task/skill
  versions to the **practices repo** (external repo). The nightly-CI artifacts
  live city-local; practices-repo sync is a separate mayor/Stephanie-gated step.

## Phases (Stephanie's ultracode shape + floor mapping)

1. **Trace mining** — parallel scouts over gascity mol transcripts, mem membench
   tasks, scix query logs → candidate YAML tasks. Heavy/parallel → ultracode
   workflow. Read-only, in-floor.
2. **Barrier: dedup + curate** the full candidate set → the corpus.
3. **Judge authoring** — per-task rule + rubric judge, then an **adversarial
   reward-hacking pass** that actively tries to game each judge before it ships.
4. **Wire the order + promotion gate** — nightly run + baseline-beat comparator.
5. **Shadow week** — nightly REPORT-ONLY; the gate flips on **only after** the
   shadow-week numbers are stable. Ship phases 1–3 as one PR-sized slice.

## Guardrails (non-negotiable — baked into the design)

- **Private held-out set** from our own repos, in a protected city-local path;
  never shipped to the practices repo, never in a prompt an eval agent sees.
- **No agent edits to golden data** — golden set on a read-only/guarded path; the
  runner + agent-as-judge cannot write it; a PreToolUse-style guard or a
  post-run integrity check (hash the golden dir before/after).
- **Network allow-list during eval runs** — eval tasks run under a restricted
  network (mechanism TBD: scix-batch-style transient cgroup, or a per-run
  netns/allow-list). Detection separate from fail-closed enforcement.

## Decisions (answered by Stephanie 2026-07-03, via mayor gc-449123)

1. **N for "solved within N tool calls"** = PER-TASK, derived at mining time from
   the observed solve length + margin. No global default. (Schema already carries
   `rule_judge.max_tool_calls` per task.)
2. **Held-out split** = all 3 sources (gascity mol / mem membench / scix
   retrieval) contribute golden; **~70/30 train/held-out** as the starting default,
   firmed up from the pilot's actual task yield. Held-out stays truly held-out —
   never in judge dev, never in a prompt.
3. **Network allow-list** = the eval-exec phase (HELD) enforces in-floor via
   `systemd-run --user --property=IPAddressDeny=any --property=IPAddressAllow=<cidrs>`
   (there is no `bin/scix-batch` in the city; systemd-run IPAddress cgroup
   properties are the in-floor lever, confirmed available). If clean enforcement
   proves infeasible, fall back to **detection-only** (report violations, don't
   fail-closed) — do not block. The report-only pilot needs no network control
   (it judges pre-recorded candidates).
4. **Practices-repo boundary** = CONFIRMED city-local only. Tasks/judges/results/
   golden stay in `/home/ds/gas-city`; upstream sync is a separate,
   mayor/Stephanie-gated step, never auto on a gate-pass.
5. **Workflow timing** = WAIT for box-health. The token-heavy trace-mining +
   judge-authoring multi-agent workflow (phases 1–3 scale-up) holds until the
   scix-postgres OOM relief lands or the mayor green-lights. The light report-only
   pilot proceeds now (additive, safe at load).

## Pilot-first start (light, in-floor, safe now)

Before the heavy workflow: scaffold the shape with a handful of hand-authored seed
tasks (1–2 per source) to prove the runner + judge contract end-to-end —
`evals/tasks/` schema, `bin/eval-runner` + rule/rubric judge, `orders/evals-
nightly.toml` (report-only), and the golden-path guard. Then run the trace-mining
workflow to scale the corpus to 50–200, box-permitting.
