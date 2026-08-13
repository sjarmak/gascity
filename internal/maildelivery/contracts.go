// Package maildelivery defines the content-free durable mail delivery contract.
package maildelivery

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// AuthorityKind identifies the controller that issued an activation fence.
type AuthorityKind string

const (
	// AuthorityNamedSessionControllerV1 is the narrow controller-issued session projection.
	AuthorityNamedSessionControllerV1 AuthorityKind = "named-session-controller-v1"
	// AuthorityActivationContractV0Alpha1 is the future full Activation Contract authority.
	AuthorityActivationContractV0Alpha1 AuthorityKind = "activation-contract-v0alpha1"
)

// ActivationFence binds one mail attempt to controller-resolved session authority.
type ActivationFence struct {
	Version               int           `json:"version"`
	FenceID               string        `json:"fence_id"`
	CityRef               string        `json:"city_ref"`
	SeatRef               string        `json:"seat_ref"`
	AuthorityKind         AuthorityKind `json:"authority_kind"`
	AuthorityRef          string        `json:"authority_ref"`
	AuthorityGeneration   uint64        `json:"authority_generation"`
	AuthorityIntentSHA256 string        `json:"authority_intent_sha256"`
	SessionRef            string        `json:"session_ref"`
	ContinuationEpoch     uint64        `json:"continuation_epoch"`
	InstanceTokenSHA256   string        `json:"instance_token_sha256"`
	IssuedByRef           string        `json:"issued_by_ref"`
	IssuedAt              time.Time     `json:"issued_at"`
}

// Validate rejects incomplete, caller-shaped, or noncanonical fences.
func (f ActivationFence) Validate() error {
	if f.Version != 1 {
		return fmt.Errorf("activation fence version must be 1")
	}
	if !validPrefixedHash(f.FenceID, "mail-activation-") {
		return fmt.Errorf("activation fence ID is invalid")
	}
	for name, value := range map[string]string{
		"city_ref": f.CityRef, "seat_ref": f.SeatRef, "authority_ref": f.AuthorityRef,
		"session_ref": f.SessionRef, "issued_by_ref": f.IssuedByRef,
	} {
		if !validRef(value) {
			return fmt.Errorf("activation fence %s is invalid", name)
		}
	}
	if f.AuthorityKind != AuthorityNamedSessionControllerV1 && f.AuthorityKind != AuthorityActivationContractV0Alpha1 {
		return fmt.Errorf("activation fence authority kind is invalid")
	}
	if f.AuthorityGeneration == 0 || f.ContinuationEpoch == 0 {
		return fmt.Errorf("activation fence generation and epoch must be nonzero")
	}
	if !validHash(f.AuthorityIntentSHA256) || !validHash(f.InstanceTokenSHA256) {
		return fmt.Errorf("activation fence digest is invalid")
	}
	if f.IssuedAt.IsZero() || f.IssuedAt.Location() != time.UTC {
		return fmt.Errorf("activation fence issue time must be UTC")
	}
	return nil
}

// DeliveryID returns the stable identity for one message delivered to one seat.
func DeliveryID(storeRef, messageID, seatRef string) (string, error) {
	for name, value := range map[string]string{"store_ref": storeRef, "message_id": messageID, "seat_ref": seatRef} {
		if !validRef(value) {
			return "", fmt.Errorf("%s is invalid", name)
		}
	}
	return "mail-delivery-" + digest("mail-delivery-v1", storeRef, messageID, seatRef), nil
}

// AttemptID returns the stable identity for one delivery under logical authority.
// Issuance-only fence metadata is deliberately excluded.
func AttemptID(deliveryID string, fence ActivationFence) (string, error) {
	if !validPrefixedHash(deliveryID, "mail-delivery-") {
		return "", fmt.Errorf("delivery ID is invalid")
	}
	if err := fence.Validate(); err != nil {
		return "", err
	}
	return "mail-attempt-" + digest(
		"mail-attempt-v1", deliveryID, string(fence.AuthorityKind), fence.AuthorityRef,
		fmt.Sprint(fence.AuthorityGeneration), fence.AuthorityIntentSHA256, fence.SessionRef,
		fmt.Sprint(fence.ContinuationEpoch), fence.InstanceTokenSHA256,
	), nil
}

// Phase is the closed durable delivery phase vocabulary.
type Phase string

const (
	// PhaseStored means the canonical delivery exists but has not been evaluated.
	PhaseStored Phase = "stored"
	// PhaseWaitingForActivation means no usable recipient authority is current.
	PhaseWaitingForActivation Phase = "waiting-for-activation"
	// PhaseNotificationRequested means a fenced transport intent exists.
	PhaseNotificationRequested Phase = "notification-requested"
	// PhaseRuntimeNotified means the destination committed the stable effect.
	PhaseRuntimeNotified Phase = "runtime-notified"
	// PhaseRead means an exact read receipt exists.
	PhaseRead Phase = "read"
	// PhaseDispositioned is the terminal phase reached through typed disposition.
	PhaseDispositioned Phase = "dispositioned"
)

var legalTransitions = map[Phase]map[Phase]struct{}{
	PhaseStored:                {PhaseWaitingForActivation: {}, PhaseDispositioned: {}},
	PhaseWaitingForActivation:  {PhaseNotificationRequested: {}, PhaseDispositioned: {}},
	PhaseNotificationRequested: {PhaseRuntimeNotified: {}, PhaseWaitingForActivation: {}, PhaseDispositioned: {}},
	PhaseRuntimeNotified:       {PhaseRead: {}, PhaseDispositioned: {}, PhaseWaitingForActivation: {}},
	PhaseRead:                  {PhaseDispositioned: {}},
}

// ValidateTransition rejects unknown phases and unregistered phase edges.
func ValidateTransition(from, to Phase) error {
	allowed, ok := legalTransitions[from]
	if !ok {
		return fmt.Errorf("unknown mail delivery phase %q", from)
	}
	if _, ok := allowed[to]; !ok {
		return fmt.Errorf("illegal mail delivery transition %q -> %q", from, to)
	}
	return nil
}

func digest(parts ...string) string {
	h := sha256.New()
	var size [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validPrefixedHash(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && validHash(strings.TrimPrefix(value, prefix))
}

func validRef(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value || strings.ToLower(value) != value {
		return false
	}
	return !strings.ContainsAny(value, "\x00\r\n\t")
}
