---
name: cityops-city-change-control
description: >-
  Change-control for this install: load BEFORE editing city.toml,
  orders/*.toml, agents/*/agent.toml, or a supervisor systemd drop-in;
  before pausing/adding an order; before promoting a janitor dry-run to
  --apply; or when deciding if a change needs Stephanie's approval. Covers
  bak-before-flip, comment-as-changelog, overrides vs .disabled. Not
  topology (cityops-topology-contract).
---

# City change control — how config changes are made here

Procedure for changing the configuration of the live city at
`/home/ds/gas-city` without breaking the agents currently working in it.
This skill owns the change **process**; it does not own the config **content**
(see the routing table below).

## When NOT to use this skill

| You need                                                      | Go to                                                                                   |
| ------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| What city.toml declares and why (rigs, providers, patches)    | sibling `cityops-topology-contract`                                                     |
| Hard Don't/Do rules for this workspace                        | `/home/ds/gas-city/CLAUDE.md` (the one home for those lists)                            |
| Building a new order/formula/agent end-to-end                 | `docs/conventions/recurring-task-example.md`                                            |
| Diagnosing WHY config behaves strangely before changing it    | `mechanic` skill (root-cause first), then the matching `compass-*`                      |
| Supervisor/tmux restart sequence after a service-level change | `compass-tmux-supervisor`, `docs/conventions/tmux-supervisor.md`                        |
| Ad-hoc guest-session conduct (may you edit at all?)           | `docs/conventions/guest-session-primer.md` + sibling `cityops-guest-session-discipline` |

## The ground rule: no git, so discipline substitutes for it

`/home/ds/gas-city` is **not a git repository** (verified 2026-07-07: no
`.git` directory). There is no `git log`, no `git revert`, no blame. Three
conventions replace version control, and every change here uses all three:

1. **Snapshot before the flip** — copy the file to a dated `.bak-*` sibling
   immediately (seconds, not minutes) before editing.
2. **Comment as changelog** — the rationale lives in a comment block at the
   edit site: date, RCA/bead IDs, approver, and the condition for undoing it.
3. **One concern per flip** — a snapshot-to-live diff must read as exactly
   one change. Never batch an unrelated "cleanup" into the same edit.

## The change loop

Run every config change through this checklist:

1. **Gate check.** Is this change yours to make? See "Human gates" below.
   Guest sessions do not edit `prompts/`, `formulas/`, `agents/` unasked
   (primer rule).
2. **Read the comments at the edit site.** city.toml comments carry RCA bead
   IDs and dates; values that look like cruft are usually load-bearing (see
   the trap table below).
3. **Snapshot.**
   ```bash
   cp /home/ds/gas-city/city.toml \
      /home/ds/gas-city/city.toml.bak-<label>-$(date -u +%Y%m%dT%H%M%SZ)
   ```
4. **Edit, with the comment contract** (next section).
5. **Let it take effect** — no supervisor restart for city.toml or
   `orders/*.toml` edits (see "Take-effect model").
6. **Verify with a read command**, not by assumption (see verification table).
7. **Confirm the diff is one concern:** `diff <newest .bak> city.toml`.

## Snapshot convention

Live snapshots on host (5 as of 2026-07-07): naming drifted early
(`bak-20260529T151405`, `pre-freshwake`, `bak-20260611-prpipeline-path`,
`bak-pre-pl-20260615-2112`) and converged on
**`city.toml.bak-<label>-<UTC timestamp>`**, e.g.
`city.toml.bak-pause-maintenance-cycle-20260706T175816Z`. Use that shape:
the label says what the flip was; the timestamp orders the archaeology.

"Immediately before" is literal: the newest snapshot's mtime is
`2026-07-06 13:58:16 EDT` and the live city.toml's is `13:58:33` — a
17-second gap. The snapshot captures the exact pre-flip state, not a state
from earlier in the day.

Snapshot any file this skill covers, not just city.toml (`orders/*.toml`,
`agents/*/agent.toml`), when the edit changes behavior. Reading a past change
back out of the snapshots is covered in `cityops-topology-contract`
("reading a topology change from its snapshot").

## The comment contract

Every behavior-changing edit carries an adjacent comment with four parts.
The live `[orders]` block in city.toml is the canonical shape:

```toml
# Paused 2026-07-06 per mayor mail gc-454759: the shared mol-formula
# worktree-provisioning bug (RCA gc-454658/gc-454686, durable perf bead
# gc-g421k, ...) ... Re-firing while the bug is live is net-negative.
# Remove this override (or flip enabled = true) once the
# worktree-provisioning fix lands, then re-dispatch the preserved #2713
# candidate ...
[[orders.overrides]]
name = "maintenance-cycle"
enabled = false
```

| Part                 | In the example above                                      |
| -------------------- | --------------------------------------------------------- |
| Date                 | "Paused 2026-07-06"                                       |
| Authority / evidence | "per mayor mail gc-454759", RCA beads gc-454658/gc-454686 |
| Why                  | the spawn-storm mechanism, "net-negative" judgment        |
| Undo condition       | "Remove this override once the … fix lands, then …"       |

Two cautions. Comments are the changelog but **not** current state — the
mayor-pin comment in the same file is stale against its own value (details in
`cityops-topology-contract`). And a comment that names a bead/issue lets any
future session reconstruct intent; a bare `enabled = false` with no comment is
a booby trap for whoever finds it in six months.

## Pausing vs retiring an order

Two live patterns in this city; pick by intent:

| Intent                                    | Mechanism                                                                                            | Live examples (2026-07-07)                               |
| ----------------------------------------- | ---------------------------------------------------------------------------------------------------- | -------------------------------------------------------- |
| **Temporary pause**, undo condition known | `[[orders.overrides]]` block with `enabled = false` at the bottom of city.toml; order file untouched | `maintenance-cycle`                                      |
| **Indefinite retirement**                 | rename the file to `orders/<name>.toml.disabled`                                                     | `bead-janitor.toml.disabled`, `rig-patrol.toml.disabled` |

The override keeps the order's definition intact and puts the RCA comment at
the flip site in city.toml, where the next operator will look. The `.disabled`
rename removes the order from the controller's scan entirely; the retirement
rationale then has to live in the file's own header. Do not delete an order
file outright — there is no git to recover it from.

Re-enabling is a change too: snapshot, remove the override (or flip
`enabled = true`), and honor the undo condition in the comment — for
maintenance-cycle that includes re-dispatching the preserved #2713 candidate,
not just un-pausing.

## Dry-run → apply promotion (janitors and reapers)

Anything that deletes, truncates, or closes at scale ships **dry-run first**
and is promoted by a human after a clean report. The convention, as practiced:

1. The order ships with an env-flag or CLI-flag dry-run default
   (`JANITOR_MODE`, `JANITOR_LOG_EXECUTE`).
2. A human reviews at least one dry-run report.
3. The flag is flipped **in the order file**, with an inline annotation
   naming who and when:
   ```toml
   JANITOR_MODE = "--execute"       # flipped by Stephanie 2026-07-04 after clean dry-run review
   JANITOR_LOG_EXECUTE = "1"        # flipped by Stephanie 2026-07-04 after clean dry-run review
   ```
   (both live in `orders/janitor-worktree-gc.toml` /
   `orders/janitor-log-rotate.toml`, verified 2026-07-07). Longer soak
   periods appear too: `bead-janitor.toml.disabled` records "--apply added
   2026-05-20 after 14d clean dry-run history".
4. Destructive orders keep **blast-radius caps** even after promotion:
   `JANITOR_MAX_REMOVE = "25"` per tick, `JANITOR_MIN_AGE_DAYS = "3"`,
   protected-path regexes, and hard-refused files (the log-rotate script
   refuses `events.jsonl` and `beads.json` by design).

Do not flip a dry-run flag yourself: promotion is the human's call, and the
annotation must name her. This is a standing pattern, not just history —
apply it to any new janitor you stage. Related: orders that are safe to
re-fire mark `idempotent = true` with a comment tying the fail-open behavior
to gastownhall/gascity#2893 (see `orders/gate-sweep.toml`,
`orders/nudge-poll-reaper.toml`).

## Take-effect model

No supervisor restart for config edits:

- **city.toml** — the supervisor re-reads city config on its reconciler ticks
  (`[daemon] patrol_interval = "2m"`; the re-parse-per-tick behavior is
  documented in `docs/supervisor-oom-pprof-notes.md` as the 2026-04-11 OOM
  root cause). Expect a change to be live within a couple of minutes.
- **orders/\*.toml** — the controller scans the directory; new orders are
  installed by copying a file in (the janitor orders' own headers say
  "install by copying into <city>/orders/", and both fired on schedule after
  installation). The order name is the file basename.
- **systemd drop-ins** — these DO need `systemctl --user daemon-reload` and a
  unit restart (see below).

Restarting the supervisor to "make sure" a config edit took is not neutral:
restarts have their own failure modes (tmux ordering, orphan adoption — owned
by `docs/conventions/tmux-supervisor.md`). Verify with a read command instead.

## Verification commands

| To verify                                | Run                                                           | Note                                                                       |
| ---------------------------------------- | ------------------------------------------------------------- | -------------------------------------------------------------------------- |
| Effective per-agent config after an edit | `gc config explain --agent <name>` (from `/home/ds/gas-city`) | shows each key's resolved value + source file                              |
| Effective provider chain                 | `gc config explain --provider <name>`                         |                                                                            |
| An order file edit was picked up         | `gc order show <name>`                                        | shows description/trigger/exec/Source; does **NOT** display override state |
| A pause actually stopped fires           | `gc order history <name>`                                     | silence after the flip timestamp is the proof                              |
| One-concern diff                         | `diff <newest .bak> /home/ds/gas-city/city.toml`              |                                                                            |
| General health after a change            | `gc doctor`                                                   | recovery ladder owned by CLAUDE.md                                         |

**Trap:** `gc order check` (the due/not-due enumerator) is not a reliable
verification tool on this host — it timed out at 45s on 2026-07-06 and again
at 90s on 2026-07-07 under normal load. Use `gc order show` +
`gc order history` instead. `gc order run <name>` fires the order for real
through the controller — it is a live dispatch, not a dry-run; only use it
when you actually want a fire.

## Paired values: edit both or neither

Some values exist in two files and must move together. The known pair: the
mayor provider lives in **both** `agents/mayor/agent.toml` (`provider = ...`)
and the `[[patches.agent]]` mayor block in city.toml (the patch is what takes
effect at launch). `bin/gc-capacity` updates both on a rebalance move; a hand
edit that touches only one re-creates the divergence that produced the stale
mayor-pin comment. Rule: provider moves go through `gc-capacity`, not hand
edits. Full three-layer story in `cityops-topology-contract`.

## Import-path changes

Pack `source =` paths are a recurring breakage class: the pr-pipeline import
path was changed and reverted between the 2026-06-11 and 2026-06-15 snapshots
(the `bak-20260611-prpipeline-path` snapshot exists because of it), and
pointing oversight-rig at the contributor tree is a documented city-breaker
(the Don't lives in CLAUDE.md). Change-control rule: an import may only point
at a **stable, branch-pinned worktree** (`/home/ds/gascity-packs-worktrees/*`
or `/home/ds/gascity-packs/*`), and an import-path edit gets its own snapshot
and comment like any other flip.

## Cron-order edits: two traps with existing homes

If your change adds or reschedules a `trigger = "cron"` order, two pieces of
operator lore apply; their homes are the order headers, read them there:

- **Zero-lastRun bootstrap trap** — a never-fired cron order must be seeded
  once or it never fires. Procedure in the header of
  `orders/pl-status-update-am.toml`.
- **Host-local timezone** — gc cron evaluates in the host's local tz
  (EDT), not UTC; a past UTC assumption fired orders 4h late. Documented in
  the `schedule` comments of `orders/morning-triage-cycle.toml` and
  `orders/overnight-digest.toml`.

Cadence doctrine (cooldown vs cron) is also recorded in order headers
(`orders/decision-ledger-push.toml`, `orders/maintenance-cycle.toml`): prefer
`cooldown` for must-just-work recurring orders — no bootstrap trap, self-pacing.

## systemd drop-in changes

Supervisor service changes are made as drop-ins under
`~/.config/systemd/user/gascity-supervisor.service.d/` — never by editing the
unit file. Nine drop-ins as of 2026-07-07 (`10-dolt-port.conf`,
`docker-group.conf`, `dolt-wait-timeout.conf`, `oom-preference.conf`,
`orphan-guard.conf`, `slack-adapter-env.conf`, `slack-autorestart.conf`,
`stop-catcher.conf`, `stop-timeout.conf`). The convention mirrors the comment
contract: **one concern per `.conf`, descriptive filename, header comment with
the incident/bead reference and the durable-fix condition** (e.g.
`10-dolt-port.conf` cites gc-74rxa and names the upstream fix that would
retire it; `dolt-wait-timeout.conf` marks itself INTERIM).

Applying one requires `systemctl --user daemon-reload` followed by a unit
restart — which means the tmux-first restart discipline applies (CLAUDE.md
"Do" list; full ladder in `docs/conventions/tmux-supervisor.md`). Several
drop-ins are stopgaps for open upstream bugs: before deleting one as
"obsolete", confirm the durable fix named in its header actually landed.

## Changes that look like cleanup but are load-bearing

Do not "tidy" these without reading their story first:

| Tempting cleanup                                      | Why it's a trap                                                     | Story lives in                                         |
| ----------------------------------------------------- | ------------------------------------------------------------------- | ------------------------------------------------------ |
| Flip `[beads] provider = "file"` to make `gc bd` work | catastrophic backend switch; `bd` works directly                    | `cityops-topology-contract`, compass-dolt              |
| Trim `CSU_PICK_EXCLUDE`                               | credential-safety change, not cleanup                               | comment in city.toml; `cityops-topology-contract`      |
| De-uniform `fork_flag` / re-add `pin = true`          | breaks account fungibility contract (2026-07-05)                    | `cityops-topology-contract`                            |
| "Fix" the stale mayor-pin comment or value            | flagged open item for Stephanie; NO CHANGE is the recorded decision | morning ledger 2026-07-07; `cityops-topology-contract` |
| Delete a `.disabled` order or an INTERIM drop-in      | retirement rationale / upstream-fix condition may still be pending  | the file's own header                                  |

## Human gates (provisional)

Per the morning-ledger 2026-07-07 provisional positions (Q2/Q3, not yet
confirmed by Stephanie): **city.toml topology changes and account/credential
changes require her per-action approval**, alongside the always-external
gates (pushes, PRs, comms) owned by CLAUDE.md and the global autonomy
boundary. Dry-run→apply promotions are hers by the convention above. No
automated subsystem in this city is documented as trusted-unsupervised;
default to spot-check. Pausing an order in an incident (the
maintenance-cycle case) was done on mayor authority with a mail reference —
if you act on delegated authority, the comment must cite it.

## Worked example: the 2026-07-06 maintenance-cycle pause, maker's side

This skill OWNS the maintenance-cycle pause story; sibling skills cite it and
carry only a live-state check (`grep -A2 'orders.overrides' city.toml`). When
the override is lifted, update this section first.

The full loop, reconstructed from host evidence and re-verifiable:

1. **Authority**: mayor mail gc-454759; RCA beads gc-454658/gc-454686
   (worktree-provisioning bug re-spawning polecats, supervisor pegged ~360%).
2. **Snapshot**: `city.toml.bak-pause-maintenance-cycle-20260706T175816Z`
   created 13:58:16 EDT.
3. **Edit** 17 seconds later (city.toml mtime 13:58:33 EDT): a new `[orders]`
   section with the 8-line RCA comment + 3-line override, nothing else —
   `diff` shows 13 added lines, 0 removed. One concern.
4. **Took effect without a restart**: `gc order history maintenance-cycle`
   shows the last fire at `2026-07-06T11:57:25-04:00` and none after, despite
   a 120m cooldown cadence (verified 2026-07-07, ~34h of silence). The
   supervisor's own restart came hours later (23:56 EDT) and is unrelated.
5. **Undo condition recorded** in the comment: remove the override once the
   worktree-provisioning fix lands, then re-dispatch the preserved #2713
   candidate.

That is the template. If your change cannot produce this shape — a snapshot,
a one-concern diff, a four-part comment, and a read-command proof it took —
it is not ready to make.

## Provenance and maintenance

All claims verified on-host 2026-07-06/07 by a read-only session. One-line
re-checks for the drift-prone facts:

| Claim                                  | Re-verify with                                                                                        |
| -------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| City root is not a git repo            | `ls -d /home/ds/gas-city/.git` (expect: No such file or directory)                                    |
| 5 city.toml snapshots; newest name     | `ls /home/ds/gas-city/city.toml*`                                                                     |
| 17s snapshot-to-edit gap               | `stat -c '%y %n' /home/ds/gas-city/city.toml{,.bak-pause-maintenance-cycle-20260706T175816Z}`         |
| Exactly one live order override        | `sed -n '/^\[orders\]/,$p' /home/ds/gas-city/city.toml`                                               |
| maintenance-cycle still silent         | `gc order history maintenance-cycle` (last fire 2026-07-06T11:57:25-04:00)                            |
| 91 order files, 2 `.disabled`          | `ls /home/ds/gas-city/orders/ \| wc -l; ls /home/ds/gas-city/orders/*.disabled`                       |
| Janitor promotion annotations          | `grep -n "flipped by Stephanie" /home/ds/gas-city/orders/janitor-*.toml`                              |
| 9 supervisor drop-ins                  | `ls ~/.config/systemd/user/gascity-supervisor.service.d/`                                             |
| `gc order check` still slow (>90s)     | `timeout 90 gc order check; echo $?` (143 = still timing out)                                         |
| Reconciler tick interval (2m)          | `grep patrol_interval /home/ds/gas-city/city.toml`                                                    |
| Human-gate positions still provisional | `grep -n "PROVISIONAL" /home/ds/gas-city/docs/design/fable-distillation/morning-ledger-2026-07-07.md` |
