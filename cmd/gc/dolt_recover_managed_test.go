package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecoverManagedDoltExistingObserveTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "zero defaults to 5s", timeout: 0, want: 5 * time.Second},
		{name: "negative defaults to 5s", timeout: -1, want: 5 * time.Second},
		{name: "below 5s returns input", timeout: 2 * time.Second, want: 2 * time.Second},
		{name: "exactly 5s returns 5s", timeout: 5 * time.Second, want: 5 * time.Second},
		{name: "above 5s capped at 5s", timeout: 30 * time.Second, want: 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recoverManagedDoltExistingObserveTimeout(tt.timeout); got != tt.want {
				t.Errorf("recoverManagedDoltExistingObserveTimeout(%v) = %v, want %v", tt.timeout, got, tt.want)
			}
		})
	}
}

func TestRecoverManagedDoltShouldReuseExisting(t *testing.T) {
	tests := []struct {
		name          string
		existingPort  int
		requestedPort string
		want          bool
	}{
		{name: "zero port never reuses", existingPort: 0, requestedPort: "3306", want: false},
		{name: "negative port never reuses", existingPort: -1, requestedPort: "3306", want: false},
		{name: "empty requested always reuses", existingPort: 3306, requestedPort: "", want: true},
		{name: "whitespace requested always reuses", existingPort: 3306, requestedPort: "  ", want: true},
		{name: "different port reuses", existingPort: 3307, requestedPort: "3306", want: true},
		{name: "same port does not reuse", existingPort: 3306, requestedPort: "3306", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recoverManagedDoltShouldReuseExisting(tt.existingPort, tt.requestedPort); got != tt.want {
				t.Errorf("recoverManagedDoltShouldReuseExisting(%d, %q) = %v, want %v",
					tt.existingPort, tt.requestedPort, got, tt.want)
			}
		})
	}
}

func TestManagedDoltRecoverFields(t *testing.T) {
	report := managedDoltRecoverReport{
		DiagnosedReadOnly: true,
		HadPID:            true,
		Forced:            false,
		Ready:             true,
		PID:               9876,
		Port:              3311,
		Healthy:           true,
		Restarted:         true,
	}
	fields := managedDoltRecoverFields(report)
	want := []string{
		"diagnosed_read_only\ttrue",
		"had_pid\ttrue",
		"forced\tfalse",
		"ready\ttrue",
		"pid\t9876",
		"port\t3311",
		"healthy\ttrue",
		"restarted\ttrue",
	}
	if len(fields) != len(want) {
		t.Fatalf("got %d fields, want %d", len(fields), len(want))
	}
	for i, w := range want {
		if fields[i] != w {
			t.Errorf("fields[%d] = %q, want %q", i, fields[i], w)
		}
	}
}

func TestCleanupFailedManagedDoltRecovery_NilCause(t *testing.T) {
	if err := cleanupFailedManagedDoltRecovery("/nonexistent", 0, 0, nil); err != nil {
		t.Errorf("cleanupFailedManagedDoltRecovery(nil cause) = %v, want nil", err)
	}
}

func TestRecoverManagedDoltObservedRebindPossible(t *testing.T) {
	t.Run("empty port always possible", func(t *testing.T) {
		if !recoverManagedDoltObservedRebindPossible(t.TempDir(), "") {
			t.Error("empty requestedPort should return true")
		}
	})

	t.Run("no state files returns false", func(t *testing.T) {
		if recoverManagedDoltObservedRebindPossible(t.TempDir(), "3306") {
			t.Error("missing state files should return false")
		}
	})

	t.Run("state with different port returns true", func(t *testing.T) {
		cityPath := t.TempDir()
		statePath := providerManagedDoltStatePath(cityPath)
		if err := writeDoltRuntimeStateFile(statePath, doltRuntimeState{
			Running: true,
			PID:     1234,
			Port:    3307,
		}); err != nil {
			t.Fatalf("writeDoltRuntimeStateFile: %v", err)
		}
		if !recoverManagedDoltObservedRebindPossible(cityPath, "3306") {
			t.Error("different port should return true")
		}
	})

	t.Run("state with same port returns false", func(t *testing.T) {
		cityPath := t.TempDir()
		statePath := providerManagedDoltStatePath(cityPath)
		if err := writeDoltRuntimeStateFile(statePath, doltRuntimeState{
			Running: true,
			PID:     1234,
			Port:    3306,
		}); err != nil {
			t.Fatalf("writeDoltRuntimeStateFile: %v", err)
		}
		if recoverManagedDoltObservedRebindPossible(cityPath, "3306") {
			t.Error("same port should return false")
		}
	})
}

// recoverManagedDoltProcess is a test convenience over
// recoverManagedDoltProcessWithOptions: automatic (non-operator) recovery
// against the loopback host with the fixed root/warning test identity. It
// lives in the test file because production code has no non-operator wrapper
// caller left; keeping it in the shipped source trips unparam.
func recoverManagedDoltProcess(cityPath, port string, timeout time.Duration) (managedDoltRecoverReport, error) {
	return recoverManagedDoltProcessWithOptions(cityPath, "127.0.0.1", port, "root", "warning", timeout, managedDoltRecoverOptions{})
}

func setupRecoveryTestCity(t *testing.T) string {
	t.Helper()
	cityPath := t.TempDir()
	packStateDir := filepath.Join(cityPath, ".gc", "runtime", "packs", "dolt")
	if err := os.MkdirAll(packStateDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cityPath, ".beads", "dolt"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Setenv("GC_DOLT_PASSWORD", "test")
	t.Setenv("GC_BEADS", "file")
	t.Setenv("GC_BEADS_SCOPE_ROOT", "")
	return cityPath
}

func writeRecoveryRuntimeState(t *testing.T, cityPath string, pid, port int) {
	t.Helper()
	if err := writeDoltRuntimeStateFile(providerManagedDoltStatePath(cityPath), doltRuntimeState{
		Running:   true,
		PID:       pid,
		Port:      port,
		DataDir:   filepath.Join(cityPath, ".beads", "dolt"),
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("writeDoltRuntimeStateFile: %v", err)
	}
}

func TestRecoverManagedDolt_SkipsRestartWhenProbeHealthy(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	writeRecoveryRuntimeState(t, cityPath, 4321, 3306)
	stubHealthyManagedDoltProbes(t)

	report, err := recoverManagedDoltProcess(cityPath, "3306", 10*time.Second)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !report.Ready {
		t.Error("expected Ready=true when probe succeeds")
	}
	if !report.Healthy {
		t.Error("expected Healthy=true when probe succeeds")
	}
	if report.DiagnosedReadOnly {
		t.Error("expected DiagnosedReadOnly=false for healthy server")
	}
	if !report.HadPID {
		t.Error("expected HadPID=true from runtime state")
	}
	if report.PID != 4321 {
		t.Errorf("expected PID=4321 from runtime state, got %d", report.PID)
	}
	if report.Port != 3306 {
		t.Errorf("expected Port=3306 from runtime state, got %d", report.Port)
	}
	if report.Restarted {
		t.Error("expected Restarted=false when healthy server is reused")
	}
}

func TestRecoverManagedDolt_ProceedsWhenReadOnly(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)

	oldProbe := managedDoltQueryProbeDirectFn
	oldReadOnly := managedDoltReadOnlyStateDirectFn
	oldConnCount := managedDoltConnectionCountDirectFn
	oldPreflight := managedDoltPreflightCleanupFn
	t.Cleanup(func() {
		managedDoltQueryProbeDirectFn = oldProbe
		managedDoltReadOnlyStateDirectFn = oldReadOnly
		managedDoltConnectionCountDirectFn = oldConnCount
		managedDoltPreflightCleanupFn = oldPreflight
	})

	managedDoltQueryProbeDirectFn = func(_, _, _ string) error { return nil }
	managedDoltReadOnlyStateDirectFn = func(_, _, _ string) (string, error) { return "true", nil }
	managedDoltConnectionCountDirectFn = func(_, _, _ string) (string, error) { return "5", nil }
	managedDoltPreflightCleanupFn = func(_ string) error {
		return fmt.Errorf("stop: expected — no real dolt process")
	}

	report, err := recoverManagedDoltProcess(cityPath, "3306", 10*time.Second)
	if err == nil {
		t.Fatal("expected error when read-only server recovery proceeds to stop/start")
	}
	if !report.DiagnosedReadOnly {
		t.Error("expected DiagnosedReadOnly=true for read-only server")
	}
	if report.Ready {
		t.Error("expected Ready=false when recovery proceeds past probe")
	}
}

func TestRecoverManagedDolt_ProceedsWhenProbeUnreachable(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)

	oldProbe := managedDoltQueryProbeDirectFn
	oldPreflight := managedDoltPreflightCleanupFn
	t.Cleanup(func() {
		managedDoltQueryProbeDirectFn = oldProbe
		managedDoltPreflightCleanupFn = oldPreflight
	})

	managedDoltQueryProbeDirectFn = func(_, _, _ string) error {
		return fmt.Errorf("connection refused")
	}
	managedDoltPreflightCleanupFn = func(_ string) error {
		return fmt.Errorf("stop: expected — no real dolt process")
	}

	report, err := recoverManagedDoltProcess(cityPath, "3306", 10*time.Second)
	if err == nil {
		t.Fatal("expected error when unreachable server recovery proceeds to stop/start")
	}
	if report.Ready {
		t.Error("expected Ready=false when probe fails")
	}
}

func TestRecoverManagedDolt_ProceedsWhenReadOnlyUnknown(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)

	oldProbe := managedDoltQueryProbeDirectFn
	oldReadOnly := managedDoltReadOnlyStateDirectFn
	oldConnCount := managedDoltConnectionCountDirectFn
	oldPreflight := managedDoltPreflightCleanupFn
	t.Cleanup(func() {
		managedDoltQueryProbeDirectFn = oldProbe
		managedDoltReadOnlyStateDirectFn = oldReadOnly
		managedDoltConnectionCountDirectFn = oldConnCount
		managedDoltPreflightCleanupFn = oldPreflight
	})

	managedDoltQueryProbeDirectFn = func(_, _, _ string) error { return nil }
	managedDoltReadOnlyStateDirectFn = func(_, _, _ string) (string, error) {
		return "unknown", errManagedDoltNoUserDatabase
	}
	managedDoltConnectionCountDirectFn = func(_, _, _ string) (string, error) { return "5", nil }
	managedDoltPreflightCleanupFn = func(_ string) error {
		return fmt.Errorf("stop: expected - no real dolt process")
	}

	report, err := recoverManagedDoltProcess(cityPath, "3306", 10*time.Second)
	if err == nil {
		t.Fatal("expected error when read-only state is unknown and recovery proceeds to stop/start")
	}
	if report.DiagnosedReadOnly {
		t.Error("expected DiagnosedReadOnly=false for unknown read-only state")
	}
	if report.Ready {
		t.Error("expected Ready=false when recovery proceeds past unknown read-only health")
	}
}

func TestRecoverManagedDolt_ProceedsWhenHealthCheckErrors(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)

	oldProbe := managedDoltQueryProbeDirectFn
	oldReadOnly := managedDoltReadOnlyStateDirectFn
	oldPreflight := managedDoltPreflightCleanupFn
	t.Cleanup(func() {
		managedDoltQueryProbeDirectFn = oldProbe
		managedDoltReadOnlyStateDirectFn = oldReadOnly
		managedDoltPreflightCleanupFn = oldPreflight
	})

	managedDoltQueryProbeDirectFn = func(_, _, _ string) error { return nil }
	managedDoltReadOnlyStateDirectFn = func(_, _, _ string) (string, error) {
		return "", fmt.Errorf("broken pipe")
	}
	managedDoltPreflightCleanupFn = func(_ string) error {
		return fmt.Errorf("stop: expected — no real dolt process")
	}

	report, err := recoverManagedDoltProcess(cityPath, "3306", 10*time.Second)
	if err == nil {
		t.Fatal("expected error when health check fails and recovery proceeds to stop/start")
	}
	if report.Ready {
		t.Error("expected Ready=false when health check errors")
	}
}

func stubRecoverManagedDoltStop(t *testing.T, fn func(cityPath, port string, clearPublishedState bool) (managedDoltStopReport, error)) *atomic.Int32 {
	t.Helper()
	old := recoverManagedDoltStopFn
	calls := &atomic.Int32{}
	recoverManagedDoltStopFn = func(cityPath, port string, clearPublishedState bool) (managedDoltStopReport, error) {
		calls.Add(1)
		return fn(cityPath, port, clearPublishedState)
	}
	t.Cleanup(func() { recoverManagedDoltStopFn = old })
	return calls
}

func holdManagedDoltLifecycleLock(t *testing.T, cityPath string) func() {
	t.Helper()
	lockFile, _, err := openManagedDoltLifecycleLock(cityPath)
	if err != nil {
		t.Fatalf("openManagedDoltLifecycleLock: %v", err)
	}
	locked, err := tryManagedDoltLifecycleLock(lockFile)
	if err != nil || !locked {
		_ = lockFile.Close()
		t.Fatalf("tryManagedDoltLifecycleLock: locked=%v err=%v", locked, err)
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { releaseManagedDoltLifecycleLock(lockFile) })
	}
	t.Cleanup(release)
	return release
}

func stubUnreachableManagedDoltProbe(t *testing.T) {
	t.Helper()
	oldProbe := managedDoltQueryProbeDirectFn
	managedDoltQueryProbeDirectFn = func(_, _, _ string) error {
		return fmt.Errorf("connection refused")
	}
	t.Cleanup(func() { managedDoltQueryProbeDirectFn = oldProbe })
}

// stubHealthyManagedDoltProbes makes the direct probes report a healthy,
// writable, query-ready server.
func stubHealthyManagedDoltProbes(t *testing.T) {
	t.Helper()
	oldProbe := managedDoltQueryProbeDirectFn
	oldReadOnly := managedDoltReadOnlyStateDirectFn
	oldConnCount := managedDoltConnectionCountDirectFn
	t.Cleanup(func() {
		managedDoltQueryProbeDirectFn = oldProbe
		managedDoltReadOnlyStateDirectFn = oldReadOnly
		managedDoltConnectionCountDirectFn = oldConnCount
	})
	managedDoltQueryProbeDirectFn = func(_, _, _ string) error { return nil }
	managedDoltReadOnlyStateDirectFn = func(_, _, _ string) (string, error) { return "false", nil }
	managedDoltConnectionCountDirectFn = func(_, _, _ string) (string, error) { return "5", nil }
}

func stubManagedDoltPreflight(t *testing.T, fn func(cityPath string) error) {
	t.Helper()
	old := managedDoltPreflightCleanupFn
	managedDoltPreflightCleanupFn = fn
	t.Cleanup(func() { managedDoltPreflightCleanupFn = old })
}

func TestRecoverManagedDolt_AutomaticLoserFailsFastWhenLockHeld(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	holdManagedDoltLifecycleLock(t, cityPath)
	stubUnreachableManagedDoltProbe(t)
	stopCalls := stubRecoverManagedDoltStop(t, func(_, _ string, _ bool) (managedDoltStopReport, error) {
		return managedDoltStopReport{}, nil
	})

	start := time.Now()
	report, err := recoverManagedDoltProcess(cityPath, "3306", time.Second)
	elapsed := time.Since(start)

	if !errors.Is(err, errManagedDoltRecoveryInProgress) {
		t.Fatalf("automatic loser error = %v, want errManagedDoltRecoveryInProgress", err)
	}
	if report.Healthy || report.Ready {
		t.Errorf("in-progress recovery must never be reported healthy/ready, got %+v", report)
	}
	if stopCalls.Load() != 0 {
		t.Errorf("automatic loser must never run the stop/start owner path, stop calls = %d", stopCalls.Load())
	}
	if elapsed > 10*time.Second {
		t.Errorf("loser lifetime = %v, want bounded near the 1s timeout", elapsed)
	}

	// The loser must not have queued for ownership: the lock holder still
	// owns the lifecycle, and a fresh probe must succeed once released.
	probe, _, err := openManagedDoltLifecycleLock(cityPath)
	if err != nil {
		t.Fatalf("openManagedDoltLifecycleLock: %v", err)
	}
	defer probe.Close() //nolint:errcheck
	locked, err := tryManagedDoltLifecycleLock(probe)
	if err != nil {
		t.Fatalf("tryManagedDoltLifecycleLock: %v", err)
	}
	if locked {
		releaseManagedDoltLifecycleLock(probe)
		t.Fatal("lifecycle lock was free after loser returned; expected the original holder to still own it")
	}
}

func TestRecoverManagedDolt_OperatorAcquiresAfterWinnerReleases(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	release := holdManagedDoltLifecycleLock(t, cityPath)
	stubUnreachableManagedDoltProbe(t)
	stopErrSentinel := fmt.Errorf("stop sentinel: operator reached the owner path")
	stopCalls := stubRecoverManagedDoltStop(t, func(_, _ string, _ bool) (managedDoltStopReport, error) {
		return managedDoltStopReport{}, stopErrSentinel
	})

	go func() {
		time.Sleep(300 * time.Millisecond)
		release()
	}()

	_, err := recoverManagedDoltProcessWithOptions(cityPath, "127.0.0.1", "3306", "root", "warning", 10*time.Second, managedDoltRecoverOptions{Operator: true})
	if err == nil || !strings.Contains(err.Error(), "stop sentinel") {
		t.Fatalf("operator recovery error = %v, want the owner-path stop sentinel (proves the operator serialized behind the winner and acquired ownership)", err)
	}
	if stopCalls.Load() != 1 {
		t.Errorf("stop calls = %d, want 1", stopCalls.Load())
	}
}

func TestRecoverManagedDolt_AbortsBeforeStartWhenStopFails(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	stubUnreachableManagedDoltProbe(t)
	stubRecoverManagedDoltStop(t, func(_, _ string, _ bool) (managedDoltStopReport, error) {
		return managedDoltStopReport{HadPID: true, PID: 4242}, fmt.Errorf("data dir still locked by flushing descendant")
	})
	preflightCalls := 0
	stubManagedDoltPreflight(t, func(_ string) error {
		preflightCalls++
		return nil
	})

	report, err := recoverManagedDoltProcess(cityPath, "3306", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "data directory was not released") {
		t.Fatalf("error = %v, want fail-closed stop abort", err)
	}
	if preflightCalls != 0 {
		t.Errorf("preflight ran %d times after a failed stop; recovery must abort before preflight/start", preflightCalls)
	}
	if report.Restarted {
		t.Errorf("report.Restarted = true, want false when recovery aborts on stop failure")
	}

	layout, layoutErr := resolveManagedDoltRuntimeLayout(cityPath)
	if layoutErr != nil {
		t.Fatalf("resolveManagedDoltRuntimeLayout: %v", layoutErr)
	}
	circuit := readManagedDoltRecoveryCircuit(layout)
	if circuit.OpenUntil.IsZero() {
		t.Error("stop abort must leave a pacing window so the next automatic attempt is deferred")
	}
	if circuit.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0 (stop abort must not escalate the breaker)", circuit.ConsecutiveFailures)
	}
	if circuit.AttemptPID != 0 {
		t.Errorf("AttemptPID = %d, want cleared after abort", circuit.AttemptPID)
	}
}

func TestRecoverManagedDolt_AutomaticBlockedByOpenCircuit(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	stubUnreachableManagedDoltProbe(t)
	stopCalls := stubRecoverManagedDoltStop(t, func(_, _ string, _ bool) (managedDoltStopReport, error) {
		return managedDoltStopReport{}, nil
	})
	layout, err := resolveManagedDoltRuntimeLayout(cityPath)
	if err != nil {
		t.Fatalf("resolveManagedDoltRuntimeLayout: %v", err)
	}
	if err := writeManagedDoltRecoveryCircuit(layout, managedDoltRecoveryCircuit{
		ConsecutiveFailures: 2,
		LastFailureAt:       time.Now(),
		OpenUntil:           time.Now().Add(time.Hour),
		LastError:           "prior recovery failed",
	}); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}

	_, err = recoverManagedDoltProcess(cityPath, "3306", 5*time.Second)
	if !errors.Is(err, errManagedDoltRecoveryCircuitOpen) {
		t.Fatalf("error = %v, want errManagedDoltRecoveryCircuitOpen", err)
	}
	if stopCalls.Load() != 0 {
		t.Errorf("stop calls = %d, want 0 while the circuit is open", stopCalls.Load())
	}
}

func TestRecoverManagedDolt_OperatorBypassesOpenCircuit(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	stubUnreachableManagedDoltProbe(t)
	stopCalls := stubRecoverManagedDoltStop(t, func(_, _ string, _ bool) (managedDoltStopReport, error) {
		return managedDoltStopReport{}, nil
	})
	stubManagedDoltPreflight(t, func(_ string) error {
		return fmt.Errorf("preflight sentinel: operator passed the circuit gate")
	})
	layout, err := resolveManagedDoltRuntimeLayout(cityPath)
	if err != nil {
		t.Fatalf("resolveManagedDoltRuntimeLayout: %v", err)
	}
	if err := writeManagedDoltRecoveryCircuit(layout, managedDoltRecoveryCircuit{
		ConsecutiveFailures: 5,
		LastFailureAt:       time.Now(),
		OpenUntil:           time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}

	_, err = recoverManagedDoltProcessWithOptions(cityPath, "127.0.0.1", "3306", "root", "warning", 5*time.Second, managedDoltRecoverOptions{Operator: true})
	if err == nil || !strings.Contains(err.Error(), "preflight sentinel") {
		t.Fatalf("operator error = %v, want the preflight sentinel past the circuit gate", err)
	}
	if stopCalls.Load() != 1 {
		t.Errorf("stop calls = %d, want 1 (operator must reach the owner path)", stopCalls.Load())
	}
}

func TestRecoverManagedDolt_FailedAttemptOpensCircuitForNextAutomatic(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	stubUnreachableManagedDoltProbe(t)
	stopCalls := stubRecoverManagedDoltStop(t, func(_, _ string, _ bool) (managedDoltStopReport, error) {
		return managedDoltStopReport{}, nil
	})
	stubManagedDoltPreflight(t, func(_ string) error {
		return fmt.Errorf("preflight boom")
	})

	if _, err := recoverManagedDoltProcess(cityPath, "3306", 5*time.Second); err == nil {
		t.Fatal("first attempt: expected preflight failure")
	}
	if stopCalls.Load() != 1 {
		t.Fatalf("stop calls after first attempt = %d, want 1", stopCalls.Load())
	}

	_, err := recoverManagedDoltProcess(cityPath, "3306", 5*time.Second)
	if !errors.Is(err, errManagedDoltRecoveryCircuitOpen) {
		t.Fatalf("second automatic attempt error = %v, want errManagedDoltRecoveryCircuitOpen", err)
	}
	if stopCalls.Load() != 1 {
		t.Errorf("stop calls after blocked second attempt = %d, want still 1", stopCalls.Load())
	}
}

func TestRecoverManagedDolt_HealthyProbeClearsOpenWindowKeepsFailureMemory(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	writeRecoveryRuntimeState(t, cityPath, 4321, 3306)
	stubHealthyManagedDoltProbes(t)

	layout, err := resolveManagedDoltRuntimeLayout(cityPath)
	if err != nil {
		t.Fatalf("resolveManagedDoltRuntimeLayout: %v", err)
	}
	lastFailure := time.Now().Add(-time.Minute)
	if err := writeManagedDoltRecoveryCircuit(layout, managedDoltRecoveryCircuit{
		ConsecutiveFailures: 2,
		LastFailureAt:       lastFailure,
		OpenUntil:           time.Now().Add(time.Hour),
		LastError:           "prior failure",
	}); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}

	report, err := recoverManagedDoltProcess(cityPath, "3306", 5*time.Second)
	if err != nil {
		t.Fatalf("recoverManagedDoltProcess = %v, want healthy-probe success even while the circuit is open (health resets availability)", err)
	}
	if !report.Healthy {
		t.Error("report.Healthy = false, want true")
	}
	circuit := readManagedDoltRecoveryCircuit(layout)
	if !circuit.OpenUntil.IsZero() {
		t.Errorf("OpenUntil = %v, want cleared after a healthy outcome", circuit.OpenUntil)
	}
	if circuit.ConsecutiveFailures != 2 || !circuit.LastFailureAt.Equal(lastFailure) {
		t.Errorf("failure memory = %+v, want failures=2 at %v kept for flap hysteresis", circuit, lastFailure)
	}
}

func TestRecoverManagedDolt_HealthySuccessSurvivesCircuitWriteFailure(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	writeRecoveryRuntimeState(t, cityPath, 4321, 3306)
	stubHealthyManagedDoltProbes(t)

	layout, err := resolveManagedDoltRuntimeLayout(cityPath)
	if err != nil {
		t.Fatalf("resolveManagedDoltRuntimeLayout: %v", err)
	}
	if err := writeManagedDoltRecoveryCircuit(layout, managedDoltRecoveryCircuit{
		ConsecutiveFailures: 1,
		LastFailureAt:       time.Now().Add(-time.Minute),
		OpenUntil:           time.Now().Add(time.Hour),
		LastError:           "prior failure",
	}); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}

	oldWrite := writeManagedDoltRecoveryCircuitFn
	writeManagedDoltRecoveryCircuitFn = func(_ managedDoltRuntimeLayout, _ managedDoltRecoveryCircuit) error {
		return fmt.Errorf("disk full")
	}
	t.Cleanup(func() { writeManagedDoltRecoveryCircuitFn = oldWrite })

	report, err := recoverManagedDoltProcess(cityPath, "3306", 5*time.Second)
	if err != nil {
		t.Fatalf("recoverManagedDoltProcess = %v, want nil: a failed circuit clear must not turn a successful recovery into a reported failure", err)
	}
	if !report.Healthy || !report.Ready {
		t.Errorf("report = %+v, want Healthy=true Ready=true", report)
	}
}

// TestRecoverManagedDolt_AutomaticStormHasAtMostOneOwner storms concurrent
// automatic recoveries at one city. The flock coalesces concurrent callers
// (losers observe and never queue for ownership) and the circuit stamped by
// the first owner's attempt gates any straggler that grabs the freed lock, so
// the destructive stop path must run exactly once and every caller must
// return, non-healthy, within its own wait budget.
//
// No rendezvous barrier is needed for the exactly-once assertion: a goroutine
// scheduled so late that it wins the already-released lock still cannot reach
// the stop path, because beginManagedDoltRecoveryAttempt stamped
// OpenUntil = first attempt + 30s and the gate refuses automatic attempts
// until that window passes — far beyond this test's 2s budgets. Whether any
// goroutine actually hits that straggler schedule is up to the scheduler
// (most runs coalesce on the flock alone); the gate itself is pinned
// deterministically by
// TestRecoverManagedDolt_FailedAttemptOpensCircuitForNextAutomatic.
func TestRecoverManagedDolt_AutomaticStormHasAtMostOneOwner(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	stubUnreachableManagedDoltProbe(t)

	stopCalls := stubRecoverManagedDoltStop(t, func(_, _ string, _ bool) (managedDoltStopReport, error) {
		// Hold ownership long enough for the rest of the storm to arrive
		// while the lock is held, then abort so recovery never reaches the
		// real start path.
		time.Sleep(500 * time.Millisecond)
		return managedDoltStopReport{}, fmt.Errorf("storm: data dir still held")
	})

	const stormSize = 8
	var wg sync.WaitGroup
	errs := make([]error, stormSize)
	reports := make([]managedDoltRecoverReport, stormSize)
	start := time.Now()
	for i := 0; i < stormSize; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reports[i], errs[i] = recoverManagedDoltProcess(cityPath, "3306", 2*time.Second)
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	if got := stopCalls.Load(); got != 1 {
		t.Errorf("stop ran %d times, want exactly one automatic owner across the storm", got)
	}
	if elapsed > 15*time.Second {
		t.Errorf("storm drained in %v, want every loser bounded near its own 2s budget", elapsed)
	}
	for i := 0; i < stormSize; i++ {
		if errs[i] == nil {
			t.Fatalf("caller %d returned nil error; no storm caller can succeed", i)
		}
		if reports[i].Healthy || reports[i].Ready {
			t.Errorf("caller %d reported healthy/ready during in-progress recovery: %+v", i, reports[i])
		}
		switch {
		case errors.Is(errs[i], errManagedDoltRecoveryInProgress):
		case errors.Is(errs[i], errManagedDoltRecoveryCircuitOpen):
		case strings.Contains(errs[i].Error(), "storm: data dir still held"):
		default:
			t.Errorf("caller %d error = %v, want owner stop-abort, in-progress, or circuit-open", i, errs[i])
		}
	}
}
