package maildelivery

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrAuthorityUnavailable reports that the controller-owned activation
// authority could not be resolved. Callers may retry it, but must not replace
// it with caller-supplied identity or a self-attested fence.
var ErrAuthorityUnavailable = errors.New("mail delivery authority unavailable")

// Policy is the closed delivery completion policy.
type Policy string

const (
	// PolicyNotifyOnly requires a committed transport receipt.
	PolicyNotifyOnly Policy = "notify-only"
	// PolicyReadRequired requires an exact current-authority read receipt.
	PolicyReadRequired Policy = "read-required"
	// PolicyResponseRequired requires an exact current-authority response receipt.
	PolicyResponseRequired Policy = "response-required"
)

// Attention controls when notification may be attempted.
type Attention string

const (
	// AttentionImmediate permits notification under current authority immediately.
	AttentionImmediate Attention = "immediate"
	// AttentionNextActive waits for the next usable activation.
	AttentionNextActive Attention = "next-active"
)

// Delivery is the canonical, content-free durable intent for one message/seat.
type Delivery struct {
	Version            int        `json:"version"`
	ID                 string     `json:"id"`
	StoreRef           string     `json:"store_ref"`
	MessageID          string     `json:"message_id"`
	MessageRevision    uint64     `json:"message_revision"`
	SeatRef            string     `json:"seat_ref"`
	Policy             Policy     `json:"policy"`
	Attention          Attention  `json:"attention"`
	PolicySourceSHA256 string     `json:"policy_source_sha256,omitempty"`
	Phase              Phase      `json:"phase"`
	Revision           uint64     `json:"-"`
	CreatedAt          time.Time  `json:"created_at"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
}

// NewDelivery constructs and validates one canonical delivery from a durable
// message identity. Message content is deliberately not an input.
func NewDelivery(storeRef, messageID string, messageRevision uint64, seatRef string, policy Policy, attention Attention, createdAt time.Time, expiresAt *time.Time, policySourceSHA256 string) (Delivery, error) {
	id, err := DeliveryID(storeRef, messageID, seatRef)
	if err != nil {
		return Delivery{}, err
	}
	delivery := Delivery{
		Version: 1, ID: id, StoreRef: storeRef, MessageID: messageID,
		MessageRevision: messageRevision, SeatRef: seatRef, Policy: policy,
		Attention: attention, PolicySourceSHA256: policySourceSHA256,
		Phase: PhaseStored, Revision: 1, CreatedAt: createdAt, ExpiresAt: expiresAt,
	}
	if err := delivery.Validate(); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

// Validate rejects identity drift, unknown enums, and incomplete expiry policy.
func (d Delivery) Validate() error {
	if d.Version != 1 || d.Revision == 0 || d.MessageRevision == 0 {
		return fmt.Errorf("mail delivery version and revisions must be nonzero")
	}
	wantID, err := DeliveryID(d.StoreRef, d.MessageID, d.SeatRef)
	if err != nil {
		return err
	}
	if d.ID != wantID {
		return fmt.Errorf("mail delivery ID does not match immutable identity")
	}
	if !validPolicy(d.Policy) || !validAttention(d.Attention) || !validPhase(d.Phase) {
		return fmt.Errorf("mail delivery enum is invalid")
	}
	if d.CreatedAt.IsZero() || d.CreatedAt.Location() != time.UTC {
		return fmt.Errorf("mail delivery creation time must be UTC")
	}
	if d.ExpiresAt != nil {
		if d.ExpiresAt.Location() != time.UTC || !d.ExpiresAt.After(d.CreatedAt) || !validHash(d.PolicySourceSHA256) {
			return fmt.Errorf("mail delivery expiry is invalid")
		}
	} else if d.PolicySourceSHA256 != "" && !validHash(d.PolicySourceSHA256) {
		return fmt.Errorf("mail delivery policy source digest is invalid")
	}
	return nil
}

// ReceiptAuthority is the exact fence and message revision carried by a receipt.
type ReceiptAuthority struct {
	DeliveryID              string        `json:"delivery_id"`
	StoreRef                string        `json:"store_ref"`
	MessageID               string        `json:"message_id"`
	SeatRef                 string        `json:"seat_ref"`
	FenceID                 string        `json:"fence_id"`
	AuthorityKind           AuthorityKind `json:"authority_kind"`
	AuthorityRef            string        `json:"authority_ref"`
	AuthorityGeneration     uint64        `json:"authority_generation"`
	AuthorityIntentSHA256   string        `json:"authority_intent_sha256"`
	SessionRef              string        `json:"session_ref"`
	ContinuationEpoch       uint64        `json:"continuation_epoch"`
	InstanceTokenSHA256     string        `json:"instance_token_sha256"`
	ObservedMessageRevision uint64        `json:"observed_message_revision"`
	RecordedAt              time.Time     `json:"recorded_at"`
}

// ReceiptAuthorityFromFence copies authority from controller output, not callers.
func ReceiptAuthorityFromFence(f ActivationFence, d Delivery, recordedAt time.Time) ReceiptAuthority {
	return ReceiptAuthority{
		DeliveryID: d.ID, StoreRef: d.StoreRef, MessageID: d.MessageID, SeatRef: d.SeatRef,
		FenceID: f.FenceID, AuthorityKind: f.AuthorityKind, AuthorityRef: f.AuthorityRef,
		AuthorityGeneration: f.AuthorityGeneration, AuthorityIntentSHA256: f.AuthorityIntentSHA256,
		SessionRef: f.SessionRef, ContinuationEpoch: f.ContinuationEpoch,
		InstanceTokenSHA256:     f.InstanceTokenSHA256,
		ObservedMessageRevision: d.MessageRevision, RecordedAt: recordedAt,
	}
}

// ValidateAgainst requires exact equality with freshly controller-resolved authority.
func (a ReceiptAuthority) ValidateAgainst(f ActivationFence, d Delivery) error {
	if err := f.Validate(); err != nil {
		return err
	}
	want := ReceiptAuthorityFromFence(f, d, a.RecordedAt)
	if a != want || a.RecordedAt.IsZero() || a.RecordedAt.Location() != time.UTC {
		return fmt.Errorf("receipt authority does not match current fence and message revision")
	}
	return nil
}

// EffectState records whether an external transport effect is proven or uncertain.
type EffectState string

const (
	// EffectCommitted means destination evidence proves the stable effect.
	EffectCommitted EffectState = "committed"
	// EffectUnknownExternalState means commit status cannot be proven either way.
	EffectUnknownExternalState EffectState = "unknown_external_state"
)

// TransportCommitBoundary names what a committed receipt actually proves.
type TransportCommitBoundary string

const (
	// TransportCommitBoundaryProviderReturn proves only that the provider call
	// returned success. It is not destination idempotency or agent consumption.
	TransportCommitBoundaryProviderReturn TransportCommitBoundary = "provider-nudge-return"
	// TransportCommitBoundaryDestinationAtomic proves the destination atomically
	// bound the stable nudge ID to the accepted effect and durable receipt.
	TransportCommitBoundaryDestinationAtomic TransportCommitBoundary = "destination-atomic-effect-receipt"
)

// TransportReceipt binds one stable nudge effect to destination evidence.
type TransportReceipt struct {
	Version        int                     `json:"version"`
	AttemptID      string                  `json:"attempt_id"`
	NudgeID        string                  `json:"nudge_id"`
	State          EffectState             `json:"state"`
	CommitBoundary TransportCommitBoundary `json:"commit_boundary,omitempty"`
	ReceiptRef     string                  `json:"receipt_ref,omitempty"`
	ReceiptSHA256  string                  `json:"receipt_sha256,omitempty"`
	RecordedAt     time.Time               `json:"recorded_at"`
}

// Validate rejects flattering or identity-mismatched transport evidence.
func (r TransportReceipt) Validate(attemptID, nudgeID string) error {
	if r.Version != 1 || r.AttemptID != attemptID || r.NudgeID != nudgeID ||
		r.RecordedAt.IsZero() || r.RecordedAt.Location() != time.UTC {
		return fmt.Errorf("transport receipt identity is invalid")
	}
	switch r.State {
	case EffectCommitted:
		if (r.CommitBoundary != TransportCommitBoundaryProviderReturn && r.CommitBoundary != TransportCommitBoundaryDestinationAtomic) ||
			!validRef(r.ReceiptRef) || !validHash(r.ReceiptSHA256) {
			return fmt.Errorf("committed transport receipt lacks destination evidence")
		}
	case EffectUnknownExternalState:
		if r.CommitBoundary != "" || r.ReceiptRef != "" || r.ReceiptSHA256 != "" {
			return fmt.Errorf("unknown transport state carries flattering evidence")
		}
	default:
		return fmt.Errorf("transport receipt state is invalid")
	}
	return nil
}

// NudgeID returns the stable external-effect identity for one covered set.
func NudgeID(attemptID string, deliveryIDs []string) (string, error) {
	if !validPrefixedHash(attemptID, "mail-attempt-") || len(deliveryIDs) == 0 {
		return "", fmt.Errorf("nudge identity input is invalid")
	}
	covered := append([]string(nil), deliveryIDs...)
	sort.Strings(covered)
	for i, id := range covered {
		if !validPrefixedHash(id, "mail-delivery-") || (i > 0 && covered[i-1] == id) {
			return "", fmt.Errorf("covered delivery identity is invalid")
		}
	}
	return "mail-nudge-" + digest("mail-nudge-v1", attemptID, strings.Join(covered, "\n")), nil
}

// DispositionReason is the closed terminal reason vocabulary.
type DispositionReason string

const (
	// DispositionPolicySatisfiedNotified closes a notify-only delivery with transport proof.
	DispositionPolicySatisfiedNotified DispositionReason = "policy-satisfied-notified"
	// DispositionPolicySatisfiedRead closes a read-required delivery with read proof.
	DispositionPolicySatisfiedRead DispositionReason = "policy-satisfied-read"
	// DispositionPolicySatisfiedResponse closes a response-required delivery with response proof.
	DispositionPolicySatisfiedResponse DispositionReason = "policy-satisfied-response"
	// DispositionRecipientDeclined records an explicit recipient-authority decision.
	DispositionRecipientDeclined DispositionReason = "recipient-declined"
	// DispositionSenderCanceled records an explicit sender-authority decision.
	DispositionSenderCanceled DispositionReason = "sender-canceled"
	// DispositionSuperseded records a durable replacement relation.
	DispositionSuperseded DispositionReason = "superseded"
	// DispositionExpired records expiry under the immutable policy source.
	DispositionExpired DispositionReason = "expired"
	// DispositionRecipientSeatRetired records controller-proven stable-seat retirement.
	DispositionRecipientSeatRetired DispositionReason = "recipient-seat-retired"
)

// DispositionProof is the already-validated mechanical authority summary.
type DispositionProof struct {
	TransportCommitted      bool
	ReadReceipt             bool
	ResponseReceipt         bool
	RecipientAuthority      bool
	SenderAuthority         bool
	ReplacementRelation     bool
	ExpiryAuthority         bool
	SeatRetirementAuthority bool
}

// ValidateDisposition enforces policy and named authority before mutation.
func ValidateDisposition(d Delivery, reason DispositionReason, proof DispositionProof) error {
	if err := d.Validate(); err != nil {
		return err
	}
	allowed := false
	switch reason {
	case DispositionPolicySatisfiedNotified:
		allowed = d.Policy == PolicyNotifyOnly && proof.TransportCommitted
	case DispositionPolicySatisfiedRead:
		allowed = d.Policy == PolicyReadRequired && proof.ReadReceipt
	case DispositionPolicySatisfiedResponse:
		allowed = d.Policy == PolicyResponseRequired && proof.ResponseReceipt
	case DispositionRecipientDeclined:
		allowed = proof.RecipientAuthority
	case DispositionSenderCanceled:
		allowed = proof.SenderAuthority
	case DispositionSuperseded:
		allowed = proof.ReplacementRelation
	case DispositionExpired:
		allowed = proof.ExpiryAuthority
	case DispositionRecipientSeatRetired:
		allowed = proof.SeatRetirementAuthority
	}
	if !allowed {
		return fmt.Errorf("mail delivery disposition %q is not authorized", reason)
	}
	return nil
}

func validPolicy(p Policy) bool {
	return p == PolicyNotifyOnly || p == PolicyReadRequired || p == PolicyResponseRequired
}

func validAttention(a Attention) bool { return a == AttentionImmediate || a == AttentionNextActive }

func validPhase(p Phase) bool {
	switch p {
	case PhaseStored, PhaseWaitingForActivation, PhaseNotificationRequested, PhaseRuntimeNotified, PhaseRead, PhaseDispositioned:
		return true
	default:
		return false
	}
}
