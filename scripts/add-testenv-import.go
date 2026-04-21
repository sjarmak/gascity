//go:build ignore

// add-testenv-import injects `_ "github.com/gastownhall/gascity/internal/testenv"`
// into every test directory that does not already have it. Idempotent.
//
// Usage: go run scripts/add-testenv-import.go
//
// See PR #746 for context — this script wires every test binary into the
// GC_* env scrub. Without the import, the lint test (TestRequiresTestenvImport)
// fails for that directory.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

const importPath = "github.com/gastownhall/gascity/internal/testenv"

func main() {
	root, err := repoRoot()
	check(err)

	type fileInfo struct {
		path string
		pkg  string
	}
	dirFiles := map[string][]fileInfo{}
	dirHasImport := map[string]bool{}

	skipDirs := map[string]bool{"vendor": true, "node_modules": true, ".git": true}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		if rel == "internal/testenv" {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		dirFiles[rel] = append(dirFiles[rel], fileInfo{path: path, pkg: f.Name.Name})
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == importPath {
				dirHasImport[rel] = true
			}
		}
		return nil
	})
	check(err)

	dirs := make([]string, 0, len(dirFiles))
	for d := range dirFiles {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	added := 0
	for _, dir := range dirs {
		if dirHasImport[dir] {
			continue
		}
		files := dirFiles[dir]
		sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
		// Prefer the first non-xtest file; only fall back to xtest if that's
		// all the directory has.
		target := files[0]
		for _, f := range files {
			if !strings.HasSuffix(f.pkg, "_test") {
				target = f
				break
			}
		}
		check(injectImport(target.path))
		fmt.Printf("injected: %s\n", target.path)
		added++
	}
	fmt.Printf("\n%d file(s) updated, %d directory already up-to-date.\n", added, len(dirs)-added)
}

func injectImport(path string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return err
	}
	if !astutil.AddNamedImport(fset, f, "_", importPath) {
		return nil // already present
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from cwd")
		}
		dir = parent
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
