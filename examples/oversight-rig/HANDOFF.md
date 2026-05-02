# Oversight-rig handoff — 2026-05-02 (post-cutover)

## State

Two-way Slack ↔ gc oversight loop is **fully working end-to-end**.
All four routing branches in chief-of-staff have been exercised
against real Slack inbound:

- match-and-route-to-project-lead (`ack GEO-rjz` → mailed
  `geo/oversight-rig.project-lead`, closed bead with `resolved` label)
- no-match-route-to-mayor (raw text → mailed mayor, no guess)
- quiet-tick (no system-reminder, empty mail)
- prime (first awake read of the prompt)

A new `examples/slack-pack/` scaffolds a slack-side parallel of
`gastownhall/gascity-packs/discord` with `gc slack bind-dm`,
`gc slack reply-current`, and `gc slack bind-room` (with
peer-fanout policy plumbing).

The cutover to the rebuilt supervisor + adapter happened this
session. The canary `bind-room` against `C0B0TQMQF2B` works
end-to-end for outbound publish + peer fanout.

## Live runtime

- **Supervisor** PID **3855171** (rebuilt `/tmp/gc` from this branch;
  accepts `fanout_policy` on `POST /extmsg/groups`, plus the
  provider-neutral nudge fix in `internal/api/handler_extmsg.go`).
- **Slack adapter** PID **3879872**, registered as
  `slack/T0B17700WUW`. Public `:8775`, internal `127.0.0.1:8766`.
  Log: `/tmp/gc-slack-adapter/run.log`. Tailscale Funnel still on
  (`:443 → :8775`). Restarted from the rebuilt binary at
  `examples/oversight-rig/adapter/gc-slack-adapter` so room-kind
  classification is in place. **Inbound classification has NOT yet
  been verified by a live human post in a room — see Open work item 4a.**
- **Slack pack** imported via local `[imports.slack]` in `city.toml`.
  `gc slack {bind-dm,bind-room,reply-current}` available.
- **chief-of-staff** session `gc-83347` (alias `oversight-rig.cos`),
  bound to `D0B0TTS550F` via binding `gc-83357` (gen 6), running on
  `claude-2`. cos still uses its handwritten "About Reply
  Instructions" section — it has not yet been switched to compose
  the `slack-v0` template fragment from the new pack (deliberate;
  cos's design today is "do not reply directly", and switching it
  to use `gc slack reply-current` is a scope decision).
- **13 project-leads** — exactly one per rig, all `awake (always)`.
  Pool/named-session duplicate collision is fixed.
- **Canary bind-room** still active on `C0B0TQMQF2B`:
  - Group `gc-83767`, mode=launcher, peer-fanout=true.
  - Participants: `cos` → `oversight-rig.cos`,
    `geo-project-lead` → `geo/oversight-rig.project-lead`.
  - Conversation binding `gc-83781`: room `C0B0TQMQF2B` →
    session `gc-77139` (geo project-lead). This binding makes the
    room a valid target for `/extmsg/outbound`.
  - Three test publishes landed (`Delivered: true`); peer-fanout to
    cos at `gc-83347` confirmed (last_active jumped to "0s ago"
    immediately after each publish).
  - Leave it in place — useful for next session without setting up
    a new channel. To unbind: `DELETE /extmsg/unbind` for session
    `gc-77139` + the `oversight-rig.cos` membership goes away
    automatically when the participant is removed.

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

## Cutover (DONE this session)

The supervisor + adapter restart happened. New supervisor PID 3855171,
new adapter PID 3879872 (see Live runtime above).

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

Sessions reattach on `gc start`. cos's prior conversation binding
(`gc-83357` → `D0B0TTS550F`) survived the restart unchanged.

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
4. **Per-rig Slack channels — three blockers before rollout.** The
   delivery side is ready: `resolve_rig_channel.py` finds the
   bead's rig's project-lead, reads its active extmsg binding,
   publishes through `/extmsg/outbound`. 12 unit tests in
   `examples/oversight-rig/assets/scripts/test_resolve_rig_channel.py`.
   Legacy `GC_OVERSIGHT_*` env vars are fallback-only.

   But three things need to land before binding 13 rigs:

   **4a. Verify inbound room-kind classification.** Adapter binary
   is rebuilt and the binary on disk includes the
   `channel_type → room/dm` patch, but we never confirmed a real
   human post in a room arrives at `/extmsg/inbound` with
   `kind=room`. Test:

   ```
   # In Slack, post a message in #C0B0TQMQF2B (the canary channel).
   /tmp/gc events --type extmsg.inbound --since 2m \
     | jq -r '.payload.message.conversation.kind'
   # Expect: "room". If "dm", the binary swap didn't take effect or
   # the classifier has a bug — re-check adapter PID + ldd.
   ```

   **4b. `gc slack bind-room` must also bind one publisher.** The
   script today only POSTs `/extmsg/groups` + `/extmsg/participants`.
   Add a `--binding-owner SESSION` flag (or default to the last
   participant) that also POSTs `/extmsg/bind` for that session.
   Without it, every rig requires a manual curl to make outbound
   publishes work. Small change in
   `examples/slack-pack/scripts/slack_chat_bind_room.py`; mirror
   the bind shape we used in the canary:

   ```
   POST /v0/city/{city}/extmsg/bind
   { "session_id": "<gc-id-of-publisher>",
     "conversation": { ...root_conversation... } }
   ```

   Use the `gc-id`, not the alias — alias-based binding has an
   open question about resolution semantics. Add a unit test
   asserting bind-room emits exactly the right three calls.

   **4c. `escalate-rollups` order has been exec-failing since
   2026-05-01 22:44** with `exit status 1` (predates this session's
   work; visible in `.gc/events.jsonl` from yesterday). Even with
   rig channels bound, the automatic delivery loop won't fire. Run
   `bash $GC_PACK_DIR/assets/scripts/deliver-rollup.sh` manually
   under the controller's spawn env first to reproduce; suspect the
   controller doesn't pass through `GC_API_BASE_URL` /
   `GC_CITY_NAME`, which the script's `:?` checks at the top would
   exit 1 on. If so: either wire `[order.env]` in
   `escalate-rollups.toml` or have the controller inject those by
   default.

   Once 4a, 4b, 4c land: invite the bot to N Slack channels, run
   `gc slack bind-room <Cxxx> oversight-rig.cos <rig>/oversight-rig.project-lead --enable-peer-fanout --binding-owner <rig>/oversight-rig.project-lead`
   per rig, and the per-rig delivery pipeline becomes self-driving.

   The user's stated end-state.
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
/tmp/gc supervisor status   # expect PID 3855171 (or current)
pgrep -af gc-slack-adapter  # expect a single PID, registered slack/T0B17700WUW

# Pack is loaded
/tmp/gc slack --help        # bind-dm, bind-room, reply-current

# Loop is live
/tmp/gc events --city /home/ds/gas-city --type extmsg.inbound --since 5m
/tmp/gc session list --city /home/ds/gas-city | grep -E "chief-of-staff|project-lead"

# Canary bind-room is still on C0B0TQMQF2B
curl -s "http://127.0.0.1:8372/v0/city/ds-research/extmsg/bindings?session_id=gc-77139" \
  | python3 -m json.tool | head -20
# Expect: one active binding to C0B0TQMQF2B (kind=room)

# DM path (oversight-rig.cos): send a Slack DM to gc-oversight, then:
/tmp/gc session peek gc-83347
# Expect: clean system-reminder, cos routes the reply.

# Direct DM reply via pack:
/tmp/gc slack reply-current --session gc-83347 \
  --conversation-id D0B0TTS550F \
  --body "*test:* hello from the slack pack"
# Expect: delivered: true, message in Slack DM.

# Room publish via gc /extmsg/outbound (canary path):
curl -s -X POST -H "Content-Type: application/json" -H "X-GC-Request: smoke" \
  -d '{"session_id":"gc-77139","conversation":{"scope_id":"ds-research","provider":"slack","account_id":"T0B17700WUW","conversation_id":"C0B0TQMQF2B","kind":"room"},"text":"*smoke:* hello room"}' \
  http://127.0.0.1:8372/v0/city/ds-research/extmsg/outbound \
  | python3 -c 'import json,sys; print("Delivered:", json.load(sys.stdin)["Receipt"]["Delivered"])'
# Expect: Delivered: True

# Rig-channel resolver (no live binding for non-canary rigs yet)
GC_API_BASE_URL=http://127.0.0.1:8372 GC_CITY_NAME=ds-research \
  python3 /home/ds/gascity/examples/oversight-rig/assets/scripts/resolve_rig_channel.py geo
# Expect: JSON with the C0B0TQMQF2B conversation
```

## Key files

- `examples/slack-pack/` — pack scaffold (`bind-dm`, `bind-room`,
  `reply-current`)
- `examples/slack-pack/README.md` — port checklist & architecture
- `examples/slack-pack/scripts/slack_chat_bind_room.py` — needs
  `--binding-owner` work (item 4b)
- `examples/slack-pack/scripts/slack_chat_reply_current.py` — defaults
  to `--via gc` so peer fanout fires
- `examples/oversight-rig/agents/chief-of-staff/prompt.template.md`
- `examples/oversight-rig/assets/scripts/deliver-rollup.sh` —
  per-rig delivery via resolver + legacy env-var fallback
- `examples/oversight-rig/assets/scripts/resolve_rig_channel.py` —
  rig → publishing target resolver (12 unit tests)
- `examples/oversight-rig/orders/escalate-rollups.toml` — the order
  that's been exec-failing (item 4c)
- `internal/api/handler_extmsg.go` + `_test.go` — provider-neutral
  nudge; peer-fanout via `extmsgNotifyMembers`
- `internal/extmsg/binding_service.go` — note: only one active
  binding per conversation; `Bind` returns `ErrBindingConflict`
  if you try to rebind to a different session
- `examples/oversight-rig/adapter/SETUP.md` — port-collision note
  for this host (`:8775`/`:8776`)
