package dashboard

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

// Serve starts the dashboard HTTP server. The dashboard is a static
// TypeScript SPA that calls the supervisor's typed OpenAPI endpoints
// directly from the browser — there is no proxy layer anymore. This
// function's only job is to embed + serve the compiled bundle and
// inject `supervisorURL` into the page so the SPA knows where to
// reach the supervisor.
//
// allowedHosts lists non-loopback Host header names the server accepts in
// addition to localhost; an empty slice means loopback-only. Host
// validation closes DNS rebinding even when the listener accepts remote
// connections (see withHostAllowing).
func Serve(port int, supervisorURL string, allowedHosts []string) error {
	supervisorURL = strings.TrimRight(strings.TrimSpace(supervisorURL), "/")
	if supervisorURL == "" {
		return fmt.Errorf("dashboard: supervisor URL is empty; pass --api")
	}

	handler, err := NewStaticHandler(supervisorURL)
	if err != nil {
		return err
	}

	addr := fmt.Sprintf(":%d", port)
	log.Printf("dashboard: listening on http://localhost%s (supervisor=%s)", addr, supervisorURL)
	return http.ListenAndServe(addr, logRequest(withHostAllowing(allowedHosts, handler)))
}

// problemHostNotAllowed is the pre-serialized RFC 9457 Problem Details body
// returned for a rejected Host header. Pre-serializing keeps the reject
// path free of runtime JSON marshaling and mirrors the supervisor API's
// 421 response shape.
var problemHostNotAllowed = []byte(`{"status":421,"title":"Misdirected Request","detail":"host_not_allowed: dashboard Host header is not allowed"}`)

// withHostAllowing rejects DNS-rebinding style requests whose Host header is
// neither loopback nor explicitly allowed, returning 421 Misdirected
// Request. The dashboard has no auth layer, so the Host allowlist is what
// stops a browser tricked into resolving an attacker host to a dashboard
// bind address from driving the local UI.
func withHostAllowing(allowedHosts []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAllowedDashboardHost(r.Host, allowedHosts) {
			w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
			w.WriteHeader(http.StatusMisdirectedRequest)
			_, _ = w.Write(problemHostNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}
