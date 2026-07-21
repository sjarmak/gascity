# dr-h2sz — local reviewer launch and worktree-ownership map

Date: 2026-07-20  
Scope: read-only ownership mapping; no reviewers launched

## Conclusion

Four local launch implementations can place multiple write-capable reviewers
in one checkout:

1. global `/review`;
2. global `/review-pr` (Codex and Gemini share the PR-head worktree);
3. `/gascity-dashboard-review-pr`;
4. the oversight-rig `mol-pr-ship` review panel.

Several city skills, prompts, commands, and orders are indirect entry points to
those four implementations. Prompt-only “read-only review” language is not an
enforced capability boundary: every local Claude reviewer definition has
`Bash`, `security-reviewer` and `database-reviewer` additionally have
`Write`/`Edit`, generic review agents are not tool-restricted here, Codex is
sometimes launched with full-access flags, and Copilot is launched with all
tools. Any reviewer with Bash can modify files or invoke `git stash`,
`git checkout`, or `git clean`.

The existing `/review-pr` worktrees protect the operator's original checkout,
but they do not isolate Codex from Gemini inside the shared PR-head worktree.
None of the four paths proves post-review tree identity before consuming
findings. Force-removing review worktrees can erase evidence of reviewer
mutations.

## Capability inventory

Local reviewer definitions under `/home/ds/.claude/agents/`:

| Reviewer | Declared tools | Enforced read-only? | Shared-tree mutation possible? |
|---|---|---:|---:|
| `code-reviewer` | Read, Grep, Glob, **Bash** | No | Yes |
| `rust-reviewer` | Read, Grep, Glob, **Bash** | No | Yes |
| `go-reviewer` | Read, Grep, Glob, **Bash** | No | Yes |
| `typescript-reviewer` | Read, Grep, Glob, **Bash** | No | Yes |
| `cpp-reviewer` | Read, Grep, Glob, **Bash** | No | Yes |
| `java-reviewer` | Read, Grep, Glob, **Bash** | No | Yes |
| `kotlin-reviewer` | Read, Grep, Glob, **Bash** | No | Yes |
| `python-reviewer` | Read, Grep, Glob, **Bash** | No | Yes |
| `flutter-reviewer` | Read, Grep, Glob, **Bash** | No | Yes |
| `security-reviewer` | Read, **Write, Edit, Bash**, Grep, Glob | No | **Explicitly yes** |
| `database-reviewer` | Read, **Write, Edit, Bash**, Grep, Glob | No | **Explicitly yes** |

“You DO NOT refactor” in some role prompts is advisory only. The AOA incident
demonstrates that a verification prompt can legitimately request mutation
testing, and Bash remains sufficient to mutate and restore a checkout.

## Direct fan-out implementations

### 1. Global `/review` — unsafe shared live checkout

Source: `/home/ds/.claude/skills/review/SKILL.md`.

- Lines 8-12 define a parallel quartet.
- Lines 19-46 dispatch three `general-purpose` reviewers in parallel.
- Lines 50-68 launch Codex concurrently with
  `codex exec -s danger-full-access ... -C <repo-root>`.
- Lines 72-103 add Copilot with `--allow-all-tools` and require all five calls
  to run concurrently.
- Phase 4 expects the orchestrator to fix findings in the same checkout.

Ownership: every reviewer runs against the caller's current repository root or
the same `/tmp/review-diff.txt`; no per-reviewer worktree/copy exists.

Risk: **critical collision surface**. General-purpose Claude agents, Codex
full-access, and Copilot all have write-capable execution against one live
checkout. This path can reproduce the dr-05cx class directly.

Indirect entry points:

- `/home/ds/.claude/skills/gascity-ship/SKILL.md` Stage 2b invokes `/review` and
  iterates fixes in place.
- Any user/agent directly invoking the globally installed `/review` skill.

### 2. Global `/review-pr` — partial isolation, shared head worktree

Source: `/home/ds/.claude/skills/review-pr/SKILL.md`.

- Lines 14-45 define parallel Claude/Codex/Gemini review.
- Lines 142-166 (PR mode) and 274-282 (local mode) create two detached
  worktrees: one base worktree for Claude and one PR-head worktree.
- Lines 361-390 run Claude alone in the base worktree but run **Codex and
  Gemini concurrently in the same PR-head worktree**.
- Lines 658-686 launch Codex with
  `--dangerously-bypass-approvals-and-sandbox`.
- Lines 758-781 launch Gemini with `--yolo`.
- Lines 422-430 remove both worktrees with `--force`; no clean-tree/tree-hash
  assertion precedes synthesis or cleanup.

Ownership: base reviewer isolated; head reviewers not mutually isolated.

Risk: **high collision surface**. The original repository is protected, and
the initial review refs are exact, but Codex and Gemini can observe or overwrite
each other's working-tree changes. Cleanup can discard the evidence. Review
findings are not proven to have been produced from an unchanged exact-SHA tree.

Indirect entry points:

- `/home/ds/.claude/skills/gascity-review-incoming-pr/SKILL.md` Phase 8 invokes
  `/review-pr` as its mandatory Codex leg and reruns it after adopt-and-fix.
- `/home/ds/.claude/skills/gascity-queue/review-batch.md` launches N
  `general-purpose` agents concurrently; each nests
  `/gascity-review-incoming-pr` and therefore `/review-pr`.
- `/home/ds/.claude/skills/gascity-queue/SKILL.md` exposes that batch entry.
- `/home/ds/.claude/skills/gascity-overnight-pipeline/SKILL.md` routes incoming
  PR review through `gascity-review-incoming-pr`.
- `/home/ds/gas-city/bin/maintenance-cycle` instructs its review worker to use
  `mol-pr-review` or the `gascity-review-incoming-pr` building blocks.
- Gas City polecat and gascity-packs-polecat skill allowlists include
  `review-pr`.

The outer queue batch gives each PR its own agent/context and `/review-pr` run
directory, so cross-PR collisions are unlikely; each nested `/review-pr` still
contains the Codex/Gemini shared-head collision.

### 3. Dashboard `/gascity-dashboard-review-pr` — unsafe shared checked-out tree

Source: `/home/ds/.claude/skills/gascity-dashboard-review-pr/SKILL.md`.

- Lines 19-23 define three Claude reviewer agents plus Codex.
- Lines 64 and 70-74 require all reviewers to run concurrently and explicitly
  state “no review worktrees.”
- Lines 117-189 fetch/check out the PR or local branch in the existing tree;
  PR mode may stash the caller's pre-existing changes first.
- Lines 218-238 dispatch `code-reviewer`, `security-reviewer`, and
  `typescript-reviewer` into that same checked-out tree.
- Lines 244-268 launch Codex concurrently from the same tree. Codex has no
  `--write` flag and is intended read-only, but the three Claude definitions are
  not capability-read-only.
- Lines 500-510 restore the original ref and pop the stash only after review.
- Lines 687-690 reaffirm that all reviewers use the checked-out working tree.

Ownership: all four reviewers and the orchestrator share one mutable checkout.

Risk: **critical collision surface** and the closest standing equivalent to
dr-05cx. `security-reviewer` explicitly has Edit/Write/Bash; code and TypeScript
reviewers have Bash. The prompt's “read-only review” intent is not enforcement.
One reviewer can alter another's observed tree or the user's stashed checkout.

Indirect entry points:

- `/home/ds/.claude/skills/gascity-dashboard-ship/SKILL.md` Stage 2 invokes the
  dashboard review orchestrator repeatedly and then fixes findings in place.
- `/home/ds/gas-city/agents/gascity-dashboard-pl/prompt.template.md` routes
  reviewable dashboard PRs into this skill.
- Any direct `/gascity-dashboard-review-pr` invocation.

### 4. Oversight-rig `mol-pr-ship` — unsafe panel in polecat worktree

Source:
`/home/ds/gascity-packs-worktrees/oversight-rig/pr-pipeline/formulas/mol-pr-ship.formula.toml`.

- Lines 157-168 define an iterative adversarial reviewer panel and in-place
  fixes.
- Lines 180-190 require parallel Agent calls for `code-reviewer`,
  `security-reviewer`, `codex:codex-rescue`, and a language reviewer.
- Lines 201-224 direct the lead to synthesize and then fix blockers/majors in
  place before another iteration.
- No per-reviewer worktree/copy or post-panel clean-tree assertion exists.

Ownership: panel reviewers share the coding polecat's current branch worktree.

Risk: **critical collision surface**. At least `security-reviewer` is explicitly
write-capable and all standard reviewers have Bash. `codex-rescue` must be
treated write-capable until its runtime sandbox is proven and pinned. Reviewer
mutations can mix with the orchestrator's intended between-iteration fixes.

Indirect entry points:

- `.../pr-pipeline/commands/pr/ship/run.sh` slings `mol-pr-ship` to a rig agent.
- `/home/ds/gas-city/agents/gascity-packs-pl/prompt.template.md` mandates
  `mol-pr-ship` for pre-PR gates.
- Gascity-packs polecat/polecat skill allowlists expose the reviewer roles used
  by the formula.

## Reviewed paths that do not create this parallel collision

These paths are relevant reviewer entry points but are single-owner as written:

- `formulas/mol-focus-review.formula.toml`: one worker invokes `/code-review`
  inside a per-bead pinned worktree. The reviewer/session is still Bash-capable,
  but there is no parallel reviewer fan-out in this formula.
- `formulas/mol-epic-review.formula.toml` and `bin/epic-review-sweeper`: one
  reviewer agent is slung per epic proxy; `review_in_flight` prevents duplicate
  review dispatch.
- Oversight-rig `mol-pr-review.formula.toml` and
  `commands/pr/review/run.sh`: one coding agent writes one scorecard; no nested
  panel is defined in that formula.
- `formulas/mol-dispatch.formula.toml`: routes each review/meta-review bead to
  one Codex agent after dependencies resolve; it does not fan multiple reviewers
  into one checkout.
- `gascity-check`, `gascity-ship` Stage 3, and dashboard ship Stage 3 launch a
  single checker or run mechanical checks, not a parallel reviewer panel.

These exclusions matter because launch comments in `approved-pr-automerge`
call `mol-pr-review` “multi-model”; the currently installed formula is not a
parallel multi-model implementation. The actual panel-bearing pack formula is
`mol-pr-ship`.

## Ownership rule required to close the collision class

For each review iteration, capture immutable inputs before launch:

```text
expected_commit = git rev-parse <review-ref>
expected_tree   = git rev-parse <review-ref>^{tree}
patch_sha256    = sha256(diff.patch)
```

Then choose exactly one enforceable mode per reviewer:

1. **Capability read-only:** reviewer sandbox denies filesystem writes and
   write-capable git operations. Advisory prompt text is insufficient; or
2. **Isolated checkout:** one detached worktree/copy per reviewer, all created
   from `expected_commit`, never shared between reviewers.

Before accepting each output, assert in that reviewer's checkout:

```text
HEAD == expected_commit
HEAD^{tree} == expected_tree
git status --porcelain is empty
```

If any assertion fails, preserve the checkout and diff as incident evidence,
discard that review result, and fail closed. Do not `--force`-remove it. Only
the orchestrator may apply fixes, after synthesis, in the branch worktree; a new
iteration receives a new expected commit/tree/patch hash.

For uncommitted `/review`, create a frozen review snapshot (temporary commit in
an isolated clone/worktree or a copied tree plus patch hash) rather than sharing
the live working tree. The user's live uncommitted changes must remain owned by
the orchestrator alone.

## Smallest implementation ownership

No implementation was authorized in this phase. The minimum future changes are:

1. **Global skill owners:** update `/review` and `/review-pr` under
   `/home/ds/.claude/skills/`. These are global/non-city changes and require the
   deferred human authorization.
2. **Dashboard skill owner:** update `gascity-dashboard-review-pr`; dashboard
   ship inherits the fix automatically.
3. **Pack owner:** update canonical `pr-pipeline/mol-pr-ship` in the
   gascity-packs source/pack, not only the live oversight worktree copy.
4. **City owner:** after upstream/global owners fix their sources, update city
   prompts only where needed to require the exact-SHA evidence contract. City
   wrappers mostly inherit behavior and should not duplicate isolation logic.
5. **Fixture owner:** add one hermetic fixture per direct launcher, using fake
   reviewers where reviewer A writes a probe and reviewer B attempts to observe,
   stash, checkout, or delete it. Pass condition: B cannot access A's checkout,
   A's mutation invalidates only A's output, and the source/branch checkout is
   byte-identical afterward.

## Verification performed

Read-only static inspection covered:

- all installed global skills matching parallel reviewer/Agent/Codex launch
  terms;
- all city-root agents, formulas, prompts, orders, and scripts matching those
  terms;
- canonical live oversight-rig PR pipeline formulas and command entry points;
- all local `*reviewer.md` tool declarations.

No reviewer/subagent was launched. No skill, setting, formula, prompt, worktree,
repository, runtime, or provider configuration was changed. No restart, signal,
external filing, or publication occurred.
