package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gastownhall/gascity/internal/citylayout"
)

//go:embed gc-beads-bd
var beadsBdScript []byte

// MaterializeBeadsBdScript writes the embedded gc-beads-bd script to
// .gc/system/bin/gc-beads-bd in the city directory with 0755 permissions.
// Always overwrites to stay in sync with the gc binary version.
// Returns the absolute path to the materialized script.
func MaterializeBeadsBdScript(cityPath string) (string, error) {
	binDir := filepath.Join(cityPath, citylayout.SystemBinRoot)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", citylayout.SystemBinRoot, err)
	}

	dst := filepath.Join(binDir, "gc-beads-bd")
	if err := os.WriteFile(dst, beadsBdScript, 0o755); err != nil {
		return "", fmt.Errorf("writing gc-beads-bd: %w", err)
	}
	return dst, nil
}
