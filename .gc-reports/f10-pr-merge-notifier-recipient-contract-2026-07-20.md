# F10 PR merge notifier recipient-contract verification

Date: 2026-07-20
Bead: `dr-cki7`

## Declared contract

`orders/pr-merge-notifier.toml` declares two required recipients for each newly merged PR: the Gas City maintenance Slack channel **and** durable mayor mail. The existing implementation already attempted both independently; no new fallback was needed.

## Fixture-first finding

The new hermetic fixture `bin/pr-merge-notifier.test:58-122` replaces both `gh` and `gc` through a temporary `PATH`. Against the pre-repair script, it exited 1 at the first total-failure assertion because the script appended the PR to global `notified_prs` even when both deliveries failed. The next cooldown therefore skipped both required recipients.

The same global marker also suppressed the failed recipient after partial delivery, despite the other recipient succeeding.

## Minimal repair

- `bin/pr-merge-notifier:20-21,56-77` defines and persists separate `slack_notified_prs` and `mail_notified_prs` acknowledgement state while retaining `notified_prs` as the compatible global completion marker.
- `bin/pr-merge-notifier:93-105` treats legacy `notified_prs` entries as fully delivered, preserving the existing quiet path.
- `bin/pr-merge-notifier:126-156` attempts only recipients without acknowledged delivery and records each recipient only after its command succeeds.
- `bin/pr-merge-notifier:159-163` advances global completion only after both required recipients are acknowledged.

No duplicate fallback or new recipient was added.

## Verification

Commands:

```text
bash -n bin/pr-merge-notifier bin/pr-merge-notifier.test
bash bin/pr-merge-notifier.test
shellcheck bin/pr-merge-notifier bin/pr-merge-notifier.test
python3 - <<'PY'  # tomllib parse and expected order contract assertions
...
PY
gc order show pr-merge-notifier
```

Result:

```text
ok: total failure retries both required recipients; full acknowledgement becomes quiet
ok: acknowledged Slack stays quiet while failed mayor mail retries
ok: acknowledged mayor mail stays quiet while failed Slack retries
ok: legacy globally-notified state remains compatible and quiet
PASS: pr-merge-notifier per-recipient delivery contract fixtures
PASS: shell syntax
PASS: shellcheck
PASS: orders/pr-merge-notifier.toml parses and resolves expected contract
PASS: gc order show pr-merge-notifier
```

No live GitHub query, Slack publication, mayor mail, human mail, or external notification occurred during the fixture.
