# gc-485440: dispatch-policy discrimination analysis

Data used: `/home/ds/gas-city/.gc/events.jsonl` plus all
`events.jsonl.archive-*` files, replayed on 2026-07-21. The trace spans
2026-04-27 to 2026-07-21 and contains 1,902 replayable work beads across
28 pools. `bin/or-replay test` passed all 9 fixtures before the analysis.

Delivery note: `gc mail send` hung twice, and direct file-store close/mail
fallback could not acquire `.gc/beads.json.lock`. The lock was held by
`/home/ds/.local/bin/gc supervisor run` (PID 3003182), while `gc supervisor
status --json` reported the supervisor running with an unreachable socket.
As of this write, `gc-485440` remains open in `.gc/beads.json`; this report is
the durable local artifact.

Recommendation: roll `oldest -> hybrid` on replay evidence alone, declare the
live canary unsatisfiable for value measurement, and stop work on the four-feature
index unless the objective changes. Hybrid is a cheap priority-band correction.
The index is not earning its complexity.

## The replay still separates FCFS from priority banding

Current all-trace replay:

| policy | n | F_w | vs FCFS | unblocking throughput | unfinished |
| --- | ---: | ---: | ---: | ---: | ---: |
| oldest | 1,896 | 6.196h | baseline | 60.716 | 6 |
| hybrid | 1,896 | 6.021h | +2.83% | 60.706 | 6 |
| priority | 1,896 | 6.018h | +2.87% | 60.778 | 6 |
| index | 1,896 | 6.015h | +2.92% | 60.914 | 6 |

The policy signal is real enough in paired replay, but it is small and it is
basically all priority banding. Index beats hybrid by about 0.1% and priority by
about 0.05 percentage points on `F_w`; that is not a product difference.

Tail checks do not block a hybrid rollout: P0 p95 moves 12.53h -> 12.36h, P1
p95 moves 20.27h -> 17.66h, P2 p95 moves 22.72h -> 24.28h (+6.9%), and P4 p95
moves 8.10h -> 8.53h (+5.3%). Those low-priority regressions stay under the
old 10% guard. Completion parity is unchanged; the 6 unfinished beads are the
known zeldascension cycle.

## A live A/B cannot measure a 2% effect here

Weekly fixed-policy variance swamps the effect. Across the 10 replay folds with
at least 10 beads, FCFS weekly `F_w` has mean 10.07h, standard deviation 17.68h,
and coefficient of variation 1.76. A 2% effect on the all-trace FCFS `F_w` is
0.124h, or 7.4 minutes.

Using alpha 0.05 and 80% power:

| design approximation | weeks needed | bead equivalent |
| --- | ---: | ---: |
| unpaired live A/B, 2% effect | 319,495 weeks per arm | 121M total beads |
| unpaired live A/B, observed 2.9% effect | 149,991 weeks per arm | 57M total beads |
| paired-replay lower bound, 2% effect | 4,347 weeks | 826k beads |
| paired-replay lower bound, observed 2.9% effect | 2,041 weeks | 388k beads |

The paired rows are lower bounds because they reuse the same trace
counterfactually. A real switchback does not get that perfect pairing; it sees
different arrivals in each block and carries queue state across block boundaries.

Arrival autocorrelation argues for blocks of at least 48h if a switchback is
run anyway. Hourly arrival-count autocorrelation first drops below 0.1 at 28h
overall; for the largest pools it is 12h for polecat, 11h for EnterpriseBench,
4h for mem, and 1h for gascity-packs. Queue carryover is longer than the arrival
memory: FCFS wait p95 is 16.7h overall, 10.8h on EnterpriseBench, and 34.4h on
gascity-packs. A 24h block is contaminated by the previous block; 48h is the
minimum defensible block, and there are nowhere near enough such blocks.

The current canary remains blind. On 2026-07-14 onward for
`enterprisebench-worker`, `bin/or-canary-check` sees 13 probe-pool claims, two
priority bands, 0 live band inversions, and 0 counterfactual FCFS band inversions.
That is not a pass. It means FCFS would have looked identical on that window.

## The workload does pose the question, but not often

Over the full replay, the baseline FCFS state has 1,896 claim slots. Of those,
1,108 slots have more than one ready bead, 370 have a mixed-priority ready set,
and only 159 slots would choose a different head under hybrid than under FCFS.
That is 8.4% of all claim slots, or 14.3% of queued slots.

The signal is concentrated:

| pool | claims | queued | hybrid head differs from FCFS |
| --- | ---: | ---: | ---: |
| polecat | 942 | 692 | 54 |
| enterprisebench-worker | 302 | 194 | 56 |
| mem-worker | 222 | 78 | 12 |
| gascity-packs-polecat | 208 | 87 | 27 |

So the workload is not fundamentally non-discriminating. The live canary was
just too small and too unlucky. But a live experiment must measure actual
head-disagreement opportunities, not raw claims, or it will green on volume
without asking the policy question.

## The extra index features have no room to work

There are no due dates in the trace. Dependency value is also too weak to justify
the index: 316 of 1,902 beads have dependents, 312 of those have exactly one
dependent, and only 4 have more than one. At claim time, 271 slots have any
positive open-dependent count in the ready set, and 240 have variation in that
count, but index differs from hybrid in only 115 queued slots (6.1% of all
claims).

The index cannot cross priority bands because the secondary weights sum below
one priority-band step. That was the intended conservative design, but it means
`f_unblock` only competes inside a priority band. With no due dates and almost
all dependency fanout equal to one, the feature is structurally weak on this
traffic.

## `F_w` is acceptable for rollout, not enough for operations

Priority-weighted flow time is a reasonable rollout metric for the cheap hybrid
change because it catches the main tradeoff: high-priority work moves earlier
while low-priority p95 is watched as a guard. It should not be the only ongoing
operations metric.

The dashboard should track P0/P1 p95 and p99, low-priority starvation, and
unblocking throughput separately. The current replay shows why: `F_w` moves
2.9%, while unblocking throughput is essentially flat at 60.716 -> 60.914. A
future policy that cuts P0 tail sharply but barely moves mean weighted flow
would be worth seeing; this metric alone could hide it.

## The bigger return is queue health, not ranking

Under FCFS, weighted flow decomposes into 3.657h wait and 2.539h service. The
index reduces weighted wait by 0.181h, or 4.95% of wait, which becomes 2.92% of
full flow. Sorting can only move the queued part.

The largest weighted-flow contributors point to stale waits and long services:
polecat is 47.3% of weighted flow and is service dominated (4.48h service,
1.92h wait); gascity-packs is 16.3% and wait dominated (9.12h wait);
EnterpriseBench is 14.4% and wait dominated (3.07h wait). Individual outliers
include `code-intel-digest-m8p` at 1,139h wait, `scix_experiments-0c73` at
923h wait, and multiple `gc-*` polecat beads with 170-779h service.

The next work should be stale/wedge detection, stranded-work repair, pool
capacity on the few wait-dominated pools, and service-time reduction for the
long polecat jobs. Dispatch sorting is a small correction, not the bottleneck.

## Experiment design

Do not run a live A/B to estimate value at the observed effect size. If a live
rollout gate is still required, use it only as a safety gate: 48h switchback
blocks on one pool, shadow-log both FCFS and hybrid heads, require enough
head-disagreement opportunities, and analyze by block with the decision rule
registered before the flip. Treat failure to discriminate as expected.

Use paired replay with week/block bootstrap CIs for value. It is the only design
that holds arrivals, service durations, and dependency graph fixed while varying
claim order. Synthetic load injection is useful for correctness of the sort
mechanism, not for value on real traffic. An odd/even within-pool split is not
valid for value because both arms share one queue and a bead claimed by one arm
changes the other arm's candidate set. Cross-pool comparison is only salvageable
as a coarse difference-in-differences safety check with pool fixed effects; it
is too confounded to measure a 2-3% value delta.

The ship decision should be: use hybrid because it is cheap, supported by replay,
and guarded by tails; do not ship the index; do not wait for a live canary to
prove a value delta it cannot measure.
