# Mayor startup recovery — 2026-07-21

`gc prime` completed. The required startup mail check found 32 unread messages;
all were inspected and given dispositions before store availability failed.

## What became true

- Executive status was refreshed at
  `/home/ds/brain/Projects/Executive Status/Inputs/mayor.md`.
- A supervisor restart recovered a verified order-firing stall. The check had
  shown multiple cooldown orders more than 30 minutes overdue and order-history
  lookup timeouts; `beads-health` executed again as `gc-527473` at
  2026-07-21T04:59:34-04:00.
- Resource-pressure source fixes at commits
  `78b369fe42fc089478e12f6969c9a73ee10345f1` and
  `bdae51cbf2e6f98631e37272b150dc8a2c3123f6` are tracked by `gc-xlhn`.
- The paid-cache bump request in mail `gc-527413` was falsified by independent
  history review. `mem-0ak9z` now records the correction: preserve protocol 3
  for both grids during rebase; do not bump to 4.
- The decisions rig now has `.gc/project-brief.md`.
- Dispatch-policy analysis from mail `gc-527400` is preserved in
  `.gc-reports/gc-485753-dispatch-policy-independent-analysis-2026-07-21.md`.
- Worktree cleanup evidence from mails `gc-527095` and `gc-527464` is recorded
  on `dr-6dc`; no reset or cleanup was performed.
- Dashboard target `gascity-dashboard-z7u3` remains branch-ready and blocked.
  External push decision `dec-8od` is open for exact SHA
  `98495f0ee9821304b3f775390334e381ea580b6d`.

## Recovered fault

At 2026-07-21T05:56:21-04:00, the canonical Dolt process had exited,
`.beads/dolt/.dolt/sql-server.info` was missing, stale runtime state still named
PID 3018479 on port 29620, and the host load was 139.44 / 221.86 / 229.15.
Amp could no longer start even a trivial shell command, so no further recovery
or mail-state writes were attempted. Five messages had been marked read after
disposition (`gc-525702`, `gc-525780`, `gc-525830`, `gc-525869`,
`gc-525957`); `gc-526188` also completed its mark-read command. Remaining mail
was inspected but could not be marked read without the store.

At 2026-07-21T06:13:53-04:00, after `gc doctor --fix` confirmed that the Dolt
runtime was still unavailable, the supervisor was restarted through the final
documented recovery rung. The canonical endpoint recovered with Dolt PID
1867426 on 127.0.0.1:29620. The new
`.beads/dolt/.dolt/sql-server.info` and runtime state agree, the PID is alive,
the socket is listening, and a TCP `SELECT 1` succeeds. Supervisor state was
active after the restart. Mails `gc-527524`, `gc-527530`, and `gc-527540` are
the same outage observed by AOA workers; their requested city-infrastructure
action is now complete, and none reported partial work that requires rollback.
Post-recovery verification found the AOA workflow active again: `aoa-3ump`
closed its load-context step at 2026-07-21T10:19:32Z, its workflow root remains
in progress, and target `aoa-ghwi` is readable and open. Order execution also
resumed after the second recovery: `beads-health` executed as `gc-527547` at
2026-07-21T06:16:35-04:00.

The first recovered process later exited under continuing host pressure. A
foreign auto-started Dolt then occupied 29620 with an empty data directory, so
the managed server correctly restarted on 29621 with all 27 schemas. The
supervisor's adopt-path stopgap still pointed its control-plane queries at
29620. The foreign listener exited; the systemd drop-in was snapshotted as
`10-dolt-port.conf.bak-rebind-29621-20260721T115241Z`, rebound to 29621 with a
rollback comment, daemon-reloaded, and the supervisor restarted. Verified
state: info file and runtime state agree on managed PID 2784208 / port 29621;
TCP sees 27 schemas; systemd exports both endpoint variables as 29621; and
`beads-health` executed as `gc-527689` at 2026-07-21T07:53:42-04:00.

The three mails that arrived during the extended recovery also have durable
dispositions. Resource alert `gc-527624` is recorded as runtime evidence on
`gc-xlhn`. Temporal alert `gc-527640` is recorded on `gc-372`: the 10:00Z
cycle failed before create/sling because Dolt was unavailable, leaving no
pending claim or orphan bead; the soak gate remains open. Tmp-reaper mail
`gc-527645` reports a completed 0.6G cleanup and needs no follow-up.

## Completion

Startup initialization is complete. Mails `gc-527624`, `gc-527640`, and
`gc-527645` were marked read individually after disposition; final
`gc mail count --json` reported total 3 / unread 0 and the inbox was empty.
The managed endpoint still answers on 29621 and post-rebind order execution is
recorded above. The sole open Stephanie decision remains `dec-8od` for the
exact-SHA dashboard branch push.

On the next wake, run `gc mail count` first.
