# Skill Tier Classification for Gas City

Skills from `~/agent-workflows` classified by Gas City compatibility.

## Tier 1: Single-Session (Safe for `gc sling`)

These skills run within a single Claude session and don't spawn Agent tool subagents.
They work under `DISABLE_INTERACTIVITY=1`.

| Skill          | What it does                                                       |
| -------------- | ------------------------------------------------------------------ |
| `/focus`       | Single-bead execution loop: plan, execute, simplify, review, close |
| `/bisect`      | Binary search for root cause with pass/fail oracle                 |
| `/scaffold`    | Build-order planning from a chosen design                          |
| `/distill`     | Progressive compression of a large artifact                        |
| `/aside`       | Quick side question without losing context                         |
| `/code-review` | Code review of recent changes                                      |
| `/simplify`    | Simplify code after generation (built-in Skill)                    |

**Usage**: `gc sling <rig>/<agent> "/<skill> <args>"`

## Tier 2: Multi-Agent (Needs Testing Before Gas City Use)

These skills spawn Agent tool subagents with `isolation: "worktree"` or parallel agents.
May fail or degrade under `DISABLE_INTERACTIVITY=1`.

| Skill                | What it does                               | Agent pattern           |
| -------------------- | ------------------------------------------ | ----------------------- |
| `/diverge`           | Multi-perspective divergent research       | N parallel agents       |
| `/converge`          | Structured debate and refinement           | Agent teams             |
| `/premortem`         | Prospective failure narratives             | N parallel agents       |
| `/stress-test`       | Adversarial attack surface analysis        | N parallel agents       |
| `/diverge-prototype` | Divergent prototyping in worktrees         | N agents + worktrees    |
| `/crossbreed`        | Structural recombination of designs        | 2-3 agents              |
| `/prd-build`         | PRD decomposition and parallel build       | Many agents + worktrees |
| `/research-project`  | Full diverge->converge->premortem pipeline | Chained multi-agent     |

**Status**: NOT TESTED with Gas City. Before using, run:

```bash
gc sling <rig>/<agent> "/prd-build --dry-run <path>"
```

If Tier 2 fails under Gas City, fall back to formula translation (`mol-do-work.toml`).

## Tier Classification Criteria

- **Tier 1**: No Agent tool calls, no worktree isolation, single context window
- **Tier 2**: Uses Agent tool, spawns subagents, or creates worktrees

## Notes

- All skills are accessible via symlinked `~/.claude/commands/` in all 5 account dirs
- `simplify` is a built-in ECC Skill (invoked via Skill tool), not a commands/ file
- Tier 1 skills that use `bd` (beads) work natively since beads are the shared substrate
