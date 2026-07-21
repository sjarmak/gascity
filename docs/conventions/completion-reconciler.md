# Completion reconciler

`bin/completion-reconciler` prevents assigned `in_progress` work from becoming a
permanent parking state. Beads/Dolt remains authoritative; recovery state is
stored on each bead rather than in a second database.

The reconciler runs from `completion-reconciler.timer`, a `systemd --user` timer
outside the Gas City supervisor and `gc order` failure domain.

## Contract

Default progress leases are priority-sensitive:

| Priority | Silence before checkpoint request |
| --- | ---: |
| P0 | 1h |
| P1 | 4h |
| P2 | 12h |
| P3 | 24h |
| P4 | 48h |

The first expired lease records `gc.completion_checkpoint_requested_at` and
nudges the assignee. The assignee has 30 minutes to finish, mark the bead
blocked with a concrete help request, or record:

```bash
bd update <id> \
  --set-metadata gc.progress_at=<UTC-timestamp> \
  --set-metadata gc.progress_evidence='<artifact, test, or checkpoint>'
```

For rig beads, use `gc bd --rig <rig> update`.

If no checkpoint arrives, the reconciler preserves all worktree, branch, and
routing metadata while changing the bead to `open` and unassigned. Existing
dispatch machinery can then claim it again. After two automatic recoveries, a
subsequent expired lease becomes `blocked` with `help_request` instead of
churning forever.

Human-assigned, `human-wip`, `health-soak`, `long_running=true`, held, and
`gc.completion_exempt=true` beads are never recovered automatically. Workflow
roots without an assignee are also excluded.

## Operations

```bash
# Read-only fleet preview
bin/completion-reconciler

# Scope a preview
bin/completion-reconciler --scope city
bin/completion-reconciler --scope gascity-packs

# Canonical install
services/completion-reconciler/deploy/install.sh

# Inspect
systemctl --user status completion-reconciler.timer
journalctl --user -u completion-reconciler.service
tail -f .gc/completion-reconciler.log
```

Thresholds can be overridden with `COMPLETION_RECONCILER_P0` through
`COMPLETION_RECONCILER_P4`, using durations such as `90m` or `2d`.
