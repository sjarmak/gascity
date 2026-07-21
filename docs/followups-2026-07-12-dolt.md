# Follow-ups queued 2026-07-12 (mayor session) — dolt / order-dispatch

Queued after the supervisor-restart recovery (mayor died at startup x2 today).
Blocked on real bead creation: the city store is the `file` provider
(`.gc/beads.json`, live + locked); `gc bd` refuses (file provider) and plain
`bd` trips the dolt auto-start guard. city-infra-pl should convert these to
`gc-` beads via the correct internal path.

Priority order: tackle **after** the paper-report republish is addressed.

## 1. (P1) Dolt GC + order-tracking retention prune on the bloated `gc` DB
- **Symptom:** `gc order check` hangs ~45s; order-dispatch open-work gates
  time out (8s) — despite the canonical dolt server answering `SELECT 1` in
  0.08s. So the bottleneck is query *volume*, not dolt health.
- **Cause:** orphan `gc` (no-prefix) database noms dir = **2.14 GB** (largest
  of 24 dbs), carrying **≥501 closed order-tracking beads** with heavy history
  churn (post-restart reconcile logged `removes=18878`).
- **Also:** `dolt-config` drift — `system_variables.wait_timeout` got 3700,
  want 30.
- **Action:** run order-tracking retention prune
  (`[beads.policies.order_tracking].delete_after_close`) + dolt GC to shrink
  noms; reconcile the `wait_timeout` drift. Verify `gc order check` returns
  fast afterward.
- **Why it matters:** this starves the reconciler's `post_start_observe`
  window → the recurring mayor `died during startup` (twice on 2026-07-12).

## 2. (P2) Reap leaked dolt sql-server processes — DONE (partial)
- **Finding:** the "11" was really **2 true zombies** (PIDs 659208, 1444957 —
  deleted-worktree dolt servers for iterate-pr-4121/4122). The other 9 are NOT
  leaks: gc-managed watchdog, a live 4-day session (293694a4) with a dashboard
  preview, still-registered nested worktrees, and the personal `~/.beads` store.
- **Done 2026-07-12:** reaped the 2 zombies (SIGTERM); dolt count 12 → 10.
- **Not** the cause of canonical slowness (canonical is fast) — process/port
  debris only. No further reaping without per-process CWD+tmux triage.

## 3. (P1) City-store bead bloat — the real noms driver (tracked: `gascity-dashboard-essq`)
- **Finding (bigger than #1):** the aggregate ds-research store (supervisor
  `/v0/city/ds-research/beads?type=X&all=true`) holds ~**247K beads**:
  **task 200,685 (open ~987 → ~199,698 CLOSED)**, session 39,369, message 1,121,
  molecule 765. The `gc` city db itself is tiny (~120 beads); the bulk is
  **closed task beads across the rig stores that the reapers are not clearing.**
- **Root:** city-side, not dashboard — the fix lives in `gas-city/orders/` +
  `bin/` reapers (per essq). The order-tracking retention in #1 (7d TTL) is
  clearly not keeping pace with the closed-task accumulation.
- **Action:** audit which `orders/`+`bin/` reapers should prune closed task
  beads and why 199,698 have survived; confirm/repair the retention pipeline;
  THEN `dolt gc` to reclaim the noms (the #1 disk item). Sequence matters:
  prune first, GC second. Related dashboard beads: `essq`, `9f4l`, `pi3w`.
