package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gastownhall/gascity/internal/systemdscope"
)

const managedDoltSliceEnv = "GC_DOLT_SLICE"

const managedDoltSliceProbeTimeout = 5 * time.Second

func probeManagedDoltSliceSupport(slice string) error {
	return systemdscope.Probe(slice, managedDoltSliceProbeTimeout)
}

func managedDoltCommand(executable string, args []string, log io.Writer) (string, []string) {
	return managedDoltCommandForSlice(os.Getenv(managedDoltSliceEnv), executable, args, probeManagedDoltSliceSupport, log)
}

func managedDoltCommandForSlice(slice, executable string, args []string, probe func(string) error, log io.Writer) (string, []string) {
	if slice == "" {
		return executable, args
	}
	if err := validateManagedDoltSlice(slice); err != nil {
		warnManagedDoltSliceFallback(log, slice, err)
		return executable, args
	}
	if err := probe(slice); err != nil {
		warnManagedDoltSliceFallback(log, slice, err)
		return executable, args
	}
	wrapped := []string{
		"--user", "--scope", "--slice=" + slice, "--collect", "--quiet", "--", executable,
	}
	wrapped = append(wrapped, args...)
	return "systemd-run", wrapped
}

func validateManagedDoltSlice(slice string) error {
	if !strings.HasSuffix(slice, ".slice") {
		return fmt.Errorf("invalid slice name: must end in .slice")
	}
	if strings.ContainsRune(slice, '/') {
		return fmt.Errorf("invalid slice name: must not contain /")
	}
	if strings.IndexFunc(slice, unicode.IsSpace) >= 0 {
		return fmt.Errorf("invalid slice name: must not contain whitespace")
	}
	return nil
}

func warnManagedDoltSliceFallback(log io.Writer, slice string, err error) {
	if log == nil {
		return
	}
	_, _ = fmt.Fprintf(log, "gc: %s=%q set but transient user scope is unavailable; managed Dolt runs unwrapped: %v\n",
		managedDoltSliceEnv, slice, err)
}

func lowerManagedDoltOOMScoreAdj(pid int, log io.Writer) {
	if pid <= 0 {
		logManagedDoltOOMScoreAdj(log, "unknown", "unknown", fmt.Errorf("invalid pid %d", pid))
		return
	}
	path := "/proc/self/oom_score_adj"
	if pid != os.Getpid() {
		path = "/proc/" + strconv.Itoa(pid) + "/oom_score_adj"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		logManagedDoltOOMScoreAdj(log, "unknown", "unknown", fmt.Errorf("read %s: %w", path, err))
		return
	}
	before, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		logManagedDoltOOMScoreAdj(log, strings.TrimSpace(string(data)), "unknown", fmt.Errorf("parse %s: %w", path, err))
		return
	}
	after, lowerErr := lowerOOMScoreAdjByBisection(before, func(value int) error {
		return os.WriteFile(path, []byte(strconv.Itoa(value)), 0o644)
	})
	logManagedDoltOOMScoreAdj(log, strconv.Itoa(before), strconv.Itoa(after), lowerErr)
}

func lowerOOMScoreAdjByBisection(current int, write func(int) error) (int, error) {
	if current <= 0 {
		return current, nil
	}
	zeroErr := write(0)
	if zeroErr == nil {
		return 0, nil
	}

	best := current
	lastErr := zeroErr
	for low, high := 1, current-1; low <= high; {
		candidate := low + (high-low)/2
		if err := write(candidate); err != nil {
			lastErr = err
			low = candidate + 1
		} else {
			best = candidate
			high = candidate - 1
		}
	}
	if best == current {
		return current, fmt.Errorf("lower oom_score_adj below %d: %w", current, lastErr)
	}
	return best, nil
}

func logManagedDoltOOMScoreAdj(log io.Writer, before, after string, err error) {
	if log == nil {
		return
	}
	if err != nil {
		_, _ = fmt.Fprintf(log, "gc managed dolt: oom_score_adj before=%s after=%s (lowering failed: %v)\n", before, after, err)
		return
	}
	_, _ = fmt.Fprintf(log, "gc managed dolt: oom_score_adj before=%s after=%s\n", before, after)
}
