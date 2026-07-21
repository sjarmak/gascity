# Gas City design audit — ds-research, 2026-07-21

Scope: orders, formulas, bin/ scripts, agent prompts, services/OOM surface, config/docs/packs
drift, and incident archaeology with a Temporal-fit assessment. Seven parallel read-only audit
agents; every finding below carries file:line or command evidence in the source sections.

Companion evidence: `.gc-reports/reliability-evidence-2026-07-21.md` (overnight investigation,
including an outage that investigation itself caused). This report incorporates and does not
supersede it.

Goals frame agreed this session: the city is a **research force-multiplier** for the priority
rigs; **silent unrecoverable loss** is the worst failure class, stalls second, operator load
third. Cleanup mandate: git-init + hard prune. Roster mandate: shrink aggressively without
dropping detection coverage. Packs: single-source-of-truth split. Temporal: lean in where
evidence supports it, single box fixed.

---

## 0. Live incident during the audit (still active at time of writing)

`gascity.slice` is pegged at 31.1 GB against `MemoryMax=32G`; `memory.events oom_kill` climbed
24 → 28 during the audit window. The canonical bead-store dolt was memcg-killed **three times
today** (07:52 global, 09:28, 09:42). It runs inside `gascity-agents.slice/run-u41328.scope`
with `oom_score_adj=200`, i.e. the kernel treats the shared bead store for all 23 rigs as
exactly as disposable as one agent pane.

Each kill respawns dolt on a different port, which desynchronizes the supervisor's
`10-dolt-port.conf` pin, which opens the dolt circuit breaker, which freezes the order floor.
`~/.gc/supervisor.log` held 51,155 circuit-breaker events at audit time.

**Applied this session** (snapshot + one-concern diff + comment contract, per
`cityops-city-change-control`):
- `10-dolt-port.conf` rebound 29621 → 29620 to match `sql-server.info`; `daemon-reload` +
  supervisor restart. Verified: breaker went `open → closed` on all rigs at 09:42:19; zero
  further 29621 references.
- Reaped 4 zombie test dolts whose cwd was deleted (`/tmp/Test*`, 2.6 days old, ~134 MB).
  Orphan-only, no judgment call: their working directories no longer exist.

**NOT applied, needs your call** (§7 Decision 1): moving the canonical dolt out of the agent
slice. It is the single highest-value stabilization available and it costs no capacity, but a
botched live cgroup move takes the bead store down for 23 rigs.

The honest arithmetic: postgres 15.0 GB + gascity.slice 32 GB + app.slice 12 GB exceeds 62 GB
of RAM with swap already 100% consumed. **No leak reaping fixes this.** Roughly 96% of resident
agent memory is warm-by-config (40 min_active floors + 24 `mode="always"` named sessions = 64
against 67-69 live sessions), not demand-driven. Demand exceeds supply structurally.

---

## 1. Where work is silently lost (ranked by your stated priority)

### 1.1 States designed to be reaper-visible that no reaper consumes

The strongest single finding in the audit. `mol-focus-review` stamps
`gc.needs_recovery=true` on land failure and explicitly comments that the state is
"queryable by a reaper" (`formulas/mol-focus-review.formula.toml:838`). Grep across `bin/`,
`orders/`, and `.gc/*.yaml`: **zero consumers**. Recovery rides on one best-effort
`gc session nudge mayor` at failure time. Miss that nudge and verified-passing work sits
blocked forever. `work-landing-reaper` cannot see it (it scans *closed* beads).

Same shape for `review_verdict=escalate` (`:565-574`), which blocks the bead and writes a note
but sends no nudge or mail at all. Nothing sweeps it.

Three further uncovered stuck-states, each a silent work-loss path:
- **Partial re-arm crash**: the review-reject script mutates in sequence (verdict write :631 →
  reopen :644 → unassign :656). A failure between reopen and unassign leaves step beads
  open + assigned + stale: invisible to the pool (claim hook skips assigned), not `in_progress`
  (so `stale-claim-reaper` misses it), root not orphaned (so `orphaned-molecule-reaper` misses
  it).
- **Closed root, open steps**: every `mol-pr-*` halt path closes the root mid-chain.
  `orphaned-molecule-reaper` triggers on roots that are *in_progress* — the inverse shape.
  This is the recurring "orphan finalize / conversion-stall" class handled by hand today.
- **`mol-epic-review` in-flight flag has no expiry**: `epic-review-sweeper` skips
  unconditionally while `review_in_flight=true` (`bin/epic-review-sweeper:151-154`). A reviewer
  dying mid-molecule wedges that epic's review permanently.

### 1.2 Stale claims cannot be reliably released

From the companion brief, independently consistent with our findings: work beads stranded on
dead sessions = 14, beads reopened by the release path = 9, **overlap = 0**. The two mechanisms
miss each other completely. `completion-reconciler` is currently `failed` with 319 `scan_failed`
events because `stores_for()` runs `gc rig list --json` with a 60s timeout before the lock and
before any store is scanned, so a timeout aborts the whole run including the city store that
needs no rig list. There is no `claimed_at` on beads; the lease is measured off `updated_at`,
which `bd` also bumps during unrelated dependency recomputes.

### 1.3 A RootOnly strand class still armed

`pour = true` is present on `mol-do-work`, `mol-scoped-work`, `mol-focus-review`.
**`mol-pr-from-issue` is vapor + `formula_compiler >= 2.0.0` with no `pour`** (`:59-63`) — the
exact `compile.go:325` RootOnly configuration that strands scaffolding with nothing routed. It
escapes today only because `maintenance-cycle` uses the exec-wrapper shape. Any direct
`--formula` instantiation strands. The pack copy has the same gap. One-line fix, both copies.

### 1.4 The pool-wedge fix landed in one of three copies

`mol-pr-iterate:146-164` stages a dedicated worktree (the gascity#2451 fix, explicitly "do NOT
`gh pr checkout` in place"). **`mol-pr-merge-only:98-100` still does in-place `gh pr checkout`**
and **`mol-pr-revert:310-312` does in-place `git checkout -B`** — both clobber the slot's
branch, the exact wedge iterate was patched for. `mol-pr-merge-only` also never sets
`gc.halt_chain` or `summary_for_human`, so all its halts are invisible to the loop-close
handler, and it squash-merges (an external write) gated only on convention, checking no
evidence field.

### 1.5 Escalation into a dead-letter box

`gascity-packs-polecat/prompt.template.md:204-242` instructs `gc mail send human` on every
close and every stuck condition. The sibling `polecat/prompt.template.md:205-207` explicitly
warns that mailbox is not read and uses `bin/terminal-worker-escalation` instead. The packs
polecat never received the dr-80uo update. Every stuck packs polecat escalates into a void.

---

## 2. De-duplication

### 2.1 Orders: ~850 exec-fires/hour, and the cadence is itself a cause

Steady state, excluding event orders: 30s×2 + 1m×4 + 2m×4 + 5m×11 + 10m×4 + 12m×2 + 15m×11 +
20m + 30m×12 + 45m + 1h×10 ≈ **850 execs/hour ≈ 14/min**, each spawning a gc/bd/jq process tree
on a toolchain with a ~455 MB RSS floor. Event orders add more: `gc order check` showed 590
pending `bead.updated` for `nudge-on-route` alone.

Merge clusters, coverage-preserving:

| Cluster | Members | Proposal |
|---|---|---|
| Session peekers | `login-wedge-scanner` 5m (peeks *every* session), `polecat-ui-stuck-scanner` 5m, `pl-529-recovery` 2m, `pool-liveness-sensor` 10m | One 5m peek pass capturing each session tail **once**, feeding 4 classifiers. Survivor: `polecat-ui-stuck-scanner` (already has the fan-out + state machinery). Full scan measured at 1m43s over ~70 sessions; today that cost is paid 4×. |
| Dispatcher wedge | `dispatcher-watchdog` (idle≥5h), `dispatcher-liveness-sensor` (heartbeat), `polecat-ui-stuck-scanner` dispatcher pass (6h) | Three thresholds for one condition. `dispatcher-liveness-sensor` becomes sole detector; `dispatcher-watchdog` keeps only the kill actuator; delete the scanner's pass. |
| am/pm twins | `executive-status-sync`, `mayor-health-surfacer`, `pl-status-update` ×2 each | gc cron accepts hour lists (`mem-digest` already does `30 8,15 * * *`). 6 files → 3. Additionally `mayor-health-surfacer` runs the same detector `stall-watch` runs every 15m; fold to a stall-watch daily rollup and retire both legs. |
| Stale-bead surfacers | `decision-staleness-reaper`, `stale-attention-reaper` | One reaper, two rules. Same target, same contract, same store. |
| Mailbox hygiene | `mail-dupe-dedupe`, `mayor-mail-janitor` | One janitor, two rules. |
| Resource eyes | `resource-observability` 5m, `resource-sweep` 3h, `resource-creep-report`, `storage-ledger`, `disk-pressure-guard` | Fold `resource-sweep` into `resource-observability`; its thresholds are already covered and were **proven blind in the 07-15 host death**. Keep creep-report (trend axis) and disk-pressure-guard (actuator). |
| city-infra tick | `patrol-city-infra-pl` 15m bare nudge, `mechanic-cycle` 20m nudge+payload | `mechanic-cycle`'s header states the bare nudge is unreliable and supersedes it. Retire `patrol-city-infra-pl`. |

### 2.2 Scripts

- **github-mirror family**: `github_mirror_common.py` exists, yet the correctness-critical
  `is_wisp()` filter is copy-pasted 3× (`github-mirror:62`, `github-central-mirror:146`,
  `github-mirror-reconcile:66`) and already drifting in shape. Divergence here leaks synthetic
  beads to GitHub, the family's own stated failure mode. `run()` 3×, `bd_json()` 2×.
- **Slack publish idiom** reimplemented in **10 scripts** with **9 hardcoded channel IDs**;
  `C0B0TQMQF2B` appears 16×. The `.bak-*-channel-to-allagentcity` trail on 4 scripts is exactly
  the cost of that, paid by hand on 07-13.
- **issue-\* family**: `gh_json()` copy-pasted 5× with drifted timeouts (60s/90s/90s).
- **Double-scheduled**: the weekly dolt flatten fires twice (crontab Sun 04:00 via a deprecated
  8-line shim, plus `orders/dolt-flatten-maintenance.toml` Sun 06:30); flock prevents overlap so
  the second run is simply wasted. `nudge-poll-reaper` runs as both an order and a systemd timer.

### 2.3 Prompts

106 lines are verbatim-identical across ≥6 of the 8 rig-PL prompts; 182 across ≥4 (true
duplication is roughly double once rig-name-only differences are counted). Eight blocks belong
in `template-fragments/`: Slack reply protocol, address-by-handle, mrkdwn rules, dedup protocol,
rollup shape, rig-scoped dispatch, replies-from-the-human, Stephanie reply format. The mechanism
is proven (dr-zsxe0 converted three fragments successfully); the rest was never migrated. This
is literally city-infra-pl's own founding-charter item 5, still open.

Also: 3 worker `prompt.template.md` files are md5-identical, and `polecat` vs
`gascity-packs-polecat` are ~200 lines the same modulo rig names, with the packs copy drifted
dangerously (§1.5).

### 2.4 Formulas

The PR family is copy-paste drift, not factoring: the `ROOT_ID` + notes-record intake shape is
pasted ~25× across 5 formulas, and `mol-pr-merge-only:69-71` / `mol-pr-ci-diagnose:53-55`
contain verbatim-identical blocks. Extract a `mol-pr-base` (extends-style, like
`mol-polecat-base`) holding intake, worktree staging, halt-and-close, and evidence steps; the
four variants become ≤100-line overlays. That refactor **mechanically fixes** §1.4.

---

## 3. Pruning

**Zero-yield orders** (running for months, never fired their purpose):
- `pl-529-recovery` — every 2m since 2026-05-20; state file is `{}`. ~40,000 scans of every PL
  session, **zero detections in 2 months**.
- `spawn-storm-detect` — state file `{}` with an mtime of its creation date (2026-05-25).
  **Zero detections in 8 weeks** at 12 fires/hour.
- `route-decide-report` — weekly since 2026-05-28, **never produced an artifact**; the FCTR
  Phase-2 consumer it measured for never shipped.

**Superseded**: `rig-patrol.toml.disabled`, `bead-janitor.toml.disabled`, `vault-status-mirror`
(disabled via a distant city.toml override, which is a re-enable trap), `maintenance-cycle`
(override-paused, its own comment schedules retirement after a clean Temporal week ~07-23).

**Open inconsistency**: `temporal-maintenance-signal-*.toml.disabled` ×2 say "enable at Phase 4
once Temporal is deployed". Temporal **is** deployed and armed since the 07-16 P5 cutover. Either
the dispatch-only Schedule made Signals moot (then delete and record it) or they were forgotten
(then the soak is running without its event bridges). Needs an explicit call at the 07-23 gate.

**Calibration phases with no end condition**: `idle-session-report` (hourly, report-only, log at
4.6 MB, auto-kill "intentionally NOT wired" since early July) and `evals-nightly` (report-only
since 07-03, last two reports byte-identical, nothing consumes the log).

**Formulas with zero callers**: `mol-polecat-base`/`-commit` (Gastown role model that does not
exist in this city), `mol-idle-rig-research` (dispatched by a "deacon patrol"; there is no
deacon), `mol-dispatch` (routing table predates the current fleet), `mol-patrol` (only caller is
a disabled order, and it mails the dead-letter box), `mol-skill-work` (one docs reference, and
it instructs premature close, the exact class that stranded vdeyx/s1o2n).

**Hard-orphan scripts** (zero references anywhere): `nudge-poll-runaway-watcher.sh` and
`control-dispatcher-workquery.sh` (both self-declared tombstones), `mayor-status`, `gc-focus`,
`freshwake-measure`, and **`gc-clear-locks`, which is actively dangerous**: it removes dolt LOCK
files across all rigs, written before the shared managed-server topology, where that LOCK belongs
to the live server. Wire-or-delete decisions: `formula-version-sweep` (203 lines, built 07-17,
never wired) and `status-change-view` (consumer never landed).

**Agent dirs**: `_archived-gascity-worker-stub` (self-declared archived, still discoverable),
`cid-worker` (suspended duplicate of `code-intel-digest-worker`), `codex-reviewer` (provider=codex
sessions die silently per dr-nxtu), `meta-reviewer` and `scix-worker-4` (no floors, rig
suspended). `city-infra-polecat/agent.toml` carries three mutually contradictory stories in 26
lines: a DEPRECATED header directly above `suspended = false`, `min_active_sessions = 2`, and a
comment claiming "0->1".

**Abandoned rigs**: `agent-diagnostics` (1 open bead, untouched since 2026-05-31) and
`background-agents` (zero issues ever). Stranded backlog on suspended rigs is significant:
zeldascension 72 open, CodeScaleBench 97 open (with `csb-pl` running while the rig is suspended),
scix-experiments 140.

---

## 4. Hardening

### 4.1 Checks that do not run

- **20 pytest suites in `bin/` are invoked by nothing** — no order, no cron, no wrapper. That
  includes `completion-reconciler` (mutates state every 15m from systemd, and is currently
  failing) and `dispatcher-watchdog`. This reproduces the exact incident `city-selftest` was
  created to prevent: its own header reads "a check nobody runs is not a check".
- **`city-selftest`'s timeout invariant is breached again.** Its TOML states "at 60s/suite, 11
  suites fill 660s; a 12th MUST raise timeout". The glob now matches **16 suites**: 960s worst
  case against a 660s timeout. A hung suite kills the order and escalates nothing.
- **No tests at all** on the highest-blast-radius mutators: `close-gate-reaper` (reopens beads),
  `stale-claim-reaper` (unclaims), `orphaned-molecule-reaper` (force-closes roots), `stall-watch`
  and `escalate-surfacer` (the human-escalation path itself).

### 4.2 Detector failures that read as "all clear"

`stall-watch:95` runs `timeout 240 "$SURFACER" --json … || true`. If the detector crashes,
stall-watch reports "no stalls" forever. Empty findings and detector-failed are indistinguishable.
This is the same failure shape as the 07-15 host death, where `resource-sweep` ran green while
watching MemAvailable as swap went to zero, and as the 07-16 Temporal incident, where every
liveness signal was green while zero work flowed for hours.

Generalize: **liveness checks pass while outcomes fail.** `temporal-soak-check` is the one order
built on outcome checks rather than liveness, and it is the pattern the rest should follow.

### 4.3 Unbounded growth

`/` is at 89%. `.gc` is 8.5 GB: `beads.json` 62 MB live with **18 backups totalling 1.7 GB**
(seven June janitor baks still present), `events.jsonl` 138 MB with **26 archives at 1.5 GB**,
`jsonl-archive` **2.1 GB uncompressed**, reconciler trace segments **1016 MB**, plus **1.35 GB**
of uncompressed dispatcher `.log.1/.2` rotation remnants that the janitor glob does not match.

Log rotation covers 4-5 paths. **~35 append-forever logs in `.gc/*.log` are covered by nothing**:
`stale-worktree-reaper.log` at 30 MB (accumulating since 2026-05-04), `codegraph-sync.log` at
20 MB, `idle-session-report.log` 4.6 MB. One list-file entry fixes the class.

Also: 1,963 files in `runtime/sling-source-locks/`, 751 older than 30 days, with no sweeper
covering lock dirs; a zero-byte `beads.json.lock` from Jul 6 with no holder.

Net reclaimable: roughly **6 GB of 8.5 GB**.

### 4.4 Memory-cost hot spots

`.gc/beads.json` is 62 MB and every full `json.loads(read_text())` costs roughly 0.5-1 GB
transient RSS on a box where oomd is already killing bystanders. `mail-dupe-dedupe` does this
**hourly** (24 full parses/day, to find duplicate mayor mail) and has no try/except at all.

Heavy work outside bounded cgroups: only `codegraph-sync` uses `scix-batch`. `dolt-flatten-maintenance`
(45m), `storage-ledger` (walks every worktree), `stale-worktree-reaper` (du across all rigs,
hourly), `janitor-worktree-gc` (~350 worktrees) all run in the supervisor's cgroup. But note the
sharper finding: the repeat global-OOM triggers (golangci-lint at 6.1 GB, compile at 2.3 GB, cass
at 2 GB) fire **inside agent scopes**, so the durable fix is per-run-scope `MemoryHigh` set by
the supervisor when `GC_AGENT_SLICE` is active, not wider wrapper adoption.

### 4.5 Missing guards

Only 12 scripts hold a flock. The worst gap: **`account-quota-warning` (armed auto-drain, every
30m) and `account-cross-drain` (every 12m) can both run `gc-capacity` drains concurrently with no
shared lock**, two movers planning against the same fleet snapshot. `gh` calls lack timeouts in 9
scripts. `mktemp` without trap in 9 scripts, with 255 `tmp.*` files in `/tmp` as live evidence.

### 4.6 Thundering herds

Six cron orders co-fire at `:00`/`:30`; only `approved-pr-automerge-packs` bothered to stagger.
`04:00` stacks the mail janitor with fleet-wide PL respawn. Monday morning is the heaviest herd
(08:30 → 11:00, including every PL fanning out DEEP_AUDIT). Cron catch-up semantics mean a
stalled floor releases a burst of deferred crons on recovery, compounding the recovery spike.

---

## 5. Agent prompts against specialist-agent practice

**What is solid**: the autonomy boundary, external-artifact gating, and +2-worker flex policy are
stated consistently across mayor, the shared fragment, and PL prompts. `graph-worker.md` is a
genuinely good worker contract (explicit failure classes, `gc.outcome=fail`,
`failure_class=transient|hard`, poll-before-drain, context exhaustion). `polecat` and
`gascity-maintenance-pl` are strong.

**Failure-path coverage is the structural gap.** Matrix across archetypes: nobody is told what to
do on `dolt unreachable at 127.0.0.1:0`, on rate-limit (one aside in one prompt), or on OOM/kill.
Those are precisely the three classes this city demonstrably loses work to. `one-shot.md` has no
failure path at all and ends "You're done. Wait for further instructions", holding a slot forever
with no drain. `loop-mail.md` has no termination condition whatsoever. A ~10-line shared
"substrate failure" fragment would close most of this for every archetype at once.

**Instrumentation gap**: `worker`, `scoped-worker`, `one-shot`, `loop`, `loop-mail`, and
`pool-worker` all close with bare `bd close <id>`, no evidence or notes requirement, producing
exactly the vague closes `close-gate-reaper` reopens. No prompt anywhere instructs recording
pushed SHAs for the pre-authorized push lanes; that duty lives only in workspace CLAUDE.md, which
is invisible from rig worktrees.

**Live contradictions**:
1. **Push authority inverted**: `mem-pl:539` says "You may NOT push, open, edit, or merge PRs,
   even for work you dispatch", and `gascity-packs-polecat:25` says "NEVER git push". Both
   contradict your 2026-07-14 carve-outs. The agents holding the authority are told they have
   none, and never receive the 3 gates or the record-the-SHA duty. The packs carve-out exists
   *because* 56/74 branches lived only on local disk and one verified fix was garbage-collected.
2. **Dead standing-rules path**: mayor:136 reads `~/.claude/rules/common/agent-collaboration.md`
   "on startup". That file moved to `~/.claude/rules-reference/` in the 07-07 restructure. The
   mayor begins every session by failing to read its own standing rules. Same for three rules
   paths in the DEEP_AUDIT fragment.
3. **`/caveman lite` is invoked at session start by 9 prompts**; the skill was pruned 2026-06-22.
4. **`pool-worker.md` may be dead**: `city.toml:680` sets `formula_v2 = true`, and under that flag
   `cmd_prime.go:352-362` gives every agent `graph-worker.md`. The two files carry contradictory
   work contracts (`pool-worker.md:47-70` mandates `bd mol current` stepping; `graph-worker.md:10-12`
   says do not use it).
5. **city-infra-pl contradicts itself**: "Survey with plain `bd`, NOT `gc bd`" (:20-29) versus
   required-first-step "`gc bd list --status open`" (:41-42), the exact command the prompt says
   returns `[]` for its store.
6. **Opposite no-match doctrines**: `gascity-maintenance-pl` says never hand back to the human;
   `gascity-packs-pl` says ask Stephanie to clarify.
7. **13 thin-PL `agent.toml`s set `suspended = true` while city.toml sets `mode = "always"`** for
   the same templates, with no comment recording which wins.

**Stale**: 8 of 22 identity rows in `dedicated-project-lead.template.md` point at agents that load
their own templates; `feat/import-slack-pack` PR#8 language (merged); "idle_timeout (2h)" comments
against a 6h value; dangling "see Branch policy below" references to a section that does not exist;
`providers.codex-mayor` referenced by no agent; three `skills.toml` allowlists naming skills that
do not exist.

No Fable pins remain in city config or agent tomls. **But `account3` and `account4`
`settings.json` still pin `claude-fable-5[1m]`** (accounts 1/2/5 and the default home are correct).
Managed sessions are shielded by the explicit `--model opus` baseline; any interactive session in
those homes re-wedges.

---

## 6. Temporal: where it helps, where it does not

You chose lean-in, so this section is a migration sketch rather than a verdict. The evidence
constrains the sketch sharply, and I am not going to soften that: the measured 30-day incident
record is dominated by memory exhaustion, a dolt saturation storm, silent order death, and
source-level dispatch bugs. **Durable execution structurally addresses none of the top three.**

The pilot's own record, which is unusually honest: the maintenance cycle does 44s of work per
120m window with zero `SkippedOverlap`, no long-lived state, and nothing to survive. Temporal's
fail-closed work-layer semantics, when chaos-injected (gc-4zf.4), produced a poisoned `pending`
claim refused forever, an orphan bead, and zero escalation. The fix made failures loud, but
closing the loop required `temporal-soak-check`, a bash scan on a cooldown order. The epic's own
sentence: *the engine made the failure loud; a scan made it converge.* Two residues remain open
(9.5a orphan unnamed, 9.5b tombstone never swept).

Meanwhile every durable wakeup already in this city reduces to (durable timestamp in store) +
(periodic scan). The rate-limit `quarantined_until` + reconciler sweep is the existence proof: it
survives process death with no engine.

| Failure mode | Temporal fit | Recommendation |
|---|---|---|
| OOM / host death | No — adds an OOM-able service; durability cannot stop a kernel OOM | Fix `resource-sweep`'s metric blindness; keep Temporal out of the memory budget |
| Runaway sidecars | No | Source fix already worked (reaps fell ~90% on 07-14) |
| Supervisor restarts | No — systemd restarts in seconds | No change |
| Dispatcher wedges | Marginal — heartbeats are attractive but the watchdog already kills and respawns | Tune the 5h idle threshold |
| **Order stalls / silent de-registration** | **Marginal, and the only live candidate** | The irreducible gap is *where the scan-of-scans runs*; it must survive order-system death. Price Temporal against a `systemd --user` timer running the existing `mayor-pattern-miner` logic. Evidence favors the timer: zero new state, zero new ops surface |
| Stuck molecules | No, despite being strongest on paper | Observed causes are a compiler bug (`compile.go:325`), a missing idempotency gate, and missing scans. Those are bug fixes and SQL questions, not durability gaps |
| Dolt outages | No — a workflow engine cannot time out queries inside a server it does not run | gc-2h7b source fixes, correct lane |
| Rate-limit / accounts | No — already the model example of engine-free durable wakeup | No change |
| Lost pushes | No | Policy + `work-landing-reaper`, both done |

**Where lean-in actually cashes out.** Two properties are genuinely demonstrated on this box:
crash-survivable exactly-once external mutation, and failure-domain independence. The first
matters for **gated GitHub mutations** (merges, PR opens, pushes) — a handful per day, but they
are exactly your worst loss class, and `mol-pr-merge-only` currently performs an unguarded
external write. That is the honest Temporal lane: **not the order floor, not the molecule engine,
but the external-mutation boundary.** Migrate `approved-pr-automerge`, the push steps of
`mol-pr-from-issue`, and the merge step of `mol-pr-merge-only` onto durable activities with
idempotency keys; leave everything else on the level-triggered scan mesh.

Budget: single-binary dev-server with SQLite persistence under a `MemoryMax`'d slice, per the
fixed-box constraint. Given §0, **any Temporal expansion must be gated on the memory arithmetic
being fixed first**, not run alongside it.

Decide the P1-P5 maintenance-cycle question at the 07-23 gate on the soak-check record (currently
12 cycles/24h dispatching beads, 1 FAILED cycle escalated 07-21).

---

## 7. Decisions I need from you

**Decision 1 — canonical dolt placement (urgent, blocks stability).** Move the running dolt out
of `gascity-agents.slice` now, or wait for the durable unit-level fix? Moving live is a
`cgroup.procs` write I can do in seconds and it removes the bead store from the agent kill
budget; the downside if it goes wrong is a bead-store outage for 23 rigs. My recommendation:
do it now, because dolt has been killed 3× today and each kill freezes the order floor. Filed as
**gc-qaid** (P1) either way.

**Decision 2 — demand reduction.** The slice is structurally overcommitted; no leak reaping fixes
it. Reducing warm-by-config sessions is a city.toml topology change, which your change-control
convention puts behind your per-action approval. Options: drop `min_active` floors on suspended-rig
pools, retire the 13 always-on-but-suspended thin PLs from `mode="always"`, or shrink
`enterprisebench-worker` (floor 10, the largest single consumer). Note the trap: `pool.go:126,131`
returns `sp.Min` on any scale-check error, so a floor of 0 means a failing check silently yields
an empty pool.

**Decision 3 — postgres.** 15.0 GB and growing past the ledgered 13.7 GB, unbounded,
`ManagedOOMPreference=none`. `shared_buffers=16GB` is the real lever, and the companion brief
retracted an earlier "just halve it" recommendation because the pgvector/HNSW workload wants the
index resident. This is a genuine tradeoff on your ledger, not obvious waste, but it is the box's
OOM gate and it now needs a call.

---

## 8. Wave 1 (safe, reversible; dispatching now)

1. `git init` the workspace, `.gitignore` (`.gc/`, `.beads/`, `.env`, `*.bak*`, stray `gc-*`
   worktree dirs), baseline commit, then delete all **156** `.bak*`/`.PROPOSED` files
   (agents 45, bin 36, orders 16, formulas 14, prompts 5, city.toml 16 at root, plus 3 stray).
   **Blocker to check first**: `.env` (mode 664) holds a live `CLAUDE_OAUTH_TOKEN`; it must be
   ignored before the first commit. `hooks/test-r2-gitleaks-jj.sh` contains an intentional fake
   AKIA key needing a `.gitleaksignore` entry.
2. `.gc` retention policy: ~6 GB reclaim (delete 7 June janitor baks, TTL the compact baks and
   event archives, gzip `jsonl-archive` and reconciler segments, delete the 1.35 GB of
   uncompressed dispatcher rotation remnants, sweep lock dirs >7d).
3. Log rotation list-file covering `/home/ds/gas-city/.gc/*.log` + `codegraph-sync.log`.
4. `city-selftest` timeout 660s → 1020s, or better: compute the budget from the glob count so the
   invariant cannot silently break again.
5. Wire the 20 pytest suites into `city-selftest` via a `bin/pytest-suites.test` shim.
6. **needs-recovery reaper**: sweep `status=blocked` beads carrying `gc.needs_recovery=true` or
   `review_verdict=escalate` → mayor + `#gascity-maintenance`. Both states were designed to be
   reaper-visible and neither has ever had a consumer (§1.1).
7. `pour = true` on `mol-pr-from-issue`, both copies (§1.3).
8. Port the `mol-pr-iterate` worktree stanza to `mol-pr-merge-only` and `mol-pr-revert` (§1.4).
9. `gascity-packs-polecat` escalation: replace dead-letter mail with `terminal-worker-escalation`
   (§1.5).
10. Fix the dead rules paths (mayor:136, fragment:156-158) and drop `/caveman lite` from 9 prompts.
11. `stall-watch` detector-failure signal: distinguish empty findings from detector crashed (§4.2).
12. `account3`/`account4` `settings.json` fable pins → `opus[1m]`.

## 9. Wave 2 (structural; filed, held for your green light)

Order cluster merges (§2.1), `mol-pr-base` extraction (§2.4), prompt fragment migration (§2.3),
`github_mirror_common` consolidation and the Slack helper + channel registry (§2.2), roster prune
(§3), packs SSOT split with deletion of shadowed copies (note: local `mol-pr-from-issue` is
*newer* than the pack by 44 lines, local `mol-pr-iterate` is *older* by 248 — reconcile both
directions before deleting either), per-run-scope `MemoryHigh` (§4.4), `mail-dupe-dedupe` off the
full-parse path, docs/skills ownership split (§ config audit: mayor is `amp`, not `claude-5`, in
4 files; `gc-binary.md:30` path; maintenance-cycle "retired" in 7 skills).

## 10. Wave 3 (Temporal external-mutation lane)

Per §6, gated on the memory arithmetic being fixed and on the 07-23 soak gate.

---

# APPENDIX — what was actually applied, 2026-07-21

Written after the audit, once Stephanie redirected: *"the city isn't really
functioning well as is anyway so optimize for long term robust fixes dont worry
about what issues you might cause in the interim with throughput we are pausing
to focus on cleaning up."* That authorized both gated decisions in §7 and shifted
the order of work to stabilize-first.

## A. The box was stabilized

| Metric | Before | After |
|---|---|---|
| gascity.slice | 31.1 GB (pinned at its 32 GB max) | 13.3 GB |
| MemAvailable | 6.5 GB | 20 GB |
| memcg OOM kills | 24 → 37 during the audit, 2-7 per 10 min | 37, flat for ~50 min |
| canonical dolt | killed 3x (07:52, 09:28, 09:42) | one process, 30+ min uptime |
| sessions | 87 | draining through 48 |
| order floor | hours behind | firing on cadence |

Sequence: rebound the supervisor's dolt-port drop-in 29621 → 29620 to match
`sql-server.info` (breaker went open → closed on every rig, 51,155 stale events
stopped); reaped 4 zombie test dolts with deleted cwds; zeroed all 39 pool
`min_active_sessions`; suspended 23 city-level PLs via `gc agent suspend`;
suspended the 20 active pack-defined pool agents via `[[patches.agent]]`
(`gc agent suspend` refuses pack-defined agents); closed the surplus sessions.
Mayor left running.

**The pause is reversible and its procedure is written down**: bead gc-x77t is
the resume checklist. Do not resume piecemeal — floors, PL suspends, and the
city.toml patch block were changed together. And do not resume before the memory
arithmetic is fixed, or the same churn returns immediately.

## B. Git is now the change-control layer

`git init` + baseline (778 files; `.env`, `.gc/`, `.beads/` ignored — `.env`
holds a live `CLAUDE_OAUTH_TOKEN` and was verified unstaged before the first
commit). The 142 `.bak`/`.PROPOSED` files were **committed once and then
deleted**, rather than deleted outright as the approved plan allowed: they were
the only record of prior versions, and throwing that away at the moment of
adopting a tool that could keep it would have been the wrong trade. Content is
recoverable with `git show 4d457f8:<path>`.

`cityops-city-change-control`'s bak-before-flip step should now read
commit-before-flip.

## C. Fixes applied, each verified by execution

1. **`pour = true` on `mol-pr-from-issue`** — closes the armed RootOnly strand
   (§1.3). Formula parses, 7 steps.
2. **`needs-recovery-reaper`** (new script + order + 12-assertion test) — sweeps
   the two states designed to be reaper-visible that had no consumer (§1.1).
   **First run found 27 stuck beads across 8 stores**, invisible for over a day.
   The one that justifies it: `EnterpriseBench-rryas.12`, `review_verdict=pass`
   with `land_failed_reason=ff_only_collision` — work that passed review, could
   not land, and nothing was watching. Triage tracked in gc-82r4.
   Surface-only by design; fails loud on an unreadable store rather than
   reporting a clean scan. Mutation-tested: removing fail-loud breaks 3
   assertions and the suite exits 1.
3. **Log rotation** — found a second silent defect: the glob
   `runtime/*--control-dispatcher-trace.log` **matched zero files** because the
   real names are `*--core.control-dispatcher-trace.log`. Dispatcher trace
   rotation had never run. Replaced with directory-wide globs. First execute:
   8 files, ~500 MB.
4. **`city-selftest` budget enforcement** — the "a 12th suite MUST raise timeout"
   instruction had been breached to 16 suites against a 660s ceiling, for the
   second time. Timeout → 1200s, and the script now performs its own preflight
   from `CITY_SELFTEST_BUDGET`, escalating when the suite count outgrows the
   order timeout. Verified firing at budget=100 and silent at 1200.
5. **20 orphaned pytest suites wired in** (170 tests, invoked by nothing — no
   order, cron, or CI). They covered `completion-reconciler`, which mutates bead
   state every 15m and was found *failed*. All green; now watched.
6. **Prompt fixes** — `gascity-packs-polecat` escalated into a dead-letter
   mailbox on every blocker (ported the canonical `terminal-worker-escalation`
   block); `/caveman lite` removed from 9 prompts (skill pruned 2026-06-22);
   dead `~/.claude/rules/common/` paths repointed to `rules-reference/` (the
   mayor had been failing to read its own standing rules at every session start).
7. **Fable pins** in `account3`/`account4` `settings.json` → `opus[1m]`.
8. **.gc retention** — 8.5 GB → 5.1 GB.

Suite count 16 → 18, all green.

## D. One audit finding did not survive verification

The prompt audit reported that `gascity-packs-polecat`'s note about slack-pack
living on `feat/import-slack-pack` pending PR #8 was stale, "merged, on main".
Checked before editing: the branch still exists, `c0894a3` is not an ancestor of
`origin/main`, and `origin/main` carries `slack-channel`/`slack-full`/
`slack-mini` rather than `slack-pack`. **The prompt's claim stands and was left
alone.** Recorded because the same class of error — a confident secondhand claim
about another repo's state — is what the evidence gate for cross-repo assertions
exists to catch.

## E. Still open

Decisions 2 (demand reduction as a permanent posture) and 3 (postgres
`shared_buffers`) in §7 remain yours. The pause bought headroom; it did not
change the arithmetic. Waves 2 and 3 are filed and held: gc-28w2, gc-5kgl.
