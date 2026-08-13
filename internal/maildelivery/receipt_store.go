package maildelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

const (
	readReceiptBeadType     = "mail-delivery-read-receipt"
	readReceiptDataKey      = "mail.delivery.read_receipt.v1"
	responseReceiptBeadType = "mail-delivery-response-receipt"
	responseReceiptDataKey  = "mail.delivery.response_receipt.v1"
	readReceiptLinkKey      = "mail.delivery.read_receipt.link.v1"
	responseReceiptLinkKey  = "mail.delivery.response_receipt.link.v1"
)

// FenceResolver reads current controller authority at the mutation boundary.
type FenceResolver interface {
	ResolveMailActivationFence(context.Context, string) (ActivationFence, error)
}

// ReadReceipt proves an exact message revision was read under current authority.
type ReadReceipt struct {
	Version                  int              `json:"version"`
	ReceiptID                string           `json:"receipt_id"`
	ExpectedDeliveryRevision uint64           `json:"expected_delivery_revision"`
	Authority                ReceiptAuthority `json:"authority"`
}

// ResponseReceiptRequest carries reply identity; authority is always resolved by the store.
type ResponseReceiptRequest struct {
	DeliveryID               string
	ExpectedDeliveryRevision uint64
	ExpectedFenceID          string
	OriginalThreadID         string
	ReplyMessageID           string
	ReplyMessageRevision     uint64
	ReplyToMessageID         string
	RecordedAt               time.Time
}

// ResponseReceipt binds one exact reply to the original delivery and authority.
type ResponseReceipt struct {
	Version                  int              `json:"version"`
	ReceiptID                string           `json:"receipt_id"`
	ExpectedDeliveryRevision uint64           `json:"expected_delivery_revision"`
	Authority                ReceiptAuthority `json:"authority"`
	OriginalThreadID         string           `json:"original_thread_id"`
	ReplyMessageID           string           `json:"reply_message_id"`
	ReplyMessageRevision     uint64           `json:"reply_message_revision"`
	ReplyToMessageID         string           `json:"reply_to_message_id"`
}

// RecordReadReceipt reloads current authority and records exactly one receipt per delivery.
func (s *Store) RecordReadReceipt(ctx context.Context, deliveryID string, expectedRevision uint64, expectedFenceID string, recordedAt time.Time, resolver FenceResolver) (ReadReceipt, error) {
	if existing, found, err := s.readReceiptForExpected(ctx, deliveryID, expectedRevision, expectedFenceID, resolver); err != nil {
		return ReadReceipt{}, err
	} else if found {
		return existing, nil
	}
	delivery, fence, err := s.resolveReceiptAuthority(ctx, deliveryID, expectedRevision, expectedFenceID, resolver)
	if err != nil {
		return ReadReceipt{}, err
	}
	receipt := ReadReceipt{
		Version: 1, ReceiptID: readReceiptID(delivery.ID),
		ExpectedDeliveryRevision: expectedRevision,
		Authority:                ReceiptAuthorityFromFence(fence, delivery, recordedAt),
	}
	if err := receipt.ValidateAgainst(fence, delivery); err != nil {
		return ReadReceipt{}, err
	}
	receipt, err = s.persistReadReceipt(receipt)
	if err != nil {
		return ReadReceipt{}, err
	}
	if err := s.reserveReceiptLink(delivery.ID, expectedRevision, readReceiptLinkKey, receipt.ReceiptID); err != nil {
		if existing, found, loadErr := s.readReceiptForExpected(ctx, deliveryID, expectedRevision, expectedFenceID, resolver); loadErr != nil {
			return ReadReceipt{}, loadErr
		} else if found {
			return existing, nil
		}
		return ReadReceipt{}, err
	}
	return receipt, nil
}

// ValidateAgainst checks a read receipt against freshly resolved authority.
func (r ReadReceipt) ValidateAgainst(f ActivationFence, d Delivery) error {
	wantID := readReceiptID(d.ID)
	if r.Version != 1 || r.ReceiptID != wantID || r.ExpectedDeliveryRevision == 0 {
		return fmt.Errorf("read receipt identity is invalid")
	}
	return r.Authority.ValidateAgainst(f, d)
}

// RecordResponseReceipt reloads authority and records an exact original/reply tuple.
func (s *Store) RecordResponseReceipt(ctx context.Context, request ResponseReceiptRequest, resolver FenceResolver) (ResponseReceipt, error) {
	if existing, found, err := s.responseReceiptForExpected(ctx, request, resolver); err != nil {
		return ResponseReceipt{}, err
	} else if found {
		return existing, nil
	}
	delivery, fence, err := s.resolveReceiptAuthority(ctx, request.DeliveryID, request.ExpectedDeliveryRevision, request.ExpectedFenceID, resolver)
	if err != nil {
		return ResponseReceipt{}, err
	}
	receipt := ResponseReceipt{
		Version: 1, ReceiptID: responseReceiptID(delivery.ID),
		ExpectedDeliveryRevision: request.ExpectedDeliveryRevision,
		Authority:                ReceiptAuthorityFromFence(fence, delivery, request.RecordedAt),
		OriginalThreadID:         request.OriginalThreadID, ReplyMessageID: request.ReplyMessageID,
		ReplyMessageRevision: request.ReplyMessageRevision, ReplyToMessageID: request.ReplyToMessageID,
	}
	if err := receipt.ValidateAgainst(fence, delivery); err != nil {
		return ResponseReceipt{}, err
	}
	receipt, err = s.persistResponseReceipt(receipt)
	if err != nil {
		return ResponseReceipt{}, err
	}
	if err := s.reserveReceiptLink(delivery.ID, request.ExpectedDeliveryRevision, responseReceiptLinkKey, receipt.ReceiptID); err != nil {
		if existing, found, loadErr := s.responseReceiptForExpected(ctx, request, resolver); loadErr != nil {
			return ResponseReceipt{}, loadErr
		} else if found {
			return existing, nil
		}
		return ResponseReceipt{}, err
	}
	return receipt, nil
}

// ValidateAgainst checks reply identity and authority without message content.
func (r ResponseReceipt) ValidateAgainst(f ActivationFence, d Delivery) error {
	wantID := responseReceiptID(d.ID)
	if r.Version != 1 || r.ReceiptID != wantID || r.ExpectedDeliveryRevision == 0 || !validRef(r.OriginalThreadID) ||
		!validRef(r.ReplyMessageID) || r.ReplyMessageRevision == 0 || r.ReplyToMessageID != d.MessageID {
		return fmt.Errorf("response receipt identity is invalid")
	}
	return r.Authority.ValidateAgainst(f, d)
}

func (s *Store) resolveReceiptAuthority(ctx context.Context, deliveryID string, expectedRevision uint64, expectedFenceID string, resolver FenceResolver) (Delivery, ActivationFence, error) {
	if err := ctx.Err(); err != nil {
		return Delivery{}, ActivationFence{}, err
	}
	delivery, err := s.Get(deliveryID)
	if err != nil {
		return Delivery{}, ActivationFence{}, err
	}
	if delivery.Revision != expectedRevision {
		return Delivery{}, ActivationFence{}, fmt.Errorf("%w: delivery %q expected revision %d, current %d", ErrConflict, deliveryID, expectedRevision, delivery.Revision)
	}
	if resolver == nil {
		return Delivery{}, ActivationFence{}, ErrAuthorityUnavailable
	}
	fence, err := resolver.ResolveMailActivationFence(ctx, delivery.SeatRef)
	if err != nil {
		return Delivery{}, ActivationFence{}, fmt.Errorf("%w: %w", ErrAuthorityUnavailable, err)
	}
	if err := fence.Validate(); err != nil {
		return Delivery{}, ActivationFence{}, fmt.Errorf("mail delivery authority invalid: %w", err)
	}
	if fence.FenceID != expectedFenceID || fence.SeatRef != delivery.SeatRef {
		return Delivery{}, ActivationFence{}, fmt.Errorf("%w: current activation fence differs", ErrConflict)
	}
	return delivery, fence, nil
}

// ReadReceipt loads and validates the canonical persisted read receipt and its
// exact delivery-revision link against current controller authority.
func (s *Store) ReadReceipt(ctx context.Context, deliveryID string, resolver FenceResolver) (ReadReceipt, error) {
	delivery, row, fence, err := s.receiptReadContext(ctx, deliveryID, readReceiptLinkKey, resolver)
	if err != nil {
		return ReadReceipt{}, err
	}
	var receipt ReadReceipt
	if err := decodeExactResource(row, readReceiptBeadType, readReceiptDataKey, &receipt); err != nil {
		return ReadReceipt{}, err
	}
	if err := validateReceiptLink(deliveryID, receipt.ExpectedDeliveryRevision, receipt.ReceiptID, delivery, s.beads, readReceiptLinkKey); err != nil {
		return ReadReceipt{}, err
	}
	if err := receipt.ValidateAgainst(fence, delivery); err != nil {
		return ReadReceipt{}, fmt.Errorf("%w: persisted read receipt: %w", ErrConflict, err)
	}
	return receipt, nil
}

// ResponseReceipt loads and validates the canonical persisted response receipt.
func (s *Store) ResponseReceipt(ctx context.Context, deliveryID string, resolver FenceResolver) (ResponseReceipt, error) {
	delivery, row, fence, err := s.receiptReadContext(ctx, deliveryID, responseReceiptLinkKey, resolver)
	if err != nil {
		return ResponseReceipt{}, err
	}
	var receipt ResponseReceipt
	if err := decodeExactResource(row, responseReceiptBeadType, responseReceiptDataKey, &receipt); err != nil {
		return ResponseReceipt{}, err
	}
	if err := validateReceiptLink(deliveryID, receipt.ExpectedDeliveryRevision, receipt.ReceiptID, delivery, s.beads, responseReceiptLinkKey); err != nil {
		return ResponseReceipt{}, err
	}
	if err := receipt.ValidateAgainst(fence, delivery); err != nil {
		return ResponseReceipt{}, fmt.Errorf("%w: persisted response receipt: %w", ErrConflict, err)
	}
	return receipt, nil
}

func (s *Store) receiptReadContext(ctx context.Context, deliveryID, linkKey string, resolver FenceResolver) (Delivery, beads.Bead, ActivationFence, error) {
	if err := ctx.Err(); err != nil {
		return Delivery{}, beads.Bead{}, ActivationFence{}, err
	}
	delivery, err := s.Get(deliveryID)
	if err != nil {
		return Delivery{}, beads.Bead{}, ActivationFence{}, err
	}
	fence, err := resolveCurrentFence(ctx, delivery, resolver)
	if err != nil {
		return Delivery{}, beads.Bead{}, ActivationFence{}, err
	}
	raw, err := s.beads.Get(deliveryID)
	if err != nil {
		return Delivery{}, beads.Bead{}, ActivationFence{}, err
	}
	if raw.Metadata[linkKey] == "" {
		return Delivery{}, beads.Bead{}, ActivationFence{}, fmt.Errorf("linked mail delivery receipt: %w", beads.ErrNotFound)
	}
	_, receiptID, err := parseReceiptLink(raw.Metadata[linkKey])
	if err != nil {
		return Delivery{}, beads.Bead{}, ActivationFence{}, err
	}
	receiptRow, err := s.beads.Get(receiptID)
	if err != nil {
		return Delivery{}, beads.Bead{}, ActivationFence{}, fmt.Errorf("loading linked receipt %q: %w", receiptID, err)
	}
	return delivery, receiptRow, fence, nil
}

func (s *Store) readReceiptForExpected(ctx context.Context, deliveryID string, expectedRevision uint64, expectedFenceID string, resolver FenceResolver) (ReadReceipt, bool, error) {
	receipt, err := s.ReadReceipt(ctx, deliveryID, resolver)
	if errors.Is(err, beads.ErrNotFound) {
		return ReadReceipt{}, false, nil
	}
	if err != nil {
		return ReadReceipt{}, false, err
	}
	if receipt.ExpectedDeliveryRevision != expectedRevision || receipt.Authority.FenceID != expectedFenceID {
		return ReadReceipt{}, false, fmt.Errorf("%w: canonical read receipt differs from request", ErrConflict)
	}
	return receipt, true, nil
}

func (s *Store) responseReceiptForExpected(ctx context.Context, request ResponseReceiptRequest, resolver FenceResolver) (ResponseReceipt, bool, error) {
	receipt, err := s.ResponseReceipt(ctx, request.DeliveryID, resolver)
	if errors.Is(err, beads.ErrNotFound) {
		return ResponseReceipt{}, false, nil
	}
	if err != nil {
		return ResponseReceipt{}, false, err
	}
	if receipt.ExpectedDeliveryRevision != request.ExpectedDeliveryRevision || receipt.Authority.FenceID != request.ExpectedFenceID ||
		receipt.OriginalThreadID != request.OriginalThreadID || receipt.ReplyMessageID != request.ReplyMessageID ||
		receipt.ReplyMessageRevision != request.ReplyMessageRevision || receipt.ReplyToMessageID != request.ReplyToMessageID {
		return ResponseReceipt{}, false, fmt.Errorf("%w: canonical response receipt differs from request", ErrConflict)
	}
	return receipt, true, nil
}

func (s *Store) reserveReceiptLink(deliveryID string, expectedRevision uint64, key, receiptID string) error {
	if s == nil || s.writer == nil {
		return fmt.Errorf("mail delivery conditional writes unavailable")
	}
	link := receiptLink(expectedRevision, receiptID)
	if err := s.writer.UpdateIfMatch(deliveryID, int64(expectedRevision), beads.UpdateOpts{Metadata: map[string]string{key: link}}); err != nil {
		if beads.IsPreconditionFailed(err) {
			return fmt.Errorf("%w: delivery revision moved before receipt commit", ErrConflict)
		}
		return fmt.Errorf("linking mail delivery receipt: %w", err)
	}
	return nil
}

func receiptLink(expectedRevision uint64, receiptID string) string {
	return strconv.FormatUint(expectedRevision, 10) + ":" + receiptID
}

func parseReceiptLink(value string) (uint64, string, error) {
	revisionText, receiptID, ok := strings.Cut(value, ":")
	if !ok || receiptID == "" {
		return 0, "", fmt.Errorf("linked mail delivery receipt is unavailable")
	}
	revision, err := strconv.ParseUint(revisionText, 10, 64)
	if err != nil || revision == 0 {
		return 0, "", fmt.Errorf("linked mail delivery receipt revision is invalid")
	}
	return revision, receiptID, nil
}

func validateReceiptLink(deliveryID string, expectedRevision uint64, receiptID string, _ Delivery, store beads.Store, key string) error {
	raw, err := store.Get(deliveryID)
	if err != nil {
		return err
	}
	revision, linkedID, err := parseReceiptLink(raw.Metadata[key])
	if err != nil {
		return err
	}
	if revision != expectedRevision || linkedID != receiptID {
		return fmt.Errorf("%w: receipt does not match delivery revision link", ErrConflict)
	}
	return nil
}

func resolveCurrentFence(ctx context.Context, delivery Delivery, resolver FenceResolver) (ActivationFence, error) {
	if resolver == nil {
		return ActivationFence{}, ErrAuthorityUnavailable
	}
	fence, err := resolver.ResolveMailActivationFence(ctx, delivery.SeatRef)
	if err != nil {
		return ActivationFence{}, fmt.Errorf("%w: %w", ErrAuthorityUnavailable, err)
	}
	if err := fence.Validate(); err != nil {
		return ActivationFence{}, fmt.Errorf("mail delivery authority invalid: %w", err)
	}
	if fence.SeatRef != delivery.SeatRef {
		return ActivationFence{}, fmt.Errorf("%w: current activation fence differs", ErrConflict)
	}
	return fence, nil
}

func readReceiptID(deliveryID string) string {
	return "mail-read-receipt-" + digest("mail-read-receipt-v1", deliveryID)
}

func responseReceiptID(deliveryID string) string {
	return "mail-response-receipt-" + digest("mail-response-receipt-v1", deliveryID)
}

func (s *Store) persistReadReceipt(proposed ReadReceipt) (ReadReceipt, error) {
	payload, err := json.Marshal(proposed)
	if err != nil {
		return ReadReceipt{}, fmt.Errorf("encoding read receipt: %w", err)
	}
	created, err := s.beads.Create(beads.Bead{ID: proposed.ReceiptID, Title: proposed.ReceiptID, Type: readReceiptBeadType, Metadata: beads.StringMap{readReceiptDataKey: string(payload)}})
	if err == nil {
		if created.ID != proposed.ReceiptID {
			return ReadReceipt{}, fmt.Errorf("mail delivery receipt store changed deterministic ID %q to %q", proposed.ReceiptID, created.ID)
		}
		return proposed, nil
	}
	existingRow, getErr := s.beads.Get(proposed.ReceiptID)
	if getErr != nil {
		return ReadReceipt{}, fmt.Errorf("creating mail delivery receipt %q: %w", proposed.ReceiptID, err)
	}
	var existing ReadReceipt
	if decodeErr := decodeExactResource(existingRow, readReceiptBeadType, readReceiptDataKey, &existing); decodeErr != nil {
		return ReadReceipt{}, fmt.Errorf("%w: canonical read receipt is invalid: %w", ErrConflict, decodeErr)
	}
	if !sameReadReceiptAttempt(existing, proposed) {
		return ReadReceipt{}, fmt.Errorf("%w: receipt %q already exists with different identity", ErrConflict, proposed.ReceiptID)
	}
	return existing, nil
}

func sameReadReceiptAttempt(existing, proposed ReadReceipt) bool {
	existing.Authority.RecordedAt = proposed.Authority.RecordedAt
	return existing == proposed
}

func (s *Store) persistResponseReceipt(proposed ResponseReceipt) (ResponseReceipt, error) {
	payload, err := json.Marshal(proposed)
	if err != nil {
		return ResponseReceipt{}, fmt.Errorf("encoding response receipt: %w", err)
	}
	created, err := s.beads.Create(beads.Bead{ID: proposed.ReceiptID, Title: proposed.ReceiptID, Type: responseReceiptBeadType, Metadata: beads.StringMap{responseReceiptDataKey: string(payload)}})
	if err == nil {
		if created.ID != proposed.ReceiptID {
			return ResponseReceipt{}, fmt.Errorf("mail delivery receipt store changed deterministic ID %q to %q", proposed.ReceiptID, created.ID)
		}
		return proposed, nil
	}
	existingRow, getErr := s.beads.Get(proposed.ReceiptID)
	if getErr != nil {
		return ResponseReceipt{}, fmt.Errorf("creating mail delivery receipt %q: %w", proposed.ReceiptID, err)
	}
	var existing ResponseReceipt
	if decodeErr := decodeExactResource(existingRow, responseReceiptBeadType, responseReceiptDataKey, &existing); decodeErr != nil {
		return ResponseReceipt{}, fmt.Errorf("%w: canonical response receipt is invalid: %w", ErrConflict, decodeErr)
	}
	if !sameResponseReceiptAttempt(existing, proposed) {
		return ResponseReceipt{}, fmt.Errorf("%w: receipt %q already exists with different identity", ErrConflict, proposed.ReceiptID)
	}
	return existing, nil
}

func sameResponseReceiptAttempt(existing, proposed ResponseReceipt) bool {
	existing.Authority.RecordedAt = proposed.Authority.RecordedAt
	return existing == proposed
}

func decodeExactResource(row beads.Bead, beadType, dataKey string, dst any) error {
	if row.Type != beadType || row.Description != "" {
		return fmt.Errorf("bead %q is not a content-free %s", row.ID, beadType)
	}
	decoder := json.NewDecoder(strings.NewReader(row.Metadata[dataKey]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("decoding %s %q: %w", beadType, row.ID, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("decoding %s %q: trailing JSON", beadType, row.ID)
	}
	return nil
}
