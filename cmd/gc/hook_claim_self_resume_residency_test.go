package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/beads/splittest"
	"github.com/gastownhall/gascity/internal/config"
)

// selfResumeOwnClaim is the session's own in_progress bead, living ONLY in the
// relocated class binding — the residency the claim-time class route creates and
// that no bd workspace leg can reach.
const selfResumeOwnClaim = `{"id":"gcg-own","status":"in_progress","assignee":"worker-1","issue_type":"task","metadata":{"gc.routed_to":"worker"}}`

// selfResumeFreshDemand is unclaimed routed work sitting in the work store. The
// bug is that a session holding selfResumeOwnClaim claims THIS as well.
const selfResumeFreshDemand = `{"id":"gc-fresh","status":"open","issue_type":"task","metadata":{"gc.routed_to":"worker"}}`

// TestHookClaimAdoptsOwnGraphResidentClaim pins the BUG-2 invariant: a session's
// own in_progress claim is returned BEFORE any fresh claim, whatever store it
// lives in.
//
// Before the tier-0 swap the crash-recovery read was single-store `bd list`, so
// a claim the class route had written into the relocated binding was invisible
// on every leg. The session's tier 0 came back empty, the routed tier served it
// fresh demand, and it claimed a second bead while still holding the first —
// the 2-3-claims-per-session hoard, produced by construction rather than by race.
func TestHookClaimAdoptsOwnGraphResidentClaim(t *testing.T) {
	rec := &turnBoundClaimRecorder{}
	// The federated reader serves both rows in one answer: the binding-resident
	// own claim and the work-store fresh demand.
	output := `[` + selfResumeOwnClaim + `,` + selfResumeFreshDemand + `]`

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"worker"},
		JSON:               true,
	}, rec.ops(t, output), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	result := decodeTurnBoundResult(t, stdout.String())
	if result.Reason != "existing_assignment" || result.BeadID != "gcg-own" {
		t.Fatalf("result = %+v, want the session's own binding-resident claim adopted", result)
	}
	if len(rec.claims) != 0 {
		t.Fatalf("claims = %v, want none: a holder must not fresh-claim while it already holds work", rec.claims)
	}
}

// TestHookClaimDoesNotAdoptForeignGraphResidentClaim is the over-adoption
// control, and it fails in the OPPOSITE direction from the test above.
//
// Widening the LEGS the tier-0 read covers must not widen the IDENTITIES it
// adopts. A binding row owned by a different session stays un-adopted, and the
// session claims the fresh demand instead. Accumulation flips the first test;
// stealing flips this one.
func TestHookClaimDoesNotAdoptForeignGraphResidentClaim(t *testing.T) {
	rec := &turnBoundClaimRecorder{}
	foreign := `{"id":"gcg-foreign","status":"in_progress","assignee":"worker-2","issue_type":"task","metadata":{"gc.routed_to":"worker"}}`
	output := `[` + foreign + `,` + selfResumeFreshDemand + `]`

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"worker"},
		JSON:               true,
	}, rec.ops(t, output), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	result := decodeTurnBoundResult(t, stdout.String())
	if result.Reason != "claimed" || result.BeadID != "gc-fresh" {
		t.Fatalf("result = %+v, want the fresh work-store row claimed", result)
	}
	if len(rec.claims) != 1 || rec.claims[0] != "gc-fresh" {
		t.Fatalf("claims = %v, want only [gc-fresh]: a foreign claim must never be adopted", rec.claims)
	}
}

// TestHookClaimSkipsGraphResidentMessageBead keeps the #4419 guard honest across
// the widened leg set: a mail message addressed to this session has the same
// assignee-matches-identity shape as a real assignment, and adopting one returns
// mail as work ahead of the routed work waiting in the same batch.
func TestHookClaimSkipsGraphResidentMessageBead(t *testing.T) {
	rec := &turnBoundClaimRecorder{}
	message := `{"id":"gcg-mail","status":"in_progress","assignee":"worker-1","issue_type":"message","metadata":{"gc.routed_to":"worker"}}`
	output := `[` + message + `,` + selfResumeFreshDemand + `]`

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"worker"},
		JSON:               true,
	}, rec.ops(t, output), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	result := decodeTurnBoundResult(t, stdout.String())
	if result.BeadID != "gc-fresh" {
		t.Fatalf("result = %+v, want the message bead skipped and the routed work served", result)
	}
}

// TestHookClaimSelfResumeBeforeFreshClaimAcrossStores is the invariant at the
// FEDERATION level rather than within one store's candidate batch.
//
// The store-selection loop picks leg A first (it reports ready work), and leg A
// holds only fresh demand. The session's own claim is in leg B. The hoard shape
// is exactly this: the leg that answers first has claimable work, so the session
// claims it without ever consulting the leg holding what it already owns.
//
// The tier-0 swap fixes this by making the PRIMARY leg's read city-wide, so both
// rows arrive in one candidate batch and the existing-assignment tier wins
// inside one process. This test drives claimHookWorkWithRunner to prove the
// ordering survives the federated loop.
func TestHookClaimSelfResumeBeforeFreshClaimAcrossStores(t *testing.T) {
	rec := &turnBoundClaimRecorder{}
	legs := []hookStore{
		{dir: "/rig-a", env: []string{"BEADS_DIR=/rig-a"}},
		{dir: "/rig-b", env: []string{"BEADS_DIR=/rig-b"}},
	}
	// The primary leg runs the city-wide reader, so it serves BOTH rows; the
	// extra leg stays on its single-store answer.
	run := func(_ string, dir string, _ []string) (string, error) {
		if dir == "/rig-a" {
			return `[` + selfResumeFreshDemand + `,` + selfResumeOwnClaim + `]`, nil
		}
		return `[]`, nil
	}

	ops := rec.ops(t, "")
	ops.Runner = nil
	var stdout, stderr bytes.Buffer
	code := claimHookWorkWithRunner("query", "/rig-a", nil, legs, hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"worker"},
		JSON:               true,
	}, ops, run, func(string, error) {}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	result := decodeTurnBoundResult(t, stdout.String())
	if result.Reason != "existing_assignment" || result.BeadID != "gcg-own" {
		t.Fatalf("result = %+v, want the session's own claim served before any fresh claim", result)
	}
	if len(rec.claims) != 0 {
		t.Fatalf("claims = %v, want none", rec.claims)
	}
}

// TestHookClaimFreshClaimWhenNothingHeldAcrossStores is the control for the
// federation-level invariant: with no own claim anywhere, the fresh demand IS
// claimed. Without it, a hook that never claimed anything would pass the test
// above.
func TestHookClaimFreshClaimWhenNothingHeldAcrossStores(t *testing.T) {
	rec := &turnBoundClaimRecorder{}
	legs := []hookStore{
		{dir: "/rig-a", env: []string{"BEADS_DIR=/rig-a"}},
		{dir: "/rig-b", env: []string{"BEADS_DIR=/rig-b"}},
	}
	run := func(_ string, dir string, _ []string) (string, error) {
		if dir == "/rig-a" {
			return `[` + selfResumeFreshDemand + `]`, nil
		}
		return `[]`, nil
	}

	ops := rec.ops(t, "")
	ops.Runner = nil
	var stdout, stderr bytes.Buffer
	code := claimHookWorkWithRunner("query", "/rig-a", nil, legs, hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"worker"},
		JSON:               true,
	}, ops, run, func(string, error) {}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	result := decodeTurnBoundResult(t, stdout.String())
	if result.Reason != "claimed" || result.BeadID != "gc-fresh" {
		t.Fatalf("result = %+v, want the fresh demand claimed", result)
	}
}

// hardParkedOwnClaim mirrors the gc-qmzou state shape from gc-94to5: a
// load-context (or similar safety-gate) step intentionally left in_progress
// and assigned to this session behind a hard, non-retryable failure
// (gc.outcome=fail, gc.failure_class=hard), so the molecule cannot advance
// past a failed gate. It must never be re-served as existing_assignment.
const hardParkedOwnClaim = `{"id":"gcg-parked","status":"in_progress","assignee":"worker-1","issue_type":"task","metadata":{"gc.routed_to":"worker","gc.outcome":"fail","gc.failure_class":"hard"}}`

// TestHookClaimSkipsHardParkedExistingAssignment pins the gc-94to5 fix: a
// hard-parked own claim must not block a fresh routed demand in the same
// batch from being served. Before the fix, hookClaimExistingAssignment
// unconditionally re-adopted any in_progress+matching-assignee candidate,
// so the parked step would win over the fresh demand and respawn forever.
func TestHookClaimSkipsHardParkedExistingAssignment(t *testing.T) {
	rec := &turnBoundClaimRecorder{}
	output := `[` + hardParkedOwnClaim + `,` + selfResumeFreshDemand + `]`

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"worker"},
		JSON:               true,
	}, rec.ops(t, output), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	result := decodeTurnBoundResult(t, stdout.String())
	if result.BeadID != "gc-fresh" {
		t.Fatalf("result = %+v, want the hard-parked bead skipped and the fresh demand served", result)
	}
	if len(rec.claims) != 1 || rec.claims[0] != "gc-fresh" {
		t.Fatalf("claims = %v, want only [gc-fresh]: the parked bead must never be claimed", rec.claims)
	}
	for _, released := range rec.releases {
		if released == "gcg-parked" {
			t.Fatalf("releases = %v, the parked bead must not be touched at all", rec.releases)
		}
	}
}

// TestHookClaimDrainsWhenOnlyHardParkedAssignmentExists is the exact gc-qmzou
// repro shape from gc-94to5: the ONLY candidate is the session's own
// hard-parked claim. Before the fix this respawned the parked step as
// existing_assignment on every drain-ack tick, forever. The fix must fall
// through to a real no_work drain instead.
func TestHookClaimDrainsWhenOnlyHardParkedAssignmentExists(t *testing.T) {
	rec := &turnBoundClaimRecorder{}
	output := `[` + hardParkedOwnClaim + `]`

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:           "worker-1",
		IdentityCandidates: []string{"worker-1"},
		RouteTargets:       []string{"worker"},
		JSON:               true,
		DrainAck:           true,
	}, rec.ops(t, output), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	result := decodeTurnBoundResult(t, stdout.String())
	if result.Reason != hookClaimReasonNoWork {
		t.Fatalf("result = %+v, want reason %q: a hard-parked step alone must drain, not respawn as existing_assignment", result, hookClaimReasonNoWork)
	}
	if len(rec.claims) != 0 {
		t.Fatalf("claims = %v, want none: the parked bead must never be claimed", rec.claims)
	}
	if len(rec.releases) != 0 {
		t.Fatalf("releases = %v, want none", rec.releases)
	}
	if !rec.drainAcked {
		t.Fatal("drainAcked = false, want true: the drain-ack loop must actually terminate")
	}
}

// TestGcReadyInProgressEnrichesBlockedBy pins the enrichment half of the swap:
// the federated in_progress read carries blocked_by resolved from the leg that
// served the row.
//
// Without it the tier is half-federated — federated discovery, single-store
// enrichment — and the shell's `bd show` resolves nothing for a relocated id.
// Failing open, that serves a gate-blocked step as workable on every tick.
func TestGcReadyInProgressEnrichesBlockedBy(t *testing.T) {
	work := splittest.NewWorkStore(t, "gc")
	graph := splittest.NewClassStore(t, config.BeadClassGraph)

	blocker := mustCreateDrainAckBead(t, graph, beads.Bead{Title: "the gate", Type: "task"}, "", "")
	step := mustCreateDrainAckBead(t, graph, beads.Bead{Title: "gated graph step", Type: "task"}, "in_progress", "worker-1")
	if err := graph.DepAdd(step.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("adding the blocking dep: %v", err)
	}

	rows := readyRowsForAssignee(t, mustReadyLegs(t, "mycity", work, nil, graph), "worker-1")
	if len(rows) != 1 || rows[0].ID != step.ID {
		t.Fatalf("rows = %+v, want the one graph-resident in_progress step", rows)
	}
	if len(rows[0].BlockedBy) != 1 || rows[0].BlockedBy[0].ID != blocker.ID || rows[0].BlockedBy[0].Status != "open" {
		t.Fatalf("blocked_by = %+v, want one OPEN blocker %s", rows[0].BlockedBy, blocker.ID)
	}

	// The hook filter must act on it: a gated resume row is not servable work.
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshaling rows: %v", err)
	}
	if got := filterUnreadyHookCandidates(string(encoded), time.Now()); got != "[]" {
		t.Fatalf("filterUnreadyHookCandidates = %s, want [] for a gated resume row", got)
	}
}

// TestGcReadyInProgressServesUnblockedResumeRow is the control: the same row
// with its blocker CLOSED is served. An enrichment that reported everything as
// blocked would strand every resume, and only this direction catches it.
func TestGcReadyInProgressServesUnblockedResumeRow(t *testing.T) {
	work := splittest.NewWorkStore(t, "gc")
	graph := splittest.NewClassStore(t, config.BeadClassGraph)

	blocker := mustCreateDrainAckBead(t, graph, beads.Bead{Title: "the gate", Type: "task"}, "", "")
	step := mustCreateDrainAckBead(t, graph, beads.Bead{Title: "ungated graph step", Type: "task"}, "in_progress", "worker-1")
	if err := graph.DepAdd(step.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("adding the blocking dep: %v", err)
	}
	if err := graph.Close(blocker.ID); err != nil {
		t.Fatalf("closing the blocker: %v", err)
	}

	rows := readyRowsForAssignee(t, mustReadyLegs(t, "mycity", work, nil, graph), "worker-1")
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the one in_progress step", rows)
	}
	if len(rows[0].BlockedBy) != 1 || rows[0].BlockedBy[0].Status != "closed" {
		t.Fatalf("blocked_by = %+v, want the blocker reported CLOSED", rows[0].BlockedBy)
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshaling rows: %v", err)
	}
	if got := filterUnreadyHookCandidates(string(encoded), time.Now()); got == "[]" {
		t.Fatal("filterUnreadyHookCandidates dropped a resume row whose only blocker is closed")
	}
}

// TestGcReadyNonInProgressArmsSkipEnrichment pins the scope: only the
// crash-recovery arm pays for dependency reads. The ready arm's rows are
// unblocked by construction (the store's own Ready applied readiness), so
// computing blocked_by there would be one dependency read per row to restate it.
func TestGcReadyNonInProgressArmsSkipEnrichment(t *testing.T) {
	work := splittest.NewWorkStore(t, "gc")
	graph := splittest.NewClassStore(t, config.BeadClassGraph)
	blocker := mustCreateDrainAckBead(t, graph, beads.Bead{Title: "the gate", Type: "task"}, "", "")
	step := mustCreateDrainAckBead(t, graph, beads.Bead{Title: "open graph step", Type: "task"}, "", "")
	if err := graph.DepAdd(step.ID, blocker.ID, "blocks"); err != nil {
		t.Fatalf("adding the blocking dep: %v", err)
	}

	legs := mustReadyLegs(t, "mycity", work, nil, graph)
	rows, err := readyBeadsForOpts(legs, readyOpts{status: "open"})
	if err != nil {
		t.Fatalf("readyBeadsForOpts: %v", err)
	}
	for _, row := range rows {
		if row.BlockedBy != nil {
			t.Fatalf("row %s carries blocked_by on the --status open arm: %+v", row.ID, row.BlockedBy)
		}
	}
}

// readyRowsForAssignee runs the crash-recovery read the swapped tier 0 issues.
func readyRowsForAssignee(t *testing.T, legs []readyLeg, assignee string) []readyBead {
	t.Helper()
	rows, err := readyBeadsForOpts(legs, readyOpts{status: readyStatusInProgress, assignee: assignee, limit: 1})
	if err != nil {
		t.Fatalf("readyBeadsForOpts(--status in_progress --assignee %s): %v", assignee, err)
	}
	return rows
}
