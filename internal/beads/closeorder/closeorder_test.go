package closeorder_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/beads/closeorder"
)

// depListFailStore wraps a real Store and forces DepList to fail for a
// configured set of ids, so error propagation can be exercised without a
// hand-rolled Store fake.
type depListFailStore struct {
	beads.Store
	failIDs map[string]bool
	err     error
}

func (s depListFailStore) DepList(id, direction string) ([]beads.Dep, error) {
	if s.failIDs[id] {
		return nil, s.err
	}
	return s.Store.DepList(id, direction)
}

func TestOrder_NilAndTrivialInputs(t *testing.T) {
	cases := []struct {
		name  string
		store beads.Store
		ids   []string
	}{
		{name: "nil store with multiple ids", store: nil, ids: []string{"b", "a"}},
		{name: "nil ids on real store", store: beads.NewMemStore(), ids: nil},
		{name: "empty ids on real store", store: beads.NewMemStore(), ids: []string{}},
		{name: "singleton on real store", store: beads.NewMemStore(), ids: []string{"only"}},
		{name: "singleton with nil store", store: nil, ids: []string{"only"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := closeorder.Order(tc.store, tc.ids)
			if err != nil {
				t.Fatalf("Order() error = %v, want nil", err)
			}
			if len(got) != len(tc.ids) {
				t.Fatalf("Order() = %v, want unchanged %v", got, tc.ids)
			}
			for i := range tc.ids {
				if got[i] != tc.ids[i] {
					t.Fatalf("Order() = %v, want unchanged %v", got, tc.ids)
				}
			}
		})
	}
}

func TestOrder_StableUnconstrainedOrder(t *testing.T) {
	store := beads.NewMemStore()
	// No "blocks" edges recorded between any of these beads: with nothing
	// constraining relative order, Order must preserve input order exactly.
	ids := []string{"charlie", "alpha", "delta", "bravo"}

	got, err := closeorder.Order(store, ids)
	if err != nil {
		t.Fatalf("Order() error = %v, want nil", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("Order() returned %d ids, want %d: %v", len(got), len(ids), got)
	}
	for i, id := range ids {
		if got[i] != id {
			t.Fatalf("Order() = %v, want input order preserved %v", got, ids)
		}
	}
}

func TestOrder_BlockerBeforeBlocked(t *testing.T) {
	store := beads.NewMemStore()
	// c is blocked by both a and b. Input order deliberately lists the
	// blocked bead first to prove Order reorders around the constraint
	// rather than just echoing input order.
	if err := store.DepAdd("c", "a", "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	if err := store.DepAdd("c", "b", "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}

	got, err := closeorder.Order(store, []string{"c", "a", "b"})
	if err != nil {
		t.Fatalf("Order() error = %v, want nil", err)
	}

	want := []string{"a", "b", "c"}
	if !equalSlices(got, want) {
		t.Fatalf("Order() = %v, want %v", got, want)
	}

	pos := indexOf(got)
	if pos["a"] > pos["c"] || pos["b"] > pos["c"] {
		t.Fatalf("Order() = %v, blockers must precede blocked bead c", got)
	}
}

func TestOrder_ExternalAndSelfEdgesIgnored(t *testing.T) {
	store := beads.NewMemStore()
	// Self-edge: must not deadlock the bead against itself.
	if err := store.DepAdd("a", "a", "blocks"); err != nil {
		t.Fatalf("DepAdd self-edge: %v", err)
	}
	// External edge: "a" depends on "outside", which is not part of the
	// close batch, so it must not constrain ordering or error.
	if err := store.DepAdd("a", "outside", "blocks"); err != nil {
		t.Fatalf("DepAdd external edge: %v", err)
	}
	// Non-"blocks" edge between in-set beads must not constrain ordering.
	if err := store.DepAdd("b", "a", "tracks"); err != nil {
		t.Fatalf("DepAdd tracks edge: %v", err)
	}

	ids := []string{"a", "b"}
	got, err := closeorder.Order(store, ids)
	if err != nil {
		t.Fatalf("Order() error = %v, want nil", err)
	}
	if !equalSlices(got, ids) {
		t.Fatalf("Order() = %v, want input order %v preserved (no real constraint)", got, ids)
	}
}

func TestOrder_DependencyLookupError(t *testing.T) {
	base := beads.NewMemStore()
	wantErr := errors.New("backend unavailable")
	store := depListFailStore{
		Store:   base,
		failIDs: map[string]bool{"b": true},
		err:     wantErr,
	}

	got, err := closeorder.Order(store, []string{"a", "b", "c"})
	if err == nil {
		t.Fatalf("Order() error = nil, want error from failing DepList")
	}
	if got != nil {
		t.Fatalf("Order() = %v, want nil result on error", got)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Order() error = %v, want wrapped %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), `"b"`) {
		t.Fatalf("Order() error = %q, want it to name the failing id %q", err.Error(), "b")
	}
}

func TestOrder_CycleFallback(t *testing.T) {
	store := beads.NewMemStore()
	// a and b mutually block each other: no valid topological order exists,
	// so Order must fall back to input order rather than hang or error.
	if err := store.DepAdd("a", "b", "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	if err := store.DepAdd("b", "a", "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}

	ids := []string{"a", "b"}
	got, err := closeorder.Order(store, ids)
	if err != nil {
		t.Fatalf("Order() error = %v, want nil (cycle must fall back, not error)", err)
	}
	if !equalSlices(got, ids) {
		t.Fatalf("Order() = %v, want cycle fallback to input order %v", got, ids)
	}
}

func TestOrder_CycleFallbackPreservesInputOrderOfRemainder(t *testing.T) {
	store := beads.NewMemStore()
	// a is freely closeable; b and c cycle against each other. Order should
	// close a first, then fall back to input order for the stuck remainder.
	if err := store.DepAdd("b", "c", "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	if err := store.DepAdd("c", "b", "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}

	got, err := closeorder.Order(store, []string{"c", "a", "b"})
	if err != nil {
		t.Fatalf("Order() error = %v, want nil", err)
	}

	want := []string{"a", "c", "b"}
	if !equalSlices(got, want) {
		t.Fatalf("Order() = %v, want %v (a first, then stuck remainder in input order)", got, want)
	}
}

func TestOrder_DuplicateIDsDeduplicated(t *testing.T) {
	store := beads.NewMemStore()
	ids := []string{"a", "a", "b"}

	got, err := closeorder.Order(store, ids)
	if err != nil {
		t.Fatalf("Order() error = %v, want nil", err)
	}

	seen := make(map[string]int, len(got))
	for _, id := range got {
		seen[id]++
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("Order() = %v, id %q appeared %d times, want exactly once", got, id, count)
		}
	}
	wantUnique := []string{"a", "b"}
	if !equalSlices(got, wantUnique) {
		t.Fatalf("Order() = %v, want deduplicated %v", got, wantUnique)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func indexOf(ids []string) map[string]int {
	pos := make(map[string]int, len(ids))
	for i, id := range ids {
		pos[id] = i
	}
	return pos
}
