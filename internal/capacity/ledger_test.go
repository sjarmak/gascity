package capacity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func intp(n int) *int { return &n }

// newTestLedger builds a ledger with a deterministic clock and ID sequence.
func newTestLedger(t *testing.T, opts ...Option) (*Ledger, *fakeClock) {
	t.Helper()
	clk := &fakeClock{now: testTime()}
	var n int
	base := []Option{
		WithClock(clk.Now),
		WithIDFunc(func() (string, error) { n++; return fmt.Sprintf("rsv-%d", n), nil }),
	}
	return NewLedger(t.TempDir(), append(base, opts...)...), clk
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func req(workload, agent string, caps Caps) ReserveRequest {
	return ReserveRequest{WorkloadID: workload, Agent: agent, Caps: caps}
}

func TestReserve_GrantsDurableReservation(t *testing.T) {
	l, _ := newTestLedger(t)

	got, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{}))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got.ID != "rsv-1" || got.WorkloadID != "gc-1" || got.Agent != "worker" {
		t.Fatalf("reservation = %+v", got)
	}
	if got.ExpiresAt.IsZero() {
		t.Fatal("ExpiresAt is zero: an unconsumed reservation must be reclaimable")
	}

	// Durable: a fresh ledger over the same city sees it.
	snap, err := l.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Total != 1 || len(snap.Held) != 1 {
		t.Fatalf("snapshot = %+v, want 1 held", snap)
	}
}

// One workload holds at most one reservation, so a re-driving scheduler
// cannot double-book capacity for the same work.
func TestReserve_IdempotentPerWorkload(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()

	first, err := l.Reserve(ctx, req("gc-1", "worker", Caps{}))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := l.Reserve(ctx, req("gc-1", "worker", Caps{}))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second.ID = %q, want %q (must return the existing reservation)", second.ID, first.ID)
	}
	snap, _ := l.Snapshot()
	if snap.Total != 1 {
		t.Fatalf("Total = %d, want 1 (re-reserve must not consume a second unit)", snap.Total)
	}
}

// Re-reserving a workload somewhere else must not silently hand back the old
// placement: the caller would believe it holds capacity the ledger attributes
// to a different agent.
func TestReserve_RejectsPlacementChange(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()

	if _, err := l.Reserve(ctx, req("gc-1", "agent-a", Caps{})); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := l.Reserve(ctx, req("gc-1", "agent-b", Caps{}))
	if !errors.Is(err, ErrReservationMismatch) {
		t.Fatalf("err = %v, want ErrReservationMismatch", err)
	}

	// Releasing first is how a caller moves a workload.
	snap, _ := l.Snapshot()
	if err := l.Release(snap.Held[0].ID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	got, err := l.Reserve(ctx, req("gc-1", "agent-b", Caps{}))
	if err != nil {
		t.Fatalf("reserve after release: %v", err)
	}
	if got.Agent != "agent-b" {
		t.Fatalf("Agent = %q, want agent-b", got.Agent)
	}
}

func TestReserve_PlacementChangeDetectsRigAndProvider(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()
	base := ReserveRequest{WorkloadID: "gc-1", Agent: "worker", Rig: "gascity", Provider: "p1"}
	if _, err := l.Reserve(ctx, base); err != nil {
		t.Fatalf("first: %v", err)
	}

	moved := base
	moved.Rig = "beads"
	if _, err := l.Reserve(ctx, moved); !errors.Is(err, ErrReservationMismatch) {
		t.Fatalf("rig change: err = %v, want ErrReservationMismatch", err)
	}
	moved = base
	moved.Provider = "p2"
	if _, err := l.Reserve(ctx, moved); !errors.Is(err, ErrReservationMismatch) {
		t.Fatalf("provider change: err = %v, want ErrReservationMismatch", err)
	}
	// The identical placement still round-trips.
	if _, err := l.Reserve(ctx, base); err != nil {
		t.Fatalf("same placement: %v", err)
	}
}

// A reservation ID that cannot be generated must fail closed, not panic:
// this runs in front of every spawn, so a crash would stop the whole city.
func TestReserve_IDFailureFailsClosed(t *testing.T) {
	boom := errors.New("no entropy")
	l := NewLedger(t.TempDir(),
		WithClock(testTime),
		WithIDFunc(func() (string, error) { return "", boom }),
	)
	_, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{}))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the id error", err)
	}
	snap, err := l.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Total != 0 {
		t.Fatalf("Total = %d, want 0 (a failed id must not leak a reservation)", snap.Total)
	}
}

func TestReserve_AgentCapRejects(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()
	caps := Caps{Agent: map[string]*int{"worker": intp(1)}}

	if _, err := l.Reserve(ctx, req("gc-1", "worker", caps)); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := l.Reserve(ctx, req("gc-2", "worker", caps))
	if !errors.Is(err, ErrAgentCapReached) {
		t.Fatalf("err = %v, want ErrAgentCapReached", err)
	}
	// A different agent is unaffected.
	if _, err := l.Reserve(ctx, req("gc-3", "other", caps)); err != nil {
		t.Fatalf("other agent: %v", err)
	}
}

func TestReserve_RigCapRejects(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()
	caps := Caps{Rig: map[string]*int{"gascity": intp(1)}}

	r1 := ReserveRequest{WorkloadID: "gc-1", Agent: "a", Rig: "gascity", Caps: caps}
	r2 := ReserveRequest{WorkloadID: "gc-2", Agent: "b", Rig: "gascity", Caps: caps}
	if _, err := l.Reserve(ctx, r1); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := l.Reserve(ctx, r2); !errors.Is(err, ErrRigCapReached) {
		t.Fatalf("err = %v, want ErrRigCapReached", err)
	}
}

func TestReserve_WorkspaceCapRejects(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()
	caps := Caps{Workspace: intp(2)}

	for i := 1; i <= 2; i++ {
		if _, err := l.Reserve(ctx, req(fmt.Sprintf("gc-%d", i), "worker", caps)); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if _, err := l.Reserve(ctx, req("gc-3", "worker", caps)); !errors.Is(err, ErrWorkspaceCapReached) {
		t.Fatalf("err = %v, want ErrWorkspaceCapReached", err)
	}
}

// nil / negative caps mean unlimited, matching config's *int convention.
func TestReserve_UnlimitedCaps(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()
	caps := Caps{Agent: map[string]*int{"worker": nil}, Workspace: intp(-1)}

	for i := 1; i <= 5; i++ {
		if _, err := l.Reserve(ctx, req(fmt.Sprintf("gc-%d", i), "worker", caps)); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
}

// A cap of 0 disables the agent entirely.
func TestReserve_ZeroCapRejectsAll(t *testing.T) {
	l, _ := newTestLedger(t)
	caps := Caps{Agent: map[string]*int{"worker": intp(0)}}
	if _, err := l.Reserve(context.Background(), req("gc-1", "worker", caps)); !errors.Is(err, ErrAgentCapReached) {
		t.Fatalf("err = %v, want ErrAgentCapReached", err)
	}
}

// Capacity is held across the bind: a consumed reservation still occupies its unit.
func TestConsume_StillCountsAgainstCaps(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()
	caps := Caps{Agent: map[string]*int{"worker": intp(1)}}

	r, err := l.Reserve(ctx, req("gc-1", "worker", caps))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := l.Consume(r.ID); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	snap, _ := l.Snapshot()
	if len(snap.Consumed) != 1 || len(snap.Held) != 0 {
		t.Fatalf("snapshot = %+v, want 1 consumed / 0 held", snap)
	}
	if _, err := l.Reserve(ctx, req("gc-2", "worker", caps)); !errors.Is(err, ErrAgentCapReached) {
		t.Fatalf("err = %v, want ErrAgentCapReached (consumed must still occupy the unit)", err)
	}
}

func TestConsume_UnknownIDErrors(t *testing.T) {
	l, _ := newTestLedger(t)
	if err := l.Consume("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestConsume_IsIdempotent(t *testing.T) {
	l, _ := newTestLedger(t)
	r, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{}))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := l.Consume(r.ID); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if err := l.Consume(r.ID); err != nil {
		t.Fatalf("second Consume: %v (must be idempotent)", err)
	}
	snap, _ := l.Snapshot()
	if len(snap.Consumed) != 1 {
		t.Fatalf("Consumed = %d, want 1", len(snap.Consumed))
	}
}

func TestConsume_ExpiredReservationErrors(t *testing.T) {
	l, clk := newTestLedger(t, WithTTL(time.Minute))
	r, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{}))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	clk.advance(time.Minute)

	if err := l.Consume(r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Consume expired reservation: err = %v, want ErrNotFound", err)
	}
	snap, err := l.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Total != 0 {
		t.Fatalf("Total = %d, want 0 after expired consume", snap.Total)
	}
}

func TestRelease_FreesCapacity(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()
	caps := Caps{Agent: map[string]*int{"worker": intp(1)}}

	r, err := l.Reserve(ctx, req("gc-1", "worker", caps))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := l.Release(r.ID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := l.Reserve(ctx, req("gc-2", "worker", caps)); err != nil {
		t.Fatalf("reserve after release: %v (release must free the unit)", err)
	}
}

// Release is the watchdog-layer verb: at-least-once, so it must be idempotent.
func TestRelease_IsIdempotent(t *testing.T) {
	l, _ := newTestLedger(t)
	r, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{}))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := l.Release(r.ID); err != nil {
			t.Fatalf("Release #%d: %v (must be idempotent for at-least-once callers)", i, err)
		}
	}
	if err := l.Release("never-existed"); err != nil {
		t.Fatalf("Release(unknown): %v, want nil", err)
	}
}

func TestRelease_FreesConsumedReservation(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()
	r, err := l.Reserve(ctx, req("gc-1", "worker", Caps{}))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := l.Consume(r.ID); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if err := l.Release(r.ID); err != nil {
		t.Fatalf("Release: %v", err)
	}
	snap, _ := l.Snapshot()
	if snap.Total != 0 {
		t.Fatalf("Total = %d, want 0", snap.Total)
	}
}

// ADR-0010 crash matrix row 1: scheduler dies after reserve, before bind.
func TestReclaim_ExpiredHeldReturnsSlot(t *testing.T) {
	l, clk := newTestLedger(t, WithTTL(5*time.Minute))
	ctx := context.Background()
	caps := Caps{Agent: map[string]*int{"worker": intp(1)}}

	if _, err := l.Reserve(ctx, req("gc-1", "worker", caps)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	// Not yet expired.
	clk.advance(4 * time.Minute)
	reclaimed, err := l.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if len(reclaimed) != 0 {
		t.Fatalf("reclaimed %d before TTL, want 0", len(reclaimed))
	}

	clk.advance(2 * time.Minute) // now past TTL
	reclaimed, err = l.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].WorkloadID != "gc-1" {
		t.Fatalf("reclaimed = %+v, want gc-1", reclaimed)
	}
	if _, err := l.Reserve(ctx, req("gc-2", "worker", caps)); err != nil {
		t.Fatalf("reserve after reclaim: %v (slot must return)", err)
	}
}

// A consumed reservation is governed by the binding's lease, not the
// reservation TTL, so reclaim must never touch it.
func TestReclaim_NeverTouchesConsumed(t *testing.T) {
	l, clk := newTestLedger(t, WithTTL(time.Minute))
	r, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{}))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := l.Consume(r.ID); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	clk.advance(time.Hour)

	reclaimed, err := l.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if len(reclaimed) != 0 {
		t.Fatalf("reclaimed %+v, want none: consumed reservations outlive the TTL", reclaimed)
	}
	snap, _ := l.Snapshot()
	if len(snap.Consumed) != 1 {
		t.Fatalf("Consumed = %d, want 1", len(snap.Consumed))
	}
}

func TestReclaim_IsIdempotent(t *testing.T) {
	l, clk := newTestLedger(t, WithTTL(time.Minute))
	if _, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{})); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	clk.advance(time.Hour)

	if got, err := l.Reclaim(); err != nil || len(got) != 1 {
		t.Fatalf("first Reclaim = %v, %v; want 1, nil", got, err)
	}
	if got, err := l.Reclaim(); err != nil || len(got) != 0 {
		t.Fatalf("second Reclaim = %v, %v; want 0, nil", got, err)
	}
}

// Reserve amortizes reclaim, so an expired hold never wedges the cap.
func TestReserve_ReclaimsExpiredInline(t *testing.T) {
	l, clk := newTestLedger(t, WithTTL(time.Minute))
	ctx := context.Background()
	caps := Caps{Agent: map[string]*int{"worker": intp(1)}}

	if _, err := l.Reserve(ctx, req("gc-1", "worker", caps)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	clk.advance(time.Hour)

	if _, err := l.Reserve(ctx, req("gc-2", "worker", caps)); err != nil {
		t.Fatalf("Reserve after expiry: %v (must reclaim inline)", err)
	}
	snap, _ := l.Snapshot()
	if snap.Total != 1 {
		t.Fatalf("Total = %d, want 1 (expired hold must be gone)", snap.Total)
	}
}

// A hold that expires while the selector runs must not spuriously reject the
// commit, so commit reclaims before it recounts. Reserving from a second
// handle inside the selector also proves the ledger lock is not held while
// the selector runs: it would deadlock if it were.
func TestReserve_ReclaimsExpiredAtCommit(t *testing.T) {
	clk := &fakeClock{now: testTime()}
	dir := t.TempDir()
	var n int
	newID := func() (string, error) { n++; return fmt.Sprintf("rsv-%d", n), nil }

	other := NewLedger(dir, WithClock(clk.Now), WithIDFunc(newID), WithTTL(time.Minute))
	slowPick := func(ctx context.Context, _ PickRequest) (string, error) {
		// A competing reserver takes the only unit...
		if _, err := other.Reserve(ctx, req("gc-1", "worker", Caps{})); err != nil {
			return "", err
		}
		clk.advance(time.Hour) // ...then abandons it and the hold expires.
		return "acct", nil
	}
	l := NewLedger(dir,
		WithClock(clk.Now), WithIDFunc(newID), WithTTL(time.Minute),
		WithSelector(slowPick),
	)

	caps := Caps{Agent: map[string]*int{"worker": intp(1)}}
	got, err := l.Reserve(context.Background(), req("gc-2", "worker", caps))
	if err != nil {
		t.Fatalf("Reserve: %v (an expired hold must not block the commit)", err)
	}
	if got.WorkloadID != "gc-2" {
		t.Fatalf("WorkloadID = %q, want gc-2", got.WorkloadID)
	}
	snap, _ := l.Snapshot()
	if snap.Total != 1 {
		t.Fatalf("Total = %d, want 1 (the abandoned hold must be gone)", snap.Total)
	}
}

func TestSnapshot_CountsByScope(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()
	mk := func(w, agent, rig string) ReserveRequest {
		return ReserveRequest{WorkloadID: w, Agent: agent, Rig: rig}
	}
	for _, r := range []ReserveRequest{
		mk("gc-1", "worker", "gascity"),
		mk("gc-2", "worker", "gascity"),
		mk("gc-3", "other", "beads"),
	} {
		if _, err := l.Reserve(ctx, r); err != nil {
			t.Fatalf("Reserve %s: %v", r.WorkloadID, err)
		}
	}

	snap, err := l.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Total != 3 {
		t.Fatalf("Total = %d, want 3", snap.Total)
	}
	if snap.ByAgent["worker"] != 2 || snap.ByAgent["other"] != 1 {
		t.Fatalf("ByAgent = %v", snap.ByAgent)
	}
	if snap.ByRig["gascity"] != 2 || snap.ByRig["beads"] != 1 {
		t.Fatalf("ByRig = %v", snap.ByRig)
	}
}

func TestSnapshot_ExcludesExpiredHolds(t *testing.T) {
	l, clk := newTestLedger(t, WithTTL(time.Minute))
	if _, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{})); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	clk.advance(time.Minute)

	snap, err := l.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Total != 0 || len(snap.Held) != 0 {
		t.Fatalf("snapshot = %+v, want expired hold excluded", snap)
	}
}

func TestReserve_RequiresWorkloadAndAgent(t *testing.T) {
	l, _ := newTestLedger(t)
	ctx := context.Background()
	if _, err := l.Reserve(ctx, req("", "worker", Caps{})); err == nil {
		t.Fatal("empty workload: err = nil, want validation error")
	}
	if _, err := l.Reserve(ctx, req("gc-1", "", Caps{})); err == nil {
		t.Fatal("empty agent: err = nil, want validation error")
	}
}

// The whole point of the flock'd ledger: concurrent reservers cannot
// oversubscribe a cap.
func TestReserve_ConcurrentRespectsCap(t *testing.T) {
	const limit, racers = 3, 12
	clk := &fakeClock{now: testTime()}
	var mu sync.Mutex
	var n int
	l := NewLedger(t.TempDir(),
		WithClock(clk.Now),
		WithIDFunc(func() (string, error) {
			mu.Lock()
			defer mu.Unlock()
			n++
			return fmt.Sprintf("rsv-%d", n), nil
		}),
	)
	caps := Caps{Agent: map[string]*int{"worker": intp(limit)}}

	var wg sync.WaitGroup
	granted := make([]bool, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := l.Reserve(context.Background(), req(fmt.Sprintf("gc-%d", i), "worker", caps))
			granted[i] = err == nil
		}(i)
	}
	close(start)
	wg.Wait()

	got := 0
	for _, ok := range granted {
		if ok {
			got++
		}
	}
	if got != limit {
		t.Fatalf("granted = %d, want exactly %d", got, limit)
	}
	snap, err := l.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Total != limit {
		t.Fatalf("ledger Total = %d, want %d", snap.Total, limit)
	}
}

func TestReserve_RejectionNamesTheScope(t *testing.T) {
	l, _ := newTestLedger(t)
	caps := Caps{Agent: map[string]*int{"worker": intp(0)}}
	_, err := l.Reserve(context.Background(), req("gc-1", "worker", caps))
	if err == nil {
		t.Fatal("err = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "worker") {
		t.Fatalf("err = %q, want the agent name for trace attribution", err)
	}
}
