package maildelivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
)

const (
	deliveryBeadType        = "mail-delivery"
	deliveryDataKey         = "mail.delivery.v1"
	deliveryPhaseKey        = "mail.delivery.phase"
	deliveryActionableLabel = "mail-delivery:actionable"
)

// ErrConflict reports an immutable identity or optimistic-revision conflict.
var ErrConflict = errors.New("mail delivery conflict")

// Store persists typed MailDelivery resources on the canonical Beads substrate.
type Store struct {
	beads  beads.Store
	writer beads.ConditionalWriter
}

// NewStore returns a typed store. Mutations fail closed when revision-CAS is absent.
func NewStore(store beads.Store) *Store {
	writer, _ := beads.ConditionalWriterFor(store)
	return &Store{beads: store, writer: writer}
}

// Create persists one deterministic delivery or returns the byte-equivalent row.
func (s *Store) Create(delivery Delivery) (Delivery, error) {
	if s == nil || s.beads == nil {
		return Delivery{}, fmt.Errorf("mail delivery store is unavailable")
	}
	if err := delivery.Validate(); err != nil {
		return Delivery{}, err
	}
	payload, err := encodeDelivery(delivery)
	if err != nil {
		return Delivery{}, err
	}
	created, err := s.beads.Create(beads.Bead{
		ID: delivery.ID, Title: delivery.ID, Type: deliveryBeadType,
		Assignee: delivery.SeatRef, Labels: []string{deliveryActionableLabel},
		Metadata: beads.StringMap{deliveryDataKey: payload, deliveryPhaseKey: string(delivery.Phase)},
	})
	if err == nil {
		if created.ID != delivery.ID {
			return Delivery{}, fmt.Errorf("mail delivery store changed deterministic ID %q to %q", delivery.ID, created.ID)
		}
		return decodeDelivery(created)
	}
	existing, getErr := s.Get(delivery.ID)
	if getErr != nil {
		return Delivery{}, fmt.Errorf("creating mail delivery %q: %w", delivery.ID, err)
	}
	if !samePersistedDelivery(existing, delivery) {
		return Delivery{}, fmt.Errorf("%w: delivery %q already exists with different bytes", ErrConflict, delivery.ID)
	}
	return existing, nil
}

// ListActionableKeys returns one exact, seat-scoped canonical keyset page.
// The key is the resource row's immutable store creation time plus delivery ID.
func (s *Store) ListActionableKeys(seatRef string, after DeliveryKey, limit int) ([]DeliveryKey, error) {
	if s == nil || s.beads == nil || !validRef(seatRef) || limit <= 0 {
		return nil, fmt.Errorf("mail delivery actionable query is invalid")
	}
	query := beads.ListQuery{
		Type: deliveryBeadType, Label: deliveryActionableLabel, Assignee: seatRef,
		Sort: beads.SortCreatedAsc, Limit: limit,
	}
	if !after.IsZero() {
		if err := after.validate(); err != nil {
			return nil, err
		}
		query.SeekAfter = &beads.SeekBoundary{CreatedAt: after.CreatedAt, ID: after.DeliveryID}
	}
	rows, err := s.beads.List(query)
	if err != nil {
		return nil, fmt.Errorf("listing actionable mail deliveries: %w", err)
	}
	keys := make([]DeliveryKey, 0, len(rows))
	for _, row := range rows {
		delivery, err := decodeDelivery(row)
		if err != nil {
			return nil, err
		}
		if delivery.SeatRef != seatRef || delivery.Phase == PhaseDispositioned || row.CreatedAt.IsZero() {
			return nil, fmt.Errorf("actionable mail delivery index contradicts canonical resource")
		}
		keys = append(keys, DeliveryKey{CreatedAt: row.CreatedAt.UTC(), DeliveryID: delivery.ID})
	}
	return keys, nil
}

// Get strictly decodes and validates one canonical delivery.
func (s *Store) Get(id string) (Delivery, error) {
	if s == nil || s.beads == nil {
		return Delivery{}, fmt.Errorf("mail delivery store is unavailable")
	}
	row, err := s.beads.Get(id)
	if err != nil {
		return Delivery{}, fmt.Errorf("getting mail delivery %q: %w", id, err)
	}
	return decodeDelivery(row)
}

// Advance applies one legal phase edge under the exact Beads revision.
func (s *Store) Advance(id string, expectedRevision uint64, next Phase) (Delivery, error) {
	if s == nil || s.writer == nil {
		return Delivery{}, fmt.Errorf("mail delivery conditional writes unavailable")
	}
	current, err := s.Get(id)
	if err != nil {
		return Delivery{}, err
	}
	if current.Revision != expectedRevision {
		return Delivery{}, fmt.Errorf("%w: delivery %q expected revision %d, current %d", ErrConflict, id, expectedRevision, current.Revision)
	}
	if next == PhaseDispositioned {
		return Delivery{}, fmt.Errorf("mail delivery disposition requires typed authority proof")
	}
	if err := ValidateTransition(current.Phase, next); err != nil {
		return Delivery{}, err
	}
	current.Phase = next
	payload, err := encodeDelivery(current)
	if err != nil {
		return Delivery{}, err
	}
	opts := beads.UpdateOpts{Metadata: map[string]string{deliveryDataKey: payload, deliveryPhaseKey: string(next)}}
	if err := s.writer.UpdateIfMatch(id, int64(expectedRevision), opts); err != nil {
		if beads.IsPreconditionFailed(err) {
			return Delivery{}, fmt.Errorf("%w: %w", ErrConflict, err)
		}
		return Delivery{}, fmt.Errorf("advancing mail delivery %q: %w", id, err)
	}
	return s.Get(id)
}

func encodeDelivery(delivery Delivery) (string, error) {
	delivery.Revision = 0
	payload, err := json.Marshal(delivery)
	if err != nil {
		return "", fmt.Errorf("encoding mail delivery: %w", err)
	}
	return string(payload), nil
}

func decodeDelivery(row beads.Bead) (Delivery, error) {
	if row.Type != deliveryBeadType || row.ID == "" || row.Title != row.ID || row.Description != "" {
		return Delivery{}, fmt.Errorf("bead %q is not a content-free mail delivery", row.ID)
	}
	data := row.Metadata[deliveryDataKey]
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	var delivery Delivery
	if err := decoder.Decode(&delivery); err != nil {
		return Delivery{}, fmt.Errorf("decoding mail delivery %q: %w", row.ID, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Delivery{}, fmt.Errorf("decoding mail delivery %q: trailing JSON", row.ID)
	}
	delivery.Revision = uint64(row.Revision)
	if delivery.ID != row.ID {
		return Delivery{}, fmt.Errorf("mail delivery %q payload identity differs", row.ID)
	}
	if err := delivery.Validate(); err != nil {
		return Delivery{}, fmt.Errorf("validating mail delivery %q: %w", row.ID, err)
	}
	if row.Assignee != delivery.SeatRef || row.Metadata[deliveryPhaseKey] != string(delivery.Phase) {
		return Delivery{}, fmt.Errorf("mail delivery %q index differs from payload", row.ID)
	}
	return delivery, nil
}

func samePersistedDelivery(a, b Delivery) bool {
	if (a.ExpiresAt == nil) != (b.ExpiresAt == nil) {
		return false
	}
	if a.ExpiresAt != nil && !a.ExpiresAt.Equal(*b.ExpiresAt) {
		return false
	}
	a.ExpiresAt = nil
	b.ExpiresAt = nil
	a.Revision = 0
	b.Revision = 0
	return a == b
}
