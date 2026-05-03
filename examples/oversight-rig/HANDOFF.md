# Oversight-rig handoff — 2026-05-03 (gc-j8h: gc-routed file uploads)

> **To the next agent:** this session closed **gc-j8h** — `gc slack upload` now routes through gc's new `/v0/city/{name}/extmsg/outbound-file` endpoint by default, picking up transcript records + peer fanout for file posts. Adapter-direct path preserved as `--via adapter` for diagnostics. Backend types + handler + Huma route, regenerated openapi.json + Go genclient + dashboard typescript bindings, pack-side switch, and tests all shipped in a single atomic commit at SHA `aca8a70f`. `core.hooksPath` is `.githooks`; `make dashboard-check` is green; full `go test -short ./...` is green.
>
> **Live smokes still pending** — gc-j8h is code-complete and unit-tested but has not been exercised against a real running supervisor + adapter binary in the live city yet. Items 1 + 2 in "Up next" cover the smokes; item 1 also subsumes the long-standing 'PL-driven outbound file smoke' gap that survived from the prior handoff.

## Up next (recommended dispatch)

With **gc-ywe** (slack-pack upstream-prep) at 6/6 and **gc-j8h** (gc-routed file uploads) closed, the slack-pack file-coordination story is structurally complete. Remaining work is dominated by operational smokes and the one P3 hardening bead. Surface area, ranked by user impact:

### Day-to-day coordination smokes (priority work — no code)

1. **Live-smoke the gc-routed outbound file path** (operational). Drive `gc slack upload --file <path> --thread-current` from a PL session; confirm: (a) `gc events --type extmsg.outbound` shows the upload with the FileID as MessageID; (b) `gc extmsg transcript ...` lists the outbound entry with `attachments[0].provider_id == file_id`; (c) any peer member of the room receives a session-log nudge containing the initial-comment text. Failure modes to watch: 422 from `/extmsg/outbound-file` (binding mismatch / unsupported adapter), 404 (no binding), or a delivered=true response with no transcript entry (Transcript.Append non-fatal swallow — would be visible in supervisor logs).

2. **Live-smoke the inbound file download path** (operational, surviving from prior handoff). `files:read` is granted; the adapter code is in place; the retention janitor is active. Confirm `inbound: chan=... files=N text=...ch` log line + `/tmp/gc-slack-adapter/inbound/<channel-id>/` artifact + the receiving PL's session log shows `attachments[0].url=file:///...`. Not a bead — drive once when next convenient.

### Quality / hardening

3. **gc-ywe.6** P3 — harden adapter `/tmp/gc-slack-adapter/` store default permissions to `0700`/`0600` for `IDENTITY_STORE_PATH`, `HANDLE_ALIAS_STORE_PATH`, `INBOUND_FILE_STORE`. Code change in `examples/slack-pack/adapter/main.go` plus a regression test guarding mode 0600 on state writes; doc note in env-contract README. Pre-existing risk; not a regression. Architect adversarial pass on a prior plan flagged: 6 write-mode call sites not 4 (INBOUND_FILE_STORE involves 2 sites in `downloadSlackFiles` + `slackDownloadToFile`); test must cover inbound-file dir + post-rename file mode; CHANGELOG entry under `### Security` required; consider startup `chmod` fixup for pre-existing-tree case. See gc-ywe.6 plan note.

### Standing bd queue (gc-side, not slack-pack)

4. **gc-a3s** P2 — `orders.overrides` with empty rig silently no-ops on per-rig orders. Owned by `gascity-pr-gc-a3s` worktree (already has fix branch `fix/gc-a3s-orders-overrides-rig-scope` at bf931ccf).
5. **gc-17z** P2 — verify cos picks up slack-v0 prompt + DM-ack behavior.
6. **gc-5rz** P2 — slack adapter Phase A absorption (UDS for /publish). Phase A is shipped and live; Phase B/C remaining work not yet scoped.

### Deferred / module rename

**Module rename** (file as new bead at upstream-extraction time): the adapter Go module is still `github.com/sjarmak/gc-slack-adapter`; rename to upstream-scoped path (e.g. `github.com/gastownhall/gascity-packs/slack/adapter`) when the pack is mirrored into the standalone gascity-packs repo. Cosmetic for a `package main` binary with zero internal importers, but worth doing at extraction time.

### Standing watch items (no action unless triggered)

- **Slow-start log line** — with gc-9ha live, the next `phases=[start_call=2m… …]` log line will either include `state_sync_recovery=Xs` (recovery branch is the cost) or won't (provider.Start is the cost). That answers gc-9ha acceptance #3 about whether the 60s `session.startup_timeout` should bound start_call.
- **Slack identity caveat on file uploads** — `files.completeUploadExternal` ignores `chat:write.customize`, so file posts appear under the default bot identity even when an agent has a registered persona. Slack API limit; documented in adapter handler comment + PL prompt. With gc-j8h shipped, the gc-side `HandleOutboundFile` is the natural place to compose a file-then-`chat.postMessage`-with-persona-identity pattern centrally if/when persona attribution on file uploads becomes important. Not currently filed as a bead — defer until a real user surface forces the question.

## Commits landed this session (gc-j8h)

This session's work: a single atomic commit `aca8a70f` implementing gc-routed file uploads end-to-end. New extmsg wire types (`PublishFileRequest` / `PublishFileReceipt`) and an optional `FileTransportAdapter` capability interface that `HTTPAdapter` implements by POSTing to the existing adapter `/publish-file` endpoint. New `extmsg.HandleOutboundFile` mirrors `HandleOutbound`: resolve binding → verify session → dispatch via `FileTransportAdapter` → record delivery context → append outbound transcript entry tagged with the FileID → emit `extmsg.outbound`. New Huma route `POST /v0/city/{name}/extmsg/outbound-file` registered alongside the existing outbound route; OpenAPI spec, Go genclient, and dashboard typescript bindings all regenerated. Pack-side: `gc slack upload` now defaults to gc-routed; `--via adapter` preserves the legacy direct path. Three new Go integration tests (happy path with transcript + peer fanout, file_path-required validation, ErrAdapterUnsupported when adapter lacks file capability) plus three new Python tests covering both routing paths. Pre-existing `csrf is False` assertions in `test_slack_chat_publish` / `test_slack_chat_reply_current` corrected to match the gc-5rz Phase A reality (proxied adapter calls carry `X-GC-Request`). gc-j8h closes.

```
aca8a70f feat(extmsg): route slack file uploads through gc for transcript + peer fanout (gc-j8h)
```

## Commits landed prior session (gc-ywe wave 3 — gc-28a + gc-ywe.5)

This session's work: a single atomic commit `e197a9be` relocating the slack adapter Go source from `examples/oversight-rig/adapter/` to `examples/slack-pack/adapter/` and updating all 11 path-bearing references (workflow YAML, pack.toml comment, README, CONTRIBUTING, CHANGELOG, .gitignore comment, `slack_chat_upload.py` docstring, `internal/extmsg/types.go` docstring, the design doc, this HANDOFF). gc-28a closes; gc-ywe.5 closes as a side-effect (adapter Go tests now travel with the pack); epic gc-ywe goes 6/6.

```
e197a9be feat(slack-pack): self-contain adapter source (gc-28a)
```

## Commits landed prior session (gc-ywe wave 2 — gc-ywe.2 + gc-ywe.3)

Four commits past prior HANDOFF (range `717aa27c..fad9a131`), pushed to `fork/feat/oversight-rig-pack`:

```
3db27544 feat(slack-pack): document adapter env contract + remove ds-research default (gc-ywe.2)
8e8acb3a chore(slack-pack): add LICENSE/CONTRIBUTING/CHANGELOG/CI for upstream layout (gc-ywe.3)
48fb88f4 fix(slack-pack): address Phase 4 review findings (gc-ywe.2, gc-ywe.3)
fad9a131 docs(oversight-rig): refresh HANDOFF after gc-ywe wave 2 (ywe.2 + ywe.3)
```

Wave was orchestrated via `/focus parallel`: two worktree-isolated subagents ran plan→execute→simplify on each bead concurrently; the main session cherry-picked their commits into a `wave-ywe23-review` branch, ran a unified Phase 4 review (3 reviewers in parallel — code, security, Go), applied fixes in `48fb88f4`, and merged the wave into `feat/oversight-rig-pack` with `--no-ff`. The subsequent `git pull --rebase fork` flattened the merge into linear history (fork was already on the same line of work), so the merge commit doesn't appear above. Key file changes (paths reflect the wave-2 state; the same files now live under `examples/slack-pack/adapter/` post-gc-28a):

- `examples/oversight-rig/adapter/main.go` (now `examples/slack-pack/adapter/main.go`): env-contract docstring expanded; `GC_CITY_NAME` default `"ds-research"` removed (loadConfig fails fast); 6 consumer-domain comments rephrased to neutral handles.
- `examples/oversight-rig/adapter/main_test.go` (now `examples/slack-pack/adapter/main_test.go`): new `TestLoadConfigRejectsMissingCityName`; `baseSlackEnv()` extended with `GC_CITY_NAME`.
- `examples/oversight-rig/adapter/run.sh` + `SETUP.md` (now `examples/slack-pack/adapter/{run.sh, SETUP.md}`): `GC_CITY_NAME` promoted to required-keys section / env-file template.
- `examples/slack-pack/README.md`: new "Adapter env contract" section (must-set / optional-override / controller-injected / consumer-specific tables); corrected false claim that controller injects `GC_CITY_NAME` for proxy_process services.
- `examples/slack-pack/{LICENSE, CONTRIBUTING.md, CHANGELOG.md}`: new — MIT to match repo root, CHANGELOG documents v0.1.0 surface + provenance, breaking `GC_CITY_NAME` requirement called out in `### Changed`.
- `.github/workflows/slack-pack.yml`: new — paths-filtered Go (`go test -race`) + Python (`pytest`) jobs; actions pinned to commit SHAs matching `.github/workflows/ci.yml`; `permissions: contents: read`; `cancel-in-progress` restricted to `pull_request`.

bd state changes this session:

- Closed: gc-ywe.2 (env-contract + ds-research removal), gc-ywe.3 (LICENSE/CONTRIBUTING/CHANGELOG/CI).
- Created: gc-ywe.6 (P3 follow-up — /tmp store perm hardening, surfaced by Phase 4 security review).

Phase 4 review summary: 0 CRITICAL, 3 HIGH (all fixed in `48fb88f4`), 4 MEDIUM-quick (all fixed), 1 MEDIUM follow-up (filed as gc-ywe.6), several LOW skipped as pre-existing.

## Commits landed prior session (gc-kvt + upstream-prep kickoff)

Range `69dc5545..010bc588`, pushed:

```
69dc5545 fix(testenv): respect nested go.mod boundaries when walking for sentinel
bfd64511 fix(extmsg): add native SessionID to PublishRequest, drop metadata workaround (gc-kvt)
20b0bb9c chore: golangci-lint fmt whitespace alignment in test tables
783ca9de docs(oversight-rig): refresh HANDOFF after gc-kvt + testenv lint fix
010bc588 docs(slack-pack): strip host-specific references + add scope banner (gc-ywe.1, gc-ywe.4)
```

Prior bd state changes:

- Closed: gc-98y (rollup), gc-kvt (PublishRequest native SessionID), gc-ywe.1 (docs scrub), gc-ywe.4 (scope banner).
- Created: gc-ywe (epic — upstream prep), gc-ywe.1..gc-ywe.5 (children).
- Wired: gc-28a → blocks → gc-ywe.5.

Prior session (kept for context, range `070f39c1..e7c51e6d`):

```
6416c171 fix(workspacesvc): include /v0/city/<name> in GC_SERVICE_URL_PREFIX (gc-cdf)
fb132d79 feat(cmd/gc): split session start_call into provider+recovery sub-phases (gc-9ha)
57321e1f fix(extmsg): snake_case json tags on PublishReceipt + AdapterCapabilities docs (gc-w1h)
cd372627 feat(extmsg): propagate source session id via wire metadata (gc-kvt prep)
b8abb72d feat(slack-pack): identity DELETE + bidirectional file attachments + new commands
57d767cc docs(oversight-rig): cos + PL prompts for address-by-handle, identity, files
34cd2344 docs(oversight-rig): refresh HANDOFF for shipped slack-pack feature wave
e7c51e6d chore: regenerate testenv import sentinels
```

Quality gates this session (run by .githooks/pre-commit on each commit):
- `golangci-lint fmt` + `golangci-lint run --new-from-rev=HEAD --fix` — clean (one S1016 surfaced and was suppressed with an inline `//nolint:staticcheck` and a comment explaining why the explicit struct literal documents the wire boundary; converting `OutboundRequest` to `PublishRequest` would let any future divergence silently leak to adapters)
- `go run ./cmd/genspec` + `go generate ./internal/api/genclient` — no drift (PublishRequest is an internal extmsg wire type, not exposed in the gc HTTP API)
- `go run ./cmd/genschema` — no drift
- `make lint` + `make vet` — clean
- `make test` — green
- `make dashboard-check` (run manually) — clean
- `make check-docs` — not triggered (no docs touched)

Known baseline noise (unchanged from prior handoff, reproduces on clean tree):
- `TestHandleExtMsgOutboundNotifiesDeliveredConversationMembers` race in `internal/api` — confirmed reproduces via `git stash && go test -race -run TestHandleExtMsg... ./internal/api/` on the bare `82d5e16d` checkout. Pre-existing.
- `TestStreamSessionPeekAcceptsPeekCapability`, `TestPhase0HandleSessionWake_ContinuityEligibleArchivedBeadRequestsStart` — same class.

## State

The Slack ↔ gc loop now supports:

- **Bidirectional file attachments**:
  - **Outbound**: `gc slack upload --file <path> [--initial-comment ...] [--thread-current|--thread-ts ...]` posts a file to a session's bound channel via Slack's three-step files-upload-v2 protocol. Adapter endpoint `/publish-file`. Bot needs `files:write`; without it the path returns `{delivered:false, failure_kind:"auth", error:"missing_scope"}` cleanly.
  - **Inbound**: when a Slack message has files, the adapter downloads each file's bytes (Bearer auth, atomic temp+rename) into `${INBOUND_FILE_STORE:-/tmp/gc-slack-adapter/inbound}/<channel>/<ts>-<safe-filename>`, then forwards the inbound to gc with `attachments=[{provider_id, url:"file://..." mime_type}]`. PLs `Read` the local path directly — no token leak, no curl.

- **Identity + alias deletion** (8A/8B):
  - `DELETE /identity?session_id=...` and `DELETE /handle-alias?handle=...` (both also accept JSON body).
  - Pack: `gc slack identity --remove` and `gc slack handle-alias --remove` flags.
  - Idempotent — missing entries return `{removed:true, existed:false}` without error.
  - Stale `smoke-test` entry was cleaned up using the new endpoint; the live identities.json now holds exactly 9 personas + 2 aliases.

- **Per-agent identities (8A)** and **cross-channel address-by-handle (8B)** unchanged from the previous handoff — both still working.

Verified by `go test -count=1 -race ./...` in `examples/oversight-rig/adapter` (all green) plus a live `/publish-file` smoke against the running adapter — **post-scope-grant** the response is `{delivered:true, file_id:"F0B1G3CFHKN"}`. The smoke artifact (45-byte `smoke-up.txt`) currently sits in `#all-agent-city`; safe to delete.

## Live runtime

- **Supervisor + gc API** PID **2448608** (systemd `gascity-supervisor.service`, restarted **2026-05-03 12:59:56 EDT**, third restart of the day to deploy gc-9ha). `/tmp/gc` rebuilt from current source at 12:58 — includes gc-cdf, gc-67o, and gc-9ha instrumentation. New systemd drop-in `slack-adapter-env.conf` adds `EnvironmentFile=-/home/ds/.config/gc-slack-adapter/env` so supervisor inherits Slack secrets and passes them to proxy_process children.
- **Slack adapter** PID **2455221** — supervised by gc as **proxy_process** (gc-5rz Phase A cutover, originally cut over **2026-05-03 12:54:45 EDT**, then respawned by supervisor at 13:00:12 EDT after the gc-9ha rebuild restart). Internal listener on UDS `/tmp/gcsvc-1000/ee31dfef/slack-*.sock`; public TCP `:8775` (Slack events) unchanged. Service state = `ready/ready`. Registered callback URL = `http://127.0.0.1:8372/v0/city/ds-research/svc/slack/publish (via gc /svc proxy)` — confirms gc-cdf URL-prefix fix end-to-end. gc-g52 janitor active in this adapter binary. Per-service log at `/home/ds/gas-city/.gc/services/slack/logs/service.log`. Current binary serves all of:
  - `POST /publish`, `POST /publish-file`, `POST /react`
  - `POST /identity`, `DELETE /identity`
  - `POST /handle-alias`, `DELETE /handle-alias`
  - `POST /slack/events` (public)
  - `GET /healthz` (both)
  - Capabilities now declare `SupportsAttachments: true`.
- **chief-of-staff** session `gc-83347`. **Mayor** session `gc-2568`.
- **13 project-leads** all on `claude-auto`. **v3 sweep ran 2026-05-03 13:16-13:32 EDT** (see /tmp/pl-restart-sweep-v3.log) — picked up the prompt template's "Files" subsection (gc slack upload protocol). All 13 PLs reset. The earlier v1+v2 sweeps had already covered the broader Files-protocol additions; v3 just refreshes everyone after the latest template tweak. **chief-of-staff (gc-83347) deliberately NOT swept** to avoid disrupting the user-facing channel. If a future template change touches cos behavior, reset gc-83347 manually.
  - **Bonus**: PLs re-registered Slack identities on restart per their template, so the identity registry grew from 9 → 12 entries. New identities: codescalebench, migration-evals, oversight-rig (deployment-local rig), agent-diagnostics. zeldascension switched emoji from `princess` → `triforce`.
- **Slack pack** has 10 commands now: bind-dm, bind-room, reply-current, publish, status, react, identity, handle-alias, publish-to-channel, **upload**.
- **Per-rig channels** unchanged — 7 of 13 rigs bound.

## Mayor 7 AM debrief — fired and posted (with one manual unblock)

Timer fired on schedule at **2026-05-03 07:00:25 EDT**, dispatched the system-reminder to mayor's session, and got a 202 from the gc API. Mayor composed the debrief but blocked on the channel ID for `#all-agent-city` — at the time the bot lacked `channels:read` and the channel had never sent an event the adapter could record, so mayor had no way to resolve the name → ID.

After the user pasted the channel ID (`C0B0TQMQF2B`), a follow-up reminder was injected and mayor published the debrief at **2026-05-03 08:33:11 EDT** — Slack ts `1777811592.120509`, posted under the registered "Mayor" identity (crown emoji), 1250 chars covering all six overnight items + status.

**Lesson now obsolete for the immediate cause** — `channels:read` was granted mid-day; agents can resolve `#name` → `C…` via `conversations.list`. The general operational hygiene still applies: when scheduling a "post to channel X" reminder, prefer to bake the channel ID into the script so the agent isn't doing a Slack API call inside a one-shot reminder.

Artifacts:
- Unit: `mayor-7am-debrief.timer` → `mayor-7am-debrief.service` (one-shot, expired after firing)
- Script: `/tmp/mayor-7am-debrief.sh`
- Log: `/tmp/mayor-7am-debrief.log` (shows POST status 202, exit 0)
- Mayor session log: `gc session logs gc-2568 | tail -100` shows the draft, the unblock, and the publish receipt.

## What changed this session

### Item 1 — DELETE methods for identity + alias

- Adapter: `identityRegistry.Delete(sessionID)` and `handleAliasRegistry.Delete(handle)` returning `(existed bool, err error)`. New types `identityDeleteReceipt`, `handleAliasDeleteReceipt`. New handlers `handleIdentityDelete`, `handleHandleAliasDelete` accepting either query param or JSON body. Wired into mux as `DELETE /identity` and `DELETE /handle-alias` (Go 1.22+ method-prefixed patterns). The existing POST routes are now `POST /identity` and `POST /handle-alias` — same behavior, different mux registration.
- Pack: `slack_chat_identity.py` and `slack_chat_handle_alias.py` gained `--remove` flags. New helpers `remove_identity_via_adapter`, `remove_handle_alias_via_adapter` in `slack_intake_common.py` (added `urllib.parse` import).
- Tests: `TestIdentityRegistryDelete`, `TestHandleAliasRegistryDelete`, `TestHandleIdentityDelete` (table-driven, query+body, idempotency, method rejection), `TestHandleHandleAliasDelete`.

### Item 2 — Slack file upload (outbound)

- Adapter: new types `publishFileRequest`, `publishFileReceipt`, `slackGetUploadURLResp`, `slackCompleteUploadReq`, `slackCompleteUploadResp`. New handler `handlePublishFile(cfg, identityReg)` mounted on `/publish-file`. Three Slack-API helpers: `slackGetUploadURL` (form-urlencoded), `slackPutFileBytes` (raw PUT, no auth header), `slackCompleteUpload` (JSON). Shared `mapSlackError` helper consolidates the failure-kind mapping. `registerAdapter` now declares `SupportsAttachments: true`.
- File-upload identity caveat: Slack's `files.completeUploadExternal` doesn't honor `chat:write.customize` overrides, so file posts appear under the default bot identity even when an identity record is registered. The adapter does the lookup for log parity but the override doesn't apply. Documented in the handler comment + the PL prompt addition.
- Pack: new command `gc slack upload`. Files: `examples/slack-pack/scripts/slack_chat_upload.py`, `examples/slack-pack/commands/upload.sh`, `examples/slack-pack/commands/upload/{command.toml,help.md}`. Helper `upload_via_adapter` in `slack_intake_common.py`.
- Tests: `TestHandlePublishFile` table-driven — happy path with thread + initial comment, missing inputs, GET/garbage rejected, missing_scope→auth, ratelimited→rate_limited, channel_not_found on complete→not_found, PUT 5xx→transient. Uses `httptest.NewServer` to stub Slack API + a separate stub for the pre-signed upload URL.

### Item 3 — Slack file download (inbound)

- Adapter: new type `externalAttachment` mirroring `extmsg.ExternalAttachment`. `slackMessageEvent` extended with `Files []slackFile`. `externalInboundMessage` extended with `Attachments []externalAttachment` (gc-side `extmsg.ExternalInboundMessage.Attachments` already existed, no gc-side change needed). `processSlackEvent` calls new `downloadSlackFiles` helper when `len(msg.Files) > 0`; failed downloads are dropped from the slice with a warning, the message itself still forwards. New helpers `downloadSlackFiles`, `safeFilename` (path-traversal-safe — strips `/`, `\`, NUL, control chars; replaces leading dots; caps at 200 chars), `slackDownloadToFile` (Bearer-auth GET + atomic temp+rename).
- New env: `INBOUND_FILE_STORE` (default `/tmp/gc-slack-adapter/inbound`).
- Tests: `TestSafeFilename` (table-driven, path-traversal cases), `TestDownloadSlackFiles` table-driven (single, two, none, missing url_private, 404 drops one but other succeeds, empty store path, sanitized name).

### PL prompt template — file protocol

Added a "Files" subsection to `agents/project-lead/prompt.template.md` between steps 5 and 6 of the reply-in-rooms protocol. Two beats:
- **Outbound**: use `gc slack upload --file ... --initial-comment ... --thread-current` instead of describing files in text. Notes the identity caveat.
- **Inbound**: when `attachments:` shows a `file://` URL, use `Read` directly — don't curl/HTTP.

PL restart sweep ran tonight to pick up the new template.

### Bd issues filed (Item 5)

- **gc-g52** — ~~extmsg adapter inbound file retention policy missing~~ **CLOSED this morning** — see "Inbound file retention janitor" section below.
- **gc-txv** — ~~gc session reset on chief-of-staff takes 1m+~~ **CLOSED this morning** — investigated; normal cos start is 5-7s, the 1m+ symptom comes from async-start queue saturation under sweep load + a 60s-startup-timeout escape (split into follow-up **gc-67o**). Operational mitigation (sequential restart sweep at 75s/session) is already in place.
- **gc-67o** — ~~session start timing — split lifecycle 'duration' into per-phase~~ **CLOSED this session** — instrumentation shipped; next slow-start log line will carry `phases=[start_call=Xs post_start_observe=Yms commit_refresh=Zms]` so the bottleneck localizes immediately. See "Phase-timing instrumentation" section below.
- **gc-kvt** — extmsg PublishRequest still lacks SessionID; metadata fallback is a workaround (P2, extmsg,tech-debt)

### Inbound file retention janitor (gc-g52)

In-process janitor in the adapter sweeps `$INBOUND_FILE_STORE` on a configurable interval, deleting regular files older than the TTL (mtime-based) and removing channel sub-directories that become empty afterwards. Defaults: TTL = `168h` (7 days), sweep interval = `1h`. Env knobs:

- `INBOUND_FILE_TTL` — duration string (e.g. `30m`, `48h`). `0` disables. Invalid duration also disables (with a warning).
- `INBOUND_FILE_SWEEP_INTERVAL` — duration string for the tick. `0` disables.

Code (in `examples/oversight-rig/adapter/main.go`):

- `sweepInboundStore(root, ttl, now)` — pure function returning `sweepResult{FilesRemoved, DirsRemoved, BytesRemoved, Errors}`. Skips files at the root level (only touches `<root>/<channel>/*`). Missing root is a no-op.
- `sweepChannelDir(channelDir, cutoff, *res)` — per-channel pass; one bad file doesn't abort the sweep.
- `runInboundFileJanitor(ctx, cfg)` — goroutine wired in `main()`. Runs one sweep on startup, then ticks. Cancels via `janitorCtx`. Disabled paths log once at startup; active paths log only when files/dirs were removed or errors occurred.
- `logSweepResult(res)` — silent on idle passes; logs counts + per-error lines on non-trivial passes.

Tests: `TestSweepInboundStore` (9 subtests covering missing/empty root, fresh vs old, empty-dir cleanup, multi-channel independence, ttl=0 disable, root-level files skipped, non-regular files skipped) plus four `TestLoadConfigInboundFileRetention*` tests for env parsing (defaults, overrides, disabled, invalid).

Activated by adapter restart on 2026-05-03 12:30:33 EDT (PID 1145516). Startup log confirms `inbound file janitor started: store=/tmp/gc-slack-adapter/inbound ttl=168h0m0s interval=1h0m0s`.

### gc-67o payoff + gc-9ha sub-phase split

Within the first wave of starts after the gc-67o supervisor restart, `phases=` segments populated correctly across all post-restart success paths (sync, async, control-dispatcher, worker-pool). One slow start showed up on the very first wave:

```
session lifecycle: op=start wave=0 session=codescalebench-worker-gc-71304
  template=/home/ds/projects/CodeScaleBench/codescalebench-worker
  outcome=success duration=1m24.557s
  phases=[start_call=1m22.53s post_start_observe=2.044s commit_refresh=1ms]
```

Bottleneck localizes cleanly to **start_call** (provider.Start + ErrStateSync recovery branch) — not post_start_observe (deterministic 2s sleep + observe), not commit_refresh (1ms bead reload).

**gc-9ha shipped** — start_call is now split between `provider.Start` (implicit; start_call - state_sync_recovery) and `state_sync_recovery` (the workerSessionTargetRunningWithConfig branch fired only when provider.Start returns ErrStateSync). New field `startPhaseTimings.StateSyncRecovery`; the format-log emits `state_sync_recovery=Xms` only when > 0, so the elision rule still holds for healthy starts. After the next slow-start observation we'll know whether the 1m22s lives in provider.Start itself or in the recovery branch — answers gc-9ha acceptance #3 about whether `session.startup_timeout` should bound start_call.

**Pre-restart phase-timing instrumentation (gc-67o)**

`startResult` gained a `phases startPhaseTimings` field with three duration sub-fields:
- `StartCall` — wall-clock inside `startPreparedStartCandidate` (provider Start + ErrStateSync recovery)
- `PostStartObserve` — `staleKeyDetectDelay` (2s) + `workerObserveSessionTarget` (only when `session_key` present)
- `CommitRefresh` — `refreshAsyncStartResult` bead reload (commit-side, async path only)

`logLifecycleOutcome` accepts an optional variadic `startPhaseTimings` tail; when non-zero, it emits a `phases=[...]` segment after `duration=`. Existing 14+ legacy callers stay untouched (variadic = back-compat). All 7 callers inside `commitStartResultTraced` and the two stale-path callers in `commitAsyncStartResultWithContext` now pass `result.phases`.

Tests in new file `cmd/gc/session_lifecycle_phases_test.go`:
- `TestStartPhaseTimingsFormatLog` (6 subtests) — zero elision, single phase, partial phases, all three, ms rounding, commit-only.
- `TestLogLifecycleOutcomeWithPhases` (4 subtests) — legacy no-phases call, phases segment present, zero phases elided, error + phases coexist.

Activated by supervisor restart on 2026-05-03 12:32:33 EDT (PID 1214945, `/tmp/gc` rebuilt at 12:31). First post-restart slow-start observation captured:

```
session lifecycle: op=start wave=0 session=codescalebench-worker-gc-71304
  ... duration=1m24.557s phases=[start_call=1m22.53s post_start_observe=2.044s commit_refresh=1ms]
```

Bottleneck is in `start_call` (provider Start + ErrStateSync recovery). Filed as **gc-9ha** for sub-phase split.

### extmsg wire-tag audit (gc-w1h)

Closes the wire-types fix that started with PublishRequest's missing snake_case json tags (silent data loss of `reply_to_message_id`, `idempotency_key`, `metadata` on the gc → adapter HTTP wire — discovered during PL room-reply threading work). PublishRequest tags were hot-patched live earlier; this session lands the proper fix + sibling-type audit:

- **`PublishReceipt`** (sibling, same hazard) — adapter `/publish` returns snake_case JSON (`{"message_id":"…","failure_kind":"…"}`); gc unmarshals into PublishReceipt. Without tags, Go's case-insensitive matcher does not bridge the underscore boundary, so `MessageID` and `FailureKind` silently arrived empty — breaking threading (no provider_message_id to thread on) and retry semantics. Now tagged snake_case in `internal/extmsg/types.go`.
- **`AdapterCapabilities`** — kept as PascalCase (matches the existing OpenAPI contract and the explicit PascalCase tags on the adapter's mirror struct). Added a doc comment so future contributors don't change it without also regenerating openapi + updating every adapter.
- **`ExternalInboundMessage`** — already had snake_case tags (verified).
- **Regression tests** — new `internal/extmsg/wire_serialization_test.go` covers PublishRequest marshal (snake_case keys present, PascalCase keys absent), PublishRequest decode of snake_case wire bodies, PublishReceipt decode of snake_case wire bodies (success + failure_kind branches), and PublishReceipt marshal (typed-Huma-wire emission also snake_case).
- **Side-effect of `make spec-ci` regen** — picked up two stale items unrelated to this fix but in the same generated artifacts: `FanoutPolicy` got snake_case json tags in the genclient (matching the actual struct tags in `extmsg/types.go`), and `ExtMsgGroupEnsureInputBody` gained its missing `FanoutPolicy` field. Both are pre-existing drift, now corrected. `make dashboard-check` and `go test -race ./internal/extmsg/...` are green; the two remaining `internal/api` race failures (`TestStreamSessionPeekAcceptsPeekCapability`, `TestHandleExtMsgOutboundNotifiesDeliveredConversationMembers`) reproduce on `main` and are baseline noise.
- **gc-kvt unblock** — the `Metadata["source_session_id"]` workaround in `internal/extmsg/outbound.go` (declared as `MetadataKeySourceSessionID`) can now be retired by adding a native `SessionID` field to PublishRequest. Not done in this session (not load-bearing for any current consumer); follow-up bead is gc-kvt.

Files touched: `internal/extmsg/types.go`, `internal/extmsg/wire_serialization_test.go` (new), `internal/api/openapi.json`, `docs/schema/openapi.{json,txt}`, `internal/api/genclient/client_gen.go`, `cmd/gc/dashboard/web/src/generated/*` (regenerated by dashboard-check).

### proxy_process URL-prefix fix (gc-cdf)

`GC_SERVICE_URL_PREFIX` injected into proxy_process children was previously the bare mount path (`/svc/<name>`), which 404'd against the supervisor's public router (which only mounts at `/v0/city/<cityName>/svc/<name>`). Slack adapter Phase A cutover hit this on 2026-05-02; the fork has been on legacy nohup ever since.

Fix: new helper `serviceURLPrefix(cityName, svc)` in `internal/workspacesvc/proxy_process.go` composes `/v0/city/<cityName>/svc/<name>` when cityName is non-empty. Empty cityName falls back to bare mount (legacy/test compat — useful so a service spawned from a half-initialized runtime gets a non-mangled prefix even though the URL still won't route).

Tests:
- `TestServiceURLPrefix` — 4 subtests: populated-city wraps correctly, different city different prefix, empty city falls back to bare mount, hyphen-rich names preserved.
- `TestProxyProcessPublishesServiceEnv` — now also asserts the helper subprocess sees `GC_SERVICE_URL_PREFIX = /v0/city/test-city/svc/bridge` end-to-end through the actual env-injection path.

This unblocks **gc-5rz Phase A**, which was completed in this evening's session. See "gc-5rz Phase A cutover" below for details.

### gc-5rz Phase A cutover (completed) — gotchas + fixes

**Two regressions surfaced after the initial cutover; both fixed in the same session before any PL noticed.**

1. **Pack scripts hardcoded `127.0.0.1:8766`.** `slack_intake_common.py` had `DEFAULT_ADAPTER_PUBLISH = "http://127.0.0.1:8766/publish"` and seven manual urllib request sites. Post-cutover that port is dead — the adapter listens on the UDS via supervisor. Fix:
   - `adapter_publish_url()` now derives the proxy URL from `${GC_API_BASE_URL}/v0/city/${city}/svc/slack/publish` by default; `SLACK_ADAPTER_PUBLISH_URL` remains an env override (legacy + tests).
   - New `_adapter_csrf_headers()` helper returns headers including `X-GC-Request: 1`. Required because gc API enforces CSRF on `/svc/<name>/<path>` private mutation endpoints.
   - The 7 manual urllib request sites now use `_adapter_csrf_headers()` instead of literal header dicts. Single sed-style replace covered 5 sites; two DELETE sites had a slightly different dict.
   - Line 230 publish call: `csrf=False` → `csrf=True` (`_request` default).

2. **Stale adapter binary in slack-pack/adapter/.** Two binaries existed:
   - `examples/slack-pack/adapter/gc-slack-adapter` (May 2 16:20) — what supervisor's proxy_process actually executes.
   - `examples/oversight-rig/adapter/gc-slack-adapter` (May 3 09:30) — the canonical build with /publish-file, /identity, /handle-alias, the janitor.
   The proxy_process service was running the stale binary, so /identity returned 404 even after CSRF was satisfied. Fix: `cp -f oversight-rig/adapter/gc-slack-adapter slack-pack/adapter/gc-slack-adapter`, then `kill` the running adapter so supervisor respawns it (1s backoff). New binary picks up; janitor logs `inbound file janitor started: …`. Filed bd **gc-28a** for the longer-term build fix.

**Cutover sequence (now correct after both fixes):**

The `[[service]]` block in `examples/slack-pack/pack.toml` was re-enabled. Sequence:

1. Edited `examples/slack-pack/pack.toml`: replaced the commented-out `[[service]]` placeholder with the live block.
2. Killed the legacy-nohup adapter (PID 1145516).
3. Issued `gc supervisor reload` — supervisor's reconciler picked up the new service, but the first spawn died with `config: missing required env vars: SLACK_WORKSPACE_ID, SLACK_BOT_TOKEN, SLACK_SIGNING_SECRET` because the supervisor's systemd unit didn't carry the secrets in its environ.
4. Added systemd drop-in `/home/ds/.config/systemd/user/gascity-supervisor.service.d/slack-adapter-env.conf` with `EnvironmentFile=-/home/ds/.config/gc-slack-adapter/env`. This is the canonical place for "secrets the supervisor should pass to proxy_process children." The leading `-` makes the file optional so the unit doesn't refuse to start if it's absent.
5. `systemctl --user daemon-reload && systemctl --user restart gascity-supervisor` — supervisor spawned the adapter cleanly. Service state went `degraded` → `starting` → `ready/ready`.
6. Verification:
   - `pgrep -af gc-slack-adapter` → `./adapter/gc-slack-adapter` (relative path, supervised — i.e., NOT the standalone nohup invocation)
   - `ss -ltnp | grep 8775` → adapter has the public Slack-events port
   - `ls /tmp/gcsvc-1000/ee31dfef/` → `slack-*.sock` UDS present
   - `curl /v0/city/ds-research/svc/slack/healthz` → 200
   - Service log line: `registered with gc as ... callback=http://127.0.0.1:8372/v0/city/ds-research/svc/slack/publish (via gc /svc proxy)` — the FIXED prefix.
   - CSRF guard: private mutation endpoints (`/svc/slack/identity` etc.) return 403 unless `X-GC-Request` header is supplied. The CLI knows how to do this; raw curl needs to pass the header.

Things to know going forward:
- Adapter restarts are now `gc supervisor reload` (or natural recycle), not nohup commands. The supervisor's per-service state lives at `/home/ds/gas-city/.gc/services/slack/`.
- The legacy `127.0.0.1:8766` internal listener is GONE. Anything that hardcoded that URL is broken; route through `http://127.0.0.1:8372/v0/city/ds-research/svc/slack/...` instead.
- If you ever need to revert, set `[[service]]` to commented out in `pack.toml`, `gc supervisor reload` to drain it, then `cd examples/slack-pack/adapter && nohup ./run.sh >> /tmp/gc-slack-adapter/run.log 2>&1 &`.

## What's NOT done — open work for next agent

### A. ~~Wait for `files:write` scope~~ — DONE, but the early "verified" smokes were ghost uploads (gc-am2)

`files:write` was granted mid-day and smoked via direct `POST /publish-file`. The receipt looked clean — `delivered:true` with a Slack file_id — but **the files never actually appeared in their target channels** (F0B1G3CFHKN, F0B0X9SALSK, and the PL's first attempt F0B19BU203F were all invisible to humans in Slack). Root cause: the adapter's step-2 upload used `PUT Content-Type: application/octet-stream`. Slack accepts those bytes but treats the resulting file as malformed (empty mimetype, `shares:{}`), and `files.completeUploadExternal` cannot bind a malformed file to the supplied `channel_id` even though it returns `ok:true`. Filed and closed as **gc-am2** in the same session.

Fix: `slackPutFileBytes` now POSTs multipart/form-data with a single `filename` field carrying the file bytes — the canonical shape the pre-signed URL expects. Tests updated. Adapter respawned 2026-05-03 15:08:20 EDT (PID 4028019). Verified end-to-end against `#zelda`: `F0B1CVBRK7U` has `mimetype=image/webp`, `shares=['public']`, `channels=['C0B13JE7M35']`, and appears in `conversations.history`. Real PL-driven smoke from gc-82782 (`gc slack upload --file ... --thread-current` style) is still outstanding — same path, just hasn't been re-driven from a session.

### B. Live-smoke the inbound file download path

`files:read` is granted; the adapter code is in place. Now needs a real Slack file upload to exercise the path. Trigger:
1. Human posts an image into a bound `C`-prefix channel.
2. Adapter log expects: `inbound: chan=... files=N text=...ch`.
3. `ls /tmp/gc-slack-adapter/inbound/<channel-id>/` should show the file.
4. The receiving PL's session log should show `attachments[0].url = "file:///..."` in the system reminder.

Doesn't need code changes — just a real Slack event. (Verified `files:read` grants via `files.info` on the `F0B1G3CFHKN` smoke artifact — `url_private` is reachable.)

### C. Wire up gc-side `HandleOutboundFile` — **promoted to bead gc-j8h (P2)**

Day-to-day coordination gap: the pack currently goes adapter-direct for `gc slack upload`, so file posts skip gc's outbound history (no transcript replay) and miss peer fanout in multi-session rooms. Promoted from "deferred/optional" to tracked work because files are increasingly part of the multi-session coordination surface (rollup attachments, smoke-test files, etc.). Adapter is already capability-ready (`SupportsAttachments: true`); needs new `OutboundFileRequest` + `HandleOutboundFile` in `internal/extmsg/outbound.go`, a Huma-typed gc API endpoint, a `PublishFile` genclient method, and a CLI default switch. See `bd show gc-j8h` for full plan + acceptance.

### D. ~~Channel-name → ID resolution~~ — DONE via Path 1

`channels:read` and `groups:read` were granted mid-day. Smoked via `conversations.list`:

```
public:  C0B0TQMQF2B #all-agent-city, C0B12SXQNF5 #social, C0B13JE7M35 #zelda, ...
private: 0 (bot isn't yet in any private channels — scope works)
```

Any agent can now call `conversations.list` directly via the bot token to resolve `#name` → `C…`. Mayor's 7 AM blocker is fixed without needing the adapter-side cache (Path 2 is no longer warranted — YAGNI).

Recurring channel IDs to remember:
- `#all-agent-city` = `C0B0TQMQF2B`

### E. Keep an eye on the bd issues

- ~~gc-g52 (file retention)~~ — **closed**; janitor shipped + activated (live adapter restarted 12:30:33 EDT).
- ~~gc-txv (slow cos restart)~~ — **closed**; investigated, split into gc-67o.
- ~~gc-67o (phase-timing instrumentation)~~ — **closed + activated** (live supervisor restarted 12:32:33 EDT). First slow-start data point captured.
- ~~gc-9ha (state_sync_recovery sub-phase split)~~ — **closed + activated** (live supervisor restarted 12:59:56 EDT). Awaiting next slow-start to localize provider.Start vs state_sync_recovery.
- ~~**gc-28a**~~ — slack-pack: dual adapter binary locations cause stale-deploy on cutover. **Closed this session** (gc-ywe wave 3): adapter source relocated from `examples/oversight-rig/adapter/` into `examples/slack-pack/adapter/`; pack is self-contained and lift-and-shift-able into upstream gascity-packs.
- ~~**gc-am2** (NEW, P1 bug, fixed same-session)~~ — slack adapter ghost-uploads: PUT octet-stream produces unshareable files; rewrote step 2 to POST multipart. Closed; adapter live with the fix.
- ~~**gc-w1h** (P1 bug)~~ — extmsg PublishRequest/PublishReceipt missing snake_case json tags caused silent data loss on the adapter ↔ gc HTTP wire. **Closed this session**: PublishReceipt now has tags (sibling of the PublishRequest fix landed earlier); regression tests in `internal/extmsg/wire_serialization_test.go`; OpenAPI/genclient/dashboard regenerated. See "extmsg wire-tag audit (gc-w1h)" below.
- ~~**gc-kvt**~~ — **closed wave 1** (commit `bfd64511 fix(extmsg): add native SessionID to PublishRequest, drop metadata workaround`). The metadata-key workaround is gone; PublishRequest carries SessionID natively.

### F. ~~Restart the adapter to activate the janitor~~ — DONE 2026-05-03 12:30:33 EDT

Adapter (PID 1145516) restarted via `cd examples/oversight-rig/adapter && nohup ./run.sh >> /tmp/gc-slack-adapter/run.log 2>&1 &`. Startup log confirmed `inbound file janitor started: store=/tmp/gc-slack-adapter/inbound ttl=168h0m0s interval=1h0m0s`. Healthz, DELETE round-trip, and identity registry all green. Smoke seed cleaned up.

For future restarts, the recipe is unchanged. To confirm the janitor sweeps real files, set `INBOUND_FILE_SWEEP_INTERVAL=10s INBOUND_FILE_TTL=1m` in `~/.config/gc-slack-adapter/env` for a quick smoke before reverting to defaults.

## Decisions still locked (do NOT re-ask the user)

| Decision | Choice | Reaffirmed |
|----------|--------|------------|
| Image inbound: bytes-down vs URL-only | **bytes-down** | shipped this session |
| Image outbound: separate endpoint | **/publish-file** | shipped |
| files.upload legacy vs v2 | **getUploadURLExternal + completeUploadExternal** | shipped |
| Image storage retention | **none for v1; bd follow-up filed** | gc-g52 |
| Where to put `PublishFileRequest` | **adapter-only struct; no gc-side yet** | shipped (deferred gc-side per C above) |
| Pack command name | **`gc slack upload`** | shipped |
| HANDLE_PREFIX | **`@`** | unchanged |
| Colon after handle | **optional** | unchanged |

## Smoke tests for next agent

```bash
# === Outbound file (gc-5rz Phase A; verified post-cutover) ===
# Use the proxy URL via the python helper (sets X-GC-Request CSRF for you):
echo "test" > /tmp/smoke-up.txt
GC_API_BASE_URL=http://127.0.0.1:8372 GC_CITY_NAME=ds-research SLACK_WORKSPACE_ID=T0B17700WUW python3 -c "
import sys; sys.path.insert(0, '/home/ds/gascity/examples/slack-pack/scripts')
import slack_intake_common as common
print(common.upload_via_adapter(
  session_id='smoke', file_path='/tmp/smoke-up.txt',
  conversation_id='C0B0TQMQF2B', kind='room',
  initial_comment='smoke', filename='smoke-up.txt', title='smoke-up.txt'))
"
# expect: {'delivered': True, 'file_id': 'F...', 'conversation': {...}}
# Verified 2026-05-03 13:30:33 EDT — F0B0X9SALSK landed in #all-agent-city.

# Or via raw curl through the proxy with explicit CSRF header:
curl -s -X POST -H "Content-Type: application/json" -H "X-GC-Request: 1" \
  -d '{
    "session_id":"smoke",
    "conversation":{"scope_id":"ds-research","provider":"slack","account_id":"T0B17700WUW","conversation_id":"C0B0TQMQF2B","kind":"room"},
    "file_path":"/tmp/smoke-up.txt",
    "initial_comment":"smoke"
  }' \
  http://127.0.0.1:8372/v0/city/ds-research/svc/slack/publish-file | jq
# expect: {... "delivered": true, "file_id": "F..." ...}

# Or via the pack from a PL session that has a bound channel:
gc slack upload --file /tmp/smoke-up.txt
# expect same shape via stdout

# === Inbound file (real Slack event needed) ===
# Human uploads image in a bound channel.
ls /tmp/gc-slack-adapter/inbound/<channel-id>/
# Adapter log lives at the supervisor's per-service log post-cutover:
tail -20 /home/ds/gas-city/.gc/services/slack/logs/service.log
# (Pre-cutover, the legacy nohup adapter logged to /tmp/gc-slack-adapter/run.log.
#  After gc-5rz Phase A that file stops at 12:51:24 EDT 2026-05-03.)
# expect log line: inbound: ... files=N text=...ch

# === Channel name → ID resolution ===
SLACK_BOT_TOKEN=$(cat /proc/$(pgrep -f 'gc-slack-adapter$')/environ | tr '\0' '\n' | grep "^SLACK_BOT_TOKEN=" | cut -d= -f2)
curl -s -X POST -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
  -d "exclude_archived=true&types=public_channel,private_channel" \
  https://slack.com/api/conversations.list | jq '.channels[] | {id, name}'

# === DELETE smoke (works now via proxy + CSRF) ===
curl -s -X POST -H "Content-Type: application/json" -H "X-GC-Request: 1" \
  -d '{"session_id":"smoke-dele","username":"X"}' \
  http://127.0.0.1:8372/v0/city/ds-research/svc/slack/identity
curl -s -X DELETE -H "X-GC-Request: 1" \
  "http://127.0.0.1:8372/v0/city/ds-research/svc/slack/identity?session_id=smoke-dele"
# expect: {"stored":true,"session_id":"smoke-dele"}
#         {"removed":true,"existed":true,"session_id":"smoke-dele"}

# === Phase-timing post-restart (after supervisor restart picks up gc-67o) ===
# Trigger a session reset and watch for phases= segment in the lifecycle log:
grep "session lifecycle: op=start" /home/ds/.gc/supervisor.log | tail -5
# look for: ... phases=[start_call=Xs post_start_observe=Yms commit_refresh=Zms]
```

## How to verify if returning fresh

```bash
# Supervisor + adapter health
systemctl --user status gascity-supervisor 2>&1 | head -5
pgrep -af "gc-slack-adapter$"
curl -s -o /dev/null -w "gc API health: %{http_code}\n" http://127.0.0.1:8372/v0/cities

# New endpoints alive
curl -s -X POST -H "Content-Type: application/json" -H "X-GC-Request: 1" \
  -d '{"session_id":"probe","username":"x"}' \
  http://127.0.0.1:8372/v0/city/ds-research/svc/slack/identity | jq
curl -s -X DELETE -H "X-GC-Request: 1" \
  "http://127.0.0.1:8372/v0/city/ds-research/svc/slack/identity?session_id=probe" | jq

# Mayor 7 AM debrief still scheduled
systemctl --user list-timers --all | grep mayor-7am

# Recent activity
tail -30 /tmp/gc-slack-adapter/run.log

# PL sweep log
tail -40 /tmp/pl-restart-sweep.log
```

## Files in scope on this branch (now committed + pushed)

The previously-uncommitted file list is now in commits `070f39c1..e7c51e6d` on `fork/feat/oversight-rig-pack`. Use `git log --stat 070f39c1..e7c51e6d` to inspect. High-level summary (commit → file groups):

```
6416c171 fix(workspacesvc) gc-cdf
  internal/workspacesvc/proxy_process.go         + serviceURLPrefix helper, /v0/city/<name>/svc/<n> prefix
  internal/workspacesvc/proxy_process_test.go    + TestServiceURLPrefix (4 subtests), env injection assertion
  examples/slack-pack/pack.toml                  - re-enables [[service]] block

fb132d79 feat(cmd/gc) gc-9ha
  cmd/gc/session_lifecycle_parallel.go           + startPhaseTimings, state_sync_recovery sub-phase, variadic phases tail
  cmd/gc/session_lifecycle_phases_test.go        + new (TestStartPhaseTimingsFormatLog, TestLogLifecycleOutcomeWithPhases)

57321e1f fix(extmsg) gc-w1h
  internal/extmsg/types.go                       + snake_case tags on PublishReceipt; AdapterCapabilities PascalCase docs
  internal/extmsg/wire_serialization_test.go     + new
  internal/api/openapi.json + docs/schema/openapi.{json,txt}    regen
  internal/api/genclient/client_gen.go                          regen
  cmd/gc/dashboard/web/src/generated/{schema.d.ts,types.gen.ts} regen

cd372627 feat(extmsg) gc-kvt prep
  internal/extmsg/outbound.go                    + MetadataKeySourceSessionID, source_session_id injection in HandleOutbound

b8abb72d feat(slack-pack) identity DELETE + bidirectional files + new commands
  examples/oversight-rig/adapter/main.go         + DELETE handlers, /publish-file, downloadSlackFiles, gc-am2 multipart POST fix
  examples/oversight-rig/adapter/main_test.go    + TestHandlePublishFile, TestSafeFilename, TestDownloadSlackFiles, registry-delete tables, sweep + retention
  examples/slack-pack/scripts/slack_intake_common.py             + remove_identity, remove_handle_alias, upload_via_adapter, csrf helpers
  examples/slack-pack/scripts/slack_chat_reply_current.py        channel-id prefix → kind detection
  examples/slack-pack/scripts/slack_chat_{handle_alias,identity,publish_to_channel,react,upload}.py   new
  examples/slack-pack/commands/{handle-alias,identity,publish-to-channel,react,upload}/ + .sh        new

57d767cc docs(oversight-rig)
  examples/oversight-rig/agents/chief-of-staff/prompt.template.md    + address-by-handle (cross-channel)
  examples/oversight-rig/agents/project-lead/prompt.template.md      + identity register, react protocol, files subsection

34cd2344 docs(oversight-rig) HANDOFF refresh

e7c51e6d chore testenv regen
  examples/oversight-rig/adapter/testenv_import_test.go              new (lint sentinel)
  examples/testenv_import_test.go                                    + "Code generated" header
```

Deployment-local (NOT in repo, unchanged this session):
- `/tmp/gc-slack-adapter/identities.json` — 9+ personas after PL re-registration sweep
- `/tmp/mayor-7am-debrief.sh` — one-shot script invoked by systemd-user timer at 7 AM
- `/tmp/pl-restart-sweep-v2.sh`, `/tmp/pl-restart-sweep.log` — fixed-delay sweep artifacts
- `/home/ds/.config/systemd/user/gascity-supervisor.service.d/slack-adapter-env.conf` — adds `EnvironmentFile=-/home/ds/.config/gc-slack-adapter/env` to the supervisor unit so secrets propagate to proxy_process children. Required for gc-5rz Phase A; without it the slack adapter dies on missing env at startup.

## Items NOT to do

- **DO NOT touch `internal/orders/override.go`** — gc-a3s PR worktree owns it.
- **ASK BEFORE restarting the supervisor or live adapter.** Both have on-disk code ahead of the running binary at various points in the rollout; restarts disturb live Slack flow / running sessions. The "Up next" block calls these out explicitly when relevant.

## Findings from earlier sessions (carried forward)

1. **`orders.overrides` rig-scoping is silently no-op for per-rig orders** — bd gc-a3s, separate worktree at `/home/ds/gascity-pr-gc-a3s` (branch `fix/gc-a3s-orders-overrides-rig-scope`).
2. ~~**proxy_process URL contract is broken on the SDK side**~~ — **fixed and shipped** (gc-cdf — landed on this branch as commit 6416c171; PR worktree `gascity-pr-gc-cdf` carries the standalone fix branch).
3. **`bin/claude-account` JSON writes were unserialized** — gc-arr, closed.

## Resolved this session: `core.hooksPath` restored

`git config core.hooksPath .githooks` ran at the start of this session; all three commits this session went through the full pre-commit pipeline. bd's lifecycle hooks under `.beads/hooks/` use a different mechanism and continued to fire (auto-export warnings about `git add` failing on the unrelated `.beads/issues.dolt/` are cosmetic and pre-existing).

## Resolved this session: nested PR worktrees removed

`/home/ds/gascity/gascity-gascity-pr/` and `/home/ds/gascity/gascity-gascity-pr-1/` were nested git worktrees inside the main repo's working tree, causing `TestDocDirCoverage` and `TestNoBdExecOutsideBeads` to flag files inside them as repo-root violations. Both removed via `git worktree remove --force`. The corresponding PRs are already open from their proper sibling worktrees (`gascity-pr-gc-w1h`, `gascity-pr-gc-cdf`, `gascity-pr-gc-a3s` etc. at `/home/ds/gascity-pr-*`). If new PRs need staging worktrees, place them as siblings of `/home/ds/gascity` (matching the `/home/ds/gascity-pr-<bd-id>` pattern), not inside it.
