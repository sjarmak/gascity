---
name: compass-scanners
description: Use when adding or debugging a periodic scanner, reading a reaper's audit log, wiring up evidence enforcement on bead closes, or routing epic-boundary review. Indexes the gc-order machinery, scanner scripts, and audit logs.
---

# Compass: scheduled scanners (`gc order`)

When working on `gc order`-driven scanners:

- `orders/*.toml` — one TOML per scheduled order (trigger + driver command); `gc order list` enumerates, `gc order check` shows due/not-due reason for each
- `bin/*-reaper`, `bin/claude-zombie-report`, `bin/memory-audit-issues`, `bin/epic-review-sweeper`, `bin/mail-redirect-to-mayor` — the actual scanner scripts (each idempotent, supports `--apply` and `--nudge-mayor`)
- `.gc/<scanner>.log` — JSONL audit per scanner (e.g. `.gc/close-gate-reaper.log`, `.gc/stale-claim-reaper.log`, `.gc/epic-review-sweeper.log`); `mayor-pattern-miner` reads these weekly
- `docs/conventions/scanners.md` — full order table, per-scanner failure modes, close-gate rule structure, epic-review chunk config

Positive target: when adding a new scanner, prefer `gc order` with cooldown or cron trigger over a systemd timer — orders are introspectable via `gc order check` / `gc order history` and survive supervisor restarts.

Hard rule: when `claude-zombie-report` fires, triage entries before killing — active long-running interactive sessions look identical to abandoned ones; check `CWD` and cross-reference with tmux.
