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
- [x] `gc slack publish` (publish to a session's saved binding;
      target session is required, no event-scan fallback —
      fail-fast when the session has no active binding)
- [ ] `gc slack import-app` / `map-channel` / `map-rig` / `sync-commands`
      (slash-command intake — `/gc fix` style)
- [ ] `gc slack post-message` (workflow status projection)
- [ ] `gc slack retry-peer-fanout`
- [x] `gc slack status` — read-only diagnostics (adapters, bindings,
      recent traffic). `--session SID` for one-session detail,
      `--since 5m` for a time window, `--json` for scripting.
- [x] Pack-owned intake service (`[[service]]` proxy_process). Phase A:
      adapter is the same Go binary, but gc supervises it via UDS for
      `/publish` while the public Slack webhook still terminates at
      adapter TCP `:8775` (Funnel unchanged). See "Adapter as a
      proxy_process service" below for the cutover.

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
    --enable-peer-fanout \
    --binding-owner geo/oversight-rig.project-lead
```

Both sessions then receive an inbound system reminder for every human
message in the channel; `extmsg.inbound` events list both as
conversation members. When the publishing session calls
`gc slack reply-current` (default `--via gc`), gc records the publish
in the conversation transcript and fans out a peer-publication
reminder to the other bound sessions so they see what their peer just
said.

`--binding-owner SESSION` is what makes outbound publishes (and
therefore `gc slack reply-current --via gc`) actually work. Without
it, peer fanout still fires on inbound, but `/extmsg/outbound` has
no SessionBindingRecord to resolve the conversation through and the
publish is rejected. The owner must be one of the participants —
prefer the session that "owns" the room from gc's perspective (the
project-lead, not the chief-of-staff). Pass the gc session id (e.g.
`gc-77139`) when alias resolution semantics matter; for stable named
sessions, the alias works too.

## Adapter as a proxy_process service

Phase A of the in-pack adapter (tracked as bd `gc-5rz`) lets gc
supervise the adapter as part of the city's services. The adapter
binds a Unix domain socket for the `/publish` endpoint that gc reaches
via the extmsg HTTP adapter, and gc reverse-proxies `/svc/slack/*` to
that UDS. The public Slack webhook (`/slack/events`) still terminates
at the adapter's TCP `:8775` so Tailscale Funnel and Slack's signing
secret verification are unchanged. The same binary runs in both modes
— the legacy `nohup ./run.sh` deployment is preserved as a rollback
target.

### What gc injects vs. what stays in the env file

`proxy_process` injects the controller-managed env at start time:

- `GC_SERVICE_NAME=slack`
- `GC_SERVICE_SOCKET=/tmp/gcsvc-<uid>/<hash>/slack-*.sock`
- `GC_SERVICE_URL_PREFIX=/svc/slack`
- `GC_SERVICE_STATE_ROOT=.../.gc/services/slack`
- `GC_SERVICE_RUN_ROOT=.../.gc/services/slack/run`
- plus `GC_API_BASE_URL` and `GC_CITY_NAME` (already set by the
  controller for any exec it spawns under the city scope)

When `GC_SERVICE_SOCKET` is set, the adapter:
- skips its `LISTEN_INTERNAL` TCP listener and binds the UDS instead;
- computes its self-registration `CallbackURL` as
  `$GC_API_BASE_URL + $GC_SERVICE_URL_PREFIX` (gc's extmsg HTTP adapter
  appends `/publish` itself when calling out, so the registered base
  URL must NOT include `/publish`);
- still binds public TCP for `/slack/events`;
- still serves `/healthz` on the UDS so the controller's `health_path`
  probe succeeds.

Slack secrets stay in `~/.config/gc-slack-adapter/env`:

```
SLACK_WORKSPACE_ID=T01234567
SLACK_BOT_TOKEN=xoxb-...
SLACK_SIGNING_SECRET=...
```

Source the file before `gc start` (or the supervisor's start) so the
adapter inherits the secrets via `os.Environ()`. Phase B will move
the env-file path into pack config; not yet wired.

### Cutover sequence

```
# 1. Build the adapter binary into the pack tree
make -C examples/oversight-rig/adapter build  # or: go build -o gc-slack-adapter
mkdir -p examples/slack-pack/adapter
cp examples/oversight-rig/adapter/gc-slack-adapter \
   examples/slack-pack/adapter/gc-slack-adapter

# 2. Source the secrets so the supervisor inherits them
set -a; source ~/.config/gc-slack-adapter/env; set +a

# 3. Stop the manually-managed adapter
pkill -f gc-slack-adapter

# 4. Reload the city so the [[service]] block from slack-pack registers
gc reload   # or: gc supervisor reload

# 5. Verify the service is ready
gc service list                            # expect: slack proxy_process ready
curl --unix-socket "$(gc service show slack --json | jq -r .socket)" \
     http://x/healthz                      # expect: 200 ok

# 6. Verify outbound publish through gc
curl -s -X POST -H "Content-Type: application/json" -H "X-GC-Request: cutover" \
  -d '{"session_id":"<bound-session>","conversation":{"scope_id":"<city>","provider":"slack","account_id":"T...","conversation_id":"D...","kind":"dm"},"text":"*cutover:* hello"}' \
  http://127.0.0.1:8372/v0/city/<city>/extmsg/outbound \
  | jq '.Receipt.Delivered'                # expect: true

# 7. Verify inbound — send a Slack DM to the bot, then:
gc events --city <city> --type extmsg.inbound --since 2m
```

Rollback: remove (or comment out) the `[[service]]` block in
`pack.toml`, `gc reload`, then restart the manual adapter via the
legacy script:

```
( cd examples/oversight-rig/adapter \
    && nohup ./run.sh > /tmp/gc-slack-adapter/run.log 2>&1 & disown )
```

The adapter ignores `$GC_SERVICE_SOCKET` when unset and falls back to
TCP-only mode, so the same binary serves both deployments.

### Known foot-guns

- **Two adapters running.** If you forget step 3 and the manual
  adapter stays up while gc starts the proxy_process one, both will
  call `/extmsg/adapters` to register. The second registration
  overwrites the first; outbound publishes go through whichever one
  registered last (last-write-wins). Symptom: outbound succeeds but
  through the wrong process. Stop the manual one and reload.
- **Slack signing key missing.** The adapter Fatals at start with
  `missing required env vars: SLACK_SIGNING_SECRET`. Under
  proxy_process this shows up as the service stuck in `degraded`
  state with the env-var name in the reason field. Source the env
  file in the supervisor's launching shell.
- **Funnel rule out-of-band.** Tailscale Funnel `:443 → :8775` is not
  declared in the city. If you reboot the host or `tailscale funnel
reset`, traffic stops landing at the adapter. Re-add the rule
  manually until Phase C lands.

## Where the work that's still missing comes from

The discord pack ships ~350K LOC of Python service code; the bulk of
that is provider-agnostic state-machine logic for room peer fanout,
launcher mode, slash-command intake, and workflow status projection.
A meaningful chunk should eventually become a shared `extmsg-pack-lib`
or similar so slack/discord/teams/etc. don't all reimplement it.

For now we're porting feature-by-feature, against real usage from the
oversight-rig pack.
