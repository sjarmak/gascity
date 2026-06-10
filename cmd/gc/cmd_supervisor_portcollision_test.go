package main

import (
	"net"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// TestSupervisorAddrInUse verifies that only EADDRINUSE (the signature of a
// second supervisor competing for the shared API port) is classified as a
// port collision — other listen errors must fall through to the generic path.
func TestSupervisorAddrInUse(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"address in use", syscall.EADDRINUSE, true},
		{"wrapped address in use", &net.OpError{Op: "listen", Err: syscall.EADDRINUSE}, true},
		{"connection refused", syscall.ECONNREFUSED, false},
		{"permission denied", syscall.EACCES, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := supervisorAddrInUse(tc.err); got != tc.want {
				t.Fatalf("supervisorAddrInUse(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestSupervisorPortInUseMessage verifies the diagnostic is loud and
// actionable: it names the address, states only one supervisor may run, and
// points the operator at both remedies (stop the other / pick another port).
func TestSupervisorPortInUseMessage(t *testing.T) {
	const addr = "127.0.0.1:8372"
	const cfg = "/home/someone/.gc/supervisor.toml"
	msg := supervisorPortInUseMessage(addr, cfg)

	for _, want := range []string{
		addr,
		cfg,
		"already in use",
		"only one supervisor",
		"gc supervisor stop",
		"port =",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("port-in-use message missing %q; got:\n%s", want, msg)
		}
	}
}

// TestSupervisorSystemdTemplatePreventsRestartOnPortCollision verifies the
// generated systemd unit lists the duplicate-port exit code in
// RestartPreventExitStatus, so a duplicate install exits once instead of
// crash-looping on the shared port forever (the ga-ceq regression).
func TestSupervisorSystemdTemplatePreventsRestartOnPortCollision(t *testing.T) {
	data := &supervisorServiceData{
		GCPath:            "/usr/local/bin/gc",
		LogPath:           "/home/someone/.gc/supervisor.log",
		GCHome:            "/home/someone/.gc",
		Path:              "/usr/bin",
		PortInUseExitCode: supervisorExitCodePortInUse,
	}
	content, err := renderSupervisorTemplate(supervisorSystemdTemplate, data)
	if err != nil {
		t.Fatalf("render systemd template: %v", err)
	}
	want := "RestartPreventExitStatus=" + strconv.Itoa(supervisorExitCodePortInUse)
	if !strings.Contains(content, want) {
		t.Fatalf("systemd unit missing %q; got:\n%s", want, content)
	}
	// Genuine crashes must still restart — Restart=always stays in force.
	if !strings.Contains(content, "Restart=always") {
		t.Fatalf("systemd unit lost Restart=always; got:\n%s", content)
	}
}
