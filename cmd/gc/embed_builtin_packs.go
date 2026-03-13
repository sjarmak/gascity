package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/gastownhall/gascity/examples/bd"
	"github.com/gastownhall/gascity/examples/dolt"
	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/config"
)

// builtinPack pairs an embedded FS with the subdirectory name used under .gc/system/packs/.
type builtinPack struct {
	fs   fs.FS
	name string // e.g. "bd", "dolt"
}

// builtinPacks lists the packs embedded in the gc binary.
// Order matters: bd includes dolt, so bd is first.
var builtinPacks = []builtinPack{
	{fs: bd.PackFS, name: "bd"},
	{fs: dolt.PackFS, name: "dolt"},
}

// MaterializeBuiltinPacks writes the embedded bd and dolt pack files to
// .gc/system/packs/bd/ and .gc/system/packs/dolt/ in the city directory. Files are always
// overwritten to stay in sync with the gc binary version (same pattern as
// system formulas and gc-beads-bd). Shell scripts get 0755; everything else 0644.
// Idempotent: safe to call on every gc start and gc init.
func MaterializeBuiltinPacks(cityPath string) error {
	for _, bp := range builtinPacks {
		dst := filepath.Join(cityPath, citylayout.SystemPacksRoot, bp.name)
		if err := materializeFS(bp.fs, ".", dst); err != nil {
			return fmt.Errorf("materializing %s pack: %w", bp.name, err)
		}
	}
	return nil
}

// materializeFS walks an embed.FS rooted at root and writes all files to dstDir.
func materializeFS(embedded fs.FS, root, dstDir string) error {
	return fs.WalkDir(embedded, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Compute the relative path from root.
		rel := path
		if root != "." {
			rel = strings.TrimPrefix(path, root+"/")
			if rel == root {
				return nil
			}
		}

		dst := filepath.Join(dstDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}

		data, err := fs.ReadFile(embedded, path)
		if err != nil {
			return fmt.Errorf("reading embedded %s: %w", path, err)
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}

		perm := os.FileMode(0o644)
		if strings.HasSuffix(path, ".sh") {
			perm = 0o755
		}
		return os.WriteFile(dst, data, perm)
	})
}

// injectBuiltinPacks appends the materialized bd and dolt pack directories
// to cfg.PackDirs when the beads provider is "bd" (or default). This makes
// pack commands, doctor checks, and formulas discoverable without requiring
// the user to add the packs to city.toml manually.
//
// Injection is skipped when:
//   - the provider is explicitly set to something other than "bd"
//   - the materialized pack directory doesn't exist
//   - a pack named "bd" is already loaded (user-supplied pack takes precedence)
func injectBuiltinPacks(cfg *config.City, cityPath string) {
	// Check provider: env var overrides config.
	provider := cfg.Beads.Provider
	if v := os.Getenv("GC_BEADS"); v != "" {
		provider = v
	}
	// Only inject for "bd" provider (the default when provider is empty).
	if provider != "" && provider != "bd" {
		return
	}

	builtinRoots := map[string]string{
		"bd":   filepath.Join(cityPath, citylayout.SystemPacksRoot, "bd"),
		"dolt": filepath.Join(cityPath, citylayout.SystemPacksRoot, "dolt"),
	}
	if _, err := os.Stat(filepath.Join(builtinRoots["bd"], "pack.toml")); err != nil {
		return
	}

	// Any user-supplied override of bd or dolt suppresses the whole builtin family.
	for _, dir := range cfg.PackDirs {
		switch readPackName(dir) {
		case "bd", "dolt":
			return
		}
	}

	cfg.PackDirs = append(cfg.PackDirs, builtinRoots["bd"])
	if _, err := os.Stat(filepath.Join(builtinRoots["dolt"], "pack.toml")); err == nil {
		cfg.PackDirs = append(cfg.PackDirs, builtinRoots["dolt"])
	}
}

// readPackName reads the [pack].name field from a pack.toml in the given directory.
// Returns "" if the file doesn't exist or can't be parsed.
func readPackName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "pack.toml"))
	if err != nil {
		return ""
	}
	var pc struct {
		Pack struct {
			Name string `toml:"name"`
		} `toml:"pack"`
	}
	if _, err := toml.Decode(string(data), &pc); err != nil {
		return ""
	}
	return pc.Pack.Name
}
