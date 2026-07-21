# `gc` binary and oversight-rig pack

Failure modes covered: installed `gc` binary built from a random PR branch in the contributor tree, `city.toml` pointing at the contributor tree (so the city breaks whenever the contributor swings branches), oversight-rig pack missing because the contributor checked out a branch without it.

## Two-worktree layout

| Path                    | Branch        | Purpose                                     |
| ----------------------- | ------------- | ------------------------------------------- |
| `/home/ds/gascity`      | any PR branch | Contributor / PR work. Never built from.    |
| `/home/ds/gascity-main` | `main`        | Only this tree builds the installed binary. |

Sync command: `gcsync` (`/home/ds/.local/bin/gcsync`). Runs `git pull --ff-only origin main` + `make install` in `/home/ds/gascity-main` only. Skips rebuild if already at HEAD. Refuses to run if the main tree has drifted off main. Does not touch `/home/ds/gascity`.

Mayor duty: at session start, run `gcsync` once. Fast (no-op if current). The running supervisor process keeps its in-memory binary until restarted — call `systemctl --user restart gascity-supervisor` only if the sync actually rebuilt AND something about the new commit matters for the supervisor (rare).

Recreate the main worktree if missing:

```bash
cd /home/ds/gascity && git worktree add /home/ds/gascity-main main
```

## Oversight-rig pack — stable worktree, NOT the contributor tree

The oversight-rig pack is not yet on upstream `main` and does not live in `gascity-packs`. It only exists on PR branches inside the gascity repo (`gascity-pr`, `feat/oversight-rig-pack`, …). To keep `city.toml` independent of whatever branch is currently parked on `/home/ds/gascity`, the pack is checked out at a stable path:

| Path                                             | Branch       | Purpose                                              |
| ------------------------------------------------ | ------------ | ---------------------------------------------------- |
| `/home/ds/gascity-packs-worktrees/oversight-rig` | `gascity-pr` | The only path `city.toml` references for this pack.  |

All `city.toml` references use `/home/ds/gascity-packs-worktrees/oversight-rig/oversight-rig` (21 lines as of 2026-07-21: one in `[imports]`, twenty per-rig `[rigs.imports.oversight-rig]` blocks; count with `grep -c 'packs-worktrees/oversight-rig/oversight-rig' city.toml`). Do NOT swap them back to `/home/ds/gascity/examples/oversight-rig` — that breaks the city every time the contributor tree gets swung off a branch with the pack.

Recreate the worktree if missing:

```bash
cd /home/ds/gascity && git worktree add /home/ds/gascity-packs-worktrees/oversight-rig gascity-pr
```

Refresh the pack content when upstream `gascity-pr` advances:

```bash
cd /home/ds/gascity-packs-worktrees/oversight-rig && git pull --ff-only origin gascity-pr
```

Retire this worktree and point `city.toml` at the upstream location once the pack lands on `main` (or in `gascity-packs`).

## gascity-packs imports — stable `-main` worktree, NOT the contributor tree

Same failure mode as the binary: `city.toml` must import packs from a stable worktree on upstream `main`, never from the contributor checkout that holds PR branches.

| Path                          | Branch / ref          | Purpose                                                     |
| ----------------------------- | --------------------- | ----------------------------------------------------------- |
| `/home/ds/gascity-packs`      | any PR branch         | Contributor / PR work (e.g. parked on `bd-gpk-g5d`). Never imported from. |
| `/home/ds/gascity-packs-main` | detached `upstream/main` | The only path `city.toml` references for merged packs.   |

`city.toml` imports these packs from `/home/ds/gascity-packs-main/...`:
- `[rigs.imports.pr-pipeline]` → `/home/ds/gascity-packs-main/pr-pipeline` (2 blocks)
- `[rigs.imports.pr-review]` → `/home/ds/gascity-packs-main/pr-review`

`/home/ds/gascity-packs-main` is kept at `upstream/main` by the **`slack-adapter-rebuild`** order (it `git checkout --detach upstream/main` on drift, then rebuilds the adapter). This is currently a *side effect* of that order — there is no dedicated packs-sync order, so if `slack-adapter-rebuild` stops firing, `-main` will go stale. A dedicated fast-forward of `gascity-packs-main` would be more robust (follow-up).

**Why this convention exists (2026-05-30 incident):** `pr-pipeline` and `pr-review` were imported from `/home/ds/gascity-packs`, which had been parked on contributor branch `bd-gpk-g5d` since 2026-05-12. The city ran 18-day-stale formulas (e.g. the pre-#37 6-step `mol-adopt-pr`) even though the merges were live on `upstream/main`. `fork-fast-forward` keeps the *remote* fork ref current but never touches a working tree, so nothing refreshed the imported files. Fixed by repointing to `gascity-packs-main`.

**Still on the contributor tree (pending):** `[imports.slack-pack]` (`/home/ds/gascity-packs/slack-pack`, line 110) is NOT repointed. The contributor branch's slack-pack is substantially diverged from `upstream/main` (upstream has ~3k lines of subteam-mention / alias-sync / thread-stickiness features the branch lacks; the branch has one unmerged fix, `gpk-5sk` "exit non-zero on delivered=false"). Repointing it is a behavior change to live Slack infra and must be done deliberately — confirm `gpk-5sk` lands upstream, then repoint to `gascity-packs-main/slack-pack`.

Recreate the worktree if missing:

```bash
cd /home/ds/gascity-packs && git worktree add --detach /home/ds/gascity-packs-main upstream/main
```
