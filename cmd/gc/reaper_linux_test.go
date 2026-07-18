//go:build linux

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestInstallPID1ReaperIfNeeded_NoOpWhenNotPID1(t *testing.T) {
	// go test never runs as PID 1, so this exercises the early-return
	// guard without needing an actual PID-1 process.
	var stderr bytes.Buffer
	installPID1ReaperIfNeeded(&stderr)
	if stderr.Len() != 0 {
		t.Fatalf("installPID1ReaperIfNeeded wrote output when not PID 1: %q", stderr.String())
	}
}

func TestReapAvailableChildren_ReapsExitedOrphan(t *testing.T) {
	cmd := exec.Command("/bin/true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}
	pid := cmd.Process.Pid

	// Poll instead of a fixed sleep: the child exits almost immediately,
	// but scheduling under load can delay it becoming reapable.
	deadline := time.Now().Add(2 * time.Second)
	reaped := 0
	for time.Now().Before(deadline) {
		reaped += reapAvailableChildren()
		if reaped > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reaped == 0 {
		t.Fatalf("reapAvailableChildren never reaped child pid %d within timeout", pid)
	}

	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("kill(%d, 0) = %v, want ESRCH (pid should be fully reaped)", pid, err)
	}
}

func TestReapChildrenOnSIGCHLD_DrainsOnSignal(t *testing.T) {
	cmd := exec.Command("/bin/true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}
	pid := cmd.Process.Pid

	// Give the child a moment to exit before signaling, mirroring a real
	// SIGCHLD delivery arriving after the child has already exited.
	time.Sleep(50 * time.Millisecond)

	sigCh := make(chan os.Signal, 1)
	sigCh <- syscall.SIGCHLD
	close(sigCh)

	done := make(chan struct{})
	go func() {
		reapChildrenOnSIGCHLD(sigCh)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reapChildrenOnSIGCHLD did not return after signal channel closed")
	}

	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("kill(%d, 0) = %v, want ESRCH (pid should be fully reaped)", pid, err)
	}
}
