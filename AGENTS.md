# Gas City

Gas City is an orchestration-builder SDK — a Go toolkit for composing
multi-agent coding workflows. It extracts the battle-tested subsystems from
Steve Yegge's Gas Town (github.com/steveyegge/gastown) into a configurable
SDK where **all role behavior is user-supplied configuration** and the SDK
provides only infrastructure. The core principle: **ZERO hardcoded roles.**
The SDK has no built-in Mayor, Deacon, Polecat, or any other role. If a
line of Go references a specific role name, it's a bug.

You can build Gas Town in Gas City, or Ralph, or Claude Code Agent Teams,
or any other orchestration pack — via specific configurations.

**Why Gas City exists:** Gas Town proved multi-agent orchestration works,
but all its roles are hardwired in Go code. Steve realized the way work is
expressed — as beads composed into formulas — was powerful enough to abstract
roles into configuration. Gas City extracts that insight into an SDK where Gas
Town becomes one configuration among many.

## Current integration mission

This fork is integrating **Gas City + T3 Code + a DoltLite-backed beads
store**. The target is not a permanent divergence from upstream Gas City.
The target is a maintainable integration branch whose useful changes can
track upstream easily and whose fork-specific behavior is isolated behind
small, obvious ownership boundaries.

When working here, assume three codebases matter:

- **This repo** (`/data/projects/gascity`): the Gas City SDK and the fork
  integration layer.
- **T3 Code** (`/data/projects/t3code` when present): the UI/runtime that
  hosts visible agent threads through the `t3bridge` runtime provider.
- **Beads / bd with DoltLite**: the work ledger backend. Gas City should use
  the normal beads abstractions and keep DoltLite-specific read/write behavior
  contained in beads/provider boundaries.

### Upstream alignment rules

- Keep `upstream/main` easy to merge. Prefer new files, small adapters,
  and fork-owned packages over broad edits to upstream-owned code.
- If upstream code must change, make the patch minimal and idiomatic so it
  can be rebased, dropped, or proposed upstream cleanly.
- Do not bury T3 Code or DoltLite assumptions in generic SDK paths. Put
  provider-specific behavior behind the existing runtime, config, or beads
  backend boundaries.
- Before rebuilding a missing feature from scratch, search history. This
  fork has repeatedly lost working code during branch churn; older branches
  and commits often already contain the fix.
- Treat archived plans and audits as evidence, not gospel. Confirm against
  current code and current upstream before porting.

### Feature archaeology workflow

Use git history deliberately when a feature appears missing or regressed:

```bash
git remote -v
git fetch upstream
git log --all --oneline --decorate --grep '<keyword>'
git log --all --oneline --decorate -- <path>
git show <commit>:<path>
git diff upstream/main...HEAD -- <path>
git range-diff upstream/main...HEAD
```

Useful search targets:

- T3 bridge/runtime: `t3bridge`, `T3Bridge`, `internal/runtime/t3bridge`,
  `cmd/gc/template_resolve_t3bridge.go`
- DoltLite/beads backend: `doltlite`, `DoltLite`, `internal/beads`,
  `providers.go`, `beads_provider_lifecycle`
- Prior parity work: `engdocs/archive/analysis/gastown-upstream-audit.md`,
  `engdocs/archive/analysis/feature-parity.md`,
  `engdocs/contributors/dolt-regression-audit.md`

If history contains working code, prefer porting the smallest proven slice
instead of inventing a parallel mechanism.

**A CLOSED bead is not proof the fix landed.** This fork routinely closes work
"branch-ready, not pushed", naming a branch in the close reason. Those branches
get reaped, and the bead still reads CLOSED while the bug runs live on main.
Before trusting any bead that cites a fix, verify the branch still exists:

```bash
git branch -a --list '*<slug>*'
git cat-file -t <sha>   # loose objects often survive a deleted branch
```

A close reason naming a SHA (`"landed on main at <sha>"`) needs the stronger
check, because there is no branch left to look for and the sentence already
asserts the thing you are testing:

```bash
git merge-base --is-ancestor <sha> origin/main && echo IN || echo NOT_IN
git grep <symbol> origin/main -- <path>   # a rebase would have changed the SHA
```

Run both. Ancestry alone can be a false negative if the work was rebased or
squashed on the way in, so confirm the absence by grepping `origin/main` for a
SYMBOL the fix introduced (a new file, a new constant) rather than trusting the
SHA. On 2026-08-27 a 5-commit stack from 2026-07-17 failed both checks: gc-ewk4
(P1, closed "landed on main at e3ce4b7af") and gc-nuhl (P1) had run live on main
for six weeks, and two further beads (gc-5xyt, gc-175t) had premises written
against that unlanded code.

A missing branch with surviving commit objects is a salvage, not a re-author:
cherry-pick onto current `origin/main` and resolve conflicts by keeping main's
structure and porting the semantics into it. Beware that `git cherry-pick
--abort` rolls back commits already completed earlier in the same multi-commit
pick; recover with `git reset --hard <sha>` on the still-reachable object, or
apply the remaining commits one at a time.

The same caution applies to any bead whose description was written against an
unlanded fix: it describes code that is not in the tree. Confirm against
`origin/main` by SHA before acting on such a premise. (Precedent: gc-j4sr, a P0
closed 2026-07-17 on a deleted branch, ran live for six weeks and left 506 of 636
`gc.work_branch` values naming the shared rig checkout; two downstream beads had
premises written against its unlanded code.)

**Two individually-correct PRs can merge into a tree that does not compile.**
Neither author is wrong and neither branch is red, so nothing in the per-PR
gates catches it: one PR changes a function signature, a second PR written
against the older base adds a caller, and the defect exists only in the merge
result. Test files are where this lands hardest, because a broken test binary
does not fail a build gate that only compiles non-test code, and every test in
that package silently stops running while the gate still reports a pass. Before
trusting any green check over a package, confirm its test binary actually
builds at the merge commit:

```bash
go vet ./cmd/gc/            # compiles the test files too
```

(Precedent 2026-08-27, gc-xwerd: `filterAssignedWorkBeadsForPoolDemand` grew a
`[]session.Info` parameter in `f6b3704fe` (#5660); tip commit `bbcc7bcf4`
(#5094) added two calls against the pre-#5660 signature. `origin/main` could
not build its `cmd/gc` test binary, so every test in the repo's largest package
was unrunnable. The required CI gate does not appear to vet or build the
`cmd/gc` test binary on the merge result.)

**And do not assume the damage stops at the test binary.** The same class recurs
on the PRODUCTION build, where the sentence above will mislead you: on
2026-08-28 `origin/main` at `eec4a2fb6` failed a plain `go build ./cmd/gc/`
(three `undefined: store` / `undefined: graphStore` errors in
`cmd/gc/wisp_step_inject.go`), so `gc` could not be built from a fresh clone at
all. `3b44512c8` (#5488) split one `store` parameter into `workStore` and
`graphStore`; `29b3687cf` (#5297), written against the pre-#5488 base, merged
callers using the old name. Nine PRs had merged on top without anyone noticing.
So run the build, not only the vet, and run it in a clean worktree cut from the
ref rather than reading a check's colour:

```bash
git worktree add -q --detach /var/tmp/mainbuild origin/main   # /var/tmp, not /tmp
cd /var/tmp/mainbuild && go build ./cmd/gc/ && go vet ./...
```

When you find one, resolve it by porting the LATER semantics onto main's current
structure, never by collapsing the refactor back to its pre-split form to make
the compiler quiet: that silently reverts the earlier PR while looking like a
build fix. (Tracked as gc-uobe9.)

**An OPEN bead is not proof the bug is live.** The mirror of the false close, and
it costs the same wasted authoring pass. A bead names a mechanism at file:line,
those line numbers no longer resolve, and the fix turns out to have landed under
a commit whose subject says nothing about it. Before implementing any bead's fix
candidate, grep `origin/main` for a SYMBOL the fix would introduce, exactly as
you would to disprove a SHA-claiming close:

```bash
git grep -c <symbol> origin/main -- <path>
git log origin/main --oneline -S'<symbol>' -- <path>   # names the commit that landed it
```

When it already landed, the bead's remaining scope is whatever the guard does NOT
cover, and its measured exposure figure is stale by construction: re-measure it
before quoting it. (Precedent 2026-08-27: gc-j0cfh (P1) proposed guarding
`stampRunSessionIdentity` against pool slot-label work_dirs; the guard
(`workDirStampHasOwnershipEvidence`) had been on main since `e938a1906` under the
subject "close terminal workflow residue (#5026)", and the bead's "77 open"
exposure had fallen to 18. The real remaining defect was a NEW mint path added
minutes earlier in the same session's own claim-time commit.)

**A PASS verifies the artifact, never that the DECISION still holds.** The rules
above test whether code landed. This one tests whether it should. A verification
bead confirms an exact head against acceptance criteria, and every one of those
criteria is a property of the branch: tests pass, the migration is reversible, the
diff matches the design. None of them asks whether main still wants the change. So a
branch can hold a clean, correctly-executed, unexpired PASS and be wrong to publish,
because the constraint that forbids it landed on main AFTER the branch was authored.
Re-running the verification does not catch this; it re-confirms the same branch
properties against the same stale criteria. Before publishing on the strength of any
recorded PASS, diff the constraint, not the code:

```bash
git log origin/main --oneline -S'<the value or symbol the branch changes>' -- <path>
git show origin/main:<path> | sed -n '/<the setting>/,+20p'   # read main's rationale
```

If main's current text names the thing your branch does and explains why not, the
PASS is intact and the decision is dead. Decline publication and record the
superseding commit on the source bead; do not treat it as a stale branch to rebase.
(Precedent 2026-08-29, gc-wi31y/gc-igbm1: branch `bd-gc-igbm1` promoted the Beads pin
to v1.1.2 and carried two independent PASS verifications, 2026-07-27 and 2026-08-19,
with migration, canary, rollback and checksum-provenance evidence all sound.
`97e88d044` (#5147, 2026-08-09) had meanwhile written into `deps.env` that v1.1.2
predates beads#5008 and therefore lacks the `--if-assignee`/`--if-status` flags.
Merging would have promoted `BD_VERSION` to a release main documents as unusable AND
regressed `BD_CURRENT_VERSION` from the flag-capable `v1.1.1-0.20260805` back to
v1.1.2, which is the exact breakage #5147 was filed to fix. The second verification
ran ten days after the constraint landed and could not see it, because "has main's
policy moved?" was not one of its criteria.)

**A negative symbol grep is itself a claim: guard it before reporting it.** The
checks above turn on a symbol being ABSENT from `origin/main`, and a too-narrow
grep manufactures that absence. Both narrowing devices are traps: a `func <name>`
prefix misses the symbol when it is a method, a var, a struct field, or wrapped
across lines, and a `-- <path>` pathspec misses it when the code lives in a
sibling file you did not predict. Before reporting ABSENT, re-run the grep with
the bare symbol and NO pathspec:

```bash
git grep -n '<symbol>' origin/main            # no `func `, no pathspec
```

Only an absence that survives the unnarrowed form is a finding. (Precedent
2026-08-28: `git grep -n 'func updateMatchesCached' origin/main --
internal/beads/` reported ABSENT while the symbol had 3 hits on `origin/main` in
`internal/beads/caching_store_writes.go`. Had that reached a bead, it would have
justified re-authoring a guard that already exists, which is the exact cost this
whole section prevents.)

**Writing a rule into this file is not landing it: it is not durable until it is
committed.** This is the shared checkout, and several seats work in it. An
addition that sits in the working tree is loaded by every seat that reads the
file from disk, so it looks landed and behaves landed, right up until any
`git checkout AGENTS.md` or pathspec checkout silently takes it. A working-set
or handoff note claiming a lesson is "captured durably at AGENTS.md:NNN" is
therefore evidence of nothing on its own. Confirm the commit:

```bash
git status --porcelain AGENTS.md      # empty = committed
git log --oneline -1 -- AGENTS.md
```

**And a commit is not the end of the check: confirm the rule is on
`origin/main`.** A seat branch in the shared checkout can sit hundreds of
commits behind and never be published, so a rule committed only there is loaded
by seats that read this working tree and by nobody else. The commit makes it
survive a `git checkout`; only `origin/main` makes it survive the checkout being
re-pointed, and only `origin/main` reaches a seat working from a fresh clone or
a worktree cut from main:

```bash
git grep -c '<distinctive phrase from the rule>' origin/main -- AGENTS.md
```

A `0` there means the rule is unpublished work, not landed governance. Treat it
like any other unpublished branch: it belongs in the publish gate, not in a
"captured durably" claim. (Precedent 2026-08-28: four rules totalling 182 lines
of AGENTS.md, all correctly committed, all reading `0` on `origin/main` because
`pl-shared-checkout` was 362 behind and had never been pushed.)

(Precedent 2026-08-28: three separate governance rules, added across different
sessions, were all found uncommitted at once in `e815abe3a` — including the
bead-routing-count rule that a seat's working set cited by line number as
durably captured. Losing it would have restored a routed-backlog undercount of
61 against a true 299.)

## Development approach

**TDD.** Write the test first, watch it fail, make it pass. Every package
has `*_test.go` files next to the code. Integration tests that need real
infrastructure (tmux, filesystem) go in `test/` with build tags.

**The architecture docs are a reference, not a blueprint.** When the DX
conflicts with the docs, DX wins. We update the docs to match.

## Architecture

**Orchestration is the value.** A formula is a method for how a job gets
done, and the controller (`engdocs/architecture/controller.md`) runs it as a
graph — decomposing the job into beads, fanning the ready ones out to many
agents at once, gating each step on its dependencies, retrying failures, and
draining convoys in parallel, driving the work to completion outside the
user's session. The control dispatcher (`internal/dispatch`) executes control
beads — check, retry, fan-out, tally, drain, scope-check, workflow-finalize
(`docs/reference/specs/formula-spec-v2.md` sec 0) — and that dispatcher is
the engine. Orders trigger formulas on a schedule or event; health patrol
keeps the fleet alive.

**This orchestration is composed from primitives, with ZERO hardcoded
roles.** Beads are the universal persistence substrate — work survives
sessions — and every orchestration mechanism is provably composable from the
five primitives. That composability is what lets the same SDK be configured
as Gas Town, Ralph, or any other pack; no role name appears in Go. (A single
agent running a formula's steps in sequence in the user's own session —
formula v1 — is still supported as a peer shape, but graph orchestration is
why Gas City exists.)

### Code-layering view (implements the six primitives)

> **Authoritative user model:** `docs/getting-started/how-gas-city-works.md` defines the
> six primitives (**Agent** = WHO, **Bead** = WHAT, **Formula** = HOW,
> **Rig** = WHERE, **Pack** = CONFIGURES, **Event** = OBSERVE). That is the
> canonical conceptual model. The view below is the *code-layering lens*: it
> decomposes the Go substrate by layer so contributors can reason about
> imports, side-effect confinement, and the CI invariants below. It is a
> finer-grained projection of the same six primitives, not a competing
> taxonomy.

How the code substrate maps onto the six user-facing primitives:

| Code substrate (this view)                          | User-facing primitive |
| --------------------------------------------------- | --------------------- |
| Session + Prompt Templates                          | **Agent** (WHO)       |
| Task Store (Beads)                                  | **Bead** (WHAT)       |
| Formulas + Molecules + Dispatch (Sling) + Orders + Health Patrol | **Formula** (HOW) |
| Rigs (project/repo registered with the city)        | **Rig** (WHERE)       |
| Config (`pack.toml` / `city.toml`; the City is the local (root) pack — it imports shared packs) | **Pack** (CONFIGURES) |
| Event Bus                                            | **Event** (OBSERVE)   |

**Layer 0-1 substrate:**

1. **Session** — start/stop/prompt/observe sessions regardless of
   provider. Identity (via `agent.SessionNameFor`), pools, sandboxes,
   resume, crash adoption. Lifecycle is a bead-backed projection
   (`internal/session/lifecycle_projection.go`). Runtime providers
   (tmux, subprocess, exec, k8s, fake) plus routing layers (acp,
   auto, hybrid) live under `internal/runtime/` and plug in behind
   the Session surface. (Under the **Agent** primitive.)
2. **Task Store (Beads)** — CRUD + Hook + Dependencies + Labels + Query
   over work units. Everything is a bead: tasks, mail, convoy members.
   (The **Bead** primitive.)
3. **Event Bus** — append-only pub/sub log of all system activity. Two
   tiers: critical (bounded queue) and optional (fire-and-forget).
   Events are fired by activity as outbound notifications so humans and
   agents can watch; the bus is the delivery machinery. (Under the
   **Event** primitive.)
4. **Config** — TOML parsing with progressive activation (Levels 0-8 from
   section presence) and multi-layer override resolution. This is the
   machinery beneath the **Pack** primitive: `pack.toml`/`city.toml`
   declare agents, formulas, and orders, and the City is the local (root)
   pack that imports shared packs.
5. **Prompt Templates** — Go `text/template` in Markdown defining what
   each role does. The behavioral specification, supplied by a Pack and
   rendered into a running Agent.

**Layer 2-4 substrate:**

6. **Messaging** — Mail = `TaskStore.Create(bead{type:"message"})`.
   Nudge = a session-layer operation implemented via
   `runtime.Provider.Nudge()` (and exposed through
   `worker.Handle.Nudge()` at the worker boundary). No new
   primitive needed.
7. **Formulas** — a Formula is the reusable method (TOML parsed by
   Config) applied *over* a convoy of beads, looping/fanning each to an
   Agent. (When a formula runs, it materializes as a molecule — a root
   bead plus child step beads in the Task Store; wisps are the ephemeral
   variant. That materialization is a v1 implementation detail, not part
   of what a formula *is*.) Orders = formulas with gate conditions on the
   Event Bus that automate *when* a formula runs (Health Patrol is one
   kind of order). All of this is the **Formula** primitive.
8. **Dispatch (Sling)** — composed: find/spawn agent → select formula →
   materialize work as beads → hook to agent → nudge → create convoy →
   fire event. (Under the **Formula** primitive.)
9. **Health Patrol** — probe sessions (Session), compare thresholds
   (Config), publish stalls (Event Bus), restart with backoff. (One kind
   of order, under the **Formula** primitive.)

### Layering invariants

1. **No upward dependencies.** Layer N never imports Layer N+1.
2. **Beads is the universal persistence substrate** for domain state.
3. **Events are the universal outbound-notification mechanism** — fired by
   activity so humans and agents can watch; the bus is delivery machinery.
4. **Config is the universal activation mechanism.**
5. **Side effects (I/O, process spawning) are confined to Layer 0.**
6. **The controller drives all SDK infrastructure operations.**
   No SDK mechanism may require a specific user-configured agent role.

### Progressive capability model

Capabilities activate progressively via config presence.

| Level | Adds                   |
| ----- | ---------------------- |
| 0-1   | Session + tasks        |
| 2     | Task loop              |
| 3     | Multiple agents + pool |
| 4     | Messaging              |
| 5     | Formulas               |
| 6     | Health monitoring      |
| 7     | Orders                 |
| 8     | Full orchestration     |

## Architecture docs

Read **`engdocs/architecture/api-control-plane.md`** and
**`engdocs/contributors/huma-usage.md`** before touching:

- `internal/api/` (HTTP + SSE API layer)
- `cmd/gc/` (CLI) — especially anything that constructs events,
  calls `apiroute.go:apiClient()`, or uses
  `internal/api/genclient`
- `internal/events/` (event bus, registry)
- `internal/extmsg/` (external-messaging emitters)
- Anything that affects `internal/api/openapi.json`,
  `docs/reference/schema/openapi.json`, or the generated TS types under
  `internal/api/dashboardspa/web/shared/src/generated/`

Load-bearing invariants enforced by CI (violating any fails the
build; full rationale is in the architecture docs):

- **Object model at the center.** `internal/{beads, mail, convoy,
  formula, events, session, worker, sling, ...}` is the canonical
  domain. The CLI (`cmd/gc/`) and the HTTP+SSE API
  (`internal/api/`) are projections over it. Neither re-implements
  domain logic. `internal/agent/` is a small helper package
  (session-name utilities, startup hints) — not a primitive.
- **Typed wire.** No hand-written JSON on any HTTP or SSE wire
  path; no `map[string]any` or `json.RawMessage` on wire types
  (documented exceptions live in the API control-plane doc). All
  endpoints are Huma-registered; the OpenAPI spec is generated,
  never hand-written (`TestOpenAPISpecInSync`).
- **Typed events.** Every constant in `events.KnownEventTypes`
  must have a registered payload via
  `events.RegisterPayload(constant, sample)`. Use
  `events.NoPayload` for events whose envelope fields alone
  capture the semantics. Enforced by
  `TestEveryKnownEventTypeHasRegisteredPayload`.
- **Vendor-neutral hosted-service wire.** The OSS client of a hosted
  Gas City service (`internal/cliauth`, `internal/serviceproto`, the
  `gc login`/`gc whoami` commands) speaks a generic, published protocol
  (`docs/reference/specs/service-protocol-v0.md`) and holds an **opaque
  bearer** it never parses. Account/commercial policy — trial, billing,
  credit, plan, quota, org/tenant identity — must **never** be a wire
  field; it travels only in the opaque server-authored `message`/`links`
  fields the CLI prints verbatim (spec §5). Default endpoint URLs (e.g.
  `defaultServiceURL = "https://gascity.com"`) are **configuration data,
  not commercial code** — sanctioned exactly like the pack-registry
  default. Enforced by `scripts/check-core-boundary.sh` check (f) and the
  `internal/cliauth` wire golden test; all provisioning/billing/trial
  logic lives server-side in the private hosted repos.

## Active migrations

These migrations are in flight. New code on affected paths must take
the canonical route, not the legacy route.

- **Worker boundary (started `12a0a848` on Apr 17 2026, in progress).**
  `internal/worker/handle.go` is the canonical boundary for session
  creation and lifecycle operations. Production `cmd/gc/*.go` files
  must route through `worker.Handle` — enforced by
  `TestGCNonTestFilesStayOnWorkerBoundary` in
  `cmd/gc/worker_boundary_import_test.go`, which forbids non-test
  files from importing `session.NewManagerWithOptions(`,
  `worker.SessionHandle`, `sessionlog`, and similar bypass paths in
  `cmd/gc`. The remaining manager-construction/direct-create bypasses
  are split by category: `internal/api/session_manager.go` constructs
  `session.Manager` values for API handlers.
  (`internal/api/session_resolution.go`'s named-session create was
  converted to the worker boundary — it now routes through
  `worker.Handle.Create(ctx, worker.CreateModeStarted)` via
  `newResolvedWorkerSessionHandle`, no longer calling
  `mgr.CreateSession(...)` directly.) Session creation goes through the
  single `Manager.CreateSession(ctx, session.CreateOptions{...})` entry
  point (`NewManagerWithOptions` is the sole Manager constructor). This
  list is not a sessionlog read-site inventory; stream and transcript
  readers in `internal/api/` and `internal/session/` still read
  session logs directly. Package-internal helpers in `internal/session/`
  may construct and use `session.Manager`; tests may construct it
  directly. Do not add new non-test direct `session.Manager.CreateSession`
  call sites outside the worker boundary.
- **Session-first (completed `dd90ac0a` on Mar 8 2026).** The former
  Agent Protocol primitive was removed; responsibilities moved to
  `internal/session/` (lifecycle) and `internal/runtime/` (providers).
  `internal/agent/` is now a helper package with session-name utilities
  and startup hints — not a primitive. Do not reconstruct the
  `Agent` / `Handle` interfaces.

## Design decisions (settled)

These decisions are final. Do not revisit them.

- **City-as-directory model.** A city is a directory on disk containing
  `city.toml`, `.gc/` runtime state, and `rigs/` infrastructure.
- **Fresh binary, not a Gas Town fork.** We build `gc` from scratch.
- **TOML for config.** `pack.toml` (definition) and `city.toml` (deployment) are the config files.
- **Tutorials win over architecture docs.** When the docs disagree, we update the docs.
- **No premature abstraction.** Don't build interfaces until two
  implementations exist.
- **Mayor is overseer, not worker.** The mayor plans; coding agents work.
- **`internal/` packages for now.** SDK exports (`pkg/`) are future work.
  Everything is private to the `gc` binary until the API stabilizes.
- **ZERO hardcoded roles.** Roles are pure configuration. No role name
  appears in Go source code.

## Decision frameworks

- **`engdocs/contributors/primitive-test.md`** — The Primitive Test: three necessary
  conditions (Atomicity + becomes more useful as models improve + keeps
  judgment out of Go) for whether a capability belongs in the SDK vs the
  consumer layer. Apply this before adding any new primitive.
- **`engdocs/archive/backlogs/worktree-roadmap.md`** — Worktree isolation roadmap, polecat
  lifecycle analysis, and Gas Town cleanup bug lessons.
- **`engdocs/contributors/release-gate-criteria-conventions.md`** — What the
  "Tests pass" criterion in a `release-gates/*.md` file must cite. Apply this
  before signing off that criterion on any deploy gate.

## Key design principles

- **Keep judgment out of Go.** Go handles transport, not reasoning. The
  framework moves work; it doesn't reason about it. If a line of Go contains
  a judgment call, it's a violation. **The test:** does any line of Go contain
  a judgment call? An `if stuck then restart` is framework intelligence. Move
  the decision to the prompt.
- **A primitive must become more useful as models improve.** Every primitive
  should grow MORE useful as models improve, not less. Don't build heuristics
  or decision trees.
- **If you find work on your hook, you run it.** No confirmation, no waiting.
  The hook having work IS the assignment. This is rendered into agent prompts
  via templates, not enforced by Go code.
- **The system converges because work persists.** The system converges to
  correct outcomes because work (beads), hooks, and molecules are all
  persistent. Sessions come and go; the work survives. Multiple independent
  observers check the same state idempotently and converge on it. Redundancy
  is the reliability mechanism.
- **No status files — query live state.** Never write PID files, lock files,
  or state files to track running processes. Always discover state by querying
  the system directly (process table, port scans, `ps`, `lsof`). Status files
  go stale on crash and create false positives. The process table is the
  single source of truth for "what is running."
- **SDK self-sufficiency.** Every SDK infrastructure operation (gate
  evaluation, health patrol, bead lifecycle, order dispatch) must
  function with only the controller running. No SDK operation may
  depend on a specific user-configured agent role existing. The
  controller drives infrastructure; user agents execute work. Test:
  if removing a `[[agent]]` entry breaks an SDK feature, it's a
  violation.

## What Gas City does NOT contain

These are permanent exclusions, not "not yet." Each fails the test of
becoming more useful as models improve — it becomes LESS useful instead.

- **No skills system** — the model IS the skill system
- **No capability flags** — a sentence in the prompt is sufficient
- **No MCP/tool registration** — if a tool has a CLI, the agent uses it
- **No decision logic in Go** — the agent decides from prompt and reality
- **No hardcoded role names** — roles are pure configuration

## Code conventions

- Unit tests next to code: `config.go` → `config_test.go`
- `t.TempDir()` for filesystem tests
- Integration tests use `//go:build integration`
- `cobra` for CLI, `github.com/BurntSushi/toml` for config
- Atomic file writes: temp file → `os.Rename`
- No panics in library code — return errors
- Error messages include context: `fmt.Errorf("adding rig %q: %w", name, err)`
- Role names never appear in Go code. If you're writing `if role == "mayor"`,
  it's a design error.
- **Tmux safety:** Never run bare `tmux kill-server` as cleanup. Never kill the
  default tmux server. If tmux cleanup is required, target only the known
  city/test socket explicitly with `tmux -L <socket> ...`, or prefer `gc stop`
  for city shutdown. Treat personal tmux servers as out of bounds.
- **Git safety:** Never run `git checkout <ref> -- .` (or any pathspec
  checkout) in a worktree you do not own — above all the shared rig root
  (`$GC_RIG_ROOT`). Unlike `git checkout <ref>`, the pathspec form overwrites
  the index and worktree for every tracked path, moves no HEAD (so no reflog
  entry) and stages nothing (so no dangling blob): overwritten uncommitted
  work is unrecoverable. To read a file at a ref use `git show <ref>:<path>`.
  To check something out, use your own worktree or a disposable
  `git worktree add`.
- **Adding agent config fields:** When adding a field to `config.Agent`,
  also add it to `AgentPatch` and `AgentOverride`, wire it into the shared
  merge body `applyAgentMutation` (in `internal/config/patch.go`) — and, for
  the rig-override path, copy it in `AgentOverride.toAgentPatch` — and, if the
  field is a slice/map/pointer, deep-copy it in `Agent.Clone`
  (`internal/config/config.go`). All four are test-guarded, so a missed field
  fails the build: `TestAgentFieldSync` (struct field sets),
  `TestApplyAgentPatchCoversAllFields` / `TestApplyAgentOverrideCoversAllFields`
  (merge + `toAgentPatch` completeness), and `TestAgentCloneIsDeep` (clone
  deepness). Both patch and rig override share `applyAgentMutation`, and both
  the pack-load cache (`deepCopyAgents`) and pool expansion
  (`cmd/gc/pool.go` `deepCopyAgent`) share `Agent.Clone`.
- **Adding rig config fields:** When adding a field to `config.Rig`, also
  add the corresponding optional field to `RigPatch` and wire the merge
  into `applyRigPatch` so layered configs (fragments, patches) can
  override it. No field-sync test exists for Rig today; the patch path
  must be checked manually.
- **Resolving conflicts in the command-census files:** `go run
  ./cmd/gen-command-census` touches four files, but they are NOT
  interchangeable "generated artifacts." `cmd/gc/productmetrics_command_census.json`
  is the hand-maintained MANIFEST (`cmd/gen-command-census/main.go:48`
  reads it as the generator's input); `cmd/gc/metrics_census_gen.go`,
  `internal/productmetrics/command_ids_gen.go`, and
  `schemas/metrics/example/result.schema.json` are the DERIVED output.
  On a rebase/merge conflict, taking either side wholesale for the
  manifest and then regenerating is unsafe: two branches that each add
  new commands independently both consume `next_id` from their own base,
  so their generated IDs collide once merged (e.g. one side assigns ID
  202 to a new command, the other independently assigns 202 to a
  different new command). Taking `--ours`/`--theirs` blindly either
  silently drops one side's new commands or leaves colliding IDs that
  the regenerator won't itself catch. Resolve it by hand: diff each
  side's `commands` array against their common merge-base to find the
  actual new entries, keep both sets, renumber the later side's new
  entries starting from the CURRENT (post-merge) `next_id`, keep the
  array sorted by `path` (`validateSortedRows` in
  `internal/commandcensus/manifest.go` enforces this), bump `next_id`
  past the highest ID used, then run `go run ./cmd/gen-command-census`
  (no `--check`) to regenerate the three derived files from the
  corrected manifest and `go run ./cmd/gen-command-census --check` to
  confirm no drift. Any test asserting a literal generated-catalog count
  (e.g. `internal/productmetrics/event_test.go`) must have its literal
  updated to match the real post-regeneration count — get that count by
  running the test once and reading its failure message, not by hand
  arithmetic. (Landed 2026-08-18 after this exact mistake nearly shipped
  during PR #5193's rebase — a first pass took `--ours` for the manifest
  and silently dropped that PR's own new `gc worktree` commands.)

- **Resolving conflicts in the resource-census ledger files**: a second,
  unrelated generated-artifact family with the same failure mode.
  `internal/testpolicy/resourcecensus/census.go`'s `bootstrapPolicy` Go
  literal is the source of truth for test-resource-debt baselines
  (subprocess/fixed_sleep/environment call-site counts); `test/test-resources.toml`
  must mirror it exactly; `TESTING.md`'s "CHECKED TEST RESOURCE LEDGER" table is
  *generated* from `test-resources.toml` (`go test
  ./internal/testpolicy/resourcecensus -run
  TestRepositoryLedgerMatchesCensusAndDocumentation -update`). On a rebase
  conflict in any of these three, do not hand-merge the numbers from either
  side: take the current/HEAD baseline as a starting point, then run `go test
  ./internal/testpolicy/resourcecensus/... -count=1` to get the census
  self-check's live diagnostic of exactly which counters the rebased branch's
  own new code requires bumping, apply only those deltas to both `census.go`
  and `test-resources.toml` in lockstep, then regenerate `TESTING.md` via the
  `-update` flag above and re-run the check clean. (Landed 2026-08-18 while
  rebasing `fix/supervisor-adopt-dolt-port` 518 commits onto `origin/main`;
  an initial take-HEAD resolution was one increment stale because it didn't
  yet account for the branch's own rebased-in test files.)

- `TESTING.md` — testing philosophy, tier boundaries, and sharded local
  runners. Read before writing any test. For broad local sweeps, prefer the
  documented shard targets (`make test-fast-parallel`,
  `make test-cmd-gc-process-parallel`, `make test-integration-shards-parallel`,
  `make test-local-full-parallel`) over raw `go test`.

## Build Cache Conventions

**Hard ban: never run `go clean -cache`** in any script, hook, or agent session.

Running `go clean -cache` against a shared `GOCACHE` (the default when
`$GOCACHE` is not overridden) corrupts the fleet-wide build cache for every
concurrent executor. Each executor that hits a missing cache entry then runs a
full rebuild, and any that calls `go clean -cache` mid-flight invalidates all
the others' in-progress caches. The incident (vp-g96b, 2026-06-13) produced
~10 cascading cache-miss errors across the executor pool.

**Just run `go build` / `make` — do NOT set `GOCACHE` yourself.** The host `go`
shim already routes the default `GOCACHE` to a shared **on-disk** cache
(`~/.cache/go-build`) and pins compile/link temp to disk
(`GOTMPDIR=/var/tmp/gotmp`). A warm shared cache is faster and is never
corrupted by a normal build.

**Never point `GOCACHE` (or `TMPDIR`) at `/tmp`.** `/tmp` is a size-capped
RAM-backed tmpfs (61G) shared by the whole fleet — including the harness's
tool-output capture dir. A bare `mktemp -d` (no `-p` dir) resolves against the
unset `$TMPDIR`, which defaults to `/tmp` — one cold cache built there is
2-3GB, and a concurrent build wave fills tmpfs and ENOSPCs every agent
on the host (incident gm-tkz1r / ga-x9k9b9, 2026-07). The shim deliberately
does **not** relocate a `GOCACHE` you set explicitly, so an explicit `/tmp` path
defeats it.

**If you truly need an isolated cold build** (a from-scratch compile without
`go clean -cache`), put the throwaway cache **on disk** and remove it
unconditionally with a `trap`, and redirect `TMPDIR` to the same dir so the
linker's own scratch also stays off tmpfs:

```bash
tmp=$(mktemp -d -p /var/tmp) && trap 'rm -rf "$tmp"' EXIT
GOCACHE="$tmp" TMPDIR="$tmp" go build ./cmd/gc/
```

**Exception:** `go clean -testcache` is explicitly allowed. It clears only the
test-result cache, not the compiled-object cache, and does not corrupt
concurrent builds.

## Code quality gates

Before considering any task complete:

- Fast unit baseline passes (`make test`, or `make test-fast-parallel` on
  machines where sharding is useful)
- Broader process/integration coverage uses the sharded targets documented in
  `TESTING.md` instead of one monolithic `go test ./...` sweep
- `go vet ./...` clean
- `.githooks/pre-commit` is active locally (`git config core.hooksPath`
  prints `.githooks`) and has run for the staged change
- `make dashboard-ci` passes for any change touching `internal/api/`,
  `internal/api/openapi.json`, `docs/reference/schema/openapi.*`,
  `internal/api/dashboardspa/`, or generated dashboard types
- The dashboard starts locally and serves the app for dashboard/API-schema
  changes; use `npm run preview -- --host 127.0.0.1 --port <port>` from
  `internal/api/dashboardspa/web` after `make dashboard-ci`
- Every exported function has a doc comment
- No premature abstractions
- Tests cover happy path AND edge cases

## Non-Interactive Shell Commands

**ALWAYS use non-interactive flags** with file operations to avoid hanging on confirmation prompts.

Shell commands like `cp`, `mv`, and `rm` may be aliased to include `-i` (interactive) mode on some systems, causing the agent to hang indefinitely waiting for y/n input.

**Use these forms instead:**

```bash
# Force overwrite without prompting
cp -f source dest           # NOT: cp source dest
mv -f source dest           # NOT: mv source dest
rm -f file                  # NOT: rm file

# For recursive operations
rm -rf directory            # NOT: rm -r directory
cp -rf source dest          # NOT: cp -r source dest
```

**Other commands that may prompt:**

- `scp` - use `-o BatchMode=yes` for non-interactive
- `ssh` - use `-o BatchMode=yes` to fail instead of prompting
- `apt-get` - use `-y` flag
- `brew` - use `HOMEBREW_NO_AUTO_UPDATE=1` env var

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->

## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files
- For controller or session reconciler incidents, use `gc trace` and follow `engdocs/contributors/reconciler-debugging.md` for the artifact collection workflow.
- **Counting beads by routing target: metadata is FLAT, and targets have two
  spellings.** `bd list --json` returns `metadata` as flat dotted keys
  (`"gc.routed_to"`), not a nested `gc` object, so
  `b["metadata"]["gc"]["routed_to"]` silently yields nothing and the count comes
  back 0 or is quietly wrong. The same seat is also written both ways: as an
  absolute path (`/home/ds/gascity/polecat`) and short
  (`gascity/polecat`). And `gc.routed_to` is a different field from
  `gc.execution_routed_to`. Sum every spelling of the field you mean, and state
  which field you counted. (2026-08-27: a routed-backlog figure was reported as
  61 when the real number was 299, because one spelling of one field was
  counted.)
- **Counting the target you expected is not counting the exposure: enumerate
  every target, then check each one exists.** The rule above fixes the count for
  a seat you already suspect. It does not tell you the queue is also addressed to
  seats you never thought to ask about, and a count scoped to the known-bad
  target reads as complete while understating the real number. Print the full
  distribution instead of grepping for one value, then check the roster:

```bash
bd list --status open --json | python3 -c 'import sys,json,collections; \
rows=json.load(sys.stdin); c=collections.Counter(); \
[c.update([(b.get("metadata") or {}).get("gc.routed_to")]) for b in rows]; \
print(c.most_common())'
gc agent list                             # read EVERY row; do not filter it
```

  **Never filter the roster to check a target, and never conclude a seat is a
  phantom.** The roster prints a seat under either spelling, absolute
  (`/home/ds/gascity/polecat`) or short (`gascity/codex-w1h`), and which one it
  uses is not yours to predict. Any filter narrow enough to be convenient
  MANUFACTURES the absence you are testing for, exactly as a `-- <path>` pathspec
  does to a symbol grep. So read every row and match each target against the
  whole list. A target that looks missing is almost always present under the
  other spelling, and the distinction that actually matters is not
  exists-vs-phantom but **active vs suspended**: a suspended seat strands work
  identically while being fixable by resuming it rather than by minting anything.
  Read the ROSTER column, not the presence of the row.

```bash
gc agent list | sed 's/^ *//' | awk 'NF>=2{print $1"\t"$2}'   # target -> state
```

  Note what this does and does not prove: `gc agent list` is a roster read, not a
  liveness probe. `active` means not suspended, never that the seat's dispatcher
  is draining. Open work parked under an active target is unexplained, not fine.

  (Precedent 2026-08-28, gc-hpidi, and this is the rule correcting its own earlier
  text. The stranded queue was escalated to the mayor as ~397 beads routed to four
  targets "that do not exist", with a proposal to mint a new worker seat and
  re-point all four. All four exist. The roster had been checked with
  `gc agent list | grep "<rig-root>/"` -- which this rule itself used to
  prescribe -- and that grep matches only absolute-path rows, so all 17
  short-name `gascity/` rows were invisible. Corrected distribution: polecat 380
  across three spellings of ONE seat and `gascity/codex` 89, both SUSPENDED; 18
  more under `codex-w2i`/`codex-w1h`, read at the time as ACTIVE seats and
  therefore not stranded. That last reading was itself wrong; see the next rule.
  The remedy for the two genuinely switched-off seats is resuming them, which is
  reversible and mayor-tier, not a topology change needing a human.)
- **A roster row is not a seat: `gc agent list` prints PROVIDERS in the same
  shape.** The rule above makes you read every row rather than grep for one. Do
  that and you hit the opposite error, which is how it just failed: the roster
  renders each configured provider per rig as a `<rig>/<provider>` row,
  character-identical to a real agent row, and a provider row always reports
  `active` because the provider is enabled. It can never claim a bead. The tell
  is that the SAME names repeat under every rig (`gascity/codex-w2i`,
  `gascity-dashboard/codex-w2i`, `gascity-packs/codex-w2i`), because they are one
  provider list rendered three times. Before concluding a seat exists, resolve the
  name against the config, not the roster:

```bash
python3 -c "import tomllib;d=tomllib.load(open('city.toml','rb'));print(sorted(d['providers']))"
grep -n 'name = "<target>"' city.toml     # a real seat has an [[patches.agent]] block
```

  A name in the first list is a provider and routing work to it strands that work
  permanently, with no seat to resume and nothing to nudge. (Precedent 2026-08-29,
  gc-hpidi: this seat read the unfiltered roster, found six `gascity/codex-*` rows
  reading ACTIVE, and posted a correction asserting the rig had six live
  implementation seats and that minting a worker would "add a row without adding a
  drain." All six are providers. The bead's original premise, ONE seat
  `/home/ds/gascity/polecat` and it is suspended, was right all along. The
  retraction is on the bead. Note this also corrects the precedent paragraph
  immediately above, which had recorded `codex-w2i`/`codex-w1h` as active seats.)
- **P0 is falsy: a `priority or N` default silently drops the band you care most
  about.** The counting rules above fix WHERE work is addressed and WHAT is in a
  band. This one fixes the filter itself. The idiomatic-looking Python guard
  `(b.get("priority") or 9) < 2`, written to tolerate a missing field, evaluates
  `0 or 9` to `9` and therefore excludes every P0 bead, while a P1 passes
  normally. The failure is invisible in both directions: the selection under-acts
  on exactly the highest-priority rows, and the verification listing built with
  the same filter reports the P0 band as clean. Write the membership test
  explicitly, and never let a falsy-default stand in for a presence check:

```python
p = b.get("priority")
if p in (0, 1):          # not: (b.get("priority") or 9) < 2
    ...
```

  (Precedent 2026-08-28, during the formula-exhaust sweep: the filter excluded 8
  P0 `gc.outcome=fail` beads from a demotion pass, and the confirming listing
  then printed a P0-free band. Caught only because two P0s known by name were
  missing from the output; a sweep that trusted its own listing would have
  reported the band cleared with six inert P0 steps still polluting claim order.)
- **A priority band is not a work count: formula step beads inherit the root's
  priority.** The two rules above fix WHERE work is addressed. This one fixes WHAT
  is in the band. A molecule materializes as a root plus child step beads, and the
  steps carry the root's priority, so a stalled run parks its whole ladder in the
  P0 band. The scheduler sorts priority-first, so those inert steps outrank real
  P0 engineering work in claim order, and the band stops being readable. Before
  quoting a P0/P1 count or picking off the top of it, separate the steps from the
  work:

```bash
bd list --status open --json | python3 -c 'import sys,json; \
[print(b["id"], b.get("title","")[:70]) for b in json.load(sys.stdin) \
 if b.get("priority")==0]'
bd mol current --json        # no live molecule => every step below is inert
```

  Step beads announce themselves in the title (`Stage N -- ...`, `input convoy
  for <id>`, `Resolve branch and prepare report path`, a bare formula name). Check
  their `Updated` date: a step untouched for weeks belongs to an abandoned run.
  Do NOT bulk-close them from a counting pass. Close per root, leaf-first, through
  the typed closer, and never force-close a root that is still live (that orphans
  its steps). (Precedent 2026-08-28, gc-zqtk2: of 31 unrouted P0s on the gascity
  rig, 12 were dead formula steps from two abandoned `mol-pr-ship` runs, stale 8
  to 23 days, with no live molecule driving them. The real unrouted P0
  engineering count was 11, not 31.)

  **And not every step bead is closable: a convoy record outlives its run.** The
  paragraph above sorts steps by whether the RUN is abandoned, which is the right
  question for a `Stage N` bead and the wrong one for an `input convoy for <id>`.
  A convoy record points at the bead it tracks, and that bead can be live work
  while the molecule around it is long dead. Closing it strips structure the work
  needs when it resumes, and the counting pass gives you no hint, because it is
  stale and P0 exactly like the steps beside it. So resolve each convoy by its
  TRACKED root, not by the run:

```bash
bd dep tree <convoy-id> | head -5        # names the tracked root
bd show <root-id> --json | python3 -c 'import sys,json; d=json.load(sys.stdin); \
d=d[0] if isinstance(d,list) else d; print(d.get("status"), d.get("priority"))'
```

  Root closed or abandoned => close the convoy with the steps. Root open or
  blocked => do NOT close it; demote it out of the band instead (`bd update <id>
  --priority 2`) and record why on the bead. Demotion fixes the actual harm, which
  is claim-order pollution, and is reversible when the root is picked up.
  (Precedent 2026-08-28, gc-zqtk2: 15 beads matched the counting pass, not 12. Of
  those, 11 were closable steps across two dead runs; the other 4 were input
  convoys tracking `gc-iipgc` (blocked), `gc-4wnvo` and `gc-dzhyp` (both open and
  live). Closing all 15 leaf-first, which the rule as previously written reads as
  authorising, would have stripped convoy structure from three live P0s.)
- **Do not sling `mol-focus-review`; it does not complete.** Stephanie's ruling of
  2026-08-25 stands: while the formula is broken, implementation work gets a manual
  `git worktree add` plus the ship skills, not a focus-review molecule. The failure is
  silent, which is why this needs writing down: the run materializes its full step
  ladder, claims a target bead, and then stops, leaving nine inert beads that inherit
  the target's priority and pollute the band (see the priority-band rule above). Three
  such runs were found abandoned on 2026-08-28 (roots `gc-dcmbx`, `gc-yhrcx`,
  `gc-5xr92`, stale 4 to 6 days, `bd mol current` empty), holding 15 of the 16 open
  beads routed to `gascity/codex-w2i` and making an active seat look like a stranded
  queue. Resolve an abandoned run by its TRACKED TARGET, never by the run: close the
  steps leaf-first through the typed closer, and leave the target bead open and
  re-dispatchable unless it closed on its own merits. The ruling is scoped to the
  formula being broken (tracked in the ds-research store as `dr-snk0q` / `dec-tqgb`,
  not visible from this rig's `gc-` store); confirm there before treating it as lifted.

- When a bead needs to pause on a specific actor or condition, only `hold:mayor` and `hold:external` are canonical (set via `bd set-state <id> hold=mayor|external --reason "..."`) — never invent a new ad hoc hold/blocked label. See `engdocs/contributors/hold-label-conventions.md`.

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   git push
   git status  # MUST show "up to date with origin"
   ```
   NOTE: gascity Dolt is LOCAL-ONLY (no remote). Do NOT run `bd dolt push`,
   `bd dolt pull`, or `bd dolt remote add` here -- they fail and re-introduce
   a doomed `origin` remote (ga-9wsri). Use `git push` only.
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**

- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->

## Architecture Best Practices

These apply to all code in this project — frontend and server:

- **TDD (Test-Driven Development)** - write the tests first; the implementation
  code isn't done until the tests pass.
- **Consider First Principles** to assess your current architecture against the
  one you'd use if you started over from scratch.
- **Leverage Types** using statically typed languages (TypeScript, Rust, etc) so
  that we can leverage the power of the compiler as guardrails and immediate
  feedback on our code at build-time instead of waiting until run-time.
- **DRY (Don't Repeat Yourself)** – eliminate duplicated logic by extracting
  shared utilities and modules.
- **Separation of Concerns** – each module should handle one distinct
  responsibility.
- **Single Responsibility Principle (SRP)** – every class/module/function/file
  should have exactly one reason to change.
- **Clear Abstractions & Contracts** – expose intent through small, stable
  interfaces and hide implementation details.
- **Low Coupling, High Cohesion** – keep modules self-contained, minimize
  cross-dependencies.
- **Scalability & Statelessness** – design components to scale horizontally and
  prefer stateless services when possible.
- **Observability & Testability** – build in logging, metrics, tracing, and
  ensure components can be unit/integration tested.
- **KISS (Keep It Simple, Sir)** - keep solutions as simple as possible.
- **YAGNI (You're Not Gonna Need It)** – avoid speculative complexity or
  over-engineering.
- **Don't Swallow Errors** by catching exceptions, silently filling in required
  but missing values, masking deserialization with nulls or empty lists, or
  ignoring timeouts when something hangs. All of those are errors (client-side
  and server-side) and must be tracked in a centralized log so it can be used to
  improve the app over time. Also, inform the user as appropriate so that they
  can take necessary action.
- **No Placeholder Code** - we're building production code here, not toys.
- **No Comments for Removed Functionality** - the source is not the place to
  keep history of what's changed; it's the place to implement the current
  requirements only.
- **Layered Architecture** - organize code into clear tiers where each layer
  depends only on the one(s) below it, keeping logic cleanly separated.
- **Use Non-Nullable Variables** when possible; use nullability only when there
  is NO other possibility.
- **Use Async Notifications** when possible over inefficient polling.
- **Eliminate Race Conditions** that might cause dropped or corrupted data
- **Write for Maintainability** so that the code is clear and readable and easy
  to maintain by future developers.
- **Arrange Project Idiomatically** for the language and framework being used,
  including recommended lints, static analysis tools, folder structure and
  gitignore entries.
- **Keep Serialization/Deserialization At The Edges** to make full use of
  type-safe objects in the app itself and to centralize error handling for
  type-system translation. Do NOT allow untyped data with known shapes to flow
  through the system and subvert the type system.
- **Prefer Well-Known, High Quality OSS Libraries** instead of hand-rolling your
  own behavior to get more robust, better maintained and better tested results.
- **Treat Static Warnings And Info As Errors To Be Fixed**. The whole point of
  static checking (linting, compilers, etc) is that they surface issues at
  build-time so that they can be fixed now instead of lead to errors at runtime.
  Take advantage of that feedback to fix those errors!
- **Use Centralized Semantic Constant Values** using enums and constants instead
  of spreading magic numbers throughout the code.
