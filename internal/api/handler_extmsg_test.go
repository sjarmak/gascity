package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/extmsg"
	"github.com/gastownhall/gascity/internal/session"
)

type testExtMsgAdapter struct {
	publishCalls        []extmsg.PublishRequest
	receiptConversation extmsg.ConversationRef
}

func (a *testExtMsgAdapter) Name() string { return "test-extmsg-adapter" }

func (a *testExtMsgAdapter) Capabilities() extmsg.AdapterCapabilities {
	return extmsg.AdapterCapabilities{}
}

func (a *testExtMsgAdapter) VerifyAndNormalizeInbound(context.Context, extmsg.InboundPayload) (*extmsg.ExternalInboundMessage, error) {
	panic("unexpected VerifyAndNormalizeInbound call")
}

func (a *testExtMsgAdapter) Publish(_ context.Context, req extmsg.PublishRequest) (*extmsg.PublishReceipt, error) {
	a.publishCalls = append(a.publishCalls, req)
	conversation := req.Conversation
	if a.receiptConversation != (extmsg.ConversationRef{}) {
		conversation = a.receiptConversation
	}
	return &extmsg.PublishReceipt{
		MessageID:    "discord-msg-1",
		Conversation: conversation,
		Delivered:    true,
	}, nil
}

func (a *testExtMsgAdapter) EnsureChildConversation(context.Context, extmsg.ConversationRef, string) (*extmsg.ConversationRef, error) {
	panic("unexpected EnsureChildConversation call")
}

func TestHandleExtMsgOutboundNotifiesPeerMembersAndMaterializesNamedSessions(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	services := extmsg.NewServices(fs.cityBeadStore)
	fs.extmsgSvc = &services
	registry := extmsg.NewAdapterRegistry()
	adapter := &testExtMsgAdapter{}
	registry.Register(extmsg.AdapterKey{Provider: "discord", AccountID: "acct-1"}, adapter)
	fs.adapterReg = registry

	source := createTestSession(t, fs.cityBeadStore, fs.sp, "Publisher")
	ref := extmsg.ConversationRef{
		ScopeID:        "guild-1",
		Provider:       "discord",
		AccountID:      "acct-1",
		ConversationID: "thread-1",
		Kind:           extmsg.ConversationThread,
	}
	caller := extmsg.Caller{Kind: extmsg.CallerController, ID: "test"}
	now := time.Now().UTC()
	if _, err := services.Bindings.Bind(context.Background(), caller, extmsg.BindInput{
		Conversation: ref,
		SessionID:    source.ID,
		Now:          now,
	}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, err := services.Transcript.EnsureMembership(context.Background(), extmsg.EnsureMembershipInput{
		Caller:         caller,
		Conversation:   ref,
		SessionID:      "myrig/worker",
		BackfillPolicy: extmsg.MembershipBackfillSinceJoin,
		Owner:          extmsg.MembershipOwnerManual,
		Now:            now,
	}); err != nil {
		t.Fatalf("EnsureMembership(peer): %v", err)
	}
	if _, err := session.ResolveSessionID(fs.cityBeadStore, "myrig/worker"); err == nil {
		t.Fatal("named peer should not be materialized before outbound publish")
	}

	body, err := json.Marshal(map[string]any{
		"session_id": source.ID,
		"conversation": map[string]any{
			"scope_id":        ref.ScopeID,
			"provider":        ref.Provider,
			"account_id":      ref.AccountID,
			"conversation_id": ref.ConversationID,
			"kind":            ref.Kind,
		},
		"text": "hello peers",
	})
	if err != nil {
		t.Fatalf("Marshal(body): %v", err)
	}
	req := newPostRequest(cityURL(fs, "/extmsg/outbound"), strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(adapter.publishCalls) != 1 {
		t.Fatalf("publish calls = %d, want 1", len(adapter.publishCalls))
	}
	if adapter.publishCalls[0].Text != "hello peers" {
		t.Fatalf("publish text = %q, want hello peers", adapter.publishCalls[0].Text)
	}

	var peerID string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		peerID, err = session.ResolveSessionID(fs.cityBeadStore, "myrig/worker")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("ResolveSessionID(myrig/worker): %v", err)
	}
	peerBead, err := fs.cityBeadStore.Get(peerID)
	if err != nil {
		t.Fatalf("Get(peer): %v", err)
	}
	peerSessionName := peerBead.Metadata["session_name"]
	if peerSessionName == "" {
		t.Fatal("materialized peer session missing session_name")
	}
	if !fs.sp.IsRunning(peerSessionName) {
		t.Fatalf("peer session %q should be running after outbound publish", peerSessionName)
	}

	peerNudges := 0
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		peerNudges = 0
		for _, call := range fs.sp.Calls {
			if call.Method != "Nudge" {
				continue
			}
			if call.Name == source.SessionName {
				t.Fatalf("source session should not receive peer publish nudge; calls=%#v", fs.sp.Calls)
			}
			if call.Name == peerSessionName && strings.Contains(call.Message, "hello peers") {
				peerNudges++
			}
		}
		if peerNudges == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if peerNudges != 1 {
		t.Fatalf("peer nudge count = %d, want 1; calls=%#v", peerNudges, fs.sp.Calls)
	}
}

func TestExtmsgNotifyMembersDoesNotMaterializeExcludedNamedSender(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	services := extmsg.NewServices(fs.cityBeadStore)
	fs.extmsgSvc = &services

	ref := extmsg.ConversationRef{
		ScopeID:        "guild-1",
		Provider:       "discord",
		AccountID:      "acct-1",
		ConversationID: "thread-1",
		Kind:           extmsg.ConversationThread,
	}
	caller := extmsg.Caller{Kind: extmsg.CallerController, ID: "test"}
	if _, err := services.Transcript.EnsureMembership(context.Background(), extmsg.EnsureMembershipInput{
		Caller:         caller,
		Conversation:   ref,
		SessionID:      "myrig/worker",
		BackfillPolicy: extmsg.MembershipBackfillSinceJoin,
		Owner:          extmsg.MembershipOwnerManual,
		Now:            time.Now().UTC(),
	}); err != nil {
		t.Fatalf("EnsureMembership(sender): %v", err)
	}
	if _, err := session.ResolveSessionID(fs.cityBeadStore, "myrig/worker"); err == nil {
		t.Fatal("named sender should not be materialized before notify")
	}

	srv.extmsgNotifyMembers(context.Background(), ref, "worker", "agent", "self update", "myrig/worker")

	if _, err := session.ResolveSessionID(fs.cityBeadStore, "myrig/worker"); err == nil {
		t.Fatal("excluded named sender was materialized")
	}
	for _, call := range fs.sp.Calls {
		if call.Method == "Nudge" {
			t.Fatalf("excluded sender should not receive nudge; calls=%#v", fs.sp.Calls)
		}
	}
}

// TestExtmsgNotifyMembersNudgeTextIsProviderNeutral ensures the
// peer-publication nudge does not embed instructions that reference a
// specific provider (e.g. "Discord") or non-existent CLI subcommands
// (e.g. `gc discord reply-current`, `gc transcript read --ack`). The
// nudge must announce the message and let the recipient's prompt
// template decide what to do with it.
func TestExtmsgNotifyMembersNudgeTextIsProviderNeutral(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	services := extmsg.NewServices(fs.cityBeadStore)
	fs.extmsgSvc = &services

	ref := extmsg.ConversationRef{
		ScopeID:        "T0B17700WUW",
		Provider:       "slack",
		AccountID:      "T0B17700WUW",
		ConversationID: "D0B0TTS550F",
		Kind:           extmsg.ConversationDM,
	}
	caller := extmsg.Caller{Kind: extmsg.CallerController, ID: "test"}
	if _, err := services.Transcript.EnsureMembership(context.Background(), extmsg.EnsureMembershipInput{
		Caller:         caller,
		Conversation:   ref,
		SessionID:      "myrig/worker",
		BackfillPolicy: extmsg.MembershipBackfillSinceJoin,
		Owner:          extmsg.MembershipOwnerManual,
		Now:            time.Now().UTC(),
	}); err != nil {
		t.Fatalf("EnsureMembership: %v", err)
	}

	srv.extmsgNotifyMembers(context.Background(), ref, "human", "human", "ack", "")

	deadline := time.Now().Add(time.Second)
	var nudgeMsg string
	for time.Now().Before(deadline) {
		for _, call := range fs.sp.Calls {
			if call.Method == "Nudge" && strings.Contains(call.Message, "ack") {
				nudgeMsg = call.Message
				break
			}
		}
		if nudgeMsg != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if nudgeMsg == "" {
		t.Fatalf("expected a nudge containing the message text; calls=%#v", fs.sp.Calls)
	}

	mustContain := []string{
		"slack/D0B0TTS550F",
		"human",
		"ack",
	}
	for _, want := range mustContain {
		if !strings.Contains(nudgeMsg, want) {
			t.Errorf("nudge missing %q; got: %s", want, nudgeMsg)
		}
	}
	mustNotContain := []string{
		"Discord",
		"gc discord",
		"reply-current",
		"gc transcript",
	}
	for _, banned := range mustNotContain {
		if strings.Contains(nudgeMsg, banned) {
			t.Errorf("nudge must not contain %q (broken/provider-specific reply instruction); got: %s", banned, nudgeMsg)
		}
	}
}

func TestHandleExtMsgOutboundNotifiesDeliveredConversationMembers(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	services := extmsg.NewServices(fs.cityBeadStore)
	fs.extmsgSvc = &services
	requestRef := extmsg.ConversationRef{
		ScopeID:        "guild-1",
		Provider:       "discord",
		AccountID:      "acct-1",
		ConversationID: "thread-request",
		Kind:           extmsg.ConversationThread,
	}
	deliveredRef := requestRef
	deliveredRef.ConversationID = "thread-delivered"
	registry := extmsg.NewAdapterRegistry()
	adapter := &testExtMsgAdapter{receiptConversation: deliveredRef}
	registry.Register(extmsg.AdapterKey{Provider: "discord", AccountID: "acct-1"}, adapter)
	fs.adapterReg = registry

	source := createTestSession(t, fs.cityBeadStore, fs.sp, "Publisher")
	caller := extmsg.Caller{Kind: extmsg.CallerController, ID: "test"}
	now := time.Now().UTC()
	if _, err := services.Bindings.Bind(context.Background(), caller, extmsg.BindInput{
		Conversation: requestRef,
		SessionID:    source.ID,
		Now:          now,
	}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, err := services.Transcript.EnsureMembership(context.Background(), extmsg.EnsureMembershipInput{
		Caller:         caller,
		Conversation:   deliveredRef,
		SessionID:      "myrig/worker",
		BackfillPolicy: extmsg.MembershipBackfillSinceJoin,
		Owner:          extmsg.MembershipOwnerManual,
		Now:            now,
	}); err != nil {
		t.Fatalf("EnsureMembership(peer): %v", err)
	}

	body, err := json.Marshal(map[string]any{
		"session_id": source.ID,
		"conversation": map[string]any{
			"scope_id":        requestRef.ScopeID,
			"provider":        requestRef.Provider,
			"account_id":      requestRef.AccountID,
			"conversation_id": requestRef.ConversationID,
			"kind":            requestRef.Kind,
		},
		"text": "hello delivered peers",
	})
	if err != nil {
		t.Fatalf("Marshal(body): %v", err)
	}
	req := newPostRequest(cityURL(fs, "/extmsg/outbound"), strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var peerID string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		peerID, err = session.ResolveSessionID(fs.cityBeadStore, "myrig/worker")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("ResolveSessionID(myrig/worker): %v", err)
	}
	peerBead, err := fs.cityBeadStore.Get(peerID)
	if err != nil {
		t.Fatalf("Get(peer): %v", err)
	}
	peerSessionName := peerBead.Metadata["session_name"]
	if peerSessionName == "" {
		t.Fatal("materialized peer session missing session_name")
	}

	found := false
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, call := range fs.sp.Calls {
			if call.Method == "Nudge" && call.Name == peerSessionName && strings.Contains(call.Message, "thread-delivered") {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatalf("delivered conversation peer nudge not found; calls=%#v", fs.sp.Calls)
	}
}

// TestHandleExtMsgGroupEnsureRoundTripsFanoutPolicy verifies that
// FanoutPolicy is settable via the Huma input on POST /extmsg/groups
// and is preserved on the returned record (and on the subsequent GET
// /extmsg/groups lookup). Without this, gc slack bind-room (and the
// upstream discord pack equivalent) cannot configure peer-fanout
// policy through the public API.
func TestHandleExtMsgGroupEnsureRoundTripsFanoutPolicy(t *testing.T) {
	fs := newSessionFakeState(t)
	srv := New(fs)

	services := extmsg.NewServices(fs.cityBeadStore)
	fs.extmsgSvc = &services

	ref := extmsg.ConversationRef{
		ScopeID:        "T0B17700WUW",
		Provider:       "slack",
		AccountID:      "T0B17700WUW",
		ConversationID: "C0123ROOM01",
		Kind:           extmsg.ConversationRoom,
	}

	body, err := json.Marshal(map[string]any{
		"root_conversation": map[string]any{
			"scope_id":        ref.ScopeID,
			"provider":        ref.Provider,
			"account_id":      ref.AccountID,
			"conversation_id": ref.ConversationID,
			"kind":            ref.Kind,
		},
		"mode":           extmsg.GroupModeLauncher,
		"default_handle": "mayor",
		"fanout_policy": map[string]any{
			"enabled":                      true,
			"allow_untargeted_publication": true,
			"max_peer_triggered_publishes": 5,
			"max_total_peer_deliveries":    12,
		},
	})
	if err != nil {
		t.Fatalf("Marshal(body): %v", err)
	}
	req := newPostRequest(cityURL(fs, "/extmsg/groups"), strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var ensured struct {
		ID           string              `json:"ID"`
		FanoutPolicy extmsg.FanoutPolicy `json:"FanoutPolicy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ensured); err != nil {
		t.Fatalf("Unmarshal ensure response: %v; body=%s", err, rec.Body.String())
	}
	if ensured.ID == "" {
		t.Fatalf("ensure response missing ID; body=%s", rec.Body.String())
	}
	if !ensured.FanoutPolicy.Enabled ||
		!ensured.FanoutPolicy.AllowUntargetedPublication ||
		ensured.FanoutPolicy.MaxPeerTriggeredPublishes != 5 ||
		ensured.FanoutPolicy.MaxTotalPeerDeliveries != 12 {
		t.Fatalf("ensure response did not preserve fanout policy: %+v", ensured.FanoutPolicy)
	}

	// Read it back via GET /extmsg/groups.
	lookupURL := cityURL(fs, "/extmsg/groups") +
		"?scope_id=" + ref.ScopeID +
		"&provider=" + ref.Provider +
		"&account_id=" + ref.AccountID +
		"&conversation_id=" + ref.ConversationID +
		"&kind=" + string(ref.Kind)
	getReq := httptest.NewRequest("GET", lookupURL, nil)
	getRec := httptest.NewRecorder()
	srv.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d; body: %s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	var fetched struct {
		FanoutPolicy extmsg.FanoutPolicy `json:"FanoutPolicy"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("Unmarshal lookup response: %v; body=%s", err, getRec.Body.String())
	}
	if !fetched.FanoutPolicy.Enabled ||
		!fetched.FanoutPolicy.AllowUntargetedPublication ||
		fetched.FanoutPolicy.MaxPeerTriggeredPublishes != 5 ||
		fetched.FanoutPolicy.MaxTotalPeerDeliveries != 12 {
		t.Fatalf("lookup did not preserve fanout policy: %+v", fetched.FanoutPolicy)
	}
}
