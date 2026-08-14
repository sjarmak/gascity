package main

import (
	"os"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// newNoScaleCheckNamedBackingCity builds a city with a single rig pool agent
// that has min=0 and NO custom scale_check, backed by one on_demand named
// session. This shape mirrors Voxist's coordinator/planner pools: the session
// identity is the rig-scoped name (e.g. "rig-A/planner") and routed demand
// lives in the city store.
func newNoScaleCheckNamedBackingCity(t *testing.T) (cfg *config.City, cityStore beads.Store, rigStores map[string]beads.Store, identity string) {
	t.Helper()
	rigPath := t.TempDir() + "/rigs/rig-A"
	if err := os.MkdirAll(rigPath, 0o755); err != nil {
		t.Fatal(err)
	}
	maxSess := 5
	minSess := 0
	cfg = &config.City{
		Agents: []config.Agent{
			{
				Name:              "planner",
				MaxActiveSessions: &maxSess,
				MinActiveSessions: &minSess,
				// No ScaleCheck: default-probe pool.
				Dir:      "rig-A",
				Provider: "mock",
			},
		},
		NamedSessions: []config.NamedSession{{
			Template: "planner",
			Dir:      "rig-A",
			Mode:     "on_demand",
		}},
		Rigs:      []config.Rig{{Name: "rig-A", Path: rigPath}},
		Providers: map[string]config.ProviderSpec{"mock": {Command: "true"}},
	}
	cityStore = beads.NewMemStore()
	rigStores = map[string]beads.Store{"rig-A": beads.NewMemStore()}
	return cfg, cityStore, rigStores, "rig-A/planner"
}

func newSplitDesiredStateRuntime(t *testing.T, cfg *config.City, workStore, sessionStore beads.Store, rigStores map[string]beads.Store) *CityRuntime {
	t.Helper()
	cityPath := t.TempDir()
	return &CityRuntime{
		storageRoutes:          messagingSplitRoutes(sessionStore),
		cityPath:               cityPath,
		cityName:               "test-city",
		cfg:                    cfg,
		sp:                     &localMockProvider{},
		stderr:                 os.Stderr,
		standaloneCityStore:    workStore,
		standaloneRigStores:    rigStores,
		buildFnWithClassStores: supervisorBuildAgentsFnWithClassStores(cityPath, "test-city", os.Stderr),
	}
}

func TestCityRuntimeBuildDesiredStateUsesWorkLedgerForNamedDemandWhenSessionsAreSplit(t *testing.T) {
	cfg, workStore, rigStores, identity := newNoScaleCheckNamedBackingCity(t)
	workStore.(*beads.MemStore).IDPrefix = "work"
	if _, err := workStore.Create(beads.Bead{
		ID:       "work-routed-1",
		Status:   "open",
		Type:     "task",
		Metadata: map[string]string{"gc.routed_to": identity},
	}); err != nil {
		t.Fatal(err)
	}

	sessionStore := beads.NewMemStore()
	sessionStore.IDPrefix = "session"
	cr := newSplitDesiredStateRuntime(t, cfg, workStore, sessionStore, rigStores)

	sessionBeads := newSessionBeadSnapshot(nil)
	result := cr.buildDesiredState(sessionBeads, nil)

	if got := result.ScaleCheckCounts[identity]; got != 1 {
		t.Fatalf("ScaleCheckCounts[%q] = %d, want 1 for routed demand in the Work ledger", identity, got)
	}
	infos := sessionBeads.OpenInfos()
	if len(infos) != 1 {
		t.Fatalf("session snapshot has %d open rows, want 1 materialized in the session ledger", len(infos))
	}
	if _, err := sessionStore.Get(infos[0].ID); err != nil {
		t.Fatalf("session %q not found in session ledger: %v", infos[0].ID, err)
	}
	if _, err := workStore.Get(infos[0].ID); err == nil {
		t.Fatalf("session %q leaked into Work ledger", infos[0].ID)
	}
}

func TestCityRuntimeBuildDesiredStateRetainsGraphLedgerDemandWhenWorkIsSplit(t *testing.T) {
	cfg, workStore, rigStores, identity := newNoScaleCheckNamedBackingCity(t)
	sessionStore := beads.NewMemStore()
	if _, err := sessionStore.Create(beads.Bead{
		Status: "open",
		Type:   "task",
		Metadata: map[string]string{
			"gc.kind":                        "workflow",
			"gc.routed_to":                   identity,
			beadmeta.RootStoreRefMetadataKey: "city:test-city",
		},
	}); err != nil {
		t.Fatal(err)
	}

	cr := newSplitDesiredStateRuntime(t, cfg, workStore, sessionStore, rigStores)

	result := cr.buildDesiredState(newSessionBeadSnapshot(nil), nil)

	if got := result.ScaleCheckCounts[identity]; got != 1 {
		t.Fatalf("ScaleCheckCounts[%q] = %d, want 1 for routed graph demand in the class ledger", identity, got)
	}
	if len(result.State) != 1 {
		t.Fatalf("desired sessions = %d, want 1 for routed graph demand", len(result.State))
	}
	if len(result.ReadyUnassignedRoutedWorkBeads) != 1 {
		t.Fatalf("ready routed graph work = %d, want 1 for the idle-claim backstop", len(result.ReadyUnassignedRoutedWorkBeads))
	}
	if got := result.ReadyUnassignedRoutedWorkStoreRefs; len(got) != 1 || got[0] != "city:test-city" {
		t.Fatalf("ready routed graph refs = %v, want [city:test-city]", got)
	}
}

func TestCityRuntimeBuildDesiredStateUnionsWorkAndGraphDemand(t *testing.T) {
	cfg, workStore, rigStores, identity := newNoScaleCheckNamedBackingCity(t)
	cfg.NamedSessions = nil
	workStore.(*beads.MemStore).IDPrefix = "work"
	if _, err := workStore.Create(beads.Bead{
		Status:   "open",
		Type:     "task",
		Metadata: map[string]string{"gc.routed_to": identity},
	}); err != nil {
		t.Fatal(err)
	}
	sessionStore := beads.NewMemStore()
	sessionStore.IDPrefix = "graph"
	if _, err := sessionStore.Create(beads.Bead{
		Status: "open",
		Type:   "task",
		Metadata: map[string]string{
			"gc.kind":                        "workflow",
			"gc.routed_to":                   identity,
			beadmeta.RootStoreRefMetadataKey: "rig:rig-A",
		},
	}); err != nil {
		t.Fatal(err)
	}

	result := newSplitDesiredStateRuntime(t, cfg, workStore, sessionStore, rigStores).
		buildDesiredState(newSessionBeadSnapshot(nil), nil)

	if got := result.ScaleCheckCounts[identity]; got != 2 {
		t.Fatalf("ScaleCheckCounts[%q] = %d, want the union of one Work and one graph direction", identity, got)
	}
	if len(result.ReadyUnassignedRoutedWorkBeads) != 2 {
		t.Fatalf("ready routed union = %d, want both Work and graph directions", len(result.ReadyUnassignedRoutedWorkBeads))
	}
	foundRigGraph := false
	for i, bead := range result.ReadyUnassignedRoutedWorkBeads {
		if bead.Metadata[beadmeta.RootStoreRefMetadataKey] != "rig:rig-A" {
			continue
		}
		foundRigGraph = i < len(result.ReadyUnassignedRoutedWorkStoreRefs) && result.ReadyUnassignedRoutedWorkStoreRefs[i] == "rig:rig-A"
	}
	if !foundRigGraph {
		t.Fatalf("ready routed refs = %v, want the graph direction under rig:rig-A", result.ReadyUnassignedRoutedWorkStoreRefs)
	}
}

func TestCollectOpenUnassignedRoutedWorkSplitUsesLiveClassCopy(t *testing.T) {
	const sharedID = "shared-graph-direction"
	staleWork := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: sharedID, Status: "open", Type: "task", Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey:     "rig-A/planner",
			beadmeta.RootStoreRefMetadataKey: "city:test-city",
		},
	}}, nil)
	liveClass := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: sharedID, Status: "open", Type: "task", Metadata: map[string]string{
			beadmeta.KindMetadataKey:         beadmeta.KindWorkflow,
			beadmeta.RoutedToMetadataKey:     "rig-A/planner",
			beadmeta.RootStoreRefMetadataKey: "city:test-city",
		},
	}}, nil)
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}

	work, stores, refs, partial := collectOpenUnassignedRoutedWorkWithClassStores(
		cfg, staleWork, nil, nil, liveClass, os.Stderr,
	)

	if partial {
		t.Fatal("split routed-work collection reported partial on healthy stores")
	}
	if len(work) != 1 || len(stores) != 1 || stores[0] != liveClass {
		t.Fatalf("collected stores = %v for work %v, want only the authoritative class binding", stores, work)
	}
	if len(refs) != 1 || refs[0] != "city:test-city" {
		t.Fatalf("collected refs = %v, want [city:test-city]", refs)
	}
}

func TestCollectAssignedWorkSplitUsesLiveClassCopyWhenRetainedClassDrifts(t *testing.T) {
	const sharedID = "shared-assigned-graph-direction"
	staleWork := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: sharedID, Status: "in_progress", Type: "task", Assignee: "rig-A/planner-1", Metadata: map[string]string{
			beadmeta.RoutedToMetadataKey:     "rig-A/planner",
			beadmeta.RootStoreRefMetadataKey: "rig:rig-A",
		},
	}}, nil)
	liveClass := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID: sharedID, Status: "in_progress", Type: "task", Assignee: "rig-A/planner-1", Metadata: map[string]string{
			beadmeta.KindMetadataKey:         beadmeta.KindWorkflow,
			beadmeta.RoutedToMetadataKey:     "rig-A/planner",
			beadmeta.RootStoreRefMetadataKey: "rig:rig-A",
		},
	}}, nil)
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Rigs:      []config.Rig{{Name: "rig-A", Path: t.TempDir()}},
		Agents:    []config.Agent{{Name: "planner", Dir: "rig-A"}},
	}

	work, stores, refs, ready, partial := collectAssignedWorkBeadsWithClassStores(
		cfg, beads.NewMemStore(), map[string]beads.Store{"rig-A": staleWork}, nil, nil, liveClass,
	)

	if partial {
		t.Fatal("split assigned-work collection reported partial on healthy stores")
	}
	if len(work) != 1 || work[0].ID != sharedID || len(stores) != 1 || stores[0] != liveClass {
		t.Fatalf("assigned work/stores = %v / %v, want only the authoritative class row", work, stores)
	}
	if len(refs) != 1 || refs[0] != "rig-A" {
		t.Fatalf("assigned refs = %v, want [rig-A]", refs)
	}
	if !ready[storeScopedBeadKey{StoreRef: "rig-A", ID: sharedID}] {
		t.Fatalf("ready assigned keys = %v, want authoritative rig-scoped key", ready)
	}
}

func TestCollectAssignedWorkSplitUsesLogicalRigScopeFromClassRow(t *testing.T) {
	workStore := beads.NewMemStore()
	classStore := beads.NewMemStore()
	assigned, err := classStore.Create(beads.Bead{
		Status:   "in_progress",
		Type:     "task",
		Assignee: "rig-A/planner-1",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey:         beadmeta.KindWorkflow,
			beadmeta.RootStoreRefMetadataKey: "rig:rig-A",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Rigs:      []config.Rig{{Name: "rig-A", Path: t.TempDir()}},
		Agents:    []config.Agent{{Name: "planner", Dir: "rig-A"}},
	}

	work, stores, refs, ready, partial := collectAssignedWorkBeadsWithClassStores(
		cfg, workStore, nil, nil, nil, classStore,
	)

	if partial {
		t.Fatal("split assigned-work collection reported partial on healthy stores")
	}
	if len(work) != 1 || work[0].ID != assigned.ID || len(stores) != 1 || stores[0] != classStore {
		t.Fatalf("assigned work/stores = %v / %v, want the class-store row", work, stores)
	}
	if len(refs) != 1 || refs[0] != "rig-A" {
		t.Fatalf("assigned refs = %v, want bare rig ref [rig-A]", refs)
	}
	if !ready[storeScopedBeadKey{StoreRef: "rig-A", ID: assigned.ID}] {
		t.Fatalf("ready assigned keys = %v, want rig-scoped key for %s", ready, assigned.ID)
	}
	if !assignedWorkIndexReachableFromAgent(t.TempDir(), cfg, &cfg.Agents[0], refs, 0) {
		t.Fatal("rig-A session cannot reach its assigned graph row after logical scope projection")
	}
}

func TestClassStoreCandidateLogicalRefs(t *testing.T) {
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	work := beads.Bead{Type: "task"}
	graph := beads.Bead{Type: "task", Metadata: map[string]string{
		beadmeta.KindMetadataKey:         beadmeta.KindWorkflow,
		beadmeta.RootStoreRefMetadataKey: "rig:rig-A",
	}}
	cityGraph := graph
	cityGraph.Metadata = map[string]string{
		beadmeta.KindMetadataKey:         beadmeta.KindWorkflow,
		beadmeta.RootStoreRefMetadataKey: "city:test-city",
	}

	for _, tc := range []struct {
		name      string
		bead      beads.Bead
		candidate classStoreCandidate
		canonical string
		assigned  string
		ok        bool
	}{
		{name: "city work", bead: work, candidate: classStoreCandidate{ref: "city", role: classStoreRoleWork}, canonical: "city:test-city", ok: true},
		{name: "rig work", bead: work, candidate: classStoreCandidate{ref: "rig:rig-A", role: classStoreRoleWork}, canonical: "rig:rig-A", assigned: "rig-A", ok: true},
		{name: "rig infrastructure", bead: graph, candidate: classStoreCandidate{ref: "city", role: classStoreRoleInfrastructure}, canonical: "rig:rig-A", assigned: "rig-A", ok: true},
		{name: "city infrastructure", bead: cityGraph, candidate: classStoreCandidate{ref: "city", role: classStoreRoleInfrastructure}, canonical: "city:test-city", ok: true},
		{name: "retained graph rejected by Work", bead: graph, candidate: classStoreCandidate{ref: "rig:rig-A", role: classStoreRoleWork}},
		{name: "work rejected by infrastructure", bead: work, candidate: classStoreCandidate{ref: "city", role: classStoreRoleInfrastructure}},
		{name: "wrong city rejected", bead: beads.Bead{Type: "task", Metadata: map[string]string{beadmeta.KindMetadataKey: beadmeta.KindWorkflow, beadmeta.RootStoreRefMetadataKey: "city:other"}}, candidate: classStoreCandidate{ref: "city", role: classStoreRoleInfrastructure}},
		{name: "malformed ref rejected", bead: beads.Bead{Type: "task", Metadata: map[string]string{beadmeta.KindMetadataKey: beadmeta.KindWorkflow, beadmeta.RootStoreRefMetadataKey: "rig:"}}, candidate: classStoreCandidate{ref: "city", role: classStoreRoleInfrastructure}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical, ok := canonicalStoreRefForCandidate(cfg, tc.bead, tc.candidate)
			if canonical != tc.canonical || ok != tc.ok {
				t.Fatalf("canonical ref = (%q, %v), want (%q, %v)", canonical, ok, tc.canonical, tc.ok)
			}
			assigned, assignedOK := assignedStoreRefForCandidate(cfg, tc.bead, tc.candidate)
			if assigned != tc.assigned || assignedOK != tc.ok {
				t.Fatalf("assigned ref = (%q, %v), want (%q, %v)", assigned, assignedOK, tc.assigned, tc.ok)
			}
		})
	}

	if got, ok := physicalCandidateStoreRef(nil, "city"); got != "city" || !ok {
		t.Fatalf("legacy city fallback = (%q, %v), want (city, true)", got, ok)
	}
	if got, ok := physicalCandidateStoreRef(cfg, "unknown:scope"); got != "" || ok {
		t.Fatalf("malformed physical ref = (%q, %v), want rejected", got, ok)
	}
}

func TestBuildDesiredStateSplitWakesRigControlDispatcherFromClassBinding(t *testing.T) {
	maxActive := 1
	minActive := 0
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city"},
		Rigs:      []config.Rig{{Name: "rig-A", Path: t.TempDir()}},
		Agents: []config.Agent{{
			Name:              config.ControlDispatcherAgentName,
			BindingName:       "core",
			Dir:               "rig-A",
			StartCommand:      config.ControlDispatcherStartCommandFor("{{.Agent}}"),
			MaxActiveSessions: &maxActive,
			MinActiveSessions: &minActive,
		}},
	}
	classStore := beads.NewMemStore()
	control, err := classStore.Create(beads.Bead{
		Status: "open",
		Type:   "task",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey:         beadmeta.KindWorkflowFinalize,
			beadmeta.RoutedToMetadataKey:     "core.control-dispatcher",
			beadmeta.RootBeadIDMetadataKey:   "graph-root-1",
			beadmeta.RootStoreRefMetadataKey: "rig:rig-A",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	workStore := beads.NewMemStore()
	retainedRigWork := beads.NewMemStoreFrom(1, []beads.Bead{{
		ID:     control.ID,
		Status: "open",
		Type:   "task",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey:         beadmeta.KindWorkflowFinalize,
			beadmeta.RoutedToMetadataKey:     "core.control-dispatcher",
			beadmeta.RootBeadIDMetadataKey:   "graph-root-1",
			beadmeta.RootStoreRefMetadataKey: "rig:rig-A",
		},
	}}, nil)

	result := newSplitDesiredStateRuntime(t, cfg, workStore, classStore, map[string]beads.Store{"rig-A": retainedRigWork}).
		buildDesiredState(newSessionBeadSnapshot(nil), nil)

	const route = "rig-A/core.control-dispatcher"
	stored, err := classStore.Get(control.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.Metadata[beadmeta.RoutedToMetadataKey]; got != route {
		t.Fatalf("live class route = %q, want repaired route %q", got, route)
	}
	stale, err := retainedRigWork.Get(control.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := stale.Metadata[beadmeta.RoutedToMetadataKey]; got != "core.control-dispatcher" {
		t.Fatalf("retained Work copy route = %q, want unchanged stale source", got)
	}
	if got := result.ScaleCheckCounts[route]; got != 1 {
		t.Fatalf("ScaleCheckCounts[%q] = %d, want 1 from rig-scoped graph control", route, got)
	}
	foundDesired := false
	for _, desired := range result.State {
		foundDesired = foundDesired || desired.TemplateName == route
	}
	if !foundDesired {
		t.Fatalf("desired state = %v, want a live %q dispatcher", result.State, route)
	}
	if len(result.ReadyUnassignedRoutedWorkBeads) != 1 || len(result.ReadyUnassignedRoutedWorkStoreRefs) != 1 || result.ReadyUnassignedRoutedWorkStoreRefs[0] != "rig:rig-A" {
		t.Fatalf("ready control work/refs = %v / %v, want one rig:rig-A direction", result.ReadyUnassignedRoutedWorkBeads, result.ReadyUnassignedRoutedWorkStoreRefs)
	}
}

// TestBuildDesiredState_ScaleFromZero_NoScaleCheck_CrossStore_NamedPath is the
// regression guard for vp-cl4 — the named-session-backed path symmetry gap.
// A cold rig pool that backs a named session and has no custom scale_check must
// cold-wake from routed demand in the CITY store (the vp-kvp cross-store
// delivery model) just as a generic no-scale_check pool does after vp-s37.
// The routed work still wakes the backing pool, not the named-session alias.
// Before the fix the named-path branch did not add the city-store probe, so a
// sleeping named-backing rig pool never woke on cross-store demand.
func TestBuildDesiredState_ScaleFromZero_NoScaleCheck_CrossStore_NamedPath(t *testing.T) {
	cfg, cityStore, rigStores, identity := newNoScaleCheckNamedBackingCity(t)

	if _, err := cityStore.Create(beads.Bead{
		ID:       "bead-city-1",
		Status:   "open",
		Type:     "task",
		Metadata: map[string]string{"gc.routed_to": identity},
	}); err != nil {
		t.Fatal(err)
	}

	result := buildDesiredStateWithSessionBeads(
		"test-city", t.TempDir(), time.Now(), cfg, &localMockProvider{},
		cityStore, rigStores, &sessionBeadSnapshot{}, nil, os.Stderr,
	)

	if result.NamedSessionDemand[identity] {
		t.Errorf("cross-store cold-wake: NamedSessionDemand[%q] = true, want false for pool-routed work", identity)
	}
	if got := result.ScaleCheckCounts[identity]; got != 1 {
		t.Errorf("cross-store cold-wake demand = %d, want 1 (city-store routed bead must wake cold named-backing pool)", got)
	}
	if len(result.State) < 1 {
		t.Errorf("desired sessions = %d, want >= 1 (backing pool must be materialized)", len(result.State))
	}
}

// TestBuildDesiredState_ScaleFromZero_NoScaleCheck_NamedPath_OwnRigStillWakes
// guards that the existing own-rig-store wake path is preserved for named-backing
// pools after the cross-store fix is applied.
func TestBuildDesiredState_ScaleFromZero_NoScaleCheck_NamedPath_OwnRigStillWakes(t *testing.T) {
	cfg, cityStore, rigStores, identity := newNoScaleCheckNamedBackingCity(t)

	if _, err := rigStores["rig-A"].Create(beads.Bead{
		ID:       "bead-rig-1",
		Status:   "open",
		Type:     "task",
		Metadata: map[string]string{"gc.routed_to": identity},
	}); err != nil {
		t.Fatal(err)
	}

	result := buildDesiredStateWithSessionBeads(
		"test-city", t.TempDir(), time.Now(), cfg, &localMockProvider{},
		cityStore, rigStores, &sessionBeadSnapshot{}, nil, os.Stderr,
	)

	if result.NamedSessionDemand[identity] {
		t.Errorf("own-rig cold-wake: NamedSessionDemand[%q] = true, want false for pool-routed work", identity)
	}
	if got := result.ScaleCheckCounts[identity]; got != 1 {
		t.Errorf("own-rig cold-wake demand = %d, want 1", got)
	}
}

// TestBuildDesiredState_ScaleFromZero_NoScaleCheck_NamedPath_NoDemandNoWake
// guards that the cross-store probe does not spuriously wake a cold named-backing
// pool when there is no routed demand anywhere.
func TestBuildDesiredState_ScaleFromZero_NoScaleCheck_NamedPath_NoDemandNoWake(t *testing.T) {
	cfg, cityStore, rigStores, identity := newNoScaleCheckNamedBackingCity(t)

	result := buildDesiredStateWithSessionBeads(
		"test-city", t.TempDir(), time.Now(), cfg, &localMockProvider{},
		cityStore, rigStores, &sessionBeadSnapshot{}, nil, os.Stderr,
	)

	if result.NamedSessionDemand[identity] {
		t.Errorf("no-demand: NamedSessionDemand[%q] = true, want false (must not spuriously wake)", identity)
	}
	if len(result.State) != 0 {
		t.Errorf("desired sessions = %d, want 0", len(result.State))
	}
}

// TestBuildDesiredState_ScaleFromZero_NoScaleCheck_NamedPath_MissingRigStoreNoCrossWake
// guards the missing-rig-store contract: when a cold named-backing pool's own
// rig store is unreachable, cross-store (city) demand must NOT wake it.
func TestBuildDesiredState_ScaleFromZero_NoScaleCheck_NamedPath_MissingRigStoreNoCrossWake(t *testing.T) {
	cfg, cityStore, _, identity := newNoScaleCheckNamedBackingCity(t)

	if _, err := cityStore.Create(beads.Bead{
		ID:       "bead-city-1",
		Status:   "open",
		Type:     "task",
		Metadata: map[string]string{"gc.routed_to": identity},
	}); err != nil {
		t.Fatal(err)
	}

	// Rig store absent (nil map): the own-rig target is unavailable.
	result := buildDesiredStateWithSessionBeads(
		"test-city", t.TempDir(), time.Now(), cfg, &localMockProvider{},
		cityStore, nil, &sessionBeadSnapshot{}, nil, os.Stderr,
	)

	if result.NamedSessionDemand[identity] {
		t.Errorf("missing rig store: NamedSessionDemand[%q] = true, want false (must not cross-store-wake without rig store)", identity)
	}
}

// TestBuildDesiredState_ScaleFromZero_NoScaleCheck_NamedPath_AliasedRigStoreNoDoubleWake
// guards the alias defense: if the rig store aliases the city store (same
// object), the cross-store city probe must be skipped so one city-store bead
// does not produce duplicate demand signals.
func TestBuildDesiredState_ScaleFromZero_NoScaleCheck_NamedPath_AliasedRigStoreNoDoubleWake(t *testing.T) {
	cfg, cityStore, _, identity := newNoScaleCheckNamedBackingCity(t)

	if _, err := cityStore.Create(beads.Bead{
		ID:       "shared-1",
		Status:   "open",
		Type:     "task",
		Metadata: map[string]string{"gc.routed_to": identity},
	}); err != nil {
		t.Fatal(err)
	}

	// Rig store IS the city store (aliased).
	aliased := map[string]beads.Store{"rig-A": cityStore}
	result := buildDesiredStateWithSessionBeads(
		"test-city", t.TempDir(), time.Now(), cfg, &localMockProvider{},
		cityStore, aliased, &sessionBeadSnapshot{}, nil, os.Stderr,
	)

	// With an aliased store, demand should still be detected (it's a real bead)
	// but must not be double-counted.
	if result.NamedSessionDemand[identity] {
		t.Errorf("aliased-store: NamedSessionDemand[%q] = true, want false for pool-routed work", identity)
	}
	if got := result.ScaleCheckCounts[identity]; got != 1 {
		t.Errorf("aliased-store demand = %d, want 1 (aliased store bead must still be detected once)", got)
	}
}
