package beads

import "fmt"

// BlockedStatusObservation is the raw lifecycle/projection tuple used by the
// blocked-status reconciler. Status is intentionally not passed through the
// legacy Gas City status mapper, which collapses upstream blocked to open.
type BlockedStatusObservation struct {
	ID        string
	Status    string
	IsBlocked bool
	Revision  int64
	Metadata  map[string]string
}

// BlockedStatusSnapshot is complete only when the bounded authoritative read
// saw every nonclosed durable issue.
type BlockedStatusSnapshot struct {
	Observations []BlockedStatusObservation
	Complete     bool
	ProjectID    string
}

// BlockedStatusReader exposes the canonical persisted is_blocked projection
// together with the raw upstream lifecycle status.
type BlockedStatusReader interface {
	ReadBlockedStatusSnapshot(limit int) (BlockedStatusSnapshot, error)
}

// BlockedStatusConditionalWriter is the narrow same-observation mutation
// capability required by blocked-status reconciliation. It extends the normal
// revision-fenced writer rather than weakening that capability contract.
type BlockedStatusConditionalWriter interface {
	ConditionalWriter
	UpdateBlockedStatusIfMatch(
		observation BlockedStatusObservation,
		nextStatus string,
		metadataSet map[string]string,
		metadataUnset []string,
	) error
}

// BlockedStatusReaderFor resolves a wrapper-declared store target and returns
// its raw blocked-status reader when present.
func BlockedStatusReaderFor(store Store) (BlockedStatusReader, bool) {
	if store == nil {
		return nil, false
	}
	store = followConditionalWritesResolveTarget(store)
	reader, ok := store.(BlockedStatusReader)
	return reader, ok
}

// ResolveBlockedStatusConditionalWriter applies the existing conditional-write
// rollout policy, then requires the resolved writer to offer the narrower
// same-is_blocked-observation capability.
func ResolveBlockedStatusConditionalWriter(store Store) (BlockedStatusConditionalWriter, *BeadsDiagnostic, error) {
	writer, diagnostic, err := ResolveConditionalWriter(store)
	if err != nil || writer == nil {
		return nil, diagnostic, err
	}
	blockedWriter, ok := writer.(BlockedStatusConditionalWriter)
	if !ok {
		return nil, diagnostic, fmt.Errorf("blocked-status reconciliation: %w", ErrConditionalWriteUnsupported)
	}
	return blockedWriter, diagnostic, nil
}
