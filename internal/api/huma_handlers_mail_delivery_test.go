package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api/apierr"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/mail"
	"github.com/gastownhall/gascity/internal/mail/beadmail"
	"github.com/gastownhall/gascity/internal/maildelivery"
)

type mailDeliveryCoordinatorFakeState struct {
	*fakeState
	coordinator *mailDeliveryCoordinatorFake
}

func (s *mailDeliveryCoordinatorFakeState) MailDeliveryCoordinator() MailDeliveryCoordinator {
	return s.coordinator
}

type mailDeliveryCoordinatorFake struct {
	durableResult  beadmail.DurableSendResult
	durableErr     error
	report         maildelivery.ReconcileReport
	reconcileErr   error
	attempt        maildelivery.TransportAttempt
	invokeErr      error
	statusErr      error
	durableCalls   int
	reconcileCalls int
	invokeCalls    int
}

func (f *mailDeliveryCoordinatorFake) SendDurableMail(context.Context, DurableMailCommand) (beadmail.DurableSendResult, error) {
	f.durableCalls++
	return f.durableResult, f.durableErr
}

func (f *mailDeliveryCoordinatorFake) ReconcileMailDeliverySeat(context.Context, string, int, string) (maildelivery.ReconcileReport, error) {
	f.reconcileCalls++
	return f.report, f.reconcileErr
}

func (f *mailDeliveryCoordinatorFake) InvokeMailDelivery(context.Context, string) (maildelivery.TransportAttempt, error) {
	f.invokeCalls++
	return f.attempt, f.invokeErr
}

func (f *mailDeliveryCoordinatorFake) MailDeliveryStatus(context.Context, string) (maildelivery.TransportAttempt, error) {
	return f.attempt, f.statusErr
}

// TestMailDeliveryControlPlaneRoundTrip exercises the generated client and
// supervisor handler through the owned in-process transport.
func TestMailDeliveryControlPlaneRoundTrip(t *testing.T) {
	observedAt := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	attempt := maildelivery.TransportAttempt{
		Version: 1, AttemptID: "mail-attempt-roundtrip", DeliveryID: "mail-delivery-roundtrip",
		State: maildelivery.TransportRequested, InvocationCount: 2, CreatedAt: observedAt,
	}
	report := maildelivery.ReconcileReport{
		SchemaVersion: "mail-delivery-reconcile/v1", SeatRef: "seat:test-city/worker",
		ObservedAt: observedAt, ActionRequired: true,
	}
	coordinator := &mailDeliveryCoordinatorFake{
		durableResult: beadmail.DurableSendResult{
			Message:  mail.Message{ID: "gc-roundtrip", From: "human", To: "worker"},
			Delivery: maildelivery.Delivery{ID: "mail-delivery-roundtrip"},
			Outcome:  beadmail.DurableSendExactReplay,
		},
		report: report, reconcileErr: errors.Join(maildelivery.ErrTransportRetrySafe, errors.New("after effect")),
		attempt: attempt, invokeErr: errors.Join(maildelivery.ErrTransportRetrySafe, errors.New("receipt read failed")),
	}
	state := &mailDeliveryCoordinatorFakeState{fakeState: newFakeState(t), coordinator: coordinator}
	handler := newTestCityHandler(t, state)
	client := newClientWithHTTPClient("http://localhost", state.CityName(), &http.Client{Transport: loopbackTransport{h: handler}})

	durable, err := client.SendDurableMail(DurableMailCommand{
		StableKey: "roundtrip", Recipient: "worker", SenderCandidates: []string{"human"}, Subject: "subject", Body: "notify",
	})
	if err != nil || durable.Outcome != beadmail.DurableSendExactReplay || coordinator.durableCalls != 1 {
		t.Fatalf("durable = %#v, err=%v, calls=%d", durable, err, coordinator.durableCalls)
	}
	coordinator.durableResult = beadmail.DurableSendResult{
		Message: mail.Message{ID: "gc-message-only", From: "human", To: "worker"},
		Outcome: beadmail.DurableSendCreated,
	}
	coordinator.durableErr = errors.New("delivery create failed after message commit")
	messageOnly, err := client.SendDurableMail(DurableMailCommand{
		StableKey: "message-only", Recipient: "worker", SenderCandidates: []string{"human"}, Body: "notify",
	})
	if messageOnly.Message.ID != "gc-message-only" || messageOnly.Delivery.ID != "" || err == nil || ShouldFallback(client, err) {
		t.Fatalf("message-only durable = %#v, err=%v fallback=%v", messageOnly, err, ShouldFallback(client, err))
	}

	gotReport, err := client.ReconcileMailDeliverySeat(report.SeatRef, 1, "mail-delivery-roundtrip")
	if !reflect.DeepEqual(gotReport, report) || err == nil || ShouldFallback(client, err) {
		t.Fatalf("reconcile report=%#v err=%v fallback=%v", gotReport, err, ShouldFallback(client, err))
	}
	gotAttempt, err := client.InvokeMailDelivery(attempt.AttemptID)
	if !reflect.DeepEqual(gotAttempt, attempt) || err == nil || ShouldFallback(client, err) {
		t.Fatalf("invoke attempt=%#v err=%v fallback=%v", gotAttempt, err, ShouldFallback(client, err))
	}
	if coordinator.reconcileCalls != 1 || coordinator.invokeCalls != 1 {
		t.Fatalf("effect calls: reconcile=%d invoke=%d", coordinator.reconcileCalls, coordinator.invokeCalls)
	}
	if !strings.Contains(err.Error(), "retry_safe") {
		t.Fatalf("invoke error = %v, want closed retry_safe code", err)
	}
	coordinator.invokeErr = errors.Join(ErrMailDeliveryNotFound, errors.New("canonical delivery missing"))
	gotAttempt, err = client.InvokeMailDelivery(attempt.AttemptID)
	if !reflect.DeepEqual(gotAttempt, attempt) || err == nil || !strings.Contains(err.Error(), "not_found") || ShouldFallback(client, err) {
		t.Fatalf("result-bearing not-found attempt=%#v err=%v fallback=%v", gotAttempt, err, ShouldFallback(client, err))
	}
	gotStatus, err := client.MailDeliveryStatus(attempt.AttemptID)
	if err != nil || !reflect.DeepEqual(gotStatus, attempt) {
		t.Fatalf("status attempt=%#v err=%v", gotStatus, err)
	}
}

func TestMailDeliveryHandlersFailClosedBeforeResultAndClassifyFailures(t *testing.T) {
	missing := &Server{state: newFakeState(t)}
	if _, err := missing.humaHandleMailDurableSend(context.Background(), &MailDurableSendInput{}); err == nil {
		t.Fatal("durable handler accepted missing coordinator")
	}

	baseErr := errors.New("infrastructure failed")
	coordinator := &mailDeliveryCoordinatorFake{
		durableErr: baseErr, reconcileErr: baseErr, invokeErr: baseErr, statusErr: baseErr,
	}
	state := &mailDeliveryCoordinatorFakeState{fakeState: newFakeState(t), coordinator: coordinator}
	server := &Server{state: state}
	if _, err := server.humaHandleMailDurableSend(context.Background(), &MailDurableSendInput{}); err == nil {
		t.Fatal("durable handler dropped pre-result failure")
	}
	if _, err := server.humaHandleMailDeliveryReconcile(context.Background(), &MailDeliveryReconcileInput{}); err == nil {
		t.Fatal("reconcile handler dropped pre-result failure")
	}
	if _, err := server.humaHandleMailDeliveryInvoke(context.Background(), &MailDeliveryAttemptInput{}); err == nil {
		t.Fatal("invoke handler dropped pre-result failure")
	}
	if _, err := server.humaHandleMailDeliveryStatus(context.Background(), &MailDeliveryAttemptInput{}); err == nil {
		t.Fatal("status handler dropped failure")
	}
	coordinator.durableResult = beadmail.DurableSendResult{
		Message:  mail.Message{ID: "gc-result-bearing", From: "human", To: "worker"},
		Delivery: maildelivery.Delivery{ID: "mail-delivery-result-bearing"},
		Outcome:  beadmail.DurableSendCreated,
	}
	output, err := server.humaHandleMailDurableSend(context.Background(), &MailDurableSendInput{})
	if err != nil || output == nil || output.Body.OK || output.Body.Result.Message.ID != "gc-result-bearing" {
		t.Fatalf("result-bearing durable output=%#v err=%v", output, err)
	}
	recorded := state.eventProv.(*events.Fake).Events
	if len(recorded) != 1 || recorded[0].Type != events.MailSent || recorded[0].Subject != "gc-result-bearing" {
		t.Fatalf("result-bearing durable events=%#v; want one authoritative MailSent", recorded)
	}

	for _, tc := range []struct {
		err  error
		code string
	}{
		{maildelivery.ErrTransportRetryEscalated, "retry_escalated"},
		{maildelivery.ErrTransportReceiptLookupRetryLater, "receipt_lookup_retry_later"},
		{maildelivery.ErrTransportRetrySafe, "retry_safe"},
		{maildelivery.ErrTransportInvocationInProgress, "invocation_in_progress"},
		{ErrMailDeliveryInvalid, "invalid"},
		{ErrMailDeliveryNotFound, "not_found"},
		{ErrMailDeliveryUnavailable, "unavailable"},
		{baseErr, "operation_failed"},
	} {
		if got := mailDeliveryFailureCode(tc.err); got != tc.code {
			t.Errorf("mailDeliveryFailureCode(%v)=%q, want %q", tc.err, got, tc.code)
		}
	}
	if mailDeliveryFailureCode(nil) != "" || mailDeliveryAPIError(maildelivery.ErrConflict) == nil || mailDeliveryAPIError(baseErr) == nil {
		t.Fatal("closed error classification failed")
	}
}

func TestMailDeliveryHTTPProblemsMapTypedCoordinatorErrorsWithoutDetailLeakage(t *testing.T) {
	const secret = "/private/operator/city.toml dsn=db.internal/city?sslmode=verify-full"
	coordinator := &mailDeliveryCoordinatorFake{}
	state := &mailDeliveryCoordinatorFakeState{fakeState: newFakeState(t), coordinator: coordinator}
	handler := newTestCityHandler(t, state)

	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{"invalid", errors.Join(ErrMailDeliveryInvalid, errors.New(secret)), http.StatusBadRequest, "invalid-request", "mail delivery request is invalid"},
		{"not found", errors.Join(ErrMailDeliveryNotFound, errors.New(secret)), http.StatusNotFound, "mail-delivery-not-found", "mail delivery resource was not found"},
		{"unavailable", errors.Join(ErrMailDeliveryUnavailable, errors.New(secret)), http.StatusServiceUnavailable, "service-unavailable", "mail delivery service is unavailable"},
		{"internal", errors.New("unexpected failure: " + secret), http.StatusInternalServerError, "internal", "mail delivery operation failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coordinator.statusErr = tc.err
			req := httptest.NewRequest(http.MethodGet, cityURL(state, "/mail/delivery/mail-attempt-test"), nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var problem apierr.ErrorModel
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode problem: %v; body=%s", err, rec.Body.String())
			}
			if problem.Code != tc.wantCode || problem.Detail != tc.wantDetail {
				t.Fatalf("problem=%#v", problem)
			}
			if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), "db.internal/city") {
				t.Fatalf("problem leaked coordinator detail: %s", rec.Body.String())
			}
		})
	}
}
