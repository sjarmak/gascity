package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// signedSlackInteractionRequest builds a POST request to /slack/interactions
// signed with the given secret + current timestamp.
func signedSlackInteractionRequest(t *testing.T, secret string, body []byte) *http.Request {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("v0:" + ts + ":"))
	_, _ = mac.Write(body)
	sig := "v0=" + hex.EncodeToString(mac.Sum(nil))
	req := httptest.NewRequest(http.MethodPost, "/slack/interactions", strings.NewReader(string(body)))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", sig)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func newTestChannelMappingRegistry(t *testing.T) *channelMappingRegistry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "channel_mappings.json")
	reg, err := newChannelMappingRegistry(path)
	if err != nil {
		t.Fatalf("newChannelMappingRegistry: %v", err)
	}
	return reg
}

func TestSlackInteractionsRejectsNonPost(t *testing.T) {
	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	mapReg := newTestChannelMappingRegistry(t)

	req := httptest.NewRequest(http.MethodGet, "/slack/interactions", nil)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, mapReg, nil)(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestSlackInteractionsRejectsBadSignature(t *testing.T) {
	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	mapReg := newTestChannelMappingRegistry(t)

	body := url.Values{"team_id": {"T1"}, "channel_id": {"C1"}, "command": {"/gc"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/slack/interactions", strings.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("X-Slack-Signature", "v0=deadbeef")
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, mapReg, nil)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestSlackInteractionsBlockActionPayloadNotYetSupported(t *testing.T) {
	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	mapReg := newTestChannelMappingRegistry(t)

	body := []byte(url.Values{"payload": {`{"type":"block_actions"}`}}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, mapReg, nil)(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not yet supported") {
		t.Errorf("body should mention not yet supported: %s", rec.Body.String())
	}
}

func TestSlackInteractionsTeamIDMismatch(t *testing.T) {
	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	mapReg := newTestChannelMappingRegistry(t)

	body := []byte(url.Values{
		"team_id":    {"T_OTHER"},
		"channel_id": {"C1"},
		"command":    {"/gc"},
		"text":       {"hello"},
		"user_id":    {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, mapReg, nil)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for team_id mismatch", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "team_id") {
		t.Errorf("body should mention team_id: %s", rec.Body.String())
	}
}

func TestSlackInteractionsMissingTeamID(t *testing.T) {
	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	mapReg := newTestChannelMappingRegistry(t)

	body := []byte(url.Values{
		"channel_id": {"C1"},
		"command":    {"/gc"},
		"text":       {"hello"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, mapReg, nil)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for missing team_id", rec.Code)
	}
}

func TestSlackInteractionsSessionMappingHitDispatches(t *testing.T) {
	// Stub gc session-message endpoint and watch for the POST.
	var gotPath atomic.Value
	gotPath.Store("")
	dispatched := make(chan string, 1)
	gcStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		select {
		case dispatched <- r.URL.Path:
		default:
		}
	}))
	t.Cleanup(gcStub.Close)

	cfg := config{
		slackSigningKey: "secret",
		accountID:       "T1",
		cityName:        "test-city",
		gcAPIBase:       gcStub.URL,
	}
	mapReg := newTestChannelMappingRegistry(t)
	now := time.Now().UTC()
	if err := mapReg.Set(channelMappingDiskRecord{
		WorkspaceID: "T1", ChannelID: "C1",
		TargetKind: "session", TargetID: "gc-2568",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	body := []byte(url.Values{
		"team_id":    {"T1"},
		"channel_id": {"C1"},
		"command":    {"/gc"},
		"text":       {"fix the build"},
		"user_id":    {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, mapReg, nil)(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "gc-2568") {
		t.Errorf("body should reference target session: %s", rec.Body.String())
	}

	// Wait for goroutine to call gc stub.
	select {
	case path := <-dispatched:
		want := "/v0/city/test-city/session/gc-2568/messages"
		if path != want {
			t.Errorf("dispatch path = %q, want %q", path, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch goroutine did not POST to gc stub within 2s")
	}
}

func TestSlackInteractionsRigMappingHitFollowUp(t *testing.T) {
	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	mapReg := newTestChannelMappingRegistry(t)
	now := time.Now().UTC()
	_ = mapReg.Set(channelMappingDiskRecord{
		WorkspaceID: "T1", ChannelID: "C1",
		TargetKind: "rig", TargetID: "alpha",
		CreatedAt: now, UpdatedAt: now,
	})

	body := []byte(url.Values{
		"team_id":    {"T1"},
		"channel_id": {"C1"},
		"command":    {"/gc"},
		"text":       {"deploy"},
		"user_id":    {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, mapReg, nil)(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "follow-up") {
		t.Errorf("body should mention follow-up bead: %s", rec.Body.String())
	}
}

func TestSlackInteractionsMappingMiss(t *testing.T) {
	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	mapReg := newTestChannelMappingRegistry(t)

	body := []byte(url.Values{
		"team_id":    {"T1"},
		"channel_id": {"C_UNBOUND"},
		"command":    {"/gc"},
		"text":       {"x"},
		"user_id":    {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, mapReg, nil)(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "No binding") {
		t.Errorf("body should mention 'No binding': %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "map-channel") {
		t.Errorf("body should reference map-channel hint: %s", rec.Body.String())
	}
}

func TestSlackInteractionsEmptyBody(t *testing.T) {
	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	mapReg := newTestChannelMappingRegistry(t)

	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, []byte(""))
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, mapReg, nil)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for empty body", rec.Code)
	}
}

func TestSlackInteractionsResponseEnvelope(t *testing.T) {
	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	mapReg := newTestChannelMappingRegistry(t)

	body := []byte(url.Values{
		"team_id":    {"T1"},
		"channel_id": {"C1"},
		"command":    {"/gc"},
		"text":       {"x"},
		"user_id":    {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, mapReg, nil)(rec, req)

	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", got)
	}
	var env struct {
		ResponseType string `json:"response_type"`
		Text         string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response not valid JSON: %v\nbody=%s", err, rec.Body.String())
	}
	if env.ResponseType != "ephemeral" {
		t.Errorf("response_type = %q, want ephemeral", env.ResponseType)
	}
	if env.Text == "" {
		t.Errorf("text empty")
	}
}

func newTestRigMappingRegistry(t *testing.T) *rigMappingRegistry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rig_mappings.json")
	reg, err := newRigMappingRegistry(path)
	if err != nil {
		t.Fatalf("newRigMappingRegistry: %v", err)
	}
	return reg
}

func TestResolveChannelTargetChannelMappingWinsOverRig(t *testing.T) {
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	if err := chanReg.Set(channelMappingDiskRecord{
		WorkspaceID: "T1", ChannelID: "C1",
		TargetKind: "session", TargetID: "gc-1",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
		CreatedAt:  now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	rec, src, ok := resolveChannelTarget(chanReg, rigReg, "T1", "C1")
	if !ok {
		t.Fatal("resolveChannelTarget ok=false")
	}
	if src != "channel" {
		t.Errorf("source = %q, want channel", src)
	}
	if rec.TargetKind != "session" || rec.TargetID != "gc-1" {
		t.Errorf("channel mapping should have won: %+v", rec)
	}
}

func TestResolveChannelTargetFallsThroughToRig(t *testing.T) {
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	if err := rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C2"},
		CreatedAt:  now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	rec, src, ok := resolveChannelTarget(chanReg, rigReg, "T1", "C2")
	if !ok {
		t.Fatal("expected fall-through to rig store")
	}
	if src != "rig" {
		t.Errorf("source = %q, want rig", src)
	}
	if rec.TargetKind != "rig" || rec.TargetID != "alpha" {
		t.Errorf("synthetic record mismatch: %+v", rec)
	}
}

func TestResolveChannelTargetMiss(t *testing.T) {
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	if _, src, ok := resolveChannelTarget(chanReg, rigReg, "T1", "C-UNBOUND"); ok || src != "" {
		t.Errorf("expected miss, got src=%q ok=%v", src, ok)
	}
}

func TestSlackInteractionsResolverRigFallThrough(t *testing.T) {
	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	if err := rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
		CreatedAt:  now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	body := []byte(url.Values{
		"team_id":    {"T1"},
		"channel_id": {"C1"},
		"command":    {"/gc"},
		"text":       {"deploy"},
		"user_id":    {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, chanReg, rigReg)(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "alpha") {
		t.Errorf("body should mention rig alpha (rig fall-through): %s", rec.Body.String())
	}
}

func TestSlackInteractionsResolverChannelOverride(t *testing.T) {
	// Stub gc session-message endpoint.
	gotPath := make(chan string, 1)
	gcStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		select {
		case gotPath <- r.URL.Path:
		default:
		}
	}))
	t.Cleanup(gcStub.Close)

	cfg := config{
		slackSigningKey: "secret",
		accountID:       "T1",
		cityName:        "test-city",
		gcAPIBase:       gcStub.URL,
	}
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	// channel mapping pins C1 to a session — that's the override.
	_ = chanReg.Set(channelMappingDiskRecord{
		WorkspaceID: "T1", ChannelID: "C1",
		TargetKind: "session", TargetID: "gc-2568",
		CreatedAt: now, UpdatedAt: now,
	})
	// rig store ALSO covers C1 — but channel mapping must win.
	_ = rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
		CreatedAt:  now, UpdatedAt: now,
	})

	body := []byte(url.Values{
		"team_id":    {"T1"},
		"channel_id": {"C1"},
		"command":    {"/gc"},
		"text":       {"x"},
		"user_id":    {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, chanReg, rigReg)(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	select {
	case path := <-gotPath:
		if path != "/v0/city/test-city/session/gc-2568/messages" {
			t.Errorf("dispatched path = %q, want session route", path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch did not occur — channel override did not win")
	}
}

func TestSlackInteractionsResolverSourceDiscriminatorLogged(t *testing.T) {
	var logs strings.Builder
	prevOut := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prevOut) })

	cfg := config{slackSigningKey: "secret", accountID: "T1", cityName: "test-city"}
	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	_ = rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "alpha",
		ChannelIDs: []string{"C1"},
		CreatedAt:  now, UpdatedAt: now,
	})
	body := []byte(url.Values{
		"team_id":    {"T1"},
		"channel_id": {"C1"},
		"command":    {"/gc"},
		"text":       {"x"},
		"user_id":    {"U1"},
	}.Encode())
	req := signedSlackInteractionRequest(t, cfg.slackSigningKey, body)
	rec := httptest.NewRecorder()
	handleSlackInteractions(cfg, chanReg, rigReg)(rec, req)

	if !strings.Contains(logs.String(), "source=rig") {
		t.Errorf("log should include source discriminator 'source=rig': %s", logs.String())
	}
}

func TestLogCrossStoreOverlapWarning(t *testing.T) {
	var logs strings.Builder
	prevOut := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prevOut) })

	chanReg := newTestChannelMappingRegistry(t)
	rigReg := newTestRigMappingRegistry(t)
	now := time.Now().UTC()
	_ = chanReg.Set(channelMappingDiskRecord{
		WorkspaceID: "T1", ChannelID: "C1",
		TargetKind: "rig", TargetID: "x",
		CreatedAt: now, UpdatedAt: now,
	})
	_ = rigReg.Set(rigMappingDiskRecord{
		WorkspaceID: "T1", RigName: "y",
		ChannelIDs: []string{"C1"},
		CreatedAt:  now, UpdatedAt: now,
	})
	logCrossStoreOverlapWarnings(chanReg, rigReg)
	out := logs.String()
	if !strings.Contains(out, "WARN") {
		t.Errorf("log should include WARN: %s", out)
	}
	if !strings.Contains(out, "rig=\"x\"") || !strings.Contains(out, "rig=\"y\"") {
		t.Errorf("log should include both conflicting rig names: %s", out)
	}
}

func TestChannelMappingRegistryRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel_mappings.json")
	corrupt := map[string]channelMappingDiskRecord{
		"T1:C1": {
			WorkspaceID: "T1", ChannelID: "C1",
			TargetKind: "bogus", TargetID: "x",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
	}
	data, _ := json.MarshalIndent(corrupt, "", "  ")
	if err := writeFile0600(path, data); err != nil {
		t.Fatal(err)
	}
	if _, err := newChannelMappingRegistry(path); err == nil {
		t.Fatal("expected load error for corrupt file")
	}
}

// TestChannelMappingRegistryRejectsUnknownField pins sec-S-02: the
// adapter's reader must use DisallowUnknownFields so a hand-edited
// file that adds an unknown JSON field is surfaced rather than
// silently absorbed. Mirrors the rig-mapping reader's policy.
func TestChannelMappingRegistryRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel_mappings.json")
	if err := writeFile0600(path, []byte(`{"T1:C1":{"workspace_id":"T1","channel_id":"C1","target_kind":"session","target_id":"gc-1","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z","bogus":42}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := newChannelMappingRegistry(path); err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

// TestDispatchSlashCommandToSessionEscapesPathSegments verifies that
// cityName and sessionID values containing URL-significant characters
// are percent-encoded in the constructed dispatch URL (sec-S-06). The
// receiver decodes them and observes the original logical values via
// r.URL.Path.
func TestDispatchSlashCommandToSessionEscapesPathSegments(t *testing.T) {
	rawPathCh := make(chan string, 1)
	decodedPathCh := make(chan string, 1)
	gcStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		select {
		case rawPathCh <- r.URL.EscapedPath():
		default:
		}
		select {
		case decodedPathCh <- r.URL.Path:
		default:
		}
	}))
	t.Cleanup(gcStub.Close)

	cfg := config{gcAPIBase: gcStub.URL, cityName: "city/with slash"}
	dispatchSlashCommandToSession(cfg, "gc/2568%evil", "/gc", "fix the build", "C1", "T1", "U1")

	var rawPath, decodedPath string
	select {
	case rawPath = <-rawPathCh:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch goroutine did not POST to gc stub within 2s")
	}
	select {
	case decodedPath = <-decodedPathCh:
	default:
	}

	wantRawCity := "city%2Fwith%20slash"
	wantRawSession := "gc%2F2568%25evil"
	if !strings.Contains(rawPath, wantRawCity) {
		t.Errorf("raw path %q missing escaped cityName %q", rawPath, wantRawCity)
	}
	if !strings.Contains(rawPath, wantRawSession) {
		t.Errorf("raw path %q missing escaped sessionID %q", rawPath, wantRawSession)
	}
	wantDecoded := "/v0/city/city/with slash/session/gc/2568%evil/messages"
	if decodedPath != wantDecoded {
		t.Errorf("decoded path = %q, want %q", decodedPath, wantDecoded)
	}
}
