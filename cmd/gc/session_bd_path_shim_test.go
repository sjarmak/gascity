package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureSessionBdPathShimWritesExecutableShimExecingGcBd(t *testing.T) {
	clearGCEnv(t)
	city := t.TempDir()

	gcExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable(): %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(gcExe); err == nil && resolved != "" {
		gcExe = resolved
	}

	if err := ensureSessionBdPathShim(city); err != nil {
		t.Fatalf("ensureSessionBdPathShim: %v", err)
	}

	shimPath := sessionBdPathShimScriptPath(city)
	if filepath.Base(shimPath) != "bd" {
		t.Fatalf("shim script must be named bd (so PATH lookup finds it), got %q", shimPath)
	}

	info, err := os.Stat(shimPath)
	if err != nil {
		t.Fatalf("Stat(shim): %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("shim not executable: mode %v", info.Mode())
	}

	content, err := os.ReadFile(shimPath)
	if err != nil {
		t.Fatalf("ReadFile(shim): %v", err)
	}
	if !strings.HasPrefix(string(content), "#!/bin/sh") {
		t.Errorf("shim missing shebang:\n%s", content)
	}
	if !strings.Contains(string(content), gcExe) {
		t.Errorf("shim does not exec current gc binary %s:\n%s", gcExe, content)
	}
	if !strings.Contains(string(content), `bd "$@"`) {
		t.Errorf("shim does not delegate to `gc bd \"$@\"`:\n%s", content)
	}

	// Idempotence: unchanged content is not rewritten.
	before := info.ModTime()
	if err := ensureSessionBdPathShim(city); err != nil {
		t.Fatalf("ensureSessionBdPathShim (second call): %v", err)
	}
	after, err := os.Stat(shimPath)
	if err != nil {
		t.Fatalf("Stat(shim) after second call: %v", err)
	}
	if !after.ModTime().Equal(before) {
		t.Errorf("unchanged shim was rewritten: modtime %s -> %s", before, after.ModTime())
	}
}

func TestEnsureSessionBdPathShimSkipsForNonBdCity(t *testing.T) {
	clearGCEnv(t)
	city := t.TempDir()
	if err := os.WriteFile(filepath.Join(city, "city.toml"), []byte("[beads]\nprovider = \"file\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ensureSessionBdPathShim(city); err != nil {
		t.Fatalf("ensureSessionBdPathShim: %v", err)
	}

	if _, err := os.Stat(sessionBdPathShimScriptPath(city)); !os.IsNotExist(err) {
		t.Errorf("stat session bd path shim err = %v, want IsNotExist for non-bd city", err)
	}
}

// TestSessionBdPathShimSurvivesDoltPortRotation is the direct regression test
// for gastownhall/gascity#792 / gc-end7: the shim script exec target is the
// gc binary, not a Dolt endpoint, so it is unaffected by a managed Dolt port
// rotation (state file port A -> B). Every invocation instead re-resolves the
// canonical endpoint inside `gc bd`, whose own port-drift handling is covered
// exhaustively by bd_env_test.go. This test only pins the shim's own
// contract: its content never bakes in a port/host, so rotation cannot make
// it stale.
func TestSessionBdPathShimSurvivesDoltPortRotation(t *testing.T) {
	clearGCEnv(t)
	city := t.TempDir()

	if err := ensureSessionBdPathShim(city); err != nil {
		t.Fatalf("ensureSessionBdPathShim: %v", err)
	}
	before, err := os.ReadFile(sessionBdPathShimScriptPath(city))
	if err != nil {
		t.Fatalf("ReadFile(shim): %v", err)
	}
	if strings.Contains(string(before), "29620") || strings.Contains(string(before), "29621") {
		t.Fatalf("shim must not bake in a Dolt port:\n%s", before)
	}

	// Simulate a canonical port rotation: nothing about the shim depends on
	// it, so regenerating leaves the content byte-identical.
	if err := ensureSessionBdPathShim(city); err != nil {
		t.Fatalf("ensureSessionBdPathShim (post-rotation): %v", err)
	}
	after, err := os.ReadFile(sessionBdPathShimScriptPath(city))
	if err != nil {
		t.Fatalf("ReadFile(shim) after rotation: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("shim content changed across a simulated port rotation:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
