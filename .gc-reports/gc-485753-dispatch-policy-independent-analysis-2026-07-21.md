# Replay supports priority ordering, not a live dispatch-policy experiment

Independent analysis for `gc-485753` used `.gc/events.jsonl` plus 26 archives
through `bin/or-replay --all`. The replay covered 1,902 work beads in 28 pools
from 2026-04-27 through 2026-07-21; 1,896 completed under every policy, while
the same six dependency-cycle beads remained unfinished. The replay fixture
suite passed 9/9.

## The simple policy captures almost all measured value

Overall priority-weighted flow time was 6.196 hours under FCFS/oldest, 6.021
hours under hybrid, 6.018 hours under plain priority, and 6.015 hours under the
four-feature index. Hybrid improves the replay by 2.83% over FCFS, plain
priority by 2.87%, and the index by 2.92%. The index gains only 0.10% over
hybrid and 0.05% over plain priority, which does not justify its extra scoring
surface.

The benefit is concentrated rather than fleet-wide: EnterpriseBench improves
7.1% across 302 beads, the polecat pool improves 2.6% across 942 beads, and
many smaller pools do not change. Among 1,896 claim decisions, 1,108 had more
than one ready bead, 370 had multiple priority bands ready, and only 159 chose
a different head under hybrid than under FCFS. That 8.4% decision rate is the
workload's structural ceiling on the sort's value. Due-date data was absent
from all 1,902 replayable beads, and the index's dependency-unblock feature did
not produce material flow improvement.

## Production traffic cannot discriminate this effect

Weekly FCFS flow time has a 17.68-hour standard deviation against an absolute
policy delta near 0.18 hours. An unpaired weekly live A/B would need roughly
56,809 weeks per arm to detect the observed 2.9% effect at 80% power; even an
optimistic paired switchback would need about 773 weekly blocks. The trace has
1,902 replayable beads across its entire span, so additional canary traffic
adds volume without enough discriminating power.

Shorter switchback blocks are invalid because real flow has a 22.18-hour p95
and 109.8-hour p99, which carries queue state across treatment boundaries.
Arrival counts remain autocorrelated at 24 hours. Simultaneous odd/even routing
inside one pool is also invalid because both arms consume the same queue; an
isolated-subqueue version would test a different system. Synthetic injection
can prove head-selection mechanics but cannot estimate production value.

## Recommendation

Use replay as the estimator for this decision. Roll out the trivial
priority-aware ordering only if its implementation risk is near zero; do not
build the four-feature index or a live A/B system for this effect. The measured
operational pain remains dominated by capacity, wedge, and strand failures:
P0 p95 changes from 12.53 to 12.36 hours, P1 p95 from 20.27 to 17.66 hours, P2
p95 worsens from 22.72 to 24.28 hours within guardrail, and unblocking
throughput is effectively flat at 60.72 versus 60.91.

The next decision is therefore implementation-shaped, not experimental:
accept a low-risk priority sort on replay evidence, or leave FCFS in place and
put the engineering attention into preventing multi-hour stalls.

Source: independent `codex-2` report delivered in mail `gc-527400` on
2026-07-21.
