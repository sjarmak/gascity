package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/maildelivery"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

type fixedMailDeliveryFenceResolver struct {
	fence maildelivery.ActivationFence
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
	for _, args := range [][]string{{"delivery"}, {"delivery", "status"}, {"delivery", "invoke"}} {
		found, _, err := cmd.Find(args)
		if err != nil || found == nil {
			t.Fatalf("Find(%v) = %#v, %v", args, found, err)
		}
	}
	for _, name := range []string{"status", "invoke"} {
		child, _, err := cmd.Find([]string{"delivery", name})
		if err != nil || child.Short == "" {
			t.Fatalf("%s Short = %q, %v", name, child.Short, err)
		}
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
	mgr := session.NewManagerWithOptions(beads.NewMemStore(), stableProvider)
	capable, err := worker.NewSessionHandle(worker.SessionHandleConfig{Manager: mgr, Session: worker.SessionSpec{ID: "gc-session-test", Provider: "exec", Transport: "exec"}})
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
		time.Date(2026, 8, 13, 23, 43, 0, 0, time.UTC), nil, invoke)
	if err != nil || first.State != maildelivery.TransportCommitted || calls != 1 {
		t.Fatalf("first execution = %#v, %v, calls=%d", first, err, calls)
	}
	replay, err := executeMailDeliveryAttempt(context.Background(), store, attempt, resolver,
		time.Date(2026, 8, 13, 23, 44, 0, 0, time.UTC), nil, invoke)
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
