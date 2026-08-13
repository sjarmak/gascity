package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/nudgequeue"
	"github.com/spf13/cobra"
)

func newNudgeCancelCmd(stdout, stderr io.Writer) *cobra.Command {
	var reason string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "cancel <nudge-id>",
		Short: "Cancel one pending nudge before delivery",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdNudgeCancel(args, reason, jsonOutput, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "required audit reason for canceling the pending nudge")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func cmdNudgeCancel(args []string, reason string, jsonOutput bool, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "gc nudge cancel: exactly one nudge id is required") //nolint:errcheck
		return 1
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		fmt.Fprintln(stderr, "gc nudge cancel: --reason is required") //nolint:errcheck
		return 1
	}
	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc nudge cancel: %v\n", err) //nolint:errcheck
		return 1
	}
	store := openNudgeBeadStore(cityPath)
	if store.Store == nil {
		fmt.Fprintln(stderr, "gc nudge cancel: audit store unavailable") //nolint:errcheck
		return 1
	}
	defer closeBeadStoreHandle(store.Store) //nolint:errcheck
	result, err := cancelPendingQueuedNudge(cityPath, store, args[0], reason, eventActor(), time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "gc nudge cancel: %v\n", err) //nolint:errcheck
		return 1
	}
	return writeNudgeDispositionResult(result, jsonOutput, stdout, stderr)
}

func cancelPendingQueuedNudge(cityPath string, store beads.NudgesStore, nudgeID, reason, actor string, now time.Time) (nudgeDispositionResult, error) {
	front, err := dispositionFront(store)
	if err != nil {
		return nudgeDispositionResult{}, err
	}
	var result nudgeDispositionResult
	err = withNudgeQueueState(cityPath, func(state *nudgeQueueState) error {
		item, found, findErr := pendingNudgeForCancellation(state, nudgeID)
		if findErr != nil {
			return findErr
		}
		if !found {
			result, findErr = existingNudgeDisposition(front, nudgeID, "canceled", reason, actor, "")
			return findErr
		}
		item, findErr = ensurePendingNudgeCancelledReceipt(front, item, reason, now)
		if findErr != nil {
			return findErr
		}
		shadow, findErr := front.RecordOperatorDisposition(item.ID, "canceled", reason, actor, "", "", now)
		if findErr != nil {
			return findErr
		}
		state.Pending = withoutNudgeID(state.Pending, nudgeID)
		result = dispositionResult("nudge cancel", shadow)
		return nil
	})
	return result, err
}

func pendingNudgeForCancellation(state *nudgeQueueState, nudgeID string) (queuedNudge, bool, error) {
	pending, pendingCount := matchingQueuedNudge(state.Pending, nudgeID)
	_, inFlightCount := matchingQueuedNudge(state.InFlight, nudgeID)
	_, deadCount := matchingQueuedNudge(state.Dead, nudgeID)
	total := pendingCount + inFlightCount + deadCount
	if total > 1 {
		return queuedNudge{}, false, fmt.Errorf("queue contains %d entries for %q; refusing ambiguous cancellation", total, nudgeID)
	}
	if pendingCount == 1 {
		return pending, true, nil
	}
	if inFlightCount == 1 {
		return queuedNudge{}, false, fmt.Errorf("nudge %q is in-flight; cancellation is no longer safe", nudgeID)
	}
	if deadCount == 1 {
		return queuedNudge{}, false, fmt.Errorf("nudge %q is dead-lettered; dismiss or retry it explicitly", nudgeID)
	}
	return queuedNudge{}, false, nil
}

func matchingQueuedNudge(items []queuedNudge, nudgeID string) (queuedNudge, int) {
	var found queuedNudge
	count := 0
	for _, item := range items {
		if item.ID == nudgeID {
			found = item
			count++
		}
	}
	return found, count
}

func ensurePendingNudgeCancelledReceipt(front *nudgequeue.Store, item queuedNudge, reason string, now time.Time) (queuedNudge, error) {
	shadow, ok, err := front.FindIncludingTerminal(item.ID)
	if err != nil {
		return queuedNudge{}, err
	}
	if !ok {
		beadID, _, saveErr := front.Save(item)
		if saveErr != nil {
			return queuedNudge{}, fmt.Errorf("creating cancellation receipt for %q: %w", item.ID, saveErr)
		}
		if beadID == "" {
			return queuedNudge{}, fmt.Errorf("creating cancellation receipt for %q: audit store returned no id", item.ID)
		}
		item.BeadID = beadID
		shadow, ok, err = front.FindIncludingTerminal(item.ID)
	}
	if err != nil {
		return queuedNudge{}, fmt.Errorf("reading cancellation receipt for %q after create: %w", item.ID, err)
	}
	if !ok {
		return queuedNudge{}, fmt.Errorf("reading cancellation receipt for %q: not found after create", item.ID)
	}
	item.BeadID = shadow.BeadID
	if shadow.Open {
		if err := front.Terminalize(item, "canceled", reason, "operator-cancellation", now); err != nil {
			return queuedNudge{}, err
		}
	} else if shadow.State != "canceled" {
		return queuedNudge{}, fmt.Errorf("nudge %q receipt is already terminal as %q", item.ID, shadow.State)
	}
	verified, ok, err := front.FindIncludingTerminal(item.ID)
	if err != nil {
		return queuedNudge{}, fmt.Errorf("verifying canceled receipt for %q: %w", item.ID, err)
	}
	if !ok {
		return queuedNudge{}, fmt.Errorf("verifying canceled receipt for %q: not found after terminalization", item.ID)
	}
	if verified.Open || verified.State != "canceled" {
		return queuedNudge{}, fmt.Errorf("verifying canceled receipt for %q: open=%t state=%q", item.ID, verified.Open, verified.State)
	}
	item.BeadID = verified.BeadID
	return item, nil
}
