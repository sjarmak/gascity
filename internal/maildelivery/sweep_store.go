package maildelivery

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
)

const (
	sweepCheckpointBeadType = "mail-delivery-sweep"
	sweepCheckpointDataKey  = "mail.delivery.sweep.v1"
)

func sweepCheckpointID(seatRef string) string {
	return "mail-sweep-" + digest("mail-sweep-v1", seatRef)
}

// CreateSweepCheckpoint creates or returns the exact initial per-seat cursor.
func (s *Store) CreateSweepCheckpoint(seatRef string) (SweepCheckpoint, error) {
	if s == nil || s.beads == nil {
		return SweepCheckpoint{}, fmt.Errorf("mail delivery store is unavailable")
	}
	checkpoint := SweepCheckpoint{Version: 1, SeatRef: seatRef, Generation: 1, Revision: 1}
	if err := checkpoint.validate(); err != nil {
		return SweepCheckpoint{}, err
	}
	payload, err := encodeSweepCheckpoint(checkpoint)
	if err != nil {
		return SweepCheckpoint{}, err
	}
	id := sweepCheckpointID(seatRef)
	created, err := s.beads.Create(beads.Bead{ID: id, Title: id, Type: sweepCheckpointBeadType, Metadata: beads.StringMap{sweepCheckpointDataKey: payload}})
	if err == nil {
		return decodeSweepCheckpoint(created, seatRef)
	}
	existing, getErr := s.GetSweepCheckpoint(seatRef)
	if getErr != nil {
		return SweepCheckpoint{}, fmt.Errorf("creating mail delivery sweep checkpoint: %w", err)
	}
	if !sameSweepCheckpoint(existing, checkpoint) {
		return SweepCheckpoint{}, fmt.Errorf("%w: sweep checkpoint already exists with different bytes", ErrConflict)
	}
	return existing, nil
}

// GetSweepCheckpoint strictly reads one seat's durable cursor.
func (s *Store) GetSweepCheckpoint(seatRef string) (SweepCheckpoint, error) {
	if s == nil || s.beads == nil {
		return SweepCheckpoint{}, fmt.Errorf("mail delivery store is unavailable")
	}
	row, err := s.beads.Get(sweepCheckpointID(seatRef))
	if err != nil {
		return SweepCheckpoint{}, fmt.Errorf("getting mail delivery sweep checkpoint: %w", err)
	}
	return decodeSweepCheckpoint(row, seatRef)
}

// AdvanceSweepCheckpoint persists one planned page under the exact revision.
func (s *Store) AdvanceSweepCheckpoint(current SweepCheckpoint, plan SweepPlan) (SweepCheckpoint, error) {
	if s == nil || s.writer == nil {
		return SweepCheckpoint{}, s.conditionalWriterError()
	}
	next, err := AdvanceSweep(current, plan)
	if err != nil {
		return SweepCheckpoint{}, err
	}
	payload, err := encodeSweepCheckpoint(next)
	if err != nil {
		return SweepCheckpoint{}, err
	}
	id := sweepCheckpointID(current.SeatRef)
	if err := s.writer.UpdateIfMatch(id, int64(current.Revision), beads.UpdateOpts{Metadata: map[string]string{sweepCheckpointDataKey: payload}}); err != nil {
		if beads.IsPreconditionFailed(err) {
			return SweepCheckpoint{}, fmt.Errorf("%w: %w", ErrConflict, err)
		}
		return SweepCheckpoint{}, fmt.Errorf("advancing mail delivery sweep checkpoint: %w", err)
	}
	return s.GetSweepCheckpoint(current.SeatRef)
}

func encodeSweepCheckpoint(checkpoint SweepCheckpoint) (string, error) {
	checkpoint.Revision = 0
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return "", fmt.Errorf("encoding mail delivery sweep checkpoint: %w", err)
	}
	return string(payload), nil
}

func decodeSweepCheckpoint(row beads.Bead, seatRef string) (SweepCheckpoint, error) {
	if row.Type != sweepCheckpointBeadType || row.Description != "" || row.ID != sweepCheckpointID(seatRef) {
		return SweepCheckpoint{}, fmt.Errorf("bead %q is not a content-free mail delivery sweep checkpoint", row.ID)
	}
	decoder := json.NewDecoder(strings.NewReader(row.Metadata[sweepCheckpointDataKey]))
	decoder.DisallowUnknownFields()
	var checkpoint SweepCheckpoint
	if err := decoder.Decode(&checkpoint); err != nil {
		return SweepCheckpoint{}, fmt.Errorf("decoding mail delivery sweep checkpoint: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return SweepCheckpoint{}, fmt.Errorf("decoding mail delivery sweep checkpoint: trailing JSON")
	}
	checkpoint.Revision = uint64(row.Revision)
	if checkpoint.SeatRef != seatRef {
		return SweepCheckpoint{}, fmt.Errorf("mail delivery sweep seat identity differs")
	}
	if err := checkpoint.validate(); err != nil {
		return SweepCheckpoint{}, err
	}
	return checkpoint, nil
}

func sameSweepCheckpoint(a, b SweepCheckpoint) bool {
	a.Revision = 0
	b.Revision = 0
	return a == b
}
