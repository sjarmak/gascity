# Independent investigation — fresh eyes

**Filed:** 2026-07-15 by mayor, at Stephanie's direction.
**Posture:** deliberately NO hypotheses included.

The mayor has misdiagnosed this **four times today**. You are being asked to investigate WITHOUT the
mayor's reasoning so you do not inherit its errors. Do not ask for the prior hypotheses — they are
withheld on purpose. Reason from the artifacts below only.

Everything here is **verified by execution** on 2026-07-15 unless marked `[REPORTED]` (i.e. it came
from another agent's report and was not independently measured).

---

## The artifacts

**A.** `/home/ds/gascity-worktrees/polecat/` holds **160** subdirectories.
- **154** return `fatal: not a git repository` at their own path.
- **6** resolve as git worktrees.
- Names are bead ids (`gc-XXXXX`) or bead-id + step-title slugs (e.g. `gc-5bzne-set-up-a-per-bead-worktree`).
- The slot **root** `/home/ds/gascity-worktrees/polecat` is **not** a git repository.

**B.** `/home/ds/gascity-worktrees/polecat-1 .. polecat-6` **are** registered worktrees of the rig
`/home/ds/gascity`. `git -C /home/ds/gascity worktree list` shows **11** per-bead worktrees at
`polecat-N/worktrees/gc-XXXXX`, each on its own `work/*` branch.

**C.** Nested **inside** the 154 non-repo dirs are **12 real git worktrees**, one or two levels down
(e.g. `polecat/gc-c7ikc-set-up-a-per-bead-worktree/worktrees/gc-jqja7/`, `polecat/gc-id8bu-.../repo/`).
**8 of the 12 carry commits not reachable from local `main`:**

| worktree | branch | not in main | dirty |
|---|---|---|---|
| `gc-dq8ns` | `bd-gc-dq8ns` | 2 | |
| `gc-qf6zf/repo` | `pr-3912` | 2 | |
| `gc-too7n` | `refactor/gc-beads-canonical-files-to-go` | 1 | |
| `gc-jqja7` | `work/gc-jqja7` | 1 | |
| `gc-lc3g4` | `work/gc-lc3g4` | 1 | **yes** |
| `gc-fzvtu` | `work/gc-fzvtu` | 1 | **yes** |
| `gc-d8qtj` | `work/gc-d8qtj-r2` | 1 | |
| `gc-fwaj6` | `bd-gc-fwaj6` | 1 | |

`git -C /home/ds/gascity worktree list --porcelain | grep '^worktree .*/polecat/'` → **16** entries.

**D.** **Three live** `claude` processes have cwd inside the non-repo dirs (via `/proc/<pid>/cwd`):

```
pid 2936884 -> /home/ds/gascity-worktrees/polecat/gc-jlt8c
pid 3003867 -> /home/ds/gascity-worktrees/polecat/gc-pfp3g
pid 3003887 -> /home/ds/gascity-worktrees/polecat/gc-n2hm8
```

Their gc sessions are `gc-482020` / `gc-483230` / `gc-484229`, whose **targets** are
`/home/ds/gascity/polecat-6`, `polecat-2`, `polecat-3` respectively.

**E.** `agents/polecat/agent.toml` currently reads:

```toml
work_dir = "/home/ds/gascity-worktrees/{{.AgentBase}}"
```

`AgentBase` is populated at `gascity internal/workdir/workdir.go:118` via
`config.ParseQualifiedName(qualifiedName)`; for target `/home/ds/gascity/polecat-3` this yields
`polecat-3`. **This template cannot produce the path in (D).**

File mtime is `2026-07-15 01:35:29`. That mtime belongs to a **33-file bulk sweep** that changed
`provider = "claude-3"` → `"claude-2"` fleet-wide (verified by diffing
`agents/city-infra-polecat/agent.toml.bak-2026-07-14-refloor` against current). **It did not touch
`work_dir`.**

**F.** `formulas/mol-focus-review.formula.toml`, step `workspace-setup`, runs in order:

```bash
git fetch --prune origin
WORKTREE=$(bd show {{issue}} --json | jq -r '.[0].metadata.work_dir // empty')
if [ -z "$WORKTREE" ]; then WORKTREE_PATH=$(pwd)/worktrees/{{issue}}; ... git worktree add ...; fi
cd "$WORKTREE"
```

**UPDATE 2026-07-15 (supersedes the earlier note in this file):** the mayor edited this file and has
since **REVERTED** it at Stephanie's direction. The **live file is now the original, unmodified
code** — read it directly. `formulas/mol-focus-review.formula.toml.bak-2026-07-15-danglingworkdir`
is byte-identical to the live file; you do not need it.

The mayor's reverted edits are preserved at
`formulas/mol-focus-review.formula.toml.PROPOSED-danglingfix-reverted-2026-07-15` and
`bin/gc-sling.PROPOSED-danglingfix-reverted-2026-07-15`. **Do not read them unless you have already
formed your own conclusion** — they encode a hypothesis that is 0-for-7 and reading them first is
exactly the contamination this brief exists to avoid. `bin/gc-sling` is likewise reverted to
original.

**G.** Newest directory under `polecat/` is dated **2026-07-14**. None dated 2026-07-15.

**H.** Bead metadata: `gc-mf2ya` and `gc-ntez3` (the beads the wedged sessions are on) have **no
`work_dir` set**. `gc-b6zqt` had `work_dir=/home/ds/gascity-worktrees/polecat/gc-4nmcr`, a path that
does not exist (since cleared). A sweep found **19 open beads fleet-wide** whose `work_dir` does not
resolve to a git worktree: gascity 13, mem 4, EnterpriseBench 1, gascity-dashboard 1 — including
`mem-0rrf` stamped `work_dir=/home/ds/gas-city` (the city root, not a git repo).

**I.** `[REPORTED, not independently verified]` gascity-maintenance-pl states 5 `mol-focus-review`
molecules are wedged with timestamps frozen at 06:50–08:01Z, and that 6 live polecats produced 0
throughput against ~100 ready beads.

**J.** `cass` (session-history search) **cannot help**: ingestion is **31 days stale** (newest indexed
message `2026-06-14T07:50`). DB at `/home/ds/.local/share/coding-agent-search/agent_search.db`,
readable read-only via sqlite3 URI `mode=ro`; timestamps are **epoch milliseconds**. Do not rely on it
for July events.

---

## The questions

**Q1 (primary — the apparent contradiction).** How were the 12 nested git worktrees in **(C)** created
inside directories that are **not git repositories**? `git worktree add $(pwd)/worktrees/X` requires
git context at pwd. Either those dirs had git context when the worktrees were made, or another code
path created them. Establish **which**, with evidence.

**Q2.** What determines the work_dir in **(D)**? The current template **(E)** provably cannot produce
it. Find the code path or persisted state that does. gc stores session work_dir
(`internal/session`: `WorkDirCanonical`, `bindPoolSessionTriggerBead`) — persisted state is a
candidate, but **verify rather than assume**.

**Q3.** Why did directory creation under `polecat/` stop on **2026-07-14** (**G**)? Nothing in **(E)**
explains it.

**Q4.** Which of the 8 branches in **(C)** carry work worth preserving, and what is the **safe
sequence** to reclaim the 154 dirs given the 16 stale worktree registrations? A plain `rm -rf` would
destroy ~10 unmerged commits and 2 dirty trees. **Do not execute a reap** — produce the sequence.

---

## Rules

- **READ-ONLY** on `/home/ds/gascity` and `/home/ds/gascity-worktrees`. Do **not** delete, move, or
  reap any directory. Do **not** kill the 3 live sessions in (D). Do **not** edit formulas or
  agent.toml.
- Cite `file:line` and paste real command output.
- **"I reproduced it" must mean the code path that actually runs**, not a scenario you constructed.
  That specific error is how the mayor got here.
- If the evidence does not support a conclusion, **say so and stop**. A clean "unknown, and here is
  what would settle it" is worth more than a fifth wrong root cause.
- Report back by mail to `mayor`.
