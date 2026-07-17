package beads

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// molFocusReviewSteps is the compiled step chain of the mol-focus-review
// graph.v2 formula, in dependency order: each step "blocks" the next.
var molFocusReviewSteps = []string{
	"load-context",
	"workspace-setup",
	"focus",
	"run-tests",
	"simplify",
	"review",
	"finalize",
	"workflow-finalize",
}

// hardFailShape builds the gc-3oxu reproduction: a graph.v2 workflow root whose
// first step closed with gc.outcome=fail + gc.failure_class=hard, with the
// remaining steps still open and chained to it by "blocks" edges. It returns the
// step beads keyed by step name.
//
// This mirrors the live shape exactly: every step "tracks" the root (a
// non-blocking bookkeeping edge) and "blocks"-depends on its predecessor.
func hardFailShape(t *testing.T, store Store, failMeta map[string]string) map[string]Bead {
	t.Helper()
	steps := buildFocusReviewSteps(t, store)
	// Bring a caching reader live while every step is still open, then close:
	// steps close under a running orchestrator, so the close is a live cache
	// transition, not a cold-start read.
	primeIfCaching(t, store)
	failLoadContext(t, store, steps, failMeta)
	return steps
}

// hardFailShapeWithoutPrime builds the same shape but leaves any caching reader
// cold, so the caller controls when the cache is primed relative to the close.
func hardFailShapeWithoutPrime(t *testing.T, store Store, failMeta map[string]string) map[string]Bead {
	t.Helper()
	steps := buildFocusReviewSteps(t, store)
	failLoadContext(t, store, steps, failMeta)
	return steps
}

// failLoadContext hard-fails and closes the first step, exactly as gc-3b6r did.
func failLoadContext(t *testing.T, store Store, steps map[string]Bead, failMeta map[string]string) {
	t.Helper()
	lc := steps["load-context"]
	for k, v := range failMeta {
		if err := store.SetMetadata(lc.ID, k, v); err != nil {
			t.Fatalf("set fail metadata %s: %v", k, err)
		}
	}
	if err := store.Close(lc.ID); err != nil {
		t.Fatalf("close load-context: %v", err)
	}
}

func buildFocusReviewSteps(t *testing.T, store Store) map[string]Bead {
	t.Helper()

	root, err := store.Create(Bead{Title: "mol-focus-review", Type: "task"})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	for k, v := range map[string]string{
		beadmeta.KindMetadataKey:            "workflow",
		beadmeta.FormulaContractMetadataKey: "graph.v2",
	} {
		if err := store.SetMetadata(root.ID, k, v); err != nil {
			t.Fatalf("set root metadata %s: %v", k, err)
		}
	}

	steps := make(map[string]Bead, len(molFocusReviewSteps))
	var prevID string
	for _, name := range molFocusReviewSteps {
		b, err := store.Create(Bead{Title: name, Type: "task"})
		if err != nil {
			t.Fatalf("create step %s: %v", name, err)
		}
		if err := store.SetMetadata(b.ID, beadmeta.RootBeadIDMetadataKey, root.ID); err != nil {
			t.Fatalf("set root_bead_id on %s: %v", name, err)
		}
		if err := store.SetMetadata(b.ID, beadmeta.StepRefMetadataKey, "mol-focus-review."+name); err != nil {
			t.Fatalf("set step_ref on %s: %v", name, err)
		}
		// Bookkeeping edge to the root: "tracks" never gates readiness.
		if err := store.DepAdd(b.ID, root.ID, "tracks"); err != nil {
			t.Fatalf("dep add tracks %s: %v", name, err)
		}
		if prevID != "" {
			if err := store.DepAdd(b.ID, prevID, "blocks"); err != nil {
				t.Fatalf("dep add blocks %s: %v", name, err)
			}
		}
		prevID = b.ID
		steps[name] = b
	}
	return steps
}

func readyIDSet(t *testing.T, store Store) map[string]bool {
	t.Helper()
	rows, err := store.Ready()
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	got := make(map[string]bool, len(rows))
	for _, b := range rows {
		got[b.ID] = true
	}
	return got
}

// hardFailStores exercises graph.v2 readiness across every store that computes
// dependency satisfaction itself, so a fix in one reader cannot silently leave
// another store unlocking downstream work (acceptance criterion 5).
func hardFailStores(t *testing.T) map[string]func(*testing.T) Store {
	t.Helper()
	return map[string]func(*testing.T) Store{
		"MemStore": func(t *testing.T) Store { return NewMemStore() },
		"CachingStore": func(t *testing.T) Store {
			return NewCachingStoreForTest(NewMemStore(), nil)
		},
	}
}

// primeIfCaching brings a CachingStore's read model live so Ready() is answered
// from cache rather than delegated to the backing store.
func primeIfCaching(t *testing.T, store Store) {
	t.Helper()
	if cs, ok := store.(*CachingStore); ok {
		if err := cs.Prime(context.Background()); err != nil {
			t.Fatalf("Prime: %v", err)
		}
	}
}

// A step that closed with gc.outcome=fail / gc.failure_class=hard is a terminal
// failure, not a completion. It must not satisfy its dependents, and every
// downstream step — body, review, finalize, workflow-finalize — must stay
// ineligible (acceptance criteria 1 and 2; live repro root gc-3oxu, where the
// hard-failed gc-3b6r unlocked all 7 downstream steps including the finalize
// step that would have closed the blocked target gc-d1yl).
func TestReadyHardFailedBlockerDoesNotSatisfyDependents(t *testing.T) {
	failMetas := map[string]map[string]string{
		"outcome_fail_and_failure_class_hard": {
			beadmeta.OutcomeMetadataKey:      beadmeta.OutcomeFail,
			beadmeta.FailureClassMetadataKey: beadmeta.FailureClassHard,
		},
		"outcome_fail_only": {
			beadmeta.OutcomeMetadataKey: beadmeta.OutcomeFail,
		},
		"failure_class_hard_only": {
			beadmeta.FailureClassMetadataKey: beadmeta.FailureClassHard,
		},
	}

	for storeName, newStore := range hardFailStores(t) {
		for metaName, failMeta := range failMetas {
			t.Run(storeName+"/"+metaName, func(t *testing.T) {
				store := newStore(t)
				steps := hardFailShape(t, store, failMeta)

				got := readyIDSet(t, store)
				for _, name := range molFocusReviewSteps[1:] {
					if got[steps[name].ID] {
						t.Errorf("step %q (%s) is Ready, but its blocker load-context hard-failed; "+
							"a terminal failure must not unlock downstream work",
							name, steps[name].ID)
					}
				}
			})
		}
	}
}

// KNOWN GAP (gc-d58o, not yet fixed): CachingStore's active cache holds only
// non-closed beads (cacheFullScanQuery), and readiness has always read an absent
// blocker as "closed, therefore satisfied". A blocker that closes while the
// orchestrator runs stays in cache with its metadata, so the gate holds on the
// live path this bug was reported on. But after a restart re-primes the cache,
// an already-closed hard-failed blocker is absent, its outcome is unreadable,
// and the dependent is unlocked again.
//
// Closing this needs a design decision, not a patch: either the cache retains a
// blocker-outcome projection for closed beads that open beads still depend on
// (bounded, immutable once closed, primed alongside deps), or the ready path
// reads those blockers from the backing store and gives up the cache-only
// contract that CachedReady/ReadyContext deliberately keep (#2210).
func TestReadyHardFailedBlockerSurvivesColdPrime(t *testing.T) {
	t.Skip("gc-d58o: CachingStore caches no closed beads, so a hard-failed blocker's " +
		"outcome is unreadable after a cold prime; needs a cache-model decision")

	store := NewCachingStoreForTest(NewMemStore(), nil)
	steps := hardFailShapeWithoutPrime(t, store, map[string]string{
		beadmeta.OutcomeMetadataKey:      beadmeta.OutcomeFail,
		beadmeta.FailureClassMetadataKey: beadmeta.FailureClassHard,
	})
	// Prime AFTER the close: the restart shape.
	if err := store.Prime(context.Background()); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if readyIDSet(t, store)[steps["workspace-setup"].ID] {
		t.Errorf("workspace-setup (%s) is Ready after a cold prime, but its blocker hard-failed",
			steps["workspace-setup"].ID)
	}
}

// A blocker that closed successfully still satisfies its dependents: the fix
// must not stall healthy workflows (acceptance criterion 4).
func TestReadyClosedPassedBlockerStillSatisfiesDependents(t *testing.T) {
	passMetas := map[string]map[string]string{
		"outcome_pass":     {beadmeta.OutcomeMetadataKey: beadmeta.OutcomePass},
		"outcome_skipped":  {beadmeta.OutcomeMetadataKey: beadmeta.OutcomeSkipped},
		"outcome_canceled": {beadmeta.OutcomeMetadataKey: beadmeta.OutcomeCanceled},
		"no_outcome":       {},
		"transient_failure_class": {
			beadmeta.FailureClassMetadataKey: beadmeta.FailureClassTransient,
		},
	}

	for storeName, newStore := range hardFailStores(t) {
		for metaName, passMeta := range passMetas {
			t.Run(storeName+"/"+metaName, func(t *testing.T) {
				store := newStore(t)
				steps := hardFailShape(t, store, passMeta)

				got := readyIDSet(t, store)
				next := steps["workspace-setup"]
				if !got[next.ID] {
					t.Errorf("workspace-setup (%s) is not Ready, but its blocker closed "+
						"without a terminal failure; successful blockers must keep unlocking work",
						next.ID)
				}
			})
		}
	}
}

// The readiness gate is driven by the blocker's own metadata, so it must hold
// even when bd's denormalized is_blocked projection — which is computed from
// status alone and knows nothing about gc.outcome — claims the dependent is
// unblocked.
func TestCachedBeadReadyHardFailedBlockerOverridesIsBlockedProjection(t *testing.T) {
	unblocked := false
	dependent := Bead{ID: "workspace-setup", Status: "open", IsBlocked: &unblocked}
	blockers := map[string]Bead{
		"load-context": {
			ID:     "load-context",
			Status: "closed",
			Metadata: map[string]string{
				beadmeta.OutcomeMetadataKey:      beadmeta.OutcomeFail,
				beadmeta.FailureClassMetadataKey: beadmeta.FailureClassHard,
			},
		},
	}
	deps := []Dep{{IssueID: "workspace-setup", DependsOnID: "load-context", Type: "blocks"}}

	if cachedBeadReady(dependent, blockerStatesFrom(blockers), deps) {
		t.Fatal("cachedBeadReady = true for a dependent whose blocker hard-failed; " +
			"bd's status-only is_blocked projection must not override the outcome gate")
	}
}

// blockerStatesFrom is a test convenience mirroring the production projection.
func blockerStatesFrom(beadsByID map[string]Bead) map[string]blockerState {
	out := make(map[string]blockerState, len(beadsByID))
	for id, b := range beadsByID {
		out[id] = blockerStateOf(b)
	}
	return out
}

// The retry machinery wires retry-eval --blocks--> retry-run precisely so the
// eval bead can observe a failed attempt and decide whether to re-run. Gating on
// the attempt's failure would strand the eval and kill the retry loop, so a
// failed retry attempt must keep satisfying its dependent (internal/dispatch/retry.go).
func TestReadyFailedRetryAttemptStillSatisfiesItsEval(t *testing.T) {
	attempts := map[string]map[string]string{
		"v1_retry_run_kind": {
			beadmeta.LogicalBeadIDMetadataKey: "gc-logical",
			beadmeta.KindMetadataKey:          "retry-run",
		},
		"v2_attempt_marker": {
			beadmeta.LogicalBeadIDMetadataKey: "gc-logical",
			beadmeta.AttemptMetadataKey:       "2",
		},
	}

	for storeName, newStore := range hardFailStores(t) {
		for attemptName, attemptMeta := range attempts {
			t.Run(storeName+"/"+attemptName, func(t *testing.T) {
				store := newStore(t)

				run, err := store.Create(Bead{Title: "retry run", Type: "task"})
				if err != nil {
					t.Fatalf("create run: %v", err)
				}
				eval, err := store.Create(Bead{Title: "retry eval", Type: "task"})
				if err != nil {
					t.Fatalf("create eval: %v", err)
				}
				for k, v := range attemptMeta {
					if err := store.SetMetadata(run.ID, k, v); err != nil {
						t.Fatalf("set %s: %v", k, err)
					}
				}
				// The attempt failed — that is the signal the eval exists to read.
				if err := store.SetMetadata(run.ID, beadmeta.OutcomeMetadataKey, beadmeta.OutcomeFail); err != nil {
					t.Fatalf("set outcome: %v", err)
				}
				if err := store.DepAdd(eval.ID, run.ID, "blocks"); err != nil {
					t.Fatalf("dep add: %v", err)
				}
				if err := store.Close(run.ID); err != nil {
					t.Fatalf("close run: %v", err)
				}
				primeIfCaching(t, store)

				if !readyIDSet(t, store)[eval.ID] {
					t.Errorf("retry-eval (%s) is not Ready after its attempt failed; "+
						"the hard-fail gate must not strand the retry loop", eval.ID)
				}
			})
		}
	}
}

func TestBlockerSatisfiesReadyDependency(t *testing.T) {
	tests := []struct {
		name string
		bead Bead
		want bool
	}{
		{"open blocker", Bead{Status: "open"}, false},
		{"in_progress blocker", Bead{Status: "in_progress"}, false},
		{"closed with no metadata", Bead{Status: "closed"}, true},
		{"closed pass", Bead{Status: "closed", Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey: beadmeta.OutcomePass}}, true},
		{"closed skipped", Bead{Status: "closed", Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey: beadmeta.OutcomeSkipped}}, true},
		{"closed canceled", Bead{Status: "closed", Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey: beadmeta.OutcomeCanceled}}, true},
		{"closed transient failure", Bead{Status: "closed", Metadata: map[string]string{
			beadmeta.FailureClassMetadataKey: beadmeta.FailureClassTransient}}, true},
		{"closed outcome fail", Bead{Status: "closed", Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey: beadmeta.OutcomeFail}}, false},
		{"closed failure_class hard", Bead{Status: "closed", Metadata: map[string]string{
			beadmeta.FailureClassMetadataKey: beadmeta.FailureClassHard}}, false},
		{"closed fail and hard", Bead{Status: "closed", Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey:      beadmeta.OutcomeFail,
			beadmeta.FailureClassMetadataKey: beadmeta.FailureClassHard}}, false},
		{"closed outcome fail with surrounding space", Bead{Status: "closed", Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey: " fail "}}, false},
		// An open bead carrying failure metadata is not terminal: it never
		// satisfied its dependents anyway, but the reason must be its status.
		{"open with fail metadata", Bead{Status: "open", Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey: beadmeta.OutcomeFail}}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := BlockerSatisfiesReadyDependency(tc.bead); got != tc.want {
				t.Errorf("BlockerSatisfiesReadyDependency(%+v) = %v, want %v", tc.bead, got, tc.want)
			}
		})
	}
}
