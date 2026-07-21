---
name: cityops-dolt-beads-reference
description: >-
  Operate this install's bead storage: read/write beads with bd, resolve
  the real dolt sql-server, recover from 'unreachable at 127.0.0.1:0',
  triage leaked/ghost dolt processes, the file-vs-dolt dual backend and gc
  bd block, and the store's GC/prune/backup lifecycle. Not endpoint-config
  drift or recovery sequences (compass-dolt).
---

# City-ops reference: dolt & beads on ds-research

Machine-local reference for the bead storage layer of the Gas City workspace at
`/home/ds/gas-city` on this host. Every command, port, PID, and size below was
verified live on 2026-07-06. This skill owns the facts that have no other home:
the dual-backend split, bd port resolution in interactive shells, canonical-vs-
leaked server triage, and the storage lifecycle. It does not restate what its
owners already document — it points at them.

## When NOT to use this skill

| You need                                                         | Go to instead                                                    |
| ---------------------------------------------------------------- | ---------------------------------------------------------------- |
| Endpoint model, `gc.endpoint_origin` shapes, dolt recovery order | `docs/conventions/dolt-sql-server.md` (via `compass-dolt`)       |
| Diagnosing dolt CPU / slow queries (which layer owns the bug)    | `compass-dolt`                                                   |
| Supervisor restart, tmux-dead recovery                           | `compass-tmux-supervisor`, `docs/conventions/tmux-supervisor.md` |
| Slinging/claiming/closing beads                                  | `gc-work`, `gc-dispatch` skills; `compass-bead-dispatch`         |
| The Don't/Do lists (lethal commands, TCP SQL recipe)             | `/home/ds/gas-city/CLAUDE.md` — authoritative                    |
| Guest-session conduct (don't touch the queue unasked)            | `docs/conventions/guest-session-primer.md`                       |
| Incident deep-dives (ghost dolt, supervisor OOM)                 | sibling `cityops-failure-archaeology`                            |

Preconditions, owned by CLAUDE.md's Don't list, restated here only as a gate:
never `bd dolt start|stop|status` (status KILLS the live server), never raw
`dolt sql` inside `.beads/dolt/` while the server is up, never add
`dolt.host/port/user` to a rig's `.beads/config.yaml`. Everything below assumes
you obey those.

## 1. The dual backend — why `gc bd` refuses to run

Two bead stores coexist in this one workspace, and they are NOT views of the
same data path:

| Backend  | Consumer                                                                           | Storage          | Size (2026-07-06)                                                         |
| -------- | ---------------------------------------------------------------------------------- | ---------------- | ------------------------------------------------------------------------- |
| **file** | the `gc` CLI's own sessions/work (`city.toml [beads] provider = "file"`, line 377) | `.gc/beads.json` | 150.4 MB                                                                  |
| **dolt** | `bd` and every rig bead store; one shared sql-server for the whole city            | `.beads/dolt/`   | ~4.0 GB data dir (2026-07-07; pre-GC volatile — re-measure with `du -sh`) |

Consequences:

- `gc bd <anything>` is hard-blocked. Verified error:
  `gc bd: only supported for bd-backed beads providers (resolved "file" for /home/ds/gas-city)`.
  The error's hint points you at `city.toml [beads].provider`. **Do not "fix"
  this by flipping the provider** — the file backend is the deliberate,
  load-bearing choice for gc's own session state. Use `bd` directly (section 3)
  or `gc beads list/show`.
- `gc beads health` is the cheap provider-level check (returns
  `Beads provider: healthy` in ~1s). `gc beads list` enumerates the file
  backend and **can exceed 45s under load** (timed out in the 2026-07-06
  verification pass, during a paused-maintenance high-load day). Prefer `bd`
  with the port workaround for bead reads.

## 2. Which dolt server is the real one

Canonical server as of 2026-07-06: **PID 1467571, port 29620**, config
`/home/ds/gas-city/.gc/runtime/packs/dolt/dolt-config.yaml`, up since
2026-07-03, ~850 MB RSS, serving 27 databases (`gc` holds the city's `dr-*`
beads; one database per rig). These values are volatile — re-derive, never
paste from memory:

```bash
cat /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info   # PID:PORT:UUID — ground truth
cat /home/ds/gas-city/.gc/runtime/packs/dolt/dolt-state.json  # gc's view; must agree
```

Trust order and known liars:

1. `sql-server.info` (written by dolt itself) — authoritative.
2. `dolt-state.json` — gc's view; verified in agreement today, but it has been
   poisoned before (section 5).
3. `.beads/dolt-server.pid` — **STALE, do not trust.** It reads `4663`;
   `/proc/4663` does not exist (verified 2026-07-06). The real PID is in
   `sql-server.info`.

### Leaked servers are running right now

Seven non-canonical `dolt sql-server` processes were live on 2026-07-06
alongside the real one: five from polecat test worktrees
(`/home/ds/gascity-worktrees/polecat-{1,3,4,5,6}/.gc/runtime/packs/dolt/`),
one from a `/tmp/city` test config, and one serving `~/.beads` (the home-dir
bd store — separate concern, not the city's server). Per the 2026-07-07
morning ledger this inventory awaits Stephanie's cleanup go-ahead
(provisional) — **do not kill any of them on your own initiative.**

Triage a dolt process before believing (or touching) it:

```bash
for p in $(pgrep -x dolt); do
  echo "PID $p: $(tr '\0' ' ' </proc/$p/cmdline) CWD=$(readlink /proc/$p/cwd 2>/dev/null)"
done
```

The canonical server is the one whose `--config` path starts with
`/home/ds/gas-city/` AND whose PID matches `sql-server.info`. Anything else is
a leak or a ghost. Two symmetric traps (discovery report §7):

- Killing "extra dolt processes" without the config-path check can hit the
  real server.
- Trusting any dolt on localhost risks stale-snapshot reads — a ghost with
  deleted-inode FDs will happily answer queries with days-old data
  (section 5).

## 3. Reading and writing beads reliably

### The 127.0.0.1:0 failure, live in every interactive shell

Bare `bd` in a fresh shell here fails. Reproduced 2026-07-06:

```
$ bd list --limit 1
Error: failed to open database: Dolt server unreachable at 127.0.0.1:0: dial tcp 127.0.0.1:0: connect: connection refused

Dolt server auto-start is disabled (dolt.auto-start: false).
Start the server manually:
  bd dolt start
```

**The error message's suggested fix is the single most dangerous command in
this workspace.** `bd dolt start` (like `bd dolt status`) does not know about
the gc-managed server and will kill/replace it. Never follow that hint.

Why it happens: bd resolves the dolt port from environment, and only the
supervisor's environment carries it (via the `10-dolt-port.conf` systemd
drop-in, a stopgap for upstream bug gc-74rxa: the supervisor exports
`GC_DOLT_PORT` only when it _starts_ dolt, not when it _adopts_ a surviving
one after restart). Your interactive shell has no such export.

The fix — derive the port from ground truth (both env names verified working
2026-07-06 with `bd version 1.1.0`):

```bash
PORT=$(cut -d: -f2 /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info)
BEADS_DOLT_SERVER_PORT=$PORT bd list --limit 5    # BEADS_DOLT_PORT also works
```

This is the reliable read/write path for all bd operations in ad-hoc sessions.
(Raw SQL over TCP: recipe in CLAUDE.md's Do list; endpoint doctrine in
`docs/conventions/dolt-sql-server.md`.)

### Live database inventory beats the docs table

The database table in `docs/conventions/dolt-sql-server.md` is stale (10 rigs
listed; the live server had 27 databases on 2026-07-06, including `aoa`,
`brains`, `dec`, `mem`, `gpk`, `gascity`, `gascity_dashboard`,
`migration_evals`). Enumerate live instead of trusting the doc:

```bash
PORT=$(cut -d: -f2 /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info)
dolt --host 127.0.0.1 --port "$PORT" --user root --no-tls --password '' sql -q 'SHOW DATABASES;'
```

## 4. Systemd drop-ins that keep this layer alive

All under `~/.config/systemd/user/gascity-supervisor.service.d/`. Two are
dolt-load-bearing; read the file comments before touching (they are the
documentation of record):

| Drop-in                  | What it does                                          | Why                                                                                                                                                                                                                                                                                      |
| ------------------------ | ----------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `10-dolt-port.conf`      | `GC_DOLT_PORT=29620` + `BEADS_DOLT_SERVER_PORT=29620` | gc-74rxa stopgap: without it, post-restart adoption leaves `bd --readonly` at 127.0.0.1:0 and **all order-firing freezes**                                                                                                                                                               |
| `dolt-wait-timeout.conf` | `GC_DOLT_WAIT_TIMEOUT=3700`                           | beads v1.0.5 pool sets ConnMaxLifetime=1h with no idle timeout or bad-conn retry; 30s server-side reaping caused "unexpected EOF / invalid connection" on sling reads. 3700s keeps the server from reaping before the client retires the conn. INTERIM; durable fix is upstream in beads |

Related, in `city.toml [dolt]`: `read_timeout_millis = 60000` (raised from the
15s managed default after cross-store enumeration across ~20 rigs tripped
`net_read_timeout` mid-query, RCA 2026-06-21 — the comment above it in
city.toml is the changelog entry). If the hardcoded port in `10-dolt-port.conf`
ever disagrees with `sql-server.info`, the drop-in is the stale one — the port
is deterministic (hashed from the city path) but not immutable.

## 5. Worked example: the poisoned dolt-state.json (evidence preserved on host)

The poisoned file is still there, kept deliberately as evidence:

```bash
$ cat /home/ds/gas-city/.gc/runtime/packs/dolt/dolt-state.json.POISONED-TEST-ARTIFACT-APR09
{"running":false,"pid":0,"port":22246,"data_dir":"/tmp/TestGcBeadsBdStartUsesRootBeadsDataDir662800604/001/.beads/dolt","started_at":"2026-04-09T07:04:56Z"}
```

What happened (full forensics in `docs/upstream-issue-draft-ghost-dolt.md`):
a Go test leaked this artifact into the production runtime state dir.
`verify_our_server()` in `gc-beads-bd` compared its `data_dir` (a dead /tmp
path) against the real beads path, mismatched, and made `gc dolt status`
report "not running" against a healthy server. The dolt-health pattern
`gc dolt status || gc dolt start` then walked `op_start` to within one step of
`kill -9` on the production server: real PID from the pid file + phantom port
22246 from the poisoned state + nothing listening there = "stale server, kill
it." Separately, a ghost server on port 44799 was found serving a 4-day-stale
snapshot (1,844 issues vs 5,669 live) from deleted-inode FDs, appending to the
same `dolt-server.log` as the real server.

Operating lessons this encodes:

1. A state file can lie while the server is healthy — cross-check
   `dolt-state.json` against `sql-server.info` before acting on either.
2. "Status says down" + "start it" is a kill chain, not a recovery. On this
   host, recovery is `gc doctor --fix` then
   `systemctl --user restart gascity-supervisor` (owned by
   `docs/conventions/dolt-sql-server.md` §Recovery), never a manual dolt start.
3. A `data_dir` pointing into `/tmp/` in any dolt state file = quarantine the
   file (rename, don't delete — it's evidence), then re-probe.

## 6. Storage lifecycle: GC, prune, backups, rotation

Dolt auto-GC is **off** by design here; the lifecycle is order-driven. All of
it runs with "spot-check recommended" status — per the provisional trust-map
position (morning ledger 2026-07-07), no subsystem is documented as
trusted-unsupervised without Stephanie's word.

| Mechanism             | Schedule               | What it does                                                                                                                                                                                                                                             | Home                              |
| --------------------- | ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------- |
| `dolt-gc-maintenance` | cron `30 4 * * *`      | `CALL DOLT_GC()` across all managed databases. Exists because the assumed "compaction-driven scheduled GC" was never wired up; garbage grew to 2.4 GB and slowed teardown past the systemd stop timeout (2026-05-25 SIGKILL incident)                    | `orders/dolt-gc-maintenance.toml` |
| `bead-prune-reaper`   | cron `0 5 * * 0` (Sun) | Prunes closed non-ephemeral beads >30d via `bd prune`; skips pinned/open/ephemeral. Pruned beads stay recoverable via dolt history (`bd restore`); this order **never flattens** — flatten is a separate, gated step. First run 2026-06-03 pruned 17,010 | `orders/bead-prune-reaper.toml`   |
| `janitor-log-rotate`  | cooldown 6h            | Copy+truncate+gzip logs >32 MB, keep 5 archives; covers `dolt-server.log` among others. `JANITOR_LOG_EXECUTE=1` flipped by Stephanie 2026-07-04 after dry-run review — the dry-run→apply promotion pattern                                               | `orders/janitor-log-rotate.toml`  |

Disk footprint to know about (all sizes 2026-07-06):

- `.beads/backup/` — 2.0 GB: 98 entries of dolt `.darc` archives plus an empty
  `oldgen/`. No script in `bin/`, `orders/`, or `scripts/` references it;
  writer provenance is undocumented. Treat as do-not-delete without
  Stephanie's decision.
- `.gc/beads.json.bak-janitor-*` — ~990 MB across 7 janitor backup copies of
  the 150 MB file backend (plus one unrelated ~480 KB `.gc/beads.json.bak`).
  Accumulating; cleanup is a pending decision, not yours to take.
- `.beads/dolt-server.log.old-20260413` — 167.5 MB, the pre-rotation blowout
  that motivated janitor-log-rotate. `dolt-server.log.1` is 56 MB.

Remember from the ghost-dolt incident: `dolt-server.log` lines are **not
attributable to a specific server process** — a ghost and the real server
append to the same file.

## Provenance and maintenance

Authored 2026-07-06 by the retiring Fable fellow (fable-sunset program,
dr-i4v). Sources: live verification on this host, `discovery-cityops.md` §1/§3/§7,
`morning-ledger-2026-07-07.md` (provisional positions marked inline),
`docs/upstream-issue-draft-ghost-dolt.md`, order files, systemd drop-ins.

One-line re-verification per drift-prone claim (all read-only):

| Claim                                       | Re-verify with                                                                                                                   |
| ------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| Canonical PID/port (1467571:29620)          | `cat /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info`                                                                        |
| dolt-state.json agrees                      | `cat /home/ds/gas-city/.gc/runtime/packs/dolt/dolt-state.json`                                                                   |
| pid file still stale (4663)                 | `cat /home/ds/gas-city/.beads/dolt-server.pid; ls /proc/$(cat /home/ds/gas-city/.beads/dolt-server.pid) 2>&1`                    |
| Leaked-server inventory                     | `for p in $(pgrep -x dolt); do echo "$p $(tr '\0' ' ' </proc/$p/cmdline)"; done`                                                 |
| `gc bd` still blocked / provider still file | `grep -A1 '^\[beads\]' /home/ds/gas-city/city.toml`                                                                              |
| bd env workaround still works               | `PORT=$(cut -d: -f2 /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info); BEADS_DOLT_SERVER_PORT=$PORT bd list --limit 1`        |
| Drop-in port matches ground truth           | `grep GC_DOLT_PORT ~/.config/systemd/user/gascity-supervisor.service.d/10-dolt-port.conf`                                        |
| Database count/inventory                    | `SHOW DATABASES;` over TCP (recipe in §3)                                                                                        |
| Sizes (beads.json, baks, backup/, logs)     | `ls -lh /home/ds/gas-city/.gc/beads.json*; du -sh /home/ds/gas-city/.beads/backup; ls -lh /home/ds/gas-city/.beads/ \| grep log` |
| GC/prune/rotate orders still enabled        | `grep -L 'enabled *= *false' /home/ds/gas-city/orders/{dolt-gc-maintenance,bead-prune-reaper,janitor-log-rotate}.toml`           |
| Poisoned artifact still preserved           | `ls /home/ds/gas-city/.gc/runtime/packs/dolt/dolt-state.json.POISONED-TEST-ARTIFACT-APR09`                                       |
