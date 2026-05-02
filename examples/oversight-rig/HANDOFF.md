# Oversight-rig handoff — 2026-05-02 (end of session)

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
`gastownhall/gascity-packs/discord` with `gc slack bind-dm` and
`gc slack reply-current`. The reply path was verified end-to-end
(`delivered: true`, message landed as `gc-oversight` in Slack).

## Live runtime

- **Supervisor** PID 2656160 (rebuilt `/tmp/gc` includes the
  provider-neutral nudge fix in `internal/api/handler_extmsg.go`).
- **Slack adapter** running, registered as `slack/T0B17700WUW`.
  Public `:8775`, internal `127.0.0.1:8766`. Log:
  `/tmp/gc-slack-adapter/run.log`. Tailscale Funnel still on
  (`:443 → :8775`).
- **Slack pack** imported via local `[imports.slack]` in `city.toml`.
  `gc slack {bind-dm,reply-current}` available.
- **chief-of-staff** session `gc-83347` (alias `oversight-rig.cos`),
  bound to `D0B0TTS550F` via binding `gc-83357` (gen 6), running on
  `claude-2`. cos still uses its handwritten "About Reply
  Instructions" section — it has not yet been switched to compose
  the `slack-v0` template fragment from the new pack (deliberate;
  cos's design today is "do not reply directly", and switching it
  to use `gc slack reply-current` is a scope decision).
- **13 project-leads** — exactly one per rig, all `awake (always)`.
  Pool/named-session duplicate collision is fixed.

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

## NEW: bind-room is built but the live supervisor + adapter are not yet
restarted to pick it up. Status snapshot:

- `gc slack bind-room` is registered in the rebuilt `/tmp/gc`. The
  live supervisor (PID 2656160) is still the previous binary; it
  accepts the existing extmsg endpoints but cannot accept the new
  `fanout_policy` field on `POST /extmsg/groups` (Huma rejects unknown
  body keys). Calls without any fanout flag DO work against the live
  supervisor today.
- The Slack Go adapter at `examples/oversight-rig/adapter/` was
  patched to classify Slack `channel_type` → `room`/`dm` (was
  hardcoded `dm`). The on-disk binary is rebuilt but the running
  adapter is still the previous binary, so room-kind inbound from
  Slack still arrives as kind=`dm`.
- API: `internal/extmsg/types.go` `FanoutPolicy` now has snake_case
  JSON tags; `ExtMsgGroupEnsureInput.Body` accepts `fanout_policy`.
  `openapi.json` + `docs/schema/openapi.{json,txt}` regenerated;
  `make dashboard-check` passes; `TestHandleExtMsgGroupEnsureRoundTripsFanoutPolicy`
  guards the round-trip.

To take bind-room live (still YOUR call — the running loop has 13
project-leads + chief-of-staff with state in memory):

```
# 1) Stop and replace the supervisor (sessions reattach on restart).
/tmp/gc stop && /tmp/gc start

# 2) Restart the slack adapter binary so room-kind classification fires.
pkill -f gc-slack-adapter
~/gascity/examples/oversight-rig/adapter/run.sh &

# 3) Verify.
/tmp/gc slack bind-room C0123ROOM01 \
    oversight-rig.mayor geo/oversight-rig.project-lead \
    --enable-peer-fanout
```

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
3. **Switch the chief-of-staff prompt to compose `slack-v0`**, and
   decide whether cos should call `gc slack reply-current` directly
   for "ack" replies (today the prompt forbids it). One-line
   prompt change once the design call is made.
4. **Per-rig Slack channels** — `bind-room` per rig (still pending
   live activation; supervisor + adapter restart needed) +
   ~~rewrite `deliver-rollup.sh` to pick `GC_OVERSIGHT_CONVERSATION_ID`
   from the bead's `rig:` label~~ DONE this session. Rollups now
   resolve target conversation per bead: `resolve_rig_channel.py`
   finds the bead's rig's project-lead session, reads its most
   recent active extmsg binding, and publishes through gc's
   `/extmsg/outbound` so peer fanout fires. The legacy
   `GC_OVERSIGHT_*` env vars are now fallback-only (used when a rig
   has no project-lead session or no active binding). 12 unit tests
   in `examples/oversight-rig/assets/scripts/test_resolve_rig_channel.py`.
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
8. **Update older `examples/oversight/chief-of-staff` prompt** —
   has the same "extmsg inbound becomes mail" mistake we fixed
   here. Five-line rewrite.

## How to verify if returning fresh

```bash
# Pack is loaded
/tmp/gc slack --help

# Loop is live
/tmp/gc events --type extmsg.inbound --since 5m
/tmp/gc session list | grep -E "chief-of-staff|project-lead"

# Send a Slack DM to gc-oversight, then:
/tmp/gc session peek gc-83347
# Expect: clean system-reminder, cos routes the reply.

# Direct reply path:
/tmp/gc slack reply-current --session gc-83347 \
  --conversation-id D0B0TTS550F \
  --body "*test:* hello from the slack pack"
# Expect: delivered: true, message in Slack DM.
```

## Key files

- `examples/slack-pack/` — new pack (commit `8495e4d7`)
- `examples/slack-pack/README.md` — port checklist & architecture
- `examples/oversight-rig/agents/chief-of-staff/prompt.template.md`
- `internal/api/handler_extmsg.go` + `_test.go` — provider-neutral
  nudge
- `examples/oversight-rig/adapter/SETUP.md` — port-collision note
  for this host (`:8775`/`:8776`)
- `examples/oversight-rig/assets/scripts/deliver-rollup.sh` —
  outbound delivery; today sends every rig to one DM (env-driven)
