# Architecture orientation — mem

_Mechanically derived from the LikeC4 model (`likec4 export json`). High-altitude map only — names every subsystem, its purpose, delivery state, and exact source path so you targeted-read instead of grep-walking. For symbol-level depth, hand a source link below to an Explore/CodeGraph agent._

## Subsystems (58 elements)

- **Agent under test** (actor)
  Coding agent whose runs are replayed and measured across memory conditions
- **Multi-agent orchestrator** (externalSystem)
  Runs agents across 18 project rigs — produces the audit corpus
  - **Work-item store** (datastore)
    The work spine: 6,691 items (id, status, assignee, labels, deps, convoy)
  - **Session-log resolver** (externalSystem)
    Maps an assignee/session id to its transcript JSONL path
  - **Session traces (JSONL)** (datastore)
    874 resolved transcripts: tool calls, outputs, decisions, errors
- **GitHub** (externalSystem)
  PRs / commits — the external outcome label, where a linkage exists
- **Harbor** (externalSystem)
  Execution substrate that replays held-out tasks under each memory arm
- **mem** (system)
  Turns the work spine + transcripts into a work-audit graph, then benchmarks memory on it
  ↳ `../ARCHITECTURE.md, ../docs/architecture-decisions.md`
  - **memory-bench/ eval harness** (container)
    Replays held-out tasks under no_memory / oracle / memory_enabled and reads the gaps
    ↳ `../memory-bench, ../memory-bench/README.md`
    - **memory_systems/ — competitive arms** (component)
      Uniform ingest/retrieve interface; arms never read the store directly
      ↳ `../memory-bench/membench/memory_systems, ../docs/architecture-decisions.md`
      - **none / ours / oracle / filesystem / lexical** (component) #built
        Stateless floor, failure-triggered ours, exact-memory ceiling, the filesystem baseline, and the deterministic…
      - **builtin (agent-native memory)** (component) #evolving
        The agent’s own built-in memory as a no-store native-memory arm (Decision 22 / D-F, branch-ready mem-mor1) —…
        ↳ `../docs/architecture-decisions.md`
      - **consolidating / retention-scheduled** (component) #built
        Two-speed consolidation and TTL/retention-policy variants
      - **mem0 / A-MEM / NAT / Graphiti / NeMo-embed** (component) #built
        Third-party memory systems behind the same interface — real adapters, all on a local OSS stack (Qdrant / Chro…
        ↳ `../docs/architecture-decisions.md`
    - **bundle/ + oracle/** (component) #built
      Assembles oracle bundles (build, curate, consensus) and replays held-out tasks
      ↳ `../memory-bench/membench/bundle, ../docs/architecture-decisions.md`
    - **generators/ — synthetic-world track** (component) #built
      Authors seed-reproducible, memory-dependent task sequences — the track that produced the first measurable lif…
      ↳ `../memory-bench/membench/generators, ../docs/architecture-decisions.md`
      - **real-anchor / ftp-shape calibration** (component) #evolving #risk
        Calibrates the synthetic generator against real fail-to-pass shapes (mem-bxhh.5).
        ↳ `../memory-bench/membench/generators/external_anchor.py`
      - **memory-necessity + pilot filter** (component) #built
        Rejects tasks an agent can solve without memory (oracle ≈ no_memory), and a structural pilot filter on width/…
        ↳ `../memory-bench/membench/generators/memory_necessity_gate.py`
      - **enterprise-workflow materializer** (component) #built
        Turns a world into multi-session sequences: every fact, distractor, and supersession authored in code and see…
        ↳ `../memory-bench/membench/generators/enterprise_workflow.py`
      - **NeMo world builder** (component) #built
        Builds an EnterpriseWorld from a NeMo natural-language surface (personas / PRDs / repo shapes), then validate…
        ↳ `../memory-bench/membench/generators/nemo/world_builder.py`
      - **world freeze + determinism manifest** (component) #built
        Serializes worlds to fixtures/worlds/<seed>/ and verifies a determinism manifest, so a frozen world replays b…
        ↳ `../memory-bench/membench/generators/world_manifest.py`
    - **grading/ — gates & metrics** (component)
      Validity gates plus the scoring stack (L1 retrieval, L2 utilization, L3 end-to-end)
      ↳ `../memory-bench/membench/grading`
      - **ablation curve + graded scorer** (component) #evolving #risk
        Reward-vs-information saturation curve — the headline metric.
        ↳ `../memory-bench/membench/grading/ablation.py, ../docs/architecture-decisions.md`
      - **leak guard + validity gate** (component) #built
        Temporal LOO leak guard; oracle ≈ no_memory rejects the task
        ↳ `../memory-bench/membench/grading/leak_guard.py, ../docs/architecture-decisions.md`
      - **precision / coverage guard** (component) #built
        Injected-context volume + retrieval precision so over-injection cannot fake a win
        ↳ `../memory-bench/membench/grading/coverage.py, ../docs/architecture-decisions.md`
      - **trace / judge / merged-diff scorers** (component) #built
        Deterministic trace-error check, LLM-judge rubric, opportunistic merged-diff oracle
        ↳ `../memory-bench/membench/grading/judge.py`
      - **oracle-soundness gate** (component) #built #risk
        CodeScaleBench fail-to-pass admission gate.
        ↳ `../memory-bench/membench/grading/safety_gates.py`
    - **harbor/ — execution adapter** (component) #built
      WorkRecord→task-env, memory injection, control grids, repro, env recon
      ↳ `../memory-bench/membench/harbor`
    - **report/ + bbon/** (component) #evolving
      Per-arm vectors, comparative judge, narrative diffs, aggregate reporting
      ↳ `../memory-bench/membench/report`
    - **runner/** (component) #built
      Drives the agent per condition; records per-run conditions + metrics
      ↳ `../memory-bench/membench/runner`
    - **telemetry/ + metrics/** (component) #built
      5-axis vector (task, efficiency, latency, privacy, interruption) as OTel GenAI spans + ATIF
      ↳ `../memory-bench/membench/telemetry, ../docs/architecture-decisions.md`
    - **fixtures/worlds/ — frozen synthetic worlds** (datastore) #built
      Seed-keyed frozen NeMo worlds + determinism manifest; the synthetic corpus the harness replays from.
      ↳ `../memory-bench/membench/generators/world_manifest.py, ../docs/architecture-decisions.md`
  - **6-stage memory controller (MCP server)** (container) #planned
    Designed; v1 fills each stage with a heuristic/judge and logs the decision so it is trainable
    ↳ `../docs/architecture-decisions.md`
    - **need-classification → query-formation** (component) #planned
    - **minimal-useful injection** (component) #planned
    - **multi-type retrieval → reranking** (component) #planned
      rerank_features vector logged from run one (relevance / recency / importance / trust / task-fit)
    - **post-task write decision** (component) #planned
      Scored as P(future task improves | memory retrievable); dual-confidence (retrieval vs truth)
      ↳ `../docs/memory-prediction-and-dual-confidence.md`
  - **OpenRath projecting read-model** (container) #evolving
    Decision 20: OpenRath (arXiv 2606.19409) incorporated as a mem-owned read model over memory_events — a detect…
    ↳ `../docs/research-openrath-2606.19409-incorporation.md, ../docs/architecture-decisions.md`
  - **Fine-tuning / RL reranker** (container) #research
    research/ PRDs — learned reranker + retrieval behavior, trained once replay produces labels
  - **Multi-session sequence eval** (container) #planned
    Sequence / convoy-epic eval object — scales past the thin real task pool
    ↳ `../docs/architecture-decisions.md`
  - **Store builder & server** (container)
    src/ — ingest → parse → store → retrieve → distill, exposed through the mem CLI
    ↳ `../src`
    - **bin/mem CLI** (component) #built
      build-store, ingest-traces, query, retrieve, coverage, export/import-lessons — JSON envelope
      ↳ `../bin/mem, ../src/cli`
    - **distill/** (component) #evolving
      Distills prior resolutions into retrievable lessons with citations (236 so far, dashboard rig)
      ↳ `../src/distill/distiller.ts`
    - **store/ — SQLite + FTS5 sidecar** (datastore) #built
      .mem/store.db (schema v8).
      ↳ `../src/store/schema.ts, ../docs/architecture-decisions.md`
    - **ingest/** (component) #built
      Source readers → raw WorkRecords (pure IO)
      ↳ `../src/ingest`
      - **beads + outcomes** (component) #built #risk
        Reads the work spine and PR/commit outcomes.
        ↳ `../src/ingest/beads.ts, ../docs/architecture-decisions.md`
      - **landed + commit-linkage (git-native oracle)** (component) #built #risk
        Decision 18: the forward mirror of provenance — dates the branch tip at session close and takes the surviving…
        ↳ `../src/ingest/landed.ts, ../docs/architecture-decisions.md`
      - **provenance** (component) #built #risk
        Session-start base commit (commit-by-date).
        ↳ `../src/ingest/provenance.ts`
      - **repo resolve (rig→repo map)** (component) #built
        Canonical owner/name on every record; records how it resolved
        ↳ `../src/ingest/repo-resolve.ts`
      - **trace resolve + index** (component) #built
        Resolves each session id to its JSONL and indexes turns/tool calls
        ↳ `../src/ingest/trace-resolve.ts`
    - **parse/** (component) #built
      Two extractors, kept strictly separate (the ZFC boundary)
      ↳ `../src/parse`
      - **deterministic extractor** (component) #built
        Tool exit states + file:line build/test/lint errors + cross-task recurrence math — in code, no keyword heuris…
        ↳ `../src/parse/error-extractors.ts`
      - **semantic extractor** (component) #evolving
        Root-cause + resolution approach via a model — batched once per record, append-only
        ↳ `../src/parse/trace-parse.ts`
    - **retrieve/** (component) #built
      Failure-triggered retrieval v1: keys on normalized file:line + error-class
      ↳ `../src/retrieve, ../docs/architecture-decisions.md`
      - **progressive disclosure** (component) #built
        index → details --pick → full; agent sees token cost before hydrating
        ↳ `../src/retrieve/disclosure.ts, ../docs/architecture-decisions.md`
      - **retrieval + exclusions** (component) #built
        Temporal leave-one-out + convoy/supersedes/PR-share exclusions enforced at read time
        ↳ `../src/retrieve/exclusions.ts, ../docs/architecture-decisions.md`
- **Inference models** (externalSystem)
  Semantic extractor, LLM-judge, and embedding models (OSS / self-hosted — no-paid-API constraint)
- **NeMo / local NIM** (externalSystem)
  Natural-language surface for the synthetic-world generator; run offline against a local NIM, then frozen

## Connections (40 edges)

- `mem.store.ingest.beadReader` → `gascity.beadStore`: reads work spine + labels
- `mem.store.ingest.beadReader` → `github`: reads PR / commit outcome (where linked)
- `mem.store.ingest.traceReader` → `gascity.gcLogs`: resolves session → JSONL path
- `mem.store.ingest.traceReader` → `gascity.sessionTraces`: reads transcripts
- `mem.store.ingest.landed` → `github`: dates branch tip at close → landed commits (direct-to-main oracle)
- `mem.store.ingest` → `mem.store.parse`: raw WorkRecords
- `mem.store.parse.semantic` → `models`: root-cause + resolution (batched, once per record)
- `mem.store.parse` → `mem.store.graph`: writes signal + projections
- `mem.store.distill` → `mem.store.graph`: appends lessons
- `mem.store.distill` → `models`: distills resolutions
- `mem.store.retrieve` → `mem.store.graph`: failure-triggered query
- `mem.store.cli` → `mem.store.ingest`: build-store / ingest-traces
- `mem.store.cli` → `mem.store.retrieve`: mem retrieve
- `mem.store.cli` → `mem.store.graph`: mem query / coverage
- `mem.store.cli` → `mem.store.distill`: mem distill-lessons
- `mem.bench.bundle` → `mem.store.cli`: loads store via `mem query`
- `mem.bench.runner` → `mem.bench.bundle`: runs each condition
- `mem.bench.arms` → `mem.bench.bundle`: uniform ingest/retrieve interface
- `mem.bench.arms.armsBuilt` → `mem.store.retrieve`: the `ours` arm fires failure-triggered retrieval
- `mem.bench.runner` → `mem.bench.harborAdapter`: submits per-condition tasks
- `mem.bench.harborAdapter` → `harbor`: replays tasks
- `harbor` → `agent`: runs the agent under test
- `mem.bench.grading` → `mem.bench.runner`: gates + scores each run
- `mem.bench.grading.scorers` → `models`: LLM-judge rubric_score
- `mem.bench.telemetry` → `mem.bench.runner`: emits per-run spans
- `mem.bench.report` → `mem.bench.grading`: aggregates graded results per arm
- `mem.bench.generators.worldBuilder` → `nemo`: NeMo NL surface (run offline, then frozen)
- `mem.bench.generators.worldFreeze` → `mem.bench.worlds`: freezes worlds + determinism manifest
- `mem.bench.generators` → `mem.bench.worlds`: materializes seed-reproducible worlds
- `mem.bench.worlds` → `mem.bench.bundle`: frozen synthetic sequences feed the task pool
- `mem.bench.worlds` → `mem.store.graph`: synthetic records load as origin-marked WorkRecords — one firewall, one reader, one LOO (Decision 19 / D-J)
- `mem.bench.generators.calibration` → `mem.bench.grading.soundness`: calibrates synthetic shapes against real fail-to-pass oracles
- `mem.controller` → `mem.store.graph`: multi-type retrieval + write
- `mem.controller.classify` → `mem.controller.multiRetrieve`
- `mem.controller.multiRetrieve` → `mem.controller.inject`
- `mem.controller.inject` → `mem.controller.writeGate`
- `agent` → `mem.controller`: MCP retrieve / write / reflect
- `mem.reranker` → `mem.controller.multiRetrieve`: learned rerank
- `mem.sequenceEval` → `mem.bench.bundle`: multi-session task object
- `mem.openrath` → `mem.store.graph`: projects a blackboard read-model over memory_events
