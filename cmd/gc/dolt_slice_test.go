package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/runtime/systemdscope"
	"github.com/gastownhall/gascity/internal/testutil"
)

type oomScoreAdjAttempt struct {
	value        int
	lastAccepted int
}

type oomScoreAdjFloorWriter struct {
	floor        int
	lastAccepted int
	errAt        int
	injectErr    error
	attempts     []oomScoreAdjAttempt
}

func (w *oomScoreAdjFloorWriter) write(value int) error {
	w.attempts = append(w.attempts, oomScoreAdjAttempt{value: value, lastAccepted: w.lastAccepted})
	if len(w.attempts) == w.errAt && w.injectErr != nil {
		return w.injectErr
	}
	if value < w.floor {
		return fmt.Errorf("kernel floor %d rejects %d: %w", w.floor, value, fs.ErrPermission)
	}
	w.lastAccepted = value
	return nil
}

type lowerOOMScoreAdjCase struct {
	name                                     string
	current, target, floor, want, wantWrites int
	errAt                                    int
	injectErr, wantErr                       error
}

func TestLowerOOMScoreAdjToFloor(t *testing.T) {
	nonPermissionErr := fmt.Errorf("write oom_score_adj: %w", fs.ErrNotExist)
	tests := []lowerOOMScoreAdjCase{
		{name: "production host floor", current: 200, floor: 100, want: 100, wantWrites: -1},
		{name: "target accepted", current: 200, floor: 0, want: 0, wantWrites: 1},
		{name: "nothing below current accepted", current: 200, floor: 200, want: 200, wantErr: fs.ErrPermission, wantWrites: -1},
		{name: "already at target", current: 0, floor: 0, want: 0, wantWrites: 0},
		{name: "already below target", current: -500, floor: 0, want: -500, wantWrites: 0},
		{name: "odd floor", current: 200, floor: 137, want: 137, wantWrites: -1},
		{
			name: "first write has non-permission error", current: 200, floor: 0,
			errAt: 1, injectErr: nonPermissionErr, want: 200, wantErr: nonPermissionErr,
			wantWrites: 1,
		},
		{
			name: "non-permission error mid-search", current: 200, floor: 100,
			errAt: 3, injectErr: nonPermissionErr, want: 100, wantErr: nonPermissionErr,
			wantWrites: 3,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkLowerOOMScoreAdjCase(t, tc) })
	}
}

func checkLowerOOMScoreAdjCase(t *testing.T, tc lowerOOMScoreAdjCase) {
	t.Helper()
	writer := &oomScoreAdjFloorWriter{
		floor: tc.floor, lastAccepted: tc.current, errAt: tc.errAt, injectErr: tc.injectErr,
	}
	got, err := lowerOOMScoreAdjToFloor(tc.current, tc.target, writer.write)
	if got != tc.want {
		t.Errorf("lowerOOMScoreAdjToFloor(%d, %d) = %d, want %d", tc.current, tc.target, got, tc.want)
	}
	if got != writer.lastAccepted {
		t.Errorf("returned value = %d, value in effect = %d", got, writer.lastAccepted)
	}
	if tc.wantErr == nil && err != nil {
		t.Errorf("lowerOOMScoreAdjToFloor error = %v, want nil", err)
	}
	if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
		t.Errorf("lowerOOMScoreAdjToFloor error = %v, want error wrapping %v", err, tc.wantErr)
	}
	if tc.wantWrites >= 0 && len(writer.attempts) != tc.wantWrites {
		t.Errorf("writes = %d, want %d: %+v", len(writer.attempts), tc.wantWrites, writer.attempts)
	}
	if tc.wantWrites == 1 && len(writer.attempts) == 1 && writer.attempts[0].value != tc.target {
		t.Errorf("only write = %d, want target %d", writer.attempts[0].value, tc.target)
	}
	for _, attempt := range writer.attempts {
		if attempt.value >= attempt.lastAccepted {
			t.Errorf("write %d was not below last accepted value %d", attempt.value, attempt.lastAccepted)
		}
	}
}

func TestManagedDoltSliceFor(t *testing.T) {
	tests := []struct {
		name     string
		testMode bool
		envValue string
		envSet   bool
		want     string
	}{
		{name: "unset preserves direct spawn", want: ""},
		{name: "explicit slice wins", envValue: "custom.slice", envSet: true, want: "custom.slice"},
		{name: "explicit empty resolves empty for fail-closed validation", envValue: "", envSet: true, want: ""},
		{name: "whitespace is trimmed", envValue: "  s.slice  ", envSet: true, want: "s.slice"},
		{name: "test mode drops the implicit default", testMode: true, want: ""},
		{
			name:     "test mode still honors an explicit slice",
			testMode: true, envValue: "s.slice", envSet: true, want: "s.slice",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedDoltSliceFor(tc.testMode, tc.envValue, tc.envSet); got != tc.want {
				t.Errorf("managedDoltSliceFor(%v, %q, %v) = %q, want %q",
					tc.testMode, tc.envValue, tc.envSet, got, tc.want)
			}
		})
	}
}

// TestManagedDoltPlacementOffUnderTestWithoutProbing guards the CI contract:
// with no explicit slice the managed-dolt suite must not touch systemd at all,
// so it neither depends on a user manager nor pays the probe timeout on hosts
// without one.
func TestManagedDoltPlacementOffUnderTestWithoutProbing(t *testing.T) {
	if got := managedDoltSlice(); got != "" {
		t.Fatalf("managedDoltSlice() = %q inside the test binary, want no placement", got)
	}
	argv := []string{"dolt", "sql-server", "--config", "/tmp/c.yaml"}
	type result struct {
		argv []string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		got, err := wrapManagedDoltArgv(argv)
		done <- result{argv: got, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("wrapManagedDoltArgv: %v", got.err)
		}
		if strings.Join(got.argv, "\x00") != strings.Join(argv, "\x00") {
			t.Fatalf("wrapManagedDoltArgv = %q, want unwrapped %q", got.argv, argv)
		}
	case <-time.After(testutil.GoroutineRaceTimeout):
		t.Fatal("wrapManagedDoltArgv blocked; it must not probe systemd when placement is off")
	}
}

func TestWrapManagedDoltArgvPreservesUnsetProductionSpawn(t *testing.T) {
	withManagedDoltTestMode(t, false)
	t.Setenv(managedDoltTestModeEnv, "0")
	if managedDoltTestModeEnabled() {
		t.Fatal("managedDoltTestModeEnabled() = true, want production-mode false")
	}
	argv := []string{"dolt", "sql-server", "--config", "/tmp/c.yaml"}
	got, err := wrapManagedDoltArgv(argv)
	if err != nil {
		t.Fatalf("wrapManagedDoltArgv unset production: %v", err)
	}
	if strings.Join(got, "\x00") != strings.Join(argv, "\x00") {
		t.Fatalf("wrapManagedDoltArgv unset production = %q, want %q", got, argv)
	}
}

func TestPrepareManagedDoltPlacementAppliesBoundedPolicy(t *testing.T) {
	var gotSlice string
	var gotPolicy systemdscope.SlicePolicy
	previousEnsure := managedDoltEnsureSlice
	managedDoltEnsureSlice = func(_ context.Context, slice string, policy systemdscope.SlicePolicy) error {
		gotSlice = slice
		gotPolicy = policy
		return nil
	}
	t.Cleanup(func() { managedDoltEnsureSlice = previousEnsure })

	if err := prepareManagedDoltPlacement(context.Background(), "custom.slice"); err != nil {
		t.Fatalf("prepareManagedDoltPlacement: %v", err)
	}
	if gotSlice != "custom.slice" {
		t.Fatalf("slice = %q, want custom.slice", gotSlice)
	}
	wantPolicy := systemdscope.SlicePolicy{
		MemoryMax:            managedDoltMemoryMaxBytes,
		MemoryLow:            managedDoltMemoryLowBytes,
		ManagedOOMPreference: managedDoltManagedOOMPreference,
	}
	if !reflect.DeepEqual(gotPolicy, wantPolicy) {
		t.Fatalf("policy = %#v, want %#v", gotPolicy, wantPolicy)
	}
}

func TestPrepareManagedDoltPlacementRejectsNestedSlice(t *testing.T) {
	calls := 0
	previousEnsure := managedDoltEnsureSlice
	managedDoltEnsureSlice = func(context.Context, string, systemdscope.SlicePolicy) error {
		calls++
		return nil
	}
	t.Cleanup(func() { managedDoltEnsureSlice = previousEnsure })

	err := prepareManagedDoltPlacement(context.Background(), "gascity-dolt.slice")
	if err == nil || !strings.Contains(err.Error(), "not top-level") {
		t.Fatalf("prepareManagedDoltPlacement error = %v, want nested-slice rejection", err)
	}
	if calls != 0 {
		t.Fatalf("systemd policy called %d times for nested slice", calls)
	}
}

func TestWrapManagedDoltArgvFailsClosedWhenPlacementCannotBePrepared(t *testing.T) {
	previousEnsure := managedDoltEnsureSlice
	managedDoltEnsureSlice = func(context.Context, string, systemdscope.SlicePolicy) error {
		return errors.New("policy unavailable")
	}
	t.Cleanup(func() { managedDoltEnsureSlice = previousEnsure })

	got, err := wrapManagedDoltArgvFor([]string{"dolt", "sql-server"}, "required.slice", true)
	if err == nil {
		t.Fatalf("wrapManagedDoltArgv error = nil, got %q", got)
	}
	if len(got) != 0 {
		t.Fatalf("wrapManagedDoltArgv returned unsafe fallback %q", got)
	}
	if !strings.Contains(err.Error(), "policy unavailable") {
		t.Fatalf("error = %q, want policy cause", err)
	}
}

func TestWrapManagedDoltArgvSkipsPlacementOnNonLinux(t *testing.T) {
	oldGOOS := supervisorRuntimeGOOS
	supervisorRuntimeGOOS = "darwin"
	t.Cleanup(func() { supervisorRuntimeGOOS = oldGOOS })

	// Even an explicit setting can't be honored: the adopt side has no
	// non-Linux implementation to keep it consistent with, so the top-level
	// entry point skips placement outright rather than half-enforcing it.
	t.Setenv(managedDoltSliceEnv, "required.slice")

	argv := []string{"dolt", "sql-server", "--config", "x"}
	got, err := wrapManagedDoltArgv(argv)
	if err != nil {
		t.Fatalf("wrapManagedDoltArgv error = %v, want nil on non-Linux", err)
	}
	if !reflect.DeepEqual(got, argv) {
		t.Fatalf("wrapManagedDoltArgv = %q, want unwrapped %q", got, argv)
	}
}

func TestWrapManagedDoltArgvRejectsExplicitEmptySlice(t *testing.T) {
	got, err := wrapManagedDoltArgvFor([]string{"dolt", "sql-server"}, "", true)
	if err == nil {
		t.Fatalf("wrapManagedDoltArgv error = nil, got %q", got)
	}
	if len(got) != 0 {
		t.Fatalf("wrapManagedDoltArgv returned unsafe fallback %q", got)
	}
	if !strings.Contains(err.Error(), managedDoltSliceEnv+" is empty") {
		t.Fatalf("error = %q, want explicit empty-slice cause", err)
	}
}

func TestWrapManagedDoltArgvFailsClosedWhenRequiredProbeFails(t *testing.T) {
	previousEnsure := managedDoltEnsureSlice
	managedDoltEnsureSlice = func(context.Context, string, systemdscope.SlicePolicy) error { return nil }
	t.Cleanup(func() { managedDoltEnsureSlice = previousEnsure })

	previousWrapper := managedDoltSliceWrapper
	managedDoltSliceWrapper = &systemdscope.Wrapper{
		Probe: func(string) error { return errors.New("user bus unavailable") },
		Warn:  &strings.Builder{},
		Label: managedDoltSliceEnv,
	}
	t.Cleanup(func() { managedDoltSliceWrapper = previousWrapper })

	got, err := wrapManagedDoltArgvFor([]string{"dolt", "sql-server"}, "required.slice", true)
	if err == nil {
		t.Fatalf("wrapManagedDoltArgv error = nil, got %q", got)
	}
	if len(got) != 0 {
		t.Fatalf("wrapManagedDoltArgv returned unsafe fallback %q", got)
	}
}

// TestManagedDoltSliceWrapperIsLabeled pins the one cmd/gc-side property of the
// shared wrapper: a probe failure must name the knob an operator would change.
// The fallback behavior itself is covered in the systemdscope package.
func TestManagedDoltSliceWrapperIsLabeled(t *testing.T) {
	if managedDoltSliceWrapper.Label != managedDoltSliceEnv {
		t.Errorf("wrapper label = %q, want %q", managedDoltSliceWrapper.Label, managedDoltSliceEnv)
	}
}
