package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/execview"
)

func mustUTC(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts.UTC()
}

// fileProviderCity sets up a temp dir as a file-provider city and registers
// it as the active --city, restoring globals on cleanup.
func fileProviderCity(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	cityToml := "[workspace]\nname = \"exec-test-city\"\nprefix = \"ga\"\n\n[beads]\nprovider = \"file\"\n"
	if err := os.WriteFile(filepath.Join(dir, "city.toml"), []byte(cityToml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GC_BEADS", "file")
	prev := cityFlag
	cityFlag = dir
	t.Cleanup(func() { cityFlag = prev })
}

func TestCmdBeadsShowExecution_MissingIDAfterResolve(t *testing.T) {
	fileProviderCity(t)
	var stdout, stderr bytes.Buffer
	code := cmdBeadsShowExecution("", "text", &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "missing bead id") {
		t.Errorf("stderr = %q, want missing-bead-id message", stderr.String())
	}
}

func TestCmdBeadsShowExecution_NotFound(t *testing.T) {
	fileProviderCity(t)
	var stdout, stderr bytes.Buffer
	code := cmdBeadsShowExecution("gc-nope", "text", &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "not found") {
		t.Errorf("stderr = %q, want not-found message", stderr.String())
	}
}

func TestCmdBeadsShowExecution_ResolveErrorTakesPrecedenceOverMissingID(t *testing.T) {
	// Regression: a resolve error (remote target without --city-name) must
	// surface even when the id is empty, not be masked by "missing bead id".
	prevURL, prevName := cityURLFlag, cityNameFlag
	cityURLFlag, cityNameFlag = "https://127.0.0.1:1/nope", ""
	t.Cleanup(func() { cityURLFlag, cityNameFlag = prevURL, prevName })

	var stdout, stderr bytes.Buffer
	code := cmdBeadsShowExecution("", "text", &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if strings.Contains(stderr.String(), "missing bead id") {
		t.Errorf("resolve error was masked by missing-bead-id: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "city-name") {
		t.Errorf("stderr = %q, want the resolve error (requires --city-name)", stderr.String())
	}
}

// execTestRepoOnBranch builds a throwaway git repo via the package's existing
// runGitInTest helper (cmd_rig_test.go) rather than a new exec.Command call
// site, per the resource census's untagged-subprocess ratchet.
func execTestRepoOnBranch(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	runGitInTest(t, dir, "init")
	runGitInTest(t, dir, "config", "user.email", "t@t.com")
	runGitInTest(t, dir, "config", "user.name", "T")
	runGitInTest(t, dir, "checkout", "-b", branch)
	runGitInTest(t, dir, "commit", "--allow-empty", "-m", "base")
	return dir
}

func TestGitWorktreeProbe_AbsentAndNonGit(t *testing.T) {
	var p gitWorktreeProbe

	got := p.Probe(filepath.Join(t.TempDir(), "does-not-exist"), "")
	if got.Present {
		t.Errorf("absent dir: Present=true, want false")
	}

	plain := t.TempDir() // exists but is not a git repo
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(plain))
	got = p.Probe(plain, "")
	if !got.Present || got.IsGit {
		t.Errorf("plain dir: got Present=%t IsGit=%t, want Present=true IsGit=false", got.Present, got.IsGit)
	}
}

func TestGitWorktreeProbe_AheadAndReachability(t *testing.T) {
	// A feature branch two commits ahead of the base branch: not reachable from
	// base, commits_ahead=2. The base is resolved from the repo default branch
	// (here "master"), NOT a hardcoded origin/main.
	repo := execTestRepoOnBranch(t, "master")
	runGitInTest(t, repo, "checkout", "-b", "feature")
	runGitInTest(t, repo, "commit", "--allow-empty", "-m", "f1")
	runGitInTest(t, repo, "commit", "--allow-empty", "-m", "f2")

	var p gitWorktreeProbe
	// Explicit base ref resolves; measure against it.
	got := p.Probe(repo, "master")
	if !got.Present || !got.IsGit {
		t.Fatalf("got %+v, want present git repo", got)
	}
	if got.Branch != "feature" {
		t.Errorf("Branch = %q, want feature", got.Branch)
	}
	if got.CommitsAhead != 2 {
		t.Errorf("CommitsAhead = %d, want 2", got.CommitsAhead)
	}
	if got.ReachableFromMain == nil || *got.ReachableFromMain {
		t.Errorf("ReachableFromMain = %v, want non-nil false (feature is ahead of master)", got.ReachableFromMain)
	}
	if got.Head == "" || got.Dirty {
		t.Errorf("Head/Dirty = %q/%t, want non-empty head, clean tree", got.Head, got.Dirty)
	}
	if got.Err != "" {
		t.Errorf("Err = %q, want empty (clean probe)", got.Err)
	}
}

func TestGitWorktreeProbe_EmptyBaseRef_UsesDefaultBranchNotCurrentBranch(t *testing.T) {
	// Regression: with no explicit base ref and no origin remote wired, the base
	// must resolve to the default branch (main), NOT the checked-out feature
	// branch. Measuring a feature branch against itself falsely reports the work
	// as merged (ahead=0, reachable_from_main=true) — the core signal inverted.
	repo := execTestRepoOnBranch(t, "main")
	runGitInTest(t, repo, "checkout", "-b", "feature")
	runGitInTest(t, repo, "commit", "--allow-empty", "-m", "f1")
	runGitInTest(t, repo, "commit", "--allow-empty", "-m", "f2")

	var p gitWorktreeProbe
	got := p.Probe(repo, "") // empty base -> must fall back to "main", not "feature"
	if got.CommitsAhead != 2 {
		t.Errorf("CommitsAhead = %d, want 2 (feature is 2 ahead of main)", got.CommitsAhead)
	}
	if got.ReachableFromMain == nil || *got.ReachableFromMain {
		t.Errorf("ReachableFromMain = %v, want non-nil false (feature not merged into main)", got.ReachableFromMain)
	}
}

func TestGitWorktreeProbe_OnBaseIsReachable(t *testing.T) {
	repo := execTestRepoOnBranch(t, "master")
	var p gitWorktreeProbe
	got := p.Probe(repo, "master")
	if got.CommitsAhead != 0 {
		t.Errorf("CommitsAhead = %d, want 0 (HEAD is master)", got.CommitsAhead)
	}
	if got.ReachableFromMain == nil || !*got.ReachableFromMain {
		t.Errorf("ReachableFromMain = %v, want non-nil true (HEAD == base)", got.ReachableFromMain)
	}
}

func TestGitWorktreeProbe_DirtyTree(t *testing.T) {
	repo := execTestRepoOnBranch(t, "master")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var p gitWorktreeProbe
	if got := p.Probe(repo, "master"); !got.Dirty {
		t.Errorf("Dirty = false, want true (untracked file present)")
	}
}

func TestRenderExecutionText_CompactDiagnostic(t *testing.T) {
	pr := 2
	last := mustUTC(t, "2026-07-22T21:39:09Z")
	proj := execview.Projection{
		WorkBead: execview.WorkBeadView{ID: "gc-im90", Status: "open", Priority: &pr, Title: "feat"},
		Workflows: []execview.WorkflowView{{
			RootID:  "gc-8cs3",
			Formula: "mol-focus-review",
			Status:  "in_progress",
			CurrentStep: &execview.StepView{
				ID: "gc-8b0v", StepRef: "mol-focus-review.focus", Status: "in_progress", Assignee: "polecat-gc-546088",
			},
			RootSession: "polecat-gc-546088",
		}},
		Session:  &execview.SessionView{Name: "polecat-gc-546088", State: "active", LastActive: &last, Live: true},
		Worktree: &execview.WorktreeView{Path: "/wt", Source: "gc.work_dir", Present: true, IsGit: true, Branch: "work/gc-im90", Head: "1d24deb0167451245273fb2e7bd2502df0cb1685", CommitsAhead: 0, ReachableFromMain: boolPtr(true)},
		Warnings: []string{"something ambiguous"},
	}
	var buf bytes.Buffer
	renderExecutionText(proj, &buf)
	out := buf.String()
	for _, want := range []string{
		"work bead gc-im90", "status=open", "priority=2",
		"gc-8cs3", "formula=mol-focus-review",
		"current step: gc-8b0v", "mol-focus-review.focus", "assignee=polecat-gc-546088",
		"session: polecat-gc-546088", "state=active", "(live)",
		"worktree: /wt", "branch=work/gc-im90", "head=1d24deb01674", "reachable_from_main=true",
		"warnings:", "- something ambiguous",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderExecutionText_UnknownAheadAndEmpties(t *testing.T) {
	proj := execview.Projection{
		WorkBead: execview.WorkBeadView{ID: "gc-x", Status: "open"},
		Worktree: &execview.WorktreeView{Path: "/wt", Source: "gc.work_dir", Present: true, IsGit: true, CommitsAhead: -1},
	}
	var buf bytes.Buffer
	renderExecutionText(proj, &buf)
	out := buf.String()
	if !strings.Contains(out, "ahead=unknown") {
		t.Errorf("want ahead=unknown for CommitsAhead=-1; got:\n%s", out)
	}
	if !strings.Contains(out, "workflows: none") || !strings.Contains(out, "session: none") {
		t.Errorf("want none placeholders; got:\n%s", out)
	}
}

func TestRenderExecutionJSON_RoundTrips(t *testing.T) {
	proj := execview.Projection{
		WorkBead: execview.WorkBeadView{ID: "gc-im90", Status: "open"},
		Warnings: []string{"w1"},
	}
	var buf bytes.Buffer
	if code := renderExecutionJSON(proj, &buf, io.Discard); code != 0 {
		t.Fatalf("renderExecutionJSON code = %d, want 0", code)
	}
	out := buf.String()
	if !strings.Contains(out, `"id": "gc-im90"`) || !strings.Contains(out, `"work_bead"`) {
		t.Errorf("json missing expected fields:\n%s", out)
	}
	if !strings.Contains(out, `"warnings"`) {
		t.Errorf("json missing warnings:\n%s", out)
	}
}
