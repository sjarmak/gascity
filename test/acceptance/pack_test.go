//go:build acceptance_a

// Pack materialization acceptance tests.
//
// Verifies that materialized packs have correct permissions (scripts
// executable) and contain all expected artifacts.
package acceptance_test

import (
	"os"
	"path/filepath"
	"testing"

	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

// TestGastownPackScriptsExecutable verifies that shell scripts in
// materialized gastown packs have executable permissions.
func TestGastownPackScriptsExecutable(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	scripts := []string{
		"packs/gastown/scripts/worktree-setup.sh",
		"packs/gastown/scripts/tmux-theme.sh",
		"packs/gastown/scripts/tmux-keybindings.sh",
	}
	for _, s := range scripts {
		path := filepath.Join(c.Dir, s)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s not found: %v", s, err)
			continue
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable (mode %o)", s, info.Mode())
		}
	}
}

// TestGastownPackCompleteness verifies that the materialized gastown
// pack contains all expected directories and key files.
func TestGastownPackCompleteness(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	expected := []string{
		"packs/gastown/pack.toml",
		"packs/gastown/prompts",
		"packs/gastown/formulas",
		"packs/gastown/scripts",
		"packs/gastown/overlays",
		"packs/gastown/commands",
		"packs/maintenance/pack.toml",
		"packs/maintenance/prompts",
		"packs/maintenance/formulas",
	}
	for _, e := range expected {
		if !c.HasFile(e) {
			t.Errorf("missing: %s", e)
		}
	}
}

// TestMaintenancePackScriptsExecutable verifies maintenance pack scripts.
func TestMaintenancePackScriptsExecutable(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	// Walk maintenance scripts and verify all .sh files are executable.
	scriptsDir := filepath.Join(c.Dir, "packs", "maintenance", "scripts")
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		// Not all maintenance packs have a scripts dir.
		t.Skip("no maintenance scripts dir")
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".sh" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Errorf("stat %s: %v", e.Name(), err)
			continue
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("packs/maintenance/scripts/%s is not executable (mode %o)", e.Name(), info.Mode())
		}
	}
}
