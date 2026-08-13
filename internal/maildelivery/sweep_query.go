package maildelivery

import (
	"fmt"

	"github.com/gastownhall/gascity/internal/beads"
)

// CaptureSweepHighWatermark pins the exact greatest actionable key before any
// bounded page is planned. An empty seat is a read-only idle observation: it
// does not advance the checkpoint revision or generation.
func (s *Store) CaptureSweepHighWatermark(current SweepCheckpoint) (SweepCheckpoint, error) {
	if err := current.validate(); err != nil {
		return SweepCheckpoint{}, err
	}
	if !current.HighWatermark.IsZero() {
		return current, nil
	}
	if s == nil || s.writer == nil {
		return SweepCheckpoint{}, fmt.Errorf("mail delivery conditional writes unavailable")
	}

	high, found, err := s.maxActionableKey(current.SeatRef)
	if err != nil {
		return SweepCheckpoint{}, err
	}
	if !found {
		return current, nil
	}

	next := current
	next.HighWatermark = high
	next.Revision++
	payload, err := encodeSweepCheckpoint(next)
	if err != nil {
		return SweepCheckpoint{}, err
	}
	if err := s.writer.UpdateIfMatch(sweepCheckpointID(current.SeatRef), int64(current.Revision), beads.UpdateOpts{
		Metadata: map[string]string{sweepCheckpointDataKey: payload},
	}); err != nil {
		if beads.IsPreconditionFailed(err) {
			return SweepCheckpoint{}, fmt.Errorf("%w: %w", ErrConflict, err)
		}
		return SweepCheckpoint{}, fmt.Errorf("capturing mail delivery sweep high-watermark: %w", err)
	}
	return s.GetSweepCheckpoint(current.SeatRef)
}

// maxActionableKey reads the exact store-side maximum for one seat. It is a
// separate descending, limit-one query; a caller cannot mistake the end of an
// ascending work page for the sweep boundary.
func (s *Store) maxActionableKey(seatRef string) (DeliveryKey, bool, error) {
	if s == nil || s.beads == nil || !validRef(seatRef) {
		return DeliveryKey{}, false, fmt.Errorf("mail delivery actionable maximum query is invalid")
	}
	rows, err := s.beads.List(beads.ListQuery{
		Type: deliveryBeadType, Label: deliveryActionableLabel, Assignee: seatRef,
		// Keep AllowBackingCreatedLimit false: the store must apply the exact
		// created-at/ID descending tie-break before the limit.
		Sort: beads.SortCreatedDesc, Limit: 1,
	})
	if err != nil {
		return DeliveryKey{}, false, fmt.Errorf("capturing actionable mail delivery maximum: %w", err)
	}
	if len(rows) == 0 {
		return DeliveryKey{}, false, nil
	}
	row := rows[0]
	delivery, err := decodeDelivery(row)
	if err != nil {
		return DeliveryKey{}, false, err
	}
	if delivery.SeatRef != seatRef || delivery.Phase == PhaseDispositioned || row.CreatedAt.IsZero() {
		return DeliveryKey{}, false, fmt.Errorf("actionable mail delivery maximum contradicts canonical resource")
	}
	key := DeliveryKey{CreatedAt: row.CreatedAt.UTC(), DeliveryID: delivery.ID}
	if err := key.validate(); err != nil {
		return DeliveryKey{}, false, err
	}
	return key, true, nil
}
