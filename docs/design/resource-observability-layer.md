# Resource observability layer + MCP hygiene (durable fix)

**Owner:** city-infra-pl · **Raised:** 2026-07-07 (mayor, after a hand-diagnosed
memory-pressure incident) · **Status:** spec, ready to build

## Why

On 2026-07-07 the box was memory-saturated (swap 100% full, MemAvailable ~9G on
62G). Root causes were all *creep that no one was watching*:

- 3 `gc nudge poll` sidecars hot-looping at 329/367/509% CPU (one 60 min old) —
  the `nudge-poll-reaper` (kills >50% CPU, >60s) missed them because the CPU
  spiral **starved the reaper's own scheduling gate**.
- `code-intel-copilot` MCP server silently regrew to **140 procs / 5.05 GB**
  (it was trimmed for zero usage on 2026-06-16 and grew back unnoticed because
  `mcp-audit` is weekly + report-only).
- `fal-ai` MCP server: **27 procs / ~1.4 GB at 0 tool-calls in 3 days** — pure
  dead weight, spawned per-session fleet-wide.
- 2 `scix.mcp_server` dupes idle 2.8 days; `stale-scix-mcp-reaper` (hourly) only
  reaps orphans, not cold-but-parented dupes.

Every one of these was found by hand in a crisis. The reapers are *reactive*
(kill after it's bad) and *per-symptom*. What's missing is a **proactive
early-warning + trend layer** that catches the creep days before saturation.

## Part A — MCP config hygiene trim (do first, biggest reclaim)

**STEP 0 — pin the config source (do NOT skip; my location guesses were wrong).**
`fal-ai` and `code-intel-copilot` are spawned per-session (fleet-wide fan-out),
but they are **NOT** declared in any of these (all checked 2026-07-07, none
contain them): home `.claude.json` root or `projects.<path>.mcpServers` in
`/home/ds/.claude-homes/account{1..5}/.claude.json`, the nested
`.claude/.claude.json` top-level, or any project `.mcp.json`
(`/home/ds/projects/*/.mcp.json`). A memory note claiming "top-level in the
nested per-home config" is **unverified and contradicted** by direct inspection
— do not trust it. Pin the real source authoritatively by tracing a live proc:
`pgrep -f code-intelligence-digest/src/mcp/server.ts` → its parent `claude` PID
→ inspect that process's **full** `/proc/<ppid>/cmdline` and
`/proc/<ppid>/environ` for `--mcp-config` / `--strict-mcp-config` / an MCP env
var, and check `~/.claude-homes/<home>/.claude/settings.json` `mcpServers` /
`enabledMcpjsonServers`. Only edit once the source is confirmed and snapshotted.

Verified usage (real `tool_use` invocations, not tool-availability listings):

| Server | procs / RSS | real calls (3d) | action |
| --- | --- | --- | --- |
| `fal-ai` (`npx fal-ai-mcp-server`) | 27 / ~1.4 GB | **0** | **remove** from all project sections, all 5 homes |
| `code-intel-copilot` (`tsx code-intelligence-digest/src/mcp/server.ts`) | 140 / 5.05 GB | 102 | **scope**: keep ONLY for projects `website` (14 of 15 calling transcripts) and `code-intelligence-digest`; remove from all other project sections |

Reclaim: ~1.4 GB (fal-ai) + most of the 5 GB code-intel fan-out (keep it for 2
projects instead of ~28).

Requirements:
- **Snapshot every home `.claude.json` before editing** (bak-before-flip).
- Scripted (`jq`/python) edit, not by hand; validate JSON after each write.
- These are read by ~26 live sessions — a malformed edit breaks session launch
  fleet-wide. Verify each file parses, then let sessions pick up the change on
  natural reconcile cycle (do NOT force-restart the fleet — spawn-storm guard).
- Confirm post-change: `code-intel-copilot` still spawns for website sessions;
  `fal-ai` no longer spawns anywhere.

## Part B — Resource observability layer (the durable fix)

Build in the city idiom (an `orders/*.toml` job + a snapshot script + a JSONL
time-series + threshold alerts). This ADDS an early-warning layer; it does not
replace the curative reapers.

### B1. Sampler order `orders/resource-observability.toml` (every 5m)

Snapshot script captures one JSON line to `.gc/observability/resource-metrics.jsonl`:
- `MemAvailable`, `SwapFree`, cumulative swap-in/out pages, `Committed_AS`, load1/5/15
- supervisor cgroup `MemoryCurrent`
- top-15 procs by RSS
- **per-MCP-server: proc count + summed RSS** (the fan-out metric that caught
  code-intel-copilot — the single most valuable new signal)
- any proc >150% CPU (hot-loop class)
- tmux session count on the `ds-research` socket

### B2. Threshold alerts (same order, cooldown/de-dup, mail mayor + Slack)

Fire on:
- `MemAvailable` < 3 GB
- `SwapFree` < 300 MB for ≥2 consecutive samples **and** ≥64 MB swap I/O in the latest 5-minute sample. Full-but-cold swap is retained occupancy, not an active performance incident.
- any MCP server > 2.5 GB **or** > 60 procs (fan-out creep)
- any single proc > 200% CPU for ≥2 samples (hot-loop)
- supervisor cgroup > 8 GB (O(n²) creep, gc-g421k)
- any MCP server with procs+RSS but **0 tool-calls in 24h** (dead-weight
  detector — the code-intel/fal-ai pattern, generalized)

Route: gascity-specific → `#gascity-maintenance`; cross-cutting → mayor mail.
De-dup so a standing condition alerts once per cooldown, not every 5m.

### B3. Daily creep report (rollup)

Diff today's per-MCP-server RSS + supervisor cgroup against a 7-day baseline;
flag anything growing monotonically. This is what would have caught
`code-intel-copilot` climbing back to 5 GB over three weeks. One line to
`#all-agent-city` in the morning digest.

### B4. Retention

Rotate/trim `resource-metrics.jsonl` (reuse `janitor-log-rotate` patterns);
keep ~14 days.

### B5. (stretch, phase 2) gascity-dashboard resource panel

Surface the time-series on the existing dashboard. Not required for v1.

## Part C — Harden the reapers this incident exposed

- `nudge-poll-reaper`: run at elevated priority / decouple from the gate it
  shares with the sessions it reaps, so it survives the CPU starvation it
  exists to cure. Durable cure remains the source backoff (`gc-b9w88`, held).
- `stale-scix-mcp-reaper`: widen criteria to reap **cold** dupes (idle > N h),
  not only orphans.
- Retire `mcp-audit`'s weekly report-only role once B1/B2 subsume the fan-out
  metric with alerting.

## Related beads

`gc-g421k` (supervisor O(n²)), `gc-b9w88` (nudge backoff, source), `gc-456423`
(city-infra-pl escalation triggers). This layer is the observability comple-
ment to those point fixes.
