package runtime

import (
	"context"
	"errors"
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
