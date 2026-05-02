package main

import (
	"strings"
	"testing"
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
	// All three Slack secrets missing — should report all three in one error.
	env := map[string]string{}

	_, err := loadConfigFromEnv(stubEnv(env))
	if err == nil {
		t.Fatal("loadConfigFromEnv: want error for missing slack secrets, got nil")
	}
	for _, key := range []string{"SLACK_WORKSPACE_ID", "SLACK_BOT_TOKEN", "SLACK_SIGNING_SECRET"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q missing %s", err.Error(), key)
		}
	}
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
