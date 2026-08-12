package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCleanupRefusesStaleAttemptAgainstReprovisionedWorktree is the regression
// test for ownership that identified the slot rather than the occupant.
//
// Bead, owner, and generation are all reproduced exactly by re-provisioning the
// same bead at the same path, so a cleanup request built from a finished attempt
// matched a live successor and removed somebody else's workspace. Binding the
// request to the attempt id the creating Ensure returned is what makes the two
// distinguishable.
func TestCleanupRefusesStaleAttemptAgainstReprovisionedWorktree(t *testing.T) {
	repo, base := initTestRepo(t)
	root := t.TempDir()
	wt := filepath.Join(root, "gc-slot")
	spec := managedSpec(repo, root, wt, "work/gc-slot", base)

	first, err := Ensure(spec)
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	stale := spec
	stale.AttemptID = first.Provenance.AttemptID
	if _, err := Cleanup(stale); err != nil {
		t.Fatalf("Cleanup of the attempt that created the worktree: %v", err)
	}

	// The same bead is re-provisioned at the same path: identical spec, so
	// identical bead, owner, and generation. Only the attempt id differs.
	second, err := Ensure(spec)
	if err != nil {
		t.Fatalf("re-provisioning Ensure: %v", err)
	}
	if second.Provenance.AttemptID == stale.AttemptID {
		t.Fatalf("re-provisioning reused attempt id %q, so the two workspaces are indistinguishable",
			stale.AttemptID)
	}

	report, cleanupErr := Cleanup(stale)
	if cleanupErr == nil {
		t.Fatal("Cleanup with a finished attempt id removed a re-provisioned workspace, want refusal")
	}
	if !report.CleanupPending || report.Error == nil || report.Error.Code != CleanupErrorOwnership {
		t.Fatalf("Cleanup report = %+v, want structured ownership refusal", report)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("Cleanup removed the re-provisioned worktree: %v", statErr)
	}

	// The live attempt is still authorized, so the binding narrows the request
	// rather than wedging the path.
	live := spec
	live.AttemptID = second.Provenance.AttemptID
	if _, err := Cleanup(live); err != nil {
		t.Fatalf("Cleanup with the live attempt id: %v", err)
	}
}

// TestPathLockSerializesWorkspaceOperations proves the lock is mutually
// exclusive, which is what closes the window between Cleanup's checks and its
// removal.
//
// The assertion is ordering, not timing: while the lock is held, a second
// acquisition must not have completed; once released, it must complete. A
// second acquirer that has not yet been scheduled also reads as "not
// completed", so the test cannot fail spuriously.
func TestPathLockSerializesWorkspaceOperations(t *testing.T) {
	repo, _ := initTestRepo(t)
	path := filepath.Join(t.TempDir(), "gc-locked")

	held, err := lockPath(repo, path)
	if err != nil {
		t.Fatalf("lockPath: %v", err)
	}

	acquired := make(chan *pathLock, 1)
	go func() {
		second, lockErr := lockPath(repo, path)
		if lockErr != nil {
			close(acquired)
			return
		}
		acquired <- second
	}()

	select {
	case second := <-acquired:
		if second != nil {
			second.unlock()
		}
		t.Fatal("a second lockPath succeeded while the first was held")
	default:
	}

	held.unlock()

	second, ok := <-acquired
	if !ok {
		t.Fatal("second lockPath failed after the first was released")
	}
	second.unlock()
}

// TestCleanupDoesNotLeaveALockFileBehind keeps the serialization lock out of
// the workspace root. The lock has to outlive the removal it guards, so it
// cannot live inside the workspace, and nothing deletes it afterwards; parking
// it beside the workspace would leave a permanent file per provisioned bead in
// a directory callers list expecting only workspaces.
func TestCleanupDoesNotLeaveALockFileBehind(t *testing.T) {
	repo, base := initTestRepo(t)
	root := t.TempDir()
	wt := filepath.Join(root, "gc-lockfile")
	spec := managedSpec(repo, root, wt, "work/gc-lockfile", base)

	rep, err := Ensure(spec)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	spec.AttemptID = rep.Provenance.AttemptID
	if _, err := Cleanup(spec); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		t.Errorf("root retains %q after ensure+cleanup, want an empty root", entry.Name())
	}
}
