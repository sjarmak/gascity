# Morning ledger — overnight autonomous run, 2026-07-07

Session: stephanie-adhoc (Fable), continuing the fable-sunset program
(dr-j0d + dr-i4v). Scope approved before sleep: gascity + city-ops campaigns
only; rules restructuring after comparison runs, with backup; everything
internal, no external actions.

## Completed before you slept

- 5 Fable golden baselines captured + verified (dr-j0d.1 Fable portion done)
- Rubric bank: 5 judge rubrics delivered, dr-i4v.4 CLOSED
- Campaign queue approved and recorded, dr-i4v.1 CLOSED
- Discovery: gascity fork report at discovery-gascity.md; city-ops report
  requested (agent finished, resend pending)
- Articulation wave (dr-i4v.3) launched: 5 process-skill drafts under
  process-skills/ (drafts only, live files untouched)

## DECISIONS TAKEN WITHOUT YOU (review these first)

### gascity campaign — provisional answers to discovery Q1-Q5

The discovery agent's five questions (full text in its report section below /
discovery-gascity.md). You chose "answer in notes now" before sleep but no
notes arrived, so authoring proceeds on these provisional positions; every
affected skill is revisable after your real answers:

- Q1 placement/audience → **PROVISIONAL: fork-local for your fleet**, written
  repo-portable (no machine-local path as load-bearing source) so upstreaming
  later is a copy, not a rewrite. No upstream commitment made while you sleep.
- Q2 hardest live problem → **PROVISIONAL: treat reconciler wake/nudge races
  as primary** (city_runtime.go 134 fix commits), managed-Dolt lifecycle
  second; gc-reconciler-lifecycle teaches the CURRENT idiom with a dated note
  that a redesign was promised (3bc34e0db) and to check before deep work.
- Q3 human-review routing rule → **PROVISIONAL RULE**: a PR must be held for
  human review when it introduces or changes a cross-subsystem contract
  (event payload shape, bead/molecule schema, provider protocol, formula
  format, public CLI surface) OR touches change-control/automation-gating
  itself. Derived from reverts b8120d697 + 19e34ab71 and your autonomy
  boundary. Marked provisional inside gc-change-workflow.
- Q4 dead limbs → **PROVISIONAL: all four parked-not-dead** (oversight-rig/
  Slack pack, delivery-phase state machine #3177, formula_v2, idle nudger).
  Skills fence them off ("do not re-land without maintainer decision") rather
  than declaring them dead forever.
- Q5 deepest worked examples → **PROVISIONAL: gc-reconciler-lifecycle and
  gc-change-workflow** get the deepest examples (orchestration-adjacent
  failures were the expensive ones in tonight's bead evidence: wedged
  convoys, claim abandonment, contract-landing reverts).

### city-ops campaign — provisional answers to discovery Q1-Q5

Report: discovery-cityops.md. Provisional positions for authoring:

- Q1 costliest incidents → **PROVISIONAL: ghost dolt servers + spawn storms
  ranked first** (they recur; six leaked dolt servers are running right now),
  supervisor OOM / binary freeze / quota burn as first-class archaeology
  entries behind them.
- Q2 permanent human gates → **PROVISIONAL**: all external artifacts, merges
  to shared refs, account/credential changes, city.toml topology changes.
  Existing push carve-outs documented AS-IS (incl. the 2026-06-19
  pre-authorized rig code pushes), not extended.
- Q3 trust map → **PROVISIONAL**: no subsystem documented as
  trusted-unsupervised without your word; current automation described with
  "spot-check recommended" defaults.
- Q4 mayor pin (claude-5 value vs claude-3 comment) → **NO CHANGE MADE**; the
  discrepancy itself is documented as an open flag for you.
- Q5 unwritten rules → seeded from known corrections (preview-before-execute,
  writing-voice bans, no honesty-signaling); wake-her thresholds left as an
  explicit documented GAP for you to fill.

### Live findings needing morning decisions (no action taken)

- SIX leaked dolt sql-servers running (polecat test worktrees + /tmp/city)
  alongside the canonical server — cleanup needs your go-ahead.
- Mayor provider pin contradiction in city.toml (value claude-5, comment says
  claude-3 intended).
- All 9 ADRs still status:proposed — city runs on v1.5 cron-reaper
  conventions; worth a ratify-or-retire pass.

### Other decisions

- New campaign skills are written DIRECTLY into /home/ds/gascity/.claude/
  skills/ (additive gc-* dirs, no existing file modified, untracked until you
  commit) — per the campaign prompt's write target. Process-skill IMPROVEMENT
  drafts (which replace existing artifacts) stay quarantined under
  process-skills/ pending consumer eval.
- bd port-discovery flake: reliable workaround BEADS_DOLT_PORT from
  sql-server.info; used for all bead writes tonight. Candidate for
  failure-archaeology + an upstream bd issue draft (NOT filed — needs your
  per-action approval).

## Your unanswered pre-sleep questions (answer when up)

1. Hardest live problem (framework + ops) 2. Costliest past failures
2. Unwritten never-do rules 4. Forever-human gates 5. Beyond-SOTA meaning
   Plus the five gascity discovery questions above and the city-ops discovery
   questions (added below when its report lands).

## Overnight log (appended as the night progresses)

- 02:4xZ gascity campaign Phase 2 authoring launched (14 skills, workflow:
  author x14 -> factual/doctrine/usability reviewers -> fixer).
- 02:5xZ city-ops discovery report landed (discovery-cityops.md); provisional
  answers + live findings recorded above. City-ops authoring queued to launch
  after the gascity workflow completes (burst control for rate limits).
- gascity workflow run wf_28845905-d6a; skills land in
  /home/ds/gascity/.claude/skills/gc-*.
- 02:54Z articulation wave complete: 6 drafts verified (110KB) under
  process-skills/; dr-i4v.3 CLOSED. All drafts, nothing live touched.
- ~04:00Z gascity campaign COMPLETE: 14 gc-* skills at
  /home/ds/gascity/.claude/skills/ (untracked, not committed). Review pass
  caught a stale-checkout skill (gc-release-ci-ops) and rewrote it against
  origin/main f828bbe4b — the 3-lens review is earning its cost. Full fix
  report: /tmp/claude-1000/-home-ds/2534b074-0475-481a-9b1c-371d33a64c34/tasks/wtzo2taqi.output
- ~04:0xZ city-ops campaign launched (wf_857d08f4-9e8): 11 cityops-* skills
  into /home/ds/gas-city/.claude/skills/ (additive). NOTE: these appear in
  city sessions once present — curation gate dr-i4v.6 prunes/tightens
  descriptions before they cost standing context.
- ~04:45Z city-ops campaign COMPLETE (15/15 agents): 11 skills + 3 read-only
  diagnostic scripts; fixer corrected rig count 17->21, inverted guest test,
  cross-skill rename; flagged (did NOT edit) stale CLAUDE.md workspace-name
  sentence. Full report: /tmp/claude-1000/-home-ds/2534b074-0475-481a-9b1c-371d33a64c34/tasks/wfdtoupi1.output
- ~04:38Z all 10 Opus/Sonnet comparison runs complete; 15/15 outputs verified
  under outputs/. Blinded copies made (scoring/BLINDING-MAP.txt); 5 Opus
  judges launched scoring A/B/C per task against the rubric bank (rubrics
  themselves under evaluation too).
- Rules tree + Codex mirror backed up: ~/.claude/rules.bak-2026-07-07-prerestructure,
  ~/.codex/AGENTS.md.bak-2026-07-07. Restructure starts after judges finish.
- ~04:50Z all 5 blinded judge reports in (scoring/). Task-03 headline:
  Fable 4.85 / Opus 4.35 / Sonnet 3.20 — Sonnet capped for
  boundary-laundering. Worktrees removed.
- ~04:50Z RULES RESTRUCTURE EXECUTED (dr-j0d.2 CLOSED): reference/ ->
  ~/.claude/rules-reference/ (out of load path), README rewritten,
  house-rules tiering = post-Fable SSOT, performance.md pins fixed. Standing
  load ~7.8K -> ~2.3K tokens/session. Codex mirror: no sync needed (tier
  content already excluded).
- 04:52-04:55Z RATE LIMIT: account session limit hit; diff-report synthesis
  (Fable) and curation audit (Opus) both failed before writing anything.
  Reset 3:30am America/New_York (07:30Z). Wakeup chain scheduled; both
  relaunch after reset, then final wrap. If the wakeup chain dies too, the
  two relaunch prompts are reconstructible from this ledger + bead notes
  (diff report -> dr-j0d.4 requirements; curation audit -> dr-i4v.6).
- ~11:26Z (post-reset relaunch, Stephanie confirmed reset): BOTH landed.
  Diff report: docs/evals/fable-baselines/diff-report-2026-07-07.md —
  Fable 4.93 / Opus 4.20 / Sonnet 3.60; task-04 TIER INVERSION (Sonnet 4.70 >
  Opus 4.20 by running the tests); highest-leverage fix = execution-evidence
  rule; dr-j0d.1 CLOSED, requirements wired into dr-j0d.4/.7.
  Curation audit: curation-audit-2026-07-07.md — 25/25 clear on substance;
  cityops descriptions need ~628-word tighten; 3 drafts are supersessions.

## RUN COMPLETE — morning decision queue (in priority order)

1. Answer the 10 discovery questions (gascity + cityops sections above) —
   skills marked "provisional" get a revision pass from your answers.
2. Six leaked dolt servers + /tmp/city config: approve cleanup.
3. Mayor pin: claude-5 (value) vs claude-3 (comment) — which is intent?
4. Curation apply: tighten 11 cityops descriptions; resolve 3 draft
   supersessions (promote-and-replace vs discard); 2 relocations.
5. dr-i4v.5 consumer eval (Sonnet with vs without artifacts) — daytime run,
   validates the drafts before anything replaces a live skill.
6. Remaining campaigns per approved queue: EB -> CSB+codeprobe -> mem -> scix
   (all need Fable — schedule against the sunset window).
7. Stale sentence in gas-city CLAUDE.md (workspace name now in pack.toml) +
   ADR ratify-or-retire pass — flagged by campaign agents.
8. gascity fork: 14 new gc-* skill dirs are untracked — commit when reviewed.

## Morning session (Stephanie's decisions applied, ~11:30Z+)

- CLEANUP DONE: six leaked dolt servers killed (all verified zero established
  connections first; gc dolt-cleanup dry-run protected them, so manual
  SIGTERM); /tmp/city removed. Canonical server untouched; gc beads health OK.
  NOT touched: PID 3879168 (a dolt server on ~/.beads/dolt, port 40191) — not
  in the leak list, possibly another session's home bead store.
- MAYOR PIN RESOLVED: accounts are fungible (identical except usage); the
  rule is quota-spreading, no per-agent affinity. Encoded into
  cityops-topology-contract + cityops-session-and-account-management
  (provisional markers replaced). Stale city.toml comment stays as changelog.
- Curation applied: implementation-planning/ renamed gascity-pr-start/;
  orchestration-tick -> prompt-fragments/, mol-decompose -> formula-drafts/;
  11 cityops description tightenings dispatched (agent).
- dr-i4v.5 consumer eval COMPLETE (~12:00Z): with-skill Sonnet won ALL FIVE
  blinded comparisons — 4.90/4.90/4.88/4.75/4.85 vs bare 2.8/3.40/3.35/4.25/
  3.50 (mean 4.86; Fable golden mean 4.93). PROMOTED to live with originals
  backed up in superseded-originals-2026-07-07/: gascity-triage,
  gascity-pr-start, gascity-review-incoming-pr, planner agent. NOT promoted
  (handed to dr-j0d.4): orchestration-tick fragment (mayor/PL prompt
  integration), mol-decompose formula payload (live-formula change control).
  CAVEAT: skills embed worked examples mined from these same five tasks, so
  lift magnitude is partly example-leakage — dr-j0d.7 re-runs on FRESH inputs.
- Description tightening applied: 11 cityops descriptions 1,113 -> 485 words.
