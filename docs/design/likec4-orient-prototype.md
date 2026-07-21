# LikeC4 orientation-layer prototype (Design A) — build + measured savings

**Status:** prototype delivered (mayor gc-422942 / Stephanie option a — thin Design-A prototype on mem).
**Date:** 2026-06-26 · **Author:** city-infra-pl
**Scope:** Design A (orientation) ONLY. Design B (governance gate) stays NO-GO per the scoping memo (`likec4-context-governance-scoping.md` §5) until the model-currency prerequisite lands.

---

## Mechanism built

`bin/likec4-orient <rig-dir> [--json <cached-export.json>]` — a **mechanical, ZFC-clean**
transform of `npx likec4 export json` into a compact orientation summary an agent loads at
dispatch instead of cold grep-walking an unfamiliar tree:

- **element tree** (dot-hierarchical id → title, kind, delivery tag `#built/#evolving/#planned/#research/#risk`),
- **one-line description** per element (first sentence, capped),
- **resolved source-file link** per element (where to point a targeted read / CodeGraph),
- **relation edges** (`source → target: label`).

Styles, layout, fonts, colors are stripped. No model reasoning, no scoring, no semantic
classification — pure mechanical summarization (squarely inside the ZFC "mechanical transforms"
allowance). Sample output committed: `docs/design/likec4-orient-mem-sample.md`.

## Measured token savings (mem testbed)

mem's model exports cleanly (58 elements / 40 relations). Measured on this host:

| Artifact | chars | ≈ tokens |
|---|---|---|
| **derived orientation summary** (`likec4-orient /home/ds/projects/mem`) | 13,936 | **~3,500** |
| grep-to-orient baseline — *conservative*: tree-listing + `README.md` + `ARCHITECTURE.md` + `src/**/index.ts` | 55,032 | **~13,760** |

**→ ~10,200 tokens saved per cold dispatch into mem**, and the summary is reusable (a durable
map) vs the grep walk (implicit, lost next task). The baseline is conservative — a real cold
orientation that greps for entry points and reads several subsystem files lands in the memo's
cited **15–40K** range, so real savings are likely higher. The summary lands ~3.5K vs the ~2K
target; it's tunable lower (drop per-leaf source links or shorten descriptions) but 3.5K with
exact source paths is the more useful artifact and still saves ~75% of orientation cost.

Complementary to CodeGraph (two-tier map): LikeC4 = high-altitude "what subsystems exist + how
they connect" (cheap, main session); CodeGraph = symbol-level depth (via Explore agents, off the
main context). Load the LikeC4 summary → pick the container → hand its source link to CodeGraph.

## Dispatch-time loading mechanism (wiring proposal)

The transform tool is the city-infra half (built, `bin/`). The per-rig wiring is a rollout step
(writes into rig repos = surfaced, not done in this prototype):

1. A periodic `gc order` runs `likec4-orient <rig>` per tracked rig and writes the summary to
   `architecture/exports/orient.md` (regeneratable, kept fresh on the model's cadence).
2. The rig's `CLAUDE.md` `@import`s `architecture/exports/orient.md` (or a dispatch-time
   compass-style skill loads it), so a dispatched agent orients off the map first.

This keeps the expensive artifact out of the source tree's hand-authored set (it's generated)
and degrades gracefully if stale (an out-of-date map still orients roughly right; worst case the
agent targeted-reads a renamed path and falls back to grep — low blast radius, per memo §5).

## Go / no-go on broader rollout

**GO — roll the orientation layer out per-rig, to TRACKED-model rigs first.** The savings are
large (~10K+/cold-dispatch), the mechanism is ZFC-clean and proven, and a stale model degrades
gracefully (unlike the Design-B gate, which inverts into active harm — hence still NO-GO).

Roll out **only as each rig crosses the trust bar** (model committed to git + published), not
city-wide in one shot:

- **Eligible now** (tracked + published per memo §1.3): `mem`, `tom-swe`,
  `code-intelligence-digest`, `website`, `embertide`.
- **Blocked until they commit `architecture/`** (~half the rigs, untracked today): `gascity-packs`,
  `agent-diagnostics`, `brains`, `codeprobe`, `background-agents`, `EnterpriseBench`, `GEO`,
  `mcp-ax`, `databot-agent`, `migration-evals`. Committing the model is the shared prerequisite
  for both A (orient off a versioned map) and B (the gate) — do it first.

**Recommended next step:** wire the prototype on the 5 eligible rigs (the `gc order` +
`CLAUDE.md @import` above), measure the real delta across 5–10 live dispatches, then decide
city-wide. The per-rig repo writes are rig-source → mayor/Stephanie-gated.
