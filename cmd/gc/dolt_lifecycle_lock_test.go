package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestResolveManagedDoltRuntimeLayoutLifecycleLockDistinctFromScriptLock pins
// the path-distinctness invariant introduced for gastownhall/gascity#2130.
//
// The gc binary's in-process lifecycle lock (acquired by both
// startManagedDoltProcess and recoverManagedDoltProcess via
// openManagedDoltLifecycleLock) MUST live at a different filesystem path
// from the shell-side flock used by gc-beads-bd.sh's op_start (which the
// script broadcasts via the GC_DOLT_LOCK_FILE env var and locks at
// `exec 9>"$LOCK_FILE"; flock -n 9`). When the script invokes
// `gc dolt-state start-managed` as a subprocess, the script holds an
// exclusive flock on LockFile; if gc also flock'd LockFile, the subprocess
// would deadlock-wait against its own parent until the lifecycle timeout
// fires with "timed out waiting for concurrent managed dolt lifecycle to
// finish". The two locks must use distinct paths so they compose.
func TestResolveManagedDoltRuntimeLayoutLifecycleLockDistinctFromScriptLock(t *testing.T) {
	layout, err := resolveManagedDoltRuntimeLayout(t.TempDir())
	if err != nil {
		t.Fatalf("resolveManagedDoltRuntimeLayout: %v", err)
	}
	if layout.LockFile == "" {
		t.Fatal("layout.LockFile is empty")
	}
	if layout.LifecycleLockFile == "" {
		t.Fatal("layout.LifecycleLockFile is empty")
	}
	if layout.LockFile == layout.LifecycleLockFile {
		t.Fatalf("layout.LockFile == layout.LifecycleLockFile (%s); gc would deadlock against gc-beads-bd.sh's op_start flock on this path (gastownhall/gascity#2130)", layout.LockFile)
	}
	if filepath.Dir(layout.LockFile) != filepath.Dir(layout.LifecycleLockFile) {
		t.Errorf("expected both lock files under the same pack-state dir, got %s vs %s", layout.LockFile, layout.LifecycleLockFile)
	}
}

// TestOpenManagedDoltLifecycleLockDoesNotCollideWithScriptFlock pins the
// behavioral invariant: holding an exclusive flock on layout.LockFile (the
// shape gc-beads-bd.sh's op_start uses) must NOT block
// openManagedDoltLifecycleLock from acquiring the in-process gc lifecycle
// lock. This is the regression that broke 10/12 cmd/gc process shards in
// PR #2296 — the bash script invoked `gc dolt-state start-managed` from
// inside its own flock'd block, and the gc binary's lifecycle lock
// (then sharing the script's path) blocked for the full timeout.
func TestOpenManagedDoltLifecycleLockDoesNotCollideWithScriptFlock(t *testing.T) {
	cityPath := t.TempDir()
	layout, err := resolveManagedDoltRuntimeLayout(cityPath)
	if err != nil {
		t.Fatalf("resolveManagedDoltRuntimeLayout: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(layout.LockFile), 0o755); err != nil {
		t.Fatalf("MkdirAll(layout.LockFile dir): %v", err)
	}

	scriptFlock, err := os.OpenFile(layout.LockFile, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open script lock file: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Flock(int(scriptFlock.Fd()), syscall.LOCK_UN)
		_ = scriptFlock.Close()
	})
	if err := syscall.Flock(int(scriptFlock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock script lock: %v", err)
	}

	gcLock, _, err := openManagedDoltLifecycleLock(cityPath)
	if err != nil {
		t.Fatalf("openManagedDoltLifecycleLock: %v", err)
	}
	t.Cleanup(func() { _ = gcLock.Close() })
	locked, err := tryManagedDoltLifecycleLock(gcLock)
	if err != nil {
		t.Fatalf("tryManagedDoltLifecycleLock: %v", err)
	}
	if !locked {
		t.Fatal("gc lifecycle lock blocked by script flock on layout.LockFile (regression: gastownhall/gascity#2130 — the two locks must use distinct paths)")
	}
	releaseManagedDoltLifecycleLock(gcLock)
}
