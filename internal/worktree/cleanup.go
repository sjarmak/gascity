package worktree

import (
	"errors"
	"fmt"
	"os"

	"github.com/gastownhall/gascity/internal/git"
)

// CleanupSpec describes the workspace worktree to tear down. It names the
// same three identity fields Verify uses, because cleanup must prove the
// path is the worktree it was asked to remove before removing anything.
type CleanupSpec struct {
	// RepoDir is the repository the worktree must belong to. Absolute.
	RepoDir string
	// Path is the worktree root to remove. Absolute.
	Path string
	// Branch must be the branch checked out at Path.
	Branch string
}

// CleanupReport describes what cleanup actually did, which is not always
// what it was asked to do. Removed and AlreadyAbsent are distinct successes:
// the caller records a disposition from them, and conflating them would
// assert a removal this run did not perform.
type CleanupReport struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	// Removed reports that this call removed the worktree.
	Removed bool `json:"removed"`
	// AlreadyAbsent reports that the path was gone before this call ran.
	AlreadyAbsent bool `json:"already_absent"`
	// CleanupPending reports that the worktree is still on disk. It is the
	// inverse of a completed teardown and always false on success.
	CleanupPending bool `json:"cleanup_pending"`
}

// RefusalError is a safety gate declining to remove a worktree. It is a
// result, not a malfunction: the worktree holds state the gate is unwilling
// to destroy, and Code names which gate refused so the caller can record an
// actionable retention rather than an unexplained one.
type RefusalError struct {
	Code    string
	Message string
}

// Error implements error.
func (e *RefusalError) Error() string { return e.Code + ": " + e.Message }

func refuse(code, format string, args ...any) error {
	return &RefusalError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Cleanup removes the workspace worktree at spec.Path after proving the path
// is that worktree and that no gate holds work removal would destroy.
//
// There is deliberately no force mode. A gate that can be overridden by a
// flag is a gate the next caller passes the flag to; the recovery path for a
// refusal is a human or the provisioning owner deciding the held work no
// longer matters, not a stronger command.
//
// Gate order is fixed and cheapest-identity-first: an absent path is a
// success (nothing to do), a path that is not the named worktree is refused
// before any state probe runs, and only then are the work-loss gates
// evaluated against the worktree itself.
func Cleanup(spec CleanupSpec) (CleanupReport, error) {
	vspec := Spec{RepoDir: spec.RepoDir, Path: spec.Path, Branch: spec.Branch}
	if err := vspec.validate(); err != nil {
		return CleanupReport{}, err
	}
	rep := CleanupReport{Path: spec.Path, Branch: spec.Branch}

	if _, err := os.Stat(spec.Path); err != nil {
		if os.IsNotExist(err) {
			rep.AlreadyAbsent = true
			return rep, nil
		}
		rep.CleanupPending = true
		return rep, fmt.Errorf("cleaning worktree %q: %w", spec.Path, err)
	}

	if _, err := Verify(vspec); err != nil {
		rep.CleanupPending = true
		if errors.Is(err, ErrWorktreeMissing) {
			rep.AlreadyAbsent = true
			rep.CleanupPending = false
			return rep, nil
		}
		return rep, refuse("not_the_named_worktree", "%v", err)
	}

	wtGit := git.New(spec.Path)
	if wtGit.HasUncommittedWork() {
		rep.CleanupPending = true
		return rep, refuse("uncommitted_work", "worktree %s has uncommitted changes", spec.Path)
	}
	stashed, err := wtGit.HasStashesResult()
	if err != nil {
		rep.CleanupPending = true
		return rep, refuse("stash_probe_failed", "%v", err)
	}
	if stashed {
		rep.CleanupPending = true
		return rep, refuse("stashed_work", "worktree %s has stashed changes", spec.Path)
	}
	unpushed, err := wtGit.HasUnpushedCommitsResult()
	if err != nil {
		rep.CleanupPending = true
		return rep, refuse("unpushed_probe_failed", "%v", err)
	}
	if unpushed {
		rep.CleanupPending = true
		return rep, refuse("unpushed_commits", "branch %s has commits no remote reaches", spec.Branch)
	}

	repoGit := git.New(spec.RepoDir)
	if err := repoGit.WorktreeRemove(spec.Path, false); err != nil {
		rep.CleanupPending = true
		return rep, refuse("remove_failed", "%v", err)
	}
	if err := repoGit.WorktreePrune(); err != nil {
		// The checkout is gone; a stale admin record is a bookkeeping
		// defect, not a retained workspace. Report the removal that
		// happened and surface the prune failure to the caller.
		rep.Removed = true
		return rep, fmt.Errorf("worktree %q removed but pruning stale records failed: %w", spec.Path, err)
	}
	rep.Removed = true
	return rep, nil
}
