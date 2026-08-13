package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

// TestCompareBeadsProviderResolutions covers the three outcomes the check must
// keep distinct. Flipping mutation for the whole table: deleting the
// TrimSpace-empty loop in compareBeadsProviderResolutions turns the
// "unresolvable" rows green (an empty provider then compares equal to another
// empty provider, or reports a bare divergence), and deleting the
// `raw.provider == authoritative.provider` return turns the agreeing rows red.
func TestCompareBeadsProviderResolutions(t *testing.T) {
	tests := []struct {
		name          string
		raw           beadsProviderResolution
		authoritative beadsProviderResolution
		wantErr       bool
		wantContains  []string
	}{
		{
			// RAIL 1 (green). Flips red if the equality check is replaced by
			// an unconditional error, or if the roles are compared by source
			// instead of by provider (the sources differ here).
			name:          "agree on bd",
			raw:           beadsProviderResolution{role: "work-class reads", provider: "bd", source: "city.toml [beads].provider"},
			authoritative: beadsProviderResolution{role: "arbitrary-bead routing", provider: "bd", source: "/city/.beads/metadata.json"},
			wantErr:       false,
		},
		{
			// RAIL 2 (red). The ds-research configuration exactly.
			// Flips green by restoring the pre-change behavior: drop the
			// divergence return and always return nil.
			name:          "diverge file vs bd",
			raw:           beadsProviderResolution{role: "work-class reads", provider: "file", source: "city.toml [beads].provider"},
			authoritative: beadsProviderResolution{role: "arbitrary-bead routing", provider: "bd", source: "/city/.beads/metadata.json"},
			wantErr:       true,
			wantContains: []string{
				"bead provider divergence",
				`"file"`,
				`"bd"`,
				"city.toml [beads].provider",
				"/city/.beads/metadata.json",
				"work-class reads",
				"arbitrary-bead routing",
			},
		},
		{
			// RAIL 2 (red), the mirror direction. Flips green if the
			// comparison is made one-directional (e.g. only erroring when the
			// raw side is "file").
			name:          "diverge bd vs file",
			raw:           beadsProviderResolution{role: "work-class reads", provider: "bd", source: "city.toml [beads].provider"},
			authoritative: beadsProviderResolution{role: "arbitrary-bead routing", provider: "file", source: "/city/.gc/beads.json"},
			wantErr:       true,
			wantContains:  []string{"bead provider divergence", `"bd"`, `"file"`, "/city/.gc/beads.json"},
		},
		{
			// Third state: unresolvable, NOT agreement. Flips green (wrongly)
			// by removing the empty-provider loop, since "" == "" would then
			// read as agreement.
			name:          "both unresolvable is not agreement",
			raw:           beadsProviderResolution{role: "work-class reads", provider: "", source: "city.toml [beads].provider"},
			authoritative: beadsProviderResolution{role: "arbitrary-bead routing", provider: "", source: "city.toml [beads].provider"},
			wantErr:       true,
			wantContains:  []string{"bead provider unresolvable", "work-class reads"},
		},
		{
			// Unresolvable on one side only. Flips to the WRONG error
			// (divergence instead of unresolvable) by removing the empty-provider
			// loop; the assertion on the message text is what catches that.
			name:          "authoritative unresolvable",
			raw:           beadsProviderResolution{role: "work-class reads", provider: "bd", source: "city.toml [beads].provider"},
			authoritative: beadsProviderResolution{role: "arbitrary-bead routing", provider: "   ", source: "/city/.beads/metadata.json"},
			wantErr:       true,
			wantContains:  []string{"bead provider unresolvable", "arbitrary-bead routing", "/city/.beads/metadata.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := compareBeadsProviderResolutions(tt.raw, tt.authoritative)
			if tt.wantErr != (err != nil) {
				t.Fatalf("compareBeadsProviderResolutions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				return
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err.Error(), want)
				}
			}
		})
	}
}

// writeDivergenceTestCity lays down a city root with the given city.toml body
// and optional store markers.
func writeDivergenceTestCity(t *testing.T, body string, bdMarker, fileMarker bool) string {
	t.Helper()
	cityDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityDir, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if bdMarker {
		if err := os.MkdirAll(filepath.Join(cityDir, ".beads"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cityDir, ".beads", "metadata.json"),
			[]byte(`{"backend":"dolt","dolt_database":"demo"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if fileMarker {
		if err := os.WriteFile(filepath.Join(cityDir, ".gc", "beads.json"), []byte(`{"beads":[]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cityDir
}

// TestAssertBeadsProviderResolutionsAgreeGreenRail is RAIL 1 end to end: a city
// whose configured provider matches its on-disk marker boots silently.
//
// Flipping mutation: add .beads/metadata.json to this city (bdMarker=true)
// while leaving provider = "file"; the authoritative resolution then returns
// "bd" and this test goes red.
func TestAssertBeadsProviderResolutionsAgreeGreenRail(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Setenv("GC_BEADS", "")
	t.Setenv("GC_BEADS_SCOPE_ROOT", "")

	cityDir := writeDivergenceTestCity(t, `[workspace]
name = "demo"
[beads]
provider = "file"
`, false, true)

	if err := assertBeadsProviderResolutionsAgree(cityDir); err != nil {
		t.Fatalf("agreeing city failed the boot assertion: %v", err)
	}
}

// TestAssertBeadsProviderResolutionsAgreeRedRail is RAIL 2 end to end: the
// ds-research configuration. [beads] provider = "file" points work-class reads
// at the file store while .beads/metadata.json routes arbitrary-bead work to
// the bd/Dolt store.
//
// Flipping mutation: remove .beads/metadata.json (bdMarker=false); the
// authoritative resolution then falls through to the configured "file" and this
// test goes green. Removing the call to authoritativeBeadsProviderForScope in
// assertBeadsProviderResolutionsAgree (resolving both sides the raw way) flips
// it green too, which is the regression this test exists to catch.
func TestAssertBeadsProviderResolutionsAgreeRedRail(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Setenv("GC_BEADS", "")
	t.Setenv("GC_BEADS_SCOPE_ROOT", "")

	cityDir := writeDivergenceTestCity(t, `[workspace]
name = "demo"
[beads]
provider = "file"
`, true, false)

	err := assertBeadsProviderResolutionsAgree(cityDir)
	if err == nil {
		t.Fatal("split-ledger city passed the boot assertion")
	}
	// Assert the REASON, not merely that something errored: a fail-closed
	// check that rejected every city would satisfy a bare non-nil assertion.
	msg := err.Error()
	for _, want := range []string{
		"bead provider divergence",
		`"file"`,
		`"bd"`,
		"city.toml [beads].provider",
		filepath.Join(cityDir, ".beads", "metadata.json"),
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("failure message does not name %q:\n%s", want, msg)
		}
	}
}

// TestPrepareCityForSupervisorRejectsProviderDivergence pins the managed
// supervisor path. This city normally starts through `gc supervisor run`, not
// `gc start`, so checking only doStartStandalone leaves the live reconciler
// able to initialize against the wrong ledger.
//
// Flipping mutation: remove the provider assertion from
// prepareCityForSupervisor. The fixture then advances past this guard instead
// of returning the provider-divergence reason.
func TestPrepareCityForSupervisorRejectsProviderDivergence(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	t.Setenv("GC_BEADS", "")
	t.Setenv("GC_BEADS_SCOPE_ROOT", "")

	cityDir := writeDivergenceTestCity(t, `[workspace]
name = "demo"
[beads]
provider = "file"
`, true, false)
	cfg := config.DefaultCity("demo")

	err := prepareCityForSupervisor(cityDir, "demo", &cfg, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "bead provider divergence") {
		t.Fatalf("prepareCityForSupervisor() error = %v, want provider-divergence refusal", err)
	}
}

// TestDescribeRawBeadsProviderSourceIgnoresOutOfScopeOverride keeps the alarm's
// attribution on the same path as provider resolution. A GC_BEADS override
// pinned to another scope does not produce this city's raw provider and must
// not be named as its source.
func TestDescribeRawBeadsProviderSourceIgnoresOutOfScopeOverride(t *testing.T) {
	cityDir := writeDivergenceTestCity(t, `[workspace]
name = "demo"
[beads]
provider = "bd"
`, false, false)
	t.Setenv("GC_BEADS", "file")
	t.Setenv("GC_BEADS_SCOPE_ROOT", t.TempDir())

	if got := describeRawBeadsProviderSource(cityDir); got != "city.toml [beads].provider" {
		t.Fatalf("describeRawBeadsProviderSource() = %q, want city.toml source", got)
	}
}

func TestDescribeBeadsProviderSources(t *testing.T) {
	t.Run("raw unscoped environment override", func(t *testing.T) {
		cityDir := writeDivergenceTestCity(t, "", false, false)
		t.Setenv("GC_BEADS", "bd")
		t.Setenv("GC_BEADS_SCOPE_ROOT", "")

		if got := describeRawBeadsProviderSource(cityDir); got != "GC_BEADS env var" {
			t.Fatalf("describeRawBeadsProviderSource() = %q, want environment source", got)
		}
	})

	t.Run("raw built-in default", func(t *testing.T) {
		cityDir := writeDivergenceTestCity(t, "", false, false)
		t.Setenv("GC_BEADS", "")
		t.Setenv("GC_BEADS_SCOPE_ROOT", "")

		if got := describeRawBeadsProviderSource(cityDir); got != `built-in default provider "bd"` {
			t.Fatalf("describeRawBeadsProviderSource() = %q, want default source", got)
		}
	})

	t.Run("authoritative file marker", func(t *testing.T) {
		cityDir := writeDivergenceTestCity(t, "", false, true)

		want := filepath.Join(cityDir, ".gc", "beads.json")
		if got := describeAuthoritativeBeadsProviderSource(cityDir); got != want {
			t.Fatalf("describeAuthoritativeBeadsProviderSource() = %q, want %q", got, want)
		}
	})

	t.Run("authoritative raw fallback", func(t *testing.T) {
		cityDir := writeDivergenceTestCity(t, `[beads]
provider = "file"
`, false, false)
		t.Setenv("GC_BEADS", "")
		t.Setenv("GC_BEADS_SCOPE_ROOT", "")

		if got := describeAuthoritativeBeadsProviderSource(cityDir); got != "city.toml [beads].provider" {
			t.Fatalf("describeAuthoritativeBeadsProviderSource() = %q, want raw fallback source", got)
		}
	})
}
