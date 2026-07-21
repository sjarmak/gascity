---
name: gc-doctrine
description: >-
  Gas City's settled doctrine taught as violation patterns: ZFC, the Bitter
  Lesson test, zero hardcoded roles, GUPP, NDI, no status/PID files, SDK
  self-sufficiency, and the Primitive Test. Load BEFORE writing or reviewing
  Go in this repo whenever you are about to (a) branch on an agent/role name,
  (b) put a judgment call, heuristic, or threshold decision in Go, (c) write a
  PID/lock/status file to track a process, (d) add a feature flag, skills
  registry, or MCP/tool registration, (e) make an SDK feature depend on a
  specific configured agent, or (f) decide whether a capability belongs in the
  SDK or in prompts/config. Also load when a reviewer cites ZFC, Bitter
  Lesson, zero-roles, GUPP, NDI, or self-sufficiency and you need to know
  what the objection means and how past violations were fixed.
---

# gc-doctrine — settled decisions as violation patterns

Tier 1 (single-session; no subagents, no worktrees; safe under
`DISABLE_INTERACTIVITY=1` and `gc sling`).

`AGENTS.md` at the repo root is the constitution: it states the principles.
This skill teaches what a **violation** looks like in practice, how each
principle is (or is not) machine-enforced, and what happened historically
when code shipped against doctrine. Nothing here overrides `AGENTS.md`; when
in doubt, that file wins.

## When NOT to use this skill

| You actually need                                            | Use instead                                                                                    |
| ------------------------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| Package map, reading order, the nine concepts                | sibling skill `gc-orientation`, or `engdocs/architecture/nine-concepts.md`                     |
| Bead/molecule/formula/order semantics                        | sibling skill `gc-meow-work-model`                                                             |
| PR scope discipline, staged landing, what needs human review | sibling skill `gc-change-workflow`                                                             |
| Make targets, test tiers, CI gates                           | sibling skill `gc-build-verify`, `TESTING.md`                                                  |
| SDK-vs-consumer decision in full detail                      | `engdocs/contributors/primitive-test.md` (the canonical framework; §8 below is only a pointer) |

Sibling `gc-*` skills are part of the same departure library and may still be
landing in `.claude/skills/` as of 2026-07-06.

## Terms (defined once, used throughout)

| Term                     | Meaning                                                                                                                                                                                                   |
| ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **ZFC**                  | Zero Framework Cognition. Go moves bytes; models decide. If a line of Go contains a judgment call, the decision belongs in a prompt.                                                                      |
| **Bitter Lesson test**   | A primitive must become MORE useful as models improve. Heuristics, decision trees, and capability gates become LESS useful, so they are excluded.                                                         |
| **Zero hardcoded roles** | No orchestration role name (mayor, deacon, witness, polecat, refinery, crew, dog, ...) in Go source. Roles are pure configuration.                                                                        |
| **GUPP**                 | "If you find work on your hook, YOU RUN IT." An agent behavior rule rendered into prompts, never enforced by Go.                                                                                          |
| **NDI**                  | Nondeterministic Idempotence. Sessions are disposable; beads, hooks, and molecules are durable. The system converges because multiple independent observers check the same persistent state idempotently. |
| **SDK self-sufficiency** | Every SDK infrastructure operation works with only the controller running. No SDK feature may depend on a specific user-configured agent existing.                                                        |
| **Primitive Test**       | Three necessary conditions (Atomicity + Bitter Lesson + ZFC) for whether a capability belongs in the SDK at all.                                                                                          |
| **Consumer layer**       | Everything that is not SDK Go code: agent prompts, pack assets, `bd` CLI usage, user config, external scripts.                                                                                            |
| **Controller**           | The per-city daemon loop in `cmd/gc/city_runtime.go` that drives all SDK infrastructure operations.                                                                                                       |

## Doctrine-to-enforcement map

"Automated gate" means a test or CI job fails your build. Where the gate
column says _review only_, the doctrine is enforced by maintainer review and,
historically, by revert (see §10). Verified against the tree on 2026-07-06.

| Doctrine                                                 | One-line test                                                                                     | Automated gate                                                                                                                                                                                                                                                                                                                                                   |
| -------------------------------------------------------- | ------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Zero hardcoded roles                                     | Does any Go line reference a role name?                                                           | **Review only** (no CI scan exists as of 2026-07-06)                                                                                                                                                                                                                                                                                                             |
| ZFC                                                      | Does any Go line make a judgment call?                                                            | Review only                                                                                                                                                                                                                                                                                                                                                      |
| Bitter Lesson                                            | Would a 10x model make this code less necessary?                                                  | Review only                                                                                                                                                                                                                                                                                                                                                      |
| GUPP                                                     | Is Go code trying to confirm/queue/ask before running hooked work?                                | Review only (rendered in prompts, e.g. `internal/bootstrap/packs/core/assets/prompts/pool-worker.md:8`)                                                                                                                                                                                                                                                          |
| NDI / no status files                                    | Are you writing a PID/lock/status file, or trusting one?                                          | Review only                                                                                                                                                                                                                                                                                                                                                      |
| SDK self-sufficiency                                     | Does removing an `[[agent]]` entry break an SDK feature?                                          | Review only                                                                                                                                                                                                                                                                                                                                                      |
| Typed events (corollary of "object model at the center") | Every `events.KnownEventTypes` constant has a registered payload                                  | `TestEveryKnownEventTypeHasRegisteredPayload` (`internal/api/event_payloads_coverage_test.go:16`)                                                                                                                                                                                                                                                                |
| Typed wire                                               | No hand-written JSON / `map[string]any` on HTTP/SSE paths                                         | `TestOpenAPISpecInSync` (`internal/api/openapi_sync_test.go:25`) + spec-ci                                                                                                                                                                                                                                                                                       |
| Worker boundary (active migration)                       | `cmd/gc` non-test files route session ops through `worker.Handle`                                 | `TestGCNonTestFilesStayOnWorkerBoundary` (`cmd/gc/worker_boundary_import_test.go:11`)                                                                                                                                                                                                                                                                            |
| Agent config four-sibling rule                           | New `config.Agent` field also lands in `AgentPatch`, `AgentOverride`, apply funcs, pool deep-copy | `TestAgentFieldSync` (struct defs) + `TestApplyAgentPatchCoversAllFields` / `TestApplyAgentOverrideCoversAllFields` (`internal/config/field_sync_test.go`) + `TestDeepCopyAgentCoversAllFields` (`cmd/gc/pool_test.go`) — every site is now coverage-tested; AGENTS.md's "must be checked manually" note predates these tests (see `gc-config-system` Runbook A) |

Run the automated gates for a doctrine-adjacent change:

```bash
go test ./internal/api -run 'TestEveryKnownEventTypeHasRegisteredPayload|TestOpenAPISpecInSync' -count=1
go test ./internal/config -run 'TestAgentFieldSync|TestApplyAgentPatchCoversAllFields|TestApplyAgentOverrideCoversAllFields' -count=1
go test ./cmd/gc -run 'TestGCNonTestFilesStayOnWorkerBoundary|TestDeepCopyAgentCoversAllFields' -count=1
```

The rest of this skill is the review-only doctrines, one section each:
statement, violation patterns, the fix shape, and real history.

## 1. Zero hardcoded roles

**Statement** (`AGENTS.md`): "If a line of Go references a specific role
name, it's a bug." Roles are pure configuration; the SDK has no built-in
Mayor, Deacon, Polecat, or any other role.

**Violation patterns:**

- `if role == "mayor"` or `strings.Contains(session, "polecat")` anywhere in
  control flow. This is the canonical design error named in `AGENTS.md`.
- A switch/case in a script or Go file mapping role names to behavior
  (colors, grouping, routing, priorities). See the worked example in §9.
- Test cases that only pass because they assume the Gas Town role taxonomy
  (fixed in 489d74523, "avoid concrete role names in stale session reaper
  cases").
- Defaults like "notify the mayor" baked into SDK code paths. Notification
  targets come from config or prompts.

**Fix shape:** drive the behavior from a _structural primitive_ the SDK
already owns: session-name separators (`--` rig scope, `__` city scope,
`-N` pool suffix, per `internal/agent/session_name.go`), bead metadata,
config fields, or scope. If no primitive expresses it, the behavior belongs
in the consumer layer.

**Nuances, verified 2026-07-06:**

- Role names that survive in-tree are illustrative doc comments and CLI help
  examples (e.g. `internal/agent/session_name.go:50-52`,
  `cmd/gc/cmd_sling.go:73`), not control flow. Do not add more, even in
  comments; examples can use neutral names.
- `role` in chat-transcript parsing (`internal/sessionlog/opencode_reader.go`,
  message roles like `assistant`/`developer`) is an LLM wire term, unrelated
  to this rule.
- Sweep before you push:

```bash
grep -rn '"mayor"\|"deacon"\|"witness"\|"polecat"\|"refinery"\|"crew"' \
  --include="*.go" cmd internal | grep -v _test
```

Expect only pre-existing comment/help-text hits; any new hit, or any hit in
an expression, is a finding.

## 2. ZFC — Zero Framework Cognition

**Statement** (`AGENTS.md`): Go handles transport, not reasoning. The test:
does any line of Go contain a judgment call? `if stuck then restart` is
framework intelligence; move the decision to the prompt.

**Violation patterns:**

- Recovery strategy in Go: "if session idle > N and has assigned work, nudge
  it" as SDK behavior. The idle-session nudger was reverted for exactly this
  churn (3bc34e0db, 2026-04-08); see §10.
- Severity classification in Go: deciding which events are worth escalating.
  The Gas Town config replaced a mandatory "notify mayor in ALL cases" rule
  with witness _judgment-based_ notification, i.e. the model decides
  (e5145f567, Section 6d).
- Setup logic that belongs to the agent: worktree preparation steps were
  added to the polecat _prompt_, explicitly labeled "ZFC, not Go code"
  (c2ede3b44).
- Keyword/regex matching on bead titles or transcript text to infer meaning.
- Hardcoded thresholds encoding a semantic judgment ("more than 3 resets
  means stuck"). Threshold _comparison_ is fine when the threshold value
  comes from config and the consequence is mechanical (publish an event);
  Health Patrol works this way: probe (Session), compare config-supplied
  thresholds (Config), publish stalls (Event Bus). Deciding what a stall
  _means_ stays with agents.

**Fix shape:** Go gathers state and executes verbs; a prompt (or the agent
reading events) makes the call. If you cannot express the behavior without an
`if` that a smarter model would make differently, it is cognition.

## 3. Bitter Lesson test and the permanent exclusions

**Statement** (`AGENTS.md`): every primitive must become MORE useful as
models improve. The concrete form: imagine a model 10x more capable. If this
capability becomes less necessary, it belongs in the consumer layer.

**Violation patterns** are usually proposals, not diffs. These are the ones
that get rejected on sight because `AGENTS.md` lists them as _permanent_
exclusions, not "not yet":

- **A skills system** — the model IS the skill system. (PROVISIONAL
  reading, this author's reconciliation of AGENTS.md with in-tree reality,
  not a maintainer statement: skill files shipped as pack _assets_, e.g.
  `internal/bootstrap/packs/core/skills/gc-dispatch/`, are consumer-layer
  content the SDK merely copies into place, and the exclusion bans Go-side
  skill registries and invocation machinery. Confirm with the maintainer
  before relying on this scoping.)
- **Capability flags** — a sentence in the prompt is sufficient.
- **MCP/tool registration** — if a tool has a CLI, the agent uses it.
- **Decision logic in Go** — see §2.
- **Hardcoded role names** — see §1.

Do not open a PR adding any of these. Do not "temporarily" add one behind a
config option; config _presence_ is the activation mechanism (Levels 0-8),
and presence of an excluded subsystem is still the subsystem.

## 4. GUPP

**Statement** (`AGENTS.md`): "If you find work on your hook, YOU RUN IT."
No confirmation, no waiting. The hook having work IS the assignment. This is
rendered into agent prompts via templates, **not enforced by Go code**.

**Violation patterns:**

- Writing Go that enforces GUPP: e.g., controller code that punishes or
  detects "agent saw work and did not start." Enforcement is a prompt
  concern; the SDK's job ends at delivering the hook.
- Writing prompts or pack templates that _undermine_ GUPP: "confirm with the
  user before starting hooked work," approval queues in front of the hook.
- Adding Go-side "are you sure" staging between hook and execution.

**Canonical rendering** to copy from, not reinvent:
`internal/bootstrap/packs/core/assets/prompts/pool-worker.md` (the "GUPP —
If you find work, YOU RUN IT" section and its startup protocol, including
the claim-verification steps and `gc runtime drain-ack` on empty).

## 5. NDI — Nondeterministic Idempotence

**Statement** (`AGENTS.md`): the system converges to correct outcomes because
work (beads), hooks, and molecules are persistent. Sessions come and go; the
work survives. Redundancy is the reliability mechanism.

**Violation patterns:**

- Treating a session as the durable record: storing progress only in a
  session's memory or transcript instead of bead state/metadata. If the
  session dies, the work must be recoverable from beads alone (the pool
  worker prompt's crash-recovery step 1 exists for this reason).
- Designing a mechanism that breaks if run twice. Sweeps, reconcilers, and
  patrols all assume multiple independent observers will re-check the same
  state; your operation must be idempotent under that.
- "Exactly one component owns detecting X" designs that fight the redundancy
  model instead of using it.
- Session lifecycle state held anywhere but its bead-backed projection
  (`internal/session/lifecycle_projection.go`).

## 6. No status files — query live state

**Statement** (`AGENTS.md`): never write PID files, lock files, or state
files to track running processes. Discover state by querying the system
directly (process table, port scans, `ps`, `lsof`). Status files go stale on
crash and create false positives.

**Violation patterns:**

- Writing `<name>.pid` on spawn and trusting it on the next tick.
- A "running" boolean persisted anywhere. Liveness is computed, not stored.
- Cleanup code keyed off a state file instead of the live process table or
  tmux server (see `internal/runtime/tmux/` and
  `internal/runtime/process_control.go` for the query-based idiom).

**Boundary note:** durable _work_ state in beads is doctrine (§5); durable
_process_ state in files is anti-doctrine. The dividing line is whether the
fact can silently become false when a process crashes.

## 7. SDK self-sufficiency

**Statement** (`AGENTS.md`): every SDK infrastructure operation (gate
evaluation, health patrol, bead lifecycle, order dispatch) must function with
only the controller running. Test: if removing an `[[agent]]` entry from
`city.toml` breaks an SDK feature, it's a violation.

**Violation patterns:**

- An SDK code path that mails, nudges, or waits on a specific configured
  agent to make progress ("orders don't dispatch unless a dispatcher agent
  exists").
- Documentation or defaults that assume a Gas Town-shaped city (a mayor
  session, a witness) for an SDK feature to work.
- Tests for SDK features that only pass with a role-bearing example config
  loaded.

**Fix shape:** the controller drives infrastructure; user agents execute
work. If a feature needs an acting brain, the brain is _whichever_ agent
config supplies, resolved through config, never a named one.

## 8. The Primitive Test (pointer, not a restatement)

Before adding ANY new capability to the SDK, apply the three-condition
framework in **`engdocs/contributors/primitive-test.md`** (canonical; do not
rely on this summary alone): Atomicity (do concurrent agents need the SDK to
make it safe?), Bitter Lesson (§3), ZFC (§2). All three must hold or the
capability belongs in the consumer layer. That doc also owns the corollary:
when a dependency (e.g. `bd`) has a concurrency bug, fix it upstream instead
of wrapping it in Gas City.

Related settled decisions you do not relitigate (full list in `AGENTS.md`
"Design decisions (settled)"): city-as-directory, fresh binary (not a Gas
Town fork), TOML config, tutorials win over architecture docs, no premature
abstraction (no interface until two implementations exist), mayor is
overseer not worker, `internal/` packages for now, zero hardcoded roles.

## 9. Worked example — role names leaked into a pack script (#1663)

Commit `432d1f610` (2026-05-08), "fix(tmux): drive tmux-theme.sh from
session-name primitives, not role names," is the cleanest zero-roles
violation-and-fix on record. Study it:

```bash
git show 432d1f610 -- examples/gastown/packs/gastown/assets/scripts/tmux-theme.sh
```

**The violation.** The theme script detected an agent's role from `$AGENT`
via hardcoded patterns (`*/witness`, `*--witness`, `*/crew/*`) and mapped
role → color/icon. Two failures followed, both _predicted by doctrine_:

1. The role list rotted: the `*/crew/*|*--crew--*` branch never matched
   anything, because the session-name primitive
   (`internal/agent/session_name.go`) never produces a literal `crew`
   substring; `gascity/navani` becomes `gascity--navani`. Crew agents
   silently fell through to the default tier.
2. Role awareness in a script whose actual job was _scope grouping_ meant
   every new role (in anyone's config, remember: roles are user config)
   required editing the script.

**The fix.** Tier selection now reads only the structural separators the SDK
guarantees, with zero role awareness. The script was later deleted from
`origin/main` entirely, so verify against the fix commit, not the live tree
(`git show 432d1f610:examples/gastown/packs/gastown/assets/scripts/tmux-theme.sh | sed -n '23,26p'`):

```sh
case "$SESSION" in
*--*)       tier="rig" ;;     # anything rig-scoped
*__*)       tier="scope" ;;   # anything city-scoped
*-[0-9]*)   tier="pool" ;;    # pool members
```

The same pattern and same day: `0365d8916` fixed `cycle.sh`, whose
`mayor|deacon` / `witness|refinery|polecat-*` hardcoding stranded operators
on single-member groups whenever the assumed roles were dormant.

**The generalizable lesson:** hardcoded role taxonomies do not fail loudly;
they fail as _silently wrong behavior for every city whose config differs
from Gas Town's_. When you feel the urge to enumerate roles, find the
structural primitive that actually carries the distinction.

## 10. Reverts prove the doctrine is enforced

When doctrine and shipped code conflict, the code gets reverted, even when
it works. Verify any of these with `git show -s <sha>`.

| Commit                                             | What happened                                                                                                                                                      | Doctrine angle                                                                  |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------- |
| `3bc34e0db` (2026-04-08)                           | Idle-session nudger reverted: SDK-side "wake idle sessions with assigned work" caused churn; backed out "until the wake/nudge architecture is redesigned"          | Framework behavior encoding a judgment about when agents should be prodded (§2) |
| `6450f8869`, `25d3da6d7` (2026-03)                 | formula-v2/orders-v2 big-bang reverted twice, then re-landed staged (`948e12c87`); rc3 later shipped with formula_v2 deliberately omitted (`bb824a86d`)            | Staged landing beats big-bang; see `gc-change-workflow`                         |
| `b8120d697` → un-reverted `331c66ceb` (2026-06-07) | Per-dispatch model selection reverted NOT because wrong, but because a new core-platform contract landed without maintainer review; restored next day after review | Process doctrine: contracts route to humans; see `gc-change-workflow`           |
| `19e34ab71` (2026-06-07)                           | ~700-line retention feature merged under a 4-line CI-fix title; whole PR reverted                                                                                  | Scope discipline; see `gc-change-workflow`                                      |

**Parked, not dead** (PROVISIONAL, 2026-07-07: standing in for maintainer
answers; re-confirm before acting): the idle nudger, formula_v2, the
delivery-phase state machine (#3177), and the oversight-rig/Slack pack are
all treated as parked pending maintainer decision. Do not re-land or delete
any of them without an explicit maintainer call. The nudger's promised
redesign has not re-landed under that name as of 2026-07-06.

## 11. Pre-PR doctrine checklist

Run through this before `gc-change-workflow` / review:

- [ ] No orchestration role name added to Go source, comments included (§1 sweep command).
- [ ] No new `if` in Go that a smarter model would decide differently (§2). Decisions moved to prompts/config.
- [ ] Nothing from the permanent-exclusions list, in any disguise (§3).
- [ ] Prompts you touched still say run-it-now; no confirmation gates in front of hooked work (§4).
- [ ] Your mechanism survives a second, concurrent run of itself, and a session crash mid-way (§5).
- [ ] No PID/status/state file written or trusted; liveness queried live (§6).
- [ ] Deleting any single `[[agent]]` from a city config leaves your SDK feature working (§7).
- [ ] New capability passed the Primitive Test, or went to the consumer layer (§8).
- [ ] Typed-events / typed-wire / worker-boundary / field-sync gates green (commands in the enforcement map).

## Provenance and maintenance

Written 2026-07-06 by the retiring-fellow distillation campaign (discovery
report and provisional maintainer answers live outside this repo in the
ds-research workspace; they are context, not load-bearing sources). Every
command, path, test name, and commit SHA above was verified against this
repo at HEAD `58e0b8dbb`. Items marked PROVISIONAL follow stand-in answers
recorded 2026-07-07 and must be re-confirmed with the maintainer.

Re-verification one-liners (run from repo root):

| Claim                                                          | Re-verify with                                                                                                                                                               |
| -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Doctrine statements & permanent exclusions unchanged           | `grep -n "ZFC\|Bitter Lesson\|GUPP\|Nondeterministic\|self-sufficiency\|does NOT contain" AGENTS.md`                                                                         |
| Enforcement tests still exist at cited locations               | `grep -rn "func TestEveryKnownEventTypeHasRegisteredPayload\|func TestOpenAPISpecInSync\|func TestGCNonTestFilesStayOnWorkerBoundary\|func TestAgentFieldSync" internal cmd` |
| Still no CI scan for role names (update §1/map if one appears) | `grep -rln "mayor" .github/workflows/ .golangci.yml`                                                                                                                         |
| Role names still absent from Go control flow                   | §1 sweep command                                                                                                                                                             |
| tmux-theme.sh fix shape (file deleted from main; read history) | `git show 432d1f610:examples/gastown/packs/gastown/assets/scripts/tmux-theme.sh \| sed -n '23,26p'`                                                                          |
| GUPP rendering path unchanged                                  | `grep -rn "GUPP" internal/bootstrap/packs/core/assets/prompts/pool-worker.md`                                                                                                |
| Cited commits resolve                                          | `for c in 432d1f610 0365d8916 3bc34e0db b8120d697 331c66ceb 19e34ab71 e5145f567 c2ede3b44 489d74523 948e12c87 bb824a86d; do git log -1 --oneline $c                          |     | echo MISSING $c; done` |
| Parked-limb status (PROVISIONAL)                               | ask the maintainer; check `git log --grep="idle-session nudger"` and issue #3177                                                                                             |
