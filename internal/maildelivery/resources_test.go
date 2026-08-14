package maildelivery

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validDelivery(t *testing.T) Delivery {
	t.Helper()
	id, err := DeliveryID("city:test-city/messaging", "msg-1", "seat:test-city/reviewer")
	if err != nil {
		t.Fatalf("DeliveryID: %v", err)
	}
	return Delivery{
		Version:         1,
		ID:              id,
		StoreRef:        "city:test-city/messaging",
		MessageID:       "msg-1",
		MessageRevision: 3,
		SeatRef:         "seat:test-city/reviewer",
		Policy:          PolicyNotifyOnly,
		Attention:       AttentionImmediate,
		Phase:           PhaseStored,
		Revision:        1,
		CreatedAt:       time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC),
	}
}

func TestNewDeliveryConstructsCanonicalResource(t *testing.T) {
	createdAt := time.Date(2026, 8, 13, 20, 0, 0, 0, time.UTC)
	delivery, err := NewDelivery("city:test-city/messaging", "msg-1", 3, "seat:test-city/reviewer", PolicyNotifyOnly, AttentionImmediate, createdAt, nil, "")
	if err != nil {
		t.Fatalf("NewDelivery: %v", err)
	}
	if delivery.ID == "" || delivery.CreatedAt != createdAt || delivery.Phase != PhaseStored || delivery.Revision != 1 {
		t.Fatalf("delivery = %#v", delivery)
	}
	if _, err := NewDelivery("bad\nstore", "msg-1", 3, "seat:test-city/reviewer", PolicyNotifyOnly, AttentionImmediate, createdAt, nil, ""); err == nil {
		t.Fatal("NewDelivery accepted malformed identity")
	}
}

func TestCanonicalResourcesMarshalSnakeCase(t *testing.T) {
	delivery := validDelivery(t)
	authority := ReceiptAuthorityFromFence(validFence(), delivery, time.Date(2026, 8, 13, 20, 1, 0, 0, time.UTC))
	payload, err := json.Marshal(ReadReceipt{Version: 1, ReceiptID: "mail-read-receipt-" + strings.Repeat("a", 64), Authority: authority})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	text := string(payload)
	for _, key := range []string{`"receipt_id"`, `"expected_delivery_revision"`, `"delivery_id"`, `"authority_generation"`, `"instance_token_sha256"`, `"observed_message_revision"`} {
		if !strings.Contains(text, key) {
			t.Fatalf("JSON %s lacks %s", text, key)
		}
	}
	if strings.Contains(text, "DeliveryID") || strings.Contains(text, "FenceID") {
		t.Fatalf("JSON leaked Go field names: %s", text)
	}
}

func TestDeliveryValidatePinsIdentityAndClosedEnums(t *testing.T) {
	delivery := validDelivery(t)
	if err := delivery.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	tests := map[string]func(*Delivery){
		"wrong identity":        func(d *Delivery) { d.ID = "mail-delivery-" + strings.Repeat("0", 64) },
		"zero message revision": func(d *Delivery) { d.MessageRevision = 0 },
		"unknown policy":        func(d *Delivery) { d.Policy = "maybe" },
		"unknown attention":     func(d *Delivery) { d.Attention = "eventually" },
		"unknown phase":         func(d *Delivery) { d.Phase = "invisible" },
		"non UTC created":       func(d *Delivery) { d.CreatedAt = d.CreatedAt.In(time.FixedZone("EDT", -4*60*60)) },
		"expiry before create": func(d *Delivery) {
			before := d.CreatedAt.Add(-time.Second)
			d.ExpiresAt = &before
			d.PolicySourceSHA256 = strings.Repeat("a", 64)
		},
		"expiry without source": func(d *Delivery) { after := d.CreatedAt.Add(time.Hour); d.ExpiresAt = &after },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := delivery
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate succeeded")
			}
		})
	}
}

func TestReceiptAuthorityRequiresExactCurrentFenceAndRevision(t *testing.T) {
	delivery := validDelivery(t)
	fence := validFence()
	authority := ReceiptAuthorityFromFence(fence, delivery, time.Date(2026, 8, 13, 20, 1, 0, 0, time.UTC))
	if err := authority.ValidateAgainst(fence, delivery); err != nil {
		t.Fatalf("ValidateAgainst: %v", err)
	}

	tests := map[string]func(*ReceiptAuthority){
		"foreign fence":    func(a *ReceiptAuthority) { a.FenceID = "mail-activation-" + strings.Repeat("f", 64) },
		"stale generation": func(a *ReceiptAuthority) { a.AuthorityGeneration-- },
		"stale epoch":      func(a *ReceiptAuthority) { a.ContinuationEpoch-- },
		"stale instance":   func(a *ReceiptAuthority) { a.InstanceTokenSHA256 = strings.Repeat("0", 64) },
		"wrong message":    func(a *ReceiptAuthority) { a.ObservedMessageRevision++ },
		"wrong delivery":   func(a *ReceiptAuthority) { a.DeliveryID = "mail-delivery-" + strings.Repeat("0", 64) },
		"wrong seat":       func(a *ReceiptAuthority) { a.SeatRef = "seat:test-city/other" },
		"caller kind":      func(a *ReceiptAuthority) { a.AuthorityKind = "caller" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := authority
			mutate(&candidate)
			if err := candidate.ValidateAgainst(fence, delivery); err == nil {
				t.Fatal("ValidateAgainst succeeded")
			}
		})
	}
}

func TestTransportReceiptDistinguishesCommittedFromUnknown(t *testing.T) {
	delivery := validDelivery(t)
	fence := validFence()
	attemptID, err := AttemptID(delivery.ID, fence)
	if err != nil {
		t.Fatalf("AttemptID: %v", err)
	}
	nudgeID, err := NudgeID(attemptID, []string{delivery.ID})
	if err != nil {
		t.Fatalf("NudgeID: %v", err)
	}

	committed := TransportReceipt{
		Version: 1, AttemptID: attemptID, NudgeID: nudgeID, State: EffectCommitted,
		CommitBoundary: TransportCommitBoundaryDestinationAtomic,
		ReceiptRef:     "nudge-receipt:test-city/1", ReceiptSHA256: strings.Repeat("e", 64),
		RecordedAt: time.Date(2026, 8, 13, 20, 2, 0, 0, time.UTC),
	}
	if err := committed.Validate(attemptID, nudgeID); err != nil {
		t.Fatalf("committed Validate: %v", err)
	}

	unknown := committed
	unknown.State = EffectUnknownExternalState
	unknown.CommitBoundary = ""
	unknown.ReceiptRef = ""
	unknown.ReceiptSHA256 = ""
	if err := unknown.Validate(attemptID, nudgeID); err != nil {
		t.Fatalf("unknown Validate: %v", err)
	}

	for name, mutate := range map[string]func(*TransportReceipt){
		"committed without receipt":       func(r *TransportReceipt) { r.ReceiptRef = "" },
		"unknown with flattering receipt": func(r *TransportReceipt) { r.State = EffectUnknownExternalState },
		"wrong nudge":                     func(r *TransportReceipt) { r.NudgeID = "mail-nudge-" + strings.Repeat("0", 64) },
		"unknown commit boundary":         func(r *TransportReceipt) { r.CommitBoundary = "invented" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := committed
			mutate(&candidate)
			if err := candidate.Validate(attemptID, nudgeID); err == nil {
				t.Fatal("Validate succeeded")
			}
		})
	}
}

func TestDispositionGuardMatchesPolicyAndAuthority(t *testing.T) {
	delivery := validDelivery(t)
	if err := ValidateDisposition(delivery, DispositionPolicySatisfiedNotified, DispositionProof{TransportCommitted: true}); err != nil {
		t.Fatalf("notify disposition: %v", err)
	}
	if err := ValidateDisposition(delivery, DispositionPolicySatisfiedRead, DispositionProof{ReadReceipt: true}); err == nil {
		t.Fatal("notify-only delivery accepted read disposition")
	}

	delivery.Policy = PolicyReadRequired
	if err := ValidateDisposition(delivery, DispositionPolicySatisfiedRead, DispositionProof{ReadReceipt: true}); err != nil {
		t.Fatalf("read disposition: %v", err)
	}
	if err := ValidateDisposition(delivery, DispositionRecipientSeatRetired, DispositionProof{SeatRetirementAuthority: true}); err != nil {
		t.Fatalf("seat retired disposition: %v", err)
	}
	if err := ValidateDisposition(delivery, DispositionRecipientSeatRetired, DispositionProof{}); err == nil {
		t.Fatal("seat retirement without controller fact succeeded")
	}
}

func TestDispositionAuthorityMatrixCoversEveryClosedReason(t *testing.T) {
	tests := []struct {
		policy Policy
		reason DispositionReason
		proof  DispositionProof
	}{
		{PolicyNotifyOnly, DispositionPolicySatisfiedNotified, DispositionProof{TransportCommitted: true}},
		{PolicyReadRequired, DispositionPolicySatisfiedRead, DispositionProof{ReadReceipt: true}},
		{PolicyResponseRequired, DispositionPolicySatisfiedResponse, DispositionProof{ResponseReceipt: true}},
		{PolicyNotifyOnly, DispositionRecipientDeclined, DispositionProof{RecipientAuthority: true}},
		{PolicyNotifyOnly, DispositionSenderCanceled, DispositionProof{SenderAuthority: true}},
		{PolicyNotifyOnly, DispositionSuperseded, DispositionProof{ReplacementRelation: true}},
		{PolicyNotifyOnly, DispositionExpired, DispositionProof{ExpiryAuthority: true}},
		{PolicyNotifyOnly, DispositionRecipientSeatRetired, DispositionProof{SeatRetirementAuthority: true}},
	}
	for _, test := range tests {
		delivery := validDelivery(t)
		delivery.Policy = test.policy
		if err := ValidateDisposition(delivery, test.reason, test.proof); err != nil {
			t.Errorf("%s: %v", test.reason, err)
		}
		if err := ValidateDisposition(delivery, test.reason, DispositionProof{}); err == nil {
			t.Errorf("%s succeeded without authority", test.reason)
		}
	}
	delivery := validDelivery(t)
	if err := ValidateDisposition(delivery, "invented", DispositionProof{TransportCommitted: true}); err == nil {
		t.Fatal("invented disposition succeeded")
	}
}
