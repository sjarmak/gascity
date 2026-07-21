package temporalmaintenance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// recordingRunner counts real executions and can be told to fail. It stands in
// for gc/gh/git so the at-most-once guarantee is tested without external deps.
type recordingRunner struct {
	mu    sync.Mutex
	calls []string // idempotency keys, in call order
	runs  int64
	fail  bool
}

func (r *recordingRunner) Run(_ context.Context, m ProposedMutation) (string, error) {
	atomic.AddInt64(&r.runs, 1)
	r.mu.Lock()
	r.calls = append(r.calls, m.IdempotencyKey)
	r.mu.Unlock()
	if r.fail {
		return "", errors.New("boom")
	}
	return "ref:" + m.Target, nil
}

func (r *recordingRunner) count() int { return int(atomic.LoadInt64(&r.runs)) }

func armed(t *testing.T, dir string, runner CommandRunner) *RealAdapter {
	t.Helper()
	store, err := NewKeyStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return NewArmedRealAdapter(store, runner)
}

// A duplicate Propose (Activity retry / duplicate delivery) executes the command
// exactly once and reports created=false the second time.
func TestRealAdapter_DuplicateProposeRunsOnce(t *testing.T) {
	rr := &recordingRunner{}
	a := armed(t, t.TempDir(), rr)

	m := mut("dup")
	rec1, created1, err := a.Propose(context.Background(), m)
	if err != nil || !created1 {
		t.Fatalf("first Propose = (created=%v, err=%v), want created=true", created1, err)
	}
	if rec1.IdempotencyKey != "dup" {
		t.Fatalf("first Propose returned key %q", rec1.IdempotencyKey)
	}
	_, created2, err := a.Propose(context.Background(), m)
	if err != nil {
		t.Fatalf("second Propose err = %v", err)
	}
	if created2 {
		t.Fatalf("second Propose created=true; the command must run at most once")
	}
	if rr.count() != 1 {
		t.Fatalf("runner executed %d times, want exactly 1", rr.count())
	}
}

// The pivotal P2 property: a worker that crashes after claiming (but whose
// successor shares the persisted store) never re-executes the mutation. Two
// distinct adapters over the same dir, one runner instance, exactly one run.
func TestRealAdapter_SingleExecutionAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	rr := &recordingRunner{}

	// worker1 executes the mutation, then "crashes" (we drop the adapter).
	a1 := armed(t, dir, rr)
	if _, created, err := a1.Propose(context.Background(), mut("gated")); err != nil || !created {
		t.Fatalf("worker1 Propose = (created=%v, err=%v)", created, err)
	}

	// worker2: fresh adapter, same persisted store, same key (Activity retry).
	a2 := armed(t, dir, rr)
	_, created, err := a2.Propose(context.Background(), mut("gated"))
	if err != nil {
		t.Fatalf("worker2 Propose err = %v", err)
	}
	if created {
		t.Fatalf("worker2 re-executed the gated mutation across the restart")
	}
	if rr.count() != 1 {
		t.Fatalf("mutation executed %d times across restart, want exactly 1", rr.count())
	}
}

// A failed command is terminal: it is never re-run, and a re-Propose surfaces a
// TerminalExecError rather than silently reporting success.
func TestRealAdapter_FailedIsTerminal(t *testing.T) {
	dir := t.TempDir()
	rr := &recordingRunner{fail: true}
	a := armed(t, dir, rr)

	if _, created, err := a.Propose(context.Background(), mut("bad")); err == nil || created {
		t.Fatalf("first Propose of a failing command = (created=%v, err=%v), want error", created, err)
	}
	_, created, err := a.Propose(context.Background(), mut("bad"))
	if created {
		t.Fatalf("a failed key must not re-execute")
	}
	var te *TerminalExecError
	if !errors.As(err, &te) {
		t.Fatalf("re-Propose of failed key err = %v, want TerminalExecError", err)
	}
	if rr.count() != 1 {
		t.Fatalf("failed command ran %d times, want exactly 1 (no retry re-exec)", rr.count())
	}
}

// Recorded reflects the persisted store, so a fresh adapter over an existing
// store still reports prior mutations.
func TestRealAdapter_RecordedFromStore(t *testing.T) {
	dir := t.TempDir()
	rr := &recordingRunner{}
	a := armed(t, dir, rr)
	for i := 0; i < 3; i++ {
		if _, _, err := a.Propose(context.Background(), mut(fmt.Sprintf("k%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(armed(t, dir, rr).Recorded()); got != 3 {
		t.Fatalf("Recorded() from fresh adapter = %d, want 3", got)
	}
}
