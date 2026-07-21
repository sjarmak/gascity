# Overnight maintenance ledger — gascity-maintenance-pl — 2026-06-18→19

Stephanie out; worked the ds-research `dr-*` city backlog. Started ~72 open `dr-*`
(she counted 47 earlier; store grew since). **Closed 33** (hygiene). **39 remain**,
fully triaged below.

## DONE — bead hygiene (33 closed, zero risk)
- **29 copilot-review-iterate beads** — all 10 underlying PRs MERGED/CLOSED (verified
  via gh 2026-06-18); review feedback moot. Massive dup loops collapsed (PR#2512 ×11,
  gascity-packs PR#15 ×11).
- **2 dated morning-triage briefings** (dr-f2fue5 05-15, dr-xvweaj 05-13) — ~1mo stale.
- **2 leaked controller wisps** (dr-wisp-12z session, dr-wisp-hfi nudge) — ephemeral,
  should've been reaped.

## DONE — 2 human decisions surfaced (mail mayor DECISION: + 🔴 channel)
- **dr-apxhk4 (P0)** — single-agent baseline-honesty gate (policy precedent). mail gc-387941.
- **dr-2juyh1 (P1)** — gc.on_fail=abort_scope enforcement, 4-way arch choice. mail gc-387943.

## BLOCKER — dispatch path dead for dr-* (operational, mayor)
`dr-*` beads live in store **city:ds-research**. `gc sling` formula-backed route resolves
beads in that store and **cannot find them** (`bead "dr-7ew0ql" not found in store
city:ds-research`); `gc bd --rig gascity show dr-7ew0ql` → "no issue found". So the gascity
polecat pool + mol-* formulas (which target the gascity RIG store, gc-* beads) cannot be
slung on these city-research beads. **No dr-* bead has ever appeared in the gascity sling
log** — this is structural, not a transient outage. The 4 gascity-repo-IMPLEMENTABLE items
below need re-filing as gascity-rig beads or gastownhall/gascity GH issues before any polecat
can take them. That re-routing is a mayor call (some are city-tooling and should NOT become
gascity-rig beads).
- Orphan worktree `/home/ds/gascity-worktrees/polecat-4-7ew0ql` left by the probe sling
  (gc-sling staged it before `gc sling` failed). Empty, off main. **Mayor please reap** —
  PL holds a no-git boundary.

## GASCITY-REPO implementable (4) — ready once re-routed
- P0 dr-7ew0ql — gc-doctor env check (CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS) + ~50-line config linter. cmd/gc/doctor_*.go. VERIFIED, bounded.
- P0 dr-rbug2g — status_changed view over events table. internal/events. (Prereq for dr-5faybt + eval golden sets.)
- P3 dr-astk58 — refactor EffectiveWorkQuery (factor dup tier1/2/3/control-dispatcher).
- P2 dr-01zan7 — negative-surface failure-mode sections in gascity + gascity-packs AGENTS docs.

## ANALYSIS/RESEARCH (4) — sequence after drain-rate
- P0 dr-bzdeso — reproduce 21% drain-without-commit from Dolt (bin/bead-janitor:13). **GATES** dr-0b9tfx and any drain pass-bar. Do first.
- P0 dr-0b9tfx — MAST failure-taxonomy retro (gated on dr-bzdeso).
- P1 dr-ji4q3v — A/B multi-agent vs single-agent (empirical backing for dr-apxhk4).
- P1 dr-7ujcxq — canary/triadic scorer over interactions.jsonl (no LLM judge).

## OPERATIONAL/CITY — mayor lane (20)
Capacity: dr-9ur1ud (P0 BUG, gc-capacity provider pins — root cause of multi-worker
rate-limit freeze), dr-lr5az (P2, weight 7d window). Slack/PL reachability: dr-dd45jd (P1,
reaper skips named-identity PL binding — PL unreachable 2× for hours; affects ME), dr-9y620w,
dr-s1mcig, dr-du2l4r, dr-gkgyes, dr-1lf4h3 (PL loop-close wiring — my brief depends on it),
dr-xbs45x. Pool/lifecycle: dr-ubezej (session close not SIGTERMing polecat), dr-uvafis
(polecat-2 stuck stopped), dr-x590qz (codeprobe control-dispatcher flaps). Telemetry:
dr-n1xm4q (OTel URLs). Process: dr-ghn6wt (5-night nightly-cycle guard), dr-nsmn4w
(merge codeprobe feature branch, 24 unmerged commits — git ops), dr-fshqho (pre-drain
verify-artifact gate, bin/bead-janitor). Design/policy: dr-0w58aw, dr-tctp8o
(one-bead-per-session), dr-45le2r (dispatch-table elasticity), dr-i06w09 (Lexler patterns).

## ARCH-PARKED — Stephanie/Julian eventually, not urgent (5)
dr-1l9hc4 (auto-ship arch, green-lit, needs design doc autonomous-pr-ship.md + children),
dr-s0xu73 (end-to-end PR loop, Phase 1 done), dr-3n0xcw (reviewer-team ZFC param,
investigation-gated), dr-8pmw1a (v2 ADRs pending review+upstream PR), dr-5faybt (Shepherd,
blocked on dr-rbug2g).

## OUT-OF-LANE epic (4) — not gascity
dr-2vydrm + .3/.6/.9 — three-benchmark QA framework (CodeScaleBench / EnterpriseBench).
Cross-rig benchmark work; belongs to those rigs, not gascity maintenance. Mayor reassign.
