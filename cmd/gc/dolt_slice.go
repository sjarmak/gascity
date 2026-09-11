package main

// Systemd slice placement and OOM hardening for managed dolt sql-servers.
//
// gc auto-starts the managed server from whichever process first needs it —
// the supervisor, a control-dispatcher pane, an interactive command. Nothing
// placed that process tree in a cgroup of its own, so it inherited the
// starter's, and the shared bead store for every rig in the city ended up
// wherever the race happened to land it. Observed on one host: the canonical
// server sat in the agent slice, was OOM-killed four times in a single morning
// once that slice reached its MemoryMax, and respawned on a different port
// each time, opening the city-wide dolt circuit breaker.
//
// Two independent inheritances caused that, and both are corrected here.
//
// Cgroup: the server is placed in its own top-level slice, so a memory
// blow-out among agents can no longer select it — the kernel's memcg OOM
// killer only considers tasks inside the cgroup that hit its limit. The slice
// must NOT be a descendant of the agent slice for this to hold; see
// an explicitly configured top-level slice.
//
// oom_score_adj: systemd's user manager applies DefaultOOMScoreAdjust=200 to
// the units under it, and that value is inherited by every descendant process.
// On a 62 GiB host it adds a ~12.5 GiB-equivalent bonus to the kernel's
// badness score, so a ~1 GiB dolt was ranked as though it were ~13 GiB and
// picked ahead of genuinely large processes. Lowering it toward 0 removes as
// much of that bonus as the user manager's unprivileged floor permits;
// negative values need CAP_SYS_RESOURCE.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/runtime/systemdscope"
)

const (
	// managedDoltSliceEnv overrides the systemd user slice that managed dolt
	// sql-servers are placed in. Placement is explicit opt-in: unset preserves
	// the existing direct-spawn behavior, while explicit empty is rejected by
	// the production placement boundary.
	managedDoltSliceEnv = "GC_DOLT_SLICE"

	// The reference host observed a 5.7 GiB managed-Dolt peak during the
	// incident window. Eight GiB leaves bounded headroom for that working set,
	// the watchdog, and short compaction bursts without allowing the shared
	// store to consume the whole user slice. MemoryLow reserves two GiB under
	// systemd-oomd pressure; it is protection, not a guarantee, when an
	// ancestor does not enable memory protection.
	managedDoltMemoryMaxBytes            = "8589934592"
	managedDoltMemoryLowBytes            = "2147483648"
	managedDoltManagedOOMPreference      = "avoid"
	managedDoltPlacementOperationTimeout = 5 * time.Second

	// managedDoltOOMScoreAdj is the preferred badness target. Application finds
	// the lowest accepted value at or above it when the kernel enforces a higher
	// unprivileged floor. Negative values additionally require CAP_SYS_RESOURCE.
	managedDoltOOMScoreAdj = 0

	// procSelfOOMScoreAdj is the calling process's badness knob. Writing it
	// before spawning the server is how the value reaches the server: it is
	// inherited across fork, and Go's os/exec exposes no pre-exec hook.
	procSelfOOMScoreAdj = "/proc/self/oom_score_adj"
)

// managedDoltSliceWrapper carries the once-only systemd-run availability probe
// for managed dolt spawns. Managed-Dolt callers use WrapRequired and fail
// closed; the shared wrapper's graceful form remains available to other
// process-placement users.
var managedDoltSliceWrapper = &systemdscope.Wrapper{Label: managedDoltSliceEnv}

var managedDoltEnsureSlice = (systemdscope.Manager{}).EnsureSlice

// managedDoltSlice resolves the slice for managed dolt spawns.
func managedDoltSlice() string {
	value, ok := os.LookupEnv(managedDoltSliceEnv)
	return managedDoltSliceFor(managedDoltTestModeEnabled(), value, ok)
}

// managedDoltSliceFor is the pure decision behind managedDoltSlice, split out
// for tests.
//
// An explicit setting always wins at resolution time. The production boundary
// rejects an explicitly configured empty result at the placement boundary;
// keeping that case in this pure resolver lets tests pin configuration apart
// from enforcement. An unset value preserves the existing direct spawn.
func managedDoltSliceFor(testMode bool, envValue string, envSet bool) string {
	if envSet {
		return strings.TrimSpace(envValue)
	}
	if testMode {
		return ""
	}
	return ""
}

// prepareManagedDoltPlacement applies and verifies the bounded resource policy
// before a managed-Dolt spawn or adoption can be considered safe.
func prepareManagedDoltPlacement(ctx context.Context, slice string) error {
	if err := validateManagedDoltSliceName(slice); err != nil {
		return err
	}
	policy := systemdscope.SlicePolicy{
		MemoryMax:            managedDoltMemoryMaxBytes,
		MemoryLow:            managedDoltMemoryLowBytes,
		ManagedOOMPreference: managedDoltManagedOOMPreference,
	}
	if err := managedDoltEnsureSlice(ctx, slice, policy); err != nil {
		return fmt.Errorf("prepare managed dolt slice %s: %w", slice, err)
	}
	return nil
}

func validateManagedDoltSliceName(slice string) error {
	slice = strings.TrimSpace(slice)
	if !strings.HasSuffix(slice, ".slice") || filepath.Base(slice) != slice {
		return fmt.Errorf("managed dolt slice %q is not a systemd slice unit name", slice)
	}
	base := strings.TrimSuffix(slice, ".slice")
	if base == "" || strings.Contains(base, "-") {
		return fmt.Errorf("managed dolt slice %q is not top-level; systemd hyphens encode nested slices", slice)
	}
	return nil
}

// wrapManagedDoltArgv places a managed dolt spawn in its verified bounded
// slice. Production placement is fail-closed: no unwrapped fallback is
// returned when the slice policy or transient-scope probe fails.
//
// Wrapping is safe for the callers' PID bookkeeping: `systemd-run --scope`
// execs in place, so the PID observed from Cmd.Start is the spawned process's
// own PID, and the start-identity snapshot, termination guards and reaping all
// continue to address the right process.
func wrapManagedDoltArgv(argv []string) ([]string, error) {
	if supervisorRuntimeGOOS != "linux" {
		// The slice/scope/cgroup mechanism this package builds on is Linux-only
		// (systemd user manager + cgroup v2). Non-Linux hosts get the same
		// unwrapped spawn they had before this placement feature existed; see
		// the design doc's Non-goals.
		return argv, nil
	}
	slice := managedDoltSlice()
	_, explicitlyConfigured := os.LookupEnv(managedDoltSliceEnv)
	return wrapManagedDoltArgvFor(argv, slice, explicitlyConfigured)
}

func wrapManagedDoltArgvFor(argv []string, slice string, explicitlyConfigured bool) ([]string, error) {
	if slice == "" {
		// Unset preserves the existing direct spawn in every mode. An explicit
		// empty setting is not an opt-out: in production that would recreate
		// the incident path.
		if !explicitlyConfigured {
			return argv, nil
		}
		return nil, fmt.Errorf("managed dolt placement is required; %s is empty", managedDoltSliceEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), managedDoltPlacementOperationTimeout)
	defer cancel()
	if err := prepareManagedDoltPlacement(ctx, slice); err != nil {
		return nil, err
	}
	wrapped, err := managedDoltSliceWrapper.WrapRequired(slice, argv)
	if err != nil {
		return nil, fmt.Errorf("prepare managed dolt spawn: %w", err)
	}
	return wrapped, nil
}

// lowerOOMScoreAdjToFloor returns the lowest value in [target, current) that
// write accepted, or current if none was accepted. It never writes when current
// is at or below target, so an operator who deliberately protected the server
// further is not overridden. It only searches past a permission error, because a
// permission error is how the kernel reports an unprivileged floor. A
// non-permission error stops the search and returns the value already in effect.
// It never writes a value at or above one already accepted, so it only ever
// lowers the value.
func lowerOOMScoreAdjToFloor(current, target int, write func(int) error) (int, error) {
	if current <= target {
		return current, nil
	}
	firstErr := write(target)
	if firstErr == nil {
		return target, nil
	}
	if !errors.Is(firstErr, fs.ErrPermission) {
		return current, firstErr
	}

	lowest := current
	for low, high := target+1, current-1; low <= high; {
		candidate := low + (high-low)/2
		switch err := write(candidate); {
		case err == nil:
			lowest = candidate
			high = candidate - 1
		case errors.Is(err, fs.ErrPermission):
			low = candidate + 1
		default:
			return lowest, err
		}
	}
	if lowest == current {
		return current, fmt.Errorf("could not lower oom_score_adj value %d toward %d: %w", current, target, firstErr)
	}
	return lowest, nil
}

// managedDoltOOMScoreAdjMu serializes the lower/spawn/restore sequence on the
// watchdog-free path. oom_score_adj is per-process, and one supervisor process
// drives several cities, so two concurrent spawns could otherwise interleave
// such that the second server inherits the value the first restored.
var managedDoltOOMScoreAdjMu sync.Mutex

// applyManagedDoltOOMScoreAdj lowers the calling process's inherited badness
// bonus to the lowest value the kernel accepts at or above the preferred
// target, so a dolt server forked from it inherits that value. It reports the
// previous value — so a caller that must not keep the new one can hand it to
// [restoreManagedDoltOOMScoreAdj] — and whether any lowering occurred.
//
// The knob only exists on the calling process and is inherited across fork, and
// Go's os/exec offers no pre-exec hook, so writing it here is the only way to
// reach the child. Errors are returned rather than swallowed: callers log them,
// but none treats a failure as fatal, because failing to harden the server is
// never a reason to refuse to start it. Unavailable on non-Linux hosts and in
// restricted sandboxes.
func applyManagedDoltOOMScoreAdj() (previous int, changed bool, err error) {
	raw, err := os.ReadFile(procSelfOOMScoreAdj)
	if err != nil {
		return 0, false, fmt.Errorf("read %s: %w", procSelfOOMScoreAdj, err)
	}
	current, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, false, fmt.Errorf("parse %s: %w", procSelfOOMScoreAdj, err)
	}
	lowest, err := lowerOOMScoreAdjToFloor(current, managedDoltOOMScoreAdj, func(value int) error {
		return os.WriteFile(procSelfOOMScoreAdj, []byte(strconv.Itoa(value)), 0o644)
	})
	return current, lowest < current, err
}

// restoreManagedDoltOOMScoreAdj puts back a value captured by a changed
// applyManagedDoltOOMScoreAdj call. The captured value is higher than the
// accepted value that was written, and raising oom_score_adj is permitted.
func restoreManagedDoltOOMScoreAdj(previous int) error {
	if err := os.WriteFile(procSelfOOMScoreAdj, []byte(strconv.Itoa(previous)), 0o644); err != nil {
		return fmt.Errorf("restore %s to %d: %w", procSelfOOMScoreAdj, previous, err)
	}
	return nil
}

// managedDoltWorkingDir returns an explicit working directory for managed
// Dolt server and watchdog processes. Recovery/test callers may omit the city
// path; in that case use the config file's directory rather than inheriting
// an unrelated caller cwd.
func managedDoltWorkingDir(cityPath, configFile string) string {
	if strings.TrimSpace(cityPath) != "" {
		return cityPath
	}
	if dir := filepath.Dir(configFile); dir != "." {
		return dir
	}
	return ""
}
