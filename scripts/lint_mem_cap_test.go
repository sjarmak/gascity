package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLintMemCap runs the shell self-test for scripts/lib/lint-mem-cap.sh
// and scripts/lint-run.sh, which exercises the gc-vrva1q memory fence with
// fake systemd-run binaries on a fully controlled PATH: an accepted cap
// applies and a genuine overshoot is killed; a value systemd rejects is a
// hard failure that never runs the wrapped command; a missing/unreachable
// systemd manager warns on stderr and falls back to plain execution; the
// explicit opt-out stays silent; and lint-run.sh's own --concurrency/
// GOMEMLIMIT argv construction is correct for both "run" and "fmt".
func TestLintMemCap(t *testing.T) {
	root := repoRoot(t)

	cmd := exec.Command(filepath.Join(root, "scripts", "lint-mem-cap-test"))
	cmd.Dir = root
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(),
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lint-mem-cap-test failed: %v\n%s", err, out)
	}
}
