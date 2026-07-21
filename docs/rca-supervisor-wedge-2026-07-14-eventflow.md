# RCA — supervisor "wedge" (work stopped flowing) ~20:00–21:11 EDT 2026-07-14

_Owner: city-infra-pl. Requested by mayor (gc-487720). Restart at 21:11:50 EDT recovered it._

## Verdict

The mayor's prime suspect — **FileStore `.gc/beads.json` re-bloat (the dr-7smz8
Part-1 failure mode) — is REFUTED.** The store held **~35 MB through the whole
window** (backup `beads.json.bak-fsclose-20260715T001658Z` at 20:16 = 34.9 MB;
current 35 MB). Part-1 compaction is holding; the automated compactor
(`orders/beads-json-compact.toml`, `COMPACT_APPLY=1`, 24h) last ran 11:12 and the
store did not creep back over the following 10h. This wedge was **not** bloat.

The supervisor process **never hung**: the beads-cache reconcile loop logged every
minute continuously 19:40→21:33 (no silent gap), `/status` returned in <5 ms
throughout, and time-cadence orders kept firing.

## What actually starved: event delivery, not the process

Evidence is an **event-consumption** stall, not an order-scheduler stall:

- `help-request-surface` **fired on cadence** (20:28, 20:40, 20:50, 21:05, 21:19)
  yet carried a **247-deep `bead.updated` backlog** (mayor's observation at
  restart) — the consumer was not draining its event queue.
- `close-gate-reaper` (~1h cadence) skipped **one** occurrence: 20:05 → **21:17**
  (fired post-restart, "caught up missed occurrence").
- `approved-pr-automerge-packs` skipped one occurrence (20:37 → 21:03).

So order *firing* was only mildly late; the real symptom — "no rigs progressing" —
is the **dispatcher reacting slowly to newly-ready beads because event delivery
lagged**. Time-cadence orders don't depend on events, which is why they kept
firing while work stopped flowing.

## Prime remaining choke: the 198 MB active `events.jsonl`

`.gc/events.jsonl` is **198 MB** (grew from the 137 MB cited in dr-7smz8) with **23
`.gz` archives**. This is dr-7smz8 **Part 2** territory (large event history), and
it is the most plausible serializer for event delivery/consumption under
contention. The Part-2 fix in flight (`storehealth.LastMaintenance` ListTail,
commit f603de5c3 in a gascity worktree) addresses the `/status` read path but
**not** the active-file size itself — an in-floor lever for events-rotation
`maxSize` does not exist (gc-source internal, no env var on the supervisor unit).

## Chronic noise ruled out

`polecat-ui-stuck-scanner` fails `context deadline exceeded` every ~10 min and
`nudge-on-route` fails `exit status 5` — both recur **before and after** the
restart, so they are standing issues, not this wedge.

## What I could NOT prove (and the tool that now closes the gap)

The exact goroutine/mutex that serialized event delivery needs a **goroutine dump
taken during a live wedge**. That is now possible: a persistent drop-in
`~/.config/systemd/user/gascity-supervisor.service.d/pprof.conf` sets `GC_PPROF=1`,
so the supervisor binds read-only pprof on **127.0.0.1:6060** and it **survives
restarts** (verified responding, 176 goroutines). The dr-7smz8 diagnostic gap is
durably closed. **Next wedge: `curl -s 'http://127.0.0.1:6060/debug/pprof/goroutine?debug=2'`
before restarting** — that pins the mutex.

## Fixes

1. **[gc-source, surfaced]** Bound the event-history size or make the
   event-consumer read path not serialize with appends. Same class as the
   dr-7smz8 Part-2 fix but for the event *bus*, not `/status`. Needs the :6060
   goroutine dump from the next wedge to target precisely.
2. **[in-floor, DONE already]** Compactor IS automated (24h order, live) — the
   mayor's "compactor must be automated" ask is satisfied; confirmed not the
   cause here.
3. **[operational]** On the next "no work flowing" report, capture :6060 goroutine
   dump FIRST, then restart. Do not lose the evidence.

## Secondary: `gc session kill` did not respawn city-infra-pl (reset-pending)

Consistent with the same event/session-loop sluggishness; cleared on restart
(this session is the respawn). Root shares the goroutine-dump dependency above.
