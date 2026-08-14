package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type providerWithoutStableNudge struct{ Provider }

type targetAwareStableFake struct{ *Fake }

func (f targetAwareStableFake) SupportsStableNudgeTarget(target string) bool {
	return target == "supported"
}

func TestSupportsStableNudgeTargetUsesExactCapabilitySurface(t *testing.T) {
	if SupportsStableNudgeTarget(providerWithoutStableNudge{Provider: NewFake()}, "supported") {
		t.Fatal("provider without stable interface reported support")
	}
	if !SupportsStableNudgeTarget(NewFake(), "supported") {
		t.Fatal("global stable provider lost support")
	}
	targeted := targetAwareStableFake{Fake: NewFake()}
	if !SupportsStableNudgeTarget(targeted, "supported") || SupportsStableNudgeTarget(targeted, "unsupported") {
		t.Fatal("target-aware capability ignored exact target")
	}
}

func TestFakeStableNudgeCommitBeforeResponseLossReplaysOneEffect(t *testing.T) {
	fake := NewFake()
	if err := fake.Start(context.Background(), "session-a", Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	effectID := "mail-nudge-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fake.LoseStableNudgeResponseOnce(effectID)
	content := TextContent("1 actionable mail delivery; run gc mail inbox")

	if _, err := fake.NudgeStable(context.Background(), "session-a", effectID, content); !errors.Is(err, ErrStableNudgeRetrySafe) {
		t.Fatalf("first NudgeStable error = %v, want ErrStableNudgeRetrySafe", err)
	}
	receipt, err := fake.NudgeStable(context.Background(), "session-a", effectID, content)
	if err != nil {
		t.Fatalf("retry NudgeStable: %v", err)
	}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("receipt.Validate: %v", err)
	}
	if receipt.EffectID != effectID || receipt.TargetRuntimeName != "session-a" || receipt.CommitBoundary != StableNudgeCommitBoundaryDestinationAtomic {
		t.Fatalf("receipt = %#v", receipt)
	}
	if got := fake.StableNudgeEffectCount(effectID); got != 1 {
		t.Fatalf("physical effect count = %d, want 1", got)
	}

	replay, err := fake.NudgeStable(context.Background(), "session-a", effectID, content)
	if err != nil || replay != receipt {
		t.Fatalf("replay = %#v, %v, want byte-stable %#v", replay, err, receipt)
	}
}

func TestFakeStableNudgeRejectsConflictingEffectIdentity(t *testing.T) {
	fake := NewFake()
	if err := fake.Start(context.Background(), "session-a", Config{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	effectID := "mail-nudge-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := fake.NudgeStable(context.Background(), "session-a", effectID, TextContent("first")); err != nil {
		t.Fatalf("first NudgeStable: %v", err)
	}
	if _, err := fake.NudgeStable(context.Background(), "session-a", effectID, TextContent("changed")); !errors.Is(err, ErrStableNudgeConflict) {
		t.Fatalf("conflicting NudgeStable error = %v, want ErrStableNudgeConflict", err)
	}
	if got := fake.StableNudgeEffectCount(effectID); got != 1 {
		t.Fatalf("physical effect count = %d, want 1", got)
	}
}

func TestStableNudgeReceiptRejectsDigestAndIdentityDrift(t *testing.T) {
	receipt, err := NewStableNudgeReceipt(
		"mail-nudge-cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"session-a", TextContent("notice"), "exec-ledger:receipt-1",
		time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewStableNudgeReceipt: %v", err)
	}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for name, mutate := range map[string]func(*StableNudgeReceipt){
		"effect": func(r *StableNudgeReceipt) {
			r.EffectID = "mail-nudge-dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
		},
		"target": func(r *StableNudgeReceipt) { r.TargetRuntimeName = "session-b" },
		"content": func(r *StableNudgeReceipt) {
			r.ContentSHA256 = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		},
		"digest": func(r *StableNudgeReceipt) {
			r.ReceiptSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := receipt
			mutate(&changed)
			if err := changed.Validate(); err == nil {
				t.Fatalf("Validate accepted mutated receipt %#v", changed)
			}
		})
	}
}

func TestStableNudgeReceiptRefsMatchMailDeliveryContract(t *testing.T) {
	effectID := "mail-nudge-" + strings.Repeat("a", 64)
	for _, invalid := range []string{"Session-A", strings.Repeat("a", 257)} {
		if _, err := NewStableNudgeReceipt(effectID, invalid, TextContent("notice"), "receipt:one", time.Now().UTC()); err == nil {
			t.Fatalf("NewStableNudgeReceipt accepted invalid target ref %q", invalid)
		}
		if _, err := NewStableNudgeReceipt(effectID, "session-a", TextContent("notice"), invalid, time.Now().UTC()); err == nil {
			t.Fatalf("NewStableNudgeReceipt accepted invalid destination ref %q", invalid)
		}
	}
}

func TestStableNudgeLookupSeparatesMalformedShapeFromIdentityConflict(t *testing.T) {
	effectID := "mail-nudge-" + strings.Repeat("b", 64)
	content := TextContent("notice")
	contentHash, err := StableNudgeContentSHA256(content)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := NewStableNudgeReceipt(effectID, "session-a", content, "receipt:one", time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	committed := StableNudgeLookup{
		Version: 1, State: StableNudgeLookupCommitted, Receipt: receipt,
		ObservedAt: time.Date(2026, 8, 14, 1, 1, 0, 0, time.UTC),
	}
	if err := committed.ValidateShape(); err != nil {
		t.Fatalf("valid committed shape: %v", err)
	}
	if err := committed.Validate(effectID, "session-a", contentHash); err != nil {
		t.Fatalf("valid committed identity: %v", err)
	}
	if err := committed.Validate(effectID, "session-b", contentHash); err == nil {
		t.Fatal("valid receipt with different request identity was accepted")
	}

	unknown := StableNudgeLookup{Version: 1, State: StableNudgeLookupUnknownExternalState, ObservedAt: committed.ObservedAt}
	if err := unknown.Validate(effectID, "session-a", contentHash); err != nil {
		t.Fatalf("valid unknown lookup: %v", err)
	}
	unknown.Receipt = receipt
	if err := unknown.ValidateShape(); err == nil {
		t.Fatal("unknown lookup carrying a receipt was structurally accepted")
	}
	malformed := committed
	malformed.Receipt.TargetRuntimeName = "Session-A"
	if err := malformed.ValidateShape(); err == nil {
		t.Fatal("malformed receipt was structurally accepted")
	}
	malformed = committed
	malformed.State = "other"
	if err := malformed.ValidateShape(); err == nil {
		t.Fatal("unknown lookup state was accepted")
	}
}
