//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/beads/beadstest"
)

// TestBdStoreConformance runs the beads conformance suite against BdStore
// backed by a real dolt server. This proves the full stack works:
// dolt server → bd CLI → BdStore → beads.Store interface.
//
// Each subtest gets a fresh database directory where bd auto-starts a
// dolt server on a unique port. This avoids port conflicts and lets bd
// manage the server lifecycle.
//
// Requires: dolt and bd installed.
func TestBdStoreConformance(t *testing.T) {
	// Skip if dolt or bd not installed.
	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt not installed")
	}
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed")
	}

	// Ensure dolt identity is configured (mirrors git user.name/email).
	ensureDoltIdentity(t)

	serverDir := t.TempDir()
	var dbCounter atomic.Int64

	// Factory: each call creates a fresh workspace where bd auto-starts
	// its own dolt server (unique port via bd's auto-start mechanism).
	newStore := func() beads.Store {
		n := dbCounter.Add(1)
		prefix := fmt.Sprintf("ct%d", n)

		// Create isolated workspace directory.
		wsDir := filepath.Join(serverDir, fmt.Sprintf("ws-%d", n))
		if err := os.MkdirAll(wsDir, 0o755); err != nil {
			t.Fatalf("creating workspace: %v", err)
		}

		// Initialize git repo (bd init requires it).
		gitCmd := exec.Command("git", "init", "--quiet")
		gitCmd.Dir = wsDir
		if out, err := gitCmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}

		// Run bd init with auto-start (no --server flag = local embedded mode).
		bdInit := exec.Command("bd", "init", "-p", prefix, "--skip-hooks")
		bdInit.Dir = wsDir
		if out, err := bdInit.CombinedOutput(); err != nil {
			t.Fatalf("bd init: %v: %s", err, out)
		}

		// Explicitly set issue_prefix (required for bd create).
		bdConfig := exec.Command("bd", "config", "set", "issue_prefix", prefix)
		bdConfig.Dir = wsDir
		if out, err := bdConfig.CombinedOutput(); err != nil {
			t.Fatalf("bd config set: %v: %s", err, out)
		}

		return beads.NewBdStore(wsDir, beads.ExecCommandRunner())
	}

	// Run conformance suite. We skip RunSequentialIDTests because BdStore
	// uses bd's ID format (prefix-XXXX), not gc-N sequential format.
	beadstest.RunStoreTests(t, newStore)
	beadstest.RunMetadataTests(t, newStore)
}

// ensureDoltIdentity ensures dolt has user.name and user.email set.
// Copies from git config if available, otherwise sets defaults.
func ensureDoltIdentity(t *testing.T) {
	t.Helper()

	// Check if dolt identity is already set.
	name, _ := exec.Command("dolt", "config", "--global", "--get", "user.name").Output()
	email, _ := exec.Command("dolt", "config", "--global", "--get", "user.email").Output()

	if len(name) > 0 && len(email) > 0 {
		return
	}

	// Copy from git config.
	if len(name) == 0 {
		gitName, _ := exec.Command("git", "config", "--global", "user.name").Output()
		if len(gitName) > 0 {
			exec.Command("dolt", "config", "--global", "--add", "user.name", string(gitName)).Run()
		} else {
			exec.Command("dolt", "config", "--global", "--add", "user.name", "test").Run()
		}
	}
	if len(email) == 0 {
		gitEmail, _ := exec.Command("git", "config", "--global", "user.email").Output()
		if len(gitEmail) > 0 {
			exec.Command("dolt", "config", "--global", "--add", "user.email", string(gitEmail)).Run()
		} else {
			exec.Command("dolt", "config", "--global", "--add", "user.email", "test@test.com").Run()
		}
	}
}
