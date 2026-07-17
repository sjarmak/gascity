package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/git"
)

// TestWorkspaceImportRootsAnchorsOnMainTree pins that every working tree of a
// repository resolves back to the same main tree: a linked worktree under
// `<repo>/.gc/worktrees/<id>` maps to `<repo>`, the tree holding the
// repository's own AGENTS.md, which a session inside the worktree sees as an
// external import. Outside a repository nothing is trusted at all.
func TestWorkspaceImportRootsAnchorsOnMainTree(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit",
		"--allow-empty", "-q", "-m", "init")

	wtParent := filepath.Join(repo, ".gc", "worktrees")
	if err := os.MkdirAll(wtParent, 0o755); err != nil {
		t.Fatalf("mkdir worktrees: %v", err)
	}
	wt := filepath.Join(wtParent, "wt")
	runGit(t, repo, "worktree", "add", "-q", "--detach", wt)

	for _, dir := range []string{repo, wt} {
		roots := importRootsFor(context.Background(), dir)
		if !containsResolved(t, roots.trusted, repo) {
			t.Errorf("import roots from %q = %v, want the main tree %q", dir, roots.trusted, repo)
		}
	}

	for _, dir := range []string{t.TempDir(), ""} {
		roots := importRootsFor(context.Background(), dir)
		if len(roots.trusted) != 0 || len(roots.untrusted) != 0 {
			t.Errorf("import roots from non-repository %q = %+v, want none", dir, roots)
		}
	}
}

// TestWorkspaceImportTrustRootsCoversSiblingWorktrees pins the layout that
// wedged live pool workers on 2026-07-16: the worker runs in a linked worktree
// that lives OUTSIDE the main repository directory, and the instruction file it
// imports belongs to another working tree of the same repository. Trusting only
// the main tree leaves that import untrusted, so the external-imports modal is
// never auto-accepted and the unattended worker sits at the prompt forever.
//
// The trees are created through the provenance front door because that is now
// what separates them from a tree staged to review an outside ref; the wedge
// must stay fixed for the managed case without trusting the review case.
func TestWorkspaceImportTrustRootsCoversSiblingWorktrees(t *testing.T) {
	t.Parallel()

	base, repo := initProvenanceRepo(t)

	// The deployed shape: worktrees live beside the repo, not under it, and one
	// worktree nests inside another (`<base>/wt/worktrees/nested`).
	outer := filepath.Join(base, "wt")
	addWorktree(t, repo, outer, git.ProvenanceManaged)
	nested := filepath.Join(outer, "worktrees", "nested")
	addWorktree(t, repo, nested, git.ProvenanceManaged)

	roots := importRootsFor(context.Background(), nested)
	for _, want := range []string{repo, outer, nested} {
		if !containsResolved(t, roots.trusted, want) {
			t.Errorf("WorkspaceImportTrustRoots(%q) = %v, want it to include %q", nested, roots, want)
		}
	}

	// The outer worktree's AGENTS.md is what the live modal listed as external.
	agents := filepath.Join(outer, "AGENTS.md")
	if !roots.firstParty(agents) {
		t.Errorf("import %q not first-party under roots %v; the live modal would wedge the worker", agents, roots)
	}

	// A path outside every working tree stays untrusted so a human decides.
	if roots.firstParty(filepath.Join(base, "elsewhere", "AGENTS.md")) {
		t.Error("a path outside every working tree must not be trusted")
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

	roots := importRootsFor(context.Background(), repo)
	for _, root := range roots.trusted {
		if filepath.Clean(root) == filepath.Clean(forged) {
			t.Errorf("WorkspaceImportTrustRoots = %v, must not include fabricated root %q", roots, forged)
		}
	}
	if roots.firstParty(filepath.Join(forged, "AGENTS.md")) {
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

	roots := importRootsFor(context.Background(), repo)
	if roots.firstParty(planted) {
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
