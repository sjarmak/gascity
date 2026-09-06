package main

import (
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

type collisionGetStore struct {
	beads.Store
	ID string
}

func (s *collisionGetStore) Get(id string) (beads.Bead, error) {
	if id == s.ID {
		return beads.Bead{}, beads.ErrIDCollision
	}
	return s.Store.Get(id)
}

func TestUnfinishedContinuationStatusRetainsAcrossMaterializationGap(t *testing.T) {
	executionStore := beads.NewMemStore()
	executionStore.HonorExplicitIDs = true
	memberStore := beads.NewMemStore()
	memberStore.HonorExplicitIDs = true
	if _, err := memberStore.Create(beads.Bead{ID: "member-1", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey: "rig:owner",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := executionStore.Create(beads.Bead{ID: "root-1", Type: "task", Status: "in_progress", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey:        "city:test",
		beadmeta.KindMetadataKey:                beadmeta.KindWorkflow,
		beadmeta.FormulaContractMetadataKey:     beadmeta.FormulaContractGraphV2,
		beadmeta.DrainMemberIDMetadataKey:       "member-1",
		beadmeta.DrainMemberStoreRefMetadataKey: "rig:owner",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := executionStore.Create(beads.Bead{ID: "step-1", Type: "task", Status: "closed", Metadata: map[string]string{
		beadmeta.RootStoreRefMetadataKey:      "city:test",
		beadmeta.RootBeadIDMetadataKey:        "root-1",
		beadmeta.ContinuationGroupMetadataKey: "drain:control-1",
	}}); err != nil {
		t.Fatal(err)
	}
	resolver := newQualifiedBeadResolver([]qualifiedStoreBinding{
		{StoreRef: "city", Store: executionStore},
		{StoreRef: "owner", Store: memberStore},
	})
	info := sessionpkg.Info{
		ID: "session-1", CurrentlyProcessingBeadID: "step-1",
		TriggerBeadID: "step-1", TriggerBeadStoreRef: "city:test",
	}
	if got := unfinishedContinuationStatus(info, resolver, nil); got.State != continuationRetain || got.Err != nil {
		t.Fatalf("unfinished status = %+v, want retain", got)
	}
	if err := executionStore.Close("root-1"); err != nil {
		t.Fatal(err)
	}
	if got := unfinishedContinuationStatus(info, resolver, nil); got.State != continuationAbsent || got.Err != nil {
		t.Fatalf("completed status = %+v, want absent", got)
	}
}

func TestUnfinishedContinuationStatusDistinguishesUnknownFromNotFound(t *testing.T) {
	store := beads.NewMemStore()
	resolver := newQualifiedBeadResolver([]qualifiedStoreBinding{{StoreRef: "city", Store: store}})
	info := sessionpkg.Info{
		ID: "session-1", CurrentlyProcessingBeadID: "step-1",
		TriggerBeadID: "step-1", TriggerBeadStoreRef: "city:test",
	}
	if got := unfinishedContinuationStatus(info, resolver, nil); got.State != continuationAbsent || got.Err != nil {
		t.Fatalf("verified missing step = %+v, want absent", got)
	}

	readErr := errors.New("store unavailable")
	unreadable := &failingGetStore{Store: store, failID: "step-1", err: readErr}
	resolver = newQualifiedBeadResolver([]qualifiedStoreBinding{{StoreRef: "city", Store: unreadable}})
	got := unfinishedContinuationStatus(info, resolver, nil)
	if got.State != continuationUnknown || !errors.Is(got.Err, readErr) {
		t.Fatalf("unreadable step = %+v, want unknown wrapping read error", got)
	}
	if got := unfinishedContinuationStatus(info, resolver, errors.New("store topology unavailable")); got.State != continuationUnknown || got.Err == nil {
		t.Fatalf("unreadable resolver topology = %+v, want unknown", got)
	}
}

func TestUnfinishedContinuationStatusTreatsIDCollisionsAsUnknown(t *testing.T) {
	tests := []struct {
		name      string
		collision string
		want      continuationRetentionState
	}{
		{name: "step", collision: "step-1", want: continuationUnknown},
		{name: "root", collision: "root-1", want: continuationUnknown},
		{name: "member", collision: "member-1", want: continuationUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			execution := beads.NewMemStore()
			execution.HonorExplicitIDs = true
			member := beads.NewMemStore()
			member.HonorExplicitIDs = true
			if _, err := member.Create(beads.Bead{ID: "member-1", Metadata: map[string]string{
				beadmeta.RootStoreRefMetadataKey: "rig:owner",
			}}); err != nil {
				t.Fatal(err)
			}
			if _, err := execution.Create(beads.Bead{ID: "root-1", Status: "in_progress", Metadata: map[string]string{
				beadmeta.RootStoreRefMetadataKey:        "city:test",
				beadmeta.KindMetadataKey:                beadmeta.KindWorkflow,
				beadmeta.FormulaContractMetadataKey:     beadmeta.FormulaContractGraphV2,
				beadmeta.DrainMemberIDMetadataKey:       "member-1",
				beadmeta.DrainMemberStoreRefMetadataKey: "rig:owner",
			}}); err != nil {
				t.Fatal(err)
			}
			if _, err := execution.Create(beads.Bead{ID: "step-1", Status: "closed", Metadata: map[string]string{
				beadmeta.RootStoreRefMetadataKey:      "city:test",
				beadmeta.RootBeadIDMetadataKey:        "root-1",
				beadmeta.ContinuationGroupMetadataKey: "drain:control-1",
			}}); err != nil {
				t.Fatal(err)
			}
			resolver := newQualifiedBeadResolver([]qualifiedStoreBinding{
				{StoreRef: "city", Store: &collisionGetStore{Store: execution, ID: tc.collision}},
				{StoreRef: "owner", Store: &collisionGetStore{Store: member, ID: tc.collision}},
			})
			info := sessionpkg.Info{ID: "session-1", CurrentlyProcessingBeadID: "step-1", TriggerBeadID: "step-1", TriggerBeadStoreRef: "city:test"}
			got := unfinishedContinuationStatus(info, resolver, nil)
			if got.State != tc.want || got.Err == nil || !errors.Is(got.Err, beads.ErrIDCollision) {
				t.Fatalf("collision at %s = %+v, want unknown wrapping ErrIDCollision", tc.name, got)
			}
		})
	}
}

func TestUnfinishedContinuationStatusTreatsGenuineMissingAsAbsentAtEachBranch(t *testing.T) {
	newInfo := func() sessionpkg.Info {
		return sessionpkg.Info{ID: "session-1", CurrentlyProcessingBeadID: "step-1", TriggerBeadID: "step-1", TriggerBeadStoreRef: "city:test"}
	}
	newStep := func(store beads.Store) {
		_, err := store.Create(beads.Bead{ID: "step-1", Status: "closed", Metadata: map[string]string{
			beadmeta.RootStoreRefMetadataKey:      "city:test",
			beadmeta.RootBeadIDMetadataKey:        "root-1",
			beadmeta.ContinuationGroupMetadataKey: "drain:control-1",
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name  string
		setup func(*beads.MemStore, *beads.MemStore)
	}{
		{name: "step", setup: func(_, _ *beads.MemStore) {}},
		{name: "root", setup: func(execution, _ *beads.MemStore) { newStep(execution) }},
		{name: "member", setup: func(execution, _ *beads.MemStore) {
			newStep(execution)
			_, err := execution.Create(beads.Bead{ID: "root-1", Status: "in_progress", Metadata: map[string]string{
				beadmeta.RootStoreRefMetadataKey:        "city:test",
				beadmeta.KindMetadataKey:                beadmeta.KindWorkflow,
				beadmeta.FormulaContractMetadataKey:     beadmeta.FormulaContractGraphV2,
				beadmeta.DrainMemberIDMetadataKey:       "member-1",
				beadmeta.DrainMemberStoreRefMetadataKey: "rig:owner",
			}})
			if err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			execution := beads.NewMemStore()
			execution.HonorExplicitIDs = true
			member := beads.NewMemStore()
			member.HonorExplicitIDs = true
			tc.setup(execution, member)
			resolver := newQualifiedBeadResolver([]qualifiedStoreBinding{
				{StoreRef: "city", Store: execution},
				{StoreRef: "owner", Store: member},
			})
			got := unfinishedContinuationStatus(newInfo(), resolver, nil)
			if got.State != continuationAbsent || got.Err != nil {
				t.Fatalf("genuine missing %s = %+v, want absent", tc.name, got)
			}
		})
	}
}

func TestUnfinishedContinuationStatusRejectsMismatchedQualifiedTrigger(t *testing.T) {
	info := sessionpkg.Info{
		ID: "session-1", CurrentlyProcessingBeadID: "prior-step",
		TriggerBeadID: "other-step", TriggerBeadStoreRef: "city:test",
	}
	got := unfinishedContinuationStatus(info, qualifiedBeadResolver{}, nil)
	if got.State != continuationUnknown || got.Err == nil {
		t.Fatalf("mismatched trigger = %+v, want unknown", got)
	}
}
