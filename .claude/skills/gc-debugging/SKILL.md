---
name: gc-debugging
description: >-
  Runbook for diagnosing a live Gas City: a session won't start, drains,
  restarts, or quarantines unexpectedly; a config change seems ignored; work
  sits wedged on a hook; a nudge never arrives; the controller or supervisor
  looks dead; events stop flowing. Teaches the observability surfaces — gc
  doctor, gc trace (reconciler trace stream), .gc/events.jsonl + gc events,
  session/dispatcher/supervisor logs, and process-table liveness checks — and
  which surface answers which symptom. Load when debugging runtime behavior of
  a running city or when a controller/lifecycle test fails and you need trace
  artifacts. Do NOT load for build/test failures (gc-build-verify), Dolt
  server trouble (gc-dolt-ops), or to modify reconciler code
  (gc-reconciler-lifecycle).
---

# gc-debugging — diagnosing a live Gas City

Tier 1 (single session, no subagents, survives `DISABLE_INTERACTIVITY=1` —
tier vocabulary per the departure library's convention).

This skill is a triage runbook: it maps **symptom → observability surface →
copy-pasteable command**. It teaches you to read the system's own diagnostics
instead of guessing from stdout.

Verified against gastownhall/gascity main @ f828bbe4b (2026-07-06). Command
surfaces drift; the Provenance section at the bottom has one-line re-checks.

## Jargon (defined once)

| Term           | Meaning                                                                                                                                       |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| **city**       | A directory containing `city.toml` plus `.gc/` runtime state. All `gc` commands take `--city <path>` (default: walk up from cwd).             |
| **controller** | The per-city daemon loop that dispatches orders and reconciles sessions each tick.                                                            |
| **reconciler** | The controller component that drives sessions toward a computed desired state (start, wake, drain, quarantine).                               |
| **tick**       | One reconcile cycle. Every trace record carries a `tick_id`.                                                                                  |
| **template**   | The normalized agent template a session is stamped from, e.g. `repo/polecat`. `gc trace` selectors take the exact normalized form.            |
| **bead**       | A persistent work unit in the task store. Work survives session death (NDI doctrine — see AGENTS.md).                                         |
| **nudge**      | A text delivery to a running session (`runtime.Provider.Nudge()`). Deferred nudges queue when the target is asleep or not at a safe boundary. |
| **arm**        | A per-template trace-enable record with a TTL, created by `gc trace start`.                                                                   |
| **supervisor** | The machine-wide daemon that starts/manages registered cities. Distinct from the per-city controller.                                         |

## When NOT to use this skill

| You actually need to…                                         | Use instead (sibling skill in this departure library) |
| ------------------------------------------------------------- | ----------------------------------------------------- |
| Fix a failing build, unit test, or CI job                     | `gc-build-verify`                                     |
| Understand or change reconciler/lifecycle **code**            | `gc-reconciler-lifecycle`                             |
| Diagnose the Dolt SQL server / bead store backend             | `gc-dolt-ops`                                         |
| Understand config compose/patch/override or reload semantics  | `gc-config-system`                                    |
| Add or change a typed event                                   | `gc-events-payloads`                                  |
| Debug a runtime provider (tmux/subprocess/k8s) implementation | `gc-runtime-providers`                                |

Sibling skills are authored in the same 2026-07 campaign; if one is missing,
fall back to `engdocs/` (paths cited below).

## Step 0 — always start with doctor

```bash
gc doctor            # read-only health sweep
gc doctor --verbose  # extra diagnostic detail
gc doctor --fix      # attempt automatic repairs (run plain doctor first)
```

Doctor checks (from `gc doctor --help`): city structure, config validity,
binary dependencies (tmux, git, bd, dolt), controller status, agent sessions,
zombie/orphan sessions, bead stores, Dolt server health, event log integrity,
formula compiler requirements, and per-rig health. If doctor flags the Dolt
server or bead store, switch to `gc-dolt-ops` before anything else.

## Symptom → surface map

| Symptom                                                                      | First surface                   | Command                                                                             |
| ---------------------------------------------------------------------------- | ------------------------------- | ----------------------------------------------------------------------------------- |
| Session won't start / drains / restarts / quarantines; config change ignored | Reconciler trace                | `gc trace` (Step 1)                                                                 |
| "What happened to bead X / session Y?" after the fact                        | Event log                       | `gc events` / `.gc/events.jsonl` (Step 2)                                           |
| Session is up but behaving oddly                                             | Session output + transcript     | `gc session peek` / `gc session logs` (Step 3)                                      |
| Message/nudge never arrived                                                  | Deferred-nudge queue            | `gc nudge status <session>` (Step 3)                                                |
| Order fired but no work appeared, or dispatch loop looks stuck               | Dispatcher trace log            | `.gc/runtime/*control-dispatcher-trace.log` (Step 4)                                |
| City won't come up at all; nothing reconciles                                | Supervisor                      | `gc supervisor status` / `logs` (Step 5)                                            |
| "Is anything actually running?" disagreement                                 | Process table                   | Step 6                                                                              |
| Controller/lifecycle **test** failure                                        | Reconciler trace in test window | `engdocs/contributors/reconciler-debugging.md` §Acceptance And Integration Failures |

## Step 1 — reconciler misbehavior: `gc trace`

The reconciler writes a trace stream persisted under
`.gc/runtime/session-reconciler-trace/` in the city directory. It can be
inspected **even when the controller is offline**. Malformed trace files are
moved to `.gc/runtime/session-reconciler-trace/quarantine/`.

The authoritative incident workflow — arm tracing, reproduce, collect
artifacts, what to hand the next agent — is
**`engdocs/contributors/reconciler-debugging.md`**. Follow it; do not
reinvent the artifact list. Core loop:

```bash
gc trace start --template repo/polecat --for 20m   # arm detail tracing
gc trace tail  --template repo/polecat --since 5m  # live view while reproducing
gc trace status                                     # arms + stream state
gc trace reasons --template repo/polecat --since 20m
gc trace show --template repo/polecat --since 20m --type cycle_result --json
gc trace cycle --tick <tick_id>                     # full record set for one tick
gc trace stop --template repo/polecat               # --all removes auto arms too
```

`gc trace start` levels: `detail` (default) or `baseline`
(`--level baseline`). `--for` defaults to `15m`. Selectors are the **exact
normalized template** (`repo/polecat`), not a glob.

The six record types and how to read them are documented in the engdoc's
"How To Read The Trace" section. Start from `cycle_result`, then
`template_tick_summary` for the suspect template, then `decision` /
`operation` / `mutation` for the tick you care about.

### Reason codes

`gc trace reasons` and the `reason_code` field on records explain _why_ the
reconciler did or did not act. The full enum lives in
`cmd/gc/session_reconciler_trace_types.go` (constants `TraceReason*`; 32
codes on `origin/main` as of 2026-07-06). The ones that most often crack a
case:

| `reason_code`                                     | Reading                                                                                                 |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `retained`                                        | Session kept as-is this tick. Persisting forever while you expect a wake = wedged (see worked example). |
| `no_demand`                                       | Nothing asked for this template; if you expected demand, chase the dispatch side (Step 4).              |
| `blocked_on_dependencies`                         | Bead has unmet deps; check `gc graph`.                                                                  |
| `idle_timeout`                                    | Idle-sleep put the session to sleep.                                                                    |
| `drain_timeout`                                   | Drain did not complete in time.                                                                         |
| `orphaned`                                        | Session no longer maps to desired state; subject to sweep.                                              |
| `agent_cap` / `rig_cap` / `workspace_cap` / `cap` | A capacity ceiling suppressed a start.                                                                  |
| `config_drift`                                    | Effective config changed under the session; pair with the `template_config_snapshot` record.            |
| `quarantine_entered`                              | Session was quarantined — dump the whole tick with `gc trace cycle`.                                    |
| `store_partial` / `store_query_partial`           | The bead store answered incompletely; suspect the store (→ `gc-dolt-ops`).                              |

Drain-ack note (2026-07): stop completion is **event-first** — newer
controllers record `SessionStopped` with message `drain acknowledged by
agent`; treat `.gc/events.jsonl` and trace `operation` records as the durable
signal and stdout as supporting only (engdoc, "Fast Incident Workflow"
follow-on note).

## Step 2 — the event spine: `.gc/events.jsonl` and `gc events`

Every city appends its event log to `<city>/.gc/events.jsonl` (with a
`.seq` sidecar). This is the durable "what happened" record across controller
restarts. Query it through the CLI, which reads the GC API (city-scoped in a
city directory, supervisor-scoped outside one):

```bash
gc events                                        # recent events, JSON Lines
gc events --type bead.created --since 1h         # filter by type + window
gc events --payload-match session_name=mayor     # payload field match (key=value or key.subkey=value)
gc events --watch --type convoy.closed --timeout 5m   # block until match
gc events --follow                               # continuous stream
gc events --seq                                  # print head cursor and exit
```

When the API is down, the file itself is plain JSONL — `grep`/`jq` directly
on `.gc/events.jsonl`. Event _types and payload shapes_ are owned by
`internal/events/payload.go` and the `gc-events-payloads` sibling; the pure
query primitives (Filter, CountByType, …) are documented in
`engdocs/architecture/event-query.md`.

## Step 3 — session-level diagnostics

```bash
gc status --json                       # city overview: controller, agents, rigs
gc session list --state all            # sessions incl. suspended/closed
gc session peek <session> --lines 100  # captured output without attaching
gc session logs <session> --tail 20    # structured transcript (JSONL-backed)
gc session logs <session> -f           # follow new messages
gc nudge status <session>              # queued + dead-letter deferred nudges
```

`gc session logs` resolves the conversation DAG from the session's JSONL file
(default search under `~/.claude/projects/` plus `[daemon] observe_paths`
from city.toml). `--tail N` is transcript-entry tail semantics as of 1.0.

A "message never arrived" complaint is usually a deferred nudge: nudges queue
when the target is asleep or not at a safe interactive boundary. Dead-letter
entries in `gc nudge status` mean delivery gave up — that plus a
`retained`/`idle_timeout` trace reason is the classic wedge signature.

## Step 4 — dispatcher trace logs

Each control-dispatcher writes its own trace file under the city's
`.gc/runtime/`, named by qualified agent name with `/` mapped to `--`
(commit 5ae2b009a, 2026-05-10):

```
control-dispatcher      → $GC_CITY/.gc/runtime/control-dispatcher-trace.log
demo/control-dispatcher → $GC_CITY/.gc/runtime/demo--control-dispatcher-trace.log
```

`GC_WORKFLOW_TRACE` is the topmost override for the path
(`internal/config/config.go:50-56`). If `gc convoy control --serve` warns
about a _legacy_ trace path, restart or recycle the long-lived dispatcher
session so it picks up the watcher-safe default — the warning is a rollout
action item, not noise (engdoc, top section).

Read these when an order fired (visible in events) but no molecule/session
appeared, or when dispatch appears to loop.

## Step 5 — supervisor

```bash
gc supervisor status
gc supervisor logs -n 200       # tail the machine-wide supervisor log
gc supervisor logs -f           # follow
gc cities                       # what the supervisor thinks is registered
```

Caveat from `--help`: with `GC_SUPERVISOR_LOG_TEE=0` the supervisor may write
only to the service manager's log (e.g. journald) — an existing log file can
be stale. Check the service manager too before declaring the log empty.

## Step 6 — liveness from the process table

Doctrine (AGENTS.md, "No status files — query live state"): Gas City never
writes PID/status files; the process table is the single source of truth.
So never trust a stale-looking artifact — query reality:

```bash
ps aux | grep -E 'gc (start|supervisor)'      # controller / supervisor procs
lsof -i :<port>                               # who owns a port (API, dolt)
tmux -L <socket> list-sessions                # sessions on the CITY socket only
```

The tmux socket is per-city: `[session] socket` in city.toml, defaulting to
the city name (`workspace.name`) — `internal/config/config.go` (SessionConfig
`Socket` field). Session-spawned shells get it as `$GC_TMUX_SOCKET`
(`internal/runtime/tmux/adapter.go`). **Never run bare `tmux kill-server`**
and never touch the default tmux server — target only the explicit city/test
socket (AGENTS.md tmux-safety rule). If reality (process table) and state
(beads/trace) disagree, reality wins; file the disagreement, don't hand-edit
state.

## Worked example — "my on_demand session never wakes"

Real incident: issue #3413, fixed in commit `77254dd5b` (2026-06-27).

**Symptom.** An asleep `on_demand` named session (a refinery) has routed
work waiting, but never auto-wakes. Restarting it cold works once; then it
wedges again.

**Triage per this runbook.**

1. `gc doctor` — clean. Not an infrastructure problem.
2. Arm the trace and reproduce:

   ```bash
   gc trace start --template <rig>/<refinery-template> --for 20m
   gc trace reasons --template <rig>/<refinery-template> --since 20m
   ```

   Output shows `retained` on **every tick** — the reconciler is choosing,
   each cycle, to leave the session asleep.

3. Dump a cycle: `gc trace show ... --type template_tick_summary --json`
   shows demand present (`open_count == desired_count == 1`) yet the session
   is re-slept. Demand exists, so Step 4 (dispatch) is exonerated; this is a
   reconciler decision bug → hand off to `gc-reconciler-lifecycle` territory
   with the trace JSON attached.

**Root cause** (from the fix): `ComputeAwakeSet`'s idle-sleep suppression
exempted only `assigned-work` / `min-active` / `reset-pending`. A session
woken by routed demand (`named-demand` / `work-query`) that was idle past its
timeout, whose bead already existed, was re-slept every tick — forever.
Cold-create only worked because a fresh bead has no idle reference.

**Fix.** One pure-function change adding the two demand reasons to the
exemption list — `cmd/gc/compute_awake_set.go:471` on main @ f828bbe4b
(`desired[name] != "named-demand" && desired[name] != "work-query"`), with
RED→GREEN regression tests in `compute_awake_set_test.go` shipped in the
same commit.

**The lesson.** `gc trace reasons` turned "it just doesn't wake" into a
named, per-tick decision (`retained`) in two commands. The reason code plus
one `template_tick_summary` was enough to hand a precise bug report — with
artifacts — to the reconciler owner. That handoff artifact list is the
"What To Send An Agent" section of the engdoc; use it verbatim.

## Chronic failure classes (history-derived, provisional)

From six months of fix archaeology (2026-01 → 2026-07). Provisional: ranked
per the 2026-07-07 morning-ledger positions, pending maintainer confirmation.

| Class                                | Signature                                                                                                  | Route                                               |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------------- | --------------------------------------------------- |
| Wake/nudge/drain-ack races (primary) | wedged `retained`, missed wakes, drain never acked (exemplars: 77254dd5b, 8ad393860, 2ce4306a3, c41b28026) | This skill's Step 1, then `gc-reconciler-lifecycle` |
| Managed-Dolt lifecycle               | port squatting, ghost servers, store partials                                                              | `gc-dolt-ops`                                       |
| Orphan/zombie sweep over/under-reach | sessions or beads reset unexpectedly                                                                       | Step 1 + Step 6, then `gc-reconciler-lifecycle`     |
| Config-reload cascades               | reload wedges or drains sessions                                                                           | `gc-config-system`                                  |
| tmux TUI driving                     | session hung on a surprise modal                                                                           | `gc session peek`, then `gc-runtime-providers`      |

Provisional note (2026-07-07): a wake/nudge redesign was promised when the
idle nudger was reverted (3bc34e0db) and has not re-landed under that name.
Before deep work on wake/nudge internals, check whether a redesign is in
flight; this runbook documents the current idiom.

## Provenance and maintenance

Sources: `engdocs/contributors/reconciler-debugging.md`,
`engdocs/architecture/event-query.md`, AGENTS.md, `gc` help output (binary
built from main @ f828bbe4b), and repo source cited inline. Authored
2026-07-06 during the retiring-fellow distillation campaign (discovery
report: gas-city workspace, fable-distillation/discovery-gascity.md — context
only, not load-bearing).

Re-verify volatile facts:

| Claim                                    | One-line re-check                                                                                           |
| ---------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `gc trace` subcommands and flags         | `gc trace --help && gc trace show --help`                                                                   |
| Reason-code enum (32 codes @ 2026-07-06) | `grep -c 'TraceReasonCode = "' cmd/gc/session_reconciler_trace_types.go`                                    |
| Trace dir + quarantine path              | `grep -rn 'session-reconciler-trace' cmd/gc/session_reconciler_trace_cmd.go`                                |
| Event log path                           | `grep -n 'events.jsonl' cmd/gc/cmd_events.go`                                                               |
| Dispatcher trace path + override         | `grep -n 'control-dispatcher-trace' cmd/gc/dispatch_runtime.go internal/config/config.go`                   |
| Doctor check list                        | `gc doctor --help`                                                                                          |
| `gc events` filters                      | `gc events --help`                                                                                          |
| Deferred-nudge inspection                | `gc nudge status --help`                                                                                    |
| tmux socket default                      | `grep -n -A5 'Socket specifies the tmux socket' internal/config/config.go`                                  |
| Worked-example fix still current         | `git log --oneline -1 -- cmd/gc/compute_awake_set.go && grep -n 'named-demand' cmd/gc/compute_awake_set.go` |
| Incident workflow doc unchanged          | `git log --oneline -3 -- engdocs/contributors/reconciler-debugging.md`                                      |
