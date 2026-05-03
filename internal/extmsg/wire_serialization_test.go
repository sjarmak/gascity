package extmsg

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// TestPublishRequestMarshalsSnakeCase guards against regression of gc-w1h:
// gc → adapter `/publish` posts a JSON-encoded PublishRequest, and the
// adapter unmarshals using snake_case json tags. If PublishRequest's tags
// are dropped, Go's default PascalCase output silently fails to populate
// the underscore-bearing fields (reply_to_message_id, idempotency_key,
// metadata) on the receiver — threading and idempotency break.
func TestPublishRequestMarshalsSnakeCase(t *testing.T) {
	req := PublishRequest{
		Conversation: ConversationRef{
			Provider:       "slack",
			AccountID:      "T0",
			ConversationID: "C0",
			Kind:           ConversationRoom,
		},
		Text:             "hi",
		ReplyToMessageID: "1.2",
		IdempotencyKey:   "idem-1",
		Metadata:         map[string]string{"source_session_id": "gc-1"},
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	mustContain := []string{
		`"conversation":`,
		`"text":"hi"`,
		`"reply_to_message_id":"1.2"`,
		`"idempotency_key":"idem-1"`,
		`"metadata":{"source_session_id":"gc-1"}`,
	}
	for _, want := range mustContain {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("PublishRequest JSON missing %q\n  got: %s", want, body)
		}
	}

	mustNotContain := []string{
		`"ReplyToMessageID"`,
		`"IdempotencyKey"`,
		`"Metadata"`,
		`"Conversation"`,
		`"Text"`,
	}
	for _, bad := range mustNotContain {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("PublishRequest JSON contains PascalCase key %q (should be snake_case)\n  got: %s", bad, body)
		}
	}
}

// TestPublishRequestRoundTripWithSnakeCase asserts that an adapter posting
// snake_case (the wire contract) decodes correctly into a Go-side
// PublishRequest. This is the reverse direction of the gc-w1h bug — same
// hazard if tags ever drift.
func TestPublishRequestRoundTripWithSnakeCase(t *testing.T) {
	wire := []byte(`{
		"conversation":{"provider":"slack","account_id":"T0","conversation_id":"C0","kind":"room"},
		"text":"hi",
		"reply_to_message_id":"1.2",
		"idempotency_key":"idem-1",
		"metadata":{"k":"v"}
	}`)
	var req PublishRequest
	if err := json.Unmarshal(wire, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.ReplyToMessageID != "1.2" {
		t.Errorf("ReplyToMessageID = %q, want %q", req.ReplyToMessageID, "1.2")
	}
	if req.IdempotencyKey != "idem-1" {
		t.Errorf("IdempotencyKey = %q, want %q", req.IdempotencyKey, "idem-1")
	}
	if req.Metadata["k"] != "v" {
		t.Errorf("Metadata[k] = %q, want %q", req.Metadata["k"], "v")
	}
	if req.Conversation.ConversationID != "C0" {
		t.Errorf("Conversation.ConversationID = %q, want %q", req.Conversation.ConversationID, "C0")
	}
}

// TestPublishReceiptUnmarshalsSnakeCase guards against the sibling-type
// half of gc-w1h: adapter `/publish` returns a snake_case JSON body
// (`{"message_id":"…","failure_kind":"…","delivered":true}`), and gc
// unmarshals into PublishReceipt. Without snake_case tags, MessageID and
// FailureKind silently arrive empty and threading + retry semantics break.
func TestPublishReceiptUnmarshalsSnakeCase(t *testing.T) {
	wire := []byte(`{
		"message_id":"1777811592.120509",
		"conversation":{"provider":"slack","account_id":"T0","conversation_id":"C0","kind":"room"},
		"delivered":true,
		"failure_kind":"",
		"retry_after":0,
		"metadata":{"file_id":"F0"}
	}`)
	var r PublishReceipt
	if err := json.Unmarshal(wire, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.MessageID != "1777811592.120509" {
		t.Errorf("MessageID = %q, want %q", r.MessageID, "1777811592.120509")
	}
	if !r.Delivered {
		t.Errorf("Delivered = false, want true")
	}
	if r.Conversation.ConversationID != "C0" {
		t.Errorf("Conversation.ConversationID = %q, want %q", r.Conversation.ConversationID, "C0")
	}
	if r.Metadata["file_id"] != "F0" {
		t.Errorf("Metadata[file_id] = %q, want %q", r.Metadata["file_id"], "F0")
	}
}

// TestPublishReceiptUnmarshalsFailureKind asserts non-delivery responses
// are decoded with the failure classification preserved.
func TestPublishReceiptUnmarshalsFailureKind(t *testing.T) {
	wire := []byte(`{
		"message_id":"",
		"conversation":{"provider":"slack","account_id":"T0","conversation_id":"C0","kind":"room"},
		"delivered":false,
		"failure_kind":"auth"
	}`)
	var r PublishReceipt
	if err := json.Unmarshal(wire, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.FailureKind != PublishFailureAuth {
		t.Errorf("FailureKind = %q, want %q", r.FailureKind, PublishFailureAuth)
	}
	if r.Delivered {
		t.Errorf("Delivered = true, want false")
	}
}

// TestPublishReceiptMarshalsSnakeCase asserts gc → typed-wire (Huma)
// emission of PublishReceipt uses snake_case keys, consistent with the
// rest of the extmsg API surface and with the HTTP-callback wire shape.
func TestPublishReceiptMarshalsSnakeCase(t *testing.T) {
	r := PublishReceipt{
		MessageID:    "X",
		Conversation: ConversationRef{Provider: "slack", AccountID: "T0", ConversationID: "C0"},
		Delivered:    true,
		FailureKind:  PublishFailureKind(""),
		RetryAfter:   5 * time.Second,
		Metadata:     map[string]string{"k": "v"},
	}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	mustContain := []string{
		`"message_id":"X"`,
		`"delivered":true`,
		`"failure_kind":""`,
		`"retry_after":5000000000`,
		`"metadata":{"k":"v"}`,
	}
	for _, want := range mustContain {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("PublishReceipt JSON missing %q\n  got: %s", want, body)
		}
	}
	for _, bad := range []string{`"MessageID"`, `"FailureKind"`, `"RetryAfter"`} {
		if bytes.Contains(body, []byte(bad)) {
			t.Errorf("PublishReceipt JSON contains PascalCase key %q\n  got: %s", bad, body)
		}
	}
}
