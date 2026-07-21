---
name: cityops-topology-contract
description: >-
  As-built topology of this ds-research install: which rigs/prefixes
  exist, the 5-account fungible claude model, CSU_PICK_EXCLUDE, the mayor
  provider-pin layering, the three suspension layers, and live
  orders.overrides. Load before editing or trusting city.toml, or when
  declared config and observed behavior disagree. Not dolt (compass-dolt)
  or dispatch (compass-bead-dispatch).
---

# City topology contract — ds-research as-built

This skill is the map of what is declared in this installation's config and
where each declaration actually lives. It does not tell you how to recover,
dispatch, or rebalance — see "When NOT to use this skill" below.

All volatile facts verified on-host **2026-07-06** unless dated otherwise.
Re-verification commands are at the bottom; run them before trusting any
count, pin, or override here.

## When NOT to use this skill

| You need                                                             | Go to                                                                 |
| -------------------------------------------------------------------- | --------------------------------------------------------------------- |
| Dolt/bead-server rules, ports, ground-truth files                    | `compass-dolt`, `docs/conventions/dolt-sql-server.md`                 |
| Dispatch (`gc sling`, `gc-sling` wrapper, formulas)                  | `compass-bead-dispatch`, `gc-dispatch`                                |
| Supervisor/tmux recovery ladder                                      | `compass-tmux-supervisor`, `docs/conventions/tmux-supervisor.md`      |
| Account rebalancing commands, oomd, scix-batch                       | `compass-capacity`, `docs/conventions/capacity.md`                    |
| gc binary / gcsync / oversight-rig worktree rules                    | `compass-gc-binary`, `docs/conventions/gc-binary.md`                  |
| Hard Don't/Do rules for this workspace                               | `/home/ds/gas-city/CLAUDE.md` (the one home for those lists)          |
| Ad-hoc guest-session conduct                                         | `docs/conventions/guest-session-primer.md`                            |
| Making a config change safely (bak-before-flip, comment conventions) | sibling `cityops-city-change-control` skill in this departure library |

## Terms (defined once)

- **City / workspace** — the orchestrated whole at `/home/ds/gas-city`.
  Workspace name `ds-research` is declared in `/home/ds/gas-city/pack.toml`
  (`[pack] name`), NOT in `city.toml [workspace]` (that section only sets the
  default provider). CLAUDE.md's preamble sentence ("Workspace name is set in
  `city.toml [workspace]`") is stale on this point — flagged for correction;
  CLAUDE.md edits are themselves change-controlled. HQ rig prefix is `dr`.
- **Rig** — one managed project repo (e.g. `/home/ds/projects/mem`), declared
  as a `[[rigs]]` block in `city.toml`, with its own bead store and agents.
- **Provider** — a launcher definition (`[providers.*]`) that says how to
  start an agent process (command, flags, env).
- **Pack / import** — reusable config bundle pulled in via `[imports]` at
  city or rig level.
- **Patch** — a `[[patches.agent]]` block in `city.toml` that overrides one
  agent's fields; **the patch value is what takes effect at launch**.
- **Override (rig)** — a `[[rigs.overrides]]` block inside a rig; targets one
  agent in that rig (usually `project-lead`).
- **Order override** — an `[[orders.overrides]]` block at the bottom of
  `city.toml`; enables/disables a named order without touching its file in
  `orders/`.

## The config file map (one home per fact)

| File                                                  | Owns                                                                                                                                        |
| ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `/home/ds/gas-city/city.toml`                         | providers, imports, named sessions, rig declarations, patches, `[beads] provider`, `[daemon]`, `[api]`, `[agent_defaults]`, order overrides |
| `/home/ds/gas-city/pack.toml`                         | workspace name `ds-research`; the `core` pack import, **pinned to a git sha** (`sha:f895c0ff…` as of 2026-07-06)                            |
| `/home/ds/gas-city/agents/<name>/agent.toml`          | per-agent scope/provider/session limits — but a matching `[[patches.agent]]` in city.toml wins at launch                                    |
| `/home/ds/gas-city/.gc/runtime/suspension-state.json` | live rig suspension (machine-local, not committed)                                                                                          |
| `/home/ds/gas-city/orders/*.toml`                     | individual order definitions (89 active .toml files + 2 `.disabled` as of 2026-07-06)                                                       |

The comments inside `city.toml` are the de-facto changelog: they carry RCA
bead IDs and dates. Read them before "cleaning up" anything — but do not
trust them as current state (see the mayor pin below).

## Providers: the fungible-account model

Seven providers declared in `city.toml`:

- `claude-1` … `claude-5` — five Claude OAuth accounts. Each launches via
  `/home/ds/gas-city/bin/claude-account <n>`, which sets
  `CLAUDE_CONFIG_DIR=/home/ds/.claude-homes/account<n>/.claude`, symlinks
  shared config from `~/.claude/`, bootstraps onboarding/trust state, and
  `exec`s `claude --dangerously-skip-permissions`.
- `claude-auto` — same launcher with arg `auto`; delegates account choice to
  `/home/ds/gas-city/bin/csu_pick.sh` (reads `~/.claude-usage/usage_cache.json`,
  skips ≥95% 7d-utilization accounts, applies rot-preference for near-expiry
  tokens, random top-K=2 among the least loaded). Falls back to account 1 if
  the picker fails.
- `codex` — Codex CLI at `/home/ds/.nvm/versions/node/v22.22.2/bin/codex`,
  `exec --sandbox=danger-full-access`.

**Fungibility contract (2026-07-05).** All five claude providers carry an
identical `fork_flag = "--fork-session"` so that quota rebalancing can move
ANY agent to ANY account. The flag is inert unless a session bead carries
`gc.brain_parent_sid` (the mem-arm warm/cold brain-fork experiment; the
mechanism is documented in the claude-3 provider comment). Per-agent
`pin = true` locks were removed the same day. Do not re-introduce a pin or
de-uniform the fork_flag without Stephanie's approval — account and
credential changes are a human gate. CONFIRMED by Stephanie 2026-07-07:
accounts are identical except accumulated usage; no agent (mayor included)
has an intended home account. The operative goal is spreading quota
effectively across all five accounts — optimize placement for that, nothing
else.

**CSU_PICK_EXCLUDE lore.** The exclusion `"claude-2,claude-3,claude-4"` lives
in TWO copies: `city.toml [providers.claude-auto.env]` and a baked-in default
in `bin/csu_pick.sh` (line ~52). The **script default is the operative one** —
per the script's own 2026-06-20 comment, the city.toml env route "wasn't
reaching launches (supervisor cached provider env from startup)", while
csu_pick.sh is read fresh per launch. Sibling
`cityops-session-and-account-management` owns the full trap write-up. Origin
(2026-06-20): csu_pick's rot-preference kept steering auto launches onto
near-expiry account 3, and a **headless launch writes the credential back
with the refresh token stripped**, clobbering fresh copies. The city.toml
comment explicitly says to leave the claude-3 exclusion in place even after
the rot issue clears, because it also keeps auto-launched PLs/workers off
mayor's account. Removing entries from this list is not a cleanup; it is a
credential-safety change.

## The mayor provider pin: three layers, one stale comment

This is the canonical example of why you read all three layers before
believing any one of them. State verified 2026-07-06:

| Layer                                      | Says                                                                           | Status                              |
| ------------------------------------------ | ------------------------------------------------------------------------------ | ----------------------------------- |
| `city.toml [[patches.agent]]` (name=mayor) | `provider = "claude-5"`                                                        | **takes effect at launch**          |
| `agents/mayor/agent.toml`                  | `provider = "claude-5"`, comment "Unpinned 2026-07-05 … accounts are fungible" | agrees with patch                   |
| Comment above the patch in `city.toml`     | "claude-3 is mayor's dedicated account (relocated 2026-07-04 from claude-5 …)" | **stale** — contradicts both values |

The comment/value contradiction is a flagged open item for Stephanie
(morning ledger 2026-07-07, city-ops Q4: NO CHANGE MADE). Do not "fix" the
comment or the value yourself. `bin/gc-capacity` updates BOTH the agent.toml
line and the city.toml patch on a rebalance move; a hand edit that touches
only one re-creates the divergence. Historical trap: gc-capacity once missed
per-agent agent.toml provider pins entirely (improvement-program P0, the
"zelda freeze").

## Rigs as-built

**21 `[[rigs]]` blocks declared** in city.toml (2026-07-06), plus the HQ rig
`ds-research` (prefix `dr`). `gc rig list` prints all 22 with live status.

| Rig                      | Prefix                                | Declared suspension (city.toml)                                                                                       |
| ------------------------ | ------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| codescalebench           | `co`                                  | none (runtime-suspended)                                                                                              |
| enterprisebench          | `EnterpriseBench`                     | PL override `suspended = true`                                                                                        |
| codeprobe                | `codeprobe`                           | PL override                                                                                                           |
| agent-diagnostics        | `agent-diagnostics`                   | `suspended_on_start = true`                                                                                           |
| scix-experiments         | `scix_experiments`                    | PL override                                                                                                           |
| background-agents        | `background-agents`                   | `suspended_on_start`                                                                                                  |
| geo                      | `GEO`                                 | `suspended_on_start`                                                                                                  |
| mcp-ax                   | `mcp-ax`                              | `suspended_on_start`                                                                                                  |
| live_docs                | `live_docs`                           | `suspended_on_start`                                                                                                  |
| code-intelligence-digest | `code-intel-digest`                   | `suspended_on_start`                                                                                                  |
| gascity                  | `gc`                                  | PL override; `default_sling_target = "polecat"`; extra imports pr-pipeline + pr-review from `/home/ds/gascity-packs/` |
| zeldascension            | `zeldascension`                       | none declared; PL override sets `wake_mode = "fresh"`                                                                 |
| migration-evals          | `migration_evals`                     | PL override                                                                                                           |
| gascity-packs            | `gpk`                                 | none                                                                                                                  |
| gascity-dashboard        | `gascity-dashboard`                   | PL override                                                                                                           |
| decisions                | `dec`                                 | none (runtime-suspended)                                                                                              |
| mem                      | `mem` (defaulted; no prefix declared) | none                                                                                                                  |
| brains                   | `br`                                  | none (runtime-suspended)                                                                                              |
| tom-swe                  | `tom-swe`                             | none (runtime-suspended)                                                                                              |
| website                  | `sjai`                                | none                                                                                                                  |
| aoa                      | `aoa`                                 | none                                                                                                                  |

Nearly every rig imports the oversight-rig pack from
`/home/ds/gascity-packs-worktrees/oversight-rig/oversight-rig` — the ONLY
sanctioned path; the rule and its failure mode are owned by
`/home/ds/gas-city/CLAUDE.md` and `compass-gc-binary`. City-level imports:
`cass` (`/home/ds/gascity-packs-worktrees/cass/cass`), `oversight-rig`,
`slack` (`/home/ds/gascity-packs/slack-pack`).

Two always-on `[[named_session]]` entries: `mayor` and `city-infra-pl`.

## Suspension has three layers — know which one you are reading

1. **`suspended_on_start = true`** on a `[[rigs]]` block — config-declared,
   applies at city start.
2. **`[[rigs.overrides]] agent = "project-lead" / suspended = true`** —
   suspends ONE AGENT in the rig, not the rig. The gascity rig's PL is
   suspended this way while its polecats work normally.
3. **Runtime suspension** — `gc rig suspend|resume <name>` writes
   `.gc/runtime/suspension-state.json` (machine-local, not committed). This
   is what makes `gc rig list` show "(suspended)".

These layers legitimately disagree. Verified 2026-07-06: zeldascension has no
suspension in city.toml but is runtime-suspended; decisions, brains, tom-swe,
codescalebench likewise runtime-only. Rigs absent from the state file fall
back to their declared flags. Ground truth for "is this rig running work
right now" is `gc rig list` (or the state file directly), never the
`[[rigs]]` block alone. State file `updated_at`: 2026-07-05T20:47:04Z.

## Reading effective config: `gc config explain`

`gc config explain` prints every agent's effective fields with a source
annotation. Use it for **values**, not for **edit locations**: on 2026-07-06
it attributed mayor's `provider = claude-5` to `pack.toml`, even though the
operative declarations live in `city.toml [[patches.agent]]` and
`agents/mayor/agent.toml`. To find where to edit, grep the three layers in
the file map above.

Under current load, prefer `gc config explain` piped through `grep -A8
'Agent: <name>'`; full output is thousands of lines. Note that `gc order
check` timed out at 45s in a 2026-07-06 probe — read-side gc commands can be
slow when the supervisor is busy; a timeout is load, not necessarily
breakage.

## Live order overrides (topology state, dated)

As of 2026-07-06 exactly one `[[orders.overrides]]` block is live:
`maintenance-cycle` `enabled = false`. The comment block above it carries the
full RCA chain (mayor mail gc-454759; RCA gc-454658/gc-454686; perf bead
gc-g421k) and the re-enable condition: remove the override once the shared
mol-formula worktree-provisioning fix lands, then re-dispatch the preserved
#2713 candidate. Do not re-enable it just because the city looks calm.

Other daemon-level knobs currently set (all in `city.toml`):
`max_wakes_per_tick = 15` (wake-storm cap), `[dolt] read_timeout_millis =
60000` (net_read_timeout EOF RCA 2026-06-21 — owned in detail by
compass-dolt), `[api] port = 9443` bound to 127.0.0.1, `[session]
startup_timeout = "30m"`, `[agent_defaults] wake_mode = "resume"` with
`default_sling_formula = "mol-focus-review"` and `append_fragments =
["cass-search"]`, `[beads] provider = "file"` (which is why `gc bd` errors
here — the dolt server serves `bd` and rig stores; details in compass-dolt).

## Worked example: reading a topology change from its snapshot

The operating convention is "back up immediately before the risky flip", so
the newest `city.toml.bak-*` is a time machine for the last change. Live
snapshots on host (2026-07-06): `city.toml.bak-20260529T151405`,
`city.toml.pre-freshwake`, `city.toml.bak-20260611-prpipeline-path`,
`city.toml.bak-pre-pl-20260615-2112`,
`city.toml.bak-pause-maintenance-cycle-20260706T175816Z`.

```bash
diff /home/ds/gas-city/city.toml.bak-pause-maintenance-cycle-20260706T175816Z \
     /home/ds/gas-city/city.toml
```

Verified output (2026-07-06): 13 added lines, 0 removed — exactly the
`[orders]` header, the RCA comment block, and:

```toml
[[orders.overrides]]
name = "maintenance-cycle"
enabled = false
```

That one diff tells you: what changed (a single order pause), why (the
comment cites mayor mail gc-454759 and the worktree-provisioning RCA), and
that nothing else was touched in the same flip. When you inherit a confusing
city.toml state, diff backwards through the snapshots before asking anyone.

## Human gates on topology (provisional)

Per the morning-ledger 2026-07-07 provisional positions (Q2/Q3, not yet
confirmed by Stephanie): city.toml topology changes, account/credential
changes, and anything touching shared refs or external artifacts require her
per-action approval; no subsystem in this file is trusted-unsupervised.
Reading and diffing everything above is free; changing any of it is not.

## Provenance and maintenance

Verified on-host 2026-07-06 by a read-only session. One-line re-checks for
each drift-prone claim:

| Claim                                   | Re-verify with                                                                           |
| --------------------------------------- | ---------------------------------------------------------------------------------------- |
| 21 declared rigs                        | `grep -c '^\[\[rigs\]\]' /home/ds/gas-city/city.toml`                                    |
| Rig prefixes / live suspension          | `gc rig list` (run from `/home/ds/gas-city`)                                             |
| Runtime suspension layer                | `cat /home/ds/gas-city/.gc/runtime/suspension-state.json`                                |
| Mayor pin value (patch)                 | `grep -B2 -A4 'patches.agent' /home/ds/gas-city/city.toml \| tail -20`                   |
| Mayor pin value (agent.toml)            | `grep provider /home/ds/gas-city/agents/mayor/agent.toml`                                |
| Effective mayor provider                | `gc config explain \| grep -A8 'Agent: mayor'`                                           |
| Provider roster                         | `grep '^\[providers\.' /home/ds/gas-city/city.toml`                                      |
| CSU_PICK_EXCLUDE contents (both copies) | `grep -n CSU_PICK_EXCLUDE /home/ds/gas-city/bin/csu_pick.sh /home/ds/gas-city/city.toml` |
| Live order overrides                    | `sed -n '/^\[orders\]/,$p' /home/ds/gas-city/city.toml`                                  |
| API port (9443)                         | `grep -A2 '^\[api\]' /home/ds/gas-city/city.toml`                                        |
| Order file count (89 + 2 disabled)      | `ls /home/ds/gas-city/orders \| wc -l` (91 entries incl. `.disabled`)                    |
| Snapshot inventory                      | `ls /home/ds/gas-city/city.toml*`                                                        |
| Core pack sha pin                       | `grep sha: /home/ds/gas-city/pack.toml`                                                  |

If any re-check disagrees with this file, the host wins; update this file
and re-date the claim.
