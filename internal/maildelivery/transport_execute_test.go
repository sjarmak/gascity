package maildelivery

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestExecuteTransportInvokesOnceAndReturnsCanonicalCommit(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 14, 0, 0, time.UTC),
	}
	invocations := 0
	invoke := func(_ context.Context, attempt TransportAttempt) (TransportReceipt, error) {
		invocations++
		return TransportReceipt{
			Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
			CommitBoundary: TransportCommitBoundaryDestinationAtomic,
			State:          EffectCommitted, ReceiptRef: "nudge-receipt:test-city/exact",
			ReceiptSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			RecordedAt:    time.Date(2026, 8, 13, 23, 15, 0, 0, time.UTC),
		}, nil
	}
	first, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		time.Date(2026, 8, 13, 23, 16, 0, 0, time.UTC), nil, invoke)
	if err != nil || first.State != TransportCommitted || invocations != 1 {
		t.Fatalf("first execution = %#v, %v, invocations=%d", first, err, invocations)
	}
	replay, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		time.Date(2026, 8, 13, 23, 17, 0, 0, time.UTC), nil, invoke)
	if err != nil || replay.AttemptID != first.AttemptID || replay.State != TransportCommitted || invocations != 1 {
		t.Fatalf("replay = %#v, %v, invocations=%d", replay, err, invocations)
	}
}

func TestExecuteTransportInvocationErrorBecomesUnknownWithoutRedelivery(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 18, 0, 0, time.UTC),
	}
	physicalEffects := 0
	invoke := func(context.Context, TransportAttempt) (TransportReceipt, error) {
		physicalEffects++ // destination committed, response was lost
		return TransportReceipt{}, errors.New("response lost after destination commit")
	}
	unknown, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		time.Date(2026, 8, 13, 23, 19, 0, 0, time.UTC), nil, invoke)
	if err == nil || unknown.State != TransportUnknownExternalState || physicalEffects != 1 {
		t.Fatalf("faulted execution = %#v, %v, effects=%d", unknown, err, physicalEffects)
	}
	replay, replayErr := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		time.Date(2026, 8, 13, 23, 20, 0, 0, time.UTC), nil, invoke)
	if replayErr != nil || replay.State != TransportUnknownExternalState || physicalEffects != 1 {
		t.Fatalf("fault replay = %#v, %v, effects=%d", replay, replayErr, physicalEffects)
	}
}

func TestExecuteTransportRetrySafeResponseLossRetriesThenCommits(t *testing.T) {
	for _, tc := range []struct {
		name        string
		deduplicate bool
		wantEffects int
	}{
		{name: "protected", deduplicate: true, wantEffects: 1},
		{name: "naive", deduplicate: false, wantEffects: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newDeliveryStore()
			delivery := createWaitingDelivery(t, store)
			fence := validFence()
			request := TransportAttemptRequest{
				DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
				ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
				CreatedAt: time.Date(2026, 8, 13, 23, 18, 0, 0, time.UTC),
			}
			effects := make(map[string]int)
			calls := 0
			invoke := func(_ context.Context, attempt TransportAttempt) (TransportReceipt, error) {
				calls++
				if !tc.deduplicate || effects[attempt.NudgeID] == 0 {
					effects[attempt.NudgeID]++
				}
				if calls == 1 {
					return TransportReceipt{}, fmt.Errorf("%w: destination committed before response loss", ErrTransportRetrySafe)
				}
				return TransportReceipt{
					Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
					State: EffectCommitted, CommitBoundary: TransportCommitBoundaryDestinationAtomic,
					ReceiptRef: "destination:test", ReceiptSHA256: strings.Repeat("a", 64),
					RecordedAt: request.CreatedAt.Add(time.Minute),
				}, nil
			}

			first, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence}, request.CreatedAt.Add(time.Second), nil, invoke)
			if !errors.Is(err, ErrTransportRetrySafe) || first.State != TransportRequested {
				t.Fatalf("first = %#v, %v, want requested retry-safe", first, err)
			}
			second, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence}, request.CreatedAt.Add(2*time.Second), nil, invoke)
			if err != nil || second.State != TransportCommitted {
				t.Fatalf("second = %#v, %v, want committed", second, err)
			}
			if got := effects[second.NudgeID]; got != tc.wantEffects {
				t.Fatalf("physical effects = %d, want %d", got, tc.wantEffects)
			}
		})
	}
}

func TestExecuteTransportRetrySafeResponseLossRemainsRetryable(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 20, 0, 0, time.UTC),
	}
	invoke := func(context.Context, TransportAttempt) (TransportReceipt, error) {
		return TransportReceipt{}, fmt.Errorf("%w: response lost", ErrTransportRetrySafe)
	}
	first, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence}, request.CreatedAt, nil, invoke)
	if !errors.Is(err, ErrTransportRetrySafe) || errors.Is(err, ErrTransportRetryEscalated) || first.State != TransportRequested || first.InvocationCount != 1 || first.InvocationStartedAt.IsZero() {
		t.Fatalf("first = %#v, %v", first, err)
	}
	second, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence}, request.CreatedAt.Add(time.Second), nil, invoke)
	if !errors.Is(err, ErrTransportRetrySafe) || errors.Is(err, ErrTransportRetryEscalated) || second.State != TransportRequested || second.InvocationCount != 2 {
		t.Fatalf("second = %#v, %v, want requested retry-safe", second, err)
	}
	third, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence}, request.CreatedAt.Add(2*time.Second), nil, invoke)
	if !errors.Is(err, ErrTransportRetrySafe) || !errors.Is(err, ErrTransportRetryEscalated) || third.State != TransportRequested || third.InvocationCount != TransportRetryEscalationThreshold ||
		!strings.Contains(err.Error(), "count 3") || !strings.Contains(err.Error(), "threshold 3") {
		t.Fatalf("third = %#v, %v, want requested escalated retry-safe", third, err)
	}
	fourth, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence}, request.CreatedAt.Add(3*time.Second), nil, invoke)
	if !errors.Is(err, ErrTransportRetrySafe) || !errors.Is(err, ErrTransportRetryEscalated) || fourth.State != TransportRequested || fourth.InvocationCount != 4 {
		t.Fatalf("fourth = %#v, %v, want later requested escalated retry-safe", fourth, err)
	}
	persisted, loadErr := store.TransportAttempt(fourth.AttemptID)
	if loadErr != nil || persisted.State != TransportRequested || persisted.InvocationCount != 4 || persisted.Receipt != (TransportReceipt{}) {
		t.Fatalf("persisted = %#v, %v", persisted, loadErr)
	}
}

func TestExecuteTransportRecoversExpiredInvokingLeaseAsUnknownWithoutCallingProvider(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 21, 0, 0, time.UTC),
	}
	attempt, err := store.CreateTransportAttempt(context.Background(), request, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("CreateTransportAttempt: %v", err)
	}
	startedAt := time.Date(2026, 8, 13, 23, 21, 0, 0, time.UTC)
	if _, err := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, startedAt); err != nil {
		t.Fatalf("BeginTransportInvocation: %v", err)
	}
	called := false
	got, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		startedAt.Add(TransportInvocationLeaseDuration+time.Second), nil, func(context.Context, TransportAttempt) (TransportReceipt, error) {
			called = true
			return TransportReceipt{}, nil
		})
	if err != nil || got.State != TransportUnknownExternalState || called {
		t.Fatalf("invoking recovery = %#v, %v, called=%t", got, err, called)
	}
}

func TestExecuteTransportExpiredInvocationCommitsReadOnlyDestinationReceipt(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	startedAt := time.Date(2026, 8, 13, 23, 21, 0, 0, time.UTC)
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID}, CreatedAt: startedAt,
	}
	attempt, err := store.CreateTransportAttempt(context.Background(), request, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatal(err)
	}
	invoking, err := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	lookupCalls, invokeCalls := 0, 0
	lookup := func(_ context.Context, current TransportAttempt) (TransportReceipt, error) {
		lookupCalls++
		return TransportReceipt{
			Version: 1, AttemptID: current.AttemptID, NudgeID: current.NudgeID,
			State: EffectCommitted, CommitBoundary: TransportCommitBoundaryDestinationAtomic,
			ReceiptRef: "destination:recovered", ReceiptSHA256: strings.Repeat("a", 64), RecordedAt: startedAt.Add(time.Second),
		}, nil
	}
	got, err := ExecuteTransportWithReceiptLookup(context.Background(), store, request, fixedFenceResolver{fence: fence},
		invoking.InvocationLeaseUntil.Add(time.Second), nil, lookup, func(context.Context, TransportAttempt) (TransportReceipt, error) {
			invokeCalls++
			return TransportReceipt{}, nil
		})
	if err != nil || got.State != TransportCommitted || lookupCalls != 1 || invokeCalls != 0 {
		t.Fatalf("recovery = %#v, %v, lookups=%d invokes=%d", got, err, lookupCalls, invokeCalls)
	}
}

func TestExecuteTransportExpiredInvocationRecordsLookupUnknown(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	startedAt := time.Date(2026, 8, 13, 23, 22, 0, 0, time.UTC)
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID}, CreatedAt: startedAt,
	}
	attempt, _ := store.CreateTransportAttempt(context.Background(), request, fixedFenceResolver{fence: fence})
	invoking, _ := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, startedAt)
	uncertainAt := invoking.InvocationLeaseUntil.Add(time.Second)
	got, err := ExecuteTransportWithReceiptLookup(context.Background(), store, request, fixedFenceResolver{fence: fence},
		uncertainAt, nil,
		func(_ context.Context, current TransportAttempt) (TransportReceipt, error) {
			return TransportReceipt{
				Version: 1, AttemptID: current.AttemptID, NudgeID: current.NudgeID,
				State: EffectUnknownExternalState, RecordedAt: startedAt.Add(-24 * time.Hour),
			}, nil
		}, func(context.Context, TransportAttempt) (TransportReceipt, error) {
			t.Fatal("provider invoked")
			return TransportReceipt{}, nil
		})
	if err != nil || got.State != TransportUnknownExternalState || !got.Receipt.RecordedAt.Equal(uncertainAt) {
		t.Fatalf("recovery = %#v, %v", got, err)
	}
}

func TestExecuteTransportConcurrentCallerLeavesLiveInvocationIntact(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	startedAt := time.Date(2026, 8, 13, 23, 22, 0, 0, time.UTC)
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID}, CreatedAt: startedAt,
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	firstDone := make(chan struct{})
	var first TransportAttempt
	var firstErr error
	go func() {
		defer close(firstDone)
		first, firstErr = ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence}, startedAt, nil,
			func(_ context.Context, attempt TransportAttempt) (TransportReceipt, error) {
				once.Do(func() { close(entered) })
				<-release
				return TransportReceipt{
					Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
					State: EffectCommitted, CommitBoundary: TransportCommitBoundaryDestinationAtomic,
					ReceiptRef: "nudge-receipt:test-city/concurrent", ReceiptSHA256: strings.Repeat("a", 64),
					RecordedAt: startedAt.Add(time.Second),
				}, nil
			})
	}()
	<-entered
	second, secondErr := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence}, startedAt.Add(time.Second), nil,
		func(context.Context, TransportAttempt) (TransportReceipt, error) {
			t.Fatal("concurrent caller invoked provider")
			return TransportReceipt{}, nil
		})
	if !errors.Is(secondErr, ErrTransportInvocationInProgress) || second.State != TransportInvoking {
		t.Fatalf("concurrent caller = %#v, %v", second, secondErr)
	}
	close(release)
	<-firstDone
	if firstErr != nil || first.State != TransportCommitted {
		t.Fatalf("first caller = %#v, %v", first, firstErr)
	}
}

func TestExecuteTransportPreflightFailureStaysRequested(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 24, 0, 0, time.UTC),
	}
	called := false
	got, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		time.Date(2026, 8, 13, 23, 25, 0, 0, time.UTC),
		func(context.Context, TransportAttempt) error { return errors.New("target inactive") },
		func(context.Context, TransportAttempt) (TransportReceipt, error) {
			called = true
			return TransportReceipt{}, nil
		})
	if err == nil || got.State != TransportRequested || called {
		t.Fatalf("preflight failure = %#v, %v, called=%t", got, err, called)
	}
	loaded, loadErr := store.TransportAttempt(got.AttemptID)
	if loadErr != nil || loaded.State != TransportRequested {
		t.Fatalf("persisted preflight state = %#v, %v", loaded, loadErr)
	}
}

func TestExecuteTransportRejectsUnavailableOrNonUTCBoundary(t *testing.T) {
	invoke := func(context.Context, TransportAttempt) (TransportReceipt, error) { return TransportReceipt{}, nil }
	if _, err := ExecuteTransport(context.Background(), nil, TransportAttemptRequest{}, nil, time.Now().UTC(), nil, invoke); err == nil {
		t.Fatal("nil store succeeded")
	}
	store, _ := newDeliveryStore()
	if _, err := ExecuteTransport(context.Background(), store, TransportAttemptRequest{}, nil, time.Now().UTC(), nil, nil); err == nil {
		t.Fatal("nil invoker succeeded")
	}
	if _, err := ExecuteTransport(context.Background(), store, TransportAttemptRequest{}, nil, time.Now(), nil, invoke); err == nil {
		t.Fatal("non-UTC uncertainty time succeeded")
	}
}

func TestExecuteTransportAcceptsTypedUnknownWithoutRedelivery(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 33, 0, 0, time.UTC),
	}
	calls := 0
	got, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		time.Date(2026, 8, 13, 23, 34, 0, 0, time.UTC), nil,
		func(_ context.Context, attempt TransportAttempt) (TransportReceipt, error) {
			calls++
			return TransportReceipt{
				Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
				State:      EffectUnknownExternalState,
				RecordedAt: time.Date(2026, 8, 13, 23, 35, 0, 0, time.UTC),
			}, nil
		})
	if err != nil || got.State != TransportUnknownExternalState || calls != 1 {
		t.Fatalf("typed unknown = %#v, %v, calls=%d", got, err, calls)
	}
	replay, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		time.Date(2026, 8, 13, 23, 36, 0, 0, time.UTC), nil,
		func(context.Context, TransportAttempt) (TransportReceipt, error) {
			calls++
			return TransportReceipt{}, nil
		})
	if err != nil || replay.State != TransportUnknownExternalState || calls != 1 {
		t.Fatalf("typed unknown replay = %#v, %v, calls=%d", replay, err, calls)
	}
}

func TestExecuteTransportUntypedProviderOutcomeBecomesUnknown(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 37, 0, 0, time.UTC),
	}
	got, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		time.Date(2026, 8, 13, 23, 38, 0, 0, time.UTC), nil,
		func(context.Context, TransportAttempt) (TransportReceipt, error) { return TransportReceipt{}, nil })
	if err == nil || got.State != TransportUnknownExternalState {
		t.Fatalf("untyped outcome = %#v, %v", got, err)
	}
}

func TestExecuteTransportRepairsCommittedAttemptBeforeReturningTerminal(t *testing.T) {
	backing := &failTransportFinalizeStore{MemStore: beads.NewMemStore()}
	backing.HonorExplicitIDs = true
	store := NewStore(backing)
	delivery := createWaitingDelivery(t, store)
	fence := validFence()
	request := TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 46, 0, 0, time.UTC),
	}
	calls := 0
	invoke := func(_ context.Context, attempt TransportAttempt) (TransportReceipt, error) {
		calls++
		return TransportReceipt{
			Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
			CommitBoundary: TransportCommitBoundaryDestinationAtomic,
			State:          EffectCommitted, ReceiptRef: "provider-acceptance:test-city/replay-repair",
			ReceiptSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			RecordedAt:    time.Date(2026, 8, 13, 23, 47, 0, 0, time.UTC),
		}, nil
	}
	backing.deliveryID, backing.failNext = delivery.ID, true
	committed, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		time.Date(2026, 8, 13, 23, 48, 0, 0, time.UTC), nil, invoke)
	if err == nil || committed.State != TransportCommitted || calls != 1 {
		t.Fatalf("first execution = %#v, %v, calls=%d", committed, err, calls)
	}
	parked, getErr := store.Get(delivery.ID)
	if getErr != nil || parked.Phase != PhaseNotificationRequested {
		t.Fatalf("parked delivery = %#v, %v", parked, getErr)
	}

	repaired, err := ExecuteTransport(context.Background(), store, request, fixedFenceResolver{fence: fence},
		time.Date(2026, 8, 13, 23, 49, 0, 0, time.UTC), nil, invoke)
	if err != nil || !reflect.DeepEqual(repaired, committed) || calls != 1 {
		t.Fatalf("repair execution = %#v, %v, calls=%d; want %#v", repaired, err, calls, committed)
	}
	notified, getErr := store.Get(delivery.ID)
	if getErr != nil || notified.Phase != PhaseRuntimeNotified {
		t.Fatalf("repaired delivery = %#v, %v", notified, getErr)
	}
}
