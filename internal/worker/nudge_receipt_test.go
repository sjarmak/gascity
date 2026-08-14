package worker

import (
	"testing"
	"time"
)

func TestNudgeAcceptanceReceiptRejectsIdentityAndDigestDrift(t *testing.T) {
	receipt, err := newNudgeAcceptanceReceipt(
		"mail-nudge-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"gc-session-test", "goal-5-temporal", "codex", "tmux",
		time.Date(2026, 8, 13, 23, 45, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("newNudgeAcceptanceReceipt: %v", err)
	}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for name, mutate := range map[string]func(*NudgeAcceptanceReceipt){
		"effect": func(r *NudgeAcceptanceReceipt) {
			r.EffectID = "mail-nudge-baaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		"session":     func(r *NudgeAcceptanceReceipt) { r.TargetSessionRef = "other-session" },
		"runtime":     func(r *NudgeAcceptanceReceipt) { r.TargetRuntimeName = "other-runtime" },
		"provider":    func(r *NudgeAcceptanceReceipt) { r.Provider = "other-provider" },
		"transport":   func(r *NudgeAcceptanceReceipt) { r.Transport = "other-transport" },
		"boundary":    func(r *NudgeAcceptanceReceipt) { r.CommitBoundary = "agent-consumed" },
		"accepted_at": func(r *NudgeAcceptanceReceipt) { r.AcceptedAt = r.AcceptedAt.Add(time.Second) },
		"digest": func(r *NudgeAcceptanceReceipt) {
			r.ReceiptSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := *receipt
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate succeeded after mutation")
			}
		})
	}
}
