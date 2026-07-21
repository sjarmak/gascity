# Mayor tick — 2026-07-06 (from frozen bead snapshot)

Snapshot: `docs/evals/fable-baselines/inputs/mayor-scenario-beads-2026-07-06.txt`. Freshest anchors in the file: EnterpriseBench ticks 06:40Z → 10:00Z → 12:00Z → ~14:00Z → ~19:00Z → ~21:20Z (u2ti, 6xm7, vbj0, 37th, tbbr, bgr7), plus the 2026-07-05 RCA beads (gc-452959, gc-452965, gc-452972, gc-453005) and the ready-queue tail (dr-*). Everything below is derived only from this snapshot.

---

## 1. Five highest-priority actions, in order

**A1. Direct-dispatch EnterpriseBench-apfp (scorer trust boundary: 3 NEW on-main silent-misscore bugs), bypassing the broken formula.**
Command intent: `gc-sling enterprisebench-worker EnterpriseBench-apfp --no-formula` with the fix instructions inlined in the sling body (the `--no-formula` interim is the sanctioned citywide workaround per gc-416254; apfp was explicitly backed out of the broken mol-focus-review formula and handed to me for direct dispatch in EnterpriseBench-37th, and is still sitting in my court as of the ~21:20Z tick bgr7). Fire EnterpriseBench-pakh's direct-instruction dispatch in the same lane right behind it (its formula path burned 12 no-op cycles per gc-453005; it is already blocked from formula re-dispatch).
Why it outranks A2: these are silent-misscore bugs live on main. Every EB scoring run that happens while this waits emits corrupt numbers, and numbers are this rig's entire product. Active data corruption beats a throughput stall.

**A2. Clear the control-dispatcher wedge and reconcile the 23 latched mol-focus-review workflows (EnterpriseBench-mi5v + gc-74rxa).**
Command intent: restart the control-dispatcher session with `GC_DOLT_PORT` read from `.beads/dolt/.dolt/sql-server.info` (the known remediation for the recurring `bd --sandbox` port-0 wedge, per gc-454270 / gc-401310 / gc-74rxa), then sweep and re-drive or close the 23 stuck workflow-finalize steps (gc-455588), and `gc-sling` the already-ready gc-74rxa durable fix (gc-454411, polecat fix ready) so this stops recurring.
Why it outranks A3: the wedge is fleet-wide, not EB-only. gc-ie14l shows the same root cause stranding three more graph.v2 roots including a 14h-stalled security fix (#2723). But it is a stall, not corruption, so it ranks below apfp.

**A3. Dispatch the EB "no-evidence pass" RCA and dedupe its clones.**
Command intent: `gc-sling city-infra gc-453005 --no-formula` (execute-step closes `outcome=pass` with zero diff, zero notes, 12 consecutive cycles, survives account swap), then `bd close gc-453008 gc-453010` (and their input convoys) as duplicates of the same RCA so a worker doesn't triple-run it.
Why it outranks A4: until the step-close gate rejects a zero-diff pass, every EB `pass` outcome is untrustworthy, which quietly undermines A1 and A2 verification too. It ranks below A2 because the immediate bleed is already contained by the direct-dispatch mitigation.

**A4. Reconcile and push codeprobe under the pre-authorized research-rig code lane.**
Command intent: in the codeprobe rig repo, verify the branch-ready set against actual git state (`git log origin/main..main`, branch list) — the rollups claim local main DIVERGED from origin with 00e unpushed (codeprobe-hbuc) and 9jxx staged/held for mayor push (codeprobe-jfb0) — then push the code-only integration to origin/main. This lane is pre-authorized (Stephanie 2026-06-19: routine research-rig code pushes direct-to-main); data/results/comparison numbers stay held.
Why it outranks A5: an entire rig is idle and "100% gated on one operator merge" (codeprobe-zr06), and unblocking it costs nothing in approvals. It ranks below A1-A3 because an idle rig is cheaper than a corrupted or wedged one.

**A5. Dispatch the two TIME-SENSITIVE Fable beads from the ready queue.**
Command intent: `gc-sling` dr-i4v.3 (P0: distill cross-repo process skills from Fable articulation runs, "TIME-SENSITIVE: Fable authors") and dr-j0d.1 (P0: capture Fable golden baselines, "TIME-SENSITIVE: needs Fable access") to a free worker with `--no-formula`. Both are in the 20-ready/no-blocker list at the snapshot tail.
Why it is last: the deadline is external and non-recoverable (model-access window), which is why it makes the top five at all, but nothing breaks this tick if it starts an hour after A1-A4, whereas those are live bleeds.

---

## 2. Escalations to Stephanie (act = no; surface with one-line decision)

- **CSB PII in public git history (co-p8b / co-bbs), aged ~12 days, rig fully decision-gated.** Decision needed: _pick the public-branch recut strategy — in-place history rewrite, or fresh orphan-branch re-cut (invalidates existing clones/PRs)?_ Rewriting a public branch is an external, destructive act; volume of aging rollups does not authorize it.
- **CSB verifier reconciliation (co-41x / co-5fr) + canonical task-count (co-261).** Decision: _which verifier copy is canonical for scoring (canonical-source overwrite vs keep task-local variants vs re-score both ways)?_ The rig itself flagged this as a scoring-methodology call, Stephanie-scope, not a dedup.
- **EB paper-lock pair: tev + 1gvr (F2 leakage).** Both are mirrored to her desk (EnterpriseBench-w3c) and are the last gates on the mirror campaign besides hpyx (EnterpriseBench-kp0). Decision: _rule on tev and 1gvr so the campaign can close._
- **dec-xpq re-score spend, with a contradiction report.** f9vs/fsup say the e1eq gate cleared; p7r9 is a CORRECTION saying the e1eq topo_order parser fix was a no-op loop (same class as pakh). Decision: _authorize the pt0n/bvjx live-spend re-runs now, or only after a verified-complete e1eq fix?_ I will not forward a spend authorization built on a contradicted gate.
- **Slack token rotation (gpk-nvxk).** The slack-full token-leak CRIT fixes are merged (#162/#163); the two remaining actions are push the registry re-cut and _rotate the leaked workspace tokens_ — credential rotation is hers. Decision: _rotate now (yes/no)?_
- **gascity-dashboard PR #166 (contributor).** Review verdict request_changes (jgv8/69nd) and a take-the-good branch is ready (ahwg, gc-456132 assigned to me). Posting a review or merging is an external act. Decision: _post the request_changes + our fixup branch, merge as-is, or close?_
- **GEO Intervention-A "before" snapshot (GEO-x2k).** The marketing publish window may permanently destroy the baseline. Decision: _authorize the web-search snapshot capture before the publish window, or accept losing the before-measurement?_

---

## 3. Not acting on, despite apparent urgency

- **The ESCALATION storm: ~8 "JSONL spike [HIGH]" and ~10 "Reaper anomalies [MEDIUM]" beads.** They are watchdog-generated, self-similar, and recur across weeks with no follow-on incident recorded in the snapshot. Pattern-matched alarms are not evidence; paging Stephanie on them burns the escalation channel. They fold into the retention/triage problem below.
- **The bead-store flood itself (thousands of open slack/#, nudge:, session-path, and step-scaffold beads; ~40 duplicate "mem-pjh8.2 ADOPTING" broadcasts; ~60 zeldascension "quiet tick" rollups; dozens of May-era `human` report beads).** It looks like the most urgent thing in the file and it is the documented store-bloat problem (gascity-dashboard-essq, ~247K beads, no retention reaper). But mass-closing records mid-tick is destructive and can break channel-state bindings; the right move is dispatching the designed retention reaper as ordinary work, not ad-hoc deletion during an orchestration tick.
- **gc-nby7oo, the "P0: pin bundled dolt to 2.0.4 (2.0.8 bricks city on upgrade)".** Reads like a fire; it is a guard against a _future_ upgrade, is deliberately blocked+HELD, and touching it has already re-triggered 6 spurious polecat spawns (gc-453188). Acting on it re-opens the spawn loop before the trigger-path fix lands.
- **The OOM emergency beads (gc-404606 / gc-404789 / gc-404854, "SUSPENDED enterprisebench+codescalebench").** Stale: EB shows a full day of active ticks today, so the suspension was lifted. Re-applying holds on that signal would kill live work.
- **dec-7wp ("ship sw1w Convoy graceful-degrade or hold").** Still open and assigned to stephanie, but the dashboard rig records sw1w approved, shipped, and merged as PR #164 (gascity-dashboard-2803, -1nvjs). Re-asking her is noise; the correct act is closing the stale decision bead with the cross-reference, which I fold into normal hygiene, not the top five.
- **The large "branch-ready, awaiting your push" backlog aimed at upstream gastownhall repos (polecat/maintenance beads).** Every upstream push/PR/merge is per-action gated by the autonomy boundary; forty ready branches do not add up to one authorization. They surface via the morning ledger, not this tick.
- **EB re-runs pt0n / lyse.** Ungated on paper (fsup) but the gate is contradicted (p7r9); live spend on re-runs through a possibly no-op parser fix produces invalid numbers twice. Held pending the dec-xpq escalation above.

---

## 4. Signals I distrust, and how that shaped the plan

- **PL rollup self-reports, especially "X is in flight / handled".** The snapshot itself contains the falsifications: p7r9 CORRECTS fsup's "e1eq landed green"; 6nrh CORRECTS vbj0's cosmetic-residue claim (real close-without-merge bug); codeprobe-isug and -da75 both record work that a prior rollup claimed in-flight but was actually idle/dead; gc-209942 and gc-264614 are explicit CORRECTION beads. Consequence: A4 verifies git state directly before pushing, and no action in this tick treats a rollup claim of completion as completion.
- **EB `outcome=pass` closures.** gc-453005 documents 12 consecutive formula cycles closing pass with zero diff and zero notes, surviving an account swap, plus e1eq "falsely passed 4x". Any EB bead closed by formula since ~07-05 is unverified until a diff exists. That is why A1 uses direct dispatch and A3 exists at all.
- **The decisions rig (dec-\*) vs rig-level reality.** dec-7wp is contradicted by the merged PR; dec-f5g's substance (grounded-citation gate) was already resolved to "wire it" with pakh dispatched (oswm), so the live problem is pakh's execution loop, not a pending ruling. I only escalate decision beads that survive cross-referencing against resolution rollups; dec-xpq survives but goes up with its contradiction attached.
- **Assignee and status columns.** Nearly every row, including `in_progress` ones, shows assignee `—`; several assignees are dead session paths or literal mailbox IDs. Claim state in this snapshot cannot distinguish claimed / stranded / abandoned, which is exactly the hpyx claim-reset failure mode the EB rollups describe (6 recurrences, one confirmed work-abandonment). So no action assumes a bead is being worked because the store says so; dispatches in A1-A3 target beads explicitly reported stranded in my court.
- **Mail/delivery claims.** tev sat undelivered ~13.5-15.4h with "3 mayor mails unanswered" (2mk, oan), a city-wide resolver mismatch was root-caused once already (gc-351352), and the 40-copy "ADOPTING" broadcast shows fanout duplication. "I mailed the mayor / mayor was told" is weak evidence either direction; the escalation list above therefore restates each decision rather than assuming prior delivery.
- **"Fix deployed" claims.** gc-107em records that gcsync installs to GOPATH/bin while sidecars exec a stale `/home/ds/go/bin/gc`, and gc-198vq records a formula fix that "never deployed" while its tracking bead was closed prematurely. Consequence: A2 verifies the dispatcher actually restarts against the right Dolt port instead of trusting that the earlier fix already landed in the running lane.
- **Aging counters in rollups.** The CSB PII age series (7d → 8d → 9d → 10.6d → 12d) is internally consistent, so I trust the direction and use it to justify the escalation's urgency; the RENDER_API_KEY series is contradictory ("closed" in b2d vs "29 days old" in ngy), so it goes to verification, not escalation.
