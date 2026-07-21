---
name: gc-reconciler-lifecycle
description: >-
  Gas City controller loop and session reconciler internals. Load before
  changing anything under cmd/gc/city_runtime.go, session_reconciler.go,
  build_desired_state.go, compute_awake_set.go, drain/undrain/drain-ack
  paths, nudge dispatch, orphan sweeps, or pool wake/scale logic. Also load
  when diagnosing: a session that never wakes, wakes late, wakes and is
  immediately re-slept, drains unexpectedly, respawns only after a 30s
  patrol tick, is starved by the wake budget, or work beads that get reset
  or double-claimed. Trigger phrases: reconciler, controller tick, poke,
  wake budget, drain-ack, orphan sweep, session bead, desired state,
  wake/nudge race.
---

# gc-reconciler-lifecycle

The reconciler is the highest fix-density subsystem in Gas City:
`cmd/gc/city_runtime.go` alone has accumulated over a hundred fix commits,
and the dominant recurring bug classes (wake/nudge latency races,
orphan-sweep over/under-reach) all live here. This skill teaches the tick
anatomy, the invariants, the canonical fix idioms, and the failure patterns
that keep recurring, each grounded in a real commit.

Tier 1 skill: pure knowledge, single-session, no subagents or worktrees
required.

## When NOT to use this skill

| You are doing                                                              | Use instead                                                                                                    |
| -------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Collecting trace artifacts for a reconciler incident                       | `engdocs/contributors/reconciler-debugging.md` (the `gc trace` workflow) plus the sibling skill `gc-debugging` |
| Changing TOML config semantics, patch/override resolution, reload plumbing | sibling skill `gc-config-system`                                                                               |
| Managed Dolt server lifecycle, `bd` shell-out, bead-store internals        | sibling skill `gc-dolt-ops`                                                                                    |
| Adding a runtime provider or changing tmux/subprocess/k8s transport        | sibling skill `gc-runtime-providers`                                                                           |
| Beads/molecules/formulas/orders semantics (what gets dispatched)           | sibling skill `gc-meow-work-model`                                                                             |
| First contact with the codebase                                            | sibling skill `gc-orientation`                                                                                 |

Sibling skills are part of the same departure library; if one is missing,
the engdocs named above are the authoritative fallback.

Doctrine (ZFC, NDI, zero hardcoded roles, no status files) is owned by
`AGENTS.md` at the repo root. Nothing below overrides it; the reconciler is
where NDI ("sessions are disposable, work is durable, every tick is
idempotent") stops being a slogan and becomes the actual design.

## Vocabulary (defined once)

| Term            | Meaning                                                                                                                                                                                                                                |
| --------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Controller      | The per-city daemon loop (`CityRuntime.run` in `cmd/gc/city_runtime.go`) that owns all reconciliation. One goroutine, one `select`.                                                                                                    |
| Tick            | One full pass of `CityRuntime.tick`: reload config if dirty, dispatch orders, compute desired state, reconcile sessions. Triggered by the patrol ticker or a poke.                                                                     |
| Patrol          | The periodic tick, `[daemon].patrol_interval` in city.toml, default `30s` (`internal/config/config.go`, `PatrolIntervalDuration`).                                                                                                     |
| Poke            | A non-blocking signal on a size-1 channel (`pokeCh`) that requests an immediate tick. CLI processes send it as the `poke` command over the controller socket (`pokeController`, `cmd/gc/cmd_sling.go`).                                |
| Nudge           | A message delivered INTO a running session ("check your hook") via `worker.Handle.Nudge()`. A poke wakes the controller; a nudge wakes an agent. Do not confuse them.                                                                  |
| Session bead    | The bead-backed record of a session's lifecycle (state: creating/active/asleep/drained/closed). Beads are durable; tmux sessions are not.                                                                                              |
| Desired state   | `DesiredStateResult` from `cmd/gc/build_desired_state.go`: which templates should exist, pool counts, assigned-work snapshot. Computed fresh from config + beads each tick.                                                            |
| Wake budget     | `[daemon].max_wakes_per_tick`, default 5 (`DefaultMaxWakesPerTick`): cap on session starts per tick.                                                                                                                                   |
| Drain           | A graceful wind-down request on a session (`gc runtime drain <name>`). The agent acknowledges with `gc runtime drain-ack`, which tells the controller to stop the session.                                                             |
| Orphan sweep    | Periodic pass that releases work beads whose assignee is dead, so NDI can re-dispatch the work. Two implementations: the core pack's `orphan-sweep.sh` script and the Go-side `releaseOrphanedPoolAssignments*` in the reconcile tick. |
| Worker boundary | `internal/worker/handle.go`, the only sanctioned surface for session lifecycle/messaging operations from production `cmd/gc` code.                                                                                                     |

## 1. Controller loop anatomy

`CityRuntime.run` (cmd/gc/city_runtime.go, around line 275) is a single
goroutine over one `select`. Verified channel set as of 2026-07-06:

```go
select {
case <-ticker.C:              // patrol tick, every patrol_interval
case <-cr.pokeCh:             // event-driven wake: sling/API assigned work
case <-cr.nudgeWakeCh:        // supervisor nudge dispatcher wake socket
case <-cr.controlDispatcherCh:// control-dispatcher-only reconcile
case req := <-cr.reloadReqCh: // structured reload from controller.sock
case req := <-cr.convergenceReqCh: // convergence CLI commands
case <-ctx.Done():
}
```

Every arm runs through `safeTick`, which recovers ALL panics, logs them
with a stack trace to stderr, and continues. This is deliberate: one
panicking tick must not cascade into `gracefulStopAll` for every session
in the city. Consequence for you: **a bug that panics does not crash the
controller; it logs "reconciler tick panicked" every tick.** Repeated
panic lines in the supervisor log are a bug report, not steady state.

### Tick ordering invariants (do not reorder)

Inside `CityRuntime.tick`, verified against the source:

1. **Config reload first** (if the dirty flag was set by the fsnotify
   watcher or a manual reload request).
2. **Order dispatch BEFORE session reconcile.** The comment in tick() is
   explicit: due formulas must not be starved by slow startup/config-drift
   work. If you add an expensive phase, it goes after `dispatchOrders`.
3. **Dead-session corpse reap before demand load** (`#742`): stale session
   bead names would otherwise block desired-state computation.
4. **Session bead sync BEFORE reconciliation, none after.** The one-tick
   state lag is intentional; the next tick corrects bead state (NDI).
5. Then `beadReconcileTick`: orphaned pool-assignment release, pool
   desired-count computation, undesired-session-bead sweep, wake/drain
   reconciliation.
6. Then wisp GC, service tick, chat auto-suspend, convergence requests
   (drained BEFORE convergence tick so user commands like stop win).

### Poke-channel debounce semantics (not a bug)

`pokeCh` is `make(chan struct{}, 1)` and every producer sends
non-blocking:

```go
select {
case cr.pokeCh <- struct{}{}:
default:
}
```

A poke that arrives while one is already queued is **dropped by design**;
the queued tick will observe all state written before it runs, because
ticks read reality fresh (beads, process table) rather than consuming
per-event payloads. Do not "fix" dropped pokes, do not add a queue, and do
not make the send blocking. If you need guaranteed delivery of a follow-up
pass, copy the existing idiom: `requestDeferredDrainFollowUpTick` /
`requestAsyncStartFollowUpTick` in city_runtime.go both do the same
non-blocking send, relying on patrol as the eventual-delivery backstop.

That two-layer pattern is the subsystem's reliability signature:
**event-driven poke for latency, patrol tick for eventual delivery.** The
nudge dispatcher startup comment in run() states it exactly: the wake
socket gives sub-second dispatch; patrol-tick fallback guarantees delivery
if the wake is missed.

## 2. Wake/sleep decisions: one function owns them

`ComputeAwakeSet` (`cmd/gc/compute_awake_set.go`) is the single decision
point for which sessions should be awake. All I/O happens before it is
called; it is a pure function over `AwakeInput` (agents, named sessions,
session beads, work beads, demand maps, running/attached/pending session
sets, clock).

**Trap, CI-visible only as silent no-ops:** `cmd/gc/session_reconcile.go`
still contains the superseded wake-reason functions. The file's own header
comment (lines ~40-51) says it plainly: they are only reachable from a
nil-guard fallback and legacy tests, and "DO NOT add new wake logic here —
it will have NO EFFECT on production behavior." If you are editing wake or
sleep behavior anywhere other than `ComputeAwakeSet` (or the input
assembly feeding it), stop and re-read this section.

Related constants you will meet: `maxIdleSleepProbesPerTick = 3`
(session_reconciler.go) bounds idle probes per tick;
`defaultOnDemandIdleTimeout = 5 * time.Minute` (compute_awake_set.go) is
the fallback idle timeout for on-demand named sessions.

### The wake budget is fairness-sensitive

Wake starts per tick are capped by `[daemon].max_wakes_per_tick` (default
5). Since commit `8ad393860` (2026-06-04), candidates within each
dependency wave are ordered least-recently-woken first using the
`last_woke_at` session-bead metadata (falling back to `CreatedAt`), so a
budget-limited tick rotates rather than starves. If you touch planned-start
ordering, preserve two properties: dependency waves stay ordered, and the
rotation signal (`last_woke_at`, written via `PreWakePatch` when a session
wins a wake) keeps getting updated. See Worked Example 2 for why.

## 3. Drain lifecycle and the poke-on-write idiom

Drain coordination is metadata on the runtime session plus session-bead
state, driven by `gc runtime` subcommands (cmd/gc/cmd_runtime.go):
`drain`, `undrain`, `drain-check`, `drain-ack`, `request-restart`. These
are designed to be called from inside agent sessions, not by humans.

Happy path: controller (or operator) signals drain → the agent notices
(drain-check or prompt convention) → agent finishes its unit of work →
agent runs `gc runtime drain-ack` → reconciler stops the session → pool
demand respawns a replacement if work remains.

**The canonical fix idiom in this subsystem:** any CLI-boundary write that
a reconciler tick must observe is followed by a best-effort poke:

```go
// after the state write succeeds
if err := pokeController(cityPath); err != nil {
    fmt.Fprintf(stderr, "...: warning: poke failed: %v\n", err)
}
// exit code and success output unaffected by poke outcome
```

`pokeController` (cmd/gc/cmd_sling.go, ~line 1429) sends `poke` over the
per-city controller socket and falls back to the supervisor socket.
Package-scoped seams (`slingPokeController`, `drainAckPokeController`,
`drainAckAsyncStopPokeController`) exist so tests can intercept the poke;
mirror that seam pattern if you add a new poke site, and never put
`t.Parallel()` on a seam-swapping test.

Corollary: **skip the poke when the write failed or when poking would
create a retry loop.** Commit `2ce4306a3` deliberately does not poke after
a hard stop error, to avoid the controller hammering an unkillable
session.

## 4. Recurring race patterns (the catalog)

Every entry below is a merged fix on main. When a symptom matches, read
the cited commit before designing anything new; the fix shape is usually
already established.

| Pattern                               | Symptom                                                                  | Exemplar commit                              | Fix shape                                                                          |
| ------------------------------------- | ------------------------------------------------------------------------ | -------------------------------------------- | ---------------------------------------------------------------------------------- |
| Missing poke after CLI-boundary write | State change only observed on next patrol tick (up to 30s latency)       | `c41b28026` (drain-ack, #2364/#2251)         | Poke after successful write; WARN on poke failure; exit code unchanged             |
| Missing poke after async completion   | Second half of a two-phase operation waits a full patrol interval        | `2ce4306a3` (post-drain respawn, #3099)      | Poke when the async work commits; skip on hard error                               |
| Budget spent without fairness         | Same sessions deferred every tick, indefinitely, under sustained demand  | `8ad393860` (#3059)                          | Rotate by persisted least-recently-woken signal within dependency waves            |
| Suppression rule missing an exemption | Session re-slept every tick (`reason_code=retained`) despite live demand | `77254dd5b` (on_demand named session, #3413) | Add the demand class to the idle-sleep suppression exemptions in `ComputeAwakeSet` |
| Wake mechanism causing churn          | Sessions repeatedly woken/nudged without net progress                    | `3bc34e0db` (idle-nudger revert, #468)       | Back the mechanism out; prefer the poke + patrol two-layer over new wake machinery |

Provisional (2026-07-06, per the morning-ledger provisional answers, not
maintainer-confirmed): wake/nudge races are treated as the primary live
fire in this subsystem, and the wake/nudge architecture redesign promised
in the `3bc34e0db` revert (follow-up bead `test-5il`) has NOT re-landed
under that name. Before any deep wake/nudge redesign work, search main for
a successor design first: `git log --oneline --grep="idle" --grep="nudge"
--all-match` and check open beads/issues. Teach toward the current idiom
(sections 1-3) until a redesign actually lands.

## 5. Orphan sweeps: over-reach and under-reach

Two independent sweepers can release or reset work:

- **Pack-level:** `internal/bootstrap/packs/core/assets/scripts/orphan-sweep.sh`
  resets in-progress beads whose assignee fails `is_known_agent`.
- **Go-level:** `releaseOrphanedPoolAssignmentsWhenSnapshotsComplete` in
  `beadReconcileTick` releases pool claims held by dead sessions. Note the
  name: it only runs when snapshots are complete, so a partial bead-store
  query cannot masquerade as "everything died."

Both encode the same hard judgment ("is this assignee alive?") and both
have failed in each direction:

- **Over-reach:** the sweep reset beads assigned to the human operator,
  because `human` is not a configured agent and never has a session
  (`93eff989d`, #3440). Silent claim-wiping, found in production.
- **Under-reach → duplicate work:** the release path "ownership" check was
  actually an exact-equality join of two store-refs derived from different
  places (where the bead lives vs where the holder reads from), so a live
  cross-store holder was treated as an orphan; its claim was reopened and a
  second worker was minted on the same bead (`478aa310f`, #3621, root
  cause behind #3453).

Rules distilled from those two fixes:

1. Any liveness/ownership predicate in a sweep needs BOTH a positive and a
   negative regression test (e.g. `human` preserved AND dead `humanoid`
   still reset).
2. When comparing identifiers, prove both sides come from the same
   namespace before using equality as a join. Store-refs, session names,
   and template names all have city/rig scoping subtleties.
3. Sweeps must degrade toward doing NOTHING when their snapshot is partial
   or ambiguous. A skipped sweep costs one patrol interval; a wrong sweep
   costs a human's claim or double token burn.

## 6. The worker boundary

Production code in `cmd/gc/` must route session lifecycle and messaging
through `worker.Handle` (`internal/worker/handle.go`: Start, Create, Stop,
Kill, Nudge, Message, Interrupt, History...). The compile-time cop is
`TestGCNonTestFilesStayOnWorkerBoundary`
(`cmd/gc/worker_boundary_import_test.go`), which forbids non-test `cmd/gc`
files from importing `session.NewManager(` and similar bypasses. Migration
status and the documented exceptions live in `AGENTS.md` under "Active
migrations"; that section is the single source of truth. If your reconciler
change needs a session operation the Handle does not expose, extend the
Handle; do not bypass it.

## 7. Debugging entry points

One home per fact: the full incident workflow is
`engdocs/contributors/reconciler-debugging.md`. Minimum recall version:

```bash
gc trace start --template <rig/template> --for 20m   # arm detail tracing
gc trace reasons --template <rig/template> --since 20m
gc trace show --template <rig/template> --since 20m --type cycle_result --json
gc trace cycle --tick <tick_id>                      # full cycle dump
gc trace stop --template <rig/template>
```

Trace records persist under `.gc/runtime/session-reconciler-trace/`.
`cycle_result` and `template_tick_summary` records answer "why did this
template (not) produce work" fastest; `reason_code=retained` plus
`open_count == desired_count` is the signature of an idle-sleep
suppression bug (see `77254dd5b`). The events spine is `.gc/events.jsonl`.
Liveness questions are answered from the process table, never from status
files (AGENTS.md doctrine).

## 8. Pre-change checklist

Before touching city_runtime.go, session_reconciler.go,
build_desired_state.go, or compute_awake_set.go:

- [ ] Wake/sleep logic change? It lives in `ComputeAwakeSet`, nowhere else
      (section 2 trap).
- [ ] New state written outside the tick that a tick must observe? Add the
      poke-on-write idiom with a test seam (section 3).
- [ ] New per-tick limit or budget? Design the fairness/rotation signal at
      the same time (section 4, row 3).
- [ ] Touching a sweep or release path? Positive AND negative regression
      tests; verify namespace compatibility of every equality join
      (section 5).
- [ ] Session operation from cmd/gc? Through `worker.Handle` only
      (section 6).
- [ ] Reordered anything in tick()? Re-check the six ordering invariants
      (section 1).
- [ ] Every fix ships its RED→GREEN regression test in the same commit;
      reconciler PRs on main consistently show tests that fail before the
      fix (see `77254dd5b`, `8ad393860`).
- [ ] `make build && make check` before push, plus the reconciler suite:
      `go test ./cmd/gc -run 'Reconcil|Awake|Drain|Wake'`. Remember
      `make test` scrubs your shell env and skips slow/tagged tiers; the
      sibling skill `gc-build-verify` owns that map.

## Worked examples from git history

### Example 1 (success path): the drain-ack latency hole, closed in two commits

**Problem.** A pool worker finishing a drain took up to a full patrol
interval (default 30s) per phase to be replaced, because both halves of
the drain-to-respawn pipeline were written for the patrol tick only.

**First half, `c41b28026` (2026-05-31, #2364, also fixed #2251).**
`doRuntimeDrainAck` wrote the drain-ack metadata and returned; the
reconciler only noticed on the next patrol tick. Fix: call
`pokeController(cityPath)` inside `doRuntimeDrainAck`, after `setDrainAck`
succeeds and before the output branch, mirroring the existing `gc sling`
wake pattern. Contract: write succeeds → poke issued; poke failure is a
grep-able stderr `warning:` line; exit code and JSON payload are
byte-identical. A package seam `drainAckPokeController` (mirroring
`slingPokeController`) makes it testable.

**Second half, `2ce4306a3` (2026-06-04, #3099).** The first tick after
drain-ack marks the session stop-pending and kills the runtime session
asynchronously, but the pool bead stayed open until a LATER patrol tick
finalized it, so the replacement still waited. Fix: poke again from
`queueDrainAckAsyncStop` in session_reconciler.go when the async stop
completes, and deliberately skip the poke on hard stop errors so an
unkillable session cannot drive a poke/retry loop.

**What to copy.** (a) Latency bugs in this subsystem are almost never
"add a watcher/queue/goroutine"; they are "find the state write that only
patrol observes, and poke after it." (b) Two-phase operations need the
idiom at BOTH phase boundaries; fixing one half moves the 30s stall, it
does not remove it. (c) Poke is always best-effort and never changes the
command's success contract.

### Example 2 (costly failure): wake-budget starvation in production

**`8ad393860` (2026-06-04, #3059).** The per-tick wake budget spent its
slots front-to-back over the stable dependency topological order, with no
memory across ticks. Two compounding design errors: (1) no fairness, so
under sustained demand the same back-of-order sessions were deferred with
`deferred_by_wake_budget` EVERY tick, indefinitely; (2) the order was a
dependency sort, so agents depending on infrastructure sorted last, which
means exactly the high-value pipeline agents lost. Observed in a
production supervisor log: with `max_wakes_per_tick=4`, six candidates
repeatedly woke control-dispatcher, two pool workers, and a docs agent
while `architect` and `reviewer` (one holding a P1) were deferred every
tick until an operator manually revived them. Lowering the budget for CPU
relief made it worse by moving the cutoff earlier.

**Fix shape.** Within each dependency wave, sort ready candidates
least-recently-woken first using persisted `last_woke_at` metadata
(fallback `CreatedAt`); winning a wake updates `last_woke_at` via
`PreWakePatch`, pushing the winner to the back next tick. Rotation, not
priority. Sorting only WITHIN a wave preserves dependency ordering.

**The lesson.** Any cap, budget, or throttle inside a convergence loop is
a starvation machine unless the selection order rotates on a signal that
the loop itself updates. NDI guarantees the system converges only if every
deferred item eventually gets a turn; "we'll get to it next tick" is false
when the ordering is stable and demand exceeds budget. When you add a
limit, add its rotation signal and a regression test in the same commit
(here: `TestExecutePlannedStarts_WakeBudgetPrioritizesLeastRecentlyWoken`,
red before, green after).

### Example 3 (costly failure): the ownership join that minted duplicate workers

**`478aa310f` (2026-06-21, #3621, root cause of #3453).** The supervisor's
orphan-release path had to decide "has this holder abandoned its work?"
Its first gate, `openSessionOwnsWork`, looked like an ownership check but
was an exact-equality join of two store-refs computed from different
worlds: the work side encoded WHERE THE BEAD LIVES (city store → `""`,
rig store → rig name), the session side encoded WHERE THE HOLDER'S AGENT
IS CONFIGURED TO READ FROM. For a live city-scoped session holding a
cross-store claim, the refs never matched, the gate returned false, and
the sweep reopened a LIVE session's in-progress claim. Demand re-counted
the reopened bead as new work and minted a second worker on the same
bead: duplicate token burn plus a double-write hazard on the output.

Pair it with the over-reach twin `93eff989d` (2026-06-28, #3440): the
pack's `orphan-sweep.sh` silently reset any in-progress bead assigned to
`human`, the canonical operator alias, because the operator is neither a
configured agent nor a session, and so failed `is_known_agent` exactly
like a dead assignee. First operator-assigned bead to go in_progress was
wiped within one sweep interval, no trace. The fix is an exact-match
guard (`human` is always known) with a negative control proving a dead
`humanoid` still resets.

**The lesson.** Reaper code sits at the sharpest edge of NDI: it converts
"I could not prove liveness/ownership" into destructive writes. Both
failure directions came from a predicate whose two sides were not actually
comparable (different namespaces; different definitions of "known"). Before
shipping any sweep change: enumerate every assignee/holder class that can
legitimately exist (configured agent, pool instance, live session, human
operator, cross-store holder), and write one test per class proving the
sweep's decision for it. The gap class you did not enumerate is the one
production finds.

## Provenance and maintenance

Authored 2026-07-06 by the retiring-fellow distillation campaign, from
direct reads of the working tree at commit `58e0b8dbb` and the merged
history on main. Provisional judgments (marked inline in section 4) stand
on the morning-ledger provisional answers of 2026-07-07, not maintainer
confirmation; revisit them when the maintainer answers discovery Q2/Q5.
Machine-local discovery sources (non-load-bearing): gas-city
docs/design/fable-distillation/discovery-gascity.md and
morning-ledger-2026-07-07.md.

Volatile facts and their re-verification one-liners (run from repo root):

| Claim                                                                           | Re-verify with                                                                                                                                       |
| ------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| select-loop channel set in run()                                                | `grep -n "case .*<-" cmd/gc/city_runtime.go \| head -20`                                                                                             |
| pokeCh is size-1, non-blocking sends                                            | `grep -n "make(chan struct{}, 1)" cmd/gc/city_runtime.go`                                                                                            |
| order dispatch before session reconcile                                         | `grep -n "Order dispatch is intentionally before" cmd/gc/city_runtime.go`                                                                            |
| ComputeAwakeSet is the sole wake authority; session_reconcile.go legacy warning | `sed -n '40,52p' cmd/gc/session_reconcile.go`                                                                                                        |
| patrol_interval default 30s; max_wakes_per_tick default 5                       | `grep -n "PatrolInterval\|DefaultMaxWakesPerTick" internal/config/config.go`                                                                         |
| gc runtime subcommand set                                                       | `grep -n "known :=" cmd/gc/cmd_runtime.go`                                                                                                           |
| pokeController behavior + fallback                                              | `grep -n -A6 "func pokeController" cmd/gc/cmd_sling.go`                                                                                              |
| worker-boundary enforcement test                                                | `grep -n "func TestGCNonTestFilesStayOnWorkerBoundary" cmd/gc/worker_boundary_import_test.go`                                                        |
| trace artifact location + workflow                                              | `sed -n '1,30p' engdocs/contributors/reconciler-debugging.md`                                                                                        |
| cited commits still on main                                                     | `for c in c41b28026 2ce4306a3 8ad393860 77254dd5b 93eff989d 478aa310f 3bc34e0db; do git merge-base --is-ancestor $c main && echo "$c on main"; done` |
| idle-nudger redesign landed? (clears the section-4 provisional)                 | `git log --oneline main --grep="idle" -i --grep="nudge" -i --all-match \| head`                                                                      |

If a re-verification fails, fix this file in the same change that moved
the code; a wrong runbook is worse than none.
