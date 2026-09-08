package herdr

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/runtime/runtimetest"
)

// TestHerdrConformance runs the full runtime.Provider conformance suite against
// the herdr provider backed by a real herdr binary. Each session gets its own
// isolated herdr session-server so the contract's session-scoped assertions
// (ListRunning, orphan detection, …) don't observe sibling sessions.
//
// The herdr session name maps directly to a host-global socket path
// (client.go's socketPath, keyed only by session name under
// os.UserConfigDir()), so two concurrent processes running this suite must
// never compute the same session name. The in-process counter alone isn't
// enough — it restarts at 1 on every test-binary invocation, so two
// concurrent `go test`/`make test` sweeps collided on identical sockets and
// raced in startServer/removeStaleSocket/TeardownServer (workspace_not_found,
// connection reset, server readiness timeout). Salt with the PID and a
// per-process start time, matching the precedent in
// subprocess/seam_conformance_test.go and tmux/tmux_test.go.
//
// Opt-in live tier: see requireLiveHerdr.
func TestHerdrConformance(t *testing.T) {
	requireLiveHerdr(t)

	pid := os.Getpid()
	start := time.Now().UnixNano()
	var counter int64
	runtimetest.RunProviderTests(t, func(t *testing.T) (runtime.Provider, runtime.Config, string) {
		n := atomic.AddInt64(&counter, 1)
		p := New(fmt.Sprintf("gctest-conf-%d-%d-%d", pid, start, n), t.TempDir(), t.TempDir(), 0, 0)
		t.Cleanup(func() { _ = p.TeardownServer() })
		return p, runtime.Config{WorkDir: t.TempDir()}, fmt.Sprintf("conf-%d-%d-%d", pid, start, n)
	})
}
