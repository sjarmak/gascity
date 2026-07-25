package beads

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/gastownhall/gascity/internal/fsys"
)

func TestFileStoreReloadUnchangedContentDoesNotDecode(t *testing.T) {
	f := fsys.NewFake()
	path := "/city/.gc/beads.json"
	beads := make([]Bead, 64)
	for i := range beads {
		beads[i] = Bead{ID: fmt.Sprintf("bead-%d", i), Title: "unchanged"}
	}
	data, err := json.Marshal(fileData{Seq: len(beads), Beads: beads})
	if err != nil {
		t.Fatal(err)
	}
	f.Files[path] = data

	store, err := OpenFileStore(f, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.reloadFromDisk(); err != nil {
		t.Fatal(err)
	}

	var reloadErr error
	allocs := testing.AllocsPerRun(100, func() {
		reloadErr = store.reloadFromDisk()
	})
	if reloadErr != nil {
		t.Fatal(reloadErr)
	}
	if allocs > 10 {
		t.Fatalf("unchanged reload allocated %.0f objects, want <= 10", allocs)
	}
}

func TestFileStoreReloadsRecreatedIdenticalContentAfterDeletion(t *testing.T) {
	f := fsys.NewFake()
	path := "/city/.gc/beads.json"
	store, err := OpenFileStore(f, path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(Bead{Title: "original"})
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), f.Files[path]...)

	if err := f.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(%q) after deletion err = %v, want ErrNotFound", created.ID, err)
	}
	if err := f.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get(%q) after exact recreation: %v", created.ID, err)
	}
	if got.Title != created.Title {
		t.Fatalf("Get(%q) title = %q, want %q", created.ID, got.Title, created.Title)
	}
}
