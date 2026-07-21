---
name: cityops-failure-archaeology
description: >-
  Incident history of this ds-research install (`/home/ds/gas-city`):
  detection signatures and where forensic evidence lives. Load when a
  symptom looks recurrent (stopped orders, OOM/pegged supervisor,
  stale/multiple dolt, runaway sidecars, burned quota), before killing a
  leaked-looking process, or when writing an RCA. Not step-by-step recovery
  (CLAUDE.md) or dolt mechanics (compass-dolt).
---

# cityops-failure-archaeology

Every scar on this installation has a story, and most of the stories repeat.
This skill teaches you to recognize a recurrence before you re-diagnose it from
scratch, and to read the evidence trail the way the operators who wrote it did.

**Scope.** This skill owns incident narratives, detection signatures, and the
forensic-evidence map. It does not own recovery procedures, hard rules, or
endpoint models — those have homes:

| Question                                         | Go to                                                                                                    |
| ------------------------------------------------ | -------------------------------------------------------------------------------------------------------- |
| "How do I recover X right now?"                  | `/home/ds/gas-city/CLAUDE.md` Do list, then `docs/conventions/tmux-supervisor.md` full recovery playbook |
| "What must I never run?"                         | `CLAUDE.md` Don't list (notably: `bd dolt status` KILLS the live server)                                 |
| Dolt endpoint model, query recipe, config shapes | `docs/conventions/dolt-sql-server.md`, `compass-dolt`                                                    |
| What city.toml declares and why                  | `cityops-topology-contract` skill                                                                        |
| Ad-hoc session etiquette                         | `docs/conventions/guest-session-primer.md`                                                               |
| Heavy-batch rules                                | `docs/conventions/heavy-batch-claude-calls.md`                                                           |

Terms used below, defined once:

- **Floor / order-firing floor** — the supervisor's periodic order-dispatch
  loop (the 90-odd TOML files in `orders/`). "Floor is dark" = orders stop
  firing city-wide.
- **Canonical dolt server** — the one gc-managed `dolt sql-server` whose
  `--config` is `/home/ds/gas-city/.gc/runtime/packs/dolt/dolt-config.yaml`.
  As of 2026-07-07: PID 1467571, port 29620, up since 2026-07-03.
- **Ghost server** — a dolt process serving a stale snapshot from
  deleted-inode file descriptors after its on-disk files were rewritten.
- **In-floor mitigation** — a fix implemented as an order/script in this
  workspace while the durable fix waits on gc source upstream.
- **RCA bead** — a bead whose description carries the incident root-cause
  analysis; the primary written record for most incidents here.

## Evidence map — where forensics live on this host

| Evidence                   | Path                                                                                                                                                                                                             | What it tells you                                                                                                                                                                                                      |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Supervisor stdout/stderr   | `~/.gc/supervisor.log` (17.3 MB, 2026-07-07)                                                                                                                                                                     | reconciler ticks, API latencies, `poolDesired`/`scaleCheck` dumps, the exact moment a hang started                                                                                                                     |
| Who stopped the supervisor | `/tmp/supervisor-stop-caller.log` (43 captures as of 2026-07-07)                                                                                                                                                 | `ExecStopPost` snapshot of the process tree at every stop (`stop-catcher.conf` drop-in). **Trap:** its "listening on port 43677" section greps a pre-2026-07 canonical port; it is always empty now and means nothing. |
| Ad-hoc incident notes      | `/home/ds/gas-city/.gc/investigations/`                                                                                                                                                                          | log-freeze tails, goroutine dumps, hang timelines                                                                                                                                                                      |
| Per-order logs             | `/home/ds/gas-city/.gc/<order-name>.log`                                                                                                                                                                         | what each reaper/patrol actually did and when                                                                                                                                                                          |
| Order header comments      | `/home/ds/gas-city/orders/*.toml`                                                                                                                                                                                | operator lore: which incident spawned each order, tightening history, `idempotent=true` rationale                                                                                                                      |
| Config archaeology         | `city.toml` comments + `city.toml.bak-*` snapshots in the city root                                                                                                                                              | comments are the real changelog (RCA bead IDs inline); the bak-naming convention is "back up immediately before the risky flip" (`bak-pause-maintenance-cycle-20260706T175816Z`)                                       |
| Supervisor unit + drop-ins | `systemctl --user cat gascity-supervisor`                                                                                                                                                                        | 9 drop-ins as of 2026-07-07; several are fossilized incidents (see catalog)                                                                                                                                            |
| Incident write-ups         | `/home/ds/gas-city/docs/upstream-issue-draft-ghost-dolt.md`, `docs/supervisor-oom-pprof-notes.md`, `docs/upstream-issue-draft-control-dispatcher-namespace.md`, `docs/gascity_improvement_program_2026-05-29.md` | the three deepest past investigations plus the ranked P0 list                                                                                                                                                          |
| Preserved poison sample    | `.gc/runtime/packs/dolt/dolt-state.json.POISONED-TEST-ARTIFACT-APR09`                                                                                                                                            | the actual file that nearly got the healthy server kill -9'd (worked example 1)                                                                                                                                        |
| systemd journal            | `journalctl --user`                                                                                                                                                                                              | **volatile:** as of 2026-07-07 the journal only reaches back to the 2026-07-01 boot; OOM-kill evidence from April is gone from it — use the docs above instead                                                         |

### Bead forensics: the two-store trap

City-level RCA beads use the `gc-` prefix but live in the **file backend**
(`.gc/beads.json`, ~150 MB) — NOT in the dolt `gc` database, and NOT in the
`gascity` rig database, which also uses `gc-` prefixed IDs. To find a `gc-NNN`
bead, try in this order (all verified 2026-07-07):

```bash
cd /home/ds/gas-city
# 1. File backend (city sessions, RCA messages) — header only:
gc beads show gc-454658
# 2. Full description (gc beads show prints no body; --json unsupported):
jq -r '.beads[] | select(.id=="gc-454658") | .description' .gc/beads.json
# 3. Rig bead stores, over TCP (recipe owned by CLAUDE.md / dolt-sql-server.md):
PORT=$(cut -d: -f2 .beads/dolt/.dolt/sql-server.info)
dolt --host 127.0.0.1 --port "$PORT" --user root --no-tls --password '' \
  sql -q "USE gascity; SELECT id,title,status FROM issues WHERE id='gc-74rxa';"
```

`gc bd` is hard-blocked here (file provider); `bd show` from the city root will
not find file-backend beads either. Expect `gc beads show` and `gc order check`
to be slow (30-45 s+) — that slowness is itself a catalogued symptom (worked
example 2), so time it before dismissing it.

## Incident catalog

Ranking note: ghost-dolt and spawn-storm class incidents are treated as
first-tier because they recur (provisional position, morning-ledger
2026-07-07 — Stephanie has not yet ranked her costliest incidents).

| Date              | Incident                                                                                                                                                                                                                                                 | Detection signature                                                                                                     | Standing mitigation                                                                                                | Primary source                                                |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------- |
| 2026-04-07..11    | **Ghost dolt fleet + poisoned dolt-state.json** (worked example 1)                                                                                                                                                                                       | multiple `dolt sql-server` procs; row counts differ per port; `gc dolt status` says down while server is up             | `dolt.auto-start: false`; hard rules in CLAUDE.md                                                                  | `docs/upstream-issue-draft-ghost-dolt.md`                     |
| 2026-04-10/11     | **Supervisor OOM, 5x oomd kills, 7.5 GB peak** (worked example 3)                                                                                                                                                                                        | supervisor "mysteriously" restarts; oomd entries in journal (now rotated out)                                           | `oom-preference.conf` drop-in; `scix-batch` wrapper                                                                | `docs/supervisor-oom-pprof-notes.md`                          |
| undated (post-04) | **Supervisor restart flap, 400+ restarts** — KillMode=process orphan child held `controller.lock`, every ExecStart collided                                                                                                                              | `supervisor already running (PID N)` + exit 1 loop in `~/.gc/supervisor.log`; order dispatcher dead                     | `orphan-guard.conf` ExecStartPre drop-in                                                                           | drop-in comment                                               |
| 2026-05-25        | **4-day binary freeze** — a ship/test left `/home/ds/gascity-main` detached; `gcsync` refuses **silently** when off-main                                                                                                                                 | new gc fixes never appear in installed binary; `git -C /home/ds/gascity-main symbolic-ref -q HEAD` != `refs/heads/main` | `gascity-main-pin-guard` order (15 m, self-heals stale off-main >30 m)                                             | `orders/gascity-main-pin-guard.toml` header                   |
| 2026-06-12        | **Heavy-batch quota burn** — 1,000+ bare-`claude` one-shots capped the default account's 5 h window; 2,156 doomed 429-retry sessions; morning crons starved                                                                                              | 429 storms in a batch log; `~/.claude-usage/usage_cache.json` shows one account pinned at 100%                          | `CLAUDE_BINARY=claude-auto` on batch workers; `scix-batch`; pre-flight check                                       | `docs/conventions/heavy-batch-claude-calls.md`                |
| 2026-06-20        | **Credential rot** — csu_pick's rot-preference steered launches onto near-expiry account3; headless launches write the credential back with the refresh token stripped                                                                                   | an account that was healthy yesterday needs re-auth                                                                     | `CSU_PICK_EXCLUDE="claude-2,claude-3,claude-4"` in city.toml (comment: leave it even after rot clears)             | `city.toml` ~line 134 + comment ~line 369                     |
| 2026-06-21        | **`net_read_timeout` "unexpected EOF"** — cross-store enumeration over ~20 rigs blew the 15 s managed default                                                                                                                                            | intermittent EOF on sling/enumeration paths                                                                             | `[dolt] read_timeout_millis = 60000` in city.toml                                                                  | `city.toml` lines 5-9                                         |
| 2026-06-24        | **All-day nudge hangs** — 4 orphaned `gc nudge poll` sidecars at ~735% CPU (gc-b9w88)                                                                                                                                                                    | `gc nudge` operations hang; poll procs >50% CPU (healthy is ~0%)                                                        | `nudge-poll-reaper`, tightened to 2 m cadence 2026-06-29 (order retired 2026-07-21; gascity-nudge-poll-reaper systemd timer is the live leg); durable fix held needs-source-impl | `orders/nudge-poll-reaper.toml.disabled` header               |
| 2026-07-06        | **Order-firing floor down ~2 h** (worked example 2)                                                                                                                                                                                                      | orders fail the 8 s open-work gate; `/status` 499 after 2 m 37 s; loadavg 29                                            | `idempotent=true` on critical reapers; `maintenance-cycle` disabled                                                | RCA beads gc-454658 / gc-454686                               |
| open (gc-74rxa)   | **Dolt resolves at 127.0.0.1:0** — supervisor exports `GC_DOLT_PORT` only when it _starts_ dolt, not when it _adopts_ one after restart                                                                                                                  | order-firing freezes right after a supervisor restart while dolt PID survived                                           | `10-dolt-port.conf` drop-in hardcodes 29620 — **must be updated if the port ever changes**                         | drop-in comment; bead gc-74rxa (gascity db, open)             |
| closed (gc-typpc) | **Concurrent claim race** — one bead claimed by 4 slots clobbering a shared branch                                                                                                                                                                       | duplicate work / force-push collisions on one bead's branch                                                             | interim reaper bind-by-name shipped; ADR-0009 written, **not built** (all 9 ADRs status:proposed as of 2026-07-07) | bead gc-typpc (gascity db); `docs/adr/`                       |
| 2026-04-13        | **Log blowout** — `dolt-server.log.old-20260413` reached 167 MB (still on disk)                                                                                                                                                                          | `.beads/` eats disk                                                                                                     | `janitor-log-rotate` order (live since 2026-07-04)                                                                 | `.beads/` listing                                             |
| 2026-07-07        | **bd port-discovery flake** — bare `bd` intermittently fails to resolve the server; workaround: export the port from `sql-server.info` before `bd` writes (provisional, morning-ledger; upstream issue not yet filed — filing needs per-action approval) | `bd` errors/hangs while TCP queries work fine                                                                           | none installed yet                                                                                                 | `docs/design/fable-distillation/morning-ledger-2026-07-07.md` |

Two meta-patterns worth internalizing: **fixes fossilize into drop-ins and
orders** (each carries its incident in a comment — read before "cleaning up"),
and **fixes create the next failure mode** (KillMode=process saved tmux
history, then caused the 400-restart flap; the stop-catcher still greps a dead
port). When you mitigate something, write the story into the artifact.

---

## Worked example 1 — the ghost dolt fleet and the poisoned state file (2026-04)

The deepest data-integrity near-miss on record, and the incident class most
likely to recur — as of 2026-07-07 there are **seven** non-canonical dolt
servers running (see Recurrence check).

### Detection

Three `dolt sql-server` processes were found running simultaneously. The same
query gave different answers per port:

```
port 44799 → SELECT COUNT(*) FROM issues → 1844   (frozen at an Apr 7 snapshot)
port 43677 → SELECT COUNT(*) FROM issues → 5669   (live)
```

Separately, `gc dolt status` reported "not running" despite the healthy server
answering queries. Both servers appended to the **same** `dolt-server.log`, so
the log could not attribute lines to a server.

### Diagnosis

1. `lsof` on the extra PIDs showed their dolt storage FDs all `(deleted)` —
   the on-disk files had been rewritten under them by a later start. One
   orphan's cwd itself was unlinked. All were reparented to init (PPID=1) via
   tmux-spawn scopes, invisible to the supervisor cgroup.
2. The false "not running": `.gc/runtime/packs/dolt/dolt-state.json` contained
   `"data_dir": "/tmp/TestGcBeadsBdStartUsesRootBeadsDataDir.../.beads/dolt"`
   — a Go **test artifact leaked into production runtime state**. The probe
   compares `data_dir` to the real path, mismatches, and exits 2.
3. The kill chain that almost fired: the dolt-health order runs
   `gc dolt status || gc dolt start`; with the poisoned file, `op_start` finds
   the real PID, reads port 22246 from the poison, finds nothing listening
   there, and **`kill -9`s the healthy server** before starting fresh.
4. Repro was clean: probe exit=2 with the poisoned file present, exit=0 with
   it moved aside, exit=2 again on restore.
5. Forensic diff between ghost and live data: zero divergent rows — dolt's
   file LOCK had prevented concurrent mutation. Luck, not design; a client
   caching the ghost's port would have silently read 4-day-old data.

### Resolution

The poisoned file was quarantined (preserved at
`.gc/runtime/packs/dolt/dolt-state.json.POISONED-TEST-ARTIFACT-APR09` — open it
to see the exact shape), `dolt.auto-start: false` went into every
`.beads/config.yaml`, the hard rules landed in CLAUDE.md, and the upstream
write-up is `docs/upstream-issue-draft-ghost-dolt.md`. Ghost _processes_ still
have no automated reaper — `gc dolt cleanup` and the mol-dog orders target
database dirs, not processes.

### Recurrence check (live finding, 2026-07-07)

```bash
pgrep -af "dolt sql-server"   # anything not --config /home/ds/gas-city/.gc/runtime/packs/dolt/dolt-config.yaml is non-canonical
jq -r .data_dir /home/ds/gas-city/.gc/runtime/packs/dolt/dolt-state.json   # must be /home/ds/gas-city/.beads/dolt; /tmp/* = poisoned
```

Right now that first command returns the canonical server **plus** six leaked
test-config servers (`/home/ds/gascity-worktrees/polecat-{1,3,4,5,6}` and
`/tmp/city`) and one serving `/home/ds/.beads` on port 40191. Their cleanup is
an open morning-ledger decision — **do not kill them without Stephanie's
go-ahead**, and never kill by name-match alone: check `--config` in the
cmdline AND `ls -l /proc/<pid>/cwd` first, because the canonical server dies
to the same pkill pattern. Also note `.beads/dolt-server.pid` reads 4663, a
dead PID — it has been stale since before 2026-07-06; `sql-server.info` is the
only PID/port ground truth.

---

## Worked example 2 — the order-firing floor goes dark (2026-07-06)

The freshest incident; its mitigations are still settling and its residual bug
is still open. Primary record: RCA beads **gc-454658** and **gc-454686** (file
backend — extraction commands above) plus
`.gc/investigations/supervisor-hang-20260706-1236.md`.

### Detection

- Orders city-wide failed their open-work gate (8 s timeout) — floor dark ~2 h.
- `~/.gc/supervisor.log` froze mid-tick at 12:36, then showed
  `GET /v0/city/ds-research/status 499 2m37s [memory]` repeating — the
  dashboard backend polls `/status` continuously, and each poll triggered a
  fresh 1-2 minute status build.
- `loading session snapshot timed out after 3s` in order output.
- `gc order check` and `gc status` burned 30-41 s of USER CPU against a tiny
  store (103 issues) — CPU-bound, not dolt, not store bloat.
- loadavg 29 on 16 cores.

### Diagnosis

Two diseases stacked, which is what made it confusing:

1. **Acute:** three runaway `gc nudge poll` sidecars at 300%+ CPU each
   (orphaned by session restarts, the gc-b9w88 hot-loop bug) saturated the
   box. The `nudge-poll-reaper` order exists precisely to kill these — but it
   could not fire **because the floor was down**, and the floor was down
   partly because the runaways starved the gate. Orders then failed _closed_
   at the gate. A textbook chicken-and-egg: the mitigation lived inside the
   system it was meant to protect.
2. **Residual (still open as of 2026-07-07):** the demand-computation path
   (`ComputePoolDesiredStates` / open-work gate,
   `cmd/gc/order_dispatch.go:624` per the RCA) is O(n²)-ish over ~30 sessions
   and runs _before_ per-order gating, so no gate tweak can fix it. Suspected
   driver: the snapshot walking git state of accumulated orphaned/detached
   worktrees. Pinning it needs a pprof session (durable perf bead gc-g421k).

After the first manual kill, **five runaways re-formed within ~30 minutes**
(~989% CPU total) — treat any nudge-poll kill as temporary until the source
fix lands.

### Resolution

- Runaways killed manually, twice (that was a human/mayor decision under an
  active incident — do not free-lance process kills; surface first).
- `idempotent = true` added to the four in-floor reapers (nudge-poll-reaper,
  blocked-routed-reaper, gate-sweep, resource-sweep) so they **fail open** at
  the gate per gastownhall/gascity#2893 — read the annotation in
  `orders/nudge-poll-reaper.toml.disabled` (order retired 2026-07-21; the
  systemd timer is the live leg); it documents why failing closed was a
  deadlock. Fail-open is only allowed on verified re-run-safe orders.
- `maintenance-cycle` disabled via `[[orders.overrides]]` in city.toml (with a
  bak snapshot 17 s prior): a separate worktree-provisioning bug sent every
  maintenance polecat into a broken no-`.git` worktree, re-spawning endlessly
  and pegging the supervisor ~360%. The override comment named its own
  re-enable condition — that pattern (exit criteria written at pause time) is
  the house style. Epilogue: the pause was **permanently superseded** — the
  Temporal maintenance-Run Schedule is the sole driver of maintenance-cycle
  dispatch since the gc-372 P5 cutover (2026-07-16); re-enabling would
  double-dispatch, and the order file moves to `.toml.disabled` after a clean
  Temporal week (~2026-07-23). RCA gc-qo3.
- Recovery of the wedge itself required
  `systemctl --user restart gascity-supervisor`; `gc doctor --fix` did NOT
  clear it. The escalation ladder lives in CLAUDE.md, not here.

### Recurrence check

```bash
ps -eo pid,pcpu,etimes,args --no-headers | awk '$0 ~ /gc nudge poll/ && $2 > 50 && $3 > 60'
find /home/ds/gas-city/.gc -maxdepth 1 -name '*.log' -mmin -30 | wc -l   # 0 = floor may be dark
grep -n "enabled = false" /home/ds/gas-city/city.toml                    # is maintenance-cycle still paused?
```

If runaways exist AND no order logs are being written, assume the chicken-and-
egg state and check whether `nudge-poll-reaper` has fired
(`tail /home/ds/gas-city/.gc/nudge-poll-reaper.log`) before anything else.

---

## Worked example 3 — supervisor OOM, and how the fix chain grew (2026-04-10/11 →)

The best example of memory archaeology, and of mitigations begetting new
incidents. Primary record: `docs/supervisor-oom-pprof-notes.md`.

### Detection

Five systemd-oomd kills in one day; the previous supervisor instance peaked at
7.5 GB RSS over 19 hours. From the inside it looked like "the supervisor keeps
mysteriously restarting" — which is exactly why the stop-catcher drop-in
exists: check `/tmp/supervisor-stop-caller.log` first for any unexplained
stop; oomd collateral shows up there as a killed scope under `user@1000`.

### Diagnosis

The supervisor has a built-in pprof endpoint (`http://localhost:6060/debug/pprof/`).
The heap profile showed 40 MB live heap against **24 GB cumulative
allocations in 34 minutes** (~700 MB/min) — not a leak, an allocation churn
problem. The dominant path: **`city.toml` re-parsed from TOML on every
reconciler tick, on every `BdStore.List/Create`, for every rig** — 16 rigs on
a short tick. The multi-GB peaks came from the cgroup, not the Go heap: up to
16 concurrent `bd` subprocesses (each hundreds of MB against dolt), stacking
for up to 120 s each when `bd list` timed out (3,111 recorded timeout events).

Lesson: for "supervisor is huge" reports, distinguish three numbers before
theorizing — Go live heap (pprof), main-process RSS (`ps`), and cgroup RSS
(`systemctl --user status gascity-supervisor`, which includes bd/dolt
children). They diverge by an order of magnitude here.

### Resolution — and the chain it started

1. `scix-batch` wrapper (capped transient cgroup, `ManagedOOMPreference=avoid`)
   so heavy batches get killed instead of the supervisor/mayor.
2. `oom-preference.conf` drop-in: `ManagedOOMPreference=omit` + `MemoryLow=256M`
   on the supervisor.
3. `KillMode=process` on the unit (predates this; kept) so a supervisor stop
   never SIGTERMs the tmux servers in its cgroup — protecting session history.
4. …which caused the **next** incident: on stop, the orphaned `gc supervisor
run` child kept holding `controller.lock`, every restart collided, and
   systemd flapped through 400+ restarts with the order dispatcher dead. Fixed
   by `orphan-guard.conf` (ExecStartPre clears a stale lock-holding child; the
   `^`-anchored pattern deliberately misses the `sg docker` wrapper).

When you touch the supervisor unit, read all nine drop-ins first
(`systemctl --user cat gascity-supervisor`) — each one is load-bearing against
a specific past failure, and their interactions are the current equilibrium.

---

## Recurrence sweep

One read-only command checks every catalogued signature (leaked/ghost dolt,
poisoned state file, runaway nudge polls, dark floor, pegged supervisor,
off-main binary tree, port drift, recent stops, load):

```bash
/home/ds/gas-city/.claude/skills/cityops-failure-archaeology/scripts/recurrence-sweep.sh
```

It prints `ok`/`FLAG` lines and mutates nothing. Verified 2026-07-07: 1 flag
(the seven known non-canonical dolt servers). A FLAG is a pointer into this
skill's catalog, not an instruction to act — killing, restarting, and file
quarantine all go through the CLAUDE.md rules and, for anything external or
destructive, per-action human approval. No automation named in this skill is
trusted-unsupervised; spot-check reaper logs after relying on them
(provisional trust position, morning-ledger 2026-07-07).

## Writing the next RCA

Follow the trail the past ones left: file the narrative as an RCA bead
(assignee the responsible PL or mayor, DONE/RESIDUAL/ASKS structure like
gc-454658), drop raw evidence in `.gc/investigations/`, annotate any
order/config/drop-in you change with the incident date and bead ID, snapshot
`city.toml` as `city.toml.bak-<reason>-<UTC timestamp>` before any risky flip,
and state the re-enable condition inside the pause comment. If the bug is
upstream's, draft the issue in `docs/` — filing it is an external artifact and
needs per-action approval.

## Provenance and maintenance

Written 2026-07-07 from on-host evidence; every command above was executed
during authoring. Volatile facts and their one-line re-verification:

| Claim (as of 2026-07-07)                                | Re-verify with                                                                            |
| ------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| Canonical dolt PID 1467571 / port 29620                 | `cat /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info`                                 |
| 7 non-canonical dolt servers running                    | `pgrep -af "dolt sql-server"`                                                             |
| `.beads/dolt-server.pid` stale (4663 dead)              | `ls -d /proc/$(cat /home/ds/gas-city/.beads/dolt-server.pid)`                             |
| dolt-state.json healthy (data_dir canonical)            | `jq -r .data_dir /home/ds/gas-city/.gc/runtime/packs/dolt/dolt-state.json`                |
| 43 stop captures in stop-caller log                     | `grep -c "STOP triggered" /tmp/supervisor-stop-caller.log`                                |
| 9 supervisor drop-ins; 10-dolt-port.conf says 29620     | `ls /home/ds/.config/systemd/user/gascity-supervisor.service.d/`                          |
| maintenance-cycle retired via orders.overrides (Temporal drives it) | `grep -A2 'name = "maintenance-cycle"' /home/ds/gas-city/city.toml`                       |
| gc-74rxa open; gc-typpc closed                          | dolt TCP query on the `gascity` db (recipe in "Bead forensics")                           |
| All 9 ADRs status:proposed                              | `grep -l '\*\*Status\*\*: proposed' /home/ds/gas-city/docs/adr/0*.md \| wc -l` (expect 9) |
| journal reaches back only to 2026-07-01                 | `journalctl --user --list-boots`                                                          |
| Supervisor main PID 773252 (restarted 2026-07-06 23:56) | `systemctl --user show gascity-supervisor -p MainPID -p ExecMainStartTimestamp`           |
| gc-454658 description retrievable from file backend     | `jq -r '.beads[] \| select(.id=="gc-454658") \| .title' /home/ds/gas-city/.gc/beads.json` |

Provisional positions relied on above (revisit after Stephanie answers the
discovery questions): incident ranking (ghost dolt + spawn storms first),
the no-subsystem-trusted-unsupervised default, and the bd port-discovery
workaround. If the canonical dolt port ever changes, the same-day update list
is: this skill, the `10-dolt-port.conf` drop-in, the sweep script's port
check, and the three sibling skills that carry the PID:port literal
(`cityops-dolt-beads-reference`, `cityops-debugging-playbook`,
`cityops-guest-session-discipline`).
