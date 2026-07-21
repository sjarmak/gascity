# F10 delivery-acknowledged dedup verification

Date: 2026-07-20
Bead: `dr-cut4`

## Repair

- `bin/help-request-surface:43-50` now writes `gc.help_surfaced_at` only after `gc session nudge mayor` returns success. A failed nudge leaves the bead unstamped and retry-eligible.
- `bin/wake-mayor-on-blocker-close:77-83,136-200` now keeps pending dedup keys grouped by recipient without advancing persisted state.
- `bin/wake-mayor-on-blocker-close:204-246` advances only the keys belonging to each successfully nudged recipient. A failed mayor or owner nudge leaves only that recipient's keys retry-eligible; successful recipients remain quiet.
- Existing nudge targets and message bodies are unchanged.

## Hermetic fixture

`bin/f10-delivery-dedup.test:77-126` replaces `gc` through a temporary `PATH` stub and never calls the live notification path. It proves:

1. A failed help-request nudge performs no metadata update; the next run retries.
2. A successful help-request nudge stamps delivery; the next run is quiet.
3. When the mayor nudge fails but the creator nudge succeeds, only mayor retries.
4. Once mayor succeeds, both recipient paths remain quiet.

Command:

```text
bash -n bin/help-request-surface bin/wake-mayor-on-blocker-close bin/f10-delivery-dedup.test
bash bin/f10-delivery-dedup.test
```

Result:

```text
ok: help request failure remains retryable; acknowledged delivery advances dedup
ok: blocker-close dedup advances per recipient only after acknowledged delivery
PASS: F10 delivery-acknowledged dedup fixtures
```

Resolved-order checks:

```text
gc order show help-request-surface              PASS
gc order show wake-mayor-on-blocker-close      PASS
```

No live Slack, human mail, session nudge, service restart, or external notification occurred during verification.
