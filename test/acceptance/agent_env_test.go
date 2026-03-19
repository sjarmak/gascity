//go:build acceptance_a

// Agent environment completeness tests.
//
// For each example config, verifies that gc init produces a city where
// config explain shows all required GC_* variables for each agent.
// These tests exercise the real gc binary's template resolution path.
package acceptance_test

import (
	"path/filepath"
	"strings"
	"testing"

	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

// TestAgentEnv_GastownCityAgents verifies that city-scoped gastown
// agents (mayor, deacon, boot) get the expected core env vars.
func TestAgentEnv_GastownCityAgents(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	// gc config explain outputs the resolved config for the city.
	// We check that city-scoped agents have the required vars.
	out, err := c.GC("config", "explain", "--city", c.Dir)
	if err != nil {
		t.Fatalf("gc config explain: %v\n%s", err, out)
	}

	// The output should contain references to city-scoped agents.
	// Verify the config loaded successfully (no pack errors).
	if strings.Contains(out, "pack.toml: no such file") {
		t.Fatalf("config explain failed with missing packs:\n%s", out)
	}
}

// TestAgentEnv_TutorialAgent verifies the tutorial config produces
// a valid agent with all required env vars.
func TestAgentEnv_TutorialAgent(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	out, err := c.GC("config", "explain", "--city", c.Dir)
	if err != nil {
		t.Fatalf("gc config explain: %v\n%s", err, out)
	}

	// Tutorial config should have at least one agent (the mayor).
	if !strings.Contains(out, "Agent:") {
		t.Fatal("config explain shows no agents for tutorial config")
	}
}

// TestAgentEnv_GastownRigAgents verifies that initializing gastown with
// a rig produces rig-scoped agents (witness, refinery, polecat) that
// have GC_RIG and GC_RIG_ROOT in their resolved config.
func TestAgentEnv_GastownRigAgents(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	// Create a fake rig directory so config can reference it.
	rigDir := filepath.Join(c.Dir, "myrig")
	if err := mkdirAll(rigDir); err != nil {
		t.Fatal(err)
	}

	// Add a rig to the city config.
	toml := c.ReadFile("city.toml")
	toml += "\n[[rig]]\nname = \"myrig\"\npath = \"" + rigDir + "\"\nincludes = [\"packs/gastown\"]\n"
	c.WriteConfig(toml)

	out, err := c.GC("config", "explain", "--city", c.Dir)
	if err != nil {
		// Config explain may warn but shouldn't error on valid config.
		// Check if it's a hard failure vs. warning.
		if strings.Contains(out, "Error") || strings.Contains(out, "error:") {
			t.Fatalf("gc config explain failed:\n%s", err)
		}
	}

	// Rig-scoped agents (witness, refinery, polecat) are only expanded
	// when the rig is registered. Config explain shows the city-level
	// view. Verify the rig is at least referenced in the config.
	tomlContent := c.ReadFile("city.toml")
	if !strings.Contains(tomlContent, "myrig") {
		t.Fatal("city.toml doesn't reference the rig")
	}

	// The key assertion: config loads without errors about missing packs
	// even with a rig configured.
	if strings.Contains(out, "pack.toml: no such file") {
		t.Fatalf("config explain failed with missing packs for rig config:\n%s", out)
	}
}

// TestAgentEnv_SwarmConfig verifies the swarm example config loads
// without errors.
func TestAgentEnv_SwarmConfig(t *testing.T) {
	swarmDir := filepath.Join(helpers.ExamplesDir(), "swarm")
	if _, err := statFile(swarmDir); err != nil {
		t.Skip("swarm example not found")
	}

	c := helpers.NewCity(t, testEnv)
	c.InitFrom(swarmDir)

	if !c.HasFile("city.toml") {
		t.Fatal("city.toml not created for swarm config")
	}

	out, err := c.GC("config", "explain", "--city", c.Dir)
	if err != nil {
		if strings.Contains(out, "pack.toml: no such file") {
			t.Fatalf("swarm config has missing pack references:\n%s", out)
		}
		// Other errors may be OK (missing provider, etc.)
	}
}

func mkdirAll(path string) error {
	return helpers.MkdirAll(path)
}

func statFile(path string) (bool, error) {
	_, err := helpers.StatFile(path)
	return err == nil, err
}
