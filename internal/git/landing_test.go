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
