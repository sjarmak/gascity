package maildelivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// ErrTransportAuthorityChanged reports a safe, pre-invocation re-fence. The
// delivery is already returned to waiting-for-activation and may be grouped
// into a new stable transport attempt.
var ErrTransportAuthorityChanged = errors.New("mail delivery transport authority changed")

// PlanSweepPage loads or creates the canonical seat cursor, captures its
// immutable high-watermark, and returns one bounded actionable page.
func (s *Store) PlanSweepPage(seatRef string, pageSize int) (SweepCheckpoint, SweepPlan, error) {
	if s == nil || !validRef(seatRef) || pageSize <= 0 {
		return SweepCheckpoint{}, SweepPlan{}, fmt.Errorf("mail delivery sweep inputs are invalid")
	}
	checkpoint, err := s.GetSweepCheckpoint(seatRef)
	if errors.Is(err, beads.ErrNotFound) {
		checkpoint, err = s.CreateSweepCheckpoint(seatRef)
	}
	if err != nil {
		return SweepCheckpoint{}, SweepPlan{}, err
	}
	checkpoint, err = s.CaptureSweepHighWatermark(checkpoint)
	if errors.Is(err, ErrConflict) {
		checkpoint, err = s.GetSweepCheckpoint(seatRef)
		if err == nil && checkpoint.HighWatermark.IsZero() {
			checkpoint, err = s.CaptureSweepHighWatermark(checkpoint)
		}
	}
	if err != nil {
		return SweepCheckpoint{}, SweepPlan{}, err
	}
	if checkpoint.HighWatermark.IsZero() {
		return checkpoint, SweepPlan{}, nil
	}
	available, err := s.ListActionableKeys(seatRef, checkpoint.After, pageSize)
	if err != nil {
		return SweepCheckpoint{}, SweepPlan{}, err
	}
	plan, err := PlanSweep(checkpoint, available, pageSize)
	return checkpoint, plan, err
}

// CommitSweepPage advances the exact planned cursor only after every delivery
// in the page has a durable canonical processing result.
func (s *Store) CommitSweepPage(checkpoint SweepCheckpoint, plan SweepPlan) (SweepCheckpoint, error) {
	if checkpoint.HighWatermark.IsZero() {
		return SweepCheckpoint{}, fmt.Errorf("mail delivery sweep has no captured work")
	}
	committed, err := s.AdvanceSweepCheckpoint(checkpoint, plan)
	if !errors.Is(err, ErrConflict) {
		return committed, err
	}
	want, planErr := AdvanceSweep(checkpoint, plan)
	if planErr != nil {
		return SweepCheckpoint{}, err
	}
	current, loadErr := s.GetSweepCheckpoint(checkpoint.SeatRef)
	if loadErr == nil && current.SeatRef == want.SeatRef && current.Generation == want.Generation &&
		current.HighWatermark == want.HighWatermark && current.After == want.After {
		return current, nil
	}
	return SweepCheckpoint{}, err
}

// ProcessSweepPage runs one bounded canonical page and advances its cursor only
// after every item has a durable processing result. A failed or canceled page
// is replayed from the unchanged checkpoint.
func (s *Store) ProcessSweepPage(ctx context.Context, seatRef string, pageSize int, process func(context.Context, string) error) (SweepCheckpoint, SweepPlan, error) {
	if process == nil {
		return SweepCheckpoint{}, SweepPlan{}, fmt.Errorf("mail delivery sweep processor is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return SweepCheckpoint{}, SweepPlan{}, err
	}
	checkpoint, plan, err := s.PlanSweepPage(seatRef, pageSize)
	if err != nil || checkpoint.HighWatermark.IsZero() {
		return checkpoint, plan, err
	}
	for _, key := range plan.Page {
		if err := ctx.Err(); err != nil {
			return checkpoint, plan, err
		}
		if err := process(ctx, key.DeliveryID); err != nil {
			return checkpoint, plan, fmt.Errorf("processing mail delivery %q: %w", key.DeliveryID, err)
		}
	}
	committed, err := s.CommitSweepPage(checkpoint, plan)
	return committed, plan, err
}

// PrepareTransportAttempt moves a stored delivery to the authority wait phase
// and persists its exact stable transport intent. Replays return the linked
// attempt without changing its original timestamp or identity.
func (s *Store) PrepareTransportAttempt(ctx context.Context, deliveryID string, preparedAt time.Time, resolver FenceResolver) (TransportAttempt, error) {
	if err := ctx.Err(); err != nil {
		return TransportAttempt{}, err
	}
	if preparedAt.IsZero() || preparedAt.Location() != time.UTC {
		return TransportAttempt{}, fmt.Errorf("mail delivery transport preparation time must be UTC")
	}
	delivery, err := s.Get(deliveryID)
	if err != nil {
		return TransportAttempt{}, err
	}
	if delivery.Phase == PhaseStored {
		delivery, err = s.Advance(delivery.ID, delivery.Revision, PhaseWaitingForActivation)
		if err != nil {
			return TransportAttempt{}, err
		}
	}
	if delivery.Phase != PhaseWaitingForActivation {
		attempt, err := s.linkedTransportAttempt(delivery.ID)
		if err != nil {
			return TransportAttempt{}, fmt.Errorf("mail delivery %q is %q without a reusable transport intent: %w", delivery.ID, delivery.Phase, err)
		}
		if attempt.State == TransportCommitted || attempt.State == TransportUnknownExternalState {
			return attempt, nil
		}
		fence, err := resolveCurrentFence(ctx, delivery, resolver)
		if err != nil {
			return attempt, err
		}
		if err := attempt.ValidateAuthority(fence); err != nil {
			switch attempt.State {
			case TransportRequested:
				if _, advanceErr := s.Advance(delivery.ID, delivery.Revision, PhaseWaitingForActivation); advanceErr != nil {
					return TransportAttempt{}, advanceErr
				}
				return TransportAttempt{}, fmt.Errorf("%w: %w", ErrTransportAuthorityChanged, err)
			case TransportInvoking:
				return attempt, fmt.Errorf("%w: stale in-flight authority", ErrTransportInvocationInProgress)
			default:
				return TransportAttempt{}, err
			}
		}
		return attempt, nil
	}
	fence, err := resolveCurrentFence(ctx, delivery, resolver)
	if err != nil {
		return TransportAttempt{}, err
	}
	return s.CreateTransportAttempt(ctx, TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: preparedAt,
	}, resolver)
}

// PrepareTransportBatch creates one stable nudge intent covering a complete
// page subset. Creation links the primary first; LinkCoveredTransportAttempt
// repairs any crash before the remaining links are durable.
func (s *Store) PrepareTransportBatch(ctx context.Context, deliveryIDs []string, preparedAt time.Time, resolver FenceResolver) (TransportAttempt, error) {
	if err := ctx.Err(); err != nil {
		return TransportAttempt{}, err
	}
	if len(deliveryIDs) == 0 || preparedAt.IsZero() || preparedAt.Location() != time.UTC {
		return TransportAttempt{}, fmt.Errorf("mail delivery transport batch inputs are invalid")
	}
	seen := make(map[string]struct{}, len(deliveryIDs))
	var primary Delivery
	var firstFence ActivationFence
	for index, deliveryID := range deliveryIDs {
		if _, duplicate := seen[deliveryID]; duplicate {
			return TransportAttempt{}, fmt.Errorf("mail delivery transport batch contains duplicate delivery %q", deliveryID)
		}
		seen[deliveryID] = struct{}{}
		delivery, err := s.Get(deliveryID)
		if err != nil {
			return TransportAttempt{}, err
		}
		if delivery.Phase == PhaseStored {
			delivery, err = s.Advance(delivery.ID, delivery.Revision, PhaseWaitingForActivation)
			if err != nil {
				return TransportAttempt{}, err
			}
		}
		if delivery.Phase != PhaseWaitingForActivation {
			return TransportAttempt{}, fmt.Errorf("mail delivery %q is not waiting for a coalesced attempt", delivery.ID)
		}
		fence, err := resolveCurrentFence(ctx, delivery, resolver)
		if err != nil {
			return TransportAttempt{}, err
		}
		if index == 0 {
			primary, firstFence = delivery, fence
		} else if !sameStableActivationFence(firstFence, fence) {
			return TransportAttempt{}, fmt.Errorf("%w: coalesced deliveries have different authority", ErrConflict)
		}
	}
	attempt, err := s.CreateTransportAttempt(ctx, TransportAttemptRequest{
		DeliveryID: primary.ID, ExpectedDeliveryRevision: primary.Revision,
		ExpectedFenceID: firstFence.FenceID, CoveredDeliveryIDs: deliveryIDs, CreatedAt: preparedAt,
	}, resolver)
	if err != nil {
		return TransportAttempt{}, err
	}
	if err := s.LinkCoveredTransportAttempt(attempt); err != nil {
		return attempt, err
	}
	return attempt, nil
}

func sameStableActivationFence(a, b ActivationFence) bool {
	return a.CityRef == b.CityRef && a.SeatRef == b.SeatRef && a.AuthorityKind == b.AuthorityKind &&
		a.AuthorityRef == b.AuthorityRef && a.AuthorityGeneration == b.AuthorityGeneration &&
		a.SessionRef == b.SessionRef &&
		a.ContinuationEpoch == b.ContinuationEpoch && a.InstanceTokenSHA256 == b.InstanceTokenSHA256
}

func (s *Store) linkedTransportAttempt(deliveryID string) (TransportAttempt, error) {
	row, err := s.beads.Get(deliveryID)
	if err != nil {
		return TransportAttempt{}, err
	}
	_, attemptID, err := parseReceiptLink(row.Metadata[transportAttemptLinkKey])
	if err != nil {
		return TransportAttempt{}, err
	}
	return s.TransportAttempt(attemptID)
}
