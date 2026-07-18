//go:build !linux

package main

import "io"

// installPID1ReaperIfNeeded is a no-op on non-Linux platforms: gc's
// supported non-Linux deployments (macOS launchd, Windows service) never
// run as PID 1, and the reparent-to-PID1 zombie-accumulation behavior this
// guards against (#4308) is Linux PID-namespace semantics.
func installPID1ReaperIfNeeded(_ io.Writer) {}
