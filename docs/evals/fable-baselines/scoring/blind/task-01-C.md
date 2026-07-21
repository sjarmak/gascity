# Gas City open-issue triage — 2026-07-06 snapshot

## Global caveat that bounds every "collision risk" call

The snapshot carries only `number`, `title`, `createdAt`, `labels`, and a
truncated `body`. It has **no assignee, no open/closed state, no linked-PR
field, and no comments**. So every judgment about "is a maintainer already on
this" is inferred from three weak signals only: (a) whether the issue is still
`status/needs-triage` vs already graded with `kind/*` + `priority/*` labels,
(b) filing date, and (c) issue shape. Concretely:

- `status/needs-triage` = maintainer has **not** scoped it → lower collision
  risk, but also higher chance it gets batch-closed as "operator config."
- A `priority/p1|p2` label = maintainer has **seen and ranked** it → higher
  chance it is already owned or has a branch in flight.
- Several bodies say "re-verified against 1.3.3" / "reproduced on main" — that
  raises my confidence the bug is _real_, not that it is _unclaimed_.

**Before starting any pick below, the one cheap check that changes everything is
`gh issue view <n> --json assignees,state` + a PR search for the issue number.**
I flag this per-pick rather than pretending the snapshot settles it.

One structural note: issues **3962–3973** (the 06:03–06:04Z cluster) are one
"18-item findings bundle" filed by a downstream operator. They are individually
real but were filed as a batch; a maintainer may triage the whole bundle in one
pass, so grabbing one mid-bundle carries a small risk of being reclassified.

---

## Classification (all 40 issues)

### GRAB NOW — self-contained, real, low collision

| #    | Title (short)                                                     | Why grabbable                                                                                                     | Confidence                                                                            |
| ---- | ----------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| 3962 | `gc init` doesn't detect Gemini CLI via Nix ($PATH)               | Textbook `exec.LookPath` fallback; exact repro; tiny blast radius                                                 | **High**                                                                              |
| 3944 | `formula cook --attach` drops rig context                         | Root cause at `cmd_formula.go:888-889` (empty `routedTo`); `gc sling` is the working oracle; reproduced on `main` | **High**                                                                              |
| 3966 | pack-import command groups return rc=0 on unknown subcommand      | Verified on unmodified 1.3.3; native surfaces already return rc=1 (reference behavior exists); dispatch-layer fix | **High**                                                                              |
| 3971 | `gc dashboard serve` binds all interfaces, no `--host`            | Security value (unauth LAN exposure); clear ask (default `127.0.0.1` + honest log line); small surface            | **High**                                                                              |
| 3965 | `gc config explain` omits idle-key provenance                     | Include already-resolved keys in explain output; trivial                                                          | **High** (low value)                                                                  |
| 3907 | doctor `rig-pack-coverage` false positive on same-name local pack | Localized to `checks_rig_coverage.go`; compare by `[pack].name` not abs dir; doctor-only blast radius             | **High**                                                                              |
| 3892 | control-dispatcher idle serve loop never quiesces                 | Clear ask (second backoff tier 5s→30s, reset on any drain); `workflowServeMaxIdleSleep` is the single knob        | **High**                                                                              |
| 3986 | `sling --on <v2-formula>` doesn't stamp `gc.source_bead_id`       | Non-graph `--on` path is the oracle; source bead leaks open forever                                               | **Med-High** — code point was truncated in my view; filed _today_ = highest collision |
| 3862 | hook projection blind-appends `ubs` PreToolUse every reconcile    | p1, strong evidence (5,760 dupes/1.4MB); fix = upsert/dedupe the projection                                       | **High on the bug; Med on collision** (p1 → likely owned)                             |

### GOOD CANDIDATE — valuable but needs scoping or carries a named risk

| #    | Title (short)                                                                       | Scoping need / risk                                                                                                                            |
| ---- | ----------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| 3977 | beads-library version exposed nowhere                                               | Decide _which_ surfaces (`--version` / doctor / the WARN); pick one, don't do all three speculatively                                          |
| 3976 | `gc start` regenerates launchd plist from ambient env (re-embeds creds, drops keys) | Security value, but body says 1.3.0 already shipped partial machinery — scope _what remains_; darwin-only                                      |
| 3975 | `gc session wake` silent no-op                                                      | Small, but "what to print (queued vs dropped vs asleep)" is a design choice; part of the bundle                                                |
| 3973 | discord intake request logging                                                      | Small feature in `bridge.py`/`service.log`; separate service, low reuse of core                                                                |
| 3970 | nudge machinery leaks an open P2 bead per nudge                                     | Two candidate fixes (stamp `last_attempt_at` vs TTL-close regardless); pick one; touches nudge lifecycle                                       |
| 3969 | `gc mail reply` counted but not listed by inbox                                     | Root cause named (`to_session_id` vs inbox filter identity) — verify the identity model before changing the write path                         |
| 3968 | pool worker can't claim work routed to POOL handle                                  | **Core claim path**; demand-count vs claim-key divergence is architectural — high value, real risk                                             |
| 3967 | no way to inspect assembled/rendered prompt                                         | Additive `gc agent render`; moderate; interacts with silent fragment-drop companion                                                            |
| 3974 | `pinned` column has no setter                                                       | **Cross-repo**: `bd pin` lands in `steveyegge/beads`, not gascity — confirm which repo owns the fix                                            |
| 3937 | mol-witness-patrol misses ephemeral wisps on DoltLite                               | Pack-formula fix (`--include-infra`); confirm the #3449 TierIssues contract before touching the template                                       |
| 3934 | `--no-supervisor` / isolated-start mode                                             | Real gap, but a new start mode is a design task, not a grab                                                                                    |
| 3929 | cascade-nudge dedup counts queued as delivered                                      | Core pack script; decide "record dedup only on real delivery" semantics                                                                        |
| 3928 | no session lifecycle events in stream                                               | Overlaps 3872/3972; scope which events + the start-pending time-box                                                                            |
| 3927 | `builtin:claude` hardcodes `--effort max`                                           | High cost value, but changing the default is a maintainer behavior call; the "agent `model` key silently dropped" half is a separable easy win |
| 3926 | order-tracking wisps unbounded; prune aborts on orphans                             | Clear asks (orphan-tolerant prune) but perf-critical reaper, wide blast radius                                                                 |
| 3914 | singleton pool + same-template named_session self-collision spam                    | Benign (log spam + per-tick write); fix = skip normalization when self-owned; delicate identity code                                           |
| 3898 | exec orders dispatch before pack staging completes                                  | p1 startup race; blast radius in supervisor start path — needs care                                                                            |
| 3891 | provider launch exec-wrapper for bwrap sandboxing                                   | Valuable (real store-destruction incident) but non-trivial provider feature                                                                    |
| 3887 | workspace-identity deprecation hint can break Discord                               | Split: "caveat the hint" is a near-grab; "sequence-aware `doctor --fix`" is harder                                                             |
| 3877 | rc-gate tutorial goldens break on CLI footer banner                                 | CI hardening (anchor capture to content); touches golden tests                                                                                 |
| 3868 | doctor dolt-backup check ignores external remotes                                   | Localized doctor enhancement; near-grabbable, moderate value                                                                                   |
| 3946 | Homebrew installs beads 1.1.0 vs required 1.0.4                                     | **Fix lives in the Homebrew tap formula (`depends_on "beads"` unversioned)** — likely a different repo than gascity core                       |

### INVESTIGATE — cannot classify without specific evidence

| #    | Title (short)                                       | Exact evidence needed                                                                                                                                                                                                   |
| ---- | --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 3972 | session event delivery lossy + silent               | Part (b) is already fixed upstream (body admits it). Need a fresh repro of the _remaining_ event-loss half on current `main`, and which delivery path drops it, before it's actionable.                                 |
| 3964 | control-dispatcher self-triggers singleton advisory | Is this a bug or working-as-designed? Body says drain-at-zero is _designed_ and cities can't silence it behavior-neutrally. Need a maintainer intent call: suppress the advisory for self-owned singletons, or wontfix. |
| 3869 | reaper closed-wisp purge DELETE fails on Dolt       | Reported on gc **1.2.1**. Need a repro on current gc + Dolt version and the exact SQL error — `DELETE ... NOT IN (subquery)` may be a Dolt dialect limitation, which changes the fix entirely.                          |
| 3924 | recover ~25% orchestration overhead                 | An analysis/profile, not a single fix. Need the _specific_ actionable item (the ~70–180s dispatch tax) isolated to one code path before it's startable; today it's diffuse.                                             |

### SKIP — with reason

| #    | Title (short)                                     | Reason                                                                                                                                  |
| ---- | ------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| 3925 | formula run profiling proposal                    | Design proposal / epic; maintainer-owned scope decision, not a contributor grab                                                         |
| 3872 | session lifecycle divergence family (5 incidents) | p1 epic bundling 5 incidents; architectural, maintainer-owned; pick a _child_ if anything                                               |
| 3879 | back-merge CHANGELOG 1.3.1–1.3.3 into main        | Maintainer release bookkeeping; high collision with the release process; the body already names the mechanical steps for the maintainer |
| 3878 | release/v1.3.0 deps.env stale `BD_VERSION=v1.0.5` | Release-branch-only; `main` already fixed via #3714; belongs to the next 1.3.x hotfix cut, maintainer-owned                             |

---

## Top 5 to start today (ranked)

Ranking favors: confirmed root cause, small blast radius, provable oracle, low
collision — over raw value. The two highest-_value_ items (3862, 3986) are held
just out of the top slots **only** for collision risk, and I say so.

### 1. #3944 — `formula cook --attach` drops rig context

- **First step:** open `cmd/gc/cmd_formula.go:888-889`; the body already
  pinpoints the empty `routedTo`/`sessionName` args to
  `DecorateGraphWorkflowRecipe`. Trace what `gc sling` passes at the equivalent
  call site (it works) and mirror that rig context into the cook path.
- **Blast radius:** the `--attach` graph.v2 decorate path only. The `gc sling`
  path is untouched, so the working path is unaffected — this is a narrowing,
  not a widening, change.
- **Test that proves it:** table test with a 2-rig fixture where the same bare
  agent name exists under both rigs; assert `cook --attach` resolves to
  `<invoking-rig>/<agent>` and no longer errors `unknown formulas v2 target`.
  Golden: identical resolution to `gc sling` on the same formula.
- **What makes this pick wrong:** if `sling` and `cook --attach` legitimately
  carry _different_ rig semantics (attach targets an existing bead that may
  belong to another rig), then copying sling's context is wrong and the real fix
  is "resolve from the attached bead's rig." Confirm which rig is authoritative
  before mirroring.

### 2. #3971 — `gc dashboard serve` binds all interfaces

- **First step:** find the `net.Listen` for the dashboard; add a `--host` flag
  defaulting to `127.0.0.1`, and change the listen log line to print the
  **actual** resolved bind address instead of a hardcoded `localhost`.
- **Blast radius:** dashboard serve command only. Default-to-localhost is a
  _tightening_ of exposure; the one behavior change is that operators relying on
  the old implicit LAN bind must now pass `--host 0.0.0.0` — call that out in the
  PR as an intentional security default.
- **Test that proves it:** start the server in-test with no flag, assert it
  answers on `127.0.0.1` and **refuses** on the LAN/`::1` address; with
  `--host 0.0.0.0`, assert it answers on all. Assert the log line string matches
  the real bind.
- **What makes this pick wrong:** if any shipped workflow (a pack, a doc, a
  demo) depends on the current LAN-visible default, flipping the default is a
  breaking change and the maintainer may prefer an opt-in `--host` with the old
  default kept. Low probability given the issue frames it as a security bug.

### 3. #3966 — pack-import groups swallow unknown subcommands with rc=0

- **First step:** in the pack-command dispatch layer
  (`cmd/gc/cmd_pack_commands.go` per issue #3898's cross-reference), make an
  unrecognized subcommand under an imported group print "unknown command" and
  exit 1 — matching the native `gc dolt-state <unknown>` → rc=1 behavior that
  already exists as the reference.
- **Blast radius:** the imported-group dispatch path. Risk: a script somewhere
  may rely on `gc <group>` (no subcommand) printing help at rc=0 — preserve the
  bare-group-prints-help case; only the _unknown-subcommand_ case flips to rc=1.
- **Test that proves it:** `gc <imported-group> <bogus>` exits 1 and prints
  "unknown command"; `gc <imported-group>` (bare) still exits 0 with help;
  regression-guard both.
- **What makes this pick wrong:** if the rc=0 is load-bearing for some existing
  pack's UX (group root == help by design), a maintainer may want the fix scoped
  to only the truly-unknown token. The test above already encodes that boundary,
  so the risk is low.

### 4. #3962 — `gc init` doesn't detect Gemini CLI installed via Nix

- **First step:** locate the provider-readiness preflight for Gemini; it checks
  hardcoded npm global dirs. Add an `exec.LookPath("gemini")` fallback (or make
  `$PATH` the primary resolution) before declaring "not installed."
- **Blast radius:** provider preflight detection only. Making detection _more_
  permissive can't break a currently-passing detection; worst case it accepts a
  binary that later fails to run, which surfaces as a normal runtime error.
- **Test that proves it:** put a fake `gemini` on `$PATH` outside the npm dirs;
  assert preflight passes. Keep a negative test: no `gemini` anywhere → still
  fails with the clear message. Do the same for any sibling provider that shares
  the hardcoded-path logic (fix the class, not the instance).
- **What makes this pick wrong:** if the npm-path check exists _deliberately_ to
  enforce a vetted install method (version pinning, provenance), a bare
  `LookPath` weakens that guarantee. Unlikely — but check whether the preflight
  also version-gates the binary before loosening it.

### 5. #3862 — hook projection blind-appends `ubs` PreToolUse every reconcile

- **First step (gating step):** `gh issue view 3862 --json assignees,state` and
  search PRs for "3862" / "hook projection." This is p1 with vivid evidence —
  **most likely of my picks to already be owned.** If clear, proceed.
- **Then:** change the config reconciler's hook projection from append to
  upsert — key each projected hook by a stable identity (command string / a
  managed-marker) and write it only if absent.
- **Blast radius:** the per-session config reconciler's `.claude/settings.json`
  projection — this runs every tick on every active session, so a mistake here
  is high-frequency. Mitigate: the change must be idempotent and must not drop a
  legitimately operator-added second hook.
- **Test that proves it:** run the projection N times against a fixture
  `settings.json`; assert `.hooks.PreToolUse` count is stable at 1 managed
  entry after N passes (not N entries); assert an unrelated operator hook
  survives untouched. Reproduces the 1→6→… regrowth as a failing test first.
- **What makes this pick wrong:** collision (p1, may have a branch already), and
  the upsert key must be chosen carefully — if two agents legitimately need two
  variants of the hook, a naive dedupe key erases one. Verify the intended
  cardinality before picking the key.

**Held just below the line on purpose:** **#3986** (source-bead leak on v2
sling) is arguably higher value than #4–#5 — a source bead that never closes is
real operational pain, and the non-graph path is a clean oracle. I ranked it
lower only because it was filed _today_ (highest chance the maintainer grabs it
first) and the "Code point" section was truncated in my view, so I can't yet
name the exact file to patch. If a 30-second assignee check comes back empty,
promote it above #4.

## Confidence summary

- **High confidence the bug/behavior is real:** 3944, 3966, 3962, 3971, 3907,
  3892, 3862, 3986 (all carry a named code point, a working oracle, or a
  re-verified-on-1.3.3/main repro).
- **Lower confidence — needs evidence before acting:** 3972, 3964, 3869, 3924
  (partial-fix, intent-call, stale-version, or diffuse-analysis respectively).
- **Confidence is _lowest_ precisely where the snapshot can't help:** every
  collision-risk call. The label + date heuristic is a proxy, not the
  assignee/PR data that would settle it. Treat the per-pick "what makes this
  wrong" lines as the real gate, not the ranking.
