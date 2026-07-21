# Shared dolt sql-server

Failure modes covered: data-dir LOCK contention from running `dolt sql` while the server is up, gc-managed server killed by foreign `bd dolt` calls, supervisor init blocked by drifted rig endpoint config.

## Ownership

One dolt sql-server serves the entire city. Owned by gc's runtime pack (`.gc/runtime/packs/dolt/`, driven by `.gc/system/bin/gc-beads-bd`), data_dir at `.beads/dolt/`. Comes up as part of runtime-pack rehydration when the supervisor boots.

DO NOT run `bd dolt start|stop|status` from this workspace — bd has no knowledge of the gc-managed server and `bd dolt status` will KILL the live server as a "drift recovery" side effect. Upstream tracking: gascity#506, #245, #323.

Port is deterministic — `gc-beads-bd` hashes it from the city path, so it's stable across restarts unless the workspace moves. Override by exporting `GC_DOLT_PORT` before the supervisor starts.

## Databases (one per scope on the shared server)

| Database            | Purpose                                            | Scope |
| ------------------- | -------------------------------------------------- | ----- |
| `gc`                | city sessions AND city work beads (`dr-*` prefix)  | city  |
| `codeprobe`         | codeprobe rig                                      | rig   |
| `CodeScaleBench`    | CodeScaleBench rig                                 | rig   |
| `EnterpriseBench`   | EnterpriseBench rig                                | rig   |
| `live_docs`         | live_docs rig                                      | rig   |
| `agent_diagnostics` | agent-diagnostics rig                              | rig   |
| `background_agents` | background-agents rig                              | rig   |
| `GEO`               | GEO rig                                            | rig   |
| `mcp_ax`            | mcp-ax rig                                         | rig   |
| `code_intel_digest` | code-intelligence-digest rig                       | rig   |

Adding a new rig: `gc rig add <path>` (or `--adopt` for existing `.beads/`). Any other path bypasses drift checks.

gc itself uses the FILE backend for its own session state (`city.toml [beads] provider = "file"`). Only `bd` commands hit the dolt server.

## Endpoint model

Each scope's `.beads/config.yaml` declares one endpoint origin via `gc.endpoint_origin`:

| Origin           | Who uses it                    | config.yaml requires                                                |
| ---------------- | ------------------------------ | ------------------------------------------------------------------- |
| `managed_city`   | the city itself                | no `dolt.host`/`dolt.port` — gc resolves from runtime pack          |
| `inherited_city` | a rig under a managed city     | no `dolt.host`/`dolt.port` — inherited from city endpoint           |
| `city_canonical` | city pinned to external dolt   | `dolt.host` + `dolt.port` required; must match external server      |

**Under managed_city, rigs must NOT set `dolt.host` / `dolt.port` / `dolt.user`.** Setting them is exactly the drift that blocks supervisor init with `canonical inherited rig config must mirror the city endpoint`.

## Canonical config.yaml shapes

City (`/home/ds/gas-city/.beads/config.yaml`):

```yaml
issue-prefix: dr
dolt.auto-start: false
no-db: true
dolt.auto-commit: batch
gc.endpoint_origin: managed_city
gc.endpoint_status: verified
```

Rig (`<rig-path>/.beads/config.yaml`):

```yaml
issue-prefix: <prefix>
dolt.auto-start: false
gc.endpoint_origin: inherited_city
gc.endpoint_status: verified
```

## Authoritative runtime state (three files, in order of trust)

| File                                     | Writer              | Purpose                                  |
| ---------------------------------------- | ------------------- | ---------------------------------------- |
| `.beads/dolt/.dolt/sql-server.info`      | dolt itself         | `PID:PORT:UUID` — ground truth           |
| `.gc/runtime/packs/dolt/dolt-state.json` | gc's runtime pack   | gc's view (running/pid/port/data_dir)    |
| each scope's `.beads/config.yaml`        | gc / `gc rig add`   | canonical endpoint origin                |

## Querying beads (always over TCP)

Never `dolt sql` inside `.beads/dolt/` while the server is up — the server holds the LOCK and the CLI will block or corrupt state.

```bash
PORT=$(cut -d: -f2 .beads/dolt/.dolt/sql-server.info)
dolt --host 127.0.0.1 --port "$PORT" --user root --no-tls --password '' \
  sql -q "USE gc; SELECT ..."
```

## Checking health

```bash
gc doctor                                    # authoritative health check
gc beads health                              # provider-level check with auto-recovery
cat .beads/dolt/.dolt/sql-server.info        # PID:PORT — ground truth
```

## Recovery

In order:

1. `gc doctor --fix` — reconciles most drift (types, split-stores, endpoint mirrors) automatically
2. `systemctl --user restart gascity-supervisor` — rehydrates runtime pack, brings dolt back on its deterministic port
3. True emergency where neither helps: full sequence in `tmux-supervisor.md`

DO NOT try to "fix" things by adding `dolt.port` / `dolt.host` to rig config.yamls. Under `managed_city`, rigs must not track the endpoint — gc does it for them.
