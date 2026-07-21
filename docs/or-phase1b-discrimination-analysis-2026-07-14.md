# OR phase 1b — can this city DISCRIMINATE dispatch policies? Noise-floor & experiment-design analysis

- **Bead:** gc-485661 (research task, re-routed off Codex, which is rate-limited until 2026-07-19). **Gates:** dr-94s (canary decision, window 2026-07-14 → 2026-07-16).
- **Author:** city-infra-polecat (gc-487174). **Date:** 2026-07-14. **Read-only.**
- **Inputs:** `bin/or-replay` (validated model, 0.37h median claim-time error under FCFS), full event corpus `.gc/events.jsonl` + 23 archives (3.0M events).
- **Corpus:** 1399 replayable work beads, 22 pools, 2026-04-27 → 2026-07-14 (~11 weeks).
- **Reproduce:** `docs/or-analysis-2026-07-14/discriminate.py` and `q1_power.py` (both import `bin/or-replay` and reuse its model verbatim). Run over `bin/or-replay extract --all --json <traces.json>`.

## TL;DR

The performance question is **answerable, and already answered**, by the replay: `--sort hybrid` beats FCFS by **+2 to +3% priority-weighted flow time**, a real but tiny effect concentrated in two contended pools. The **live A/B (canary) is formally unsatisfiable for the value question** and should be scoped to live-path safety only. The **leverage is not in the sort**: one wedged bead costs more flow-time than reordering every claim for 11 weeks.

Recommendation: **(ii)** — roll the one-word `--sort hybrid` swap on replay evidence, declare the canary's *value* mission unsatisfiable (keep only its *safety* mission), and redirect OR effort to the heavy service-time tail. Details in §6.

---

## 1. Is the effect even there? (noise floor)

**Yes, but it sits far below the live noise floor.**

The between-policy signal on the whole corpus:

| policy | F_w (weighted mean flow) | vs oldest |
|--------|--------------------------|-----------|
| oldest (FCFS) | 5.906h | — |
| hybrid | 5.715h | **+3.24%** (+0.191h) |
| priority | 5.717h | +3.20% |

(Phase-0 reported +2.0% on the 2026-07-13 corpus of 1286 beads; the extra day added contended beads. Both figures are the same order of magnitude and the conclusion is identical.)

The noise, measured as **week-to-week F_w under a FIXED policy** (FCFS), is enormous:

| scope | weekly F_w mean | weekly F_w std | CV |
|-------|-----------------|----------------|----|
| citywide | 6.59h | 8.06h | **122%** |
| enterprisebench | 2.29h | 1.44h | 63% |
| gascity-packs-polecat | 6.24h | 5.99h | 96% |

Weekly FCFS F_w series (citywide) — the swing is real, not a single artifact:

```
W21 7.28h  W22 0.79h  W23 1.18h  W24 26.76h  W25 7.84h
W26 5.62h  W27 1.94h  W28 6.33h  W29 1.56h
```

W24 alone is 26.76h against a ~1h neighbour week, a **30× swing under the same policy**. The root cause is a viciously heavy service-time tail:

```
service hours:  median 0.10   p90 0.65   p99 109.8   max 530.7   mean 3.08   std 26.21
```

A handful of multi-day beads (max = 530h = 22 days) dominate whatever week they complete in. The between-policy effect (~0.19h) is **~40× smaller than the between-week std** (8.06h).

### Required live A/B sample size (α=0.05, power=0.8, detect +2%)

Two-arm test, `n = 2·(z_α+z_β)²·σ²/δ²`, δ = 2% of mean:

| unit of analysis | σ | required N per arm | in city-time |
|------------------|---|--------------------|--------------|
| weekly F_w (citywide) | 8.06h | **58,709 weeks** | ~5.5M beads/arm |
| weekly F_w (enterprisebench) | 1.44h | 15,524 weeks | ~0.93M beads/arm |
| weekly F_w (packs) | 5.99h | 36,155 weeks | ~0.98M beads/arm |
| per-bead flow (citywide) | 47.4h (CV 693%) | **1,884,559 beads** | ~285 years |

This city produces **~127 beads/week (~6,585/year)**. Both units of analysis put the required sample at **~285× the city's annual output per arm** (≈570× total). **The live A/B is unsatisfiable by 2–3 orders of magnitude.** This is the finding the bead anticipated ("more beads than this city will produce in a year"): confirmed, and then some.

Note the replay does *not* fight this noise: it holds the trace fixed, so it is a **paired** design. Block-bootstrap (resample weeks, 5000×) on the paired weekly delta:

| scope | 95% CI on weekly (oldest−hybrid) delta | P(delta>0) |
|-------|----------------------------------------|------------|
| citywide | [+0.026h, +0.233h] | **1.00** |
| enterprisebench | [+0.000h, +0.404h] | 0.93 |
| packs | [+0.018h, +0.870h] | 0.99 |

The replay resolves the sign with confidence precisely *because* it pairs; a live A/B cannot pair away the tail.

## 2. Is the traffic structurally non-discriminating? (the ceiling)

**Mostly yes.** Over 11 weeks the workload posed a scheduling *choice* (a freed slot with ≥2 ready beads) only **758 times**, and of those only **124 (16.4%) present a genuine priority-vs-arrival disagreement**. The rest of the 1399 claims happened with 0 or 1 bead ready — no choice, policy irrelevant.

```
decision points (freed slot, ≥2 ready)     758
  FCFS pick == priority pick (AGREE)        634   (83.6%)
  DISAGREE (workload poses the question)    124   (16.4%)   ← the discrimination ceiling
```

Every disagreement is priority-improving (by construction: the oldest ready bead is not the top-priority one). The disagreements are **heavily concentrated in the two contended pools**:

| pool | decision points | disagree | disagree % |
|------|-----------------|----------|-----------|
| polecat (725-bead, capacity-unbound) | 418 | 14 | **3.3%** |
| gascity-packs-polecat | 145 | 55 | **37.9%** |
| enterprisebench-worker | 113 | 40 | **35.4%** |
| mem-worker | 37 | 5 | 13.5% |

This is the mechanism behind phase-0's per-pool split (enterprisebench +10.1%, polecat +0.7%): the big pool almost never poses the question (3.3%), the contended pools pose it a third of the time. So the honest read is **not** "the workload never discriminates" — it discriminates ~124 times, concentrated in EB and packs. It is "the discriminating question is rare and pooled, which caps the aggregate effect at the ~2-3% we measured."

## 3. If discriminating in principle, design the experiment

The value effect is discriminating in principle (§2) but unmeasurable live (§1). Evaluating the bead's design menu against that:

- **Interleaved / switchback (alternate policy per time-block):** does not save it. The noise is a heavy tail that arrives at random weeks (W24 = 26.76h). No block length cancels a 530h bead that lands in one arm; adjacent weeks already swing 10–30×. Switchback reduces *seasonal* confounds, not *tail* variance, and the tail is the whole problem.
- **Paired replay + bootstrap CI:** this is the only design that works, and it is done (§1 bootstrap). It resolves the sign because it holds the trace fixed. It is retrospective, not prospective, which is acceptable here: the mechanism is deterministic given a trace.
- **Within-pool odd/even split:** invalid. The two policies share one ready queue, so a bead claimed by arm A is unavailable to arm B; the arms are not independent, and the coupling biases toward null. Do not use.
- **Synthetic-load injection:** valid but answers a *different* question. It tests sort *correctness* (does a P0 behind older P1s get claimed first through the live path), not sort *value* on real traffic. This is exactly the canary's legitimate residual job (§6).
- **Cross-pool diff-in-diff:** confounded by pool workload heterogeneity (CV 63–122% differs per pool); with only 2 contended pools and a ~2% effect, the DiD standard error swamps the signal. Not worth it.

**Conclusion for Q3:** there is no live experimental design that resolves the *value* question at feasible N. The paired replay already resolves it. Any live test can only verify *mechanism/safety*.

## 4. What is the right objective?

We optimize F_w (priority-weighted mean flow, w = 2^(4−p)). A critical fact: on this trace the sort's gain is **entirely redistribution across priority classes, with zero change to total throughput or makespan.**

```
TOTAL   flow-hours:   oldest 9527h   hybrid 9528h   →  −1h  (−0.01%, i.e. net zero)
WEIGHTED flow-hours:  oldest 40642   hybrid 39325   →  +1317 (+3.24%)
```

Reordering claims cannot change how long the servers are busy (fixed service, fixed slots); it only changes *which* class waits. The +3.24% is real *if and only if* you value priority-weighting. If the objective were makespan or unweighted throughput, dispatch order is provably irrelevant here (−0.01%). F_w is defensible as the objective (P0s should wait less), and phase-0 confirms the redistribution is benign (P1 p95 tail 20.4h→18.8h, P2 tail +8.3% within the 10% guard). But nobody should expect the sort to make the factory *faster* — it will not.

The index's `f_unblock` feature added nothing (+0.1%) because unblocking value only matters when a claim decision gates dependents, and §2 shows decision points are rare and mostly single-band; the feature had almost no decision points to act on. It was not a weak feature so much as a non-discriminating workload for it.

## 5. The honest null — where the leverage actually is

Dispatch-policy optimization is **not** where the leverage is. The service-time tail owns the flow time:

| bead set | share of total real flow-hours |
|----------|-------------------------------|
| single longest bead | **12.1%** (1140h) |
| top 5 beads | 35.8% |
| top 1% (14 beads) | **53.3%** |
| top 5% (70 beads) | 73.1% |

The sort saves ~1317 weighted-hours (net-zero wall-clock) across all 1399 beads. **The single longest bead's real flow (1140h) exceeds that.** Eliminating one multi-day wedge returns more than reordering every claim for 11 weeks. The leverage is in the strands/wedges that cost HOURS-to-DAYS (the 530h and 109h beads are stuck workers, dependency cycles, and stale work_dirs, not scheduling losses), not the 2% sort.

This aligns with dr-94s's own findings: the first canary window was void because the EB pool was wedged (5/6 workers claiming nothing against 100 ready beads). The wedge is the story; the sort is a rounding error next to it.

## 6. Recommendation

**Primary: option (ii) — ship the sort on replay evidence; declare the canary's value mission unsatisfiable.**

1. **Roll the one-word `--sort oldest → hybrid`** swap fleet-wide (per dr-94s mechanism: replicate the proven `work_query` override to the remaining pools, bak each `agent.toml`, `gc reload --soft`, verify effective per pool). Do **not** build the 4-feature index (fails the phase-0 SWAY bar at +0.1% over pure priority). Risk is ~zero: net-zero wall-clock, benign tail redistribution, one word.

2. **Re-scope the live canary to SAFETY ONLY.** It cannot measure value (§1: needs ~285× annual output per arm). Its only sound job is falsifiable mechanism verification, exactly the re-armed dr-94s gate: (a) ≥20 pool-probe claims through `probe_pool_demand`, (b) spanning ≥2 priority bands, (c) claim order matches hybrid and demonstrably differs from `created_at` order, (d) zero strands. Gate the roll on that falsifiable condition, never on absence-of-noise. Record in dr-94s that the *value* question is closed by replay and must not be re-opened as a live A/B.

3. **Redirect (the real win): attack the tail.** File OR follow-up on wedge/strand elimination — the top 1% of beads own 53% of flow-hours. Instrument multi-day service outliers (stuck work_dirs, dependency cycles like the zeldascension `.8↔.9` deadlock, stale claims) and drive them down. That is where hours, not percents, live.

**What would change this recommendation:** if the fleet's traffic mix shifted so contended pools (EB, packs) became the bulk of volume, the discrimination ceiling (§2) would rise and the effect could grow past 2-3%. Worth a re-run of `discriminate.py` quarterly. But on today's traffic, the sort is a free, tiny, real improvement, and the canary cannot and should not be asked to prove more than its own safety.

---

### Adversarial-review notes (for the queued post-2026-07-19 Codex cross-review)

- **Model dependency:** every number rides on `bin/or-replay`'s claim-slot model (servers = observed max concurrency; slots = real claim timestamps). That model reproduces real FCFS claim times to 0.37h median error, but it is one model; a reviewer should attack whether the slot schedule is itself policy-dependent (it is not held so under FCFS, but a real fleet running hybrid might free slots at different times — the replay cannot capture second-order feedback of policy on service durations). The effect being net-zero on wall-clock makes this second-order concern low-stakes.
- **Corpus drift:** +3.24% (this run, 1399 beads) vs +2.0% (phase-0, 1286 beads). The delta is one day of contended traffic, not a methodology change; both are far below the noise floor either way.
- **Power formula:** standard two-sample normal approximation. Heavy-tailed flow violates normality, which makes the *true* required N even larger (fat tails need more samples, not fewer), so the "unsatisfiable" conclusion is conservative.
- **Decision-point count depends on the driving trajectory** (instrumented under FCFS/oldest, the incumbent). Re-running under `--policy hybrid` shifts individual decision points but not the order of magnitude of the ceiling; a reviewer can re-run `discriminate.py --policy hybrid` to confirm.
