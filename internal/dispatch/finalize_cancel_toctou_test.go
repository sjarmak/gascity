package dispatch

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// buildCanceledRootFinalizeScenario builds the exact interleave the
// cancel-vs-finalize TOCTOU produces: POST /runs/{id}/cancel has already closed
// the workflow root as canceled, and only afterwards does the control-dispatcher
// run the (still-open) finalizer over its passing step. It returns the root and
// finalizer beads.
func buildCanceledRootFinalizeScenario(t *testing.T, store beads.Store) (root, finalizer beads.Bead) {
	t.Helper()
	root = mustCreateWorkflowBead(t, store, beads.Bead{
		Title: "workflow root",
		Type:  "task",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey: beadmeta.KindWorkflow,
		},
	})
	// Cancel won the race: the root is already terminal, recorded canceled.
	closed := "closed"
	if err := store.Update(root.ID, beads.UpdateOpts{
		Status: &closed,
		Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey:         beadmeta.OutcomeCanceled,
			beadmeta.CancelRequestedMetadataKey: "true",
		},
	}); err != nil {
		t.Fatalf("close root as canceled: %v", err)
	}
	step := mustCreateWorkflowBead(t, store, beads.Bead{
		Title:  "work step",
		Type:   "task",
		Status: "closed",
		Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey: beadmeta.OutcomePass,
		},
	})
	finalizer = mustCreateWorkflowBead(t, store, beads.Bead{
		Title: "Finalize workflow",
		Type:  "task",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey:       beadmeta.KindWorkflowFinalize,
			beadmeta.RootBeadIDMetadataKey: root.ID,
		},
	})
	mustDepAdd(t, store, finalizer.ID, step.ID, "blocks")
	mustDepAdd(t, store, root.ID, finalizer.ID, "blocks")
	return root, finalizer
}

// TestProcessWorkflowFinalizePreservesCanceledRoot is the regression guard for
// the cancel-vs-finalize TOCTOU: a root that a concurrent run-cancel already
// closed as canceled must NOT be overwritten with gc.outcome=pass|fail when the
// finalizer subsequently runs. The finalizer itself must still close cleanly.
func TestProcessWorkflowFinalizePreservesCanceledRoot(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()
	root, finalizer := buildCanceledRootFinalizeScenario(t, store)

	if _, err := ProcessControl(store, finalizer, ProcessOptions{}); err != nil {
		t.Fatalf("ProcessControl(workflow-finalize): %v", err)
	}

	rootAfter := mustGetBead(t, store, root.ID)
	if got := rootAfter.Metadata[beadmeta.OutcomeMetadataKey]; got != beadmeta.OutcomeCanceled {
		t.Fatalf("root gc.outcome = %q, want %q (finalize overwrote a concurrent cancel)", got, beadmeta.OutcomeCanceled)
	}
	if rootAfter.Status != "closed" {
		t.Fatalf("root status = %q, want closed", rootAfter.Status)
	}
	if fin := mustGetBead(t, store, finalizer.ID); fin.Status != "closed" {
		t.Fatalf("finalizer status = %q, want closed (cleanup must still complete)", fin.Status)
	}
}

// TestProcessWorkflowFinalizeCrashRetryClosesSourceChain guards the crash-retry
// idempotency contract. processWorkflowFinalize closes the root before the
// finalizer so a dispatcher crash between those closes retries cleanly; on that
// retry the root is already closed with the same outcome. The finalizer's
// source-bead chain close (which makes "Adopt PR"-style parent beads disappear)
// must still run — the fenced close must NOT mistake our own prior outcome for a
// foreign cancel and skip it, or the parents orphan forever.
func TestProcessWorkflowFinalizeCrashRetryClosesSourceChain(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	parent := mustCreateWorkflowBead(t, store, beads.Bead{Title: "adopt pr parent (source)", Type: "task"})
	root := mustCreateWorkflowBead(t, store, beads.Bead{
		Title: "workflow root",
		Type:  "task",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey:         beadmeta.KindWorkflow,
			beadmeta.SourceBeadIDMetadataKey: parent.ID,
		},
	})
	// Simulate a prior finalize pass that closed the root pass, then crashed
	// before closing the source chain and the finalizer.
	closed := "closed"
	if err := store.Update(root.ID, beads.UpdateOpts{
		Status:   &closed,
		Metadata: map[string]string{beadmeta.OutcomeMetadataKey: beadmeta.OutcomePass},
	}); err != nil {
		t.Fatalf("pre-close root as pass: %v", err)
	}
	step := mustCreateWorkflowBead(t, store, beads.Bead{
		Title:  "work step",
		Type:   "task",
		Status: "closed",
		Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey: beadmeta.OutcomePass,
		},
	})
	finalizer := mustCreateWorkflowBead(t, store, beads.Bead{
		Title: "Finalize workflow",
		Type:  "task",
		Metadata: map[string]string{
			beadmeta.KindMetadataKey:       beadmeta.KindWorkflowFinalize,
			beadmeta.RootBeadIDMetadataKey: root.ID,
		},
	})
	mustDepAdd(t, store, finalizer.ID, step.ID, "blocks")
	mustDepAdd(t, store, root.ID, finalizer.ID, "blocks")

	if _, err := ProcessControl(store, finalizer, ProcessOptions{}); err != nil {
		t.Fatalf("ProcessControl(workflow-finalize): %v", err)
	}

	if got := mustGetBead(t, store, parent.ID); got.Status != "closed" {
		t.Fatalf("parent source bead status = %q, want closed (crash-retry must still close the source chain)", got.Status)
	}
	if fin := mustGetBead(t, store, finalizer.ID); fin.Status != "closed" {
		t.Fatalf("finalizer status = %q, want closed", fin.Status)
	}
}

// Fenced-helper unit tests live in finalize_cancel_toctou_helper_test.go, added
// alongside the fencedUpdateMetadataAndClose implementation.
