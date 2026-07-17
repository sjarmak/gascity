package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeAddRecordsProvenance(t *testing.T) {
	repo := initTestRepo(t)

	for _, tc := range []struct {
		name  string
		class Provenance
	}{
		{"managed", ProvenanceManaged},
		{"external review", ProvenanceExternalReview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wt := filepath.Join(t.TempDir(), "wt")
			g := New(repo)
			if err := g.WorktreeAdd(WorktreeAddOptions{
				Path:       wt,
				Base:       "HEAD",
				Provenance: tc.class,
			}); err != nil {
				t.Fatalf("WorktreeAdd: %v", err)
			}

			got, err := New(wt).ReadWorktreeProvenance()
			if err != nil {
				t.Fatalf("ReadWorktreeProvenance: %v", err)
			}
			if got.Class != tc.class {
				t.Errorf("Class = %q, want %q", got.Class, tc.class)
			}
			if got.Base != "HEAD" {
				t.Errorf("Base = %q, want %q", got.Base, "HEAD")
			}
		})
	}
}

// TestWorktreeAddRequiresProvenance pins the front door's fail-closed contract:
// a caller that does not classify the worktree gets an error and no worktree at
// all, rather than an unclassified tree that later trust checks must guess about.
func TestWorktreeAddRequiresProvenance(t *testing.T) {
	repo := initTestRepo(t)

	for _, tc := range []struct {
		name  string
		class Provenance
	}{
		{"empty", ""},
		{"unrecognized", Provenance("trusted")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wt := filepath.Join(t.TempDir(), "wt")
			err := New(repo).WorktreeAdd(WorktreeAddOptions{
				Path:       wt,
				Base:       "HEAD",
				Provenance: tc.class,
			})
			if err == nil {
				t.Fatal("WorktreeAdd with unclassified provenance succeeded, want error")
			}
			if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
				t.Errorf("worktree %q exists after a rejected add; the create must not be half-applied", wt)
			}
		})
	}
}

// TestWorktreeAddAcceptsDashLeadingRefs pins the option terminator. A ref may
// legally begin with "-", and this front door is what stages refs from outside
// this orchestration — precisely where an odd name is most likely to turn up.
// Without a "--" before the positionals, git parses the ref as a switch.
func TestWorktreeAddAcceptsDashLeadingRefs(t *testing.T) {
	repo := initTestRepo(t)
	runGit(t, repo, "update-ref", "refs/heads/-evil", "HEAD")

	wt := filepath.Join(t.TempDir(), "wt")
	if err := New(repo).WorktreeAdd(WorktreeAddOptions{
		Path:       wt,
		Base:       "-evil",
		Provenance: ProvenanceExternalReview,
	}); err != nil {
		t.Fatalf("WorktreeAdd with a dash-leading base: %v", err)
	}
	got, err := New(wt).ReadWorktreeProvenance()
	if err != nil {
		t.Fatalf("ReadWorktreeProvenance: %v", err)
	}
	if got.Class != ProvenanceExternalReview {
		t.Errorf("Class = %q, want %q", got.Class, ProvenanceExternalReview)
	}
}

// TestWorktreeAddRollsBackWhenStampFails pins the atomicity claim: a caller
// never receives a worktree that exists but carries no class. An unclassified
// tree is untrusted, so leaving one behind would wedge its worker with no
// record of why.
func TestWorktreeAddRollsBackWhenStampFails(t *testing.T) {
	repo := initTestRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")

	stampErr := errors.New("no space left on device")
	original := stampWorktree
	t.Cleanup(func() { stampWorktree = original })
	stampWorktree = func(context.Context, string, WorktreeProvenance) error { return stampErr }

	err := New(repo).WorktreeAdd(WorktreeAddOptions{Path: wt, Base: "HEAD", Provenance: ProvenanceManaged})
	if !errors.Is(err, stampErr) {
		t.Fatalf("WorktreeAdd err = %v, want it to wrap the stamp failure", err)
	}
	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Errorf("worktree %q survived a failed stamp; an unclassified tree must be rolled back", wt)
	}

	// The registration must go too: git keeps listing a worktree whose directory
	// merely vanished, and a listed path is what trust resolution walks.
	worktrees, err := New(repo).WorktreeList()
	if err != nil {
		t.Fatalf("WorktreeList: %v", err)
	}
	for _, w := range worktrees {
		if filepath.Clean(w.Path) == filepath.Clean(wt) {
			t.Errorf("worktree %q is still registered after rollback: %v", wt, worktrees)
		}
	}
}

// TestWorktreeProvenanceStoredOutsideCheckout pins AC#3: the classification
// lives in the repository's admin area, not in the worktree's tracked content,
// so a candidate ref cannot carry its own stamp and self-trust.
func TestWorktreeProvenanceStoredOutsideCheckout(t *testing.T) {
	repo := initTestRepo(t)

	wt := filepath.Join(t.TempDir(), "wt")
	if err := New(repo).WorktreeAdd(WorktreeAddOptions{
		Path:       wt,
		Base:       "HEAD",
		Provenance: ProvenanceManaged,
	}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	// Nothing the checkout can express may hold the stamp.
	err := filepath.WalkDir(wt, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == provenanceFile {
			t.Errorf("provenance stamp found inside the checkout at %q", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk checkout: %v", err)
	}

	// And it is readable from the admin directory.
	if _, err := New(wt).ReadWorktreeProvenance(); err != nil {
		t.Fatalf("ReadWorktreeProvenance: %v", err)
	}
}

// TestReadWorktreeProvenanceMissing pins that a worktree created by bare
// `git worktree add` reports unknown provenance rather than a default class.
func TestReadWorktreeProvenanceMissing(t *testing.T) {
	repo := initTestRepo(t)

	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", "-q", "--detach", wt)

	if _, err := New(wt).ReadWorktreeProvenance(); !errors.Is(err, ErrNoProvenance) {
		t.Fatalf("ReadWorktreeProvenance err = %v, want ErrNoProvenance", err)
	}
}

// TestReadWorktreeProvenanceRejectsUnrecognizedClass pins that a stamp holding
// a class this build does not know fails closed instead of being trusted.
func TestReadWorktreeProvenanceRejectsUnrecognizedClass(t *testing.T) {
	repo := initTestRepo(t)

	wt := filepath.Join(t.TempDir(), "wt")
	if err := New(repo).WorktreeAdd(WorktreeAddOptions{
		Path:       wt,
		Base:       "HEAD",
		Provenance: ProvenanceManaged,
	}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	admin, err := New(wt).WorktreeAdminDir()
	if err != nil {
		t.Fatalf("WorktreeAdminDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(admin, provenanceFile), []byte(`{"class":"totally-trusted"}`), 0o644); err != nil {
		t.Fatalf("rewrite stamp: %v", err)
	}

	if _, err := New(wt).ReadWorktreeProvenance(); err == nil {
		t.Fatal("ReadWorktreeProvenance accepted an unrecognized class, want error")
	}
}

// TestWorktreeRemoveAndRevokeDeletesStampAndDeregisters pins the front door's
// two effects together: the stamp is gone, and so is git's own registration —
// a replant later has neither a readable provenance file nor a listed
// worktree entry to inherit.
func TestWorktreeRemoveAndRevokeDeletesStampAndDeregisters(t *testing.T) {
	repo := initTestRepo(t)

	wt := filepath.Join(t.TempDir(), "wt")
	if err := New(repo).WorktreeAdd(WorktreeAddOptions{
		Path:       wt,
		Base:       "HEAD",
		Provenance: ProvenanceManaged,
	}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	admin, err := New(wt).WorktreeAdminDir()
	if err != nil {
		t.Fatalf("WorktreeAdminDir: %v", err)
	}

	if err := New(repo).WorktreeRemoveAndRevoke(wt, true); err != nil {
		t.Fatalf("WorktreeRemoveAndRevoke: %v", err)
	}

	if _, err := os.Stat(admin); !os.IsNotExist(err) {
		t.Errorf("admin dir %q survived WorktreeRemoveAndRevoke (stat err=%v)", admin, err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("checkout %q survived WorktreeRemoveAndRevoke (stat err=%v)", wt, err)
	}

	worktrees, err := New(repo).WorktreeList()
	if err != nil {
		t.Fatalf("WorktreeList: %v", err)
	}
	for _, w := range worktrees {
		if filepath.Clean(w.Path) == filepath.Clean(wt) {
			t.Errorf("worktree %q is still registered after WorktreeRemoveAndRevoke: %v", wt, worktrees)
		}
	}
}

// TestWorktreeRemoveAndRevokeNonForcedRefusalLeavesStampIntact pins a
// regression a security review caught in this same change: a non-forced
// WorktreeRemoveAndRevoke must not revoke a live worktree's stamp when git
// refuses the removal (uncommitted/untracked content). reapClosedBeadWorktrees
// is the one production caller that passes force=false — it already verified
// the tree was clean before calling this, but relies on git's own refusal as
// a second gate against a race (a concurrent write landing between that check
// and this call). Revoking unconditionally before the git call would strip a
// perfectly healthy, still-in-use worktree of its trust on every such race,
// with no way back except a manual `gc worktree provenance set` — turning an
// expected, retry-next-cycle no-op into a standing availability regression.
func TestWorktreeRemoveAndRevokeNonForcedRefusalLeavesStampIntact(t *testing.T) {
	repo := initTestRepo(t)

	wt := filepath.Join(t.TempDir(), "wt")
	if err := New(repo).WorktreeAdd(WorktreeAddOptions{
		Path:       wt,
		Base:       "HEAD",
		Provenance: ProvenanceManaged,
	}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	// Simulate the race: something writes to the tree after the caller's own
	// safety checks passed but before this call runs.
	if err := os.WriteFile(filepath.Join(wt, "untracked.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	if err := New(repo).WorktreeRemoveAndRevoke(wt, false); err == nil {
		t.Fatal("WorktreeRemoveAndRevoke(force=false) on a dirty tree succeeded, want a refusal")
	}

	if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree %q was removed despite the refusal: %v", wt, err)
	}
	got, err := New(wt).ReadWorktreeProvenance()
	if err != nil {
		t.Fatalf("ReadWorktreeProvenance after a refused non-forced removal: %v — the stamp must survive a refusal", err)
	}
	if got.Class != ProvenanceManaged {
		t.Errorf("Class after refused removal = %q, want %q (unchanged)", got.Class, ProvenanceManaged)
	}
}

// TestRevokeWorktreeProvenanceLeavesGitRegistrationUntouched pins that
// revoking is a distinct, narrower step than removing: it only ever deletes
// the stamp file, never touching the admin directory or git's own worktree
// registration. That separation is what makes the pair crash-safe — if the
// process is killed between the two calls in WorktreeRemoveAndRevoke, revoke
// has already run to completion on its own, independent of whatever state
// the removal step was in.
func TestRevokeWorktreeProvenanceLeavesGitRegistrationUntouched(t *testing.T) {
	repo := initTestRepo(t)

	wt := filepath.Join(t.TempDir(), "wt")
	if err := New(repo).WorktreeAdd(WorktreeAddOptions{
		Path:       wt,
		Base:       "HEAD",
		Provenance: ProvenanceManaged,
	}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	if err := New(wt).RevokeWorktreeProvenance(); err != nil {
		t.Fatalf("RevokeWorktreeProvenance: %v", err)
	}

	if _, err := New(wt).ReadWorktreeProvenance(); !errors.Is(err, ErrNoProvenance) {
		t.Fatalf("ReadWorktreeProvenance after revoke = %v, want ErrNoProvenance", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("checkout %q was removed by RevokeWorktreeProvenance alone: %v", wt, err)
	}
	worktrees, err := New(repo).WorktreeList()
	if err != nil {
		t.Fatalf("WorktreeList: %v", err)
	}
	found := false
	for _, w := range worktrees {
		if filepath.Clean(w.Path) == filepath.Clean(wt) {
			found = true
		}
	}
	if !found {
		t.Errorf("worktree %q was deregistered by RevokeWorktreeProvenance alone: %v", wt, worktrees)
	}
}

// TestRevokeWorktreeProvenanceIsIdempotent pins that revoking a tree with no
// stamp, or no admin directory at all, is a safe no-op rather than an error —
// teardown must not fail merely because a previous attempt already revoked,
// or because the tree is already gone.
func TestRevokeWorktreeProvenanceIsIdempotent(t *testing.T) {
	repo := initTestRepo(t)

	wt := filepath.Join(t.TempDir(), "wt")
	if err := New(repo).WorktreeAdd(WorktreeAddOptions{
		Path:       wt,
		Base:       "HEAD",
		Provenance: ProvenanceManaged,
	}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	if err := New(wt).RevokeWorktreeProvenance(); err != nil {
		t.Fatalf("first RevokeWorktreeProvenance: %v", err)
	}
	if err := New(wt).RevokeWorktreeProvenance(); err != nil {
		t.Fatalf("second RevokeWorktreeProvenance (already revoked): %v", err)
	}

	if err := New(filepath.Join(t.TempDir(), "never-existed")).RevokeWorktreeProvenance(); err != nil {
		t.Fatalf("RevokeWorktreeProvenance on a non-worktree path: %v", err)
	}
}

func TestIsMainWorktree(t *testing.T) {
	repo := initTestRepo(t)

	wt := filepath.Join(t.TempDir(), "wt")
	if err := New(repo).WorktreeAdd(WorktreeAddOptions{
		Path:       wt,
		Base:       "HEAD",
		Provenance: ProvenanceManaged,
	}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	for _, tc := range []struct {
		dir  string
		want bool
	}{
		{repo, true},
		{wt, false},
	} {
		got, err := New(tc.dir).IsMainWorktree()
		if err != nil {
			t.Fatalf("IsMainWorktree(%q): %v", tc.dir, err)
		}
		if got != tc.want {
			t.Errorf("IsMainWorktree(%q) = %v, want %v", tc.dir, got, tc.want)
		}
	}
}

// TestWorktreeAddCreatesBranch covers the branch-creating form the pack
// formulas need alongside the detached form.
func TestWorktreeAddCreatesBranch(t *testing.T) {
	repo := initTestRepo(t)

	wt := filepath.Join(t.TempDir(), "wt")
	if err := New(repo).WorktreeAdd(WorktreeAddOptions{
		Path:       wt,
		Base:       "HEAD",
		Branch:     "feature",
		Provenance: ProvenanceManaged,
	}); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	branch, err := New(wt).CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "feature" {
		t.Errorf("CurrentBranch = %q, want %q", branch, "feature")
	}
}
