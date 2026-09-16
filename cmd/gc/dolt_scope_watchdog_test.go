package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestManagedDoltScopeGone(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "dolt-config.yaml")
	if err := os.WriteFile(existing, []byte("log_level: warning\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		configFile string
		want       bool
	}{
		{"existing config is alive", existing, false},
		{"missing config is gone", filepath.Join(dir, "removed", "dolt-config.yaml"), true},
		{"empty path never reaps", "", false},
		{"blank path never reaps", "   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedDoltScopeGone(tc.configFile); got != tc.want {
				t.Errorf("managedDoltScopeGone(%q) = %v, want %v", tc.configFile, got, tc.want)
			}
		})
	}
}

func TestManagedDoltScopeWatchdogEnabledFor(t *testing.T) {
	cases := []struct {
		name     string
		testMode bool
		env      string
		want     bool
	}{
		{"production default on", false, "", true},
		{"production explicit off", false, "0", false},
		{"production explicit on", false, "1", true},
		{"test mode always off", true, "", false},
		{"test mode off even when forced", true, "1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedDoltScopeWatchdogEnabledFor(tc.testMode, tc.env); got != tc.want {
				t.Errorf("managedDoltScopeWatchdogEnabledFor(%v, %q) = %v, want %v", tc.testMode, tc.env, got, tc.want)
			}
		})
	}
}

func TestManagedDoltScopeWatchdogEnabled_OffInTestBinary(t *testing.T) {
	// The test binary is always in managed-dolt test mode, so the scope
	// watchdog must never interpose on test-spawned servers.
	if managedDoltScopeWatchdogEnabled() {
		t.Fatal("scope watchdog enabled inside the test binary; test scopes are owned by the test watchdog")
	}
}

func TestManagedDoltScopeWatchdogInterval(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
	}{
		{"", managedDoltScopeWatchdogDefaultInterval},
		{"50", 50 * time.Millisecond},
		{"0", managedDoltScopeWatchdogDefaultInterval},
		{"-5", managedDoltScopeWatchdogDefaultInterval},
		{"nonsense", managedDoltScopeWatchdogDefaultInterval},
	}
	for _, tc := range cases {
		t.Run("env="+tc.env, func(t *testing.T) {
			t.Setenv(managedDoltScopeWatchdogIntervalEnv, tc.env)
			if got := managedDoltScopeWatchdogInterval(); got != tc.want {
				t.Errorf("interval for %q = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

// TestManagedDoltScopeWatchdogKillsServerWhenScopeDeleted exercises the full
// production supervision loop: a helper process starts a fake dolt server
// under the scope watchdog, the test deletes the config file (the scope
// anchor), and the watchdog must terminate the server after the two-check
// confirmation window.
func TestManagedDoltScopeWatchdogKillsServerWhenScopeDeleted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process semantics required")
	}
	dir := t.TempDir()
	fakeDoltDir := writeFakeDoltSQLServer(t)
	statePath := filepath.Join(dir, "state")
	configPath := filepath.Join(dir, "dolt-config.yaml")
	logPath := filepath.Join(dir, "dolt.log")
	if err := os.WriteFile(configPath, []byte("log_level: debug\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestManagedDoltScopeWatchdogHelper", "-test.v")
	cmd.Env = sanitizedBaseEnv(
		"GC_TEST_MANAGED_DOLT_HELPER=scope-watchdog",
		"GC_TEST_MANAGED_DOLT_HELPER_STATE="+statePath,
		"GC_TEST_MANAGED_DOLT_HELPER_CONFIG="+configPath,
		"GC_TEST_MANAGED_DOLT_HELPER_LOG="+logPath,
		"GC_TEST_MANAGED_DOLT_HELPER_FAKE_DOLT_DIR="+fakeDoltDir,
		// TestMain scrubs non-GC_TEST_ GC_* keys, so the interval rides a
		// GC_TEST_ control var and the helper re-exports it for the watchdog.
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_INTERVAL_MS=50",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, output)
	}
	doltPID, watchdogPID := readManagedDoltTestState(t, statePath)
	t.Cleanup(func() {
		cleanupManagedDoltTestPID(t, doltPID)
		cleanupManagedDoltTestPID(t, watchdogPID)
	})

	// Control window: with the config present, the server must stay alive
	// across several poll intervals — and so must its watchdog (the spawner
	// helper has already exited, the production lifecycle shape).
	time.Sleep(300 * time.Millisecond)
	if !pidAlive(doltPID) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("fake dolt pid %d exited while scope was alive; helper output:\n%s\nwatchdog log:\n%s", doltPID, output, logData)
	}
	if !pidAlive(watchdogPID) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("watchdog pid %d died while scope was alive; watchdog log:\n%s", watchdogPID, logData)
	}

	// Delete the scope anchor; the watchdog should confirm twice and reap.
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for pidAlive(doltPID) {
		if time.Now().After(deadline) {
			logData, _ := os.ReadFile(logPath)
			t.Fatalf("fake dolt pid %d still alive after scope deletion; watchdog log:\n%s", doltPID, logData)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for pidAlive(watchdogPID) {
		if time.Now().After(deadline) {
			t.Fatalf("watchdog pid %d still alive after reaping its server", watchdogPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	logData, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logData), "gone for") {
		t.Errorf("watchdog log missing the scope-gone termination decision; log:\n%s", logData)
	}
}

// TestManagedDoltScopeWatchdogHelper runs in a child process: it starts a
// fake dolt server under the scope watchdog and records both PIDs, then
// exits — proving the watchdog supervises independently of its spawner,
// exactly the production lifecycle (gc exits, the watchdog stays).
func TestManagedDoltScopeWatchdogHelper(t *testing.T) {
	if os.Getenv("GC_TEST_MANAGED_DOLT_HELPER") != "scope-watchdog" {
		t.Skip("helper process only")
	}
	fakeDoltDir := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_FAKE_DOLT_DIR"))
	if fakeDoltDir == "" {
		t.Fatal("missing fake dolt dir")
	}
	t.Setenv("PATH", fakeDoltDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if interval := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_INTERVAL_MS")); interval != "" {
		t.Setenv(managedDoltScopeWatchdogIntervalEnv, interval)
	}
	if idleWindow := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_IDLE_WINDOW_MS")); idleWindow != "" {
		t.Setenv(managedDoltScopeIdleWindowEnv, idleWindow)
	}
	statePath := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_STATE"))
	configPath := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_CONFIG"))
	logPath := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_LOG"))
	cityPath := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_CITY_PATH"))
	if cityPath == "EPHEMERAL" {
		// Computed here, inside the child, rather than passed a literal path
		// from the parent: TestMain re-adopts a fresh per-process TMPDIR on
		// re-exec (adoptPerRunTMPDIR), so a path the parent considered
		// temp-rooted is not necessarily temp-rooted under THIS process's
		// os.TempDir(). Only this cityPath argument needs to satisfy the
		// ephemeral gate; configPath/logPath/statePath are unaffected and
		// stay wherever the parent put them.
		cityPath = filepath.Join(os.TempDir(), "gc-idle-scope-test")
	}
	if statePath == "" || configPath == "" || logPath == "" {
		t.Fatal("missing helper paths")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer logFile.Close() //nolint:errcheck

	started, err := startManagedDoltSQLServerWithScopeWatchdog(cityPath, configPath, logPath, logFile)
	if err != nil {
		t.Fatalf("start managed dolt with scope watchdog: %v", err)
	}
	state := fmt.Sprintf("%d %d\n", started.PID, started.WatchdogPID)
	if err := os.WriteFile(statePath, []byte(state), 0o644); err != nil {
		t.Fatalf("write helper state: %v", err)
	}
	// Opt-in: record the reported start identity so a caller test can assert the
	// scope-watchdog path populates it (the PR #4004 PID-reuse guard input).
	// Two lines: start-time ticks, then the ps-lstart identity (possibly empty).
	if identityPath := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_IDENTITY")); identityPath != "" {
		identity := fmt.Sprintf("%d\n%s\n", started.StartTimeTicks, started.StartIdentity)
		if err := os.WriteFile(identityPath, []byte(identity), 0o644); err != nil {
			t.Fatalf("write helper identity: %v", err)
		}
	}
}

// readManagedDoltScopeIdentityState parses the two-line identity file the scope
// watchdog helper writes when GC_TEST_MANAGED_DOLT_HELPER_IDENTITY is set:
// start-time ticks on line 1, the ps-lstart identity (possibly empty) on line 2.
func readManagedDoltScopeIdentityState(t *testing.T, path string) (uint64, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read helper identity: %v", err)
	}
	lines := strings.SplitN(strings.TrimRight(string(data), "\n"), "\n", 2)
	ticks, err := strconv.ParseUint(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		t.Fatalf("parse helper identity ticks %q: %v", lines[0], err)
	}
	identity := ""
	if len(lines) >= 2 {
		identity = lines[1]
	}
	return ticks, identity
}

// TestManagedDoltScopeWatchdogReportsStartIdentity is the PR #4004 F1 regression
// for the production scope-watchdog path: the returned managedDoltStartedProcess
// must carry the dolt child's OS start identity, snapshotted by the watchdog
// before it can reap the child. Without it the startup-failure cleanup guard
// (terminateManagedDoltStartedProcess) falls through to unconditional bare-PID
// signaling and can kill an unrelated process that reused the numeric PID.
func TestManagedDoltScopeWatchdogReportsStartIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process semantics required")
	}
	dir := t.TempDir()
	fakeDoltDir := writeFakeDoltSQLServer(t)
	statePath := filepath.Join(dir, "state")
	identityPath := filepath.Join(dir, "identity")
	configPath := filepath.Join(dir, "dolt-config.yaml")
	logPath := filepath.Join(dir, "dolt.log")
	if err := os.WriteFile(configPath, []byte("log_level: debug\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestManagedDoltScopeWatchdogHelper", "-test.v")
	cmd.Env = sanitizedBaseEnv(
		"GC_TEST_MANAGED_DOLT_HELPER=scope-watchdog",
		"GC_TEST_MANAGED_DOLT_HELPER_STATE="+statePath,
		"GC_TEST_MANAGED_DOLT_HELPER_IDENTITY="+identityPath,
		"GC_TEST_MANAGED_DOLT_HELPER_CONFIG="+configPath,
		"GC_TEST_MANAGED_DOLT_HELPER_LOG="+logPath,
		"GC_TEST_MANAGED_DOLT_HELPER_FAKE_DOLT_DIR="+fakeDoltDir,
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_INTERVAL_MS=50",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, output)
	}
	doltPID, watchdogPID := readManagedDoltTestState(t, statePath)
	t.Cleanup(func() {
		cleanupManagedDoltTestPID(t, doltPID)
		cleanupManagedDoltTestPID(t, watchdogPID)
	})

	ticks, identity := readManagedDoltScopeIdentityState(t, identityPath)
	if ticks == 0 && identity == "" {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("scope watchdog reported no start identity (ticks=%d identity=%q); PID-reuse guard disabled; log:\n%s", ticks, identity, logData)
	}
}

// TestManagedDoltScopeWatchdogServerSurvivesScopePresent asserts the
// watchdog never reaps a server whose scope stays on disk, and exits
// cleanly when the server itself goes away.
func TestManagedDoltScopeWatchdogServerSurvivesScopePresent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process semantics required")
	}
	dir := t.TempDir()
	fakeDoltDir := writeFakeDoltSQLServer(t)
	statePath := filepath.Join(dir, "state")
	configPath := filepath.Join(dir, "dolt-config.yaml")
	logPath := filepath.Join(dir, "dolt.log")
	if err := os.WriteFile(configPath, []byte("log_level: debug\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestManagedDoltScopeWatchdogHelper", "-test.v")
	cmd.Env = sanitizedBaseEnv(
		"GC_TEST_MANAGED_DOLT_HELPER=scope-watchdog",
		"GC_TEST_MANAGED_DOLT_HELPER_STATE="+statePath,
		"GC_TEST_MANAGED_DOLT_HELPER_CONFIG="+configPath,
		"GC_TEST_MANAGED_DOLT_HELPER_LOG="+logPath,
		"GC_TEST_MANAGED_DOLT_HELPER_FAKE_DOLT_DIR="+fakeDoltDir,
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_INTERVAL_MS=50",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, output)
	}
	doltPID, watchdogPID := readManagedDoltTestState(t, statePath)
	t.Cleanup(func() {
		cleanupManagedDoltTestPID(t, doltPID)
		cleanupManagedDoltTestPID(t, watchdogPID)
	})

	time.Sleep(300 * time.Millisecond)
	if !pidAlive(doltPID) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("fake dolt pid %d reaped while scope present; watchdog log:\n%s", doltPID, logData)
	}
	if !pidAlive(watchdogPID) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("watchdog pid %d died while scope present; watchdog log:\n%s", watchdogPID, logData)
	}

	// Kill the server directly (the `gc stop` shape); the watchdog must
	// notice and exit instead of lingering.
	proc, err := os.FindProcess(doltPID)
	if err != nil {
		t.Fatalf("find dolt pid: %v", err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatalf("kill dolt pid: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for pidAlive(watchdogPID) {
		if time.Now().After(deadline) {
			t.Fatalf("watchdog pid %d still alive after its server exited", watchdogPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRunManagedDoltScopeWatchdogUsage pins the argv contract.
func TestRunManagedDoltScopeWatchdogUsage(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close() //nolint:errcheck
	if code := runManagedDoltScopeWatchdog(nil, devnull, devnull); code != 2 {
		t.Errorf("no args exit = %d, want 2", code)
	}
	if code := runManagedDoltScopeWatchdog([]string{"a", "b"}, devnull, devnull); code != 2 {
		t.Errorf("two args exit = %d, want 2", code)
	}
	if code := runManagedDoltScopeWatchdog([]string{" ", "log", "city"}, devnull, devnull); code != 2 {
		t.Errorf("blank config exit = %d, want 2", code)
	}
}

// TestTerminateManagedDoltScopeWatchdogChildSkipsReusedPID is the PR #4004
// completeness regression for the watchdog's own reap path: the scope-gone and
// signal-forward branches terminate the dolt child through
// terminateManagedDoltScopeWatchdogChild, which must skip the signal when the
// child's numeric PID was reaped and reused (identity mismatch) while still
// terminating a child whose start identity still matches. Without the guard the
// production scope reap could SIGKILL an unrelated process that reused the PID.
func TestTerminateManagedDoltScopeWatchdogChildSkipsReusedPID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal semantics required")
	}

	// The live re-read is mocked to a fixed identity (3333); the guard compares
	// each snapshot against it, exactly as the watchdog runloop does.
	oldTicks := managedDoltTestReadStartTimeTicks
	oldIdent := managedDoltTestReadStartIdentity
	managedDoltTestReadStartTimeTicks = func(int) uint64 { return 3333 }
	managedDoltTestReadStartIdentity = func(int) string { return "" }
	t.Cleanup(func() {
		managedDoltTestReadStartTimeTicks = oldTicks
		managedDoltTestReadStartIdentity = oldIdent
	})

	// Matching snapshot (3333 == mocked re-read): the child is signaled and a
	// sleep dies on SIGTERM (a zombie reads as not-alive).
	matching := exec.Command("sleep", "60")
	if err := matching.Start(); err != nil {
		t.Fatalf("start matching child: %v", err)
	}
	matchingPID := matching.Process.Pid
	t.Cleanup(func() {
		_ = matching.Process.Kill()
		_ = matching.Wait()
	})
	if err := terminateManagedDoltScopeWatchdogChild("", matchingPID, 3333, ""); err != nil {
		t.Fatalf("guarded terminate of matching child: %v", err)
	}
	if pidAlive(matchingPID) {
		t.Fatalf("watchdog reap did not signal matching dolt child pid %d", matchingPID)
	}

	// Reused snapshot (1111 != mocked re-read 3333): the PID was reaped and the
	// number reused, so the guard must leave the live process untouched.
	reused := exec.Command("sleep", "60")
	if err := reused.Start(); err != nil {
		t.Fatalf("start reused child: %v", err)
	}
	reusedPID := reused.Process.Pid
	t.Cleanup(func() {
		_ = reused.Process.Kill()
		_ = reused.Wait()
	})
	if err := terminateManagedDoltScopeWatchdogChild("", reusedPID, 1111, ""); err != nil {
		t.Fatalf("guarded terminate of reused child: %v", err)
	}
	// Give any erroneous SIGTERM time to land before asserting survival.
	time.Sleep(200 * time.Millisecond)
	if !pidAlive(reusedPID) {
		t.Fatalf("watchdog reap signaled reused dolt child pid %d; identity guard not enforced", reusedPID)
	}
}

func TestConfiguredListenerPort(t *testing.T) {
	dir := t.TempDir()
	writeConfig := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(dir, fmt.Sprintf("config-%d.yaml", time.Now().UnixNano()))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		return path
	}

	cases := []struct {
		name     string
		content  string
		wantPort uint16
		wantOK   bool
	}{
		{"well-formed listener block", "log_level: warning\n\nlistener:\n  port: 3306\n  host: 127.0.0.1\n", 3306, true},
		{"no port line", "log_level: warning\n\nlistener:\n  host: 127.0.0.1\n", 0, false},
		{"malformed port value", "listener:\n  port: not-a-number\n", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.content)
			gotPort, gotOK := configuredListenerPort(path)
			if gotPort != tc.wantPort || gotOK != tc.wantOK {
				t.Errorf("configuredListenerPort() = (%d, %v), want (%d, %v)", gotPort, gotOK, tc.wantPort, tc.wantOK)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		gotPort, gotOK := configuredListenerPort(filepath.Join(dir, "does-not-exist.yaml"))
		if gotOK || gotPort != 0 {
			t.Errorf("configuredListenerPort(missing) = (%d, %v), want (0, false)", gotPort, gotOK)
		}
	})
}

// listenerPort starts a real TCP listener on 127.0.0.1 and returns its
// numeric port, so establishedConnectionCount reads real kernel state rather
// than a mock.
func listenerPort(t *testing.T) (net.Listener, uint16) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener addr type %T", ln.Addr())
	}
	return ln, uint16(addr.Port)
}

func TestEstablishedConnectionCount(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/net/tcp")
	}
	ln, port := listenerPort(t)
	defer ln.Close() //nolint:errcheck

	if count, checked := establishedConnectionCount(port); !checked || count != 0 {
		t.Fatalf("established count before any client = (%d, checked=%v), want (0, true)", count, checked)
	}

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close() //nolint:errcheck
	server := <-accepted
	defer server.Close() //nolint:errcheck

	count, checked := establishedConnectionCount(port)
	if !checked || count < 1 {
		t.Fatalf("established count with one open client = (%d, checked=%v), want (>=1, true)", count, checked)
	}

	client.Close() //nolint:errcheck
	server.Close() //nolint:errcheck
	deadline := time.Now().Add(5 * time.Second)
	for {
		count, checked := establishedConnectionCount(port)
		if checked && count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("established count did not drain to zero after close: count=%d checked=%v", count, checked)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestManagedDoltScopeRootEphemeral(t *testing.T) {
	tempDir := filepath.Clean(os.TempDir())
	cases := []struct {
		name     string
		cityPath string
		want     bool
	}{
		{"empty path never ephemeral", "", false},
		{"exact temp dir is ephemeral", tempDir, true},
		{"child of temp dir is ephemeral", filepath.Join(tempDir, "gc-scratch-123"), true},
		{"sibling prefix is not ephemeral", tempDir + "-not-actually-temp", false},
		{"unrelated absolute path is not ephemeral", "/opt/gascity/city", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedDoltScopeRootEphemeral(tc.cityPath); got != tc.want {
				t.Errorf("managedDoltScopeRootEphemeral(%q) = %v, want %v", tc.cityPath, got, tc.want)
			}
		})
	}
}

func TestManagedDoltScopeHasZeroConnections(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/net/tcp")
	}
	dir := t.TempDir()

	t.Run("unreadable config is unknown", func(t *testing.T) {
		zero, known := managedDoltScopeHasZeroConnections(filepath.Join(dir, "missing.yaml"))
		if known || zero {
			t.Errorf("managedDoltScopeHasZeroConnections(missing) = (%v, %v), want (false, false)", zero, known)
		}
	})

	t.Run("known port with no clients reports zero", func(t *testing.T) {
		ln, port := listenerPort(t)
		defer ln.Close() //nolint:errcheck
		configPath := filepath.Join(dir, "zero-conn.yaml")
		content := fmt.Sprintf("listener:\n  port: %d\n", port)
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		zero, known := managedDoltScopeHasZeroConnections(configPath)
		if !known || !zero {
			t.Errorf("managedDoltScopeHasZeroConnections(idle port) = (%v, %v), want (true, true)", zero, known)
		}
	})
}

// TestManagedDoltScopeWatchdogIdleAbandonmentTempRoot is the #4679 regression:
// a temp-rooted scope whose managed dolt server observes zero established
// client connections for the idle window must be reaped even though its
// --config file still exists, and an active connection must block the reap.
func TestManagedDoltScopeWatchdogIdleAbandonmentTempRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/net/tcp")
	}
	cityDir := t.TempDir() // under os.TempDir(): the idle-abandonment gate.
	fakeDoltDir := writeFakeDoltSQLServer(t)
	statePath := filepath.Join(cityDir, "state")
	logPath := filepath.Join(cityDir, "dolt.log")

	ln, port := listenerPort(t)
	defer ln.Close() //nolint:errcheck
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close() //nolint:errcheck
	server := <-accepted
	defer server.Close() //nolint:errcheck

	configPath := filepath.Join(cityDir, "dolt-config.yaml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("listener:\n  port: %d\n", port)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestManagedDoltScopeWatchdogHelper", "-test.v")
	cmd.Env = sanitizedBaseEnv(
		"GC_TEST_MANAGED_DOLT_HELPER=scope-watchdog",
		"GC_TEST_MANAGED_DOLT_HELPER_STATE="+statePath,
		"GC_TEST_MANAGED_DOLT_HELPER_CONFIG="+configPath,
		"GC_TEST_MANAGED_DOLT_HELPER_LOG="+logPath,
		"GC_TEST_MANAGED_DOLT_HELPER_FAKE_DOLT_DIR="+fakeDoltDir,
		"GC_TEST_MANAGED_DOLT_HELPER_CITY_PATH=EPHEMERAL",
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_INTERVAL_MS=20",
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_IDLE_WINDOW_MS=150",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, output)
	}
	doltPID, watchdogPID := readManagedDoltTestState(t, statePath)
	t.Cleanup(func() {
		cleanupManagedDoltTestPID(t, doltPID)
		cleanupManagedDoltTestPID(t, watchdogPID)
	})

	// Control window: an open client connection must keep the server alive
	// past the idle window even though the scope root is temp-rooted.
	time.Sleep(300 * time.Millisecond)
	if !pidAlive(doltPID) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("fake dolt pid %d reaped while a client connection was open; watchdog log:\n%s", doltPID, logData)
	}

	// Close the client; the watchdog must observe zero connections for the
	// idle window and reap.
	client.Close() //nolint:errcheck
	server.Close() //nolint:errcheck
	deadline := time.Now().Add(10 * time.Second)
	for pidAlive(doltPID) {
		if time.Now().After(deadline) {
			logData, _ := os.ReadFile(logPath)
			t.Fatalf("fake dolt pid %d still alive after idle window elapsed; watchdog log:\n%s", doltPID, logData)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for pidAlive(watchdogPID) {
		if time.Now().After(deadline) {
			t.Fatalf("watchdog pid %d still alive after reaping its idle server", watchdogPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	logData, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logData), "idle with zero client connections") {
		t.Errorf("watchdog log missing the idle-abandonment termination decision; log:\n%s", logData)
	}
}

// TestManagedDoltScopeWatchdogNeverIdleReapsNonTempRoot pins the gate: a
// non-temp-rooted scope (the canonical city) must never be reaped by the
// idle-abandonment leg even with zero connections and a tiny idle window --
// only the pre-existing deleted-config leg applies to it.
func TestManagedDoltScopeWatchdogNeverIdleReapsNonTempRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/net/tcp")
	}
	dir := t.TempDir()
	fakeDoltDir := writeFakeDoltSQLServer(t)
	statePath := filepath.Join(dir, "state")
	logPath := filepath.Join(dir, "dolt.log")

	ln, port := listenerPort(t)
	defer ln.Close() //nolint:errcheck

	configPath := filepath.Join(dir, "dolt-config.yaml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("listener:\n  port: %d\n", port)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestManagedDoltScopeWatchdogHelper", "-test.v")
	cmd.Env = sanitizedBaseEnv(
		"GC_TEST_MANAGED_DOLT_HELPER=scope-watchdog",
		"GC_TEST_MANAGED_DOLT_HELPER_STATE="+statePath,
		"GC_TEST_MANAGED_DOLT_HELPER_CONFIG="+configPath,
		"GC_TEST_MANAGED_DOLT_HELPER_LOG="+logPath,
		"GC_TEST_MANAGED_DOLT_HELPER_FAKE_DOLT_DIR="+fakeDoltDir,
		// GC_TEST_MANAGED_DOLT_HELPER_CITY_PATH intentionally omitted: the
		// helper defaults cityPath to "", the non-ephemeral production shape
		// for a canonical city that is never under os.TempDir().
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_INTERVAL_MS=20",
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_IDLE_WINDOW_MS=50",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, output)
	}
	doltPID, watchdogPID := readManagedDoltTestState(t, statePath)
	t.Cleanup(func() {
		cleanupManagedDoltTestPID(t, doltPID)
		cleanupManagedDoltTestPID(t, watchdogPID)
	})

	// Zero connections the whole time, idle window is tiny -- if the gate
	// were missing this would reap well within this sleep.
	time.Sleep(400 * time.Millisecond)
	if !pidAlive(doltPID) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("fake dolt pid %d reaped for idleness on a non-temp-rooted scope; watchdog log:\n%s", doltPID, logData)
	}
	if !pidAlive(watchdogPID) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("watchdog pid %d died on a non-temp-rooted scope; watchdog log:\n%s", watchdogPID, logData)
	}
}
