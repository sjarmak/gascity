---
name: gc-meow-work-model
description: >-
  The Gas City MEOW work model: beads, formulas, molecules, wisps, orders,
  convoys, and sling dispatch. Load when creating, routing, or debugging
  work — a slung bead sits unclaimed, a wisp never gets picked up, an order
  fires twice or never, a convoy won't close — or before changing code under
  cmd/gc/cmd_sling.go, cmd/gc/order_dispatch.go, cmd/gc/wisp_gc.go,
  cmd/gc/cmd_convoy*.go, internal/sling/, internal/formula/,
  internal/molecule/, or internal/orders/. Also load when deciding exec
  order vs formula order, when writing a formula or order TOML file, or
  when the docs mention MolCook (removed 2026-03-19 — this skill has the
  current instantiation path). Do NOT load for controller tick / reconciler
  internals (gc-reconciler-lifecycle), first-hour codebase orientation
  (gc-orientation), or test-kind selection (gc-test-authoring).
---

# gc-meow-work-model — how work is defined, instantiated, routed, discovered, and reaped

MEOW ("Molecular Expression of Work") is the load-bearing domain layer of
Gas City: **work is the primitive, not orchestration** (AGENTS.md). This
runbook teaches the full life of a unit of work — definition (formula) →
instance (molecule/wisp) → routing (sling / order dispatch) → discovery
(work query / hook) → completion (close) → reaping (wisp GC) — with the
composition and ordering invariants that the glossary does not cover.

Tier 1 skill: single-session reference runbook, no subagents, safe under
`DISABLE_INTERACTIVITY=1`.

All file:line references and command flags verified against the working
tree on **2026-07-06**. Where the engdocs lag the code, this skill says so
explicitly and cites the commit that changed the behavior.

## When NOT to use this skill

| You are asking about                                                           | Use instead                                                               |
| ------------------------------------------------------------------------------ | ------------------------------------------------------------------------- |
| Controller tick anatomy, poke debounce, wake/nudge races, drain, orphan sweeps | `gc-reconciler-lifecycle`                                                 |
| What a bead/rig/pack _is_, package map, reading route                          | `gc-orientation`                                                          |
| ZFC / Bitter Lesson / zero-roles doctrine deep-dive                            | `gc-doctrine` (if present) — else AGENTS.md directly                      |
| Which test kind to write, fakes, conformance suites                            | `gc-test-authoring`                                                       |
| Release gates, CI tiers                                                        | `gc-release-ci-ops`                                                       |
| Dolt server lifecycle behind the bead store                                    | `gc-dolt-ops` (if present) — else `engdocs` + `internal/beads/bdstore.go` |

Authoritative noun definitions live in `engdocs/architecture/glossary.md`
and the one-phrase decoder in `gc-orientation`. This skill restates only
what it needs and owns the _composition_ facts.

## Working definitions (one line each)

- **Bead** — one unit of work in the task store. Everything is a bead:
  tasks, mail, molecules, convoys, epics (`internal/beads/beads.go:20`).
- **Formula** — a TOML workflow template on disk (`formulas/<name>.toml`).
- **Molecule** — a formula instantiated as a root bead + child step beads.
- **Wisp** — an ephemeral molecule; eligible for TTL garbage collection.
- **Order** — a formula or shell command the controller dispatches when a
  trigger condition fires (`orders/<name>.toml`).
- **Convoy** — a container bead grouping related beads for batch tracking;
  the only container type expanded during dispatch
  (`internal/beads/beads.go:62-64`).
- **Epic** — an ordinary tracking bead. NOT a container; never expanded.
- **Sling** — the dispatch composition: resolve target → (optionally)
  instantiate formula → stamp routing metadata → auto-convoy → poke
  controller → (optionally) nudge.
- **Nudge** — text typed into an agent's live session to wake it.
- **Work query / hook** — the shell command an agent (or the controller)
  runs to discover its next bead; `gc hook` is the CLI wrapper.
- **graph.v2** — the newer formula contract for DAG workflows; detected by
  `gc.kind = "workflow"` metadata on the compiled root step
  (`internal/molecule/molecule.go:389`) and stamped as
  `gc.formula_contract = "graph.v2"` on the root bead
  (`internal/sling/sling.go:1009`).

## The 2026-03-19 pivot: MolCook is gone — read this before trusting any doc

Commit `98ca7b172` ("Port formula compilation engine from beads; remove
MolCook from Store", 2026-03-19) moved formula execution **in-process**:

- `internal/formula/` compiles a `*.toml` formula file into a `Recipe`
  (`formula.Compile*`, `internal/formula/compile.go`).
- `internal/molecule/` instantiates a compiled Recipe as beads through the
  plain `beads.Store` CRUD interface. Entry points: `Cook` (compile +
  instantiate), `Instantiate` (pre-compiled recipe), `CookOn` / `Attach`
  (attach a sub-DAG under an existing bead)
  (`internal/molecule/molecule.go:125,142,216,363`).
- `beads.Store` has **no** `MolCook` / `MolCookOn` methods anymore.

Stale as of 2026-07-06: `engdocs/architecture/glossary.md`,
`formulas.md`, `dispatch.md`, `life-of-a-molecule.md`, and
`nine-concepts.md` all still describe `Store.MolCook` delegating to
`bd mol wisp` / `bd mol bond`. When those docs and this skill disagree on
the instantiation path, run the provenance check at the bottom; the code
wins.

## Formula → molecule mechanics

**File naming.** Canonical: `formulas/<name>.toml`. Legacy (still
recognized): `formulas/<name>.formula.toml`
(`internal/formula/filenames.go:5-15`). Same pattern for orders:
canonical `orders/<name>.toml`; deprecated `orders/<name>.order.toml`
and `orders/<name>/order.toml` still load with a warning
(`internal/orders/discovery.go`, `internal/orders/scanner.go:13`).

**Layer resolution.** `ComputeFormulaLayers()` (`internal/config/pack.go`)
orders formula directories from packs, city config, and rig config;
`ResolveFormulas()` (`cmd/gc/formula_resolve.go`) keeps the
highest-priority winner per filename and stages winners as symlinks into
`.beads/formulas/`. Last-wins by filename; real files are never
overwritten. System formulas embedded in the `gc` binary materialize to
`.gc/system-formulas/` at startup as the lowest layer
(`cmd/gc/system_formulas.go`); the shipped core-pack formulas live at
`internal/bootstrap/packs/core/formulas/` (mol-do-work, mol-polecat-base,
mol-polecat-commit, mol-review-quorum, mol-scoped-work).

**Two contracts.** Legacy `[[steps]]` formulas produce a flat molecule
(root + step beads with `needs` dependencies) — format reference:
`docs/reference/formula.md`. graph.v2 formulas compile to DAG workflows
with lanes, retries, and control beads; slinging one routes through the
graph-workflow launch path (`internal/sling/sling_graph.go`) instead of
plain bead routing.

Copy-paste inspection commands (all read-only):

```bash
gc formula list                 # active formulas after layer resolution
gc formula show <formula-name>  # resolved definition + winning layer
gc formula cook <name> --var issue=<bead-id>          # instantiate, no routing
gc formula cook <name> --attach <bead-id>             # late-bound sub-DAG under a bead
```

`gc formula cook --attach` gives the attached bead a blocking dependency
on the sub-DAG root, so it cannot close until the sub-DAG completes
(`cmd/gc/cmd_formula.go:248-262`). This is the core primitive for runtime
DAG expansion.

## Routing: `gc sling`

```
gc sling [target] <bead-or-formula-or-text> [flags]
```

The second argument is one of three things (`cmd/gc/cmd_sling.go:69-88`):
an existing bead ID, a formula name (only with `--formula`), or arbitrary
text (auto-creates a task bead; requires an explicit target).

Flag table, verified against `cmd/gc/cmd_sling.go:120-139`:

| Flag                                     | Meaning                                                                   | Trap                                                                      |
| ---------------------------------------- | ------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| `-f, --formula`                          | **BOOL** — treat the argument as a formula name                           | `--formula <name>` is wrong; write `gc sling <target> <name> --formula`   |
| `--on <formula>`                         | attach a wisp from `<formula>` to the bead before routing                 | mutually exclusive with `--formula` and `--no-formula`                    |
| `--no-formula`                           | suppress the agent's `default_sling_formula`, route the raw bead          |                                                                           |
| `--nudge`                                | nudge the target after routing                                            | without it a warm worker stays idle until its next poll                   |
| `--force`                                | suppress warnings, allow cross-rig routing and graph-workflow replacement |                                                                           |
| `-t, --title`                            | wisp root title (with `--formula` / `--on`)                               |                                                                           |
| `--var k=v`                              | formula variable (repeatable)                                             | unresolved required vars fail instantiation                               |
| `--merge direct\|mr\|local`              | stamp `merge_strategy` metadata                                           | only those three values                                                   |
| `--no-convoy` / `--owned`                | suppress auto-convoy / mark it `owned` (skip auto-close)                  | mutually exclusive with each other                                        |
| `-n, --dry-run`                          | show what would be done                                                   | exists since the sling package split; `dispatch.md` "no dry-run" is stale |
| `--stdin`                                | read bead text from stdin (first line = title)                            | requires exactly one arg (target)                                         |
| `--scope-kind city\|rig` + `--scope-ref` | logical scope for graph.v2 launches                                       | must be provided together                                                 |

**What routing actually is.** Unless the agent overrides `sling_query`,
routing is a metadata stamp, nothing more:

```
bd update {} --set-metadata gc.routed_to=<qualified-agent-name>
```

(`internal/config/config.go:2126-2128`, `DefaultSlingQuery`). It does NOT
set the assignee, does NOT create a session, does NOT start work. The
reconciler and pool scale checks handle session creation; discovery
happens on the consumer side (next section). After routing, sling pokes
the controller (`internal/sling/sling_core.go`, `finalize` →
`PokeController`) so the next tick sees the new demand — tick semantics
belong to `gc-reconciler-lifecycle`.

**Formula selection order** inside sling (`internal/sling/sling_core.go:43-70`):
`--formula` (new wisp) > `--on <f>` (attach to bead) > agent's
`default_sling_formula` (per-agent config, inheritable from
`[agent_defaults]`; `internal/config/config.go:2132-2140`) unless
`--no-formula` > plain bead route.

**Auto-convoy.** Slinging a plain single bead wraps it in a new convoy
bead (`sling-<bead-id>`) for batch tracking. Suppressed by `--no-convoy`,
by `--formula`, and when the bead is itself a container
(`internal/sling/sling_core.go:368-410`).

**Container expansion.** Slinging a convoy routes each **open** child
individually; non-open children are skipped and reported; the container
itself is the convoy. Only type `convoy` expands
(`internal/beads/beads.go:60-70`) — slinging an epic routes the epic bead
itself.

**Pre-flight warnings never block.** "already assigned" warnings
(`internal/sling/sling_attachment.go:336`) are advisory; routing proceeds.
That leniency is why the unclaimed-bead failure mode below exists.

## Discovery: how routed work gets picked up

The default work query (`internal/config/config.go:2026-2098`,
`EffectiveWorkQuery`) is a three-tier shell pipeline over `bd`:

1. **Crash recovery** — `in_progress` beads assigned to any of my
   identifiers (`$GC_SESSION_ID` > `$GC_SESSION_NAME` > `$GC_ALIAS`).
2. **Pre-assigned** — `ready` beads assigned to any of my identifiers.
3. **Routed queue** — `bd ready --metadata-field gc.routed_to=<target>
--unassigned` — only evaluated when `$GC_SESSION_ORIGIN` is
   `ephemeral` or empty (controller probes), so long-lived named sessions
   do not steal generic config demand.

Consequences you must design around:

- **Tier 3 requires `--unassigned`.** A bead with a stale assignee that
  you route to a config/pool will never match tier 3. Clear it first:
  `bd update <bead-id> --assignee "" --status open` (the reset idiom the
  SDK itself uses, `internal/config/config.go:2281`).
- `bd ready` means: status open, all blocking dependencies closed, and
  the type is not in the ready-exclusion list — `merge-request`, `gate`,
  `molecule`, `message`, `session`, `agent`, `role`, `rig` are
  bookkeeping, never actionable work (`internal/beads/beads.go:84-97`).
  Wisp roots that are themselves executable carry type `wisp` so they DO
  surface as ready work.
- Agent-side commands: `gc hook [agent]` runs the work query ("check for
  available work"); `gc prime [agent-name]` prints the behavioral prompt
  (`cmd/gc/cmd_hook.go:22`, `cmd/gc/cmd_prime.go:65`). GUPP (AGENTS.md):
  if the hook has work, the agent runs it — no confirmation step. That
  rule lives in prompts, never in Go.

## Orders: scheduled and reactive dispatch

An order is a TOML file `orders/<name>.toml` under a formula layer, with
an `[order]` header (`internal/orders/order.go`):

| Field                                    | Meaning                                                                                                                  |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| `formula`                                | formula to instantiate as a wisp when due (XOR with `exec`)                                                              |
| `exec`                                   | shell command run directly by the controller — no LLM, no agent, no wisp (XOR with `formula`; `pool` forbidden)          |
| `trigger`                                | `cooldown` \| `cron` \| `condition` \| `event` \| `manual` (`internal/orders/triggers.go:52-66`)                         |
| `interval` / `schedule` / `check` / `on` | the trigger's required parameter (cooldown/cron/condition/event respectively; enforced by `Validate`)                    |
| `pool`                                   | target agent/pool for the wisp root (formula orders only)                                                                |
| `timeout`                                | per-run timeout; defaults 300s exec / 30s formula (`order.go:116-127`; the field comment saying "60s for exec" is stale) |
| `enabled`                                | defaults true; disabled orders vanish from scan results                                                                  |

Dispatch mechanics you must not re-learn the hard way:

- **Tracking bead first, synchronously.** Each firing creates a bead
  labeled `order-run:<scopedName>` _before_ the dispatch goroutine runs
  (`cmd/gc/order_dispatch.go:25,324`) — that is what stops a cooldown
  trigger from re-firing on the next tick.
- **Formula orders compile in-process** (`formula.Compile*` +
  `molecule.Instantiate`) and stamp the wisp root with
  `gc.routed_to=<qualified pool>` metadata plus the
  `order-run:<scopedName>` label (`cmd/gc/order_dispatch.go:605-738`).
  The `orders.md` description of label-based `pool:` routing is stale.
- **ScopedName isolates rigs**: `dolt-health` vs
  `dolt-health:rig:demo-repo` track cooldowns and event cursors
  independently (`internal/orders/order.go:55-62`).
- **Event triggers dedupe by cursor**: `seq:<N>` labels on order-run
  beads; runs fail closed if the cursor cannot be read.
- **Manual orders never auto-fire** — dispatchers filter them out; only
  `gc order run <name>` triggers them.

```bash
gc order list                # discovered orders after layer override
gc order show <name>         # resolved definition
gc order check               # due / not-due reason for every order
gc order history [name]      # recent firings
gc order run <name>          # manual fire
gc order sweep-tracking      # prune order tracking beads
```

### Ordering invariant: orders dispatch before session reconcile

Each controller tick runs order dispatch **before** the expensive session
reconcile phases, "so due formulas are not starved by slow startup/config
drift work" (`cmd/gc/city_runtime.go:733-738`; ordering comment introduced
in commit `f9c9cc907`, 2026-04-27). Do not move order dispatch after
reconcile "for consistency" — starving due orders behind a wedged
reconcile is a regression the codebase already paid for. Tick internals:
`gc-reconciler-lifecycle`.

## Convoys

```bash
gc convoy create <name> [issue-ids...]   # explicit batch container
gc convoy list | status <id> | add <convoy-id> <issue-id> | close <id>
gc convoy check                          # auto-close convoys whose children are all closed
gc convoy stranded                       # convoys with no live owner
gc convoy land <convoy-id>               # landing flow (see cmd_convoy.go:993)
```

`gc convoy check` closes any open convoy whose children are all `closed`
and records a `ConvoyClosed` event (`cmd/gc/cmd_convoy.go:791-815`).
Auto-convoys labeled `owned` (from `gc sling --owned`) are skipped by
auto-close — use `--owned` when you want to close the batch yourself.

## Wisp TTL and garbage collection

Configured in `city.toml`; **disabled unless BOTH keys are set**
(`cmd/gc/wisp_gc.go:33-42`, `internal/config/config.go:1420-1426`):

```toml
[daemon]
wisp_gc_interval = "5m"   # how often GC runs
wisp_ttl = "24h"          # how long a closed molecule survives
```

What gets purged (`cmd/gc/wisp_gc.go:48-99`): only **closed** beads past
TTL, from three populations — closed roots of type `molecule`, closed
beads with metadata `gc.kind = "wisp"`, and closed `order-tracking`
labeled beads. Open work is never touched; per-bead delete failures are
best-effort and reported without aborting the sweep. If `.beads/` is
filling with thousands of closed wisps, the usual cause is a city that
never set these two keys.

## Worked example: exec order vs formula order (real repo code)

`examples/bd/dolt/orders/mol-dog-backup.toml:1-10` (path per `origin/main`,
2026-07-06; checkouts from before mid-2026 carry the pack at
`examples/dolt/` — see the ground-truth note in sibling `gc-dolt-ops`)
documents an in-repo
conversion in its own header comment: the Dolt backup order was
"Converted from formula+pool to exec. All backup operations are
deterministic: dolt backup sync per DB, rsync backup artifacts to offsite
path. No LLM judgment needed — runs inline in the controller."

```toml
[order]
description = "Sync Dolt backups to configured remotes"
exec = "$PACK_DIR/assets/scripts/mol-dog-backup.sh"
trigger = "cooldown"
interval = "6h"
timeout = "1800s"
```

The decision rule this encodes (and which ZFC in AGENTS.md demands):

- **Deterministic transport work** (sync, rsync, health probe, cleanup) →
  `exec` order. No agent, no wisp, no tokens; default timeout 300s, so
  size `timeout` to the real worst case as this file does (1800s for ten
  120s DB syncs plus a 300s rsync).
- **Judgment work** (triage, review, anything a model should decide) →
  `formula` order + `pool`. The controller instantiates the wisp and
  stamps `gc.routed_to`; an agent discovers it via tier 3 of the work
  query.

Compare the neighbors `examples/bd/dolt/orders/dolt-health.toml` (exec,
cooldown 30s) and `examples/bd/dolt/orders/mol-dog-stale-db.toml`
(formula `mol-dog-stale-db`, `pool = "dog"`, cron every 4h): same
trigger machinery, opposite sides of the judgment line.

## Failure-mode checklist (work sits, fires twice, or piles up)

- [ ] **Slung bead never picked up** — does it have an assignee? Tier 3
      needs `--unassigned`: `bd show <id>`, then
      `bd update <id> --assignee "" --status open`.
- [ ] **Warm worker idle after sling** — did you pass `--nudge`? Routing
      alone only stamps metadata and pokes the controller.
- [ ] **"requires 1 or 2 arguments" from sling** — you wrote
      `--formula <name>`; `--formula` is a bool.
- [ ] **Expected batch fan-out didn't happen** — target was an epic, not
      a convoy; only `convoy` is a container type.
- [ ] **Order fired twice / never** — `gc order check` shows the due/
      not-due reason; check for a failed tracking-bead create (fires
      again) or a lingering `order-run:<scoped>` bead inside the cooldown
      window (never fires). Manual-trigger orders never auto-fire.
- [ ] **Molecule/message beads "missing" from `bd ready`** — they are in
      the ready-exclusion list by design; query them with `bd list`.
- [ ] **Closed wisps accumulating** — `[daemon]` `wisp_gc_interval` +
      `wisp_ttl` both set?
- [ ] **Docs said MolCook** — doc drift; the path is
      `internal/formula` + `internal/molecule` since `98ca7b172`.

## Fences and provisional notes

- **Provisional (maintainer answers pending, positions taken 2026-07-07):**
  this skill library is fork-local, written repo-portable; no upstream
  placement decision has been made.
- **Provisional — parked, not dead:** the reverted orders-v2/formula-v2
  rewrite generation and related dead limbs are parked pending a
  maintainer decision. Do not re-land or resurrect them from old branches
  without explicit maintainer sign-off. The `graph.v2` formula contract
  described here is live in-tree code, distinct from those reverts.
- Changes to formula format, order schema, wisp semantics, or the sling
  CLI surface are cross-subsystem contracts: route them to human review;
  do not let automation merge them (see `gc-change-workflow` if present,
  else AGENTS.md review norms). Nothing in this skill authorizes routing
  around change control.

## Provenance and maintenance

Sources: working tree at gastownhall/gascity fork (checkout `58e0b8dbb`,
branch `_pr1945_check`; worked-example order paths re-verified against
`origin/main` at `f828bbe4b`), 2026-07-06 —
`AGENTS.md`, `engdocs/architecture/{glossary,nine-concepts,formulas,dispatch,orders,life-of-a-molecule}.md`
(with drift noted above), and direct reads of the files cited inline.
Authored as part of the retiring-fellow departure library (discovery
report and provisional maintainer answers live in the ds-research
workspace, not this repo; they are context, not load-bearing sources).

Re-verify volatile facts before trusting this skill after a large merge:

```bash
# MolCook still absent from the store seam (expect no matches):
grep -rn "MolCook" internal/beads/ --include="*.go" | grep -v _test
# Order dispatch still precedes reconcile (expect the comment near line 733):
grep -n "Order dispatch is intentionally before" cmd/gc/city_runtime.go
# Default routing is still a metadata stamp:
grep -n "gc.routed_to" internal/config/config.go | grep "bd update"
# Sling flag surface:
grep -n "cmd.Flags()" cmd/gc/cmd_sling.go
# Trigger set:
grep -n 'case "' internal/orders/triggers.go | head -6
# Wisp GC eligibility populations:
grep -n "gc.kind\|order-tracking\|Type: \"molecule\"" cmd/gc/wisp_gc.go
# Ready-exclusion list:
sed -n '84,97p' internal/beads/beads.go
# Canonical vs legacy formula filenames:
sed -n '1,16p' internal/formula/filenames.go
```

If any check fails, fix this file in the same change that re-verifies it;
a wrong runbook is worse than none.
