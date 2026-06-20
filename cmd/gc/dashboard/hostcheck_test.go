package dashboard

import "testing"

func TestIsAllowedDashboardHost(t *testing.T) {
	cases := []struct {
		name  string
		host  string
		extra []string
		want  bool
	}{
		{"empty host", "", nil, false},
		{"localhost", "localhost", nil, true},
		{"localhost with port", "localhost:8080", nil, true},
		{"ipv4 loopback", "127.0.0.1", nil, true},
		{"ipv4 loopback with port", "127.0.0.1:8080", nil, true},
		{"ipv4 loopback range", "127.0.0.2:8080", nil, true},
		{"ipv6 loopback", "[::1]:8080", nil, true},
		{"ipv6 loopback no port", "::1", nil, true},
		{"uppercase localhost", "LOCALHOST:8080", nil, true},
		{"private ip rejected", "192.168.1.20:8080", nil, false},
		{"public host rejected", "evil.example", nil, false},
		{"public host with port rejected", "evil.example:8080", nil, false},
		{"localhost-suffixed attack rejected", "localhost.evil.example", nil, false},
		{"configured host allowed", "dash.internal:8080", []string{"dash.internal"}, true},
		{"configured host case-insensitive", "Dash.Internal:8080", []string{"dash.internal"}, true},
		{"configured host with port in allowlist", "dash.internal:8080", []string{"dash.internal:9999"}, true},
		{"unconfigured host rejected", "other.internal:8080", []string{"dash.internal"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAllowedDashboardHost(tc.host, tc.extra); got != tc.want {
				t.Fatalf("isAllowedDashboardHost(%q, %v) = %v, want %v", tc.host, tc.extra, got, tc.want)
			}
		})
	}
}
