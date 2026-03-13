package automations

import (
	"fmt"
	"path/filepath"

	"github.com/gastownhall/gascity/internal/fsys"
)

// automationDir is the subdirectory name within formula layers that contains automations.
const automationDir = "automations"

// automationFileName is the expected filename inside each automation subdirectory.
const automationFileName = "automation.toml"

// ScanRoot describes one automation discovery root and, optionally, the
// formula layer it belongs to for PACK_DIR semantics.
type ScanRoot struct {
	Dir          string
	FormulaLayer string
}

// Scan discovers automations across formula layers. For each layer dir, it scans
// <layer>/automations/*/automation.toml. Higher-priority layers (later in the slice)
// override lower by subdirectory name. Disabled automations and those in the skip
// list are excluded from results.
func Scan(fs fsys.FS, formulaLayers []string, skip []string) ([]Automation, error) {
	roots := make([]ScanRoot, 0, len(formulaLayers))
	for _, layer := range formulaLayers {
		roots = append(roots, ScanRoot{
			Dir:          filepath.Join(layer, automationDir),
			FormulaLayer: layer,
		})
	}
	return ScanRoots(fs, roots, skip)
}

// ScanRoots discovers automations across explicit automation roots. Higher-priority
// roots (later in the slice) override lower ones by automation name.
func ScanRoots(fs fsys.FS, roots []ScanRoot, skip []string) ([]Automation, error) {
	skipSet := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipSet[s] = true
	}

	// Scan layers lowest → highest priority. Later entries override earlier ones.
	found := make(map[string]Automation) // name → automation
	var order []string                   // preserve discovery order

	for _, root := range roots {
		entries, err := fs.ReadDir(root.Dir)
		if err != nil {
			continue // layer has no automations/ directory — skip
		}

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			tomlPath := filepath.Join(root.Dir, name, automationFileName)
			data, err := fs.ReadFile(tomlPath)
			if err != nil {
				continue // no automation.toml — skip
			}

			a, err := Parse(data)
			if err != nil {
				return nil, fmt.Errorf("automation %q in %s: %w", name, root.Dir, err)
			}
			a.Name = name
			a.Source = tomlPath
			a.FormulaLayer = root.FormulaLayer

			if _, exists := found[name]; !exists {
				order = append(order, name)
			}
			found[name] = a // higher-priority layer overwrites
		}
	}

	// Collect results, excluding disabled and skipped automations.
	var result []Automation
	for _, name := range order {
		a := found[name]
		if !a.IsEnabled() {
			continue
		}
		if skipSet[name] {
			continue
		}
		result = append(result, a)
	}
	return result, nil
}
