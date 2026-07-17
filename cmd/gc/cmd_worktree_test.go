package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/git"
)

// runWorktreeCmd executes `gc worktree ...` with cwd set to repo.
func runWorktreeCmd(t *testing.T, repo string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	t.Chdir(repo)
	var outBuf, errBuf bytes.Buffer
	cmd := newWorktreeCmd(&outBuf, &errBuf)
	cmd.SetArgs(args)
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func initWorktreeCmdRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

func TestWorktreeCreateRecordsProvenance(t *testing.T) {
	repo := initWorktreeCmdRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")

	stdout, stderr, err := runWorktreeCmd(t, repo, "create", wt, "--base", "HEAD", "--class", "managed")
	if err != nil {
		t.Fatalf("create: %v stderr=%q", err, stderr)
	}
	if !strings.Contains(stdout, "managed") {
		t.Errorf("stdout = %q, want it to report the recorded class", stdout)
	}

	got, err := git.New(wt).ReadWorktreeProvenance()
	if err != nil {
		t.Fatalf("ReadWorktreeProvenance: %v", err)
	}
	if got.Class != git.ProvenanceManaged {
		t.Errorf("Class = %q, want %q", got.Class, git.ProvenanceManaged)
	}
}

// TestWorktreeCreateRequiresClass pins that the front door cannot be used
// without classifying the tree: a pack that forgets --class gets an error, not
// a default.
func TestWorktreeCreateRequiresClass(t *testing.T) {
	repo := initWorktreeCmdRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")

	if _, _, err := runWorktreeCmd(t, repo, "create", wt, "--base", "HEAD"); err == nil {
		t.Fatal("create without --class succeeded, want error")
	}
	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Errorf("worktree %q was created despite the rejected class", wt)
	}
}

// TestWorktreeRemoveRevokesProvenanceAndDeregisters pins gc-1fbg's front door
// end to end through the CLI surface the pack formulas actually call: after
// `gc worktree remove`, neither the checkout nor the admin directory (and the
// stamp inside it) survive, so a later replant at the same path has nothing
// to forge a trust root out of.
func TestWorktreeRemoveRevokesProvenanceAndDeregisters(t *testing.T) {
	repo := initWorktreeCmdRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")

	if _, stderr, err := runWorktreeCmd(t, repo, "create", wt, "--base", "HEAD", "--class", "managed"); err != nil {
		t.Fatalf("create: %v stderr=%q", err, stderr)
	}
	admin, err := git.New(wt).WorktreeAdminDir()
	if err != nil {
		t.Fatalf("WorktreeAdminDir: %v", err)
	}

	stdout, stderr, err := runWorktreeCmd(t, repo, "remove", wt, "--force")
	if err != nil {
		t.Fatalf("remove: %v stderr=%q", err, stderr)
	}
	if !strings.Contains(stdout, wt) {
		t.Errorf("stdout = %q, want it to report the removed path", stdout)
	}

	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Errorf("checkout %q survived gc worktree remove (stat err=%v)", wt, statErr)
	}
	if _, statErr := os.Stat(admin); !os.IsNotExist(statErr) {
		t.Errorf("admin dir %q survived gc worktree remove (stat err=%v); its stamp is still reachable for a replant", admin, statErr)
	}
}

func TestWorktreeProvenanceListClassifies(t *testing.T) {
	repo := initWorktreeCmdRepo(t)
	base := t.TempDir()

	managed := filepath.Join(base, "managed")
	review := filepath.Join(base, "review")
	legacy := filepath.Join(base, "legacy")
	if _, _, err := runWorktreeCmd(t, repo, "create", managed, "--base", "HEAD", "--class", "managed"); err != nil {
		t.Fatalf("create managed: %v", err)
	}
	if _, _, err := runWorktreeCmd(t, repo, "create", review, "--base", "HEAD", "--class", "external-review"); err != nil {
		t.Fatalf("create review: %v", err)
	}
	// A worktree from before the front door existed.
	c := exec.Command("git", "worktree", "add", "-q", "--detach", legacy)
	c.Dir = repo
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("bare worktree add: %v\n%s", err, out)
	}

	stdout, stderr, err := runWorktreeCmd(t, repo, "provenance", "list")
	if err != nil {
		t.Fatalf("provenance list: %v stderr=%q", err, stderr)
	}
	for _, want := range []string{"main", "managed", "external-review", "unclassified"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("provenance list output %q missing %q", stdout, want)
		}
	}
}

// TestWorktreeProvenanceSetRequiresNote pins that backfilling trust onto an
// existing tree records why. Marking a tree managed tells every later
// unattended session it may auto-accept that tree's instruction files, so the
// evidence belongs on the record rather than in someone's memory.
func TestWorktreeProvenanceSetRequiresNote(t *testing.T) {
	repo := initWorktreeCmdRepo(t)
	legacy := filepath.Join(t.TempDir(), "legacy")

	c := exec.Command("git", "worktree", "add", "-q", "--detach", legacy)
	c.Dir = repo
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("bare worktree add: %v\n%s", err, out)
	}

	if _, _, err := runWorktreeCmd(t, repo, "provenance", "set", legacy, "--class", "managed"); err == nil {
		t.Fatal("provenance set without --note succeeded, want error")
	}

	if _, _, err := runWorktreeCmd(t, repo, "provenance", "set", legacy,
		"--class", "managed", "--note", "created by the pool formula on 2026-07-16"); err != nil {
		t.Fatalf("provenance set: %v", err)
	}
	got, err := git.New(legacy).ReadWorktreeProvenance()
	if err != nil {
		t.Fatalf("ReadWorktreeProvenance: %v", err)
	}
	if got.Class != git.ProvenanceManaged || got.Note == "" {
		t.Errorf("provenance = %+v, want managed with the evidence recorded", got)
	}
}

// TestWorktreeProvenanceSetRefusesReclassify pins that the backfill command
// cannot promote a tree that was already classified. It is a guard rail rather
// than a security boundary — the stamp is a plain file — but it keeps the one
// obvious misuse, turning a review tree into a first-party one, off the
// supported path.
func TestWorktreeProvenanceSetRefusesReclassify(t *testing.T) {
	repo := initWorktreeCmdRepo(t)
	review := filepath.Join(t.TempDir(), "review")

	if _, _, err := runWorktreeCmd(t, repo, "create", review, "--base", "HEAD", "--class", "external-review"); err != nil {
		t.Fatalf("create review: %v", err)
	}

	_, stderr, err := runWorktreeCmd(t, repo, "provenance", "set", review,
		"--class", "managed", "--note", "trust me")
	if err == nil {
		t.Fatal("provenance set promoted an external-review worktree, want refusal")
	}
	if !strings.Contains(stderr, "already recorded") {
		t.Errorf("stderr = %q, want it to say the class is already recorded", stderr)
	}

	got, provErr := git.New(review).ReadWorktreeProvenance()
	if provErr != nil {
		t.Fatalf("ReadWorktreeProvenance: %v", provErr)
	}
	if got.Class != git.ProvenanceExternalReview {
		t.Errorf("Class = %q, want it unchanged at %q", got.Class, git.ProvenanceExternalReview)
	}
}
