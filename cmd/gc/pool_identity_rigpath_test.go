package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/runtime"
)

// TestPoolSessionBeadAgentNameNotPathShapedForAbsRigDir is the dr-2x9x /
// dec-a5ar end-to-end pin: a worker agent whose agent config sets dir to the
// ABSOLUTE rig path must, after config load, mint a rig-qualified pool
// instance identity ("decisions/decisions-worker-1"), and the pool session
// bead created for that instance must carry that normalized form as
// agent_name and alias — not the path-shaped
// "<abs rig path>/decisions-worker-1" that models misread as a workspace
// directory.
func TestPoolSessionBeadAgentNameNotPathShapedForAbsRigDir(t *testing.T) {
	rigPath := t.TempDir()
	cityPath := t.TempDir()
	cityToml := `
[workspace]
name = "test-city"

[[rigs]]
name = "decisions"
path = "` + rigPath + `"

[[agent]]
name = "decisions-worker"
dir = "` + rigPath + `"
start_command = "true"
max_active_sessions = 3
`
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte(cityToml), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	cfg, _, err := config.LoadWithIncludes(fsys.OSFS{}, filepath.Join(cityPath, "city.toml"))
	if err != nil {
		t.Fatalf("LoadWithIncludes: %v", err)
	}
	var cfgAgent *config.Agent
	for i := range cfg.Agents {
		if cfg.Agents[i].Name == "decisions-worker" {
			cfgAgent = &cfg.Agents[i]
			break
		}
	}
	if cfgAgent == nil {
		t.Fatalf("agent decisions-worker not found after load")
	}

	_, qualifiedInstance, slot := poolDesiredRequestIdentity(cfgAgent, 1)
	const want = "decisions/decisions-worker-1"
	if qualifiedInstance != want {
		t.Fatalf("poolDesiredRequestIdentity = %q, want %q (rig-qualified, not path-shaped)", qualifiedInstance, want)
	}

	store := beads.NewMemStore()
	bp := newAgentBuildParams("test-city", cityPath, cfg, runtime.NewFake(), time.Now().UTC(), store, io.Discard)
	bp.sessionBeads = newSessionBeadSnapshot(nil)

	info, err := createPoolSessionBeadWithGuardedAlias(bp, cfgAgent, cfgAgent.QualifiedName(), qualifiedInstance, slot, nil)
	if err != nil {
		t.Fatalf("createPoolSessionBeadWithGuardedAlias: %v", err)
	}
	stored, err := store.Get(info.ID)
	if err != nil {
		t.Fatalf("store.Get(%s): %v", info.ID, err)
	}
	if got := stored.Metadata["agent_name"]; got != want {
		t.Errorf("session bead agent_name = %q, want %q", got, want)
	}
	if got := stored.Metadata["alias"]; got != want {
		t.Errorf("session bead alias = %q, want %q", got, want)
	}
	if strings.HasPrefix(stored.Metadata["agent_name"], "/") {
		t.Errorf("agent_name %q is path-shaped (dr-2x9x regression)", stored.Metadata["agent_name"])
	}
}
