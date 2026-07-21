# Dispatcher-watchdog per-target conversion scoping — 2026-07-20

## Outcome

`bin/dispatcher-watchdog` no longer attributes one city-wide control-bead set
to every dispatcher. It now derives a stable canonical target from each active
dispatcher name, reads only that target's city or rig store, and exactly matches
normalized `gc.routed_to` / `gc.run_target` ownership
(`bin/dispatcher-watchdog:54-111`). The query uses the dispatcher's processable
universe: `bd ready --unassigned --exclude-type=epic`, with
`gc.instantiating` entries excluded. Dependency-blocked, epic, and
mid-instantiation beads therefore cannot age the conversion clock.

Conversion history is keyed by stable target identity rather than ephemeral
`gc-NNN` session ID (`bin/dispatcher-watchdog:114-160`). The current session ID
is retained only as action/observability metadata. An unchanged target set keeps
its original clock across a replacement ID; a changed set resets only that
target; an empty set clears the target clock. Legacy session-ID keys are ignored
rather than migrated because their historical sets were city-wide and cannot be
safely attributed.

Unreadable or malformed store data is not treated as an empty or partial set.
The full jq result is captured and validated before any IDs are emitted; command,
shape, metadata-type, or bead-ID errors preserve the existing clock without
aging or action (`bin/dispatcher-watchdog:77-111,125-139`). An unparseable
`last_active` skips only the idle signal and still permits the independently
scoped conversion check (`bin/dispatcher-watchdog:191-223`). The production
conversion threshold remains 5400 seconds / 90 minutes
(`bin/dispatcher-watchdog:42-46`).

## Hermetic fixture

`bin/test_dispatcher_watchdog.py` pins fake `gc` and `bd` executables, temporary
city/audit/conversion-state paths, a deterministic clock, the production 5400s
conversion threshold, and `DRY_RUN=0` (`bin/test_dispatcher_watchdog.py:16-112`).
It proves:

- a stalled rig A neither creates state nor kills an empty rig B
  (`:127-150`);
- changes in rig A do not reset rig B's independent clock (`:153-176`);
- empty queues clear history before the same set reappears (`:179-198`);
- a replacement session ID retains the target clock and a genuine unchanged
  same-rig stall still flags at 5400s (`:201-216`);
- city and rig stores remain isolated and legacy ID-keyed state is not reused
  (`:219-234`);
- malformed target/ID rows, invalid timestamps, dependency-blocked beads,
  epics, and `gc.instantiating` entries cannot become kill evidence
  (`:237-292`).

## Verification

All checks passed without invoking the live watchdog or any live session action:

```text
bash -n bin/dispatcher-watchdog                         PASS
shellcheck bin/dispatcher-watchdog                     PASS
python3 -m pytest -q bin/test_dispatcher_watchdog.py  11 passed
python3 -m ruff check bin/test_dispatcher_watchdog.py All checks passed
Python tomllib parse orders/dispatcher-watchdog.toml   TOML PASS
gc order show dispatcher-watchdog                     resolved expected exec/30m cooldown
```

No threshold, order, city config, service, dispatcher, supervisor, worker, or
session runtime action was changed or executed. No external action was taken.

## Exact touched files and initial red-test artifact

Intended files:

- `bin/dispatcher-watchdog`
- `bin/test_dispatcher_watchdog.py` (new)
- `.gc-reports/dispatcher-watchdog-per-target-scoping-2026-07-20.md` (this report)

During the first expected-red TDD run, before temporary path overrides existed,
the old script ignored `DISPATCHER_WATCHDOG_CITY` and appended nine false test
records to the live audit log at `.gc/dispatcher-watchdog.log:1289-1297`
(`alpha-old`, `beta-old`, `alpha-new`, and `city-new`). `gc` itself was replaced
by the fixture fake, so no real session was killed and no mail was sent. The live
conversion-state file was not changed (mtime remained 2026-07-20 11:54:37 EDT,
before the 11:59:04-05 test records), and it contains no fake test keys. The
audit lines were preserved rather than silently rewriting an append-only
operational log. The fixture now explicitly pins both audit and conversion-state
paths (`bin/test_dispatcher_watchdog.py:93-106`), and all subsequent verification
was isolated.
