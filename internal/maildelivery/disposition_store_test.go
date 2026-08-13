package maildelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestStoreDispositionDerivesReadProofFromCanonicalReceipt(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := validDelivery(t)
	delivery.Policy = PolicyReadRequired
	created, err := store.Create(delivery)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fence := validFence()
	_, err = store.RecordReadReceipt(context.Background(), created.ID, created.Revision, fence.FenceID,
		time.Date(2026, 8, 13, 20, 6, 0, 0, time.UTC), fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("RecordReadReceipt: %v", err)
	}
	current, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after receipt: %v", err)
	}
	read, err := store.Advance(current.ID, current.Revision, PhaseWaitingForActivation)
	if err != nil {
		t.Fatalf("Advance waiting: %v", err)
	}
	read, err = store.Advance(read.ID, read.Revision, PhaseNotificationRequested)
	if err != nil {
		t.Fatalf("Advance requested: %v", err)
	}
	read, err = store.Advance(read.ID, read.Revision, PhaseRuntimeNotified)
	if err != nil {
		t.Fatalf("Advance notified: %v", err)
	}
	read, err = store.Advance(read.ID, read.Revision, PhaseRead)
	if err != nil {
		t.Fatalf("Advance read: %v", err)
	}

	disposition, err := store.RecordDisposition(context.Background(), DispositionRequest{
		DeliveryID: read.ID, ExpectedDeliveryRevision: read.Revision,
		Reason:     DispositionPolicySatisfiedRead,
		RecordedAt: time.Date(2026, 8, 13, 20, 7, 0, 0, time.UTC),
	}, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("RecordDisposition: %v", err)
	}
	if disposition.ProofReceiptID == "" || disposition.Reason != DispositionPolicySatisfiedRead {
		t.Fatalf("disposition = %#v", disposition)
	}
	retry, err := store.RecordDisposition(context.Background(), DispositionRequest{
		DeliveryID: read.ID, ExpectedDeliveryRevision: read.Revision,
		Reason:     DispositionPolicySatisfiedRead,
		RecordedAt: time.Date(2026, 8, 13, 21, 7, 0, 0, time.UTC),
	}, fixedFenceResolver{fence: fence})
	if err != nil || retry != disposition {
		t.Fatalf("idempotent disposition = %#v, %v; want canonical %#v", retry, err, disposition)
	}
	terminal, err := store.Get(read.ID)
	if err != nil || terminal.Phase != PhaseDispositioned {
		t.Fatalf("terminal delivery = %#v, %v", terminal, err)
	}
	loaded, err := store.Disposition(context.Background(), read.ID, fixedFenceResolver{fence: fence})
	if err != nil || loaded != disposition {
		t.Fatalf("Disposition = %#v, %v; want %#v", loaded, err, disposition)
	}
}

func TestStoreDispositionRejectsCallerBoolAndMissingCanonicalProof(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := validDelivery(t)
	delivery.Policy = PolicyReadRequired
	created, err := store.Create(delivery)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A caller can construct the legacy validation summary, but the mutation API
	// accepts no proof booleans and independently requires a persisted receipt.
	if err := ValidateDisposition(created, DispositionPolicySatisfiedRead, DispositionProof{ReadReceipt: true}); err != nil {
		t.Fatalf("legacy pure validation setup: %v", err)
	}
	if _, err := store.Advance(created.ID, created.Revision, PhaseDispositioned); err == nil {
		t.Fatal("generic Advance bypassed typed disposition authority")
	}
	_, err = store.RecordDisposition(context.Background(), DispositionRequest{
		DeliveryID: created.ID, ExpectedDeliveryRevision: created.Revision,
		Reason:     DispositionPolicySatisfiedRead,
		RecordedAt: time.Date(2026, 8, 13, 20, 8, 0, 0, time.UTC),
	}, fixedFenceResolver{fence: validFence()})
	if err == nil || errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("RecordDisposition without canonical receipt = %v", err)
	}
	got, getErr := store.Get(created.ID)
	if getErr != nil || got.Phase == PhaseDispositioned {
		t.Fatalf("caller bool terminalized delivery = %#v, %v", got, getErr)
	}
}

func TestStoreDispositionDerivesResponseProofAndRejectsConflictingReplay(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := validDelivery(t)
	delivery.Policy = PolicyResponseRequired
	created, err := store.Create(delivery)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fence := validFence()
	request := ResponseReceiptRequest{
		DeliveryID: created.ID, ExpectedDeliveryRevision: created.Revision,
		ExpectedFenceID: fence.FenceID, OriginalThreadID: "thread:original-2",
		ReplyMessageID: "gc-mail-reply-2", ReplyMessageRevision: 3,
		ReplyToMessageID: created.MessageID,
		RecordedAt:       time.Date(2026, 8, 13, 20, 10, 0, 0, time.UTC),
	}
	receipt, err := store.RecordResponseReceipt(context.Background(), request, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("RecordResponseReceipt: %v", err)
	}
	current, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after receipt: %v", err)
	}
	for _, phase := range []Phase{PhaseWaitingForActivation, PhaseNotificationRequested, PhaseRuntimeNotified} {
		current, err = store.Advance(current.ID, current.Revision, phase)
		if err != nil {
			t.Fatalf("Advance %s: %v", phase, err)
		}
	}
	dispositionRequest := DispositionRequest{
		DeliveryID: current.ID, ExpectedDeliveryRevision: current.Revision,
		Reason:     DispositionPolicySatisfiedResponse,
		RecordedAt: time.Date(2026, 8, 13, 20, 11, 0, 0, time.UTC),
	}
	disposition, err := store.RecordDisposition(context.Background(), dispositionRequest, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("RecordDisposition: %v", err)
	}
	if disposition.ProofReceiptID != receipt.ReceiptID {
		t.Fatalf("proof receipt = %q, want %q", disposition.ProofReceiptID, receipt.ReceiptID)
	}
	conflict := dispositionRequest
	conflict.Reason = DispositionPolicySatisfiedRead
	if _, err := store.RecordDisposition(context.Background(), conflict, fixedFenceResolver{fence: fence}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting disposition replay = %v, want ErrConflict", err)
	}
}

func TestStoreDispositionRepairsResourceCreatedBeforeTerminalLink(t *testing.T) {
	backing := &failFirstLinkStore{MemStore: beads.NewMemStore()}
	backing.HonorExplicitIDs = true
	store := NewStore(backing)
	delivery := validDelivery(t)
	delivery.Policy = PolicyReadRequired
	created, err := store.Create(delivery)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fence := validFence()
	if _, err := store.RecordReadReceipt(context.Background(), created.ID, created.Revision, fence.FenceID,
		time.Date(2026, 8, 13, 20, 12, 0, 0, time.UTC), fixedFenceResolver{fence: fence}); err != nil {
		t.Fatalf("RecordReadReceipt: %v", err)
	}
	current, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, phase := range []Phase{PhaseWaitingForActivation, PhaseNotificationRequested, PhaseRuntimeNotified, PhaseRead} {
		current, err = store.Advance(current.ID, current.Revision, phase)
		if err != nil {
			t.Fatalf("Advance %s: %v", phase, err)
		}
	}
	backing.deliveryID = current.ID
	backing.failed = false
	request := DispositionRequest{
		DeliveryID: current.ID, ExpectedDeliveryRevision: current.Revision,
		Reason:     DispositionPolicySatisfiedRead,
		RecordedAt: time.Date(2026, 8, 13, 20, 13, 0, 0, time.UTC),
	}
	if _, err := store.RecordDisposition(context.Background(), request, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("first RecordDisposition succeeded despite injected link failure")
	}
	nonterminal, err := store.Get(current.ID)
	if err != nil || nonterminal.Phase == PhaseDispositioned {
		t.Fatalf("failed link terminalized delivery = %#v, %v", nonterminal, err)
	}
	request.RecordedAt = request.RecordedAt.Add(time.Hour)
	repaired, err := store.RecordDisposition(context.Background(), request, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("repair RecordDisposition: %v", err)
	}
	if !repaired.RecordedAt.Equal(request.RecordedAt.Add(-time.Hour)) {
		t.Fatalf("repair timestamp = %v, want canonical first timestamp", repaired.RecordedAt)
	}
}

func TestStoreDispositionFailsClosedOnInvalidOrUnavailableAuthority(t *testing.T) {
	store, _ := newDeliveryStore()
	created, err := store.Create(validDelivery(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	base := DispositionRequest{
		DeliveryID: created.ID, ExpectedDeliveryRevision: created.Revision,
		Reason:     DispositionPolicySatisfiedNotified,
		RecordedAt: time.Date(2026, 8, 13, 20, 14, 0, 0, time.UTC),
	}
	if _, err := store.RecordDisposition(context.Background(), base, fixedFenceResolver{fence: validFence()}); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("unsupported canonical proof = %v, want ErrAuthorityUnavailable", err)
	}
	base.Reason = DispositionReason("invented")
	if _, err := store.RecordDisposition(context.Background(), base, fixedFenceResolver{fence: validFence()}); err == nil {
		t.Fatal("invalid disposition reason succeeded")
	}
	base.Reason = DispositionPolicySatisfiedRead
	base.RecordedAt = base.RecordedAt.In(time.FixedZone("offset", 3600))
	if _, err := store.RecordDisposition(context.Background(), base, fixedFenceResolver{fence: validFence()}); err == nil {
		t.Fatal("non-UTC disposition time succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.RecordDisposition(canceled, base, fixedFenceResolver{fence: validFence()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled disposition = %v, want context.Canceled", err)
	}
}
