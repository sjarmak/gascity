All evidence is in. Here is the RCA.

---

# RCA: Closed beads with unmerged code on the EnterpriseBench rig (score-forgery fix stranded off main)

**Date:** 2026-07-12 · **Scope:** `mol-focus-review` formula, EnterpriseBench rig, bead lifecycle · **Author:** investigation session for Stephanie

## Executive summary

The `mol-focus-review` formula has no landing step: its `finalize` step commits to the per-bead `work/*` branch, closes the bead, and drains (`formulas/mol-focus-review.formula.toml:326-360`). Nothing anywhere in the city merges that branch to main; the formula's own workspace-setup comment admits "the assumed 'controller reconciles to main' does not exist" (`mol-focus-review.formula.toml:126`). A second, now-patched defect (coada: finalize closed beads without checking the review verdict) means most pre-2026-07-12 closes can't even be trusted as genuine passes. The CRITICAL score-forgery fix (bead `EnterpriseBench-8krz5`, commits `c5559ed`/`4440d56`/`f6af89a`, including `tests/integrity/test_grading_asset_seal.py`) was closed **today at 22:43Z with `review_verdict=pass` under the fully patched formula** and is still stranded, proving the no-merge defect is the live one. Downstream audits inspect main, find the bugs still present, and re-file them (e.g. `kyo34`, filed 2026-07-12 while `chc2z`'s closed 10-commit fix to the same file sits unmerged).

## 1. The failure chain

The formula (`/home/ds/gas-city/formulas/mol-focus-review.formula.toml`, version 2, 7 steps: `load-context → workspace-setup → focus → run-tests → simplify → review → finalize`) fails in two distinct ways that compound.

### Defect A ("p5wrm") — no landing step. CONFIRMED, still live.

- `workspace-setup` (lines 107-139) creates a per-bead worktree on branch `work/{{issue}}` off `origin/{{base_branch}}`.
- All work, tests, simplification, and review happen on that branch.
- `finalize` (lines 288-362) does exactly four things: re-enter the worktree and `git add`/`git commit` stragglers (lines 326-341), record `diff_stat`/`impl_complete` metadata (lines 343-350), **`bd close {{issue}}`** (line 354), `gc runtime drain-ack` (line 359). Then the exit criteria: "Bead closed, worker drained."
- `grep -E 'git (merge|push|rebase)|--ff-only'` over the formula exits 1. There is no merge, no push, no PR creation, in any step.
- No controller-side reconciler exists either. The formula documents this itself, in the comment justifying real branches over detached HEADs: *"a worker's commits must survive worktree teardown even if no main-merge reconciliation ever runs (EnterpriseBench-1fb2 — detached HEAD leaves commits unreachable, and the assumed 'controller reconciles to main' does not exist)"* (lines 123-127).

So a **perfect** run of the formula — clean implementation, passing tests, passing review, verdict gate satisfied — strands its commits on `work/*` forever. `EnterpriseBench-8krz5` is the smoking gun: closed 2026-07-12T22:43:03Z with `review_verdict=pass` and `gc.outcome=pass` under the current (post-coada-fix) formula, 3 commits ahead of main (`e491459`), `test_grading_asset_seal.py` present on the branch and absent from main.

### Defect B ("coada") — close-on-failed-review. CONFIRMED as historical; patched at the prompt level today (2026-07-12).

The graph edge `finalize needs=["review"]` is satisfied by the review **step bead closing**, and the review step closes on its reject and hard-fail paths too. In the pre-2026-07-12 formula (`mol-focus-review.formula.toml.bak-2026-07-12-precoada`), `finalize` opened with the literal assumption *"The review passed. Close out the work."* and had no gate. A rejected review armed finalize exactly like a passing one; finalize then closed the target bead. The current formula documents two real hits: a near-miss on `jn73.1` (BLOCKED behind a Stephanie sign-off gate) and `jn73.2` (finalize told to close a BLOCKED bead holding a frozen fix list) — see the comment block at lines 296-301.

Today's fix adds `review_verdict` metadata written by the review step (lines 259-286) and a finalize gate that refuses unless `review_verdict=pass` and status isn't blocked (lines 303-317). Note this is **prompt-level** enforcement: the graph edge semantics are unchanged, and gc/bd still treat any closed bead as satisfying its dependency edges. A worker that skips the gate script closes the bead anyway.

### How they compound

- Defect B means "closed" ≠ "review passed" for everything closed before today.
- Defect A means "closed" ≠ "on main" for everything, including genuine passes.
- bd dependency resolution treats closed = satisfied, so downstream beads unblock and build on the assumption main has the fix. Both defects launder work-not-on-main into a green bead state that the entire city (dependency edges, `wake-mayor-on-slung-close`, epic reviews, velocity metrics) consumes as "done."

There was also a **Defect 0** worth recording: before 2026-07-10 (`bak-20260710-163323-eb-1fb2-73pw`), worktrees were created `--detach`, so torn-down worktrees left commits *unreachable* (EnterpriseBench-1fb2), not just unmerged. The 1fb2 fix created the `work/*` branch convention; every one of the 24 `work/*` branches therefore dates from the last ~2 days, which shows the strand rate under current throughput: roughly a dozen branches per day.

## 2. Why it went undetected

1. **Every prior fix patched one level up from the real gap.** 1fb2 fixed "commits become unreachable" by pinning branches, and in doing so *wrote down* that no reconciliation exists (line 126) — the missing landing step was observed, recorded in a comment, and used to justify branch persistence rather than treated as the defect. 73pw fixed "finalize verifies the wrong tree." coada fixed "finalize closes rejects." Nobody asked "and then who merges?"
2. **`bd close` is the terminal signal for all automation; nothing reads git.** Orders, dependency edges, epic sweeps, and audits all key off bead status. The bead even carries the evidence needed to catch this (`work_branch`, `diff_stat`, `impl_complete=true`) but no consumer runs a merge-base check against main.
3. **"Closed" and "landed" were conflated by every human-facing surface.** `bd show` says done; the audit trail says pass; the dashboard shows the bead gone from the queue. The only artifact that disagrees is `git branch --no-merged main`, which nothing surfaced.
4. **gc's own session tracking is blind to the branch.** On 8krz5 the bead carries `gc.work_branch="main"` alongside `work_branch="work/EnterpriseBench-8krz5"` and two different `work_dir` values. Even a hypothetical controller reconciler reading gc metadata would have looked at main and found nothing to do.
5. **The re-filed P0s looked like new findings, not duplicates.** Dedup/intake compares against *open* beads; the bead that fixed the bug is closed, so the re-file sails through.

## 3. Blast radius

**Corrected counts.** The 24 `work/*` branches carry 94 ahead-of-main commits (75 unique; some branches share history). The 187 figure is real but broader: it is the count of **unique unmerged commits across all 77 non-main local branches** — the `fix/*`, `audit/*`, `feat/*` families (over 30 branches, visible in `git branch`) are stranded by the same class of workflow, not just `work/*`.

**Closed-but-unmerged beads:** 12 closed beads have commits ahead on their `work/*` branch: `chc2z`(10), `qc7f`(4), `glka.2`(4), `jn73.13`(3), `8krz5`(3), `0nru`(3), `jrbkn`(2), `fi9mm`(2), `ewr8`(2), `e08u4`(2), `8csa`(2), `2hum`(2). Patch-equivalence (`git rev-list --cherry-pick`) shows `8csa` is fully equivalent to main (already landed via another path), leaving **11 with real unlanded content**; `2hum` may be semantically superseded, needing judgment. Critically, **only 4 of the 12 have `review_verdict=pass`** (`chc2z`, `8krz5`, `fi9mm`, `e08u4`); the other 8 closed pre-coada with no verdict, so their closes may themselves be coada false-closes.

**Affected work classes:** exactly the ones that matter most on a benchmark rig — verifier soundness and grading integrity (`8krz5` score-forgery seal, `chc2z` third-verifier-runner, `fi9mm` free-1.0 normalize_score, `e08u4` trace-copy integrity), cost/metrics correctness (`qc7f`, `ewr8`), and test-infrastructure honesty (`jrbkn` "make test runs ZERO tests").

**Why the same P0s keep reappearing:** audits and /simplify passes inspect main (or a fresh worktree off main), where the bugs still exist. Concrete loop: `kyo34` ("runner.py still fabricates scores from exit code") was filed 2026-07-12T12:14:37Z during /simplify of `glka.2` — while `chc2z`, closed with verdict=pass and 10 commits touching `lib/eb_verify/runner.py`, sits unmerged. Four separate branches (`chc2z`, `fi9mm`, `kyo34`, `ssikq`) now carry overlapping edits to that one file, which means the strand is also **breeding merge conflicts**: the longer the backlog sits, the harder the backfill gets.

One mitigating finding: `orders/prune-branches.toml` only prunes `gc/*` branches, and the worktree reapers don't touch `work/*` refs, so the stranded commits are not at deletion risk. Since the 1fb2 fix, this is data loss deferred, not data loss.

## 4. Remediation, ranked

**(b) Controller-side landing reconciler — recommended primary.** A new order (e.g. `work-landing-reaper`, alongside the existing reaper family in `orders/`): scan beads with `impl_complete=true` + `review_verdict=pass` + a `work_branch` whose head is not an ancestor of main; rebase/ff-merge onto main in a lock-guarded checkout; run the rig's test command; on green, record `landed_at` + `landed_sha` metadata; on conflict or red tests, set the bead blocked with an escalation note and mail the mayor.
*Fixes:* all future formula runs (any formula, not just mol-focus-review), deterministic code rather than prompt text, one central place for the conflict policy and test gate. *Doesn't fix:* the close-≠-landed semantics gap in the window between close and land (bounded by the order interval); needs judgment hooks for superseded branches. *Risk:* low-moderate — auto-merging to main needs a hard test gate and a conflict-means-stop rule; the pre-authorization for routine research-rig code pushes (CLAUDE.md, 2026-06-19) covers the rig-local landing, but results/data pushes stay per-action.

**(a) Merge step in the formula — good complement, insufficient alone.** Add a `land` step after `finalize`'s gate: `git fetch && git rebase origin/main && <test_command> && git checkout main && git merge --ff-only work/{{issue}}`.
*Fixes:* the common case at the source, with the worker present to resolve trivial rebases. *Doesn't fix:* the 30+ non-`work/*` stranded branches from other workflows; prompt-level only (the same enforcement weakness coada exploited — a worker can skip it); concurrent finalizes race on main. *Risk:* moderate — mid-formula conflict resolution by a draining worker is where quality goes to die; must be ff-only with a "conflict → set metadata, leave for the reconciler" escape hatch, which means you want (b) anyway.

**(c) Closed-but-unmerged guard — cheap, do it regardless, but it lands nothing.** The infrastructure already exists: `bin/close-gate-reaper` reopens recently-closed beads that fail evidence rules from `.gc/close-gates.yaml`, runs hourly, and nudges the mayor. Add a rule: a closed bead with `impl_complete=true` whose `work_branch` head is not an ancestor of main and not patch-equivalent (`git cherry`) fails the gate.
*Fixes:* converts silent failure into a loud, hourly, mayor-visible signal; enforces the invariant in §5. *Doesn't fix:* nothing merges; as the sole fix it just reopens beads into a queue that still has no landing path. *Risk:* minimal; needs the patch-equivalence check so legitimately-superseded closes (8csa-class) don't flap.

**Recommended combination:** (b) as the landing mechanism, (c) as the invariant enforcer, (a) later as a fast-path once (b) exists to catch its failures.

### One-time backfill of the 24 branches

Not a blind octopus merge — sequential, per-branch, with a re-review gate:

1. **`8krz5` first** (CRITICAL, 3 commits, clean `main...` delta): rebase onto `e491459`, run the integrity suite including `test_grading_asset_seal.py`, land. This closes the score-forgery hole on main immediately.
2. **The other 3 verdict=pass beads** (`chc2z`, `fi9mm`, `e08u4`): same procedure. `chc2z` (10 commits) goes before `fi9mm`/`kyo34`/`ssikq` work since all four touch `lib/eb_verify/runner.py`; landing chc2z first minimizes conflict surface and may moot parts of the open kyo34/ssikq beads.
3. **The 8 closed-without-verdict beads** (`qc7f`, `glka.2`, `jn73.13`, `0nru`, `jrbkn`, `ewr8`, `2hum`; `8csa` drops as patch-equivalent): treat the close as untrusted (coada-era). Re-review each diff against its bead's acceptance criteria before landing. `2hum` specifically: check whether main's current multi-command handling supersedes it before merging.
4. **Open/blocked/escalated beads** (`4p5k`, `ssikq`, `kyo34`, `glka.1`, `7rc1`, `jn73.*`): not backfill candidates; they re-enter the normal queue and rebase onto the moving main.
5. After each landing, re-run `git branch --no-merged main` and the cherry check so supersession decisions use the current main, then delete the landed `work/*` branch.
6. Repeat the sweep for the non-`work/*` branch families (`fix/*`, `audit/*`, …), which hold the balance of the 187 commits — likely including `fix/eb-hmcp-verifier-soundness`, directly relevant to the recurring verifier-soundness P0s.

## 5. Preventing the class

**Invariant: a bead may not close as done while its artifact is unreachable from main** (or is not patch-equivalent to something on main). Concretely:

- "Closed" must mean "landed," not "worker finished." If a delay between finish and land is unavoidable, that is a distinct, visible state (`impl_complete=true`, status still open or a dedicated `landing` label) that dependency edges do *not* treat as satisfied.
- Enforce it in code, not prompts: the close-gate-reaper rule from (c) is the checker; the landing reconciler from (b) is the fixer. This RCA's central lesson is that all three prior fixes (1fb2, 73pw, coada) were prompt-text patches to a lifecycle-semantics bug, and each held only until the next path around the prompt appeared.
- Any formula whose workspace-setup creates a branch must name, in the same file, the mechanism that retires that branch. A branch-creating step with no corresponding landing mechanism is a defect at formula-review time, findable by grep.
- Audits filing P0s against a repo should check `git branch --no-merged main` for an existing fix before filing; a match becomes "land bead X" instead of a new P0.
- Fix the `gc.work_branch` vs `work_branch` metadata contradiction (8krz5 shows `gc.work_branch="main"` while the work sits on `work/EnterpriseBench-8krz5`) so controller-side tooling can trust one field.

---

**Corrections to the briefed facts:** "coada" is confirmed but was patched at the prompt level today (the `bak-2026-07-12-precoada` backup preserves the unguarded finalize); "p5wrm" is confirmed and live, proven by 8krz5 closing under the patched formula tonight and still stranding. The count is 12 closed beads with commits ahead (11 after excluding patch-equivalent `8csa`), not 10; and 187 is the unique unmerged commit count across all 77 non-main branches, with the 24 `work/*` branches accounting for 75 unique commits of it.