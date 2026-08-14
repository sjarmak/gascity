package beads

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

// echoProjectionStore is a backing store that serves bd's is_blocked column the
// way a real bd/dolt backend does: every active row comes back with a non-nil
// verdict. That column is what the reconcile-echo loop feeds on, so no fake
// without it can reproduce the defect.
type echoProjectionStore struct {
	Store
	blocked map[string]bool
}

// interface in caching_store.go; this fake serves an in-memory verdict map and
// has no failure mode. Siblings carry an injectable error field because their
// tests exercise projection failure; the echo tests do not, and adding one here
// would be machinery nothing reads.
//
//nolint:unparam // the error is required by the enrichReadyProjectionForCache
func (s *echoProjectionStore) enrichReadyProjectionForCache(items []Bead) ([]Bead, error) {
	out := make([]Bead, 0, len(items))
	for _, item := range items {
		verdict := s.blocked[item.ID]
		item.IsBlocked = &verdict
		out = append(out, item)
	}
	return out, nil
}

// echoLoop wires a CachingStore's onChange back into its own ApplyEvent, which
// is exactly what cmd/gc's controller does: wrapWithCachingStore records every
// notification onto the city event bus, and applyBeadEventToStores hands each
// bead event straight back to the CachingStore that owns the id.
type echoLoop struct {
	mu     sync.Mutex
	cache  *CachingStore
	counts []map[string]int
	cycle  int
}

func (e *echoLoop) startCycle() {
	e.mu.Lock()
	e.counts = append(e.counts, map[string]int{})
	e.cycle = len(e.counts) - 1
	e.mu.Unlock()
}

func (e *echoLoop) onChange(eventType, _ string, payload json.RawMessage) {
	e.mu.Lock()
	if e.cycle < len(e.counts) {
		e.counts[e.cycle][eventType]++
	}
	cache := e.cache
	e.mu.Unlock()
	if cache != nil {
		cache.ApplyEvent(eventType, payload)
	}
}

func (e *echoLoop) cycleCounts(i int) map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts[i]
}

func newEchoLoopCache(t *testing.T, beadCount int, withDeps bool) (*CachingStore, *echoLoop, Store, []string) {
	t.Helper()
	mem := NewMemStore()
	ids := make([]string, 0, beadCount)
	for i := 0; i < beadCount; i++ {
		b, err := mem.Create(Bead{Type: "task", Status: "open", Title: "work"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids = append(ids, b.ID)
	}
	blocked := map[string]bool{}
	if withDeps && len(ids) >= 2 {
		if err := mem.DepAdd(ids[1], ids[0], "blocks"); err != nil {
			t.Fatalf("DepAdd: %v", err)
		}
		blocked[ids[1]] = true
	}

	loop := &echoLoop{}
	cache := NewCachingStoreForTest(&echoProjectionStore{Store: mem, blocked: blocked}, loop.onChange)
	loop.cache = cache
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	return cache, loop, mem, ids
}

// seedOneRealChange mutates the backing store behind the cache's back so the
// next reconcile pass has exactly one genuine bead.updated to emit. Without a
// first event there is no echo to sustain, and a primed cache over a quiet
// store is silent either way; the defect only shows once one thing has changed.
func seedOneRealChange(t *testing.T, backing Store, id string) {
	t.Helper()
	title := "changed behind the cache"
	if err := backing.Update(id, UpdateOpts{Title: &title}); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

// TestReconcileEchoDoesNotResurrectItself pins the root cause of dr-l09kl: the
// city's event log grew ~145MB per 13 minutes because the reconciler re-emitted
// a byte-identical bead.updated for every active bead, every cycle, forever.
//
// The loop: reconcile lists the backing store, the ready projection stamps a
// non-nil is_blocked on every row, beadChanged sees cached.IsBlocked == nil and
// synthesizes bead.updated. The controller records that event and hands it
// straight back to the same CachingStore, whose ApplyEvent then clears the very
// projection the reconcile just installed (clearDependentReadyProjectionsLocked
// fired on the mere PRESENCE of a status field, and the deps-unknown degrade
// fired on the mere ABSENCE of a dependencies field). is_blocked is nil again,
// so the next cycle re-emits the identical payload. Nothing in the store ever
// changed.
//
// One real change must produce one event and then silence. Cycle 1 reports the
// seeded title change; cycles 2..4 run against a backing store nobody has
// touched since, so they must emit nothing at all.
func TestReconcileEchoDoesNotResurrectItself(t *testing.T) {
	cache, loop, backing, ids := newEchoLoopCache(t, 4, true)
	seedOneRealChange(t, backing, ids[2])

	for cycle := 0; cycle < 4; cycle++ {
		loop.startCycle()
		cache.runReconciliation()
	}

	if got := loop.cycleCounts(0); got["bead.updated"] != 1 || len(got) != 1 {
		t.Fatalf("first reconcile cycle emitted %v, want exactly one bead.updated for the seeded change; "+
			"the test proves nothing if the change never reached the event stream", got)
	}
	for cycle := 1; cycle < 4; cycle++ {
		got := loop.cycleCounts(cycle)
		if len(got) != 0 {
			t.Fatalf("reconcile cycle %d emitted %v against a backing store unchanged since cycle 1; "+
				"a steady store must produce no events (dr-l09kl)", cycle+1, got)
		}
	}
}

// TestReconcileEchoKeepsTheReadyProjectionItJustInstalled pins the second half
// of the loop. An update event that omits dependencies has to mark the bead's
// dep coverage unknown — that omission is how bd reports a dependency mutation,
// and TestCachingStoreReadyFallsBackAfterDependencyOmittingUpdateEvent pins both
// directions of it. What it must NOT do is throw away bd's is_blocked verdict
// for the bead, which the reconcile pass installed moments earlier and which no
// dependency-omitting payload contradicts. Clearing it made every echo look like
// a change to the next reconcile pass.
func TestReconcileEchoKeepsTheReadyProjectionItJustInstalled(t *testing.T) {
	cache, loop, backing, ids := newEchoLoopCache(t, 3, false)
	seedOneRealChange(t, backing, ids[0])

	loop.startCycle()
	cache.runReconciliation()

	cache.mu.RLock()
	blockedKnown := 0
	for _, b := range cache.beads {
		if b.IsBlocked != nil {
			blockedKnown++
		}
	}
	cache.mu.RUnlock()

	if blockedKnown != 3 {
		t.Errorf("rows with a known is_blocked = %d, want 3: the echo cleared the ready projection "+
			"the reconcile pass had just installed", blockedKnown)
	}
}

// TestStatusChangeStillClearsDependentReadyProjections is the control for the
// clearDependentReadyProjectionsLocked narrowing. Gating on a REAL status
// change must not stop a real status change from invalidating the dependents'
// cached is_blocked — that projection is derived from the blocker's status, so
// a blocker moving to in_progress or closed has to knock it down.
func TestStatusChangeStillClearsDependentReadyProjections(t *testing.T) {
	mem := NewMemStore()
	blocker, err := mem.Create(Bead{Type: "task", Status: "open", Title: "blocker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dependent, err := mem.Create(Bead{Type: "task", Status: "open", Title: "dependent"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mem.DepAdd(dependent.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	cache := NewCachingStoreForTest(&echoProjectionStore{Store: mem, blocked: map[string]bool{dependent.ID: true}}, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	cache.runReconciliation()

	cache.mu.RLock()
	before := cache.beads[dependent.ID].IsBlocked
	cache.mu.RUnlock()
	if before == nil {
		t.Fatal("setup: dependent's is_blocked was not installed by the reconcile pass")
	}

	payload, err := json.Marshal(Bead{ID: blocker.ID, Type: "task", Status: "in_progress", Title: "blocker", CreatedAt: blocker.CreatedAt})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	cache.ApplyEvent("bead.updated", payload)

	cache.mu.RLock()
	after := cache.beads[dependent.ID].IsBlocked
	status := cache.beads[blocker.ID].Status
	cache.mu.RUnlock()
	if status != "in_progress" {
		t.Fatalf("blocker status after the event = %q, want in_progress", status)
	}
	if after != nil {
		t.Fatal("dependent's is_blocked survived a real status change of its blocker; " +
			"the projection is derived from that status and must be invalidated")
	}
}

// TestReCreatedEventDoesNotInvalidateDependents covers the bead.created arm of
// the same echo. The reconciler synthesizes bead.created for a row it has just
// installed; when that event comes back the row is already cached, nothing is
// absorbed, and re-announcing it cannot have changed any dependent's
// blocked-ness. Clearing the dependents' is_blocked anyway made every new bead
// cost a fresh round of re-emissions.
func TestReCreatedEventDoesNotInvalidateDependents(t *testing.T) {
	mem := NewMemStore()
	blocker, err := mem.Create(Bead{Type: "task", Status: "open", Title: "blocker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dependent, err := mem.Create(Bead{Type: "task", Status: "open", Title: "dependent"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mem.DepAdd(dependent.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	cache := NewCachingStoreForTest(&echoProjectionStore{Store: mem, blocked: map[string]bool{dependent.ID: true}}, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	cache.runReconciliation()

	cache.mu.RLock()
	seeded := cache.beads[dependent.ID].IsBlocked
	cache.mu.RUnlock()
	if seeded == nil {
		t.Fatal("setup: dependent's is_blocked was not installed by the reconcile pass")
	}

	payload, err := json.Marshal(Bead{ID: blocker.ID, Type: "task", Status: "open", Title: "blocker", CreatedAt: blocker.CreatedAt})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	cache.ApplyEvent("bead.created", payload)

	cache.mu.RLock()
	after := cache.beads[dependent.ID].IsBlocked
	cache.mu.RUnlock()
	if after == nil {
		t.Fatal("a bead.created event for a bead the cache already held cleared its dependents' is_blocked; " +
			"only a row this event actually installed can invalidate them")
	}
}

// TestDependencyOmittingEventStillDegradesDepCoverage is the control for the
// deps-unknown change. Keeping is_blocked must not weaken the coverage verdict:
// an update event that omits dependencies still drops the deps row and clears
// depsComplete, because that omission is how bd reports a dependency mutation
// in either direction.
func TestDependencyOmittingEventStillDegradesDepCoverage(t *testing.T) {
	mem := NewMemStore()
	blocker, err := mem.Create(Bead{Type: "task", Status: "open", Title: "blocker"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dependent, err := mem.Create(Bead{Type: "task", Status: "open", Title: "dependent"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mem.DepAdd(dependent.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("DepAdd: %v", err)
	}
	cache := NewCachingStoreForTest(&echoProjectionStore{Store: mem, blocked: map[string]bool{dependent.ID: true}}, nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}

	cache.mu.RLock()
	if !cache.depsComplete {
		cache.mu.RUnlock()
		t.Fatal("setup: prime left depsComplete false")
	}
	cache.mu.RUnlock()

	// The hook payload after `bd dep remove`: a complete-looking bead snapshot
	// with no dependencies field at all.
	payload, err := json.Marshal(Bead{ID: dependent.ID, Type: "task", Status: "open", Title: "dependent", CreatedAt: dependent.CreatedAt})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	cache.ApplyEvent("bead.updated", payload)

	cache.mu.RLock()
	depsComplete := cache.depsComplete
	_, stillCached := cache.deps[dependent.ID]
	cache.mu.RUnlock()

	if depsComplete {
		t.Error("depsComplete stayed true after an update event omitted the dependencies of a bead " +
			"the cache holds dependencies for; that omission is how bd reports a removal")
	}
	if stillCached {
		t.Error("the cache kept its dependency snapshot for a bead whose update event omitted dependencies; " +
			"it must fall back to the backing store")
	}
}
