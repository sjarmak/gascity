# gc slack upload

Upload a local file to the Slack channel a session is bound to. Goes
directly to the local adapter, which handles Slack's three-step
files-upload-v2 protocol (`files.getUploadURLExternal` →
`PUT bytes` → `files.completeUploadExternal`).

Use this instead of describing a file in text — Slack renders the
upload inline (image preview, syntax-highlighted snippet, etc.) and
keeps it discoverable in the channel's Files tab.

## Usage

```
# Plain upload into the bound channel, no thread
gc slack upload --file ./out/plot.png

# With a comment that acts as the message body
gc slack upload --file ./out/plot.png --initial-comment "latest run, n=512"

# Threaded under the most recent inbound (parallels reply-current)
gc slack upload --file ./out/plot.png --thread-current

# Threaded under a specific message ts
gc slack upload --file ./out/plot.png --thread-ts 1730000000.123456

# Override displayed filename / title
gc slack upload --file /tmp/raw.csv --filename results.csv --title "Run 42 metrics"
```

## Flags

- `--file PATH` — local file to upload (required).
- `--session SID` — session id whose binding to upload into. Defaults
  to `$GC_SESSION_ID`.
- `--filename NAME` — override displayed filename (defaults to
  `basename(--file)`).
- `--title TITLE` — display title in Slack (defaults to filename).
- `--initial-comment TEXT` — comment posted alongside the file. Acts
  as the message body for threading purposes.
- `--thread-ts TS` — Slack message ts to thread under. Mutually
  exclusive with `--thread-current`.
- `--thread-current` — thread under the latest inbound for this
  session, same logic as `gc slack reply-current`.
- `--idempotency-key KEY` — caller-supplied idempotency key for
  retries.

## Required Slack scope

The bot token needs **`files:write`**. Without it the adapter returns
`{delivered: false, failure_kind: "auth", error: "missing_scope"}`
and prints the receipt — no exception is raised. Steps to grant:

1. api.slack.com → your app → **OAuth & Permissions**
2. Bot Token Scopes → add `files:write`
3. **Reinstall to Workspace**

No restart needed; the next `gc slack upload` picks up the scope.

## Identity caveat

Files post under the bot's default identity, NOT the per-session
`chat:write.customize` identity. Slack's file-upload API doesn't
honor that override on the file post itself. If identity matters
more than the file preview, post the file with `gc slack upload`
and follow up with `gc slack reply-current` containing the
explanatory text — that reply will carry the per-session identity.

## How it works

1. Resolves the session's active extmsg binding to find the
   target channel id.
2. Reads the file from the local filesystem.
3. POSTs `{session_id, conversation, file_path, filename,
   initial_comment, reply_to_message_id, title}` to the adapter
   `/publish-file` endpoint.
4. The adapter handles the three Slack API calls, posts to the
   channel, and returns a receipt with `{delivered, file_id,
   failure_kind, error}`.

## Examples

```bash
# PL session: upload a generated plot threaded under the human's request
gc slack upload --file out/snr_plot.png \
                --initial-comment "snr vs t for 50 inj/rec runs" \
                --thread-current

# cos session: post a status digest with attached CSV
gc slack upload --file /tmp/digest.csv --title "overnight digest"
```
