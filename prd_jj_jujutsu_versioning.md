# PRD: Optional jj (Jujutsu) versioning interface for Gas City

> Status: RISK-ANNOTATED (post-premortem). Pipeline: diverge ✅ → converge ✅ → premortem ✅. Full registry: `premortem_jj_jujutsu_versioning.md`.
>
> **PREMORTEM VERDICT (5 lenses, 4 scored Critical×High):** The design as written has a make-or-break flaw — a colocated "read overlay" is NOT read-only on disk (auto-`git import` mutates the shared backend every command), and the kill-switch by construction cannot repair the resulting git-backend corruption. Four mandatory design changes before any build past Phase 0:
> 1. **Structural read-only overlay** — feed a NON-colocated/clone jj repo from periodic out-of-band import; never `jj git init --colocate` on a gc/polecat-writable dir. (Kills cross-cutting Theme A.)
> 2. **Upstream go/no-go FIRST + default to "ship Phase 0 and stop"** — a cheap upstream "no" ends the project before fork-divergence cost; gate Phase 1 entry on a *filed* incident.
> 3. **Phase-0 fixes must be jj-PROOF, not jj-aware** — R1 regression test non-deferrable + fail-closed on unknown push verb; R2 scans auto-snapshotted/untracked content (test plants an UNTRACKED secret).
> 4. **Pull R8 (GC cadence) into Phase 1; R3 reaper fails CLOSED; sandbox state structurally enforced** (zero push remote, zero real bead, watermarked, gate-red blocks downstream).
> Research provenance: 3 independent lenses — First-Principles Technical Design (A1), Prior Art + Visualization (A2), Failure Modes + Contrarian (A3). Converged via 3-advocate structured debate (build-now / don't-build / safety-first).

## Convergence Outcome (decisions)

1. **R1–R3 are unconditional, NOT jj-preconditions.** All three advocates (including "don't build it") accept them as standalone perimeter hardening: the ship gate, reaper, and gitleaks each encode a latent fail-open ("`git push` is the only push verb"; "clean porcelain = no work"; "secrets live in the git index"). Fix them on their own merits; they pay off even if jj is never built, and may be proposed upstream independently.
2. **jj does not fix gc's top named pains** (dolt drift, supervisor/tmux recovery, rate-limit failover, bead-dispatch claim races — all have dedicated machinery). The bead↔change-ID double-claim relief (R7) is salvage-ergonomics, not a root-cause fix; the double-claim root cause is the routed-bead-nudger nudging multiple warm slots, not git. jj's real, narrow value is **durable per-agent action history + change-ID-stable dashboard visualization**.
3. **The read overlay is not actually "free."** Decisive debate correction: "read-only" describes the *gc code path*, not jj's on-disk behavior. `jj git init --colocate` plus jj's per-command auto-`git import` **mutates the colocated backend** concurrently with polecat `git commit` and gc-core `git stash -u`. So even a read-overlay spike carries the colocated-backend-corruption and dolt-ref-interference risks. ⇒ the spike rig must share a filesystem with **neither a live polecat worktree nor the dolt store** until the concurrency soak passes.
4. **Phased, gated, reversible.** Phase 0 = R1–R3 unconditionally. Phase 1 = ONE sandboxed, non-shipping, non-dolt-adjacent research rig, read-overlay only (R4 git path byte-identical + jj colocated read; R5 DAG; R6 op-timeline); NO R7/R8. No production rig and no write/push path until all five go/no-go gates (below) are green.
5. **Preserved dissent:** the "don't build it" advocate remains unconvinced Phase 1 clears the opportunity-cost bar — same effort on dolt-GC, supervisor recovery, or the claim-race nudger returns more. Minimum-acceptable fallback if Phase 1 is declined: ship R1–R3 and stop.

## Problem Statement

Gas City orchestrates many parallel Claude Code agents, each typically in its own git worktree, with work routed as beads via `gc sling`. The current model hand-solves several git limitations: per-agent branches must be hash-namespaced (`gc-<agent>-<hash>`) to dodge global ref collisions; parallel polecats can double-claim and double-commit a bead (rebase-salvage today); and the only per-agent "what changed" record is a porcelain before/after snapshot in reviewquorum — there is no durable history of agent actions.

jj (Jujutsu) is a git-compatible VCS whose model — working-copy-as-commit, stable change-IDs, anonymous branches, first-class conflicts, and an operation log — maps unusually well onto multi-agent orchestration, and gives a dashboard a richer, evolution-aware view of concurrent agent work than git can. The question is whether gc should offer jj as an **optional** versioning interface for orchestrating work across the city and visualizing changes in the dashboard, **without forcing migration off git** and **without disarming the existing safety perimeter**.

The diverge research found a sharp split: the integration *seam* is small and additive for a read path, but jj violates two load-bearing assumptions baked into three independent safety systems ("`git push` is the only push verb"; "clean working tree = no work"). Adopting jj naively disarms the ship gate, the secret scanner, and the worktree reaper simultaneously.

## Goals & Non-Goals

### Goals
- Offer jj as an **opt-in, per-rig** mode (`vcs = "jj"`, default `"git"`) that leaves git-only rigs bit-for-bit unaffected.
- Run jj strictly in **colocated mode** so the real `.git`, the fork/origin/upstream remotes, the `gascity-ship` gate, and the GitHub PR flow remain the source of truth for all remote operations.
- Surface a **richer per-agent change view in the dashboard** — change graph (stable change-IDs) and operation timeline (`jj op log`) — as a read-only consumer.
- Make the existing safety systems **jj-aware before** any jj write/push path is enabled (fail-closed, not silently bypassed).

### Non-Goals
- NOT making jj the primary/required VCS on any rig that pushes to `gastownhall/gascity` (maintainer-scope + scope-creep risk).
- NOT routing the PR/ship pipeline through `jj git push` in the initial phase (colocated-backend corruption caveat + ship-gate bypass).
- NOT abandoning PRs for revset-based cross-workspace merging (the leading-edge jj-agent pattern), which is incompatible with gc's PR-gated model.
- NOT a gc-binary fork of git internals; integration is via swappable `pre_start` scripts + a sibling status package behind the existing interface.

## Requirements

Each requirement has a verifiable acceptance criterion.

### Phase 0 — Must-Have (unconditional perimeter hardening; ship even if jj is rejected)

- **R1. Ship-gate covers jj push verbs, fail-closed.** Rewrite `~/.claude/hooks/gascity-ship-gate.sh` trigger to also match `jj git push` / `jj git fetch` and to fail closed on unknown push verbs.
  - Acceptance: running `jj git push --remote fork` from an agent shell WITHOUT a `/gascity-ship` sentinel is blocked with the same non-zero exit + message as `git push fork`; a unit test asserts the regex matches both `git push` and `jj git push`.
- **R2. Secret scanning works under jj (no staging area).** Re-wire the pre-commit/pre-push gitleaks path to scan `jj diff`/changed files rather than git `--staged`.
  - Acceptance: committing a planted test secret via the jj path triggers a gitleaks failure (non-zero exit); demonstrably scans non-empty content (log shows files scanned > 0).
- **R3. Worktree reaper does not destroy live jj work.** Change `bin/stale-worktree-reaper` clean-detection from `git status --porcelain`==0 to a jj-aware emptiness check (e.g. working-copy commit is empty AND no unpushed changes).
  - Acceptance: reaper dry-run against a jj worktree with live uncommitted (auto-snapshotted) edits lists it as NOT reapable; against a genuinely empty jj worktree, lists it as reapable.

### Phase 1 — Should-Have (sandboxed read-overlay spike; non-shipping, non-dolt-adjacent rig only)

- **R4. Per-rig opt-in flag, two seams only.** Add `vcs` (default `git`) to rig config; pack `pre_start` selects `workspace-setup.sh` (jj) vs `worktree-setup.sh` (git); a sibling `internal/jj` package implements the same status surface (`StatusPorcelain`/`AheadBehind`/worktree-list shapes) consumed by `handler_rigs.go`, `checks_semantic.go`, and reviewquorum's snapshot caller.
  - Acceptance: a git-only rig produces a byte-identical worktree-setup + status path (no behavior change, verified by existing tests still green); a `vcs="jj"` rig provisions a colocated jj worktree (`jj git init --colocate`) on dispatch and reports correct branch/clean/ahead-behind to the rigs API.
- **R5. Dashboard renders the jj change graph.** New backend route runs `jj log -T '<pinned json template>' --no-graph -r '<hardcoded revset>'`, parsed like the existing `git.ts` VIEWS-enum/TSV pattern (no agent-supplied args); frontend renders a DAG (d3-dag layout + custom nodes keyed by change-ID).
  - Acceptance: dashboard shows ≥1 jj rig's concurrent agent changes as a DAG; a node retains identity (change-ID) across an agent `amend`/`rebase` (verified by `jj evolog`), where git would show a new hash.
- **R6. Dashboard renders the operation timeline.** Second view from `jj op log -T`, one swimlane per workspace/agent; diffs reuse `WorkflowDiffPanel` pointed at `jj diff -r <change>`.
  - Acceptance: op-timeline view shows time-ordered ops per agent for a jj rig; clicking an op shows the corresponding diff.

### Phase 2 — Nice-to-Have (write path; ONLY after all five go/no-go gates are green)

- **R7. Bead ↔ change-ID identity.** Route a slung bead to a jj change (stable change-ID) instead of a hash-namespaced branch, retiring the `gc-<agent>-<hash>` naming dance.
  - Acceptance: a slung bead's worktree has a change-ID recorded against the bead; double-claim of one bead yields two changes on a shared op-log timeline (recoverable) rather than a rebase-salvage.
- **R8. jj `util gc` maintenance cadence.** Wire `jj util gc` into a nightly order alongside the existing `dolt-gc-maintenance` pattern to bound colocated import/export latency on high-ref-count rigs.
  - Acceptance: nightly order runs `jj util gc` on jj rigs; auto-import latency on the gascity-ref-count rig stays under a measured threshold.

## Go/No-Go Gates (ALL green before any production rig OR write/push path)

1. **Concurrency soak.** ≥ real-polecat-count concurrent git+jj mutations under auto-snapshot on a shared FS, sustained run, **zero corruption**; if corruption occurs, `jj debug reindex` must fully recover. (Resolves Open Question: colocated-backend corruption.)
2. **Structured-output stability.** `jj log -T json()` / `jj op log -T` with our pinned template parse identically across ≥1 jj version bump. (Pin the jj version; own the templates.)
3. **Dolt isolation.** Colocated jj root scoped away from `.beads/dolt/.dolt`; measured per-command `git import` does not scan/slow/corrupt dolt refs.
4. **Dashboard legibility.** ~20 concurrent agent changes render as a legible DAG (d3-dag); a node's change-ID identity survives an `amend`/`rebase` (verified via `jj evolog`).
5. **Seam containment.** Existing git-only tests stay green; zero jj-awareness leaked into commit/stash/export internals.

## Kill-Switch (one command, fully reversible)

Flip rig `vcs` back to `"git"` → `pre_start` reverts to `worktree-setup.sh`; `jj workspace forget` + remove the colocated `.jj`. Git history is untouched because the colocated `.git` was the source of truth throughout. The dashboard jj route returns empty for non-jj rigs by construction. **No data migration** — nothing was ever stored only in jj.

## Design Considerations

**Read-overlay first, write-path gated.** The convergent design is: agents keep committing and pushing via **git**; gc runs jj colocated purely so the dashboard (and later, orchestration) can *read* the richer change/op model. This keeps all three safety systems on their git assumptions intact. The jj *write* path (jj-as-commit-tool, `jj git push`) is unlocked only after R1–R3 land.

**The "optional is a lie at scale" tension (A3 vs A1/A2).** A1 argues the seam is tiny (one swappable script + one status package). A3 argues every git-assuming code path (`internal/git`, `workdir`, reapers, `mol-*` formulas, the `bd` `issues.jsonl` export hook) must branch, doubling the test surface on the maintainer's repo. Resolution: confine dual-mode to the **two named seams** (R4) plus the **three safety fixes** (R1–R3); explicitly do NOT thread jj-awareness through commit/stash/export internals in phase 1 (those stay git-only because the write path stays git-only).

**Workspace model is an open architecture fork.** `jj workspace add` (shared backing store, best concurrency, deletes the branch-naming workaround) vs. one colocated repo per worktree (preserves today's isolation, weaker concurrency). Phase 1 read-overlay can start with per-worktree colocation (lowest deviation from today); shared-store `jj workspace` is a phase-2 decision tied to R7.

**Colocated backend is not fully lock-free.** jj's strongest concurrency guarantees weaken in colocated mode; corruption is possible under heavy concurrent git+jj mutation (recover via `jj debug reindex`), and cross-machine use is undertested. This is why the write path is gated and a concurrency soak test is required.

**Upstream acceptance.** A dual-VCS mode is exactly the kind of unrequested-feature scope creep gastownhall/gascity bounces. Land/prove it on a **sandboxed non-shipping research rig** first; only propose upstream with evidence.

## Open Questions

- **[RESOLVED] Dashboard repo location.** Confirmed present at `/home/ds/gascity-dashboard`; `backend/src/routes/git.ts` shells `git log` with a fixed pretty-format behind a hardcoded `VIEWS` enum, and `backend/src/workflows/diff.ts` runs status/diff per workflow path — both map ~1:1 to `jj log -T` / `jj diff`. (Verified by two independent agents.) Still TODO: read the exact `GitCommit` wire-shape/SSOT type before sizing the op-log lane.
- Does colocated jj actually corrupt under gc's real multi-polecat write load + auto-snapshot hooks on a shared FS? (Soak test before trusting the write path.)
- Is `jj log -T json()` output stable enough across the jj versions gc would pin/upgrade? (No backward-compat guarantee per jj docs — pin a version, own the templates.)
- Does a colocated jj root created above `.beads/dolt/.dolt` cause jj's per-command `git import` to scan/slow/corrupt dolt's own refs? (Keep jj roots scoped below/away from the dolt store.)
- Does the `rtk` `git status`→`rtk git status` rewrite (`~/.claude/settings.json`) need a jj-aware analogue, or does routing jj around it just forfeit token savings in VCS-heavy worktrees?
- Would upstream maintainers accept any jj mode at all?

## Research Provenance

- **A1 (First-Principles):** gc never runs `git worktree add` (swappable `pre_start` script); only 3 internal read-only git callers (`handler_rigs.go:117`, `checks_semantic.go:362`, `reviewquorum/mutations.go:20`); ship gate is a tool-layer PreToolUse hook independent of gc git internals; bead↔change-ID is a natural/better mapping; op-log is a new orchestration substrate. Confidence: High on seams, Medium on dashboard surfacing.
- **A2 (Prior Art + Viz):** multi-agent jj is an active 2025/26 pattern (Geirsson, Panozzo, Kurilyak); jj is lock-free EXCEPT colocated git backend; machine-readable via `jj log -T json()` but no stable contract; dashboard `git.ts` plumbing maps ~1:1 to `jj log -T`; use d3-dag for the fleet graph; jj↔GitHub PR tooling exists (jj-spr/stack/ryu). Confidence: Medium-High.
- **A3 (Contrarian):** ship gate regex blind to `jj git push` (bypass); gitleaks `--staged` no-ops under jj (no staging); reaper reaps live jj worktrees (auto-snapshot reads clean); `git stash -u` in gc core races jj snapshot; doesn't fix any named gc pain → YAGNI. Three safety systems fail in the same direction. Confidence: High on findings 1–3.

### Convergence summary
All three lenses agree the ONLY safe initial shape is **colocated, git-as-ship-substrate, read-overlay**. The split is on appetite: feasible-and-additive (A1/A2) vs. not-worth-it-and-dangerous (A3). The PRD resolves it by making A3's three findings hard must-have preconditions (R1–R3) and scoping phase 1 to the read-overlay value (R4–R6) on a sandboxed non-shipping rig.
