# LikeC4 as Context Layer + Governance Gate — Scoping Memo

**Status:** Scoping / survey (option a). No code or model changes made.
**Date:** 2026-06-26
**Author:** research session (ds-research / Gas City)
**Decision asked:** Is the heavier prototype (option b) / city-wide rollout (option c) worth building?

---

## TL;DR

We already carry a high-quality, machine-readable LikeC4 model in ~21 distinct projects
(instantiated across ~100 working dirs). Both proposed uses are technically feasible today —
the JSON export (`likec4 export json`) gives a clean element+relation+tag graph, and the
drift guard (`check-links.mjs`) already exists. **But the gating risk is model currency, and
it is currently failing in two concrete ways:** roughly half the distinct projects keep
`architecture/` *untracked in git* (working-tree only), and the one automated drift signal we
have is non-blocking and only catches dead links — not under-modeling or stale tags. **Verdict:
the orientation layer (use A) is worth a thin prototype now; the governance gate (use B) should
not ship until an automated, trusted drift-audit exists.** That drift-audit is the shared
prerequisite for both at scale.

---

## 1. Inventory & freshness (the gating unknown)

### 1.1 What exists

100 `architecture/` directories carrying the 4-file LikeC4 model
(`spec.c4` / `model.c4` / `views.c4` / `deployment.c4`) live under `/home/ds/projects/*` plus
`/home/ds/gascity-packs/architecture/`. These collapse to **~21 distinct projects** — the
`mem` family (~50 working dirs: `mem`, `mem-0b2o`, `mem-lvp.*`, `mem-ltte.*`, `mem-75t.*`, …),
`codeprobe` (~16: `codeprobe`, `codeprobe-3ry9`, …) and `EnterpriseBench` (3) are
run/worktree copies of a single canonical project each. Distinct projects include: `mem`,
`codeprobe`, `EnterpriseBench`, `agent-diagnostics`, `brains`, `tom-swe`,
`code-intelligence-digest`, `background-agents`, `GEO`, `website`, `mcp-ax`, `databot-agent`,
`embertide`, `migration-evals`, `WheelOfFortune`, `scix_experiments`, `pipegen-agent`,
`nls-finetune-scix`, `live_docs`, `CodeScaleBench`, `gascity-packs`.

87 copies carry `.github/workflows/likec4-pages.yml` (auto-deploy of the architecture page) —
propagated by the `architecture-refresh` skill's template-copy step (§E of
`~/.claude/skills/architecture-refresh/SKILL.md`).

### 1.2 Are they hand-authored or generated?

**Hand-authored.** There is no generator script. Each `model.c4` is bespoke prose-rich domain
modeling: every component carries a real one-line description and a `link` to its source path,
and risk/delivery state is hand-tagged (`#built` / `#evolving` / `#planned` / `#research` /
`#risk`). See `gascity-packs/architecture/model.c4` (398 lines) and
`mem/architecture/model.c4` (425 lines). These are not regenerable on demand — refreshing them
is a judgment task, which is exactly why the `architecture-refresh` skill exists and is
model-driven, not scripted.

### 1.3 Inventory + freshness table

| Project | distinct? | `architecture/` tracked in git? | likec4-pages CI | freshness evidence |
|---|---|---|---|---|
| `mem` (canonical) | yes | **yes** (13 files) | yes | arch last touched 2026-06-23; **15 code commits since** |
| `tom-swe` | yes | **yes** (12) | yes | arch 2026-06-17; **27 code commits since** (5 days) |
| `code-intelligence-digest` | yes | **yes** (12) | yes | tracked + published |
| `website` | yes | **yes** (12) | yes | tracked + published |
| `embertide` | yes | **yes** (12) | yes | tracked + published |
| `gascity-packs` | yes | **NO — untracked** (`?? architecture/`) | no | working-tree only |
| `agent-diagnostics` | yes | **NO — untracked** (0 files) | no | working-tree only |
| `brains` | yes | **NO — untracked** (0) | no | working-tree only |
| `codeprobe` | yes | **NO — untracked** (0) | no | working-tree only |
| `background-agents` | yes | **NO — untracked** (0) | no | working-tree only |
| `EnterpriseBench` | yes | **NO — untracked** (0) | no | working-tree only |
| `GEO` / `mcp-ax` / `databot-agent` / `migration-evals` | yes | **NO — untracked** (0) | no | working-tree only |

Sampled with `git ls-files architecture/` and `git log -1 -- architecture/` per repo.

### 1.4 Freshness verdict — MIXED / NOT-YET-TRUSTED

The models are **structurally excellent where present** (current spot-check: `mem` and
`gascity-packs` models accurately mirror their real subsystem layout — components, source
links, and delivery tags all line up with the code). But three facts block trusting them as a
governance substrate today:

1. **~Half of distinct projects keep `architecture/` untracked in git.** `gascity-packs`,
   `agent-diagnostics`, `brains`, `codeprobe`, `background-agents`, `EnterpriseBench`, `GEO`,
   `mcp-ax`, `databot-agent`, `migration-evals` all show `git ls-files architecture/` = 0. An
   untracked model **cannot be diffed at PR time** (nothing in the base tree to compare against)
   and is **never published**. A governance gate is structurally impossible on these until the
   model is committed.

2. **The one automated drift signal is non-blocking and narrow.** `check-links.mjs` walks every
   `.c4` and verifies each relative `link` target resolves — but it `process.exit(0)`s
   unconditionally (emits GitHub `::warning::`, never fails the run). And it only catches
   *dead links* (moved/renamed/deleted source). It cannot catch the most important drift class —
   a real subsystem that exists in code but is **missing or under-modeled** in the `.c4`
   (the `architecture-refresh` skill says this explicitly: "A regex cannot reliably catch
   'subsystem X is under-modeled'").

3. **Tracked models already drift between refreshes.** `mem` accumulated 15 non-architecture
   code commits in the 2 days since its model was last touched; `tom-swe` 27 commits in 5 days.
   Drift is the steady-state, and nothing currently fails on it.

**Blunt implication:** if a stale model is wired into either use case, it actively *misleads* —
an agent orients off a map that omits a subsystem, or a gate blocks/passes a PR against rules
derived from yesterday's structure. **Both uses are only as good as the drift-audit behind them,
and that audit is today manual (the `architecture-refresh` skill, human/agent-invoked).**

---

## 2. Model shape & machine-readability

What the `.c4` models encode (quoting `mem/architecture/model.c4`):

- **Elements** in a hierarchy: `actor` / `externalSystem` / `system` / `container` /
  `component` / `datastore`, nested by domain — e.g.
  `mem.store.ingest.beadReader`. Each carries a `description`, a delivery `#tag`, and a `link`
  to source:
  ```
  store = container 'Store builder & server' {
    description 'src/ — ingest → parse → store → retrieve → distill, exposed through the mem CLI'
    technology 'TypeScript / Node'
    link ../src 'src/'
    ingest = component 'ingest/' {
      #built
      description 'Source readers → raw WorkRecords (pure IO)'
      link ../src/ingest 'src/ingest/'
      beadReader = component 'beads + outcomes' {
        #built
        #risk
        description 'Reads the work spine and PR/commit outcomes. RISK: outcome linkage is structurally sparse …'
        link ../src/ingest/beads.ts 'beads.ts'
      }
    }
  }
  ```
- **Relationships** as directed, labelled edges: `source -> target 'label'`, e.g.
  `slackPack.spAdapter -> gascity.supervisor 'registers as proxy_process service; …'`. Some edges
  carry tags (`{ #planned }`).
- **Tags / delivery state** machine-readable per element (`#built/#evolving/#planned/#research/#risk`).
- **Deployment** (`deployment.c4`) maps elements to where they run.

**Machine-readability — confirmed strong.** `npx likec4 export json architecture` produces a
clean graph (validated live against `mem`):

```json
elements: 58, relations: 40
sample element: {"id":"github","kind":"externalSystem","title":"GitHub",
                 "tags":[...], "links":[{"url":"../ARCHITECTURE.md",
                 "relative":"file:///home/ds/projects/mem/ARCHITECTURE.md"}], ...}
sample relation: {"id":"1iqtegt","title":"reads work spine + labels",
                  "source":{"model":"mem.store.ingest.beadReader"},
                  "target":{"model":"gascity.beadStore"}}
```

So the dependency graph (who-talks-to-whom, with stable hierarchical IDs), the delivery tags,
and the resolved source-file links are all available **mechanically, no model reasoning
required**. That is the enabling fact for use B.

**Important gap for governance:** the models encode the **actual** edges, not **allowed/forbidden**
edges. There is no allow-list/deny-list construct in any model today (grep for
`forbidden|deny|allowedRelation` finds only prose in one description). LikeC4 has no native
"this edge is illegal" primitive either. So a boundary gate needs a **rule-set authored on top**
of the existing model (see §4B).

---

## 3. LikeC4 tooling (does a gate have something to read?)

Yes. Confirmed from `mem/.github/workflows/likec4-pages.yml` and a live run:

- **CLI** via `npx -y likec4@latest` — `validate`, `export png`, `export json`, `build`.
- **JSON export** (`export json architecture -o model.json`) — already used by the CI to feed
  `build-page.mjs`. ~200 KB for `mem` (includes layout); the element+relation+tag subset we'd
  consume is a small fraction.
- **GitHub Action** `likec4/actions@v1` (export / build) — already wired in 87 workflow copies.
- **Drift guard** `architecture/site/check-links.mjs` — a 60-line mechanical link-resolution
  walker (currently non-blocking).

A governance gate can therefore read the model with a one-line `npx likec4 export json` and a
small Node/JS consumer — no bespoke parser, no model-in-the-loop for the mechanical checks.

---

## 4. The two integration designs

### A. Context-savings orientation layer (mechanical context-loading — ZFC-clean)

**Goal:** an agent dispatched to a rig orients off the compact `.c4` map first, then
targeted-reads only the files the map points at — instead of grep/glob-walking an unfamiliar tree.

**Injection point (recommended):** a **dispatch-time skill / CLAUDE.md include**, not a new hook.
Concretely, a rig whose `architecture/` is present gets an auto-summoned compass-style skill (or
a CLAUDE.md `@import`) that loads a **derived summary**, generated mechanically from
`likec4 export json`:

- element tree (id → title → kind → delivery tag),
- one-line description per element,
- the relation list (`source -> target: label`),
- the resolved source-file `link` per element.

Strip styles, layout, fonts, colors. This is a mechanical transform of the JSON export — **no
model reasoning**, squarely inside the ZFC "mechanical transformations" allowance.

**Form: derived summary, not raw `.c4`.** Sizes measured:

| Artifact | size | ≈ tokens |
|---|---|---|
| `model.c4` raw | ~24 KB | ~6,000 |
| 4-file model raw | ~35 KB | ~9,000 |
| `export json` (full, w/ layout) | ~200 KB | too large — do not inject |
| **derived summary** (tree + 1-line + edges + links, est.) | ~6–10 KB | **~1,500–2,500** |

**Token-cost comparison.** A cold grep-to-orient on an unfamiliar rig — repeated
`glob`/`grep`/`Read` round-trips to discover top-level structure, entry points, and how
subsystems connect — routinely dumps **15–40 K tokens** of file content into the main context
before the agent has a mental map (and that map is implicit, lost on the next task). A
~2 K-token derived summary that *names every container/component, its purpose, its delivery
state, and its exact source path* replaces that discovery phase, then the agent does a handful
of **targeted** reads. Rough saving: **~10–35 K tokens per cold-orientation**, recurring on every
fresh dispatch into a rig.

**Composition with CodeGraph.** Complementary, different altitudes — frame them as a two-tier
map:
- **LikeC4 = the high-altitude map** (container/component, ~40–60 nodes, "what subsystems exist
  and how they connect"). Cheap to load wholesale.
- **CodeGraph = the street map** (symbol-level, used via Explore agents precisely because it
  returns large source sections — see project `CLAUDE.md`).
The orientation flow becomes: load LikeC4 summary (cheap, main session) → pick the relevant
container → hand its source `link` paths to an Explore/CodeGraph agent for symbol-level depth.
LikeC4 tells you *where to point* CodeGraph, which keeps the expensive tool's output off the
main context.

**ZFC note:** this is pure context-loading and mechanical summarization. No semantic
classification, no scoring. Clean.

### B. Governance / drift + boundary gate (PR-time check)

Two mechanically-separable checks; keep them apart because one is ZFC-clean and one is not.

**B1. Drift gate (mostly exists — needs hardening).** `check-links.mjs` already detects dead
source links mechanically. To make it a *gate*:
- flip it to **exit non-zero** on dead links (today it's `exit(0)` / warning-only), gating the
  PR when the model points at code that moved/was deleted;
- add the **harder drift class** — *missing/under-modeled subsystem* — which a regex **cannot**
  do. That stays a **model-agent** job (the `architecture-refresh` audit, §A of its skill),
  invoked as a PR-formula step, not a mechanical check. This is the ZFC boundary: "is subsystem
  X under-modeled / is this good design" → delegate to a model; "does this link resolve / does
  this edge exist" → code.

**B2. Boundary gate (new — ZFC-clean mechanical graph check).** Using `export json`:
- build the directed graph from `relations`;
- compare it to the **actual** code-level dependencies (import graph / CodeGraph edges) — flag
  any code edge that crosses a container boundary **not present in the model** (architectural
  drift: code grew a dependency the architecture doesn't sanction);
- optionally, enforce an **allow-list**: edges the architecture explicitly forbids
  (e.g. "ingest must not import server"). **This rule-set does not exist in the models today**
  and must be authored — either as a new tag convention on relations or a small sibling
  `architecture/boundaries.allow` file. The *check* is a mechanical graph operation (ZFC-clean);
  the *rules* are a one-time human/agent authoring cost.

**Where it hooks:** mirror the existing `likec4-pages.yml` pattern — a CI job (or a
`mol-pr-*` formula step in the `pr-pipeline` pack) that runs on PRs touching the rig, executes
`likec4 validate` + hardened `check-links` (B1 mechanical) + the boundary graph check (B2), and
— for the under-modeling class — slings the `architecture-refresh` audit agent (B1 semantic).
Mechanical checks block; the semantic audit advises.

**Prerequisite for B at all:** the model must be **committed to git** in the target repo.
Today ~half the projects fail this. No tracked model → no PR-base to diff → no gate.

---

## 5. Recommendation (go / no-go on options b & c)

**Split decision.**

- **Use A (orientation layer): GO for a thin prototype (option b, scoped to A).** It is
  ZFC-clean, the tooling is proven (`export json` works), the token math is favorable
  (~10–35 K saved per cold dispatch), and — critically — **a stale model degrades A gracefully**:
  an out-of-date map still orients an agent roughly right and saves grep cost; worst case the
  agent targeted-reads a renamed path and falls back to grep. Low blast radius if wrong.
  Prototype on one canonical, *tracked*, published rig (`mem` or `tom-swe`) as a derived-summary
  CLAUDE.md include, measure real token delta on 5–10 dispatches, then decide on rollout.

- **Use B (governance gate): NO-GO until the drift-audit is automated and trusted.** A gate
  inverts the failure mode of A: **a stale model actively harms** — it blocks a legitimate PR
  against yesterday's boundaries, or green-lights one because the rule it should have caught
  isn't modeled yet. Shipping a gate on top of models that are (a) untracked in half the repos
  and (b) only drift-checked by a non-blocking dead-link scan would generate false signal and
  erode trust in the whole architecture-model effort.

- **City-wide rollout (option c): NO-GO now**, gated on the prerequisite below.

**The shared prerequisite (do this first, regardless):** make the drift-audit **automated,
blocking-where-mechanical, and trusted**:
1. **Commit `architecture/` in every distinct project** (close the ~10 untracked repos:
   `gascity-packs`, `agent-diagnostics`, `brains`, `codeprobe`, `background-agents`,
   `EnterpriseBench`, `GEO`, `mcp-ax`, `databot-agent`, `migration-evals`). Until this is done,
   those rigs are out of scope for B entirely and orient off an unversioned map for A.
2. **Harden `check-links.mjs` to block** on dead links, and add it to CI on the untracked repos
   (propagate `likec4-pages.yml` via the `architecture-refresh` §E step, which already does this).
3. **Schedule the semantic drift-audit** (`architecture-refresh` §A) as a periodic `gc order` /
   reaper across rigs, so the under-modeling class is caught continuously, not ad-hoc. A model
   the city *trusts* is the thing that unlocks B and c.

Once a rig's model is committed + CI-drift-gated + on a refresh cadence, it qualifies for the B
gate. Roll B out **per-rig as each crosses that trust bar**, not city-wide in one shot.

---

## 6. Risks

- **Model currency (the dominant risk).** Both uses degrade with staleness; B *inverts* into
  active harm. Mitigation = the §5 prerequisite. This risk alone is why B waits.
- **Altitude mismatch.** LikeC4 is container/component-level, not line-level. It complements,
  never replaces, CodeGraph / grep. Selling it as a full code map would overpromise; frame it
  strictly as the high-altitude orientation tier (§4A).
- **Authoring cost.** Models are hand-authored (no generator). The boundary allow-list (B2) is
  *additional* hand-authoring on top. Budget human/agent time; do not assume free.
- **ZFC boundary leakage.** The mechanical checks (link resolution, graph edge comparison) are
  ZFC-clean; the "is this under-modeled / is this good design" judgment must stay with
  model-agents. Risk = someone encodes a regex/keyword heuristic for the semantic class. Guard
  against it in review (the `architecture-refresh` skill already states the boundary).
- **Untracked-model trap.** ~half the repos keep `architecture/` out of git. Any rollout that
  silently skips them creates uneven coverage and a false sense of city-wide governance.
- **Stale-model-harms failure mode (named explicitly).** A gate built on a drifted model is
  worse than no gate: it manufactures false blocks/passes and trains agents and humans to ignore
  architecture signal — poisoning the well for the whole initiative.

---

## Appendix — evidence index

- Inventory: `find /home/ds/projects /home/ds/gascity-packs -maxdepth 2 -type d -name architecture` → 100 dirs; deduped to ~21 distinct projects.
- Models read: `/home/ds/gascity-packs/architecture/{model,spec}.c4`, `/home/ds/projects/mem/architecture/{model,spec}.c4`.
- Tracked status: `git ls-files architecture/` per repo (tracked: `mem`/`tom-swe`/`code-intelligence-digest`/`website`/`embertide`; untracked: `gascity-packs`/`agent-diagnostics`/`brains`/`codeprobe`/`background-agents`/`EnterpriseBench`/`GEO`/`mcp-ax`/`databot-agent`/`migration-evals`).
- Drift evidence: `git log -1 -- architecture/` vs `git log --since=<arch-date>` → `mem` 15 / `tom-swe` 27 code commits since last model touch.
- Tooling: `/home/ds/projects/mem/.github/workflows/likec4-pages.yml`; `/home/ds/projects/mem/architecture/site/check-links.mjs`; live `npx likec4@latest export json` → 58 elements / 40 relations, JSON shape verified.
- Skill: `~/.claude/skills/architecture-refresh/SKILL.md` (audit §A, fix §B, validate §C, deploy §D, CI-propagate §E).
