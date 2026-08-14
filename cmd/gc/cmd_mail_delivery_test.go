package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/maildelivery"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

type fixedMailDeliveryFenceResolver struct {
	fence maildelivery.ActivationFence
}

type authorityDriftAfterStableNudgeProvider struct {
	*runtime.Fake
	afterStableNudge func()
}

func (p *authorityDriftAfterStableNudgeProvider) NudgeStable(ctx context.Context, name, effectID string, content []runtime.ContentBlock) (runtime.StableNudgeReceipt, error) {
	receipt, err := p.Fake.NudgeStable(ctx, name, effectID, content)
	if err == nil && p.afterStableNudge != nil {
		p.afterStableNudge()
	}
	return receipt, err
}

func (r fixedMailDeliveryFenceResolver) ResolveMailActivationFence(context.Context, string) (maildelivery.ActivationFence, error) {
	return r.fence, nil
}

func testMailDeliveryFence() maildelivery.ActivationFence {
	return maildelivery.ActivationFence{
		Version: 1, FenceID: "mail-activation-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CityRef: "city:test-city", SeatRef: "seat:test-city/reviewer",
		AuthorityKind: maildelivery.AuthorityNamedSessionControllerV1,
		AuthorityRef:  "controller:test-city/session", AuthorityGeneration: 2,
		AuthorityIntentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SessionRef:            "gc-session-test", ContinuationEpoch: 3,
		InstanceTokenSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		IssuedByRef:         "controller:test-city/mail-delivery-cli",
		IssuedAt:            time.Date(2026, 8, 13, 23, 40, 0, 0, time.UTC),
	}
}

func testMailDeliveryAttempt(t *testing.T) (*maildelivery.Store, maildelivery.TransportAttempt, fixedMailDeliveryFenceResolver) {
	t.Helper()
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	delivery, err := maildelivery.NewDelivery(
		"city:test-city/messaging", "msg-cli", 1, "seat:test-city/reviewer",
		maildelivery.PolicyNotifyOnly, maildelivery.AttentionImmediate,
		time.Date(2026, 8, 13, 23, 39, 0, 0, time.UTC), nil, "",
	)
	if err != nil {
		t.Fatalf("NewDelivery: %v", err)
	}
	delivery, err = store.Create(delivery)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	delivery, err = store.Advance(delivery.ID, delivery.Revision, maildelivery.PhaseWaitingForActivation)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	resolver := fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()}
	attempt, err := store.CreateTransportAttempt(context.Background(), maildelivery.TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: resolver.fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 13, 23, 41, 0, 0, time.UTC),
	}, resolver)
	if err != nil {
		t.Fatalf("CreateTransportAttempt: %v", err)
	}
	return store, attempt, resolver
}

func TestMailDeliveryCommandsAreRegistered(t *testing.T) {
	cmd := newMailCmd(&bytes.Buffer{}, &bytes.Buffer{})
	for _, args := range [][]string{{"delivery"}, {"delivery", "status"}, {"delivery", "invoke"}, {"delivery", "reconcile-seat"}} {
		found, _, err := cmd.Find(args)
		if err != nil || found == nil {
			t.Fatalf("Find(%v) = %#v, %v", args, found, err)
		}
	}
	for _, name := range []string{"status", "invoke", "reconcile-seat"} {
		child, _, err := cmd.Find([]string{"delivery", name})
		if err != nil || child.Short == "" {
			t.Fatalf("%s Short = %q, %v", name, child.Short, err)
		}
	}
}

// TestMailDeliveryInvokeAPIPreservesResultAndNeverFallsBackOnResponses is the
// checked Medium HTTP owner for the CLI mutation-routing/no-second-effect proof.
func TestMailDeliveryInvokeAPIPreservesResultAndNeverFallsBackOnResponses(t *testing.T) {
	attempt := maildelivery.TransportAttempt{
		Version: 1, AttemptID: "mail-attempt-result", DeliveryID: "mail-delivery-result",
		State: maildelivery.TransportRequested, InvocationCount: 2,
		CreatedAt: time.Date(2026, 8, 14, 12, 30, 0, 0, time.UTC),
	}
	calls := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v0/city/test-city/mail/delivery/"), "/invoke")
		calls[attemptID]++
		w.Header().Set("Content-Type", "application/json")
		switch attemptID {
		case attempt.AttemptID:
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"ok": false, "failure_code": "retry_safe", "attempt": attempt,
			})
		case "mail-attempt-app-500":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"title": "Internal Server Error", "status": http.StatusInternalServerError,
				"detail": "provider failed after admission",
			})
		default:
			_, _ = w.Write([]byte(`{"ok":true,"attempt":`))
		}
	}))
	t.Cleanup(server.Close)

	cityPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[workspace]\nname = \"test-city\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GC_CITY", cityPath)
	t.Setenv("GC_NO_API", "")
	originalAlive, originalSupervisor := apiRouteControllerAliveHook, apiRouteSupervisorClientHook
	t.Cleanup(func() {
		apiRouteControllerAliveHook = originalAlive
		apiRouteSupervisorClientHook = originalSupervisor
	})
	apiRouteControllerAliveHook = func(string) int { return 0 }
	client := api.NewCityScopedClient(server.URL, "test-city")
	apiRouteSupervisorClientHook = func(string) *api.Client { return client }

	for _, tc := range []struct {
		attemptID  string
		wantStdout string
		wantStderr string
	}{
		{attempt.AttemptID, attempt.AttemptID, "retry_safe"},
		{"mail-attempt-app-500", "", "provider failed after admission"},
		{"mail-attempt-malformed", "", "decoding mail delivery invoke response"},
	} {
		var stdout, stderr bytes.Buffer
		if code := cmdMailDeliveryInvoke(context.Background(), tc.attemptID, &stdout, &stderr); code != 1 {
			t.Fatalf("invoke %q code=%d stdout=%q stderr=%q", tc.attemptID, code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), tc.wantStdout) || !strings.Contains(stderr.String(), tc.wantStderr) {
			t.Fatalf("invoke %q stdout=%q stderr=%q", tc.attemptID, stdout.String(), stderr.String())
		}
		if strings.Contains(stderr.String(), "open city store") || calls[tc.attemptID] != 1 {
			t.Fatalf("invoke %q fell back or reinvoked: calls=%d stderr=%q", tc.attemptID, calls[tc.attemptID], stderr.String())
		}
	}
}

func TestMailDeliveryReconcileCommandRejectsWideExactCanaryBeforeOpeningStore(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdMailDeliveryReconcileSeat(context.Background(), "seat:test-city/reviewer", 2,
		"mail-delivery-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "--limit 1") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

type failingMailDeliveryFenceResolver struct{ err error }

func (r failingMailDeliveryFenceResolver) ResolveMailActivationFence(context.Context, string) (maildelivery.ActivationFence, error) {
	return maildelivery.ActivationFence{}, r.err
}

func createMailDeliveryForReconcile(t *testing.T, store *maildelivery.Store, suffix string, createdAt time.Time) maildelivery.Delivery {
	t.Helper()
	delivery, err := maildelivery.NewDelivery(
		"city:test-city/messaging", "msg-reconcile-"+suffix, 1, "seat:test-city/reviewer",
		maildelivery.PolicyNotifyOnly, maildelivery.AttentionImmediate, createdAt, nil, "",
	)
	if err != nil {
		t.Fatalf("NewDelivery: %v", err)
	}
	delivery, err = store.Create(delivery)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return delivery
}

func TestReconcileMailDeliverySeatAuthorityWaitIsDurableAndDoesNotStarvePage(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	first := createMailDeliveryForReconcile(t, store, "a", time.Date(2026, 8, 14, 3, 10, 0, 0, time.UTC))
	second := createMailDeliveryForReconcile(t, store, "b", time.Date(2026, 8, 14, 3, 10, 1, 0, time.UTC))
	resolver := failingMailDeliveryFenceResolver{err: fmt.Errorf("%w: no active named session", maildelivery.ErrAuthorityUnavailable)}

	report, err := maildelivery.ReconcileSeat(context.Background(), store, first.SeatRef, 2,
		time.Date(2026, 8, 14, 3, 11, 0, 0, time.UTC), resolver, nil)
	if err != nil {
		t.Fatalf("reconcileMailDeliverySeat: %v", err)
	}
	if !report.PageCommitted || report.ActionRequired || len(report.Deliveries) != 2 {
		t.Fatalf("report = %#v", report)
	}
	gotIDs := map[string]bool{report.Deliveries[0].DeliveryID: true, report.Deliveries[1].DeliveryID: true}
	if !gotIDs[first.ID] || !gotIDs[second.ID] {
		t.Fatalf("report IDs = %#v", gotIDs)
	}
	for _, item := range report.Deliveries {
		if item.Outcome != mailDeliveryReconcileWaitingForAuthority || item.Phase != maildelivery.PhaseWaitingForActivation || item.Attempt != nil {
			t.Fatalf("item = %#v", item)
		}
	}
	checkpoint, err := store.GetSweepCheckpoint(first.SeatRef)
	if err != nil || checkpoint.Generation != 2 || !checkpoint.After.IsZero() || !checkpoint.HighWatermark.IsZero() {
		t.Fatalf("checkpoint = %#v, %v", checkpoint, err)
	}
}

func TestReconcileExpectedMailDeliverySeatRejectsDifferentPageBeforeDeliveryEffect(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	first := createMailDeliveryForReconcile(t, store, "canary-a", time.Date(2026, 8, 14, 3, 9, 0, 0, time.UTC))
	second := createMailDeliveryForReconcile(t, store, "canary-b", time.Date(2026, 8, 14, 3, 9, 1, 0, time.UTC))
	effects := 0
	report, err := maildelivery.ReconcileExpectedSeat(context.Background(), store, first.SeatRef, 1, second.ID,
		time.Date(2026, 8, 14, 3, 9, 30, 0, time.UTC), fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()},
		func(context.Context, maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
			effects++
			return maildelivery.TransportAttempt{}, nil
		})
	if err == nil || !strings.Contains(err.Error(), "differs from expected") || effects != 0 || report.PageCommitted || len(report.Deliveries) != 0 {
		t.Fatalf("report=%#v effects=%d err=%v", report, effects, err)
	}
	current, getErr := store.Get(first.ID)
	if getErr != nil || current.Phase != maildelivery.PhaseStored {
		t.Fatalf("first delivery changed before exact canary gate: %#v, %v", current, getErr)
	}
	checkpoint, checkpointErr := store.GetSweepCheckpoint(first.SeatRef)
	if checkpointErr != nil || checkpoint.Generation != 1 || checkpoint.HighWatermark.IsZero() {
		t.Fatalf("refused canary must retain its checkpoint-only observation: %#v, %v", checkpoint, checkpointErr)
	}
}

func TestReconcileExpectedMailDeliverySeatReportsExactTerminalCanaryWithoutFailure(t *testing.T) {
	store, attempt, resolver := testMailDeliveryAttempt(t)
	invoking, err := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, attempt.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("BeginTransportInvocation: %v", err)
	}
	_, err = store.RecordTransportReceipt(attempt.AttemptID, invoking.Revision, maildelivery.TransportReceipt{
		Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
		State: maildelivery.EffectCommitted, CommitBoundary: maildelivery.TransportCommitBoundaryDestinationAtomic,
		ReceiptRef: "nudge-receipt:test-city/terminal", ReceiptSHA256: strings.Repeat("c", 64),
		RecordedAt: attempt.CreatedAt.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("RecordTransportReceipt: %v", err)
	}
	current, err := store.Get(attempt.DeliveryID)
	if err != nil {
		t.Fatalf("Get notified delivery: %v", err)
	}
	_, err = store.RecordDisposition(context.Background(), maildelivery.DispositionRequest{
		DeliveryID: current.ID, ExpectedDeliveryRevision: current.Revision,
		Reason: maildelivery.DispositionPolicySatisfiedNotified, RecordedAt: attempt.CreatedAt.Add(3 * time.Second),
	}, resolver)
	if err != nil {
		t.Fatalf("RecordDisposition: %v", err)
	}

	effects := 0
	report, err := maildelivery.ReconcileExpectedSeat(context.Background(), store, current.SeatRef, 1, current.ID,
		attempt.CreatedAt.Add(4*time.Second), resolver, func(context.Context, maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
			effects++
			return maildelivery.TransportAttempt{}, nil
		})
	if err != nil || effects != 0 || report.ExpectedDeliveryID != current.ID || report.ExpectedDeliveryPhase != maildelivery.PhaseDispositioned ||
		report.PageCommitted || len(report.Deliveries) != 0 {
		t.Fatalf("terminal report=%#v effects=%d err=%v", report, effects, err)
	}
}

func TestReconcileMailDeliverySeatUnknownCommitsPageAndReportsAction(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	delivery := createMailDeliveryForReconcile(t, store, "unknown", time.Date(2026, 8, 14, 3, 12, 0, 0, time.UTC))
	resolver := fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()}
	execute := func(_ context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
		attempt.State = maildelivery.TransportUnknownExternalState
		attempt.Receipt = maildelivery.TransportReceipt{
			Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
			State: maildelivery.EffectUnknownExternalState, RecordedAt: time.Date(2026, 8, 14, 3, 13, 0, 0, time.UTC),
		}
		return attempt, errors.New("provider response lost")
	}

	report, err := maildelivery.ReconcileSeat(context.Background(), store, delivery.SeatRef, 1,
		time.Date(2026, 8, 14, 3, 12, 30, 0, time.UTC), resolver, execute)
	if err != nil {
		t.Fatalf("reconcileMailDeliverySeat: %v", err)
	}
	if !report.PageCommitted || !report.ActionRequired || len(report.Deliveries) != 1 ||
		report.Deliveries[0].Outcome != mailDeliveryReconcileUnknownExternalState || report.Deliveries[0].Attempt == nil {
		t.Fatalf("report = %#v", report)
	}
	checkpoint, err := store.GetSweepCheckpoint(delivery.SeatRef)
	if err != nil || checkpoint.Generation != 2 || !checkpoint.After.IsZero() {
		t.Fatalf("checkpoint = %#v, %v", checkpoint, err)
	}
}

func TestReconcileMailDeliverySeatInfrastructureFailureReplaysUnchangedPage(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	delivery := createMailDeliveryForReconcile(t, store, "infra", time.Date(2026, 8, 14, 3, 14, 0, 0, time.UTC))
	resolver := fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()}
	wantErr := errors.New("provider unavailable")
	execute := func(_ context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
		return attempt, wantErr
	}

	report, err := maildelivery.ReconcileSeat(context.Background(), store, delivery.SeatRef, 1,
		time.Date(2026, 8, 14, 3, 15, 0, 0, time.UTC), resolver, execute)
	if !errors.Is(err, wantErr) || report.PageCommitted {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	checkpoint, getErr := store.GetSweepCheckpoint(delivery.SeatRef)
	if getErr != nil || !checkpoint.After.IsZero() || checkpoint.Generation != 1 {
		t.Fatalf("checkpoint = %#v, %v", checkpoint, getErr)
	}
	_, replay, planErr := store.PlanSweepPage(delivery.SeatRef, 1)
	if planErr != nil || len(replay.Page) != 1 || replay.Page[0].DeliveryID != delivery.ID {
		t.Fatalf("replay = %#v, %v", replay, planErr)
	}
}

func TestReconcileMailDeliverySeatCoalescesPageIntoOneDurableEffect(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	first := createMailDeliveryForReconcile(t, store, "coalesce-a", time.Date(2026, 8, 14, 3, 30, 0, 0, time.UTC))
	second := createMailDeliveryForReconcile(t, store, "coalesce-b", time.Date(2026, 8, 14, 3, 30, 1, 0, time.UTC))
	resolver := fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()}
	effects := 0
	execute := func(ctx context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
		effects++
		if len(attempt.CoveredDeliveryIDs) != 2 || mailDeliveryNudgeText(len(attempt.CoveredDeliveryIDs)) != "2 actionable mail deliveries; run gc mail inbox" {
			t.Fatalf("attempt coverage/text = %#v / %q", attempt.CoveredDeliveryIDs, mailDeliveryNudgeText(len(attempt.CoveredDeliveryIDs)))
		}
		return executeMailDeliveryAttempt(ctx, store, attempt, resolver,
			time.Date(2026, 8, 14, 3, 31, 0, 0, time.UTC),
			func(_ context.Context, invoking maildelivery.TransportAttempt) (maildelivery.TransportReceipt, error) {
				return maildelivery.TransportReceipt{
					Version: 1, AttemptID: invoking.AttemptID, NudgeID: invoking.NudgeID,
					State: maildelivery.EffectCommitted, CommitBoundary: maildelivery.TransportCommitBoundaryDestinationAtomic,
					ReceiptRef: "destination:coalesced", ReceiptSHA256: strings.Repeat("d", 64),
					RecordedAt: time.Date(2026, 8, 14, 3, 31, 1, 0, time.UTC),
				}, nil
			})
	}
	report, err := maildelivery.ReconcileSeat(context.Background(), store, first.SeatRef, 2,
		time.Date(2026, 8, 14, 3, 30, 30, 0, time.UTC), resolver, execute)
	if err != nil || effects != 1 || !report.PageCommitted || len(report.Deliveries) != 2 {
		t.Fatalf("report=%#v effects=%d err=%v", report, effects, err)
	}
	if report.Deliveries[0].Attempt == nil || report.Deliveries[1].Attempt == nil ||
		report.Deliveries[0].Attempt.AttemptID != report.Deliveries[1].Attempt.AttemptID ||
		report.Deliveries[0].Attempt.NudgeID != report.Deliveries[1].Attempt.NudgeID {
		t.Fatalf("coalesced items = %#v", report.Deliveries)
	}
	for _, delivery := range []maildelivery.Delivery{first, second} {
		current, loadErr := store.Get(delivery.ID)
		if loadErr != nil || current.Phase != maildelivery.PhaseDispositioned {
			t.Fatalf("delivery %s = %#v, %v", delivery.ID, current, loadErr)
		}
	}
}

func TestReconcileMailDeliverySeatRefencesRequestedGroupWithoutStarvation(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	first := createMailDeliveryForReconcile(t, store, "refence-a", time.Date(2026, 8, 14, 3, 32, 0, 0, time.UTC))
	second := createMailDeliveryForReconcile(t, store, "refence-b", time.Date(2026, 8, 14, 3, 32, 1, 0, time.UTC))
	firstResolver := fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()}
	firstAttemptID := ""
	retrySafe := func(ctx context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
		firstAttemptID = attempt.AttemptID
		return executeMailDeliveryAttempt(ctx, store, attempt, firstResolver,
			time.Date(2026, 8, 14, 3, 33, 0, 0, time.UTC),
			func(context.Context, maildelivery.TransportAttempt) (maildelivery.TransportReceipt, error) {
				return maildelivery.TransportReceipt{}, fmt.Errorf("%w: destination refused before effect", maildelivery.ErrTransportRetrySafe)
			})
	}
	firstReport, err := maildelivery.ReconcileSeat(context.Background(), store, first.SeatRef, 2,
		time.Date(2026, 8, 14, 3, 32, 30, 0, time.UTC), firstResolver, retrySafe)
	if err != nil || !firstReport.PageCommitted || firstReport.Deliveries[0].Outcome != mailDeliveryReconcileRetryable {
		t.Fatalf("first report=%#v err=%v", firstReport, err)
	}
	refenced := testMailDeliveryFence()
	refenced.AuthorityGeneration++
	refenced.AuthorityIntentSHA256 = strings.Repeat("e", 64)
	refenced.FenceID = "mail-activation-" + refenced.AuthorityIntentSHA256
	secondResolver := fixedMailDeliveryFenceResolver{fence: refenced}
	effects := 0
	commit := func(ctx context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
		effects++
		return executeMailDeliveryAttempt(ctx, store, attempt, secondResolver,
			time.Date(2026, 8, 14, 3, 34, 0, 0, time.UTC),
			func(_ context.Context, invoking maildelivery.TransportAttempt) (maildelivery.TransportReceipt, error) {
				return maildelivery.TransportReceipt{
					Version: 1, AttemptID: invoking.AttemptID, NudgeID: invoking.NudgeID,
					State: maildelivery.EffectCommitted, CommitBoundary: maildelivery.TransportCommitBoundaryDestinationAtomic,
					ReceiptRef: "destination:refenced", ReceiptSHA256: strings.Repeat("f", 64),
					RecordedAt: time.Date(2026, 8, 14, 3, 34, 1, 0, time.UTC),
				}, nil
			})
	}
	secondReport, err := maildelivery.ReconcileSeat(context.Background(), store, second.SeatRef, 2,
		time.Date(2026, 8, 14, 3, 33, 30, 0, time.UTC), secondResolver, commit)
	if err != nil || !secondReport.PageCommitted || effects != 1 || len(secondReport.Deliveries) != 2 {
		t.Fatalf("second report=%#v effects=%d err=%v", secondReport, effects, err)
	}
	if secondReport.Deliveries[0].Attempt == nil || secondReport.Deliveries[0].Attempt.AttemptID == firstAttemptID {
		t.Fatalf("refence did not replace stable attempt: %#v", secondReport.Deliveries)
	}
	checkpoint, loadErr := store.GetSweepCheckpoint(first.SeatRef)
	if loadErr != nil || checkpoint.Generation != 3 || !checkpoint.After.IsZero() {
		t.Fatalf("checkpoint=%#v err=%v", checkpoint, loadErr)
	}
}

func TestReconcileMailDeliverySeatEscalatedRetryRemainsRetryableAndActionable(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	delivery := createMailDeliveryForReconcile(t, store, "retry-escalated", time.Date(2026, 8, 14, 3, 34, 0, 0, time.UTC))
	resolver := fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()}
	report, err := maildelivery.ReconcileSeat(context.Background(), store, delivery.SeatRef, 1,
		time.Date(2026, 8, 14, 3, 34, 30, 0, time.UTC), resolver,
		func(_ context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
			attempt.ReceiptLookupFailureCount = maildelivery.TransportReceiptLookupEscalationThreshold
			return attempt, errors.Join(maildelivery.ErrTransportRetryEscalated, maildelivery.ErrTransportReceiptLookupRetryLater)
		})
	if err != nil || !report.PageCommitted || !report.ActionRequired || len(report.Deliveries) != 1 ||
		report.Deliveries[0].Outcome != mailDeliveryReconcileRetryable || report.Deliveries[0].Attempt == nil ||
		report.Deliveries[0].Attempt.ReceiptLookupFailureCount != maildelivery.TransportReceiptLookupEscalationThreshold {
		t.Fatalf("report=%#v err=%v", report, err)
	}
}

func TestMailDeliveryWorkerInvokerRetainsCommittedReceiptAcrossPostEffectAuthorityDrift(t *testing.T) {
	ctx := context.Background()
	cityPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityPath, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, ".gc", "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	sessBacking := beads.NewMemStore()
	provider := &authorityDriftAfterStableNudgeProvider{Fake: runtime.NewFake()}
	mgr := newSessionManagerWithConfig(cityPath, sessBacking, provider, cfg)
	info, err := mgr.CreateSession(ctx, session.CreateOptions{
		Alias: "reviewer", ExplicitName: "mail-reviewer", Template: "reviewer", Title: "Reviewer",
		Command: "claude", WorkDir: t.TempDir(), Provider: "exec", Transport: "exec",
		ExtraMeta: map[string]string{session.NamedSessionIdentityMetadata: "reviewer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := &mailDeliveryFenceResolver{store: sessionFrontDoor(sessBacking), sessionRef: info.ID, options: session.MailActivationFenceOptions{
		CityRef: "city:test-city", SeatRef: "seat:test-city/reviewer", ConfigSHA256: strings.Repeat("a", 64),
		IssuedByRef: "controller:test-city/mail-delivery-cli",
	}}
	fence, err := resolver.ResolveMailActivationFence(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	delivery, err := maildelivery.NewDelivery("city:test-city/messaging", "msg-authority-drift", 1, fence.SeatRef,
		maildelivery.PolicyNotifyOnly, maildelivery.AttentionImmediate, time.Date(2026, 8, 14, 15, 5, 0, 0, time.UTC), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	delivery, err = store.Create(delivery)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err = store.Advance(delivery.ID, delivery.Revision, maildelivery.PhaseWaitingForActivation)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.CreateTransportAttempt(ctx, maildelivery.TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision, ExpectedFenceID: fence.FenceID,
		CoveredDeliveryIDs: []string{delivery.ID}, CreatedAt: time.Date(2026, 8, 14, 15, 6, 0, 0, time.UTC),
	}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	provider.afterStableNudge = func() {
		if setErr := sessBacking.SetMetadata(info.ID, "instance_token", "replacement-after-provider-acceptance"); setErr != nil {
			t.Errorf("replace authority: %v", setErr)
		}
	}
	receipt, err := mailDeliveryWorkerInvoker(cityPath, cfg, sessBacking, provider, resolver)(ctx, attempt)
	if err != nil || receipt.State != maildelivery.EffectCommitted || receipt.ReceiptRef == "" || provider.StableNudgeEffectCount(attempt.NudgeID) != 1 {
		t.Fatalf("invoke receipt=%#v err=%v effects=%d", receipt, err, provider.StableNudgeEffectCount(attempt.NudgeID))
	}
}

func TestReconcileMailDeliverySeatExistingAttemptWaitsWhenAuthorityUnavailable(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	delivery := createMailDeliveryForReconcile(t, store, "authority-offline", time.Date(2026, 8, 14, 3, 35, 0, 0, time.UTC))
	resolver := fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()}
	retrySafe := func(ctx context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
		return executeMailDeliveryAttempt(ctx, store, attempt, resolver,
			time.Date(2026, 8, 14, 3, 35, 30, 0, time.UTC),
			func(context.Context, maildelivery.TransportAttempt) (maildelivery.TransportReceipt, error) {
				return maildelivery.TransportReceipt{}, fmt.Errorf("%w: offline before effect", maildelivery.ErrTransportRetrySafe)
			})
	}
	first, err := maildelivery.ReconcileSeat(context.Background(), store, delivery.SeatRef, 1,
		time.Date(2026, 8, 14, 3, 35, 10, 0, time.UTC), resolver, retrySafe)
	if err != nil || first.Deliveries[0].Outcome != mailDeliveryReconcileRetryable {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	offline := failingMailDeliveryFenceResolver{err: fmt.Errorf("%w: no active named session", maildelivery.ErrAuthorityUnavailable)}
	called := false
	second, err := maildelivery.ReconcileSeat(context.Background(), store, delivery.SeatRef, 1,
		time.Date(2026, 8, 14, 3, 36, 0, 0, time.UTC), offline,
		func(context.Context, maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
			called = true
			return maildelivery.TransportAttempt{}, nil
		})
	if err != nil || called || !second.PageCommitted || second.ActionRequired || len(second.Deliveries) != 1 ||
		second.Deliveries[0].Outcome != mailDeliveryReconcileWaitingForAuthority {
		t.Fatalf("second=%#v called=%v err=%v", second, called, err)
	}
}

func TestReconcileMailDeliverySeatUnknownRemainsActionRequiredAcrossRefence(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	delivery := createMailDeliveryForReconcile(t, store, "unknown-refence", time.Date(2026, 8, 14, 3, 37, 0, 0, time.UTC))
	resolver := fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()}
	unknown := func(ctx context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
		return executeMailDeliveryAttempt(ctx, store, attempt, resolver,
			time.Date(2026, 8, 14, 3, 37, 30, 0, time.UTC),
			func(context.Context, maildelivery.TransportAttempt) (maildelivery.TransportReceipt, error) {
				return maildelivery.TransportReceipt{}, errors.New("provider response lost after possible effect")
			})
	}
	first, err := maildelivery.ReconcileSeat(context.Background(), store, delivery.SeatRef, 1,
		time.Date(2026, 8, 14, 3, 37, 10, 0, time.UTC), resolver, unknown)
	if err != nil || !first.ActionRequired || first.Deliveries[0].Outcome != mailDeliveryReconcileUnknownExternalState {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	refenced := testMailDeliveryFence()
	refenced.AuthorityGeneration++
	refenced.AuthorityIntentSHA256 = strings.Repeat("9", 64)
	refenced.FenceID = "mail-activation-" + refenced.AuthorityIntentSHA256
	called := false
	second, err := maildelivery.ReconcileSeat(context.Background(), store, delivery.SeatRef, 1,
		time.Date(2026, 8, 14, 3, 38, 0, 0, time.UTC), fixedMailDeliveryFenceResolver{fence: refenced},
		func(context.Context, maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
			called = true
			return maildelivery.TransportAttempt{}, nil
		})
	if err != nil || called || !second.PageCommitted || !second.ActionRequired ||
		second.Deliveries[0].Outcome != mailDeliveryReconcileUnknownExternalState || second.Deliveries[0].Attempt == nil ||
		second.Deliveries[0].Attempt.State != maildelivery.TransportUnknownExternalState {
		t.Fatalf("second=%#v called=%v err=%v", second, called, err)
	}
}

func TestClassifyMailDeliveryExecutionDoesNotHideIdentityConflict(t *testing.T) {
	_, err := maildelivery.ClassifyExecution(maildelivery.TransportAttempt{}, fmt.Errorf("%w: identity differs", maildelivery.ErrConflict))
	if !errors.Is(err, maildelivery.ErrConflict) {
		t.Fatalf("identity conflict classified as retryable: %v", err)
	}
}

func TestReconcileMailDeliverySeatRejectsInvalidObservationBeforeStoreMutation(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	observedAt := time.Date(2026, 8, 14, 3, 15, 0, 0, time.FixedZone("offset", 3600))
	if _, err := maildelivery.ReconcileSeat(context.Background(), store, "seat:test-city/reviewer", 1,
		observedAt, fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()}, nil); err == nil {
		t.Fatal("non-UTC observation accepted")
	}
	if _, err := store.GetSweepCheckpoint("seat:test-city/reviewer"); !errors.Is(err, beads.ErrNotFound) {
		t.Fatalf("invalid input mutated checkpoint: %v", err)
	}
}

func TestWriteMailDeliveryReconcileReportIsTypedAndContentFree(t *testing.T) {
	report := mailDeliveryReconcileReport{
		SchemaVersion: "mail-delivery-reconcile/v1", SeatRef: "seat:test-city/reviewer",
		ObservedAt: time.Date(2026, 8, 14, 3, 16, 0, 0, time.UTC), PageCommitted: true,
		Deliveries: []mailDeliveryReconcileItem{{DeliveryID: "mail-delivery-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Phase: maildelivery.PhaseRuntimeNotified, Outcome: mailDeliveryReconcileCommitted}},
	}
	var stdout, stderr bytes.Buffer
	if code := writeMailDeliveryReconcileReport(&stdout, &stderr, report); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var decoded mailDeliveryReconcileReport
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil || decoded.SchemaVersion != report.SchemaVersion || len(decoded.Deliveries) != 1 {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	for _, forbidden := range []string{"subject", "body", "thread", "message_text"} {
		if bytes.Contains(stdout.Bytes(), []byte(forbidden)) {
			t.Fatalf("output contains %q: %s", forbidden, stdout.String())
		}
	}
}

func TestMailDeliveryFenceResolverForSeatUsesExactConfiguredNamedSession(t *testing.T) {
	ctx := context.Background()
	cityPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityPath, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, ".gc", "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Agents:        []config.Agent{{Name: "reviewer"}},
		NamedSessions: []config.NamedSession{{Name: "reviewer", Template: "reviewer", Scope: "city"}},
	}
	backing := beads.NewMemStore()
	provider := runtime.NewFake()
	mgr := newSessionManagerWithConfig(cityPath, backing, provider, cfg)
	info, err := mgr.CreateSession(ctx, session.CreateOptions{
		Alias: "reviewer", ExplicitName: config.NamedSessionRuntimeName("test-city", cfg.Workspace, "reviewer"),
		Template: "reviewer", Title: "Reviewer", Command: "claude", WorkDir: t.TempDir(), Provider: "exec", Transport: "exec",
		ExtraMeta: map[string]string{session.NamedSessionIdentityMetadata: "reviewer"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	resolver, err := mailDeliveryFenceResolverForSeat(sessionFrontDoor(backing), cfg, "test-city",
		"seat:test-city/reviewer", strings.Repeat("a", 64))
	if err != nil || resolver.sessionRef != info.ID || resolver.authorityErr != nil {
		t.Fatalf("resolver=%#v err=%v; session=%#v", resolver, err, info)
	}
	fence, err := resolver.ResolveMailActivationFence(ctx, "seat:test-city/reviewer")
	if err != nil || fence.SessionRef != info.ID || fence.SeatRef != "seat:test-city/reviewer" || fence.CityRef != "city:test-city" {
		t.Fatalf("fence=%#v err=%v", fence, err)
	}
}

func TestMailDeliveryFenceResolverForSeatKeepsAbsentProjectionTypedUnavailable(t *testing.T) {
	cfg := &config.City{
		Workspace:     config.Workspace{Name: "test-city"},
		Agents:        []config.Agent{{Name: "reviewer"}},
		NamedSessions: []config.NamedSession{{Name: "reviewer", Template: "reviewer", Scope: "city"}},
	}
	resolver, err := mailDeliveryFenceResolverForSeat(sessionFrontDoor(beads.NewMemStore()), cfg, "test-city",
		"seat:test-city/reviewer", strings.Repeat("a", 64))
	if err != nil || resolver.sessionRef != "" || !errors.Is(resolver.authorityErr, maildelivery.ErrAuthorityUnavailable) {
		t.Fatalf("resolver=%#v err=%v", resolver, err)
	}
	if _, resolveErr := resolver.ResolveMailActivationFence(context.Background(), "seat:test-city/reviewer"); !errors.Is(resolveErr, maildelivery.ErrAuthorityUnavailable) {
		t.Fatalf("ResolveMailActivationFence = %v", resolveErr)
	}
	if _, err := mailDeliveryFenceResolverForSeat(sessionFrontDoor(beads.NewMemStore()), cfg, "test-city",
		"seat:other-city/reviewer", strings.Repeat("a", 64)); err == nil {
		t.Fatal("cross-city seat accepted")
	}
}

func TestMailDeliveryReceiptHandleRejectsRuntimeOnlyBeforeInvocation(t *testing.T) {
	var runtimeOnly worker.Handle = (*worker.RuntimeHandle)(nil)
	if err := requireExactMailDeliveryReceiptHandle(runtimeOnly); err == nil {
		t.Fatal("runtime-only handle accepted")
	}
	if err := requireExactMailDeliveryReceiptHandle(nil); err == nil {
		t.Fatal("nil handle accepted")
	}
	var exact worker.Handle = &worker.SessionHandle{}
	if err := requireExactMailDeliveryReceiptHandle(exact); err == nil {
		t.Fatal("receipt-incapable session handle accepted")
	}
	stableProvider := runtime.NewFake()
	sessionStore := beads.NewMemStore()
	mgr := session.NewManagerWithOptions(sessionStore, stableProvider)
	info, err := mgr.CreateSession(context.Background(), session.CreateOptions{BeadOnly: true, Template: "reviewer", Title: "Reviewer", Command: "true", WorkDir: t.TempDir(), Provider: "exec", Transport: "exec"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	capable, err := worker.NewSessionHandle(worker.SessionHandleConfig{Manager: mgr, Session: worker.SessionSpec{ID: info.ID, Provider: "exec", Transport: "exec"}})
	if err != nil {
		t.Fatalf("NewSessionHandle: %v", err)
	}
	if err := requireExactMailDeliveryReceiptHandle(capable); err != nil {
		t.Fatalf("stable-nudge-capable session rejected: %v", err)
	}
}

func TestExecuteMailDeliveryAttemptCommitsTypedProviderAcceptanceOnce(t *testing.T) {
	store, attempt, resolver := testMailDeliveryAttempt(t)
	calls := 0
	invoke := func(_ context.Context, invoking maildelivery.TransportAttempt) (maildelivery.TransportReceipt, error) {
		calls++
		return maildelivery.TransportReceipt{
			Version: 1, AttemptID: invoking.AttemptID, NudgeID: invoking.NudgeID,
			CommitBoundary: maildelivery.TransportCommitBoundaryProviderReturn,
			State:          maildelivery.EffectCommitted,
			ReceiptRef:     "provider-acceptance:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			ReceiptSHA256:  "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			RecordedAt:     time.Date(2026, 8, 13, 23, 42, 0, 0, time.UTC),
		}, nil
	}
	first, err := executeMailDeliveryAttempt(context.Background(), store, attempt, resolver,
		time.Date(2026, 8, 13, 23, 43, 0, 0, time.UTC), invoke)
	if err != nil || first.State != maildelivery.TransportCommitted || calls != 1 {
		t.Fatalf("first execution = %#v, %v, calls=%d", first, err, calls)
	}
	replay, err := executeMailDeliveryAttempt(context.Background(), store, attempt, resolver,
		time.Date(2026, 8, 13, 23, 44, 0, 0, time.UTC), invoke)
	if err != nil || replay.AttemptID != first.AttemptID || calls != 1 {
		t.Fatalf("replay = %#v, %v, calls=%d", replay, err, calls)
	}

	var stdout, stderr bytes.Buffer
	if code := writeMailDeliveryAttempt(&stdout, &stderr, "status", replay); code != 0 {
		t.Fatalf("writeMailDeliveryAttempt code=%d stderr=%q", code, stderr.String())
	}
	var output struct {
		SchemaVersion string                        `json:"schema_version"`
		Attempt       maildelivery.TransportAttempt `json:"attempt"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if output.SchemaVersion != "mail-delivery-attempt/v1" || output.Attempt.State != maildelivery.TransportCommitted {
		t.Fatalf("output = %#v", output)
	}
	for _, forbidden := range []string{"msg-cli", "subject", "body", "text"} {
		if bytes.Contains(stdout.Bytes(), []byte(forbidden)) {
			t.Fatalf("status output contains forbidden content marker %q: %s", forbidden, stdout.String())
		}
	}
}

func TestMailDeliveryWorkerReceiptLookupMapsExactDestinationEvidence(t *testing.T) {
	ctx := context.Background()
	cityPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityPath, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, ".gc", "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.City{Workspace: config.Workspace{Name: "test-city"}}
	sessBacking := beads.NewMemStore()
	provider := runtime.NewFake()
	mgr := newSessionManagerWithConfig(cityPath, sessBacking, provider, cfg)
	info, err := mgr.CreateSession(ctx, session.CreateOptions{
		Alias: "reviewer", ExplicitName: "mail-reviewer", Template: "reviewer", Title: "Reviewer",
		Command: "claude", WorkDir: t.TempDir(), Provider: "exec", Transport: "exec",
		ExtraMeta: map[string]string{session.NamedSessionIdentityMetadata: "reviewer"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	resolver := &mailDeliveryFenceResolver{
		store: sessionFrontDoor(sessBacking), sessionRef: info.ID,
		options: session.MailActivationFenceOptions{
			CityRef: "city:test-city", SeatRef: "seat:test-city/reviewer",
			ConfigSHA256: strings.Repeat("a", 64), IssuedByRef: "controller:test-city/mail-delivery-cli",
		},
	}
	fence, err := resolver.ResolveMailActivationFence(ctx, "")
	if err != nil {
		t.Fatalf("ResolveMailActivationFence: %v", err)
	}
	deliveryBacking := beads.NewMemStore()
	deliveryBacking.HonorExplicitIDs = true
	deliveryStore := maildelivery.NewStore(deliveryBacking)
	delivery, err := maildelivery.NewDelivery(
		"city:test-city/messaging", "msg-lookup", 1, fence.SeatRef,
		maildelivery.PolicyNotifyOnly, maildelivery.AttentionImmediate,
		time.Date(2026, 8, 14, 2, 10, 0, 0, time.UTC), nil, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err = deliveryStore.Create(delivery)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err = deliveryStore.Advance(delivery.ID, delivery.Revision, maildelivery.PhaseWaitingForActivation)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := deliveryStore.CreateTransportAttempt(ctx, maildelivery.TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 14, 2, 11, 0, 0, time.UTC),
	}, resolver)
	if err != nil {
		t.Fatal(err)
	}
	lookup := mailDeliveryWorkerReceiptLookup(cityPath, cfg, sessBacking, provider, resolver)
	unknown, err := lookup(ctx, attempt)
	if err != nil || unknown.State != maildelivery.EffectUnknownExternalState || unknown.RecordedAt.IsZero() {
		t.Fatalf("unknown lookup = %#v, %v", unknown, err)
	}
	want, err := provider.NudgeStable(ctx, info.SessionName, attempt.NudgeID, runtime.TextContent(mailDeliveryNudgeText(1)))
	if err != nil {
		t.Fatal(err)
	}
	committed, err := lookup(ctx, attempt)
	if err != nil || committed.State != maildelivery.EffectCommitted ||
		committed.CommitBoundary != maildelivery.TransportCommitBoundaryDestinationAtomic ||
		committed.ReceiptRef != "destination:"+want.DestinationRef ||
		committed.ReceiptSHA256 != want.ReceiptSHA256 || !committed.RecordedAt.Equal(want.AcceptedAt) {
		t.Fatalf("committed lookup = %#v, %v; want %#v", committed, err, want)
	}
}
