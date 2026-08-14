package maildelivery

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func typeField(value any, name string) (reflect.StructField, bool) {
	return reflect.TypeOf(value).FieldByName(name)
}

func createWaitingDelivery(t *testing.T, store *Store) Delivery {
	t.Helper()
	delivery, err := store.Create(validDelivery(t))
	if err != nil {
		t.Fatalf("Create delivery: %v", err)
	}
	delivery, err = store.Advance(delivery.ID, delivery.Revision, PhaseWaitingForActivation)
	if err != nil {
		t.Fatalf("Advance waiting: %v", err)
	}
	return delivery
}

func TestStoreCreateTransportAttemptIsStableAcrossEquivalentFenceReissue(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	createdAt := time.Date(2026, 8, 13, 23, 1, 0, 0, time.UTC)
	first, err := store.CreateTransportAttempt(context.Background(), TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: createdAt,
	}, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("CreateTransportAttempt: %v", err)
	}

	reissued := fence
	reissued.IssuedByRef = "controller:test-city/restarted-reconciler"
	reissued.IssuedAt = reissued.IssuedAt.Add(time.Hour)
	replay, err := store.CreateTransportAttempt(context.Background(), TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: reissued.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: createdAt.Add(time.Hour),
	}, fixedFenceResolver{fence: reissued})
	if err != nil {
		t.Fatalf("equivalent CreateTransportAttempt: %v", err)
	}
	if !reflect.DeepEqual(replay, first) {
		t.Fatalf("equivalent fence minted attempt: %#v != %#v", replay, first)
	}
	if first.State != TransportRequested || first.AttemptID == "" || first.NudgeID == "" || first.Revision == 0 {
		t.Fatalf("attempt = %#v", first)
	}
	if err := first.ValidateAuthority(fence); err != nil {
		t.Fatalf("ValidateAuthority: %v", err)
	}
	staleAuthority := fence
	staleAuthority.AuthorityGeneration++
	if err := first.ValidateAuthority(staleAuthority); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale ValidateAuthority = %v, want ErrConflict", err)
	}
	linked, err := store.Get(delivery.ID)
	if err != nil || linked.Phase != PhaseNotificationRequested {
		t.Fatalf("linked delivery = %#v, %v", linked, err)
	}
}

func TestStoreTransportReceiptCommitsOnceAndConflictingRepeatFails(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	attempt, err := store.CreateTransportAttempt(context.Background(), TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 2, 0, 0, time.UTC),
	}, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("CreateTransportAttempt: %v", err)
	}
	invoking, err := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, attempt.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("BeginTransportInvocation: %v", err)
	}
	if _, err := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, attempt.CreatedAt.Add(time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("second BeginTransportInvocation = %v, want ErrConflict", err)
	}
	receipt := TransportReceipt{
		Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
		State: EffectCommitted, CommitBoundary: TransportCommitBoundaryDestinationAtomic,
		ReceiptRef:    "nudge-receipt:test-city/one",
		ReceiptSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RecordedAt:    time.Date(2026, 8, 13, 23, 3, 0, 0, time.UTC),
	}
	committed, err := store.RecordTransportReceipt(attempt.AttemptID, invoking.Revision, receipt)
	if err != nil {
		t.Fatalf("RecordTransportReceipt: %v", err)
	}
	if committed.State != TransportCommitted || committed.Receipt != receipt {
		t.Fatalf("committed attempt = %#v", committed)
	}
	notified, err := store.Get(delivery.ID)
	if err != nil || notified.Phase != PhaseRuntimeNotified {
		t.Fatalf("notified delivery = %#v, %v", notified, err)
	}
	retryReceipt := receipt
	retryReceipt.RecordedAt = retryReceipt.RecordedAt.Add(time.Hour)
	replay, err := store.RecordTransportReceipt(attempt.AttemptID, invoking.Revision, retryReceipt)
	if err != nil || !reflect.DeepEqual(replay, committed) {
		t.Fatalf("receipt replay = %#v, %v; want %#v", replay, err, committed)
	}
	conflict := receipt
	conflict.ReceiptSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := store.RecordTransportReceipt(attempt.AttemptID, invoking.Revision, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting receipt = %v, want ErrConflict", err)
	}
}

type failTransportFinalizeStore struct {
	*beads.MemStore
	deliveryID string
	failNext   bool
}

func (s *failTransportFinalizeStore) UpdateIfMatch(id string, expected int64, opts beads.UpdateOpts) error {
	if id == s.deliveryID && s.failNext && opts.Metadata[deliveryPhaseKey] == string(PhaseRuntimeNotified) {
		s.failNext = false
		return errors.New("injected runtime-notified write failure")
	}
	return s.MemStore.UpdateIfMatch(id, expected, opts)
}

func TestStoreTransportReceiptRepairsCommitBeforeDeliveryFinalize(t *testing.T) {
	backing := &failTransportFinalizeStore{MemStore: beads.NewMemStore()}
	backing.HonorExplicitIDs = true
	store := NewStore(backing)
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	attempt, err := store.CreateTransportAttempt(context.Background(), TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 3, 30, 0, time.UTC),
	}, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("CreateTransportAttempt: %v", err)
	}
	invoking, err := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, attempt.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("BeginTransportInvocation: %v", err)
	}
	receipt := TransportReceipt{
		Version: 1, AttemptID: invoking.AttemptID, NudgeID: invoking.NudgeID,
		State: EffectCommitted, CommitBoundary: TransportCommitBoundaryProviderReturn,
		ReceiptRef:    "provider-acceptance:test-city/crash-gap",
		ReceiptSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		RecordedAt:    time.Date(2026, 8, 13, 23, 3, 45, 0, time.UTC),
	}
	backing.deliveryID, backing.failNext = delivery.ID, true
	committed, err := store.RecordTransportReceipt(attempt.AttemptID, invoking.Revision, receipt)
	if err == nil || committed.State != TransportCommitted {
		t.Fatalf("first RecordTransportReceipt = %#v, %v", committed, err)
	}
	parked, getErr := store.Get(delivery.ID)
	if getErr != nil || parked.Phase != PhaseNotificationRequested {
		t.Fatalf("delivery after failed finalize = %#v, %v", parked, getErr)
	}

	repaired, err := store.RecordTransportReceipt(attempt.AttemptID, invoking.Revision, receipt)
	if err != nil || !reflect.DeepEqual(repaired, committed) {
		t.Fatalf("repair RecordTransportReceipt = %#v, %v; want %#v", repaired, err, committed)
	}
	notified, getErr := store.Get(delivery.ID)
	if getErr != nil || notified.Phase != PhaseRuntimeNotified {
		t.Fatalf("delivery after repair = %#v, %v", notified, getErr)
	}
}

func TestStoreUnreceiptedAttemptBecomesUnknownAndCannotRedeliver(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	attempt, err := store.CreateTransportAttempt(context.Background(), TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 4, 0, 0, time.UTC),
	}, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("CreateTransportAttempt: %v", err)
	}
	invoking, err := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, attempt.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("BeginTransportInvocation: %v", err)
	}
	unknown, err := store.RecordUnknownTransportState(attempt.AttemptID, invoking.Revision,
		time.Date(2026, 8, 13, 23, 5, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RecordUnknownTransportState: %v", err)
	}
	if unknown.State != TransportUnknownExternalState || unknown.Receipt.State != EffectUnknownExternalState {
		t.Fatalf("unknown attempt = %#v", unknown)
	}
	committed := TransportReceipt{
		Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
		State: EffectCommitted, CommitBoundary: TransportCommitBoundaryDestinationAtomic,
		ReceiptRef:    "nudge-receipt:test-city/late",
		ReceiptSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RecordedAt:    time.Date(2026, 8, 13, 23, 6, 0, 0, time.UTC),
	}
	if _, err := store.RecordTransportReceipt(attempt.AttemptID, invoking.Revision, committed); !errors.Is(err, ErrConflict) {
		t.Fatalf("late commit after unknown = %v, want ErrConflict", err)
	}
	loaded, err := store.TransportAttempt(attempt.AttemptID)
	if err != nil || !reflect.DeepEqual(loaded, unknown) {
		t.Fatalf("TransportAttempt = %#v, %v; want %#v", loaded, err, unknown)
	}
}

func TestStoreTransportAttemptRejectsStaleFenceAndMessageContent(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	stale := fence
	stale.ContinuationEpoch++
	stale.AuthorityIntentSHA256 = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	stale.FenceID = "mail-activation-" + stale.AuthorityIntentSHA256
	_, err := store.CreateTransportAttempt(context.Background(), TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 7, 0, 0, time.UTC),
	}, fixedFenceResolver{fence: stale})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale fence = %v, want ErrConflict", err)
	}

	requestType := TransportAttemptRequest{}
	attemptType := TransportAttempt{}
	for name := range map[string]bool{
		"Body": false, "Subject": false, "Message": false, "Text": false,
	} {
		if field, ok := typeField(requestType, name); ok {
			t.Fatalf("TransportAttemptRequest exposes content field %s (%v)", name, field.Type)
		}
		if field, ok := typeField(attemptType, name); ok {
			t.Fatalf("TransportAttempt exposes content field %s (%v)", name, field.Type)
		}
	}
}

func TestStoreTransportAttemptRejectsMissingAuthorityRevisionAndPhase(t *testing.T) {
	store, _ := newDeliveryStore()
	fence := validFence()
	missingID := "mail-delivery-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base := TransportAttemptRequest{
		DeliveryID: missingID, ExpectedDeliveryRevision: 1,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{missingID},
		CreatedAt: time.Date(2026, 8, 13, 23, 7, 30, 0, time.UTC),
	}
	if _, err := store.CreateTransportAttempt(context.Background(), base, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("missing delivery succeeded")
	}

	delivery, err := store.Create(validDelivery(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	base.DeliveryID = delivery.ID
	base.CoveredDeliveryIDs = []string{delivery.ID}
	if _, err := store.CreateTransportAttempt(context.Background(), base, nil); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("nil authority = %v, want ErrAuthorityUnavailable", err)
	}
	if _, err := store.CreateTransportAttempt(context.Background(), base, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("stored-phase attempt succeeded")
	}

	delivery, err = store.Advance(delivery.ID, delivery.Revision, PhaseWaitingForActivation)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	base.ExpectedDeliveryRevision = delivery.Revision + 1
	if _, err := store.CreateTransportAttempt(context.Background(), base, fixedFenceResolver{fence: fence}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision = %v, want ErrConflict", err)
	}
	if _, err := store.BeginTransportInvocation("mail-attempt-"+strings.Repeat("f", 64), 1, base.CreatedAt); err == nil {
		t.Fatal("missing invocation attempt succeeded")
	}
	if _, err := store.RecordUnknownTransportState("mail-attempt-"+strings.Repeat("f", 64), 1, base.CreatedAt); err == nil {
		t.Fatal("missing unknown attempt succeeded")
	}

	invalidFence := fence
	invalidFence.Version = 0
	validAttempt := TransportAttempt{}
	if err := validAttempt.ValidateAuthority(invalidFence); err == nil {
		t.Fatal("invalid authority fence succeeded")
	}
}

func TestStoreTransportAttemptRejectsInvalidIntentAndTerminalMutation(t *testing.T) {
	store, backing := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 8, 0, 0, time.UTC),
	}
	badTime := request
	badTime.CreatedAt = badTime.CreatedAt.In(time.FixedZone("offset", 3600))
	if _, err := store.CreateTransportAttempt(context.Background(), badTime, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("non-UTC attempt time succeeded")
	}
	duplicateCoverage := request
	duplicateCoverage.CoveredDeliveryIDs = []string{delivery.ID, delivery.ID}
	if _, err := store.CreateTransportAttempt(context.Background(), duplicateCoverage, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("duplicate covered delivery succeeded")
	}
	notCovered := request
	notCovered.CoveredDeliveryIDs = []string{"mail-delivery-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}
	if _, err := store.CreateTransportAttempt(context.Background(), notCovered, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("attempt not covering primary delivery succeeded")
	}
	zeroRevision := request
	zeroRevision.ExpectedDeliveryRevision = 0
	if _, err := store.CreateTransportAttempt(context.Background(), zeroRevision, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("zero expected delivery revision succeeded")
	}

	attempt, err := store.CreateTransportAttempt(context.Background(), request, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("CreateTransportAttempt: %v", err)
	}
	if _, err := store.RecordUnknownTransportState(attempt.AttemptID, attempt.Revision, request.CreatedAt.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown before invocation = %v, want ErrConflict", err)
	}
	invoking, err := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, attempt.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("BeginTransportInvocation: %v", err)
	}
	if _, err := store.RecordUnknownTransportState(attempt.AttemptID, invoking.Revision+1, request.CreatedAt.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale attempt revision = %v, want ErrConflict", err)
	}
	unknown, err := store.RecordUnknownTransportState(attempt.AttemptID, invoking.Revision, request.CreatedAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("RecordUnknownTransportState: %v", err)
	}
	replayed, err := store.RecordUnknownTransportState(attempt.AttemptID, invoking.Revision, request.CreatedAt.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(replayed, unknown) {
		t.Fatalf("unknown replay = %#v, %v; want %#v", replayed, err, unknown)
	}

	malformedID := "mail-attempt-" + "f" + attempt.AttemptID[len("mail-attempt-")+1:]
	if _, err := backing.Create(beads.Bead{
		ID: malformedID, Title: malformedID, Type: transportAttemptBeadType,
		Metadata: beads.StringMap{transportAttemptDataKey: `{"version":1,"message_body":"forbidden"}`},
	}); err != nil {
		t.Fatalf("seed malformed attempt: %v", err)
	}
	if _, err := store.TransportAttempt(malformedID); err == nil {
		t.Fatal("malformed content-bearing attempt succeeded")
	}
}

func TestStoreTransportAttemptRejectsMismatchedReceiptOutcome(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	attempt, err := store.CreateTransportAttempt(context.Background(), TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 9, 0, 0, time.UTC),
	}, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("CreateTransportAttempt: %v", err)
	}
	invoking, err := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, attempt.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("BeginTransportInvocation: %v", err)
	}
	unknownReceipt := TransportReceipt{
		Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
		State:      EffectUnknownExternalState,
		RecordedAt: time.Date(2026, 8, 13, 23, 10, 0, 0, time.UTC),
	}
	if _, err := store.RecordTransportReceipt(attempt.AttemptID, invoking.Revision, unknownReceipt); err == nil {
		t.Fatal("committed state accepted unknown receipt")
	}
	wrongAttempt := unknownReceipt
	wrongAttempt.State = EffectCommitted
	wrongAttempt.CommitBoundary = TransportCommitBoundaryDestinationAtomic
	wrongAttempt.AttemptID = "mail-attempt-" + "e" + attempt.AttemptID[len("mail-attempt-")+1:]
	wrongAttempt.ReceiptRef = "nudge-receipt:test-city/wrong"
	wrongAttempt.ReceiptSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := store.RecordTransportReceipt(attempt.AttemptID, invoking.Revision, wrongAttempt); err == nil {
		t.Fatal("receipt for another attempt succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.CreateTransportAttempt(canceled, TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: unknownReceipt.RecordedAt,
	}, fixedFenceResolver{fence: fence}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled attempt = %v, want context.Canceled", err)
	}
}

func TestStoreTransportAttemptRejectsPersistedStateDrift(t *testing.T) {
	store, backing := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	attempt, err := store.CreateTransportAttempt(context.Background(), TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 11, 0, 0, time.UTC),
	}, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("CreateTransportAttempt: %v", err)
	}
	attempt.State = TransportState("flattering-success")
	payload, err := encodeTransportAttempt(attempt)
	if err != nil {
		t.Fatalf("encodeTransportAttempt: %v", err)
	}
	if err := backing.SetMetadata(attempt.AttemptID, transportAttemptDataKey, payload); err != nil {
		t.Fatalf("seed state drift: %v", err)
	}
	if _, err := store.TransportAttempt(attempt.AttemptID); err == nil {
		t.Fatal("unknown persisted transport state succeeded")
	}
}

func TestStoreTransportAttemptRejectsPersistedIdentityDrift(t *testing.T) {
	for name, mutate := range map[string]func(*TransportAttempt){
		"attempt ID": func(a *TransportAttempt) { a.AttemptID = "mail-attempt-" + strings.Repeat("f", 64) },
		"nudge ID":   func(a *TransportAttempt) { a.NudgeID = "mail-nudge-" + strings.Repeat("f", 64) },
		"authority":  func(a *TransportAttempt) { a.AuthorityGeneration++ },
		"coverage":   func(a *TransportAttempt) { a.CoveredDeliveryIDs = []string{"mail-delivery-" + strings.Repeat("e", 64)} },
	} {
		t.Run(name, func(t *testing.T) {
			store, backing := newDeliveryStore()
			delivery := createWaitingDelivery(t, store)
			fence := validFence()
			attempt, err := store.CreateTransportAttempt(context.Background(), TransportAttemptRequest{
				DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
				ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
				CreatedAt: time.Date(2026, 8, 13, 23, 12, 0, 0, time.UTC),
			}, fixedFenceResolver{fence: fence})
			if err != nil {
				t.Fatalf("CreateTransportAttempt: %v", err)
			}
			originalID := attempt.AttemptID
			mutate(&attempt)
			payload, err := encodeTransportAttempt(attempt)
			if err != nil {
				t.Fatalf("encodeTransportAttempt: %v", err)
			}
			if err := backing.SetMetadata(originalID, transportAttemptDataKey, payload); err != nil {
				t.Fatalf("seed identity drift: %v", err)
			}
			if _, err := store.TransportAttempt(originalID); err == nil {
				t.Fatal("persisted identity drift succeeded")
			}
		})
	}
}

func TestStoreTransportAttemptRepairsResourceCreatedBeforeDeliveryLink(t *testing.T) {
	backing := &failFirstLinkStore{MemStore: beads.NewMemStore()}
	backing.HonorExplicitIDs = true
	store := NewStore(backing)
	delivery := createWaitingDelivery(t, store)
	backing.deliveryID = delivery.ID
	backing.failed = false
	fence := validFence()
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 13, 0, 0, time.UTC),
	}
	if _, err := store.CreateTransportAttempt(context.Background(), request, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("first CreateTransportAttempt succeeded despite injected link failure")
	}
	unlinked, err := store.Get(delivery.ID)
	if err != nil || unlinked.Phase != PhaseWaitingForActivation {
		t.Fatalf("failed link changed delivery = %#v, %v", unlinked, err)
	}
	request.CreatedAt = request.CreatedAt.Add(time.Hour)
	repaired, err := store.CreateTransportAttempt(context.Background(), request, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("repair CreateTransportAttempt: %v", err)
	}
	if !repaired.CreatedAt.Equal(request.CreatedAt.Add(-time.Hour)) {
		t.Fatalf("repair timestamp = %v, want canonical first timestamp", repaired.CreatedAt)
	}
	linked, err := store.Get(delivery.ID)
	if err != nil || linked.Phase != PhaseNotificationRequested {
		t.Fatalf("repaired delivery = %#v, %v", linked, err)
	}
}

func TestTransportAttemptValidateAuthorityRejectsStaleFence(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	attempt, err := store.CreateTransportAttempt(context.Background(), TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 23, 0, 0, time.UTC),
	}, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("CreateTransportAttempt: %v", err)
	}
	if err := attempt.ValidateAuthority(fence); err != nil {
		t.Fatalf("ValidateAuthority current: %v", err)
	}
	stale := fence
	stale.ContinuationEpoch++
	stale.AuthorityIntentSHA256 = strings.Repeat("e", 64)
	stale.FenceID = "mail-activation-" + stale.AuthorityIntentSHA256
	if err := attempt.ValidateAuthority(stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("ValidateAuthority stale = %v, want ErrConflict", err)
	}
}
