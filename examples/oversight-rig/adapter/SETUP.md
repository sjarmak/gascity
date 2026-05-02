# gc-slack-adapter setup

End-to-end walkthrough to get rollups flowing into your personal Slack
DM and replies routed back to chief-of-staff. Estimated time: ~20 min
of clicking + a few one-line commands.

## Step 1 — Tailscale Funnel public URL

You need a stable public HTTPS URL Slack can POST to. Tailscale Funnel
gives you one for free.

```bash
# Confirm Funnel is enabled for your tailnet
tailscale funnel status

# Expose port 8765 (the adapter's default listen port) on your tailnet
tailscale funnel --bg --https=443 8765
```

Tailscale prints the public URL — something like
`https://<machine>.<tailnet>.ts.net`. **Copy this URL — you need it for
both Slack app config and the adapter env file.**

You'll see traffic to `https://<...>.ts.net/publish` and
`https://<...>.ts.net/slack/events` flow through Funnel into the adapter
on your local machine.

## Step 2 — Create the Slack app

1. Go to https://api.slack.com/apps → **Create New App** → **From scratch**.
2. Name it `gc-oversight` (or whatever). Pick your personal workspace.
3. In the left sidebar:

   **OAuth & Permissions** →
   - Bot Token Scopes — add:
     - `chat:write` (post messages)
     - `im:history` (read DM history for inbound replies)
     - `im:read` (open DM channel)
     - `users:read` (resolve display names — optional but useful)
   - Click **Install to Workspace** at the top, approve.
   - Copy the **Bot User OAuth Token** (`xoxb-...`) — you need it.

   **Basic Information** →
   - Scroll to **App Credentials**.
   - Copy the **Signing Secret** — you need it.
   - Note the **Team ID** under App Credentials (looks like `T01234567`)
     — that's your `SLACK_WORKSPACE_ID`.

   **Event Subscriptions** →
   - Toggle **Enable Events** → ON.
   - Request URL: paste
     `https://<your-tailnet>.ts.net/slack/events`
     (Slack will verify it; the adapter handles the challenge
     automatically. The adapter must be running for this to succeed —
     start it after Step 4 and come back here.)
   - **Subscribe to bot events** — add:
     - `message.im` (DMs to your bot)
   - Save changes.

   **App Home** →
   - Show Tabs → enable **Messages Tab**.
   - **Allow users to send Slash commands and messages from the messages
     tab** → ON.

## Step 3 — Find your DM channel ID with the bot

After installing the app, open Slack, click on the app's name in the
sidebar to start a DM conversation. Send any message to it (e.g.
"hello"). Then in your terminal:

```bash
curl -sS -H "Authorization: Bearer xoxb-YOUR-TOKEN" \
  "https://slack.com/api/conversations.list?types=im&limit=200" \
  | jq -r '.channels[] | [.id, .user] | @tsv'
```

This lists DM channel IDs and the user IDs they're with. Find the one
where the user is your own user ID (you can find it via:
`curl -sS -H "Authorization: Bearer xoxb-YOUR-TOKEN" https://slack.com/api/auth.test | jq`).

**Copy the DM channel ID** (looks like `D01234567`) — you need it for
the bind step below.

## Step 4 — Configure and start the adapter

Create the env file:

```bash
mkdir -p ~/.config/gc-slack-adapter
cat > ~/.config/gc-slack-adapter/env <<'EOF'
PUBLIC_URL=https://<your-tailnet>.ts.net
SLACK_WORKSPACE_ID=T01234567
SLACK_BOT_TOKEN=xoxb-YOUR-BOT-TOKEN-HERE
SLACK_SIGNING_SECRET=YOUR-SIGNING-SECRET-HERE
EOF
chmod 600 ~/.config/gc-slack-adapter/env
```

Run the adapter:

```bash
cd /home/ds/gascity/examples/oversight-rig/adapter
./run.sh
```

You should see:

```
starting gc-slack-adapter listen=:8765 public=https://...ts.net gc=http://127.0.0.1:9443 city=ds-research
registered with gc as provider=slack account=T01234567 callback=https://...ts.net/publish
listening on :8765
```

If registration fails, check that `gc supervisor` is running and the
city name matches.

Now go back to **Step 2 → Event Subscriptions** and click **Verify** on
the Request URL — Slack will POST a challenge, the adapter will respond,
and Slack should show ✓ Verified.

## Step 5 — Bind chief-of-staff session to your Slack DM

The chief-of-staff session needs to be created and bound to your Slack
DM channel so outbound messages route there.

```bash
# Create the session
gc --city /home/ds/gas-city session new oversight-rig.chief-of-staff \
  --no-attach --alias cos --title "chief-of-staff (slack)"
# Output: Session gc-XXXXX created

# Capture the session ID
COS_ID=$(gc --city /home/ds/gas-city session list --json \
  | jq -r '.[] | select(.alias == "cos") | .id')
echo "Chief-of-staff session: $COS_ID"

# Bind to Slack DM
curl -sS -X POST http://127.0.0.1:9443/v0/city/ds-research/extmsg/bind \
  -H 'Content-Type: application/json' \
  -d "$(jq -n \
    --arg sid "$COS_ID" \
    --arg acct "$SLACK_WORKSPACE_ID" \
    --arg chan "D01234567" \
    '{session_id: $sid, conversation: {provider: "slack", account_id: $acct, id: $chan}}')"
```

Replace `D01234567` with your DM channel ID from Step 3.

## Step 6 — Update deliver-rollup.sh env vars

The deliver script needs the chief-of-staff session ID so it can attribute
outbound messages correctly.

```bash
# Add to your shell rc or to wherever the gc supervisor inherits env from
export GC_API_BASE_URL=http://127.0.0.1:9443
export GC_CITY_NAME=ds-research
export GC_OVERSIGHT_SESSION_ID=$COS_ID  # the session ID from Step 5
export GC_PACK_DIR=/home/ds/gascity/examples/oversight-rig
```

For the order runtime to pick these up, the supervisor must be started
with them in its environment. The simplest path: put them in
`~/.profile` (or `~/.bashrc`) and restart the supervisor.

## Step 7 — Test end-to-end

Create a test rollup bead manually and watch it flow:

```bash
cd /home/ds/projects/GEO  # or any rig you're testing from
gc bd create "Rollup(geo): test message from oversight-rig" \
  -t task \
  --label rollup --label rig:geo --label severity:escalate --label "ref:test" \
  -d "Rig: geo
Project: GEO
State: testing the slack adapter pipeline
Source bead(s): test
Stuck since: now
Why: this is a manual test rollup to verify the slack adapter is wired up correctly.
Smallest ask: reply to this message in Slack with 'ack' to verify inbound works."

# Run delivery manually
bash $GC_PACK_DIR/assets/scripts/deliver-rollup.sh
```

You should see:
- The adapter logs a `publish:` line
- A Slack DM appears in your channel
- The bead gets labeled `delivered`

Reply to the Slack message with "ack". You should see:
- Slack POSTs to `/slack/events`
- The adapter logs a `inbound:` line
- The chief-of-staff session receives the message (visible via
  `gc session peek $COS_ID`)

## Step 8 — Clean up the test

```bash
gc --city /home/ds/gas-city bd close <test-bead-id>
```

## Step 9 — Enable continuous patrol

Once you're confident, edit `/home/ds/gas-city/city.toml` and remove
the `[[orders.overrides]]` block that disabled `patrol-project-leads`.
Reload:

```bash
gc supervisor reload
```

Project-leads will now triage on the 15m cadence and any
severity:escalate rollup will reach Slack within ~1 minute.

## Running the adapter as a service

For always-on, install as a systemd user service:

```bash
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/gc-slack-adapter.service <<'EOF'
[Unit]
Description=gc Slack adapter
After=network-online.target

[Service]
Type=simple
ExecStart=/home/ds/gascity/examples/oversight-rig/adapter/run.sh
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
systemctl --user daemon-reload
systemctl --user enable --now gc-slack-adapter.service
journalctl --user -u gc-slack-adapter -f
```

## Troubleshooting

- **Adapter starts but Slack URL verify fails**: confirm Tailscale Funnel
  is up (`tailscale funnel status`), check `curl https://<your-url>/healthz`
  returns ok, and confirm the Slack app's Request URL exactly matches
  `https://<your-url>/slack/events` (note the path).
- **"register adapter" fails on startup**: gc supervisor needs to be
  running; verify `gc cities` lists `ds-research`.
- **Rollups not delivered**: check `gc supervisor logs` for "publish"
  errors. If you see "channel_not_found" the bot isn't a member of the
  channel — for DMs this shouldn't happen since you DM'd it; for a
  channel, invite the bot.
- **Inbound replies don't reach chief-of-staff**: check signing secret is
  correct; check the `message.im` event subscription is active in the
  Slack app config; confirm session is bound (look up `extmsg/bindings`).
