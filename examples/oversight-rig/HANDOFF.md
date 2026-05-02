# Oversight-rig handoff — 2026-05-02

State at handoff: **Two-way oversight loop is fully working
end-to-end on this branch. Outbound rollups → Slack DM, inbound
human replies → chief-of-staff routing works under the new prompt
template, and the gas-city core nudge text is now provider-neutral
(the old "reply in Discord" hardcoding has been removed and the
test for it lives in `internal/api/handler_extmsg_test.go`).**

The original blocker (chief-of-staff sessions dying during startup)
was a corrupt `~/.claude-homes/account4/.claude.json` and is fixed.

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

## What was blocked (now resolved)

**Root cause:** `~/.claude-homes/account4/.claude.json` was corrupt —
one trailing `}` past the end of the valid object (likely a race
between two concurrent `claude-account` writers). The `claude-account`
launcher does `json.load()` early in startup; the JSON decode threw
`Extra data: line 554 column 2 (char 22943)`, the launcher exited
status 1, and tmux reported `Pane is dead (status 1)` which the
supervisor surfaced as `provider_error: session died during startup`.

It hit chief-of-staff (and the bulk project-lead deploy) because both
templates omit `provider`, so they fall back to the workspace default
`provider = "claude-4"` — i.e. the one corrupt account.

**Fix applied:** truncated the file to the first valid JSON object;
backup at `~/.claude-homes/account4/.claude.json.bak-corrupt-*`.
Verified: `gc session new chief-of-staff --no-attach` reaches `active`
and runs the prompt cleanly (`Inbox is empty — quiet run`).

**Follow-up worth filing as a Gas City bead:** `bin/claude-account`
should write `.claude.json` atomically (`tmp + rename` under a flock)
to prevent this race recurring. It currently does
`open(path,'w'); json.dump(...)` directly, which is not safe under
concurrent invocations.

The other supervisor warnings observed during the original failure
are unrelated to this incident but still worth triaging separately:
- `[providers.codex] relying on legacy auto-inheritance` — pre-existing,
  unrelated; will become a hard error in a future gc release
- `control-dispatcher: ambiguous session identifier` — orthogonal
- `config-drift mayor: drifted fields: CopyFiles` — orthogonal

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
- All 13 `[rigs.imports.oversight-rig]` blocks **restored** from
  `city.toml.bak-pre-unstick-20260502T141433Z` (note: that's the
  correct backup; the previous handoff incorrectly named
  `city.toml.bak-oversight-deploy-20260502T125137Z`, which is from
  *before* the rig imports were ever added).
- `[orders.overrides]` still disables `patrol-project-leads` so the
  project-leads only triage when you sling them manually.
- Backups in `/home/ds/gas-city/`:
  - `city.toml.bak-pre-restore-20260502T144614Z` — current-state
    snapshot taken just before this restore (rollback target if needed)
  - `city.toml.bak-pre-unstick-20260502T141433Z` — pre-unstick state
    (was restored into place)
  - `city.toml.bak-oversight-deploy-20260502T125137Z` — pre-anything
    state from earliest deploy attempt (city.toml minus the entire
    oversight stack)
  - `city.toml.bak-oversight-test-...`, `city.toml.bak-doctorfix-...`
    — older snapshots

## Currently running

- **Supervisor**: PID 2656160, restarted from a freshly built `/tmp/gc`
  that includes the provider-neutral nudge fix
  (`internal/api/handler_extmsg.go`). Verified: live system-reminders
  no longer contain "Discord", `gc discord reply-current`, or
  `gc transcript read --ack`.
- **Adapter**: `gc-slack-adapter` (restarted post-supervisor-bounce so
  it could re-register). Public listener `:8775`, internal `:8766`.
  Logs: `/tmp/gc-slack-adapter/run.log`. Started by hand (`run.sh`);
  not under systemd. To make persistent, follow `adapter/SETUP.md` §
  "Running the adapter as a service".
- **Tailscale Funnel** still on, forwarding `:443 → :8775`.
- **Chief-of-staff session** `gc-83161` (alias `oversight-rig.cos`,
  title `chief-of-staff (slack)`). Bound to `D0B0TTS550F` via binding
  record `gc-83180` (`BindingGeneration=5`). Picked up the new prompt
  template at creation. Verified end-to-end: a synthetic inbound POST
  to `/v0/city/ds-research/extmsg/inbound` produced the new clean
  system-reminder, and cos correctly routed the unmatched reply to
  mayor (`gc-83183`) per the four-step algorithm's "do not guess"
  rule.
- **13 rig-level project-leads**: each rig has *two* active
  project-lead sessions — an old `gc-82xxx` one (alias without `-1`
  suffix, reason flags `session,config`) and a new `gc-83xxx` one
  (alias with `-1` suffix, reason flag `session` only). Survived the
  supervisor restart; not auto-reconciled. Per-rig duplicates violate
  `max_active_sessions = 1`. Safe move: close the unsuffixed
  `gc-82xxx` ones first (since they predate the recent topology) and
  let the supervisor confirm it does not respawn them. Watch for any
  in-flight rollup beads attributed to the closing session before
  closing.

## Currently running (left in place)

- `gc supervisor` (PID was 2214637) with the production city state +
  city-level oversight-rig import only
- All your existing project workers, mayor, etc. — untouched

## Test artifacts to know about

- `GEO-u8w` (in geo's bd) — closed test rollup, `[OVERSIGHT-TEST]` prefix
- `oversight-test-eda`, `ot-i6x`, `ot-d2c` (from earlier multi-city pack
  test) — also closed, in some other bd context

## Next action

1. **End-to-end smoke test with a real Slack DM** (synthetic inbound
   already verified). Send any DM to `gc-oversight` and watch:

   ```bash
   /tmp/gc events --type extmsg.inbound --since 5m
   /tmp/gc session peek gc-83161
   ```

   Expected: `extmsg.inbound` event with
   `target_session=gc-83161`, the new clean system-reminder appears
   in cos's prompt, cos either matches an open escalation and routes
   to that rig's project-lead, or (if no escalation matches) routes
   to mayor.

2. **Reconcile the project-lead duplicates** (see Currently Running).
   Suggested: close the `gc-82xxx` unsuffixed sessions, watch for the
   reconciler reaction.

3. **Optional follow-ups:**
   - File a Gas City bead for the `bin/claude-account` atomic-write
     race (original blocker — corrupt account4 JSON from concurrent
     writers).
   - Make the adapter a systemd user service so it survives reboot
     (`adapter/SETUP.md` § "Running the adapter as a service").
   - Re-enable `patrol-project-leads` in `city.toml` once you want
     periodic triage instead of manual sling.

## Source-level changes on this branch (not yet committed)

```
examples/oversight-rig/HANDOFF.md
examples/oversight-rig/adapter/SETUP.md
examples/oversight-rig/agents/chief-of-staff/prompt.template.md
internal/api/handler_extmsg.go
internal/api/handler_extmsg_test.go
```

- Pack docs and prompt updated for the new inbound delivery model.
- Gas City core: `extmsgNotifyMembers` no longer embeds Discord-
  specific reply instructions in the inbound system-reminder; new
  test `TestExtmsgNotifyMembersNudgeTextIsProviderNeutral` enforces
  that. Build clean (`go build ./...`), vet clean (`go vet ./...`),
  `internal/api` test package green.

## What this agent did NOT touch

- Production agents (mayor, dog, control-dispatchers, all rig workers)
- bd state for non-test beads
- Slack workspace beyond installing the gc-oversight app
- Any provider configs
