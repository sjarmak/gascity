# PRD: Integrating agent-workflows Skills into Gas City

## Problem Statement

The user maintains 25 custom Claude Code skills in `~/agent-workflows` (diverge, converge, premortem, prd-build, focus, scaffold, etc.) and a Gas City instance orchestrating 9 research projects with 5 Claude OAuth accounts + Codex. These two systems share the same substrate (beads, Claude Code, git) but operate independently. The user wants to:

1. Use skills within Gas City-managed agent sessions
2. Continue independent work without mandatory mayor coordination
3. Maintain unified observability across all projects regardless of execution mode

## Research Findings (3 Independent Perspectives)

### Perspective 1: Primitive Mapping (Prior Art)

Gas City and agent-workflows share structural parallels:

| agent-workflows                     | Gas City                           | Compatibility                              |
| ----------------------------------- | ---------------------------------- | ------------------------------------------ |
| SKILL.md phases                     | Formula `[[steps]]` DAG            | High -- both are ordered, dependency-aware |
| Agent tool subagents                | `gc sling` + separate sessions     | Low -- different execution models          |
| `bd` bead tracking                  | `bd` bead tracking                 | Native -- same CLI, same substrate         |
| Skill pipelines (diverge->converge) | Convoys / convergence loops        | Medium -- different coordination models    |
| Prompt templates                    | Prompt templates                   | High -- both markdown                      |
| `/focus` lifecycle                  | Pool-worker/graph-worker lifecycle | High -- nearly identical patterns          |

**Key insight**: Beads are the shared lingua franca. Skills that use `bd` (focus, scaffold) need minimal adaptation. Skills that spawn Agent tool subagents (diverge, prd-build) face the biggest gap.

### Perspective 2: Four Technical Approaches Evaluated

**Approach A -- Formula Translation**: High effort, high durability. Reserve for prd-build/focus if hybrid proves insufficient.

**Approach B -- Prompt Template Injection**: Low friction but breaks resource accounting.

**Approach C -- Hybrid (Selected)**: Gas City orchestrates lifecycle; skills execute naturally within sessions. Minimal new infrastructure.

**Approach D -- Pack Distribution**: Premature for one user, one city. Revisit when there's distribution need.

### Perspective 3: Failure Modes & Risks

| Risk                                                     | Severity | Resolution                                       |
| -------------------------------------------------------- | -------- | ------------------------------------------------ |
| Credential file race condition                           | CRITICAL | Fix first: per-account CLAUDE_CONFIG_DIR         |
| AGENTS.md mandates `bd dolt push`                        | HIGH     | Update AGENTS.md for backend-agnostic commands   |
| Skill invocation via `-p` may not trigger slash-commands | HIGH     | Test empirically, fall back to formula if needed |
| Orphaned skill worktrees invisible to witness            | MEDIUM   | Acceptable for Phase 1, hook later               |
| Subagent activity invisible to Gas City                  | LOW      | Artifact-level observability is sufficient       |

## Convergence Decisions

After structured debate between pragmatist and architect perspectives:

1. **Credential isolation is non-negotiable and comes first.** flock serializes but doesn't isolate -- running sessions re-read the shared credential file. The fix is simple: each provider's env points at its existing `~/.claude-homes/accountN/.claude/` via `CLAUDE_CONFIG_DIR`. No new directories needed.

2. **Test the simple path before building formulas.** Try `gc sling codeprobe/claude-3 "/focus cp-a3f2"` first. If Claude Code's `-p` mode invokes skills, no formula needed. If not, write `mol-skill-work.formula.toml` as the bridge.

3. **AGENTS.md must be updated.** The file mandates `bd dolt push` as a session completion step. Gas City uses file backend. This is a guaranteed runtime failure. Make push conditional on backend, or create a `bd push` alias that no-ops on file backend.

4. **No pack yet.** Direct file references (`~/agent-workflows/skills/...`) work fine. One user, one city, 9 rigs. Add pack structure only when distributing to others or when direct references cause friction.

5. **Subagent observability is acceptable at artifact level.** Multi-agent skills (diverge, prd-build) produce markdown artifacts. Watching the artifact + bead notes is sufficient. No event bridge needed.

## Architecture

### Credential Isolation (Blocking -- Do First)

Replace the shared-credential-file approach with per-account config directories:

```bash
#!/usr/bin/env bash
# claude-account: launch claude with per-account credential isolation
set -euo pipefail
ACCOUNT="${1:?Usage: claude-account <1-5> [args...]}"
shift
export CLAUDE_CONFIG_DIR="$HOME/.claude-homes/account${ACCOUNT}/.claude"
if [ ! -f "$CLAUDE_CONFIG_DIR/.credentials.json" ]; then
  echo "ERROR: Credentials not found at $CLAUDE_CONFIG_DIR/.credentials.json" >&2
  exit 1
fi
exec claude --dangerously-skip-permissions "$@"
```

Each provider now runs with its own credential file, settings, and session data. No shared mutable state.

### Skill Invocation (Test-Then-Decide)

**Step 1**: Verify skills are accessible in Gas City sessions. Check that `~/.claude/commands/` or project-level `.claude/commands/` contains the skill symlinks/files.

**Step 2**: Test `gc sling codeprobe/claude-3 "/focus cp-a3f2"`. If the agent invokes the skill correctly, Phase 1 is done.

**Step 3 (fallback)**: If `-p` mode doesn't trigger skills, write a thin formula:

```toml
formula = "mol-skill-work"
version = 1
[[steps]]
id = "run"
title = "Execute skill on assigned bead"
description = "Read your bead. The description contains a skill invocation. Execute it."
```

### Bead Backend Alignment

Update `~/agent-workflows/AGENTS.md` to make dolt push conditional:

```bash
# Push beads (skip if using file backend)
bd dolt push 2>/dev/null || true
git push
```

Or better: update the AGENTS.md to use `bd sync` (if available) or remove the dolt-specific command entirely, since Gas City manages bead lifecycle.

### Independent Work Model

Preserved by design:

- All rig agents are pool `min=0` -- nothing spawns unless you sling work
- Running `claude-N` directly in any project works as before
- `observe_paths` captures independent session JSONL for dashboard visibility
- Beads created independently are visible to Gas City agents via `bd ready`
- Handoff: bead state persists across sessions; sling to a Gas City agent to continue

### Observability Strategy

| What                        | How                              | Coverage                 |
| --------------------------- | -------------------------------- | ------------------------ |
| Gas City agent sessions     | `gc status`, `gc events --watch` | Full                     |
| Independent claude sessions | `observe_paths` JSONL watching   | Session-level            |
| Skill artifacts             | File system + bead notes         | Sufficient               |
| Cross-project dashboard     | `gc dashboard serve`             | Full for GC-managed work |

## Success Criteria

1. No credential corruption when multiple accounts run concurrently
2. User can sling a skill invocation to any rig agent and it executes correctly
3. Independent `claude-N` work in any project remains unaffected
4. `gc status` shows activity across all 9 rigs
5. Beads created by skills are visible and manageable through Gas City

## Implementation Plan

### Phase 1: Foundation (This Session)

- [x] Fix credential isolation (CLAUDE_CONFIG_DIR per provider)
- [ ] Update AGENTS.md for backend-agnostic bead commands
- [ ] Verify skill accessibility in Gas City sessions
- [ ] Test: `gc sling <rig>/<agent> "/focus <bead-id>"`

### Phase 2: Pipeline Integration (Next Session)

- [ ] If Phase 1 test fails: write `mol-skill-work.formula.toml`
- [ ] Write `mol-research-project.formula.toml` (diverge->converge->premortem)
- [ ] Artifact handoff between pipeline stages via bead notes
- [ ] Test: full research-project pipeline across agents

### Phase 3: Refinement (When Needed)

- [ ] Pack structure if distributing to other users/cities
- [ ] Worktree hook integration for skill-created worktrees
- [ ] Event bridging if subagent visibility becomes a real problem

## Risk Annotations (Premortem Analysis)

Nine failure narratives were generated across three lenses (operational, architectural, scope). After deduplication, six distinct risks remain:

### RISK 1: CLAUDE_CONFIG_DIR strips agents of skills, hooks, and MCP servers [CRITICAL]

**Narrative**: `CLAUDE_CONFIG_DIR` redirects ALL config resolution, not just credentials. The per-account directories (`~/.claude-homes/accountN/.claude/`) contain only `.credentials.json`. Gas City agents launch with no installed skills, no hooks, no MCP servers, no custom settings. Skill invocation fails on the first attempt.

**Likelihood**: High | **Impact**: High

**Mitigation (BLOCKING)**: The `claude-account` script must either:

- (a) Symlink shared config into each account dir: `commands/`, `hooks/`, `skills/`, `mcp-configs/`, `settings.json`, `settings.local.json`, `rules/` -- via a setup script that runs idempotently, OR
- (b) Use a more surgical approach: only set credential-specific env vars rather than redirecting the entire config dir, OR
- (c) Copy credentials into a per-session temp dir that starts as a clone of `~/.claude/`

Option (a) is simplest. Add to `claude-account`:

```bash
for item in commands hooks skills mcp-configs settings.json settings.local.json rules agents; do
  src="$HOME/.claude/$item"
  dest="$CLAUDE_CONFIG_DIR/$item"
  [ -e "$src" ] && [ ! -e "$dest" ] && ln -s "$src" "$dest"
done
```

### RISK 2: Multi-agent skills silently fail under DISABLE_INTERACTIVITY=1 [HIGH]

**Narrative**: `/focus` works via `gc sling` because it's single-agent. `/prd-build` spawns Agent tool subagents with `isolation: "worktree"` -- these may fail or degrade under non-interactive mode. The user assumes all skills are "Gas City safe" because the simple test passed.

**Likelihood**: High | **Impact**: High

**Mitigation**: Classify skills into two tiers:

- **Tier 1 (single-session)**: focus, bisect, scaffold, distill, simplify, code-review -- safe for `gc sling`
- **Tier 2 (multi-agent)**: prd-build, diverge, converge, premortem, research-project, diverge-prototype, stress-test -- require testing before Gas City use

Add a mandatory Phase 1 gate: test `/prd-build --dry-run` through Gas City in addition to `/focus`. If Tier 2 fails, scope Phase 1 to Tier 1 only and defer Tier 2 to formula translation.

### RISK 3: Bead store split-brain between dolt and file backends [HIGH]

**Narrative**: Several rigs have months of dolt-backed bead history from independent work. Gas City uses file backend. `bd ready` in Gas City sessions sees different beads than `bd ready` in independent sessions. Two truths, one project.

**Likelihood**: Medium | **Impact**: High

**Mitigation**: Add a Phase 0 audit:

1. For each rig, detect current bead backend (`cat .beads/metadata.json | jq .backend`)
2. If dolt: either migrate with `bd backup restore` into file backend, or configure per-rig bead backend override in `city.toml` (if supported)
3. Ensure `bd` resolves the same store regardless of whether the session is Gas City-managed or independent

### RISK 4: Bundled scope kills Phase 2 urgency [HIGH]

**Narrative**: Problem A (skills in GC sessions) and Problem B (multi-skill pipeline orchestration) are bundled. Problem A reaches "good enough," urgency evaporates, Problem B is never built.

**Likelihood**: High | **Impact**: Medium

**Mitigation**: Split into two PRDs:

- **PRD-A**: "Skills in Gas City sessions" -- 1-week deadline, `gc doctor` check confirms skill dispatch
- **PRD-B**: "Pipeline orchestration" -- independently scoped, gated on PRD-A, with its own research phase

### RISK 5: Gas City path is slower than independent path for daily work [MEDIUM]

**Narrative**: Independent: `cd ~/codeprobe && claude-3` then `/focus cp-a3f2` (5 seconds). Gas City: `gc sling codeprobe/claude-3 "/focus cp-a3f2"` + `gc session peek` (30+ seconds). Friction exceeds value for single-skill work.

**Likelihood**: High | **Impact**: Medium

**Mitigation**: Accept that single-skill daily work stays independent. Scope the integration to multi-agent pipelines and cross-project coordination where Gas City provides genuine value. Add convenience aliases:

```bash
# gc-focus: auto-resolve rig from bead prefix, pick idle agent
gc-focus() { gc sling "$(bd prefix $1)/claude-1" "/focus $1"; }
```

### RISK 6: 54-agent namespace explosion [LOW]

**Narrative**: 9 rigs x 6 providers = 54 agent pools in `gc status`. The user must remember rig/agent names. Cognitive overhead for no benefit since accounts are interchangeable.

**Likelihood**: Medium | **Impact**: Low

**Mitigation**: Set `default_sling_target` per rig in `city.toml` so `gc sling codeprobe "..."` auto-resolves to an available agent. Consider reducing to 2-3 providers per rig instead of all 6.

---

## Risk Priority Matrix

```
           HIGH IMPACT          LOW IMPACT
HIGH    | R1: Config dir     | R5: Friction
LIKELY  | R2: Multi-agent    | R6: Naming
        | R4: Scope creep    |
        |                    |
LOW     | R3: Bead split     |
LIKELY  |                    |
```

**Phase 1 must address R1 and R3 before any skill invocation testing.**
R2 determines whether Phase 1 scope includes Tier 2 skills or not.
R4 and R5 are process/scope decisions, not technical fixes.
