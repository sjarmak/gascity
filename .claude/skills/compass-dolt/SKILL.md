---
name: compass-dolt
description: Use when working on the shared dolt sql-server, the bead store, endpoint configuration in `.beads/config.yaml`, or recovering from dolt drift. Indexes canonical files for the dolt subsystem of this workspace.
---

# Compass: shared dolt sql-server

When working on dolt or the bead store:

- `.beads/dolt/.dolt/sql-server.info` — ground-truth `PID:PORT:UUID` written by dolt itself; trust over everything else
- `.beads/config.yaml` (and each rig's) — `gc.endpoint_origin` declares scope; under `managed_city`, rigs MUST NOT set `dolt.host` / `dolt.port` / `dolt.user` (that drift blocks supervisor init)
- `.gc/runtime/packs/dolt/dolt-state.json` — gc's view of the server (running / pid / port / data_dir); should match `sql-server.info`
- `docs/conventions/dolt-sql-server.md` — endpoint model table, ownership rules, sql-over-tcp recipe, recovery order

Hard rules: never run `bd dolt status|start|stop` here (`bd dolt status` will KILL the live server as a "drift recovery" side effect); never `dolt sql` inside `.beads/dolt/` while the server is up (LOCK contention will block or corrupt state). Query over TCP using the port from `sql-server.info`.

Diagnosing a dolt-CPU/slow-query symptom — which layer owns it? (adapted from Wldc4rd/gc-debug):

- **Volume problem** (many cheap queries, or a bloated table) → almost always **gc/bd** (too much work sent / store bloat). Confirm: `SHOW PROCESSLIST` full of fast repeats, high `Com_select`, store-tier bloat (`reference_bead_store_bloat_and_prune`, `reference_dolt_gc_never_runs`). Fix upstream in gc/bd or run the store prune / `DOLT_GC('--full')` (delete rows ≠ reclaim disk — GC is a separate step).
- **Per-query problem** (one query is expensive / mis-planned) → points **down** at **dolt / go-mysql-server (`dolthub/go-mysql-server`) / vitess**, which we do NOT own but can be the bug owner. Confirm with `EXPLAIN <query>` showing a full scan despite a usable index, and `SHOW INDEX FROM <table>` + populated stats. A plan/parser bug here is upstreamed to the dolthub repo, not patched in gc.
