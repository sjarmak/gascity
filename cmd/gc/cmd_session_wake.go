package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/spf13/cobra"
)

// exitCodeWakeNoOp is returned by "gc session wake --strict" when the wake is a
// no-op (the session has no wake reasons and remains asleep). It is distinct
// from 1 (real error) so scripts can tell a no-op wake from a failure.
const exitCodeWakeNoOp = 3

// newSessionWakeCmd creates the "gc session wake <id-or-alias>" command.
func newSessionWakeCmd(stdout, stderr io.Writer) *cobra.Command {
	var jsonOutput bool
	var strict bool
	cmd := &cobra.Command{
		Use:   "wake <session-id-or-alias>",
		Short: "Wake a session (request start and clear holds)",
		Long: `Request wake for a session and release user hold or crash-loop quarantine metadata.

After waking, the reconciler will start the session on its next tick
if it has wake reasons (e.g., a matching config agent). If the session
has no wake reasons, it remains asleep and the command reports
"no wake reasons, remaining asleep" (JSON state "no_wake_reasons")
instead of "wake requested", so callers can tell a queued wake from a
no-op.

Pass --strict to exit non-zero when the wake is a no-op, so scripts can
branch on the outcome without parsing output.

Accepts a session ID (e.g., gc-42) or session alias (e.g., mayor).`,
		Example: `  gc session wake gc-42
  gc session wake mayor`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return exitForCode(cmdSessionWake(args, stdout, stderr, jsonOutput, strict))
		},
		ValidArgsFunction: completeSessionIDs,
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "emit JSONL")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero when the wake is a no-op (session has no wake reasons)")
	return cmd
}

type sessionWakeDeps struct {
	store                     beads.Store
	cfg                       *config.City
	cityPath                  string
	cityResolved              bool
	now                       func() time.Time
	withdrawQueuedWaitNudges  func(string, []string) error
	cityUsesManagedReconciler func(string) bool
	pokeController            func(string) error
}

// cmdSessionWake is the CLI entry point for "gc session wake".
//
// The variadic flags are, in order: [0] emit JSONL, [1] --strict (exit
// non-zero on a no-op wake). Extra elements are ignored.
func cmdSessionWake(args []string, stdout, stderr io.Writer, flags ...bool) int {
	asJSON := sessionJSONRequested(flags)
	strict := len(flags) > 1 && flags[1]
	store, code := openCityStore(stderr, "gc session wake")
	if store == nil {
		return code
	}

	cityPath, cityErr := resolveCity()
	var cfg *config.City
	if cityErr == nil {
		cfg, _ = loadCityConfig(cityPath, stderr)
	}
	return doSessionWake(args[0], stdout, stderr, asJSON, strict, sessionWakeDeps{
		store:                     store,
		cfg:                       cfg,
		cityPath:                  cityPath,
		cityResolved:              cityErr == nil,
		now:                       time.Now,
		withdrawQueuedWaitNudges:  withdrawQueuedWaitNudges,
		cityUsesManagedReconciler: cityUsesManagedReconciler,
		pokeController:            pokeController,
	})
}

func doSessionWake(target string, stdout, stderr io.Writer, asJSON, strict bool, deps sessionWakeDeps) int {
	sessStore := cliSessionStore(deps.store, deps.cfg, deps.cityPath)
	id, err := resolveSessionIDMaterializingNamed(deps.cityPath, deps.cfg, sessStore, target)
	if err != nil {
		fmt.Fprintf(stderr, "gc session wake: %v\n", err) //nolint:errcheck
		return 1
	}

	sessFront := sessionFrontDoor(sessStore)
	res, err := sessFront.WakeSession(id, deps.now().UTC(), session.WakeOpts{})
	if err != nil {
		if state, conflict := session.WakeConflictState(err); conflict {
			fmt.Fprintf(stderr, "gc session wake: session %s is %s\n", id, state) //nolint:errcheck
			return 1
		}
		switch {
		case errors.Is(err, session.ErrNotSessionBead):
			fmt.Fprintf(stderr, "gc session wake: %s is not a session\n", id) //nolint:errcheck
		case errors.Is(err, beads.ErrNotFound):
			fmt.Fprintf(stderr, "gc session wake: %v\n", err) //nolint:errcheck
		default:
			fmt.Fprintf(stderr, "gc session wake: updating metadata: %v\n", err) //nolint:errcheck
		}
		return 1
	}
	nudgeIDs := res.NudgeIDs
	hasRunnableTemplate := sessionWakeHasRunnableTemplateInfo(res.Info, deps.cfg)
	// noWakeReasons records the case the help text describes: the session has no
	// wake reason (no matching config agent), so the wake cannot start it. When
	// the session was suspended/drained we roll it back to asleep and clear the
	// wake request — a proven no-op the CLI must report distinctly instead of
	// claiming "wake requested" (#3975).
	noWakeReasons := false
	if !hasRunnableTemplate && sessionWakeRequestedCreateInfo(res.Info) {
		if err := sessFront.ApplyPatch(id, map[string]string{
			"state":                     string(session.StateAsleep),
			"state_reason":              "",
			"pending_create_claim":      "",
			"pending_create_started_at": "",
			"wake_request":              "",
			"wake_requested_at":         "",
		}); err != nil {
			fmt.Fprintf(stderr, "gc session wake: updating metadata: %v\n", err) //nolint:errcheck
			return 1
		}
		noWakeReasons = true
	}
	if deps.cityResolved {
		if err := deps.withdrawQueuedWaitNudges(deps.cityPath, nudgeIDs); err != nil {
			fmt.Fprintf(stderr, "gc session wake: warning: withdrawing queued wait nudges: %v\n", err) //nolint:errcheck
		}
		if deps.cityUsesManagedReconciler(deps.cityPath) {
			if err := deps.pokeController(deps.cityPath); err != nil {
				fmt.Fprintf(stderr, "gc session wake: warning: poke failed: %v\n", err) //nolint:errcheck
			}
		}
	}

	state := "wake_requested"
	if noWakeReasons {
		state = "no_wake_reasons"
	}
	if asJSON {
		if err := writeSessionActionJSON(stdout, sessionActionResult{
			Action:              "wake",
			SessionID:           id,
			State:               state,
			WaitNudgesWithdrawn: len(nudgeIDs),
		}); err != nil {
			fmt.Fprintf(stderr, "gc session wake: %v\n", err) //nolint:errcheck
			return 1
		}
		return wakeExitCode(noWakeReasons, strict)
	}
	if noWakeReasons {
		fmt.Fprintf(stdout, "Session %s: no wake reasons, remaining asleep.\n", id) //nolint:errcheck
		return wakeExitCode(noWakeReasons, strict)
	}
	fmt.Fprintf(stdout, "Session %s: wake requested.\n", id) //nolint:errcheck
	return 0
}

// wakeExitCode returns the process exit code for a wake outcome. A no-op wake
// under --strict exits non-zero so scripts can branch on it; otherwise the
// command succeeds (the mechanics ran; the session simply had nothing to wake).
func wakeExitCode(noWakeReasons, strict bool) int {
	if noWakeReasons && strict {
		return exitCodeWakeNoOp
	}
	return 0
}

func sessionWakeHasRunnableTemplateInfo(info session.Info, cfg *config.City) bool {
	if cfg == nil {
		return true
	}
	template := normalizedSessionTemplateInfo(info, cfg)
	if template == "" {
		template = info.Template
	}
	return findAgentByTemplate(cfg, template) != nil
}

func sessionWakeRequestedCreateInfo(info session.Info) bool {
	state := session.State(strings.TrimSpace(info.MetadataState))
	return state == session.StateSuspended || state == session.StateDrained
}
