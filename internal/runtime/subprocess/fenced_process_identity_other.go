//go:build !linux

package subprocess

import "github.com/gastownhall/gascity/internal/runtime"

func fencedProcessStartIdentity(pid int) (string, error) {
	return "", runtime.ErrFencedExecutionUnsupported
}

func revokeRecoveredFencedExecution(runtime.FencedExecutionReceipt) (runtime.FencedExecutionStopReceipt, error) {
	return runtime.FencedExecutionStopReceipt{}, runtime.ErrFencedExecutionUnsupported
}
