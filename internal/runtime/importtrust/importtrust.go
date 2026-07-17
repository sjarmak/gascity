// Package importtrust decides which working trees of a repository an
// unattended agent may auto-accept external CLAUDE.md/AGENTS.md imports from.
//
// It sits beside the runtime contract package rather than inside it: resolving
// trust means reading worktree provenance through internal/git, and the root
// internal/runtime package is the in-process expression of the Runtime Provider
// Protocol contract, which stays stdlib-only so providers and the conformance
// suite do not drag the SDK with them (RUNTIME-INV-001).
package importtrust

import (
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/git"
)

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

// Roots is the trust classification of a repository's working trees.
//
// Untrusted is not merely "the trees left over": it is load-bearing. Working
// trees nest here — the pack formulas create them at `$(pwd)/worktrees/<bead>`,
// inside whatever tree they run in — and trust is decided by path prefix, so a
// tree checked out inside a trusted tree is covered by the enclosing root
// whatever its own class says. Naming the untrusted trees is what lets the
// caller cut that inheritance.
type Roots struct {
	// Trusted are working trees this orchestration created from a base it
	// controls, plus the main tree.
	Trusted []string
	// Untrusted are working trees of the same repository that are staged to
	// review an outside ref, or carry no readable provenance.
	Untrusted []string
}

// WorkspaceImportRoots classifies every working tree of the repository that
// contains dir: the main tree and each linked worktree registered against the
// same common git directory, split by whether this orchestration can vouch for
// its content. Callers pass Trusted to WithTrustedImportRoots and Untrusted to
// WithUntrustedImportRoots; no trusted root leaves the external-imports modal
// for a human instead of auto-accepting.
//
// The main tree alone is not enough. Worktrees need not live under the
// repository directory — this fork checks them out beside it
// (`<repo>-worktrees/<slot>/...`) and nests one inside another — and a worktree's
// CLAUDE.md imports that worktree's own AGENTS.md. Trusting only the main tree
// leaves such an import outside the boundary, so the modal is never
// auto-accepted and an unattended pool worker wedges at the startup prompt with
// work it never begins.
//
// But "every live worktree" is too much. A worktree staged to review an
// external ref (an incoming PR head) is also a live worktree of this
// repository, so trusting the set wholesale let a reviewed ref's CLAUDE.md
// import its own attacker-authored AGENTS.md as first-party — auto-accepting
// the modal in exactly the provenance-review case it exists for. The two cases
// are indistinguishable from git: same repository, and neither path shape nor
// current ref is evidence. Only the class recorded when the tree was created
// separates them, so a tree carrying no class is not trusted (see
// git.WorktreeAdd). Imports that escape every root, or descend through
// repository metadata, still fail closed in the caller's own first-party check.
func WorkspaceImportRoots(ctx context.Context, dir string) Roots {
	common := gitCommonDir(ctx, dir)
	if common == "" {
		return Roots{} // not a git repository: trust nothing
	}
	main := filepath.Dir(common)
	roots := Roots{Trusted: []string{main}}
	seen := map[string]bool{main: true}
	var unclassified []string

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
		// Drop a listed path that no longer resolves to this repository. Git
		// keeps listing a worktree whose directory was removed by anything
		// other than `git worktree remove`/`prune`, and this fork reaps
		// worktree directories exactly that way, so a listed path may hold no
		// working tree at all.
		//
		// This check is weaker than it looks and is NOT what keeps a reaped
		// slot out: anyone able to write the path can restore this answer with
		// a one-line `gitdir:` pointer at the orphaned admin directory, and git
		// then reports the replant as the genuine worktree (gc-1fbg). Since a
		// pool worker executes model-generated code, that is inside the threat
		// model. Closing it needs teardown to deregister and revoke, which is
		// gc-1fbg's subsystem, not a sharper read of the candidate's metadata.
		if gitCommonDir(ctx, path) != common {
			continue
		}
		seen[path] = true
		trusted, err := worktreeTrusted(ctx, path)
		switch {
		case err != nil:
			// No class on record: untrusted, and an operator-visible gap.
			roots.Untrusted = append(roots.Untrusted, denySpellings(path)...)
			unclassified = append(unclassified, path)
		case !trusted:
			// Recorded external-review: untrusted by design, nothing to report.
			roots.Untrusted = append(roots.Untrusted, denySpellings(path)...)
		default:
			roots.Trusted = append(roots.Trusted, path)
		}
	}
	if len(unclassified) > 0 {
		// Fail closed, but say so: a worktree created before this front door
		// existed, or by bare `git worktree add`, silently loses trust and the
		// only symptom is an unattended worker sitting at the import modal.
		slog.Warn("worktrees have no readable provenance and are not trusted for external instruction-file imports; classify them with `gc worktree provenance set <path> --class managed`",
			"count", len(unclassified),
			"worktrees", unclassified)
	}
	return roots
}

// denySpellings returns the names an untrusted tree must be refused under: the
// path as git reports it, and its symlink-resolved form when they differ.
//
// The two lists are deliberately asymmetric. A trusted root is matched by one
// spelling, but a denial that misses is a tree silently regaining trust, and
// the import path being compared is rendered elsewhere and may already be
// resolved (on macOS /tmp and /var are symlinks). Naming both spellings costs a
// slice entry and removes the question.
func denySpellings(path string) []string {
	if resolved := resolvePath(path); resolved != path {
		return []string{path, resolved}
	}
	return []string{path}
}

// worktreeTrusted reports whether the linked worktree at path is one this
// orchestration created from a base it controls. The error is returned rather
// than folded into false so the caller can tell "recorded as external-review",
// which is a normal and correct exclusion, from "no class on record", which is
// an operator-visible gap.
func worktreeTrusted(ctx context.Context, path string) (bool, error) {
	p, err := git.New(path).ReadWorktreeProvenanceCtx(ctx)
	if err != nil {
		return false, err
	}
	return p.Class.Trusted(), nil
}
