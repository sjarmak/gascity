# Oversight-rig handoff — 2026-05-02

State at handoff: **Slack adapter is built, tested end-to-end, and the
city is in a clean state. The blocker is one specific gas-city issue:
chief-of-staff agent sessions die seconds after starting in this city,
which prevents inbound Slack replies from being acted on.**

Outbound flow is fully working — rollups land in Slack DMs.

## What's verified

- **Outbound** (`bd → deliver-rollup.sh → gc API → adapter → Slack`)
  proven twice end-to-end with real test rollups landing in `D0B0TTS550F`.
- **Inbound** (`Slack → adapter → gc /extmsg/inbound`) — adapter
  HMAC-verifies signed events, normalizes them, and POSTs to gc which
  emits `extmsg.inbound` events with the correct `target_session`. The
  pipeline reaches gc successfully.
- **Adapter security** — split listeners enforce that `/publish` is
  localhost-only (`127.0.0.1:8766`) and only `/slack/events` (HMAC-protected)
  is exposed via Tailscale Funnel on `:8775`.

## What's blocked

**chief-of-staff sessions die during startup.** Each session this
agent created (`gc-82707`, `gc-82759`, `gc-82790`) reached "creating"
state, briefly went "active" or "asleep", then `tmux pane is dead
(status 1)` with `provider_error: session died during startup` in
the supervisor logs. Inbound mail is accepted by gc but has no live
agent to be delivered to.

This pattern is **not specific to chief-of-staff** — multiple
oversight-rig.project-lead sessions hit the same provider_error
during the bulk deploy. Suggests a city-level issue, not a pack issue.

Possibly relevant warnings observed in supervisor logs:
- `gc supervisor: warning: [providers.codex] relying on legacy
  auto-inheritance: name matches built-in "codex"` — a future hard
  error, may be related
- `control-dispatcher: lookup error: ambiguous session identifier
  "control-dispatcher" matches multiple configured named sessions`
- `config-drift mayor: drifted fields: CopyFiles` — pending drift
  reconciliation may be competing for reconciler attention

## Where everything lives

- **Pack**: `/home/ds/gascity/examples/oversight-rig/` (branch
  `feat/oversight-rig-pack`, all committed up to `c855cb40`)
- **Adapter binary**: `examples/oversight-rig/adapter/gc-slack-adapter`
  (gitignored; rebuild with `cd examples/oversight-rig/adapter && go build`)
- **Adapter env**: `~/.config/gc-slack-adapter/env` (chmod 600).
  Keys: `SLACK_BOT_TOKEN`, `SLACK_SIGNING_SECRET`, `SLACK_WORKSPACE_ID`,
  `LISTEN_PUBLIC=:8775`, `GC_API_BASE_URL=http://127.0.0.1:8372`
- **Setup walkthrough**: `examples/oversight-rig/adapter/SETUP.md`
- **Project briefs**: installed at `<rig>/.gc/project-brief.md` for all 13 rigs

## Slack identifiers (active workspace)

- Workspace ("Agent City"): `T0B17700WUW`
- Bot user (`gc-oversight`): `U0B17A4J7TQ`
- Human (you): `U0B1N5KD6HF`
- DM channel (you ↔ bot): `D0B0TTS550F`
- Public URL (Tailscale Funnel): `https://ds-5090.tailbae122.ts.net`
  → forwards to local `:8775` (currently still ON; turn off with
  `sudo tailscale funnel --https=443 off` if you want it dark)

## Live city.toml state

- City-level `[imports.oversight-rig]` is **present** (provides
  chief-of-staff template)
- **NO** `[rigs.imports.oversight-rig]` blocks — the 13 rig-level
  imports were removed during this session to clear the start queue.
  Restore from `city.toml.bak-oversight-deploy-20260502T125137Z` once
  chief-of-staff sessions are stable.
- `[orders.overrides]` disables `patrol-project-leads` (so it doesn't
  fire periodically until you opt in)
- Backups in `/home/ds/gas-city/`:
  - `city.toml.bak-oversight-deploy-20260502T125137Z` — full deploy
    state (city import + 13 rig imports + patrol disable)
  - `city.toml.bak-pre-unstick-...` — same as above
  - `city.toml.bak-oversight-test-20260502T023602Z` — pre-anything
  - `city.toml.bak-doctorfix-...` — pre-this-whole-effort

## Currently NOT running

- Adapter process (was registered with gc, now unregistered cleanly)
- chief-of-staff session (closed, binding ended)
- Tailscale Funnel **still on** (needs sudo to stop)

## Currently running (left in place)

- `gc supervisor` (PID was 2214637) with the production city state +
  city-level oversight-rig import only
- All your existing project workers, mayor, etc. — untouched

## Test artifacts to know about

- `GEO-u8w` (in geo's bd) — closed test rollup, `[OVERSIGHT-TEST]` prefix
- `oversight-test-eda`, `ot-i6x`, `ot-d2c` (from earlier multi-city pack
  test) — also closed, in some other bd context

## Next action (the actual blocker)

**Diagnose why chief-of-staff sessions die during startup.**
Suggested approach:

1. Try a known-good template first. Spin up `gc session new claude-1
   --no-attach` (or any template you trust) and confirm it stays alive
   more than a minute. If even that dies, the issue is provider/account
   level, not pack level — investigate provider config (the codex
   `legacy auto-inheritance` warning, claude-account script, OAuth
   token freshness).
2. If known-good templates work, diff what's different about
   `oversight-rig.chief-of-staff`. Likely candidates: the prompt template
   referencing `{{ .AgentName }}`, the agent.toml's `nudge` text, or
   pack-stamping interaction with the on_demand mode.
3. Once one chief-of-staff session stays alive for >1 min, the inbound
   chain is wired and ready — re-bind that session to `D0B0TTS550F` via
   `POST /v0/city/ds-research/extmsg/bind` and `gc events --type
   extmsg.inbound --since 5m` will show messages reaching it.

After chief-of-staff is stable, restoring the rig-level project-leads
is a separate (smaller) re-deploy.

## What this agent did NOT touch

- Production agents (mayor, dog, control-dispatchers, all rig workers)
- bd state for non-test beads
- Slack workspace beyond installing the gc-oversight app
- Any provider configs
