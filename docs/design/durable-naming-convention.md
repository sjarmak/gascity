# Durable naming convention — idempotency keys + Workflow IDs

**Bead:** gc-4zf.6 (epic gc-4zf) · **Date:** 2026-07-21 · **Status:** adopted, forward-looking
**Helpers:** `services/temporal-maintenance/naming.go` (`OrderRunKey`, `MoleculeKey`, `WorkflowIDFor` + fixed-arity wrappers)

One naming convention for every durable-execution workload in this city,
adopted before a second workflow type exists. The first adopter is Track D
(pr-state-poller, gc-4zf.3); the maintenance-cycle pilot keeps its legacy
formats (see [Non-migration rule](#non-migration-rule)).

## The convention

Idempotency keys (per side effect, stored in the `KeyStore` exec ledger):

```
order/{scopedOrderName}/run/{scheduledTime}/{operation}
molecule/{rootBeadID}/{operation}
```

Workflow IDs (per orchestration episode):

```
gc/{cityID}/bead/{beadID}
gc/{cityID}/molecule/{moleculeID}
gc/{cityID}/order/{scopedOrderName}/{runKey}
```

Segment rules, enforced by the constructors:

- **No empty segments.** A blank input is a caller bug; fail fast, never emit
  a key with a hole in it.
- **No `/` inside a segment.** `/` is the reserved separator; a slash inside a
  segment would silently change the key's shape. Gc scoped order names are
  safe by construction (`Order.ScopedName()` renders `dolt-health` or
  `dolt-health:rig:demo-repo` — colons, never slashes).
- **No whitespace or control characters.** Keys land in logs, CLI arguments,
  and `KeyStore` file names.
- **Time formatting rule:** one rule for every time-bearing segment —
  convert to UTC, truncate to second granularity, render ISO 8601 basic with
  a trailing `Z` (`20260721T193000Z`). `FormatScheduledTime` is the single
  implementation; never hand-format. The input must be the Schedule's
  *nominal* fire time (or `workflow.Now` inside a workflow), never wall-clock
  time read in an Activity: a retry of the same fire must re-derive the
  identical key.

Identity sources: every segment derives from stable, re-derivable episode
identity — order name + nominal fire time, root bead id, city workspace name
(`pack.toml [pack]`, here `ds-research`). Never wall-clock "now", never
random data. This is the same discipline `idempotency.go` already states for
the legacy keys, generalized.

`{cityID}` exists so two cities sharing one Temporal namespace can never
collide on workflow IDs. `{kind}` (`bead` | `molecule` | `order`) names the
identity the episode is keyed off — per the epic's non-negotiables there is
no workflow-per-bead-lifetime; a workflow is one orchestration EPISODE keyed
off bead identity, and the bead remains the source of record.

## Why these two namespaces exist

**Cross-run dedup cannot ride on Temporal's message dedup.** Temporal
deduplicates a redelivered Signal/Update *within one workflow run* (request
identity is run-scoped). An external mutation that must happen at most once
*across* runs — a re-fired Schedule, a reset, a Continue-As-New chain, a
second episode touching the same bead — needs a key that outlives any single
run. That is what the idempotency key is: durable episode identity
(`order/…/run/{fireTime}/…`, `molecule/{rootBead}/…`) carried through the
Activity into the persisted `KeyStore`, so a worker crash, an Activity retry,
or a whole new run re-derives the same key and finds the existing claim
instead of double-firing.

**A stable workflow ID replaces hand-rolled dedup machinery.** With a
deterministic ID, a duplicate start is rejected server-side
(`WorkflowExecutionAlreadyStarted`) — there is no crash window between "did
the work" and "recorded that I did it". And the ID is a lookup handle: an
operator holding only a bead id can go straight to the episode's history
(`temporal workflow show --workflow-id gc/ds-research/bead/{beadID}`). The
Track D walkthrough (`durable-execution-walkthrough-pr-state-poller.md`)
shows the before/after: today's poller needs *two* idempotency mechanisms
(a cache file written after the sling, plus a title-string scan covering the
crash window between them) and both collapse into one workflow ID that
either exists or does not.

Keys and IDs are different lifetimes, hence different namespaces: a workflow
ID names an *episode*; an idempotency key names one *side effect inside* it.
One episode holds many keys; a key never outlives the semantic operation it
guards.

## Examples

```go
// Order episode: the 19:30 UTC fire of the rig-scoped dolt-health order.
wfID, _ := OrderRunWorkflowID("ds-research", "dolt-health:rig:demo-repo",
	FormatScheduledTime(fire)) // gc/ds-research/order/dolt-health:rig:demo-repo/20260721T193000Z
key, _ := OrderRunKey("dolt-health:rig:demo-repo", fire, "sling-selection")
// order/dolt-health:rig:demo-repo/run/20260721T193000Z/sling-selection

// Molecule episode keyed off its root bead.
wfID, _ = MoleculeWorkflowID("ds-research", "gc-4zf.3") // gc/ds-research/molecule/gc-4zf.3
key, _ = MoleculeKey("gc-4zf.3", "dispatch")            // molecule/gc-4zf.3/dispatch

// Bead-keyed episode (e.g. one pr-iterate review episode rooted on its bead).
wfID, _ = BeadWorkflowID("ds-research", "gc-9abc") // gc/ds-research/bead/gc-9abc
```

All constructors return an error on any segment violation; callers must not
paper over it (a malformed key is a dedup hole).

## Non-migration rule

The live maintenance-cycle formats do **not** migrate under this bead:

| Legacy producer | Live format |
| --- | --- |
| `workflow.go WorkflowID()` | `gascity-maintenance/{repo}/{cycleKey}` |
| `idempotency.go idempotencyKey()` | `temporal-shadow/{repo}/{cycleKey}/{branch}/{action}/{target}` |

Both are in production mid-soak (gc-372 clean-week, closing ~07-23). The
running Schedule addresses workflows by the legacy ID pattern, and the
persisted `KeyStore` exec records are filed under legacy keys — rekeying now
would orphan the live Schedule and void the at-most-once dedup history,
exactly the double-fire the keys exist to prevent.

So: **new derivation helpers only, zero changes to legacy producers or call
sites.** Migration (or a documented adapter that maps legacy keys into the
convention) is a separate change, gated on the soak closing, with its own
bead. `TestLegacyFormatsUnchanged` in `naming_test.go` pins the legacy
strings so accidental drift fails CI before it orphans anything. Until that
migration lands, the maintenance-cycle keys are grandfathered; every *new*
workflow type must use `naming.go` from its first commit.
