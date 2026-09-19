package beads

import (
	"testing"

	"github.com/gastownhall/gascity/internal/fsys"
)

// TestFileStoreReloadSkipsParseWhenFileUnchanged proves the fast path added
// to reloadFromDisk for gc-q9m9tk: two mutating operations in a row, with no
// external change to the file between them, parse (json.Unmarshal) the file
// once rather than twice. It counts parses via the package-internal
// reloadParses counter rather than asserting on timing.
func TestFileStoreReloadSkipsParseWhenFileUnchanged(t *testing.T) {
	f := fsys.NewFake()
	path := "/city/.gc/beads.json"

	s, err := OpenFileStore(f, path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(Bead{Title: "one"}); err != nil {
		t.Fatalf("Create #1: %v", err)
	}
	before := s.reloadParses

	if _, err := s.Create(Bead{Title: "two"}); err != nil {
		t.Fatalf("Create #2: %v", err)
	}
	after := s.reloadParses

	if got := after - before; got != 0 {
		t.Fatalf("reloadFromDisk parses during Create #2 = %d, want 0 (file unchanged since Create #1's own save)", got)
	}
}

// TestFileStoreReloadParsesOnExternalWrite proves the counterpart safety
// property: an external mutation of the file between two mutating operations
// on this handle does force a fresh parse, so the change is never missed.
func TestFileStoreReloadParsesOnExternalWrite(t *testing.T) {
	f := fsys.NewFake()
	path := "/city/.gc/beads.json"

	s, err := OpenFileStore(f, path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := OpenFileStore(f, path)
	if err != nil {
		t.Fatal(err)
	}

	created, err := s.Create(Bead{Title: "one"})
	if err != nil {
		t.Fatalf("Create #1: %v", err)
	}
	// A different handle to the same file (standing in for another process
	// sharing the cross-process flock) mutates it independently.
	if err := other.Update(created.ID, UpdateOpts{Title: reloadTestStrPtr("external")}); err != nil {
		t.Fatalf("external Update: %v", err)
	}

	before := s.reloadParses
	if _, err := s.Create(Bead{Title: "two"}); err != nil {
		t.Fatalf("Create #2: %v", err)
	}
	if got := s.reloadParses - before; got != 1 {
		t.Fatalf("reloadFromDisk parses during Create #2 after external write = %d, want 1", got)
	}

	got, err := s.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "external" {
		t.Fatalf("Title after external write = %q, want external (external write must not be lost)", got.Title)
	}
}

func reloadTestStrPtr(s string) *string { return &s }
