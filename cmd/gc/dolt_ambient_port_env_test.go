package main

import (
	"io"
	"os"
	"testing"
)

// enableSupervisorAmbientDoltPortExportForTest turns on the supervisor-only
// ambient export gate for one test and restores the disabled default on
// cleanup so the process-env mutation cannot leak into other tests.
func enableSupervisorAmbientDoltPortExportForTest(t *testing.T) {
	t.Helper()
	supervisorAmbientDoltPortExportEnabled.Store(true)
	t.Cleanup(func() { supervisorAmbientDoltPortExportEnabled.Store(false) })
}

// clearAmbientDoltPortEnvForTest sandboxes GC_DOLT_PORT and
// BEADS_DOLT_SERVER_PORT: t.Setenv registers restoration of the original
// values, then Unsetenv models a fresh supervisor process env.
func clearAmbientDoltPortEnvForTest(t *testing.T) {
	t.Helper()
	t.Setenv("GC_DOLT_PORT", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	_ = os.Unsetenv("GC_DOLT_PORT")
	_ = os.Unsetenv("BEADS_DOLT_SERVER_PORT")
}

func TestExportSupervisorAmbientDoltPortEnv(t *testing.T) {
	t.Run("disabled gate leaves process env untouched", func(t *testing.T) {
		clearAmbientDoltPortEnvForTest(t)
		exportSupervisorAmbientDoltPortEnv("43307")
		if _, ok := os.LookupEnv("GC_DOLT_PORT"); ok {
			t.Fatal("GC_DOLT_PORT set despite disabled export gate")
		}
		if _, ok := os.LookupEnv("BEADS_DOLT_SERVER_PORT"); ok {
			t.Fatal("BEADS_DOLT_SERVER_PORT set despite disabled export gate")
		}
	})

	t.Run("enabled gate exports both port vars", func(t *testing.T) {
		clearAmbientDoltPortEnvForTest(t)
		enableSupervisorAmbientDoltPortExportForTest(t)
		exportSupervisorAmbientDoltPortEnv("43307")
		if got := os.Getenv("GC_DOLT_PORT"); got != "43307" {
			t.Fatalf("GC_DOLT_PORT = %q, want %q", got, "43307")
		}
		if got := os.Getenv("BEADS_DOLT_SERVER_PORT"); got != "43307" {
			t.Fatalf("BEADS_DOLT_SERVER_PORT = %q, want %q", got, "43307")
		}
	})

	t.Run("port flip overwrites a stale ambient pin", func(t *testing.T) {
		// The systemd drop-in stopgap pinned a static port; every managed
		// Dolt respawn onto the sibling port then poisoned ambient
		// passthrough until an operator rebound the pin. The export must
		// overwrite, not respect, a stale value.
		clearAmbientDoltPortEnvForTest(t)
		enableSupervisorAmbientDoltPortExportForTest(t)
		t.Setenv("GC_DOLT_PORT", "29621")
		t.Setenv("BEADS_DOLT_SERVER_PORT", "29621")
		exportSupervisorAmbientDoltPortEnv("29620")
		if got := os.Getenv("GC_DOLT_PORT"); got != "29620" {
			t.Fatalf("GC_DOLT_PORT = %q, want %q", got, "29620")
		}
		if got := os.Getenv("BEADS_DOLT_SERVER_PORT"); got != "29620" {
			t.Fatalf("BEADS_DOLT_SERVER_PORT = %q, want %q", got, "29620")
		}
	})

	t.Run("blank port never unsets the last known-good hint", func(t *testing.T) {
		clearAmbientDoltPortEnvForTest(t)
		enableSupervisorAmbientDoltPortExportForTest(t)
		t.Setenv("GC_DOLT_PORT", "43307")
		t.Setenv("BEADS_DOLT_SERVER_PORT", "43307")
		exportSupervisorAmbientDoltPortEnv("   ")
		if got := os.Getenv("GC_DOLT_PORT"); got != "43307" {
			t.Fatalf("GC_DOLT_PORT = %q, want preserved %q", got, "43307")
		}
		if got := os.Getenv("BEADS_DOLT_SERVER_PORT"); got != "43307" {
			t.Fatalf("BEADS_DOLT_SERVER_PORT = %q, want preserved %q", got, "43307")
		}
	})
}

// TestDoSupervisorRunEnablesAmbientDoltPortExport verifies `gc supervisor
// run` arms the ambient export gate before entering the run loop, so the
// city-runtime ticks it drives may project the live managed Dolt port into
// the supervisor's own process env.
func TestDoSupervisorRunEnablesAmbientDoltPortExport(t *testing.T) {
	t.Setenv("BEADS_ACTOR", "controller") // keep defaultSupervisorBeadsActor inert

	origRun := runSupervisorFunc
	invoked := false
	runSupervisorFunc = func(io.Writer, io.Writer) int {
		invoked = true
		return 0
	}
	t.Cleanup(func() { runSupervisorFunc = origRun })
	t.Cleanup(func() { supervisorAmbientDoltPortExportEnabled.Store(false) })

	if rc := doSupervisorRun(io.Discard, io.Discard); rc != 0 {
		t.Fatalf("doSupervisorRun = %d, want 0", rc)
	}
	if !invoked {
		t.Fatal("runSupervisorFunc was not invoked")
	}
	if !supervisorAmbientDoltPortExportEnabled.Load() {
		t.Fatal("supervisor run did not enable ambient dolt port export")
	}
}
