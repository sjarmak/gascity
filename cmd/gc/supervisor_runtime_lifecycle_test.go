package main

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/testutil"
)

func TestManagedDoltSupervisorAttemptCancelsEachExitedRuntime(t *testing.T) {
	const attempts = 5
	var live atomic.Int32
	var stderr bytes.Buffer

	for attempt := 0; attempt < attempts; attempt++ {
		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		live.Add(1)
		go func() {
			<-ctx.Done()
			live.Add(-1)
			close(stopped)
		}()

		runManagedCityAttempt(ctx, cancel, func() bool { return false }, &stderr, "test-city", func() {
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
	if got := strings.Count(stderr.String(), "runtime exited before startup readiness"); got != attempts {
		t.Fatalf("retry warnings = %d, want %d; stderr=%q", got, attempts, stderr.String())
	}
}

func TestManagedDoltSupervisorAttemptCancelsOnPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("runManagedCityAttempt did not propagate panic")
			}
		}()
		runManagedCityAttempt(ctx, cancel, func() bool { return false }, &bytes.Buffer{}, "test-city", func() { panic("boom") })
	}()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("managed city context remains live after panic")
	}
}

func TestManagedDoltCacheReconcilerIdentityIncludesStoreScope(t *testing.T) {
	if got, want := cacheReconcilerIdentity("worker", "rig:alpha"), "worker/store:rig:alpha"; got != want {
		t.Fatalf("cacheReconcilerIdentity() = %q, want %q", got, want)
	}
	if city, rig := cacheReconcilerIdentity("worker", "city:test"), cacheReconcilerIdentity("worker", "rig:test"); city == rig {
		t.Fatalf("city and rig cache identities collide: %q", city)
	}
}

func TestManagedDoltCacheReconcilerStaggersStoresWithSharedPrefix(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := beads.NewCachingStoreForTestWithPrefix(beads.NewMemStore(), "gc", nil)
	second := beads.NewCachingStoreForTestWithPrefix(beads.NewMemStore(), "gc", nil)
	primeThenStartReconciler(ctx, first, "worker", "rig:alpha")
	primeThenStartReconciler(ctx, second, "worker", "rig:beta")

	if first.IDPrefix() != second.IDPrefix() {
		t.Fatalf("test premise: prefixes differ: %q vs %q", first.IDPrefix(), second.IDPrefix())
	}
	if first.Stats().StaggerOffsetMs == second.Stats().StaggerOffsetMs {
		t.Fatalf("co-primed stores with shared prefix synchronized at %dms", first.Stats().StaggerOffsetMs)
	}
}
