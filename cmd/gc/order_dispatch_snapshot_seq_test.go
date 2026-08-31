package main

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/orders"
)

// TestApplyOrderSetSnapshotLockedRejectsStaleScan pins the #2604/#3368 review
// followup fixing a TOCTOU race: rescanOrderDispatcher,
// rescanOrderDispatcherForReload, and the full config-reload branch all
// snapshot the order set from disk BEFORE acquiring orderDispatchMu, so two
// concurrent scans can finish (and therefore acquire the lock) in the
// opposite order from which they started scanning. A seq captured before the
// unlocked scan and compared under the lock is what lets
// applyOrderSetSnapshotLocked detect and drop the older one instead of
// letting it clobber a fresher applied state — e.g. resurrecting a removed
// order and immediately making it eligible for dispatch again.
func TestApplyOrderSetSnapshotLockedRejectsStaleScan(t *testing.T) {
	cr := &CityRuntime{stderr: io.Discard}
	cfg := &config.City{}
	fresh := orders.Order{Name: "fresh", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	stale := orders.Order{Name: "stale", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	freshSnapshot := orderSetSnapshot{Orders: []orders.Order{fresh}, Signature: "sig-fresh"}
	staleSnapshot := orderSetSnapshot{Orders: []orders.Order{stale}, Signature: "sig-stale"}

	// The fresher scan (seq 2) applies first, as if it started later than the
	// stale scan below but won the race to acquire orderDispatchMu.
	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), cfg, freshSnapshot, 2, 0, time.Now())
	if !changed {
		t.Fatalf("first apply (seq 2) did not report a change: summary=%q", summary)
	}
	if cr.orderSetSignature != "sig-fresh" || cr.orderSetAppliedSeq != 2 {
		t.Fatalf("after seq 2: signature=%q appliedSeq=%d, want sig-fresh/2", cr.orderSetSignature, cr.orderSetAppliedSeq)
	}

	// The older, slower scan (seq 1) arrives after. It carries a completely
	// different order set, but its seq is at or below the already-applied
	// seq, so it must be rejected rather than reverting the fresher state.
	changed, summary = cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), cfg, staleSnapshot, 1, 0, time.Now())
	if changed {
		t.Fatalf("stale scan (seq 1) was applied over a fresher seq 2 result: summary=%q", summary)
	}
	if summary != "stale-scan" {
		t.Fatalf("summary = %q, want %q", summary, "stale-scan")
	}
	if cr.orderSetSignature != "sig-fresh" || cr.orderSetAppliedSeq != 2 {
		t.Fatalf("stale scan corrupted applied state: signature=%q appliedSeq=%d, want sig-fresh/2 (unchanged)",
			cr.orderSetSignature, cr.orderSetAppliedSeq)
	}
	if len(cr.orderSet) != 1 || cr.orderSet[0].Name != "fresh" {
		t.Fatalf("orderSet = %#v, want the fresher scan's order still installed", cr.orderSet)
	}
}

// TestApplyOrderSetSnapshotLockedNewerConfigVersionAppliesConfigOnlyChange pins
// the #2604/#3368 review followup's Finding #1: config-only fields (e.g.
// Orders.MaxDispatchesPerTick) are baked into the dispatcher at construction
// time from cfg but are not part of any orders.Order field the order-set
// signature hashes, so a config-only change produces an unchanged signature.
// cfgVersion exceeding orderDispatchAppliedConfigVersion must bypass the
// unchanged-signature shortcut and rebuild from the caller's freshly
// snapshotted cfg, then record that version as applied.
func TestApplyOrderSetSnapshotLockedNewerConfigVersionAppliesConfigOnlyChange(t *testing.T) {
	cr := &CityRuntime{stderr: io.Discard}
	order := orders.Order{Name: "reaper", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	snapshot := orderSetSnapshot{Orders: []orders.Order{order}, Signature: "sig-a"}

	oldMax := 1
	oldCfg := &config.City{}
	oldCfg.Orders.MaxDispatchesPerTick = &oldMax
	if changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), oldCfg, snapshot, 1, 1, time.Now()); !changed {
		t.Fatalf("initial apply did not report a change: summary=%q", summary)
	}
	if cr.orderDispatchAppliedConfigVersion != 1 {
		t.Fatalf("orderDispatchAppliedConfigVersion after initial apply = %d, want 1", cr.orderDispatchAppliedConfigVersion)
	}
	oldDispatcher, ok := cr.od.(*memoryOrderDispatcher)
	if !ok {
		t.Fatalf("cr.od = %T, want *memoryOrderDispatcher", cr.od)
	}
	if oldDispatcher.maxDispatchesPerTick != oldMax {
		t.Fatalf("initial maxDispatchesPerTick = %d, want %d", oldDispatcher.maxDispatchesPerTick, oldMax)
	}

	// A config-only change: the order set (and therefore its signature) is
	// byte-identical, but Orders.MaxDispatchesPerTick changed and cfgVersion
	// (2) now exceeds orderDispatchAppliedConfigVersion (1). Without that
	// comparison, the unchanged-signature shortcut would silently keep
	// serving the old dispatcher — and its stale maxDispatchesPerTick —
	// forever.
	newMax := 5
	newCfg := &config.City{}
	newCfg.Orders.MaxDispatchesPerTick = &newMax

	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), newCfg, snapshot, 2, 2, time.Now())
	if !changed {
		t.Fatalf("newer-cfgVersion apply did not report a change: summary=%q", summary)
	}
	newDispatcher, ok := cr.od.(*memoryOrderDispatcher)
	if !ok {
		t.Fatalf("cr.od = %T after newer-cfgVersion apply, want *memoryOrderDispatcher", cr.od)
	}
	if newDispatcher == oldDispatcher {
		t.Fatal("newer-cfgVersion apply did not replace the dispatcher instance")
	}
	if newDispatcher.maxDispatchesPerTick != newMax {
		t.Fatalf("maxDispatchesPerTick after newer-cfgVersion apply = %d, want %d (the config-only change)",
			newDispatcher.maxDispatchesPerTick, newMax)
	}
	if cr.orderDispatchAppliedConfigVersion != 2 {
		t.Fatalf("orderDispatchAppliedConfigVersion after newer-cfgVersion apply = %d, want 2", cr.orderDispatchAppliedConfigVersion)
	}
}

// TestApplyOrderSetSnapshotLockedStaleScanDoesNotAdvanceRescanTimestamp pins
// the #2604/#3368 review's third-pass followup MINOR finding: a rejected
// stale scan must leave orderRescanLast untouched, not just the applied order
// set. rescanOrderDispatcherIfDue gates on time.Since(orderRescanLast), so
// advancing that timestamp from a discarded scan would delay the next
// legitimate rescan by orderRescanInterval for no reason — the discarded scan
// contributed nothing.
func TestApplyOrderSetSnapshotLockedStaleScanDoesNotAdvanceRescanTimestamp(t *testing.T) {
	cr := &CityRuntime{stderr: io.Discard}
	cfg := &config.City{}
	fresh := orders.Order{Name: "fresh", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	stale := orders.Order{Name: "stale", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	freshSnapshot := orderSetSnapshot{Orders: []orders.Order{fresh}, Signature: "sig-fresh"}
	staleSnapshot := orderSetSnapshot{Orders: []orders.Order{stale}, Signature: "sig-stale"}

	freshApplyTime := time.Now()
	if changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), cfg, freshSnapshot, 2, 0, freshApplyTime); !changed {
		t.Fatalf("first apply (seq 2) did not report a change: summary=%q", summary)
	}
	if cr.orderRescanLast != freshApplyTime {
		t.Fatalf("orderRescanLast = %v after the applied scan, want %v", cr.orderRescanLast, freshApplyTime)
	}

	// The stale scan (seq 1) arrives later in wall-clock time but must be
	// rejected on seq alone. Its `now` is strictly after freshApplyTime, so if
	// applyOrderSetSnapshotLocked wrote orderRescanLast before checking
	// staleness, this assertion would catch it advancing.
	staleApplyTime := freshApplyTime.Add(time.Minute)
	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), cfg, staleSnapshot, 1, 0, staleApplyTime)
	if changed {
		t.Fatalf("stale scan (seq 1) was applied over a fresher seq 2 result: summary=%q", summary)
	}
	if summary != "stale-scan" {
		t.Fatalf("summary = %q, want %q", summary, "stale-scan")
	}
	if cr.orderRescanLast != freshApplyTime {
		t.Fatalf("orderRescanLast = %v after a rejected stale scan, want unchanged %v", cr.orderRescanLast, freshApplyTime)
	}
}

// TestApplyOrderSetSnapshotLockedUnrelatedChangeDoesNotAdvancePastItsOwnVersion
// pins the #2604/#3368 review's fourth-pass followup MAJOR finding: a
// rebuild triggered by an unrelated order-set signature change must record
// ONLY the cfgVersion it actually carried, never a higher one some other
// (not-yet-applied) reload has since claimed. Concretely: a concurrent
// reload has bumped orderDispatchConfigVersion to a newer value (2) than
// this scan's own cfg snapshot (which is still 1); this scan rebuilds for an
// unrelated reason with cfgVersion 1. Recording anything higher than 1 here
// would falsely mark version 2's config-only change as already applied, the
// exact lost-update the boolean orderDispatchConfigRebuildPending flag this
// design replaces could not prevent (its Store(false)/Store(true) pair had
// no way to represent "two different, still-distinguishable versions are in
// flight" — only a single before/after bit).
func TestApplyOrderSetSnapshotLockedUnrelatedChangeDoesNotAdvancePastItsOwnVersion(t *testing.T) {
	cr := &CityRuntime{stderr: io.Discard}
	cfg := &config.City{}
	first := orders.Order{Name: "first", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	second := orders.Order{Name: "second", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	firstSnapshot := orderSetSnapshot{Orders: []orders.Order{first}, Signature: "sig-first"}
	secondSnapshot := orderSetSnapshot{Orders: []orders.Order{second}, Signature: "sig-second"}

	if changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), cfg, firstSnapshot, 1, 1, time.Now()); !changed {
		t.Fatalf("initial apply did not report a change: summary=%q", summary)
	}
	if cr.orderDispatchAppliedConfigVersion != 1 {
		t.Fatalf("orderDispatchAppliedConfigVersion after initial apply = %d, want 1", cr.orderDispatchAppliedConfigVersion)
	}

	// This scan sees an unrelated order-set signature change and rebuilds for
	// that reason alone. Its own cfgVersion (1) predates a concurrent
	// reload's config version 2, which this scan never observed.
	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), cfg, secondSnapshot, 2, 1, time.Now())
	if !changed {
		t.Fatalf("signature-change apply did not report a change: summary=%q", summary)
	}
	if summary == "unchanged" {
		t.Fatalf("summary = %q, want a real change summary for a signature change", summary)
	}
	if cr.orderDispatchAppliedConfigVersion != 1 {
		t.Fatalf("orderDispatchAppliedConfigVersion = %d after a rebuild that only carried cfgVersion 1, want 1 (must not claim a newer version it never saw)",
			cr.orderDispatchAppliedConfigVersion)
	}

	// A later scan capturing the real, newer config version (2) must still
	// see it as owed and rebuild for it — proving the unrelated rebuild above
	// did not silently make it unreachable.
	changed, summary = cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), cfg, secondSnapshot, 3, 2, time.Now())
	if !changed {
		t.Fatalf("cfgVersion 2 was not detected as still owed: summary=%q", summary)
	}
	if cr.orderDispatchAppliedConfigVersion != 2 {
		t.Fatalf("orderDispatchAppliedConfigVersion = %d after applying cfgVersion 2, want 2", cr.orderDispatchAppliedConfigVersion)
	}
}

// TestApplyOrderSetSnapshotLockedConcurrentReloadVersionNotLost reproduces the
// exact interleaving the #2604/#3368 fourth-pass review flagged against the
// boolean orderDispatchConfigRebuildPending design this version counter
// replaces: an in-flight rebuild captures an OLDER cfgVersion and holds
// orderDispatchMu while it works; a newer reload bumps the config version
// further and gives up on the mutex (busy) while the old rebuild is still
// running; the old rebuild then finishes. Under the removed boolean design,
// the old rebuild's completion did `Store(false)` unconditionally, erasing
// the newer reload's `Store(true)` — its still-owed config-only change
// became permanently unreachable (no signature-gated rescan would ever
// retry it). The version counter cannot do this: the old rebuild can only
// ever record the version it actually carried, never a fresher one it never
// saw, so the newer version is still visibly owed to the next scan.
//
// This is a genuine concurrency test (real goroutines contending on
// orderDispatchMu), not a same-goroutine call-order simulation, because the
// prior review noted the earlier pending-flag test did not exercise a real
// concurrent reload.
func TestApplyOrderSetSnapshotLockedConcurrentReloadVersionNotLost(t *testing.T) {
	cr := &CityRuntime{stderr: io.Discard}
	cityRoot := t.TempDir()
	cfg := &config.City{}
	order := orders.Order{Name: "reaper", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	snapshot := orderSetSnapshot{Orders: []orders.Order{order}, Signature: "sig-a"}

	// Seed: cfgVersion 1 already applied.
	if changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), cityRoot, cfg, snapshot, 1, 1, time.Now()); !changed {
		t.Fatalf("seed apply did not report a change: summary=%q", summary)
	}

	// "Old rebuild": captured cfgVersion 2 before starting, and holds
	// orderDispatchMu (acquired here, on the test goroutine, standing in for
	// the lane/reload goroutine that would normally acquire it) while a
	// newer reload observes the mutex is busy.
	cr.orderDispatchMu.Lock()
	reloadObservedBusy := make(chan struct{})
	rebuildDone := make(chan struct{})
	go func() {
		defer close(rebuildDone)
		defer cr.orderDispatchMu.Unlock()
		<-reloadObservedBusy
		changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), cityRoot, cfg, snapshot, 2, 2, time.Now())
		if !changed {
			t.Errorf("old-rebuild apply (cfgVersion 2) did not report a change: summary=%q", summary)
		}
	}()

	// "Newer reload": in the real code this is reloadConfigTraced bumping
	// cr.orderDispatchConfigVersion to 3 under serviceStateMu (independent of
	// orderDispatchMu) and then finding orderDispatchMu busy — the exact
	// branch that, under the removed boolean design, stored a pending flag
	// representing cfgVersion 3. That branch does nothing in the version
	// design (see reloadConfigTraced): the version bump alone is enough.
	if cr.orderDispatchMu.TryLock() {
		cr.orderDispatchMu.Unlock()
		t.Fatal("acquired orderDispatchMu while the old rebuild goroutine should still be holding it")
	}
	close(reloadObservedBusy)
	<-rebuildDone

	if cr.orderDispatchAppliedConfigVersion != 2 {
		t.Fatalf("orderDispatchAppliedConfigVersion = %d after the old rebuild (cfgVersion 2) completed, want 2 (must not claim the newer cfgVersion 3 it never saw)",
			cr.orderDispatchAppliedConfigVersion)
	}

	// The newer reload's cfgVersion (3) is still owed: a subsequent scan
	// must detect and apply it, proving the old rebuild's completion did not
	// erase it.
	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), cityRoot, cfg, snapshot, 3, 3, time.Now())
	if !changed {
		t.Fatalf("cfgVersion 3 was not detected as still owed after the stale rebuild completed: summary=%q", summary)
	}
	if cr.orderDispatchAppliedConfigVersion != 3 {
		t.Fatalf("orderDispatchAppliedConfigVersion = %d after applying cfgVersion 3, want 3", cr.orderDispatchAppliedConfigVersion)
	}
}
