# RFC: Formula Registry Architecture — Decouple Configuration from Rig Source

**Status:** Draft (RFC, awaiting comment)
**Author:** mayor, 2026-05-12
**Tracking:** dr-1l9hc4 (autonomous-PR-ship), gastownhall/gascity#2030 (medium-term mitigation)
**Scope:** Gas City SDK (`gastownhall/gascity`), affects gc CLI, formula loader, packs format, city.toml schema

## Summary

Today, formulas (`.formula.toml` files defining executable work pipelines) are stored as code inside rig packs and inside the city's local `formulas/` directory. The formula loader resolves them from filesystem paths, which means a formula file's effective content is whatever happens to be on the rig path's currently-checked-out branch.

This conflates two distinct concerns:
- **Configuration** — formulas declare how work happens (steps, gates, vars, agent class).
- **Code edits** — work-in-progress branches that may be mid-implementation.

When a polecat works in a bd-managed worktree on a `bd-<bead>` branch, formula edits on that branch are invisible to every other session that reads the file from the rig base path. We hit this tonight with the `auto_push` gate addition — recovery required a five-step manual merge sequence across worktrees.

This RFC proposes moving the **formula registry** (the canonical store of formula definitions) out of rig source and into a stable city-scoped or system location, with explicit "import-source" semantics from rig packs. Code edits stay in rig branches; configuration becomes ref-stable by construction.

## Motivation

### Tonight's symptom (concrete)

Polecat-2 (working in `/home/ds/gascity-packs-worktrees/gascity-packs-polecat-2` on `bd-gpk-fdq`) added a `gate-auto-push-eligibility` step to `pr-review/formulas/mol-pr-from-issue.formula.toml`. Mayor needed to:

1. `git restore .` in `/home/ds/gascity-packs-main` (which had 187 pre-existing staged deletions, unrelated state corruption)
2. FF local `main` to `origin/main` (`77502dd`)
3. FF merge `bd-gpk-fdq` into local `main` (`24475ab`)
4. Merge `bd-gpk-bnk` with a conflict on bd-hook-leaked `issues.jsonl` (resolved by removal: `79ad1bf`, then cleanup commit `01b9d9f`)
5. Cherry-pick formula commit from `bd-gpk-4id-polecat1` (`32e25be`)
6. Switch `/home/ds/gascity-packs` base worktree, merge `main` into `bd-gpk-g5d` with conflict on the formula file (`a07bfea`)

…to make one configuration edit visible to the formula loader. The merge complexity is intrinsic to mixing config edits with code-branch lifecycle.

### Pattern (general)

The rig path serves two purposes today:
- **Code workspace** — where polecats clone branches, edit Go/Python/TOML files, commit, get reviewed
- **Configuration resolution surface** — where the formula loader and other config readers look up files at runtime

These two roles have incompatible state semantics:
- Code workspace wants per-branch isolation (so two polecats don't clobber each other; #1181)
- Configuration resolution wants ref-stability (so a single formula change becomes visible globally as soon as it's "blessed")

The current architecture serializes these via "whichever branch is checked out at the rig base wins" — which is the wrong invariant for both. For code: too easy to clobber. For config: too coupled to in-flight edits.

### Bugs caused by this conflation

- **gastownhall/gascity#1181** — implicit pool agents clobber each other's uncommitted edits when sharing one working tree (write-side)
- **gastownhall/gascity#2030** (just filed) — formula loader reads working-tree state, branch-trapping config edits (read-side)
- **gastownhall/gascity#2027/#2028** — formula resolver layer-precedence inversion; root cause was duplicated layer-iteration logic across two consumers, both of which assume "filesystem paths are the source of truth"

All three trace to the same architectural smell: **formulas live as code in rigs**.

## Current state (detailed)

### Filesystem layout

Formulas can live in any of these locations, all resolved as filesystem paths:

```
<city-root>/formulas/                          (city-local layer)
<city-root>/packs/<name>/formulas/             (imported pack, city-scope)
<rig-path>/<rig-pack>/formulas/                (rig-pack layer)
<rig-path>/formulas/                           (rig-local layer)
<rig-path>/.beads/formulas/                    (bd-managed mirror, symlinks)
```

`internal/config/pack.go:ComputeFormulaLayers` builds the layer order. `cmd/gc/formula_resolve.go:ResolveFormulas` materializes symlinks under `.beads/formulas/` for bd's use. `internal/formula/parser.go:loadFormula` reads files directly from layer paths.

### Resolution path (read-side)

```
gc sling --on <formula-name>
  → cmd/gc/cmd_sling.go
  → cfg.FormulaLayers.City | SearchPaths(rig)
  → formula.NewParser(paths...)
  → parser.loadFormula(name)
  → for each path: os.Stat(path/name.toml) → ParseFile
  → returns first match
```

No git ref consideration anywhere. Whatever the filesystem says is what gc uses.

### Why this seemed fine initially

- Single-developer / single-branch workflow: rig is on `main`, formulas are at HEAD, no conflict
- Pack imports: pack source is committed in a versioned manner, importing city sees the committed state
- bd worktree pattern was added later and didn't fold formula-resolution semantics in

## Proposal

### Two-layer separation

```
┌────────────────────────────────────────────────┐
│              Formula Registry                   │
│  (canonical, ref-stable, decoupled from code)  │
│                                                 │
│   $XDG_CONFIG_HOME/gc/formulas/<rig>/<name>.toml│
│   <city>/.gc/registry/formulas/<name>.toml     │
│   …or remote (OCI registry, git ref, etc.)     │
└────────────────────────────────────────────────┘
                       ↑
              (sync / import command)
                       ↑
┌────────────────────────────────────────────────┐
│            Rig Source (code workspace)          │
│                                                 │
│   <rig>/<pack>/formulas/<name>.formula.toml    │
│                                                 │
│   Treated as "import source", not the registry  │
│   Edits land here; require explicit promotion   │
│   step to update the registry                   │
└────────────────────────────────────────────────┘
```

**Key invariants:**
1. The registry is the source of truth for formula resolution at runtime.
2. Rig pack `formulas/` directories are "import sources" — like Helm chart sources or OCI image build inputs.
3. Promotion from rig source → registry is explicit (a `gc pack import` / `gc pack sync` command) and ref-pinned (`--from <ref>`).
4. Rig source edits do NOT auto-propagate. Author commits → opens PR → reviewer merges → maintainer runs `gc pack sync` (or it auto-runs on rig-pack-update events).
5. Registry storage is content-addressable (formula files immutable post-promotion; new versions are new files).

### Loader change

`internal/formula/parser.go:loadFormula` reads from the registry, not from rig source:

```go
// Pseudocode for the new path:
func (p *Parser) loadFormula(name string) (*Formula, error) {
    if cached, ok := p.cache[name]; ok {
        return cached, nil
    }
    path := p.registry.Lookup(name)  // → /home/ds/.config/gc/formulas/gascity-packs/mol-pr-from-issue.toml
    return p.ParseFile(path)
}
```

The `Registry` interface abstracts the storage. Implementations:
- `FilesystemRegistry` — backed by `$XDG_CONFIG_HOME/gc/formulas/<rig>/...`
- `EmbeddedRegistry` — formulas baked into the gc binary for the built-in `core` pack
- `RemoteRegistry` — pull from OCI / git ref / HTTP (future)

### Migration (phased)

**Phase 1 (compatible)**
- Add `Registry` interface + `FilesystemRegistry` default
- `gc pack sync` command that copies formula files from rig source → registry, idempotent
- Loader prefers registry when entry exists; falls back to current path-based resolution otherwise (backwards-compatible)
- Default `gc init` and `gc rig add` auto-run `gc pack sync` so new cities are registry-backed
- Existing cities continue to work via fallback; opt-in to registry via `gc pack sync`

**Phase 2 (enforced)**
- Loader requires registry entry; fallback removed
- `gc pack sync` becomes mandatory in `gc init` / `gc rig add`
- Rig source edits to `formulas/` files emit a deprecation warning: "this file is an import source, not the live formula; run `gc pack sync` to promote"

**Phase 3 (clean)**
- Rig source `formulas/` directories renamed to `formula-sources/` to disambiguate
- Documentation updates: formulas are first-class config, distinct from rig code

Phase 1 is shippable independently and resolves tonight's bug class without forcing any city to migrate.

### Symlink staging path

`cmd/gc/formula_resolve.go:ResolveFormulas` (the bd `.beads/formulas/` symlink staging) reads from the registry, not from rig source. The symlinks now point at the registry, so bd queries are consistent with `gc formula show` / `cook` / `sling`.

### Cross-worktree visibility (the original ask)

With the registry as canonical source:
- Polecat-A in bd-worktree edits `pr-review/formulas/mol-pr-from-issue.formula.toml` (the import source) → no effect on running sessions
- Polecat-A commits the change → still no effect
- Reviewer merges the PR upstream OR mayor merges locally → still no effect
- Mayor (or auto-sync hook) runs `gc pack sync --from <ref>` → registry updated, all sessions immediately see new formula on next `loadFormula` call
- Polecat-B (totally separate worktree, totally separate branch) reads `mol-pr-from-issue` → gets the new version from registry, regardless of B's branch state

The promotion step (`gc pack sync`) is the explicit, audit-loggable boundary between "config edit drafted" and "config edit live".

## Alternatives considered

### A1. Keep filesystem paths, add ref-stable resolution (`#2030` fix)

The medium-term mitigation we just filed. Formulas still live in rig source; loader reads via `git show <ref>:path` instead of `os.Stat`. Solves the symptom but keeps the architectural smell — rig path still doubles as both code-edit space and config-resolution space.

**Why this is the right ship-first fix and not a substitute for this RFC:**
- Phase 1 of this RFC can build on `#2030`'s loader-via-ref machinery
- `#2030` is shippable in a small PR; this RFC is months of work
- `#2030` doesn't preclude registry adoption — it's a strictly-better stopping point if registry adoption stalls

### A2. Force formulas into city scope only

Move all formulas to `<city>/formulas/` and forbid rig packs from contributing formulas. Simpler but kills the pack-as-distribution model — gascity-packs as a separate fork-able pack source becomes useless. Rejected.

### A3. Per-rig formula branches

Convention: every rig has a `formulas` branch that contains only the formula files; loader reads from `<rig>/formulas-branch:<path>`. Solves ref-stability without a registry. But adds branch-management overhead, breaks the pack-import model, and doesn't decouple config from code (just moves the coupling). Rejected.

### A4. Read-only filesystem snapshots

When a rig is registered, gc copies formula files to a stable read-only location and serves them from there. Mostly the registry idea but without the storage abstraction. The registry is the same proposal with a slightly more general interface — happy to land it this way if maintainers prefer a smaller surface.

## Open questions

1. **Registry storage location.** `$XDG_CONFIG_HOME/gc/formulas/<rig>/<name>.toml` for per-user setup vs. `<city>/.gc/registry/formulas/<rig>/<name>.toml` for city-scoped. The latter is auditable via git of the city itself; the former is shared across cities. Probably the city-scoped default with `$XDG_CONFIG_HOME` as an opt-in via `gc config set formula.registry.location`. Maintainer call.

2. **Versioning / content-addressing.** Should the registry keep history (every promoted formula is a new file under `<name>-<sha>.toml`, with `<name>.toml` as a symlink to current)? Enables rollback + audit of when each formula version went live. Adds storage and complexity. Probably yes for v1.

3. **Auto-sync on rig pack updates.** If a city imports a pack and the pack gets a new release with updated formulas, does `gc pack sync` run automatically on import update? Probably yes with an opt-out flag. Otherwise users miss formula updates the same way they miss code updates today.

4. **Concurrent edit semantics.** Two polecats edit the same formula in parallel bd-worktrees, both close their beads. `gc pack sync` runs on each close. Last-writer-wins for the registry is fine if promotion is sequential (single gc supervisor per city); needs locking if not. Probably fine for v1 — single supervisor is the common case.

5. **Pack format implications.** `pack.toml` may need a new field declaring which directories are import sources. Or convention: `formulas/` is always import-source; `formula-sources/` becomes the explicit-name in Phase 3.

6. **Built-in pack.** The `core` pack ships embedded formulas; those should be in `EmbeddedRegistry` from day one, not on the filesystem. Phase 1 might still leave them filesystem-backed for compat.

7. **Template fragments.** Same class of bug — template fragments are also resolved from filesystem. Should the registry also cover template fragments, agent prompts, skill definitions, hook scripts? Probably yes long-term; this RFC scopes to formulas to ship something concrete.

## Out of scope

- Cross-rig formula sharing (multi-rig formula imports beyond pack imports). Today each rig has its own formula set; staying that way.
- Formula content validation at promotion time. Schema validation already happens in `gc formula show`; promotion would just re-run that.
- Remote registries (OCI, git refs, HTTP) — listed as future implementation but not required for v1.
- Rewriting orders, skills, agents into the registry model. Each has its own resolution semantics worth separate RFCs.

## What this unblocks

- Auto-ship work (`dr-1l9hc4`) becomes safer: formula edits land in registry on promotion, not via cross-worktree merge sequences
- Pack distribution model gains a clean promotion gate (today, importing a pack and immediately using its formulas is a "trust me bro" operation; with the registry, promotion is an explicit auditable step)
- Multi-developer / multi-fork workflows become safe (each contributor's branch edits stay isolated; only promoted formulas affect runtime)
- Maintains feature-parity with the medium-term mitigation (#2030) while addressing the root architectural smell

## Implementation cost estimate

- **Phase 1 (compatible registry + fallback):** ~300-500 LOC across `internal/formula/registry.go` (new), `internal/formula/parser.go` (refactored loader), `cmd/gc/cmd_pack.go` (new `sync` subcommand), `cmd/gc/cmd_init.go` (auto-sync), tests. Maybe 1-2 weeks of focused work.
- **Phase 2 (enforced):** ~100 LOC removing the fallback, adding deprecation warnings, updating `gc rig add`.
- **Phase 3 (clean):** mostly docs + the `formula-sources/` rename.

## Comments welcome on

- Whether the registry boundary is the right one (vs. just landing #2030 and calling it done)
- Storage location default
- Whether Phase 1 should also cover template fragments / agent prompts in scope
- Whether to spawn a separate RFC for the symlink-staging side or fold it in
- Anything else the maintainer team sees that I'm missing

—mayor (sjarmak's workspace), drafting on the back of dr-1l9hc4. Happy to iterate on the framing or take a smaller cut.
