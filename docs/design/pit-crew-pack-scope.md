# Scope: `pit-crew` pack contribution to gastownhall/gascity-packs

Generalized quality-of-life pack for city operators — shareable skills + a small
set of genuinely-decoupled orders. Drafted from ds-research's homegrown
orders/skills. Destination: gastownhall/gascity-packs (Stephanie's pipeline).

**Naming:** follows Julian's pack-name standard (#44): single plain lowercase
noun, drop redundant qualifiers/suffixes (`github-intake`→`github`,
`slack-pack`→`slack`). `pit-crew` chosen over `city-qol` (which violates it:
"city" redundant, "qol" an abbreviation); matches the meaningful-compound
precedent of `pr-pipeline` / `tmux-theme`.

## Guiding split — upstream the fixes, ship the conveniences

Triage of our homegrown orders shows two distinct classes. They take different
paths:

**Track A — upstream the FIX (do NOT bundle the band-aid into a pack).** These
exist only because of an upstream bug/gap; shipping the workaround as a pack
spreads the band-aid instead of curing it.

| Order | Root cause | Right path |
| --- | --- | --- |
| `stale-claim-reaper` | abandoned-claim recovery missing in gc | upstream gc-1b2 (already proposed) |
| `stale-worktree-reaper` | worktree side of same | upstream gc-1b2 |
| `routed-bead-nudger` | warm-pool no-nudge stall | upstream #1129 fix in `gc sling` |
| `dolt-gc-maintenance` | dolt pack sets `dolt_auto_gc_enabled=OFF`, promises scheduled GC that was never wired | contribute the scheduled `CALL DOLT_GC()` **into the existing dolt pack** |

**Track B — the actual QoL pack.** Real conveniences with no upstream
equivalent, decoupled from our setup:

| Component | Type | State | Generalization needed |
| --- | --- | --- | --- |
| `mechanic` | skill | built (ds-localized) | de-localize: strip our hard-rules, make the dolt section topology-aware (managed vs per-rig), drop our compass/path refs |
| `spawn-storm-detect` | order + script | working | pack-relative paths (no `/home/ds/...`), configurable threshold, pack-relative state file |
| `bead-janitor` | order + script | working | strip our cadence-tuning notes; expose `--min-age`, `--apply`, scope flags as config |
| compass pattern | skill template | pattern only | ship a `compass-template` SKILL.md showing the thin per-subsystem file-index pattern (not our actual compasses, which are ds-specific) |
| named-session watchdog | order | derived from `mayor-watchdog` | **must** parameterize the session name — gascity's "zero hardcoded roles" rule forbids a hardcoded `mayor` |

## Pack structure

```
pit-crew/
  pack.toml            # [pack] name, version (start 0.1.0 — version="" is a known defect), description
  README.md            # what it is, install, the Track-A "these are fixes not workarounds" note
  CHANGELOG.md
  skills/
    mechanic/SKILL.md
    compass-template/SKILL.md
  orders/
    spawn-storm-detect.toml + scripts/
    bead-janitor.toml + scripts/
    session-watchdog.toml      # parameterized
  formulas/            # (empty for v0.1 unless a clean candidate emerges)
```

## Hard constraints (gascity adoption-review expectations)

- **ZFC-clean** — orders/scripts are mechanism only (IO, structural checks,
  deterministic math); no semantic heuristics with hardcoded thresholds standing
  in for judgment.
- **Zero hardcoded roles** — no literal `mayor`/`polecat`/rig names; everything
  role/session-specific is a config value.
- **Topology-agnostic** — must not assume managed-city vs per-rig dolt; the
  `mechanic` skill's dolt guidance especially must branch on topology, never
  emit the destructive `gc dolt start/recover` path into a managed city.
- **Opt-in** — orders ship disabled / documented-as-opt-in; no surprise cadence.
- **Versioned** — non-empty `[pack] version`; pairs with the version-bump CI
  guard from gc-9ai6o.

## Process

1. Build in a gascity-packs worktree (not the contributor tree parked on bd-gpk-g5d).
2. Gate green (pack lint + any pack CI).
3. PR to gastownhall/gascity-packs; @julianknutsen for adoption review.
4. Track-A fixes are SEPARATE PRs against gastownhall/gascity (the gc repo), not
   part of this pack.

## Next action (daytime, deliberate dispatch)

Lease-before-sling a packs-polecat to build Track B v0.1; file the Track-A
upstream fixes as their own gascity beads. Do not start at 1 AM.
