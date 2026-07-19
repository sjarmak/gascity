package binding

import (
	"context"
	"errors"
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

	if err := s.Release("gc-1", first.Generation); err != nil {
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

	if err := s.Release("gc-never-bound", 1); err != nil {
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
	if err := s.Release("gc-1", got.Generation); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := s.Release("gc-1", got.Generation); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestBind_ConcurrentReleaseAfterConsumeCannotInvalidateReturn(t *testing.T) {
	city := t.TempDir()
	ledger := capacity.NewLedger(city, capacity.WithClock(testTime))
	consumed := make(chan struct{})
	resume := make(chan struct{})
	s := NewScheduler(city, ledger, WithClock(testTime), withCrashHook(func(point crashPoint) error {
		if point == crashAfterConsume {
			close(consumed)
			<-resume
		}
		return nil
	}))

	type result struct {
		binding *Binding
		err     error
	}
	done := make(chan result, 1)
	go func() {
		b, err := s.Bind(context.Background(), BindRequest{
			Candidates: []ReadyWorkload{workload("gc-1", 1, 0)},
			Caps:       unlimited(),
		})
		done <- result{binding: b, err: err}
	}()
	<-consumed

	// Generation 1 exists only as pending at this point. It is not executable,
	// so a terminal release cannot legitimately target it and must not cancel
	// the promotion that makes Bind's return value usable.
	if err := s.Release("gc-1", 1); err != nil {
		t.Fatalf("Release during promotion: %v", err)
	}
	close(resume)
	got := <-done
	if got.err != nil || got.binding == nil {
		t.Fatalf("Bind = %+v, err=%v", got.binding, got.err)
	}
	state, err := LoadState(city)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Pending) != 0 || len(state.Bound) != 1 || state.Bound[0] != *got.binding {
		t.Fatalf("state = %+v, want exact returned binding active", state)
	}
	snap, err := ledger.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Consumed) != 1 || snap.Consumed[0].ID != got.binding.ReservationRef {
		t.Fatalf("ledger = %+v, want returned binding's consumed reservation", snap)
	}
}

func TestRelease_StaleGenerationDoesNotRemoveRebind(t *testing.T) {
	s, _, _ := newTestScheduler(t)
	req := BindRequest{Candidates: []ReadyWorkload{workload("gc-1", 1, 0)}, Caps: unlimited()}
	first, err := s.Bind(context.Background(), req)
	if err != nil || first == nil {
		t.Fatalf("first Bind = %+v, err=%v", first, err)
	}
	if err := s.Release("gc-1", first.Generation); err != nil {
		t.Fatal(err)
	}
	req.Candidates[0].PriorGeneration = first.Generation
	req.Candidates[0].PriorAttempt = first.Attempt
	second, err := s.Bind(context.Background(), req)
	if err != nil || second == nil {
		t.Fatalf("second Bind = %+v, err=%v", second, err)
	}
	if err := s.Release("gc-1", first.Generation); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(s.cityPath)
	if err != nil || len(state.Bound) != 1 || state.Bound[0] != *second {
		t.Fatalf("state after stale release = %+v, err=%v; want generation %d", state, err, second.Generation)
	}
}

func TestRelease_RejectsActiveReservationOwnershipMismatch(t *testing.T) {
	s, ledger, city := newTestScheduler(t)
	b, err := s.Bind(context.Background(), BindRequest{Candidates: []ReadyWorkload{workload("gc-1", 1, 0)}, Caps: unlimited()})
	if err != nil || b == nil {
		t.Fatalf("Bind = %+v, err=%v", b, err)
	}
	if err := capacity.WithState(city, func(st *capacity.State) error {
		st.Consumed[0].Agent = "other-worker"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Release(b.WorkloadID, b.Generation); err == nil {
		t.Fatal("Release error = nil, want ownership mismatch")
	}
	state, stateErr := LoadState(city)
	snap, snapErr := ledger.Snapshot()
	if stateErr != nil || snapErr != nil {
		t.Fatalf("reload: state err=%v ledger err=%v", stateErr, snapErr)
	}
	if len(state.Bound) != 1 || len(snap.Consumed) != 1 {
		t.Fatalf("mismatched release mutated state: binding=%+v ledger=%+v", state, snap)
	}
}

func TestBind_TimeNowClockPromotesSerializedIntent(t *testing.T) {
	city := t.TempDir()
	ledger := capacity.NewLedger(city)
	s := NewScheduler(city, ledger, WithClock(time.Now))
	b, err := s.Bind(context.Background(), BindRequest{Candidates: []ReadyWorkload{workload("gc-1", 1, 0)}, Caps: unlimited()})
	if err != nil || b == nil {
		t.Fatalf("Bind = %+v, err=%v", b, err)
	}
	state, err := LoadState(city)
	if err != nil || len(state.Pending) != 0 || len(state.Bound) != 1 || !bindingsEqual(state.Bound[0], *b) {
		t.Fatalf("state = %+v, err=%v; want promoted binding %+v", state, err, b)
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

func TestBind_CrashTransitionsRecoverWithoutLeakOrDuplicate(t *testing.T) {
	crashed := errors.New("simulated process death")
	for _, tc := range []struct {
		point                          crashPoint
		held, consumed, pending, bound int
	}{
		{crashAfterReserve, 1, 0, 0, 0},
		{crashAfterPending, 1, 0, 1, 0},
		{crashAfterConsume, 0, 1, 1, 0},
		{crashAfterActive, 0, 1, 0, 1},
	} {
		t.Run(string(tc.point), func(t *testing.T) {
			city := t.TempDir()
			ledger := capacity.NewLedger(city, capacity.WithClock(testTime))
			injected := false
			s := NewScheduler(city, ledger, WithClock(testTime), withCrashHook(func(got crashPoint) error {
				if got == tc.point && !injected {
					injected = true
					return crashed
				}
				return nil
			}))
			req := BindRequest{Candidates: []ReadyWorkload{workload("gc-1", 1, 0)}, Caps: unlimited()}

			if _, err := s.Bind(context.Background(), req); !errors.Is(err, crashed) {
				t.Fatalf("Bind error = %v, want simulated crash at %s", err, tc.point)
			}
			crashState, err := LoadState(city)
			if err != nil {
				t.Fatalf("LoadState at crash: %v", err)
			}
			crashSnap, err := ledger.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot at crash: %v", err)
			}
			if len(crashSnap.Held) != tc.held || len(crashSnap.Consumed) != tc.consumed ||
				len(crashState.Pending) != tc.pending || len(crashState.Bound) != tc.bound {
				t.Fatalf("crash state ledger=%+v binding=%+v, want held=%d consumed=%d pending=%d bound=%d",
					crashSnap, crashState, tc.held, tc.consumed, tc.pending, tc.bound)
			}

			// A fresh scheduler models restart. Recovery is also called twice to
			// prove replay itself is harmless before scheduling resumes.
			fresh := NewScheduler(city, ledger, WithClock(testTime))
			if err := fresh.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			if err := fresh.Recover(); err != nil {
				t.Fatalf("second Recover: %v", err)
			}
			if _, err := fresh.Bind(context.Background(), req); err != nil {
				t.Fatalf("Bind after restart: %v", err)
			}

			state, err := LoadState(city)
			if err != nil {
				t.Fatalf("LoadState: %v", err)
			}
			if len(state.Pending) != 0 || len(state.Bound) != 1 {
				t.Fatalf("state = %+v, want one active binding and no pending intent", state)
			}
			snap, err := ledger.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if snap.Total != 1 || len(snap.Consumed) != 1 || len(snap.Held) != 0 {
				t.Fatalf("ledger = %+v, want one consumed unit and no leaked hold", snap)
			}
			if state.Bound[0].ReservationRef != snap.Consumed[0].ID {
				t.Fatalf("binding reservation = %q, consumed = %q", state.Bound[0].ReservationRef, snap.Consumed[0].ID)
			}
		})
	}
}

func TestRecover_ReclaimedPendingIntentAllowsFreshBind(t *testing.T) {
	city := t.TempDir()
	ledger := capacity.NewLedger(city, capacity.WithClock(testTime))
	crashed := errors.New("simulated process death")
	s := NewScheduler(city, ledger, WithClock(testTime), withCrashHook(func(point crashPoint) error {
		if point == crashAfterPending {
			return crashed
		}
		return nil
	}))
	req := BindRequest{Candidates: []ReadyWorkload{workload("gc-1", 1, 0)}, Caps: unlimited()}
	if _, err := s.Bind(context.Background(), req); !errors.Is(err, crashed) {
		t.Fatalf("Bind error = %v, want simulated crash", err)
	}
	state, err := LoadState(city)
	if err != nil || len(state.Pending) != 1 {
		t.Fatalf("pending state = %+v, err=%v", state, err)
	}
	// Release models TTL reclamation of the still-held reservation.
	if err := ledger.Release(state.Pending[0].ReservationRef); err != nil {
		t.Fatalf("Release: %v", err)
	}
	fresh := NewScheduler(city, ledger, WithClock(testTime))
	if err := fresh.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got, err := fresh.Bind(context.Background(), req); err != nil || got == nil {
		t.Fatalf("Bind after reclamation = %+v, err=%v", got, err)
	}
	state, err = LoadState(city)
	if err != nil || len(state.Pending) != 0 || len(state.Bound) != 1 {
		t.Fatalf("recovered state = %+v, err=%v", state, err)
	}
}

func TestRecover_RejectsPendingReservationOwnedByDifferentPlacementWithoutMutation(t *testing.T) {
	city := t.TempDir()
	ledger := capacity.NewLedger(city, capacity.WithClock(testTime), capacity.WithIDFunc(func() (string, error) { return "rsv-1", nil }))
	r, err := ledger.Reserve(context.Background(), capacity.ReserveRequest{WorkloadID: "gc-owner", Agent: "owner", Rig: "rig-a", Provider: "provider-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WithState(city, func(st *State) error {
		st.Pending = append(st.Pending, Binding{WorkloadID: "gc-other", Agent: "other", Rig: "rig-b", Provider: "provider-b", ReservationRef: r.ID, Generation: 1, Attempt: 1, BoundAt: testTime()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	s := NewScheduler(city, ledger, WithClock(testTime))
	if err := s.Recover(); err == nil {
		t.Fatal("Recover error = nil, want ownership mismatch")
	}
	state, stateErr := LoadState(city)
	snap, snapErr := ledger.Snapshot()
	if stateErr != nil || snapErr != nil {
		t.Fatalf("reload: state err=%v ledger err=%v", stateErr, snapErr)
	}
	if len(state.Pending) != 1 || len(state.Bound) != 0 || len(snap.Held) != 1 || len(snap.Consumed) != 0 {
		t.Fatalf("ambiguous recovery mutated state: binding=%+v ledger=%+v", state, snap)
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
		{"negative prior generation", ReadyWorkload{ID: "gc-1", Agent: "worker", PriorGeneration: -1}},
		{"negative prior attempt", ReadyWorkload{ID: "gc-1", Agent: "worker", PriorAttempt: -1}},
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

func TestBind_InvalidCandidateDoesNotRecoverOrConsumePendingIntent(t *testing.T) {
	city := t.TempDir()
	ledger := capacity.NewLedger(city, capacity.WithClock(testTime))
	r, err := ledger.Reserve(context.Background(), capacity.ReserveRequest{WorkloadID: "gc-pending", Agent: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WithState(city, func(st *State) error {
		st.Pending = append(st.Pending, Binding{WorkloadID: "gc-pending", Agent: "worker", ReservationRef: r.ID, Generation: 1, Attempt: 1, BoundAt: testTime()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(city, ledger, WithClock(testTime))
	if _, err := s.Bind(context.Background(), BindRequest{Candidates: []ReadyWorkload{{ID: "gc-invalid", Agent: "worker", PriorGeneration: -1}}}); err == nil {
		t.Fatal("Bind error = nil, want invalid candidate")
	}
	state, stateErr := LoadState(city)
	snap, snapErr := ledger.Snapshot()
	if stateErr != nil || snapErr != nil {
		t.Fatalf("reload: state err=%v ledger err=%v", stateErr, snapErr)
	}
	if len(state.Pending) != 1 || len(state.Bound) != 0 || len(snap.Held) != 1 || len(snap.Consumed) != 0 {
		t.Fatalf("invalid candidate mutated recovery state: binding=%+v ledger=%+v", state, snap)
	}
}
