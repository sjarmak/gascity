package dashboard

import (
	"net"
	"strings"
)

// isAllowedDashboardHost reports whether an HTTP Host header may reach the
// dashboard server. A loopback Host (localhost, 127.0.0.0/8, ::1) is always
// allowed; any other host must be listed in extra. This mirrors the
// supervisor API's allowlist (internal/api isAllowedSupervisorHost) so the
// dashboard and supervisor present the same DNS-rebinding posture: a browser
// tricked into resolving an attacker host to 127.0.0.1 carries that
// attacker Host header, which the allowlist rejects even though the TCP
// connection is to loopback.
//
// extra entries are matched on hostname only (any port is ignored on both
// sides), so an operator who binds the dashboard to a real hostname can
// allow it with a single name.
func isAllowedDashboardHost(hostHeader string, extra []string) bool {
	host := canonicalHost(hostHeader)
	if host == "" {
		return false
	}
	if isLoopbackHost(host) {
		return true
	}
	for _, allowed := range extra {
		if canonicalHost(allowed) == host {
			return true
		}
	}
	return false
}

// canonicalHost extracts the lowercased hostname from a Host header,
// dropping any port and IPv6 brackets. It returns "" for an empty header.
func canonicalHost(hostHeader string) string {
	hostHeader = strings.TrimSpace(hostHeader)
	if hostHeader == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(hostHeader); err == nil {
		return strings.ToLower(strings.Trim(host, "[]"))
	}
	if strings.HasPrefix(hostHeader, "[") && strings.HasSuffix(hostHeader, "]") {
		hostHeader = strings.TrimPrefix(strings.TrimSuffix(hostHeader, "]"), "[")
	}
	return strings.ToLower(hostHeader)
}

// isLoopbackHost reports whether host (a bare hostname or IP, no port) is a
// loopback identity.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
