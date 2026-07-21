---
name: gc-dolt-ops
description: >-
  Managed Dolt server operations for Gas City: lifecycle (start/stop/restart
  races, the noms-journal lock), port ownership and squatter detection,
  leaked-server cleanup and reaping, read-only recovery, disk preflight,
  read-storm/thundering-herd protections, and bd shell-out semantics. Load
  before touching cmd/gc/dolt_*.go, cmd/gc/beads_provider_lifecycle.go,
  internal/beads/bdstore.go, or examples/bd/assets/scripts/gc-beads-bd.sh.
  Also load when diagnosing: bead reads/writes hanging or timing out, a city
  that drained its pools for no visible reason, "address already in use" on
  the dolt port, dozens of leaked dolt sql-server processes, dolt stuck
  read-only, noms chunk-journal corruption, or ENOSPC crashes. Trigger
  phrases: dolt, sql-server, bd store, noms, chunk journal, port squatter,
  dolt-cleanup, ghost dolt, read-only dolt, dolt.lock.
---

# gc-dolt-ops

Gas City's bead store in production is `bd` backed by an embedded **Dolt SQL
server** — a real external database process, not an in-process library. The
git archaeology shows managed-Dolt lifecycle is one of the two chronic fix
classes in this codebase (the other is reconciler wake races): singleton-lock
races corrupted the chunk journal (#3174), a port squatter silently drained a
whole fleet (#2930), and one dev box accumulated 314 leaked `dolt sql-server`
processes holding ~44 GB RSS (#3306). One lifecycle fix took four branch
generations to land (`fix/dolt-start-lifecycle-lock-2130` through `-v4`).
This skill teaches the process model, the iron rules, and the runbook for
each recurring failure class, each grounded in a real commit.

Tier 1 skill: pure knowledge, single-session, no subagents or worktrees
required. (Tier classification is provisional per the 2026-07-07
morning-ledger answers, pending maintainer confirmation.)

**Ground truth note (2026-07-06):** facts below were verified against
`origin/main` at `f828bbe4b`. Older checkouts differ materially: before
mid-2026 the dolt pack lived at `examples/dolt/` (now `examples/bd/dolt/`),
the minimum dolt version was 1.86.2 (now 2.1.0), and the squatter-hold,
deleted-scope-reap, disk-preflight, and data-dir-lock hardening did not
exist. Run the re-verification commands at the bottom against YOUR checkout
before acting on version- or path-sensitive claims.

## When NOT to use this skill

| You are doing                                                          | Use instead                                                                 |
| ---------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| Bead/molecule/formula semantics, sling, hooks (what the store _holds_) | sibling skill `gc-meow-work-model`                                          |
| Controller tick, pool scaling, drain/wake logic (what _reads_ demand)  | sibling skill `gc-reconciler-lifecycle`                                     |
| Periodic compaction / `DOLT_GC()` / backup snapshotting                | `engdocs/contributors/dolt-maintenance.md` (the supervisor runbook)         |
| Running the test suite, build tags, CI parity                          | sibling skill `gc-build-verify`                                             |
| Writing new tests against the store (fakes, conformance, exec-spy)     | sibling skill `gc-test-authoring`                                           |
| Trace collection for controller incidents                              | sibling skill `gc-debugging`                                                |
| Historical bug-by-bug audit of Dolt regressions                        | `engdocs/contributors/dolt-regression-audit.md`                             |
| Redesigning the beads/Dolt endpoint contract                           | `engdocs/design/beads-dolt-contract-redesign.md` (design doc, not a how-to) |

Sibling skills are part of the same departure library; if one is missing,
the engdocs named above are the authoritative fallback.

## Vocabulary (defined once)

| Term                     | Meaning                                                                                                                                                                                               |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **bead**                 | A unit of work (task, message, molecule step) stored in the task store.                                                                                                                               |
| **bd**                   | The beads CLI (`gastownhall/beads`). Gas City's Go code never speaks SQL to Dolt for bead CRUD; it shells out to `bd`.                                                                                |
| **Dolt**                 | A MySQL-compatible, git-versioned SQL database (`dolthub/dolt`). `bd` stores beads in it.                                                                                                             |
| **managed Dolt**         | The one `dolt sql-server` OS process per city that gc itself launches, monitors, and stops. Contrast: a _remote_ server gc never manages (any `GC_DOLT_HOST` other than 127.0.0.1/0.0.0.0/localhost). |
| **noms / chunk journal** | Dolt's on-disk storage layer. Each database holds an exclusive flock on `.dolt/noms/LOCK` from open until the journal is flushed at close. Two servers on one data_dir = corrupted journal.           |
| **city / rig**           | A city is a directory with `city.toml` + `.gc/` runtime state; rigs are per-project sub-scopes that may share the city's Dolt server or run their own.                                                |
| **scope**                | A bead-store root (`<city>/.beads` or `<rig>/.beads`). Many scopes can be databases inside one Dolt server.                                                                                           |
| **pack**                 | A directory of config/commands/orders gc composes into a city. The `bd` and `dolt` packs are builtin (embedded in the binary, materialized under `.gc/system/packs/`).                                |
| **dog**                  | The dolt pack's local maintenance agent pool; `mol-dog-*` formulas (backup, doctor, stale-db, phantom-db) route to it.                                                                                |

## 1. The process model

```
gc controller ──spawns/monitors──▶ dolt sql-server   (one per city, port ~10000-60000)
     │                                   ▲
     │ shells out                        │ MySQL wire protocol
     ▼                                   │
 internal/beads/BdStore ──exec──▶ bd CLI ┘
```

Three load-bearing consequences:

1. **Dolt is an operational subsystem, not a library.** It crashes, leaks,
   fills disks, and holds POSIX locks independently of gc. Liveness is
   discovered from the live process table and TCP probes, never assumed.
2. **All bead CRUD goes through `bd`** (`internal/beads/bdstore.go`), which
   speaks SQL to the server. gc's own lifecycle probes (`SELECT @@datadir`,
   health checks) are the only direct SQL gc issues.
3. **Two lifecycle implementations exist and must agree**: the shell exec
   provider `examples/bd/assets/scripts/gc-beads-bd.sh` (the canonical
   beads-provider protocol: `start`, `ensure-ready`, `stop`, `shutdown`,
   `init`, `health`, `recover`, `probe`) and the Go-side helpers in
   `cmd/gc/dolt_*.go` that the script and the controller both call back into
   (via the hidden `gc dolt-state` subcommands). When you change one side,
   grep the other.

Backend variants (2026-07-06): `GC_BEADS_BACKEND=dolt` is the default;
`doltlite` is a sqlite3-backed variant handled inside the same script; a
Postgres beads backend also exists (`gc doctor --explain-postgres-auth`,
`internal/doctor/postgres_auth.go`). This skill covers managed **Dolt**
only.

## 2. On-disk layout and discovery

Per city (env override in parentheses, from
`cmd/gc/dolt_runtime_layout.go`):

| Path                                                                     | What it is                                                                                                  |
| ------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------- |
| `.beads/dolt/` (`GC_DOLT_DATA_DIR`)                                      | Dolt data_dir — one subdirectory per database                                                               |
| `.beads/dolt/<db>/.dolt/noms/LOCK`                                       | Dolt's own exclusive store lock (see iron rules)                                                            |
| `.gc/runtime/packs/dolt/dolt.log` (`GC_DOLT_LOG_FILE`)                   | Server stdout/stderr — first stop for any startup failure                                                   |
| `.gc/runtime/packs/dolt/dolt.lock` (`GC_DOLT_LOCK_FILE`)                 | gc's flock guarding start/stop (NOT dolt's lock)                                                            |
| `.gc/runtime/packs/dolt/dolt.pid` (`GC_DOLT_PID_FILE`)                   | PID hint (validated, never trusted — see below)                                                             |
| `.gc/runtime/packs/dolt/dolt-provider-state.json` (`GC_DOLT_STATE_FILE`) | Provider runtime state: running/pid/port/data_dir                                                           |
| `.gc/runtime/packs/dolt/dolt-state.json`                                 | Published state hint read by `currentManagedDoltPort`                                                       |
| `.gc/runtime/packs/dolt/dolt-config.yaml` (`GC_DOLT_CONFIG_FILE`)        | Generated `dolt sql-server --config` file — regenerated on every start; hand edits are lost                 |
| `.beads/dolt-server.port`                                                | Compatibility mirror for raw `bd`, not a gc control-plane input (`cmd/gc/beads_provider_lifecycle.go:1259`) |

**Port allocation** (`cmd/gc/dolt_port_selection.go`): `GC_DOLT_PORT` env >
valid state file > repaired state > deterministic seed
(`cksum(cityPath) % 50000 + 10000`), then linear scan for a free port. On
"address already in use" during start, the Go path retries on the next free
port up to 5 attempts (`dolt_start_managed.go`).

**The PID-file doctrine nuance.** AGENTS.md says "No status files — query
live state." The dolt PID/state files look like a violation but are not:
every consumer treats them as _hints_ and re-validates against reality
before use. `repairedManagedDoltRuntimeState` accepts a state file only if
the port's holder PID is alive, is a managed dolt owned by this layout, has
no deleted data-dir inodes in `/proc/<pid>`, and answers TCP
(`dolt_port_selection.go`). Copy that pattern; never branch on a PID file
alone.

To find the live port for SQL work, prefer the pack command (it resolves
everything for you):

```bash
gc dolt sql -q "SHOW DATABASES"     # one-shot query, server or embedded mode
gc dolt sql                          # interactive shell
```

## 3. Command surface

Operator commands (the `dolt` builtin pack, source `examples/bd/dolt/commands/`):

| Command                 | Does                                                                                |
| ----------------------- | ----------------------------------------------------------------------------------- |
| `gc dolt status`        | Exit 0 if the server answers, non-zero otherwise (delegates to `gc-beads-bd probe`) |
| `gc dolt start`         | Start if not already running (exit 0 if started/already up, 2 if remote = no-op)    |
| `gc dolt restart`       | Stop and start — the force-restart when `start` would no-op                         |
| `gc dolt logs`          | Tail the server log                                                                 |
| `gc dolt sql [args]`    | SQL shell / one-shot `-q`; falls back to embedded mode when no server is up         |
| `gc dolt list`          | List databases                                                                      |
| `gc dolt health --json` | Data-plane health report; pipe into `gc dolt health-check` to fail on criticals     |
| `gc dolt recover`       | Detect and recover read-only state (local servers only)                             |
| `gc dolt cleanup`       | Orphaned-database cleanup (pack wrapper)                                            |
| `gc dolt compact`       | Flatten commit history to reclaim storage                                           |
| `gc dolt sync` / `pull` | Push/pull configured remotes (only meaningful when remotes are configured — see §8) |
| `gc dolt rollback`      | List/restore migration backups                                                      |

Go-side and diagnostic commands:

| Command                               | Does                                                                                                                                                                                                                                                     |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `gc dolt-cleanup [--json] [--force]`  | The Go-side orphan reaper (top-level because the pack owns the `gc dolt` namespace). **Dry-run by default.** See §6.                                                                                                                                     |
| `gc doctor`                           | Includes checks `dolt-server`, `dolt-noms-size`, `dolt-config`, `dolt-version` (`internal/doctor/checks.go`). `dolt-config` drift is report-only — `CanFix()` is false.                                                                                  |
| `gc dolt-state …`, `gc dolt-config …` | Hidden plumbing used by the lifecycle scripts (`allocate-port`, `probe-managed`, `start-managed`, `recover-managed`, `wait-ready`, …). Useful read-only when debugging the scripts; do not build automation on them — they are internal and unversioned. |

Config knobs live in city.toml `[dolt]` (`internal/config/config.go`,
`DoltConfig`): `port`, `host`, `archive_level` (default 0),
`auto_gc_enabled` (default true — bounds the noms journal so the
unclean-stop corruption window stays small), `max_connections`,
`read_timeout_millis`, `write_timeout_millis`, and
`dolt_lock_release_timeout` (default "1m", see §4).

## 4. Iron rules

1. **One server per data_dir, ever.** Dolt holds an exclusive flock on each
   database's `.dolt/noms/LOCK` from open until the chunk journal is
   flushed at close. A second `dolt sql-server` binding the same data_dir
   while that lock is held races the journal flush and corrupts the store
   (#3174, fixed by `60e402be9`: lock-keyed singleton guard, blocking stop,
   fail-closed start). The start path now waits up to
   `dolt_lock_release_timeout` (default 1m) for the lock to clear, then
   **refuses to start** rather than race. If start fails with a held-lock
   error, find and gracefully stop the holder; do not delete the LOCK file.
2. **Never SIGKILL a dolt server mid-write.** Stop paths block until
   process exit AND lock release (`graceful_stop_owned_pid` in
   `gc-beads-bd.sh`; `dolt_data_dir_lock.go` keys the guard on "what dolt
   actually enforces, rather than TCP readiness or PID files").
3. **Never delete `dolt.lock` (gc's start lock) to "unstick" a start.**
   flock attaches to the inode, not the pathname; recreating the file lets
   a second starter bypass a live holder (comment at the `exec 9>` block in
   `gc-beads-bd.sh`).
4. **Never hand-launch `dolt sql-server` or let `bd` auto-start one.** gc
   pins `BEADS_DOLT_AUTO_START=0` in every bd invocation because bd's CLI
   auto-start ignores `dolt.auto-start:false` (a beads `resolveAutoStart`
   priority bug) and starts rogue servers from the agent's cwd with the
   wrong data_dir (`cmd/gc/bd_env.go`, comment above the pin). Rogue
   servers are exactly how port squatting (§5) starts.
5. **Never blanket-`pkill dolt`.** Other cities, rigs, and live test runs
   own some of those processes. Use `gc dolt-cleanup`, whose classifier
   protects anything it cannot prove is orphaned (§6).
6. **Don't run embedded-mode `dolt sql` inside the data dir while the
   server is up** — the server holds the store locks. `gc dolt sql` does
   the right thing automatically (server connection when reachable,
   embedded only when not).
7. **Respect the disk floor.** Managed start refuses below 500 MiB free
   (`GC_DOLT_MIN_FREE_BYTES` to tune, 0 disables) and warns below 2 GiB
   (`cmd/gc/dolt_disk_preflight.go`). ENOSPC is not just a crash: it opened
   the port-squat window in the #2930 incident.

## 5. Worked example: the fleet-drain incident (#2930) and the squatter hold

The single best illustration of why Dolt is an _operational_ subsystem.

**What happened (2026-06-01):** the managed Dolt hit ENOSPC and crashed. A
standalone `bd`-launched Dolt (iron rule 4 violation) **squatted the vacated
port**. The reconciler connected to the squatter — a different data-dir with
zero beads — read **zero demand**, and did exactly what zero demand implies:
drained every `min=0` pool in the fleet, with nothing left to respawn them.
Recovery was fully manual. The root gap: _a successful read of the wrong
store is indistinguishable from "genuinely idle"_ — no error path fires.

**The fix** (`9c2b6f564`, "hold pools when a foreign Dolt squats the managed
port", #3003 — the squash-merged commit on main; its pre-merge branch
history carried separate squatter-probe-gating and steady-state-drain-guard
commits that were folded in and are not reachable from `origin/main`):

- `cmd/gc/dolt_identity.go` — `managedDoltDataDirMismatch` asks the server
  bound to the city's managed port `SELECT @@datadir` and compares against
  the expected `<city>/.beads/dolt`. The function's doc comment carries the
  incident write-up.
- `cmd/gc/city_runtime.go:2177` — on a **confirmed** mismatch the tick logs
  `"managed dolt serves an unexpected data-dir (squatter on the managed
port?); holding pools this tick"` and sets `result.StoreQueryPartial =
true`, which the existing partial-read hold path already honors: the
  undesired-pool sweep is suppressed and the fleet **holds instead of
  draining**.
- **Fail-open by design**: any inability to determine identity (query
  error, unparseable CSV, no resolvable port) does NOT hold — a transient
  SQL hiccup must never wedge the fleet. Only a _positive_ mismatch holds.
- **Gated to the only tick that can drain** (no assigned work while
  sessions are running), so idle and busy cities pay nothing.

**Runbook when you suspect a squatter** (symptoms: pools draining on a busy
city, or the log line above):

```bash
# 1. Who actually holds the managed port, and is it ours?
gc dolt-state probe-managed --city "$PWD" --host 127.0.0.1 --port "$(gc dolt-state allocate-port --city "$PWD")"
#    -> running / port_holder_pid / port_holder_owned / port_holder_deleted_inodes / tcp_reachable

# 2. What data-dir is the listener serving?
gc dolt sql -q "SELECT @@datadir"

# 3. If it is foreign: stop the squatter by PID (verify its cmdline first),
#    then restart the managed server.
ps -o pid,cmd -p <squatter-pid>
kill <squatter-pid>            # plain SIGTERM; it is not our server but is still a dolt holding locks
gc dolt start
```

The reconciler-side hold behavior is `gc-reconciler-lifecycle` territory;
the identity probe and everything above the reconciler is this skill's.

## 6. Leaked servers: `gc dolt-cleanup`

Leaked `dolt sql-server` processes come from killed test harnesses, deleted
worktrees, and rogue auto-starts. The reaper (`cmd/gc/dolt_cleanup_*.go`)
is deliberately conservative — read its `--help`, which is the authoritative
contract. Verified highlights (2026-07-06):

- **Dry-run by default; `--force` to actually drop/purge/kill.**
- Port resolution follows the AD-04 chain: `--port` > city `dolt.port` >
  `<rigRoot>/.beads/dolt-server.port` > legacy 3307.
- A process is reaped only when its scope is **provably gone**: its
  `/proc/<pid>/cwd` readlink carries the kernel's `" (deleted)"` marker, or
  its `--config` path is on the test-path allowlist (`/tmp/Test*`,
  `os.TempDir()/Test*`, `~/.gotmp/Test*`, known test prefixes). A server
  whose config merely vanished but whose cwd is live is **protected, not
  reaped** (`3f57854fc` — the fix that made 300 previously untouchable
  zombies reapable while keeping live servers safe).
- Anything indeterminate degrades to protected, with the reason echoed in
  the report's PROTECTED section.
- `--json` emits stable schema `gc.dolt.cleanup.v1`. Automation MUST check
  `summary.errors_total` and MUST refuse `--force` when the dry-run's
  `force_blockers` is non-empty.

```bash
gc dolt-cleanup --json | jq '.summary'         # always dry-run first
gc dolt-cleanup --force --max-orphan-dbs 20    # bounded destructive pass
```

## 7. Storms: why you must not add polling against Dolt

Two landed fix classes define the pattern:

- **Thundering herd on restart** (`97e1ee426`): concurrent health checks +
  recovery attempts spawned a server storm when dolt bounced. Fix: a
  per-city semaphore serializing provider lifecycle operations + 0–2 s
  jitter on recovery.
- **Read storms** (`e8f2f4740`): maintenance reapers and jsonl-export
  hammering the server. The standing mitigation is
  `internal/beads/caching_store.go` (`CachingStore`): reads served from
  memory, external writes picked up via the bd hook → event bus path, and a
  background reconciler that full-scans only when the cache degrades.

If you are about to add a loop that queries Dolt on an interval, stop:
route reads through the store/cache layer, or gate the probe to the rare
event that needs it (the squatter probe in §5 is the model — it runs only
on ticks that could drain).

## 8. bd shell-out semantics (what BdStore actually guarantees)

From `internal/beads/bdstore.go` on main (2026-07-06):

- Every operation execs `bd` with a **120 s default timeout**
  (`bdCommandTimeout`; some read commands get per-command overrides via
  `bdCommandTimeoutFor`). Timeouts kill the whole process tree and
  propagate a real error.
- **Transient write conflicts are retried 3×** with backoff — bd surfaces
  Dolt serialization failures as `Error 1213 (40001): serialization
failure` / "this transaction conflicts with a committed transaction".
  Treat leftover 1213s in logs as contention signal, not corruption.
- **Batch writes are not atomic.** `SetMetadataBatch` is one `bd update`
  call but is documented "not truly atomic for external stores";
  `CloseAll` sets metadata per-bead, then batch-closes, then falls back to
  per-id closes on batch failure. A crash mid-batch leaves partial state.
  This is why the system doctrine is NDI (nondeterministic idempotence):
  design every store mutation to be safely re-runnable rather than assumed
  transactional.
- **Tracing**: set `GC_BD_TRACE=<file>` for line-per-invocation timing of
  every bd call (or `GC_BD_TRACE_JSON` for structured output). This is the
  fastest way to find which caller is storming the store.

Health monitoring that exists out of the box: the `dolt-health` order runs
`gc dolt health --json | gc dolt health-check` every 30 s and is
**diagnostic-only — it never restarts the server** (restart is an operator
decision via `gc dolt start`/`restart`); `dolt-remotes-patrol` runs
`gc dolt sync` every 15 m _when remotes are configured_. Whether remotes
exist is deployment-specific — as of 2026-07-06, origin/main's AGENTS.md
declares this repo's own Dolt LOCAL-ONLY (`bd dolt push`/`pull` fail there);
older checkouts' AGENTS.md still list `bd dolt push` in session completion.
Check YOUR checkout's AGENTS.md before syncing. Read-only recovery: `gc
dolt recover` write-probes via the `__gc_read_only_probe` table and calls
the provider's `recover` op (`cmd/gc/dolt_sql_health.go`,
`dolt_recover_managed.go`).

## 9. Version pins (volatile — dated 2026-07-06)

| Pin                                             | Value | Source                                                |
| ----------------------------------------------- | ----- | ----------------------------------------------------- |
| Minimum managed dolt (fail-closed doctor check) | 2.1.0 | `internal/doltversion/doltversion.go` `ManagedMin`    |
| CI + Docker image dolt                          | 2.1.7 | `deps.env` `DOLT_VERSION`, `.github/workflows/ci.yml` |

The 2.1.0 floor preserves the original guard against a dolt GC/writer
deadlock that hung sql-server under concurrent write load (fixed upstream
in dolthub/dolt PR #10876) — see `examples/bd/dolt/pack.toml`. Pre-release
dolt versions fail closed (`doltversion.ErrPreRelease`).

## 10. Testing the lifecycle

- **Fakes for everything else**: `GC_DOLT=skip` no-ops all dolt lifecycle
  in `cmd_init.go`/`cmd_start.go`/`cmd_stop.go` (TESTING.md env table) —
  testscript runs default to it. Do not require a live dolt in unit tests.
- **The chaos tier is the lifecycle's real test**: `make test-chaos-dolt`
  runs `TestManagedDoltChaos_CityAndRigCallersRemainConsistent`
  (`test/integration/dolt_managed_chaos_test.go`, build tags
  `integration chaos_dolt`, 45 m timeout). `GC_DOLT_CHAOS_DURATION`
  (default 2m) controls runtime; `GC_DOLT_CHAOS_SEED` replays a failing
  schedule. If you touch start/stop/recover paths, run it.
- Conformance against a real server: the beads conformance suite runs
  BdStore against real dolt + bd when installed, skips otherwise
  (TESTING.md). Leak hygiene helpers live in
  `cmd/gc/dolt_leak_helper_test.go`.

## 11. Open / candidate items (not settled — do not build on them)

- Upstream issue drafts about ghost-dolt reaping and database namespacing
  existed in the operator's workspace as **unfiled drafts** as of
  2026-07-06 (provisional; morning-ledger). Do not assume upstream dolt or
  bd behavior changes are coming.
- The Postgres and doltlite beads backends are live code but this skill
  documents neither; treat their ops story as unwritten.
- `gc dolt-cleanup`'s pack wrapper (`gc dolt cleanup`) and the Go core are
  converging ("can delegate … once feature parity lands" —
  `cmd/gc/cmd_dolt_cleanup.go`); prefer the Go-side `gc dolt-cleanup` for
  scripting because of its stable JSON envelope.

## Provenance and maintenance

Authored 2026-07-06 by the retiring-fellow distillation campaign. Facts
verified by direct reads of `origin/main` at `f828bbe4b` (2026-07-06);
where the local checkout (`58e0b8dbb`, 2026-05-11) disagreed, main won and
the drift is flagged inline. Incident narratives come from merged commit
messages (`60e402be9`, `9c2b6f564`, `3f57854fc`, `97e1ee426`,
`e8f2f4740`), which are permanent. Machine-local discovery sources
(non-load-bearing): the gas-city workspace's
`docs/design/fable-distillation/discovery-gascity.md` and
`morning-ledger-2026-07-07.md`.

Volatile facts and their re-verification one-liners (run from repo root):

| Claim                                 | Re-verify with                                                                                                                          |
| ------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| Minimum dolt version                  | `grep -n ManagedMin internal/doltversion/doltversion.go`                                                                                |
| CI/deps dolt pin                      | `grep -n DOLT_VERSION deps.env .github/workflows/ci.yml \| head -3`                                                                     |
| Dolt pack location + command set      | `ls examples/bd/dolt/commands/`                                                                                                         |
| Runtime layout paths + env overrides  | `sed -n '/func resolveManagedDoltRuntimeLayout/,/^}/p' cmd/gc/dolt_runtime_layout.go`                                                   |
| Port allocation chain                 | `sed -n '/func chooseManagedDoltPort/,/^}/p' cmd/gc/dolt_port_selection.go`                                                             |
| `[dolt]` config knobs                 | `grep -n -A4 "type DoltConfig struct" internal/config/config.go`                                                                        |
| Lock-release wait default (1m)        | `grep -n dolt_lock_release_timeout internal/config/config.go`                                                                           |
| Disk preflight floors                 | `grep -n "MinFreeBytes\|WarnFreeBytes" cmd/gc/dolt_disk_preflight.go`                                                                   |
| Squatter hold wiring                  | `grep -n "squatter on the managed port" cmd/gc/city_runtime.go`                                                                         |
| dolt-cleanup reap/protect contract    | `go run ./cmd/gc dolt-cleanup --help` (or read the `Long:` string in `cmd/gc/cmd_dolt_cleanup.go`)                                      |
| bd auto-start suppression + rationale | `grep -n -B3 BEADS_DOLT_AUTO_START cmd/gc/bd_env.go \| head`                                                                            |
| bd command timeout + 1213 retry       | `grep -n "bdCommandTimeout \|bdTransientWriteAttempts\|1213" internal/beads/bdstore.go \| head`                                         |
| Chaos tier target                     | `grep -n -A6 "^test-chaos-dolt:" Makefile`                                                                                              |
| Doctor dolt checks                    | `grep -n 'func (c \*Dolt.*Check) Name' internal/doctor/checks.go`                                                                       |
| dolt-health order is diagnostic-only  | `cat examples/bd/dolt/orders/dolt-health.toml`                                                                                          |
| Cited commits still on main           | `for c in 60e402be9 9c2b6f564 3f57854fc 97e1ee426 e8f2f4740; do git merge-base --is-ancestor $c origin/main && echo "$c on main"; done` |

If a re-verification fails, fix this file in the same change that moved the
code; a wrong runbook is worse than none.
