package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stubRecoveryCircuitClock(t *testing.T, now time.Time) {
	t.Helper()
	old := managedDoltRecoveryCircuitNow
	managedDoltRecoveryCircuitNow = func() time.Time { return now }
	t.Cleanup(func() { managedDoltRecoveryCircuitNow = old })
}

func recoveryCircuitTestLayout(t *testing.T) managedDoltRuntimeLayout {
	t.Helper()
	cityPath := t.TempDir()
	layout, err := resolveManagedDoltRuntimeLayout(cityPath)
	if err != nil {
		t.Fatalf("resolveManagedDoltRuntimeLayout: %v", err)
	}
	return layout
}

func TestManagedDoltRecoveryCircuitRoundTrip(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	want := managedDoltRecoveryCircuit{
		ConsecutiveFailures: 3,
		LastFailureAt:       now,
		OpenUntil:           now.Add(time.Minute),
		LastError:           "boom",
		AttemptPID:          1234,
		LastAttemptAt:       now.Add(-time.Second),
	}
	if err := writeManagedDoltRecoveryCircuit(layout, want); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}
	got := readManagedDoltRecoveryCircuit(layout)
	if got.ConsecutiveFailures != want.ConsecutiveFailures ||
		!got.LastFailureAt.Equal(want.LastFailureAt) ||
		!got.OpenUntil.Equal(want.OpenUntil) ||
		got.LastError != want.LastError ||
		got.AttemptPID != want.AttemptPID ||
		!got.LastAttemptAt.Equal(want.LastAttemptAt) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestManagedDoltRecoveryCircuitAbsentReadsZero(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	got := readManagedDoltRecoveryCircuit(layout)
	if got.ConsecutiveFailures != 0 || got.AttemptPID != 0 || !got.OpenUntil.IsZero() {
		t.Errorf("absent circuit = %+v, want zero value", got)
	}
}

func TestManagedDoltRecoveryCircuitCorruptReadsZero(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	path := managedDoltRecoveryCircuitPath(layout)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"consecutive_failures": 7, "open_unt`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got := readManagedDoltRecoveryCircuit(layout)
	if got.ConsecutiveFailures != 0 || !got.OpenUntil.IsZero() {
		t.Errorf("corrupt circuit = %+v, want zero value (treated as absent)", got)
	}
}

func TestBumpManagedDoltRecoveryFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	memory := managedDoltRecoveryFailureMemory
	tests := []struct {
		name    string
		circuit managedDoltRecoveryCircuit
		want    int
	}{
		{name: "first failure", circuit: managedDoltRecoveryCircuit{}, want: 1},
		{
			name: "recent failure escalates",
			circuit: managedDoltRecoveryCircuit{
				ConsecutiveFailures: 2,
				LastFailureAt:       now.Add(-time.Minute),
			},
			want: 3,
		},
		{
			name: "stale failure memory decays to fresh",
			circuit: managedDoltRecoveryCircuit{
				ConsecutiveFailures: 4,
				LastFailureAt:       now.Add(-memory - time.Second),
			},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bumpManagedDoltRecoveryFailures(tt.circuit, now); got != tt.want {
				t.Errorf("bumpManagedDoltRecoveryFailures = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestManagedDoltRecoveryBackoff(t *testing.T) {
	base := managedDoltRecoveryCircuitBaseCooldown
	maxCooldown := managedDoltRecoveryCircuitMaxCooldown
	tests := []struct {
		failures int
		want     time.Duration
	}{
		{failures: 0, want: base},
		{failures: 1, want: base},
		{failures: 2, want: 2 * base},
		{failures: 3, want: 4 * base},
		{failures: 4, want: 4 * time.Minute},
		{failures: 5, want: maxCooldown},
		{failures: 50, want: maxCooldown},
	}
	for _, tt := range tests {
		if got := managedDoltRecoveryBackoff(tt.failures); got != tt.want {
			t.Errorf("managedDoltRecoveryBackoff(%d) = %v, want %v", tt.failures, got, tt.want)
		}
	}
}

func TestBeginManagedDoltRecoveryAttemptGatesAutomatic(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	stubRecoveryCircuitClock(t, now)
	if err := writeManagedDoltRecoveryCircuit(layout, managedDoltRecoveryCircuit{
		ConsecutiveFailures: 2,
		LastFailureAt:       now.Add(-time.Second),
		OpenUntil:           now.Add(time.Minute),
		LastError:           "still broken",
	}); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}

	err := beginManagedDoltRecoveryAttempt(layout, false, "/city", "3306")
	if !errors.Is(err, errManagedDoltRecoveryCircuitOpen) {
		t.Fatalf("beginManagedDoltRecoveryAttempt = %v, want errManagedDoltRecoveryCircuitOpen", err)
	}
	if !strings.Contains(err.Error(), "--operator") {
		t.Errorf("circuit-open error %q should name the --operator escape hatch", err.Error())
	}
	got := readManagedDoltRecoveryCircuit(layout)
	if got.AttemptPID != 0 {
		t.Errorf("refused attempt must not stamp AttemptPID, got %d", got.AttemptPID)
	}
}

func TestBeginManagedDoltRecoveryAttemptOperatorBypassesGate(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	stubRecoveryCircuitClock(t, now)
	if err := writeManagedDoltRecoveryCircuit(layout, managedDoltRecoveryCircuit{
		ConsecutiveFailures: 5,
		LastFailureAt:       now.Add(-time.Second),
		OpenUntil:           now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}

	if err := beginManagedDoltRecoveryAttempt(layout, true, "/city", "3306"); err != nil {
		t.Fatalf("operator beginManagedDoltRecoveryAttempt = %v, want nil", err)
	}
	got := readManagedDoltRecoveryCircuit(layout)
	if got.AttemptPID != os.Getpid() {
		t.Errorf("AttemptPID = %d, want %d", got.AttemptPID, os.Getpid())
	}
	if !got.LastAttemptAt.Equal(now) {
		t.Errorf("LastAttemptAt = %v, want %v", got.LastAttemptAt, now)
	}
}

func TestBeginManagedDoltRecoveryAttemptStampsPacingWindow(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	stubRecoveryCircuitClock(t, now)

	if err := beginManagedDoltRecoveryAttempt(layout, false, "/city", "3306"); err != nil {
		t.Fatalf("beginManagedDoltRecoveryAttempt = %v, want nil", err)
	}
	got := readManagedDoltRecoveryCircuit(layout)
	if want := now.Add(managedDoltRecoveryCircuitBaseCooldown); !got.OpenUntil.Equal(want) {
		t.Errorf("OpenUntil = %v, want attempt-start pacing stamp %v", got.OpenUntil, want)
	}
	if got.AttemptPID != os.Getpid() {
		t.Errorf("AttemptPID = %d, want %d", got.AttemptPID, os.Getpid())
	}
}

func TestBeginManagedDoltRecoveryAttemptCountsCrashedOwner(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	stubRecoveryCircuitClock(t, now)
	// A stale AttemptPID with an expired pacing window means a prior owner
	// began recovery and never completed (crash): the flock is released on
	// process death, so the next winner must account the crash as a failure.
	if err := writeManagedDoltRecoveryCircuit(layout, managedDoltRecoveryCircuit{
		ConsecutiveFailures: 1,
		LastFailureAt:       now.Add(-time.Minute),
		OpenUntil:           now.Add(-time.Second),
		AttemptPID:          999999,
		LastAttemptAt:       now.Add(-31 * time.Second),
	}); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}

	if err := beginManagedDoltRecoveryAttempt(layout, false, "/city", "3306"); err != nil {
		t.Fatalf("beginManagedDoltRecoveryAttempt = %v, want nil (crash accounting must not block a converging attempt)", err)
	}
	got := readManagedDoltRecoveryCircuit(layout)
	if got.ConsecutiveFailures != 2 {
		t.Errorf("ConsecutiveFailures = %d, want 2 (crashed owner counted)", got.ConsecutiveFailures)
	}
	if got.AttemptPID != os.Getpid() {
		t.Errorf("AttemptPID = %d, want current pid %d", got.AttemptPID, os.Getpid())
	}
	if !strings.Contains(got.LastError, "did not complete") {
		t.Errorf("LastError = %q, want crash diagnostic", got.LastError)
	}
}

func TestRecordManagedDoltRecoveryFailureEscalates(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	stubRecoveryCircuitClock(t, now)
	if err := writeManagedDoltRecoveryCircuit(layout, managedDoltRecoveryCircuit{
		ConsecutiveFailures: 2,
		LastFailureAt:       now.Add(-time.Minute),
		AttemptPID:          os.Getpid(),
		LastAttemptAt:       now,
	}); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}

	if err := recordManagedDoltRecoveryFailure(layout, fmt.Errorf("start failed"), false); err != nil {
		t.Fatalf("recordManagedDoltRecoveryFailure: %v", err)
	}
	got := readManagedDoltRecoveryCircuit(layout)
	if got.ConsecutiveFailures != 3 {
		t.Errorf("ConsecutiveFailures = %d, want 3", got.ConsecutiveFailures)
	}
	if want := now.Add(managedDoltRecoveryBackoff(3)); !got.OpenUntil.Equal(want) {
		t.Errorf("OpenUntil = %v, want escalated %v", got.OpenUntil, want)
	}
	if got.AttemptPID != 0 {
		t.Errorf("AttemptPID = %d, want cleared", got.AttemptPID)
	}
	if got.LastError != "start failed" {
		t.Errorf("LastError = %q, want %q", got.LastError, "start failed")
	}
}

func TestRecordManagedDoltRecoveryFailureStopAbortDoesNotEscalate(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	stubRecoveryCircuitClock(t, now)
	if err := writeManagedDoltRecoveryCircuit(layout, managedDoltRecoveryCircuit{
		ConsecutiveFailures: 3,
		LastFailureAt:       now.Add(-time.Minute),
		AttemptPID:          os.Getpid(),
		LastAttemptAt:       now,
	}); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}

	if err := recordManagedDoltRecoveryFailure(layout, fmt.Errorf("data dir not released"), true); err != nil {
		t.Fatalf("recordManagedDoltRecoveryFailure: %v", err)
	}
	got := readManagedDoltRecoveryCircuit(layout)
	if got.ConsecutiveFailures != 3 {
		t.Errorf("ConsecutiveFailures = %d, want unchanged 3 (stop abort is a transient flush, not an escalating failure)", got.ConsecutiveFailures)
	}
	if want := now.Add(managedDoltRecoveryCircuitBaseCooldown); !got.OpenUntil.Equal(want) {
		t.Errorf("OpenUntil = %v, want fixed base pacing %v", got.OpenUntil, want)
	}
}

func TestMarkManagedDoltRecoveryHealthyKeepsFailureMemory(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	stubRecoveryCircuitClock(t, now)
	lastFailure := now.Add(-time.Minute)
	if err := writeManagedDoltRecoveryCircuit(layout, managedDoltRecoveryCircuit{
		ConsecutiveFailures: 2,
		LastFailureAt:       lastFailure,
		OpenUntil:           now.Add(time.Minute),
		LastError:           "boom",
		AttemptPID:          os.Getpid(),
		LastAttemptAt:       now,
	}); err != nil {
		t.Fatalf("writeManagedDoltRecoveryCircuit: %v", err)
	}

	if err := markManagedDoltRecoveryHealthy(layout); err != nil {
		t.Fatalf("markManagedDoltRecoveryHealthy: %v", err)
	}
	got := readManagedDoltRecoveryCircuit(layout)
	if !got.OpenUntil.IsZero() || got.AttemptPID != 0 || got.LastError != "" {
		t.Errorf("healthy mark must clear the open window, attempt marker, and last error, got %+v", got)
	}
	// Flap hysteresis: a single healthy observation must NOT zero the failure
	// count — a server flapping around the cooldown boundary would otherwise
	// reset the breaker on every brief "up" and never accumulate backoff.
	if got.ConsecutiveFailures != 2 || !got.LastFailureAt.Equal(lastFailure) {
		t.Errorf("healthy mark must keep failure memory for flap hysteresis, got %+v", got)
	}
}

func TestMarkManagedDoltRecoveryHealthyNoFileNoWrite(t *testing.T) {
	layout := recoveryCircuitTestLayout(t)
	if err := markManagedDoltRecoveryHealthy(layout); err != nil {
		t.Fatalf("markManagedDoltRecoveryHealthy: %v", err)
	}
	if _, err := os.Stat(managedDoltRecoveryCircuitPath(layout)); !os.IsNotExist(err) {
		t.Errorf("healthy mark on a city with no circuit history must not create the file (stat err = %v)", err)
	}
}
