# temporal-maintenance (Run provider)

Temporal-backed Run provider for the upstream-maintainer maintenance cycle —
the option-1 promotion of the `dr-2uh` shadow pilot. Standalone Go module;
**not** part of any Gas City build, **not** imported by the supervisor,
`city.toml`, or any order. The worker is launched only by its own
`systemd --user` unit (Phase 4). Promotion plan:
`docs/design/temporal-maintenance-promotion-plan.md`; pilot design:
`docs/design/temporal-maintenance-run-pilot.md`.

**Changing a deployed Workflow definition is gated**: any edit to
`workflow.go` / `state.go` / `idempotency.go` (and activity names or
registrations) must pass the replay gate (`go test -run TestReplay .`) and
record a versioning verdict per
`docs/conventions/temporal-versioning.md` (gc-4zf.9) before deploy.

The bound default is still `DryRunAdapter`: every external mutation is
**recorded, never executed**, so `gh pr create/review/merge`, `git push`, and
Slack posts are captured as `ProposedMutation`s instead of run. `RealAdapter`
(in `real_adapter.go`) implements the same interface but is **unregistered and
fail-closed** — Phase 2 wires its persisted idempotency store and
approval-gated execution before it can ever be bound.

## Layout

| File | Contents |
|------|----------|
| `state.go` | Typed `MaintenanceCycleState`, `Phase`, `Verdict`, `Decision`, branch state |
| `idempotency.go` | `ProposedMutation`, `SideEffectAdapter`, `DryRunAdapter` (find-or-create by key), key derivation |
| `execstore.go` | `KeyStore` — crash-safe, at-most-once **persisted** side-effect ledger (one file per key, atomic exclusive-link claim) |
| `maintenance_runner.go` | `CommandRunner` + `ExecRunner` — real, allowlisted `gc`/`gc-sling`/`gh`/`git` execution; selection = create+sling |
| `real_adapter.go` | `RealAdapter` — armed (persisted at-most-once) or unarmed (fail-closed `ErrRealAdapterUnarmed`) |
| `activities.go` | `Activities` (`DispatchSelection`, `ProposeExternalAction`) |
| `naming.go` | Forward-looking durable idempotency-key + Workflow-ID constructors (convention: `docs/design/durable-naming-convention.md`; legacy formats pinned by `TestLegacyFormatsUnchanged`) |
| `observe.go` | `ObserveBridge` — read-only observe-mode metrics (gc-4zf.7); one-shot runner in `cmd/temporal-observe` |
| `workflow.go` | `MaintenanceCycleWorkflow` + Signals, `state` Query, `humanDecision` Update |
| `worker/main.go` | Worker entrypoint for the local dev server (binds `DryRunAdapter`) |
| `*_test.go` | In-process + dev-server testsuite coverage |

## Adapters

- **`DryRunAdapter`** (bound default) — records every mutation in memory, executes nothing.
- **`RealAdapter`** — executes each mutation **exactly once**, keyed on the idempotency key against the persisted `KeyStore`, so an Activity retry or a worker restart mid-execution never double-fires. Unarmed by default (fails closed); `NewArmedRealAdapter(store, runner)` binds real execution. It is still **unregistered**: the worker binds `DryRunAdapter`; the armed adapter is exercised only by tests and the staging path. The human-approval gate lives in the Workflow — `ProposeExternalAction` runs only after an approve Update — so `RealAdapter` executes whatever it is handed, once.

The `staging_test.go` guard exercises the real `ExecRunner` against the live bead store (creates one throwaway bead, proves a duplicate `Propose` creates no second bead, reports the id to close). It is gated behind `TEMPORAL_MAINT_STAGING=1` so normal CI never mutates the store:

```bash
TEMPORAL_MAINT_STAGING=1 GC_CITY_DIR=/home/ds/gas-city go test -run TestStaging_RealExecRunner -v .
```

## Run the in-process tests (no server needed)

```bash
cd services/temporal-maintenance
GOFLAGS=-mod=mod go test ./...
```

These cover the happy path, the gated approve/reject paths, human-decision Update
validation, the reprompt loop, idempotency (adapter dedup + duplicate signals +
duplicate approve), and durable wait-boundary state.

## Run against the local dev server (requires the temporal CLI)

The dev-server integration test, the replay test against a **real** captured
history, and forced-worker-restart resume all need the Temporal CLI.

`integration_test.go` covers the forced-restart resume automatically: it starts a
real dev server via the SDK's `testsuite.StartDevServer` (using the `temporal`
binary on `PATH`), drives the cycle to the human gate, stops the worker, and
resumes with a fresh worker — asserting the gated mutation runs exactly once. It
**skips** cleanly when the CLI is absent. Run it (and everything else) with:

```bash
cd services/temporal-maintenance
GOFLAGS=-mod=mod go test -race ./...     # 16 pass, 0 skip when the CLI is installed
```

Install the CLI once. `go install github.com/temporalio/cli/cmd/temporal@latest`
currently fails because the CLI module carries `replace` directives; use the
release binary instead:

```bash
# grab the latest linux/amd64 release tarball and install onto PATH
url=$(curl -sSL https://api.github.com/repos/temporalio/cli/releases/latest \
  | grep -o 'https://[^"]*temporal_cli_[^"]*_linux_amd64.tar.gz' | head -1)
curl -sSL "$url" | tar xz temporal && install -m0755 temporal "$(go env GOPATH)/bin/temporal"
```

The manual drive-through below is how `testdata/maintenance_cycle_history.json`
was captured (used by the replay test):

```bash
# terminal 1 — start the local dev server
temporal server start-dev

# terminal 2 — run the shadow worker
cd services/temporal-maintenance && go run ./worker

# terminal 3 — start one cycle and drive it with signals/updates
temporal workflow start \
  --task-queue temporal-maintenance-shadow \
  --type MaintenanceCycleWorkflow \
  --workflow-id gascity-maintenance/gastownhall-gascity/$(date +%Y-%m-%dT%H) \
  --input '{"repo":"gastownhall-gascity","cycle_key":"demo","require_human_gate":true,"gated_action":"gh pr merge","gated_target":"1712"}'

temporal workflow signal --workflow-id <id> --name ci.completed     --input '{"branch":"author","verdict":"pass"}'
temporal workflow signal --workflow-id <id> --name review.completed --input '{"branch":"review","verdict":"pass"}'
temporal workflow query  --workflow-id <id> --type state
temporal workflow update execute --workflow-id <id> --name humanDecision --input '{"decision":"approve","approver":"stephanie"}'

# capture the completed history for the replay-safety gate
temporal workflow show --workflow-id <id> --output json \
  > testdata/maintenance_cycle_history.json
# then `go test -run TestReplay` replays it against the current code
```

## Signal bridge (Phase 3)

The bridge turns real-world maintenance events into the typed Signals the
workflow waits on — the production path that replaces the tests' direct
`SignalWorkflow` calls.

| Component | Role |
|-----------|------|
| `signaler.go` | `Signaler` — typed `BeadClosed` / `CICompleted` / `ReviewCompleted` / `Escalation`; signal names + payloads live here once |
| `cishim.go` | `CIReviewShim` — **interim** verdict→Signal relay (one signal per branch, not a lifecycle poller); replaced by GitHub webhooks at P5 |
| `reconciler.go` | `Reconciler` — re-sends a boundary signal the workflow missed but that truth says occurred (signal path is at-least-once by re-drive; the mutation path stays at-most-once) |
| `bridge_client.go` | `ClientStateReader` (Query adapter) + `GHStatusFetcher` (gh REST → typed Verdict) |
| `cmd/temporal-signal` | exec behind the bead.closed / bead.created event orders |
| `cmd/maintenance-reconcile` | reconcile one cycle from gh ground truth |

Event orders live in `orders/temporal-maintenance-signal-*.toml.disabled` with
execs `bin/temporal-maintenance-signal-*`. They are **disarmed** (`.disabled`)
until Phase 4 deploys Temporal, and they read a metadata contract on the cycle's
beads (`gc.metadata.temporal.repo` / `.cycle_key` / `.branch`) that **Phase 4's
real `DispatchSelection` populates** — so today they are correct no-ops. Build the
binaries with `make build`.

Proven end-to-end against a dev server: `TestIntegration_Bridge_SignalerDrivesWorkflow`
drives a cycle to its gate purely through the `Signaler`, and
`TestIntegration_Bridge_ReconcilerRepairsDroppedEvent` drops a review signal, then
the `Reconciler` repairs it from ground truth and the cycle advances.

**Deferred to P4/P5:** the reconcile *sweep* order (needs the deployed schedule +
running-cycle discovery; arm it with a `Skip`-overlap policy so two reconciles
never race the same cycle), and the GitHub webhook intake that retires the interim
`CIReviewShim` (there is no local webhook intake today — verdicts arrive only via
gh REST).

**Known bounded gaps** (documented, non-gating): the event execs scan a bounded
`--closed-after`/`--created-after` window (`TEMPORAL_MAINT_SIGNAL_SINCE_MIN`, 10m),
so a bead-closed/escalation event that falls entirely outside a long order outage
is not caught up — the reconciler re-derives CI/review truth, not bead-closure, so
this is a loss of bookkeeping signal, not a stuck cycle. The exec state file is not
locked across overlapping invocations; a lost write only causes a duplicate,
workflow-idempotent no-op signal.

## Deploy (Phase 4 — self-hosted systemd --user)

Two `systemd --user` units on ds-5090, **outside** the gascity-supervisor failure
domain (own cgroups, `WantedBy=default.target`, linger-enabled — never a
supervisor drop-in). Canonical unit files live in `deploy/`.

```bash
make install          # build binaries + copy units + enable/start both
# server: temporal server start-dev, persistent sqlite, gRPC 127.0.0.1:7233, UI 8233, ns=maintenance
# worker: binds DryRunAdapter (records mutations, executes nothing) — SHADOW mode
```

Verify: `systemctl --user status temporal-server.service temporal-maintenance-worker.service`.
Independence is a promotion invariant — a `systemctl --user restart gascity-supervisor`
leaves both untouched, and the persisted workflow survives (verified at deploy).

Teardown:
```bash
systemctl --user disable --now temporal-maintenance-worker.service temporal-server.service
rm ~/.config/systemd/user/temporal-{server,maintenance-worker}.service
systemctl --user daemon-reload   # sqlite store: ~/.local/share/temporal-maintenance/
```

**Not yet armed:** the 120m Schedule is deliberately NOT installed. A shadow
Schedule with the DryRun worker would start cycles that park in `awaiting-events`
forever (no real PR is selected, so the CI/review shim has nothing to watch) and
`Skip`-overlap would then block every later fire. Meaningful shadow cycles need
real selection dispatch (the deferred `DispatchSelection` wiring) — which is a
live-dispatch decision, so schedule-arming stops at the P4 gate for Stephanie.

## Safety boundary

Read-only `gc`/`gh` are allowed. Mutating actions are recorded with an
idempotency key and a `temporal-shadow` marking. Nothing here arms a Schedule,
registers a service, edits `city.toml`, restarts the supervisor, or touches
GitHub/git/Slack.
