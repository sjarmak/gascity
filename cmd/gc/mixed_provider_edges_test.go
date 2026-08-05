package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beads/contract"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
)

func writeMixedProviderFileCity(t *testing.T) string {
	t.Helper()
	cityPath := t.TempDir()
	rigPath := filepath.Join(cityPath, "frontend")
	if err := os.MkdirAll(filepath.Join(rigPath, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(`[workspace]
name = "demo"

[beads]
provider = "file"

[[rigs]]
name = "frontend"
path = "frontend"
prefix = "fe"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.EnsureCanonicalMetadata(fsys.OSFS{}, filepath.Join(rigPath, ".beads", "metadata.json"), contract.MetadataState{
		Database:     "dolt",
		Backend:      "dolt",
		DoltMode:     "server",
		DoltDatabase: "fe",
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.PersistRigSiteBindings(fsys.OSFS{}, cityPath, []config.Rig{{Name: "frontend", Path: rigPath}}); err != nil {
		t.Fatal(err)
	}
	return cityPath
}

func TestBeadsLifecycleProviderKeepsUnboundIncludeOnlyRigFileBacked(t *testing.T) {
	t.Setenv("GC_BEADS", "")
	t.Setenv("GC_BEADS_SCOPE_ROOT", "")

	cityPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(`[workspace]
name = "demo"

[beads]
provider = "file"

[[rigs]]
name = "frontend"
includes = ["./packs/frontend"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := beadsLifecycleProvider(cityPath)
	if err != nil {
		t.Fatalf("beadsLifecycleProvider() error = %v", err)
	}
	if got != "file" {
		t.Fatalf("beadsLifecycleProvider() = %q, want file until the rig receives a canonical path binding", got)
	}
}

func TestEnsureBeadsProviderCustomExecDoesNotLoadOwnershipConfig(t *testing.T) {
	cityPath := t.TempDir()
	script := filepath.Join(t.TempDir(), "custom-store")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(`[workspace]
name = "demo"

[beads]
provider = "exec:`+script+`"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "pack.toml"), []byte("not valid toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GC_BEADS", "exec:"+script)
	t.Setenv("GC_BEADS_SCOPE_ROOT", cityPath)

	if err := ensureBeadsProvider(cityPath); err != nil {
		t.Fatalf("ensureBeadsProvider(custom exec) = %v, want lifecycle success without ownership config load", err)
	}
	if err := shutdownBeadsProvider(cityPath); err != nil {
		t.Fatalf("shutdownBeadsProvider(custom exec) = %v, want lifecycle success without ownership config load", err)
	}
}
