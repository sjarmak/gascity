# The resolved order fleet has no complete missed-schedule signal

_2026-07-20, `dr-s7am`, read-only census. No order was fired, no schedule or configuration changed, and no sensor was added._

`gc order list --json` resolves 160 active rows in the live city. The local `orders/` directory accounts for 111 of them; seven come from the built-in core pack, while two oversight-pack definitions expand into 42 global and rig-scoped rows. A file-only scan therefore omits 49 runtime rows and loses the scoped identity needed to assess the oversight fleet.

## The runtime census is 160 rows, not 113 files

The resolved list contains 120 distinct order names and 160 distinct scoped names. Every row was enabled at the time of the census.

| Source | Resolved rows | Distinct definitions | Examples |
|---|---:|---:|---|
| `/home/ds/gas-city/orders/` | 111 | 111 | `beads-health`, `morning-triage-cycle`, `dispatcher-liveness-sensor` |
| built-in core pack | 7 | 7 | `order-tracking-sweep`, `orphan-sweep`, `nudge-mail-sweep` |
| oversight-rig pack | 42 | 2 | `escalate-rollups` and `patrol-project-leads`, each expanded across the city and 20 rigs |

The trigger split is 86 cooldown, 45 cron, eight event, and 21 condition rows. The prior local-file assessment correctly counted 45 cron definitions, but its seven-event count was one short of the resolved fleet and it omitted all imported/scoped instances.

The stable inventory key must be `scoped_name`, not `name` or source basename. For example, `patrol-project-leads`, `patrol-project-leads:rig:aoa`, and `patrol-project-leads:rig:website` share one source file but have independent firing histories.

## Thirty-four resolved rows have no firing history

`gc order history --json` returned 32,033 retained entries and 114 base order names. Comparing its `(order, rig)` identity with the resolved census found 126 current rows with history and 34 without it: eight cron rows, 11 cooldown rows, and 15 condition rows.

The eight cron definitions without retained firing all have source mtimes before at least one host-local schedule slot:

| Order | Schedule (America/New_York) | Source mtime | First schedule slot after mtime |
|---|---|---|---|
| `ci-hardening-check` | `0 10 1,15 * *` | 2026-06-28 | 2026-07-01 10:00 |
| `codebase-audit-monthly` | `0 9 1 * *` | 2026-06-28 | 2026-07-01 09:00 |
| `issue-design-digest` | `0 14 * * 3` | 2026-06-28 | 2026-07-01 14:00 |
| `issue-stale-conservative` | `0 14 * * 2` | 2026-06-28 | 2026-06-30 14:00 |
| `issue-triage-incremental` | `0 13 * * 1` | 2026-06-28 | 2026-06-29 13:00 |
| `resource-creep-report` | `10 7 * * *` | 2026-07-07 | 2026-07-08 07:10 |
| `route-decide-report` | `30 9 * * 1` | 2026-05-28 | 2026-06-01 09:30 |
| `storage-ledger` | `20 7 * * *` | 2026-07-19 22:14 | 2026-07-20 07:20 |

These eight rows are zero-history candidates consistent with the cron bootstrap trap, not proof that a specific slot was missed. A source mtime does not establish when the running controller first resolved or enabled the definition; that classification requires a durable `first_seen` or historical resolved-inventory record. The check behavior still exposes the gap: outside the exact minute, a zero-history cron reports `cron: schedule not matched`. Cron orders with prior history are different because the trigger evaluator catches up elapsed minutes and reports `cron: caught up missed occurrence`.

The 11 never-fired cooldown rows are all rig-scoped `patrol-project-leads` instances, and `gc order check` exposes them as `never run`. The 15 condition rows without history are not failure evidence by themselves; a condition order should remain unfired while its source condition is false.

A live `gc order check --json` snapshot took long enough for the queue to move underneath it and still returned 52 due rows, including 11 `never run` cooldown instances and many cooldowns several multiples beyond cadence. That command is a useful operator view, but no existing consumer turns its result into a durable per-order missed-schedule signal.

## Existing signals cover attempts or narrow slices

| Signal | What it proves | What remains invisible |
|---|---|---|
| `gc doctor: order-firing-current` | On-demand per-order checks for resolved enabled cron/cooldown rows, including imported and scoped instances; reports never-fired, overdue, and stale rows | No event/condition coverage, no durable automatic alert, and never-fired state is bounded by controller history/restart semantics |
| `gc order check` | Current due/not-due evaluation, including cooldown `never run`, pending event counts, condition results, and missed cron catch-up after a prior run | Zero-history cron slots outside the exact minute; no automatic alert; slow under load |
| `gc order history` | Retained firing attempts, with `rig` on scoped histories | Whether an absent run was expected; requires resolved schedule and first-seen context |
| `order.fired`, `order.completed`, `order.failed` events | An attempted execution started, completed, or failed; `subject` carries the scoped order identity | A trigger that never enters dispatch emits no event; no city-root consumer turns failures or gaps into a fleet alert |
| `order-tracking-sweep` | Closes stale open tracking beads and prunes history while retaining recent records | It does not detect a missing trigger. Its own help says it operates on already-created tracking beads and abandoned order-run wisps |
| `mayor-pattern-miner` stale-order section | Weekly scan of local files with parseable `interval`; flags no history or age greater than twice interval | All cron, event, condition, imported, and scoped rows; it also shares the order substrate and skips itself |
| `morning-triage-watchdog` | Outcome-specific proof that `morning-triage-cycle` produced today's `slung` audit event | Every other scheduled order |
| `temporal-soak-check` | Outcome checks for one temporary Temporal maintenance schedule | The Gas City order fleet |

The recent 20,000-line event-log window contained 2,560 `order.fired`, 2,218 `order.completed`, and 340 `order.failed` events; scoped subjects such as `patrol-project-leads:rig:aoa` are present. No city-root script or order consumes `order.failed` as a fleet alert, and the feed cannot report a missing attempt because no edge exists to consume. The only dedicated schedule/outcome watchdog found in the Gas City fleet is the morning-triage pair.

`order-tracking-sweep` is particularly easy to misclassify. Its one-minute cadence repairs stale tracking state after an order has entered the run path; it cannot create evidence for a cron trigger that never matched, a disabled definition, or a missed event delivery.

## A follow-up detector needs trigger-specific semantics

A correct detector starts from the resolved list and keeps durable state for each `scoped_name`: `first_seen`, `last_seen`, a trigger-definition fingerprint, and the last evaluated and alerted schedule slots. The first-seen boundary matters because source mtime cannot date a newly expanded rig-scoped instance, while `last_seen` is needed to detect silent de-registration. A fingerprint prevents a schedule edit or disable/re-enable cycle from inheriting stale activation state.

Cooldown rows can compare the latest scoped history timestamp with the resolved interval plus a documented load/grace allowance. Cron rows must reuse the authoritative host-local and DST-aware evaluator semantics rather than introducing a second cron interpretation. For each expected slot after `first_seen`, require a run inside a bounded `[slot, slot + grace]` window; a later healthy run must not erase an earlier missed slot.

Event rows cannot use elapsed cadence. A quiet event source is healthy, while a durable matching source event that advances beyond the order's consumed watermark without a corresponding fire/completion is a delivery gap. The scheduler persists an event cursor per scoped order, but list/check/history JSON does not expose that cursor directly; a follow-up must either consume supported `gc order check` output or first add a supported cursor API. Coalescing multiple source events into one fire is normal and must not become a false alert.

Attempt failures belong to a separate path consuming `order.failed`. Condition rows need an explicit false-versus-error contract before coverage can be claimed: ordinary nonzero checks currently mean not due, while timeout is surfaced separately. A fleet detector cannot infer configuration or execution failure from “condition false” alone.

The minimum fixture set for any implementation follow-up is:

- a city-local order, a built-in imported order, and two rig-scoped instances sharing one base name;
- a never-fired cron before and after its first expected host-local slot;
- a missed cron slot, plus a late run inside the accepted grace window;
- a cooldown with no history and a cooldown delayed under bounded host load;
- an event order with a quiet source, coalesced source events delivered successfully, an unconsumed durable source event, and an event-triggered `order.failed` whose cursor advanced;
- a condition that is false/quiet, true followed by a successful fire, timed out, and failed because of execution or configuration;
- an `order.failed` attempt that is not mislabeled as a missing schedule;
- a formerly resolved local, imported, or scoped row disappearing from the inventory.

Placement remains the unresolved design boundary. An order-based fleet detector can identify one dormant peer while the controller is otherwise firing, but it cannot detect collapse of the order substrate that runs the detector itself. The per-order doctor check is currently the only outside-on-demand view. Choosing a non-order runner or accepting that shared-substrate limitation needs an explicit decision before implementation.

## Reproduction

All commands below were read-only:

```bash
gc order list --json
gc order history --json
gc order check --json
gc order show order-tracking-sweep
gc order history order-tracking-sweep
gc order sweep-tracking --help
tail -n 20000 .gc/events.jsonl
```

The next step is a scoped detector design decision, not a configuration change: choose its execution substrate and grace policy, then build the fixtures above before adding any live sensor.
