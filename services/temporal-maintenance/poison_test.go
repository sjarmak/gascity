package temporalmaintenance

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// fakeEscalator records escalations in memory and can be told to fail delivery.
type fakeEscalator struct {
	mu     sync.Mutex
	got    []Escalation
	failer error
}

func (f *fakeEscalator) Escalate(_ context.Context, e Escalation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failer != nil {
		return f.failer
	}
	f.got = append(f.got, e)
	return nil
}

func (f *fakeEscalator) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

// TestRealAdapter_PoisonedPendingQuarantinesAndEscalates is the fault-injection
// regression for the gc-4zf.4 / dr-3nya defect: a worker SIGKILLed between Claim
// and Complete/Fail leaves a pending record. On redelivery, the armed adapter
// must quarantine the poison, emit a durable escalation, and still refuse to
// re-run (at-most-once) — turning a silent dropped cycle into a loud, recorded
// one.
func TestRealAdapter_PoisonedPendingQuarantinesAndEscalates(t *testing.T) {
	dir := t.TempDir()
	store, err := NewKeyStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Inject the crash: claim the key (record now pending) and never finish —
	// exactly the on-disk state a SIGKILL mid-sling leaves.
	m := mut("temporal-shadow/repo/cyc/review/sling-selection/bead")
	m.Action = ActionSelection
	m.BeadRef = "gc-4qz"
	if _, claimed, err := store.Claim(m); err != nil || !claimed {
		t.Fatalf("inject crash: Claim = claimed=%v err=%v", claimed, err)
	}

	// Redelivery: a fresh armed adapter over the same store, with an escalator.
	rr := &recordingRunner{}
	esc := &fakeEscalator{}
	a := NewArmedRealAdapter(store, rr, WithEscalator(esc))

	_, created, err := a.Propose(context.Background(), m)
	if created {
		t.Fatalf("redelivery re-created/re-ran the poisoned key")
	}
	var te *TerminalExecError
	if !errors.As(err, &te) {
		t.Fatalf("redelivery err = %v, want TerminalExecError", err)
	}
	// At-most-once: the command is never run for a poisoned key.
	if rr.count() != 0 {
		t.Fatalf("runner executed %d times for a poisoned key, want 0", rr.count())
	}
	// The claim is quarantined to failed with a reason.
	rec, ok, err := store.Load(m.IdempotencyKey)
	if err != nil || !ok {
		t.Fatalf("load quarantined record: ok=%v err=%v", ok, err)
	}
	if rec.Status != ExecFailed {
		t.Fatalf("poisoned record status = %q, want failed (quarantined)", rec.Status)
	}
	if rec.Err == "" {
		t.Fatalf("quarantined record has no reason")
	}
	// A durable escalation carrying the orphan bead ref was emitted.
	if esc.count() != 1 {
		t.Fatalf("escalations = %d, want 1", esc.count())
	}
	if esc.got[0].BeadRef != "gc-4qz" {
		t.Fatalf("escalation BeadRef = %q, want gc-4qz (the orphan maintenance bead)", esc.got[0].BeadRef)
	}
	if esc.got[0].Key != m.IdempotencyKey {
		t.Fatalf("escalation Key = %q, want %q", esc.got[0].Key, m.IdempotencyKey)
	}
}

// A second redelivery of the same poisoned key does not double-escalate: the
// record is already quarantined, so escalation fires exactly once.
func TestRealAdapter_PoisonedPendingEscalatesOnce(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewKeyStore(dir)
	m := mut("poison")
	if _, _, err := store.Claim(m); err != nil {
		t.Fatal(err)
	}
	rr := &recordingRunner{}
	esc := &fakeEscalator{}
	a := NewArmedRealAdapter(store, rr, WithEscalator(esc))

	for i := 0; i < 3; i++ {
		if _, created, err := a.Propose(context.Background(), m); created || err == nil {
			t.Fatalf("redelivery %d = (created=%v, err=%v), want refused with error", i, created, err)
		}
	}
	if esc.count() != 1 {
		t.Fatalf("escalations = %d across 3 redeliveries, want exactly 1", esc.count())
	}
	if rr.count() != 0 {
		t.Fatalf("runner ran %d times, want 0", rr.count())
	}
}

// A genuine command failure (ExecFailed, not a crash) is a normal terminal
// outcome and must NOT escalate — escalation is only for poisoned pending claims.
func TestRealAdapter_FailedDoesNotEscalate(t *testing.T) {
	dir := t.TempDir()
	rr := &recordingRunner{fail: true}
	esc := &fakeEscalator{}
	store, _ := NewKeyStore(dir)
	a := NewArmedRealAdapter(store, rr, WithEscalator(esc))

	// First Propose runs and fails (record -> failed).
	if _, created, err := a.Propose(context.Background(), mut("bad")); created || err == nil {
		t.Fatalf("first Propose of failing command = (created=%v, err=%v)", created, err)
	}
	// Redelivery of the failed key surfaces terminal but does not escalate.
	if _, _, err := a.Propose(context.Background(), mut("bad")); err == nil {
		t.Fatalf("redelivery of failed key returned nil error")
	}
	if esc.count() != 0 {
		t.Fatalf("escalations = %d for a genuine command failure, want 0", esc.count())
	}
}

// Escalation delivery failure does not make the mutation retryable and does not
// lose the durable quarantine: the record is still failed, and the returned
// error stays a TerminalExecError so the workflow fails closed.
func TestRealAdapter_EscalateFailureStillQuarantines(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewKeyStore(dir)
	m := mut("poison")
	if _, _, err := store.Claim(m); err != nil {
		t.Fatal(err)
	}
	rr := &recordingRunner{}
	esc := &fakeEscalator{failer: errors.New("slack down")}
	a := NewArmedRealAdapter(store, rr, WithEscalator(esc))

	_, created, err := a.Propose(context.Background(), m)
	if created {
		t.Fatalf("created=true despite poison")
	}
	var te *TerminalExecError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want TerminalExecError even when escalation delivery fails", err)
	}
	rec, _, _ := store.Load(m.IdempotencyKey)
	if rec.Status != ExecFailed {
		t.Fatalf("record status = %q, want failed (quarantine persists despite escalation failure)", rec.Status)
	}
	if rr.count() != 0 {
		t.Fatalf("runner ran %d times, want 0", rr.count())
	}
}

// The adapter still works with no escalator bound (nil): it quarantines the
// poison and refuses to re-run, just without a notification.
func TestRealAdapter_PoisonedPendingNoEscalator(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewKeyStore(dir)
	m := mut("poison")
	if _, _, err := store.Claim(m); err != nil {
		t.Fatal(err)
	}
	rr := &recordingRunner{}
	a := NewArmedRealAdapter(store, rr) // no WithEscalator

	if _, created, err := a.Propose(context.Background(), m); created || err == nil {
		t.Fatalf("Propose = (created=%v, err=%v), want refused", created, err)
	}
	rec, _, _ := store.Load(m.IdempotencyKey)
	if rec.Status != ExecFailed {
		t.Fatalf("record status = %q, want failed (quarantined even with no escalator)", rec.Status)
	}
}
