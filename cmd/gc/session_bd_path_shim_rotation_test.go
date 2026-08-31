package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/beads/contract"
	"github.com/gastownhall/gascity/internal/fsys"
)

// startPasswordedDoltServerOnExistingDataDir starts a real `dolt sql-server`
// bound to an already-initialized data dir (skipping `dolt init` and the
// user/grant setup that startPasswordedDoltServer performs, since both
// persist in the data dir and must not be redone on a restart). Used to
// simulate a managed Dolt process rebinding to a fresh port over the SAME
// underlying database, the way recoverManagedDoltProcess does.
func startPasswordedDoltServerOnExistingDataDir(t *testing.T, dataDir string) (port int, pid int, cleanup func()) {
	t.Helper()
	skipSlowCmdGCTest(t, "requires a real Dolt server; run make test-cmd-gc-process for full coverage")
	configureTestDoltIdentityEnv(t)

	doltPath := os.Getenv("GC_DOLT_REAL_BINARY")
	var err error
	if doltPath == "" {
		doltPath, err = exec.LookPath("dolt")
		if err != nil {
			t.Skip("dolt not installed")
		}
	}

	port = reserveRandomTCPPort(t)
	cmd := exec.Command(doltPath, "sql-server", "--host", "127.0.0.1", "--port", fmt.Sprintf("%d", port), "--allow-cleartext-passwords", "--loglevel=warning")
	cmd.Dir = dataDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start passworded dolt sql-server on existing data dir: %v", err)
	}

	t.Setenv("GC_DOLT_PASSWORD", "secret")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := managedDoltQueryProbeDirect("127.0.0.1", fmt.Sprintf("%d", port), "root"); err == nil {
			return port, cmd.Process.Pid, func() {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				_, _ = cmd.Process.Wait()
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	t.Fatalf("passworded dolt sql-server on existing data dir, port %d, did not become query-ready", port)
	return 0, 0, func() {}
}

// TestGcBdReadWriteSurvivesRealManagedDoltPortRotation is the direct
// regression test for gastownhall/gascity#792 / gc-end7 (AC4): a live
// session created against a managed Dolt endpoint on port A must still read
// and write the SAME database after a canonical restart rebinds it to port
// B, with no bootstrap of a divergent database along the way.
//
// This exercises `gc bd`'s call-time canonical resolution (doBd, the same
// code path the session bd path shim delegates to per
// session_bd_path_shim.go) against two real `dolt sql-server` processes
// sharing one on-disk database, rather than a fake or mocked store, so a
// regression that only breaks against a real handshake/reconnect is caught.
func TestGcBdReadWriteSurvivesRealManagedDoltPortRotation(t *testing.T) {
	clearInheritedBeadsEnv(t)
	resetFlags(t)

	bdPath := waitTestRealBDPath(t)
	doltPath, err := exec.LookPath("dolt")
	if err != nil {
		t.Skip("dolt not installed")
	}
	t.Setenv("PATH", strings.Join([]string{filepath.Dir(bdPath), filepath.Dir(doltPath), os.Getenv("PATH")}, string(os.PathListSeparator)))

	cityPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[workspace]\nname = \"rotation-test-city\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(city.toml): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cityPath, ".beads"), 0o700); err != nil {
		t.Fatalf("MkdirAll(.beads): %v", err)
	}

	const projectID = "gc-managed-dolt-port-rotation-test"
	if err := contract.WriteProjectIdentity(fsys.OSFS{}, cityPath, projectID); err != nil {
		t.Fatalf("WriteProjectIdentity(): %v", err)
	}
	if _, err := contract.EnsureCanonicalMetadata(fsys.OSFS{}, filepath.Join(cityPath, ".beads", "metadata.json"), contract.MetadataState{
		Database:     "dolt",
		Backend:      "dolt",
		DoltMode:     "server",
		DoltDatabase: "hq",
	}); err != nil {
		t.Fatalf("EnsureCanonicalMetadata(): %v", err)
	}

	layout, err := resolveManagedDoltRuntimeLayout(cityPath)
	if err != nil {
		t.Fatalf("resolveManagedDoltRuntimeLayout: %v", err)
	}

	// The Dolt data dir is independent of the city layout — dolt init names
	// the database after this directory's basename, so it must be "hq" to
	// match the metadata.json dolt_database below (see
	// TestCmdMailInbox_NormalizesCanonicalManagedProviderEnvAndReadsInbox for
	// the same convention).
	dataDir := filepath.Join(t.TempDir(), "hq")
	setupQueries := append(seedDatabaseProjectIDQueries(projectID),
		"CALL DOLT_ADD('.')",
		"CALL DOLT_COMMIT('-m', 'test: seed rotation identity', '--author', 'gascity-test <test@gascity.local>')")
	_, portA, pidA, cleanupA := startPasswordedDoltServer(t, dataDir, setupQueries...)

	if err := ensureCanonicalScopeConfigState(fsys.OSFS{}, cityPath, contract.ConfigState{
		IssuePrefix:    "gc",
		EndpointOrigin: contract.EndpointOriginCityCanonical,
		EndpointStatus: contract.EndpointStatusVerified,
		DoltHost:       "127.0.0.1",
		DoltPort:       fmt.Sprint(portA),
		DoltUser:       "root",
		DoltMode:       "server",
	}); err != nil {
		t.Fatalf("ensureCanonicalScopeConfigState(): %v", err)
	}
	writeManagedDoltRuntimeState(t, layout, dataDir, pidA, portA)

	nativeEnv, err := nativeDoltOpenEnvForScope(cityPath, nil, cityPath)
	if err != nil {
		t.Fatalf("nativeDoltOpenEnvForScope(): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), nativeStorageFixtureBootTimeout)
	nativeStorage, err := beads.OpenNativeStorage(ctx, cityPath, nativeEnv)
	if err != nil {
		cancel()
		t.Fatalf("OpenNativeStorage(): %v", err)
	}
	if err := nativeStorage.SetConfig(ctx, "issue_prefix", "gc"); err != nil {
		_ = nativeStorage.Close()
		cancel()
		t.Fatalf("SetConfig(issue_prefix): %v", err)
	}
	if err := nativeStorage.Close(); err != nil {
		cancel()
		t.Fatalf("close native fixture storage: %v", err)
	}
	cancel()

	t.Setenv("GC_CITY_PATH", cityPath)

	// A live session created while the canonical endpoint is port A: `gc bd
	// create` must reach the real database and mint an ID.
	var stdout, stderr bytes.Buffer
	if code := doBd([]string{"create", "--json", "rotation test bead", "-t", "task"}, &stdout, &stderr); code != 0 {
		t.Fatalf("doBd(create) on port A = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	id := parseCreatedBeadID(t, stdout.String())

	// Canonical restart: the managed Dolt process on port A goes away and a
	// new one comes up on port B over the SAME data dir (same database),
	// then publishes updated canonical runtime state — mirroring
	// recoverManagedDoltProcess + publishManagedDoltRuntimeStateIfOwned on a
	// successful rebind. No bootstrap, clone, or second divergent server is
	// involved.
	cleanupA()
	portB, pidB, cleanupB := startPasswordedDoltServerOnExistingDataDir(t, dataDir)
	defer cleanupB()
	if portB == portA {
		t.Fatalf("rotation must move to a different port than the original %d, got the same port twice", portA)
	}
	writeManagedDoltRuntimeState(t, layout, dataDir, pidB, portB)
	if err := ensureCanonicalScopeConfigState(fsys.OSFS{}, cityPath, contract.ConfigState{
		IssuePrefix:    "gc",
		EndpointOrigin: contract.EndpointOriginCityCanonical,
		EndpointStatus: contract.EndpointStatusVerified,
		DoltHost:       "127.0.0.1",
		DoltPort:       fmt.Sprint(portB),
		DoltUser:       "root",
		DoltMode:       "server",
	}); err != nil {
		t.Fatalf("ensureCanonicalScopeConfigState() after rotation: %v", err)
	}

	// The session's ambient env (nothing in this test process ever changed
	// GC_CITY_PATH or GC_DOLT_PASSWORD) is exactly what a bare `bd`, routed
	// through the session bd path shim to `gc bd`, would see post-rotation:
	// stale in every respect except that gc bd re-resolves the canonical
	// endpoint on every call. It must read the SAME bead created pre-rotation
	// from the SAME database, now reachable only on port B.
	stdout.Reset()
	stderr.Reset()
	if code := doBd([]string{"show", id, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("doBd(show) after rotation to port B = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), id) {
		t.Fatalf("doBd(show) after rotation did not return the bead created before rotation (same database, new port): stdout=%q", stdout.String())
	}

	// A subsequent write must also land on the same, now-canonical, database.
	stdout.Reset()
	stderr.Reset()
	if code := doBd([]string{"update", id, "--set-metadata", "post_rotation=true"}, &stdout, &stderr); code != 0 {
		t.Fatalf("doBd(update) after rotation to port B = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "bd dolt start") || strings.Contains(strings.ToLower(combined), "bootstrap") {
		t.Fatalf("gc bd must never suggest or trigger a bootstrap on endpoint drift, output=%q", combined)
	}
}

func writeManagedDoltRuntimeState(t *testing.T, layout managedDoltRuntimeLayout, dataDir string, pid, port int) {
	t.Helper()
	if err := writeDoltRuntimeStateFile(layout.StateFile, doltRuntimeState{
		Running:   true,
		PID:       pid,
		Port:      port,
		DataDir:   dataDir,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("writeDoltRuntimeStateFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(layout.PIDFile), 0o755); err != nil {
		t.Fatalf("MkdirAll(runtime dir): %v", err)
	}
	if err := os.WriteFile(layout.PIDFile, []byte(fmt.Sprintf("%d\n", pid)), 0o644); err != nil {
		t.Fatalf("WriteFile(pid): %v", err)
	}
}
