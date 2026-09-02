package main

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/molecule"
)

// orphanedContinuationStepFixtureGroup and orphanedContinuationStepFixtureAssignee
// are the continuation group and step assignee used by every
// newOrphanedContinuationStepFixture call in this file; none of the tests
// below vary them, so they are constants rather than parameters.
const (
	orphanedContinuationStepFixtureGroup    = "grp-1"
	orphanedContinuationStepFixtureAssignee = "worker-dead"
)

// newOrphanedContinuationStepFixture builds a real *beads.MemStore (never the
// conditionalReleaseProbeStore wrapper used elsewhere in this file):
// conditionalReleaseProbeStore embeds beads.Store as an interface-typed
// field, and Go's method-set promotion through an embedded interface field
// only promotes that interface's own methods — not the extra UpdateIfMatch
// method the underlying *beads.MemStore happens to also implement. So
// *conditionalReleaseProbeStore never satisfies beads.ConditionalWriter, and
// molecule.ReleaseContinuationLease (via molecule.ClaimExact ->
// beads.ConditionalWriterFor) can only ever take its non-fatal error/log
// branch through it, never a genuine CAS release. A bare *beads.MemStore
// implements ConditionalWriter directly on its concrete type, so it is the
// vehicle that actually exercises the lease-release CAS path.
func newOrphanedContinuationStepFixture(t *testing.T, rootStatus, leaseHolder string) (*beads.MemStore, beads.Bead, beads.Bead) {
	t.Helper()
	store := beads.NewMemStore()

	root, err := store.Create(beads.Bead{Title: "workflow root"})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	if leaseHolder != "" {
		// Seed the lease before any terminalization: AcquireContinuationLease
		// refuses on a closed root (ContinuationLeaseRootTerminal), so a
		// terminal-root scenario must close the root only after the lease is
		// already held, matching how a real workflow root would close mid-flight
		// with an outstanding lease still on it.
		if _, outcome, err := molecule.AcquireContinuationLease(store, root.ID, orphanedContinuationStepFixtureGroup, leaseHolder); err != nil || outcome != molecule.ContinuationLeaseAcquired {
			t.Fatalf("seed continuation lease: outcome=%v err=%v, want acquired/nil", outcome, err)
		}
	}
	if rootStatus != "" && rootStatus != "open" {
		if err := store.Update(root.ID, beads.UpdateOpts{Status: stringPtr(rootStatus)}); err != nil {
			t.Fatalf("set root status: %v", err)
		}
	}
	root, err = store.Get(root.ID)
	if err != nil {
		t.Fatalf("reload root: %v", err)
	}

	step, err := store.Create(beads.Bead{
		Title:    "orphaned step",
		Assignee: orphanedContinuationStepFixtureAssignee,
		Metadata: map[string]string{
			"gc.routed_to":                        "worker",
			"gc.session_affinity":                 "require",
			beadmeta.RootBeadIDMetadataKey:        root.ID,
			beadmeta.ContinuationGroupMetadataKey: orphanedContinuationStepFixtureGroup,
		},
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	if err := store.Update(step.ID, beads.UpdateOpts{Status: stringPtr("in_progress")}); err != nil {
		t.Fatalf("set step status: %v", err)
	}
	step, err = store.Get(step.ID)
	if err != nil {
		t.Fatalf("reload step: %v", err)
	}
	return store, root, step
}

// TestReleaseOrphanedPoolAssignment_ForceReleasesHeldContinuationLease pins
// the main crash-recovery case: an orphaned step bead's release must also
// force-release its workflow root's continuation lease, not merely log past
// a "root not found" error (the only branch
// TestReleaseOrphanedPoolAssignments_ContinuationGroupBeadBypassesCASWindow
// exercises, since that fixture never creates the root bead it names).
func TestReleaseOrphanedPoolAssignment_ForceReleasesHeldContinuationLease(t *testing.T) {
	store, root, step := newOrphanedContinuationStepFixture(t, "open", "worker-dead")

	if !releaseOrphanedPoolAssignment(store, step, false) {
		t.Fatalf("releaseOrphanedPoolAssignment returned false, want true")
	}

	got, err := store.Get(step.ID)
	if err != nil {
		t.Fatalf("Get step: %v", err)
	}
	if got.Status != "open" || got.Assignee != "" {
		t.Fatalf("step = status %q assignee %q, want open/unassigned", got.Status, got.Assignee)
	}

	holder, err := molecule.ContinuationLeaseHolder(store, root.ID)
	if err != nil {
		t.Fatalf("ContinuationLeaseHolder: %v", err)
	}
	if holder != "" {
		t.Fatalf("lease holder = %q, want cleared by the reconciler's force release", holder)
	}
}

// TestReleaseOrphanedPoolAssignment_ForceReleasesLeaseHeldByDifferentFencedSession
// pins the "release it for a fenced owner" half of gc-jspv's requirement: the
// root's lease holder need not match the orphaned step bead's own stale
// assignee (the lease can have been acquired earlier by a session since
// superseded on this particular step). releaseOrphanedContinuationLease calls
// molecule.ReleaseContinuationLease with requireHolder="" specifically so
// crash recovery force-releases regardless of who currently holds it.
func TestReleaseOrphanedPoolAssignment_ForceReleasesLeaseHeldByDifferentFencedSession(t *testing.T) {
	store, root, step := newOrphanedContinuationStepFixture(t, "open", "worker-other-fenced")

	if !releaseOrphanedPoolAssignment(store, step, false) {
		t.Fatalf("releaseOrphanedPoolAssignment returned false, want true")
	}

	holder, err := molecule.ContinuationLeaseHolder(store, root.ID)
	if err != nil {
		t.Fatalf("ContinuationLeaseHolder: %v", err)
	}
	if holder != "" {
		t.Fatalf("lease holder = %q, want cleared even though the lease holder (worker-other-fenced) differs from the step's stale assignee (worker-dead)", holder)
	}
}

// TestReleaseOrphanedPoolAssignment_ReleasesLeaseOnTerminalRoot pins root
// terminalization: a workflow root can close between the lease being
// acquired and the reconciler discovering the orphaned step assignment.
// molecule.ReleaseContinuationLease carries no status precondition (unlike
// AcquireContinuationLease), so the release must still clear the lease
// metadata rather than leaving it permanently stranded on a bead that can
// never transition again.
func TestReleaseOrphanedPoolAssignment_ReleasesLeaseOnTerminalRoot(t *testing.T) {
	store, root, step := newOrphanedContinuationStepFixture(t, "closed", "worker-dead")

	if !releaseOrphanedPoolAssignment(store, step, false) {
		t.Fatalf("releaseOrphanedPoolAssignment returned false, want true")
	}

	rootBead, err := store.Get(root.ID)
	if err != nil {
		t.Fatalf("Get root: %v", err)
	}
	if rootBead.Status != "closed" {
		t.Fatalf("root status = %q, want closed: terminalization must not be undone by the release", rootBead.Status)
	}
	if holder := strings.TrimSpace(rootBead.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey]); holder != "" {
		t.Fatalf("lease holder metadata = %q, want cleared on the terminal root", holder)
	}
}

// TestReleaseOrphanedPoolAssignment_NoActiveLeaseIsANoOp is the control case:
// a step bead can carry the continuation-group routing vector while its root
// holds no lease at all (never acquired). The best-effort release call must
// resolve to molecule.ContinuationLeaseNotHeld, not an error, and must not
// block the step bead's own release.
func TestReleaseOrphanedPoolAssignment_NoActiveLeaseIsANoOp(t *testing.T) {
	store, root, step := newOrphanedContinuationStepFixture(t, "open", "")

	if !releaseOrphanedPoolAssignment(store, step, false) {
		t.Fatalf("releaseOrphanedPoolAssignment returned false, want true")
	}

	got, err := store.Get(step.ID)
	if err != nil {
		t.Fatalf("Get step: %v", err)
	}
	if got.Status != "open" || got.Assignee != "" {
		t.Fatalf("step = status %q assignee %q, want open/unassigned", got.Status, got.Assignee)
	}

	holder, err := molecule.ContinuationLeaseHolder(store, root.ID)
	if err != nil {
		t.Fatalf("ContinuationLeaseHolder: %v", err)
	}
	if holder != "" {
		t.Fatalf("lease holder = %q, want still unheld", holder)
	}
}
