# gc-4zf Track A — the city's reliability surface, classified by failure shape

_2026-07-16 · city-infra-pl · read-only analysis (bead gc-4zf.1). Evidence-backed, no symptom-theorizing (mechanic skill). v1: exhaustive on the evidenced surface; §7 lists the known gaps still to enumerate._

## The classification that carries the epic

Per gc-4zf.1, each stall gets a **failure shape**, because the shape decides the tool:

- **query-shaped** — level-triggered; answerable by a scan question against durable state ("any ready bead unclaimed > 30m?"). Needs no timer, cannot drift, re-derives itself every pass. **No Temporal case** — a periodic scan is strictly simpler and self-healing.
- **event-shaped** — edge-triggered; needs a signal, and signals get lost at integration boundaries, so it needs a reconciler too. Temporal helps only if the signal source is itself durable; otherwise the reconciler is query-shaped and does the real work.
- **timer-shaped** — genuinely needs a durable wakeup that survives process death ("no completion in N hours → escalate"). **The only shape with a prima facie Temporal case**, and even then see the substrate question.

**Substrate question (applied to every cover):** would the cover survive its own substrate dying? The patrols are *orders*; **orders are what silently die** (gc-qo3). A query-shaped leak guarded by an order has just moved the single point of failure, not removed it.

Headline finding, consistent with the bead's premise: **the large majority of this city's leaks are query-shaped**, and most already have cover. The recurring meta-defect is not missing timers — it is (a) covers that check **liveness** (is the process alive?) instead of **outcome** (did the artifact move?), and (b) covers whose **own substrate can die unnoticed**.

## 1. Dispatch / routing

| # | Where flow stops | Shape | Current cover (liveness vs outcome) | Evidence | Substrate question |
|---|---|---|---|---|---|
| 1.1 | **Bead created but never routed** — sits open, `gc.routed_to` unset, nothing claims it | query | **NONE** (a scan for `open AND routed_to=null AND age>N` would catch it) | gc-s5c, gc-q4s, gc-4qz (2026-07-16, found by a human reading journals) | n/a — no cover to test |
| 1.2 | **Ready bead unclaimed past threshold** | query | `bin/routed-bead-nudger` + readiness gate gc-xk1hg — **liveness** (nudges) | working; its regression test was RED 2 days (nothing ran it) until `orders/city-selftest.toml` | nudger is an order → dies silently; selftest now guards the test but selftest is *also* an order |
| 1.3 | **gc-sling writes metadata to the wrong rig** — `--rig ${bead%%-*}` (prefix ≠ name) fails silently; orphan worktrees + false "worktree-recorded" | query (audit-log scan) | **NONE for the false-green**; fixed at source 2026-07-16 | gc-na2o (this session): ~40 orphans/~2.8GB, 1384 false audit events | the audit log asserted success that never happened — a cover reading it would inherit the lie |
| 1.4 | **Slung to a dead/min=0 target** — `gc sling` exits 0, bead stamped, nothing claims | query | `bin/gc-sling` pre-flight dead-target guard (warn-only) | 2026-07-16 gascity/codex strand | guard is inline in the wrapper, not an order — survives; but warn-only, no durable record |
| 1.5 | **Fleet claims oldest-first, priority discarded** (hybrid-claim) | query | canary dr-dcd (closed unverified) → re-opened dr-fk4 | dr-fk4 | verification itself was skipped — the "cover" was a claim, not a check |

## 2. Orders / scheduler (the substrate under most covers)

| # | Where flow stops | Shape | Current cover | Evidence | Substrate question |
|---|---|---|---|---|---|
| 2.1 | **Order silently de-registered / disabled** — dormant, unnoticed | query ("did this order fire within 2× its cadence?") | `gc doctor: order-firing-current` flags *aggregate* staleness, not per-order dormancy; **no per-order cover** | gc-qo3: maintenance-cycle dormant **10 days** (city.toml `enabled=false`, uncommented → read as a bug); RCA this session | **the thing that would check it is itself an order** — the canonical substrate-death case |
| 2.2 | **Order-firing floor collapses** — scheduler stops firing | query/liveness | `beads-health`/`close-gate-reaper` cadence = the de-facto canary; `gc doctor` order-firing-current | 2026-07-14 filestore-bloat stall (`.gc/beads.json` 176MB reload-under-lock); RCA landed | the canary orders share the substrate that fails |
| 2.3 | **`gc order check` too slow to use** — times out at 90s on 152 orders | n/a (tooling) | none | this session (gc-qo3 investigation) | the health check for orders is itself unreliable at scale |

## 3. Sessions / workers

| # | Where flow stops | Shape | Current cover | Evidence | Substrate question |
|---|---|---|---|---|---|
| 3.1 | **Worker parked at a UI-modal** (slash/model/theme selector, permission prompt) — reads "active", unreachable | query (peek trailing prompt) | `bin/polecat-ui-stuck-scanner` (surface-only, AUTO_RESET off) — **liveness** | codescalebench gc-485664, EnterpriseBench/mem/dashboard (dr-3sz) | scanner is an order; and it detect-only, recovery is dr-3sz Parts 2/3 |
| 3.2 | **Worker stalls at a tool-invoked question** (`AskUserQuestion`) | prevented | `bin/claude-account` strips the tool from pool workers (dr-3sz Part 1) | this session | prevention at launch — no runtime cover needed |
| 3.3 | **Worker stalls at a startup modal** (external AGENTS.md import) | prevented | `claude-account` pre-accepts `hasClaudeMdExternalIncludesApproved` for pool workers (dr-oo8d) | 2 gascity pool sessions 2026-07-16 | prevention at launch |
| 3.4 | **Agent rate-limited (429)** — no Retry-After handling in runtime/session/worker | event (429 signal) → query (utilization scan) | `account-quota-warning` + `csu_pick` exclusions (static) | dr-785 (static exclusions never re-admit) | quota order can die; exclusions don't self-clear when headroom returns |
| 3.5 | **Worker boots then drains** — dirty work_dir, can't claim | query | none automated (manual stash-clean) | reference_worker_noclaim_dirty_workdir | — |
| 3.6 | **Reconciler spawns duplicate singleton-pool sessions** (alias collision) | query | mayor drains each cycle (stopgap) | dr-l66ch (gc-2568 mayor) | recurs on every restart; root is gc-source |

## 4. Supervisor

| # | Where flow stops | Shape | Current cover | Evidence | Substrate question |
|---|---|---|---|---|---|
| 4.1 | **Event-delivery starvation** — consumers back up (247 queued), dispatch reacts slowly, work stops while orders still fire | event (bus) | none direct; `dispatcher-watchdog`/`dispatcher-liveness-sensor` catch a *wedged dispatcher*, not slow event delivery | 2026-07-14 wedge RCA (events.jsonl 198MB, storehealth walks full history) | watchdogs are orders on the same supervisor |
| 4.2 | **`/v0/city/status` ~20s+ synchronous hang** drags reconcile/dispatch | timer-ish (health-cache expiry) | none; GC_PPROF now persistent for diagnosis | dr-7smz8 (storehealth LastMaintenance ListTail fix in flight) | — |
| 4.3 | **Supervisor process alive but wedged** (tmux state-cache starved) | query (log signature) | **`tmux-refresh-failure-sensor`** (built from dr-59p RCA) — watches the `tmux -u: context deadline exceeded` line | 2026-07-15 host death precursor (3m→18m tmux refresh) | sensor is an order; it watches a signal the supervisor emits ~45m before death |

## 5. Dolt / bead store

| # | Where flow stops | Shape | Current cover | Evidence | Substrate question |
|---|---|---|---|---|---|
| 5.1 | **Read-tx draws a reaped conn** (`withReadTx` bare BeginTx, no retry) | event (conn death) | upstream fix pending (SetConnMaxIdleTime + retry) | founding charter #2 | — |
| 5.2 | **Write-hang** — intermittent `gc mail send` hangs (auto_gc/write_timeout) | timer-ish | investigate/propose (charter #3) | — | — |
| 5.3 | **Dolt GC never runs → bloat → SIGKILL** | query (size) | `dolt-gc-maintenance` nightly | reference_dolt_gc_never_runs | GC is an order; if it dies, bloat returns silently |
| 5.4 | **File-store `.gc/beads.json` bloat → order stall** | query (size) | `beads-json-compact` (24h, COMPACT_APPLY=1) | 2026-07-14 RCA | compactor is an order (was manual-only — the durability gap that caused the stall) |
| 5.5 | **DOLT_PUSH network-hung zombies survive KILL QUERY, block DOLT_GC fleet-wide** | timer (durable timeout) | none (needs server-side max-query-duration + push timeout) | dr-ne8 (2026-07-13 incident) | **genuine timer-shaped candidate** |

## 6. Host / resource

| # | Where flow stops | Shape | Current cover | Evidence | Substrate question |
|---|---|---|---|---|---|
| 6.1 | **Whole-host memory-exhaustion death** — zero swap headroom, ~120% commit, unclean kernel death | query (trend: swap_free≈0 AND MemAvailable falling) | `resource-sweep` gates on MemAvailable<4G only — **missed it** (MemAvailable stayed 10–18G while swap sat at 0) | 2026-07-15 host death RCA; ~85m outage | resource-sweep is an order; and its metric (MemAvailable) was the wrong one |
| 6.2 | **oomd collateral** picks mayor/supervisor under pressure | query | `disk-pressure-guard`, `resource-sweep` (surface-only) | box_oom_gate_scix_postgres (Stephanie ledger) | orders on the box that's under pressure |

## 7. Molecule / workflow / review (partial — extend here)

| # | Where flow stops | Shape | Current cover | Evidence |
|---|---|---|---|---|
| 7.1 | **Reject-loop never converges** — retry re-dispatches review not body | query | fixed in mol-focus-review source (dr-5bk); runtime convergence unwitnessed | dr-5bk |
| 7.2 | **File-store workflow molecule has no worker-close path** → respawn loop | query | stopgap `bin/gc-filestore-close`; durable fix gc-source | dr-61j |
| 7.3 | **Convoy container outlives its target** — guard-bypass re-dispatch | query | none (confirmed 4 rigs) | dr-oj4 |
| 7.4 | **Copilot review lands, iterate wedges, review "handled" forever** | **timer** ("no completion in N hours → escalate") | `pr-state-poller` catches "review not noticed" but is fire-and-forget past dispatch | gc-4zf seed; Track D |
| 7.5 | **Temporal worker crash mid-sling silently drops a cycle** (poisoned pending key, fail-closed, no escalation) | event (terminal-pending) | none — the refusal is silent | gc-4zf.4 (chaos test 2026-07-16) |
| 7.6 | **Orphaned graph.v2 finalize wedges dispatcher** | query | `orphaned-molecule-reaper` / manual | reference_dispatcher_conversion_stall |
| 7.7 | **Closed bead, code never merged to main** — `mol-focus-review` finalize commits to `work/*`, closes, drains; **no landing step** and no controller reconciler. Downstream audits inspect main, find the bug still there, re-file it | **query** ("any closed bead whose `work_branch` isn't an ancestor of main?" — a merge-base check) | **`work-landing-reaper`** (daily, **outcome** — the one cover that reads git not bead-status) — but **report-mode only** (no `--apply`), and it is an order (substrate-death exposure). Built AFTER the RCA. | rca-eb-merge-gap (EnterpriseBench-8krz5 score-forgery fix closed `pass`, stranded; ~12 branches/day strand rate) |

**8. Auth / integration boundaries**

| # | Where flow stops | Shape | Current cover | Evidence |
|---|---|---|---|---|
| 8.1 | **Stale `GITHUB_TOKEN` env-shadow** — a dead `ghp_` PAT exported into the process tree shadows the valid stored PAT → every spawned process auths `gh` as the wrong/dead identity → fleet-wide gh-queue block | query ("does `gh api user` match the expected login?") | **NONE** watched identity; fix was `set-environment -gu` on the tmux global env (new spawns only) | rca-github-token-envshadow (dr-e6l; blocked #2723/#3958/#4003/#3420) |

## 9. The cover set classified — liveness vs outcome (quantifies the thesis)

All ~48 patrol/reaper/sensor/guard/audit orders, split by **what they actually check**:

- **OUTCOME** (did the work-artifact move/land?) — the correct shape: `work-landing-reaper` (code in main), `flow-patrol` (closed-24h/rig), `close-gate-reaper` (evidence-gate on closes), `gate-sweep` (pending gates), `decision-staleness-reaper` (dec subject resolved), `velocity-audit-sensor` (commit/churn), `issue-merge-watcher` (covers_issue), `stall-watch` (stalled work). **~8.**
- **LIVENESS** (is the process/session alive/unwedged?) — the weaker shape: `dispatcher-liveness-sensor`, `dispatcher-watchdog`, `mayor-watchdog`, `polecat-ui-stuck-scanner`, `login-wedge-scanner`, `tmux-refresh-failure-sensor`, `stale-claim-reaper`, `nudge-poll-reaper`, `stale-scix-mcp-reaper`, `routed-bead-nudger`, `morning-triage-watchdog`. **~11.** These answer "is it up?" not "did work flow?" — a liveness-green worker can still produce nothing (the modal-stall class 3.1, the routed-bead-nudger that nudges but never verifies a claim).
- **RESOURCE/HYGIENE**: `disk-pressure-guard`, `resource-sweep`, `tmp-reaper`, `bead-prune-reaper`, `stale-worktree-reaper`, `slack-binding-reaper`, `slack-handle-alias-reaper`. **~7.**
- **DRIFT/AUDIT** (periodic scans): `agents-claude-md-audit`, `mcp-audit`, `memory-audit-issues`, `codebase-audit-monthly`, `pl-deep-audit-weekly`, `pl-flow-audit-daily`, `unlabeled-assignee-sensor`. **~7.**

**Two structural facts fall out:**

1. **Liveness covers outnumber outcome covers (~11 vs ~8).** The city watches "is it alive" more than "did work land," which is why liveness-green-but-stalled (3.1) and closed-but-unlanded (7.7, cover is report-only) both slip through. dr-t4w0 ("liveness by ARTIFACT movement, never bead status") is the standing bead for this exact shift.
2. **Every single cover is an order.** The only thing resembling a substrate guard is `city-selftest` (runs the `bin/*.test` backstops every 6h — it guards the covers' *correctness*), and even it is an order. **There is no non-order watchdog over "did each order fire within 2× its cadence."** That is the gc-qo3 hole, and it is the map's #1 recommendation — the single place the substrate argument favors a Temporal Schedule (something that is not itself an order).

## 10. Comms / escalation delivery (both well-covered — recorded for completeness)

| # | Where flow stops | Shape | Current cover | Notes |
|---|---|---|---|---|
| 10.1 | **Escalation raised but never delivered** — a `severity:escalate` rollup or worker help-request that no one sees | **event** (bead.blocked / help_request), **reconciled by query** | `help-request-surface` (event) + `escalate-surfacer` (query: "OPEN `severity:escalate` lacking `delivered` label" every 15m) | **The reference pattern.** Event delivery for latency + a periodic query reconciler for the lost-signal case. This is exactly what event-shaped leaks SHOULD look like; the 247-event backlog (4.1) was event-*bus* pressure, not a missing reconciler. Both are orders (substrate exposure). |
| 10.2 | **Slack binding points at a dead session** — messages to a channel never reach the live PL | query (binding → session liveness) | `slack-binding-reaper` (rebind to live session) + `slack-handle-alias-reaper` (10m re-register aliases) | Query-shaped, covered. Orders. |

**Enumeration status: complete for the evidenced surface.** Every place work is known to stop through this city (2026-07-16 evidence horizon) is mapped across §1–§10, classified by shape, mapped to cover with the liveness-vs-outcome distinction (§9), and evidence-cited. New failure shapes will appear as the city changes; the method above is the template for adding them.

> **7.7 is the archetype of this map's thesis.** `bd close` is the terminal signal for *all* city automation, and it is a **status** signal, not an **outcome** signal — the bead even carries `work_branch`/`diff_stat`/`impl_complete`, but no consumer runs a merge-base check. Converting the close-gate (or a new reaper) to verify artifact landing, not bead status, is the highest-value reliability change the map surfaces, and it is purely query-shaped — no Temporal.

## Ranked conclusion (where Temporal earns its weight — feeds Track E)

1. **Most leaks are query-shaped and already covered** — the ROI is not Temporal, it is (a) converting liveness-covers to **outcome/artifact-movement** covers (dr-t4w0 is exactly this), and (b) fixing the **substrate-death** class: a cover that is an order cannot guard against orders dying (gc-qo3). A single non-order watchdog (or Temporal Schedule) over "did each order fire within 2× cadence" is the highest-leverage reliability change, and it is the one place the substrate argument favors Temporal.
2. **Genuine timer-shaped candidates** (the only prima facie Temporal cases): 5.5 (DOLT_PUSH durable timeout), 7.4 (iterate no-completion escalate), 4.2 (health-cache), 5.5 already has a bead. These are few.
3. **event-shaped at integration boundaries** (4.1 event bus, 7.5 terminal-pending, 5.1 conn death) — Temporal helps only where the signal source is durable; most need a reconciler that is itself query-shaped.

The map's load-bearing claim for the epic: **the city's reliability problem is dominated by covers that check the wrong thing (liveness not outcome) and by covers that share the substrate they guard — not by a shortage of durable timers.** Temporal's defensible surface is narrow (the order-substrate watchdog + a handful of timer-shaped escalations), which is the finding Track E must weigh against dual-state cost.
