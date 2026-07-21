---
name: compass-bead-dispatch
description: Use when dispatching beads to agents, handing off self-claimed beads, debugging silent worker skip, or attaching a formula to a bead. Indexes canonical files for `gc sling` and the `gc-sling` wrapper in this workspace.
---

# Compass: bead dispatch

When dispatching or debugging a stuck dispatch:

- `bin/gc-sling` — wrapper script (on PATH via `/home/ds/.local/bin/gc-sling`) that auto-injects `--nudge` and applies formula rules; default tool for all dispatch in this workspace
- `.gc/sling-intercept.yaml` — formula injection rules (Python-regex against bead ID); add a rule here, not in the script
- `.gc/sling-intercept.log` — JSONL audit; grep `formula_injected` and `passthrough` to debug rule misfires; `mayor-pattern-miner` reads this
- `docs/conventions/bead-dispatch.md` — claim handoff (`--reassign` vs `bd update --unassign`), `--on` vs `--formula` flag note, the warm-pool nudge story

Hard rules: don't sling a bead you personally claimed without `--reassign` (the pool-claim hook silently skips it); use `--on <name>` to attach a formula — `--formula` is a bool flag and `--formula <name>` errors with "requires 1 or 2 arguments".
