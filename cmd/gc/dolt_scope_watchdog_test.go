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

	"github.com/gastownhall/gascity/internal/config"
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

func TestManagedDoltScopeWatchdogIdleWindow(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
	}{
		{"", managedDoltScopeWatchdogDefaultIdleWindow},
		{"250", 250 * time.Millisecond},
		{"0", 0}, // explicit disable
		{"-5", managedDoltScopeWatchdogDefaultIdleWindow},
		{"nonsense", managedDoltScopeWatchdogDefaultIdleWindow},
	}
	for _, tc := range cases {
		t.Run("env="+tc.env, func(t *testing.T) {
			t.Setenv(managedDoltScopeWatchdogIdleEnv, tc.env)
			if got := managedDoltScopeWatchdogIdleWindow(); got != tc.want {
				t.Errorf("idle window for %q = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

func TestManagedDoltScopeRootEphemeral(t *testing.T) {
	tempDir := t.TempDir()
	cases := []struct {
		name      string
		scopeRoot string
		want      bool
	}{
		{"under temp", filepath.Join(tempDir, "city-a"), true},
		{"equal to temp", tempDir, true},
		{"outside temp", "/opt/cities/prod", false},
		{"empty is non-ephemeral", "", false},
		{"blank is non-ephemeral", "   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedDoltScopeRootEphemeral(tc.scopeRoot, tempDir); got != tc.want {
				t.Errorf("managedDoltScopeRootEphemeral(%q, %q) = %v, want %v", tc.scopeRoot, tempDir, got, tc.want)
			}
		})
	}
}

func TestManagedDoltConfigListenerPort(t *testing.T) {
	dir := t.TempDir()
	// A realistic config produced by the production writer, so the parser is
	// pinned to the actual on-disk shape rather than a hand-rolled sample.
	realConfig := filepath.Join(dir, "real-dolt-config.yaml")
	if err := writeManagedDoltConfigFile(realConfig, "127.0.0.1", "50127", filepath.Join(dir, "data"), "warning", config.DoltConfig{}); err != nil {
		t.Fatalf("write managed config: %v", err)
	}

	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}
	quotedPort := write("quoted.yaml", "listener:\n  port: \"3307\"\n  host: 0.0.0.0\n")
	noListenerPort := write("noport.yaml", "log_level: warning\ndata_dir: \"/x\"\n")
	// A stray indented `port:` outside the listener block must be ignored.
	strayPort := write("stray.yaml", "other:\n  port: 9999\nlistener:\n  host: 127.0.0.1\n")

	cases := []struct {
		name       string
		configFile string
		wantPort   uint16
		wantOK     bool
	}{
		{"production writer output", realConfig, 50127, true},
		{"quoted port", quotedPort, 3307, true},
		{"no listener port", noListenerPort, 0, false},
		{"stray port outside listener block", strayPort, 0, false},
		{"missing file", filepath.Join(dir, "absent.yaml"), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port, ok := managedDoltConfigListenerPort(tc.configFile)
			if ok != tc.wantOK || port != tc.wantPort {
				t.Errorf("managedDoltConfigListenerPort(%q) = (%d, %v), want (%d, %v)", tc.configFile, port, ok, tc.wantPort, tc.wantOK)
			}
		})
	}
}

func TestManagedDoltIdleAbandonmentObserve(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)

	t.Run("zero window disables the leg", func(t *testing.T) {
		a := &managedDoltIdleAbandonment{window: 0}
		if a.observe(0, true, base) || a.observe(0, true, base.Add(time.Hour)) {
			t.Fatal("idle leg fired with a non-positive window")
		}
	})

	t.Run("sustained zero connections fires after the window", func(t *testing.T) {
		a := &managedDoltIdleAbandonment{window: time.Minute}
		if a.observe(0, true, base) {
			t.Fatal("fired on the first idle poll (window not yet elapsed)")
		}
		if a.observe(0, true, base.Add(30*time.Second)) {
			t.Fatal("fired before the window elapsed")
		}
		if !a.observe(0, true, base.Add(time.Minute)) {
			t.Fatal("did not fire once the idle window elapsed")
		}
	})

	t.Run("an established connection resets the idle span", func(t *testing.T) {
		a := &managedDoltIdleAbandonment{window: time.Minute}
		a.observe(0, true, base)
		if a.observe(2, true, base.Add(30*time.Second)) {
			t.Fatal("fired despite an active connection")
		}
		// The span restarts from the reset; the old start no longer counts.
		if a.observe(0, true, base.Add(90*time.Second)) {
			t.Fatal("fired using the pre-reset idle start")
		}
		if !a.observe(0, true, base.Add(150*time.Second)) {
			t.Fatal("did not fire a full window after the reset")
		}
	})

	t.Run("an unreadable /proc poll resets and never reaps", func(t *testing.T) {
		a := &managedDoltIdleAbandonment{window: time.Minute}
		a.observe(0, true, base)
		if a.observe(0, false, base.Add(30*time.Second)) {
			t.Fatal("fired on an unreadable /proc poll")
		}
		if a.observe(0, false, base.Add(2*time.Minute)) {
			t.Fatal("fired while /proc stayed unreadable")
		}
	})
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
	// TestMain scrubs non-GC_TEST_ GC_* keys, so the idle window rides a GC_TEST_
	// control var and the helper re-exports it for the watchdog re-exec.
	if idle := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_IDLE_MS")); idle != "" {
		t.Setenv(managedDoltScopeWatchdogIdleEnv, idle)
	}
	// The re-exec'd helper runs its own TestMain, which remaps TMPDIR to a
	// per-process dir, so os.TempDir() here differs from the parent test's.
	// When the parent asks for an ephemeral scope, create the scope root under
	// THIS process's os.TempDir() so managedDoltScopeRootEphemeral (which the
	// watchdog evaluates against the same os.TempDir()) sees it as temp-rooted.
	cityPath := ""
	if os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_CITY_MODE") == "temp" {
		root, err := os.MkdirTemp(os.TempDir(), "scope-idle-*")
		if err != nil {
			t.Fatalf("make temp scope root: %v", err)
		}
		cityPath = root
	}
	statePath := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_STATE"))
	configPath := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_CONFIG"))
	logPath := strings.TrimSpace(os.Getenv("GC_TEST_MANAGED_DOLT_HELPER_LOG"))
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

// freeLocalPort grabs a currently-unused TCP port. Nothing binds it after this
// returns, so no process holds an established connection on it — the idle-leg
// tests need a port whose established-connection count is provably zero.
func freeLocalPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close reservation listener: %v", err)
	}
	return port
}

// writeIdleLegConfig writes a managed-shaped config carrying a listener port so
// managedDoltConfigListenerPort resolves it and the idle-abandonment leg can arm.
func writeIdleLegConfig(t *testing.T, path string, port int) {
	t.Helper()
	content := fmt.Sprintf("log_level: debug\n\nlistener:\n  port: %d\n  host: 127.0.0.1\n", port)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestManagedDoltScopeWatchdogReapsIdleTempRootedScope exercises the #4679
// idle-abandonment leg end to end: a temp-rooted server with zero established
// client connections must be reaped once the idle window elapses, even though
// its scope directory (and --config anchor) still exist.
func TestManagedDoltScopeWatchdogReapsIdleTempRootedScope(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("idle-abandonment leg counts connections via /proc/net/tcp (Linux only)")
	}
	dir := t.TempDir() // under os.TempDir(): an ephemeral scope root
	fakeDoltDir := writeFakeDoltSQLServer(t)
	statePath := filepath.Join(dir, "state")
	configPath := filepath.Join(dir, "dolt-config.yaml")
	logPath := filepath.Join(dir, "dolt.log")
	writeIdleLegConfig(t, configPath, freeLocalPort(t))

	cmd := exec.Command(os.Args[0], "-test.run=TestManagedDoltScopeWatchdogHelper", "-test.v")
	cmd.Env = sanitizedBaseEnv(
		"GC_TEST_MANAGED_DOLT_HELPER=scope-watchdog",
		"GC_TEST_MANAGED_DOLT_HELPER_STATE="+statePath,
		"GC_TEST_MANAGED_DOLT_HELPER_CONFIG="+configPath,
		"GC_TEST_MANAGED_DOLT_HELPER_LOG="+logPath,
		"GC_TEST_MANAGED_DOLT_HELPER_FAKE_DOLT_DIR="+fakeDoltDir,
		"GC_TEST_MANAGED_DOLT_HELPER_CITY_MODE=temp",
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_INTERVAL_MS=50",
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_IDLE_MS=100",
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

	deadline := time.Now().Add(10 * time.Second)
	for pidAlive(doltPID) {
		if time.Now().After(deadline) {
			logData, _ := os.ReadFile(logPath)
			t.Fatalf("idle temp-rooted dolt pid %d not reaped; watchdog log:\n%s", doltPID, logData)
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
	if !strings.Contains(string(logData), "idle") {
		t.Errorf("watchdog log missing the idle-abandonment termination decision; log:\n%s", logData)
	}
}

// TestManagedDoltScopeWatchdogKeepsIdleNonTempScope pins the ephemeral gate: a
// non-temp-rooted scope (empty scope root here) with zero connections must NOT
// be reaped by the idle leg, even with the idle window shrunk to milliseconds.
// Only the deleted-config anchor governs non-temp roots.
func TestManagedDoltScopeWatchdogKeepsIdleNonTempScope(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("idle-abandonment leg counts connections via /proc/net/tcp (Linux only)")
	}
	dir := t.TempDir()
	fakeDoltDir := writeFakeDoltSQLServer(t)
	statePath := filepath.Join(dir, "state")
	configPath := filepath.Join(dir, "dolt-config.yaml")
	logPath := filepath.Join(dir, "dolt.log")
	writeIdleLegConfig(t, configPath, freeLocalPort(t))

	cmd := exec.Command(os.Args[0], "-test.run=TestManagedDoltScopeWatchdogHelper", "-test.v")
	cmd.Env = sanitizedBaseEnv(
		"GC_TEST_MANAGED_DOLT_HELPER=scope-watchdog",
		"GC_TEST_MANAGED_DOLT_HELPER_STATE="+statePath,
		"GC_TEST_MANAGED_DOLT_HELPER_CONFIG="+configPath,
		"GC_TEST_MANAGED_DOLT_HELPER_LOG="+logPath,
		"GC_TEST_MANAGED_DOLT_HELPER_FAKE_DOLT_DIR="+fakeDoltDir,
		// No GC_TEST_MANAGED_DOLT_HELPER_CITY: scope root is empty -> non-ephemeral.
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_INTERVAL_MS=50",
		"GC_TEST_MANAGED_DOLT_HELPER_SCOPE_WD_IDLE_MS=100",
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

	// Several idle windows must pass without a reap.
	time.Sleep(500 * time.Millisecond)
	if !pidAlive(doltPID) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("non-temp-rooted dolt pid %d reaped by idle leg; ephemeral gate failed; log:\n%s", doltPID, logData)
	}
	if !pidAlive(watchdogPID) {
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("watchdog pid %d died while non-temp scope idle; log:\n%s", watchdogPID, logData)
	}
	logData, _ := os.ReadFile(logPath)
	if strings.Contains(string(logData), "idle-abandonment armed") {
		t.Errorf("idle-abandonment armed for a non-temp-rooted scope; log:\n%s", logData)
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
