package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// A rig whose store fails to open must stay visible in the standalone store
// map as an erroring entry, matching api_state.buildStores. Silently skipping
// it makes the demand phase report rigStores=0 and never count routed work for
// the rig as demand — the store-map half of gastownhall/gascity#4586.
func TestBuildStandaloneRigStoresKeepsUnavailableRigVisible(t *testing.T) {
	t.Setenv("GC_BEADS", "sqlite") // removed provider: deterministic open error
	city := t.TempDir()
	rigPath := t.TempDir()
	cfg := &config.City{Rigs: []config.Rig{{Name: "prod", Path: rigPath}}}
	var stderr bytes.Buffer

	stores := buildStandaloneRigStores(cfg, city, &stderr)

	if stores == nil {
		t.Fatal("buildStandaloneRigStores() = nil, want map with an unavailable entry")
	}
	store, ok := stores["prod"]
	if !ok {
		t.Fatalf("stores = %v, want entry for rig %q", stores, "prod")
	}
	if _, err := store.List(beads.ListQuery{}); err == nil {
		t.Fatal("List() on unavailable rig store: error = nil, want open failure")
	}
	if !strings.Contains(stderr.String(), "prod") {
		t.Fatalf("stderr = %q, want diagnostic naming rig %q", stderr.String(), "prod")
	}
}

// Unbound rigs (no .gc/site.toml binding, empty Path) must stay skipped:
// opening them would alias the rig store to the city scope.
func TestBuildStandaloneRigStoresSkipsUnboundRig(t *testing.T) {
	city := t.TempDir()
	cfg := &config.City{Rigs: []config.Rig{{Name: "unbound"}}}
	var stderr bytes.Buffer

	if stores := buildStandaloneRigStores(cfg, city, &stderr); stores != nil {
		t.Fatalf("buildStandaloneRigStores() = %v, want nil for all-unbound rigs", stores)
	}
}
