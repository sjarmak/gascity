package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/pathutil"
)

// TestPoolTriggerWorkDirDoesNotCreateDirectories guards the dry-run purity
// contract from gc-r9fx: computing a gc.work_dir metadata patch is a pure
// path computation and must never create directories on disk. The former
// implementation routed through resolveConfiguredWorkDir → resolveAgentDir,
// whose MkdirAll materialized agent base directories during `gc sling
// --dry-run` and other read-only desired-state builds.
func TestPoolTriggerWorkDirDoesNotCreateDirectories(t *testing.T) {
	cityPath := t.TempDir()
	cfgAgent := config.Agent{Name: "claude", Dir: "agents/claude"}
	bp := &agentBuildParams{cityPath: cityPath, cityName: "testcity"}
	request := SessionRequest{WorkBeadID: "gc-abc12", WorkBeadTitle: "fix the thing"}

	workDir := poolTriggerWorkDir(bp, &cfgAgent, "claude", request)
	if workDir == "" {
		t.Fatal("poolTriggerWorkDir returned empty workDir for a valid request")
	}
	if want := filepath.Join(cityPath, "agents", "claude"); !pathutil.PathWithin(want, workDir) {
		t.Fatalf("workDir = %q, want under %q", workDir, want)
	}
	if _, err := os.Stat(filepath.Join(cityPath, "agents")); !os.IsNotExist(err) {
		t.Errorf("pure workDir computation created directories under city path (stat err = %v)", err)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("pure workDir computation created the workDir itself (stat err = %v)", err)
	}
}

// TestPoolTriggerWorkDirBareProviderUsesScratchNotCityRoot guards gascity#938:
// a pool-trigger session for an agent with no dir, no work_dir, and therefore
// no rig association (the bare-provider shape produced by a session created
// directly against a provider preset with no matching agents/ entry) must not
// silently resolve its bead-trigger workdir to the city root itself.
func TestPoolTriggerWorkDirBareProviderUsesScratchNotCityRoot(t *testing.T) {
	cityPath := t.TempDir()
	cfgAgent := config.Agent{Name: "codex", Provider: "codex"}
	bp := &agentBuildParams{cityPath: cityPath, cityName: "testcity"}
	request := SessionRequest{WorkBeadID: "gc-485446", WorkBeadTitle: "load context and understand assignment"}

	workDir := poolTriggerWorkDir(bp, &cfgAgent, "codex", request)
	if workDir == "" {
		t.Fatal("poolTriggerWorkDir returned empty workDir for a valid bare-provider request")
	}
	if workDir == cityPath {
		t.Fatalf("workDir = %q, must not resolve to the city root itself", workDir)
	}
	want := filepath.Join(cityPath, ".gc", "scratch", "codex", "gc-485446")
	if workDir != want {
		t.Fatalf("workDir = %q, want scratch dir %q", workDir, want)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("pure workDir computation created the workDir itself (stat err = %v)", err)
	}
}

// TestPoolTriggerWorkDirBareProviderDistinctPerBead guards against two
// concurrent bare-provider sessions colliding on the same scratch directory,
// which would reproduce the original city-root littering bug one level down.
func TestPoolTriggerWorkDirBareProviderDistinctPerBead(t *testing.T) {
	cityPath := t.TempDir()
	cfgAgent := config.Agent{Name: "codex", Provider: "codex"}
	bp := &agentBuildParams{cityPath: cityPath, cityName: "testcity"}

	first := poolTriggerWorkDir(bp, &cfgAgent, "codex", SessionRequest{WorkBeadID: "gc-485446"})
	second := poolTriggerWorkDir(bp, &cfgAgent, "codex", SessionRequest{WorkBeadID: "gc-485753"})
	if first == "" || second == "" {
		t.Fatalf("expected non-empty workdirs, got %q and %q", first, second)
	}
	if first == second {
		t.Fatalf("two different work beads resolved to the same scratch dir %q", first)
	}
}
