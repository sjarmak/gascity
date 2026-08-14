package beadmail

import (
	"encoding/json"
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

func stableDurableIntent() StableDurableSendIntent {
	return StableDurableSendIntent{
		CityRef: "city:test-city", MessagingStoreRef: "city:test-city/messaging",
		SeatRef: "seat:test-city/reviewer", StableKey: "goal-5-canary-1",
		Policy: maildelivery.PolicyNotifyOnly, Attention: maildelivery.AttentionImmediate,
	}
}

func TestSendDurableStableDerivesDomainBoundIdentityAndReplaysExactly(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	provider := New(backing)
	intent := stableDurableIntent()

	firstMessage, firstDelivery, err := provider.SendDurableStable("sender", "reviewer", "subject", "body", intent)
	if err != nil {
		t.Fatalf("first SendDurableStable: %v", err)
	}
	secondMessage, secondDelivery, err := provider.SendDurableStable("sender", "reviewer", "subject", "body", intent)
	if err != nil {
		t.Fatalf("replayed SendDurableStable: %v", err)
	}
	if firstMessage.ID == "" || !strings.HasPrefix(firstMessage.ID, "gc-mail-") {
		t.Fatalf("stable message id = %q", firstMessage.ID)
	}
	if !reflect.DeepEqual(secondMessage, firstMessage) || !reflect.DeepEqual(secondDelivery, firstDelivery) {
		t.Fatalf("replay = %#v / %#v, want %#v / %#v", secondMessage, secondDelivery, firstMessage, firstDelivery)
	}
	if _, _, err := provider.SendDurableStable("sender", "reviewer", "subject", "changed body", intent); !errors.Is(err, maildelivery.ErrConflict) {
		t.Fatalf("changed-content replay error = %v, want ErrConflict", err)
	}
	if _, _, err := provider.SendDurableStable("sender", "other", "subject", "body", intent); !errors.Is(err, maildelivery.ErrConflict) {
		t.Fatalf("changed-recipient replay error = %v, want ErrConflict", err)
	}

	wantID, err := durableStableMessageID(intent)
	if err != nil {
		t.Fatalf("durableStableMessageID: %v", err)
	}
	if firstMessage.ID != wantID {
		t.Fatalf("message id = %q, want %q", firstMessage.ID, wantID)
	}
	for name, mutate := range map[string]func(*StableDurableSendIntent){
		"city": func(i *StableDurableSendIntent) {
			i.CityRef = "city:other"
			i.MessagingStoreRef = "city:other/messaging"
			i.SeatRef = "seat:other/reviewer"
		},
		"store": func(i *StableDurableSendIntent) { i.MessagingStoreRef = "city:test-city/other" },
		"seat":  func(i *StableDurableSendIntent) { i.SeatRef = "seat:test-city/other" },
		"key":   func(i *StableDurableSendIntent) { i.StableKey = "goal-5-canary-2" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := intent
			mutate(&changed)
			got, err := durableStableMessageID(changed)
			if err == nil && got == wantID {
				t.Fatalf("domain mutation retained id %q", got)
			}
		})
	}
}

func TestSendDurableStableReplayReturnsAdvancedDeliveryPhase(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	provider := New(backing)
	intent := stableDurableIntent()

	message, created, err := provider.SendDurableStable("sender", "reviewer", "subject", "body", intent)
	if err != nil {
		t.Fatalf("first SendDurableStable: %v", err)
	}
	advanced, err := maildelivery.NewStore(backing).Advance(created.ID, created.Revision, maildelivery.PhaseWaitingForActivation)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	replayedMessage, replayedDelivery, err := provider.SendDurableStable("sender", "reviewer", "subject", "body", intent)
	if err != nil {
		t.Fatalf("replay after phase advance: %v", err)
	}
	if replayedMessage.ID != message.ID || !reflect.DeepEqual(replayedDelivery, advanced) {
		t.Fatalf("advanced replay = %#v / %#v, want message %q and %#v", replayedMessage, replayedDelivery, message.ID, advanced)
	}
}

func TestSendDurableStableRejectsUnboundedOrCrossCityIdentityBeforeWrite(t *testing.T) {
	for name, mutate := range map[string]func(*StableDurableSendIntent){
		"empty key":        func(i *StableDurableSendIntent) { i.StableKey = "" },
		"spaced key":       func(i *StableDurableSendIntent) { i.StableKey = "not stable" },
		"oversized key":    func(i *StableDurableSendIntent) { i.StableKey = strings.Repeat("a", 129) },
		"foreign store":    func(i *StableDurableSendIntent) { i.MessagingStoreRef = "city:other/messaging" },
		"foreign seat":     func(i *StableDurableSendIntent) { i.SeatRef = "seat:other/reviewer" },
		"empty seat owner": func(i *StableDurableSendIntent) { i.SeatRef = "seat:test-city/" },
	} {
		t.Run(name, func(t *testing.T) {
			backing := beads.NewMemStore()
			backing.HonorExplicitIDs = true
			intent := stableDurableIntent()
			mutate(&intent)
			if _, _, err := New(backing).SendDurableStable("sender", "reviewer", "subject", "body", intent); err == nil {
				t.Fatal("SendDurableStable succeeded")
			}
			rows, err := backing.List(beads.ListQuery{AllowScan: true, IncludeClosed: true, TierMode: beads.TierBoth})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(rows) != 0 {
				t.Fatalf("invalid intent wrote rows: %#v", rows)
			}
		})
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

type changedExplicitIDStore struct{ *beads.MemStore }

func (s changedExplicitIDStore) Create(bead beads.Bead) (beads.Bead, error) {
	if bead.Type == messageBeadType {
		bead.ID = ""
	}
	return s.MemStore.Create(bead)
}

func TestSendDurableRejectsStoreThatChangesExplicitMessageID(t *testing.T) {
	backing := changedExplicitIDStore{MemStore: beads.NewMemStore()}
	message, delivery, err := New(backing).SendDurable("sender", "reviewer", "subject", "body", durableIntent())
	if err == nil || !strings.Contains(err.Error(), "changed deterministic message ID") {
		t.Fatalf("SendDurable = %#v / %#v, %v; want explicit-ID refusal", message, delivery, err)
	}
	if delivery.ID != "" {
		t.Fatalf("delivery was created after message ID changed: %#v", delivery)
	}
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

func TestRepairDurableDeliveryRejectsSeatMetadataThatDiffersFromMessageAssignee(t *testing.T) {
	backing := &failNthCreateStore{MemStore: beads.NewMemStore(), failAt: 2}
	backing.HonorExplicitIDs = true
	provider := New(backing)
	message, _, err := provider.SendDurable("sender", "reviewer", "subject", "body", durableIntent())
	if err == nil || message.ID == "" {
		t.Fatalf("message-only setup = %#v, %v", message, err)
	}
	row, err := backing.Get(message.ID)
	if err != nil {
		t.Fatalf("Get message: %v", err)
	}
	repair, err := decodeDurableRepair(row.Metadata[durableDeliveryRepairKey])
	if err != nil {
		t.Fatalf("decode repair: %v", err)
	}
	repair.SeatRef = "seat:test-city/other"
	payload, err := json.Marshal(repair)
	if err != nil {
		t.Fatalf("marshal repair: %v", err)
	}
	if err := backing.Update(message.ID, beads.UpdateOpts{Metadata: map[string]string{durableDeliveryRepairKey: string(payload)}}); err != nil {
		t.Fatalf("mutate repair metadata: %v", err)
	}
	backing.failAt = 0
	if _, err := provider.RepairDurableDelivery(message.ID); err == nil || !strings.Contains(err.Error(), "seat") {
		t.Fatalf("RepairDurableDelivery error = %v, want seat/assignee refusal", err)
	}
}

func TestRepairDurableDeliveryRejectsUnavailableStore(t *testing.T) {
	var provider *Provider
	if _, err := provider.RepairDurableDelivery("gc-mail-missing"); err == nil || !strings.Contains(err.Error(), "store unavailable") {
		t.Fatalf("nil provider repair error = %v", err)
	}
	if _, err := (&Provider{}).RepairDurableDelivery("gc-mail-missing"); err == nil || !strings.Contains(err.Error(), "store unavailable") {
		t.Fatalf("nil store repair error = %v", err)
	}
	if _, err := New(beads.NewMemStore()).RepairDurableDelivery("gc-mail-missing"); err == nil || !strings.Contains(err.Error(), "getting message") {
		t.Fatalf("missing message repair error = %v", err)
	}
}

func TestDeliveryFromMessageRowRejectsBrokenRepairIdentity(t *testing.T) {
	createdAt := time.Now().UTC()
	row := beads.Bead{ID: durableIntent().MessageID, Type: messageBeadType, Assignee: "reviewer", Revision: 1, CreatedAt: createdAt}
	repair := durableDeliveryRepair{
		Version: 1, MessageID: row.ID, StoreRef: durableIntent().StoreRef,
		SeatRef: durableIntent().SeatRef, Policy: durableIntent().Policy, Attention: durableIntent().Attention,
	}
	for name, mutate := range map[string]func(*beads.Bead, *durableDeliveryRepair){
		"message identity": func(_ *beads.Bead, repair *durableDeliveryRepair) { repair.MessageID = "other" },
		"message revision": func(row *beads.Bead, _ *durableDeliveryRepair) { row.Revision = 0 },
		"store identity":   func(_ *beads.Bead, repair *durableDeliveryRepair) { repair.StoreRef = "logical-store" },
	} {
		t.Run(name, func(t *testing.T) {
			changedRow, changedRepair := row, repair
			mutate(&changedRow, &changedRepair)
			if _, err := deliveryFromMessageRow(changedRow, changedRepair); err == nil {
				t.Fatal("deliveryFromMessageRow succeeded")
			}
		})
	}
}

func TestSameDurableDeliveryIntentComparesExpiryValue(t *testing.T) {
	wanted, err := maildelivery.NewDelivery(durableIntent().StoreRef, durableIntent().MessageID, 1, durableIntent().SeatRef, durableIntent().Policy, durableIntent().Attention, time.Now().UTC(), nil, "")
	if err != nil {
		t.Fatalf("NewDelivery: %v", err)
	}
	withExpiry := wanted
	expiresAt := wanted.CreatedAt.Add(time.Hour)
	withExpiry.ExpiresAt = &expiresAt
	if sameDurableDeliveryIntent(wanted, withExpiry) {
		t.Fatal("nil and non-nil expiry compared equal")
	}
	differentExpiry := withExpiry
	otherExpiry := expiresAt.Add(time.Hour)
	differentExpiry.ExpiresAt = &otherExpiry
	if sameDurableDeliveryIntent(withExpiry, differentExpiry) {
		t.Fatal("different expiry values compared equal")
	}
	equalExpiry := withExpiry
	equalExpiry.Phase = maildelivery.PhaseRuntimeNotified
	equalExpiry.Revision++
	if !sameDurableDeliveryIntent(withExpiry, equalExpiry) {
		t.Fatal("phase/revision-only change did not retain immutable intent")
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
