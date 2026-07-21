# Phase 1: priority-index dispatch in the work_query seam

**Status:** design spec, ready to implement · **Date:** 2026-07-11
**Parent:** `/home/ds/projects/aoa/docs/or-for-software-factories.md` (adoption table, phase 1) and its literature review sibling.
**Code ground truth:** verified against `/home/ds/gascity-main` at today's HEAD. The paper cites `config.go:2009-2100`; the seam has since moved to `internal/config/workquery.go` (`EffectiveWorkQuery` at `workquery.go:294`, the tier-3 routed-pool command builder at `workquery.go:42-44` and `:127-135`). Everything below uses current paths.
**Scope guard:** phase 1 changes claim *order* only. No Go, no bd changes, no new daemons. One scoring script, one shadow logger, one replay harness, per-agent `work_query` config lines.

> **CORRECTIONS (2026-07-13, post phase-0 replay).**
> (1) §0.4 "there is no deadline anywhere" is **wrong**: bd v1.1.0 natively
> supports `--due` / `--defer` / `--estimate` / `--acceptance` (verified
> against the live binary; the §0.4 investigation inspected gascity's mirror
> struct, not bd itself). The `gc.due` metadata convention (§1.3) is
> **superseded** by the native `due` field. See
> `docs/conventions/bead-declaration-rubric.md`.
> (2) The phase-0 replay (`docs/or-phase0-replay-results-2026-07-13.md`)
> **rejected the weighted index** per §3.4: +2.1% over FCFS (bar: 15%), +0.1%
> over plain priority sort (bar: 5%). Shipped instead: the one-word
> `--sort hybrid` swap, canaried on enterprisebench-worker. The lexicographic
> graph-aware successor design is tracked in bead `dr-fol`.

---

## 0. Verified ground truth (what the codebase actually offers)

Facts checked against `/home/ds/gascity-main` and the live `bd` (v1.1.0) in this city. These correct or sharpen the paper's premises; the design depends on each.

1. **The seam is real and total.** `Agent.WorkQuery` (`internal/config/config.go:3053`, toml key `work_query`) is returned as-is when set (`workquery.go:305-306`). A custom `work_query` **replaces all three tiers**, not just tier 3 — the doc comment calls it "the caller-owned full discovery contract" (`workquery.go:352-355`). The replacement script must therefore reproduce tiers 1–2 (assigned in-progress crash recovery, assigned ready) verbatim and change only tier-3 ordering. Section 2 does exactly that.
2. **The claim contract is "first eligible element of a JSON array."** `gc hook --claim` runs the work query, normalizes, defensively strips unready rows (future `defer_until`, open blocking deps, self-blocked) via `filterUnreadyHookCandidates` (`cmd/gc/cmd_hook.go:664-707`), then `claimFirstEligibleHookCandidate` claims the head and falls through on lost races (`cmd/gc/cmd_hook_claim.go:137-190`). So the entire dispatch policy is the *sort order of the array the script prints*. The default tier 3 asks bd for `--sort oldest --limit=20` (`workquery.go:127-135`) — FCFS, widened to 20 so a blocked head has fall-through candidates.
3. **Every phase-1 feature except deadline is already queryable.** `bd ready --json` rows carry `id, priority, created_at, dependent_count, dependency_count, dependencies, metadata, defer_until, labels, issue_type, status` (verified live). In particular:
   - `priority`: int 0–4, 0 = highest, default 2 (`bd create --help`).
   - `created_at`: RFC3339 — age is computable in jq.
   - `dependent_count`: count of beads that depend on this bead — the unblocking-value feature, precomputed by bd. **Caveat:** it counts all dependency types including `parent-child` (verified: `dr-i4v.3` shows `dependent_count=2` with a parent-child edge among them), and it is not documented as open-only. The phase-0 harness must measure how well it tracks true open-`blocks` dependents (computable offline from the events-log dep graphs, or per-bead via `bd dep list <id> --direction up --type blocks`, which is too slow for the claim path). Until then it is used as-is: a declared, cheap, monotone proxy.
   - `metadata`: serialized when present, so a `gc.due` key is readable in the same query.
4. **There is no deadline anywhere.** `internal/beads/beads.go` Bead struct has `Priority *int` and nothing temporal beyond timestamps; `internal/beadmeta/keys.go` declares no due/deadline key; `defer_until` is the opposite semantic (don't start before). The paper's "one schema gap worth closing early" is confirmed. Phase 1 closes it as a metadata convention (§1.3), not a schema change.
5. **The scale_check correspondence survives re-ranking.** `EffectivePoolDemandQuery` (the reconciler count-form, `workquery.go:476-492`) shares its *predicate* with tier 3 but is deliberately order-free ("the shared predicate stays order-free so the count-form does no wasted sorting", `workquery.go:128-129`). Re-ordering tier-3 output changes no set membership, so worker-claim and reconciler-spawn stay symmetric and the protocol-mismatch regression class (dispatch.md "scale_check ↔ work_query correspondence", PR #1516) is not reopened. **scale_check is not touched.**
6. **jq is already a contract dependency** of the default worker environment (the graph.v2 migration fallback tiers require it, `workquery.go:49-56`), so a jq scoring pipeline adds no new dependency.
7. **The replay substrate exists.** This city's `.gc/events.jsonl` plus rotated archives span 2026-05-19 → today (≈7.5 weeks, seq 890190 → 4.4M+). `bead.created` / `bead.updated` / `bead.closed` events carry **full bead payload snapshots** including `metadata.gc.routed_to`, `priority`, `dependencies`, timestamps (verified by sampling). Recent 50k-event window: 16,682 bead.updated, 7,039 bead.created, 6,500 bead.closed, 5 session.work_query_failed.

---

## 1. The priority index

### 1.1 Design stance, from the observatory record

The formula shape is Rubin/LSST's feature scheduler transplanted (Naghib `2019AJ....157..151N`), which the paper's survey identifies as one of the two policies a decade of operations vindicated:

- **Memoryless weighted sum over declared features of the current state.** No transition model, no forecast of future arrivals, no persisted scheduler state. Every claim re-scores the ready set from scratch — "re-decide cheaply" (ZTF re-solves, Rubin re-scores; scenario-tree planning lost everywhere it was tried).
- **Crash-robust by construction.** A worker that dies mid-claim leaves a state from which the next claim is exactly as valid — the property that let the feature scheduler absorb weather loss for free. `on_death` unclaim recovery composes unchanged.
- **Weights tuned offline against replayed traces, never online.** Rubin trained weights against the operations simulator; we tune against the phase-0 replay harness (§3). The policy in production is a frozen deterministic function.
- **ZFC-compliant.** Deterministic arithmetic over declared bead fields is "deterministic math" on ZFC's allowed list — the same species as `clamp(desired, min, max)`. The judgment stays in what agents *declare* (priority, due dates, dependencies); the scoring is transport.

### 1.2 Features and normalization

For candidate bead `b` at claim time `t`, four features, each normalized to [0, 1]:

| Feature | Definition | Normalization | Source field |
|---|---|---|---|
| `f_prio` | declared priority | `(4 − clamp(p, 0, 4)) / 4` → {0, .25, .5, .75, 1}; missing → bd default 2 → 0.5 | `priority` |
| `f_due` | deadline urgency | `gc.due` absent → **0**; else `clamp(1 − (t_due − t)/W_due, 0, 1)`; overdue → 1 | `metadata["gc.due"]` (§1.3) |
| `f_unblock` | unblocking value | `min(dependent_count, C_dep) / C_dep` | `dependent_count` |
| `f_age` | queue age | `min(t − created_at, A_max) / A_max` | `created_at` |

Constants (declared, versioned in the script header; phase-0 harness may revise before rollout):

- `W_due = 72h` — urgency window; a due date starts mattering three days out and saturates at the deadline. Deadline **absence** contributes exactly 0: undated work competes on the other three features and is never penalized below the FCFS status quo. This is the requested "deadline-absence" semantics — absence is the neutral point, not a boost or a penalty.
- `C_dep = 5` — unblocking credit saturates at 5 dependents; beyond that a bead is "critical path" and more credit buys nothing (and guards against `dependent_count` inflation from deep parent-child fans, per §0.3 caveat).
- `A_max = 14d` — anti-starvation horizon: any bead accrues full age credit within two weeks.

### 1.3 The `gc.due` metadata convention (phase-0 prerequisite, costs nothing)

Deadline-shaped work declares `gc.due=<RFC3339 UTC>` via `bd update <id> --set-metadata gc.due=2026-07-18T00:00:00Z` (or at sling time through the `gc-sling` wrapper / `.gc/sling-intercept.yaml` rules). Convention, not schema: the key is read only by the scoring script, never enforced, never blocks anything. Justification is the paper's Whittle-index result (Yu/Xu/Tong `2016arXiv161000399Y`): deadline scheduling collapses to a per-job priority scalar, and the single scalar our formula folds `f_due` into is the phase-1-sized version of that index. When phase 1 upstreams to gastownhall, `gc.due` gets declared in `beadmeta.KnownMetadataKeys` (the guard test at `internal/beadmeta/guard_test.go` only polices Go literals, so a config-side key needs nothing today).

### 1.4 Weights

```
S(b) = 1.00·f_prio + 0.10·f_due + 0.08·f_unblock + 0.06·f_age
```

**Initial vector rationale — the band-dominance invariant.** Adjacent priority bands are 0.25 apart in `f_prio`. Secondary weights sum to 0.24 < 0.25, so at the initial vector *declared priority is strictly dominant*: no accumulation of age, urgency, and unblocking value ever lifts a P2 above a P1. That is the conservative launch stance — priority is the one feature a human explicitly declared, and dispatch order within a band is where FCFS was actually leaving value (an aged, heavily-blocking, due-tomorrow P2 behind a fresh isolated P2). Within-band ordering: due-ness slightly over unblocking over age, reflecting that `gc.due` is an explicit declaration, `dependent_count` is structural, and age is passive.

**Tuning (offline, phase-0 harness).** The weight vector is the only tunable. Black-box search over replayed traces per Naghib: random oversampling plus successive halving (SWAY, `2016arXiv160807617C` — the survey's mandatory-baseline discipline applied to our own tuner), train on weeks 1–5, validate on held-out weeks 6–7. The tuner is free to break band dominance; acceptance is by replay metric (§3.4), never by the invariant. Two anchor points come free:

- `(w=1, 0, 0, 0)` + tie-breaks ≡ bd's `--sort priority` trivially-tuned baseline.
- `(w=0, 0, 0, ε)` + tie-breaks ≡ **current FCFS exactly** — the baseline is a point in the weight space, so tuned weights can never lose to FCFS on training traces except by overfitting, which the temporal holdout catches.

### 1.5 Tie-breaks

Total, deterministic order (identical ready sets rank identically on every worker, keeping claim races benign):

1. Higher `S(b)`.
2. Older `created_at` — FCFS is the within-tie policy, so the current behavior is the exact degenerate case.
3. Lexicographic `id` — total order for byte-identical timestamps.

---

## 2. The work_query implementation

### 2.1 Shape

One POSIX-sh script, `bin/work-query-ranked` (this workspace's `bin/` already hosts dispatch tooling), invoked per agent via config:

```toml
# agents/<rig-worker>/agent.toml  (or the [[agent]] block in city.toml)
# 2026-07-XX or-phase1: ranked tier-3 claim order (docs/design/fable-plans/2026-07-11/…)
work_query = "/home/ds/gas-city/bin/work-query-ranked <pool-target>"
```

The pool target is the same value the default passes positionally (`Agent.PoolName`, else qualified name — `workquery.go:158-164`); `{{.Rig}}`/`{{.AgentBase}}` template expansion is available in user `work_query` (`cmd/gc/cmd_hook.go:273-276`) if a shared per-rig line is preferred. Revert = delete one config line. `gc lint` after every flip.

### 2.2 The script

Because a custom `work_query` owns the full discovery contract (§0.1), the script reproduces the default's structure verbatim and swaps only the routed-ready tier's ordering. Tiers below mirror `workquery.go` generators for a standard (non-control-dispatcher) pool agent with bd-1.0.4 semantics (`includeEphemeralReady=false`, this city's current mode — confirm against `city.toml [beads]` before freezing; if the city is on bd-105 semantics, add `--include-ephemeral` and drop the legacy-ephemeral probes, exactly as the generator does).

```sh
#!/bin/sh
# work-query-ranked — phase-1 priority-index work query.
# Contract: prints a JSON array of claim candidates, best first; exit 0.
# gc hook claims the first eligible element and defensively re-filters
# blocked/deferred rows (cmd_hook.go filterUnreadyHookCandidates), so this
# script only owns ORDER. Mirrors internal/config/workquery.go defaults for
# tiers 1-2 and the legacy migration probes; only the routed-ready tier's
# sort is replaced (bd --sort oldest -> scored rank).
# Weights/constants version: or-p1-w1 (1.00, 0.10, 0.08, 0.06; 72h, 5, 14d).

target="$1"

# ---- Tier 1: assigned in_progress (crash recovery) — verbatim default ----
for id in "$GC_SESSION_ID" "$GC_SESSION_NAME" "$GC_ALIAS"; do
  [ -z "$id" ] && continue
  r=$(bd list --status in_progress --assignee="$id" --json --limit=1 2>/dev/null)
  [ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0
  r=$(bd query --json 'ephemeral=true AND status=in_progress' --limit=0 2>/dev/null | \
      jq --arg id "$id" '[.[] | select((.assignee // "") == $id)] | .[:1]' 2>/dev/null)
  [ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0
done

# ---- Tier 2: assigned ready (pre-assigned) — verbatim default ----
for id in "$GC_SESSION_ID" "$GC_SESSION_NAME" "$GC_ALIAS"; do
  [ -z "$id" ] && continue
  r=$(bd ready --assignee="$id" --json --limit=1 2>/dev/null)
  [ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0
  # legacy ephemeral assigned-ready probe: copy verbatim from the default
  # (workquery.go ephemeralAssignedReadyProbeScript) when freezing the script.
done

# ---- Origin gate — verbatim default ----
case "$GC_SESSION_ORIGIN" in ephemeral|"") ;; *) exit 0 ;; esac
[ -z "$target" ] && { printf "[]"; exit 0; }

# ---- Tier 3: routed pool demand, RANKED (the phase-1 change) ----
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
r=$(bd ready --metadata-field "gc.routed_to=$target" --unassigned \
     --exclude-type=epic --json --limit=0 2>/dev/null | \
  jq --arg now "$now" '
    ($now | fromdateiso8601) as $t
    | map(. + {gc_score: (
        (1.00 * ((4 - ((.priority // 2) | if . < 0 then 0 elif . > 4 then 4 else . end)) / 4))
      + (0.10 * (if ((.metadata["gc.due"] // "") | length) > 0
                 then ([1, ([0, (1 - (((.metadata["gc.due"] | fromdateiso8601) - $t) / 259200))] | max)] | min)
                 else 0 end))
      + (0.08 * ([1, ((.dependent_count // 0) / 5)] | min))
      + (0.06 * ([1, (($t - ((.created_at // $now) | fromdateiso8601)) / 1209600)] | min))
    )})
    | sort_by([-.gc_score, .created_at, .id])
    | .[:20]' 2>/dev/null)
[ -n "$r" ] && [ "$r" != "[]" ] && printf "%s" "$r" && exit 0

# ---- Tier 3 legacy migration + legacy-ephemeral fallbacks — verbatim default ----
# Copy from workquery.go bdReadyPoolDemandMigrationShell / legacyEphemeralPoolDemandShell
# output when freezing (retirement-window probes, ga-dhf44). Order inside these
# rarely-hit tiers stays oldest-first.

printf "[]"
```

Implementation notes, each load-bearing:

- **`--limit=0` then `.[:20]`**: the default fetches 20 oldest; ranking needs the whole routed-ready set to find the argmax, then emits the top 20 so the blocked-head fall-through behavior (`workquery.go:130-134`) is preserved. Queue depths here are 10²–10³; `bd ready --limit=0 --json` on the shared dolt server is milliseconds (shadow mode measures it, §4.1).
- **`gc_score` rides along in the output**: `filterUnreadyHookCandidates` tolerates extra keys, and the score in the claimed-bead JSON is free forensics (it lands in `bead.updated` event payloads only if stamped — it is not; it appears only in hook output and shadow logs, deliberately: no metadata writes on the read path).
- **Malformed `gc.due` fails the whole jq program** (`fromdateiso8601` on garbage): the `2>/dev/null` + empty-`r` guard drops to the legacy tiers rather than wedging the worker. Acceptable for phase 1; the shadow-mode error-rate metric (§4.1) watches for it, and a due-date-validating sling intercept is the cheap hardening if it fires.
- **Do not diverge the predicate.** The `bd ready` filter set (`gc.routed_to=$target --unassigned --exclude-type=epic`) is byte-identical to `bdReadyPoolDemandShell` (`workquery.go:42-44`). Only `--sort`/limit change. This is what keeps §0.5's scale_check symmetry intact.
- **Freeze, don't transcribe.** Before rollout, capture the live default for the target agent (run the agent's effective query via `gc` config inspection or lift the generated string from a `workquery.go`-driven unit-test run) and diff the script's tiers 1–2/legacy sections against it, so upstream drift in the default (new probes, bd-105 flip) is caught at flip time rather than discovered in production.

### 2.3 What `sling_query` does

Nothing. Slinging still stamps `gc.routed_to` (`workquery.go:431-433`); routing is untouched. Phase 1 changes which routed bead a worker claims first, not where beads go.

---

## 3. Evaluation harness — replay fixed traces, beat the dumb baseline first

The paper's own bar (its Evaluation section) is a prerequisite, not an appendix: Decima's fixed-trace discipline (`2018arXiv181001963M`) plus SWAY's mandatory-trivial-baseline discipline (`2016arXiv160807617C`). Nothing ships on argument.

### 3.1 Replay dataset

**Source:** this city's `.gc/events.jsonl` + `.gc/events.jsonl.archive-*.gz` — 2026-05-19 → present, ≈7.5 weeks, full bead snapshots per event (§0.7). Split: weeks 1–5 train (weight tuning), weeks 6–7 holdout, and per-week folds for the acceptance count.

**Extraction** (per pool target, from `bead.created`/`bead.updated`/`bead.closed` payloads):

| Trace element | Definition |
|---|---|
| arrival(b) | first event where payload has `metadata["gc.routed_to"]==pool`, `status=="open"`, empty assignee (readiness is *recomputed by the simulator* from the dep graph, not taken from the trace) |
| claim(b) | first `bead.updated` with `status=="in_progress"` and non-empty assignee |
| complete(b) | `bead.closed` ts |
| service(b) | complete(b) − claim(b), **trace-fixed**: the same duration is replayed under every policy (declared approximation: duration is policy-independent) |
| servers(pool) | replayed from observed concurrency: at each instant, the number of in-flight claims the real trace shows for that pool (upper-bounded by the pool's `max_active_sessions`) |
| features(b) | `priority`, `created_at` from payload; `dependent_count` recomputed at claim time as open-`blocks`/`parent-child` dependents from the evolving dep graph — this doubles as the §0.3 fidelity check of bd's live field |

**Exclusions:** session beads (`issue_type=="session"`, `gc:session` label), wisps/ephemeral, mail, convoys, order-fired patrol wisps — work beads routed to worker pools only. Molecules replay as their observed leaf beads (formula-internal ordering is out of scope, §5).

**Simulator:** discrete-event. When a server frees (or a bead arrives at an idle server), the policy under test scores the currently-ready routed set and claims the argmax; service consumes the trace-fixed duration; completions unblock dependents. Identical arrival trace, identical durations, identical server availability across all compared policies — the only degree of freedom is claim order. Also build the **LCO-style constructed fixtures** (known-optimum instances, oversubscribed and undersubscribed) as simulator unit tests: e.g. a queue where one P2 blocks five P1s must be claimed first by any sane index and is not by FCFS.

### 3.2 Policies compared

| ID | Policy | Cost to adopt |
|---|---|---|
| B0 | FCFS oldest-first — the current default, exactly | zero (status quo) |
| B1 | Trivially-tuned priority queue: `f_prio` only, FCFS tie-break (≡ `bd --sort priority` reordering) | one-word config change |
| B2 | The §1.4 index, initial weights | this spec |
| B2* | The §1.4 index, replay-tuned weights (train weeks only) | this spec + tuning run |

### 3.3 Metrics

- **Primary — priority-weighted mean flow time:** `F_w = Σ_b w(p_b)·(complete(b) − arrival(b)) / Σ_b w(p_b)`, with declared weights `w(p) = 2^(4−p)` (16, 8, 4, 2, 1 for P0…P4).
- **Secondary — unblocking-value throughput:** time-averaged cumulative open-blocks `dependent_count` of completed beads (area under the released-work curve, normalized by horizon).
- **Guard — per-class tails:** p95 flow time per priority class (starvation detector for P3/P4 under the index; the paper's silent-loss class).
- **Guard — completion parity:** every bead completed in the real trace completes within the replay horizon under the candidate policy (no starvation drops).

### 3.4 Acceptance threshold

Phase 1 flips a canary only if, on replayed traces:

1. **B2* beats B0 by ≥15% on `F_w`** in ≥4 of 5 train-fold weeks **and** on both holdout weeks.
2. **B2* beats B1**, and the margin is reported. If B2* − B1 < 5% relative on `F_w`, **ship B1 instead** — the one-word `--sort priority` swap — and record that the SWAY bar did its job. Beating FCFS with priority alone is not evidence for the four-feature index.
3. **No priority class's p95 flow time degrades >10%** vs B0, and completion parity holds.
4. Initial-weight B2 (untuned) is reported alongside B2* as the overfitting sentinel: a large B2*−B2 gap that vanishes on holdout means the tuner memorized the trace.

---

## 4. Rollout

Change control per `cityops-city-change-control` (bak-before-flip, comment-as-changelog); config flips in this workspace get Stephanie's per-change approval.

### 4.1 Stage A — shadow mode (log what WOULD have been picked; behavior unchanged)

`bin/work-query-shadow`: identical to §2.2 except tier 3 computes **both** heads — FCFS-ordered (default `--sort oldest --limit=20`) and ranked — appends one JSON line to `.gc/or-shadow/<pool>.jsonl` (`{ts, pool, session, fcfs_head, ranked_head, ranked_score, queue_len, bd_ms}`), and **prints the FCFS result unchanged**. Single-line `printf >>` appends (atomic under PIPE_BUF). Deploy on 2–3 pools spanning traffic regimes; run ≥7 days or ≥200 tier-3 decisions per pool.

Shadow mode is the *operational* gate (the replay harness is the *policy* gate): it measures the `--limit=0` fetch latency delta, jq error rate (target: zero empty-`r` fallthroughs from scoring), `session.work_query_failed` delta vs the ~5/50k-events baseline, and the FCFS-vs-ranked disagreement rate — a disagreement rate near zero says the queue rarely gets deep enough for policy to matter, which is itself a finding that caps expected value before any behavior changes.

### 4.2 Stage B — one-rig canary

Flip one mid-traffic research-rig worker pool (candidate: `codeprobe` or `mem` worker pool — enough queue depth to exercise ranking, not fleet-critical) to `bin/work-query-ranked`. `.bak` the agent config first; run `gc lint`; watch one week: claim-error events, idle-exit/wake patterns, dispatcher-stall patrol logs, P0/P1 claim latency from live events vs the same pool's prior week. Keep the shadow logger running on the *other* pools as the concurrent control. Revert = restore the one line.

### 4.3 Stage C — fleet

Apply per pool-agent config (mechanical, one line each; there is no global work_query inheritance — that's fine, the flip stays per-rig revertible). Shadow logs stay on for two weeks post-fleet. Weight retuning is **one-shot offline** in phase 1; a periodic retune order is a phase-2+ candidate, not built now.

---

## 5. What phase 1 does NOT touch

Negative space, explicit:

- **No `scale_check` / `EffectivePoolDemandQuery` changes** — the count-form is order-free and stays byte-identical (§0.5).
- **No bandit tier routing** — `gc.model`, `mol-dispatch` routing switches, and `internal/pricing` are untouched (phase 3).
- **No MILP, no epoch planner, no solver dependency** — no HiGHS/CP-SAT, no `internal/orders` exec order (phase 2).
- **No Go changes, no bd changes** — no new Bead fields; `gc.due` is a config-side metadata convention only.
- **No preemption, no reassignment** — running sessions are never disturbed; the index only orders *unclaimed* work (ALMA's in-flight-blocks-are-atomic discipline).
- **No deadline enforcement** — `gc.due` is read by one script; nothing blocks, expires, or escalates on it.
- **No formula/prompt changes, no `sling_query` changes** — routing and molecule internals are out of scope; within-molecule step order stays bd's.
- **No online learning, no persisted scheduler state** — weights are frozen constants in a versioned script.
- **No scalar "queue health" score in any gate** — replay metrics stay a vector (§3.3); acceptance is the §3.4 predicate, not a blended number.

---

## Executive summary

1. Gas City's pool dispatch is FCFS only because tier 3 of the fully-overridable `work_query` seam asks bd for `--sort oldest`; the claim contract is "first element of the printed JSON array," so dispatch policy is exactly the array order one config-declared script controls.
2. Phase 1 replaces that order with a Rubin-style memoryless priority index — `S = 1.00·priority + 0.10·due-urgency + 0.08·unblocking(dependent_count) + 0.06·age`, all normalized from fields `bd ready --json` already returns, plus one new `gc.due` metadata convention — with FCFS tie-breaks, so the status quo is the formula's degenerate point.
3. No Go changes anywhere: one jq-scoring shell script reproducing the default's tiers 1–2 verbatim, one per-agent `work_query` line, `scale_check` untouched (the order-free count-form keeps the worker/reconciler correspondence intact).
4. Nothing flips until the replay harness — 7.5 weeks of this city's `.gc/events.jsonl` bead lifecycles, fixed-trace discrete-event replay — shows the tuned index beating FCFS by ≥15% priority-weighted flow time AND beating the trivially-tuned priority sort by ≥5% (else ship the one-word `--sort priority` swap), with no priority class's p95 degrading >10%.
5. Rollout is shadow-log (behavior unchanged, would-have-picked logged) → one-rig canary → fleet, each stage one-line revertible; no bandits, no MILP, no solver, no preemption — those are phases 2–3 and are explicitly out.

**First artifact to build:** `bin/or-replay` — the trace-extraction + fixed-trace replay simulator over `.gc/events.jsonl(.archive-*)` that reports `F_w`, unblocking throughput, and per-class p95 for B0/B1/B2 (§3). It gates everything else, produces the baseline numbers this city has never measured, and needs zero approvals to run read-only today.
