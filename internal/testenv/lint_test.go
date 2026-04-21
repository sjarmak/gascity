package testenv_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const importPath = "github.com/gastownhall/gascity/internal/testenv"

// TestRequiresTestenvImport walks every *_test.go file in the repo and fails
// if any test package lacks a blank import of internal/testenv. The init()
// scrub in testenv.go is the load-bearing defense against GC_* env leaks for
// direct `go test` and IDE-runner invocations that bypass the Makefile's
// env -i wrapper. Without this lint, the convention drifts the moment a new
// test package is added without the import.
func TestRequiresTestenvImport(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	// Group by directory: a single test binary covers all *_test.go files in
	// a dir (package foo and package foo_test compile into one binary), so
	// one blank import per dir is sufficient to run testenv's init() once.
	hasImport := map[string]bool{}
	allDirs := map[string]bool{}

	skipDirs := map[string]bool{
		"vendor":       true,
		"node_modules": true,
		".git":         true,
	}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Skip the testenv package itself — it cannot import itself.
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		if rel == "internal/testenv" {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		allDirs[rel] = true
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == importPath {
				hasImport[rel] = true
				return nil
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}

	var missing []string
	for dir := range allDirs {
		if !hasImport[dir] {
			missing = append(missing, dir)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Fatalf("test directories missing blank import of %q (%d):\n  %s\n\n"+
			"Add this to one *_test.go file in each directory:\n\n"+
			"    import _ %q\n\n"+
			"This guarantees GC_* env vars are scrubbed before tests run, "+
			"so a leak from an agent session cannot corrupt a live city.\n"+
			"Run scripts/add-testenv-import.sh to inject automatically.",
			importPath, len(missing), strings.Join(missing, "\n  "), importPath)
	}
}

// repoRoot returns the repository root by asking git. Falls back to walking up
// from this file looking for go.mod if git is unavailable.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	// Fallback: walk up looking for go.mod.
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	for {
		if _, err := filepath.Abs(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}
