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

func TestRecoverManagedDoltProcessWithOps(t *testing.T) {
	queryErr := errors.New("query unavailable")
	stopErr := errors.New("stop failed")
	preflightErr := errors.New("preflight failed")
	startErr := errors.New("start failed")
	healthErr := errors.New("health failed")
	publishErr := errors.New("publish failed")
	cleanupErr := errors.New("cleanup failed")

	type testCase struct {
		name         string
		stopReport   managedDoltStopReport
		stopErr      error
		preflightErr error
		startReport  managedDoltStartReport
		startErr     error
		healthReport managedDoltSQLHealthReport
		healthErr    error
		publishErr   error
		cleanupErr   error
		wantReport   managedDoltRecoverReport
		wantErrIs    error
		wantErrText  string
		wantOps      []string
	}

	const (
		cityToken = "$CITY"
		queryOp   = "query host=127.0.0.1 port=3306 user=root"
		stopOp    = "stop city=$CITY port=3306"
		preflight = "preflight city=$CITY"
		startOp   = "start city=$CITY host=127.0.0.1 port=3306 user=root log=warning timeout=2s"
		healthOp  = "health host=127.0.0.1 port=4407 user=root"
		publishOp = "publish city=$CITY"
	)
	baseStop := managedDoltStopReport{HadPID: true, PID: 111}
	baseStart := managedDoltStartReport{Ready: true, PID: 222, Port: 4407}
	baseReport := managedDoltRecoverReport{
		HadPID:    true,
		Ready:     true,
		PID:       222,
		Port:      4407,
		Healthy:   true,
		Restarted: true,
	}
	tests := []testCase{
		{
			name:         "unknown no-user final health succeeds",
			stopReport:   baseStop,
			startReport:  baseStart,
			healthReport: managedDoltSQLHealthReport{QueryReady: true, ReadOnly: "unknown"},
			wantReport:   baseReport,
			wantOps:      []string{queryOp, stopOp, preflight, startOp, healthOp, publishOp},
		},
		{
			name:         "writable final health succeeds",
			stopReport:   baseStop,
			startReport:  baseStart,
			healthReport: managedDoltSQLHealthReport{QueryReady: true, ReadOnly: "false"},
			wantReport:   baseReport,
			wantOps:      []string{queryOp, stopOp, preflight, startOp, healthOp, publishOp},
		},
		{
			name:       "stop error aborts before preflight and retains stop report",
			stopReport: managedDoltStopReport{HadPID: true, PID: 111, Forced: true},
			stopErr:    stopErr,
			wantReport: managedDoltRecoverReport{
				HadPID: true, Forced: true, PID: 111, Port: 3306,
			},
			wantErrIs: stopErr,
			wantOps:   []string{queryOp, stopOp},
		},
		{
			name:         "preflight failure cleans stopped process at requested port",
			stopReport:   baseStop,
			preflightErr: preflightErr,
			cleanupErr:   cleanupErr,
			wantReport: managedDoltRecoverReport{
				HadPID: true, PID: 111, Port: 3306,
			},
			wantErrIs:   cleanupErr,
			wantErrText: cleanupErr.Error(),
			wantOps: []string{
				queryOp, stopOp, preflight,
				"cleanup city=$CITY pid=111 port=3306 cause=preflight failed",
			},
		},
		{
			name:        "start failure returns start report without coordinator cleanup",
			stopReport:  baseStop,
			startReport: managedDoltStartReport{PID: 222, Port: 4407, AddressInUse: true, Attempts: 2},
			startErr:    startErr,
			wantReport: managedDoltRecoverReport{
				HadPID: true, PID: 222, Port: 4407, Restarted: true,
			},
			wantErrIs:   startErr,
			wantErrText: startErr.Error(),
			wantOps:     []string{queryOp, stopOp, preflight, startOp},
		},
		{
			name:        "final health error cleans replacement at resolved port",
			stopReport:  baseStop,
			startReport: baseStart,
			healthErr:   healthErr,
			cleanupErr:  cleanupErr,
			wantReport: managedDoltRecoverReport{
				HadPID: true, Ready: true, PID: 222, Port: 4407, Restarted: true,
			},
			wantErrIs:   cleanupErr,
			wantErrText: cleanupErr.Error(),
			wantOps: []string{
				queryOp, stopOp, preflight, startOp, healthOp,
				"cleanup city=$CITY pid=222 port=4407 cause=health failed",
			},
		},
		{
			name:         "still read-only cleans replacement",
			stopReport:   baseStop,
			startReport:  baseStart,
			healthReport: managedDoltSQLHealthReport{QueryReady: true, ReadOnly: "true"},
			wantReport: managedDoltRecoverReport{
				HadPID: true, Ready: true, PID: 222, Port: 4407, Restarted: true,
			},
			wantErrText: "dolt server on 127.0.0.1:4407 is still read-only after recovery",
			wantOps: []string{
				queryOp, stopOp, preflight, startOp, healthOp,
				"cleanup city=$CITY pid=222 port=4407 cause=dolt server on 127.0.0.1:4407 is still read-only after recovery",
			},
		},
		{
			name:         "query-not-ready cleans replacement",
			stopReport:   baseStop,
			startReport:  baseStart,
			healthReport: managedDoltSQLHealthReport{ReadOnly: "false"},
			wantReport: managedDoltRecoverReport{
				HadPID: true, Ready: true, PID: 222, Port: 4407, Restarted: true,
			},
			wantErrText: "dolt server on 127.0.0.1:4407 is not query-ready after recovery",
			wantOps: []string{
				queryOp, stopOp, preflight, startOp, healthOp,
				"cleanup city=$CITY pid=222 port=4407 cause=dolt server on 127.0.0.1:4407 is not query-ready after recovery",
			},
		},
		{
			name:         "publication failure cleans replacement",
			stopReport:   baseStop,
			startReport:  baseStart,
			healthReport: managedDoltSQLHealthReport{QueryReady: true, ReadOnly: "false"},
			publishErr:   publishErr,
			wantReport:   baseReport,
			wantErrIs:    publishErr,
			wantErrText:  "publish managed dolt runtime state: publish failed",
			wantOps: []string{
				queryOp, stopOp, preflight, startOp, healthOp, publishOp,
				"cleanup city=$CITY pid=222 port=4407 cause=publish managed dolt runtime state: publish failed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cityPath := t.TempDir()
			var gotOps []string
			ops := managedDoltRecoveryOps{
				queryProbe: func(host, port, user string) error {
					gotOps = append(gotOps, fmt.Sprintf("query host=%s port=%s user=%s", host, port, user))
					return queryErr
				},
				healthCheck: func(host, port, user string) (managedDoltSQLHealthReport, error) {
					gotOps = append(gotOps, fmt.Sprintf("health host=%s port=%s user=%s", host, port, user))
					return tt.healthReport, tt.healthErr
				},
				stop: func(cityPath, port string) (managedDoltStopReport, error) {
					gotOps = append(gotOps, fmt.Sprintf("stop city=%s port=%s", cityPath, port))
					return tt.stopReport, tt.stopErr
				},
				preflightCleanup: func(cityPath string) error {
					gotOps = append(gotOps, "preflight city="+cityPath)
					return tt.preflightErr
				},
				start: func(cityPath, host, port, user, logLevel string, timeout time.Duration) (managedDoltStartReport, error) {
					gotOps = append(gotOps, fmt.Sprintf("start city=%s host=%s port=%s user=%s log=%s timeout=%s", cityPath, host, port, user, logLevel, timeout))
					return tt.startReport, tt.startErr
				},
				publish: func(cityPath string) error {
					gotOps = append(gotOps, "publish city="+cityPath)
					return tt.publishErr
				},
				failedCleanup: func(cityPath string, pid, port int, cause error) error {
					gotOps = append(gotOps, fmt.Sprintf("cleanup city=%s pid=%d port=%d cause=%v", cityPath, pid, port, cause))
					if tt.cleanupErr != nil {
						return tt.cleanupErr
					}
					return cause
				},
			}

			gotReport, gotErr := recoverManagedDoltProcessWithOps(
				cityPath, "127.0.0.1", "3306", "root", "warning", 2*time.Second, ops,
			)
			if gotReport != tt.wantReport {
				t.Errorf("report = %+v, want %+v", gotReport, tt.wantReport)
			}
			switch {
			case tt.wantErrIs != nil && !errors.Is(gotErr, tt.wantErrIs):
				t.Errorf("error = %v, want errors.Is(_, %v)", gotErr, tt.wantErrIs)
			case tt.wantErrIs == nil && tt.wantErrText == "" && gotErr != nil:
				t.Errorf("error = %v, want nil", gotErr)
			}
			if tt.wantErrText != "" && (gotErr == nil || gotErr.Error() != tt.wantErrText) {
				t.Errorf("error = %v, want text %q", gotErr, tt.wantErrText)
			}
			wantOps := make([]string, len(tt.wantOps))
			for i, op := range tt.wantOps {
				wantOps[i] = strings.ReplaceAll(op, cityToken, cityPath)
			}
			if strings.Join(gotOps, "\n") != strings.Join(wantOps, "\n") {
				t.Errorf("operations:\n%s\nwant:\n%s", strings.Join(gotOps, "\n"), strings.Join(wantOps, "\n"))
			}
		})
	}
}

func TestCleanupFailedManagedDoltRecovery_NilCause(t *testing.T) {
	if err := cleanupFailedManagedDoltRecovery("/nonexistent", 0, 0, nil); err != nil {
		t.Errorf("cleanupFailedManagedDoltRecovery(nil cause) = %v, want nil", err)
	}
}

func TestCleanupFailedManagedDoltRecovery_ClearsRuntimeAndPublishedState(t *testing.T) {
	cityPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(`[workspace]
name = "cleanup-test"

[beads]
provider = "bd"
backend = "dolt"
`), 0o644); err != nil {
		t.Fatalf("write city config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cityPath, ".beads"), 0o755); err != nil {
		t.Fatalf("create beads dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, ".beads", "config.yaml"), []byte("issue_prefix: gc\ngc.endpoint_origin: managed_city\ngc.endpoint_status: verified\ndolt.auto-start: false\n"), 0o644); err != nil {
		t.Fatalf("write beads config: %v", err)
	}
	owned, err := managedDoltLifecycleOwned(cityPath)
	if err != nil {
		t.Fatalf("managedDoltLifecycleOwned: %v", err)
	}
	if !owned {
		t.Fatal("managedDoltLifecycleOwned = false, want true for managed bd city")
	}

	layout, err := resolveManagedDoltRuntimeLayout(cityPath)
	if err != nil {
		t.Fatalf("resolveManagedDoltRuntimeLayout: %v", err)
	}
	const (
		pid  = 4242
		port = 33123
	)
	state := doltRuntimeState{
		Running:   true,
		PID:       pid,
		Port:      port,
		DataDir:   layout.DataDir,
		StartedAt: "2026-07-21T00:00:00Z",
	}
	if err := writeDoltRuntimeStateFile(layout.StateFile, state); err != nil {
		t.Fatalf("write provider runtime state: %v", err)
	}
	if err := os.WriteFile(layout.PIDFile, []byte("4242\n"), 0o644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	if err := writeDoltRuntimeStateFile(managedDoltStatePath(cityPath), state); err != nil {
		t.Fatalf("write published runtime state: %v", err)
	}

	cause := errors.New("preflight cleanup failed")
	err = cleanupFailedManagedDoltRecovery(cityPath, 0, port, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("cleanupFailedManagedDoltRecovery error = %v, want sentinel cause", err)
	}

	got, err := readDoltRuntimeStateFile(layout.StateFile)
	if err != nil {
		t.Fatalf("read provider runtime state: %v", err)
	}
	if got.Running || got.PID != 0 || got.Port != port {
		t.Fatalf("provider runtime state = {Running:%v PID:%d Port:%d}, want {Running:false PID:0 Port:%d}", got.Running, got.PID, got.Port, port)
	}
	if _, err := os.Stat(layout.PIDFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pid file stat error = %v, want os.ErrNotExist", err)
	}
	if _, err := os.Stat(managedDoltStatePath(cityPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published runtime state stat error = %v, want os.ErrNotExist", err)
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
	preflightErr := errors.New("preflight cleanup failed")

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
		return preflightErr
	}

	report, err := recoverManagedDoltProcess(cityPath, "3306", 10*time.Second)
	if !errors.Is(err, preflightErr) {
		t.Fatalf("recoverManagedDoltProcess error = %v, want preflight cleanup sentinel", err)
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
	contended := make(chan struct{})
	stopErrSentinel := fmt.Errorf("stop sentinel: operator reached the owner path")
	var stopCalls atomic.Int32
	ops := defaultManagedDoltRecoveryOps()
	ops.contended = func() { close(contended) }
	ops.queryProbe = func(_, _, _ string) error { return fmt.Errorf("connection refused") }
	ops.stop = func(_, _ string) (managedDoltStopReport, error) {
		stopCalls.Add(1)
		return managedDoltStopReport{}, stopErrSentinel
	}

	type result struct {
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		_, err := recoverManagedDoltProcessWithOptionsAndOps(cityPath, "127.0.0.1", "3306", "root", "warning", 10*time.Second, managedDoltRecoverOptions{Operator: true}, ops)
		resultCh <- result{err: err}
	}()

	select {
	case <-contended:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("operator never reached the held lifecycle lock")
	}
	var err error
	select {
	case got := <-resultCh:
		err = got.err
	case <-time.After(5 * time.Second):
		t.Fatal("operator did not acquire the released lifecycle lock")
	}
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
// The stop stub holds the owner until every other caller observes the held
// flock, making the contention deterministic. The circuit still protects a caller scheduled
// after the flock is released; that gate is pinned directly by
// TestRecoverManagedDolt_FailedAttemptOpensCircuitForNextAutomatic.
func TestRecoverManagedDolt_AutomaticStormHasAtMostOneOwner(t *testing.T) {
	cityPath := setupRecoveryTestCity(t)
	const stormSize = 8
	var contendedCalls atomic.Int32
	allContended := make(chan struct{})
	var allContendedOnce sync.Once
	ops := defaultManagedDoltRecoveryOps()
	ops.queryProbe = func(_, _, _ string) error { return fmt.Errorf("connection refused") }
	ops.contended = func() {
		if contendedCalls.Add(1) >= stormSize-1 {
			allContendedOnce.Do(func() { close(allContended) })
		}
	}

	var stopCalls atomic.Int32
	ops.stop = func(_, _ string) (managedDoltStopReport, error) {
		stopCalls.Add(1)
		// Keep the owner in the destructive phase until every caller has
		// observed the held flock. Losers therefore contend while this owner
		// still holds the flock, without relying on scheduler timing.
		select {
		case <-allContended:
		case <-time.After(5 * time.Second):
			return managedDoltStopReport{}, fmt.Errorf("storm: callers did not rendezvous")
		}
		return managedDoltStopReport{}, fmt.Errorf("storm: data dir still held")
	}

	var wg sync.WaitGroup
	errs := make([]error, stormSize)
	reports := make([]managedDoltRecoverReport, stormSize)
	start := time.Now()
	for i := 0; i < stormSize; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reports[i], errs[i] = recoverManagedDoltProcessWithOptionsAndOps(cityPath, "127.0.0.1", "3306", "root", "warning", 2*time.Second, managedDoltRecoverOptions{}, ops)
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
