# Recurring task end-to-end (order → formula → drain)

A worked example of a recurring task in this city, showing the four moving parts you need to stand up your own: the `gc order` definition, the dispatch path that wakes a worker, the formula the worker runs, and the drain pattern that lets the worker exit cleanly so the pool reconciler can spawn the next slot.

The example is **`epic-review-sweeper`** — every 10 minutes, scan the gascity epic-bead rules; for each rule where all child beads are closed and the epic itself is still open, dispatch an external-review agent to grade the aggregate diff.

## 1. The order — recurring trigger

`/home/ds/gas-city/orders/epic-review-sweeper.toml`

```toml
[order]
description = "Every 10m: dispatch epic-level external review when chunk members close. Migrated from systemd timer."
trigger = "cooldown"
interval = "10m"
exec = "/home/ds/gas-city/bin/epic-review-sweeper --apply"
timeout = "5m"
```

Trigger options:

- `trigger = "cooldown"` + `interval = "10m"` — fires whenever the previous run finished more than 10 minutes ago. Self-paces; will not pile up if a run takes longer than the interval.
- `trigger = "cron"` + `schedule = "0 11 * * *"` — wall-clock schedule. Use for once-a-day-style runs (the morning triage cycle uses this).
- `trigger = "event"` + `on = "bead.closed"` — fires on a supervisor event. Use for handlers that need to react to state changes (the `mail-redirect-to-mayor` order uses this).

`exec` is the shell command the controller runs. `timeout` bounds it.

Inspect / dispatch:

```bash
gc order list                            # all orders + trigger + cadence
gc order check                           # which are due to fire next tick
gc order show epic-review-sweeper        # full config + last fire
gc order history epic-review-sweeper     # recent fires
gc order run epic-review-sweeper         # ad-hoc fire through the controller
```

## 2. The exec wrapper — slings the formula

`exec`-based orders work around `gastownhall/gascity#1440` (native `formula = ... + pool = ...` order shape silently stops firing after first execution; fix in PR #1986). The wrapper hides the sling inside a script the controller treats as opaque.

`/home/ds/gas-city/bin/epic-review-sweeper` (the relevant call):

```bash
# scan all configured chunk rules; for each rule where all members are
# closed AND epic is still open: stamp dispatch metadata + sling
bd_update_meta "$scope" "$epic_id" last_review_dispatched_at "$now_ts"

# Proxy bead because `gc sling` rejects beads of type=epic
# ("first-class support is for convoys only"). The proxy carries the
# wisp; the epic is the real review target.
proxy_id=$(create_proxy_for "$epic_id")

gc sling "$reviewer" "$proxy_id" --on mol-epic-review
```

Audit JSONL at `.gc/epic-review-sweeper.log` records every dispatch / skip / failure event for later debugging.

`gc sling`:

- attaches the formula (`--on mol-epic-review`) as a wisp on the work bead,
- writes routing metadata (`gc.routed_to = "<agent-path>"`) on the bead,
- pokes the controller so the target agent's session wakes if it was idle.

The local wrapper `/home/ds/.local/bin/gc-sling` auto-injects `--nudge` and applies per-bead formula rules from `.gc/sling-intercept.yaml`. Prefer it over raw `gc sling` in scripts.

## 3. The formula — what the worker actually does

`/home/ds/gas-city/formulas/mol-epic-review.formula.toml`

```toml
description = """
Epic-boundary external review. The polecat reads the epic's
acceptance criteria + the aggregate diff across all child beads,
grades per-criterion, and either passes (close with evidence) or
rejects (create follow-up beads + re-queue).
"""

formula = "mol-epic-review"

[[steps]]
id = "load-epic-context"
title = "Load epic + all child bead work + aggregate diff"
description = """..."""

[[steps]]
id = "grade"
title = "Grade per-criterion against the aggregate diff"
description = """..."""

[[steps]]
id = "act"
title = "On PASS: close with evidence. On REJECT: follow-ups + re-queue."
description = """
if [ "$verdict" = "pass" ]; then
    bd update "$EPIC_ID" --notes "evidence.artifact_path: $REPORT_PATH
                                   evidence.reviewer_verdict: pass
                                   evidence.reviewer_agent: $GC_AGENT"
    bd close "$EPIC_ID"
else
    # create follow-up beads per failing criterion
    # increment review_pass_count, clear review_in_flight
fi
"""
```

A formula is an ordered list of steps the polecat works through. Each step has:

- `id` — referenced by `needs = [...]` on downstream steps for ordering
- `title` — surfaces in run state and audit logs
- `description` — the actual instructions the polecat reads + executes

The molecule root bead IS the control bead — `bd update <root-id> --notes "<key>: <value>"` records run state, and `bd mol current` lets a respawning polecat resume at the right step (crash-safe).

## 4. The drain — two patterns

The drain is how a worker signals "this work bead is done, reclaim my session for the next bead." Two shapes in our codebase:

### Pattern A — explicit `drain` step

Canonical in the `mol-do-work` family. The formula has a dedicated step whose only job is to ack the controller via the runtime API.

`/home/ds/gas-city/formulas/mol-do-work.toml`

```toml
[[steps]]
id = "drain"
title = "Signal completion"
needs = ["do-work"]
description = """
Work is done. Signal the controller to reclaim this session:

    gc runtime drain-ack

Run this command and nothing else.
"""
```

### Pattern B — implicit drain via `bd close`

Used by complex formulas with explicit decision branches at the end (`mol-epic-review`, `mol-pr-ship`, `mol-pr-triage`, `mol-adopt-pr`, `mol-pr-from-issue`). The final step calls `bd close <root-bead>` directly; the controller picks up the close via the `bead.closed` event. No separate `drain` step.

Both end the same way for the worker:

1. The work bead closes (explicit `bd close` OR via the drain-ack on the controller).
2. The polecat then runs `gc session close "$GC_SESSION_ID"` to actually terminate the session.
3. The pool reconciler observes `current < desired` and spawns a fresh polecat slot.

The `gc session close` step is **mandatory** per the polecat's one-bead-per-session contract. Typing `exit 0` at the prompt does NOT work — it exits the bash subprocess, but the Claude Code agent stays parked at the editor prompt and the reconciler reads the session as `active, last_activity=Nm ago` indefinitely. New beads queue against the wedged slot. See [bead-dispatch.md](./bead-dispatch.md) for the full claim/drain contract.

## End-to-end cycle for epic-review-sweeper

```
 controller cron tick (cooldown 10m elapsed)
   ↓
 ORDER: exec /home/ds/gas-city/bin/epic-review-sweeper --apply
   ↓
 EXEC SCRIPT: scan rules → find ready epic → create proxy bead
             → gc sling reviewer proxy --on mol-epic-review
   ↓
 supervisor wakes reviewer agent if idle; routes work via gc.routed_to metadata
   ↓
 FORMULA: mol-epic-review runs steps load-epic-context → grade → act
   ↓
 DRAIN (Pattern B): act step runs bd close <epic-bead>
   ↓
 wake-mayor-on-blocker-close + close-gate-reaper read bead.closed event
   ↓
 polecat runs gc session close "$GC_SESSION_ID" (one-bead-per-session)
   ↓
 reconciler: current=1, desired=2 → spawns fresh polecat slot for next bead
```

## Building your own recurring task

Minimum set of files:

1. **Order TOML** in `orders/<name>.toml` — `[order]` block with `description`, `trigger`, schedule / interval, and `exec`. Keep `description` operator-readable; it surfaces in `gc order list`.
2. **Exec wrapper** in `bin/<name>` — bash script that does the actual `gc sling <agent> <bead> --on <formula>`. Add a JSONL audit log under `.gc/<name>.log`.
3. **Formula TOML** in `formulas/<name>.formula.toml` — the steps the worker runs. End with `bd close <root-bead>` if the formula has decision branches, OR add an explicit `drain` step calling `gc runtime drain-ack` if the formula is a simple one-shot.
4. **Worker agent** — already exists if you're slinging to one of the polecat pools (`/home/ds/gascity/polecat` or `/home/ds/gascity-packs/gascity-packs-polecat`). New pools need an `agents/<name>/agent.toml` + `prompt.template.md`.

Verify:

```bash
gc order show <name>           # confirm trigger + exec match what you wrote
gc order run <name>            # ad-hoc fire
gc bd --rig <rig> list         # confirm the bead the script created
gc session list                # confirm the worker session woke + claimed
```

## When to use formula+pool vs exec

The native `[order]` shape supports `formula = "..." + pool = "..."` directly (no wrapper script needed). Currently regressed by gastownhall/gascity#1440; restore once #1986 lands + supervisor restarts. Until then, prefer `exec` wrappers and put the sling inside the script.

**Use `formula + pool`** (or its exec wrapper today) when the order needs an agent to reason / decide based on the work output — anything where an LLM judgment-call is the load-bearing step.

**Use `exec` only** (no sling, just a script) when the work is deterministic mechanism — file scans, sql probes, port checks, rsync — and no LLM judgment is needed. The `mol-dog-*` family in `gastownhall/gascity/examples/dolt/orders/` is the canonical reference: `mol-dog-stale-db` kept its formula because it has to decide apply/escalate/no-op based on probe output; the other four (`mol-dog-doctor`, `mol-dog-phantom-db`, `mol-dog-backup`, `mol-dog-compactor`) converted to exec because their work is purely deterministic.

## See also

- [bead-dispatch.md](./bead-dispatch.md) — `gc sling` semantics, the gc-sling wrapper, claim/drain contract
- [scanners.md](./scanners.md) — the full table of recurring scanners currently configured in this city
- `gastownhall/gascity/examples/dolt/orders/mol-dog-stale-db.toml` — canonical native `formula + pool` shape (non-mayor pool)
- `gastownhall/gascity/examples/gastown/packs/gastown/orders/digest-generate.toml` — second native example, same shape
