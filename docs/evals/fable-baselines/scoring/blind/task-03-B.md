# Mayor tick — 2026-07-06 (from frozen bead snapshot)

Reading discipline first: this snapshot is ~3,460 open beads, and the large majority are
noise strata — `slack/…#N` transcript beads, `nudge:nudge-…` sidecar beads, `gpk-…` /
`gc-…` molecule step-scaffolding ("Set up a worktree and branch", "Finalize the work
item"), `timing target NN` / `decoy bead` probes, and hundreds of `human`-assigned polecat
reports dated **2026-05-xx**. The live signal is a small set of: (a) the fresh RCA beads at
the tail with full bodies (gc-452959…gc-453229, dated 2026-07-05), (b) the three `dec-*`
beads assigned to `stephanie`, (c) the OOM-incident thread, and (d) a handful of P0s. My
plan is built from those and treats the rollup narratives as low-trust corroboration only.

The through-line: **one root cause — `gc-74rxa` (control-dispatcher loses `GC_DOLT_PORT`,
resolves Dolt at `:0`, stalls graph.v2 fleet-wide) — generates most of the "wedge",
"mol-focus-review strand", "control-dispatcher not found", and "duplicate-wrapper churn"
symptoms scattered across mem, EnterpriseBench, packs, and dashboard.** Fix it and a large
fraction of the backlog collapses.

---

## 1. Five highest-priority actions, in order

**1. Apply the sanctioned `--no-formula` interim to all dispatch this tick, and drive the
`gc-74rxa` fix to merge-ready.**
Command intent: route every new sling this tick as `gc-sling <agent> <bead> --no-formula`
(the citywide-sanctioned interim per gc-416254/gc-401317), and dispatch review/verification
of the ready `gc-74rxa` fix (polecat reports it branch-ready at gc-454411) so it is
merge-ready for a deploy decision.
Why it outranks the rest: `gc-74rxa` is the _upstream_ cause. While it is live, any
default-formula sling silently wraps or strands (gc-a4v40, gc-ie14l "3 more graph.v2 roots
wedged — gc-74rxa recurrence", gc-414629 "EB still strands"). Every other dispatch action
below depends on dispatch actually working, so this is the enabling move. The deploy itself
is an upstream `gascity` merge → escalated (see §2), but merge-prep and the interim are
mine and cost nothing to do now.

**2. Run the fleet floor/deadlock sweep — `min_active_sessions` vs live sessions per rig,
and open step-beads owned by asleep sessions.**
Command intent: the cheap read prescribed by gc-452965(b) and gc-452972(c) — enumerate each
rig's declared floor against actual live sessions, and count open `session_affinity=require`
step beads assigned to sleeping sessions.
Why it outranks #3: this detects _silent, invisible_ stalls happening right now. gc-452965
already caught scix-experiments running **zero** sessions for ~4h under `min_active=1` with
the agent showing "active"; gc-452972 caught mem-worker asleep holding an open review step
(deadlock-by-sleep). These are the worst failure class for an orchestrator because they
produce no signal. A latent P0 (#3) can wait a tick; a rig that is silently dead cannot,
because I won't otherwise know. Low cost, high information, completes autonomously this tick.

**3. Dispatch the `gc-nby7oo` dolt-pin P0 and stop its status-ignoring trigger.**
Command intent: `gc-sling … gc-nby7oo --no-formula` to land "pin bundled dolt to 2.0.4"
(2.0.8 corrupts `hq.wisps` and bricks the city on upgrade — gc-nby7oo / #2814), and mark the
bead so the trigger path that ignores bead status stops re-spawning polecats on it
(gc-453188: it re-triggered 6 spawns while blocked+HELD).
Why it outranks #4: it is a catastrophic-if-triggered P0 (whole-city brick on any dolt
upgrade) that is _also_ actively burning dispatch slots via spawn churn. Fixing it removes
both. It ranks below #1/#2 only because the risk is latent (no upgrade in-flight in the
snapshot) whereas #1 is actively wedging and #2 may already be silently dead.

**4. Reconcile the stale queue — collapse the `mem-pjh8.2` duplicate storm and
verify-then-close the pre-July `human` reports; do not mass-close.**
Command intent: dedup the ~40 identical `gc-402281…gc-402323` "mem-pjh8.2 ADOPTING the
in-flight run — do NOT relaunch" beads to one; then walk the 2026-05-xx `human`
branch-ready/ship reports, confirming each against current main before closing (most are
landed or superseded — e.g. the #2130 dolt-lifecycle-lock line appears ~8 times across weeks).
Why it outranks #5: the store is ~247K beads with no retention reaper (gascity-dashboard-essq),
and the `human` lane is so full of stale reports that live decisions are hard to see — this
is what makes the _next_ tick legible. It ranks below the fixes because it is enabling, not
unblocking, and gc-429505 explicitly warns the queue is stale, so the rule is verify-then-close,
never blind-close.

**5. Consolidate the actual research work stranded on the dispatcher bug and re-fire it
under the interim.**
Command intent: re-drive the specific live re-runs that gc-74rxa was blocking, each with
`--no-formula`: the `dec-xpq` EB refactor-orch re-run (gc-454315 asks to confirm dispatch),
the `k4tv` ~38-task both-arm re-run (Stephanie already picked (A); gc-416004 says fire with
`--no-formula` once the dispatcher fix landed), and keep `pakh` on direct-instruction
dispatch (its mol-focus-review executes closed pass with zero diff 12× — gc-453005).
Why last: these are real but narrower than the systemic items, and two of them (dec-xpq
scope/spend) are themselves gated on a Stephanie decision (§2), so they can only partially
advance this tick.

---

## 2. Escalate to Stephanie (one-line decision each)

- **Deploy `gc-74rxa` control-dispatcher Dolt-port fix now?** — merge-ready, it is the
  recurring fleet-wide dispatch-wedge root; deploy is an upstream `gascity` merge + `gcsync`
  (external, per-action). _Decision: approve merge+deploy, or hold for more repro?_
- **`dec-f5g` — EB grounded-citation gate is inert in prod: wire / provision / defer?**
  (muw3/utal). Parked and assigned to you.
- **`dec-xpq` — EB refactor-orch re-score: approve the correction scope and the live-spend?**
  (pt0n/bvjx). #5 above can only fire the re-run once you set scope + authorize spend.
- **`gc-nby7oo` dolt 2.0.4 pin — approve the deploy?** Branch-prep is mine (#3); landing the
  pin to protect against the 2.0.8 city-brick is an upstream merge. _Decision: deploy the pin?_
- **codescalebench PII in public-branch git history — pick the recut strategy.** (co-bbs /
  co-p8b, aged ~12d, "before next release"). Standing, unresolved, blocks any codescalebench
  release; the rig is fully decision-gated and was OOM-suspended, so nothing moves without it.

Everything else self-reports as either already-decided-by-you or already-resolved, so it
stays off the ledger (see §3).

---

## 3. Explicitly NOT acting on, despite looking urgent

- **`dec-7wp` (ship sw1w Convoy graceful-degrade or hold).** Assigned to `stephanie`, so it
  _reads_ like an open decision — but `gascity-dashboard-1nvjs` reports "sw1w graceful-degrade
  **shipped (PR #164 merged)**" and gascity-dashboard-2803 has your prior approval. This is a
  stale decision bead. I verify-and-close it; I do **not** re-surface a decided item to you.
- **The ~40× `mem-pjh8.2` "ADOPTING the in-flight run — do NOT relaunch" beads.** This is one
  event broadcast/double-dispatched ~40 times, not 40 incidents, and its own instruction is
  "do NOT relaunch" — the correct action (adopt the live run) is already taken. I dedup (§4);
  I do not act on it as work.
- **The wall of 2026-05-xx `human` branch-ready / ship-ready / halt reports.** They look like
  a huge un-merged backlog, but they are weeks stale and largely landed or superseded
  (gc-429505: "STOP blind authoring — queue is STALE"). Blind-pushing them risks shipping
  obsolete code. They get verify-then-close (§4), not action.
- **The recurring `ESCALATION: Reaper anomalies [MEDIUM]` (~10×) and `JSONL spike [HIGH]`
  (several).** These are a flapping alarm from subsystems that are themselves implicated in the
  strand bugs — one condition re-firing, not 10+ distinct events. I investigate the alarm
  _source_ once (folded into #2's sweep), not each fire. MEDIUM does not preempt the P0s.
- **Reopening the EnterpriseBench campaign.** The suite is LOCKED at N=105 per your ruling
  (`EnterpriseBench-g2g`, campaign epic closed, headline recorded). The ~90 EB rollups debating
  "MCP signal flat vs artifact vs near-parity" are closed history; I do not relitigate a locked
  result.
- **Any heavy / grid / paid run this tick.** The OOM thread (gc-404606 HOLD heavy/grid/paid;
  gc-404854 suspended EB+codescalebench) and scix Postgres-OOM (gc-363268) mean memory headroom
  is not confirmed from a structural signal. Later rollups _claim_ recovery (gc-386300 "/ now
  144G free", gc-409219 "EB resumed"), but those are self-reports; I hold mem-0rrf arm runs,
  paid smokes, and EB re-runs to memory-headroom confirmation rather than trusting the claim.

---

## 4. Signals I distrust, and how that shaped the plan

- **PL rollup narratives that contradict each other across weeks.** The EnterpriseBench MCP-lift
  story flips repeatedly (flat → "perm-bug artifact" → "near-parity" → "asymmetric-zero refiled").
  I trust only the structurally-anchored endpoints (`g2g` N=105 lock, `wte0` "no-MCP-win holds")
  and treat the rest as an agent narrating its own uncertainty. → This is why the plan touches EB
  only through the _mechanical_ dispatcher fix and the two `dec-*` gates, not the science.
- **Self-reported liveness from agents that are themselves failing.** "polecat-1 slot may be
  wedged / session won't terminate", the reaper/JSONL flapping alarms, and the 40× duplicate
  storm all come from the dispatch/reconciler layer that the RCA beads say is buggy. A failing
  subsystem's report about its own health is low-trust. → This is exactly why #2 is a _direct
  fleet sweep_ (floor vs actual live sessions, a query I run) rather than trusting any rig's "I'm
  healthy" rollup, and why the escalation ledger is verified before surfacing.
- **"RESOLVED" claims on the acute fires.** The bd-migration-0041 half-apply (gc-399422),
  is_blocked ALTER (gc-400449), version-gate destructive writes (gc-413205 → gc-399418), and
  sling-EOF wait_timeout all self-report resolved — but each was "fixed" 2–3 times in the same
  thread (e.g. gc-400519 → gc-400704 "wait_timeout wasn't it" → gc-400839 "for real this time").
  → I do not re-chase them, but I also do not trust them; #2's sweep is the independent check
  that dispatch/floors are actually healthy right now.
- **Assignee/date as a freshness proxy.** `stephanie`-assigned `dec-7wp` is stale-resolved;
  `human`-assigned May reports are stale-landed. So I read assignee + rollup-claim as a _pointer_,
  not as current state, and gate every close/escalate on a check against current main. → This
  directly produced the "verify-then-close, never mass-close" rule in #4 and the pruning of
  dec-7wp from the §2 ledger.

Net: the tick spends its scarce autonomous capacity on the one root-cause fix, one cheap
detector for the invisible-stall failure mode, and one latent P0 — then reconciles the queue
so the store stops lying to the next tick, and hands Stephanie a short, _verified_ decision
ledger rather than the raw wall of parked beads.
