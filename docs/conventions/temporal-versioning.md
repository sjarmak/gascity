# Temporal workflow/worker versioning gate (gc-4zf.9)

Operational gate for changing a **deployed** Temporal Workflow definition in
this install. Lands in change-control BEFORE Track D deploys anything
long-running. Companion to `services/temporal-maintenance/deploy/CUTOVER.md`
(deploy mechanics) and `.claude/skills/cityops-city-change-control` (the change
process itself).

Install facts this gate assumes (re-verify if any drift): one worker
(`temporal-maintenance-worker.service`, binary
`services/temporal-maintenance/bin/maintenance-worker`), one workflow type
(`MaintenanceCycleWorkflow`), task queue `temporal-maintenance-shadow`,
namespace `maintenance`, server 127.0.0.1:7233 (start-dev, sqlite; server
1.31.2, CLI 1.8.0, Go SDK 1.46.0). Schedule `maintenance-cycle`: every 120m,
fires at **even hours UTC**, Skip-overlap, `dispatch_only` input. A healthy
dispatch-only cycle completes in **seconds** (17-event history).

## 1. Does this change need versioning treatment?

Replay verifies a running history by re-executing the workflow function and
matching the **commands it emits** (activity schedules, timers, child
workflows, side effects, version markers) against the recorded events — by
command type and activity type/ID, **in order**. The gate question is only:

> Can this edit change WHICH commands the workflow emits, or their ORDER, for
> a history that was recorded by the old code?

| Change | Treatment |
|---|---|
| Activity-internal code (`real_adapter.go`, `maintenance_runner.go`, `execstore.go`, `escalation.go`, bridge, cmd/, prompts) | **None.** Activities are never replayed; a completed activity's result comes from history. |
| `workflow.ActivityOptions` (timeouts, RetryPolicy) | **None** for replay. Options are not compared during replay. Caveat below. |
| Add / remove / reorder an `ExecuteActivity`, timer, `workflow.SideEffect`, child workflow, or signal send; change an activity **function name**; move code across an `Await`/`Select` boundary; change loop bounds that emit commands | **Versioning treatment** (§2). |
| Change a Signal/Update/Query **name** constant | **Versioning treatment** — in-flight cycles and the bridge/orders speak the old name. |
| Fields of `state.go` types (history/query payloads) | Replay-safe if JSON-compatible (add optional fields OK; rename/retype = treatment: old payloads must still decode). |
| Bump `go.temporal.io/sdk` in go.mod | Treat as a workflow change: run the replay gate; read the SDK changelog for determinism notes. |

Regardless of the table: **the replay gate runs on every change that touches a
gate-trigger file (§4)**. It is cheap and executable:

```bash
cd services/temporal-maintenance
GOFLAGS=-mod=mod go test -run TestReplay .
```

Two pinned fixtures, both from real executions: `testdata/
maintenance_cycle_history.json` (gated path) and `testdata/
dispatch_only_history.json` (production dispatch-only path, captured from the
live server). A failure = the change is NOT replay-safe → §2.

### Worked example: gc-372.1 (commit 6024920, 2026-07-21) — no treatment

The fix moved the selection in-flight read (`gc bd list`) out of the
at-most-once execstore unit into a pre-claim `Preflighter`, split retryable
read failures from `PermanentPreflightError`, and widened the selection
RetryPolicy in `workflow.go` (3×1s → 5s initial, ×2, 60s cap, 8 attempts).

- `real_adapter.go` / `maintenance_runner.go`: activity-internal. Never
  replayed. No treatment.
- `workflow.go` ActivityOptions: a workflow-file edit, but the command
  sequence is unchanged — same two `DispatchSelection` schedules, same order,
  same activity types/IDs. Replay matches by ID/type; options are not part of
  the determinism check. No treatment.
- The one real consequence: **ActivityOptions apply only to newly scheduled
  activities.** An activity already scheduled by the old code keeps the retry
  policy recorded at schedule time until it resolves. Moot here (cycles are
  fresh-start dispatch-only), but on a long-running Track D workflow this
  means an options fix does NOT rescue an in-flight activity — plan for that.
- Executable proof: `testdata/dispatch_only_history.json` was recorded by the
  **pre-fix** worker and replays green against the post-fix code
  (`TestReplay_FromCapturedHistory/dispatch_only`).

## 2. Treatment mechanisms (current APIs only)

**API caveat (bead gc-4zf.9 note):** build only on the CURRENT Worker
Versioning API — Worker Deployments, GA 2026 — plus `workflow.GetVersion`.
**NOT** the pre-2025 experimental Build-ID path (`worker.Options.BuildID` +
`UseBuildIDForVersioning`, `temporal task-queue update-build-ids` /
`UpdateWorkerBuildIdCompatibility` version sets): deprecated in SDK 1.46.0 and
removed from server support ~March 2026.

Pick ONE, in this order of preference for this install:

1. **Drain deploy (default while cycles are seconds long).** With zero open
   histories nothing is ever replayed against new code, so ANY change is safe.
   §3 is this procedure. If the change window can't fit between fires, pause
   the schedule (`temporal schedule toggle --schedule-id maintenance-cycle
   --pause --reason "<bead>"`), drain, deploy, unpause. One standing rule it
   leaves behind: after an unversioned command-sequence change, never
   `temporal workflow reset` an execution recorded by the old code — reset
   replays old history against current code.
2. **`workflow.GetVersion` in-place patch** — when a history must stay open
   across the change (future full path: a cycle parked at the human gate).
   `v := workflow.GetVersion(ctx, "<change-id>", workflow.DefaultVersion, 1)`;
   branch old/new on `v`. One change-id per change, never reuse or renumber;
   remove the old branch only when no open history predates the patch; replay
   gate must hold fixtures from both sides while both branches live.
3. **Worker Versioning (pinned Worker Deployments)** — when drain windows
   disappear (Track D). Worker declares
   `worker.Options{DeploymentOptions: worker.DeploymentOptions{UseVersioning:
   true, Version: worker.WorkerDeploymentVersion{DeploymentName:
   "temporal-maintenance", BuildID: <git rev>}}}` and registers workflows with
   `workflow.RegisterOptions{VersioningBehavior:
   workflow.VersioningBehaviorPinned}`. Roll forward with `temporal worker
   deployment set-current-version --deployment-name temporal-maintenance
   --build-id <new rev>`: new executions route to the new version, pinned
   in-flight histories drain on the old worker process (two worker processes
   run during drain — the systemd single-unit model must grow a second
   templated unit first). Do not adopt before Track D needs it (§5).

## 3. Deploy checklist for this install (drain deploy)

Change-control preamble: commit-before-flip per
`cityops-city-change-control`; the diff carries bead ID + the §1 verdict
("versioning treatment: none, because …" or the chosen mechanism).

1. Tests green by execution, replay gate included:
   `cd services/temporal-maintenance && GOFLAGS=-mod=mod go test -race ./...`
2. Confirm the fire gap. Fires are at even hours UTC; a cycle is seconds
   long. Deploy inside the gap, not within 10 min of the next fire:
   `temporal schedule describe --namespace maintenance --schedule-id
   maintenance-cycle` (check next fire time).
3. Confirm drained — zero running executions:
   `temporal workflow count --namespace maintenance --query
   'ExecutionStatus="Running"'` → `Total: 0`. Non-zero: a cycle is riding out
   a retry window (post-gc-372.1 that can stretch minutes) or is wedged —
   wait for it to settle or treat via §2; never restart the worker under an
   in-flight history you haven't versioned for.
4. Build and restart:
   `make build && systemctl --user restart temporal-maintenance-worker.service`
5. **Verify the deployed binary is the new one** (in-place fix ≠ deployed):
   ```bash
   PID=$(systemctl --user show -p MainPID --value temporal-maintenance-worker.service)
   sha256sum /proc/$PID/exe bin/maintenance-worker   # hashes must match
   ```
   `ls -l /proc/$PID/exe` showing `(deleted)` = still the old binary.
6. Confirm mode survived the restart:
   `journalctl --user -u temporal-maintenance-worker.service -n5` — expect
   `ARMED (RealAdapter)` (armed drop-in present) or the deliberate shadow line.
7. Watch the next even-hour fire complete:
   `temporal workflow list --namespace maintenance --limit 3` → newest cycle
   `Completed`; on the armed path, review+author beads created and slung.

## 4. Change-control hook: files that trigger this gate

Any diff touching these paths under `services/temporal-maintenance/` gets the
§1 verdict recorded in the commit body and the replay gate run:

| File | Trigger |
|---|---|
| `workflow.go` | Always. |
| `state.go` | Always (history/query payload compatibility). |
| `idempotency.go` | Always — `idempotencyKey` is called from workflow code; a derivation change silently re-keys at-most-once dedup across retries/cycles even though replay stays green. |
| `activities.go`, `worker/main.go` | Only when activity **names/signatures** or the **registration set** change (activity type is matched by name during replay). Body-only edits: exempt. |
| `go.mod` | When `go.temporal.io/sdk` or `go.temporal.io/api` moves. |

Not gate-triggering (activity-internal / out-of-band): `real_adapter.go`,
`maintenance_runner.go`, `execstore.go`, `escalation.go`, `bridge_client.go`,
`cishim.go`, `reconciler.go`, `signaler.go`, `cmd/*`, `prompts/*`,
`deploy/*` (deploy files follow CUTOVER.md and normal change-control instead).

Never delete or regenerate the `testdata/*_history.json` fixtures to make the
gate pass — a red replay gate is the gate working. Re-capture is legitimate
only AFTER a deliberate, versioned definition change, from a post-change
execution, noted in the commit.

## 5. Track D preconditions (specified now, built with Track D)

Track D execution is blocked until the gc-372 clean-week soak closes
(~07-23). When it unblocks, these are preconditions to arming any
long-running workflow — mechanical checks land WITH that work, not before:

1. Worker registers a deployment version: `DeploymentOptions{UseVersioning:
   true, Version: {DeploymentName: "temporal-maintenance", BuildID: <git rev
   baked at build via -ldflags>}}`; workflows registered with explicit
   `VersioningBehaviorPinned`. Ship a test asserting the worker's BuildID is
   non-empty and equals the build-stamped rev (fails if someone deploys an
   unversioned worker onto a long-running task queue).
2. A pinned replay fixture for every long-running path, captured before
   arming (`temporal workflow show --output json`, read-only).
3. Deploy flow for that queue switches from §3 restart-in-place to
   `set-current-version` + drain (two worker units during drain).
