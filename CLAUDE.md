# Gas City — ds-research workspace

This is the `ds-research` Gas City workspace: a multi-agent Claude Code orchestrator across project rigs, driven by the `gc` CLI. Workspace name is set in `pack.toml [pack]` (`city.toml [workspace]` only sets the default provider); the running mayor agent is always-on via `[[named_session]]`.

Standing collaboration rules: `~/.claude/rules/common/agent-collaboration.md` (autonomy boundary, preview-before-execute, parallel-by-default, no effort estimates, tests-ship-with-fixes). Mayor-specific clarifications in `agents/mayor/prompt.template.md`.

---

## Don't (each line names the failure mode it prevents)

**Dolt sql-server**

- Don't run `bd dolt start|stop|status` here → kills the live gc-managed server (`bd dolt status` does it as a "drift recovery" side effect; gascity#506, #245, #323).
- Don't run `dolt sql` inside `.beads/dolt/` while the server is up → server holds the LOCK; CLI either blocks or corrupts state.
- Don't add `dolt.host` / `dolt.port` / `dolt.user` to a rig's `.beads/config.yaml` under `managed_city` → supervisor init fails with `canonical inherited rig config must mirror the city endpoint`.

**Bead dispatch**

- Don't `gc sling` a bead you personally claimed without `--reassign` (or `bd update --unassign` first) → pool-claim hook silently skips, bead sits unclaimed.
- Don't reach for `--formula <name>` to attach a formula → that flag is a bool; use `--on <name>`. `--formula <name>` errors with `requires 1 or 2 arguments`.
- Don't `gc sling` to a warm pool without `--nudge` → warm worker stays idle, bead sits unclaimed (default to the `gc-sling` wrapper, which auto-injects `--nudge`; gascity#1129).

**gc binary & packs**

- Don't build `gc` from `/home/ds/gascity` → that tree holds PR branches; the installed binary must come from `/home/ds/gascity-main` (driven by `gcsync`).
- Don't reference `/home/ds/gascity/examples/oversight-rig` in `city.toml` → city breaks every time the contributor tree swings to a branch without the pack. Use `/home/ds/gascity-packs-worktrees/oversight-rig` instead.

**Supervisor & tmux**

- Don't start the supervisor before tmux is alive on the `ds-research` socket → reconciler can't create sessions, then drains everything it doesn't recognize as orphans.
- Don't blanket-kill processes flagged by `claude-zombie-report` → interactive tmux work looks identical to abandoned sessions. Triage by `CWD` and tmux cross-reference first.

**Heavy work**

- Don't run multi-GB scix scripts in your default shell cgroup → systemd-oomd picks an unlucky cgroup inside `user@1000`, usually mayor or the supervisor. Wrap in `scix-batch`.

**Git / external artifacts**

- Don't `git push`, open PRs, or send slack / mail / messenger without explicit per-action approval, even mid-phase — see global `agent-collaboration.md` autonomy boundary.
- **Pre-authorized (Stephanie, 2026-06-19):** routine research-rig **code** pushes — direct-to-main integration of branch-ready worker code in rig repos (e.g. `sjarmak/mem`, `sjarmak/codeprobe`) — do NOT need per-action approval. Still per-action: pushing **data / results / comparison-numbers**, force-pushes to shared refs, upstream `gastownhall/*` PRs & merges, and all external comms (slack / mail / PR bodies).
- **Pre-authorized (Stephanie, 2026-07-14) — PL push carve-out (mem / codeprobe only), 3 gates:** a PL may push branch-ready worker **code** direct-to-main in `sjarmak/mem` and `sjarmak/codeprobe` without per-action approval, but ONLY when all three gates hold: (1) a **review record** exists on the bead (green review gate, not a self-report); (2) **build + tests verified green** by execution, not by claim; (3) the diff is **code only** — no data, results, or comparison-numbers (those stay per-action, per the 2026-06-19 pre-auth). Record the pushed SHA on the bead. Any rig outside mem/codeprobe, and any PR or force-push, stays per-action.
- **Pre-authorized (Stephanie, 2026-07-14) — gascity-packs push-branch-only:** the packs PL / polecat MAY `git push` a completed+verified `bd-gpk-*` branch to the **fork** (`sjarmak/gascity-packs`) once it has passed its review gate, recording the pushed SHA on the bead. NO PR, NO merge, NO push to canonical — those stay per-action with Stephanie. Rationale: 56/74 packs branches lived only on local disk and one verified fix (`bd-gpk-fzej`) was garbage-collected off origin, destroying ~26 days of tested work.
- **Pre-authorized (Stephanie, 2026-07-14) — fork-PR CI approval:** the mayor MAY approve queued GitHub Actions **workflow runs** on fork / outside-contributor PRs (`gastownhall/gascity`, `gastownhall/gascity-packs`) after a per-PR content skim: reject anything touching `.github/workflows/**`, Makefile, CI scripts, `go:generate`, or install hooks, and anything with curl-to-shell, base64-exec, added network egress, or secrets/`GITHUB_TOKEN` access. Approve only runs at the PR's **current head SHA** (stale-SHA runs waste CI). This is CI-execution consent only — reviews, merges, labels, and comments stay fully gated.
- Don't `--no-verify` / `--no-gpg-sign` / `--dangerously-skip-permissions` → hides root causes the hook caught.

---

## Do (concrete point targets)

- **Session start:** `gcsync` — fast no-op if `/home/ds/gascity-main` is already at origin/main HEAD, otherwise rebuilds and installs `gc`.
- **Health check:** `gc doctor` → `gc doctor --fix` → if still wedged, `systemctl --user restart gascity-supervisor`. Full recovery playbook in `docs/conventions/tmux-supervisor.md`.
- **Tmux dead:** `tmux -L ds-research new-session -d -s placeholder "sleep infinity"` → `systemctl --user restart gascity-supervisor` → wait 30s → `gc session list` to verify.
- **Bead SQL queries:** read port from `.beads/dolt/.dolt/sql-server.info`, then `dolt --host 127.0.0.1 --port "$PORT" --user root --no-tls --password '' sql -q '...'`.
- **Dispatch:** `gc-sling <agent> <bead>` (wrapper auto-injects `--nudge` and applies formula rules from `.gc/sling-intercept.yaml`); add `--reassign` if you previously claimed the bead.
- **Rate-limited agent:** `gc-capacity --rebalance <agent>` — add `--force` if you observed the limit in-session but the dashboard reads 0% (consumer vs API limit gap).
- **Heavy scix batch:** `scix-batch <command>` — transient cgroup with `ManagedOOMPreference=avoid` and a memory ceiling.
- **New rig:** `gc rig add <path>` (`--adopt` if it already has `.beads/`). Never wire it up by hand.
- **Mysterious supervisor stop:** check `/tmp/supervisor-stop-caller.log` for the process tree captured at the kill.
- **Reading the order schedule:** `gc order check` shows due / not-due reason for every order; `gc order history <name>` for recent fires.

---

## Codebase compasses

Summon by name when working on the matching area — each compass is a 3-4 file index, not a tutorial.

| Compass                   | Summon when                                                                    |
| ------------------------- | ------------------------------------------------------------------------------ |
| `compass-tmux-supervisor` | tmux socket, supervisor service, session-name collisions, recovery             |
| `compass-dolt`            | shared sql-server, endpoint model, bead store, dolt drift                      |
| `compass-bead-dispatch`   | `gc sling`, `gc-sling` wrapper, formula injection, claim handoff               |
| `compass-gc-binary`       | `gcsync`, two-worktree layout, oversight-rig pack worktree                     |
| `compass-capacity`        | claude account allocation, rate-limit failover, `scix-batch`, oomd interaction |
| `compass-scanners`        | `gc order` reapers, audit logs, evidence gates, epic-review automation         |

Detailed playbooks live in `docs/conventions/*.md`; each compass points at the relevant one.

---

## Layout

```
city.toml                — workspace config (agents, providers, rigs)
prompts/, formulas/      — agent prompt templates, work formula templates
orders/                  — periodic dispatch configs (one TOML per order)
bin/                     — launchers, reapers, surfacers (gc-sling, scix-batch, *-reaper, …)
hooks/                   — gc lifecycle hooks
.beads/                  — bead store (dolt server, but the gc CLI uses the file backend for its own sessions)
.gc/                     — runtime state, locks, audit logs (supervisor log lives at ~/.gc/supervisor.log)
.claude/skills/compass-* — codebase compasses (this file's index)
.claude/skills/gc-*      — gc CLI cheat-sheets (work, dispatch, agents, rigs, mail, city, dashboard)
docs/conventions/        — detailed playbooks (recovery sequences, endpoint model, reaper rules)
docs/adr/, docs/design/  — architecture decisions and design notes
```
