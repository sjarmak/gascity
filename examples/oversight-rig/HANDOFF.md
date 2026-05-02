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
8495e4d7 feat(slack-pack): scaffold + bind-dm + reply-current
054b92a6 docs(oversight-rig): handoff after end-to-end validation
3f95d85f fix(oversight-rig): drop redundant min/max_active_sessions on project-lead
e9c07d31 feat(oversight-rig): adapt chief-of-staff to system-reminder delivery
16a82b6d fix(api): make extmsg inbound system-reminder provider-neutral
9ae8003c docs(oversight-rig): handoff state for next agent
... (earlier slack-adapter commits)
```

Local-only (not for commit): `city.toml` has
`[[patches.agent]] name="chief-of-staff" provider="claude-2"` and
`[imports.slack] source = .../examples/slack-pack`. Backups:
`city.toml.bak-pre-cos-patch-*`, `city.toml.bak-pre-restore-*`.

## Open work, in priority order

1. **`gc slack bind-room` + peer fanout** in the slack pack. Largest
   unblock for "monitor mayor↔project-lead conversations" and
   `@@handle`-style spawning. Same pattern as `bind-dm` plus the
   routing-logic state. The discord pack's `discord_chat_bind.py`
   and the bind-room flag set is the reference.
2. **Absorb the Go adapter into the slack pack as a
   `[[service]] proxy_process`.** Right now the adapter is run by
   hand and managed externally; the pack should own its lifecycle
   like discord-interactions does. This also gives you `gc service
   list` integration and tenant-vs-public publication tiers.
3. **Switch the chief-of-staff prompt to compose `slack-v0`**, and
   decide whether cos should call `gc slack reply-current` directly
   for "ack" replies (today the prompt forbids it). One-line
   prompt change once the design call is made.
4. **Per-rig Slack channels** — `bind-room` per rig + rewrite
   `deliver-rollup.sh` to pick `GC_OVERSIGHT_CONVERSATION_ID` from
   the bead's `rig:` label. The user's stated end-state.
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
