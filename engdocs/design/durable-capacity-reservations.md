---
title: "Durable Capacity Reservations"
---

| Field | Value |
|---|---|
| Status | Proposed; Stage 0 package is dark |
| Scope | Durable, city-local admission accounting before a workload is bound to a worker |
| Implementation | `internal/capacity` |
| Tracking | `gc-6mdz` |

## Purpose and sequencing

The capacity ledger prevents concurrent schedulers from admitting more work
than the configured agent, rig, workspace, or account capacity. Admission must
be durable because an in-memory check followed by a spawn can oversubscribe
after a scheduler restart or when two processes race.

Stage 0 lands only the dark `internal/capacity` package and its tests. Nothing
constructs the ledger from configuration, calls it from scheduling, exposes it
through CLI/API surfaces, or changes worker behavior. Later stages may add
configuration and bind the package to scheduler and recovery paths, each as a
separately reviewed contract change. This stage does not add speculative
wiring or make the dark package load-bearing.

## State machine

Each reservation occupies exactly one unit and is in one durable bucket:

```text
absent --Reserve--> held --Consume--> consumed --Release--> absent
                         |
                         +--expiry/Reclaim--> absent

held --Release--> absent
```

- `Reserve(workload, placement, caps)` is idempotent for the same workload and
  placement. A live reservation at a different agent, rig, or provider is an
  error; moving work requires release followed by a new reservation.
- `Consume(id)` records that binding committed. It is idempotent for an
  already-consumed ID and rejects an unknown or expired ID.
- `Release(id)` removes either bucket and treats an unknown ID as success, so
  an at-least-once recovery scan can safely repeat it.
- `Reclaim()` removes only expired held reservations and is idempotent.

The durable invariants are:

1. A workload has at most one live reservation.
2. A reservation appears in exactly one bucket.
3. Held and consumed reservations both count against all applicable caps.
4. A grant is committed only after every cap is rechecked under the ledger
   lock; selector output is advisory, not authority.
5. Account identifiers are opaque. Core code neither enumerates nor parses
   provider accounts.
6. State mutations serialize across processes and rewrite the complete state
   atomically. A failed mutation leaves the previous file intact.

## Expiry and crash recovery

The default held-reservation TTL is ten minutes. It covers only the short
interval between admission and binding. Expiry is inclusive: a hold is expired
when `now >= expires_at`. Reserve reclaims expired holds both before selection
and again before commit, so an abandoned hold does not need to wait for a
periodic sweep. An explicit `Reclaim` exists for a future recovery scan.

The crash cases are intentionally asymmetric:

| Crash point | Durable result | Recovery |
|---|---|---|
| Before `Reserve` commits | No reservation | Scheduling may retry |
| After `Reserve`, before binding | Held reservation | TTL plus `Reclaim` returns the unit |
| After binding commits and `Consume` succeeds | Consumed reservation | Future binding-lease recovery calls `Release` |
| Around `Release` | Reservation may or may not remain | Repeat `Release`; it is idempotent |

Consumed reservations never expire by the reservation TTL. Once binding
commits, the binding's lease is the authority for liveness; expiring consumed
capacity independently could admit replacement work while the original worker
still runs.

## Persistence and fail-closed behavior

State is city-local runtime data:

- state: `<city>/.gc/runtime/capacity/state.json`
- lock: `<city>/.gc/runtime/capacity/state.lock`

Both paths are resolved through `citylayout.RuntimePath`, so test/runtime path
overrides remain effective. The separate lock file is required because atomic
replacement changes the state file's inode. A transaction takes an exclusive
`flock`, reads state inside the lock, applies one mutation, sorts it
deterministically, and atomically replaces `state.json`.

A missing state file means the ledger has never been used and is empty. An
unreadable, empty, or invalid-JSON state file is corruption: load and every
dependent mutation return an error. The package must not rename, truncate,
ignore, or recreate such a file, because treating unknown occupancy as zero
could double-book every unit represented by it. Operators preserve the file
for diagnosis and repair it explicitly; scheduler integration must propagate
this error and decline admission. Snapshot also returns the error rather than
presenting an empty view.

## Selector process lifecycle

An optional `AccountSelector` delegates install-specific account choice to a
configured command. Without a selector, reservations have no account and the
account cap does not apply. With one, reservation follows three phases:

1. Under the ledger lock, reclaim, detect an existing reservation, precheck
   non-account caps, and collect saturated accounts.
2. Without the lock, run the selector. It receives `GC_ACCOUNT_EXCLUDE`,
   `GC_ACCOUNT_AGENT`, and `GC_ACCOUNT_PROVIDER` and returns one opaque token.
3. Under the lock, reload current state and recheck every cap, including the
   selected account, before committing.

The command runs through `sh -c` in a new process group with a 60-second
default timeout. Cancellation terminates the process group, waits briefly for
exit, and bounds the final wait. Empty output, multiple tokens, output over 128
bytes, start/exit failure, cancellation, and timeout all fail closed: no
reservation is written and no fallback account is guessed. The selector never
runs while the ledger lock is held, so slow policy or network-backed cache
refresh cannot block unrelated ledger transactions.

## Future binding relationship

The ledger is an admission primitive, not a binding store. A future binder
will carry the reservation ID into its durable workload-to-worker binding and
call `Consume` only after that binding commits. Terminal, retry, or expired
binding-lease recovery will call `Release` at least once. Recovery must derive
release eligibility from the durable binding, not from process presence or
the reservation TTL.

That integration must define ordering and compensation at the binding write
site. It is deliberately absent here: there is no metadata key, binding schema,
provider protocol, scheduler hook, watchdog scan, event, config field, or
public observability surface in Stage 0.

## Non-goals

- Choosing accounts in Go or teaching core code provider/account policy.
- Starting, stopping, or probing workers.
- Replacing pool cardinality, reconciler demand, or provider limits.
- Distributing capacity across cities or hosts; this ledger is one city only.
- Automatically repairing or discarding corrupt state.
- Expiring consumed reservations without consulting a future binding lease.
- Adding scheduler/binder/recovery wiring in the dark-package stage.
