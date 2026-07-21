# Polecat concurrency audit — cross-repo root cause

**Date:** 2026-07-08
**Scope:** gastownhall/beads @ origin/main 1c1f2501e, gascity @ /home/ds/gascity-main, gascity-packs @ /home/ds/gascity-packs-main, plus empirical evidence from the live ds-research install (supervisor log, .gc/ reaper logs, dolt bead store, GitHub issue trackers).
**Method:** five parallel auditors (evidence miner, beads primitives, gascity dispatch, gascity lifecycle, packs protocol); load-bearing file:line claims spot-verified against source afterward.

## Verdict

There is no single bug. The claim primitive itself is sound at both layers that implement one: beads' `ClaimIssueInTx` is a genuine single-transaction compare-and-set with rows-affected verification (`internal/storage/issueops/claim.go:59-72`), and gascity's `gc hook --claim` wraps it with post-claim identity verification and a `bead.claim_rejected` event (`cmd/gc/cmd_hook_claim.go:197-252, 411-416`). The polecat failures come from five architectural gaps **around** the claim:

1. **RC1 — Identity is not fenced** (beads + packs). Claim safety assumes unique actor strings and honest single-writer behavior; neither holds.
2. **RC2 — Observation failures read as facts** (gascity lifecycle). A tmux error is indistinguishable from "session not running", and destructive actions fire on that single bad observation.
3. **RC3 — Wake delivery is fire-and-forget** (gascity dispatch). Sling is a metadata stamp; nothing guarantees a warm idle worker ever learns about routed work, and every backstop is gated off or silent on tmux.
4. **RC4 — Divergence states have no owner** (gascity). Several reachable bead states are invisible to every actuator, and the repair loops either don't exist or exist as untriggered helpers.
5. **RC5 — The model is the mutex** (packs). The gastown polecat protocol enforces claim discipline, drain, and kill decisions through prose; the sibling gascity pack already has the correct scripted pattern and gastown never adopted it.

Symptom → root-cause mapping: *duplicate work* = RC1 + RC2 + RC5 (and RC4's zombie survivors); *fail to claim* = RC3 + RC4 (routed+assigned blind spot, split-brain); *fail to drain* = RC2 (false drain-complete) + RC5 (no terminal command on the no-work path); *racing* = RC1 + RC5.

---

## RC1 — Identity is not fenced

**Mechanism.** `bd`'s claim CAS admits the caller's own actor: predicate `WHERE status='open' AND (assignee='' OR assignee IS NULL OR assignee=?)` and, on 0 rows, an idempotent-success branch when `assignee == actor && status == in_progress` (`claim.go:95-100`, added for agent retry, GH#8). Actor resolution falls back through `--actor` > `BEADS_ACTOR` > `BD_ACTOR` > `git user.name` > `$USER` > `"unknown"`. N workers sharing an identity therefore ALL claim the same bead "successfully" — the claim degrades to a no-op mutex.

**This resolves the open upstream issue beads#4657.** Its load test (`for i in 1..8; do bd update BD-xxxx --claim & done`) runs every racer as the same actor from one shell; "both callers succeed 5/5" is the idempotent branch working as designed, not a check-then-act TOCTOU (the CAS SQL is verified present). The 8-way non-determinism (2/8, 3/8, 7/8 successes) is consistent with dolt commit-conflict retry-budget exhaustion surfacing raw 1213 errors for some callers. The issue's suggested fix ("make it a conditional UPDATE") is already implemented; the real fix is identity/fencing. **Worth a correcting comment upstream — the misdiagnosis will otherwise send the fix in the wrong direction.**

**Adjacent unfenced writes (beads, all verified):**
- `bd assign` / `bd update --assignee|--status` is a blind last-writer-wins `UPDATE ... WHERE id=?` with no precondition (`issueops/update.go:277`), and `withRetryTx` replays the loser until the overwrite lands. A reassign silently steals a live claim; a replayed `--status open` can revert a concurrent close.
- `bd unclaim` never compares actor to assignee (`issueops/unclaim.go:22-60`) — any process can release any worker's live claim — and violates the store's own row_lock invariant (doesn't rewrite `row_lock`, doesn't clear lease columns), so a concurrent heartbeat cell-merges with it silently.
- The proxied-server (uow) claim path skips the lease and row_lock entirely (`internal/storage/domain/db/issue.go:185-214`) — claims made through it are invisible to `bd reclaim` and reintroduce the cell-merge class.
- Lease machinery exists (5m TTL stamped at claim; `bd reclaim` recovery) but heartbeating is opt-in, brand new (#4537), undocumented in AGENTS.md — enabling a reclaim reaper before workers heartbeat converts every long task into systematic duplicate work; not enabling one strands dead workers' beads forever.

**Pack half (RC5 overlap):** a restarted polecat is told to "determine where you left off from context" (`gastown/template-fragments/following-mol.template.md:10-12`) with no instruction to re-verify the bead is still assigned to its current session identity — pool restarts mint new session IDs, so a resumed worker can grind a bead the witness already reset and re-dispatched.

**Evidence.** beads#3570/#3575 (bead `vx-6i8.1` double-claimed → duplicate PRs #114/#115), beads#4657, gascity#1052 (two same-tick pool spawns fed the same bead), local trace: bead `gc-s43l4x` claimed by three polecat identities with one claim landing while an assignee was set and no unassign event recorded, then closed twice by different actors (2026-06-12). The enabling condition for that third claim is undetermined between a blind-assign write and an assignee clear hidden inside a metadata-merge update (the events table does not record assignee clears embedded in metadata updates) — both mechanisms are now confirmed real code paths.

## RC2 — Observation failures read as facts (gascity lifecycle)

**Mechanism.** The tmux provider swallows unavailability: `ListSessions` returns `nil, nil` on `ErrNoServer` (`internal/runtime/tmux/tmux.go:1005-1012`), `HasSession` → `false, nil`, liveness has no error channel at all, and callers coerce probe errors to "not running" (`cmd/gc/session_reconciler.go:1538-1541`, `session_wake.go:552-556`). The store side has a mature deferral discipline (`storeQueryPartial` gates nearly every destructive arm); **the runtime side has no equivalent**. Consequences, each a distinct incident class:

- **Drain-everything-at-boot:** boot tick is protected (`withDeferSessionClosesOnBoot`, one pass), the first steady-state tick is not — dead tmux reads as zero sessions, undesired beads are closed as orphaned, and respawns mint a tmux server inside the supervisor's cgroup.
- **staleTTL cliff:** after 30s of failed state-cache refreshes, every session reads dead in one step, silently (`state_cache.go:145-151`) — under oomd pressure this flips the whole city at once. (The ps-scan failure path deliberately degrades optimistically; the tmux-fetch path never got the same principle.)
- **Single-observation kill chain:** one transient probe failure against an idle warm worker → heal-to-asleep(runtime-missing) → slot freeable same tick → bead closed + worktree pruned under a live agent (`session_reconcile.go:1145-1250`, `session_state_helpers.go:78,95`, `session_reconciler.go:3329-3376`).
- **Invisible zombies:** the reconciler's input is open session beads only (`city_runtime.go:2252-2254`); once a bead is closed under a surviving process, that polecat is permanently invisible, keeps its bd credentials, and can claim more work.
- **False drain-complete:** observation error during drain-advance = "process exited" → bead closed, process never signaled (`session_wake.go:552-556, 671-674`).
- **Fail-open orphan kill:** a list error during pre-start orphan scan marks every process "tracked" and start proceeds → two claudes on one bead (`internal/runtime/tmux/adapter.go:323-331`).
- **on_death storms:** empty ListRunning with nil error fires every pool session's on_death hook (`city_runtime.go:958-985`).

Also in this class: losing the controller-lock race triggers `shutdown()` without `preserveSessions` — the **loser destroys the winner's live sessions** (`cmd_supervisor.go:2130-2137` → `city_runtime.go:3417-3470`); kill-then-start never confirms death (SIGKILL send ≠ dead, D-state escapees survive respawn); and the reparent heuristic (`PPID==1`) is wrong under a `systemd --user` subreaper, so tree-kills miss orphans on this host.

**Evidence.** gascity#2081 (live pytest session killed on stale store view), #1029 (drain during 5s zero-work window between formula steps), #2234 (config-drift rename drain killed two consecutive polecats mid-task), the documented tmux-before-supervisor footgun in this workspace's CLAUDE.md, session-name-collision incidents, `claude-zombie-report` existing at all.

## RC3 — Wake delivery is fire-and-forget (gascity dispatch)

**Mechanism.** `gc sling` = one metadata stamp (`gc.routed_to`) + optional nudge; everything downstream is pull. For a **warm idle pool worker on tmux there is no wake path at all** — independently established by both gascity auditors and verified:

1. Reconciler demand-binding re-points the trigger bead of an already-running session; no Start → no startup nudge; trigger env injected only at Start.
2. The purpose-built backstop `nudgeStalledPoolClaims` (`cmd/gc/idle_nudge.go`) is gated `if !cr.sp.Capabilities().CanReportActivity` (`city_runtime.go:2354`) — tmux never runs it. The gate's premise (tmux self-heals via relaunch) only holds if the session restarts; a healthy idle one never does. It is also keyed on `trigger_bead_id`, so later slung beads are invisible even where it runs.
3. The assigned-work anchor exempts the session from idle-sleep, so it never even gets the cold-wake nudge. The stuck state self-seals.
4. The natural operator repair — re-sling with `--nudge` — is swallowed: the idempotent early-return (`internal/sling/sling_core.go:120-131`) never sets `NudgeAgent`, and the CLI only nudges when it is set.

Supporting gaps: one shared 10s claim budget for the whole hook candidate loop, with a `claims_errored` drain that emits no event and nothing retries; continuation-group preassign uses a non-CAS `Update(Assignee)` (list-then-write, duplicate-work window) and a preassign failure exits 1 *after* the primary claim durably landed (agent sees failure, bead assigned to an idle owner); queued nudges die at a 24h TTL with no escalation; drain of an "idle"-reason worker is not cancelable when work is slung into the ack gap (`session_wake.go:288-295`, verified — only `orphaned`/`no-wake-reason` cancel), so the mayor slinging to an idle worker mid-drain gets that worker SIGKILLed on its first turn.

**Evidence.** gascity#1129 (fixed at the surface; the structural gate remains), #3554 (claim succeeds then no continuation driver), #3968 (pool-handle vs instance-identity claim-query mismatch), local: `routed-bead-nudger` patrol running daily with 69 queued undelivered nudges, `dead-target-warn` 371×, bead `gc-73cv2` warned across 7 hours, dispatcher-watchdog killing 4 wedged dispatchers in 14h.

## RC4 — Divergence states have no owner (gascity)

Reachable states invisible to every actuator, with no sweep:

- **open + routed + assigned:** every probe requires `--unassigned` and the hook requires an empty assignee; sling onto an assigned bead is a stderr *warning*, exit 0 (`internal/sling/sling_attachment.go:411-459`). Live instance: bead `gc-d4s4h`, stuck 12 days, appearing in `assignedWorkBeads` dumps every reconcile tick.
- **in_progress + unassigned:** `--reassign`'s `clearHumanAssignee` clears assignee regardless of status and never resets status (`sling_core.go:1423-1471`); beads' update path confirms nothing downstream reopens it. Excluded from `bd ready` (open-only), from assigned tiers (no assignee), from resume (no assignee). Permanently invisible.
- **stranded in_progress with dead assignee:** the reconciler *diagnoses* (`emitSessionStrandedDiagnostic`) but never repairs, though tested repair helpers exist (`unclaimWorkAssignedToRetiredSessionBead` / `reassignWorkAssignedToRetiredSessionBead`, `session_beads.go:728,779`) — wired only into named-session retirement and manual close, never the stranded-pool-worker path. The operator-built stale-claim-reaper in this workspace is the compensating mechanism.
- **split-brain dual backend:** `.beads/dolt` (server) and `.beads/embeddeddolt` (file) are two unrelated databases; mode resolves per-process from env, so a hook missing the server-mode env writes claims to a store no server-mode process ever reads. Exit 0 throughout. Cleanest no-race explanation for "claims silently skipped." This install runs exactly that split (gc file-backend sessions + rig dolt server).
- **refinery bounce gap:** reopen-then-restamp-routing as two steps (gascity#3383) — a crash between them leaves a demand-invisible orphan.

## RC5 — The model is the mutex (packs, gastown)

The gascity pack has the correct pattern: a deterministic scripted claim block (`gc-role-worker.md.tmpl:26-29, 100-163`) with `CLAIM_REJECTED` handling, post-claim assignee/status/route verification, drain-ack-on-no-work, and an explicit ban on freelance work discovery. **The gastown pack has none of it:**

- No STOP branch on claim failure anywhere in the polecat prompt/formulas; claim instruction split across two divergent commands (`gc hook --claim` vs `gc bd update --claim`).
- No ownership re-verify on crash/resume (RC1 overlap) — resume is "figure it out from context."
- Three divergent copies of the done sequence (formula / prompt FINAL REMINDER / injected fragment); only the formula copy has the branch-shape gate; a second execution can reassign the bead out from under the refinery mid-merge, and the prompt copies use bare `<work-bead>` placeholders that bd's fuzzy matching will happily resolve to the wrong bead post-compaction.
- No terminal command on the no-work startup path or the escalation fork — the 2h `idle_timeout` is the only drain backstop ("Idle Polecat heresy" prose stands in for a lifecycle mechanism).
- The shutdown dance (nudge → sleep 60/120/240 → peek) kills any polecat inside a single tool call longer than ~7 minutes — a full test suite reads as dead — with no pre-kill check of bead `updated_at` or worktree mtime; no warrant dedup, so witness and deacon file repeat warrants and up to 3 dogs run concurrent dances, later ones hitting the reconciler-restarted replacement.
- The refinery merge-push path lacks the already-merged check the witness path has: a restart between `git push` and `bd close` converts a successful merge into a false-completion human escalation.
- `polecat-churn-watcher.sh` is wired to nothing, false-positives on every clean handoff, and half its detector keys on metadata nothing sets.

---

## Fix roadmap

### A. Gascity source fixes (you're maintainer — highest leverage), ranked

1. **Typed runtime-unavailable error, threaded as `runtimeQueryPartial` through every destructive arm** (RC2). One pattern, uniform application; `storeQueryPartial` is the in-tree template. Sites: `tmux.go:1005`, `state_cache.go:145`, `session_reconciler.go:1538`, `session_wake.go:552,671`, `city_runtime.go:958,2793`, `adapter.go:323`. Kills boot-drain, staleTTL mass-death, false drain-complete, on_death storms, fail-open orphan kill.
2. **Give warm tmux workers a demand-driven wake** (RC3): un-gate `nudgeStalledPoolClaims` from `CanReportActivity` (its state machine is already restart-safe and bead-state-keyed) or deliver the claim nudge on trigger re-point in `bindPoolSessionTriggerBead`; key it on the routed bead, not `trigger_bead_id`. Companion one-liner: honor `--nudge` on idempotent slings. Closes #1129 structurally and makes re-sling the universal repair verb.
3. **Own the divergence states** (RC4): hard-error sling-onto-assigned without `--reassign`; make `--reassign` reopen in_progress beads; add a reconcile-tick sweep for `routed_to`-set beads no live identity can claim; wire the existing unclaim/reassign helpers into the stranded-pool-worker path after a confirmation window.
4. **Drain work-safety** (RC3): re-check assigned work before `markDrainAckStopPending` for all reconciler-owned ack reasons (not just orphaned/no-wake-reason); set `GC_DRAIN` at begin so agents can see it; add the assigned-work gate to idle-timeout kills.
5. **Confirmed-dead-before-start** (RC2): token-fence the async drain stop; post-kill re-scan before Start; subreaper-aware tree kill (parent-outside-descendant-set, not `PPID==1`); fail-closed orphan scan.
6. **Identity fencing at spawn** (RC1, no beads change needed): inject a unique per-session `BEADS_ACTOR` into every worker env so direct `bd` invocations can't collapse into one actor. Verify first what actor polecats' `gc bd update --claim` calls actually resolve to today.
7. `preserveSessionsOnShutdown()` on every pre-run supervisor cleanup path (controller-lock loser must never stop the winner's sessions).

### B. Packs fixes (deployable immediately as text)

1. **Port the gascity GC_CLAIM discipline into the gastown polecat prompt**: one canonical scripted claim block, CLAIM_REJECTED → STOP + drain, post-claim verification, resume-time ownership re-check, drain-ack on no-work, and the no-freelance-discovery ban. Single highest-impact pack change; kills the racing-claim, work-without-claim, resume-a-reset-bead, and idle-no-drain classes.
2. **Collapse the three done-sequence copies to one** (formula as source of truth; others say "if submit-and-exit already ran, drain — running it twice is a bug"); add `WORK_BEAD_ID` derivation everywhere.
3. **Evidence-gated shutdown dance + warrant dedup**: pre-kill progress check on bead `updated_at`/worktree mtime → PARDON; check for an existing open warrant for the same target before filing.
4. Refinery already-merged check (`git merge-base --is-ancestor`) before the 0-diff halt; churn-watcher filter fix + actually wiring it to an order (this is also how you measure whether 1–3 worked).

### C. Beads upstream PRs (ranked by acceptance likelihood — small, self-evidently correct first)

1. **`bd unclaim` ownership check** (`AND assignee=?` + `--force`), clear lease columns, rewrite `row_lock`, wrap in `withRetryTx`. Small diff, fixes a stated-invariant violation.
2. **Proxied-server claim path**: reuse `issueops.ClaimIssueInTx`/`leaseSetClause` so uow claims get lease + row_lock. Invariant violation, mechanical fix.
3. **Guard `bd update --assignee/--status`**: optimistic version check on the already-in-hand `updated_at`, or refuse `--assignee` on in_progress without `--force`; recompute metadata/notes merges inside the tx.
4. **Structured claim-conflict errors**: JSON error object on stdout under `--json`, per-issue exit accounting (batch with one lost claim currently exits 0).
5. **Comment on #4657 with the corrected root cause** (same-actor idempotency, not TOCTOU), then propose **claim fencing** (session-scoped claim token / `BEADS_CLAIM_STRICT`) as the follow-up issue — this is the architectural ask, so lead with the evidence.
6. Bounded embedded-backend open backoff (currently waits forever, silently — the "gc hangs" class) + "waiting for lock held by PID" diagnostic.
7. Fail loud when both `.beads/dolt` and `.beads/embeddeddolt` exist and mode resolution picks embedded while a live server pid/port file is present (split-brain tripwire).
8. Docs: AGENTS.md should teach `bd ready --claim` (atomic pop — it exists and is correct) instead of list-then-claim, and document the heartbeat contract before anyone wires a reclaim reaper.

### D. Ops mitigations for ds-research, now

- Keep the `gc-sling` wrapper (auto `--nudge`) and the routed-bead-nudger until A2 lands; keep the placeholder-tmux-before-supervisor playbook until A1 lands.
- Set unique `BEADS_ACTOR` per worker session (after verifying current actor resolution).
- Do **not** enable any `bd reclaim` timer until workers heartbeat.
- Formalize the stale-claim-reaper as an order sweeping in_progress beads whose assignee session bead is closed/dead (it's the manual stand-in for A3).
- systemd drop-in: `Type=notify` + `WatchdogSec` for the supervisor (plumbing exists in-tree; unit doesn't use it) — a wedged reconcile tick currently halts convergence while systemd sees healthy.
- Audit which processes in the install run with server-mode env vs embedded fallback (split-brain check): any `bd` caller without `BEADS_DOLT_SERVER_MODE`/shared-server env is writing to a different database.

## Open questions / verification items

1. What actor string do polecat `gc bd update --claim` calls resolve to today? (Determines urgency of A6/C5. The gc hook path uses per-session identities; direct bd paths may not.)
2. The `gc-s43l4x` third-claim enabling condition: blind assign vs assignee-clear-inside-metadata-merge. Beads events don't record assignee clears embedded in metadata updates — an events-coverage gap worth an upstream note of its own.
3. Reproduce #4657's 8-way partial-failure distribution with **distinct** `BEADS_ACTOR` values to confirm exactly-one-winner behavior (expected: 1 success, 7 clean rejections) — this is the empirical artifact to attach to the upstream comment.
4. "Fail to drain" in the strict sense (session refuses to exit post-completion) had the weakest direct evidence — mostly explained by RC5's open-ended no-work paths and the 2h idle timeout. Wiring the churn watcher (B4) gives real telemetry before deeper investigation.
