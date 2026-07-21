---
name: mechanic
description: Use when diagnosing gc / beads / dolt / supervisor / config / pack behavior you don't fully understand — trace the root cause from source before theorizing from symptoms or applying per-bead/per-config workarounds. Provides the diagnostic-first method, the binary-grep schema technique, RCA discipline, and this workspace's safety floor. For subsystem file-indexes, summon the matching compass instead.
---

# Mechanic — infrastructure diagnostic method

Keep the engine running. This skill is the **method** for diagnosing gc /
beads / dolt / supervisor / config / pack-import malfunctions — not an agent
and not a health-sweep loop (orders + reapers already run the sweep; see
`compass-scanners`). Summon it for an *acute* "why is this broken" question.

## Method: trace from source first

For ANY gc / beads / dolt / supervisor behavior you don't fully understand,
**reading the source is the go-to method** — before theorizing from symptoms,
before per-bead/per-config workarounds, before escalating. Trace the actual
code path to the exact lines that produce the behavior.

- **gc** source: `/home/ds/gascity-main` (Go). Confirm it matches the installed
  binary first — `gcsync` keeps `/home/ds/gascity-main` at `origin/main` HEAD and
  rebuilds/installs `gc`, so `git -C /home/ds/gascity-main log -1` should match the
  running `gc`. Do **not** read `/home/ds/gascity` (contributor PR branches) for
  ground-truth — see `compass-gc-binary`.
- **pack scripts / prompts / orders / formulas**: each rig's
  `.gc/system/packs/*` (e.g. `core/`, `maintenance/`, `dolt/`) shows how features
  are actually wired. Our own orders live in `orders/*.toml`, launchers/reapers in
  `bin/`.
- **resolved config provenance**: `gc config explain` — which file each value
  came from.

Symptom-guessing is expensive. A ~10-minute source trace beats several failed
containment attempts that all get undone. When a stanza key is rejected or docs
are silent, use the binary-grep technique below.

## Binary-grep schema technique (when schema docs lie or are missing)

The `gc` binary carries its TOML/JSON struct tags and Go type names in the
symbol table. Grep the binary for ground-truth:

```sh
grep -aoE 'toml:"[^"]+"' $(which gc) | sort -u                      # all TOML tags
grep -aoE 'toml:"[^"]+"' $(which gc) | sort -u | grep -iE 'patch|rig|agent'
grep -aoE 'json:"[^"]+"' $(which gc) | sort -u | grep -iE 'patch|rig'  # HTTP API surface
grep -aoE '[A-Z][a-zA-Z]+Patch(es)?[a-zA-Z]*' $(which gc) | sort -u    # Go struct names
grep -aoE '/v[0-9]+/city/[^ ]*' $(which gc) | tr ' ' '\n' | grep -i <pattern>  # API paths
```

Use `grep -ao` on the raw binary (works everywhere); if `strings | grep` returns
empty but `grep -ao` finds the pattern, suspect `strings` min-length filtering.
This technique replaces rounds of parser trial-and-error.

## CPU-vs-load — saturated, or capped? (adapted from Wldc4rd/gc-debug)

A high load average is not high CPU. Before chasing a "CPU-starved" symptom
(recurring here — oomd/SIGKILL, `compass-capacity`, `reference_dolt_gc_never_runs`),
separate *real saturation* from a *vCPU/quota cap* or *IO/lock wait*:

```sh
cat /proc/pressure/cpu /proc/pressure/io   # PSI — some/full stall %; full>0 = genuinely starved
mpstat 1 2                                 # %usr+%sys (work) vs %idle / %iowait / %steal (cap)
ps -o pid,stat,pcpu,wchan,etime -p <pid>   # STAT S + wchan ep_poll/futex = idle/waiting, NOT burning CPU
```

- `%steal` high or load≫cores at low `%usr` → **vCPU/cgroup cap**, not saturation (don't "optimize" a process that's actually throttled).
- `%iowait` high or PSI `io full`>0 → **IO/lock wait**, look at dolt/disk, not CPU.
- Find the *real* consumer, don't eyeball `top`:

```sh
pidstat -u 1 3 | awk '$8>1{print $8"%",$NF}' | sort -rn   # processes actually using CPU
ss -tnp | grep <port> | awk '{print $6}' | sort | uniq -c # map connections → owning pid
```

## RCA discipline — name the bias before testing the hypothesis

Three questions, in order:

1. **Coincidence** — did symptom and suspected cause both appear in the same
   window by chance? Two unrelated log events near each other is the default
   state of a busy system. Default prior: yes, until ruled out.
2. **Correlation** — do they always co-occur? Run the suspected cause in
   isolation; does the symptom follow? If no, the link was coincidence — stop.
3. **Causation** — is there a mechanism you can state in one sentence with no
   hand-waving? If not, you have correlation, not causation, and your "fix" may
   not fix anything.

Falsifiable or it's not a diagnosis. "It's flaky" is not a diagnosis. When you
propose a fix, state **what you expect if you're right, and what you expect if
you're wrong** — if both predictions are identical, the fix is untestable.

Two laws before you call a fix done (adapted from Wldc4rd/gc-debug):

- **A green unit test proves behavior, not scale.** Performance, OOM, lock, and
  bloat fixes must be proved on production-shaped data (real row counts / store
  size / concurrency), not just a passing test. "It passes" ≠ "it holds at 1.4M
  rows." Dogfood before claiming done.
- **Enumerate every consumer before mutating shared state.** Before deleting or
  changing persistent/shared data (beads, store rows, cursors, cron last-run),
  list every reader first. The classic miss: a row that's *also* a ledger entry,
  a `seq:*` cursor, or an order's last-run marker (the gc #2929 order-bead leak —
  the deleted beads were also the run-ledger).

## Diagnostic-first — gc-native tool before the spanner

Before `kill -9`, raw SQL, manual file deletion, or `systemctl`, ask: is there a
gc-native tool? There almost always is.

- ❌ `pkill -f tmux` → ✅ `gc session kill <id>` / recovery in `compass-tmux-supervisor`
- ❌ raw `mysql ... drop` → ✅ `gc dolt-cleanup --probe --json` first, never `--force` without go-ahead
- ❌ `rm -rf .beads/dolt/` → ✅ see `compass-dolt` recovery order
- ❌ `systemctl restart gascity-supervisor` as first move → ✅ `gc doctor` → `gc doctor --fix` → soft reload, restart last

Two scope traps that have burned hours:
- **bd scope** — bare `gc bd` (no `--rig`) resolves to the cwd namespace and
  returns only city-level beads. "the bd database is wiped" is 99% scope-misaddress,
  not data loss. Pass `--rig <name>` explicitly; read the port from
  `.beads/dolt/.dolt/sql-server.info` for direct SQL.
- **dolt is gc-MANAGED here** — this is a shared, supervised endpoint, NOT
  per-rig `.beads/dolt/` you can `gc dolt start`/`recover`/quarantine. The generic
  "server not started → gc dolt start" playbook will **kill the live server**.
  Defer entirely to `compass-dolt` and `docs/conventions/dolt-sql-server.md`.

## Safety floor (this workspace's hard rules)

Free to run: any read-only / inspection command, any dry-run (`--probe`,
`--dry-run`, `--check`, `gc bd query`, `gc events`, `gc trace show`,
`gc config explain`), and reversible edits to skills/compasses you own.

**Do not** run without explicit human/mayor go-ahead in-thread:
- `bd dolt start|stop|status` here → kills the live gc-managed server (`bd dolt
  status` does it as a "drift recovery" side effect). [gascity#506/#245/#323]
- `dolt sql` inside `.beads/dolt/` while the server is up → LOCK contention.
- `gc supervisor stop|uninstall`, `gc stop --force`, `gc cities unregister`.
- `gc dolt-cleanup --force` (probe is fine; check `force_blockers` first).
- blanket-killing `claude-zombie-report` flagged processes → interactive tmux
  work looks identical to abandoned sessions; triage by CWD + tmux cross-ref first.
- `--no-verify` / `--no-gpg-sign` / `--dangerously-skip-permissions` → hides the
  root cause the hook caught.
- any `git push` / PR / external send → per-action approval (agent-collaboration).

When in doubt: **capture state first** (`gc config show > /tmp/gc-snapshot-$(date
+%s).toml`), then ask with the diagnostic data. Cost of asking is one round-trip;
cost of a wrong destructive op is hours.

## Where to go next (don't duplicate the compasses)

| Subsystem | Compass |
| --- | --- |
| shared dolt sql-server, bead store, endpoint config, drift | `compass-dolt` |
| tmux socket, supervisor service, session collisions, recovery | `compass-tmux-supervisor` |
| periodic scanners, reapers, audit logs, evidence gates | `compass-scanners` |
| `gc` binary, gcsync, two-worktree layout, oversight-rig pack | `compass-gc-binary` |
| account allocation, rate-limit failover, scix-batch, oomd | `compass-capacity` |
| `gc sling`, gc-sling wrapper, formula injection, claim handoff | `compass-bead-dispatch` |

Detailed recovery playbooks: `docs/conventions/*.md`.
