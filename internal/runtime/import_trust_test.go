package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorkspaceImportTrustRoot(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit",
		"--allow-empty", "-q", "-m", "init")

	// A linked worktree under the repo must resolve back to the main repo root,
	// so an import of the repo-root AGENTS.md (seen as external from the
	// worktree subdirectory) is recognized as first-party.
	wtParent := filepath.Join(repo, ".gc", "worktrees")
	if err := os.MkdirAll(wtParent, 0o755); err != nil {
		t.Fatalf("mkdir worktrees: %v", err)
	}
	wt := filepath.Join(wtParent, "wt")
	runGit(t, repo, "worktree", "add", "-q", "--detach", wt)

	wantRoot := evalSymlinks(t, repo)

	for _, dir := range []string{repo, wt} {
		if got := evalSymlinks(t, WorkspaceImportTrustRoot(context.Background(), dir)); got != wantRoot {
			t.Errorf("WorkspaceImportTrustRoot(%q) = %q, want repo root %q", dir, got, wantRoot)
		}
	}

	if got := WorkspaceImportTrustRoot(context.Background(), t.TempDir()); got != "" {
		t.Errorf("WorkspaceImportTrustRoot(non-git dir) = %q, want empty", got)
	}
	if got := WorkspaceImportTrustRoot(context.Background(), ""); got != "" {
		t.Errorf("WorkspaceImportTrustRoot(empty) = %q, want empty", got)
	}
}

// TestWorkspaceImportTrustRootsCoversSiblingWorktrees pins the layout that
// wedged live pool workers on 2026-07-16: the worker runs in a linked worktree
// that lives OUTSIDE the main repository directory, and the instruction file it
// imports belongs to another working tree of the same repository. Trusting only
// the main tree leaves that import untrusted, so the external-imports modal is
// never auto-accepted and the unattended worker sits at the prompt forever.
func TestWorkspaceImportTrustRootsCoversSiblingWorktrees(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit",
		"--allow-empty", "-q", "-m", "init")

	// The deployed shape: worktrees live beside the repo, not under it, and one
	// worktree nests inside another (`<base>/wt/worktrees/nested`).
	outer := filepath.Join(base, "wt")
	runGit(t, repo, "worktree", "add", "-q", "--detach", outer)
	nested := filepath.Join(outer, "worktrees", "nested")
	runGit(t, repo, "worktree", "add", "-q", "--detach", nested)

	roots := WorkspaceImportTrustRoots(context.Background(), nested)
	for _, want := range []string{repo, outer, nested} {
		if !containsResolved(t, roots, want) {
			t.Errorf("WorkspaceImportTrustRoots(%q) = %v, want it to include %q", nested, roots, want)
		}
	}

	// The outer worktree's AGENTS.md is what the live modal listed as external.
	agents := filepath.Join(outer, "AGENTS.md")
	if !anyRootFirstParty(agents, roots) {
		t.Errorf("import %q not first-party under roots %v; the live modal would wedge the worker", agents, roots)
	}

	// A path outside every working tree stays untrusted so a human decides.
	if anyRootFirstParty(filepath.Join(base, "elsewhere", "AGENTS.md"), roots) {
		t.Error("a path outside every working tree must not be trusted")
	}

	if got := WorkspaceImportTrustRoots(context.Background(), t.TempDir()); len(got) != 0 {
		t.Errorf("WorkspaceImportTrustRoots(non-git dir) = %v, want none", got)
	}
	if got := WorkspaceImportTrustRoots(context.Background(), ""); len(got) != 0 {
		t.Errorf("WorkspaceImportTrustRoots(empty) = %v, want none", got)
	}
}

// TestWorkspaceImportTrustRootsRejectsFabricatedRecord pins the porcelain parse
// against a worktree path that contains a literal newline. A newline is legal in
// a path, and `git worktree list --porcelain` does not escape it, so the tail of
// such a path renders on its own line and reads as a second `worktree <path>`
// record that git never registered. Splitting that output on newlines lets one
// ordinary `git worktree add` fabricate an arbitrary trust root — repo-wide and
// for every later session, since the registration persists until it is pruned.
func TestWorkspaceImportTrustRootsRejectsFabricatedRecord(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit",
		"--allow-empty", "-q", "-m", "init")

	forged := filepath.Join(base, "forged")
	runGit(t, repo, "worktree", "add", "-q", "--detach",
		filepath.Join(base, "evil")+"\nworktree "+forged)

	roots := WorkspaceImportTrustRoots(context.Background(), repo)
	for _, root := range roots {
		if filepath.Clean(root) == filepath.Clean(forged) {
			t.Errorf("WorkspaceImportTrustRoots = %v, must not include fabricated root %q", roots, forged)
		}
	}
	if anyRootFirstParty(filepath.Join(forged, "AGENTS.md"), roots) {
		t.Errorf("import under fabricated root %q is trusted; roots=%v", forged, roots)
	}
}

// TestWorkspaceImportTrustRootsRejectsStaleRegistration pins that a worktree
// path git still lists, but which no longer holds a working tree, is not a trust
// root. Git only deregisters a worktree on `git worktree remove`/`prune`, and
// this fork reaps worktree directories by other means, so stale registrations are
// routine. Trusting the bare path would let anyone able to write to a reaped slot
// plant an instruction file that an unattended worker auto-imports.
func TestWorkspaceImportTrustRootsRejectsStaleRegistration(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit",
		"--allow-empty", "-q", "-m", "init")

	reaped := filepath.Join(base, "reaped")
	runGit(t, repo, "worktree", "add", "-q", "--detach", reaped)
	// Removed the way a reaper removes it: the directory goes, the registration stays.
	if err := os.RemoveAll(reaped); err != nil {
		t.Fatalf("remove worktree dir: %v", err)
	}
	// Someone re-creates the path and plants an instruction file in the slot.
	if err := os.MkdirAll(reaped, 0o755); err != nil {
		t.Fatalf("replant slot: %v", err)
	}
	planted := filepath.Join(reaped, "AGENTS.md")
	if err := os.WriteFile(planted, []byte("planted instructions"), 0o644); err != nil {
		t.Fatalf("write planted file: %v", err)
	}

	roots := WorkspaceImportTrustRoots(context.Background(), repo)
	if anyRootFirstParty(planted, roots) {
		t.Errorf("planted import %q under stale registration is trusted; roots=%v", planted, roots)
	}
}

func containsResolved(t *testing.T, roots []string, want string) bool {
	t.Helper()
	wantResolved := evalSymlinks(t, want)
	for _, root := range roots {
		if evalSymlinks(t, root) == wantResolved {
			return true
		}
	}
	return false
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	if path == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}
