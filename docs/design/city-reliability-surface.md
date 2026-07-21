# The city's reliability surface, classified by failure shape

_2026-07-16 · bead gc-4zf.1 (Track A) · read-only analysis, method per the `mechanic` skill (trace from source; no symptom-theorizing)._

_v2. Supersedes `docs/gc-4zf-track-a-reliability-surface-map-2026-07-16.md` (v1), which closed with a list of gaps "still to enumerate." This version closes the largest of them (the full order cover set, one-by-one, for liveness-vs-outcome), adds the mail/escalation and slack-binding slices, and **corrects four v1 rows that were wrong or stale**. v1 rows are retained by number so the two read side by side._

_v2.1 (2026-07-17, mayor, post-close amendment): **9.5 was stale within hours of v2 being written** — the fix landed, was found NOT DEPLOYED, and was then deployed and re-proven by live injection. 9.5 rewritten; **9.5a / 9.5b added** for the two residues the live re-run exposed and that no reading of the source would have (the escalation's `bead_ref` cannot name the orphan; a terminated workflow's poisoned record is never swept). §10 gains the division those residues sharpen: **the engine made the failure loud; a scan made it converge.** Amended in the doc, not only on the bead: this file is what feeds Tracks B/D/E, and a finding parked in a closed bead's notes is exactly the fire-and-forget shape §2 catalogues._

---

## What this track had to answer

Not "where could Temporal go" but "where does flow actually stop, and what shape is it". The bead put a falsifiable prediction in writing:

> If the honest answer is "mostly query-shaped, already covered, and the real gap is that the covers are orders that die silently", then the Temporal case narrows to ONE thing — hosting the level-triggered scans on an independent failure domain (P4-proven) — and the epic should say so plainly rather than find work for the substrate.

**That prediction holds, and the exhaustive pass narrows the case further than v1 did.** The order enumeration turns v1's central assertion into a measurement, and the timer-shape analysis (§10) dissolves all three of v1's "genuine timer-shaped candidates." See §11.

## The classification that carries the epic

- **query-shaped** — level-triggered; answerable by a scan question against durable state ("any ready bead unclaimed > 30m?"). Needs no timer, cannot drift, re-derives itself every pass. **No Temporal case** — a periodic scan is strictly simpler and self-healing.
- **event-shaped** — edge-triggered; needs a signal, and signals get lost at integration boundaries, so it needs a reconciler too. Temporal helps only if the signal source is itself durable; otherwise the reconciler is query-shaped and does the real work.
- **timer-shaped** — genuinely needs a durable wakeup that survives process death. **The only shape with a prima facie Temporal case** — and §10 shows this category is nearly empty here.

**Substrate question, applied to every cover:** would the cover survive its own substrate dying? The patrols are *orders*; **orders are what silently die** (gc-qo3). A query-shaped leak guarded by an order has moved the single point of failure, not removed it.

---

## 0. Corrections to v1 (read this first)

Four v1 rows do not survive an exhaustive check. Three of them were wrong *at the time v1 was written*, not merely stale. This matters for the epic: v1's ranked conclusion rests partly on gaps that were already filled.

| v1 row | v1 claim | Correction | Evidence |
|---|---|---|---|
| **7.7** | "Closed bead, code never merged to main… **NONE** — every consumer keys off `bd close`; **nothing reads git**." v1 line 91 calls fixing this "the highest-value reliability change the map surfaces." | **Wrong, and wrong by 3 days.** `bin/work-landing-reaper` (file mtime **2026-07-13** 15:34; `orders/work-landing-reaper.toml` 15:39) does exactly the demanded check: `git merge-base --is-ancestor "$tip" "$base"` (`work-landing-reaper:121`) plus a patch-equivalence fallback `git cherry` (`:125`). It lands the branch under lock with a test gate (`:153-227`) or blocks the bead and mails the mayor (`:136-147`). `close-gate-reaper:117-121` already carries an `unmerged_branch` rule that flags and explicitly punts the fix to it. | `bin/work-landing-reaper:121,125,136-147,153-227,236-306` |
| **2.1** | "**no per-order cover**" for a silently de-registered order. | **Partially wrong.** `bin/mayor-pattern-miner:243-296` globs every `orders/*.toml`, parses `interval`, calls `gc order history <name>`, and flags any order silent past **interval × 2**. The logic v1 asks for exists. Its real defects are narrower and worse-defined: it runs **weekly** (Mon 08:30), it **skips itself**, and it `continue`s past every event-triggered order (`:248-249`, `[[ -z "$interval" ]]`). Also `morning-triage-watchdog` is a true per-order **outcome** watchdog for exactly one order. | `bin/mayor-pattern-miner:243-296`; `bin/morning-triage-watchdog:31-37` |
| **3.4** | "Agent rate-limited (429) — **no Retry-After handling** in runtime/session/worker." | **Refuted as stated.** No HTTP 429 handler exists because agent sessions are tmux panes, not an HTTP client the runtime owns. A real screen-scrape quarantine lane exists and self-heals: detect (`internal/runtime/dialog.go:1268,1290`) → `ExitRateLimitQuarantine` (`internal/session/lifecycle_exits.go:96-129`) → `RateLimitQuarantinePatch` sets `state=asleep`, `quarantined_until` (`lifecycle_exits.go:201`) → re-admitted when the patrol observes expiry (`cmd/gc/session_reconcile.go:440-458`, `internal/session/lifecycle_transition.go:263`). Work is **not** abandoned. See §10 — this is the city's proof that durable wakeups need no timer service. | `/home/ds/gascity-main` at origin/main |
| **1.2** | cites "readiness gate **gc-xk1hg**". | **Unverifiable.** That bead ID does not resolve in the bd store (264 beads scanned). The *mechanism* is real (`bin/routed-bead-nudger`, `.test`, `orders/routed-bead-nudger.toml`, `.gc/routed-bead-nudger-state.json` all exist). The "regression test RED for 2 days" claim has no surviving log or git artifact (gas-city is not a git repo) — **treat as unconfirmed**, not as evidence. | `bd list --all` |

Two v1 counts were also loose. Precise now: **116 files in `orders/`, 106 live, 10 disabled/`.bak`**. The figures in circulation (~13 patrols, ~48 cover set, 103 order definitions) describe different subsets and are not contradictory; cite the subset, never the bare number.

---

## 1. The order cover set, measured (v1's "single most valuable remaining slice")

v1 named this the highest-value remaining work because "it directly quantifies the central finding." It does, and the finding survives contact with the data.

**Of 106 live orders, 23 check outcome outright; 3 more are mixed.** The other ~80 are liveness, resource thresholds, hygiene, or reporting. (The boundary is a judgment call at the margin — "did the token stay valid" is liveness of a precondition, not of a process. The ratio is robust to a few reclassifications; the conclusion does not turn on 23 vs 26.)

Orders that genuinely verify **outcome**: `work-landing-reaper`, `close-gate-reaper`, `mem-digest`, `flow-patrol`, `dispatch-patrol`, `pl-flow-audit-daily`, `stale-claim-reaper`, `velocity-audit-sensor`, `unlabeled-assignee-sensor`, `morning-triage-watchdog`, `temporal-soak-check`, `spawn-storm-detect`, `gate-sweep`, `issue-merge-watcher`, `pr-state-poller`, `pr-merge-notifier`, `fork-pr-approval-gate`, `approved-pr-automerge{,-packs}`, `blocked-routed-reaper`, `cross-rig-deps`, `decision-staleness-reaper`, `evals-nightly`. Mixed: `dispatcher-watchdog` (signal 2 only), `mcp-audit` (usage half), `stall-watch` (WORK_STALLED sub-signal only).

Three structural facts fall out of the enumeration, and they are the real Track A result:

**1.1 — The tightest loops in the city are the blindest.** Ranked by cadence, the fastest orders are `beads-health` (30s, `gc beads health --quiet`), `gate-sweep` (30s), `mayor-watchdog` (1m: `tmux_alive()` / `systemctl is-active` / session-state), `nudge-poll-reaper` (2m), `pl-529-recovery` (2m). Every one of these except `gate-sweep` is pure liveness. This *is* the "everything green, nothing flowing" incident, stated structurally: the checks that run most often are precisely the ones that cannot see whether work moved. A 30-second liveness loop reports green 120 times an hour while zero beads are dispatched.

**1.2 — Of 106 live orders, exactly one has a dedicated watchdog.** `morning-triage-watchdog` covers `morning-triage-cycle`, and it does it properly, by outcome: it greps `.gc/morning-triage-cycle.log` for an `"event":"slung"` line dated today (`:31-37`) rather than asking whether cron fired. Nothing covers `morning-triage-watchdog` itself. The other ~105 live orders have no dedicated cover.

**1.3 — The fleet-wide order-firing floor exists, is undocumented, and is weekly.** `mayor-pattern-miner:243-296` is the only thing standing between this city and another gc-qo3. It is invisible: its own `.toml` `description` never mentions the capability, so it is discoverable only by reading the script. Its blind spots are structural, not incidental:
  - **weekly** — an order that stops firing Monday afternoon is unreported until the following Monday, plus one interval;
  - **skips itself** — the check-of-checks has no check;
  - **blind to event-triggered orders** — `[[ -z "$interval" ]] && continue` (`:248-249`) drops every event order silently, which is the entire `nudge-on-route` / `help-request-surface` / `pl-human-gate-surface` / `pl-loop-close` family;
  - **it is an order** — the substrate question applies to it in full.

`city-selftest` (6h) is meta in a different and narrower sense: it runs the `bin/*.test` regression suites, so it verifies that scanner **code** is correct, not that scanners **fired**. Its own header records the precedent that justifies it, and it is the sharpest sentence in the city's own docs: *"A check nobody runs is not a check."*

---

## 2. The fire-and-forget family (closes v1's "mail/escalation delivery" gap)

v1 asked whether the `help-request-surface` backlog is a leak or a symptom. It is a symptom of a **family**, and the family has a precise, sourced membership: scripts that stamp *"I acted"* rather than *"the thing I triggered finished."*

| script | marker written | skip gate | never re-checked |
|---|---|---|---|
| `bin/pr-state-poller` | `handled_ids` → cache (`:227-230`, persisted `:234-241`) | `:211-213` `continue` | **Confirmed by exhaustion**: `grep -rln handled_review_ids bin/` matches only `pr-state-poller` itself. Nothing re-reads the cache; nothing checks whether the dispatched `mol-pr-iterate` ever closed. Its `existing_iterate_bead` guard (`:94-109`) runs *before* the marker is set and only prevents double-dispatch. |
| `bin/nudge-on-route` | `state[bead_id] = {routed_to, nudged_at}` (`:95-97,106-107`) | `:59` (`current == previous`) | `routed_to` never changes again, so the bead is skipped forever even if the nudge woke nothing. |
| `bin/help-request-surface` | `gc.help_surfaced_at` (`:44-45`) | `:41` | Marks the help request surfaced; never checks the block was resolved. Edge-triggered on `gc events --since 2m`. |
| `bin/pl-human-gate-surface` | `gate_surfaced_at` (`:211-213`) | `:144-147` | Marks the notification posted; never checks a human answered the gate. |

Two caveats that keep this honest. First, for the surfacers, "delivery" *is* the stated scope, so this is only a defect if you read the job as "did the request get answered." Second, and more important for the epic:

**The thesis is real but not universal.** It is concentrated in the **edge-triggered, one-shot-idempotence family** above. The counterexamples matter more than the confirmations (§10): `close-gate-reaper` and `work-landing-reaper` re-derive outcome from evidence metadata and from **git ancestry** on every run and hold no marker at all; `routed-bead-nudger`, `dispatch-patrol`, `stall-watch`, `orphaned-molecule-reaper`, and most reapers are level-triggered and self-healing with no persistent skip marker.

`nudge-on-route` and `routed-bead-nudger` are the designed pair and the city's own worked example of the fix: the edge-triggered nudger is idempotent-once, so a level-triggered backstop was written for it, and `routed-bead-nudger:2-9` says so in its header. **The pattern that repairs the family already exists in-tree.** It is not a durable timer.

---

## 3. Dispatch / routing

| # | Where flow stops | Shape | Cover (liveness vs outcome) | Evidence | Substrate |
|---|---|---|---|---|---|
| 3.1 | **Bead created but never routed** — open, `gc.routed_to` unset | query | **Partial, newly traced.** `cmd/gc/route_recovery.go:73-137` scans *all* open beads every patrol tick and re-stamps `routed_to` from a carried `gc.run_target`. But `carriedPoolRoute` (`:25-42`) can only restore a route the bead **already declares**. A sling that dies before writing either field leaves a bead no scan can route: *"A bead with no carried route is left for its owner to sling."* | gc-s5c, gc-q4s, gc-4qz (2026-07-16). All three created **and closed same-day** during active cutover — they did *not* sit for days. The finding is structural, not durational: **no query exists** for `open AND routed_to unset AND age>N`. Found by a human watching the session. | no cover to test |
| 3.2 | **routed-raw footgun** — `routed_to` set, no assignee, no molecule (formula-attach failed after routing) | query | **NONE.** Named in source (`internal/sling/sling_core.go:281-282`) and healed only reactively when a human re-runs `gc sling --on <formula>` (`:270-290`). Grep for the term returns only the two comments; **no scan consumes that state.** | source | — |
| 3.3 | **Ready bead unclaimed past threshold** | query | `bin/routed-bead-nudger` — level-triggered, live `gc bd ready` re-check before every nudge (`:317-325`), per-pool cooldown only (`:334,343-347`), no permanent marker | working (gastownhall/gascity#1129) | nudger is an order; `city-selftest` guards its test, and is also an order |
| 3.4 | **gc-sling writes metadata to the wrong rig** — `--rig ${bead%%-*}`, prefix ≠ rig name; errors swallowed by `\|\| true` | query (audit scan) | **NONE for the false-green**; fixed at source 2026-07-16 | gc-na2o: ~40 orphan worktrees (~2.8GB) and **1384 false `worktree-recorded` events** | **the audit log asserted success that never happened — a cover reading it would inherit the lie** |
| 3.5 | **Duplicate workflow roots for one target** | query (atomic gate) | **NONE.** No same-target/same-formula idempotency gate at instantiation. Root cause is cross-store: the order's `inflight_review_prs` dedup matches open review beads in the gascity **rig** store, but the duplicate lived in another store, so dedup never saw it. | gc-28jm (**open, P0**): `gc-5wse`/`gc-r7c6` for gc-89e **33s apart** (22:14:11Z / 22:14:44Z); `gc-hlyl`/`gc-647v` for gc-z9z 26s apart; earlier `gc-4e1`/`gc-0tlhd`. | caught for gc-4e1 only by a second polecat's incidental self-check. Detection method for the other two pairs: **unconfirmed** |
| 3.6 | **Slung to a dead/min=0 target** | query | `bin/gc-sling` pre-flight guard (warn-only) | 2026-07-16 strand | inline in the wrapper, so it survives order death; warn-only, no durable record |
| 3.7 | **Fleet claims oldest-first, priority discarded** | query | canary dr-dcd (closed unverified) → re-opened dr-fk4 | dr-fk4 | the "cover" was a claim, not a check |

## 4. Orders / scheduler (the substrate under most covers)

| # | Where flow stops | Shape | Cover | Evidence | Substrate |
|---|---|---|---|---|---|
| 4.1 | **Order silently de-registered** | query ("fired within 2× cadence?") | **Weak, not absent** (v1 correction §0). `mayor-pattern-miner:243-296` — weekly, skips itself, blind to event orders. `gc doctor: order-firing-current` flags aggregate staleness only. | gc-qo3: `maintenance-cycle` dormant **10 days** (2026-07-06→16). Root cause **is not a bug**: a deliberate `city.toml` `[[orders.overrides]] enabled=false`, proven by the backup `city.toml.bak-pause-maintenance-cycle-20260706T175816Z`. The loader honored it correctly. It read as a fault only because **the override carried no comment** — so gc-372 logged "de-registration root-cause unknown." | **the thing that would check it is itself an order** — the canonical case |
| 4.2 | **Order-firing floor collapses** | query/liveness | `beads-health`/`close-gate-reaper` cadence = de-facto canary | 2026-07-14 filestore-bloat stall (`.gc/beads.json` 176MB, reload-under-lock) | the canary orders share the substrate that fails |
| 4.3 | **`gc order check` too slow to use** — times out at 90s on 152 orders | n/a (tooling) | none | gc-qo3 investigation | the health check for orders is itself unreliable at scale |
| 4.4 | **Event-triggered orders are unwatched entirely** | query | **NONE** — `mayor-pattern-miner` `continue`s past every order without an `interval` (`:248-249`) | this pass | the one fleet-wide check has a hole shaped exactly like §2's family |

## 5. Sessions / workers

| # | Where flow stops | Shape | Cover | Evidence | Substrate |
|---|---|---|---|---|---|
| 5.1 | **"Productive" means elapsed time, not work done** | query (thin) | `cmd/gc/session_reconcile.go:787-798` `productiveLongEnoughInfo` — comment says *"long enough to have done useful work"*; the test is `clk.Now().Sub(lastWoke) >= churnProductivityThreshold`. Identical proxy at `internal/session/lifecycle_exits.go:121-128`. **No code path inspects the bead's diff, close, or fan-out.** | source trace | **This is the map's thesis proven in Go, not inferred from an incident.** A session alive and idle is indistinguishable from one that shipped. |
| 5.2 | **Worker parked at a UI modal** — reads "active", unreachable | query (peek prompt) | `bin/polecat-ui-stuck-scanner` (surface-only, AUTO_RESET off) — liveness | codescalebench gc-485664; dr-3sz | scanner is an order; detect-only |
| 5.3 | **Worker stalls at `AskUserQuestion`** | prevented | `bin/claude-account` strips the tool from pool workers | dr-3sz Part 1 | prevention at launch — no runtime cover needed |
| 5.4 | **Worker stalls at a startup modal** | prevented | `claude-account` pre-accepts `hasClaudeMdExternalIncludesApproved` | dr-oo8d | prevention at launch |
| 5.5 | **Agent rate-limited** | **query + durable timestamp** (see §0, §10) | quarantine → `quarantined_until` → patrol expiry sweep. Self-heals. | source | the sweep is the reconciler, not an order — it survives order death |
| 5.6 | **Claim orphaned by a dead worker** | query | **No lease, no TTL anywhere** (grep-confirmed across `internal/{beads,dispatch,sling}`, `cmd/gc`). `cmd/gc/pool_session_name.go:114-213` re-derives every tick whether a claim's assignee still maps to a live open session bead (`:533-573`) and reopens it. `bead.dead_assignee_reopened` is emitted purely as a record of an already-completed repair. | source | **cannot drift while the tick runs** — the correct shape |
| 5.7 | **Worker boots then drains** — dirty work_dir | query | none automated | reference_worker_noclaim_dirty_workdir | — |
| 5.8 | **Duplicate singleton-pool sessions** (alias collision) | query | mayor drains each cycle (stopgap) | dr-l66ch | recurs on every restart |

## 6. Supervisor / event bus

| # | Where flow stops | Shape | Cover | Evidence | Substrate |
|---|---|---|---|---|---|
| 6.1 | **Events drop silently at the transport** | **event** | `internal/events/recorder.go:196` states it: *"Errors are written to stderr — never returned."* Cross-process flock contention drops the record after `recordFlockTimeout = 250ms` (`:28-33`, `:220-225`). Exec provider is *"fire-and-forget (fork per event, errors to stderr)"* (`internal/events/exec/exec.go:6,52-64`). | source | — |
| 6.2 | **The "critical" event tier is not honored at the transport** | event → query | **Newly traced, load-bearing.** `internal/convergence/events.go:22-35` defines `TierCritical` as at-least-once "re-emitted on replay". But `cmd/gc/convergence_store.go:380-388` funnels **every tier** through the same `rec.Record(...)` and **discards the recovery flag entirely** (`_ bool` in the signature). Critical delivery is not a bus property. It is achieved by the caller's **state scan**: `cmd/gc/convergence_tick.go:555-589` lists convergence beads at startup and re-emits what the bead state shows never landed. Best-effort events (`internal/convergence/manual.go:71,359`) have no reconciliation and are simply gone. | source | **the durability label is on the wrong layer** — a consumer trusting the tier name would be trusting a scan it cannot see |
| 6.3 | **Event-delivery starvation** — consumers back up, work stops while orders still fire | event | none direct; the watchdogs catch a *wedged* dispatcher, not slow delivery | 2026-07-14 wedge RCA (events.jsonl 198MB) | watchdogs are orders on the same supervisor |
| 6.4 | **`/v0/city/status` ~20s synchronous hang** drags reconcile/dispatch | in-process cache expiry (**not** orchestration — see §10) | none; GC_PPROF persistent | dr-7smz8 | — |
| 6.5 | **Supervisor alive but wedged** (tmux state-cache starved) | query (log signature) | `tmux-refresh-failure-sensor` — fires ~45m before death | 2026-07-15 host death precursor | sensor is an order watching a signal the supervisor emits while dying |

## 7. Dispatcher

| # | Where flow stops | Shape | Cover | Evidence | Substrate |
|---|---|---|---|---|---|
| 7.1 | **Dispatcher wedged** | query | `bin/dispatcher-watchdog` (idle≥5h = liveness; **same unconverted bead set ≥90m = outcome**, `:94-120,160-189`) + `bin/dispatcher-liveness-sensor` (port-0/silent-hang log signature). The sensor's own note is the honest one: *"a bead-STATE health check structurally cannot see this."* | reference_dispatcher_conversion_stall | both are orders on the supervisor they guard |
| 7.2 | **In-process: dispatch tolerates event loss by design** | query | `cmd/gc/city_runtime.go:722-751` — the patrol tick **cancels pending event-driven fires**: *"Patrol scans every reconciler state authoritatively, so any pending event-driven fires are redundant — drop them."* Work-query failures are classified retryable, not terminal (`internal/dispatch/control.go:427-499`): *"a long-running control dispatcher must keep sweeping rather than exit permanently during the outage."* Raw `bd` closes with no event are caught on the ≤5s idle re-poll (`cmd/gc/dispatch_runtime.go:56-60`). | source | **the dispatcher is already built the way this map recommends**: events are a latency optimization, the scan is the truth |

## 8. Dolt / bead store

| # | Where flow stops | Shape | Cover | Evidence | Substrate |
|---|---|---|---|---|---|
| 8.1 | **Read-tx draws a reaped conn** | event (conn death) | upstream fix pending | founding charter #2 | — |
| 8.2 | **Write-hang** — `gc mail send` hangs | trust-boundary timeout | investigate (charter #3) | — | — |
| 8.3 | **Dolt GC never runs → bloat → SIGKILL** | query (size) | `dolt-gc-maintenance` nightly | reference_dolt_gc_never_runs | GC is an order; if it dies, bloat returns silently |
| 8.4 | **`.gc/beads.json` bloat → order stall** | query (size) | `beads-json-compact` (24h) | 2026-07-14 RCA | compactor is an order (was manual-only — the gap that caused the stall) |
| 8.5 | **DOLT_PUSH network-hung zombies survive KILL QUERY, block DOLT_GC fleet-wide** | **trust-boundary timeout, not orchestration** (§10) | none (needs server-side max-query-duration + push timeout) | dr-ne8 (2026-07-13) | **v1 filed this as Temporal's best timer case. It is not orchestration at all** — it is a dolt server config. No workflow engine can time out a query inside a server it does not run. |

## 9. Host, molecule, auth

| # | Where flow stops | Shape | Cover | Evidence |
|---|---|---|---|---|
| 9.1 | **Whole-host memory-exhaustion death** | query (trend) | `resource-sweep` gates on `MemAvailable<4G` and **missed it** — MemAvailable stayed 10–18G while **swap sat at 0** | 2026-07-15 host death; ~85m outage. **The cover ran, stayed green, and watched the box die: right cadence, wrong metric.** |
| 9.2 | **oomd collateral** picks mayor/supervisor | query | `disk-pressure-guard`, `resource-sweep` (surface-only) | box_oom_gate_scix_postgres |
| 9.3 | **Closed bead, code never merged** | query (merge-base) | **COVERED** — `work-landing-reaper` (§0). v1's headline gap, already filled. | rca-eb-merge-gap |
| 9.4 | **Copilot review lands, iterate wedges, review "handled" forever** | **query once dispatch is durable** (§10; v1 said timer) | `pr-state-poller` fire-and-forget past dispatch (§2) | gc-4zf seed; **Track D** |
| 9.5 | **Temporal worker crash mid-sling drops a cycle** | event (terminal-pending) | **FIXED + deployed + live-proven 2026-07-17** — redelivery now quarantines the poisoned claim (`pending`→`failed` with reason) and writes a durable `escalations.jsonl` record. Was "none — the refusal is silent". At-most-once preserved (sling never made retryable). **The drop is now loud; see 9.5a/9.5b for what it still does not do.** | gc-4zf.4 (chaos test 07-16; acceptance re-run 07-17T00:41Z) |
| 9.5a | **The escalated orphan is never NAMED** — crash mid-sling leaves the cycle bead open with `routed_to` unset; 9.5's escalation fires but carries a **synthetic** `bead_ref` (`temporal-shadow/…/review/selection`), never the real id | query (`open + routed_to unset > N min?`) | **detection only, and narrow.** `orders/temporal-soak-check.toml` (2h) scans it — **verified live against `gc-0qr1`**, not fixtures. Nothing CLOSES it. Scoped to `maintenance-cycle` only; the general case (any bead created but never routed: gc-s5c, gc-q4s, gc-4qz) still has **no cover**. And the check is an order (§4). | gc-4zf.4 acceptance re-run. **`bead_ref` is fixed at Propose time, BEFORE the bead exists**, so the crash guarantees the real id never reaches the record. Its unit test asserted `bead_ref=gc-4qz` — a real-looking **fixture the live path cannot produce**, so the test passed while the property does not hold. Discoverable only via the cycle key in the bead title. |
| 9.5b | **Poisoned execstore record never GC'd** — a `pending` claim whose workflow already terminated is never re-Proposed, so 9.5's quarantine never fires on it | query (`pending older than the activity timeout is unresolvable by definition`) | **NONE.** An age-gate sweeper was **deliberately not built** (it risks quarantining a claim near the StartToClose boundary — a real trade, not an oversight). | The original 07-16 record (`56317233…`, claimed 16:43:20Z) **is still `pending` today and always will be**. Store now 12 `done` / 2 `failed` / 1 `pending`. Tombstone accumulation is unbounded in the terminal case. |
| 9.6 | **Orphaned graph.v2 finalize wedges dispatcher** | query | `orphaned-molecule-reaper` — re-queries the referenced bead's real status every run (`:145-147`), closes directly | reference_dispatcher_conversion_stall |
| 9.7 | **Reject-loop never converges** | query | fixed in mol-focus-review source (dr-5bk); runtime convergence unwitnessed | dr-5bk |
| 9.8 | **Convoy container outlives its target** | query | none (confirmed 4 rigs) | dr-oj4 |
| 9.9 | **Stale `GITHUB_TOKEN` env-shadow** — dead PAT shadows the valid one, fleet-wide gh block | query (`does gh api user match?`) | **NONE** watched identity | rca-github-token-envshadow (dr-e6l) |
| 9.10 | **Slack binding / handle alias staleness** (v1 gap) | query | `slack-binding-reaper` (5m), `slack-handle-alias-reaper` (10m) — both level-triggered, re-derived from live session state; self-healing and cheap | this pass (order definitions) |

---

## 10. The finding that decides the epic: timer-shape is mostly an artifact

v1 named three "genuine timer-shaped candidates" and called them Temporal's prima facie case. **All three dissolve under source inspection, in three different ways.**

- **8.5 (DOLT_PUSH durable timeout)** is not orchestration. It needs a `max-query-duration` inside the dolt server. A workflow engine cannot time out a query in a server it does not run. Misfiled.
- **6.4 (health-cache ~20s hang)** is an in-process cache expiry on a synchronous HTTP handler. Misfiled for the same reason.
- **9.4 (iterate no-completion → escalate)** is the interesting one, and it is **query-shaped, not timer-shaped**. "No completion in N hours" reads like a durable wakeup. It is a **scan** the moment the pending state is written somewhere a scan can see: *"any dispatched iterate bead with `dispatched_at` older than N and not closed?"* is a SQL question. It only looks timer-shaped because `pr-state-poller` records its pending state in a **local JSON cache** (`:234-241`) instead of as a queryable durable record. **The shape is a property of the implementation, not of the problem.**

The generalization, and the city already proves it:

> **Every durable wakeup in this city reduces to (a durable timestamp in the store) + (a periodic scan).**

The existence proof is the rate-limit backoff (§5.5), the one wakeup that demonstrably survives process death. It is not a timer service. It is a `quarantined_until` field on a bead plus `healExpiredTimersInfo` checking it against the clock on every reconciler tick (`cmd/gc/session_reconcile.go:440-458`). It survives crashes **because the deadline is in durable storage**, not because something stayed awake to fire it. The internal helper is even named `healExpiredTimers` — the city built durable timers as scans and did not notice it had answered this question.

The second proof is the two best covers in the fleet. `work-landing-reaper` and `close-gate-reaper` do the hardest verification the city performs — *did the artifact actually land in git* — with no timer, no marker, and no engine, by re-deriving from `git merge-base`/`cherry` and evidence metadata on every run. **They are bash.**

So the fix for §2's fire-and-forget family is not durable timers. It is to write the pending state down where a scan can see it, then scan. That is precisely the `nudge-on-route` → `routed-bead-nudger` pattern the city already wrote for itself.

**What survives, then, is exactly one thing.** If every leak is a scan, and scans run as orders, and orders die silently (gc-qo3), then the only irreducible gap is **where the scan-of-scans runs**. That is the substrate question, and it is the sole place the evidence favors an independent failure domain — which is exactly the one property P4 proved (survives `systemctl --user restart gascity-supervisor`, PIDs and state intact) and the one the epic recorded as real.

**But it does not follow that Temporal wins even there,** and Track E should not be allowed to skip this step. The logic already exists (`mayor-pattern-miner:243-296`); the gap is substrate and cadence, not logic. A systemd timer outside the supervisor buys the same failure-domain independence for roughly zero marginal state. Temporal's proven property is real but **not exclusive**, and the negative evidence is on the record: on `maintenance-cycle` it reduced to cron plus a lockfile (44s of work per 120m window, `SkippedOverlap: 0`, no long-lived state, no event wait, no crash to survive), and its fail-closed adapter **destroyed liveness** on crash — a poisoned `pending` key refused forever (`TerminalExecError, retryable:false`), workflow failed, orphan bead, and **nothing escalated**, with the next cycle looking healthy because the per-fire `cycleKey` erased the evidence (gc-4zf.4).

**Update 2026-07-17, and it sharpens rather than softens the point.** gc-4zf.4 is now fixed, deployed, and re-proven by live injection: the drop **escalates durably** and the poisoned claim is **quarantined with a reason**. That is a genuine win and it is Temporal's — the engine's redelivery is what creates the moment where a crashed claim can be noticed at all. But it bought exactly *half*. The orphan bead is still left open with `routed_to` unset (9.5a), the escalation cannot even name it (its `bead_ref` is fixed before the bead exists), and the poisoned record for an already-terminated workflow is never swept (9.5b). **Closing the loop needed a level-triggered scan — which is what `temporal-soak-check` now does, in bash, on a cooldown order.** So the split for Track E is clean and worth stating as the epic's sharpest single sentence: **the engine made the failure loud; a scan made it converge.** Durable execution is good at *noticing*; it is not what makes the state right. Any Track E proposal should be priced against that division, not against "durable execution vs nothing".

The city's own contradiction, worth stating plainly for Track E: **the work layer wants at-most-once fail-closed** (a duplicate PR is worse than a skipped cycle) **and the watchdog layer wants at-least-once with idempotent repairs** (re-nudge and re-scan are harmless). A watchdog built on `RealAdapter`/execstore semantics **would go silent exactly when things break** — which is the failure it was hired to prevent. Any substrate proposal that reuses the work layer's semantics for the watchdog layer is disqualified on this ground alone, whatever engine it runs on.

---

## 11. Ranked conclusion (feeds Tracks B / D / E)

**The bead's prediction holds, and the exhaustive pass narrows the case beyond v1.** The city's reliability problem is dominated by covers that check the wrong thing and by covers that share the substrate they guard. It is **not** a shortage of durable timers, and after §10 it is not clear there is a durable-timer problem here at all.

Now measured rather than asserted: **83 of 106 live orders never check whether work moved; 105 of 106 have no dedicated watchdog; and the single fleet-wide order-firing check is weekly, undocumented, skips itself, and is blind to every event-triggered order.**

1. **Convert liveness covers to outcome covers.** Highest value, purely query-shaped, no engine. The 30s/1m loops are the blindest (§1.1), and 9.1 is the archetype: the cover ran, stayed green, and watched the box die on the wrong metric. **Not a Temporal candidate.**
2. **Put the pending state in the store, then scan it.** Repairs §2's whole family (`pr-state-poller`, `nudge-on-route`, `help-request-surface`, `pl-human-gate-surface`) with the pattern already in-tree. **Removes v1's best timer case (9.4). Not a Temporal candidate.**
3. **The substrate: one non-order scan-of-scans, on an independent failure domain.** The only place the evidence favors independence. The logic exists (`mayor-pattern-miner`); the ask is relocation + cadence + covering event orders (4.4). **Temporal is one option; a systemd timer is the cheaper one. Track E must price the dual-state cost against a property Temporal does not hold exclusively.**
4. **Fix the mislabels.** 8.5 → dolt server config; 6.4 → in-process cache. Neither is orchestration. **Remove from the Temporal ledger.**
5. **Genuinely event-shaped, at integration boundaries** (6.1/6.2 bus drops, 9.5 terminal-pending, 8.1 conn death): Temporal helps only where the signal source is durable. **6.2 shows the city already answers this with a state scan and mislabels it as a bus guarantee** — fix the label before buying an engine.

**Uncovered and unranked, needing an owner:** 3.2 (routed-raw — named in source, no scan), 3.5 (gc-28jm, **open P0**, duplicate roots), 9.9 (token env-shadow, no watched identity), 4.4 (event orders unwatched).

**For Track B (fault injection).** Test the *covers*, not the code. The three highest-information injections, each targeting a cover this map shows is blind rather than a bug it shows is present:
  1. **Disable an order silently** (uncommented `enabled=false`, per gc-qo3) and measure time-to-detection. Prediction: up to a week (§1.3). If `mayor-pattern-miner` catches it sooner, §1.3 is wrong and the substrate case weakens.
  2. **Wedge a dispatched `mol-pr-iterate`** and confirm the review stays `handled` forever (§2). This is Track D's premise; prove it before building on it.
  3. **Drive MemAvailable high while swap → 0** and confirm `resource-sweep` stays green (9.1). Metric-blindness reproduces on demand.
  Metric per the epic: violation → durable detection → verified restoration. Note that injections 1 and 3 have **no detection step to measure** — that is the finding, and Track B should report a null detection rather than call the test inconclusive.

**For Track D (`pr-state-poller` → durable workflow).** Blocked-by gc-372 until the P5 soak closes (~2026-07-23); two live cutovers at once is how a controlled experiment stops being one. Carry §10 in as the null hypothesis: **the defect is that pending state lives in a JSON cache, not that bash lacks timers.** Writing `dispatched_at` to a bead and scanning it would close 9.4 with no engine. The walkthrough's own scope discipline already points this way — *"Keep the poll — demote it… Events give latency; reconciliation gives completeness… What dies is the memory, not the scan"* — and it concedes the latency win needs webhooks that do not exist. If Track D adopts a workflow engine, it must show what the scan cannot do, not what the current script does not do.

**For Track E (synthesis).** Weigh: Temporal's defensible surface is **one item (#3)**, its logic already exists, and its unique proven property is **failure-domain independence, which a systemd timer also provides**. Against that, the negatives are on the record and load-bearing: cron-plus-a-lockfile on `maintenance-cycle`, and a fail-closed adapter that destroyed liveness and escalated nothing. The epic said it must be able to conclude "don't" or "only here." **The evidence supports "only here, and possibly not even here."**

---

### Provenance and limits

- Sources: `/home/ds/gas-city/orders/` (116 files, all opened), `/home/ds/gas-city/bin/`, `/home/ds/gascity-main` at `origin/main` (the tree matching the installed `gc`; contributor branches deliberately not read), the bd store, and v1.
- **Unconfirmed, flagged not padded:** `gc-xk1hg` does not resolve (§0). The "regression test RED for 2 days" claim has no surviving artifact — gas-city is not a git repo, so **no incident in this map has git corroboration**. Detection method for the `gc-5wse`/`gc-r7c6` and `gc-hlyl`/`gc-647v` duplicate pairs is unknown; no bead records it.
- **Proven by injection, not by incident:** the gc-4zf.4 chaos-test result was deliberately induced (`gc-372`'s Schedule drove 4/4 scheduled cycles green; the only failed run was the chaos test). This is stronger evidence than an inferred symptom, not weaker, but it is not an organic production failure and should not be cited as one.
- **Zero automated cover at incident time** — every one of these was found by a human, which is the map's thesis restated as history: gc-s5c/gc-q4s/gc-4qz (human reading journals), gc-qo3 (engineer diffing on-disk vs registered orders during unrelated work), gc-4zf.4 (deliberate chaos test), gc-na2o (source-reading during an unrelated investigation).
- The liveness/outcome split (§1) is a judgment call at the margin; the ratio is robust to a few reclassifications, and the conclusion does not turn on 23 vs 26.
