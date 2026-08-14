package maildelivery

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ReconcileOutcome is the closed result vocabulary for one covered delivery.
type ReconcileOutcome string

const (
	// ReconcileWaitingForAuthority means the delivery remains durably queued for session authority.
	ReconcileWaitingForAuthority ReconcileOutcome = "waiting_for_authority"
	// ReconcileCommitted means the destination accepted the stable nudge.
	ReconcileCommitted ReconcileOutcome = "committed"
	// ReconcileUnknownExternalState means the provider effect cannot be proven absent or committed.
	ReconcileUnknownExternalState ReconcileOutcome = "unknown_external_state"
	// ReconcileRetryable means no effect was committed and the same attempt may be retried.
	ReconcileRetryable ReconcileOutcome = "retryable"
)

// ReconcileItem is the body-free result for one delivery in a bounded page.
type ReconcileItem struct {
	DeliveryID string            `json:"delivery_id"`
	Phase      Phase             `json:"phase"`
	Outcome    ReconcileOutcome  `json:"outcome"`
	Attempt    *TransportAttempt `json:"attempt,omitempty"`
}

// ReconcileReport is the canonical domain result of one bounded seat sweep.
type ReconcileReport struct {
	SchemaVersion         string          `json:"schema_version"`
	SeatRef               string          `json:"seat_ref"`
	ObservedAt            time.Time       `json:"observed_at"`
	ExpectedDeliveryID    string          `json:"expected_delivery_id,omitempty"`
	ExpectedDeliveryPhase Phase           `json:"expected_delivery_phase,omitempty"`
	PageCommitted         bool            `json:"page_committed"`
	ActionRequired        bool            `json:"action_required"`
	Checkpoint            SweepCheckpoint `json:"checkpoint"`
	Plan                  SweepPlan       `json:"plan"`
	Deliveries            []ReconcileItem `json:"deliveries"`
}

// AttemptExecutor applies one prepared transport attempt.
type AttemptExecutor func(context.Context, TransportAttempt) (TransportAttempt, error)

// ReconcileSeat processes one bounded page without an exact-canary assertion.
func ReconcileSeat(ctx context.Context, store *Store, seatRef string, limit int, observedAt time.Time, resolver FenceResolver, execute AttemptExecutor) (ReconcileReport, error) {
	return ReconcileExpectedSeat(ctx, store, seatRef, limit, "", observedAt, resolver, execute)
}

// ReconcileExpectedSeat processes one bounded body-free page and optionally
// requires that its sole row be the named exact delivery.
func ReconcileExpectedSeat(ctx context.Context, store *Store, seatRef string, limit int, expectedDeliveryID string, observedAt time.Time, resolver FenceResolver, execute AttemptExecutor) (ReconcileReport, error) {
	report := ReconcileReport{
		SchemaVersion: "mail-delivery-reconcile/v1", SeatRef: seatRef, ObservedAt: observedAt,
		Deliveries: make([]ReconcileItem, 0, limit),
	}
	if store == nil || observedAt.IsZero() || observedAt.Location() != time.UTC {
		return report, fmt.Errorf("mail delivery reconcile inputs are invalid")
	}
	if expectedDeliveryID != "" {
		if ValidateDeliveryID(expectedDeliveryID) != nil || limit != 1 {
			return report, fmt.Errorf("mail delivery exact canary inputs are invalid")
		}
		expected, err := store.Get(expectedDeliveryID)
		if err != nil {
			return report, err
		}
		if expected.SeatRef != seatRef {
			return report, fmt.Errorf("mail delivery canary seat differs from expected delivery")
		}
		if expected.Phase == PhaseDispositioned {
			report.ExpectedDeliveryID = expected.ID
			report.ExpectedDeliveryPhase = expected.Phase
			return report, nil
		}
	}
	checkpoint, plan, err := store.PlanSweepPage(seatRef, limit)
	report.Checkpoint = checkpoint
	report.Plan = plan
	if err != nil {
		return report, err
	}
	if expectedDeliveryID != "" && (ValidateDeliveryID(expectedDeliveryID) != nil || limit != 1 || len(plan.Page) != 1 || plan.Page[0].DeliveryID != expectedDeliveryID) {
		return report, fmt.Errorf("mail delivery canary page differs from expected delivery %q", expectedDeliveryID)
	}
	if checkpoint.HighWatermark.IsZero() {
		return report, nil
	}
	pageIDs := make([]string, 0, len(plan.Page))
	pageSet := make(map[string]struct{}, len(plan.Page))
	for _, key := range plan.Page {
		pageIDs = append(pageIDs, key.DeliveryID)
		pageSet[key.DeliveryID] = struct{}{}
	}
	attempts := make(map[string]TransportAttempt)
	attemptOrder := make([]string, 0, len(pageIDs))
	handled := make(map[string]struct{}, len(pageIDs))
	waiting := make([]string, 0, len(pageIDs))

	for _, deliveryID := range pageIDs {
		delivery, loadErr := store.Get(deliveryID)
		if loadErr != nil {
			return report, loadErr
		}
		if delivery.Phase == PhaseStored || delivery.Phase == PhaseWaitingForActivation {
			continue
		}
		attempt, prepareErr := store.PrepareTransportAttempt(ctx, deliveryID, observedAt, resolver)
		if errors.Is(prepareErr, ErrTransportAuthorityChanged) {
			continue
		}
		if prepareErr != nil {
			if errors.Is(prepareErr, ErrAuthorityUnavailable) {
				coveredIDs := []string{deliveryID}
				if attempt.AttemptID != "" {
					coveredIDs = attempt.CoveredDeliveryIDs
				}
				for _, covered := range coveredIDs {
					if _, inPage := pageSet[covered]; !inPage {
						continue
					}
					current, currentErr := store.Get(covered)
					if currentErr != nil {
						return report, currentErr
					}
					report.Deliveries = append(report.Deliveries, ReconcileItem{DeliveryID: covered, Phase: current.Phase, Outcome: ReconcileWaitingForAuthority})
					handled[covered] = struct{}{}
				}
				continue
			}
			if errors.Is(prepareErr, ErrTransportInvocationInProgress) {
				if _, exists := attempts[attempt.AttemptID]; !exists {
					attemptOrder = append(attemptOrder, attempt.AttemptID)
				}
				attempts[attempt.AttemptID] = attempt
				for _, covered := range attempt.CoveredDeliveryIDs {
					if _, inPage := pageSet[covered]; inPage {
						handled[covered] = struct{}{}
					}
				}
				continue
			}
			return report, prepareErr
		}
		if err := store.LinkCoveredTransportAttempt(attempt); err != nil {
			return report, err
		}
		if _, exists := attempts[attempt.AttemptID]; !exists {
			attemptOrder = append(attemptOrder, attempt.AttemptID)
		}
		attempts[attempt.AttemptID] = attempt
		for _, covered := range attempt.CoveredDeliveryIDs {
			if _, inPage := pageSet[covered]; inPage {
				handled[covered] = struct{}{}
			}
		}
	}
	for _, deliveryID := range pageIDs {
		if _, alreadyHandled := handled[deliveryID]; !alreadyHandled {
			waiting = append(waiting, deliveryID)
		}
	}
	if len(waiting) > 0 {
		attempt, prepareErr := store.PrepareTransportBatch(ctx, waiting, observedAt, resolver)
		if prepareErr != nil {
			if !errors.Is(prepareErr, ErrAuthorityUnavailable) {
				return report, prepareErr
			}
			for _, deliveryID := range waiting {
				delivery, loadErr := store.Get(deliveryID)
				if loadErr != nil {
					return report, loadErr
				}
				if delivery.Phase == PhaseStored {
					delivery, loadErr = store.Advance(delivery.ID, delivery.Revision, PhaseWaitingForActivation)
					if loadErr != nil {
						return report, loadErr
					}
				}
				report.Deliveries = append(report.Deliveries, ReconcileItem{DeliveryID: delivery.ID, Phase: delivery.Phase, Outcome: ReconcileWaitingForAuthority})
			}
		} else {
			attemptOrder = append(attemptOrder, attempt.AttemptID)
			attempts[attempt.AttemptID] = attempt
		}
	}
	items := make(map[string]ReconcileItem, len(pageIDs))
	for _, item := range report.Deliveries {
		items[item.DeliveryID] = item
	}
	for _, attemptID := range attemptOrder {
		attempt := attempts[attemptID]
		result := attempt
		var executeErr error
		var outcome ReconcileOutcome
		switch attempt.State {
		case TransportCommitted:
			outcome = ReconcileCommitted
		case TransportUnknownExternalState:
			outcome = ReconcileUnknownExternalState
		default:
			if execute == nil {
				return report, fmt.Errorf("mail delivery transport executor is unavailable")
			}
			result, executeErr = execute(ctx, attempt)
		}
		var classifyErr error
		if outcome == "" {
			outcome, classifyErr = ClassifyExecution(result, executeErr)
		}
		if classifyErr != nil {
			return report, classifyErr
		}
		report.ActionRequired = report.ActionRequired || outcome == ReconcileUnknownExternalState || errors.Is(executeErr, ErrTransportRetryEscalated)
		for _, deliveryID := range attempt.CoveredDeliveryIDs {
			if _, inPage := pageSet[deliveryID]; !inPage {
				continue
			}
			delivery, loadErr := store.Get(deliveryID)
			if loadErr != nil {
				return report, loadErr
			}
			if outcome == ReconcileCommitted && delivery.Policy == PolicyNotifyOnly && delivery.Phase == PhaseRuntimeNotified {
				if _, dispositionErr := store.RecordDisposition(ctx, DispositionRequest{
					DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
					Reason: DispositionPolicySatisfiedNotified, RecordedAt: result.Receipt.RecordedAt,
				}, resolver); dispositionErr != nil {
					return report, dispositionErr
				}
				delivery, loadErr = store.Get(deliveryID)
				if loadErr != nil {
					return report, loadErr
				}
			}
			resultCopy := result
			items[deliveryID] = ReconcileItem{DeliveryID: deliveryID, Phase: delivery.Phase, Outcome: outcome, Attempt: &resultCopy}
		}
	}
	report.Deliveries = report.Deliveries[:0]
	for _, deliveryID := range pageIDs {
		item, ok := items[deliveryID]
		if !ok {
			return report, fmt.Errorf("mail delivery %q has no durable page result", deliveryID)
		}
		report.Deliveries = append(report.Deliveries, item)
	}
	committed, err := store.CommitSweepPage(checkpoint, plan)
	if err != nil {
		return report, err
	}
	report.Checkpoint = committed
	report.PageCommitted = true
	return report, nil
}

// ClassifyExecution maps a transport attempt/error pair to the closed sweep result.
func ClassifyExecution(result TransportAttempt, err error) (ReconcileOutcome, error) {
	if result.State == TransportUnknownExternalState {
		return ReconcileUnknownExternalState, nil
	}
	if err != nil && (errors.Is(err, ErrTransportRetrySafe) || errors.Is(err, ErrTransportReceiptLookupRetryLater) ||
		errors.Is(err, ErrTransportInvocationInProgress) || errors.Is(err, ErrTransportInvocationRace)) {
		return ReconcileRetryable, nil
	}
	if err != nil {
		return "", err
	}
	if result.State == TransportCommitted {
		return ReconcileCommitted, nil
	}
	return "", fmt.Errorf("mail delivery returned nonterminal transport state %q", result.State)
}
