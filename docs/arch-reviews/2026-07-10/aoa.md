# Architecture Review — aoa (AOA Toolkit)

**Repo:** /home/ds/projects/aoa · 16 Rust workspace crates, ~26K src LOC, 155 commits, active (last commit 2026-07-10) · Reviewed 2026-07-10, read-only.

## Executive summary

The workspace's layered design (trace substrate → measurement → trust gates → gated migration) is real and mostly honored: the c4 model matches the code, there is essentially no duplicated-struct/conversion sprawl, and every crate has tests. The two structural problems are (1) zero Rust CI — no fmt/clippy/test gate anywhere despite a 2,999-line binary-spawn integration suite being the de-facto safety net — and (2) the library boundary drawn one layer too high: the load-bearing pipelines (falsify evidence builder, eval scoring pipeline, codeprobe wire contract) live inside the CLI binary, which is why `crates/aoa/tests/cli.rs` is simultaneously the largest and highest-churn file in the repo (32 commits). Secondary leverage: the trace wire format has no version field and a closed 8-span enum with `additionalProperties: false`, forcing lockstep upgrades with external producers; `aoa-metrics` doubles as an accidental shared-types crate; and `aoa-gap` bundles four unrelated responsibilities whose four dependents each use a disjoint slice. Everything below is ranked by leverage per effort.

## Ranked findings

### 1. No Rust CI at all — the workspace's only workflow builds the architecture diagram
- **Evidence:** `.github/workflows/` contains exactly one file, `likec4-pages.yml` (LikeC4 site export + link check). No `cargo test`, `cargo clippy`, `cargo fmt`, and the shipped self-audit machinery (`crates/aoa/src/commands/self_audit.rs`, wired at `crates/aoa/src/cli.rs:131`) is never invoked in CI.
- **Why it matters:** every other finding in this report is only safely fixable behind a green gate. A toolkit whose whole thesis is "measure, don't trust checkboxes" currently has no automated measurement of itself; the 107-test `assert_cmd` suite exists but only runs when someone remembers. This is the cheapest change that de-risks all future structural work.
- **Effort:** S. **Risk:** none (additive; worst case is surfacing existing failures).

### 2. Trace wire format has no version field and a hard-closed schema — producers and consumers must upgrade in lockstep
- **Evidence:** `crates/aoa-trace/src/model.rs:11-28` (`Span`/`Trace`, no schema-version field); `crates/aoa-trace/src/span_type.rs:8-29` (closed `SpanType` enum, no `#[serde(other)]`, `pub const ALL: [SpanType; 8]`); `crates/aoa-trace/schema/trace.schema.json:8,19,22-34` (`additionalProperties: false` on trace and span objects, 8-value `type` enum). `SpanType::` appears 85 times across 13 files including both shims, aoa-metrics, aoa-enforce, and the CLI.
- **Why it matters:** the trace schema is the published contract with an external producer ecosystem (codeprobe transcripts, live `.aoa/traces` logs). Today an older reader hits a hard unknown-variant deserialization error on a newer trace, and the schema forbids even adding a negotiation field. Adding a 9th span type is a synchronized workspace-wide + external-producer break. A `schema_version` plus an `Unknown` fallback variant (with per-consumer drop/preserve policy) decouples upgrade timing permanently.
- **Effort:** S–M. **Risk:** low; the one design decision (drop vs preserve unknown spans per consumer) is reversible.

### 3. Codeprobe wire contract and provenance-reduction lattice are trapped inside the CLI binary
- **Evidence:** `crates/aoa/src/commands/codeprobe.rs:33-116` (`DualScoring` — the codeprobe `scoring.json` contract plus clean-dual validation rules) and `:190-221` (`aggregate_provenance` — the provenance lattice: synthesized→hard error, any-None→unavailable, native>external). Consumed by `r0b.rs`, `eval_run.rs`, and `falsify_build.rs` — all inside the bin crate; 9 library-grade unit tests stranded with it. A second copy of the scoring schema lives at `eval_run.rs:67-79`.
- **Why it matters:** this is an external-tool contract and a domain rule of `aoa-gap`/`aoa-bench`, reachable only by spawning the binary. Extracting it (into `aoa-bench` or a small `aoa-codeprobe` crate) is the prerequisite that unblocks findings 6 and 7 — both `falsify_build` and `eval_run` import it.
- **Effort:** M. **Risk:** low — move-and-re-point, tests move with it.

### 4. Dead dependency edge: `aoa-gap` declares `aoa-metrics` and never uses it
- **Evidence:** `crates/aoa-gap/Cargo.toml:10` lists `aoa-metrics`; a full grep of `aoa-gap/src` and `tests` finds zero `aoa_metrics` references.
- **Why it matters:** the phantom edge misleads every dependency-graph reading ("gap needs the symbol graph" — it doesn't; gap is pure arithmetic over its own types) and adds compile weight to gap's four dependents. One-line deletion.
- **Effort:** S. **Risk:** none.

### 5. Terminal-injection escaping and the dual-register output contract are enforced by convention, per-author, per-command
- **Evidence:** `crates/aoa/src/output.rs` is 19 lines (`print_json`/`print_human`); the actual R17 dual-register contract is implemented as 16 bespoke `render_*` functions across 11 command files with 28 hand-written `#[derive(Serialize)]` view structs (e.g. `eval_run.rs:410`, `r0b.rs:288`, `falsify_build.rs:599`). The `escape_debug` terminal-injection hardening is copy-pasted 23 times across 8 files. The capped-JSON-read trio (`read_to_string_capped` → `serde_json::from_str` → `.with_context`) repeats verbatim at 6 sites (`falsify_build.rs:573`, `r0b.rs:57`, `report.rs:124`, `eval.rs:75`, `self_audit.rs:193`, `codeprobe.rs:47`).
- **Why it matters:** a security control (escaping untrusted trace/transcript fields) and a DoS guard (byte cap) each depend on every future command author remembering them. A `Report` trait binding `Serialize + render_human()`, one shared escape helper, and a generic `load_json_capped<T>` make the contracts structural instead of memorial.
- **Effort:** S (escape + json helper) / M (Report trait). **Risk:** low, mechanical.

### 6. The R0 falsification evidence builder — the toolkit's load-bearing experiment assembler — lives entirely in the binary
- **Evidence:** `crates/aoa/src/commands/falsify_build.rs` (694 lines + `falsify_build/answer.rs`): experiment manifest schema (lines 67–145), build-report schema (151–202), and the full assembly orchestration — per-arm held-out joining, identical-pair detection, cross-seed exclusion accounting, eligibility derivation (`build_repo` 262–445, `build` 478–563). `aoa-falsify` owns the verdict types but nothing can construct a `FalsifyInput` from paired run dirs without shelling out to `aoa`.
- **Why it matters:** R0 is the whole design's gate ("does migrating the repo beat swapping the harness"). Its honesty rules (degraded-sentinel abstention, native-span derivation) sit above the library seam, unverifiable except through process-spawn integration tests — a major driver of the `cli.rs` churn (32 commits, 2,999 lines, 107 spawn tests).
- **Effort:** L (extract `aoa-falsify-build`; do finding 3 first). **Risk:** medium blast radius but behavior-preserving; the existing integration suite is the safety net (after finding 1 puts it in CI).

### 7. The per-task eval scoring pipeline has no library entry point
- **Evidence:** `crates/aoa/src/commands/eval_run.rs:274-387` (`process_task`: trace-shim → symbol-graph build/degrade → oracle join → four metrics + gap → `TaskRecord`), graph-source selection at 216–225, subtree-partition policy at 239–271; the canonical `EvalRunReport`/`TaskRecord` record schema is defined in the CLI (lines 81–140).
- **Why it matters:** "transcript in, scored record out" is the toolkit's core composition, and any batch scorer, CI service, or future live-observe path must either spawn the binary or re-derive the join. Same extraction pattern as finding 6; together they convert the bulk of the 107 spawn tests into fast unit tests and make the "CLI = thin wiring" crate description true.
- **Effort:** L. **Risk:** medium, behavior-preserving refactor.

### 8. `aoa-metrics` is an accidental shared-types crate; substrate type ownership is inverted
- **Evidence:** `SymbolGraph`, `IndexQuality`, `TransformMap`, `MetricInput`, `Confidence` live in `crates/aoa-metrics/src/input.rs:13-168`; `aoa-scip-graph` depends on aoa-metrics solely to name its own output type and re-exports it as its public API (`crates/aoa-scip-graph/src/lib.rs:35`); `aoa-falsify` pulls the whole crate for the `Confidence` enum alone (`falsify/src/types.rs:4`).
- **Why it matters:** graph producers and the falsify gate compile the locality-math extractors they never call, and any future graph producer inherits the same wrong-direction edge. Moving the ~4 substrate types down (small `aoa-graph` crate, or into `aoa-trace` which everything already depends on) makes the substrate layer of the c4 model match the Cargo graph.
- **Effort:** M. **Risk:** low, type moves with re-exports for compatibility.

### 9. Metric identity has three sources of truth reconciled by drift tests, not types
- **Evidence:** audit's `FindingKind` enum keys `structure_measurements` (`crates/aoa-audit/src/structure.rs:350-388`); gap keys the same concept by string consts (`GATING_CANDIDATES`, `crates/aoa-gap/src/construct.rs:195-231`); `recommend::join` is the hand-maintained bridge table (`crates/aoa-recommend/src/lib.rs:233-256`) guarded only by runtime drift tests (`lib.rs:690-715`). A fourth, deliberately narrowed copy of the orientation policy lives in the CLI (`crates/aoa/src/commands/corpus.rs:42-78`, doc comment admits it "mirrors `aoa_recommend`'s private `join`").
- **Why it matters:** adding one gating metric currently means editing an enum, a string table, a join table, and a CLI mirror, with tests as the only tripwire. One shared enum (or deriving the gap strings from `FindingKind`) makes it a single-site, compile-checked change.
- **Effort:** M. **Risk:** low.

### 10. `aoa-gap` bundles four unrelated responsibilities; its four dependents each use a disjoint slice
- **Evidence:** `crates/aoa-gap/src/lib.rs:25-61` exposes: held-out gap gate (`gap.rs`/`run.rs`/`provenance.rs`/`compare.rs`), construct validity (`construct.rs`/`signal.rs`), git revert mining + external-outcome corpus (`outcome.rs`), and the Factory checkbox-baseline rubric (`checkbox_baseline.rs`) plus Spearman stats (`correlation.rs`). Dependent slices are disjoint: bench uses `RunResult`/`TaskOutcome`/`HeldOutProvenance` only; falsify uses `HeldOutProvenance` only; audit uses `BehavioralSignal`/`InsufficientDataNote`; recommend uses `ConstructValidityReport`/`MetricMode`. Zero dependents touch `checkbox_baseline`/`Corpus`/`spearman`.
- **Why it matters:** the most-shared type is a 4-variant enum, yet bench and falsify compile revert mining and permutation statistics to get it. A `gap-core` (provenance/run/gap) vs construct-validity vs CLI-only corpus split gives each dependent a legible surface. Lower rank than it looks: the cost today is compile weight and reading confusion, not correctness.
- **Effort:** M–L. **Risk:** low–medium (mechanical moves, 5 consumers re-point).

### 11. Repo-root pollution from agent-harness scaffolding, with uncommitted .gitignore edits
- **Evidence:** 13 untracked `aoa-*` worktree dirs plus `worktrees/`, `.gc/`, `.codex/`, `.ruff_cache`, `.DS_Store` at repo root; none covered by `.gitignore`; `.gitignore` and `CLAUDE.md` carry uncommitted modifications.
- **Why it matters:** low architectural leverage, but it degrades every future agent run against this repo (the toolkit's own audience) — root-listing noise, accidental-add risk. Cheap to fence off.
- **Effort:** S. **Risk:** none.

## Strengths to protect

- **The layering thesis holds in the code.** The c4 model (`architecture/model.c4`) matches reality — every doc claim spot-checked was accurate, subcommands align with `cli.rs`, and the read-only vs write-gated split is real (only `aoa-migrate` writes, behind the falsify gate).
- **No conversion-glue sprawl.** Exactly one cross-crate `From` impl in the tree; `aoa-bench` reuses gap's types instead of redefining them. The `*Outcome`/`*Result` families are distinct concepts sharing a suffix, not duplicates.
- **Honest-degradation discipline** (degraded graph lowers weight, never raises scores; nulls with reasons instead of fabricated values) is consistently carried through `eval_run`, metrics, and scip-graph, and is worth preserving through any extraction.
- **`migrate.rs` and `policy.rs`** are the model thin commands; `commands/enforce.rs` is genuine hook wiring, not a duplicate of `aoa-enforce`.

## Leave unchanged

- The eslint `assets/` tree: only 4 files tracked; the 42 MB `node_modules` is a gitignored local `npm ci` install, deliberately hermetic (`crates/aoa-migrate/assets/eslint/README.md`).
- Test coverage topology: all 16 crates have tests (weakest are aoa-budget and aoa-lint with single integration files — fine at their size).
- The three "held-out success" shapes (`gap::TaskOutcome`, `falsify::PairTask`, `metrics::MetricInput`) — legitimately different (single vs A/B-paired vs per-record); do not unify.
- Fixtures: no copied fixture trees, just a reused `auth.py`/`tokens.py` motif.
- Unused `#planned`/`#research` c4 tags: cosmetic; note only.
