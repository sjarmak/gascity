//go:build linux || darwin

package proctable

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/runtime"
)

// startTimeRecheckInterval bounds how often the post-SIGKILL reap poll
// re-verifies PID identity via pidutil.AliveWithStartTime. waitUntil ticks
// every 25ms; where /proc is unavailable (darwin) that check is itself a `ps`
// invocation on top of the zombie check Alive already performs, so
// re-running it on every tick roughly doubled process churn in the slow reap
// path. The ps fallback also only carries one-second resolution (see
// pidutil.StartTime), so re-checking far less often than 25ms loses nothing
// a caller could have observed anyway.
const startTimeRecheckInterval = 200 * time.Millisecond

// KillByPID terminates pid with SIGTERM, then SIGKILL after
// runtime.ManagedProcessStopGrace, then waits (bounded by
// runtime.ManagedProcessReapGrace) for the process to be confirmed dead — gone
// or a zombie — before returning. Already-gone processes are success. A process
// that survives its own SIGKILL past the reap grace (e.g. wedged in D-state
// under I/O) yields an error so callers can refuse to start a name-reused
// replacement that would race it for the same work.
func KillByPID(pid int) error {
	// Capture the target's start-time identity BEFORE signaling. During the
	// post-SIGKILL reap wait the PID can be reaped and recycled to an unrelated
	// process; without this, a recycled PID reads as "still alive" and we would
	// wrongly report a target that is actually gone as not-confirmed-dead,
	// spuriously refusing a legitimate Start. StartTime reads /proc where it
	// exists and falls back to ps elsewhere, so it is empty only when neither
	// mechanism can answer, in which case runLive falls back to plain liveness
	// — current behavior preserved.
	startTime, _ := pidutil.StartTime(pid)
	runLive := throttle(startTimeRecheckInterval, time.Now, func() bool {
		return pidutil.AliveWithStartTime(pid, startTime)
	})
	return killByPID(
		pid,
		syscall.Kill,
		pidAlive,
		func(int) bool { return runLive() },
		runtime.ManagedProcessStopGrace,
		runtime.ManagedProcessReapGrace,
	)
}

// throttle wraps check so it runs at most once per interval; between runs it
// returns the last result rather than re-invoking check. The first call
// always invokes check, matching waitUntil's own up-front check of an
// already-satisfied condition.
//
// This is safe for a liveness/identity probe specifically because the only
// value that can go stale between real checks is "still alive" (true): a
// stale true just delays noticing a transition to false by up to interval,
// it never fabricates one. Callers that bound the overall wait (waitUntil's
// timeout) still get a correct final answer, just possibly interval late.
func throttle(interval time.Duration, now func() time.Time, check func() bool) func() bool {
	var (
		last   bool
		lastAt time.Time
		primed bool
	)
	return func() bool {
		if primed && now().Sub(lastAt) < interval {
			return last
		}
		last = check()
		lastAt = now()
		primed = true
		return last
	}
}

// killByPID is the signal/confirm core with its syscalls injected so the
// confirmed-dead-before-return contract can be unit-tested without real
// processes. termLive is the cheap kill(0) liveness used during the SIGTERM
// grace window (a zombie still counts as live here, matching prior behavior).
// runLive reports whether the process is still runnable — false once it is gone
// or a zombie, since a zombie can no longer execute and therefore cannot race a
// replacement.
func killByPID(
	pid int,
	kill func(int, syscall.Signal) error,
	termLive func(int) bool,
	runLive func(int) bool,
	grace, reapGrace time.Duration,
) error {
	if pid <= 1 {
		return fmt.Errorf("proctable: refusing to kill PID %d", pid)
	}
	if !termLive(pid) {
		return nil
	}
	if err := signalPIDWith(pid, syscall.SIGTERM, kill); err != nil {
		return fmt.Errorf("signal PID %d with SIGTERM: %w", pid, err)
	}
	if waitUntil(func() bool { return !termLive(pid) }, grace) {
		return nil
	}
	if err := signalPIDWith(pid, syscall.SIGKILL, kill); err != nil {
		return fmt.Errorf("signal PID %d with SIGKILL: %w", pid, err)
	}
	if waitUntil(func() bool { return !runLive(pid) }, reapGrace) {
		return nil
	}
	return fmt.Errorf("proctable: PID %d still runnable %s after SIGKILL (not confirmed dead)", pid, reapGrace)
}

// waitUntil polls done at 25ms until it reports true or timeout elapses,
// returning done's final result. Checked once up front so a zero timeout still
// observes an already-satisfied condition.
func waitUntil(done func() bool, timeout time.Duration) bool {
	if done() {
		return true
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			return done()
		case <-ticker.C:
			if done() {
				return true
			}
		}
	}
}

func signalPIDWith(pid int, sig syscall.Signal, kill func(int, syscall.Signal) error) error {
	if err := kill(-pid, sig); err == nil {
		return nil
	}
	err := kill(pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
