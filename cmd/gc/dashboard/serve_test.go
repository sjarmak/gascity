package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithHostAllowing(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("served"))
	})

	cases := []struct {
		name       string
		host       string
		allowed    []string
		wantStatus int
		wantServed bool
	}{
		{"loopback ipv4", "127.0.0.1:8080", nil, http.StatusOK, true},
		{"localhost", "localhost:8080", nil, http.StatusOK, true},
		{"ipv6 loopback", "[::1]:8080", nil, http.StatusOK, true},
		{"rebinding host rejected", "evil.example:8080", nil, http.StatusMisdirectedRequest, false},
		{"private ip rejected", "192.168.1.20:8080", nil, http.StatusMisdirectedRequest, false},
		{"empty host rejected", "", nil, http.StatusMisdirectedRequest, false},
		{"configured host allowed", "dash.internal:8080", []string{"dash.internal"}, http.StatusOK, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := withHostAllowing(tc.allowed, okHandler)
			req := httptest.NewRequest(http.MethodGet, "http://placeholder/", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantServed {
				if rec.Body.String() != "served" {
					t.Fatalf("body = %q, want served", rec.Body.String())
				}
				return
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
				t.Fatalf("Content-Type = %q, want application/problem+json", got)
			}
			if !strings.Contains(rec.Body.String(), "host_not_allowed") {
				t.Fatalf("body = %q, want host_not_allowed problem detail", rec.Body.String())
			}
		})
	}
}
