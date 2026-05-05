package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSlackServer captures the most recent /chat.postMessage or
// /chat.update request and returns a configurable response. Tests
// inspect Last* fields to assert on what the CLI sent.
type fakeSlackServer struct {
	server      *httptest.Server
	LastPath    string
	LastAuth    string
	LastBody    slackChatPostBody
	LastRawBody string
	NextResp    slackChatPostResponse
	NextStatus  int
}

func newFakeSlackServer(t *testing.T) *fakeSlackServer {
	t.Helper()
	f := &fakeSlackServer{
		NextResp:   slackChatPostResponse{OK: true, TS: "1234567890.000100", Channel: "C0001"},
		NextStatus: http.StatusOK,
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.LastPath = r.URL.Path
		f.LastAuth = r.Header.Get("Authorization")
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		f.LastRawBody = buf.String()
		// Best-effort decode for assertions.
		_ = json.Unmarshal(buf.Bytes(), &f.LastBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.NextStatus)
		_ = json.NewEncoder(w).Encode(f.NextResp)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func TestRunSlackPostMessageMilestone(t *testing.T) {
	f := newFakeSlackServer(t)
	stdout := new(bytes.Buffer)
	opts := slackPostMessageOpts{
		Channel:    "C0001",
		Kind:       "milestone",
		PayloadRaw: `{"title":"Polecat 7 reached green","summary":"Done"}`,
		BotToken:   "xoxb-test",
		APIBase:    f.server.URL,
		Stdout:     stdout,
	}
	if err := runSlackPostMessage(opts); err != nil {
		t.Fatalf("runSlackPostMessage: %v", err)
	}
	if f.LastPath != "/chat.postMessage" {
		t.Errorf("path: want /chat.postMessage, got %q", f.LastPath)
	}
	if f.LastAuth != "Bearer xoxb-test" {
		t.Errorf("auth: want Bearer xoxb-test, got %q", f.LastAuth)
	}
	if f.LastBody.Channel != "C0001" {
		t.Errorf("channel: want C0001, got %q", f.LastBody.Channel)
	}
	if !strings.Contains(f.LastBody.Text, "Polecat 7") {
		t.Errorf("fallback text missing title: %q", f.LastBody.Text)
	}
	if len(f.LastBody.Blocks) == 0 {
		t.Errorf("blocks not sent: %s", f.LastRawBody)
	}
	// stdout should contain the response ts so callers can capture it for --update.
	if !strings.Contains(stdout.String(), "1234567890.000100") {
		t.Errorf("stdout missing ts: %s", stdout.String())
	}
}

func TestRunSlackPostMessageProgress(t *testing.T) {
	f := newFakeSlackServer(t)
	opts := slackPostMessageOpts{
		Channel:    "C0001",
		Kind:       "progress",
		PayloadRaw: `{"title":"Convoy 12","progress":{"current":3,"total":5}}`,
		BotToken:   "xoxb-test",
		APIBase:    f.server.URL,
		Stdout:     new(bytes.Buffer),
	}
	if err := runSlackPostMessage(opts); err != nil {
		t.Fatalf("runSlackPostMessage: %v", err)
	}
	if !strings.Contains(f.LastRawBody, "3/5") {
		t.Errorf("progress fraction missing: %s", f.LastRawBody)
	}
}

func TestRunSlackPostMessageRollup(t *testing.T) {
	f := newFakeSlackServer(t)
	opts := slackPostMessageOpts{
		Channel:    "C0001",
		Kind:       "rollup",
		PayloadRaw: `{"title":"Daily","items":[{"label":"polecat-7","value":"healthy"}]}`,
		BotToken:   "xoxb-test",
		APIBase:    f.server.URL,
		Stdout:     new(bytes.Buffer),
	}
	if err := runSlackPostMessage(opts); err != nil {
		t.Fatalf("runSlackPostMessage: %v", err)
	}
	if !strings.Contains(f.LastRawBody, "polecat-7") {
		t.Errorf("rollup item missing: %s", f.LastRawBody)
	}
}

func TestRunSlackPostMessageUpdate(t *testing.T) {
	f := newFakeSlackServer(t)
	opts := slackPostMessageOpts{
		Channel:    "C0001",
		Kind:       "milestone",
		PayloadRaw: `{"title":"Updated","summary":"Now green"}`,
		UpdateTS:   "1234567890.000100",
		BotToken:   "xoxb-test",
		APIBase:    f.server.URL,
		Stdout:     new(bytes.Buffer),
	}
	if err := runSlackPostMessage(opts); err != nil {
		t.Fatalf("runSlackPostMessage: %v", err)
	}
	if f.LastPath != "/chat.update" {
		t.Errorf("path: want /chat.update, got %q", f.LastPath)
	}
	if f.LastBody.TS != "1234567890.000100" {
		t.Errorf("ts: want 1234567890.000100, got %q", f.LastBody.TS)
	}
}

func TestRunSlackPostMessageMissingChannel(t *testing.T) {
	err := runSlackPostMessage(slackPostMessageOpts{
		Kind:       "milestone",
		PayloadRaw: `{"title":"x"}`,
		BotToken:   "xoxb-test",
		APIBase:    "http://127.0.0.1:1",
		Stdout:     new(bytes.Buffer),
	})
	if err == nil || !strings.Contains(err.Error(), "channel") {
		t.Fatalf("want channel error, got %v", err)
	}
}

func TestRunSlackPostMessageMissingToken(t *testing.T) {
	err := runSlackPostMessage(slackPostMessageOpts{
		Channel:    "C0001",
		Kind:       "milestone",
		PayloadRaw: `{"title":"x"}`,
		Stdout:     new(bytes.Buffer),
	})
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("want token error, got %v", err)
	}
}

func TestRunSlackPostMessageInvalidPayload(t *testing.T) {
	err := runSlackPostMessage(slackPostMessageOpts{
		Channel:    "C0001",
		Kind:       "milestone",
		PayloadRaw: `not-json`,
		BotToken:   "xoxb-test",
		APIBase:    "http://127.0.0.1:1",
		Stdout:     new(bytes.Buffer),
	})
	if err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("want payload error, got %v", err)
	}
}

func TestRunSlackPostMessageUnknownKind(t *testing.T) {
	err := runSlackPostMessage(slackPostMessageOpts{
		Channel:    "C0001",
		Kind:       "wat",
		PayloadRaw: `{"title":"x"}`,
		BotToken:   "xoxb-test",
		APIBase:    "http://127.0.0.1:1",
		Stdout:     new(bytes.Buffer),
	})
	if err == nil {
		t.Fatalf("want unknown-kind error, got nil")
	}
}

func TestRunSlackPostMessageSlackError(t *testing.T) {
	f := newFakeSlackServer(t)
	f.NextResp = slackChatPostResponse{OK: false, Error: "channel_not_found"}
	err := runSlackPostMessage(slackPostMessageOpts{
		Channel:    "C0001",
		Kind:       "milestone",
		PayloadRaw: `{"title":"x"}`,
		BotToken:   "xoxb-test",
		APIBase:    f.server.URL,
		Stdout:     new(bytes.Buffer),
	})
	if err == nil {
		t.Fatalf("want slack error, got nil")
	}
	if !strings.Contains(err.Error(), "channel_not_found") {
		t.Errorf("error should include slack code: %v", err)
	}
}

func TestRunSlackPostMessageHTTPError(t *testing.T) {
	f := newFakeSlackServer(t)
	f.NextStatus = http.StatusInternalServerError
	f.NextResp = slackChatPostResponse{}
	err := runSlackPostMessage(slackPostMessageOpts{
		Channel:    "C0001",
		Kind:       "milestone",
		PayloadRaw: `{"title":"x"}`,
		BotToken:   "xoxb-test",
		APIBase:    f.server.URL,
		Stdout:     new(bytes.Buffer),
	})
	if err == nil {
		t.Fatalf("want http error, got nil")
	}
	var herr *slackHTTPError
	if !errors.As(err, &herr) {
		t.Errorf("want slackHTTPError, got %T: %v", err, err)
	}
}
