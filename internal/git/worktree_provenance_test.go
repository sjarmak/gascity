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
