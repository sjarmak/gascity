package dispatch

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// buildAbortScopeRoot builds a workflow root plus a direct member with
// gc.on_fail=abort_scope that is closed failed, and a finalizer whose only
// blocker is a passing bead (so resolveBlockedOutcome alone would say pass).
// memberIssueID, when non-empty, is stamped as the member's gc.var.issue so
// abortScopeFailureTargetAlreadyDelivered can resolve a target bead.
func buildAbortScopeRoot(t *testing.T, store beads.Store, memberIssueID string) (finalizer beads.Bead) {
	t.Helper()

	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Type:     "molecule",
		Metadata: map[string]string{beadmeta.KindMetadataKey: "workflow"},
	})

	memberMetadata := map[string]string{
		beadmeta.RootBeadIDMetadataKey:    root.ID,
		beadmeta.OnFailMetadataKey:        "abort_scope",
		beadmeta.OutcomeMetadataKey:       beadmeta.OutcomeFail,
		beadmeta.FailureClassMetadataKey:  beadmeta.FailureClassHard,
		beadmeta.FailureReasonMetadataKey: "duplicate_dispatch",
	}
	if memberIssueID != "" {
		memberMetadata[beadmeta.FormulaVarPrefix+"issue"] = memberIssueID
	}
	member := mustCreate(t, store, beads.Bead{Title: "workspace-setup", Metadata: memberMetadata})
	mustClose(t, store, member.ID)

	passingBlocker := mustCreate(t, store, beads.Bead{Title: "preflight", Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey: root.ID,
		beadmeta.OutcomeMetadataKey:    beadmeta.OutcomePass,
	}})
	mustClose(t, store, passingBlocker.ID)

	finalizerBead := mustCreate(t, store, beads.Bead{Title: "finalize", Metadata: map[string]string{
		beadmeta.RootBeadIDMetadataKey: root.ID,
	}})
	mustDep(t, store, finalizerBead.ID, passingBlocker.ID, "blocks")
	return mustGet(t, store, finalizerBead.ID)
}

// TestResolveFinalizeOutcomePostCloseSetupFailureDoesNotInvertDeliveredRoot
// covers acceptance criterion 2 (gc-le45hl): a root whose target closed
// deliverable and whose workspace-setup step failed AFTER that close resolves
// to a passing outcome, not fail.
func TestResolveFinalizeOutcomePostCloseSetupFailureDoesNotInvertDeliveredRoot(t *testing.T) {
	t.Parallel()

	store := beads.NewMemStore()
	target := mustCreate(t, store, beads.Bead{Title: "target issue"})
	mustClose(t, store, target.ID)
	if err := store.SetMetadata(target.ID, beadmeta.CoordinatorOutcomeProducerDispositionMetadataKey, beadmeta.CoordinatorDispositionDeliverable); err != nil {
		t.Fatalf("stamp producer disposition: %v", err)
	}

	finalizer := buildAbortScopeRoot(t, store, target.ID)

	outcome, err := resolveFinalizeOutcome(store, finalizer)
	if err != nil {
		t.Fatalf("resolveFinalizeOutcome: %v", err)
	}
	if outcome != beadmeta.OutcomePass {
		t.Fatalf("outcome = %q, want %q: a post-close duplicate_dispatch refusal must not invert a delivered root", outcome, beadmeta.OutcomePass)
	}
}

// TestResolveFinalizeOutcomeNoProducerDispositionStillFails covers acceptance
// criterion 3 (gc-le45hl), modeled on the gc-3myte3 shape: a root whose
// target has NO producer disposition and whose step failed still resolves to
// fail. This is the regression guard against over-demoting: a scope failure
// where the work landed on the wrong branch (target never closed deliverable)
// must keep failing.
func TestResolveFinalizeOutcomeNoProducerDispositionStillFails(t *testing.T) {
	t.Parallel()

	store := beads.NewMemStore()
	target := mustCreate(t, store, beads.Bead{Title: "target issue"})
	// Target stays OPEN, no producer disposition recorded -- the gc-3myte3
	// shape: work landed on a self-named branch, the formula branch stayed
	// empty, and the target was never closed deliverable.

	finalizer := buildAbortScopeRoot(t, store, target.ID)

	outcome, err := resolveFinalizeOutcome(store, finalizer)
	if err != nil {
		t.Fatalf("resolveFinalizeOutcome: %v", err)
	}
	if outcome != beadmeta.OutcomeFail {
		t.Fatalf("outcome = %q, want %q: a scope failure with no recorded delivery must still fail", outcome, beadmeta.OutcomeFail)
	}
}

// TestResolveFinalizeOutcomeFailureBeforeAnyCloseStillFails covers acceptance
// criterion 4 (gc-le45hl): a failure that happens BEFORE any deliverable
// close still resolves to fail, even when the failing member names a target
// bead (the genuine duplicate-dispatch shape, gc-e8lim): the target is closed
// but carries no producer disposition, so nothing was ever recorded as
// delivered by way of this failure.
func TestResolveFinalizeOutcomeFailureBeforeAnyCloseStillFails(t *testing.T) {
	t.Parallel()

	store := beads.NewMemStore()
	target := mustCreate(t, store, beads.Bead{Title: "target issue"})
	mustClose(t, store, target.ID)
	// Closed, but with NO producer disposition recorded -- distinguishes a
	// bare closed status (never sufficient to demote) from an actual
	// recorded deliverable disposition.

	finalizer := buildAbortScopeRoot(t, store, target.ID)

	outcome, err := resolveFinalizeOutcome(store, finalizer)
	if err != nil {
		t.Fatalf("resolveFinalizeOutcome: %v", err)
	}
	if outcome != beadmeta.OutcomeFail {
		t.Fatalf("outcome = %q, want %q: a bare closed target with no recorded disposition must still fail", outcome, beadmeta.OutcomeFail)
	}
}

// TestResolveFinalizeOutcomeMissingIssueVarStillFails guards the case where
// the failing member carries no gc.var.issue at all: there is no target to
// resolve, so the demotion must proceed exactly as before this change.
func TestResolveFinalizeOutcomeMissingIssueVarStillFails(t *testing.T) {
	t.Parallel()

	store := beads.NewMemStore()
	finalizer := buildAbortScopeRoot(t, store, "")

	outcome, err := resolveFinalizeOutcome(store, finalizer)
	if err != nil {
		t.Fatalf("resolveFinalizeOutcome: %v", err)
	}
	if outcome != beadmeta.OutcomeFail {
		t.Fatalf("outcome = %q, want %q: a failing member with no target to resolve must still fail", outcome, beadmeta.OutcomeFail)
	}
}
