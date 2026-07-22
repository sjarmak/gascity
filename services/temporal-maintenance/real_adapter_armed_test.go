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

// preflightRunner adds a controllable Preflight to recordingRunner: it can
// skip, fail like a transient backend outage, and counts its checks.
type preflightRunner struct {
	recordingRunner
	skip      bool
	preErr    error
	prechecks int64
}

func (p *preflightRunner) Preflight(_ context.Context, _ ProposedMutation) (bool, error) {
	atomic.AddInt64(&p.prechecks, 1)
	if p.preErr != nil {
		return false, p.preErr
	}
	return p.skip, nil
}

// The gc-372.1 property: a transient failure in the pre-claim read stamps
// NOTHING — no terminal key, no pending claim — so a retry of the same Propose
// recovers and executes exactly once when the backend comes back.
func TestRealAdapter_TransientPreflightErrorRetriesCleanly(t *testing.T) {
	store, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pr := &preflightRunner{preErr: errors.New("dolt circuit breaker is open: server appears down (cooldown 5s)")}
	a := NewArmedRealAdapter(store, pr)
	m := mut("blip")

	_, created, err := a.Propose(context.Background(), m)
	if err == nil || created {
		t.Fatalf("Propose during outage = (created=%v, err=%v), want error", created, err)
	}
	var te *TerminalExecError
	if errors.As(err, &te) {
		t.Fatalf("preflight outage came back terminal (%v); it must stay retryable", err)
	}
	if _, ok, _ := store.Load("blip"); ok {
		t.Fatal("a failed preflight stamped the execstore; the key must stay untouched")
	}
	if pr.count() != 0 {
		t.Fatalf("mutation ran %d times during a failed preflight, want 0", pr.count())
	}

	// The backend recovers; the Activity retry re-enters the same Propose.
	pr.preErr = nil
	if _, created, err := a.Propose(context.Background(), m); err != nil || !created {
		t.Fatalf("retry after recovery = (created=%v, err=%v), want clean execution", created, err)
	}
	if pr.count() != 1 {
		t.Fatalf("mutation ran %d times across the blip, want exactly 1", pr.count())
	}
}

// A preflight skip is recorded durably as done/"skipped-inflight" without ever
// running the command, and a duplicate delivery reads the skip back from the
// store instead of re-checking live state.
func TestRealAdapter_PreflightSkipRecordedOnce(t *testing.T) {
	store, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pr := &preflightRunner{skip: true}
	a := NewArmedRealAdapter(store, pr)
	m := mut("half-open")

	rec1, created1, err := a.Propose(context.Background(), m)
	if err != nil || !created1 || rec1.Result != "skipped-inflight" {
		t.Fatalf("skip Propose = (result=%q, created=%v, err=%v), want recorded skip", rec1.Result, created1, err)
	}
	if pr.count() != 0 {
		t.Fatalf("a skipped mutation ran %d times, want 0", pr.count())
	}

	rec2, created2, err := a.Propose(context.Background(), m)
	if err != nil || created2 || rec2.Result != "skipped-inflight" {
		t.Fatalf("dup skip Propose = (result=%q, created=%v, err=%v), want the recorded skip", rec2.Result, created2, err)
	}
	if got := atomic.LoadInt64(&pr.prechecks); got != 1 {
		t.Fatalf("preflight ran %d times, want 1 — a duplicate answers from the store", got)
	}
}

// A PERMANENT preflight failure (validation/config defect) is recorded as a
// terminal failed key — never retried, never masked as a skip — preserving the
// old validate-inside-Run fail-closed behavior.
func TestRealAdapter_PermanentPreflightErrorIsTerminal(t *testing.T) {
	store, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pr := &preflightRunner{preErr: &PermanentPreflightError{Err: errors.New("params polecat and title are required")}}
	a := NewArmedRealAdapter(store, pr)
	m := mut("bad-config")

	if _, created, err := a.Propose(context.Background(), m); err == nil || created {
		t.Fatalf("Propose with permanent preflight error = (created=%v, err=%v), want error", created, err)
	}
	rec, ok, err := store.Load("bad-config")
	if err != nil || !ok || rec.Status != ExecFailed {
		t.Fatalf("record = (%+v, ok=%v, err=%v), want a terminal failed key", rec, ok, err)
	}
	_, created, err := a.Propose(context.Background(), m)
	if created {
		t.Fatal("a permanently-failed key must not re-execute")
	}
	var te *TerminalExecError
	if !errors.As(err, &te) {
		t.Fatalf("re-Propose err = %v, want TerminalExecError", err)
	}
	if pr.count() != 0 {
		t.Fatalf("mutation ran %d times despite a permanent config error, want 0", pr.count())
	}
}

// A passing preflight followed by a failing Run still fails closed: the run
// error stamps a terminal key through the new Load->preflight->Claim path and
// a re-Propose refuses to re-execute.
func TestRealAdapter_RunFailureAfterPreflightStaysTerminal(t *testing.T) {
	store, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pr := &preflightRunner{}
	pr.fail = true
	a := NewArmedRealAdapter(store, pr)
	m := mut("pre-ok-run-bad")

	if _, created, err := a.Propose(context.Background(), m); err == nil || created {
		t.Fatalf("Propose with failing run = (created=%v, err=%v), want error", created, err)
	}
	_, created, err := a.Propose(context.Background(), m)
	if created {
		t.Fatal("a failed key must not re-execute")
	}
	var te *TerminalExecError
	if !errors.As(err, &te) {
		t.Fatalf("re-Propose of failed key err = %v, want TerminalExecError", err)
	}
	if pr.count() != 1 {
		t.Fatalf("failing run executed %d times, want exactly 1", pr.count())
	}
}

// A duplicate delivery answers from the recorded outcome even while the
// backend behind the preflight is down — the store short-circuit runs first.
func TestRealAdapter_DuplicateAnswersWhilePreflightDown(t *testing.T) {
	store, err := NewKeyStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pr := &preflightRunner{}
	a := NewArmedRealAdapter(store, pr)
	m := mut("done-then-down")

	if _, created, err := a.Propose(context.Background(), m); err != nil || !created {
		t.Fatalf("first Propose = (created=%v, err=%v)", created, err)
	}

	pr.preErr = errors.New("dolt circuit breaker is open")
	rec, created, err := a.Propose(context.Background(), m)
	if err != nil || created {
		t.Fatalf("dup during outage = (created=%v, err=%v), want the recorded outcome", created, err)
	}
	if rec.Result != "ref:"+m.Target {
		t.Fatalf("dup Result = %q, want the recorded ref", rec.Result)
	}
	if pr.count() != 1 {
		t.Fatalf("mutation ran %d times, want exactly 1", pr.count())
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
