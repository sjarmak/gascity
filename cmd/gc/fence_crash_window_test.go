package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/molecule"
)

// fencedControlBead is the shape molecule.fenceGraphWorkflowBead leaves behind:
// gc.instantiating set, the live route/assignee/type moved aside into their
// gc.deferred_* companions. Only activateFencedGraphWorkflowBead clears it, so a
// crash or kill between the fence and the activation leaves this on disk.
func fencedControlBead(id string) beads.Bead {
	return beads.Bead{
		ID: id, Title: "fenced step", Type: "gate", Status: "open",
		Metadata: map[string]string{
			beadmeta.InstantiatingMetadataKey:    "true",
			beadmeta.DeferredRoutedToMetadataKey: "city/gastown.polecat",
			beadmeta.DeferredTypeMetadataKey:     "task",
			beadmeta.KindMetadataKey:             "workflow",
		},
	}
}

// A fenced bead is invisible to the control dispatcher forever, so the drop must
// leave a trace line naming it rather than failing silent (gc-og1z criterion 3).
func TestMergeControlReadyGroupsTracesInstantiatingDrop(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "workflow-trace.log")
	t.Setenv("GC_WORKFLOW_TRACE", tracePath)

	merged := mergeControlReadyGroups([]beads.Bead{fencedControlBead("CB-1")})
	if len(merged) != 0 {
		t.Fatalf("merged = %v, want the instantiating bead dropped", merged)
	}

	traceBytes, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	if !strings.Contains(string(traceBytes), "CB-1") {
		t.Fatalf("trace = %q, want a line naming the dropped bead", traceBytes)
	}
}

// The unfence is a CONVERGENCE repair, not a race with a live instantiation: it
// waits for the same consecutive-pass confirmation the quarantine marker uses, so
// a workflow genuinely mid-instantiation during one pass is never disturbed.
func TestBackstopUnfencesStaleInstantiatingAfterConfirmingPasses(t *testing.T) {
	store := beads.NewMemStoreFrom(0, []beads.Bead{fencedControlBead("CB-1")}, nil)
	lane := newRouteRecoveryLane()

	first := lane.backstopLeg(planeLeg{store: store})
	if first.err != nil {
		t.Fatalf("first pass: %v", first.err)
	}
	if first.unfenced != 0 {
		t.Fatalf("first pass unfenced = %d, want 0 (one sighting is not staleness)", first.unfenced)
	}
	if got := mustMeta(t, store, beadmeta.InstantiatingMetadataKey); got != "true" {
		t.Fatalf("after one pass gc.instantiating = %q, want it still fenced", got)
	}

	second := lane.backstopLeg(planeLeg{store: store})
	if second.err != nil {
		t.Fatalf("second pass: %v", second.err)
	}
	if second.unfenced != 1 {
		t.Fatalf("second pass unfenced = %d, want 1", second.unfenced)
	}
	if got := mustMeta(t, store, beadmeta.InstantiatingMetadataKey); got != "" {
		t.Fatalf("gc.instantiating = %q, want cleared", got)
	}
	if got := mustMeta(t, store, beadmeta.RoutedToMetadataKey); got != "city/gastown.polecat" {
		t.Fatalf("gc.routed_to = %q, want the deferred route restored", got)
	}
	if got := mustMeta(t, store, beadmeta.DeferredRoutedToMetadataKey); got != "" {
		t.Fatalf("gc.deferred_routed_to = %q, want consumed", got)
	}
	if b, err := store.Get("CB-1"); err != nil {
		t.Fatalf("get CB-1: %v", err)
	} else if b.Type != "task" {
		t.Fatalf("type = %q, want the deferred type restored", b.Type)
	}
}

// A workflow that instantiates normally between two backstop passes must not
// leave a streak behind that unfences the NEXT fence early.
func TestBackstopUnfenceStreakResetsWhenBeadActivatesItself(t *testing.T) {
	store := beads.NewMemStoreFrom(0, []beads.Bead{fencedControlBead("CB-1")}, nil)
	lane := newRouteRecoveryLane()

	if report := lane.backstopLeg(planeLeg{store: store}); report.unfenced != 0 {
		t.Fatalf("first pass unfenced = %d, want 0", report.unfenced)
	}
	// The owning process finishes and activates the bead itself.
	if _, err := molecule.ActivateFencedGraphWorkflowBead(store, "CB-1"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if report := lane.backstopLeg(planeLeg{store: store}); report.unfenced != 0 {
		t.Fatalf("pass over an activated bead unfenced = %d, want 0", report.unfenced)
	}
	// A fresh fence on the same id starts its streak over.
	if err := store.SetMetadataBatch("CB-1", map[string]string{
		beadmeta.InstantiatingMetadataKey:    "true",
		beadmeta.DeferredRoutedToMetadataKey: "city/gastown.polecat",
	}); err != nil {
		t.Fatalf("re-fence: %v", err)
	}
	if report := lane.backstopLeg(planeLeg{store: store}); report.unfenced != 0 {
		t.Fatalf("first pass after re-fence unfenced = %d, want 0", report.unfenced)
	}
}

func mustMeta(t *testing.T, store beads.Store, key string) string {
	t.Helper()
	b, err := store.Get("CB-1")
	if err != nil {
		t.Fatalf("get CB-1: %v", err)
	}
	return b.Metadata[key]
}
