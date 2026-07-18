//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// installPID1ReaperIfNeeded makes gc reap re-parented orphan children when
// it is running as PID 1 (e.g. as a container ENTRYPOINT with no separate
// init shim). The kernel reparents every orphan in a PID namespace to
// whichever process is PID 1 in that namespace, but PID 1 gets no reaping
// behavior for free — something has to wait() those children or they
// accumulate as <defunct> zombies until the pids-cgroup ceiling blocks all
// further forking (#4308). gc's own exec sites already wait() on the
// children they spawn directly; this only covers descendants that
// reparent to gc because their original parent already exited.
func installPID1ReaperIfNeeded(stderr io.Writer) {
	if os.Getpid() != 1 {
		return
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGCHLD)
	go reapChildrenOnSIGCHLD(sigCh)
	fmt.Fprintln(stderr, "gc supervisor: running as PID 1, installed orphan-reaping SIGCHLD handler") //nolint:errcheck
}

// reapChildrenOnSIGCHLD drains every exited-but-unwaited child on each
// SIGCHLD notification, looping until reapAvailableChildren finds nothing
// left. Runs for the life of the process once installed.
func reapChildrenOnSIGCHLD(sigCh <-chan os.Signal) {
	for range sigCh {
		reapAvailableChildren()
	}
}

// reapAvailableChildren performs one non-blocking pass reaping every
// exited-but-unwaited child of this process, returning the count reaped.
// Split out from reapChildrenOnSIGCHLD so tests can exercise it without a
// real SIGCHLD delivery.
func reapAvailableChildren() int {
	reaped := 0
	for {
		pid, err := syscall.Wait4(-1, nil, syscall.WNOHANG, nil)
		if pid <= 0 || err != nil {
			break
		}
		reaped++
	}
	return reaped
}
