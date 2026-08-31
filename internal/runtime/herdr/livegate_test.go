package herdr

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// liveHerdrSkipReason reports why the live tier should be skipped, or "" when it
// should run. Split out from requireLiveHerdr so the gating decision itself is
// testable without a t.Skip that unwinds the calling goroutine.
func liveHerdrSkipReason(short, herdrInstalled bool, fastUnit, liveTests string) string {
	if short {
		return "skipping live herdr test in -short mode"
	}
	if !herdrInstalled {
		return "herdr not installed"
	}
	if strings.TrimSpace(liveTests) == "1" {
		return ""
	}
	if strings.TrimSpace(fastUnit) == "0" {
		return ""
	}
	return "skipping live herdr journey in unit lane; set GC_FAST_UNIT=0 or GC_HERDR_LIVE_TESTS=1, or run `make test-herdr-live`"
}

// requireLiveHerdr gates the package's live journeys, which drive a real herdr
// server: they place panes, force agent-status reports, bounce the server, and
// assert on the wire event stream.
//
// Presence of the binary is not the precondition these tests actually need.
// What they need is a herdr whose behavior matches the contract they assert,
// and that is not something a guard can probe cheaply: herdr 0.8.0 made the
// agent registry detection-based, so a plain shell pane is never registered and
// agent lookups correctly report not-found. Gating on the binary alone made the
// result depend on which herdr happened to be installed, and since CI has no
// herdr, a version bump turns every local `make test` red while CI stays green.
//
// So this tier is opt-in. `make test` runs ./... with GC_FAST_UNIT=1 and no
// -short, and TESTING.md places live journeys in explicit profile lanes rather
// than the fast unit sweep. `make test-herdr-live` is the lane that runs them;
// scripts/test-integration-shard also sets GC_FAST_UNIT=0.
func requireLiveHerdr(t *testing.T) {
	t.Helper()
	_, err := exec.LookPath("herdr")
	if reason := liveHerdrSkipReason(
		testing.Short(),
		err == nil,
		os.Getenv("GC_FAST_UNIT"),
		os.Getenv("GC_HERDR_LIVE_TESTS"),
	); reason != "" {
		t.Skip(reason)
	}
}

func TestLiveHerdrSkipReason(t *testing.T) {
	for _, tc := range []struct {
		name      string
		short     bool
		installed bool
		fastUnit  string
		liveTests string
		wantRun   bool
	}{
		{name: "short mode skips even when opted in", short: true, installed: true, fastUnit: "0", liveTests: "1"},
		{name: "missing binary skips even when opted in", installed: false, fastUnit: "0", liveTests: "1"},
		{name: "unit lane skips", installed: true, fastUnit: "1"},
		{name: "unset skips", installed: true},
		{name: "GC_FAST_UNIT=0 runs", installed: true, fastUnit: "0", wantRun: true},
		{name: "GC_HERDR_LIVE_TESTS=1 runs", installed: true, fastUnit: "1", liveTests: "1", wantRun: true},
		{name: "opt-in is whitespace tolerant", installed: true, fastUnit: "1", liveTests: " 1 ", wantRun: true},
		{name: "GC_HERDR_LIVE_TESTS=0 does not opt in", installed: true, fastUnit: "1", liveTests: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason := liveHerdrSkipReason(tc.short, tc.installed, tc.fastUnit, tc.liveTests)
			if gotRun := reason == ""; gotRun != tc.wantRun {
				t.Fatalf("short=%v installed=%v GC_FAST_UNIT=%q GC_HERDR_LIVE_TESTS=%q: run=%v want run=%v (reason %q)",
					tc.short, tc.installed, tc.fastUnit, tc.liveTests, gotRun, tc.wantRun, reason)
			}
		})
	}
}
