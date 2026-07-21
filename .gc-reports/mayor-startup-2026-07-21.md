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

## 09:55 resumption and second recovery

`gc prime` completed again after the session resumed. This resumption processed
41 individual unread messages as they arrived; every message was inspected,
given a durable disposition, and marked read separately. Final
`gc mail count --json` reports total 20 / unread 0.

The canonical Dolt endpoint moved back from 29621 to 29620 during the outage
sequence. Final ground truth now agrees end to end: info file and runtime state
name PID 638571 / port 29620, the systemd drop-in and live supervisor
environment export both endpoint variables as 29620, the supervisor is active,
the PID listens on 127.0.0.1:29620, direct `SELECT 1` succeeds, and an AOA rig
query succeeds. Older AOA sessions that retained 29621 in their startup
environment are a different live contract defect; source P0 `gc-end7` now
tracks managed port rotation without bootstrap or divergent per-rig servers.

The AOA enforcement fail-open report was independently confirmed from source:
errors in `run_check` reach generic exit 1 while the hook protocol blocks only
on exit 2. P0 `aoa-s6m7` records the exact boundary and acceptance criteria and
is routed through workflow `aoa-q6mm`. Its newly provisioned worktree reproduced
the detached-HEAD P0 before implementation; mayor verified it was clean and
created `work/aoa-s6m7` at the exact detached head. Fleet prevention remains on
`dr-6dc` / `gc-r9fx`; the one branch attachment is containment only.

AOA outage artifacts are consolidated on `aoa-wvhu`, assigned to `aoa-pl`.
Current corrections are durable there: `aoa-ych` is reachable from main at
`04c23a4`; `aoa-ghwi`, `aoa-x5ai`, `aoa-wew0`, and `aoa-1lq0` remain preserved
on branches not reachable from current main and require exact-head test/review
verification before finalization. No bootstrap, remote clone, blind metadata
pass, or hand-land was performed.

The shared `mol-focus-review` sentinel change received an independent review
that did not invoke the formula under review. `dr-dwrn` closed PASS; all eight
unset/empty/SKIP/resolved x code/non-code cases passed, current formula hash
`fe47e38cfee570ad22b5a0a2cc5f715fa7de5547ac7710583ea5533664f73487`
was retained, and the report
`.gc-reports/dr-dwrn-independent-mol-focus-review-2026-07-21.md` hashes to
`28a0b3cd99710e70ecdf16dcfdd23dba7fcc701df68de00dd794ac62db6170b4`.

Resource-pressure mails are acceptance evidence on `gc-xlhn`; the source-owner
handoff now names the reviewed dispatcher and bd-subprocess commits and is
assigned to `gascity-maintenance-pl`, with no install, restart, recycle, push,
or MCP mutation authorized. The mem stale-branch recovery is authorized
internally on `mem-qylng`: preserve protocol 3, retire the empty-test root, and
use one fresh rebase/re-pin/test/review lane. It is not a paid-rerun decision.

Terminal escalation `te-434daaff6169` was acknowledged and disposed as split
work. Pack P0 `gpk-tj3d5` owns the missing `pour = true` in the gascity-packs
formula; source `gc-2lab` records the live RootOnly root. The separate dirty
`polecat-5` slot remains quarantined: its staged state is preserved by local ref
`refs/wip/polecat-5-orphaned-staged-revert-20260721` at
`06d20565507a20200c2e558160defd0c393e5246`, and nested processes are still
live, so no reset or cleanup was attempted. `gpk-tj3d5` is blocked because its
dedicated clean pool is administratively suspended; `dec-obi` is the bounded
resume decision. EnterpriseBench root `EnterpriseBench-4rok5` remains the one
routed-open root with no duplicate pour; its worker pool is also suspended, so
no spawn was forced under the resource-pressure posture.

## 10:32 resumption — suspended-rig split-brain

`gc prime` completed on the next wake. Two AOA workers reported that they were
spawned after the rig was suspended, then `gc hook --claim --drain-ack` failed
before drain acknowledgement with the misleading error that active agent
`aoa/claude-4` was suspended. Independent ground truth is internally
contradictory: `gc rig list` reports AOA suspended, `gc rig status aoa` reports
`suspended=false`, agent configuration reports `aoa/claude-4` active, and the
reconciler had just spawned two workers. Neither worker claimed work or touched
files; one explicitly drain-acked.

Gas City P0 `gc-7u05` now owns the suspension source-of-truth, spawn-race,
misattributed-error, and skipped-drain path. AOA target `aoa-s6m7` records the
hold and remains preserved on `work/aoa-s6m7`; no rig resume, duplicate sling,
bootstrap, or blanket session kill was attempted. Both mails (`gc-527949`,
`gc-527951`) were marked read individually after those durable dispositions;
the next mail count returned unread 0.

Disk-pressure guard mail `gc-527985` reported a completed cache-only reclaim:
33.5G freed and root free space increased from 178G to 211G. This is a
completed guard action, not a second cleanup request; no additional deletion or
cache sweep was run.

## 10:40 resumption — morning triage deliberately not re-fired

`gc prime` completed on the next wake. Watchdog mail `gc-528010` reported that
the 07:00 morning-triage cycle missed its briefing. Ground truth confirms a real
miss rather than a missing audit line: order history records execution
`gc-527601` at 07:07 EDT, the audit ends at `bead_create_failed`, and there is
no same-date tracking bead, label, or briefing artifact.

The cycle was deliberately not re-fired. Its configured target
`/home/ds/gascity/polecat` is administratively suspended with zero live
sessions; a manual run would create and sling into a pool that cannot claim,
stranding another workflow instead of delivering the briefing. Child bug
`dr-s0xu73.1` now owns create-error capture, output parsing, retry idempotence,
suspended-pool refusal, and watchdog failure classification. No pool resume or
duplicate triage bead was created.

## 10:44 resumption — executive status and suspended-pool routing

The required executive-status input was refreshed with health `at-risk`, a
citywide stabilization focus, and the business risk from infrastructure
instability and deliberately constrained capacity. No routine Slack update was
posted.

Resource alert `gc-528032` is additional acceptance evidence on `gc-xlhn`:
available memory and load had recovered, but SwapFree remained 54.6M with
1403.7M sampled swap I/O. The source P0 is assigned to
`gascity-maintenance-pl`; this evidence does not authorize a broad process
cleanup, runtime mutation, deploy, or restart.

Dead-pool alert `gc-528037` reported three recent unassigned maintenance-cycle
beads routed to `/home/ds/gascity/polecat`. Independent ground truth contradicts
its proposed short-name reroute: only one `polecat` agent exists, that qualified
target is suspended with pool minimum zero, and no live session exists. The
evidence is attached to suspension P0 `gc-7u05`, now assigned to
`gascity-maintenance-pl`; no reroute, re-sling, resume, or force-spawn was
performed. The morning-cycle reliability child `dr-s0xu73.1` also received its
missing `city-infra` label, making it visible to its assigned pulling consumer.
All four mails from this resumption were marked read individually after these
durable dispositions; final mail count reported unread 0.

## Open Stephanie decisions

[1] `dec-obi` — authorize one clean `gascity-packs-polecat` worker for P0
`gpk-tj3d5`, or keep the pool suspended? (raised 2026-07-21)
[2] `dec-me6` — authorize correcting the stale paid-rerun ask in Slack, or
leave the old ask visible? (raised 2026-07-21)
[3] `dec-8od` — authorize pushing dashboard branch
`work/gascity-dashboard-z7u3` at exact SHA
`98495f0ee9821304b3f775390334e381ea580b6d`, or keep it local? (raised
2026-07-21)

Next action: obtain Stephanie's ruling on `dec-obi`; if approved, resume only
the dedicated gascity-packs pool and verify one clean worker claims
`gpk-tj3d5` before any further dispatch.
