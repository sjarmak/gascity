# Canonical Dolt — slice isolation

`gcdolt.slice` gives the canonical Dolt sql-server its own top-level systemd
user slice, so agent memory pressure can no longer OOM-kill the shared bead
store.

**Status: committed, not installed.** It pairs with a `gc` change that places
the managed server into the slice; installing it alone changes nothing, because
nothing puts Dolt there yet.

## Why it exists

Dolt was started by whichever process first needed it — the supervisor, a
control-dispatcher pane, an interactive command — and inherited that process's
cgroup. On 2026-07-21 it landed in `gascity-agents.slice`, and when
`gascity.slice` hit its 32G `MemoryMax` the kernel killed Dolt four times in one
morning. Each respawn took a new port, desyncing the supervisor's `GC_DOLT_PORT`
pin and opening the Dolt circuit breaker city-wide.

The four kills are at 09:28:56, 09:33:21, 09:42:00 and 09:49:05, counted from
the kernel ring buffer rather than from any summary:

```sh
journalctl -k --since "2026-07-21 00:00" --until "2026-07-22 00:00" \
  | grep -cE 'Killed process .*\(dolt\)'   # -> 4
```

Earlier prose about this incident circulated counts of 2, 3 and 5 and cited a
07:52 "global" kill; the kernel log records no non-memcg dolt kill that day, so
it is not carried here.

Two inheritances caused it, and both had to be fixed:

| Inheritance | Effect | Fixed by |
|---|---|---|
| Starter's cgroup | Dolt sat inside the 32G agent memcg | this slice + `GC_DOLT_SLICE` placement in `gc` |
| `oom_score_adj=200` from systemd's user manager | +12.5 GiB-equivalent badness on a 62.4 GiB host, so a ~1 GiB Dolt outranked genuinely large processes | the scope watchdog clears it before spawning |

Measured anon-rss at the four kills was 829,032-1,060,144 kB (0.79-1.01 GiB),
which is where the 4 GiB ceiling's headroom comes from.

`ManagedOOMPreference=avoid` is in the unit as defense-in-depth only. Every
2026-07-21 kill was `CONSTRAINT_MEMCG` (the kernel); `journalctl -u
systemd-oomd` for that day is empty, so oomd never had a say.

## Install

Do this together with the `gc` fix, as part of resume checklist `gc-x77t` — not
before.

```sh
install -m 644 gcdolt.slice ~/.config/systemd/user/gcdolt.slice
systemctl --user daemon-reload
```

The slice activates itself when the first process is placed into it, so there
is nothing to `start` or `enable`. It takes effect on the next Dolt respawn; it
does not move an already-running server.

That last point matters: left alone, "next respawn" may not arrive until the
next crash — the very event this prevents. Once the `gc` fix is installed,
restart the server deliberately rather than waiting, during a window where a
brief bead-store outage is acceptable.

## Verify

After the next respawn, from a neutral cwd:

```sh
DP=$(cut -d: -f1 ~/gas-city/.beads/dolt/.dolt/sql-server.info)
cat /proc/$DP/cgroup          # expect .../gcdolt.slice/...
cat /proc/$DP/oom_score_adj   # expect 0, not 200
```

The failure signature to watch for is either of those reverting: a cgroup
containing `gascity-agents.slice` means placement did not apply (check that
`systemd-run` works for the starting user), and `200` means the watchdog did not
clear the inherited adjustment.

## Do not

- Do not rename this to `gascity-dolt.slice`. systemd reads `a-b.slice` as a
  child of `a.slice`, which would put Dolt back inside the memcg that kills it.
- Do not add `MemoryHigh`. This cgroup is anon-dominated with swap disabled, so
  the throttle has nothing to reclaim and spins. `MemoryHigh=26G` was set on
  `gascity.slice` on 2026-07-20 ~22:2x and reverted 2026-07-21 09:23 — the
  thrash ran overnight, ~11h, with load sustained at 140-375 and
  `pgscan_direct` at 1.88B; load fell 375 -> 14 on the revert.
