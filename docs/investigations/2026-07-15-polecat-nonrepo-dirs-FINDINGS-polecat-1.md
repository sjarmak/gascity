# Findings — polecat non-repo dirs (independent investigation)

**By:** polecat-1 (session `gc-493385`), bead `gc-g9ka3`, molecule `gc-q4up9`
**Date:** 2026-07-15
**Posture:** read-only. Nothing deleted, moved, pruned, or reaped. No formula/agent.toml edits.
**Note:** routed here because the Codex runtime is down. Same model family that got this wrong six
times, so every claim below is backed by pasted command output or `file:line` on the ref that
actually builds the binary. Where the brief and the evidence disagree, the evidence is shown.

---

## Summary

All four questions are answered. The central contradiction in **Q1** is not a contradiction: it
rests on a premise (`git worktree add` needs git context *at the target's parent*) that is false.

The one-paragraph version: pool per-bead work_dirs are derived from the **pool template** agent
identity (`polecat`), not the slot identity (`polecat-N`). That yields
`/home/ds/gascity-worktrees/polecat/<bead-slug>` under a root that **was never provisioned as a git
worktree**. gc mkdirs it and stages `.gc/` + `.claude/` into it, so every polecat lands in a
non-repo directory. Formula steps that assume git context then fail, and the agents — being LLMs —
*improvise* their way around the failure with `git -C /home/ds/gascity worktree add "$ABS_PATH"`.
That single improvisation produces every artifact in the brief.

**A live instance of the bug is running this investigation.** My own cwd is
`/home/ds/gascity-worktrees/polecat/gc-4n24m`, created today at 14:09, not a git repository.

---

## Q1 — How were 12 real worktrees created inside non-repo dirs? **ANSWERED**

**They were created by the agents, from outside, with an absolute path.** The parent dirs were never
git repositories — not before, not after.

`git worktree add <path>` does **not** require the target's parent to be a repo. It requires git
context in the *invoking process*, and it `mkdir -p`s the target's parent chain. Both halves of the
brief's "either/or" are false; there is a third option, and it is what happened.

### Evidence 1 — birth times separate the two creators

```
/home/ds/gascity-worktrees/polecat/gc-c7ikc-set-up-a-per-bead-worktree
  Birth: 2026-07-14 01:14:41.928204569 -0400     group docker   (gc: .claude + .gc)
    └── worktrees/
  Birth: 2026-07-14 01:16:01.492870518 -0400     group ds       (git)
        └── worktrees/gc-jqja7/
  Birth: 2026-07-14 01:16:01.492870518 -0400     group ds       (git)
```

gc created the bare parent at 01:14:41 (group `docker`). **80 seconds later** git created
`worktrees/` and the leaf **in the same instant** (group `ds`) — the signature of `mkdir -p` on a
target path. The parent contains `.claude`, `.gc`, `worktrees` and **no `.git`, ever**:

```
$ ls -la /home/ds/gascity-worktrees/polecat/gc-c7ikc-set-up-a-per-bead-worktree
drwxr-xr-x .claude      2026-07-14 01:14:41
drwxr-xr-x .gc          2026-07-14 02:00:52
drwxrwxr-x worktrees    2026-07-14 01:16:01
```

The 80-second gap is itself diagnostic: a script does this in <1s. That gap is an LLM taking turns.

### Evidence 2 — the transcript, verbatim

`~/.claude/projects/-home-ds-gascity-worktrees-polecat-gc-c7ikc-set-up-a-per-bead-worktree/7e574075-*.jsonl`

The agent first ran the formula body as written and got:

```
fatal: not a git repository (or any of the parent directories): .git
fatal: not a git repository (or any of the parent directories): .git
```

It then worked around it — note `$(pwd)` is captured from the **non-repo** dir, while git context is
supplied from `$REPO`:

```bash
cd /home/ds/gascity-worktrees/polecat/gc-c7ikc-set-up-a-per-bead-worktree
WORKTREE_PATH="$(pwd)/worktrees/gc-jqja7"
REPO=/home/ds/gascity
if git -C "$REPO" show-ref --verify --quiet "refs/heads/$BRANCH"; then
  git -C "$REPO" worktree add "$WORKTREE_PATH" "$BRANCH" 2>&1
else
  git -C "$REPO" worktree add "$WORKTREE_PATH" -b "$BRANCH" origin/main 2>&1
fi
...
gc bd update gc-jqja7 --set-metadata work_dir="$WORKTREE_PATH"
```

That last line is the origin of artifact **(H)**: the agent stamps the dangling non-repo path into
bead metadata itself.

### Evidence 3 — it generalises

The same `git -C /home/ds/gascity worktree add` improvisation appears in **12+ independent work_dir
transcripts** (`gc-23ma5`, `gc-7wlrw`, `gc-drvay`, `gc-eeuu1`, `gc-gryyv`, `gc-h9zkl`, `gc-i1q43`,
`gc-jj2eu`, `gc-jtlo1`, `gc-kp2dr`, `gc-l6z9r`, `gc-n2hm8`, …). This is the fleet's standard
coping behaviour, not one agent's quirk.

**Side effect worth noting:** the improvised path cuts from `origin/main`, while the live formula
(`mol-focus-review.formula.toml:133-137`) explicitly says cut from **local** `main` and explains why.
The workaround silently discards that reasoning. Local `main` is currently ahead of `origin/main`.

---

## Q2 — What determines the work_dir in (D)? **ANSWERED — fact (E) is refuted**

**The `agent.toml` template does produce it.** Fact (E) says "This template cannot produce the path
in (D)". That is the false step, and I believe it is what sent the prior six hypotheses hunting for
a phantom second code path. There is no phantom.

`AgentBase` is `config.ParseQualifiedName(qualifiedName)` — it is whatever identity is *passed in*.
For a **pooled** agent the pool loop passes the **template's** qualified name (`gascity/polecat`),
giving `AgentBase = "polecat"` — **not** `polecat-3`.

```
work_dir = "/home/ds/gascity-worktrees/{{.AgentBase}}"     # agents/polecat/agent.toml:2
                                     ↓ AgentBase = "polecat"
           /home/ds/gascity-worktrees/polecat               # ← NOT a git repository
```

Then `cmd/gc/build_desired_state.go:3005` `poolTriggerWorkDir()` appends the per-bead slug:

```go
base, err := resolveConfiguredWorkDir(bp.cityPath, bp.cityName, qualifiedName, cfgAgent, bp.rigs)
...
if slug := triggerBeadPathSlug(request.WorkBeadID, request.WorkBeadTitle); slug != "" {
    return filepath.Join(base, slug)
}
```

`triggerBeadPathSlug` (`:3036`) returns `id + "-" + titleSlug`, or bare `id` when the title is
empty — which is exactly why **both** naming variants exist in the wild (`gc-jlt8c` and
`gc-c7ikc-set-up-a-per-bead-worktree`). No second code path is needed to explain the names.

**Proof from the running system** (independent of any code reading):

```
GC_DIR      = /home/ds/gascity-worktrees/polecat/gc-4n24m   ⇒ base = .../polecat ⇒ AgentBase = "polecat"
GC_TEMPLATE = /home/ds/gascity/polecat        ← template identity, no -N suffix
GC_AGENT    = /home/ds/gascity/polecat-1      ← slot identity, has -N

$ git -C /home/ds/gascity-worktrees/polecat rev-parse --show-toplevel
fatal: not a git repository (or any of the parent directories): .git
```

`GIT_DIR`/`GIT_WORK_TREE` are **not** set in the polecat environment (checked my own env), so there
is no ambient git context rescuing the formula body.

**So the root cause is a single identity mismatch:**

| path | AgentBase | resolves to | provisioned as worktree? |
|---|---|---|---|
| slot session | `polecat-1` | `/home/ds/gascity-worktrees/polecat-1` | **yes** (verified `--is-inside-work-tree` = true) |
| pool trigger | `polecat` | `/home/ds/gascity-worktrees/polecat` | **no — nobody ever created it** |

Both derive from the same template. Only the slot path was ever provisioned. The pool path gets
`os.MkdirAll` + staging, so it looks plausible and behaves like a repo-less hole.

---

## Q3 — Why did creation stop on 2026-07-14? **IT DIDN'T**

Fact **(G)** was true when the brief was filed (17:44Z / 13:44 EDT) and **my own dispatch falsified
it 25 minutes later.** There are now 162 dirs, not 160. The two new ones are mine:

```
gc-4n24m   Birth 2026-07-15 14:09   (my session's step bead)
gc-g9ka3   Birth 2026-07-15 14:14   (this investigation bead)
```

Birth-date histogram of all 162:

```
 5  2026-07-02      12  2026-07-06      4  2026-07-11
 2  2026-07-03       4  2026-07-08      7  2026-07-13
 8  2026-07-04      40  2026-07-09     24  2026-07-14
10  2026-07-05       9  2026-07-10      2  2026-07-15  ← both created by this investigation
```

Directory creation is a pure function of **new pool trigger-bead binding** (`poolTriggerWorkDir` runs
on bind). No new trigger beads bound ⇒ no new dirs. That is consistent with **(I)**: 5 molecules
wedged, 6 polecats at 0 throughput. The pause is a *dispatch-volume* symptom of the wedge, not a
cause, and not a code or config change. Nothing needs to explain it beyond "the queue stalled" —
and the counter-proof is that the moment a molecule was slung, creation resumed immediately.

**Corollary:** the `agent.toml` mtime of 2026-07-15 01:35 is a red herring, as the brief already
established. It is not connected to (G) in either direction.

---

## Q4 — What is worth preserving, and the safe reap sequence

### The brief's risk premise is inverted

> "A plain `rm -rf` would destroy ~10 unmerged commits and 2 dirty trees."

**For the 16 linked worktrees this is false.** A linked worktree's `.git` is a pointer file; its
refs and objects live in the rig:

```
$ cat .../worktrees/gc-jqja7/.git
gitdir: /home/ds/gascity/.git/worktrees/gc-jqja7
$ cat /home/ds/gascity/.git/worktrees/gc-jqja7/commondir
../..                       → /home/ds/gascity/.git

work/gc-jqja7        → refs/heads/work/gc-jqja7 PRESENT | object in rig store
work/gc-lc3g4        → refs/heads/work/gc-lc3g4 PRESENT | object in rig store
bd-gc-dq8ns          → refs/heads/bd-gc-dq8ns PRESENT   | object in rig store
fix/storehealth-3374 → refs/heads/fix/storehealth-3374 PRESENT | object in rig store
```

`rm -rf` of a linked worktree **directory** destroys **zero commits**. The branches survive in
`/home/ds/gascity/.git/refs/heads/`; the dirs are recreatable with `git worktree add`.

### The "2 dirty trees" are a false alarm

Both are dirty *only* from an untracked, gc-staged skills dir — no human or agent work:

```
gc-jlt8c/worktrees/gc-lc3g4                        ?? .claude/skills/review-pr/
gc-o34vz-.../worktrees/gc-fzvtu                    ?? .claude/skills/review-pr/
```

(The same `.claude/skills/review-pr/` is staged into my own work_dir.) **Nothing uncommitted is
worth preserving anywhere under `polecat/`.**

### The real hazard the brief missed: 4 independent clones

These have `.git` **directories** (own object stores), are **not** in `git worktree list`, and are
therefore **invisible to `git worktree prune`**. A reap driven by worktree registrations alone
destroys them silently and irrecoverably:

| clone | branch | state | verdict |
|---|---|---|---|
| `gc-qf6zf-…/repo` | `pr-3912` | local HEAD `efa81e8` = local merge commit; PR #3912 **OPEN** at `d94bea7` | contributor work safe on GitHub; local merge is a review artifact — **confirm, then disposable** |
| `gc-itx8q-simplify-the-diff/review-pr-4121` | `pr-4121-head` | HEAD `14bbbdba` **== PR #4121 head, MERGED** | fully recoverable — safe |
| `gc-id8bu-…/repo` | `main` | 0 ahead | safe |
| `gc-zk2s5-codex-review-for-merge-queue-pr-4096` | — | `.git` dir present but `fatal: not a git repository` — broken/interrupted clone | safe |

`gc-wxjfp-codex-review-for-merge-queue-pr-3310` also carries git artifacts below depth 3; treat it
with the same check before removal.

### Corrected branch table — the brief's table omits 3 branches

Every row of the brief's 8-row table **confirms**. But it counted only *nested* worktrees and missed
three **direct** ones that also carry unmerged commits. A reap planned from that table would
destroy three more branches than expected:

| worktree (under `polecat/`) | branch | ahead of `main` | dirty | recoverable? |
|---|---|---|---|---|
| `gc-0vz2r` | `fix/storehealth-3374` | 1 | | **local-only** ← *missed by brief* |
| `gc-dnfu6-mol-adopt-pr-review-incoming-pr-3564` | `fix/ga-zzj6xu-pack-pins` | 1 | | **local-only** ← *missed by brief* |
| `gc-i1q43-work` | `fix/ralph-check-path-workdir-fallback-3008` | 1 | | pushed — safe ← *missed by brief* |
| `gc-eeuu1-…/worktrees/gc-dq8ns` | `bd-gc-dq8ns` | 2 | | local-only |
| `gc-caa4k-…/worktrees/gc-too7n` | `refactor/gc-beads-canonical-files-to-go` | 1 | | local-only |
| `gc-c7ikc-…/worktrees/gc-jqja7` | `work/gc-jqja7` | 1 | | local-only |
| `gc-jlt8c/worktrees/gc-lc3g4` | `work/gc-lc3g4` | 1 | staging junk | local-only |
| `gc-o34vz-…/worktrees/gc-fzvtu` | `work/gc-fzvtu` | 1 | staging junk | local-only |
| `gc-y10ec/worktrees/gc-d8qtj` | `work/gc-d8qtj-r2` | 1 | | local-only |
| `gc-zvr8m/worktrees/gc-fwaj6` | `bd-gc-fwaj6` | 1 | | local-only |

9 of 10 are local-only (polecats never push, as expected). All 10 are **safe from a dir reap**
regardless, because the refs live in the rig. They are at risk only from `git branch -D`.

### Classification of all 162 dirs

```
dirs containing git artifacts (handle):           21
dirs with NO git artifact (only .claude + .gc):  141   ← pure staging junk
```

### Proposed safe sequence — NOT executed

1. **Tag the local-only work first** (cheap, makes every later step unconditional). In
   `/home/ds/gascity`: for each of the 9 local-only branches,
   `git tag salvage/<branch> <branch>`. Refs then survive any dir removal *and* any later
   `git branch -D`.
2. **Handle the 4 clones before anything else** — they are the only true data-loss risk, and no git
   tooling protects them. `gc-itx8q/review-pr-4121` (merged), `gc-id8bu/repo` (0 ahead) and
   `gc-zk2s5` (broken) are safe to delete. For `gc-qf6zf-…/repo`, confirm PR #3912 carries the work
   (local HEAD is a local merge commit, so it will never match the PR head) and then delete.
3. **Exclude live sessions.** Reap by *birth date* with a cutoff, or check `/proc/*/cwd` at reap
   time. The 3 pids in (D) are **already gone** (`/proc/<pid>/environ` → No such file or directory),
   but new ones appear continuously — mine did, mid-investigation. This is the step that makes the
   reap non-idempotent and needs a live guard, not a static list.
4. **Remove the 141 junk dirs** (`.claude` + `.gc` only, no git artifact at any depth, not live).
   No git involvement, nothing to prune.
5. **Remove the 16 linked worktree dirs**, then a single `git -C /home/ds/gascity worktree prune`
   to clear the stale admin entries. Order does not matter; prune is what reconciles.
6. **Leave the branches alone.** Reclaiming disk does not require touching a single ref. Decide
   merge-vs-drop for the 9 salvage tags separately, as a normal review, with no time pressure.

Steps 4–5 reclaim the 154 dirs. Step 6 is the point: **the dirs and the work are separable**, and
conflating them is what made this look dangerous.

---

## Corrections to the brief

Recorded because the next reader inherits these:

- **(E) is wrong**, and it is load-bearing. "This template cannot produce the path in (D)" — it can,
  and does. `AgentBase` = `polecat` for the pool template identity, not `polecat-N`. The claim that
  `ParseQualifiedName` "for target `/home/ds/gascity/polecat-3` yields `polecat-3`" is true in
  isolation and irrelevant: the pool loop never passes that identity.
- **(G) is stale** — falsified 25 minutes after filing, by the very dispatch that delivered the brief.
- **(D) is stale** — all 3 pids are dead. The "do not kill" constraint is moot (I did not touch them).
- **(C) undercounts** — 3 direct worktrees with unmerged commits are missing from the table, and the
  "12 nested worktrees" is really 10 linked worktrees + 4 clones (2 of which the brief didn't see).
- **Q4's premise is inverted** — `rm -rf` on a linked worktree destroys nothing; the clones are the
  only real risk, and the brief doesn't mention them.
- **The "2 dirty trees" are staging junk**, not work.

### A trap that may explain some of the six misses

`/home/ds/gascity`'s **working tree is parked on branch `_pr1945_check` at a 2026-05-11 commit**,
while local `main` is at `9a8421c11` (2026-07-15 18:18Z):

```
$ git -C /home/ds/gascity branch --show-current
_pr1945_check
$ git -C /home/ds/gascity log -1 --format='%h %ci' main
9a8421c11 2026-07-15 18:18:43 +0000
```

Any `grep`/`sed`/Read against the rig's checkout reads **two-month-old source**. I hit this myself:
my first pass concluded `WorkDirCanonical` and `bindPoolSessionTriggerBead` "do not exist in the
codebase" — they don't, *on that stale checkout*. They exist on `main`
(`cmd/gc/build_desired_state.go:2986`, `internal/session/info_codec.go:139`), and the brief's Q2
hint was correct. I retracted it. Every code claim above is from `git show main:<file>`.

If the mayor was reading the working tree, its model of the code was two months stale.

---

## The gap is already half-guarded — and the codebase already named this bug

**gc never runs `git worktree add` anywhere in code.** On `main`, the only matches are comments:

```
$ git -C /home/ds/gascity grep -nE '"worktree", "add"|worktree add|WorktreeAdd' main -- 'cmd/gc/*.go' 'internal/*/*.go'
internal/doctor/checks_semantic.go:385:  // via `git worktree add path origin/<branch>`, so removing the local
internal/workdir/workdir.go:298:         // on an ancestor lets "git -C <rig-root> worktree add <child>" register a
internal/workdir/workdir.go:301:         // worktree add" surfaces the stale ancestor to the operator instead of
```

So gc provisions **nothing**: it `MkdirAll`s the work_dir and stages into it. `polecat-1..6` are
hand-made. The pool root never was, and nothing in gc would ever have made it.

Now the important part. `internal/workdir/workdir.go:303`
`ValidateAncestorWorktreesNotStale(path)` is a **spawn-time guard that already runs on the pool
path** — `poolTriggerWorkDir` → `resolveConfiguredWorkDir` (`cmd/gc/cmd_start.go:1306`) → the guard
at `cmd/gc/cmd_start.go:1322`. Its own doc comment describes our exact artifact:

> This is the spawn-time guard for gascity#1556: a stale worktree pointer on an ancestor lets
> `"git -C <rig-root> worktree add <child>"` register a **structurally orphaned child that can't be
> reached from the ancestor itself**. Failing closed on stale-pointer cases before invoking
> `"git worktree add"` surfaces the stale ancestor to the operator instead of producing dangling
> content.

That is precisely what the polecats produced, by precisely the command the comment names. The
project diagnosed this failure once already (**gascity#1556**) and shipped a guard for it.

**The guard covers the stale-pointer case and not ours.** It walks ancestors looking for a `.git`
that *points somewhere dead*. Our ancestors have **no `.git` at all**, so the walk finds nothing,
returns `nil`, and the spawn proceeds into a hole. It fails open on exactly the input that produced
154 dirs. (The code comments the fail-open choice deliberately, for unreadable/non-pointer `.git`
files — reasonable there, wrong for "no git context anywhere up the chain".)

That makes the fix location unambiguous: **the guard is already called from the right place on the
right path — it just needs to also reject a work_dir with no git context.**

## What I did not establish

- **Whether the 6 direct worktrees** (`gc-0vz2r`, `gc-akvn6`, `gc-doi5s`, `gc-g421k`, `gc-i1q43-work`,
  `gc-dnfu6-…`) were created by the same improvisation or another path. Their transcripts are on
  disk and would settle it; I stopped at the point the question was answered.
- **The wedge itself.** I explained why *dirs* stopped appearing; I did not diagnose why the 5
  molecules are wedged. (D) being dead pids suggests those sessions ended without draining, but I
  did not verify that and it is a separate investigation.

---

## Recommendation

The fix is **not** in the formula and **not** in `agent.toml`'s template string — both are internally
consistent, and the six prior hypotheses were, I suspect, hunting there because (E) told them the
template was innocent. The defect is that the pool trigger path derives a work_dir under a root
nothing ever provisions, and the one guard positioned to catch it fails open.

**1. Close the guard (smallest correct change, right where it already lives).**
`ValidateAncestorWorktreesNotStale` (`internal/workdir/workdir.go:303`) already runs on the pool path
via `cmd/gc/cmd_start.go:1322`. Extend it — or add a sibling called from the same site — to fail
closed when the resolved work_dir has **no git context on any ancestor**, not just a *stale* pointer.
Same failure mode, same issue (**gascity#1556**), same call site, one uncovered input. A polecat would
then refuse to spawn with a clear reason instead of landing in a hole.

**2. Then decide what the pool work_dir should actually be.** Two shapes:
   - **(a)** Derive it from the slot identity (`polecat-N`, already a real worktree), so per-bead dirs
     nest under a repo. Smallest behavioural delta; the slot worktrees already exist and work.
   - **(b)** Provision the pool root as a real worktree at bind time, making the path's implied
     promise true. But note gc provisions **no** worktrees today, so this adds a new
     responsibility — and something must then own teardown, or this recurs as a leak instead of a
     hole.

I'd take **(a)**. It preserves the per-bead isolation the formula wants and the
`mem-cvn3` / `EnterpriseBench-1fb2` lessons its comments encode, without giving gc a provisioning
role it has never had.

**3. Do not make the workaround official.** The `git -C $REPO worktree add "$ABS_PATH"` pattern is
the symptom, and it is the exact command `gascity#1556`'s guard was written to prevent. Blessing it
in the formula would re-file that bug as a feature.

### The meta-finding

The agents were **too good at coping**. Each one hit `fatal: not a git repository`, reasoned its way
to a working absolute-path invocation, completed its bead, and stamped the dangling path into
metadata on the way out. Every individual session looks like a success. That is what converted a
one-line provisioning gap into 154 dirs, 16 stale registrations, and 19 dangling `work_dir` stamps
accumulating silently for two weeks — and why it surfaced as a mysterious filesystem anomaly rather
than as a spawn failure on day one.

A guard that fails closed at spawn would have turned all 154 of these into one legible error.
