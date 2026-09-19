---
title: execview — execution projection (gc beads show --execution)
description: Read-only JSON projection correlating a work bead with its workflow, step, session, and worktree state, and its stability contract for machine consumers.
---

**Status: Proposed**

## Summary

`gc beads show <id> --execution` renders `internal/execview.Projection`, a
typed, read-only view that correlates one work bead with:

- its own identity/status/priority/assignee (`work_bead`),
- the active graph workflow(s) that reference it (`workflows`), each with its
  current runnable/assigned/in-progress step (`current_step`),
- the live session executing the current step, or the workflow root's
  recorded session as a fallback (`session`),
- the target worktree's git state: path, branch, dirty flag, HEAD,
  commits ahead of the integration base, and whether HEAD is already
  reachable from that base (`worktree`),
- a flat list of human-readable `warnings` for every ambiguous or
  inconsistent situation the build encountered (conflicting `gc.work_dir` /
  `work_dir`, multiple live workflows, a stale session reference, an absent
  or non-git worktree, a probe error).

`internal/execview.Build` never mutates beads, sessions, worktrees, or refs;
it is a direct multi-store read, not the reconciler's cached API view, so it
can be fresher (and can disagree with) `gc beads show` without `--execution`.
It never guesses: any place the source data is ambiguous produces a warning
alongside the best answer it could still assemble, rather than silently
picking one candidate.

## Why this needs a design-capture entry

`--format=json` on this command is machine-readable output another tool or
script may parse (a "component" in the Phase 3.5 sense), not just a
human-facing rendering. This doc pins the JSON shape and what callers may
rely on so the shape does not drift silently under future changes to
`internal/execview`.

## JSON shape

```json
{
  "work_bead": {
    "id": "gc-1dm86y",
    "title": "port the July gc beads show --execution implementation onto current main",
    "status": "open",
    "priority": 2,
    "assignee": "gc-975982"
  },
  "workflows": [
    {
      "root_id": "gc-frw3u6",
      "formula": "mol-scoped-work",
      "status": "in_progress",
      "current_step": {
        "id": "gc-lx5vn2",
        "step_ref": "mol-scoped-work.implement.attempt.1",
        "status": "in_progress",
        "assignee": "gc-975982",
        "runnable": false
      },
      "root_session": "gascity-worker-pool"
    }
  ],
  "session": {
    "name": "gc-975982",
    "state": "",
    "last_active": null,
    "live": false
  },
  "worktree": {
    "path": "/home/ds/gascity-worktrees/.../gc-1dm86y",
    "source": "gc.work_dir",
    "present": true,
    "is_git": true,
    "branch": "work/gc-1dm86y",
    "dirty": true,
    "head": "3618fc23aa06b07c9da6617190951ea5d9f267f5",
    "commits_ahead": 0,
    "reachable_from_main": true,
    "err": ""
  },
  "warnings": [
    "root names session \"gascity-worker-pool\" but the active step is assigned to \"gc-975982\" (possible stale predecessor)",
    "referenced session \"gc-975982\" is not live"
  ]
}
```

Top-level fields:

| Field | Type | Presence |
|---|---|---|
| `work_bead` | object | always present |
| `workflows` | array of workflow objects | omitted (`omitempty`) when no live workflow correlates to the bead |
| `session` | object or absent | omitted when neither a step assignee nor a root's recorded session names one |
| `worktree` | object or absent | omitted when no `gc.work_dir` / legacy `work_dir` resolves for the bead or its workflow root(s) |
| `warnings` | array of strings | omitted when empty |

`workflows` can have more than one entry: a work bead can legitimately be
referenced by more than one live workflow root (rare, but possible after a
restart/resling); when it does, a warning is emitted and every correlated
root is still listed, never just one arbitrarily chosen root.

`worktree.commits_ahead` is `-1` when it could not be computed (no worktree
probe available, or the integration base ref did not resolve) — never `0`,
which is a real answer ("no commits ahead"). Consumers must treat `-1` as
"unknown," not "clean."

## Stability contract

- **Field presence, not field absence, is the compatibility surface.** New
  optional fields may be added to any object in a future version; consumers
  must ignore unknown fields. A field's removal or a change to an existing
  field's *meaning* (not just presence) is a breaking change requiring a new
  design-capture revision.
- **`warnings` strings are human-readable prose, not a stable enum.** Do not
  pattern-match on warning text in automation; the *fact* that a warning
  exists (non-empty `warnings` array) is the only stable signal. If a
  specific ambiguity needs to be machine-actionable later, it should get its
  own typed field instead of being parsed out of a warning string.
- **This is a read-only, local-only projection.** It reads live store state
  directly (every rig/city store reachable from the resolved city, per
  `internal/execview.Build`'s multi-store search) and does not route through
  the supervisor API; it is not available against a remote city
  (`gc beads show --execution` against `--city-url` exits 1). Do not build
  automation that assumes API-routed staleness semantics (`_cache_age_s`)
  apply here — they do not, because this path bypasses the cache entirely.
- **No verdicts.** The projection reports state; it does not decide whether
  a workflow is "stuck," a session is "healthy," or work is "done." Any such
  judgment belongs in the consumer, not in `internal/execview` or the
  `cmd_beads_execution.go` CLI wiring, per this repo's ZFC / "keep judgment
  out of Go" convention.

## Source

- `internal/execview/execview.go` — the typed projection and `Build`.
- `cmd/gc/cmd_beads_execution.go` — CLI wiring: `gc beads show <id>
  --execution`, text and `--format=json` rendering, session/worktree probe
  adapters.
- Ported from `work/gc-im90` (commit `c956330584`, 2026-07-22, never landed)
  onto current `main` under `gc-1dm86y`; the four required projection
  behaviors and the warnings-on-ambiguity discipline were verified against
  that commit's own test suite (`internal/execview/execview_test.go`,
  `cmd/gc/cmd_beads_execution_test.go`) rather than assumed from its commit
  message.
