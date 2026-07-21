---
name: cityops-dispatch-and-formulas
description: >-
  Route work through this ds-research install: sling beads with gc-sling,
  choose/debug mol-* formulas, the PR-molecule gate/carve-out and
  kill-switch, the routing.yaml check, tracing a stuck dispatch. Load when
  a slung bead sits unclaimed, a molecule stalls, or editing a formula. Not
  raw sling flags (compass-bead-dispatch) or order scheduling
  (cityops-orders-and-patrols).
---

# City ops: dispatch and formulas

Runbook for one question: **how does a unit of work get from "someone decided
to do X" to "an agent did X and the slot is free again" in THIS city** — and
where to look at each stage when it does not. All paths are machine-local to
this host (ds-research city, root `/home/ds/gas-city`); that is deliberate.
All volatile facts date-stamped; re-verification commands at the bottom.

Part of the cityops-* departure library (see
`docs/design/fable-distillation/discovery-cityops.md` §8 for the roster).

## When NOT to use this skill

| You need                                                                   | Go to                                                                                      |
| -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| `gc sling` flag mechanics (`--reassign`, `--on` vs `--formula`, `--nudge`) | `CLAUDE.md` Don't/Do lists + `compass-bead-dispatch` + `docs/conventions/bead-dispatch.md` |
| gc CLI cheat-sheet for dispatch commands                                   | `gc-dispatch` skill (`gc skill dispatch`)                                                  |
| Drain patterns, order→formula→drain end-to-end anatomy                     | `docs/conventions/recurring-task-example.md`                                               |
| Order scheduling, cron traps, the reaper/nudger fleet mechanics            | `cityops-orders-and-patrols` (sibling)                                                     |
| Wedged slot / stuck dispatcher / supervisor recovery ladders               | `cityops-debugging-playbook` (sibling)                                                     |
| dolt endpoint, `bd` port resolution, bead-store hard rules                 | `compass-dolt` + `cityops-dolt-beads-reference` (sibling)                                  |
| Ad-hoc guest-session conduct (don't touch the queue unasked)               | `docs/conventions/guest-session-primer.md`                                                 |

## Terms (defined once)

- **bead** — one unit of work, stored in a dolt-backed bead store; ID shape
  `<prefix>-<suffix>` (e.g. `gc-i7a8c`; prefix maps to a rig).
- **sling** — `gc sling`: stamp `gc.routed_to` metadata on a bead so a target
  agent's pool claims it. Routing, not execution.
- **formula** — reusable TOML method (`mol-*`) describing multi-step work;
  spec in the gascity repo, `docs/reference/specs/formula-spec-v2.md`.
- **molecule** — an instantiated formula: real step beads plus a root bead
  that IS the control bead (run state lives in its notes/metadata).
- **wisp** — ephemeral molecule (v1 formulas / `--on` attachments).
- **drain** — the worker's "reclaim my session" signal (`gc runtime
drain-ack` or an implicit `bd close` of the root).
- **polecat** — fork-coding agent, worktree-per-bead, push forbidden except
  documented carve-outs. `gascity` rig's `default_sling_target` (2026-07-07).

## 1. The dispatch lifecycle, and where it stalls

```
decide → gc-sling → gc.routed_to stamped → worker claims → steps run → drain → slot free
```

Stage-by-stage, with the stall signature and the first thing to check:

| Stage            | Stall signature                           | First check                                                                                                                                                                                             |
| ---------------- | ----------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| sling            | `gc sling` exits 0 but nothing happens    | `gc-sling` stderr for `WARN target ... 0 live sessions` (see §2); wrong target strands silently                                                                                                         |
| routed → claimed | bead stuck `open` with `gc.routed_to` set | pool warm-idle (needs nudge) or bead still assigned to you (needs `--reassign`) — both owned by `CLAUDE.md`/`bead-dispatch.md`; backstops: `nudge-on-route` (event) + `routed-bead-nudger` (15m) orders |
| claimed → steps  | worker active but reading the wrong thing | formula attached? `--no-formula` text bead vs molecule (§4); step beads materialized? (§3 `pour` trap)                                                                                                  |
| mid-molecule     | root bead open, no step progress          | `bd mol current <root>` / `bd mol progress <root>`; check `metadata.summary_for_human` + `gc.halt_chain` — many halts are by design (§5)                                                                |
| drain            | bead closed but slot never frees          | worker typed `exit 0` instead of `gc session close` — wedge documented in `recurring-task-example.md`; recovery in `cityops-debugging-playbook`                                                         |

Two dispatch-shape rules with no other home:

- `gc sling` **rejects beads of type=epic**. The live pattern is a proxy bead
  - wisp; `bin/epic-review-sweeper` (lines ~230–290) is the reference
    implementation. Don't fight the rejection; copy that shape.
- Blocked/deferred beads that still carry `gc.routed_to` get respawning no-op
  polecats (unfiltered workflow-root projection, RCA gc-453188). The
  `blocked-routed-reaper` order clears them every 15m; if you see polecats
  spawning for a blocked bead, that reaper's log is
  `.gc/blocked-routed-reaper.log`.

## 2. What the gc-sling wrapper does beyond the documented basics

`bead-dispatch.md` owns the wrapper's original two jobs (formula injection
from `.gc/sling-intercept.yaml`, default `--nudge`). The wrapper
(`/home/ds/.local/bin/gc-sling`, source `bin/gc-sling`) has since grown two
guards that are documented nowhere else:

**Dead-target guard.** Before dispatch it counts live sessions matching the
target (template/work_dir basename match, slot suffix stripped). Zero live
sessions → loud stderr WARN + a `dead-target-warn` JSONL event. The sling
still proceeds — the guard warns, it does not block. 365 such events in
`.gc/sling-intercept.log` as of 2026-07-07; this fires for real, routinely.
Classic trigger: slinging gascity code work to `gascity/codex` (no workers)
instead of `polecat`.

**Per-bead worktree guard.** For targets whose formula has no worktree step
(the city default `mol-focus-review` does not), it runs
`git worktree add --detach <repo>-<beadshort> main` and records the path in
bead metadata `work_dir` + `gc.work_dir`. Born from the scix o835 WIP pile-up
(2026-06-14) where sequential beads dirtied the shared rig tree. Best-effort
and timeout-guarded: any failure logs (`worktree-skip`/`worktree-fail`) and
falls through to a normal sling. 723 `worktree-created` events as of
2026-07-07. Knobs (env, zero-edit):

| Env var                         | Effect                                    |
| ------------------------------- | ----------------------------------------- |
| `GC_SLING_NO_WORKTREE=1`        | kill-switch: skip the worktree guard      |
| `GC_SLING_DRYRUN=1`             | print what the guard would do, do nothing |
| `GC_SLING_BASE_BRANCH=<br>`     | worktree base (default `main`)            |
| `GC_SLING_INTERCEPT_CONFIG/LOG` | override config / audit-log paths         |

These per-bead worktrees land in `/home/ds/gascity-worktrees/` etc. and are
the "scattered" population reaped by `/home/ds/bin/reap-worktrees.sh`, not by
`gc doctor --fix` (two-population rule; details in
`cityops-debugging-playbook`).

## 3. Formula anatomy: the three local traps

Format and contract belong to `formula-spec-v2.md` (gascity repo). What that
spec will not tell you about THIS city:

1. **`pour = true` or your pool strands.** Every v2 (`[requires]
formula_compiler = ">=2.0.0"`) vapor-phase formula here sets `pour = true`
   at top level. Without it, vapor+graph.v2 compiles RootOnly and drops every
   step bead — workers find nothing to claim (compile.go:325; verified
   2026-06-30, noted in `mol-do-work.toml` and
   `mol-focus-review.formula.toml` headers). Copy the header comment when
   authoring a new formula.
2. **`issue` is a reserved var name.** Formulas-v2 auto-binds `issue`;
   declaring or passing `--var issue=<n>` collides and errors at
   instantiation. The maintained `mol-pr-from-issue` renamed its var to
   `issue_number` for exactly this reason. Older formulas (`mol-do-work`,
   `mol-focus-review`) still declare `[vars.issue]` and work because the
   binding comes from the slung bead, not a caller `--var`. When authoring:
   never name a caller-supplied var `issue`.
3. **One name, two definitions (resolution precedence).** Formulas resolve
   from BOTH `formulas/` in the city root AND rig-imported packs
   (`/home/ds/gascity-packs/pr-review/formulas/`,
   `/home/ds/gascity-packs/pr-pipeline/formulas/`). The copies have diverged.
   Verified 2026-07-07 for `mol-pr-from-issue`:

   ```bash
   gc formula show mol-pr-from-issue | grep -n 'var issue'
   # city scope → "--var issue=<number>"      (stale city copy, formulas/)
   gc formula show mol-pr-from-issue --rig gascity | grep -n 'var issue'
   # rig scope  → "--var issue_number=<number>" (pack copy wins for the rig)
   ```

   Rule: **`gc formula show <name> --rig <rig>` is ground truth for what a
   rig's worker will run.** Before editing a formula, confirm which file the
   scope you care about resolves, or you will edit a shadowed copy. 19 files
   in `formulas/`; 28 names resolvable at city scope (2026-07-07) — the extra
   9 come from packs.

## 4. Choosing how to dispatch work

| Shape of work                                | Dispatch                                                                            | Notes                                                                                                                                                                                                                                                         |
| -------------------------------------------- | ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Normal code bead on a rig                    | `gc-sling <target> <bead>` (no formula flag)                                        | picks up the rig/agent `default_sling_formula`; city default is `mol-focus-review` (`city.toml [agent_defaults]`, 2026-07-07) — implement → test → /simplify → /code-review with a reject-and-retry gate (`metadata.rejection_reason` → fresh worker retries) |
| Skill invocation or precise instruction text | `gc-sling <target> "Run /focus <bead>" --no-formula`                                | `--no-formula` makes a plain bead whose description IS the instruction; the formula wrap would bury it. This is the dominant live pattern — maintenance-cycle runs (order retired 2026-07-16; the Temporal maintenance-Run Schedule drives that dispatch now) and most 2026-07 dispatches in `.gc/sling-intercept.log` use it |
| Attach a specific method to an existing bead | `gc-sling <target> <bead> --on <formula>`                                           | `--on`, never `--formula <name>` (bool; owned by CLAUDE.md)                                                                                                                                                                                                   |
| Instantiate a formula fresh                  | `gc sling <target> <formula> --formula --var k=v ...` or `gc formula cook --attach` | v2 formulas referencing `{{convoy_id}}` or containing a drain step need a target convoy — the error message tells you; `cook --attach` is the escape hatch                                                                                                    |
| Epic review                                  | don't sling the epic                                                                | proxy-bead + wisp via `bin/epic-review-sweeper` (§1)                                                                                                                                                                                                          |

`.gc/sling-intercept.yaml` is intentionally near-empty (epic-level review
superseded per-bead rules, 2026-04-21); only 5 `formula_injected` events have
ever fired, all 2026-04-21. Add a rule there only for a genuine standing
bead-pattern → formula mapping, and expect the log to prove whether it fires.

## 5. The PR molecules: gates, carve-outs, kill-switch

The three write-side molecules are where dispatch meets the autonomy
boundary. **Provisional** (morning-ledger 2026-07-07, city-ops Q2/Q3): all
external artifacts remain per-action human-gated; the carve-outs below are
documented AS-IS, not to be extended, and nothing in this section is
trusted-unsupervised — spot-check every armed run. No skill may weaken these
gates.

| Molecule                                                                                                           | Steps                                                                        | Push behavior                                                                           |
| ------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| `mol-pr-from-issue` (7 declared + auto `workflow-finalize`)                                                        | pr-start → gate → ship → gate → gate-auto-push-eligibility → open-pr → drain | default-deny: `auto_push=false` soft-halts at branch-ready with `summary_for_human` set |
| `mol-pr-iterate` (6: intake → parse-feedback → propose-patch → apply → verify-clearance → report)                  | commits to the local PR branch only                                          | **never pushes**; maintainer pushes after review                                        |
| `mol-pr-revert` (6: intake-pr → verify-revertable → revert-branch → open-revert-pr → comment-on-original → report) | mayor-only invocation                                                        | carve-out per run, verified once in revert-branch                                       |

**The auto-push gate stack** (`mol-pr-from-issue`, both copies): pushing
requires ALL of (a) caller slung with `--var auto_push=true`, (b) every
mechanical check in `gate-auto-push-eligibility` passed and wrote
`evidence.gate_passed=true` + a bypass token, (c) the kill-switch file
`/home/ds/.gc/auto-push-armed.flag` exists. Absence of any one → halt at
branch-ready. **The flag does not exist as of 2026-07-07** — autonomous PR
push is disarmed city-wide right now; creating that file is a human decision,
never an agent's.

**mol-pr-iterate halts to mayor** (mail + `gc.halt_chain=true`) on: feedback
too ambiguous to parse, plan exceeding 200 LOC, or verify-clearance failing
after 2 apply iterations. A halted iterate molecule is the system working,
not a bug — read `summary_for_human` on the root before "fixing" anything.

**mol-pr-revert carve-out**: push permitted only when root metadata
`auto_push == "true"` AND verify-revertable's verdict is `pass`, OR the
emergency env `GASCITY_SHIP_BYPASS == "mayor_revert"` set by mayor for
verify-revertable false-negatives.

## 6. FCTR / routing.yaml: reality check

`.gc/routing.yaml` declares per-(formula, step) decisions — `model_tier`,
`grounded_review` (Codex), `human_gate` — for a Formula-Compile-Time Router.
The as-verified state (2026-07-07):

- Phase 1 is **measure-only by design**: `orders/route-decide-report.toml`
  says "formulas do not yet consume the stamped tier".
- Even the measuring side shows nothing: **zero `routing.*` metadata stamps**
  exist in the city store or any rig store (checked `beads`, `gascity`,
  `dec`, `mem`, `brains`, `gpk`, `gascity_dashboard`, `EnterpriseBench` via
  the sanctioned TCP query), the installed `gc` binary contains no
  `routing.yaml` reference, and no `~/.gc/routing-report-*.json` has ever
  been written.

Operational meaning: **editing `.gc/routing.yaml` changes nothing that runs
today.** Actual model/tool routing comes from each agent's provider in
`city.toml`/`agent.toml` and the formula step text itself. Treat routing.yaml
as declared intent for a future rollout; do not "fix" a dispatch by editing
it, and do not cite it as evidence of what tier ran. `bin/route-decide-report
--human` is the measurement tool if stamps ever appear.

## 7. Worked example: one bead, healthy and stranded (real log)

`.gc/sling-intercept.log` (JSONL, 7,827 lines as of 2026-07-07) records every
wrapper decision. Bead `gc-i7a8c`, dispatched to the gascity polecat pool,
shows both outcomes 23h apart:

Healthy dispatch, 2026-07-06 — worktree guard fires, sling passes through:

```json
{"ts":"2026-07-06T03:03:16Z","event":"worktree-created","bead":"gc-i7a8c","target":"/home/ds/gascity/polecat","formula":"","reason":"/home/ds/gascity-worktrees/polecat-1-i7a8c"}
{"ts":"2026-07-06T03:03:17Z","event":"worktree-recorded","bead":"gc-i7a8c","target":"/home/ds/gascity/polecat","formula":"","reason":"/home/ds/gascity-worktrees/polecat-1-i7a8c"}
{"ts":"2026-07-06T03:03:17Z","event":"passthrough","bead":"gc-i7a8c","target":"/home/ds/gascity/polecat","formula":"","reason":"--no-formula set"}
```

Same bead re-slung 2026-07-07 02:34 — the pool had zero live sessions:

```json
{"ts":"2026-07-07T02:34:25Z","event":"dead-target-warn","bead":"gc-i7a8c","target":"/home/ds/gascity/polecat","formula":"","reason":"0 live sessions for target"}
{"ts":"2026-07-07T02:34:32Z","event":"worktree-skip","bead":"gc-i7a8c","target":"/home/ds/gascity/polecat","formula":"","reason":"no live work_dir for target"}
{"ts":"2026-07-07T02:34:32Z","event":"passthrough","bead":"gc-i7a8c","target":"/home/ds/gascity/polecat","formula":"","reason":"--no-formula set"}
```

Reading: the second sling was stamped but had no claimer — exactly the
silent-strand the guard exists to surface. The follow-up is NOT to re-sling
harder; it is to find out why the pool is empty (`gc session list`, then
`cityops-debugging-playbook`) and let `routed-bead-nudger` or a fresh
worker pick the bead up once the pool lives. Trace any bead the same way:

```bash
grep '"bead":"<bead-id>"' /home/ds/gas-city/.gc/sling-intercept.log | tail -20
```

## 8. Inspecting a live molecule run

```bash
bd mol current <root-bead>      # step-by-step position ([done]/[current] markers)
bd mol progress <root-bead>     # progress summary
bd show <root-bead> --json | jq '.[0].metadata'   # halt flags, evidence.*, summary_for_human
gc formula version-check <root-bead>              # did the on-disk formula drift since instantiation?
```

If `bd` cannot reach the store (port-discovery flake, live finding
2026-07-07): `export BEADS_DOLT_PORT=$(cut -d: -f2
/home/ds/gas-city/.beads/dolt/.dolt/sql-server.info)` and retry. Endpoint
rules and hard prohibitions (`bd dolt status` kills the server) are owned by
`compass-dolt` / `cityops-dolt-beads-reference` — never improvise around
them.

## Provenance and maintenance

Verified live on this host 2026-07-06/07 by the retiring-fellow session. One
re-verification command per drift-prone claim:

| Claim                                                                       | Re-verify                                                                                                                                                                                                                                                                  |
| --------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| gascity default_sling_target=polecat; city default formula mol-focus-review | `grep -n 'default_sling' /home/ds/gas-city/city.toml`                                                                                                                                                                                                                      |
| wrapper guards + env knobs                                                  | `grep -n 'dead-target\|GC_SLING_NO_WORKTREE' /home/ds/gas-city/bin/gc-sling`                                                                                                                                                                                               |
| intercept log size / event mix                                              | `wc -l /home/ds/gas-city/.gc/sling-intercept.log; grep -c dead-target-warn $_`                                                                                                                                                                                             |
| 19 city formula files vs 28 resolvable names                                | `ls /home/ds/gas-city/formulas/*.toml \| wc -l; gc formula list \| wc -l`                                                                                                                                                                                                  |
| city-vs-pack copy divergence (mol-pr-from-issue)                            | `diff -q /home/ds/gas-city/formulas/mol-pr-from-issue.formula.toml /home/ds/gascity-packs/pr-review/formulas/mol-pr-from-issue.formula.toml`                                                                                                                               |
| auto-push kill-switch disarmed                                              | `ls /home/ds/.gc/auto-push-armed.flag` (absent = disarmed)                                                                                                                                                                                                                 |
| zero routing.* stamps (FCTR not live)                                       | `PORT=$(cut -d: -f2 /home/ds/gas-city/.beads/dolt/.dolt/sql-server.info); dolt --host 127.0.0.1 --port $PORT --user root --no-tls --password '' --use-db gascity sql -q "SELECT COUNT(*) FROM issues WHERE JSON_VALUE(metadata,'\$.\"routing.model_tier\"') IS NOT NULL;"` |
| sling-intercept rules still near-empty                                      | `cat /home/ds/gas-city/.gc/sling-intercept.yaml`                                                                                                                                                                                                                           |
| pour=true trap still applies                                                | header comments in `formulas/mol-do-work.toml` / `mol-focus-review.formula.toml`; retest after any gc upgrade that touches formula compile                                                                                                                                 |

Provisional positions relied on above (mark for revision once Stephanie
answers the discovery questions): permanent-gate list and the
no-trusted-unsupervised default in §5 (morning-ledger-2026-07-07, city-ops
Q2/Q3).
