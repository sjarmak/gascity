# City Layout Audit

## Goal

Move city-authored content out of `.gc/` and into explicit top-level folders:

- `prompts/`
- `formulas/`
- `automations/`
- `packs/`
- `hooks/`
- `scripts/`

Keep `.gc/` for runtime state, caches, generated system assets, and sensitive
material only.

## Scope And Non-Goals

Scope:

- Re-architect the layout of a city checkout on disk.
- Preserve current override behavior for prompts, formulas, automations, and
  builtin packs.
- Make the migration safe for mixed-mode cities that temporarily contain both
  legacy `.gc/...` content and new visible roots.

Non-goals for this design:

- Defining a new city-level `overlays/` root. Current overlay behavior is pack-
  and agent-specific (`overlay/`, `overlay_dir`) and needs a separate design.
- Re-architecting pack-internal layout. This design changes city-root layout
  and the content-resolution model that consumes packs, but it does not require
  every pack to adopt new top-level roots in the same release.

## Current `.gc/` Footprint

### User-facing content currently stored in `.gc/`

| Current path | Current behavior | Proposed home | Notes |
| --- | --- | --- | --- |
| `.gc/prompts/*` | Seeded by `gc init`; re-materialized on `gc start` and overwritten | `prompts/*` | This is the clearest mismatch today: docs and CLI examples already point toward top-level `prompts/`, but startup still writes into `.gc/prompts/`. |
| `.gc/formulas/*.formula.toml` | Seeded by `gc init`; re-materialized on `gc start` and overwritten | `formulas/*.formula.toml` | This is the default city-local formula layer today. |
| `.gc/formulas/automations/*/automation.toml` | Supported implicitly because automation scanning looks under each formula layer root | `automations/*/automation.toml` | Requires scanner and layer model changes; today automations are coupled to formula roots. |
| `.gc/settings.json` | City-level Claude hook config; installed if missing and then staged into workdirs/pods | `hooks/claude.json` | Runtime destination inside session workdirs/pods stays `.gc/settings.json`. Only the city source path moves. |
| `.gc/scripts/*` | No core writer; staged into workdirs if present | `scripts/*` | This is effectively a hidden source directory today. |

### Generated system assets currently stored in `.gc/`

| Current path | Current behavior | Proposed home | Notes |
| --- | --- | --- | --- |
| `.gc/system/prompts/**` | Immutable builtin prompt copies if retained after migration | `.gc/system/prompts/**` | Binary-owned prompt templates stay hidden and are never user-editable. |
| `.gc/system-formulas/**` | Materialized from embedded FS on start/reload; overwritten and stale files removed | `.gc/system/formulas/**` | Binary-owned default formulas. Keep hidden. |
| `.gc/packs/bd/**` | Embedded builtin pack materialized on init/start | `.gc/system/packs/bd/**` | Must stay separate from user-authored top-level `packs/`. |
| `.gc/packs/dolt/**` | Embedded builtin pack materialized on init/start | `.gc/system/packs/dolt/**` | Same ownership rule as `bd`. |
| `.gc/packs/<name>/**` | Named remote pack cache | `.gc/cache/packs/<name>/**` | Today user and generated pack namespaces are mixed under `.gc/packs/`. |
| `.gc/packs/_inc/<cache>/**` | Remote include cache | `.gc/cache/includes/<cache>/**` | Keep hidden; avoid collision with user `packs/`. |
| `.gc/bin/gc-beads-bd` | Embedded helper script materialized on init/start | `.gc/system/bin/gc-beads-bd` | Internal helper; no need to surface. |

### Runtime, state, cache, and sensitive data that should remain in `.gc/`

| Current path | Purpose | Proposed home |
| --- | --- | --- |
| `.gc/controller.lock` | Single-controller flock | `.gc/runtime/controller.lock` |
| `.gc/controller.sock` | CLI/controller IPC | `.gc/runtime/controller.sock` |
| `.gc/controller.token` | Convergence ACL token | `.gc/runtime/controller.token` |
| `.gc/events.jsonl` | Event log | `.gc/runtime/events.jsonl` |
| `.gc/daemon.log` | Daemon log | `.gc/runtime/daemon.log` |
| `.gc/secrets/*.key` | Session resume tokens | `.gc/runtime/secrets/*.key` |
| `.gc/beads.json` | File-provider city bead store | `.gc/runtime/beads.json` |
| `.gc/artifacts/<bead>/iter-<n>/` | Convergence artifacts | `.gc/runtime/artifacts/...` |
| `.gc/worktrees/<rig>/<agent>/` | Rig worktrees | `.gc/runtime/worktrees/...` |

### Pack-owned runtime state currently dumped into top-level `.gc/`

These are not core runtime files, but example and builtin pack scripts
currently write them directly into the top-level `.gc/` namespace:

- `.gc/dolt-data`
- `.gc/dolt.log`
- `.gc/dolt.pid`
- `.gc/dolt.lock`
- `.gc/dolt-config.yaml`
- `.gc/dolt-state.json`
- `.gc/jsonl-archive`
- `.gc/jsonl-export-state.json`
- `.gc/spawn-storm-counts.json`

Recommended future convention:

- `.gc/runtime/packs/<pack-name>/...`

This move is gated on pack env-var migration. It should not happen in the same
change that introduces visible city content roots.

### Legacy or stale `.gc/` paths

| Current path | Status | Recommendation |
| --- | --- | --- |
| `.gc/agents/*` | No current production writer found; referenced only in tests/build-image exclusions | Stop documenting it; keep defensive exclusions until code audit confirms it is inert. |

## Existing Inconsistencies

### 1. Docs and CLI already lean toward visible folders

- `README.md` shows top-level `prompts/` and `packs/`.
- `gc agent add --prompt-template` examples already use `prompts/worker.md`.
- Pack examples use top-level `packs/...`.

### 2. Runtime still treats `.gc/prompts` and `.gc/formulas` as binary-owned

- Builtin prompts are overwritten on `gc start`.
- Builtin formulas are overwritten on `gc start`.
- `gc init --from` comments still describe `.gc/prompts/` as user-editable,
  but startup re-materialization contradicts that.

### 3. City discovery is keyed to `.gc/`, not `city.toml`

Many commands discover the city root by walking upward until they find `.gc/`.
That makes a checked-out city without runtime state effectively invisible, even
if `city.toml`, `prompts/`, `formulas/`, and `packs/` are present.

### 4. Staging and build-image code still source hidden paths directly

Today the implementation reads city-owned hook and script inputs from hidden
paths:

- hook staging uses `.gc/settings.json` as a source path
- session setup stages `.gc/scripts/`
- K8s initialization copies a city tree and relies on `gc init --from`
- build-image currently copies almost the entire city tree and excludes only a
  small subset of `.gc/` runtime files

The migration must therefore define explicit source-to-destination contracts,
not just rename directories.

## Recommended Target Layout

```text
city/
  city.toml
  pack.lock
  prompts/
  formulas/
  automations/
  packs/
  hooks/
  scripts/
  rigs/
  .gc/
    runtime/
    system/
    cache/
```

Ownership model:

- Top-level visible folders are user-authored or repo-authored city content and
  are intended to be checked into version control.
- `.gc/runtime/` is mutable runtime state and secrets and remains ignored.
- `.gc/system/` is generated by the gc binary and may be overwritten.
- `.gc/cache/` is fetched or derived material that can be recreated.

## Root Discovery Contract

`city.toml` becomes the canonical city marker.

Compatibility behavior:

- `findCity()` uses this ordered algorithm:
  1. Walk from the current directory upward to filesystem root, recording every
     ancestor.
  2. If any recorded ancestor contains `city.toml`, return the nearest such
     ancestor.
  3. Otherwise, if any recorded ancestor contains `.gc/`, return the nearest
     such ancestor for compatibility releases.
  4. Otherwise, return "not in a city".
- `resolveCity(--city PATH)` accepts either:
  - a directory containing `city.toml`
  - a legacy directory containing `.gc/` but no `city.toml`
- If `--city PATH` contains both `city.toml` and `.gc/`, `city.toml` wins and
  `.gc/` is treated as runtime state only.
- If discovery used legacy `.gc/` fallback, gc emits a deprecation warning and
  `gc doctor` reports the city as needing layout migration.
- New cities created by `gc init` or `gc init --from` always write `city.toml`
  and create `.gc/`.
- A freshly cloned repo with `city.toml` and visible roots but no `.gc/` is a
  valid city. Commands that only inspect config must work. Commands that need
  runtime state may create `.gc/runtime` lazily or report a fixable runtime
  bootstrap requirement, but they must not say "not a city".

Examples:

- child has `.gc/`, parent has `city.toml` -> parent wins
- child has `city.toml`, parent has `.gc/` -> child wins
- child has `.gc/`, parent has `.gc/`, no `city.toml` anywhere -> nearest
  `.gc/` wins for compatibility
- any ancestor with `city.toml` always outranks all legacy `.gc/` ancestors

Command classes:

| Command class | Examples | Behavior when `city.toml` exists but `.gc/` does not |
| --- | --- | --- |
| Config-only/read-only | `gc config`, `gc build-image`, `gc pack`, `gc migrate-layout --plan` | Must succeed without creating `.gc/` |
| Read-only diagnostics | `gc doctor` | Must resolve the city and report missing runtime scaffold as a fixable issue, not "not a city" |
| Runtime-mutating/bootstrap | `gc start`, `gc daemon start`, session creation/resume, automation execution | May create the minimal `.gc/{runtime,system,cache}` scaffold before continuing |

Classification rule:

- runtime-free: completes using `city.toml`, visible roots, includes, and
  `pack.lock` only
- runtime-required: reads or writes controller IPC, events, worktrees,
  artifacts, staged runtime files, or `.gc/runtime/**`

## Canonical Path Resolution And Mixed-Mode Rules

Path handling must be centralized in one resolver used by config loading,
prompt rendering, automation scanning, hook staging, doctor checks, and K8s
staging. Legacy `.gc/...` strings are compatibility inputs, not equal-
precedence peers.

### Canonical roots

| Logical asset | Canonical city root | Legacy fallback | Same-layer mixed-mode rule |
| --- | --- | --- | --- |
| Prompt templates | `prompts/` | `.gc/prompts/` | New visible path wins; legacy path is ignored with warning |
| City-local formulas | `formulas/` | `.gc/formulas/` | New visible path wins; legacy path is ignored with warning |
| City-local automations | `automations/` | `.gc/formulas/automations/` | New visible path wins; legacy path is ignored with warning |
| Claude hook source | `hooks/claude.json` | `.gc/settings.json` | New visible path wins; legacy path is ignored with warning |
| Session helper scripts | `scripts/` | `.gc/scripts/` | New visible path wins; legacy path is ignored with warning |

Rules:

- If only the canonical visible path exists, use it.
- If only the legacy path exists, use it and emit a deprecation warning.
- If both exist and resolve to the same logical asset, load only the canonical
  visible path, emit a warning, and report the shadowed legacy path in
  `gc doctor`.
- Do not load both copies. Mixed-mode cities must be deterministic.
- Resolver behavior must be identical in CLI, daemon, controller reload, K8s
  staging, and doctor checks. There is no command-specific precedence.

Config canonicalization:

- "same logical asset" means the same relative path within the canonical root
  pair for that content type:
  - `prompts/foo.md` == `.gc/prompts/foo.md`
  - `formulas/bar.formula.toml` == `.gc/formulas/bar.formula.toml`
  - `automations/baz/automation.toml` ==
    `.gc/formulas/automations/baz/automation.toml`
- Known legacy user-owned references such as `.gc/prompts/...`,
  `.gc/formulas/...`, `.gc/settings.json`, and `.gc/scripts/...` are
  canonicalized at load time to logical asset identities.
- Writes generated by `gc init`, `gc init --from`, config explainers, and any
  future migration tooling write new visible paths only.
- Relative paths inside packs and fragments that do not target these city-owned
  roots remain unchanged.

## Content Layer Model

Moving automations into `automations/` is a model change, not a path rename.
The scanner must stop inferring automation precedence from formula directory
shape and instead consume explicit logical layers.

Proposed abstraction:

```text
ContentLayer {
  Rank            // system < city pack < city local < rig pack < rig local
  Owner           // system | city-pack | city-local | rig-pack | rig-local
  FormulaRoots[]  // ordered low->high within the layer
  AutomationRoots[] // ordered low->high within the layer
}
```

Rules:

- Formula precedence remains exactly:
  `system < city pack < city local < rig pack < rig local`
- Automation precedence uses the same five-rank lattice.
- A city-root `automations/` directory participates at `city local` rank only.
  It does not outrank rig-local content.
- Automation identity is the automation directory name within the effective
  layer lattice.
- Higher-rank layers override lower-rank layers by automation name, matching
  the current implicit scanner behavior.
- Within the same rank, existing root ordering remains authoritative:
  - city-pack and rig-pack roots follow the current pack expansion/topological
    order
  - later roots shadow earlier roots
  - automation resolution must mirror formula resolution for same-rank ties

### Compatibility within a layer

Each layer can expose multiple automation roots during migration, ordered
low -> high precedence:

- city-local layer:
  - `.gc/formulas/automations/`
  - `automations/`
- pack or rig layers:
  - current pack/rig legacy automation roots derived from formula roots remain
    valid during this design's compatibility window
  - canonical pack/rig automation roots are out of scope for Release N/N+1 and
    must not be introduced until a follow-up design defines their precedence

Within a single layer:

- new root wins over legacy root for the same automation name
- legacy roots still contribute names absent from the canonical root
- the loser is not loaded
- a warning is emitted once per shadowed automation name

This preserves deterministic behavior and avoids double execution.

Resolver iteration rule:

- `AutomationRoots[]` are iterated low -> high, matching current formula-layer
  scanning, and later roots win on name collision.
- For pack-provided layers, root ordering is the existing pack expansion order
  produced by `EffectiveCityPacks` / `EffectiveRigPacks`:
  - declaration order is preserved among sibling packs
  - included packs are expanded before the including pack
  - the resulting topological order is the authoritative low -> high order
- Automation ordering is required to use the exact same per-rank root order as
  formula ordering.
- If a pack- or rig-level canonical automation root appears before the follow-up
  design lands, doctor must flag it as unsupported instead of guessing.

## Override And Compatibility Analysis

### Prompt templates

Current state:

- Default cities point to `.gc/prompts/mayor.md`.
- Agent patches, rig overrides, and inline agent config can override
  `prompt_template`.
- Pack prompts are already relative to pack roots and do not depend on
  `.gc/prompts/`.

Breakage risk:

- Any existing `city.toml`, fragment, patch, or rig override that references
  `.gc/prompts/...` breaks if the files simply move.
- User edits to `.gc/prompts/*` are already unsafe because startup overwrites
  them.

Required compatibility:

- Accept both `.gc/prompts/...` and `prompts/...` during migration.
- Seed builtin prompt files into `prompts/` for new cities.
- Stop overwriting user-owned `prompts/` on every start.
- Keep immutable binary-owned prompt copies, if still needed, under
  `.gc/system/prompts/`.
- In mixed mode, `prompts/...` wins over `.gc/prompts/...`.

### Formulas and automations

Current state:

- Default city formula directory is `.gc/formulas`.
- Formula precedence is deterministic:
  `system < city pack < city local < rig pack < rig local`
- Automation discovery is coupled to formula roots because the scanner looks
  for `<layer>/automations/*/automation.toml`.

Breakage risk:

- Moving local formulas from `.gc/formulas` to `formulas/` breaks default path
  resolution, tests, docs, and examples unless a compatibility alias is
  introduced.
- Splitting automations into a top-level `automations/` folder is a real model
  change, not just a path rename.

Required compatibility:

- Preserve formula precedence exactly.
- Make automation precedence explicitly equal to formula precedence.
- Accept legacy `.gc/formulas` and new `formulas/` during migration.
- Accept legacy `.gc/formulas/automations/...` and new top-level
  `automations/...` during migration.
- In mixed mode for the same layer, `automations/...` wins over the legacy
  subdirectory.

### Builtin prompts, formulas, and packs

Required ownership boundary:

- Builtin prompts and formulas stop living in user-owned roots.
- Binary-owned defaults move under `.gc/system/`.
- User-visible roots shadow binary-owned system assets by logical relative
  path.

Override contract:

- builtin/system prompts: `.gc/system/prompts/...`
  - overridden by `prompts/...`
- builtin/system formulas: `.gc/system/formulas/...`
  - overridden by city-pack, city-local, rig-pack, and rig-local formula layers
    exactly as today
- builtin packs `bd` and `dolt`: `.gc/system/packs/...`
  - user-authored or fetched packs with the same logical builtin name continue
    to suppress builtin injection

Explicit rule:

- Preserve the existing builtin `bd` override rule.
- Add the same explicit override rule for builtin `dolt`.
- Keep builtin packs together under the same hidden parent so relative includes
  such as `../dolt` remain stable.

Builtin pack family contract:

- `bd` and `dolt` are treated as one builtin family for override purposes.
- Ambient discovery roots are:
  - visible city/user pack roots (`packs/...`)
  - explicit pack refs resolved from city/rig includes
- Non-roots:
  - `.gc/system/packs/...`
  - `.gc/cache/packs/...`
  - `.gc/cache/includes/...`
  These are storage locations, not peer discovery namespaces.
- Injection algorithm in `cmd/gc/embed_builtin_packs.go`:
  1. materialize builtin family under `.gc/system/packs/{bd,dolt}`
  2. build the logical pack set from visible roots and explicitly resolved
     includes only
  3. if that logical set already provides `bd` or `dolt` from a non-system
     root, skip injection of the entire builtin family
- Mixed system/user builtin families are unsupported. If the user overrides one
  of `bd` or `dolt`, they must override the whole family or doctor fails the
  city. This avoids `../dolt` resolving partly into stale system content.
- `pack.lock` remains stable across cache relocation because it stores
  source/ref/commit/hash, not cache paths. Cache moves do not rewrite
  `pack.lock`.
- Relative includes remain rooted at the including pack's resolved root,
  regardless of whether that root lives under `packs/`, `.gc/system/packs/`, or
  `.gc/cache/...`.

### Hook config

Current state:

- `.gc/settings.json` is created once and not overwritten if already present.
- Runtime staging expects the destination inside workdirs/pods to be
  `.gc/settings.json`.

Breakage risk:

- Simply moving the source path breaks startup command construction, session
  copy-files, and K8s staging.

Required compatibility:

- Move the city source file to `hooks/claude.json`.
- Continue staging it into agent workdirs and pods as `.gc/settings.json`.
- Preserve existing non-overwrite behavior for user-owned hook source.
- In mixed mode, `hooks/claude.json` wins over `.gc/settings.json`.

### Scripts

Current state:

- Session setup currently stages `.gc/scripts/` into runtime workdirs.

Required compatibility:

- Move the city source root to `scripts/`.
- Continue staging runtime scripts into `.gc/scripts/` inside workdirs/pods
  during the compatibility window.
- In mixed mode, `scripts/` wins over `.gc/scripts/`.

### Pack-generated runtime files

Current state:

- Pack scripts hard-code `.gc/` paths for worktrees, dolt state, jsonl export
  state, and other runtime files.

Breakage risk:

- Any internal `.gc/` reorganization breaks pack scripts immediately.

Required compatibility:

- Introduce stable env vars before moving pack runtime files:
  - `GC_CITY_ROOT`
  - `GC_CITY_RUNTIME_DIR`
  - `GC_PACK_STATE_DIR`
- Inject them identically in local and K8s runtimes.
- Do not relocate pack runtime files until builtin packs and shipped examples
  consume env-based lookup.
- `GC_PACK_STATE_DIR` is the absolute per-pack runtime directory:
  `.gc/runtime/packs/<pack-name>/`
- It is injected into every execution surface that can run pack logic:
  controller-managed processes, agents, staged helper scripts, hooks, formula
  execs, and their subprocess trees.

## Staging, Runtime, And Build-Image Contract

This design needs an explicit source-to-destination contract for each runtime
surface.

| Content | Canonical city source | Local runtime/workdir destination | K8s/pod destination | Build-image context |
| --- | --- | --- | --- | --- |
| Prompt templates | `prompts/...` | Not copied into workdir; resolved from city root | Included in city source baked/copied into pod city root | Include verbatim |
| Formulas | `formulas/...` | Not copied into workdir; consumed by controller/runtime from city root | Included in city source baked/copied into pod city root | Include verbatim |
| City-local automations | `automations/...` | Not copied into workdir; consumed by controller/runtime from city root | Included in city source baked/copied into pod city root | Include verbatim |
| User packs | `packs/...` | Not copied into workdir directly | Included in city source baked/copied into pod city root | Include verbatim |
| Claude hooks | `hooks/claude.json` | Stage as `.gc/settings.json` | Stage as `.gc/settings.json` | Include `hooks/claude.json`, not staged runtime copy |
| Helper scripts | `scripts/...` | Stage as `.gc/scripts/...` | Stage as `.gc/scripts/...` | Include `scripts/...` |

Build-image contract:

- Switch from an exclusion-based copy of the whole city tree to an allowlist.
- Base allowlist:
  - `city.toml`
  - `pack.lock`
  - visible content roots: `prompts/`, `formulas/`, `automations/`, `packs/`,
    `hooks/`, `scripts/`
- Resolver-backed closure allowlist:
  - every checked-in config file reachable from `city.toml` via known resolver
    edges:
    - `workspace.include`
    - rig config references
    - checked-in patch/override/template references
  - every repo-authored runtime input referenced by those config files through
    known city-owned path fields:
    - `prompt_template`
    - `session_setup_script`
    - `[formulas].dir`
    - hook/script source fields introduced by this migration
  - rig-authored runtime inputs needed by image build or pod bootstrap,
    regardless of whether they live under `rigs/**/*.toml` or sibling files
- Closure confinement rules:
  - follow only relative paths or `//`-rooted city-relative paths
  - resolved paths must stay within the city root
  - resolved paths under `.gc/` are never included
  - symlinks must not escape the city root
  - unknown reference types or absolute-path escapes cause `gc build-image` to
    fail rather than widening the copy set
- Exclude all of `.gc/**` from build context by default.
- If a future build needs `.gc/system/**`, that should be a narrow explicit
  inclusion, not a broad exception.
- `hooks/` and `packs/` are included only under a secret-free content policy:
  - `hooks/` must not contain credentials, API tokens, or environment-specific
    secrets
  - `packs/` must not embed secrets; pack secrets must come from runtime env or
    secret mounts
- Secret-policy contract:
  - enforced by a checked-in policy file consumed by both `gc doctor` and
    `gc build-image`
  - initial hard-fail classes include:
    - PEM/private key blocks
    - known token/API-key prefixes (for example `ghp_`, `xoxb-`, `AKIA`,
      `sk-`)
    - inline `token`, `secret`, `password`, or `authorization` values above a
      small literal threshold
    - high-entropy base64/hex blobs unless explicitly allowlisted
  - encrypted blobs are not exempt; the policy for `hooks/` and `packs/` is no
    secret material at rest in those roots
- `gc build-image` hard-fails on secret-policy violations in `hooks/` or
  `packs/`; this is not advisory-only doctor output.
- `rigs/**/.beads/**` is excluded from build context.
- `.gc/agents/**` remains excluded in legacy codepaths until the code audit is
  complete; the audit must finish before the allowlist switch ships.
- The allowlist switch ships only with a checked-in inventory/test manifest that
  proves every repo-authored runtime input class is covered.
- Inventory/test manifest contract:
  - checked into the gc repo alongside build-image tests
  - schema includes at minimum:
    - `class`: logical runtime-input class name
    - `source_examples[]`: representative repo paths
    - `resolver_edges[]`: why the class is reachable
    - `include_rule`: base root or closure rule that admits it
    - `exclude_rule`: proof it does not come from `.gc/**`
    - `required`: whether omission is a test failure
  - allowlist conversion does not ship until tests prove all declared classes
    are present and no broad-copy fallback path remains
- Release gate:
  - the allowlist switch is blocked until:
    - the `.gc/agents/**` audit closes with an explicit finding
    - all build-image/K8s/init codepaths stop broad tree copy in favor of the
      allowlist contract above

K8s contract:

- The same canonical source roots used locally must be the ones copied or baked
  into pod city roots.
- Hook and script staging semantics must match local runtime exactly:
  - `hooks/claude.json` -> `.gc/settings.json`
  - `scripts/` -> `.gc/scripts/`
- No path may exist only in one runtime flavor.
- Pod bootstrap must not depend on `.gc/system/**` being pre-baked into the
  image. System assets are expected to be materialized by gc at startup from
  embedded assets.
- Supported K8s bootstrap modes are "copied" or "baked", but both must produce
  the same final pod city root and the same resolver inputs before runtime
  start.
- Staged runtime destinations such as `.gc/settings.json` and `.gc/scripts/`
  are write-only compatibility outputs. They are never resolver source paths.
- Pods must provide a writable `.gc/` area so startup can create
  `.gc/{runtime,system,cache}` on first boot.

Workdir contract:

- Agents and scripts must not rely on `../../` traversal from worktrees to
  discover city content.
- `GC_CITY_ROOT` is the absolute path to the canonical city content root
  visible to the current runtime:
  - local: the city checkout root
  - K8s: the pod city root after copy/bake
- `GC_CITY_RUNTIME_DIR` is the absolute path to the mutable `.gc/runtime`
  subtree for that city.
- `GC_CITY_ROOT`, `GC_CITY_RUNTIME_DIR`, and `GC_PACK_STATE_DIR` must be
  injected into controller processes, agent processes, hooks, staged scripts,
  pack scripts, and their subprocess trees.

## Migration Tooling Contract

`gc init --from` source mapping:

- only legacy content present -> copy to canonical visible destination
- only canonical content present -> copy canonical destination as-is
- both present and byte-identical -> copy canonical destination only, record
  the dedup in the migration report
- both present and divergent -> stop with a conflict report; do not overwrite
  either side and do not silently pick one
- runtime `.gc/**` state is never copied
- destination output is always canonical layout; `gc init --from` never writes
  both canonical and legacy copies into the new city

`gc migrate-layout --apply` rewrite scope:

- files rewritten:
  - `city.toml`
  - config fragments loaded through `workspace.include`
  - rig-local config files loaded as part of the city
  - checked-in templates, patch files, and override files in the city repo that
    contain recognized city-owned path fields
- fields rewritten when they target legacy city-owned roots:
  - `prompt_template`
  - `session_setup_script` when it points under `.gc/scripts/...`
  - `[formulas].dir`
  - rig or patch overrides of `prompt_template`
  - rig or patch overrides of `session_setup_script`
  - any known hook/script source fields added by this migration
- files not rewritten:
  - remote pack contents
  - fetched cache contents
  - pack-internal relative paths that do not target city-owned roots

`gc migrate-layout --apply` execution semantics:

- create a journal under `.gc/cache/migrations/layout/<timestamp>/`
- acquire the controller lock before mutating city content
- for each asset:
  1. write canonical target to a temp file in the destination filesystem
  2. `fsync` and `os.Rename` into place
  3. verify contents
  4. record journal entry
  5. rewrite referencing config via temp-write + `os.Rename`
  6. archive or prune legacy source only after the canonical target is valid
- for each config rewrite:
  - write temp file
  - `fsync`
  - `os.Rename`
  - record in the journal before touching the next file
- if the command sees an existing unfinished journal, it repairs or replays
  that journal before starting any new moves
- `gc migrate-layout --plan --json` provides a machine-readable version of the
  planned moves, rewrites, conflicts, and blockers
- successful apply writes `layout_version = 2` to `city.toml`
- when `layout_version = 2` is present, the resolver stops probing legacy
  city-owned content roots by default; doctor can still inspect them explicitly
- if interrupted, the next run resumes from the journal
- canonical-path-wins resolution means an interrupted migration remains safe:
  once a canonical asset exists, it is the active copy; otherwise the legacy
  copy stays active
- conflicts are left in place, recorded in the manifest, and require operator
  resolution
- rollback bundle and manifest remain until legacy read support is removed

Operator observability:

- legacy-path warnings are emitted once per logical path per command invocation
  and once per reload cycle in long-running controller processes
- `gc doctor --json` must provide machine-readable output for CI/fleet checks
- `gc doctor` must cover:
  - discovery via legacy `.gc/` fallback
  - missing `city.toml`
  - legacy config references even when visible roots exist
  - shadowed mixed-mode duplicates for prompts, formulas, automations, hooks,
    and scripts
  - tracked `.gc/**` content in git
  - secret-pattern violations in `hooks/` and `packs/`
  - packs still using hard-coded `.gc/<name>` runtime paths instead of
    `GC_PACK_STATE_DIR`
  - unsupported mixed builtin-family overrides
  - runtime/build-image/K8s paths still sourcing legacy roots

Repo hygiene:

- new cities ignore `.gc/` entirely in version control
- `gc init` writes the ignore contract
- `gc doctor` fails when tracked `.gc/runtime/**`, `.gc/cache/**`,
  `.gc/system/**`, or legacy `.gc/*` runtime content is committed
- visible roots intended for image inclusion (`hooks/`, `packs/`, `scripts/`)
  are treated as commit-safe only under the secret-free policy above

## Migration And Release Policy

This migration needs a noisy deprecation path, not indefinite dual-read
support.

Release N:

- Add visible roots and centralized path resolver.
- Discovery becomes `city.toml` first, `.gc/` fallback second.
- All legacy reads emit warnings.
- `gc init --from` maps legacy city-owned source paths into visible target
  roots:
  - `.gc/prompts/` -> `prompts/`
  - `.gc/formulas/` -> `formulas/`
  - `.gc/settings.json` -> `hooks/claude.json`
  - `.gc/scripts/` -> `scripts/`
  - runtime `.gc/**` state is not copied
- `gc doctor` gains checks for:
  - legacy `.gc/prompts/`
  - legacy `.gc/formulas/`
  - legacy `.gc/formulas/automations/`
  - legacy `.gc/settings.json`
  - legacy `.gc/scripts/`
  - mixed-mode duplicates where both new and legacy copies exist
- Add `gc migrate-layout --plan` to report the exact moves and config rewrites
  required.

Release N+1:

- `gc init` and `gc init --from` write visible roots only.
- Startup stops materializing builtin prompts/formulas into user-owned paths.
- `gc migrate-layout --apply` becomes supported for city-local content.
- `gc migrate-layout --apply` must be idempotent and restart-safe:
  - if the canonical target is absent, move the legacy source
  - if target and source are byte-identical, keep the target and remove or
    archive the legacy source
  - if target and source differ, leave both in place, report a conflict, and
    rely on canonical-path-wins semantics until the operator resolves it
- Documentation and examples flip to visible roots only.
- Legacy reads remain fully active in N+1 for unmigrated cities.
- `gc start` in N+1 warns loudly when a city still depends on legacy roots and
  recommends `gc migrate-layout --plan`.
- Mixed-version compatibility target:
  - Release N controller/CLI must still understand legacy `.gc/...` layouts
  - running Release N binaries against a city after `gc migrate-layout --apply`
    is unsupported
  - `gc migrate-layout --apply` may only be run after all controllers, CLIs,
    pods, and build-image pipelines for that city are on Release N+1; doctor
    must flag mixed-version fleets as a blocker for apply
- rollback policy:
  - `gc migrate-layout --apply` writes a rollback bundle and manifest under
    `.gc/cache/migrations/layout/<timestamp>/`
  - `gc migrate-layout --rollback <timestamp>` is supported through Release N+2
    to restore the archived legacy layout and config rewrites if an N+1 rollout
    must be backed out
- Protocol note:
  - this migration does not change `controller.sock` protocol semantics; the
    compatibility surface is filesystem layout and resolver behavior, not IPC

Release N+2 earliest:

- Remove reads from legacy `.gc/prompts/`, `.gc/formulas/`, `.gc/settings.json`,
  and `.gc/scripts/` only if Release N and N+1 both shipped warnings and doctor
  coverage.
- Legacy automation discovery from `.gc/formulas/automations/` should be
  removed no earlier than the same release, and only after the `ContentLayer`
  scanner is already stable.

## Major Code Hotspots

These are the main places the migration has to touch:

- City discovery: `cmd/gc/main.go`
- Init and template copy behavior: `cmd/gc/cmd_init.go`
- Builtin prompt/formula materialization: `cmd/gc/builtin_prompts.go`,
  `cmd/gc/system_formulas.go`
- Default config paths: `internal/config/config.go`
- Composition and path resolution: `internal/config/compose.go`
- Formula layer computation: `internal/config/pack.go`
- Automation scanning: `internal/automations/scanner.go`
- Automation CLI loading: `cmd/gc/cmd_automation.go`
- Startup materialization and staging: `cmd/gc/cmd_start.go`
- Template/session copy-files staging: `cmd/gc/template_resolve.go`
- Runtime reload path handling: `cmd/gc/city_runtime.go`
- Hook installation: `internal/hooks/hooks.go`
- Builtin pack injection and caches: `cmd/gc/embed_builtin_packs.go`,
  `internal/config/pack_fetch.go`, `internal/config/pack_include.go`
- Build-image filtering: `internal/buildimage/context.go`
- K8s initialization and staging: `internal/runtime/k8s/provider.go`,
  `internal/runtime/k8s/staging.go`
- Doctor checks and docs: `internal/doctor/checks.go`, `README.md`,
  `cmd/gc/skills/city.md`

## Recommended Implementation Shape

### Phase 1: Resolver and discovery

- Introduce canonical visible roots:
  `prompts/`, `formulas/`, `automations/`, `hooks/`, `scripts/`.
- Centralize mixed-mode path resolution.
- Switch discovery to `city.toml` first with legacy `.gc/` fallback.
- Add warnings, doctor checks, and migration planning output.

### Phase 2: Ownership boundaries and layer model

- Stop re-materializing builtin prompts/formulas into user-owned locations.
- Move binary-owned assets under `.gc/system/`.
- Refactor automation scanning to consume `ContentLayer` metadata.
- Update hook/script staging to source visible roots and stage compatibility
  destinations.

### Phase 3: Runtime and build-image hardening

- Convert build-image context assembly to an allowlist.
- Make K8s and local runtime staging use the same canonical source rules.
- Inject `GC_CITY_ROOT`, `GC_CITY_RUNTIME_DIR`, and `GC_PACK_STATE_DIR`.
- Hold pack runtime file relocation until env-var adoption is complete.
- Keep internal runtime layout changes behind helpers/env vars so pack authors
  do not have to guess `.gc/` internals from relative paths.

### Phase 4: Legacy removal

- Remove legacy `.gc/...` reads no earlier than Release N+2.
- Delete compatibility-only code paths, tests, and docs after doctor coverage
  and migration tooling have been in place for two releases.
- Migrate pack scripts from hard-coded `.gc/...` paths to runtime env vars.

## Bottom Line

The folder move is feasible, but it is not just a rename.

The risky parts are:

- city discovery currently depending on `.gc/`
- automation discovery being coupled to formula roots
- builtin prompt/formula materialization currently overwriting hidden paths that are treated elsewhere as user content
- builtin pack injection and cache layout sharing the same namespace
- pack scripts hard-coding `.gc/...` runtime paths

The safest architecture is:

- visible top-level folders for city-authored content
- hidden `.gc/` subtrees for runtime, system, and cache
- explicit compatibility aliases for legacy `.gc/...` config references
- env-based pack runtime paths before internal `.gc/` state is rearranged
