# Slack audit delivery diagnosis

Bead: `dr-vgtg`

## Findings

The Slack adapter itself is currently healthy. The earlier maintenance-PL
failure coincided with a city/supervisor interruption, not an unknown adapter:

- `.gc/services/slack/logs/service.log` records the city API disappearing at
  10:12 EDT, an adapter registration failure with `city-not-found` at 10:13,
  and successful registration at 10:27.
- The actual process is PID 98881,
  `/home/ds/gascity-packs/slack-pack/adapter/gc-slack-adapter`.
- It listens internally on
  `/tmp/gcsvc-1000/ee31dfef/slack-3811561616.sock` and is exposed by gc's
  city service proxy at `/v0/city/ds-research/svc/slack/*`.
- Registration is visible at
  `GET /v0/city/ds-research/extmsg/adapters` as
  `slack/T0B17700WUW`, name `slack-adapter`.
- The producer publish endpoint is
  `POST /v0/city/ds-research/svc/slack/publish`. A side-effect-free
  `GET /svc/slack/publish` reached the adapter and returned its expected 405;
  `GET /svc/slack/healthz` returned 200, `ok`, and
  `dispatch_dropped_total=0`.
- Successful audit publications at 10:37, 10:38, and 10:43 in the adapter log
  further corroborate restored outbound service after registration.

`gc slack status` is a separate diagnostic defect. Its first adapter-registry
request returns immediately, then
`/v0/city/ds-research/events?type=extmsg.inbound&limit=50` never returns.
Direct curl and strace reproduce that exact boundary. The status implementation
in `/home/ds/gascity-packs/slack-pack/scripts/slack_chat_status.py` serially
fetches inbound and outbound event history after adapter state, so a wedged
optional event-history query prevents it from displaying healthy adapter state.
Changing that implementation is pack source outside this city-local bead's
authorized floor.

## Binding evidence

- `gascity-maintenance-pl` session `gc-517911` has an active binding
  `gc-517961` to channel `C0B25SS12CD`. Its condensed durable audit can be
  replayed once an external Slack post is explicitly authorized.
- `embertide-pl` session `gc-507741` has zero bindings. No embertide channel or
  intended channel mapping exists in `.gc/services/slack/data/config.json`,
  `.gc/slack/rig_mappings.json`, or the city config. Binding it by guess would
  risk publishing to the wrong external audience; the intended channel ID is
  required.

## Hold

No service was restarted or modified. No binding was guessed or created. No
Slack publication or other external action occurred. Remaining work is blocked
on (1) authorization to change slack-pack status handling, (2) the intended
embertide channel ID, and (3) explicit authorization for the two replay posts.
