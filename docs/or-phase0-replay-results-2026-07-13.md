# OR phase 0 — `bin/or-replay` results: dispatch-policy replay over the event log

- **Bead:** dr-8sy (P0). **Artifact:** `bin/or-replay` (read-only). **Date:** 2026-07-13
- **Author:** city-infra-polecat (gc-480328)
- **Spec:** `docs/design/fable-plans/2026-07-11/or-phase1-dispatch-priority-index.md` §3, acceptance §3.4
- **Weights version:** or-p1-w1 (1.00·f_prio + 0.10·f_due + 0.08·f_unblock + 0.06·f_age; W_due=72h, C_dep=5, A_max=14d)

## What was built

`bin/or-replay` — a read-only, fixed-trace discrete-event replay simulator over
this city's event bus (`.gc/events.jsonl` + `.gc/events.jsonl.archive-*.gz`). It

1. **extracts** per-pool work-bead traces (arrival / claim / complete /
   trace-fixed service / features) from `bead.created|updated|closed` payload
   snapshots. `dependent_count` is **recomputed** from the evolving blocks-dep
   graph, never trusted from the (usually-null) bd field;
2. **replays** the identical fixed trace under **four** claim policies;
3. **reports** F_w, unblocking throughput, per-class p95 tails, completion parity;
4. ships **LCO-style known-optimum fixtures** as `or-replay test` (9 unit checks).

READ-ONLY: no config flips, no bd writes, no event-log writes. The live
oldest→hybrid decision stays HELD; this only produces the numbers.

### The four policies (bd sort semantics verified empirically against the live store)

| id | policy | order |
|----|--------|-------|
| oldest   | current fleet FCFS (`bd ready --sort oldest`) | `created_at` asc, priority-blind |
| hybrid   | `bd ready --sort hybrid`   | priority band, then **oldest**-within-band |
| priority | `bd ready --sort priority` | priority band, then **newest**-within-band |
| index    | phase-1 index (or-p1-w1)   | `S(b)` desc, tie-break `created_at` asc, `id` |

### Simulator model (why it is faithful)

The **claim-slot model**: a pool's real claim timestamps are the moments a
server became free. That schedule is held fixed; the policy only chooses which
*currently-ready* bead fills each freed slot. Arrivals, service durations and
the dependency graph are all trace-fixed — the **only** degree of freedom across
policies is claim order (exactly the spec's "replay observed concurrency; only
claim order is free"). In-population blocks-deps gate readiness in-sim (a
dependent cannot be claimed before its blocker's counterfactual completion);
out-of-population blockers unblock at their real completion time.

**Validation:** under `oldest`, replayed claim times reproduce the *real* claim
times to a **0.37h median absolute error** (polecat pool) — the model
reconstructs the actual FCFS baseline, so deltas off it are trustworthy. An
earlier naive G/G/S model (S = peak concurrency) was rejected: it over-provisions
servers, drives max in-sim queue depth to 1, and makes every policy identical —
a model artifact, not a finding. The slot model reproduces real contention.

## Results — full history

Command: `bin/or-replay run --all --by-week`
Span **2026-04-27 → 2026-07-13** (~11 weeks), **3.0M** bead events parsed,
**1286** replayable work beads across **22** pools, 40s wall.

### Overall (1280 beads with complete traces)

| policy | F_w | vs oldest | unblkTput | parity |
|--------|-----|-----------|-----------|--------|
| oldest   | 6.84h | +0.0% | 42.05 | 6 unfinished* |
| hybrid   | 6.70h | **+2.0%** | 42.05 | 6* |
| priority | 6.70h | **+2.0%** | 42.04 | 6* |
| index    | 6.70h | **+2.1%** | 42.06 | 6* |

`index vs oldest +2.1%` · `index vs priority +0.1%` · `index vs hybrid +0.0%`

Per-class p95 flow (starvation guard): the priority-aware policies improve the
**P1 tail** (20.4h→18.8h) at a modest **P2 tail** cost (23.2h→25.2h, +8.3%, under
the §3.4 10% guard); P0/P3 stable, P4 +5.3%.

\*Completion parity: **6/1286 (0.5%)** beads never complete — all in
`zeldascension-worker`, a real **dependency cycle** in the bead graph
(`zeldascension-lhlo.8` blocks `.9` and `.9` blocks `.8`). Policy-independent
(identical 6 under all four), a pre-existing data artifact, not a sim bug; the
simulator correctly declines to deadlock on it.

### Per-pool (where contention actually lives)

| pool | beads | oldest F_w | index F_w | index vs oldest |
|------|-------|-----------|-----------|-----------------|
| enterprisebench-worker | 148 | 3.33h | 2.99h | **+10.1%** |
| zeldascension-worker   | 29  | 1.40h | 1.28h | +8.7% |
| gascity-packs-polecat  | 164 | 12.34h | 11.86h | +3.9% |
| polecat                | 725 | 5.76h | 5.72h | +0.7% |
| mem-worker             | 107 | 2.12h | 2.11h | +0.4% |
| gascity-dashboard-worker | 41 | 0.97h | 0.97h | +0.5% |

Gains concentrate in the genuinely-contended pools (EnterpriseBench +10.1%);
the large polecat pool is nearly capacity-unbound (+0.7%), so order barely moves it.

### Per-week (index vs oldest F_w)

W21 +3.9% · W22 +4.6% · W23 +0.0% · W24 +0.2% · W25 +2.8% · W26 +0.0% ·
W27 +0.8% · W28 +4.1%. Never approaches the §3.4 15% bar.

## Verdict against acceptance §3.4

1. **index beats oldest by ≥15%?** **NO** — +2.1% overall (best pool +10.1%,
   best week +4.6%). Fails the 15% F_w bar.
2. **index beats priority (B1) by ≥5%?** **NO** — +0.1%. Per §3.4.2 this means
   **ship the one-word `--sort priority`/`--sort hybrid` swap, not the
   four-feature index** — the SWAY bar doing its job: the extra due/unblock/age
   features add essentially nothing over pure priority banding on this traffic.
3. **No class p95 degrades >10%?** Holds (worst is P2 +8.3%). Parity: the only
   incompletions are the pre-existing zeldascension cycle.

**Bottom line:** on this city's real 11-week traffic the expensive index is not
justified. If anything ships, it is the one-word `oldest → hybrid` (or
`priority`) sort for a ~2% priority-weighted flow-time gain that improves the P1
tail — concentrated in the few contended pools (EnterpriseBench). The decision
stays with the mayor/Stephanie; this is the evidence the choice was held for.

## Reproduce

```bash
bin/or-replay test                       # 9 LCO known-optimum fixtures
bin/or-replay run                        # live log only (fast)
bin/or-replay run --all --by-week        # full 11-week history (~40s)
bin/or-replay run --pool enterprisebench # one pool
bin/or-replay extract --all --json /tmp/traces.json
```
