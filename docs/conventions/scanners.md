# Scheduled scanners (`gc order`)

Failure modes covered: abandoned worker process leaving a bead claim stuck in `in_progress` (orphan-sweep only catches dead agent names, not dead worker processes for live names); abandoned Claude Code processes accumulating until systemd-oomd kills `user@1000`; slack bindings orphaning after a pool respawn so the channel goes silent; mail to `human`/`stephanie`/`sjarmak` sitting dead-letter in mayor's inbox; beads closed without required evidence metadata; CLAUDE.md / mayor-memory references to GitHub issues that have since been resolved; epic-level review fragmenting into per-bead reviews.

## Order table (current)

Eleven `gc order` definitions in `orders/`:

| Order                              | Trigger              | Driver                                            |
| ---------------------------------- | -------------------- | ------------------------------------------------- |
| `mayor-watchdog`                   | cooldown 1m          | `bin/mayor-watchdog`                              |
| `close-gate-reaper`                | cron `5 * * * *`     | `bin/close-gate-reaper --apply --nudge-mayor`     |
| `stale-claim-reaper`               | cooldown 1h          | `bin/stale-claim-reaper --apply --nudge-mayor`    |
| `stale-worktree-reaper`            | cooldown 1h          | `bin/stale-worktree-reaper --include-unlocked --threshold 3d --max-remove 10 --nudge-mayor` (dry-run) |
| `epic-review-sweeper`              | cooldown 10m         | `bin/epic-review-sweeper --apply`                 |
| `slack-binding-reaper`             | cooldown 5m          | `bin/slack-binding-reaper --apply --nudge-mayor`  |
| `claude-zombie-report`             | cron `0 9 * * *`     | `bin/claude-zombie-report --nudge-mayor`          |
| `memory-audit-issues`              | cron `15 9 * * *`    | `bin/memory-audit-issues --nudge-mayor`           |
| `bead-janitor`                     | cron `30 9 * * *`    | `bin/bead-janitor --nudge-mayor`                  |
| `mail-redirect-to-mayor`           | event `mail.sent`    | `bin/mail-redirect-to-mayor`                      |
| `mayor-health-surfacer-am` / `-pm` | cron `45 9` / `0 17` | `bin/mayor-health-surfacer --nudge-mayor`         |
| `mayor-pattern-miner`              | cron `30 8 * * 1`    | `bin/mayor-pattern-miner --nudge-mayor`           |
| `cross-rig-handoff-patrol`         | cooldown 2m          | `bin/cross-rig-handoff scan --apply --nudge-mayor` |
| `terminal-escalation-patrol`       | cooldown 2m          | `bin/terminal-worker-escalation scan --apply`     |
| `disk-pressure-guard`              | cron every 30m       | `bin/disk-pressure-guard --apply --nudge-mayor`   |
| `storage-ledger`                   | cron daily 07:20     | `bin/storage-ledger`                              |

Eight legacy `.timer` units are `disable --now`'d but the `.service` units stay on disk for emergency re-enable. To re-enable systemd path for any one:

```bash
systemctl --user enable --now <name>.timer
mv orders/<name>.toml orders/<name>.toml.disabled    # avoid double-fire
```

## Universal commands

```bash
gc order list                     # all orders + trigger + cadence
gc order check                    # which are due to fire next tick (shows due/not-due reason)
gc order show <name>              # config and source file
gc order history <name>           # recent fires
gc order run <name>               # ad hoc fire through the controller
```

## Per-scanner notes

### disk-pressure-guard and storage-ledger (2026-07-20)

The disk guard warns below 300 GiB free, acts below 200 GiB, and reports
critical pressure below 100 GiB. Inode use is a parallel trigger at 85%, 90%,
and 95%. It only removes children of the existing allowlisted regenerable cache
directories. It also measures `~/.beads/eventsData` by allocated blocks: when
Beads metrics are durably disabled and that disposable queue exceeds 1 GiB,
the guard atomically detaches it, creates an empty private replacement, then
deletes only the detached queue. If metrics are enabled, telemetry is
report-only. Audit: `.gc/disk-pressure-guard.log`.

`storage-ledger` is a read-only daily inventory (apart from its audit append).
It records root and `/mnt` capacity/inodes, telemetry allocation, registered
worktree bytes by rig, conservative clean+merged+stale potential, the 25
largest worktrees, and totals for `node_modules`, `.venv`, `target`, `.next`,
`dist`, and `build`. It prunes generated directories instead of traversing
their contents and reports day-over-day deltas from `.gc/storage-ledger.log`.

### stale-worktree-reaper storage gates (2026-07-20)

The default script behavior remains locked-only. The order explicitly passes
`--include-unlocked`, so it can detect the incident class where clean, merged,
inactive unlocked worktrees retained large result trees. Before listing a tree
it requires an old named branch merged into the default branch, clean Git/jj
state, no live process path, and no open/in-progress/blocked bead whose
`gc.work_dir` points at it. Any bead-store query failure is fail-closed. Safe
candidates are sorted largest-first and `--max-remove` bounds each apply run.
Removal never uses `--force` and retains branch refs. The order is intentionally
dry-run pending review of a fleet report.

### terminal-escalation-patrol (2026-07-18, dr-80uo)

`bin/terminal-worker-escalation raise` replaces the split help-request/status/
mail sequence for terminal worker blockers. One bead update records typed
escalation metadata, changes the bead to `blocked`, clears its assignee and
session/route metadata, and adds `terminal-escalated` plus `dispatch-blocked`.
That blocked source metadata is authoritative; `.gc/terminal-escalations/`
records notification attempts and the required coordinator acknowledgement
and disposition. Both the owning PL and mayor receive retryable `--notify`
mail, but delivery never completes the escalation.

One of those two coordinators claims the escalation with `ack`, then the same
coordinator records its disposition with `dispose`. The patrol reconstructs a
missing JSON record from source metadata, repairs state drift, and retries
coordinator notifications until both obligations are recorded. Remove it only
after the native scheduler provides the same atomic transition and durable
coordinator lifecycle.

### cross-rig-handoff-patrol (2026-07-18, dr-wjza)

`bin/cross-rig-handoff` is the sole owner of deterministic handoff records in
`.gc/cross-rig-handoffs/`. It materializes one provenance-tagged child in each
declared target rig, routes exact open/unassigned children, and gates source
closure until every target is accepted, explicitly blocked, or completed. The
patrol retries partial materializations and reopens prematurely closed sources;
it never creates native dependencies or parent links across stores. Remove this
patrol after a native Gas City cross-store primitive provides the same persisted
materialization, reconciliation, and close-gate contract and existing records
have been migrated.

Source references are always qualified: `city:<id>` for the city store or
`rig:<rig>:<id>` for a rig store. A bare bead ID is invalid, even when its prefix
appears unique. Use `cross-rig-handoff gate --source <qualified-ref>` as the
mechanical close gate; a closed tracker bead alone does not satisfy it.

### claude-zombie-report

Abandoned interactive Claude Code sessions and orphaned `Agent`-spawned sub-processes (those with `--parent-session-id` but no live parent) do not self-terminate. Left alone they accumulate and eventually trigger `systemd-oomd` against `user@1000`, taking mayor and the supervisor down as collateral.

Script matches process `comm` (`claude` or `X.Y.Z` version-name), excludes anything with `GC_SESSION_ID` in env. Default age threshold 3 days.

```bash
claude-zombie-report                # print only
claude-zombie-report --nudge-mayor  # nudge mayor if anything stale
claude-zombie-report --min-age 86400  # 1-day threshold
```

**When the report fires, triage entries first — don't blanket-kill.** Active long-running interactive sessions (your own work in detached tmux panes) look identical to abandoned ones. Check the `CWD` column and cross-reference with tmux before killing. Clean kill: `kill <PID>`; if unresponsive after a few seconds, `kill -9 <PID>`.

### memory-audit-issues

Workspace memory files (project CLAUDE.md, mayor's auto-memory at `~/.claude-homes/account1/.claude/projects/-home-ds-gas-city/memory/`) accumulate references to upstream GitHub issues and PRs that are later resolved without the memory being updated. Scanner extracts refs matching `owner/repo#NNN` and `github.com/owner/repo/{issues,pull}/NNN`, queries each via `gh`, and flags refs that are closed/merged when last-seen state was open.

State file `~/.gc/memory-audit-state.json` — repeat runs only flag changes.

```bash
memory-audit-issues               # silent if no findings
memory-audit-issues --force       # re-flag even unchanged entries
memory-audit-issues --nudge-mayor # send to mayor if findings exist
```

When fires: review each ref. A closed issue cited as historical context (e.g. "fixed in #899") is fine to leave — scanner won't re-flag on subsequent runs. What matters is pruning workaround sections whose underlying issue is resolved, and rewording entries that treat closed issues as open problems.

### stale-claim-reaper

`bd update --claim` atomically sets `assignee` and `status=in_progress`, but there is no liveness signal: if the worker process dies between claim and `bd close`, the claim sticks forever. Upstream `orphan-sweep` only catches dead **agent names** (assignee not in `city.toml`), not dead **worker processes** for live agent names. This script fills the gap.

For each rig, scans `bd list --status=in_progress`, flags beads where `updated_at` > 24h AND no commit in the rig's git log mentions the bead ID. Flagged beads are unclaimed (`status=open`, `assignee=""`) — NOT closed, since the work may still be valid. Skips beads with `metadata.long_running = "true"`. City beads excluded.

Audit log `.gc/stale-claim-reaper.log` (JSONL): `stale_claim_detected`, `stale_claim_reaped`, `skipped_has_commit`, `reap_failed`.

```bash
stale-claim-reaper                          # dry-run report
stale-claim-reaper --apply                  # actually unclaim
stale-claim-reaper --rig zeldascension      # scope to one rig
stale-claim-reaper --threshold 12h          # tighter
```

Upstream tracking: gascity `gc-1b2` (P2). Retire when maintenance pack ships its own version.

### slack-binding-reaper (2026-05-10)

Slack bindings on named sessions survive respawn because the API stores their stable qualified name (e.g. `oversight-rig.cos`). Pool-spawned sessions (gascity-maintenance-pl, gascity-packs-pl, …) get bound by raw `gc-XXXX` SessionID — when the pool respawns, the binding orphans and `extmsg: notify gc-XXXX failed: session is closed` floods the log. The channel goes silent (no eyes-react, no reply protocol fires).

Script reads `.gc/services/slack/data/config.json`, detects bindings whose raw `gc-XXX` SessionID isn't in the live session list, looks up the live session for the same agent slot via handle → `agents/<name>/agent.toml` dir → template path, and calls `/extmsg/{unbind,bind}` to rewire. Local config.json is updated to keep `gc slack status` consistent.

Tracking bead: `dr-9y620w` (P2). Retire once upstream fix lands.

Audit `.gc/slack-binding-reaper.log` (JSONL): `stale_binding_detected`, `rebound`, `skip_no_live_target`, `rebind_failed`, plus post-#1953 events `config_drift_detected` / `config_drift_resynced` for when the API was already cascade-rebound and only the local config.json is stale, and `unbind_unexpected_status` for any non-200/422 unbind response.

```bash
slack-binding-reaper --verbose             # dry-run
slack-binding-reaper --apply --nudge-mayor # what the gc order runs

# Manual one-off rebind:
curl -sS -X POST http://127.0.0.1:8372/v0/city/ds-research/extmsg/unbind \
  -H 'Content-Type: application/json' -H 'X-GC-Request: 1' \
  -d '{"session_id":"<stale-gc-id>","conversation":{...}}'
curl -sS -X POST http://127.0.0.1:8372/v0/city/ds-research/extmsg/bind \
  -H 'Content-Type: application/json' -H 'X-GC-Request: 1' \
  -d '{"session_id":"<live-gc-id>","conversation":{...}}'
```

### mail-redirect-to-mayor (2026-05-10)

Stephanie doesn't read `gc mail` — she's only alerted via slack. When a polecat / PL / worker mails `human` / `stephanie` / `sjarmak` (typically at a human-gate step), the mail is dead-letter. Mayor scans her inbox on wakeup but those mails sit unsurfaced.

Handler fires on `mail.sent` event: when mail to a dead-letter recipient arrives, sends a copy to mayor with `[redirect] (to <recipient>)` subject prefix and a body header annotating the original sender / recipient / mail ID. Mayor surfaces to relevant slack channel / rig PL on next interaction.

Idempotent via `.gc/mail-redirect-to-mayor-state.json`. Skips mail FROM mayor (avoid loops). Extend `DEAD_LETTER_RECIPIENTS` in the script.

Backstory (`dr-o3he9r`): gc-v81c polecat (PR #1636 rebase+merge) finished work, stopped at push, mailed `human` for approval, sat parked ~30min until Stephanie asked "check polecat status" in slack. Without this handler, every polecat human-gate step has the same failure mode.

### close-gate-reaper

Some bead classes require specific evidence metadata before they can stay closed. Historically mayor-enforced (per `feedback_codeprobe_validation_gate.md`); now system-enforced at epic level.

Scans beads closed after `enabled_since` in `.gc/close-gates.yaml`, reopens any whose required_metadata fields are missing. Bypass: legitimate closes outside the regime set `metadata.gate_bypass = "<reason>"` before close. Grace period 5min — freshly-closed beads aren't touched until the closer has had time to write evidence metadata.

Currently enforced rules:

| Rule id                       | Pattern                                    | Scope     | Precondition                                   | Required metadata                                                                |
| ----------------------------- | ------------------------------------------ | --------- | ---------------------------------------------- | -------------------------------------------------------------------------------- |
| `codeprobe-br7-epic-evidence` | `^codeprobe-br7$` (epic only, not members) | codeprobe | `epic_review_dispatched=true` (set by sweeper) | `evidence.artifact_path`, `evidence.reviewer_verdict`, `evidence.reviewer_agent` |

Three precondition mechanisms:

- `require_metadata: [{path, value}]` — all listed metadata fields must match the given values
- `require_molecule_formula: "<formula-name>"` — bead's wisp title must equal this (dispatch-side check)
- `id_pattern` + `title_pattern` — an OR pair; either match gets the bead into scope

Adding a rule: append to `rules:` in `.gc/close-gates.yaml`, bump `enabled_since` to avoid retroactively reopening pre-existing closes. If the pattern could match human-closed beads, add a `require_metadata:` precondition that only the formula-driven path sets.

Audit `.gc/close-gate-reaper.log` (JSONL): `evidence_missing`, `reopened`, `bypass_respected` (deduped on `(rule, bead, reason)` since 2026-05-03), `reopen_failed`. `mayor-pattern-miner` has a dedicated section for close-gate events and flags repeat offenders as a signal that the worker/reviewer loop isn't learning the evidence requirement.

```bash
close-gate-reaper                 # dry-run report
close-gate-reaper --apply         # reopen offenders

# Per-rule breakdown from audit log:
jq -r 'select(.event == "evidence_missing") | [.rule, .bead] | @tsv' .gc/close-gate-reaper.log \
  | sort | uniq -c | sort -rn
```

### epic-review-sweeper (2026-04-21)

External review runs at **epic boundaries**, not per bead. Individual work beads (br7.N etc.) close on self-review via `/focus` — no gating. When ALL beads in a rule-defined chunk are closed, the sweeper dispatches the epic bead to a reviewer agent, which grades the aggregate diff and either closes the epic with evidence or creates follow-up beads. Max 3 passes before mailing human; no human attention needed in the happy path.

Files:

- `bin/epic-review-sweeper` — periodic chunk-readiness scanner (10m cadence)
- `orders/epic-review-sweeper.toml` — `gc order` cooldown 10m
- `.gc/epic-review-rules.yaml` — rule config (per-chunk: member patterns, epic bead id, reviewer, max_passes)
- `formulas/mol-epic-review.formula.toml` — reviewer formula (grade → pass-close-with-evidence OR reject-with-follow-ups)
- `.gc/epic-review-sweeper.log` — JSONL audit (`ready`, `dispatched`, `skip_max_passes`, `mailed_human`, `dispatch_failed`)

Flow (automated):

1. Sweeper sees all members of a rule-defined chunk closed AND epic still open → sets `metadata.review_in_flight=true` + `epic_review_dispatched=true` + `last_review_dispatched_at=<ts>` on epic, then `gc sling <reviewer> <epic> --on mol-epic-review`.
2. Reviewer reads epic acceptance criteria + aggregate diff, grades per-criterion.
3. **Pass:** writes `evidence.artifact_path/verdict/agent` on epic, closes epic, drains.
4. **Reject:** increments `metadata.review_pass_count`, writes `metadata.rejection_reason`, creates follow-up beads linked to epic (one per failing criterion), clears `review_in_flight`, drains. Workers claim follow-ups through the normal flow.
5. When all follow-ups close, sweeper re-dispatches (pass 2, pass 3…).
6. At pass 3+, sweeper skips dispatch, mails human ONCE per stuck epic (sets `metadata.max_passes_mailed=true`), epic waits for human intervention.

Adding a new chunk:

```yaml
# .gc/epic-review-rules.yaml
rules:
  - id: "<rig>-<name>-epic"
    scope: "<rig>"
    epic_id: "<rig>-<epic-bead>"
    member_id_pattern: "^<rig>-<prefix>(\\.\\d+)?$"
    member_title_pattern: "^<name>\\.[0-9]"
    reviewer: "<rig-path>/<agent-name>"
    max_passes: 3
```

Also add a matching rule to `.gc/close-gates.yaml` scoped to the epic id only (NOT members) so evidence enforcement closes the loop at epic close.

Superseded formulas (removed 2026-04-21): `mol-br7-focus-review`, `mol-codex-review`, `mol-meta-review` — use `mol-epic-review` at epic boundaries instead.

```bash
epic-review-sweeper                   # dry-run
epic-review-sweeper --apply           # actually dispatch
gc order show epic-review-sweeper
jq -r 'select(.event == "dispatched") | [.ts, .rule, .epic] | @tsv' .gc/epic-review-sweeper.log
```
