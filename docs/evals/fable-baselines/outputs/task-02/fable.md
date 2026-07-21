# Plan: issue #3972 — session event delivery is lossy and its failures are silent (gc-core / discord launcher)

All file:line references verified against the pinned checkout at `/tmp/fable-baseline-task02`.

## 0. Premise check

The issue is substantially correct, with two adjustments:

- **The (b) withdrawal is right.** `submitEnterAndConfirm` exists at `internal/runtime/tmux/tmux.go:1705`, is wired into `NudgeSession` at `tmux.go:1820-1825`, and is gated on the claude provider family by `submitVerifyEligible` (`tmux.go:1746-1751`), referencing the ga-bwm "drafted but not submitted" stall (`tmux.go:1678-1683`). The Enter-loss half is already fixed upstream; do not touch it.
- **"Historical fence-mismatch dead letters are invisible" is almost right.** Dead letters _are_ shown by `gc nudge status` (`cmd/gc/cmd_nudge.go:385-391`), but only pull-based, only per-agent, and only within `defaultQueuedNudgeDeadRetention = 1 * time.Hour` (`cmd_nudge.go:41`) before `pruneDeadQueuedNudges` removes them (`cmd_nudge.go:2224-2278`). No event is ever emitted on a dead-letter transition — `cmd_nudge.go` contains zero references to the events package. So the operator-facing claim (nothing to see by the time you investigate) holds, and ask (c) stands.
- **`pending_thread` does not exist upstream.** Zero hits across the checkout; it is downstream-shim terminology. Ask (d) is reframed below as reconciliation from the durable extmsg transcript cursor (`ConversationMembershipRecord.LastReadSequence`, `internal/extmsg/types.go:284-292`), which is the upstream-shaped equivalent, and is scoped as a follow-up PR because the issue itself says it overlaps its sibling ask #8.

## 1. Root cause (verified)

The inbound extmsg pipeline makes acceptance durable but delivery ephemeral. The chain for a discord launcher message:

1. `POST /v0/extmsg/inbound` → `humaHandleExtMsgInbound` (`internal/api/huma_handlers_extmsg.go:41`). `HandleInboundNormalized` (`internal/extmsg/inbound.go:237-293`) resolves the route and durably appends the transcript entry, then the handler returns 200. This is the "accepted".
2. Delivery is a fire-and-forget goroutine: `go s.extmsgNotifyInboundMembers(s.backgroundCtx(), ...)` (`huma_handlers_extmsg.go:92`; outbound sibling at `:170`).
3. `extmsgNotifyMembers` (`internal/api/handler_extmsg.go:128-211`) resolves each member (materializing a bead-only named session when none exists, `:196`) and calls `sendBackgroundMessageToSession`. **Every failure in this function is `log.Printf` only** (`:146`, `:176` "notify %s failed", `:198`). Nothing is enqueued, retried, or dead-lettered. The queued-nudge state machine (`internal/nudgequeue`, `cmd/gc/cmd_nudge.go`) is never involved — hence "accepted, no queue trace".
4. `sendBackgroundMessageToSession` (`internal/api/session_resolution.go:611-618`) issues `handle.Nudge` with default delivery → `SessionHandle.Nudge` → `manager.Send` (`internal/worker/handle_lifecycle.go:321-335`) → `sendLocked` (`internal/session/chat.go:703-721`): `ensureRunning` cold-starts the runtime (`chat.go:319-432` — `m.sp.Start(...)`, **no runtime-ready wait**), then `nudgeSession` immediately pastes into the pane.
5. The only cold-start accommodation in the paste path is `sendKeysLiteralWithRetry(target, message, t.cfg.NudgeReadyTimeout)` (`tmux.go:1790`) with `NudgeReadyTimeout = 10 * time.Second` (`tmux.go:80`), and it retries **only on transient send-keys errors** ("not in a mode", `tmux.go:1644-1676`). A tmux `paste-buffer` into a pane whose Claude TUI is still booting (trust dialog, worktree rematerialization) **succeeds at the tmux layer and is silently swallowed by the application** — no error, no retry. `WaitForRuntimeReady` (`tmux.go:2871`) exists but is only called from the bootstrap adapter (`internal/runtime/tmux/adapter.go:836`), never from the nudge path.

The codebase itself documents that a cold wake "can legitimately take a couple of minutes" (`cmd/gc/cmd_nudge.go:55-57`, `defaultNudgePollStartGrace = 5 * time.Minute`). So the effective readiness window on this path (10s error-retry; 0s for the swallowed-paste case) is far below cold-boot P99 — the issue's "cold-start readiness miss".

Separately, for nudges that _do_ go through the queue: a session-fence mismatch dead-letters instantly with no retry (`failedQueuedNudge`, `cmd_nudge.go:2137-2139`; fence predicate `queuedNudgeMatchesTargetFence` `cmd_nudge.go:1616-1624`), the bookkeeping error is discarded at `cmd_nudge.go:470` (`_ = recordQueuedNudgeFailureWithStore(...)`; a second discard at `:516`), no event is emitted, and the dead entry is pruned after 1 hour.

## 2. Blast radius

- **HTTP handlers / goroutine boundary:** `humaHandleExtMsgInbound` (`huma_handlers_extmsg.go:92`) and `humaHandleExtMsgOutbound` (`:170`) both spawn `extmsgNotifyMembers` goroutines off `s.backgroundCtx()`; server shutdown mid-notify currently loses the message. `extmsgNotifyMembers` itself spawns one goroutine per member (`handler_extmsg.go:181-209`).
- **Notify internals:** `notifyResolved` closure (`handler_extmsg.go:161-178`), `formatExtmsgNotifyReminder` (`:290-320`) — already produces a full `<system-reminder>` block; the queued drain path applies its own framing (`formatNudgeInjectOutput` / `formatNudgeRuntimeMessage`, used at `cmd_nudge.go:500-505`), so routing the reminder through the queue risks double-framing. Must be handled.
- **Worker/session layers (read-only for this fix, but on the path):** `SessionHandle.Nudge` wake policies (`handle_lifecycle.go:299-368`) including `NudgeWakeLiveOnly` → `Manager.SendLiveOnly` (`chat.go:733-751`, checks `IsRunning`, never boots); `nudgeObservationBusy` quiescence check (`cmd_nudge.go:820-825`) — the existing busy→queue precedent is `cmd_nudge.go:745-749` (gco-90ui).
- **Queue machinery (new producer added):** enqueue + rollback (`enqueueManagedNudgeThenWake`, `cmd_nudge.go:846-861`), wake socket producer `pingNudgeWakeSocket` (`cmd/gc/nudge_dispatcher.go:32-44`), supervisor dispatcher (`dispatchAllQueuedNudges`, `nudge_dispatcher.go:115-198` — only delivers to `obs.Running` sessions, `:186-188`), per-session poller with 5-minute start grace (`cmd_nudge.go:696-705`), 24h item TTL (`cmd_nudge.go:37`), retry 5×/15s (`:39-40`), shadow beads (`internal/nudgequeue/store.go`).
- **Events subsystem:** typed sealed payload registry (`internal/events/payload.go:37-61`); event-type constants (`events.go:148`); cmd-side best-effort emit precedent `cmdEventEmit`/`openCityEventEmitProvider` (`cmd/gc/cmd_event_emit.go:160-199`). New event type + payload must be registered or `DecodePayload` consumers (SSE projection, `gc events`) reject it.
- **API State seam:** the `internal/api` ↔ `cmd/gc` boundary (`cmd/gc/api_state.go` implements the `State` interface exposing `EventProvider()`, `ExtMsgServices()`, `CityBeadStore()`, ...). The queue enqueue must cross this seam without `internal/api` importing package-main helpers.
- **Config surface:** none — no new config keys; `nudge_dispatcher` mode (`supervisor` vs `legacy`) is honored automatically because we produce into the same queue both consumers drain. No config-reload path is touched.
- **Consumers of `gc nudge status` JSON** (`nudgeStatusJSON`, `cmd_nudge.go:127-144`): unchanged shape; only more items may appear with `source: "extmsg"`.

## 3. Fix candidates

**A (picked): make extmsg member delivery durable by falling back to the queued-nudge state machine, and emit dead-letter events.**
Live sessions that are observed running-and-idle keep immediate delivery. Anything else (not running, booting, busy, live-delivery error) enqueues a durable queued nudge fenced to the resolved session, wakes the reconciler, and pings the dispatcher wake socket. The queue already provides everything asks (a)+(c) need: flock'd persistence, retry with backoff, 24h TTL vs the 10s window, delivery gated on the session actually being up and quiescent (dispatcher `nudge_dispatcher.go:186-189`; poller quiescence 3s + 5-minute start grace), shadow beads for audit, and terminal states. Dead-letter transitions (fence mismatch, expiry, max attempts) get a typed event so failures are no longer silent.

**B (rejected): widen the readiness window in place** — call `WaitForRuntimeReady` (exists, `tmux.go:2871`) inside the notify goroutine after materializing, with a timeout ≥ cold-boot P99 (e.g. reuse the 5-minute grace), then paste. Rejected: pins one goroutine per member for minutes inside the API server; still fire-and-forget (a crash/restart between accept and paste loses the event with no trace, which is the reported symptom); duplicates delivery logic the queue+poller already own; does nothing for ask (c). It patches the window; A removes the class.

**C (follow-up PR, reframed ask (d)): transcript-cursor reconciler** — a supervisor patrol sweep that finds extmsg memberships with `LastReadSequence` behind the latest transcript sequence and no pending queued nudge, and enqueues a redelivery. The transcript is already durable before the 200 (`inbound.go:255-279`), so this converges any residual loss path. Deferred: overlaps the issue's sibling ask #8, and A shrinks the loss window to "enqueue itself failed", which the new alerting makes visible.

## 4. Implementation steps

Work in two commits: **commit 1 = dead-letter events (ask c)**, **commit 2 = durable extmsg delivery (ask a)**. Each commit carries its tests.

### Commit 1 — dead-letter transitions emit events; stop discarding bookkeeping errors

**Step 1.1** — Add the event type and payload.

- `internal/events/events.go`: add `NudgeDeadLettered = "nudge.dead_letter"` next to the existing constants (`:148` area) and include it in the known-types list at `:239` area.
- `internal/events/payloads.go`: add `NudgeDeadLetterPayload{NudgeID, Agent, SessionID, Source, Reason, Attempts, DeadAt}` implementing the sealed `Payload` interface, registered via `RegisterPayload(NudgeDeadLettered, ...)` in the same `init` pattern as the existing payloads.
- Verify: `cd /path/to/worktree && go build ./internal/events/ && go test ./internal/events/` (the registry conformance test `conformance_test.go` must pass with the new type).

**Step 1.2** — Emit on every transition into `state.Dead`. Three sites, all in `cmd/gc/cmd_nudge.go`:

- `recordQueuedNudgeFailureDetailed` (`:2044`) — after the `withNudgeQueueState` transaction commits (i.e. in the existing post-transaction loop at `:2123-2127`), emit one event per `deadLettered` item alongside the existing best-effort `markQueuedNudgeTerminal`.
- `pruneExpiredQueuedNudges` (`:2168-2191`) and `recoverExpiredInFlightNudges` (`:2193-2222`) — these append to `state.Dead` inside the flock; collect the items and emit after the enclosing transaction returns (thread a `*[]queuedNudge` out, or emit from the callers that own the transaction), never inside the flock.
- Use the `openCityEventEmitProvider` + `doEventEmit`-style best-effort path (`cmd_event_emit.go:160-199`): a failed event write warns to `nudgeWarningWriter` and never rolls back queue state (mirror the comment discipline at `cmd_nudge.go:2117-2122`).
- Verify: `go test ./cmd/gc/ -run 'QueuedNudge|DeadLetter|NudgeFailure'`.

**Step 1.3** — Fix the discarded errors. `cmd_nudge.go:470` and `:516`: replace `_ = recordQueuedNudgeFailureWithStore(...)` with the pattern already used at `:1238-1239` (capture the error, `fmt.Fprintf(stderr, "gc nudge drain: dead-lettering ...: %v\n", err)`; exit codes unchanged — bookkeeping must not abort delivery of the remaining items).

- Verify: `go test ./cmd/gc/ -run TestCmdNudgeDrain`.

**Step 1.4** — Surface dead letters in `gc doctor`: add a check that loads `nudgequeue.LoadState(cityPath)` and reports a warning with count + reasons when `len(state.Dead) > 0`. Follow the shape of an existing doctor check in `internal/doctor` / `cmd/gc` (grep `builtin_include_doctor_check.go` for the registration pattern).

- Verify: `go test ./internal/doctor/ ./cmd/gc/ -run Doctor`.

### Commit 2 — extmsg notify falls back to the durable queue

**Step 2.1** — Add the enqueue seam to the API state interface. In the `State` interface consumed by `internal/api` (where `EventProvider()` / `ExtMsgServices()` live) add:

```go
// EnqueueSessionNudge durably queues text for delivery to the resolved
// session, waking the managed reconciler and the dispatcher. source is the
// queue-item source label (e.g. "extmsg").
EnqueueSessionNudge(sessionID, source, text string) error
```

Implement it in `cmd/gc/api_state.go` by composing the existing package helpers: build the item with `newQueuedNudgeWithOptions(agentKey, text, source, time.Now(), queuedNudgeOptions{SessionID: id, ContinuationEpoch: epoch})` after resolving the target via `resolveNudgeTarget`-equivalent lookup from the session bead; then `enqueueManagedNudgeThenWake` (`cmd_nudge.go:846`) so the reconciler wake + rollback semantics are inherited unchanged; then `pingNudgeWakeSocket(cityPath)` (`nudge_dispatcher.go:32`). Do **not** re-implement enqueue in `internal/api` — the queue's supersede/rollback/close-reason invariants live in cmd/gc and `internal/nudgequeue`.

- Verify: `go build ./... && go test ./cmd/gc/ -run 'ApiState|EnqueueSessionNudge'`.

**Step 2.2** — Change `notifyResolved` in `extmsgNotifyMembers` (`handler_extmsg.go:161-178`):

1. Attempt live-only delivery: `handle.Nudge(ctx, worker.NudgeRequest{Text: nudge, Wake: worker.NudgeWakeLiveOnly})` — this routes to `Manager.SendLiveOnly` (`chat.go:733-751`), which never cold-boots and returns `Delivered=false` for a non-running session. Before attempting, consult the handle's `LiveObservation` and treat a running-but-busy session (`LastActivity` within the poller quiescence, the `nudgeObservationBusy` rule at `cmd_nudge.go:820-825`) as not-live, matching the gco-90ui precedent at `cmd_nudge.go:745-749`.
2. If not delivered (false, or error): call `s.state.EnqueueSessionNudge(resolvedID, "extmsg", body)` where `body` is the reminder **without** the outer `<system-reminder>` wrapper (see 2.3). Log enqueue failures at error level — this is now the only loss path, and commit 1's alerting plus the follow-up reconciler cover it.

- The materialization call (`resolveSessionIDMaterializingNamedWithContext`, `handler_extmsg.go:196`) stays as-is: it is bead-only (`createDeferredLocked` → `CreateAliasedBeadOnlyNamedWithMetadata`, `handle_lifecycle.go:384-402`), so the goroutine never blocks on a runtime boot; the enqueue's managed wake asks the reconciler to boot it, and the poller/dispatcher deliver once the session is up and quiescent.
- Verify: `go test ./internal/api/ -run Extmsg`.

**Step 2.3** — Prevent double `<system-reminder>` framing. Split `formatExtmsgNotifyReminder` (`handler_extmsg.go:290-320`) into body-builder + wrapper: the live path keeps the wrapped form (unchanged bytes — assert in test); the queued path passes the body, because drain applies its own framing (`cmd_nudge.go:500-505`; wait-idle wraps at `chat.go:568-583`). Sanitization (`SanitizeForSystemReminder`) already runs in both paths — keep it in the body-builder so it is applied exactly once.

- Verify: `go test ./internal/api/ -run Reminder && go test ./cmd/gc/ -run 'InjectOutput|RuntimeMessage'`.

**Step 2.4** — Coalesce chatty conversations. On enqueue for source `extmsg`, supersede any still-pending item for the same `(SessionID, conversation)` — carry the conversation identity in the item `Reference` (`nudgequeue.Reference`, already round-tripped via `reference_json`, `store.go:103-108`) and use the existing `superseded` terminal state (`store.go:380`). The reminder semantics are "there is a new message in conversation X; check it", so last-write-wins is correct and bounds queue growth to one pending item per member per conversation.

- Verify: `go test ./cmd/gc/ -run Supersede`.

**Step 2.5** — Full gates before push: `make build && make check` (and `make check-docs` if any docs touched). Classify any failures against a clean baseline run of the same commands on the unmodified base commit.

## 5. Test strategy (ships in the same commits)

Commit 1:

- `cmd/gc/cmd_nudge_test.go`: `TestRecordQueuedNudgeFailureEmitsDeadLetterEvent` — enqueue an item, dead-letter it via `recordQueuedNudgeFailureWithStore(..., errNudgeSessionFenceMismatch, ...)` with a fake/file event provider; assert exactly one `nudge.dead_letter` event whose payload carries the nudge ID, `reason` distinguishing fence-mismatch from expiry, and that the item is in `state.Dead`. A companion case drives the expiry path through `pruneExpiredQueuedNudges` and asserts its event.
- `TestNudgeDrainWarnsWhenDeadLetterBookkeepingFails` — force the store write to fail (existing seam pattern: `nudgeWarningWriter` + a failing `beads.NudgesStore`); assert a warning is written and drain still delivers the remaining items (regression for the `:470` discard).
- Doctor: `TestDoctorReportsDeadLetterBacklog` — seed `state.Dead`, assert the check reports count and reason.

Commit 2:

- `internal/api`: `TestExtmsgNotifyQueuesWhenSessionNotRunning` — fake worker handle returns `Delivered=false` for live-only; assert `EnqueueSessionNudge` is called with the resolved session ID, source `extmsg`, and an **unwrapped** body (no `<system-reminder>` substring). This is the regression test for the reported symptom: the notify no longer evaporates when the target is cold.
- `TestExtmsgNotifyQueuesWhenLiveDeliveryErrors` — handle returns an error; same assertion.
- `TestExtmsgNotifyDeliversLiveWhenIdle` — running + idle observation; assert live nudge with the byte-identical wrapped reminder and no enqueue.
- `cmd/gc`: end-to-end queue test in the `nudge_dispatcher_test.go` style — enqueue via the new state method against a fake provider with `IsRunning=false`, flip to running, run a dispatcher pass, assert delivery and terminal `injected`/`accepted_for_injection` state; assert the item survives (stays Pending) while the session is down instead of dead-lettering before TTL.
- Supersede: two enqueues for the same member+conversation leave one pending item; the first is terminal `superseded`.

## 6. Maintainer-rejection risks and pre-emption

1. **"You changed live chat latency"** — only busy/booting/down sessions defer to the queue; running-and-idle sessions keep the exact current live path and reminder bytes (asserted in `TestExtmsgNotifyDeliversLiveWhenIdle`). The busy→queue rule reuses the codebase's own gco-90ui precedent (`cmd_nudge.go:728-749`).
2. **"Queue flooding from busy rooms"** — supersede-per-(member, conversation) in step 2.4 bounds pending growth; reminder semantics make last-write-wins correct.
3. **"Double system-reminder wrapping / prompt-injection regression"** — step 2.3 splits builder from wrapper, keeps `SanitizeForSystemReminder` (the #2195/ga-vs7 guard) applied exactly once, and tests both framings.
4. **"Layering: api importing cmd/gc queue internals"** — crossed via the existing `State` interface seam (`cmd/gc/api_state.go`), the same pattern as `EventProvider()`; no new import edges.
5. **"Event feed noise / unregistered payloads break SSE"** — one typed event per terminal transition only (not per retry), registered in the sealed payload registry so `DecodePayload` consumers keep working; conformance test covers it.
6. **"Emitting inside the queue flock"** — all emission happens after the `withNudgeQueueState` transaction commits, best-effort, mirroring the existing shadow-bead discipline (`cmd_nudge.go:2117-2122`); a failed emit can never wedge the queue.
7. **"Fence dead-letters will increase"** (extmsg items are session-fenced, so a rebind/regeneration kills them) — intended: that is precisely the failure class ask (c) makes visible, and the follow-up transcript-cursor reconciler (candidate C) redelivers from the durable record. Called out in the PR body as the explicit follow-up.
8. **"Scope creep vs the issue's four asks"** — (b) verified already upstream and untouched; (a)+(c) are this PR; (d) reframed to the upstream concept (no `pending_thread` exists here) and deferred with rationale to its overlapping sibling ask.
