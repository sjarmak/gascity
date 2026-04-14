// Package gastown embeds the gastown orchestration pack for bundling into the gc binary.
package gastown

import "embed"

// PackFS contains the gastown pack files.
//
//go:embed pack.toml commands doctor formulas orders namepools all:overlay all:overlays prompts scripts
var PackFS embed.FS
