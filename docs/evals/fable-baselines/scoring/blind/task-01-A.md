# Triage: gastownhall/gascity open issues (snapshot 2026-07-06)

40 issues total. Source note: most issues #3962 and below down through #3862 in this
range carry the marker "downstream Gas City deployment... operator-approved findings
bundle (18 items)" with a checked "searched existing issues, no duplicate found" box —
that's a meaningfully higher evidence bar than a typical drive-by report, and it shows in
how many of these have exact file:line root causes already. That raised my GRAB NOW rate
for this batch; it also means I found zero confident SKIPs — I'd rather mark something
INVESTIGATE than assert staleness/duplication I can't verify from the snapshot alone.

Confidence per bucket: GRAB NOW picks below are high confidence unless noted inline.
GOOD CANDIDATE and INVESTIGATE calls are medium confidence by construction — the whole
point of those buckets is that classification depends on something I can't see from the
issue text.

---

## GRAB NOW (self-contained, high value, low collision risk)

- **#3986** — `gc sling --on <v2-formula>` doesn't stamp `gc.source_bead_id`, so
  `workflow-finalize` can't close the source bead. Reporter already diagnosed that the
  legacy `--on` path stamps correctly and only the v2 graph branch drops it — this is
  "make the v2 branch match the legacy branch," not new design.
- **#3971** — `gc dashboard serve` binds all interfaces with a misleading "listening on
  localhost" log line, no `--host` flag to narrow it. Security-relevant (unauthenticated
  LAN-visible dashboard) but the ask is precisely scoped: add `--host` (default
  `127.0.0.1`), fix the log line to print the real bind address.
- **#3969** — `gc mail reply` messages are counted but not listed by `gc mail inbox`.
  Reporter already found the root cause: reply path assigns by `mail.to_session_id`,
  inbox listing filters by a different identity key. Align the two.
- **#3966** — pack-imported command groups (e.g. `gc dolt <unknown>`) swallow unknown
  subcommands as help+exit-0, while native command groups correctly exit 1. Contained to
  the pack-import dispatch layer; the fix is "use the same unknown-command path native
  groups use."
- **#3965** — `gc config explain` omits `idle_timeout`/`sleep_after_idle` from agent
  blocks even when resolved. Additive fix to the explain renderer's key coverage.
- **#3962** — `gc init --default-provider gemini` fails to detect a Nix-installed
  gemini CLI because the preflight only checks hardcoded npm paths, not `$PATH`. Textbook
  "add an `exec.LookPath` fallback" fix.
- **#3947** — never-run cron orders can't bootstrap because `checkCron`'s catch-up scan
  (added by #2721 for exactly this class of miss) is skipped when `lastRun` is zero.
  Already `kind/bug` + `priority/p2` (maintainer-triaged, not just `needs-triage`), with
  a live repro and a named regression source.
- **#3944** — `gc formula cook --attach` drops rig context at the decorate step,
  so rig-scoped graph.v2 targets that aren't city-unique fail to resolve. Reporter
  pinpoints the exact call (`decorateFormulaCookGraphV2Recipe`, `cmd/gc/cmd_formula.go:
888-889`, passing empty `routedTo`) and confirmed it reproduces on both 1.3.3 and
  current `main`. As precise as issue reports get.
- **#3937** — `mol-witness-patrol`'s reconciliation commands miss ephemeral wisps on
  DoltLite because `bd list` excludes `ephemeral=true` by default per #3449, and the
  formula's query doesn't pass `--include-infra`. If the fix is "add `--include-infra` to
  the formula's bd list command," this is a one-line pack/formula change; keeping it in
  GRAB NOW on that read, but see the "could make this wrong" note in the ranked list if
  it turns out to require reopening the ephemeral-visibility contract itself.
- **#3929** — `cascade-nudge-on-blocker-close`'s at-most-once dedup treats "nudge
  queued" as "nudge delivered," so a dead-lettered nudge permanently swallows the
  dependent's wake-up. Root cause traced to exact lines in both the shell order script
  and `cmd_nudge.go`.
- **#3927** — `builtin:claude` hardcodes `--effort max` with no override path (agent
  `args` are prepended, so last-flag-wins always favors the builtin's flag), and an
  agent-level `model` key is silently dropped. Measured cost: 40-56s of wasted model time
  per trivial orchestration turn. High value, precisely diagnosed, contained to the
  claude provider's launch-arg construction.
- **#3926** — order-tracking wisps accumulate unbounded (250k+ rows measured) because
  `gc order sweep-tracking`'s prune only deletes a trickle and aborts entirely on the
  first orphaned row. Reporter proved causality (dolt CPU 90-110%→0% on stop, spawn time
  85s→25-38s after a bulk purge). Fix: make the prune orphan-tolerant and raise its
  per-run delete ceiling.
- **#3914** — singleton pool + same-template `named_session` self-collision spams a
  deferral log line and writes to the store on every reconcile pass, forever (non-fatal
  but a standing store-load and log-noise cost). Root cause traced to
  `normalizeNonExpandingPoolSessionBeadForSelection`/`ensureSessionAliasAvailable`.
  Related to #3964 below — see cross-reference note.
- **#3907** — `gc doctor`'s `rig-pack-coverage` check false-positives when a rig
  intentionally replaces a pack with a same-named local copy, because the check compares
  exact absolute pack-dir paths instead of pack name. Root cause cited to
  `internal/doctor/checks_rig_coverage.go`.
- **#3892** — control-dispatcher's `--follow` serve loop idle backoff caps at 5s and
  never quiesces further, so a fully idle city pays ~12 `bd`-subprocess-forking sweeps/min
  per dispatcher forever. Ask is a second backoff tier (5s→~30s after N consecutive empty
  sweeps), building on an `idleSweeps` counter that already exists and already resets
  correctly on real events. `priority/p2`.
- **#3879** — back-merge the 1.3.1-1.3.3 CHANGELOG sections from `release/v1.3.0` into
  `main`; exact commit hashes given, direct precedent (#2932). Pure documentation,
  `priority/p3`. Only caveat: it targets a release branch, which some maintainers keep to
  themselves — flagged, not disqualifying.
- **#3878** — `release/v1.3.0`'s `deps.env` still pins the withdrawn `BD_VERSION=v1.0.5`;
  `main` already fixed this via #3714. One-line correction, `priority/p3`. Same
  release-branch caveat as #3879.
- **#3877** — rc-gate tutorial-golden peek assertions break when the Claude CLI shows a
  new footer banner that pushes expected content out of a fixed-height tmux capture.
  Confirmed environmental (installer/CLI drift, not the release diff) with a control run
  on unchanged code. Fix is test-infrastructure only (assert against transcript or a
  content-anchored capture instead of a fixed window) — zero risk to production code.
- **#3868** — `gc doctor`'s dolt-backup check doesn't recognize external Dolt backup
  remotes (self-managed accessory servers), producing false "no backup registered"
  warnings. Root cause cited to the specific checker function and its two local-only
  signals. `priority/p3`.
- **#3862** — the per-session config reconciler blind-appends the `ubs` PreToolUse hook
  on every reconcile tick with no dedup, so `.claude/settings.json` grows unbounded on
  long-lived sessions (5,760 identical entries / 1.4MB measured on one 5-day session).
  `priority/p1`, extremely well quantified, clear fix (upsert/dedupe instead of append).
- **#3975** — `gc session wake` is a silent no-op (rc 0, no output) when there are no
  wake reasons, despite documented behavior. Contained fix: report queued-vs-dropped
  outcome. Slight discount vs. the rest of this bucket because the issue also cites
  broader session-lifecycle introspection unreliability (`drain-check` rc) that could
  tempt scope creep — keep this pick narrowly to the wake-reporting fix itself.

## GOOD CANDIDATE (valuable, needs scoping or carries risk)

- **#3977** — version_compat gate is unobservable (linked beads version exposed
  nowhere: not `gc --version`, not doctor, not the WARN itself). Real value, but touches
  three separate surfaces — needs a decision on which surface(s) to land first.
- **#3976** — `gc start`/supervisor install regenerates the LaunchAgent plist from
  ambient shell env, re-embedding credential-shaped vars as plaintext and dropping
  operator-added keys (confirmed root cause of a recurring `TMUX_TMPDIR` regression).
  High value, genuinely security-sensitive — needs a designed distinction between
  "credential-shaped env var, don't copy" and "operator-declared key, always preserve,"
  not just a partial patch. macOS/launchd-specific, so verification needs a macOS host.
- **#3970** — nudge machinery leaks one open P2 bead per nudge (44/day observed) because
  delivery state is never written back and the sweep only closes nudges it considers
  delivered. Two fix directions offered by the reporter (record delivery state, or make
  the sweep close-past-TTL regardless of delivered-state) — that's a real design choice,
  and the issue flags a companion ask (#9/ga-21k) not present in this snapshot, so full
  context isn't available here.
- **#3968** — a pool worker can't claim work routed to its POOL handle because
  demand-counting and worker-claim use different identity lookups (instance vs. pool).
  Two fix directions proposed (match claim query to pool handle, or change dispatch to
  route to instance identity) — this is core dispatch/claim logic, so the choice matters
  and blast radius is city-wide across every pool-routed workflow.
- **#3967** — no way to inspect an agent's assembled/rendered prompt (`gc session peek`
  only shows the tmux pane). Clean value (would have caught the inert-fragment bug the
  issue cites), but likely requires extracting prompt-assembly from the session-spawn
  path so it can run without actually spawning a session — real refactor risk if the two
  are currently coupled.
- **#3964** — the bundled core `control-dispatcher` pack fires a permanent advisory
  against its own by-design drain-at-zero behavior, in every city, forever. The issue's
  own "non-asks" section says there's no way to silence it today without a real config
  change — the fix needs new logic to recognize "self-collision, benign" vs. a genuine
  conflict. Likely shares a root mechanism with #3914 (same identity-normalization code
  path, different triggering config) — worth reading both before picking either.
- **#3934** — a `--no-supervisor`/isolated-start mode for `gc start`, so a cutover/
  migration can rehearse the real start path without colliding with a live machine-wide
  supervisor (127.0.0.1:8372 bind is effectively an unrelocatable singleton today). Real
  feature work: new port/isolation model, decide scope of what runs vs. is skipped.
- **#3928** — no `session.*` events in the events stream, forcing readiness tooling to
  poll `tmux has-session` on a timer that can't distinguish "slow" from "wedged." Good
  value, but scope needs a decision (which lifecycle events: created/starting/ready/
  failed) and it overlaps meaningfully with the architectural question raised in #3872 —
  read that one first.
- **#3925** — proposal for formula-run profiling support, explicitly framed as "v0: a
  profiler pack, no core changes" plus a prioritized list of core enablers. This is a
  design proposal awaiting alignment on the pack-vs-core split, not a single patch.
  `kind/feature`.
- **#3898** — exec orders dispatch before pack staging completes after a supervisor
  start, causing a ~5-minute burst of spurious "unknown command"/missing-asset failures
  that self-recovers. `priority/p1`, root cause partially diagnosed (`registerPackCommands`
  fails silently, `quietLoadCityConfig` swallows "not found, skipping" for unstaged
  packs). The actual fix needs a readiness barrier — gate order/exec dispatch on a
  "packs staged" signal — which touches core startup sequencing and needs care to avoid
  introducing a new race. High value given the p1 label; scoping the gate mechanism is
  the real work.
- **#3891** — first-class launch exec-wrapper (bwrap/`unshare`) for fs-sandboxing
  spawned agents, motivated by a real incident (`rm -rf` on the Dolt store from a
  misbehaving pool agent under `--dangerously-skip-permissions`). Real security value,
  but it's a security-surface feature — wrapper contract, cross-platform story
  (bwrap is Linux-only), and interaction with existing `permissions.deny` all need
  design. This is also the kind of security-critical addition a maintainer may want to
  own directly rather than delegate.
- **#3887** — following the workspace-identity deprecation hint (by hand or via
  `gc doctor --fix`) can silently kill Discord delivery if installed packs predate
  site-binding support. Two asks offered: (1) caveat the warning text — trivial, could be
  pulled out as its own GRAB NOW slice — and (2) sequence-aware `doctor --fix` that
  detects pre-site-binding pack pins before stripping the field — real scope. Grabbing
  just (1) first is a reasonable way to de-risk this one.
- **#3872** — a P1 mega-issue documenting five separate session-lifecycle incidents
  (adoption alias, serve-loop store pinning, drift-relaunch losing priming, ghost session
  beads, scale-from-zero demand blindness) that the reporter argues share one root cause:
  durable state and runtime state can diverge with no reconciliation invariant forcing
  convergence. This is not a single fix — it's an architectural proposal. If picked up,
  the right first step is scoping down to incident #1 alone (adoption re-creating a
  singleton session bead without `alias`) as the smallest independently-testable slice,
  not attempting the general invariant in one pass. Given the cross-cutting nature and
  p1 label, this reads like something the maintainer would want to weigh in on before a
  contributor starts patching five paths independently.

## INVESTIGATE (cannot classify without more evidence)

- **#3974** — `pinned` column exists in the beads schema with no setter (`bd pin`,
  `--pinned` on create/update, or a `gc` equivalent). Evidence needed: whether `bd` (the
  CLI) lives in this repo or is vendored from the separate `steveyegge/beads` upstream —
  if the setter belongs in `bd` itself, this may be partly out of scope for a gascity-repo
  contribution and would need a companion issue upstream. Also need to check whether `gc`
  already has generic column-setter plumbing that could expose `pinned` without touching
  `bd` at all.
- **#3973** — discord intake service request logging has no timestamps; `bridge.py` logs
  nothing per-connection. Evidence needed: whether this Python-based intake service and
  `bridge.py` actually live in the gascity repo (vs. a separate pack/service repo) — the
  language (Python) stands out against an otherwise Go codebase, and I can't confirm
  location from the snapshot alone.
- **#3972** — session event delivery is lossy and silent. The reporter's own re-grade
  says part (b) (Enter-loss via tmux submit path) is already fixed upstream via
  `submitEnterAndConfirm`, folding into the ga-sgx upgrade track — not a new ask. Part (a)
  (genuine event-delivery loss, distinct from the tmux path) is the real remaining scope,
  but the "Effect" section describing it is truncated in this snapshot. Evidence needed:
  the full issue body, and confirmation of whether 1.3.3 already carries the
  `submitEnterAndConfirm` fix (the reporter flags this as an open question for AUR2).
- **#3946** — Homebrew tap pulls `beads` 1.1.0 while gc 1.3.x requires `bd` v1.0.4,
  silently disabling the native store. Evidence needed: which repository hosts the
  `gastownhall/gascity` Homebrew formula (likely a separate tap repo, not this codebase)
  — if so, the fix (pin/version the `beads` dependency in the formula) may not be
  actionable from a gascity-repo PR at all.
- **#3924** — measured profile of a 3h35m formula run finding ~25% overhead is
  orchestration, with a ~70-180s dispatch tax at every step transition. This reads as a
  measurement/investigation report; the visible text is cut off before it states the
  specific "platform-layer findings, each grounded in specific code paths and constants"
  that would make this actionable as a single fix. Evidence needed: the rest of the issue
  body, to know which of the (implied, not-yet-seen) fixes to target first. Companion
  proposal #3925 for tooling is separable and already captured above.
- **#3869** — reaper closed-wisp purge DELETE fails on the Dolt beads store. The
  reporter's own environment is `gc 1.2.1` / `bd 1.0.5` (withdrawn per #3878/#3946) /
  `dolt 2.1.4` — meaningfully behind the 1.3.2/1.3.3 + dolt 2.1.10 baseline the rest of
  this batch is filed against. Evidence needed: whether the specific `DELETE ... WHERE
status='closed' AND ... AND id NOT IN (...)` failure still reproduces on current
  versions before treating this as live rather than already resolved by a dolt/bd
  version bump.

## SKIP

None with enough evidence to call confidently. The closest candidates for a SKIP call —
#3869 (possibly stale versions) and #3946 (possibly wrong repo) — are marked INVESTIGATE
instead, because I'd be guessing at staleness/repo-location rather than verifying it.

---

## Top 5 to start today

**1. #3862 — dedupe the `ubs` PreToolUse hook projection (stop unbounded settings.json growth)**

- First step: read the per-session config reconciler's hook-projection step; change
  blind-append to an upsert keyed on hook content (or dedupe the array before write).
- Blast radius: the reconciler runs on every live session's `.claude/settings.json` on
  every tick — logically contained to one function, but touches the write path shared by
  every hook type, so the fix must not collapse legitimately-distinct hook entries, only
  true byte-identical duplicates.
- Test: run the reconcile loop N times against a fresh session and a simulated long-lived
  one; assert `.hooks.PreToolUse` count stays at 1 (or the correct fixed count) instead of
  growing; assert a second, genuinely distinct hook entry survives the dedupe.
- What could make this wrong: if some of the "duplicate" entries aren't actually
  byte-identical in every real deployment (only in the two the reporter checked), a naive
  content-hash dedupe could be too broad. Also worth checking whether this was already
  fixed upstream since discovery (2026-06-25) given the `priority/p1` label — a maintainer
  would likely have moved fast on unbounded growth.

**2. #3944 — pass real rig context through `gc formula cook --attach`'s graph.v2 decoration**

- First step: read `cmd/gc/cmd_formula.go:888-889` (`decorateFormulaCookGraphV2Recipe`)
  and `internal/graphroute/graphroute.go:490` (how `routingRigContext` is derived from
  `routedTo`); mirror whatever `gc sling` passes for the same decoration call.
- Blast radius: the formula-cook command's decoration step only; the decoration function
  itself is shared with `sling`, so passing a real context must be a no-op for
  already-working city-unique bare names, not just additive for the ambiguous case.
- Test: reproduce the multi-rig city from the issue (two rigs importing a pack that
  contributes the same bare agent name), run `gc formula cook --attach=<bead> <formula>`
  from within one rig, assert the step resolves to `<rig>/<name>` instead of erroring
  `unknown formulas v2 target`.
- What could make this wrong: if `cook --attach`'s notion of "current rig" isn't as
  well-defined as `sling`'s (e.g., cook can be invoked from outside any rig directory),
  naively threading "current rig" through could resolve to the wrong rig rather than
  correctly failing closed.

**3. #3947 — fix cron catch-up bootstrap when `lastRun` is zero**

- First step: read `checkCron`'s catch-up-scan logic (the code added by #2721); add
  handling for the zero-`lastRun` case so a fresh order's first eligible window is
  treated as due, instead of requiring exact-minute controller-tick luck.
- Blast radius: cron order matching is evaluated for every cron-scheduled order in every
  city on every controller tick — must not cause double-fire, and must not make orders
  fire immediately on install if that's semantically wrong for some order types.
- Test: install a fresh narrow-window (e.g., single daily-minute) cron order, let
  controller ticks pass its first scheduled window without exact-minute coincidence,
  assert `gc order history` shows a fire and `lastRun` becomes non-zero without a manual
  `gc order run`.
- What could make this wrong: ambiguity in what "catch up" means when multiple windows
  have already been missed since install (e.g., installed 3 days ago) — firing once vs.
  firing for every missed window needs a decision that matches #2721's original intent.

**4. #3892 — add a second idle-backoff tier to the control-dispatcher serve loop**

- First step: read the `workflowServeMaxIdleSleep` cap and the existing `idleSweeps`
  counter; extend the sleep ceiling from 5s to ~30s after N consecutive empty sweeps,
  keeping the existing reset-on-any-event behavior untouched.
- Blast radius: idle-loop scheduling only inside the control-dispatcher serve loop — does
  not touch work detection or event handling, since `idleSweeps` already resets correctly.
- Test: run a fully idle city for a sustained window; measure serve-trace events/min per
  dispatcher before (~12x/min per the issue) and after (should drop once the extended cap
  engages); separately confirm a real work item introduced during the idle window is still
  picked up within the original ≤5s latency, proving the reset path still works.
- What could make this wrong: the 5s cap exists specifically because raw `bd` writes
  publish no city event — if such writes happen more often in some deployments than the
  reporter's idle test captured, extending the cap could introduce a real latency
  regression for that class of write that this issue's evidence doesn't cover.

**5. #3966 — make pack-imported command groups exit non-zero on unknown subcommands**

- First step: locate the pack-import command dispatch layer (distinct from native command
  groups, which already correctly exit 1) and route unknown subcommands through the same
  "unknown command" error path native groups use, instead of falling through to help+exit-0.
- Blast radius: only pack-imported command groups' unknown-subcommand handling; valid
  subcommands and normal no-args help behavior should be unaffected.
- Test: run `gc dolt <nonexistent-subcommand>`, assert non-zero exit and an "unknown
  command" message matching `gc dolt-state <unknown>`'s existing behavior; regression-check
  that `gc dolt` with no args (or a valid subcommand) still behaves as before.
- What could make this wrong: if any bundled pack's own scripts or orders call an
  ambiguous/removed subcommand defensively and rely on the current rc=0 fallthrough not
  failing a pipeline — low likelihood, but worth a quick grep across bundled pack
  scripts/orders before flipping this exit code globally.
