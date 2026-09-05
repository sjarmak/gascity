package main

import (
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

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
