package maildelivery

import (
	"strings"
	"testing"
	"time"
)

func validFence() ActivationFence {
	return ActivationFence{
		Version:               1,
		FenceID:               "mail-activation-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CityRef:               "city:test-city",
		SeatRef:               "seat:test-city/reviewer",
		AuthorityKind:         AuthorityNamedSessionControllerV1,
		AuthorityRef:          "mail-session-fence:test-city/session-1@4",
		AuthorityGeneration:   4,
		AuthorityIntentSHA256: strings.Repeat("b", 64),
		SessionRef:            "session-1",
		ContinuationEpoch:     7,
		InstanceTokenSHA256:   strings.Repeat("c", 64),
		IssuedByRef:           "controller:test-city/session-reconciler",
		IssuedAt:              time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC),
	}
}

func TestDeliveryIDIsDeterministicAndDomainSeparated(t *testing.T) {
	got, err := DeliveryID("city:test-city/messaging", "msg-1", "seat:test-city/reviewer")
	if err != nil {
		t.Fatalf("DeliveryID: %v", err)
	}
	if got != "mail-delivery-46fabb544508bf5cc85f8d32e615e0ff5a47374f6a19af80113153e1696f55b2" {
		t.Fatalf("DeliveryID = %q", got)
	}
	changed, err := DeliveryID("city:test-city/messaging", "msg-2", "seat:test-city/reviewer")
	if err != nil {
		t.Fatalf("DeliveryID changed input: %v", err)
	}
	if changed == got {
		t.Fatal("message identity change reused delivery ID")
	}
}

func TestAttemptIDIgnoresIssuanceMetadata(t *testing.T) {
	fence := validFence()
	first, err := AttemptID("mail-delivery-"+strings.Repeat("d", 64), fence)
	if err != nil {
		t.Fatalf("AttemptID: %v", err)
	}

	reissued := fence
	reissued.IssuedByRef = "controller:test-city/restarted-reconciler"
	reissued.IssuedAt = reissued.IssuedAt.Add(time.Hour)
	second, err := AttemptID("mail-delivery-"+strings.Repeat("d", 64), reissued)
	if err != nil {
		t.Fatalf("AttemptID reissued: %v", err)
	}
	if second != first {
		t.Fatalf("equivalent authority minted a new attempt: %q != %q", second, first)
	}
}

func TestAttemptIDChangesWithStableAuthority(t *testing.T) {
	fence := validFence()
	first, err := AttemptID("mail-delivery-"+strings.Repeat("d", 64), fence)
	if err != nil {
		t.Fatalf("AttemptID: %v", err)
	}
	fence.ContinuationEpoch++
	second, err := AttemptID("mail-delivery-"+strings.Repeat("d", 64), fence)
	if err != nil {
		t.Fatalf("AttemptID changed authority: %v", err)
	}
	if second == first {
		t.Fatal("continuation epoch change reused attempt ID")
	}
}

func TestAttemptIDRejectsInvalidDeliveryAndAuthority(t *testing.T) {
	if _, err := AttemptID("caller-chosen", validFence()); err == nil {
		t.Fatal("AttemptID accepted a caller-chosen delivery ID")
	}
	invalidFence := validFence()
	invalidFence.AuthorityGeneration = 0
	if _, err := AttemptID("mail-delivery-"+strings.Repeat("d", 64), invalidFence); err == nil {
		t.Fatal("AttemptID accepted invalid controller authority")
	}
}

func TestActivationFenceValidationFailsClosed(t *testing.T) {
	tests := map[string]func(*ActivationFence){
		"zero version":        func(f *ActivationFence) { f.Version = 0 },
		"unknown authority":   func(f *ActivationFence) { f.AuthorityKind = "caller" },
		"missing intent hash": func(f *ActivationFence) { f.AuthorityIntentSHA256 = "" },
		"bad token hash":      func(f *ActivationFence) { f.InstanceTokenSHA256 = "not-a-hash" },
		"mismatched fence ID": func(f *ActivationFence) { f.FenceID = "mail-activation-" + strings.Repeat("e", 64) },
		"zero generation":     func(f *ActivationFence) { f.AuthorityGeneration = 0 },
		"zero epoch":          func(f *ActivationFence) { f.ContinuationEpoch = 0 },
		"non UTC issue time": func(f *ActivationFence) {
			f.IssuedAt = time.Date(2026, 8, 13, 16, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fence := validFence()
			mutate(&fence)
			if err := fence.Validate(); err == nil {
				t.Fatal("Validate succeeded")
			}
		})
	}
}

func TestDeliveryPhaseTransitionsAreClosed(t *testing.T) {
	legal := []struct{ from, to Phase }{
		{PhaseStored, PhaseWaitingForActivation},
		{PhaseWaitingForActivation, PhaseNotificationRequested},
		{PhaseNotificationRequested, PhaseRuntimeNotified},
		{PhaseRuntimeNotified, PhaseRead},
		{PhaseRead, PhaseDispositioned},
		{PhaseStored, PhaseDispositioned},
		{PhaseNotificationRequested, PhaseDispositioned},
	}
	for _, edge := range legal {
		if err := ValidateTransition(edge.from, edge.to); err != nil {
			t.Errorf("%s -> %s: %v", edge.from, edge.to, err)
		}
	}
	for _, edge := range []struct{ from, to Phase }{
		{PhaseStored, PhaseRuntimeNotified},
		{PhaseDispositioned, PhaseStored},
		{"invented", PhaseStored},
	} {
		if err := ValidateTransition(edge.from, edge.to); err == nil {
			t.Errorf("%s -> %s succeeded", edge.from, edge.to)
		}
	}
}
