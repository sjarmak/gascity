# Supervisor OOM — pprof findings (2026-04-11)

## Profile captured

Binary heap profile at `/tmp/pprof-captures/heap-20260411-211459.pb.gz` (72 KB).
Supervisor instance: PID 3557175, version 0.13.5, ~34 min uptime at capture time.
pprof endpoint: `http://localhost:6060/debug/pprof/` (already built in, `internal/api/supervisor.go:8,94-96`).

## Key stats

| Metric            | Value                       | Notes                                             |
| ----------------- | --------------------------- | ------------------------------------------------- |
| Live heap (Alloc) | 40 MB                       | Current Go heap in use                            |
| Total alloc       | 24 GB                       | Cumulative allocations since start                |
| Sys reserved      | 95 MB                       | OS memory reserved for Go runtime                 |
| Alloc rate        | ~700 MB/min                 | 24 GB / 34 min                                    |
| VmPeak (proc)     | 3.3 GB                      | Virtual memory peak (mmap'd, mostly not resident) |
| VmHWM (proc)      | 108 MB                      | Resident set high-water mark (main process only)  |
| RSS (systemd)     | 251 MB current, 934 MB peak | Includes bd/dolt subprocesses in cgroup           |
| Previous instance | 7.5 GB peak over 19 hours   | 5× OOM kills the day before                       |

## Top allocation site

The dominant allocation path (from the heap profile text output):

```
main.reconcileCities.func26                 → cmd_supervisor.go:1254
  main.(*CityRuntime).run                   → city_runtime.go:322
    main.(*CityRuntime).tick                → city_runtime.go:415
      main.(*memoryOrderDispatcher).dispatch → order_dispatch.go:130
        BdStore.List (or .Create)           → bdstore.go:668 (or :427)
          main.bdRuntimeEnv                 → bd_env.go:98
            main.rawBeadsProvider           → providers.go:236
              main.loadCityConfig           → cmd_agent.go:23
                toml.Decode(city.toml)      → decode.go:36
```

**The city config (`city.toml`) is re-parsed from TOML on every reconciler tick, on every `BdStore.List` or `BdStore.Create` call, for every rig.** With 16 rigs and a short tick interval, this generates enormous allocation throughput.

## Why 40 MB live but 7.5 GB peak

The Go heap is well-managed (40 MB live) so this isn't a classic leak. The 7.5 GB peak was likely caused by:

1. **Subprocess memory.** Each `bd list` / `bd create` spawns a bd subprocess (134 MB binary) that connects to dolt, reads data, and exits. With 16 rigs being reconciled in parallel, the cgroup RSS can spike to 16 × (bd RSS + dolt connection overhead). If bd holds ~200-400 MB during a list operation on a large bead store, 16 concurrent bd processes = 3-6 GB in the cgroup.

2. **bd list timeouts.** Historical: 3,111 "bd list: timed out after 120s" events. Each stuck bd subprocess holds memory for up to 120 seconds before being killed. If multiple rigs are timing out simultaneously, their memory stacks.

3. **Allocation pressure → Go runtime MADV_FREE.** At 700 MB/min allocation rate, the Go runtime maps many OS pages for temporary objects. Linux doesn't immediately reclaim MADV_FREE pages, so VmRSS can grow even though live heap is small. Over 19 hours at this rate, the runtime may hold significant unreturned pages.

## Recommendations

1. **Cache `loadCityConfig` result.** The city config doesn't change between ticks. Parse once, cache, invalidate on SIGHUP or file-change notification. This would reduce allocation rate by an estimated 50%+.

2. **Bound concurrent bd subprocess invocations.** If the reconciler launches bd operations for all 16 rigs in parallel, the peak cgroup RSS scales linearly. A semaphore limiting concurrent bd invocations to 4-6 would cap the memory ceiling at ~2-3 GB.

3. **Profile a long-running instance.** The 34-minute profile I captured is too young to show the growth pattern. Capture heap profiles at 1h, 4h, 8h, and 16h intervals on the next supervisor instance to find the inflection point. Use:

   ```bash
   for h in 1 4 8 16; do
     sleep ${h}h && curl -s http://localhost:6060/debug/pprof/heap > /tmp/pprof-captures/heap-${h}h.pb.gz
   done
   ```

4. **Set GOMEMLIMIT.** As a safety net, set `GOMEMLIMIT=4GiB` in the systemd unit to pressure Go's GC before hitting the systemd-oomd threshold. This doesn't fix the root cause but prevents the OOM kills.
