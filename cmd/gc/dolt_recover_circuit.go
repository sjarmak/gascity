package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gastownhall/gascity/internal/fsys"
)

// managedDoltRecoveryCircuitFileName is the per-city persisted circuit state
// that coalesces AUTOMATIC managed-dolt recovery across independent processes
// (supervisor, dispatcher, health orders, bd-retry CLI invocations). It lives
// next to the lifecycle lock in the pack state dir so every process that can
// win the flock resolves the same file, including under GC_PACK_STATE_DIR /
// GC_CITY_RUNTIME_DIR overrides.
const managedDoltRecoveryCircuitFileName = "dolt-recovery-circuit.json"

// managedDoltRecoveryCircuitNow is the clock for circuit accounting.
// Stubbable for tests. Tests that swap it MUST NOT call t.Parallel().
var managedDoltRecoveryCircuitNow = time.Now

const (
	// managedDoltRecoveryCircuitBaseCooldown paces consecutive automatic
	// recovery attempts. Aligned with providerRecoverCooldown (the
	// process-local first-layer filter in beads_provider_lifecycle.go) so
	// the two layers agree on the minimum recover cadence.
	managedDoltRecoveryCircuitBaseCooldown = 30 * time.Second
	// managedDoltRecoveryCircuitMaxCooldown bounds the exponential backoff:
	// automatic recovery is never deferred longer than this, so a genuinely
	// degraded store always gets another attempt within the cap.
	managedDoltRecoveryCircuitMaxCooldown = 5 * time.Minute
	// managedDoltRecoveryFailureMemory is the flap-hysteresis window: a
	// failure older than this no longer escalates the next backoff. A
	// healthy observation deliberately does NOT zero the failure count —
	// a server flapping around the cooldown boundary would otherwise reset
	// the breaker on every brief "up" and never accumulate backoff.
	managedDoltRecoveryFailureMemory = 10 * time.Minute
)

var (
	// errManagedDoltRecoveryInProgress is returned by automatic recovery
	// losers: another process holds the lifecycle lock and no healthy server
	// was observed within the wait budget. Losers never queue for ownership.
	errManagedDoltRecoveryInProgress = errors.New("managed dolt recovery already in progress in another process")
	// errManagedDoltRecoveryCircuitOpen is returned when the persisted
	// per-city circuit defers an automatic recovery attempt.
	errManagedDoltRecoveryCircuitOpen = errors.New("automatic managed dolt recovery is on cooldown")
)

// managedDoltRecoveryCircuit is the persisted circuit state. Only the
// lifecycle-lock winner ever writes it (gate check and stamp both happen
// under the exclusive flock), so atomic-rename writes are race-free.
type managedDoltRecoveryCircuit struct {
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastFailureAt       time.Time `json:"last_failure_at"`
	OpenUntil           time.Time `json:"open_until"`
	LastError           string    `json:"last_error"`
	AttemptPID          int       `json:"attempt_pid"`
	LastAttemptAt       time.Time `json:"last_attempt_at"`
}

// isZero reports whether the circuit carries no history at all. Fields are
// checked explicitly (time.Time via IsZero) rather than by struct equality,
// which is fragile for time.Time internals after a JSON round-trip.
func (c managedDoltRecoveryCircuit) isZero() bool {
	return c.ConsecutiveFailures == 0 && c.AttemptPID == 0 && c.LastError == "" &&
		c.LastFailureAt.IsZero() && c.OpenUntil.IsZero() && c.LastAttemptAt.IsZero()
}

func managedDoltRecoveryCircuitPath(layout managedDoltRuntimeLayout) string {
	return filepath.Join(layout.PackStateDir, managedDoltRecoveryCircuitFileName)
}

// readManagedDoltRecoveryCircuit loads the circuit state. An absent or
// unreadable/corrupt file reads as the zero circuit: the breaker fails open
// (recovery allowed) rather than wedging automatic recovery on bad state.
func readManagedDoltRecoveryCircuit(layout managedDoltRuntimeLayout) managedDoltRecoveryCircuit {
	data, err := os.ReadFile(managedDoltRecoveryCircuitPath(layout))
	if err != nil {
		return managedDoltRecoveryCircuit{}
	}
	var circuit managedDoltRecoveryCircuit
	if err := json.Unmarshal(data, &circuit); err != nil {
		return managedDoltRecoveryCircuit{}
	}
	return circuit
}

// writeManagedDoltRecoveryCircuitFn is the circuit persistence hook.
// Stubbable for tests that need to force a circuit write failure.
// Tests that swap it MUST NOT call t.Parallel().
var writeManagedDoltRecoveryCircuitFn = writeManagedDoltRecoveryCircuit

func writeManagedDoltRecoveryCircuit(layout managedDoltRuntimeLayout, circuit managedDoltRecoveryCircuit) error {
	path := managedDoltRecoveryCircuitPath(layout)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create managed dolt recovery circuit dir: %w", err)
	}
	data, err := json.Marshal(circuit)
	if err != nil {
		return fmt.Errorf("encode managed dolt recovery circuit: %w", err)
	}
	data = append(data, '\n')
	if err := fsys.WriteFileAtomic(fsys.OSFS{}, path, data, 0o644); err != nil {
		return fmt.Errorf("write managed dolt recovery circuit: %w", err)
	}
	return nil
}

// bumpManagedDoltRecoveryFailures returns the next consecutive-failure count:
// escalating while the previous failure is inside the failure-memory window,
// resetting to a fresh first failure once the memory has decayed.
func bumpManagedDoltRecoveryFailures(circuit managedDoltRecoveryCircuit, now time.Time) int {
	if circuit.LastFailureAt.IsZero() || now.Sub(circuit.LastFailureAt) > managedDoltRecoveryFailureMemory {
		return 1
	}
	return circuit.ConsecutiveFailures + 1
}

// managedDoltRecoveryBackoff maps a consecutive-failure count to the deferral
// window before the next automatic attempt: base * 2^(n-1), capped.
func managedDoltRecoveryBackoff(failures int) time.Duration {
	backoff := managedDoltRecoveryCircuitBaseCooldown
	for i := 1; i < failures; i++ {
		backoff *= 2
		if backoff >= managedDoltRecoveryCircuitMaxCooldown {
			return managedDoltRecoveryCircuitMaxCooldown
		}
	}
	return backoff
}

// beginManagedDoltRecoveryAttempt gates and stamps a destructive recovery
// attempt. It MUST be called while holding the lifecycle flock: the exclusive
// lock is what makes the read-gate-stamp sequence race-free across processes.
//
// A stale AttemptPID means a prior owner began recovery and never completed —
// the flock is released on process death, so the crash is accounted as a
// failure (escalating future backoff) before this attempt proceeds.
//
// Automatic attempts are refused while the pacing window is open; operator
// attempts bypass the gate. Granted attempts stamp AttemptPID and extend the
// window by the base cooldown so a crash of THIS owner still paces the fleet.
func beginManagedDoltRecoveryAttempt(layout managedDoltRuntimeLayout, operator bool, cityPath, port string) error {
	now := managedDoltRecoveryCircuitNow()
	circuit := readManagedDoltRecoveryCircuit(layout)
	if circuit.AttemptPID != 0 {
		crashDiag := fmt.Sprintf("previous recovery attempt (pid %d, started %s) did not complete", circuit.AttemptPID, circuit.LastAttemptAt.Format(time.RFC3339))
		circuit.ConsecutiveFailures = bumpManagedDoltRecoveryFailures(circuit, now)
		circuit.LastFailureAt = now
		circuit.LastError = crashDiag
		circuit.AttemptPID = 0
	}
	if !operator && now.Before(circuit.OpenUntil) {
		// Persist the crash accounting even when refusing the attempt. If
		// this write fails the stale AttemptPID stays on disk and the next
		// caller re-counts the same crash — acceptable: it only escalates
		// backoff, consistent with failing open on read and closed on pacing.
		if err := writeManagedDoltRecoveryCircuit(layout, circuit); err != nil {
			return err
		}
		return fmt.Errorf("%w until %s after %d failed attempt(s) (last error: %s); run 'gc dolt-state recover-managed --city %s --port %s --operator' to recover now",
			errManagedDoltRecoveryCircuitOpen, circuit.OpenUntil.Format(time.RFC3339), circuit.ConsecutiveFailures, circuit.LastError, cityPath, port)
	}
	circuit.AttemptPID = os.Getpid()
	circuit.LastAttemptAt = now
	if until := now.Add(managedDoltRecoveryCircuitBaseCooldown); circuit.OpenUntil.Before(until) {
		circuit.OpenUntil = until
	}
	return writeManagedDoltRecoveryCircuit(layout, circuit)
}

// recordManagedDoltRecoveryFailure closes out a failed attempt. Stop aborts
// (the lock-safe stop could not release the data dir — usually a transient
// mid-flush condition) get a fixed base pacing window without escalating the
// breaker; every other failure escalates the exponential backoff.
func recordManagedDoltRecoveryFailure(layout managedDoltRuntimeLayout, cause error, stopAbort bool) error {
	now := managedDoltRecoveryCircuitNow()
	circuit := readManagedDoltRecoveryCircuit(layout)
	if stopAbort {
		circuit.OpenUntil = now.Add(managedDoltRecoveryCircuitBaseCooldown)
	} else {
		circuit.ConsecutiveFailures = bumpManagedDoltRecoveryFailures(circuit, now)
		circuit.LastFailureAt = now
		circuit.OpenUntil = now.Add(managedDoltRecoveryBackoff(circuit.ConsecutiveFailures))
	}
	circuit.AttemptPID = 0
	circuit.LastError = cause.Error()
	return writeManagedDoltRecoveryCircuit(layout, circuit)
}

// markManagedDoltRecoveryHealthy closes out a healthy outcome: the pacing
// window, in-progress marker, and last error are cleared so the next needed
// recovery is not deferred, while the failure count and timestamp are KEPT —
// failure memory decays by time (managedDoltRecoveryFailureMemory), not by a
// single healthy observation, so a flapping server keeps escalating.
func markManagedDoltRecoveryHealthy(layout managedDoltRuntimeLayout) error {
	circuit := readManagedDoltRecoveryCircuit(layout)
	if circuit.isZero() {
		return nil
	}
	circuit.OpenUntil = time.Time{}
	circuit.AttemptPID = 0
	circuit.LastError = ""
	return writeManagedDoltRecoveryCircuitFn(layout, circuit)
}
