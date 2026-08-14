package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/worker"
)

// FencedExecutionApplicationError is a closed error returned by the controller.
type FencedExecutionApplicationError struct{ Code string }

func (e *FencedExecutionApplicationError) Error() string {
	return "fenced execution failed: " + e.Code
}

// IsFencedExecutionApplicationError reports whether err came from the controller's closed application envelope.
func IsFencedExecutionApplicationError(err error) bool {
	var target *FencedExecutionApplicationError
	return errors.As(err, &target)
}

// ResolveFencedExecution resolves or attaches to one durable fenced execution.
func (c *Client) ResolveFencedExecution(ctx context.Context, input worker.FencedExecutionInput) (runtime.FencedExecutionReceipt, error) {
	if err := c.requireCityScope(); err != nil {
		return runtime.FencedExecutionReceipt{}, err
	}
	resp, err := c.cw.PostV0CityByCityNameTemporalFencedExecutionsResolveWithResponse(ctx, c.cityName, nil, fencedExecutionInputToGen(input))
	if err != nil {
		return runtime.FencedExecutionReceipt{}, fencedExecutionClientCallError(err)
	}
	if resp == nil {
		return runtime.FencedExecutionReceipt{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(resp.StatusCode(), pdOf(resp)); err != nil {
		return runtime.FencedExecutionReceipt{}, err
	}
	if resp.JSON200 == nil {
		return runtime.FencedExecutionReceipt{}, fmt.Errorf("API returned %d with no body", resp.StatusCode())
	}
	if !resp.JSON200.Ok {
		return runtime.FencedExecutionReceipt{}, fencedExecutionApplicationError(resp.JSON200.Error)
	}
	if resp.JSON200.Receipt == nil {
		return runtime.FencedExecutionReceipt{}, fmt.Errorf("fenced execution resolve returned no receipt")
	}
	receipt, err := fencedExecutionReceiptFromGen(*resp.JSON200.Receipt)
	if err != nil {
		return runtime.FencedExecutionReceipt{}, err
	}
	if receipt.Target.SessionID != input.TargetSessionID {
		return runtime.FencedExecutionReceipt{}, fmt.Errorf("fenced execution resolve receipt target is invalid")
	}
	return receipt, nil
}

// ExecuteFencedExecution waits for one durable fenced execution's terminal result.
func (c *Client) ExecuteFencedExecution(ctx context.Context, input worker.FencedExecutionInput) (runtime.FencedExecutionResult, error) {
	if err := c.requireCityScope(); err != nil {
		return runtime.FencedExecutionResult{}, err
	}
	resp, err := c.cw.PostV0CityByCityNameTemporalFencedExecutionsExecuteWithResponse(ctx, c.cityName, nil, fencedExecutionInputToGen(input))
	if err != nil {
		return runtime.FencedExecutionResult{}, fencedExecutionClientCallError(err)
	}
	if resp == nil {
		return runtime.FencedExecutionResult{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(resp.StatusCode(), pdOf(resp)); err != nil {
		return runtime.FencedExecutionResult{}, err
	}
	if resp.JSON200 == nil {
		return runtime.FencedExecutionResult{}, fmt.Errorf("API returned %d with no body", resp.StatusCode())
	}
	var result runtime.FencedExecutionResult
	if resp.JSON200.Result != nil {
		var conversionErr error
		result, conversionErr = fencedExecutionResultFromGen(*resp.JSON200.Result)
		if conversionErr != nil {
			return runtime.FencedExecutionResult{}, conversionErr
		}
		if result.Receipt.Target.SessionID != input.TargetSessionID {
			return runtime.FencedExecutionResult{}, fmt.Errorf("fenced execution execute result target is invalid")
		}
	}
	if !resp.JSON200.Ok {
		return result, fencedExecutionApplicationError(resp.JSON200.Error)
	}
	if resp.JSON200.Result == nil {
		return runtime.FencedExecutionResult{}, fmt.Errorf("fenced execution execute returned no result")
	}
	return result, nil
}

// CancelFencedExecution durably revokes and stops one exact fenced execution.
func (c *Client) CancelFencedExecution(ctx context.Context, input worker.FencedExecutionCancelInput) (runtime.FencedExecutionStopReceipt, error) {
	if err := c.requireCityScope(); err != nil {
		return runtime.FencedExecutionStopReceipt{}, err
	}
	resp, err := c.cw.PostV0CityByCityNameTemporalFencedExecutionsCancelWithResponse(ctx, c.cityName, nil, fencedExecutionCancelInputToGen(input))
	if err != nil {
		return runtime.FencedExecutionStopReceipt{}, fencedExecutionClientCallError(err)
	}
	if resp == nil {
		return runtime.FencedExecutionStopReceipt{}, &connError{err: fmt.Errorf("nil response")}
	}
	if err := apiErrorFromResponse(resp.StatusCode(), pdOf(resp)); err != nil {
		return runtime.FencedExecutionStopReceipt{}, err
	}
	if resp.JSON200 == nil {
		return runtime.FencedExecutionStopReceipt{}, fmt.Errorf("API returned %d with no body", resp.StatusCode())
	}
	var receipt runtime.FencedExecutionStopReceipt
	if resp.JSON200.Receipt != nil {
		converted, conversionErr := fencedExecutionReceiptFromGen(resp.JSON200.Receipt.Receipt)
		if conversionErr != nil {
			return runtime.FencedExecutionStopReceipt{}, conversionErr
		}
		receipt = runtime.FencedExecutionStopReceipt{Receipt: converted, Revoked: resp.JSON200.Receipt.Revoked, Stopped: resp.JSON200.Receipt.Stopped}
		if receipt.Receipt.Target.SessionID != input.TargetSessionID || receipt.Receipt.ExecutionID != input.ExecutionID {
			return runtime.FencedExecutionStopReceipt{}, fmt.Errorf("fenced execution cancel receipt identity is invalid")
		}
	}
	if !resp.JSON200.Ok {
		return receipt, fencedExecutionApplicationError(resp.JSON200.Error)
	}
	if resp.JSON200.Receipt == nil {
		return runtime.FencedExecutionStopReceipt{}, fmt.Errorf("fenced execution cancel returned no receipt")
	}
	return receipt, nil
}

func fencedExecutionCancelInputToGen(input worker.FencedExecutionCancelInput) genclient.FencedExecutionCancelInput {
	return genclient.FencedExecutionCancelInput{
		BeadId: input.BeadID, CityId: input.CityID, ClaimToken: input.ClaimToken, ExecutionId: input.ExecutionID,
		FormulaRig: input.FormulaRig, Generation: input.Generation, TargetSessionId: input.TargetSessionID,
	}
}

func fencedExecutionInputToGen(input worker.FencedExecutionInput) genclient.FencedExecutionInput {
	return genclient.FencedExecutionInput{
		BeadId: input.BeadID, CityId: input.CityID, ClaimToken: input.ClaimToken,
		FormulaHash: input.FormulaHash, FormulaName: input.FormulaName, FormulaRig: input.FormulaRig,
		FormulaRootId: input.FormulaRootID, FormulaStepKey: input.FormulaStepKey, FormulaVersion: input.FormulaVersion,
		Generation: input.Generation, RunId: input.RunID, TargetSessionId: input.TargetSessionID,
	}
}

func fencedExecutionApplicationError(input *genclient.FencedExecutionError) error {
	if input == nil || input.Code == "" {
		return fmt.Errorf("fenced execution response is missing a closed error code")
	}
	return &FencedExecutionApplicationError{Code: input.Code}
}

func fencedExecutionClientCallError(err error) error {
	var syntax *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &typeError) {
		return fmt.Errorf("decode fenced execution response: %w", err)
	}
	return &connError{err: fmt.Errorf("request failed: %w", err)}
}

func fencedExecutionResultFromGen(input genclient.FencedExecutionResult) (runtime.FencedExecutionResult, error) {
	receipt, err := fencedExecutionReceiptFromGen(input.Receipt)
	if err != nil {
		return runtime.FencedExecutionResult{}, err
	}
	result := runtime.FencedExecutionResult{Receipt: receipt, Outcome: input.Outcome}
	if input.ArtifactRefs != nil {
		for _, artifact := range *input.ArtifactRefs {
			result.ArtifactRefs = append(result.ArtifactRefs, runtime.FencedExecutionArtifact{Kind: artifact.Kind, URI: artifact.Uri, SHA256: artifact.Sha256})
		}
	}
	if result.Outcome == "" {
		return runtime.FencedExecutionResult{}, fmt.Errorf("fenced execution result outcome is empty")
	}
	return result, nil
}

func fencedExecutionReceiptFromGen(input genclient.FencedExecutionReceipt) (runtime.FencedExecutionReceipt, error) {
	hash, hashErr := hex.DecodeString(input.RequestHash)
	if input.ExecutionId == "" || input.OperationId == "" || hashErr != nil || len(hash) != 32 || input.Process.Pid <= 1 || input.Process.ProcessGroupId <= 1 || input.Process.StartIdentity == "" {
		return runtime.FencedExecutionReceipt{}, fmt.Errorf("fenced execution receipt identity is invalid")
	}
	receipt := runtime.FencedExecutionReceipt{
		ExecutionID: input.ExecutionId, OperationID: input.OperationId, RequestHash: input.RequestHash, Attached: input.Attached,
		Target: runtime.FencedExecutionTarget{
			SessionID: input.Target.SessionId, SessionName: input.Target.SessionName, ConfiguredIdentity: input.Target.ConfiguredIdentity,
			Generation: input.Target.Generation, ContinuationEpoch: input.Target.ContinuationEpoch,
			InstanceToken: input.Target.InstanceToken, StartedConfigHash: input.Target.StartedConfigHash,
		},
		Process: runtime.FencedProcessIdentity{PID: int(input.Process.Pid), ProcessGroupID: int(input.Process.ProcessGroupId), StartIdentity: input.Process.StartIdentity},
	}
	if receipt.Target.SessionID == "" || receipt.Target.SessionName == "" || receipt.Target.ConfiguredIdentity == "" || receipt.Target.Generation == "" || receipt.Target.ContinuationEpoch == "" || receipt.Target.InstanceToken == "" || receipt.Target.StartedConfigHash == "" {
		return runtime.FencedExecutionReceipt{}, fmt.Errorf("fenced execution receipt target authority is invalid")
	}
	return receipt, nil
}
