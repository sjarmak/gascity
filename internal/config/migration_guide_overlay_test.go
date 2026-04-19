package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Regression for gastownhall/gascity#784:
// The pack migration guide claimed `overlays/` (plural) was the canonical
// pack-wide overlay directory, but the loader (ExpandPacks in pack.go and
// DiscoverPackAgents in agent_discovery.go) only reads `overlay/` (singular).
// Students following the guide created a directory the loader ignores, with
// silent failure.
//
// Guard against the guide re-diverging: any backtick-quoted reference to the
// directory name must use the singular form that the loader actually reads.
func TestMigrationGuide_Regression784_UsesSingularOverlayDirectory(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	guidePath := filepath.Join(repoRoot, "docs", "guides", "migrating-to-pack-vnext.md")

	data, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatalf("reading %s: %v", guidePath, err)
	}

	// The loader reads `overlay/` (singular) for the pack-wide bucket; see
	// internal/config/pack.go ExpandPacks and internal/config/agent_discovery.go
	// DiscoverPackAgents. The guide may mention `overlays/` (plural) when
	// explaining the common typo, but must never describe it as canonical.
	text := string(data)
	forbidden := []string{
		"top-level `overlays/`",     // former canonical-instruction wording
		"pack-wide `overlays/`",     // cross-reference in migration tables
		"`overlays/` for pack-wide", // the "use overlays/ for pack-wide" lie
		"Keep as top-level `overlays/`",
		"Keep as top-level  `overlays/`",
	}
	var hits []string
	for _, phrase := range forbidden {
		if strings.Contains(text, phrase) {
			hits = append(hits, phrase)
		}
	}
	if len(hits) > 0 {
		t.Fatalf("%s still describes `overlays/` (plural) as canonical via these phrases: %v\nThe loader only reads `overlay/` (singular); the guide must match. See gastownhall/gascity#784.",
			guidePath, hits)
	}

	// Guard: the guide must clearly state which form the loader actually reads,
	// so readers following the skew-warning are pointed at the right answer.
	if !strings.Contains(text, "loader only discovers `overlay/`") {
		t.Fatalf("%s must state `loader only discovers \\`overlay/\\`` so readers know which form is canonical. See gastownhall/gascity#784.", guidePath)
	}
}
