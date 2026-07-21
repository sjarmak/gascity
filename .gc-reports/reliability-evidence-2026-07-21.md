# ds-5090 reliability investigation — evidence brief

Investigation window: 2026-07-20 22:00 EDT → 2026-07-21 09:20 EDT.
All line numbers from `gascity` `main` @ `92119a5` unless noted. The checkout at
`/home/ds/gascity` sits on stale branch `_pr1945_check`; the deployed binary tracks
`main` via the `gc-sync` cron. `/home/ds/gas-city` is NOT a git repo.

Every claim below is marked VERIFIED (observed directly) or INFERRED. Several
earlier conclusions in this investigation were wrong and were retracted; the
retractions are included because the wrong turns are informative.

---

## 0. Changes already applied — the baseline moved

An auditor comparing against yesterday's data needs these. All are reversible.

| Change | Where | Applied |
|---|---|---|
| `MemoryHigh` — **set to 26G, then REVERTED to `infinity` 2026-07-21 09:23** (caused an outage, see §0.1) | `gascity.slice` | 2026-07-20 ~22:2x → reverted |
| `MemoryMax=32G` (retained) | `gascity.slice` | 2026-07-20 ~22:2x |
| `MemoryHigh=12G` (no Max) | `app.slice` | 2026-07-20 ~22:3x |
| `[workspace] max_active_sessions = 80` | `/home/ds/gas-city/city.toml` | 2026-07-20 22:23 |
| `max_active_sessions = 20` on all 22 rigs | same | 2026-07-20 22:23 |
| `GC_AGENT_SLICE` promoted to user-manager env | `~/.config/environment.d/gascity-agent-slice.conf` | 2026-07-20 ~22:4x |
| `idle_timeout` 2h → 6h | `agents/polecat`, `agents/city-infra-polecat` | 2026-07-20 22:59 |
| Killed 40 leaked test tmux servers | `-L test-city`, tmpdirs gone | 2026-07-20 ~22:1x |

Backups: `city.toml.bak-20260720-222354`, `agents/*/agent.toml.bak-idle-*`.
`gc config show` exits 0; warning count unchanged at 24 before and after.

### 0.1 An outage this investigation CAUSED — read before trusting §1 or §4

**VERIFIED.** `MemoryHigh=26G` on `gascity.slice` (applied 2026-07-20 ~22:2x)
caused a ~10-hour degradation that peaked in a user-visible outage on 2026-07-21
morning: an interactive Claude session became unresponsive and the hvir beads
panel reported `dolt circuit breaker is open: server appears down`.

Mechanism. The workload settled at **26.35 GB, pinned just above the 26.00 GB
watermark** — identical across 5 samples 3s apart. `memory.high` throttles by
forcing the allocating task into *direct* reclaim and adding a sleep penalty. With
swap 100% consumed and `MemorySwapMax=0` on `gascity-agents.slice`, anon pages had
nowhere to go, so the kernel scanned pages it could never free, permanently:
```
pgscan_direct                1,881,514,136
memory.events high           29,343,959 and climbing
procs_running                331  (on 16 cores)
/proc/pressure/cpu some      avg10=92.87
load average                 375
```
Dolt was **up and listening throughout**. The circuit breaker tripped because
Dolt's health probe could not be scheduled, not because the server was down.

Revert and result — `MemoryHigh=infinity`, 2026-07-21 09:23:
```
09:24:31  load=348.87  runnable=75  cpu_pressure=84.85
09:25:11  load=193.05  runnable=2   cpu_pressure=17.20
09:26:11  load=74.61   runnable=8   cpu_pressure=3.38
09:31     load=14.37   runnable=3   cpu_pressure=2.46
```

**Two lessons that bear on how the rest of this brief should be read.**

1. **"0 OOM kills" was reported repeatedly as evidence the ceiling worked.** It was
   true and it was misleading: discrete kills had been traded for continuous
   reclaim thrash. The absence of the old symptom concealed a worse new one. Any
   claim of the form "metric X stopped" needs a paired check that X was not
   replaced by something costlier.
2. **`MemoryMax` was worth keeping; `MemoryHigh` was not.** `MemoryMax=32G` does
   not throttle — it only intervenes at the limit, and the resulting kill is
   *scoped to the slice* rather than escalating to a global OOM that selects
   interactive sessions by oom_score. That was the actual goal. Do not re-add
   `MemoryHigh` on this slice while swap is exhausted and `MemorySwapMax=0`.

---

## 1. Memory and OOM

**VERIFIED.** Host: 62.4 GB RAM, 8 GB swap, 100% swap consumed and not draining
(`vm.swappiness=10`, cold pages).

Consumption at 2026-07-21 07:31:
```
gascity.slice (all in gascity-agents.slice)   23.7–25.9 GB
postgresql@16-main                            14.1 GB
app.slice (29 tmux-spawn-*.scope units)        9.7 GB
user login sessions (session-*.scope)          5.1 GB
kernel slab/pagetables/stacks                  1.6 GB
reclaimable page cache                         ~2.4 GB
```

`free` is misleading here: of 18 GB "buff/cache", ~14 GB is postgres `Shmem`
(shared_buffers), which is pinned and unreclaimable. Real headroom is
`MemAvailable` ≈ 6 GB.

**Per-session cost: ~0.49 GB** (33 GB of agent memory / 67 sessions). A warm
`claude` is 400–750 MB, a warm `amp` up to 600 MB. This is why the box tops out
near 80 sessions and why `max_active_sessions = 80` was chosen.

**OOM history.** 19 kills in the 3 days before intervention, rate ~2.2/hour on
2026-07-20 (13 kills 10:11–16:09), killing `cass` and `claude` processes by
oom_score across the whole host — because `gascity.slice` had `memory.max=max`
and `memory.high=max`, so pressure escalated to *global* OOM rather than being
contained. Victims were whatever the kernel picked, including interactive
sessions.

**After the ceilings:** 0 OOM kills in 9.1 hours against an expected ~20.
Slice-scoped counters `oom_kill` unchanged at 22 (gascity) and 5 (app).
`memory.events` `high` climbed 0 → 440,637 → 18,995,966, confirming the throttle
is engaging continuously. `max 0` on both — no cgroup-level kills needed.

**Caveat — and this is what went wrong.** The ceiling converted OOM kills into
sustained reclaim thrash. Load ran 140–375 through the night and memory never
dropped below 54 GB. A ceiling cannot create memory. With `MemorySwapMax=0` on
`gascity-agents.slice` (the unit's *only* directive) reclaim has nowhere to spill.
See §0.1: `MemoryHigh` was reverted on 2026-07-21 09:23 and load fell 375 → 14.
`MemoryMax=32G` is retained. Demand still exceeds supply by the same margin it did
before any of this, and no change in this investigation reduced consumption —
they only bounded or redistributed it.

**Agents escape the slice.** 12 of 66 agent panes (~18%) run in `app.slice`, not
`gascity-agents.slice`. Cause: `--slice=` comes from `GC_AGENT_SLICE`
(`internal/runtime/tmux/agent_slice.go:29,129-130`), read from the *spawning
process's own environment*, and it was set only in
`gascity-supervisor.service.d/agent-slice.conf`. Any other spawner hits
`agent_slice.go:94-96` — `if slice == "" { return command }` — which returns the
command unwrapped **with no log line**. Verified not stale: two panes (pids
1720188, 1720240) started in the same second with identical command prefixes
landed in opposite slices. cgroup membership is fixed at fork, so the env fix
only affects new sessions.

---

## 2. Session churn — idle kills

**VERIFIED.** `session.idle_killed` on 2026-07-20: **69 events**.
```
53  polecat                (min_active=6, idle_timeout was 2h)
24  city-infra-polecat     (min_active=2, idle_timeout was 2h)
 2  gascity-packs-polecat  (min_active=2, idle_timeout 6h)
```
The pool already at 6h was 25× quieter. After raising the other two to 6h at
22:59: **0 kills in 8.54 hours** against an expected ~25.6 at the prior rate of
3.0/hour. That result is not a sampling artifact.

**Each kill runs `sp.ClearScrollback(name)`** — the conversation is destroyed,
not detached. With `wake_mode = "fresh"` (set on the worker/polecat pools) the
replacement cannot rejoin the killed turn.

**The May incident** (`agents/gascity-packs-polecat/agent.toml:29-35`): long
single Claude turns emitting no bead activity were reaped mid-run at 2h;
`wake_mode=fresh` replaced the worker and orphaned the convoy
(`routed_to=NONE, 0 progress`). The mitigation was the timeout bump, **not** the
`min_active_sessions` floors — a floor guarantees a *replacement*, not survival
(`session_reconciler.go:1288-1329` falls through to wake logic and respawns on
the same tick).

---

## 3. The fleet is sized by configuration, not demand

**VERIFIED.** 49 agent templates. Sum of `min_active_sessions` floors across 16
templates = **40 sessions**:
```
10 enterprisebench-worker    2 gascity-packs-polecat
 6 polecat                   2 codescalebench-worker
 6 mem-worker                2 city-infra-polecat
 3 gascity-dashboard-worker  1 × 9 others
```
Plus **24 `[[named_session]]` with `mode = "always"`** (double-exempt from idle
reaping). 40 + 24 = 64 against 67–69 live sessions, so ~96% of resident memory is
warm-by-config rather than demand-driven.

Live counts match the floors almost exactly (enterprisebench-worker 10,
polecat 6, mem-worker 6, gascity-dashboard-worker 3).

**Do not treat floor removal as part of an idle-detection fix.** The code already
decouples them: `compute_awake_set.go:646-655` restricts min-active revival to
`SleepReason == city-stop`, with the comment *"Scoped to sleep_reason=city-stop so
idle_timeout and wake_mode semantics are unchanged."* Floor removal is gated on
the `dr-26g` dispatch epic (below), not on the idle work.

**Trap if floors go to 0 today:** `pool.go:126,131` returns `sp.Min` on *any*
scale-check error, so the floor is also the failure-mode default. Setting it to 0
means a failing scale check silently yields an empty pool.

---

## 4. The Dolt connection cascade — the largest live problem

**VERIFIED.** One `dolt sql-server` serves **all 23 databases** from a single
`data_dir` (`/home/ds/gas-city/.beads/dolt`).

`dolt.log` (68 MB) error census:
```
437,556 ×  "max waiting connections reached. Client rejected. Increase server max_connections"
  7,892 ×  "error running query"
  2,425 ×  "socket state is broken, returning error"
    261 ×  "max connections reached. Clients waiting"
```

Config: `max_connections: 256`, `back_log: 50`, `wait_timeout: 30`,
`read_timeout_millis: 60000`.

**Connection rate: ~7/second sustained** (`connectionID=34613` at `m=+4895s`).

**Socket states oscillate**, they do not leak monotonically:
```
09:01:20  CLOSE-WAIT=2799  ESTAB=24
09:01:52  CLOSE-WAIT=2871  ESTAB=8
09:14:29  CLOSE-WAIT=556   ESTAB=22
09:15:52  CLOSE-WAIT=681   ESTAB=18
```
An earlier reading of this as a permanent Dolt socket leak was **retracted** — a
goroutine deadlock would never drain. This is backlog under overload.

**Retracted a second time, and more strongly.** After `MemoryHigh` was reverted
(§0.1) and CPU pressure fell from 92.87% to 2.46%, socket states became:
```
241 TIME-WAIT, 1 LISTEN, 0 CLOSE-WAIT, 0 ESTAB backlog
```
CLOSE-WAIT went to **zero** with no Dolt change, no restart, and no config change —
only CPU becoming available. Dolt closes sockets promptly when its goroutines can
be scheduled; under CPU starvation they could not run to call `close()`, so
sockets accumulated. **The CLOSE-WAIT accumulation was a symptom of host CPU
starvation, substantially self-inflicted, not a Dolt defect.**

What this does NOT retract: the 437K `max waiting connections reached` entries
accumulated in a 68 MB log over a long period including well before any change
here, and the ~7 conn/sec rate against `max_connections: 256` is real and
independently measured. The fork-per-command architecture in
`doctor_fork_rate.go` remains a genuine problem worth fixing. But the acute
symptoms — the wedge, the circuit breaker, the socket pile-up — should be
re-measured on a CPU-healthy box before being attributed to Dolt at all. An
auditor should treat §4's severity as unproven pending that re-measurement.

**Root cause is fork-per-command, and it is already named in-tree.**
`cmd/gc/doctor_fork_rate.go:13-31` exists specifically for this cascade:
> *"gc forks bd.real per command, which in turn talks to a per-city dolt
> sql-server… measured in the field at load ~25 with CPU ~66% busy and ~96% of
> forks coming from bd.real + dolt + gc, vs ~0.4% from the agents themselves."*

Remedies documented at `doctor_fork_rate.go:133-136`.

**Pooling is NOT the fix.** The beads library already pools correctly for
daemons: `beads@v1.1.0/internal/storage/dolt/store.go:292-299` sets
`MaxOpenConns=10, MaxIdleConns=5, ConnMaxLifetime=1h, ConnMaxIdleTime=20s`,
applied at `store.go:1402-1405`. The connections come from **short-lived
processes**, where a pool cannot help by construction.

Paths:
- `cmd/gc/cmd_bd.go:298-304` — `gc bd ...` forks the `bd` binary per command.
- `internal/beads/bdstore.go:96,469` — `BdStore` implements *every* operation as a `bd` subprocess.
- `cmd/gc/cmd_bd.go:268` — a write additionally forks a second `bd` just for a substring-collision guard, so `gc bd update` ≈ 2 processes ≈ 4 connections.
- Agent prompts instruct bare `bd` in 39 places (`ready` 15, `show` 11, `update` 8, `list` 5).
- ~2 connections per `bd` process (in-tree note at `cmd/gc/cmd_nudge.go:1768-1770`).

**Ranked remedies (from code inspection):**
1. `[beads] backend = "doltlite"` (`config.go:1378`) — `cityUsesManagedDoltBeadsLifecycle` (`providers.go:657`) then returns false and **no sql-server is launched at all**. One config line + a data migration. `store_rollout.go` exists for staging.
2. Route read-only `bd` subcommands through the already-running supervisor API (127.0.0.1:8372, long-lived pooled stores + `CachingStore`). **`gc beads show` already does this** (`cmd_beads.go:207-241`); `gc bd show` is a raw passthrough. Insert in `doBd` (`cmd_bd.go:~223`) with fallback. Reads are ~31 of 39 prompt-instructed calls.
3. Drop the redundant gc-side store open at `cmd_bd.go:268`. ~20 lines.
4. NOT recommended: raising `max_connections` (delays the wedge, costs memory — server already 862 MB RSS) or pool tuning (no effect).

**Upstream:** running **2.1.2**; current is **2.2.1** (2.2.0 ≈ 2.1.11, no novel
PRs). dolt#11196 (closed, fixed in 2.1.7) documents a `strftime` v1.0.4 RLock
leak reachable via `DATE_FORMAT` that permanently deadlocks handler goroutines
and pegs connection slots. Mechanism resembles ours; the draining CLOSE-WAIT
counts argue against it being our bug. Upgrade is low-risk (JWT auth scoping
changed in 2.1.11 — only matters with `authentication_dolt_jwt`; query plans
changed in 2.1.11). **Do not file upstream** without a goroutine dump from a
wedged server; the log saying "Increase server max_connections" 437K times points
at our configuration, not their defect. `kill -QUIT <pid>` dumps to `dolt.log`
(verified: fds 1 and 2 both point there).

---

## 5. SPOF and the 2026-07-21 outage

**VERIFIED.** 06:59:00 the server (pid 2339669, port 29620) started and closed its
listener 14s later (`Flush() failed: set tcp 127.0.0.1:29620: use of closed
network connection`). It then sat alive in `futex_do_wait`, **zero open sockets**,
port unbound, for ~42 minutes. All 23 stores were unavailable simultaneously; the
completion reconciler logged `scanned=0 ... errors=23`.

The `__gc-managed-dolt-scope-watchdog` (`cmd/gc/dolt_scope_watchdog.go:167-278`)
eventually recycled it (new pid 2784208, port 29621). **The watchdog monitors
exactly one thing**: whether the `--config` file still exists
(`managedDoltScopeGone`, `:118-125`, a single `os.Stat`, 30s poll, 2-miss
threshold). It does not monitor health, connections, or memory.

A CLOSE-WAIT/health threshold could live in the existing 30s supervise loop at
`:250-278`; `/proc/<pid>/net/tcp` state `08` counting needs no DB connection.
Two caveats: (a) restart is blunt — it takes all 23 stores down, which is the
failure mode being avoided, so a threshold is better as an **event** than an
auto-kill (`events.Recorder` already wired at `dolt_project_id.go:117`); (b) the
watchdog's contract is deliberately ownership-only (`:56-58`), and making it a
health supervisor deserves an explicit decision.

**Per-rig isolation is not config-only.** `gc rig set-endpoint <rig> --self --port N`
(`cmd_rig_endpoint.go:40-208`) handles the re-pin, but rigs have no local
`.beads/dolt` and Dolt refuses a second server on a locked database
(`database "CodeScaleBench" is locked by another dolt process; either clone the
database`). Databases must be physically split out of the shared `data_dir`
first. Isolating only the city store has the same requirement.

---

## 6. Stale claims cannot be reliably released

**VERIFIED.** Two mechanisms, both with holes.

**A — `/home/ds/gas-city/bin/completion-reconciler`** (Python, 521 lines, NOT in
the repo, installed 2026-07-18, timer every 15 min). Contract: `in_progress` +
assigned + no progress for a priority-scaled window (P0 1h → P4 48h), then a
checkpoint request + 30 min grace, then status `open` with assignee cleared and
worktree/branch/routing preserved. `decide()` `:139-173`, `requeue()` `:315-343`.

It works when it runs — 11 requeues in 3 days, covering named sessions
(`city-infra-pl`, `gascity-maintenance-pl`, `gascity-packs-pl`), priority scaling
visible in the data (checkpoints at 3614s/3840s for P0, 14550s/15116s for P1).

But: **currently `failed`**, only **5 of 21 runs clean today**, **319
`scan_failed`** events. `stores_for()` (`:433`) runs `gc rig list --json` with a
60s timeout *before the lock and before any store is scanned*, and a timeout
aborts the whole run — including the city store, which needs no rig list.

**B — `releaseOrphanedPoolAssignments`** (`cmd/gc/pool_session_name.go:118-214`,
called from `city_runtime.go:2177`, emits `bead.dead_assignee_reopened`). Narrower
than it sounds: only agents where `SupportsGenericEphemeralSessions()` is true
(`:164-167`), named sessions skipped (`:180`), liveness = "an open session bead
exists" (`:177,183`) rather than a process probe — **so a wedged-but-alive session
is invisible to it** — and it returns `nil` releasing nothing when the tick's
snapshot is partial (`:108-110`), which is what a struggling store produces.

**Empirical result:**
```
work beads stranded on dead sessions (session.stranded)   14
beads reopened (bead.dead_assignee_reopened)               9
overlap                                                    0
```
Zero. `code-intel-digest-dv0.5.6` stranded 2026-07-20T07:35:51Z, unreopened 28h
later. 13 `gascity-dashboard-*` beads stranded 2026-07-21T09:02:46Z with a healthy
store available for ~2h, unreopened. All 9 reopens happened in one 10-minute
window on 2026-07-20 18:33–18:43 and none since.

**No `claimed_at` exists on beads.** `git grep claimed_at main` hits only
`internal/nudgequeue/state.go:46` (unrelated). The claim path
(`cmd_hook_claim.go:480-516`) writes work branch, session ID, session name — no
timestamp. The lease is measured off bead `updated_at`
(`completion-reconciler:147-148`), which `bd` also bumps during unrelated
`is_blocked` dependency recomputes (`internal/beads/beads.go:174-178`). Observed
staleness did reach 124,553s (34.6h) and 51,609s (14.3h) at requeue time, so the
lease is not universally frozen — but the baseline is wrong in principle.

---

## 7. Idle detection keys off terminal bytes

**VERIFIED.** Path: `buildIdleTracker` (`cmd_start.go:211`) → `checkIdle`
(`idle_tracker.go:101-117`) → `workerSessionTargetLastActivityWithConfig`
(`worker_handle.go:516-525`) → `sp.GetLastActivity` →
`Tmux.GetSessionActivity` (`tmux.go:2289-2301`) → `tmux list-windows -F
'#{window_activity}'`.

Any pane output — spinner frame, token counter, progress bar — advances
`window_activity`. `discountPokeActivity` (`tmux.go:2338+`) subtracts only *gc's
own* send-keys echo. A redrawing agent TUI resets its own idle clock forever; a
quiet working agent looks idle.

**`DecideIdleTimeout` deliberately ignores assigned work.**
`lifecycle_timers.go:132-155` has no assigned-work rung, while the structurally
identical `DecideMaxSessionAge` 20 lines above (`:113-119`) does. This is a
*tested contract*, not an oversight —
`lifecycle_timers_test.go:190-192` asserts it explicitly:
> *"Idle-timeout never consults assigned work; an unknown work fact must not
> trigger a gather action or change the stop decision."*

**Why that contract likely exists (INFERRED, but the mechanism is concrete):**
assigned work is a *state*, not a liveness signal. A session that claims a bead
and then wedges holds `AssignedWorkHas` forever, so an assigned-work rung would
make wedged claim-holders **immortal** — the sessions most needing reaping. Idle
timeout is currently the last line of defence against them, and §6 shows the
recovery machinery does not reliably release stale claims.

**A better signal already exists and is live.** `internal/sessionlog` parses the
agent CLI's own JSONL transcript: `InferActivity` (`tail.go:164-198`) returns
`in-turn` / `idle`; `ExtractTailMeta` (`tail.go:39-53`) reads only the last 64 KiB;
`SessionLogAdapter.TailActivity` (`sessionlog_adapter.go:124-140`) types it; it is
**already consumed** to set `PhaseBusy` at `handle_lifecycle.go:237-240`. Path
resolution is batched via `ResolveKeyedTranscriptPaths`
(`transcript_lookup.go:35`). VERIFIED on the box: 15 of 36,537 transcripts
modified within a 10-minute window, so mtimes track real activity.

Gap: `TailMeta` carries no timestamp — sufficient for a boolean "mid-turn" gate,
would need an additive field for duration-based idle.

**`sleep_after_idle` is a trap, not an underused feature.** It reads
`bead.IdleSince`, populated from exactly one place — `detached_at`, the time a
*human tmux client* detached (`compute_awake_bridge.go:142-144`,
`session_sleep.go:163-207`). Headless fleet agents never have a human attached, so
it is stamped near creation and never moves. Enabling it would sleep workers a
fixed duration after start regardless of work. Configured on zero agents, which is
correct.

**Reporting-semantics bug:** an idle kill is scored as *unhealthy*
(`events.SessionIdleKilled` → `LifecycleIdleKilled` → `g.UnhealthyTotal`,
`reliability.go:36-37,65-66,287-291`). Accurate reaping would inflate unhealthy
counts for correct behaviour.

---

## 8. Broken things noticed in passing

- **CASS index is stale.** `cass health --json` → `"healthy": false, "errors": ["index stale"]`. Searches return `count: 0` alongside non-zero `total_matches` (13 and 842) — it reports hits it cannot return. Agent prompts instruct checking CASS before investigating.
- **Two "it's tracked" citations are false.** `gpk-me1h` (cited at `agents/gascity-packs-polecat/agent.toml:34` as tracking the idle-detect gap) and `gc-09bs3` (cited in ADR-0010 as tracking the scale-to-zero regression) **do not exist in any of the 23 bead databases**.
- **`code_intel` schema drift.** Live errors: `relation "mirror_state" does not exist` (07-20 15:24), `column "watermark" does not exist` (07-20 15:40), `function to_timestamp(timestamp with time zone) does not exist` (07-12), `column "records_ingested" does not exist` on `ds@scix` (07-13). Source at `/home/ds/projects/code-intelligence-digest`.
- **Postgres is sized for a dedicated DB host.** `shared_buffers=16GB` (26% of RAM, pinned), `work_mem=256MB` × `max_connections=100`, `effective_cache_size=48GB` on a box with ~2.4 GB real page cache. It legitimately serves pgvector/vectorscale halfvec + HNSW indexes for `code-intelligence-digest`, so this is a genuine tradeoff, not obvious waste. An earlier recommendation to halve `shared_buffers` was **retracted** — low checkpoint volume measures writes, and a read-heavy vector workload wants the index resident.
- **`/tmp` is 17 GB; root filesystem 88% full** (231 GB free).
- **40 leaked `-L test-city` tmux servers** from `TestCmdNudgeStatusJSON` with vanished `/tmp/gct*` dirs (killed; 18 with live dirs left alone). The test does not kill its tmux server, and the socket name is shared across runs.

---

## 9. Existing work — do not duplicate

- **`dr-26g`** — OPEN, P0. *"EPIC: dispatch redesign — claim order to code."* `.1` closed, delivering **ADR-0010 `scheduler-bound-ephemeral-workers`** (`docs/adr/0010-scheduler-bound-ephemeral-workers.md`). `.2`/`.3` blocked, `.4`–`.8` open. Critical path `.2→.3→.4→.6→.7→.8`, `.5` parallel. **This is the scheduler-spawns-directed-ephemeral-worker design.** ADR-0010 states the blocker for `min_active=0` is dispatch: a cold pool has nothing to nudge, and the nudge defaults to `wait-idle`, which reports success then dead-letters (ADR-0010:31-32, `CLAUDE.md:21`). Cutover gates at :519 require shadow-parallel comparison, chaos suite, scale-to-zero regression, and migration replay to be green first.
- **`dr-ae1b8`** — closed 2026-07-05. Idle-reaper calibration, report-only; shipped `bin/idle-session-report`; auto-kill deliberately never wired. Output `docs/design/idle-reaper-calibration.md`: n=4,034 over 9 days, dashboard-idle over-reports idle by >60min in **47%** of observations, max ~5 days. Caveat: that study measured *transcript* mtime, the killer uses *tmux window_activity* — different signals, so it bounds one and not the other.
- **`dr-t4w0`** — closed. *"liveness sensor — detect stalled work by ARTIFACT movement, never by bead status."* Same conclusion as §7 reached independently.
- **`dr-bzdeso` / `dr-fshqho`** — closed 2026-05-29. The "21% drain-without-commit" figure. `dr-bzdeso` says plainly: *"The 21% drain-without-commit figure is lore… If unreproducible, dependent pass-bars are unfalsifiable."* Successors `gc-0o2ub`/`gc-x2b26` do not exist in the `gascity` db. **The rate was never reproduced.**
- **`cmd/gc/doctor_fork_rate.go`** — existing doctor check for the fork cascade in §4, with remedies at `:133-136`.

---

## 10. What was NOT verified

- Whether `cache-reconcile`'s bulk `bead.updated` traffic bumps `updated_at` on work beads and resets completion-reconciler leases. Observed 34h staleness argues it does not do so universally; untested.
- Whether the 13 stranded `gascity-dashboard-*` beads were skipped via the partial-snapshot bail at `pool_session_name.go:108-110`. Behaviour is consistent; the controller's `assignedWorkBeads: PARTIAL` stderr does not reach journald under this user.
- Exact connection count per `bd` invocation (INFERRED as ~2 from `cmd_nudge.go:1769`, not measured). `doctor_fork_rate.go:133` names the command to measure it.
- Whether the Dolt wedge is caused by connection exhaustion, auto-GC (`auto_gc_behavior.enable: true`, `dolt_auto_gc_enabled: ON`), or a goroutine deadlock. A `kill -QUIT` dump on a wedged server would settle it.
- Whether idle kills observed 2026-07-20 orphaned in-flight convoys as the May incident did. Mechanism is identical; no orphan evidence gathered for those specific kills. Correlating `session.idle_killed` timestamps against bead `routed_to` transitions would confirm.
- The `max_active_sessions = 80` cap has never bound (sessions 67–69 throughout), so its enforcement is unproven in production.
- **Whether the 2026-07-21 06:59 Dolt wedge was caused by the `MemoryHigh` CPU starvation.** The wedge (42 min, `futex_do_wait`, zero bound sockets) occurred ~8h into the throttled period and is consistent with goroutines unable to run. Not proven; the wedge predates the load peak measured at 09:20. Re-measure before treating Dolt wedges as an independent failure mode.
- **Baseline contamination generally.** Everything measured between 2026-07-20 22:2x and 2026-07-21 09:23 was taken on a host in permanent direct reclaim at 85–93% CPU pressure. Latency, timeout, wedge, and connection-state observations from that window are suspect and should be re-taken. Session counts, memory totals, config contents, bead-store contents, and code/file:line claims are unaffected.
