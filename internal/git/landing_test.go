package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeadLandedOnDefaultResult(t *testing.T) {
	repo := initLandingRepo(t)

	t.Run("ancestor", func(t *testing.T) {
		landed, err := New(repo).HeadLandedOnDefaultResult()
		if err != nil || !landed {
			t.Fatalf("landed=%v err=%v, want true", landed, err)
		}
	})

	t.Run("rescue ref is preservation only", func(t *testing.T) {
		runGit(t, repo, "checkout", "-b", "work")
		if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("unlanded\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, repo, "add", "work.txt")
		runGit(t, repo, "commit", "-m", "unlanded")
		runGit(t, repo, "branch", "rescue/work")

		landed, err := New(repo).HeadLandedOnDefaultResult()
		if err != nil {
			t.Fatal(err)
		}
		if landed {
			t.Fatal("rescue ref made unlanded HEAD eligible")
		}
	})

	t.Run("patch equivalent", func(t *testing.T) {
		runGit(t, repo, "checkout", "main")
		runGit(t, repo, "cherry-pick", "work")
		runGit(t, repo, "checkout", "work")

		landed, err := New(repo).HeadLandedOnDefaultResult()
		if err != nil || !landed {
			t.Fatalf("landed=%v err=%v, want patch-equivalent true", landed, err)
		}
	})
}

// TestHeadLandedOnDefaultResult_RefusesLocalOnlyCommitAbsentFromRemote pins
// the fix for the local-ref-preference data-loss path: when a remote is
// configured, landing must be judged against refs/remotes/origin/<branch>,
// never against a local branch the operator can advance independently. Fails
// against the naive "prefer refs/heads/<branch>" implementation this test was
// written to catch.
func TestHeadLandedOnDefaultResult_RefusesLocalOnlyCommitAbsentFromRemote(t *testing.T) {
	repo := initLandingRepo(t)

	// Model a remote whose refs/remotes/origin/main sits at the seed commit,
	// unrelated to the local repo's own remote config (no network needed: a
	// direct ref update is sufficient to make refs/remotes/origin/main
	// resolvable).
	runGit(t, repo, "remote", "add", "origin", "https://example.invalid/repo.git")
	seedSHA, err := runGitAllowFail(t, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %s: %v", seedSHA, err)
	}
	runGit(t, repo, "update-ref", "refs/remotes/origin/main", strings.TrimSpace(seedSHA))

	// Local main advances with a commit that is never published to origin.
	if err := os.WriteFile(filepath.Join(repo, "local-only.txt"), []byte("unpublished\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "local-only.txt")
	runGit(t, repo, "commit", "-m", "local-only work, never published")

	landed, err := New(repo).HeadLandedOnDefaultResult()
	if err != nil {
		t.Fatal(err)
	}
	if landed {
		t.Fatal("HEAD ancestor of local-only main but absent from origin/main was reported landed")
	}
}

func initLandingRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README")
	runGit(t, repo, "commit", "-m", "seed")
	return repo
}
