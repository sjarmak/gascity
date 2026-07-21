---
name: cityops-session-and-account-management
description: >-
  Operate the five claude OAuth accounts and gc session population:
  credentials-error launches, expired/rotting tokens, which account an
  agent runs under or moving it, surprising claude-auto picks, zombie
  triage, dormant/wedged pool sessions. Covers ~/.claude-homes isolation,
  claude-account, csu_pick, token keepalive, /ds-cred. Not rate-limit
  rebalancing (compass-capacity) or supervisor recovery
  (compass-tmux-supervisor).
---

# City ops: session and account management

Runbook for the account layer (five Claude OAuth identities) and the session
layer (the gc-managed Claude processes running under them) of the ds-research
city at `/home/ds/gas-city`. Facts are dated; drift-prone ones have a
re-verification one-liner in "Provenance and maintenance" at the end.

**When NOT to use this skill:**

| Your problem                                                 | Go to instead                                                                                                  |
| ------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------- |
| Agent is rate-limited, needs to move accounts NOW            | CLAUDE.md "Do" list + `compass-capacity` + `docs/conventions/capacity.md` (full `gc-capacity` command catalog) |
| Heavy batch of `claude -p` one-shots (>100 calls)            | `docs/conventions/heavy-batch-claude-calls.md`                                                                 |
| Supervisor dead, tmux dead, sessions drained as orphans      | `compass-tmux-supervisor` + `docs/conventions/tmux-supervisor.md`                                              |
| You are an ad-hoc guest session wondering what you may touch | `docs/conventions/guest-session-primer.md`                                                                     |
| oomd killed something / memory pressure                      | `compass-capacity` (`scix-batch`)                                                                              |

## 1. The account model (verified 2026-07-07)

Five Claude Max OAuth accounts, each with a fully isolated Claude Code home:

```
/home/ds/.claude-homes/account<n>/           # n = 1..5
├── .claude/                                 # CLAUDE_CONFIG_DIR for this account
│   ├── .credentials.json                    # the OAuth blob — the ONLY per-account secret
│   ├── skills -> /home/ds/.claude/skills    # shared config, symlinked in on first launch
│   ├── hooks  -> /home/ds/.claude/hooks     # (also: commands, rules, agents, mcp-configs, …)
│   └── settings.json                        # may be a REAL per-account file (see below)
├── .claude.json                             # onboarding/trust state (parent of CONFIG_DIR)
└── .claude-account.lock                     # flock serializing concurrent bootstraps
```

- Isolation exists to prevent the credential race: concurrent sessions on a
  shared `~/.claude/.credentials.json` overwrite each other's refreshed
  tokens. Each account writes only its own blob.
- `/home/ds/gas-city/bin/claude-account <1-5|auto> [args...]` is the single
  launcher. It exports `CLAUDE_CONFIG_DIR`, flock-serializes bootstrap (bd
  issue gc-arr), seeds the symlinks (existing files are left alone — that is
  why account1's `settings.json` is a real file while `skills/` is a
  symlink), pre-accepts onboarding and the trust dialog for `$GC_WORK_DIR`,
  then `exec claude --dangerously-skip-permissions "$@"`.
- Convenience wrappers in `~/.local/bin`: `claude-1` … `claude-5` (one-line
  execs of `claude-account <n>`) and `claude-auto` (usage-aware picker).
- Every `city.toml [providers.claude-N]` block runs
  `/home/ds/gas-city/bin/claude-account` with `args = ["<n>"]`; provider
  `claude-auto` passes `["auto"]`. `fork_flag = "--fork-session"` is uniform
  across all five (2026-07-05) and inert unless a session bead carries
  `gc.brain_parent_sid` — do not "clean it up".
- The supervisor unit itself runs with
  `CLAUDE_CONFIG_DIR=/home/ds/.claude-homes/account4/.claude` (verified in
  `systemctl --user cat gascity-supervisor`, 2026-07-07). Anything an order
  execs that calls bare `claude` without a launcher lands on account4's
  identity — route scripts through `claude-auto` (heavy-batch doc owns that
  rule).

## 2. Which account picks whom: `claude-account auto` / csu_pick

`claude-account auto` delegates to `/home/ds/gas-city/bin/csu_pick.sh`, which
reads `~/.claude-usage/usage_cache.json` (refreshing via `csu` when older than
15 min) and prints an account number. Selection rule, in order:

1. Skip accounts at >= 95% 7-day utilization (exhausted).
2. Skip accounts in `CSU_PICK_EXCLUDE`.
3. **Rot-preference:** if any remaining account's OAuth token expires within
   2h (`CSU_PICK_EXPIRY_PRESSURE_HOURS`), route this launch there so a real
   session refreshes it.
4. Otherwise sort by (7d util, 5h util) and pick randomly among the top 2
   (`CSU_PICK_TOP_K=2`, so burst spawns don't herd onto one account between
   15-min cache refreshes).

Fallback on any error: prints `1`.

**The CSU_PICK_EXCLUDE trap (dated 2026-06-20, still live 2026-07-07).**
`EXCLUDE` defaults to `claude-2,claude-3,claude-4` **inside csu_pick.sh
itself**, not just in `city.toml [providers.claude-auto.env]`. Reason, from
the script's own comment: a headless `claude-auto` launch steered onto a
near-expiry account writes `.credentials.json` back **with the refresh token
stripped**, clobbering fresh credential copies; accounts 3 and 4 were
repeatedly broken this way. The city.toml env route was tried first and did
not reach launches (the supervisor caches provider env from startup), so the
fix moved into the script default, which is read fresh per launch. Both
copies carry the same instruction: leave the exclusion in place until the
launch-writeback bug is fixed, even after the rot issue clears. Practical
consequence: **claude-auto currently only ever picks account 1 or 5.** Do not
"fix" that as a load imbalance.

Note `~/.local/bin/claude-auto` is a second, older picker with its own logic
(5h >= 80% cutoff, no exclusion list, no rot-preference) that also execs
`claude-account`. City providers use `claude-account auto` (csu_pick); shell
users and batch scripts typically use `claude-auto`. They can disagree about
"best account"; that is expected.

## 3. Token lifecycle and recovery (three layers)

Tokens live 8h. A headless launch does not necessarily rotate a token while
it is still valid, so an unchanged pre-expiry `expiresAt` is not a refresh
failure. An **expired** access token can still be recovered headlessly while
its refresh token remains valid; this was verified on account3 and account5
on 2026-07-15. `/ds-cred account<n>` is needed only when that post-expiry
recovery fails.

| Layer | What                                                                                                                                                  | Schedule                                                                  | Where                                                                     |
| ----- | ----------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| 1     | `claude-refresh-all` — force-expires each blob then `claude -p ok` under `HOME=account<n>` to trigger SDK refresh                                     | user crontab, `0 1,7,13,19 * * *` ET (6h cadence, 8h tokens = 2h overlap) | `~/.local/bin/claude-refresh-all`, log `/home/ds/logs/claude-refresh.log` |
| 2     | csu_pick rot-preference — organic launches steered to the nearest-expiry account                                                                      | per `claude-auto` launch                                                  | `bin/csu_pick.sh` step 3                                                  |
| 3     | `account-keepalive` — leaves valid tokens alone; after expiry, tries headless recovery and mails mayor only if recovery fails. Alerts dedupe by account + expiry epoch. | cron `*/15 * * * *` | `bin/account-keepalive`, `orders/account-keepalive.toml` |

Layer 3 is verify-then-escalate after expiry. It checks that `expiresAt`
advanced to a valid future time before declaring recovery. A still-valid
token never launches Claude and never pages. The former `account-1h-warning`
pre-expiry Slack alarm was retired on 2026-07-16 because it encoded the same
false assumption and duplicated the recovery order.

**Ground truth for token state** is each account's `.credentials.json`
(`claudeAiOauth.expiresAt`, epoch ms), not the usage cache:

```bash
for n in 1 2 3 4 5; do printf 'account%s: ' $n; python3 -c "
import json,datetime,time
e=json.load(open('/home/ds/.claude-homes/account$n/.claude/.credentials.json'))['claudeAiOauth']['expiresAt']/1000
print(datetime.datetime.fromtimestamp(e,datetime.timezone.utc).strftime('%Y-%m-%dT%H:%MZ'),'({:+.1f}h)'.format((e-time.time())/3600))"; done
```

Healthy output (captured 2026-07-07T04:0xZ): all five accounts showing the
same next refresh-cron boundary, e.g. `account1: 2026-07-07T07:00Z (+3.0h)`.
An account showing a negative offset is expired. The 15-minute recovery order
will attempt a headless refresh, verify the new expiry, and surface
`/ds-cred` only if that attempt fails.

Usage visibility: `csu` refreshes `~/.claude-usage/usage_cache.json`
(per-account `five_hour`/`seven_day` utilization + `token_expires_at`);
`gc-capacity` renders the dashboard. The cache reflects **API** limits, not
the consumer limits agents actually hit — that gap and the `--force` rule are
owned by `docs/conventions/capacity.md`.

## 4. Who runs where: assignment, pins, and the auto-drain

- Assignment lives in `city.toml`: `[workspace] provider = "claude-4"` is the
  default; per-rig `[[rigs.overrides]]` set PLs to `claude-auto`;
  `[[patches.agent]]` entries override per agent. **The patch value is what
  takes effect at launch.**
- All per-agent `pin = true` locks were removed 2026-07-05 ("accounts are
  fungible, handled by quota"): any agent may be rebalanced to any account.
  `gc-capacity --rebalance` refuses pinned agents, so a future re-pin is an
  explicit opt-out of auto-drain.
- A provider change only takes effect via `gc session reset <agent>` — the
  session cycles and resumes in the new account home **without its
  transcript** (mayor's context handoff covers that for mayor).
- **Mayor pin state (RESOLVED by Stephanie 2026-07-07):** the mayor's account
  assignment is intentionally arbitrary — accounts are identical except for
  accumulated usage, so which one hosts the mayor "shouldn't matter." The
  operative rule is quota-spreading: prioritize balancing consumption across
  the five accounts over any per-agent affinity. The stale claude-3 comment
  in city.toml is historical, not intent (`gc-capacity` updates value lines
  on a move but not comments — read comments as changelog, never as current
  state).
- **Auto-drain is live:** `orders/account-quota-warning.toml` runs every 30
  min with `QUOTA_AUTO_DRAIN=1` (added 2026-07-05 after account4 coasted to
  92% 7-day while the order was alert-only and workers throttled into silent
  no-op loops). Over 75% 7d it Slack-warns AND runs
  `gc-capacity --rebalance auto --no-reset` + `gc reload`, so sessions cycle
  on the reconciler's staggered cadence rather than a reset storm. Provisional
  trust position (morning-ledger 2026-07-07): no subsystem is documented as
  trusted-unsupervised without Stephanie's word — after an auto-drain fires,
  spot-check `gc-capacity` and `gc session list` rather than assuming the
  moves landed cleanly.

## 5. Session layer: what you may do to sessions

`gc session` subcommands (verified 2026-07-07): `new, list, attach, submit,
suspend, pin, unpin, reset, close, rename, prune, peek, kill, nudge, logs,
wake, wait`. Read-only triage uses `list`, `peek`, `logs`; everything else
mutates live agent state — have a reason, and prefer letting the standing
orders do their jobs:

| Order                   | Cadence     | What it does (and does NOT do)                                                                                                                                                                                                                                                             |
| ----------------------- | ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `session-prune-dormant` | 24h         | `gc session prune --state asleep,drained,suspended --before 14d`. Active sessions are never pruned (gc refuses).                                                                                                                                                                           |
| `idle-session-report`   | 1h          | **REPORT-ONLY** calibration for a future transcript-idle reaper (mayor spec gc-414556). Records transcript-JSONL idle vs dashboard-idle to `.gc/idle-session-report.log`; nudges mayor at the 4h line. Auto-kill is intentionally NOT wired and stays Stephanie-gated. Do not "finish" it. |
| `claude-zombie-report`  | daily 09:00 | Reports stale non-gc claude processes to mayor. Output is NOT a kill list (§6).                                                                                                                                                                                                            |

Reading `gc session list`: `asleep` + reason `config-drift` (several
mem-workers show this, 2026-07-07) means the session's config changed under
it and the reconciler parked it — it is not a crash. Reason `session,config`
on an active row is normal. The mayor session (`gc-2568`, 77 days old) is
`pin`ned and always-on; never close/reset it casually — resets lose the
transcript (§4).

A pool worker that ran `exit 0` without `gc session close` wedges its slot
(the reconciler still reads it active; beads queue behind it). Recovery for
wedged slots and the full `gc doctor` ladder belongs to
`docs/conventions/tmux-supervisor.md` and the CLAUDE.md "Do" list, not here.

## 6. Zombie triage runbook

`bin/claude-zombie-report` lists `claude` processes older than 3 days
(`--min-age <seconds>` to change) that are NOT gc-managed. A process is
gc-managed if `GC_SESSION_ID` is in its environ **or** it (or an ancestor) is
a live tmux pane on the `ds-research` socket. CLAUDE.md owns the hard rule
(don't blanket-kill); this is the procedure behind it:

1. Run `/home/ds/gas-city/bin/claude-zombie-report` (stdout only; the daily
   order adds `--nudge-mayor`).
2. For each flagged PID, read its `AGENT | CWD` column. `(interactive)` or
   `(resumed)` + a CWD under a real project = probably Stephanie's own tmux
   work. Leave it.
3. Cross-check tmux yourself before believing "not in tmux":
   `tmux -L ds-research list-panes -a -F '#{pane_pid} #{session_name}'` and
   walk the flagged PID's ancestry (`ps -o ppid= -p <pid>`). If tmux is down
   when the report runs, the tmux filter silently self-disables and
   EVERYTHING gc looks like a zombie.
4. Only a process that is aged, unmatched in tmux, without `GC_SESSION_ID`,
   and whose CWD is gone or clearly abandoned is a kill candidate — and
   killing is still a surfaced decision, not an autonomous action ("Do not
   kill unilaterally" is in the report's own mayor-nudge text).

**Worked example (real incident, 2026-06-28, mayor bead gc-429945).** The
report's original filter was env-only (`GC_SESSION_ID` in
`/proc/<pid>/environ`). It false-flagged **six live gc workers** —
`enterprisebench-worker-gc-404898`, several `mem-worker-gc-*`, and
`city-infra-pl` — because the flagged PID didn't carry `GC_SESSION_ID` in its
readable environ. Blindly killing that report would have taken down a
project lead and five active workers. The fix (documented in the script
header) added the tmux-pane ancestry cross-reference as a second exclusion.
Moral: the report is a _lead generator_; the tmux + CWD walk is the actual
test.

## 7. Human gates (provisional trust map, morning-ledger 2026-07-07)

Per-action Stephanie approval, regardless of how routine it looks:

- Running `/ds-cred` or otherwise replacing any `.credentials.json`.
- Editing `CSU_PICK_EXCLUDE` (either copy), account launchers, or the refresh
  cron.
- Adding/removing accounts or changing provider blocks in `city.toml`.
- Killing anything from a zombie report.
- Re-pinning or resetting mayor.

These are marked **provisional** pending Stephanie's answer to discovery Q2/Q3
(permanent-gates and trust-map questions in
`docs/design/fable-distillation/discovery-cityops.md` §9); until then nothing
in this layer is documented as trusted-unsupervised.

## Provenance and maintenance

All facts verified on-host 2026-07-07 against the live workspace. One
re-verification line per drift-prone claim:

| Claim                                                          | Re-verify with                                                                                                                                    |
| -------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| Launcher behavior / symlink seeding                            | `cat /home/ds/gas-city/bin/claude-account`                                                                                                        |
| csu_pick rule + EXCLUDE default (`claude-2,claude-3,claude-4`) | `grep -n 'CSU_PICK_EXCLUDE' /home/ds/gas-city/bin/csu_pick.sh /home/ds/gas-city/city.toml`                                                        |
| Refresh cron times (01/07/13/19 ET)                            | `crontab -l \| grep claude-refresh`                                                                                                               |
| Refresh actually working                                       | `tail -8 /home/ds/logs/claude-refresh.log` (expect `REFRESHED expiry=8.0h rc=0` x5)                                                               |
| Expiry recovery cadence and alert policy                       | `grep -n 'schedule\|description' /home/ds/gas-city/orders/account-keepalive.toml; grep -n 'hte > 0\|alert already sent' /home/ds/gas-city/bin/account-keepalive` |
| Token expiry ground truth                                      | the 5-account one-liner in §3                                                                                                                     |
| Mayor provider (claude-5 in both files as of 2026-07-07)       | `grep -n 'provider' /home/ds/gas-city/agents/mayor/agent.toml; grep -A4 '\[\[patches.agent\]\]' /home/ds/gas-city/city.toml`                      |
| Auto-drain armed                                               | `grep -n 'QUOTA_AUTO_DRAIN' /home/ds/gas-city/orders/account-quota-warning.toml`                                                                  |
| Idle reaper still report-only                                  | `grep -n 'REPORT-ONLY\|NOT wired' /home/ds/gas-city/orders/idle-session-report.toml`                                                              |
| Supervisor's account home (account4)                           | `systemctl --user cat gascity-supervisor \| grep CLAUDE_CONFIG_DIR`                                                                               |
| gc session subcommand set                                      | `gc session` (bare; prints the list)                                                                                                              |
