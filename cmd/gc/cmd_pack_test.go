package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
)

// TestDoPackFetch_BootstrapsDeclaredImports is the regression test for
// issue #805: a fresh PackV2 city with a remote [imports.X] entry has
// no CLI bootstrap path. `gc pack fetch` must populate the include-cache
// BEFORE calling config.Load, otherwise loadCityConfig dies on the
// same cache-miss the user was trying to resolve.
func TestDoPackFetch_BootstrapsDeclaredImports(t *testing.T) {
	clearGCEnv(t)
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	repo := initImportBarePackRepo(t, "flywheel", "", `
[pack]
name = "flywheel"
schema = 1

[[agent]]
name = "cass"
scope = "city"
`)
	source := "file://" + repo

	cityDir := filepath.Join(dir, "city")
	if err := os.MkdirAll(cityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCityToml(t, cityDir, "[workspace]\nname = \"demo\"\n")
	writePackToml(t, cityDir, `[pack]
name = "demo"
schema = 1

[imports.cass]
source = "`+source+`"
`)
	t.Setenv("GC_CITY", cityDir)

	var stdout, stderr bytes.Buffer
	code := doPackFetch(&stdout, &stderr)
	if code != 0 {
		t.Fatalf("doPackFetch code = %d\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}

	// The whole point of the bug: after `gc pack fetch`, a subsequent
	// config.Load must succeed. Before #805's fix this failed with
	// "remote include ... is not cached at ...".
	cfg, _, err := config.LoadWithIncludes(fsys.OSFS{}, filepath.Join(cityDir, "city.toml"))
	if err != nil {
		t.Fatalf("LoadWithIncludes after bootstrap: %v", err)
	}
	found := false
	for _, a := range cfg.Agents {
		if a.Name == "cass" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(cfg.Agents))
		for _, a := range cfg.Agents {
			names = append(names, a.QualifiedName())
		}
		t.Errorf("expected imported agent named cass after bootstrap; got %v", names)
	}
}

func TestDoPackFetch_NoImportsOrPacksReportsClean(t *testing.T) {
	clearGCEnv(t)
	cityDir := t.TempDir()
	writeCityToml(t, cityDir, "[workspace]\nname = \"demo\"\n")
	t.Setenv("GC_CITY", cityDir)

	var stdout, stderr bytes.Buffer
	code := doPackFetch(&stdout, &stderr)
	if code != 0 {
		t.Fatalf("doPackFetch code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No remote packs or remote imports configured") {
		t.Errorf("stdout = %q, want mention of empty config", stdout.String())
	}
}
