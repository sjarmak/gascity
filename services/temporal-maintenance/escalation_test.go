package temporalmaintenance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// FileEscalator appends a durable, greppable JSONL record per escalation, so a
// dropped maintenance cycle leaves an artifact an operator (or a scanning order)
// can find — not a silent workflow failure.
func TestFileEscalator_AppendsDurableRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "escalations.jsonl")
	esc, err := NewFileEscalator(path)
	if err != nil {
		t.Fatalf("NewFileEscalator: %v", err)
	}

	claimedAt := time.Date(2026, 7, 16, 16, 43, 20, 0, time.UTC)
	e := Escalation{
		Key:       "temporal-shadow/repo/cyc/review/sling-selection/bead",
		Action:    ActionSelection,
		Target:    "bead",
		BeadRef:   "gc-4qz",
		Reason:    "poisoned pending after worker crash",
		ClaimedAt: claimedAt,
	}
	if err := esc.Escalate(context.Background(), e); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	// A second, distinct escalation appends rather than truncates.
	e2 := e
	e2.Key = "another/key"
	if err := esc.Escalate(context.Background(), e2); err != nil {
		t.Fatalf("Escalate#2: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read escalations: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("escalations file has %d lines, want 2", len(lines))
	}
	var got Escalation
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal line 0: %v", err)
	}
	if got.Key != e.Key || got.BeadRef != "gc-4qz" || got.Reason != e.Reason {
		t.Fatalf("record 0 = %+v, want key/beadref/reason from %+v", got, e)
	}
	if !got.ClaimedAt.Equal(claimedAt) {
		t.Fatalf("record 0 ClaimedAt = %v, want %v", got.ClaimedAt, claimedAt)
	}
	if got.EscalatedAt.IsZero() {
		t.Fatalf("record 0 has no EscalatedAt stamp")
	}
}

// A fresh FileEscalator over an existing file keeps prior records (append mode),
// so escalations accumulate across worker restarts.
func TestFileEscalator_AppendsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "escalations.jsonl")
	e := Escalation{Key: "k", Reason: "r"}

	esc1, err := NewFileEscalator(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := esc1.Escalate(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	esc2, err := NewFileEscalator(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := esc2.Escalate(context.Background(), e); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if n := len(strings.Split(strings.TrimSpace(string(data)), "\n")); n != 2 {
		t.Fatalf("reopened escalator produced %d lines, want 2 (append, not truncate)", n)
	}
}
