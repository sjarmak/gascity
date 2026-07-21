# Temporal-backed maintenance Run — promotion plan (option 1)

**Status:** approved (Stephanie, 2026-07-15) — build the optional Temporal-backed Run provider
**Owner:** city-infra-pl
**Predecessors:** `docs/design/temporal-maintenance-run-pilot.md` (design), `docs/experiments/temporal-maintenance-pilot-2026-07-14.md` (shadow pilot + forced-restart evidence)
**Bead:** dr-2uh (pilot) → this plan's phase beads block on it

## Decision record

- **Direction:** option 1 of the pilot report — expose an *optional* Temporal-backed Run provider for the maintenance cycle (and later periodic audits), **not** a city-wide substrate. (Stephanie, 2026-07-15.)
- **Deployment target:** **self-hosted `systemd --user` service on ds-5090**, outside the `gascity-supervisor` failure domain — own cgroup, `WantedBy=default.target`, NOT a supervisor drop-in. On-box, zero data egress, no SaaS bill; operator owns persistence/backup/upgrade/retention. (Stephanie, 2026-07-15.) Mirror to the decisions rig when the store is reachable.

## Invariants that gate every phase

1. Temporal never enters the supervisor failure domain. High-frequency health/reaper/account-guard/disk-pressure orders, tmux, supervisor recovery, and bead-state queries must all keep working when Temporal is down.
2. Every external mutation (gh/git/gc/Slack) stays behind the validated `humanDecision` Update, keyed by a workflow-computed idempotency key, find-or-create — a worker crash never double-executes.
3. Workflow histories carry only IDs/phases/verdicts/timestamps/artifact-refs — never prompts/diffs/transcripts/logs/secrets. (Verified clean on the captured history.)
4. Promotion is worthwhile only if it **deletes** machinery. A phase that wraps legacy coordination without retiring any is a failed phase.
5. Nothing arms on live maintenance work until Phase 4's shadow-parallel comparison is clean; the legacy exec keeps running until Phase 5's cutover gate passes.

## Retirement targets (what a completed promotion deletes)

| Target | Where | Retired by |
|---|---|---|
| `bin/maintenance-cycle` lifecycle — `create_and_sling` (`:124-156`), `build_loop_close_metadata` (`:109-119`), `open_half_inflight` (`:97-103`), `log_event` (`:83-89`) | `bin/maintenance-cycle` (293 ln) | Workflow state + Activities (P5) |
| `order-run:` open-work-guard workaround (exec wrapper instead of native formula+pool) | `bin/maintenance-cycle:26-30` | Workflow `Skip` overlap policy + stable Workflow ID (P4) |
| `orders/maintenance-cycle.toml` (120m cooldown exec) | order file | Temporal Schedule (P4→P5) |
| `bin/pr-state-poller` (243 ln, 15m poll of @me PRs) + `orders/pr-state-poller.toml` | order + exec | Webhook/event Signal bridge + narrow reconciler (P5) |
| `log_event` audit plumbing (`.gc/*.log`) | both execs | Temporal event history (P5) |

The `REVIEW_BODY`/`AUTHOR_BODY` polecat prompts (`bin/maintenance-cycle:160-257`) are *payloads*, not lifecycle — they move verbatim into the selection-dispatch Activity, not deleted.

## Phases (each = one bead, blocked-by the prior)

### Phase 1 — Provider skeleton, disarmed, dry-run default (internal, zero behavior change)
- Relocate `experiments/temporal-maintenance/` → `services/temporal-maintenance/` (module path `github.com/sjarmak/gas-city/services/temporal-maintenance`); drop "experiment" framing. Nothing else in the workspace references it, so this is a mechanical move + import-path bump.
- Add a `RealAdapter` type implementing `SideEffectAdapter`, **unregistered** — `DryRunAdapter` stays the bound default.
- **Gate:** full suite green incl. `TestIntegration_ForcedWorkerRestart` (`-race`, `go vet`, `gofmt`); grep proves no `orders/*.toml`, `city.toml`, `bin/*`, or systemd unit references the module; `bin/maintenance-cycle` + `bin/pr-state-poller` byte-identical.

### Phase 2 — Real side-effect adapter behind the human gate (staged, no live GitHub mutation)
- `RealAdapter.Propose`: real `gc bd create` + `gc-sling <polecat> --nudge` for selection (mirrors `create_and_sling`); real gated action (`gh pr merge`, etc.) only after an approve Update. Find-or-create against a **persisted** idempotency-key store on disk so a restart mid-`Propose` never double-fires.
- **Gate:** run against a `staging/*` cycle-key targeting throwaway test beads + a scratch PR only; extend the integration test with a real-adapter double-call harness asserting single execution across a forced restart; every mutation still requires the human Update.

### Phase 3 — Signal bridge (existing bead event bus + interim CI/review shim)
- `bead.closed` ← `.gc/events.jsonl` via an `on = "bead.closed"` order calling a small `temporal-signal` exec (reuse the `wake-mayor-on-slung-close` mechanism). `agent.escalation` ← the `pl-human-gate-surface` (`on = "bead.created"`) analog.
- `ci.completed` / `review.completed`: **interim** thin gh-REST→Signal shim (verdict→Signal only; NOT a lifecycle poller — far smaller than `pr-state-poller`), explicitly flagged for webhook replacement in P5. No local webhook intake exists today, so full webhookization is deferred, not blocking.
- Add the narrow lost-boundary reconciler.
- **Gate:** the workflow is driven end-to-end by real Signals in staging; reconciler repairs a deliberately dropped boundary event.

### Phase 4 — Deploy service + worker (systemd --user) and arm a Schedule SHADOW-PARALLEL
- Temporal server as a `systemd --user` unit modeled on `gas-city-dashboard.service` (`Type=simple`, explicit `Environment=PATH`, `Restart=always`, `WantedBy=default.target`, own cgroup, sqlite/postgres persistence). Worker as a sibling `Type=simple` unit binding `RealAdapter`. Neither is a `gascity-supervisor` drop-in.
- Temporal Schedule (`Skip` overlap) arms `MaintenanceCycleWorkflow` at the 120m cadence, running **in parallel** with the still-live `orders/maintenance-cycle.toml`; Temporal's mutations stay gated; compare selections/decisions across N cycles.
- **Gate:** ≥N parallel cycles agree with the legacy exec; Temporal survives a `systemctl --user restart gascity-supervisor` (proves failure-domain independence) and a box reboot; approve/reject reachable from the morning-ledger/#gascity-maintenance path.

### Phase 5 — Cutover: flip to Temporal, delete legacy, webhookize Signals
- Remove/pause `orders/maintenance-cycle.toml`; the Schedule is sole driver. **Delete** `bin/maintenance-cycle`'s lifecycle functions (prompts move to the dispatch Activity).
- Stand up GitHub webhook intake (net-new), replace the interim CI/review shim, retire `bin/pr-state-poller` + its order (migrate the copilot-iterate loop onto the same Signal path).
- **Gate:** legacy files deleted, net-negative LOC, one clean week of Temporal-only operation, documented rollback (re-arm the paused order + rebind DryRunAdapter).

## Dispatch

city-infra-pl owns execution, one phase bead at a time, each blocked-by the prior. Phases 1–3 are internal/staged (autonomous between sub-steps). Phase 4's systemd install and Phase 5's cutover + webhook intake touch live infra and external surfaces — those stop at their gates for Stephanie per the autonomy boundary. No `git push`/PR without per-action approval (this is city-infra in `gas-city`, not a pre-authorized rig-code push).
