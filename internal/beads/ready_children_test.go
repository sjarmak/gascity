package beads

import (
	"testing"
	"time"
)

func TestReadyDirectChildrenAppliesReadinessPolicy(t *testing.T) {
	now := time.Now().UTC()
	deferredUntil := now.Add(time.Hour)
	store := NewMemStoreFrom(3, []Bead{
		{
			ID:        "gc-ready",
			Type:      "step",
			Status:    "open",
			ParentID:  "gc-root",
			CreatedAt: now,
		},
		{
			ID:         "gc-deferred",
			Type:       "step",
			Status:     "open",
			ParentID:   "gc-root",
			CreatedAt:  now,
			DeferUntil: &deferredUntil,
		},
		{
			ID:        "gc-missing-blocker",
			Type:      "step",
			Status:    "open",
			ParentID:  "gc-root",
			CreatedAt: now,
		},
	}, []Dep{{
		IssueID:     "gc-missing-blocker",
		DependsOnID: "gc-absent",
		Type:        "blocks",
	}})

	got, err := ReadyDirectChildren(store, "gc-root", "step", TierBoth)
	if err != nil {
		t.Fatalf("ReadyDirectChildren: %v", err)
	}
	if len(got) != 1 || got[0].ID != "gc-ready" {
		t.Fatalf("ReadyDirectChildren = %+v, want only gc-ready", got)
	}
}
