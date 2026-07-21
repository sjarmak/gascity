---
name: gc-test-authoring
description: >
  How to WRITE tests for Gas City (gastownhall/gascity): choosing among the
  five test kinds (unit, testscript, integration, docsync, coordination),
  hand-written fakes and conformance suites, the exec-spy pattern, tmux
  session safety, and host-environment hermeticity. Load this when adding or
  modifying any *_test.go file, a .txtar testscript, a conformance suite, a
  fake/test double, or when a test passes locally but fails in CI or inside
  an agent worktree. NOT for running/sharding the existing suite (that is
  gc-build-verify) or for debugging production controller behavior (that is
  gc-debugging).
---

# gc-test-authoring — writing tests that survive Gas City's harness

**Tier: 1 (single-session; no subagents, no worktrees; safe under
`DISABLE_INTERACTIVITY=1`).** _(Tier taxonomy is a departure-library
convention, provisional pending maintainer sign-off.)_

`TESTING.md` at the repo root is the constitution for testing and the single
home for the testing doctrine. Read it before writing any test. This skill
does not restate it; it adds the routing decisions, the verified entry-point
names, the hermeticity rules that recur as bug classes, and the places where
`TESTING.md` has drifted from the code (dated notes below).

## When NOT to use this skill

| You want to…                                                                                          | Use instead                                                                                                                |
| ----------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| Run or shard the existing suite, map local targets to CI jobs, understand `make test` vs `make check` | sibling skill `gc-build-verify` (departure library; if absent, `TESTING.md` "Sharded local runners" + `Makefile` comments) |
| Debug a failing controller/reconciler at runtime                                                      | sibling skill `gc-debugging`; `engdocs/contributors/reconciler-debugging.md`                                               |
| Fix generated-artifact drift (OpenAPI, TS types, docs schema)                                         | sibling skill `gc-generated-artifacts`; `engdocs/contributors/huma-usage.md`                                               |
| Add a typed event + payload                                                                           | sibling skill `gc-events-payloads`; AGENTS.md "Typed events"                                                               |

Sibling `gc-*` skills are being authored in the same departure wave and may
not all exist yet; the fallback docs above always do.

## Jargon (defined once)

- **bead** — a persistent work unit in the task store (`internal/beads`);
  everything (tasks, mail, molecules) is a bead.
- **city / rig** — a city is a directory with `city.toml` + `.gc/` runtime
  state; rigs are project repos registered inside it.
- **testscript / txtar** — CLI-level tests using
  `github.com/rogpeppe/go-internal/testscript`; each `.txtar` file under
  `cmd/gc/testdata/` runs the real `gc` binary and asserts on output.
- **conformance suite** — a shared behavioral test suite for a provider
  interface, run against every implementation of that interface.
- **coordination test** — a test that verifies components are _called in the
  right order_ (wiring), not that each component is correct.
- **fake / spy** — hand-written test double; a spy additionally records the
  calls made to it.
- **exec-spy** — a shell script substituted for an external binary (via
  `GC_BEADS=exec:<script>`) that logs every invocation to a file.
- **hermetic** — the test depends only on state it created itself; nothing
  from the host machine (env vars, ~/.gc, default tmux server, port 3307)
  can change its outcome.

## Step 1 — Route the test to the right kind

Five kinds exist (`TESTING.md` sections 1–5). Decision table, condensed:

| The thing under test                                           | Kind         | Lives at                                        |
| -------------------------------------------------------------- | ------------ | ----------------------------------------------- |
| Internal behavior, edge cases, injected failures, corrupt data | Unit         | `*_test.go` next to the code, same package      |
| CLI output, exit codes, user-facing errors, tutorial flows     | Testscript   | `cmd/gc/testdata/*.txtar`                       |
| Real tmux / real `gc` binary / real dolt fitting together      | Integration  | `test/…` with `//go:build integration`          |
| Docs ↔ code sync (tutorial coverage, links, Mintlify nav)      | Docsync      | `test/docsync` (run: `make check-docs`)         |
| Call _ordering_ across components (lifecycle wiring)           | Coordination | `cmd/gc/lifecycle_coordination_test.go` pattern |

Tie-breakers that recur in review:

- **The env-var rule:** if a testscript scenario needs more than two env vars
  to set up, it is a unit test, not a testscript (`TESTING.md`).
- **Conformance vs coordination:** "does the store handle corrupt JSONL?" →
  conformance/unit. "does `gc start` call ensure-ready before init?" →
  coordination. Do not re-verify conformance-covered contracts inside
  coordination tests (the overtesting line, `TESTING.md`).
- **Contract vs edge case:** externally visible behavior that only exists
  with the real supervisor running (async request results, SSE, OpenAPI
  agreement) → supervisor API contract tests in `test/integration/`. Parser
  failures and single-handler error branches → unit tests next to the code.

## Step 2 — Follow the kind-specific authoring rules

### Unit tests

- Same package as the code (access to unexported functions); `t.TempDir()`
  for all filesystem state; testify `require` for preconditions, `assert`
  for checks. No env vars to control behavior — inject dependencies.
- Use the **`do*()` split**: `cmdFoo()` wires real deps, `doFoo()` takes all
  deps as arguments and returns an exit code. Unit tests call `doFoo()` with
  fakes directly (`TESTING.md` "The do*() function pattern").
- Slow process-backed `cmd/gc` cases must self-gate with
  `skipSlowCmdGCTest(t, reason)` (`cmd/gc/fast_loop_helpers_test.go:15`),
  which skips unless `GC_FAST_UNIT=0`. Without the gate your test silently
  bloats the fast loop that `make test` and pre-push hooks run.
- When the behavior under test is _argument construction_ for a subprocess
  (flag lists, socket flags), extract an executor interface and assert on
  the captured `[]string` args — see `tmux.executor` / `fakeExecutor`
  (`TESTING.md` "The executor interface pattern").

### Testscript (.txtar)

- Location: `cmd/gc/testdata/` (32 files as of 2026-07-06); runner is
  `cmd/gc/main_test.go`, which defaults missing backends to fakes:
  `GC_SESSION=fake`, `GC_BEADS=file`, `GC_DOLT=skip`
  (`cmd/gc/main_test.go:41-43`). Omitting the env vars means "use fakes",
  never "use real tmux".
- Fake modes are capped at three per dependency: works (`fake`), fails
  (`fail`), real (`tmux`/`bd`). Do not invent a fourth mode; if you need
  one, the scenario belongs in a unit test.
- Syntax: `!` prefix = command must fail; `stdout`/`stderr` assert output;
  `-- filename --` blocks create fixtures. Example on `TESTING.md` §2.

### Integration tests

- Tag the file `//go:build integration`. Broader tag tiers also exist:
  `acceptance_a` / `acceptance_b` / `acceptance_c` (live inference) and
  `chaos_dolt` (opt-in managed-Dolt chaos, `make test-chaos-dolt`,
  Makefile:422-428). Untagged `go test ./...` silently skips all of them.
- **Tmux safety is non-negotiable** (AGENTS.md "Code conventions" owns the
  rule): never bare `tmux kill-server`, never touch the default server. Use
  `test/tmuxtest`:
  - `RequireTmux(t)` — skip when tmux is absent (guard.go:31).
  - `guard := tmuxtest.NewGuard(t)` — generates a unique `gctest-…` city
    name, uses a **per-city isolated tmux socket**, and registers cleanup
    (guard.go:49-78).
  - `tmuxtest.KillAllTestSessions(t)` in `TestMain` pre- and post-sweep for
    orphaned `gctest-*` sockets (guard.go:121).
- Keep supervisor API contract tests hermetic: isolated `GC_HOME`, own
  runtime dir, own ports, self-provisioned fixtures, mutations through HTTP
  only. Full rule list: `TESTING.md` §3.

### Docsync

- Adding a tutorial command, moving a docs page, or renaming an anchor?
  `make check-docs` (runs `go test ./test/docsync`, Makefile:443-445) must
  pass in the same change.

### Coordination tests

- Pattern: substitute the external `bd` binary with a spy script via
  `t.Setenv("GC_BEADS", "exec:"+script)`. The spy appends `"$@"` to a log
  file; the test asserts on operation ordering with helpers like
  `assertOpSubsequence`. Reference implementation: `writeSpyScript`
  (`cmd/gc/lifecycle_coordination_test.go:18`) and
  `TestLifecycleCoordination_InitRigAddStart`
  (`cmd/gc/lifecycle_coordination_test.go:130`), which proves init ordering
  and hook survival across `gc init` → `gc rig add` → `gc start` without a
  Dolt server anywhere.

## Step 3 — Fakes and conformance (no mock libraries, ever)

No `gomock`, no `mockgen`. Every double is a hand-written exported type in
the same package as the interface it implements, with a compile-time check
(`var _ Provider = (*Fake)(nil)`).

Error injection, current API (verified 2026-07-06):

- `runtime.Fake` (`internal/runtime/fake.go`): per-session error maps
  (`StartErrors`, `StopErrors`, `WaitForIdleErrors`, …) plus
  `runtime.NewFailFake()` for the all-operations-fail mode (fake.go:92).
- `fsys.Fake` (`internal/fsys/fake.go`): per-path `Errors` map.
- `beads.MemStore` is a _real_ `Store` implementation backed by a slice,
  not a test-only fake; `FileStore` composes it.

**Drift note (2026-07-06):** `TESTING.md` still shows `f.Broken = true` for
`runtime.Fake` and shows `guard.SessionName("mayor")` returning
`"gc-gctest-a1b2c3d4-mayor"`. Both are stale: `broken` is now unexported
(set only via `NewFailFake()`, fake.go:16-24), and `Guard.SessionName`
returns just the sanitized agent name because per-city socket isolation made
the prefix unnecessary (guard.go:90-95). Follow the code, and fix
`TESTING.md` through normal change-control when you next touch it.

### Conformance suite entry points

`TESTING.md` names the suites; these are the actual functions to call
(verified 2026-07-06):

| Interface          | Package                        | Entry points                                                                                        |
| ------------------ | ------------------------------ | --------------------------------------------------------------------------------------------------- |
| `beads.Store`      | `internal/beads/beadstest`     | `RunStoreTests`, `RunMetadataTests`, `RunSequentialIDTests`, `RunCreationOrderTests`, `RunDepTests` |
| `runtime.Provider` | `internal/runtime/runtimetest` | `RunProviderTests`, `RunLifecycleTests`, `RunSessionTests`                                          |
| `mail.Provider`    | `internal/mail/mailtest`       | `RunProviderTests`                                                                                  |
| `events.Recorder`  | `internal/events/eventstest`   | `RunProviderTests`, `RunConcurrencyTests`                                                           |

Usage shape (real call site, `internal/beads/filestore_test.go:88-92`):

```go
beadstest.RunStoreTests(t, factory)
beadstest.RunSequentialIDTests(t, factory)
beadstest.RunCreationOrderTests(t, factory)
beadstest.RunDepTests(t, factory)
beadstest.RunMetadataTests(t, factory)
```

**New provider checklist** (from `TESTING.md` "Provider seam inventory"):

- [ ] Run the full conformance suite against it (mandatory).
- [ ] If it has lifecycle dependencies (startup/shutdown ordering), add a
      coordination test using the exec-spy pattern.
- [ ] Update the seam inventory table in `TESTING.md`.
- [ ] Compile-time interface assertion for any new fake.

## Step 4 — Hermeticity: the recurring rot class

Test-isolation failures are a repeat offender in this repo's history. Tests
here run _inside_ live gc cities and agent worktrees, where `GC_*` /
`DOLT_*` / `BEADS_*` env vars are always set. A test that inherits the host
environment is green on a clean shell and red (or worse, **mutates the
developer's real city**) everywhere else.

Rules, all verified against current code:

1. **`make test` already scrubs.** Every make test target runs under
   `TEST_ENV`, an `env -i` allowlist (Makefile:185-221, rationale at 175-179,
   introduced by PR #746). Shell exports are invisible to tests; to pass one
   through deliberately: `EXTRA_TEST_ENV='FOO=bar' make test`.
2. **Raw `go test` does NOT scrub.** If you run a package directly while
   inside a city, your host `GC_*` vars leak in. Prefer the make targets for
   anything beyond a single focused test.
3. **Never hand `os.Environ()` raw to an `exec.Cmd`** that runs `gc`, the
   `gc-beads-bd` lifecycle script, or anything dolt-adjacent. Use
   `sanitizedBaseEnv(extra...)` (`cmd/gc/fast_loop_helpers_test.go:32`),
   which strips every `GC_*`/`BEADS_*` entry first. It exists because an
   inherited `GC_CITY_RUNTIME_DIR`/`GC_DOLT_STATE_FILE` pointed test
   subprocesses at the user's real registered city and silently overwrote
   its state on every run (regression guard for gastownhall/gascity#938).
4. **Test helpers that build filtered environments must strip by prefix,
   not by known key.** New `GC_*` vars appear constantly; an explicit-key
   denylist rots. See the worked example.
5. **No status files.** Tests must not assert on PID/status files — the
   doctrine is query-live-state (AGENTS.md "No status files"). Assert via
   the process table, the API, or the op log.

### Worked example: commit `273f6c3ab` (2026-06-25, PR #3747)

Two suites — `examples/bd/dolt` health tests and the k8s beads-script test —
passed on clean dev shells but failed inside cities, CI, and agent
worktrees. Root cause: their env filtering was allowlist-of-explicit-keys,
so ambient `GC_*`/`DOLT_*` leaked through and `runtime.sh` resolved the
host's real dolt state file and port 3307 instead of the test's hermetic
temp fixture.

The fix (files: `examples/bd/dolt/health_test.go`,
`internal/runtime/k8s/testenv_helpers_test.go`,
`internal/runtime/k8s/beads_script_test.go`):

- `filteredEnv` now strips every `GC_*` and `DOLT_*` entry unconditionally;
  callers must pass any intentionally inherited var explicitly.
- A meta-regression test, `TestFilteredEnvStripsGCAndDOLTPrefixes`, fails on
  a _clean_ machine if the prefix scrub is ever reverted — the guard does
  not depend on the dirty environment that exposed the bug.
- `GC_STORE_ROOT` was added to the k8s `clearDoltAndCityEnv` helper so an
  inherited value cannot defeat an "unset fallback" assertion.

The transferable lessons: (a) scrub by prefix; (b) when you fix an isolation
leak, add a regression test that detects the _absence of the scrub_, not the
presence of the leak; (c) verify the fix both with the vars leaked and with
them unset (the PR's test plan did exactly that). Note the fix and its tests
landed in one commit — tests ship with fixes here.

## Pre-review checklist for any test change

- [ ] Right kind per the routing table (env-var rule respected).
- [ ] Slow `cmd/gc` process tests gated with `skipSlowCmdGCTest`.
- [ ] No raw `os.Environ()` into gc/bd/dolt subprocesses.
- [ ] tmux only via `tmuxtest.Guard` / isolated sockets; no bare
      `kill-server`.
- [ ] New provider implementation → conformance suite wired in.
- [ ] Coordination tests assert ordering only, not component correctness.
- [ ] `make test` (fast loop) and, if you touched process-backed paths,
      `make test-cmd-gc-process` pass locally.
- [ ] Docs touched → `make check-docs` passes.
- [ ] Test lands in the same commit as the change it covers.

## Provenance and maintenance

Written 2026-07-06 by the retiring-fellow distillation campaign; grounded in
`TESTING.md`, `AGENTS.md`, `Makefile`, and the cited files/commits. Two
baselines, stated explicitly: entry points and Makefile line numbers (e.g.
TEST_ENV at Makefile:185-221) are from the checked-out tree at `58e0b8dbb`
(branch `_pr1945_check`, 2026-05-11); on `origin/main` at `f828bbe4b` the
TEST_ENV block sits near Makefile:266. The hermeticity worked example
(commit `273f6c3ab`, 2026-06-25, `examples/bd/dolt/health_test.go`,
`TestFilteredEnvStripsGCAndDOLTPrefixes`) exists only on `origin/main`, not
in that checkout — read it via `git show origin/main:<path>`.
Placement (fork-local, repo-portable) is
**provisional** per the 2026-07-07 morning ledger pending the maintainer's
answer to discovery Q1. Machine-local inputs (discovery report, skill-tier
doc under /home/ds/gas-city/docs/) informed structure only; no fact above
depends on them.

Re-verification one-liners for everything volatile:

```bash
# Five kinds + doctrine still as summarized:
sed -n '1,120p' TESTING.md
# TEST_ENV allowlist + EXTRA_TEST_ENV still at these lines:
grep -n "TEST_ENV = env -i" -A 5 Makefile
# Fast-loop gate helper still exists:
grep -n "func skipSlowCmdGCTest" cmd/gc/fast_loop_helpers_test.go
# sanitizedBaseEnv + #938 guard still exists:
grep -n "func sanitizedBaseEnv" cmd/gc/fast_loop_helpers_test.go
# Testscript fake defaults:
grep -n "setTestscriptEnvDefault" cmd/gc/main_test.go
# Conformance entry points unchanged:
grep -n "^func Run" internal/beads/beadstest/conformance.go internal/runtime/runtimetest/conformance.go internal/mail/mailtest/conformance.go internal/events/eventstest/conformance.go
# Fake error-injection API (drift note above still accurate?):
grep -n "NewFailFake\|Broken" internal/runtime/fake.go
# Guard API (drift note above still accurate?):
grep -n "func (g \*Guard) SessionName" -A 3 test/tmuxtest/guard.go
# Chaos/docsync targets:
grep -n "^test-chaos-dolt:\|^check-docs:" Makefile
```

If any command's output no longer matches this skill, update the skill in
the same change — one home per fact; `TESTING.md` owns doctrine, this skill
owns routing, entry points, and the hermeticity rot class.
