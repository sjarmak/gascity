# tmux + supervisor

Failure modes covered: chicken-and-egg between tmux server and supervisor reconciler, session-name uniqueness across closed beads, supervisor's tmux state cache going stale, supervisor stops that look external but were oomd collateral.

## tmux socket

The city uses a NAMED tmux socket derived from workspace name: `/tmp/tmux-1000/ds-research`. Not the default. All `gc` commands and the supervisor connect here.

- Check: `tmux -L ds-research list-sessions`
- Manually start placeholder: `tmux -L ds-research new-session -d -s placeholder "sleep infinity"`

## Chicken-and-egg with the reconciler

The supervisor's reconciler needs a running tmux server to create agent sessions but ALSO drains unrecognized tmux sessions as orphans. If tmux dies (all managed sessions close), the server disappears and the reconciler can't create new sessions.

Recovery when tmux is dead:

1. `tmux -L ds-research new-session -d -s placeholder "sleep infinity"`
2. `systemctl --user restart gascity-supervisor`
3. Wait ~30s for the city to boot (runs scale checks for all agents)
4. The reconciler drains the placeholder session as orphaned, but creates managed sessions first
5. If sessions get stuck in `creating` or `asleep` state: `gc session attach <id>` to force-start

Symptoms of a dead socket:

- `gc session new` errors with `tmux state cache: refresh failed ... no tmux server running`
- `gc status` shows agents `stopped` with same tmux errors
- `~/.gc/supervisor.log`: `tmux state cache: refresh failed`

## Session name collisions

Session names (e.g. `mayor`) must be unique across ALL beads, including closed ones. Stale beads in dolt block new session creation.

Diagnosis: supervisor log shows `session name already exists: "mayor" already belongs to gc-XXX`.

Fix — connect over TCP (never `dolt sql` in the data dir; see `dolt-sql-server.md`):

```bash
PORT=$(cut -d: -f2 .beads/dolt/.dolt/sql-server.info)
D="dolt --host 127.0.0.1 --port $PORT --user root --no-tls --password ''"
$D sql -q "USE gc; SELECT id, status, JSON_EXTRACT(metadata, '\$.session_name') AS sn FROM issues WHERE issue_type = 'session' AND JSON_EXTRACT(metadata, '\$.session_name') = 'mayor';"
$D sql -q "USE gc; UPDATE issues SET status = 'closed', metadata = JSON_SET(metadata, '\$.session_name', '', '\$.state', 'closed') WHERE id = '<offending-id>';"
```

Also remove stale `.gc/session-name-locks/*.lock` for the stuck name.

## Supervisor process

- systemd user unit: `gascity-supervisor.service`
- Log: `~/.gc/supervisor.log` (append-only, grows large)
- Status: `systemctl --user status gascity-supervisor`
- Restart: `systemctl --user restart gascity-supervisor`
- The supervisor caches tmux state at startup. If tmux wasn't running then, it won't find sessions until restarted.
- `gc start|stop|restart` manage registration with the supervisor, not the process itself. Use `systemctl` for the process.

## Mysterious supervisor stops

`/tmp/supervisor-stop-caller.log` captures the process tree at any future supervisor stop (written by the `ExecStopPost` catcher in the `stop-catcher.conf` drop-in). Empty file = no stops since install. If populated and shows an oomd-killed scope under `user@1000`, move that workload into `scix-batch` (see `capacity.md`).

## Full recovery playbook (everything broken)

```bash
# 1. Stop supervisor
systemctl --user stop gascity-supervisor

# 2. Start tmux on the correct socket
tmux -L ds-research new-session -d -s placeholder "sleep infinity"

# 3. Clear any session-name locks
rm -f .gc/session-name-locks/*.lock

# 4. Start supervisor (tmux must be running; dolt comes up with it)
systemctl --user start gascity-supervisor
sleep 30

# 5. Check for zombie session beads (server is up now)
PORT=$(cut -d: -f2 .beads/dolt/.dolt/sql-server.info)
dolt --host 127.0.0.1 --port "$PORT" --user root --no-tls --password '' sql \
  -q "USE gc; SELECT id, status, JSON_EXTRACT(metadata, '\$.session_name') AS sn FROM issues WHERE issue_type = 'session' AND status != 'closed' AND JSON_EXTRACT(metadata, '\$.session_name') != '';"
# If any found: close them per "Session name collisions" above, then restart supervisor once more.

# 6. Verify
gc session list
```
