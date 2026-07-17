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
	common := gitCommonDir(ctx, dir)
	if common == "" {
		return ""
	}
	return filepath.Dir(common)
}

// gitCommonDir returns the absolute common git directory shared by every working
// tree of the repository that contains dir, or "" when dir is empty or is not
// inside a git repository. Two directories belong to the same repository exactly
// when this agrees, which is what makes it the identity check for a trust root.
func gitCommonDir(ctx context.Context, dir string) string {
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
	return resolvePath(common)
}

// resolvePath resolves symlinks in path so two names for the same directory
// compare equal, falling back to a lexical clean when the path does not exist.
func resolvePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
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
	common := gitCommonDir(ctx, dir)
	if common == "" {
		return nil // not a git repository: trust nothing
	}
	main := filepath.Dir(common)
	roots := []string{main}
	seen := map[string]bool{main: true}

	// --git-common-dir already proved dir is in a repository, so a failure here
	// is a degraded git; fall back to the main tree rather than trusting nothing.
	//
	// -z is load-bearing, not a detail: a path may legally contain a newline, and
	// the plain porcelain form does not escape it, so the tail of such a path
	// renders as its own line and parses as a `worktree <path>` record git never
	// registered. NUL framing keeps every path in exactly one field, so one
	// ordinary `git worktree add` cannot fabricate a trust root.
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "worktree", "list", "--porcelain", "-z").Output()
	if err != nil {
		return roots
	}
	for _, field := range strings.Split(string(out), "\x00") {
		path, ok := strings.CutPrefix(field, "worktree ")
		if !ok {
			continue
		}
		path = filepath.Clean(path)
		if !filepath.IsAbs(path) || seen[path] {
			continue
		}
		// Trust the working tree, not the registration. Git keeps listing a
		// worktree whose directory was removed by anything other than
		// `git worktree remove`/`prune`, and this fork reaps worktree
		// directories exactly that way — so a listed path may hold no working
		// tree at all, or hold whatever someone since put there. Only a path
		// that still resolves to this same repository is first-party.
		if gitCommonDir(ctx, path) != common {
			continue
		}
		seen[path] = true
		roots = append(roots, path)
	}
	return roots
}
