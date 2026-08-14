//go:build integration

package beads_test

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestNativeDoltStoreBlockedStatusGuardUsesSameCanonicalObservation(t *testing.T) {
	store := openRealNativeDoltStoreForConditionalWriter(t)
	reader := store.(beads.BlockedStatusReader)
	writer := store.(beads.BlockedStatusConditionalWriter)

	candidate, err := store.Create(beads.Bead{Title: "candidate"})
	if err != nil {
		t.Fatal(err)
	}
	if err := beads.CommitNativeDoltStoreForConditionalConformance(
		context.Background(), store.(*beads.NativeDoltStore), "blocked-status-test",
	); err != nil {
		t.Fatalf("commit candidate fixture: %v", err)
	}

	observed := readBlockedStatusObservation(t, reader, candidate.ID)
	if observed.IsBlocked || observed.Status != "open" {
		t.Fatalf("observed = %#v, want raw open and unblocked", observed)
	}
	stale := observed
	stale.IsBlocked = true
	err = writer.UpdateBlockedStatusIfMatch(stale, "blocked", map[string]string{
		"gc.blocked_status_projection": "v1",
		"gc.blocked_status_preimage":   "open",
	}, nil)
	if !beads.IsPreconditionFailed(err) {
		t.Fatalf("stale guarded update error = %v, want PreconditionFailedError", err)
	}

	got := readBlockedStatusObservation(t, reader, candidate.ID)
	if got.Status != "open" || got.IsBlocked {
		t.Fatalf("after stale update = %#v, want raw open and unblocked", got)
	}
	if len(got.Metadata) != 0 {
		t.Fatalf("metadata after stale update = %#v, want none", got.Metadata)
	}
}

func TestNativeDoltStoreBlockedStatusGuardRestoresLifecycleAndUnsetsMarkersAtomically(t *testing.T) {
	store := openRealNativeDoltStoreForConditionalWriter(t)
	reader := store.(beads.BlockedStatusReader)
	writer := store.(beads.BlockedStatusConditionalWriter)

	created, err := store.Create(beads.Bead{Title: "projected blocked status"})
	if err != nil {
		t.Fatal(err)
	}
	blocked := "blocked"
	if err := store.Update(created.ID, beads.UpdateOpts{
		Status: &blocked,
		Metadata: map[string]string{
			"gc.blocked_status_projection": "v1",
			"gc.blocked_status_preimage":   "in_progress",
		},
	}); err != nil {
		t.Fatalf("seed projected blocked status: %v", err)
	}
	if err := beads.CommitNativeDoltStoreForConditionalConformance(
		context.Background(), store.(*beads.NativeDoltStore), "blocked-status-test",
	); err != nil {
		t.Fatalf("commit projected fixture: %v", err)
	}

	observed := readBlockedStatusObservation(t, reader, created.ID)
	if observed.Status != "blocked" || observed.IsBlocked {
		t.Fatalf("observed = %#v, want raw blocked and unblocked", observed)
	}
	if err := writer.UpdateBlockedStatusIfMatch(observed, "in_progress", nil, []string{
		"gc.blocked_status_preimage", "gc.blocked_status_projection",
	}); err != nil {
		t.Fatalf("restore projected lifecycle: %v", err)
	}

	got := readBlockedStatusObservation(t, reader, created.ID)
	if got.Status != "in_progress" || got.IsBlocked {
		t.Fatalf("restored = %#v, want raw in_progress and unblocked", got)
	}
	if len(got.Metadata) != 0 {
		t.Fatalf("restored metadata = %#v, want markers absent", got.Metadata)
	}
}

func TestNativeDoltStoreBlockedStatusSnapshotIncludesCustomNonclosedStatus(t *testing.T) {
	store := openRealNativeDoltStoreForConditionalWriter(t)
	reader := store.(beads.BlockedStatusReader)

	created, err := store.Create(beads.Bead{Title: "custom lifecycle status"})
	if err != nil {
		t.Fatal(err)
	}
	custom := "awaiting_review"
	if err := store.Update(created.ID, beads.UpdateOpts{Status: &custom}); err != nil {
		t.Fatalf("seed custom status: %v", err)
	}
	if err := beads.CommitNativeDoltStoreForConditionalConformance(
		context.Background(), store.(*beads.NativeDoltStore), "blocked-status-test",
	); err != nil {
		t.Fatalf("commit custom-status fixture: %v", err)
	}

	observed := readBlockedStatusObservation(t, reader, created.ID)
	if observed.Status != custom || observed.IsBlocked {
		t.Fatalf("observed = %#v, want raw custom status and unblocked", observed)
	}
}

func readBlockedStatusObservation(t *testing.T, reader beads.BlockedStatusReader, id string) beads.BlockedStatusObservation {
	t.Helper()
	snapshot, err := reader.ReadBlockedStatusSnapshot(100)
	if err != nil {
		t.Fatalf("ReadBlockedStatusSnapshot: %v", err)
	}
	if !snapshot.Complete {
		t.Fatal("snapshot unexpectedly incomplete")
	}
	for _, observation := range snapshot.Observations {
		if observation.ID == id {
			return observation
		}
	}
	t.Fatalf("observation %q not found", id)
	return beads.BlockedStatusObservation{}
}
