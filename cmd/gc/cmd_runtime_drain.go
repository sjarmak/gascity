package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/beads/contract"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/spf13/cobra"
)

// drainOps abstracts drain signal operations for testability.
type drainOps interface {
	setDrain(sessionName string) error
	clearDrain(sessionName string) error
	isDraining(sessionName string) (bool, error)
	drainStartTime(sessionName string) (time.Time, error)
	setDrainAck(sessionName string) error
	isDrainAcked(sessionName string) (bool, error)
	setRestartRequested(sessionName string) error
	isRestartRequested(sessionName string) (bool, error)
	clearRestartRequested(sessionName string) error
	setDriftRestart(sessionName string) error
	isDriftRestart(sessionName string) (bool, error)
	clearDriftRestart(sessionName string) error
}

// providerDrainOps implements drainOps using runtime.Provider metadata.
type providerDrainOps struct {
	sp runtime.Provider
}

var errDrainAcknowledgementSuperseded = errors.New("drain acknowledgement superseded")

type runtimeDrainCheckJSON struct {
	SchemaVersion string `json:"schema_version"`
	OK            bool   `json:"ok"`
	Command       string `json:"command"`
	Session       string `json:"session"`
	Target        string `json:"target,omitempty"`
	Draining      bool   `json:"draining"`
}

type runtimeActionJSON struct {
	SchemaVersion string `json:"schema_version"`
	OK            bool   `json:"ok"`
	Command       string `json:"command"`
	Action        string `json:"action"`
	Session       string `json:"session"`
	Target        string `json:"target,omitempty"`
	Status        string `json:"status"`
}

func (o *providerDrainOps) setDrain(sessionName string) error {
	return o.sp.SetMeta(sessionName, "GC_DRAIN", strconv.FormatInt(time.Now().Unix(), 10))
}

func (o *providerDrainOps) clearDrain(sessionName string) error {
	return errors.Join(
		o.sp.RemoveMeta(sessionName, "GC_DRAIN_ACK"),
		o.sp.RemoveMeta(sessionName, reconcilerDrainAckSourceKey),
		o.sp.RemoveMeta(sessionName, reconcilerDrainAckReasonKey),
		o.sp.RemoveMeta(sessionName, reconcilerDrainAckGenerationKey),
		o.sp.RemoveMeta(sessionName, "GC_DRAIN"),
	)
}

func (o *providerDrainOps) isDraining(sessionName string) (bool, error) {
	val, err := o.sp.GetMeta(sessionName, "GC_DRAIN")
	if err != nil {
		return false, fmt.Errorf("reading GC_DRAIN: %w", err)
	}
	return val != "", nil
}

func (o *providerDrainOps) drainStartTime(sessionName string) (time.Time, error) {
	val, err := o.sp.GetMeta(sessionName, "GC_DRAIN")
	if err != nil {
		return time.Time{}, fmt.Errorf("reading GC_DRAIN: %w", err)
	}
	if val == "" {
		return time.Time{}, fmt.Errorf("GC_DRAIN not set")
	}
	unix, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing GC_DRAIN timestamp %q: %w", val, err)
	}
	return time.Unix(unix, 0), nil
}

func (o *providerDrainOps) setDrainAck(sessionName string) error {
	return joinDrainAckMutationErrors(
		o.sp.RemoveMeta(sessionName, reconcilerDrainAckReasonKey),
		o.sp.RemoveMeta(sessionName, reconcilerDrainAckGenerationKey),
		o.sp.SetMeta(sessionName, reconcilerDrainAckSourceKey, drainAckSourceAgentValue),
		o.sp.SetMeta(sessionName, "GC_DRAIN_ACK", "1"),
	)
}

func (o *providerDrainOps) isDrainAcked(sessionName string) (bool, error) {
	val, err := o.sp.GetMeta(sessionName, "GC_DRAIN_ACK")
	if err != nil {
		return false, fmt.Errorf("reading GC_DRAIN_ACK: %w", err)
	}
	return val == "1", nil
}

func (o *providerDrainOps) setRestartRequested(sessionName string) error {
	return o.sp.SetMeta(sessionName, "GC_RESTART_REQUESTED", strconv.FormatInt(time.Now().Unix(), 10))
}

func (o *providerDrainOps) isRestartRequested(sessionName string) (bool, error) {
	val, err := o.sp.GetMeta(sessionName, "GC_RESTART_REQUESTED")
	if err != nil {
		if runtime.IsSessionGone(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading GC_RESTART_REQUESTED: %w", err)
	}
	return val != "", nil
}

func (o *providerDrainOps) clearRestartRequested(sessionName string) error {
	err := o.sp.RemoveMeta(sessionName, "GC_RESTART_REQUESTED")
	if runtime.IsSessionGone(err) {
		return nil
	}
	return err
}

func (o *providerDrainOps) setDriftRestart(sessionName string) error {
	return o.sp.SetMeta(sessionName, "GC_DRIFT_RESTART", "1")
}

func (o *providerDrainOps) isDriftRestart(sessionName string) (bool, error) {
	val, err := o.sp.GetMeta(sessionName, "GC_DRIFT_RESTART")
	if err != nil {
		return false, fmt.Errorf("reading GC_DRIFT_RESTART: %w", err)
	}
	return val == "1", nil
}

func (o *providerDrainOps) clearDriftRestart(sessionName string) error {
	return o.sp.RemoveMeta(sessionName, "GC_DRIFT_RESTART")
}

func joinDrainAckMutationErrors(errs ...error) error {
	var joined []error
	for _, err := range errs {
		if err == nil || drainAckMissingSessionBeadError(err) {
			continue
		}
		joined = append(joined, err)
	}
	return errors.Join(joined...)
}

func drainAckMissingSessionBeadError(err error) bool {
	return runtime.IsSessionGone(err) || errors.Is(err, beads.ErrNotFound)
}

// newDrainOps creates a drainOps from a runtime.Provider.
func newDrainOps(sp runtime.Provider) drainOps {
	return &providerDrainOps{sp: sp}
}

// ---------------------------------------------------------------------------
// gc runtime drain <name>
// ---------------------------------------------------------------------------

func newRuntimeDrainCmd(stdout, stderr io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "drain <name>",
		Short: "Signal a session to drain (wind down gracefully)",
		Long: `Signal a session to drain — wind down its current work gracefully.

Sets a GC_DRAIN metadata flag on the session. The agent should check
for drain status periodically (via "gc runtime drain-check") and finish
its current task before exiting. Pass a session alias or ID. Use
"gc runtime undrain" to cancel.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdRuntimeDrain(args, jsonOutput, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func cmdRuntimeDrain(args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "gc runtime drain: missing session alias or ID") //nolint:errcheck // best-effort stderr
		return 1
	}
	target, err := resolveSessionRuntimeTarget(args[0], stderr)
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime drain: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	sp, err := newSessionProvider()
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime drain: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	dops := newDrainOps(sp)
	rec := openCityRecorder(stderr)
	return doRuntimeDrain(dops, sp, rec, target.display, target.sessionName, jsonOutput, stdout, stderr)
}

// doRuntimeDrain sets the drain signal on a session.
func doRuntimeDrain(dops drainOps, sp runtime.Provider, rec events.Recorder,
	targetName, sn string, jsonOutput bool, stdout, stderr io.Writer,
) int {
	running, err := workerSessionTargetRunningWithConfig("", nil, sp, nil, sn)
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime drain: observing %q: %v\n", targetName, err) //nolint:errcheck // best-effort stderr
		return 1
	}
	if !running {
		fmt.Fprintf(stderr, "gc runtime drain: session %q is not running\n", targetName) //nolint:errcheck // best-effort stderr
		return 1
	}
	if err := dops.setDrain(sn); err != nil {
		fmt.Fprintf(stderr, "gc runtime drain: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	rec.Record(events.Event{
		Type:    events.SessionDraining,
		Actor:   eventActor(),
		Subject: targetName,
	})
	if jsonOutput {
		if err := writeCLIJSONLine(stdout, runtimeActionJSON{
			SchemaVersion: "1",
			OK:            true,
			Command:       "runtime drain",
			Action:        "drain",
			Session:       sn,
			Target:        targetName,
			Status:        "draining",
		}); err != nil {
			fmt.Fprintf(stderr, "gc runtime drain: writing JSON: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "Draining session '%s'\n", targetName) //nolint:errcheck // best-effort stdout
	return 0
}

// ---------------------------------------------------------------------------
// gc runtime undrain <name>
// ---------------------------------------------------------------------------

func newRuntimeUndrainCmd(stdout, stderr io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "undrain <name>",
		Short: "Cancel drain on a session",
		Long: `Cancel a pending drain signal on a session.

Clears the GC_DRAIN and GC_DRAIN_ACK metadata flags, allowing the
session to continue normal operation. Pass a session alias or ID.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdRuntimeUndrain(args, jsonOutput, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func cmdRuntimeUndrain(args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "gc runtime undrain: missing session alias or ID") //nolint:errcheck // best-effort stderr
		return 1
	}
	target, err := resolveSessionRuntimeTarget(args[0], stderr)
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime undrain: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	sp, err := newSessionProvider()
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime undrain: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	dops := newDrainOps(sp)
	rec := openCityRecorder(stderr)
	_, cancelAck, err := runtimeDrainAckPersistence(context.Background(), target, sp)
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime undrain: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	return doRuntimeUndrain(dops, sp, cancelAck, rec, target.display, target.sessionName, jsonOutput, stdout, stderr)
}

// doRuntimeUndrain clears the drain signal on a session.
func doRuntimeUndrain(dops drainOps, sp runtime.Provider, cancelAck func() error, rec events.Recorder,
	targetName, sn string, jsonOutput bool, stdout, stderr io.Writer,
) int {
	running, err := workerSessionTargetRunningWithConfig("", nil, sp, nil, sn)
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime undrain: observing %q: %v\n", targetName, err) //nolint:errcheck // best-effort stderr
		return 1
	}
	if !running {
		fmt.Fprintf(stderr, "gc runtime undrain: session %q is not running\n", targetName) //nolint:errcheck // best-effort stderr
		return 1
	}
	if cancelAck != nil {
		if err := cancelAck(); err != nil {
			if errors.Is(err, errDrainAcknowledgementSuperseded) {
				_ = sp.RemoveMeta(sn, drainAckCancelPendingKey)
			}
			fmt.Fprintf(stderr, "gc runtime undrain: clearing durable provenance: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
	}
	clearErr := dops.clearDrain(sn)
	// Re-check the same durable token fence after clearing provider metadata.
	// A newer acknowledgement that landed between the first check and the clear
	// must keep a runtime signal, so republish it before reporting the lost race.
	if cancelAck != nil {
		if err := cancelAck(); err != nil {
			if errors.Is(err, errDrainAcknowledgementSuperseded) {
				restoreErr := ensureRuntimeDrainAcknowledgement(dops, sn)
				if clearErr == nil && restoreErr == nil {
					restoreErr = sp.RemoveMeta(sn, drainAckCancelPendingKey)
				}
				err = errors.Join(err, clearErr, restoreErr)
			} else {
				err = errors.Join(clearErr, err)
			}
			fmt.Fprintf(stderr, "gc runtime undrain: verifying durable provenance after runtime clear: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
	}
	if clearErr != nil {
		fmt.Fprintf(stderr, "gc runtime undrain: %v\n", clearErr) //nolint:errcheck // best-effort stderr
		return 1
	}
	if cancelAck != nil {
		if err := sp.RemoveMeta(sn, drainAckCancelPendingKey); err != nil {
			fmt.Fprintf(stderr, "gc runtime undrain: clearing cancellation retry marker: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
	}
	rec.Record(events.Event{
		Type:    events.SessionUndrained,
		Actor:   eventActor(),
		Subject: targetName,
	})
	if jsonOutput {
		if err := writeCLIJSONLine(stdout, runtimeActionJSON{
			SchemaVersion: "1",
			OK:            true,
			Command:       "runtime undrain",
			Action:        "undrain",
			Session:       sn,
			Target:        targetName,
			Status:        "undrained",
		}); err != nil {
			fmt.Fprintf(stderr, "gc runtime undrain: writing JSON: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "Undrained session '%s'\n", targetName) //nolint:errcheck // best-effort stdout
	return 0
}

// ---------------------------------------------------------------------------
// gc runtime drain-check
// ---------------------------------------------------------------------------

func newRuntimeDrainCheckCmd(stdout, stderr io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "drain-check [name]",
		Short: "Check if a session is draining (exit 0 = draining)",
		Long: `Check if a session is currently draining.

Returns exit code 0 if draining, 1 if not. Designed for use in
conditionals: "if gc runtime drain-check; then finish-up; fi". Without
arguments, uses the current session context.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdRuntimeDrainCheck(args, jsonOutput, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func cmdRuntimeDrainCheck(args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		target, err := resolveSessionRuntimeTarget(args[0], stderr)
		if err != nil {
			fmt.Fprintf(stderr, "gc runtime drain-check: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1                                                 // silent — same as current "not draining" behavior
		}
		sp, err := newSessionProvider()
		if err != nil {
			fmt.Fprintf(stderr, "gc runtime drain-check: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
		dops := newDrainOps(sp)
		return doRuntimeDrainCheck(dops, target.display, target.sessionName, jsonOutput, stdout, stderr)
	}

	current, err := currentSessionRuntimeTarget()
	if err != nil {
		return 1 // not in agent context → not draining
	}
	sp, err := newSessionProvider()
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime drain-check: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	dops := newDrainOps(sp)
	return doRuntimeDrainCheck(dops, current.display, current.sessionName, jsonOutput, stdout, stderr)
}

// doRuntimeDrainCheck returns 0 if the session is draining, 1 otherwise.
// Silent on stdout — designed for `if gc runtime drain-check; then ...`.
func doRuntimeDrainCheck(dops drainOps, targetName, sn string, jsonOutput bool, stdout, stderr io.Writer) int {
	draining, err := dops.isDraining(sn)
	if err != nil {
		return 1
	}
	if !draining {
		if jsonOutput {
			if err := writeCLIJSONLine(stdout, runtimeDrainCheckJSON{
				SchemaVersion: "1",
				OK:            true,
				Command:       "runtime drain-check",
				Session:       sn,
				Target:        targetName,
				Draining:      false,
			}); err != nil {
				fmt.Fprintf(stderr, "gc runtime drain-check: writing JSON: %v\n", err) //nolint:errcheck // best-effort stderr
				return 1
			}
		}
		return 1
	}
	if jsonOutput {
		if err := writeCLIJSONLine(stdout, runtimeDrainCheckJSON{
			SchemaVersion: "1",
			OK:            true,
			Command:       "runtime drain-check",
			Session:       sn,
			Target:        targetName,
			Draining:      true,
		}); err != nil {
			fmt.Fprintf(stderr, "gc runtime drain-check: writing JSON: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// gc runtime drain-ack
// ---------------------------------------------------------------------------

func newRuntimeDrainAckCmd(stdout, stderr io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "drain-ack [name]",
		Short: "Acknowledge drain — signal the controller to stop this session",
		Long: `Acknowledge a drain signal — tell the controller to stop this session.

Sets GC_DRAIN_ACK metadata on the session, then pokes the controller
socket so the reconciler stops the session immediately rather than on
its next patrol tick. Call this after the session has finished its
current work in response to a drain signal.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdRuntimeDrainAck(args, jsonOutput, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func cmdRuntimeDrainAck(args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	return cmdRuntimeDrainAckContext(context.Background(), args, jsonOutput, stdout, stderr)
}

func cmdRuntimeDrainAckContext(ctx context.Context, args []string, jsonOutput bool, stdout, stderr io.Writer) int {
	var target sessionRuntimeTarget
	var err error
	if len(args) > 0 {
		target, err = resolveSessionRuntimeTarget(args[0], stderr)
		if err != nil {
			fmt.Fprintf(stderr, "gc runtime drain-ack: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
	} else {
		target, err = currentSessionRuntimeTarget()
		if err != nil {
			fmt.Fprintf(stderr, "gc runtime drain-ack: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
	}
	sp, err := newSessionProvider()
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime drain-ack: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	persistAck, rollbackAck, err := runtimeDrainAckPersistence(ctx, target, sp)
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime drain-ack: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	dops := newDrainOps(sp)
	return doRuntimeDrainAck(dops, persistAck, rollbackAck, target.cityPath, target.display, target.sessionName, jsonOutput, stdout, stderr)
}

func runtimeDrainAckPersistence(ctx context.Context, target sessionRuntimeTarget, sp runtime.Provider) (func() error, func() error, error) {
	cfg, cfgErr := loadCityConfigWithoutBuiltinPackRefresh(target.cityPath, io.Discard)
	if cfgErr != nil && !errors.Is(cfgErr, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("loading config: %w", cfgErr)
	}
	store, err := openDrainAckStore(ctx, target.cityPath, cfg)
	if err != nil {
		if runtimeDrainAckTargetIsProviderOnly(target, sp) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("opening store: %w", err)
	}
	sessStore := cliSessionStore(store, cfg, target.cityPath)
	durableIdentity := target.durableIdentity()
	id, err := session.ResolveSessionID(sessStore, durableIdentity)
	if err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return nil, nil, nil
		}
		if runtimeDrainAckTargetIsProviderOnly(target, sp) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("resolving durable session: %w", err)
	}
	handle, err := workerHandleForSessionWithConfig(target.cityPath, sessStore, sp, cfg, id)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving session: %w", err)
	}
	info, err := sessionFrontDoor(sessStore).GetLive(id)
	if err != nil {
		return nil, nil, fmt.Errorf("reading durable drain acknowledgement: %w", err)
	}
	ackToken := info.DrainAckToken
	return func() error {
			token, err := handle.AcknowledgeDrain(ctx)
			if err == nil {
				ackToken = token
			}
			return err
		}, func() error {
			if err := sp.SetMeta(target.sessionName, drainAckCancelPendingKey, ackToken); err != nil {
				return fmt.Errorf("queueing durable cancellation retry: %w", err)
			}
			if err := retryDrainAckCancellation(ctx, func() error {
				return handle.CancelDrainAcknowledgement(ctx, ackToken)
			}); err != nil {
				return err
			}
			latest, err := sessionFrontDoor(sessStore).GetLive(id)
			if err != nil {
				return fmt.Errorf("verifying durable drain acknowledgement cancellation: %w", err)
			}
			if latest.DrainAckToken != "" &&
				latest.DrainAckToken != ackToken &&
				latest.DrainAckCancelToken != latest.DrainAckToken {
				return errDrainAcknowledgementSuperseded
			}
			return nil
		}, nil
}

func openDrainAckStore(ctx context.Context, cityPath string, cfg *config.City) (beads.Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	provider := rawBeadsProvider(cityPath)
	var (
		store beads.Store
		err   error
	)
	switch {
	case provider == "file":
		var fileStore *beads.FileStore
		fileStore, err = openCompatibleFileStoreContext(ctx, cityPath)
		if err == nil {
			fileStore.SetLocker(beads.NewContextFileFlock(ctx, filepath.Join(cityPath, ".gc", "beads.json.lock")))
			store = fileStore
		}
	case strings.HasPrefix(provider, "exec:"):
		store, err = openExecStoreAtForCityContext(ctx, provider, cityPath, cityPath)
	case provider == "doltlite" || contract.ProviderUsesBDContract(provider):
		store, err = scopedBdStoreForCity(ctx, cityPath)
	default:
		err = fmt.Errorf("unsupported beads provider %q", provider)
	}
	if err != nil {
		return nil, err
	}
	return wrapStoreWithBeadPolicies(store, cfg), nil
}

func openCompatibleFileStoreContext(ctx context.Context, cityPath string) (*beads.FileStore, error) {
	type result struct {
		store *beads.FileStore
		err   error
	}
	ready := make(chan result, 1)
	go func() {
		store, err := openCompatibleFileStore(cityPath, cityPath)
		ready <- result{store: store, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case opened := <-ready:
		return opened.store, opened.err
	}
}

func runtimeDrainAckTargetIsProviderOnly(target sessionRuntimeTarget, sp runtime.Provider) bool {
	if strings.TrimSpace(target.sessionID) != "" || sp == nil {
		return false
	}
	id, err := sp.GetMeta(target.sessionName, "GC_SESSION_ID")
	return err == nil && strings.TrimSpace(id) == ""
}

func retryDrainAckCancellation(ctx context.Context, cancel func() error) error {
	if cancel == nil {
		return nil
	}
	retryCtx := ctx
	if _, bounded := retryCtx.Deadline(); !bounded {
		var stop context.CancelFunc
		retryCtx, stop = context.WithTimeout(retryCtx, time.Second)
		defer stop()
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := retryCtx.Err(); err != nil {
			return errors.Join(lastErr, err)
		}
		err := cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-retryCtx.Done():
			return errors.Join(lastErr, retryCtx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	return lastErr
}

// ---------------------------------------------------------------------------
// gc runtime request-restart
// ---------------------------------------------------------------------------

func newRuntimeRequestRestartCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "request-restart",
		Short: "Request controller restart this session (waits to be killed)",
		Long: `Signal the controller to stop and restart this session.

Sets GC_RESTART_REQUESTED metadata on the session, then waits while the
controller stops the session on its next reconcile tick and restarts it
fresh. The wait keeps the agent idle so it does not consume more context
in the interim.

Under normal operation the controller SIGKILLs the process tree before
this command returns. If the controller accepts the stop handoff, the
runtime is already gone, or a SIGINT/SIGTERM is received, the command
exits 0 cleanly. If the controller has not acted within a bounded
timeout (max(5*PatrolInterval, 5min), capped at 30min) the command exits
1 with a diagnostic pointing at controller health.

This command is designed to be called from within a session context.
It emits a session.draining event before waiting.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if cmdRuntimeRequestRestart(stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
}

func cmdRuntimeRequestRestart(stdout, stderr io.Writer) int {
	current, err := currentSessionRuntimeTarget()
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime request-restart: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	sp, err := newSessionProvider()
	if err != nil {
		fmt.Fprintf(stderr, "gc runtime request-restart: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	dops := newDrainOps(sp)
	store, storeErr := openCityStoreAt(current.cityPath)
	if storeErr != nil {
		fmt.Fprintf(stderr, "gc runtime request-restart: opening store: %v\n", storeErr) //nolint:errcheck // best-effort stderr
	}
	// Route the SESSION-class access (restart persist through the worker
	// boundary) to the session coordination-class store so a
	// [beads.classes.sessions] relocation reaches gc runtime request-restart.
	// The routing cfg is loaded refresh-free (the full cfg loads later, for
	// timeout/template resolution). Identity today, so byte-identical.
	var sessStore beads.Store
	if store != nil {
		routeCfg, _ := loadCityConfigWithoutBuiltinPackRefresh(current.cityPath, io.Discard)
		sessStore = cliSessionStore(store, routeCfg, current.cityPath)
	}
	rec := openCityRecorderAt(current.cityPath, stderr)
	cfg, _ := loadCityConfig(current.cityPath, stderr)
	var persistRestart func() error
	if store != nil {
		persistRestart = func() error {
			handle, err := workerHandleForSessionTargetWithConfig(current.cityPath, sessStore, sp, cfg, current.sessionName)
			if err != nil {
				return err
			}
			return handle.Reset(context.Background())
		}
	}
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return doRuntimeRequestRestart(sigCtx, dops, persistRestart, rec, current.display, current.sessionName,
		controllerRestartPollInterval, controllerRestartTimeout(cfg), stdout, stderr)
}

const controllerRestartPollInterval = 1 * time.Second

// controllerRestartTimeout computes the bounded timeout for waiting on the
// controller to act on a restart request: max(5*PatrolInterval, 5min), capped at 30min.
func controllerRestartTimeout(cfg *config.City) time.Duration {
	const floor = 5 * time.Minute
	const ceil = 30 * time.Minute
	patrol := 30 * time.Second
	if cfg != nil {
		patrol = cfg.Daemon.PatrolIntervalDuration()
	}
	d := 5 * patrol
	if d < floor {
		d = floor
	}
	if d > ceil {
		d = ceil
	}
	return d
}

// doRuntimeRequestRestart sets the restart-requested flag then polls until the
// controller accepts the stop handoff (exit 0), the context is canceled by a
// signal (exit 0), or the bounded timeout expires (exit 1 with diagnostic).
func doRuntimeRequestRestart(ctx context.Context, dops drainOps, persistRestart func() error, rec events.Recorder,
	targetName, sn string, pollInterval, timeout time.Duration, stdout, stderr io.Writer,
) int {
	if err := dops.setRestartRequested(sn); err != nil {
		fmt.Fprintf(stderr, "gc runtime request-restart: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	// Also persist the request through the worker boundary so it survives
	// tmux session death. Non-fatal: the runtime flag above is primary.
	if persistRestart != nil {
		if err := persistRestart(); err != nil {
			fmt.Fprintf(stderr, "gc runtime request-restart: setting bead restart flag: %v\n", err) //nolint:errcheck // best-effort stderr
		}
	}
	rec.Record(events.Event{
		Type:    events.SessionDraining,
		Actor:   targetName,
		Subject: targetName,
		Message: "restart requested by session",
	})
	fmt.Fprintf(stdout, "Restart requested. Waiting up to %s for controller to stop this session...\n", timeout) //nolint:errcheck // best-effort stdout

	return waitForControllerRestart(ctx, dops, sn, "gc runtime request-restart", pollInterval, timeout, stderr)
}

func waitForControllerRestart(ctx context.Context, dops drainOps, sn, command string, pollInterval, timeout time.Duration, stderr io.Writer) int {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastPollErr error

	for {
		select {
		case <-ctx.Done():
			// Signal received; leave the flag set so the controller still acts on its next tick.
			fmt.Fprintf(stderr, "%s: signal received; restart request remains set; controller will stop this session on its next reconcile tick\n", command) //nolint:errcheck // best-effort stderr
			return 0
		case <-ticker.C:
			requested, err := dops.isRestartRequested(sn)
			switch {
			case err != nil:
				lastPollErr = err
			case !requested:
				// The controller accepted the stop handoff or the runtime is already gone.
				return 0
			default:
				lastPollErr = nil
			}
			if time.Now().After(deadline) {
				if lastPollErr != nil {
					fmt.Fprintf(stderr, "%s: controller did not act within %s; last poll error: %v; check `gc dashboard` or `gc trace`\n", command, timeout, lastPollErr) //nolint:errcheck // best-effort stderr
				} else {
					fmt.Fprintf(stderr, "%s: controller did not act within %s; check `gc dashboard` or `gc trace`\n", command, timeout) //nolint:errcheck // best-effort stderr
				}
				return 1
			}
		}
	}
}

// drainAckPokeController is a mutable global test seam over pokeController.
// Tests that swap it MUST NOT call t.Parallel().
var drainAckPokeController = pokeController

// doRuntimeDrainAck sets the drain-ack flag on the session, then pokes the
// controller so the reconciler observes the drained state immediately instead
// of waiting for its next patrol tick.

func doRuntimeDrainAck(dops drainOps, persistAck, rollbackAck func() error, cityPath, targetName, sn string, jsonOutput bool, stdout, stderr io.Writer) int {
	if persistAck != nil {
		if err := persistAck(); err != nil {
			fmt.Fprintf(stderr, "gc runtime drain-ack: recording durable provenance: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
	}
	if err := dops.setDrainAck(sn); err != nil {
		// setDrainAck can return a cleanup error after it has successfully
		// published GC_DRAIN_ACK. Preserve durable provenance unless a read
		// authoritatively proves that publication did not happen.
		acked, readErr := dops.isDrainAcked(sn)
		if !acked && readErr == nil && rollbackAck != nil {
			if rollbackErr := rollbackAck(); rollbackErr != nil {
				err = errors.Join(err, fmt.Errorf("rolling back durable provenance: %w", rollbackErr))
			}
		}
		fmt.Fprintf(stderr, "gc runtime drain-ack: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	if err := drainAckPokeController(cityPath); err != nil {
		fmt.Fprintf(stderr, "gc runtime drain-ack: warning: poke failed: %v\n", err) //nolint:errcheck // best-effort stderr
	}
	if jsonOutput {
		if err := writeCLIJSONLine(stdout, runtimeActionJSON{
			SchemaVersion: "1",
			OK:            true,
			Command:       "runtime drain-ack",
			Action:        "drain-ack",
			Session:       sn,
			Target:        targetName,
			Status:        "acknowledged",
		}); err != nil {
			fmt.Fprintf(stderr, "gc runtime drain-ack: writing JSON: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "Drain acknowledged. Controller poked for immediate stop.") //nolint:errcheck // best-effort stdout
	return 0
}
