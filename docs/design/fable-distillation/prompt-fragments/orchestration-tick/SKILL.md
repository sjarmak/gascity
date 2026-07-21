<!--
Integration point: referenced (not inlined) from agents/mayor/prompt.md and
  template-fragments/pl-periodic-directives.template.md wherever a coordinator
  produces a periodic tick over noisy multi-agent state. Role-agnostic: "the
  human" is whoever owns external/irreversible decisions for the installation.
Date: 2026-07-06
Status: draft pending dr-i4v.5 consumer eval
What this adds over the current mayor prompt (5 lines):
  1. A signal-hygiene pass with concrete detection rules (correction pairs,
     hold-vs-activity, completion-without-artifact) run BEFORE any prioritizing.
  2. A severity taxonomy and decision table for ordering actions, with a
     mandatory pairwise "outranks" defense per rank.
  3. The one-line-answer test for escalations, plus the pre-digest requirement.
  4. Deliberate non-action as a required output with mechanism-level reasons.
  5. Distrust-with-consequence linkage: every distrusted signal must visibly
     reshape an action, escalation, or non-action, or it is theater.
-->

# Orchestration Tick

One tick: read the current state of a multi-agent system (bead queues, rollups,
mail, session state), then emit four parts and nothing else:

1. A small ordered set of highest-priority **actions** (respect any requested
   count; never exceed it).
2. **Escalations** to the human, each a single decision she can answer in one
   line.
3. Deliberate **non-actions**: the loudest-looking items you are leaving alone,
   with the harm of acting stated.
4. A **distrust register**: which signals you discounted, the rule that caught
   each, and the consequence for parts 1 to 3.

The dominant quality of a good tick is selectivity under pressure: most of the
state is deliberately not acted on, and the output says why. Coverage without
commitment (re-narrating what is broken without ranking, declining, or
escalating it) is a failed tick; every item you mention must land in one of the
four parts.

## Step 1 — State intake, in this order

1. Direct human messages and unanswered mail (highest-trust signal; also the
   channel whose credibility every later escalation spends).
2. Correction and escalation records: anything marked CORRECTION, contradiction
   reports, claim-abandonment reports. Read these before the rollups they
   correct.
3. PL/worker rollups and status self-reports.
4. Queue state: ready/blocked/in-progress beads, assignees, timestamps.
5. Ground truth where cheaply reachable: git state, session liveness, port/info
   files. Frozen snapshots substitute cross-references within the snapshot.

Do not prioritize during intake. Intake produces raw claims; Step 2 decides
which claims are load-bearing.

## Step 2 — Signal hygiene pass (always before prioritization)

Run every detection rule. Each hit produces a distrust-register entry with
three fields: **signal class**, **falsifying evidence** (cite both halves of
the pair), **consequence** (which action gains a verify step, which escalation
gains a caveat, which emergency downgrades to stale).

Detection rules, all concrete checks:

- **Correction-pair scan.** For every completion/success/delivery claim, search
  the intake for a later record that corrects or contradicts it. A claim with a
  standing correction is falsified: route it to verification or escalate it
  with the contradiction attached. Never act on the original claim.
- **Hold-vs-activity cross-check.** For every hold, suspension, or emergency
  order, check the target subsystem's activity after the order's timestamp.
  Later activity means the order is stale; re-applying it would kill live work.
- **Completion-without-artifact.** A "pass", "done", "fixed", or "deployed"
  state with no accompanying artifact (diff, commit, merged-PR reference, log
  of the deployed binary actually running) is unverified. Repeated zero-diff
  passes from the same lane mean the lane's entire pass history since the first
  such closure is untrusted.
- **Self-report discount.** A component under investigation cannot certify its
  own outputs. Its "fixed / in flight / passed / delivered" claims carry zero
  evidential weight for this tick; only an independent artifact counts.
- **Rollup-vs-queue cross-check.** For each rollup claim that work is in flight
  or handled, find corroboration in a second source (queue state, git log,
  session list). Uncorroborated claims are marked unverified, and any action
  depending on one embeds a direct check as its first step.
- **Claim-state audit.** If assignee/status columns cannot distinguish claimed
  from stranded from abandoned (empty assignees, dead session paths, mailbox
  IDs), then "someone is on it" is not a reason to skip an item. Dispatch only
  against items explicitly reported stranded or unowned.
- **Staleness thresholds.** A record older than 24h describing the "current"
  state of a live agent is presumed stale; older than 7 days it is history, not
  state. Tighten or relax per installation, but pick a number and apply it
  uniformly.
- **Delivery claims.** "I mailed X" or "X was told" is weak evidence in either
  direction when the mail system itself has known delivery failures. Restate
  rather than assume prior delivery.
- **Aging-counter consistency.** A monotone, internally consistent series
  (7d, 8d, 9d, ...) earns trust in its direction; a contradictory series
  ("closed" in one record, "29 days old" in another) goes to verification, not
  escalation.

Two symmetric failures to hold off: trusting everything, and distrusting
everything uniformly (equally useless). Grade each claim; verified facts get
asserted plainly, inferences get marked as inferences, contradicted signals get
a verify step or an attached contradiction. Uncertainty must change behavior;
"may be stale" with an unchanged plan is decorative.

## Step 3 — Prioritize via the decision table

Severity taxonomy, highest first:

1. **Active corruption**: bad data/results being produced right now; every
   cycle of delay emits more damage.
2. **Fleet-wide stall**: a shared mechanism (dispatcher, queue, credential)
   wedging multiple rigs.
3. **Untrusted verification gate**: the mechanism that certifies work is
   itself broken, so downstream "pass" states are meaningless.
4. **Idle capacity**: a rig or pool fully blocked on one cheap unblock.
5. **External deadline**: ranked by recoverability, not label volume. Ask
   "what breaks this tick if it starts an hour later?" A non-recoverable
   window (access ends, baseline destroyed) makes the list; an alarming label
   does not.

Score each candidate on three columns and let the columns, not labels, drive
the order:

| Column                   | Question                                                                                                                                                 |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Unblocking value         | How much work does this one move free? A command that frees a whole rig or unwedges a shared dispatcher outranks a locally louder fix.                   |
| Blast radius of inaction | What accumulates while this waits: corrupt output, stalled fleet, burned deadline, or nothing?                                                           |
| Evidence quality         | Is the premise verified, corroborated, or a bare self-report? Actions on distrusted premises embed the verification as their first step or drop in rank. |

Output requirements per action:

- **Pairwise defense.** Each rank carries "why it outranks the next", compared
  against the adjacent item using the taxonomy (e.g. corruption beats stall).
  "This is critical" defends nothing.
- **Command-level intent.** Name the operation, target, and operative flags,
  using only identifiers and mechanisms present in the intake. A reader with
  the same tools could execute the tick without follow-ups.
- **Guards embedded, not appended.** When an action rests on a distrusted
  signal, the verify step is part of the action ("verify actual git state
  first, then push"), and it inherits any standing rule from the distrust
  register ("no action this tick treats a rollup completion claim as
  completion").
- **Grounded state only.** Every ID, command, policy, and authorization you
  cite must exist in the intake. Assumed future outcomes ("once the fix lands
  we can...") never drive present-tick actions.

## Step 4 — Escalation filter

An escalation is one crisp decision for the human. The test: **can she answer
it in one line?** Write the one-line answer you expect; if you cannot, it is
not ready to escalate: do your own triage first.

Each escalation must pass all four checks:

1. **Human-scoped.** Irreversible, external, spend-authorizing,
   credential-touching, or policy-setting. Anything you can safely verify or
   execute yourself is your job, not hers; escalating it delegates the
   coordinator's role upward.
2. **Single decision, enumerable options.** One question, options named inline
   ("rewrite in place, or re-cut an orphan branch?"). "And"-chained asks,
   "please look into X", "the situation with Y" are bundles, not decisions;
   split or digest them.
3. **Pre-digested.** Attach your own verification, contradiction report, or
   recommendation so she rules instead of investigating. Never forward an
   authorization request built on a claim your hygiene pass contradicted;
   forward the contradiction with it.
4. **Clean partition.** No item appears in both the action list and the
   escalation list.

The boundary in the other direction is absolute: a human-gated act (push to a
shared/external target, public post or reply, credential rotation, destructive
history rewrite, live spend) appears in the action list only with a specific
authorization cited from the intake. Readiness is not authorization; forty
branch-ready items do not add up to one approval. When in doubt, it goes in the
escalation list as a decision, and the escalation channel's credibility is a
budget: page the human only on items that survive the hygiene pass.

## Step 5 — Deliberate non-action

List the items that genuinely look urgent in the intake (alarm storms,
P0-labelled guards, mass backlog, aged emergencies) that you are leaving
alone. Test: would a naive reader of the intake have acted on it? If nothing in
your non-action list clears that bar, you have listed strawmen; go back.

Each entry states the mechanism-level harm of acting, in one of these shapes:

- **Pattern-matched noise.** Watchdog-generated, self-similar alerts recurring
  without recorded follow-on incidents are not evidence; paging the human on
  them burns the escalation channel.
- **Stale emergency.** The hygiene pass showed later activity contradicting the
  hold; re-applying it kills live work. Cite the cross-reference.
- **Documented failure loop.** Acting re-triggers a recorded loop (spurious
  spawns, re-dispatch storms). Cite the record.
- **Bulk state.** Floods of stale records route to the designed process
  (retention reaper, cleanup job) dispatched as ordinary work. Ad-hoc mass
  mutation mid-tick is destructive and can break state bindings; never do it.
- **Gated backlog.** Ready-but-external items surface through the designated
  approval channel (ledger, digest), not this tick's actions.
- **Contradicted premise.** The item's justification failed the hygiene pass;
  it is held pending the linked verification or escalation.

## Final self-check before emitting

- [ ] Action count is at or under the requested limit; no watchdog-storm items,
      future-event guards, or bulk cleanup in the action list.
- [ ] Every rank has a pairwise "outranks" defense; unblocking value visibly
      drove at least one ordering.
- [ ] Every escalation ends in one answerable question with options and carries
      your pre-digest; none delegates your own triage upward.
- [ ] No item sits in both actions and escalations.
- [ ] No gated act in the action list without a cited authorization from the
      intake.
- [ ] Every distrust entry names its falsifying evidence and its consequence in
      parts 1 to 3; no suspect signal is acted on unchanged elsewhere.
- [ ] Every identifier, command, and policy cited exists in the intake.
- [ ] Every item mentioned anywhere is ranked, declined, or escalated; zero
      narration-only content.

## Worked example (from the 2026-07-06 golden run)

**Distrusted signal and the rule that caught it.** Rollup `fsup` reported the
e1eq verification gate cleared, unblocking the pt0n/bvjx re-runs. The
correction-pair scan found `p7r9`, a later CORRECTION recording that the e1eq
topo_order parser fix was a no-op loop (the same failure class as another lane
that burned 12 cycles closing "pass" with zero diff). One claim, one standing
correction: falsified. Consequence: the re-runs, "ungated on paper", were held
rather than dispatched, and the completion-without-artifact rule extended the
distrust to every formula-closed "pass" in that rig since the first zero-diff
closure.

**Prioritization trade-off.** Two candidates for rank 1: three silent-misscore
scorer bugs live on main, versus a wedged control-dispatcher latching 23
workflows fleet-wide. The taxonomy decided it: "these are silent-misscore bugs
live on main. Every EB scoring run that happens while this waits emits corrupt
numbers, and numbers are this rig's entire product. Active data corruption
beats a throughput stall." The wedge ranked second, defended the same way
against rank 3: fleet-wide stall beats a broken verification gate whose bleed
was already contained by direct dispatch.

**Escalation as a one-line decision.** The contradicted gate fed the
escalation instead of an action: "Decision: authorize the pt0n/bvjx live-spend
re-runs now, or only after a verified-complete e1eq fix? I will not forward a
spend authorization built on a contradicted gate." Human-scoped (live spend),
one question, two options, pre-digested with the contradiction report attached.
She can answer it in one line; the same signal reshaped an action hold, a
non-action, and an escalation, which is what distrust-with-consequence means.
