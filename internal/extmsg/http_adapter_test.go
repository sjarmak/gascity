package extmsg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHTTPAdapterSendsCSRFHeader is a regression for the live-smoke
// finding from gc-j8h: HTTPAdapter callbacks routed through gc's /svc
// proxy were silently 403'd because Publish/PublishFile/EnsureChildConversation
// never set the X-GC-Request header that the proxy's CSRF middleware
// requires for private mutation endpoints.
//
// Each subtest stands up a tiny HTTP server, invokes the callback, and
// asserts the header is present on the outbound request.
func TestHTTPAdapterSendsCSRFHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		response any
		exercise func(t *testing.T, a *HTTPAdapter, base string)
	}{
		{
			name:     "publish",
			path:     "/publish",
			response: PublishReceipt{Delivered: true, MessageID: "M1"},
			exercise: func(t *testing.T, a *HTTPAdapter, _ string) {
				t.Helper()
				if _, err := a.Publish(context.Background(), PublishRequest{
					SessionID: "gc-test",
					Conversation: ConversationRef{
						ScopeID:        "ds-research",
						Provider:       "slack",
						AccountID:      "T0TEST",
						ConversationID: "C0TEST",
						Kind:           "room",
					},
					Text: "hello",
				}); err != nil {
					t.Fatalf("Publish: %v", err)
				}
			},
		},
		{
			name:     "publish-file",
			path:     "/publish-file",
			response: PublishFileReceipt{Delivered: true, FileID: "F1"},
			exercise: func(t *testing.T, a *HTTPAdapter, _ string) {
				t.Helper()
				if _, err := a.PublishFile(context.Background(), PublishFileRequest{
					SessionID: "gc-test",
					Conversation: ConversationRef{
						ScopeID:        "ds-research",
						Provider:       "slack",
						AccountID:      "T0TEST",
						ConversationID: "C0TEST",
						Kind:           "room",
					},
					FilePath: "/tmp/whatever.txt",
				}); err != nil {
					t.Fatalf("PublishFile: %v", err)
				}
			},
		},
		{
			name: "child-conversation",
			path: "/child-conversation",
			response: ConversationRef{
				ScopeID: "ds-research", Provider: "slack",
				AccountID: "T0TEST", ConversationID: "C0CHILD", Kind: "thread",
			},
			exercise: func(t *testing.T, a *HTTPAdapter, _ string) {
				t.Helper()
				ref := ConversationRef{
					ScopeID: "ds-research", Provider: "slack",
					AccountID: "T0TEST", ConversationID: "C0PARENT", Kind: "room",
				}
				if _, err := a.EnsureChildConversation(context.Background(), ref, "label"); err != nil {
					t.Fatalf("EnsureChildConversation: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var sawCSRF, sawContentType string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Errorf("unexpected path: got %q want %q", r.URL.Path, tt.path)
				}
				sawCSRF = r.Header.Get("X-GC-Request")
				sawContentType = r.Header.Get("Content-Type")
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tt.response)
			}))
			t.Cleanup(srv.Close)

			a := NewHTTPAdapter("test", srv.URL, AdapterCapabilities{SupportsAttachments: true})
			tt.exercise(t, a, srv.URL)

			if sawCSRF == "" {
				t.Errorf("X-GC-Request header missing on %s callback", tt.path)
			}
			if sawContentType != "application/json" {
				t.Errorf("Content-Type header wrong: got %q", sawContentType)
			}
		})
	}
}
