---
name: gc-build-verify
description: >
  How to BUILD and VERIFY changes in Gas City (gastownhall/gascity): the Make
  target map, what `make check` and `make test` actually run (and what they
  silently skip), GC_FAST_UNIT routing, build-tag-gated test tiers, the
  env-scrubbing harness (TEST_ENV / internal/testenv), the git hooks
  (pre-commit regenerates artifacts, pre-push runs tests), sharded local
  runners, and how local targets map onto CI jobs and the `ci-required` gate.
  Load this before running any build/test sweep in this repo, when "make check
  passed but CI failed", when deciding which target proves a change, or when a
  test suite behaves differently locally vs CI. NOT for writing new tests
  (sibling skill gc-test-authoring), fixing generated-artifact drift
  (gc-generated-artifacts), release/RC/nightly operations (gc-release-ci-ops),
  or debugging live controller behavior (gc-debugging).
---

# gc-build-verify — proving a Gas City change actually passes

**Tier: 1 (single-session; no subagents, no worktrees; safe under
`DISABLE_INTERACTIVITY=1`).** _(Tier taxonomy is a departure-library
convention, provisional pending maintainer sign-off.)_

Everything below was verified against `origin/main` at commit `f828bbe4b`
(2026-07-06) by reading the Makefile, `.githooks/`, `.github/workflows/ci.yml`,
`TESTING.md`, `AGENTS.md`, and the scripts they invoke. Line numbers cite that
commit. Where older prose in the repo has drifted from the Makefile, the
Makefile wins; drift notes below are dated.

**First check, every session:** agent worktrees in this repo often sit on
stale scratch branches (weeks behind main; target names and paths move fast
here). Before trusting any target name in this skill or in your checkout:

```bash
git fetch origin
git log -1 --format='%h %ci %s' origin/main   # is your base recent?
git status -sb                                 # are you on a scratch branch?
```

If your worktree base predates 2026-07 by much, read build files from main
directly (`git show origin/main:Makefile`) rather than trusting the checkout.

## When NOT to use this skill

| You want to…                                                  | Use instead                                                                  |
| ------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| Write or restructure a test (choose tier, fakes, hermeticity) | sibling skill `gc-test-authoring`; `TESTING.md`                              |
| Fix OpenAPI / TS-type / schema-doc drift the gates caught     | sibling skill `gc-generated-artifacts`; `engdocs/contributors/huma-usage.md` |
| Cut a release, run the RC gate, nightly tiers, Trivy waivers  | sibling skill `gc-release-ci-ops`; `RELEASING.md`                            |
| Debug a live controller / reconciler / session incident       | sibling skill `gc-debugging`; `engdocs/contributors/reconciler-debugging.md` |

Sibling `gc-*` skills are authored in the same departure wave and may not all
exist yet; the fallback docs above always do.

## Jargon (defined once)

- **gate** — a check that must pass before a change is considered done
  (formatter, linter, test target, drift check).
- **build tag** — a Go compile-time tag (`//go:build integration`). Tagged
  test files are invisible to `go test` unless you pass `-tags <tag>`; they
  do not fail, they simply never compile in.
- **GC_FAST_UNIT** — env var read by test skip-helpers in `cmd/gc`. `1` (or
  unset) skips slow process-backed scenarios; only the exact value `0` runs
  them (`cmd/gc/fast_loop_helpers_test.go:15-23`).
- **TEST_ENV** — the Makefile's `env -i` allowlist wrapper around `go test`.
  Your shell exports do not reach tests unless allowlisted (Makefile:248-307).
- **shard** — one slice of a big test package run as its own process, so a
  25-minute package becomes N parallel short jobs.
- **drift gate** — a CI job that regenerates a committed artifact and fails
  if the regeneration differs from what you committed (`spec-ci`,
  `dashboard-ci`, `check-schema`).
- **`ci-required`** — the single fan-in CI job that branch protection watches;
  it aggregates every other job's result (ci.yml:1337).
- **hook** — a git hook from `.githooks/` (activated by `make setup`), not a
  bead hook. This skill only means the git kind.
- **bd / Dolt** — `bd` is the beads (issue-tracker) CLI; Dolt is the SQL
  server that backs it. Several test tiers need both installed.

## 1. One-time setup

```bash
make setup                        # installs golangci-lint + oapi-codegen, activates .githooks/
git config core.hooksPath         # MUST print: .githooks
```

`AGENTS.md` ("Code quality gates") makes the active pre-commit hook itself a
quality gate: work is not done unless the hook ran for the staged change.

Tool pins (as of 2026-07-06): golangci-lint 2.9.0 (Makefile:1), oapi-codegen
v2.6.0 (Makefile:631). Dolt/bd pins live in `deps.env` (Dolt 2.1.7, bd
v1.1.0) and are guard-tested; see section 7.

## 2. The everyday loop

```bash
make build                        # go build ./cmd/gc → bin/gc (signs on macOS)
make check                        # fmt-check + lint + vet + check-routed-test-rows + test
```

That is the contributor contract from `CONTRIBUTING.md` ("make build && make
check"). What `make check` expands to (Makefile:112):

| Step                     | What it runs                                                                   |
| ------------------------ | ------------------------------------------------------------------------------ |
| `fmt-check`              | `golangci-lint fmt --diff ./...` (fails if formatting would change files)      |
| `lint`                   | full-repo `golangci-lint run ./...`                                            |
| `vet`                    | `go vet ./...`                                                                 |
| `check-routed-test-rows` | `scripts/check-routed-test-rows.sh` (six-row matrix on routed read-path tests) |
| `test`                   | the fast unit loop, see below                                                  |

`make test` (Makefile:317-318) is exactly:

```
env -i <allowlist> GC_FAST_UNIT=1 scripts/go-test-observable test -- -p=4 -count=1 -timeout 15m ./...
```

Read that line as three deliberate restrictions:

1. `env -i <allowlist>`: your exported env vars are stripped (section 5).
2. `GC_FAST_UNIT=1`: slow process-backed `cmd/gc` scenarios are skipped
   (section 3).
3. No `-tags`: every build-tag-gated tier is invisible (section 3).

`scripts/go-test-observable` wraps `go test -json`, streams a JSONL log to
`$TMPDIR/gascity-<name>.jsonl.*` (or `$OBSERVABLE_TEST_LOG`), and prints
failure details via `jq` when available. On hosts with a provisioned
`gascity-test.slice` systemd user slice it re-execs itself inside that slice
for resource isolation; `GC_TEST_NO_SLICE=1` opts out (TESTING.md,
"Resource isolation via gascity-test.slice").

Docs changed? Also run:

```bash
make check-docs                   # go test ./test/docsync (link/nav/tutorial sync)
```

API, schema, or dashboard changed? Also run:

```bash
make spec-ci                      # regen OpenAPI spec + Go client, fail on drift
make dashboard-check              # SPA typecheck + build + go test embedded handler/BFF
```

Dashboard SPA source lives at `internal/api/dashboardspa/web/` with the
embedded bundle at `internal/api/dashboardspa/dist/` (Makefile:708-744).
_Drift note (2026-07-06): CONTRIBUTING.md and the AGENTS.md quality-gates
list still say `cmd/gc/dashboard/web/`; the SPA moved and the Makefile
targets are authoritative._

## 3. What "make test passed" does NOT mean

This is the modal newcomer failure: green `make check`, red CI. Three whole
families of coverage are deliberately routed out of the default loop.

### 3a. GC_FAST_UNIT-gated process tests (cmd/gc)

Dozens of `cmd/gc` tests open with `skipSlowCmdGCTest(t, ...)`
(`cmd/gc/fast_loop_helpers_test.go:15-23`). They skip under `testing.Short()`
and under any `GC_FAST_UNIT` value except exactly `0`. These are the tests
that start real Dolt lifecycles, real `bd` processes, and the `gc-beads-bd`
provider suite. Run them with:

```bash
make test-cmd-gc-process                                   # full, -timeout 25m, serial
make test-cmd-gc-process-parallel                          # sharded locally
make test-cmd-gc-process-shard CMD_GC_PROCESS_SHARD=3 CMD_GC_PROCESS_TOTAL=6   # one shard
```

In CI this is the `cmd/gc process` job: a 12-way shard matrix
(ci.yml:487-513, `CMD_GC_PROCESS_TOTAL=12`, 10-minute cap per shard).

### 3b. Build-tag-gated tiers

`go test ./...` silently skips all of these (they need `-tags`):

| Tag                      | What it covers                                 | Local target                                   | Needs                      |
| ------------------------ | ---------------------------------------------- | ---------------------------------------------- | -------------------------- |
| `integration`            | real tmux/fs/dolt end-to-end                   | `make test-integration` (30m) or shard targets | tmux, dolt, bd             |
| `acceptance_a`           | Tier A acceptance (command-level, every PR)    | `make test-acceptance` (15m default)           | dolt, bd, claude CLI       |
| `acceptance_b`           | Tier B lifecycle (nightly)                     | `make test-acceptance-b` (10m)                 | same                       |
| `acceptance_c`           | Tier C real inference (manual/nightly, 30-40m) | `make test-acceptance-c` (45m)                 | authed provider CLIs       |
| `integration chaos_dolt` | opt-in managed-Dolt chaos test                 | `make test-chaos-dolt` (45m)                   | dolt                       |
| `gascity_native_beads`   | native DoltLite read-store suite               | `make test-native-doltlite-beads`              | none extra (CGO_ENABLED=0) |

Tier A runs on EVERY PR in CI (`preflight-acceptance`, ci.yml:294-311) even
though no local default target runs it. If you touched dispatch, beads, or
session lifecycle, run `make test-acceptance` locally before pushing.

### 3c. Generated-artifact drift gates

CI regenerates and diffs; you must commit regenerated artifacts:

| Gate                | CI job (`preflight-generated`) step | Local command       | Artifacts checked                                                                                                        |
| ------------------- | ----------------------------------- | ------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| OpenAPI spec/client | `make spec-ci`                      | `make spec-ci`      | `internal/api/openapi.json`, `docs/reference/schema/{openapi,events}.{json,txt}`, `internal/api/genclient/client_gen.go` |
| Dashboard bundle    | `make dashboard-ci`                 | `make dashboard-ci` | `internal/api/dashboardspa/dist/`                                                                                        |
| Schema/CLI docs     | (inside docs tests)                 | `make check-schema` | `docs/reference/` (from `cmd/genschema`)                                                                                 |

The pre-commit hook regenerates these for you when Go files are staged
(section 6), but only if the needed tooling (go, npm, oapi-codegen) is on
PATH. The mechanics of fixing drift belong to sibling skill
`gc-generated-artifacts`.

## 4. Target map: which command proves what

Discovery: `make help` lists every documented target.

| Scope of your change                     | Minimum local proof                                       | CI job that will check it               |
| ---------------------------------------- | --------------------------------------------------------- | --------------------------------------- |
| Any Go change                            | `make build && make check`                                | `preflight-static` (+ more, see below)  |
| `cmd/gc` behavior (lifecycle, providers) | + `make test-cmd-gc-process-parallel`                     | `cmd/gc process` 12-shard matrix        |
| Anything with `//go:build integration`   | + `make test-integration-shards-parallel`                 | `Integration / *` shard matrix          |
| Dispatch / beads / acceptance surface    | + `make test-acceptance`                                  | `Preflight / acceptance A`              |
| `internal/api`, events, wire types       | + `make spec-ci`                                          | `preflight-generated`                   |
| Dashboard SPA or spec-affecting change   | + `make dashboard-check` (and `dashboard-ci` before push) | `Dashboard SPA` + `preflight-generated` |
| Docs / navigation / tutorials            | + `make check-docs`                                       | `preflight-static` Docs step            |
| Broad multi-package sweep                | `make test-local-full-parallel`                           | (union of the above)                    |
| `go.mod` replace directives              | `make check-gomod-replace`                                | `preflight-static`                      |

`preflight-static` in CI runs MORE than local `make check`: it adds
`check-gomod-replace`, `check-native-dependency-surface`,
`check-eventexport-isolation`, `check-core-boundary`,
`test-native-doltlite-beads`, and `make check-docs` (ci.yml:172-231). If you
touched go.mod, `internal/beads`, or event-export code, run those targets
locally too; each is a one-liner named above.

### Sharded local runners (prefer these for sweeps)

Per AGENTS.md and TESTING.md, prefer the sharded wrappers over raw
`go test ./...` for broad runs. They use the same buckets as CI and the same
scrubbed environment:

```bash
make test-fast-parallel                # fast unit loop, cmd/gc sharded
make test-cmd-gc-process-parallel      # full process-backed cmd/gc suite
make test-integration-shards-parallel  # CI integration buckets
make test-local-full-parallel          # all of the above
LOCAL_TEST_JOBS=48 CMD_GC_PROCESS_TOTAL=12 make test-local-full-parallel   # big machine
```

Single buckets (names from `scripts/test-integration-shard` usage):

```bash
./scripts/test-integration-shard packages-cmd-gc-3-of-6
./scripts/test-integration-shard rest-full-4-of-16
GO_TEST_COUNT=1 GO_TEST_TIMEOUT=20m ./scripts/test-go-test-shard ./cmd/gc 1 6
```

Raw `go test ./internal/foo -run TestBar` remains right for a focused loop on
one package.

## 5. The env-scrubbing harness (why your exports vanish)

Every Make test target wraps `go test` in `env -i` with an explicit allowlist
(Makefile:248-307). Rationale: this repo's tests are frequently run by agents
inside live cities; leaked session vars (`GC_CITY`, `GC_HOME`, ...) would let
tests corrupt the very city running them (PR #746).

Rules:

- Need a var to reach a test? `EXTRA_TEST_ENV='FOO=bar' make test`. Do not
  edit the allowlist casually.
- `GC_DOLT_PORT` and `BEADS_DOLT_SERVER_PORT` are deliberately banned from
  the allowlist. Letting them through pointed every bd-forking test at the
  live shared city Dolt server; 18+ parallel workers pegged it and stalled
  bd writes city-wide (incident ga-w2kh1r, documented at Makefile:254-260).
  Never add them.
- Bare `go test` (IDE runners, one-off commands) bypasses TEST_ENV, so
  `internal/testenv` scrubs the same leak-vector vars at test-binary init.
  Every test directory must blank-import it via a `testenv_import_test.go`
  file; `TestRequiresDedicatedTestenvImportFile` (in
  `internal/testenv/lint_test.go`) enforces the layout. See
  `internal/testenv/testenv.go` header comment for passthrough rules.
- Consequence for debugging: "it passes when I run go test by hand but fails
  under make" (or vice versa) is usually an env var difference. Diff against
  the allowlist first.

## 6. Git hooks: what runs when

**pre-commit** (`.githooks/pre-commit`), when Go files are staged:

1. formats staged Go files (`scripts/precommit-format-staged-go`) and
   re-stages them,
2. `make lint-changed LINT_CHANGED_SCOPE=staged ... --fix`,
3. regenerates and stages the OpenAPI spec + Go client (`go run ./cmd/genspec`,
   `go generate ./internal/api/genclient`) and the schema/CLI docs
   (`go run ./cmd/genschema`),
4. `make vet`.

When docs are staged: `make check-docs`. When the spec or SPA source is
staged and `npm` is on PATH: `make dashboard-check dashboard-smoke` and
stages `internal/api/dashboardspa/dist`; without npm it warns and defers to
CI.

**pre-commit runs NO test suite.** That moved to pre-push in commit
`684b27e4f` (PR #3634): running `make test-fast-parallel` per commit fired an
`xargs -P<nproc>` storm of ~2.8 GB test-binary compiles (~17 GB memory
pressure, load >100 on constrained hosts).

**pre-push** (`.githooks/pre-push`): if any pushed ref changes `*.go` files,
it runs `make test-fast-parallel` with `LOCAL_TEST_JOBS` defaulting to 3
(`.githooks/pre-push:40-41`). Branch deletions skip it; a brand-new remote
branch runs it unconditionally. So expect `git push` to take minutes after Go
changes; that is the design, not a hang.

Never bypass with `--no-verify`; fix what the hook caught.

## 7. CI anatomy: what actually gates a merge

As of 2026-07-06 (ci.yml at `f828bbe4b`):

- **`ci-required`** (ci.yml:1337) is the fan-in gate. Every listed job must
  be `success`, except these which may be `skipped` (they are path-gated):
  `cmd-gc-process`, `pack-gate`, `docker-session`, `k8s-session`,
  `openclaw-bridge`. Anything else skipped or failed fails the gate.
- **`Check`** (ci.yml:425) is the historical fan-in for the preflight half
  (static, acceptance A, generated artifacts, plus the path-gated
  `contract-acceptance-current` bd-matrix cell). Branch protection has
  historically required `Check`; the workflow comment says it may move to
  `CI / required`. Treat both as required.
- **Path gating with a union escape** (`changes` job, ci.yml:50-171): jobs
  like `cmd-gc-process` and `integration-shards` only run when their paths
  changed, BUT any change to `shared` paths (go.mod/go.sum, Makefile,
  `.github/workflows/**`, installer scripts, `internal/beads/**`,
  `internal/events/**`, `internal/config/**`) forces the full union run.
  Touching the Makefile or config re-runs everything; budget CI time
  accordingly.
- **Shard matrices**: `cmd/gc process` is 12 shards x 10-minute cap;
  integration is ~30 named shards (packages-core 1-4, packages-cmd-gc 1-6,
  packages-runtime-tmux 1-6, bdstore, rest-smoke 1-2, rest-full 1-16) at
  10-15 minutes each (ci.yml:514-645). A single shard's log names the exact
  `scripts/test-integration-shard` command to reproduce it locally.
- **Runner policy**: PRs from trusted authors run on Blacksmith runners;
  others run on `ubuntu-latest` (ci.yml:28-49,
  `.github/workflows/scripts/runner_policy.py`). Timing differences between
  the two are normal.
- **Coverage jobs** (`preflight-unit-cover-*`) run on pushes to main only,
  not PRs.
- **Non-gating radar**: `contract-radar-bd-head` builds bd from beads main
  HEAD and runs acceptance A against it; a red there is an advisory signal,
  never a merge blocker (ci.yml comment at the job).

**Version pins are guard-tested.** `deps.env` is the single source of truth
for Dolt (2.1.7) and bd (v1.1.0 / prev v1.0.4 / current-rc ref) as of
2026-07-06. `TestDoltVersionPins` (`scripts/dolt_version_pin_test.go`) and
`TestBDVersionPins` (`scripts/bd_version_pin_test.go`) fail the fast unit
loop if any anchor (Dockerfiles, k8s manifests, README floors, every
workflow's `DOLT_VERSION:` assignment) drifts from `deps.env`. To bump a pin:
edit `deps.env`, run `make test` (well, `go test ./scripts`), and let the
guard test enumerate every other file to touch. An earlier era where CI
hardcoded a different Dolt version than deps.env is closed by these guards.

## 8. Safety rails (violations damage shared infrastructure)

- **Never run `go clean -cache`** in any script, hook, or session. Against a
  shared GOCACHE it corrupts the fleet-wide build cache for every concurrent
  executor (incident vp-g96b, 2026-06-13; AGENTS.md "Build Cache
  Conventions"). For a cold build: `GOCACHE=$(mktemp -d) go build ./cmd/gc/`.
  `go clean -testcache` is explicitly allowed.
- **Never run bare `tmux kill-server`** and never kill the default tmux
  server. Test sessions use the `gctest-<8hex>` city prefix and clean
  themselves via `tmuxtest.Guard`; if manual cleanup is unavoidable, target
  only the known city/test socket with `tmux -L <socket> ...` (AGENTS.md
  "Tmux safety"; TESTING.md "Session safety").
- **Do not export GC__/BEADS__ vars expecting tests to see them** (section
  5). Conversely, when tests fork subprocesses they must sanitize env
  themselves; see `sanitizedBaseEnv` in `cmd/gc/fast_loop_helpers_test.go`
  (regression for gascity#938, where inherited env silently overwrote the
  user's real registered city).
- **Prefer `gc stop`** for city shutdown over process killing.

## 9. Worked example: green `make check`, red CI

Scenario (reconstructed from the mechanisms above; every citation is real
code): you modify the beads provider lifecycle in
`cmd/gc/beads_provider_lifecycle.go`, run `make check`, everything passes,
you push, and CI's `cmd/gc process / shard 7 of 12` fails.

Why local was green: the failing test opens with

```go
skipSlowCmdGCTest(t, "starts the real gc-beads-bd lifecycle script; run make test-cmd-gc-process for full coverage")
```

(`cmd/gc/beads_provider_lifecycle_test.go:4320` at `f828bbe4b`; the helper is
`cmd/gc/fast_loop_helpers_test.go:15-23`). `make test` sets `GC_FAST_UNIT=1`
(Makefile:318), so the test was SKIPPED locally, and skips are silent in a
passing run. CI's `cmd/gc process` job runs
`make test-cmd-gc-process-shard ... GC_FAST_UNIT=0` (via Makefile:374-375),
so the test actually executed there and caught the regression.

Reproduce and fix locally:

```bash
# Exactly the failing test, with the slow path enabled:
env -i PATH="$PATH" HOME="$HOME" USER="$USER" TMPDIR=/tmp \
  GC_FAST_UNIT=0 go test -count=1 -timeout 20m ./cmd/gc -run 'TestName_FromCILog'

# Or the whole suite the CI job runs, sharded:
make test-cmd-gc-process-parallel
```

Checklist to avoid the class entirely, before every push that touches
`cmd/gc` lifecycle/provider/dolt/bd code:

- [ ] `make check` green
- [ ] `make test-cmd-gc-process-parallel` green (this is the one people skip)
- [ ] `make test-acceptance` green if dispatch/beads surface changed
- [ ] regenerated artifacts committed if the pre-commit hook staged any

## 10. Timeout expectations (do not "fix" these)

Long timeouts are intentional here; killing a run early and calling it hung
is a false diagnosis. As of 2026-07-06:

| Suite                      | Timeout                                      |
| -------------------------- | -------------------------------------------- |
| `make test` (fast unit)    | 15m                                          |
| `make test-cmd-gc-process` | 25m                                          |
| `make test-integration`    | 30m                                          |
| `make test-acceptance` (A) | 15m default (`ACCEPTANCE_TIMEOUT` overrides) |
| Tier C / chaos-dolt        | 45m                                          |
| Tutorial goldens           | 90m                                          |
| CI shard caps              | 10-15m each                                  |

## Provenance and maintenance

Authored 2026-07-06 by the retiring-fellow distillation campaign, from
`origin/main` @ `f828bbe4b` of gastownhall/gascity, cross-checked against the
Phase-1 discovery report (machine-local:
`/home/ds/gas-city/docs/design/fable-distillation/discovery-gascity.md`) and
the provisional maintainer answers in the 2026-07-07 morning ledger. Items
marked "provisional" await Stephanie's real answers.

Volatile facts and how to re-verify each (run from the repo root):

| Claim                                        | Re-verify with                                                                         |
| -------------------------------------------- | -------------------------------------------------------------------------------------- |
| `make check` composition                     | `git show origin/main:Makefile \| grep -n '^check:'`                                   |
| `make test` flags / GC_FAST_UNIT / 15m       | `git show origin/main:Makefile \| grep -n -A2 '^test: test-fsys'`                      |
| TEST_ENV allowlist + banned ports            | `git show origin/main:Makefile \| sed -n '248,310p'`                                   |
| ci-required allow_skipped set                | `git show origin/main:.github/workflows/ci.yml \| grep -n -A20 'ci-required:'`         |
| Shard counts (12 cmd/gc, 16 rest-full)       | `git show origin/main:.github/workflows/ci.yml \| grep -cn 'rest-full-.*-of-16'`       |
| Dolt/bd pins                                 | `git show origin/main:deps.env`                                                        |
| Hook behavior (no tests at commit)           | `git show origin/main:.githooks/pre-commit` and `:.githooks/pre-push`                  |
| Dashboard path (`internal/api/dashboardspa`) | `git show origin/main:Makefile \| grep -n 'dashboardspa'`                              |
| skipSlowCmdGCTest semantics                  | `git show origin/main:cmd/gc/fast_loop_helpers_test.go \| head -25`                    |
| Tier-A-on-every-PR                           | `git show origin/main:.github/workflows/ci.yml \| grep -n -B2 'make test-acceptance$'` |

If any re-verification disagrees with this skill, the repo wins; update the
skill in the same change.
