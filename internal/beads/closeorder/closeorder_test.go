package closeorder

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// newStoreWithBeads returns a MemStore seeded with one open bead per id, so
// DepAdd/DepList have real rows to work against.
func newStoreWithBeads(t *testing.T, ids ...string) *beads.MemStore {
	t.Helper()
	store := beads.NewMemStore()
	for _, id := range ids {
		if _, err := store.Create(beads.Bead{ID: id, Title: id}); err != nil {
			t.Fatalf("seeding bead %q: %v", id, err)
		}
	}
	return store
}

// blocks records that blocked depends on (is blocked by) blocker, i.e.
// blocker must close before blocked in Order's output.
func blocks(t *testing.T, store *beads.MemStore, blocked, blocker string) {
	t.Helper()
	if err := store.DepAdd(blocked, blocker, "blocks"); err != nil {
		t.Fatalf("DepAdd(%q, %q, blocks): %v", blocked, blocker, err)
	}
}

func TestOrderNilStoreReturnsIDsUnchanged(t *testing.T) {
	ids := []string{"c", "a", "b"}
	out, err := Order(nil, ids)
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, ids) {
		t.Fatalf("Order(nil, %v) = %v, want unchanged input", ids, out)
	}
}

func TestOrderEmptyIDs(t *testing.T) {
	store := newStoreWithBeads(t)
	out, err := Order(store, nil)
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("Order(store, nil) = %v, want empty", out)
	}
}

func TestOrderSingletonSkipsLookup(t *testing.T) {
	// A store whose DepList always errors would fail this test if Order
	// queried it — the single-id short-circuit must never call DepList.
	store := erroringStore{err: errors.New("must not be called")}
	out, err := Order(store, []string{"solo"})
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, []string{"solo"}) {
		t.Fatalf("Order(store, [solo]) = %v, want [solo]", out)
	}
}

func TestOrderStableWhenUnconstrained(t *testing.T) {
	ids := []string{"c", "a", "b"}
	store := newStoreWithBeads(t, ids...)
	out, err := Order(store, ids)
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, ids) {
		t.Fatalf("Order(store, %v) = %v, want input order preserved when nothing blocks", ids, out)
	}
}

func TestOrderBlockerBeforeBlocked(t *testing.T) {
	// Input order lists the blocked bead ("b") ahead of its blocker ("a"),
	// so a naive priority-only sort would emit b first. Order must still
	// place the blocker first because "a" is not ready until "b"... no,
	// the reverse: b depends on a, so a must close before b regardless of
	// input position.
	ids := []string{"b", "a", "c"}
	store := newStoreWithBeads(t, ids...)
	blocks(t, store, "b" /* blocked */, "a" /* blocker */)

	out, err := Order(store, ids)
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("Order(store, %v) = %v, want %v (blocker before blocked, independent id keeps its relative priority)", ids, out, want)
	}
}

func TestOrderChainOfBlockers(t *testing.T) {
	// c blocked by b, b blocked by a: only valid close order is a, b, c
	// regardless of how the ids were listed.
	ids := []string{"c", "b", "a"}
	store := newStoreWithBeads(t, ids...)
	blocks(t, store, "c", "b")
	blocks(t, store, "b", "a")

	out, err := Order(store, ids)
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("Order(store, %v) = %v, want %v", ids, out, want)
	}
}

func TestOrderIgnoresExternalBlockerNotInSet(t *testing.T) {
	// "b" depends on "z", but z is not part of the closing batch. That
	// external edge must not block b or error the call.
	ids := []string{"b", "a"}
	store := newStoreWithBeads(t, "a", "b", "z")
	blocks(t, store, "b", "z")

	out, err := Order(store, ids)
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, ids) {
		t.Fatalf("Order(store, %v) = %v, want %v (external edge ignored)", ids, out, ids)
	}
}

func TestOrderIgnoresSelfEdge(t *testing.T) {
	ids := []string{"a", "b"}
	store := newStoreWithBeads(t, ids...)
	blocks(t, store, "a", "a") // self-dependency must not deadlock Order

	out, err := Order(store, ids)
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, ids) {
		t.Fatalf("Order(store, %v) = %v, want %v (self edge ignored)", ids, out, ids)
	}
}

func TestOrderOnlyHonorsBlocksEdgeType(t *testing.T) {
	// A "relates-to" edge from b to a must not force a before b, unlike a
	// "blocks" edge.
	ids := []string{"b", "a"}
	store := newStoreWithBeads(t, ids...)
	if err := store.DepAdd("b", "a", "relates-to"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}

	out, err := Order(store, ids)
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, ids) {
		t.Fatalf("Order(store, %v) = %v, want %v (non-blocks edge ignored)", ids, out, ids)
	}
}

func TestOrderPropagatesDependencyLookupError(t *testing.T) {
	wantErr := errors.New("backend unavailable")
	store := erroringStore{err: wantErr}

	out, err := Order(store, []string{"a", "b"})
	if out != nil {
		t.Fatalf("Order: got out=%v on error, want nil", out)
	}
	if err == nil {
		t.Fatal("Order: expected an error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Order error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestOrderCycleFallsBackToInputOrder(t *testing.T) {
	// a and b block each other: neither can ever become ready, so Order
	// must fall back to input order instead of hanging or erroring.
	ids := []string{"a", "b"}
	store := newStoreWithBeads(t, ids...)
	blocks(t, store, "a", "b")
	blocks(t, store, "b", "a")

	out, err := Order(store, ids)
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(out, ids) {
		t.Fatalf("Order(store, %v) = %v, want %v (cycle falls back to input order)", ids, out, ids)
	}
}

func TestOrderCycleFallbackOnlyAppendsUnresolvedRemainder(t *testing.T) {
	// x is resolvable and must be emitted first; a and b then form an
	// unresolvable cycle and fall back to their relative input order.
	ids := []string{"x", "a", "b"}
	store := newStoreWithBeads(t, ids...)
	blocks(t, store, "a", "b")
	blocks(t, store, "b", "a")

	out, err := Order(store, ids)
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	want := []string{"x", "a", "b"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("Order(store, %v) = %v, want %v", ids, out, want)
	}
}

// TestOrderDuplicateIDsAreDeduplicated locks in Order's actual contract for
// a duplicate id in the input: the duplicate collapses into a single
// occurrence in the output (keyed by id, as the internal emitted/priority
// maps are) rather than being rejected or preserved as two output entries.
// Callers must not assume len(Order(...)) == len(ids) when ids may contain
// duplicates.
func TestOrderDuplicateIDsAreDeduplicated(t *testing.T) {
	store := newStoreWithBeads(t, "a", "b")

	out, err := Order(store, []string{"a", "a", "b"})
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	want := []string{"a", "b"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("Order(store, [a a b]) = %v, want %v (duplicate collapsed)", out, want)
	}
}

func TestOrderDuplicateBlockedIDRespectsBlocker(t *testing.T) {
	store := newStoreWithBeads(t, "a", "b")
	blocks(t, store, "b", "a")

	out, err := Order(store, []string{"b", "a", "b"})
	if err != nil {
		t.Fatalf("Order: unexpected error: %v", err)
	}
	want := []string{"a", "b"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("Order(store, [b a b]) = %v, want %v", out, want)
	}
}

// erroringStore is a minimal beads.Store double for exercising DepList
// failure paths without standing up a MemStore. Order only calls DepList,
// so every other method is left as the embedded nil interface and would
// panic if invoked — a deliberate signal that a test exercising a different
// method needs a real store instead.
type erroringStore struct {
	beads.Store
	err error
}

func (s erroringStore) DepList(id, _ string) ([]beads.Dep, error) {
	return nil, fmt.Errorf("depList(%s): %w", id, s.err)
}
