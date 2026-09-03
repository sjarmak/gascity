//go:build linux || darwin

package proctable

import (
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestKillByPIDRefusesLowPIDs(t *testing.T) {
	for _, pid := range []int{-1, 0, 1} {
		if err := KillByPID(pid); err == nil {
			t.Errorf("KillByPID(%d) succeeded, want error", pid)
		}
	}
}

func TestKillByPIDAlreadyGoneIsSuccess(t *testing.T) {
	// Spawn a short-lived process and wait for it to exit, then try to kill it.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawning test process: %v", err)
	}
	pid := cmd.ProcessState.Pid()
	// Process is already gone. KillByPID should return nil (ESRCH → success).
	if err := KillByPID(pid); err != nil {
		t.Fatalf("KillByPID(%d) for already-dead process: %v", pid, err)
	}
}

func TestSignalPIDGroupThenFallback(t *testing.T) {
	var got []int
	err := signalPIDWith(12345, syscall.SIGTERM, func(pid int, sig syscall.Signal) error {
		if sig != syscall.SIGTERM {
			t.Fatalf("signal = %v, want SIGTERM", sig)
		}
		got = append(got, pid)
		if pid < 0 {
			return syscall.ESRCH
		}
		return nil
	})
	if err != nil {
		t.Fatalf("signalPIDWith(): %v", err)
	}
	want := []int{-12345, 12345}
	if len(got) != len(want) {
		t.Fatalf("signal calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("signal calls = %v, want %v", got, want)
		}
	}
}

func TestSignalPIDGroupSuccessSkipsFallback(t *testing.T) {
	var got []int
	err := signalPIDWith(12345, syscall.SIGTERM, func(pid int, sig syscall.Signal) error {
		if sig != syscall.SIGTERM {
			t.Fatalf("signal = %v, want SIGTERM", sig)
		}
		got = append(got, pid)
		return nil
	})
	if err != nil {
		t.Fatalf("signalPIDWith(): %v", err)
	}
	want := []int{-12345}
	if !slices.Equal(got, want) {
		t.Fatalf("signal calls = %v, want %v", got, want)
	}
}

// TestKillByPIDConfirmedDeadBeforeReturn drives the injected core: a process
// still runnable after SIGKILL (e.g. wedged in D-state) must yield an error so
// a caller can refuse to start a racing replacement, while one that becomes
// dead (gone or zombie) after SIGKILL returns nil.
func TestKillByPIDConfirmedDeadBeforeReturn(t *testing.T) {
	t.Run("survives SIGKILL -> error", func(t *testing.T) {
		var signals []syscall.Signal
		kill := func(_ int, sig syscall.Signal) error {
			// Record every delivery attempt. signalPIDWith signals the process
			// group (negative pid) first and returns on success, so with this
			// always-succeeding fake these are the group deliveries; the
			// assertion below only checks the final escalation is SIGKILL.
			signals = append(signals, sig)
			return nil
		}
		termLive := func(int) bool { return true } // never exits on SIGTERM
		runLive := func(int) bool { return true }  // survives SIGKILL too
		err := killByPID(4321, kill, termLive, runLive, 5*time.Millisecond, 5*time.Millisecond)
		if err == nil {
			t.Fatal("killByPID returned nil for a process that survived SIGKILL")
		}
		if !strings.Contains(err.Error(), "not confirmed dead") {
			t.Fatalf("error = %v, want 'not confirmed dead'", err)
		}
		if len(signals) == 0 || signals[len(signals)-1] != syscall.SIGKILL {
			t.Fatalf("signals = %v, want SIGKILL escalation", signals)
		}
	})

	t.Run("dies after SIGKILL -> nil", func(t *testing.T) {
		kill := func(int, syscall.Signal) error { return nil }
		termLive := func(int) bool { return true } // ignores SIGTERM
		var kills int
		runLive := func(int) bool {
			kills++
			return kills <= 1 // alive on first confirm poll, dead after
		}
		if err := killByPID(4321, kill, termLive, runLive, 5*time.Millisecond, time.Second); err != nil {
			t.Fatalf("killByPID: %v", err)
		}
	})

	t.Run("exits during SIGTERM grace -> no SIGKILL", func(t *testing.T) {
		var sawKill bool
		kill := func(_ int, sig syscall.Signal) error {
			if sig == syscall.SIGKILL {
				sawKill = true
			}
			return nil
		}
		var polls int
		termLive := func(int) bool {
			polls++
			return polls <= 1 // alive at entry, exits before grace elapses
		}
		runLive := func(int) bool { return false }
		if err := killByPID(4321, kill, termLive, runLive, time.Second, time.Second); err != nil {
			t.Fatalf("killByPID: %v", err)
		}
		if sawKill {
			t.Fatal("SIGKILL sent even though the process exited during grace")
		}
	})
}

func TestWaitUntilRespectsZeroTimeout(t *testing.T) {
	if !waitUntil(func() bool { return true }, 0) {
		t.Fatal("waitUntil should observe an already-satisfied condition at zero timeout")
	}
	if waitUntil(func() bool { return false }, 0) {
		t.Fatal("waitUntil should report false when the condition never holds at zero timeout")
	}
}

// TestThrottleLimitsUnderlyingChecks is the regression test for the reap-loop
// doubling: a caller polling the throttled function every tick (as waitUntil
// does every 25ms) must not drive the wrapped check any more often than once
// per interval.
func TestThrottleLimitsUnderlyingChecks(t *testing.T) {
	var calls int
	check := func() bool {
		calls++
		return true
	}
	current := time.Unix(0, 0)
	now := func() time.Time { return current }
	throttled := throttle(100*time.Millisecond, now, check)

	if !throttled() {
		t.Fatal("throttled() = false, want true")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 after the first call", calls)
	}

	// Simulate waitUntil's 25ms ticks landing inside the throttle window.
	for i := 0; i < 3; i++ {
		current = current.Add(25 * time.Millisecond)
		throttled()
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 while every tick lands inside the throttle interval", calls)
	}

	// Once the interval elapses, the next call re-invokes check.
	current = current.Add(100 * time.Millisecond)
	if !throttled() {
		t.Fatal("throttled() = false after the interval elapsed")
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 after the interval elapsed", calls)
	}
}

// TestThrottleNeverInventsATrueResult pins the safe staleness direction: a
// cached false must not be papered over by a stale true, and a transition to
// true is only ever visible once the underlying check actually observes it.
func TestThrottleNeverInventsATrueResult(t *testing.T) {
	calls := 0
	check := func() bool {
		calls++
		return calls > 1 // false on the first real check, true afterward
	}
	current := time.Unix(0, 0)
	now := func() time.Time { return current }
	throttled := throttle(50*time.Millisecond, now, check)

	if throttled() {
		t.Fatal("throttled() = true on the first (false) check")
	}
	current = current.Add(10 * time.Millisecond)
	if throttled() {
		t.Fatal("throttled() = true from cache before the interval elapsed")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 before the interval elapsed", calls)
	}

	current = current.Add(50 * time.Millisecond)
	if !throttled() {
		t.Fatal("throttled() = false after the interval elapsed and the underlying check turned true")
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 after the interval elapsed", calls)
	}
}
