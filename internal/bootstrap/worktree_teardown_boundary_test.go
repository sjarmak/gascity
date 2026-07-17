package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCoreFormulasDoNotRawRemoveWorktrees pins gc-1fbg's call-site migration:
// no built-in formula may tear down a worktree with a raw filesystem removal
// (`rm -rf`, or the `git worktree remove || rm -rf` fallback this fork used to
// ship). A raw removal leaves the worktree's git admin directory — and the
// import-trust provenance stamp inside it — behind, which lets anything able
// to write the reaped path replant a forged `.git` pointer and inherit the
// stamp's trust. `gc worktree remove` is the sole supported front door: it
// revokes the stamp before asking git to deregister the tree, so a crash
// between the two still fails closed.
//
// This is a content scan, not a shell-semantics parser: it forbids the
// specific `rm -rf` invocation this bug's fix removed, not every mention of
// "rm -rf" (a formula step is free to rm -rf scratch state that was never a
// git worktree).
func TestCoreFormulasDoNotRawRemoveWorktrees(t *testing.T) {
	dir := coreFormulaSearchPaths(t)[0]
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", path, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "rm -rf") && strings.Contains(line, "WORKTREE") {
				t.Errorf("%s: %q raw-removes a worktree; use `gc worktree remove` so the provenance stamp is revoked (gc-1fbg)", entry.Name(), strings.TrimSpace(line))
			}
		}
	}
}
