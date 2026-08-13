package beadmail

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/maildelivery"
)

func durableIntent() DurableSendIntent {
	return DurableSendIntent{
		MessageID: "gc-mail-aaaaaaaaaaaaaaaaaaaaaaaa",
		StoreRef:  "city:test-city/messaging", SeatRef: "seat:test-city/reviewer",
		Policy: maildelivery.PolicyNotifyOnly, Attention: maildelivery.AttentionImmediate,
	}
}

func TestSendDurableWritesMessageFirstAndContentFreeDelivery(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	provider := New(backing)

	message, delivery, err := provider.SendDurable("sender", "reviewer", "secret subject", "secret body", durableIntent())
	if err != nil {
		t.Fatalf("SendDurable: %v", err)
	}
	if message.ID != durableIntent().MessageID || delivery.MessageID != message.ID || delivery.SeatRef != durableIntent().SeatRef {
		t.Fatalf("message/delivery = %#v / %#v", message, delivery)
	}
	row, err := backing.Get(delivery.ID)
	if err != nil {
		t.Fatalf("getting delivery row: %v", err)
	}
	if row.Description != "" || strings.Contains(row.Title, "secret") || strings.Contains(row.Metadata["mail.delivery.v1"], "secret") {
		t.Fatalf("delivery row leaked content: %#v", row)
	}
	messageRow, err := backing.Get(message.ID)
	if err != nil {
		t.Fatalf("getting message row: %v", err)
	}
	if !messageRow.NoHistory || messageRow.Ephemeral {
		t.Fatalf("message storage = no_history:%v ephemeral:%v, want durable no-history", messageRow.NoHistory, messageRow.Ephemeral)
	}
}

func TestSendDurableExactReplayReturnsExistingMessageAndDelivery(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	provider := New(backing)
	intent := durableIntent()

	firstMessage, firstDelivery, err := provider.SendDurable("sender", "reviewer", "subject", "body", intent)
	if err != nil {
		t.Fatalf("first SendDurable: %v", err)
	}
	secondMessage, secondDelivery, err := provider.SendDurable("sender", "reviewer", "subject", "body", intent)
	if err != nil {
		t.Fatalf("replayed SendDurable: %v", err)
	}
	if !reflect.DeepEqual(secondMessage, firstMessage) || !reflect.DeepEqual(secondDelivery, firstDelivery) {
		t.Fatalf("replay = %#v / %#v, want %#v / %#v", secondMessage, secondDelivery, firstMessage, firstDelivery)
	}
	rows, err := backing.List(beads.ListQuery{AllowScan: true, IncludeClosed: true, TierMode: beads.TierBoth})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("replay rows = %#v, want one message and one delivery", rows)
	}

	if _, _, err := provider.SendDurable("sender", "reviewer", "subject", "changed body", intent); !errors.Is(err, maildelivery.ErrConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrConflict", err)
	}
}

func TestSendDurableValidExpiryPersistsAndReplaysAfterExpiry(t *testing.T) {
	now := time.Now().UTC()
	backing := &failNthCreateStore{MemStore: beads.NewMemStore()}
	backing.HonorExplicitIDs = true
	provider := New(backing)
	intent := durableIntent()
	expiresAt := now.Add(time.Hour)
	intent.ExpiresAt = &expiresAt
	intent.PolicySourceSHA256 = strings.Repeat("a", 64)
	intent.ObservedAt = now

	firstMessage, firstDelivery, err := provider.SendDurable("sender", "reviewer", "subject", "body", intent)
	if err != nil {
		t.Fatalf("SendDurable with valid expiry: %v", err)
	}
	intent.ObservedAt = expiresAt.Add(time.Hour)
	secondMessage, secondDelivery, err := provider.SendDurable("sender", "reviewer", "subject", "body", intent)
	if err != nil {
		t.Fatalf("exact replay after expiry: %v", err)
	}
	if !reflect.DeepEqual(secondMessage, firstMessage) || !reflect.DeepEqual(secondDelivery, firstDelivery) {
		t.Fatalf("expired replay = %#v / %#v, want byte-identical %#v / %#v", secondMessage, secondDelivery, firstMessage, firstDelivery)
	}
	rows, err := backing.List(beads.ListQuery{AllowScan: true, IncludeClosed: true, TierMode: beads.TierBoth})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expired replay rows = %#v, want one message and one delivery", rows)
	}
	if backing.messageCreates != 1 {
		t.Fatalf("expired replay message writes = %d, want the original single write", backing.messageCreates)
	}
}

type failNthCreateStore struct {
	*beads.MemStore
	call           int
	failAt         int
	messageCreates int
}

func (s *failNthCreateStore) Create(bead beads.Bead) (beads.Bead, error) {
	s.call++
	if bead.Type == messageBeadType {
		s.messageCreates++
	}
	if s.call == s.failAt {
		return beads.Bead{}, errors.New("injected create failure")
	}
	return s.MemStore.Create(bead)
}

func TestSendDurableMessageOnlyGapRepairsDeterministically(t *testing.T) {
	backing := &failNthCreateStore{MemStore: beads.NewMemStore(), failAt: 2}
	backing.HonorExplicitIDs = true
	provider := New(backing)
	intent := durableIntent()

	message, _, err := provider.SendDurable("sender", "reviewer", "subject", "body", intent)
	if err == nil {
		t.Fatal("SendDurable succeeded across injected delivery failure")
	}
	if message.ID != intent.MessageID {
		t.Fatalf("message = %#v", message)
	}
	if _, err := backing.Get(intent.MessageID); err != nil {
		t.Fatalf("message was not durable: %v", err)
	}

	backing.failAt = 0
	repaired, err := provider.RepairDurableDelivery(intent.MessageID)
	if err != nil {
		t.Fatalf("RepairDurableDelivery: %v", err)
	}
	wantID, _ := maildelivery.DeliveryID(intent.StoreRef, intent.MessageID, intent.SeatRef)
	if repaired.ID != wantID || repaired.CreatedAt.IsZero() {
		t.Fatalf("repaired = %#v", repaired)
	}
	again, err := provider.RepairDurableDelivery(intent.MessageID)
	if err != nil || !reflect.DeepEqual(again, repaired) {
		t.Fatalf("idempotent repair = %#v, %v", again, err)
	}
}

func TestSendDurableExpiringMessageOnlyGapRepairsAfterExpiry(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	backing := &failNthCreateStore{MemStore: beads.NewMemStore(), failAt: 2}
	backing.HonorExplicitIDs = true
	provider := New(backing)
	intent := durableIntent()
	expiresAt := now.Add(time.Hour)
	intent.ExpiresAt = &expiresAt
	intent.PolicySourceSHA256 = strings.Repeat("b", 64)
	intent.ObservedAt = now

	message, _, err := provider.SendDurable("sender", "reviewer", "subject", "body", intent)
	if err == nil || message.ID != intent.MessageID {
		t.Fatalf("message-only result = %#v, %v", message, err)
	}
	backing.failAt = 0
	repaired, err := provider.RepairDurableDelivery(intent.MessageID)
	if err != nil {
		t.Fatalf("repair after expiry: %v", err)
	}
	again, err := provider.RepairDurableDelivery(intent.MessageID)
	if err != nil || !reflect.DeepEqual(again, repaired) {
		t.Fatalf("exact repair replay = %#v, %v, want %#v", again, err, repaired)
	}
}

func TestSendDurableRejectsMalformedIntentBeforeWrite(t *testing.T) {
	now := time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC)
	backing := &failNthCreateStore{MemStore: beads.NewMemStore()}
	backing.HonorExplicitIDs = true
	provider := New(backing)
	intent := durableIntent()
	intent.ExpiresAt = timePointer(now.Add(-time.Nanosecond))
	intent.PolicySourceSHA256 = strings.Repeat("c", 64)
	intent.ObservedAt = now
	if _, _, err := provider.SendDurable("sender", "reviewer", "subject", "body", intent); err == nil {
		t.Fatal("SendDurable succeeded")
	}
	if backing.call != 0 {
		t.Fatalf("past expiry reached %d store writes, want zero", backing.call)
	}
	rows, err := backing.List(beads.ListQuery{AllowScan: true, IncludeClosed: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("malformed intent wrote rows: %#v", rows)
	}
}

func TestSendDurableRejectsPastExpiryUsingCurrentTimeBeforeWrite(t *testing.T) {
	backing := &failNthCreateStore{MemStore: beads.NewMemStore()}
	backing.HonorExplicitIDs = true
	provider := New(backing)
	intent := durableIntent()
	expiresAt := time.Now().UTC().Add(-time.Minute)
	intent.ExpiresAt = &expiresAt
	intent.PolicySourceSHA256 = strings.Repeat("d", 64)

	if _, _, err := provider.SendDurable("sender", "reviewer", "subject", "body", intent); err == nil {
		t.Fatal("SendDurable accepted an expiry before the current UTC time")
	}
	if backing.call != 0 {
		t.Fatalf("past expiry reached %d store writes, want zero", backing.call)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
