// Package systemdscope provides shared support for transient systemd user
// scopes.
package systemdscope

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Probe verifies that systemd-run exists and the systemd user manager can run
// a no-op command in a transient scope on slice.
func Probe(slice string, timeout time.Duration) error {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return fmt.Errorf("systemd-run not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemd-run",
		"--user", "--scope", "--slice="+slice, "--collect", "--quiet", "--", "true")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("systemd user manager probe failed: %w: %s", err, msg)
		}
		return fmt.Errorf("systemd user manager probe failed: %w", err)
	}
	return nil
}
