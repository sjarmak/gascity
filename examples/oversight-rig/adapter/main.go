// gc-slack-adapter — out-of-process Slack ↔ gc extmsg bridge.
//
// Registers itself with the gc API as an extmsg adapter (provider=slack).
// Two HTTP endpoints:
//
//	POST /publish        — gc forwards outbound publish requests here;
//	                        we translate to Slack chat.postMessage.
//	POST /slack/events   — Slack forwards user events here; we verify the
//	                        signing secret, normalize, and POST to
//	                        gc /v0/city/{city}/extmsg/inbound.
//
// Threading: gc.PublishRequest.ReplyToMessageID is mapped to Slack
// thread_ts. Slack message ts is returned as PublishReceipt.MessageID
// so subsequent replies thread correctly.
//
// All configuration via env vars — keep secrets out of source.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// Public listener: serves /slack/events only. Bind to 0.0.0.0 so
	// Tailscale Funnel can reach it.
	defaultPublicListen = ":8765"
	// Internal listener: serves /publish only. Bound to 127.0.0.1 so
	// only processes on this machine (i.e. gc) can reach it.
	defaultInternalListen     = "127.0.0.1:8766"
	defaultInternalCallback   = "http://127.0.0.1:8766"
	slackAPIBase              = "https://slack.com/api"
)

type config struct {
	publicListen        string
	internalListen      string
	internalCallbackURL string
	gcAPIBase           string
	cityName            string
	provider            string
	accountID           string
	slackBotToken       string
	slackSigningKey     string
	registerOnStart     bool
}

func loadConfig() (config, error) {
	cfg := config{
		publicListen:        envOr("LISTEN_PUBLIC", defaultPublicListen),
		internalListen:      envOr("LISTEN_INTERNAL", defaultInternalListen),
		internalCallbackURL: strings.TrimRight(envOr("INTERNAL_CALLBACK_URL", defaultInternalCallback), "/"),
		gcAPIBase:           strings.TrimRight(envOr("GC_API_BASE_URL", "http://127.0.0.1:9443"), "/"),
		cityName:            envOr("GC_CITY_NAME", "ds-research"),
		provider:            envOr("ADAPTER_PROVIDER", "slack"),
		accountID:           os.Getenv("SLACK_WORKSPACE_ID"),
		slackBotToken:       os.Getenv("SLACK_BOT_TOKEN"),
		slackSigningKey:     os.Getenv("SLACK_SIGNING_SECRET"),
		registerOnStart:     envOr("REGISTER_ON_START", "true") == "true",
	}
	var missing []string
	if cfg.accountID == "" {
		missing = append(missing, "SLACK_WORKSPACE_ID")
	}
	if cfg.slackBotToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}
	if cfg.slackSigningKey == "" {
		missing = append(missing, "SLACK_SIGNING_SECRET")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// gc-side types — mirrored from internal/extmsg/types.go to avoid coupling
// to the gc module. Wire-compatible only.

type conversationRef struct {
	ScopeID              string `json:"scope_id"`
	Provider             string `json:"provider"`
	AccountID            string `json:"account_id"`
	ConversationID       string `json:"conversation_id"`
	ParentConversationID string `json:"parent_conversation_id,omitempty"`
	Kind                 string `json:"kind"`
}

type publishRequest struct {
	SessionID        string          `json:"session_id"`
	Conversation     conversationRef `json:"conversation"`
	Text             string          `json:"text"`
	ReplyToMessageID string          `json:"reply_to_message_id,omitempty"`
	IdempotencyKey   string          `json:"idempotency_key,omitempty"`
}

type publishReceipt struct {
	Conversation conversationRef `json:"conversation"`
	MessageID    string          `json:"message_id,omitempty"`
	Delivered    bool            `json:"delivered"`
	FailureKind  string          `json:"failure_kind,omitempty"`
}

type externalActor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	IsBot       bool   `json:"is_bot"`
}

type externalInboundMessage struct {
	ProviderMessageID string          `json:"provider_message_id"`
	Conversation      conversationRef `json:"conversation"`
	Actor             externalActor   `json:"actor"`
	Text              string          `json:"text"`
	ReplyToMessageID  string          `json:"reply_to_message_id,omitempty"`
	DedupKey          string          `json:"dedup_key,omitempty"`
	ReceivedAt        time.Time       `json:"received_at"`
}

type adapterCapabilities struct {
	SupportsChildConversations bool `json:"SupportsChildConversations"`
	SupportsAttachments        bool `json:"SupportsAttachments"`
	MaxMessageLength           int  `json:"MaxMessageLength"`
}

type adapterRegisterRequest struct {
	Provider     string              `json:"provider"`
	AccountID    string              `json:"account_id"`
	Name         string              `json:"name,omitempty"`
	CallbackURL  string              `json:"callback_url,omitempty"`
	Capabilities adapterCapabilities `json:"capabilities,omitempty"`
}

// Slack API types

type slackPostMessageReq struct {
	Channel  string `json:"channel"`
	Text     string `json:"text"`
	ThreadTS string `json:"thread_ts,omitempty"`
}

type slackPostMessageResp struct {
	OK      bool   `json:"ok"`
	TS      string `json:"ts,omitempty"`
	Channel string `json:"channel,omitempty"`
	Error   string `json:"error,omitempty"`
}

type slackEventEnvelope struct {
	Type      string          `json:"type"`
	Challenge string          `json:"challenge,omitempty"`
	TeamID    string          `json:"team_id,omitempty"`
	APIAppID  string          `json:"api_app_id,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"`
}

type slackMessageEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype,omitempty"`
	User      string `json:"user,omitempty"`
	BotID     string `json:"bot_id,omitempty"`
	Text      string `json:"text,omitempty"`
	Channel   string `json:"channel,omitempty"`
	TS        string `json:"ts,omitempty"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	EventTS   string `json:"event_ts,omitempty"`
	ChannelType string `json:"channel_type,omitempty"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("starting gc-slack-adapter public=%s internal=%s gc=%s city=%s",
		cfg.publicListen, cfg.internalListen, cfg.gcAPIBase, cfg.cityName)

	// Public mux: only /slack/events (HMAC-verified) and /healthz.
	// Bound to 0.0.0.0 by default so Tailscale Funnel can reach it.
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("/slack/events", handleSlackEvents(cfg))
	publicMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	publicMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	// Internal mux: /publish (gc-only). Bound to 127.0.0.1 so only
	// processes on this machine can reach it.
	internalMux := http.NewServeMux()
	internalMux.HandleFunc("/publish", handlePublish(cfg))
	internalMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	publicSrv := &http.Server{
		Addr:              cfg.publicListen,
		Handler:           publicMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	internalSrv := &http.Server{
		Addr:              cfg.internalListen,
		Handler:           internalMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if cfg.registerOnStart {
		if err := registerAdapter(cfg); err != nil {
			log.Fatalf("register adapter: %v", err)
		}
		log.Printf("registered with gc as provider=%s account=%s callback=%s/publish (LOCALHOST ONLY)",
			cfg.provider, cfg.accountID, cfg.internalCallbackURL)
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("public listener serving on %s (Slack events)", cfg.publicListen)
		errCh <- publicSrv.ListenAndServe()
	}()
	go func() {
		log.Printf("internal listener serving on %s (gc publish only)", cfg.internalListen)
		errCh <- internalSrv.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
		log.Println("shutting down (signal)")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("listener error: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = publicSrv.Shutdown(ctx)
	_ = internalSrv.Shutdown(ctx)
}

func registerAdapter(cfg config) error {
	body, _ := json.Marshal(adapterRegisterRequest{
		Provider:    cfg.provider,
		AccountID:   cfg.accountID,
		Name:        "slack-adapter",
		CallbackURL: cfg.internalCallbackURL,
		Capabilities: adapterCapabilities{
			SupportsChildConversations: false,
			SupportsAttachments:        false,
			MaxMessageLength:           40000, // Slack's chat.postMessage limit
		},
	})
	url := fmt.Sprintf("%s/v0/city/%s/extmsg/adapters", cfg.gcAPIBase, cfg.cityName)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "gc-slack-adapter")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("register failed: %s — %s", resp.Status, string(respBody))
	}
	return nil
}

func handlePublish(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req publishRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
			return
		}
		log.Printf("publish: conv=%s text=%dch reply_to=%s idem=%s",
			req.Conversation.ConversationID, len(req.Text), req.ReplyToMessageID, req.IdempotencyKey)

		slackResp, err := postToSlack(cfg.slackBotToken, slackPostMessageReq{
			Channel:  req.Conversation.ConversationID,
			Text:     req.Text,
			ThreadTS: req.ReplyToMessageID,
		})
		receipt := publishReceipt{Conversation: req.Conversation}
		switch {
		case err != nil:
			log.Printf("slack POST error: %v", err)
			receipt.Delivered = false
			receipt.FailureKind = "transient"
		case !slackResp.OK:
			log.Printf("slack returned error: %s", slackResp.Error)
			receipt.Delivered = false
			switch slackResp.Error {
			case "channel_not_found", "not_in_channel":
				receipt.FailureKind = "not_found"
			case "invalid_auth", "not_authed", "token_revoked":
				receipt.FailureKind = "auth"
			case "rate_limited":
				receipt.FailureKind = "rate_limited"
			default:
				receipt.FailureKind = "permanent"
			}
		default:
			receipt.Delivered = true
			receipt.MessageID = slackResp.TS
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(receipt)
	}
}

func postToSlack(token string, req slackPostMessageReq) (*slackPostMessageResp, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, slackAPIBase+"/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	var sr slackPostMessageResp
	if err := json.Unmarshal(respBody, &sr); err != nil {
		return nil, fmt.Errorf("decode slack: %w (body=%s)", err, string(respBody))
	}
	return &sr, nil
}

func handleSlackEvents(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		// Verify Slack signing secret.
		ts := r.Header.Get("X-Slack-Request-Timestamp")
		sig := r.Header.Get("X-Slack-Signature")
		if !verifySlackSignature(cfg.slackSigningKey, ts, body, sig) {
			log.Printf("slack signature verify FAILED")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		var env slackEventEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
			return
		}

		// URL verification challenge.
		if env.Type == "url_verification" && env.Challenge != "" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(env.Challenge))
			return
		}

		// Process event_callback. Always 200 quickly to avoid Slack retries.
		w.WriteHeader(http.StatusOK)
		go processSlackEvent(cfg, env)
	}
}

func verifySlackSignature(secret, ts string, body []byte, sig string) bool {
	if secret == "" || ts == "" || sig == "" {
		return false
	}
	// Reject stale requests (>5 min) to mitigate replay.
	if tsInt, err := strconv.ParseInt(ts, 10, 64); err == nil {
		if time.Since(time.Unix(tsInt, 0)) > 5*time.Minute {
			return false
		}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("v0:" + ts + ":"))
	_, _ = mac.Write(body)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sig))
}

// slackKindFromChannelType maps a Slack message event's channel_type
// onto a gc ConversationKind. Slack channel_type values are:
//   "im"       -> direct message between two users  -> dm
//   "channel"  -> public channel                    -> room
//   "group"    -> private channel                   -> room
//   "mpim"     -> multi-party DM (group DM)         -> room
// When channel_type is missing, fall back to the channel-id prefix
// (D=im, C=channel, G=group). Defaults to "dm" for safety.
func slackKindFromChannelType(channelType, channelID string) string {
	switch channelType {
	case "channel", "group", "mpim":
		return "room"
	case "im":
		return "dm"
	}
	if len(channelID) > 0 {
		switch channelID[0] {
		case 'C', 'G':
			return "room"
		case 'D':
			return "dm"
		}
	}
	return "dm"
}

func processSlackEvent(cfg config, env slackEventEnvelope) {
	if env.Type != "event_callback" || len(env.Event) == 0 {
		return
	}
	var msg slackMessageEvent
	if err := json.Unmarshal(env.Event, &msg); err != nil {
		log.Printf("decode slack event: %v", err)
		return
	}
	if msg.Type != "message" && msg.Type != "app_mention" {
		return
	}
	// Skip bot/system messages.
	if msg.BotID != "" || msg.Subtype != "" || msg.User == "" {
		return
	}
	if strings.TrimSpace(msg.Text) == "" {
		return
	}

	inbound := externalInboundMessage{
		ProviderMessageID: msg.TS,
		Conversation: conversationRef{
			ScopeID:        cfg.cityName,
			Provider:       cfg.provider,
			AccountID:      cfg.accountID,
			ConversationID: msg.Channel,
			Kind:           slackKindFromChannelType(msg.ChannelType, msg.Channel),
		},
		Actor: externalActor{
			ID:          msg.User,
			DisplayName: msg.User, // resolving display name needs users.info — defer
			IsBot:       false,
		},
		Text:             msg.Text,
		ReplyToMessageID: msg.ThreadTS,
		DedupKey:         "slack-" + msg.TS,
		ReceivedAt:       time.Now().UTC(),
	}
	if err := postInbound(cfg, inbound); err != nil {
		log.Printf("inbound POST failed: %v", err)
		return
	}
	log.Printf("inbound: chan=%s user=%s ts=%s thread=%s text=%dch",
		msg.Channel, msg.User, msg.TS, msg.ThreadTS, len(msg.Text))
}

func postInbound(cfg config, msg externalInboundMessage) error {
	body, _ := json.Marshal(map[string]any{
		"message": msg,
	})
	url := fmt.Sprintf("%s/v0/city/%s/extmsg/inbound", cfg.gcAPIBase, cfg.cityName)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "gc-slack-adapter")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, string(respBody))
	}
	return nil
}
