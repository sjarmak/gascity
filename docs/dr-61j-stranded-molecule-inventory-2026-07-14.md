# dr-61j — stranded file-store workflow-molecule inventory

_Generated 2026-07-15T01:17:50.628140+00:00 by city-infra-pl (read-only analysis). Source: `.gc/beads.json`._

Open/in_progress workflow-marker beads: **37**. Isolated by `metadata` containing any of `gc.step_ref` / `gc.kind` / `gc.root_bead_id`. This EXCLUDES the ~1300 mail/message, session, convoy, gate, and slack-conversation-state beads that also sit open in the file store — those are NOT workflow molecules and must never be closed (gc-2568 `mayor` is the LIVE mayor session; `slack/.../state` beads are live thread state).

## Per-bead (oldest first)

| id | age(d) | status | kind | routed_to | step_ref |
|----|-------|--------|------|-----------|----------|
| gc-1920 | 87 | open | workflow | /home/ds/projects/codeprobe/codeprobe-worker | - |
| gc-1927 | 87 | open | workflow-finalize | control-dispatcher | mol-focus-review.workflow-finalize |
| gc-452961 | 9 | in_progress | workflow | city-infra-pl | - |
| gc-452962 | 9 | open | - | city-infra-pl | mol-do-work.do-work |
| gc-452963 | 9 | open | - | city-infra-pl | mol-do-work.drain |
| gc-452964 | 9 | open | workflow-finalize | core.control-dispatcher | mol-do-work.workflow-finalize |
| gc-452967 | 9 | in_progress | workflow | city-infra-pl | - |
| gc-452968 | 9 | open | - | city-infra-pl | mol-do-work.do-work |
| gc-452969 | 9 | open | - | city-infra-pl | mol-do-work.drain |
| gc-452970 | 9 | open | workflow-finalize | core.control-dispatcher | mol-do-work.workflow-finalize |
| gc-452974 | 9 | in_progress | workflow | city-infra-pl | - |
| gc-452975 | 9 | open | - | city-infra-pl | mol-do-work.do-work |
| gc-452976 | 9 | open | - | city-infra-pl | mol-do-work.drain |
| gc-452977 | 9 | open | workflow-finalize | core.control-dispatcher | mol-do-work.workflow-finalize |
| gc-453159 | 9 | open | workflow | city-infra-pl | - |
| gc-453160 | 9 | open | - | - | mol-do-work.do-work |
| gc-453161 | 9 | open | - | - | mol-do-work.drain |
| gc-453162 | 9 | open | workflow-finalize | - | mol-do-work.workflow-finalize |
| gc-453190 | 9 | in_progress | workflow | city-infra-pl | - |
| gc-453191 | 9 | open | - | city-infra-pl | mol-do-work.do-work |
| gc-453192 | 9 | open | - | city-infra-pl | mol-do-work.drain |
| gc-453193 | 9 | open | workflow-finalize | core.control-dispatcher | mol-do-work.workflow-finalize |
| gc-453232 | 9 | open | workflow | - | - |
| gc-453233 | 9 | open | - | - | mol-do-work.do-work |
| gc-458840 | 7 | open | workflow | - | - |
| gc-458841 | 7 | open | - | - | mol-focus-review.load-context |
| gc-458842 | 7 | open | - | - | mol-focus-review.workspace-setup |
| gc-458843 | 7 | open | - | - | mol-focus-review.focus |
| gc-485445 | 0 | in_progress | workflow | codex | - |
| gc-485446 | 0 | open | - | codex | mol-focus-review.load-context |
| gc-485447 | 0 | open | - | codex | mol-focus-review.workspace-setup |
| gc-485448 | 0 | open | - | codex | mol-focus-review.focus |
| gc-485449 | 0 | open | - | codex | mol-focus-review.run-tests |
| gc-485450 | 0 | open | - | codex | mol-focus-review.simplify |
| gc-485451 | 0 | open | - | codex | mol-focus-review.review |
| gc-485452 | 0 | open | - | codex | mol-focus-review.finalize |
| gc-485453 | 0 | open | workflow-finalize | core.control-dispatcher | mol-focus-review.workflow-finalize |

## Disposition classes (mayor delivered-verification worklist)

**A. LIVE — DO NOT CLOSE.** `gc-485445 gc-485446 gc-485447 gc-485448 gc-485449 gc-485450 gc-485451 gc-485452 gc-485453` — age-0 codex `mol-focus-review`, in_progress today.

**B. age-9 `mol-do-work` cluster routed to `city-infra-pl`** (6 molecules; each root + do-work + drain + finalize): roots **gc-452961, gc-452967, gc-452974, gc-453159, gc-453190, gc-453232**. Finalize beads gc-452964/452970/452977/453193/453162 route to `core.control-dispatcher` = respawn-loop fuel. Dispatched ~2026-07-05. Needs per-molecule delivered check.

**C. age-7 `mol-focus-review`** gc-458840/458841/458842/458843 — unrouted, no dispatcher finalize; low churn. Verify delivered.

**D. age-87 codeprobe strand** gc-1920 (`mol-focus-review` root, routed codeprobe-worker) + gc-1927 (`workflow-finalize` -> control-dispatcher). 2026-04-18; rig long moved on. Highest-confidence abandoned; a primary control-dispatcher churn source (cf. dr-y3yak, mayor gc-487394).

## Close path (after mayor confirms delivered, per molecule)

```
bin/gc-filestore-close --apply --note "dr-61j sweep: <reason>" <ids...>
```
flock(LOCK_EX) + timestamped backup + atomic tempfile+rename; dry-run default; refuses already-closed/missing. NEVER include the class-A LIVE codex set. Whole molecule (root + all steps + finalize) closes together so no orphan step re-routes.
