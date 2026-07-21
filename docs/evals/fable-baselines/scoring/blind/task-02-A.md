# Task 02 — Issue #3972: "session event delivery is lossy and its failures are silent (gc-core / discord launcher)"

Checkout: `/tmp/fable-baseline-task02` @ `ee616a7` (`chore(deps): bump bundled bd CLI v1.0.4 → v1.1.0 (#4007)`), read-only.

## Verdict up front

**The issue's premise is substantially wrong as filed, and I do not recommend implementing it as written.** It is a hedged bundle-item that conflates two unrelated subsystems, is predicated on a data structure (`pending_thread`) that does not exist anywhere in the codebase, and asserts a "silent" failure mode (fence-mismatch dead-letters) that is in fact surfaced by a shipped CLI command. Two of its three live asks are non-actionable against this code. Exactly one ask — proactive dead-letter observability — maps to a real, narrow gap, and I plan that fully below. I also surface one genuine defect the issue gestures at but misdiagnoses (a live message silently dropped during the hydration window), which should be filed and fixed on its own, not under this bundle.

A maintainer should **request re-scoping** rather than accept #3972 whole. The plan below gives (1) the evidence that dismantles the bad framing, (2) a fully-executable plan for the one defensible slice, and (3) the separate finding to file.

---

## 1. Current-state map (what actually exists), with verified file:line evidence

There is no single "session event delivery" subsystem. There are **three distinct paths**, and the issue smears them together:

### Path A — raw signed webhook → _order_ dispatch (NOT a session)

`internal/api/handler_webhook.go:45` `handleHookProxy`: resolve webhook → perimeter (`:351`) → rate-limit (`:88`) → verify (`:137`) → Discord PING→PONG (`:165`,`:490`) → parse/match (`:170`,`:182`) → dedup (`:227`) → **dispatch an order** via `webhooksink.Route` (`:254`). The terminal action is firing an _order_, never waking a chat session. Discord signature auth is `internal/webhookverify/discord.go:60` (Ed25519 over `timestamp+rawbody`, replay window `:88-94`).

### Path B — external message inbound → _session_ (this is the "discord launcher" the issue means)

`POST /v0/extmsg/inbound` → `internal/api/huma_handlers_extmsg.go:60` → `extmsg.HandleInboundNormalized` (raw variant `HandleInbound`, `internal/extmsg/inbound.go:155`/`:237`). A Discord chat mention reaches a session here, posted by an **out-of-process adapter** after it verifies/normalizes. The single-`@` handle routing is `internal/extmsg/group_service.go:362` `ResolveInbound` (byHandle map → `GroupRouteExplicitTarget`), with fallbacks to `LastAddressedHandle` (`:373`) then `DefaultHandle` (`:379`). There is **no literal `@@handle`**; every `@@` in the repo is Dolt SQL (`SELECT @@datadir`).

Delivery model on Path B (load-bearing): the message's **durable record is the transcript append** (`inbound.go:195`, `:262`), and the wake is **best-effort**. The code comment states it directly (`inbound.go:218-220`):

> `// Wake is handled by the caller (HTTP handler calls state.Poke()). // Sessions discover unread entries via gc transcript check --inject.`

Cold-boot handling is real and deliberate: `resolveLiveSessionByPathAlias` (`internal/api/session_resolution.go:386-436`) accepts `{active, awake, none}` and **excludes** `asleep/draining/creating`; a still-booting (`creating`) session falls through to not-found _by design_ (`:400-402`): "sendBackgroundMessageToSession would deliver against an incomplete provider … Once the reconciler flips state=active, subsequent inbounds resolve." When no session is live, the handler **materializes and cold-wakes** one (`handler_extmsg.go:188-207` → `session_resolution.go:280` `materializeNamedSessionWithContext` → `Poke()` `:370`). The HTTP status contract (`huma_handlers_extmsg.go:62-72`) is the redelivery mechanism: transient store faults → 5xx so the adapter holds and redelivers; permanent → 4xx to drop. **Redelivery-on-ready is the adapter's job plus transcript-pull-on-boot, not an in-core event queue.**

### Path C — the nudge queue (`gc nudge`) — a _different_ subsystem

`internal/nudgequeue/` is a durable, flock'd `state.json` queue (`internal/nudgequeue/state.go:52-56`) with three persisted buckets `Pending / InFlight / Dead`, items carrying `Attempts, LeaseUntil, DeadAt, LastError` (`state.go:31-49`), persisted at `<city>/.gc/runtime/nudges/state.json`. **This** is where the "fence mismatch → dead-letter" mechanism the issue describes actually lives — and it is the CLI/supervisor nudge-dispatch path, unrelated to the Discord launcher (Path B).

The fence: `errNudgeSessionFenceMismatch` (`cmd/gc/cmd_nudge.go:88`); the key is `sessionID + continuationEpoch` (`queuedNudgeMatchesTargetFence` `:1616-1624`, stamped by `withNudgeTargetFence` `:1377-1404`); on drain, mismatches are split to `rejected` (`splitQueuedNudgesForTarget` `:1418-1432`) and dead-lettered (`recordQueuedNudgeFailureWithStore(..., errNudgeSessionFenceMismatch, ...)` `:1238`, terminal at `:2137`, `failedQueuedNudge` sets `DeadAt` `:2131-2135`).

## 2. Per-ask disposition

### Ask (b) [already withdrawn by filer] — Enter-fire verification. CONFIRMED already upstream in this checkout; nothing to do.

`submitEnterAndConfirm` exists at `internal/runtime/tmux/tmux.go:1705`: up to `submitEnterMaxSends = 3` (`:1685`) Enter re-sends, each gated on a pane-idle check (`busy` = `paneBusy`/`paneContainsBusyIndicator`, `:1711`,`:1734`,`:1739`), confirming `submitConfirmPollsPerSend = 4` polls @ `150ms` (`:1686-1687`). Eligibility is Claude-family only (`submitVerifyEligible` `:1746` → `ProviderFamily(provider) == "claude"` `:1748`), wired at `:1820`. The `ga-bwm` reference the filer cites is in-code at `tmux.go:1680/1744/1815`. The filer's own "RE-GRADE" is correct; this checkout postdates 1.3.3 and carries the fix. **No action.**

### Ask (d) — "background reconciler for `pending_thread` launches". NON-ACTIONABLE — the premise object does not exist.

`pending_thread` / `pending thread` / `PendingThread`: **zero matches repo-wide** (Go and non-Go). There is no such table, queue, or record to reconcile. What _does_ exist is the reconciler that already drives async session launch: `state.Poke()` "wake reconciler to start the agent" (`internal/api/handler_session_create.go:228`, definition `internal/api/state.go:169`). Asking for "a background reconciler for pending_thread launches" is asking to build a reconciler for a data structure that isn't there; the launch-reconciler it would duplicate already exists. **Cannot be implemented as specified. Reject; if the filer has a concrete lost-launch trace, it belongs in the hydration finding (§5), not here.**

### Ask (a) — "readiness window > cold-boot P99, OR event redelivery-on-ready". LARGELY ALREADY SATISFIED by design; no in-core event queue to add.

The durable delivery is the transcript row, and the booting session pulls unread entries via `gc transcript check --inject` (`inbound.go:218-220`); the reconciler is the readiness authority (`session_resolution.go:400-402`); transient faults already trigger adapter redelivery via 5xx (`huma_handlers_extmsg.go:62-72`). Adding an in-core "redelivery-on-ready event queue" would duplicate the transcript-pull mechanism and the adapter's retry contract — a net-positive-diff reinvention the architecture explicitly avoids. **Reject as framed.** The _only_ real loss window on this path is the hydration edge in §5, which is a targeted bug fix, not a new queue.

### Ask (c) — "dead-letter alerting (fence mismatches are silent)". PARTIALLY VALID — this is the one slice worth doing.

"Silent" is an overstatement (`gc nudge status <session>` reports `Pending/InFlight/Dead` — `cmd/gc/cmd_nudge.go:317` `cmdNudgeStatus`, counts at `:350-357`), but the **proactive** observability really is absent: `grep -n 'Emit|events\.|metric|alert' cmd/gc/cmd_nudge.go` → **0 matches**. A successful fence-mismatch dead-letter emits **no event and no log line** — the `:1239` "dead-lettering fence-mismatched nudges" string is only a wrapped error surfaced _if the bookkeeping itself fails_, and the `:2125` stderr warning fires _only_ when the terminal bead write fails, not on the normal dead-letter. So an operator only learns a nudge died by polling `gc nudge status`. That is a genuine, narrow observability gap. **Plan below.**

---

## 3. Root-cause hypothesis (for the one actionable slice, ask c)

Fence-mismatch dead-lettering (`cmd/gc/cmd_nudge.go` drain path, `recordQueuedNudgeFailureWithStore` at `:1238`; terminal transition in `failedQueuedNudge` `:2131-2135` and the loop at `:2123-2127`) transitions a nudge to the `Dead` bucket with `DeadAt`/`LastError` set, but has **no push-side signal**: no structured log at the moment of death, no events-bus emission, no metric. The only read surface is the pull-based `gc nudge status`. Root cause: dead-lettering was implemented as a pure state transition on the persisted queue, and observability was left to the status command, so failures are invisible unless an operator actively looks.

## 4. Blast radius (for the ask-c fix)

- **Files touched:** `cmd/gc/cmd_nudge.go` only (the drain/dead-letter path); a new `_test.go` sibling. No schema change to `internal/nudgequeue` (the `Dead` bucket and `LastError` already carry everything needed).
- **Callers / entry points into the dead-letter path:** the drain pass invoked by `gc nudge drain` (hidden, `:273`) and the supervisor/poller that drives draining. Adding a log/emit is on the drain path, so it fires wherever draining fires — verify it does not fire per-item in a hot loop that would spam (dead-lettering is comparatively rare: only fence mismatches and terminal failures reach it).
- **Goroutine boundary:** draining runs in the CLI/poller process, not the API server; there is no shared-state race introduced by adding a log line or a best-effort emit. If an events-bus emit is chosen, it must be **best-effort and non-fatal** (mirror the existing `bookkeepErr`/`nudgeWarningWriter` "must not abort delivery of remaining items" contract at `:1231-1235`).
- **Cross-subsystem:** none if scoped to a structured log line. If an events emission is added, it couples `cmd_nudge` to the events bus (currently zero coupling — see the 0-match grep); that is a new dependency edge and should be justified, not default.
- **Output-contract risk:** `gc nudge status` output shape and the `nudgeWarningWriter` stderr channel are consumed by operators/tests; a new log line must go to a channel that does not corrupt machine-readable status output.

## 5. Fix candidates and pick (ask c)

**Candidate 1 — structured log line at the dead-letter transition (RECOMMENDED).**
At the point a nudge is dead-lettered (the `deadLettered` loop `cmd/gc/cmd_nudge.go:2123-2127`, and the fence-mismatch record at `:1238`), emit one structured warning per newly-dead item to `nudgeWarningWriter` (already the established best-effort operator channel, `:97`), including nudge ID, target sessionID, continuationEpoch, cause (`item.LastError`), and `DeadAt`. Zero new dependencies, matches the existing best-effort logging idiom (`:1074`, `:877`, `:2125`), non-fatal by construction.

**Candidate 2 — events-bus emission (`events.Emit`) on dead-letter.**
Richer (dashboards/alerts can subscribe), but introduces the first `cmd_nudge → events` coupling (currently 0 matches), needs an event-type constant, an emitter injected into the drain path, and careful best-effort semantics so a bus failure never rolls back the authoritative dead-letter transition (`:2117-2122` warns against exactly this class of rollback). Larger blast radius.

**Pick: Candidate 1.** It closes the "operator never learns" gap with a net-minimal diff, reuses the exact channel and best-effort contract the surrounding code already uses, and adds no coupling. Candidate 2 is the right move only if the maintainer explicitly wants push-based alerting wired to the events bus — that is a product decision, not a bug fix, and should be its own scoped issue rather than smuggled in under #3972.

## 6. Step-by-step implementation (ask c, Candidate 1)

1. **RED — write the failing test first.** In `cmd/gc/cmd_nudge_test.go` (sibling, same commit), add a test that drives the drain path with a fence-mismatched queued nudge against a `nudgeWarningWriter` swapped to a `bytes.Buffer`, asserts the item lands in `Dead`, **and** asserts the buffer contains a line naming the nudge ID and cause. Model setup on the existing fence-mismatch tests (`cmd_nudge_test.go`, and the fence path exercised around `:1238`). Verify it fails: `cd /tmp/... && go test ./cmd/gc/ -run TestNudgeDeadLetterLogged -count=1` (expect FAIL — no line emitted today).
2. **GREEN — emit the line.** In the `deadLettered` handling (loop at `cmd_nudge.go:2123-2127`), add, before/after the terminal-mark, a best-effort `fmt.Fprintf(nudgeWarningWriter, "gc nudge: dead-letter: nudge %q target %s epoch %s: %s (at %s)\n", item.ID, item.SessionID, item.ContinuationEpoch, item.LastError, item.DeadAt.Format(time.RFC3339))` guarded by `nudgeWarningWriter != nil`, matching the `//nolint:errcheck` idiom already used at `:2125`. Confirm the exact field names on `nudgequeue.Item` (`internal/nudgequeue/state.go:31-49`) before referencing them. Verify: same `go test` command now PASSes.
3. **Guard against spam / double-log.** Confirm the emission is once per newly-dead item, not per drain pass over already-dead items — dead items are pruned by `pruneDeadQueuedNudges` (`:2224`) and the loop iterates only `deadLettered` (this pass's transitions), so it is naturally once-per-death. Add a test asserting a _second_ drain over the same now-dead item does **not** re-log.
4. **Full gate.** `make build && make check` (per repo `gascity-check` gates). If docs mention `gc nudge` observability, run `make check-docs`. Classify any failures as baseline vs. new.
5. **Slop pass.** Re-read the whole `deadLettered` block and the drain function end-to-end (not just the hunk); confirm the addition reads as if written from scratch and does not duplicate the `:2125` warning's intent.

## 7. Test strategy (ships in the same commit)

- **Regression unit test** in `cmd/gc/cmd_nudge_test.go`: fence-mismatched nudge → drain → assert (a) item in `Dead` bucket with `DeadAt` set and `LastError == errNudgeSessionFenceMismatch.Error()`, (b) `nudgeWarningWriter` buffer contains exactly one line naming the nudge ID and the fence-mismatch cause, (c) a repeat drain emits no further line. The buffer assertion is the load-bearing new coverage — it is what makes the previously-silent transition observable.
- Reuse the existing fence-mismatch fixtures rather than hand-rolling a session bead; grep `cmd_nudge_test.go` for the current fence-mismatch test and extend its arrange block.

## 8. Risks that would make a maintainer reject this, and pre-emption

- **"This is bundle-item scope-creep / conflates subsystems."** — Pre-empted by explicitly de-bundling: the PR fixes _only_ nudge-queue dead-letter observability (Path C), states in the body that Path A/B and asks (a)/(d) are separately dispositioned as non-actionable with the file:line evidence above, and does not touch the Discord launcher.
- **"`pending_thread` / redelivery-on-ready are the actual asks; you did something else."** — Pre-empted: the plan states, with evidence, that (d) targets a non-existent object and (a) is already satisfied by transcript-pull + adapter 5xx-retry; the PR delivers the one ask (c) that has a real gap.
- **"Log spam on every drain."** — Pre-empted by step 3's once-per-death test.
- **"Broke the machine-readable `gc nudge status` output."** — Pre-empted: emission goes to `nudgeWarningWriter` (stderr), not the status stdout path; test asserts status output is unchanged.
- **"Best-effort emit rolled back an authoritative dead-letter."** — Pre-empted: Candidate 1 only logs (no state write), and the code comment at `:2117-2122` documenting exactly this hazard is cited as the reason to prefer it over Candidate 2.

---

## 5b. Separate genuine finding — file on its own, do NOT bundle here

The issue's one concrete P2.8 claim ("a launch event vanished: accepted, no queue trace") has a real analogue the issue misdiagnoses. On the extmsg inbound path, when a conversation is `HydrationPending`, a **live inbound message is silently dropped**:

`internal/extmsg/inbound.go:208-216` (and `:271-277`): `Transcript.Append` returns `ErrHydrationPending` (`transcript_service.go:68` — live provenance rejected while `state.HydrationStatus == HydrationPending`); the handler **swallows it** (`// Hydration pending — transcript entry was not written.`), leaves `TranscriptEntry` nil, still emits the event, and returns `result, nil`. The HTTP handler then returns 2xx (`huma_handlers_extmsg.go:60-72`, success path), so the out-of-process adapter treats it as delivered and does **not** redeliver. Net effect within the (one-time, per-conversation) hydration window: message accepted with a 2xx, **no transcript row, no redelivery** — exactly "accepted, no trace."

This is a targeted bug (a dropped-live-message-during-hydration window), not the sprawling "lossy event delivery + silent failures + reconciler" framing of #3972. It deserves its own issue with: (a) a repro that forces `HydrationStatus == HydrationPending` and posts a live inbound, asserting the message is neither persisted nor redelivered; (b) a fix candidate — either return a retryable 5xx while pending (so the adapter redelivers after hydration completes) or queue the live message for replay on `HydrationComplete`. I flag it here as evidence but do not fold it into the ask-c PR, because mixing a delivery-semantics change into an observability PR is precisely the bundling that makes #3972 hard to review.

## Bottom line for the maintainer

Close/re-scope #3972 as filed. Actionable now: the ask-c dead-letter log line (§3–8, one file + test). File separately: the hydration-window drop (§5b). Reject with evidence: ask (d) (`pending_thread` does not exist) and ask (a) as framed (redelivery-on-ready already provided by transcript-pull + adapter 5xx-retry). Ask (b) is already fixed in this checkout.
