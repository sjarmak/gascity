package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestMaterializeBeadsBdScript(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}

	path, err := MaterializeBeadsBdScript(dir)
	if err != nil {
		t.Fatalf("MaterializeBeadsBdScript() error: %v", err)
	}

	// Check file exists.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	// Check it's executable.
	if info.Mode()&0o111 == 0 {
		t.Errorf("script is not executable: mode %v", info.Mode())
	}

	// Check content is non-empty and starts with shebang.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 100 {
		t.Errorf("script too small: %d bytes", len(data))
	}
	if string(data[:2]) != "#!" {
		t.Error("script doesn't start with shebang")
	}
}

// TestBeadsBdScript_K8sDoltEnvInheritance verifies that gc-beads-bd inherits
// GC_K8S_DOLT_HOST/PORT into GC_DOLT_HOST/PORT when the standard vars are
// unset. This is critical for K8s pods where buildPodEnv strips GC_DOLT_HOST
// and only injects GC_K8S_DOLT_HOST.
func TestBeadsBdScript_K8sDoltEnvInheritance(t *testing.T) {
	dir := t.TempDir()
	scriptPath, err := MaterializeBeadsBdScript(dir)
	if err != nil {
		t.Fatal(err)
	}

	// The "start" operation exits 2 immediately when is_remote() is true
	// (remote server — nothing to start locally). Without the K8s env var
	// inheritance fix, is_remote() returns false and the script tries to
	// start a local dolt server, exiting 1 (missing dolt/flock).
	cmd := exec.Command(scriptPath, "start")
	cmd.Env = []string{
		"GC_CITY_PATH=" + dir,
		"GC_K8S_DOLT_HOST=dolt.example.com",
		"GC_K8S_DOLT_PORT=3307",
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}
	// GC_DOLT_HOST intentionally NOT set — simulates K8s pod env.
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if exitCode != 2 {
		t.Errorf("gc-beads-bd start with GC_K8S_DOLT_HOST: exit %d, want 2 (remote detected)\noutput: %s", exitCode, out)
	}
}

// TestBeadsBdScript_EnsureReadyRemoteExits2 verifies that ensure-ready
// returns exit 2 (not needed) when a remote host is configured.
func TestBeadsBdScript_EnsureReadyRemoteExits2(t *testing.T) {
	dir := t.TempDir()
	scriptPath, err := MaterializeBeadsBdScript(dir)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(scriptPath, "ensure-ready")
	cmd.Env = []string{
		"GC_CITY_PATH=" + dir,
		"GC_DOLT_HOST=dolt.example.com",
		"GC_DOLT_PORT=3306",
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if exitCode != 2 {
		t.Errorf("ensure-ready with remote host: exit %d, want 2\noutput: %s", exitCode, out)
	}
}

// TestBeadsBdScript_EnsureReadyNotRunning verifies that ensure-ready exits 1
// with "not running" when no dolt server PID exists.
func TestBeadsBdScript_EnsureReadyNotRunning(t *testing.T) {
	dir := t.TempDir()
	scriptPath, err := MaterializeBeadsBdScript(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Use a dynamically allocated port to avoid flakiness if a hardcoded
	// port happens to be in use on the CI host.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	freePort := fmt.Sprintf("%d", ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()

	cmd := exec.Command(scriptPath, "ensure-ready")
	cmd.Env = []string{
		"GC_CITY_PATH=" + dir,
		"GC_DOLT_PORT=" + freePort,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if exitCode != 1 {
		t.Errorf("ensure-ready with no server: exit %d, want 1\noutput: %s", exitCode, out)
	}
	if !strings.Contains(string(out), "not running") {
		t.Errorf("expected 'not running' in output, got: %s", out)
	}
}

// TestBeadsBdScript_EnsureReadyNoPort verifies that ensure-ready exits 1
// with "no recorded port" when the PID is alive but state has no port.
func TestBeadsBdScript_EnsureReadyNoPort(t *testing.T) {
	dir := t.TempDir()
	scriptPath, err := MaterializeBeadsBdScript(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Set up pack state dir and write a PID file pointing at our own PID
	// (which is guaranteed alive).
	packDir := filepath.Join(dir, ".gc", "runtime", "packs", "dolt")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	selfPID := fmt.Sprintf("%d", os.Getpid())
	if err := os.WriteFile(filepath.Join(packDir, "dolt.pid"), []byte(selfPID), 0o644); err != nil {
		t.Fatal(err)
	}
	// State file with no port field.
	if err := os.WriteFile(filepath.Join(packDir, "dolt-state.json"), []byte(`{"running":true,"pid":`+selfPID+`}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(scriptPath, "ensure-ready")
	cmd.Env = []string{
		"GC_CITY_PATH=" + dir,
		"GC_DOLT_PORT=19999",
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if exitCode != 1 {
		t.Errorf("ensure-ready with no port in state: exit %d, want 1\noutput: %s", exitCode, out)
	}
	if !strings.Contains(string(out), "no recorded port") {
		t.Errorf("expected 'no recorded port' in output, got: %s", out)
	}
}

// TestBeadsBdScript_EnsureReadyTCPUnreachable verifies that ensure-ready
// exits 1 with "not reachable" when PID is alive and port is recorded
// but nothing is listening on that port.
func TestBeadsBdScript_EnsureReadyTCPUnreachable(t *testing.T) {
	dir := t.TempDir()
	scriptPath, err := MaterializeBeadsBdScript(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Use a port that nothing is listening on. Bind and immediately close
	// to get a known-free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	packDir := filepath.Join(dir, ".gc", "runtime", "packs", "dolt")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	selfPID := fmt.Sprintf("%d", os.Getpid())
	if err := os.WriteFile(filepath.Join(packDir, "dolt.pid"), []byte(selfPID), 0o644); err != nil {
		t.Fatal(err)
	}
	portStr := fmt.Sprintf("%d", port)
	stateJSON := fmt.Sprintf(`{"running":true,"pid":%s,"port":%s}`, selfPID, portStr)
	if err := os.WriteFile(filepath.Join(packDir, "dolt-state.json"), []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(scriptPath, "ensure-ready")
	cmd.Env = []string{
		"GC_CITY_PATH=" + dir,
		"GC_DOLT_PORT=" + portStr,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if exitCode != 1 {
		t.Errorf("ensure-ready with unreachable port: exit %d, want 1\noutput: %s", exitCode, out)
	}
	if !strings.Contains(string(out), "not reachable") {
		t.Errorf("expected 'not reachable' in output, got: %s", out)
	}
}

// TestBeadsBdScript_EnsureReadyTCPReachableQueryFails verifies that
// ensure-ready exits 1 when TCP is reachable but the query probe fails
// (server listening but not a dolt SQL server).
func TestBeadsBdScript_EnsureReadyTCPReachableQueryFails(t *testing.T) {
	dir := t.TempDir()
	scriptPath, err := MaterializeBeadsBdScript(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Start a TCP listener that accepts but doesn't speak MySQL/dolt protocol.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	// Accept and immediately close connections so nc -z succeeds but
	// dolt query probe fails.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	packDir := filepath.Join(dir, ".gc", "runtime", "packs", "dolt")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	selfPID := fmt.Sprintf("%d", os.Getpid())
	if err := os.WriteFile(filepath.Join(packDir, "dolt.pid"), []byte(selfPID), 0o644); err != nil {
		t.Fatal(err)
	}
	portStr := fmt.Sprintf("%d", port)
	stateJSON := fmt.Sprintf(`{"running":true,"pid":%s,"port":%s}`, selfPID, portStr)
	if err := os.WriteFile(filepath.Join(packDir, "dolt-state.json"), []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(scriptPath, "ensure-ready")
	cmd.Env = []string{
		"GC_CITY_PATH=" + dir,
		"GC_DOLT_PORT=" + portStr,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if exitCode != 1 {
		t.Errorf("ensure-ready with non-dolt listener: exit %d, want 1\noutput: %s", exitCode, out)
	}
	if !strings.Contains(string(out), "not answering queries") {
		t.Errorf("expected 'not answering queries' in output, got: %s", out)
	}
}

// TestBeadsBdScript_EnsureReadyNeverKills is the core regression test for #560.
// It verifies that ensure-ready NEVER kills a running process, even when
// TCP probes fail. Before the fix, op_ensure_ready aliased op_start which
// had a kill-9 branch on tcp_check_port failure.
func TestBeadsBdScript_EnsureReadyNeverKills(t *testing.T) {
	dir := t.TempDir()
	scriptPath, err := MaterializeBeadsBdScript(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Start a long-lived subprocess to act as a fake "dolt server".
	sleeper := exec.Command("sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sleeper.Process.Kill()
		_, _ = sleeper.Process.Wait() // Reap to avoid zombie.
	}()
	sleeperPID := fmt.Sprintf("%d", sleeper.Process.Pid)

	// Use a port nothing is listening on — this triggers the TCP failure
	// that previously caused op_start's kill-9 path.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	packDir := filepath.Join(dir, ".gc", "runtime", "packs", "dolt")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "dolt.pid"), []byte(sleeperPID), 0o644); err != nil {
		t.Fatal(err)
	}
	portStr := fmt.Sprintf("%d", port)
	stateJSON := fmt.Sprintf(`{"running":true,"pid":%s,"port":%s}`, sleeperPID, portStr)
	if err := os.WriteFile(filepath.Join(packDir, "dolt-state.json"), []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(scriptPath, "ensure-ready")
	cmd.Env = []string{
		"GC_CITY_PATH=" + dir,
		"GC_DOLT_PORT=" + portStr,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
	}
	_, _ = cmd.CombinedOutput() // Ignore exit code — we only care about the side effect.

	// The critical assertion: the sleeper process must still be alive.
	// Before the fix, op_ensure_ready → op_start → kill -9.
	if err := sleeper.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("regression #560: ensure-ready killed PID %s (process no longer alive)", sleeperPID)
	}
}

func TestMaterializeBeadsBdScript_idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}

	path1, err := MaterializeBeadsBdScript(dir)
	if err != nil {
		t.Fatal(err)
	}
	path2, err := MaterializeBeadsBdScript(dir)
	if err != nil {
		t.Fatal(err)
	}
	if path1 != path2 {
		t.Errorf("paths differ: %s != %s", path1, path2)
	}
}
