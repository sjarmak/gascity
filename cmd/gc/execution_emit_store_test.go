package main

import (
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/coordclass"
	"github.com/gastownhall/gascity/internal/executionevent"
	"github.com/gastownhall/gascity/internal/storeref"
)

func TestExecutionEmitStoreLabelsBindingFallback(t *testing.T) {
	cityPath, _ := foreignProviderCity(t)
	work := workStoreFor(t, cityPath)
	shadow, err := work.Create(beads.Bead{Title: "retained work copy", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}
	resident, _ := classResidentWorkShapedBead(t, cityPath, shadow.ID, "binding copy")
	reader := executionEmitStore(nonResidentBeadStore{Store: work, hidden: resident.ID}, cityPath)
	row, ref, err := executionevent.ReadWithStoreRef(reader, resident.ID)
	want := string(storeref.ClassRef(infrastructureClasses()))
	if err != nil || row.Title != "binding copy" || ref != want {
		t.Fatalf("fallback row=%#v ref=%q err=%v, want binding copy with ref %q", row, ref, err, want)
	}
}

func TestExecutionGraphProjectionUsesOpenedRouteIdentity(t *testing.T) {
	work, graph := beads.NewMemStore(), beads.NewMemStore()
	row, err := graph.Create(beads.Bead{})
	if err != nil {
		t.Fatal(err)
	}
	routes := &storageRoutes{stores: map[coordclass.Class]beads.Store{coordclass.ClassGraph: graph}}
	for _, tc := range []struct {
		name          string
		routes        *storageRoutes
		work          beads.Store
		workRef, want string
	}{
		{"relocated", routes, work, "city:test", "class:g"},
		{"collapsed", nil, graph, "rig:project", "rig:project"},
		{"unmatched", nil, work, "city:test", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := executionGraphProjectionStore(tc.routes, tc.work, graph, tc.workRef)
			got, ref, err := executionevent.ReadWithStoreRef(reader, row.ID)
			if err != nil || got.ID != row.ID || ref != tc.want {
				t.Fatalf("row=%#v ref=%q err=%v, want ref %q", got, ref, err, tc.want)
			}
		})
	}
}

// nonResidentBeadStore hides one bead from Get while serving everything else,
// modeling a split-store city where a convoy's tracks edges are readable from
// the store that materialized the molecule while the launch bead they name is
// resident in another store.
type nonResidentBeadStore struct {
	beads.Store
	hidden string
}

func (s nonResidentBeadStore) Get(id string) (beads.Bead, error) {
	if id == s.hidden {
		return beads.Bead{}, beads.ErrNotFound
	}
	return s.Store.Get(id)
}

func TestExecutionEmitStoreAnchorsLaunchesResidentInAnotherStore(t *testing.T) {
	graph := beads.NewMemStore()
	work := beads.NewMemStore()
	rig := beads.NewMemStore()
	work.HonorExplicitIDs = true
	rig.HonorExplicitIDs = true

	convoy, err := work.Create(beads.Bead{ID: "mc-convoy", Type: "convoy"})
	if err != nil {
		t.Fatalf("create convoy: %v", err)
	}
	launch, err := work.Create(beads.Bead{ID: "ga-launch"})
	if err != nil {
		t.Fatalf("create launch placeholder: %v", err)
	}
	if err := work.DepAdd(convoy.ID, launch.ID, "tracks"); err != nil {
		t.Fatalf("add tracks edge: %v", err)
	}
	if _, err := rig.Create(beads.Bead{
		ID:       launch.ID,
		Metadata: map[string]string{beadmeta.SourceBeadIDMetadataKey: "mc-source"},
	}); err != nil {
		t.Fatalf("create rig launch: %v", err)
	}
	root, err := graph.Create(beads.Bead{Metadata: map[string]string{
		beadmeta.KindMetadataKey:            beadmeta.KindWorkflow,
		beadmeta.FormulaContractMetadataKey: beadmeta.FormulaContractGraphV2,
		beadmeta.InputConvoyIDMetadataKey:   convoy.ID,
	}})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}

	primary := nonResidentBeadStore{Store: work, hidden: launch.ID}

	// Without routing, the launch read misses and the anchor is silently
	// dropped — the split-store regression this seam exists to close.
	bare, err := executionevent.ProjectCurrent(
		beads.GraphStore{Store: graph}, beads.WorkStore{Store: primary}, root.ID)
	if err != nil {
		t.Fatalf("bare projection: %v", err)
	}
	if len(bare.WorkAssociations) != 1 || len(bare.RunAnchors) != 0 {
		t.Fatalf("bare projection = %d associations, %d anchors; want the association without an anchor", len(bare.WorkAssociations), len(bare.RunAnchors))
	}

	routed := executionEmitWorkStore{Store: primary, resolveOwning: func(id string) (beads.Store, bool) {
		if id != launch.ID {
			return nil, false
		}
		return rig, true
	}}
	projection, err := executionevent.ProjectCurrent(
		beads.GraphStore{Store: graph}, beads.WorkStore{Store: routed}, root.ID)
	if err != nil {
		t.Fatalf("routed projection: %v", err)
	}
	if len(projection.RunAnchors) != 1 || projection.RunAnchors[0].SourceBeadID != "mc-source" {
		t.Fatalf("routed anchors = %#v, want one anchor on mc-source", projection.RunAnchors)
	}
}

func TestExecutionEmitStorePreservesPrimaryReadsAndMisses(t *testing.T) {
	work := beads.NewMemStore()
	resident, err := work.Create(beads.Bead{ID: "mc-resident"})
	if err != nil {
		t.Fatalf("create resident: %v", err)
	}
	resolved := 0
	routed := executionEmitWorkStore{Store: work, resolveOwning: func(string) (beads.Store, bool) {
		resolved++
		return nil, false
	}}
	if got, err := routed.Get(resident.ID); err != nil || got.ID != resident.ID {
		t.Fatalf("resident read = %v, %v", got, err)
	}
	if resolved != 0 {
		t.Fatalf("resident read consulted the owning-store resolver %d times, want none", resolved)
	}
	if _, err := routed.Get("mc-absent"); !errors.Is(err, beads.ErrNotFound) {
		t.Fatalf("absent read error = %v, want the primary store's not-found", err)
	}
	if resolved != 1 {
		t.Fatalf("absent read consulted the resolver %d times, want once", resolved)
	}
}

type executionScopedTestStore struct {
	beads.Store
	ref string
}

func (s executionScopedTestStore) GetWithStoreRef(id string) (beads.Bead, string, error) {
	row, err := s.Get(id)
	return row, s.ref, err
}

func TestExecutionEmitStorePairsScopeWithResolvedRead(t *testing.T) {
	primary, remote := beads.NewMemStore(), beads.NewMemStore()
	primary.HonorExplicitIDs, remote.HonorExplicitIDs = true, true
	for _, item := range []struct {
		store beads.Store
		id    string
	}{{primary, "gc-primary"}, {remote, "gc-remote"}} {
		if _, err := item.store.Create(beads.Bead{ID: item.id}); err != nil {
			t.Fatal(err)
		}
	}
	resolved := 0
	routed := executionEmitWorkStore{Store: executionScopedTestStore{primary, "city:test"}, resolveOwning: func(string) (beads.Store, bool) {
		resolved++
		return executionScopedTestStore{remote, "rig:remote"}, true
	}}
	for _, tc := range []struct {
		id, ref string
		calls   int
	}{{"gc-primary", "city:test", 0}, {"gc-remote", "rig:remote", 1}, {"gc-absent", "", 2}} {
		row, ref, err := routed.GetWithStoreRef(tc.id)
		if ref != tc.ref || resolved != tc.calls {
			t.Fatalf("%s: ref=%q resolutions=%d", tc.id, ref, resolved)
		}
		if tc.ref != "" && (err != nil || row.ID != tc.id) {
			t.Fatalf("%s: row=%#v err=%v", tc.id, row, err)
		}
		if tc.ref == "" && !errors.Is(err, beads.ErrNotFound) {
			t.Fatalf("missing row: %v", err)
		}
	}
}
