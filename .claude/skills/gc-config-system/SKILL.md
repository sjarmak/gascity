---
name: gc-config-system
description: >
  Gas City config system runbook: city.toml composition (includes, packs,
  patches, rig overrides), config-presence activation (Levels 0-8), the
  Agent-field four-sibling rule, the schema/docs codegen ritual, config
  reload semantics, session-drain cascades from config drift, and the
  fsnotify watcher. Load when adding or changing a field in
  internal/config, editing city.toml / pack.toml / fragments, debugging
  "my config change didn't take effect", "config reload ... (keeping old
  config)", unexpected session drains after a config or binary change,
  hunting for a feature flag, or touching cmd/gc/controller.go config-watch
  or reload paths.
---

# gc-config-system

Runbook for working on and with Gas City's config system
(`internal/config/` plus the controller's watch/reload paths in
`cmd/gc/`). Tier 1 (single-session, no subagents, safe under
`gc sling` / `DISABLE_INTERACTIVITY=1`).

Reference architecture doc: **`engdocs/architecture/config.md`** — key
types, the 7-step load pipeline, invariants, code map. That doc owns the
what; this skill owns the how-to and the failure history. Read it once
before deep config work. Glossary nouns: `engdocs/architecture/glossary.md`.

## When NOT to use this skill

| You are working on                                            | Use instead               |
| ------------------------------------------------------------- | ------------------------- |
| Controller tick order, drain/wake/nudge races, orphan sweeps  | `gc-reconciler-lifecycle` |
| Provider seam internals, tmux/ACP transport, TUI driving      | `gc-runtime-providers`    |
| Make targets, test tiers, CI gates in general                 | `gc-build-verify`         |
| ZFC / Bitter Lesson / zero-roles doctrine calls               | `gc-doctrine`             |
| Diagnosing a running city end-to-end (gc trace/doctor/events) | `gc-debugging`            |
| First orientation to the codebase                             | `gc-orientation`          |

(Sibling skills are part of the same departure library under
`.claude/skills/`; some may still be landing as of 2026-07-06.)

## Jargon (defined once)

| Term                   | Meaning                                                                                                                                    |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| **city.toml**          | Root config file of a city (a directory containing `.gc/` runtime state).                                                                  |
| **Fragment**           | A TOML file pulled in via the root's `include = [...]`. Exactly one level deep — fragments cannot include fragments.                       |
| **Pack**               | Reusable agent-config directory (`pack.toml` + prompts/formulas/orders) stamped into the city at load time.                                |
| **Rig**                | A managed project directory declared via `[[rigs]]`.                                                                                       |
| **Patch**              | `[[patches.agent]]` / rig / provider block: modifies an _existing_ resource by identity key after fragment merge. Never creates resources. |
| **Override**           | `[[rigs.overrides]]` block: modifies a pack-stamped agent for one rig, applied during rig-pack expansion.                                  |
| **Provenance**         | Per-element record of which source file contributed each config value (`gc config explain`).                                               |
| **Revision**           | Deterministic SHA-256 of all config sources + pack contents; the controller reloads when it changes.                                       |
| **Config fingerprint** | Per-session hash of behavior-defining fields (`internal/runtime/fingerprint.go`); drift ⇒ the reconciler drains and restarts that session. |
| **Drain**              | Graceful stop of a session so the reconciler can restart it toward desired state.                                                          |

## Mental model in 60 seconds

1. **Config presence IS the feature-flag system.** There are no flags.
   Adding a section activates a capability (Levels 0-8). Table:
   `engdocs/architecture/nine-concepts.md` §Progressive Capability Model.
   If you are grepping for a feature flag, stop — look for the section.
2. **Everything merges into one flat `City` struct** via
   `config.LoadWithIncludes` (pipeline order in
   `engdocs/architecture/config.md` §Data Flow). Order matters: city
   packs expand _before_ patches (so patches can target city-pack
   agents); rig packs expand _after_ patches (so rig overrides apply to
   final stamped agents).
3. **The controller owns reload.** File watcher (or `gc reload`, or the
   tick) → recompute Revision → if changed, rebuild config → sessions
   whose fingerprint drifted get drained and restarted. A failed reload
   keeps the old config and logs `... (keeping old config)`.

## Layering: who wins

Lowest to highest precedence, with where each is written:

| Layer                     | TOML location                                                                                                      | Applied                                                                             |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------- |
| Built-in provider presets | Go (`internal/config/provider.go`, `BuiltinProviders`)                                                             | at `ResolveProvider` (agent startup)                                                |
| Pack defaults             | `pack.toml` in the pack dir                                                                                        | pack expansion                                                                      |
| City-level providers      | `[providers.X]` in city.toml (base-chain inheritance via `base = ...`, see `internal/config/chain.go` doc comment) | `ResolveProvider`                                                                   |
| Workspace defaults        | `[workspace]`                                                                                                      | load                                                                                |
| Fragment content          | files in `include = [...]`                                                                                         | fragment merge (arrays concatenate; workspace fields merge with collision warnings) |
| Patches                   | `[[patches.agent]]` (targets `dir` + `name`), `[[patches.rigs]]`, `[[patches.providers]]`                          | after fragment merge + city-pack expansion                                          |
| Rig overrides             | `[[rigs.overrides]]` (targets `agent = "name"`)                                                                    | rig-pack expansion                                                                  |
| Per-agent fields          | `[[agent]]`                                                                                                        | final word for that agent                                                           |

**Empty-list trap (unfixed, 2026-07-06):** a patch/override with
`depends_on = []` (or any empty list field: `pre_start`,
`session_setup`, ...) cannot _clear_ an inherited list — the apply
functions skip empty lists (`len > 0` guard). See the TODO in
`internal/config/patch.go` (search `TODO: depends_on`). Use a
non-empty replacement or change the source layer.

## Runbook A — adding a field to `config.Agent` (the four-sibling rule)

AGENTS.md "Adding agent config fields" is the constitution here. One
`Agent` field has **four siblings** that must change together or pool
agents / patches / overrides silently lose the value:

- [ ] 1. `Agent` struct — `internal/config/config.go`
- [ ] 2. `AgentPatch` — `internal/config/patch.go` + apply in `applyAgentPatchFields`
- [ ] 3. `AgentOverride` — `internal/config/config.go` + apply in `applyAgentOverride` (`internal/config/pack.go`)
- [ ] 4. Pool deep-copy — `deepCopyAgent` in `cmd/gc/pool.go`
- [ ] If the field is runtime-only (not user-overridable), add it to the
      `excluded` map in `TestAgentFieldSync` with a one-line reason instead.
- [ ] Regenerate schema/docs (Runbook B).
- [ ] Same pattern for `config.Rig`: field → `RigPatch` → `applyRigPatch` (AGENTS.md "Adding rig config fields").

AGENTS.md says the apply functions and pool deep-copy "must be checked
manually"; since that was written, coverage tests were added that catch
misses at every site (verified passing 2026-07-06). Run all of them:

```bash
go test ./internal/config -run 'TestAgentFieldSync|TestApplyAgentPatchCoversAllFields|TestApplyAgentOverrideCoversAllFields' -count=1
go test ./cmd/gc -run 'TestDeepCopyAgentCoversAllFields' -count=1
```

Still eyeball the apply functions — pointer fields for scalars ("not
set" vs "set to zero"), `append([]string(nil), ...)` copies for lists.

**Worked example (real miss):** commit `710bd3b54` (2026-04-15, "sync
new Agent fields across patch/override/pool") fixed exactly this: six
new Agent fields (Skills, MCP, ...) had landed on `Agent` only, so pool
agents silently lost skill/MCP attachment state. The fix touched all
four sites plus `TestAgentFieldSync` exclusions plus regenerated
`city-schema.json` / `config.md` / `cli.md` — the complete checklist
above, discovered in review instead of up front.

## Runbook B — the codegen ritual

Any change to config structs (or CLI flags) changes generated
artifacts. Generated files are a hard CI gate — hand-editing them or
forgetting regen fails the build.

```bash
make generate      # go run ./cmd/genschema
make check-schema  # regen + git diff --exit-code docs/reference/
```

Outputs (as of origin/main f828bbe4b, 2026-07-06):
`docs/reference/schema/city-schema.{json,txt}`,
`docs/reference/schema/pack-schema.{json,txt}`,
`docs/reference/config.md`, `docs/reference/cli.md`. The pre-commit
hook (`.githooks/pre-commit`) regenerates and stages these
automatically — but only if the Go toolchain is on PATH, so do not
rely on it from minimal environments. API-schema changes have their own
chain (genspec/genclient/dashboard) — see the `gc-generated-artifacts`
sibling skill and `engdocs/contributors/huma-usage.md`.

## Runbook C — "my config change didn't take effect"

Work down this list; each step is copy-pasteable from the city root.

```bash
gc config show --validate      # structural errors, exit non-zero on failure
gc config show                 # dump the fully-resolved merged TOML
gc config show --provenance    # which file contributed each element
gc config explain              # per-field source annotations (add --rig/--agent to filter)
gc config explain --provider X # provider base-chain attribution, --json for machine-readable
gc config show -f overlay.toml # test an overlay without editing city.toml
gc reload                      # force a reload tick now (default --timeout 5m)
```

Then check, in order:

1. **Typo?** Unknown TOML keys are _warnings_, not errors, with a
   did-you-mean suggestion (edit distance ≤ 2;
   `internal/config/undecoded.go`, `CheckUndecodedKeys`). Read the
   warnings — a typoed key is silently ignored otherwise.
2. **Wrong layer won?** `gc config explain` shows the winning source.
   Remember patch-vs-override apply order (Layering table above).
3. **Empty-list trap?** See Layering section.
4. **Reload failed?** Controller stderr shows
   `config reload: <err> (keeping old config)` — the city keeps running
   on the old config and the dirty flag is re-set, so it retries every
   tick. The reload path is `reloadConfigTraced` in
   `cmd/gc/city_runtime.go`.
5. **Revision unchanged?** If the recomputed Revision equals the current
   one, reload reports "No config changes detected." Revision hashes
   config _sources_ and pack dir contents — see
   `internal/config/revision.go`.
6. **Watcher blind spot?** See Watcher facts below; worst case the
   watcher failed at startup (`gc start: config watcher: ... (reload on
tick only)`) and changes only land on the next tick.
7. **Change is applied but the session predates it?** Only fields in the
   session's config fingerprint force a restart (next section). Others
   apply to _future_ sessions.

## Reload and drain hazards (failure archaeology)

A config reload is not just a re-parse — it can restart every session
in the city. Know the mechanism before editing reload code:

- `ConfigFingerprint` (`internal/runtime/fingerprint.go`) hashes the
  behavior-defining subset of a session's config: command, allow-listed
  env keys, pre_start, session_setup(+script), overlay dir, copied
  files, session_live. **Core** drift ⇒ drain + restart; **live-only**
  drift (`session_live`) ⇒ re-apply without restart.
- Env uses an **allow list**, not a deny list — new env vars do NOT
  cause restarts unless explicitly opted in (rationale in the
  `envFingerprintAllow` doc comment: a deny list caused spurious
  drift-restarts and token burn).

Three real incidents, all in the last month of history (verify with
`git show <sha>`):

| Commit              | Date       | Lesson                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| ------------------- | ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `be6e3c1cc` (#3229) | 2026-06-08 | A `gc` binary upgrade rewrote the managed `.gc/settings.json`; its content hash was in the core fingerprint, so **every live session drained** within one tick, killing unrelated work. Fix: managed files are path-only fingerprinted (`Probed:false`); user-authored hook files stay content-probed. Fingerprint changes require a `FingerprintVersion` bump (v4 as of 2026-07-06) so old stored hashes rebaseline silently instead of mass-draining. |
| `4fcec0b01` (#3555) | 2026-06-26 | Rig bead-store `init` during reload was killed at a 30s exec timeout when Dolt migration ran long; the reload silently kept the old config, retried every cycle, and **new rigs never came online**. Lesson: reload-path subprocess timeouts must use the long (120s) lifecycle class; a "keeping old config" loop in stderr is the symptom.                                                                                                            |
| `2ecdacd24` (#2944) | 2026-06-03 | An _attached_ session with persistent config drift can't be drained, so the reconciler defers — and the deferral re-stamp cadence was mis-tuned, producing a durable Dolt commit every ~30s (~92% of all write volume). Lesson: any per-tick write on the drift path needs a cadence bound. (Deep dive: `gc-reconciler-lifecycle`.)                                                                                                                     |

Before touching reload or fingerprint code, ask: "if this ships, what
happens to the 20 sessions already running?" That question would have
caught all three.

## Watcher facts

`watchConfigTargets` + `configWatchRegistrar` in `cmd/gc/controller.go`
(all verified on origin/main, 2026-07-06):

- **fsnotify is non-recursive.** Config source dirs are watched
  shallowly (handles vim/emacs rename-swap atomic saves); pack roots
  and convention roots are walked and watched recursively (regression
  guard: gastownhall/gascity#780). Target set computed by
  `config.WatchTargets` (`internal/config/revision.go`).
- Events are **debounced 200ms** (`debounceDelay`) then set a dirty
  flag and poke the controller; the actual reload happens on the loop,
  not in the watcher goroutine. A one-shot save is one reload.
- If the watcher can't start, the city degrades to **tick-only reload**
  (stderr: `gc start: config watcher: ... (reload on tick only)`) — not
  a crash, just latency.
- `syscall.ENOSPC` from inotify means the watch limit is exhausted;
  raise `fs.inotify.max_user_watches` or shrink the watched pack tree
  (the error message says exactly this).
- Don't "fix" non-recursion by watching everything — `.gc/` and
  `.beads/` are explicitly ignored (`shouldIgnoreConfigWatchEvent`) to
  avoid reload storms from runtime state.

## Trap checklist (before you push config-touching code)

- [ ] No role names or judgment calls in Go — config activates, models decide (AGENTS.md: ZFC, zero hardcoded roles).
- [ ] New capability activated by _section presence_, not a new flag field.
- [ ] Four-sibling tests pass (Runbook A) if `Agent`/`Rig` fields changed.
- [ ] `make generate && make check-schema` clean if any config struct changed.
- [ ] Reload path changes reasoned through the drain question (previous section) and, for fingerprint-affecting changes, `FingerprintVersion` bumped.
- [ ] Patches target existing resources; remember they cannot create, and empty lists cannot clear.
- [ ] `internal/config` is the #1 churn file in the repo (346 touches in 6 months per the 2026-07 discovery audit) — re-read the whole touched function, not just your hunk, and expect maintainer scrutiny.

## Provenance and maintenance

Authored 2026-07-06 against origin/main `f828bbe4b`; commands run and
tests executed in /home/ds/gascity (fork working copy). Provisional
inputs: skill placement (fork-local, written repo-portable) follows the
maintainer's **provisional** overnight answers of 2026-07-07, not a
confirmed upstream decision. Churn/incident framing draws on the
fable-distillation discovery report (machine-local, non-load-bearing);
every command, path, symbol, and commit cited here was verified
directly against the repo.

Re-verification one-liners for the facts most likely to drift:

```bash
# Four-sibling enforcement tests still exist and pass
go test ./internal/config -run 'TestAgentFieldSync|TestApplyAgentPatchCoversAllFields|TestApplyAgentOverrideCoversAllFields' -count=1
# Pool deep-copy still named deepCopyAgent and tested
grep -n 'func deepCopyAgent' cmd/gc/pool.go && go test ./cmd/gc -run TestDeepCopyAgentCoversAllFields -count=1
# Codegen outputs still land under docs/reference/
grep -n 'docs/reference' cmd/genschema/main.go
# Empty-list patch limitation still open
grep -n 'TODO: depends_on' internal/config/patch.go
# Fingerprint version and settings.json path-only fix still current
grep -n 'FingerprintVersion = ' internal/runtime/fingerprint.go
# Watcher debounce and tick-only degradation unchanged
grep -n 'debounceDelay = \|reload on tick only' cmd/gc/controller.go
# CLI surface unchanged
gc config --help && gc reload --help
```

If any line fails, fix this skill in the same change — a wrong runbook
is worse than none.
