package api

import (
	"context"
	"errors"

	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/worker"
)

func (s *Server) fencedExecutionCoordinator(input worker.FencedExecutionInput) (*worker.FencedExecutionCoordinator, error) {
	if input.CityID != s.state.CityName() {
		return nil, worker.ErrFencedExecutionClaimConflict
	}
	workStore := s.state.BeadStore(input.FormulaRig)
	if workStore == nil {
		workStore = s.state.CityBeadStore()
	}
	factory, err := s.workerFactory(s.state.SessionsBeadStore().Store)
	if err != nil {
		return nil, err
	}
	return factory.FencedExecutionCoordinator(workStore)
}

func (s *Server) humaHandleFencedExecutionResolve(ctx context.Context, input *FencedExecutionResolveInput) (*FencedExecutionResolveOutput, error) {
	output := &FencedExecutionResolveOutput{}
	coordinator, err := s.fencedExecutionCoordinator(input.Body)
	if err == nil {
		var receipt runtime.FencedExecutionReceipt
		receipt, err = coordinator.Resolve(ctx, input.Body)
		if err == nil {
			output.Body.OK = true
			output.Body.Receipt = &receipt
			return output, nil
		}
	}
	output.Body.Error = &FencedExecutionError{Code: fencedExecutionErrorCode(err)}
	return output, nil
}

func (s *Server) humaHandleFencedExecutionExecute(ctx context.Context, input *FencedExecutionExecuteInput) (*FencedExecutionExecuteOutput, error) {
	output := &FencedExecutionExecuteOutput{}
	coordinator, err := s.fencedExecutionCoordinator(input.Body)
	if err == nil {
		var result runtime.FencedExecutionResult
		result, err = coordinator.Execute(ctx, input.Body)
		if result.Receipt.ExecutionID != "" {
			output.Body.Result = &result
		}
		if err == nil {
			output.Body.OK = true
			return output, nil
		}
	}
	output.Body.Error = &FencedExecutionError{Code: fencedExecutionErrorCode(err)}
	return output, nil
}

func (s *Server) humaHandleFencedExecutionCancel(ctx context.Context, input *FencedExecutionCancelInput) (*FencedExecutionCancelOutput, error) {
	output := &FencedExecutionCancelOutput{}
	coordinator, err := s.fencedExecutionCoordinator(worker.FencedExecutionInput{CityID: input.Body.CityID, FormulaRig: input.Body.FormulaRig, TargetSessionID: input.Body.TargetSessionID})
	if err == nil {
		var receipt runtime.FencedExecutionStopReceipt
		receipt, err = coordinator.Cancel(ctx, input.Body)
		if receipt.Receipt.ExecutionID != "" {
			output.Body.Receipt = &receipt
		}
		if err == nil {
			output.Body.OK = true
			return output, nil
		}
	}
	output.Body.Error = &FencedExecutionError{Code: fencedExecutionErrorCode(err)}
	return output, nil
}

func fencedExecutionErrorCode(err error) string {
	switch {
	case errors.Is(err, runtime.ErrFencedExecutionUnsupported):
		return "unsupported"
	case errors.Is(err, runtime.ErrFencedExecutionConflict):
		return "conflict"
	case errors.Is(err, runtime.ErrFencedExecutionStaleAuthority), errors.Is(err, worker.ErrFencedExecutionAuthorityChanged):
		return "stale_authority"
	case errors.Is(err, worker.ErrFencedExecutionClaimConflict):
		return "claim_conflict"
	case errors.Is(err, runtime.ErrFencedExecutionRevoked):
		return "revoked"
	case errors.Is(err, runtime.ErrFencedExecutionUnknown):
		return "unknown_external_state"
	default:
		return "internal"
	}
}
