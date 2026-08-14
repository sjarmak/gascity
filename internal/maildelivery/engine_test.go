package maildelivery

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fenceResolverFunc func(context.Context, string) (ActivationFence, error)

func (fn fenceResolverFunc) ResolveMailActivationFence(ctx context.Context, deliveryID string) (ActivationFence, error) {
	return fn(ctx, deliveryID)
}

func TestPlanSweepPageCapturesCanonicalBoundAndAdvancesOnlyAfterCommit(t *testing.T) {
	store, _ := newDeliveryStore()
	seat := "seat:test-city/reviewer"
	createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 0, 0, 0, time.UTC), "engine-a")
	createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 0, 1, 0, time.UTC), "engine-b")
	createSweepDelivery(t, store, "seat:test-city/other", time.Date(2026, 8, 14, 3, 0, 2, 0, time.UTC), "engine-foreign")
	keys, err := store.ListActionableKeys(seat, DeliveryKey{}, 10)
	if err != nil || len(keys) != 2 {
		t.Fatalf("ListActionableKeys = %#v, %v", keys, err)
	}
	firstID, secondID := keys[0].DeliveryID, keys[1].DeliveryID

	checkpoint, plan, err := store.PlanSweepPage(seat, 1)
	if err != nil {
		t.Fatalf("PlanSweepPage: %v", err)
	}
	if len(plan.Page) != 1 || plan.Page[0].DeliveryID != firstID || plan.HighWatermark.DeliveryID != secondID || plan.Wrap {
		t.Fatalf("checkpoint=%#v plan=%#v", checkpoint, plan)
	}
	replayCheckpoint, replayPlan, err := store.PlanSweepPage(seat, 1)
	if err != nil || replayCheckpoint != checkpoint || !equalDeliveryKeys(replayPlan.Page, plan.Page) {
		t.Fatalf("replay checkpoint=%#v plan=%#v err=%v", replayCheckpoint, replayPlan, err)
	}
	advanced, err := store.CommitSweepPage(checkpoint, plan)
	if err != nil || advanced.After.DeliveryID != firstID {
		t.Fatalf("CommitSweepPage=%#v, %v", advanced, err)
	}
	_, next, err := store.PlanSweepPage(seat, 1)
	if err != nil || len(next.Page) != 1 || next.Page[0].DeliveryID != secondID || !next.Wrap {
		t.Fatalf("next=%#v, %v", next, err)
	}
}

func TestProcessSweepPageCommitsOnlyAfterEveryDurableResult(t *testing.T) {
	store, _ := newDeliveryStore()
	seat := "seat:test-city/reviewer"
	createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 3, 0, 0, time.UTC), "process-a")
	createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 3, 1, 0, time.UTC), "process-b")
	keys, err := store.ListActionableKeys(seat, DeliveryKey{}, 10)
	if err != nil || len(keys) != 2 {
		t.Fatalf("ListActionableKeys = %#v, %v", keys, err)
	}
	firstID, secondID := keys[0].DeliveryID, keys[1].DeliveryID
	wantErr := errors.New("crash after first durable result")
	processed := make([]string, 0, 2)
	checkpoint, plan, err := store.ProcessSweepPage(context.Background(), seat, 2, func(_ context.Context, deliveryID string) error {
		processed = append(processed, deliveryID)
		if deliveryID == secondID {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) || len(processed) != 2 || checkpoint.After != (DeliveryKey{}) || !plan.Wrap {
		t.Fatalf("first pass checkpoint=%#v plan=%#v processed=%v err=%v", checkpoint, plan, processed, err)
	}
	persisted, err := store.GetSweepCheckpoint(seat)
	if err != nil || !persisted.After.IsZero() {
		t.Fatalf("persisted checkpoint=%#v, %v", persisted, err)
	}
	processed = processed[:0]
	committed, replay, err := store.ProcessSweepPage(context.Background(), seat, 2, func(_ context.Context, deliveryID string) error {
		processed = append(processed, deliveryID)
		return nil
	})
	if err != nil || committed.Generation != checkpoint.Generation+1 || !committed.After.IsZero() || len(processed) != 2 ||
		processed[0] != firstID || processed[1] != secondID || !replay.Wrap {
		t.Fatalf("replay checkpoint=%#v plan=%#v processed=%v err=%v", committed, replay, processed, err)
	}
}

func TestCommitSweepPageTreatsExactConcurrentWinnerAsIdempotent(t *testing.T) {
	store, _ := newDeliveryStore()
	seat := "seat:test-city/reviewer"
	createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 4, 0, 0, time.UTC), "concurrent")
	checkpoint, plan, err := store.PlanSweepPage(seat, 1)
	if err != nil {
		t.Fatalf("PlanSweepPage: %v", err)
	}
	winner, err := store.CommitSweepPage(checkpoint, plan)
	if err != nil {
		t.Fatalf("winner CommitSweepPage: %v", err)
	}
	replay, err := store.CommitSweepPage(checkpoint, plan)
	if err != nil || replay != winner {
		t.Fatalf("idempotent CommitSweepPage = %#v, %v; want %#v", replay, err, winner)
	}
}

func TestPrepareTransportAttemptTransitionsStoredAndReplaysStableIntent(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createSweepDelivery(t, store, "seat:test-city/reviewer", time.Date(2026, 8, 14, 3, 1, 0, 0, time.UTC), "prepare")
	fence := validFence()
	resolver := fixedFenceResolver{fence: fence}
	preparedAt := time.Date(2026, 8, 14, 3, 2, 0, 0, time.UTC)

	first, err := store.PrepareTransportAttempt(context.Background(), delivery.ID, preparedAt, resolver)
	if err != nil {
		t.Fatalf("PrepareTransportAttempt: %v", err)
	}
	if first.DeliveryID != delivery.ID || first.State != TransportRequested || len(first.CoveredDeliveryIDs) != 1 || first.CoveredDeliveryIDs[0] != delivery.ID {
		t.Fatalf("attempt=%#v", first)
	}
	replay, err := store.PrepareTransportAttempt(context.Background(), delivery.ID, preparedAt.Add(time.Hour), resolver)
	if err != nil || replay.AttemptID != first.AttemptID || replay.NudgeID != first.NudgeID || !replay.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("replay=%#v, %v; want %#v", replay, err, first)
	}
	current, err := store.Get(delivery.ID)
	if err != nil || current.Phase != PhaseNotificationRequested {
		t.Fatalf("delivery=%#v, %v", current, err)
	}
}

func TestPrepareTransportBatchCoalescesOnlyEqualStableAuthority(t *testing.T) {
	store, _ := newDeliveryStore()
	seat := "seat:test-city/reviewer"
	first := createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 2, 10, 0, time.UTC), "batch-a")
	second := createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 2, 11, 0, time.UTC), "batch-b")
	fence := validFence()
	resolver := fenceResolverFunc(func(context.Context, string) (ActivationFence, error) {
		return fence, nil
	})
	preparedAt := time.Date(2026, 8, 14, 3, 2, 30, 0, time.UTC)
	attempt, err := store.PrepareTransportBatch(context.Background(), []string{first.ID, second.ID}, preparedAt, resolver)
	if err != nil {
		t.Fatalf("PrepareTransportBatch: %v", err)
	}
	if len(attempt.CoveredDeliveryIDs) != 2 || attempt.State != TransportRequested {
		t.Fatalf("coalesced attempt = %#v", attempt)
	}
	for _, deliveryID := range []string{first.ID, second.ID} {
		linked, linkErr := store.linkedTransportAttempt(deliveryID)
		if linkErr != nil || linked.AttemptID != attempt.AttemptID {
			t.Fatalf("linked attempt for %s = %#v, %v", deliveryID, linked, linkErr)
		}
	}

	third := createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 2, 12, 0, time.UTC), "batch-c")
	fourth := createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 2, 13, 0, time.UTC), "batch-d")
	different := fence
	different.AuthorityGeneration++
	different.AuthorityIntentSHA256 = "abababababababababababababababababababababababababababababababab"
	different.FenceID = "mail-activation-" + different.AuthorityIntentSHA256
	resolveCount := 0
	mixedResolver := fenceResolverFunc(func(_ context.Context, _ string) (ActivationFence, error) {
		resolveCount++
		if resolveCount == 2 {
			return different, nil
		}
		return fence, nil
	})
	if _, err := store.PrepareTransportBatch(context.Background(), []string{third.ID, fourth.ID}, preparedAt.Add(time.Minute), mixedResolver); !errors.Is(err, ErrConflict) {
		t.Fatalf("mixed-authority batch = %v, want ErrConflict", err)
	}
	for _, deliveryID := range []string{third.ID, fourth.ID} {
		delivery, getErr := store.Get(deliveryID)
		if getErr != nil || delivery.Phase != PhaseWaitingForActivation {
			t.Fatalf("mixed-authority delivery %s = %#v, %v", deliveryID, delivery, getErr)
		}
	}
}

func TestSweepEngineRejectsInvalidAndCanceledWorkBeforeProcessing(t *testing.T) {
	store, _ := newDeliveryStore()
	seat := "seat:test-city/reviewer"
	checkpoint, plan, err := store.PlanSweepPage(seat, 1)
	if err != nil || !checkpoint.HighWatermark.IsZero() || len(plan.Page) != 0 {
		t.Fatalf("idle plan checkpoint=%#v plan=%#v err=%v", checkpoint, plan, err)
	}
	if _, err := store.CommitSweepPage(checkpoint, plan); err == nil {
		t.Fatal("empty high-watermark committed")
	}
	if _, _, err := store.ProcessSweepPage(context.Background(), seat, 1, nil); err == nil {
		t.Fatal("nil processor accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.ProcessSweepPage(ctx, seat, 1, func(context.Context, string) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ProcessSweepPage = %v", err)
	}
	if _, err := store.PrepareTransportAttempt(context.Background(), "mail-delivery-missing", time.Time{}, fixedFenceResolver{fence: validFence()}); err == nil {
		t.Fatal("zero preparation time accepted")
	}
	if _, err := store.PrepareTransportBatch(context.Background(), nil, time.Now().UTC(), fixedFenceResolver{fence: validFence()}); err == nil {
		t.Fatal("empty transport batch accepted")
	}
	nonUTC := time.Date(2026, 8, 14, 3, 3, 0, 0, time.FixedZone("offset", 3600))
	if _, err := store.PrepareTransportBatch(context.Background(), []string{"mail-delivery-missing"}, nonUTC, fixedFenceResolver{fence: validFence()}); err == nil {
		t.Fatal("non-UTC transport batch time accepted")
	}
	if _, err := store.PrepareTransportBatch(context.Background(), []string{"mail-delivery-missing"}, time.Date(2026, 8, 14, 3, 3, 1, 0, time.UTC), fixedFenceResolver{fence: validFence()}); err == nil {
		t.Fatal("missing transport batch delivery accepted")
	}
	delivery := createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 3, 2, 0, time.UTC), "invalid-batch")
	if _, err := store.PrepareTransportBatch(context.Background(), []string{delivery.ID, delivery.ID}, time.Now().UTC(), fixedFenceResolver{fence: validFence()}); err == nil {
		t.Fatal("duplicate transport batch accepted")
	}
	if _, err := store.PrepareTransportBatch(ctx, []string{delivery.ID}, time.Now().UTC(), fixedFenceResolver{fence: validFence()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled transport batch = %v", err)
	}
	if _, err := store.PrepareTransportAttempt(context.Background(), delivery.ID, time.Date(2026, 8, 14, 3, 3, 4, 0, time.UTC), fixedFenceResolver{fence: validFence()}); err != nil {
		t.Fatalf("prepare invalid-batch delivery: %v", err)
	}
	if _, err := store.PrepareTransportBatch(context.Background(), []string{delivery.ID}, time.Date(2026, 8, 14, 3, 3, 5, 0, time.UTC), fixedFenceResolver{fence: validFence()}); err == nil {
		t.Fatal("already-linked delivery accepted into a new transport batch")
	}
	authorityDelivery := createSweepDelivery(t, store, seat, time.Date(2026, 8, 14, 3, 3, 3, 0, time.UTC), "unavailable-batch")
	if _, err := store.PrepareTransportBatch(context.Background(), []string{authorityDelivery.ID}, time.Now().UTC(), fixedFenceResolver{err: errors.New("controller offline")}); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("authority-unavailable transport batch = %v", err)
	}
}

func TestPlanSweepPageRejectsScalarInputsBeforeCheckpointMutation(t *testing.T) {
	store, _ := newDeliveryStore()
	seat := "seat:test-city/reviewer"
	if _, _, err := store.PlanSweepPage(seat, 0); err == nil {
		t.Fatal("zero page size accepted")
	}
	if _, err := store.GetSweepCheckpoint(seat); err == nil {
		t.Fatal("invalid page size created checkpoint")
	}
	if _, _, err := store.PlanSweepPage(" invalid seat ", 1); err == nil {
		t.Fatal("invalid seat accepted")
	}
}

func TestPrepareTransportAttemptMakesAuthorityAndReplayFailuresExplicit(t *testing.T) {
	store, _ := newDeliveryStore()
	delivery := createSweepDelivery(t, store, "seat:test-city/reviewer", time.Date(2026, 8, 14, 3, 20, 0, 0, time.UTC), "prepare-errors")
	preparedAt := time.Date(2026, 8, 14, 3, 21, 0, 0, time.UTC)
	authorityErr := fmt.Errorf("%w: controller offline", ErrAuthorityUnavailable)
	if _, err := store.PrepareTransportAttempt(context.Background(), delivery.ID, preparedAt, fixedFenceResolver{err: authorityErr}); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("authority error = %v", err)
	}
	waiting, err := store.Get(delivery.ID)
	if err != nil || waiting.Phase != PhaseWaitingForActivation {
		t.Fatalf("waiting delivery = %#v, %v", waiting, err)
	}
	fence := validFence()
	first, err := store.PrepareTransportAttempt(context.Background(), delivery.ID, preparedAt, fixedFenceResolver{fence: fence})
	if err != nil {
		t.Fatalf("prepare after authority restore: %v", err)
	}
	stale := fence
	stale.AuthorityGeneration++
	stale.AuthorityIntentSHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	stale.FenceID = "mail-activation-" + stale.AuthorityIntentSHA256
	if _, err := store.PrepareTransportAttempt(context.Background(), delivery.ID, preparedAt.Add(time.Minute), fixedFenceResolver{fence: stale}); !errors.Is(err, ErrTransportAuthorityChanged) {
		t.Fatalf("stale replay = %v; first=%#v", err, first)
	}
	refenced, err := store.PrepareTransportBatch(context.Background(), []string{delivery.ID}, preparedAt.Add(2*time.Minute), fixedFenceResolver{fence: stale})
	if err != nil || refenced.AttemptID == first.AttemptID || refenced.NudgeID == first.NudgeID {
		t.Fatalf("refenced attempt = %#v, %v; first=%#v", refenced, err, first)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.PrepareTransportAttempt(ctx, delivery.ID, preparedAt, fixedFenceResolver{fence: fence}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prepare = %v", err)
	}
	if _, err := store.PrepareTransportAttempt(context.Background(), "mail-delivery-missing", preparedAt, fixedFenceResolver{fence: fence}); err == nil {
		t.Fatal("missing delivery prepared")
	}
	unavailable := NewStore(nil)
	if _, _, err := unavailable.PlanSweepPage(delivery.SeatRef, 1); err == nil {
		t.Fatal("unavailable store planned a page")
	}
}
