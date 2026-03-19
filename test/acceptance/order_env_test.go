//go:build acceptance_a

// Order exec environment propagation tests.
//
// Regression test for Bug 1 (2026-03-18): mergeRuntimeEnv double-call
// stripped GC_CITY_PATH when adding GC_DOLT_PORT.
//
// These test the invariant that orderExecEnv always includes the full
// set of city runtime vars, regardless of whether dolt port is set.
package acceptance_test

import (
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/citylayout"
	"pgregory.net/rapid"
)

// TestOrderEnvInvariant_CityVarsAlwaysPresent verifies that the city
// runtime env vars (as produced by CityRuntimeEnv) always include
// GC_CITY_PATH, GC_CITY_ROOT, and GC_CITY_RUNTIME_DIR regardless of
// what additional vars are merged.
//
// This is the invariant that broke when cityRuntimeProcessEnv called
// mergeRuntimeEnv twice — the second call stripped the first call's vars.
func TestOrderEnvInvariant_CityVarsAlwaysPresent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cityPath := tempDir(rt)

		// CityRuntimeEnv is what orderExecEnv uses as its base.
		envSlice := citylayout.CityRuntimeEnv(cityPath)

		// Convert to map for easy lookup.
		env := make(map[string]string)
		for _, entry := range envSlice {
			if k, v, ok := splitEnvEntry(entry); ok {
				env[k] = v
			}
		}

		// INVARIANT: GC_CITY_PATH always present and equals cityPath.
		if v := env["GC_CITY_PATH"]; v != cityPath {
			rt.Fatalf("GC_CITY_PATH = %q, want %q", v, cityPath)
		}

		// INVARIANT: GC_CITY_ROOT always present and equals cityPath.
		if v := env["GC_CITY_ROOT"]; v != cityPath {
			rt.Fatalf("GC_CITY_ROOT = %q, want %q", v, cityPath)
		}

		// INVARIANT: GC_CITY_RUNTIME_DIR always present.
		wantRuntime := filepath.Join(cityPath, ".gc", "runtime")
		if v := env["GC_CITY_RUNTIME_DIR"]; v != wantRuntime {
			rt.Fatalf("GC_CITY_RUNTIME_DIR = %q, want %q", v, wantRuntime)
		}
	})
}

// TestOrderEnvInvariant_PackEnvIncludesCity verifies that PackRuntimeEnv
// (used by order dispatch for pack-scoped orders) includes city vars
// in addition to pack-specific vars.
func TestOrderEnvInvariant_PackEnvIncludesCity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cityPath := tempDir(rt)
		packName := rapid.StringMatching(`[a-z][a-z0-9]{1,8}`).Draw(rt, "packName")

		envSlice := citylayout.PackRuntimeEnv(cityPath, packName)

		env := make(map[string]string)
		for _, entry := range envSlice {
			if k, v, ok := splitEnvEntry(entry); ok {
				env[k] = v
			}
		}

		// INVARIANT: City vars still present in pack env.
		if v := env["GC_CITY_PATH"]; v != cityPath {
			rt.Fatalf("GC_CITY_PATH = %q, want %q", v, cityPath)
		}

		// INVARIANT: Pack state dir is set.
		wantPack := citylayout.PackStateDir(cityPath, packName)
		if v := env["GC_PACK_STATE_DIR"]; v != wantPack {
			rt.Fatalf("GC_PACK_STATE_DIR = %q, want %q", v, wantPack)
		}
	})
}

func splitEnvEntry(entry string) (key, val string, ok bool) {
	for i := 0; i < len(entry); i++ {
		if entry[i] == '=' {
			return entry[:i], entry[i+1:], true
		}
	}
	return "", "", false
}
