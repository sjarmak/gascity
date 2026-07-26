package tmuxtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestKillServersUnderRootReapsNamedSocket verifies that KillServersUnderRoot
// kills a live server on a non-gctest socket (here "test-city", the name the
// cmd/gc named-session tests resolve) whose socket lives under an isolated
// root. This is the reap that prevents the #4656 orphan: a server the
// gctest-* sweep never matches, left running while its socket root is removed.
func TestKillServersUnderRootReapsNamedSocket(t *testing.T) {
	RequireTmux(t)

	root := t.TempDir()
	const socketName = "test-city"
	socketPath := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()), socketName)

	// Safety net: reap directly by socket path even if the code under test
	// fails, so a bug here never leaks the very orphan it is meant to prevent.
	// Registered after t.TempDir so it runs before the root is removed.
	t.Cleanup(func() { _ = killTestSocketPath(socketPath) })

	spawnDetachedTmuxServer(t, root, socketName)
	if !tmuxServerListening(socketPath) {
		t.Fatalf("expected a live tmux server on socket %s", socketPath)
	}

	if killed := KillServersUnderRoot(root); killed != 1 {
		t.Fatalf("KillServersUnderRoot(%q) = %d, want 1", root, killed)
	}
	// The server must be dead. The socket file itself may linger (a Unix
	// domain socket is not auto-unlinked on server exit); that is harmless —
	// the socket-root removal sweeps it, and #4656 is about a *live* server
	// stranded on an unlinked socket, not a dead socket file.
	if tmuxServerListening(socketPath) {
		t.Fatalf("tmux server on %s still listening after KillServersUnderRoot", socketPath)
	}
	if killed := KillServersUnderRoot(root); killed != 0 {
		t.Fatalf("second KillServersUnderRoot(%q) = %d, want 0 (already reaped)", root, killed)
	}
}

// TestKillServersUnderRootEmptyRootIsNoop guards the trivial inputs so the
// helper never enumerates the whole filesystem or panics on a missing root.
func TestKillServersUnderRootEmptyRootIsNoop(t *testing.T) {
	if killed := KillServersUnderRoot(""); killed != 0 {
		t.Fatalf("KillServersUnderRoot(\"\") = %d, want 0", killed)
	}
	if killed := KillServersUnderRoot(t.TempDir()); killed != 0 {
		t.Fatalf("KillServersUnderRoot(<empty root>) = %d, want 0", killed)
	}
}

// spawnDetachedTmuxServer starts a detached tmux server on socketName under
// socketRoot, the same way production does: TMUX_TMPDIR points at the root and
// -L selects the socket. The pane runs a long sleep so the server stays up
// until it is explicitly killed.
func spawnDetachedTmuxServer(t testing.TB, socketRoot, socketName string) {
	t.Helper()
	cmd := exec.Command("tmux", "-L", socketName, "new-session", "-d", "-s", "probe", "sleep 300")
	cmd.Env = tmuxSpawnEnv(socketRoot)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("spawn tmux server on socket %q: %v\n%s", socketName, err, out)
	}
}

// tmuxServerListening reports whether a live tmux server answers on socketPath.
// It uses list-sessions, which exits non-zero ("no server running") when the
// server is dead and never auto-starts one.
func tmuxServerListening(socketPath string) bool {
	return exec.Command("tmux", "-S", socketPath, "list-sessions").Run() == nil
}

// tmuxSpawnEnv returns the process environment with TMUX_TMPDIR pointed at
// socketRoot and any inherited tmux client bindings stripped, so the spawned
// server lands under socketRoot and does not attach to an outer session.
func tmuxSpawnEnv(socketRoot string) []string {
	base := os.Environ()
	env := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if strings.HasPrefix(kv, tmuxTmpEnv+"=") ||
			strings.HasPrefix(kv, tmuxEnv+"=") ||
			strings.HasPrefix(kv, tmuxPaneEnv+"=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, tmuxTmpEnv+"="+socketRoot)
}
