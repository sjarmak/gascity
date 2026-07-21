# Bead dispatch (`gc sling`, `gc-sling`)

Failure modes covered: pool-claim hook silently skipping beads still assigned to a human, warm pool workers staying idle on dispatch, `--formula <name>` failing because that flag is a bool.

## Claim handoff (the silent-skip problem)

`gc sling` (fixed for embedded-dolt rigs in gascity#899, merged 2026-04-19) sets `gc.routed_to` metadata but does NOT clear a human `assignee` unless `--reassign` is passed. If you created or claimed a bead yourself (assignee = your user), the worker's pool-claim (`gc hook`) skips it silently.

One-step (preferred, gascity#1841, merged 2026-05-12):

```bash
gc sling <pool-target> <bead> --reassign    # clears assignee + routes in one shot
```

Manual two-step (equivalent):

```bash
bd update <bead> --unassign
gc sling <pool-target> <bead>
```

Legacy `sling-embedded` (`/home/ds/.local/bin/sling-embedded`) is obsolete now that `--reassign` exists; can be removed once nothing in the workspace still calls it.

## The wrapper: `gc-sling`

Upstream `gc` has no per-prefix/per-rig formula override (verified 2026-04-20 against origin/main); formula choice comes from the target agent's `default_sling_formula`. And `gc sling` does NOT auto-wake warm pool workers (gascity#1129).

`bin/gc-sling` (on PATH via `/home/ds/.local/bin/gc-sling`) handles both:

1. **Formula injection** — matches bead ID against rules in `.gc/sling-intercept.yaml` and auto-injects `--on <formula>` when a rule applies. Caller's explicit `--on` / `--formula` / `--no-formula` always wins.
2. **Default `--nudge`** — every dispatch auto-passes `--nudge` so warm pool workers wake up. Opt out via `--no-nudge` (wrapper-only flag, stripped before exec).

```bash
gc-sling <agent> <matching-bead>
# → gc-sling: bead <id> matches rule '<rule-name>' — injecting --on <formula>
# → exec gc sling <agent> <bead> --on <formula> --nudge

gc-sling <agent> <non-matching-bead>
# → exec gc sling <agent> <bead> --nudge

gc-sling <agent> <bead> --on <other-formula>
# → caller's --on wins; --nudge still injected

gc-sling <agent> <bead> --no-nudge
# → exec gc sling <agent> <bead>           # --no-nudge stripped, --nudge omitted
```

## Flag note: `--on` vs `--formula`

The attach-a-formula-to-this-bead flag is `--on <name>`, NOT `--formula <name>`. `--formula` is a BOOL flag meaning "treat the positional argument as a formula to instantiate." Using `--formula <name>` errors with `requires 1 or 2 arguments`.

## Why `--nudge` defaults on (2026-05-09)

`gc sling` alone leaves warm pool workers idle by design (gascity#1129). On 2026-05-09 the maintenance PL slung two beads (gc-v81c, gc-cpub) to `/home/ds/gascity/polecat`; they sat unclaimed for 13+ minutes — polecat-1 was warm-idle and never knew. Auto-injecting `--nudge` avoids the stall without an upstream change. `--no-nudge` is the explicit opt-out for cold-pool dispatch.

## Audit log

`.gc/sling-intercept.log` (JSONL). Every rule match writes a `formula_injected` event; every explicit-override writes a `passthrough` event with `reason`. Nudge injection is silent (fires every dispatch). `mayor-pattern-miner` reads this log.

Adding a new rule: append to `rules:` in `.gc/sling-intercept.yaml`. Pattern is a Python-regex matched against the bead ID.
