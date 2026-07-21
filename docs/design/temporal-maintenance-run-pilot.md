# Temporal-backed maintenance Run pilot

**Status:** proposed  
**Date:** 2026-07-14  
**Owner:** city-infra-pl  
**Scope:** `/home/ds/gas-city`; no upstream Gas City dependency or production cutover in the pilot

The city currently carries 103 order definitions, all of them exec orders. The
Gas City order implementation and its tests span 7,979 lines, while the July 14
order-firing incident showed that a 168 MB file-backed bead store could stall
the dispatcher by forcing full-file reloads under its write lock. This does not
make Temporal a replacement for the store, but it exposes a more useful split:
Gas City should own agent work, while a durable procedure engine can own the
long-lived coordination around that work.

The pilot applies [Temporal](https://github.com/temporalio/temporal) to one
procedure: the upstream `maintenance-cycle` that reviews the highest-priority
contributor PR and authors work for the highest-priority suitable issue. It is
also an executable test of the persistent `Run` proposed by
[Orchestration v3](https://github.com/gastownhall/gascity/issues/1709), without
making Temporal a required Gas City substrate.

## Temporal coordinates procedures; Gas City remains the work system

Gas City's current architecture treats beads as the universal domain
persistence substrate, the event bus as the observation substrate, and the
controller as the driver of infrastructure operations. Those boundaries remain
intact. A Temporal Workflow records the execution state of a maintenance
procedure, but it does not become the source of truth for beads, sessions,
convoys, artifacts, or agent results.

```text
Temporal Schedule / GitHub webhook / human decision
                         |
                         v
             Temporal Workflow (durable Run)
                 |                    ^
        short Activities              | Signals / Updates
                 v                    |
       Gas City beads, sessions, and event bus
```

The ownership rule is simple: beads answer *what work exists and what happened
to its artifacts*; Temporal answers *where this procedure is, what it is waiting
for, and which transition comes next*. Every workflow records the bead IDs it
touches, and every affected bead receives the Temporal Workflow ID in metadata,
so either observability surface can lead to the other.

Temporal Workflows are deterministic code whose event history recreates state
after worker failure. External calls, filesystem operations, LLM invocations,
and subprocesses therefore belong in Activities, not Workflow code. Completed
Activity results are recorded for replay, but an Activity may execute more than
once if its worker fails before reporting completion; every mutating Activity
needs an idempotency key and a find-or-create path. See Temporal's
[Workflow](https://docs.temporal.io/workflows) and
[Activity](https://docs.temporal.io/activities) documentation.

## The maintenance cycle is the right first Run

`bin/maintenance-cycle` already contains the shape Temporal is built to hold:
scheduled initiation, two independent branches, structural deduplication,
agent dispatch, waits on external state, retries, an audit trail, and a durable
human gate before approval or merge. The script also documents two active
workarounds: native formula-plus-pool orders are bypassed because an
`order-run:` marker trips the open-work guard, and v2 workflow instantiation is
dark in the current control-dispatcher path.

The pilot replaces none of those live paths. It runs the same lifecycle in
shadow mode, using a dry-run side-effect adapter and explicit test signals, so
the city-infra PL can test the coordination model without changing GitHub,
arming a schedule, restarting the supervisor, or taking ownership away from the
existing order.

The proposed workflow has two levels:

1. `MaintenanceCycleWorkflow` represents one scheduled cycle. Its stable ID is
   `gascity-maintenance/gastownhall-gascity/<cycle-key>`, and its overlap policy
   is `Skip`, so a still-open cycle suppresses another start.
2. `PRLifecycleWorkflow` and `IssueLifecycleWorkflow` represent independently
   durable units only when lifecycle isolation is useful. Code organization
   alone is not a reason to create a child Workflow; a normal Activity is the
   default.

The maintenance workflow performs the following state transitions:

```text
scheduled
  -> dispatch selection bead
  -> wait for structured selection result
  -> fan out review and author branches
  -> wait for bead, CI, and review events
  -> request human decision when an external mutation is gated
  -> execute an idempotent authorized action
  -> record artifact references and finish
```

Semantic selection stays with the polecat. The workflow validates the returned
schema and moves between states; it never ranks PRs, classifies issue meaning,
or embeds policy keywords in Go. That preserves the city's Zero Framework
Cognition boundary.

## Messages replace coordination polling

A small bridge converts Gas City and GitHub events into Temporal messages. Bead
closure, CI completion, review completion, and agent escalation are asynchronous
Signals. Dashboard reads are Queries. Human approval, rejection, skip, abort,
and re-prompt are Updates because the caller needs validation and an acknowledged
result. Temporal defines these exact distinctions in its
[message-passing model](https://docs.temporal.io/encyclopedia/workflow-message-passing).

The pilot may inject those messages through a local command or test harness,
but the production design does not add another periodic poller. Promotion
requires a bridge from the Gas City event bus and GitHub webhooks, with a narrow
fallback reconciler only for lost-boundary-event repair.

Workflow state contains small typed values: IDs, hashes, phase names, verdicts,
timestamps, and artifact references. Prompts, transcripts, command output,
diffs, test logs, and review bodies stay in their existing stores. Long-lived or
high-message workflows use
[Continue-As-New](https://docs.temporal.io/develop/go/workflows/continue-as-new)
before event history approaches platform limits.

## Schedules replace only scheduling that needs durable semantics

Temporal Schedules provide overlap policy, catch-up windows, pause-on-failure,
backfill, and operator notes. Those semantics fit maintenance cycles and
periodic audits whose executions can span restarts or await people. See the
[Schedule documentation](https://docs.temporal.io/schedule).

The other city orders stay where they are during and after the pilot unless a
separate review shows that moving one removes more machinery than it adds.
High-frequency health checks, local reapers, account guards, disk-pressure
checks, and supervisor recovery must continue functioning when Temporal is
unavailable. Temporal must never become a prerequisite for starting tmux,
recovering the supervisor, or querying bead state.

Task Queue priority and fairness do not automatically replace polecat capacity
allocation. In the pilot, Temporal queues schedule only bridge Activities;
Gas City still schedules agent sessions after `gc sling`. Temporal's
[priority and fairness](https://docs.temporal.io/develop/task-queue-priority-fairness)
become relevant only if agent execution itself later moves behind Temporal
workers.

## The pilot proves the Run contract before changing upstream architecture

ADR-0005 rejected an external orchestrator as the implementation of typed
formula gates. That rejection remains correct: requiring Temporal to evaluate a
small declarative gate would add infrastructure without modeling the agent
reasoning lifecycle. Orchestration v3 asks a larger question, however. Its Run
must be persistent, restartable, queryable, pausable for human input, mutable by
operators, and observable as one execution across sessions and beads.

The pilot treats Temporal as an executable reference implementation for that
contract. After it runs, upstream Gas City can choose among three evidence-based
directions: reproduce the proven semantics over beads, expose an optional
Temporal-backed Run provider, or reject the model because dual-state operation
costs more than the failure classes it removes. No option is selected in
advance.

Worker code changes introduce a replay constraint that ordinary Go services do
not have. The pilot therefore includes history replay tests and a versioning
plan; a later production deployment would use
[Worker Versioning](https://docs.temporal.io/production-deployment/worker-deployments/worker-versioning)
or an equivalent replay-safe rollout rather than replacing workflow code in
place.

## Shadow mode has a hard safety boundary

The first implementation lives under
`/home/ds/gas-city/experiments/temporal-maintenance/`. It uses the current
Temporal Go SDK pinned in its own module and the CLI development server started
manually. It is not registered in `city.toml`, managed by the Gas City
supervisor, or armed through an order.

The dry-run adapter implements the full Activity contract but records proposed
mutations instead of executing `gh pr create`, `gh pr review`, `gh pr merge`,
`git push`, Slack posts, or other external actions. Read-only `gc` and `gh`
operations are allowed. Internal test beads must carry an unmistakable
`temporal-shadow` label and a stable idempotency key; the pilot must cleanly
identify them for later removal without deleting unrelated work.

Temporal's local development server is appropriate for this phase. Production
promotion must place the Temporal service outside Gas City's supervisor and
failure domain, whether managed or separately self-hosted. The
[self-hosted guide](https://docs.temporal.io/self-hosted-guide) makes the
production obligations explicit: persistence, security, monitoring, schema
upgrades, retention, and recovery become operator responsibilities.

## Promotion requires evidence, not a successful demo

The pilot is complete only when all of these conditions hold:

- `MaintenanceCycleWorkflow` and its Activities have unit tests, Temporal
  workflow tests, and an integration test against the local development server.
- Forced worker termination at each wait boundary resumes the same Workflow ID
  without losing state or repeating a recorded mutation.
- Duplicate schedule starts, Signals, Updates, and Activity retries do not
  create duplicate beads or duplicate proposed external actions.
- A human-decision Update survives worker restart, validates the approver field,
  and records an acknowledged approve or reject result.
- Workflow Queries and Search Attributes expose repo, cycle key, bead IDs,
  current phase, `needs_human`, and terminal outcome without storing business
  state in Search Attributes.
- Workflow histories contain no prompt, transcript, diff, test-log, secret, or
  credential payloads.
- Replay tests pass against a captured completed history after a workflow-code
  change.
- The pilot report names the existing script, poller, tracking-bead logic, and
  audit-log code that production promotion would delete. Wrapping all existing
  coordination without retiring any of it is a failed result.
- Existing `maintenance-cycle` behavior and all current orders remain unchanged
  until a separately approved cutover.

The deliverable is a working shadow run plus a dated report under
`docs/experiments/`. That report should make the next decision narrower: promote
one real lifecycle, use the Run semantics without Temporal, or remove the
experiment before it becomes permanent parallel infrastructure.
