# Temporal maintenance Run pilot — shadow build report

**Date:** 2026-07-14
**Author:** city-infra-pl
**Bead:** dr-2uh
**Design:** `docs/design/temporal-maintenance-run-pilot.md`
**Code:** `experiments/temporal-maintenance/` (standalone Go module, not wired into any build)

## What was built

A shadow-mode implementation of the maintenance cycle as a durable Temporal Run,
touching nothing live. The module models the design note's full state machine and
message contract:

- **Typed state** (`state.go`): `MaintenanceCycleState` carries only IDs, phases,
  verdicts, timestamps, and artifact refs — no prompts/diffs/logs/secrets. Phase
  enum matches the design's transition order (`scheduled → selecting → fanout →
  awaiting-events → awaiting-human-decision → executing → done`, plus `aborted`).
- **Deterministic workflow** (`workflow.go`): `MaintenanceCycleWorkflow` with a
  stable ID `gascity-maintenance/<repo>/<cycle-key>`, four Signals
  (`bead.closed`, `ci.completed`, `review.completed`, `agent.escalation`), a
  `state` Query, and a validated `humanDecision` Update. Iterates a fixed branch
  slice (never a map) so replay is deterministic; all IO is in Activities.
- **Dry-run adapter** (`idempotency.go`, `activities.go`): `SideEffectAdapter`
  with a find-or-create `DryRunAdapter` keyed on a workflow-computed idempotency
  key. Every external mutation (`gh pr merge`, etc.) is recorded, never executed.
- **Worker entrypoint** (`worker/main.go`) for the local dev server.
- **ZFC boundary preserved**: the workflow validates the selection schema and
  moves between states; it never ranks PRs, classifies issues, or embeds policy
  keywords. Semantic selection stays with the polecat.

## Verification (executed)

`GOFLAGS=-mod=mod go test -race ./...` against Temporal Go SDK v1.46.0 and
Temporal CLI v1.8.0 (bundled server 1.31.2) — **16 pass, 0 skip**, `go vet`
clean:

| Acceptance criterion | Coverage | Status |
|---|---|---|
| Workflow, typed state, Queries, Signals, validated human Update, stable IDs | `workflow_test.go`, `restart_test.go` (`TestWorkflowID_Stable`) | ✅ in-process |
| Dry-run Activity adapters with stable idempotency keys | `idempotency_test.go` (`TestAdapter_DedupsByKey`, `TestAdapter_RejectsMissingKey`) | ✅ |
| Human-decision Update validated (approver, decision set, gate open) + acknowledged | `update_test.go` (invalid / missing-approver / gate-closed), `TestReprompt_ThenApprove` | ✅ |
| Duplicate delivery / retries → no duplicate beads or proposed actions | `idempotency_test.go` (`TestDuplicateSignals_NoDuplicateBeads`, `TestDuplicateApprove_SingleExternalAction`) | ✅ |
| No business payloads in workflow state | typed state by construction; asserted via query in tests | ✅ |
| Does not arm a schedule / register a service / edit city.toml / restart supervisor / mutate GitHub-git-Slack | standalone module + dry-run adapter | ✅ |
| Workflow/replay tests + integration against local dev server | `integration_test.go` (real dev server via `testsuite.StartDevServer`); `replay_test.go` replays a captured completed history | ✅ dev-server-proven |
| Replay against a real captured history after workflow-code change | `replay_test.go` replays `testdata/maintenance_cycle_history.json` (36 events, captured 2026-07-15) | ✅ |
| Forced worker termination at the wait boundary resumes without state loss / repeated mutation | `integration_test.go` (`TestIntegration_ForcedWorkerRestart_ResumesSingleMutation`) + in-process `TestWaitBoundary_StateDurable` | ✅ |

### The dev-server-gated gap — now closed (2026-07-15)

The three previously gated criteria — the local-dev-server **integration** test,
**replay against a real captured history**, and **forced-worker-restart
resume** — are proven. The Temporal CLI v1.8.0 was installed
(`go install` is blocked by replace directives in the CLI module, so the release
binary `temporal_cli_1.8.0_linux_amd64.tar.gz` was used) and the box was under
manageable memory pressure (~30 GiB free, 4.4/8 GiB swap), not the 100% swap of
the build night.

Two forms of evidence, both executable and reproducible on any host with the CLI:

1. **Live drive-through** (manual, captured the history): started the shadow
   worker against `temporal server start-dev`, drove one cycle to the human gate,
   **`kill -9`'d the worker while parked**, confirmed the workflow stayed
   `RUNNING` server-side with no pollers, restarted a fresh worker, and it
   recovered the exact parked state (2 selection beads, gate open, zero
   mutations). Approving then completed the cycle with **exactly one** gated
   mutation; a duplicate approve was rejected (`workflow execution already
   completed`). The completed history is saved at
   `testdata/maintenance_cycle_history.json`.
2. **Automated integration test** (`integration_test.go`) makes the same
   forced-restart reproducible in `go test`: it starts a real dev server via the
   SDK `testsuite.StartDevServer` (using the installed CLI), drives worker1 to the
   gate, stops it, and resumes with **worker2 bound to a fresh empty adapter**.
   Because worker2's adapter never sees the selection dispatches (they replay from
   history rather than re-execute) and records only the post-approval merge, a
   single assertion — `len(adapter2.Recorded()) == 1` — proves both "resumed from
   history, not worker memory" and "the gated mutation ran exactly once across the
   restart". It skips cleanly on hosts without the CLI.

## What a promotion would retire

The pilot is only worthwhile if promotion **removes** machinery, not wraps it. The
concrete coordination code a real Temporal-backed maintenance Run would delete or
shrink:

- **`bin/maintenance-cycle`** (14.3 KB exec-order script) — its hand-rolled
  lifecycle becomes the Workflow. Specifically:
  - `create_and_sling()` + `build_loop_close_metadata()` — tracking-bead creation
    and loop-close metadata stamping → replaced by Workflow state + Activities.
  - `open_half_inflight()` — the structural-dedup / open-work-guard workaround
    (the `order-run:` marker that trips `hasOpenWorkStrict`, PR #1986) → replaced
    by the Workflow's `Skip` overlap policy and stable Workflow ID.
  - `log_event()` audit-trail plumbing → subsumed by Temporal event history
    (with the payload-hygiene rule keeping bodies out of history).
  - the morning-ledger escalation path for approve/merge → the `humanDecision`
    Update (validated, acknowledged, survives restart).
- **`pr-state-poller`** (the fast 15 m polling loop `maintenance-cycle`
  complements) → a promotion replaces polling with event-bus + webhook Signals
  plus a narrow lost-boundary reconciler, per the design.
- The **dark v2-workflow formula instantiation** in the control-dispatcher path
  (currently bypassed) → no longer needed for this procedure.

Wrapping all of the above without deleting any of it would be a **failed** result,
per the acceptance criteria.

## Recommendation

**The pivotal evidence is now in: a worker `kill -9`'d mid-wait resumes the exact
Run and completes with a single external mutation** (both a live drive-through and
the automated `integration_test.go`). The Run contract — durable, queryable,
pausable-for-human, idempotent, replay-deterministic — is proven cleanly
implementable, and the pilot names the real machinery a promotion would retire
(`bin/maintenance-cycle`'s lifecycle, `pr-state-poller`, the dark v2-workflow
instantiation). All eight promotion conditions from the design note now hold.

Recommended call — **option 1: expose an optional Temporal-backed Run provider**
for exactly the procedures that span restarts or await humans (maintenance cycle,
periodic audits), not a city-wide substrate. The forced-restart evidence is the
specific thing that makes this worth the dual-state cost: it removes a failure
class (worker crash mid-human-gate → lost cycle or double-merge) that bead-state
alone models only with non-trivial reconciler code. Hard constraints carried
forward from the design:

- Temporal stays **outside the supervisor failure domain**. High-frequency
  health/reaper/account-guard/disk-pressure orders and supervisor recovery must
  keep working when Temporal is down. Temporal is never a prerequisite for tmux,
  supervisor recovery, or bead-state queries.
- Promotion is worthwhile **only if it deletes machinery**, not wraps it. The
  cutover PR must remove `bin/maintenance-cycle`'s hand-rolled lifecycle and the
  `pr-state-poller` coordination loop, not run them in parallel.
- Payload hygiene stays enforced: the captured history was scanned and contains
  **zero** prompt/diff/transcript/secret/token payloads.

The two alternatives (2: reproduce Run semantics over beads; 3: remove the
experiment) are now the weaker options — they were live only until the
forced-restart class was shown to be real and cleanly handled, which it is. This
is a decision for Stephanie (new parallel infrastructure with an operator
burden), not an autonomous cutover.

## Status of dr-2uh

The build + evidence work is complete; all three previously dev-server-gated rows
are green. What remains is a **decision**, not more engineering:

1. ✅ Temporal CLI installed (v1.8.0, release binary — `go install` is blocked by
   the CLI module's replace directives).
2. ✅ Drove one cycle against `temporal server start-dev`; captured
   `testdata/maintenance_cycle_history.json`.
3. ✅ Replay test replays the real history; `integration_test.go` kills the worker
   at the human-gate boundary and asserts single-mutation resume. Full suite: 16
   pass, 0 skip, `-race` and `go vet` clean.
4. ⏳ **Stephanie's call** on promote (option 1) vs reproduce-over-beads (option 2)
   vs remove (option 3). No cutover happens without it — a cutover adds operator-
   owned infrastructure (persistence, security, monitoring, retention, recovery)
   outside the supervisor failure domain.
