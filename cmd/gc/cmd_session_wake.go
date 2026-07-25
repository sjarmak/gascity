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
has no wake reasons, it remains asleep.

The command reports which outcome occurred: "wake requested" when the
wake was queued, or "no wake reasons, remaining asleep" when it was a
no-op. Pass --strict to exit non-zero on the no-op case so scripts can
branch on it.

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
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero when the wake is a no-op (no wake reasons)")
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
func cmdSessionWake(args []string, stdout, stderr io.Writer, asJSON, strict bool) int {
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
	// remainedAsleep is the silent no-op the wake help text warns about: the
	// session has no wake reason (no matching config agent) but requested a
	// fresh create, so the reconciler will never start it and the metadata is
	// reset back to asleep. Report it distinctly instead of claiming success.
	remainedAsleep := !hasRunnableTemplate && sessionWakeRequestedCreateInfo(res.Info)
	if remainedAsleep {
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

	if asJSON {
		state := "wake_requested"
		if remainedAsleep {
			state = "no_wake_reasons"
		}
		if err := writeSessionActionJSON(stdout, sessionActionResult{
			Action:              "wake",
			SessionID:           id,
			State:               state,
			WaitNudgesWithdrawn: len(nudgeIDs),
		}); err != nil {
			fmt.Fprintf(stderr, "gc session wake: %v\n", err) //nolint:errcheck
			return 1
		}
		if remainedAsleep && strict {
			return exitWakeNoOp
		}
		return 0
	}
	if remainedAsleep {
		fmt.Fprintf(stdout, "Session %s: no wake reasons, remaining asleep.\n", id) //nolint:errcheck
		if strict {
			return exitWakeNoOp
		}
		return 0
	}
	fmt.Fprintf(stdout, "Session %s: wake requested.\n", id) //nolint:errcheck
	return 0
}

// exitWakeNoOp is the exit code "gc session wake --strict" returns when the
// wake was a no-op (the session had no wake reasons and remains asleep). It is
// distinct from the generic error code (1) so a --strict caller can tell a
// dropped wake apart from a hard failure.
const exitWakeNoOp = 2

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
