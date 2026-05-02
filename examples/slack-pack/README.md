# Slack pack (scaffold)

A Slack provider extension for Gas City. Modeled directly on the
upstream `discord` pack
(https://github.com/gastownhall/gascity-packs/tree/main/discord) so
the same primitives can be ported one at a time.

This pack lives in-tree at `examples/slack-pack/` for the moment. It
is intended to be promoted to the `gastownhall/gascity-packs` repo
(or a sibling) once it has feature parity worth upstreaming.

## Status

- [x] `gc slack bind-dm` — bind a Slack DM channel to one named session
- [x] `gc slack reply-current` — reply to the latest Slack event in the
      current session, by default through gc's `/extmsg/outbound` so
      transcript recording + peer fanout fire (`--via adapter` keeps the
      old direct-to-adapter path for diagnostics)
- [x] `template-fragments/slack-v0.template.md` — composable prompt
      fragment for any agent in a slack-bound session
- [x] `gc slack bind-room` — bind a room to multiple sessions; flags
      `--enable-peer-fanout`, `--allow-untargeted-publication`,
      `--max-peer-triggered-publishes`, `--max-total-peer-deliveries`,
      `--default-handle`, `--handle HANDLE=SESSION` (creates a
      launcher-mode group + participants under the hood)
- [ ] `gc slack enable-room-launch` (`@@handle` thread-scoped sessions)
- [ ] `gc slack publish` (explicit publish to a saved binding)
- [ ] `gc slack import-app` / `map-channel` / `map-rig` / `sync-commands`
      (slash-command intake — `/gc fix` style)
- [ ] `gc slack post-message` (workflow status projection)
- [ ] `gc slack retry-peer-fanout`
- [ ] `gc slack status`
- [ ] Pack-owned intake service (`[[service]]` proxy_process) that
      replaces the externally-run `examples/oversight-rig/adapter/`
      Go binary

## Architecture (current)

The Go adapter at `examples/oversight-rig/adapter/gc-slack-adapter`
is the public-facing webhook receiver and outbound publisher. It runs
outside this pack — for now, it is left in place exactly as-is. This
pack adds CLI surface around it: `bind-dm` writes to gc's
`/extmsg/bind` and to the pack's local config; `reply-current` reads
recent gc events to find the conversation, then POSTs to gc's
`/extmsg/outbound` (which calls the registered HTTP adapter's
`/publish` endpoint internally and emits `ExtMsgOutbound` so peer
fanout fires for bind-room sessions). `--via adapter` is available
for adapter-only diagnostics that bypass gc.

```
                   ┌──── public ────┐
Slack  ──HMAC──▶  Go adapter :8775  ──▶ gc /extmsg/inbound
                  Go adapter :8766  ◀── gc /extmsg/outbound  ◀── gc slack reply-current
                                    ◀── (--via adapter) ─────── gc slack reply-current
                   └────────────────┘
```

## Install

```toml
# city.toml
[imports.slack]
source = "/path/to/examples/slack-pack"
```

Then `gc reload` (or wait for the supervisor to pick up the change).
Verify the commands appear:

```
gc slack --help
gc slack bind-dm --help
gc slack reply-current --help
```

## Verify

Assuming the adapter is up and `SLACK_WORKSPACE_ID` /
`SLACK_BOT_TOKEN` are exported (the same env the adapter consumes,
sourced from `~/.config/gc-slack-adapter/env`):

```
# Bind a DM channel to a session
gc slack bind-dm D0B0TTS550F oversight-rig.cos

# From inside that session (or any session that has seen recent
# extmsg.inbound on a slack conversation):
echo "*oversight-rig.cos:* ack via slack pack" > /tmp/reply.txt
gc slack reply-current --body-file /tmp/reply.txt
```

The reply should land in your Slack DM.

To bind a room (public or private channel) to multiple sessions so
that mayor and project-lead are visible peers and a human can join the
conversation:

```
gc slack bind-room C0123ROOM01 \
    oversight-rig.mayor geo/oversight-rig.project-lead \
    --enable-peer-fanout
```

Both sessions then receive an inbound system reminder for every human
message in the channel; `extmsg.inbound` events list both as
conversation members. When the publishing session calls
`gc slack reply-current` (default `--via gc`), gc records the publish
in the conversation transcript and fans out a peer-publication
reminder to the other bound sessions so they see what their peer just
said.

## Where the work that's still missing comes from

The discord pack ships ~350K LOC of Python service code; the bulk of
that is provider-agnostic state-machine logic for room peer fanout,
launcher mode, slash-command intake, and workflow status projection.
A meaningful chunk should eventually become a shared `extmsg-pack-lib`
or similar so slack/discord/teams/etc. don't all reimplement it.

For now we're porting feature-by-feature, against real usage from the
oversight-rig pack.
