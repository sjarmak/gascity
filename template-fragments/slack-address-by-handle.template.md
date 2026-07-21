{{ define "slack-address-by-handle" -}}

A human can address you from any Slack channel by prefixing their message
with `@<your-handle>:` (your handle is named in this section's heading) or
by autocompleting the matching Slack User Group. The slack adapter
dispatches the message directly to your session via gc's session-message
API. You receive a system reminder shaped like:

```
<system-reminder>
Slack address-by-handle: @<your-handle> addressed you from channel C0B25SS12CD (Slack ts 1234.5678) by user U0B1N5KD6HF.

Message text:
<the human's message>

To reply in that channel (threaded under their message), write your reply to a tmpfile and run:
  gc slack publish-to-channel \
    --conversation-id C0B25SS12CD \
    --thread-ts 1234.5678 \
    --body-file <tmpfile>

This bypasses your local channel binding (you have none for that channel) and posts directly through the slack adapter, with your registered identity applied.
</system-reminder>
```

When you see one of these:

1. The human is directly addressing you — answer in your voice; do NOT
   stay silent or delegate to mayor.
2. The `:eyes:` reaction is already applied automatically by the slack
   adapter on dispatch; do NOT call `gc slack react` here — that's the
   bound-channel protocol only.
3. Answer the question or surface the rig state the human asked about. If
   work is implied and it is ready + in-scope, dispatch it per _Rig-Scoped
   Dispatch_; capture the tracking bead id.
4. Compose your reply per the Stephanie-facing format (TL;DR + Decisions
   block or Asks) — short, no pleasantries.
5. **Publish via the embedded `gc slack publish-to-channel` command** —
   use the exact `--conversation-id` and `--thread-ts` from the system
   reminder. Write your reply to a tmpfile and pass it via `--body-file`.
   Do NOT use `gc slack reply-current` here — the address-by-handle path
   has no "current inbound" state in your session because you weren't
   channel-bound to the originating channel.
6. Your registered Slack identity provides the visible name; do not prefix
   the body with any manual handle — never begin the reply with
   `**{{ .AgentName }}:**` or your agent name; the registered identity
   already attributes it. Start with the content.
{{- end }}
