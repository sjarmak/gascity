package nudgequeue

import (
	"fmt"
	"strings"
	"time"
)

// RecordOperatorDisposition appends an operator adjudication to an existing
// nudge shadow. Delivery terminal state and its failure reason are deliberately
// preserved; disposition is a separate audit layer.
func (s *Store) RecordOperatorDisposition(nudgeID, disposition, reason, actor, retryNudgeID, retryTarget string, now time.Time) (NudgeShadow, error) {
	if s == nil || s.store.Store == nil {
		return NudgeShadow{}, fmt.Errorf("recording nudge disposition: audit store unavailable")
	}
	disposition = strings.TrimSpace(disposition)
	reason = strings.TrimSpace(reason)
	actor = strings.TrimSpace(actor)
	if disposition == "" || reason == "" || actor == "" {
		return NudgeShadow{}, fmt.Errorf("recording nudge disposition: disposition, reason, and actor are required")
	}

	shadow, ok, err := s.FindIncludingTerminal(nudgeID)
	if err != nil {
		return NudgeShadow{}, err
	}
	if !ok {
		return NudgeShadow{}, fmt.Errorf("recording nudge disposition: no shadow for %q", nudgeID)
	}
	if shadow.Open || !IsTerminalState(shadow.State) {
		return NudgeShadow{}, fmt.Errorf("recording nudge %q operator disposition: shadow is not terminal", nudgeID)
	}
	if shadow.OperatorDisposition != "" {
		if shadow.OperatorDisposition == disposition && shadow.OperatorReason == reason &&
			shadow.OperatorActor == actor && shadow.RetryNudgeID == retryNudgeID && shadow.RetryTarget == retryTarget {
			return shadow, nil
		}
		return NudgeShadow{}, fmt.Errorf("nudge %q already dispositioned as %q by %q", nudgeID, shadow.OperatorDisposition, shadow.OperatorActor)
	}

	if err := s.store.SetMetadataBatch(shadow.BeadID, map[string]string{
		"operator_disposition": disposition,
		"operator_reason":      reason,
		"operator_actor":       actor,
		"operator_at":          now.UTC().Format(time.RFC3339),
		"retry_nudge_id":       retryNudgeID,
		"retry_target":         retryTarget,
	}); err != nil {
		return NudgeShadow{}, fmt.Errorf("recording nudge %q operator disposition: %w", nudgeID, err)
	}

	verified, ok, err := s.FindIncludingTerminal(nudgeID)
	if err != nil {
		return NudgeShadow{}, fmt.Errorf("verifying nudge %q operator disposition: %w", nudgeID, err)
	}
	if !ok || verified.Open || !IsTerminalState(verified.State) || verified.OperatorDisposition != disposition || verified.OperatorReason != reason ||
		verified.OperatorActor != actor || verified.RetryNudgeID != retryNudgeID || verified.RetryTarget != retryTarget {
		return NudgeShadow{}, fmt.Errorf("verifying nudge %q operator disposition: authoritative shadow did not match the write", nudgeID)
	}
	return verified, nil
}
