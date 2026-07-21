---
name: cityops-mail-and-coordination
description: >-
  Agent-to-agent and agent-to-human coordination in this install: reaching
  Stephanie, surfacing a decision/blocker, verifying a Slack post landed,
  debugging silent channels or orphaned bindings, the
  DECISION:/BLOCKED-ON-HUMAN: subject protocol, dead-letter redirect, and
  STATUS_UPDATE/DEEP_AUDIT cadence.
---

# City Ops: Mail and Coordination (ds-research)

How messages move in this city and how the human actually gets reached.
All paths are machine-local to this installation. Live facts verified
2026-07-06/07 (EDT) and date-stamped where volatile.

## When NOT to use this skill

| You need                                                                 | Go to                                                                                  |
| ------------------------------------------------------------------------ | -------------------------------------------------------------------------------------- |
| `gc mail` command syntax (send/read/reply/archive)                       | `/gc-mail` skill (core pack)                                                           |
| Dispatching work, sling/nudge semantics                                  | `compass-bead-dispatch`, sibling `cityops-dispatch-and-formulas`                       |
| Order/trigger mechanics (cron traps, cooldowns)                          | sibling `cityops-orders-and-patrols`; reapers index in `compass-scanners`              |
| Recovering a wedged supervisor/session                                   | `compass-tmux-supervisor`, sibling `cityops-debugging-playbook`                        |
| Conduct rules for an ad-hoc human-launched session                       | `docs/conventions/guest-session-primer.md`, sibling `cityops-guest-session-discipline` |
| Mayor's reply format to Stephanie (banner, TL;DR, Open-Decisions ledger) | `agents/mayor/prompt.template.md` (owns it; do not restate)                                     |
| The Don't list (dolt, sling, push gates)                                 | `/home/ds/gas-city/CLAUDE.md`                                                          |

## Terms (defined once)

- **mail** — bead-based messaging (`type="message"` beads). CLI: `gc mail …`.
- **mayor** — the always-on orchestrator session; the ONLY agent that talks to
  Stephanie routinely. Prompt: `agents/mayor/prompt.template.md`.
- **PL** — project lead, one per rig (`<rig>-pl`).
- **bound channel** — a Slack channel bound to a session via the slack
  adapter's binding registry; a PL "posting to its channel" means this.
- **adapter** — `gc-slack-adapter`, the process bridging gc sessions and
  Slack. Binary: `/home/ds/gascity-packs/slack-pack/adapter/gc-slack-adapter`.
- **dead-letter recipient** — a mail address no human reads: `human`,
  `stephanie`, `sjarmak`.

## The prime fact: Stephanie reads Slack, not gc mail

Verified 2026-07-07: `gc mail count` for the `human` mailbox shows **664
total, 664 unread**. Nothing sent to `human`/`stephanie`/`sjarmak` has ever
been read there. The only human-facing surface is Slack, and the only agent
that owns that surface routinely is mayor (source:
`bin/mail-redirect-to-mayor` header, bead dr-o3he9r).

Consequences:

1. To reach Stephanie, follow the Tier-2 protocol below. Never assume a mail
   to `human` was seen.
2. Mail to a dead-letter recipient is auto-reposted to mayor (see
   "Dead-letter redirect"), so it is not lost, but delivery latency becomes
   "whenever mayor next surfaces it to Slack".
3. External comms remain human-gated: ad-hoc/guest sessions do not post to
   Slack at all without explicit per-action approval (house autonomy
   boundary). Mayor's own Slack publishes are the documented exception
   (autonomous, per `agents/mayor/prompt.template.md`).

## Routing table

| You are                | Situation                                        | Do this                                                                                                                                                                                                                                                                      |
| ---------------------- | ------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Any agent              | Operational/infra blocker mayor can fix (Tier 1) | `gc mail send mayor -s "<summary>" -m "<detail>" --notify`. Never Slack; it is noise to Stephanie.                                                                                                                                                                           |
| A PL                   | Decision only Stephanie can make (Tier 2)        | BOTH: mail mayor with subject prefix `DECISION:` AND post 🔴 to your bound channel. No binding → add `NO-SLACK-CHANNEL-BOUND` flag in the mayor mail. SSOT: `template-fragments/pl-periodic-directives.template.md` (routing rules, maturity gate, +2-worker capacity asks). |
| A worker, stuck        | Need help mid-bead                               | Set the bead `status=blocked` with `metadata.help_request` per your worker prompt; the `help-request-surface` event order nudges mayor. Mailing mayor directly also works.                                                                                                   |
| A guest/ad-hoc session | Overlapping live work                            | `gc mail send <rig>-pl --notify --from stephanie-adhoc` per `docs/conventions/guest-session-primer.md`.                                                                                                                                                                      |
| Any agent              | Reply arrives to your own alias                  | `gc mail check` costs one cheap call; the unread-mail reminder race is documented in `agents/mayor/prompt.template.md` ("Mail self-check") and applies to any long-lived session.                                                                                                     |
| Mayor                  | Everything human-facing                          | Owned by `agents/mayor/prompt.template.md`; not restated here.                                                                                                                                                                                                                        |

## Wire protocol: subject prefixes and markers

These strings are load-bearing; automation and mayor's triage key on them.

| Marker                                                         | Meaning                                                                                | Producer → consumer                                        |
| -------------------------------------------------------------- | -------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `DECISION:`                                                    | Enters mayor's Open-Decisions ledger; pre-stages a Stephanie ask                       | PL → mayor                                                 |
| `BLOCKED-ON-HUMAN:`                                            | Mechanical block on human input/permission; raised loudly with a 🔴 channel post       | PL → mayor (fired with every DEEP_AUDIT via BLOCKED_CHECK) |
| `NO-SLACK-CHANNEL-BOUND`                                       | The Slack half of a Tier-2 surface could not fire; mayor must get the sender bound     | PL → mayor                                                 |
| `[redirect] (to <recipient>) <subject>`                        | Auto-repost of dead-letter mail; body header carries ORIGINAL SENDER/RECIPIENT/MAIL ID | `bin/mail-redirect-to-mayor` → mayor                       |
| `severity:escalate` label on a rollup bead + `delivered` label | Escalation rollup; `delivered` is the dedup stamp                                      | rig reapers → `bin/escalate-surfacer` → Slack              |
| `EXPLORATORY — not validated`                                  | Pre-validation research signal; must NOT be surfaced as 🔴 or `DECISION:`              | any PL, inside `*State:*`/FYI                              |

## The Slack delivery stack (and how it breaks)

Pipeline: session → `gc slack publish-to-channel` → gc daemon
(`127.0.0.1:8372`) → registered adapter → Slack workspace `T0B17700WUW`.

State on disk (all verified 2026-07-06):

| File                                                    | Holds                                                                                                                                                                                                                                                 |
| ------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/home/ds/gas-city/.gc/services/slack/data/config.json` | Channel bindings (room → default_handle/participants). 17 rooms bound as of 2026-07-06; PL-owned: C0B1A0CKEH0→codeprobe-pl, C0B1NSHTSKT→enterprisebench-pl, C0B25SS12CD→gascity-maintenance-pl, C0B7C62HKFH→gascity-dashboard-pl, C0B8FKL9QKB→mem-pl. |
| `/home/ds/gas-city/.gc/slack/rig_mappings.json`         | Rig → channel slash-command routing (4 rigs)                                                                                                                                                                                                          |
| `/home/ds/gas-city/.gc/slack/subteam-aliases.json`      | Slack usergroup → session alias (7 entries: mayor, cos, gc-pl, packs-pl, probe-pl, zelda-pl, mem-pl)                                                                                                                                                  |
| `/home/ds/gas-city/.gc/services/slack/logs/service.log` | Adapter runtime log (3.3 MB)                                                                                                                                                                                                                          |
| `/home/ds/.gc/slack-adapter-last-built.txt`             | Rebuild sentinel (subtree sha)                                                                                                                                                                                                                        |

Known fragility (each has a standing self-heal order; order mechanics belong
to sibling `cityops-orders-and-patrols`):

- **Pool-spawned PL bindings orphan on respawn** (bound by raw `gc-XXXX`
  session id). Symptom: channel goes silent, `extmsg: notify gc-XXXX failed:
session is closed` in logs. Self-heal: `slack-binding-reaper` order every
  5m. Full explanation owned by `docs/conventions/scanners.md` §Slack
  bindings.
- **Handle aliases go stale on PL respawn** (`@dashboard`, `@eb-pl`).
  Self-heal: `slack-handle-alias-reaper` every 10m.
- **Adapter staleness/breakage**: `slack-adapter-rebuild` order every 30m.
  As of 2026-07-06 rebuilds are **parked** "pending slack-pack/slack-full
  reconciliation (dec-44y)" (see `/home/ds/.gc/slack-adapter-rebuild.log`);
  the adapter keeps running the last-built binary.
- **city.toml registry note**: `config.json` shows many rooms with
  `default_handle: mayor`, while `agents/mayor/prompt.template.md` states mayor has no
  channel binding and receives Slack only via `@mayor:` address-by-handle.
  Open flag for Stephanie; do not "fix" either side.

### Health-check ladder (copy-paste, read-only)

```bash
# 1. Is any adapter registered with the gc daemon?
gc slack status        # "(none registered)" => outbound publishing is DOWN

# 2. What did the adapter last do?
tail -20 /home/ds/gas-city/.gc/services/slack/logs/service.log

# 3. Is the rebuild loop healthy/parked?
tail -5 /home/ds/.gc/slack-adapter-rebuild.log

# 4. Which channels are bound to whom?
python3 -c "
import json
c=json.load(open('/home/ds/gas-city/.gc/services/slack/data/config.json'))
for k,v in c['bindings'].items(): print(k,'->',v.get('default_handle'))"

# 5. Recent binding repairs?
tail -20 /home/ds/gas-city/.gc/slack-binding-reaper.log
```

If step 1 shows no adapter, do NOT restart services yourself; that is
supervisor territory (sibling `cityops-debugging-playbook`). Fall back to
mailing mayor with `--notify` and say the Slack leg failed.

## Dead-letter redirect

Order `orders/mail-redirect-to-mayor.toml` (event-triggered on `mail.sent`)
runs `bin/mail-redirect-to-mayor`: any mail addressed to `human`,
`stephanie`, or `sjarmak` is reposted to mayor with the `[redirect]` subject
and an audit header. Idempotent via
`.gc/mail-redirect-to-mayor-state.json`; mail FROM mayor is skipped to
prevent loops. The recipient list is hardcoded in the script; a new
human-ish alias must be added there or its mail rots unread (the 664-unread
`human` mailbox is what this looks like).

## Attention events (getting mayor to act without a human ping)

Standing principle from the order headers: **mayor is always the responder,
never the trigger** — scripts deliver/detect, mayor (or the script itself)
acts on wake. The coordination-relevant event orders:

| Order                                 | Fires when                                                                 | Effect                                                                                                                                                           |
| ------------------------------------- | -------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `help-request-surface`                | bead → blocked with `metadata.help_request`                                | nudges mayor now, not at next mail scan                                                                                                                          |
| `wake-mayor-on-blocker-close`         | a blocker bead closes with open dependents                                 | mayor follows through on deferred work                                                                                                                           |
| `wake-mayor-on-slung-close`           | a bead mayor slung closes                                                  | mayor polls its own dispatched work                                                                                                                              |
| `pl-human-gate-surface` (+1m recheck) | polecat creates a human-gate step bead with `originating_slack.*` metadata | posts to the originating Slack thread                                                                                                                            |
| `escalate-surfacer` (15m)             | OPEN `severity:escalate` rollups lacking `delivered` label, ANY rig        | posts each to #gascity-maintenance (C0B25SS12CD), stamps `delivered`. Naming-independent backstop for rigs whose PL name breaks the oversight-rig pack resolver. |

## Mail hygiene: the inbox is not an archive

- `mayor-mail-janitor` (daily 04:00 EDT) archives mayor-addressed mail older
  than 7 days. Do not park anything in mayor's inbox as long-term state; use
  beads or docs.
- `mail-dupe-dedupe` (hourly) archives duplicate mayor mail by
  (recipient, subject, body-sha), keeping the newest. Recurring escalators
  firing identical bodies get collapsed; your mail surviving dedup means it
  carried new content.

## Coordination cadence (all times EDT, host-local cron; verified 2026-07-06)

| When                | Order                            | What lands where                                                                        |
| ------------------- | -------------------------------- | --------------------------------------------------------------------------------------- |
| 06:30 daily         | `overnight-digest`               | overnight bead-close digest mailed to mayor `--notify`, surfaced to Slack on first wake |
| 09:30 + 16:30 daily | `pl-status-update-am/pm`         | every live PL posts State/Blockers/Decisions-needed to its channel                      |
| 09:45 + 17:00 daily | `mayor-health-surfacer-am/pm`    | mailbox/queue/bead anomalies nudged to mayor                                            |
| Mon 10:00 weekly    | `pl-deep-audit-weekly`           | DEEP_AUDIT → `<rig>/.gc-reports/` + condensed channel post + BLOCKED_CHECK              |
| continuous          | Tier-1/Tier-2 surfacing contract | see `template-fragments/pl-periodic-directives.template.md` (SSOT)                      |

Directive content (STATUS_UPDATE shape, DEEP_AUDIT sections, VAULT_NOTES
rules) is owned by the template fragment; the orders are pure triggers
(`bin/pl-periodic-directive` is plumbing by design).

## Worked example: "did my Slack post actually land?" (live, 2026-07-06)

Evidence from this host, night of 2026-07-06:

```
$ gc slack status
Adapters:  (none registered — slack inbound + outbound publishing won't work)

$ tail -3 .gc/services/slack/logs/service.log
2026/07/06 23:56:48 starting gc-slack-adapter public=:8775 internal=uds:/tmp/gcsvc-1000/... gc=http://127.0.0.1:8372 city=ds-research ...
2026/07/06 23:56:48 register adapter: register failed: 404 Not Found — {"detail":"not_found: city not found or not running: ds-research"}
```

Reading: the adapter restarted at 23:56:48 but could not register because
the gc daemon reported the city not running (supervisor churn that evening;
maintenance-cycle was paused for a worktree-provisioning bug). Every
`gc slack publish-to-channel` in that window failed silently from the
sender's perspective. Correct operator response, in order:

1. Confirm with the ladder above (status → service.log → rebuild log).
2. Do not restart the adapter or supervisor from a coordination context;
   hand off via sibling `cityops-debugging-playbook`.
3. Re-route the payload: mail mayor `--notify`, state that the Slack leg is
   down, include the intended channel id.
4. For Tier-2 decisions, keep the `DECISION:` mail flowing regardless; the
   ledger half must never depend on Slack being up.

Reliability statement (provisional trust-map position, morning-ledger
2026-07-07): none of this delivery automation is designated
trusted-unsupervised. Escalate/status posts warrant spot-checks; a silent
channel is a symptom, never proof of "nothing to report". No guidance here
weakens a human gate.

## Provenance and maintenance

Verified on-host 2026-07-06/07. One-line re-verification per drift-prone
claim:

| Claim                                           | Re-verify with                                                                                                                                                                                                              |
| ----------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `human` mailbox unread count (664 @ 2026-07-07) | `gc mail count` (defaults to `human` outside a session)                                                                                                                                                                     |
| Dead-letter recipient list                      | `grep DEAD_LETTER_RECIPIENTS /home/ds/gas-city/bin/mail-redirect-to-mayor`                                                                                                                                                  |
| Adapter registered / down                       | `gc slack status`                                                                                                                                                                                                           |
| Rebuild parked on dec-44y                       | `tail -3 /home/ds/.gc/slack-adapter-rebuild.log`                                                                                                                                                                            |
| Channel bindings (17 rooms @ 2026-07-06)        | the python one-liner in the health ladder                                                                                                                                                                                   |
| Subteam aliases (7)                             | `cat /home/ds/gas-city/.gc/slack/subteam-aliases.json`                                                                                                                                                                      |
| Cadence times                                   | `grep -H schedule /home/ds/gas-city/orders/pl-status-update-*.toml /home/ds/gas-city/orders/overnight-digest.toml /home/ds/gas-city/orders/mayor-health-surfacer-*.toml /home/ds/gas-city/orders/pl-deep-audit-weekly.toml` |
| Janitor windows (7d archive, hourly dedup)      | `grep -H description /home/ds/gas-city/orders/mayor-mail-janitor.toml /home/ds/gas-city/orders/mail-dupe-dedupe.toml`                                                                                                       |
| Tier-1/Tier-2 contract text                     | open `template-fragments/pl-periodic-directives.template.md`                                                                                                                                                                |
| Escalate channel id C0B25SS12CD                 | `grep C0B25SS12CD /home/ds/gas-city/orders/escalate-surfacer.toml`                                                                                                                                                          |
| Mayor binding discrepancy still open            | compare `config.json` default_handles vs `agents/mayor/prompt.template.md` "not bound to any Slack channel"                                                                                                                          |
