# RCA — order-firing stall + /status ~2min hang (dr-7smz8)

**Date:** 2026-07-14 (~03:1x UTC / 23:1x EDT 2026-07-13)
**Author:** city-infra-pl
**Bead:** dr-7smz8 (P1). Authorized by mayor (mail gc-481947) + Stephanie to
execute GC_PPROF + restart and land the fix.
**Method:** enabled the supervisor pprof endpoint, captured goroutine dumps
during live hangs, traced to source in `/home/ds/gascity-main`.

## TL;DR

The "order-firing stall" and the "/status ~2min hang" are **two distinct
root causes**, not one. The dr-7smz8 hypothesis (`ScopedStoreLike` mutex) was
close on the *shape* (an unguarded synchronous call the 1s guard misses) but
wrong on the *location*.

1. **Order-firing stall (RESOLVED tonight, in-floor):** the gc file-backed
   bead store `.gc/beads.json` had bloated to **168 MB**. Every order
   cursor-advance / run-create reloads and JSON-unmarshals the *entire* file
   under the FileStore flock+mutex, serializing all order dispatch.
2. **/status ~2min hang (gc-source, surfaced to mayor):** the non-lite
   `/status` handler runs `cachedStoreHealth` → `computeStoreHealth`, which does
   an **uncancellable synchronous disk walk of the 2.5 GB `.beads/dolt` dir**
   plus a closed-inclusive Dolt history row count. Neither is bounded by the 1s
   `statusStoreReadTimeout`. It does **not** block order dispatch.

## Evidence

### Part 1 — order-firing stall = FileStore full-reload under mutex

Goroutine dump (`docs/diag/goroutine-dump-boot-*.txt`) caught the order
dispatcher blocked acquiring the FileStore mutex:

```
beads.(*FileStore).Create            filestore.go:186   [sync.Mutex.Lock, BLOCKED]
 <- orders.(*Store).CreateRun        store.go:252
 <- memoryOrderDispatcher.launchResolvedDispatch  order_dispatch.go:725
 <- CityRuntime.dispatchOrders       city_runtime.go:1315  (reconcile tick)
```

The **holder** of that mutex, same dump:

```
beads.(*FileStore).Update            filestore.go:216
 -> reloadFromDisk                   filestore.go:120
   -> json.Unmarshal(<0xa819991 bytes = ~168 MB>)   encoding/json
 <- orders.(*Store).SetCursor        store.go:287
 <- memoryOrderDispatcher.dispatchExec  order_dispatch.go:1326
```

`filestore.go`: every write path (`Create:188`, `Update:212`, `Close:259`)
takes `fs.locker.Lock()` — a cross-process flock on `.gc/beads.json.lock`
(`filestore.go:66 NewFileFlock`) — then `reloadFromDisk()` re-reads and
unmarshals the whole file. At 168 MB each reload is multi-second; concurrent
order fires + session reconciles serialize behind it → order-firing freezes.
This is the same failure class as the 2026-06-12 incident documented in
`bin/beads-json-compact-order-tracking.py` ("225 MB / 320K beads starves the
order dispatch loop entirely").

Store composition (79,210 beads): **76,182 closed (96%)** — 19,888 order-run,
6,346 nudge, tens of thousands of session beads. Only ~3,028 open/in_progress
are live state. The bloat is **24,550 closed non-order-tracking beads >30d old
that no reaper covers in the file store** (`bead-prune-reaper` targets the Dolt
rig stores; `bead-janitor` only *closes*, and is `.disabled`).

### Part 2 — /status hang = uncancellable storeHealth walk (gc-source)

Dump during a live /status hang (`docs/diag/goroutine-dump-hang-*.txt`): three
concurrent `buildStatusBody` goroutines at `handler_status.go:288`, all
`[runnable]` (CPU-bound, **not** lock-blocked). Line 288 =
`cachedStoreHealth(ctx, time.Now())`.

`internal/api/store_health.go` `computeStoreHealth`:
- `storehealth.WalkSize(StorePath(cityPath))` — comment: "a synchronous,
  uncancellable disk walk". `StorePath` = `.beads/dolt` = **2.5 GB / 4,026
  files**.
- `countBeadStoreRows(...)` — "closed-inclusive query ... always hydrates".
- 30 s memoized (`storeHealthCacheTTL`), but the dashboard poller stacks
  cold-cache misses → multi-minute tails.

Decisive proof: `/status?lite=true` (omits the storeHealth/session/work blocks)
returns in **0.38 s cold / 0.0007 s warm**; the full body **>2 min**. The delta
is exactly the storeHealth block.

**Decoupling:** order-firing recovered (16 orders fired in 15 min, incl.
`beads-health` which had been stalled since 21:25) *while* /status still hung —
confirming the two are independent subsystems.

## Actions taken (in-floor)

1. `pprof.conf` drop-in → `GC_PPROF=1`; `daemon-reload` + supervisor restart
   (mayor+Stephanie authorized; verified safe). pprof now on
   `127.0.0.1:6060` (localhost-only, diagnostic).
2. Ran `bin/beads-json-compact-order-tracking.py` with `COMPACT_APPLY=1
   COMPACT_CLOSED_ANY_DAYS=30` — the sanctioned 30d window, matching the
   standing `bead-prune-reaper` policy. Safe against the live supervisor: the
   compactor holds `LOCK_EX` on `.gc/beads.json.lock` (the same flock every
   FileStore write acquires), backs up, and does an atomic tempfile+rename.
   Result: 168 MB → 82 MB *immediately after compaction*. **The durable
   reduction is the bead-count: 79,210 → 54,656 beads (−31%).** The 82 MB was
   transient compact encoding — `FileStore.save()` uses
   `json.MarshalIndent(fd, "", "  ")` (filestore.go:580), so the next full
   rewrite re-expands to pretty-print; steady state settles at **~116 MB
   (~168→116, −31%)**. Backup: `.gc/beads.json.bak-compact-20260714T031033Z`.
   Store verified valid (54,648 beads at compaction; seq advanced under live
   writes; 3,025 open / 4 in_progress preserved).

   **Cheap gc-source win (add to Part-2 routing):** the file store is
   machine-only yet persisted pretty-printed (`MarshalIndent`). Switching to
   `json.Marshal` would ~halve on-disk size *and* every `reloadFromDisk` parse
   cost permanently — directly attacking the Part-1 reload latency with a
   one-line change.

## Remaining / surfaced to mayor

- **gc-source (Part 2):** bound `WalkSize` + `countBeadStoreRows` by
  `ctx`/timeout (the source comment defers this "until it shows up in profiles"
  — it now has), or raise `storeHealthCacheTTL`, or make the dashboard poller
  use `?lite=true`. Upstream-gated.
- **Durability (Part 1): DONE — staged, awaiting promotion.**
  `orders/beads-json-compact.toml` (added 2026-07-14, cooldown 24h) now runs the
  compactor on a cadence. It **ships in dry-run**; per the city's janitor
  convention a human reviews one report and flips `COMPACT_APPLY = "1"` in the
  order file, annotated with who and when. Until that flip, the file still
  re-bloats.

  **Correction to "Actions taken" #2 — the 30d window is now a no-op.** The 30d
  generic pass worked once because it drained a historical backlog of 24,550
  long-closed beads. That backlog is gone, and the regrowth is *young*: measured
  on the live store 6h after the compaction (2026-07-14 05:08, 55,272 beads),
  **30d deletes 828 beads (1.5%) while 7d deletes 26,621 (48%)**. The store
  regrows ~3.7 MB/h (82 MB → 105 MB in 6h) and returns to stall size in about a
  day, so a 30d retention window releases nothing on the timescale the problem
  recurs on. Anyone reaching for `COMPACT_CLOSED_ANY_DAYS=30` during the next
  stall will watch it delete ~1.5% of the store and conclude the compactor is
  broken. The order uses **7d**, matching the TTL the primary order-tracking
  pass already applies and gc's own `sweepClosedOrderTrackingRetentionBounded`
  policy; steady state is ~28.6K beads.

  Two compactor bugs found and fixed while wiring this up
  (`bin/beads-json-compact-order-tracking.py`):
  - the write was gated on `deleted == 0`, which counts only the *order-tracking*
    pass — a run that deleted 26,000 generic beads but zero order-tracking ones
    printed its results and returned **without writing**. Now gated on the total.
  - the rewrite replaced `raw["beads"]` but left `raw["deps"]` untouched, so each
    compaction stranded dependency edges. 19 of 60 edges were already inert, and
    open bead `gc-1927` was left blocked on `gc-1926`, a bead a prior compaction
    had deleted. Dangling edges are now pruned in the same pass, and a non-closed
    bead in the delete set aborts the write.
- **Mitigation for WalkSize cost:** `.beads/dolt` is 2.5 GB; effective Dolt GC
  (nightly `dolt-gc-maintenance`) shrinks the walk. Partial, not the fix.
- **pprof drop-in** is diagnostic-only; remove after the Part-2 source fix
  lands.
