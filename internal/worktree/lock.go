package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/gastownhall/gascity/internal/git"
)

// pathLock serializes provisioning operations on one workspace path across
// processes.
//
// Cleanup's safety is a check-then-act sequence: it verifies registration,
// ownership, cleanliness, reachability, and merge state, and only then removes.
// Without a lock, another process can remove that worktree and provision a new
// one at the same path in between, and the removal then lands on a workspace
// none of the checks ever examined. Ensure has the mirror-image race, where two
// callers both observe a missing path and one loses the creation.
type pathLock struct{ f *os.File }

// lockPath acquires an exclusive advisory lock for the given workspace path
// within the given repository. The returned lock must be released with unlock.
//
// The lock file lives under the repository's common git dir rather than beside
// the workspace, for two reasons. It must outlive the removal it guards, so it
// cannot live inside the workspace; and it must not litter the workspace root,
// which callers list and expect to hold only workspaces. The common dir is also
// the correct scope: every worktree of one repository shares it, so two
// processes contending for the same path always agree on the same lock file.
func lockPath(repoDir, path string) (*pathLock, error) {
	lockFile, err := lockFilePath(repoDir, path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(lockFile), 0o755); err != nil {
		return nil, fmt.Errorf("preparing worktree lock dir for %q: %w", path, err)
	}
	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening worktree lock for %q: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("locking worktree %q: %w", path, err)
	}
	return &pathLock{f: f}, nil
}

// lockFilePath derives the lock file for a workspace path. The name is a digest
// of the absolute path rather than the path itself, so that no workspace name
// can produce an invalid or colliding file name.
func lockFilePath(repoDir, path string) (string, error) {
	common, err := git.New(repoDir).CommonDir()
	if err != nil {
		return "", fmt.Errorf("locating worktree lock dir for %q: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving worktree path %q for locking: %w", path, err)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return filepath.Join(common, "gc-worktree-locks", hex.EncodeToString(sum[:])+".lock"), nil
}

// unlock releases the lock. It is safe to call on a nil lock so callers can
// defer it unconditionally.
func (l *pathLock) unlock() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}
