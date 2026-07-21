---
name: cityops-orders-and-patrols
description: >-
  Operate the scheduled-order fleet (`orders/`): reading/changing orders,
  misfires (never/late/false-alarm/timeout-killed), cooldown-vs-cron-vs-event
  choice, pause/re-enable, retiring a reaper after an upstream fix, and
  reconciling order counts across sources. Not writing a scanner
  (compass-scanners) or supervisor recovery (compass-tmux-supervisor).
---

# City ops: orders and patrols

The order fleet is this city's autonomic nervous system: ~97 scheduled or
event-triggered jobs that reap leaks, nudge stalled dispatch, surface problems
to Slack, and hold the floor against known upstream bugs. This skill owns the
fleet-level view — the census, the trigger doctrine, the trap catalog, the
change-control conventions, and the mitigation registry (which orders are
stopgaps and when each may be retired). It does not own individual scanner
internals or the machinery index.

**Terms** (used throughout, defined once):

| Term                       | Meaning here                                                                                          |
| -------------------------- | ----------------------------------------------------------------------------------------------------- |
| order                      | One scheduled/event job: a TOML in `orders/` (or shipped by a pack) run by the gc controller          |
| reaper                     | Order that deletes/resets leaked state (procs, claims, worktrees, labels)                             |
| patrol                     | Order that scans and nudges/reports but does not destroy (flow-patrol, dispatch-patrol)               |
| watchdog                   | Order that verifies another order or session actually did its job (morning-triage-watchdog)           |
| cycle                      | Order that injects a concrete work payload into an agent queue (mechanic-cycle, maintenance-cycle)    |
| surfacer / guard / janitor | Pushes findings to Slack/mayor / protects a resource threshold / rotates or GCs artifacts             |
| in-floor mitigation        | An order that exists only because an upstream gascity bug is unfixed; carries an un-install condition |

## When NOT to use this skill

| You want                                                                   | Go to                                        |
| -------------------------------------------------------------------------- | -------------------------------------------- |
| Add/debug a scanner script, find audit logs, evidence gates                | `compass-scanners` skill                     |
| Per-scanner failure modes, close-gate rules, universal `gc order` commands | `docs/conventions/scanners.md`               |
| End-to-end anatomy of one recurring task (order → formula → drain)         | `docs/conventions/recurring-task-example.md` |
| Supervisor/tmux recovery when orders stop firing at all                    | `compass-tmux-supervisor` skill              |
| Sling/dispatch semantics that orders' exec scripts rely on                 | `compass-bead-dispatch` skill                |
| The hard Don't list (bd dolt, zombie-kill triage, push gates)              | `/home/ds/gas-city/CLAUDE.md`                |

## Census: reconciling the three counts (2026-07-07)

Three different numbers are all "correct". Know which one you are looking at:

- **`orders/` directory**: 89 enabled `.toml` files + 2 hard-disabled = 91
  entries as of this census (the 2026-07-21 order-layer consolidation deleted
  or retired many; recount with `ls orders/*.toml orders/*.toml.disabled`).
  Renaming to `.toml.disabled` is the hard-disable mechanism — current
  examples: `nudge-poll-reaper.toml.disabled`, `pl-529-recovery.toml.disabled`.
- **`gc order list`**: 97 distinct live orders. That is 88 file-backed
  (89 minus `maintenance-cycle`, disabled via a `[[orders.overrides]]` block in
  `city.toml`, see Change control below) plus 9 with **no file in `orders/`**:
  `orphan-sweep`, `reaper`, `wisp-compact`, `jsonl-export`, `nudge-mail-sweep`,
  `order-tracking-sweep`, `cascade-nudge-on-blocker-close` from gc's built-in
  core pack (source path under `~/.gc/cache/repos/.../internal/bootstrap/packs/core/orders/`),
  and `escalate-rollups` + `patrol-project-leads` from the oversight-rig pack
  (`/home/ds/gascity-packs-worktrees/oversight-rig/oversight-rig/orders/`).
  The two pack orders instantiate **per rig**, so the raw list prints ~135 rows
  (20 rows each for escalate-rollups and patrol-project-leads). `gc order show
<name>` prints the `Source:` path — that is how you tell file-backed from
  pack-shipped.
- **`docs/conventions/scanners.md` order table says eleven orders.** It is a
  snapshot from when the fleet was eleven. Its per-scanner notes and universal
  commands remain valid; its census is stale by ~8x. Trust `gc order list`.

Functional shape of the file-backed fleet (approximate, from the 2026-07-06
discovery pass): ~16 reapers, ~12 patrols, 5 watchdogs, 9 triage cycles, ~18
notifiers/surfacers, 3 account/quota managers, 9 maintenance/janitors, 6
git/PR-pipeline, ~10 audits; ~40 cooldown, ~35 cron, 7 event, 1 gate.

## Reading the schedule (and how slow it is)

```bash
cd /home/ds/gas-city
gc order list                      # every order + trigger + cadence
gc order check                     # due / not-due reason per order
gc order show <name>               # config + Source: file path
gc order history <name>            # recent fires (bead id + timestamp per fire)
```

`gc order check` and `gc order history` walk live state and are **slow**: on
2026-07-07 a healthy `gc order check` took 1m46s; on 2026-07-06 it timed out at
45s while the supervisor was pegged by a spawn storm. Give these commands a
multi-minute timeout before concluding anything is wedged. A check that never
returns is a load symptom, not an orders bug — look at supervisor CPU first
(`compass-tmux-supervisor`).

`gc order run <name>` fires an order ad hoc through the controller. Most
enabled orders run with `--apply` and mutate state (kill procs, clear labels,
post to Slack) — treat an ad-hoc fire as a real action, not a dry run.

## Trigger doctrine

Three trigger types, chosen deliberately (`docs/conventions/recurring-task-example.md`
has the syntax; this is the local doctrine for choosing):

| Trigger    | Use for                                                      | Why here                                                                                |
| ---------- | ------------------------------------------------------------ | --------------------------------------------------------------------------------------- |
| `cooldown` | Must-just-work recurring jobs (default choice)               | Self-paces on the runner's real tick; survives missed/late ticks; **no bootstrap trap** |
| `cron`     | Wall-clock jobs (daily digests, off-peak GC, 2x/day updates) | Exact slots — but carries both traps below                                              |
| `event`    | Reacting to `bead.updated` / `bead.closed` / `mail.sent`     | Reactive handlers (nudge-on-route, mail-redirect-to-mayor)                              |

### Trap 1 — cron zero-lastRun bootstrap

A **never-fired cron order only fires on an exact-minute evaluation match**
(`internal/orders/triggers.go` skips catch-up when lastRun is zero). A new
daily-at-9:30 order can sit inert for days. Seed it once:

1. Temporarily set `schedule = "* * * * *"`.
2. Wait for one controller fire (`gc order history <name>` shows it).
3. Restore the real schedule.

Or sidestep entirely: use cooldown. Order headers that made this choice say so
(`dispatcher-watchdog.toml`: "trigger=cooldown (no zero-lastRun bootstrap
trap)"). Lore source: `orders/pl-status-update.toml` header (am/pm twins
merged into it 2026-07-21).

### Trap 2 — cron evaluates in host-local time (EDT)

gc cron is **not UTC**. `orders/morning-triage-cycle.toml` carries the scar:
the schedule was written `0 11` on the UTC assumption and fired at 11:00 EDT,
4 hours late; it is now `0 7` with an inline comment confirming host-local
evaluation (verified via claude-zombie-report's `0 9` firing at 09:00 EDT).
When writing or reading any cron schedule in this city, read it as
America/New_York wall clock.

### Trap 3 — timeout sizing

`timeout` kills the exec at the deadline, and a kill mid-run can create a
**false alarm downstream**: on 2026-07-04 morning-triage-cycle's 60s timeout
killed the exec after the sling dispatched but before the "slung" audit line
was written, so the sling succeeded yet morning-triage-watchdog alarmed on the
missing event (gc-450571). It now runs `timeout = "180s"`. Sizing rules:

- Budget for the slowest observed run under load, not the median
  (a `gc-sling` alone routinely takes ~42s).
- Long classification passes need explicit headroom AND a check that
  `city.toml [orders] max_timeout` (if set) is not lower — see the note in
  `orders/janitor-worktree-gc.toml` (1200s for ~350 worktrees).
- If the exec writes an audit line that a watchdog reads, the timeout must
  cover the write, not just the action.

### Trap 4 — `idempotent = true` is a fail-open declaration

Under open-work-gate contention/timeout (gastownhall/gascity#2893),
`idempotent = true` lets an order run anyway (fail OPEN). Set it **only** when
a duplicate run is provably harmless, and say why in a comment — every current
use does (re-clearing a cleared label, re-killing a dead PID). The
`nudge-poll-reaper.toml` header records the sharpest case: the gate can be
starved BY the very CPU runaways that reaper kills, so failing closed would be
a deadlock (mayor, 2026-07-06). Current `idempotent = true` orders:
blocked-routed-reaper, nudge-poll-reaper, gate-sweep, idle-session-report,
resource-sweep.

## Change control for orders

- **Pause via override, not deletion.** The live mechanism is a
  `[[orders.overrides]]` block in `city.toml` with `name = "<order>"` and
  `enabled = false`, preceded by a comment naming the RCA beads and the
  explicit exit condition. Live example: `maintenance-cycle` — originally a
  2026-07-06 pause, now a **permanent retirement**: the Temporal
  maintenance-Run Schedule is the sole driver of maintenance-cycle dispatch
  since the gc-372 P5 cutover (2026-07-16), and re-enabling the order would
  double-dispatch. Never flip it back; per the comment (RCA gc-qo3) the file
  moves to `.toml.disabled` after a clean Temporal week (~2026-07-23).
  Hard-disable by renaming to `.toml.disabled` is the heavier form, used when
  the order shape itself is broken or the order is retired with a re-enable
  condition (current examples: `nudge-poll-reaper.toml.disabled`,
  `pl-529-recovery.toml.disabled`; `rig-patrol.toml.disabled` — the only
  native formula+pool order, shape regressed upstream gascity#1440 — was
  deleted outright 2026-07-21, its story lives in git).
- **The comment header is the changelog.** Every non-trivial order file opens
  with why it exists, the incident/bead IDs, cadence-change history, and (for
  mitigations) the un-install condition. When you edit an order, extend the
  header; when you are about to "clean up" a weird setting, read the header
  first — it usually names the incident that put it there.
- **Destructive orders promote dry-run → apply, human-flipped.** The pattern:
  ship report-only, accumulate clean dry-run history, then Stephanie flips the
  env/flag and the flip is date-stamped in the file. Examples:
  `janitor-log-rotate.toml` (`JANITOR_LOG_EXECUTE = "1"  # flipped by
Stephanie 2026-07-04 after clean dry-run review`), `janitor-worktree-gc.toml`
  (same date, `JANITOR_MODE = "--execute"`, plus a `JANITOR_MAX_REMOVE = "25"`
  per-tick blast-radius cap), `bead-janitor` (`--apply` added 2026-05-20 after
  14d clean dry-run). Do not flip a dry-run order to apply yourself; that
  promotion is a human decision (provisional position, morning-ledger
  2026-07-07: no subsystem is documented trusted-unsupervised without
  Stephanie's word).
- **City topology / `city.toml` changes are human-gated** (provisional, same
  ledger entry), and the local convention is a `.bak` snapshot immediately
  before a risky flip. Adding a new order file is additive and lower-risk, but
  anything that posts externally (Slack, GitHub) inherits the external-artifact
  approval gate in CLAUDE.md.

## Mitigation registry: stopgap orders and when they may retire

These orders are load-bearing workarounds for open upstream bugs. Removing one
before its condition is met reintroduces a known outage. Each header carries
the full story; this table is the retirement index (verified in-file
2026-07-07):

| Order                                                          | Covers                                                                                                                                                               | Retire when                                                                                                    |
| -------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `blocked-routed-reaper` (15m)                                  | Workflow-root spawn path reads an unfiltered projection (`orders_feed.go:292`) and respawns no-op polecats for blocked routed beads (gc-nby7oo class, RCA gc-453188) | Upstream status-gate lands in gascity source                                                                   |
| `gascity-nudge-poll-reaper` systemd user timer (2m; the order leg retired to `.toml.disabled` 2026-07-21 — the timer fires independent of order-firing stalls) | Leaked `gc nudge poll` sidecars busy-loop at 300–650% CPU after session restarts (gc-b9w88; caused the 2026-06-24 all-day nudge hangs)                               | gc-source poll backoff ships (gc-b9w88, held needs-source-impl)                                                |
| `nudge-on-route` (event) + `routed-bead-nudger` (15m)          | `gc sling` does not wake warm/asleep pools (gascity#1129, by-design); event handler covers active sessions, the 15m sweep covers asleep pools                        | #1129 behavior changes upstream (do not hold your breath: by-design)                                           |
| `morning-triage-cycle` exec-wrapper shape (`maintenance-cycle` retired — Temporal Schedule drives it since 2026-07-16; file → `.toml.disabled` ~07-23) | Native formula+pool orders regressed upstream (#1440); cycles exec-wrap `gc-sling` instead                                                                           | gastownhall/gascity#1986 lands + supervisor restarts; then restore the native formula+pool shape (ex-`rig-patrol.toml.disabled`, deleted 2026-07-21 — recover from git) |
| 529-wedge classifier inside `polecat-ui-stuck-scanner` (5m; the `pl-529-recovery` order retired to `.toml.disabled` 2026-07-21 after 2 months / zero detections — classifier is surface-only, no auto-reset) | PL sessions wedge on Claude API 529 Overloaded; two-scan confirmation, mayor excluded                                                                                | Claude Code retry path stops wedging sessions                                                                  |
| `dispatcher-watchdog` (30m)                                    | control-dispatcher (gc-2790) wedges ~6h after reset; only `gc session kill` clears the tripped circuit breaker                                                       | Upstream dispatcher fix                                                                                        |
| `escalate-surfacer` (15m)                                      | oversight-rig pack's `escalate-rollups` only resolves pack-named PL sessions; bespoke-PL rigs' escalate rollups rot undelivered                                      | Pack resolver becomes naming-independent                                                                       |
| `gascity-main-pin-guard` (15m)                                 | A detached `/home/ds/gascity-main` silently freezes the installed binary (4-day incident 2026-05-25)                                                                 | Never — cheap permanent guard                                                                                  |
| `pool-worktree-provision-check` (30m, report-only)             | Pool members on never-provisioned base worktrees strand code beads (gc-dvvym; polecat-5/6 sat dead 2 days)                                                           | pre_start template substitution provisions city-level pool worktrees                                           |
| `dolt-gc-maintenance` (04:30 nightly)                          | Managed dolt runs with auto-GC OFF assuming a scheduled GC that was never wired; garbage hit 2.4GB and blew the systemd stop timeout (2026-05-25 SIGKILL)            | gc wires its own scheduled dolt GC                                                                             |

## Doctrine encoded across the fleet

- **Mayor is always the responder, never the trigger.** Delivery to Stephanie
  goes straight to Slack from the order's script; mayor gets a copy/nudge to
  respond with. Stated verbatim in `escalate-surfacer.toml` and
  `stall-watch.toml` — stall-watch exists precisely because the old path
  ("nudge mayor to surface at next interaction") kept stalls invisible until
  she asked.
- **The monitoring trio** (from `dispatch-patrol.toml`): `flow-patrol`
  (2x/day, positive "is work flowing" digest to #all-agent-city),
  `stall-watch` (15m, breakage push to #gascity-maintenance),
  `dispatch-patrol` (30m, undispatched-backlog nudges; skips gascity's gated
  PR queue, paces mem). Extend the right leg rather than adding a fourth.
- **Watchdog pairs.** Anything with a silent-miss history gets a paired
  verifier reading the audit trail one slot later: `morning-triage-cycle`
  (7:00) + `morning-triage-watchdog` (8:00, born from the 2026-05-14 silent
  miss); `pl-loop-close` (event) + `pl-loop-close-timeout` (sweep). New
  critical cron order → add the watchdog in the same change.
- **Patrol nudges are liveness pings, not kick mechanisms.** Nudges hang
  intermittently, so a bare `gc session nudge` patrol
  (`patrol-city-infra-pl`) is complemented by a cycle that injects a
  self-contained priority-ranked payload (`mechanic-cycle`); the pair is
  deliberate, not redundant (rationale in `mechanic-cycle.toml`).
- **Autonomy is bounded per order.** `approved-pr-automerge` only ENABLES
  GitHub auto-merge behind a current Codex-inclusive approve-record gate and
  never merges directly; report-only orders (`pool-worktree-provision-check`,
  `idle-session-report`) say "never drains/kills/provisions" in their headers.
  When authoring a new order, state its action ceiling the same way. No order
  may weaken a human gate, and none is documented trusted-unsupervised
  (provisional, morning-ledger 2026-07-07).
- **Reapers write JSONL audit logs** at `.gc/<order>.log` and nudge mayor on
  action. Log locations and reading conventions: `compass-scanners`.

## Worked example: reading a mitigation's heartbeat

Question an operator actually faces: "blocked-routed-reaper fires every 15
minutes — is it still needed, or did the upstream fix land?" Answer from the
live evidence (2026-07-07):

```bash
cd /home/ds/gas-city
gc order history blocked-routed-reaper | head -4
# ORDER                 BEAD            EXECUTED
# blocked-routed-reaper gc-456452       2026-07-06T23:51:15-04:00
# blocked-routed-reaper gc-456407       2026-07-06T23:30:38-04:00
# blocked-routed-reaper gc-456325       2026-07-06T22:55:16-04:00

tail -3 .gc/blocked-routed-reaper.log
# {"ts":"2026-07-07T03:30:41Z","event":"cleared","db":"gascity","bead":"gc-i7a8c","rig":"gascity"}
# {"ts":"2026-07-07T03:30:42Z","event":"cleared","db":"gascity","bead":"gc-73cv2","rig":"gascity"}
# {"ts":"2026-07-07T03:51:17Z","event":"cleared","db":"gascity","bead":"gc-73cv2","rig":"gascity"}
```

Read: the order is firing on schedule (history), and it is still finding work
— bead gc-73cv2 had `gc.routed_to` cleared at 03:30:42Z and had **re-acquired
it within 21 minutes** (re-cleared 03:51:17Z). That live re-accumulation
matches the header's recorded rate ("5 offenders re-accumulated within ~1h of
a manual sweep on 2026-07-05") and is exactly why the header prescribes a
tight 15m cooldown. Verdict: upstream gate has not landed; the mitigation
stays. The same three-step read (history → audit log → header) answers
"retire it?" for every order in the mitigation registry: an empty audit log
over days is the retirement signal to raise (to Stephanie — retirement is a
config change, human-gated), a busy one is proof of continued need.

The sibling log makes the stakes concrete — `.gc/nudge-poll-reaper.log` the
same night shows `{"ts":"2026-07-07T03:36:35Z","event":"reaped","pid":"649214","cpu":"655",...,"session":"mayor gc-2568"}`:
a leaked poll sidecar burning 6.5 cores on the mayor session, killed within
its 2m cadence.

## Provenance and maintenance

Written 2026-07-07 against the live workspace; discovery basis
`docs/design/fable-distillation/discovery-cityops.md` (2026-07-06). Positions
marked "provisional" trace to the morning-ledger 2026-07-07 city-ops answers
and are revisable by Stephanie. Re-verify volatile facts:

| Claim                                          | Re-verify with                                                                                       |
| ---------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| 89 enabled + 2 disabled files in `orders/`     | `command ls /home/ds/gas-city/orders/ \| wc -l` and `command ls /home/ds/gas-city/orders/*.disabled` |
| 97 distinct live orders, ~135 rows             | `gc order list \| tail -n +2 \| awk '{print $1}' \| sort -u \| wc -l`                                |
| maintenance-cycle retired via override (Temporal drives it) | `grep -n -A2 '\[\[orders.overrides\]\]' /home/ds/gas-city/city.toml`                                 |
| 9 pack/built-in orders without files           | `gc order show orphan-sweep \| grep Source:` (and compare list vs `orders/` basenames)               |
| `gc order check` latency (1m46s on 2026-07-07) | `time gc order check >/dev/null`                                                                     |
| idempotent=true set on 5 orders                | `grep -l '^idempotent = true' /home/ds/gas-city/orders/*.toml`                                       |
| Mitigation-registry cadences/conditions        | `head -30 /home/ds/gas-city/orders/<name>.toml` (header is authoritative)                            |
| Janitor apply flags still human-flipped        | `grep -n 'JANITOR_LOG_EXECUTE\|JANITOR_MODE' /home/ds/gas-city/orders/janitor-*.toml`                |
| scanners.md census still stale                 | compare its order table against `gc order list`                                                      |
