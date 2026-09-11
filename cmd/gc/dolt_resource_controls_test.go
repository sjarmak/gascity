package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestManagedDoltSliceCommandFallbacks(t *testing.T) {
	originalArgs := []string{"sql-server", "--config", "/city/dolt-config.yaml"}

	t.Run("unset", func(t *testing.T) {
		t.Setenv(managedDoltSliceEnv, "")
		if err := os.Unsetenv(managedDoltSliceEnv); err != nil {
			t.Fatalf("unset %s: %v", managedDoltSliceEnv, err)
		}
		var warning strings.Builder
		name, args := managedDoltCommand("dolt", originalArgs, &warning)
		if name != "dolt" || !slices.Equal(args, originalArgs) {
			t.Fatalf("unset %s changed command: name=%q args=%q", managedDoltSliceEnv, name, args)
		}
		if warning.Len() != 0 {
			t.Fatalf("unset %s warning = %q, want none", managedDoltSliceEnv, warning.String())
		}
	})

	t.Run("empty", func(t *testing.T) {
		probeCalls := 0
		var warning strings.Builder
		name, args := managedDoltCommandForSlice("", "dolt", originalArgs, func(string) error {
			probeCalls++
			return nil
		}, &warning)
		if name != "dolt" || !slices.Equal(args, originalArgs) {
			t.Fatalf("empty %s changed command: name=%q args=%q", managedDoltSliceEnv, name, args)
		}
		if probeCalls != 0 {
			t.Fatalf("empty %s probed systemd %d times, want 0", managedDoltSliceEnv, probeCalls)
		}
		if warning.Len() != 0 {
			t.Fatalf("empty %s warning = %q, want none", managedDoltSliceEnv, warning.String())
		}
	})

	t.Run("valid", func(t *testing.T) {
		var warning strings.Builder
		name, args := managedDoltCommandForSlice("gctestdolt.slice", "dolt", originalArgs, func(string) error {
			return nil
		}, &warning)
		wantArgs := []string{
			"--user", "--scope", "--slice=gctestdolt.slice", "--collect", "--quiet", "--",
			"dolt", "sql-server", "--config", "/city/dolt-config.yaml",
		}
		if name != "systemd-run" || !slices.Equal(args, wantArgs) {
			t.Fatalf("valid %s command: name=%q args=%q, want systemd-run %q", managedDoltSliceEnv, name, args, wantArgs)
		}
		if warning.Len() != 0 {
			t.Fatalf("valid %s warning = %q, want none", managedDoltSliceEnv, warning.String())
		}
	})

	for _, value := range []string{"dolt", "dolt/scope.slice", "dolt scope.slice", "dolt\tscope.slice"} {
		t.Run("invalid_"+strconv.Quote(value), func(t *testing.T) {
			probeCalls := 0
			var warning strings.Builder
			name, args := managedDoltCommandForSlice(value, "dolt", originalArgs, func(string) error {
				probeCalls++
				return nil
			}, &warning)
			if name != "dolt" || !slices.Equal(args, originalArgs) {
				t.Fatalf("invalid %s changed command: name=%q args=%q", managedDoltSliceEnv, name, args)
			}
			if probeCalls != 0 {
				t.Fatalf("invalid %s probed systemd %d times, want 0", managedDoltSliceEnv, probeCalls)
			}
			if lines := strings.Count(warning.String(), "\n"); lines != 1 {
				t.Fatalf("invalid %s emitted %d warning lines, want 1: %q", managedDoltSliceEnv, lines, warning.String())
			}
			if !strings.Contains(warning.String(), managedDoltSliceEnv) || !strings.Contains(warning.String(), "invalid") {
				t.Fatalf("invalid-slice warning missing env name or error: %q", warning.String())
			}
		})
	}

	t.Run("probe failure", func(t *testing.T) {
		var warning strings.Builder
		name, args := managedDoltCommandForSlice("gctestdolt.slice", "dolt", originalArgs, func(string) error {
			return errors.New("user manager unavailable")
		}, &warning)
		if name != "dolt" || !slices.Equal(args, originalArgs) {
			t.Fatalf("probe failure changed command: name=%q args=%q", name, args)
		}
		if lines := strings.Count(warning.String(), "\n"); lines != 1 {
			t.Fatalf("probe failure emitted %d warning lines, want 1: %q", lines, warning.String())
		}
		for _, want := range []string{managedDoltSliceEnv, "user manager unavailable", "unwrapped"} {
			if !strings.Contains(warning.String(), want) {
				t.Fatalf("probe-failure warning %q missing %q", warning.String(), want)
			}
		}
	})
}

func TestLowerOOMScoreAdjByBisection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		floor     int
		refuseAll bool
		want      int
		wantErr   bool
	}{
		{name: "floor zero", floor: 0, want: 0},
		{name: "floor one hundred", floor: 100, want: 100},
		{name: "writer refuses everything", refuseAll: true, want: 200, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := 200
			attempts := make([]int, 0)
			got, err := lowerOOMScoreAdjByBisection(current, func(value int) error {
				attempts = append(attempts, value)
				if value < 0 {
					t.Fatalf("attempted oom_score_adj below zero: %d", value)
				}
				if value > current {
					t.Fatalf("attempted to raise oom_score_adj from %d to %d", current, value)
				}
				if tc.refuseAll || value < tc.floor {
					return os.ErrPermission
				}
				current = value
				return nil
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("lowerOOMScoreAdjByBisection error = %v, wantErr %t", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("lowerOOMScoreAdjByBisection = %d, want %d (attempts %v)", got, tc.want, attempts)
			}
			if len(attempts) == 0 || attempts[0] != 0 {
				t.Fatalf("first write attempt = %v, want 0 first", attempts)
			}
		})
	}
}

func TestLowerManagedDoltOOMScoreAdjRejectsInvalidPID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux /proc process properties required")
	}
	before, err := os.ReadFile("/proc/self/oom_score_adj")
	if err != nil {
		t.Fatalf("read oom_score_adj before invalid-pid call: %v", err)
	}
	var log bytes.Buffer

	lowerManagedDoltOOMScoreAdj(0, &log)

	after, err := os.ReadFile("/proc/self/oom_score_adj")
	if err != nil {
		t.Fatalf("read oom_score_adj after invalid-pid call: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("/proc/self/oom_score_adj changed after invalid-pid call")
	}
	wantLog := "gc managed dolt: oom_score_adj before=unknown after=unknown (lowering failed: invalid pid 0)\n"
	if got := log.String(); got != wantLog {
		t.Errorf("invalid-pid log = %q, want %q", got, wantLog)
	}
}

func TestManagedDoltProductionSpawnWithoutSlice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux /proc process properties required")
	}
	for _, tc := range []struct {
		name             string
		scopeWatchdogEnv string
		wantWatchdog     bool
	}{
		{name: "direct", scopeWatchdogEnv: "0"},
		{name: "scope_watchdog", scopeWatchdogEnv: "1", wantWatchdog: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withManagedDoltTestMode(t, false)
			t.Setenv(managedDoltTestModeEnv, "")
			t.Setenv("GC_MANAGED_DOLT_TEST_WATCHDOG", "0")
			t.Setenv(managedDoltScopeWatchdogEnv, tc.scopeWatchdogEnv)
			t.Setenv(managedDoltSliceEnv, "")
			t.Setenv("DOLT_DISABLE_EVENT_FLUSH", "false")

			cityPath := t.TempDir()
			fakeDoltDir := writeFakeDoltSQLServer(t)
			t.Setenv("PATH", fakeDoltDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			configPath := filepath.Join(cityPath, "dolt-config.yaml")
			logPath := filepath.Join(cityPath, "dolt.log")
			if err := os.WriteFile(configPath, []byte("log_level: debug\n"), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				t.Fatalf("open log: %v", err)
			}
			defer logFile.Close() //nolint:errcheck

			parentCgroup := readProcProperty(t, os.Getpid(), "cgroup")
			parentOOM := readProcIntProperty(t, os.Getpid(), "oom_score_adj")
			started, err := startManagedDoltSQLServer(cityPath, configPath, logPath, logFile)
			if err != nil {
				t.Fatalf("start managed dolt: %v", err)
			}
			cleanupManagedDoltProductionSpawn(t, started)
			waitForProcessExe(t, started.PID, func(exe string) bool {
				return filepath.Base(exe) == "sleep"
			}, "sleep after stub exec")
			if (started.WatchdogPID > 0) != tc.wantWatchdog {
				t.Fatalf("watchdog pid = %d, want watchdog=%t", started.WatchdogPID, tc.wantWatchdog)
			}
			assertProcessEnvironmentEntry(t, started.PID, "DOLT_DISABLE_EVENT_FLUSH=true")

			if got := readProcProperty(t, started.PID, "cgroup"); got != parentCgroup {
				t.Fatalf("unset %s changed child cgroup: got %q, want inherited %q", managedDoltSliceEnv, got, parentCgroup)
			}
			assertManagedDoltProductionProperties(t, started.PID, cityPath, parentOOM)
			if started.WatchdogPID > 0 {
				if got := readProcProperty(t, started.WatchdogPID, "cgroup"); got != parentCgroup {
					t.Errorf("unset %s changed watchdog cgroup: got %q, want inherited %q", managedDoltSliceEnv, got, parentCgroup)
				}
				assertManagedDoltCWD(t, started.WatchdogPID, cityPath)
			}
			logData, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read dolt log: %v", err)
			}
			if !strings.Contains(string(logData), "oom_score_adj") || !strings.Contains(string(logData), "before=") || !strings.Contains(string(logData), "after=") {
				t.Errorf("dolt log missing oom_score_adj before/after line:\n%s", logData)
			}
		})
	}
}

func TestManagedDoltProductionSpawnInConfiguredSlice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux cgroup and /proc process properties required")
	}
	slice := uniqueManagedDoltTestSlice()
	if err := probeManagedDoltSliceSupport(slice); err != nil {
		t.Skipf("systemd user scope unavailable: %v", err)
	}
	t.Cleanup(func() { stopManagedDoltTestSlice(t, slice) })

	for _, tc := range []struct {
		name             string
		scopeWatchdogEnv string
		wantWatchdog     bool
	}{
		{name: "direct", scopeWatchdogEnv: "0"},
		{name: "scope_watchdog", scopeWatchdogEnv: "1", wantWatchdog: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withManagedDoltTestMode(t, false)
			t.Setenv(managedDoltTestModeEnv, "")
			t.Setenv("GC_MANAGED_DOLT_TEST_WATCHDOG", "0")
			t.Setenv(managedDoltScopeWatchdogEnv, tc.scopeWatchdogEnv)
			t.Setenv(managedDoltSliceEnv, slice)
			t.Setenv("DOLT_DISABLE_EVENT_FLUSH", "false")

			cityPath := t.TempDir()
			fakeDoltDir := writeFakeDoltSQLServer(t)
			t.Setenv("PATH", fakeDoltDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			configPath := filepath.Join(cityPath, "dolt-config.yaml")
			logPath := filepath.Join(cityPath, "dolt.log")
			if err := os.WriteFile(configPath, []byte("log_level: debug\n"), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				t.Fatalf("open log: %v", err)
			}
			defer logFile.Close() //nolint:errcheck

			parentOOM := readProcIntProperty(t, os.Getpid(), "oom_score_adj")
			started, err := startManagedDoltSQLServer(cityPath, configPath, logPath, logFile)
			if err != nil {
				t.Fatalf("start managed dolt: %v", err)
			}
			cleanupManagedDoltProductionSpawn(t, started)
			waitForProcessExe(t, started.PID, func(exe string) bool {
				return filepath.Base(exe) == "sleep"
			}, "sleep after systemd-run and stub exec")
			if started.WatchdogPID > 0 {
				testExe, err := os.Executable()
				if err != nil {
					t.Fatalf("resolve test executable: %v", err)
				}
				waitForProcessExe(t, started.WatchdogPID, func(exe string) bool {
					return samePath(exe, testExe)
				}, "test executable after systemd-run exec")
			}
			if (started.WatchdogPID > 0) != tc.wantWatchdog {
				t.Fatalf("watchdog pid = %d, want watchdog=%t", started.WatchdogPID, tc.wantWatchdog)
			}
			assertProcessEnvironmentEntry(t, started.PID, "DOLT_DISABLE_EVENT_FLUSH=true")

			assertProcessInSlice(t, started.PID, slice)
			assertManagedDoltProductionProperties(t, started.PID, cityPath, parentOOM)
			if got := readProcStartTimeTicks(started.PID); got == 0 || got != started.StartTimeTicks {
				t.Errorf("returned dolt identity ticks = %d, live /proc ticks = %d", started.StartTimeTicks, got)
			}
			doltExe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(started.PID), "exe"))
			if err != nil {
				t.Fatalf("read dolt child exe: %v", err)
			}
			if filepath.Base(doltExe) != "sleep" {
				t.Errorf("returned dolt pid %d exe = %q, want sleep after systemd-run and stub exec", started.PID, doltExe)
			}

			if started.WatchdogPID > 0 {
				assertProcessInSlice(t, started.WatchdogPID, slice)
				watchdogCWD, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(started.WatchdogPID), "cwd"))
				if err != nil {
					t.Fatalf("read watchdog cwd: %v", err)
				}
				if !samePath(watchdogCWD, cityPath) {
					t.Errorf("watchdog cwd = %q, want city path %q", watchdogCWD, cityPath)
				}
				if got := readProcStartTimeTicks(started.WatchdogPID); got == 0 || got != started.WatchdogStartTimeTicks {
					t.Errorf("returned watchdog identity ticks = %d, live /proc ticks = %d", started.WatchdogStartTimeTicks, got)
				}
				watchdogExe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(started.WatchdogPID), "exe"))
				if err != nil {
					t.Fatalf("read watchdog exe: %v", err)
				}
				testExe, err := os.Executable()
				if err != nil {
					t.Fatalf("resolve test executable: %v", err)
				}
				if !samePath(watchdogExe, testExe) {
					t.Errorf("returned watchdog pid %d exe = %q, want re-exec %q after systemd-run exec", started.WatchdogPID, watchdogExe, testExe)
				}
			}

			logData, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read dolt log: %v", err)
			}
			if !strings.Contains(string(logData), "oom_score_adj") || !strings.Contains(string(logData), "before=") || !strings.Contains(string(logData), "after=") {
				t.Errorf("dolt log missing oom_score_adj before/after line:\n%s", logData)
			}
		})
	}
}

func assertProcessInSlice(t *testing.T, pid int, slice string) {
	t.Helper()
	if cgroup := readProcProperty(t, pid, "cgroup"); !strings.Contains(cgroup, slice) {
		t.Errorf("process %d cgroup %q does not contain configured slice %q", pid, cgroup, slice)
	}
}

func waitForProcessExe(t *testing.T, pid int, match func(string) bool, desc string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	lastExe := "unknown"
	for {
		exe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
		if err == nil {
			lastExe = exe
			if match(exe) {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("process %d exe = %q, want %s", pid, lastExe, desc)
		case <-ticker.C:
		}
	}
}

func assertProcessEnvironmentEntry(t *testing.T, pid int, want string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	readSucceeded := false
	for {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
		if err != nil {
			lastErr = err
		} else {
			readSucceeded = true
			if len(data) > 0 {
				for _, entry := range bytes.Split(data, []byte{0}) {
					if string(entry) == want {
						return
					}
				}
				t.Fatalf("%s absent from pid %d environ", want, pid)
			}
		}
		select {
		case <-deadline.C:
			if !readSucceeded {
				t.Fatalf("reading pid %d environ kept failing: %v", pid, lastErr)
			}
			t.Fatalf("pid %d environ was still empty after 10 s", pid)
		case <-ticker.C:
		}
	}
}

func stopManagedDoltTestSlice(t *testing.T, slice string) {
	t.Helper()
	if !regexp.MustCompile(`^gctestdolt[0-9]+\.slice$`).MatchString(slice) {
		t.Errorf("refusing to stop non-test slice %q", slice)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "stop", "--", slice).CombinedOutput()
	cancel()
	if err != nil {
		t.Errorf("stop test slice %q: %v: %s", slice, err, strings.TrimSpace(string(out)))
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	out, err = exec.CommandContext(ctx, "systemctl", "--user", "show", "-p", "ActiveState", "--value", "--", slice).CombinedOutput()
	cancel()
	if err != nil {
		t.Errorf("show test slice %q ActiveState: %v", slice, err)
		return
	}
	if got := strings.TrimSpace(string(out)); got != "inactive" {
		t.Errorf("test slice %q ActiveState = %q, want inactive", slice, got)
	}
}

func cleanupManagedDoltProductionSpawn(t *testing.T, started managedDoltStartedProcess) {
	t.Helper()
	t.Logf("started managed Dolt test processes: dolt=%d watchdog=%d", started.PID, started.WatchdogPID)
	t.Cleanup(func() {
		terminateManagedDoltStartedProcess(started)
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			doltGone := started.PID <= 0 || !pidAlive(started.PID)
			watchdogGone := started.WatchdogPID <= 0 || !pidAlive(started.WatchdogPID)
			if doltGone && watchdogGone {
				t.Logf("confirmed managed Dolt test processes gone: dolt=%d watchdog=%d", started.PID, started.WatchdogPID)
				return
			}
			select {
			case <-deadline.C:
				t.Errorf("managed Dolt test processes still alive after cleanup: dolt=%d alive=%t watchdog=%d alive=%t",
					started.PID, !doltGone, started.WatchdogPID, !watchdogGone)
				return
			case <-ticker.C:
			}
		}
	})
}

func assertManagedDoltProductionProperties(t *testing.T, pid int, cityPath string, parentOOM int) {
	t.Helper()
	assertManagedDoltCWD(t, pid, cityPath)
	childOOM := readProcIntProperty(t, pid, "oom_score_adj")
	if parentOOM <= 0 {
		t.Logf("skipping oom_score_adj comparison: test process is already at floor %d", parentOOM)
	} else if childOOM >= parentOOM {
		t.Errorf("managed Dolt oom_score_adj = %d, want lower than test process value %d", childOOM, parentOOM)
	}
}

func assertManagedDoltCWD(t *testing.T, pid int, cityPath string) {
	t.Helper()
	cwd, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "cwd"))
	if err != nil {
		t.Fatalf("read process %d cwd: %v", pid, err)
	}
	if !samePath(cwd, cityPath) {
		t.Errorf("process %d cwd = %q, want city path %q", pid, cwd, cityPath)
	}
}

func readProcProperty(t *testing.T, pid int, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), name))
	if err != nil {
		t.Fatalf("read /proc/%d/%s: %v", pid, name, err)
	}
	return strings.TrimSpace(string(data))
}

func readProcIntProperty(t *testing.T, pid int, name string) int {
	t.Helper()
	raw := readProcProperty(t, pid, name)
	value, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("parse /proc/%d/%s value %q: %v", pid, name, raw, err)
	}
	return value
}

func uniqueManagedDoltTestSlice() string {
	return fmt.Sprintf("gctestdolt%d%d.slice", os.Getpid(), time.Now().UnixNano())
}
