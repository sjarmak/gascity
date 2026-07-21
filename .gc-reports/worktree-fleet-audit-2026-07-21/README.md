# Fleet worktree audit and detached-HEAD rescue

Captured at `2026-07-20T20:57:26-04:00` from the ephemeral source directory:

```text
/tmp/claude-1000/-home-ds-projects-persona-maker/8fec5849-6479-4525-b13a-981713e9c37e/scratchpad
```

No worktree, branch, or file was removed. Eight local rescue refs were created
to preserve every unique detached HEAD in the reported 25-row unmerged set that
was not already reachable from a local branch, remote-tracking branch, or tag.

## Source artifacts

| File | Rows/lines | SHA-256 |
|---|---:|---|
| `worktrees.tsv` | 417 | `eb6e36b06012f2dd8ba9da4eda4c1fd1a248a8a5470e37e26c729099b3ab1e72` |
| `unmerged_class.tsv` | 179 | `6808800491b564e137e9ad391d3fc423af07c8dd536a32d7677f2948b07927d6` |
| `audit_wt.sh` | 30 lines | `f3e79170e9cdf0241179efceebbe6460b77ffe64c1b76868917b526a903ffc9f` |

The TSV files have no header. `worktrees.tsv` columns are class, repo, branch,
ahead count, dirty count, and path. `unmerged_class.tsv` columns are label,
repo, worktree basename, and `new=<count>`.

## Corrections to the originating summary

- The script records 417 non-primary, existing worktrees, not 429 rows. It
  skips each primary checkout and missing paths. The larger total needs its
  scope reconciled before use.
- The classes are 148 `SAFE`, 179 `UNMERGED`, and 90 `DIRTY`; these sum to the
  417 recorded rows.
- Sixteen of the 179 `UNMERGED` rows have `new=0` in `unmerged_class.tsv`, so
  they are patch-equivalent rather than all carrying new patches.
- The 25 detached/unmerged rows sum to 582 commits ahead, but that is not the
  number of commits at data-loss risk. They represent 17 unique HEADs; 16 rows
  were already protected by containing refs. The 500-commit
  `code-intelligence-digest` HEAD was already contained by three local branches.
- Live validation found nine unprotected rows representing eight unique HEADs.
  Seven heads contain nine new patch commits; the eighth is one patch-equivalent
  SciX HEAD shared by two worktrees.
- All 25 paths still existed and were registered, clean, detached, and in no
  rebase, bisect, merge, cherry-pick, or revert operation at validation time.

## Derived evidence and mutation record

| File | Purpose | SHA-256 |
|---|---|---|
| `detached-live-audit.tsv` | Live path, registration, HEAD, base, dirty, cherry, containing-ref, and operation-state verification for all 25 detached/unmerged rows | `fcc595bd2ebf0f6690a3107bc9cf65f42381138fce9e64de3546d582e7f92cec` |
| `rescue-refs.tsv` | Exact eight refs created, target SHAs, and associated worktree paths | `1c9b33e707d6d403bcb15205f8f58a153551a6ab9bd025c6fb3e583425d59da0` |

Rescue refs use this local-only namespace:

```text
refs/heads/rescue/detached-20260721/<12-char-head-sha>
```

Post-create verification found zero unprotected HEADs in the 25-row detached,
unmerged set. No ref was pushed, checked out, merged, rebased, or attached to a
worktree. Rescue refs must remain until each head is reviewed and explicitly
landed, superseded as patch-equivalent, or authorized for deletion.

The 148 merged-and-clean rows remain cleanup candidates only. They have not yet
passed the separate live-process/session, active-bead-reference, registered
parent/child topology, and fresh pre-removal gates from `dr-6dc`.
