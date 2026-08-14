package maildelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

const (
	dispositionBeadType = "mail-delivery-disposition"
	dispositionDataKey  = "mail.delivery.disposition.v1"
	dispositionLinkKey  = "mail.delivery.disposition.link.v1"
)

var errNotDispositioned = errors.New("mail delivery is not dispositioned")

// DispositionRequest names only the closed reason and optimistic delivery
// revision. It deliberately has no caller-supplied proof booleans: the store
// derives proof from canonical, revision-linked receipt resources.
type DispositionRequest struct {
	DeliveryID               string
	ExpectedDeliveryRevision uint64
	Reason                   DispositionReason
	RecordedAt               time.Time
}

// Disposition is the content-free terminal record linked to the delivery row.
type Disposition struct {
	Version                  int               `json:"version"`
	ID                       string            `json:"id"`
	DeliveryID               string            `json:"delivery_id"`
	ExpectedDeliveryRevision uint64            `json:"expected_delivery_revision"`
	Reason                   DispositionReason `json:"reason"`
	ProofReceiptID           string            `json:"proof_receipt_id"`
	RecordedAt               time.Time         `json:"recorded_at"`
}

// RecordDisposition derives policy proof from persisted receipts, then commits
// the terminal phase and disposition link under one delivery revision CAS.
func (s *Store) RecordDisposition(ctx context.Context, request DispositionRequest, resolver FenceResolver) (Disposition, error) {
	if existing, found, err := s.dispositionForExpected(ctx, request, resolver); err != nil {
		return Disposition{}, err
	} else if found {
		return existing, nil
	}
	if err := ctx.Err(); err != nil {
		return Disposition{}, err
	}
	delivery, err := s.Get(request.DeliveryID)
	if err != nil {
		return Disposition{}, err
	}
	if delivery.Revision != request.ExpectedDeliveryRevision {
		return Disposition{}, fmt.Errorf("%w: delivery %q expected revision %d, current %d", ErrConflict, delivery.ID, request.ExpectedDeliveryRevision, delivery.Revision)
	}
	if request.RecordedAt.IsZero() || request.RecordedAt.Location() != time.UTC {
		return Disposition{}, fmt.Errorf("mail delivery disposition time must be UTC")
	}
	proofReceiptID, err := s.deriveDispositionProof(ctx, delivery, request.Reason, resolver)
	if err != nil {
		return Disposition{}, err
	}
	if err := ValidateTransition(delivery.Phase, PhaseDispositioned); err != nil {
		return Disposition{}, err
	}
	disposition := Disposition{
		Version: 1, ID: dispositionID(delivery.ID), DeliveryID: delivery.ID,
		ExpectedDeliveryRevision: request.ExpectedDeliveryRevision,
		Reason:                   request.Reason, ProofReceiptID: proofReceiptID, RecordedAt: request.RecordedAt,
	}
	disposition, err = s.persistDisposition(disposition)
	if err != nil {
		return Disposition{}, err
	}
	delivery.Phase = PhaseDispositioned
	deliveryPayload, err := encodeDelivery(delivery)
	if err != nil {
		return Disposition{}, err
	}
	if s == nil || s.writer == nil {
		return Disposition{}, fmt.Errorf("mail delivery conditional writes unavailable")
	}
	err = s.writer.UpdateIfMatch(delivery.ID, int64(request.ExpectedDeliveryRevision), beads.UpdateOpts{
		Metadata: map[string]string{
			deliveryDataKey: deliveryPayload, deliveryPhaseKey: string(PhaseDispositioned),
			dispositionLinkKey: receiptLink(request.ExpectedDeliveryRevision, disposition.ID),
		},
		RemoveLabels: []string{deliveryActionableLabel},
	})
	if err != nil {
		if beads.IsPreconditionFailed(err) {
			if existing, found, loadErr := s.dispositionForExpected(ctx, request, resolver); loadErr == nil && found {
				return existing, nil
			}
			return Disposition{}, fmt.Errorf("%w: delivery revision moved before disposition commit", ErrConflict)
		}
		return Disposition{}, fmt.Errorf("committing mail delivery disposition: %w", err)
	}
	return disposition, nil
}

// Disposition reads and validates a persisted terminal record, including its
// exact delivery-revision link and canonical receipt proof.
func (s *Store) Disposition(ctx context.Context, deliveryID string, resolver FenceResolver) (Disposition, error) {
	if err := ctx.Err(); err != nil {
		return Disposition{}, err
	}
	delivery, err := s.Get(deliveryID)
	if err != nil {
		return Disposition{}, err
	}
	if delivery.Phase != PhaseDispositioned {
		return Disposition{}, fmt.Errorf("%w: %q", errNotDispositioned, deliveryID)
	}
	raw, err := s.beads.Get(deliveryID)
	if err != nil {
		return Disposition{}, err
	}
	if raw.Metadata[dispositionLinkKey] == "" {
		return Disposition{}, fmt.Errorf("linked mail delivery disposition: %w", beads.ErrNotFound)
	}
	expectedRevision, id, err := parseReceiptLink(raw.Metadata[dispositionLinkKey])
	if err != nil {
		return Disposition{}, err
	}
	row, err := s.beads.Get(id)
	if err != nil {
		return Disposition{}, fmt.Errorf("loading linked disposition %q: %w", id, err)
	}
	var disposition Disposition
	if err := decodeExactResource(row, dispositionBeadType, dispositionDataKey, &disposition); err != nil {
		return Disposition{}, err
	}
	if disposition.Version != 1 || disposition.ID != id || disposition.ID != dispositionID(deliveryID) ||
		disposition.DeliveryID != deliveryID || disposition.ExpectedDeliveryRevision != expectedRevision ||
		disposition.RecordedAt.IsZero() || disposition.RecordedAt.Location() != time.UTC {
		return Disposition{}, fmt.Errorf("%w: persisted disposition identity is invalid", ErrConflict)
	}
	proofID, err := s.deriveDispositionProof(ctx, delivery, disposition.Reason, resolver)
	if err != nil {
		return Disposition{}, err
	}
	if proofID != disposition.ProofReceiptID {
		return Disposition{}, fmt.Errorf("%w: disposition proof differs from canonical receipt", ErrConflict)
	}
	return disposition, nil
}

func (s *Store) deriveDispositionProof(ctx context.Context, delivery Delivery, reason DispositionReason, resolver FenceResolver) (string, error) {
	switch reason {
	case DispositionPolicySatisfiedNotified:
		if delivery.Policy != PolicyNotifyOnly {
			return "", fmt.Errorf("mail delivery disposition %q does not match policy", reason)
		}
		attempt, err := s.committedTransportAttemptForDelivery(delivery.ID)
		if err != nil {
			return "", fmt.Errorf("deriving disposition transport proof: %w", err)
		}
		if attempt.Receipt.CommitBoundary != TransportCommitBoundaryDestinationAtomic {
			return "", fmt.Errorf("%w: notify disposition requires destination-atomic transport proof", ErrAuthorityUnavailable)
		}
		return attempt.AttemptID, nil
	case DispositionPolicySatisfiedRead:
		if delivery.Policy != PolicyReadRequired {
			return "", fmt.Errorf("mail delivery disposition %q does not match policy", reason)
		}
		receipt, err := s.ReadReceipt(ctx, delivery.ID, resolver)
		if err != nil {
			return "", fmt.Errorf("deriving disposition read proof: %w", err)
		}
		return receipt.ReceiptID, nil
	case DispositionPolicySatisfiedResponse:
		if delivery.Policy != PolicyResponseRequired {
			return "", fmt.Errorf("mail delivery disposition %q does not match policy", reason)
		}
		receipt, err := s.ResponseReceipt(ctx, delivery.ID, resolver)
		if err != nil {
			return "", fmt.Errorf("deriving disposition response proof: %w", err)
		}
		return receipt.ReceiptID, nil
	case DispositionRecipientDeclined, DispositionSenderCanceled, DispositionSuperseded, DispositionExpired,
		DispositionRecipientSeatRetired:
		return "", fmt.Errorf("%w: canonical proof reader for disposition %q", ErrAuthorityUnavailable, reason)
	default:
		return "", fmt.Errorf("mail delivery disposition reason %q is invalid", reason)
	}
}

func (s *Store) dispositionForExpected(ctx context.Context, request DispositionRequest, resolver FenceResolver) (Disposition, bool, error) {
	disposition, err := s.Disposition(ctx, request.DeliveryID, resolver)
	if errors.Is(err, beads.ErrNotFound) || errors.Is(err, errNotDispositioned) {
		return Disposition{}, false, nil
	}
	if err != nil {
		return Disposition{}, false, err
	}
	if disposition.ExpectedDeliveryRevision != request.ExpectedDeliveryRevision || disposition.Reason != request.Reason {
		return Disposition{}, false, fmt.Errorf("%w: canonical disposition differs from request", ErrConflict)
	}
	return disposition, true, nil
}

func (s *Store) persistDisposition(proposed Disposition) (Disposition, error) {
	payload, err := json.Marshal(proposed)
	if err != nil {
		return Disposition{}, fmt.Errorf("encoding mail delivery disposition: %w", err)
	}
	created, err := s.beads.Create(beads.Bead{ID: proposed.ID, Title: proposed.ID, Type: dispositionBeadType, Metadata: beads.StringMap{dispositionDataKey: string(payload)}})
	if err == nil {
		if created.ID != proposed.ID {
			return Disposition{}, fmt.Errorf("mail delivery disposition store changed deterministic ID %q to %q", proposed.ID, created.ID)
		}
		return proposed, nil
	}
	existingRow, getErr := s.beads.Get(proposed.ID)
	if getErr != nil {
		return Disposition{}, fmt.Errorf("creating mail delivery disposition %q: %w", proposed.ID, err)
	}
	var existing Disposition
	if decodeErr := decodeExactResource(existingRow, dispositionBeadType, dispositionDataKey, &existing); decodeErr != nil {
		return Disposition{}, fmt.Errorf("%w: canonical disposition is invalid: %w", ErrConflict, decodeErr)
	}
	canonical := existing
	existing.RecordedAt = proposed.RecordedAt
	if existing != proposed {
		return Disposition{}, fmt.Errorf("%w: disposition %q already exists with different identity", ErrConflict, proposed.ID)
	}
	return canonical, nil
}

func dispositionID(deliveryID string) string {
	return "mail-disposition-" + digest("mail-disposition-v1", deliveryID)
}
