# dr-6dc — Gas City worktree-hygiene enforcement census

**Snapshot:** 2026-07-20 18:59–19:04 EDT  
**Scope:** the 181 worktrees registered in `/home/ds/gascity`'s common Git directory  
**Machine-readable manifest:** `dr-6dc-worktree-census-2026-07-20.tsv`  
**Manifest SHA-256:** `1d1c88d475a23bfa8b76dd29d8cdf6bbad508f734a1eb55e5fcca63e4afbeb73`

No cleanup or runtime action was performed. The scan was sequential, used one bounded `/proc` snapshot, three bounded active-bead reads from `rig:gascity`, one bounded session-list read, sequential Git probes, and low-priority/idle-I/O `du` with an eight-second per-tree timeout.

## Reproduced census and classification

| Measure | Current count |
|---|---:|
| Registered worktrees | 181 |
| Detached HEAD | 68 |
| Nested-layout paths (`/worktrees/`) | 31 |
| Nested under another registered worktree | 22 |
| Registered parents containing registered children | 7 |
| Dirty | 41 |
| Unreadable | 0 |
| Live process reference | 36 |
| Live gc session reference | 8 |
| Active gascity bead `work_dir` reference | 22 |
| Safe cleanup candidates | 29 |
| Preservation-required WIP | 152 |
| Safety-unknown/fail-closed | 0 |
| Size measured | 181/181 |
| Non-exclusive measured size | 40,371.2 MiB |

“Non-exclusive” matters: sizes of nested registered trees are also included in their registered parent. The manifest carries both `nested_under` and `nested_children` so cleanup tooling can avoid double-counting or parent-first removal.

## Manifest contract

Every registered tree has one TSV row with:

- registered path, HEAD, branch, detached/locked/prunable state;
- nearest registered parent and registered-child count, plus the 31-tree nested-layout marker;
- provisioning-owner classification;
- live process PIDs, gc session IDs/names, and active bead IDs/statuses with `rig:gascity` store provenance;
- dirty/clean/unreadable result;
- HEAD reachability from `origin/main` and any remote ref;
- commit age, path age, measured size or bounded-probe failure;
- current `janitor-worktree-gc` and `stale-worktree-reaper` candidate booleans;
- final class: `safe-cleanup-candidate`, `preservation-required-wip`, `safe-prevention/protected`, or `unknown/fail-closed`.

Provisioning ownership is derived from the actual path contract, not branch names: shared primary, gcsync binary tree, framework pool slot, gc-sling legacy/current per-bead tree, mol-focus-review workspace setup, nested/legacy polecat provisioner, recovery, ship, PR-review, Claude reviewer, specialized/manual, or unknown/manual. Six trees have unknown/manual ownership; this is an ownership-attribution unknown, not a cleanup-safety unknown.

## Current safe cleanup queue — do not execute from this bead

Twenty-nine clean, `origin/main`-reachable trees have no live process, gc session, or active gascity bead reference. Their non-exclusive size is 2,476.4 MiB. The manifest is the authoritative row-level list.

Five are ≥3-day detached trees selected by `janitor-worktree-gc` but excluded by `stale-worktree-reaper`'s detached-head gate:

- `polecat-1-q5mp`
- `polecat-1-rmnq`
- `polecat-2-2nkx`
- `polecat-2-7zhu`
- `polecat-6-i42a`

Six are selected by `stale-worktree-reaper` but not by the janitor:

- three outside `/home/ds/gascity-worktrees`: the PR 4075, 4093, and 4102 trees;
- three beneath the janitor's protected `polecat/` namespace: `gc-eqbom`, `gc-akvn6`, and `gc-doi5s`.

The remaining 18 are clean/merged/inactive but younger than a current age threshold, detached below the stale reaper's gate, outside one tool's scope, or otherwise not selected by either current patrol. They belong to terminal-lifecycle cleanup design (`dr-97p`), not an ad-hoc bulk removal.

## Preservation-required WIP

The 152 preserved rows include any tree with one or more of:

- a live process or active/asleep gc session tied to the tree;
- an open, in-progress, or blocked gascity bead `work_dir` reference;
- dirty working-copy state;
- an unmerged HEAD or commit unreachable from every remote;
- shared-primary/pool/nested-parent infrastructure whose removal would invalidate child/session ownership.

Source P0 `gc-r9fx` is correctly preserved at `/home/ds/gascity-worktrees/polecat-3/worktrees/gc-r9fx`: nested-layout, dirty (one entry), branch `work/gc-r9fx`, unmerged, and therefore `preservation-required-wip`. This census must not race or “help” that source lane by modifying its tree.

## Existing patrol overlap and gaps

### Coverage overlaps; current candidate sets do not

Both patrols inspect registered Gas City worktrees and classify clean/merged/aged state. With current state and configured thresholds:

- janitor candidates: 5;
- stale-reaper candidates: 6;
- candidate intersection: **0**.

The zero intersection is a policy split, not proof that duplicate patrols are harmless: the janitor accepts detached merged trees and is scoped to `/home/ds/gascity-worktrees`; the stale reaper rejects detached trees, scans across rigs/outside that directory, and honors live-process/active-bead references.

### `janitor-worktree-gc` unsafe race boundary

The daily janitor is configured `JANITOR_MODE=--execute`. It checks clean/merged, location, protect regex, nested registered children, path age, and a removal cap. It does **not** check live process/session ownership or active bead `work_dir` immediately before removal. Its final non-forced `git worktree remove` protects dirty files but does not protect a clean tree actively read or executed by a session. This is the highest-risk current enforcement gap.

### `stale-worktree-reaper` stronger runtime gates, incomplete topology gate

The hourly stale reaper is currently dry-run and checks live process paths plus active open/in-progress/blocked bead references, failing closed on store errors and rechecking before apply. It does not reject a registered tree merely because that tree contains another registered worktree. A parent-first candidate can therefore be reported even though non-forced Git removal may later refuse it. It also excludes all detached trees, leaving the five current detached janitor candidates invisible to its cleanup set.

### Safe consolidation boundary

One authoritative classifier should own shared safety predicates: readable/clean, process/session-free, active-reference-free with store provenance, topology-safe (not a registered parent), ref-reachable, and terminal/age policy. Patrols may differ in schedule/scope, but they must not implement divergent safety gates. Any store/process/topology probe failure must remain `unknown/fail-closed`.

## Coordination boundaries

### Prevention: source P0 `gc-r9fx`

`gc-r9fx` owns transactional provisioning prevention in `/home/ds/gascity-main`: one workspace owner must ensure and verify the worktree, reject dangling/non-worktree stamps, record the provisioning owner/store reference, and avoid two provisioners independently treating a path as valid. This city-infra lane supplies the current topology and owner census only; it does not edit source or the dirty `gc-r9fx` worktree.

Minimum source acceptance additions suggested by this census:

1. fail closed when an existing `work_dir` is not the exact registered worktree expected for its bead/store;
2. reject parent/child registration ambiguity unless the provisioning contract explicitly owns nested layout;
3. emit durable owner + bead/store-ref metadata at successful provisioning;
4. prove concurrent ensure calls converge on one registered tree and one owner;
5. never silently fall back to a shared primary.

### Terminal cleanup: `dr-97p`

`dr-97p` owns terminal success/rejection/cancel/drain/finalize behavior. It should consume the same classifier and either remove a clean landed, topology-safe, unreferenced tree or durably set `gc.worktree_cleanup_pending=true`. Dirty, unmerged, live, active-referenced, unreadable, or nested-parent trees must be preserved. A patrol clears the marker only after all gates pass again.

The 29 current safe candidates are test fixtures for that lifecycle contract, not authorization to remove them.

## Safe prevention, cleanup, WIP, and unknown decisions

- **Safe prevention now:** add shared fail-closed classification and ownership proof in `gc-r9fx`; route terminal state through `dr-97p`; make the executing janitor honor live/session/active-ref/topology gates. No runtime mutation is required to specify or test these contracts.
- **Safe cleanup candidates:** 29 rows, subject to a separately authorized terminal cleanup action and a fresh just-before-action recheck.
- **Preservation-required WIP:** 152 rows; no cleanup consideration until their live/ref/dirty/unmerged reason clears.
- **Safety unknowns:** zero in this bounded snapshot. Six provisioning owners remain `unknown/manual` and should stay explicitly unknown until source provenance is recovered; owner inference must never grant cleanup permission.

## Verification

- Manifest has 182 lines: one header + 181 unique registered paths.
- Counts reproduce the supplied ground truth: 181 registered, 68 detached, 31 nested-layout paths.
- All 181 dirty-state and size probes resolved; active-bead and session queries returned without errors.
- Manifest SHA-256 is recorded above for handoff and drift detection.
