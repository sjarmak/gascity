package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

func newNudgeAcceptanceReceipt(effectID, sessionRef, runtimeName, provider, transport string, acceptedAt time.Time) (*NudgeAcceptanceReceipt, error) {
	receipt := NudgeAcceptanceReceipt{
		Version: 1, EffectID: effectID, TargetSessionRef: sessionRef,
		TargetRuntimeName: runtimeName, Provider: provider, Transport: transport,
		CommitBoundary: NudgeCommitBoundaryProviderReturn, AcceptedAt: acceptedAt.UTC(),
	}
	digest, err := receipt.digest()
	if err != nil {
		return nil, err
	}
	receipt.ReceiptSHA256 = digest
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	return &receipt, nil
}

// Validate verifies the exact provider-acceptance tuple and its digest.
func (r NudgeAcceptanceReceipt) Validate() error {
	if r.Version != 1 || !validNudgeEffectID(r.EffectID) || !validReceiptRef(r.TargetRuntimeName) ||
		!validReceiptRef(r.Provider) || !validReceiptRef(r.Transport) ||
		(r.CommitBoundary != NudgeCommitBoundaryProviderReturn && r.CommitBoundary != NudgeCommitBoundaryDestinationAtomic) || r.AcceptedAt.IsZero() || r.AcceptedAt.Location() != time.UTC {
		return fmt.Errorf("nudge provider-acceptance receipt identity is invalid")
	}
	if r.TargetSessionRef != "" && !validReceiptRef(r.TargetSessionRef) {
		return fmt.Errorf("nudge provider-acceptance receipt session identity is invalid")
	}
	if r.CommitBoundary == NudgeCommitBoundaryDestinationAtomic {
		if !validReceiptRef(r.DestinationRef) || !validSHA256(r.ContentSHA256) || !validSHA256(r.DestinationReceiptSHA256) {
			return fmt.Errorf("nudge destination-atomic receipt evidence is invalid")
		}
	} else if r.ContentSHA256 != "" || r.DestinationRef != "" || r.DestinationReceiptSHA256 != "" {
		return fmt.Errorf("nudge provider-acceptance receipt carries destination evidence")
	}
	digest, err := r.digest()
	if err != nil || r.ReceiptSHA256 != digest {
		return fmt.Errorf("nudge provider-acceptance receipt digest is invalid")
	}
	return nil
}

func newDestinationAtomicNudgeReceipt(effectID, sessionRef, provider, transport string, source runtime.StableNudgeReceipt) (*NudgeAcceptanceReceipt, error) {
	receipt := NudgeAcceptanceReceipt{
		Version: 1, EffectID: effectID, TargetSessionRef: sessionRef,
		TargetRuntimeName: source.TargetRuntimeName, Provider: provider, Transport: transport,
		CommitBoundary: NudgeCommitBoundaryDestinationAtomic, AcceptedAt: source.AcceptedAt,
		ContentSHA256: source.ContentSHA256, DestinationRef: source.DestinationRef,
		DestinationReceiptSHA256: source.ReceiptSHA256,
	}
	var err error
	receipt.ReceiptSHA256, err = receipt.digest()
	if err != nil {
		return nil, err
	}
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (r NudgeAcceptanceReceipt) digest() (string, error) {
	r.ReceiptSHA256 = ""
	payload, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encoding nudge provider-acceptance receipt: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func validNudgeEffectID(value string) bool {
	const prefix = "mail-nudge-"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 {
		return false
	}
	encoded := strings.TrimPrefix(value, prefix)
	if strings.ToLower(encoded) != encoded {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func validReceiptRef(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n\t")
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
