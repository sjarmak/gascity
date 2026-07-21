# F09 whole-operation writer serialization fixture

Bead: `dr-6qrb`  
Authority: mayor `gc-520788`  
Recorded: 2026-07-20 EDT

## Change boundary

- `bin/login-wedge-scanner` now takes a nonblocking `flock` before loading
  state or enumerating sessions and holds it through detection, the existing
  two-scan confirmation, attached/protected/cooldown/attempt/per-tick guards,
  any bounded `gc session kill`, mail surface, and state write.
- `bin/dolt-flatten-maintenance` now takes a nonblocking `flock` before the
  server/threshold phase and holds it through mapping, quiescence, dry-run
  identity check, verified snapshot, flatten, health gates, snapshot rotation,
  and abort handling.
- Contenders exit without entering either operation and emit distinct audit
  evidence: `scan-skipped-lock` or `SKIP lock-busy`.
- Pre-edit snapshots:
  - `bin/login-wedge-scanner.bak-f09-serialization-20260720T041443Z`
  - `bin/dolt-flatten-maintenance.bak-f09-serialization-20260720T041443Z`

## Concurrent fixture

Command:

```text
$ bash -n bin/dolt-flatten-maintenance bin/f09-writer-serialization.test
$ python3 -m py_compile bin/login-wedge-scanner
$ bash bin/f09-writer-serialization.test
ok: login scanner admits one mutation writer; contender is audited skipped-lock
ok: flatten admits one operation writer; contender is audited skipped-lock
PASS: F09 whole-operation concurrent serialization fixtures
```

The login fixture overlaps two live-execute-mode scanner processes against a
stubbed `gc`: only one reaches session enumeration and exactly one bounded kill;
the contender reports `scan-skipped-lock` with `recycled=0` and writes the same
event to JSONL.

The flatten fixture overlaps two processes and stops the admitted process
immediately after the lock boundary, before any server or store call. Exactly
one PID crosses the boundary; the contender emits `SKIP lock-busy`.

No live session kill, Dolt query, flatten, service restart, or external
notification was performed by the fixture.
