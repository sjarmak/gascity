package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This alarm refuses startup when work reads and arbitrary-bead routing resolve
// the city root to different providers. It never changes either store.

// beadsProviderResolution is one of the two resolutions compared at boot.
type beadsProviderResolution struct {
	// role names the resolution ("work-class reads" / "arbitrary-bead routing").
	role string
	// provider is the resolved provider name, or "" when unresolvable.
	provider string
	// source names the config key or on-disk marker that produced it.
	source string
}

// compareBeadsProviderResolutions reports the boot verdict for a pair of
// resolutions. It distinguishes three outcomes deliberately: agreement (nil),
// an unresolvable side, and a divergence. Collapsing the middle case into
// agreement would let an unresolvable provider boot silently.
func compareBeadsProviderResolutions(raw, authoritative beadsProviderResolution) error {
	for _, r := range []beadsProviderResolution{raw, authoritative} {
		if strings.TrimSpace(r.provider) == "" {
			return fmt.Errorf(
				"bead provider unresolvable: %s resolved to no provider (from %s); "+
					"refusing startup without changing either store",
				r.role, r.source)
		}
	}
	if raw.provider == authoritative.provider {
		return nil
	}
	return fmt.Errorf(
		"bead provider divergence: %s=%q (from %s), %s=%q (from %s); "+
			"refusing startup without changing either store",
		raw.role, raw.provider, raw.source,
		authoritative.role, authoritative.provider, authoritative.source)
}

// describeRawBeadsProviderSource names the input that produced the raw
// resolution for the city scope.
func describeRawBeadsProviderSource(cityPath string) string {
	if v := strings.TrimSpace(os.Getenv("GC_BEADS")); v != "" {
		scopedRoot := strings.TrimSpace(os.Getenv("GC_BEADS_SCOPE_ROOT"))
		if scopedRoot == "" || samePath(resolveStoreScopeRoot(cityPath, scopedRoot), cityPath) {
			return "GC_BEADS env var"
		}
	}
	if provider := strings.TrimSpace(peekBeadsProvider(filepath.Join(cityPath, "city.toml"))); provider != "" {
		return "city.toml [beads].provider"
	}
	return `built-in default provider "bd"`
}

// describeAuthoritativeBeadsProviderSource names the on-disk marker that
// overrode the configured provider, falling back to the raw source when no
// marker applied.
func describeAuthoritativeBeadsProviderSource(cityPath string) string {
	if scopeUsesBdStoreContract(cityPath) {
		return filepath.Join(cityPath, ".beads", "metadata.json")
	}
	if scopeUsesFileStoreContract(cityPath) {
		return filepath.Join(cityPath, ".gc", "beads.json")
	}
	return describeRawBeadsProviderSource(cityPath)
}

// assertBeadsProviderResolutionsAgree is the boot-time call site: it resolves
// the city scope both ways and returns the verdict.
func assertBeadsProviderResolutionsAgree(cityPath string) error {
	return compareBeadsProviderResolutions(
		beadsProviderResolution{
			role:     "work-class reads",
			provider: rawBeadsProviderForScope(cityPath, cityPath),
			source:   describeRawBeadsProviderSource(cityPath),
		},
		beadsProviderResolution{
			role:     "arbitrary-bead routing",
			provider: authoritativeBeadsProviderForScope(cityPath, cityPath),
			source:   describeAuthoritativeBeadsProviderSource(cityPath),
		},
	)
}
