package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubEnv builds a getenv function from a fixed map, mirroring os.Getenv's
// "missing key returns empty string" contract.
func stubEnv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func baseSlackEnv() map[string]string {
	return map[string]string{
		"SLACK_WORKSPACE_ID":   "T01234567",
		"SLACK_BOT_TOKEN":      "xoxb-test",
		"SLACK_SIGNING_SECRET": "secret",
		// GC_CITY_NAME is must-set: every URL the adapter constructs
		// for gc-side calls is /v0/city/{cityName}/.... Tests
		// targeting alternate cities override this in their own env.
		"GC_CITY_NAME": "test-city",
	}
}

func TestLoadConfigLegacyTCPMode(t *testing.T) {
	env := baseSlackEnv()
	env["GC_API_BASE_URL"] = "http://127.0.0.1:9443"

	cfg, err := loadConfigFromEnv(stubEnv(env))
	if err != nil {
		t.Fatalf("loadConfigFromEnv: %v", err)
	}
	if cfg.serviceSocket != "" {
		t.Errorf("serviceSocket = %q, want empty in legacy mode", cfg.serviceSocket)
	}
	if cfg.internalListen != defaultInternalListen {
		t.Errorf("internalListen = %q, want default %q", cfg.internalListen, defaultInternalListen)
	}
	if cfg.internalCallbackURL != defaultInternalCallback {
		t.Errorf("internalCallbackURL = %q, want default %q", cfg.internalCallbackURL, defaultInternalCallback)
	}
}

func TestLoadConfigProxyProcessModeDerivesCallbackURL(t *testing.T) {
	env := baseSlackEnv()
	env["GC_SERVICE_SOCKET"] = "/tmp/gcsvc-1000/abcd/slack-xyz.sock"
	env["GC_SERVICE_URL_PREFIX"] = "/svc/slack"
	env["GC_API_BASE_URL"] = "http://127.0.0.1:8372"

	cfg, err := loadConfigFromEnv(stubEnv(env))
	if err != nil {
		t.Fatalf("loadConfigFromEnv: %v", err)
	}
	if cfg.serviceSocket != "/tmp/gcsvc-1000/abcd/slack-xyz.sock" {
		t.Errorf("serviceSocket = %q, want UDS path", cfg.serviceSocket)
	}
	want := "http://127.0.0.1:8372/svc/slack"
	if cfg.internalCallbackURL != want {
		t.Errorf("internalCallbackURL = %q, want %q (gc appends /publish itself)", cfg.internalCallbackURL, want)
	}
	if strings.HasSuffix(cfg.internalCallbackURL, "/publish") {
		t.Errorf("internalCallbackURL = %q must not include /publish suffix; gc's extmsg http_adapter appends it", cfg.internalCallbackURL)
	}
}

func TestLoadConfigProxyProcessModeStripsTrailingSlashes(t *testing.T) {
	env := baseSlackEnv()
	env["GC_SERVICE_SOCKET"] = "/tmp/x.sock"
	env["GC_SERVICE_URL_PREFIX"] = "/svc/slack/"
	env["GC_API_BASE_URL"] = "http://127.0.0.1:8372/"

	cfg, err := loadConfigFromEnv(stubEnv(env))
	if err != nil {
		t.Fatalf("loadConfigFromEnv: %v", err)
	}
	want := "http://127.0.0.1:8372/svc/slack"
	if cfg.internalCallbackURL != want {
		t.Errorf("internalCallbackURL = %q, want %q (no double slash)", cfg.internalCallbackURL, want)
	}
}

func TestLoadConfigProxyProcessModeRejectsMissingURLPrefix(t *testing.T) {
	env := baseSlackEnv()
	env["GC_SERVICE_SOCKET"] = "/tmp/x.sock"
	// GC_SERVICE_URL_PREFIX intentionally missing.
	env["GC_API_BASE_URL"] = "http://127.0.0.1:8372"

	_, err := loadConfigFromEnv(stubEnv(env))
	if err == nil {
		t.Fatal("loadConfigFromEnv: want error when GC_SERVICE_SOCKET set without GC_SERVICE_URL_PREFIX, got nil")
	}
	if !strings.Contains(err.Error(), "GC_SERVICE_URL_PREFIX") {
		t.Errorf("error message = %q, want it to mention GC_SERVICE_URL_PREFIX", err.Error())
	}
}

func TestLoadConfigMissingSlackSecretsReportsAll(t *testing.T) {
	// All three Slack secrets + GC_CITY_NAME missing — should report
	// all four in one error. GC_CITY_NAME has no default because the
	// fallback (silently posting to the wrong city) is worse than
	// fail-fast.
	env := map[string]string{}

	_, err := loadConfigFromEnv(stubEnv(env))
	if err == nil {
		t.Fatal("loadConfigFromEnv: want error for missing slack secrets, got nil")
	}
	for _, key := range []string{
		"SLACK_WORKSPACE_ID", "SLACK_BOT_TOKEN", "SLACK_SIGNING_SECRET", "GC_CITY_NAME",
	} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q missing %s", err.Error(), key)
		}
	}
}

func TestLoadConfigRejectsMissingCityName(t *testing.T) {
	// All Slack secrets present but GC_CITY_NAME unset — adapter must
	// fail-fast rather than silently route inbound traffic to a wrong
	// default city. Regression guard for gc-ywe.2 (removed the
	// "ds-research" fallback).
	env := baseSlackEnv()
	delete(env, "GC_CITY_NAME")

	_, err := loadConfigFromEnv(stubEnv(env))
	if err == nil {
		t.Fatal("loadConfigFromEnv: want error when GC_CITY_NAME is unset, got nil")
	}
	if !strings.Contains(err.Error(), "GC_CITY_NAME") {
		t.Errorf("error %q must mention GC_CITY_NAME", err.Error())
	}
}

func TestHandleReact(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		method        string
		slackResponse string
		wantStatus    int
		wantDelivered bool
		wantFailKind  string
		wantSlackPath string
	}{
		{
			name:          "happy path",
			method:        http.MethodPost,
			body:          `{"conversation":{"conversation_id":"C123"},"message_id":"1234.5678","emoji":"eyes"}`,
			slackResponse: `{"ok":true}`,
			wantStatus:    http.StatusOK,
			wantDelivered: true,
			wantSlackPath: "/reactions.add",
		},
		{
			name:          "strips colons from emoji",
			method:        http.MethodPost,
			body:          `{"conversation":{"conversation_id":"C123"},"message_id":"1.2","emoji":":eyes:"}`,
			slackResponse: `{"ok":true}`,
			wantStatus:    http.StatusOK,
			wantDelivered: true,
			wantSlackPath: "/reactions.add",
		},
		{
			name:          "already_reacted is success",
			method:        http.MethodPost,
			body:          `{"conversation":{"conversation_id":"C123"},"message_id":"1.2","emoji":"eyes"}`,
			slackResponse: `{"ok":false,"error":"already_reacted"}`,
			wantStatus:    http.StatusOK,
			wantDelivered: true,
			wantSlackPath: "/reactions.add",
		},
		{
			name:          "channel_not_found maps to not_found",
			method:        http.MethodPost,
			body:          `{"conversation":{"conversation_id":"C123"},"message_id":"1.2","emoji":"eyes"}`,
			slackResponse: `{"ok":false,"error":"channel_not_found"}`,
			wantStatus:    http.StatusOK,
			wantDelivered: false,
			wantFailKind:  "not_found",
			wantSlackPath: "/reactions.add",
		},
		{
			name:       "GET rejected",
			method:     http.MethodGet,
			body:       "",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "missing emoji rejected",
			method:     http.MethodPost,
			body:       `{"conversation":{"conversation_id":"C123"},"message_id":"1.2"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing message_id rejected",
			method:     http.MethodPost,
			body:       `{"conversation":{"conversation_id":"C123"},"emoji":"eyes"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing channel rejected",
			method:     http.MethodPost,
			body:       `{"message_id":"1.2","emoji":"eyes"}`,
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origBase := slackAPIBase
			t.Cleanup(func() { slackAPIBase = origBase })
			var gotPath string
			fakeSlack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.slackResponse))
			}))
			t.Cleanup(fakeSlack.Close)
			slackAPIBase = fakeSlack.URL

			cfg := config{slackBotToken: "xoxb-test"}
			req := httptest.NewRequest(tc.method, "/react", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handleReact(cfg)(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			if gotPath != tc.wantSlackPath {
				t.Errorf("slack path = %q, want %q", gotPath, tc.wantSlackPath)
			}
			var got reactReceipt
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode receipt: %v (body=%s)", err, rec.Body.String())
			}
			if got.Delivered != tc.wantDelivered {
				t.Errorf("delivered = %v, want %v", got.Delivered, tc.wantDelivered)
			}
			if got.FailureKind != tc.wantFailKind {
				t.Errorf("failure_kind = %q, want %q", got.FailureKind, tc.wantFailKind)
			}
		})
	}
}

func TestIdentityRegistryRoundTrip(t *testing.T) {
	store := filepath.Join(t.TempDir(), "identities.json")
	reg, err := newIdentityRegistry(store)
	if err != nil {
		t.Fatalf("newIdentityRegistry: %v", err)
	}

	// Empty registry: lookup misses cleanly.
	if _, ok := reg.Get("gc-unknown"); ok {
		t.Errorf("Get on empty registry: ok=true, want false")
	}

	// Set then get.
	want := identityRecord{Username: "Gas City PL", IconEmoji: "robot_face"}
	if err := reg.Set("gc-12345", want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok := reg.Get("gc-12345")
	if !ok {
		t.Fatalf("Get after Set: ok=false")
	}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}

	// Reload from disk: persistence works across restarts.
	reg2, err := newIdentityRegistry(store)
	if err != nil {
		t.Fatalf("newIdentityRegistry reload: %v", err)
	}
	got2, ok := reg2.Get("gc-12345")
	if !ok || got2 != want {
		t.Errorf("after reload Get = (%+v, %v), want (%+v, true)", got2, ok, want)
	}

	// Update: overwrite with new record persists.
	updated := identityRecord{Username: "cos", IconURL: "https://example.com/cos.png"}
	if err := reg2.Set("gc-12345", updated); err != nil {
		t.Fatalf("Set update: %v", err)
	}
	reg3, err := newIdentityRegistry(store)
	if err != nil {
		t.Fatalf("newIdentityRegistry reload2: %v", err)
	}
	got3, _ := reg3.Get("gc-12345")
	if got3 != updated {
		t.Errorf("after update reload Get = %+v, want %+v", got3, updated)
	}
}

func TestIdentityRegistryEmptyDiskPath(t *testing.T) {
	// diskPath="" disables persistence — must not error on Set/Get.
	reg, err := newIdentityRegistry("")
	if err != nil {
		t.Fatalf("newIdentityRegistry(\"\"): %v", err)
	}
	if err := reg.Set("gc-1", identityRecord{Username: "x"}); err != nil {
		t.Errorf("Set with empty diskPath: %v", err)
	}
	if _, ok := reg.Get("gc-1"); !ok {
		t.Errorf("Get after Set with empty diskPath: ok=false")
	}
}

func TestHandleIdentity(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		body        string
		wantStatus  int
		wantStored  bool
		wantSession string
	}{
		{
			name:        "happy path full identity",
			method:      http.MethodPost,
			body:        `{"session_id":"gc-abc","username":"PL gascity","icon_emoji":"robot_face"}`,
			wantStatus:  http.StatusOK,
			wantStored:  true,
			wantSession: "gc-abc",
		},
		{
			name:        "username only",
			method:      http.MethodPost,
			body:        `{"session_id":"gc-def","username":"cos"}`,
			wantStatus:  http.StatusOK,
			wantStored:  true,
			wantSession: "gc-def",
		},
		{
			name:        "icon_url only",
			method:      http.MethodPost,
			body:        `{"session_id":"gc-ghi","icon_url":"https://example.com/x.png"}`,
			wantStatus:  http.StatusOK,
			wantStored:  true,
			wantSession: "gc-ghi",
		},
		{
			name:       "GET rejected",
			method:     http.MethodGet,
			body:       "",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "missing session_id rejected",
			method:     http.MethodPost,
			body:       `{"username":"x"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "blank session_id rejected",
			method:     http.MethodPost,
			body:       `{"session_id":"   "}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "garbage body rejected",
			method:     http.MethodPost,
			body:       `not json`,
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := newIdentityRegistry(filepath.Join(t.TempDir(), "id.json"))
			if err != nil {
				t.Fatalf("newIdentityRegistry: %v", err)
			}
			req := httptest.NewRequest(tc.method, "/identity", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handleIdentity(reg)(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var got identityReceipt
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode receipt: %v (body=%s)", err, rec.Body.String())
			}
			if got.Stored != tc.wantStored {
				t.Errorf("stored = %v, want %v", got.Stored, tc.wantStored)
			}
			if got.SessionID != tc.wantSession {
				t.Errorf("session_id = %q, want %q", got.SessionID, tc.wantSession)
			}
			// Verify it actually landed in the registry.
			if _, ok := reg.Get(tc.wantSession); !ok {
				t.Errorf("registry.Get(%q): not found after handleIdentity", tc.wantSession)
			}
		})
	}
}

func TestHandlePublishInjectsIdentity(t *testing.T) {
	cases := []struct {
		name          string
		registerSID   string
		registerRec   identityRecord
		publishBody   string
		wantUsername  string
		wantIconURL   string
		wantIconEmoji string
	}{
		{
			name:          "matched session injects all identity fields",
			registerSID:   "gc-pl-1",
			registerRec:   identityRecord{Username: "Gascity PL", IconEmoji: "robot_face"},
			publishBody:   `{"session_id":"gc-pl-1","conversation":{"conversation_id":"C1","kind":"room"},"text":"hi"}`,
			wantUsername:  "Gascity PL",
			wantIconEmoji: "robot_face",
		},
		{
			name:         "matched session with icon_url",
			registerSID:  "gc-cos",
			registerRec:  identityRecord{Username: "cos", IconURL: "https://example.com/cos.png"},
			publishBody:  `{"session_id":"gc-cos","conversation":{"conversation_id":"C2","kind":"room"},"text":"x"}`,
			wantUsername: "cos",
			wantIconURL:  "https://example.com/cos.png",
		},
		{
			name:        "unknown session id sends no identity overrides",
			registerSID: "gc-other",
			registerRec: identityRecord{Username: "Other"},
			publishBody: `{"session_id":"gc-pl-99","conversation":{"conversation_id":"C3","kind":"room"},"text":"y"}`,
			// All want* zero — no override should be sent.
		},
		{
			name:        "empty session id skips lookup entirely",
			registerSID: "gc-pl-1",
			registerRec: identityRecord{Username: "Gascity PL"},
			publishBody: `{"conversation":{"conversation_id":"C4","kind":"room"},"text":"z"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := newIdentityRegistry(filepath.Join(t.TempDir(), "id.json"))
			if err != nil {
				t.Fatalf("newIdentityRegistry: %v", err)
			}
			if err := reg.Set(tc.registerSID, tc.registerRec); err != nil {
				t.Fatalf("Set: %v", err)
			}

			origBase := slackAPIBase
			t.Cleanup(func() { slackAPIBase = origBase })

			var captured slackPostMessageReq
			fakeSlack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&captured)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"ts":"1.2"}`))
			}))
			t.Cleanup(fakeSlack.Close)
			slackAPIBase = fakeSlack.URL

			cfg := config{slackBotToken: "xoxb-test"}
			req := httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader(tc.publishBody))
			rec := httptest.NewRecorder()
			handlePublish(cfg, reg)(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body.String())
			}
			if captured.Username != tc.wantUsername {
				t.Errorf("slack username = %q, want %q", captured.Username, tc.wantUsername)
			}
			if captured.IconURL != tc.wantIconURL {
				t.Errorf("slack icon_url = %q, want %q", captured.IconURL, tc.wantIconURL)
			}
			if captured.IconEmoji != tc.wantIconEmoji {
				t.Errorf("slack icon_emoji = %q, want %q", captured.IconEmoji, tc.wantIconEmoji)
			}
		})
	}
}

func TestHandlePublishIdentityFallsBackToMetadataSourceSessionID(t *testing.T) {
	// gc forwards session id via PublishRequest.Metadata["source_session_id"]
	// because PublishRequest itself has no SessionID field. The adapter must
	// resolve identity from that metadata key when the explicit SessionID is
	// absent on the wire.
	cases := []struct {
		name         string
		body         string
		wantUsername string
	}{
		{
			name:         "metadata fallback when SessionID empty",
			body:         `{"conversation":{"conversation_id":"C1"},"text":"x","metadata":{"source_session_id":"gc-pl-1"}}`,
			wantUsername: "Gascity PL",
		},
		{
			name:         "explicit SessionID wins over metadata",
			body:         `{"session_id":"gc-pl-1","conversation":{"conversation_id":"C1"},"text":"x","metadata":{"source_session_id":"gc-other"}}`,
			wantUsername: "Gascity PL",
		},
		{
			name:         "no session anywhere posts under default identity",
			body:         `{"conversation":{"conversation_id":"C1"},"text":"x"}`,
			wantUsername: "",
		},
		{
			name:         "metadata with unknown session id has no identity",
			body:         `{"conversation":{"conversation_id":"C1"},"text":"x","metadata":{"source_session_id":"gc-unknown"}}`,
			wantUsername: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := newIdentityRegistry(filepath.Join(t.TempDir(), "id.json"))
			if err != nil {
				t.Fatalf("newIdentityRegistry: %v", err)
			}
			if err := reg.Set("gc-pl-1", identityRecord{Username: "Gascity PL"}); err != nil {
				t.Fatalf("Set: %v", err)
			}

			origBase := slackAPIBase
			t.Cleanup(func() { slackAPIBase = origBase })
			var captured slackPostMessageReq
			fakeSlack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&captured)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true,"ts":"1.2"}`))
			}))
			t.Cleanup(fakeSlack.Close)
			slackAPIBase = fakeSlack.URL

			cfg := config{slackBotToken: "xoxb-test"}
			req := httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handlePublish(cfg, reg)(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body.String())
			}
			if captured.Username != tc.wantUsername {
				t.Errorf("slack username = %q, want %q", captured.Username, tc.wantUsername)
			}
		})
	}
}

func TestParseHandlePrefix(t *testing.T) {
	const prefix = "@oversight."
	cases := []struct {
		name          string
		text          string
		prefix        string
		wantHandle    string
		wantRemainder string
	}{
		{"matched simple", "@oversight.gascity: status?", prefix, "gascity", "status?"},
		{"matched no space after colon", "@oversight.cos:hello", prefix, "cos", "hello"},
		{"matched leading whitespace", "   @oversight.mayor: hi", prefix, "mayor", "hi"},
		{"matched empty body", "@oversight.gascity:", prefix, "gascity", ""},
		{"matched dash in handle", "@oversight.scix-experiments: x", prefix, "scix-experiments", "x"},
		{"matched underscore in handle", "@oversight.code_intel: x", prefix, "code_intel", "x"},
		{"no prefix passes through", "regular text", prefix, "", "regular text"},
		{"prefix not at start passes through", "hi @oversight.gascity: x", prefix, "", "hi @oversight.gascity: x"},
		{"empty handle rejected", "@oversight.: foo", prefix, "", "@oversight.: foo"},
		{"whitespace separator accepted", "@oversight.gascity status", prefix, "gascity", "status"},
		{"invalid char in handle rejected", "@oversight.bad/handle: x", prefix, "", "@oversight.bad/handle: x"},
		{"space terminates handle then rest is body", "@oversight.bad handle: x", prefix, "bad", "handle: x"},
		{"handle with no body", "@oversight.cos", prefix, "cos", ""},
		{"bare-at prefix with whitespace separator", "@cos parser test", "@", "cos", "parser test"},
		{"bare-at prefix with colon", "@cos: parser test", "@", "cos", "parser test"},
		{"bare-at prefix with newline separator", "@cos\nfoo", "@", "cos", "foo"},
		{"bare-at handle alone", "@mayor", "@", "mayor", ""},
		{"bare-at handle followed by punctuation", "@cos.foo", "@", "", "@cos.foo"},
		{"empty prefix disables", "@oversight.gascity: x", "", "", "@oversight.gascity: x"},
		{"empty text", "", prefix, "", ""},
		{"just whitespace", "   ", prefix, "", "   "},
		{"alternate prefix", "@gc.zelda: art", "@gc.", "zelda", "art"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHandle, gotRemainder := parseHandlePrefix(tc.text, tc.prefix)
			if gotHandle != tc.wantHandle {
				t.Errorf("handle = %q, want %q", gotHandle, tc.wantHandle)
			}
			if gotRemainder != tc.wantRemainder {
				t.Errorf("remainder = %q, want %q", gotRemainder, tc.wantRemainder)
			}
		})
	}
}

func TestHandleAliasRegistryRoundTrip(t *testing.T) {
	store := filepath.Join(t.TempDir(), "aliases.json")
	reg, err := newHandleAliasRegistry(store)
	if err != nil {
		t.Fatalf("newHandleAliasRegistry: %v", err)
	}

	if _, ok := reg.Get("mayor"); ok {
		t.Errorf("Get on empty registry: ok=true, want false")
	}

	if err := reg.Set("mayor", "gc-2568"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := reg.Set("cos", "gc-83347"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok := reg.Get("mayor")
	if !ok || got != "gc-2568" {
		t.Errorf("Get(mayor) = (%q, %v), want (gc-2568, true)", got, ok)
	}

	// Reload from disk.
	reg2, err := newHandleAliasRegistry(store)
	if err != nil {
		t.Fatalf("newHandleAliasRegistry reload: %v", err)
	}
	got2, ok := reg2.Get("cos")
	if !ok || got2 != "gc-83347" {
		t.Errorf("after reload Get(cos) = (%q, %v), want (gc-83347, true)", got2, ok)
	}

	// Empty session_id removes the entry.
	if err := reg2.Set("mayor", ""); err != nil {
		t.Fatalf("Set empty: %v", err)
	}
	if _, ok := reg2.Get("mayor"); ok {
		t.Errorf("Get(mayor) after Set empty: ok=true, want false")
	}
	reg3, err := newHandleAliasRegistry(store)
	if err != nil {
		t.Fatalf("newHandleAliasRegistry reload after delete: %v", err)
	}
	if _, ok := reg3.Get("mayor"); ok {
		t.Errorf("Get(mayor) after delete + reload: ok=true, want false")
	}
}

func TestHandleHandleAlias(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		body        string
		wantStatus  int
		wantStored  bool
		wantRemoved bool
		wantHandle  string
	}{
		{
			name:       "store mayor",
			method:     http.MethodPost,
			body:       `{"handle":"mayor","session_id":"gc-2568"}`,
			wantStatus: http.StatusOK,
			wantStored: true,
			wantHandle: "mayor",
		},
		{
			name:        "remove with empty session_id",
			method:      http.MethodPost,
			body:        `{"handle":"mayor","session_id":""}`,
			wantStatus:  http.StatusOK,
			wantRemoved: true,
			wantHandle:  "mayor",
		},
		{
			name:       "GET rejected",
			method:     http.MethodGet,
			body:       "",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "missing handle rejected",
			method:     http.MethodPost,
			body:       `{"session_id":"gc-2568"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "blank handle rejected",
			method:     http.MethodPost,
			body:       `{"handle":"   ","session_id":"gc-2568"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "garbage body rejected",
			method:     http.MethodPost,
			body:       `not json`,
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := newHandleAliasRegistry(filepath.Join(t.TempDir(), "aliases.json"))
			if err != nil {
				t.Fatalf("newHandleAliasRegistry: %v", err)
			}
			req := httptest.NewRequest(tc.method, "/handle-alias", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handleHandleAlias(reg)(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var got handleAliasReceipt
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode receipt: %v (body=%s)", err, rec.Body.String())
			}
			if got.Stored != tc.wantStored {
				t.Errorf("stored = %v, want %v", got.Stored, tc.wantStored)
			}
			if got.Removed != tc.wantRemoved {
				t.Errorf("removed = %v, want %v", got.Removed, tc.wantRemoved)
			}
			if got.Handle != tc.wantHandle {
				t.Errorf("handle = %q, want %q", got.Handle, tc.wantHandle)
			}
		})
	}
}

func TestDispatchToAliasedSession(t *testing.T) {
	// Verify the adapter POSTs a system-reminder-shaped message to the
	// gc session-message endpoint at the right URL with the right body.
	var gotPath string
	var gotBody gcSessionMessageRequest
	gcStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(gcStub.Close)

	cfg := config{gcAPIBase: gcStub.URL, cityName: "ds-research"}
	inbound := externalInboundMessage{
		ProviderMessageID: "1234.5678",
		Conversation: conversationRef{
			ConversationID: "C0B1NSK4N3T",
		},
		Actor: externalActor{ID: "U0B1N5KD6HF"},
		Text:  "hi mayor please ack the deploy",
	}
	dispatchToAliasedSession(cfg, "gc-2568", inbound, "mayor")

	wantPath := "/v0/city/ds-research/session/gc-2568/messages"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	for _, want := range []string{
		"<system-reminder>",
		"@mayor",
		"channel C0B1NSK4N3T",
		"Slack ts 1234.5678",
		"hi mayor please ack the deploy",
		"--conversation-id C0B1NSK4N3T",
		"--thread-ts 1234.5678",
		"gc slack publish-to-channel",
	} {
		if !strings.Contains(gotBody.Message, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, gotBody.Message)
		}
	}
}

func TestIdentityRegistryDelete(t *testing.T) {
	store := filepath.Join(t.TempDir(), "identities.json")
	reg, err := newIdentityRegistry(store)
	if err != nil {
		t.Fatalf("newIdentityRegistry: %v", err)
	}
	rec := identityRecord{Username: "Test", IconEmoji: "robot_face"}
	if err := reg.Set("gc-1", rec); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Delete existing entry: existed=true, no error.
	existed, err := reg.Delete("gc-1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !existed {
		t.Errorf("Delete existing: existed=false, want true")
	}
	if _, ok := reg.Get("gc-1"); ok {
		t.Errorf("Get after Delete: ok=true, want false")
	}

	// Idempotent: deleting missing entry succeeds with existed=false.
	existed, err = reg.Delete("gc-1")
	if err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if existed {
		t.Errorf("second Delete: existed=true, want false (already removed)")
	}

	// Persistence: reload from disk after delete preserves the deletion.
	reg2, err := newIdentityRegistry(store)
	if err != nil {
		t.Fatalf("newIdentityRegistry reload: %v", err)
	}
	if _, ok := reg2.Get("gc-1"); ok {
		t.Errorf("after reload Get: ok=true, want false (deletion not persisted)")
	}
}

func TestHandleAliasRegistryDelete(t *testing.T) {
	store := filepath.Join(t.TempDir(), "aliases.json")
	reg, err := newHandleAliasRegistry(store)
	if err != nil {
		t.Fatalf("newHandleAliasRegistry: %v", err)
	}
	if err := reg.Set("mayor", "gc-2568"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	existed, err := reg.Delete("mayor")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !existed {
		t.Errorf("Delete existing: existed=false, want true")
	}
	if _, ok := reg.Get("mayor"); ok {
		t.Errorf("Get after Delete: ok=true, want false")
	}

	// Idempotent.
	existed, err = reg.Delete("mayor")
	if err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if existed {
		t.Errorf("second Delete: existed=true, want false")
	}
}

func TestHandleIdentityDelete(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		preSeed    string
		wantStatus int
		wantExist  bool
		wantSID    string
	}{
		{
			name:       "delete via query param removes existing",
			method:     http.MethodDelete,
			path:       "/identity?session_id=gc-abc",
			preSeed:    "gc-abc",
			wantStatus: http.StatusOK,
			wantExist:  true,
			wantSID:    "gc-abc",
		},
		{
			name:       "delete via JSON body removes existing",
			method:     http.MethodDelete,
			path:       "/identity",
			body:       `{"session_id":"gc-def"}`,
			preSeed:    "gc-def",
			wantStatus: http.StatusOK,
			wantExist:  true,
			wantSID:    "gc-def",
		},
		{
			name:       "delete missing session is idempotent",
			method:     http.MethodDelete,
			path:       "/identity?session_id=gc-missing",
			wantStatus: http.StatusOK,
			wantExist:  false,
			wantSID:    "gc-missing",
		},
		{
			name:       "missing session id rejected",
			method:     http.MethodDelete,
			path:       "/identity",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "blank session id rejected",
			method:     http.MethodDelete,
			path:       "/identity?session_id=%20%20",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "POST rejected on delete handler",
			method:     http.MethodPost,
			path:       "/identity?session_id=gc-x",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := newIdentityRegistry(filepath.Join(t.TempDir(), "id.json"))
			if err != nil {
				t.Fatalf("newIdentityRegistry: %v", err)
			}
			if tc.preSeed != "" {
				if err := reg.Set(tc.preSeed, identityRecord{Username: "x"}); err != nil {
					t.Fatalf("seed Set: %v", err)
				}
			}
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handleIdentityDelete(reg)(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var got identityDeleteReceipt
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode receipt: %v (body=%s)", err, rec.Body.String())
			}
			if !got.Removed {
				t.Errorf("removed = false, want true")
			}
			if got.Existed != tc.wantExist {
				t.Errorf("existed = %v, want %v", got.Existed, tc.wantExist)
			}
			if got.SessionID != tc.wantSID {
				t.Errorf("session_id = %q, want %q", got.SessionID, tc.wantSID)
			}
			// Round-trip check: entry is gone from registry regardless.
			if _, ok := reg.Get(tc.wantSID); ok {
				t.Errorf("registry.Get(%q) after delete: ok=true, want false", tc.wantSID)
			}
		})
	}
}

func TestHandleHandleAliasDelete(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		preSeed    string
		wantStatus int
		wantExist  bool
		wantHandle string
	}{
		{
			name:       "delete via query param removes existing",
			method:     http.MethodDelete,
			path:       "/handle-alias?handle=mayor",
			preSeed:    "mayor",
			wantStatus: http.StatusOK,
			wantExist:  true,
			wantHandle: "mayor",
		},
		{
			name:       "delete via JSON body removes existing",
			method:     http.MethodDelete,
			path:       "/handle-alias",
			body:       `{"handle":"cos"}`,
			preSeed:    "cos",
			wantStatus: http.StatusOK,
			wantExist:  true,
			wantHandle: "cos",
		},
		{
			name:       "delete missing handle is idempotent",
			method:     http.MethodDelete,
			path:       "/handle-alias?handle=ghost",
			wantStatus: http.StatusOK,
			wantExist:  false,
			wantHandle: "ghost",
		},
		{
			name:       "missing handle rejected",
			method:     http.MethodDelete,
			path:       "/handle-alias",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "POST rejected on delete handler",
			method:     http.MethodPost,
			path:       "/handle-alias?handle=mayor",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := newHandleAliasRegistry(filepath.Join(t.TempDir(), "aliases.json"))
			if err != nil {
				t.Fatalf("newHandleAliasRegistry: %v", err)
			}
			if tc.preSeed != "" {
				if err := reg.Set(tc.preSeed, "gc-2568"); err != nil {
					t.Fatalf("seed Set: %v", err)
				}
			}
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handleHandleAliasDelete(reg)(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var got handleAliasDeleteReceipt
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode receipt: %v (body=%s)", err, rec.Body.String())
			}
			if !got.Removed {
				t.Errorf("removed = false, want true")
			}
			if got.Existed != tc.wantExist {
				t.Errorf("existed = %v, want %v", got.Existed, tc.wantExist)
			}
			if got.Handle != tc.wantHandle {
				t.Errorf("handle = %q, want %q", got.Handle, tc.wantHandle)
			}
			if _, ok := reg.Get(tc.wantHandle); ok {
				t.Errorf("registry.Get(%q) after delete: ok=true, want false", tc.wantHandle)
			}
		})
	}
}

// fakeSlackFiles emulates the three-step Slack files-upload-v2 protocol
// for handlePublishFile tests. Each tracker captures the most recent
// request; per-step error injection lets cases exercise failure modes.
type fakeSlackFiles struct {
	server           *httptest.Server
	uploadServer     *httptest.Server
	getURLPath       string
	getURLForm       string
	completePath     string
	completeBody     slackCompleteUploadReq
	uploadedBytes    []byte
	uploadedFilename string
	getURLResp       string
	completeResp     string
	uploadStatus     int
}

func newFakeSlackFiles(t *testing.T) *fakeSlackFiles {
	t.Helper()
	f := &fakeSlackFiles{
		getURLResp:   `{"ok":true,"upload_url":"PLACEHOLDER","file_id":"F123"}`,
		completeResp: `{"ok":true,"files":[{"id":"F123"}]}`,
		uploadStatus: http.StatusOK,
	}
	// Pre-signed upload URL emulator: parses the multipart POST that
	// slackPutFileBytes sends and stashes just the file content for the
	// assertion. Slack accepts only multipart-with-`filename` field; raw
	// PUT silently produces an unshareable ghost file (see comment on
	// slackPutFileBytes for the bug history).
	f.uploadServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			f.uploadedBytes = nil
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fhs := r.MultipartForm.File["filename"]
		if len(fhs) == 0 {
			f.uploadedBytes = nil
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fh := fhs[0]
		f.uploadedFilename = fh.Filename
		ff, err := fh.Open()
		if err != nil {
			f.uploadedBytes = nil
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer ff.Close()
		body, _ := io.ReadAll(ff)
		f.uploadedBytes = body
		w.WriteHeader(f.uploadStatus)
	}))
	t.Cleanup(f.uploadServer.Close)
	// Slack API emulator: routes /files.getUploadURLExternal and
	// /files.completeUploadExternal to the trackers above.
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files.getUploadURLExternal":
			f.getURLPath = r.URL.Path
			body, _ := io.ReadAll(r.Body)
			f.getURLForm = string(body)
			w.Header().Set("Content-Type", "application/json")
			// Substitute the real upload URL into the response so the
			// adapter PUTs to the test fixture.
			resp := strings.ReplaceAll(f.getURLResp, "PLACEHOLDER", f.uploadServer.URL+"/upload")
			_, _ = w.Write([]byte(resp))
		case "/files.completeUploadExternal":
			f.completePath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&f.completeBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(f.completeResp))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func TestHandlePublishFile(t *testing.T) {
	cases := []struct {
		name             string
		body             string
		method           string
		seedFile         bool
		fileContent      string
		filePathOverride string
		getURLResp       string
		completeResp     string
		uploadStatus     int
		wantStatus       int
		wantDelivered    bool
		wantFailKind     string
		wantFileID       string
		wantChannel      string
		wantThreadTS     string
		wantInitial      string
		wantUploadBody   string
	}{
		{
			name:           "happy path with thread + initial comment",
			method:         http.MethodPost,
			seedFile:       true,
			fileContent:    "PNGDATA-12345",
			body:           `{"conversation":{"conversation_id":"C123","kind":"room"},"file_path":"PLACEHOLDER","filename":"plot.png","initial_comment":"latest run","reply_to_message_id":"1234.5678"}`,
			wantStatus:     http.StatusOK,
			wantDelivered:  true,
			wantFileID:     "F123",
			wantChannel:    "C123",
			wantThreadTS:   "1234.5678",
			wantInitial:    "latest run",
			wantUploadBody: "PNGDATA-12345",
		},
		{
			name:       "missing file_path rejected",
			method:     http.MethodPost,
			body:       `{"conversation":{"conversation_id":"C1"}}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:        "missing channel rejected",
			method:      http.MethodPost,
			seedFile:    true,
			fileContent: "x",
			body:        `{"file_path":"PLACEHOLDER"}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:       "nonexistent file rejected",
			method:     http.MethodPost,
			body:       `{"conversation":{"conversation_id":"C1"},"file_path":"/tmp/definitely-not-here-12345.png"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "GET rejected",
			method:     http.MethodGet,
			body:       "",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "garbage JSON rejected",
			method:     http.MethodPost,
			body:       `not-json`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:          "missing_scope on getUploadURL maps to auth",
			method:        http.MethodPost,
			seedFile:      true,
			fileContent:   "x",
			body:          `{"conversation":{"conversation_id":"C1"},"file_path":"PLACEHOLDER","filename":"f.bin"}`,
			getURLResp:    `{"ok":false,"error":"missing_scope"}`,
			wantStatus:    http.StatusOK,
			wantDelivered: false,
			wantFailKind:  "auth",
		},
		{
			name:          "rate_limited on getUploadURL maps to rate_limited",
			method:        http.MethodPost,
			seedFile:      true,
			fileContent:   "x",
			body:          `{"conversation":{"conversation_id":"C1"},"file_path":"PLACEHOLDER"}`,
			getURLResp:    `{"ok":false,"error":"ratelimited"}`,
			wantStatus:    http.StatusOK,
			wantDelivered: false,
			wantFailKind:  "rate_limited",
		},
		{
			name:          "channel_not_found on complete maps to not_found",
			method:        http.MethodPost,
			seedFile:      true,
			fileContent:   "x",
			body:          `{"conversation":{"conversation_id":"C-nope"},"file_path":"PLACEHOLDER"}`,
			completeResp:  `{"ok":false,"error":"channel_not_found"}`,
			wantStatus:    http.StatusOK,
			wantDelivered: false,
			wantFailKind:  "not_found",
		},
		{
			name:          "POST 5xx maps to transient",
			method:        http.MethodPost,
			seedFile:      true,
			fileContent:   "x",
			body:          `{"conversation":{"conversation_id":"C1"},"file_path":"PLACEHOLDER"}`,
			uploadStatus:  http.StatusInternalServerError,
			wantStatus:    http.StatusOK,
			wantDelivered: false,
			wantFailKind:  "transient",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origBase := slackAPIBase
			t.Cleanup(func() { slackAPIBase = origBase })

			fake := newFakeSlackFiles(t)
			if tc.getURLResp != "" {
				fake.getURLResp = tc.getURLResp
			}
			if tc.completeResp != "" {
				fake.completeResp = tc.completeResp
			}
			if tc.uploadStatus != 0 {
				fake.uploadStatus = tc.uploadStatus
			}
			slackAPIBase = fake.server.URL

			body := tc.body
			if tc.seedFile {
				path := filepath.Join(t.TempDir(), "in.bin")
				if err := os.WriteFile(path, []byte(tc.fileContent), 0o600); err != nil {
					t.Fatalf("seed file: %v", err)
				}
				body = strings.ReplaceAll(body, "PLACEHOLDER", path)
			}

			reg, err := newIdentityRegistry(filepath.Join(t.TempDir(), "id.json"))
			if err != nil {
				t.Fatalf("newIdentityRegistry: %v", err)
			}

			cfg := config{slackBotToken: "xoxb-test"}
			req := httptest.NewRequest(tc.method, "/publish-file", strings.NewReader(body))
			rec := httptest.NewRecorder()
			handlePublishFile(cfg, reg)(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var got publishFileReceipt
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode receipt: %v (body=%s)", err, rec.Body.String())
			}
			if got.Delivered != tc.wantDelivered {
				t.Errorf("delivered = %v, want %v (failure_kind=%q error=%q)",
					got.Delivered, tc.wantDelivered, got.FailureKind, got.Error)
			}
			if got.FailureKind != tc.wantFailKind {
				t.Errorf("failure_kind = %q, want %q", got.FailureKind, tc.wantFailKind)
			}
			if !tc.wantDelivered {
				return
			}
			if got.FileID != tc.wantFileID {
				t.Errorf("file_id = %q, want %q", got.FileID, tc.wantFileID)
			}
			if fake.completeBody.ChannelID != tc.wantChannel {
				t.Errorf("complete.channel_id = %q, want %q", fake.completeBody.ChannelID, tc.wantChannel)
			}
			if fake.completeBody.ThreadTS != tc.wantThreadTS {
				t.Errorf("complete.thread_ts = %q, want %q", fake.completeBody.ThreadTS, tc.wantThreadTS)
			}
			if fake.completeBody.InitialComment != tc.wantInitial {
				t.Errorf("complete.initial_comment = %q, want %q", fake.completeBody.InitialComment, tc.wantInitial)
			}
			if string(fake.uploadedBytes) != tc.wantUploadBody {
				t.Errorf("upload body = %q, want %q", string(fake.uploadedBytes), tc.wantUploadBody)
			}
			if !strings.Contains(fake.getURLForm, "filename=") {
				t.Errorf("getUploadURL form missing filename: %q", fake.getURLForm)
			}
		})
	}
}

func TestSafeFilename(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain.png", "plain.png"},
		{"with space.png", "with space.png"},
		{"../../etc/passwd", "_._.._etc_passwd"},
		{"a/b/c.txt", "a_b_c.txt"},
		{"\\windows\\path.txt", "_windows_path.txt"},
		{"", "file"},
		{"  ", "file"},
		{".hidden", "_hidden"},
		{"...dotty", "_..dotty"},
		{"with\x00null.bin", "with_null.bin"},
		{"with\nnewline.bin", "with_newline.bin"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := safeFilename(tc.in)
			if got != tc.want {
				t.Errorf("safeFilename(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	// Length cap: input >200 chars truncates to 200.
	long := strings.Repeat("a", 300)
	got := safeFilename(long)
	if len(got) != 200 {
		t.Errorf("long filename: len = %d, want 200", len(got))
	}
}

func TestSafePathComponent(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Real-world Slack identifiers pass through unchanged.
		{"C0B13JE7M35", "C0B13JE7M35"},
		{"1234567890.123456", "1234567890.123456"},
		{"abc-def_ghi.123", "abc-def_ghi.123"},

		// Path traversal attempts — separators replaced.
		{"../etc", "_._etc"},
		{"/abs/path", "_abs_path"},
		{"\\windows\\path", "_windows_path"},

		// NUL + control chars + whitespace + non-ASCII all replaced.
		{"with\x00null", "with_null"},
		{"with\nnewline", "with_newline"},
		{"with space", "with_space"},
		{"unicode-é", "unicode-_"},

		// Other non-allowlist punctuation replaced.
		{"hash#tag", "hash_tag"},

		// Leading-dot scrub (defense against `.` and `..` parents).
		{".hidden", "_hidden"},
		{"...trip", "_..trip"},

		// Empty / whitespace-only fall back to "_".
		{"", "_"},
		{"   ", "___"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := safePathComponent(tc.in)
			if got != tc.want {
				t.Errorf("safePathComponent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// Length cap: input >64 chars truncates to 64.
	long := strings.Repeat("a", 200)
	if got := safePathComponent(long); len(got) != 64 {
		t.Errorf("long input: len = %d, want 64", len(got))
	}

	// Result never contains a path separator or NUL, regardless of input.
	hostile := "/" + strings.Repeat("../", 30) + "\x00\n\\"
	got := safePathComponent(hostile)
	if strings.ContainsAny(got, "/\\\x00") {
		t.Errorf("safePathComponent kept a separator or NUL: %q", got)
	}
}

func TestDownloadSlackFiles(t *testing.T) {
	cases := []struct {
		name       string
		files      []slackFile
		fileBodies map[string]string // url_private path -> body returned by stub
		fileStatus map[string]int    // url_private path -> HTTP status
		emptyStore bool
		channel    string // override default "C123" — used by malformed-id case
		ts         string // override default "1234.5678" — used by malformed-id case
		wantCount  int
		wantBodies []string
	}{
		{
			name: "single file downloaded",
			files: []slackFile{{
				ID:         "F1",
				Name:       "plot.png",
				URLPrivate: "PLACEHOLDER/files/F1",
				MIMEType:   "image/png",
			}},
			fileBodies: map[string]string{"/files/F1": "PNG-BYTES"},
			wantCount:  1,
			wantBodies: []string{"PNG-BYTES"},
		},
		{
			name: "two files",
			files: []slackFile{
				{ID: "F1", Name: "a.txt", URLPrivate: "PLACEHOLDER/files/F1"},
				{ID: "F2", Name: "b.txt", URLPrivate: "PLACEHOLDER/files/F2"},
			},
			fileBodies: map[string]string{"/files/F1": "AAA", "/files/F2": "BBB"},
			wantCount:  2,
			wantBodies: []string{"AAA", "BBB"},
		},
		{
			name:      "no files returns nil",
			files:     nil,
			wantCount: 0,
		},
		{
			name: "missing url_private dropped",
			files: []slackFile{
				{ID: "F1", Name: "ok.txt", URLPrivate: "PLACEHOLDER/files/F1"},
				{ID: "F2", Name: "noupload.txt"}, // no URLPrivate
			},
			fileBodies: map[string]string{"/files/F1": "GOOD"},
			wantCount:  1,
			wantBodies: []string{"GOOD"},
		},
		{
			name: "404 from slack drops file but other succeeds",
			files: []slackFile{
				{ID: "F1", Name: "good.txt", URLPrivate: "PLACEHOLDER/files/F1"},
				{ID: "F2", Name: "bad.txt", URLPrivate: "PLACEHOLDER/files/F2"},
			},
			fileBodies: map[string]string{"/files/F1": "GOOD", "/files/F2": ""},
			fileStatus: map[string]int{"/files/F2": http.StatusNotFound},
			wantCount:  1,
			wantBodies: []string{"GOOD"},
		},
		{
			name:       "empty store path returns nil",
			files:      []slackFile{{ID: "F1", URLPrivate: "PLACEHOLDER/files/F1"}},
			emptyStore: true,
			wantCount:  0,
		},
		{
			name: "path traversal in name sanitized",
			files: []slackFile{{
				ID:         "F1",
				Name:       "../../escape.png",
				URLPrivate: "PLACEHOLDER/files/F1",
			}},
			fileBodies: map[string]string{"/files/F1": "X"},
			wantCount:  1,
			wantBodies: []string{"X"},
		},
		{
			// Defense-in-depth: even if SLACK_SIGNING_SECRET leaks and an
			// attacker forges a Slack event with hostile channel/ts, the
			// resulting filesystem write must stay under inboundFileStore.
			name: "malformed channel and ts sanitized",
			files: []slackFile{{
				ID:         "F1",
				Name:       "ok.png",
				URLPrivate: "PLACEHOLDER/files/F1",
			}},
			fileBodies: map[string]string{"/files/F1": "Y"},
			channel:    "../../etc",
			ts:         "../boom",
			wantCount:  1,
			wantBodies: []string{"Y"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slackStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, ok := tc.fileBodies[r.URL.Path]
				if !ok {
					http.NotFound(w, r)
					return
				}
				if status, has := tc.fileStatus[r.URL.Path]; has && status >= 400 {
					http.Error(w, "boom", status)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(slackStub.Close)

			files := make([]slackFile, len(tc.files))
			for i, f := range tc.files {
				files[i] = f
				if f.URLPrivate != "" {
					files[i].URLPrivate = strings.ReplaceAll(f.URLPrivate, "PLACEHOLDER", slackStub.URL)
				}
			}

			cfg := config{
				slackBotToken:    "xoxb-test",
				inboundFileStore: filepath.Join(t.TempDir(), "inbound"),
			}
			if tc.emptyStore {
				cfg.inboundFileStore = ""
			}

			channel := tc.channel
			if channel == "" {
				channel = "C123"
			}
			ts := tc.ts
			if ts == "" {
				ts = "1234.5678"
			}
			got := downloadSlackFiles(cfg, channel, ts, files)
			if len(got) != tc.wantCount {
				t.Fatalf("got %d attachments, want %d (%+v)", len(got), tc.wantCount, got)
			}
			// File must live under inboundFileStore/<sanitized-channel>/, not
			// escape via path traversal in channel or ts. Use EvalSymlinks
			// so a hostile symlink can't defeat the prefix check by yielding
			// a path that lexically lives under the store but resolves
			// elsewhere on the filesystem.
			realStore, err := filepath.EvalSymlinks(cfg.inboundFileStore)
			if err != nil {
				t.Fatalf("evalSymlinks(inboundFileStore): %v", err)
			}
			for i, att := range got {
				if !strings.HasPrefix(att.URL, "file://") {
					t.Errorf("attachment[%d].url = %q, want file:// prefix", i, att.URL)
				}
				path := strings.TrimPrefix(att.URL, "file://")
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read attachment[%d]: %v", i, err)
				}
				if string(body) != tc.wantBodies[i] {
					t.Errorf("attachment[%d] body = %q, want %q", i, string(body), tc.wantBodies[i])
				}
				realPath, err := filepath.EvalSymlinks(path)
				if err != nil {
					t.Fatalf("evalSymlinks(%s): %v", path, err)
				}
				if !strings.HasPrefix(realPath, realStore+string(filepath.Separator)) {
					t.Errorf("attachment[%d] path %q escapes store dir %q", i, realPath, realStore)
				}
			}
		})
	}
}

// writeAged creates a file at path with the given content and mtime.
// Used by sweep tests to seed inbound store fixtures with controlled
// ages without sleeping.
func writeAged(t *testing.T, path string, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func TestSweepInboundStore(t *testing.T) {
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	ttl := 24 * time.Hour
	old := now.Add(-48 * time.Hour)  // older than ttl, should be removed
	fresh := now.Add(-1 * time.Hour) // within ttl, should be kept

	t.Run("missing root is no-op", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "does-not-exist")
		res := sweepInboundStore(root, ttl, now)
		if res.FilesRemoved != 0 || res.DirsRemoved != 0 || len(res.Errors) != 0 {
			t.Fatalf("expected zero result for missing root, got %+v", res)
		}
	})

	t.Run("empty root is no-op", func(t *testing.T) {
		root := t.TempDir()
		res := sweepInboundStore(root, ttl, now)
		if res.FilesRemoved != 0 || res.DirsRemoved != 0 || len(res.Errors) != 0 {
			t.Fatalf("expected zero result for empty root, got %+v", res)
		}
	})

	t.Run("removes old files keeps fresh", func(t *testing.T) {
		root := t.TempDir()
		writeAged(t, filepath.Join(root, "C123", "1700000000.000-old.png"), "OLD", old)
		writeAged(t, filepath.Join(root, "C123", "1700000001.000-fresh.png"), "FRESH", fresh)

		res := sweepInboundStore(root, ttl, now)
		if res.FilesRemoved != 1 {
			t.Errorf("FilesRemoved = %d, want 1", res.FilesRemoved)
		}
		if res.DirsRemoved != 0 {
			t.Errorf("DirsRemoved = %d, want 0 (channel not empty)", res.DirsRemoved)
		}
		if res.BytesRemoved != int64(len("OLD")) {
			t.Errorf("BytesRemoved = %d, want %d", res.BytesRemoved, len("OLD"))
		}
		if len(res.Errors) != 0 {
			t.Errorf("unexpected errors: %v", res.Errors)
		}
		if _, err := os.Stat(filepath.Join(root, "C123", "1700000000.000-old.png")); !os.IsNotExist(err) {
			t.Error("old file should have been removed")
		}
		if _, err := os.Stat(filepath.Join(root, "C123", "1700000001.000-fresh.png")); err != nil {
			t.Errorf("fresh file should remain: %v", err)
		}
	})

	t.Run("removes empty channel dir after sweep", func(t *testing.T) {
		root := t.TempDir()
		writeAged(t, filepath.Join(root, "C123", "1700000000.000-only.png"), "OLD", old)

		res := sweepInboundStore(root, ttl, now)
		if res.FilesRemoved != 1 {
			t.Errorf("FilesRemoved = %d, want 1", res.FilesRemoved)
		}
		if res.DirsRemoved != 1 {
			t.Errorf("DirsRemoved = %d, want 1", res.DirsRemoved)
		}
		if _, err := os.Stat(filepath.Join(root, "C123")); !os.IsNotExist(err) {
			t.Error("empty channel dir should have been removed")
		}
	})

	t.Run("multiple channels processed independently", func(t *testing.T) {
		root := t.TempDir()
		writeAged(t, filepath.Join(root, "C123", "old.png"), "OLD", old)
		writeAged(t, filepath.Join(root, "C456", "fresh.png"), "FRESH", fresh)

		res := sweepInboundStore(root, ttl, now)
		if res.FilesRemoved != 1 {
			t.Errorf("FilesRemoved = %d, want 1", res.FilesRemoved)
		}
		if res.DirsRemoved != 1 {
			t.Errorf("DirsRemoved = %d, want 1 (only C123 became empty)", res.DirsRemoved)
		}
		if _, err := os.Stat(filepath.Join(root, "C123")); !os.IsNotExist(err) {
			t.Error("C123 should have been removed")
		}
		if _, err := os.Stat(filepath.Join(root, "C456", "fresh.png")); err != nil {
			t.Errorf("C456/fresh should remain: %v", err)
		}
	})

	t.Run("non-positive ttl disables", func(t *testing.T) {
		root := t.TempDir()
		writeAged(t, filepath.Join(root, "C123", "old.png"), "OLD", old)

		res := sweepInboundStore(root, 0, now)
		if res.FilesRemoved != 0 {
			t.Errorf("FilesRemoved = %d, want 0 (ttl=0 disables)", res.FilesRemoved)
		}
		if _, err := os.Stat(filepath.Join(root, "C123", "old.png")); err != nil {
			t.Errorf("file should remain when ttl disabled: %v", err)
		}
	})

	t.Run("empty root path disables", func(t *testing.T) {
		res := sweepInboundStore("", ttl, now)
		if res.FilesRemoved != 0 || len(res.Errors) != 0 {
			t.Fatalf("expected zero result for empty root, got %+v", res)
		}
	})

	t.Run("files at root level skipped", func(t *testing.T) {
		root := t.TempDir()
		// A file directly at the store root (not under a channel dir).
		// The janitor should leave it alone — only <root>/<channel>/* is
		// in scope.
		writeAged(t, filepath.Join(root, "stray.txt"), "STRAY", old)

		res := sweepInboundStore(root, ttl, now)
		if res.FilesRemoved != 0 {
			t.Errorf("FilesRemoved = %d, want 0 (root-level files not swept)", res.FilesRemoved)
		}
		if _, err := os.Stat(filepath.Join(root, "stray.txt")); err != nil {
			t.Errorf("root-level file should remain: %v", err)
		}
	})

	t.Run("non-regular files skipped", func(t *testing.T) {
		root := t.TempDir()
		channelDir := filepath.Join(root, "C123")
		if err := os.MkdirAll(channelDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		// Create a nested directory inside the channel dir — should be
		// ignored by the file-pass and not removed.
		nested := filepath.Join(channelDir, "nested")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("mkdir nested: %v", err)
		}
		writeAged(t, filepath.Join(channelDir, "old.png"), "OLD", old)

		res := sweepInboundStore(root, ttl, now)
		if res.FilesRemoved != 1 {
			t.Errorf("FilesRemoved = %d, want 1", res.FilesRemoved)
		}
		// Channel dir is not empty (still contains `nested/`), so don't remove.
		if res.DirsRemoved != 0 {
			t.Errorf("DirsRemoved = %d, want 0 (channel still has nested dir)", res.DirsRemoved)
		}
		if _, err := os.Stat(nested); err != nil {
			t.Errorf("nested dir should remain: %v", err)
		}
	})
}

func TestLoadConfigInboundFileRetentionDefaults(t *testing.T) {
	cfg, err := loadConfigFromEnv(stubEnv(baseSlackEnv()))
	if err != nil {
		t.Fatalf("loadConfigFromEnv: %v", err)
	}
	if cfg.inboundFileTTL != 168*time.Hour {
		t.Errorf("inboundFileTTL = %s, want 168h", cfg.inboundFileTTL)
	}
	if cfg.inboundFileSweepInterval != 1*time.Hour {
		t.Errorf("inboundFileSweepInterval = %s, want 1h", cfg.inboundFileSweepInterval)
	}
}

func TestLoadConfigInboundFileRetentionOverrides(t *testing.T) {
	env := baseSlackEnv()
	env["INBOUND_FILE_TTL"] = "30m"
	env["INBOUND_FILE_SWEEP_INTERVAL"] = "5m"
	cfg, err := loadConfigFromEnv(stubEnv(env))
	if err != nil {
		t.Fatalf("loadConfigFromEnv: %v", err)
	}
	if cfg.inboundFileTTL != 30*time.Minute {
		t.Errorf("inboundFileTTL = %s, want 30m", cfg.inboundFileTTL)
	}
	if cfg.inboundFileSweepInterval != 5*time.Minute {
		t.Errorf("inboundFileSweepInterval = %s, want 5m", cfg.inboundFileSweepInterval)
	}
}

func TestLoadConfigInboundFileRetentionDisabled(t *testing.T) {
	env := baseSlackEnv()
	env["INBOUND_FILE_TTL"] = "0"
	cfg, err := loadConfigFromEnv(stubEnv(env))
	if err != nil {
		t.Fatalf("loadConfigFromEnv: %v", err)
	}
	if cfg.inboundFileTTL != 0 {
		t.Errorf("inboundFileTTL = %s, want 0 (disabled)", cfg.inboundFileTTL)
	}
}

func TestLoadConfigInboundFileRetentionInvalid(t *testing.T) {
	env := baseSlackEnv()
	env["INBOUND_FILE_TTL"] = "not-a-duration"
	cfg, err := loadConfigFromEnv(stubEnv(env))
	if err != nil {
		t.Fatalf("loadConfigFromEnv: %v", err)
	}
	// Invalid → field stays at zero, which disables the janitor.
	if cfg.inboundFileTTL != 0 {
		t.Errorf("inboundFileTTL = %s, want 0 on invalid input", cfg.inboundFileTTL)
	}
}

// TestStorePermissions guards the create-time perm constants on the two
// JSON-backed registries: identity store and handle-alias store. Both
// must produce 0o600 files inside 0o700 parent dirs so default
// /tmp/gc-slack-adapter/* state is not world-readable on a shared host.
// gc-ywe.6.
func TestStorePermissions(t *testing.T) {
	cases := []struct {
		name string
		make func(t *testing.T) (path string, write func() error)
	}{
		{
			name: "identity registry",
			make: func(t *testing.T) (string, func() error) {
				path := filepath.Join(t.TempDir(), "store", "identities.json")
				reg, err := newIdentityRegistry(path)
				if err != nil {
					t.Fatalf("newIdentityRegistry: %v", err)
				}
				return path, func() error {
					return reg.Set("gc-perm-test", identityRecord{Username: "x"})
				}
			},
		},
		{
			name: "handle alias registry",
			make: func(t *testing.T) (string, func() error) {
				path := filepath.Join(t.TempDir(), "store", "handle-aliases.json")
				reg, err := newHandleAliasRegistry(path)
				if err != nil {
					t.Fatalf("newHandleAliasRegistry: %v", err)
				}
				return path, func() error {
					return reg.Set("@perm", "gc-perm-test")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, write := tc.make(t)
			if err := write(); err != nil {
				t.Fatalf("write: %v", err)
			}
			fileInfo, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat file: %v", err)
			}
			if got := fileInfo.Mode().Perm(); got != 0o600 {
				t.Errorf("file %s mode = %#o, want 0o600", path, got)
			}
			dirInfo, err := os.Stat(filepath.Dir(path))
			if err != nil {
				t.Fatalf("stat parent dir: %v", err)
			}
			if got := dirInfo.Mode().Perm(); got != 0o700 {
				t.Errorf("parent dir %s mode = %#o, want 0o700", filepath.Dir(path), got)
			}
		})
	}
}

// TestDownloadSlackFilesPermissions guards the create-time perms on the
// inbound-file path: the per-channel directory must be 0o700 and the
// downloaded file (post-rename) must be 0o600. Rename preserves the
// source mode set by OpenFile, so this also locks in the OpenFile
// constant. gc-ywe.6.
func TestDownloadSlackFilesPermissions(t *testing.T) {
	const body = "PNG-BYTES"
	slackStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(slackStub.Close)

	cfg := config{
		slackBotToken:    "xoxb-test",
		inboundFileStore: filepath.Join(t.TempDir(), "inbound"),
	}
	files := []slackFile{{
		ID:         "F1",
		Name:       "shot.png",
		URLPrivate: slackStub.URL + "/files/F1",
		MIMEType:   "image/png",
	}}

	got := downloadSlackFiles(cfg, "C123", "1234.5678", files)
	if len(got) != 1 {
		t.Fatalf("got %d attachments, want 1", len(got))
	}

	channelDir := filepath.Join(cfg.inboundFileStore, "C123")
	dirInfo, err := os.Stat(channelDir)
	if err != nil {
		t.Fatalf("stat channel dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("channel dir mode = %#o, want 0o700", perm)
	}

	destPath := strings.TrimPrefix(got[0].URL, "file://")
	fileInfo, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("stat downloaded file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %#o, want 0o600 (rename should preserve OpenFile mode)", perm)
	}
}

// TestUDSPermissions guards that the proxy_process Unix domain socket is
// chmod'd to 0o600 immediately after bind. Defense-in-depth on top of
// the controller-managed 0o700 parent dir at /tmp/gcsvc-<uid>/<hash>/.
// gc-ywe.6.
func TestUDSPermissions(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	lis, err := listenUDS(sockPath)
	if err != nil {
		t.Fatalf("listenUDS: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("UDS mode = %#o, want 0o600", perm)
	}
}

// TestTightenStorePermissions covers the one-shot startup migration
// helper: legacy state from pre-fix installs gets tightened to
// 0o700/0o600, but already-tight perms are left alone, deliberately
// setuid/setgid/sticky bits are preserved, and operator-tighter perms
// (e.g. 0o400 read-only) are not loosened. gc-ywe.6.
func TestTightenStorePermissions(t *testing.T) {
	t.Run("loose perms tightened", func(t *testing.T) {
		dir := t.TempDir()
		idDir := filepath.Join(dir, "id")
		idFile := filepath.Join(idDir, "identities.json")
		aliasDir := filepath.Join(dir, "alias")
		aliasFile := filepath.Join(aliasDir, "handle-aliases.json")
		inboundDir := filepath.Join(dir, "inbound")
		channelDir := filepath.Join(inboundDir, "C123")
		channelFile := filepath.Join(channelDir, "1234.5678-pic.png")
		for _, d := range []string{idDir, aliasDir, channelDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", d, err)
			}
		}
		for _, f := range []string{idFile, aliasFile, channelFile} {
			if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
				t.Fatalf("write %s: %v", f, err)
			}
		}

		cfg := config{
			identityStorePath:    idFile,
			handleAliasStorePath: aliasFile,
			inboundFileStore:     inboundDir,
		}
		tightenStorePermissions(cfg)

		for _, d := range []string{idDir, aliasDir, inboundDir, channelDir} {
			info, err := os.Stat(d)
			if err != nil {
				t.Fatalf("stat %s: %v", d, err)
			}
			if perm := info.Mode().Perm(); perm != 0o700 {
				t.Errorf("dir %s mode = %#o, want 0o700", d, perm)
			}
		}
		for _, f := range []string{idFile, aliasFile, channelFile} {
			info, err := os.Stat(f)
			if err != nil {
				t.Fatalf("stat %s: %v", f, err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Errorf("file %s mode = %#o, want 0o600", f, perm)
			}
		}
	})

	t.Run("already-tight no-op", func(t *testing.T) {
		dir := t.TempDir()
		idFile := filepath.Join(dir, "identities.json")
		if err := os.WriteFile(idFile, []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatalf("chmod parent: %v", err)
		}
		cfg := config{identityStorePath: idFile}
		tightenStorePermissions(cfg)
		info, err := os.Stat(idFile)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("file mode drifted from 0o600 to %#o", perm)
		}
	})

	t.Run("missing paths no-op", func(t *testing.T) {
		dir := t.TempDir()
		cfg := config{
			identityStorePath:    filepath.Join(dir, "missing-id", "id.json"),
			handleAliasStorePath: filepath.Join(dir, "missing-alias", "alias.json"),
			inboundFileStore:     filepath.Join(dir, "missing-inbound"),
		}
		// Should not panic, should not error to caller (helper returns void).
		tightenStorePermissions(cfg)
		// And should not have created any of the missing paths.
		for _, p := range []string{cfg.identityStorePath, cfg.handleAliasStorePath, cfg.inboundFileStore} {
			if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s should still be missing, got err=%v", p, err)
			}
		}
	})

	t.Run("empty paths no-op", func(t *testing.T) {
		// All-empty config: helper should be a no-op without panicking.
		tightenStorePermissions(config{})
	})

	t.Run("setgid bit preserved on dir", func(t *testing.T) {
		dir := t.TempDir()
		inboundDir := filepath.Join(dir, "inbound")
		if err := os.MkdirAll(inboundDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		// Set 0o2755 — setgid + world-readable. Tightener should drop
		// the world bits but preserve the setgid bit.
		if err := os.Chmod(inboundDir, os.ModeSetgid|0o755); err != nil {
			t.Fatalf("chmod setgid: %v", err)
		}
		cfg := config{inboundFileStore: inboundDir}
		tightenStorePermissions(cfg)
		info, err := os.Stat(inboundDir)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode()&os.ModeSetgid == 0 {
			t.Errorf("setgid bit was stripped: mode = %v", info.Mode())
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("perm bits = %#o, want 0o700", perm)
		}
	})

	t.Run("operator-tighter file not loosened", func(t *testing.T) {
		dir := t.TempDir()
		idFile := filepath.Join(dir, "identities.json")
		if err := os.WriteFile(idFile, []byte("x"), 0o400); err != nil {
			t.Fatalf("write: %v", err)
		}
		cfg := config{identityStorePath: idFile}
		tightenStorePermissions(cfg)
		info, err := os.Stat(idFile)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o400 {
			t.Errorf("operator-tighter 0o400 file was loosened to %#o", perm)
		}
	})

	t.Run("symlinks not followed", func(t *testing.T) {
		// Defense-in-depth: a symlink planted in INBOUND_FILE_STORE/<channel>/
		// must NOT cause tightenPerm to chmod the symlink target. Go's
		// stdlib has no Lchmod, so chmod-on-symlink would silently
		// modify whatever the link points to.
		dir := t.TempDir()
		inboundDir := filepath.Join(dir, "inbound")
		channelDir := filepath.Join(inboundDir, "C123")
		if err := os.MkdirAll(channelDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		// Target file lives outside the store and stays at 0o644 — if the
		// tightener follows the symlink, this will become 0o600.
		targetFile := filepath.Join(dir, "outside.txt")
		if err := os.WriteFile(targetFile, []byte("x"), 0o644); err != nil {
			t.Fatalf("write target: %v", err)
		}
		linkPath := filepath.Join(channelDir, "link")
		if err := os.Symlink(targetFile, linkPath); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		cfg := config{inboundFileStore: inboundDir}
		tightenStorePermissions(cfg)

		info, err := os.Stat(targetFile)
		if err != nil {
			t.Fatalf("stat target: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o644 {
			t.Errorf("symlink target chmod'd to %#o; tightener followed the link", perm)
		}
	})

	t.Run("operator-tighter file: subsequent saveLocked propagates EACCES", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses DAC; cannot validate EACCES propagation")
		}
		// Architect M2: if operator pre-set the file to 0o400, the
		// tightener correctly skips, but the next saveLocked must
		// still surface the EACCES rather than swallowing it.
		dir := t.TempDir()
		idFile := filepath.Join(dir, "identities.json")
		if err := os.WriteFile(idFile, []byte("{}"), 0o400); err != nil {
			t.Fatalf("write: %v", err)
		}
		// Lock the parent dir read-only too — this prevents the
		// atomic temp-file write rather than the rename, which is
		// the actual EACCES surface. (0o400 file alone is fine for
		// the rename target since rename replaces; the parent's
		// write bit is what gates tmp-file creation.)
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatalf("chmod parent: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

		reg, err := newIdentityRegistry(idFile)
		if err != nil {
			t.Fatalf("newIdentityRegistry: %v", err)
		}
		err = reg.Set("gc-x", identityRecord{Username: "x"})
		if err == nil {
			t.Fatalf("Set: want error, got nil")
		}
		if !strings.Contains(err.Error(), "identity store") {
			t.Errorf("error not wrapped with context: %v", err)
		}
	})
}

func TestSlackKindFromChannelType(t *testing.T) {
	cases := []struct {
		name        string
		channelType string
		channelID   string
		want        string
	}{
		{"explicit im", "im", "D0B0TTS550F", "dm"},
		{"explicit public channel", "channel", "C0123ROOM01", "room"},
		{"explicit private channel", "group", "G0123ROOM01", "room"},
		{"explicit multi-party DM", "mpim", "G0123ROOM02", "room"},
		{"missing type, dm prefix", "", "D0B0TTS550F", "dm"},
		{"missing type, public prefix", "", "C0FALLBACK01", "room"},
		{"missing type, private prefix", "", "G0FALLBACK02", "room"},
		{"unknown both, default dm", "weird", "X0NEW", "dm"},
		{"empty both", "", "", "dm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := slackKindFromChannelType(tc.channelType, tc.channelID)
			if got != tc.want {
				t.Errorf("slackKindFromChannelType(%q, %q) = %q, want %q",
					tc.channelType, tc.channelID, got, tc.want)
			}
		})
	}
}
