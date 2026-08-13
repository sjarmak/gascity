package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/nudgequeue"
	"github.com/spf13/cobra"
)

type nudgeDispositionResult struct {
	SchemaVersion  string `json:"schema_version"`
	Command        string `json:"command"`
	NudgeID        string `json:"nudge_id"`
	Disposition    string `json:"disposition"`
	Reason         string `json:"reason"`
	Actor          string `json:"actor"`
	OperatorAt     string `json:"operator_at"`
	TerminalBeadID string `json:"terminal_bead_id"`
	RetryNudgeID   string `json:"retry_nudge_id,omitempty"`
	RetryTarget    string `json:"retry_target,omitempty"`
}

func newNudgeDismissCmd(stdout, stderr io.Writer) *cobra.Command {
	var reason string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "dismiss <nudge-id>",
		Short: "Dismiss one adjudicated dead-letter nudge",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdNudgeDismiss(args, reason, jsonOutput, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "required audit reason for dismissing the dead letter")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func newNudgeRetryCmd(stdout, stderr io.Writer) *cobra.Command {
	var target string
	var reason string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "retry <nudge-id>",
		Short: "Retry one dead-letter nudge against an explicit current session",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdNudgeRetry(args, target, reason, jsonOutput, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "to", "", "required current session alias or id")
	cmd.Flags().StringVar(&reason, "reason", "", "required audit reason for retrying the dead letter")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func cmdNudgeDismiss(args []string, reason string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "gc nudge dismiss: exactly one nudge id is required") //nolint:errcheck
		return 1
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		fmt.Fprintln(stderr, "gc nudge dismiss: --reason is required") //nolint:errcheck
		return 1
	}
	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc nudge dismiss: %v\n", err) //nolint:errcheck
		return 1
	}
	store := openNudgeBeadStore(cityPath)
	if store.Store == nil {
		fmt.Fprintln(stderr, "gc nudge dismiss: audit store unavailable") //nolint:errcheck
		return 1
	}
	defer closeBeadStoreHandle(store.Store) //nolint:errcheck
	result, err := dismissDeadQueuedNudge(cityPath, store, args[0], reason, eventActor(), time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "gc nudge dismiss: %v\n", err) //nolint:errcheck
		return 1
	}
	return writeNudgeDispositionResult(result, jsonOutput, stdout, stderr)
}

func cmdNudgeRetry(args []string, targetID, reason string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "gc nudge retry: exactly one nudge id is required") //nolint:errcheck
		return 1
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		fmt.Fprintln(stderr, "gc nudge retry: --to is required") //nolint:errcheck
		return 1
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		fmt.Fprintln(stderr, "gc nudge retry: --reason is required") //nolint:errcheck
		return 1
	}
	target, err := resolveNudgeTarget(targetID, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "gc nudge retry: %v\n", err) //nolint:errcheck
		return 1
	}
	store := openNudgeBeadStore(target.cityPath)
	if store.Store == nil {
		fmt.Fprintln(stderr, "gc nudge retry: audit store unavailable") //nolint:errcheck
		return 1
	}
	defer closeBeadStoreHandle(store.Store) //nolint:errcheck
	result, err := retryDeadQueuedNudge(target.cityPath, store, args[0], target, reason, eventActor(), time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "gc nudge retry: %v\n", err) //nolint:errcheck
		return 1
	}
	return writeNudgeDispositionResult(result, jsonOutput, stdout, stderr)
}

func writeNudgeDispositionResult(result nudgeDispositionResult, jsonOutput bool, stdout, stderr io.Writer) int {
	if jsonOutput {
		if err := writeCLIJSONLine(stdout, result); err != nil {
			fmt.Fprintf(stderr, "gc %s: writing JSON: %v\n", result.Command, err) //nolint:errcheck
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "%s %s: %s (receipt %s)", result.NudgeID, result.Disposition, result.Reason, result.TerminalBeadID) //nolint:errcheck
	if result.RetryNudgeID != "" {
		fmt.Fprintf(stdout, "; retry=%s target=%s", result.RetryNudgeID, result.RetryTarget) //nolint:errcheck
	}
	fmt.Fprintln(stdout) //nolint:errcheck
	return 0
}

func dismissDeadQueuedNudge(cityPath string, store beads.NudgesStore, nudgeID, reason, actor string, now time.Time) (nudgeDispositionResult, error) {
	front, err := dispositionFront(store)
	if err != nil {
		return nudgeDispositionResult{}, err
	}
	var result nudgeDispositionResult
	err = withNudgeQueueState(cityPath, func(state *nudgeQueueState) error {
		item, found, err := exactlyOneDeadNudge(state.Dead, nudgeID)
		if err != nil {
			return err
		}
		if !found {
			result, err = existingNudgeDisposition(front, nudgeID, "dismissed", reason, actor, "")
			return err
		}
		item, err = ensureDeadNudgeTerminalReceipt(front, item, now)
		if err != nil {
			return err
		}
		shadow, err := front.RecordOperatorDisposition(item.ID, "dismissed", reason, actor, "", "", now)
		if err != nil {
			return err
		}
		state.Dead = withoutNudgeID(state.Dead, nudgeID)
		result = dispositionResult("nudge dismiss", shadow)
		return nil
	})
	return result, err
}

func retryDeadQueuedNudge(cityPath string, store beads.NudgesStore, nudgeID string, target nudgeTarget, reason, actor string, now time.Time) (nudgeDispositionResult, error) {
	front, err := dispositionFront(store)
	if err != nil {
		return nudgeDispositionResult{}, err
	}
	var result nudgeDispositionResult
	var createdRetryBead string
	err = withNudgeQueueState(cityPath, func(state *nudgeQueueState) error {
		var applyErr error
		result, createdRetryBead, applyErr = applyRetryDeadNudge(state, front, nudgeID, target, reason, actor, now)
		return applyErr
	})
	if err != nil && createdRetryBead != "" {
		if rollbackErr := front.RollbackEnqueue(createdRetryBead); rollbackErr != nil {
			return nudgeDispositionResult{}, fmt.Errorf("%w; rollback retry receipt %q: %w", err, createdRetryBead, rollbackErr)
		}
	}
	if err == nil {
		pingNudgeWakeSocket(cityPath)
	}
	return result, err
}

func applyRetryDeadNudge(state *nudgeQueueState, front *nudgequeue.Store, nudgeID string, target nudgeTarget, reason, actor string, now time.Time) (nudgeDispositionResult, string, error) {
	item, found, err := exactlyOneDeadNudge(state.Dead, nudgeID)
	if err != nil {
		return nudgeDispositionResult{}, "", err
	}
	if !found {
		result, existingErr := existingNudgeDisposition(front, nudgeID, "retried", reason, actor, target.sessionID)
		return result, "", existingErr
	}
	item, err = ensureDeadNudgeTerminalReceipt(front, item, now)
	if err != nil {
		return nudgeDispositionResult{}, "", err
	}

	retryID := retryNudgeID(nudgeID)
	createdRetryBead, err := ensureRetryQueuedNudge(state, front, item, target, retryID, now)
	if err != nil {
		return nudgeDispositionResult{}, createdRetryBead, err
	}
	shadow, err := front.RecordOperatorDisposition(item.ID, "retried", reason, actor, retryID, target.sessionID, now)
	if err != nil {
		return nudgeDispositionResult{}, createdRetryBead, err
	}
	state.Dead = withoutNudgeID(state.Dead, nudgeID)
	sortQueuedNudges(state)
	return dispositionResult("nudge retry", shadow), createdRetryBead, nil
}

func ensureRetryQueuedNudge(state *nudgeQueueState, front *nudgequeue.Store, original queuedNudge, target nudgeTarget, retryID string, now time.Time) (string, error) {
	retry := newQueuedNudgeWithOptions(target.agentKey(), original.Message, original.Source, now, queuedNudgeOptions{
		ID:                retryID,
		SessionID:         target.sessionID,
		ContinuationEpoch: target.continuationEpoch,
		Reference:         original.Reference,
	})
	present, terminal, err := retryState(state, retryID)
	if err != nil || terminal {
		if terminal {
			err = fmt.Errorf("retry %q is already dead; retry that nudge id explicitly", retryID)
		}
		return "", err
	}
	if present {
		return "", nil
	}
	if conflict := liveReferenceConflict(state, retry); conflict != "" {
		return "", fmt.Errorf("refusing duplicate retry: live nudge %q already carries the same reference", conflict)
	}

	beadID, created, err := ensureOpenRetryReceipt(front, retry)
	if err != nil {
		return "", err
	}
	retry.BeadID = beadID
	state.Pending = append(append([]queuedNudge(nil), state.Pending...), retry)
	if created {
		return beadID, nil
	}
	return "", nil
}

func ensureOpenRetryReceipt(front *nudgequeue.Store, retry queuedNudge) (string, bool, error) {
	shadow, ok, err := front.FindIncludingTerminal(retry.ID)
	if err != nil {
		return "", false, err
	}
	if ok {
		if !shadow.Open {
			return "", false, fmt.Errorf("retry shadow %q is already closed; retry the newer dead letter instead", retry.ID)
		}
		return shadow.BeadID, false, nil
	}
	beadID, created, err := front.Save(retry)
	if err != nil {
		return "", false, err
	}
	if beadID == "" {
		return "", false, fmt.Errorf("creating retry %q: audit store returned no receipt", retry.ID)
	}
	return beadID, created, nil
}

func dispositionFront(store beads.NudgesStore) (*nudgequeue.Store, error) {
	if store.Store == nil {
		return nil, fmt.Errorf("nudge audit store unavailable")
	}
	return nudgeFrontDoor(store), nil
}

func ensureDeadNudgeTerminalReceipt(front *nudgequeue.Store, item queuedNudge, now time.Time) (queuedNudge, error) {
	shadow, ok, err := front.FindIncludingTerminal(item.ID)
	if err != nil {
		return queuedNudge{}, err
	}
	if !ok {
		beadID, _, saveErr := front.Save(item)
		if saveErr != nil {
			return queuedNudge{}, saveErr
		}
		if beadID == "" {
			return queuedNudge{}, fmt.Errorf("creating dead-letter receipt for %q: audit store returned no id", item.ID)
		}
		item.BeadID = beadID
		shadow, ok, err = front.FindIncludingTerminal(item.ID)
		if err != nil || !ok {
			return queuedNudge{}, fmt.Errorf("reading dead-letter receipt for %q after create: found=%t err=%w", item.ID, ok, err)
		}
	}
	item.BeadID = shadow.BeadID
	if shadow.Open || !nudgequeue.IsTerminalState(shadow.State) {
		reason := strings.TrimSpace(item.LastError)
		if reason == "" {
			reason = "dead-letter"
		}
		if err := front.Terminalize(item, terminalStateForDeadQueuedNudge(item), reason, "operator-disposition", now); err != nil {
			return queuedNudge{}, err
		}
	}
	verified, ok, err := front.FindIncludingTerminal(item.ID)
	if err != nil || !ok || verified.Open || !nudgequeue.IsTerminalState(verified.State) {
		return queuedNudge{}, fmt.Errorf("verifying terminal receipt for %q: found=%t open=%t state=%q err=%w", item.ID, ok, verified.Open, verified.State, err)
	}
	item.BeadID = verified.BeadID
	return item, nil
}

func existingNudgeDisposition(front *nudgequeue.Store, nudgeID, want, reason, actor, retryTarget string) (nudgeDispositionResult, error) {
	shadow, ok, err := front.FindIncludingTerminal(nudgeID)
	if err != nil {
		return nudgeDispositionResult{}, err
	}
	if !ok || shadow.OperatorDisposition == "" {
		return nudgeDispositionResult{}, fmt.Errorf("dead-letter nudge %q not found", nudgeID)
	}
	if shadow.Open || !nudgequeue.IsTerminalState(shadow.State) {
		return nudgeDispositionResult{}, fmt.Errorf("nudge %q disposition receipt is not terminal", nudgeID)
	}
	if shadow.OperatorDisposition != want {
		return nudgeDispositionResult{}, fmt.Errorf("nudge %q already dispositioned as %q", nudgeID, shadow.OperatorDisposition)
	}
	if shadow.OperatorReason != reason || shadow.OperatorActor != actor || shadow.RetryTarget != retryTarget {
		return nudgeDispositionResult{}, fmt.Errorf("nudge %q already dispositioned as %q with different audit inputs", nudgeID, want)
	}
	command := "nudge dismiss"
	switch want {
	case "canceled":
		command = "nudge cancel"
	case "retried":
		command = "nudge retry"
	}
	return dispositionResult(command, shadow), nil
}

func dispositionResult(command string, shadow nudgequeue.NudgeShadow) nudgeDispositionResult {
	return nudgeDispositionResult{
		SchemaVersion:  "1",
		Command:        command,
		NudgeID:        shadow.ID,
		Disposition:    shadow.OperatorDisposition,
		Reason:         shadow.OperatorReason,
		Actor:          shadow.OperatorActor,
		OperatorAt:     shadow.OperatorAt.UTC().Format(time.RFC3339),
		TerminalBeadID: shadow.BeadID,
		RetryNudgeID:   shadow.RetryNudgeID,
		RetryTarget:    shadow.RetryTarget,
	}
}

func exactlyOneDeadNudge(items []queuedNudge, nudgeID string) (queuedNudge, bool, error) {
	var found queuedNudge
	count := 0
	for _, item := range items {
		if item.ID == nudgeID {
			found = item
			count++
		}
	}
	if count > 1 {
		return queuedNudge{}, false, fmt.Errorf("dead-letter queue contains %d entries for %q; refusing ambiguous disposition", count, nudgeID)
	}
	return found, count == 1, nil
}

func withoutNudgeID(items []queuedNudge, nudgeID string) []queuedNudge {
	filtered := make([]queuedNudge, 0, len(items))
	for _, item := range items {
		if item.ID != nudgeID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func retryNudgeID(nudgeID string) string {
	sum := sha256.Sum256([]byte("nudge-retry\x00" + nudgeID))
	return "nudge-retry-" + hex.EncodeToString(sum[:6])
}

func retryState(state *nudgeQueueState, retryID string) (present, terminal bool, err error) {
	for _, item := range append(append([]queuedNudge(nil), state.Pending...), state.InFlight...) {
		if item.ID != retryID {
			continue
		}
		if present {
			return false, false, fmt.Errorf("queue contains duplicate retry id %q", retryID)
		}
		present = true
	}
	for _, item := range state.Dead {
		if item.ID != retryID {
			continue
		}
		if present || terminal {
			return false, false, fmt.Errorf("queue contains duplicate retry id %q", retryID)
		}
		terminal = true
	}
	return present, terminal, nil
}

func liveReferenceConflict(state *nudgeQueueState, retry queuedNudge) string {
	if retry.Reference == nil || retry.Reference.ID == "" {
		return ""
	}
	for _, item := range append(append([]queuedNudge(nil), state.Pending...), state.InFlight...) {
		if item.ID == retry.ID || item.Reference == nil {
			continue
		}
		if item.Source == retry.Source && item.Reference.Kind == retry.Reference.Kind && item.Reference.ID == retry.Reference.ID {
			return item.ID
		}
	}
	return ""
}
