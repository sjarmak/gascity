# Capacity and memory isolation

Failure modes covered: rate-limited agent silently stalling because `csu` only sees API limits (not consumer/subscription limits), heavy scix scripts pushing `user@1000` over the systemd-oomd pressure threshold so oomd kills mayor/supervisor as collateral.

## Setup

5 Claude accounts (`claude-1` through `claude-5`) mapped in each `agents/<name>/agent.toml` via `provider = "claude-N"`. Workspace default is `claude-1`. Plus Codex. Exception: the mayor runs on the `amp` provider since 2026-07-17 and sits outside the claude-account pool (see the rebalance note below).

## Tools

- **`csu`** — refreshes `~/.claude-usage/usage_cache.json` by probing each account's API rate-limit headers.
- **`gc-capacity`** — dashboard + rebalancer that cross-references usage data with agent→provider mappings in `agents/*/agent.toml`.
- **`mayor-failover`** — convenience alias for `gc-capacity --rebalance mayor`. Inoperative while the mayor runs on the `amp` provider (since 2026-07-17, set in `city.toml [[patches.agent]]` + `agents/mayor/agent.toml`): rebalancing moves agents among the claude-N accounts, so running it would move the mayor OFF amp — a provider change, not a rebalance.

## API vs consumer-subscription limits

`csu` probes **API rate limits** (via a Haiku request), but Claude Code agents hit **consumer/subscription rate limits** — these are separate systems. The dashboard may show 0% while an agent is actually rate-limited. When you see a rate limit in a session but `gc-capacity` doesn't detect it, use `--force`.

## Common operations

```bash
gc-capacity                                                # dashboard
gc-capacity --refresh                                      # refresh usage data first, then show
gc-capacity --rebalance mayor                              # move to coolest account (auto-detected) — inoperative while mayor = amp (2026-07-17+), see Tools above
gc-capacity --rebalance scix-worker-3                      # specific agent
gc-capacity --rebalance mayor --force                      # when tool can't detect the rate limit
gc-capacity --rebalance mayor --force --avoid 3            # exclude known-bad accounts
gc-capacity --rebalance auto --avoid 3 --avoid 2           # multi-exclude
gc-capacity --rebalance auto                               # capacity-balance all movable agents
gc-capacity --rebalance auto --dry-run                     # preview
gc-capacity --rebalance mayor --no-reset                   # city.toml only, don't reset session
gc-capacity --json                                         # programmatic
```

How rebalancing works:

- Single agent (`--rebalance <name>`): moves to the account with the lowest 5h utilization. Skips if already on coolest unless `--force` is set.
- Auto (`--rebalance auto`): computes a stable target weighted by the smaller of each account's remaining 5h and 7d quota. DOWN, HOT (>60% 5h), and 7d-hot (>=75%) accounts receive no movable agents. Existing placements are retained up to target so repeated runs do not churn sessions.
- `account-quota-warning` runs this convergence every 30 minutes and stays silent unless quota is hot. Provider changes take effect through the supervisor's staggered reload path.

## `scix-batch` — memory isolation

A single heavy scix batch script (e.g. `recompute_citation_communities.py`) can push `user@1000` above the `ManagedOOMMemoryPressureLimit=50%` threshold. When that happens concurrently with a supervisor startup burst (reconciler fan-out of `bd`/`jq` probes), systemd-oomd picks an unlucky cgroup inside `user@1000` to kill — and the supervisor exits cleanly as collateral. Looks identical to a mysterious external `systemctl stop` in the journal; confirm via `journalctl -k | grep oom-kill`.

Two mitigations are installed:

- `~/.config/systemd/user/gascity-supervisor.service.d/oom-preference.conf` — tells oomd to skip the supervisor (`ManagedOOMPreference=omit`) and reserves `MemoryLow=256M`. Redirects oomd's casualty, doesn't reduce pressure.
- `~/.local/bin/scix-batch` — wrapper that runs a command inside a transient systemd scope with a hard memory ceiling and `ManagedOOMPreference=avoid`. When pressure rises, oomd kills this scope first.

```bash
# Default caps (MemoryHigh=20G, MemoryMax=30G):
scix-batch python scripts/recompute_citation_communities.py --dsn ... --allow-prod

# Custom caps:
scix-batch --mem-high 10G --mem-max 15G python scripts/foo.py

# Watch the scope live:
systemctl --user status scix-batch-*.scope

# Kill a misbehaving batch:
systemctl --user kill scix-batch-<timestamp>-<pid>.scope
```

Use `scix-batch` for any scix script expected to allocate more than a few GB or run longer than a minute. Interactive work, quick queries, and the scix MCP server do not need it.

## Diagnosing the next supervisor kill

`/tmp/supervisor-stop-caller.log` is written by the `ExecStopPost` catcher in `stop-catcher.conf`. Captures the process tree at the moment of any future supervisor stop. Empty file = no stops since install. If populated and shows an oomd-killed scope under `user@1000`, move that workload into `scix-batch` or a similar capped scope.
