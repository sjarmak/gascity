<!-- Generated 2026-05-29 by scix_experiments workflow gascity-research-synthesis (run wf_b53ea849-d5f, 17 agents). Inputs: gastownhall/{gascity,gastown,beads,dolt}, /home/ds/gas-city config, /home/ds/gascity-packs, gascity-dashboard, our Claude work-history transcripts, + SciX MCP literature (last 6 months). NOTE: line-refs into /home/ds/gas-city and /home/ds/gascity-packs were on-host VERIFIED; refs into gastownhall library .go and dashboard .ts are UNVERIFIED from this checkout. The 2026 arXiv bibcodes ARE real (verified against the SciX corpus this session). -->

Confirmed: `dolt-gc-maintenance.toml` order exists and is scheduled. This materially down-ranks Area 3's "Dolt bloat blocker" P0 — the remediation is already shipped. The event-stream view (no `status_changed` query path in bin/orders) remains a genuine gap, consistent across Areas 1/2/3.

I have enough verified ground truth to synthesize.

---

# Gas City Improvement Program — Decision-Grade Synthesis

Cross-area synthesis of 4 reviewed proposals (orchestration, research, eval-release, test-projects+model-change). On-host verification done against `/home/ds/gas-city` and `/home/ds/gascity-packs`. Every `gastownhall/gascity` `.go`, dashboard `.ts`, and all bibcode anchors remain **UNVERIFIED from this host** and are gated accordingly. Bias is net-negative: cut speculative tooling, ship verified single-file fixes.

## 1. Executive summary — 5 highest-value moves

1. **[orchestration] Make `gc-capacity --rebalance` read per-agent `agents/*/agent.toml` provider pins.** VERIFIED bug: `gc-capacity` parses only `city.toml [[agent]]` blocks (`bin/gc-capacity:114-125`); `zeldascension-worker/agent.toml:2` pins `provider="claude-3"`, invisible to the rebalancer. Single-file fix, prevents the worst lived failure (multi-worker rate-limit freeze). **Ship first.**
2. **[eval-release] Single-agent baseline honesty gate** — any new/changed formula ships `enabled=false` unless it beats its 1-agent baseline. Cheap, boolean, ZFC-clean, directly attacks unverified-fan-out cost. Highest impact-per-effort of all eval items.
3. **[research] MAST-style failure taxonomy fitted to our closed-bead corpus** — pure retrospective on existing Dolt data, no new infra; calibrates which failures to instrument before building anything. Gated on first reproducing the "21% drain-without-commit" number from Dolt (currently lore).
4. **[orchestration/research/eval — shared] Materialize the `(issue_id, from_status, to_status, actor, session, ts)` state-transition view** over the `events` table. Confirmed gap: no query path exists in `bin/` or `orders/`. Single prerequisite shared by 3 areas (Shepherd meta-agent, golden sets, `bd trace`).
5. **[orchestration/eval] Doctor check for `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS`.** VERIFIED: both `mol-pr-ship.formula.toml:173` and `mol-adopt-pr.formula.toml:215` hard-gate on the env var with no install-time warning. Cheapest mitigation for silent capability degradation.

## 2. Prioritized cross-area roadmap

| Item | Area | Pri | Effort | Evidence | Premortem risk |
|---|---|---|---|---|---|
| Rebalancer reads `agents/*/agent.toml` provider | orch | **P0** | S | `bin/gc-capacity:114-125`, `agents/zeldascension-worker/agent.toml:2` | Must honor explicit pins (`mayor`→claude-5, `city.toml:113`); add `pin=true` marker |
| Single-agent baseline honesty gate | eval | **P0** | M | proposal (a)/(c)-2; MAD `2025arXiv250312029C` (VERIFIED real — Chun et al. 2025; see §6) | None durable — was the one premortem survivor |
| State-transition view over `events` | orch/eval/research | **P0** | M | No `status_changed` path in bin/orders (verified absent); `events` schema UNVERIFIED (library repo) | Confirm `events` schema in gascity repo before effort estimate |
| MAST taxonomy on bead corpus | research | **P0** | M | proposal P0-1; MAST `2025arXiv250313657C` (VERIFIED real — Cemri et al. 2025; see §6) | Reproduce "21%" from Dolt first or labels reify lore |
| Agent-Teams doctor check | orch/eval | **P0** | S | `mol-pr-ship:173`, `mol-adopt-pr:215` (verified) | None |
| Reproduce drain-without-commit rate from Dolt | eval/research | **P0** | S | `bin/bead-janitor:13` drain-orphan scan (verified); 21% is lore | If unreproducible, all dependent pass-bars are unfalsifiable |
| Multi-agent vs single-agent A/B | research | P1 | M | proposal P0-2 | A/B confounded by bead-type, model |
| Pre-drain commit gate (with no-code exemption) | orch | P1 | M | `bin/bead-janitor:13`; `conditional-blocks` UNVERIFIED | Manufactures false orphans on docs-only/investigation beads without exemption |
| Wire per-agent cost/token/latency (set OTel URLs) | orch/eval | P1 | M | `gc-supervisor.ts:6310` UNVERIFIED | `cost_usd_estimate` may stay always-absent (upstream #1255) → dashboard shows zeros |
| Canary/triadic scorer (CI-pass + close-reason axes ONLY) | model-change/eval | P1 | M | proposal (B); HARBOR (UNRESOLVED — no corpus match, see §6) | Overfits to one model's prompt surface form if scored on structure |
| Shepherd meta-agent over trace | research | P1 | M | proposal P1-3 | Becomes another idle dispatcher — needs a route for its escalations |
| Overnight regression-guard project (CP2) | projects | P1 | M | `bin/pl-nightly-cycle`, `routed-bead-nudger.toml` (verified exist) | None major; guards verified silent-stall class |
| Reviewer-team parameterization (CP4 / P1b) | orch/projects | P1 | M | `mol-adopt-pr:245-248`, `mol-pr-ship:192-195` (verified) | Drops Codex slot (its verified value); keep Codex non-derivable |
| Pack version-increment + golden-replay CI | eval | P1 | M | packs `version=""` (proposal) | Golden set goes stale → Goodhart green while success rots |
| Extract duplicated 11-cat rubric to fragment | orch/eval | P2 | M | `mol-adopt-pr` + `mol-pr-ship` both inline (verified) | recall latency stalls review step |
| Tool-budget doctor check (warning only) | orch | P2 | S | proposal (b) | Keep as warning, cap is guideline not invariant |
| Failure-taxonomy dashboard panel | eval/obs | P2 | S | `gc-supervisor.ts:4332` UNVERIFIED | Bitter-Lesson-fragile (frozen MAST map) |
| Cost-aware tier router (TRACER) | research/model-change | P2 | L | proposal P2-6; depends on cost fields | Trains on NULL/zero cost → routes synthesis to Haiku |

## 3. The four areas — what survived review

### Area 1 — Orchestration

**Kept:**
- **P0(c) rebalancer reads provider overrides** — promoted to program-#1. Fully verified on-host (strongest single finding across all four areas).
- **P0(d) state-transition view** — kept, merged with the identical asks in Areas 2 and 3 (one shared deliverable). Verified absent in bin/orders.
- **P1(b) Agent-Teams doctor check** — kept (promoted; same item as Area 3's P0 and Area 4 CP5's surviving half).
- **P1(d) wire cost/token telemetry** — kept at P1 but gated: setting OTel URLs is cheap, but the payoff depends on upstream #1255 populating `cost_usd_estimate`. Don't sequence the cost-router behind it until fields are confirmed non-null.

**Down-ranked / conditional:**
- **P0(a) pre-drain commit gate → P1, with mandatory no-code-expected exemption.** Reviewer is right: without exempting docs-only/investigation/deferred beads it manufactures a new orphan class — "a janitor sweep with extra steps." `conditional-blocks` failure path is UNVERIFIED (library repo).

**Dropped:**
- **P1(b) reviewer parameterization as an Area-1 priority** — flagged most-likely-over-engineering; folded into Area 4 CP4 as a single P1 *project* that must keep the Codex slot non-derivable. Don't pursue it twice.
- **P1(b)'s "derive specialization from file extensions"** — drop the ext→reviewer map framing entirely (stringly-typed ZFC smell); if pursued, the polecat reasons from the diff, no dict ships.

### Area 2 — Research directions

**Kept:**
- **P0-1 MAST taxonomy** — highest-value research item; promoted to program P0. Gate it behind reproducing the 21% from Dolt (the proposal's own "replace lore with measurement" thesis applies to its own input).
- **P0-2 multi-agent-vs-single** — kept at P1; the empirical backing for the eval honesty gate.
- **P1-3 Shepherd meta-agent** — kept P1 but with the explicit premortem fix: its escalation wisps need a consuming route or it becomes the idle dispatcher it was built to detect.

**Dropped:**
- **P1-4 λ_A typed-config + TLA+ termination checking** — DROPPED. Both reviewer and Bitter-Lesson agree this is formal-methods ceremony for what a ~50-line install-time `gc doctor` validator solves (catch `patches.agent`→nonexistent-agent, empty `{{.RigRoot}}`). Replace with a cheap runtime config linter, folded into the doctor-check work.
- **P2-6 cost router** — kept at P2 but explicitly last; blocked on the same NULL cost fields as Area 1 P1(d).

### Area 3 — Eval + release

**Kept:**
- **Single-agent baseline honesty gate** — promoted to program P0 (the premortem's one durable winner; survives even with all line-refs unverified).
- **Doctor-check-per-env-capability** — kept P0 (= Area 1 P1b, verified on-host).
- **Relative-regression-over-absolute-SLA threshold philosophy** — kept; this is the best-grounded part (matches verified scix zpm4/ADR-010 anti-target lesson).
- **Pack version-increment + golden-replay CI** — kept P1; packs `version=""` is real.

**Down-ranked / dropped:**
- **"Dolt bloat remediation must land before eval write-path" (P0 blocker) → DOWN-RANKED to a verification check.** NEW on-host finding: `bin/dolt-gc-maintenance` exists AND `orders/dolt-gc-maintenance.toml` schedules it. The blocker is largely already shipped — confirm it runs and reclaims, don't re-scope it as net-new P0.
- **`IsFailureClose`/`FailureCloseKeywords` keyword-scan as the success metric** — flag retained: this is regex-for-meaning. Success/failure must come from the triadic verification result, not a keyword scan of NOTES.
- **`bd trace <bead-id>` full triadic reconstruction → DROPPED to P2/deferred.** Most-likely-over-engineering: depends on the not-yet-built event view, adds a Dolt read/write path, justified by one bibcode + one anecdote. Defer until the event view exists and the debugging wall is hit twice.

### Area 4 — Test-projects + model-change

**Kept:**
- **The (B) canary/triadic scorer — but scored ONLY on model-form-invariant axes (CI pass-rate, close-reason, cycle-time), NOT prompt structure.** This is the keystone for making a model swap observable; CP7's substrate (`interactions.jsonl` + close-reason) is real and cheap (no LLM judge). Build the scorer; defer the auto-rewrite workflow.
- **CP2 overnight regression guard** — kept P1; `pl-nightly-cycle` and `routed-bead-nudger.toml` verified to exist. Directly guards the worst lived failure (silent stalls).
- **CP4 reviewer-{lang}** — kept P1 (small, validated win); keep it a formula variable, not a lang→agent registry, and keep Codex non-derivable.

**Dropped / rewritten:**
- **CP5 must be REWRITTEN — its central asymmetry is FALSE (verified).** Both `mol-pr-ship.formula.toml:173` and `mol-adopt-pr.formula.toml:215` hard-gate on AGENT_TEAMS, and both document `subagent-fallback` (`:254`, `:302` respectively). The "only mol-pr-ship has fallback" claim is wrong. The surviving real bug — no doctor check at install — is already captured as the program-P0 doctor check. Drop CP5 as a distinct project.
- **CP6 pre-flight resource gate — DROPPED.** Single-incident encoding (HNSW 67GB/62GB) as a general `condition` order; `single_entry_registry`/YAGNI. May never fire twice.
- **(B)-step-2 descriptor table routing — DROPPED.** A task-class→tier descriptor table is exactly the hardcoded semantic heuristic ZFC forbids and the Bitter-Lesson warns against; it adds machinery to `csu_pick.sh`, which today does one clean mechanical thing (verified: pure utilization sort, `csu_pick.sh:16,43`). Routing learning, if pursued, is the TRACER P2 path (learned, label-free), not a hand-authored map.
- **CP1/CP2 `<5% drain-without-commit` pass bar** — derived from the unsourced 21%; do not set a numeric bar until the rate is reproduced from Dolt (same gate as Area 2 P0-1).

## 4. Open questions / what we still don't know

1. **Is the "21% drain-without-commit" rate real?** It anchors three areas' P0 priorities and is pure lore. → Query Dolt: closed-non-failure beads with empty NOTES and no commit by `ClosedBySession` in the worktree. **Do this before setting any drain-rate pass-bar.** (Method already designed in Area 2 P0-1 / Area 3.)
2. **Does `cost_usd_estimate` ever populate?** Both the cost dashboard (Area 1/3) and the TRACER router (Area 2/3/4) are dead if it stays always-absent (upstream #1255). → Check whether `GC_OTEL_*` URLs are set on host and whether `worker.operation` events carry non-null cost after setting them. Gate the router on a confirmed non-null sample.
3. **Library/dashboard schema confirmation.** Every `types.go`, `runtime.go`, `gc-supervisor.ts`, `Agents.tsx` line-ref is unverified from this checkout. The `events` table `status_changed` schema specifically gates the P0 state-transition view. → Confirm in `gastownhall/gascity` + dashboard repos before committing the view's effort estimate.
4. **Do the bibcodes resolve to real papers?** **RESOLVED 2026-05-31** (SciX corpus): the two load-bearing anchors are real and correctly attributed — MAST `2025arXiv250313657C` (Cemri et al. 2025) and MAD `2025arXiv250312029C` (Chun et al. 2025); see §6. HARBOR does **not** resolve to a corpus paper (non-load-bearing); TRACER is an internal codename, not a citation. As designed, no P0 depends on a bibcode — the literature is motivational only.
5. **Is `dolt-gc-maintenance` actually reclaiming?** The script + order exist; unknown whether it runs off-hours successfully and bounds the 680MB/17.7K-commit growth. → Check `~/.gc/dolt-gc-maintenance.log` and current `.beads/dolt` size before adding any eval write-path.
6. **Has the fixed 4-role reviewer team ever actually failed on a non-Go city?** CP4/P1b is justified by a bibcode, not an observed failure. → Look for a `review_mode` or reviewer-mismatch signal in closed Python-city beads before building parameterization.

## 5. Suggested beads to file (P0/P1)

- **P0** `gc-capacity --rebalance: read per-agent agents/*/agent.toml provider pins (honor pin=true)` — fixes verified rebalancer blindness behind the zelda freeze
- **P0** `Add single-agent baseline honesty gate: formulas ship enabled=false unless they beat 1-agent baseline`
- **P0** `Materialize (issue_id,from_status,to_status,actor,session,ts) view over events table` — shared prereq for meta-agent + golden sets
- **P0** `Add gc-doctor check for CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS + config linter (patches.agent→nonexistent, empty RigRoot)` — replaces dropped λ_A formal-methods proposal
- **P0** `Reproduce drain-without-commit rate from Dolt before setting any pass-bar` — validates the 21% lore that anchors 3 areas
- **P0** `Retrospective: MAST failure-taxonomy over closed-bead corpus (LLM-judge labels, arithmetic aggregation)` — gated on the above
- **P1** `Retrospective A/B: multi-agent molecule vs single-agent on resolution rate + cost by bead type`
- **P1** `Pre-drain verify-artifact gate WITH no-code-expected exemption (docs/investigation/deferred)`
- **P1** `Set GC_OTEL_METRICS_URL/LOGS_URL + confirm worker.operation cost fields populate (gate router on non-null sample)`
- **P1** `Canary/triadic scorer over interactions.jsonl + close-reason — CI-pass/close-reason/cycle-time axes ONLY, no prompt-structure diff`
- **P1** `Shepherd meta-agent over trace view — MUST define a consuming route for escalation wisps`
- **P1** `Overnight regression-guard project: 5-night pl-nightly-cycle run, zero 24h+ silent stalls, morning-ledger reconciles`
- **P1** `Parameterize reviewer team to reviewer-{lang} variable (NOT a lang→agent map); keep codex:codex-rescue slot non-derivable`
- **P1** `Pack CI: enforce version increment + golden-subset replay on formulas/*.toml and SKILL.md diffs`

## 6. Literature grounding — how the corpus informed this

Resolved against the SciX corpus on 2026-05-31 (closes Open Question #4 for the load-bearing anchors). The literature shaped the *method and framing* of the highest-value items; it is **not** load-bearing for any priority — every P0 rests on an on-host verified code finding, not a citation.

| Cited as | Resolves to | Status | What it informed |
|---|---|---|---|
| **MAST** `2025arXiv250313657C` | Cemri et al. (2025), *"Why Do Multi-Agent LLM Systems Fail?"* | ✅ Real, corpus-verified | The P0 "MAST taxonomy on bead corpus" retrospective (§1.3, Area 2 P0-1) and the P2 failure-taxonomy dashboard panel. MAST's method — 14 failure modes in 3 categories (specification issues, inter-agent misalignment, task verification), built via an LLM-as-Judge pipeline at Cohen's κ=0.88 — is the template for labeling our closed-bead corpus. Its *fixed*-taxonomy nature is exactly why the dashboard panel carries the "Bitter-Lesson-fragile (frozen MAST map)" caveat. |
| **MAD** `2025arXiv250312029C` | Chun et al. (2025), *"Is Multi-Agent Debate (MAD) the Silver Bullet? An Empirical Analysis of MAD in Code Summarization and Translation"* | ✅ Real, corpus-verified | The program-#2 single-agent baseline honesty gate (§1.2) and the P1 multi-agent-vs-single A/B (Area 2 P0-2). MAD's empirical finding — structured multi-agent debate yields minimal-to-inconsistent gains over single-agent baselines on SE tasks — is the evidentiary basis for "a formula ships `enabled=false` unless it beats its 1-agent baseline." |
| **HARBOR** | *(no corpus match)* | ❌ Unresolved | Cited only as soft backing for the canary/triadic scorer (Area 4 (B)). Does not resolve to a paper in the SciX corpus; treat as non-load-bearing. The scorer's real justification is the on-host-verified `interactions.jsonl` + close-reason substrate (CP7), not this citation. |
| **TRACER** | *(internal codename)* | — | The doc's own name for the proposed cost-aware tier router (P2), **not** a citation. If grounding is wanted, the nearest real corpus work is CascadeDebate `2026arXiv260412262C` (cost-aware LLM cascades) and sequential LLM routing `2026arXiv260412385Z`. |

**Net:** the two citations that actually shaped P0/P1 priorities (MAST, MAD) are real and correctly attributed; both are 2025 SE-domain empirical studies whose findings argue *against* unconditional multi-agent fan-out — which is precisely the synthesis's net-negative bias. The two unverified names back nothing load-bearing. This is consistent with the synthesis's own rule: bibcodes are motivational, never load-bearing; every priority rests on a verified code finding.

---

Relevant verified files: `/home/ds/gas-city/bin/gc-capacity` (114-125, 181-204), `/home/ds/gas-city/agents/zeldascension-worker/agent.toml:2`, `/home/ds/gas-city/city.toml:2,113,125`, `/home/ds/gas-city/bin/csu_pick.sh:16,43`, `/home/ds/gas-city/bin/bead-janitor:13`, `/home/ds/gas-city/bin/dolt-gc-maintenance` + `/home/ds/gas-city/orders/dolt-gc-maintenance.toml`, `/home/ds/gas-city/bin/route-decide-report:5,69-71,122-123`, `/home/ds/gascity-packs/pr-pipeline/formulas/mol-pr-ship.formula.toml:173,192-195,254`, `/home/ds/gascity-packs/pr-review/formulas/mol-adopt-pr.formula.toml:215,245-248,302`.
