---
name: cityops-debugging-playbook
description: >-
  Something in this ds-research install is broken NOW: orders not firing,
  supervisor dead/looping, OOM, beads queuing behind idle workers, wedged
  dispatcher, dolt 127.0.0.1:0 errors, gc hangs. Symptom-triage table,
  recovery ladder, and fixes with no other doc home. For subsystem indexes
  use compass-*; for RCA method use mechanic.
---

# Cityops debugging playbook

Runbook for recovering the live ds-research city at `/home/ds/gas-city` when
something is broken. It routes symptoms to owners, walks the recovery ladder
rung by rung, and holds the handful of fixes no other doc owns.

**Authority scope.** The fixes below mutate live infrastructure (killing
sessions, restarting the supervisor, reaping worktrees). They assume you are
operating the city with Stephanie's or the mayor's authority. In an ad-hoc
guest session, run the diagnosis and report; act only when asked
(`docs/conventions/guest-session-primer.md`).

**Hard rules first.** Before running anything here, read
`/home/ds/gas-city/CLAUDE.md` §Don't and the `mechanic` skill's safety floor.
The single most dangerous read-looking command in this workspace is
`bd dolt status` — it kills the production dolt server. Nothing in this
playbook ever requires it.

## When NOT to use this skill

| You actually want                                                      | Go to                                                                                                                           |
| ---------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| The diagnostic METHOD (trace from source, RCA discipline, CPU-vs-load) | `mechanic` skill                                                                                                                |
| File index for one subsystem                                           | `compass-dolt`, `compass-tmux-supervisor`, `compass-scanners`, `compass-capacity`, `compass-gc-binary`, `compass-bead-dispatch` |
| The full tmux/supervisor reset recipe, session-name collisions         | `docs/conventions/tmux-supervisor.md`                                                                                           |
| Dolt endpoint model, SQL-over-TCP recipe                               | `docs/conventions/dolt-sql-server.md`                                                                                           |
| Sling flags, claim handoff, `--reassign`/`--nudge`                     | `docs/conventions/bead-dispatch.md`                                                                                             |
| Account quota, rate-limit failover, scix-batch                         | `docs/conventions/capacity.md`                                                                                                  |
| Past-incident catalog with detection signatures (not live recovery)    | `cityops-failure-archaeology` (departure-library sibling)                                                                       |
| Making a config change safely after recovery                           | `cityops-city-change-control` (departure-library sibling)                                                                       |
| Routine health monitoring                                              | already automated — the orders/reaper surface (`compass-scanners`); don't hand-roll a sweep                                     |

## First move: snapshot before touching anything

```bash
/home/ds/gas-city/.claude/skills/cityops-debugging-playbook/scripts/triage.sh
```

Strictly read-only (verified 2026-07-07). One screen: supervisor state and
OOM-kill count, dolt ground truth (pid/port/TCP probe), every dolt process on
the host, tmux/session states, load and known runaway classes, supervisor-log
tail. Capture this BEFORE any fix so the before/after is comparable, and so a
wrong theory can be falsified later.

## Symptom → first move

| Symptom                                                                          | First check                                                                                                    | Owner of the fix                                                        |
| -------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| Orders not firing at all                                                         | Did the supervisor restart recently? (`triage.sh`) → suspect the 127.0.0.1:0 adopt-path bug                    | Worked incident 3 below                                                 |
| Orders fire but routed beads never convert to work                               | `gc session list \| grep control-dispatcher`                                                                   | §Wedged control-dispatcher below                                        |
| `gc session new` fails `tmux state cache: refresh failed` / agents all `stopped` | `tmux -L ds-research list-sessions`                                                                            | `docs/conventions/tmux-supervisor.md` (chicken-and-egg)                 |
| Supervisor log: `session name already exists`                                    | stale session bead / lock                                                                                      | `docs/conventions/tmux-supervisor.md` §Session name collisions          |
| Supervisor dead or flapping                                                      | `journalctl --user -u gascity-supervisor \| grep -iE "oom\|Failed" \| tail`; `/tmp/supervisor-stop-caller.log` | §Supervisor OOM-kill loop below; stop-caller lore in tmux-supervisor.md |
| bd errors, connection refused, or dolt at port 0                                 | `cat .beads/dolt/.dolt/sql-server.info` (run from `/home/ds/gas-city`)                                         | §Which dolt server is real below; `compass-dolt`                        |
| Beads queue behind a pool worker that looks active but idle                      | LAST ACTIVE column in `gc session list`                                                                        | §Wedged pool slot below                                                 |
| Bead slung but sits unclaimed                                                    | missing `--reassign` / `--nudge`                                                                               | `docs/conventions/bead-dispatch.md`                                     |
| Disk filling, dozens of stale worktrees                                          | which population?                                                                                              | §Two worktree populations below                                         |
| Load pegged / everything slow                                                    | `mechanic` §CPU-vs-load to classify, then `pgrep -af "gc nudge poll"`                                          | Worked incident 1 below (runaway classes)                               |
| Agent rate-limited                                                               | —                                                                                                              | `CLAUDE.md` §Do (`gc-capacity --rebalance`), `compass-capacity`         |
| `gc doctor` / `gc order check` itself hangs                                      | that IS the finding                                                                                            | §gc commands slow or hanging below                                      |

## The recovery ladder

Climb one rung at a time; verify after each rung; stop climbing the moment the
city is healthy. Jumping straight to a supervisor restart destroys the
evidence a lower rung would have produced.

**Rung 0 — snapshot.** `triage.sh` (above). Read it. Most incidents are
diagnosable from this screen alone.

**Rung 1 — `gc doctor`** (read-only diagnosis). Run it as
`timeout 300 gc doctor` — on 2026-07-07, minutes after an OOM restart with
load ~50, it exceeded 120s. A timeout here is itself a finding: the supervisor
is overloaded; go hunt runaways (worked incident 1) before anything else.

**Rung 2 — `gc doctor --fix`.** Framework-owned repairs, including
re-provisioning broken framework pool worktrees under `.gc/worktrees/`.

**Rung 3 — targeted named fix.** One of the §Named fixes below, or the owning
convention doc from the triage table. Prefer this over a blanket restart: a
restart clears symptoms and evidence together.

**Rung 4 — `systemctl --user restart gascity-supervisor`.** Facts that make
this safer than it sounds (verified 2026-07-07):

- `KillMode=process`: the dolt server and tmux sessions survive. The canonical
  dolt (started 2026-07-03) lived through six supervisor OOM kills on
  2026-07-06 without a blip.
- The `orphan-guard.conf` drop-in (`ExecStartPre` pkill of a stale
  lock-holding `gc supervisor run` child) prevents the historical 400+-restart
  flap; you do not need to clear `controller.lock` by hand.
- tmux MUST be alive on socket `ds-research` first — if it is dead, use the
  placeholder recipe in `CLAUDE.md` §Do, or you convert an outage into a
  drain-everything incident.
- Every restart takes the dolt ADOPT path, which is exactly the gc-74rxa trap
  (worked incident 3). The `10-dolt-port.conf` drop-in covers it; after any
  restart, confirm order firing resumes.
- Wait ~30s before judging: the supervisor runs scale checks for all agents on
  boot.

**Rung 5 — full reset.** Everything broken (tmux dead AND supervisor confused
AND stale session beads): follow `docs/conventions/tmux-supervisor.md` §Full
recovery playbook verbatim. Do not improvise the ordering — supervisor before
tmux drains every session as an orphan.

**Verify after any rung:**

```bash
gc session list                      # expected sessions active, none stuck in creating
tail -20 ~/.gc/supervisor.log        # "beads cache: reconciled rig=..." lines flowing
PORT=$(cut -d: -f2 /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info)
dolt --host 127.0.0.1 --port "$PORT" --user root --no-tls --password '' sql -q "SELECT 1;"
timeout 120 gc order check | head    # due/not-due enumeration works again
```

## Named fixes (no other doc home)

### Wedged control-dispatcher

The city-level `core.control-dispatcher` singleton recurrently wedges: session
stays `active` but stops converting routed beads; routed-bead nudges loop
without landing (observed 3x on 2026-06-13 alone). The non-obvious part:
**`gc session reset` and `gc session wake` silently NO-OP** because the wedge
trips the named-session circuit breaker. Only a force-kill clears the breaker:

```bash
gc session list | grep control-dispatcher     # get the session id
gc session kill <id>    # force-kills runtime, bead stays active, reconciler respawns fresh
```

`bin/dispatcher-watchdog` (dispatched as an order) automates this with two
OR'd wedge signals. It has worked in practice but is not Stephanie-certified
trusted-unsupervised (provisional trust-map position, morning ledger
2026-07-07): after any dispatcher incident, verify by hand that routed beads
convert again rather than assuming the watchdog caught it.

### Wedged pool slot

A pool worker that ends its bead with `exit 0` instead of
`gc session close "$GC_SESSION_ID"` leaves the Claude agent parked at the
editor prompt. The reconciler reads the session as `active,
last_activity=Nm ago` indefinitely and new beads queue behind the dead slot
(contract details: `docs/conventions/recurring-task-example.md`).

Detect: `gc session list` shows the slot `active` with LAST ACTIVE far older
than its pool's normal cadence, while beads sit routed to that pool. Confirm
before acting — `gc session peek <id>` shows what it is (or isn't) doing.

Fix — pick by intent (semantics verified against `gc session --help`,
2026-07-07):

- `gc session close <id>` — ends the conversation AND closes the session bead;
  frees the slot permanently. Use for a finished-but-parked worker.
- `gc session kill <id>` — kills only the runtime; the reconciler restarts the
  session. Use when you want the slot back with history intact.

After closing, check the bead the worker was holding: if it is still assigned
to the dead session, re-dispatch it per `docs/conventions/bead-dispatch.md`
(`gc-sling ... --reassign`).

### Two worktree populations, two reapers

Neither reaper covers the other's population; run both when cleaning up.

| Population                        | Location                                                                                 | Reaper                           |
| --------------------------------- | ---------------------------------------------------------------------------------------- | -------------------------------- |
| Framework pool worktrees          | `/home/ds/gas-city/.gc/worktrees/` (only `polecats/` as of 2026-07-07)                   | `gc doctor --fix`                |
| Scattered skill/PR/ship worktrees | `gascity-worktrees/*`, `ship-*`, `gcd-*`, `gascity-dashboard-wt-*` under the repo family | `/home/ds/bin/reap-worktrees.sh` |

`reap-worktrees.sh` is dry-run by default; `--apply` to remove. Safety model
(from its header): reaps only clean, ≥14-day-idle (`--ttl-days`), non-root,
non-pool, non-config-referenced, non-live-pid-locked worktrees; dirty trees
with dead-pid locks are quarantined-reported, never auto-deleted. Branches and
stashes live in the main repo, so reaped history stays recoverable. Default
repo set: `/home/ds/gascity`, `/home/ds/gascity-dashboard`,
`/home/ds/gascity-packs` (override with `--repo`).

### Which dolt server is real

Multiple `dolt sql-server` processes on this host is the NORMAL state, and two
opposite mistakes are both live hazards: killing "extra" dolts can hit the
canonical one, and querying an arbitrary localhost dolt can silently return
stale data (worked incident 2).

Identification procedure:

```bash
cat /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info   # pid:port:uuid — THE ground truth
cat /home/ds/gas-city/.gc/runtime/packs/dolt/dolt-state.json  # gc's view; must agree
pgrep -af "dolt sql-server"                                # everyone else, with --config paths
```

The canonical server is the one whose `--config` lives under
`/home/ds/gas-city/.gc/runtime/` AND matches both files above (as of
2026-07-07: pid 1467571, port 29620, up since 2026-07-03). Ignore
`.beads/dolt-server.pid` — it held a long-dead pid (4663) when checked
2026-07-06; `sql-server.info` is authoritative (rule owned by `compass-dolt`).

As of 2026-07-07 there are ~7 non-canonical dolt servers running (polecat test
worktrees `polecat-{1,3,4,5,6}`, one `/tmp/city`, one serving `~/.beads`).
Their cleanup is a pending morning-ledger decision — do NOT kill them without
Stephanie's go-ahead. If you must query beads, use only the canonical port.

### Supervisor OOM-kill loop

`Restart=always` masks OOM kills completely: the city looks "up" while dying
every few hours, and `~/.gc/supervisor.log` just goes quiet mid-write with no
error line. The journal is the only honest witness:

```bash
journalctl --user -u gascity-supervisor | grep -c "oom-kill"          # loop counter
journalctl --user -u gascity-supervisor | grep -B1 "oom-kill" | tail  # when + memory peak
systemctl --user show gascity-supervisor -p MemoryPeak -p NRestarts
```

Treat ≥2 oom-kills in a day as a loop, not bad luck; find what is inflating
the cgroup (worked incident 1 shows the full trace). Heavy workloads belong in
`scix-batch`, never the supervisor's cgroup (`docs/conventions/capacity.md`);
the 2026-04-11 heap analysis with structural recommendations is
`docs/supervisor-oom-pprof-notes.md`.

### gc commands slow or hanging

Under load, even read-side gc commands stall: measured 2026-07-06/07,
`gc order check` timed out at 45s and `gc doctor` exceeded 120s while load sat
near 50. Always wrap ad-hoc gc calls in `timeout` (300s for doctor, 45–120s
for list/check). Interpret a timeout as data: demand computation runs
pre-gate in the supervisor (`ComputePoolDesiredStates` /
`order_dispatch.go:624`, RCA bead gc-454686), so a starved supervisor makes
everything downstream sluggish. Fix the load source; do not "fix" the timeout.

## Worked incidents

Three real recoveries on this host, chosen because they exercise different
rungs of the ladder. Ghost-dolt and spawn-storm rank as the costliest
recurring classes (provisional ranking, morning ledger 2026-07-07). The full
catalog belongs to `cityops-failure-archaeology`.

### Incident 1 — OOM-kill loop + polecat spawn storm (2026-07-06, partially live)

**Detection.** Order-firing floor down ~2h (RCA bead gc-454658). Journal
showed SIX supervisor oom-kills in one day (18:01 → 23:56), each cycle peaking
13.3 GB; load 40–50; five `gc nudge poll` sidecars reaped at ~989% CPU
re-formed within the hour (gc-454686).

**Diagnosis.** Two interacting causes. (a) Leaked `gc nudge poll` sidecars
busy-looping after session restarts — known class gc-b9w88, mitigated by the
`nudge-poll-reaper` order every 2m. That order is annotated `idempotent=true`
deliberately: the runaways it kills are the same processes starving the
open-work-gate, so failing closed on gate timeout would deadlock the mitigation
against its own target. (b) A shared mol-formula worktree-provisioning bug
sending every maintenance-cycle polecat into a broken no-`.git` worktree; the
polecats re-spawned endlessly and pegged the supervisor ~360% (fix analysis
gc-453622 Part 1, durable perf bead gc-g421k).

**Resolution.** Reap the runaways (reaper handles it); then stop the storm at
its source — city.toml gained `[[orders.overrides]] name = "maintenance-cycle"
/ enabled = false` (pre-flip state captured first; that convention is owned
by `cityops-city-change-control`). Epilogue: the override is now a
**permanent retirement**, not a pause awaiting the provisioning fix — the
Temporal maintenance-Run Schedule is the sole driver of maintenance-cycle
dispatch since the gc-372 P5 cutover (2026-07-16), re-enabling would
double-dispatch, and the file moves to `.toml.disabled` after a clean
Temporal week (~2026-07-23). Residual: the demand-computation stall is
pre-gate and needs an upstream pprof session, so as of 2026-07-07 the OOM
loop had recurred the same evening.

**Lessons.** `Restart=always` hides loops — count kills, don't check
liveness. A reaper can mask a storm's source indefinitely; disable at the
source via `[[orders.overrides]]`, not by killing spawned agents faster.
Read the annotation comments on any order you're tempted to change — they
encode deadlock reasoning.

### Incident 2 — Ghost dolt + poisoned dolt-state.json (2026-04-07 → 09)

**Detection.** `gc dolt status` reported "not running" despite a healthy
server answering on its port; three `dolt sql-server` processes found; the
same `USE gc; SELECT COUNT(*) FROM issues;` returned 1844 on one port and 5669
on another.

**Diagnosis.** One process was a ghost holding deleted-inode noms files
(`lsof` showed `(deleted)` on LOCK/journal; another's cwd itself was
unlinked), serving a 4-day-stale snapshot that would accept doomed writes.
Status lied because `.gc/runtime/packs/dolt/dolt-state.json` had been poisoned
by a leaked Go-test artifact pointing `data_dir` at a `/tmp/Test...` path;
`verify_our_server` rejected the real server on the mismatch. The kill vector:
the dolt-health pattern `gc dolt status || gc dolt start` would have
`kill -9`'d the healthy server and started a duplicate.

**Resolution.** Quarantine (not delete) the poisoned state file — preserved
today as `.gc/runtime/packs/dolt/dolt-state.json.POISONED-TEST-ARTIFACT-APR09`
— after which the probe flipped exit 2 → 0. Forensic full-ID diff between
ghost and canonical confirmed zero data loss (dolt's file LOCK had prevented
concurrent mutation). Full RCA and upstream fix proposals:
`docs/upstream-issue-draft-ghost-dolt.md`.

**Lessons.** Status tooling is only as honest as its state file; when status
contradicts a TCP probe, the probe wins. Never let an auto-remediating health
check run while its status source is suspect. Quarantine poisoned artifacts
with a loud filename instead of deleting them — this one is still teaching.

### Incident 3 — Order-firing freeze at 127.0.0.1:0 after supervisor restart (gc-74rxa)

**Detection.** After a `systemctl --user restart gascity-supervisor`, ALL
order firing froze. The control-dispatcher's `bd --readonly` work-queries were
resolving dolt at `127.0.0.1:0`.

**Diagnosis.** `gc supervisor run` exports `GC_DOLT_PORT` only when it STARTS
the managed dolt, not when it ADOPTS one that survived the restart. Because
`KillMode=process` guarantees dolt survives, **every** restart on this host
takes the adopt path — the bug class re-fires on any restart unless patched
around.

**Resolution.** Standing systemd drop-in
`~/.config/systemd/user/gascity-supervisor.service.d/10-dolt-port.conf`
hardcodes `GC_DOLT_PORT=29620` and `BEADS_DOLT_SERVER_PORT=29620` (stopgap;
durable fix is upstream: read the port from dolt-state.json on adopt). For an
ad-hoc shell hitting the same class (bd port-discovery flake, used successfully
overnight 2026-07-06→07, provisional):

```bash
export BEADS_DOLT_PORT=$(cut -d: -f2 /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info)
```

**Lessons.** A restart is not a no-op here — it changes which code path
configured your environment. The drop-in couples the port value to
`sql-server.info`; if the canonical port ever changes, the drop-in must change
with it (that drift trap is in the provenance list below). After every
supervisor restart, verify orders fire before declaring recovery.

## Provenance and maintenance

Facts verified live 2026-07-06/07 by the retiring fellow. Volatile claims and
their one-line re-verification commands:

| Claim (as of 2026-07-07)                                                                               | Re-verify with                                                        |
| ------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------- |
| Canonical dolt pid 1467571, port 29620                                                                 | `cat /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info`             |
| dolt-state.json agrees with sql-server.info                                                            | `cat /home/ds/gas-city/.gc/runtime/packs/dolt/dolt-state.json`        |
| ~7 non-canonical dolt servers; cleanup pending decision                                                | `pgrep -af "dolt sql-server"`                                         |
| 9 supervisor oom-kills in journal (6 on 2026-07-06)                                                    | `journalctl --user -u gascity-supervisor \| grep -c oom-kill`         |
| 9 supervisor drop-ins incl. `10-dolt-port.conf` (port 29620), `orphan-guard.conf`, `stop-catcher.conf` | `ls /home/ds/.config/systemd/user/gascity-supervisor.service.d/`      |
| `maintenance-cycle` retired via `[[orders.overrides]]` (Temporal drives it)                            | `grep -A2 'name = "maintenance-cycle"' /home/ds/gas-city/city.toml`   |
| `gc session kill` restarts / `close` ends permanently                                                  | `gc session kill --help; gc session close --help`                     |
| dispatcher-watchdog wedge signals + kill recovery                                                      | `head -30 /home/ds/gas-city/bin/dispatcher-watchdog`                  |
| reap-worktrees.sh safety model, default repos, TTL 14d                                                 | `head -30 /home/ds/bin/reap-worktrees.sh`                             |
| nudge-poll-reaper cadence 2m, idempotent=true rationale                                                | `cat /home/ds/gas-city/orders/nudge-poll-reaper.toml`                 |
| `.beads/dolt-server.pid` stale (dead pid 4663)                                                         | `p=$(cat /home/ds/gas-city/.beads/dolt-server.pid); ls /proc/$p 2>&1` |
| gc read commands stall under load (doctor >120s)                                                       | `time timeout 300 gc doctor >/dev/null`                               |

If a re-verification contradicts this file, the workspace is right and this
file is stale: fix the file, re-date the fact.
