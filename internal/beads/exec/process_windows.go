//go:build windows

package exec

import osexec "os/exec"

func prepareCommandForTimeout(_ *osexec.Cmd) {}

func killCommandTree(cmd *osexec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
