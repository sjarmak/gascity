package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func newPeerFanoutRetryBody(t *testing.T, sess string) ([]byte, extmsg.ConversationRef) {
	t.Helper()
	conv := extmsg.ConversationRef{
		ScopeID:        "test-city",
		Provider:       "slack",
		AccountID:      "T0TESTWS",
		ConversationID: "C0ROOM01",
		Kind:           extmsg.ConversationRoom,
	}
	body, err := json.Marshal(map[string]any{
		"original_seq":       uint64(101),
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

func waitForRetriedEvent(t *testing.T, prov events.Provider) extmsg.PeerFanoutRetriedEventPayload {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		evts, _ := prov.List(events.Filter{Type: events.ExtMsgPeerFanoutRetried})
		if len(evts) > 0 {
			var p extmsg.PeerFanoutRetriedEventPayload
			if err := json.Unmarshal(evts[0].Payload, &p); err != nil {
				t.Fatalf("decode peer_fanout_retried payload: %v", err)
			}
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for extmsg.peer_fanout_retried event")
	return extmsg.PeerFanoutRetriedEventPayload{}
}

func TestPeerFanoutRetrySuccessEmitsAuditEvent(t *testing.T) {
	fs := peerFanoutTestState(t)
	srv := New(fs)

	target := createTestSession(t, fs.cityBeadStore, fs.sp, "Peer worker")

	body, conv := newPeerFanoutRetryBody(t, target.ID)
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
	if resp.Body.OriginalSeq != 101 {
		t.Fatalf("OriginalSeq = %d, want 101", resp.Body.OriginalSeq)
	}

	got := waitForRetriedEvent(t, fs.eventProv)
	if !got.Success {
		t.Fatalf("retried event should be success=true, got %+v", got)
	}
	if got.OriginalSeq != 101 {
		t.Fatalf("retried event OriginalSeq = %d, want 101", got.OriginalSeq)
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

	body, _ := newPeerFanoutRetryBody(t, "no-such-session-xyz")
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

	got := waitForRetriedEvent(t, fs.eventProv)
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
