package maildelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

const (
	transportAttemptBeadType = "mail-delivery-transport-attempt"
	transportAttemptDataKey  = "mail.delivery.transport_attempt.v1"
	transportAttemptLinkKey  = "mail.delivery.transport_attempt.link.v1"
	// TransportInvocationLeaseDuration bounds how long another caller must
	// treat an invoking attempt as live before crash recovery may mark it unknown.
	TransportInvocationLeaseDuration = 2 * time.Minute
)

// ErrTransportInvocationInProgress reports that an unexpired invocation lease
// is owned by another caller and canonical state was left unchanged.
var ErrTransportInvocationInProgress = errors.New("mail delivery transport invocation is in progress")

// ErrTransportInvocationRace identifies a benign competing claimant. It also
// wraps ErrConflict for callers that need the broader compatibility class.
var ErrTransportInvocationRace = errors.New("mail delivery transport invocation race")

// ErrTransportRetrySafe permits only an identical stable-effect retry.
var ErrTransportRetrySafe = errors.New("mail delivery transport invocation is safe to retry with the same effect ID")

// TransportState is the closed state of one stable external-effect attempt.
type TransportState string

const (
	// TransportRequested means the intent is durable but no outcome is proven.
	TransportRequested TransportState = "requested"
	// TransportInvoking means one caller won the right to invoke the provider.
	// A caller inside the lease backs off; after expiry, recovery records unknown
	// rather than invoking the provider again.
	TransportInvoking TransportState = "invoking"
	// TransportCommitted means destination evidence proves the effect committed.
	TransportCommitted TransportState = "committed"
	// TransportUnknownExternalState means invocation may have committed but no
	// destination receipt can prove either outcome; it must not be redelivered.
	TransportUnknownExternalState TransportState = "unknown_external_state"
)

// TransportAttemptRequest names one stable, content-free notification intent.
type TransportAttemptRequest struct {
	DeliveryID string
	// ExpectedDeliveryRevision is the revision at attempt creation. Replays
	// retain it; callers must not replace it with the delivery's linked revision.
	ExpectedDeliveryRevision uint64
	ExpectedFenceID          string
	CoveredDeliveryIDs       []string
	CreatedAt                time.Time
}

// TransportAttempt is the canonical lifecycle and receipt for one stable nudge.
type TransportAttempt struct {
	Version                  int              `json:"version"`
	AttemptID                string           `json:"attempt_id"`
	NudgeID                  string           `json:"nudge_id"`
	DeliveryID               string           `json:"delivery_id"`
	ExpectedDeliveryRevision uint64           `json:"expected_delivery_revision"`
	CoveredDeliveryIDs       []string         `json:"covered_delivery_ids"`
	AuthorityKind            AuthorityKind    `json:"authority_kind"`
	AuthorityRef             string           `json:"authority_ref"`
	AuthorityGeneration      uint64           `json:"authority_generation"`
	AuthorityIntentSHA256    string           `json:"authority_intent_sha256"`
	SessionRef               string           `json:"session_ref"`
	ContinuationEpoch        uint64           `json:"continuation_epoch"`
	InstanceTokenSHA256      string           `json:"instance_token_sha256"`
	State                    TransportState   `json:"state"`
	InvocationStartedAt      time.Time        `json:"invocation_started_at,omitempty"`
	InvocationLeaseUntil     time.Time        `json:"invocation_lease_until,omitempty"`
	InvocationCount          uint64           `json:"invocation_count"`
	Receipt                  TransportReceipt `json:"receipt"`
	CreatedAt                time.Time        `json:"created_at"`
	Revision                 uint64           `json:"-"`
}

// ValidateAuthority checks a persisted attempt against freshly controller-
// resolved authority immediately before provider invocation.
func (a TransportAttempt) ValidateAuthority(fence ActivationFence) error {
	if err := fence.Validate(); err != nil {
		return err
	}
	want := attemptIDFromAuthority(a.DeliveryID, fence.AuthorityKind, fence.AuthorityRef,
		fence.AuthorityGeneration, fence.AuthorityIntentSHA256, fence.SessionRef,
		fence.ContinuationEpoch, fence.InstanceTokenSHA256)
	if a.AttemptID != want || a.AuthorityKind != fence.AuthorityKind || a.AuthorityRef != fence.AuthorityRef ||
		a.AuthorityGeneration != fence.AuthorityGeneration || a.AuthorityIntentSHA256 != fence.AuthorityIntentSHA256 ||
		a.SessionRef != fence.SessionRef || a.ContinuationEpoch != fence.ContinuationEpoch ||
		a.InstanceTokenSHA256 != fence.InstanceTokenSHA256 {
		return fmt.Errorf("%w: transport attempt authority is stale", ErrConflict)
	}
	return nil
}

// CreateTransportAttempt persists the stable intent before provider invocation.
func (s *Store) CreateTransportAttempt(ctx context.Context, request TransportAttemptRequest, resolver FenceResolver) (TransportAttempt, error) {
	if err := ctx.Err(); err != nil {
		return TransportAttempt{}, err
	}
	delivery, err := s.Get(request.DeliveryID)
	if err != nil {
		return TransportAttempt{}, err
	}
	fence, err := resolveCurrentFence(ctx, delivery, resolver)
	if err != nil {
		return TransportAttempt{}, err
	}
	if fence.FenceID != request.ExpectedFenceID {
		return TransportAttempt{}, fmt.Errorf("%w: current activation fence differs", ErrConflict)
	}
	if request.CreatedAt.IsZero() || request.CreatedAt.Location() != time.UTC {
		return TransportAttempt{}, fmt.Errorf("mail delivery transport attempt time must be UTC")
	}
	attemptID, err := AttemptID(delivery.ID, fence)
	if err != nil {
		return TransportAttempt{}, err
	}
	nudgeID, err := NudgeID(attemptID, request.CoveredDeliveryIDs)
	if err != nil {
		return TransportAttempt{}, err
	}
	covered := append([]string(nil), request.CoveredDeliveryIDs...)
	slices.Sort(covered)
	attempt := TransportAttempt{
		Version: 1, AttemptID: attemptID, NudgeID: nudgeID, DeliveryID: delivery.ID,
		ExpectedDeliveryRevision: request.ExpectedDeliveryRevision, CoveredDeliveryIDs: covered,
		AuthorityKind: fence.AuthorityKind, AuthorityRef: fence.AuthorityRef,
		AuthorityGeneration: fence.AuthorityGeneration, AuthorityIntentSHA256: fence.AuthorityIntentSHA256,
		SessionRef: fence.SessionRef, ContinuationEpoch: fence.ContinuationEpoch,
		InstanceTokenSHA256: fence.InstanceTokenSHA256, State: TransportRequested,
		CreatedAt: request.CreatedAt, Revision: 1,
	}
	if err := attempt.validate(); err != nil {
		return TransportAttempt{}, err
	}
	if existing, found, err := s.transportAttemptForExpected(delivery.ID, request.ExpectedDeliveryRevision, attempt); err != nil {
		return TransportAttempt{}, err
	} else if found {
		return existing, nil
	}
	if delivery.Revision != request.ExpectedDeliveryRevision {
		return TransportAttempt{}, fmt.Errorf("%w: delivery %q expected revision %d, current %d", ErrConflict, delivery.ID, request.ExpectedDeliveryRevision, delivery.Revision)
	}
	if delivery.Phase != PhaseWaitingForActivation {
		return TransportAttempt{}, fmt.Errorf("mail delivery transport attempt requires phase %q, current %q", PhaseWaitingForActivation, delivery.Phase)
	}
	payload, err := encodeTransportAttempt(attempt)
	if err != nil {
		return TransportAttempt{}, err
	}
	created, createErr := s.beads.Create(beads.Bead{
		ID: attempt.AttemptID, Title: attempt.AttemptID, Type: transportAttemptBeadType,
		Metadata: beads.StringMap{transportAttemptDataKey: payload},
	})
	if createErr == nil {
		if created.ID != attempt.AttemptID {
			return TransportAttempt{}, fmt.Errorf("mail delivery attempt store changed deterministic ID %q to %q", attempt.AttemptID, created.ID)
		}
	} else {
		existing, getErr := s.TransportAttempt(attempt.AttemptID)
		if getErr != nil {
			return TransportAttempt{}, fmt.Errorf("creating mail delivery transport attempt %q: %w", attempt.AttemptID, createErr)
		}
		if !sameTransportIntent(existing, attempt) {
			return TransportAttempt{}, fmt.Errorf("%w: transport attempt %q already exists with different identity", ErrConflict, attempt.AttemptID)
		}
		attempt = existing
	}
	delivery.Phase = PhaseNotificationRequested
	deliveryPayload, err := encodeDelivery(delivery)
	if err != nil {
		return TransportAttempt{}, err
	}
	if s.writer == nil {
		return TransportAttempt{}, fmt.Errorf("mail delivery conditional writes unavailable")
	}
	err = s.writer.UpdateIfMatch(delivery.ID, int64(request.ExpectedDeliveryRevision), beads.UpdateOpts{Metadata: map[string]string{
		deliveryDataKey: deliveryPayload, deliveryPhaseKey: string(PhaseNotificationRequested),
		transportAttemptLinkKey: receiptLink(request.ExpectedDeliveryRevision, attempt.AttemptID),
	}})
	if err != nil {
		if beads.IsPreconditionFailed(err) {
			if existing, found, loadErr := s.transportAttemptForExpected(delivery.ID, request.ExpectedDeliveryRevision, attempt); loadErr == nil && found {
				return existing, nil
			}
			return TransportAttempt{}, fmt.Errorf("%w: delivery revision moved before transport attempt link", ErrConflict)
		}
		return TransportAttempt{}, fmt.Errorf("linking mail delivery transport attempt: %w", err)
	}
	return s.TransportAttempt(attempt.AttemptID)
}

func (s *Store) transportAttemptForExpected(deliveryID string, expectedRevision uint64, proposed TransportAttempt) (TransportAttempt, bool, error) {
	raw, err := s.beads.Get(deliveryID)
	if err != nil {
		return TransportAttempt{}, false, err
	}
	link := raw.Metadata[transportAttemptLinkKey]
	if link == "" {
		return TransportAttempt{}, false, nil
	}
	revision, attemptID, err := parseReceiptLink(link)
	if err != nil {
		return TransportAttempt{}, false, err
	}
	if revision != expectedRevision {
		delivery, decodeErr := decodeDelivery(raw)
		if decodeErr == nil && delivery.Phase == PhaseWaitingForActivation && delivery.Revision == expectedRevision {
			previous, loadErr := s.TransportAttempt(attemptID)
			if loadErr == nil && previous.State == TransportRequested {
				return TransportAttempt{}, false, nil
			}
		}
		return TransportAttempt{}, false, fmt.Errorf("%w: canonical transport attempt link differs from request", ErrConflict)
	}
	if attemptID != proposed.AttemptID {
		return TransportAttempt{}, false, fmt.Errorf("%w: canonical transport attempt link differs from request", ErrConflict)
	}
	existing, err := s.TransportAttempt(attemptID)
	if err != nil {
		return TransportAttempt{}, false, err
	}
	if !sameTransportIntent(existing, proposed) {
		return TransportAttempt{}, false, fmt.Errorf("%w: canonical transport attempt differs from request", ErrConflict)
	}
	return existing, true, nil
}

func (s *Store) committedTransportAttemptForDelivery(deliveryID string) (TransportAttempt, error) {
	raw, err := s.beads.Get(deliveryID)
	if err != nil {
		return TransportAttempt{}, err
	}
	_, attemptID, err := parseReceiptLink(raw.Metadata[transportAttemptLinkKey])
	if err != nil {
		return TransportAttempt{}, err
	}
	attempt, err := s.TransportAttempt(attemptID)
	if err != nil {
		return TransportAttempt{}, err
	}
	if !slices.Contains(attempt.CoveredDeliveryIDs, deliveryID) || attempt.State != TransportCommitted || attempt.Receipt.State != EffectCommitted {
		return TransportAttempt{}, fmt.Errorf("%w: delivery has no committed canonical transport receipt", ErrConflict)
	}
	return attempt, nil
}

// TransportAttempt strictly loads one canonical content-free attempt.
func (s *Store) TransportAttempt(attemptID string) (TransportAttempt, error) {
	if s == nil || s.beads == nil {
		return TransportAttempt{}, fmt.Errorf("mail delivery store is unavailable")
	}
	row, err := s.beads.Get(attemptID)
	if err != nil {
		return TransportAttempt{}, err
	}
	if row.Type != transportAttemptBeadType || row.ID != attemptID || row.Title != attemptID || row.Description != "" {
		return TransportAttempt{}, fmt.Errorf("bead %q is not a content-free mail delivery transport attempt", row.ID)
	}
	decoder := json.NewDecoder(strings.NewReader(row.Metadata[transportAttemptDataKey]))
	decoder.DisallowUnknownFields()
	var attempt TransportAttempt
	if err := decoder.Decode(&attempt); err != nil {
		return TransportAttempt{}, fmt.Errorf("decoding mail delivery transport attempt %q: %w", row.ID, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return TransportAttempt{}, fmt.Errorf("decoding mail delivery transport attempt %q: trailing JSON", row.ID)
	}
	attempt.Revision = uint64(row.Revision)
	if attempt.AttemptID != row.ID {
		return TransportAttempt{}, fmt.Errorf("mail delivery transport attempt %q payload identity differs", row.ID)
	}
	if err := attempt.validate(); err != nil {
		return TransportAttempt{}, err
	}
	return attempt, nil
}

// BeginTransportInvocation grants exactly one caller a bounded right to invoke
// the provider. A second caller cannot claim or shorten that lease.
func (s *Store) BeginTransportInvocation(attemptID string, expectedRevision uint64, startedAt time.Time) (TransportAttempt, error) {
	if startedAt.IsZero() || startedAt.Location() != time.UTC {
		return TransportAttempt{}, fmt.Errorf("mail delivery transport invocation start must be UTC")
	}
	current, err := s.TransportAttempt(attemptID)
	if err != nil {
		return TransportAttempt{}, err
	}
	if current.State != TransportRequested || current.Revision != expectedRevision {
		return TransportAttempt{}, fmt.Errorf("%w: transport attempt %q cannot begin at revision %d from %s/%d", ErrConflict, attemptID, expectedRevision, current.State, current.Revision)
	}
	current.State = TransportInvoking
	current.InvocationCount++
	current.InvocationStartedAt = startedAt
	current.InvocationLeaseUntil = startedAt.Add(TransportInvocationLeaseDuration)
	payload, err := encodeTransportAttempt(current)
	if err != nil {
		return TransportAttempt{}, err
	}
	if s.writer == nil {
		return TransportAttempt{}, fmt.Errorf("mail delivery conditional writes unavailable")
	}
	if err := s.writer.UpdateIfMatch(attemptID, int64(expectedRevision), beads.UpdateOpts{Metadata: map[string]string{transportAttemptDataKey: payload}}); err != nil {
		if beads.IsPreconditionFailed(err) {
			return TransportAttempt{}, fmt.Errorf("%w: %w: transport invocation already claimed", ErrTransportInvocationRace, ErrConflict)
		}
		return TransportAttempt{}, fmt.Errorf("beginning mail delivery transport invocation: %w", err)
	}
	return s.TransportAttempt(attemptID)
}

// ReleaseTransportInvocation returns a claimed attempt to requested only when
// the destination contract guarantees an identical effect-ID retry is safe.
func (s *Store) ReleaseTransportInvocation(attemptID string, expectedRevision uint64) (TransportAttempt, error) {
	current, err := s.TransportAttempt(attemptID)
	if err != nil {
		return TransportAttempt{}, err
	}
	if current.State != TransportInvoking || current.Revision != expectedRevision {
		return TransportAttempt{}, fmt.Errorf("%w: transport attempt %q cannot release from %s/%d", ErrConflict, attemptID, current.State, current.Revision)
	}
	current.State = TransportRequested
	current.InvocationLeaseUntil = time.Time{}
	payload, err := encodeTransportAttempt(current)
	if err != nil {
		return TransportAttempt{}, err
	}
	if s.writer == nil {
		return TransportAttempt{}, fmt.Errorf("mail delivery conditional writes unavailable")
	}
	if err := s.writer.UpdateIfMatch(attemptID, int64(expectedRevision), beads.UpdateOpts{Metadata: map[string]string{transportAttemptDataKey: payload}}); err != nil {
		if beads.IsPreconditionFailed(err) {
			return TransportAttempt{}, fmt.Errorf("%w: transport invocation release lost CAS", ErrConflict)
		}
		return TransportAttempt{}, fmt.Errorf("releasing mail delivery transport invocation: %w", err)
	}
	return s.TransportAttempt(attemptID)
}

// RecordTransportReceipt commits exact destination evidence under attempt CAS.
func (s *Store) RecordTransportReceipt(attemptID string, expectedRevision uint64, receipt TransportReceipt) (TransportAttempt, error) {
	attempt, err := s.finishTransportAttempt(attemptID, expectedRevision, TransportCommitted, receipt)
	if err != nil {
		return attempt, err
	}
	if err := s.finalizeCommittedTransport(attempt); err != nil {
		return attempt, err
	}
	return attempt, nil
}

// RecordUnknownTransportState closes an ambiguous invocation without retrying it.
func (s *Store) RecordUnknownTransportState(attemptID string, expectedRevision uint64, recordedAt time.Time) (TransportAttempt, error) {
	attempt, err := s.TransportAttempt(attemptID)
	if err != nil {
		return TransportAttempt{}, err
	}
	receipt := TransportReceipt{
		Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
		State: EffectUnknownExternalState, RecordedAt: recordedAt,
	}
	return s.finishTransportAttempt(attemptID, expectedRevision, TransportUnknownExternalState, receipt)
}

func (s *Store) finishTransportAttempt(attemptID string, expectedRevision uint64, state TransportState, receipt TransportReceipt) (TransportAttempt, error) {
	current, err := s.TransportAttempt(attemptID)
	if err != nil {
		return TransportAttempt{}, err
	}
	if current.State != TransportInvoking {
		if current.State == state && sameTransportReceipt(current.Receipt, receipt) {
			return current, nil
		}
		return TransportAttempt{}, fmt.Errorf("%w: transport attempt %q is already %s", ErrConflict, attemptID, current.State)
	}
	if current.Revision != expectedRevision {
		return TransportAttempt{}, fmt.Errorf("%w: transport attempt %q expected revision %d, current %d", ErrConflict, attemptID, expectedRevision, current.Revision)
	}
	if err := receipt.Validate(current.AttemptID, current.NudgeID); err != nil {
		return TransportAttempt{}, err
	}
	if (state == TransportCommitted) != (receipt.State == EffectCommitted) ||
		(state == TransportUnknownExternalState) != (receipt.State == EffectUnknownExternalState) {
		return TransportAttempt{}, fmt.Errorf("mail delivery transport state and receipt differ")
	}
	current.State = state
	current.Receipt = receipt
	payload, err := encodeTransportAttempt(current)
	if err != nil {
		return TransportAttempt{}, err
	}
	if s.writer == nil {
		return TransportAttempt{}, fmt.Errorf("mail delivery conditional writes unavailable")
	}
	if err := s.writer.UpdateIfMatch(attemptID, int64(expectedRevision), beads.UpdateOpts{Metadata: map[string]string{transportAttemptDataKey: payload}}); err != nil {
		if beads.IsPreconditionFailed(err) {
			latest, loadErr := s.TransportAttempt(attemptID)
			if loadErr == nil && latest.State == state && sameTransportReceipt(latest.Receipt, receipt) {
				return latest, nil
			}
			return TransportAttempt{}, fmt.Errorf("%w: transport attempt revision moved before receipt commit", ErrConflict)
		}
		return TransportAttempt{}, fmt.Errorf("committing mail delivery transport receipt: %w", err)
	}
	return s.TransportAttempt(attemptID)
}

func (s *Store) finalizeCommittedTransport(attempt TransportAttempt) error {
	for _, deliveryID := range attempt.CoveredDeliveryIDs {
		if err := s.finalizeCoveredTransport(deliveryID, attempt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) finalizeCoveredTransport(deliveryID string, attempt TransportAttempt) error {
	delivery, err := s.Get(deliveryID)
	if err != nil {
		return err
	}
	if delivery.Phase == PhaseRuntimeNotified {
		return nil
	}
	if delivery.Phase != PhaseNotificationRequested {
		return fmt.Errorf("%w: committed transport delivery is in phase %q", ErrConflict, delivery.Phase)
	}
	raw, err := s.beads.Get(delivery.ID)
	if err != nil {
		return err
	}
	_, linkedAttemptID, err := parseReceiptLink(raw.Metadata[transportAttemptLinkKey])
	if err != nil || linkedAttemptID != attempt.AttemptID {
		return fmt.Errorf("%w: committed transport attempt is not linked to delivery", ErrConflict)
	}
	delivery.Phase = PhaseRuntimeNotified
	payload, err := encodeDelivery(delivery)
	if err != nil {
		return err
	}
	if s.writer == nil {
		return fmt.Errorf("mail delivery conditional writes unavailable")
	}
	if err := s.writer.UpdateIfMatch(delivery.ID, int64(delivery.Revision), beads.UpdateOpts{Metadata: map[string]string{
		deliveryDataKey: payload, deliveryPhaseKey: string(PhaseRuntimeNotified),
	}}); err != nil {
		if beads.IsPreconditionFailed(err) {
			latest, loadErr := s.Get(delivery.ID)
			if loadErr == nil && latest.Phase == PhaseRuntimeNotified {
				return nil
			}
			return fmt.Errorf("%w: delivery revision moved before runtime-notified commit", ErrConflict)
		}
		return fmt.Errorf("committing mail delivery runtime-notified phase: %w", err)
	}
	return nil
}

// LinkCoveredTransportAttempt repairs the crash gap between creation of one
// coalesced attempt and linking every covered delivery. It never changes the
// attempt identity or invokes the external effect.
func (s *Store) LinkCoveredTransportAttempt(attempt TransportAttempt) error {
	if err := attempt.validate(); err != nil {
		return err
	}
	for _, deliveryID := range attempt.CoveredDeliveryIDs {
		delivery, err := s.Get(deliveryID)
		if err != nil {
			return err
		}
		if delivery.Phase == PhaseNotificationRequested || delivery.Phase == PhaseRuntimeNotified {
			linked, err := s.linkedTransportAttempt(delivery.ID)
			if err != nil || linked.AttemptID != attempt.AttemptID {
				return fmt.Errorf("%w: covered delivery %q has a different transport attempt", ErrConflict, delivery.ID)
			}
			continue
		}
		if delivery.Phase != PhaseWaitingForActivation {
			return fmt.Errorf("%w: covered delivery %q is in phase %q", ErrConflict, delivery.ID, delivery.Phase)
		}
		if err := s.linkCoveredTransportAttempt(delivery, attempt); err != nil {
			return err
		}
	}
	if attempt.State == TransportCommitted {
		return s.finalizeCommittedTransport(attempt)
	}
	return nil
}

func (s *Store) linkCoveredTransportAttempt(delivery Delivery, attempt TransportAttempt) error {
	if !slices.Contains(attempt.CoveredDeliveryIDs, delivery.ID) {
		return fmt.Errorf("%w: transport attempt does not cover delivery %q", ErrConflict, delivery.ID)
	}
	delivery.Phase = PhaseNotificationRequested
	payload, err := encodeDelivery(delivery)
	if err != nil {
		return err
	}
	if s.writer == nil {
		return fmt.Errorf("mail delivery conditional writes unavailable")
	}
	err = s.writer.UpdateIfMatch(delivery.ID, int64(delivery.Revision), beads.UpdateOpts{Metadata: map[string]string{
		deliveryDataKey: payload, deliveryPhaseKey: string(PhaseNotificationRequested),
		transportAttemptLinkKey: receiptLink(delivery.Revision, attempt.AttemptID),
	}})
	if err != nil {
		if beads.IsPreconditionFailed(err) {
			linked, loadErr := s.linkedTransportAttempt(delivery.ID)
			if loadErr == nil && linked.AttemptID == attempt.AttemptID {
				return nil
			}
			return fmt.Errorf("%w: delivery revision moved before coalesced transport link", ErrConflict)
		}
		return fmt.Errorf("linking coalesced mail delivery transport attempt: %w", err)
	}
	return nil
}

func (a TransportAttempt) validate() error {
	if a.Version != 1 || a.Revision == 0 || !validPrefixedHash(a.AttemptID, "mail-attempt-") ||
		!validPrefixedHash(a.NudgeID, "mail-nudge-") || !validPrefixedHash(a.DeliveryID, "mail-delivery-") ||
		a.ExpectedDeliveryRevision == 0 || len(a.CoveredDeliveryIDs) == 0 ||
		!validRef(string(a.AuthorityKind)) || !validRef(a.AuthorityRef) || a.AuthorityGeneration == 0 ||
		!validHash(a.AuthorityIntentSHA256) || !validRef(a.SessionRef) || a.ContinuationEpoch == 0 ||
		!validHash(a.InstanceTokenSHA256) || a.CreatedAt.IsZero() || a.CreatedAt.Location() != time.UTC {
		return fmt.Errorf("mail delivery transport attempt identity is invalid")
	}
	for i, id := range a.CoveredDeliveryIDs {
		if !validPrefixedHash(id, "mail-delivery-") || (i > 0 && a.CoveredDeliveryIDs[i-1] >= id) {
			return fmt.Errorf("mail delivery transport attempt coverage is invalid")
		}
	}
	if !slices.Contains(a.CoveredDeliveryIDs, a.DeliveryID) {
		return fmt.Errorf("mail delivery transport attempt does not cover its primary delivery")
	}
	wantAttemptID := attemptIDFromAuthority(a.DeliveryID, a.AuthorityKind, a.AuthorityRef,
		a.AuthorityGeneration, a.AuthorityIntentSHA256, a.SessionRef, a.ContinuationEpoch, a.InstanceTokenSHA256)
	if a.AttemptID != wantAttemptID {
		return fmt.Errorf("mail delivery transport attempt ID differs from stable authority")
	}
	wantNudgeID, err := NudgeID(a.AttemptID, a.CoveredDeliveryIDs)
	if err != nil || a.NudgeID != wantNudgeID {
		return fmt.Errorf("mail delivery transport nudge ID differs from coverage")
	}
	switch a.State {
	case TransportRequested:
		if a.Receipt != (TransportReceipt{}) {
			return fmt.Errorf("requested transport attempt carries a receipt")
		}
		if !a.InvocationLeaseUntil.IsZero() || (a.InvocationCount == 0) != a.InvocationStartedAt.IsZero() ||
			(!a.InvocationStartedAt.IsZero() && a.InvocationStartedAt.Location() != time.UTC) {
			return fmt.Errorf("requested transport attempt carries invocation lease")
		}
	case TransportInvoking:
		if a.InvocationCount == 0 || a.Receipt != (TransportReceipt{}) || a.InvocationStartedAt.IsZero() || a.InvocationStartedAt.Location() != time.UTC ||
			a.InvocationLeaseUntil.IsZero() || a.InvocationLeaseUntil.Location() != time.UTC ||
			!a.InvocationLeaseUntil.Equal(a.InvocationStartedAt.Add(TransportInvocationLeaseDuration)) {
			return fmt.Errorf("invoking transport attempt lease is invalid")
		}
	case TransportCommitted, TransportUnknownExternalState:
		if a.InvocationCount == 0 || a.InvocationStartedAt.IsZero() || a.InvocationStartedAt.Location() != time.UTC ||
			a.InvocationLeaseUntil.IsZero() || a.InvocationLeaseUntil.Location() != time.UTC {
			return fmt.Errorf("terminal transport attempt lacks invocation identity")
		}
		if err := a.Receipt.Validate(a.AttemptID, a.NudgeID); err != nil {
			return err
		}
	default:
		return fmt.Errorf("mail delivery transport attempt state is invalid")
	}
	return nil
}

func encodeTransportAttempt(attempt TransportAttempt) (string, error) {
	attempt.Revision = 0
	payload, err := json.Marshal(attempt)
	if err != nil {
		return "", fmt.Errorf("encoding mail delivery transport attempt: %w", err)
	}
	return string(payload), nil
}

func sameTransportIntent(existing, proposed TransportAttempt) bool {
	existing.CreatedAt = proposed.CreatedAt
	existing.State = proposed.State
	existing.Receipt = proposed.Receipt
	existing.InvocationStartedAt = proposed.InvocationStartedAt
	existing.InvocationLeaseUntil = proposed.InvocationLeaseUntil
	existing.InvocationCount = proposed.InvocationCount
	existing.Revision = 0
	proposed.Revision = 0
	return transportAttemptEqual(existing, proposed)
}

func sameTransportReceipt(existing, proposed TransportReceipt) bool {
	existing.RecordedAt = proposed.RecordedAt
	return existing == proposed
}

func transportAttemptEqual(a, b TransportAttempt) bool {
	if !slices.Equal(a.CoveredDeliveryIDs, b.CoveredDeliveryIDs) {
		return false
	}
	a.CoveredDeliveryIDs = nil
	b.CoveredDeliveryIDs = nil
	return reflect.DeepEqual(a, b)
}
