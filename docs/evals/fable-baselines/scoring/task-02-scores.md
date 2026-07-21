# Task 02 — Scoring Report (issue #3972, implementation-plan rubric)

**Judge:** anonymous scoring against `rubrics/implementation-plan.md`. Candidates
scored blind (A/B/C), no authorship attribution. Checkout available and used:
`/tmp/fable-baseline-task02` @ `ee616a7e41c74285d37283e9ca0022db120e9f14`
(confirmed via `git rev-parse HEAD`), read-only, no mutating commands run.

## Headline

| Candidate | Weighted overall | Verdict band          |
| --------- | ---------------- | --------------------- |
| **C**     | **5.00**         | golden-equivalent     |
| **A**     | **3.85**         | acceptable            |
| **B**     | **3.50**         | acceptable (boundary) |

The single fact that separates the three: **is the extmsg member-notify path a
real loss vector, or is it recoverable by design?** C traced it to the pane
paste and found no recovery; A rejected the corresponding ask as "already
satisfied" on the strength of a recovery mechanism that **does not exist in the
checkout**; B never located the path at all. The checkout adjudicates in C's
favor unambiguously (evidence below).

---

## The decisive repo finding (drives the ranking)

The issue's live ask (a) is "readiness window > cold-boot P99, OR event
redelivery-on-ready" — i.e. _messages to a cold/booting session are lost_.

**What the code actually does** (`internal/api/huma_handlers_extmsg.go:92`):
after the durable transcript append succeeds, the handler fires
`go s.extmsgNotifyInboundMembers(s.backgroundCtx(), *input.Body.Message)` — a
fire-and-forget goroutine. That calls `extmsgNotifyMembers`
(`handler_extmsg.go:128`) → `sendBackgroundMessageToSession`
(`session_resolution.go:611`) → `handle.Nudge(...)` with **default wake**, which
cold-starts the runtime and pastes into the pane. **Every failure in
`extmsgNotifyMembers` is `log.Printf` only** — verified at `:146`
(ListMemberships), `:176` (`"extmsg: notify %s failed"`), `:202` (materialize).
Nothing is enqueued, retried, or dead-lettered.

**A's claimed recovery mechanism does not exist.** A rejected ask (a) as
"LARGELY ALREADY SATISFIED by design," asserting: _"the booting session pulls
unread entries via `gc transcript check --inject`"_ and _"the wake is
best-effort … HTTP handler calls `state.Poke()`."_ Both are false against this
checkout:

- `grep 'Poke()' internal/api/huma_handlers_extmsg.go internal/api/handler_extmsg.go internal/extmsg/inbound.go` → the **only** hit is the _comment_ at `inbound.go:219` ("Wake is handled by the caller (HTTP handler calls state.Poke())"). The actual normalized-inbound handler calls `extmsgNotifyInboundMembers`, **not** `Poke()`. A quoted a stale comment and treated it as operative behavior.
- **There is no `gc transcript` command.** `ls cmd/gc/ | grep -i transcript` → none. Searching `--inject` across the tree returns only `gc nudge drain --inject` and `gc mail check --inject` (hooks); there is no `gc transcript check` anywhere. The recovery pull A leaned on to reject a real ask is a phantom.

C's root cause is what the code shows; A's rejection of ask (a) is built on a
mechanism that isn't there. This is exactly the rubric's strongest negative
signal ("a confident claim that does not survive the checkout").

---

## Repo checks performed (shared across candidates)

- `git rev-parse HEAD` → `ee616a7e4…` ✓ matches pinned SHA.
- `grep -rn pending_thread` → **zero hits** ✓ (all three claim this; correct).
- `huma_handlers_extmsg.go:92` = `go s.extmsgNotifyInboundMembers(...)` ✓ (C exact).
- `handler_extmsg.go` `log.Printf` failure sites: `:146`, `:176` "notify %s failed", `:202` materialize, plus resolve-session fail ~`:198` ✓ (C's `:146/:176/:198` land exact/near-exact).
- `sendBackgroundMessageToSession` at `session_resolution.go:611`, calls `handle.Nudge` default wake ✓ (C exact).
- `Poke()` on inbound path → **only in the comment**, not called ✗ for A.
- `gc transcript check --inject` → **no such command exists** ✗ for A.
- `cmd_nudge.go`: `errNudgeSessionFenceMismatch`=`:88`; discard `_ = recordQueuedNudgeFailureWithStore` at `:470` and `:516`; `:1238` fence record; `failedQueuedNudge`=`:2131` (two-cause: fence → instant DeadAt; else attempts/expiry); `queuedNudgeMatchesTargetFence`=`:1616`; `pruneDeadQueuedNudges`=`:2227` w/ `defaultQueuedNudgeDeadRetention=1h`=`:41`; `defaultNudgePollStartGrace=5min`=`:57`; `nudgeObservationBusy`=`:820`; gco-90ui precedent obs.Running check `:746`; `enqueueManagedNudgeThenWake`=`:846` — **all verified exact** (cited by A and C).
- `tmux.go`: `submitEnterAndConfirm`~`:1705` (`submitEnterMaxSends` loop `:1707`); `NudgeReadyTimeout=10s` (`:55`/`:80`); `sendKeysLiteralWithRetry`=`:1644`, used `:1790`; `WaitForRuntimeReady`=`:2871`, **only non-test caller is `adapter.go:836`** ✓ (confirms C's "never called from the nudge path" absence claim).
- `chat.go`: `ensureRunning`=`:319`, `sendLocked`=`:703` ✓; **`SendLiveOnly`=`:796`, NOT `:733-751` as C cited** (C's one line-number slip; symbol real, behavior as described).
- `handle_lifecycle.go`: `Nudge`=`:299`, `createDeferredLocked`=`:384` ✓ (C exact).
- `internal/events`: best-effort `discardRecorder.Record`=`:328`; `RegisterPayload`=`payload.go:51`; sealed `IsEventPayload` pattern in `payloads.go` ✓ (B/C).
- `bead_worktree_reaper.go`: `rec events.Recorder`=`:27`, `rec = events.Discard`=`:34`, `BeadWorktreeReaped` payload `:138/:145` ✓ (B's precedent — exact).
- `cmd_nudge_test.go:2952` = `recordQueuedNudgeFailureWithStore(... errNudgeSessionFenceMismatch ...)`; `:3468` = `failedQueuedNudge(item, errNudgeSessionFenceMismatch, ...)`; `TestFailedQueuedNudge_DeadLettersFenceMismatch`=`:3461` ✓ (B's test citations — exact).
- A's §5b hydration finding: `transcript_service.go:68` returns `ErrHydrationPending`; `inbound.go:209` & `:272` swallow it (`if !errors.Is(err, ErrHydrationPending)`), then emit event + return success ✓ — **a genuine second-order drop A found and B/C did not.**
- A's Path-A/absence claims: `resolveLiveSessionByPathAlias`=`:413` (within A's `:386-436`) ✓; "every `@@` is Dolt SQL" → non-SQL `@@` hits ≈ none ✓.

More than 5 citations sampled per candidate spanning root-cause and
blast-radius. **Judge confidence: HIGH.**

---

## Candidate A

```
D1 evidence-grounding:   3 — dense, mostly-exact citations WITH quoted literals and verified
                             absence claims (grep 'events\.' cmd_nudge.go → 0 ✓; '@@'→Dolt-only ✓;
                             hydration finding transcript_service.go:68 ✓). BUT one materially wrong
                             BEHAVIORAL claim about real code: "booting session pulls unread entries
                             via `gc transcript check --inject`" + "HTTP handler calls state.Poke()"
                             — no such command, Poke() only in a stale comment. Rubric cap: one
                             materially-wrong behavioral claim → D1 ≤ 3. Sits at the cap (other
                             evidence is 5-quality).
D2 premise-checking:     3 — structurally 5-level: per-claim adjudication of (b)(c)(d)(a), detects
                             `pending_thread` absence, translates vocabulary. BUT the ask-(a) verdict
                             ("LARGELY ALREADY SATISFIED … Reject as framed") REFUTES A REAL CLAIM
                             using false evidence (phantom transcript-pull). Correct on b/c/d, wrong-
                             and-consequential on the primary ask → not a 4/5.
D3 coverage:             4 — strong current-state map (three paths A/B/C, §1) naming boundary types
                             (goroutine, HTTP 4xx/5xx contract, out-of-process adapter) + the
                             hydration second-order path (§5b). But the ACTIONABLE blast radius is
                             confined to the ask-(c) slice (one file cmd_nudge.go + test) because A
                             rejected the primary ask; misses the notify-path fix surface C covers.
D4 decomposition:        4 — two genuinely different candidates for ask (c) (log line vs events-bus
                             emission) weighed on repo-specific coupling ("0 matches" grep, rollback
                             hazard :2117-2122); PLUS hydration captured as an explicitly deferred
                             candidate w/ two fixes. Candidates 1&2 are close in scope; no strawman.
D5 verification:         5 — RED-first `go test -run TestNudgeDeadLetterLogged -count=1` (expect FAIL),
                             buffer assertion = observable consequence (line names nudge ID+cause),
                             once-per-death spam-guard test, full gate w/ baseline classification.
D6 executability:        5 — exact loop site (:2123-2127), exact fmt.Fprintf w/ fields, guard,
                             "confirm field names on nudgequeue.Item before referencing." Choice
                             resolved (picks candidate 1). Few open decisions in the chosen slice.
D7 calibration:          3 — good risk section (§8, 5 risks each tied to a step/test, repo-specific).
                             BUT headline calibration error: states "reject as framed" FLATLY on ask
                             (a) — confidence exceeds its (phantom) evidence.
Caps applied:            D1 ≤ 3 (materially wrong behavioral claim: nonexistent transcript-pull recovery).
Weighted overall:        3×.20 + 3×.15 + 4×.20 + 4×.15 + 5×.15 + 5×.10 + 3×.05 = 3.85 → acceptable
Judge confidence:        high — checkout available; the load-bearing recovery claim was directly falsified.
```

A is the deepest _investigation_ of the three (three-path map, the hydration
bug nobody else found, the densest correct citations) but its central diagnosis
mis-rejects the primary reported bug on a mechanism that isn't in the tree, then
scopes the delivered fix down to ask (c) only — the same small slice as B. The
slice itself is executed at a 5 level; the penalty is concentrated where it
belongs (D1/D2/D7).

## Candidate B

```
D1 evidence-grounding:   4 — citations verified are honest and exact where load-bearing
                             (submitEnterAndConfirm+ga-bwm; bead_worktree_reaper.go:23-34 events.Recorder
                             precedent; cmd_nudge_test.go:2952 & :3468; :470/:1238). Real searched-
                             absence claims (discord→webhookverify+contrib only; pending_thread zero;
                             no session.launched event type). No fabrications. Thinner than A/C, and
                             the core "delivery is lossy = events.go best-effort by design" points
                             evidence at the OBSERVABILITY bus, a different subsystem than message
                             delivery — accurate citation, wrong target. Not capped (no false claim).
D2 premise-checking:     3 — good phantom detection ("discord launcher does not exist as a component";
                             pending_thread zero; ask-d → binding_reaper.go analog). BUT concludes ask
                             (a) has "no corresponding code path found … Nothing to root-cause here" —
                             B FAILED TO FIND the extmsg notify path A and C both located, and mis-frames
                             the central claim as the events-bus design choice.
D3 coverage:             3 — ask-(c) blast radius (§4) names boundary types for the slice (goroutine,
                             config=none, cross-subsystem event-stream consumers: SSE, gc event,
                             event_export; State-interface layering). But NO delivery-path map at all
                             and misses the primary loss surface. Narrowest coverage of the three.
D4 decomposition:        4 — two architecturally different candidates (in-process event via events.Recorder
                             vs cross-subsystem mail/slack push, rejected on ZFC-adjacent layering +
                             "mail can fail"); thoughtful §6 open-question (fence-only vs all causes).
                             Linear 6-step list, no commit/phase boundaries.
D5 verification:         4 — per-step verify present; §8 asserts observable consequences (exactly one
                             event Type==NudgeDeadLettered, Subject==nudge ID, Cause matches; AND a
                             non-dead-lettering call records ZERO events = the over-firing regression
                             guard). Some verifies are compile-only `go build`; step-6 "--help shows text"
                             is weak. Names existing tests to keep green.
D6 executability:        3 — names files/symbols/precedent, but leaves load-bearing decisions open:
                             "payloads.go (or supervisor_payloads.go, whichever holds the closest sibling)"
                             and "check how cmd_session.go or cmd_runtime_drain.go obtain theirs … same
                             pattern applies here" — the rubric's "similar to how other code does it"
                             3-tell; plus the fence-only-vs-all decision deferred to the maintainer. Two
                             engineers would produce different diffs.
D7 calibration:          3 — honest on the best-effort trade-off (§5/§9, not oversold); RFC-sizing of the
                             durable-bus change is well-judged. But overconfident absence conclusion on
                             ask (a) ("nothing to root-cause") when B simply didn't find the code.
Caps applied:            none.
Weighted overall:        4×.20 + 3×.15 + 3×.20 + 4×.15 + 4×.15 + 3×.10 + 3×.05 = 3.50 → acceptable (boundary)
Judge confidence:        high — checkout available; B's absence conclusion on ask (a) directly contradicted
                             by handler_extmsg.go:128 / huma_handlers_extmsg.go:92.
```

B is the shallowest investigation — keyword-driven, concluding _absence_ where
real code exists, and re-framing "lossy delivery" as the events-bus's documented
best-effort contract (true about the wrong subsystem). Its ask-(c) plan is
nonetheless competent, precisely cites the existing tests and the
`bead_worktree_reaper` precedent, and is honestly scoped. It lands `acceptable`
essentially on the strength of the one slice it committed to, not on diagnosing
the issue.

## Candidate C

```
D1 evidence-grounding:   5 — dense, exact, falsifiable: quoted literal ("notify %s failed" at :176),
                             multiple VERIFIED absence claims that only a searcher writes ("cmd_nudge.go
                             contains zero references to the events package" ✓; WaitForRuntimeReady
                             "exists … never called from the nudge path" ✓ — only adapter.go:836;
                             pending_thread "zero hits" ✓). ~25 citations sampled land exact
                             (huma:92, handler_extmsg log sites, cmd_nudge :88/:470/:516/:1238/:2131/
                             :1616/:846/:820, tmux :2871/:1790, chat :319/:703, handle_lifecycle
                             :299/:384). One line-number slip: SendLiveOnly cited :733-751, actually
                             :796 — symbol real, behavior ("checks IsRunning, never boots") correct;
                             not fabrication, not a behavioral error, does not trip a cap. The 5-markers
                             (literals + verified absences + exact ranges) are all present.
D2 premise-checking:     5 — explicit §0 per-claim check: (b) confirmed already-upstream w/ evidence;
                             (c) NARROWED ("almost right" — dead letters ARE shown by gc nudge status
                             but pruned after 1h, so the operator-facing claim still holds); (d)
                             "`pending_thread` does not exist upstream … downstream-shim terminology,"
                             reframed to the real ConversationMembershipRecord.LastReadSequence concept
                             and deferred. Textbook 5-level premise adjudication.
D3 coverage:             5 — blast radius organized by boundary type: goroutine/shutdown, notify
                             internals with the SECOND-ORDER double-framing interaction
                             (formatNudgeInjectOutput at cmd_nudge.go:500-505 double-wraps if routed
                             through the queue — verified real), worker/session layers, queue machinery
                             (new producer), events payload registry (SSE/DecodePayload consumers),
                             the internal/api ↔ cmd/gc State-interface LAYERING seam, "config surface:
                             none," gc nudge status JSON shape unchanged. Names locks (flock), goroutines,
                             registries, serialized formats, import layering.
D4 decomposition:        5 — three candidates, architecturally distinct: A (durable delivery via the
                             EXISTING nudgequeue state machine — reuse, not new machinery), B (widen
                             readiness window in place, rejected with FOUR repo-specific costs incl. a
                             tie back to the reported symptom: "still fire-and-forget … a crash/restart
                             between accept and paste loses the event"), C (transcript-cursor reconciler,
                             explicitly deferred to a follow-up PR as the out-of-scope-but-real candidate).
                             Two commits split along ask/risk boundaries, each carrying its tests.
D5 verification:         5 — per-step `go test` with run-filters; the regression test asserts an
                             OBSERVABLE CONSEQUENCE ("EnqueueSessionNudge called with … an UNWRAPPED body
                             (no `<system-reminder>` substring) … the regression test for the reported
                             symptom"); byte-identical live path asserted; full-gate step with baseline
                             classification against the unmodified base commit; names existing suites.
D6 executability:        5 — names files, symbols, in-repo precedents (gco-90ui busy→queue rule
                             cmd_nudge.go:745-749; the exact enqueue helper enqueueManagedNudgeThenWake;
                             "do NOT re-implement enqueue in internal/api — invariants live in cmd/gc");
                             resolves ambiguity ("emit AFTER the withNudgeQueueState transaction commits,
                             never inside the flock"). Minimal open decisions.
D7 calibration:          5 — verified vs inferred separated; scope defended (d deferred w/ reason);
                             8 maintainer risks each bound to a plan step/test, all repo-specific
                             (live-latency, queue-flooding→supersede, double-wrap→ga-vs7 sanitizer guard,
                             api↔cmd layering, SSE payload registry, flock-emission ordering). Includes
                             the 5-level tell: "fence dead-letters will INCREASE — intended: that is
                             precisely the failure class ask (c) makes VISIBLE" (a previously-hidden
                             failure class surfacing, flagged proactively so it doesn't read as a regression).
Caps applied:            none.
Weighted overall:        5×.20 + 5×.15 + 5×.20 + 5×.15 + 5×.15 + 5×.10 + 5×.05 = 5.00 → golden-equivalent
Judge confidence:        high — checkout available; the load-bearing root cause, the WaitForRuntimeReady
                             absence, the double-framing second-order interaction, and the premise
                             greps all independently confirmed. One immaterial line-number slip noted.
```

---

## Comparative analysis

### D1 — evidence-grounding (C 5 / B 4 / A 3)

The gap is not citation _density_ (A is nearly as dense as C) but whether the
load-bearing claim survives the checkout.

- **C** pairs exact cites with _verified_ absence claims — "cmd_nudge.go contains
  zero references to the events package," `WaitForRuntimeReady` "exists … never
  called from the nudge path" (only `adapter.go:836`) — the fingerprints of a
  completed search. Both confirmed.
- **A** writes with equal confidence but stakes its central conclusion on
  behavior that isn't in the tree: _"the booting session pulls unread entries via
  `gc transcript check --inject`"_ — there is **no `gc transcript` command**, and
  the `Poke()` it invokes is only in a comment. This is the rubric's
  materially-wrong-behavioral-claim cap (D1 ≤ 3).
- **B** makes no false claim but points its evidence at the wrong subsystem:
  _"session event delivery is lossy" is a documented design choice_
  (`events.go:1-6` best-effort) — accurate about the **observability bus**, not
  the **message-delivery path** the issue's symptom ("accepted, no queue trace")
  describes.

### D2 — premise-checking (C 5 / A 3 / B 3)

All three adjudicate per-claim and all three correctly excise `pending_thread`
and the withdrawn ask (b). They diverge entirely on ask (a):

- **C:** _"the inbound extmsg pipeline makes acceptance durable but delivery
  ephemeral … Every failure in this function is `log.Printf` only. Nothing is
  enqueued, retried, or dead-lettered."_ — correct; ask (a) is real, planned.
- **A:** _"Ask (a) … LARGELY ALREADY SATISFIED by design … Reject as framed."_
  — refutes a **real** claim with false evidence.
- **B:** _"No corresponding code path found … Nothing to root-cause here without
  a concrete trace."_ — never found the path.

C's is the only verdict the code supports. A's is worse than B's on this axis
because A _affirmatively steers the maintainer away_ from a real bug ("reject"),
whereas B merely fails to find it and asks for a trace.

### D3 — coverage (C 5 / A 4 / B 3)

- **C** produces a boundary-typed blast radius including a second-order
  interaction a naive fix would ship as a bug: _"the queued drain path applies
  its own framing (`formatNudgeInjectOutput` … used at `cmd_nudge.go:500-505`),
  so routing the reminder through the queue risks double-framing. Must be
  handled."_ — verified real.
- **A** has an excellent _current-state_ map (three delivery paths) and the
  hydration second-order drop, but its _fix_ blast radius is one file because it
  rejected the primary ask.
- **B** has no delivery-path map and the narrowest fix surface.

### D4 — decomposition & fix selection (C 5 / A 4 / B 4)

All three field two-plus non-strawman candidates with repo-specific rejections
(A: log-line vs events-bus; B: event vs mail/slack push; C: durable-queue vs
widen-window-in-place vs deferred reconciler). C's rejected candidate carries
four repo-specific costs, one tying back to the reported symptom, and it splits
work into two test-carrying commits along ask boundaries — the only candidate to
decompose into independently shippable units. A and B remain linear step lists.

### D5 — verification (C 5 / A 5 / B 4)

A and C both write observable-consequence assertions (A: a substring must appear
in a buffer; C: a `<system-reminder>` substring must be _absent_, bound
explicitly to the reported symptom). B is close but leans on some compile-only
`go build` checks and a `--help` text check.

### D6 — executability (C 5 / A 5 / B 3)

A and C resolve their load-bearing decisions in-plan (A: picks candidate 1,
exact fmt.Fprintf; C: "emit after the transaction commits, never inside the
flock," "do NOT re-implement enqueue in internal/api"). B leaves two real forks
open — _"payloads.go (or supervisor_payloads.go, whichever …)"_ and _"check how
cmd_session.go or cmd_runtime_drain.go obtain theirs … same pattern applies"_ —
the rubric's exact 3-signature.

### D7 — calibration (C 5 / A 3 / B 3)

C states verified facts flatly, marks the deferred reconciler as inferred/scoped,
and proactively flags that its fix will make a hidden failure class _visible_
(fence dead-letters will rise — "intended"). A and B both carry a headline
calibration error: certainty on ask (a) that their evidence (phantom recovery /
un-found code) does not support.

### Failure signatures exhibited

- **A:** _issue-echo — inverted._ Not signature 4 (planning a fix for an invalid
  premise) but its mirror: **confidently rejecting a valid ask on a phantom
  recovery mechanism** (`gc transcript check --inject` / `state.Poke()` — neither
  operative). Also a mild signature 2 (fix blast radius = one edited file),
  mitigated by the strong §1 current-state map. Redeeming: found a real
  second-order bug (hydration-window drop) B and C both missed.
- **B:** signature 6 (issue-echo / mis-scoped root cause — reframes "lossy
  delivery" as the events-bus best-effort _design_, a different subsystem) plus a
  coverage miss (never locates the notify path; declares ask (a) un-actionable).
  Honest, no fabrication, competent slice plan.
- **C:** none material. One immaterial line-number slip (`SendLiveOnly`
  `:733-751` vs actual `:796`; symbol and behavior correct). Matches the golden
  anchors near-verbatim _and_ the anchored claims survive independent checkout
  verification.

---

## Rubric defects encountered

1. **No signature/cap for confidently rejecting a _valid_ ask on phantom
   evidence.** Failure signature 4 caps the case where a candidate _plans a fix
   for an invalid premise_ (over-inclusion) at D2 = 1 / verdict = reject. A
   committed the symmetric error — _refuting a real ask_ using a nonexistent
   recovery mechanism (under-inclusion) — which is just as harmful to a
   maintainer (it steers engineering effort away from a live bug) but attracts no
   dedicated cap. I handled it via the D1 materially-wrong-behavioral-claim cap
   (D1 ≤ 3) and by penalizing D2/D7 on the merits, but the rubric would be
   stronger with an explicit "false-negative on a verified-real ask" signature
   mirroring signature 4. Without the D1 cap, A's structural fluency would have
   over-scored it.

2. **Anchor examples are quoted verbatim from the golden run, and candidate C
   reproduces them near-verbatim.** This risks a judge rewarding C for
   _matching the anchors_ rather than for independent correctness. The rubric's
   caveat ("these anchor what a 5 looks like, not facts a candidate must
   reproduce") helps, but the overlap for C is near-total (the `pending_thread`
   reframe, the "every failure is `log.Printf` only" line, the double-framing
   finding, the widen-window rejection, and the unwrapped-body regression test
   all appear in both). I mitigated by scoring C's load-bearing claims against
   the checkout directly — they hold — rather than against the anchor text. A
   rubric under evaluation should probably note that anchor-verbatim overlap
   demands _extra_ independent verification, not less, precisely because the
   matching is so persuasive.

3. **The D1 "few lines" tolerance is unquantified.** C's `SendLiveOnly` slip is
   ~63 lines off — well outside "a few" — yet the symbol is real and its behavior
   correctly described, and C's citation profile (quoted literals + multiple
   verified absence claims) is unambiguously 5-level. The rubric offers no rule
   for "one imprecise cite amid an otherwise 5-level, verified-absence-bearing
   body," leaving the integer call to judgment. I scored D1 = 5 with the slip
   noted; a defensible judge could argue 4. A tie-breaker ("line-number
   imprecision on a correctly-described, real symbol does not cap when
   ≥N citations verify exact and ≥1 absence claim is confirmed") would remove the
   ambiguity.
