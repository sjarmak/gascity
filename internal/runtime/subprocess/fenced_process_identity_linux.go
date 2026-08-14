//go:build linux

package subprocess

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/gastownhall/gascity/internal/runtime"
)

func revokeRecoveredFencedExecution(receipt runtime.FencedExecutionReceipt) (runtime.FencedExecutionStopReceipt, error) {
	start, err := fencedProcessStartIdentity(receipt.Process.PID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runtime.FencedExecutionStopReceipt{Receipt: receipt, Revoked: true, Stopped: true}, nil
		}
		return runtime.FencedExecutionStopReceipt{}, errors.Join(runtime.ErrFencedExecutionUnknown, err)
	}
	if start != receipt.Process.StartIdentity || receipt.Process.ProcessGroupID != receipt.Process.PID {
		return runtime.FencedExecutionStopReceipt{}, runtime.ErrFencedExecutionStaleAuthority
	}
	if err := syscall.Kill(-receipt.Process.ProcessGroupID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return runtime.FencedExecutionStopReceipt{Receipt: receipt, Revoked: true}, err
	}
	return runtime.FencedExecutionStopReceipt{Receipt: receipt, Revoked: true, Stopped: true}, nil
}

func fencedProcessStartIdentity(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	text := string(data)
	closeParen := strings.LastIndex(text, ")")
	if closeParen < 0 {
		return "", fmt.Errorf("malformed process stat")
	}
	fields := strings.Fields(text[closeParen+1:])
	// starttime is field 22 of proc_pid_stat; fields begins at field 3.
	if len(fields) <= 19 || fields[19] == "" {
		return "", fmt.Errorf("malformed process stat")
	}
	return fields[19], nil
}
