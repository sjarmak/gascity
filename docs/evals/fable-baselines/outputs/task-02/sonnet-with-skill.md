Issue: #3972 — bug: session event delivery is lossy and its failures are silent (gc-core / discord launcher)

## 0. Premise check

**Ask (b) [Enter-fire submit loss] — refuted / already-fixed (confirmed independently, matches reporter's own re-grade).**
`submitEnterAndConfirm` exists at `internal/runtime/tmux/tmux.go:1705-1726`: it re-sends Enter while the pane stays idle, up to `submitEnterMaxSends = 3` (`tmux.go:1685`), polling `submitConfirmPollsPerSend = 4` times per send at `submitConfirmPollInterval = 150ms` (`tmux.go:1686-1687`). It is wired into `NudgeSession` at `tmux.go:1818-1826`, gated by `submitVerifyEligible` (`tmux.go:1746-1752`), which scopes the behavior to `sessionlog.ProviderFamily(provider) == "claude"` — exactly the reporter's claim. The comment at `tmux.go:1816-1818` references the "ga-bwm 'drafted but not submitted' stall" by the same name the issue uses. Verdict: excluded from scope.

**"discord launcher" (title vocabulary) — narrowed / reframed.** The generic webhook receiver's conversation-target sink is an explicit, unimplemented stub: `webhooksink.routeConversation` (`internal/webhooksink/sink.go:202-208`) falls back to `StubConversationSink`, whose `Deliver` (`sink.go:216-224`) always returns `Rejected: true, Reason: "conversation sink not yet wired (E7)"` with a `TODO(E7)` comment describing the still-unbuilt normalizer. **No code path in this repo turns a raw Discord webhook POST into a session launch or event.** The real, wired path is `POST /v0/extmsg/inbound` → `extmsg.HandleInboundNormalized` (`internal/api/huma_handlers_extmsg.go:39-60`, `internal/extmsg/inbound.go:234-286`), which the handler's own doc comment says is for "out-of-process adapters that verify and normalize on their side before posting to the API" (`inbound.go:234-236`) — i.e. an operator-run Discord bot outside this repo. The one in-repo concept literally named "launcher" is `extmsg.GroupModeLauncher` (`internal/extmsg/types.go:329-331`, used at `group_service.go:52`), a conversation-group routing mode ("routes messages through a launcher participant"), not a webhook-triggered session-launch feature. Reframed ask: the issue's "discord launcher" is an external Discord adapter posting normalized messages to `/v0/extmsg/inbound`, optionally into a `GroupModeLauncher`-routed group.

**"pending_thread" — vocabulary not found.** Zero hits for `pending_thread` across the entire checkout (`grep -rn "pending_thread" --include="*.go" .` → no matches). Reframed onto the real durable state: `extmsg.ConversationMembershipRecord` (`internal/extmsg/types.go:283-296`), specifically its `LastReadSequence` field (`:292`), plus the fact (established below) that post-append member notification has no durable "attempted/failed" marker at all.

**Ask (a) [readiness window / redelivery-on-ready] — confirmed as a real, narrower gap than stated.** See Root cause §1. Real for materializing ("pending") sessions; not real for already-running sessions, which self-heal via `gc mail check --inject` on `UserPromptSubmit`.

**Ask (c) [dead-letter alerting for fence mismatches is silent] — confirmed, exact code cited.** See Root cause §2.

**Ask (d) [background reconciler for pending_thread launches] — confirmed as a real, scopable gap**, given an existing sibling reconciler pattern that does _not_ cover notify retries. See Root cause §1 and Blast radius.

## 1. Root cause (verified)

Two related, independently-verified silent-loss mechanisms, in two different subsystems, share the same bug shape: **an async delivery step whose failure is only `log.Printf`'d, with no event, no telemetry, and no durable retry marker.**

**§1. extmsg member notification (asks a, d).** `HandleInboundNormalized` durably appends every routed inbound message to the transcript synchronously (`internal/extmsg/inbound.go:253-267`, `deps.Services.Transcript.Append`) and the HTTP handler propagates a real error to the caller on failure (`huma_handlers_extmsg.go:61-90`) — so **the raw message itself is never silently lost at intake**; this narrows the issue's framing. What _is_ silent is the step that wakes a live session. Two production call sites spawn it detached from the request:

- `huma_handlers_extmsg.go:92` — `go s.extmsgNotifyInboundMembers(s.backgroundCtx(), *input.Body.Message)` (inbound path)
- `huma_handlers_extmsg.go:170` — `go s.extmsgNotifyMembers(s.backgroundCtx(), notifyConversation, sourceDisplay, "agent", input.Body.Text, input.Body.SessionID, "")` (outbound/broadcast path)

Inside `extmsgNotifyMembers` (`internal/api/handler_extmsg.go:128-206`), each conversation member is handled in its own inner goroutine bounded by a `wg.Wait()` (`:177-206`), so the fan-out doesn't leak — but the _outer_ call is still detached from both HTTP responses. Two failure points are logged and dropped with no other trace:

- materializing a not-yet-existing named session — `log.Printf("extmsg: resolve session %s failed: %v", ...)` then `return` (`handler_extmsg.go:192-196`)
- delivering the nudge to an already-resolved session — `log.Printf("extmsg: notify %s failed: %v", ...)` inside `notifyResolved` (`handler_extmsg.go:172-173`)

Neither path emits an event, a metric, or writes any durable "not notified" marker. This is a real, load-bearing fact the issue doesn't mention: **an already-running session self-heals.** `gc mail check --inject` is wired to the `UserPromptSubmit` hook (`cmd/gc/cmd_mail.go:494-505`, `routeMailCheck`, dispatched via `writeProviderHookContextForEvent(stdout, hookFormat, "UserPromptSubmit", ...)`), so it re-derives unread state independent of the failed push on the session's very next turn — this is latency, not loss, for a live session. The gap is real only for a session that must be **materialized** (a genuinely "pending" launch): if `resolveSessionIDMaterializingNamedWithContext` fails, or the notify races the new session's cold start, that member never reaches `UserPromptSubmit` and nothing else ever revisits it. (Also worth fixing in the same commit: the comment at `inbound.go:220` — "Sessions discover unread entries via gc transcript check --inject" — cites a command that doesn't exist; the real command is `gc mail check --inject`, `cmd_mail.go:494`. Stale-comment drift, cheap to fix alongside.)

**§2. Nudge queue dead-lettering (ask c).** In `tryDeliverQueuedNudgesByPoller` (`cmd/gc/cmd_nudge.go:1211-1296`), the fence-mismatch branch dead-letters items with zero telemetry:

```go
// cmd_nudge.go:1237-1241
items, rejected := splitQueuedNudgesForTarget(target, items)
if len(rejected) > 0 {
    if recErr := recordQueuedNudgeFailureWithStore(target.cityPath, beads.NudgesStore{Store: deliveryStore}, queuedNudgeIDs(rejected), errNudgeSessionFenceMismatch, time.Now()); recErr != nil {
        bookkeepErr = fmt.Errorf("dead-lettering fence-mismatched nudges: %w", recErr)
    }
}
```

Compare the delivery-error branch six lines later, which _does_ call `telemetry.RecordNudge` (metric + OTel log event `session.nudge`, `internal/telemetry/recorder.go:387-401`) before dead-lettering (`cmd_nudge.go:1281-1282`). The asymmetry is real and undocumented: fence-mismatched items go dead with no signal at all, while delivery-failed items get an attempt-level signal that _still_ doesn't distinguish "now permanently dead" from "requeued." `recordQueuedNudgeFailureDetailed`'s own doc comment (`cmd_nudge.go:2039-2042`) states the dead-lettered return slice "is discarded" by its only production caller. The sole operator-facing surface is `gc nudge status` (`cmd_nudge.go:252-256`), a pull-only CLI command — this matches, and confirms, the issue's own stated mitigation ("watch `gc nudge status <session>` for pending/dead") because there genuinely is no push signal today.

## 2. Blast radius

**Callers + siblings**

- `extmsgNotifyMembers` has exactly 2 production call sites (`huma_handlers_extmsg.go:92` via `extmsgNotifyInboundMembers`, and `:170` directly) with identical silent-failure shape. A fix touching only one is a half-fix (adoption-review finding #13, parallel code path siblings).
- `recordQueuedNudgeFailureWithStore` has 2 call sites in `cmd_nudge.go` (fence-mismatch `:1238`, delivery-error `:1281`) with _different_ telemetry behavior around them today; normalizing one without the other reintroduces the asymmetry in a new shape.

**Config paths**

- `internal/config/extmsg.go` `ExtMsgConfig.DefaultRoutes` only selects which agent an unbound conversation routes to; it does not gate retry/notify behavior — untouched by this fix.
- No `city.toml` surface exists for notify-retry bounds today. Following the nudge queue's own precedent of a hardcoded default (`defaultQueuedNudgeMaxAttempts = 5`, `cmd_nudge.go:1216`) rather than adding a new config key; a config knob is explicitly deferred, not silently added.

**Goroutine boundaries + locks**

- `backgroundCtx()` (`internal/api/server.go:22-38`) is a deliberately bounded (30s), self-cancelling, go-vet-clean detached context — reuse it; do not add a second goroutine pattern.
- `recordQueuedNudgeFailureDetailed` runs its dead-letter transition inside the flock'd `withNudgeQueueState` transaction (`cmd_nudge.go:2044-2076`). Any new telemetry/event emission for the fence-mismatch case must fire **after** that closure returns, mirroring the existing pattern at `cmd_nudge.go:2118-2123` where `markQueuedNudgeTerminal` warnings are logged only once the lock is released — new work must never run inside `withNudgeQueueState`.
- The reconciler-tick call site `cmd/gc/city_runtime.go:1213-1220` (`reapStaleExtmsgBindings` / `reapStaleExtmsgParticipants`) already runs on every tick, outside any request goroutine, with its own "errors logged and swallowed, never stalls the loop" contract (`extmsg_binding_reaper.go:21-23`) — this is the extension point for ask (d), not a new ticker.

**Cross-subsystem contracts**

- `internal/events/events.go:143-149` (const block) + the enumerated registry slice at `:237-245` is the closed event-type contract; a new event constant (e.g. `ExtMsgNotifyFailed`, `NudgeDeadLettered`) must be added to **both** or downstream `events.jsonl` schema validation / docs generation won't recognize it (see `WebhookReceived, WebhookRejected` at `:241` for the precedent of a paired received/rejected event name).
- `extmsg.ConversationMembershipRecord` (`types.go:283-296`) is read by `internal/extmsg/transcript_service.go:569,613,994` and serialized via bead metadata (`last_read_sequence` at `:994`) — adding a field to this struct must follow the same bead-metadata round-trip as `LastReadSequence`, not a parallel store.

**Double-transformation**

- Checked: `sendBackgroundMessageToSession` delivers text built once by `formatExtmsgNotifyReminder` (`handler_extmsg.go:159-170`); it is not re-routed through a second formatter (contrast the nudge queue's own `formatNudgeInjectOutput`, `cmd_nudge.go:1520-1538`, which already sanitizes via `extmsg.SanitizeForSystemReminder` for the `<system-reminder>` breakout risk noted at `gastownhall/gascity#2195`). No double-wrap risk found on either touched path.

**Untouched surfaces (explicit)**

- No change to the transcript `Append` durability contract or to `/v0/extmsg/inbound` / `/v0/extmsg/outbound` response codes/shapes.
- No change to `webhooksink`'s E7 `StubConversationSink` — wiring raw Discord webhooks into extmsg is a separate, larger feature (Candidate C below), deliberately out of scope here.
- No change to `GroupModeLauncher` routing semantics.
- No new `city.toml` keys.
- No change to `gc mail check` behavior — only its stale citation in a comment.
- No change to `tryDeliverQueuedNudgesByPoller`'s delivery-error telemetry call (already correct); only the fence-mismatch branch gains a call.

## 3. Fix candidates (>=2, genuinely weighed)

**A (picked): make notify failures durably visible, and add a reconciler-tick retry sweep, reusing two patterns already in the codebase.**

- Emit a new `events.ExtMsgNotifyFailed` constant (mirroring `ExtMsgInbound`/`ExtMsgOutbound` at `events.go:148-149`, registered in the slice at `:237-245`) from both `extmsgNotifyMembers` failure points (`handler_extmsg.go:172-173` and `:192-196`), carrying the conversation ref, session selector, and error.
- For the nudge queue's separate bug, add a `telemetry.RecordNudge` call in the fence-mismatch branch (`cmd_nudge.go:1237-1241`), matching the delivery-error branch's existing call (`:1281-1282`), plus a distinct dead-letter event so a permanently-dead item is distinguishable from a requeue (closing the gap `recordQueuedNudgeFailureDetailed`'s own comment at `:2039-2042` already flags).
- Extend `ConversationMembershipRecord` with a `NotifyFailedAt`/attempt-count field, following `LastReadSequence`'s existing bead-metadata round-trip shape (`types.go:292`, `transcript_service.go:994`), instead of a parallel store.
- Add a small `reapPendingExtmsgNotifies`-shaped function next to `reapStaleExtmsgBindings` (new function in `cmd/gc/extmsg_binding_reaper.go`, matching its ~50-80 line size and "errors logged and swallowed" contract), wired into the existing tick at `city_runtime.go:1213-1220`. It re-attempts `sendBackgroundMessageToSession` for members with a recorded failed-notify marker, bounded by an attempts cap modeled on `defaultQueuedNudgeMaxAttempts = 5` (`cmd_nudge.go:1216`), and marks them dead (with the new event) past the cap.

Cost in this codebase: one new small reaper function, one struct field extension, two new event constants + registry entries, one telemetry call added at an existing branch, wiring at an already-established reconciler call site. No new goroutines, no new config surface, no change to the fast-ACK HTTP contract.

**B (rejected): make `extmsgNotifyMembers` synchronous** — drop the `go` at `huma_handlers_extmsg.go:92` and `:170`, fold its outcome into the HTTP response so a failure becomes a retryable 5xx.

Rejected on repo-specific costs, not abstract ones: `humaHandleExtMsgInbound`'s own doc comment (`huma_handlers_extmsg.go:65-79`) explicitly designs the 4xx/5xx split so a _transient_ fault is retryable and a _permanent_ fault is not, specifically to avoid "pinning the adapter's ordered poll offset behind one poison message" and "wedging the whole account stream." Folding a fan-out to N conversation members into that same per-request retry contract means one slow/cold-booting member forces the adapter to redeliver the _entire_ message — including the transcript entry that already durably succeeded — into exactly the wedge-prone poll-offset failure the existing comment says this split exists to prevent. It also stretches the fast-ACK webhook latency budget to N × session-materialize time. It moves the failure earlier and widens its blast radius instead of shrinking it; A keeps the existing fast-ACK contract and adds the missing retry loop only where the gap actually is.

**C (deferred, named, out of scope): wire up the E7 `ConversationSink`** (`webhooksink/sink.go:202-224`) so raw Discord webhooks route directly into extmsg without an external adapter.

This is the feature "discord launcher" in the issue title most naturally evokes, and it's real, already-planned work (the `TODO(E7)` comment names the exact shape: a provider-normalizer → `extmsg.HandleInboundNormalized`). But it is an architecturally distinct, multi-file feature (new Discord payload normalizer, its own auth/verification reuse of `internal/webhookverify/discord.go`, its own premise and blast-radius pass) that is orthogonal to the silent-failure bug asks (a)/(c)/(d) are actually describing — building it wouldn't fix the notify-goroutine or dead-letter-alerting gaps, and fixing those gaps doesn't require it. Recommend filing as a separate candidate issue rather than folding into this PR.

## 4. Implementation steps

Two commits, split by subsystem so each ships independently and each carries its own tests.

**Commit 1 — nudge queue dead-letter alerting (ask c).**

1. In `cmd/gc/cmd_nudge.go`, add a `telemetry.RecordNudge(context.Background(), target.agentKey(), errNudgeSessionFenceMismatch)` call in the fence-mismatch branch (`:1237-1241`), immediately before the existing `recordQueuedNudgeFailureWithStore` call, matching the call shape already used at `:1281`.
   Verification: `go build ./cmd/gc/...` succeeds; `go vet ./cmd/gc/...` clean.
2. Add a `NudgeDeadLettered` (or similarly-named) event constant to `internal/events/events.go`'s const block (near `:148-155`) and its registry slice (`:237-245`), following the `WebhookReceived, WebhookRejected` pairing precedent at `:241`.
   Verification: `go build ./internal/events/...`; if a schema/doc generator exists for event names (`make generate` / `make check-schema`), run it and confirm no stale-docs diff.
3. Emit the new event from `recordQueuedNudgeFailureDetailed` (`cmd_nudge.go:2044-2076`) for each item in `deadLettered`, but **after** the `withNudgeQueueState` closure returns (mirroring the existing post-lock warning loop at `:2118-2123`), not inside it.
   Verification: new/updated unit test in `cmd/gc/cmd_nudge_test.go` asserting the event fires exactly once per dead-lettered item and zero times for a requeued (non-terminal) item — see Test strategy below for the exact assertion.
4. Run the package test suite for the touched file: `go test ./cmd/gc/... -run TestNudge -v`.
   Verification: passes; specifically the new fence-mismatch-alerting test and the existing tests around `cmd_nudge_test.go:2902-2913` (`nudgeCalls[0].Message` / `dead = %+v` fence-mismatch assertions) still pass unmodified, confirming no behavior change to delivery itself.

**Commit 2 — extmsg notify durability + reconciler retry (asks a, d).**

5. Extend `extmsg.ConversationMembershipRecord` (`internal/extmsg/types.go:283-296`) with a notify-failure marker field, and thread it through the same bead-metadata read/write path `LastReadSequence` already uses (`transcript_service.go:569,613,994` — add a matching `parseInt64`/format pair for the new field).
   Verification: `go build ./internal/extmsg/...`; new unit test in `internal/extmsg/` (package-level, using the existing test store fixtures) asserting a round-trip: set the field, reload the record via the store, read it back unchanged.
6. Add `events.ExtMsgNotifyFailed` to `internal/events/events.go` (const block + registry slice, same pattern as step 2).
7. In `internal/api/handler_extmsg.go`, emit `events.ExtMsgNotifyFailed` from both silent failure points: the materialize-failure branch (`:192-196`) and the `notifyResolved` failure branch (`:172-173`), and — inside the same call — persist the notify-failure marker on the member's `ConversationMembershipRecord` via the existing `Transcript` service (no new store).
   Verification: extend `TestExtmsgNotifyMembersSuppressesDiscriminatorForRoutedParticipant`-style tests in `internal/api/handler_extmsg_test.go` (reusing `newSessionFakeState(t)` and `fs.sp.Calls`) with a new case that forces `resolveSessionIDMaterializingNamedWithContext` to fail (fake returns an error) and asserts (i) `fs.sp.Calls` contains no `Nudge` call, (ii) an `ExtMsgNotifyFailed` event was emitted with the right conversation/session, (iii) the membership record's failure marker is set.
8. Add `reapPendingExtmsgNotifies` to `cmd/gc/extmsg_binding_reaper.go`, following `reapStaleExtmsgBindings`'s exact signature/error-handling shape (`:24-38`): takes `(ctx, store beads.SessionStore, now time.Time, stderr io.Writer)`, scans for membership records with a set failure marker and attempt count below `defaultQueuedNudgeMaxAttempts` (reused constant from `cmd_nudge.go:1216`, not duplicated), retries `sendBackgroundMessageToSession`, clears the marker on success or emits the dead-letter event past the cap. Errors logged and swallowed, matching the file's stated contract.
   Verification: new test in `extmsg_binding_reaper_test.go` (existing file, same fake store pattern) covering: a pending-notify member gets retried and cleared on success; a member past the attempts cap gets the dead-letter event and no further retry.
9. Wire the new reaper into `cmd/gc/city_runtime.go:1213-1220`, immediately after `reapStaleExtmsgParticipants` on the same tick.
   Verification: `go build ./cmd/gc/...`; existing `session_reconciler_test.go` reconciler-tick tests still pass unmodified (confirms the new call doesn't alter tick ordering/timing for unrelated assertions).
10. Fix the stale comment at `internal/extmsg/inbound.go:220` — replace "gc transcript check --inject" with "gc mail check --inject" to match the real command name (`cmd_mail.go:494`).
    Verification: `grep -rn "gc transcript check" .` returns zero hits after the change.
11. Final step for both commits: `make build && make check` (and `make check-docs` only if `docs/reference/cli.md` or `docs/tutorials/04-communication.md` needed touching — they should not, since no CLI surface or output shape changed). Classify any failure against a clean baseline run of `make build && make check` on the unmodified base commit before attributing it to this change.

## 5. Test strategy (same commit as the source change)

**Tier:** unit tests in both commits — `cmd/gc/cmd_nudge_test.go` (table-driven, existing `nudgeCalls`/`fake` harness) and `internal/api/handler_extmsg_test.go` (existing `newSessionFakeState(t)` / `fs.sp.Calls` fake session provider), plus a small new suite in `cmd/gc/extmsg_binding_reaper_test.go` reusing that file's existing fake `beads.SessionStore` setup. No integration or `.txtar` tier needed — no CLI-visible output or command surface changes.

**Fakes:** `fs.sp` (fake session provider tracking `.Calls`, already used at `handler_extmsg_test.go:214-220`) for the extmsg-side tests; the existing `beads.NudgesStore`/fake store plumbing already used throughout `cmd_nudge_test.go` for the nudge-side tests. No gomock; no new fake types needed — extend existing ones.

**THE regression test for the reported symptom (ask c, since it has the clearest observable symptom and the issue's own reproduction is strongest here):** a new test in `cmd/gc/cmd_nudge_test.go`, structured like the existing fence-mismatch test at `:2902-2913`, that:

1. Enqueues a nudge, forces a fence mismatch (as the existing test already does).
2. Asserts — this is the new assertion the current tests don't make — that a `telemetry`/event hook fires for the dead-lettered item (inject a fake telemetry/event recorder the same way `fs.sp.Calls` is injected, or assert on the emitted `events.NudgeDeadLettered` payload if routed through the existing `EmitEvent` deps pattern).
3. Asserts that a **non**-fence-mismatched item delivered normally produces **no** dead-letter event — discriminating the fix from a "fire the event on every delivery" false-pass.

This directly asserts the user-visible symptom ("dead-letter alerting is silent") is gone, not internal bookkeeping — before the fix, this test fails because no event fires; after, it passes because the fence-mismatch branch now emits one.

A second, equally load-bearing regression test lives in Commit 2: a `handler_extmsg_test.go` case forcing session materialization failure and asserting `ExtMsgNotifyFailed` fires and the membership record's retry marker is set — this is the regression test for asks (a)/(d)'s "silent" claim specifically (distinct from ask (c)'s subsystem).

Both tests assert branches beyond the happy path: the fence-mismatch/materialize-failure branch AND a control case (successful delivery emits no spurious dead-letter/failure event) — satisfying the "missing regression test branches" adoption-review pattern (multiple code paths, not just happy path).

## 6. Maintainer-rejection pre-mortem

- **"You're adding telemetry inside the flock'd `withNudgeQueueState` transaction, extending lock hold time on a hot path."** Pre-empted by step 3's explicit placement of the new event emission _after_ the closure returns, mirroring the existing `markQueuedNudgeTerminal` post-lock warning pattern at `cmd_nudge.go:2118-2123` — called out explicitly in Blast radius §Goroutine boundaries and enforced by the implementation step itself, not left to the implementer's judgment.
- **"The new reconciler retry could double-notify a member who actually did receive the nudge but whose success bookkeeping raced the failure marker."** Pre-empted by clearing the failure marker on success inside `reapPendingExtmsgNotifies` itself (step 8) before returning, and by the regression test in step 8's verification asserting the success case clears the marker — an operator running the reconciler twice on a since-recovered member should see no duplicate nudge on the second tick because the marker is already cleared.
- **"Parallel code path siblings — you fixed the inbound notify call site but not outbound."** Pre-empted explicitly: step 7 touches both `handler_extmsg.go:172-173` (used by both call sites, since `notifyResolved` is shared) and `:192-196` (also shared, since `extmsgNotifyMembers` itself is the single function both `:92` and `:170` call into) — the fix is naturally single-sited because both production callers already funnel through the same function; documented in Blast radius §Callers+siblings so the reviewer doesn't have to re-derive it.
- **"New event constants without a schema/registry update will silently be dropped by consumers."** Pre-empted by steps 2 and 6 explicitly requiring the `events.go` registry-slice addition (`:237-245`) alongside the const, and step 11's `make check-docs` / `make check-schema` gate catching a missed registration as a generated-docs diff.
- **"A background reconciler retrying nudge delivery could contaminate infrastructure/control sessions with work-agent semantics."** Not applicable here — `reapPendingExtmsgNotifies` only re-delivers a message a human/agent already sent into an extmsg conversation the member is already bound to; it does not originate new work or apply `[agent_defaults]`/work config to any session. No config-contamination surface exists in this change (explicitly noted as untouched in Blast radius §Config paths).
- **"Silent error handling reintroduced elsewhere" (e.g. `_ = store.Write()`).** The new reaper follows `reapStaleExtmsgBindings`'s existing "log and swallow, never stall the tick" contract deliberately (matching the file's stated design, `extmsg_binding_reaper.go:21-23`) rather than propagating errors that would stall the reconciler — this is a considered match to precedent, not an oversight, and is called out as such in step 8 so a reviewer doesn't flag it as an accidental swallow.
- **"Golden snapshot / doc drift" from the new event names or the stale comment fix.** No CLI output, log line, or error message visible to a user changes — only internal event emission and a code comment. Step 11 explicitly scopes `make check-docs` to only run if a docs file needed touching, and states none should.

## Design capture

Change class: point-fix (two related, narrowly-scoped point fixes in existing subsystems; no new package, no new subsystem boundary, no new wire/config contract)
[ ] Trigger fired? No — no new subsystem boundary, no new package, no new public contract/schema. The two new event constants extend an existing enumerated contract (`events.go`) rather than establishing a new one; the `ConversationMembershipRecord` field extension follows an existing field's exact shape.
N/A, code-only PR — this is fixing a silent-failure gap in two existing delivery paths (extmsg notify goroutine, nudge dead-letter bookkeeping), not introducing a subsystem, store topology, or provider interface. A design doc would be over-scoped for two ~80-100 line additions that each closely mirror an existing sibling (`reapStaleExtmsgBindings`, the delivery-error telemetry call).

## Convention triggers

[x] Config field sync (B11, B15) — N/A, no new config field added (deliberately deferred, see Blast radius §Config paths)
[x] Store write error propagation (B12) — membership-record marker write failures in step 7/8 must propagate like `LastReadSequence` writes do today, not be silently dropped
[x] Timeout isolation (B13) — reuses `backgroundCtx()`'s existing 30s bound; no new timeout introduced
[ ] do*()/cmd*() split (B19) — N/A, no new `cmd_*.go` command surface added
[x] Test doubles (B20) — reuses `fs.sp` fake and existing nudge-queue fakes; no gomock
[ ] Map key separation (B18) — N/A
[ ] Startup vs reload (B16) — N/A, no reload-path interaction
[x] Goroutine lifecycle (B17) — no new goroutine introduced; reconciler retry runs synchronously within the existing tick, not a new background loop
[ ] Config snapshot safety (B21) — N/A
[x] Dead code audit (B22) — new `reapPendingExtmsgNotifies` has exactly one call site (step 9); grep after implementation to confirm no orphaned helper
[x] Fix scope completeness (B23) — both `extmsgNotifyMembers` call sites covered via the shared function (see pre-mortem); both `recordQueuedNudgeFailureWithStore` call sites reviewed (only fence-mismatch needed the new call; delivery-error already had it — confirm this asymmetry is intentional, not overlooked, in the PR description)
[x] Verify-before-delete (B24) — reconciler clears the failure marker only after a confirmed successful `sendBackgroundMessageToSession` return, never speculatively
[x] Constant grep radius (B25) — `defaultQueuedNudgeMaxAttempts` reused, not redefined; grep for any other hardcoded "5" retry-count literal near the new reaper before landing
[ ] Golden snapshot drift (B26) — N/A, no CLI/log-visible output changes
[ ] Env projection layer (B29) — N/A
[ ] Package-level race-safety (B30) — N/A, no new package-level mutable state
[ ] Hard-fail examples audit (B31) — N/A, no silent-degradation-to-hard-error conversion
[x] Test save/restore of pkg state (B33) — N/A expected (no package-level state to restore), confirm no test in steps 3/7/8 mutates shared package vars without `t.Cleanup`
