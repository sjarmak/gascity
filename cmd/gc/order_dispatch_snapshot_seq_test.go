package main

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
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
	cr := &CityRuntime{stderr: io.Discard, cfg: &config.City{}}
	fresh := orders.Order{Name: "fresh", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	stale := orders.Order{Name: "stale", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	freshSnapshot := orderSetSnapshot{Orders: []orders.Order{fresh}, Signature: "sig-fresh"}
	staleSnapshot := orderSetSnapshot{Orders: []orders.Order{stale}, Signature: "sig-stale"}

	// The fresher scan (seq 2) applies first, as if it started later than the
	// stale scan below but won the race to acquire orderDispatchMu. Both
	// scans observed the same (zero-value) config version, so the
	// scanCfgVersion guard is not what is under test here.
	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), freshSnapshot, 2, 0, time.Now())
	if !changed {
		t.Fatalf("first apply (seq 2) did not report a change: summary=%q", summary)
	}
	if cr.orderSetSignature != "sig-fresh" || cr.orderSetAppliedSeq != 2 {
		t.Fatalf("after seq 2: signature=%q appliedSeq=%d, want sig-fresh/2", cr.orderSetSignature, cr.orderSetAppliedSeq)
	}

	// The older, slower scan (seq 1) arrives after. It carries a completely
	// different order set, but its seq is at or below the already-applied
	// seq, so it must be rejected rather than reverting the fresher state.
	changed, summary = cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), staleSnapshot, 1, 0, time.Now())
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
// A live orderDispatchConfigVersion exceeding orderDispatchAppliedConfigVersion
// must bypass the unchanged-signature shortcut and rebuild from the current
// cr.cfg, then record that version as applied. cr.cfg/orderDispatchConfigVersion
// are set directly here (not passed as call arguments): applyOrderSetSnapshotLocked
// reads them fresh under serviceStateMu at rebuild time, mirroring what a real
// reload's cr.cfg install does. Each call's scanCfgVersion matches the version
// live at that call's own (simulated) scan time, so the sixth-pass
// consistency guard is a no-op here — this test is about the version-vs-signature
// rebuild trigger, not the scan/apply staleness guard (see
// TestApplyOrderSetSnapshotLockedRejectsTornConfigScan for that).
func TestApplyOrderSetSnapshotLockedNewerConfigVersionAppliesConfigOnlyChange(t *testing.T) {
	oldMax := 1
	oldCfg := &config.City{}
	oldCfg.Orders.MaxDispatchesPerTick = &oldMax
	cr := &CityRuntime{stderr: io.Discard, cfg: oldCfg, orderDispatchConfigVersion: 1}
	order := orders.Order{Name: "reaper", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	snapshot := orderSetSnapshot{Orders: []orders.Order{order}, Signature: "sig-a"}

	if changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), snapshot, 1, 1, time.Now()); !changed {
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
	// byte-identical, but Orders.MaxDispatchesPerTick changed and
	// orderDispatchConfigVersion (2) now exceeds orderDispatchAppliedConfigVersion
	// (1) — exactly what reloadConfigTraced does under serviceStateMu on a real
	// reload. Without that comparison, the unchanged-signature shortcut would
	// silently keep serving the old dispatcher — and its stale
	// maxDispatchesPerTick — forever.
	newMax := 5
	newCfg := &config.City{}
	newCfg.Orders.MaxDispatchesPerTick = &newMax
	cr.cfg = newCfg
	cr.orderDispatchConfigVersion = 2

	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), snapshot, 2, 2, time.Now())
	if !changed {
		t.Fatalf("newer-version apply did not report a change: summary=%q", summary)
	}
	newDispatcher, ok := cr.od.(*memoryOrderDispatcher)
	if !ok {
		t.Fatalf("cr.od = %T after newer-version apply, want *memoryOrderDispatcher", cr.od)
	}
	if newDispatcher == oldDispatcher {
		t.Fatal("newer-version apply did not replace the dispatcher instance")
	}
	if newDispatcher.maxDispatchesPerTick != newMax {
		t.Fatalf("maxDispatchesPerTick after newer-version apply = %d, want %d (the config-only change)",
			newDispatcher.maxDispatchesPerTick, newMax)
	}
	if cr.orderDispatchAppliedConfigVersion != 2 {
		t.Fatalf("orderDispatchAppliedConfigVersion after newer-version apply = %d, want 2", cr.orderDispatchAppliedConfigVersion)
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
	cr := &CityRuntime{stderr: io.Discard, cfg: &config.City{}}
	fresh := orders.Order{Name: "fresh", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	stale := orders.Order{Name: "stale", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	freshSnapshot := orderSetSnapshot{Orders: []orders.Order{fresh}, Signature: "sig-fresh"}
	staleSnapshot := orderSetSnapshot{Orders: []orders.Order{stale}, Signature: "sig-stale"}

	freshApplyTime := time.Now()
	if changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), freshSnapshot, 2, 0, freshApplyTime); !changed {
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
	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), staleSnapshot, 1, 0, staleApplyTime)
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

// TestApplyOrderSetSnapshotLockedRebuildAlwaysUsesLiveConfig pins the
// #2604/#3368 review's fifth-pass MAJOR finding: a rebuild triggered by an
// unrelated order-set signature change must use the LIVE cr.cfg and record
// the LIVE orderDispatchConfigVersion at the moment it actually rebuilds —
// never a cfg/version pair a caller captured earlier and carried across its
// own unlocked disk scan. The second scan here is modeled as having started
// AFTER the concurrent reload already installed the newer cfg (its own
// scanCfgVersion matches the live version), so the sixth-pass consistency
// guard passes and this exercises what the fifth-pass fix actually
// guarantees on the match path: whatever gets built and whatever version
// gets recorded always agree and always reflect the live config, never a
// value carried over from an earlier apply.
func TestApplyOrderSetSnapshotLockedRebuildAlwaysUsesLiveConfig(t *testing.T) {
	oldMax := 1
	oldCfg := &config.City{}
	oldCfg.Orders.MaxDispatchesPerTick = &oldMax
	cr := &CityRuntime{stderr: io.Discard, cfg: oldCfg, orderDispatchConfigVersion: 1}

	first := orders.Order{Name: "first", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	firstSnapshot := orderSetSnapshot{Orders: []orders.Order{first}, Signature: "sig-first"}
	if changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), firstSnapshot, 1, 1, time.Now()); !changed {
		t.Fatalf("initial apply did not report a change: summary=%q", summary)
	}
	if cr.orderDispatchAppliedConfigVersion != 1 {
		t.Fatalf("orderDispatchAppliedConfigVersion after initial apply = %d, want 1", cr.orderDispatchAppliedConfigVersion)
	}

	// A concurrent reload installs a newer cfg (config-only change) and bumps
	// the version — modeled here as a direct field write since this test
	// exercises applyOrderSetSnapshotLocked's own freshness guarantee, not the
	// mutex plumbing around it (see
	// TestApplyOrderSetSnapshotLockedRacesWithConfigReload below for that).
	newMax := 5
	newCfg := &config.City{}
	newCfg.Orders.MaxDispatchesPerTick = &newMax
	cr.cfg = newCfg
	cr.orderDispatchConfigVersion = 2

	// A scan that started after the reload above (its own scanCfgVersion is
	// already 2, matching live) observed an UNRELATED order-set signature
	// change and rebuilds for that reason. The fix under test is that this
	// rebuild picks up newCfg and correctly records version 2 as applied, not
	// oldCfg with version 1 falsely left stale or (worse) newCfg baked in
	// while still reporting version 1.
	second := orders.Order{Name: "second", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	secondSnapshot := orderSetSnapshot{Orders: []orders.Order{second}, Signature: "sig-second"}
	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), secondSnapshot, 2, 2, time.Now())
	if !changed {
		t.Fatalf("signature-change apply did not report a change: summary=%q", summary)
	}
	if summary == "unchanged" {
		t.Fatalf("summary = %q, want a real change summary for a signature change", summary)
	}
	dispatcher, ok := cr.od.(*memoryOrderDispatcher)
	if !ok {
		t.Fatalf("cr.od = %T, want *memoryOrderDispatcher", cr.od)
	}
	if dispatcher.maxDispatchesPerTick != newMax {
		t.Fatalf("maxDispatchesPerTick after signature-change rebuild = %d, want %d (the live config, not a stale caller-carried one)",
			dispatcher.maxDispatchesPerTick, newMax)
	}
	if cr.orderDispatchAppliedConfigVersion != 2 {
		t.Fatalf("orderDispatchAppliedConfigVersion = %d after a rebuild that used the live version 2, want 2", cr.orderDispatchAppliedConfigVersion)
	}
}

// TestApplyOrderSetSnapshotLockedRejectsTornConfigScan pins the #2604/#3368
// review's sixth-pass MAJOR finding: scanOrderSetSnapshotFS bakes
// cfg-dependent fields (formula layers, skips, timezone, per-order overrides)
// into snapshot.Orders using the cfg in effect AT SCAN TIME, which runs
// unlocked before orderDispatchMu is acquired. If a concurrent reload
// installs a newer cr.cfg/orderDispatchConfigVersion in that window,
// combining the STALE-generation snapshot.Orders with the LIVE cfg read at
// apply time would build a torn dispatcher — e.g. an order the new config
// disabled via an override remains enabled because the stale scan never saw
// it. The scanCfgVersion guard rejects any scan whose captured version no
// longer matches the live cr.orderDispatchConfigVersion, leaving all applied
// order-set and version state untouched so a later, self-consistent scan
// (which will observe the newer version and scan under it) converges
// instead of installing the mix.
func TestApplyOrderSetSnapshotLockedRejectsTornConfigScan(t *testing.T) {
	oldCfg := &config.City{}
	cr := &CityRuntime{stderr: io.Discard, cfg: oldCfg, orderDispatchConfigVersion: 1}

	first := orders.Order{Name: "first", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	firstSnapshot := orderSetSnapshot{Orders: []orders.Order{first}, Signature: "sig-first"}
	if changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), firstSnapshot, 1, 1, time.Now()); !changed {
		t.Fatalf("initial apply did not report a change: summary=%q", summary)
	}
	baselineDispatcher := cr.od
	if cr.orderDispatchAppliedConfigVersion != 1 {
		t.Fatalf("orderDispatchAppliedConfigVersion after initial apply = %d, want 1", cr.orderDispatchAppliedConfigVersion)
	}

	// A concurrent reload installs a newer cfg and bumps the version AFTER
	// the scan below captured scanCfgVersion=1 but BEFORE it reaches this
	// call — exactly the window scanOrderSetSnapshotFS's unlocked disk read
	// leaves open.
	cr.cfg = &config.City{}
	cr.orderDispatchConfigVersion = 2

	// The stale-generation scan carries an order set that was built under the
	// OLD cfg (e.g. one the new config would have disabled via an override,
	// but the stale scan never saw that) plus its own captured
	// scanCfgVersion (1) — no longer matching the live version (2).
	torn := orders.Order{Name: "torn", Trigger: "cooldown", Interval: "1m", Exec: "true"}
	tornSnapshot := orderSetSnapshot{Orders: []orders.Order{torn}, Signature: "sig-torn"}
	changed, summary := cr.applyOrderSetSnapshotLocked(context.Background(), t.TempDir(), tornSnapshot, 2, 1, time.Now())
	if changed {
		t.Fatalf("torn scan (scanCfgVersion=1, live=2) was applied instead of rejected: summary=%q", summary)
	}
	if summary != "stale-config-scan" {
		t.Fatalf("summary = %q, want %q", summary, "stale-config-scan")
	}
	if cr.orderSetSignature != "sig-first" {
		t.Fatalf("orderSetSignature = %q after a rejected torn scan, want unchanged %q", cr.orderSetSignature, "sig-first")
	}
	if len(cr.orderSet) != 1 || cr.orderSet[0].Name != "first" {
		t.Fatalf("orderSet = %#v after a rejected torn scan, want the baseline order still installed", cr.orderSet)
	}
	if cr.orderDispatchAppliedConfigVersion != 1 {
		t.Fatalf("orderDispatchAppliedConfigVersion = %d after a rejected torn scan, want unchanged 1", cr.orderDispatchAppliedConfigVersion)
	}
	if cr.od != baselineDispatcher {
		t.Fatal("cr.od was replaced by a rejected torn scan")
	}
}

// TestApplyOrderSetSnapshotLockedRacesWithConfigReload races
// applyOrderSetSnapshotLocked's internal cr.cfg/orderDispatchConfigVersion
// read against a concurrent serviceStateMu writer standing in for
// reloadConfigTraced's cr.cfg install, mirroring
// TestCityRuntimeClassAccessorsRaceWithConfigReload in class_store_test.go.
// Run with -race: if the read inside applyOrderSetSnapshotLocked ever bypassed
// serviceStateMu, this test would flag a data race. The apply goroutine
// captures its own scanCfgVersion under serviceStateMu.RLock() immediately
// before each call, mirroring rescanOrderDispatcher's real capture pattern,
// and counts its successful applies: a legal schedule where the writer
// closes stop before the apply goroutine ever runs would exercise no
// synchronization at all, so the count is asserted nonzero after wg.Wait()
// to keep this a reliable regression guard rather than a possibly-vacuous
// one (#2604/#3368 review followup, sixth-pass MINOR finding).
func TestApplyOrderSetSnapshotLockedRacesWithConfigReload(t *testing.T) {
	cr := &CityRuntime{stderr: io.Discard, cfg: &config.City{}}
	cityRoot := t.TempDir()
	order := orders.Order{Name: "reaper", Trigger: "cooldown", Interval: "1m", Exec: "true"}

	var applies atomic.Int64
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			cr.serviceStateMu.RLock()
			scanCfgVersion := cr.orderDispatchConfigVersion
			cr.serviceStateMu.RUnlock()
			snapshot := orderSetSnapshot{Orders: []orders.Order{order}, Signature: fmt.Sprintf("sig-%d", i)}
			cr.orderDispatchMu.Lock()
			cr.applyOrderSetSnapshotLocked(context.Background(), cityRoot, snapshot, uint64(i+1), scanCfgVersion, time.Now())
			cr.orderDispatchMu.Unlock()
			applies.Add(1)
			runtime.Gosched()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			cr.serviceStateMu.Lock()
			cr.cfg = &config.City{}
			cr.orderDispatchConfigVersion++
			cr.serviceStateMu.Unlock()
			runtime.Gosched()
		}
		close(stop)
	}()

	wg.Wait()

	if applies.Load() == 0 {
		t.Fatal("the apply goroutine never ran before the writer closed stop; this schedule exercised no synchronization and is not a reliable regression guard")
	}
}
