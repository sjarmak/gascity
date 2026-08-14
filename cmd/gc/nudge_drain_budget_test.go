package main

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
)

func installAdvancingNudgeStoreSeam(t *testing.T, store beads.Store) {
	t.Helper()
	previous := openNudgeBeadStore
	openNudgeBeadStore = func(string) beads.NudgesStore {
		return beads.NudgesStore{Store: store}
	}
	t.Cleanup(func() { openNudgeBeadStore = previous })
}

func runQueuedNudgeClaimWithClock(t *testing.T, backlog int, budget time.Duration) (time.Duration, int64) {
	t.Helper()
	cityPath := t.TempDir()
	fakeClock := &clock.Fake{Time: time.Now().UTC()}
	seedDeadBacklog(t, cityPath, fakeClock.Now(), backlog)
	store := &advancingNudgeStore{fakeClock: fakeClock, latency: 20 * time.Millisecond}
	installAdvancingNudgeStoreSeam(t, store)
	start := fakeClock.Now()
	if _, err := claimDueQueuedNudgesMatchingWithClock(
		cityPath,
		start,
		start.Add(budget),
		fakeClock,
		func(queuedNudge) bool { return false },
	); err != nil {
		t.Fatalf("claimDueQueuedNudgesMatchingWithClock: %v", err)
	}
	return fakeClock.Now().Sub(start), store.operations()
}

func TestQueuedNudgeClaimHonorsForegroundMaintenanceDeadline(t *testing.T) {
	const backlog = 160
	elapsed, operations := runQueuedNudgeClaimWithClock(t, backlog, nudgeForegroundMaintenanceBudget)
	if elapsed > nudgeForegroundMaintenanceBudget+advancingNudgeStoreDeadItemOps*20*time.Millisecond {
		t.Fatalf("virtual elapsed = %v, want at most one item beyond %v", elapsed, nudgeForegroundMaintenanceBudget)
	}
	if operations >= int64(backlog) {
		t.Fatalf("store operations = %d, want fewer than backlog %d to prove the deadline cut in", operations, backlog)
	}
}

func TestQueuedNudgeClaimKeepsBackgroundMaintenanceUnbounded(t *testing.T) {
	const backlog = 50
	_, operations := runQueuedNudgeClaimWithClock(t, backlog, 24*time.Hour)
	if operations < int64(backlog) {
		t.Fatalf("store operations = %d, want at least backlog %d", operations, backlog)
	}
}
