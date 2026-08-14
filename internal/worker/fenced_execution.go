package worker

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// FencedExecutionBindingMetadataKey stores the secret-free durable execution binding.
const FencedExecutionBindingMetadataKey = "gc.temporal.fenced_execution"

var (
	// ErrFencedExecutionClaimConflict means the request conflicts with canonical work authority.
	ErrFencedExecutionClaimConflict = errors.New("fenced execution claim authority conflicts with canonical work")
	// ErrFencedExecutionAuthorityChanged means the configured session identity was replaced.
	ErrFencedExecutionAuthorityChanged = errors.New("fenced execution session authority changed")
)

// FencedExecutionInput carries canonical formula, work, claim, and target identity.
type FencedExecutionInput struct {
	CityID          string `json:"city_id"`
	RunID           string `json:"run_id"`
	BeadID          string `json:"bead_id"`
	Generation      int64  `json:"generation"`
	FormulaName     string `json:"formula_name"`
	FormulaHash     string `json:"formula_hash"`
	FormulaVersion  string `json:"formula_version"`
	FormulaRootID   string `json:"formula_root_id"`
	FormulaStepKey  string `json:"formula_step_key"`
	FormulaRig      string `json:"formula_rig"`
	ClaimToken      string `json:"claim_token"`
	TargetSessionID string `json:"target_session_id"`
}

// FencedExecutionCancelInput identifies one exact durable execution to revoke.
type FencedExecutionCancelInput struct {
	CityID          string `json:"city_id"`
	FormulaRig      string `json:"formula_rig"`
	BeadID          string `json:"bead_id"`
	Generation      int64  `json:"generation"`
	ClaimToken      string `json:"claim_token"`
	TargetSessionID string `json:"target_session_id"`
	ExecutionID     string `json:"execution_id"`
}

// FencedExecutionBinding is the secret-free CAS-persisted state machine record.
type FencedExecutionBinding struct {
	Version     int                            `json:"version"`
	State       string                         `json:"state"`
	OperationID string                         `json:"operation_id"`
	RequestHash string                         `json:"request_hash"`
	ClaimDigest string                         `json:"claim_digest"`
	Receipt     runtime.FencedExecutionReceipt `json:"receipt"`
	Result      *runtime.FencedExecutionResult `json:"result,omitempty"`
}

// FencedExecutionCoordinatorConfig supplies controller-owned stores and provider.
type FencedExecutionCoordinatorConfig struct {
	WorkStore             beads.Store
	SessionStore          beads.Store
	Provider              runtime.Provider
	ResolveSessionRuntime SessionRuntimeResolver
}

// FencedExecutionCoordinator owns durable execution authority and state transitions.
type FencedExecutionCoordinator struct {
	workStore      beads.Store
	sessionStore   beads.Store
	provider       runtime.Provider
	resolveRuntime SessionRuntimeResolver
}

// NewFencedExecutionCoordinator validates and constructs a coordinator.
func NewFencedExecutionCoordinator(cfg FencedExecutionCoordinatorConfig) (*FencedExecutionCoordinator, error) {
	if cfg.WorkStore == nil || cfg.SessionStore == nil || cfg.Provider == nil {
		return nil, fmt.Errorf("fenced execution coordinator requires work store, session store, and provider")
	}
	if _, ok := beads.MetadataCASWriterFor(cfg.WorkStore); !ok {
		return nil, fmt.Errorf("fenced execution coordinator requires metadata CAS: %w", beads.ErrConditionalWriteUnsupported)
	}
	return &FencedExecutionCoordinator{
		workStore: cfg.WorkStore, sessionStore: cfg.SessionStore, provider: cfg.Provider,
		resolveRuntime: cfg.ResolveSessionRuntime,
	}, nil
}

// Resolve starts or attaches to the request's stable operation and persists its receipt.
func (c *FencedExecutionCoordinator) Resolve(ctx context.Context, input FencedExecutionInput) (runtime.FencedExecutionReceipt, error) {
	prepared, err := c.prepare(input)
	if err != nil {
		return runtime.FencedExecutionReceipt{}, err
	}
	capability, err := runtime.RequireFencedExecutionProvider(c.provider)
	if err != nil {
		return runtime.FencedExecutionReceipt{}, err
	}
	if prepared.existing != nil {
		return prepared.existing.Receipt, nil
	}
	receipt, err := capability.StartOrAttachFenced(ctx, runtime.FencedExecutionRequest{
		OperationID: prepared.operationID, RequestHash: prepared.requestHash,
		Target: prepared.target, Config: prepared.config,
	})
	if err != nil {
		return runtime.FencedExecutionReceipt{}, err
	}
	binding := FencedExecutionBinding{
		Version: 1, State: "invoking", OperationID: prepared.operationID,
		RequestHash: prepared.requestHash, ClaimDigest: prepared.claimDigest, Receipt: receipt,
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return runtime.FencedExecutionReceipt{}, fmt.Errorf("encode fenced execution binding: %w", err)
	}
	outcome, err := beads.ApplyMetadataCAS(c.workStore, input.BeadID, FencedExecutionBindingMetadataKey, "", string(encoded))
	if err != nil {
		return runtime.FencedExecutionReceipt{}, errors.Join(runtime.ErrFencedExecutionUnknown, fmt.Errorf("persist fenced execution binding: %w", err))
	}
	if outcome == beads.MetadataCASSwapped || outcome == beads.MetadataCASAlreadyNext {
		return receipt, nil
	}
	// A contender may have persisted the exact provider-deduplicated receipt.
	prepared, err = c.prepare(input)
	if err != nil {
		return runtime.FencedExecutionReceipt{}, err
	}
	if prepared.existing == nil {
		return runtime.FencedExecutionReceipt{}, runtime.ErrFencedExecutionConflict
	}
	return prepared.existing.Receipt, nil
}

// Execute resolves the operation and durably records its terminal result.
func (c *FencedExecutionCoordinator) Execute(ctx context.Context, input FencedExecutionInput) (runtime.FencedExecutionResult, error) {
	receipt, err := c.Resolve(ctx, input)
	if err != nil {
		return runtime.FencedExecutionResult{}, err
	}
	prepared, err := c.prepare(input)
	if err != nil {
		return runtime.FencedExecutionResult{}, err
	}
	if prepared.existing != nil && prepared.existing.Result != nil {
		return *prepared.existing.Result, nil
	}
	if prepared.existing == nil {
		return runtime.FencedExecutionResult{}, runtime.ErrFencedExecutionUnknown
	}
	capability, err := runtime.RequireFencedExecutionProvider(c.provider)
	if err != nil {
		return runtime.FencedExecutionResult{}, err
	}
	result, err := capability.WaitFenced(ctx, receipt)
	if err != nil {
		return runtime.FencedExecutionResult{}, err
	}
	if err := c.persistBindingResult(input.BeadID, prepared.rawBinding, *prepared.existing, result); err != nil {
		return result, errors.Join(runtime.ErrFencedExecutionUnknown, err)
	}
	return result, nil
}

// Cancel durably revokes the binding before requesting an exact provider stop.
func (c *FencedExecutionCoordinator) Cancel(ctx context.Context, input FencedExecutionCancelInput) (runtime.FencedExecutionStopReceipt, error) {
	prepared, err := c.prepareCancel(input)
	if err != nil {
		return runtime.FencedExecutionStopReceipt{}, err
	}
	if prepared.binding.State == "revoked" {
		return runtime.FencedExecutionStopReceipt{Receipt: prepared.binding.Receipt, Revoked: true, Stopped: true}, nil
	}
	if prepared.binding.State != "revoking" {
		prepared, err = c.persistCancellationState(input, prepared, "revoking")
		if err != nil {
			return runtime.FencedExecutionStopReceipt{}, err
		}
	}
	capability, err := runtime.RequireFencedExecutionProvider(c.provider)
	if err != nil {
		return runtime.FencedExecutionStopReceipt{}, err
	}
	stopped, err := capability.RevokeAndStopFenced(ctx, runtime.FencedExecutionStopRequest{Receipt: prepared.binding.Receipt})
	if err != nil {
		return stopped, err
	}
	if _, err := c.persistCancellationState(input, prepared, "revoked"); err != nil {
		return stopped, err
	}
	return stopped, nil
}

func (c *FencedExecutionCoordinator) persistCancellationState(input FencedExecutionCancelInput, prepared preparedFencedCancellation, state string) (preparedFencedCancellation, error) {
	next := prepared.binding
	next.State = state
	encoded, err := json.Marshal(next)
	if err != nil {
		return preparedFencedCancellation{}, errors.Join(runtime.ErrFencedExecutionUnknown, err)
	}
	outcome, err := beads.ApplyMetadataCAS(c.workStore, input.BeadID, FencedExecutionBindingMetadataKey, prepared.rawBinding, string(encoded))
	if err != nil {
		return preparedFencedCancellation{}, errors.Join(runtime.ErrFencedExecutionUnknown, fmt.Errorf("persist fenced execution %s: %w", state, err))
	}
	if outcome == beads.MetadataCASConflict {
		current, prepareErr := c.prepareCancel(input)
		if prepareErr == nil && (current.binding.State == state || state == "revoking" && current.binding.State == "revoked") {
			return current, nil
		}
		return preparedFencedCancellation{}, errors.Join(runtime.ErrFencedExecutionUnknown, fmt.Errorf("persist fenced execution %s: %w", state, runtime.ErrFencedExecutionConflict))
	}
	return preparedFencedCancellation{rawBinding: string(encoded), binding: next}, nil
}

type preparedFencedCancellation struct {
	rawBinding string
	binding    FencedExecutionBinding
}

func (c *FencedExecutionCoordinator) prepareCancel(input FencedExecutionCancelInput) (preparedFencedCancellation, error) {
	if input.Generation <= 0 || input.ClaimToken == "" || strings.TrimSpace(input.CityID) == "" || strings.TrimSpace(input.FormulaRig) == "" || strings.TrimSpace(input.BeadID) == "" || strings.TrimSpace(input.TargetSessionID) == "" || strings.TrimSpace(input.ExecutionID) == "" {
		return preparedFencedCancellation{}, fmt.Errorf("fenced execution cancellation identity is incomplete")
	}
	work, err := c.workStore.Get(input.BeadID)
	if err != nil {
		return preparedFencedCancellation{}, fmt.Errorf("load fenced execution work: %w", err)
	}
	canonicalClaim := work.Metadata["gc.temporal.claim_token"]
	if work.Metadata["gc.temporal.generation"] != strconv.FormatInt(input.Generation, 10) || canonicalClaim == "" || subtle.ConstantTimeCompare([]byte(canonicalClaim), []byte(input.ClaimToken)) != 1 {
		return preparedFencedCancellation{}, ErrFencedExecutionClaimConflict
	}
	raw := work.Metadata[FencedExecutionBindingMetadataKey]
	if raw == "" {
		return preparedFencedCancellation{}, runtime.ErrFencedExecutionUnknown
	}
	var binding FencedExecutionBinding
	if err := json.Unmarshal([]byte(raw), &binding); err != nil {
		return preparedFencedCancellation{}, fmt.Errorf("decode fenced execution binding: %w", err)
	}
	if binding.ClaimDigest != sha256Hex(input.ClaimToken) || binding.Receipt.ExecutionID != input.ExecutionID || binding.Receipt.Target.SessionID != input.TargetSessionID {
		return preparedFencedCancellation{}, runtime.ErrFencedExecutionStaleAuthority
	}
	return preparedFencedCancellation{rawBinding: raw, binding: binding}, nil
}

type preparedFencedExecution struct {
	operationID string
	requestHash string
	claimDigest string
	target      runtime.FencedExecutionTarget
	config      runtime.Config
	rawBinding  string
	existing    *FencedExecutionBinding
}

func (c *FencedExecutionCoordinator) prepare(input FencedExecutionInput) (preparedFencedExecution, error) {
	if err := validateFencedExecutionInput(input); err != nil {
		return preparedFencedExecution{}, err
	}
	work, err := c.workStore.Get(input.BeadID)
	if err != nil {
		return preparedFencedExecution{}, fmt.Errorf("load fenced execution work: %w", err)
	}
	if work.Metadata["gc.temporal.generation"] != strconv.FormatInt(input.Generation, 10) ||
		work.Metadata["gc.root_bead_id"] != input.FormulaRootID || work.Metadata["gc.step_ref"] != input.FormulaStepKey {
		return preparedFencedExecution{}, ErrFencedExecutionClaimConflict
	}
	canonicalClaim := work.Metadata["gc.temporal.claim_token"]
	if canonicalClaim == "" || subtle.ConstantTimeCompare([]byte(canonicalClaim), []byte(input.ClaimToken)) != 1 {
		return preparedFencedExecution{}, ErrFencedExecutionClaimConflict
	}
	root, err := c.workStore.Get(input.FormulaRootID)
	if err != nil {
		return preparedFencedExecution{}, fmt.Errorf("load fenced execution formula root: %w", err)
	}
	if root.Metadata["gc.formula_name"] != input.FormulaName || root.Metadata["gc.formula_hash"] != input.FormulaHash ||
		root.Metadata["gc.formula_contract"] != "graph.v2" {
		return preparedFencedExecution{}, ErrFencedExecutionClaimConflict
	}
	info, persisted, err := sessionpkg.ResolveSessionRecordByExactID(c.sessionStore, input.TargetSessionID)
	if err != nil {
		return preparedFencedExecution{}, err
	}
	target := runtime.FencedExecutionTarget{
		SessionID: info.ID, SessionName: strings.TrimSpace(info.SessionName), ConfiguredIdentity: strings.TrimSpace(info.ConfiguredNamedIdentity),
		Generation: strings.TrimSpace(info.Generation), ContinuationEpoch: strings.TrimSpace(info.ContinuationEpoch),
		InstanceToken: strings.TrimSpace(info.InstanceToken), StartedConfigHash: strings.TrimSpace(info.StartedConfigHash),
	}
	if target.SessionName == "" || target.ConfiguredIdentity == "" || target.Generation == "" || target.ContinuationEpoch == "" ||
		target.InstanceToken == "" || target.StartedConfigHash == "" {
		return preparedFencedExecution{}, fmt.Errorf("fenced execution target authority is incomplete")
	}
	config, err := c.resolvedExecutionConfig(info, persisted)
	if err != nil {
		return preparedFencedExecution{}, err
	}
	claimDigest := sha256Hex(input.ClaimToken)
	operationID := fencedOperationID(input)
	requestHash, err := fencedRequestHash(input, claimDigest, target)
	if err != nil {
		return preparedFencedExecution{}, err
	}
	prepared := preparedFencedExecution{
		operationID: operationID, requestHash: requestHash, claimDigest: claimDigest,
		target: target, config: config, rawBinding: work.Metadata[FencedExecutionBindingMetadataKey],
	}
	if prepared.rawBinding != "" {
		var binding FencedExecutionBinding
		if err := json.Unmarshal([]byte(prepared.rawBinding), &binding); err != nil {
			return preparedFencedExecution{}, fmt.Errorf("decode fenced execution binding: %w", err)
		}
		if binding.Receipt.Target != target {
			return preparedFencedExecution{}, ErrFencedExecutionAuthorityChanged
		}
		if binding.RequestHash != requestHash || binding.OperationID != operationID || binding.ClaimDigest != claimDigest {
			return preparedFencedExecution{}, runtime.ErrFencedExecutionConflict
		}
		if binding.State == "revoked" || binding.State == "revoking" {
			return preparedFencedExecution{}, runtime.ErrFencedExecutionRevoked
		}
		prepared.existing = &binding
	}
	return prepared, nil
}

func (c *FencedExecutionCoordinator) resolvedExecutionConfig(info sessionpkg.Info, persisted sessionpkg.PersistedResponse) (runtime.Config, error) {
	spec := SessionSpec{ID: info.ID, Template: info.Template, Command: info.Command, WorkDir: info.WorkDir, Provider: info.Provider}
	if c.resolveRuntime != nil {
		resolved, err := c.resolveRuntime(info, strings.TrimSpace(persisted.Metadata["real_world_app_session_kind"]), cloneStringMap(persisted.Metadata))
		if err != nil {
			return runtime.Config{}, err
		}
		applyResolvedRuntimeToSessionSpec(&spec, resolved)
	}
	config := cloneRuntimeConfig(spec.Hints)
	config.Command = strings.TrimSpace(spec.Command)
	config.WorkDir = strings.TrimSpace(spec.WorkDir)
	config.Env = mergeStringMaps(config.Env, spec.Env)
	config.Lifecycle = runtime.LifecycleOneShot
	if config.Command == "" || config.WorkDir == "" {
		return runtime.Config{}, fmt.Errorf("fenced execution target runtime is incomplete")
	}
	return config, nil
}

func (c *FencedExecutionCoordinator) persistBindingResult(beadID, expected string, binding FencedExecutionBinding, result runtime.FencedExecutionResult) error {
	binding.State = "terminal"
	binding.Result = &result
	encoded, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	outcome, err := beads.ApplyMetadataCAS(c.workStore, beadID, FencedExecutionBindingMetadataKey, expected, string(encoded))
	if err != nil {
		return fmt.Errorf("persist fenced execution result: %w", err)
	}
	if outcome == beads.MetadataCASConflict {
		return fmt.Errorf("persist fenced execution result: %w", runtime.ErrFencedExecutionConflict)
	}
	return nil
}

func validateFencedExecutionInput(input FencedExecutionInput) error {
	if input.Generation <= 0 || input.ClaimToken == "" {
		return fmt.Errorf("fenced execution generation and claim are required")
	}
	for name, value := range map[string]string{
		"city": input.CityID, "run": input.RunID, "bead": input.BeadID, "formula name": input.FormulaName,
		"formula hash": input.FormulaHash, "formula version": input.FormulaVersion, "formula root": input.FormulaRootID,
		"formula step": input.FormulaStepKey, "formula rig": input.FormulaRig, "target session": input.TargetSessionID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("fenced execution %s is required", name)
		}
	}
	if len(input.FormulaHash) != 64 {
		return fmt.Errorf("fenced execution formula hash is invalid")
	}
	return nil
}

func fencedOperationID(input FencedExecutionInput) string {
	canonical := strings.Join([]string{input.CityID, input.RunID, input.FormulaRootID, input.FormulaStepKey, input.BeadID, strconv.FormatInt(input.Generation, 10)}, "\x00")
	return "formula-" + sha256Hex(canonical)[:32]
}

func fencedRequestHash(input FencedExecutionInput, claimDigest string, target runtime.FencedExecutionTarget) (string, error) {
	canonical := struct {
		CityID, RunID, BeadID                                                               string
		Generation                                                                          int64
		FormulaName, FormulaHash, FormulaVersion, FormulaRootID, FormulaStepKey, FormulaRig string
		ClaimDigest                                                                         string
		Target                                                                              runtime.FencedExecutionTarget
	}{
		input.CityID, input.RunID, input.BeadID, input.Generation,
		input.FormulaName, input.FormulaHash, input.FormulaVersion, input.FormulaRootID, input.FormulaStepKey, input.FormulaRig,
		claimDigest, target,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return sha256Hex(string(encoded)), nil
}

func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
