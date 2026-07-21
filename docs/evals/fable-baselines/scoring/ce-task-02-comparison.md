# CE Judge Report — Task 02 (implementation plan from issue #3972)

**Judge setup:** scored against the pinned read-only checkout at
`/tmp/fable-baseline-task02` (HEAD `ee616a7e41c74285d37283e9ca0022db120e9f14`,
confirmed). 15+ file:line citations spanning both candidates' root-cause and
blast-radius sections were spot-verified. No repo modifications.

**One-line verdict:** **X 3.40 (needs rework, borderline) vs Y 4.90
(golden-equivalent).** Y found and planned the extmsg member-notify silent-loss
path (asks a/d) that the golden run centers on; X declared that same path
non-existent after a keyword search and planned only ask (c).

---

## The load-bearing divergence

The issue bundles a self-withdrawn claim (b), phantom vocabulary
("discord launcher", "pending_thread"), and three asks: (a) readiness-window /
redelivery-on-ready, (c) dead-letter alerting for silent fence mismatches,
(d) a background reconciler. Both candidates verified (b) is already fixed and
both flagged the phantom vocabulary. They split on asks (a)/(d):

- **X** searched `internal/events` for `readiness`/`cold-boot`/`pending_thread`,
  found no literal matches, and concluded "**No corresponding code path
  found**" for (a) and that ask (d)'s "closest real analog already exists"
  (`ReapStaleBindings`) and is "mature." X therefore planned **only ask (c)**
  and sent the rest back for a repro.
- **Y** reframed the phantom vocabulary onto real concepts and found the actual
  silent-loss mechanism: `extmsgNotifyMembers` is spawned **detached** from the
  HTTP response (`huma_handlers_extmsg.go:92`, `:170`), and its two failure
  points are `log.Printf`-only with no event, metric, or durable marker
  (`handler_extmsg.go` "notify %s failed" / "resolve session %s failed"). Y
  mapped ask (a)'s "readiness window vs cold-boot" onto the real
  materialize-race in that path and ask (d) onto the missing notify-retry
  reconciler.

**Repo adjudication of the split:**

- `handler_extmsg.go` — `extmsgNotifyMembers` exists at `:128`; `notifyResolved`
  closure at `:161`; `log.Printf("extmsg: notify %s failed", …)` at `:176`;
  `log.Printf("extmsg: resolve session %s failed", …)` at `:198`. Both
  go-detached call sites at `huma_handlers_extmsg.go:92` and `:170` with
  `s.backgroundCtx()`. **Y's mechanism is real and precise** (Y cited
  `:172-173`/`:192-196`, off by ~4 lines; every symbol lands). This is the exact
  path the rubric's golden anchor #2 quotes (`:176` "notify %s failed", `:198`).
- `internal/extmsg/binding_reaper.go:27-50` — `ReapStaleBindings` doc comment:
  it re-points/clears conversation **bindings** against live session identity.
  It does **not** retry member **notifications**. `grep` for any notify-failure
  marker (`NotifyFailed`, `pending_notify`, …) across `internal/extmsg` and
  `internal/api` returns **zero hits**. **Y's claim that the existing reaper
  does not cover notify retries survives the checkout; X's dismissal ("mature,
  covers it") does not.** X conflated "a reconciler for extmsg binding state
  exists" with "the notify-loss gap is covered" — different concerns.

X's section-2 argument that "lossy delivery is a documented design choice"
correctly describes the `internal/events` best-effort contract
(`events.go:1-6`, verified) — but that is the wrong subsystem. Asks (a)/(d) live
in `internal/api/handler_extmsg.go`, which X never examined for this. X used an
adjacent-but-wrong code fact to argue a real gap out of existence.

---

## Candidate X

```
D1 evidence-grounding:   4 — citations real and verified; one falsifiable structural error
D2 premise-checking:     3 — per-claim verdicts, but (a)/(d) verdicts don't survive the checkout
D3 coverage:             3 — boundary types named, but misses a whole subsystem + registry + lock + undercounts call sites
D4 decomposition:        3 — two candidates, B partly a strawman; single-unit; no deferred real work
D5 verification:         4 — concrete runnable checks; discriminating zero-event regression guard
D6 executability:        3 — decisive but the call-site undercount and missing registry step would trip an engineer
D7 calibration:          4 — genuinely repo-specific risks tied to plan sections; honest on best-effort limit
Caps applied:            none (no fabrication; no invalid-premise fix; no circular-check cluster)
Weighted overall:        3.40 → needs rework (borderline acceptable)
Judge confidence:        high — checkout available, all sampled citations verified
```

**What X gets right.** Real, verified investigation — not a fabricator.
`events.go:1-6` best-effort doc quoted exactly; `bead_worktree_reaper.go:27-34`
(`rec events.Recorder`, `if rec == nil { rec = events.Discard }`) is a genuine
precedent for the recommended fix; `cmd_nudge_test.go:2952`
(`recordQueuedNudgeFailureWithStore(… errNudgeSessionFenceMismatch …)`) is exact;
`extmsgEmitEvent` at `:25`/`ep.Record` at `:36` verified; `events.Discard` at
`:324` and `events.Fake` (fake.go:14) exist. Ask (c) is a genuine gap and X's
plan for it is idiomatic and mostly executable. Absence claims
(`grep pending_thread` → zero, "no `session.launched` event type") are the mark
of real searches.

**What breaks it.**

1. **Call-site undercount (D1/D3/D6).** X §4 and step 3 claim
   `recordQueuedNudgeFailureWithStore` has "**exactly two**" call sites
   (`cmd_nudge.go:470`, `:1238`) and that "the compiler finds every one." The
   checkout shows **four** (`:470`, `:516`, `:1238`, `:1281`) plus the wrapper at
   `:2031`/`:2035`. X's own phrasing "two sites **on fence mismatch**" is correct
   (`:470` and `:1238` are the fence-mismatch sites) — but X's plan threads a new
   `rec events.Recorder` param **through the function**, which surfaces all four
   sites. X's executability claim is falsified by a build.
2. **Missing registry registration (D3/D6).** Step 1 adds the new event const to
   "the const block" and stops. The code shows event types must also be added to
   the `KnownEventTypes` slice (`events.go:237-245`, e.g. `WebhookReceived,
WebhookRejected` at `:240-241`) or downstream consumers drop them. X never
   mentions the slice; an engineer following X ships an unrecognized event.
3. **Missing lock-ordering constraint (D3).** X's emission step (4) fires
   `rec.Record` inside `recordQueuedNudgeFailureDetailed` without addressing the
   `withNudgeQueueState` flock (`cmd_nudge.go:2065`) the transition runs inside.
4. **Mis-scoped premise (D2).** Declaring asks (a)/(d) ungrounded is the central
   miss: a senior executing X's plan ships ask (c) but never touches the
   extmsg-notify silent-loss the issue is actually about.

---

## Candidate Y

```
D1 evidence-grounding:   5 — dense, exact, falsifiable; verified across 12+ spot-checks incl. quoted literals + absence claims
D2 premise-checking:     5 — distinct §0, per-claim verdicts, phantoms translated to real concepts, (b) excluded from scope
D3 coverage:             5 — organized by boundary type; second-order effects (registry pairing, lock-order, double-wrap) caught pre-implementation
D4 decomposition:        5 — three architecturally distinct candidates; B rejection non-transplantable; C deferred/named
D5 verification:         5 — runnable per-step checks, symptom-bound regression test + discriminating control, baseline classification
D6 executability:        4 — decisive and precedent-anchored; two-commit scope leaves a few mechanical spots (new field shape) with modest latitude
D7 calibration:          5 — 7 step-linked repo-specific maintainer objections + adoption-review (B11-B33) anticipation
Caps applied:            none
Weighted overall:        4.90 → golden-equivalent
Judge confidence:        high — checkout available, all sampled citations verified
```

**Verified citations (sample).** `extmsgNotifyMembers` structure + both failure
points (within ~4 lines); go-detached call sites `:92`/`:170` (exact); events
extmsg consts `:148-149` + `KnownEventTypes` slice `:237-245` + the
`WebhookReceived, WebhookRejected` pairing precedent (exact); `reapStaleExtmsg
Bindings` signature and "errors logged and swallowed, never stalls the loop"
contract (exact); `city_runtime.go:1213`/`:1220` reaper tick (exact);
`inbound.go:220` stale comment citing the **nonexistent** `gc transcript check
--inject` (exact — a subtle, real find); `StubConversationSink` E7 at `sink.go:
202-224` with `TODO(E7)` and "conversation sink not yet wired (E7)" (exact);
`tryDeliverQueuedNudgesByPoller` `:1211-1296`, fence-mismatch branch
`:1237-1241`. Minor line drift on a handful of citations, but every symbol lands
and does what Y says.

**Second-order effects Y caught that X did not.** (1) The events
const-**and**-registry pairing contract. (2) The `withNudgeQueueState` flock:
new emission must fire **after** the closure returns, mirroring the existing
post-lock warning pattern at `:2118-2123`. (3) The telemetry asymmetry — the
delivery-error branch already calls `telemetry.RecordNudge` (`:1274`) before
dead-lettering while the fence-mismatch branch (`:1238`) has none; verified. Y
uses that existing call as the precedent for the fix. (4) A double-wrap check on
`formatExtmsgNotifyReminder` (explicitly cleared).

**One blemish.** Y also states `recordQueuedNudgeFailureWithStore` has "2 call
sites" (`:1238`, `:1281`) — also an undercount (there are 4). But Y's fix emits
from **inside** `recordQueuedNudgeFailureDetailed` and adds telemetry at the
branch, so the undercount does not break Y's plan the way it breaks X's
signature-threading approach.

---

## Failure signatures

| #   | Signature                                       | X                                                                                                      | Y                                                                                                           |
| --- | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------- |
| 1   | Unverified/fabricated citations                 | No — verified                                                                                          | No — verified                                                                                               |
| 2   | Blast radius = edited function                  | **Partial** — misses the a/d subsystem, the registry slice, the flock ordering; undercounts call sites | No — boundary-typed, second-order effects covered                                                           |
| 3   | Circular verification steps                     | No                                                                                                     | No                                                                                                          |
| 4   | Fix planned for invalid premise                 | No — X refused to plan phantoms (correct discipline)                                                   | No — Y reframed phantoms onto verified real concepts                                                        |
| 5   | Alternatives not weighed                        | **Mild** — candidate B leans strawman (has some repo reasoning)                                        | No — three genuinely-weighed candidates; B non-transplantable                                               |
| 6   | Issue-echo root cause                           | **Inverse** — used the events-bus best-effort design to argue a real gap _out_ of existence            | No — root cause adds links the issue never mentions (self-heal via `UserPromptSubmit`; telemetry asymmetry) |
| 7   | Scope creep / unrequested machinery             | No — under-scoped, not over                                                                            | No — steps map to asks (a)/(c)/(d); C explicitly deferred                                                   |
| 8   | Tests ship later / assert mechanism not symptom | No — symptom-bound regression test, same commit                                                        | No — two symptom-bound regression tests with controls                                                       |

Neither candidate triggers a scoring cap. Signature 4 (the highest-severity,
reject-inducing one) fires for **neither**: X did not plan against phantoms, and
Y planned against verified real code, not the phantom vocabulary.

---

## Which is better, by how much, and specifically what

**Y is better by ~1.5 points (4.90 vs 3.40)** — the difference between
golden-equivalent and borderline-needs-rework.

**What Y does that X does not:**

1. **Finds the issue's actual core.** Y identifies the `extmsgNotifyMembers`
   detached-goroutine silent-loss (asks a/d) — the exact mechanism the golden
   run's own anchor examples quote — where X keyword-searched the wrong
   subsystem and declared "no corresponding code path found," then used the
   events-bus best-effort design to dismiss the gap.
2. **Reframes phantom vocabulary onto real concepts instead of stopping at
   "doesn't exist."** "pending_thread" → `ConversationMembershipRecord.
LastReadSequence` + the missing notify marker; "discord launcher" → the
   `/v0/extmsg/inbound` path + the deferred E7 `ConversationSink`. X noted the
   phantoms and stopped.
3. **Correctly distinguishes the existing reaper from the gap.** `ReapStale
Bindings` re-points bindings; it does not retry notifications (verified: zero
   notify-marker hits). X called that reaper "mature" and treated ask (d) as
   covered; Y found the real uncovered gap.
4. **Catches three second-order blast-radius effects X misses** (events
   const/registry pairing, the `withNudgeQueueState` flock ordering, the
   fence-mismatch-vs-delivery-error telemetry asymmetry) — two of which are
   latent bugs in X's own plan (unregistered event const; emission relative to
   the lock).
5. **Weighs genuinely distinct candidates** including a deferred-but-named
   out-of-scope feature (C), where X's candidate B leans strawman.

X's redeeming quality is discipline: it refused to plan the truly-nonexistent
discord-launcher feature and delivered a clean, idiomatic, mostly-executable
plan for the one ask it grounded (c). But it extended that discipline too far,
declaring two grounded asks ungrounded, and shipped a plan whose own
executability claim ("exactly two call sites") the checkout falsifies.
