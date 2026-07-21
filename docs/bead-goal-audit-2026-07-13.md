# Bead-store hygiene audit — 2026-07-13

One-off Stephanie-approved cleanup pass. **Proposals only — nothing was closed,
merged, or edited.** All queries read-only from `/home/ds/gas-city`.

Stores audited: city store (dr-\*), rigs enterprisebench, mem, gascity,
gascity-packs. Method: full JSON dumps per store, mechanical sweeps (stale >3wk
no-assignee, orphans P2+ untouched 14d+, blocks-graph cycle detection,
closed-blocker edges), then semantic review of every flagged bead against
descriptions/notes/metadata, the priority bands in
`docs/conventions/bead-declaration-rubric.md`, and the stranded-branch list in
`docs/rca-eb-merge-gap-2026-07-12.md` §3. gh cross-checks (issue/PR state) were
run read-only where a bead names an upstream number.

Store snapshot at audit time:

| store | total | open | blocked | in_progress | closed | open P0/P1 |
|---|---|---|---|---|---|---|
| city (dr-\*) | 150 | 32 | 2 | 3 | 111 | 2 / 14 |
| enterprisebench | 1280 | 268 | 11 | 0 | 1001 | 43 / 63 |
| mem | 1363 | 113 | 21 | 5 | 1222 | 0 / 23 |
| gascity | 3485 | 353 | 29 | 3 | 3093 | 3 / 157 |
| gascity-packs | 1201 | 190 | 11 | 0 | 999 | 0 / 24 |

Blocks-graph cycle check: **no cycles among live beads in any of the four rigs**
(the zeldascension-lhlo.8↔.9 pattern does not occur here).

---

## 1. City store (dr-\*)

### Stale / overtaken

- dr-d3g | close-stale | synthetic P0 input convoy tracking dr-8sy, which closed 2026-07-13; 0 of 111 closed city beads are convoys — nothing ever closes them
- dr-1uo | close-stale | synthetic convoy tracking dr-e6l, closed 2026-07-13
- dr-701 | close-stale | synthetic P1 convoy tracking dr-dcd, closed 2026-07-13
- dr-l66ch | close-stale | own description: "VERIFIED 2026-07-06: currently RESOLVED" — mayor-alias collision settled, dups self-drain, harm low; durable fix (option a) belongs as a gascity rig bead if still wanted
- dr-i4v.2 | close-stale | "Must complete while Fable access lasts" — Fable access removed 2026-07-07 (same day it was filed); salvage continues via in_progress dr-i4v.5/.6 which are model-agnostic
- dr-1l9hc4 | close-stale | 62d untouched P1; autonomous polecat issue→PR ship is overtaken by landed mol-pr-from-issue + automerge-order machinery; notes devolved into tracking upstream #1440/#1986; scope also overlaps dr-s0xu73
- dr-s0xu73 | close-stale | 62d untouched P1; triage→auto-ship→merge→notify loop overtaken by the landed merge machinery / overnight pipeline; residual slack-publish defect is tracked separately (dr-6vhzid)
- dr-i06w09 | close-stale | 63d untouched P2 planning epic (Lexler pattern mapping), never started, no children, no assignee; re-file if still wanted

### Miscalibrated priority

- dr-2vydrm | demote-to-P2 | P1 epic untouched 22d with no assignee; P1 band requires naming what it blocks now — nothing is waiting
- dr-2vydrm.3 | demote-to-P2 | child of above, same inactivity, no assignee
- dr-2vydrm.6 | demote-to-P2 | child of above, same inactivity, no assignee
- dr-2vydrm.9 | demote-to-P2 | child of above, same inactivity, no assignee
- dr-2juyh1 | demote-to-P3 | "Decide: gc.on_fail=abort_scope" — 62d untouched, names no waiting consumer; upstream gascity#1657 owns the enforcement semantics
- dr-dd45jd | demote-to-P2 | P1 for 8 weeks with a documented manual workaround (`gc slack bind-room` after respawn); own notes say "leaving open as a real scoped decision", i.e. nobody is blocked now

### Blocked-pile

- dr-7smz8 | unblock-stale-edge | sole blocker dr-w1k9r closed 2026-07-06 as redundant (gc-454422 correction); the /status-hang issue itself is still live (07-11 storm evidence in notes) — bead should return to open

### Orphans

- dr-y3yak | needs-goal-link | P3 rotation-reaper idempotency defect, 17d untouched, no parent/epic; impact mitigated by the standing dispatcher watchdog

Kept without action (checked): dr-tyq (P0 correctly banded — CRITICAL stranded
score-forgery fix, mayor-assigned), dr-t1m, dr-rz3, dr-94s, dr-j0d family
(capacity-parked per the 2026-07-07 salvage decision — KEEP), dr-i4v epic +
.5/.6, dr-1h19c (properly deferred), rubric-wiring beads (dr-jeu, dr-0q3,
dr-1nu), fresh 07-11..13 beads.

City subtotal: 16 proposals.

---

## 2. enterprisebench

### Semantic duplicates

The known RCA loop (audits inspect main where stranded fixes are absent, then
re-file) produced verified duplicate filings:

- EnterpriseBench-4hk7h | close-dup-of-EnterpriseBench-ssikq | identical defect (missing `eb_verify.plugins.file_extraction`, same 2 tasks), filed 07-12T11:37 with less detail; ssikq is canonical (P0, repro, spawned follow-up rmz1x)
- EnterpriseBench-9gt1f | close-dup-of-EnterpriseBench-ssikq | third filing of the same missing-module bug, 07-12T13:06, identical two task paths
- EnterpriseBench-bfrfm | close-dup-of-EnterpriseBench-p5wrm | both diagnose the same root cause (glka.2/kyo34/chc2z fixed-but-unmerged, same commit-ahead counts); p5wrm is the systemic writeup the dr-tyq backfill tracks against; fold bfrfm's unique detail (glka.2 branch's `grep -oP` "999" pass-through check) into the dr-tyq checklist

Explicitly checked and ruled NOT duplicates (each names a distinct code
path/mechanism): 6c9wp, ffeu6, sh0fn, wto43, 6ca3n, 8cc10, yli6k, q044f,
cj9zo, 7x8v, jrgs, qrxg6.

### Stale / overtaken

45 of the 46 mechanical stale candidates are `Rollup(enterprisebench)` mayor
tick/status snapshots — point-in-time log entries superseded by dozens of later
ticks (current tick 356mo/1p9rq), plus one empty placeholder:

- EnterpriseBench-iqt5 | close-stale | c:05-31 "watch next tick" — resolved next day by 406a
- EnterpriseBench-rh8q | close-stale | c:05-31 "mayor re-notified" — same convoy resolved by 406a
- EnterpriseBench-406a | close-stale | c:06-01 self-describes resolution ("CLOSED pass, infra thread closed")
- EnterpriseBench-oput | close-stale | c:06-01 "rig healthy-idle" snapshot, 6 weeks stale
- EnterpriseBench-zn7l | close-stale | c:06-03 "wave-1 gated on mayor" — waves 1-4 completed same week
- EnterpriseBench-mlz2 | close-stale | c:06-03 "wave-1 LAUNCHED" — superseded same day
- EnterpriseBench-2gj0 | close-stale | c:06-03 "batch-2 launched" — superseded by kp0 (88/88, 06-04)
- EnterpriseBench-2584 | close-stale | c:06-03 "audit dispatched" — mirror policy closed by kp0/g2g
- EnterpriseBench-ejnh | close-stale | c:06-03 "batch-3 advancing" — campaign closed (g2g, 06-06)
- EnterpriseBench-iajt | close-stale | c:06-03 claim-reset historical note, superseded by hpyx trail
- EnterpriseBench-b7xh | close-stale | c:06-03 "HARD-STOPPED on hpyx" — campaign closed 06-06
- EnterpriseBench-ulr0 | close-stale | c:06-03 "ORPHANING wave-1 (recovered)" — recovered same tick
- EnterpriseBench-rn47 | close-stale | c:06-03 "105 missing mirrors" — closed 88/88 by kp0
- EnterpriseBench-eei0 | close-stale | c:06-03 "gated on VALIDITY AUDIT" — audit CLEARED per 5whp 06-25
- EnterpriseBench-lw83 | close-stale | c:06-03 historical finding, absorbed into gt9d same day
- EnterpriseBench-gt9d | close-stale | c:06-03 "turning point" narrative beat, purely historical
- EnterpriseBench-x1h | close-stale | c:06-04 "awaiting suite-lock answer" — suite LOCKED per g2g
- EnterpriseBench-pyb | close-stale | c:06-04 claim-reset historical, superseded
- EnterpriseBench-nte | close-stale | c:06-04 "landed branch-ready" historical
- EnterpriseBench-h4l | close-stale | c:06-04 "mirror batch resized" — closed 88/88 same day
- EnterpriseBench-ktr | close-stale | c:06-04 claim-reset historical, superseded
- EnterpriseBench-2mk | close-stale | c:06-04 claim-reset historical, superseded
- EnterpriseBench-c8o | close-stale | c:06-04 "wave-2 mirrors greenlit" — closed same day
- EnterpriseBench-82d | close-stale | c:06-04 "wave-2 COMPLETE" — historical
- EnterpriseBench-oan | close-stale | c:06-04 "wave-3 dispatched" — closed same day
- EnterpriseBench-50q | close-stale | c:06-04 paper-lock ask — both decisions resolved same day (6w9)
- EnterpriseBench-vmx | close-stale | c:06-04 "wave-4 FINAL dispatched" — closed same day
- EnterpriseBench-w3c | close-stale | c:06-04 "mirrored to Stephanie" — resolved same day (6w9)
- EnterpriseBench-kp0 | close-stale | c:06-04 self-describes resolution ("CLOSED at 88/88")
- EnterpriseBench-6w9 | close-stale | c:06-04 self-describes resolution ("all 3 decisions resolved")
- EnterpriseBench-6v8 | close-stale | c:06-05 self-describes resolution ("CLOSED")
- EnterpriseBench-ptn | close-stale | c:06-05 historical dispatch note
- EnterpriseBench-1pl | close-stale | c:06-05 historical ("P0s closed")
- EnterpriseBench-v8e | close-stale | c:06-05 historical dispatch note
- EnterpriseBench-cpk | close-stale | c:06-05 historical (".22+.25 closed")
- EnterpriseBench-s6j | close-stale | c:06-05 P4, description AND notes empty — dead placeholder, 38d untouched
- EnterpriseBench-g2g | close-stale | c:06-06 self-describes resolution ("campaign epic closed, headline recorded")
- EnterpriseBench-0db | close-stale | c:06-12 historical (audit answered, dispatched)
- EnterpriseBench-7zp | close-stale | c:06-12 "dse in flight" — closed next day (nop)
- EnterpriseBench-nop | close-stale | c:06-13 self-describes resolution ("dse CLOSED")
- EnterpriseBench-s63 | close-stale | c:06-14 self-describes resolution ("fully resolved… rig idle")
- EnterpriseBench-064 | close-stale | c:06-14 historical ("branch-ready")
- EnterpriseBench-fgk | close-stale | c:06-15 self-describes resolution ("GREEN 0/180")
- EnterpriseBench-0t0 | close-stale | c:06-15 self-describes resolution ("CLOSED")
- EnterpriseBench-oslz | close-stale | c:06-16 self-describes resolution ("CLOSED")

Plus 14 more rollup/status beads from the 14-day orphan window (06-22..06-29),
same disposition:

- EnterpriseBench-mchf | close-stale | c:06-22 DEEP_AUDIT "dispatch+channel infra down" — superseded by current 07-11/12 cluster
- EnterpriseBench-9eie | close-stale | c:06-25 "dispatch DOWN" — transient outage, long resolved
- EnterpriseBench-rq8s | close-stale | c:06-25 "k4tv re-run UNBLOCKED" — k4tv headline CLOSED per xo48
- EnterpriseBench-e8lk | close-stale | c:06-25 historical token-unblock note
- EnterpriseBench-ndd7 | close-stale | c:06-25 self-describes resolution ("RESOLVED")
- EnterpriseBench-5whp | close-stale | c:06-25 "c7wb fix branch-ready" — LANDED per k5l6 (06-28)
- EnterpriseBench-s08a | close-stale | c:06-26 self-describes resolution ("RESOLVED")
- EnterpriseBench-8g8w | close-stale | c:06-26 "cdzi rebase BLOCKED" — cdzi's live status tracked in cdzi itself
- EnterpriseBench-xo48 | close-stale | c:06-27 self-describes resolution ("CLOSED")
- EnterpriseBench-5566 | close-stale | c:06-27 historical unstick note
- EnterpriseBench-wte0 | close-stale | c:06-27 self-describes resolution ("COMPLETE")
- EnterpriseBench-rh6h | close-stale | c:06-27 self-describes resolution ("LANDED… settled")
- EnterpriseBench-k5l6 | close-stale | c:06-28 self-describes resolution ("LANDED on origin/main")
- EnterpriseBench-wdz7 | close-stale | c:06-29 "4 hygiene beads dispatched" — subsumed by 07-11/12 verifier-soundness cluster

Kept open deliberately: **EnterpriseBench-hpyx** (real never-root-fixed
reconciler claim-reset bug, 6 logged recurrences, only ever worked around) —
needs a Stephanie decision: root-fix or accept the workaround permanently.

### Miscalibrated priority

**27 of EB's 43 open P0s (63%) and 8 of 63 P1s are workflow scaffolding with
blank descriptions** that mechanically inherit the root bead's priority
(`gc.kind=workflow-finalize` / workflow containers / synthetic convoys, all
routed to core.control-dispatcher, none human-actionable):

- EnterpriseBench-fg8nx, i3qx9, r8kmf, inh8n, bab0n, hq75x, 5cntt, sraec, ycq45, v93ts, gy436, 4h8f, xncd, yv5n, 1ib6, wj5e, ev8o | demote-to-P3 | "Finalize workflow" gc.kind=workflow-finalize, description blank, priority copied from root — 17 of 43 open P0s
- EnterpriseBench-859k2, cvm1o, 4o2dd, 7vw3u, 2s1y, e4fj | demote-to-P3 | same workflow-finalize class at P1, description blank
- EnterpriseBench-xqtsj, 98otd, j3gze, 6gxc5, 90z5 | demote-to-P3 | bare "mol-focus-review" gc.kind=workflow containers, boilerplate formula text — 5 more open P0s; 8brs5 (open) documents pool nudges wrongly offering exactly this class as claimable work
- EnterpriseBench-c7ga | demote-to-P3 | same container class at P1
- EnterpriseBench-u07d, ncaf, eg9f, w1gt7 | demote-to-P3 | "input convoy" gc.synthetic=true, blank description — 4 more open P0s
- EnterpriseBench-08e3 | demote-to-P3 | same synthetic-convoy class at P1
- EnterpriseBench-5u5by | demote-to-P3 | templated review-step script (gc.step_ref=mol-focus-review.review), not a finding — P0

Caveat (operational, flagged by the analyst): demoting live workflow beads
changes dispatch behavior — confirm with the dispatch owner that lowering
priority won't starve the mol-focus-review pipeline these steps gate. The
better structural fix may be to stop scaffolding beads inheriting root priority
at all (see cross-store patterns).

No miscalibration found among the remaining ~65 substantive P0/P1 beads — each
names concrete measured impact.

### Blocked-pile (11 blocked)

- EnterpriseBench-f53i2 | unblock-stale-edge | blocks-edge on EnterpriseBench-3501c, closed 2026-07-12T14:03Z; orphaned finalize wisp (its DISARMED payload would have run `bd close kyo34`) — clear the edge or close the wisp under the dr-t1m finalize-gap fix

Legit holds (verified, leave alone): cdzi (parked pending UNIT-B landing +
Stephanie go, branch-ready), jn73.2 (escalated to sjarmak, 4th reject at same
SHA), jn73.1 (explicit HALT for Stephanie sign-off), 7rc1 (escalated 3rd
same-SHA reject), pakh (gc.hold_reason=direct-dispatch-only, mayor note: do NOT
re-sling through any formula), yn0a8 (blocker 5u5by genuinely open), 42o8
(blocker jn73.10 genuinely open — side-note: its yqif gate was closed against
its own "DO NOT CLOSE THIS GATE" instruction; safety margin narrowed),
ubqcr/kzkjw/6ui1 (correctly parked on blocked targets per coada D1).

EB subtotal: 98 proposals.

---

## 3. mem

### Stale / overtaken

Same dangling-convoy pattern as the city store — roots closed 2026-07-13,
`tracks`-only edges now inert (finalize-gap pattern):

- mem-gqdy3 | close-stale | convoy for mem-03acq, closed 07-13T19:45Z
- mem-4dr1b | close-stale | second convoy copy for mem-03acq
- mem-ckq4l | close-stale | convoy for mem-0xz9b, closed 07-13T20:04Z
- mem-mauyh | close-stale | second convoy copy for mem-0xz9b
- mem-w926m | close-stale | convoy for mem-ljp8b, closed 07-13T19:55Z
- mem-efcjy | close-stale | second convoy copy for mem-ljp8b
- mem-3pig8 | close-stale | convoy for mem-zfeys, closed 07-13T19:51Z

(mem-t5btp/mem-200ju convoys kept — their roots are still live. All 5
mechanical stale candidates — mem-sxe, mem-e71, mem-ajj, mem-ux2, mem-2hm —
checked out as live backlog, not proposed.)

### Orphans

- mem-2hm | needs-goal-link | real scoped tech-debt (shared claude-headless+telemetry package across tom-swe/brains/mem), zero deps, untouched 32d, no epic

(10 mechanical candidates dropped as false positives: all `label:rollup` audit
digests; mem-0ut dropped — parent of the active mem-0rrf via description links.)

### Miscalibrated priority

- mem-ltte | demote-to-P2 | P1 epic whose own 07-03 audit note says the acceptance artifact is empty (yield=0, research/data/ absent on main) and warns "do not schedule … against this dataset" — names nothing it blocks now

No other demotions: root epics are structural P1s; the 15 July-13 wisp P1s
inherit from in_progress P1 parents (mechanical, active).

### Blocked-pile (21 blocked)

- mem-lvp.27 | unblock-stale-edge | blocker mem-pjh8 closed with real completion (mem-pjh8.1 branch-ready, 74 tests green); lvp.27 itself already carries gc.outcome=pass/branch-ready — edge is vestigial
- mem-lvp.31 | unblock-stale-edge | both blockers mem-lvp.6/.6.1 closed with genuine completions (commits + code-reviewer APPROVE)
- mem-0rrf.4 | (note — do NOT unblock) | blocker mem-0rrf.3 closed with gc.outcome=fail/dispatch_env_unprovisioned (a false-close: deliverable never produced); the prerequisite was actually delivered by mem-6bsd (branch-ready 07-05) — when the A/B hold lifts, repoint the dependency to mem-6bsd rather than trusting mem-0rrf.3's close

Legit holds: all 17 mem-0rrf.\* blocked beads carry
gc.hold_reason=reserved-ab-substrate (verified per-bead) — deliberate A/B
substrate reservation. mem-lvp: P1 parent epic blocked-by-design ("progress via
children only") — legit but sitting in `blocked` status is a status-model
mismatch worth a cosmetic fix.

mem subtotal: 11 proposals (+1 repoint note).

---

## 4. gascity

### Semantic duplicates

- gc-bfq12 | close-dup-of-gc-09bs3 | both target the #2978 pool-wedge fix (same day 06-03); gc-09bs3 is the fully-scoped Candidate-A v2 fix; bfq12's "verify gc.continuation_group" step is wholly subsumed
- gc-ke1x2 | close-dup-of-gc-svpn7 | both are mol-adopt-pr roots for PR #3420; ke1x2's child closed 07-11 APPROVE-but-gh-401 and never progressed (finalize-gap wedge); svpn7 re-dispatched the same PR today now that gh auth works

### Stale / overtaken

- gc-w33sk | close-stale | copilot-iterate bead against PR #3958 commit 4eb06e93; head has advanced to db0f9797, mergeStateStatus=CLEAN — superseded by today's gc-e9mr6
- gc-3n6q6 | close-stale | own recommended action ("close #2321 as duplicate") already done: #2321 CLOSED, PR #2145 MERGED (gh-verified)
- gc-vk82d | close-stale | halt-to-mayor memo for #2319; issue CLOSED — fix landed via another path
- gc-3xosa | close-stale | "branch ready … closes #2320"; #2320 CLOSED — awaited push moot
- gc-jjlfl | close-stale | mail bead for #2293 blast-radius; #2293 CLOSED
- gc-q246i | close-stale | "branch ready … #2293 Shape A"; #2293 CLOSED
- gc-zv90z | close-stale | own text "State: RESOLVED-DIRECTION 2026-06-25"; PR #3733 CLOSED consistent with the split-not-merge direction
- gc-ed3nf | close-stale | rollup premised on "PR #3474 still unmerged"; #3474 MERGED — instance unstuck; root cause tracked in still-open gc-yfn01
- gc-8h5ls | close-stale | own 07-09 note: "scope is ALREADY RESOLVED … fix landed at the FORMULA level … mayor decides — close gc-8h5ls as resolved"; engine-level rewire already split out to open gc-fzvtu
- gc-dvvym | close-stale | own 07-09 help_request: "PREMISE NOW STALE … pool moved to per-bead-worktree provisioning … RECOMMEND close as resolved-by-arch-change"
- gc-fgt01, gc-hj0lc, gc-l3a5w, gc-rrd98, gc-v0tkn, gc-xx43z, gc-ya08x | close-stale | 7 orphaned mol-focus-review step beads under root gc-vkruy, force-closed by mayor 07-12 ("bleed stopped"); target gc-n9gyw re-scoped to an engine-only fix that doesn't need these steps
- gc-1b2.1, gc-1b2.2, gc-1b2.3 | close-stale | P3 "pure cleanup, not blocking" follow-ups from a 05-04 PR review whose anchor bead doesn't exist in-store; 2+ months untouched
- gc-g42mt | close-stale | asks for SetConnMaxIdleTime + withReadTx retry; beads v1.1.0 already ships both (store.go:643/299, per the 07-13 /status-hang correction gc-454422)
- gc-jprqq, gc-1roez, gc-3wmx4, gc-k9qsc, gc-96iqd, gc-jafzx, gc-u63hi, gc-yi1b2, gc-jqbnq, gc-os0da, gc-wlc10, gc-jnvm2, gc-l179q, gc-vahi9, gc-0ib08, gc-gvk8e, gc-liko9, gc-v83ga, gc-9rm09, gc-k2lao, gc-ttfk0, gc-dlg04, gc-z2zrl, gc-gfmk5, gc-kyg6d, gc-l3ab7, gc-sllg0 | close-stale | 27 synthetic test fixtures ("decoy bead"/"timing target NN", metadata timing_worker=N, empty descriptions, all created 06-29, never touched) — a benchmark/timing run leaked into the live store instead of a test store

### Orphans

- gc-en0o3 | needs-goal-link | text says "cost-side slice of P0 honesty gate gc-viqje" but carries no dep edge — link parent gc-viqje
- gc-u2qvo, gc-1z3fq, gc-4knkh, gc-fe3mk, gc-yku6x | needs-goal-link | 5 "guardrail:" beads all citing "[Audit 2026-06-28, parent gc-3gx8b]" in-text with no edge — link parent gc-3gx8b
- gc-zv1az | needs-goal-link | real defect (hook --claim --drain-ack timeout under slow dolt), untouched since 06-25, no epic
- gc-107em | needs-goal-link | real defect (gcsync installs to GOPATH/bin but sidecars exec stale /home/ds/go/bin/gc), no epic
- gc-f5wek | needs-goal-link | real defect (graph.v2 controller drops claims on workflow latch beads), no epic
- gc-fwnkr | needs-goal-link | real defect (mol-pr-ship doesn't write the /gascity-ship sentinel), no epic
- gc-fuf | needs-goal-link | real bug (oversight-rig PL template renders `<rig>` unsubstituted), open since 05-04, no epic

(~50 further mechanical orphan candidates are ordinary backlog; no action.)

### Miscalibrated priority

- gc-viqje | demote-to-P2 | P0 but self-described structural/preventive gate ("premortem's one durable winner"), not active damage now
- gc-oh5cu | demote-to-P1 | P0 doctor/linter addition against silent capability degradation — real but preventive tooling
- gc-ti1gp | demote-to-P2 | own text: "the immediate fix is in place"; remaining scope is defense-in-depth
- gc-vqfhf | demote-to-P2 | retrospective A/B analysis, blocked on gc-zrt5r, no current-blocking impact named
- gc-841z4 | demote-to-P2 | own text explicitly defers ("do NOT sequence cost-router behind this")
- gc-0nc36 | demote-to-P2 | speculative shepherd meta-agent; depends on gc-9rmqo which does not exist in the store (dangling ref)
- gc-b1t84 | demote-to-P2 | infra "verified to exist" per own text; remaining ask is a 5-night proof run
- gc-8txxz | demote-to-P2 | blocked on a replay-eval harness that "does not yet exist"; CI-hardening investment
- gc-j8lub, gc-lh53p, gc-w4w5f, gc-zrt5r | demote-to-P2 | honesty-gate instrumentation C.1–C.4 cluster — measurement program, no named current blocker
- gc-5urel | demote-to-P2 | CONTRIBUTING/contributor-lifecycle docs initiative, nothing waiting
- gc-yn516 | demote-to-P2 | speculative control-theory retrofit from a vault note, no concrete impact
- gc-i1mog, gc-i1mog.5, gc-i1mog.6 | demote-to-P2 | long-horizon OR-scheduling research-adoption cluster, admit-one-at-a-time by design

### Blocked-pile (29 blocked)

- gc-6x2cs | unblock-stale-edge | sole blocker gc-too7n is closed
- gc-ghc6d | unblock-stale-edge | both edges resolved (tracks gc-irfwa closed, blocks gc-3odco closed)
- gc-h4c4e | unblock-stale-edge | gh-auth root-fixed today (dr-e6l: tmux global-env GITHUB_TOKEN shadow); rebase of 2 DIRTY maintenance PRs is now unblocked
- gc-8exjp | unblock-stale-edge | same gh-auth root fix; push+PR-open for 4 branch-ready maintenance PRs now unblocked

Legit holds (verified): gc-xd77u (box-health hold on approved project),
gc-h3t89, gc-yfn01 (maintainer AC3 decision), gc-d4s4h (design decision),
gc-jbnfc (human-gate awaiting Stephanie), gc-tfuf8 (worktree bug 5x confirmed),
gc-m5hun (mem-rig SSOT decision first), gc-zie4f (needs human git push),
gc-nby7oo (deliberate mayor hold — but note: upstream #2814 now shows CLOSED;
worth a mayor re-check whether the hold can lift), gc-n5j (beads-repo PR flow),
gc-kxk5h/gc-11021 (fresh PR scaffolds).

gascity subtotal: 82 proposals.

---

## 5. gascity-packs

### Semantic duplicates

- gpk-egzub | close-dup-of-gpk-xs3ev | both gc.synthetic input convoys tracking gpk-3uzz (same tracks-edge), created 5h apart on 07-09, both empty
- gpk-a2qhj | close-dup-of-gpk-y9c07 | both filed 07-12 reporting "P1 arch-drift molecule fully CLOSED, awaiting merge"; a2qhj's title cross-references y9c07 and adds no new fact

### Stale / overtaken

- gpk-0it4 | close-stale | 06-15 informational rollup, superseded by 15+ later rollups
- gpk-vfp3 | close-stale | PR #128 MERGED (gh-verified); "landed" fact stale since 06-20
- gpk-h0zx | close-stale | all 5 cited PRs resolved (#109/#75/#89 MERGED, #88/#64 CLOSED, gh-verified); pool-stall diagnosis superseded
- gpk-yq22 | close-stale | 07-04 slack-full snapshot superseded by gpk-nvxk (07-09) then gpk-j5pex (07-13)
- gpk-z8qb4 | close-stale | 07-11 "in flight" snapshot superseded by gpk-y9c07/a2qhj (molecule closed, branch on origin)
- gpk-0ag4 | close-stale | own appended note: "cluster RESOLVED" (07-07), all 7 queued items drained
- gpk-9h6hf | close-stale | reports "#186 merged" as historical fact (gh-confirmed), no outstanding action
- gpk-vts5 | close-stale | own title declares the issue resolved ("bd 1.0.4↔1.0.5 no longer firing — bd now 1.0.5"); untouched since 06-28
- gpk-6yfct | close-stale (or unset gc.routed_to) | blocked control bead re-consuming polecat spawns (4 sessions: gc-466926/467144/473173/480942), worktree re-provisioned without a git repo — known blocked-bead/live-routed_to no-op-spawn pattern; break the loop pending mayor re-stage of the gpk-t1ks9 subtree

**Mass finding — 129 live molecule-step beads under 10 already-CLOSED workflow
roots** (mechanically verified: gc.root_bead_id status=closed, child open, zero
exceptions; generic scaffolding titles like "Clean up the worktree", "Finalize
the work item", "Run preflight checks", "Load context"). All 129 |
close-stale | dead leaves of finished molecules; 22 of them are P1s driving the
rig's P1 count:

- gpk-g9eq (closed 07-07), 11 children: gpk-0tbv, gpk-34sn, gpk-52uw, gpk-5jrz, gpk-9nv7, gpk-llub, gpk-m9o4, gpk-ng0d, gpk-nkjg, gpk-tpp7, gpk-vwg3
- gpk-m3yn (closed 07-07), 11: gpk-75sm, gpk-9si8, gpk-cnl2, gpk-j0fo, gpk-knhc, gpk-pfrl, gpk-rzq2, gpk-smqx, gpk-szr9, gpk-xaik, gpk-y7dg
- gpk-47nm (closed 07-07), 11: gpk-1291, gpk-1uac, gpk-3mt0, gpk-49kw, gpk-4de5, gpk-89my, gpk-ac6e, gpk-bu8w, gpk-hzzd, gpk-lgld, gpk-nv2f
- gpk-ptcp (closed 07-07), 11: gpk-0jo5, gpk-0vj5, gpk-5o8c, gpk-f0j5, gpk-f2we, gpk-g3v0, gpk-k2mk, gpk-nnlq, gpk-xxe2, gpk-ymxo, gpk-zppu
- gpk-c30t (closed 07-07), 11: gpk-068w, gpk-2ig1, gpk-31h3, gpk-3p8r, gpk-76tx, gpk-dh7m, gpk-ki01, gpk-l7r3, gpk-lubh, gpk-n8wy, gpk-qk30
- gpk-t2hr (closed 07-07), 11: gpk-848b, gpk-auki, gpk-dul0, gpk-j5a8, gpk-m4ce, gpk-phhh, gpk-r5ia, gpk-u51j, gpk-uz8m, gpk-vrpy, gpk-whak
- gpk-n0r5f (closed 07-10), 20: gpk-05d6a, gpk-0ucyt, gpk-0zjqx, gpk-1c8vy, gpk-2w6ty, gpk-3jivb, gpk-5h91f, gpk-7jxnu, gpk-8m0vc, gpk-8w60d, gpk-apgkh, gpk-bxw61, gpk-h5i9i, gpk-i41sd, gpk-mdmf1, gpk-n0nts, gpk-p7l5x, gpk-suhpd, gpk-swu3q, gpk-xrx4y
- gpk-2spof (closed 07-10), 21: gpk-1xl1s, gpk-2ikmp, gpk-3231j, gpk-37x5d, gpk-642rs, gpk-8yn9i, gpk-btuqo, gpk-cbfe4, gpk-dqv1s, gpk-h0jqc, gpk-hp6tj, gpk-i768j, gpk-jkd2i, gpk-ksmmc, gpk-lckk1, gpk-mtwk8, gpk-sx5uk, gpk-u6o88, gpk-uv8l1, gpk-x9rc6, gpk-y20of
- gpk-d4mf6 (closed 07-10), 21: gpk-1u8vh, gpk-3n0l7, gpk-64go4, gpk-8jsyj, gpk-bm0ti, gpk-ef51k, gpk-g5n1a, gpk-hbs8n, gpk-hi92k, gpk-kdtaa, gpk-me6cg, gpk-mpstz, gpk-mzq9g, gpk-ojjid, gpk-rnd32, gpk-s9qpk, gpk-vqxi3, gpk-wrf1t, gpk-wtgz4, gpk-xyhv2, gpk-zihhv
- gpk-g2uwe (closed 07-10), 1: gpk-ix9q7

This single pattern accounts for ~64% of all live beads in gascity-packs.

### Orphans

- gpk-4g26 | needs-goal-link | 06-27 handoff re: gascity #3710/#3731 (both still OPEN, gh-verified) — content live but 16d silent, no parent; link to maintenance-pl queue or ping
- gpk-x7hc | needs-goal-link | 06-29 registry auto-publish PROPOSAL, zero notes, no parent, 14d untouched — needs a decision owner

### Miscalibrated priority

None. The 3 genuine P1 escalates (gpk-13ca, gpk-nvxk — active credential
exposure, tokens unrotated — and gpk-y9c07) all name concrete impact; the other
22 live P1s are the closed-root wisps handled above. No P0s in the rig.

### Blocked-pile (11 blocked)

No stale blocks-edges (none of the 11 has a formal blocks-edge to a closed
bead). Legit holds: gpk-13ca (awaiting Stephanie's bd-realign decision; its
32-PR triage snapshot is a week stale — refresh when unblocked);
gpk-t1ks9 + gpk-58e9t/8u7lc/b24ms/ei7av/g5wh5/yv3t1/6yfct (mis-staged molecule
cluster: targets a file that is 404 on origin/main — staged with
base_branch=main where the file can never exist; needs mayor re-stage, not a
merge wait); gpk-3uzz (pr-review/pr-pipeline folders removed from main);
gpk-czlvl (blocked on 2 unresolved dispatch-infra bugs).

gascity-packs subtotal: 142 proposals.

---

## Cross-store patterns (what to fix so this doesn't recur)

1. **Closed-root scaffolding leaks are the #1 noise source** (219 of 349
   proposals): molecule roots close but their step/convoy/finalize children
   stay open — packs 129, gascity 7 (gc-vkruy steps), EB f53i2 + 5 synthetic
   convoys, mem 7 convoys, city 3 convoys. Zero convoys have ever been closed
   in the city store (0/111 closed beads). A standing reaper rule —
   root closed → cascade-close open scaffolding children (gc.kind
   workflow/workflow-finalize/wisp, issue_type=convoy) — would have prevented
   ~2/3 of this audit.
2. **Priority inheritance by scaffolding inflates P0/P1 counts**: 27/43 of
   EB's open P0s and 22/24 of packs' P1s are blank-description machinery beads.
   The rubric already says formulas inherit root priority; scaffolding beads
   should either be excluded from priority statistics/dispatch nudges or
   created at fixed P3.
3. **Rollup/status-tick beads never get closed** (EB 59, packs several):
   point-in-time snapshots filed as open tasks. Rollups should be filed closed
   (they are log entries) or swept by age.
4. **Test runs must not write to live stores**: 27 timing/decoy fixtures from a
   06-29 benchmark run are still open in the gascity store.
5. **Close ≠ landed / close ≠ done remains the live RCA lesson**: mem-0rrf.3
   closed with outcome=fail yet satisfies its dependency edge; the EB
   re-filed-duplicate family exists because closed-but-unmerged fixes are
   invisible to dedup. Both feed the invariant work already tracked (dr-tyq
   backfill, dr-t1m finalize-gap, close-gate-reaper rule from RCA §4c).

---

## Count summary

| store | close-dup | close-stale | demote-to-PN | unblock-stale-edge | needs-goal-link | total |
|---|---|---|---|---|---|---|
| city (dr-\*) | 0 | 8 | 6 | 1 | 1 | 16 |
| enterprisebench | 3 | 59 | 35 | 1 | 0 | 98 |
| mem | 0 | 7 | 1 | 2 | 1 | 11 |
| gascity | 2 | 48 | 17 | 4 | 11 | 82 |
| gascity-packs | 2 | 138 | 0 | 0 | 2 | 142 |
| **total** | **7** | **260** | **59** | **8** | **15** | **349** |

Plus 3 non-counted flags: mem-0rrf.4 (repoint dep to mem-6bsd when hold lifts),
gc-nby7oo (upstream #2814 now closed — mayor re-check the hold),
EnterpriseBench-hpyx (real unfixed reconciler bug — root-fix-or-accept decision).

*Audit executed 2026-07-13. Analysts: 1 orchestrator + 4 parallel per-rig
review agents; all findings verified against store JSON dumps; gh
cross-checks read-only. No bead was mutated.*
