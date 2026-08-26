package herdr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// socketPath must resolve the SAME directory herdr itself uses for its
// config/socket state, or this client dials a path no herdr server ever
// binds: serverAlive() then always reads false against a healthy server
// ("did not become ready"), and every retry launches a redundant herdr
// server contending for the same pane ("agent_pane_busy") — ga-nqlb8q.
// Verified empirically against the herdr binary itself: `XDG_CONFIG_HOME=X
// herdr --help` prints "Config: X/herdr/config.toml" regardless of $HOME,
// and with XDG_CONFIG_HOME unset it falls back to "$HOME/.config/herdr/…" —
// standard XDG Base Directory precedence, which os.UserConfigDir()
// implements and the old os.UserHomeDir()+".config" join did not: a sandbox
// that sets XDG_CONFIG_HOME to the real user's config dir while redirecting
// $HOME elsewhere (this fleet's agent sandboxes do exactly that) made the
// old code compute a path no herdr process ever binds.

func TestSocketPathHonorsXDGConfigHomeOverHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", t.TempDir()) // deliberately different; must be ignored

	c := newClient("xdgtest", "")
	if got, want := c.socketPath(), filepath.Join(xdg, "herdr", "sessions", "xdgtest", "herdr.sock"); got != want {
		t.Errorf("socketPath() = %q; want %q", got, want)
	}

	c.session = "default"
	if got, want := c.socketPath(), filepath.Join(xdg, "herdr", "herdr.sock"); got != want {
		t.Errorf("socketPath() (default session) = %q; want %q", got, want)
	}
}

func TestSocketPathFallsBackToHomeConfigWhenXDGUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	c := newClient("hometest", "")
	if got, want := c.socketPath(), filepath.Join(home, ".config", "herdr", "sessions", "hometest", "herdr.sock"); got != want {
		t.Errorf("socketPath() = %q; want %q", got, want)
	}
}

// TestRunPreservesErrorCodeFromStderrEnvelope pins the structured-code path
// for a herdr invocation that exits nonzero and writes its error envelope to
// stderr. Every code-driven recovery in this package — the agent_pane_busy
// retry loop in Start, resolveAgentNameTaken's adoption — gates on
// herdrErrorCode, so a stderr envelope flattened into an opaque string
// silently disables all of them: the caller sees an unrecognized failure and
// gives up on the first attempt. That is what made
// TestProviderLiveClaudeKindPath fail in 0.53s under load (gc-mlkgy), far
// short of the retry loop's own backoff budget.
func TestRunPreservesErrorCodeFromStderrEnvelope(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	script := "#!/bin/sh\n" +
		`printf '%s' '{"error":{"code":"agent_pane_busy","message":"agent target pane w1:p1 is not an available shell"},"id":"cli:agent:start"}' >&2` + "\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &client{bin: bin, session: "gctest"}
	_, err := c.run(context.Background(), "agent", "start", "kindsmoke")
	if err == nil {
		t.Fatal("run returned nil error for a nonzero herdr exit")
	}
	if got := herdrErrorCode(err); got != "agent_pane_busy" {
		t.Fatalf("herdrErrorCode = %q, want %q (error was %v)", got, "agent_pane_busy", err)
	}
	if !strings.Contains(err.Error(), "not an available shell") {
		t.Fatalf("error = %v, want herdr's own message preserved", err)
	}
}

// TestRunKeepsOpaqueStderrWhenNotAnEnvelope keeps the non-JSON stderr path
// intact: a herdr crash or a shell-level failure has no envelope to decode
// and must still surface its text.
func TestRunKeepsOpaqueStderrWhenNotAnEnvelope(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'panic: boom' >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &client{bin: bin, session: "gctest"}
	_, err := c.run(context.Background(), "agent", "list")
	if err == nil {
		t.Fatal("run returned nil error for a nonzero herdr exit")
	}
	if got := herdrErrorCode(err); got != "" {
		t.Fatalf("herdrErrorCode = %q, want empty for non-envelope stderr", got)
	}
	if !strings.Contains(err.Error(), "panic: boom") {
		t.Fatalf("error = %v, want the raw stderr preserved", err)
	}
}
