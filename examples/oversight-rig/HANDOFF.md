# Oversight-rig handoff — 2026-05-02 (end of session)

## State

Two-way Slack ↔ gc oversight loop is **fully working end-to-end** on
this branch. All four routing branches in chief-of-staff have been
exercised against real Slack inbound:

- match-and-route-to-project-lead (`ack GEO-rjz` → mailed
  `geo/oversight-rig.project-lead`, closed bead with `resolved` label)
- no-match-route-to-mayor (raw text → mailed mayor, no guess)
- quiet-tick (no system-reminder, empty mail)
- prime (first awake read of the prompt)

## Live runtime

- **Supervisor** PID 2656160 (rebuilt `/tmp/gc` includes the
  provider-neutral nudge fix in `internal/api/handler_extmsg.go`).
- **Adapter** running, registered as `slack/T0B17700WUW`. Public
  `:8775`, internal `127.0.0.1:8766`. Log: `/tmp/gc-slack-adapter/run.log`.
  Tailscale Funnel still on (`:443 → :8775`).
- **chief-of-staff** session `gc-83347` (alias `oversight-rig.cos`),
  bound to `D0B0TTS550F` via binding `gc-83357` (gen 6), running on
  `claude-2` (account4 was quota-exhausted; see "Local config" below).
- **13 project-leads** — exactly one per rig, all `awake (always)`.
  Duplicates from the pool/named-session collision are gone.

## What's on the branch

```
3f95d85f fix(oversight-rig): drop redundant min/max_active_sessions on project-lead
e9c07d31 feat(oversight-rig): adapt chief-of-staff to system-reminder delivery
16a82b6d fix(api): make extmsg inbound system-reminder provider-neutral
9ae8003c docs(oversight-rig): handoff state for next agent
... (earlier slack-adapter commits)
```

Local-only (not for commit): `city.toml` has a `[[patches.agent]]`
pinning `chief-of-staff` to `provider = "claude-2"` so cos is
independent of project-worker quota. Backup at
`city.toml.bak-pre-cos-patch-*`.

## Open work, in priority order

1. **Per-rig Slack channels** — today everything fans into one DM.
   See the three-shape design discussion late in this session
   (per-rig outbound only / per-rig binding / mirror internal mail).
   The user wants shape (3) eventually so they can monitor
   mayor↔project-lead conversations and join in.
2. **Adapter as systemd user service** (one-time, unit file already
   in `adapter/SETUP.md`).
3. **`bin/claude-account` atomic-write race** — the original blocker
   that ate account4's `.claude.json`. Fix: `tmp + rename` under a
   flock. File as a Gas City bead.
4. **Re-enable `patrol-project-leads`** in `city.toml` once
   continuous 15m triage is wanted. Currently disabled via
   `[[orders.overrides]]`.

## How to verify if returning fresh

```bash
# Loop is live
/tmp/gc events --type extmsg.inbound --since 5m
/tmp/gc session list | grep -E "chief-of-staff|project-lead"

# Send a Slack DM to gc-oversight, then:
/tmp/gc session peek gc-83347
# Expect: clean system-reminder, no Discord text, cos routes the reply.
```

## Key files

- `examples/oversight-rig/agents/chief-of-staff/prompt.template.md` —
  the rewritten prompt
- `internal/api/handler_extmsg.go` + `_test.go` — provider-neutral
  nudge
- `examples/oversight-rig/adapter/SETUP.md` — port-collision note for
  this host (`:8775`/`:8776` instead of `:8765`/`:8766`)
- `examples/oversight-rig/assets/scripts/deliver-rollup.sh` — outbound
  delivery; currently sends every rig to one DM (env-driven)
