---
name: gc-orientation
description: >-
  First-session orientation to the Gas City codebase (gastownhall/gascity).
  Load when you are new to this repo, resuming after a long gap, or before
  your first code change here — when you need the nine concepts, the noun
  decoder (bead, molecule, formula, wisp, order, convoy, sling, rig, pack,
  city), the package map, the three seam interfaces, or the fastest reading
  route through the code. Also load when you are hunting for a feature flag
  (there are none — config presence activates capabilities) or wondering
  where a role like "mayor" is implemented in Go (it isn't — zero hardcoded
  roles). Do NOT load for build/test mechanics (gc-build-verify), doctrine
  deep-dives (gc-doctrine), or reconciler debugging (gc-reconciler-lifecycle).
---

# gc-orientation — your first hour in the Gas City codebase

Purpose: get a zero-context engineer or agent from "cloned the repo" to
"knows where things live, what the nouns mean, and which document is
authoritative for what" in one session. This skill is a curated route, not
a reference — every fact below has exactly one authoritative home in the
repo, and this skill points there instead of restating it.

Tier: 1 — single-session, read-only, no subagents, no worktrees.
(Provisional: tier convention per the maintainer's fleet notes, 2026-07-06.)

## When NOT to use this skill

| You need                                              | Use instead                                |
| ----------------------------------------------------- | ------------------------------------------ |
| Build/test commands, CI traps, `make test` env quirks | `gc-build-verify`                          |
| ZFC / Bitter Lesson / zero-roles violation patterns   | `gc-doctrine`                              |
| Bead/molecule/formula dispatch semantics in depth     | `gc-meow-work-model`                       |
| Controller tick / reconciler race debugging           | `gc-reconciler-lifecycle`                  |
| Writing tests (five kinds, fakes, conformance)        | `gc-test-authoring`                        |
| Reviewing a PR in this repo                           | `review-pr` (already in `.claude/skills/`) |

The `gc-*` siblings are part of the same departure library, landing
2026-07-06/07 — check `ls .claude/skills/` for what exists yet. If a
sibling is missing, its facts live in the repo docs cited below.

## The 60-second model

Gas City is an **orchestration-builder SDK** in Go: a toolkit for composing
multi-agent coding workflows where **all role behavior is user-supplied
configuration** and the Go code contains **zero hardcoded roles**. Work —
not orchestration — is the primitive. The load-bearing layer is the MEOW
stack (Molecular Expression of Work: beads → molecules → formulas); the
orchestration shape (Gas Town's mayor/polecat roles, or anything else) is
one configuration among many.

The constitution is **`AGENTS.md`** at the repo root (`CLAUDE.md` is just
`@AGENTS.md`). Nothing you write may contradict it. It owns: the nine
concepts summary, layering invariants, active migrations, settled
decisions, design principles (ZFC, Bitter Lesson, GUPP, NDI, no status
files, SDK self-sufficiency), code conventions, and quality gates.

## Noun decoder

One-phrase glosses so you can read the code and docs. The authoritative
definitions live in `engdocs/architecture/glossary.md` — if this table and
the glossary ever disagree, the glossary wins (its own header says so).

| Noun       | One-phrase gloss                                                                                                                                          |
| ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| City       | A directory on disk: `city.toml` + `.gc/` runtime state + rigs                                                                                            |
| Rig        | A project (repo) managed inside a city                                                                                                                    |
| Pack       | A distributable bundle of config/prompts/formulas (`pack.toml`)                                                                                           |
| Bead       | A single unit of work; _everything_ is a bead (tasks, mail, molecules, convoys, epics)                                                                    |
| Molecule   | A root bead + child step beads: a runtime bead tree for multi-step work                                                                                   |
| Formula    | A TOML template that instantiates molecules                                                                                                               |
| Wisp       | An ephemeral molecule (TTL + garbage collection)                                                                                                          |
| Order      | A formula or shell script dispatched on a trigger condition (`orders/<name>.toml`; the legacy `orders/<name>/order.toml` form still loads with a warning) |
| Convoy     | A container bead grouping related work as a tracked batch                                                                                                 |
| Sling      | The dispatch composition: find/spawn agent → select formula → create molecule → hook → nudge → convoy → event                                             |
| Nudge      | Text delivered into an agent's live session to wake it                                                                                                    |
| Session    | A running agent process under a runtime provider (tmux, subprocess, exec, k8s, fake)                                                                      |
| Controller | The per-city daemon loop that drives all SDK infrastructure                                                                                               |
| Reconciler | The controller phase that drives sessions toward computed desired state                                                                                   |
| MEOW       | Molecular Expression of Work — the beads/molecules/formulas stack                                                                                         |

Jargon from the principles (details in AGENTS.md → deep treatment in
`gc-doctrine`): **ZFC** = Zero Framework Cognition (Go moves bytes, models
decide; a judgment call in Go is a bug). **Bitter Lesson test** = every
primitive must get MORE useful as models improve. **GUPP** = "if you find
work on your hook, YOU RUN IT." **NDI** = nondeterministic idempotence
(sessions are disposable; beads/hooks/molecules are durable; convergence
via idempotent re-checking).

## The fastest reading route

Read in this order. Total: roughly one focused hour. Check each box.

- [ ] **1. `AGENTS.md`** (repo root) — the constitution. Pay special
      attention to "Active migrations" (routes that are mid-move; taking the
      legacy route fails CI) and "Design decisions (settled)" (do not relitigate).
- [ ] **2. `engdocs/architecture/nine-concepts.md`** — five primitives +
      four derived mechanisms, with the derivation proof for each mechanism
      and the Level 0-8 capability table. This is the mental model everything
      else hangs off.
- [ ] **3. `engdocs/architecture/glossary.md`** — skim now, return often.
      Authoritative for every term.
- [ ] **4. `engdocs/contributors/codebase-map.md`** — key packages and the
      common change paths (CLI change, runtime change, config change, docs
      change).
- [ ] **5. The three seam interfaces** (read the interface docs, not the
      implementations — line numbers verified against `origin/main` at
      `f828bbe4b`, 2026-07-06; the highest-churn anchors in this skill, so
      re-grep before trusting them):
  - `internal/runtime/runtime.go:119` — `runtime.Provider`: the lowest
    session-runtime seam (Start/Stop/Interrupt/IsRunning/Nudge/...).
    Transport only. Implementations: `tmux/` (production), `subprocess/`,
    `exec/`, `k8s/`, `ssh/`, `t3bridge/`, `herdr/`, `fake.go` (test), plus
    `acp/`/`auto/`/`hybrid/` routing layers — all under
    `internal/runtime/`.
  - `internal/beads/beads.go:337` — `beads.Store`: the work-store seam.
    Production `BdStore` (`internal/beads/bdstore.go:294`) shells out to
    the external `bd` CLI backed by a Dolt SQL server; `FileStore`,
    `MemStore`, and an exec store also implement it.
  - `internal/events/events.go:285` — `events.Provider` (writer
    sub-interface `Recorder` at `:277`): append-only JSONL event log at
    `.gc/events.jsonl`, the universal observation substrate.
- [ ] **6. `cmd/gc/main.go`** — CLI wiring (cobra). The CLI is a
      _projection_ over the object model in `internal/`, never a place for
      domain logic.
- [ ] **7. `cmd/gc/city_runtime.go`** — the controller loop (see worked
      example below). Skim `run()` at `:363` and the event `select` loop at
      `:726-770`.

After the route, when you touch `internal/api/`, events, or anything that
regenerates OpenAPI/dashboard types, AGENTS.md directs you to
`engdocs/architecture/api-control-plane.md` and
`engdocs/contributors/huma-usage.md` first. Sibling skill:
`gc-generated-artifacts`.

## Package map (top level)

Counts and layout from `origin/main` at `f828bbe4b`, 2026-07-06.

| Path                                  | What lives there                                                                                                                                                                                                                                                                                                             |
| ------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cmd/gc/`                             | CLI + controller daemon — ~95% of the binary surface (700 `.go` files, 401 of them tests)                                                                                                                                                                                                                                    |
| `cmd/{genschema,genspec,gen-client}/` | Codegen tools for schema/spec/client                                                                                                                                                                                                                                                                                         |
| `internal/`                           | The canonical object model (~89 packages). Start with: `runtime/`, `beads/`, `events/`, `config/`, `session/`, `worker/`, `sling/`, `formula/`, `molecule/`, `orders/`, `mail/`, `convoy/`, `api/`                                                                                                                           |
| `engdocs/`                            | Contributor + architecture docs (`architecture/`, `contributors/`, `design/`)                                                                                                                                                                                                                                                |
| `docs/`                               | User-facing Mintlify docs + generated reference                                                                                                                                                                                                                                                                              |
| `examples/`                           | Runnable reference cities: `gastown/`, `swarm/`, `hyperscale/`, `lifecycle/`, `bd/` (dolt pack at `bd/dolt/`), `t3bridge-gastown/`. Fork-only parked packs on older checkouts (`oversight-rig/`, `slack-pack/`, top-level `dolt/`) are NOT on main — treat them as parked limbs (see `gc-doctrine` §10), not landed examples |
| `test/`                               | Tag-gated integration tests needing real infrastructure                                                                                                                                                                                                                                                                      |
| `release-gates/`                      | Pinned per-blocker release gate contracts                                                                                                                                                                                                                                                                                    |
| `.beads/`                             | The live issue tracker (`bd` CLI; run `bd prime` for workflow)                                                                                                                                                                                                                                                               |

Everything is `internal/` by design — SDK exports (`pkg/`) are future work
(settled decision, AGENTS.md).

## Two traps this skill exists to prevent

**Trap 1 — hunting for feature flags.** There are none. Config _presence_
is the activation mechanism: an empty `city.toml` gives Level 0-1; adding
`[daemon]` activates the controller loop; adding formula files +
`[formulas]` activates molecules; and so on up to Level 8. The full table
is in `engdocs/architecture/nine-concepts.md` ("Progressive Capability
Model"). If you find yourself grepping for `enable_`, stop and check
which config section gates the behavior instead.

**Trap 2 — looking for the role implementation.** `grep -ri mayor
internal/` finds no role logic, and that is the point: **if a line of Go
references a specific role name, it's a bug** (AGENTS.md, first
paragraph). Roles exist only in prompt templates and config. Before
writing any `if role == ...` or any judgment call in Go, load
`gc-doctrine`.

## First-session verification checklist

Copy-paste, in order, from the repo root:

```bash
make setup          # installs tools, activates .githooks pre-commit
make build          # builds bin/gc
./bin/gc --help     # ~50 subcommands; sanity check the build
make check          # fmt-check + lint + vet + check-routed-test-rows + fast unit tests
```

Two warnings before you trust that green `make check`:

- `make check` runs the _fast_ unit tier only. "Tests pass" locally is
  not "CI is green" — the full tier map, env-scrubbing behavior of
  `make test`, and build-tag-gated suites are `gc-build-verify`'s
  territory (and `TESTING.md`'s). Read one of them before your first PR.
- Never run bare `tmux kill-server` on this machine — test and city
  sessions share tmux servers with humans (AGENTS.md "Tmux safety").

## Worked example: trace a sling wake through the controller

Question a newcomer actually hits: _"I slung work at an idle agent — what
makes the controller act on it, and why is a dropped poke not a bug?"_
Answer by walking the code (line numbers from `origin/main` at `f828bbe4b`,
2026-07-06 — this is the repo's highest-churn file, so re-grep first):

1. The dispatch path calls `controllerState.Poke()` —
   `cmd/gc/api_state.go:1816-1824`. It does a **non-blocking send** on the
   poke channel: `select { case cs.pokeCh <- struct{}{}: default: }` with
   the comment `// poke already pending`. Extra pokes are _dropped by
   design_ — this is debounce, not loss.
2. The channel is buffered size 1: `cmd/gc/city_runtime.go:323`
   (`make(chan struct{}, 1)`), declared at `:117` as "non-blocking signal
   to trigger immediate reconciler tick".
3. The controller's single event `select` loop
   (`cmd/gc/city_runtime.go:726-770`) receives it: `case <-cr.pokeCh:`
   arms a tick debouncer whose deferred fire runs `runTick("poke")` — an
   immediate tick (with `debounce=0`, the default, behavior is identical
   to a direct send) instead of waiting for the next patrol-interval tick
   (`cr.cfg.Daemon.PatrolIntervalDuration()`, `:675`).
4. Inside the tick, **order dispatch runs before the expensive session
   reconcile phases** — intentional, per the comment at
   `cmd/gc/city_runtime.go:1077-1080` ("so due formulas are not starved by
   slow startup/config drift work") — then dead session beads are reaped,
   a demand snapshot is loaded, desired state is computed
   (`cmd/gc/build_desired_state.go`), and the reconciler
   (`cmd/gc/session_reconciler.go`) drives real sessions toward it.

The lesson (NDI in action): a dropped poke never loses work, because work
lives in durable beads, and the next tick — poke-driven or patrol-driven —
re-reads that durable state idempotently. Worst case without a poke is
one patrol interval of latency. If you catch yourself "fixing" the
dropped-poke behavior, you are misreading the design; load
`gc-reconciler-lifecycle` before touching this file (it has the highest
fix-commit density in the repo).

### Reading history before writing code

Two real migrations show why AGENTS.md's "Active migrations" section is a
pre-write checkpoint, not trivia:

```bash
git log --oneline -1 dd90ac0a   # session-first migration — removed agent.Agent
git log --oneline -1 12a0a848   # worker boundary — worker.Handle is canonical
```

`dd90ac0a1` (Mar 8 2026) removed the old `agent.Agent`/`agent.Handle`
primitive entirely; do not reconstruct those interfaces. `12a0a8485`
(Apr 17 2026) made `internal/worker/handle.go` the canonical boundary for
session creation from `cmd/gc/` — enforced by
`TestGCNonTestFilesStayOnWorkerBoundary`
(`cmd/gc/worker_boundary_import_test.go`). Code that takes the legacy
route fails CI, so read the migrations list before writing
session-lifecycle code.

## Provenance and maintenance

Authored 2026-07-06 by the retiring-fellow distillation campaign, from
the discovery report and provisional maintainer answers in the ds-research
workspace (machine-local, non-load-bearing: `/home/ds/gas-city/docs/design/
fable-distillation/`). Placement is **provisional: fork-local, written
repo-portable** — no upstream commitment yet. Every command, path, and
line number above was verified against `origin/main` at `f828bbe4b`
(2026-07-06). Local checkouts can be weeks behind main — if a line anchor
misses, re-run the checks below against `origin/main`
(`git show origin/main:<path> | grep -n ...`) before concluding this skill
is wrong.

Re-verification one-liners (run from repo root; if any fails, this skill
has drifted and needs the corresponding section updated):

```bash
head -c 20 CLAUDE.md                                      # expect: @AGENTS.md
grep -n "type Provider interface" internal/runtime/runtime.go   # expect :119 area
grep -n "type Store interface" internal/beads/beads.go          # expect :337 area
grep -n "type Provider interface" internal/events/events.go     # expect :285 area
grep -n "non-blocking signal to trigger" cmd/gc/city_runtime.go # pokeCh decl
grep -n "Order dispatch is intentionally before" cmd/gc/city_runtime.go
grep -n "poke already pending" cmd/gc/api_state.go
grep -nE "^(setup|build|check|check-docs):" Makefile
ls engdocs/architecture/nine-concepts.md engdocs/architecture/glossary.md \
   engdocs/contributors/codebase-map.md
ls .claude/skills/                                        # sibling availability
git log --oneline -1 dd90ac0a && git log --oneline -1 12a0a848
```

Volatile facts most likely to drift: file counts (700/401 in `cmd/gc/`),
the `gc --help` subcommand list, all `city_runtime.go` line numbers (the
repo's highest-churn file), and the sibling-skill roster.
