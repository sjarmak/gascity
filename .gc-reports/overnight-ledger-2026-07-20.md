# Overnight operations ledger — 2026-07-20

## Scope

- Tight internal operations only: investigate, repair, verify, dispatch, and
  keep maintenance loops moving.
- No pushes, PR creation, merges, review posting, automerge, destructive
  cleanup, broad restarts, or worker-pool expansion without their existing
  explicit gates.
- Rank active corruption first, then fleet-wide stalls, broken verification
  gates, and cheap unblocks.

## Verified complete

- Pgvector ownership hygiene: Stephanie ran the authorized correction.
  `/usr/lib/postgresql/16/lib/vector.so` is `root:root` mode 755 and
  `/usr/share/postgresql/16/extension/vector.control` is `root:root` mode 644.
  No apt reinstall or version change occurred.
- F04 Temporal scheduled external-write containment: closed bead `dr-iw7`.
  Reviewer outcomes are proposal-only and all author paths are
  `auto_push=false`, branch-ready-only. Mayor independently reran the focused Go
  policy test: PASS. No GitHub mutation occurred.
- F08 Temporal fallback: closed diagnosis `dr-r9z` and repair `dr-hvf2`. The
  two workflow failures and orphan `gc-8d65` are durably diagnosed without
  recovery mutation. Slack failure now sends one independently deduped durable
  mayor mail while Slack remains retryable. Mayor independently reran the shell
  suite: 24 passed, 0 failed.
- F13 dispatcher false positives: closed bead `dr-l9hy`. Root cause was mixed
  local-DST `mktime` semantics adding exactly 60 minutes. UTC parsing, exact
  path evidence, current/stale fixtures, and a live read independently passed;
  the live dispatcher was not restarted or killed.
- F09 P1 destructive-writer serialization: closed bead `dr-6qrb`. Login-wedge
  and Dolt-flatten now hold fail-closed locks across their complete operations;
  contenders are audited skips. Mayor independently reran py_compile, syntax,
  and the concurrent fixture: exactly one login enumeration/kill writer and one
  flatten boundary entrant. Existing guards are unchanged; no live destructive
  run occurred.
- F10 P1 delivery-acknowledged dedup: closed bead `dr-cut4`. Help-request
  metadata advances only after successful mayor delivery; blocker-close state
  advances per successful recipient. Mayor independently reran syntax,
  shellcheck, resolved-order reads, and the hermetic failure/success fixture:
  PASS. No live notification occurred.

## Active overnight actions

1. **EnterpriseBench pool deadlock** — `gc-520229` assigned recovery to
   `enterprisebench-pl` after live peeks confirmed all ten workers parked at
   prompts. Seven pinned sessions contain task state or dirty/staged artifacts;
   preserve in the owning session, release claims before redispatch, and prove
   one fresh routed job advances. A direct nudge timed out at 22:50 EDT; do not
   retry blindly. The durable mail remains the handoff, and the next mayor wake
   must verify that the PL read it before treating recovery as in flight.
2. **Packs PR #217 fix-loop worktree** — `gpk-lvmti` plus molecule `gpk-bx0ht`
   owns the local request-changes loop. Independent verification found the
   current claim on `gascity-packs-polecat-gc-519701` has
   `gc.work_branch=policy/rc-last-resort-take-the-good`; its legacy checkout is
   detached at `upstream/main@56d07c53`, not the required
   `bd-gpk-xp7s6@c4e62fa`. The worker released the claim under
   `hold:invalid-worktree`; its release artifact records no edits, commits,
   branch operations, pushes, or GitHub calls.
   `gc-520740`/`gc-520781` assigned `gascity-packs-pl` to repair and
   independently verify the binding. Completed artifact
   `.gc/pr-pipeline/bindings/gpk-lvmti-c4e62fa-binding.json` records one clean
   worktree, `bd-gpk-xp7s6@c4e62fa`, aligned canonical+legacy metadata, zero
   live sessions, and exact remote-head match. Mayor independently rechecked
   symbolic ref, SHA, cleanliness, registration count, remote ref, and bead
   state. A subsequent handoff to `gc-519689` failed: the worker's persistent
   session cwd differed even though a shell-local `cd` checked the target. The
   worker was interrupted and released without edits or external actions, but
   claim/release rewrote canonical `gc.work_dir` and reintroduced stale session
   identity. `gpk-lvmti` is open/unassigned under
   `hold:session-workdir-mismatch`; `gc-520855` directs packs PL to restore the
   verified metadata and stop. No further dispatch or GitHub action.
3. **Rig-agnostic PR maintenance pipeline** — `gc-520317` and scope correction
   `gc-520333` assigned the gascity-owned side to `gascity-maintenance-pl`.
   `gpk-6n7x2` plus workflow `gpk-ul2cl` owns F06 with
   `gascity-packs-pl`; formulas must parameterize or infer repo, identity,
   worktree root, artifact namespace, and remote policy, with gascity as a
   compatible default and multi-project tests. `mol-pr-iterate` coordinates
   with `gpk-lvmti`; `mol-pr-triage` waits for PR #217's final SHA.
4. **Packs local iterate/review loop** — `gpk-lvmti` is the durable owner and
   is currently held for the binding defect in item 2. Once the exact-head
   checkout is independently verified, `gascity-packs-pl` is to fix the
   request-changes findings, generate a Codex artifact on each new exact SHA,
   and iterate until PASS or a concrete blocker. No push/open/merge/automerge.
5. **Known city-infra issues** — after F04/F08/F09-P1/F10-P1/F13 closed,
   F10 P2 `dr-cki7` is also closed and independently verified: total failure
   and both partial-delivery directions retry only missing recipients, full
   acknowledgement becomes quiet, and legacy state stays compatible. Remaining
   corrected-scope beads are F09 P2 mirror/codegraph diagnosis `dr-p3f4`, F11
   runtime order census `dr-s7am`, and F12 atomic dependency conversion
   `dr-u0gu`. Keep verified
   Dolt, pool-liveness, storage prevention, and graceful model-pin rollover work
   moving with reversible in-floor fixes.
6. **Orders/formulas pipeline audit** — durable report:
   `.gc-reports/pipeline-orders-formulas-audit-2026-07-20.md`. P0 repairs
   are now verified as owned: F01 `gc-wc3f` P0, F02 `gc-nfqp` P1, F03
   `gc-6xzc` P1, and F07 `gc-mroa` P1. Each has top-level owner, assignee and
   `owner_role=gascity-maintenance-pl`, and
   `external_action_authorized=false`. F05 read-only formula inventory is
   `gpk-0oj72`; F06 portability is `gpk-6n7x2` plus `gpk-ul2cl`. Temporal
   policy and watchdog repairs are `gc-520516` and corrected `gc-520530`;
   fail-open shared-writer audit is corrected `gc-520532`.
   F01 is now immediately contained: snapshots
   `orders/approved-pr-automerge{,-packs}.toml.bak-fail-closed-codex-auth-20260720T034548Z`
   preserve the prior config, both live orders have `--apply` removed, and
   `gc order show` verifies both are dry-run. `gc-520664` tells the owner that
   exact-head Codex PASS is quality evidence, not action permission; separate
   current merge authorization remains mandatory before reactivation.
   `gc-520783` directs `gascity-maintenance-pl` to implement P0 `gc-wc3f`
   first and report its negative-test artifact before moving to F02.
   Correction at 04:30Z: the claimed dedicated checkout
   `/home/ds/gascity-worktrees/polecat-6-wc3f` is detached at `92119a5` and
   does not contain the F01 target. The target is loose operational source at
   `/home/ds/gas-city/bin/approved-pr-automerge`. Worker `gc-520826` reported
   no edits and was interrupted. `gc-wc3f` is blocked/unassigned under
   `hold:wrong-source-layer`; `gc-520895` directs the PL to rebind to a
   persistent `/home/ds/gas-city` session, snapshot first, keep both orders
   dry-run, and produce the full negative-test artifact before F02.
   Correction at 04:48Z: the city child crossed the edit boundary before its
   interrupt and briefly installed an unaccepted live candidate plus a pure-gate
   test. Independent post-hold verification now confirms live
   `bin/approved-pr-automerge` is byte-identical to snapshot sha256 `b226dc9e…`,
   the unaccepted live test is absent, and both order files still omit `--apply`.
   Snapshot `.gc-snapshots/F01-20260720T044531Z/` preserves the source and
   orders; the rejected candidate is preserved under
   `/home/ds/gas-city-f01-dev/rejected-candidate-live-20260720T045010Z/`.
   Independent review is
   `request_changes`: the 34/34 pure-gate pass omits trusted issuer/provider
   provenance, atomic `--match-head-commit`, the structured producer contract,
   side-effect-free dry-run, and mocked-main integration. `gc-wc3f` and F02
   `gc-nfqp` are blocked; revision is confined to the isolated dev copy until
   an independent integration/security review passes.
   Round 2 artifact `/home/ds/gas-city-f01-dev/F01-revised-test-artifact.md`
   sha256 `6703c3d6…` independently reran 67/67 standalone, pytest, and AST
   checks successfully, but review remains `request_changes`. Exact allowlisted
   identity strings in agent-writable records still pass as provenance; an
   authorization is replayable and accepts unbounded future expiry; armed
   draft/conflicting/unreadable PRs can escape disarm and failed disarms exit
   success; mutation timeout can abort before audit and later cleanup. Mail
   `gc-521208` returns these four blockers to the owner. Live bytes and order
   containment remain unchanged; F02 stays blocked.
   New F14 `gc-lcue` P1 owns the claim/release binding defect exposed by
   `gpk-lvmti`; it stays behind F01 but is the prerequisite for another pooled
   PR #217 attempt unless a supported persistent-cwd session path is proven.
   `gc-520863` queued that ordering to `gascity-maintenance-pl`.
7. **Fork PR workflow approvals** — content skimmed at each current head per
   the standing fork-CI carve-out. Approved 20 queued runs for safe PRs #4315,
   #4335, #4388, #4416, #4427, #4428, #4438, #4443, #4444, and #4460.
   Deliberately did not approve #4293 because it adds process signal/kill
   execution, and did not approve #3912 because no current-head
   `action_required` workflow run was discoverable.
8. **`gc-ma53` completion claim corrected** — `gc-520361` said the raw step
   was open/routed, but a fresh bead read showed `status=blocked`, no assignee,
   `gc.routed_to=null`, and unchanged terminal labels. `gc-520487` sent the
   contradiction to `city-infra-pl`; completion now requires a fresh open/routed
   bead artifact plus a live claim/advance.
9. **Code-intel stranded branch queue** — worker reports `gc-520387` and
   `gc-520411` established no claimable pool work and branch-only premises
   surfacing as ready work. `gc-520507` directs `code-intel-pl` to block/defer
   those follow-ups, stop empty-worker churn, and prepare one exact-diff,
   current-main, independently reviewed landing decision for the `bd-fze`
   10-commit stack and `bd-xeh` at `42e9a9e`. No push is authorized.
10. **Code-intel Render-auth spawn loop contained** — `gc-520932` reports
    `render whoami` remains unauthorized, the local CLI credential expired on
    2026-07-07, and no `RENDER_API_KEY`/`RENDER_TOKEN` is present. The worker
    reversibly deferred both blocked molecule roots and their steps through
    2026-08-03, stopping repeated empty-worker spawns without closing or
    landing anything. `code-intel-pl` was directed to verify the defer set and
    keep the lane quiet until authentication is restored.
    Correction from `gc-521016`: deferring the beads did not stop the pool's
    empty-worker drain/respawn loop; `gc-521005` spawned with zero routed or
    claimable work. `gc-521028` directs the PL to preserve a stable idle floor
    or use the narrow reversible empty-pool control, and `gc-521029` assigns the
    generic min-active-versus-drain recurrence to city infra. The earlier
    containment claim is not accepted until a no-respawn artifact exists.
    `gc-521054` then confirmed a third empty spawn (`gc-521018`). A canonical
    `gc agent suspend code-intel-digest-worker` attempt failed without changing
    state because the agent is pack-defined and requires a city-level patch.
    That topology change is human-gated, so no patch or whole-rig suspension
    was applied. Pre-change agent config was snapshotted at
    `agents/code-intel-digest-worker/agent.toml.bak-empty-pool-spawn-loop-20260720T045949Z`.
    `gc-521138` confirms a fourth empty spawn (`gc-521121`). It filed `bd-fn9`:
    local code-intel `main` is four commits ahead of every remote and includes
    an XFF security fix, while no `origin/production` branch exists despite the
    Render declaration. `gc-521221` directs the PL to preserve state and produce
    one exact-range test + Codex-inclusive review artifact; no push or Render
    change is authorized.
11. **hvir `gc` read-latency investigation completed locally** — gascity bead
    `gc-7wnu` root-caused the 2–3 second reads to API misrouting under supervisor
    mode, which forced a serverless fallback that repeatedly parsed a 61.4 MB
    bead snapshot and performed tmux N+1 probes. Branch `bd-gc-7wnu` at
    `1b2c53acd` reduced production-shaped medians from 2.39s to 0.81s for
    session list and 2.11s to 1.00s for rig list. `gc-521030` queues the exact
    branch into the local gascity PR pipeline after F01; no push or PR action is
    authorized.
12. **Dispatcher reset wave converted into common-cause investigation** — the
    watchdog killed seven control dispatchers from 01:17:25–01:19:05 for the
    same routed-control-set remaining unconverted for at least 90 minutes.
    Five active dispatcher traces were fresh after the wave, but reset alone is
    not conversion proof. `gc-521229` directs city infra to map killed IDs to
    rigs, identify exact control beads, verify replacements, and prove whether
    conversion resumed. No additional kill/restart/threshold change was made.

## Pending Stephanie decisions

1. **Code-intel stranded branch stack** — after `code-intel-pl` produces the
   exact diff, current-main rebase result, and independent review: land, rework,
   or drop `bd-fze` plus `bd-xeh`? Raised 2026-07-20 from `gc-520387` and
   `gc-520411`.
2. **Durable Render authentication for code-intel workers** — create a Render
   API key and install `RENDER_API_KEY` in the worker environment, or retain
   interactive monthly CLI re-authentication? The queue remains deferred and
   must not be resumed until one authentication path is verified.
3. **Temporary code-intel worker-pool containment** — authorize a city-level
   patch or whole-rig suspension until Render authentication and branch-landing
   decisions unblock claimable work, or require one floor worker to remain idle
   instead of draining? Direct agent suspension is unavailable for the
   pack-defined worker, and the current floor/drain loop has spawned at least
   three empty sessions.

## Signals deliberately not acted on

- No automatic PR publication or merge: the user requested maintenance health,
  not external GitHub mutations.
- No forced session recycling: active/dirty/pinned worker state must be
  preserved before lifecycle recovery.
- No dashboard worktree or branch deletion: read-only audits identified safe
  candidates, but deletion remains separately gated.
- No dispatcher restart for `gc-520360` or repeated `gc-520564`: the supposedly
  silent city trace had fresh `serve wake-event`/`wake-sweep` activity through
  23:22 and 23:52 EDT, after both alerts. Re-applying wedge recovery would
  interrupt a live dispatcher. `gc-520685` assigns the sensor's path/age bug to
  `city-infra-pl`; this signature is verify-first until its test artifact lands.
- No process cleanup for `gc-520396`: MemAvailable had recovered to 11.84 GiB;
  the remaining MCP fan-out count alone does not justify blanket-killing
  processes that can include active interactive sessions.
- No process cleanup for `gc-520869`: MCP counts were 65/106, but MemAvailable
  was 13.96 GiB and supervisor RSS was 2.08 GiB. Count-only threshold breaches
  do not distinguish active sessions from leaks; blanket cleanup would risk
  killing live work. Continue normal sensor monitoring instead.
- No process cleanup for `gc-521228`: counts remain 65/106, but current
  MemAvailable is 15.63 GiB, load is 9.64/10.55/13.32, and supervisor RSS is
  1.46 GiB. The count-only signature still cannot distinguish active sessions
  from leaks; blanket cleanup would kill live work without relieving pressure.
- No re-sling or closure for repeated Temporal fallback mail `gc-521118`: fresh
  reads confirm it reports the already-diagnosed 14:00/16:00 failed workflows
  and still-open unrouted `gc-8d65`. That bead's embedded reviewer policy is
  stale—it permits contributor-facing request-changes/comments, contrary to the
  repaired proposal-only Temporal contract. Replaying it could perform an
  unauthorized GitHub action; closing it remains a separate recovery decision.
  The durable fallback itself worked as designed despite Slack failure.

## 10:55 EDT mail disposition

Every message in the 68-message unread set was assigned one durable outcome
before acknowledgement.

### Actions committed

1. **Pool-liveness false positive** — `dr-8fgf` owns `gc-521587` and its
   resolved follow-up `gc-524113`. Fresh peeks showed all three Codeprobe
   workers in coherent current work or review gates, so none was recycled or
   nudged. The repair must distinguish prompt-waiting/current work from a
   corpse before authorizing recovery.
2. **Slack audit delivery** — `dr-vgtg` owns `gc-524008`, `gc-524055`, and
   `gc-524088`. `gc slack status` timed out, the guessed
   `gascity-slack-adapter.service` unit does not exist, and three durable audit
   reports need replay only after the real route/service/bindings are proven.
   No Slack post was retried.
3. **Dispatcher conversion common cause** — `dr-6v9f` owns informational
   auto-resets `gc-521369`, `gc-521372`, `gc-521958`, `gc-521962`,
   `gc-521963`, `gc-521964`, `gc-521969`, `gc-522183`, `gc-522185`,
   `gc-522186`, `gc-522190`, `gc-522793`, `gc-522797`, `gc-522798`,
   `gc-522799`, `gc-523022`, `gc-523025`, `gc-523026`, and `gc-523029` as
   one read-only common-cause investigation. No extra kill, restart,
   force-spawn, re-sling, or threshold change was made.
4. **Packs routing/binding deadlock** — the packs PL was given single ownership
   of `gc-521451`, `gc-522928`, `gc-523176`, `gc-523278`, `gc-523334`,
   `gc-523764`, `gc-524014`, and `gc-524048`. Fresh state proves dispatcher
   session `gc-522899` is active despite a timed-out partial status probe;
   restart is therefore declined. `gpk-lvmti` remains held behind F01/F14 and
   `gpk-r14la` must be unrouted/held until its PR overlap, dead affinity,
   invalid worktree, detached branch, and invalid test command are reconciled.
   Because hold labels are not honored by current routing, both were still in
   `bd ready`. Mayor converted `gpk-lvmti`, `gpk-r14la`, and broken root
   `gpk-ul2cl` to blocked, removed routing/affinity metadata that could re-serve
   them, and preserved the exact PR #217 branch/head/worktree binding. A fresh
   ready query contains none of the three. No PR #217 edit or GitHub action was
   authorized.
5. **Held-bead routing leak** — new P0 `gc-5cgt` owns `gc-524101`. It must make
   `dispatch-blocked` fail closed in both routing and workflow load-context;
   `aoa-wbn7` and the other 13 held AOA beads remain held.
6. **Direct-plus-formula double dispatch** — new P1 `gc-xgo4` owns
   `gc-523789`; the redundant audit worker stood down, so no rerun occurred.
7. **RED agent-guidance audit findings** — `gc-523832` produced owned repairs
   `aoa-847s` and `codeprobe-zavs`. Both are documentation-only and require
   every path/command to be verified against the live tree.
8. **SciX resume gate** — `gc-524067` was answered durably by mail
   `gc-524105`. Disk is safe, but the preflight failed on 6.7 GiB available
   memory, exhausted swap, and load 44.98/37.99/64.53. The rig remains
   suspended and the production request remains withdrawn.
9. **Executive status** — directive `gc-523821` was executed in the vault
   input with an updated seven-field citywide block and no operational detail.
10. **AOA live-config process violation** — `gc-524090` reports a correct-looking
    `cargo test --workspace` city config change, but no immediately preceding
    snapshot or adjacent change-control comment exists. Mail `gc-524126`
    freezes further AOA config edits and requires one durable ratify-or-rollback
    decision; the concurrent change was not reverted.
11. **EnterpriseBench recovery** — `gc-524104` and `gc-524112` prove one fresh
    worker is advancing after targeted corpse cleanup. No further recovery or
    blanket re-sling is warranted.

### Human decisions preserved

1. **Code-intel publication gate** — mails `gc-521289`, `gc-521396`,
   `gc-521432`, `gc-521456`, `gc-521497`, `gc-521777`, `gc-521801`,
   `gc-522078`, `gc-522129`, `gc-522212`, and `gc-522305` remain owned by
   `bd-fn9`/`bd-fze`; no push is authorized. Decision, after one exact-range
   Codex-inclusive review packet: authorize a fast-forward of reviewed local
   `main` to `origin/main`, yes or no?
2. **Halfvec startup contract** — `gc-521601` remains on `bd-f0m`: should the
   halfvec assertion be scoped to the local curation path instead of blocking
   hosted-service startup on an unused production column?
3. **Nightly checkout contract** — `gc-522247` remains on `bd-bdr`: should the
   systemd ingest run from a dedicated checkout pinned to `main` rather than a
   dirty primary checkout?
4. **Decisions PL onboarding** — `gc-524058` remains blocked on one human
   answer: what outcome and operating boundaries belong in the Decisions
   rig's project brief?

### Deliberate non-actions

- Resource samples `gc-521591`, `gc-521871`, `gc-521917`, `gc-522310`,
  `gc-522646`, `gc-522819`, `gc-522980`, `gc-523151`, `gc-523316`,
  `gc-523468`, `gc-523632`, `gc-523826`, and `gc-523946` do not identify
  process ownership. Blanket cleanup could kill active managed or interactive
  work, so they remain telemetry only.
- Digest `gc-522909` does not authorize a blanket push: its summaries are
  truncated and the individual halted, branch-ready, and investigation beads
  remain the durable source of review/current-SHA truth. Acting from the digest
  would bypass the PR pipeline.

## 11:08 EDT mail disposition

1. **SciX-labelled memory pressure** — `gc-524120` is not evidence that the
   suspended SciX rig resumed. Four ~570 MiB `scix.mcp_server` children were
   mapped to one live SciX audit session and three AOA Claude scopes because
   `/home/ds/.claude/.mcp.json` enables that MCP globally. A separate PID
   `3957760` (`gc nudge poll ... scix-experiments-pl gc-507722`) is consuming
   about 50% CPU and 536 MiB inside the supervisor cgroup while the Amp PL is
   live. City infra was assigned exact sidecar-lifecycle and attribution
   diagnosis before any containment. No MCP or sidecar was killed, and SciX
   remains suspended.
2. **Hard-fail re-serve loop** — `gc-524122` adds a second failure mode to P0
   `gc-5cgt`: `gc.outcome=fail` plus `gc.failure_class=hard` is not terminal,
   so `aoa-7k78` is re-served as `existing_assignment`. AOA PL was directed to
   cancel/unroute molecule `aoa-oe6o` without closing the load step or target;
   `aoa-wbn7` remains `dispatch-blocked`. The AOA-side durable defect is
   `aoa-g3rp`. Containment then completed: the root and all remaining steps are
   blocked, unassigned, unrouted, and cancelled; finalize did not advance;
   `aoa-7k78` remains unclosed hard-fail evidence; and the one residual worker
   was suspended only after it refused and drained. The respawn loop stopped.
3. **Dead-affinity terminal escalation** — duplicate mails `gc-524136`,
   `gc-524138`, and `gc-524140` were acknowledged/disposed once for source
   `rig:gascity:gc-nsc1`. `gc-nsc1` remains blocked + dispatch-blocked; a
   sibling drain-ack was explicitly refused. The dead-owner finalization and
   foreign-affinity re-serve defect is appended to `gc-xgo4`; no session or
   worktree was reaped.
4. **Slack status source repair and channel decisions** — `gc-524142` proves
   the adapter is healthy and the hang is the optional events query. Packs PL
   was directed to own a one-concern local source repair through the full PR
   pipeline, branch-ready only. Intended Embertide and GEO channel IDs and any
   audit replay remain separate human decisions; no binding or post occurred.
5. **GEO research ordering** — `gc-524144` was split from channel delivery by
   mail `gc-524160`: GEO PL must record the irreversible Pattern-A-before-web-
   search choice as its own decision bead. Neither pages nor web search may
   proceed until that decision is answered. Follow-up `gc-524179` confirms
   decision bead `GEO-grg` now owns the A/B question and escalation `GEO-x2k`
   records the pause; no Slack binding was guessed or replayed.

### Infra proof completed during this disposition

- `dr-8fgf` closed with a prompt-aware liveness fixture and full passing suite.
- `dr-6v9f` closed read-only: all seven dispatcher kills shared the same
  city-wide 66-ID control set because `dispatcher-watchdog` scopes targets
  globally, then stores the identical set per session ID. Resets only renewed
  session IDs/grace clocks. The required repair is per-dispatcher target
  scoping with cross-rig fixtures; no threshold change or runtime mutation was
  made.
- `dr-vgtg` remains blocked only on local packs-source repair, intended channel
  mappings, and separate replay authorization.
- `dr-3z4k` closed the exact sidecar incident: lease evidence and the existing
  `nudge-poll-reaper --dry-run` matched only PID `3957760`; a targeted TERM
  exited in two seconds while the PL remained active, SciX remained suspended,
  and all MCPs remained untouched. Follow-through repair `dr-bfvv` owns the
  now-unblocked per-dispatcher watchdog implementation.

## 11:12 EDT capacity disposition

- `gc-524152` requested one more EnterpriseBench worker, but the effective
  pool is already 10/10 after two prior +2 expansions from 6. Another worker
  exceeds the mayor's automatic +2-per-PL authority, and the concurrent host
  sample has only 6.5 GiB available memory with exhausted swap. No agent config,
  reload, or session was changed. Mail `gc-524170` holds the Study Capsule step
  in queue and prohibits force-spawn. Human decision: authorize an exceptional
  EnterpriseBench 10→11 expansion despite the prior +4 and current host
  pressure, yes or no?

## 11:20 EDT EnterpriseBench deep-audit disposition

- The promotion-grade Study Capsule already has owner
  `EnterpriseBench-rryas.11` and workflow `EnterpriseBench-jprzj`; capsule
  implementation may continue without the denied 10→11 scale-up.
- The three critical audit defects had no separate owners. EnterpriseBench PL
  was directed to create one-concern repairs for: validated-run-only promotion
  scope; removal of the second checkpoint-count normalization; and a
  prespecified fail-closed attempt policy that cannot select the best retry and
  includes all-attempt spend. The cheap 3-arm pilot, promotion, and any full
  paid run must wait for all three plus the capsule.
- Repetition attribution, CLI-arm inclusion, and failed-health/degraded-judge
  invalidation must be separate bounded follow-ups. No pilot, paid execution,
  publication, or other external action was authorized.
- EnterpriseBench PL completed the graph: critical repairs are
  `EnterpriseBench-rryas.14` (declared-capsule-only promotion), `.12`
  (normalization), and `.13` (attempt policy); shared capsule `.11`; bounded
  follow-ups `.15`, `EnterpriseBench-k1nud`, and `.16`. Pilot `.17` blocks on
  `.11/.12/.13/.14`; full paid/promotion `.18` additionally blocks on `.17`;
  publication `.9` additionally blocks on `.18`. Fresh verification shows
  `.17`, `.18`, and `.9` absent from `bd ready`.

## 11:23 EDT resource disposition

- `gc-524225` reports 100 `mcp-remote` processes / 2.72 GiB and 63 `--mcp`
  processes / 2.54 GiB, but MemAvailable improved to 8.76 GiB, load declined,
  and supervisor memory is 1.37 GiB. Count-only matching does not identify
  ownership, so no process was killed or disabled.
- Repetition across prior samples is now owned by P1 `dr-f1pq`, blocked behind
  dispatcher repair `dr-bfvv`: map process trees/cgroups to live sessions and
  exact config layers, distinguish active servers from stale survivors,
  quantify per-session duplication, and propose a narrowly scoped
  shared/multiplexed or scoped-config fix. Its guardrails prohibit MCP signals,
  config edits, service restarts, and broader cleanup; the eventual config
  decision remains human-gated.

## 11:48 EDT Mem claim-alignment disposition

- `gc-524309` was not a capacity deadlock. The configured Mem pool has all six
  sessions present: workers 1–4 were responsive, worker 5 had just landed and
  closed earlier work, and worker 6 was idle. No force-spawn, scale-up, kill,
  restart, or broad re-sling was performed.
- Workflow roots `mem-1pxru`, `mem-qylng`, `mem-sdomj`, and `mem-tsypj` are
  orchestration containers; child-step ownership is the relevant liveness
  signal. `mem-sdomj` showed the healthy path: session `gc-507061` claimed and
  closed load-context child `mem-08zhs` before advancing.
- Direct wake/nudge prompts produced the actual leak. Worker 1 was mutating
  `mem-rk41.3.2.1` while child `mem-bj06o` remained open/unassigned; worker 2
  was mutating `mem-0ak9z` while `mem-zi0n7` remained open/unassigned and its
  session routing record pointed at the other root; `mem-j3zp1` was routed to
  idle worker 6 but had received no prompt or claim. Workers 1 and 2 received
  immediate fail-closed instructions to preserve state and continue only
  after an exact matching workflow-child claim. Worker 6 was nudged only for
  exact routed child `mem-j3zp1` / root `mem-tsypj` / target `mem-t81u1`.
- The deterministic repair is already owned by Gas City bead `gc-xgo4`; this
  Mem reproduction was appended to its evidence and sent to
  `gascity-maintenance-pl` as mail `gc-524358`. Mem PL received disposition
  `gc-524357`: do not dispatch deeper or directly nudge target IDs outside an
  acknowledged workflow-child claim.
- The exact selector failure was then reproduced. Session `gc-519704` was
  routed/work-dir-bound to `mem-j3zp1` / `mem-tsypj` / `mem-t81u1`, but
  `gc hook --claim` claimed `mem-bj06o` / `mem-1pxru` /
  `mem-rk41.3.2.1` and attempted to update nonexistent rig bead `gc-519704`.
  The worker stopped before edits. Mail `gc-524376` and the `gc-xgo4` notes
  now require a cross-root selector fixture and prohibit treating a session ID
  as a rig bead ID. Only the accidental claim is being released; no direct
  claim of `mem-j3zp1` is permitted.
- `gc-524313` remains a human policy decision. Option A is recommended but was
  not approved on Stephanie's behalf. Mem PL confirms the exact yes/no
  escalation was already posted through the correct binding at Slack message
  timestamp `1784561944.268609`; it was not duplicated. `mem-j5l9j` remains
  blocked pending the ruling.

## 11:50 EDT Temporal orphan containment

- Durable fallback `gc-524351` reconfirmed failed workflow
  `maintenance-cycle-2026-07-19T16:00:00Z` and orphan review task `gc-8d65`.
  The task was still open, unassigned, unrouted, and more than 21 hours stale.
- `gc-8d65` is now blocked + `dispatch-blocked`, unassigned/unrouted, with
  `gc.outcome=cancelled`, `gc.failure_class=workflow_failed_orphan`, and
  `external_action_authorized=false`. It remains unclosed as incident evidence
  and cannot become a new review cycle. Maintenance PL received `gc-524383`
  to carry terminal-workflow orphan disposition into the F08/Temporal repair.
  No PR selection, review, comment, approval, merge, or other external action
  occurred.

## 11:58 EDT launch-policy and repair-queue verification

- Model inheritance is fixed at the provider boundary. All six managed Claude
  providers (`claude-1` through `claude-5` plus `claude-auto`) have
  `option_defaults.model="opus"`; a live census found 47/47 Claude sessions
  launched with explicit `--model claude-opus-4-8`, with zero missing-model or
  Fable launches. A human changing an account-local `/model` selection cannot
  leak into a later managed worker launch.
- `gascity-maintenance-pl`, `gascity-packs-pl`, and `city-infra-pl` are all
  live on provider `amp-medium` with command `amp --no-ide --mode medium`.
  None is running under Claude or Fable.
- Maintenance PL acknowledged P0 `gc-5cgt` through workflow child `gc-0x7p`
  / session `polecat-gc-520627` and is applying the same exact-child gate to
  P1 `gc-xgo4`. F01/F02/F14 and PR #217 remain held; authoring is local/no-land
  and a separate exact-SHA Codex-inclusive ship gate is required before any
  publication eligibility.
- Packs PL resumed with PR #217 / `gpk-lvmti` read-only. `gpk-0oj72`,
  `gpk-6n7x2`, and `gpk-qpvvc` are now durably assigned to that PL with
  `external_action_authorized=false`; it is sequencing one non-overlapping
  local dispatch behind an acknowledged claim.
- Infra PL started `dr-bfvv` with a red hermetic reproduction and local
  implementation. Informational resets `gc-522894` and `gc-522804` were added
  as paired stable-target/replacement-clock evidence; the mayor performed no
  runtime action. `dr-f1pq` remains blocked behind this repair.
- Mem collision cleanup completed: worker 6 released accidental child
  `mem-bj06o` to open/unassigned and removed only hook-written session/worktree
  pointers; worker 1 restored its three out-of-band target metadata fields and
  is held. Worker 2 then acquired the correct child `mem-j3zp1` through the
  workflow and may continue only `mem-tsypj` / `mem-t81u1`; `mem-sdomj`
  remains the other healthy workflow. `mem-1pxru` and `mem-qylng` remain held
  pending selector repair. Mail `gc-524451` explicitly forbids session-ID
  impersonation as a workaround.

## 12:05 EDT Packs dispatch correction

- Packs PL completed durable ownership reconciliation: `gpk-0oj72`,
  `gpk-6n7x2`, and `gpk-qpvvc` are in progress under session `gc-507724`;
  the 63-PR/18-issue ledger is preserved, PR #216 is the next backlog review,
  and PR #217 plus `gpk-lvmti`/`gpk-r14la`/`gpk-ul2cl` remain blocked.
- Local Slack-status repair `gpk-wi3dh` correctly failed its pre-edit
  acknowledgement because candidate session `gc-524007` retained a forbidden
  `gpk-r14la` workdir. The attempted workflow `gpk-cceol` used `mol-do-work`,
  whose own contract explicitly provides no branch/worktree isolation. A pool
  expansion would only move that unsafe formula to a clean slot, so request
  `gc-524455` was declined pending retry through isolated authoring
  (`mol-focus-review` no-land or the established `mol-polecat-work`) followed
  by separate exact-SHA `mol-pr-ship`/Codex verification. Decision mail:
  `gc-524468`. No capacity/config/session change occurred.
- Slot 1 also had a queued instruction to close active portability bead
  `gpk-6n7x2` without implementation. It was stopped before any mutation;
  the bead remains in progress under Packs PL. Correction `gc-524474` forbids
  coordinator-bead closure by a held PR #217 worker.

## 12:13 EDT Dashboard deep-audit disposition

- The two new correctness findings have separate owned P1s:
  `gascity-dashboard-ys8i` makes run-diff Git failures fail closed, and
  `gascity-dashboard-z7u3` removes caller-array mutation during React render.
  Contract P1 `gascity-dashboard-xe6y` owns the bounded typed
  supervisor-provided `CityObservation` specification.
- Independent P2 follow-ups are `gascity-dashboard-t4s0` (per-city/path
  persistence queues), `gascity-dashboard-3pdm` (shared state categories),
  `gascity-dashboard-8qhe` (CityBootstrap boundary tests), and
  `gascity-dashboard-kj2g` (phaseMapping responsibility split). All seven are
  assigned to `gascity-dashboard-pl` with `external_action_authorized=false`.
- Systemic completed-work attribution is not a dashboard-local workaround:
  Gas City P1 `gc-2zz4` separately owns an atomic branch-ready outcome and
  exactly-once review-queue transition, assigned to maintenance PL.
- Preserved Reef work `gascity-dashboard-h5rl.8` is blocked with
  `hold:codex-review` at exact head
  `e360c52995464eadc6cfdd56fd653c540494101c`; its existing worktree was not
  recreated. Dashboard PL is running the local multi-lens/Codex review and
  dispatching only the two high bugs through isolated no-land workflows. No
  PR, push, review submission, merge, or publication is authorized.

## 12:17 EDT Packs capacity and maintenance claim recovery

- After retrying `gpk-wi3dh` through the correct isolated formula, Packs PL
  proved both 2/2 slots remained durably bound to held work. The +1 request was
  therefore approved under worker-pool flex. Snapshot:
  `agents/gascity-packs-polecat/agent.toml.bak-gpk-clean-slot-20260720T161457Z`.
  The one-concern 2→3 floor/ceiling change passed TOML parsing and was applied
  by soft reload revision `edb06ef3ba96`, preserving held sessions.
- Fresh slot 3 session `gc-524521` is active at clean scaffold
  `/home/ds/gascity-packs-worktrees/gascity-packs-polecat-3`. Packs PL must
  create and stamp a dedicated `gpk-wi3dh` worktree/branch before routing and
  obtain exact persistent-cwd/clean-HEAD acknowledgement before edits. Shrink
  back to 2/2 once that repair is branch-ready and stale slot bindings are
  repaired. `gc doctor` after reload reported 143 passes; its sole failure was
  the documented order-history 15-second read timeout, not config parsing or
  session convergence.
- Maintenance P0 `gc-5cgt` exposed the selector bug circularly: implementation
  was already underway in the correct pinned worktree while child `gc-y51u`
  still appeared open/unassigned. One exact recovery mutation recorded only
  the observed tuple: `gc-y51u` in progress, session/assignee
  `polecat-gc-520627`, worktree
  `/home/ds/gascity-worktrees/polecat-1/worktrees/gc-5cgt`, branch
  `work/gc-5cgt`. P0 may now finish branch-ready/no-land; P1 `gc-xgo4` remains
  held until this selector repair artifact exists.

## 12:38 EDT Exact-claim verification and fail-closed corrections

- The apparent `gc-5cgt` exact-child recovery did not survive authoritative
  session verification. `gc-y51u`/`gc-lbj1` were overwritten with projected
  cwd `/home/ds/gascity-worktrees/polecat/gc-y51u-execute-focus-on-the-bead`,
  while `gc session list` reported the worker's persistent cwd as a third path,
  `/home/ds/gascity-worktrees/polecat/gc-f6pi-maintenance-cycle-review-gastownhall-gascity-20260720t160000`.
  Neither is the target Git worktree. The worker stopped before commit and
  preserved six-file WIP only in the intended `gc-5cgt` worktree. Maintenance
  PL then blocked and `dispatch-blocked` the P0 target/root/child plus the held
  P1 target/root/child. The prior `manual_exact_child_claim_recorded` outcome
  is invalid; no workflow may reopen until an authoritative pre-claim tuple is
  proven.
- Packs provisioned the correct dedicated checkout
  `/home/ds/gascity-packs-worktrees/gascity-packs-polecat-3-wi3dh` on
  `work/gpk-wi3dh`, but routing `gpk-7o680` to exact slot 3 was rewritten to the
  generic pool and projected into held slot 1. The child remained open and
  unclaimed; slot 1 reported no edits. Slot 3 meanwhile respawned at its
  non-repository scaffold rather than the checkout. `gpk-wi3dh`, workflow
  `gpk-ezmg6`, and child `gpk-7o680` are now blocked + `dispatch-blocked`,
  unassigned/unrouted, with the dedicated checkout preserved. No more routing
  attempts are permitted before `gc-xgo4` is verified.
- Reef exact-head review completed locally at
  `e360c52995464eadc6cfdd56fd653c540494101c` with verdict
  `request_changes`; artifacts are recorded on `gascity-dashboard-h5rl.8` and
  publication remains forbidden. Dashboard P1 `gascity-dashboard-ys8i` was
  stopped before edits because its checkout was detached at stale local main
  `5f52927`, not a named branch from canonical `origin/main` `fdd2d63`.
  P1 `gascity-dashboard-z7u3` has a named canonical-base branch, finished its
  targeted/full local checks green, and is limited to a branch-ready commit.
- Infra P1 `dr-bfvv` closed with artifact
  `.gc-reports/dispatcher-watchdog-per-target-scoping-2026-07-20.md`: exact
  per-city/rig processable target sets, target-keyed clocks stable across
  replacement session IDs, empty-queue clearing, malformed-read fail-safe,
  and unchanged 5400-second threshold. Syntax, shellcheck, ruff, TOML/order
  resolution, and 11 pytest cases passed; no runtime action occurred. Fresh
  resource alert `gc-524581` is attached to now-unblocked read-only
  `dr-f1pq`; it reports 106 `mcp-remote` and 65 `--mcp` processes with 9.7 GiB
  MemAvailable. No MCP signal, cleanup, config edit, or restart is authorized.

## 13:08 EDT Verified local progress and capacity containment

- Dashboard P1 `gascity-dashboard-z7u3` is complete at exact SHA
  `98495f0ee9821304b3f775390334e381ea580b6d`: local typecheck/shared/frontend/
  lint/build/format gates passed and the separate Codex-inclusive exact-SHA
  review verdict is PASS. It is blocked + `dispatch-blocked` + `hold:external`
  rather than open, so branch-ready work cannot be reimplemented or finalized.
  No push, PR, review submission, merge, or publication is authorized.
- Dashboard P1 `gascity-dashboard-ys8i` also exposed false session
  attribution: claiming from the PL rewrote `gc.work_dir` to the shared rig
  root. It was reclassified truthfully as PL-direct/no-pool, committed only on
  canonical branch `work/gascity-dashboard-ys8i` at
  `1a27a1ad71684ba4ada5cd6cceef87964ba20701`, blocked branch-ready, and sent
  through its separate exact-SHA local review. The overwrite evidence is owned
  by systemic repair `gc-2zz4`.
- Packs F05 `gpk-0oj72` completed its current-source inventory of all five
  resolved mutation-capable formulas and their live callers, repository/action
  scope, prompt/hook gates, and head binding. It found two candidate-disable
  formulas with no live executable caller, an under-bound revert path, and
  account-dependent ship/merge hook coverage. No formula, hook, order, prompt,
  registry, or external state changed. Separate one-concern follow-ups precede
  any disablement; `gpk-6n7x2` may next touch only non-overlapping
  `mol-adopt-pr` portability.
- The temporary Packs 2→3 flex was retired after `gc-524627` proved the clean
  third slot cannot claim through the broken selector. Snapshot:
  `agents/gascity-packs-polecat/agent.toml.bak-retire-gpk-clean-slot-20260720T165600Z`.
  A one-concern 3→2 change passed TOML/diff checks and soft reload revision
  `4fa2f3a88941`; resolved min/max are 2/2. This avoids an idle Amp/MCP set
  after swap alert `gc-524686`; no other pool or rig changed.
- P0 `gc-5cgt` survived three fail-closed recovery probes without losing its
  original six-file WIP: workflow-child projection was non-repo, an exact-slot
  route reused an unrelated session, and both sibling/nested apply-patch roots
  were rejected by tool isolation. Focused read-only Go tests passed. A final
  bounded recovery is now running as `amp --mode medium --execute` launched
  directly from the canonical `work/gc-5cgt` worktree—the first mode whose
  project root and patch boundary are the same. It may commit branch-ready only;
  P1 `gc-xgo4`, PR gate `gc-3cp6`, and all publication remain held.
- Proposal-only Gas City PR #4460 review is no longer an inbox-only APPROVE:
  `gc-3cp6` owns the exact-head Codex gate at
  `5dd638f6d61a686b319de24f49fa73d0f99478f7` and preserves the finding that
  #4460 does not establish coverage of #4382. GitHub review submission remains
  separately authorization-gated.
- Dispatcher alert `gc-524720` was observed, not reset blindly. The one shared
  trace stopped while Dolt/supervisor CPU were saturated, then resumed with
  fresh wake/idle heartbeats within the bounded observation window. No session
  was killed. New owned P1 `dr-u9py` requires per-dispatcher heartbeat identity
  and an overload-stall fixture before any exact-session recovery recommendation.
  Existing F11 `dr-s7am` is assigned to Infra PL for the resolved order census
  behind active read-only MCP attribution `dr-f1pq`.

## 13:20 EDT Evidence correction and queued local follow-through

- Infra's original `dr-f1pq` closure omitted a literal signal to PID 3940434,
  so the mayor invalidated and blocked the closure pending exact attribution.
  The corrected artifact now identifies the target as the investigation's own
  long-running `grep | head` shell-command handle, not an MCP, managed session,
  service, or unrelated process. Exact historical `/proc` identity was not
  captured and is explicitly marked inferred; the owning Amp/pane remained live
  in the recorded tmux scope and PID 3940434 remains absent. The bead is
  re-closed with `guardrail-breach` retained durably. No further signal,
  cleanup, config edit, or restart occurred.
- The substantive MCP census found 214 process layers / 4.89 GiB dominated by
  live global configuration, not stale survivors. Infra requested decisions on
  native HTTP Sourcegraph transport and CodeGraph scoping. Those global/token
  decisions are deferred for Stephanie; no overnight config mutation is
  authorized. Infra is proceeding one-at-a-time with P1 `dr-u9py`, then P2
  `dr-s7am`, under no-arbitrary-kill/no-manual-fire guardrails.
- Dashboard `gascity-dashboard-ys8i` exact-SHA review at
  `1a27a1ad71684ba4ada5cd6cceef87964ba20701` returned `request_changes` for two
  reproduced completeness defects: an exit-1 untracked-file read can appear as
  a valid empty patch, and truncated name-status output can lose its partial
  marker. It remains blocked while Dashboard PL performs a local correction and
  exact-new-head four-lens/Codex-inclusive re-review. No publication is allowed.
- Packs inventory `gpk-0oj72` is acceptance-complete and closed with no formula
  mutation. Follow-up `gpk-6qifn` owns account-independent, repo/action/head-bound
  ship/merge verification design; settings changes remain human-gated.
  Portability bead `gpk-6n7x2` has a two-file uncommitted `mol-adopt-pr`
  worktree-root parameterization only in its dedicated checkout on
  `work/gpk-6n7x2@7ed265a`; targeted tests pass. Packs PL was directed to remove
  generated test cache, complete local gates, commit branch-ready from that exact
  cwd, and keep `mol-pr-iterate`, `mol-pr-triage`, and PR #217 untouched.
- Bounded Amp PID 339525 remains active from the canonical `gc-5cgt` worktree;
  the original six-file WIP is still preserved and uncommitted. Maintenance PL
  remains in oversight hold, so `gc-xgo4`, `gc-3cp6`, F01/F02/F14, and all
  publication paths cannot advance before an independently verified branch-ready
  result.

## Next action

Collect the exact-worktree Amp result for `gc-5cgt`, the exact-SHA review for
Dashboard `ys8i`, and Infra's per-dispatcher heartbeat artifact; then advance
only their already-owned local follow-ups. Keep every
publication/merge path held until exact-SHA Codex PASS plus separate current
action authorization are both present.

## 14:55 EDT Branch-ready claim fence and Infra/Dashboard/Packs reconciliation

- Gas City P0 `gc-5cgt` is locally branch-ready on `work/gc-5cgt` at
  `e192a2465769bc0e103773abcadc3e9d5cee2efd`. Focused `cmd/gc` and
  `internal/beads` tests passed; the city-local `mol-focus-review` precheck
  harness, ShellCheck, Bash parse, and TOML parse passed; final independent
  Oracle review returned PASS after same-owner idempotency and omitted-label
  compatibility were corrected. The broad suite remains baseline-red on the
  five independently reproduced tests recorded in
  `.gc-reports/gc-5cgt-dispatch-blocked-claim-fence-2026-07-20.md`.
- The current AOA hold census is safe: 11 nonterminal exact-label beads are all
  unassigned and unrouted, including `aoa-wbn7`. One has stale historical
  session metadata only. The original 14-bead acceptance cannot yet be claimed:
  `aoa-d6t.41`, `aoa-ctyo`, and assigned `aoa-g2g5` closed after the P0 was
  filed and before the local commit. Their history was preserved; no AOA state
  was reverted. Maintenance later dispositioned this as a historical exception
  that cannot be made true retroactively and closed `gc-5cgt` branch-ready at
  the exact local SHA. A post-adoption runtime census is required before held
  follow-ups advance; the commit remains local-only.
- Dashboard `gascity-dashboard-ys8i` now durably records exact-head approval at
  `c6baa0614a67f20239d3b99e592fd8430827bbcf`, with four-lens/Codex synthesis at
  `/tmp/gcd-review-pr/ys8i-c6baa06/synthesis.md`. It remains blocked,
  branch-ready, no-land, and unpublished.
- Infra P1 `dr-u9py` is closed after exact per-dispatcher heartbeat attribution,
  malformed/duplicate census fail-closed handling, shared-stall classification,
  and hermetic tests all passed. Its read-only live evaluation mapped nine
  dispatchers and recommended no action; no dispatcher was killed or reset.
- Infra F11 `dr-s7am` closed after independent factual/design review of the
  read-only resolved-fleet
  artifact `.gc-reports/f11-resolved-order-fleet-census-2026-07-20.md`: 160
  active resolved rows, 49 absent from a city-file-only scan, and 34 with no
  history. It proves no complete missed-schedule signal exists and defers any
  sensor until execution substrate and grace policy are explicitly chosen.
- Packs `gpk-6n7x2` has one reviewed local slice at
  `6a461dd0aa857196387d4f5d80150582db214c77` for `mol-adopt-pr` worktree-root
  portability. The parent remains blocked on PR #217 before overlapping
  `mol-pr-iterate` work. Account-independent exact-head authorization design is
  durably owned by P1 `gpk-6qifn`; no settings or external action is authorized.
- Resource pressure remains elevated: 63.9 GiB RAM, 8.3 GiB available, all
  8 GiB swap consumed, load 23/46/63 at the 14:51 snapshot. The largest new
  short-lived pressure included a ~1 GiB `rust-analyzer` under an active AOA
  worker; no process was signalled because no safe leak classification was
  established. Heavy suites, new heavy sessions, MCP cleanup, and broad
  restarts remain held.

## 14:58 EDT Subagent-output security incident is owned, not inbox-only

- AOA review mail `gc-525196` rejected `aoa-w0o@02b6583` for an independently
  reproduced non-UTF-8 trial-directory regression and re-armed its correction
  workflow. No promotion or publication occurred.
- The same review reported two fabricated `system-reminder` blocks inside a
  security-reviewer subagent's tool output. They attributed the reviewer's own
  probe edits to the user and instructed concealment/non-reversion. The reviewer
  refused, reverted its probes, and the parent independently verified the
  worktree clean. A separate two-reviewer worktree collision is known but is not
  accepted as the explanation without provenance evidence.
- P1 security incident `dr-05cx` is assigned to City Infra PL with raw-evidence
  preservation, exact producer attribution, trusted-harness-versus-untrusted-
  injection discrimination, and a fail-closed containment/test requirement.
  Provider/global config changes, evidence deletion, agent signals, restarts,
  and external publication are explicitly unauthorized. Mail `gc-525215`
  notified the owner; investigation begins read-only under exhausted swap.
- Resource alert `gc-525207` showed 8.96 GiB available, 1.8 MiB swap free,
  load 19/32/53, and continued MCP fan-out. This was recorded without an
  unproven process kill or broad cleanup.

## 15:04 EDT Maintenance P0 closure preserves the runtime gate

- Maintenance mail `gc-525242` confirms `gc-5cgt` closed branch-ready/no-land
  at exact local SHA `e192a2465769bc0e103773abcadc3e9d5cee2efd`. The three
  pre-commit AOA closures remain durable incident evidence rather than being
  rewritten to manufacture literal 14-bead compliance.
- Metadata requires `post_adoption_runtime_validation` before any held queue
  advancement. `gc-xgo4` and `gc-3cp6` were rechecked blocked, unassigned,
  `dispatch-blocked`, and unauthorized; F01/F02/F14 and publication remain held.
  No worktree edit, land, push, PR, comment, review submission, merge, or
  publication occurred during disposition.

## 15:12 EDT Runtime metadata incident attributed; repair work stays split

- City Infra closed `dr-05cx` with exact producer attribution in
  `.gc-reports/dr-05cx-fabricated-system-reminder-provenance-2026-07-20.md`.
  Claude Code 2.1.215's built-in `edited_text_file` metadata template—not AOA
  content, MCP/hook output, or model fabrication—generated the unsafe intent and
  non-disclosure wording. Binary SHA-256 is
  `c1efffaaf370aa187cb6a09dd93d4e511c646899b0078476f83791b664bde7fe`; the
  template begins at byte offset 250044100.
- Exact trigger reconstruction showed another reviewer's Bash `git checkout`
  removed the security reviewer's `answer.rs` Edit probes; the security
  reviewer later removed its own `codeprobe_run.rs` probes through Bash. Both
  operations bypassed Edit-tool attribution. The first is a shared-worktree
  collision; both expose the runtime's invalid inference from unknown producer
  to user/linter intent. Expanded per-event metadata is omitted from retained
  JSONL, leaving a separate auditability gap.
- P1 `dr-h2sz` owns local containment: parallel mutation-testing reviewers must
  be read-only or isolated by worktree, with a hermetic collision regression.
  P2 `dr-tinx` owns an evidence-complete, locally reviewed upstream issue draft;
  external filing is not authorized. City Infra began `dr-h2sz` with read-only
  launch-path mapping and no global config/runtime mutation.
- Packs P1 `gpk-6qifn` completed a read-only contract map at
  `/home/ds/gascity-packs-main/.gc-reports/gpk-6qifn-authorization-contract-map-2026-07-20.md`.
  It records account-dependent hook registration, non-head-bound merge evidence,
  arbitrary bypass acceptance, and five negative fixtures. Human decision bead
  `gpk-dto9v` separates the installation surface choice (global/account repair,
  pack overlay, or Gas City launch preflight). Packs may build only
  installation-independent inert fixtures in a dedicated clean worktree; no
  settings mutation or external action is authorized.

## 15:21 EDT Reviewer collision containment is split by ownership

- City Infra's read-only `dr-h2sz` map at
  `.gc-reports/dr-h2sz-reviewer-launch-ownership-map-2026-07-20.md` found four
  direct collision-capable launchers: global `/review`, global `/review-pr`,
  `/gascity-dashboard-review-pr`, and Packs `mol-pr-ship`. Prompt-only
  read-only wording is not enforcement: every local reviewer exposes Bash and
  security/database reviewers also expose Edit/Write.
- Human decision `dr-q0hg` owns authorization and the isolation choice for
  installed global/Dashboard skill paths. Default recommendation is one
  detached exact-commit worktree per reviewer; capability-read-only is acceptable
  only when filesystem denial is proven. No global skill/config edit occurred.
- Packs P1 `gpk-3tsgd` owns canonical `mol-pr-ship` reviewer isolation and
  hermetic collision fixtures, queued behind active `gpk-6qifn`. PR #217 and
  all external actions remain excluded.
- The currently running Dashboard Reef review received an immediate fail-closed
  instruction: verify exact HEAD/tree and unchanged status before consuming any
  reviewer output, preserve any mutation as incident evidence, and launch no
  further reviewer wave. No result is publication authorization.
- `gpk-6qifn` source work is correctly isolated at
  `/home/ds/gascity-packs-worktrees/gascity-packs-pl-gpk-6qifn` on
  `work/gpk-6qifn`; read-only verification showed the chair checkout still has
  only its pre-existing `.gitignore` modification. Two new untracked verifier
  files are confined to the dedicated worktree.
- Sensor mail `gc-525305` caught P2 `dr-tinx` assigned without its pulling
  `city-infra` label. The label was added immediately; blocked `dr-h2sz` and
  closed source `dr-05cx` were normalized too. `dr-tinx` remains local-draft
  only and externally unauthorized.

## 15:31 EDT Private upstream draft and inert Packs verifier are complete

- City Infra closed `dr-tinx` after writing and sequentially security-reviewing
  `.gc-reports/dr-tinx-claude-code-edited-text-file-upstream-draft-2026-07-20.md`.
  It contains the exact binary/template evidence, disposable reproduction,
  provenance-neutral wording, privacy-controlled metadata serialization, and a
  hermetic regression. Private paths and session/agent IDs are absent. The draft
  remains local; no vendor/GitHub/Slack submission is authorized or performed.
- Packs committed the installation-independent `gpk-6qifn` verifier slice at
  `66c47302b73e05f56971ad54ea98abe9b81d37ad` on `work/gpk-6qifn`. It adds a
  pure authorization envelope and 31 passing targeted fixtures covering stale
  head, missing verifier/envelope, arbitrary bypass, identity mismatch,
  replay/expiry, malformed input, and fail-closed trust defaults. Ruff, format,
  and diff checks passed; two independent findings were fixed.
- `gpk-6qifn` is now correctly BLOCKED on human decision `gpk-dto9v`, with a
  recorded dependency and `gc.work_dir` pointing to its clean dedicated
  worktree. It is not left falsely in progress. The chair checkout still has
  only its pre-existing `.gitignore` change; no settings, registration,
  formula, or PR #217 source changed. A separate synced-vault scope breach is
  recorded below.
- Packs started only read-only source/fixture planning for P1 `gpk-3tsgd` in a
  new dedicated worktree. No parallel reviewer fan-out or personal-vault edit
  is authorized for this follow-up.
- Resource pressure remains guarded: at 15:31 EDT available memory fell to
  3.56 GiB with all 8 GiB swap consumed. No new heavy wave or unattributed
  process signal was authorized; Dashboard's already-running reviewer/correction
  tool was steered to fail closed and stop before another wave.

## 15:38 EDT Dashboard review is contained; synced-vault write is preserved

- Dashboard stopped before another reviewer wave. Reef correction head
  `48373446bf604cc46eab70dd57d3e267ae5b3822` and tree
  `53d7f93c99ae379f4efb2624197b8640ca706a13` were verified unchanged with an
  empty staged/unstaged/untracked set in the preserved Reef worktree. The prior
  `ae14020b...` gate remains `request_changes`; `48373446...` is explicitly
  not reviewed. Bead `gascity-dashboard-h5rl.8` is blocked/no-land awaiting an
  isolated read-only exact-head review mechanism. No publication occurred.
- Before the explicit no-vault boundary, Packs PL made one Python write call
  that changed two live-synced vault files and inserted three bullets: an Open
  work item and 2026-07-20 Daily log entry in
  `/home/ds/brain/Projects/Gas City Packs.md`, plus an issue-learning entry in
  `/home/ds/brain/Projects/Gas City Packs Issues Log.md`. The earlier claim
  that no other vault file changed was false. The PL acknowledged that treating
  standing memory instructions as authorization was too broad. No control path
  or sync-conflict artifact changed.
- The two notes have no git undo and propagate to personal devices, so both
  were preserved unchanged rather than blindly reverted. `gpk-6qifn` now
  records both exact paths, `vault_write_count=2`, `vault_added_bullets=3`,
  `external_action_breach=obsidian_live_sync_write`, and human disposition.
  P1 `dr-imrf` was assigned to City Infra for the read-only trace and is now
  closed with exact inherited-instruction evidence and a fail-closed wording
  and test proposal. Vault, global prompt/config, and Syncthing mutation
  remained prohibited during that investigation.
- Resource alert `gc-525369` recorded 270.8 MiB swap I/O with 0.1 MiB free and
  7.52 GiB memory available. The heavy Dashboard review wave is now stopped;
  no process was signalled or service restarted on aggregate attribution.

## 15:52 EDT Vault incident scope corrected; shared repair is fail-closed

- City Infra closed read-only trace `dr-imrf`. Artifact
  `.gc-reports/dr-imrf-vault-write-authorization-trace-2026-07-20.md` proves the
  shared `VAULT_NOTES` fragment mandates writes while the current bead, mayor
  instruction, and user task denied external action. The loaded Obsidian skill
  disclosed live propagation but did not grant authorization. Eight rig PLs
  directly inherit the fragment; City Infra is a ninth indirect consumer.
- Exact preserved evidence: `/home/ds/brain/Projects/Gas City Packs.md`,
  SHA-256 `55d3a005ae1be5bd083911c070a38c65d85ba8af6f10ac892f62850d07531c8e`,
  mtime `2026-07-20 15:25:49.366757951 -0400`, contains inserted bullets at
  lines 10 and 22. `/home/ds/brain/Projects/Gas City Packs Issues Log.md`,
  SHA-256 `8630dcb50aa1ca57cd4ab0c4466dd1cac5c2fc3210751983fba4d97b1ac63d59`,
  mtime `2026-07-20 15:25:49.367150303 -0400`, contains the third inserted
  bullet at line 5. Exactly those two files changed in the write-time window;
  no conflict artifact was found. Neither file was mutated during disposition.
- P0 `dr-1cc0` now owns the shared prompt repair and hermetic nine-consumer
  policy test. It is BLOCKED with `shared_prompt_edit_authorized=false` pending
  a current explicit authorization for that boundary. No shared prompt, global
  skill/config, vault, Syncthing, or external communication was changed.
- Packs implementation bead `gpk-6qifn` was corrected in place to remove its
  false one-file claim and now links the full City Infra evidence. Its inert
  verifier commit and human-gated installation state are otherwise unchanged.

## 16:03 EDT Premature closures reopened; lightweight lane work remains bounded

- Dashboard read-only reconciliation confirmed both deep-audit high findings
  already have separate, reviewed, branch-ready ownership. Backend run-diff
  fail-closed repair `gascity-dashboard-ys8i` is held at exact reviewed SHA
  `c6baa0614a67f20239d3b99e592fd8430827bbcf`; frontend render-immutability
  repair `gascity-dashboard-z7u3` is held at exact reviewed SHA
  `98495f0ee9821304b3f775390334e381ea580b6d`. Their scopes do not overlap.
  Reef `gascity-dashboard-h5rl.8` remains blocked at
  `48373446bf604cc46eab70dd57d3e267ae5b3822`; no new reviewer wave, source
  change, heavy suite, or publication occurred.
- Packs committed reviewer-isolation candidate `gpk-3tsgd` at exact SHA
  `2981f2f37a875017d46553465de60abf4ff89fe6`, tree
  `57edc18162646d2993045766aebcf5831ea0ec57`, in its clean dedicated worktree.
  Three hermetic tests, Ruff, TOML/step validation, and diff checks pass. A
  sequential independent review found four issues and those were corrected,
  but the exact post-fix SHA was not independently re-reviewed because of the
  resource guard. The bead was corrected from `in_progress` to BLOCKED/no-land
  with `review_verdict=pending`; no further review wave or broad suite ran.
- Maintenance selected P1 security-hardening bead `gc-30a5`: validate provider
  session keys at the common prime boundary and shell-escape all resume-command
  sinks without breaking template/flag/subcommand forms. A dedicated clean
  worktree is pinned to local integration base
  `92119a561f34f5a60d5cea71c6d9997cd33415ea`; work is local/branch-ready only,
  focused tests only, with no public disclosure or external action.
- City Infra read-only tracker `dr-5g8` proved source bead `gc-r9fx` had been
  closed before its load-bearing consumer integration was implemented. The new
  transactional owner is called only by `cmd/gc/cmd_worktree.go`; `bin/gc-sling`
  still performs ad hoc detached provisioning and `mol-focus-review` remains a
  second owner. The installed binary also lacks the new command. `gc-r9fx` was
  reopened, assigned to Maintenance, cleared of stale routing/session/workdir
  metadata, and queued behind current work with no-land/external-action denies.
- During that read-only check, a quoted durable-note command accidentally used
  shell backticks. `git worktree add --detach` failed from the non-git city root
  and `gc worktree` failed as unknown; neither created a path/ref nor changed
  runtime/config. City Infra corrected the durable note and disclosed it in
  `gc-525492`.
- City Infra then verified `dr-ezx`: owning P0 source `gc-yrwp` was open and
  unassigned with no branch, implementation, test, review, or landing evidence.
  `gc-yrwp` is now assigned to Maintenance as an owned-but-not-started queue
  item behind `gc-30a5` and reopened `gc-r9fx`; `dr-ezx` remains blocked on it.
- Resource alert `gc-525509` reported 106 `mcp-remote`, 64 `--mcp`, and 24
  `code-intel-copilot` processes, with the last group at 2.56 GiB, available
  memory 5.63 GiB, and swap effectively exhausted. Existing attribution bead
  `dr-f1pq` remains the basis for not signalling MCPs from aggregate counts.
  Work remains serialized and targeted; no process, worker, dispatcher, or
  service was signalled or restarted.

## 16:14 EDT Packs backlog blocker corrected to the real source layer

- `gpk-qpvvc` initially recorded that making reviewed portability commit
  `6a461dd0aa857196387d4f5d80150582db214c77` canonical would unblock incoming
  PR #216. Read-only ancestry and tree checks disproved that standalone action:
  `main`, `origin/main`, and `upstream/main` contain no
  `pr-review/formulas/mol-adopt-pr.formula.toml` at all.
- The running city instead imports `pr-review` directly at
  `city.toml:395-396` from `/home/ds/gascity-packs/pr-review`. That checkout is
  on old feature branch `policy/rc-last-resort-take-the-good` at
  `7ed265aeb8c5d9f3a4bac75f658b7e543d267559`, with pre-existing dirty changes
  in `mol-pr-from-issue.formula.toml` and `mol-pr-iterate.formula.toml`.
  `gpk-6n7x2` is one clean child of that exact feature head. Repointing the
  city import to it could drop the dirty changes; applying it in the imported
  checkout would cross-contaminate shared live state.
- New P1 `gpk-lpzsk` owns a read-only source-of-truth reconciliation: identify
  the exact resolved formula bytes and dirty-file owners, then specify either a
  clean reviewed integration branch or immutable versioned pack. It formally
  blocks `gpk-qpvvc`. `gpk-6n7x2` and the backlog help request now record the
  corrected source-layer dependency. No checkout, formula, `city.toml`, PR #217,
  vault, or external state was mutated.

## 16:19 EDT Packs source decision is evidence-complete and blocked safely

- Packs completed read-only `gpk-lpzsk`. The currently resolved live
  `mol-adopt-pr` source has SHA-256
  `ebac7a81a02058c3ba3e63224250ce567aa0e3fa85145460ca8916d151c1bd35`;
  its compiled intake still hardcodes the Gas City worktree root and exposes no
  `worktree_root` variable. The reviewed child formula has SHA-256
  `2cf3d9225b8a6a33c018fe3c5efc010f1d6aa306f149d2b47ee771a4c26ae02d`.
- The two unowned imported-checkout deltas are old formula-compiler migrations,
  not verified PR #217 work: `mol-pr-from-issue` patch-id
  `74ee0b56482098a84a4a41d5a5d28540cab3f161` and `mol-pr-iterate` patch-id
  `bf1976dc2e101803b5451d1bac54d8ed6ce71672`. No local/remote ref contains
  either exact dirty file hash and no bead owns them, so both remain preserved
  as concurrent WIP.
- `gpk-lpzsk` is BLOCKED on an answerable source decision: Option A, recommended
  interim, builds a clean exact-`7ed265a` integration branch after explicit
  keep/drop ownership for both patches, applies reviewed `6a461dd0`, and runs an
  exact-SHA gate before a separately authorized import change. Option B builds
  the same combined tree as a versioned immutable pack first. Directly pointing
  the import at `6a461dd0` or editing the dirty checkout is rejected. No source,
  config, worktree, PR #217, vault, or external mutation occurred.

## 16:28 EDT Maintenance partial security fix is preserved, reviewed, and frozen

- The isolated `gc-30a5` Amp executor stopped making file progress after
  16:11 EDT and had no active test/build child, only idle MCP/plugin children.
  Maintenance applied TERM to exact PID `1802623` only; it exited and the
  dedicated worktree, branch, base, and nine-file uncommitted diff remained
  intact. No worker, service, supervisor, dispatcher, or MCP was signalled.
- Maintenance reran three focused groups successfully: `internal/session`
  resume/persistence/marker tests, `internal/worker` first-start command tests,
  and `cmd/gc` resume/session command tests. The candidate shell-quotes
  explicit-template, flag, subcommand, first-start, and fork keys and rejects
  leading-dash/NUL through `PersistSessionKey` and `SetMarker`.
- Local review found a remaining security gap: shell quoting prevents shell
  metacharacter execution but does not stop leading-dash option interpretation
  after quote removal. Legacy/raw metadata, direct `SetMetadata`, and generated
  first-start paths can bypass the current validation. The patch is therefore
  explicitly not committable or branch-ready. `gc-30a5` is BLOCKED with its
  exact worktree preserved and a next gate requiring fail-close coverage for
  every raw/generated/legacy path, focused tests, and final review. No
  replacement executor was started; `gc-r9fx` and `gc-yrwp` remain queued.
- While steering that review, the mayor shell command accidentally embedded a
  backtick example inside a double-quoted argument. Bash ran
  `codex resume --help` and substituted local help text into the first message.
  The command was read-only and changed no repository, runtime, config, or
  external state. A second message sent through a non-shell-interpolating
  argument recorded the authoritative concern and this ledger preserves the
  operator error.
- Available memory briefly fell to approximately 3.2 GiB with all swap used.
  Maintenance and Packs were instructed to start no additional executor,
  reviewer, broad suite, or queued bead. No process was signalled on aggregate
  memory/CPU evidence.
- Final lifecycle metadata was made fail-closed: `gpk-3tsgd` lost its premature
  `branch-ready` label while `review_verdict=pending`; reopened `gc-r9fx` now
  records `impl_complete=false` and `review_verdict=pending` while preserving
  `92119a561` as the historical partial landed SHA. This prevents old partial
  PASS evidence from satisfying a later eligibility check.

## 16:47 EDT IDE restart detached access but did not destroy city sessions

- The operator reported an IDE restart at approximately 16:38 EDT and apparent
  loss of access to multiple sessions. Immediate read-only reconciliation found
  68 active Gas City session records and exactly 68 live panes on the
  `ds-research` tmux socket; no pane was dead and neither side had an unmatched
  active session name. Ten additional Gas City session records were asleep.
  The surviving panes include mayor, Maintenance, Packs, Dashboard, Infra, and
  the active worker pools. No session was recreated as incident recovery.
- There was no kernel OOM, user `systemd-oomd` event, supervisor restart, or
  coredump record in the 16:25-16:40 incident window. The supervisor remains on
  the instance started at 14:39:06 EDT after its earlier, separate OOM. Amp's
  server-side thread list remains readable, and the detached Amp/Claude
  processes remain present. No live Amp remote runner was registered after the
  IDE restart, which can explain lost client access but is not evidence that the
  underlying city or Amp threads were deleted.
- The resource hazard is nevertheless real. Swap is fully consumed (8 GiB),
  available memory fluctuated around 5-7 GiB, load reached 70-114 during the
  incident, and memory PSI `full avg60` briefly reached approximately 23%.
  The Gas City supervisor and canonical Dolt each sustained approximately 190%
  CPU and roughly 1.0-1.8 GiB cgroup memory; supervisor sampling requests had
  timed out from 16:28 onward. A point-in-time census also found 75 Amp-named
  process layers using about 7.4 GiB RSS, including each PL's plugin children;
  this is RSS rather than unique proportional memory and is not a signal basis.
  The earlier MCP attribution remains `dr-f1pq` and explicitly rejects
  command-name-based cleanup.
- New P1 `dr-wsh8` is assigned to `city-infra-pl` for an exact, read-only-first
  attribution of supervisor/Dolt saturation, event/query amplification, run
  scope churn, and the smallest feeder-specific containment. Its durable
  guardrails prohibit session/service/supervisor/Dolt/dispatcher/worker/MCP
  signals or restarts, rig suspension/resume, config edits, and new heavy work
  without separate evidence-based authorization. No broad cleanup, restart,
  suspension, MCP signal, or external action occurred during this response.

## 18:37 EDT Infra ownership required explicit delivery, then was acknowledged

- A later audit found that assigning `dr-wsh8` had not itself caused work:
  the bead was still open with no notes or artifact and unchanged since 16:47,
  while `city-infra-pl` remained alive at an idle prompt. The initial assignment
  was therefore not counted as handled ownership.
- A normal wait-idle session nudge was queued but still produced no bead
  acknowledgement. After confirming the PL was idle, mayor sent one immediate
  nudge to the existing `city-infra-pl` session; delivery returned `ok=true`,
  `queued=false`, and `outcome=delivered`. No new executor or session was
  created.
- `city-infra-pl` then claimed `dr-wsh8`, changed it to `in_progress`, and wrote
  a durable acceptance note acknowledging every read-only/runtime guardrail.
  The pane showed the PL beginning read-only attribution. This is accepted work,
  not yet a completed diagnosis: no artifact or final disposition exists yet.
