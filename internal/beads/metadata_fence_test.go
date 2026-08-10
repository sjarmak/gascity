package beads_test

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/fsys"
)

func TestMetadataKeyFencerIgnoresConditionalWritesRollout(t *testing.T) {
	store := beads.NewMemStore()
	created, err := store.Create(beads.Bead{Title: "target", Type: "task", Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	store.DisableConditionalWrites = true
	fencer, ok := beads.MetadataKeyFencerFor(beads.SessionStore{Store: store})
	if !ok {
		t.Fatal("typed session store did not expose metadata fencer")
	}
	swapped, err := fencer.FenceMetadataKey(created.ID, "lifecycle_fence", "", "claimed")
	if err != nil || !swapped {
		t.Fatalf("FenceMetadataKey = (%v, %v), want (true, nil)", swapped, err)
	}
}

func TestFileStoreMetadataKeyFenceIsCrossHandleAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "beads.json")
	first, err := beads.OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := first.Create(beads.Bead{Title: "target", Type: "task", Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := beads.OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatal(err)
	}
	winner, _ := beads.MetadataKeyFencerFor(first)
	loser, _ := beads.MetadataKeyFencerFor(second)
	if swapped, err := winner.FenceMetadataKey(created.ID, "lifecycle_fence", "", "first"); err != nil || !swapped {
		t.Fatalf("winning fence = (%v, %v)", swapped, err)
	}
	if swapped, err := loser.FenceMetadataKey(created.ID, "lifecycle_fence", "", "second"); err != nil || swapped {
		t.Fatalf("losing fence = (%v, %v), want (false, nil)", swapped, err)
	}
}

func TestNativeDoltStoreProvidesMetadataKeyFence(t *testing.T) {
	store := beads.NewNativeDoltStoreForConformance()
	created, err := store.Create(beads.Bead{Title: "target", Type: "task", Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	fencer, ok := beads.MetadataKeyFencerFor(store)
	if !ok {
		t.Fatal("native Dolt store did not expose metadata fencer")
	}
	if swapped, err := fencer.FenceMetadataKey(created.ID, "lifecycle_fence", "", "claimed"); err != nil || !swapped {
		t.Fatalf("winning fence = (%v, %v), want (true, nil)", swapped, err)
	}
	if swapped, err := fencer.FenceMetadataKey(created.ID, "lifecycle_fence", "", "lost"); err != nil || swapped {
		t.Fatalf("losing fence = (%v, %v), want (false, nil)", swapped, err)
	}
}

func TestNativeDoltStoreMetadataKeyFenceHasSingleWinner(t *testing.T) {
	store := beads.NewNativeDoltStoreForConformance()
	created, err := store.Create(beads.Bead{Title: "target", Type: "task", Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	fencer, _ := beads.MetadataKeyFencerFor(store)
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for _, next := range []string{"first", "second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			swapped, err := fencer.FenceMetadataKey(created.ID, "lifecycle_fence", "", next)
			results <- swapped
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	winners := 0
	for swapped := range results {
		if swapped {
			winners++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("fence winners = %d, want 1", winners)
	}
}
