package maildelivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

type fixedFenceResolver struct {
	fence ActivationFence
	err   error
}

func (r fixedFenceResolver) ResolveMailActivationFence(context.Context, string) (ActivationFence, error) {
	return r.fence, r.err
}

func TestStoreRecordReadReceiptReloadsCurrentFenceAndIsIdempotent(t *testing.T) {
	store, backing := newDeliveryStore()
	delivery, err := store.Create(validDelivery(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fence := validFence()
	recordedAt := time.Date(2026, 8, 13, 20, 3, 0, 0, time.UTC)

	first, err := store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision, fence.FenceID, recordedAt, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("RecordReadReceipt: %v", err)
	}
	if first.Authority.DeliveryID != delivery.ID || first.Authority.InstanceTokenSHA256 != fence.InstanceTokenSHA256 {
		t.Fatalf("receipt = %#v", first)
	}
	second, err := store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision, fence.FenceID, recordedAt, fixedFenceResolver{fence: fence})
	if err != nil || second != first {
		t.Fatalf("idempotent receipt = %#v, %v", second, err)
	}
	laterRetry, err := store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision, fence.FenceID, recordedAt.Add(time.Hour), fixedFenceResolver{fence: fence})
	if err != nil || laterRetry != first {
		t.Fatalf("later idempotent receipt = %#v, %v; want canonical %#v", laterRetry, err, first)
	}
	if first.ExpectedDeliveryRevision != delivery.Revision {
		t.Fatalf("expected delivery revision = %d, want %d", first.ExpectedDeliveryRevision, delivery.Revision)
	}
	rows, err := backing.List(beads.ListQuery{Type: readReceiptBeadType})
	if err != nil || len(rows) != 1 || rows[0].Description != "" {
		t.Fatalf("receipt rows = %#v, %v", rows, err)
	}

	stale := fence
	stale.ContinuationEpoch++
	if _, err := store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision, fence.FenceID, recordedAt, fixedFenceResolver{fence: stale}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale fence error = %v, want ErrConflict", err)
	}
	if _, err := store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision, fence.FenceID, recordedAt, fixedFenceResolver{err: errors.New("controller unavailable")}); !errors.Is(err, ErrAuthorityUnavailable) || strings.Contains(err.Error(), "recorded") {
		t.Fatalf("authority outage error = %v", err)
	}

	loaded, err := store.ReadReceipt(context.Background(), delivery.ID, fixedFenceResolver{fence: fence})
	if err != nil || loaded != first {
		t.Fatalf("ReadReceipt = %#v, %v; want %#v", loaded, err, first)
	}
}

func TestStoreRecordResponseReceiptPinsOriginalAndReplyIdentity(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery, err := store.Create(validDelivery(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fence := validFence()
	recordedAt := time.Date(2026, 8, 13, 20, 4, 0, 0, time.UTC)
	request := ResponseReceiptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, OriginalThreadID: "thread:original-1",
		ReplyMessageID: "gc-mail-reply-1", ReplyMessageRevision: 2,
		ReplyToMessageID: delivery.MessageID, RecordedAt: recordedAt,
	}

	first, err := store.RecordResponseReceipt(context.Background(), request, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("RecordResponseReceipt: %v", err)
	}
	second, err := store.RecordResponseReceipt(context.Background(), request, fixedFenceResolver{fence: fence})
	if err != nil || second != first {
		t.Fatalf("idempotent response = %#v, %v", second, err)
	}
	later := request
	later.RecordedAt = later.RecordedAt.Add(time.Hour)
	canonical, err := store.RecordResponseReceipt(context.Background(), later, fixedFenceResolver{fence: fence})
	if err != nil || canonical != first {
		t.Fatalf("later idempotent response = %#v, %v; want %#v", canonical, err, first)
	}
	if first.ExpectedDeliveryRevision != delivery.Revision {
		t.Fatalf("expected delivery revision = %d, want %d", first.ExpectedDeliveryRevision, delivery.Revision)
	}

	changed := request
	changed.ReplyMessageRevision++
	if _, err := store.RecordResponseReceipt(context.Background(), changed, fixedFenceResolver{fence: fence}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting response error = %v, want ErrConflict", err)
	}
	changed = request
	changed.ReplyToMessageID = "other-message"
	if _, err := store.RecordResponseReceipt(context.Background(), changed, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("wrong ReplyTo succeeded")
	}

	loaded, err := store.ResponseReceipt(context.Background(), delivery.ID, fixedFenceResolver{fence: fence})
	if err != nil || loaded != first {
		t.Fatalf("ResponseReceipt = %#v, %v; want %#v", loaded, err, first)
	}
}

type revisionRaceStore struct {
	*beads.MemStore
	deliveryID string
	raced      bool
}

func (s *revisionRaceStore) UpdateIfMatch(id string, expected int64, opts beads.UpdateOpts) error {
	if id == s.deliveryID && !s.raced {
		s.raced = true
		if err := s.SetMetadata(id, "test.concurrent", "moved"); err != nil {
			return err
		}
	}
	return s.MemStore.UpdateIfMatch(id, expected, opts)
}

func TestRecordReadReceiptDoesNotCommitAfterDeliveryRevisionMoves(t *testing.T) {
	backing := &revisionRaceStore{MemStore: beads.NewMemStore()}
	backing.HonorExplicitIDs = true
	store := NewStore(backing)
	delivery, err := store.Create(validDelivery(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backing.deliveryID = delivery.ID
	_, err = store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision, validFence().FenceID,
		time.Date(2026, 8, 13, 20, 5, 0, 0, time.UTC), fixedFenceResolver{fence: validFence()})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("RecordReadReceipt race = %v, want ErrConflict", err)
	}
	rows, listErr := backing.List(beads.ListQuery{Type: readReceiptBeadType})
	if listErr != nil || len(rows) != 1 {
		t.Fatalf("race persisted receipt resources = %#v, %v", rows, listErr)
	}
	raw, getErr := backing.Get(delivery.ID)
	if getErr != nil || raw.Metadata[readReceiptLinkKey] != "" {
		t.Fatalf("race delivery link = %q, %v; want no committed link", raw.Metadata[readReceiptLinkKey], getErr)
	}
	if _, readErr := store.ReadReceipt(context.Background(), delivery.ID, fixedFenceResolver{fence: validFence()}); !errors.Is(readErr, beads.ErrNotFound) {
		t.Fatalf("unlinked receipt read = %v, want ErrNotFound", readErr)
	}
}

type failFirstLinkStore struct {
	*beads.MemStore
	deliveryID string
	failed     bool
}

func (s *failFirstLinkStore) UpdateIfMatch(id string, expected int64, opts beads.UpdateOpts) error {
	if id == s.deliveryID && !s.failed {
		s.failed = true
		return errors.New("injected link write failure")
	}
	return s.MemStore.UpdateIfMatch(id, expected, opts)
}

func TestRecordReadReceiptRepairsResourceCreatedBeforeLink(t *testing.T) {
	backing := &failFirstLinkStore{MemStore: beads.NewMemStore()}
	backing.HonorExplicitIDs = true
	store := NewStore(backing)
	delivery, err := store.Create(validDelivery(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backing.deliveryID = delivery.ID
	fence := validFence()
	firstTime := time.Date(2026, 8, 13, 20, 9, 0, 0, time.UTC)
	if _, err := store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision, fence.FenceID, firstTime, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("first RecordReadReceipt succeeded despite injected link failure")
	}

	repaired, err := store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision, fence.FenceID, firstTime.Add(time.Hour), fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("repair RecordReadReceipt: %v", err)
	}
	if !repaired.Authority.RecordedAt.Equal(firstTime) {
		t.Fatalf("repair RecordedAt = %v, want canonical %v", repaired.Authority.RecordedAt, firstTime)
	}
	loaded, err := store.ReadReceipt(context.Background(), delivery.ID, fixedFenceResolver{fence: fence})
	if err != nil || loaded != repaired {
		t.Fatalf("ReadReceipt after repair = %#v, %v; want %#v", loaded, err, repaired)
	}
}

func TestRecordResponseReceiptRepairsResourceCreatedBeforeLink(t *testing.T) {
	backing := &failFirstLinkStore{MemStore: beads.NewMemStore()}
	backing.HonorExplicitIDs = true
	store := NewStore(backing)
	delivery, err := store.Create(validDelivery(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backing.deliveryID = delivery.ID
	fence := validFence()
	request := ResponseReceiptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, OriginalThreadID: "thread:repair",
		ReplyMessageID: "gc-mail-repair", ReplyMessageRevision: 2,
		ReplyToMessageID: delivery.MessageID,
		RecordedAt:       time.Date(2026, 8, 13, 20, 15, 0, 0, time.UTC),
	}
	if _, err := store.RecordResponseReceipt(context.Background(), request, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("first RecordResponseReceipt succeeded despite injected link failure")
	}
	if _, err := store.ResponseReceipt(context.Background(), delivery.ID, fixedFenceResolver{fence: fence}); !errors.Is(err, beads.ErrNotFound) {
		t.Fatalf("unlinked response receipt read = %v, want ErrNotFound", err)
	}
	request.RecordedAt = request.RecordedAt.Add(time.Hour)
	repaired, err := store.RecordResponseReceipt(context.Background(), request, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("repair RecordResponseReceipt: %v", err)
	}
	if !repaired.Authority.RecordedAt.Equal(request.RecordedAt.Add(-time.Hour)) {
		t.Fatalf("repair timestamp = %v, want canonical first timestamp", repaired.Authority.RecordedAt)
	}
	loaded, err := store.ResponseReceipt(context.Background(), delivery.ID, fixedFenceResolver{fence: fence})
	if err != nil || loaded != repaired {
		t.Fatalf("ResponseReceipt after repair = %#v, %v; want %#v", loaded, err, repaired)
	}
}

func TestReceiptStoreRejectsInvalidInputsAndPersistedLinks(t *testing.T) {
	store, backing := newDeliveryStore()
	delivery, err := store.Create(validDelivery(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fence := validFence()
	when := time.Date(2026, 8, 13, 20, 16, 0, 0, time.UTC)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.RecordReadReceipt(canceled, delivery.ID, delivery.Revision, fence.FenceID, when, fixedFenceResolver{fence: fence}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled receipt = %v, want context.Canceled", err)
	}
	if _, err := store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision+1, fence.FenceID, when, fixedFenceResolver{fence: fence}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v, want ErrConflict", err)
	}
	if _, err := store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision, fence.FenceID, when, nil); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("nil authority = %v, want ErrAuthorityUnavailable", err)
	}
	badFence := fence
	badFence.SeatRef = "seat:test-city/other"
	if _, err := store.RecordReadReceipt(context.Background(), delivery.ID, delivery.Revision, fence.FenceID, when, fixedFenceResolver{fence: badFence}); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong seat authority = %v, want ErrConflict", err)
	}

	if err := backing.SetMetadata(delivery.ID, readReceiptLinkKey, "bad-link"); err != nil {
		t.Fatalf("seed bad link: %v", err)
	}
	if _, err := store.ReadReceipt(context.Background(), delivery.ID, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("malformed receipt link succeeded")
	}
	if err := backing.SetMetadata(delivery.ID, readReceiptLinkKey, "1:missing-receipt"); err != nil {
		t.Fatalf("seed missing link: %v", err)
	}
	if _, err := store.ReadReceipt(context.Background(), delivery.ID, fixedFenceResolver{fence: fence}); !errors.Is(err, beads.ErrNotFound) {
		t.Fatalf("missing linked receipt = %v, want ErrNotFound", err)
	}
}

func TestDecodeExactResourceRejectsContentAndNonCanonicalJSON(t *testing.T) {
	valid := beads.Bead{
		ID: "mail-read-receipt-test", Title: "mail-read-receipt-test", Type: readReceiptBeadType,
		Metadata: beads.StringMap{readReceiptDataKey: `{"version":1}`},
	}
	var target struct {
		Version int `json:"version"`
	}
	if err := decodeExactResource(valid, readReceiptBeadType, readReceiptDataKey, &target); err != nil || target.Version != 1 {
		t.Fatalf("valid exact resource = %#v, %v", target, err)
	}
	withContent := valid
	withContent.Description = "message body must never be persisted here"
	if err := decodeExactResource(withContent, readReceiptBeadType, readReceiptDataKey, &target); err == nil {
		t.Fatal("content-bearing receipt succeeded")
	}
	unknown := valid
	unknown.Metadata[readReceiptDataKey] = `{"version":1,"message_body":"secret"}`
	if err := decodeExactResource(unknown, readReceiptBeadType, readReceiptDataKey, &target); err == nil {
		t.Fatal("unknown receipt field succeeded")
	}
	trailing := valid
	trailing.Metadata = beads.StringMap{readReceiptDataKey: `{"version":1}{}`}
	if err := decodeExactResource(trailing, readReceiptBeadType, readReceiptDataKey, &target); err == nil {
		t.Fatal("trailing receipt JSON succeeded")
	}
}
