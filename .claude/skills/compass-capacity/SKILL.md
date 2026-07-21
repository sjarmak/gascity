---
name: compass-capacity
description: Use when an agent is rate-limited, when planning heavy scix batches, or when diagnosing oomd kills that take down mayor or the supervisor as collateral. Indexes capacity tools and the memory-isolation pattern.
---

# Compass: capacity and memory isolation

When an agent is rate-limited or memory pressure is high:

- `city.toml` — `[[agent]] provider = "claude-N"` is the source of truth for account allocation; rebalancers rewrite these fields
- `~/.claude-usage/usage_cache.json` — what `csu` writes and `gc-capacity` reads; reflects API rate limits, NOT consumer/subscription limits
- `~/.local/bin/scix-batch` — wrapper for any scix script > few GB or > 1min; runs in transient systemd scope with `ManagedOOMPreference=avoid`
- `docs/conventions/capacity.md` — API vs consumer-limit gap, full rebalance command set, oomd interaction with the supervisor, `/tmp/supervisor-stop-caller.log` diagnosis

Hard rule: don't run multi-GB scix work outside `scix-batch` — oomd picks an unlucky cgroup inside `user@1000` under pressure and the supervisor is the usual casualty.

Positive targets: `gc-capacity --rebalance <agent>` moves an agent to the coolest account; add `--force` when in-session you observed a rate limit the dashboard reads as 0% (consumer vs API limit gap); `scix-batch <command>` is the default invocation for heavy scix scripts.
