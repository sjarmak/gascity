---
name: cityops-capacity-and-scaling
description: >-
  Operate capacity in this ds-research install: suspend/resume rigs,
  grow/shrink worker pools (+2-workers flow), the reconciler wake budget and
  wake/spawn storms, disk/memory pressure guards. Load when work queues
  behind a saturated pool, a pool must scale, or the supervisor is
  CPU-pegged. Not rate-limits (compass-capacity) or wedged-supervisor
  recovery (compass-tmux-supervisor).
---

# City ops: capacity and scaling

How this specific installation is scaled up and down: which knobs exist, where
each one lives, and which ones bite. Everything below was verified on-host
2026-07-06/07; drift-prone facts are date-stamped and have a re-verification
command in the last section.

**One-command overview** (read-only, safe any time):

```bash
/home/ds/gas-city/.claude/skills/cityops-capacity-and-scaling/scripts/capacity-snapshot.sh
```

## When NOT to use this skill

| Symptom                                                                          | Go to instead                                                                                                               |
| -------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| Agent rate-limited, account HOT/DOWN, rebalancing                                | `compass-capacity` → `docs/conventions/capacity.md` (owns `gc-capacity --rebalance`, csu, the API-vs-consumer-limit gap)    |
| Heavy scix batch about to run, oomd killed something                             | `compass-capacity` → `capacity.md` §scix-batch                                                                              |
| Supervisor wedged, tmux dead, sessions drained                                   | `compass-tmux-supervisor` → `docs/conventions/tmux-supervisor.md`; recovery one-liners in `/home/ds/gas-city/CLAUDE.md` §Do |
| Something is broken NOW (pegged supervisor, runaway processes, recovery ladders) | sibling `cityops-debugging-playbook` — this skill is doctrine, not recovery                                                 |
| Dolt slow/broken, bead SQL                                                       | `compass-dolt` → `docs/conventions/dolt-sql-server.md`                                                                      |
| Dispatching a bead to a pool                                                     | `compass-bead-dispatch`; `gc-sling` rules in CLAUDE.md §Bead dispatch                                                       |
| You are an ad-hoc guest session deciding what you may touch                      | `cityops-guest-session-discipline`                                                                                          |
| Hard Don't rules (dolt, sling flags, push gates)                                 | `/home/ds/gas-city/CLAUDE.md` — this skill never overrides it                                                               |

## Terms (defined once)

- **Rig** — an external project directory registered with the city; its agents
  and bead store are managed by the reconciler.
- **Pool** — a worker agent template that the reconciler keeps between
  `min_active_sessions` (floor) and `max_active_sessions` (ceiling) live
  sessions, per `agents/<name>/agent.toml`.
- **PL** — project lead; one per active rig, `max_active_sessions = 1`.
- **Reconciler tick** — the supervisor's periodic pass that converges live
  sessions toward config; interval is `[daemon] patrol_interval` (**2m** here).
- **Wake** — the reconciler starting/resuming one session during a tick.
- **Order** — a periodic job in `orders/*.toml`; paused via
  `[[orders.overrides]]` in `city.toml`.

## Capacity model at a glance (verified 2026-07-07)

- 21 rigs declared in `city.toml` (22 with HQ; roster owned by sibling
  `cityops-topology-contract`, re-verify `grep -c '^\[\[rigs\]\]' city.toml`);
  effective active set is smaller — see
  "Rig suspend/resume" below for why `city.toml` alone doesn't tell you.
- ~29–34 tmux sessions on socket `ds-research` under normal load (2026-07-06
  count; fluctuates).
- Provider capacity (5 Claude OAuth accounts, fungible since 2026-07-05) is
  the other axis — owned by `compass-capacity`, not here.
- Wake budget: `max_wakes_per_tick = 15` at a 2m patrol interval (gc default
  is 5; raised here as the wake-storm cap, added between the 2026-05-29 and
  2026-06-07 config snapshots).

Largest pools (from `agents/*/agent.toml`, 2026-07-07 — regenerate with the
snapshot script rather than trusting this table):

| Pool                     | min/max | Note                                                                                                              |
| ------------------------ | ------- | ----------------------------------------------------------------------------------------------------------------- |
| polecat (gascity rig)    | 6/6     | fork-coding pool                                                                                                  |
| enterprisebench-worker   | 6/6     |                                                                                                                   |
| mem-worker               | 4/6     | `wake_mode = "fresh"`; provider coupled to mem-arm while the fork experiment is live (see its agent.toml comment) |
| gascity-dashboard-worker | 3/3     |                                                                                                                   |
| gascity-packs-polecat    | 2/2     |                                                                                                                   |
| all `-pl` agents         | 1/1     | never scale a PL                                                                                                  |

## Rig suspend/resume — layered, runtime wins

Suspension has **three layers** that routinely disagree — rig-declared
`suspended_on_start`, per-agent `[[rigs.overrides]]`, and runtime
`/home/ds/gas-city/.gc/runtime/suspension-state.json` written by
`gc rig suspend|resume`. The taxonomy is owned by sibling
`cityops-topology-contract` ("Suspension has three layers"); what matters
here: the runtime layer is machine-local, not committed, and an explicit
resume **sticks across restarts even when the rig declares
`suspended_on_start = true`** (per `gc rig resume --help`).

Verified example of the disagreement (2026-07-07): `zeldascension` and
`tom-swe` carry no `suspended` flag for their PL in `city.toml` sections but
are `"suspended": true` in the runtime file — and are down. **Effective state
is what `gc rig list` prints** (it shows `(suspended)` per rig); never infer
it from `city.toml` alone.

```bash
gc rig list                      # effective state — the ground truth
gc rig status <name>             # agents + running state for one rig
gc rig suspend <name>            # reconciler skips its agents; beads DB stays accessible
gc rig resume <name>             # agents start on the next tick (≤2m)
```

Suspension is the cheap, reversible way to shed load: it stops wakes and
dispatch for the rig without touching config, sessions elsewhere, or data.
Prefer it over editing `city.toml` for anything temporary. Permanent topology
changes (adding/removing rigs, flipping declarative suspension) are a
**human gate** — city.toml topology changes need Stephanie's approval
(provisional position, morning-ledger 2026-07-07, city-ops Q2).

## Growing a pool — the +2-workers flow

Policy lives in `agents/mayor/prompt.template.md` ("Worker-pool flex") and the PL side
in `template-fragments/pl-periodic-directives.template.md` ("Pool saturation →
request capacity"). Do not restate or re-derive the policy — read those. As a
dated quotation of that SSOT (2026-07-07): a PL whose pool is saturated (beads
queuing, not idle-by-design) mails mayor; **mayor may auto-approve up to +2
workers per PL; anything larger surfaces to Stephanie.**

The mechanics, verified against the live config:

```bash
# 1. Raise the floor (and ceiling if needed) for the pool:
#    edit agents/<rig>-worker/agent.toml
#      min_active_sessions = <old + N>      # N <= 2 without Stephanie
#      max_active_sessions = <at least the new min>
# 2. Apply without restarting the city:
gc reload
# 3. Verify it settles over the next few ticks (~2m each):
gc session list | grep <rig>-worker
```

Known gotchas (all observed on this host):

- **Config drift restarts the pool.** Editing an agent's config drifts its
  running pool members; they restart once and in-flight beads re-dispatch.
  One-time churn — expect it, don't fight it. (Some mem-workers were observed
  asleep with reason `config-drift` on 2026-07-06.) `gc reload --soft` exists
  to accept drift without draining; read `gc reload --help` before using it.
- **Growth is throttled by the wake budget.** Fresh pool session creation
  spends the same `max_wakes_per_tick` budget as wakes (verified in gc source,
  `internal/config/config.go` MaxWakesPerTick doc). The pool reaches the new
  floor over a few reconcile cycles, not instantly. Wall time to wake N
  sessions ≈ `ceil(N / 15) * 2m` here.
- **Do not force-spawn** sessions to hurry it — that trips spawn-storm
  detection (below). Wait for convergence.
- **A wedged slot blocks the whole floor.** A worker that exited without
  `gc session close` still counts as active; the reconciler will not backfill
  it and beads queue behind it. Fix via the recovery ladder in
  `docs/conventions/tmux-supervisor.md`, not by raising the floor.

Shrinking is the same edit downward plus `gc reload`; the reconciler drains
surplus sessions. Shrink the floor before suspending a rig if you want the
rig alive but cheaper.

## Wake storms and spawn storms

Two distinct failure shapes with similar names:

- **Wake storm** — the reconciler tries to start many sessions at once
  (city start, mass resume). Capped by `max_wakes_per_tick = 15`; the cost is
  slow startup, not damage. `[convergence] max_per_agent = 2, max_total = 10`
  in `city.toml` additionally caps concurrent convergence loops.
- **Spawn storm** — a bead or worktree bug makes sessions re-spawn in a loop.
  This one does damage: it pegs the supervisor and burns account quota.
  Detection: the `spawn-storm-detect` order (every 5m) counts "reset to pool"
  bead events and mails mayor when one bead bounces more than twice; state in
  `.gc/runtime/packs/maintenance/spawn-storm-counts.json`.

**The correct response to a spawn storm is to pause the _feeder_, not to kill
the spawned sessions** — the reconciler just re-creates them. Worked example
from this host, 2026-07-06: a shared mol-formula worktree-provisioning bug
sent every maintenance-cycle polecat into a broken no-`.git` worktree, which
re-spawned endlessly and kept the supervisor pegged ~360% CPU. The fix was a
config pause, done in the house style (pre-flip state captured, comment
carries the RCA and the exit condition). Epilogue: that pause became a
**permanent retirement** — the Temporal maintenance-Run Schedule is the sole
driver of maintenance-cycle dispatch since the gc-372 P5 cutover
(2026-07-16); re-enabling the order would double-dispatch, and the file moves
to `.toml.disabled` after a clean Temporal week (~2026-07-23). Never
re-enable it. The override block and the maker's-side walkthrough are owned
by sibling `cityops-city-change-control` — cite it, don't copy it. The
commit-before-flip convention also belongs to change-control; what matters
for capacity is the _shape_ of the response: identify the feeder (order,
formula, or pool), `enabled = false` it via `[[orders.overrides]]`, and let
the reconciler drain.

## Disk and memory pressure

Host: 1.9T root NVMe at **92% used, 168G free**; 62G RAM (2026-07-07). Both
axes have installed guards; per the provisional trust map (morning-ledger
2026-07-07: nothing documented as trusted-unsupervised without Stephanie's
word), treat each as **working but spot-check recommended**:

| Guard                             | Cadence       | What it does                                                                                                                                                                            | Reliability notes                                                                                           |
| --------------------------------- | ------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `orders/disk-pressure-guard.toml` | every 30m     | if root free < 30G, trims **regenerable** caches only (go-build, golangci-lint, staticcheck, pip, uv); never `go clean -cache`, never huggingface/ollama; nudges mayor on reclaim ≥0.5G | pre-cleared by mayor bead gc-431237 after root hit 100%; go-build regrows 4–8G/cycle so it will keep firing |
| `orders/janitor-log-rotate.toml`  | every 6h      | copy+truncate+gzip any covered log >32MB, keep 5 archives; hard-refuses `events.jsonl` and `beads.json`                                                                                 | execute-mode since 2026-07-04 after one clean dry-run review                                                |
| `orders/janitor-worktree-gc.toml` | daily 04:20   | GCs CLEAN_MERGED gascity worktrees, ≤25 per tick, ≥3 days old                                                                                                                           | execute-mode since 2026-07-04; protects `polecat-*` pool worktrees                                          |
| `orders/dolt-gc-maintenance.toml` | nightly 04:30 | `CALL DOLT_GC()` (dolt auto-GC is OFF here)                                                                                                                                             | see `compass-dolt` for everything else dolt                                                                 |
| `scix-batch` + oomd drop-ins      | on use        | memory blast-radius control for heavy batches                                                                                                                                           | owned by `capacity.md` — read it before any multi-GB job                                                    |

**What no guard reaps (verified 2026-07-07)** — the known disk-growth
liabilities; if root free trends toward 30G, these are where the gigabytes
are, and cleaning them is a Stephanie decision, not an automated one:

- `/home/ds/gas-city/.gc/beads.json` — **150.4M** and growing (file-backend
  city store; `[beads] provider = "file"` is load-bearing, never "fix" it).
- `.gc/beads.json.bak-janitor-*` — 7 copies, **~1.0G** total.
- `/home/ds/gas-city/.beads/backup/` — **2.0G** of dolt archives + oldgen.

Memory: supervisor cgroup peaked at **13.3G** (visible in
`systemctl --user status gascity-supervisor`); the 2026-04-11 supervisor OOM
(7.5G, config re-parse churn) is documented in the incident docs. If
`free -h` shows <3G available, do not start new heavy work; anything multi-GB
goes through `scix-batch` (see `capacity.md`).

## Saturation signals — is the city at capacity?

Cheap reads, in escalation order:

1. `free -h` and `df -h /` — the hardware axes.
2. `systemctl --user status gascity-supervisor | grep -E "Memory|CPU"` —
   supervisor pegged well above ~100% CPU sustained = something is looping
   (spawn storm until proven otherwise).
3. `gc order check` latency — this normally enumerates all ~90 orders; on
   2026-07-06 it timed out at 45s under spawn-storm load and on 2026-07-07 it
   exceeded 2m. Treat multi-minute `gc order check` as a saturation symptom
   in itself, not a broken command.
4. PL mail asking for workers (the designed signal — see the +2 flow above).
5. `spawn-storm-detect` mail to mayor.

When saturated: suspend the lowest-value active rigs first (`gc rig suspend`),
then shrink pool floors, then look at feeders. Account rebalancing does not
help CPU/memory/disk saturation — different axis.

## Provenance and maintenance

All facts verified on-host 2026-07-06/07. One-line re-verification per
drift-prone claim:

| Claim                          | Re-verify with                                                                                                    |
| ------------------------------ | ----------------------------------------------------------------------------------------------------------------- |
| Wake budget 15 / patrol 2m     | `grep -e max_wakes_per_tick -e patrol_interval /home/ds/gas-city/city.toml`                                       |
| Pool floors/ceilings table     | `grep -H _active_sessions /home/ds/gas-city/agents/*/agent.toml`                                                  |
| Effective rig suspension       | `gc rig list` (and `cat /home/ds/gas-city/.gc/runtime/suspension-state.json`)                                     |
| maintenance-cycle retired (Temporal drives it) | `grep -A2 "orders.overrides" /home/ds/gas-city/city.toml`                                                         |
| +2-workers policy wording      | `grep -n "auto-approve" /home/ds/gas-city/agents/mayor/prompt.template.md`                                                 |
| Disk headroom / 30G threshold  | `df -h /` and `head -12 /home/ds/gas-city/orders/disk-pressure-guard.toml`                                        |
| Janitor execute-mode flags     | `grep JANITOR /home/ds/gas-city/orders/janitor-log-rotate.toml /home/ds/gas-city/orders/janitor-worktree-gc.toml` |
| Unreaped growth sizes          | `du -sh /home/ds/gas-city/.gc/beads.json* /home/ds/gas-city/.beads/backup`                                        |
| Supervisor memory peak         | `systemctl --user show gascity-supervisor -p MemoryPeak -p MemoryCurrent`                                         |
| Wake-budget semantics (source) | `grep -n -A6 "MaxWakesPerTick caps" /home/ds/gascity-main/internal/config/config.go`                              |

Provisional positions relied on above (revisit after Stephanie answers the
city-ops discovery questions): city.toml topology changes as a permanent human
gate (Q2); no guard treated as trusted-unsupervised (Q3). Both marked inline
where used.
