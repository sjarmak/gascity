# RCA — host hard-died 08:11 EDT 2026-07-15; ~85 min city outage

**Owner:** city-infra-pl
**Severity:** P1 (whole-host death; city stopped flowing work ~06:50, host died 08:11, recovered 08:20)
**Status:** city fully recovered; root cause is a Stephanie-gated capacity item, detection gap is city-infra's

## Timeline (evidence-backed)

| time (EDT) | event | evidence |
|---|---|---|
| 03:33–06:47 | **swap free = 0.0–0.2 MB continuously**; `Committed_AS` 68–75G on a **62.4G-RAM box**; load1 sustained 23–41 | `.gc/observability/resource-metrics.jsonl` |
| 06:32 | `resource-sweep` runs: MemAvailable 14.5G, swap 99%, disk 97% → **stays silent** | `.gc/resource-sweep.log` |
| 06:50 | last order fires (`beads-health` gc-491370); **order-firing stops** | `gc order history beads-health` |
| 07:10→08:00 | supervisor log density thins (07:10,07:12,07:14,07:17,07:20,07:22,07:26,07:49,08:00) | `~/.gc/supervisor.log` |
| 07:26:24 | `tmux state cache: refresh failed in **3m14.6s**: tmux -u: context deadline exceeded` | supervisor.log |
| 07:49:28 | `tmux state cache: refresh failed in **18m22.9s**: tmux -u: context deadline exceeded` | supervisor.log |
| 08:11:07 | **host journal stops mid-stream. No shutdown/reboot sequence. No OOM-kill or oomd-kill record.** | `journalctl -b -1` (boot -1 ran 07-01 08:13 → 07-15 08:11, 14d) |
| 08:12:53 | host boots fresh | `journalctl --list-boots`, `uptime` |
| 08:13:48 | supervisor starts | `systemctl --user status gascity-supervisor` |
| 08:15:40 | orders firing again (`beads-health`) | order history |
| 08:17–08:20 | 40 tmux sessions recreated; dolt sql-server up (pid 4156, :29620); bead store reachable | `tmux -L ds-research ls`, `sql-server.info` |

## What happened

The box ran **chronically over its memory budget**: `Committed_AS` 68–75G against 62.4G physical RAM, with the entire 8G of swap **fully consumed and 0 MB free for hours**. Top RSS at the last pre-crash sample (06:47):

- `postgres: 16/main: checkpointer` — **9.6 GB**
- `postgres: 16/main: background writer` — **5.4 GB**
- ~15 GB in postgres alone (the known `box_oom_gate_scix_postgres` ledger item)
- then `gc supervisor` 1.15G, several `claude` agents ~0.8–1.0G each, qdrant 0.83G, dolt 0.83G

With zero swap headroom, the kernel had nowhere to reclaim to. The system degraded rather than cleanly OOM-killing one cgroup: order-firing stopped ~06:50, then **tmux itself went unresponsive** — `tmux -u` taking 3m14s at 07:26 and **18m22s** at 07:49 — which starved the supervisor's tmux state cache. At 08:11:07 the host stopped logging entirely with no shutdown sequence and no OOM-kill record: an unclean hard death (kernel hang/panic under exhaustion; a panic would not reach disk).

**Not claimed:** I have no positive proof memory *alone* killed the kernel — there is no OOM-kill record and no panic trace on disk. What is established is that the host was in a zero-reclaim-headroom state for hours, degraded progressively, and died uncleanly. Note also `systemd-journald: Under memory pressure, flushing caches` is **chronic since Jul 01** and is *not* a death signature — do not read it as one.

## Why no guard fired (the detection gap)

`bin/resource-sweep` (fired 06:32, 18 min before the stall) gates its mayor nudge on:

```
AVAIL_MIN_GB="${RESOURCE_SWEEP_AVAIL_MIN_GB:-4}"     # line 25
awk "BEGIN{exit !($avail_gb < $AVAIL_MIN_GB)}" && pressure=1   # line 137
(( disk_free_gb < DISK_MIN_FREE_GB )) && pressure=1            # line 139
```

MemAvailable never fell below **9.9G** — far above the 4G threshold — so the guard was silent **exactly as designed**. Its header comment states the intent plainly:

> *"Nudges mayor only under genuine pressure (MemAvailable < AVAIL_MIN_GB) or when a candidate is flagged — not on every run (swap sitting full is steady-state)"*

Two problems:

1. **MemAvailable is the wrong health metric here.** Linux counts reclaimable page cache in MemAvailable, so it reads 10–18G "healthy" while swap is exhausted and commit is ~120% of RAM. The box had no *reclaim headroom* despite a comfortable-looking MemAvailable.
2. **`sw_used_pct` is computed (line 49) but never feeds `pressure`.** Swap exhaustion is reported and then discarded.

**The naive fix is wrong.** Adding `swap_free < X` or `Committed_AS > MemTotal` to the pressure gate would fire on **every run** — both conditions were chronic all night and across the 14-day boot. That is precisely the alert fatigue the original author avoided, and it would bury the signal.

## Recommendation

**Root (Stephanie-gated, not mine):** the box is chronically committed ~75G on 62.4G RAM with swap fully consumed. That is a capacity decision — postgres alone holds ~15G (`box_oom_gate_scix_postgres`, already on Stephanie's ledger). Either the footprint comes down (postgres tuning), RAM/swap goes up, or the box keeps living one spike away from this. No threshold tweak substitutes for that.

**Detection (city-infra floor, proposed — NOT yet built):** the highest-signal, lowest-false-positive lever is **not** a memory threshold. It is the supervisor's own
`tmux state cache: refresh failed in <duration>: tmux -u: context deadline exceeded`.
That line is sharp, non-chronic, and appeared **~45 minutes before the death** (07:26), escalating 3m14s → 18m22s. A sensor that watches supervisor.log for tmux-refresh failures and nudges mayor would have given ~45 min of warning with essentially zero false positives, because a multi-minute `tmux -u` is never steady-state. Proposed as a small additive sensor + order, in the shape of `bin/account-identity-check` + `orders/account-identity-check.toml`.

Secondary, if a memory signal is still wanted: alert on a **trend/delta** (swap_free pinned at ~0 *and* MemAvailable dropping run-over-run), not a static level — a level test on this box is either always-on or never-on.

## Follow-ups

1. Surface the detection proposal to mayor; build the tmux-refresh sensor on their go (city-infra floor, additive).
2. Root capacity call stays with Stephanie (`box_oom_gate_scix_postgres`).
3. Do **not** add a static swap/commit threshold to `resource-sweep` — it fires chronically on this box.
