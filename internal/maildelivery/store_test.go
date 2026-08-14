package maildelivery

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func newDeliveryStore() (*Store, *beads.MemStore) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	return NewStore(backing), backing
}

func TestStoreCreateFailsBeforeWriteAndRetainsConditionalWriterDiagnostic(t *testing.T) {
	backing := beads.NewMemStore()
	store := &Store{beads: backing, writerErr: errors.New("native CAS probe failed")}
	if _, err := store.Create(validDelivery(t)); err == nil || !strings.Contains(err.Error(), "native CAS probe failed") {
		t.Fatalf("Create error = %v, want retained diagnostic", err)
	}
	rows, err := backing.List(beads.ListQuery{AllowScan: true, IncludeClosed: true, TierMode: beads.TierBoth})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("failed Create wrote rows: %#v", rows)
	}
}

func TestStoreListActionableKeysIsSeatScopedBoundedAndKeysetPaged(t *testing.T) {
	store, backing := newDeliveryStore()
	base := validDelivery(t)
	base.CreatedAt = time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC)
	for index, messageID := range []string{"msg-a", "msg-b", "msg-c"} {
		delivery, err := NewDelivery(base.StoreRef, messageID, 1, base.SeatRef, base.Policy, base.Attention, base.CreatedAt.Add(time.Duration(index)*time.Second), nil, "")
		if err != nil {
			t.Fatalf("NewDelivery: %v", err)
		}
		if _, err := store.Create(delivery); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	foreign, err := NewDelivery(base.StoreRef, "msg-foreign", 1, "seat:test-city/other", base.Policy, base.Attention, base.CreatedAt, nil, "")
	if err != nil {
		t.Fatalf("NewDelivery foreign: %v", err)
	}
	if _, err := store.Create(foreign); err != nil {
		t.Fatalf("Create foreign: %v", err)
	}

	first, err := store.ListActionableKeys(base.SeatRef, DeliveryKey{}, 2)
	if err != nil {
		t.Fatalf("ListActionableKeys first: %v", err)
	}
	if len(first) != 2 || compareKey(first[0], first[1]) >= 0 {
		t.Fatalf("first page = %#v", first)
	}
	second, err := store.ListActionableKeys(base.SeatRef, first[1], 2)
	if err != nil {
		t.Fatalf("ListActionableKeys second: %v", err)
	}
	if len(second) != 1 || compareKey(first[1], second[0]) >= 0 {
		t.Fatalf("second page = %#v", second)
	}

	delivery, err := store.Get(second[0].DeliveryID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, phase := range []Phase{PhaseWaitingForActivation, PhaseNotificationRequested, PhaseRuntimeNotified} {
		delivery, err = store.Advance(delivery.ID, delivery.Revision, phase)
		if err != nil {
			t.Fatalf("Advance %s: %v", phase, err)
		}
	}
	if err := backing.Update(delivery.ID, beads.UpdateOpts{RemoveLabels: []string{deliveryActionableLabel}, Metadata: map[string]string{deliveryPhaseKey: string(PhaseDispositioned)}}); err != nil {
		t.Fatalf("seed disposition index: %v", err)
	}
	all, err := store.ListActionableKeys(base.SeatRef, DeliveryKey{}, 10)
	if err != nil {
		t.Fatalf("ListActionableKeys after disposition: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("actionable after disposition = %#v", all)
	}
}

func TestStoreCreateGetAndIdempotentReplay(t *testing.T) {
	store, backing := newDeliveryStore()
	delivery := validDelivery(t)
	created, err := store.Create(delivery)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Revision != 1 {
		t.Fatalf("created revision = %d", created.Revision)
	}
	got, err := store.Get(delivery.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != created {
		t.Fatalf("Get = %#v, want %#v", got, created)
	}
	replayed, err := store.Create(delivery)
	if err != nil {
		t.Fatalf("idempotent Create: %v", err)
	}
	if replayed != created {
		t.Fatalf("replayed = %#v, want %#v", replayed, created)
	}
	rows, err := backing.List(beads.ListQuery{Type: "mail-delivery"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].Description != "" {
		t.Fatalf("stored rows = %#v", rows)
	}
}

func TestStoreCreateConflictingIdentityFailsClosed(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := validDelivery(t)
	if _, err := store.Create(delivery); err != nil {
		t.Fatalf("Create: %v", err)
	}
	conflict := delivery
	conflict.Policy = PolicyReadRequired
	if _, err := store.Create(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting Create = %v, want ErrConflict", err)
	}
}

func TestStoreAdvanceUsesRevisionFence(t *testing.T) {
	store, _ := newDeliveryStore()
	created, err := store.Create(validDelivery(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	advanced, err := store.Advance(created.ID, created.Revision, PhaseWaitingForActivation)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if advanced.Phase != PhaseWaitingForActivation || advanced.Revision <= created.Revision {
		t.Fatalf("advanced = %#v", advanced)
	}
	if _, err := store.Advance(created.ID, created.Revision, PhaseNotificationRequested); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Advance = %v, want ErrConflict", err)
	}
	if _, err := store.Advance(created.ID, advanced.Revision, PhaseRuntimeNotified); err == nil {
		t.Fatal("illegal phase jump succeeded")
	}
}

func TestStoreRejectsMalformedPersistedResource(t *testing.T) {
	store, backing := newDeliveryStore()
	delivery := validDelivery(t)
	_, err := backing.Create(beads.Bead{ID: delivery.ID, Type: "mail-delivery", Title: delivery.ID, Metadata: map[string]string{"mail.delivery.v1": "not-json"}})
	if err != nil {
		t.Fatalf("seed malformed: %v", err)
	}
	if _, err := store.Get(delivery.ID); err == nil {
		t.Fatal("Get malformed succeeded")
	}
}
