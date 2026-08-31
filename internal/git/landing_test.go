package git

import (
	"os"
	"path/filepath"
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

// TestHeadLandedOnDefaultResult_PrefersOriginOverLocalBranch pins the
// ref-selection order: a commit merged into the local main but never pushed
// must not read as landed. refs/remotes/origin/<branch> is the canonical
// record of what actually landed upstream; refs/heads/<branch> is only a
// local convenience ref that a caller can advance (merge, reset, rebase)
// without ever pushing. Checking refs/heads/<branch> first would let an
// unpushed local merge falsely mark a worktree safe to reclaim.
func TestHeadLandedOnDefaultResult_PrefersOriginOverLocalBranch(t *testing.T) {
	origin := t.TempDir()
	runGit(t, origin, "init", "--bare", "-b", "main")

	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README")
	runGit(t, repo, "commit", "-m", "seed")
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "origin", "main")

	runGit(t, repo, "checkout", "-b", "work")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("unlanded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "work.txt")
	runGit(t, repo, "commit", "-m", "unlanded")

	// Merge work into local main WITHOUT pushing. refs/heads/main now
	// contains the commit; refs/remotes/origin/main does not.
	runGit(t, repo, "checkout", "main")
	runGit(t, repo, "merge", "--no-ff", "-m", "local-only merge", "work")
	runGit(t, repo, "checkout", "work")

	landed, err := New(repo).HeadLandedOnDefaultResult()
	if err != nil {
		t.Fatal(err)
	}
	if landed {
		t.Fatal("unpushed local-main merge made HEAD read as landed; must check refs/remotes/origin first")
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
