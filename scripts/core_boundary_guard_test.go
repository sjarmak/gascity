package scripts_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckCoreBoundaryScansTrackedFilesOnly verifies scripts/check-core-boundary.sh
// enumerates TRACKED source files (git ls-files) rather than walking the whole
// working tree. Walking the tree scanned any .go file present on disk, so a setup
// that keeps Go caches inside the checkout — e.g. a project-local
// GOMODCACHE=$CI_PROJECT_DIR/.cache/go-mod, the canonical GitLab-CI layout — leaked
// third-party module sources into the scan and tripped a false open-core violation
// on the commercial `org_` token. Regression guard for #4479.
//
// The tracked-file enumeration also makes git load-bearing, so the guard must fail
// CLOSED (matching its own stated contract) when it is not run inside a git checkout,
// rather than let an empty file list masquerade as "no violations".
func TestCheckCoreBoundaryScansTrackedFilesOnly(t *testing.T) {
	script := filepath.Join(repoRoot(t), "scripts", "check-core-boundary.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("check-core-boundary.sh not found at %s: %v", script, err)
	}

	// git initializes dir as a repo and stages paths so git ls-files sees them.
	git := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeFile := func(t *testing.T, dir, rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// cleanGoMod is a benign module manifest so check (d) passes.
	const cleanGoMod = "module example.com/core\n\ngo 1.22\n"
	// benignCore is a first-party file with no commercial coupling.
	const benignCore = "package core\n\n// Route returns the tenant's publication path.\nfunc Route(tenant string) string { return \"/\" + tenant }\n"
	// commercialSrc contains the commercial `org_` tenant key that check (b) flags.
	const commercialSrc = "package github\n\ntype Organization struct{ org_id string }\n"

	runScript := func(t *testing.T, dir string) (string, int) {
		t.Helper()
		cmd := exec.Command("bash", script)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			ex := &exec.ExitError{}
			if errors.As(err, &ex) {
				return string(out), ex.ExitCode()
			}
			t.Fatalf("exec error: %v", err)
		}
		return string(out), 0
	}

	t.Run("passes_clean_tracked_tree", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init", "-q", dir)
		writeFile(t, dir, "go.mod", cleanGoMod)
		writeFile(t, dir, "internal/core/route.go", benignCore)
		git(t, dir, "-C", dir, "add", "--", "go.mod", "internal/core/route.go")
		out, code := runScript(t, dir)
		if code != 0 {
			t.Fatalf("expected exit 0 for a clean tracked tree, got %d\n%s", code, out)
		}
	})

	t.Run("ignores_untracked_in_tree_cache", func(t *testing.T) {
		// The #4479 case: a third-party source carrying `org_` under an in-tree Go
		// module cache that is NOT tracked. A tree walk would flag it; enumerating
		// tracked files must not.
		dir := t.TempDir()
		git(t, dir, "init", "-q", dir)
		writeFile(t, dir, "go.mod", cleanGoMod)
		writeFile(t, dir, "internal/core/route.go", benignCore)
		writeFile(t, dir, ".cache/go-mod/github.com/google/go-github/v50@v50.0.0/github/orgs.go", commercialSrc)
		// stage only the first-party files; the cache stays untracked.
		git(t, dir, "-C", dir, "add", "--", "go.mod", "internal/core/route.go")
		out, code := runScript(t, dir)
		if code != 0 {
			t.Fatalf("expected exit 0 — untracked in-tree cache must not be scanned (#4479), got %d\n%s", code, out)
		}
	})

	t.Run("catches_tracked_org_violation", func(t *testing.T) {
		// The guard must still flag a genuine `org_` leak in tracked core source.
		dir := t.TempDir()
		git(t, dir, "init", "-q", dir)
		writeFile(t, dir, "go.mod", cleanGoMod)
		writeFile(t, dir, "internal/core/leak.go", commercialSrc)
		git(t, dir, "-C", dir, "add", "--", "go.mod", "internal/core/leak.go")
		out, code := runScript(t, dir)
		if code == 0 {
			t.Fatalf("expected non-zero exit for a tracked org_ violation, got 0\n%s", out)
		}
		if !strings.Contains(out, "(b)") {
			t.Errorf("failure should name check (b):\n%s", out)
		}
	})

	t.Run("excludes_testdata", func(t *testing.T) {
		// testdata sources are fixtures, not core; a commercial token there is not a leak.
		dir := t.TempDir()
		git(t, dir, "init", "-q", dir)
		writeFile(t, dir, "go.mod", cleanGoMod)
		writeFile(t, dir, "internal/core/route.go", benignCore)
		writeFile(t, dir, "internal/core/testdata/fixture.go", commercialSrc)
		git(t, dir, "-C", dir, "add", "--", "go.mod", "internal/core/route.go", "internal/core/testdata/fixture.go")
		out, code := runScript(t, dir)
		if code != 0 {
			t.Fatalf("expected exit 0 — testdata must stay excluded, got %d\n%s", code, out)
		}
	})

	t.Run("excludes_test_files", func(t *testing.T) {
		// _test.go is non-core surface; a commercial token there is not a leak.
		dir := t.TempDir()
		git(t, dir, "init", "-q", dir)
		writeFile(t, dir, "go.mod", cleanGoMod)
		writeFile(t, dir, "internal/core/route.go", benignCore)
		writeFile(t, dir, "internal/core/leak_test.go", commercialSrc)
		git(t, dir, "-C", dir, "add", "--", "go.mod", "internal/core/route.go", "internal/core/leak_test.go")
		out, code := runScript(t, dir)
		if code != 0 {
			t.Fatalf("expected exit 0 — _test.go must stay excluded, got %d\n%s", code, out)
		}
	})

	t.Run("fails_closed_outside_git", func(t *testing.T) {
		// Not a git checkout: the guard cannot enumerate tracked sources, so it must
		// fail closed rather than silently pass on an empty file list.
		dir := t.TempDir()
		writeFile(t, dir, "go.mod", cleanGoMod)
		writeFile(t, dir, "internal/core/leak.go", commercialSrc)
		out, code := runScript(t, dir)
		if code == 0 {
			t.Fatalf("expected non-zero exit outside a git work tree (fail-closed), got 0\n%s", out)
		}
		if !strings.Contains(out, "git work tree") {
			t.Errorf("failure should explain the missing git work tree:\n%s", out)
		}
	})
}
