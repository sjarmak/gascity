package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrStableNudgeUnsupported reports that a provider lacks the declared contract.
	ErrStableNudgeUnsupported = errors.New("stable nudge is unsupported")
	// ErrStableNudgeRetrySafe reports response loss after an idempotent destination call.
	ErrStableNudgeRetrySafe = errors.New("stable nudge response lost; identical retry is safe")
	// ErrStableNudgeConflict reports reuse of an effect ID for different input.
	ErrStableNudgeConflict = errors.New("stable nudge identity conflicts with destination state")
)

// StableNudgeCommitBoundaryDestinationAtomic names atomic effect+receipt persistence.
const StableNudgeCommitBoundaryDestinationAtomic = "destination-atomic-effect-receipt"

// StableNudgeProvider accepts destination-idempotent nudges under stable IDs.
type StableNudgeProvider interface {
	SupportsStableNudge() bool
	NudgeStable(context.Context, string, string, []ContentBlock) (StableNudgeReceipt, error)
}

// StableNudgeRequest is the body-free exec-provider wire request.
type StableNudgeRequest struct {
	Version  int            `json:"version"`
	EffectID string         `json:"effect_id"`
	Content  []ContentBlock `json:"content"`
}

// StableNudgeReceipt is destination evidence for one stable physical effect.
type StableNudgeReceipt struct {
	Version           int       `json:"version"`
	EffectID          string    `json:"effect_id"`
	TargetRuntimeName string    `json:"target_runtime_name"`
	ContentSHA256     string    `json:"content_sha256"`
	DestinationRef    string    `json:"destination_ref"`
	CommitBoundary    string    `json:"commit_boundary"`
	AcceptedAt        time.Time `json:"accepted_at"`
	ReceiptSHA256     string    `json:"receipt_sha256"`
}

// NewStableNudgeReceipt constructs and validates a canonical receipt.
func NewStableNudgeReceipt(effectID, target string, content []ContentBlock, destinationRef string, acceptedAt time.Time) (StableNudgeReceipt, error) {
	contentHash, err := stableNudgeContentSHA256(content)
	if err != nil {
		return StableNudgeReceipt{}, err
	}
	r := StableNudgeReceipt{
		Version: 1, EffectID: effectID, TargetRuntimeName: target, ContentSHA256: contentHash,
		DestinationRef: destinationRef, CommitBoundary: StableNudgeCommitBoundaryDestinationAtomic, AcceptedAt: acceptedAt.UTC(),
	}
	r.ReceiptSHA256, err = r.digest()
	if err != nil {
		return StableNudgeReceipt{}, err
	}
	if err := r.Validate(); err != nil {
		return StableNudgeReceipt{}, err
	}
	return r, nil
}

// Validate checks receipt identity, boundary, time, and digest.
func (r StableNudgeReceipt) Validate() error {
	if r.Version != 1 || !validStableNudgeID(r.EffectID) || !validStableNudgeRef(r.TargetRuntimeName) ||
		!validStableNudgeRef(r.DestinationRef) || r.CommitBoundary != StableNudgeCommitBoundaryDestinationAtomic ||
		r.AcceptedAt.IsZero() || r.AcceptedAt.Location() != time.UTC || !validLowerHexSHA256(r.ContentSHA256) || !validLowerHexSHA256(r.ReceiptSHA256) {
		return fmt.Errorf("stable nudge receipt identity is invalid")
	}
	digest, err := r.digest()
	if err != nil || digest != r.ReceiptSHA256 {
		return fmt.Errorf("stable nudge receipt digest is invalid")
	}
	return nil
}

func (r StableNudgeReceipt) digest() (string, error) {
	r.ReceiptSHA256 = ""
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// StableNudgeContentSHA256 returns the canonical structured-content digest.
func StableNudgeContentSHA256(content []ContentBlock) (string, error) {
	return stableNudgeContentSHA256(content)
}

func stableNudgeContentSHA256(content []ContentBlock) (string, error) {
	b, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("encoding stable nudge content: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func validStableNudgeID(v string) bool {
	return strings.HasPrefix(v, "mail-nudge-") && len(v) == len("mail-nudge-")+sha256.Size*2 && validLowerHexSHA256(strings.TrimPrefix(v, "mail-nudge-"))
}

// ValidateStableNudgeEffectID rejects malformed IDs before destination use.
func ValidateStableNudgeEffectID(v string) error {
	if !validStableNudgeID(v) {
		return fmt.Errorf("stable nudge effect ID is invalid")
	}
	return nil
}

func validLowerHexSHA256(v string) bool {
	if len(v) != sha256.Size*2 || strings.ToLower(v) != v {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}

func validStableNudgeRef(v string) bool {
	return v != "" && strings.TrimSpace(v) == v && !strings.ContainsAny(v, "\x00\r\n\t")
}
