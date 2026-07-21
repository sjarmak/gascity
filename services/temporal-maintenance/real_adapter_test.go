package temporalmaintenance

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The Phase-1 RealAdapter must fail closed on every Propose so an accidental
// bind can never silently no-op or half-execute a mutation. Phase 2 replaces
// this behaviour with the persisted, approval-gated executor.
func TestRealAdapter_ProposeFailsClosed(t *testing.T) {
	a := NewRealAdapter()
	rec, created, err := a.Propose(context.Background(), ProposedMutation{
		IdempotencyKey: "temporal-shadow/repo/key/review/gh pr merge/1712",
		Action:         "gh pr merge",
		Target:         "1712",
		ProposedAt:     time.Unix(0, 0).UTC(),
	})
	if !errors.Is(err, ErrRealAdapterUnarmed) {
		t.Fatalf("Propose err = %v, want ErrRealAdapterUnarmed", err)
	}
	if created {
		t.Fatalf("Propose created = true, want false for an unarmed adapter")
	}
	if rec.Action != "" || rec.IdempotencyKey != "" || rec.Target != "" || rec.Params != nil {
		t.Fatalf("Propose recorded = %+v, want zero value", rec)
	}
}

// Recorded is empty: an unarmed adapter has proposed nothing.
func TestRealAdapter_RecordedEmpty(t *testing.T) {
	if got := NewRealAdapter().Recorded(); len(got) != 0 {
		t.Fatalf("Recorded() = %v, want empty", got)
	}
}

// Guards the interface seam Phase 2 relies on: RealAdapter is a
// SideEffectAdapter, so the only Phase-2 change is filling in the methods and
// flipping the worker binding.
func TestRealAdapter_ImplementsSideEffectAdapter(t *testing.T) {
	var _ SideEffectAdapter = NewRealAdapter()
}
