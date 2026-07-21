---
name: compass-gc-binary
description: Use when working on the installed `gc` binary, rebuilding via gcsync, or referencing the oversight-rig pack from city.toml. Indexes the two-worktree layout that keeps the binary off contributor PR branches.
---

# Compass: gc binary and oversight-rig pack

When touching the `gc` binary or city.toml pack references:

- `/home/ds/gascity-main` (branch: `main`) — the ONLY tree built from; `gcsync` operates here exclusively
- `/home/ds/gascity` (branch: any PR) — contributor tree; never built from, never referenced by `city.toml`
- `/home/ds/gascity-packs-worktrees/oversight-rig` (branch: `gascity-pr`) — the ONLY path `city.toml` references for the oversight-rig pack
- `docs/conventions/gc-binary.md` — `gcsync` rules, worktree recreation, when to restart the supervisor after sync, retirement plan once pack lands on `main`

Hard rules: don't build `gc` from `/home/ds/gascity` (binary drifts onto whatever PR branch is parked there); don't put `/home/ds/gascity/examples/oversight-rig` in city.toml (breaks the city every time the contributor tree swings to a branch without the pack).

Positive target: run `gcsync` at session start — it's a fast no-op when `/home/ds/gascity-main` is already at origin/main HEAD, and otherwise rebuilds and installs the binary.
