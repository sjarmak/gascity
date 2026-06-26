package main

import (
	"io"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// newStaleOrderMolecule creates an order-run molecule root plus one open step
// stamped with gc.root_bead_id, both created in the distant past, modeling an
// abandoned order subtree whose pool step was never executed (#3407).
func newStaleOrderMolecule(t *testing.T, store beads.Store, orderName string, createdAt time.Time) (root, step beads.Bead) {
	t.Helper()
	root, err := store.Create(beads.Bead{
		Title:     "mol-" + orderName,
		Type:      "molecule",
		Labels:    []string{"order-run:" + orderName},
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("Create(root %s): %v", orderName, err)
	}
	step, err = store.Create(beads.Bead{
		Title:     orderName + " step",
		Type:      "task",
		ParentID:  root.ID,
		CreatedAt: createdAt,
		Metadata:  map[string]string{"gc.root_bead_id": root.ID},
	})
	if err != nil {
		t.Fatalf("Create(step %s): %v", orderName, err)
	}
	return root, step
}

func assertClosed(t *testing.T, store beads.Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		got, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if got.Status != "closed" {
			t.Fatalf("%s status = %q, want closed", id, got.Status)
		}
	}
}

func assertOpen(t *testing.T, store beads.Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		got, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if got.Status != "open" {
			t.Fatalf("%s status = %q, want open", id, got.Status)
		}
	}
}

// TestSweepStaleOrderWispSubtreesAllOrdersReapsEveryOrder is the core #3407
// guarantee: the automatic reaper closes abandoned order-run molecule subtrees
// for EVERY order without being handed a name filter — the case the scoped
// operator sweep (which requires --include-wisps + order names) cannot cover
// from an automatic watchdog.
func TestSweepStaleOrderWispSubtreesAllOrdersReapsEveryOrder(t *testing.T) {
	store := &createdAtOverrideStore{Store: beads.NewMemStore()}
	now := time.Now()
	old := now.Add(-3 * time.Hour)

	rootA, stepA := newStaleOrderMolecule(t, store, "dog-stale-db", old)
	rootB, stepB := newStaleOrderMolecule(t, store, "nightly-digest", old)

	cutoff := now.Add(-2 * time.Hour)
	n, err := sweepStaleOrderWispSubtreesAllOrders(store, cutoff, orderWispSubtreeWatchdogMetadataInitiator)
	if err != nil {
		t.Fatalf("sweepStaleOrderWispSubtreesAllOrders: %v", err)
	}
	if n != 4 {
		t.Fatalf("reaped %d beads, want 4 (two roots + two steps)", n)
	}
	assertClosed(t, store, rootA.ID, stepA.ID, rootB.ID, stepB.ID)
}

// TestSweepStaleOrderWispSubtreesAllOrdersSparesFreshSubtree proves the
// freshness veto still holds under the unscoped path: a subtree with any open
// bead newer than the cutoff is genuinely in-flight and must not be reaped,
// even while a sibling abandoned subtree is.
func TestSweepStaleOrderWispSubtreesAllOrdersSparesFreshSubtree(t *testing.T) {
	store := &createdAtOverrideStore{Store: beads.NewMemStore()}
	now := time.Now()

	staleRoot, staleStep := newStaleOrderMolecule(t, store, "dog-stale-db", now.Add(-3*time.Hour))
	// In-flight: root is old but its step was just created (live work).
	freshRoot, err := store.Create(beads.Bead{
		Title:     "mol-active-order",
		Type:      "molecule",
		Labels:    []string{"order-run:active-order"},
		CreatedAt: now.Add(-3 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Create(fresh root): %v", err)
	}
	freshStep, err := store.Create(beads.Bead{
		Title:     "active step",
		Type:      "task",
		ParentID:  freshRoot.ID,
		CreatedAt: now.Add(-1 * time.Minute),
		Metadata:  map[string]string{"gc.root_bead_id": freshRoot.ID},
	})
	if err != nil {
		t.Fatalf("Create(fresh step): %v", err)
	}

	cutoff := now.Add(-2 * time.Hour)
	n, err := sweepStaleOrderWispSubtreesAllOrders(store, cutoff, orderWispSubtreeWatchdogMetadataInitiator)
	if err != nil {
		t.Fatalf("sweepStaleOrderWispSubtreesAllOrders: %v", err)
	}
	if n != 2 {
		t.Fatalf("reaped %d beads, want 2 (only the abandoned subtree)", n)
	}
	assertClosed(t, store, staleRoot.ID, staleStep.ID)
	assertOpen(t, store, freshRoot.ID, freshStep.ID)
}

// TestRunOrderWispSubtreeSweepWatchdogReapsAbandonedSubtree exercises the
// controller watchdog end to end: an abandoned order subtree older than the
// generous window is reaped, and an immediate re-run is interval-gated.
func TestRunOrderWispSubtreeSweepWatchdogReapsAbandonedSubtree(t *testing.T) {
	store := &createdAtOverrideStore{Store: beads.NewMemStore()}
	createdAt := time.Now()
	root, step := newStaleOrderMolecule(t, store, "dog-stale-db", createdAt)

	cr := &CityRuntime{
		cityName:            "test-city",
		cfg:                 &config.City{Workspace: config.Workspace{Name: "test-city"}},
		standaloneCityStore: store,
		stdout:              io.Discard,
		stderr:              io.Discard,
		logPrefix:           "gc test",
	}

	// Before the staleness window elapses, nothing is reaped. (The two calls
	// are spaced beyond orderWispSubtreeWatchdogInterval so the second is not
	// interval-gated.)
	cr.runOrderWispSubtreeSweepWatchdog(createdAt.Add(orderWispSubtreeWatchdogStaleAfter - 10*time.Minute))
	assertOpen(t, store, root.ID, step.ID)

	// Past the window, the abandoned subtree is reaped.
	cr.runOrderWispSubtreeSweepWatchdog(createdAt.Add(orderWispSubtreeWatchdogStaleAfter + 10*time.Minute))
	assertClosed(t, store, root.ID, step.ID)
}

// TestRunOrderWispSubtreeSweepWatchdogIntervalGated confirms the heavier
// full-scan reaper is rate-limited: a second call within the interval is a
// no-op even when fresh abandoned work appears.
func TestRunOrderWispSubtreeSweepWatchdogIntervalGated(t *testing.T) {
	store := &createdAtOverrideStore{Store: beads.NewMemStore()}
	base := time.Now()

	cr := &CityRuntime{
		cityName:            "test-city",
		cfg:                 &config.City{Workspace: config.Workspace{Name: "test-city"}},
		standaloneCityStore: store,
		stdout:              io.Discard,
		stderr:              io.Discard,
		logPrefix:           "gc test",
	}

	// First call stamps the watchdog clock.
	cr.runOrderWispSubtreeSweepWatchdog(base)

	// An abandoned subtree appears, but a re-run within the interval is gated.
	root, step := newStaleOrderMolecule(t, store, "dog-stale-db", base.Add(-3*time.Hour))
	cr.runOrderWispSubtreeSweepWatchdog(base.Add(orderWispSubtreeWatchdogInterval - time.Second))
	assertOpen(t, store, root.ID, step.ID)

	// Once the interval elapses, the reaper runs and closes it.
	cr.runOrderWispSubtreeSweepWatchdog(base.Add(orderWispSubtreeWatchdogInterval + time.Second))
	assertClosed(t, store, root.ID, step.ID)
}
