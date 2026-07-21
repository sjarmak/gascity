package temporalmaintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Escalation is a durable notice that a maintenance side effect was dropped and
// cannot be safely recovered — a claim left pending by a crashed worker, which
// at-most-once forbids re-running. It carries enough to find the wreckage
// without re-reading the store: the poisoned idempotency key, the intended
// action/target, the maintenance bead the cycle created (which may be open with
// routed_to unset), and why it was quarantined.
type Escalation struct {
	Key         string    `json:"key"`
	Action      string    `json:"action,omitempty"`
	Target      string    `json:"target,omitempty"`
	BeadRef     string    `json:"bead_ref,omitempty"`
	Reason      string    `json:"reason"`
	ClaimedAt   time.Time `json:"claimed_at,omitempty"`
	EscalatedAt time.Time `json:"escalated_at"`
}

// Escalator delivers an Escalation to a human channel (the mayor /
// #gascity-maintenance). It is the seam the armed RealAdapter uses so the
// notification transport (a durable file, mail, slack) is injected, not
// hardcoded. A nil Escalator disables notification — the quarantine record in
// the KeyStore is the durable backstop either way.
type Escalator interface {
	Escalate(ctx context.Context, e Escalation) error
}

// FileEscalator appends one JSON line per escalation to a durable log, fsynced
// so a dropped cycle survives a crash as a greppable artifact an operator or a
// scanning order can pick up. It is the default durable transport until a live
// mayor/slack transport is wired at deploy.
type FileEscalator struct {
	mu   sync.Mutex
	path string
	now  func() time.Time // injectable for tests; defaults to time.Now
}

// NewFileEscalator roots an escalation log at path, creating parent dirs (0700).
func NewFileEscalator(path string) (*FileEscalator, error) {
	if path == "" {
		return nil, fmt.Errorf("FileEscalator: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("FileEscalator: create dir for %s: %w", path, err)
	}
	return &FileEscalator{path: path, now: time.Now}, nil
}

// Escalate appends e as one JSON line, stamping EscalatedAt, and fsyncs the file
// so the record is durable before returning.
func (f *FileEscalator) Escalate(_ context.Context, e Escalation) error {
	if e.EscalatedAt.IsZero() {
		e.EscalatedAt = f.now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("FileEscalator: marshal: %w", err)
	}
	line = append(line, '\n')

	f.mu.Lock()
	defer f.mu.Unlock()
	fh, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("FileEscalator: open %s: %w", f.path, err)
	}
	defer fh.Close()
	if _, err := fh.Write(line); err != nil {
		return fmt.Errorf("FileEscalator: append: %w", err)
	}
	if err := fh.Sync(); err != nil {
		return fmt.Errorf("FileEscalator: fsync: %w", err)
	}
	return nil
}
