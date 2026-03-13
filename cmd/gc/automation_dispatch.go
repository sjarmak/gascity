package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/automations"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
)

// automationDispatcher evaluates automation gate conditions and dispatches due
// automations as wisps or exec scripts. Follows the nil-guard tracker pattern:
// nil means no auto-dispatchable automations exist.
//
// dispatch is fire-and-forget: gate evaluation is synchronous, but each due
// automation's dispatch action runs in its own goroutine. The tracking bead
// is created before the goroutine launches to prevent re-fire on the next tick.
type automationDispatcher interface {
	dispatch(ctx context.Context, cityPath string, now time.Time)
}

// ExecRunner runs a shell command with context, working directory, and
// environment variables. Returns combined stdout or an error.
type ExecRunner func(ctx context.Context, command, dir string, env []string) ([]byte, error)

// shellExecRunner is the production ExecRunner using os/exec.
func shellExecRunner(ctx context.Context, command, dir string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), env...)
	return cmd.CombinedOutput()
}

// memoryAutomationDispatcher is the production implementation.
type memoryAutomationDispatcher struct {
	aa         []automations.Automation
	store      beads.Store
	ep         events.Provider
	runner     beads.CommandRunner
	execRun    ExecRunner
	rec        events.Recorder
	stderr     io.Writer
	maxTimeout time.Duration
}

// buildAutomationDispatcher scans formula layers for automations and returns a
// dispatcher. Returns nil if no auto-dispatchable automations are found.
// Scans both city-level and per-rig automations. Rig automations get their Rig
// field stamped so they use independent scoped labels.
func buildAutomationDispatcher(cityPath string, cfg *config.City, runner beads.CommandRunner, rec events.Recorder, stderr io.Writer) automationDispatcher {
	allAA, err := scanAllAutomations(cityPath, cfg, stderr, "gc start: automation scan")
	if err != nil {
		fmt.Fprintf(stderr, "gc start: automation scan: %v\n", err) //nolint:errcheck // best-effort stderr
		return nil
	}
	if len(cfg.Automations.Overrides) > 0 {
		if err := automations.ApplyOverrides(allAA, convertOverrides(cfg.Automations.Overrides)); err != nil {
			fmt.Fprintf(stderr, "gc start: automation overrides: %v\n", err) //nolint:errcheck // best-effort stderr
		}
	}

	// Filter out manual-gate automations — they are never auto-dispatched.
	var auto []automations.Automation
	for _, a := range allAA {
		if a.Gate != "manual" {
			auto = append(auto, a)
		}
	}
	if len(auto) == 0 {
		return nil
	}

	store := beads.NewBdStore(cityPath, runner)

	// Extract events.Provider from recorder if available.
	// FileRecorder implements Provider; Discard does not.
	var ep events.Provider
	if p, ok := rec.(events.Provider); ok {
		ep = p
	}

	return &memoryAutomationDispatcher{
		aa:         auto,
		store:      store,
		ep:         ep,
		runner:     runner,
		execRun:    shellExecRunner,
		rec:        rec,
		stderr:     stderr,
		maxTimeout: cfg.Automations.MaxTimeoutDuration(),
	}
}

func (m *memoryAutomationDispatcher) dispatch(ctx context.Context, cityPath string, now time.Time) {
	lastRunFn := automationLastRunFn(m.store)
	cursorFn := bdCursorFunc(m.store)

	for _, a := range m.aa {
		result := automations.CheckGate(a, now, lastRunFn, m.ep, cursorFn)
		if !result.Due {
			continue
		}

		// Skip dispatch if previous work hasn't been processed yet.
		scoped := a.ScopedName()
		if m.hasOpenWork(scoped) {
			continue
		}

		// Create tracking bead synchronously BEFORE dispatch goroutine.
		// This prevents the cooldown gate from re-firing on the next tick.
		trackingBead, err := m.store.Create(beads.Bead{
			Title:  "automation:" + scoped,
			Labels: []string{"automation-run:" + scoped, "automation-tracking"},
		})
		if err != nil {
			fmt.Fprintf(m.stderr, "gc: automation dispatch: creating tracking bead for %s: %v\n", scoped, err) //nolint:errcheck
			continue
		}

		// Fire and forget with timeout.
		a := a // capture loop variable
		go m.dispatchOne(ctx, a, cityPath, trackingBead.ID)
	}
}

// dispatchOne runs a single automation dispatch in its own goroutine.
// For exec automations, runs the script directly. For formula automations,
// instantiates a wisp. Emits events and updates the tracking bead.
func (m *memoryAutomationDispatcher) dispatchOne(ctx context.Context, a automations.Automation, cityPath, trackingID string) {
	defer m.store.Close(trackingID) //nolint:errcheck // best-effort close

	timeout := effectiveTimeout(a, m.maxTimeout)
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	scoped := a.ScopedName()
	m.rec.Record(events.Event{
		Type:    events.AutomationFired,
		Actor:   "controller",
		Subject: scoped,
	})

	if a.IsExec() {
		m.dispatchExec(childCtx, a, cityPath, trackingID)
	} else {
		m.dispatchWisp(childCtx, a, cityPath, trackingID)
	}
}

// dispatchExec runs an exec automation's shell command.
func (m *memoryAutomationDispatcher) dispatchExec(ctx context.Context, a automations.Automation, cityPath, trackingID string) {
	scoped := a.ScopedName()

	// Build env with AUTOMATION_DIR and PACK_DIR.
	var env []string
	if a.Source != "" {
		env = append(env, "AUTOMATION_DIR="+filepath.Dir(a.Source))
	}
	if a.FormulaLayer != "" {
		env = append(env, "PACK_DIR="+filepath.Dir(a.FormulaLayer))
	}

	output, err := m.execRun(ctx, a.Exec, cityPath, env)

	// Update tracking bead with outcome labels.
	labels := []string{"exec"}
	if err != nil {
		labels = append(labels, "exec-failed")
		fmt.Fprintf(m.stderr, "gc: automation exec %s failed: %v\n", scoped, err) //nolint:errcheck
		if len(output) > 0 {
			fmt.Fprintf(m.stderr, "gc: automation exec %s output: %s\n", scoped, output) //nolint:errcheck
		}
		m.rec.Record(events.Event{
			Type:    events.AutomationFailed,
			Actor:   "controller",
			Subject: scoped,
			Message: err.Error(),
		})
	} else {
		m.rec.Record(events.Event{
			Type:    events.AutomationCompleted,
			Actor:   "controller",
			Subject: scoped,
		})
	}

	// Label tracking bead with outcome via store (not CLI).
	m.store.Update(trackingID, beads.UpdateOpts{Labels: labels}) //nolint:errcheck // best-effort
}

// dispatchWisp instantiates a wisp from the automation's formula.
func (m *memoryAutomationDispatcher) dispatchWisp(ctx context.Context, a automations.Automation, cityPath, trackingID string) {
	scoped := a.ScopedName()

	if err := ctx.Err(); err != nil {
		m.rec.Record(events.Event{
			Type:    events.AutomationFailed,
			Actor:   "controller",
			Subject: scoped,
			Message: err.Error(),
		})
		m.store.Update(trackingID, beads.UpdateOpts{Labels: []string{"wisp", "wisp-canceled"}}) //nolint:errcheck // best-effort
		return
	}

	// Capture event head before wisp creation for event gates.
	var headSeq uint64
	if a.Gate == "event" && m.ep != nil {
		headSeq, _ = m.ep.LatestSeq()
	}

	rootID, err := m.store.MolCook(a.Formula, "", nil)
	if err != nil {
		m.rec.Record(events.Event{
			Type:    events.AutomationFailed,
			Actor:   "controller",
			Subject: scoped,
			Message: err.Error(),
		})
		return // best-effort: skip failed cook, don't crash
	}

	// Label wisp with automation-run:<scopedName> for tracking.
	args := []string{"update", rootID, "--add-label=automation-run:" + scoped}
	if a.Gate == "event" && m.ep != nil {
		args = append(args, fmt.Sprintf("--add-label=automation:%s", scoped))
		args = append(args, fmt.Sprintf("--add-label=seq:%d", headSeq))
	}
	if a.Pool != "" {
		pool := qualifyPool(a.Pool, a.Rig)
		args = append(args, fmt.Sprintf("--add-label=pool:%s", pool))
	}
	if _, err := m.runner(cityPath, "bd", args...); err != nil {
		// Label failure is critical for duplicate-dispatch prevention.
		// Log and emit an event so operators can investigate.
		fmt.Fprintf(m.stderr, "gc: automation %s: failed to label wisp %s: %v\n", scoped, rootID, err) //nolint:errcheck
		m.rec.Record(events.Event{
			Type:    events.AutomationFailed,
			Actor:   "controller",
			Subject: scoped,
			Message: fmt.Sprintf("wisp %s created but label failed: %v", rootID, err),
		})
		return
	}

	m.rec.Record(events.Event{
		Type:    events.AutomationCompleted,
		Actor:   "controller",
		Subject: scoped,
	})

	// Label tracking bead with outcome.
	m.store.Update(trackingID, beads.UpdateOpts{Labels: []string{"wisp"}}) //nolint:errcheck // best-effort
}

// hasOpenWork reports whether any non-closed work bead exists for this
// automation. Tracking beads (title "automation:<name>") are excluded —
// only actual work (wisps, exec results) counts. Returns false on error
// (fail open: allow dispatch rather than block).
func (m *memoryAutomationDispatcher) hasOpenWork(scopedName string) bool {
	results, err := m.store.ListByLabel("automation-run:"+scopedName, 0)
	if err != nil {
		return false
	}
	trackingTitle := "automation:" + scopedName
	for _, b := range results {
		if b.Status != "closed" && b.Title != trackingTitle {
			return true
		}
	}
	return false
}

// effectiveTimeout returns the timeout to use for an automation dispatch.
// Uses the automation's configured timeout (or default), capped by maxTimeout.
func effectiveTimeout(a automations.Automation, maxTimeout time.Duration) time.Duration {
	t := a.TimeoutOrDefault()
	if maxTimeout > 0 && t > maxTimeout {
		return maxTimeout
	}
	return t
}

// rigExclusiveLayers returns the suffix of rigLayers that is not in
// cityLayers. Since rig layers are built as [cityLayers..., rigTopoLayers...,
// rigLocalLayer], we strip the city prefix to avoid double-scanning city
// automations.
func rigExclusiveLayers(rigLayers, cityLayers []string) []string {
	if len(rigLayers) <= len(cityLayers) {
		return nil
	}
	return rigLayers[len(cityLayers):]
}

// qualifyPool prefixes an unqualified pool name with the rig name for
// rig-scoped automations. Already-qualified names (containing "/") are
// returned as-is. City automations (empty rig) are unchanged.
func qualifyPool(pool, rig string) string {
	if rig == "" || strings.Contains(pool, "/") {
		return pool
	}
	return rig + "/" + pool
}

// convertOverrides converts config.AutomationOverride to automations.Override.
func convertOverrides(cfgOvs []config.AutomationOverride) []automations.Override {
	out := make([]automations.Override, len(cfgOvs))
	for i, c := range cfgOvs {
		out[i] = automations.Override{
			Name:     c.Name,
			Rig:      c.Rig,
			Enabled:  c.Enabled,
			Gate:     c.Gate,
			Interval: c.Interval,
			Schedule: c.Schedule,
			Check:    c.Check,
			On:       c.On,
			Pool:     c.Pool,
			Timeout:  c.Timeout,
		}
	}
	return out
}
