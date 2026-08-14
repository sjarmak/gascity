package api

import (
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/worker"
)

// FencedExecutionResolveInput is the typed resolve request.
type FencedExecutionResolveInput struct {
	CityScope
	Body worker.FencedExecutionInput
}

// FencedExecutionExecuteInput is the typed execute request.
type FencedExecutionExecuteInput struct {
	CityScope
	Body worker.FencedExecutionInput
}

// FencedExecutionCancelInput is the typed cancellation request.
type FencedExecutionCancelInput struct {
	CityScope
	Body worker.FencedExecutionCancelInput
}

// FencedExecutionError carries a closed, content-free error code.
type FencedExecutionError struct {
	Code string `json:"code"`
}

// FencedExecutionResolveOutput is the typed resolve envelope.
type FencedExecutionResolveOutput struct {
	Body struct {
		OK      bool                            `json:"ok"`
		Receipt *runtime.FencedExecutionReceipt `json:"receipt,omitempty"`
		Error   *FencedExecutionError           `json:"error,omitempty"`
	}
}

// FencedExecutionExecuteOutput is the typed terminal-result envelope.
type FencedExecutionExecuteOutput struct {
	Body struct {
		OK     bool                           `json:"ok"`
		Result *runtime.FencedExecutionResult `json:"result,omitempty"`
		Error  *FencedExecutionError          `json:"error,omitempty"`
	}
}

// FencedExecutionCancelOutput is the typed cancellation envelope.
type FencedExecutionCancelOutput struct {
	Body struct {
		OK      bool                                `json:"ok"`
		Receipt *runtime.FencedExecutionStopReceipt `json:"receipt,omitempty"`
		Error   *FencedExecutionError               `json:"error,omitempty"`
	}
}
