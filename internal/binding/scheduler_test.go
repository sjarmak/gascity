package binding

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/capacity"
)

func intPtr(n int) *int { return &n }

// newTestScheduler returns a scheduler and its ledger over a fresh city.
func newTestScheduler(t *testing.T, opts ...Option) (*Scheduler, *capacity.Ledger, string) {
	t.Helper()
	city := t.TempDir()
	ledger := capacity.NewLedger(city, capacity.WithClock(testTime))
	opts = append([]Option{WithClock(testTime)}, opts...)
	return NewScheduler(city, ledger, opts...), ledger, city
}

func workload(id string, band int, enqueuedOffset time.Duration) ReadyWorkload {
	return ReadyWorkload{
		ID:           id,
		Agent:        "worker",
		PriorityBand: band,
		EnqueuedAt:   testTime().Add(enqueuedOffset),
	}
}

// unlimited caps: this bead's ordering and fencing behavior must not depend on
// capacity being scarce.
func unlimited() capacity.Caps { return capacity.Caps{} }

func TestBind_EmptyCandidatesBindsNothing(t *testing.T) {
	s, ledger, _ := newTestScheduler(t)

	got, err := s.Bind(context.Background(), BindRequest{Caps: unlimited()})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got != nil {
		t.Fatalf("Bind = %+v, want nil (nothing to bind)", got)
	}

	snap, err := ledger.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Total != 0 {
		t.Fatalf("reservations = %d, want 0 (an empty pass must not reserve)", snap.Total)
	}
}

// Band ascending, then enqueued_at ascending within a band: each successive
// pass binds the next workload in that order, skipping the already-bound.
func TestBind_PriorityBandThenFCFS(t *testing.T) {
	s, _, _ := newTestScheduler(t)
	req := BindRequest{
		Candidates: []ReadyWorkload{
			workload("gc-band2-old", 2, 0),
			workload("gc-band1-new", 1, time.Hour),
			workload("gc-band1-old", 1, time.Minute),
		},
		Caps: unlimited(),
	}

	want := []string{"gc-band1-old", "gc-band1-new", "gc-band2-old"}
	for i, wantID := range want {
		got, err := s.Bind(context.Background(), req)
		if err != nil {
			t.Fatalf("Bind %d: %v", i, err)
		}
		if got == nil {
			t.Fatalf("Bind %d = nil, want %q", i, wantID)
		}
		if got.WorkloadID != wantID {
			t.Fatalf("Bind %d = %q, want %q", i, got.WorkloadID, wantID)
		}
	}

	// Every candidate is bound; a further pass has nothing left to bind.
	got, err := s.Bind(context.Background(), req)
	if err != nil {
		t.Fatalf("Bind exhausted: %v", err)
	}
	if got != nil {
		t.Fatalf("Bind = %+v, want nil (all candidates already bound)", got)
	}
}

// The health gate is checked in front of Reserve, so an unhealthy provider
// costs no capacity.
func TestBind_SkipsUnhealthyProvider(t *testing.T) {
	health := func(provider string) (healthy, present bool) {
		switch provider {
		case "red":
			return false, true
		case "green":
			return true, true
		default:
			return true, false
		}
	}
	s, ledger, _ := newTestScheduler(t, WithHealthCheck(health))

	red := workload("gc-red", 1, 0)
	red.Provider = "red"
	green := workload("gc-green", 2, 0)
	green.Provider = "green"

	got, err := s.Bind(context.Background(), BindRequest{
		Candidates: []ReadyWorkload{red, green},
		Caps:       unlimited(),
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got == nil || got.WorkloadID != "gc-green" {
		t.Fatalf("Bind = %+v, want gc-green (gc-red's provider is red)", got)
	}

	snap, err := ledger.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Total != 1 {
		t.Fatalf("reservations = %d, want 1 (the skipped candidate must not reserve)", snap.Total)
	}
}

// An absent registry entry fails open: an unknown provider is not a reason to
// refuse work, matching the existing reconciler gate's contract.
func TestBind_UnknownProviderFailsOpen(t *testing.T) {
	health := func(string) (healthy, present bool) { return true, false }
	s, _, _ := newTestScheduler(t, WithHealthCheck(health))

	w := workload("gc-1", 1, 0)
	w.Provider = "never-probed"
	got, err := s.Bind(context.Background(), BindRequest{
		Candidates: []ReadyWorkload{w},
		Caps:       unlimited(),
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got == nil {
		t.Fatal("Bind = nil, want a binding (absent health entry must fail open)")
	}
}

// Fail-closed: without a reservation there is no Binding, and the refusal
// leaves no capacity behind either.
func TestBind_NoCapacityBindsNothingAndLeaksNothing(t *testing.T) {
	s, ledger, city := newTestScheduler(t)

	got, err := s.Bind(context.Background(), BindRequest{
		Candidates: []ReadyWorkload{workload("gc-1", 1, 0)},
		Caps:       capacity.Caps{Workspace: intPtr(0)},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got != nil {
		t.Fatalf("Bind = %+v, want nil (workspace cap is 0)", got)
	}

	state, err := LoadState(city)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Bound) != 0 {
		t.Fatalf("Bound = %+v, want none", state.Bound)
	}
	snap, err := ledger.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Total != 0 {
		t.Fatalf("reservations = %d, want 0 (a refused bind must not leak a unit)", snap.Total)
	}
}

// A capped scope refuses only its own candidates; the pass moves on rather
// than stalling behind them.
func TestBind_SkipsCappedAgentAndBindsNext(t *testing.T) {
	s, _, _ := newTestScheduler(t)

	full := workload("gc-full", 1, 0)
	full.Agent = "busy"
	next := workload("gc-next", 2, 0)
	next.Agent = "idle"

	got, err := s.Bind(context.Background(), BindRequest{
		Candidates: []ReadyWorkload{full, next},
		Caps:       capacity.Caps{Agent: map[string]*int{"busy": intPtr(0)}},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got == nil || got.WorkloadID != "gc-next" {
		t.Fatalf("Bind = %+v, want gc-next (agent busy is at its cap)", got)
	}
}

func TestBind_ReservationRefNamesTheRealReservation(t *testing.T) {
	s, ledger, _ := newTestScheduler(t)

	got, err := s.Bind(context.Background(), BindRequest{
		Candidates: []ReadyWorkload{workload("gc-1", 1, 0)},
		Caps:       unlimited(),
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got == nil {
		t.Fatal("Bind = nil, want a binding")
	}

	snap, err := ledger.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// The unit backing a committed Binding is consumed, not merely held: its
	// lifetime is now the Binding's, not the hold TTL's.
	if len(snap.Consumed) != 1 {
		t.Fatalf("Consumed = %+v, want exactly 1", snap.Consumed)
	}
	if snap.Consumed[0].ID != got.ReservationRef {
		t.Fatalf("ReservationRef = %q, want %q", got.ReservationRef, snap.Consumed[0].ID)
	}
	if snap.Consumed[0].WorkloadID != "gc-1" {
		t.Fatalf("reservation workload = %q, want gc-1", snap.Consumed[0].WorkloadID)
	}
	if len(snap.Held) != 0 {
		t.Fatalf("Held = %+v, want none (the hold must be consumed at bind)", snap.Held)
	}
}

func TestBind_GenerationAndAttemptSucceedPrior(t *testing.T) {
	s, _, _ := newTestScheduler(t)

	w := workload("gc-1", 1, 0)
	w.PriorGeneration = 3
	w.PriorAttempt = 2

	got, err := s.Bind(context.Background(), BindRequest{
		Candidates: []ReadyWorkload{w},
		Caps:       unlimited(),
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got == nil {
		t.Fatal("Bind = nil, want a binding")
	}
	if got.Generation != 4 {
		t.Fatalf("Generation = %d, want 4 (must outrank the Binding it replaces)", got.Generation)
	}
	if got.Attempt != 3 {
		t.Fatalf("Attempt = %d, want 3", got.Attempt)
	}
	if !got.BoundAt.Equal(testTime()) {
		t.Fatalf("BoundAt = %v, want %v", got.BoundAt, testTime())
	}
}

// The at-most-once invariant, raced. Two schedulers binding the same single
// candidate must produce exactly one Binding backed by exactly one unit.
func TestBind_ConcurrentBindOfSameCandidateBindsOnce(t *testing.T) {
	s, ledger, city := newTestScheduler(t)
	req := BindRequest{
		Candidates: []ReadyWorkload{workload("gc-1", 1, 0)},
		Caps:       unlimited(),
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		wins    int
		bindErr error
	)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.Bind(context.Background(), req)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				bindErr = err
				return
			}
			if got != nil {
				wins++
			}
		}()
	}
	wg.Wait()

	if bindErr != nil {
		t.Fatalf("Bind: %v", bindErr)
	}
	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1", wins)
	}

	state, err := LoadState(city)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Bound) != 1 {
		t.Fatalf("Bound = %+v, want exactly 1 Binding", state.Bound)
	}
	snap, err := ledger.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// The loser must not release the winner's unit, and must not leave one of
	// its own behind.
	if snap.Total != 1 {
		t.Fatalf("reservations = %d, want exactly 1 backing the Binding", snap.Total)
	}
	if snap.Consumed[0].ID != state.Bound[0].ReservationRef {
		t.Fatalf("reservation %q does not back Binding %+v", snap.Consumed[0].ID, state.Bound[0])
	}
}

func TestRelease_ReturnsCapacityAndAllowsRebind(t *testing.T) {
	s, ledger, city := newTestScheduler(t)
	req := BindRequest{
		Candidates: []ReadyWorkload{workload("gc-1", 1, 0)},
		Caps:       capacity.Caps{Workspace: intPtr(1)},
	}

	first, err := s.Bind(context.Background(), req)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if first == nil {
		t.Fatal("Bind = nil, want a binding")
	}

	if err := s.Release("gc-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	state, err := LoadState(city)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Bound) != 0 {
		t.Fatalf("Bound = %+v, want none after Release", state.Bound)
	}
	snap, err := ledger.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Total != 0 {
		t.Fatalf("reservations = %d, want 0 (Release must return the unit)", snap.Total)
	}

	// The returned unit is usable: the workspace cap of 1 admits a rebind.
	second, err := s.Bind(context.Background(), req)
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if second == nil {
		t.Fatal("rebind = nil, want a binding (the released unit must be reusable)")
	}
	if second.ReservationRef == first.ReservationRef {
		t.Fatalf("rebind reused reservation %q, want a fresh one", second.ReservationRef)
	}
}

// Release is the seam a terminal outcome and a retry both call, and those
// paths are at-least-once: a second call must not fail.
func TestRelease_UnknownWorkloadIsNoOp(t *testing.T) {
	s, _, _ := newTestScheduler(t)

	if err := s.Release("gc-never-bound"); err != nil {
		t.Fatalf("Release unknown: %v", err)
	}

	got, err := s.Bind(context.Background(), BindRequest{
		Candidates: []ReadyWorkload{workload("gc-1", 1, 0)},
		Caps:       unlimited(),
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got == nil {
		t.Fatal("Bind = nil, want a binding")
	}
	if err := s.Release("gc-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := s.Release("gc-1"); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

// The Binding is durable: a scheduler that did not write it still sees it and
// refuses to bind the workload a second time.
func TestBind_BindingSurvivesFreshScheduler(t *testing.T) {
	s, ledger, city := newTestScheduler(t)
	req := BindRequest{
		Candidates: []ReadyWorkload{workload("gc-1", 1, 0)},
		Caps:       unlimited(),
	}

	first, err := s.Bind(context.Background(), req)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if first == nil {
		t.Fatal("Bind = nil, want a binding")
	}

	fresh := NewScheduler(city, ledger, WithClock(testTime))
	got, err := fresh.Bind(context.Background(), req)
	if err != nil {
		t.Fatalf("fresh Bind: %v", err)
	}
	if got != nil {
		t.Fatalf("fresh Bind = %+v, want nil (gc-1 is already bound)", got)
	}

	state, err := LoadState(city)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Bound) != 1 || state.Bound[0].ReservationRef != first.ReservationRef {
		t.Fatalf("Bound = %+v, want the original binding %+v", state.Bound, first)
	}
}

func TestBind_RequiresWorkloadIDAndAgent(t *testing.T) {
	s, _, _ := newTestScheduler(t)

	for _, tc := range []struct {
		name string
		w    ReadyWorkload
	}{
		{"no id", ReadyWorkload{Agent: "worker"}},
		{"no agent", ReadyWorkload{ID: "gc-1"}},
		{"blank id", ReadyWorkload{ID: "  ", Agent: "worker"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Bind(context.Background(), BindRequest{
				Candidates: []ReadyWorkload{tc.w},
				Caps:       unlimited(),
			}); err == nil {
				t.Fatal("Bind error = nil, want a validation error")
			}
		})
	}
}
