# PRD: Dolt Storage Backend Evaluation for Beads

## Problem Statement

The beads (bd) issue tracker used by Gas City relies exclusively on Dolt as its storage backend. In practice, Dolt's "git for data" features (branching, remoting, diffing, merging) are entirely unused — the workspace runs 17,700+ auto-commits on a single branch with zero remotes. Meanwhile, Dolt imposes significant operational cost: two incompatible storage modes (server vs embedded) with no graceful migration, stale PID/lock files, hidden cross-rig coupling through a shared sql-server, data loss during mode switches, 680MB disk usage for 866KB of actual data, and a 757MB RSS daemon process.

Gas City already has an escape hatch via the `exec:` provider protocol and a working SQLite+JSONL backend (`beads_rust`/`br` in `contrib/beads-scripts/gc-beads-br`). Additionally, DoltHub is actively working on restoring embedded mode as the default ("Restoring Beads Classic", April 2026) and has announced DoltLite (a SQLite fork with Dolt's version control). The question is not "can we replace dolt" but "what is the right storage strategy given the trajectory of both gascity and dolt?"

## Goals & Non-Goals

### Goals

- Eliminate server/embedded mode confusion and the operational pain it causes (stale PIDs, lock files, cross-rig coupling, mode-switch data loss)
- Reduce storage overhead from 680MB to a reasonable level for <6k issues
- Provide a working local-first storage option that requires zero daemon processes
- Preserve the option to use Dolt's version-control features for rigs that actually need them (federation, multi-machine sync, audit trails)

### Non-Goals

- Forking or patching the upstream bd tool to remove Dolt
- Maintaining two SQL dialects inside bd's core codebase
- Optimizing for multi-machine or multi-user scenarios in this workspace (single user, 11 local rigs)
- Replacing Dolt for rigs that may genuinely benefit from it in the future (federation, K8s deployments)

## Requirements

### Must-Have

- Requirement: Run `dolt gc` on all active dolt databases to reclaim bloat from accumulated auto-commits
  - Acceptance: `du -sh .beads/dolt/` shows at least 50% reduction from current 680MB baseline for the gc database; bd commands continue to work after compaction

- Requirement: Switch bd auto-commit policy to batch mode to prevent future bloat accumulation
  - Acceptance: `bd config get dolt.auto-commit` returns `batch` for all active rigs; commit count growth rate drops by at least 10x over a 24h period

- Requirement: Document the shared dolt sql-server coupling and mode-switch procedure with export/import steps
  - Acceptance: CLAUDE.md contains a "mode switch procedure" section with explicit `bd export` / `bd import` steps; the procedure is tested by switching one rig from server to embedded and back without data loss

### Should-Have

- Requirement: Test the `exec:` provider with `gc-beads-br` (SQLite backend) on one non-critical rig
  - Acceptance: `city.toml` rig config uses `provider = "exec:contrib/beads-scripts/gc-beads-br"`; `gc doctor` passes for that rig; `bd list`, `bd create`, `bd close`, `bd show` all work correctly; disk usage is under 10MB

- Requirement: Determine whether "Restoring Beads Classic" embedded mode is available in bd 0.63.3
  - Acceptance: `bd init --help` shows an embedded mode option, OR a bd version is identified that restores it; if available, test on one rig and confirm zero daemon processes required

- Requirement: Migrate codeprobe and live_docs off the shared dolt server to eliminate the single-server coupling
  - Acceptance: Each rig has its own independent storage (embedded dolt or exec: provider); killing gas-city's dolt server does not break bd operations in codeprobe or live_docs; `gc doctor` passes for all three rigs independently

### Nice-to-Have

- Requirement: Evaluate the Dolt embedded Go driver (`github.com/dolthub/driver`) as a potential BdStore backend
  - Acceptance: A proof-of-concept Go program connects to a bead store via the embedded driver, runs `SELECT COUNT(*) FROM issues`, and returns the correct count without any external dolt process

- Requirement: Track DoltLite maturity for potential future convergence
  - Acceptance: A reference bead is created linking to the DoltLite repo with quarterly check-in reminders

## Design Considerations

**Tension: Simplicity now vs optionality later.** SQLite solves every current problem (zero daemon, tiny footprint, no mode confusion) but lacks Dolt's replication, branching, and audit trail. Gascity's roadmap includes multi-machine orchestration and federation where these features become load-bearing. The exec: provider gives optionality without commitment — different rigs can use different backends.

**Tension: Storage bloat vs audit value.** The 17k auto-commits consume 680MB but provide point-in-time recovery for debugging non-deterministic agent behavior. Switching to batch commit mode + periodic `dolt gc` is a compromise that keeps some history at much lower cost.

**Tension: Upstream alignment vs local needs.** DoltHub built Gas Town, is actively investing in the beads ecosystem, and is fixing the exact pain points driving this evaluation. Ripping out Dolt means walking away from a vendor building products around this tool's needs. But the user's local workspace doesn't need those features today.

## Open Questions

1. Why was SQLite support removed from bd? No rationale found in the codebase or documentation.
2. Does `convoy_sql.go` (Gas City's direct MySQL-protocol connection to dolt) require a real dolt server, or can it work through the exec: provider?
3. What is the actual concurrent write pressure during dispatch bursts? Determines whether SQLite's write lock is a real scaling ceiling.
4. Is "Restoring Beads Classic" embedded mode available in bd 0.63.3 (the current installed version)?
5. What is DoltLite's realistic timeline to production readiness?

## Research Provenance

Three independent research agents explored this question:

1. **Technical Comparison (Dolt vs SQLite vs JSONL)**: Quantified the storage and memory overhead (784x bloat, 757MB RSS), confirmed embedded mode removal in bd v0.56, recommended SQLite as primary backend.

2. **Upstream Codebase Archaeology**: Found the exec: provider escape hatch, confirmed dolt features are deeply integrated in bd but unused in practice, discovered Gas City's own upstream audit categorizes dolt changes as "N/A."

3. **Contrarian / Devil's Advocate**: Steel-manned dolt's case via DoltHub's active investment, gascity's multi-machine roadmap, SQLite's documented concurrent write ceiling, DoltLite convergence path, and the silent audit value of commit history.

**Key convergence**: All three agents agree the server/embedded mode split is the root problem, and that Gas City already has an escape hatch via the exec: provider.

**Key divergence**: Whether to replace dolt (Technical), work around it (Archaeology), or invest in fixing its deployment model (Contrarian). The right answer likely varies per rig based on scale and collaboration needs.
