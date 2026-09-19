package git

import "testing"

// commitEmpty makes an empty commit with the given message and returns the
// resulting HEAD sha.
func commitEmpty(t *testing.T, dir, msg string) string {
	t.Helper()
	runGit(t, dir, "commit", "--allow-empty", "-m", msg)
	g := New(dir)
	head, err := g.Head()
	if err != nil {
		t.Fatalf("Head after commit %q: %v", msg, err)
	}
	return head
}

func TestHead(t *testing.T) {
	repo := initTestRepo(t)
	g := New(repo)
	head, err := g.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if len(head) != 40 {
		t.Errorf("Head() = %q, want a 40-char sha", head)
	}
	// A second commit must change HEAD.
	next := commitEmpty(t, repo, "second")
	if next == head {
		t.Errorf("Head did not advance after commit: still %q", head)
	}
}

func TestHead_NonRepo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", dir)
	g := New(dir)
	if _, err := g.Head(); err == nil {
		t.Error("Head() on non-repo: want error, got nil")
	}
}

func TestCommitsAhead(t *testing.T) {
	repo := initTestRepo(t)
	g := New(repo)
	base, err := g.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}

	// Branch off and add two commits; the feature branch is 2 ahead of base.
	runGit(t, repo, "checkout", "-b", "feature")
	commitEmpty(t, repo, "feat-1")
	commitEmpty(t, repo, "feat-2")

	ahead, err := g.CommitsAhead(base)
	if err != nil {
		t.Fatalf("CommitsAhead: %v", err)
	}
	if ahead != 2 {
		t.Errorf("CommitsAhead(%q) = %d, want 2", base, ahead)
	}

	// On the base itself there are zero commits ahead of the feature tip.
	zero, err := New(repo).CommitsAhead("feature")
	if err != nil {
		t.Fatalf("CommitsAhead(feature): %v", err)
	}
	if zero != 0 {
		t.Errorf("CommitsAhead(feature) from feature HEAD = %d, want 0", zero)
	}
}

func TestCommitsAhead_BadBase(t *testing.T) {
	repo := initTestRepo(t)
	g := New(repo)
	if _, err := g.CommitsAhead("no-such-ref"); err == nil {
		t.Error("CommitsAhead(no-such-ref): want error, got nil")
	}
}

func TestIsAncestor(t *testing.T) {
	repo := initTestRepo(t)
	g := New(repo)
	first, err := g.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	second := commitEmpty(t, repo, "second")

	// first is an ancestor of second.
	ok, err := g.IsAncestor(first, second)
	if err != nil {
		t.Fatalf("IsAncestor(first, second): %v", err)
	}
	if !ok {
		t.Errorf("IsAncestor(first, second) = false, want true")
	}

	// second is NOT an ancestor of first (exit code 1, not an error).
	ok, err = g.IsAncestor(second, first)
	if err != nil {
		t.Fatalf("IsAncestor(second, first) returned error, want (false,nil): %v", err)
	}
	if ok {
		t.Errorf("IsAncestor(second, first) = true, want false")
	}
}

func TestIsAncestor_BadRef(t *testing.T) {
	repo := initTestRepo(t)
	g := New(repo)
	if _, err := g.IsAncestor("no-such-ref", "HEAD"); err == nil {
		t.Error("IsAncestor(no-such-ref, HEAD): want error, got nil")
	}
}
