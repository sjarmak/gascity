# gascity-packs control-dispatch "112 backlog" — decomposition (2026-07-10)

Investigator: city-infra-pl. Ask: mayor gc-468509 — confirm/refute the
cross-rig scoping hypothesis, tie to gc-451304 (routed to me as gc-466218).
Read-only analysis over the packs rig store (`gc bd --rig gascity-packs list
--status all --json`, snapshot 2026-07-10 ~11:1x EDT).

## Headline

The "~112 packs control beads not draining" is **NOT 112 independent ready beads
invisible to the city dispatcher.** It decomposes into two populations, neither
of which is cleanly the "cross-rig scoping gap" as originally framed:

| bucket | count | what it is | owner of fix |
|---|---|---|---|
| root **CLOSED** | **60** | moot finalize-gap orphans — workflow member steps whose root already closed/merged; dispatcher correctly ignores them | finalize-gap (gc-fzvtu class) + packs-rig batch cleanup |
| root **OPEN/blocked** | **52** | control steps of **only 5 workflow molecules** (13 steps each), not 52 independent beads | dispatcher advance + scoping-vs-stall question (below) |

Total open packs beads at snapshot: 204 (112 routed to `core.control-dispatcher`,
77 `routed_to=none`, 15 to the packs polecat).

## The 60 moot orphans = the finalize-gap, not a scoping gap

These have `gc.root_bead_id` pointing at a **closed** root. The mayor's cited
example gpk-ng0d ("Run preflight checks", routed to core.control-dispatcher) is
in THIS bucket: its root gpk-g9eq is closed. A control-dispatcher that skips a
member of a closed root is behaving correctly — so gpk-ng0d is not evidence of a
scoping gap. This is the same signature as the mol-focus-review churn (gc-466529)
and the packs moot-root re-spawn (gc-467145): root closes, member steps linger
open + routed to the dispatcher. Durable cure = gc-fzvtu (RootOnly workflow-
finalize rewire, needs-source-impl).

## The 52 = 5 stuck molecules, not 52 ready beads

The 52 open-root members belong to exactly 5 roots:

- gpk-n0r5f (open, 3/28 closed) — mayor's "implement committed 2b7b51e+4d63abd,
  self-review done" root. The underlying WORK is done, but its control beads
  (implement, self-review, submit, workflow-finalize — all `routed=core.control-
  dispatcher`) are still OPEN. The molecule never advanced.
- gpk-2spof (open, 1/28 closed) — barely started: even load-context and
  workspace-setup control steps are still open. `.attempt.1` executable steps
  were dispatched to a polecat but never closed.
- gpk-d4mf6 (open, 7/28 closed) — partial progress, then stuck.
- gpk-t1ks9 (**blocked**, 0/28 closed) — fully blocked; its `.attempt.1` steps
  are `blocked`.
- gpk-g2uwe (open) — a mol-focus-review remnant (churn class), 1 workflow-
  finalize open routed to the dispatcher.

Uniform pattern: every `kind=retry|scope-check|workflow-finalize` control bead is
routed to `core.control-dispatcher` and OPEN; every `.attempt.N` executable bead
is routed to the packs polecat. The molecules are not advancing because their
control steps are not being closed/advanced by the dispatcher.

## Confirm/refute the scoping hypothesis

**Refuted as stated** ("112 independent ready beads the city dispatcher can't
see"). The real shape is 60 moot + 52-across-5-molecules.

**Undecided from bead-state alone**, and it reduces to a gascity-source question:
is the control-dispatcher *not seeing* the packs-rig control beads (structural
cross-rig work-query scoping gap — mayor's hypothesis), or *seeing but not
advancing* them (dispatcher starved/stalled)? Both produce identical bead-state.
Two facts pull opposite ways:
- FOR scoping gap: dispatcher gc-468444 is fresh+healthy yet even gpk-2spof's
  FIRST control step never advanced.
- FOR stall: the dispatcher loop is being blocked ~85-105s every ~60s by the
  cold `/status` path (dr-7smz8, still live post-#4122 — separate report), so
  "fresh" ≠ "unstarved."

## Recommendation (cheapest disambiguator first)

1. **Fix dr-7smz8 `/status` drag first**, then re-check whether the 5 molecules
   drain with an unstarved dispatcher. This is the cheapest way to separate stall
   from scoping gap — do it before filing any cross-rig scoping PR (which might be
   a red herring).
2. If a healthy **and** unstarved dispatcher still won't advance ready gpk-
   control beads → it's the structural scoping gap; needs the dispatcher work-
   query source (gascity engine) = external PR = Stephanie-gated. I can spec the
   source shape if wanted.
3. The 60 moot orphans are packs-RIG beads (not city-infra store) → cleanup is
   packs-pl's / mayor's, folded into the gc-fzvtu finalize-gate work (gc-467145).
4. Ties to gc-451304 ("packs review/dispatch lane dead", = gc-466218): same lane.
   gpk-n0r5f's completed-but-unfinalized branch (bd-gpk-2qi0c) is durable per
   mayor; no force-closes.

Out of city-infra in-place floor: packs-rig bead mutation, gascity dispatcher
source. This report is confirm/refute + decision surface only.
