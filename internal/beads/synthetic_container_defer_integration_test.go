//go:build integration

package beads_test

import (
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// TestSyntheticContainerDeferUntilHoldsConvoyOutOfRealBdReady is the
// integration-row regression for gc-c7lp0 against a REAL bd binary. The unit
// tests in invocation_test.go and drain_test.go only prove DeferUntil gets
// set and that beads.IsDeferred recognizes it; both run against MemStore,
// whose own readyExcludeTypes already hides every "convoy" bead regardless
// of defer_until, so they cannot fail the way the reported bug failed. The
// actual gap is in the vendored bd binary's own ready-work query: its
// default exclude-type list (sqlbuild.ReadyWorkExcludeTypes) does not
// include "convoy", so a bare `bd ready` run directly against the real
// database — exactly how aoa-pl observed aoa-co23v ranking top of the P0
// band — surfaces it unless defer_until is set. This test creates one
// convoy bead with SyntheticContainerDeferUntil() and one without, runs the
// literal `bd ready --json` binary (not BdStore.Ready(), which would apply
// Gas City's own type filter on top and mask the same gap the unit tests
// miss), and asserts only the undeferred control convoy is present.
func TestSyntheticContainerDeferUntilHoldsConvoyOutOfRealBdReady(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skipf("bd not on PATH: %v", err)
	}

	store, dir := newConditionalIntegrationBdStore(t)
	runner := newConditionalIntegrationRunner(dir)

	if out, err := runner(dir, "bd", "config", "set", "types.custom", "convoy"); err != nil {
		t.Fatalf("bd config set types.custom convoy: %v\n%s", err, out)
	}

	held, err := store.Create(beads.Bead{
		Title:      "held convoy",
		Type:       "convoy",
		DeferUntil: beads.SyntheticContainerDeferUntil(),
	})
	if err != nil {
		t.Fatalf("Create(held convoy): %v", err)
	}
	control, err := store.Create(beads.Bead{
		Title: "undeferred control convoy",
		Type:  "convoy",
	})
	if err != nil {
		t.Fatalf("Create(control convoy): %v", err)
	}

	out, err := runner(dir, "bd", "ready", "--json")
	if err != nil {
		t.Fatalf("bd ready --json: %v\n%s", err, out)
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("unmarshal bd ready output: %v\nraw: %s", err, out)
	}

	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row.ID] = true
	}
	if seen[held.ID] {
		t.Errorf("bd ready --json included %s (held convoy with far-future defer_until), want it hidden", held.ID)
	}
	if !seen[control.ID] {
		t.Errorf("bd ready --json omitted %s (undeferred control convoy); the real bd binary's default exclude-type list may have changed to cover \"convoy\" (rendering the defer_until fix redundant, not wrong) or the harness is broken", control.ID)
	}
}
