package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/testutil"
)

func TestRunManagedCityAttemptCancelsEachExitedRuntime(t *testing.T) {
	const attempts = 5
	var live atomic.Int32

	for attempt := 0; attempt < attempts; attempt++ {
		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		live.Add(1)
		go func() {
			<-ctx.Done()
			live.Add(-1)
			close(stopped)
		}()

		runManagedCityAttempt(cancel, func() {
			if got := live.Load(); got != 1 {
				t.Fatalf("attempt %d: live reconciler sets = %d, want exactly 1", attempt+1, got)
			}
		})

		select {
		case <-stopped:
		case <-time.After(testutil.GoroutineRaceTimeout):
			t.Fatalf("attempt %d: reconciler set did not stop after runtime exit", attempt+1)
		}
		if got := live.Load(); got != 0 {
			t.Fatalf("after attempt %d: live reconciler sets = %d, want 0", attempt+1, got)
		}
	}
}

func TestRunManagedCityAttemptCancelsOnPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("runManagedCityAttempt did not propagate panic")
			}
		}()
		runManagedCityAttempt(cancel, func() { panic("boom") })
	}()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("managed city context remains live after panic")
	}
}

func TestCacheReconcilerIdentityIncludesStorePrefix(t *testing.T) {
	if got, want := cacheReconcilerIdentity("worker", "gc"), "worker/store:gc"; got != want {
		t.Fatalf("cacheReconcilerIdentity() = %q, want %q", got, want)
	}
	if city, rig := cacheReconcilerIdentity("worker", ""), cacheReconcilerIdentity("worker", "gc"); city == rig {
		t.Fatalf("city and rig cache identities collide: %q", city)
	}
}
