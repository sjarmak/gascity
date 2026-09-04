package beads

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestMintClaimGenerationFitsFifteenDigits is the discriminating regression
// for the gc-3ohe47 round-4 finding recorded against 74e19c487: the
// predecessor UnixNano-based mint produced 19-digit values, but the external
// consumer /home/ds/gas-city/bin/gc-outcome-close parses at most 15 decimal
// digits, so every claim it minted was permanently uncloseable. This asserts
// the digit-width bound directly, both for "now" and for a candidate far
// enough in the future that the millisecond-since-epoch term would be the
// one at risk of overflowing it.
func TestMintClaimGenerationFitsFifteenDigits(t *testing.T) {
	resetClaimGenerationMintState(t)

	farFuture := claimGenerationEpoch.AddDate(1000, 0, 0) // +1000 years, nowhere near the ~31,000 year ceiling
	for _, now := range []time.Time{time.Now(), claimGenerationEpoch, farFuture} {
		got := mintClaimGeneration(now, 0)
		s := strconv.FormatInt(got, 10)
		if len(s) > 15 {
			t.Fatalf("mintClaimGeneration(%v, 0) = %d has %d digits, want <= 15 (gc-outcome-close's parsing ceiling)", now, got, len(s))
		}
		if got <= 0 {
			t.Fatalf("mintClaimGeneration(%v, 0) = %d, want a positive value", now, got)
		}
	}
}

// TestMintClaimGenerationAdvancesDespiteBackwardClockStep is the
// discriminating regression for the other half of the round-4 finding:
// UnixNano has no monotonicity guarantee under host clock correction, so a
// second mint computed after a backward wall-clock step (NTP correction,
// manual clock set) could reuse or regress below a value already minted.
// This simulates exactly that: the second "now" passed to mintClaimGeneration
// is earlier than the first, with no observedFloor to lean on, and the
// process-local high-water mark must still force forward progress.
func TestMintClaimGenerationAdvancesDespiteBackwardClockStep(t *testing.T) {
	resetClaimGenerationMintState(t)

	later := claimGenerationEpoch.Add(24 * time.Hour)
	earlier := claimGenerationEpoch.Add(1 * time.Hour) // "clock corrected backward" relative to later

	first := mintClaimGeneration(later, 0)
	second := mintClaimGeneration(earlier, 0)

	if second <= first {
		t.Fatalf("second mint = %d did not advance past first mint = %d despite a backward clock step; a stale/reused generation would let a superseded claim episode's authority pass gc-outcome-close's equality check", second, first)
	}
}

// TestMintClaimGenerationHonorsObservedFloor covers the other lower bound:
// when the bead's own previously-recorded generation is ahead of the current
// wall-clock candidate (the single-actor backward-clock-jump case
// mintClaimGeneration's doc names), the mint must still advance past it.
func TestMintClaimGenerationHonorsObservedFloor(t *testing.T) {
	resetClaimGenerationMintState(t)

	const floor = 999_999_999_999 // far ahead of a fresh-epoch wall-clock candidate
	got := mintClaimGeneration(claimGenerationEpoch, floor)

	if got <= floor {
		t.Fatalf("mintClaimGeneration(epoch, %d) = %d, want a value strictly greater than the observed floor", floor, got)
	}
}

// TestMintClaimGenerationConcurrentCallsNeverCollide exercises the
// claimGenerationHighWater mutex under -race and asserts the mechanism it
// exists for: no two concurrent mints for the same wall-clock instant
// produce the same generation.
func TestMintClaimGenerationConcurrentCallsNeverCollide(t *testing.T) {
	resetClaimGenerationMintState(t)

	const n = 64
	now := time.Now()
	results := make([]int64, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			results[i] = mintClaimGeneration(now, 0)
		}(i)
	}
	wg.Wait()

	seen := make(map[int64]bool, n)
	for _, v := range results {
		if seen[v] {
			t.Fatalf("generation %d minted more than once across %d concurrent callers sharing the same wall-clock instant", v, n)
		}
		seen[v] = true
	}
}

// resetClaimGenerationMintState clears the process-local high-water mark
// before a test that asserts on mintClaimGeneration's return values relative
// to a specific starting point. Tests in this package run sequentially
// (none call t.Parallel()), so resetting package state at the start of a
// test is safe.
func resetClaimGenerationMintState(t *testing.T) {
	t.Helper()
	claimGenerationHighWaterMu.Lock()
	claimGenerationHighWater = 0
	claimGenerationHighWaterMu.Unlock()
}
