# Idle-session auto-reaper — calibration findings (dr-ae1b8)

Report-only calibration of the transcript-sustained-idle criterion, over
**168 hourly runs, 2026-06-24 → 2026-07-03 (~9 days)** of
`.gc/idle-session-report.log`. Source: `bin/idle-session-report`
(REPORT-ONLY; never kills). This report answers the Stephanie-gated question:
should the auto-kill phase be enabled, and at what threshold?

## Headline

**Do NOT enable transcript-idle auto-kill at the specified 240min threshold — on
9 days of data it would have fired ZERO times, and the transcript-idle signal is
structurally incapable of ever firing at any threshold ≥ ~1h.**

## The numbers

- **would_reap @ 240min over 168 runs: 0.** Not one live session crossed 240min
  (4h) transcript-idle in 9 days. False-positive-guard hits (attached/held/infra)
  were also 0 — because nothing was ever a candidate.
- **Worker transcript-idle distribution** (n=2,627 worker-observations):
  p50=27m, p90=52m, p95=56m, p99=59m, **max=59m**. No worker ever exceeded ~1h.
  Zero worker-observations at ≥2h. (`idle_min` is uncapped in source —
  `bin/idle-session-report:67` = `(now-newest)/60` — so the 59m ceiling is a real
  fleet property, not a measurement artifact.)
- **Dashboard-idle (`gc last_active`) vs transcript-idle divergence** (n=4,034):
  median over-report **59min**, max **6,986min (~5 days)**; **47%** of
  observations have dashboard over-reporting idle by >60min.

## What it means

The decisive observation is the two signals diverging on the *same live
sessions*: dashboard-idle climbs to ~5 days while transcript-idle stays ≤59min.
So those sessions' transcript files are being refreshed ~hourly by something
independent of dashboard-visible work — the fleet-wide system traffic (mechanic
ticks, patrols, routed-bead nudges, mail) injects `<system-reminder>` events into
every session's transcript at least hourly, resetting transcript-mtime.

Consequently:

- **Transcript-idle can never exceed ~1h fleet-wide**, so any auto-kill threshold
  ≥ 60min (and certainly the specified 240min) is a permanent no-op. Enabling it =
  taking on session-killing capability for zero demonstrated benefit.
- **Neither raw signal isolates "agent is doing no useful work":** dashboard-idle
  *over*-flags (47% over-report >1h → would over-reap actively-working sessions,
  the mayor's original correction), and transcript-idle *under*-flags (nudge
  traffic keeps it <1h → never reaps). Transcript-mtime is better than
  dashboard-idle but is confounded by injected system events.
- The genuine idle/stuck-worker recycling is already handled within the hour by
  the existing `stale-claim-reaper` + `resource-sweep` (claim/resource level),
  which is consistent with no live worker ever reaching even 1h transcript-idle.

## Recommendation (Stephanie-gated)

1. **Do NOT enable the auto-kill phase** on the transcript-idle ≥240min criterion.
   It cannot fire on the observed distribution; it only adds risk.
2. **Keep `bin/idle-session-report` / `orders/idle-session-report.toml` as a
   monitor.** It has already delivered its calibration value (proved dashboard-idle
   over-flags 47%, proved transcript-idle never reaches 1h).
3. **If idle/stuck-session auto-reaping is still wanted, the criterion needs
   redesign, not a threshold tweak:** a signal that isolates *agent-work* events
   (e.g. last user/assistant turn, excluding injected `<system-reminder>` events)
   rather than transcript file mtime. That is a separate spec — surface to
   Stephanie before building, since it carries session-killing capability.

## Reproduce

```
python3 -c 'analysis over .gc/idle-session-report.log'   # see dr-ae1b8 NOTES for the script
```
Key checks: `would_reap` sum over all runs = 0; worker `idle_min` max = 59;
`dash_idle_min - idle_min` > 60 in 47% of rows.
