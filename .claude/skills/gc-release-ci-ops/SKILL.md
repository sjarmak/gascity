---
name: gc-release-ci-ops
description: >-
  Gas City release and CI operations runbook: cutting a release (tag-only
  versioning, bump-version.sh, release.yml/GoReleaser, Homebrew tap), running
  the manual RC gate, reading the CI workflow landscape (ci-required
  semantics, runner policy, path-gated jobs), scheduled test tiers (nightly,
  mac-regression, chaos-dolt, tutorial goldens), security scanning (Trivy
  waivers, CodeQL, Scorecard), and dependency pin management (deps.env as
  the guard-tested source of truth, Renovate). Load when asked to cut or
  troubleshoot a
  release, interpret a red or skipped CI check, run RC/nightly/mac suites,
  add or expire a Trivy waiver, bump a pinned tool version (dolt, bd, br,
  claude-code), or explain a release-gates/ file.
---

# Gas City release and CI operations

Tier 1 (single-session, no subagents).

Runbook for the machinery that runs after `git push`: pull-request CI,
scheduled test tiers, security scans, and the tag-to-Homebrew release
pipeline. All facts below were verified against `origin/main` at
`f828bbe4b` (2026-07-06); volatile ones are date-stamped and have
re-verification commands at the end. Local checkouts can be weeks stale —
run the re-verification commands against `origin/main`, not your worktree,
before acting on any version- or line-number-sensitive claim (sibling
`gc-build-verify` owns that discipline).

## When NOT to use this skill

| You want to...                                                                          | Use instead                                                                                                                        |
| --------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| Run local gates before pushing (`make build`, `make check`, test env traps, build tags) | sibling skill `gc-build-verify`                                                                                                    |
| Write new tests or pick a test kind                                                     | sibling skill `gc-test-authoring`, plus `TESTING.md`                                                                               |
| Fix OpenAPI / dashboard / schema drift that `spec-ci` or `dashboard` jobs flagged       | sibling skill `gc-generated-artifacts`                                                                                             |
| Decide PR scope, or whether a change needs a human maintainer hold                      | sibling skill `gc-change-workflow`                                                                                                 |
| Debug managed Dolt itself (locks, ports, reaping)                                       | sibling skill `gc-dolt-ops`                                                                                                        |
| The step-by-step release checklist and Homebrew troubleshooting                         | `RELEASING.md` at the repo root — it is the canonical owner of the release runbook; this skill adds the CI/gates context around it |

Nothing here routes around change-control: cutting a release, pushing a tag,
and merging to `main` are maintainer actions. If you are an agent, a tag push
publishes binaries worldwide — treat it as an external artifact requiring
explicit human approval.

## Vocabulary (defined once)

| Term                    | Meaning                                                                                                                                                                                                                                                     |
| ----------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Tag-only versioning** | The version exists only in the git tag `vX.Y.Z`. Go source has `var version = "dev"` (`cmd/gc/cmd_version.go`); Makefile and `.goreleaser.yml` inject the real value via `-X main.version=...` at build time. There is no version constant to keep in sync. |
| **GoReleaser**          | The release builder run by `release.yml`; builds linux/darwin x amd64/arm64 binaries and creates the GitHub Release. Config: `.goreleaser.yml`.                                                                                                             |
| **RC gate**             | `.github/workflows/rc-gate.yml`, a manually-dispatched superset of CI run before cutting a release candidate. Not triggered by tags or PRs.                                                                                                                 |
| **Blacksmith**          | Sponsored third-party CI runners (faster, bigger: 2-32 vCPU Ubuntu + 12 vCPU macOS). Gated by author allowlist, see Runner policy below.                                                                                                                    |
| **Tier**                | A test layer behind a build tag or schedule: fast unit (default), `integration`, `acceptance_a/b/c`, `chaos_dolt`. `TESTING.md` owns tier definitions.                                                                                                      |
| **Release gate file**   | A markdown verdict record under `release-gates/` documenting that a release-blocker bead passed review (see "release-gates/ convention").                                                                                                                   |
| **Waiver**              | A time-boxed Trivy vulnerability ignore entry in `.trivyignore.yaml` with an `expired_at` date and a remediation statement.                                                                                                                                 |
| **Renovate**            | Bot that opens dependency-bump PRs; config in `renovate.json`, including custom regex managers for the tool pins.                                                                                                                                           |

## 1. Cutting a release

`RELEASING.md` is the canonical runbook — read it first. Operational summary
and the traps it implies:

```bash
# The one-command path (maintainer only; publishes worldwide):
./scripts/bump-version.sh X.Y.Z --commit --tag --push
```

The script moves the `CHANGELOG.md` `[Unreleased]` section to `[X.Y.Z] -
DATE`, commits, tags `vX.Y.Z`, and pushes. `--tag` requires `--commit`;
`--push` requires `--tag`. Version format is `X.Y.Z` — no leading `v`, no
pre-release suffix (the script validates this).

What fires on tag push (`.github/workflows/release.yml`, trigger `tags:
v*`, only publishes when `github.repository == 'gastownhall/gascity'` —
forks skip publish/announce automatically):

1. Reject `replace` directives in `go.mod` (they break `go install @latest`
   and homebrew-core bottles).
2. `make check-version-tag` — no-op on untagged HEAD; passes clean
   `vMAJOR.MINOR.PATCH`; **fails any pre-release suffix** (`-rc1`, `-beta`).
   This failing run is the designed mechanism that keeps RC tags from
   publishing.
3. GoReleaser build + GitHub Release with grouped changelog.
4. SBOM upload + artifact attestations (`attest-release` job).
5. Homebrew tap formula rewrite in `gastownhall/homebrew-gascity`
   (`update-homebrew-formula` job; GitHub App credentials only, fails hard
   if `HOMEBREW_TAP_APP_ID` / `HOMEBREW_TAP_APP_PRIVATE_KEY` are missing).

Pre-tag local checks (copy-paste):

```bash
make check-version-tag    # no-op unless HEAD is tagged
grep '^replace' go.mod    # must print nothing
goreleaser check          # config sanity; also enforced in CI
```

### Worked example: v1.3.3 (2026-07-02)

Real history, verify with `git log --oneline v1.3.3-rc1 v1.3.3`:

- RC practice: tag `v1.3.3-rc1` was cut on `a44126951` ("fix(init): seed
  gascity role pack so Mayor can launch built-in formulas (#3832) (#3875)")
  — an ordinary fix commit, no changelog rewrite. Per `RELEASING.md`, an RC
  tag never publishes: `make check-version-tag` fails it in `release.yml`.
- The stable release is a separate bump commit: `55acb481229` ("chore:
  release v1.3.3"), the `bump-version.sh` shape (CHANGELOG rewrite +
  annotated tag `v1.3.3`), pushed 2026-07-02 20:38 UTC.
- Pattern to copy: RC tag on the candidate commit → run the RC gate → cut
  the stable tag via the bump script on a fresh changelog commit. The
  earlier `v1.3.0-rc1..rc3` series shows the same loop taking three
  candidates.

## 2. The RC gate (pre-release superset)

`.github/workflows/rc-gate.yml` — `workflow_dispatch` only; run it from the
Actions tab (or `gh workflow run rc-gate.yml`) before cutting a release.
It forces Blacksmith runners (`ci_parity` passes `force_blacksmith: true`)
and inherits the shared CI graph so new PR-CI checks apply automatically.

Jobs (verified 2026-07-06):

| Job                                 | What it adds beyond PR CI                                                                                                                                                                                                                                              |
| ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ci_parity`                         | Reuses `ci.yml` wholesale                                                                                                                                                                                                                                              |
| `ubuntu_fast_tests`                 | Fast-suite matrix incl. a darwin/arm64 cross-compile of `internal/fsys`                                                                                                                                                                                                |
| `ubuntu_make_check_docs`            | `make check-docs`                                                                                                                                                                                                                                                      |
| `ubuntu_acceptance_a` / `_b` / `_c` | The tag-gated acceptance tiers, with real inference for C                                                                                                                                                                                                              |
| `ubuntu_integration_shards`         | Integration tier shards                                                                                                                                                                                                                                                |
| `ubuntu_tutorial`                   | Tutorial goldens, 6 shards at `max-parallel: 2`, 110-minute timeout each, `GO_TEST_TIMEOUT=90m`, real Claude inference via Ollama-hosted models (`ANTHROPIC_BASE_URL: https://ollama.com`, needs `OLLAMA_API_KEY` secret + `GC_WORKER_INFERENCE_CLAUDE_OLLAMA_*` vars) |
| `ubuntu_goreleaser_snapshot`        | `goreleaser release --snapshot --clean`, uploads the dist as an artifact                                                                                                                                                                                               |
| `mac_regression`                    | Dispatches `mac-regression.yml` with `suite: full`, `force_blacksmith: true`                                                                                                                                                                                           |
| `rc_summary`                        | Aggregates verdicts                                                                                                                                                                                                                                                    |

Budget a multi-hour wall clock; the tutorial goldens alone can take ~9
shard-hours across the 6 shards. Local equivalent of the goldens:
`make test-tutorial-goldens` (Makefile; `acceptance_c` tag, 90m timeout,
requires tmux + dolt + bd + authed claude).

## 3. PR CI landscape and `ci-required`

`.github/workflows/ci.yml` (~59K) is the PR gate. Branch protection watches
one check: **`CI / required`** (job `ci-required`, ci.yml:1337 at
`f828bbe4b`). Its semantics: every job in its `needs` list must be
`success`, EXCEPT these five, which may also be `skipped` (they are
path-gated and only run when their paths changed):

```
cmd-gc-process   pack-gate   docker-session   k8s-session   openclaw-bridge
```

`needs` list of `ci-required`: `runner-policy`, `changes`, `ci-preflight`
(itself gating `check`, `release-config`, `dashboard`), `ci-integration`
(gating `integration-shards`), `cmd-gc-process`, `worker-core-summary`,
`worker-core-phase2-summary`, `pack-gate`, `docker-session`, `k8s-session`,
`openclaw-bridge`. So: a `skipped` on the five path-gated jobs is normal; a
`skipped` or `failure` anywhere else fails the PR. The job writes a per-job
result table into the Actions step summary — read that before rerunning
anything.

Other jobs in ci.yml worth knowing: `preflight-static`,
`preflight-unit-cover-{noncmdgc,cmdgc}` (pushes to main only),
`preflight-acceptance`, `preflight-generated`
(generated-artifact drift; see sibling `gc-generated-artifacts`), and the
`worker-core-{claude,codex,gemini}` (+ `phase2`) matrices whose `-summary`
jobs are the required aggregation points.

### Runner policy

`.github/workflows/scripts/runner_policy.py` picks runners per run:

- PR author in `.github/blacksmith-allowlist.txt` (currently
  `julianknutsen`, `csells`, `sjarmak`, `quad341`; 2026-07-06) → Blacksmith
  runners.
- Everyone else, and all non-PR events (push, schedule, dispatch) →
  GitHub-hosted (`ubuntu-latest` / `macos-15`), unless a workflow passes
  `force_blacksmith` (rc-gate does).

Consequence: outside-contributor PRs run slower on GitHub-hosted runners.
That is policy, not a bug; do not "fix" it by editing the allowlist —
allowlist changes are a maintainer decision.

### Label-dispatched suites

`.github/workflows/dispatch-labeled-pr-suite.yml` (`pull_request_target`,
trusted authors only, non-draft PRs): label `needs-mac` dispatches
`mac-regression.yml` with `suite: needs-mac`; label `needs-review-formulas`
dispatches `review-formulas.yml`.

## 4. Scheduled tiers (what runs when nobody pushes)

All crons UTC; verified 2026-07-06.

| Workflow                                                                                        | Schedule                                                                                                    | Contents                                                                                                                                                                                                 |
| ----------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `nightly.yml`                                                                                   | daily 06:00                                                                                                 | `tier-b` (=`make test-acceptance-b` then `test-acceptance-c` with Ollama-hosted Claude inference), `mac-inference` (Tier B+C on macOS, 180m timeout), `worker-inference-{claude,codex,gemini}` + summary |
| `mac-regression.yml`                                                                            | daily 03:17, plus PRs to main, plus dispatch (suite: `smoke`/`full`/`needs-mac`)                            | macOS quality/unit/acceptance/coverage/integration shards                                                                                                                                                |
| `container-scan.yml`                                                                            | Wednesdays 06:43, plus PR/push touching container paths, `deps.env`, `go.mod/sum`, or the trivyignore files | Trivy config scan + image build/scan (see section 5)                                                                                                                                                     |
| `homebrew-tap-smoke.yml`                                                                        | daily 06:25                                                                                                 | Installs from the tap, smoke-tests the published formula                                                                                                                                                 |
| `scorecard.yml`                                                                                 | daily 05:37                                                                                                 | OpenSSF Scorecard                                                                                                                                                                                        |
| `codeql.yml`                                                                                    | push/PR to main, plus Mondays 04:24                                                                         | CodeQL static analysis                                                                                                                                                                                   |
| `close-stale-needs.yml`, `remove-needs-info.yml`, `remove-needs-triage.yml`, `triage-label.yml` | various                                                                                                     | Issue-triage hygiene bots                                                                                                                                                                                |
| `ollama-acceptance-c.yml`                                                                       | manual dispatch                                                                                             | Acceptance-C rerun against Ollama-hosted models                                                                                                                                                          |

The chaos tier does not run in any workflow: `make test-chaos-dolt`
(Makefile:542-548, tags `integration chaos_dolt`, 45m timeout, tunable via
`GC_DOLT_CHAOS_DURATION` / `GC_DOLT_CHAOS_SEED`) is opt-in local only
(verified 2026-07-06: `chaos_dolt` appears in no workflow file). If a Dolt
lifecycle change is in your diff, run it yourself; CI will not.

A nightly/mac failure on your merged change is still yours to fix
(codebase-ownership), even though `ci-required` was green — several tiers
only run after merge.

## 5. Security scanning and Trivy waiver discipline

`container-scan.yml` has two enforcement jobs, each with its own ignore
file (do not mix them up):

- `dockerfile-config`: `trivy config --severity HIGH,CRITICAL --ignorefile
.trivyignore-config --exit-code 1 contrib/k8s`. `.trivyignore-config`
  holds misconfiguration IDs (currently one: `KSV-0053`, the deliberately
  narrow pod-exec RBAC exception — the file's comment says keep it narrow).
- `image-vulnerabilities`: builds the four images (`gc-agent-base`,
  `gc-agent`, `gc-controller`, `gc-mcp-mail`), generates SARIF + CycloneDX
  SBOMs, then enforces `trivy image --severity HIGH,CRITICAL
--ignore-unfixed --ignorefile .trivyignore.yaml --exit-code 1`.

### Waiver format (the discipline)

Every entry in `.trivyignore.yaml` must carry all four fields — this is the
established shape of all 44 current entries (2026-07-06), not an
aspiration:

```yaml
vulnerabilities:
  - id: CVE-2026-33811
    paths:
      - "usr/local/bin/dolt" # narrowest possible path
    expired_at: 2026-08-07 # time-box: Trivy re-flags after this date
    statement: Upstream dolt embeds Go 1.26.2 stdlib; remove once it rebuilds against 1.26.3+.
```

Rules when adding one: (a) waive only what upstream cannot yet fix, and say
which upstream release removes the need; (b) scope `paths` to the exact
binary/package; (c) set `expired_at` weeks-not-months out — when it lapses,
the Wednesday scheduled scan goes red and forces a decision (bump the dep,
or consciously extend the waiver with a new statement); (d) never waive a
finding in first-party Go code — fix it.

Date-stamped observation (2026-07-06): all 44 current entries expire
2026-08-07 — the horizon was bulk-extended on 2026-07-06 after a re-audit
confirmed every waived upstream is still pinned at its vulnerable version
(the file's own header comment records this). Before touching this file,
check the latest scheduled `Container Scan` run and each entry's
`statement:` — drop entries whose upstream has rebuilt; renew the rest
consciously. Do not assume either; look.

## 6. Dependency pins: deps.env is the guard-tested source of truth

`deps.env` (repo root) owns the tool pins: `DOLT_VERSION=2.1.7`,
`BD_VERSION=v1.1.0`, `BR_VERSION=0.1.20`, plus the contract-test matrix
pins `BD_PREV_VERSION` / `BD_CURRENT_VERSION` / `BD_CURRENT_REF` (values as
of 2026-07-06). Workflows still carry their own `DOLT_VERSION:` env values,
but they can no longer drift: `TestDoltVersionPins`
(`scripts/dolt_version_pin_test.go`) and `TestBDVersionPins`
(`scripts/bd_version_pin_test.go`) run in the fast unit loop and fail if
any anchor — workflow env blocks, `contrib/k8s/Dockerfile.base`, k8s
manifests, README version floors, the install-script SHA tables — disagrees
with `deps.env`. An earlier era where CI hardcoded a different Dolt version
than the images shipped is closed by these guards (sibling `gc-build-verify`
§7 owns the bump procedure).

To bump a pinned tool: edit `deps.env`, run `go test ./scripts`, and let
the guard test enumerate every other file to touch in the same PR. Two
adjacent layers to know about: `internal/deps` defines minimum _compatible_
versions, which may deliberately trail the pins (deps.env header comment);
and Renovate (`renovate.json` custom regex managers) bumps `DOLT_VERSION:`
in six workflows, `DOLT_VERSION=` in `deps.env`, and the Makefile as one
grouped PR, plus GitHub Action digests, Docker digests, and the
`@anthropic-ai/claude-code` version in the setup actions and
`contrib/k8s/Dockerfile.base`.

## 7. The `release-gates/` convention

`release-gates/` holds one markdown file per release-blocker bead
(199 files on `origin/main`, most named `ga-<bead-id>[-slug]-gate.md`, a
minority with descriptive slugs and no bead id; verified 2026-07-06).
There is no doc specifying the format; the convention below is read off the
files themselves — treat it as observed practice, and match it rather than
inventing variations.

Anatomy (worked example: `release-gates/ga-iwec-dolt-1862-floor-gate.md`,
the dolt 1.86.2 version-floor gate):

1. Title: `# Release gate - <what> (<bead ids>)` and a `**Verdict:** PASS`
   line up top.
2. Branch + base ref with SHAs, and the exact commit stack reviewed
   (e.g. `c4cbec40d` feat, `6defe1dd3` test, gate commit, review fixup).
3. `## Review Beads Bundled In This PR` — table mapping review beads to
   verdicts and reviewer identities.
4. `## Criteria` — a five-row verdict table: review PASS present;
   acceptance criteria met; tests pass (with the literal commands run,
   e.g. `go test -run 'TestDoltVersionCheck|...' ./internal/doctor`);
   no high-severity review findings open; branch evidence matches the
   reviewed state.
5. `## Notes` — scope honesty, e.g. "the broader repository suite was not
   rerun; this gate records the scoped checks".

The gate file is committed **on the release branch itself** (it appears in
its own changed-files list), so the PASS evidence travels with the code it
gates. If you are asked to produce one: copy the structure of an existing
`ga-*-gate.md`, record verdicts you actually verified with commands you
actually ran, and never write PASS ahead of the evidence.

## Checklists

**Cutting a stable release (maintainer):**

- [ ] `main` green on `CI / required`; no open release-blocker beads without a PASS gate file in `release-gates/`
- [ ] `CHANGELOG.md` `[Unreleased]` section reflects the release content
- [ ] RC loop done: RC tag(s) cut, `rc-gate.yml` dispatched and green (tutorial goldens included)
- [ ] `grep '^replace' go.mod` prints nothing; `goreleaser check` passes
- [ ] `./scripts/bump-version.sh X.Y.Z --commit --tag --push` (external artifact: human approval required)
- [ ] Watch `release.yml`: GoReleaser, attestations, tap formula update all green
- [ ] Next day: `homebrew-tap-smoke` green

**Interpreting a red PR check:**

- [ ] Open the `CI / required` step summary table first — it names the failing job
- [ ] `skipped` on `cmd-gc-process` / `pack-gate` / `docker-session` / `k8s-session` / `openclaw-bridge` is normal (path-gated); `skipped` elsewhere is a failure
- [ ] Generated-artifact failures (`preflight-generated`, `dashboard`) → sibling skill `gc-generated-artifacts`
- [ ] Reproduce locally with the tier-correct make target (sibling `gc-build-verify`); CI's dolt/bd versions are the guard-tested `deps.env` pins — match them locally before blaming the code

## Provenance and maintenance

Sources: `RELEASING.md`, `Makefile`, `scripts/bump-version.sh`,
`.github/workflows/{ci,rc-gate,nightly,mac-regression,container-scan,release,dispatch-labeled-pr-suite,homebrew-tap-smoke,codeql,scorecard,ollama-acceptance-c}.yml`,
`.github/workflows/scripts/runner_policy.py`, `.github/blacksmith-allowlist.txt`,
`.trivyignore.yaml`, `.trivyignore-config`, `deps.env`, `renovate.json`,
`release-gates/*.md`, git tag history — verified against `origin/main` at
`f828bbe4b` on 2026-07-06 (an earlier draft carried facts from a stale
2026-05-11 checkout; corrected in review).
Background context from the fable-distillation discovery report (machine-local,
non-load-bearing). Authored under the provisional campaign answers of
2026-07-07 (fork-local placement, repo-portable wording); revisit after the
maintainer's real answers land.

Re-verification one-liners for everything that drifts. Run them against
`origin/main` (`git show origin/main:<path>`), not your possibly-stale
worktree:

```bash
# Pin sync guards (section 6): deps.env values + the tests that enforce them
git show origin/main:deps.env
go test ./scripts -run 'TestDoltVersionPins|TestBDVersionPins' -count=1

# ci-required semantics + allow_skipped set (section 3)
git show origin/main:.github/workflows/ci.yml | grep -n -A20 "ci-required:"

# Blacksmith allowlist (section 3)
git show origin/main:.github/blacksmith-allowlist.txt

# Trivy waiver expiry state (section 5)
git show origin/main:.trivyignore.yaml | grep -n "expired_at" && date -u +%F

# chaos tier still absent from CI (section 4)
git grep -l "chaos_dolt" origin/main -- .github/workflows/ || echo "still local-only"

# Cron schedule table (section 4)
git grep -n "cron:" origin/main -- .github/workflows/

# Release-gate inventory (section 7)
git ls-tree --name-only origin/main release-gates/ | wc -l

# Latest release shape (section 1)
git tag -l 'v*' | sort -V | tail -3
```
