package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
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

func execTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Strip inherited git env so the temp repo is not confused with a parent.
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		switch k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_CONFIG":
			continue
		}
		cmd.Env = append(cmd.Env, e)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
	}
}

func execTestRepoOnBranch(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	execTestGit(t, dir, "init")
	execTestGit(t, dir, "config", "user.email", "t@t.com")
	execTestGit(t, dir, "config", "user.name", "T")
	execTestGit(t, dir, "checkout", "-b", branch)
	execTestGit(t, dir, "commit", "--allow-empty", "-m", "base")
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
	execTestGit(t, repo, "checkout", "-b", "feature")
	execTestGit(t, repo, "commit", "--allow-empty", "-m", "f1")
	execTestGit(t, repo, "commit", "--allow-empty", "-m", "f2")

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
	if got.ReachableFromMain {
		t.Errorf("ReachableFromMain = true, want false (feature is ahead of master)")
	}
	if got.Head == "" || got.Dirty {
		t.Errorf("Head/Dirty = %q/%t, want non-empty head, clean tree", got.Head, got.Dirty)
	}
}

func TestGitWorktreeProbe_OnBaseIsReachable(t *testing.T) {
	repo := execTestRepoOnBranch(t, "master")
	var p gitWorktreeProbe
	got := p.Probe(repo, "master")
	if got.CommitsAhead != 0 {
		t.Errorf("CommitsAhead = %d, want 0 (HEAD is master)", got.CommitsAhead)
	}
	if !got.ReachableFromMain {
		t.Errorf("ReachableFromMain = false, want true (HEAD == base)")
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
		Worktree: &execview.WorktreeView{Path: "/wt", Source: "gc.work_dir", Present: true, IsGit: true, Branch: "work/gc-im90", Head: "1d24deb0167451245273fb2e7bd2502df0cb1685", CommitsAhead: 0, ReachableFromMain: true},
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
