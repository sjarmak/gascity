# Issue #3972 — "session event delivery is lossy and its failures are silent (gc-core / discord launcher)"

## Verdict up front

The issue as filed should **not** be planned as a fix. It bundles a self-withdrawn
claim, several codenamed items (`ga-sgx`, `ga-ovvh`, `P2.8 battery`, `AUR2`) that
don't correspond to anything in this checkout, and two asks ("discord launcher"
event loss, "pending_thread" reconciler) that reference components and states
that do not exist under those names anywhere in the repository. There is no
stack trace, log excerpt, failing test, or reproduction step attached — only
a synthesized narrative. I verified every concrete, checkable claim in the
checkout at commit `ee616a7e41c74285d37283e9ca0022db120e9f14` (2026-07-06).

One narrow, genuinely-grounded finding did fall out of the investigation —
ask (c), "dead-letter alerting (fence mismatches are silent)" — and I've
written a full implementation plan for that specific, verified gap in section 7. Everything else in the issue should be closed or sent back for a concrete
repro before any engineering time is spent on it.

---

## 1. What I verified, claim by claim

### (b) — reporter's own re-grade: "ALREADY FIXED upstream, do NOT file it"

Confirmed correct. `internal/runtime/tmux/tmux.go:1680-1821` contains
`submitEnterAndConfirm` and `submitVerifyEligible`, both with comments
explicitly naming the `ga-bwm` "drafted but not submitted" stall:

- `tmux.go:1680`: "leaving the text drafted but never submitted (ga-bwm). For
  providers with a [busy-check]…"
- `tmux.go:1705`: `func submitEnterAndConfirm(sendEnter func() error, wake func(), busy func() (bool, error), sleep func(time.Duration)) (bool, error)`
- `tmux.go:1742-1746`: `submitVerifyEligible` gates the retry loop to providers
  whose busy signal is trustworthy — "(the confirmed ga-bwm failure)."
- `tmux.go:1766-1821`: `NudgeSession` calls `submitEnterAndConfirm` when
  `submitVerifyEligible(target)` is true.
- Dedicated test coverage: `internal/runtime/tmux/nudge_submit_confirm_test.go`
  and `nudge_submit_confirm_integration_test.go` (`TestSubmitEnterAndConfirmReEntersWhileIdle`,
  `TestNudgeSessionReEntersUntilSubmittedForClaude`) both assert the re-entry
  behavior by name against `ga-bwm`.

The reporter's self-correction is accurate: this half is fixed upstream and
should stay withdrawn. Good discipline on their part — but it also means the
one falsifiable claim in the whole issue was already retracted before filing.

### "discord launcher" — does not exist as a component

Searched the full tree for `discord` (case-insensitive, all extensions). The
only hits:

- `internal/webhookverify/discord.go` — Discord webhook **signature
  verification** only (HMAC/Ed25519 checks), no session-launch semantics.
- `contrib/openclaw-bridge/README.md` — unrelated contrib doc.

There is no "discord launcher" module, package, or code path. The nearest
real subsystem is `internal/extmsg/` (external message binding, delivery,
group membership — Discord would plug in via `TransportAdapter` /
`AdapterRegistry` in `internal/extmsg/adapter_registry.go`), but nothing in
that package is named or scoped the way the issue describes.

### "pending_thread" launches — string does not exist in the repo

`grep -rn "pending_thread"` across the entire checkout returns zero matches,
in code, tests, or docs. Whatever internal state the reporter's downstream
deployment tracks under that name, it isn't a concept this codebase
recognizes. Ask (d) can't be evaluated against source because there's nothing
to point it at.

### Ask (d) as stated ("a background reconciler for pending_thread launches") — closest real analog already exists

If ask (d) is read generously as "a background process that repairs
external-message session state after a respawn," that already exists and is
mature: `internal/extmsg/binding_reaper.go`.

- `ReapStaleBindings` (`binding_reaper.go:51-105`): re-points conversation
  bindings at a session's current live bead after a respawn, or clears them
  when no live session owns the name. Doc comment at `binding_reaper.go:27-50`
  describes exactly the failure mode the issue gestures at ("inbound routing
  resolves to the dead bead and silently drops").
- `ReapStaleParticipants` (`binding_reaper.go:118-223`): the group-membership
  analog, including a second pass for handovers that partially committed
  (`previous_session_id_pending_cleanup`, `binding_reaper.go:200-220`).

Both are explicitly documented as idempotent, tick-safe sweeps
(`binding_reaper.go:48-50`, `145-147`). This is a well-built reconciler for
the closest matching real concern. If the reporter's deployment needs
something this doesn't cover, the issue needs to say what "pending_thread"
actually maps to in their fork/config before anyone can act on it.

### Ask (a) — "readiness window > session cold-boot P99, OR event redelivery-on-ready"

No corresponding code path found. Searches for `readiness`, `cold-start`,
`cold-boot`, and `coldStart` surface only provider-lifecycle readiness
(`cmd/gc/init_provider_readiness.go`, `internal/api/handler_provider_readiness.go`)
— which governs whether an _agent provider binary_ is considered installed
and ready, not session cold-boot timing relative to event delivery. There is
no P99 tracking, readiness-window config, or redelivery-on-ready mechanism
anywhere in `internal/events`. Nothing to root-cause here without a concrete
trace showing an event arriving before a session was "ready."

### Ask (c) — "dead-letter alerting (fence mismatches are silent)"

**This one is real and verifiable.** See section 7 for the full plan.

---

## 2. Why "session event delivery is lossy" is a documented design choice, not (by itself) a bug

The issue's framing implies a defect. The actual code says this is
intentional, in the package doc itself:

> `internal/events/events.go:1-6`: "Package events provides tier-0
> observability for Gas City... Recording is best-effort: errors are logged
> to stderr but never returned to callers."

- `events.go:275-279`: `Recorder` interface — `Record(e Event)` returns
  nothing. Comment: "Records events. Safe for concurrent use. Best-effort."
- `internal/events/recorder.go:195-232` (`FileRecorder.Record`): doc comment
  "Errors are written to stderr — never returned." The flock-acquisition loop
  (`recorder.go:216-232`) drops the record entirely (after logging to stderr)
  if it can't get the lock within `recordFlockTimeout`, or if the recorder is
  closed (`recorder.go:206-208`).
- `internal/api/handler_extmsg.go:25-42` (`extmsgEmitEvent`): calls
  `ep.Record(...)` with no error check, consistent with the documented
  contract — this is not a bug in the extmsg wiring, it's using the API as
  designed.
- `cmd/gc/cmd_event_emit.go:52-56`: the `gc event emit` CLI command's own
  help text says it plainly: "Best-effort: always exits 0 so bead hooks never
  fail... the event bus does not acknowledge durable persistence."

There is also no `session.launched` / `session.created` event type in the
catalog at all (`events.go:20-52` lists only `SessionWoke`, `SessionStopped`,
`SessionCrashed`, `SessionDraining`, `SessionUndrained`, `SessionQuarantined`,
`SessionIdleKilled`, `SessionMaxAgeKilled`, `SessionSuspended`,
`SessionUpdated`). So the issue's specific claim — "a launch event vanished
(accepted, no queue trace)" — can't be mapped onto an existing event type
either. If the reporter's downstream deployment has a custom event type
named something like that, this issue needs to say so; as filed, there's
nothing in upstream `gascity` that emits a "launch event" to lose.

Changing the event bus to a durable/acked model (if that's what's actually
wanted) is a legitimate but large architectural proposal — it touches every
`Record()` call site in the codebase (dozens of files) and the doc comment
at the top of the package would need to change along with the interface
contract. That is not something to plan as a drive-by fix inside this issue;
it would need its own RFC given the blast radius.

---

## 3. Recommendation for the issue itself

Request the reporter supply, for each remaining "genuine" item:

- The literal event type name and a `.gc/events.jsonl` excerpt (or equivalent)
  showing the gap between "accepted" and "recorded," for the launch-event-loss
  claim.
- What "pending_thread" refers to concretely — a bead label, a session
  status enum value, a field name — since it doesn't exist under that name
  upstream.
- Whether "discord launcher" means the `internal/extmsg` inbound/outbound
  path (in which case, name it that) or something specific to their
  deployment's fork.

Given the batch-filing context (18 items, cross-referenced by placeholder
codenames not yet indexed), this specific issue reads as a placeholder that
was drafted before the investigation was finished, then partially
self-corrected in an edit. That's a reasonable norm for a findings bundle,
but it means issue #3972 isn't actionable yet outside the one item below.

---

## 4. Blast radius (scoped to the one grounded item — ask (c), see section 7)

- **Caller**: `cmd/gc/cmd_nudge.go` — `recordQueuedNudgeFailureWithStore` /
  `recordQueuedNudgeFailureDetailed`, called from two sites on fence
  mismatch: `cmd_nudge.go:470` (queued-nudge delivery path) and
  `cmd_nudge.go:1238` (a second delivery/reconciliation path).
- **Config path**: none — no new config surface, this only adds an event
  emission using the existing `events.Recorder` plumbing already threaded
  through `cmd/gc`.
- **Goroutine boundary**: none — `Record()` is synchronous and already
  documented as safe for concurrent use (`events.go:277`); no new
  goroutines introduced.
- **Cross-subsystem effect**: adds entries to `.gc/events.jsonl` (or the
  configured event provider) for dead-lettered nudges. Any downstream
  consumer of the event stream (SSE projection, `gc event` CLI readers,
  redacted exporter in `cmd/gc/event_export.go`) will start seeing a new
  event type — additive only, no existing event schema changes.
- **Test surface touched**: `cmd/gc/cmd_nudge_test.go` (already has a test
  at line 2952 exercising `recordQueuedNudgeFailureWithStore` with
  `errNudgeSessionFenceMismatch`; that test's fixture needs an
  `events.Recorder` wired through it to assert the new emission).

---

## 5. Fix candidates for ask (c) — dead-letter alerting

**Candidate A — emit an event at the dead-letter transition site (recommended).**
Add an `events.Recorder` parameter to `recordQueuedNudgeFailureWithStore` /
`recordQueuedNudgeFailureDetailed` (mirroring the pattern already used in
`cmd/gc/bead_worktree_reaper.go:23-34`, which threads `rec events.Recorder`
through and defaults to `events.Discard` when nil), and call
`rec.Record(...)` with a new `NudgeDeadLettered` event type whenever
`failedQueuedNudge` returns `dead == true` for the fence-mismatch cause
specifically (or more broadly for any terminal dead-letter, see open
question below).

- Pro: matches an existing, tested idiom in the same package family
  (`bead_worktree_reaper.go:108-145`, `events.BeadWorktreeReaped`/
  `BeadWorktreeReapSkipped` payloads). Small, additive diff.
- Con: still best-effort (per the documented `Recorder` contract) — an
  event-bus outage means the alert is lost too, same as the underlying
  concern in section 2. This is an acceptable trade-off for a first pass:
  it moves dead-letter visibility from "must run `gc nudge status`" to
  "shows up in the same event stream everything else does," without
  taking on a durable-delivery redesign.

**Candidate B — active push notification (e.g., mail/Slack alert) on dead-letter.**
Wire `recordQueuedNudgeFailureWithStore` to also enqueue a `gc mail` message
or webhook call when a nudge dead-letters.

- Con: much larger blast radius — introduces a new cross-subsystem
  dependency (mail delivery, which itself can fail) into a code path that
  today only touches the beads store. Conflates "observability signal" with
  "notification policy," which per this codebase's ZFC-adjacent layering
  should be a separate concern (something subscribing to the event stream
  decides how to alert a human, not the CLI command itself).
- Rejected for this pass. If proactive paging is genuinely wanted, it
  should consume the event added in Candidate A rather than be built
  directly into `cmd_nudge.go`.

**Pick: Candidate A.** It is the minimal, idiomatic change that closes the
verified gap (no event today; other reapers in the same package already
emit one on a comparable terminal-state transition) without expanding scope
into notification-channel design, which isn't specified by the issue anyway.

---

## 6. Open question to resolve before coding (flag to maintainer, don't guess)

`failedQueuedNudge` (`cmd_nudge.go:2131-2147`) dead-letters on **two**
distinct causes: fence mismatch (`errNudgeSessionFenceMismatch`) and
exhausted retries / expiry (`item.Attempts >= defaultQueuedNudgeMaxAttempts`
or `item.ExpiresAt` passed). The issue only asks about fence-mismatch
visibility specifically ("fence mismatches are silent"). Emitting the event
only for the fence-mismatch branch is truer to the ask; emitting it for
every dead-letter cause is more useful operationally but slightly exceeds
what was requested. Recommend emitting for **all** dead-letter causes with
the cause recorded in the event payload (`item.LastError` is already
populated at `cmd_nudge.go:2134`) — narrower scoping just to fence-mismatch
would need a second, separate event type later when someone inevitably asks
about the other cause, which is worse long-term than one event type with a
`reason` field from the start.

---

## 7. Step-by-step implementation (ask (c) only)

1. **Add the event type.** In `internal/events/events.go`, add
   `NudgeDeadLettered = "nudge.dead_lettered"` to the const block
   (`events.go:20-52`), following the existing naming convention
   (`bead.claim_rejected`, `session.max_age_killed`).
   - Verify: `go build ./internal/events/...`

2. **Add a payload type.** In `internal/events/payloads.go` (or
   `supervisor_payloads.go`, whichever holds the closest sibling shape —
   check `BeadWorktreeReapedPayload` for the pattern), add
   `NudgeDeadLetteredPayload{ NudgeID, SessionName, Cause, Attempts string/int }`
   with an `IsEventPayload()` marker method matching the sealed-interface
   convention (`internal/extmsg/events.go:24` shows the pattern:
   `func (InboundEventPayload) IsEventPayload() {}`).
   - Verify: `go build ./internal/events/...`

3. **Thread an `events.Recorder` into the dead-letter path.** Add a
   `rec events.Recorder` parameter to `recordQueuedNudgeFailureWithStore`
   and `recordQueuedNudgeFailureDetailed` (`cmd_nudge.go:2034-2044`),
   defaulting to `events.Discard` at call sites that don't have one yet
   (mirror `bead_worktree_reaper.go:27,34`).
   - Verify: `go build ./cmd/gc/...` (compile error surfaces every call
     site needing an update — there are exactly two, `cmd_nudge.go:470`
     and `cmd_nudge.go:1238`).

4. **Emit on the dead-letter transition.** Inside
   `recordQueuedNudgeFailureDetailed`, after `failedQueuedNudge` returns
   `dead == true` for an item, call
   `rec.Record(events.Event{Type: events.NudgeDeadLettered, Subject: item.ID, Payload: <marshaled payload>})`
   before the loop continues to the next id. Marshal errors should log to
   stderr and continue (matching the `extmsgEmitEvent` pattern at
   `handler_extmsg.go:31-35`), not abort the dead-letter bookkeeping itself
   — the event is observability, not the source of truth.
   - Verify: add a unit test (see section 8) and run
     `go test ./cmd/gc/... -run TestRecordQueuedNudgeFailure -v`.

5. **Wire the two call sites** (`cmd_nudge.go:470`, `cmd_nudge.go:1238`) to
   pass the command's already-available event provider/recorder (check how
   `cmd_session.go` or `cmd_runtime_drain.go` obtain theirs from the
   `cityRuntime`/`api_state.go` wiring — same pattern applies here).
   - Verify: `go build ./cmd/gc/...` and `go vet ./cmd/gc/...`.

6. **Update `gc nudge status` help text** (`cmd_nudge.go:257-258`) to
   mention that dead-letter transitions are now also recorded to the event
   log, so operators know they don't have to poll.
   - Verify: `go run ./cmd/gc nudge status --help` shows updated text.

---

## 8. Test strategy

Ships in the same commit as the source change.

- **Unit test** (extend `cmd/gc/cmd_nudge_test.go`, near the existing test
  at line 2952 that already exercises
  `recordQueuedNudgeFailureWithStore(dir, beads.NudgesStore{Store: store}, []string{item.ID}, errNudgeSessionFenceMismatch, time.Now())`):
  pass a fake `events.Recorder` (the package already has `events.Fake` per
  `internal/events/fake.go:34` used throughout `events_test.go`) and assert:
  - Exactly one event is recorded with `Type == events.NudgeDeadLettered`.
  - The event's `Subject` equals the dead-lettered nudge's ID.
  - The payload decodes to include `Cause` matching
    `errNudgeSessionFenceMismatch.Error()`.
  - A **non**-dead-lettering call (retry path, `failedQueuedNudge` returns
    `dead == false`) records **zero** events — this is the regression guard
    that prevents over-firing on every retry attempt.

- **Existing regression coverage to keep green:**
  `cmd_nudge_test.go:2952` (dead-letter transition via fence mismatch) and
  `cmd_nudge_test.go:3468` (`failedQueuedNudge` terminal-state assertion)
  must still pass unmodified in their existing assertions — the new
  recorder parameter should default through `events.Discard` for any test
  call site not updated, so this is a compile-time forcing function, not a
  behavior change for callers that don't care about events.

- **Integration check:** run the full `cmd/gc` package test suite
  (`go test ./cmd/gc/... -run TestNudge`) to confirm no other dead-letter
  code path silently regresses from the new parameter.

---

## 9. Risks a maintainer would flag, and how this plan pre-empts them

- **"This changes a widely-called function's signature."**
  Pre-empted: only two production call sites exist
  (`cmd_nudge.go:470`, `:1238`), both inside the same file; the compiler
  finds every one, and step 3 verifies via build before any logic changes.

- **"Best-effort event emission doesn't actually solve 'silent failures.'"**
  Acknowledged directly in section 5 — this is a known trade-off inherited
  from the existing `Recorder` contract (section 2), not something this
  change can or should fix. Framed honestly in the PR description rather
  than oversold as a durability guarantee.

- **"Why not fix the whole event bus to be durable instead?"**
  Out of scope for a single-issue fix — changing `Recorder.Record`'s
  contract touches every emission site in the codebase (dozens of files,
  per the grep in section 2) and the package's own doc comment. That's an
  RFC-sized change, flagged here so the maintainer doesn't expect this PR
  to solve it.

- **"Does emitting for both dead-letter causes (fence mismatch + exhausted
  retries) scope-creep past what the issue asked?"**
  Addressed head-on in section 6 with the reasoning for going slightly
  broader — a `reason`/`cause` field on one event type beats a second event
  type shipped later for the other cause. If the maintainer disagrees,
  narrowing to fence-mismatch-only is a one-line change to step 4's
  condition.

- **"No test for the two real call sites, only the helper function."**
  Pre-empted by the integration check in section 8 running the full
  `TestNudge*` suite, which already exercises both call sites indirectly
  through their existing fence-mismatch and retry-exhaustion test paths.
