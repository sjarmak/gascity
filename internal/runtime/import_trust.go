package runtime

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorkspaceImportTrustRoot returns the root of the git repository that contains
// dir, to be used as the trusted boundary for external CLAUDE.md imports with
// WithTrustedImportRoot. It resolves the common git directory (so a linked
// worktree under `<repo>/.gc/worktrees/<id>` maps back to `<repo>`, the main
// working tree that holds the repository's own AGENTS.md), then returns that
// tree's root.
//
// It returns "" when dir is empty or is not inside a git repository. Callers
// pass the result straight to WithTrustedImportRoot, so an empty result simply
// leaves the external-imports modal for a human instead of auto-accepting.
func WorkspaceImportTrustRoot(ctx context.Context, dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return ""
	}
	// --git-common-dir is absolute for linked worktrees and may be relative
	// (e.g. ".git") for the main tree; resolve it against dir before taking the
	// parent so the repository root is absolute.
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	return filepath.Dir(filepath.Clean(common))
}

// WorkspaceImportTrustRoots returns every working tree of the repository that
// contains dir: the main tree (WorkspaceImportTrustRoot) plus each linked
// worktree registered against the same common git directory. Callers pass the
// result to WithTrustedImportRoots; an empty result leaves the external-imports
// modal for a human instead of auto-accepting.
//
// The main tree alone is not enough. Worktrees need not live under the
// repository directory — this fork checks them out beside it
// (`<repo>-worktrees/<slot>/...`) and nests one inside another — and a worktree's
// CLAUDE.md imports that worktree's own AGENTS.md. Trusting only the main tree
// leaves such an import outside the boundary, so the modal is never
// auto-accepted and an unattended pool worker wedges at the startup prompt with
// work it never begins. Every working tree of the repository holds the same
// first-party tracked content, so each is a legitimate root; imports that escape
// all of them, or descend through repository metadata, still fail closed
// (see importPathFirstParty).
func WorkspaceImportTrustRoots(ctx context.Context, dir string) []string {
	main := WorkspaceImportTrustRoot(ctx, dir)
	if main == "" {
		return nil // not a git repository: trust nothing
	}
	roots := []string{main}
	seen := map[string]bool{main: true}

	// --git-common-dir already proved dir is in a repository, so a failure here
	// is a degraded git; fall back to the main tree rather than trusting nothing.
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return roots
	}
	for _, line := range strings.Split(string(out), "\n") {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !ok {
			continue
		}
		path = filepath.Clean(strings.TrimSpace(path))
		if !filepath.IsAbs(path) || seen[path] {
			continue
		}
		seen[path] = true
		roots = append(roots, path)
	}
	return roots
}
