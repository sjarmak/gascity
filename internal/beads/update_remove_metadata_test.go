package beads

import "testing"

func TestUpdateRemoveMetadata(t *testing.T) {
	store := NewMemStore()
	created, err := store.Create(Bead{Title: "item", Metadata: map[string]string{"keep": "yes", "drop": "yes"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(created.ID, UpdateOpts{RemoveMetadata: []string{"drop"}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := got.Metadata["drop"]; present {
		t.Fatalf("removed metadata remained: %v", got.Metadata)
	}
	if got.Metadata["keep"] != "yes" {
		t.Fatalf("unrelated metadata changed: %v", got.Metadata)
	}
}

func TestBDUpdateArgsRemoveMetadata(t *testing.T) {
	got := bdUpdateArgs("gc-1", UpdateOpts{RemoveMetadata: []string{"z", "a"}})
	want := []string{"update", "--json", "gc-1", "--unset-metadata", "a", "--unset-metadata", "z"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestConditionalUpdateRemoveMetadata(t *testing.T) {
	store := NewMemStore()
	created, err := store.Create(Bead{Title: "item", Metadata: map[string]string{"drop": "yes"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateIfMatch(created.ID, created.Revision, UpdateOpts{RemoveMetadata: []string{"drop"}}); err != nil {
		t.Fatalf("UpdateIfMatch(RemoveMetadata): %v", err)
	}
	got, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := got.Metadata["drop"]; present {
		t.Fatalf("removed metadata remained: %v", got.Metadata)
	}
}
