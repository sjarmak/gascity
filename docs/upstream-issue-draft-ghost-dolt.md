# Issue Draft: Orphaned dolt sql-server holding deleted inodes serves stale snapshot silently

**Repo:** gastownhall/gascity (and related: gastownhall/beads)
**Component:** gc-beads-bd / dolt lifecycle management
**Severity:** Data integrity — silent stale reads, potential write loss

## Summary

A dolt sql-server process can become orphaned and continue serving a stale snapshot from deleted-inode file descriptors after its on-disk database files have been rewritten by a subsequent `gc dolt start` or `bd init`. Clients connecting to the orphan receive stale reads and any writes they make will vanish when the orphan process dies. This is distinct from the known "phantom database crashes server on startup" pattern handled by `quarantine_phantom_dbs()`.

## Reproduction

Observed on a single-machine deployment (ds-research city) running gc v0.13.5, bd v0.63.3, 16 rigs, shared dolt sql-server on port 43677.

### What was found

Three `dolt sql-server` processes running simultaneously:

| PID     | Port  | cwd                     | State                                                                                     |
| ------- | ----- | ----------------------- | ----------------------------------------------------------------------------------------- |
| 3275503 | 43677 | `.beads/dolt`           | **Canonical** (matches `.beads/dolt-server.pid`)                                          |
| 2462687 | 44799 | `.beads/dolt`           | **Ghost** — `lsof` shows `gc/.dolt/noms/{LOCK,vvvv...,journal.idx,.darc}` all `(deleted)` |
| 3270681 | 43678 | `.beads/dolt (deleted)` | **Orphan** — cwd itself is unlinked                                                       |

All three had PPID=1 (reparented to init via tmux-spawn scopes, outside the supervisor cgroup).

### Querying the ghost directly

```
$ dolt --host 127.0.0.1 --port 44799 --user root --password "" --no-tls sql -q "USE gc; SELECT COUNT(*) FROM issues;"
1844   ← frozen at Apr 7 17:52 snapshot

$ dolt --host 127.0.0.1 --port 43677 --user root --password "" --no-tls sql -q "USE gc; SELECT COUNT(*) FROM issues;"
5669   ← current, live data
```

The ghost reports 3 fewer databases (missing `codeprobe`, `live_docs`) and a 4-day-stale gc dataset. It accepts connections, serves `SHOW DATABASES`, and would accept writes that would evaporate on process death. Both the ghost and the real server append to the **same** `dolt-server.log` file, making it impossible to attribute log entries to a specific server.

### Forensic diff

Full ID comparison: 0 issues on the ghost that aren't on the real server. 0 cases where the ghost has a newer `updated_at` than the real server for any shared ID. **No data loss was detected in this specific instance** because dolt's file-level LOCK apparently prevented concurrent mutations during the overlap window. However, the stale-read hazard was live and any client that cached port 44799 would have silently gotten Apr 7 data.

## Root cause analysis

### How ghosts form

1. A `gc dolt start` or `bd` auto-start spawns a `dolt sql-server` inside a tmux pane.
2. The parent process (gc/bd command) exits, leaving dolt reparented to init via the tmux-spawn systemd scope.
3. Later, `gc dolt start` fires again (e.g., from the dolt-health order or manual invocation).
4. `op_start()` in `gc-beads-bd` calls `find_dolt_pid()`, which:
   - Checks `.beads/dolt-server.pid` — if stale (dead PID), falls through.
   - Checks `find_port_holder` (lsof on port) — may find the wrong process.
   - Falls back to ps-grep with `--config` or `--data-dir` flags — misses servers started without those flags.
5. `op_start()` starts a **new** dolt sql-server with a new config file.
6. The new server opens the same `.beads/dolt/` directory, which causes dolt to create new noms storage files.
7. The old server's open FDs now point at deleted inodes.
8. `.beads/dolt-server.{pid,port}` are updated to point at the new server.
9. The old server remains listening on its original port with no pointer file referencing it.

### Amplifying factor: poisoned dolt-state.json

A separate bug causes `gc-beads-bd probe` / `verify_our_server` to reject the real running server. On our system, `.gc/runtime/packs/dolt/dolt-state.json` contained:

```json
{
  "running": false,
  "pid": 0,
  "port": 22246,
  "data_dir": "/tmp/TestGcBeadsBdStartUsesRootBeadsDataDir662800604/001/.beads/dolt",
  "started_at": "2026-04-09T07:04:56Z"
}
```

A Go test artifact leaked into the production runtime state directory. `verify_our_server()` reads `data_dir` from this file and compares it to the real beads path — mismatch causes `return 1 → exit 2`, making `gc dolt status` report "not running" despite a healthy server on port 43677.

If `gc dolt start` is subsequently invoked (e.g., by the dolt-health order: `gc dolt status >/dev/null 2>&1 || gc dolt start`), `op_start` at lines 730–749 would:

1. `find_dolt_pid` → returns the real PID from `.beads/dolt-server.pid`
2. `load_state_field port` → returns 22246 (from the poisoned state file)
3. `tcp_check_port 22246` → false (nothing listening there)
4. **`kill -9 $existing_pid`** → SIGKILLs the real healthy server
5. Starts a new server, writing fresh state

Reproduced live:

```
$ GC_CITY_PATH=/home/ds/gas-city gc-beads-bd probe  # with poisoned file
exit=2

$ mv dolt-state.json dolt-state.json.QUARANTINED
$ GC_CITY_PATH=/home/ds/gas-city gc-beads-bd probe  # without
exit=0

$ mv dolt-state.json.QUARANTINED dolt-state.json    # restore
$ GC_CITY_PATH=/home/ds/gas-city gc-beads-bd probe
exit=2
```

## Existing mitigations that don't cover this case

- `quarantine_phantom_dbs()` — handles missing noms/manifest on startup. Does NOT handle a ghost server holding deleted noms files while already running.
- `sync_port_files()` — propagates canonical port to rig `.beads/` dirs. Does NOT detect or kill ghost servers on other ports.
- `dolt.auto-start: false` — prevents bd from spawning new servers. Does NOT clean up existing orphans.
- `mol-dog-phantom-db` / `mol-dog-stale-db` orders — detect and clean orphan databases. But they're opt-in, require the dog pool, and target database directories not running processes.
- `gc dolt cleanup` — removes orphaned database dirs. Does NOT kill orphan processes.

## Suggested fixes

1. **Port-scan on startup.** Before starting a new server, `op_start` should scan for ANY `dolt sql-server` process with the same `--data-dir` or cwd, not just the one in the PID file. Kill or adopt it before starting fresh.

2. **Process-level orphan detector.** Complement the existing database-level `gc dolt cleanup` with a process-level scanner that:
   - Enumerates all `dolt sql-server` processes owned by the current user
   - Checks each against `.beads/dolt-server.pid` across ALL rigs
   - Kills any process not referenced by any pid file
   - Runs as a periodic order (like mol-dog-phantom-db but for processes)

3. **Validate dolt-state.json before trusting it.** `verify_our_server` should sanity-check the `data_dir` field — if it points at a non-existent path (especially `/tmp/`), discard the state file rather than rejecting a live healthy server.

4. **Test isolation for dolt-state.json.** `TestGcBeadsBdStartUsesRootBeadsDataDir` (or something in its call chain) wrote to the production runtime state directory. Either:
   - The test should write to `$cityPath/.gc/runtime/` (which is the tempdir), or
   - `save_state()` should be patched to use the city-scoped runtime dir rather than a potentially env-leaked global path.

5. **Per-server identity in log output.** Both the ghost and real server appended to the same `dolt-server.log` with no differentiator. Adding a server UUID or PID prefix to each log line would make forensic attribution possible.

## Environment

- gc v0.13.5 (binary mtime 2026-04-09 08:03 EDT)
- bd v0.63.3 (binary mtime 2026-03-30 12:02 EDT)
- dolt (path: /home/ds/.local/bin/dolt)
- Source: gastownhall/gascity@894861ac (origin/main) + 2 local test commits on `fix/flaky-idle-probe-intra-tick-race`
- Ubuntu Linux 6.17.0-19-generic, systemd-oomd active
- City: ds-research, 16 rigs registered, shared dolt sql-server

## Related

- Upstream beads refs in gc-beads-bd: 4eaca225, b97a04ea, gt-sv1h, f379e3b3, 38f7b380
- Supervisor OOM (7.5 GB peak, 5× systemd-oomd kills on Apr 10) — likely correlated via caching-store reconciler retrying bd operations against broken rigs
- Deepsearch conversation: https://demo.sourcegraph.com/deepsearch/786aeabe-8014-44b7-9b72-9bba92e3cf12
