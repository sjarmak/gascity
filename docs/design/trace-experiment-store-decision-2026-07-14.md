# Decision memo: trace / experiment store for replays, A/B, prompt/harness variants, evals

**Bead:** gc-485635 · **Author:** city-infra-polecat-1 · **Date:** 2026-07-14
**Status:** research + recommendation. Stephanie decides adoption.
**Related:** gc-485464 (record schema — owns the RECORD, this bead owns the STORE), gc-485440 / `bin/or-replay` (optimizer replay), Braintrust rejection 2026-07-14.

---

## TL;DR

**Two stores, split by axis, and do NOT stand up Langfuse now.**

1. **Orchestration / replay** stays on the event bus. Harden it: compact
   `.gc/events.jsonl` + archives to date-partitioned **Parquet**, query with
   **DuckDB** (embedded, zero services), and **materialize derived fields**
   (`dependent_count`, provenance) into **dolt**, which already runs. This is
   the only path that satisfies the load-bearing **REPLAYABLE** requirement,
   and no off-the-shelf tool satisfies it — `or-replay` already does.

2. **Eval runs + LLM traces** get a single **append-only record contract** in
   dolt (from gc-485464's schema) with a **fail-loud insert gate** (reject null
   SHA / model / seed), transcripts as local content-addressed blobs, queried
   by the same DuckDB layer. **Langfuse / MLflow are deferred to an optional
   Phase-2 read/view layer**, adopted only if a real UI need appears AND the box
   has RAM headroom (or it runs on a different host).

**Single strongest reason:** the one requirement that actually blocks our work
(REPLAYABLE — reconstruct a fixed trace and vary one variable) is met by **none**
of the candidate tools and is **already met by our own event bus**. Every
candidate store is a *viewer/tracker*, not a *replayer*. So the heavy option
(Langfuse self-host) pays a large, standing operational cost — on a box that is
**at swap saturation today** — to buy the requirement it *does* satisfy (trace
viewing) while leaving the blocking one untouched.

---

## Empirical grounding (measured on this box, 2026-07-14)

| Fact | Value | Source |
|---|---|---|
| Orchestration events | ~3.8M (106,936 live + 3,689,150 archived) | `wc -l .gc/events.jsonl`; `zcat archive-*.gz \| wc -l` |
| Live event log | 176 MB JSONL | `ls -lh .gc/events.jsonl` |
| Event archives | 20 gz files, ~1.3 GB compressed | `ls -lh .gc/events.jsonl.archive-*` |
| CSB result records | 36,288 `result.json` (~4 KB each) | `find CodeScaleBench -name result.json \| wc -l` |
| mem telemetry | OTel GenAI spans, **no OTLP collector wired** | `membench/telemetry/otel_spans.py:7` |
| dolt sql-server | **running**, port 29620, versioned DB | `.beads/dolt/.dolt/sql-server.info` |
| **Box memory** | **47 / 62 GiB RAM used** | `free -h` |
| **Box swap** | **8.0 / 8.0 GiB — 100% full** | `free -h`, `swapon --show` |

The last two rows decide the operational half of this memo. This is not a box
with spare capacity for a 5-6 container analytics stack.

---

## Requirements table (challenged and extended)

Hard requirements from the bead, plus three derived ones. Each is marked with
how load-bearing it is.

| # | Requirement | Weight | What it actually demands |
|---|---|---|---|
| R1 | **Local / self-hosted** | hard | Embargoed pre-pub results + private-repo source in transcripts never leave the box. Killed the Braintrust hosted path. |
| R2 | **Permanent retention** | hard | Compare grids across months. 14-day retention killed Braintrust. |
| R3 | **Immutable / append-only** for cited numbers | hard | A paper's number must be re-derivable: config + SHAs + seed + suite version + **input data**. |
| R4 | **Fail-loud on missing provenance** | hard | Reject null SHA/model/seed at write time. Nulls produced 30k unusable CSB rows. |
| **R5** | **REPLAYABLE** | **decisive** | Given a stored run, reconstruct exact conditions and re-run under ONE changed variable (optimizer / prompt / harness), everything else fixed. **The requirement most tools do not meet.** |
| R6 | **Scale without a distributed system** | hard | ~3.8M events + tens of thousands of eval rows + multi-GB transcripts, on ONE box, no cluster to operate. |
| R7 | Score ↔ trace linkage | derived | Today it is filename convention across four directory trees. Should be a foreign key, not a `glob`. |
| R8 | Low operational cost | derived | Must not compete with the workloads for RAM. On a swap-saturated box this is nearly as hard as R1. |
| R9 | Suite / dataset versioning | derived | "which version of the suite produced this" must be a stored field, not tribal knowledge. |

**R5 is the crux.** It is the requirement the OR work needs, and it is the one
that separates a *replay substrate* from a *trace viewer*. Weigh it heavily, per
the bead.

---

## The workloads are not one shape

| | (a) Orchestration traces | (b) Eval / benchmark runs | (c) LLM-level traces |
|---|---|---|---|
| Content | bead arrivals, claims, completions, dep edges, pool state | per-task scores, arms/conditions, tokens/cost, dual-verifier out, transcripts | prompt variants, tool-call sequences, model/tier |
| Consumer | replay simulators comparing **dispatch policies** | experiment/arm-vs-arm comparison | trace inspection, prompt debugging |
| Killer requirement | **R5 REPLAYABLE** + recompute derived fields | R3/R4/R7/R9 + comparison | R1 + a decent UI |
| Who serves it today | `.gc/events.jsonl` + `or-replay` (works) | filename convention across 4 trees (fragile) | mem emits OTLP spans, **nothing ingests them** |
| Built-for-this tool | **none** | MLflow, Langfuse | **Langfuse** |

(a) and (b)/(c) want **different stores**. (a) is a fixed-trace **simulation
input**; the store's job is faithful reconstruction and cheap columnar scans.
(b)/(c) are **comparison + viewing**; the store's job is scores, diffs, and a UI.
Forcing (a) into an eval tool buys nothing — no eval tool can replay — and
forcing (b)/(c) to stay as filename-convention leaves R7/R9 unmet. Hence **two
stores**.

---

## Candidates scored against the requirements

Scoring: ✅ meets · ⚠️ partial / with work · ❌ fails.

| Candidate | R1 local | R2 retain | R3 immut | R4 fail-loud | **R5 replay** | R6 scale | R8 op-cost | Verdict |
|---|---|---|---|---|---|---|---|---|
| **Self-hosted Langfuse (v3)** | ✅ | ✅ | ⚠️ | ⚠️ (app-level) | **❌** | ⚠️ | **❌ heavy** | Defer to optional viewer |
| **MLflow (self-host)** | ✅ | ✅ | ⚠️ | ⚠️ | **❌** | ✅ | ⚠️ | Fallback for (b) if UI needed |
| **W&B self-hosted** | ⚠️ ent-only | ✅ | ⚠️ | ⚠️ | ❌ | ⚠️ | ❌ | Reject (Braintrust-shaped: local deploy is enterprise) |
| **OTel + ClickHouse** | ✅ | ✅ | ⚠️ | build it | ❌ | ✅ | ❌ server | Reject (ClickHouse = a server to operate) |
| **OTel + DuckDB (embedded)** | ✅ | ✅ | ✅ (Parquet) | build it | **⚠️→✅** | ✅ | **✅** | **Store-1 query layer** |
| **dolt + defined artifact contract** | ✅ | ✅ | **✅ versioned** | **✅ at gate** | n/a (record store) | ✅ | **✅ already runs** | **Store-2 system of record** |
| **Extend what we have** (dolt + events + contract + DuckDB) | ✅ | ✅ | ✅ | ✅ | ✅ (via or-replay) | ✅ | ✅ | **Recommended** |

### Langfuse, examined seriously (it is the named candidate)

- **What it gives us:** OTLP/OpenTelemetry ingestion (natural fit for mem's
  existing GenAI spans), datasets, experiments + scores, prompt management, and
  a genuinely good trace UI. For workload (c) it is exactly the intended tool.
- **Self-host footprint (v3):** this is not one container. It is **Postgres +
  ClickHouse + Redis + S3-compatible blob store (MinIO) + web server + async
  worker** — 5-6 services. ClickHouse alone expects multiple GB of RAM to behave.
  On a box at **47/62 GiB and swap 100% full**, that is a standing 4-8 GiB
  liability competing directly with mem grids and the supervisor. This is
  disqualifying on **R8 alone**, before any feature discussion.
- **Licensing (do not assume):** the OSS core (MIT) covers tracing, prompt
  management, datasets, scores, and OTLP ingest. Several operational features are
  **commercial-gated even in the self-hosted build** — SSO enforcement / fine
  RBAC, some annotation-queue / managed-eval (LLM-as-judge) features, and data
  retention/management policies have historically sat behind the paid tier.
  Anything we build a workflow on must be confirmed OSS first; treat the paid
  boundary as movable and do not design dependencies across it.
- **Does it meet R5 (REPLAYABLE)?** **No.** Langfuse stores and *compares* runs;
  it does not reconstruct a fixed trace and re-execute it under a changed
  variable. It is a viewer + scorer, not a simulator. For the OR replay workload
  it adds nothing `or-replay` does not already do, at a large operational cost.

### The do-nothing-new option, taken seriously (per the bead)

We already run **dolt** (a versioned SQL database — R3 for free) and an
**append-only event bus** (R2/R6). The gaps are not "we lack a store"; they are:
(1) no **fail-loud provenance gate** (R4), (2) no **columnar query layer** so
every analysis re-parses JSONL (R8), (3) **score↔trace linkage is filenames**
(R7), (4) **derived fields are recomputed** each time instead of materialized.
All four are closed with a schema + a thin DuckDB layer, no new daemon. This is
the recommendation.

---

## One store or two? — **Two.**

- **Store-1 — orchestration / replay substrate.** The event bus, hardened.
  Compact `events.jsonl` + archives to date-partitioned Parquet; expose it
  through DuckDB views; materialize `dependent_count` and provenance into dolt so
  `or-replay` stops recomputing them. **R5 lives here and only here**, and it is
  already satisfied. No tool on the market improves it.

- **Store-2 — eval + LLM-trace record store.** One append-only dolt table
  (`experiment_runs`) built from gc-485464's schema, a fail-loud insert gate,
  transcripts as content-addressed local blobs, DuckDB for cross-month
  comparison. Optionally, later, a Langfuse/MLflow **read layer** on top of the
  OTLP spans mem already emits — never the system of record.

Forcing these into one tool serves neither: Store-1's requirement (replay) no
eval tool has; Store-2's requirement (scores/diffs/UI) the event bus does not
have. They share only DuckDB as a query engine, which is the right amount of
sharing (a library, not a service).

---

## Migration / backfill — what is recoverable, said plainly

- **Orchestration events (3.8M):** **losslessly importable** to Parquet. They are
  effectively immutable and `or-replay` already reconstructs claim times to
  0.37h median error. This backfill is real and worth doing.
- **CSB (36,288 records) and multi-week-old mem grids:** **provenance is largely
  unrecoverable.** We cannot reconstruct which SHA / model / seed produced a
  3-week-old row that never recorded them — that is precisely how 30k CSB rows
  became unusable. **Do not fabricate it.** Backfill is **forward-only**: stamp
  from the gate's activation date onward; mark historical rows
  `provenance=unknown` and exclude them from any cited number. Honesty here is
  the whole point of R4.

---

## Operational cost, in plain terms

| Option | Idle RAM cost | Services to operate | Reversibility |
|---|---|---|---|
| **Recommended** (dolt + Parquet + DuckDB) | ~0 new (dolt already up; DuckDB embedded) | 0 new daemons | drop table, delete Parquet |
| Langfuse v3 self-host | **4-8 GiB standing** (ClickHouse + PG + Redis + MinIO + 2 app) | 5-6 | tear down stack, migrate data out |
| MLflow self-host | ~0.5-1 GiB (tracking server + PG + artifact store) | 1-2 | moderate |
| ClickHouse-direct | multi-GiB | 1 server | moderate |

On a box with **zero free swap and an OOM history**, the RAM column is the
decision. The recommended path adds no standing daemon.

---

## Phased adoption path (Phase 1 is small and reversible)

**Phase 1 — days, fully reversible, no new daemon.**
1. Compact `events.jsonl` + archives → date-partitioned Parquet; wrap
   `or-replay`'s extraction behind a DuckDB view. (Reversible: delete Parquet.)
2. Define `experiment_runs` as a dolt table from gc-485464's schema; add a
   **fail-loud insert gate** that rejects null SHA / model / seed / suite-version.
   (Reversible: `drop table`.)
3. Point the mem / CSB / codeprobe result-writers at the gate. Historical rows
   marked `provenance=unknown`, not backfilled.
   *Exit check:* one real grid comparison runs end-to-end through DuckDB with
   every cited row carrying non-null provenance.

**Phase 2 — optional, only if a UI need is demonstrated AND RAM frees (or a
second host exists).**
- Stand up **MLflow first** (1-2 services) or Langfuse, wired as a **read/view
  layer** over the OTLP spans mem already emits and the `experiment_runs` table.
  Never let it become the system of record; dolt + Parquet stay authoritative.
- Trigger to revisit: a concrete workflow that the DuckDB/SQL layer cannot serve
  (interactive trace debugging at scale, shared annotation queues). Absent that,
  Phase 1 is the whole system.

---

## Anti-goals honored

- Not adopting Langfuse for popularity — judged against R1-R9 and rejected on R5
  + R8, with the door left open as an optional viewer.
- Not proposing an operational burden that competes with the workloads — the
  recommended path adds zero standing daemons on a swap-saturated box.
- Not duplicating gc-485464 — this memo consumes its record schema; if Store-2's
  contract needs a schema change, that goes **on gc-485464**, not a fork.
- Not building it yet — this is the recommendation. Stephanie decides adoption.
