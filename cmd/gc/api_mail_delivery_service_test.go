package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/mail/beadmail"
	"github.com/gastownhall/gascity/internal/maildelivery"
	"github.com/gastownhall/gascity/internal/runtime"
)

type missingCanonicalDeliveryStore struct {
	beads.Store
	missingID string
}

func (s missingCanonicalDeliveryStore) Get(id string) (beads.Bead, error) {
	if id == s.missingID {
		return beads.Bead{}, beads.ErrNotFound
	}
	return s.Store.Get(id)
}

func TestControllerMailDeliveryInvokeRequiresCanonicalDeliveryBeforeProvider(t *testing.T) {
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	store := maildelivery.NewStore(backing)
	delivery, err := maildelivery.NewDelivery(
		"city:test-city/messaging", "gc-message-api", 1, "seat:test-city/reviewer",
		maildelivery.PolicyNotifyOnly, maildelivery.AttentionImmediate,
		time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC), nil, "",
	)
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
	resolver := fixedMailDeliveryFenceResolver{fence: testMailDeliveryFence()}
	attempt, err := store.CreateTransportAttempt(context.Background(), maildelivery.TransportAttemptRequest{
		DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
		ExpectedFenceID: resolver.fence.FenceID, CoveredDeliveryIDs: []string{delivery.ID},
		CreatedAt: time.Date(2026, 8, 14, 13, 1, 0, 0, time.UTC),
	}, resolver)
	if err != nil {
		t.Fatal(err)
	}

	cityPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[workspace]\nname = \"test-city\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := &controllerState{
		cfg: &config.City{Workspace: config.Workspace{Name: "test-city"}}, sp: runtime.NewFake(),
		cityBeadStore: missingCanonicalDeliveryStore{Store: backing, missingID: delivery.ID},
		cityName:      "test-city", cityPath: cityPath,
	}
	coordinator := &controllerMailDeliveryCoordinator{state: state}
	if got := state.MailDeliveryCoordinator(); got == nil {
		t.Fatal("controller state returned no mail delivery coordinator")
	}
	status, err := coordinator.MailDeliveryStatus(context.Background(), attempt.AttemptID)
	if err != nil || status.AttemptID != attempt.AttemptID {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	result, err := coordinator.InvokeMailDelivery(context.Background(), attempt.AttemptID)
	if result.AttemptID != attempt.AttemptID || !errors.Is(err, beads.ErrNotFound) || !strings.Contains(err.Error(), "canonical delivery") {
		t.Fatalf("invoke result=%#v err=%v", result, err)
	}
}

func TestControllerMailDeliveryDurableSendAndAuthorityWaitUseCanonicalService(t *testing.T) {
	const cityName = "test-city"
	cityPath := t.TempDir()
	writeMailDeliveryBinaryCity(t, cityPath, cityName, "reviewer", filepath.Join(t.TempDir(), "unused-adapter"))
	cfg, err := config.Load(fsys.OSFS{}, filepath.Join(cityPath, "city.toml"))
	if err != nil {
		t.Fatal(err)
	}
	backing := beads.NewMemStore()
	backing.HonorExplicitIDs = true
	state := &controllerState{
		cfg: cfg, sp: runtime.NewFake(), cityBeadStore: backing, cityMailProv: beadmail.New(backing),
		cityName: cityName, cityPath: cityPath,
	}
	coordinator := &controllerMailDeliveryCoordinator{state: state}
	command := api.DurableMailCommand{
		StableKey: "api-service-canary", Recipient: "reviewer", SenderCandidates: []string{"human"},
		Subject: "subject", Body: "body",
	}
	created, err := coordinator.SendDurableMail(context.Background(), command)
	if err != nil || created.Outcome != beadmail.DurableSendCreated {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	replay, err := coordinator.SendDurableMail(context.Background(), command)
	if err != nil || replay.Outcome != beadmail.DurableSendExactReplay || replay.Message.ID != created.Message.ID || replay.Delivery.ID != created.Delivery.ID {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	report, err := coordinator.ReconcileMailDeliverySeat(context.Background(), "seat:test-city/reviewer", 1, created.Delivery.ID)
	if err != nil || !report.PageCommitted || len(report.Deliveries) != 1 || report.Deliveries[0].Outcome != maildelivery.ReconcileWaitingForAuthority {
		t.Fatalf("report=%#v err=%v", report, err)
	}
	if _, err := coordinator.ReconcileMailDeliverySeat(context.Background(), "seat:test-city/reviewer", 0, ""); err == nil {
		t.Fatal("reconcile accepted invalid limit")
	}
	if _, err := coordinator.SendDurableMail(context.Background(), api.DurableMailCommand{
		StableKey: "bad-recipient", Recipient: "missing", SenderCandidates: []string{"human"}, Body: "body",
	}); err == nil {
		t.Fatal("durable send accepted an unconfigured recipient")
	}
	if _, err := coordinator.SendDurableMail(context.Background(), api.DurableMailCommand{
		StableKey: "bad-sender", Recipient: "reviewer", SenderCandidates: []string{"missing"}, Body: "body",
	}); err == nil {
		t.Fatal("durable send accepted an unresolved sender")
	}
	if _, err := coordinator.InvokeMailDelivery(context.Background(), "mail-attempt-missing"); !errors.Is(err, beads.ErrNotFound) {
		t.Fatalf("missing attempt error=%v", err)
	}
	snapshot, err := coordinator.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	state.sp = nil
	snapshot.provider = nil
	if _, _, _, err := coordinator.deliveryRuntime(snapshot, "seat:test-city/reviewer", "session-missing"); err == nil {
		t.Fatal("delivery runtime accepted missing provider")
	}
	state.sp = runtime.NewFake()
	snapshot.provider = state.sp
	if _, _, execute, err := coordinator.deliveryRuntime(snapshot, "seat:test-city/reviewer", "session-present"); err != nil || execute == nil {
		t.Fatalf("delivery runtime execute=%v err=%v", execute != nil, err)
	}
	state.cityMailProv = nil
	if _, err := coordinator.SendDurableMail(context.Background(), command); err == nil {
		t.Fatal("durable send accepted provider without stable support")
	}

	var nilState *controllerState
	if nilState.MailDeliveryCoordinator() != nil {
		t.Fatal("nil controller state returned a coordinator")
	}
	if _, err := (&controllerMailDeliveryCoordinator{}).snapshot(); err == nil {
		t.Fatal("nil coordinator state produced a snapshot")
	}
}
