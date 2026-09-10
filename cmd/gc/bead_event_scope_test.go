package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
)

func TestBeadEventExplicitScopeSelectsOpenedStore(t *testing.T) {
	city, rig, class := beads.NewMemStore(), beads.NewMemStore(), beads.NewMemStore()
	cs := &controllerState{cityPath: "/city", cfg: &config.City{
		Workspace: config.Workspace{Name: "test"},
		Rigs:      []config.Rig{{Name: "alpha", Path: "/rig", Prefix: "ra"}},
	}, cityBeadStore: city, beadStores: map[string]beads.Store{"alpha": rig}, storageRoutes: splitRoutes(class)}
	for _, tc := range []struct {
		ref  string
		want beads.Store
	}{
		{"city:test", city},
		{"rig:alpha", rig},
		{"rig:missing", nil},
		{"city:other", nil},
		{"class:gmnos", class},
		{"class:g", nil},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			got := cs.beadEventStoresLocked(events.Event{Subject: "ra-same", SubjectStoreRef: tc.ref, Payload: json.RawMessage(`{"id":"ra-same"}`)})
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("unknown scope routed to %v", got)
				}
				return
			}
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("scope %s routed to %v, want selected opened store", tc.ref, got)
			}
		})
	}
}

func TestScopedClassCloseStillAutoclosesCanonicalRoot(t *testing.T) {
	store := beads.NewMemStore()
	root, err := store.Create(beads.Bead{Title: "root", Type: "molecule"})
	if err != nil {
		t.Fatal(err)
	}
	step, err := store.Create(beads.Bead{Title: "step", Type: "step", ParentID: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(step.ID); err != nil {
		t.Fatal(err)
	}
	step, err = store.Get(step.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := beads.EncodeBeadEventPayload(step)
	if err != nil {
		t.Fatal(err)
	}
	prev := beadCloseAutocloseDispatch
	beadCloseAutocloseDispatch = func(fn func()) { fn() }
	t.Cleanup(func() { beadCloseAutocloseDispatch = prev })
	cs := &controllerState{cityBeadStore: beads.NewMemStore(), storageRoutes: splitRoutes(store)}
	cs.applyBeadEventToStores(events.Event{Type: events.BeadClosed, Subject: step.ID, SubjectStoreRef: "class:gmnos", Payload: payload})
	got, err := store.Get(root.ID)
	if err != nil || got.Status != "closed" {
		t.Fatalf("canonical root got=%+v err=%v", got, err)
	}
}

func TestForeignScopedCloseCannotAlterCacheOrDispatchAutoclose(t *testing.T) {
	cache := beads.NewCachingStoreForTest(beads.NewMemStore(), nil)
	if err := cache.Prime(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs := &controllerState{cityPath: "/city", cfg: &config.City{Workspace: config.Workspace{Name: "test"}}, cityBeadStore: cache}
	prev := beadCloseAutocloseDispatch
	dispatched := 0
	beadCloseAutocloseDispatch = func(func()) { dispatched++ }
	t.Cleanup(func() { beadCloseAutocloseDispatch = prev })
	for _, evt := range []events.Event{
		{Type: events.BeadClosed, Subject: "gc-same", SubjectStoreRef: "city:foreign", Payload: json.RawMessage(`{"id":"gc-same","status":"closed"}`)},
		{Type: events.BeadClosed, Subject: "gc-same", SubjectStoreRef: "city:test", Payload: json.RawMessage(`{"id":"gc-other","status":"closed"}`)},
	} {
		cs.applyBeadEventToStores(evt)
	}
	items, err := cache.List(beads.ListQuery{AllowScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 || dispatched != 0 {
		t.Fatalf("rejected events changed cache=%v or dispatched autoclose=%d", items, dispatched)
	}
}

func TestScopedBeadEventAcceptsCanonicalAndWrappedSnapshots(t *testing.T) {
	for _, payload := range []string{`{"id":"same","title":"updated","status":"open"}`, `{"bead":{"id":"same","title":"updated","status":"open"}}`} {
		t.Run(payload, func(t *testing.T) {
			city := beads.NewCachingStoreForTest(beads.NewMemStore(), nil)
			rig := beads.NewCachingStoreForTest(beads.NewMemStore(), nil)
			for _, s := range []*beads.CachingStore{city, rig} {
				if err := s.Prime(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			cs := &controllerState{cityPath: "/city", cfg: &config.City{Workspace: config.Workspace{Name: "test"}, Rigs: []config.Rig{{Name: "alpha"}}}, cityBeadStore: city, beadStores: map[string]beads.Store{"alpha": rig}}
			cs.applyBeadEventToStores(events.Event{Type: events.BeadCreated, Subject: "same", SubjectStoreRef: "rig:alpha", Payload: json.RawMessage(payload)})
			if got, err := rig.Get("same"); err != nil || got.Title != "updated" {
				t.Fatalf("selected cache got=%+v err=%v", got, err)
			}
			if _, err := city.Get("same"); err == nil {
				t.Fatal("unselected cache received event")
			}
		})
	}
}
