# AOA stale-worktree audit — 2026-07-25

## Decision

This was an autonomous, read-only audit of the 20 AOA worktrees selected by
`stale-worktree-reaper` before recurring pruning was enabled. It requires no
human content review.

- Audited: 20 worktrees, 56,116,164 KiB (53.5165 GiB).
- Safe to prune: 19, subject to the reaper's mandatory final identity, process,
  bead-reference, and clean-state revalidation.
- Held fail-closed: `/home/ds/projects/aoa-d6t.35`; four live processes have
  that worktree as their current directory.
- Reproducible ignored build output: `target/` accounts for 56,011,732 KiB
  (53.4170 GiB, 99.814% of the audited allocation).
- All 20 branch refs resolve to their worktree HEAD and are retained by the
  reaper. Every tip is reachable from local `main`.
- Local default snapshot during the audit:
  `e6da26d360d54ba5215a7ec33af7c2d9b59678f1`.

## Why the large artifacts are disposable

The classification is repository-backed, not inferred from directory names:

1. AOA `.gitignore` line 8 identifies `target/` as Rust build output.
2. `README.md` documents `cargo build --workspace` and
   `cargo test --workspace`, which recreate these artifacts.
3. The largest entries in every candidate are the standard generated trees
   `target/debug/deps`, `.fingerprint`, `incremental`, `build`, and `examples`.
4. `git check-ignore -v target target/debug target/debug/deps` resolves all
   three paths to that ignore rule.
5. No candidate has a tracked modification or a non-ignored untracked file.
   Content outside `target/` totals only 104,432 KiB (101.98 MiB) across all
   20 worktrees.
6. No candidate contains a unique untracked checkpoint, result dataset,
   nested repository, submodule, bind mount, or bead-owned working directory.

## Candidate ledger

| Worktree | Branch | Exact tip | GiB | Decision |
|---|---|---|---:|---|
| `/home/ds/projects/aoa-age9` | `work/aoa-age9` | `717431ceaa9e7903302486504a62df3a8a41726c` | 4.42 | prune |
| `/home/ds/projects/aoa-6xk` | `work/aoa-6xk` | `1eda906acabf9b7495a847883fb765c60401a438` | 3.75 | prune |
| `/home/ds/projects/aoa-w0o` | `work/aoa-w0o` | `05dbd8ee89ddcc0d4c51d88028128698ae2868ea` | 3.62 | prune |
| `/home/ds/projects/aoa-d6t.37` | `work/aoa-d6t.37` | `b090d2bf010731e443fdcfa43031e3d0332b8eff` | 3.60 | prune |
| `/home/ds/projects/aoa-5cnl` | `work/aoa-5cnl` | `e6da26d360d54ba5215a7ec33af7c2d9b59678f1` | 3.47 | prune |
| `/home/ds/projects/aoa-vrx.3` | `work/aoa-vrx.3` | `7975e61b15dcbf94fb790e7594d2fd20c736420d` | 2.89 | prune |
| `/home/ds/projects/aoa/worktrees/aoa-d6t.38` | `work/aoa-d6t.38` | `7bce7206f2c9d99a10b2f68470065422822a9008` | 2.83 | prune |
| `/home/ds/projects/aoa-d6t.35` | `work/aoa-d6t.35` | `8fccf5b7fbbf28c5fb51e52982e48a691307aad4` | 2.67 | **hold: live** |
| `/home/ds/projects/aoa-gcwl` | `work/aoa-gcwl` | `2a157809549fe6b68634fdc3c72afb3999964b59` | 2.63 | prune |
| `/home/ds/projects/aoa-w9cb` | `work/aoa-w9cb` | `349846dd80578f8547295cfe3aea7a9816715a06` | 2.57 | prune |
| `/home/ds/projects/aoa-empz` | `work/aoa-empz` | `9587cd339ce9fd27ef13b3f7c8d16a9bf4344bb6` | 2.56 | prune |
| `/home/ds/projects/aoa-kk6m` | `work/aoa-kk6m` | `b3aed35c234130b5fb9106ae70d14340f34e538a` | 2.56 | prune |
| `/home/ds/projects/aoa-g2g5` | `work/aoa-g2g5` | `fcf2c686e43de5bf7ce722f3a383da2a1c0da0e4` | 2.56 | prune |
| `/home/ds/projects/aoa-d6t.41` | `work/aoa-d6t.41` | `edf08beec94385be013d40c12525bec5d89857dd` | 2.42 | prune |
| `/home/ds/projects/aoa-n9oj` | `work/aoa-n9oj` | `e83ac2918ac7eec12e8c503d4f926e69d5588691` | 2.18 | prune |
| `/home/ds/projects/aoa/worktrees/aoa-ctyo` | `work/aoa-ctyo` | `62dc21987466aa93b81b4cfa82bab37f3364bcab` | 2.14 | prune |
| `/home/ds/projects/aoa-4uc5` | `work/aoa-4uc5` | `d3493b39c7f7a9404788c8d0fa76a79eb0101130` | 2.14 | prune |
| `/home/ds/projects/aoa-dyx` | `work/aoa-dyx` | `81150507497ba011ed32ce934a9c459e2d92a09a` | 1.95 | prune |
| `/home/ds/projects/aoa-d6t.36` | `work/aoa-d6t.36` | `c3320a26ac0eb34e1c17df8098c25cef0031d8a0` | 1.32 | prune |
| `/home/ds/projects/aoa-yyc` | `work/aoa-yyc` | `e4c0f17936a4d101409cfd53926cc16d71509b27` | 1.23 | prune |

## Live hold and final verification

At the audit boundary, PIDs `2465206`, `2465248`, `2465281`, and `2466898`
all resolved their CWD to `/home/ds/projects/aoa-d6t.35`. The fresh hardened
dry-run independently selected 19 candidates and logged `aoa-d6t.35` as
`skipped_live_process` at `2026-07-25T22:07:18Z`.

For each candidate the audit checked:

- `git worktree list --porcelain` registration and branch binding;
- exact `HEAD` and retained `refs/heads/<branch>` equality;
- `git merge-base --is-ancestor <tip> main`;
- tracked, untracked, and ignored status;
- active open/in-progress/blocked bead `gc.work_dir` references;
- process CWD/open-FD references;
- top-level allocation and ignored-artifact contribution;
- repository ignore/build provenance;
- nested Git metadata, submodule, symlink, and mount hazards.

The recurring reaper repeats the dynamic checks immediately before each
non-forced `git worktree remove`, writes a pre-removal JSONL manifest with exact
tip/default SHAs, and never deletes the branch ref.
