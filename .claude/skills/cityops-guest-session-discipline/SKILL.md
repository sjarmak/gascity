---
name: cityops-guest-session-discipline
description: >-
  You are a human-launched guest session (not a gc-managed pool worker)
  with cwd under `/home/ds/gas-city`, `/home/ds/gascity`, or a rig while
  automated agents claim beads live. Load to decide whether you may touch
  beads/mail/config/dolt, tell the canonical dolt server from leaked ones,
  or know which gascity tree you are in.
---

# Guest-session discipline (ds-research city)

A **guest session** is a Claude (or human shell) session launched by hand inside
the city or a rig while the city's own agents are running. A **managed session**
is one the gc supervisor created and reconciles: pool workers, project leads
(`*-pl`), polecats, mayor. The difference matters because the reconciler tracks
managed sessions and their bead claims; it knows nothing about you, and the
automated agents will race you on anything you touch.

Identity test — you are a guest if both hold:

```bash
env | grep -E '^GC_'          # empty in a guest shell (verified 2026-07-06)
# Guard the not-in-tmux case: an empty grep pattern matches EVERY row and
# silently inverts the test. Not being in tmux at all already means guest.
s=$(tmux display-message -p '#S' 2>/dev/null)
[ -n "$s" ] && gc session list | grep -i "$s" || echo "not in tmux: guest"   # no row for you
```

The five standing rules for guests live in
`/home/ds/gas-city/docs/conventions/guest-session-primer.md` and the workspace
Don't/Do lists live in `/home/ds/gas-city/CLAUDE.md`. This skill does not
restate them; it owns what neither covers: the failure mechanism behind each
rule, the orientation sequence, the three-tree trap, and how to verify you are
about to act on the right live objects.

## When NOT to use this skill

| You need                                    | Go to instead                                                                                                            |
| ------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| The rule list itself (what's forbidden)     | `docs/conventions/guest-session-primer.md`, then `CLAUDE.md` Don't/Do                                                    |
| Dolt endpoint model, SQL recipes, backup/GC | `compass-dolt` skill → `docs/conventions/dolt-sql-server.md`; sibling `cityops-dolt-beads-reference` (departure library) |
| Recovering a wedged supervisor/tmux         | `compass-tmux-supervisor` → `docs/conventions/tmux-supervisor.md`; sibling `cityops-debugging-playbook`                  |
| Dispatching work (you were ASKED to sling)  | `compass-bead-dispatch` + the `gc-dispatch` skill                                                                        |
| gc CLI syntax for mail/beads/rigs           | `gc-mail`, `gc-work`, `gc-rigs` skills                                                                                   |
| You are the mayor or a managed agent        | your own `prompt.md`; this skill's constraints do not all apply                                                          |

Sibling `cityops-*` skills are part of the same 2026-07 departure library; all
eleven ship in `.claude/skills/`.

## First five minutes: orient read-only

Run these from `/home/ds/gas-city` before doing anything else. All are safe.

```bash
timeout 30 gc rig list                        # which rigs exist, which are suspended
timeout 30 gc session list | head -25         # what's alive and what it's working on (WORKDIR column)
tmux -L ds-research list-sessions | head      # the real tmux population (socket is ds-research, not default)
timeout 45 gc beads list | head -15           # recent bead traffic in the HQ rig
systemctl --user status gascity-supervisor --no-pager | head -5   # supervisor up? which drop-ins loaded
```

Timing caveat (verified 2026-07-06): read-side gc commands can hang well past
20s under load (`gc order check` observed at 45s+; `gc beads list` likewise) —
hence the `timeout` wrappers above, per the debugging playbook's convention.
A slow or silent gc read is load, not breakage; do not "fix" anything because
of it.

Note what `gc session list` shows before you touch a file: the WORKDIR column
tells you which worktrees and rig checkouts are owned by live agents. Editing
inside an agent-owned worktree is how guests silently corrupt in-flight work.

## The three-tree trap

Three sibling paths differ by one hyphen and are routinely confused. Standing
in the wrong one is the root cause of the leaked-dolt-server class of incident.

| Path                    | What it is                                                         | Guest rule                                                       |
| ----------------------- | ------------------------------------------------------------------ | ---------------------------------------------------------------- |
| `/home/ds/gas-city`     | THE city workspace (city.toml, orders, agents, beads)              | Operate here; bare `gc` is fine                                  |
| `/home/ds/gascity`      | gascity **contributor tree** (PR branches of the framework)        | Never bare `gc` here; use `gc --city /home/ds/gas-city …`        |
| `/home/ds/gascity-main` | pinned main; sole source of the installed `gc` binary via `gcsync` | Read-only reference; never build from `/home/ds/gascity` instead |

Mechanism behind the `/home/ds/gascity` rule: `gc` resolves its workspace by
walking up from cwd (`--city string   path to the city directory (default:
walk up from cwd)`). `/home/ds/gascity` contains a stale `.gc/` directory
(leftover `beads.json`, `runtime/` — verified present 2026-07-06), so bare `gc`
there adopts it as a workspace and can spin up its own runtime, including a
rogue dolt sql-server. The same mechanism fires in test worktrees under
`/home/ds/gascity-worktrees/` — see the worked example below for six live
instances.

## Rule-to-mechanism map

The primer states each rule in one line; this table is why each one exists, and
the read-only check that proves you are on the safe side.

| Primer rule                                           | Failure mechanism                                                                                                                                                                                             | Pre-flight check                                                                     |
| ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| No claim/close/edit of beads unasked                  | Pool workers race you; a guest-claimed bead has no reconciler backstop, so it stalls dispatch until someone notices (managed agents at least get reaped)                                                      | `gc beads show <id>` — if assignee is a live session in `gc session list`, hands off |
| Never `bd dolt start\|stop\|status`                   | `bd dolt status` performs "drift recovery" as a side effect and kills the production server (gascity#506/#245/#323) — the most dangerous read-looking command here                                            | Need server state? `cat .beads/dolt/.dolt/sql-server.info` and check `/proc/<pid>`   |
| Never raw `dolt sql` inside `.beads/dolt/`            | Server holds the LOCK; CLI blocks or corrupts. Query over TCP instead (recipe in CLAUDE.md "Bead SQL queries")                                                                                                | `ss -tlnp \| grep <port>` confirms the TCP endpoint before querying                  |
| Mail with `--from stephanie-adhoc`                    | Sender defaults to `"human"` in a guest shell (no `GC_SESSION_ID`/`GC_ALIAS` set); replies to `human` dead-letter into the `mail-redirect-to-mayor` event order and surface via Slack instead of reaching you | `gc mail send --help` shows the sender-default chain                                 |
| Don't edit `prompts/`, `formulas/`, `agents/` unasked | Live sessions template from these on wake; a mid-flight edit changes agent behavior with no change-control trail (see sibling `cityops-city-change-control` for how edits are actually made)                  | `gc session list` — anything active may re-read them                                 |

Reading replies to your guest mail:

```bash
gc mail send <rig>-pl --from stephanie-adhoc -s "guest session active in <dir>" --notify
gc mail inbox stephanie-adhoc        # inbox takes a session alias positionally
```

Cross-rig coordination goes to `mayor`. Stephanie does not read `gc mail`
directly; anything for her goes through mayor's Slack surface.

## Worked example: eight dolt servers, one canonical (2026-07-06)

Live enumeration from this host, 2026-07-06 — every `dolt sql-server` process
with its `--config` path:

```
PID 1467571  --config /home/ds/gas-city/.gc/runtime/packs/dolt/dolt-config.yaml     ← canonical
PID 376249   --config /home/ds/gascity-worktrees/polecat-6/.gc/runtime/packs/dolt/dolt-config.yaml
PID 430354   --config /tmp/city/.gc/runtime/packs/dolt/dolt-config.yaml
PID 1526588  --config /home/ds/gascity-worktrees/polecat-4/.gc/runtime/packs/dolt/dolt-config.yaml
PID 2809155  --config /home/ds/gascity-worktrees/polecat-1/.gc/runtime/packs/dolt/dolt-config.yaml
PID 3231620  --config /home/ds/gascity-worktrees/polecat-5/.gc/runtime/packs/dolt/dolt-config.yaml
PID 4192601  --config /home/ds/gascity-worktrees/polecat-3/.gc/runtime/packs/dolt/dolt-config.yaml
PID 3879168  -H 127.0.0.1 -P 40191  (cwd /home/ds/.beads — the user-global bd store, NOT the city's)
```

Six leaks came from polecat test runs that walked up into worktree-local `.gc/`
state — exactly the bare-`gc`-in-the-wrong-tree mechanism above. Two guest
mistakes are available here and both are wrong:

1. "Clean up the extra dolts" with a blanket `pkill dolt` — kills the
   production server (PID 1467571) and the user-global one serving `~/.beads`.
2. "Query whatever dolt answers on localhost" — a leaked server serves a stale
   snapshot of its test worktree, not city beads (the ghost-dolt hazard,
   `docs/design/` upstream-issue draft).

The correct guest moves, in order:

```bash
cat /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info   # pid:port:uuid → 1467571:29620:… (2026-07-06)
tr '\0' ' ' < /proc/1467571/cmdline                       # confirm --config is under /home/ds/gas-city
```

Only a server whose `--config` resolves under `/home/ds/gas-city/.gc/runtime/`
is the city's. Do not kill the others yourself — leaked-server cleanup is a
flagged open decision for Stephanie (morning ledger 2026-07-07, "Live findings
needing morning decisions"); report what you found and stop.

Known decoy while you are in there: `/home/ds/gas-city/.beads/dolt-server.pid`
reads `4663`, and `/proc/4663` does not exist (verified 2026-07-06). The pid
file is stale by design-gap; `sql-server.info` is the ground truth.

## Leaving cleanly

- Undo any state you created for yourself: if Stephanie had you claim a bead,
  either finish it or `bd update <id> --unassign` before you exit — an assigned
  bead with a dead guest behind it stalls dispatch with no reaper to save it.
- Anything you concluded that a live agent should know goes as mail to the
  owning `<rig>-pl` (or `mayor` cross-rig), `--from stephanie-adhoc`, before
  you exit. Findings that die with the session were never made.
- Do not restart the supervisor, tmux, or any service on the way out "to leave
  things tidy". Restart sequencing has a strict order (tmux before supervisor)
  and is owned by the recovery playbook, not by guests.

Trust posture (provisional, per the 2026-07-07 morning-ledger city-ops
positions, pending Stephanie's answers): no automated subsystem in this city is
documented as trusted-unsupervised, and guests inherit the strictest reading —
external artifacts, merges to shared refs, account/credential changes, and
city.toml topology changes are always human-gated. Nothing in this skill
weakens a human gate.

## Provenance and maintenance

Written 2026-07-06 against the live host. Volatile facts and their one-line
re-verification commands; if a check disagrees with this file, the host wins —
update the file.

| Claim                                                    | Re-verify with                                                                                           |
| -------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| Canonical dolt pid:port = 1467571:29620                  | `cat /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info`                                                |
| Stale pid file reads 4663 (nonexistent)                  | `cat /home/ds/gas-city/.beads/dolt-server.pid; ls /proc/$(cat /home/ds/gas-city/.beads/dolt-server.pid)` |
| 6 leaked polecat + /tmp/city dolt servers, 1 user-global | `pgrep -af 'dolt sql-server'`                                                                            |
| Stale `.gc/` exists in the contributor tree              | `ls /home/ds/gascity/.gc /home/ds/gascity/city.toml` (first exists, second must not)                     |
| Guest shells carry no `GC_*` env                         | `env \| grep -E '^GC_'` in a fresh human shell                                                           |
| tmux socket name is `ds-research`                        | `tmux -L ds-research list-sessions \| head -3`                                                           |
| `gc order check` slow under load                         | time-box it: `timeout 20 gc order check \| head -5`                                                      |
| Mail sender default chain / `--from` flag                | `gc mail send --help`                                                                                    |
| Dead-letter redirect order exists                        | `cat /home/ds/gas-city/orders/mail-redirect-to-mayor.toml`                                               |
| Leaked-dolt cleanup still an open human decision         | `grep -n "leaked dolt" /home/ds/gas-city/docs/design/fable-distillation/morning-ledger-2026-07-07.md`    |
