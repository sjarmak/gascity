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
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// Public listener: serves /slack/events only. Bind to 0.0.0.0 so
	// Tailscale Funnel can reach it.
	defaultPublicListen = ":8765"
	// Internal listener: serves /publish only. Bound to 127.0.0.1 so
	// only processes on this machine (i.e. gc) can reach it.
	defaultInternalListen   = "127.0.0.1:8766"
	defaultInternalCallback = "http://127.0.0.1:8766"
)

// slackAPIBase is a var (not const) so tests can replace it with a fake.
var slackAPIBase = "https://slack.com/api"

type config struct {
	publicListen        string
	internalListen      string // unused when serviceSocket is set
	serviceSocket       string // when set, bind a UDS here for /publish instead of internalListen
	internalCallbackURL string
	gcAPIBase           string
	cityName            string
	provider            string
	accountID           string
	slackBotToken       string
	slackSigningKey     string
	registerOnStart     bool
	// identityStorePath is the JSON file backing the per-session Slack
	// identity registry (chat:write.customize username/avatar overrides).
	// Persisted so adapter restarts don't strip identity from running
	// sessions.
	identityStorePath string
	// handlePrefix is the leading address token recognized on inbound
	// messages (e.g. "@"). When a message starts with
	// `<prefix><handle>:`, the handle is extracted into ExplicitTarget
	// and the prefix is stripped from the forwarded text. Empty disables
	// keyword routing.
	handlePrefix string
	// handleAliasStorePath is the JSON file backing the handle-alias
	// registry. Maps handle -> gc session id; used to dispatch
	// cross-channel address-by-handle messages (e.g. `@mayor:` from
	// any channel routes to the Mayor session even though Mayor has
	// no Slack binding).
	handleAliasStorePath string
	// inboundFileStore is the local directory where inbound Slack
	// file attachments are written so PLs can Read them directly
	// (no bot-token leak). Files are organized as
	// <store>/<channel>/<ts>-<safe-filename>.
	inboundFileStore string
	// inboundFileTTL is the maximum age (mtime-based) of files in
	// inboundFileStore before the in-process janitor deletes them.
	// Empty or zero disables the janitor entirely.
	inboundFileTTL time.Duration
	// inboundFileSweepInterval is how often the janitor wakes up to
	// scan inboundFileStore. Empty or zero disables the janitor.
	inboundFileSweepInterval time.Duration
}

func loadConfig() (config, error) {
	return loadConfigFromEnv(os.Getenv)
}

// loadConfigFromEnv reads adapter configuration from a getenv function. When
// $GC_SERVICE_SOCKET is set, the adapter switches to proxy_process mode: it
// binds a Unix domain socket for /publish (and /healthz) instead of an
// internal TCP listener, and registers the callback URL gc routes through
// its /svc/{name} mount. This keeps a single binary serving both the legacy
// nohup-managed deployment and the proxy_process deployment.
func loadConfigFromEnv(getenv func(string) string) (config, error) {
	envOrFn := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}
	cfg := config{
		publicListen:         envOrFn("LISTEN_PUBLIC", defaultPublicListen),
		internalListen:       envOrFn("LISTEN_INTERNAL", defaultInternalListen),
		serviceSocket:        getenv("GC_SERVICE_SOCKET"),
		internalCallbackURL:  strings.TrimRight(envOrFn("INTERNAL_CALLBACK_URL", defaultInternalCallback), "/"),
		gcAPIBase:            strings.TrimRight(envOrFn("GC_API_BASE_URL", "http://127.0.0.1:9443"), "/"),
		cityName:             envOrFn("GC_CITY_NAME", "ds-research"),
		provider:             envOrFn("ADAPTER_PROVIDER", "slack"),
		accountID:            getenv("SLACK_WORKSPACE_ID"),
		slackBotToken:        getenv("SLACK_BOT_TOKEN"),
		slackSigningKey:      getenv("SLACK_SIGNING_SECRET"),
		registerOnStart:      envOrFn("REGISTER_ON_START", "true") == "true",
		identityStorePath:    envOrFn("IDENTITY_STORE_PATH", "/tmp/gc-slack-adapter/identities.json"),
		handlePrefix:         envOrFn("HANDLE_PREFIX", "@"),
		handleAliasStorePath: envOrFn("HANDLE_ALIAS_STORE_PATH", "/tmp/gc-slack-adapter/handle-aliases.json"),
		inboundFileStore:     envOrFn("INBOUND_FILE_STORE", "/tmp/gc-slack-adapter/inbound"),
	}

	// Retention controls. Defaults: keep inbound files for 7 days,
	// sweep every hour. Setting either to "0" disables the janitor.
	// Invalid duration strings also disable (with a fatal-config error
	// would be too aggressive — log and continue without sweeping).
	if d, err := time.ParseDuration(envOrFn("INBOUND_FILE_TTL", "168h")); err == nil {
		cfg.inboundFileTTL = d
	} else {
		log.Printf("INBOUND_FILE_TTL %q invalid: %v (janitor disabled)", getenv("INBOUND_FILE_TTL"), err)
	}
	if d, err := time.ParseDuration(envOrFn("INBOUND_FILE_SWEEP_INTERVAL", "1h")); err == nil {
		cfg.inboundFileSweepInterval = d
	} else {
		log.Printf("INBOUND_FILE_SWEEP_INTERVAL %q invalid: %v (janitor disabled)", getenv("INBOUND_FILE_SWEEP_INTERVAL"), err)
	}

	if cfg.serviceSocket != "" {
		// proxy_process mode: gc reaches us via $GC_API_BASE_URL +
		// $GC_SERVICE_URL_PREFIX (e.g. http://127.0.0.1:8372/svc/slack).
		// gc's extmsg HTTP adapter appends "/publish" itself when calling,
		// so the registered base URL must NOT include /publish.
		urlPrefix := strings.TrimRight(getenv("GC_SERVICE_URL_PREFIX"), "/")
		if urlPrefix == "" {
			return cfg, errors.New("GC_SERVICE_SOCKET is set but GC_SERVICE_URL_PREFIX is empty — controller-injected env is incomplete")
		}
		if cfg.gcAPIBase == "" {
			return cfg, errors.New("GC_SERVICE_SOCKET is set but GC_API_BASE_URL is empty — cannot compute callback URL for self-registration")
		}
		cfg.internalCallbackURL = cfg.gcAPIBase + urlPrefix
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
	SessionID        string            `json:"session_id"`
	Conversation     conversationRef   `json:"conversation"`
	Text             string            `json:"text"`
	ReplyToMessageID string            `json:"reply_to_message_id,omitempty"`
	IdempotencyKey   string            `json:"idempotency_key,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// metadataKeySourceSessionID is the legacy metadata key gc used to
// propagate the originating session id before PublishRequest gained a
// native SessionID field (gc-kvt). Modern gc binaries write SessionID
// directly; this fallback exists only so older gc binaries publishing
// through this adapter still resolve the per-session identity record.
const metadataKeySourceSessionID = "source_session_id"

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

// externalAttachment mirrors extmsg.ExternalAttachment on the gc side.
// URL is a `file://` local path when the adapter has downloaded the bytes
// for inbound files (so PLs can Read it directly without leaking the bot
// token); for outbound transcripts that originated as outbound files, URL
// is the Slack permalink.
type externalAttachment struct {
	ProviderID string `json:"provider_id"`
	URL        string `json:"url"`
	MIMEType   string `json:"mime_type,omitempty"`
}

type externalInboundMessage struct {
	ProviderMessageID string               `json:"provider_message_id"`
	Conversation      conversationRef      `json:"conversation"`
	Actor             externalActor        `json:"actor"`
	Text              string               `json:"text"`
	ExplicitTarget    string               `json:"explicit_target,omitempty"`
	ReplyToMessageID  string               `json:"reply_to_message_id,omitempty"`
	Attachments       []externalAttachment `json:"attachments,omitempty"`
	DedupKey          string               `json:"dedup_key,omitempty"`
	ReceivedAt        time.Time            `json:"received_at"`
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
	Channel   string `json:"channel"`
	Text      string `json:"text"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	Username  string `json:"username,omitempty"`
	IconURL   string `json:"icon_url,omitempty"`
	IconEmoji string `json:"icon_emoji,omitempty"`
}

type slackPostMessageResp struct {
	OK      bool   `json:"ok"`
	TS      string `json:"ts,omitempty"`
	Channel string `json:"channel,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Slack files-upload-v2 API types.
//
// Slack deprecated the legacy /files.upload endpoint; the supported flow is
// the three-step v2 protocol:
//
//	1. POST /files.getUploadURLExternal (form-urlencoded) with {filename, length}
//	   → {ok, upload_url, file_id}
//	2. PUT raw bytes to the returned upload_url (no auth header — the URL is
//	   pre-signed and short-lived).
//	3. POST /files.completeUploadExternal (JSON) with {files: [{id, title}],
//	   channel_id, initial_comment, thread_ts} — channel posting happens here.
//
// The bot token requires the `files:write` scope. Without it, step 1 returns
// {ok: false, error: "missing_scope"} and the failure propagates as
// FailureKind="permanent" with the auth error logged.

type slackGetUploadURLResp struct {
	OK        bool   `json:"ok"`
	UploadURL string `json:"upload_url,omitempty"`
	FileID    string `json:"file_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

type slackCompleteUploadFile struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

type slackCompleteUploadReq struct {
	Files          []slackCompleteUploadFile `json:"files"`
	ChannelID      string                    `json:"channel_id,omitempty"`
	InitialComment string                    `json:"initial_comment,omitempty"`
	ThreadTS       string                    `json:"thread_ts,omitempty"`
}

type slackCompleteUploadResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Files []struct {
		ID string `json:"id"`
	} `json:"files,omitempty"`
}

// publishFileRequest is the body of POST /publish-file. Mirrors
// publishRequest but adds a file payload (path on the local filesystem
// the adapter can read). The session-id resolution precedence is the
// same: explicit SessionID wins over Metadata["source_session_id"].
type publishFileRequest struct {
	SessionID        string            `json:"session_id,omitempty"`
	Conversation     conversationRef   `json:"conversation"`
	FilePath         string            `json:"file_path"`
	Filename         string            `json:"filename,omitempty"`
	InitialComment   string            `json:"initial_comment,omitempty"`
	ReplyToMessageID string            `json:"reply_to_message_id,omitempty"`
	Title            string            `json:"title,omitempty"`
	IdempotencyKey   string            `json:"idempotency_key,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

// publishFileReceipt mirrors publishReceipt but carries the Slack file_id
// instead of a chat ts. When Delivered=true, FileID is the canonical
// reference for the uploaded file (used by tests + downstream tooling).
type publishFileReceipt struct {
	Conversation conversationRef `json:"conversation"`
	FileID       string          `json:"file_id,omitempty"`
	Delivered    bool            `json:"delivered"`
	FailureKind  string          `json:"failure_kind,omitempty"`
	Error        string          `json:"error,omitempty"`
}

type slackReactionsAddReq struct {
	Channel   string `json:"channel"`
	Name      string `json:"name"`
	Timestamp string `json:"timestamp"`
}

type slackReactionsAddResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// reactRequest is the body the slack pack POSTs to /react. The conversation
// id is the Slack channel id; the message id is the Slack ts. Emoji is the
// reaction name without colons (e.g. "eyes", not ":eyes:").
type reactRequest struct {
	Conversation conversationRef `json:"conversation"`
	MessageID    string          `json:"message_id"`
	Emoji        string          `json:"emoji"`
}

type reactReceipt struct {
	Delivered   bool   `json:"delivered"`
	FailureKind string `json:"failure_kind,omitempty"`
}

// identityRecord is the persisted Slack identity override for a single gc
// session id. All fields are optional; an empty record means "use the default
// bot identity for any publish from this session". Slack's chat.postMessage
// requires the `chat:write.customize` scope for these fields to take effect.
type identityRecord struct {
	Username  string `json:"username,omitempty"`
	IconURL   string `json:"icon_url,omitempty"`
	IconEmoji string `json:"icon_emoji,omitempty"`
}

// identityRequest is the body of POST /identity. SessionID is required;
// every other field is optional. Posting an empty record (only session_id)
// effectively resets the session back to the default bot identity.
type identityRequest struct {
	SessionID string `json:"session_id"`
	Username  string `json:"username,omitempty"`
	IconURL   string `json:"icon_url,omitempty"`
	IconEmoji string `json:"icon_emoji,omitempty"`
}

type identityReceipt struct {
	Stored    bool   `json:"stored"`
	SessionID string `json:"session_id,omitempty"`
}

// identityDeleteReceipt is the response body of DELETE /identity. Existed
// is true when the session id was actually registered before; the call
// succeeds either way (idempotent delete).
type identityDeleteReceipt struct {
	Removed   bool   `json:"removed"`
	Existed   bool   `json:"existed"`
	SessionID string `json:"session_id,omitempty"`
}

// handleAliasRequest is the body of POST /handle-alias. Empty session_id
// removes the alias.
type handleAliasRequest struct {
	Handle    string `json:"handle"`
	SessionID string `json:"session_id"`
}

type handleAliasReceipt struct {
	Stored    bool   `json:"stored"`
	Removed   bool   `json:"removed,omitempty"`
	Handle    string `json:"handle,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// handleAliasDeleteReceipt mirrors identityDeleteReceipt for the alias
// registry. Existed is true iff the handle was actually registered.
type handleAliasDeleteReceipt struct {
	Removed bool   `json:"removed"`
	Existed bool   `json:"existed"`
	Handle  string `json:"handle,omitempty"`
}

// gcSessionMessageRequest mirrors handler_session_interaction.go's
// sessionMessageRequest. We POST it to gc /v0/session/{id}/messages to
// inject a system reminder into a session that has no binding for the
// originating Slack conversation.
type gcSessionMessageRequest struct {
	Message string `json:"message"`
}

type slackEventEnvelope struct {
	Type      string          `json:"type"`
	Challenge string          `json:"challenge,omitempty"`
	TeamID    string          `json:"team_id,omitempty"`
	APIAppID  string          `json:"api_app_id,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"`
}

// slackFile is a subset of Slack's file object, just the fields we need
// to download the bytes and pass useful metadata up to gc.
type slackFile struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Title      string `json:"title,omitempty"`
	URLPrivate string `json:"url_private,omitempty"`
	MIMEType   string `json:"mimetype,omitempty"`
}

type slackMessageEvent struct {
	Type        string      `json:"type"`
	Subtype     string      `json:"subtype,omitempty"`
	User        string      `json:"user,omitempty"`
	BotID       string      `json:"bot_id,omitempty"`
	Text        string      `json:"text,omitempty"`
	Channel     string      `json:"channel,omitempty"`
	TS          string      `json:"ts,omitempty"`
	ThreadTS    string      `json:"thread_ts,omitempty"`
	EventTS     string      `json:"event_ts,omitempty"`
	ChannelType string      `json:"channel_type,omitempty"`
	Files       []slackFile `json:"files,omitempty"`
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	internalDescr := cfg.internalListen
	if cfg.serviceSocket != "" {
		internalDescr = "uds:" + cfg.serviceSocket
	}
	log.Printf("starting gc-slack-adapter public=%s internal=%s gc=%s city=%s",
		cfg.publicListen, internalDescr, cfg.gcAPIBase, cfg.cityName)

	identityReg, err := newIdentityRegistry(cfg.identityStorePath)
	if err != nil {
		log.Fatalf("identity registry: %v", err)
	}
	log.Printf("identity registry: store=%s", cfg.identityStorePath)

	aliasReg, err := newHandleAliasRegistry(cfg.handleAliasStorePath)
	if err != nil {
		log.Fatalf("handle alias registry: %v", err)
	}
	log.Printf("handle alias registry: store=%s", cfg.handleAliasStorePath)

	// Public mux: only /slack/events (HMAC-verified) and /healthz.
	// Bound to 0.0.0.0 by default so Tailscale Funnel can reach it.
	publicMux := http.NewServeMux()
	publicMux.HandleFunc("/slack/events", handleSlackEvents(cfg, aliasReg))
	publicMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	publicMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	// Internal mux: /publish (gc-only). Served either on a UDS that gc
	// proxies through /svc/{name}/ (proxy_process mode), or on a
	// 127.0.0.1 TCP listener (legacy nohup mode).
	internalMux := http.NewServeMux()
	internalMux.HandleFunc("/publish", handlePublish(cfg, identityReg))
	internalMux.HandleFunc("/publish-file", handlePublishFile(cfg, identityReg))
	internalMux.HandleFunc("/react", handleReact(cfg))
	internalMux.HandleFunc("POST /identity", handleIdentity(identityReg))
	internalMux.HandleFunc("DELETE /identity", handleIdentityDelete(identityReg))
	internalMux.HandleFunc("POST /handle-alias", handleHandleAlias(aliasReg))
	internalMux.HandleFunc("DELETE /handle-alias", handleHandleAliasDelete(aliasReg))
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
		Handler:           internalMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if cfg.registerOnStart {
		if err := registerAdapter(cfg); err != nil {
			log.Fatalf("register adapter: %v", err)
		}
		mode := "LOCALHOST ONLY"
		if cfg.serviceSocket != "" {
			mode = "via gc /svc proxy"
		}
		log.Printf("registered with gc as provider=%s account=%s callback=%s/publish (%s)",
			cfg.provider, cfg.accountID, cfg.internalCallbackURL, mode)
	}

	janitorCtx, janitorCancel := context.WithCancel(context.Background())
	defer janitorCancel()
	go runInboundFileJanitor(janitorCtx, cfg)

	errCh := make(chan error, 2)
	go func() {
		log.Printf("public listener serving on %s (Slack events)", cfg.publicListen)
		errCh <- publicSrv.ListenAndServe()
	}()
	go func() {
		if cfg.serviceSocket != "" {
			log.Printf("internal listener serving on UDS %s (gc proxy_process)", cfg.serviceSocket)
			lis, err := listenUDS(cfg.serviceSocket)
			if err != nil {
				errCh <- fmt.Errorf("listen unix %s: %w", cfg.serviceSocket, err)
				return
			}
			errCh <- internalSrv.Serve(lis)
		} else {
			internalSrv.Addr = cfg.internalListen
			log.Printf("internal listener serving on %s (gc publish only)", cfg.internalListen)
			errCh <- internalSrv.ListenAndServe()
		}
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

// listenUDS binds a Unix domain socket at path, removing any stale entry
// first so restarts succeed. The socket file is left in place on shutdown
// — the controller's proxy_process supervisor cleans it up via
// cleanupProxyProcessSocketPath when the service is closed.
func listenUDS(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	return net.Listen("unix", path)
}

func registerAdapter(cfg config) error {
	body, _ := json.Marshal(adapterRegisterRequest{
		Provider:    cfg.provider,
		AccountID:   cfg.accountID,
		Name:        "slack-adapter",
		CallbackURL: cfg.internalCallbackURL,
		Capabilities: adapterCapabilities{
			SupportsChildConversations: false,
			SupportsAttachments:        true,
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

func handlePublish(cfg config, reg *identityRegistry) http.HandlerFunc {
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

		post := slackPostMessageReq{
			Channel:  req.Conversation.ConversationID,
			Text:     req.Text,
			ThreadTS: req.ReplyToMessageID,
		}
		// SessionID precedence: explicit field wins (used by direct-to-adapter
		// callers like smoke tests). Otherwise fall back to the wire-metadata
		// key gc populates when forwarding from /v0/city/.../extmsg/outbound.
		identitySessionID := req.SessionID
		if identitySessionID == "" {
			identitySessionID = req.Metadata[metadataKeySourceSessionID]
		}
		identityApplied := ""
		if reg != nil && identitySessionID != "" {
			if rec, ok := reg.Get(identitySessionID); ok {
				post.Username = rec.Username
				post.IconURL = rec.IconURL
				post.IconEmoji = rec.IconEmoji
				identityApplied = rec.Username
			}
		}
		log.Printf("publish: conv=%s text=%dch reply_to=%s idem=%s session=%s as=%q",
			req.Conversation.ConversationID, len(req.Text), req.ReplyToMessageID,
			req.IdempotencyKey, identitySessionID, identityApplied)

		slackResp, err := postToSlack(cfg.slackBotToken, post)
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

// handlePublishFile serves POST /publish-file. It uploads the file at
// req.FilePath to Slack via the files-upload-v2 protocol and posts it to
// req.Conversation.ConversationID, optionally threaded under
// req.ReplyToMessageID. The bot token requires the `files:write` scope —
// without it, Slack returns {ok: false, error: "missing_scope"} and the
// receipt's FailureKind is "permanent".
//
// Slack's files.completeUploadExternal does NOT accept chat:write.customize
// username/icon overrides, so file posts appear under the default bot
// identity even when an identity record is registered for the source
// session. This is a Slack platform limitation, not an adapter bug.
// The identity lookup still happens for log parity with /publish.
func handlePublishFile(cfg config, reg *identityRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req publishFileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.FilePath) == "" {
			http.Error(w, "file_path is required", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Conversation.ConversationID) == "" {
			http.Error(w, "conversation.conversation_id is required", http.StatusBadRequest)
			return
		}
		fi, err := os.Stat(req.FilePath)
		if err != nil {
			http.Error(w, fmt.Sprintf("file_path: %v", err), http.StatusBadRequest)
			return
		}
		if fi.IsDir() {
			http.Error(w, "file_path is a directory", http.StatusBadRequest)
			return
		}
		fileBytes, err := os.ReadFile(req.FilePath)
		if err != nil {
			http.Error(w, fmt.Sprintf("read file_path: %v", err), http.StatusInternalServerError)
			return
		}
		filename := req.Filename
		if filename == "" {
			filename = filepath.Base(req.FilePath)
		}
		title := req.Title
		if title == "" {
			title = filename
		}

		// Identity lookup: same precedence as /publish. Logged for parity
		// even though Slack's file-upload API ignores chat:write.customize
		// overrides.
		identitySessionID := req.SessionID
		if identitySessionID == "" {
			identitySessionID = req.Metadata[metadataKeySourceSessionID]
		}
		identityApplied := ""
		if reg != nil && identitySessionID != "" {
			if rec, ok := reg.Get(identitySessionID); ok {
				identityApplied = rec.Username
			}
		}
		log.Printf("publish-file: conv=%s file=%s size=%d reply_to=%s session=%s as=%q",
			req.Conversation.ConversationID, filename, len(fileBytes),
			req.ReplyToMessageID, identitySessionID, identityApplied)

		receipt := publishFileReceipt{Conversation: req.Conversation}

		// Step 1: get a pre-signed upload URL.
		urlResp, err := slackGetUploadURL(cfg.slackBotToken, filename, len(fileBytes))
		if err != nil {
			log.Printf("slack files.getUploadURLExternal error: %v", err)
			receipt.FailureKind = "transient"
			receipt.Error = err.Error()
			writeJSON(w, receipt)
			return
		}
		if !urlResp.OK {
			log.Printf("slack files.getUploadURLExternal returned error: %s", urlResp.Error)
			receipt.FailureKind = mapSlackError(urlResp.Error)
			receipt.Error = urlResp.Error
			writeJSON(w, receipt)
			return
		}

		// Step 2: POST bytes (multipart) to the pre-signed URL.
		if err := slackPutFileBytes(urlResp.UploadURL, filename, fileBytes); err != nil {
			log.Printf("slack file upload error: %v", err)
			receipt.FailureKind = "transient"
			receipt.Error = err.Error()
			writeJSON(w, receipt)
			return
		}

		// Step 3: complete the upload — channel posting happens here.
		completeReq := slackCompleteUploadReq{
			Files:          []slackCompleteUploadFile{{ID: urlResp.FileID, Title: title}},
			ChannelID:      req.Conversation.ConversationID,
			InitialComment: req.InitialComment,
			ThreadTS:       req.ReplyToMessageID,
		}
		completeResp, err := slackCompleteUpload(cfg.slackBotToken, completeReq)
		if err != nil {
			log.Printf("slack files.completeUploadExternal error: %v", err)
			receipt.FailureKind = "transient"
			receipt.Error = err.Error()
			writeJSON(w, receipt)
			return
		}
		if !completeResp.OK {
			log.Printf("slack files.completeUploadExternal returned error: %s", completeResp.Error)
			receipt.FailureKind = mapSlackError(completeResp.Error)
			receipt.Error = completeResp.Error
			writeJSON(w, receipt)
			return
		}

		receipt.Delivered = true
		receipt.FileID = urlResp.FileID
		writeJSON(w, receipt)
	}
}

// writeJSON writes the receipt as a JSON response. Errors during encoding
// are logged but not surfaced — the receipt body is best-effort and the
// caller has the HTTP status anyway.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON encode error: %v", err)
	}
}

// mapSlackError maps a Slack error code to a publishReceipt failure kind.
// Shared between /publish and /publish-file so the contract is consistent.
func mapSlackError(slackErr string) string {
	switch slackErr {
	case "channel_not_found", "not_in_channel", "file_not_found":
		return "not_found"
	case "invalid_auth", "not_authed", "token_revoked", "missing_scope", "no_permission":
		return "auth"
	case "rate_limited", "ratelimited":
		return "rate_limited"
	case "":
		return ""
	default:
		return "permanent"
	}
}

// slackGetUploadURL calls files.getUploadURLExternal. Slack accepts both
// form-urlencoded body and query string for this endpoint; we use form
// to keep secrets out of access logs. The returned upload_url is a
// pre-signed URL valid for ~10 minutes.
func slackGetUploadURL(token, filename string, length int) (*slackGetUploadURLResp, error) {
	form := url.Values{}
	form.Set("filename", filename)
	form.Set("length", strconv.Itoa(length))
	httpReq, err := http.NewRequest(http.MethodPost,
		slackAPIBase+"/files.getUploadURLExternal",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	var sr slackGetUploadURLResp
	if err := json.Unmarshal(respBody, &sr); err != nil {
		return nil, fmt.Errorf("decode slack: %w (body=%s)", err, string(respBody))
	}
	return &sr, nil
}

// slackPutFileBytes POSTs the file contents to a pre-signed Slack upload
// URL using multipart/form-data with a single “filename“ field. The URL
// itself encodes auth — no Bearer header needed. Slack returns 200 OK with
// "OK - <bytes>" on success; we treat any non-2xx as a transport failure.
//
// History: an earlier revision used PUT with Content-Type:
// application/octet-stream. Slack accepted the bytes (returns 200 OK) and
// files.completeUploadExternal returned ok:true with a file_id, but the
// resulting file had empty mimetype/filetype and never actually appeared
// in the channel — files.info reported `shares: {}` and conversations.history
// did not contain the post. The pre-signed URL evidently treats the
// multipart-with-filename pattern as the canonical shape; raw PUT silently
// degrades to a "ghost upload" the channel post step can't bind to.
func slackPutFileBytes(uploadURL string, filename string, body []byte) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("filename", filename)
	if err != nil {
		return fmt.Errorf("create multipart form file: %w", err)
	}
	if _, err := part.Write(body); err != nil {
		return fmt.Errorf("write multipart body: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("upload POST %s: %s — %s", uploadURL, resp.Status, string(respBody))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// slackCompleteUpload calls files.completeUploadExternal with a JSON body.
// Channel posting (and threading via thread_ts) happens here, not in a
// separate chat.postMessage call.
func slackCompleteUpload(token string, req slackCompleteUploadReq) (*slackCompleteUploadResp, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost,
		slackAPIBase+"/files.completeUploadExternal",
		bytes.NewReader(body))
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
	var sr slackCompleteUploadResp
	if err := json.Unmarshal(respBody, &sr); err != nil {
		return nil, fmt.Errorf("decode slack: %w (body=%s)", err, string(respBody))
	}
	return &sr, nil
}

// handleReact serves POST /react. It maps reactRequest → Slack
// reactions.add. Emoji name is forwarded verbatim minus surrounding
// colons (clients can send "eyes" or ":eyes:").
func handleReact(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req reactRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
			return
		}
		emoji := strings.Trim(req.Emoji, ":")
		if emoji == "" || req.Conversation.ConversationID == "" || req.MessageID == "" {
			http.Error(w, "conversation.conversation_id, message_id, and emoji are required", http.StatusBadRequest)
			return
		}
		log.Printf("react: conv=%s ts=%s emoji=%s", req.Conversation.ConversationID, req.MessageID, emoji)

		slackResp, err := postReactionToSlack(cfg.slackBotToken, slackReactionsAddReq{
			Channel:   req.Conversation.ConversationID,
			Name:      emoji,
			Timestamp: req.MessageID,
		})
		receipt := reactReceipt{}
		switch {
		case err != nil:
			log.Printf("slack reactions.add error: %v", err)
			receipt.FailureKind = "transient"
		case !slackResp.OK:
			// "already_reacted" is benign: the emoji is already on the message.
			if slackResp.Error == "already_reacted" {
				receipt.Delivered = true
			} else {
				log.Printf("slack reactions.add returned error: %s", slackResp.Error)
				switch slackResp.Error {
				case "channel_not_found", "not_in_channel", "message_not_found":
					receipt.FailureKind = "not_found"
				case "invalid_auth", "not_authed", "token_revoked":
					receipt.FailureKind = "auth"
				case "rate_limited":
					receipt.FailureKind = "rate_limited"
				default:
					receipt.FailureKind = "permanent"
				}
			}
		default:
			receipt.Delivered = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(receipt)
	}
}

func postReactionToSlack(token string, req slackReactionsAddReq) (*slackReactionsAddResp, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest(http.MethodPost, slackAPIBase+"/reactions.add", bytes.NewReader(body))
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
	var sr slackReactionsAddResp
	if err := json.Unmarshal(respBody, &sr); err != nil {
		return nil, fmt.Errorf("decode slack: %w (body=%s)", err, string(respBody))
	}
	return &sr, nil
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

func handleSlackEvents(cfg config, aliasReg *handleAliasRegistry) http.HandlerFunc {
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
		go processSlackEvent(cfg, aliasReg, env)
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
//
//	"im"       -> direct message between two users  -> dm
//	"channel"  -> public channel                    -> room
//	"group"    -> private channel                   -> room
//	"mpim"     -> multi-party DM (group DM)         -> room
//
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

func processSlackEvent(cfg config, aliasReg *handleAliasRegistry, env slackEventEnvelope) {
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

	text := msg.Text
	target := ""
	if cfg.handlePrefix != "" {
		if h, rest := parseHandlePrefix(msg.Text, cfg.handlePrefix); h != "" {
			target = h
			text = rest
		}
	}

	var attachments []externalAttachment
	if len(msg.Files) > 0 {
		attachments = downloadSlackFiles(cfg, msg.Channel, msg.TS, msg.Files)
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
		Text:             text,
		ExplicitTarget:   target,
		ReplyToMessageID: msg.ThreadTS,
		Attachments:      attachments,
		DedupKey:         "slack-" + msg.TS,
		ReceivedAt:       time.Now().UTC(),
	}
	if err := postInbound(cfg, inbound); err != nil {
		log.Printf("inbound POST failed: %v", err)
		return
	}
	log.Printf("inbound: chan=%s user=%s ts=%s thread=%s target=%q files=%d text=%dch",
		msg.Channel, msg.User, msg.TS, msg.ThreadTS, target, len(attachments), len(text))

	// Cross-channel address-by-handle: if the parsed target matches a
	// registered alias (e.g. "mayor" or "cos"), dispatch the inbound
	// directly to the aliased session via gc's session-message API,
	// regardless of channel binding. The originating channel's PL still
	// sees the inbound (above) and stays silent per its prompt rule
	// because target != its handle.
	if target != "" && aliasReg != nil {
		if aliasedSessionID, ok := aliasReg.Get(target); ok {
			go dispatchToAliasedSession(cfg, aliasedSessionID, inbound, target)
		}
	}
}

// downloadSlackFiles fetches each file's bytes from Slack (Bearer-auth
// against url_private), writes them to
// $INBOUND_FILE_STORE/<channel>/<ts>-<safe-filename>, and returns
// externalAttachment records pointing at the local file:// path. Any file
// that fails to download is dropped from the returned slice and a
// warning is logged — the inbound is still posted with whatever files
// succeeded so the message itself isn't lost.
func downloadSlackFiles(cfg config, channel, ts string, files []slackFile) []externalAttachment {
	if cfg.inboundFileStore == "" {
		log.Printf("inbound file download skipped: INBOUND_FILE_STORE empty (%d files dropped)", len(files))
		return nil
	}
	channelDir := filepath.Join(cfg.inboundFileStore, channel)
	if err := os.MkdirAll(channelDir, 0o755); err != nil {
		log.Printf("inbound file download: mkdir %s: %v", channelDir, err)
		return nil
	}
	out := make([]externalAttachment, 0, len(files))
	for _, f := range files {
		if f.URLPrivate == "" {
			log.Printf("inbound file %s: url_private empty, dropped", f.ID)
			continue
		}
		name := f.Name
		if name == "" {
			name = f.Title
		}
		if name == "" {
			name = f.ID
		}
		dest := filepath.Join(channelDir, ts+"-"+safeFilename(name))
		if err := slackDownloadToFile(cfg.slackBotToken, f.URLPrivate, dest); err != nil {
			log.Printf("inbound file %s download failed: %v", f.ID, err)
			continue
		}
		out = append(out, externalAttachment{
			ProviderID: f.ID,
			URL:        "file://" + dest,
			MIMEType:   f.MIMEType,
		})
	}
	return out
}

// safeFilename strips path separators and other dangerous characters from
// a Slack-supplied filename so it can't escape the inbound file store
// directory. Length is capped at 200 chars (well under the typical 255
// filename limit) to leave room for the leading ts prefix.
func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "file"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == 0:
			b.WriteRune('_')
		case r < 0x20:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	cleaned := b.String()
	for strings.HasPrefix(cleaned, ".") {
		cleaned = "_" + cleaned[1:]
	}
	if len(cleaned) > 200 {
		cleaned = cleaned[:200]
	}
	if cleaned == "" {
		return "file"
	}
	return cleaned
}

// slackDownloadToFile GETs urlPrivate with a Bearer token and streams the
// body to dest via an atomic temp+rename. Non-2xx responses produce an
// error with the truncated body for diagnosis.
func slackDownloadToFile(token, urlPrivate, dest string) error {
	req, err := http.NewRequest(http.MethodGet, urlPrivate, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GET %s: %s — %s", urlPrivate, resp.Status, string(respBody))
	}
	tmp := dest + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open tmp %s: %w", tmp, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("copy body: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, dest, err)
	}
	return nil
}

// sweepResult summarizes one pass of the inbound file janitor. All counts
// are over a single sweep; aggregate behavior over time is not tracked
// (the bd issue gc-g52 was scoped to retention, not metrics).
type sweepResult struct {
	FilesRemoved int
	DirsRemoved  int
	BytesRemoved int64
	Errors       []error
}

// sweepInboundStore deletes regular files under root whose mtime is
// older than now-ttl, then removes any channel sub-directories that are
// empty after the file pass. Returns counts and any errors encountered;
// a missing root is not an error (the store is created lazily on first
// inbound). A non-positive ttl is a no-op so callers can guard at the
// config layer without re-checking here.
//
// The function is pure (no goroutines, no logging) so callers can test
// it deterministically with table-driven inputs and a fixed `now`.
func sweepInboundStore(root string, ttl time.Duration, now time.Time) sweepResult {
	var res sweepResult
	if root == "" || ttl <= 0 {
		return res
	}
	cutoff := now.Add(-ttl)

	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return res
		}
		res.Errors = append(res.Errors, fmt.Errorf("read root %s: %w", root, err))
		return res
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			// Files at the root are unexpected (the store layout puts
			// everything under <channel>/) — skip them so we don't delete
			// configuration the operator may have left there.
			continue
		}
		channelDir := filepath.Join(root, entry.Name())
		sweepChannelDir(channelDir, cutoff, &res)
	}
	return res
}

// sweepChannelDir applies the file-age filter to a single channel
// directory and removes the directory itself if it ends up empty.
// Errors are appended to res.Errors but never abort the sweep — one
// unreadable file shouldn't block the rest of the housekeeping pass.
func sweepChannelDir(channelDir string, cutoff time.Time, res *sweepResult) {
	files, err := os.ReadDir(channelDir)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Errorf("read %s: %w", channelDir, err))
		return
	}
	for _, f := range files {
		if !f.Type().IsRegular() {
			continue
		}
		path := filepath.Join(channelDir, f.Name())
		info, err := f.Info()
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("stat %s: %w", path, err))
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		size := info.Size()
		if err := os.Remove(path); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("remove %s: %w", path, err))
			continue
		}
		res.FilesRemoved++
		res.BytesRemoved += size
	}
	// Re-read to see if the directory is now empty; only remove if so.
	remaining, err := os.ReadDir(channelDir)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Errorf("re-read %s: %w", channelDir, err))
		return
	}
	if len(remaining) == 0 {
		if err := os.Remove(channelDir); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("rmdir %s: %w", channelDir, err))
			return
		}
		res.DirsRemoved++
	}
}

// runInboundFileJanitor wakes every cfg.inboundFileSweepInterval and
// runs sweepInboundStore against cfg.inboundFileStore using cfg.inboundFileTTL.
// Returns immediately if either duration is non-positive or the store
// path is empty (janitor disabled). Cancellation via ctx is honored
// between ticks; an in-flight sweep runs to completion since each pass
// is bounded by the directory size.
func runInboundFileJanitor(ctx context.Context, cfg config) {
	if cfg.inboundFileStore == "" || cfg.inboundFileTTL <= 0 || cfg.inboundFileSweepInterval <= 0 {
		log.Printf("inbound file janitor disabled (store=%q ttl=%s interval=%s)",
			cfg.inboundFileStore, cfg.inboundFileTTL, cfg.inboundFileSweepInterval)
		return
	}
	log.Printf("inbound file janitor started: store=%s ttl=%s interval=%s",
		cfg.inboundFileStore, cfg.inboundFileTTL, cfg.inboundFileSweepInterval)
	ticker := time.NewTicker(cfg.inboundFileSweepInterval)
	defer ticker.Stop()
	// Run one sweep promptly on startup so a long-uptime adapter doesn't
	// wait a full interval before the first pass.
	logSweepResult(sweepInboundStore(cfg.inboundFileStore, cfg.inboundFileTTL, time.Now()))
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			logSweepResult(sweepInboundStore(cfg.inboundFileStore, cfg.inboundFileTTL, time.Now()))
		}
	}
}

// logSweepResult emits one log line per sweep pass at most. Silent
// no-op passes (nothing removed, no errors) don't log to keep noise
// down on idle deployments.
func logSweepResult(res sweepResult) {
	if res.FilesRemoved == 0 && res.DirsRemoved == 0 && len(res.Errors) == 0 {
		return
	}
	log.Printf("inbound file janitor: files_removed=%d dirs_removed=%d bytes_removed=%d errors=%d",
		res.FilesRemoved, res.DirsRemoved, res.BytesRemoved, len(res.Errors))
	for _, err := range res.Errors {
		log.Printf("inbound file janitor error: %v", err)
	}
}

// parseHandlePrefix recognizes a leading address token of the form
// "<prefix><handle>" at the start of text, where the handle is followed
// by a colon, whitespace, or end-of-string. Leading whitespace before
// the prefix is tolerated. The handle character class is [A-Za-z0-9_-]
// (matches the rig naming convention). When matched, the handle is
// returned along with the remainder of the text (with any leading
// separator + single leading space trimmed); on no match, the original
// text is returned with an empty handle.
//
// Both `@cos: foo` and `@cos foo` are accepted because human users
// don't reliably type the colon — the colon is optional, but if it
// appears it must be the first character after the handle.
//
// Examples (with prefix "@"):
//
//	"@gascity: status?"      -> ("gascity", "status?")
//	"@cos foo"                -> ("cos",     "foo")
//	"@cos:hello"              -> ("cos",     "hello")
//	"  @mayor hi"             -> ("mayor",   "hi")
//	"@gascity"                -> ("gascity", "")
//	"@: foo"                  -> ("",        "@: foo")           (empty handle)
//	"hello @gascity: x"       -> ("",        "hello @gascity: x") (not at start)
//	"@bad/handle x"           -> ("",        "@bad/handle x")    (invalid separator after handle chars)
func parseHandlePrefix(text, prefix string) (handle, remainder string) {
	if prefix == "" {
		return "", text
	}
	trimmed := strings.TrimLeft(text, " \t")
	if !strings.HasPrefix(trimmed, prefix) {
		return "", text
	}
	rest := trimmed[len(prefix):]

	// Scan the longest run of valid handle characters at the start.
	handleEnd := 0
	for i := 0; i < len(rest); i++ {
		r := rest[i]
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			handleEnd = i + 1
		} else {
			break
		}
	}
	if handleEnd == 0 {
		return "", text
	}
	candidate := rest[:handleEnd]
	body := rest[handleEnd:]

	// Handle must end at: end-of-string, colon, or whitespace.
	// Anything else (e.g. `@cos.foo`) means this isn't an address token.
	if body == "" {
		return candidate, ""
	}
	sep := body[0]
	switch sep {
	case ':':
		body = body[1:]
	case ' ', '\t', '\n':
		// whitespace separator — leave it; the next trim handles it
	default:
		return "", text
	}
	if len(body) > 0 && (body[0] == ' ' || body[0] == '\t' || body[0] == '\n') {
		body = body[1:]
	}
	return candidate, body
}

// identityRegistry maps gc session ids to per-message Slack identity
// overrides (chat:write.customize username/avatar). When a publish arrives
// for a known session id, the adapter injects username/icon into
// chat.postMessage so each role posts under its own visible name + avatar
// without spinning up a separate bot user.
//
// The registry persists to disk (atomic temp + rename) so adapter restarts
// don't strip identity from running sessions. Reads are RLock-only so
// concurrent /publish calls don't serialize.
type identityRegistry struct {
	mu       sync.RWMutex
	byID     map[string]identityRecord
	diskPath string
}

func newIdentityRegistry(diskPath string) (*identityRegistry, error) {
	r := &identityRegistry{
		byID:     make(map[string]identityRecord),
		diskPath: diskPath,
	}
	if err := r.load(); err != nil {
		return nil, fmt.Errorf("load identity registry from %s: %w", diskPath, err)
	}
	return r, nil
}

// Get returns the identity record for sessionID, plus a boolean indicating
// whether one is registered. Callers should treat the no-record case as
// "use default bot identity" rather than an error.
func (r *identityRegistry) Get(sessionID string) (identityRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.byID[sessionID]
	return rec, ok
}

// Set stores rec for sessionID and persists the registry to disk. An empty
// record (zero username + icon fields) is allowed — it effectively unsets
// the override. To fully delete the entry use Delete instead.
func (r *identityRegistry) Set(sessionID string, rec identityRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[sessionID] = rec
	return r.saveLocked()
}

// Delete removes the identity record for sessionID and persists the
// registry. Returns whether an entry actually existed; missing entries
// are not an error so callers can treat Delete as idempotent.
func (r *identityRegistry) Delete(sessionID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, existed := r.byID[sessionID]
	delete(r.byID, sessionID)
	if err := r.saveLocked(); err != nil {
		return existed, err
	}
	return existed, nil
}

func (r *identityRegistry) load() error {
	if r.diskPath == "" {
		return nil
	}
	data, err := os.ReadFile(r.diskPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored map[string]identityRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("decode identity store: %w", err)
	}
	if stored != nil {
		r.byID = stored
	}
	return nil
}

func (r *identityRegistry) saveLocked() error {
	if r.diskPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.diskPath), 0o755); err != nil {
		return fmt.Errorf("mkdir identity store dir: %w", err)
	}
	data, err := json.MarshalIndent(r.byID, "", "  ")
	if err != nil {
		return fmt.Errorf("encode identity store: %w", err)
	}
	tmp := r.diskPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write identity store tmp: %w", err)
	}
	if err := os.Rename(tmp, r.diskPath); err != nil {
		return fmt.Errorf("rename identity store: %w", err)
	}
	return nil
}

// handleAliasRegistry maps a handle (e.g. "mayor", "cos") to a gc session
// id. Used by the cross-channel address-by-handle dispatcher: when a Slack
// inbound parses a handle that matches an alias, the adapter delivers the
// inbound directly to the aliased session via gc's session-message API,
// even if that session has no Slack binding for the originating channel.
//
// Persists to disk so restarts don't lose mappings; same atomic write
// pattern as the identity registry.
type handleAliasRegistry struct {
	mu       sync.RWMutex
	byHandle map[string]string
	diskPath string
}

func newHandleAliasRegistry(diskPath string) (*handleAliasRegistry, error) {
	r := &handleAliasRegistry{
		byHandle: make(map[string]string),
		diskPath: diskPath,
	}
	if err := r.load(); err != nil {
		return nil, fmt.Errorf("load handle alias registry from %s: %w", diskPath, err)
	}
	return r, nil
}

// Get returns the session id mapped to handle, plus a bool indicating
// whether one is registered. Callers should treat the no-record case as
// "not an alias — fall through to normal channel-binding routing".
func (r *handleAliasRegistry) Get(handle string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sid, ok := r.byHandle[handle]
	return sid, ok
}

// Set stores handle -> sessionID. Empty sessionID removes the entry.
func (r *handleAliasRegistry) Set(handle, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sessionID == "" {
		delete(r.byHandle, handle)
	} else {
		r.byHandle[handle] = sessionID
	}
	return r.saveLocked()
}

// Delete removes the alias for handle and persists the registry. Returns
// whether an entry actually existed; missing entries are not an error so
// callers can treat Delete as idempotent. This is the explicit counterpart
// to Set with empty sessionID; both work, but Delete is the intent-clear
// verb.
func (r *handleAliasRegistry) Delete(handle string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, existed := r.byHandle[handle]
	delete(r.byHandle, handle)
	if err := r.saveLocked(); err != nil {
		return existed, err
	}
	return existed, nil
}

func (r *handleAliasRegistry) load() error {
	if r.diskPath == "" {
		return nil
	}
	data, err := os.ReadFile(r.diskPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var stored map[string]string
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("decode handle alias store: %w", err)
	}
	if stored != nil {
		r.byHandle = stored
	}
	return nil
}

func (r *handleAliasRegistry) saveLocked() error {
	if r.diskPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.diskPath), 0o755); err != nil {
		return fmt.Errorf("mkdir handle alias store dir: %w", err)
	}
	data, err := json.MarshalIndent(r.byHandle, "", "  ")
	if err != nil {
		return fmt.Errorf("encode handle alias store: %w", err)
	}
	tmp := r.diskPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write handle alias store tmp: %w", err)
	}
	if err := os.Rename(tmp, r.diskPath); err != nil {
		return fmt.Errorf("rename handle alias store: %w", err)
	}
	return nil
}

// handleHandleAlias serves POST /handle-alias. Empty session_id removes
// the entry; non-empty stores or replaces it.
func handleHandleAlias(reg *handleAliasRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req handleAliasRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
			return
		}
		handle := strings.TrimSpace(req.Handle)
		if handle == "" {
			http.Error(w, "handle is required", http.StatusBadRequest)
			return
		}
		removed := strings.TrimSpace(req.SessionID) == ""
		if err := reg.Set(handle, strings.TrimSpace(req.SessionID)); err != nil {
			log.Printf("handle alias store error: %v", err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		log.Printf("handle alias: handle=%q session=%q removed=%v",
			handle, req.SessionID, removed)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(handleAliasReceipt{
			Stored:    !removed,
			Removed:   removed,
			Handle:    handle,
			SessionID: req.SessionID,
		})
	}
}

// handleHandleAliasDelete serves DELETE /handle-alias. The handle is
// taken from either ?handle= query param (preferred for explicit verb)
// or from a JSON body { "handle": "..." } for symmetry with POST. Empty
// handle is rejected.
func handleHandleAliasDelete(reg *handleAliasRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handle := strings.TrimSpace(r.URL.Query().Get("handle"))
		if handle == "" {
			var req handleAliasRequest
			if r.ContentLength > 0 {
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
					return
				}
				handle = strings.TrimSpace(req.Handle)
			}
		}
		if handle == "" {
			http.Error(w, "handle is required (?handle= or JSON body)", http.StatusBadRequest)
			return
		}
		existed, err := reg.Delete(handle)
		if err != nil {
			log.Printf("handle alias delete error: %v", err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		log.Printf("handle alias delete: handle=%q existed=%v", handle, existed)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(handleAliasDeleteReceipt{
			Removed: true,
			Existed: existed,
			Handle:  handle,
		})
	}
}

// dispatchToAliasedSession POSTs a system reminder to the gc session-message
// endpoint for the aliased session. The payload carries everything mayor /
// cos needs to compose a reply: originating channel id (for routing the
// reply back), message ts (for threading), and the inbound text.
//
// On error we log and continue — best-effort delivery; the originating
// channel's transcript still records the inbound regardless.
func dispatchToAliasedSession(cfg config, sessionID string, msg externalInboundMessage, handle string) {
	body := fmt.Sprintf(
		"<system-reminder>\n"+
			"Slack address-by-handle: @%s addressed you from channel %s (Slack ts %s) by user %s.\n"+
			"\n"+
			"Message text:\n"+
			"%s\n"+
			"\n"+
			"To reply in that channel (threaded under their message), write your reply to a tmpfile and run:\n"+
			"  gc slack publish-to-channel \\\n"+
			"    --conversation-id %s \\\n"+
			"    --thread-ts %s \\\n"+
			"    --body-file <tmpfile>\n"+
			"\n"+
			"This bypasses your local channel binding (you have none for that channel) and posts directly through the slack adapter, with your registered identity applied.\n"+
			"</system-reminder>",
		handle,
		msg.Conversation.ConversationID,
		msg.ProviderMessageID,
		msg.Actor.ID,
		msg.Text,
		msg.Conversation.ConversationID,
		msg.ProviderMessageID,
	)
	payload, _ := json.Marshal(gcSessionMessageRequest{Message: body})
	url := fmt.Sprintf("%s/v0/city/%s/session/%s/messages",
		cfg.gcAPIBase, cfg.cityName, sessionID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		log.Printf("alias dispatch: build request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "gc-slack-adapter-alias")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("alias dispatch: POST %s: %v", url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("alias dispatch: %s -> %s: %s", url, resp.Status, string(respBody))
		return
	}
	log.Printf("alias dispatch: handle=%s -> session=%s OK", handle, sessionID)
}

// handleIdentity serves POST /identity. The caller (gc slack identity)
// supplies a session_id and zero or more of {username, icon_url, icon_emoji}.
// The record is stored in the registry and persisted; subsequent publishes
// keyed by the same session_id pick up the override.
func handleIdentity(reg *identityRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req identityRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.SessionID) == "" {
			http.Error(w, "session_id is required", http.StatusBadRequest)
			return
		}
		rec := identityRecord{
			Username:  req.Username,
			IconURL:   req.IconURL,
			IconEmoji: req.IconEmoji,
		}
		if err := reg.Set(req.SessionID, rec); err != nil {
			log.Printf("identity store error: %v", err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		log.Printf("identity: session=%s username=%q icon_url=%q icon_emoji=%q",
			req.SessionID, rec.Username, rec.IconURL, rec.IconEmoji)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(identityReceipt{Stored: true, SessionID: req.SessionID})
	}
}

// handleIdentityDelete serves DELETE /identity. The session id is taken
// from either ?session_id= query param (preferred) or JSON body
// { "session_id": "..." }. Idempotent: missing entries return Existed=false
// without error.
func handleIdentityDelete(reg *identityRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
		if sessionID == "" {
			var req identityRequest
			if r.ContentLength > 0 {
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
					return
				}
				sessionID = strings.TrimSpace(req.SessionID)
			}
		}
		if sessionID == "" {
			http.Error(w, "session_id is required (?session_id= or JSON body)", http.StatusBadRequest)
			return
		}
		existed, err := reg.Delete(sessionID)
		if err != nil {
			log.Printf("identity delete error: %v", err)
			http.Error(w, "store failed", http.StatusInternalServerError)
			return
		}
		log.Printf("identity delete: session=%s existed=%v", sessionID, existed)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(identityDeleteReceipt{
			Removed:   true,
			Existed:   existed,
			SessionID: sessionID,
		})
	}
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
