package herdr

import (
	"path/filepath"
	"runtime"
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
//
// Both tests below skip on windows, darwin, ios, and plan9: os.UserConfigDir()
// only follows XDG_CONFIG_HOME on its Unix default branch, and on darwin/ios
// it ignores the env var entirely, always resolving under
// "$HOME/Library/Application Support" (see the Go stdlib implementation
// and its own os_test.go, which skips the identical platform set for the
// same reason — darwin and ios share that branch). Asserting an XDG- or
// ".config"-rooted path is only valid on the platforms os.UserConfigDir()
// treats as XDG-following. Whether herdr's own binary uses pure-XDG
// resolution on every one of those platforms is unverified here — the
// empirical check above was run on Linux only — so the tests skip on the
// platforms os.UserConfigDir() itself does not follow XDG, rather than
// assert an unconfirmed cross-platform contract.

func skipUnlessXDGPlatform(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "windows", "darwin", "ios", "plan9":
		t.Skipf("os.UserConfigDir() does not follow XDG_CONFIG_HOME on %s; herdr's own resolution on this platform is unverified (see ga-nqlb8q)", runtime.GOOS)
	}
}

func TestSocketPathHonorsXDGConfigHomeOverHome(t *testing.T) {
	skipUnlessXDGPlatform(t)
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
	skipUnlessXDGPlatform(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	c := newClient("hometest", "")
	if got, want := c.socketPath(), filepath.Join(home, ".config", "herdr", "sessions", "hometest", "herdr.sock"); got != want {
		t.Errorf("socketPath() = %q; want %q", got, want)
	}
}
