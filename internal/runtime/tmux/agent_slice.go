package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/shellquote"
)

// AgentSliceEnv names the environment variable that, when set to a systemd
// user slice (e.g. "gascity-agents.slice"), makes the tmux provider wrap
// every pane's initial command in a transient systemd user scope:
//
//	systemd-run --user --scope --slice=<slice> --collect --quiet -- sh -c '<command>'
//
// Rationale: systemd-enabled tmux builds (stock Ubuntu) move every pane into
// a transient tmux-spawn-*.scope under the default user slice, so agent
// processes escape whatever slice the tmux server itself runs in. Wrapping
// the pane command re-parents the agent's process tree into a dedicated user
// slice where resource weights can be applied. Default-off: when unset, pane
// commands run unwrapped exactly as before.
const AgentSliceEnv = "GC_AGENT_SLICE"

const (
	// agentSlicePlacementAttempts bounds respawns when tmux's own transient
	// scope wins the race with systemd-run. A pane that still escapes is
	// terminated.
	agentSlicePlacementAttempts = 3
	// Placement must remain correct for 11 samples (500ms between the first
	// and last) before it is accepted. The full 41-sample window gives PID
	// startup and both asynchronous cgroup movers up to two seconds to settle.
	agentSlicePlacementChecks       = 41
	agentSlicePlacementStableChecks = 11
	agentSlicePlacementInterval     = 50 * time.Millisecond
)

// agentSliceProbeTimeout bounds the one-time systemd-run availability probe.
// Test-overridable.
var agentSliceProbeTimeout = 5 * time.Second

// wrapperCommands lists pane-root wrapper binaries produced by pane-command
// wrapping. A wrapped pane reports the wrapper as pane_current_command for
// the pane's whole lifetime, so command-wait and detection paths must treat
// these like shells: the agent is identified through descendant inspection,
// never by the pane command itself.
var wrapperCommands = []string{"systemd-run"}

// isWrapperCommand reports whether cmd is a known pane-root wrapper binary
// (see wrapperCommands).
func isWrapperCommand(cmd string) bool {
	for _, w := range wrapperCommands {
		if cmd == w {
			return true
		}
	}
	return false
}

// probeAgentSliceSupport verifies that systemd-run exists and the systemd
// user manager responds by running a no-op command in a transient scope on
// the target slice. The probe runs in the gc process's environment, while
// pane commands execute with the tmux server's environment. gc normally
// spawns the tmux server itself, so the server inherits gc's environment
// and the probe is representative — but a pre-existing server whose global
// environment lacks a reachable user bus (XDG_RUNTIME_DIR,
// DBUS_SESSION_BUS_ADDRESS) can still fail wrapped spawns after a
// successful probe here.
func probeAgentSliceSupport(slice string) error {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return fmt.Errorf("systemd-run not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentSliceProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemd-run",
		"--user", "--scope", "--slice="+slice, "--collect", "--quiet", "--", "true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("systemd user manager probe failed: %w: %s", err, msg)
		}
		return fmt.Errorf("systemd user manager probe failed: %w", err)
	}
	return nil
}

// agentSliceWrapper decides whether pane commands are wrapped in a transient
// systemd user scope. The availability probe runs at most once per Tmux
// instance; on failure it warns once and all subsequent commands run
// unwrapped (graceful fallback).
type agentSliceWrapper struct {
	probe func(slice string) error // test seam; nil means probeAgentSliceSupport
	warn  io.Writer                // test seam; nil means the standard logger
	once  sync.Once
	ok    bool
}

// wrap returns command wrapped for the given slice, or command unchanged
// when slice is empty, command is empty, or transient user scopes are
// unavailable on this host.
func (w *agentSliceWrapper) wrap(slice, command string) string {
	if slice == "" || command == "" {
		return command
	}
	w.once.Do(func() {
		probe := w.probe
		if probe == nil {
			probe = probeAgentSliceSupport
		}
		if err := probe(slice); err != nil {
			msg := fmt.Sprintf("%s=%q set but transient user scopes are unavailable; pane commands run unwrapped: %v",
				AgentSliceEnv, slice, err)
			if w.warn != nil {
				_, _ = fmt.Fprintln(w.warn, "gc: "+msg)
			} else {
				log.Printf("tmux agent slice: %s", msg)
			}
			return
		}
		w.ok = true
	})
	if !w.ok {
		return command
	}
	return shellquote.Join([]string{
		"systemd-run", "--user", "--scope", "--slice=" + slice,
		"--collect", "--quiet", "--", "sh", "-c", command,
	})
}

// wrapPaneCommand applies the GC_AGENT_SLICE systemd user-scope wrapper to a
// pane's initial command. See [AgentSliceEnv]. The environment variable is
// read per call but the availability probe result is cached, so the first
// non-empty slice value decides whether wrapping is active for this Tmux.
func (t *Tmux) wrapPaneCommand(command string) string {
	return t.agentSlice.wrap(os.Getenv(AgentSliceEnv), command)
}

// ensureAgentSlicePlacement verifies that a wrapped pane actually landed in
// GC_AGENT_SLICE. systemd-enabled tmux moves the pane into its own scope after
// fork, racing the systemd-run wrapper. Retry the spawn when tmux wins; if all
// attempts fail, terminate the pane so an unbounded agent never runs outside
// the configured resource slice.
func (t *Tmux) ensureAgentSlicePlacement(target, workDir, command, wrapped string, newSession bool) error {
	slice := os.Getenv(AgentSliceEnv)
	if slice == "" || command == "" || wrapped == command || !t.agentSlice.ok {
		return nil
	}
	verify := t.agentSlicePlacement
	if verify == nil {
		verify = t.verifyAgentSlicePlacement
	}
	var placementErr error
	for attempt := 1; attempt <= agentSlicePlacementAttempts; attempt++ {
		placementErr = verify(target, slice)
		if placementErr == nil {
			return nil
		}
		if attempt == agentSlicePlacementAttempts {
			break
		}
		args := []string{"respawn-pane", "-k", "-t", target}
		if workDir != "" {
			args = append(args, "-c", workDir)
		}
		args = append(args, wrapped)
		if _, err := t.run(args...); err != nil {
			placementErr = fmt.Errorf("retrying pane in agent slice: %w", err)
			break
		}
	}

	abortCommand := "kill-pane"
	if newSession {
		abortCommand = "kill-session"
	}
	if _, err := t.run(abortCommand, "-t", target); err != nil {
		return fmt.Errorf("agent pane placement failed after %d attempts (%w); aborting escaped pane: %w",
			agentSlicePlacementAttempts, placementErr, err)
	}
	return fmt.Errorf("agent pane placement failed after %d attempts: %w",
		agentSlicePlacementAttempts, placementErr)
}

func (t *Tmux) verifyAgentSlicePlacement(target, slice string) error {
	sample := t.agentSlicePlacementSample
	if sample == nil {
		sample = t.sampleAgentSlicePlacement
	}
	wait := t.agentSlicePlacementWait
	if wait == nil {
		wait = time.Sleep
	}

	stableChecks := 0
	var lastErr error
	for check := 1; check <= agentSlicePlacementChecks; check++ {
		if err := sample(target, slice); err != nil {
			stableChecks = 0
			lastErr = err
		} else {
			stableChecks++
			if stableChecks == agentSlicePlacementStableChecks {
				return nil
			}
		}
		if check < agentSlicePlacementChecks {
			wait(agentSlicePlacementInterval)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("placement did not remain stable for the required observation window")
	}
	return fmt.Errorf("pane placement did not stabilize after %d checks: %w",
		agentSlicePlacementChecks, lastErr)
}

func (t *Tmux) sampleAgentSlicePlacement(target, slice string) error {
	pid, err := t.run("display-message", "-p", "-t", target, "#{pane_pid}")
	if err != nil {
		return fmt.Errorf("reading pane pid: %w", err)
	}
	if pid == "" {
		return errors.New("tmux returned an empty pane pid")
	}
	data, err := os.ReadFile("/proc/" + pid + "/cgroup")
	if err != nil {
		return fmt.Errorf("reading pane %s cgroup: %w", pid, err)
	}
	if cgroupContainsSlice(data, slice) {
		return nil
	}
	return fmt.Errorf("pane %s cgroup is outside %s: %s", pid, slice, strings.TrimSpace(string(data)))
}

func cgroupContainsSlice(data []byte, slice string) bool {
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		for _, component := range strings.Split(parts[2], "/") {
			if component == slice {
				return true
			}
		}
	}
	return false
}
