package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/fsys"
)

// Provenance classifies how a working tree came to exist.
//
// Trust decisions about external instruction-file imports turn on this and
// cannot be derived without it. A tree this orchestration checked out from a
// base it controls holds first-party content; a tree staged to review a ref
// from outside holds content nobody has vetted yet. Both are ordinary live
// worktrees of the same repository, so `git worktree list` cannot tell them
// apart, and neither can the path or the currently checked-out ref — a review
// tree may sit anywhere and hold any ref. The classification is therefore
// recorded when the tree is created and read back later.
type Provenance string

const (
	// ProvenanceManaged marks a worktree this orchestration created from a base
	// it controls. Its tracked content is first-party.
	ProvenanceManaged Provenance = "managed"

	// ProvenanceExternalReview marks a worktree staged to review a ref from
	// outside this orchestration, such as an incoming pull request head.
	// Treating such a tree as first-party is what lets a reviewed ref
	// auto-accept its own instruction files, which is the case the
	// external-imports modal exists to put in front of a human.
	ProvenanceExternalReview Provenance = "external-review"
)

// Valid reports whether p is a class this build understands. An unrecognized
// class is not a trusted class.
func (p Provenance) Valid() bool {
	return p == ProvenanceManaged || p == ProvenanceExternalReview
}

// Trusted reports whether content under a worktree of this class may be
// auto-accepted as first-party.
func (p Provenance) Trusted() bool { return p == ProvenanceManaged }

// provenanceFile names the stamp inside a worktree's git admin directory.
//
// The location carries the security property. The admin directory
// (`<common-dir>/worktrees/<name>`) is outside the checkout, so a ref cannot
// carry a stamp for the tree it is checked out into — git refuses to write
// tracked paths under `.git`, and a linked worktree's own `.git` is a file
// pointing here.
//
// That location defends against the ref, and only against the ref.
//
// `git worktree remove` discards the stamp along with the admin directory, but
// this fork also reaps worktrees by deleting the directory outright, which
// leaves both the registration and the stamp behind. A stale stamp is a live
// liability rather than a leftover: anything able to write the reaped path can
// drop a one-line `gitdir:` pointer back at the orphaned admin directory, and
// the stamp then vouches for a directory this orchestration never created
// (gc-1fbg). Nothing here closes that. Git reports such a replant as the
// genuine worktree, so no metadata read from the candidate distinguishes it —
// teardown has to deregister and revoke the stamp before the path can be
// reused. Until it does, a stamp means "created here", not "still ours".
const provenanceFile = "gc-provenance.json"

// ErrNoProvenance reports that a working tree carries no classification. This
// is not a defaultable condition: an unclassified tree is one this orchestration
// cannot vouch for, so callers fail closed.
var ErrNoProvenance = errors.New("worktree has no recorded provenance")

// WorktreeProvenance is the recorded classification of one working tree.
type WorktreeProvenance struct {
	Class Provenance `json:"class"`
	// Base is the ref the tree was created from, recorded for diagnostics. It
	// is not consulted for trust: the class is the decision.
	Base string `json:"base,omitempty"`
	// Note records why a class was assigned, used by the migration path to
	// carry the operator's evidence for a tree created before this front door
	// existed.
	Note string `json:"note,omitempty"`
}

// WorktreeAddOptions describes a working tree to create through the provenance
// front door.
type WorktreeAddOptions struct {
	// Path is the directory to create the working tree at.
	Path string
	// Base is the ref to check out. Empty checks out HEAD.
	Base string
	// Branch, when set, creates that branch at Base. Otherwise the tree is
	// checked out detached.
	Branch string
	// Provenance classifies the tree. It is required and has no default:
	// picking one for the caller is what this bug was.
	Provenance Provenance
	// Note records the evidence behind the classification.
	Note string
}

// stampWorktree records provenance for a freshly created working tree.
//
// It is a variable so tests can force a failure between the create and the
// stamp. That window is the whole reason WorktreeAdd exists as one call, and it
// cannot be provoked from outside: git chooses the admin directory itself, so
// there is no moment a test can make the write fail without also breaking the
// create it is supposed to follow.
var stampWorktree = func(ctx context.Context, path string, p WorktreeProvenance) error {
	return New(path).WriteWorktreeProvenanceCtx(ctx, p)
}

// WorktreeAdd creates a working tree and records its provenance as one
// operation.
//
// The two halves are a single front door because they must not drift. A tree
// that exists without a class is untrusted, so a caller that could create first
// and stamp second could — by crashing, erroring, or simply not calling the
// second half — leave a tree that silently loses trust. Creation therefore
// either yields a classified tree or no tree at all.
func (g *Git) WorktreeAdd(opts WorktreeAddOptions) error {
	return g.WorktreeAddCtx(context.Background(), opts)
}

// WorktreeAddCtx is like WorktreeAdd but accepts a context.
func (g *Git) WorktreeAddCtx(ctx context.Context, opts WorktreeAddOptions) error {
	if strings.TrimSpace(opts.Path) == "" {
		return errors.New("worktree path is required")
	}
	if !opts.Provenance.Valid() {
		return fmt.Errorf("worktree provenance %q is not a recognized class: pass %q for a tree created from a base this orchestration controls, or %q for one staged to review an outside ref",
			opts.Provenance, ProvenanceManaged, ProvenanceExternalReview)
	}

	args := []string{"worktree", "add"}
	if opts.Branch != "" {
		args = append(args, "-b", opts.Branch)
	} else {
		args = append(args, "--detach")
	}
	// Terminate options before the positionals: a ref may legally begin with
	// "-", and this front door stages refs from outside, so git would otherwise
	// parse an incoming branch name as a switch (cf. clone.go).
	args = append(args, "--", opts.Path)
	if opts.Base != "" {
		args = append(args, opts.Base)
	}
	if _, err := g.runCtx(ctx, args...); err != nil {
		return fmt.Errorf("adding worktree %q: %w", opts.Path, err)
	}

	stamp := WorktreeProvenance{Class: opts.Provenance, Base: opts.Base, Note: opts.Note}
	if err := stampWorktree(ctx, opts.Path, stamp); err != nil {
		// Roll back rather than hand back a tree that would quietly fail every
		// later trust check with no record of why.
		if rmErr := g.WorktreeRemove(opts.Path, true); rmErr != nil {
			return fmt.Errorf("recording provenance for %q: %w (removing the unclassified worktree also failed: %w)", opts.Path, err, rmErr)
		}
		return fmt.Errorf("recording provenance for %q: %w", opts.Path, err)
	}
	return nil
}

// WorktreeAdminDir returns the absolute git admin directory for this working
// tree: `<common-dir>/worktrees/<name>` for a linked worktree, and the common
// directory itself for the main tree.
func (g *Git) WorktreeAdminDir() (string, error) {
	return g.WorktreeAdminDirCtx(context.Background())
}

// WorktreeAdminDirCtx is like WorktreeAdminDir but accepts a context.
func (g *Git) WorktreeAdminDirCtx(ctx context.Context) (string, error) {
	out, err := g.runPathCtx(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("resolving git admin directory: %w", err)
	}
	dir := strings.TrimSpace(out)
	if dir == "" {
		return "", errors.New("resolving git admin directory: git reported no path")
	}
	return dir, nil
}

// commonDirCtx returns the absolute common git directory shared by every
// working tree of this repository.
func (g *Git) commonDirCtx(ctx context.Context) (string, error) {
	out, err := g.runPathCtx(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolving git common directory: %w", err)
	}
	common := strings.TrimSpace(out)
	if common == "" {
		return "", errors.New("resolving git common directory: git reported no path")
	}
	// --git-common-dir is absolute for linked worktrees but may be relative
	// (".git") for the main tree, where it is relative to the working dir.
	if !filepath.IsAbs(common) {
		common = filepath.Join(g.workDir, common)
	}
	return common, nil
}

// IsMainWorktree reports whether this working tree is the repository's main
// tree. The main tree is the only one whose admin directory is the common
// directory itself; every linked worktree gets its own subdirectory under it.
func (g *Git) IsMainWorktree() (bool, error) {
	return g.IsMainWorktreeCtx(context.Background())
}

// IsMainWorktreeCtx is like IsMainWorktree but accepts a context.
func (g *Git) IsMainWorktreeCtx(ctx context.Context) (bool, error) {
	admin, err := g.WorktreeAdminDirCtx(ctx)
	if err != nil {
		return false, err
	}
	common, err := g.commonDirCtx(ctx)
	if err != nil {
		return false, err
	}
	return canonicalWorktreePath(admin) == canonicalWorktreePath(common), nil
}

// ReadWorktreeProvenance returns the recorded classification of this working
// tree, or ErrNoProvenance when it carries none.
func (g *Git) ReadWorktreeProvenance() (WorktreeProvenance, error) {
	return g.ReadWorktreeProvenanceCtx(context.Background())
}

// ReadWorktreeProvenanceCtx is like ReadWorktreeProvenance but accepts a
// context. Every failure — unreadable, unparseable, or holding a class this
// build does not know — is returned as an error rather than a default class, so
// callers cannot accidentally trust a tree whose stamp they could not read.
func (g *Git) ReadWorktreeProvenanceCtx(ctx context.Context) (WorktreeProvenance, error) {
	admin, err := g.WorktreeAdminDirCtx(ctx)
	if err != nil {
		return WorktreeProvenance{}, err
	}
	data, err := os.ReadFile(filepath.Join(admin, provenanceFile))
	if errors.Is(err, os.ErrNotExist) {
		return WorktreeProvenance{}, ErrNoProvenance
	}
	if err != nil {
		return WorktreeProvenance{}, fmt.Errorf("reading worktree provenance: %w", err)
	}
	var p WorktreeProvenance
	if err := json.Unmarshal(data, &p); err != nil {
		return WorktreeProvenance{}, fmt.Errorf("parsing worktree provenance: %w", err)
	}
	if !p.Class.Valid() {
		return WorktreeProvenance{}, fmt.Errorf("worktree provenance %q is not a recognized class", p.Class)
	}
	return p, nil
}

// WriteWorktreeProvenance records the classification of this working tree.
func (g *Git) WriteWorktreeProvenance(p WorktreeProvenance) error {
	return g.WriteWorktreeProvenanceCtx(context.Background(), p)
}

// WriteWorktreeProvenanceCtx is like WriteWorktreeProvenance but accepts a
// context.
func (g *Git) WriteWorktreeProvenanceCtx(ctx context.Context, p WorktreeProvenance) error {
	if !p.Class.Valid() {
		return fmt.Errorf("worktree provenance %q is not a recognized class", p.Class)
	}
	admin, err := g.WorktreeAdminDirCtx(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encoding worktree provenance: %w", err)
	}
	if err := fsys.WriteFileAtomic(fsys.OSFS{}, filepath.Join(admin, provenanceFile), data, 0o644); err != nil {
		return fmt.Errorf("writing worktree provenance: %w", err)
	}
	return nil
}
