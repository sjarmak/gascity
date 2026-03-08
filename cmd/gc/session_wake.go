package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// preWakeCommit persists a new incarnation (generation + token) BEFORE
// starting the process. This is Phase 1 of the two-phase wake protocol.
// Returns the new generation and instance token on success.
func preWakeCommit(
	session beads.Bead,
	store beads.Store,
	clk clock.Clock,
) (newGen int, token string, err error) {
	name := session.Metadata["session_name"]
	if !sessionNamePattern.MatchString(name) {
		return 0, "", fmt.Errorf("invalid session_name %q", name)
	}

	gen, _ := strconv.Atoi(session.Metadata["generation"])
	newGen = gen + 1
	token = generateToken()

	batch := map[string]string{
		"generation":     strconv.Itoa(newGen),
		"instance_token": token,
		"last_woke_at":   clk.Now().UTC().Format(time.RFC3339),
		"sleep_reason":   "",
	}
	if err := store.SetMetadataBatch(session.ID, batch); err != nil {
		return 0, "", fmt.Errorf("pre-wake metadata commit: %w", err)
	}
	// Update in-memory snapshot.
	if session.Metadata == nil {
		session.Metadata = make(map[string]string)
	}
	for k, v := range batch {
		session.Metadata[k] = v
	}

	return newGen, token, nil
}

// validateWorkDir ensures the path is safe to use as a working directory.
func validateWorkDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if abs != filepath.Clean(abs) {
		return fmt.Errorf("non-canonical path")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	return nil
}

// beginSessionDrain initiates an async drain. Returns immediately.
// The drainTracker stores in-memory state; advanceSessionDrains progresses it.
func beginSessionDrain(
	session beads.Bead,
	sp runtime.Provider,
	dt *drainTracker,
	reason string,
	clk clock.Clock,
	timeout time.Duration,
) {
	if dt.get(session.ID) != nil {
		return // already draining
	}
	gen, _ := strconv.Atoi(session.Metadata["generation"])

	dt.set(session.ID, &drainState{
		startedAt:  clk.Now(),
		deadline:   clk.Now().Add(timeout),
		reason:     reason,
		generation: gen,
	})

	// Best-effort drain signal: interrupt the process.
	// Verify instance_token before signaling.
	_ = verifiedInterrupt(session, sp)
}

// cancelSessionDrain removes a drain if wake reasons reappeared for the same generation.
func cancelSessionDrain(session beads.Bead, dt *drainTracker) bool {
	ds := dt.get(session.ID)
	if ds == nil {
		return false
	}
	gen, _ := strconv.Atoi(session.Metadata["generation"])
	if gen == ds.generation {
		dt.remove(session.ID)
		return true
	}
	return false
}

// advanceSessionDrains checks all in-progress drains. Called once per tick.
func advanceSessionDrains(
	dt *drainTracker,
	sp runtime.Provider,
	store beads.Store,
	sessionLookup func(id string) *beads.Bead,
	cfg *config.City,
	poolDesired map[string]int,
	clk clock.Clock,
) {
	for id, ds := range dt.all() {
		session := sessionLookup(id)
		if session == nil {
			dt.remove(id)
			continue
		}

		// Stale check: if session was re-woken (generation changed), cancel drain.
		gen, _ := strconv.Atoi(session.Metadata["generation"])
		if gen != ds.generation {
			dt.remove(id)
			continue
		}

		// Cancelation check: if wake reasons reappeared, cancel drain.
		// Config-drift drains are NOT cancelable — the config changed.
		if ds.reason != "config-drift" {
			reasons := wakeReasons(*session, cfg, sp, poolDesired, clk)
			if len(reasons) > 0 {
				dt.remove(id)
				continue
			}
		}

		name := session.Metadata["session_name"]

		// Check if process exited.
		if !sp.IsRunning(name) {
			// Process exited — drain complete.
			completeDrain(session, store, ds, clk)
			dt.remove(id)
			continue
		}

		if clk.Now().After(ds.deadline) {
			// Drain timed out — force stop.
			if err := verifiedStop(*session, sp); err != nil {
				// Token mismatch means session was re-woken by a different
				// incarnation — this drain is stale. Cancel it.
				dt.remove(id)
				continue
			}
			// Re-probe after stop to confirm process actually exited
			// before marking metadata as asleep.
			if !sp.IsRunning(name) {
				completeDrain(session, store, ds, clk)
			}
			dt.remove(id)
		}
		// Else: still draining, check again next tick.
	}
}

// completeDrain writes drain-complete metadata to the bead.
func completeDrain(session *beads.Bead, store beads.Store, ds *drainState, clk clock.Clock) {
	batch := map[string]string{
		"slept_at":     clk.Now().UTC().Format(time.RFC3339),
		"sleep_reason": ds.reason,
		"state":        "asleep",
		"last_woke_at": "", // Clear to prevent false crash detection.
	}
	if err := store.SetMetadataBatch(session.ID, batch); err == nil {
		if session.Metadata == nil {
			session.Metadata = make(map[string]string)
		}
		for k, v := range batch {
			session.Metadata[k] = v
		}
	}
}

// verifiedStop stops a session after verifying the instance_token matches.
// Prevents stale drain operations from targeting a re-woken session.
func verifiedStop(session beads.Bead, sp runtime.Provider) error {
	name := session.Metadata["session_name"]
	expectedToken := session.Metadata["instance_token"]
	if expectedToken != "" {
		actualToken, _ := sp.GetMeta(name, "GC_INSTANCE_TOKEN")
		if actualToken != "" && actualToken != expectedToken {
			return fmt.Errorf("instance token mismatch for session %s", session.ID)
		}
	}
	return sp.Stop(name)
}

// verifiedInterrupt sends an interrupt signal after verifying instance_token.
func verifiedInterrupt(session beads.Bead, sp runtime.Provider) error {
	name := session.Metadata["session_name"]
	expectedToken := session.Metadata["instance_token"]
	if expectedToken != "" {
		actualToken, _ := sp.GetMeta(name, "GC_INSTANCE_TOKEN")
		if actualToken != "" && actualToken != expectedToken {
			return fmt.Errorf("instance token mismatch for session %s", session.ID)
		}
	}
	return sp.Interrupt(name)
}

// needsConfigRestart returns true if the session's core config has drifted
// and needs a drain-then-restart cycle.
func needsConfigRestart(session beads.Bead, cfg *config.City, buildConfigFn func(*config.Agent) runtime.Config) bool {
	template := session.Metadata["template"]
	agent := findAgentByTemplate(cfg, template)
	if agent == nil {
		return false
	}
	storedHash := session.Metadata["config_hash"]
	if storedHash == "" {
		return false // no hash stored yet — can't detect drift
	}
	currentHash := runtime.CoreFingerprint(buildConfigFn(agent))
	return storedHash != currentHash
}
