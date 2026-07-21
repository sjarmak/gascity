# Gas City Issue Triage — 2026-07-06 snapshot (40 open issues)

Input: frozen snapshot only, no live `gh`/Sourcegraph/Dolt access. Applying the
gascity-triage decision procedure (Step Zero → Q0-Q4 table → confidence rules →
collision checks → ranking) issue-by-issue.

## Systemic blind spots (stated once, discounting every verdict below)

- **No PR/assignee/comment data at all.** Every "competing work" or
  "maintainer-ambivalent comment" (MA gate) judgment is unconditionally
  **unchecked**, not "clear" — the snapshot cannot show either. Every Tier 1/2
  entry's collision risk is inferred from labels + issue age only.
- **No design-doc access.** The DD (design-doc conflict) gate is likewise
  unconditionally **unchecked** — I looked for textual self-contradiction
  against known precedent inside the issue bodies themselves, not against
  `engdocs/design/*.md`.
- **Several issue bodies are truncated mid-sentence** in the raw JSON (3976,
  3972, 3986, 3872, 3898, 3887 all cut off before their conclusion). Any
  classification of these carries an explicit "re-read full body" first step,
  not a confident verdict.
- **No cross-repo access.** Where ownership plausibly sits in a sibling repo
  (`gastownhall/beads`, a packs repo, a homebrew tap), I could not confirm —
  flagged per-issue as Tier 3 or as a named Tier 1/2 risk rather than guessed.

## Structural observations (apply before any per-issue verdict)

1. **18-item batch, same reporter, same morning.** #3964–#3977 (14 of the
   claimed 18 are in this snapshot) were filed 2026-07-06 06:03:30–06:04:22Z,
   identical "Gas City version / Environment" boilerplate, from one downstream
   operator's "operator-approved findings bundle." Treat this cluster as one
   shared-provenance family: the same environment (gc 1.3.2, re-verified
   against unmodified 1.3.3 in several) backs all of them, so an environment
   doubt in one member is a doubt worth checking in siblings too.
2. **Umbrella already split: #3924 → #3925, #3929.** #3924 is a measurement
   report ("3h35m profile"), not itself an actionable ask; its two concrete
   children are already filed separately. #3924 is Tier 4 (umbrella); work the
   children.
3. **Likely duplicate: #3914 and #3964.** Both describe the identical
   mechanism (`normalizeNonExpandingPoolSessionBeadForSelection` /
   `ensureSessionAliasAvailable` self-collision on a singleton pool sharing a
   template with a `[[named_session]]`) and both cite the same evidence shape
   ("~168–267 lines per run," `pool_alias_conflict_count`). #3914 is two days
   older (2026-07-04 vs 2026-07-06) — treat it as canonical, #3964 as the
   duplicate, pending a live diff I can't run here.
4. **Beads-version-pairing cluster:** #3878 (deps.env stale BD_VERSION),
   #3946 (Homebrew formula pulls unversioned `beads`), #3977 (version_compat
   gate unobservable) are the same underlying pain (bd/gc version pairing)
   split across ownership boundaries — two are release/packaging (Tier 4),
   one is a genuine gc-core observability feature (Tier 2).
5. **Wisp/order-lifecycle-hygiene cluster:** #3926 (tracking-wisp prune
   aborts on orphan), #3970 (nudge dedup leaks open beads), #3869 (reaper
   closed-wisp purge DELETE fails, filed against a much older 1.2.1) are three
   _distinct_ mechanisms in the same broad subsystem — no shared fix, but
   worth knowing before touching more than one.
6. **Formula cook / graph.v2 decorate-step cluster:** #3944 (rig context
   dropped in `cook --attach`) and #3986 (`gc.source_bead_id` not stamped in
   `sling --on <v2-formula>`) both live in the graph.v2 decorate/attach code
   path but are different specific defects — same neighborhood, coordinate if
   picking both.

## Bucket counts (40 total, sums to input)

| Tier               | Count |
| ------------------ | ----- |
| 1 — GRAB NOW       | 14    |
| 2 — GOOD CANDIDATE | 15    |
| 3 — INVESTIGATE    | 6     |
| 4 — SKIP           | 5     |

---

## Tier 1 — GRAB NOW

**#3944** — `gc formula cook --attach` drops rig context (`cmd/gc/cmd_formula.go:888-889` passes empty `routedTo` → empty `routingRigContext` at `internal/graphroute/graphroute.go:490`; reproduced on unmodified `main`, byte-identical function). Single fix: thread real rig context through like `gc sling` already does. Confidence: **high** — mechanism pinned to two exact file:line citations, cross-verified on current main. Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3966** — pack-imported command groups swallow unknown subcommands at exit 0, verified on a hash-checked unmodified 1.3.3 binary, with a clean native-vs-imported behavioral contrast (`gc dolt-state <unknown>` → rc=1; `gc dolt <unknown>` → rc=0). Single fix: align the shared pack-import dispatch layer's unknown-subcommand handling with native groups. Blast radius is the shared dispatcher (used by every imported pack), but the change only affects a currently-broken error path. Confidence: **high** — self-verified repro on a clean release binary, precise before/after contrast. Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3969** — `gc mail reply` messages counted-but-unlisted: root cause explicitly named (reply path assigns by `mail.to_session_id`, inbox listing filters by a different identity field), reproduced under a controlled seed/reply test. Single fix: align the read-path filter with the write-path identity key. Confidence: **high** — mechanism stated in mechanism language, not impact language, plus a controlled repro. Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3892** — control-dispatcher `--follow` serve loop never quiesces past a 5s idle-backoff cap (`workflowServeMaxIdleSleep`), measured at ~50 serve events/min/dispatcher on a fully idle city. Single, already-sketched fix: second backoff tier (5s→~30s) after N empty sweeps, reusing the existing `idleSweeps` reset-on-activity counter. Confidence: **high** — named constant + named counter, reporter already reasoned through the one acceptable latency tradeoff. Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3907** — `gc doctor` rig-pack-coverage false positive for same-name local pack replacements (`internal/doctor/checks_rig_coverage.go`, `rigHasPackDir` does exact-path comparison instead of matching by `[pack].name`). Pure diagnostic — cannot affect runtime behavior even if the fix is imperfect. Confidence: **high** — exact file + function cited, safest possible blast radius. Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3926** — order-tracking wisp prune aborts on the first `delete bead: <id>: bead not found` orphan instead of skipping it, so the prune only ever removes a trickle against a continuous mint rate (measured 250k+ rows, idle dolt at 90–110% CPU, session spawns ~85s, all confirmed causally via supervisor-stop and bulk-purge experiments). Single fix: make the per-row delete loop orphan-tolerant. Confidence: **high** — exact error string, isolated causal proof, fix strictly narrows an existing failure mode. Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3862** — per-session config reconciler blind-appends the `ubs` `PreToolUse` hook every reconcile tick with no dedup (measured 5,760 duplicate entries / 1.4MB on one 5-day session, ~1 new duplicate every few seconds on active sessions). Single fix: make hook projection idempotent (upsert, not append). Confidence: **high** — exact duplicate content quoted, clean before/after measurement. Note: already labeled `priority/p1` (5–6 days old, borderline the "fresh-P1" 7-day collision window) — state "check open PRs first" as a precondition. Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3927** — `builtin:claude` hardcodes `--effort max`, agent-level `args` are prepended (so last-flag-wins always favors the builtin), settings-file `effort` key is honored but overridden by the flag, and agent `model` key is silently dropped. Mechanism proven with measured token counts under every flag ordering (not inference). Primary fix (append builtin flags before agent args, or drop the hardcode) is single-shaped and scoped to one provider's launch-assembly code. Confidence: **high** on mechanism; **medium-high** on this exact fix shape (see disconfirmer in ranked picks below — a "safety floor" alternative reading exists). Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3914** — singleton pool (`max=1`) sharing a template with a `[[named_session]]` causes `normalizeNonExpandingPoolSessionBeadForSelection` to permanently defer (its own alias is reserved by its own named-session identity), spamming a deferral log line plus a `beads.Update` (`pool_alias_conflict*`) every reconcile pass, forever — non-fatal but log-spam + store-write cost on every tick. Single fix: detect "reserved by my own configured identity" as benign and skip the deferral branch for that specific case. Confidence: **high** — named function, named fields, clear repro numbers. Gates: [DD n/a · AU clear · MA n/a · PD clear]. Cross-reference: **duplicate #3964** should close/link once confirmed; adjacent (not identical) to item 1 of #3872's umbrella (adoption path skipping alias stamping entirely — a different alias bug in the same subsystem).

**#3877** — rc-gate tutorial-golden peek assertion (`tutorial03_test.go:229`) breaks when a new claude-CLI footer banner pushes the expected "reviewer"/"codex" line out of a fixed-height tmux capture; confirmed environmental (identical setup scripts across a passing 1.3.2 gate and failing 1.3.3 gate; a separate live-transport test failed the same day on unchanged code). Single fix direction: anchor the assertion to session-transcript content instead of a fixed-height live capture. Confidence: **high** — exact test+line, zero production blast radius (test-only). Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3971** — `gc dashboard serve` binds the wildcard interface with a log line that falsely claims `localhost`, and there's no `--host`/`--bind` flag to narrow it. Single fix: add `--host` (default `127.0.0.1`) and correct the log line. Confidence: **medium-high** — mechanism well-understood and the command surface is small, but no file:line is cited in the issue itself (not literally pinned, only "trivially locatable"). Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3965** — `gc config explain` omits `idle_timeout`/`sleep_after_idle` from resolved agent-key output even when both are set. Single fix: include all resolved keys in the explain renderer. Confidence: **medium-high** — mechanism inferred (a keylist the renderer iterates is almost certainly incomplete) rather than cited to a line; scope is unambiguous and tiny (pure observability, no runtime change). Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3962** — `gc init --default-provider gemini` fails to detect a Nix-installed Gemini CLI because provider-readiness preflight checks hardcoded npm install paths instead of `$PATH`/`exec.LookPath`. Single fix, isolated to the Gemini provider's own preflight check (siloed per-provider, per #3927's finding that provider checks don't share code). Confidence: **medium-high** — mechanism plausible and well-described by the reporter, not confirmed against source. Gates: [DD n/a · AU clear · MA n/a · PD clear].

**#3887 (sliced)** — the workspace-identity deprecation hint can silently break Discord delivery if followed on a city with packs pinned before site-binding support (real 2-day incident cited). **Slicing the standalone-safe ask**: add a one-line caveat to the existing warning text (exact current text is quoted verbatim in the issue) naming the pre-site-binding-pack risk. This slice is single-line, zero blast radius beyond that message, and fully addresses the acute "silent break" risk. Confidence: **high** on this slice. The second ask in the same issue (a pack-revision-aware `doctor --fix` sequencing check) is scoped OUT here — see Tier 2 below. Gates: [DD n/a · AU clear · MA n/a · PD clear].

---

## Tier 2 — GOOD CANDIDATE

**#3968** — pool worker can't claim work routed to its pool handle (demand-counting keys on the pool handle, claim keys on instance identity). **Risk/fork:** two viable fixes named in the issue itself — (a) widen the worker's claim query to match its pool handle, or (b) make order/cron dispatch convoy-wrap so a fresh convoy worker claims it (as `gc sling` already does implicitly). This is the calibration worked example in the skill itself; classification held. Confidence: **medium** — mechanism characterized precisely, but fix shape is an open fork.

**#3986** — `sling <target> <bead> --on <v2-formula>` never stamps `gc.source_bead_id`, so `workflow-finalize` no-ops and the source bead never closes. **Risk:** the exact stamp call site in the v2 graph decorate path isn't visible (body truncated before the "Code point" section) — a locate judgment remains before the fix is scoped, even though the fix direction (mirror the legacy `--on` path's stamp) looks singular. Confidence: **medium-high** on direction, **medium** on scope pending the missing code pointer.

**#3977** — beads-library version exposed nowhere (`gc --version`, doctor, the WARN itself). **Risk/decision:** which surface(s) to add it to (version output format, doctor check, WARN text) is an open, bounded-but-real presentation decision — a feature, not a pinned bug. Confidence: **medium** — direction (expose it somewhere) is clear, surface choice isn't.

**#3975** — `gc session wake` is a silent no-op with no outcome reporting when there are no wake reasons. **Risk:** the issue bundles a second, related ask (`gc runtime drain-check` reliability) that should be sliced off rather than folded into one PR; the wake-reporting half alone is well-scoped. Confidence: **medium-high** on classification, **medium** on scope (recommend slicing to just `gc session wake` outcome reporting).

**#3970** — nudge machinery leaks an open P2 bead per nudge because `sweep-nudge-mail`'s TTL-close logic never sees undelivered-state nudges. **Risk/fork:** the issue itself poses an either/or — (a) record delivery state on the nudge bead, or (b) force-close past `expires_at` regardless of delivered-state — a genuine, un-resolved design choice, plus an unverifiable cross-reference to a companion bug (#9/ga-21k, not in this snapshot). Confidence: **medium**.

**#3967** — no way to inspect an agent's assembled/rendered system prompt. **Risk:** two CLI-surface options proposed (`gc agent render <name>` vs `gc session new --print-prompt`) is a bounded choice, but whether the existing prompt-assembly function can be called side-effect-free without spawning a session is unconfirmed — a real locate/wiring risk, not just naming. Confidence: **medium-high** on value/classification, **medium** on scope.

**#3947** — never-run cron orders can't bootstrap because the catch-up scan (from #2721) is skipped when `lastRun` is zero. **Risk/decision:** what "catch-up" should mean for a never-run order is a genuine semantic question (catch up to install time? just check the current window?) — not a locate problem but an actual design call, and this issue already carries maintainer-shaped labels (`kind/bug`, `priority/p2`, not "needs-triage"), raising collision risk. Confidence: **medium**.

**#3937 (sliced narrowly)** — `mol-witness-patrol`'s reconciliation query silently matches zero rows because patrol wisps are `ephemeral=true` and `bd list` excludes those by default per #3449's contract; the packaged command doesn't pass `--include-infra`. The one-line packaging fix (add the flag to the documented command) is mechanism-pinned and safe, but I can't confirm from this snapshot whether `packs/gastown/...` lives in `gastownhall/gascity` itself or a sibling packs repo — **Risk/decision:** confirm repo ownership before treating as grab-able here; if confirmed in-repo, this demotes cleanly to Tier 1. A deeper open question (should ephemeral-default-exclusion apply to assignee-scoped queries at all?) is separate and out of scope for the narrow fix. Confidence: **medium-high** on mechanism, **medium** on repo ownership.

**#3934** — `--no-supervisor`/isolated-start mode for `gc start`. **Risk/decision:** the issue itself proposes three different interface shapes (`--no-supervisor`, `--isolated`, `GC_SUPERVISOR=none`) and touches core startup/port-binding/config-resolution — a real product-shape decision, not a bounded one-PR fix, with above-average blast radius on sensitive startup code. Confidence: **medium**.

**#3929** — cascade-nudge dedup marks a `(blocker, dependent)` pair as "nudged" on command exit 0, which includes "queued," so a later dead-lettered nudge permanently swallows the wake-up. Exceptionally precise mechanism (four file:line citations across two files, split cleanly out of #3924). **Risk:** correctly fixing this likely needs the nudge pipeline to expose its _terminal_ delivery outcome back to the dedup script — a new query capability may not exist yet, which is more than a script edit. Confidence: **medium-high** on classification, **medium** on fix scope (script-only vs. needs a new core capability).

**#3928** — no session-lifecycle events in the events stream, forcing readiness tooling to poll. **Risk/decision:** bundles at least three asks of different weight (emit lifecycle events — a real instrumentation design task; time-box `start-pending` — depends on external #2895; document pin-vs-wake semantics — docs-only) plus a smaller, cleanly separable ask (`--payload-match` should support top-level `subject`). Needs slicing before any single PR is realistic. Confidence: **medium**.

**#3925** — formula-run profiling proposal (a new "profiler" pack, v0 explicitly scoped to zero core changes, plus a short list of core enablers). **Risk/decision:** even though the author already minimized core-change surface, accepting a new pack/tooling paradigm and choosing which core enablers matter is a maintainer product call, not a single PR. Confidence: **medium**.

**#3898** — exec orders dispatch before pack staging completes after supervisor start (5-minute failure window, self-recovers). Mechanism partially pinned (`registerPackCommands`, `quietLoadCityConfig`, `citylayout.SystemPacksRoot` all named). **Risk/decision:** real fork between gating order dispatch on staging completion vs. making order execution retry-tolerant during the startup window — a design call on sensitive, already `priority/p1`-labeled startup-sequencing code (elevated collision risk: maintainer-active area). Confidence: **medium**.

**#3891** — first-class launch exec-wrapper for fs-sandboxing spawned agents (motivated by a real `rm -rf .beads/dolt/*` destruction incident). **Risk/decision:** config-schema naming and provider scope (builtin:claude only vs. all runtimes in v1) is an open product decision; severity of the motivating incident doesn't override that this is an unscoped feature, not a pinned fix. Confidence: **medium**.

**#3868** — `gc doctor` dolt-backup check doesn't recognize externally-managed Dolt backup remotes, firing false warnings. **Risk/fork:** the issue proposes two genuinely different fixes — live-probe the external server's registered remotes (more correct, more complex) vs. suppress the local-backup warning entirely for external endpoints (simpler, less verification value) — a real design choice, not an implementation detail. Confidence: **medium**.

---

## Tier 3 — INVESTIGATE

**#3976** — plist credential re-embedding / operator-key dropping on every `gc start`. **Self-invalidation fires**: the body contains an explicit "RE-GRADE 2026-07-06" walking back part of the original claim ("upstream already shipped partial machinery in 1.3.0"), and the full re-grade text is truncated in this snapshot. Missing artifact: the untruncated re-grade paragraph plus a fresh repro of the credential-copy behavior against a clean, unmodified 1.3.3 binary run from a scrubbed shell. Action: re-read the full issue on GitHub; re-run the repro. Resolution paths: if credential re-embedding still reproduces on 1.3.3 → likely Tier 1/2 security fix (severity doesn't grant Tier 1 on its own — mechanism still needs confirming); if the re-grade shows only the operator-key-dropping half remains → re-scope to that narrower ask, which may itself need a merge-strategy design decision (Tier 2) or be trivially "preserve unknown keys" (Tier 1).

**#3974** — `pinned` column exists in the beads schema with no setter (no `bd pin`, no `--pinned` flag). Missing artifact: confirmation of which repository owns the fix — `bd create --pinned` most plausibly belongs in `gastownhall/beads`, not `gascity`, but "gc beads surface" in the issue's own "Where" line leaves open a gc-side passthrough. Action: check the beads repo for an existing pin-setter or open issue there. Resolution paths: if beads-repo-owned → Tier 4 for gascity (re-file there); if gc needs its own wrapper flag independent of the beads-side change → Tier 1/2 here.

**#3973** — discord intake service has no timestamped request logging (forensics-only gap, no live incident, explicit "no local mitigation... forensic-only"). Missing artifact: confirmation of which repository hosts `bridge.py`/the discord intake service (gascity core vs. a packs repo vs. a downstream-operator's own service). Action: locate the service's source tree. Resolution: if in-repo and contributor-shaped → likely Tier 1 (trivial logging addition, no design fork); if it's a downstream operator's own service or a separate packs repo → Tier 4 (wrong repo).

**#3972** — session event delivery lossy/silent (gc-core + discord launcher). The reporter's own RE-GRADE already slices out and withdraws the "Enter-loss" half ("ALREADY FIXED upstream — do NOT file it"), which is exactly the self-invalidation pattern to respect. The remaining "genuine" half (event delivery) has its root-cause text truncated before any mechanism is shown. Missing artifact: the untruncated description of the event-loss mechanism, plus confirmation it still reproduces on current main (only the _other_, already-fixed half was checked against 1.3.2/upstream). Action: read the full issue; re-run event-delivery repro on unmodified 1.3.3. Resolution: mechanism-pinned remainder → Tier 1/2; if it too turns out already-fixed upstream → Tier 4.

**#3872** — five distinct session-lifecycle durable/runtime-state divergence incidents bundled into one P1 issue, NOT yet split into child issues (unlike the #3924 family). Only 2 of the 5 table rows are visible in full in this snapshot; items 3–5 (drift-relaunch losing priming, ghost session beads, scale-from-zero demand blindness) are cut off. Missing artifact: full text of items 3–5. Action: read the full issue and determine whether each of the 5 rows should become its own issue (the pattern already demonstrated elsewhere in this same snapshot, #3924→#3925/#3929, suggests yes). Resolution: items 1–2, as visible, already read as independently mechanism-pinned (item 1: adoption path skips alias stamping; item 2: dispatcher serve-subprocess doesn't apply site bindings, hardcodes `BEADS_DIR` to city store) and could plausibly promote to Tier 1/2 once split out; if instead all 5 trace to one shared reconciliation-invariant fix, it becomes a single larger Tier 2 architectural item needing maintainer design buy-in.

**#3869** — reaper closed-wisp purge DELETE fails on current Dolt beads store. **Staleness fires**: filed against gc **1.2.1** / bd 1.0.5 / dolt 2.1.4 — markedly older than the rest of this snapshot's 1.3.2/1.3.3 cluster, with no re-verification against current main stated (unlike several siblings in this same snapshot that explicitly did re-check). #3926, in this same snapshot, independently found a related-but-distinct reaper/prune defect on 1.3.2 — worth checking whether whatever changed between 1.2.1 and 1.3.2/1.3.3 already touched this. Missing artifact: a repro of `mol-dog-reaper`'s Step 2 purge against a current 1.3.3/main build. Action: re-run the exact quoted DELETE query against a current build. Resolution: if it still fails identically → Tier 1 (the failing SQL is quoted verbatim, mechanism would be well-pinned); if it no longer reproduces → close as stale/already-fixed.

---

## Tier 4 — SKIP

**#3964** — **duplicate-of #3914** (identical self-collision mechanism, identical evidence numbers, filed 2 days later). Work #3914, close/link #3964 once confirmed.

**#3946** — **maintainer-owned** (Homebrew formula's unversioned `depends_on "beads"` — packaging/release infra, likely in a tap the maintainer controls; per the skill's own hard rule, "one-line fix in maintainer-owned release/packaging infra is still Tier 4" regardless of how easy the fix looks).

**#3924** — **umbrella**, children already split into #3925 (tooling proposal) and #3929 (dedup bug). Work the children, not this issue.

**#3879** — **maintainer-owned / deferred** (CHANGELOG back-merge across release branches, self-filed via automation, explicit existing precedent of the maintainer handling this exact pattern before via #2932).

**#3878** — **maintainer-owned / deferred** (release-branch `deps.env` staleness; author's own text explicitly frames it as "if another 1.3.x hotfix happens, consider..." — a low-urgency future note, not an active ask, and release-branch dependency pinning is maintainer process).

---

## Confidence anti-collapse check

Histogram across all 40: high (16), medium-high (11), medium (11), N/A/Tier-3 (6). Values vary and track real evidence differences — the "high" cluster is exclusively file:line-or-named-function mechanism pins with clean repros (#3944, #3966, #3969, #3892, #3907, #3926, #3862, #3877, #3914); "medium-high" marks a clear direction with exactly one open locate/scope judgment; "medium" marks either an explicit two-option fork or a genuinely bundled multi-ask issue.

## Collision checks

- **Fresh-P1 rule:** no Tier 1/2 issue was filed within the last ~7 days _and_ carries a maintainer-set P0/P1 label (the 06:03 batch is fresh but carries only `status/needs-triage`, i.e., not yet maintainer-reviewed). The two closest to the window are #3862 (`priority/p1`, 5–6 days old) and #3898 (`priority/p1`, 4 days old, already correctly labeled) — both carry an explicit "check open PRs first" precondition above.
- **Maintainer-active families:** startup/supervisor sequencing (#3898) and cron/order semantics (#3947, already `kind/bug`+`priority/p2` rather than needs-triage) show signs of prior maintainer attention — elevated collision risk noted in their entries.
- **List-level caveat:** this entire ranking is conditional on a collision sweep this snapshot cannot perform. The true first action for the whole list, before starting _any_ pick, is `gh pr list --search '<issue-number>'` plus a timeline check for cross-referenced PRs on every Tier 1/2 entry — none of that was possible here.

---

## Top 5 to start today

Ranked by actionability gradient (mechanism-pinned-to-code > one-bounded-decision > small-and-safe), diversified across five non-overlapping subsystems so none of these five need cross-referencing against each other.

1. **#3877 — rc-gate tutorial-golden peek assertion breaks on CLI footer banners.**
   - First step: open `tutorial03_test.go:229` and read the exact capture/assertion helper `TestTutorial03Sessions/gc_session_peek_mc` calls, to see whether it takes a raw fixed-height `tmux capture-pane` or has any content-anchored option available.
   - Blast radius: the tutorial-golden test assertion helpers only. Untouched: the real `gc session peek` command, the claude CLI, install scripts — no production code changes at all.
   - Proving test: replay the exact failing capture with the CLI footer banner text injected ahead of the `You are 'reviewer'.` line, same terminal dimensions as the original failure, and assert the hardened assertion PASSES; separately assert it still correctly FAILS when "reviewer"/"codex" is genuinely absent from the transcript, so the fix doesn't collapse into an always-pass tautology.
   - Disconfirmer: the same day, an unrelated live-transport test (`TestPhase2WorkerCoreRealTransportProof`) also failed on unchanged code, and nightly live-model tiers have failed since 06-25 — this could be a symptom of a broader claude-CLI/install-version drift in CI, not a pure assertion-height bug. If so, hardening only the assertion masks a real environment-pinning gap rather than fixing it.

2. **#3926 — order-tracking wisp prune aborts on the first orphaned row.**
   - First step: locate `gc order sweep-tracking`'s prune delete-loop and the exact point that raises `delete bead: <id>: bead not found`.
   - Blast radius: the `sweep-tracking` prune loop only. Untouched: wisp minting rate, `mol-dog-reaper`'s own purge (#3869), `sweep-nudge-mail` (#3970) — distinct code paths in the same subsystem, not touched by this fix.
   - Proving test: fixture DB with N normal closed trackable wisps plus at least one orphaned row (present in `wisps`, missing matching `issues`); run the prune; assert all N normal rows are deleted (not a trickle) AND the orphan is skipped/logged rather than aborting the sweep for the remaining rows.
   - Disconfirmer: the orphan rows might be a symptom of an upstream write-path bug that leaves broken referential integrity in the first place — tolerating orphans in the pruner would hide that defect rather than fix it, and the real fix might belong in whatever writes `wisps`/`wisp_events`/`wisp_labels`.

3. **#3862 — hook-projection blind-append bloats `.claude/settings.json` unboundedly.**
   - First step: locate the per-session config reconciler's hook-projection function that writes the `ubs` `PreToolUse` entry on every reconcile tick.
   - Blast radius: the hook-projection function for all packs/hooks projected this way (check whether others share the pattern). Untouched: the `ubs` hook's own runtime behavior, and any hand-authored (non-projected) settings entries.
   - Proving test: fresh session (1 entry), force 5 reconcile ticks; assert `.hooks.PreToolUse` stays at exactly 1 entry after all 5 (not 5+); assert a deliberately idle session also stays at 1 (already-working baseline, don't break it).
   - Disconfirmer: if the reconciler's intended design is "replace the whole hooks array from pack config each tick" rather than "append if missing," the correct fix is wholesale-replace, not dedupe-append — and dedupe-append risks silently dropping a genuinely hand-edited, non-pack-sourced hook entry that the reconciler can't tell apart from a projected one.

4. **#3944 — `cook --attach` drops rig context for graph.v2 formulas.**
   - First step: read `cmd/gc/cmd_formula.go:888-889`'s call into `graphroute.DecorateGraphWorkflowRecipe`, then `internal/graphroute/graphroute.go:490`, and compare against the equivalent `gc sling` call site that already passes real rig context correctly.
   - Blast radius: `cook --attach`'s call site only. Untouched: `gc sling`'s own decorate call (the reference implementation, unmodified) and legacy (non-graph) formula cook.
   - Proving test: multi-rig fixture (≥2 rigs sharing one bare agent name via a common pack import); run `gc formula cook --attach=<bead-id> <formula>` from within one rig; assert it resolves to `<that-rig>/<agent>` rather than erroring "unknown formulas v2 target," mirroring the already-passing `gc sling` behavior on the identical formula.
   - Disconfirmer: reproducing byte-identically on current `main` is unusually strong evidence, but the empty `routedTo` could be _intentional_ if `cook --attach` is meant to be rig-context-agnostic — in which case the correct fix is disambiguating bare names generically on the resolution side, not threading rig context through the call site as `sling` does. The issue's own "Expected" section assumes sling is the correct reference; that's a reasonable but unconfirmed assumption.

5. **#3927 — `builtin:claude` hardcodes `--effort max`, defeating both agent-level `args` and the settings-file `effort` key.**
   - First step: locate the `builtin:claude` provider's launch-argument assembly function and find where agent-level `args` are spliced relative to the builtin's own injected flags.
   - Blast radius: `builtin:claude`'s own launch-assembly code only (other providers, e.g. Gemini, have independent per-provider launch logic per #3962's finding). Untouched: the settings-file `effort`-key read path, which already works correctly once not overridden.
   - Proving test: agent config with `args = ["--effort", "medium"]`; capture the actual spawned command line and assert `--effort medium` wins; separately run a fixed reasoning-depth prompt and assert output-token count lands near the issue's own measured medium-effort baseline (~45-49 tokens) rather than the max-effort baseline (~375), pinning the fix against the issue's own quantified evidence.
   - Disconfirmer: the hardcoded max effort might be a deliberate safety/quality floor for orchestration-critical builtin agents rather than an oversight — blanket reordering could let any agent silently downgrade reasoning depth on paths that were never meant to be overridable. The correct fix might need to be an explicit opt-in override rather than unconditional last-flag-wins. Separately, the silently-dropped `model` key points at a missing unknown-key-validation gap that may be higher-value than the effort-ordering fix itself.

**Ordering rationale:** #3877 leads because it is the only pick with zero production-code blast radius while unblocking CI for every contributor's RC gate — the safest possible category of fix combined with platform-wide leverage. #3926 and #3862 follow because both have the most rigorously measured real-world severity (250k+ rows / 85s spawns; 1.4MB unbounded growth) paired with mechanism pins as precise as #3877's. #3944 is included specifically because it is the skill's own calibration example for Tier 1 — two exact file:line citations, cross-verified on `main`, the cleanest single-fix-shape bug in the set. #3927 closes the list on value (a measured 40–56s-per-turn cost tax across the whole orchestration-agent population) even though its disconfirmer is the most substantive of the five — a real chance the "obvious" fix isn't the intended one.

**Swap test:** re-reading pick 2's disconfirmer (orphan rows as symptom of an upstream write-path bug) against pick 4's issue (rig-context threading) does not fit — confirms the disconfirmers are issue-specific, not templated filler.

**Pick-vs-pick overlap:** none of the five share a code neighborhood (test-infra / order-wisp-prune / hook-projection-reconciler / formula-cook-decorate / provider-launch-assembly are five distinct subsystems) — no cross-reference needed among the top 5 themselves. Broader same-neighborhood notes are already stated in "Structural observations" above for issues outside this top 5.
