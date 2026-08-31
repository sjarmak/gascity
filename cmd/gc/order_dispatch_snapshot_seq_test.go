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
	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), cfg, freshSnapshot, 2, false, time.Now())
	if !changed {
		t.Fatalf("first apply (seq 2) did not report a change: summary=%q", summary)
	}
	if cr.orderSetSignature != "sig-fresh" || cr.orderSetAppliedSeq != 2 {
		t.Fatalf("after seq 2: signature=%q appliedSeq=%d, want sig-fresh/2", cr.orderSetSignature, cr.orderSetAppliedSeq)
	}

	// The older, slower scan (seq 1) arrives after. It carries a completely
	// different order set, but its seq is at or below the already-applied
	// seq, so it must be rejected rather than reverting the fresher state.
	changed, summary = cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), cfg, staleSnapshot, 1, false, time.Now())
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

// TestApplyOrderSetSnapshotLockedForceRebuildAppliesConfigOnlyChange pins the
// #2604/#3368 review followup's Finding #1: config-only fields (e.g.
// Orders.MaxDispatchesPerTick) are baked into the dispatcher at construction
// time from cfg but are not part of any orders.Order field the order-set
// signature hashes, so a config-only change produces an unchanged signature.
// If the one-time full-reload rebuild attempt loses the orderDispatchMu race,
// no signature-gated rescan would otherwise ever retry it. forceRebuild (set
// via orderDispatchConfigRebuildPending) must bypass the unchanged-signature
// shortcut and rebuild from the caller's freshly snapshotted cfg.
func TestApplyOrderSetSnapshotLockedForceRebuildAppliesConfigOnlyChange(t *testing.T) {
	cr := &CityRuntime{stderr: io.Discard}
	order := orders.Order{Name: "reaper", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	snapshot := orderSetSnapshot{Orders: []orders.Order{order}, Signature: "sig-a"}

	oldMax := 1
	oldCfg := &config.City{}
	oldCfg.Orders.MaxDispatchesPerTick = &oldMax
	if changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), oldCfg, snapshot, 1, false, time.Now()); !changed {
		t.Fatalf("initial apply did not report a change: summary=%q", summary)
	}
	oldDispatcher, ok := cr.od.(*memoryOrderDispatcher)
	if !ok {
		t.Fatalf("cr.od = %T, want *memoryOrderDispatcher", cr.od)
	}
	if oldDispatcher.maxDispatchesPerTick != oldMax {
		t.Fatalf("initial maxDispatchesPerTick = %d, want %d", oldDispatcher.maxDispatchesPerTick, oldMax)
	}

	// A config-only change: the order set (and therefore its signature) is
	// byte-identical, but Orders.MaxDispatchesPerTick changed. Without
	// forceRebuild, the unchanged-signature shortcut would silently keep
	// serving the old dispatcher — and its stale maxDispatchesPerTick —
	// forever.
	newMax := 5
	newCfg := &config.City{}
	newCfg.Orders.MaxDispatchesPerTick = &newMax

	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), newCfg, snapshot, 2, true, time.Now())
	if !changed {
		t.Fatalf("forceRebuild apply did not report a change: summary=%q", summary)
	}
	newDispatcher, ok := cr.od.(*memoryOrderDispatcher)
	if !ok {
		t.Fatalf("cr.od = %T after forceRebuild, want *memoryOrderDispatcher", cr.od)
	}
	if newDispatcher == oldDispatcher {
		t.Fatal("forceRebuild did not replace the dispatcher instance")
	}
	if newDispatcher.maxDispatchesPerTick != newMax {
		t.Fatalf("maxDispatchesPerTick after forceRebuild = %d, want %d (the config-only change)",
			newDispatcher.maxDispatchesPerTick, newMax)
	}
	if cr.orderDispatchConfigRebuildPending.Load() {
		t.Fatal("orderDispatchConfigRebuildPending still set after a successful forced rebuild")
	}
}
