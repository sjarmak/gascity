# Heavy-batch headless Claude calls (>100 `claude -p` one-shots)

Incident: 2026-06-12 — the UMBRELA judge batch (`scix_experiments-9na0`, scix-worker-3,
~01:52–03:12 EDT) ran 1,000+ `claude -p` one-shots through the bare `claude` binary,
i.e. the DEFAULT account home (`~/.claude`). It capped that account's 5-hour window at
~02:43, produced 2,156 doomed 429-retry sessions on top of 1,069 useful calls, and
starved the 06:00/06:45 website digest crons. Point fixes (judge dispatcher aborts on
RateLimitError, resumable via `--reuse-scores`; entry scripts default
`--claude-binary claude-auto`) landed on scix_experiments main at `409e2f5`.

Town-level rules (the durable layer):

1. **Route through `claude-auto`, never bare `claude`.** All batch-capable worker
   agents export `CLAUDE_BINARY=claude-auto` (set in `agents/<name>/agent.toml`
   `[env]` — applied 2026-06-12 to scix-worker{,-2,-3,-4}, codeprobe-worker,
   codescalebench-worker, enterprisebench-worker, migration-evals-worker,
   mem-worker). Batch scripts must honor `$CLAUDE_BINARY` (or take
   `--claude-binary`); `claude-auto` (`~/.local/bin`) routes each call to the
   least-loaded account and prints its selection on stderr.

2. **Pre-flight utilization check.** Before launching any batch >100 calls, check
   `~/.claude-usage/usage_cache.json` (refreshed by `csu`) and defer or reroute when
   the target account is >80% on the 5-hour window.

3. **Kill switch.** Batches run inside named `scix-batch` scopes, so a runaway storm
   halts instantly with:

       systemctl --user stop "scix-batch-*"

4. **Memory pairing:** heavy batches also need the oomd-safe cgroup — see
   `scix-batch` in CLAUDE.md and compass-capacity. The two wrappers compose.

Tracking: website-workspace bead `sjai-3lu`.
