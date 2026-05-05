package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/extmsg"
)

// peerFanoutTestState wires the minimum extmsg services required for the
// retry handler: services + adapter registry. The retry handler does not
// publish through an adapter, but the surrounding init expects them.
func peerFanoutTestState(t *testing.T) *fakeState {
	t.Helper()
	fs := newSessionFakeState(t)
	services := extmsg.NewServices(fs.cityBeadStore)
	fs.extmsgSvc = &services
	fs.adapterReg = extmsg.NewAdapterRegistry()
	return fs
}

func newPeerFanoutRetryBody(t *testing.T, sess string, originalSeq uint64) ([]byte, extmsg.ConversationRef) {
	t.Helper()
	conv := extmsg.ConversationRef{
		ScopeID:        "test-city",
		Provider:       "slack",
		AccountID:      "T0TESTWS",
		ConversationID: "C0ROOM01",
		Kind:           extmsg.ConversationRoom,
	}
	body, err := json.Marshal(map[string]any{
		"original_seq":       originalSeq,
		"target_session":     sess,
		"actor_display_name": "alice",
		"actor_kind":         "human",
		"text":               "hello peers",
		"conversation": map[string]any{
			"scope_id":        conv.ScopeID,
			"provider":        conv.Provider,
			"account_id":      conv.AccountID,
			"conversation_id": conv.ConversationID,
			"kind":            string(conv.Kind),
		},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return body, conv
}

// seedFailedEvent records an extmsg.peer_fanout_failed event with the
// supplied payload tuple so the retry handler's gc-cby.40 validation
// can find it. Returns the assigned Seq.
//
// Note: Fake.Record auto-assigns Seq via f.seq++ regardless of the
// input event's Seq, so we read LatestSeq after recording to obtain
// the value the handler will see.
//
// The handler validates that original_seq references a real failed
// event whose payload (provider, conversation_id, target_session)
// matches the request — without seeding this, every test would 400.
func seedFailedEvent(t *testing.T, prov events.Provider, conv extmsg.ConversationRef, target string) uint64 {
	t.Helper()
	payload := extmsg.PeerFanoutFailedEventPayload{
		Provider:       conv.Provider,
		ConversationID: conv.ConversationID,
		TargetSession:  target,
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed payload: %v", err)
	}
	prov.Record(events.Event{
		Type:    events.ExtMsgPeerFanoutFailed,
		Payload: pb,
	})
	seq, err := prov.LatestSeq()
	if err != nil {
		t.Fatalf("latest seq: %v", err)
	}
	return seq
}

// readRetriedEvent fetches the extmsg.peer_fanout_retried audit event
// emitted by the retry handler. The handler emits SYNCHRONOUSLY before
// returning from ServeHTTP (see extmsgEmitEvent in handler_extmsg.go),
// so by the time the HTTP response is decoded the event is already on
// the bus — no polling required.
func readRetriedEvent(t *testing.T, prov events.Provider) extmsg.PeerFanoutRetriedEventPayload {
	t.Helper()
	evts, err := prov.List(events.Filter{Type: events.ExtMsgPeerFanoutRetried})
	if err != nil {
		t.Fatalf("list peer_fanout_retried events: %v", err)
	}
	if len(evts) == 0 {
		t.Fatal("expected extmsg.peer_fanout_retried event to be recorded synchronously, got none")
	}
	var p extmsg.PeerFanoutRetriedEventPayload
	if err := json.Unmarshal(evts[0].Payload, &p); err != nil {
		t.Fatalf("decode peer_fanout_retried payload: %v", err)
	}
	return p
}

func TestPeerFanoutRetrySuccessEmitsAuditEvent(t *testing.T) {
	fs := peerFanoutTestState(t)
	srv := New(fs)

	target := createTestSession(t, fs.cityBeadStore, fs.sp, "Peer worker")

	conv := extmsg.ConversationRef{
		ScopeID: "test-city", Provider: "slack",
		AccountID: "T0TESTWS", ConversationID: "C0ROOM01",
		Kind: extmsg.ConversationRoom,
	}
	seq := seedFailedEvent(t, fs.eventProv, conv, target.ID)
	body, _ := newPeerFanoutRetryBody(t, target.ID, seq)
	req := newPostRequest(cityURL(fs, "/extmsg/peer-fanout/retry"), strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp ExtMsgPeerFanoutRetryOutput
	if err := json.NewDecoder(rec.Body).Decode(&resp.Body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Body.Success {
		t.Fatalf("expected Success=true, got error=%q", resp.Body.Error)
	}
	if resp.Body.OriginalSeq != seq {
		t.Fatalf("OriginalSeq = %d, want %d", resp.Body.OriginalSeq, seq)
	}

	got := readRetriedEvent(t, fs.eventProv)
	if !got.Success {
		t.Fatalf("retried event should be success=true, got %+v", got)
	}
	if got.OriginalSeq != seq {
		t.Fatalf("retried event OriginalSeq = %d, want %d", got.OriginalSeq, seq)
	}
	if got.Provider != conv.Provider {
		t.Fatalf("retried event provider = %q, want %q", got.Provider, conv.Provider)
	}
	if got.ConversationID != conv.ConversationID {
		t.Fatalf("retried event conversation_id = %q, want %q", got.ConversationID, conv.ConversationID)
	}
}

func TestPeerFanoutRetryUnknownSessionEmitsFailureEvent(t *testing.T) {
	fs := peerFanoutTestState(t)
	srv := New(fs)

	conv := extmsg.ConversationRef{
		ScopeID: "test-city", Provider: "slack",
		AccountID: "T0TESTWS", ConversationID: "C0ROOM01",
		Kind: extmsg.ConversationRoom,
	}
	seq := seedFailedEvent(t, fs.eventProv, conv, "no-such-session-xyz")
	body, _ := newPeerFanoutRetryBody(t, "no-such-session-xyz", seq)
	req := newPostRequest(cityURL(fs, "/extmsg/peer-fanout/retry"), strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var resp ExtMsgPeerFanoutRetryOutput
	if err := json.NewDecoder(rec.Body).Decode(&resp.Body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Body.Success {
		t.Fatal("expected Success=false for unknown session")
	}
	if resp.Body.Error == "" {
		t.Fatal("expected non-empty Error on failure response")
	}

	got := readRetriedEvent(t, fs.eventProv)
	if got.Success {
		t.Fatalf("retried event should be success=false, got %+v", got)
	}
	if got.Error == "" {
		t.Fatal("retried event Error should be non-empty on failure")
	}
}

func TestPeerFanoutRetryRequiresOriginalSeq(t *testing.T) {
	fs := peerFanoutTestState(t)
	srv := New(fs)

	target := createTestSession(t, fs.cityBeadStore, fs.sp, "Peer worker")
	body, err := json.Marshal(map[string]any{
		// original_seq omitted entirely
		"target_session":     target.ID,
		"actor_display_name": "alice",
		"actor_kind":         "human",
		"text":               "hello",
		"conversation": map[string]any{
			"scope_id":        "test-city",
			"provider":        "slack",
			"account_id":      "T0TESTWS",
			"conversation_id": "C0ROOM01",
			"kind":            "room",
		},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	req := newPostRequest(cityURL(fs, "/extmsg/peer-fanout/retry"), strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("status = %d, want a 4xx for missing original_seq; body: %s", rec.Code, rec.Body.String())
	}
}

// TestPeerFanoutRetryRejectsForgedSeq — gc-cby.40: original_seq that
// does not reference a real peer_fanout_failed event must be rejected
// with 400, preventing audit-log poisoning and bounded amplification.
func TestPeerFanoutRetryRejectsForgedSeq(t *testing.T) {
	fs := peerFanoutTestState(t)
	srv := New(fs)

	target := createTestSession(t, fs.cityBeadStore, fs.sp, "Peer worker")
	body, _ := newPeerFanoutRetryBody(t, target.ID, 101)
	// Do NOT seed a failed event — the seq=101 in the body is forged.
	req := newPostRequest(cityURL(fs, "/extmsg/peer-fanout/retry"), strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "does not reference") {
		t.Errorf("expected error to mention missing failed event; got %s", rec.Body.String())
	}
}

// TestPeerFanoutRetryRejectsTargetSessionMismatch — gc-cby.40: even
// when seq references a real failed event, the request's
// target_session must match the failed event's payload. Otherwise an
// attacker could redirect the retry to a different session.
func TestPeerFanoutRetryRejectsTargetSessionMismatch(t *testing.T) {
	fs := peerFanoutTestState(t)
	srv := New(fs)

	target := createTestSession(t, fs.cityBeadStore, fs.sp, "Peer worker")
	conv := extmsg.ConversationRef{
		ScopeID: "test-city", Provider: "slack",
		AccountID: "T0TESTWS", ConversationID: "C0ROOM01",
		Kind: extmsg.ConversationRoom,
	}
	// Seed the failed event with a DIFFERENT target_session than the request.
	seq := seedFailedEvent(t, fs.eventProv, conv, "different-target-session")
	body, _ := newPeerFanoutRetryBody(t, target.ID, seq)
	req := newPostRequest(cityURL(fs, "/extmsg/peer-fanout/retry"), strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "target_session mismatch") {
		t.Errorf("expected target_session-mismatch error; got %s", rec.Body.String())
	}
}

// TestPeerFanoutRetryRejectsConversationIDMismatch — gc-cby.40:
// conversation_id must also match.
func TestPeerFanoutRetryRejectsConversationIDMismatch(t *testing.T) {
	fs := peerFanoutTestState(t)
	srv := New(fs)

	target := createTestSession(t, fs.cityBeadStore, fs.sp, "Peer worker")
	// Seed with a DIFFERENT conversation_id than the request.
	seq := seedFailedEvent(t, fs.eventProv,
		extmsg.ConversationRef{Provider: "slack", ConversationID: "C-OTHER"},
		target.ID)
	body, _ := newPeerFanoutRetryBody(t, target.ID, seq)
	req := newPostRequest(cityURL(fs, "/extmsg/peer-fanout/retry"), strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "conversation_id mismatch") {
		t.Errorf("expected conversation_id-mismatch error; got %s", rec.Body.String())
	}
}

// TestPeerFanoutRetryHandlerCallable is a smoke check that the route is
// registered and the handler resolves without panicking on an empty body.
func TestPeerFanoutRetryRequiresFields(t *testing.T) {
	fs := peerFanoutTestState(t)
	srv := New(fs)

	req := newPostRequest(cityURL(fs, "/extmsg/peer-fanout/retry"), strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("empty body should yield 4xx, got %d body=%s", rec.Code, rec.Body.String())
	}
}
