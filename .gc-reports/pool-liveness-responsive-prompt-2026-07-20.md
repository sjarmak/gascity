# Pool-liveness coherent-prompt false-positive repair

Bead: `dr-8fgf`

## Finding

`bin/pool-liveness-sensor` treated every unchanged `gc session peek` tail as
evidence of a frozen process. Read-only peeks of codeprobe sessions
`gc-518023`, `gc-518032`, and `gc-509863` instead showed coherent completed or
current-work text, Claude's ready-input `❯` prompt, and the active TUI footer.
They were waiting for their next instruction, not frozen corpses. No worker was
recycled, nudged, restarted, or otherwise mutated.

## Repair

- `bin/pool-liveness-sensor:295-328` records both the terminal-tail hash and a
  narrowly identified Claude ready-input state. Identification requires the
  `❯` prompt and the live status footer; historical prompt text alone does not
  qualify.
- `bin/pool-liveness-sensor:501-534` classifies that state as `RESPONSIVE` and
  resets the frozen-output streak.
- `bin/pool-liveness-sensor:545-562` excludes responsive workers from deadlock
  classification and accepts responsiveness as positive recovery evidence.
- `bin/pool-liveness-sensor.test:251-266` reproduces multiple pinned workers
  with stable current-work/landing-gate prompts and proves they remain
  unflagged. Existing corpse fixtures still prove that stable non-prompt output
  flags after the configured debounce.

## Verification

- `python3 -m py_compile bin/pool-liveness-sensor` — PASS.
- `bash -n bin/pool-liveness-sensor.test` — PASS.
- `bash bin/pool-liveness-sensor.test` — PASS, including stable prompt
  `RESPONSIVE` coverage and frozen-pinned-pool alert coverage.
- `python3` `tomllib.load(orders/pool-liveness-sensor.toml)` — PASS.
- `gc order show pool-liveness-sensor` — PASS; resolved order remains the
  10-minute surface-only detector.
- `shellcheck bin/pool-liveness-sensor.test` — existing test-file findings
  remain at lines 107, 173, 181, and 184 (`SC2015`, `SC2034`); the new fixture
  introduces no additional finding.

No external action occurred.
