package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

// legacyRigPathCfg builds a normalized-config city: rig "decisions" bound to a
// real absolute path, pool agent "decisions-worker" carrying the canonical
// rig-name Dir. Persisted identities from BEFORE the dec-a5ar normalization
// still spell the dir segment as the absolute rig path; those must keep
// resolving to the same agent (read compatibility), while only the rig-name
// form is minted on write.
func legacyRigPathCfg(t *testing.T) (*config.City, string) {
	t.Helper()
	rigRoot := t.TempDir()
	cfg := &config.City{
		Rigs:   []config.Rig{{Name: "decisions", Path: rigRoot}},
		Agents: []config.Agent{poolAgent("decisions-worker", "decisions", intPtr(3), 0)},
	}
	return cfg, rigRoot
}

// TestFindAgentByTemplateResolvesLegacyRigPathForm is the migration guard for
// persisted identities written before the dec-a5ar Dir normalization:
// "<abs rig path>/decisions-worker" (in gc.routed_to, assignees, session-bead
// agent_name/alias) must resolve to the same configured agent as the
// normalized "decisions/decisions-worker" form.
func TestFindAgentByTemplateResolvesLegacyRigPathForm(t *testing.T) {
	cfg, rigRoot := legacyRigPathCfg(t)

	legacy := rigRoot + "/decisions-worker"
	agent := findAgentByTemplate(cfg, legacy)
	if agent == nil {
		t.Fatalf("findAgentByTemplate(%q) = nil, want decisions-worker", legacy)
	}
	if got, want := agent.QualifiedName(), "decisions/decisions-worker"; got != want {
		t.Errorf("resolved agent = %q, want %q", got, want)
	}
}

// TestFindAgentByTemplateResolvesLegacyRigPathSymlinkSpelling covers a
// persisted identity whose dir segment is a symlinked spelling of the rig
// path.
func TestFindAgentByTemplateResolvesLegacyRigPathSymlinkSpelling(t *testing.T) {
	cfg, rigRoot := legacyRigPathCfg(t)
	link := filepath.Join(t.TempDir(), "rig-link")
	if err := os.Symlink(rigRoot, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	agent := findAgentByTemplate(cfg, link+"/decisions-worker")
	if agent == nil || agent.QualifiedName() != "decisions/decisions-worker" {
		t.Fatalf("findAgentByTemplate(symlink spelling) = %v, want decisions-worker", agent)
	}
}

// TestFindAgentByTemplateRejectsNonRigAbsForm pins that the fallback does not
// over-match: an absolute dir segment that matches no configured rig resolves
// to nothing.
func TestFindAgentByTemplateRejectsNonRigAbsForm(t *testing.T) {
	cfg, _ := legacyRigPathCfg(t)
	other := t.TempDir()

	if agent := findAgentByTemplate(cfg, other+"/decisions-worker"); agent != nil {
		t.Fatalf("findAgentByTemplate(non-rig abs form) = %q, want nil", agent.QualifiedName())
	}
}

// TestNormalizeAgentTemplateIdentityMapsLegacyRigPathToCanonical pins the
// normalization funnel the reconciler's migration passes ride: the legacy
// absolute form maps to the canonical rig-name identity, and both forms
// compare as equivalent.
func TestNormalizeAgentTemplateIdentityMapsLegacyRigPathToCanonical(t *testing.T) {
	cfg, rigRoot := legacyRigPathCfg(t)

	legacy := rigRoot + "/decisions-worker"
	const canonical = "decisions/decisions-worker"
	if got := normalizeAgentTemplateIdentity(cfg, legacy); got != canonical {
		t.Errorf("normalizeAgentTemplateIdentity(%q) = %q, want %q", legacy, got, canonical)
	}
	if !agentTemplateIdentitiesEquivalent(cfg, legacy, canonical) {
		t.Errorf("agentTemplateIdentitiesEquivalent(%q, %q) = false, want true", legacy, canonical)
	}
}

// TestLegacyRigPathTemplateIdentitySkipsRelativeRigPaths pins the fail-closed
// edge: a rig whose configured path was not resolved to absolute cannot be
// compared, so the rewrite must not fire.
func TestLegacyRigPathTemplateIdentitySkipsRelativeRigPaths(t *testing.T) {
	cfg := &config.City{
		Rigs:   []config.Rig{{Name: "decisions", Path: "rigs/decisions"}},
		Agents: []config.Agent{poolAgent("decisions-worker", "decisions", intPtr(3), 0)},
	}
	if got := legacyRigPathTemplateIdentity(cfg, "/somewhere/rigs/decisions/decisions-worker"); got != "" {
		t.Errorf("legacyRigPathTemplateIdentity with relative rig path = %q, want \"\"", got)
	}
}
