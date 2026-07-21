# Corrected pipeline-audit actions — first verification

Recorded: 2026-07-20 EDT

## F04 live containment

- Bead: `dr-iw7`
- The live armed worker resolves reviewer and author prompts directly from
  `services/temporal-maintenance/prompts/`.
- `review.md:40-47` now makes every outcome proposal-only and prohibits every
  GitHub review/comment/edit/merge/push/PR mutation.
- `author.md:19-38` now fixes `auto_push=false` for every authorable class and
  halts at a locally reviewed branch-ready result.
- Verification:

  ```text
  $ go test ./... -run TestScheduledPromptsAreProposalOnly -count=1
  ok github.com/sjarmak/gas-city/services/temporal-maintenance
  ```

No GitHub mutation command was run.

## Dispatcher false-positive diagnosis and repair

- Bead: `dr-l9hy`
- Alerts `gc-520360` and `gc-520564` reported an exact 60-minute age while
  direct reads showed the reported trace remained active.
- Root cause: `_epoch()` parsed the trace tuple with local-DST `mktime`, while
  the comparison used `mktime(gmtime())`; `gmtime()` carries `tm_isdst=0`, so
  `mktime` applied EST to one side and EDT to the other. The result was an exact
  +60-minute phantom age during daylight-saving time.
- Repair: parse trace UTC with `calendar.timegm`, compare directly to
  `time.time`, make the trace path fixture-injectable, and include that exact
  path in JSON evidence.
- Verification:

  ```text
  $ python3 -m py_compile bin/dispatcher-liveness-sensor
  $ bash -n bin/dispatcher-liveness-sensor.test
  $ bash bin/dispatcher-liveness-sensor.test
  PASS: dispatcher-liveness uses exact reported path and UTC age without DST skew

  $ bin/dispatcher-liveness-sensor --json
  "alerts": []
  "trace": "/home/ds/gas-city/.gc/runtime/core.control-dispatcher-trace.log"
  "newest_age_min": 0.0
  ```

The live dispatcher was not restarted, killed, or otherwise mutated.

## F08 read-only incident diagnosis

- Incident bead: `dr-r9z`
- Independent code-repair bead: `dr-hvf2`
- Failed workflow `maintenance-cycle-2026-07-19T14:00:00Z`, run
  `019f7aad-3798-79fa-9da8-7c36450148f6`: selection created `gc-8d65` and its
  worktree, then `gc-sling` was killed. The at-most-once record is terminal
  failed with `result_ref=gc-8d65`; the bead remains open and unrouted.
- Failed workflow `maintenance-cycle-2026-07-19T16:00:00Z`, run
  `019f7b1b-1450-76a3-8b78-249f3aff845c`: the in-flight guard failed before
  creation because its rig query inherited Dolt endpoint `127.0.0.1:0`.
- `.gc/temporal-soak-check.log:144-168` repeatedly preserves both workflow IDs
  and orphan `gc-8d65`; each Slack attempt records `push-failed`. This is
  delayed/absent escalation, not lost evidence.
- No workflow, bead, worktree, service, or dispatcher was mutated during this
  diagnosis. Recovery of `gc-8d65` remains separately gated.

## F08 independent fallback repair

- `bin/temporal-soak-check:266-272` retains independent mayor-fallback dedup
  state without advancing Slack's `last_alert`.
- `bin/temporal-soak-check:324-353` sends durable mayor mail when Slack fails,
  dedups identical evidence, and keeps Slack retryable.
- Verification:

  ```text
  $ bash -n bin/temporal-soak-check bin/temporal-soak-check.test
  $ bash bin/temporal-soak-check.test
  RESULT: 24 passed, 0 failed
  ```

The failure fixture proves two Slack attempts, one durable mayor mail, and
independent fallback-vs-Slack state.

## Remaining corrected-scope beads

- F09 P1 login/flatten serialization: `dr-6qrb`
- F09 P2 mirror/codegraph investigation only: `dr-p3f4`
- F10 P1 blocker-delivery dedup repair: `dr-cut4`
- F10 P2 merge-notifier total-failure/recipient-contract test: `dr-cki7`
- F11 resolved runtime order census/discovery gate: `dr-s7am`
- F12 atomic cross-rig dependency conversion: `dr-u0gu`
