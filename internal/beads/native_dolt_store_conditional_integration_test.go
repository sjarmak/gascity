//go:build integration

package beads_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/beads/beadstest"
)

func openRealNativeDoltStoreForConditionalWriter(t *testing.T) beads.Store {
	t.Helper()
	store, err := beads.OpenNativeDoltStoreForConditionalConformance(
		context.Background(),
		filepath.Join(t.TempDir(), ".beads"),
		"conditional-writer-conformance",
	)
	if err != nil {
		t.Fatalf("open upstream native beads storage: %v", err)
	}
	t.Cleanup(func() {
		if err := store.CloseStore(); err != nil {
			t.Errorf("close upstream native beads storage: %v", err)
		}
	})
	return store
}

// TestNativeDoltStoreConditionalWriterConformance runs the revision-fenced
// lifecycle contract against real Dolt, whose transactions can settle the
// suite's contention requirements.
func TestNativeDoltStoreConditionalWriterConformance(t *testing.T) {
	beadstest.RunConditionalWriterConformanceWithOptions(
		t,
		"NativeDoltStore",
		openRealNativeDoltStoreForConditionalWriter,
		beadstest.ConditionalWriterOptions{SuppliesCurrent: true},
	)
}

func TestNativeDoltStoreUpdateIfMatchSupportsLabelsAndParent(t *testing.T) {
	store := openRealNativeDoltStoreForConditionalWriter(t)
	writer := store.(beads.ConditionalWriter)
	parent, err := store.Create(beads.Bead{Title: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.Create(beads.Bead{Title: "child", Labels: []string{"old"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.UpdateIfMatch(child.ID, child.Revision, beads.UpdateOpts{
		Labels:       []string{"new"},
		RemoveLabels: []string{"old"},
		ParentID:     &parent.ID,
	}); err != nil {
		t.Fatalf("UpdateIfMatch labels+parent: %v", err)
	}
	got, err := store.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentID != parent.ID {
		t.Fatalf("ParentID = %q, want %q", got.ParentID, parent.ID)
	}
	if len(got.Labels) != 1 || got.Labels[0] != "new" {
		t.Fatalf("Labels = %v, want [new]", got.Labels)
	}
}

func TestNativeDoltStoreDeleteIfMatchClearsLocalStrings(t *testing.T) {
	store := openRealNativeDoltStoreForConditionalWriter(t)
	created, err := store.Create(beads.Bead{Title: "delete sidecar cleanup"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetLocalString(created.ID, "lease", "stale"); err != nil {
		t.Fatalf("SetLocalString: %v", err)
	}
	writer := store.(beads.ConditionalWriter)
	if err := writer.DeleteIfMatch(created.ID, created.Revision); err != nil {
		t.Fatalf("DeleteIfMatch: %v", err)
	}
	if got, err := store.GetLocalString(created.ID, "lease"); err != nil || got != "" {
		t.Fatalf("GetLocalString after delete = (%q, %v), want (empty, nil)", got, err)
	}
}
