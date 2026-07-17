package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/git"
	"github.com/gastownhall/gascity/internal/runtime/importtrust"
)

// These tests live in package runtime, not in importtrust, because the property
// under test is the composed one: the roots importtrust resolves, run through
// the dialog's own trust decision, determine whether the modal auto-accepts. A
// test that only inspected the trusted-roots slice would have passed while the
// modal still accepted a nested review tree's own instructions. Test files are
// exempt from the stdlib-only boundary that keeps importtrust out of this
// package (see import_boundary_test.go).

// importRootsFor resolves a workspace's trust classification and hands back the
// same policy value the dialog uses, so a test cannot accidentally assert
// against the trusted list alone.
func importRootsFor(ctx context.Context, dir string) importRoots {
	r := importtrust.WorkspaceImportRoots(ctx, dir)
	return importRoots{trusted: r.Trusted, untrusted: r.Untrusted}
}

// initProvenanceRepo returns a base directory and an initialized repository
// inside it, so worktrees can be placed beside the repo the way this fork
// deploys them.
func initProvenanceRepo(t *testing.T) (base, repo string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	base = t.TempDir()
	repo = filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit",
		"--allow-empty", "-q", "-m", "init")
	return base, repo
}

// addWorktree creates a worktree through the provenance front door, which is
// the only supported way to make a trusted one.
func addWorktree(t *testing.T, repo, path string, class git.Provenance) {
	t.Helper()

	if err := git.New(repo).WorktreeAdd(git.WorktreeAddOptions{
		Path:       path,
		Base:       "HEAD",
		Provenance: class,
	}); err != nil {
		t.Fatalf("WorktreeAdd(%q, %s): %v", path, class, err)
	}
}

// TestWorkspaceImportTrustRootsExcludesExternalReviewWorktree pins the bug this
// bead exists for: a worktree staged to review an EXTERNAL ref is still a live
// worktree of this repository, so trusting every live worktree let its
// attacker-authored CLAUDE.md import its own attacker-authored AGENTS.md as
// first-party — auto-accepting the modal in exactly the provenance-review case
// the modal exists for.
func TestWorkspaceImportTrustRootsExcludesExternalReviewWorktree(t *testing.T) {
	t.Parallel()

	base, repo := initProvenanceRepo(t)

	review := filepath.Join(base, "pr-4094-adopt")
	addWorktree(t, repo, review, git.ProvenanceExternalReview)

	roots := importRootsFor(context.Background(), review)

	if containsResolved(t, roots.trusted, review) {
		t.Errorf("WorkspaceImportTrustRoots(%q) = %v, must not trust the external-review worktree itself", review, roots)
	}

	// The concrete import the live modal would list: the reviewed ref's own
	// instruction file, read from a session running inside the review worktree.
	agents := filepath.Join(review, "AGENTS.md")
	if roots.firstParty(agents) {
		t.Errorf("external-review import %q is first-party under roots %v; the modal would auto-accept attacker-authored instructions", agents, roots)
	}
}

// TestWorkspaceImportTrustRootsExcludesNestedExternalReviewWorktree pins the
// case that defeats a trust check built only from directory prefixes: a review
// worktree checked out INSIDE a trusted tree.
//
// Excluding the review tree from the roots is not enough on its own. The main
// tree is a root, the review tree is a subdirectory of it, and "first-party"
// is a path-prefix test — so the reviewed ref's own AGENTS.md still resolves
// inside a trusted root and auto-accepts, with its recorded class never
// consulted. Nesting is a normal layout here: the pack formulas create
// worktrees at `$(pwd)/worktrees/<bead>`, inside whatever tree they run in.
func TestWorkspaceImportTrustRootsExcludesNestedExternalReviewWorktree(t *testing.T) {
	t.Parallel()

	_, repo := initProvenanceRepo(t)

	// The review tree lives under the main tree, which is trusted unconditionally.
	review := filepath.Join(repo, "pr-4094-review")
	addWorktree(t, repo, review, git.ProvenanceExternalReview)

	roots := importRootsFor(context.Background(), review)

	agents := filepath.Join(review, "AGENTS.md")
	if roots.firstParty(agents) {
		t.Errorf("import %q from a nested external-review worktree is first-party under roots %v; "+
			"the reviewed ref's own instructions auto-accept because the tree sits inside a trusted root", agents, roots)
	}

	// The enclosing managed tree's own files must stay first-party: excluding a
	// nested review tree must not disown the tree it sits in.
	if !roots.firstParty(filepath.Join(repo, "AGENTS.md")) {
		t.Error("the main tree's own AGENTS.md must stay first-party")
	}
}

// TestWorkspaceImportTrustRootsIgnoresForgedInTreeMarker pins that the trust
// classification cannot be changed by the candidate ref. A reviewed ref is
// attacker-authored content: if the stamp were read from the checkout, the ref
// would simply carry a stamp claiming it is managed and self-trust again.
func TestWorkspaceImportTrustRootsIgnoresForgedInTreeMarker(t *testing.T) {
	t.Parallel()

	base, repo := initProvenanceRepo(t)

	review := filepath.Join(base, "pr-forged")
	addWorktree(t, repo, review, git.ProvenanceExternalReview)

	// Everywhere in the checkout a stamp could plausibly be looked for.
	forged := []byte(`{"class":"managed"}`)
	for _, name := range []string{
		"gc-provenance.json",
		filepath.Join(".gc", "gc-provenance.json"),
		filepath.Join(".gc", "provenance.json"),
	} {
		path := filepath.Join(review, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", path, err)
		}
		if err := os.WriteFile(path, forged, 0o644); err != nil {
			t.Fatalf("write forged marker %q: %v", path, err)
		}
	}

	roots := importRootsFor(context.Background(), review)
	if containsResolved(t, roots.trusted, review) {
		t.Errorf("WorkspaceImportTrustRoots(%q) = %v, an in-tree marker must not confer trust", review, roots)
	}
	if roots.firstParty(filepath.Join(review, "AGENTS.md")) {
		t.Error("a forged in-tree marker made the reviewed ref's own import first-party")
	}
}

// TestWorkspaceImportTrustRootsRejectsMissingProvenance pins the fail-closed
// default. A worktree created by bare `git worktree add`, bypassing the front
// door, has no recorded provenance — so its classification is unknown and it
// must not be trusted.
func TestWorkspaceImportTrustRootsRejectsMissingProvenance(t *testing.T) {
	t.Parallel()

	base, repo := initProvenanceRepo(t)

	unstamped := filepath.Join(base, "unstamped")
	runGit(t, repo, "worktree", "add", "-q", "--detach", unstamped)

	roots := importRootsFor(context.Background(), unstamped)
	if containsResolved(t, roots.trusted, unstamped) {
		t.Errorf("WorkspaceImportTrustRoots(%q) = %v, unknown provenance must fail closed", unstamped, roots)
	}
	if roots.firstParty(filepath.Join(unstamped, "AGENTS.md")) {
		t.Error("an import under a worktree of unknown provenance is trusted; it must fail closed")
	}

	// The main tree stays trusted: it is the operator's own checkout and the
	// anchor every other trust decision is measured against.
	if !containsResolved(t, roots.trusted, repo) {
		t.Errorf("WorkspaceImportTrustRoots(%q) = %v, want it to include the main tree %q", unstamped, roots, repo)
	}
}
