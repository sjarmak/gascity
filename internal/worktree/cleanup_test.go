package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// initTestRepoWithRemote returns a repo whose initial branch is pushed to a
// bare remote, so a branch cut from it has no unpushed commits. Cleanup's
// work-loss gates are all about reachability, and every one of them answers
// "yes, unpushed" in a repo with no remote at all.
func initTestRepoWithRemote(t *testing.T) (string, string) {
	t.Helper()
	repo, branch := initTestRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, repo, "init", "--bare", remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", branch)
	return repo, branch
}

func ensureFixture(t *testing.T, repo, base, branch string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wt")
	if _, err := Ensure(Spec{RepoDir: repo, Path: path, Branch: branch, Base: base}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	return path
}

func TestCleanupRemovesCleanWorktree(t *testing.T) {
	repo, base := initTestRepoWithRemote(t)
	path := ensureFixture(t, repo, base, "work/clean")

	rep, err := Cleanup(CleanupSpec{RepoDir: repo, Path: path, Branch: "work/clean"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !rep.Removed || rep.AlreadyAbsent || rep.CleanupPending {
		t.Fatalf("report = %+v, want removed", rep)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worktree still present after removal: %v", err)
	}
}

func TestCleanupAbsentPathIsNotReportedAsARemoval(t *testing.T) {
	repo, _ := initTestRepoWithRemote(t)
	path := filepath.Join(t.TempDir(), "never-existed")

	rep, err := Cleanup(CleanupSpec{RepoDir: repo, Path: path, Branch: "work/gone"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !rep.AlreadyAbsent {
		t.Fatalf("report = %+v, want already_absent", rep)
	}
	if rep.Removed {
		t.Fatal("absent path reported as a removal this call performed")
	}
}

func TestCleanupRefusesEachWorkLossGate(t *testing.T) {
	cases := []struct {
		name string
		code string
		// setup dirties the fixture so the named gate refuses.
		setup func(t *testing.T, repo, path string)
	}{
		{
			name: "uncommitted changes",
			code: "uncommitted_work",
			setup: func(t *testing.T, _, path string) {
				if err := os.WriteFile(filepath.Join(path, "dirty.txt"), []byte("x"), 0o600); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			},
		},
		{
			name: "stashed changes",
			code: "stashed_work",
			setup: func(t *testing.T, _, path string) {
				// Stash a tracked-file change, so the gate fires on a
				// worktree whose status is otherwise clean.
				if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("x"), 0o600); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
				runGit(t, path, "add", "tracked.txt")
				runGit(t, path, "commit", "-m", "add tracked")
				runGit(t, path, "push", "origin", "HEAD:refs/heads/work-held")
				if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("y"), 0o600); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
				runGit(t, path, "stash")
			},
		},
		{
			name: "commits no remote reaches",
			code: "unpushed_commits",
			setup: func(t *testing.T, _, path string) {
				runGit(t, path, "commit", "--allow-empty", "-m", "local only")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, base := initTestRepoWithRemote(t)
			path := ensureFixture(t, repo, base, "work/held")
			tc.setup(t, repo, path)

			rep, err := Cleanup(CleanupSpec{RepoDir: repo, Path: path, Branch: "work/held"})
			var refusal *RefusalError
			if !errors.As(err, &refusal) {
				t.Fatalf("Cleanup err = %v, want a RefusalError", err)
			}
			if refusal.Code != tc.code {
				t.Fatalf("refusal code = %q, want %q", refusal.Code, tc.code)
			}
			if rep.Removed || !rep.CleanupPending {
				t.Fatalf("report = %+v, want cleanup_pending and no removal", rep)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("refusal destroyed the worktree it declined to remove: %v", err)
			}
		})
	}
}

func TestCleanupRefusesAPathThatIsNotTheNamedWorktree(t *testing.T) {
	repo, base := initTestRepoWithRemote(t)
	path := ensureFixture(t, repo, base, "work/named")

	rep, err := Cleanup(CleanupSpec{RepoDir: repo, Path: path, Branch: "work/some-other-branch"})
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("Cleanup err = %v, want a RefusalError", err)
	}
	if refusal.Code != "not_the_named_worktree" {
		t.Fatalf("refusal code = %q, want not_the_named_worktree", refusal.Code)
	}
	if rep.Removed {
		t.Fatal("cleanup removed a worktree whose identity it could not confirm")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree removed despite the identity refusal: %v", err)
	}
}

func TestEnsureReportsTheBaseCommitItBranchedFrom(t *testing.T) {
	repo, base := initTestRepo(t)
	want := runGit(t, repo, "rev-parse", base+"^{commit}")
	path := filepath.Join(t.TempDir(), "wt")

	rep, err := Ensure(Spec{RepoDir: repo, Path: path, Branch: "work/base", Base: base})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if rep.BaseSHA != want {
		t.Fatalf("BaseSHA = %q, want %q", rep.BaseSHA, want)
	}
}

func TestEnsureRefusesWhenTheBaseRefMovedUnderTheCaller(t *testing.T) {
	repo, base := initTestRepo(t)
	stale := runGit(t, repo, "rev-parse", base+"^{commit}")
	runGit(t, repo, "commit", "--allow-empty", "-m", "moved")
	path := filepath.Join(t.TempDir(), "wt")

	_, err := Ensure(Spec{RepoDir: repo, Path: path, Branch: "work/moved", Base: base, BaseSHA: stale})
	if err == nil {
		t.Fatal("Ensure accepted a base that resolves elsewhere than the caller expects")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("refused Ensure left a worktree behind: %v", statErr)
	}
}

func TestEnsureReportsNoBaseCommitWhenTheBranchAlreadyExists(t *testing.T) {
	repo, base := initTestRepo(t)
	runGit(t, repo, "branch", "work/existing", base)
	path := filepath.Join(t.TempDir(), "wt")

	rep, err := Ensure(Spec{RepoDir: repo, Path: path, Branch: "work/existing", Base: base})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if rep.BaseSHA != "" {
		t.Fatalf("BaseSHA = %q, want empty: no base was consulted for an existing branch", rep.BaseSHA)
	}
}
