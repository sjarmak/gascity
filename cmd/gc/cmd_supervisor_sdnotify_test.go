package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/supervisor"
)

// TestRunSupervisorEmitsSdNotifyLifecycle verifies that when
// NOTIFY_SOCKET is set (systemd Type=notify), runSupervisor emits
// READY=1 once the control socket and API are up, WATCHDOG=1 on each
// healthy reconcile cycle, RELOADING=1 followed by READY=1 around a
// reload-triggered reconcile, and STOPPING=1 when shutdown begins.
func TestRunSupervisorEmitsSdNotifyLifecycle(t *testing.T) {
	gcHome := shortTempDir(t, "gc-home-")
	runtimeDir := shortTempDir(t, "gc-run-")
	t.Setenv("GC_HOME", gcHome)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	notifyDir := shortTempDir(t, "gc-sdn-")
	notifyPath := filepath.Join(notifyDir, "notify.sock")
	notifyConn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: notifyPath, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listening on notify socket: %v", err)
	}
	defer notifyConn.Close() //nolint:errcheck
	t.Setenv("NOTIFY_SOCKET", notifyPath)

	cfg := []byte("[supervisor]\nport = " + freeLoopbackPort(t) + "\npatrol_interval = \"100ms\"\n")
	if err := os.WriteFile(supervisor.ConfigPath(), cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	sigChReady := make(chan chan<- os.Signal, 1)
	oldSignalNotify := supervisorSignalNotify
	supervisorSignalNotify = func(c chan<- os.Signal, _ ...os.Signal) {
		sigChReady <- c
	}
	t.Cleanup(func() {
		supervisorSignalNotify = oldSignalNotify
	})

	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() {
		done <- runSupervisor(&stdout, &stderr)
	}()

	var sigCh chan<- os.Signal
	awaitCond(t, func() bool {
		select {
		case sigCh = <-sigChReady:
			return true
		default:
			return false
		}
	}, "supervisor signal hook registering")

	// readDatagram must not gate on a fixed wall-clock deadline: on a
	// contended host the supervisor's reconcile loop can be descheduled long
	// enough for a short deadline to fire even though the notification is
	// still coming (gc-v8vlo). The read runs in its own goroutine so the test
	// can poll for it with the shared hang budget instead of racing
	// SetReadDeadline against host load.
	readDatagram := func() string {
		t.Helper()
		type result struct {
			s   string
			err error
		}
		resCh := make(chan result, 1)
		go func() {
			buf := make([]byte, 256)
			n, err := notifyConn.Read(buf)
			resCh <- result{s: string(buf[:n]), err: err}
		}()
		var res result
		awaitCond(t, func() bool {
			select {
			case res = <-resCh:
				return true
			default:
				return false
			}
		}, "notify datagram")
		if res.err != nil {
			t.Fatalf("reading notify datagram: %v; stdout=%q stderr=%q", res.err, stdout.String(), stderr.String())
		}
		return res.s
	}

	if got := readDatagram(); got != "READY=1" {
		t.Fatalf("first datagram = %q, want READY=1", got)
	}
	if got := readDatagram(); got != "WATCHDOG=1" {
		t.Fatalf("second datagram = %q, want WATCHDOG=1 from initial reconcile", got)
	}

	sigCh <- syscall.SIGHUP

	// Ticker reconciles may interleave WATCHDOG=1 datagrams before the
	// reload branch runs; drain until RELOADING=1 arrives. The outer bound
	// is the shared hang budget, not a short fixed literal: each
	// readDatagram call already backstops itself, so this only needs to
	// catch a genuine "RELOADING=1 never arrives" wedge, not host jitter.
	reloadDeadline := time.Now().Add(hangBudget)
	var reload []string
	for time.Now().Before(reloadDeadline) {
		got := readDatagram()
		reload = append(reload, got)
		if got == "RELOADING=1" {
			break
		}
		if got != "WATCHDOG=1" {
			t.Fatalf("unexpected datagram %q while waiting for RELOADING=1 (saw %v)", got, reload)
		}
	}
	if len(reload) == 0 || reload[len(reload)-1] != "RELOADING=1" {
		t.Fatalf("never received RELOADING=1 after SIGHUP; saw %v; stdout=%q stderr=%q", reload, stdout.String(), stderr.String())
	}
	// All notify sends happen on the single supervisor loop goroutine,
	// so the reload's own reconcile pet and its READY=1 completion
	// follow RELOADING=1 with no ticker interleave.
	if got := readDatagram(); got != "WATCHDOG=1" {
		t.Fatalf("datagram after RELOADING=1 = %q, want WATCHDOG=1 from reload reconcile", got)
	}
	if got := readDatagram(); got != "READY=1" {
		t.Fatalf("datagram after reload reconcile = %q, want READY=1 reload completion", got)
	}

	sigCh <- syscall.SIGTERM

	// Ticker reconciles may interleave more WATCHDOG=1 datagrams before
	// the shutdown branch runs; drain until STOPPING=1 arrives. Same
	// hang-budget rationale as the reload drain above.
	deadline := time.Now().Add(hangBudget)
	var lifecycle []string
	for time.Now().Before(deadline) {
		got := readDatagram()
		lifecycle = append(lifecycle, got)
		if got == "STOPPING=1" {
			break
		}
		if got != "WATCHDOG=1" {
			t.Fatalf("unexpected datagram %q while waiting for STOPPING=1 (saw %v)", got, lifecycle)
		}
	}
	if len(lifecycle) == 0 || lifecycle[len(lifecycle)-1] != "STOPPING=1" {
		t.Fatalf("never received STOPPING=1 after SIGTERM; saw %v; stdout=%q stderr=%q", lifecycle, stdout.String(), stderr.String())
	}

	var code int
	awaitCond(t, func() bool {
		select {
		case code = <-done:
			return true
		default:
			return false
		}
	}, "runSupervisor exiting after SIGTERM")
	if code != 0 {
		t.Fatalf("runSupervisor code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Supervisor stopped.") {
		t.Fatalf("stdout = %q, want supervisor stop message", stdout.String())
	}
}

// TestRunSupervisorNoNotifySocketIsSilentNoop verifies the supervisor
// runs cleanly with NOTIFY_SOCKET unset: no sd_notify errors on stderr.
func TestRunSupervisorNoNotifySocketIsSilentNoop(t *testing.T) {
	gcHome := shortTempDir(t, "gc-home-")
	runtimeDir := shortTempDir(t, "gc-run-")
	t.Setenv("GC_HOME", gcHome)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("NOTIFY_SOCKET", "")

	cfg := []byte("[supervisor]\nport = " + freeLoopbackPort(t) + "\npatrol_interval = \"10m\"\n")
	if err := os.WriteFile(supervisor.ConfigPath(), cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	sigChReady := make(chan chan<- os.Signal, 1)
	oldSignalNotify := supervisorSignalNotify
	supervisorSignalNotify = func(c chan<- os.Signal, _ ...os.Signal) {
		sigChReady <- c
	}
	t.Cleanup(func() {
		supervisorSignalNotify = oldSignalNotify
	})

	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() {
		done <- runSupervisor(&stdout, &stderr)
	}()

	var sigCh chan<- os.Signal
	awaitCond(t, func() bool {
		select {
		case sigCh = <-sigChReady:
			return true
		default:
			return false
		}
	}, "supervisor signal hook registering")
	// Best effort: give the supervisor a chance to log its start line before
	// sending SIGTERM, but don't fail the test over it — the shutdown path
	// below is the real assertion either way. The wait is a hang detector
	// (shared budget), not a latency assertion, so it never truncates a slow
	// but healthy start under host load.
	waitDeadline := time.Now().Add(hangBudget)
	for time.Now().Before(waitDeadline) && !strings.Contains(stdout.String(), "Supervisor started.") {
		time.Sleep(10 * time.Millisecond)
	}
	sigCh <- syscall.SIGTERM

	var code int
	awaitCond(t, func() bool {
		select {
		case code = <-done:
			return true
		default:
			return false
		}
	}, "runSupervisor exiting after SIGTERM")
	if code != 0 {
		t.Fatalf("runSupervisor code = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "sd_notify") {
		t.Fatalf("stderr = %q, want no sd_notify noise when NOTIFY_SOCKET is unset", stderr.String())
	}
}
