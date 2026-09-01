package main

import (
	"reflect"
	"slices"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/beads/splittest"
)

// This file expresses gc-6rae5's "ready" surface: filterReadyBeads and its
// caller readyBeadsForOpts (which `gc ready` itself calls) must fence a
// descendant whose graph.v2 workflow root is held, even though the
// descendant carries no hold label of its own -- the same fencing gc-6rae5
// already added to `gc hook` (hook_root_hold.go) and the control-dispatcher
// feeder (dispatch_control_ready.go), extended to the `gc ready` data source.

// TestFilterReadyBeadsExcludesHeldGraphRootDescendant is a direct unit test
// on the predicate: a bead whose gc.root_bead_id names a held root is
// dropped, while a bead under an unheld root and a bead that is its own root
// survive.
func TestFilterReadyBeadsExcludesHeldGraphRootDescendant(t *testing.T) {
	items := []beads.Bead{
		{ID: "ga-under-held-root", Metadata: map[string]string{
			beadmeta.RootBeadIDMetadataKey: "ga-held-root",
		}},
		{ID: "ga-under-unheld-root", Metadata: map[string]string{
			beadmeta.RootBeadIDMetadataKey: "ga-unheld-root",
		}},
		{ID: "ga-is-own-root", Metadata: map[string]string{
			beadmeta.RootBeadIDMetadataKey: "ga-is-own-root",
		}},
	}
	rootHeld := func(rootID string) bool { return rootID == "ga-held-root" }

	got := filterReadyBeads(items, readyOpts{}, nil, rootHeld)
	want := []string{"ga-under-unheld-root", "ga-is-own-root"}
	if !stringSlicesEqual(beadIDs(got), want) {
		t.Fatalf("filterReadyBeads ids = %v, want %v (a descendant of a held graph.v2 root must be excluded; a candidate that is its own root must not be)", beadIDs(got), want)
	}
}

// TestFilterReadyBeadsNilRootHeldIsNoop pins that a nil resolver preserves
// pre-gc-6rae5 behavior, matching every other seam this fix touches.
func TestFilterReadyBeadsNilRootHeldIsNoop(t *testing.T) {
	items := []beads.Bead{
		{ID: "ga-under-root", Metadata: map[string]string{
			beadmeta.RootBeadIDMetadataKey: "ga-some-root",
		}},
	}
	got := filterReadyBeads(items, readyOpts{}, nil, nil)
	want := []string{"ga-under-root"}
	if !stringSlicesEqual(beadIDs(got), want) {
		t.Fatalf("filterReadyBeads ids = %v, want %v (nil rootHeld must be a no-op)", beadIDs(got), want)
	}
}

// TestReadyBeadsForOptsFencesHeldGraphRootDescendant is gc-6rae5 acceptance
// criterion 1 end-to-end on the `gc ready` surface: a descendant of a held
// graph.v2 root must not appear in readyBeadsForOpts's merged result, even
// though the descendant itself carries no hold label.
func TestReadyBeadsForOptsFencesHeldGraphRootDescendant(t *testing.T) {
	store := splittest.NewWorkStore(t, "gc")

	heldRoot := mustCreateReadyBead(t, store, beads.Bead{Title: "held root", Type: "task"})
	if err := store.Update(heldRoot.ID, beads.UpdateOpts{Labels: []string{beadmeta.HoldMayorLabel}}); err != nil {
		t.Fatalf("hold root: %v", err)
	}
	underHeldRoot := mustCreateReadyBead(t, store, beads.Bead{Title: "under held root", Type: "task", Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey: heldRoot.ID,
	}})
	unheldRoot := mustCreateReadyBead(t, store, beads.Bead{Title: "unheld root", Type: "task"})
	underUnheldRoot := mustCreateReadyBead(t, store, beads.Bead{Title: "under unheld root", Type: "task", Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey: unheldRoot.ID,
	}})

	rows, err := readyBeadsForOpts([]readyLeg{readyTestLeg("city", store)}, readyOpts{})
	if err != nil {
		t.Fatalf("gc ready: %v", err)
	}
	got := readyWireIDs(rows)
	slices.Sort(got)
	want := []string{heldRoot.ID, underUnheldRoot.ID, unheldRoot.ID}
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readyBeadsForOpts ids = %v, want %v (descendant %s of held root %s must not be served by gc ready)", got, want, underHeldRoot.ID, heldRoot.ID)
	}
}

// TestReadyBeadsForOptsServesGraphRootDescendantOnceUnheld is gc-6rae5
// acceptance criterion 2 on the `gc ready` path: removing the root's hold
// label makes its descendant routable again.
func TestReadyBeadsForOptsServesGraphRootDescendantOnceUnheld(t *testing.T) {
	store := splittest.NewWorkStore(t, "gc")

	root := mustCreateReadyBead(t, store, beads.Bead{Title: "root", Type: "task"})
	if err := store.Update(root.ID, beads.UpdateOpts{Labels: []string{beadmeta.HoldMayorLabel}}); err != nil {
		t.Fatalf("hold root: %v", err)
	}
	descendant := mustCreateReadyBead(t, store, beads.Bead{Title: "descendant", Type: "task", Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey: root.ID,
	}})

	legs := []readyLeg{readyTestLeg("city", store)}

	rows, err := readyBeadsForOpts(legs, readyOpts{})
	if err != nil {
		t.Fatalf("gc ready (held): %v", err)
	}
	if slices.Contains(readyWireIDs(rows), descendant.ID) {
		t.Fatalf("descendant %s served while root %s is held", descendant.ID, root.ID)
	}

	if err := store.Update(root.ID, beads.UpdateOpts{RemoveLabels: []string{beadmeta.HoldMayorLabel}}); err != nil {
		t.Fatalf("clear root hold: %v", err)
	}

	rows, err = readyBeadsForOpts(legs, readyOpts{})
	if err != nil {
		t.Fatalf("gc ready (unheld): %v", err)
	}
	if !slices.Contains(readyWireIDs(rows), descendant.ID) {
		t.Fatalf("descendant %s not served after clearing hold on root %s", descendant.ID, root.ID)
	}
}
