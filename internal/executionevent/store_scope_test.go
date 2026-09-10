package executionevent

import (
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
)

type scopedReadResult struct {
	beads.Store
	row beads.Bead
	ref string
	err error
}

// Store labels on forward events must not make an already recorded completion
// look missing to the existing recovery lane. This exercises the real journal
// and reconciler, not merely equality of internal index keys.
func TestRecoveryDoesNotReplayScopedCompletion(t *testing.T) {
	graph := beads.NewMemStore()
	root := mustCreateProjectionRoot(t, graph, "")
	step := mustCreateProjectionStep(t, graph, "gcg-attempt", root.ID, "build", "[]")
	closed := "closed"
	if err := graph.Update(step.ID, beads.UpdateOpts{Status: &closed, Metadata: map[string]string{
		beadmeta.SessionIDMetadataKey: "gcs-session",
	}}); err != nil {
		t.Fatal(err)
	}
	step, err := graph.Get(step.ID)
	if err != nil {
		t.Fatal(err)
	}
	recorder := events.NewFake()
	if !EmitLifecycle(recorder, WithStoreRef(graph, "class:g"), events.ExecutionStepCompleted, step, "worker") {
		t.Fatal("direct completion was not recorded")
	}
	if len(recorder.Events) != 1 || recorder.Events[0].SubjectStoreRef != "class:g" {
		t.Fatalf("direct scoped completion = %#v", recorder.Events)
	}
	if got := ReconcileCompleted(recorder, beads.GraphStore{Store: graph}, "execution-reconcile"); got != 0 {
		t.Fatalf("recovery emitted %d duplicate completion(s) after a scoped direct fact", got)
	}
}

func TestRecoveryCompatibilityRequiresJournalWitnessAndExpiresOnRebuild(t *testing.T) {
	fact := events.Event{
		Type: events.ExecutionStepCompleted, Subject: "step", RunID: "root",
		SessionID: "session", StepID: "build", SubjectStoreRef: "class:g", RunStoreRef: "class:g",
	}
	key := completedFactKeyFor(fact)
	unknown := key
	unknown.subjectStoreRef, unknown.runStoreRef = "", ""
	idx := &CompletedFactIndex{}
	idx.add(key)
	if present, _ := idx.lookupRecovery(unknown); present {
		t.Fatal("attempted scoped append became an unscoped journal witness")
	}
	idx.Absorb(fact)
	if present, confirmed := idx.lookupRecovery(unknown); !present || !confirmed {
		t.Fatal("journal-confirmed scoped fact did not prevent legacy recovery replay")
	}
	foreign := key
	foreign.subjectStoreRef, foreign.runStoreRef = "rig:other", "rig:other"
	if present, _ := idx.lookupRecovery(foreign); present {
		t.Fatal("compatibility lookup crossed two known store owners")
	}
	idx.mergeFromJournal(nil, true, true)
	if present, _ := idx.lookupRecovery(unknown); present {
		t.Fatal("full journal rebuild retained an expired recovery witness")
	}
}

func TestClaimLifecycleUsesPairedStepAndRootOwnership(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateProjectionBead(t, store, beads.Bead{Metadata: map[string]string{
		"gc.kind": "workflow", "gc.formula_contract": "graph.v2",
	}})
	step := mustCreateProjectionBead(t, store, beads.Bead{Metadata: map[string]string{
		"gc.root_bead_id": root.ID, "gc.step_id": "build", "gc.session_id": "gcs-session",
	}})
	rec := events.NewFake()
	if !EmitLifecycle(rec, WithStoreRef(store, "class:gmnos"), events.ExecutionStepStarted, step, "worker") {
		t.Fatal("claim lifecycle was not emitted")
	}
	if len(rec.Events) != 1 || rec.Events[0].SubjectStoreRef != "class:gmnos" || rec.Events[0].RunStoreRef != "class:gmnos" {
		t.Fatalf("claim owner = %#v", rec.Events)
	}
}

func TestClosedEventRequiresMatchingOriginForScopedCompletion(t *testing.T) {
	store := beads.NewMemStore()
	root := mustCreateProjectionBead(t, store, beads.Bead{ID: "gcg-root", Metadata: map[string]string{
		"gc.kind": "workflow", "gc.formula_contract": "graph.v2",
	}})
	step := beads.Bead{ID: "gcg-step", Status: "closed", Metadata: map[string]string{
		"gc.root_bead_id": root.ID, "gc.step_id": "build", "gc.session_id": "gcs-session",
	}}
	payload, err := beads.EncodeBeadEventPayload(step)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, origin, graphRef, subject string
		want                            bool
		wantRef                         string
	}{
		{"matching", "class:g", "class:g", step.ID, true, "class:g"},
		{"other store same IDs", "rig:other", "class:g", step.ID, false, ""},
		{"unknown graph", "class:g", "", step.ID, false, ""},
		{"legacy unknown origin", "", "class:g", step.ID, true, ""},
		{"different subject", "class:g", "class:g", "gcg-other", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := events.NewFake()
			ok := EmitCompletedFromEvent(rec, WithStoreRef(store, tc.graphRef), events.Event{
				Type: events.BeadClosed, Subject: tc.subject, SubjectStoreRef: tc.origin, Payload: payload,
			})
			if ok != tc.want {
				t.Fatalf("emitted=%v, want %v", ok, tc.want)
			}
			if !tc.want && len(rec.Events) != 0 {
				t.Fatal("rejected close emitted a fact")
			}
			if tc.want && (len(rec.Events) != 1 || rec.Events[0].SubjectStoreRef != tc.wantRef || rec.Events[0].RunStoreRef != tc.wantRef) {
				t.Fatalf("completion = %#v", rec.Events)
			}
		})
	}
}

func (s scopedReadResult) GetWithStoreRef(string) (beads.Bead, string, error) {
	return s.row, s.ref, s.err
}

func TestReadWithStoreRefRejectsUnverifiedScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  beads.Bead
		err  error
	}{
		{"wrong row", beads.Bead{ID: "gc-other"}, nil},
		{"failed exact row", beads.Bead{ID: "gc-target"}, beads.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ref, err := ReadWithStoreRef(scopedReadResult{row: tc.row, ref: "rig:unverified", err: tc.err}, "gc-target")
			if ref != "" || !errors.Is(err, tc.err) {
				t.Fatalf("ref=%q err=%v, want unknown scope and original error", ref, err)
			}
		})
	}
	legacy := beads.NewMemStore()
	row := mustCreateProjectionBead(t, legacy, beads.Bead{})
	got, ref, err := ReadWithStoreRef(legacy, row.ID)
	if err != nil || got.ID != row.ID || ref != "" {
		t.Fatalf("legacy result = %#v %q %v", got, ref, err)
	}
}

func TestWithStoreRefIsProjectionOnlyAndLeavesMissingOwnershipUnknown(t *testing.T) {
	store := beads.NewMemStore()
	row := mustCreateProjectionBead(t, store, beads.Bead{})
	if got := WithStoreRef(store, ""); got != store {
		t.Fatal("unknown scope changed store identity")
	}
	if got := WithStoreRef(nil, "city:test"); got != nil {
		t.Fatal("nil store became nonnil")
	}
	scoped := WithStoreRef(store, "city:test")
	got, ref, err := ReadWithStoreRef(scoped, row.ID)
	if err != nil || got.ID != row.ID || ref != "city:test" {
		t.Fatalf("scoped result = %#v %q %v", got, ref, err)
	}
	_, ref, err = ReadWithStoreRef(scoped, "gc-absent")
	if !errors.Is(err, beads.ErrNotFound) || ref != "" {
		t.Fatalf("missing result = %q %v", ref, err)
	}
}

type scopedProjectionStore struct {
	beads.Store
	refs map[string]string
}

func (s scopedProjectionStore) GetWithStoreRef(id string) (beads.Bead, string, error) {
	row, err := s.Get(id)
	return row, s.refs[id], err
}

func TestProjectionUsesEachResolvedRowsStoreOwner(t *testing.T) {
	graph, work := beads.NewMemStore(), beads.NewMemStore()
	convoy := mustCreateProjectionBead(t, work, beads.Bead{})
	source := mustCreateProjectionBead(t, work, beads.Bead{})
	launch := mustCreateProjectionBead(t, work, beads.Bead{Metadata: map[string]string{beadmeta.SourceBeadIDMetadataKey: source.ID}})
	if err := work.DepAdd(convoy.ID, launch.ID, "tracks"); err != nil {
		t.Fatal(err)
	}
	root := mustCreateProjectionRoot(t, graph, convoy.ID)
	mustCreateProjectionStep(t, graph, "gcg-step", root.ID, "build", "[]")
	projection, err := ProjectCurrent(
		beads.GraphStore{Store: scopedProjectionStore{Store: graph, refs: map[string]string{root.ID: "class:g"}}},
		beads.WorkStore{Store: scopedProjectionStore{Store: work, refs: map[string]string{launch.ID: "rig:alpha", source.ID: "rig:beta"}}}, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	facts := projection.Events("producer")
	if len(facts) != 3 {
		t.Fatalf("facts = %#v, want association, anchor and step", facts)
	}
	want := map[string]string{events.ExecutionWorkAssociated: "rig:alpha", events.ExecutionRunAnchored: "rig:beta", events.ExecutionStepDefined: "class:g"}
	for _, fact := range facts {
		if fact.SubjectStoreRef != want[fact.Type] || fact.RunStoreRef != "class:g" {
			t.Fatalf("fact owner = (%q, %q), want (%q, class:g): %#v", fact.SubjectStoreRef, fact.RunStoreRef, want[fact.Type], fact)
		}
	}
}

func TestCompletedFactIndexSeparatesStoreOwners(t *testing.T) {
	base := events.Event{Type: events.ExecutionStepCompleted, Subject: "gcg-step", RunID: "gcg-root", SessionID: "gcs-session", StepID: "build", SubjectStoreRef: "rig:alpha", RunStoreRef: "class:graph"}
	index := CompletedFactIndex{loaded: true, facts: make(map[completedFactKey]bool)}
	index.Absorb(base)
	if present, confirmed := index.lookup(completedFactKeyFor(base)); !present || !confirmed {
		t.Fatal("absorbed completion was not confirmed")
	}
	for _, tc := range []struct{ name, subjectRef, runRef string }{
		{"other subject owner", "rig:beta", "class:graph"},
		{"other run owner", "rig:alpha", "rig:beta"},
		{"legacy unknown owners", "", ""},
		{"unknown subject owner", "", "class:graph"},
		{"unknown run owner", "rig:alpha", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := base
			other.SubjectStoreRef, other.RunStoreRef = tc.subjectRef, tc.runRef
			if present, _ := index.lookup(completedFactKeyFor(other)); present {
				t.Fatal("a completion from different or unknown owners suppressed this fact")
			}
			index.Absorb(other)
			if present, confirmed := index.lookup(completedFactKeyFor(other)); !present || !confirmed {
				t.Fatal("independently absorbed completion was not confirmed")
			}
		})
	}
}
