# Investigation: named session won't revive after circuit-breaker clear (#2714)

**Status:** root-cause investigation — NO fix committed. Halted at branch-ready
for maintainer/mayor decision. See "Why no fix is committed" below.

**Issue:** [#2714](https://github.com/gastownhall/gascity/issues/2714) — "named-session
circuit breaker cleared by kill but session still won't revive (creating→asleep
loop, no session file)". Labels: `kind/bug`, `priority/p1`.

## Symptom recap

A pinned/`always` named session crash-loops enough to trip the named-session
circuit breaker, then stays `asleep`. Operator runs `gc session kill <name>`,
which prints "cleared named-session circuit breaker" and exits 0. **The session
still never revives**: it cycles `creating`→`asleep` indefinitely, no session
file is ever written, `gc session logs` finds nothing, and neither the trace nor
the supervisor log surfaces a process-error. The wedge survives `gc session
reset` and a full `gc stop` + `gc start` with a fresh binary, while sibling
named sessions in the same city come up fresh and `active`. The wedged session
reappears "at its original creation age" — i.e. the **old session bead is reused**.

The reporter notes this is the same family as #1493 ("named-always post-churn
stays asleep") but worse: it survives a full restart and `gc session kill`.

## What the breaker-clear path actually does (confirmed)

`gc session kill` / `gc session reset` clear the breaker via
`clearPersistedSessionCircuitBreakerMetadata`
(`cmd/gc/session_circuit_breaker.go:737`). It writes empty strings for every key
in `sessionCircuitMetadataKeys` (`session_circuit_state`,
`session_circuit_restarts`, …) and **sets** `session_circuit_reset_generation`
to the bumped generation. `sessionCircuitBreaker.Reset`
(`session_circuit_breaker.go:497`) deletes the in-memory entry and advances the
reset generation.

Consequently, after a clear **and** after a full restart, the breaker
re-hydrates as CLOSED: `restoreFromMetadata` maps an empty
`session_circuit_state` to `circuitClosed` (`session_circuit_breaker.go:361`),
and `hasSessionCircuitMetadata` returns false. **So the circuit breaker itself is
not the persisted gate that survives the restart.** The clearing genuinely works,
exactly as the reporter observed.

## The persisted gate is the awake-set decision, not the breaker (primary hypothesis)

Whether the reconciler spawns a session is decided by `ComputeAwakeSet`
(`cmd/gc/compute_awake_set.go:106`). A bead is started only if
`decision.ShouldWake` becomes true, and the demand-driven path keys the desired
set by `bead.SessionName` (`compute_awake_set.go:351`). If the wedged bead is not
matched into the `desired` set for its runtime `SessionName`, `ShouldWake` stays
false, the reconciler never prepares a start candidate, and **no spawn is
attempted at all** — which is exactly why there is no session file, no
process-error, and no log line. The `creating`→`asleep` oscillation then comes
purely from the lifecycle projection / heal path, not from real spawn attempts.

This is the **same failure shape as #1493** (fixed in `fe5cb13be`,
"wake named-always sessions when configured_named_identity is missing"): in
#1493, `ComputeAwakeSet`'s named-always pass keyed `desired` by the wrong name
when a bead had lost its `configured_named_identity` tag, the
`bead.SessionName` lookup missed, `ShouldWake` stayed false, and the session was
"stuck asleep forever … even though `gc session pin` still unstuck it." #1493
added a conservative runtime-name fallback in `resolveNamedSessionBeadName`.

#2714 is explicitly a **different trigger** in this same family — one that the
crash-loop + breaker-trip leaves behind on the reused old bead. The trigger
must be **persisted on the bead** (it survives a full `gc stop`/`gc start`) and
must be something a freshly-created sibling bead does not have. Candidate
persisted markers on the post-crash-loop bead that could drop it out of the
awake match, ranked:

1. **A stale identity/name marker.** Like #1493 but a different clearing path:
   the crash-loop or breaker-trip path mutates `configured_named_identity` /
   `session_name` / `template` so the bead no longer matches `ns.Identity` *or*
   the runtime-name fallback `resolveNamedSessionBeadName` added in #1493.
   Confirm by diffing the wedged bead's `Metadata` against a fresh sibling's.
2. **A persisted `state`/`sleep_reason` that the awake pass treats as a hard
   blocker.** `pinBlockedByState` blocks pin-driven wake when
   `bead.State ∈ {suspended, closed}` or `bead.Drained`
   (`compute_awake_set.go:393`). A residual `suspended`/drained marker from the
   crash-loop teardown would silently suppress the pin override.
3. **`wait_hold`** left set on the bead — it suppresses demand-driven,
   attached, and pending wake (`compute_awake_set.go:352,359,365`).

## Confirmed secondary mechanism: silent breaker re-trip before spawn

Independent of the primary gate, there is a real silent-re-trip loop. On every
start-wave tick the reconciler calls `recordSessionCircuitBreakerRestart`
(`cmd/gc/session_lifecycle_parallel.go:2200`) **before** preparing the start
candidate. That appends a restart timestamp and **persists** the breaker
metadata to the bead (`session_circuit_breaker.go:649,658`). If the session
never records "progress" (`RecordProgress` — a bead state transition
attributable to the identity), then after `MaxRestarts` (default 5) ticks inside
the 30-min window the breaker **re-trips** (`recordRestartLocked`,
`session_circuit_breaker.go:253-260`) and the start is skipped via the
`state == circuitOpen` branch (`session_lifecycle_parallel.go:2212`). The
re-tripped `session_circuit_state=open` is persisted to the bead, so it survives
a restart. A subsequent `gc session kill` clears it, the loop repeats, and the
session never stays up long enough to clear via progress.

This re-trip explains "survives `gc session kill`" and "survives restart" for the
breaker dimension, but on its own it should still emit a `LogOpenOnce` ERROR on
each fresh trip — the reporter sees no such log. That points back to the primary
hypothesis: the spawn is never attempted because `ShouldWake` is false, so
`recordSessionCircuitBreakerRestart` is never reached and nothing logs.

## What is NOT the cause (ruled out)

- **"`pending_create_claim` is never cleared."** It is. The heal path clears the
  stale lease once it expires (`healStatePatchWithRollback`,
  `cmd/gc/session_reconcile.go:999-1004`, and the `failed-create` branch at
  `:974-980`). The `creating`↔`asleep` oscillation is the *expected* ga-mf1
  behavior, not a stuck claim. A patch that merely also clears
  `pending_create_claim` in the breaker-clear path would not fix the revive
  failure.

## Operability gap (reporter explicitly requests this)

"Nothing surfaces 'circuit breaker tripped' to the operator until `gc session
kill` happens to print it. An operator has no way to know *why* a pinned session
won't wake." Today the only signal is `LogOpenOnce`
(`session_circuit_breaker.go:477`), an stderr ERROR emitted once per OPEN
incident — invisible after the fact and absent entirely on the
`ShouldWake==false` path. A durable, queryable signal (an event on trip/re-trip,
and a reason exposed via `gc session list` / status hook for any named session
that is desired-but-not-waking) would make this class of wedge diagnosable
without a forceful kill. This is lower-blast-radius than the revive fix and is
independently shippable.

## Recommended next steps (reproduction-first)

1. **Capture a real wedged bead.** On a repro, dump
   `gc bd show <session-bead> --json | jq '.[0].metadata'` and diff it against a
   healthy sibling named session. The single differing key that drops it out of
   `ComputeAwakeSet` is the trigger.
2. **Write the missing revival test.** Existing tests
   (`cmd/gc/cmd_session_reset_test.go:27`, `:117`) only assert the breaker
   *clears* — none assert the reconciler actually *revives* (spawns) the
   session. Add a reconciler-level test: named-`always` bead carrying the
   post-crash-loop metadata shape, breaker cleared, drive the parallel start
   path, assert a session file / `active` state results. Mirror the harness in
   `TestReconcileSessionBeads_AlwaysNamedSessionWakesPostChurnWithMissingConfiguredIdentity`
   (the #1493 pin) and `session_lifecycle_parallel_test.go`.
3. **Fix at the awake-set match**, following #1493's conservative pattern (exact
   marker + template equality), once the trigger key is identified. Do not widen
   the match beyond the confirmed trigger.
4. **Ship the operability signal** (trip/re-trip event + desired-but-not-waking
   reason) as a separate, smaller change.

## Why no fix is committed

This is a p1 bug on a high-blast-radius concurrency path (the session
reconciler / awake-set / circuit breaker). The exact persisted trigger that
drops the wedged bead out of `ComputeAwakeSet` cannot be pinned by static
reading alone — #2714 is, by the reporter's own account, a *different* trigger
than #1493, and confirming it requires a captured wedged bead or a reproduction
harness. Committing a speculative awake-set change without a failing reproduction
test risks either not fixing the bug or destabilizing named-session wake for
every consumer. The investigation is committed here so the eventual fix can be
reproduction-driven and minimal.
