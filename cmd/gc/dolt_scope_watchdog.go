package main

// Scope-death watchdog for production managed dolt sql-servers (ga-gz19s4).
//
// gc spawns one managed `dolt sql-server` per scope (city, worktree, PR
// clone), but nothing owned the server once its scope was deleted: the
// server orphaned to ppid 1 and ran forever. On one dev box 314 servers
// accumulated this way — 230 with deleted working directories — holding
// ~44GB RSS and a sustained ~4.5 cores in aggregate.
//
// The watchdog closes the ownership gap at the source instead of relying on
// after-the-fact reaping: production servers are spawned under a supervisor
// process (a gc re-exec, modeled on the managed-dolt TEST watchdog in
// dolt_start_managed.go) that terminates the server when its scope is
// provably gone. "Provably gone" means the server's --config file no longer
// exists on disk for two consecutive checks — the second check is the grace
// window that protects transient states (crash-adoption moves, mid-flight
// renames) from being misread as deletion. The check queries live
// filesystem state every cycle; there are no status files.
//
// Memory cost: a gc re-exec initializes the full cmd/gc dependency graph
// and holds it for the life of the scope — measured at ~97MB RSS per
// watchdog, of which ~20MB is private dirty (the rest is binary text shared
// across all gc processes). The marginal cost is therefore ~20MB of
// unshareable memory per live scope. This is a deliberate trade against the
// multi-GB orphan leak above; it scales only with live scopes because each
// watchdog dies with its server.

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// managedDoltScopeWatchdogArg is the argv[1] re-exec marker for the
	// production scope watchdog. No production `gc` invocation collides with
	// it; reaching init() with it set is proof of an intentional re-exec.
	managedDoltScopeWatchdogArg = "__gc-managed-dolt-scope-watchdog"

	// managedDoltScopeWatchdogEnv disables the production scope watchdog
	// when set to "0" (the managed server is then spawned directly, exactly
	// the pre-watchdog behavior).
	managedDoltScopeWatchdogEnv = "GC_DOLT_SCOPE_WATCHDOG"

	// managedDoltScopeWatchdogIntervalEnv overrides the scope poll interval
	// in milliseconds. Tests use it to shrink the deleted-scope reaction
	// time from tens of seconds to tens of milliseconds.
	managedDoltScopeWatchdogIntervalEnv = "GC_DOLT_SCOPE_WATCHDOG_INTERVAL_MS"

	// managedDoltScopeWatchdogDefaultInterval is the production poll
	// cadence. Scope deletion is rare and the response does not need to be
	// fast — it needs to be reliable. A slow cadence keeps the watchdog's
	// steady-state CPU cost negligible; the per-watchdog memory footprint
	// is the re-exec cost documented in the file header.
	managedDoltScopeWatchdogDefaultInterval = 30 * time.Second

	// managedDoltScopeGoneConfirmations is how many consecutive polls must
	// observe the scope missing before the server is terminated. Two checks
	// one full interval apart distinguish "scope permanently deleted" from
	// "scope momentarily absent" (crash-adoption window, transient rename).
	managedDoltScopeGoneConfirmations = 2

	// managedDoltScopeIdleWindowEnv overrides the idle-abandonment window in
	// milliseconds, following the managedDoltScopeWatchdogIntervalEnv
	// pattern. Tests use it to shrink the production window from hours to
	// tens of milliseconds.
	managedDoltScopeIdleWindowEnv = "GC_DOLT_SCOPE_WATCHDOG_IDLE_WINDOW_MS"

	// managedDoltScopeIdleWindowDefault is the production idle-abandonment
	// window (#4679): how long a temp-rooted scope's server must observe
	// zero established client connections before the watchdog reaps it.
	// Abandonment is rare and undetected abandonment is what leaks, not
	// speed, so this is deliberately long -- an agent idle between
	// operations must never be mistaken for a dead scope.
	managedDoltScopeIdleWindowDefault = 6 * time.Hour
)

func init() {
	if len(os.Args) < 2 || os.Args[1] != managedDoltScopeWatchdogArg {
		return
	}
	os.Exit(runManagedDoltScopeWatchdog(os.Args[2:], os.Stdout, os.Stderr))
}

// managedDoltScopeWatchdogEnabled reports whether production managed dolt
// servers are spawned under the scope watchdog. Default on; opt out with
// GC_DOLT_SCOPE_WATCHDOG=0. Always off in managed-dolt test mode: test
// scopes are owned by the test watchdog (or deliberately watchdog-free),
// and interposing a second supervisor would change test process topology.
func managedDoltScopeWatchdogEnabled() bool {
	return managedDoltScopeWatchdogEnabledFor(managedDoltTestModeEnabled(), os.Getenv(managedDoltScopeWatchdogEnv))
}

// managedDoltScopeWatchdogEnabledFor is the pure decision behind
// managedDoltScopeWatchdogEnabled, split out for tests (the test binary is
// always in test mode, so the production default is unreachable in-process).
func managedDoltScopeWatchdogEnabledFor(testMode bool, envValue string) bool {
	if testMode {
		return false
	}
	return strings.TrimSpace(envValue) != "0"
}

// managedDoltScopeWatchdogInterval resolves the poll interval, honoring the
// millisecond test override when it parses to a positive value.
func managedDoltScopeWatchdogInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(managedDoltScopeWatchdogIntervalEnv))
	if raw == "" {
		return managedDoltScopeWatchdogDefaultInterval
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return managedDoltScopeWatchdogDefaultInterval
	}
	return time.Duration(ms) * time.Millisecond
}

// managedDoltScopeGone reports whether the scope anchoring a managed dolt
// server has been deleted, using the server's --config file as the anchor:
// the config lives inside the scope's runtime directory, so the scope
// disappearing takes the config with it. Only a definitive not-exist counts;
// stat errors (permissions, I/O) lean toward "alive" so a degraded
// filesystem never kills a healthy server.
func managedDoltScopeGone(configFile string) bool {
	if strings.TrimSpace(configFile) == "" {
		return false
	}
	_, err := os.Stat(configFile)
	return errors.Is(err, fs.ErrNotExist)
}

// managedDoltScopeIdleWindow resolves the idle-abandonment window, honoring
// the millisecond test override when it parses to a positive value.
func managedDoltScopeIdleWindow() time.Duration {
	raw := strings.TrimSpace(os.Getenv(managedDoltScopeIdleWindowEnv))
	if raw == "" {
		return managedDoltScopeIdleWindowDefault
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return managedDoltScopeIdleWindowDefault
	}
	return time.Duration(ms) * time.Millisecond
}

// managedDoltScopeRootEphemeral reports whether cityPath sits under the OS
// temp directory -- the gate for the idle-abandonment leg (#4679). Only
// ephemeral scopes (agent scratchpads under /tmp, ad hoc kept worktrees) are
// eligible; the canonical city, and any non-temp rig checkout, keeps today's
// behavior byte for byte.
func managedDoltScopeRootEphemeral(cityPath string) bool {
	cityPath = strings.TrimSpace(cityPath)
	if cityPath == "" {
		return false
	}
	tempDir := filepath.Clean(os.TempDir())
	if tempDir == "" || tempDir == string(filepath.Separator) {
		return false
	}
	clean := filepath.Clean(cityPath)
	return clean == tempDir || strings.HasPrefix(clean, tempDir+string(filepath.Separator))
}

// configuredListenerPort extracts the `listener: / port:` value written into
// a managed dolt config file by writeManagedDoltConfigFile. The second
// result reports whether a well-formed port line was found; a missing or
// malformed line composes to "unknown," which the idle-abandonment leg
// treats the same as "connected" so it never reaps on a bad read.
func configuredListenerPort(configFile string) (uint16, bool) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		rest, ok := strings.CutPrefix(trimmed, "port:")
		if !ok {
			continue
		}
		rest = strings.Trim(strings.TrimSpace(rest), `"'`)
		portNum, err := strconv.ParseUint(rest, 10, 16)
		if err != nil {
			continue
		}
		return uint16(portNum), true
	}
	return 0, false
}

// establishedConnectionCount counts ESTABLISHED TCP connections (state "01")
// whose local port is port, by scanning /proc/net/tcp and /proc/net/tcp6 --
// the same no-status-files, live-filesystem-read approach as
// managedDoltScopeGone and the existing listeningSocketInodesFromProc helper.
// The bool result reports whether either table was readable at all; false
// means "unknown," not "zero," so a degraded /proc read is never misread as
// an idle server.
func establishedConnectionCount(port uint16) (int, bool) {
	count := 0
	checked := false
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		checked = true
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 || fields[3] != "01" {
				continue
			}
			_, portHex, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			gotPort, err := strconv.ParseUint(portHex, 16, 16)
			if err != nil || uint16(gotPort) != port {
				continue
			}
			count++
		}
	}
	return count, checked
}

// managedDoltScopeHasZeroConnections reports whether the managed dolt server
// anchored by configFile currently has zero established client connections.
// The second result reports whether the observation is trustworthy (a known
// listener port and a readable /proc/net/tcp{,6}); an unknown observation
// composes to "not idle" at the call site, never to a reap.
func managedDoltScopeHasZeroConnections(configFile string) (bool, bool) {
	port, ok := configuredListenerPort(configFile)
	if !ok {
		return false, false
	}
	count, checked := establishedConnectionCount(port)
	if !checked {
		return false, false
	}
	return count == 0, true
}

// startManagedDoltSQLServerWithScopeWatchdog spawns the managed dolt
// sql-server under the production scope watchdog. The watchdog process
// (a gc re-exec) starts the server itself and reports the server PID on
// stdout using the same one-line protocol as the test watchdog, so callers
// observe the same managedDoltStartedProcess shape as a direct spawn plus
// the supervising WatchdogPID.
func startManagedDoltSQLServerWithScopeWatchdog(cityPath, configFile, logFilePath string, logFile *os.File) (managedDoltStartedProcess, error) {
	watchdogExecutable, err := managedDoltWatchdogExecutable()
	if err != nil {
		return managedDoltStartedProcess{}, err
	}
	cmd := exec.Command(watchdogExecutable, managedDoltScopeWatchdogArg, configFile, logFilePath, cityPath)
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = managedDoltSQLServerSysProcAttr()
	cmd.Env = doltServerEnv(cityPath, os.Environ())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return managedDoltStartedProcess{}, fmt.Errorf("prepare dolt scope watchdog: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return managedDoltStartedProcess{}, fmt.Errorf("start dolt scope watchdog: %w", err)
	}
	pid, startTimeTicks, startIdentity, err := readManagedDoltScopeWatchdogStart(stdout, cmd.Process.Pid)
	if err != nil {
		_ = terminateManagedDoltPID(cityPath, cmd.Process.Pid)
		_ = cmd.Wait()
		return managedDoltStartedProcess{}, err
	}
	watchdogPID := cmd.Process.Pid
	// Snapshot the watchdog's own OS start identity while it is definitely alive
	// (it has just handed off the dolt PID and entered its supervise loop), before
	// the reaper goroutine below can Wait() it and free the PID. Startup-failure
	// cleanup then re-verifies the watchdog PID the same way it does the dolt
	// child's, so a reused watchdog PID is never signaled.
	watchdogTicks, watchdogIdentity := snapshotManagedDoltStartIdentity(watchdogPID)
	go func() { _ = cmd.Wait() }()
	return managedDoltStartedProcess{
		CityPath:               cityPath,
		PID:                    pid,
		WatchdogPID:            watchdogPID,
		StartTimeTicks:         startTimeTicks,
		StartIdentity:          startIdentity,
		WatchdogStartTimeTicks: watchdogTicks,
		WatchdogStartIdentity:  watchdogIdentity,
	}, nil
}

// runManagedDoltScopeWatchdog is the re-exec'd watchdog process body. It
// spawns the dolt sql-server as its own process-group leader, prints the
// server PID on stdout, then supervises: it terminates the server when the
// scope is gone for managedDoltScopeGoneConfirmations consecutive polls,
// forwards SIGTERM/SIGINT, and exits when the server exits on its own.
func runManagedDoltScopeWatchdog(args []string, stdout, stderr *os.File) int {
	if len(args) != 3 {
		fmt.Fprintf(stderr, "usage: %s <config-file> <log-file> <city-path>\n", managedDoltScopeWatchdogArg) //nolint:errcheck
		return 2
	}
	configFile := args[0]
	logFilePath := args[1]
	cityPath := args[2]
	if strings.TrimSpace(configFile) == "" {
		fmt.Fprintln(stderr, "empty config file path") //nolint:errcheck
		return 2
	}
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "open dolt log: %v\n", err) //nolint:errcheck
		return 1
	}
	defer logFile.Close() //nolint:errcheck

	cmd := exec.Command("dolt", "sql-server", "--config", configFile)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	// Setpgid: the dolt sql-server leads its own process group, matching
	// the direct production spawn (managedDoltSQLServerSysProcAttr) and the
	// test watchdog's layout, and keeping the server's descendants out of
	// the watchdog's own group. Termination here is leader-only:
	// the guarded terminate below signals just this PID — group-kill
	// (kill(-pgid, ...)) exists only in terminateManagedDoltTestPID, the
	// test-registry reaper. Leader-only is accepted on this path because
	// the managed config disables auto_gc/stats helper workers (see
	// cmd_dolt_config.go), so descendant helpers are rare by construction,
	// and a SIGTERM'd dolt winds down its own children; only the SIGKILL
	// escalation of an unresponsive server could strand descendants.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = doltServerEnv(cityPath, os.Environ())
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "start dolt sql-server: %v\n", err) //nolint:errcheck
		return 1
	}
	// Report the dolt child's PID and OS start identity to the parent BEFORE the
	// reap goroutine below can Wait() the child and free its numeric PID.
	// Snapshotting here — while the watchdog still holds the un-reaped child — is
	// race-free and mirrors the direct-spawn snapshot in startManagedDoltSQLServer,
	// so the parent's startup-failure cleanup guard
	// (terminateManagedDoltStartedProcess) never signals an unrelated process that
	// reused the PID after this child exited and was reaped. The watchdog also
	// reuses this snapshot to guard its own scope-gone and signal-forward
	// termination of the child below (terminateManagedDoltScopeWatchdogChild), so
	// a reaped-then-reused PID is never signaled on the local reap path either.
	// snapshotManagedDoltStartIdentity reads the ps fallback lazily, so this
	// handshake — which the parent reads under a timeout — never blocks on a ps
	// fork when /proc ticks are available.
	startPID := cmd.Process.Pid
	startTicks, startIdentity := snapshotManagedDoltStartIdentity(startPID)
	fmt.Fprintln(stdout, formatManagedDoltWatchdogStartLine(startPID, startTicks, startIdentity)) //nolint:errcheck

	interval := managedDoltScopeWatchdogInterval()
	fmt.Fprintf(logFile, "gc scope watchdog: supervising dolt sql-server pid %d (config %s, poll interval %s)\n", //nolint:errcheck
		cmd.Process.Pid, configFile, interval)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	ephemeralScope := managedDoltScopeRootEphemeral(cityPath)
	idleWindow := managedDoltScopeIdleWindow()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	goneStreak := 0
	var idleSince time.Time
	for {
		select {
		case sig := <-signals:
			fmt.Fprintf(logFile, "gc scope watchdog: received %v; terminating dolt sql-server pid %d\n", sig, cmd.Process.Pid) //nolint:errcheck
			_ = terminateManagedDoltScopeWatchdogChild(cityPath, cmd.Process.Pid, startTicks, startIdentity)
			<-done
			return 0
		case <-ticker.C:
			if managedDoltScopeGone(configFile) {
				goneStreak++
				if goneStreak < managedDoltScopeGoneConfirmations {
					continue
				}
				fmt.Fprintf(logFile, "gc scope watchdog: config %s gone for %d consecutive checks; terminating dolt sql-server pid %d\n", //nolint:errcheck
					configFile, goneStreak, cmd.Process.Pid)
				_ = terminateManagedDoltScopeWatchdogChild(cityPath, cmd.Process.Pid, startTicks, startIdentity)
				<-done
				return 0
			}
			goneStreak = 0

			if !ephemeralScope {
				continue
			}
			zero, known := managedDoltScopeHasZeroConnections(configFile)
			if !known || !zero {
				idleSince = time.Time{}
				continue
			}
			if idleSince.IsZero() {
				idleSince = time.Now()
				continue
			}
			if idleWait := time.Since(idleSince); idleWait >= idleWindow {
				fmt.Fprintf(logFile, "gc scope watchdog: temp-rooted scope %s idle with zero client connections for %s; terminating dolt sql-server pid %d\n", //nolint:errcheck
					cityPath, idleWait, cmd.Process.Pid)
				_ = terminateManagedDoltScopeWatchdogChild(cityPath, cmd.Process.Pid, startTicks, startIdentity)
				<-done
				return 0
			}
		case err := <-done:
			if err != nil {
				fmt.Fprintf(logFile, "gc scope watchdog: dolt sql-server pid %d exited with error: %v\n", cmd.Process.Pid, err) //nolint:errcheck
				return 1
			}
			fmt.Fprintf(logFile, "gc scope watchdog: dolt sql-server pid %d exited cleanly\n", cmd.Process.Pid) //nolint:errcheck
			return 0
		}
	}
}

// terminateManagedDoltScopeWatchdogChild terminates the watchdog's own dolt
// sql-server child by PID, guarded against PID reuse with the child's OS start
// identity that the watchdog snapshotted at startup (startTicks/startIdentity,
// captured before the reap goroutine could Wait() the child and free its
// numeric PID). This is the production scope-gone/signal reap path — the reason
// the watchdog exists — so it carries the same reuse hazard the parent-side
// cleanup does: the reaper goroutine frees the PID when a server that outlived
// the SIGTERM grace finally exits, and terminateManagedDoltPIDGuarded re-checks
// identity before both SIGTERM and SIGKILL so neither signal lands on an
// unrelated process that reused the number. A zero identity (no /proc ticks and
// no ps fallback) composes to the legacy unconditional terminate.
func terminateManagedDoltScopeWatchdogChild(cityPath string, pid int, startTicks uint64, startIdentity string) error {
	return terminateManagedDoltPIDGuarded(cityPath, pid, func() bool {
		return managedDoltPIDStartIdentityMatches(pid, startTicks, startIdentity)
	})
}
