# Local-only branch audit — 2026-09-16 (gc-y0c19)

Re-measurement of the local-only-branch exposure tracked on `gc-y0c19`, done
from a clean worktree at `origin/main` = `7a34e556ebb8bac29f1ff74d1f3848c1ac7314f7`.
This is a read-only triage and observability proposal. **Nothing was deleted,
pruned, or force-moved.** No branch, worktree, or ref was touched. This
report is committed to `work/gc-y0c19` per the rig's publication hold; it is
not pushed and no PR is opened (see the two dispatch notes on `gc-y0c19`).

## Method

Prior passes (2026-08-10, 2026-09-15) tested reachability against `refs/remotes/*`
generically or against bare `main` (which has been diverged from `origin/main`
for months in this checkout and is not a valid baseline). This pass narrows the
reachability test to the two remotes that matter — `origin` (gastownhall/gascity,
canonical) and `fork` (sjarmak/gascity) — per the 2026-09-16 dispatch note.

```
git fetch origin --prune
git fetch fork --prune
git rev-list --remotes=origin --remotes=fork > reachable.txt   # ancestor-commit set
git for-each-ref refs/heads --format='%(objectname) %(refname:lstrip=2)' > local.txt
# a local branch is "local-only" iff its tip is NOT in reachable.txt
```

This checks per-branch ancestry against the actual tip set of `origin/*` and
`fork/*` refs (16,079 distinct commits), not a merged-into-`main` check, and
runs in under a second because it is one `rev-list` call plus a set-membership
join instead of one `--contains`/`merge-base` invocation per branch. It shares
the squash-blindness of every ancestry-based test noted in the 2026-08-10 pass:
a branch whose content landed upstream via squash merge still tests local-only
here. That is a known, documented limitation, not a defect introduced this
pass.

## Headline numbers, measured today

| Metric | 2026-08-10 (final) | 2026-09-15 | 2026-09-16 (this pass, origin+fork only) |
|---|---|---|---|
| Local branches (`refs/heads`) | 635 | 1072 | **1090** |
| Local-only (no origin/fork ancestry) | 257 | 549 | **623** |
| Registered worktrees | — | — | **278** |

The prior two passes computed local-only-ness against a broader/looser
reachability definition (`refs/remotes/*` across all 16 configured remotes, or
bare `main`). Re-run today with the corrected origin+fork-only definition, the
number is 623, not directly comparable to the 257/549 series above but the
trend (branch count growing without bound, nothing consuming the observability
output) is the same finding the 2026-09-15 pass already made. I am not
replacing that finding; I am supplying a numbers with a stated, narrower
methodology as the dispatch note asked.

## Breakdown of the 623 local-only branches

| Tier | Count | Basis |
|---|---|---|
| Worktree-held | 164 | branch is checked out in a registered `git worktree` (city-infra-pl's lane — not touched) |
| Keep-prefix | 109 | name starts `preserve/` (92), `backup/` (12), or `rollback/` (5) — declares itself non-disposable |
| Forward-linked to a live bead | 59 | branch name contains a `gc-` id that resolves in the store to `open`/`in_progress`/`blocked`/`deferred` |
| Reapable-candidate (none of the above) | **323** | not held by a worktree, no keep-prefix, no forward-linked live bead id in the name |

Tiers are not disjoint from each other in general (a worktree-held branch can
also be forward-linked), so the four rows do not sum to 623; the "none of the
above" row is the actual complement and is the only number I am treating as an
input to any future reap.

**I did not run the reverse-linkage check** (live bead's `gc.work_branch` /
`gc.pinned_branch` / thirteen other metadata keys pointing at a branch that
carries no id in its own name). The 2026-09-15 pass on this bead already
established that forward and reverse linkage are different questions and
catch different branches, and specifically that `_pr1945_check`-shaped
branches (no id in the name, live bead points at it) exist in this store.
Treat the 323 "reapable-candidate" figure as an **upper bound on true
reapable branches**, not a delete list: it has not been checked against
reverse linkage, open GitHub PRs/issues, or recency, all of which the 2026-08-10
pass showed materially shrink a naive candidate list (139 GitHub-number
references cut 62 off a 282-candidate list; 56 branches were held purely for
being ≤14 days old).

### The 59 forward-linked-to-a-live-bead branches

Full list (branch → live bead id):

```
backup/gc-nukq-pre-clean-20260721        gc-nukq
bd-gc-0irqa                              gc-0irqa
bd-gc-1fbg                               gc-1fbg
bd-gc-28jm                               gc-28jm
bd-gc-ami4-3926-orphan-tolerant-delete   gc-ami4
bd-gc-dass-4299-nudge-sweep-expiry       gc-dass
bd-gc-mt22                               gc-mt22
fix/gc-12a1fr-dropped-sweep-kick         gc-12a1fr
fix/gc-3l55-orphan-borrow-veto-visibility gc-3l55
fix/gc-4rrz-3862-reprojection-coverage   gc-4rrz
fix/gc-6tywt-idle-flake                  gc-6tywt
fix/gc-9tql-continuation-owner-guard     gc-9tql
fix/gc-b2xeu-session-reuse               gc-b2xeu
fix/gc-b8r7x-guard-branch-id-prefix      gc-b8r7x
fix/gc-dn3mx-ci-go-version-from-gomod    gc-dn3mx
fix/gc-ecryh-order-check-bounded         gc-ecryh
fix/gc-ewk4-salvage-assigned-tier-widen  gc-ewk4
fix/gc-gdljl-mail-inject-recency         gc-gdljl
fix/gc-gyksx-registry-loss-hold          gc-gyksx
fix/gc-j4sr-salvage                      gc-j4sr
fix/gc-m6y2s-restore-stage1-test         gc-m6y2s
fix/gc-og1z-fence-crash-window           gc-og1z
fix/gc-p8hwb-nudge-disposition           gc-p8hwb
fix/gc-q68wq-doctor-timeout-budget       gc-q68wq
fix/gc-rdrt1-native-conditional          gc-rdrt1
fix/gc-s0j9g-provider-divergence         gc-s0j9g
fix/gc-tblk-record-lock-clock            gc-tblk
fix/gc-tiav2-defer-evidence              gc-tiav2
fix/gc-ugmpl-suspended-agent-gating      gc-ugmpl
preserve/gc-p8hwb-96591e8c4              gc-p8hwb
preserved/gc-tbcdp-branch-ready-88fa7411 gc-tbcdp
recovery/gc-28jm-current                 gc-28jm
recovery/gc-28jm-gated                   gc-28jm
recovery/gc-r9fx-20260824                gc-r9fx
recovery/gc-u6an-current-cg89            gc-u6an
salvage/gc-j4sr-recut-20260901           gc-j4sr
wip/gc-28jm-verify                       gc-28jm
work/gc-12a1fr                           gc-12a1fr
work/gc-1usp4d                           gc-1usp4d
work/gc-37cmv0                           gc-37cmv0
work/gc-7sxa                             gc-7sxa
work/gc-8fx8z.2.2                        gc-8fx8z
work/gc-a3y1l                            gc-a3y1l
work/gc-b36ye-recut                      gc-b36ye
work/gc-e6971                            gc-e6971
work/gc-ejm38                            gc-ejm38
work/gc-f0jiq                            gc-f0jiq
work/gc-f0jiq-hooks                      gc-f0jiq
work/gc-fg8u7                            gc-fg8u7
work/gc-g7x3t                            gc-g7x3t
work/gc-iipgc-orig-backup                gc-iipgc
work/gc-j3c8e                            gc-j3c8e
work/gc-j4sr-remediate11                 gc-j4sr
work/gc-on04y.1                          gc-on04y
work/gc-pdhc9                            gc-pdhc9
work/gc-tbcdp                            gc-tbcdp
work/gc-uxxhov                           gc-uxxhov
work/gc-vf1mg                            gc-vf1mg
work/gc-w4oq1                            gc-w4oq1
```

Notably `fix/gc-b2xeu-session-reuse` — the branch this bead's own opening
inventory named as the single largest local-only branch on 2026-08-10 (33
commits) — is still local-only 37 days later and still points at a live bead.
It has not regressed to closed/absent; it has simply never been consolidated.
Of the 190 distinct `gc-` ids found across all 623 branch names, 176 resolved
in the store and 54 of those are live; 14 ids did not resolve at all (absent,
same trap the 2026-08-10 pass documented: a regex hit is not proof of
linkage, and an absent id is a genuine delete signal only if the correct
store was queried, which it was here — `gc-` ids are native to the gascity
rig store).

## What this pass does NOT re-establish

- **Reverse linkage** (bead metadata → branch). Not re-run; treat prior
  findings (2026-09-15 comment on this bead) as still authoritative until
  someone re-runs it.
- **Open GitHub PR/issue linkage.** Not re-run; the 2026-08-10 pass found this
  materially changes the delete list (42/139 checked numbers were still open)
  and nothing here supersedes that caution.
- **Age and per-branch commit-count histograms.** Not recomputed this pass;
  scope was the remote-reachability re-measurement the dispatch note asked
  for. A future pass should recompute these against the 323-branch candidate
  set, not the original 95, since the population has grown 6.5x.
- **Squash-merge detection** (cumulative patch-id against an upstream index).
  Not re-run; expensive and the 2026-08-10 method for it is already
  documented on this bead for reuse.

## Recommendation

Do not act on the 323-branch "reapable-candidate" figure directly — it is a
candidate list, not a delete list, for the same reasons the 2026-08-10 pass
found the naive candidate list wrong by tens of branches in both directions
(under- and over-inclusive). The consolidation work that bead scope 2
("consolidate the keepers") anticipated has not happened for the branches
identified as keepers on 2026-08-10 (`fix/gc-b2xeu-session-reuse` is proof:
still local-only, still live, unchanged in status for 37 days). That is a
process gap, not a measurement gap — the observability half of this bead
(`bin/local-only-branches.sh`) already surfaces this population; nothing is
consuming it to actually push or land the 59 forward-linked branches. The
concrete next action is not another audit: it is deciding who executes the
push-to-fork step for the confirmed-live 59, and fixing
`bin/local-only-branches.sh`'s three outstanding defects (forward linkage,
keep-prefix awareness, the `refs/heads/heads/<name>` tag-collision bug) so it
stops sitting at a permanent non-zero exit that nobody is acting on. Those
three fixes were already scoped in this bead's 2026-09-15 comment and remain
outside this rig's ownership (`bin/local-only-branches.sh` lives in
`/home/ds/gas-city/bin`, city infrastructure).

No branch, worktree, or ref was deleted, pruned, or force-moved in the
production of this report.
