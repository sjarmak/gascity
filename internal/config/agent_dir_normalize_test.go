package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/fsys"
)

// loadCityWithRigDirAgent writes a minimal city with one rig ("decisions")
// whose path is rigPath and one pool worker agent whose dir is agentDir,
// then loads it through the full compose path.
func loadCityWithRigDirAgent(t *testing.T, rigPath, agentDir string) *City {
	t.Helper()
	cityDir := t.TempDir()
	writeTestFile(t, cityDir, "city.toml", `
[workspace]
name = "test"

[[rigs]]
name = "decisions"
path = "`+rigPath+`"

[[agent]]
name = "decisions-worker"
dir = "`+agentDir+`"
max_active_sessions = 3
`)
	cfg, _, err := LoadWithIncludes(fsys.OSFS{}, filepath.Join(cityDir, "city.toml"))
	if err != nil {
		t.Fatalf("LoadWithIncludes: %v", err)
	}
	return cfg
}

func findDecisionsWorker(t *testing.T, cfg *City) *Agent {
	t.Helper()
	for i := range cfg.Agents {
		if cfg.Agents[i].Name == "decisions-worker" {
			return &cfg.Agents[i]
		}
	}
	t.Fatalf("agent decisions-worker not found in %d loaded agents", len(cfg.Agents))
	return nil
}

// TestLoadNormalizesAbsRigDirToRigName pins the dec-a5ar mint-site fix: an
// agent whose dir is the ABSOLUTE path of a configured rig has its Dir
// rewritten to the rig NAME at config finalize, so every derived identity
// (QualifiedName, QualifiedInstanceName, and through them alias, agent_name,
// GC_AGENT/GC_ALIAS/GC_TEMPLATE, beacon, gc.routed_to) is rig-qualified
// instead of path-shaped.
func TestLoadNormalizesAbsRigDirToRigName(t *testing.T) {
	rigPath := t.TempDir()
	cfg := loadCityWithRigDirAgent(t, rigPath, rigPath)

	a := findDecisionsWorker(t, cfg)
	if a.Dir != "decisions" {
		t.Errorf("Agent.Dir = %q, want %q (abs rig path normalized to rig name)", a.Dir, "decisions")
	}
	if got, want := a.QualifiedName(), "decisions/decisions-worker"; got != want {
		t.Errorf("QualifiedName() = %q, want %q", got, want)
	}
	if got, want := a.QualifiedInstanceName("decisions-worker-1"), "decisions/decisions-worker-1"; got != want {
		t.Errorf("QualifiedInstanceName(decisions-worker-1) = %q, want %q", got, want)
	}
}

// TestLoadNormalizesAbsRigDirBoundViaSiteToml pins the compose ordering: rig
// paths bound only through .gc/site.toml (the schema-2 shape) are overlaid by
// ApplySiteBindings, and the normalize pass must run after that overlay.
func TestLoadNormalizesAbsRigDirBoundViaSiteToml(t *testing.T) {
	rigPath := t.TempDir()
	cityDir := t.TempDir()
	writeTestFile(t, cityDir, "city.toml", `
[workspace]
name = "test"

[[rigs]]
name = "decisions"

[[agent]]
name = "decisions-worker"
dir = "`+rigPath+`"
max_active_sessions = 3
`)
	writeTestFile(t, filepath.Join(cityDir, ".gc"), "site.toml", `
[[rig]]
name = "decisions"
path = "`+rigPath+`"
`)
	cfg, _, err := LoadWithIncludes(fsys.OSFS{}, filepath.Join(cityDir, "city.toml"))
	if err != nil {
		t.Fatalf("LoadWithIncludes: %v", err)
	}
	a := findDecisionsWorker(t, cfg)
	if a.Dir != "decisions" {
		t.Errorf("Agent.Dir = %q, want %q (normalize must run after ApplySiteBindings)", a.Dir, "decisions")
	}
}

// TestLoadNormalizesRigDirSpelledWithTrailingSlash covers the trailing-slash
// spelling of the same rig path.
func TestLoadNormalizesRigDirSpelledWithTrailingSlash(t *testing.T) {
	rigPath := t.TempDir()
	cfg := loadCityWithRigDirAgent(t, rigPath, rigPath+string(filepath.Separator))

	a := findDecisionsWorker(t, cfg)
	if a.Dir != "decisions" {
		t.Errorf("Agent.Dir = %q, want %q (trailing-slash rig path normalized)", a.Dir, "decisions")
	}
}

// TestLoadNormalizesRigDirSpelledThroughSymlink covers a symlinked spelling of
// the rig path: SamePath resolves symlinks, so the agent still associates and
// the identity still normalizes.
func TestLoadNormalizesRigDirSpelledThroughSymlink(t *testing.T) {
	base := t.TempDir()
	rigPath := filepath.Join(base, "rig-real")
	if err := os.Mkdir(rigPath, 0o755); err != nil {
		t.Fatalf("mkdir rig: %v", err)
	}
	link := filepath.Join(base, "rig-link")
	if err := os.Symlink(rigPath, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cfg := loadCityWithRigDirAgent(t, rigPath, link)

	a := findDecisionsWorker(t, cfg)
	if a.Dir != "decisions" {
		t.Errorf("Agent.Dir = %q, want %q (symlinked rig path normalized)", a.Dir, "decisions")
	}
}

// TestLoadLeavesNonRigAbsDirsUntouched pins the no-false-rewrite edge: an
// absolute dir that is a SUBpath of a rig, or unrelated to every rig, must
// stay exactly as configured.
func TestLoadLeavesNonRigAbsDirsUntouched(t *testing.T) {
	rigPath := t.TempDir()
	sub := filepath.Join(rigPath, "nested")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	unrelated := t.TempDir()

	for name, dir := range map[string]string{
		"rig subpath":   sub,
		"unrelated abs": unrelated,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := loadCityWithRigDirAgent(t, rigPath, dir)
			a := findDecisionsWorker(t, cfg)
			if a.Dir != dir {
				t.Errorf("Agent.Dir = %q, want %q (non-rig abs dir must not be rewritten)", a.Dir, dir)
			}
		})
	}
}

// TestLoadLeavesRigNameDirUntouched pins that the canonical rig-name form is
// a no-op through the normalize pass.
func TestLoadLeavesRigNameDirUntouched(t *testing.T) {
	rigPath := t.TempDir()
	cfg := loadCityWithRigDirAgent(t, rigPath, "decisions")

	a := findDecisionsWorker(t, cfg)
	if a.Dir != "decisions" {
		t.Errorf("Agent.Dir = %q, want %q", a.Dir, "decisions")
	}
}

// TestNormalizeAgentRigDirsSkipsCollidingRewrite pins the collision guard: a
// rewrite that would land on another agent's (dir, name) key is skipped, so
// the pass never mints duplicate identities.
func TestNormalizeAgentRigDirsSkipsCollidingRewrite(t *testing.T) {
	rigPath := t.TempDir()
	cfg := &City{
		Rigs: []Rig{{Name: "decisions", Path: rigPath}},
		Agents: []Agent{
			{Name: "claude", Dir: rigPath},
			{Name: "claude", Dir: "decisions"},
		},
	}
	NormalizeAgentRigDirs(cfg, t.TempDir())

	if cfg.Agents[0].Dir != rigPath {
		t.Errorf("colliding agent Dir = %q, want %q (rewrite skipped)", cfg.Agents[0].Dir, rigPath)
	}
	if cfg.Agents[1].Dir != "decisions" {
		t.Errorf("existing rig-name agent Dir = %q, want decisions (untouched)", cfg.Agents[1].Dir)
	}
}

// TestNormalizeAgentRigDirsResolvesRelativeRigPath pins that a rig declared
// with a city-relative path still matches an agent dir spelled absolutely.
func TestNormalizeAgentRigDirsResolvesRelativeRigPath(t *testing.T) {
	cityRoot := t.TempDir()
	rigAbs := filepath.Join(cityRoot, "rigs", "decisions")
	if err := os.MkdirAll(rigAbs, 0o755); err != nil {
		t.Fatalf("mkdir rig: %v", err)
	}
	cfg := &City{
		Rigs:   []Rig{{Name: "decisions", Path: filepath.Join("rigs", "decisions")}},
		Agents: []Agent{{Name: "decisions-worker", Dir: rigAbs}},
	}
	NormalizeAgentRigDirs(cfg, cityRoot)

	if cfg.Agents[0].Dir != "decisions" {
		t.Errorf("Agent.Dir = %q, want decisions (relative rig path resolved against cityRoot)", cfg.Agents[0].Dir)
	}
}

// TestNormalizeAgentRigDirsSkipsBlankRigs pins that rigs with an empty name
// or empty path never match.
func TestNormalizeAgentRigDirsSkipsBlankRigs(t *testing.T) {
	dir := t.TempDir()
	cfg := &City{
		Rigs:   []Rig{{Name: "", Path: dir}, {Name: "ghost", Path: ""}},
		Agents: []Agent{{Name: "worker", Dir: dir}},
	}
	NormalizeAgentRigDirs(cfg, t.TempDir())

	if cfg.Agents[0].Dir != dir {
		t.Errorf("Agent.Dir = %q, want %q (blank rigs must not match)", cfg.Agents[0].Dir, dir)
	}
}
