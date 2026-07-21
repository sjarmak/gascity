# GitHub mirror pilot — beads → GitHub Issues (+ Projects v2 kanban)

2026-07-18. Investigation + pilot spec, Stephanie-approved through "cherry-pick
and pilot order spec". Problem: pipeline fragility loses work, and beads are
illegible as workable to humans (and to agents outside the rig). Fix: make
GitHub Issues a rendered mirror of each rig's real work beads, with a Projects
v2 board as the cross-rig kanban.

## Decisions

- **beads is the single source of truth.** GitHub Issues are a push-mirror.
  Evidence from the sync-pattern survey: every OSS two-way tracker sync is
  archived; survivors are one-way with a designated SoT. Pull is a narrow
  backchannel (phase 3), not a peer.
- **Use `bd github sync`, no custom bridge.** First-party in bd 1.1.0
  (`bd github pull|push|sync|status|repos`), identity mapping via
  `external_ref` (issue URL) + `source_system`. Upstream charter
  (beads engdocs/INTEGRATION_CHARTER.md): polled sync only, "no webhooks,
  ever", no comment mirroring — matches gc's order model exactly.
- **Wrapper-computed selective push.** `--push-only` with no filter would push
  the full store (mem: 2,573 incl. 2,354 closed + formula wisps). The
  `bin/github-mirror` wrapper passes `--issues` with only real work:
  statuses open/in_progress/blocked/deferred/hooked, EXCLUDING wisps
  (metadata `gc.step_ref` or `gc.root_bead_id`, `gc.synthetic=true`, or
  `issue_type=convoy`), PLUS already-mirrored beads (`external_ref` set)
  closed in the last `MIRROR_CLOSED_DAYS` so closes propagate.
- **Never mirror to unowned upstreams.** Hard allowlist (`sjarmak/` prefix) in
  the wrapper. gascity → gastownhall/gascity, codescalebench → sourcegraph,
  gascity-dashboard → gastownhall are permanently out of scope for direct
  mirroring.
- **PRIVATE sibling mirror repos** (Stephanie, 2026-07-19): sjarmak/mem and
  sjarmak/codeprobe are public, and bead bodies carry internal eval/infra
  detail (standing no-public-claims rule). Mirrors live at sjarmak/mem-beads
  and sjarmak/codeprobe-beads (private), full-fidelity bodies. Colocation on
  the code repos was traded away deliberately; the kanban aggregates instead.
- **Molecule roots are wisps too** (found in pilot): formula wrapper beads
  carry `gc.formula_name` / `gc.kind=workflow` but not `gc.step_ref`; the
  filter excludes them (mem set dropped 153→144, codeprobe 27→26).

## Evidence (2026-07-18 dry-runs, patched bd)

| Rig | Repo | Unfiltered push scope | Filtered mirror set |
|---|---|---|---|
| mem | sjarmak/mem | 2,573 | 153 (124 open, 22 blocked, 6 deferred, 1 in_progress) |
| codeprobe | sjarmak/codeprobe | 391 | 27 |

Both under GitHub's content-creation caps (80/min, 500/hr) for one-shot
initial population.

## The #4329 patch (prerequisite, DONE)

Stock bd 1.1.0 carries beads#4214: the GitHub push path never records what it
pushed, so every sync re-PATCHes the entire `--issues` set (O(N) API calls per
run). Fix = upstream PR #4329 (open), cherry-picked 2026-07-18 onto the
v1.1.0 tag: branch `v1.1.0-pushhooks` in /home/ds/gastownhall/beads
(worktree /home/ds/gastownhall/beads-v110-pushhooks), 4 commits, one conflict
resolved by dropping a post-1.1.0 `created.Warnings` hunk that isn't part of
the fix. `internal/tracker` + `internal/github` tests pass; `cmd/bd/protocol`
failures reproduce identically at stock v1.1.0 (CGO-build environmental,
baseline noise). Installed as `~/.local/bin/bd` = **1.1.0 (56e6f65da)**;
stock backup at `~/.local/bin/bd-1.1.0-stock`. Version string stays 1.1.0 so
the v53 store gate chain is unaffected; diff touches no migrations.
Watch upstream: when #4329 merges into a release ≥1.1.0, drop the local patch
and take the release.

## Status 2026-07-19: phases 1+2 LIVE

Initial population complete, supervised: codeprobe 26 issues (27 minus one
molecule-wrapper escapee, closed as not-planned and filter fixed), mem 144
issues, 0 errors, `external_ref` backlinks verified (full issue URLs), labels
mapped (`type::` / `priority::` / `status::` / bead labels carried over).
Board: https://github.com/users/sjarmak/projects/2 (private), 170 items,
Status = Todo / In Progress / Blocked / Done. Order `github-mirror` installed
(cooldown 45m, --execute, promotion annotated) and verified via
`gc order show`. bd patch branch pushed: sjarmak/beads `v1.1.0-pushhooks`
@ 56e6f65da44b225c39d8fe28277ca28a4cf98441.

## Phase 1 — mirror pilot (as designed; see status above)

Components:
- `bin/github-mirror` (live, inert until an order calls it): filter + chunked
  `bd github sync --push-only --issues …`; refuses non-allowlisted repos;
  aborts if new creations exceed `MIRROR_MAX_CREATE`; audit line per run to
  `.gc/github-mirror-audit.log`; token from `gh auth token` at runtime
  (nothing written to config files).
- `docs/design/github-mirror/github-mirror.toml` (STAGED order): cooldown 45m,
  dry-run default. Install by copying into `orders/`.
- Rig config (set): `bd config set github.repository sjarmak/mem` and
  `…/codeprobe` (stored in each rig's dolt config; config.yaml untouched —
  the gc canonical-config gate only checks dolt.host/port/user, verified in
  gascity-main internal/beads/contract/connection.go).

Promotion gates (Stephanie, per change-control):
1. Review a dry-run audit line after installing the order (or the 2026-07-18
   manual runs above).
2. Flip `MIRROR_MODE = "--execute"` in the installed order file with the
   who/when annotation.
3. First execute run on mem is the initial population (~153 issues,
   ~2 min). Spot-check labels/status mapping on a few issues, then leave the
   cooldown loop running.

Rollback: remove order file; `bd config unset github.repository` per rig;
`~/.local/bin/bd-1.1.0-stock` restores stock bd. Mirrored issues stay put.

## Phase 2 — Projects v2 kanban (design only)

One user-level project on sjarmak aggregating both pilot repos. Facts that
shaped it: item limit is now 50k (old 1,200 is obsolete); built-in auto-add
workflows are one-repo-each, ~5/project on Pro, and don't backfill; user-level
projects get no webhooks. So: skip auto-add entirely — extend the mirror order
with a `gh project item-add` + Status-field update step after each push
(GraphQL mutations cost 5 rate-limit points each; trivial at pilot scale).
Bead status → project Status single-select (open→Todo, in_progress/hooked→In
progress, blocked/deferred→Blocked, closed→Done). Per-rig board views by
repo filter. Needs `project` scope (account4 token already has it).
Creating the project itself is an external artifact: per-action approval.

## Phase 3 — intake + reconciliation (LIVE 2026-07-19)

Implemented as `bin/github-mirror-reconcile`, run by `bin/github-mirror-cycle`
(reconcile → push+board) every 45m via the same `github-mirror` order. E2E
tested with a synthetic issue (sjarmak/mem-beads#145 → bead ingested deferred
+ `origin:github` → GH-closed drift detected → close converged; artifacts
cleaned up).

- **Intake**: open mirror-repo issues no bead references = human-filed →
  selective `bd github pull <number>` (bare number; URL refs fail Atoi
  parsing, and pull exits 0 on failure so success is verified by external_ref
  lookup). Ingested beads are triage-gated: status=deferred +
  `origin:github` label; a human/mayor flips to open to make dispatchable.
  Bulk pull is never used — GitHub echoes would overwrite beads. With private
  mirror repos the injection surface is repo collaborators only.
- **Echo guard (found in e2e, the key phase-3 lesson)**: a mirror push
  REOPENS a human-closed issue whenever the bead lacks a recorded push-hash
  or changed locally. Fix is field ownership: reconcile labels such beads
  `gh-closed-pending-triage`, the push set excludes that label, and reconcile
  runs BEFORE push in the same cycle so the race window is sub-run. Triage:
  close the bead (converged) or remove the label to deliberately reopen.
- **Controller exec gotcha (first-fire RCA)**: the order exec env pins
  `BEADS_DIR=/home/ds/gas-city/.beads`, so `bd` with a rig cwd silently
  resolves the CITY store (config looked unset; gates failed closed, nothing
  wrong was pushed). Both scripts now set `BEADS_DIR=<rig>/.beads` on every
  bd subprocess (`rig_env()`). Applies to ANY rig-targeting order script.
- **Loss/drift sweep**, per rig, findings reported not auto-fixed:
  ISSUE-MISSING (mirrored bead, issue deleted), UNPUSHED (active real bead
  with no issue after 2h = mirror gap), GH-CLOSED (above), INTAKE/CONFIG
  errors. Fresh findings (24h fingerprint dedup,
  `.gc/github-mirror-reconcile-state.json`) post to #gascity-maintenance +
  mayor nudge (stall-watch idiom); silent when clean. Audit:
  `.gc/github-mirror-reconcile.log`.

## Open questions

- Mirror `decision`-type beads? (4 in mem's set — currently included.)
- Dependency fidelity: beads deps don't cross the boundary yet (beads#4307
  open); GitHub now has native sub-issues + blocked-by dependencies, so
  contributing that mapping upstream is a candidate.
- Expansion order after pilot: EnterpriseBench has 534 open beads (needs
  MIRROR_MAX_CREATE raise + throttled initial population; also a private
  mirror repo per the 2026-07-19 decision).

## Citywide successor — one private Issues view (STAGED 2026-07-21)

The per-rig mem/codeprobe pilot remains live while the citywide successor is
staged at `sjarmak/gas-city-beads` (private). The successor deliberately uses
a different contract:

- `bin/github-central-mirror` is a **one-way renderer only**. It has no intake
  or loss reconciler and permits only read-only `bd list/show` calls.
- A strict final-line `(rig, bead ID)` marker owns central issue identity.
  Existing `external_ref` values are preserved because many point to upstream
  issues/PRs or the two pilot mirrors.
- The executable refuses every target except exact
  `sjarmak/gas-city-beads`, verifies it is private with Issues enabled before
  reads or writes, and validates every remote issue, bead, marker, label, and
  migration-state record before mutation.
- Only the immutable private sources `sjarmak/mem-beads` and
  `sjarmak/codeprobe-beads` are transferable. Source issues are bound to their
  node IDs before transfer so interruption after transfer resumes by node
  rather than creating a duplicate.
- Labels and create/update/transfer operations have independent per-run caps.
  Dry-run is the default. A process lock serializes complete central cycles.
- The live pilot cycle and transfer cutover share
  `.gc/github-mirror-pilot.lock`. Transfers additionally require the resolved
  `github-mirror` order to report `enabled=false`, so a paused order can drain
  before any issue moves. Both legacy scripts explicitly refuse the central
  repository even if a rig is misconfigured. Once one issue from a pilot repo
  has completed transfer, the durable migration state permanently fences that
  source repo from both legacy scripts, even if the order is re-enabled or a
  script is invoked manually.
- While any pilot transfer remains, new mem/codeprobe beads without an
  `external_ref` are reported as `deferred` rather than created centrally.
  This prevents the still-live pilot from creating a second representation in
  its next cycle. They become central creates only after pilot transfer
  convergence.

Latest reviewed dry-run before population:
`.gc-reports/github-central-mirror-plan-20260721T035307Z.json` — 1,354 records
(1,158 creates, 196 pilot transfers), 38 structural labels, 0 invalid records,
209 existing `external_ref` values preserved. Counts can increase as new beads
arrive; every rollout uses a fresh plan.

Promotion is intentionally staged:

1. Create only the planned structural labels.
2. Create one issue with transfers disabled; verify private visibility,
   marker recovery, rendering, and a second-run `noop`.
3. Populate bounded create-only batches. The pilot order remains live.
4. Pause the resolved pilot order, acquire/drain the shared lock, then transfer
   the two pilot histories in bounded batches. Never run pilot writes and
   transfers concurrently.
5. Install a central dry-run/execute order only after idempotence and cutover
   evidence are reviewed. The central repository never becomes an intake
   path.
