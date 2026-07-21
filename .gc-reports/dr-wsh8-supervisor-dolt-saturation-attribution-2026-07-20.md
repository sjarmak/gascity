# dr-wsh8 — supervisor/Dolt saturation attribution

**Incident window:** 2026-07-20 16:20–16:50 EDT  
**Investigation:** read-only, 2026-07-20 18:38–18:48 EDT  
**Guardrails observed:** no signals, restart, suspension, config edit, MCP cleanup, new executor, or runtime mutation.

## Conclusion

The dominant shared feeder was the fleet of long-lived `gc convoy control --serve --follow` processes repeatedly rebuilding a short-lived control-ready cache. This is **query amplification**, not a process-spawn loop and not session lifecycle churn.

Each idle dispatcher wakes every 1–5 seconds. Its process-local ready cache expires after 3 seconds. On expiry, `controlReadyCacheFor` constructs a fresh `CachingStore` and calls `PrimeActive`. For a `BdStore`, that prime performs:

1. persistent + ephemeral `open` reads;
2. persistent + ephemeral `in_progress` reads;
3. `bd version` and a full active-row `bd sql` ready projection; and
4. because `BdStore.listIncludesCompleteDependencies()` returns `false`, one `DepList` call per active bead.

There is no cross-process cache sharing. Every rig-scoped dispatcher independently repeats the same shape. The resulting transient `bd` subprocesses connect to the one canonical Dolt server, drive Dolt CPU, consume memory/CPU in their owning scopes, and backpressure the supervisor's own cache/API work. The IDE detach was a client symptom of host and API starvation; it did not correspond to city session loss.

## Correlated incident evidence

### Supplied host snapshot

The incident record preserved these contemporaneous observations at about 16:38 EDT:

- load 70–114;
- all 8 GiB swap consumed;
- memory PSI `full avg60` around 23%;
- supervisor and canonical Dolt each sustained about 190% CPU;
- each cgroup used roughly 1.0–1.8 GiB;
- no kernel/user oomd event in 16:25–16:40;
- the supervisor had not restarted after its separate 14:39 OOM.

### Dolt connection/query amplification

Canonical Dolt warning records provide monotonic connection IDs:

| Time EDT | Connection ID | Delta/rate |
|---|---:|---:|
| 16:34:14 | 384,652 | baseline |
| 16:38:08 | 396,706 | +12,054 in 234 s = **51.5/s** |
| 16:43:20 | 409,377 | +12,671 in 312 s = **40.6/s** |
| 16:48:06 | 426,027 | +16,650 in 286 s = **58.2/s** |

The window also contains 10 `socket state is broken` records across EnterpriseBench and gpk, consistent with saturation/backpressure rather than one malformed query.

The same behavior was still observable read-only at 18:41:56–18:42:01:

- `Connections`: 819,627 → 819,920, **+293/5 s = 58.6/s**;
- `Com_select`: 2,604,318 → 2,605,372, **+1,054/5 s = 210.8/s**;
- 22 connected threads, 21 sleeping database connections and the observation query.

Thus this is high connection/query turnover, not connection-count accumulation at the instantaneous `Threads_connected` layer.

### Exact feeder attribution

A five-second process sample captured 30 distinct transient `bd` PID/command observations. The command-family totals were 8 `bd list`, 14 `bd query`, 7 `bd sql`, and one other command. Every `query` and `sql` observation, plus repeated `list` observations, had one of the long-lived control dispatchers below as its direct parent; the remaining one-shot list/other callers were retained as unrelated concurrent workload rather than attributed to the dispatcher path.

The recurring parent PIDs and command lines were:

| PID | Start EDT | Avg CPU | Command |
|---:|---|---:|---|
| 397540 | 06:19 | 3.3% | `gc convoy control --serve --follow mem/core.control-dispatcher` |
| 978438 | 06:35 | 3.5% | `... enterprisebench/core.control-dispatcher` |
| 1032532 | 06:37 | 3.2% | `... gascity-packs/core.control-dispatcher` |
| 2229983 | 07:10 | 3.1% | `... codeprobe/core.control-dispatcher` |
| 2771097 | 12:04 | 3.0% | `... gascity-dashboard/core.control-dispatcher` |
| 2771098 | 12:04 | 3.1% | `... gascity/core.control-dispatcher` |
| 3077413 | 14:43 | 2.9% | `... code-intelligence-digest/core.control-dispatcher` |

All seven predated and survived the incident window. A replacement root `core.control-dispatcher` observed at 18:41 was not used as historical identity evidence; it independently reproduced the same path and reached about 45% CPU / 704–778 MiB RSS during its first five minutes.

The captured child command sequence exactly matches the source path: `bd list --status=open`, ephemeral `bd query ... status=open`, corresponding `in_progress` reads, and `bd sql select id,is_blocked ...` under each dispatcher parent.

Relevant source:

- `/home/ds/gascity-main/cmd/gc/dispatch_runtime.go:40-77,425-583` — 1–5 s follow-loop polling and event wake behavior.
- `/home/ds/gascity-main/cmd/gc/dispatch_control_ready.go:63-70,302-351` — 3 s TTL; each stale entry opens a new store and calls `PrimeActive`; registry is process-local.
- `/home/ds/gascity-main/internal/beads/caching_store.go:704-775,1234-1273` — two-status prime and dependency fallback.
- `/home/ds/gascity-main/internal/beads/bdstore.go:385,2239-2324,2379-2425,2523-2532` — dependencies are not list-complete; persistent/ephemeral list subprocesses.
- `/home/ds/gascity-main/internal/beads/bdstore_ready_projection.go:17-131` — `bd version` and active-row-wide SQL projection.

The supervisor's ordinary cache heartbeat is not a reliable call-count measure: successful lines are rate-limited to one per minute even though the scheduler wakes every five seconds. In the incident window, the visible supervisor cache reconciles remained mostly cheap (240 lines; median 26 ms, p95 288 ms), then five caches promoted to the slower latency cadence at 16:42 with p95 7.8–12.8 s. That is downstream contention evidence, not the high-rate feeder itself.

### API latency and event/session churn

Supervisor log, 16:20–16:50:

- `/status`: 70 completions; 53 HTTP 200 and 17 HTTP 499; median 2.15 s, maximum 163.3 s.
- `/events`: 23 completions; 22 HTTP 499 and one HTTP 200; median 59.2 s, maximum 91.4 s.
- `pl-529-recovery` first recorded `context deadline exceeded` at 16:28:27.

Event spine, same 30-minute window:

- 925 total events (about 31/min), including 623 cache-reconcile and 244 controller events;
- 16 `session.stopped`, 14 `session.woke`, 2 `session.idle_killed`;
- 120 `order.fired`, 107 `order.completed`, 13 `order.failed`.

At 16:38 itself there were only 17 events. This is not an event or session-lifecycle storm.

Systemd journal recorded 22 agent run-scope starts in 30 minutes (14 Claude, 8 Codex) and 18 scope CPU-completion records. This is active workload, but not a runaway feeder/spawn loop at a rate capable of explaining 40–58 new Dolt connections per second. The seven continuously polling dispatchers do explain and were directly observed creating the relevant child commands.

## Session-preservation check

Contemporaneous incident reconciliation: 68 active gc sessions, 68 live tmux panes, zero dead panes.

Later read-only reconciliation at 18:43 EDT:

- gc nonclosed records: 78 = 68 active + 10 asleep;
- tmux panes/sessions: 68/68;
- dead panes: 0;
- every active gc `session_name` had a tmux session;
- the 10 gc names without tmux were exactly the 10 asleep sessions;
- no tmux session lacked a gc record.

There was no later city-session divergence. Recovery by signal/restart would have risked intact state without addressing the feeder.

## Smallest containment and durable fix

No containment was executed; runtime action remains unauthorized.

### Smallest feeder-specific containment candidate

Under separate authorization, switch the control-ready scan to its already-existing one-call fallback (`controlReadyFallbackReady`) for a canary dispatcher instead of building a `PrimeActive` cache every 3–5 seconds. That path executes one batched `bd ready --json --limit=5000` and applies the same `evaluateControlReady` logic in Go. This removes the 6-plus-active-bead subprocess fan-out without signaling sessions or changing Dolt.

Do **not** stop/restart all dispatchers or the supervisor: that destroys evidence, delays routed workflows, and does not repair the query shape.

Rollback: revert the code-path selection and recycle only the canary dispatcher under separately recorded authorization.

Verification before wider rollout:

1. existing cache/fallback candidate, route-alias, dedup, and readiness parity tests pass;
2. canary parent emits one `bd ready` per idle sweep and no `list/query/sql/dep` fan-out;
3. canonical Dolt `Connections` and `Com_select` deltas fall by the canary's former share;
4. routed control beads still advance within the 5 s idle bound;
5. gc-session↔tmux equality remains exact; no dispatcher/session lifecycle churn;
6. `/status` and `/events` latency improve without new 499s.

### Durable fix

Make control-ready state a persistent, event-fed per-store cache (or a supervisor-owned shared ready projection) instead of a 3-second re-prime. If `BdStore` remains the backing, use its existing batched dependency primitive rather than per-active-bead `DepList`. Cross-process dispatchers must not independently full-prime the same canonical Dolt server every few seconds. Add a load regression test with multiple follow-mode dispatchers that asserts bounded store calls per interval, not only decision correctness.

## Commands used

All were bounded/read-only: `bd show dr-wsh8`; time-window `awk` over `~/.gc/supervisor.log`; streaming Python aggregation over `.gc/events.jsonl`; bounded `journalctl`; TCP `SHOW GLOBAL STATUS` / `SHOW FULL PROCESSLIST` via the canonical port from `sql-server.info`; `ps`; `/proc/*/{status,cgroup}` reads; `gc session list --json`; and `tmux -L ds-research list-panes`.
