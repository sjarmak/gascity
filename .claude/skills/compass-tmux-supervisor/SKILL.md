---
name: compass-tmux-supervisor
description: Use when working on tmux socket issues, the supervisor service, session creation failures, or session-name collisions. Indexes canonical files for the tmux/supervisor subsystem of the Gas City ds-research workspace.
---

# Compass: tmux + supervisor

When tmux or the supervisor is misbehaving, check these in order:

- `~/.gc/supervisor.log` — append-only supervisor log; grep for `tmux state cache: refresh failed` and `session name already exists`
- `tmux -L ds-research list-sessions` — live sessions on the city's NAMED socket (`/tmp/tmux-1000/ds-research`, not the default tmux socket)
- `.gc/session-name-locks/*.lock` — filesystem locks for session names; stale ones block creation of a fresh session with the same name
- `docs/conventions/tmux-supervisor.md` — recovery sequences, supervisor service details, full city-wide reset playbook, mysterious-stop diagnosis via `/tmp/supervisor-stop-caller.log`

Hard rule: don't start the supervisor before tmux is alive on the `ds-research` socket — the reconciler will drain anything it doesn't recognize as orphans, and the chicken-and-egg makes recovery harder.
