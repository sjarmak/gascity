package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// slackChatAPIDefaultBase is the production Slack web API origin. Tests
// override via the --api-base flag (or SLACK_API_BASE env) to point at
// httptest servers without monkey-patching globals.
const slackChatAPIDefaultBase = "https://slack.com/api"

// slackBotTokenEnv is the env var the post-message verb reads when
// --token is not provided. Mirrors the adapter's own SLACK_BOT_TOKEN
// contract so operators only configure the token once.
const slackBotTokenEnv = "SLACK_BOT_TOKEN"

// slackAPIBaseEnv lets ops point the verb at a non-production Slack
// host (e.g. an air-gapped relay) without touching the CLI flag. The
// flag still wins when both are set.
const slackAPIBaseEnv = "SLACK_API_BASE"

// slackChatPostBody is the request body for chat.postMessage. We send
// `text` as a Block-Kit fallback (notifications, accessibility) and
// `blocks` as the rich rendering. ThreadTS is omitted by this verb —
// status projections post to the channel root, not into threads.
type slackChatPostBody struct {
	Channel string       `json:"channel"`
	Text    string       `json:"text"`
	Blocks  []slackBlock `json:"blocks,omitempty"`
	TS      string       `json:"ts,omitempty"`
}

// slackChatPostResponse is the subset of chat.postMessage / chat.update
// response we consume. OK=false drives an error return; TS is echoed
// to stdout so callers can plumb it back as --update <ts>.
type slackChatPostResponse struct {
	OK      bool   `json:"ok"`
	TS      string `json:"ts,omitempty"`
	Channel string `json:"channel,omitempty"`
	Error   string `json:"error,omitempty"`
}

// slackHTTPError is returned when Slack responds with a non-2xx status.
// Wrapped errors so callers can `errors.As` for transport-level
// failures vs API-level (ok=false) failures.
type slackHTTPError struct {
	Status int
	Body   string
}

func (e *slackHTTPError) Error() string {
	return fmt.Sprintf("slack http %d: %s", e.Status, e.Body)
}

// slackPostMessageOpts is the runner's input. The CLI binds flags to
// fields and calls runSlackPostMessage; tests construct opts directly.
type slackPostMessageOpts struct {
	Channel    string
	Kind       string
	PayloadRaw string
	UpdateTS   string
	BotToken   string
	APIBase    string
	Stdout     io.Writer
}

// newSlackPostMessageCmd builds the cobra command for `gc slack
// post-message`. The verb pushes a structured workflow-status payload
// (milestone | progress | rollup) to a Slack channel, rendered via
// Block Kit. Use --update <ts> for in-place updates of a prior post —
// useful for progress bars that refresh in place.
func newSlackPostMessageCmd(stdout io.Writer) *cobra.Command {
	opts := slackPostMessageOpts{Stdout: stdout}
	cmd := &cobra.Command{
		Use:   "post-message",
		Short: "Post a workflow-status payload to a Slack channel as Block Kit",
		Long: `Post a structured workflow-status payload (milestone | progress | rollup)
to a Slack channel, rendered via Block Kit.

This is the agent-driven status-projection surface. Unlike 'gc slack
publish' (human-driven, binding-resolved) and 'gc slack reply-current'
(human-driven, inbound-anchored), post-message bypasses extmsg
bindings and posts directly to the configured channel using
SLACK_BOT_TOKEN. Use it for cron-driven rollups, milestone
notifications, and progress-bar projections from orchestration code.

Payload kinds:

  milestone  Headline + summary + optional label/value fields.
  progress   Headline + unicode progress bar from {current,total}.
  rollup     Headline + bulleted list from {items:[{label,value}]}.

Pass --update <ts> with a previously-returned message ts to edit the
post in place (Slack chat.update). Without --update we call
chat.postMessage and print the new ts to stdout so callers can
capture it for the next refresh.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSlackPostMessage(opts)
		},
	}
	cmd.Flags().StringVar(&opts.Channel, "channel", "",
		"Slack channel id (e.g. C01234567) — required")
	cmd.Flags().StringVar(&opts.Kind, "kind", "",
		"Payload kind: milestone | progress | rollup — required")
	cmd.Flags().StringVar(&opts.PayloadRaw, "payload", "",
		"JSON payload object — required")
	cmd.Flags().StringVar(&opts.UpdateTS, "update", "",
		"If set, edit the message at this ts via chat.update instead of chat.postMessage")
	cmd.Flags().StringVar(&opts.BotToken, "token", "",
		"Slack bot token (xoxb-...); defaults to $"+slackBotTokenEnv)
	cmd.Flags().StringVar(&opts.APIBase, "api-base", "",
		"Slack web API base URL (defaults to $"+slackAPIBaseEnv+" or "+slackChatAPIDefaultBase+")")
	return cmd
}

// runSlackPostMessage validates opts, renders the Block Kit payload,
// and calls Slack's chat.postMessage (or chat.update when UpdateTS is
// set). Returns a slackHTTPError on transport failures and a regular
// error on API-level (ok=false) failures.
func runSlackPostMessage(opts slackPostMessageOpts) error {
	if strings.TrimSpace(opts.Channel) == "" {
		return fmt.Errorf("--channel is required")
	}
	if strings.TrimSpace(opts.Kind) == "" {
		return fmt.Errorf("--kind is required (milestone | progress | rollup)")
	}
	if strings.TrimSpace(opts.PayloadRaw) == "" {
		return fmt.Errorf("--payload is required")
	}
	token := opts.BotToken
	if token == "" {
		token = strings.TrimSpace(os.Getenv(slackBotTokenEnv))
	}
	if token == "" {
		return fmt.Errorf("slack bot token not provided (--token or $%s)", slackBotTokenEnv)
	}
	apiBase := opts.APIBase
	if apiBase == "" {
		apiBase = strings.TrimSpace(os.Getenv(slackAPIBaseEnv))
	}
	if apiBase == "" {
		apiBase = slackChatAPIDefaultBase
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}

	var payload slackStatusPayload
	if err := json.Unmarshal([]byte(opts.PayloadRaw), &payload); err != nil {
		return fmt.Errorf("decode --payload: %w", err)
	}
	blocks, err := renderStatusBlocks(slackStatusKind(opts.Kind), payload)
	if err != nil {
		return fmt.Errorf("render %s: %w", opts.Kind, err)
	}

	body := slackChatPostBody{
		Channel: opts.Channel,
		Text:    fallbackText(payload),
		Blocks:  blocks,
	}
	apiRoot := strings.TrimRight(apiBase, "/")
	endpoint := apiRoot + "/chat.postMessage"
	if strings.TrimSpace(opts.UpdateTS) != "" {
		body.TS = opts.UpdateTS
		endpoint = apiRoot + "/chat.update"
	}

	resp, err := postSlackChat(endpoint, token, body)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("slack chat api error: %s", resp.Error)
	}
	out := struct {
		OK      bool   `json:"ok"`
		TS      string `json:"ts"`
		Channel string `json:"channel"`
	}{OK: resp.OK, TS: resp.TS, Channel: resp.Channel}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	return nil
}

// fallbackText derives the notification/accessibility text for a Block
// Kit message. Slack requires `text` even when `blocks` is set —
// notifications, screen readers, and old clients render it. Title +
// summary is the most useful summary to surface there.
func fallbackText(p slackStatusPayload) string {
	if p.Summary != "" {
		return p.Title + " — " + p.Summary
	}
	return p.Title
}

// postSlackChat issues the HTTP POST to Slack and returns the parsed
// response. Non-2xx returns a slackHTTPError; decode failures wrap the
// underlying decoder error with the response body for diagnostics.
func postSlackChat(endpoint, token string, body slackChatPostBody) (*slackChatPostResponse, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("build slack request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read slack response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &slackHTTPError{Status: resp.StatusCode, Body: string(respBody)}
	}
	var out slackChatPostResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decode slack response: %w (body=%s)", err, string(respBody))
	}
	return &out, nil
}
