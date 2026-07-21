# Skill Curation Audit — 2026-07-07

Scope: three skill libraries authored recently. Recommendations only; nothing deleted or modified.

Bar applied per skill (all four must hold): real recurring trigger; no other home for the fact; earns its context cost; actually changes behavior. Plus, for standing-context libraries, a description-length check (~40 words) because every description word is paid in every session whether or not the skill fires.

---

## (a) gascity `.claude/skills/gc-*` — 14 skills (gastownhall/gascity codebase runbooks)

These are model-invoked reference skills for the upstream Go codebase. Descriptions are uniformly rich — they enumerate file paths, symptoms, trigger phrases, and explicit NOT-conditions, so trigger precision is high across the board. The cost is verbosity: 14 descriptions average ~95 words = **~1,330 words of standing context** in every gascity session.

| Skill                   | Body words | Desc words | Trigger verdict                                                                 | Duplication flags                                                                          |
| ----------------------- | ---------- | ---------- | ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| gc-build-verify         | 3097       | 117        | Sharp (file/symptom + NOT-guards to test-authoring/generated/release/debugging) | clean split vs gc-test-authoring                                                           |
| gc-change-workflow      | 2951       | 98         | Sharp (PR-planning trigger, NOT-guards)                                         | none                                                                                       |
| gc-config-system        | 2262       | 85         | Sharp (config paths + "didn't take effect" symptoms)                            | none                                                                                       |
| gc-debugging            | 2447       | 106        | Sharp (live-city symptoms, NOT-guards to build/dolt/reconciler)                 | boundary vs gc-reconciler-lifecycle is clean                                               |
| gc-doctrine             | 3143       | 130        | Sharp but longest desc; enumerates 6 pre-write triggers                         | overlaps AGENTS.md doctrine as deep-dive (acceptable split)                                |
| gc-dolt-ops             | 3498       | 100        | Sharp (paths + trigger-phrase list)                                             | naming near-collision w/ cityops-dolt-beads-reference (different subject: code vs install) |
| gc-events-payloads      | 2009       | 70         | Sharp (test names + paths)                                                      | cross-references gc-generated-artifacts; clean split                                       |
| gc-generated-artifacts  | 2205       | 90         | Sharp (CI job + test names)                                                     | cross-references gc-events-payloads; clean split                                           |
| gc-meow-work-model      | 2943       | 110        | Sharp (nouns + paths + NOT-guards)                                              | none                                                                                       |
| gc-orientation          | 2163       | 112        | Sharp (first-session trigger)                                                   | overlaps AGENTS.md "nine concepts"/"zero roles" as expansion (acceptable split)            |
| gc-reconciler-lifecycle | 3525       | 83         | Sharp (paths + trigger phrases)                                                 | none                                                                                       |
| gc-release-ci-ops       | 2712       | 97         | Sharp (release/CI verbs)                                                        | none                                                                                       |
| gc-runtime-providers    | 3046       | 64         | Sharp, tightest desc in set                                                     | none                                                                                       |
| gc-test-authoring       | 2285       | 84         | Sharp (file-type triggers + NOT-guards)                                         | clean split vs gc-build-verify                                                             |

**Verdict:** all 14 clear the four-test bar as codebase reference runbooks. AGENTS.md is a summary index, not a deep runbook, so gc-orientation and gc-doctrine expanding its "nine concepts"/doctrine sections is legitimate progressive disclosure, not a one-home violation — each carries an explicit "When NOT to use" pointing back. The only structural note is aggregate description verbosity; if gascity-session context ever tightens, the same shortening pattern proposed for cityops (below) applies here and would recover ~400-500 words without losing trigger precision.

---

## (b) gas-city `.claude/skills/cityops-*` — 11 skills (THIS ds-research install ops)

Standing context in every city session. Trigger quality is genuinely good — each description is specific, symptom-driven, and carries NOT-guards that point at the pre-existing `compass-*` library as the other home (deliberate non-overlap, good hygiene). **The problem is length: every one of the 11 exceeds the ~40-word guideline, summing to ~1,098 words of permanent standing context.**

| Skill                                  | Body words | Desc words | Trigger verdict                                                 | Duplication flags                                                            |
| -------------------------------------- | ---------- | ---------- | --------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| cityops-capacity-and-scaling           | 2075       | 95         | Good, NOT-guards vs compass-capacity/tmux/dolt                  | none                                                                         |
| cityops-city-change-control            | 2500       | 83         | Good                                                            | orders/ editing also in cityops-orders-and-patrols (change-vs-operate split) |
| cityops-debugging-playbook             | 2805       | 100        | Good but symptom list overlaps failure-archaeology              | **merge/sharpen vs cityops-failure-archaeology**                             |
| cityops-dispatch-and-formulas          | 2385       | 94         | Good, NOT-guards                                                | none                                                                         |
| cityops-dolt-beads-reference           | 1990       | 72         | Good (tightest)                                                 | dolt symptoms recur in debugging-playbook + failure-archaeology              |
| cityops-failure-archaeology            | 3381       | 132        | Good but longest desc; symptom list overlaps debugging-playbook | **merge/sharpen vs cityops-debugging-playbook**                              |
| cityops-guest-session-discipline       | 1763       | 117        | Good, narrow behavioral trigger ("you're a guest session")      | none                                                                         |
| cityops-mail-and-coordination          | 1976       | 92         | Good, trigger-phrase list                                       | none                                                                         |
| cityops-orders-and-patrols             | 2624       | 96         | Good, NOT-guards                                                | orders/ editing also in city-change-control                                  |
| cityops-session-and-account-management | 2277       | 94         | Good                                                            | none                                                                         |
| cityops-topology-contract              | 2130       | 123        | Good but 2nd-longest desc                                       | city.toml also in city-change-control (what-is vs how-to-change; clean)      |

**Verdict:** all 11 clear the four-test bar on substance (real install-specific facts with no other home — `compass-*` is carved out explicitly). None clears the description-length check.

### Standing-context tightening — all 11, ranked by words saved

The disambiguating NOT-clauses earn their keep (they sharpen firing), so the compression target is the descriptive middle, not the guards. Rewrites below hold the trigger + a compressed guard and land at ~40-45 words. Estimated recovery: **~1,098 → ~470 words (~57%, ~628 words).**

1. **cityops-failure-archaeology** (132→~45): "Incident history of this ds-research install (`/home/ds/gas-city`): detection signatures, diagnosis walkthroughs, and where forensic evidence lives on this host. Load when a symptom looks recurrent (stopped orders, OOM/pegged supervisor, stale/multiple dolt, runaway sidecars, burned quota), before killing a leaked-looking process, or when writing an RCA. Not step-by-step recovery (CLAUDE.md + tmux-supervisor doc) or dolt mechanics (compass-dolt)."
2. **cityops-topology-contract** (123→~42): "As-built topology of this ds-research install: which rigs/prefixes exist, the 5-account fungible claude model, CSU_PICK_EXCLUDE, the mayor provider-pin layering, the three suspension layers, and live orders.overrides. Load before editing or trusting a comment in city.toml, or when declared config and observed behavior disagree. Not dolt (compass-dolt) or dispatch (compass-bead-dispatch)."
3. **cityops-guest-session-discipline** (117→~40): "You are a human-launched guest session (not a gc-managed pool worker) with cwd under `/home/ds/gas-city`, `/home/ds/gascity`, or a rig while automated agents claim beads live. Load to decide whether you may touch beads/mail/config/dolt, tell the canonical dolt server from leaked ones, and know which of the three gascity trees you are in."
4. **cityops-debugging-playbook** (100→~42): "Something in this ds-research install is broken NOW and you must recover it: orders not firing, supervisor dead/looping, OOM, beads queuing behind idle workers, wedged dispatcher, dolt 127.0.0.1:0 errors, gc hangs. Gives the symptom-triage table, recovery ladder, and named fixes with no other doc home. For subsystem file indexes use compass-*; for RCA method use mechanic."
5. **cityops-orders-and-patrols** (96→~42): "Operate the scheduled-order fleet (`orders/`): reading/changing orders, misfires (never/late/false-alarm/timeout-killed), cooldown-vs-cron-vs-event choice, pause/re-enable, retiring a reaper after an upstream fix, and reconciling order counts across sources. Not writing a scanner (compass-scanners) or supervisor recovery (compass-tmux-supervisor)."
6. **cityops-capacity-and-scaling** (95→~42): "Operate capacity in this ds-research install: suspend/resume rigs, grow/shrink worker pools (+2-workers flow), the reconciler wake budget and wake/spawn storms, disk/memory pressure guards. Load when work queues behind a saturated pool, a pool must scale, the supervisor is CPU-pegged, or deciding if the city can absorb more load. Not rate-limits (compass-capacity) or wedged-supervisor recovery (compass-tmux-supervisor)."
7. **cityops-session-and-account-management** (94→~42): "Operate the five claude OAuth accounts and the gc session population: credentials-error launches, expired/rotting tokens, which account an agent runs under (and moving it), surprising claude-auto picks, zombie triage, dormant/wedged pool sessions. Covers ~/.claude-homes isolation, claude-account, csu_pick, token keepalive, /ds-cred. Not rate-limit rebalancing (compass-capacity) or supervisor recovery (compass-tmux-supervisor)."
8. **cityops-dispatch-and-formulas** (94→~42): "Route work through this ds-research install: sling beads with gc-sling, choose/debug mol-* formulas, the PR-molecule gate/carve-out and kill-switch, the routing.yaml reality check, tracing a stuck dispatch. Load when a slung bead sits unclaimed, a molecule stalls, or editing a formula. Not raw sling flags (compass-bead-dispatch) or order scheduling (cityops-orders-and-patrols)."
9. **cityops-mail-and-coordination** (92→~42): "Agent-to-agent and agent-to-human coordination in this install: reaching Stephanie, surfacing a decision/blocker, verifying a Slack post landed, debugging silent channels or orphaned bindings, the DECISION:/BLOCKED-ON-HUMAN: subject protocol, dead-letter redirect, and STATUS_UPDATE/DEEP_AUDIT cadence."
10. **cityops-city-change-control** (83→~42): "Change-control for this install: load BEFORE editing city.toml, orders/_.toml, agents/_/agent.toml, or a supervisor systemd drop-in; before pausing/adding an order; before promoting a janitor dry-run to --apply; or when deciding if a change needs Stephanie's approval. Covers bak-before-flip, comment-as-changelog, overrides vs .disabled. Not topology (cityops-topology-contract)."
11. **cityops-dolt-beads-reference** (72→~42): "Operate this install's bead storage: read/write beads with bd, resolve the real dolt sql-server, recover from 'unreachable at 127.0.0.1:0', triage leaked/ghost dolt processes, the file-vs-dolt dual backend and gc bd block, and the store's GC/prune/backup lifecycle. Not endpoint-config drift or recovery sequences (compass-dolt)."

### Merge/sharpen flag (cityops)

**cityops-debugging-playbook × cityops-failure-archaeology** overlap materially: both descriptions list stopped orders, OOM/pegged/restarting supervisor, and multiple dolt sql-server processes as triggers. The intended split is recover-now vs has-this-happened-before/forensics, which is a defensible reason-to-change boundary — but a reader hitting an OOM won't know which to load. Recommendation: keep both (they clear the bar individually) but make the boundary mutually exclusive in the first sentence of each ("recover it NOW" vs "recognize a recurrence / write the RCA") and strip the shared symptom enumeration from failure-archaeology, which is the longer of the two. This is a sharpen, not a merge.

---

## (c) process-skills drafts — `docs/design/fable-distillation/process-skills/` (5 dirs)

These are staged drafts under a design folder (per `orchestration-tick`: "Status: draft pending dr-i4v.5 consumer eval"), so they carry **zero standing-context cost today** — they are not installed under any `.claude/skills/`. Judged on readiness to promote.

| Dir                        | Skill name                 | Body words | Desc words | Verdict                                                                                                                                                |
| -------------------------- | -------------------------- | ---------- | ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| gascity-triage             | gascity-triage             | 3254       | 42         | Well-formed; **revises a shipped skill of the same name**                                                                                              |
| implementation-planning    | gascity-pr-start           | 3706       | 47         | Well-formed; **revises shipped gascity-pr-start**; dir name ≠ skill name (confusing); has planner-agent.md sibling                                     |
| gascity-review-incoming-pr | gascity-review-incoming-pr | 2956       | 55         | Well-formed; **revises a shipped skill of the same name**                                                                                              |
| orchestration-tick         | (none)                     | 2032       | —          | **No YAML frontmatter** — HTML-comment header instead. Will not fire as a model-invoked skill; it is a prompt fragment referenced from mayor/prompt.md |
| mol-decompose              | (none)                     | —          | —          | **No SKILL.md** — only `mol-decompose.formula.toml`. This is a formula, not a skill                                                                    |

Findings:

- **Three are revisions of already-shipped skills**, not net-new. gascity-triage, gascity-pr-start, and gascity-review-incoming-pr all exist in the live skill set. The drafts differ (e.g. pr-start draft says "premise-checks the issue against the checkout, maps blast radius by boundary type" vs shipped "reads the issue, maps blast radius via agent"), so they are intended supersessions. Recommendation: promote-and-replace or discard per dr-i4v eval — do **not** install alongside the shipped versions, which would create two homes for one skill. Track which wins.
- **`implementation-planning/` directory name should be renamed to `gascity-pr-start/`** to match its skill name before promotion; the mismatch will confuse the installer and any router index.
- **`orchestration-tick` and `mol-decompose` are not skills.** orchestration-tick is a prompt fragment (correctly referenced, not inlined, from mayor/prompt.md — that is the right pattern for it) and mol-decompose is a formula TOML. Neither belongs in a "process-skills" folder framed as skills. Recommendation: relocate both out of `process-skills/` into a `prompt-fragments/` and `formulas/` sibling respectively, so the folder's contents are homogeneous and the skill count is honest. Neither should ever gain a description in a standing skill list.

---

## Bottom line — four-test bar

| Library               | Skills                             | Clear the four-test bar as-is                                                   | Notes                                                                                                                        |
| --------------------- | ---------------------------------- | ------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| gc-*                  | 14                                 | **14/14**                                                                       | All clear on substance; aggregate desc verbosity (~1,330 words) is the only flag — no prunes                                 |
| cityops-*             | 11                                 | **11/11 on substance, 0/11 on the ≤40-word desc check**                         | Tighten all 11 (~628 words recoverable); sharpen debugging-playbook/failure-archaeology boundary                             |
| process-skills drafts | 3 skills (+1 fragment, +1 formula) | **3/3 as drafts**, but all 3 are supersessions of shipped skills, not additions | Promote-and-replace or discard; rename implementation-planning→gascity-pr-start; move orchestration-tick + mol-decompose out |

**Total that clear the bar as-is with no action: 14 (gc-\*).** The 11 cityops-* clear on content but every one needs a description tightened before it is paying its standing-context rent honestly. The 3 process-skills drafts are fine as drafts but must supersede — not join — their shipped namesakes; two folder entries there aren't skills at all.

Highest-leverage single action: tighten the 11 cityops descriptions (~628 words off permanent city-session context). Second: resolve the 3 draft-vs-shipped supersessions so no skill has two homes.
