# dr-fk4 — Hybrid-claim canary verification (EB pool, 2026-07-13 → 2026-07-19)

**Bead:** dr-fk4 · **Author:** city-infra-polecat-1 · **Date:** 2026-07-19

dr-dcd flipped the EnterpriseBench worker pool's routed-tier claim sort from
`--sort oldest` (the compiled gc default) to `--sort hybrid` on
2026-07-13T17:33Z (13:33 EDT) and closed without verification. This report is
the missing verification. Scope is measurement only; the fleet-wide decision is
held for Stephanie per dr-dcd.

## Verdict in one paragraph

The canary works as designed and did no attributable harm, but plain hybrid is
the wrong fleet-wide target. Within the sorted tier, hybrid measurably
reordered claims toward priority (pairwise concordance with the hybrid key 67%
vs 51% for the oldest key; both controls invert) and fresh P0s were claimed
faster than fresh P1s (median 1.2h vs 2.4h). However the 48h window is a
cliff, not a decay: every bead older than 48h ranks behind every fresh bead of
any priority, so the sorted tier claimed a >48h-old bead exactly once in 158
first-claims while 14 routed, dependency-free, unassigned beads (1 P0, 11 P1,
2 P2; ages 51h–210h) sit permanently parked behind fresh arrivals. Under the
old oldest-first default those beads would be at the head of the queue; hybrid
moved them to the back. Recommendation: **flip with changes** — target
priority-first ordering (`SortPolicyPriority`: priority ASC, created_at ASC
tie-break) via an upstream gc change rather than fleet-wide hybrid via
agent.toml overrides.

## Method and data provenance

- Claim events from the city Dolt server (port 29620), `events` tables,
  `event_type='claimed'`, window 2026-07-13 13:33 EDT → 2026-07-19 ~16:45 EDT.
  Real claim events, not self-report (acceptance 1).
- Rigs: EnterpriseBench (hybrid canary, 362 claims), controls mem (262) and
  gascity (229), both on the compiled oldest-first default. EB pre-flip
  (07-07 → 07-13, 83 claims) as a same-rig baseline.
- **Tier ground truth:** each `claimed` event's `old_value` holds the full
  pre-claim issue JSON. Pre-claim `assignee == actor` → resume tier;
  `assignee` empty + `gc.routed_to` = pool path → the sorted pool-demand tier
  (the only tier the canary changes); assignee empty + no route → legacy
  tiers (sorted oldest regardless of the canary). EB post-flip pool claims:
  **224 of 347 (65%) went through the sorted tier**, 113 legacy, 10 resume.
- Timestamp normalization: `issues.*` datetimes are UTC, `events.*` are EDT
  (verified: created-events lag `issues.created_at` by exactly 4h); all math
  done in one zone.
- Analysis scripts and raw CSV extracts preserved in the session scratchpad
  (`analyze.py`, `analyze2.py`, `analyze3.py`); every number below is
  reproducible from the `events`/`issues`/`dependencies` tables with the
  queries embedded in those scripts.

## Findings

### 1. The hybrid sort is live and does reorder claims (acceptance 1)

Pairwise order-concordance over co-queued sorted-tier first-claims (pairs
where the later-claimed bead already existed when the earlier claim happened):

| Rig (live sort) | pairs | hybrid-key concordance | oldest-key concordance |
|---|---|---|---|
| EB (hybrid) | 1126 | **67%** | 51% |
| mem (oldest) | 325 | 82% | **87%** |
| gascity (oldest) | 921 | 52% | 50% |

EB is the only rig where the hybrid key explains claim order better than the
oldest key, and the gap (+16pt) appears exactly where the canary is live. mem
shows the opposite (oldest wins, as expected of the default). gascity's queue
is mostly same-priority and short, so the keys are indistinguishable there.
Concordance is not 100% anywhere because the reconstruction cannot see
dependency-blocking or claim races; the *relative* pattern is the signal.

Wait time by priority, sorted-tier first-claims only:

| | P0 | P1 | P2 |
|---|---|---|---|
| EB hybrid, median wait | **1.2h** (n=54) | 2.4h (n=95) | 1.7h (n=9) |
| gascity oldest, median wait | 0.8h (n=41) | 1.9h (n=43) | 0.4h (n=87) |

In EB, P0 < P1 < queue as priority predicts. In the gascity control P2 is the
*fastest* class — under FIFO, priority does nothing and arrival order
dominates. (gascity's absolute waits are low because its queue is shallow, not
because FIFO serves P0 well.)

### 2. The 48h window: hybrid governs only the fresh sliver (acceptance 2)

- Sorted-tier claims of beads older than 48h: **1 of 158 first-claims
  (0.6%)**. The >48h-old claims visible in the raw totals (23 of 347) came
  through the legacy/resume tiers, not the sorted tier.
- The routed ready queue right now: 17 dependency-free, unassigned,
  pool-routed open beads, of which **14 (82%) are older than 48h** — 1 P0
  (EnterpriseBench-639lv, 61h), 11 P1 (up to 210h: jn73.10, vdeyx, 8cc10,
  mzve9, wv6bp, u90sn, 46rq8, d0jih, njqo4, 78zt5, agux1), 2 P2 (p5wrm,
  crryp).
- Because the window is `CASE WHEN created_at >= now-48h THEN priority ELSE
  999 END`, these 14 rank behind **every** fresh bead of **any** priority.
  Under sustained fresh arrivals (EB averaged ~58 claims/day post-flip) they
  never reach the head. This is not "priority becomes advisory after 48h" —
  it is an inversion of the old behaviour: oldest-first drained the backlog
  head-first; hybrid drains it last.

So: most P0s do NOT age past 48h (fresh P0s are claimed at 1.2h median — the
window is generous for the steady state), but the beads that DO age out are
exactly the ones a priority policy exists to protect, and for them hybrid is
strictly worse than the default it replaced.

Important scoping fact: EB's headline "100-deep P0 ready queue" is mostly NOT
a sort problem. Of 538 open never-claimed non-epic beads (459 older than 48h,
43 P0), only 17 are routed to the worker pool at all. The rest are invisible
to every sort policy because nothing routed them — that is dr-e83's backfill
problem. No sort flip will drain unrouted work.

### 3. Regressions the hold was protecting against (acceptance 3)

- **Starvation of old beads:** confirmed, but bounded — the 14 routed >48h
  beads above. This is the one real regression, and it is inherent to the
  window cliff, not an implementation bug.
- **Molecule step ordering:** no violations. Two apparent out-of-order
  sibling claims (EnterpriseBench-rryas.4 before .3, .6 before .5) turned out
  to be parallel siblings with only `parent-child` edges — no `blocks`
  dependency exists between them, so any order is legal. The only
  dependency-chained sibling in the sample (rryas.9 blocks rryas.8) was
  respected. Structurally, `bd ready` only surfaces dependency-free beads, so
  a sort policy cannot reorder chained steps; the data agrees.
- **Claim churn:** elevated in EB post-flip (224 sorted-tier claims over 158
  beads = 1.42 claims/bead, vs mem 1.20, gascity 1.25; whole-window EB
  re-claim rate 33% vs 17–21% elsewhere) but **not attributable to the
  sort**. The top churner is one session claiming the same bead 10× in 5.6h
  (a wedged claim-loop), and the multi-session churners sit exactly in the
  window of the EB dangling-work_dir/worktree-husk incidents (dr-6dc,
  dr-5g8/EnterpriseBench-j75m), which force unassign→re-route cycles. Claim
  order has no mechanical path to double-claims (claiming is
  assignee-CAS-gated); the churn correlates with EB's concurrent workspace
  bugs, not the canary.

### 4. The canary override itself is a maintenance hazard

The dr-dcd canary reproduces gc's entire compiled multi-tier work_query as a
~3KB inline shell string in `agents/enterprisebench-worker/agent.toml`,
diverging from `routedReadyTierCommand` the day the compiled default next
changes. It was the right tool for a canary; it is the wrong tool for a fleet
policy. A fleet flip should change the compiled default (gc
`internal/config/workquery.go` routedReadyTierCommand) / the bd sort policy
(`beads/internal/storage/sqlbuild/ready.go`), i.e. an upstream change, not 20+
agent.toml overrides.

## Recommendation (acceptance 4): flip with changes

1. **Do not flip plain hybrid fleet-wide.** Its 48h cliff demotes exactly the
   beads priority exists to protect, and it inverted backlog draining relative
   to the oldest-first default (finding 2).
2. **Target `SortPolicyPriority`** — priority ASC, created_at ASC tie-break,
   no window. Stale P0s outrank fresh P3s; ties drain FIFO. The residual risk
   (low-priority beads starving under sustained high-priority arrivals) is the
   *intended* semantics of priority, is the opposite of today's failure mode
   (P0s starving behind P3s), and can be softened later with true aging
   upstream if it bites.
3. **Route the change upstream** (gc compiled default / bd sort policy), not
   through per-agent work_query overrides. Until then, keep the EB canary
   running — it is doing no attributable harm and keeps generating data.
4. **Treat routing backfill (dr-e83) as the bigger lever** for EB's visible
   backlog: ~520 of EB's 538 never-claimed open beads are unrouted and no sort
   policy can reach them.
5. If hybrid is nevertheless preferred fleet-wide, widen the window well past
   the fleet's real queue-age distribution (p90 age of EB's routed ready queue
   is ~170h; a 48h window covers 18% of it) — but a window of any size keeps
   the cliff, so priority-first remains the cleaner fix.

Per dr-dcd and dr-fk4, this decision is Stephanie's; nothing has been flipped
anywhere, and no rig other than EB deviates from the compiled default.

## Caveats

- Priority at claim time is read from the claimed-event snapshot (exact); for
  the never-claimed backlog it is current-state (priority edits after filing
  would shift bucket counts slightly).
- The pairwise-concordance candidate sets cannot see dependency-blocking or
  `--limit=20` truncation at historical claim moments; both rigs' numbers
  carry the same bias, so cross-rig comparison is the load-bearing part.
- EB's workload roughly quadrupled across the flip (83 claims/6d pre vs
  362/6d post), so pre-flip vs post-flip comparisons are context, not
  evidence; the concurrent controls carry the comparison.
- 2026-07-18 had zero EB claims (pool idle/quota day); daily-rate statements
  average over it.
