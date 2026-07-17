package sling

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/formula"
	"github.com/gastownhall/gascity/internal/graphv2"
	"github.com/gastownhall/gascity/internal/molecule"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/sourceworkflow"
)

// launchForBeadTarget runs the dispatcher's bead-target launch: normalize the
// target into an input convoy, then instantiate through the chokepoint. Every
// existing chokepoint test hands in a convoy it made itself, which skips
// normalization — the one step that decided the RootKey in gc-28jm.
func launchForBeadTarget(t *testing.T, formulaDir, formulaName, targetID string, a config.Agent, deps SlingDeps) string {
	t.Helper()
	inv, err := graphv2.PrepareInvocation(context.Background(), deps.Store, formulaName, []string{formulaDir}, targetID, nil)
	if err != nil {
		t.Fatalf("PrepareInvocation(%s): %v", targetID, err)
	}
	res, err := InstantiateSlingFormula(context.Background(), formulaName, []string{formulaDir}, molecule.Options{Vars: inv.Vars}, "", "default", "", a, deps)
	if err != nil {
		t.Fatalf("InstantiateSlingFormula(%s): %v", targetID, err)
	}
	return res.RootID
}

// TestLaunchWorkflowBeadTargetDoubleSlingCreatesOneRoot is the gc-28jm
// regression. Production dispatched mol-focus-review at gc-89e twice, 33s
// apart, and got roots gc-5wse and gc-r7c6 — keys identical but for their
// leading convoy segment, because each dispatch minted its own input convoy.
// Both guards behaved correctly; they were handed two different keys. Sequential
// is the real shape here: the first root had been committed for 33 seconds.
func TestLaunchWorkflowBeadTargetDoubleSlingCreatesOneRoot(t *testing.T) {
	formulaDir := t.TempDir()
	writeGraphV2ConvoyFormula(t, formulaDir)
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath = t.TempDir()
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	target, err := deps.Store.Create(beads.Bead{Title: "work bead", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}

	first := launchForBeadTarget(t, formulaDir, "graph-work", target.ID, a, deps)
	afterFirst := beadCount(t, deps.Store)
	second := launchForBeadTarget(t, formulaDir, "graph-work", target.ID, a, deps)

	if first != second {
		t.Fatalf("root = %q then %q, want the second dispatch to reuse the first root (I10)", first, second)
	}
	if live := liveGraphV2Roots(t, deps.Store); len(live) != 1 {
		t.Fatalf("live graph roots = %d, want exactly one for one target+formula (I1); roots=%+v", len(live), live)
	}
	// The loser must report the existing root and write nothing: no second root,
	// no duplicate step beads, and no orphan input convoy.
	if afterSecond := beadCount(t, deps.Store); afterSecond != afterFirst {
		t.Fatalf("bead count = %d after the second dispatch, want %d unchanged; the loser materialized beads instead of reusing the root", afterSecond, afterFirst)
	}
}

// beadCount returns every bead in store across both tiers.
func beadCount(t *testing.T, store beads.Store) int {
	t.Helper()
	all, err := store.List(beads.ListQuery{
		IncludeClosed: true,
		AllowScan:     true,
		TierMode:      beads.TierBoth,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return len(all)
}

// TestLaunchWorkflowBeadTargetConcurrentSlingCreatesOneRoot covers the
// simultaneous shape: callers that all miss the convoy lookup must still land on
// one root, since converging on a convoy is what puts them behind one file lock.
func TestLaunchWorkflowBeadTargetConcurrentSlingCreatesOneRoot(t *testing.T) {
	formulaDir := t.TempDir()
	writeGraphV2ConvoyFormula(t, formulaDir)
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath = t.TempDir()
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	target, err := deps.Store.Create(beads.Bead{Title: "work bead", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}

	const n = 6
	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i] = launchForBeadTarget(t, formulaDir, "graph-work", target.ID, a, deps)
		}(i)
	}
	wg.Wait()

	for i := range ids {
		if ids[i] != ids[0] {
			t.Fatalf("launch %d root = %q, want the shared winner %q", i, ids[i], ids[0])
		}
	}
	if live := liveGraphV2Roots(t, deps.Store); len(live) != 1 {
		t.Fatalf("live graph roots = %d, want exactly one (I1); roots=%+v", len(live), live)
	}
}

// TestLaunchWorkflowBeadTargetDistinctLaunchesStillAllowed keeps the gate from
// over-reaching: one convoy per target must not collapse separate formulas into
// one root, strand a different target, or block a retry once the root is closed.
func TestLaunchWorkflowBeadTargetDistinctLaunchesStillAllowed(t *testing.T) {
	formulaDir := t.TempDir()
	writeGraphV2ConvoyFormula(t, formulaDir)
	writeNamedGraphV2ConvoyFormula(t, formulaDir, "graph-other")
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath = t.TempDir()
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	target, err := deps.Store.Create(beads.Bead{Title: "work bead", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := deps.Store.Create(beads.Bead{Title: "other bead", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}

	root := launchForBeadTarget(t, formulaDir, "graph-work", target.ID, a, deps)

	if got := launchForBeadTarget(t, formulaDir, "graph-other", target.ID, a, deps); got == root {
		t.Fatalf("a second formula at the same target reused root %q; different formulas are independent (I6)", got)
	}
	if got := launchForBeadTarget(t, formulaDir, "graph-work", other.ID, a, deps); got == root {
		t.Fatalf("a different target reused root %q; distinct targets are independent (I6)", got)
	}

	// A closed root is spent, not a permanent veto on the same target+formula.
	if err := deps.Store.Close(root); err != nil {
		t.Fatalf("Close(%s): %v", root, err)
	}
	if got := launchForBeadTarget(t, formulaDir, "graph-work", target.ID, a, deps); got == root || got == "" {
		t.Fatalf("relaunch after close returned %q, want a fresh root (I7)", got)
	}
}

// liveGraphV2Roots returns the non-closed graph.v2 workflow roots in store.
func liveGraphV2Roots(t *testing.T, store beads.Store) []beads.Bead {
	t.Helper()
	roots, err := store.ListByMetadata(map[string]string{"gc.formula_contract": "graph.v2"}, 0, beads.WithBothTiers)
	if err != nil {
		t.Fatalf("ListByMetadata: %v", err)
	}
	var live []beads.Bead
	for _, root := range roots {
		if sourceworkflow.IsWorkflowRoot(root) && root.Status != "closed" {
			live = append(live, root)
		}
	}
	return live
}

// TestLaunchWorkflowDuplicateAttemptReturnsSameLiveRoot proves the single
// dedupe guard: concurrent launches that resolve to the same RootKey converge
// on exactly one live root, and every loser receives the winner's root as an
// idempotent success (invariants I1 + I10) — never a second root, never an
// error. This is the #1053 "duplicate molecules" window closed.
func TestLaunchWorkflowDuplicateAttemptReturnsSameLiveRoot(t *testing.T) {
	formulaDir := t.TempDir()
	writeGraphV2ConvoyFormula(t, formulaDir)
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath = t.TempDir()
	convoy, err := deps.Store.Create(beads.Bead{Title: "input", Type: "convoy"})
	if err != nil {
		t.Fatal(err)
	}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	opts := molecule.Options{Vars: map[string]string{"convoy_id": convoy.ID}}

	const n = 6
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := InstantiateSlingFormula(context.Background(), "graph-work", []string{formulaDir}, opts, "", "default", "", a, deps)
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = res.RootID
		}(i)
	}
	wg.Wait()

	first := ids[0]
	for i, err := range errs {
		if err != nil {
			t.Fatalf("launch %d errored (a duplicate attempt must be an idempotent success, not an error): %v", i, err)
		}
		if ids[i] != first {
			t.Fatalf("launch %d RootID = %q, want the shared winner root %q (I10)", i, ids[i], first)
		}
	}
	if live := liveGraphV2Roots(t, deps.Store); len(live) != 1 {
		t.Fatalf("live graph roots = %d, want exactly one (I1); roots=%+v", len(live), live)
	}
}

// TestLaunchWorkflowUsesCrossProcessFileLock proves the dedupe guard is the
// cross-process sourceworkflow file lock, not the old process-local striped
// mutex. A graph launch must leave a lock file under the city runtime dir; the
// process-local mutex never touched the filesystem, so this fails before the
// #1053 fix and passes after.
func TestLaunchWorkflowUsesCrossProcessFileLock(t *testing.T) {
	formulaDir := t.TempDir()
	writeGraphV2ConvoyFormula(t, formulaDir)
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath = t.TempDir()
	convoy, err := deps.Store.Create(beads.Bead{Title: "input", Type: "convoy"})
	if err != nil {
		t.Fatal(err)
	}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	opts := molecule.Options{Vars: map[string]string{"convoy_id": convoy.ID}}

	if _, err := InstantiateSlingFormula(context.Background(), "graph-work", []string{formulaDir}, opts, "", "default", "", a, deps); err != nil {
		t.Fatalf("InstantiateSlingFormula: %v", err)
	}

	lockDir := filepath.Join(citylayout.RuntimeDataDir(deps.CityPath), "sling-source-locks")
	entries, err := os.ReadDir(lockDir)
	if err != nil {
		t.Fatalf("reading sling-source-locks dir %s (a cross-process file lock must have been taken on the RootKey): %v", lockDir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("sling-source-locks dir %s is empty; the launch did not take a cross-process file lock on the RootKey", lockDir)
	}
}

// TestLaunchWorkflowLegitimateDistinctLaunchesAllowed proves the guard never
// blocks a legitimate launch (#720): distinct RootKeys (different convoy input)
// coexist, and a relaunch after the prior root is closed succeeds with a fresh
// root (invariants I6 + I7).
func TestLaunchWorkflowLegitimateDistinctLaunchesAllowed(t *testing.T) {
	formulaDir := t.TempDir()
	writeGraphV2ConvoyFormula(t, formulaDir)
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath = t.TempDir()
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	convoyA, err := deps.Store.Create(beads.Bead{Title: "input-a", Type: "convoy"})
	if err != nil {
		t.Fatal(err)
	}
	convoyB, err := deps.Store.Create(beads.Bead{Title: "input-b", Type: "convoy"})
	if err != nil {
		t.Fatal(err)
	}

	optsA := molecule.Options{Vars: map[string]string{"convoy_id": convoyA.ID}}
	optsB := molecule.Options{Vars: map[string]string{"convoy_id": convoyB.ID}}
	rootA, err := InstantiateSlingFormula(context.Background(), "graph-work", []string{formulaDir}, optsA, "", "default", "", a, deps)
	if err != nil {
		t.Fatalf("launch A: %v", err)
	}
	rootB, err := InstantiateSlingFormula(context.Background(), "graph-work", []string{formulaDir}, optsB, "", "default", "", a, deps)
	if err != nil {
		t.Fatalf("launch B: %v", err)
	}
	if rootA.RootID == rootB.RootID {
		t.Fatalf("distinct convoys shared a root %q, want two roots (I7)", rootA.RootID)
	}
	if live := liveGraphV2Roots(t, deps.Store); len(live) != 2 {
		t.Fatalf("live graph roots = %d, want two distinct identities (I7)", len(live))
	}

	// Relaunch after the prior root is closed: never blocked (I6).
	if _, err := sourceworkflow.CloseWorkflowSubtree(deps.Store, rootA.RootID); err != nil {
		t.Fatalf("close root A: %v", err)
	}
	relaunch, err := InstantiateSlingFormula(context.Background(), "graph-work", []string{formulaDir}, optsA, "", "default", "", a, deps)
	if err != nil {
		t.Fatalf("relaunch after close: %v", err)
	}
	if relaunch.RootID == rootA.RootID {
		t.Fatalf("relaunch reused closed root %q, want a fresh root (I6)", rootA.RootID)
	}
}

// TestAttachFormulaToBeadForceCreatesParallelRoot covers gc-mrh0 AC5 at the
// real gc sling --force entry point: --force is the explicit escape hatch
// that permits a second, independent workflow run in parallel with an
// existing live one, rather than the default converge-on-one-root behavior
// every other test in this file pins. It exercises the full DoSling ->
// attachFormulaToBead path (not the lower-level launchForBeadTarget helper),
// so it also proves opts.Force actually reaches
// graphv2.PrepareInvocationForced through prepareGraphV2FormulaInvocation.
func TestAttachFormulaToBeadForceCreatesParallelRoot(t *testing.T) {
	formulaDir := t.TempDir()
	writeGraphV2ConvoyFormula(t, formulaDir)
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath = t.TempDir()
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	target, err := deps.Store.Create(beads.Bead{Title: "work bead", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}

	first, err := DoSling(SlingOpts{Target: a, BeadOrFormula: target.ID, OnFormula: "graph-work"}, deps, deps.Store)
	if err != nil {
		t.Fatalf("DoSling (first): %v", err)
	}
	second, err := DoSling(SlingOpts{Target: a, BeadOrFormula: target.ID, OnFormula: "graph-work", Force: true}, deps, deps.Store)
	if err != nil {
		t.Fatalf("DoSling --force: %v", err)
	}
	if second.WorkflowID == "" {
		t.Fatal("forced dispatch produced no workflow root")
	}
	if second.WorkflowID == first.WorkflowID {
		t.Fatalf("forced dispatch reused root %q, want an independent parallel root", first.WorkflowID)
	}
	if live := liveGraphV2Roots(t, deps.Store); len(live) != 2 {
		t.Fatalf("live graph roots = %d, want 2 (the original plus the forced parallel run): %+v", len(live), live)
	}
}

// createOverlapDetector wraps a beads.Store and records whether two Create
// calls were ever concurrently in flight. NormalizeInputConvoy's own
// lookup-then-create-then-reresolve dance only converges racing callers on
// the same convoy if every caller observes every other caller's writes — an
// assumption a store with cross-connection read lag (Dolt) is not guaranteed
// to satisfy. attachFormulaToBead closes that gap architecturally instead of
// depending on the assumption: it now acquires the cross-process
// per-source-bead file lock (immune to store read lag, since it's plain
// flock) BEFORE calling into convoy normalization, so at most one caller
// should ever be inside a Create call for a given source bead at a time. The
// short sleep while "inside" widens the detection window — MemStore's
// critical sections are otherwise fast enough that concurrent goroutines
// rarely truly overlap regardless of whether serialization is enforced (see
// the probe behind TestNormalizeInputConvoyClosesOrphanedConvoyAfterLostRace,
// which found 0/160 unassisted trials produced contention).
type createOverlapDetector struct {
	beads.Store
	mu         sync.Mutex
	inside     bool
	overlapped atomic.Bool
}

func (d *createOverlapDetector) Create(b beads.Bead) (beads.Bead, error) {
	d.mu.Lock()
	if d.inside {
		d.overlapped.Store(true)
	}
	d.inside = true
	d.mu.Unlock()

	time.Sleep(5 * time.Millisecond)

	d.mu.Lock()
	d.inside = false
	d.mu.Unlock()

	return d.Store.Create(b)
}

// TestAttachFormulaToBeadSerializesConvoyCreationPerSourceBead proves the
// fix for the race Codex's review flagged: acquiring
// withGraphV2SourceWorkflowLock before prepareGraphV2FormulaInvocation (not
// after, as the code read before this test) means concurrent gc sling
// dispatches at the same bead target never have two Create calls in flight
// at once, so NormalizeInputConvoy's oldest-wins convergence never has to
// rely on cross-connection read-after-write visibility to begin with — the
// race window is closed, not just usually avoided.
func TestAttachFormulaToBeadSerializesConvoyCreationPerSourceBead(t *testing.T) {
	formulaDir := t.TempDir()
	writeGraphV2ConvoyFormula(t, formulaDir)
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath = t.TempDir()
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}

	target, err := deps.Store.Create(beads.Bead{Title: "work bead", Type: "task"})
	if err != nil {
		t.Fatal(err)
	}

	detector := &createOverlapDetector{Store: deps.Store}
	deps.Store = detector

	const n = 4
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = DoSling(SlingOpts{Target: a, BeadOrFormula: target.ID, OnFormula: "graph-work"}, deps, deps.Store)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("DoSling %d: %v", i, err)
		}
	}
	if detector.overlapped.Load() {
		t.Fatal("two Create calls overlapped for the same source bead — the source-bead lock did not serialize convoy creation")
	}
	if live := liveGraphV2Roots(t, deps.Store); len(live) != 1 {
		t.Fatalf("live graph roots = %d, want exactly 1", len(live))
	}
}

// TestInstantiateCompiledSlingFormulaAcceptsPrecompiledRecipe pins the
// compile-once primitive: a recipe compiled by the caller is instantiated
// without a second disk compile, materializing the same graph root.
func TestInstantiateCompiledSlingFormulaAcceptsPrecompiledRecipe(t *testing.T) {
	formulaDir := t.TempDir()
	writeGraphV2ConvoyFormula(t, formulaDir)
	cfg := graphV2SlingTestConfig(t, formulaDir)
	deps := testDeps(cfg, runtime.NewFake(), newFakeRunner().run)
	deps.CityPath = t.TempDir()
	convoy, err := deps.Store.Create(beads.Bead{Title: "input", Type: "convoy"})
	if err != nil {
		t.Fatal(err)
	}
	a := config.Agent{Name: "mayor", MaxActiveSessions: intPtr(1)}
	vars := map[string]string{"convoy_id": convoy.ID}
	opts := molecule.Options{Vars: vars}

	recipe, err := formula.CompileWithoutRuntimeVarValidation(context.Background(), "graph-work", []string{formulaDir}, vars)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	res, err := InstantiateCompiledSlingFormula(context.Background(), recipe, "graph-work", opts, "", "default", "", a, deps)
	if err != nil {
		t.Fatalf("InstantiateCompiledSlingFormula: %v", err)
	}
	if res.RootID == "" {
		t.Fatalf("no root materialized")
	}
	if live := liveGraphV2Roots(t, deps.Store); len(live) != 1 {
		t.Fatalf("live graph roots = %d, want one", len(live))
	}
}
