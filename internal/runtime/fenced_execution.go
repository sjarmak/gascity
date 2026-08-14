package runtime

import (
	"context"
	"errors"
)

var (
	// ErrFencedExecutionUnsupported means the selected provider lacks the optional capability.
	ErrFencedExecutionUnsupported = errors.New("fenced execution is unsupported")
	// ErrFencedExecutionConflict means an operation ID was reused for different content.
	ErrFencedExecutionConflict = errors.New("fenced execution request conflicts with the durable binding")
	// ErrFencedExecutionStaleAuthority means the exact target or process identity no longer matches.
	ErrFencedExecutionStaleAuthority = errors.New("fenced execution authority is stale")
	// ErrFencedExecutionRevoked means durable cancellation forbids attachment or execution.
	ErrFencedExecutionRevoked = errors.New("fenced execution is revoked")
	// ErrFencedExecutionUnknown means the external effect cannot be classified safely.
	ErrFencedExecutionUnknown = errors.New("fenced execution effect is unknown")
)

// FencedExecutionTarget is the controller-owned authority tuple for the
// configured session whose resolved runtime is used by a formula execution.
// Claim capabilities are deliberately absent: the controller validates the raw
// capability at its request boundary and binds only its digest into RequestHash.
type FencedExecutionTarget struct {
	SessionID          string `json:"session_id"`
	SessionName        string `json:"session_name"`
	ConfiguredIdentity string `json:"configured_identity"`
	Generation         string `json:"generation"`
	ContinuationEpoch  string `json:"continuation_epoch"`
	InstanceToken      string `json:"instance_token"`
	StartedConfigHash  string `json:"started_config_hash"`
}

// FencedProcessIdentity identifies one exact controller-owned process start.
type FencedProcessIdentity struct {
	PID            int    `json:"pid"`
	ProcessGroupID int    `json:"process_group_id"`
	StartIdentity  string `json:"start_identity"`
}

// FencedExecutionRequest is the provider-facing start-or-attach request. The
// controller has already validated work/session authority and computed the
// stable RequestHash before this value crosses the optional provider seam.
type FencedExecutionRequest struct {
	OperationID string                `json:"operation_id"`
	RequestHash string                `json:"request_hash"`
	Target      FencedExecutionTarget `json:"target"`
	Config      Config                `json:"-"`
}

// FencedExecutionReceipt is a replayable exact-start receipt.
type FencedExecutionReceipt struct {
	ExecutionID string                `json:"execution_id"`
	OperationID string                `json:"operation_id"`
	RequestHash string                `json:"request_hash"`
	Target      FencedExecutionTarget `json:"target"`
	Process     FencedProcessIdentity `json:"process"`
	Attached    bool                  `json:"attached"`
}

// FencedExecutionArtifact is a compact reference emitted by the child result
// protocol. It never carries artifact contents.
type FencedExecutionArtifact struct {
	Kind   string `json:"kind"`
	URI    string `json:"uri"`
	SHA256 string `json:"sha256"`
}

// FencedExecutionResult is the closed terminal result channel.
type FencedExecutionResult struct {
	Receipt      FencedExecutionReceipt    `json:"receipt"`
	Outcome      string                    `json:"outcome"`
	ArtifactRefs []FencedExecutionArtifact `json:"artifact_refs,omitempty"`
}

// FencedExecutionStopRequest identifies the exact execution to revoke and stop.
type FencedExecutionStopRequest struct {
	Receipt FencedExecutionReceipt `json:"receipt"`
}

// FencedExecutionStopReceipt proves revocation and exact stop completion.
type FencedExecutionStopReceipt struct {
	Receipt FencedExecutionReceipt `json:"receipt"`
	Revoked bool                   `json:"revoked"`
	Stopped bool                   `json:"stopped"`
}

// FencedExecutionProvider is an optional capability alongside Provider. A
// controller must fail closed with ErrFencedExecutionUnsupported when its
// selected provider does not implement this interface.
type FencedExecutionProvider interface {
	StartOrAttachFenced(context.Context, FencedExecutionRequest) (FencedExecutionReceipt, error)
	WaitFenced(context.Context, FencedExecutionReceipt) (FencedExecutionResult, error)
	RevokeAndStopFenced(context.Context, FencedExecutionStopRequest) (FencedExecutionStopReceipt, error)
}

// RequireFencedExecutionProvider narrows a generic provider without changing
// Provider's long-standing signature.
func RequireFencedExecutionProvider(provider Provider) (FencedExecutionProvider, error) {
	capability, ok := provider.(FencedExecutionProvider)
	if !ok {
		return nil, ErrFencedExecutionUnsupported
	}
	return capability, nil
}
