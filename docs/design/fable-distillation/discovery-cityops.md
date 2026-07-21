# Gas City Discovery Report — ds-research city ops (Phase 1)

Retiring-fellow discovery pass, 2026-07-06. Read-only investigation of the live
workspace at /home/ds/gas-city. All claims below were verified on-host during
this session unless marked as sourced from a doc.

## 1. City topology as-built

**Workspace.** One Gas City workspace ("ds-research", HQ prefix `dr`)
orchestrating **17 rigs declared in city.toml** (~13 operationally meaningful):
active — enterprisebench, gascity (fork, prefix `gc`,
default_sling_target=polecat), gascity-packs (`gpk`), zeldascension, decisions
(`dec`), mem, brains, tom-swe, website (`sjai`), aoa, gascity-dashboard;
suspended or suspended_on_start — codescalebench, codeprobe, agent-diagnostics,
scix-experiments, background-agents, geo, mcp-ax, live_docs,
code-intelligence-digest, migration-evals. Nearly every rig imports the
**oversight-rig pack** from the stable path
`/home/ds/gascity-packs-worktrees/oversight-rig/oversight-rig` (never from the
contributor tree — a documented breakage class).

**Providers.** Five Claude OAuth accounts (`claude-1..5`), each launched via
`bin/claude-account <n>` which flips `CLAUDE_CONFIG_DIR` to
`/home/ds/.claude-homes/account<n>/`, plus `claude-auto` (picker via
`csu_pick.sh`) and `codex`. All accounts carry identical config
(`fork_flag = "--fork-session"` made uniform 2026-07-05 so accounts stay
**fungible for quota rebalancing**; the fork flag is inert unless a session
bead carries `gc.brain_parent_sid` — mem-arm warm/cold brain-fork experiment).
`CSU_PICK_EXCLUDE` defaults to empty: healthy accounts are fungible and the
picker must not encode account-specific exclusions. Operators and recovery
automation may still set an explicit temporary exclusion for a verified bad
slot; quota exhaustion remains an automatic mechanical exclusion.

**Mayor pin contradiction (live).** `city.toml [[patches.agent]]` pins mayor to
**claude-5**, but the adjacent comment says claude-3 is mayor's dedicated
account (relocated 2026-07-04 from claude-5 which hit 100%/72% quota). The
patch value is what takes effect at launch; this file/comment divergence needs
reconciling against `agents/mayor/agent.toml`. Also note: all per-agent
`pin=true` locks were removed 2026-07-05 ("accounts are fungible"), and
`bin/gc-capacity` historically **missed per-agent agent.toml provider pins**
(improvement-program P0; caused the "zelda freeze").

**Beads dual-backend.** `city.toml [beads].provider = "file"` — the gc CLI
itself runs on the file backend (`.gc/beads.json`, currently **150.3 MB**, with
~140 MB janitor backup copies accumulating), while a gc-managed **dolt
sql-server** serves `bd` and rig bead stores. `gc bd` is therefore hard-blocked:
verified error `gc bd: only supported for bd-backed beads providers (resolved
"file" for /home/ds/gas-city)`. Use `bd` directly or `gc beads list/show`.

**Live population (verified 2026-07-06).** 29 tmux sessions on socket
`ds-research`; 34 gc sessions including mayor (77 days old, pinned, attached),
city-infra-pl (15d), 5 polecats, 6 enterprisebench workers,
mem/dashboard/packs workers, per-rig oversight project-leads, and the
`core.control-dispatcher` singleton. Some mem-workers asleep with reason
`config-drift`.

## 2. Runtime & supervision reality

- **`gascity-supervisor.service`** (systemd user unit): `gc supervisor run`,
  `Restart=always`, logs append to `~/.gc/supervisor.log` (16.9 MB + rotated
  archive). `KillMode=process` deliberately — the systemd default would cascade
  SIGTERM into tmux servers in the same cgroup and destroy session history. The
  reconciler re-adopts tmux on start. The supervisor itself runs under
  **account4's** CLAUDE_CONFIG_DIR.
- **Two load-bearing drop-ins**: (1) `10-dolt-port.conf` hardcodes
  `GC_DOLT_PORT=29620` / `BEADS_DOLT_SERVER_PORT=29620` — stopgap for
  **gc-74rxa**: the supervisor only exports the dolt port when it _starts_
  dolt, not when it _adopts_ a surviving one after restart; without it,
  `bd --readonly` resolves dolt at **127.0.0.1:0** and all order-firing
  freezes. (2) `docker-group.conf` wraps ExecStart in `sg docker` because the
  user@1000 manager predates the docker group membership.
- **`gascity-tmux.service`**: keepalive session `gc-keepalive` on socket
  `ds-research`, ordered `Before=` the supervisor — encoding the
  chicken-and-egg rule (supervisor started without tmux drains everything as
  orphans).
- **OOM history**: 2026-04-11 supervisor OOM (7.5 GB peak; root cause:
  city.toml re-parsed on every reconciler tick + 16 concurrent bd
  subprocesses). `scix-batch` (capped transient cgroup,
  `ManagedOOMPreference=avoid`) exists so systemd-oomd kills batches, not
  mayor/supervisor. Mysterious stops: `/tmp/supervisor-stop-caller.log`
  (845 KB — it has fired a lot) captures the killer's process tree.
- **Binary hygiene**: installed `gc` comes only from `/home/ds/gascity-main`
  via `gcsync` (also a 15m order); `/home/ds/gascity` is the PR-branch
  contributor tree — never build from it, never run bare `gc` in it (spawns
  rogue dolt servers). `gascity-main-pin-guard` order self-heals the main pin
  (born from a 4-day binary-freeze incident).
- **Live right now**: `gc order check` **timed out at 45s** in this session's
  probe — even read-side order enumeration is slow under current load
  (maintenance-cycle is paused today precisely because a worktree-provisioning
  bug had the supervisor pegged ~360%).

## 3. Beads/dolt failure-mode catalog (evidence verified on-host)

Canonical server: PID **1467571**, port **29620**, data_dir
`/home/ds/gas-city/.beads/dolt`, up since 2026-07-03, **1.2 GB RSS**. Ground
truth = `.beads/dolt/.dolt/sql-server.info` (format `pid:port:uuid`) and
`.gc/runtime/packs/dolt/dolt-state.json` — both agree.

| #   | Failure mode                                                                                                                                                                                                             | Evidence                                                                                                                           |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| 1   | `bd dolt start\|stop\|status` **kills the live server** (drift-recovery side effect)                                                                                                                                     | CLAUDE.md hard rule; gascity#506/#245/#323                                                                                         |
| 2   | Raw `dolt sql` in `.beads/dolt/` while server up → LOCK block/corruption                                                                                                                                                 | dolt-sql-server.md                                                                                                                 |
| 3   | Stale pid file: `.beads/dolt-server.pid` says **4663 — PID does not exist**; sql-server.info is authoritative                                                                                                            | verified: /proc/4663 absent                                                                                                        |
| 4   | Port resolves to 127.0.0.1:0 after supervisor restart (adopt path)                                                                                                                                                       | gc-74rxa; systemd drop-in stopgap; workaround `PORT=$(cut -d: -f2 .beads/dolt/.dolt/sql-server.info)`                              |
| 5   | **Ghost dolt servers** serving stale snapshots from deleted-inode FDs; a leaked test artifact (`dolt-state.json` pointing at /tmp) made `gc dolt status` reject the healthy server and `gc dolt start` nearly kill -9 it | upstream-issue-draft-ghost-dolt.md; poisoned file preserved: `.gc/runtime/packs/dolt/dolt-state.json.POISONED-TEST-ARTIFACT-APR09` |
| 6   | **Six leaked dolt servers running right now** from `/home/ds/gascity-worktrees/polecat-{1,3,4,5,6}` test configs plus one `/tmp/city` config, and a seventh serving `~/.beads` — none are the city's server              | verified via ss + /proc cmdline                                                                                                    |
| 7   | `net_read_timeout` "unexpected EOF" on cross-store enumeration (~20 rigs > 15s default)                                                                                                                                  | fixed via `[dolt] read_timeout_millis = 60000` in city.toml, RCA 2026-06-21                                                        |
| 8   | Rig config drift: setting `dolt.host/port/user` under `managed_city` blocks supervisor init                                                                                                                              | dolt-sql-server.md; `.beads/config.yaml` shows canonical shape (`gc.endpoint_origin: managed_city`)                                |
| 9   | Log blowout history: `dolt-server.log.old-20260413` is **167 MB**; rotation now handled by janitor-log-rotate (live since 2026-07-04)                                                                                    | .beads/ listing                                                                                                                    |
| 10  | Dolt auto-GC is OFF; `dolt-gc-maintenance` order runs `CALL DOLT_GC()` nightly 04:30                                                                                                                                     | orders inventory                                                                                                                   |
| 11  | Concurrent claim race: one bead claimed by 4 slots clobbering a shared branch (gc-typpc) — ADR-0009 written to fix, **not yet built**                                                                                    | ADR-0009                                                                                                                           |
| 12  | Backups: `.beads/backup/` holds dolt `.darc` archives + oldgen; beads.json janitor .baks accumulate ~1 GB                                                                                                                | verified listing                                                                                                                   |

## 4. Orchestration surface

**Orders (91 files; ~40 cooldown, ~35 cron, 7 event-triggered, 1 gate).**
Functional split: 16 reapers, 12 patrols, 5 watchdogs, 9 triage cycles, ~18
notifiers/surfacers, 3 account/quota managers, 9 maintenance/janitors, 6
git/PR-pipeline, ~10 audits. Only two hard-disabled (`bead-janitor.disabled`,
`rig-patrol.toml.disabled` — the latter was the only native formula+pool order;
that shape is regressed upstream, gascity#1440, so cycles exec-wrap slings
instead). `maintenance-cycle` is `enabled=false` via city.toml override as of
2026-07-06 (worktree-provisioning bug RCA gc-454658/gc-454686). Critical
operator lore encoded in order headers: the **cron zero-lastRun bootstrap
trap** (never-fired cron orders never fire; use cooldown for must-just-work
orders), **host-local EDT cron evaluation** (a past 4h-late incident),
`idempotent=true` fail-open annotations tied to gascity#2893, and the standing
principle "**mayor is always the responder, never the trigger**". Several
orders are named in-floor mitigations for open upstream bugs (pl-529-recovery,
blocked-routed-reaper, nudge-poll-reaper, nudge-on-route/routed-bead-nudger for
gascity#1129).

**Formulas (19 mol-\*).** Work executors (mol-do-work, mol-scoped-work,
mol-focus-review with its reject-and-retry self-review gate), pipeline stages
(research→brainstorm→decompose→dispatch), and the three big PR molecules:
**mol-pr-from-issue** (6 steps, default-deny `auto_push=false`, soft-halts at
branch-ready; push carve-out requires `auto_push=true` AND
`evidence.gate_passed` AND the kill-switch flag
`/home/ds/.gc/auto-push-armed.flag`), **mol-pr-iterate** (never pushes; halts
to mayor on ambiguity/>200 LOC/2 failed iterations), **mol-pr-revert**
(mayor-only, 3 carved-out write steps). Routing is **compile-time** via
`.gc/routing.yaml` (FCTR v1): defaults sonnet, haiku on cheap steps,
`grounded_review` (Codex) on review/apply steps, `human_gate` on
mol-pr-iterate report. `.gc/sling-intercept.yaml` is intentionally near-empty
(epic-level review superseded per-bead rules); city default formula =
mol-focus-review.

**Agents (37 homes).** `agent.toml` (scope, dir, work_dir template, provider,
pool min/max, wake_mode, env) + optional `prompt.md`/`prompt.template.md` +
optional `skills.toml`. Naming: `-pl` project leads (8), `-worker` pool
workers, `polecat` fork-coding agents (worktree-per-agent, no-push with
carve-outs), `-arm` experimental fork-dispatch targets,
`codex-reviewer`/`meta-reviewer` for epic review. All PLs get standing
directives from `template-fragments/pl-periodic-directives.template.md`: tiered
surfacing (Tier-1 infra → mail mayor only; Tier-2 decisions → `DECISION:` mail

- red-flag Slack post), STATUS_UPDATE twice daily, weekly DEEP_AUDIT,
  VAULT_NOTES (Obsidian). Mayor's prompt hard-gates all external GitHub artifacts
  per-action and forbids `gh pr merge` without a review record; Slack publishes
  are autonomous.

**Mail/coordination.** Stephanie does **not** read `gc mail` —
`mail-redirect-to-mayor` forwards anything addressed to
human/stephanie/sjarmak to mayor, who owns the Slack-facing reply format
(status banner, TL;DR, Open-Decisions ledger). Guest sessions coordinate via
`gc mail send <rig>-pl --from stephanie-adhoc` and never touch the bead queue
unasked.

## 5. Config archaeology (6 snapshots, 2026-05-29 → 2026-07-06)

1. **05-29 → 06-07 (pre-freshwake):** mass rig suspension (focus narrowed),
   dashboard/decisions/mem rigs added, `max_wakes_per_tick=15` (wake-storm cap).
2. **06-07 → 06-11:** `suspended` refined to `suspended_on_start`; mayor pin
   claude-5 → claude-auto.
3. **06-11 → 06-15 (pre-pl):** cass import + `append_fragments=["cass-search"]`
   on every agent; brains/tom-swe/website rigs; pr-pipeline paths reverted.
4. **06-15 → 07-06 (the big one):** `[dolt] read_timeout_millis=60000` (EOF
   RCA); uniform `fork_flag` (brain-fork experiment); `CSU_PICK_EXCLUDE`
   (credential-rot); oversight-rig path lost `examples/`; city-infra-pl became
   a named always-on session; mayor pin moved again (claude-auto → claude-5,
   comment says claude-3); aoa rig added.
5. **07-06 (17s apart):** only the maintenance-cycle `enabled=false` override —
   the snapshot naming convention is "back up immediately before the risky
   flip."

Drift themes: quota-chasing mayor pin churn; credential-rot workarounds as env
vars; import-path flip-flops as a recurring breakage source; **comments in
city.toml are the real changelog** (RCA bead IDs inline); experiment plumbing
(brain-fork) threaded through providers/imports/defaults.

## 6. Docs of record

All 9 ADRs are **status: proposed — none implemented**; the city runs the v1.5
convention/cron-reaper mechanisms the ADRs would replace. The conventions docs
(dolt-sql-server, tmux-supervisor, bead-dispatch, capacity, gc-binary,
scanners, heavy-batch-claude-calls, guest-session-primer,
recurring-task-example, bartertown-pilot-plan) are the live operational
playbooks. Design docs are mostly aspirational except idle-reaper-calibration
(finding: transcript-idle auto-kill would have fired zero times — stays
report-only) and the likec4-orient prototype. The improvement program
(2026-05-29) ranks verified P0s: gc-capacity pin-blindness, honesty gate,
events state-transition view, agent-teams env check in `gc doctor`, and the
warning that the "21% drain-without-commit" figure is **unverified lore**.
Incident docs: supervisor OOM (config re-parse churn), control-dispatcher
namespace regression (fix branch-ready 2026-06-26, unpushed), ghost-dolt
hazard.

## 7. Traps for a Sonnet-class operator (ranked)

1. **`bd dolt status` kills the production server.** The single most dangerous
   read-looking command in the workspace.
2. **Port/PID ground truth**: pid file is stale; bare `bd` can resolve
   127.0.0.1:0 after supervisor restarts. Always
   `cut -d: -f2 .beads/dolt/.dolt/sql-server.info`.
3. **Seven non-canonical dolt servers are running right now.** Killing "extra
   dolt processes" without checking `--config` path / cwd could hit the real
   one; conversely, trusting any dolt on localhost risks stale-snapshot reads
   (ghost-dolt).
4. **`gc bd` is blocked (file provider) but `bd` works** — the error hint sends
   you to city.toml, and a naive fix would be flipping `[beads].provider`,
   which would be catastrophic.
5. **Supervisor-before-tmux drains all sessions as orphans**; recovery order is
   tmux placeholder → restart supervisor → wait 30s → verify.
6. **Sling semantics**: `--formula` is a bool (use `--on`); claimed beads need
   `--reassign`; warm pools need `--nudge` — use the `gc-sling` wrapper, not
   raw `gc sling`.
7. **Cron bootstrap trap + EDT evaluation** when adding/editing orders; and
   dry-run→`--apply`/env-flag promotion conventions on janitors.
8. **Two worktree populations, two reapers**: framework pool worktrees
   (`.gc/worktrees/`) → `gc doctor --fix`; scattered skill/PR worktrees
   (`gascity-worktrees/*`, ship-_, gcd-_) →
   `/home/ds/bin/reap-worktrees.sh`.
9. **`claude-zombie-report` output is not a kill list** — interactive tmux work
   is indistinguishable from abandoned sessions; triage by CWD + tmux first.
10. **Heavy batches**: bare `claude` in scripts burns the default account's 5h
    window (2026-06-12 incident: 2,156 doomed retries); route via
    `claude-auto` + `scix-batch`.
11. **city.toml comments and the mayor-pin patch/agent.toml pair drift** — the
    patch value wins at launch; read comments before "cleaning up."
12. **`exit 0` without `gc session close` wedges a pool slot forever**
    (reconciler reads it active; beads queue behind it).

## 8. Proposed skill taxonomy (ops-adapted, 11 skills)

1. **topology-contract** — city.toml as-built: rigs/providers/patches/
   overrides, the fungible-accounts model, CSU_PICK_EXCLUDE lore, mayor-pin
   reconciliation. Evidence: §1, §5.
2. **city-change-control** — how config changes are made here: bak-before-flip
   snapshot convention, comments-as-changelog with RCA bead IDs,
   orders.overrides for pausing, dry-run→apply promotion, import-path
   stability rules. Evidence: §5, order headers.
3. **dolt-beads-reference** — the shared-server endpoint model, ground-truth
   files, TCP query recipe, dual file/dolt backend split, `gc bd` block,
   backup/GC arrangements, and the full hard-rule list. Evidence: §3.
4. **failure-archaeology** — the incident catalog with detection signatures:
   ghost dolt, poisoned dolt-state, supervisor OOM, 127.0.0.1:0, binary
   freeze, heavy-batch quota burn, worktree-provisioning spawn storm. Where to
   look: supervisor-stop-caller.log, journalctl oom-kill, control-dispatcher
   traces. Evidence: §3, §6.
5. **ops-debugging-playbook** — the layered recovery ladder: `gc doctor` →
   `--fix` → tmux placeholder → supervisor restart → session verification;
   wedged-slot, stuck-dispatcher, stale-session-name fixes; both worktree
   reapers. Evidence: §2, §7.
6. **orders-and-patrols** — the 91-order surface by function, cron-vs-cooldown
   doctrine, EDT/bootstrap traps, idempotent fail-open rules, which orders are
   incident mitigations awaiting upstream fixes (and their un-installation
   conditions). Evidence: §4.
7. **dispatch-and-formulas** — gc-sling wrapper semantics, molecule anatomy,
   FCTR routing, the PR-molecule gate/carve-out model, kill-switch flag, drain
   protocol. Evidence: §4.
8. **session-and-account-management** — 5-account CLAUDE_CONFIG_DIR model,
   claude-account/csu_pick, quota rebalancing (`gc-capacity --rebalance
[--force]`, consumer-vs-API limit gap), keepalive/rot, scix-batch, zombie
   triage. Evidence: §1, §2, §7.
9. **mail-and-coordination** — mayor-as-single-human-interface, tiered PL
   surfacing contract, DECISION/BLOCKED-ON-HUMAN conventions, Slack channel
   wiring, dead-letter redirect. Evidence: §4.
10. **guest-session-discipline** — the primer plus scar-tissue: what an ad-hoc
    session may touch, `gc --city` requirement outside the workspace, never
    bare `gc` in `/home/ds/gascity`. Evidence: guest-session-primer.md, §7.
11. **capacity-and-scaling** — rig suspend/resume, pool min/max,
    max_wakes_per_tick, +2-workers auto-approval bound, wake storms,
    disk/memory pressure guards. Evidence: §1, §4, §5.

Worktree reaping folds into ops-debugging-playbook rather than a standalone
skill; bartertown stays out of the core set (pilot-scoped, its own doc is
already prescriptive).

## 9. Questions for Stephanie

1. **Costliest incidents ranking:** of the recorded incidents (ghost dolt,
   supervisor OOM, 4-day binary freeze, heavy-batch quota burn, today's spawn
   storm), which actually cost you the most — in lost work or trust — and is
   there a painful one that never got a doc?
2. **Human gates forever:** the push carve-outs (auto-push-armed flag, mayor
   revert bypass, pre-authorized rig code pushes) are drifting toward more
   autonomy. Which gates do you want declared permanent regardless of how good
   the automation gets?
3. **Trust map:** which automated subsystems do you now trust unsupervised
   (janitors at `--apply`, automerge, quota auto-drain?) and which do you
   still spot-check every time? The skills should encode your actual trust
   boundaries, not the configured ones.
4. **Mayor pin intent:** city.toml pins mayor to claude-5 while the comment
   says claude-3 is its dedicated account — which is the intent of record, and
   what's your preferred rule when quota forces the next move?
5. **Unwritten rules:** what do you correct new sessions on most often that
   appears in no doc — timing (when not to restart things), tone in Slack
   surfacing, or thresholds for waking you?
