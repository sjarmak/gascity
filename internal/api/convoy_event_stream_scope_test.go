package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
)

// probeStore counts Get calls so a test can assert that a store was never
// asked, not merely that the answer came from somewhere else. The whole point
// of the scoping fix is the ABSENCE of a probe: an assertion that only checks
// the returned bead passes just as happily when every store is swept, which is
// exactly the bug.
type probeStore struct {
	beads.Store
	gets []string
}

func (p *probeStore) Get(id string) (beads.Bead, error) {
	p.gets = append(p.gets, id)
	return p.Store.Get(id)
}

func newProbeStore() *probeStore {
	m := beads.NewMemStore()
	m.HonorExplicitIDs = true
	return &probeStore{Store: m}
}

// scopedFanoutFixture builds a two-rig city with distinct configured bead-ID
// prefixes and a counting store for each scope.
type scopedFanoutFixture struct {
	state *fakeState
	city  *probeStore
	alpha *probeStore
	beta  *probeStore
}

func newScopedFanoutFixture(t *testing.T) *scopedFanoutFixture {
	t.Helper()
	f := &scopedFanoutFixture{
		state: newFakeState(t),
		city:  newProbeStore(),
		alpha: newProbeStore(),
		beta:  newProbeStore(),
	}
	f.state.cityName = "testcity"
	f.state.cityBeadStore = f.city
	f.state.stores = map[string]beads.Store{"alpha": f.alpha, "beta": f.beta}
	f.state.cfg = &config.City{
		Workspace: config.Workspace{Name: "testcity", Prefix: "tc"},
		Rigs: []config.Rig{
			{Name: "alpha", Path: "/tmp/alpha", Prefix: "alp"},
			{Name: "beta", Path: "/tmp/beta", Prefix: "bet"},
		},
	}
	return f
}

// seedWorkflow creates a workflow root and one step, both with pinned IDs in
// the store's own prefix namespace, and returns the step.
func seedWorkflow(t *testing.T, store beads.Store, prefix, scopeRef, storeRef string) beads.Bead {
	t.Helper()
	root, err := store.Create(beads.Bead{
		ID:    prefix + "-root",
		Title: "Workflow",
		Type:  "task",
		Metadata: map[string]string{
			"gc.kind":           "workflow",
			"gc.workflow_id":    "wf-" + scopeRef,
			"gc.scope_kind":     "rig",
			"gc.scope_ref":      scopeRef,
			"gc.root_store_ref": storeRef,
		},
	})
	if err != nil {
		t.Fatalf("Create(root): %v", err)
	}
	step, err := store.Create(beads.Bead{
		ID:    prefix + "-step",
		Title: "Step",
		Type:  "task",
		Metadata: map[string]string{
			"gc.root_bead_id":    root.ID,
			"gc.root_store_ref":  storeRef,
			"gc.logical_bead_id": "node-1",
		},
	})
	if err != nil {
		t.Fatalf("Create(step): %v", err)
	}
	return step
}

// nonWorkflowPayload is a bead snapshot that carries an ID but none of the
// workflow metadata, so workflowEventPayloadLooksWorkflow rejects it and the
// projection falls through to the subject lookup. This is the shape 65% of the
// live fleet's bead.* events actually have.
func nonWorkflowPayload(t *testing.T, id string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(beads.Bead{ID: id, Title: "snapshot", Type: "task"})
	if err != nil {
		t.Fatalf("Marshal(payload): %v", err)
	}
	return raw
}

// TestProjectWorkflowEventDoesNotProbeStoresThatCannotOwnTheSubject is the
// regression gate for the fan-out. It fails if the subject lookup goes back to
// sweeping every store: the assertion is on the probe COUNT of the stores that
// cannot own the id, so returning the right bead by the wrong route is still a
// failure.
func TestProjectWorkflowEventDoesNotProbeStoresThatCannotOwnTheSubject(t *testing.T) {
	f := newScopedFanoutFixture(t)
	step := seedWorkflow(t, f.alpha, "alp", "alpha", "rig:alpha")

	projection := projectWorkflowEvent(f.state, events.Event{
		Type:    events.BeadUpdated,
		Seq:     7,
		Ts:      time.Unix(1711300000, 0).UTC(),
		Subject: step.ID,
		Payload: nonWorkflowPayload(t, step.ID),
	})
	if projection == nil {
		t.Fatal("projection = nil, want the workflow event to still resolve via the subject")
	}
	if projection.Bead.ID != step.ID {
		t.Fatalf("bead.id = %q, want %q", projection.Bead.ID, step.ID)
	}
	if projection.WorkflowID != "wf-alpha" {
		t.Fatalf("workflow_id = %q, want wf-alpha", projection.WorkflowID)
	}

	if len(f.alpha.gets) == 0 {
		t.Fatal("alpha store was never probed; the owning store must still be asked")
	}
	if len(f.beta.gets) != 0 {
		t.Fatalf("beta store probed %v; a rig store cannot own an \"alp-\" id and must not be asked", f.beta.gets)
	}
	if len(f.city.gets) != 0 {
		t.Fatalf("city store probed %v; the city prefix is \"tc\" and must not be asked for an \"alp-\" id", f.city.gets)
	}
}

// TestProjectWorkflowEventDoesNotProbeForeignStoresForTheRoot covers the second
// fan-out in the same function: the root lookup that runs when gc.root_store_ref
// does not resolve.
func TestProjectWorkflowEventDoesNotProbeForeignStoresForTheRoot(t *testing.T) {
	f := newScopedFanoutFixture(t)
	// "rig:ghost" names no configured rig, so workflowStoreByRef misses and the
	// root falls through to the scan.
	step := seedWorkflow(t, f.alpha, "alp", "alpha", "rig:ghost")

	projection := projectWorkflowEvent(f.state, events.Event{
		Type:    events.BeadUpdated,
		Seq:     8,
		Ts:      time.Unix(1711300000, 0).UTC(),
		Subject: step.ID,
		Payload: nonWorkflowPayload(t, step.ID),
	})
	if projection == nil {
		t.Fatal("projection = nil, want the root to still resolve via the scan")
	}
	if projection.RootBeadID != "alp-root" {
		t.Fatalf("root_bead_id = %q, want alp-root", projection.RootBeadID)
	}
	for _, probe := range f.beta.gets {
		if probe == "alp-root" {
			t.Fatal("beta store probed for \"alp-root\"; the root scan must stay in the owning namespace")
		}
	}
	for _, probe := range f.city.gets {
		if probe == "alp-root" {
			t.Fatal("city store probed for \"alp-root\"; the root scan must stay in the owning namespace")
		}
	}
}

// TestProjectWorkflowEventStillSweepsWhenNoConfiguredPrefixOwnsTheID pins the
// fail-open half of the contract. An id in nobody's namespace (a relocated
// class's reserved prefix, a legacy id, a store minting outside its declared
// prefix) must still reach every store, or the narrowing silently drops
// workflows instead of speeding them up.
func TestProjectWorkflowEventStillSweepsWhenNoConfiguredPrefixOwnsTheID(t *testing.T) {
	f := newScopedFanoutFixture(t)
	step := seedWorkflow(t, f.alpha, "zzz", "alpha", "rig:alpha")

	projection := projectWorkflowEvent(f.state, events.Event{
		Type:    events.BeadUpdated,
		Seq:     9,
		Ts:      time.Unix(1711300000, 0).UTC(),
		Subject: step.ID,
		Payload: nonWorkflowPayload(t, step.ID),
	})
	if projection == nil {
		t.Fatal("projection = nil, want an unowned id to still resolve by sweeping every store")
	}
	if projection.Bead.ID != step.ID {
		t.Fatalf("bead.id = %q, want %q", projection.Bead.ID, step.ID)
	}
	if len(f.beta.gets) == 0 || len(f.city.gets) == 0 {
		t.Fatalf("unowned id did not sweep: city probes=%v beta probes=%v", f.city.gets, f.beta.gets)
	}
}

// TestConfiguredScopeRefForBeadIDPrefersTheLongestPrefix guards the routing
// itself. The cases that matter are the ones where TWO configured prefixes both
// match the id: "mem-eval-1" is in the "mem-" namespace and in the "mem-eval-"
// namespace. Only the longest is the real owner. First-match-wins would route
// by cfg.Rigs declaration order — arbitrary — and send every mem-eval bead to
// the mem store, where it is never found.
func TestConfiguredScopeRefForBeadIDPrefersTheLongestPrefix(t *testing.T) {
	f := newScopedFanoutFixture(t)
	f.state.cfg = &config.City{
		Workspace: config.Workspace{Name: "testcity", Prefix: "mem"},
		Rigs: []config.Rig{
			// Declared before the longer prefix on purpose: a scan that stops
			// at the first hit picks this one.
			{Name: "shortrig", Path: "/tmp/shortrig", Prefix: "mem-e"},
			{Name: "evalrig", Path: "/tmp/evalrig", Prefix: "mem-eval"},
		},
	}

	cityRef := workflowCityScopeRef("testcity")
	for _, tc := range []struct {
		id      string
		wantRef string
		wantOK  bool
	}{
		// Three configured prefixes match "mem-eval-1": the city's "mem", the
		// rig "mem-e", and the rig "mem-eval". The longest owns it.
		{id: "mem-eval-1", wantRef: "evalrig", wantOK: true},
		// Two match here; the longer rig prefix does not.
		{id: "mem-e-7", wantRef: "shortrig", wantOK: true},
		{id: "mem-1", wantRef: cityRef, wantOK: true},
		{id: "other-1", wantOK: false},
		// Bare prefix with no separator is not in the namespace.
		{id: "mem", wantOK: false},
		{id: "", wantOK: false},
	} {
		gotRef, gotOK := configuredScopeRefForBeadID(f.state, tc.id)
		if gotOK != tc.wantOK || (tc.wantOK && gotRef != tc.wantRef) {
			t.Errorf("configuredScopeRefForBeadID(%q) = (%q, %v), want (%q, %v)", tc.id, gotRef, gotOK, tc.wantRef, tc.wantOK)
		}
	}
}

// TestConfiguredScopeRefForBeadIDFailsOpenOnAColludingPrefix covers the
// misconfiguration that longest-prefix alone cannot resolve: two scopes
// claiming the SAME prefix. This is reachable on a real city, because both a
// rig prefix and the city prefix default to config.DeriveBeadsPrefix over their
// name, and DeriveBeadsPrefix("gas-city") and DeriveBeadsPrefix("gascity") both
// yield "gc". Picking either scope would make the other's beads permanently
// unresolvable; the full scan still finds them.
func TestConfiguredScopeRefForBeadIDFailsOpenOnAColludingPrefix(t *testing.T) {
	f := newScopedFanoutFixture(t)
	f.state.cfg = &config.City{
		Workspace: config.Workspace{Name: "testcity", Prefix: "dup"},
		Rigs: []config.Rig{
			{Name: "alpha", Path: "/tmp/alpha", Prefix: "dup"},
			{Name: "beta", Path: "/tmp/beta", Prefix: "solo"},
		},
	}

	if ref, ok := configuredScopeRefForBeadID(f.state, "dup-1"); ok {
		t.Fatalf("configuredScopeRefForBeadID(dup-1) = (%q, true), want ok=false so the caller falls back to the full scan", ref)
	}
	// An unambiguous prefix in the same config still routes.
	if ref, ok := configuredScopeRefForBeadID(f.state, "solo-1"); !ok || ref != "beta" {
		t.Fatalf("configuredScopeRefForBeadID(solo-1) = (%q, %v), want (beta, true)", ref, ok)
	}
}
