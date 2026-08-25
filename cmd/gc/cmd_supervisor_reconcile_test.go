package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/supervisor"
)

// newTestManagedCity builds a managedCity that reconcileCities/stopManagedCity
// can safely stop without touching real processes: cancel is a no-op, done is
// pre-closed so stopManagedCity's first select case fires immediately, and
// cr/closer are nil so stopManagedCity takes its lightest path.
func newTestManagedCity(gen int64) *managedCity {
	done := make(chan struct{})
	close(done)
	mc := &managedCity{
		name:          "city-a",
		started:       true,
		status:        "running",
		cancel:        func() {},
		done:          done,
		regGeneration: gen,
	}
	return mc
}

func setUpReconcileTestCity(t *testing.T) (dir, cityPath string) {
	t.Helper()
	dir = t.TempDir()
	cityPath = filepath.Join(dir, "city-a")
	if err := os.MkdirAll(cityPath, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, cityPath
}

// TestReconcileCitiesStopsAuthorizedUnregister proves the smallest accepted
// behavior: an explicit gc unregister for the exact current generation is
// still honored and the city is stopped (ga-nqlb8q).
func TestReconcileCitiesStopsAuthorizedUnregister(t *testing.T) {
	dir, cityPath := setUpReconcileTestCity(t)
	reg := supervisor.NewRegistry(filepath.Join(dir, "cities.toml"))
	if err := reg.Register(cityPath, "city-a"); err != nil {
		t.Fatal(err)
	}
	entries, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	gen := entries[0].Generation

	cr := newCityRegistry()
	mc := newTestManagedCity(gen)
	cr.BatchUpdate(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		cities[cityPath] = mc
	})

	// Explicit gc unregister: removes the entry AND records a durable
	// authorization for exactly this generation.
	if err := reg.Unregister(cityPath); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	reconcileCities(reg, cr, supervisor.PublicationConfig{}, &stdout, &stderr)

	cr.ReadCallback(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		if _, ok := cities[cityPath]; ok {
			t.Fatal("expected authorized-unregister city to be removed from cr, but it is still present")
		}
	})
	if !mc.tombstoned.Load() {
		t.Error("expected authorized-unregister city to be tombstoned")
	}
	if !strings.Contains(stdout.String(), "Unregistered city") {
		t.Errorf("expected stop confirmation on stdout, got: %s", stdout.String())
	}
	if strings.Contains(stderr.String(), "ALERT") {
		t.Errorf("expected no ALERT for an authorized unregister, got stderr: %s", stderr.String())
	}
}

// TestReconcileCitiesHoldsUnauditedDisappearance proves the core regression
// fix: a city missing from the registry read with NO recorded unregister
// authorization must be held running and a loud typed alert emitted, never
// silently stopped (ga-nqlb8q — this is the incident's root cause).
func TestReconcileCitiesHoldsUnauditedDisappearance(t *testing.T) {
	dir, cityPath := setUpReconcileTestCity(t)
	reg := supervisor.NewRegistry(filepath.Join(dir, "cities.toml"))
	if err := reg.Register(cityPath, "city-a"); err != nil {
		t.Fatal(err)
	}
	entries, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	gen := entries[0].Generation

	cr := newCityRegistry()
	mc := newTestManagedCity(gen)
	cr.BatchUpdate(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		cities[cityPath] = mc
	})

	// Simulate an unaudited registry disappearance: remove the on-disk
	// entry directly, bypassing Unregister — so no authorization record
	// is created. This is the exact shape of an empty/racy registry read.
	regFilePath := filepath.Join(dir, "cities.toml")
	if err := os.WriteFile(regFilePath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	reconcileCities(reg, cr, supervisor.PublicationConfig{}, &stdout, &stderr)

	cr.ReadCallback(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		if _, ok := cities[cityPath]; !ok {
			t.Fatal("expected city to be HELD running after an unaudited registry disappearance, but it was removed")
		}
	})
	if mc.tombstoned.Load() {
		t.Error("expected city to NOT be tombstoned on an unaudited disappearance")
	}
	if !strings.Contains(stderr.String(), "ALERT") {
		t.Errorf("expected a loud typed ALERT on stderr for an unaudited disappearance, got: %s", stderr.String())
	}
	if strings.Contains(stdout.String(), "Unregistered city") {
		t.Errorf("expected no stop confirmation for a held city, got stdout: %s", stdout.String())
	}
}

// TestReconcileCitiesHoldsStaleGenerationAuthorization proves that an
// authorization recorded for an OLDER registration generation does not
// authorize stopping a city that was re-registered (and is now running
// under a newer generation) — a stale-generation disappearance must be
// held, not stopped (ga-nqlb8q).
func TestReconcileCitiesHoldsStaleGenerationAuthorization(t *testing.T) {
	dir, cityPath := setUpReconcileTestCity(t)
	reg := supervisor.NewRegistry(filepath.Join(dir, "cities.toml"))
	if err := reg.Register(cityPath, "city-a"); err != nil {
		t.Fatal(err)
	}
	entries, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	staleGen := entries[0].Generation

	// Unregister (records an authorization for staleGen) then re-register,
	// minting a NEW generation. The running managedCity in cr still carries
	// the stale generation, simulating a city that hasn't been restarted
	// yet against the newer registration.
	if err := reg.Unregister(cityPath); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(cityPath, "city-a"); err != nil {
		t.Fatal(err)
	}
	entries, err = reg.List()
	if err != nil {
		t.Fatal(err)
	}
	newGen := entries[0].Generation
	if newGen == staleGen {
		t.Fatalf("expected re-register to mint a new generation, got same %d", newGen)
	}

	// Now force a fresh unaudited disappearance for the CURRENT generation
	// (delete the on-disk entry without going through Unregister), while
	// the in-memory managedCity still carries the STALE generation. The
	// stale generation's already-consumed... actually not consumed here:
	// prove the stale authorization cannot cover the currently-running
	// (stale-tagged) city once the registry itself has moved on.
	if err := os.WriteFile(filepath.Join(dir, "cities.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cr := newCityRegistry()
	mc := newTestManagedCity(staleGen)
	cr.BatchUpdate(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		cities[cityPath] = mc
	})

	var stdout, stderr bytes.Buffer
	reconcileCities(reg, cr, supervisor.PublicationConfig{}, &stdout, &stderr)

	cr.ReadCallback(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		if _, ok := cities[cityPath]; !ok {
			t.Fatal("expected city running under a stale generation to be HELD, not stopped, on disappearance")
		}
	})
	if !strings.Contains(stderr.String(), "ALERT") {
		t.Errorf("expected ALERT for stale-generation disappearance, got stderr: %s", stderr.String())
	}
}

// TestReconcileCitiesHoldsOnCorruptRegistry proves that a corrupt registry
// file causes reconcileCities to log and return without touching any
// running city — a corrupt read must fail safe (hold), never fail stop
// (ga-nqlb8q).
func TestReconcileCitiesHoldsOnCorruptRegistry(t *testing.T) {
	dir, cityPath := setUpReconcileTestCity(t)
	regFilePath := filepath.Join(dir, "cities.toml")
	if err := os.WriteFile(regFilePath, []byte("this is not valid toml [[["), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := supervisor.NewRegistry(regFilePath)

	cr := newCityRegistry()
	mc := newTestManagedCity(1)
	cr.BatchUpdate(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		cities[cityPath] = mc
	})

	var stdout, stderr bytes.Buffer
	reconcileCities(reg, cr, supervisor.PublicationConfig{}, &stdout, &stderr)

	cr.ReadCallback(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		if _, ok := cities[cityPath]; !ok {
			t.Fatal("expected city to be HELD running when the registry file itself is corrupt")
		}
	})
	if mc.tombstoned.Load() {
		t.Error("expected city to NOT be tombstoned when the registry read fails")
	}
	if !strings.Contains(stderr.String(), "registry:") {
		t.Errorf("expected the registry-read error to be logged, got stderr: %s", stderr.String())
	}
}

// TestReconcileCitiesRestartAfterHold proves the end-to-end restart path:
// a city held by an unaudited disappearance keeps running across repeated
// reconcile ticks (no flapping), and once the operator performs a genuine
// explicit unregister for its current generation, the very next reconcile
// tick stops it cleanly (ga-nqlb8q).
func TestReconcileCitiesRestartAfterHold(t *testing.T) {
	dir, cityPath := setUpReconcileTestCity(t)
	reg := supervisor.NewRegistry(filepath.Join(dir, "cities.toml"))
	if err := reg.Register(cityPath, "city-a"); err != nil {
		t.Fatal(err)
	}
	entries, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	gen := entries[0].Generation

	cr := newCityRegistry()
	mc := newTestManagedCity(gen)
	cr.BatchUpdate(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		cities[cityPath] = mc
	})

	// Tick 1: unaudited disappearance — city held.
	if err := os.WriteFile(filepath.Join(dir, "cities.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout1, stderr1 bytes.Buffer
	reconcileCities(reg, cr, supervisor.PublicationConfig{}, &stdout1, &stderr1)
	cr.ReadCallback(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		if _, ok := cities[cityPath]; !ok {
			t.Fatal("tick 1: expected city held after unaudited disappearance")
		}
	})

	// Tick 2: registry still empty, no new authorization — city must still
	// be held (no flapping, no eventual stop from repeated ticks alone).
	var stdout2, stderr2 bytes.Buffer
	reconcileCities(reg, cr, supervisor.PublicationConfig{}, &stdout2, &stderr2)
	cr.ReadCallback(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		if _, ok := cities[cityPath]; !ok {
			t.Fatal("tick 2: expected city still held; repeated ticks must not eventually stop an unauthorized disappearance")
		}
	})
	if !strings.Contains(stderr2.String(), "ALERT") {
		t.Error("tick 2: expected the alert to keep firing on every tick the disappearance remains unaudited")
	}

	// Re-register (city comes back into the registry at the SAME path);
	// this mints a new generation, but our in-memory mc still carries the
	// old one — reconcile must not touch a city that IS desired again.
	if err := reg.Register(cityPath, "city-a"); err != nil {
		t.Fatal(err)
	}
	var stdout3, stderr3 bytes.Buffer
	reconcileCities(reg, cr, supervisor.PublicationConfig{}, &stdout3, &stderr3)
	cr.ReadCallback(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		if _, ok := cities[cityPath]; !ok {
			t.Fatal("tick 3: expected city untouched once its path is desired again")
		}
	})

	// Now perform a genuine explicit unregister at the CURRENT generation
	// and confirm the very next tick stops it cleanly.
	entries, err = reg.List()
	if err != nil {
		t.Fatal(err)
	}
	currentGen := entries[0].Generation
	mc.regGeneration = currentGen
	if err := reg.Unregister(cityPath); err != nil {
		t.Fatal(err)
	}
	var stdout4, stderr4 bytes.Buffer
	reconcileCities(reg, cr, supervisor.PublicationConfig{}, &stdout4, &stderr4)
	cr.ReadCallback(func(cities map[string]*managedCity, _ map[string]cityInitProgress, _ map[string]*initFailRecord, _ map[string]*panicRecord) {
		if _, ok := cities[cityPath]; ok {
			t.Fatal("tick 4: expected city stopped after a genuine explicit unregister at its current generation")
		}
	})
	if !mc.tombstoned.Load() {
		t.Error("tick 4: expected city to be tombstoned after authorized stop")
	}
	if strings.Contains(stderr4.String(), "ALERT") {
		t.Errorf("tick 4: expected no ALERT for an authorized unregister, got stderr: %s", stderr4.String())
	}
}
