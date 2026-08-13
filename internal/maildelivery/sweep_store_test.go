package maildelivery

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestStoreSweepCheckpointPersistsAndFencesAdvance(t *testing.T) {
	store, backing := newSweepDeliveryStore()
	seat := "seat:test-city/reviewer"
	created, err := store.CreateSweepCheckpoint(seat)
	if err != nil {
		t.Fatalf("CreateSweepCheckpoint: %v", err)
	}
	if created.Generation != 1 || created.Revision != 1 || created.SeatRef != seat {
		t.Fatalf("created = %#v", created)
	}
	replayed, err := store.CreateSweepCheckpoint(seat)
	if err != nil || replayed != created {
		t.Fatalf("replayed = %#v, %v", replayed, err)
	}

	keys := []DeliveryKey{sweepKey(1, "a"), sweepKey(2, "b")}
	created.HighWatermark = keys[1]
	plan, err := PlanSweep(created, keys, 1)
	if err != nil {
		t.Fatalf("PlanSweep: %v", err)
	}
	advanced, err := store.AdvanceSweepCheckpoint(created, plan)
	if err != nil {
		t.Fatalf("AdvanceSweepCheckpoint: %v", err)
	}
	if advanced.After != keys[0] || advanced.HighWatermark != keys[1] || advanced.Revision <= created.Revision {
		t.Fatalf("advanced = %#v", advanced)
	}
	if _, err := store.AdvanceSweepCheckpoint(created, plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale advance = %v, want ErrConflict", err)
	}
	got, err := store.GetSweepCheckpoint(seat)
	if err != nil || got != advanced {
		t.Fatalf("GetSweepCheckpoint = %#v, %v", got, err)
	}
	rows, err := backing.List(beads.ListQuery{Type: sweepCheckpointBeadType})
	if err != nil || len(rows) != 1 || rows[0].Description != "" {
		t.Fatalf("checkpoint rows = %#v, %v", rows, err)
	}
}

func TestStoreSweepCapturesExactMaxBeforePagingAndSurvivesCASCrashes(t *testing.T) {
	seat := "seat:test-city/reviewer"
	store := seededSweepStore(t, []time.Time{
		time.Date(2020, 1, 1, 0, 0, 1, 0, time.UTC),
		time.Date(2020, 1, 1, 0, 0, 2, 0, time.UTC),
		time.Date(2020, 1, 1, 0, 0, 2, 0, time.UTC),
		time.Date(2020, 1, 1, 0, 0, 3, 0, time.UTC),
		time.Date(2020, 1, 1, 0, 0, 4, 0, time.UTC),
	})
	cp, err := store.CreateSweepCheckpoint(seat)
	if err != nil {
		t.Fatalf("CreateSweepCheckpoint: %v", err)
	}

	// More than two pages exist. The captured maximum must be the final store
	// key, never the tail of the first bounded page.
	cp, err = store.CaptureSweepHighWatermark(cp)
	if err != nil {
		t.Fatalf("CaptureSweepHighWatermark: %v", err)
	}
	all, err := store.ListActionableKeys(seat, DeliveryKey{}, 100)
	if err != nil {
		t.Fatalf("ListActionableKeys all: %v", err)
	}
	if cp.HighWatermark != all[len(all)-1] {
		t.Fatalf("high-watermark = %#v, want exact max %#v", cp.HighWatermark, all[len(all)-1])
	}

	firstPage, err := store.ListActionableKeys(seat, cp.After, 2)
	if err != nil {
		t.Fatalf("ListActionableKeys first: %v", err)
	}
	firstPlan, err := PlanSweep(cp, firstPage, 2)
	if err != nil {
		t.Fatalf("PlanSweep first: %v", err)
	}
	// Crash before checkpoint CAS: reload repeats the exact page.
	reloaded, err := store.GetSweepCheckpoint(seat)
	if err != nil {
		t.Fatalf("GetSweepCheckpoint before CAS: %v", err)
	}
	repeated, err := store.ListActionableKeys(seat, reloaded.After, 2)
	if err != nil || !equalDeliveryKeys(repeated, firstPage) {
		t.Fatalf("page after pre-CAS crash = %#v, %v; want %#v", repeated, err, firstPage)
	}

	cp, err = store.AdvanceSweepCheckpoint(reloaded, firstPlan)
	if err != nil {
		t.Fatalf("AdvanceSweepCheckpoint: %v", err)
	}
	// Crash after checkpoint CAS: reload resumes strictly after the page.
	reloaded, err = store.GetSweepCheckpoint(seat)
	if err != nil {
		t.Fatalf("GetSweepCheckpoint after CAS: %v", err)
	}
	nextPage, err := store.ListActionableKeys(seat, reloaded.After, 2)
	if err != nil {
		t.Fatalf("ListActionableKeys next: %v", err)
	}
	if len(nextPage) == 0 || compareKey(nextPage[0], firstPage[len(firstPage)-1]) <= 0 {
		t.Fatalf("page after post-CAS crash = %#v", nextPage)
	}

	visited := append([]DeliveryKey(nil), firstPage...)
	for !reloaded.HighWatermark.IsZero() {
		page, err := store.ListActionableKeys(seat, reloaded.After, 2)
		if err != nil {
			t.Fatalf("ListActionableKeys drain: %v", err)
		}
		plan, err := PlanSweep(reloaded, page, 2)
		if err != nil {
			t.Fatalf("PlanSweep drain: %v", err)
		}
		visited = append(visited, plan.Page...)
		reloaded, err = store.AdvanceSweepCheckpoint(reloaded, plan)
		if err != nil {
			t.Fatalf("AdvanceSweepCheckpoint drain: %v", err)
		}
	}
	if !equalDeliveryKeys(visited, all) {
		t.Fatalf("visited = %#v, want %#v", visited, all)
	}
}

func TestStoreSweepCompletesCapturedRangeUnderSustainedArrivals(t *testing.T) {
	seat := "seat:test-city/reviewer"
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	store := seededSweepStore(t, []time.Time{base.Add(time.Second), base.Add(2 * time.Second), base.Add(3 * time.Second)})
	cp, err := store.CreateSweepCheckpoint(seat)
	if err != nil {
		t.Fatalf("CreateSweepCheckpoint: %v", err)
	}
	cp, err = store.CaptureSweepHighWatermark(cp)
	if err != nil {
		t.Fatalf("CaptureSweepHighWatermark: %v", err)
	}
	captured := cp.HighWatermark

	for iteration := 0; !cp.HighWatermark.IsZero(); iteration++ {
		if iteration > 10 {
			t.Fatal("captured sweep did not complete under sustained arrivals")
		}
		createSweepDelivery(t, store, seat, base.Add(time.Duration(100+iteration)*time.Second), "arrival")
		page, err := store.ListActionableKeys(seat, cp.After, 1)
		if err != nil {
			t.Fatalf("ListActionableKeys: %v", err)
		}
		plan, err := PlanSweep(cp, page, 1)
		if err != nil {
			t.Fatalf("PlanSweep: %v", err)
		}
		cp, err = store.AdvanceSweepCheckpoint(cp, plan)
		if err != nil {
			t.Fatalf("AdvanceSweepCheckpoint: %v", err)
		}
	}
	if cp.Generation != 2 {
		t.Fatalf("generation = %d, want completed generation 2", cp.Generation)
	}
	cp, err = store.CaptureSweepHighWatermark(cp)
	if err != nil {
		t.Fatalf("CaptureSweepHighWatermark next: %v", err)
	}
	if compareKey(cp.HighWatermark, captured) <= 0 {
		t.Fatalf("next high-watermark = %#v, want arrivals after %#v", cp.HighWatermark, captured)
	}
}

func TestStoreIdleSweepDoesNotChurnCheckpoint(t *testing.T) {
	store, _ := newSweepDeliveryStore()
	cp, err := store.CreateSweepCheckpoint("seat:test-city/reviewer")
	if err != nil {
		t.Fatalf("CreateSweepCheckpoint: %v", err)
	}
	for range 3 {
		got, err := store.CaptureSweepHighWatermark(cp)
		if err != nil {
			t.Fatalf("CaptureSweepHighWatermark: %v", err)
		}
		if got != cp {
			t.Fatalf("idle capture changed checkpoint: got %#v, want %#v", got, cp)
		}
	}
}

func TestStoreSweepCaptureIsIdempotentAndRevisionFenced(t *testing.T) {
	seat := "seat:test-city/reviewer"
	store := seededSweepStore(t, []time.Time{time.Date(2020, 1, 1, 0, 0, 1, 0, time.UTC)})
	initial, err := store.CreateSweepCheckpoint(seat)
	if err != nil {
		t.Fatalf("CreateSweepCheckpoint: %v", err)
	}
	captured, err := store.CaptureSweepHighWatermark(initial)
	if err != nil {
		t.Fatalf("CaptureSweepHighWatermark: %v", err)
	}
	again, err := store.CaptureSweepHighWatermark(captured)
	if err != nil {
		t.Fatalf("CaptureSweepHighWatermark active: %v", err)
	}
	if again != captured {
		t.Fatalf("active capture changed checkpoint: got %#v, want %#v", again, captured)
	}
	if _, err := store.CaptureSweepHighWatermark(initial); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale capture = %v, want ErrConflict", err)
	}
}

func TestStoreSweepHighWatermarkUsesLargestIDAtEqualTimestamp(t *testing.T) {
	seat := "seat:test-city/reviewer"
	at := time.Date(2020, 1, 1, 0, 0, 1, 0, time.UTC)
	store := seededSweepStore(t, []time.Time{at, at, at})
	all, err := store.ListActionableKeys(seat, DeliveryKey{}, 10)
	if err != nil {
		t.Fatalf("ListActionableKeys: %v", err)
	}
	cp, err := store.CreateSweepCheckpoint(seat)
	if err != nil {
		t.Fatalf("CreateSweepCheckpoint: %v", err)
	}
	cp, err = store.CaptureSweepHighWatermark(cp)
	if err != nil {
		t.Fatalf("CaptureSweepHighWatermark: %v", err)
	}
	if cp.HighWatermark != all[len(all)-1] {
		t.Fatalf("equal-time high-watermark = %#v, want largest ID key %#v", cp.HighWatermark, all[len(all)-1])
	}
}

func TestStoreSweepHighWatermarkFailsClosedOnUnavailableOrMalformedStore(t *testing.T) {
	checkpoint := SweepCheckpoint{
		Version: 1, SeatRef: "seat:test-city/reviewer", Generation: 1, Revision: 1,
	}
	var unavailable *Store
	if _, err := unavailable.CaptureSweepHighWatermark(checkpoint); err == nil {
		t.Fatal("CaptureSweepHighWatermark on unavailable store succeeded")
	}

	store, backing := newSweepDeliveryStore()
	checkpoint, err := store.CreateSweepCheckpoint(checkpoint.SeatRef)
	if err != nil {
		t.Fatalf("CreateSweepCheckpoint: %v", err)
	}
	if _, err := backing.Create(beads.Bead{
		ID: "malformed-actionable", Title: "malformed-actionable", Type: deliveryBeadType,
		Assignee: checkpoint.SeatRef, Labels: []string{deliveryActionableLabel},
		Metadata: beads.StringMap{deliveryDataKey: "not-json", deliveryPhaseKey: string(PhaseStored)},
	}); err != nil {
		t.Fatalf("seed malformed actionable row: %v", err)
	}
	if _, err := store.CaptureSweepHighWatermark(checkpoint); err == nil {
		t.Fatal("CaptureSweepHighWatermark accepted malformed maximum")
	}
	if _, _, err := store.maxActionableKey(""); err == nil {
		t.Fatal("maxActionableKey accepted empty seat")
	}
}

func seededSweepStore(t *testing.T, created []time.Time) *Store {
	t.Helper()
	const seat = "seat:test-city/reviewer"
	rows := make([]beads.Bead, 0, len(created))
	for index, at := range created {
		delivery, err := NewDelivery("city:test-city/messaging", fmt.Sprintf("message-%02d", index), 1, seat, PolicyNotifyOnly, AttentionImmediate, at, nil, "")
		if err != nil {
			t.Fatalf("NewDelivery: %v", err)
		}
		payload, err := encodeDelivery(delivery)
		if err != nil {
			t.Fatalf("encodeDelivery: %v", err)
		}
		rows = append(rows, beads.Bead{
			ID: delivery.ID, Title: delivery.ID, Type: deliveryBeadType, Status: "open",
			Assignee: seat, Labels: []string{deliveryActionableLabel}, CreatedAt: at,
			UpdatedAt: at, Revision: 1,
			Metadata: beads.StringMap{deliveryDataKey: payload, deliveryPhaseKey: string(delivery.Phase)},
		})
	}
	backing := beads.NewMemStoreFrom(len(rows), rows, nil)
	backing.HonorExplicitIDs = true
	return NewStore(backing)
}

func createSweepDelivery(t *testing.T, store *Store, seat string, created time.Time, prefix string) Delivery {
	t.Helper()
	delivery, err := NewDelivery("city:test-city/messaging", fmt.Sprintf("%s-%d", prefix, created.UnixNano()), 1, seat, PolicyNotifyOnly, AttentionImmediate, created, nil, "")
	if err != nil {
		t.Fatalf("NewDelivery: %v", err)
	}
	createdDelivery, err := store.Create(delivery)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return createdDelivery
}

func equalDeliveryKeys(a, b []DeliveryKey) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func newSweepDeliveryStore() (*Store, *beads.MemStore) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	return NewStore(backing), backing
}

func TestStoreSweepCheckpointRejectsMalformedPersistedState(t *testing.T) {
	store, backing := newSweepDeliveryStore()
	seat := "seat:test-city/reviewer"
	id := sweepCheckpointID(seat)
	if _, err := backing.Create(beads.Bead{ID: id, Type: sweepCheckpointBeadType, Metadata: map[string]string{sweepCheckpointDataKey: `{"version":1,"seat_ref":"wrong"}`}}); err != nil {
		t.Fatalf("seed malformed: %v", err)
	}
	if _, err := store.GetSweepCheckpoint(seat); err == nil {
		t.Fatal("GetSweepCheckpoint malformed succeeded")
	}
}
