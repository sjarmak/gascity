package main

import (
	"path/filepath"

	"github.com/gastownhall/gascity/internal/citylayout"
)

// materializeBuiltinPrompts writes embedded prompt files to .gc/system/prompts/.
// Files are always overwritten to stay in sync with the gc binary version.
// Uses materializeFS to walk the embed.FS — no hardcoded filename list.
func materializeBuiltinPrompts(cityPath string) error {
	return materializeFS(defaultPrompts, "prompts",
		filepath.Join(cityPath, citylayout.SystemPromptsRoot))
}

// materializeBuiltinFormulas is retained for callers that still expect the helper,
// but city-local default formulas are now written only by gc init.
func materializeBuiltinFormulas(_ string) error { return nil }
