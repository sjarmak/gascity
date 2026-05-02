# Oversight-rig handoff — 2026-05-02 (post-rollout)

## State

Two-way Slack ↔ gc oversight loop is **fully working end-to-end**
across the city DM and 7 per-rig channels. All four routing branches
in chief-of-staff have been exercised against real Slack inbound:

- match-and-route-to-project-lead (`ack GEO-rjz` → mailed
  `geo/oversight-rig.project-lead`, closed bead with `resolved` label)
- no-match-route-to-mayor (raw text → mailed mayor, no guess)
- quiet-tick (no system-reminder, empty mail)
- prime (first awake read of the prompt)

A new `examples/slack-pack/` scaffolds a slack-side parallel of
`gastownhall/gascity-packs/discord` with `gc slack bind-dm`,
`gc slack reply-current`, and `gc slack bind-room` (with
peer-fanout policy + `--binding-owner` plumbing).

The supervisor was restarted onto the rebuilt binary this session.
Per-rig rollout for 7 of 13 rigs is live — outbound publish,
peer-fanout to cos, and inbound `kind=room` classification all
verified end-to-end.

## Live runtime

- **Supervisor** PID **2575673** (rebuilt `/tmp/gc` from this branch;
  accepts `fanout_policy` on `POST /extmsg/groups`, the provider-neutral
  nudge fix in `internal/api/handler_extmsg.go`, and now injects
  `GC_API_BASE_URL` + `GC_CITY_NAME` into order exec env via
  `cmd/gc/order_store.go:orderExecEnv`).
- **Slack adapter** PID **2582270**, registered as
  `slack/T0B17700WUW`. Public `:8775`, internal `127.0.0.1:8766`.
  Log: `/tmp/gc-slack-adapter/run.log`. Tailscale Funnel still on
  (`:443 → :8775`).
- **Slack app event subscriptions** include `message.im` and
  `message.channels`. **`message.groups` is NOT subscribed** — if any
  rig's channel is private (`G`-prefix id), add it in
  api.slack.com → Event Subscriptions and reinstall.
- **Slack pack** imported via local `[imports.slack]` in `city.toml`.
  `gc slack {bind-dm,bind-room,reply-current}` available.
- **chief-of-staff** session `gc-83347` (alias `oversight-rig.cos`),
  bound to DM `D0B0TTS550F`, also a peer in 7 room groups (see
  table below). cos still uses its handwritten "About Reply
  Instructions" section — has not been switched to compose the
  `slack-v0` template fragment from the new pack. Cos hit its
  Anthropic plan limit during the rollout
  ("resets May 3, 3pm America/New_York"); it'll resume routing on
  next available capacity.
- **13 project-leads** — exactly one per rig, all `awake (always)`.

### Per-rig room bindings (LIVE)

| rig | gc-id | channel id | binding id | group id |
|---|---|---|---|---|
| codeprobe | gc-82316 | C0B1A0CKEH0 | gc-84162 | gc-84154 |
| codescalebench | gc-82318 | C0B248JP54Y | gc-84110 | gc-84102 |
| enterprisebench | gc-82313 | C0B1NSHTSKT | gc-84136 | gc-84129 |
| gascity | gc-82783 | C0B1NSK4N3T | gc-84144 | gc-84138 |
| geo (new) | gc-77139 | C0B13JH8T35 | gc-84152 | gc-84146 |
| scix-experiments | gc-82781 | C0B17TXMT1C | gc-84118 | gc-84112 |
| zeldascension | gc-82782 | C0B13JE7M35 | gc-84127 | gc-84120 |

All bindings: peer-fanout enabled, mode=launcher, participants are
`oversight-rig.cos` (alias) + `<rig>/oversight-rig.project-lead`
(alias), binding-owner is the project-lead's gc-id (so
`resolve_rig_channel.py` finds the binding via its gc-id-keyed
lookup against `/extmsg/bindings?session_id=<gc-id>`).

`resolve_rig_channel.py` returns the dedicated room for all 7 rigs;
verified by direct invocation. Outbound smoke test against
`#gascity` (`gc-82783` → `C0B1NSK4N3T`) returned `Delivered: True`
and peer-fanout to cos fired in <1s.

### Remaining 6 rigs (no channel yet)

`mcp-ax`, `background-agents`, `live_docs`, `migration-evals`,
`agent-diagnostics`, `code-intelligence-digest`. For each:

1. Create a Slack channel (any name; a consistent prefix like
   `oversight-<rig>` keeps the sidebar grep-able).
2. Invite `@gc-oversight` to the channel.
3. Run:
   ```
   /tmp/gc slack bind-room <Cxxx> \
       oversight-rig.cos <rig>/oversight-rig.project-lead \
       --enable-peer-fanout \
       --binding-owner <gc-id-of-project-lead>
   ```
   Project-lead gc-ids: agent-diagnostics=gc-83263,
   gascity=gc-82783 (already bound),
   code-intelligence-digest=gc-82780, background-agents=gc-82315,
   mcp-ax=gc-82314, live_docs=gc-82312, migration-evals=gc-82311.

### Canary on `C0B0TQMQF2B` (= `#all-agent-city`)

The original canary binding `gc-83781` (geo PL → `C0B0TQMQF2B`) is
still active in addition to the new dedicated geo binding
`gc-84152` (geo PL → `C0B13JH8T35`). `resolve_rig_channel.py` picks
the most recent active binding, so geo escalates now route to the
dedicated channel. The canary binding is harmless extra membership;
unbind with `POST /v0/city/ds-research/extmsg/unbind` for session
`gc-77139` + conversation `C0B0TQMQF2B` if you want to clean it up.

## Known formatting gotcha (Slack ≠ Discord)

Slack's mrkdwn uses **single asterisks** for bold (`*text*`).
Double asterisks (`**text**`) render literally. The first
verification message landed in Slack as
`**slack-pack:** verifying ...` with the asterisks visible to the
human. The `slack-v0` template fragment now spells this out
explicitly so future agent-authored replies use the right syntax.
The Go adapter does NOT translate Markdown bold; the right surface
for this is the prompt-fragment level, where each provider can
specify its own formatting contract.

## What's on the branch

```
<this commit> feat(slack-pack): bind-room + fanout policy plumbing
8495e4d7 feat(slack-pack): scaffold + bind-dm + reply-current
054b92a6 docs(oversight-rig): handoff after end-to-end validation
3f95d85f fix(oversight-rig): drop redundant min/max_active_sessions on project-lead
e9c07d31 feat(oversight-rig): adapt chief-of-staff to system-reminder delivery
16a82b6d fix(api): make extmsg inbound system-reminder provider-neutral
9ae8003c docs(oversight-rig): handoff state for next agent
... (earlier slack-adapter commits)
```

## Cutover (DONE — twice this branch)

Cutover #1 (last session) brought the supervisor onto the `bind-room` +
fanout-policy build. Cutover #2 (this session) brought it onto the
order-exec env-injection build (item 4c). Current supervisor PID
2575673, current adapter PID 2582270 (see Live runtime).

Sequence used (for reference if it has to happen again):

```
/tmp/gc stop /home/ds/gas-city
/tmp/gc supervisor stop
/tmp/gc supervisor start
/tmp/gc start /home/ds/gas-city
pkill -f "gc-slack-adapter$"
( cd /home/ds/gascity/examples/oversight-rig/adapter \
  && nohup ./run.sh > /tmp/gc-slack-adapter/run.log 2>&1 & disown )
```

`gc stop` takes ~3min on a city with ~30 background sessions
(stops in waves of 1–2 per second). Sessions reattach on `gc start`.
All cos + project-lead conversation bindings survived intact across
both cutovers; the canary binding `gc-83781` survived as well.

## Findings from the cutover canary

Three things tripped us up; capture them so the next agent doesn't
hit the same wall:

1. **The HANDOFF's prior bind-room example was wrong.** It used
   `oversight-rig.mayor` as a participant, but the pack only defines
   `chief-of-staff` (city scope) and `project-lead` (rig scope). There
   is no `mayor` template anywhere in the oversight-rig pack — the
   existing `gc-2568 mayor` session is unrelated, from a different
   pack. Don't pair `oversight-rig.mayor` in any future bind-room.
   For peer-fanout against cos, use the alias `oversight-rig.cos`.

2. **Participant `session_id` should be the alias, not the template.**
   Using the template name (`oversight-rig.chief-of-staff`) caused
   `extmsgNotifyMembers` to materialize a NEW session under that name
   instead of routing to the existing cos at `gc-83347` (whose alias
   is `oversight-rig.cos`). The alias resolves correctly. The
   slack-pack `bind-room` README/docs should call this out, and the
   handle-override convention should default to alias-based selectors.
   (We had to close the orphan `gc-83796` session that got materialized
   from the bad selector.)

3. **`bind-room` does NOT create a `SessionBindingRecord`.** It only
   creates the group + participants. `gc /extmsg/outbound` requires a
   binding to resolve the conversation to a publishing session — so
   peer-fanout works (it reads from group membership) but outbound
   publish does not, until you separately POST to `/extmsg/bind` for
   one of the participants. We worked around this manually in the
   canary; doing it 13 times for 13 rigs is fragile. See Open work
   item 4b.

Local-only (not for commit): `city.toml` has
`[[patches.agent]] name="chief-of-staff" provider="claude-2"` and
`[imports.slack] source = .../examples/slack-pack`. Backups:
`city.toml.bak-pre-cos-patch-*`, `city.toml.bak-pre-restore-*`.

## Open work, in priority order

1. ~~**Switch `gc slack reply-current` to publish through gc
   `/extmsg/outbound`**~~ — DONE this session. `reply-current` now
   defaults to `--via gc` (POST `/v0/city/{city}/extmsg/outbound`),
   which goes through `humaHandleExtMsgOutbound` → the registered
   HTTP adapter's `/publish` and fires `extmsgNotifyMembers` for peer
   sessions. `--via adapter` retains the old direct-to-adapter path
   for diagnostics. Test coverage in
   `examples/slack-pack/tests/test_slack_chat_reply_current.py`
   (3 cases; 18/18 pack tests pass). The live supervisor (PID 2656160)
   already serves `/extmsg/outbound`, so no supervisor restart is
   needed for this change to take effect — pack scripts hit gc over
   HTTP at runtime.
2. **Absorb the Go adapter into the slack pack as a
   `[[service]] proxy_process`.** Right now the adapter is run by
   hand and managed externally; the pack should own its lifecycle
   like discord-interactions does. This also gives you `gc service
   list` integration and tenant-vs-public publication tiers.

   **Scope note (added on second look):** bigger than the description
   implies. `internal/workspacesvc/proxy_process.go` requires the
   service to listen on a Unix domain socket at `GC_SERVICE_SOCKET`;
   gc reverse-proxies HTTP through `/svc/{name}` to that socket. Our
   Slack adapter today binds TCP `:8775` (public webhook) and `:8766`
   (internal `/publish`), with Tailscale Funnel pinned to TCP
   `:443 → :8775`. A clean absorption needs:
   - (a) UDS-listener mode in the Go adapter (the public-webhook path
     can stay TCP if Funnel must keep terminating directly, but the
     `/publish` callback should move to UDS so gc owns it),
   - (b) a decision on whether the public Slack webhook ingress
     migrates through gc (`/svc/slack/webhook`) or stays on the
     direct TCP path (Tailscale Funnel reconfig either way),
   - (c) env-var plumbing: signing secret / bot token / workspace id
     flow from pack `[[service]]` config + `GC_SERVICE_*` instead of
     `~/.config/gc-slack-adapter/env`.

   Worth designing on paper before coding. Until then, keep
   `examples/oversight-rig/adapter/run.sh` as-is.
3. **Switch the chief-of-staff prompt to compose `slack-v0`**, and
   decide whether cos should call `gc slack reply-current` directly
   for "ack" replies (today the prompt forbids it). One-line
   prompt change once the design call is made.
4. ~~**Per-rig Slack channels — 4a, 4b, 4c blockers**~~ — ALL THREE
   DONE this session. 7 of 13 rigs now have dedicated channels with
   live bindings (see "Per-rig room bindings" table above). Resolver,
   peer-fanout, and inbound classification all verified end-to-end.

   **4a. Verify inbound `kind=room`.** DONE.
   First attempt with the canary channel returned no inbound — the
   bot's Slack app event subscription only had `message.im` (DMs)
   and not `message.channels` (public channel posts). User added
   `message.channels` and reinstalled the app; next post in
   `#all-agent-city` (= `C0B0TQMQF2B`) arrived at the adapter and
   was routed to gc-77139 via the kind=room binding. Peer-fanout
   to cos at gc-83347 fired ~700ms after the inbound. Note: the
   `extmsg.inbound` event payload doesn't carry `.kind` directly
   (its shape is `{provider, conversation_id, actor, target_session}`),
   so the verification was via routing behavior — peer-fanout only
   fires through room group memberships, so successful peer-fanout
   proves the conversation was classified as room.

   **Reminder for future channels:** if any new channel is private
   (`G`-prefix id), also subscribe `message.groups` in api.slack.com
   and reinstall.

   **4b. `gc slack bind-room --binding-owner SESSION`.** DONE.
   `slack_chat_bind_room.py` now POSTs `/extmsg/bind` after creating
   group + participants. Initial validation required binding-owner
   to match a participant alias, but during rollout we discovered
   that `resolve_rig_channel.py` looks up bindings by **gc-id**
   (from the sessions list), so the binding must be created with
   the gc-id, not the participant alias. Validation was relaxed:
   `--binding-owner` now accepts any session id verbatim; the
   docstring spells out when to pass alias vs gc-id. Test was
   updated to assert the canonical "alias participants + gc-id
   owner" shape instead of the participant-membership constraint.
   20/20 pack tests pass. README updated.

   **4c. `escalate-rollups` order exit-1.** DONE + LIVE.
   Root cause: the controller's `orderExecEnv` (in
   `cmd/gc/order_store.go`) never injected `GC_API_BASE_URL` or
   `GC_CITY_NAME` for exec orders. Supervisor log confirmed
   `deliver-rollup.sh: line 54: GC_API_BASE_URL: GC_API_BASE_URL must be set`.

   Fix:
   - `orderExecEnv` now injects `GC_CITY_NAME` from
     `loadedCityName(cfg, cityPath)` and `GC_API_BASE_URL` from a
     hookable `orderExecAPIBaseURLHook` defaulting to
     `supervisorAPIBaseURL()`. When no supervisor config is found
     (one-off CLI runs without supervised city), the URL is left
     unset rather than guessed at — pack scripts surface the
     missing var via their own `${VAR:?}` checks.
   - Both keys added to `mergeRuntimeEnv`'s strip list so inherited
     stale values can't poison child orders.
   - Two new tests in `cmd/gc/order_dispatch_test.go`:
     `TestOrderExecEnvInjectsCityNameAndAPIBaseURL` and
     `TestOrderExecEnvOmitsAPIBaseURLWhenHookEmpty`.
   - Full `go test ./cmd/gc/` clean (87s).

   Verified live after the cutover: `escalate-rollups:rig:enterprisebench`
   now emits `order.completed` (was `order.failed exit 1`). The
   retry loop went silent — no open undelivered escalates anywhere
   right now (`EnterpriseBench-2wk` was already closed/resolved by
   the time the order ran post-fix).

   **Per-rig delivery is self-driving for the 7 bound rigs.** The 6
   remaining rigs need channels — see "Remaining 6 rigs" above for
   the recipe.
5. **Adapter as systemd user service** so it survives reboot
   (subsumed by item 2 once that lands; until then,
   `adapter/SETUP.md` § "Running the adapter as a service").
6. **`bin/claude-account` atomic-write race** — original blocker
   that ate account4's `.claude.json`. Fix: `tmp + rename` under a
   flock. File as a Gas City bead.
7. **Re-enable `patrol-project-leads`** in `city.toml` once
   continuous 15m triage is wanted (currently disabled via
   `[[orders.overrides]]`).
8. ~~**Update older `examples/oversight/chief-of-staff` prompt**~~ —
   DONE this session. The "When the Human Replies" section in
   `examples/oversight/agents/chief-of-staff/prompt.template.md` no
   longer claims inbound replies arrive via `gc mail inbox`; it now
   documents the system-reminder injection model and tells cos to
   ignore embedded "To reply in <provider>, run …" hints.

## How to verify if returning fresh

```bash
# Supervisor + adapter on rebuilt binaries
/tmp/gc supervisor status   # expect PID 2575673 (or current)
pgrep -af gc-slack-adapter  # expect a single PID, registered slack/T0B17700WUW

# Pack is loaded
/tmp/gc slack --help        # bind-dm, bind-room, reply-current

# Loop is live
/tmp/gc events --city /home/ds/gas-city --type extmsg.inbound --since 5m
/tmp/gc session list --city /home/ds/gas-city | grep -E "chief-of-staff|project-lead"

# Bindings for the 7 bound rigs
for sid in gc-82316 gc-82318 gc-82313 gc-82783 gc-77139 gc-82781 gc-82782; do
  curl -s "http://127.0.0.1:8372/v0/city/ds-research/extmsg/bindings?session_id=$sid" \
    | jq -r --arg sid "$sid" '.items[0] | "\($sid) -> conv=\(.Conversation.conversation_id) kind=\(.Conversation.kind) status=\(.Status)"'
done

# DM path (oversight-rig.cos): send a Slack DM to gc-oversight, then:
/tmp/gc session peek gc-83347
# Expect: clean system-reminder, cos routes the reply.

# Direct DM reply via pack:
/tmp/gc slack reply-current --session gc-83347 \
  --conversation-id D0B0TTS550F \
  --body "*test:* hello from the slack pack"
# Expect: delivered: true, message in Slack DM.

# Room publish via gc /extmsg/outbound (gascity binding):
curl -s -X POST -H "Content-Type: application/json" -H "X-GC-Request: smoke" \
  -d '{"session_id":"gc-82783","conversation":{"scope_id":"ds-research","provider":"slack","account_id":"T0B17700WUW","conversation_id":"C0B1NSK4N3T","kind":"room"},"text":"*smoke:* hello gascity"}' \
  http://127.0.0.1:8372/v0/city/ds-research/extmsg/outbound \
  | python3 -c 'import json,sys; print("Delivered:", json.load(sys.stdin)["Receipt"]["Delivered"])'
# Expect: Delivered: True

# Rig-channel resolver — should return a dedicated room for each
# of the 7 bound rigs:
for rig in codeprobe codescalebench enterprisebench gascity geo scix-experiments zeldascension; do
  printf "%-22s " "$rig"
  GC_API_BASE_URL=http://127.0.0.1:8372 GC_CITY_NAME=ds-research \
    python3 /home/ds/gascity/examples/oversight-rig/assets/scripts/resolve_rig_channel.py "$rig" \
    | jq -r '"sid=\(.session_id) conv=\(.conversation.conversation_id) kind=\(.conversation.kind)"'
done
```

## Key files

- `examples/slack-pack/` — pack scaffold (`bind-dm`, `bind-room`,
  `reply-current`)
- `examples/slack-pack/README.md` — port checklist & architecture
- `examples/slack-pack/scripts/slack_chat_bind_room.py` — supports
  `--binding-owner` (item 4b done; validation relaxed to accept
  gc-id when participants are aliases)
- `examples/slack-pack/scripts/slack_chat_reply_current.py` — defaults
  to `--via gc` so peer fanout fires
- `examples/oversight-rig/agents/chief-of-staff/prompt.template.md`
- `examples/oversight-rig/assets/scripts/deliver-rollup.sh` —
  per-rig delivery via resolver + legacy env-var fallback
- `examples/oversight-rig/assets/scripts/resolve_rig_channel.py` —
  rig → publishing target resolver (12 unit tests)
- `examples/oversight-rig/orders/escalate-rollups.toml` — the order
  whose exit-1 was fixed in 4c (controller now injects
  `GC_API_BASE_URL` + `GC_CITY_NAME`)
- `cmd/gc/order_store.go` — `orderExecEnv` injects city name +
  api base url; `orderExecAPIBaseURLHook` is the testing seam
- `internal/api/handler_extmsg.go` + `_test.go` — provider-neutral
  nudge; peer-fanout via `extmsgNotifyMembers`
- `internal/extmsg/binding_service.go` — note: only one active
  binding per conversation; `Bind` returns `ErrBindingConflict`
  if you try to rebind to a different session
- `examples/oversight-rig/adapter/SETUP.md` — port-collision note
  for this host (`:8775`/`:8776`)
