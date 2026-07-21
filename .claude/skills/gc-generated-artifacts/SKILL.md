---
name: gc-generated-artifacts
description: >
  Gas City generated-artifacts runbook: the codegen chain from Go structs to
  OpenAPI spec, generated Go client, dashboard TypeScript client, embedded
  dashboard dist/, and config reference docs. Load when touching internal/api/,
  internal/api/openapi.json, internal/api/genclient/, internal/api/dashboardspa/,
  internal/config struct tags, or docs/reference/schema/; when CI fails on
  spec-ci, dashboard-ci, "Preflight / generated artifacts",
  TestOpenAPISpecInSync, TestGeneratedClientInSync, or TestSchemaFreshness;
  when a diff says dist/ or spec/client artifacts are "stale" or "drifted";
  or when deciding which regeneration commands to run before committing an
  API or config-schema change. Not for adding event types (gc-events-payloads)
  or general test-tier selection (gc-build-verify).
---

# gc-generated-artifacts — the codegen chain and its drift gates

Tier 1 (single-session, no subagents; survives `DISABLE_INTERACTIVITY=1`).

Gas City commits its generated artifacts: the OpenAPI spec, the typed Go API
client, the dashboard's TypeScript supervisor client, the compiled dashboard
bundle, and the config reference docs all live in git. The contract is
**generated, never hand-written** (AGENTS.md "Typed wire"; rationale in
`engdocs/architecture/api-control-plane.md` §3.2). CI does not regenerate for
you; it regenerates and **fails if your commit disagrees**. This skill maps
every generator to its committed outputs and to the gate that catches each
kind of drift, so you run the right commands before push instead of decoding
a red CI rollup after.

All paths and commands below were verified against `origin/main` at
`f828bbe4b` (2026-07-06). Layout-volatile facts are date-stamped; the
re-verification commands are in the last section.

## When NOT to use this skill

| You are doing                                                           | Use instead                                                                                                                                          |
| ----------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Adding or changing an event type / payload struct                       | `gc-events-payloads` (sibling); it owns the constant→register→regen runbook                                                                          |
| Choosing make targets / test tiers, diagnosing `env -i` test env issues | `gc-build-verify` (sibling) and `TESTING.md`                                                                                                         |
| Designing a new HTTP/SSE endpoint (Huma patterns, typed-wire rules)     | `engdocs/contributors/huma-usage.md` + `engdocs/architecture/api-control-plane.md` — AGENTS.md requires reading both before touching `internal/api/` |
| Release tagging, rc-gate, nightly tiers                                 | `gc-release-ci-ops` (sibling) and `RELEASING.md`                                                                                                     |
| Dashboard feature work (React/SPA behavior, BFF endpoints)              | `engdocs/architecture/api-control-plane.md` and `plans/new-dashboard-supervisor-hosting.md`                                                          |

Sibling skills are part of the same departure library and may land
separately; the repo docs cited above are always authoritative.

## Vocabulary (defined once)

| Term                       | Meaning                                                                                                                                                                                                                                                                                                                                                            |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Huma**                   | The Go framework (`github.com/danielgtaylor/huma/v2`) that registers every HTTP/SSE operation and emits the OpenAPI spec from Go types. The spec is a _projection of code_, never edited by hand.                                                                                                                                                                  |
| **genspec**                | `cmd/genspec`: boots a `SupervisorMux` against an empty resolver, fetches its live `/openapi.json`, writes all committed spec copies.                                                                                                                                                                                                                              |
| **genclient**              | `internal/api/genclient/client_gen.go`: the typed Go client the `gc` CLI uses to call the supervisor. Regenerated via `go generate ./internal/api/genclient` → `scripts/gen-client.sh` → `cmd/gen-client` → `oapi-codegen` v2.6.0 (pinned) consuming Huma's automatic OpenAPI **3.0 downgrade** (oapi-codegen chokes on 3.1; see `cmd/gen-client/main.go` header). |
| **genschema**              | `cmd/genschema`: generates JSON Schema + markdown reference docs from the Go config structs (`city.toml` / `pack.toml` shapes).                                                                                                                                                                                                                                    |
| **dashboardspa**           | `internal/api/dashboardspa/`: the vendored React/Vite dashboard (npm workspaces `shared` + `frontend` under `web/`) plus the committed compiled bundle `dist/`, embedded by `//go:embed all:dist` (`embed.go:10`) so a Node-less `go build` still ships a working dashboard.                                                                                       |
| **dashboardbff**           | `internal/api/dashboardbff/`: the Go host-side `/api` plane serving the dashboard's non-Huma needs (git log, builds, health samplers). Tested by `make dashboard-check`.                                                                                                                                                                                           |
| **drift gate**             | A test or CI step that regenerates an artifact (or compares live output to disk) and fails when the committed copy disagrees.                                                                                                                                                                                                                                      |
| **spec-ci / dashboard-ci** | Make targets used by the CI job `Preflight / generated artifacts` (`preflight-generated` in `.github/workflows/ci.yml`) to enforce spec/client and dist/ freshness.                                                                                                                                                                                                |
| **docsync**                | `test/docsync`, run by `make check-docs`: doc-to-code sync tests, including `TestSchemaFreshness` and schema download-link checks.                                                                                                                                                                                                                                 |

## Layout note (2026-06-28 migration)

Commit `677ce243f` (#3727, "supervisor hosts the new React/Vite dashboard")
moved the dashboard from `cmd/gc/dashboard/` to `internal/api/dashboardspa/`
and the schema docs from `docs/schema/` to `docs/reference/schema/`. Older
material — including two path mentions inside AGENTS.md's own checklists and
pre-migration commits/skills — still cites `cmd/gc/dashboard/web/...` and
`docs/schema/...`. The _requirements_ in AGENTS.md (run `make dashboard-check`
for API/dashboard changes; spec is generated, never hand-written) are
unchanged; apply them at the current paths below. If a path in this skill 404s,
re-run the provenance commands at the bottom before trusting anything else.

## The chain

```
Go source of truth
│  internal/api/*.go          Huma-registered operations + typed wire structs
│  internal/events/*          typed event registry (see gc-events-payloads)
│  internal/config structs    city.toml / pack.toml shapes
│
├── go run ./cmd/genspec
│     → internal/api/openapi.json                 (OpenAPI 3.1; drift-check source of truth)
│     → docs/reference/schema/openapi.{json,txt}  (published docs copy + mirror)
│     → docs/reference/schema/events.{json,txt}   (gc events JSONL line schema)
│
├── go generate ./internal/api/genclient          (needs oapi-codegen v2.6.0 on PATH)
│     → internal/api/genclient/client_gen.go      (typed Go client for the gc CLI)
│
├── [out-of-band, see Trap 4] @hey-api/openapi-ts
│     → internal/api/dashboardspa/web/shared/src/generated/gc-supervisor-client/*.gen.ts
│
├── make dashboard-build                          (npm ci + workspace build, then
│     → internal/api/dashboardspa/dist/            cp frontend/dist → ../dist; committed,
│                                                  embedded via //go:embed all:dist)
│
└── go run ./cmd/genschema
      → docs/reference/schema/{city,pack}-schema.{json,txt}
      → docs/reference/config.md, docs/reference/cli.md
```

## Which gate catches which drift

| Miss                                                              | Local gate                                                                                                                                                | CI gate                                                                                               |
| ----------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Spec not regenerated after a Go type/handler change               | `TestOpenAPISpecInSync` (`internal/api/openapi_sync_test.go`) — runs in plain `make test`; compares live supervisor spec against all three tracked copies | `spec-ci` in `preflight-generated`, plus the same test in the unit suite                              |
| Go client stale relative to spec                                  | `TestGeneratedClientInSync` (`internal/api/genclient/genclient_test.go`) — **silently skips if `oapi-codegen` is not on PATH**                            | `spec-ci` with `GC_REQUIRE_OAPI_CODEGEN=1` set at the job level, which turns that skip into a failure |
| Committed `dist/` doesn't match SPA source                        | `make dashboard-ci` (runs dashboard-check, then `git diff --quiet -- internal/api/dashboardspa/dist`)                                                     | `dashboard-ci` in `preflight-generated`                                                               |
| Frontend type errors                                              | `npm run typecheck` at `internal/api/dashboardspa/web` (part of `make dashboard-check`)                                                                   | same, inside `dashboard-ci`                                                                           |
| `city-schema.json` / `config.md` stale after config-struct change | `TestSchemaFreshness` via `make check-docs`; broader: `make check-schema` (regenerates, then `git diff --exit-code docs/reference/`)                      | `make check-docs` in the `preflight-static` job ("Docs" step)                                         |
| Schema download links pointing at uncommitted files               | `TestSchemaDownloadLinksUseGitHubRaw` via `make check-docs`                                                                                               | same                                                                                                  |
| Dashboard TS supervisor client stale                              | **nothing** (see Trap 4)                                                                                                                                  | **nothing** (see Trap 4)                                                                              |

`preflight-generated` feeds the branch-protection required rollup; a red
there blocks merge.

## Runbook: you changed anything under internal/api/ (types, handlers, tags)

Run from the repo root:

```bash
# 1. Regenerate the spec (all committed copies)
go run ./cmd/genspec

# 2. Regenerate the typed Go client (installs pinned oapi-codegen if missing)
make install-oapi-codegen
go generate ./internal/api/genclient

# 3. Rebuild + typecheck + test the dashboard against the new spec
make dashboard-check

# 4. Commit everything, generated files included, in the same commit
#    as the Go change. Then confirm zero residual drift:
make spec-ci
make dashboard-ci
```

Commit the generated diffs with the source change (one commit; a follow-up
"regen" commit is the failure mode in the worked example below). If the wire
shape you changed is one the dashboard consumes, also see Trap 4.

Scope note (provisional, from the 2026-07-07 maintainer ledger): a change to
the wire contract itself — payload shapes, endpoint semantics, public CLI
surface — is a cross-subsystem contract and must be routed to human review
rather than auto-merged, regardless of green gates.

## Runbook: you changed config structs (city.toml / pack.toml shapes)

```bash
go run ./cmd/genschema        # same as `make generate`
git add docs/reference/schema/city-schema.json docs/reference/schema/city-schema.txt \
        docs/reference/schema/pack-schema.json docs/reference/schema/pack-schema.txt \
        docs/reference/config.md docs/reference/cli.md
make check-docs               # runs test/docsync incl. TestSchemaFreshness
```

Remember the config.Agent four-sibling rule (AGENTS.md "Adding agent config
fields") — genschema documents the struct you wrote, it does not check that
patch/override/pool copies were updated.

## Runbook: you changed dashboard SPA source

```bash
make dashboard-check          # npm ci + workspace build + typecheck + go test dashboardspa/... dashboardbff/...
make dashboard-smoke          # serve the built bundle via vite preview, verify it responds
git add internal/api/dashboardspa/dist
make dashboard-ci             # final stale-dist check (after committing)
```

The `web/` tree is vendored from `github.com/gastownhall/gascity-dashboard`
(`web/package.json` `repository` field); check whether a change belongs
upstream in that repo before diverging the vendored copy.

## The pre-commit hook does most of this — when it can

`.githooks/pre-commit` (active when `git config core.hooksPath` prints
`.githooks`; `make setup` configures it), on any staged `*.go` file:
runs genspec, `go generate ./internal/api/genclient`, genschema, and stages
the outputs; on a staged spec change or dashboard web-source change it runs
`make dashboard-check dashboard-smoke` and stages `dist/` — but only if `npm`
is on PATH, otherwise it prints a warning and leaves dist/ regeneration to
CI. Heavy tests moved to `.githooks/pre-push` (#3628). Consequences:

- No Go/npm toolchain at commit time (or `--no-verify`) means committed drift
  that CI will reject. The hook is a convenience, not the gate.
- As of 2026-07-06 the hook's genschema `git add` list omits
  `pack-schema.{json,txt}`; if your change touches pack config structs, stage
  those two by hand or the worktree stays dirty after commit.

## Traps

1. **`make test` alone does not prove client freshness.**
   `TestGeneratedClientInSync` skips without `oapi-codegen` on PATH. Locally
   green, CI red. Run `make install-oapi-codegen` once, or run `make spec-ci`
   before push.
2. **`spec-ci` and `dashboard-ci` are `git diff` checks.** They compare the
   worktree after regeneration, so pre-existing uncommitted edits to those
   paths read as drift. Commit first, then run them as the final check.
3. **`tsc --noEmit` is load-bearing, in order.** The web-root `typecheck`
   script typechecks `shared`, _builds_ `shared`, then typechecks `frontend`
   (frontend imports shared's built `dist/`). Don't typecheck the frontend
   workspace in isolation and conclude the tree is clean.
4. **The dashboard TS supervisor client has no in-repo regeneration target
   (open gap, observed 2026-07-06).** `engdocs/architecture/api-control-plane.md`
   §5 states the spec pre-commit step does not regenerate
   `web/shared/src/generated/gc-supervisor-client/`, and no make target,
   npm script, or openapi-ts config file exists in-tree (the plan doc names a
   `web/openapi-ts.config.ts` that is absent at origin/main). Recent API
   commits (e.g. `35fb51511`) updated it by running `@hey-api/openapi-ts`
   out-of-band. No gate diffs it against the spec, so it can drift silently:
   `tsc` only fails if SPA code references a shape that changed. If your API
   change alters shapes the dashboard consumes, regenerate this client
   deliberately and say how in the commit message. Candidate fix (unproven):
   add it to `spec-ci`'s regen + diff list.
5. **Never redirect gen-client output onto its own target.**
   `go run ./cmd/gen-client > client_gen.go` zeroes the file before the
   compile step reads it and the build dies. `scripts/gen-client.sh` exists
   precisely to write via a temp file + atomic `mv`. Use the `go generate`
   entry point.
6. **Never hand-edit any `*_gen.*`, `openapi.json`, `dist/`, or
   `docs/reference/{config,cli}.md`.** Fix the Go source (or SPA source) and
   regenerate. A hand-edit survives exactly until the next regen, and the
   sync tests treat the regenerated form as truth.
7. **Stale paths in older material.** Anything citing
   `cmd/gc/dashboard/web/...`, `docs/schema/...`, or `npm run gen` predates
   the 2026-06-28 migration (see Layout note). Translate before acting.

## Worked examples (real history)

**The failure shape — regen forgotten, CI catches it, follow-up commit.**
Commit `5a6c5f33f` (2026-05-10) added a typed `SessionLifecyclePayload` and
regenerated the spec, Go client, and (it believed) the dashboard types. The
dashboard TS schema regen was incomplete: CI's dashboard job regenerated and
diffed, and three rollups went red from the one root cause (per the fix
commit's own message: "CI/preflight + CI/required + Dashboard SPA — three
rollups, one root cause"). The fix, `58e0b8dbb` (2026-05-11,
"fix(dashboard): regenerate TS schema for SessionLifecyclePayload"), is a
purely mechanical 3-file regen commit that should have been part of the
feature commit. Lesson: generated artifacts ship in the same commit as the
source change, and the regen commands in the runbook above are cheaper than
decoding three red rollups. (Paths in those commits are pre-migration.)

**The correct shape — one commit, full fan-out.** Commit `35fb51511`
(2026-06-30, #3843, "expose provider option_defaults on create/update")
changed two wire-input structs and one handler pair, and the same commit
carries the entire generated fan-out at current paths: `internal/api/openapi.json`
(+14), `docs/reference/schema/openapi.{json,txt}` (+14 each),
`internal/api/genclient/client_gen.go` (+6),
`web/shared/src/generated/gc-supervisor-client/{types.gen.ts,zod.gen.ts}`,
and the rebuilt `internal/api/dashboardspa/dist/` (hashed asset renames).
37 files, of which roughly 30 are generated. That ratio is normal for an API
change; a reviewer seeing the Go diff without the generated fan-out should
ask where it went.

## Provenance and maintenance

Authored 2026-07-06 by the retiring-fellow distillation campaign, grounded in
`origin/main` at `f828bbe4b`. Placement is provisional per the 2026-07-07
maintainer ledger: fork-local, written repo-portable for possible upstreaming.
Discovery evidence lived in the ds-research workspace
(`docs/design/fable-distillation/discovery-gascity.md`); nothing in this file
depends on it.

Re-verify before trusting, cheapest first:

```bash
# Generator commands and drift-gate targets still exist and match:
grep -n "genspec\|genclient\|genschema" .githooks/pre-commit
grep -n -A6 "^generate:\|^check-schema:\|^spec-ci:\|^dashboard-ci:\|^dashboard-check:" Makefile

# Committed output paths still match the generators' own headers:
head -25 cmd/genspec/main.go cmd/genschema/main.go cmd/gen-client/main.go

# CI job still runs both drift checks with the skip-proofing env:
grep -n -A20 "preflight-generated:" .github/workflows/ci.yml

# Trap 4 still open? (a hit here means a regen entry point landed — update this skill)
git grep -ln "openapi-ts" -- Makefile scripts internal/api/dashboardspa/web/*.json internal/api/dashboardspa/web/*.ts 2>/dev/null

# Sync tests still enforce what this skill claims:
grep -rn "func TestOpenAPISpecInSync\|func TestGeneratedClientInSync\|func TestSchemaFreshness" internal/api test/docsync
```

If the dashboard moves again, or `docs/reference/schema/` moves, rewrite the
Layout note and re-date every path in this file; a runbook with one dead path
teaches distrust of the live ones.
