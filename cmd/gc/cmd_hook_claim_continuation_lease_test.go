package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/molecule"
)

// TestHookClaimSkipsLeaseForMissingContinuation pins the no-op half of
// acquireHookContinuationLease: a bead marked gc.session_affinity=require but
// missing its root/group (a formula that stamped affinity without routing the
// bead through graph.v2's continuation machinery) must claim exactly like
// ordinary work, and the lease seams must never be called — there is no root
// to lease.
func TestHookClaimSkipsLeaseForMissingContinuation(t *testing.T) {
	const work = `[{"id":"work-1","status":"open","metadata":{"gc.routed_to":"worker","gc.session_affinity":"require"}}]`
	rec := &turnBoundClaimRecorder{}
	ops := rec.ops(t, work)
	ops.AcquireContinuationLease = func(context.Context, string, []string, string, string, string) (molecule.ContinuationLeaseOutcome, error) {
		t.Fatal("AcquireContinuationLease called for a bead with no root/group")
		return "", nil
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:     "worker-1",
		RouteTargets: []string{"worker"},
		JSON:         true,
	}, ops, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if len(rec.claims) != 1 || rec.claims[0] != "work-1" {
		t.Fatalf("claims = %v, want [work-1]", rec.claims)
	}
	if len(rec.releases) != 0 {
		t.Fatalf("releases = %v, want none", rec.releases)
	}
	result := decodeTurnBoundResult(t, stdout.String())
	if result.Action != "work" || result.BeadID != "work-1" {
		t.Fatalf("result = %+v, want action=work bead=work-1", result)
	}
}

// TestHookClaimUnwindsOnLeaseDenied pins the "crash between lease/CAS/launch"
// scenario from gc-jspv: the claim CAS wins, but this session cannot acquire
// the workflow root's continuation lease (a concurrent session already holds
// it). The claim must be given back exactly like the other undeliverable-claim
// cases (F-C, straddle) rather than executed without the affinity guarantee
// gc.session_affinity=require promised the caller.
func TestHookClaimUnwindsOnLeaseDenied(t *testing.T) {
	const work = `[{"id":"work-1","status":"open","metadata":{"gc.routed_to":"worker","gc.root_bead_id":"root-1","gc.continuation_group":"grp-1","gc.session_affinity":"require"}}]`
	rec := &turnBoundClaimRecorder{}
	ops := rec.ops(t, work)
	var leaseCalls []string
	ops.AcquireContinuationLease = func(_ context.Context, _ string, _ []string, rootID, group, sessionID string) (molecule.ContinuationLeaseOutcome, error) {
		leaseCalls = append(leaseCalls, rootID+"|"+group+"|"+sessionID)
		return molecule.ContinuationLeaseHeldByOther, nil
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:     "worker-1",
		RouteTargets: []string{"worker"},
		JSON:         true,
	}, ops, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("code = %d, want 1; stdout=%q stderr=%s", code, stdout.String(), stderr.String())
	}
	if len(rec.claims) != 1 || rec.claims[0] != "work-1" {
		t.Fatalf("claims = %v, want exactly [work-1]: the CAS must still have been attempted", rec.claims)
	}
	if len(rec.releases) != 1 || rec.releases[0] != "work-1" {
		t.Fatalf("releases = %v, want [work-1]: a lease-denied claim must be given back", rec.releases)
	}
	if len(rec.claimReleased) != 1 || rec.claimReleased[0].BeadID != "work-1" || rec.claimReleased[0].Reason != hookClaimReleaseReasonLeaseDenied {
		t.Fatalf("bead.claim_released events = %+v, want one for work-1 with reason %s", rec.claimReleased, hookClaimReleaseReasonLeaseDenied)
	}
	if len(leaseCalls) != 1 || leaseCalls[0] != "root-1|grp-1|worker-1" {
		t.Fatalf("lease calls = %v, want [root-1|grp-1|worker-1]", leaseCalls)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty: an unwound claim writes no result line", stdout.String())
	}
}

// TestHookClaimUnwindsOnLeaseAcquireError is the error-path sibling of
// TestHookClaimUnwindsOnLeaseDenied: the lease seam itself fails (a crash or
// timeout between the claim CAS and the lease write) rather than resolving to
// a definite outcome. That must fail closed exactly like an explicit denial —
// silently executing on an unconfirmed lease would be worse than refusing.
func TestHookClaimUnwindsOnLeaseAcquireError(t *testing.T) {
	const work = `[{"id":"work-1","status":"open","metadata":{"gc.routed_to":"worker","gc.root_bead_id":"root-1","gc.continuation_group":"grp-1","gc.session_affinity":"require"}}]`
	rec := &turnBoundClaimRecorder{}
	ops := rec.ops(t, work)
	ops.AcquireContinuationLease = func(context.Context, string, []string, string, string, string) (molecule.ContinuationLeaseOutcome, error) {
		return "", errors.New("bd store unavailable")
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:     "worker-1",
		RouteTargets: []string{"worker"},
		JSON:         true,
	}, ops, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("code = %d, want 1; stdout=%q stderr=%s", code, stdout.String(), stderr.String())
	}
	if len(rec.releases) != 1 || rec.releases[0] != "work-1" {
		t.Fatalf("releases = %v, want [work-1]", rec.releases)
	}
	if len(rec.claimReleased) != 1 || rec.claimReleased[0].Reason != hookClaimReleaseReasonLeaseDenied {
		t.Fatalf("bead.claim_released events = %+v, want reason %s", rec.claimReleased, hookClaimReleaseReasonLeaseDenied)
	}
}

// TestReorderHookClaimCandidatesDropsUnrelatedRootFallback pins graph.v2's "no
// unrelated-root fallback": a candidate whose workflow root's continuation
// lease is actively held by a DIFFERENT live session must be dropped from the
// fresh-claim attempt order entirely, even when it sorts first in the
// work-query output and an ordinary candidate is available right behind it.
// Without the fence, claimFirstEligibleHookCandidate would attempt it, lose to
// the lease denial, and only then fall through — spending a doomed claim
// attempt (and a claim-rejected event) on work this session can never execute
// under session_affinity=require.
func TestReorderHookClaimCandidatesDropsUnrelatedRootFallback(t *testing.T) {
	const work = `[
		{"id":"leased-1","status":"open","metadata":{"gc.routed_to":"worker","gc.root_bead_id":"root-1","gc.continuation_group":"grp-1","gc.session_affinity":"require"}},
		{"id":"plain-1","status":"open","metadata":{"gc.routed_to":"worker"}}
	]`
	rec := &turnBoundClaimRecorder{}
	ops := rec.ops(t, work)
	ops.ContinuationLeaseHolder = func(_ context.Context, _ string, _ []string, rootID string) (string, error) {
		if rootID != "root-1" {
			t.Fatalf("ContinuationLeaseHolder called for unexpected root %q", rootID)
		}
		return "other-session", nil
	}
	ops.AcquireContinuationLease = func(context.Context, string, []string, string, string, string) (molecule.ContinuationLeaseOutcome, error) {
		t.Fatal("AcquireContinuationLease called for a candidate the reorder should have dropped")
		return "", nil
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:     "worker-1",
		RouteTargets: []string{"worker"},
		JSON:         true,
	}, ops, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if len(rec.claims) != 1 || rec.claims[0] != "plain-1" {
		t.Fatalf("claims = %v, want exactly [plain-1]: leased-1 must never be attempted", rec.claims)
	}
	result := decodeTurnBoundResult(t, stdout.String())
	if result.Action != "work" || result.BeadID != "plain-1" {
		t.Fatalf("result = %+v, want action=work bead=plain-1", result)
	}
}

// TestReorderHookClaimCandidatesPromotesOwnedRoot pins the positive half of
// root-aware claim precedence: a candidate whose workflow root THIS session
// already holds the continuation lease for must be attempted BEFORE unrelated
// generic work, even when it sorts second in the work-query output — the
// session should keep executing the same root rather than wander onto
// unrelated work while its own continuation sits ready.
func TestReorderHookClaimCandidatesPromotesOwnedRoot(t *testing.T) {
	const work = `[
		{"id":"plain-1","status":"open","metadata":{"gc.routed_to":"worker"}},
		{"id":"owned-1","status":"open","metadata":{"gc.routed_to":"worker","gc.root_bead_id":"root-1","gc.continuation_group":"grp-1","gc.session_affinity":"require"}}
	]`
	rec := &turnBoundClaimRecorder{}
	ops := rec.ops(t, work)
	ops.ContinuationLeaseHolder = func(_ context.Context, _ string, _ []string, rootID string) (string, error) {
		if rootID != "root-1" {
			t.Fatalf("ContinuationLeaseHolder called for unexpected root %q", rootID)
		}
		return "worker-1", nil
	}
	ops.AcquireContinuationLease = func(_ context.Context, _ string, _ []string, rootID, group, sessionID string) (molecule.ContinuationLeaseOutcome, error) {
		if rootID != "root-1" || group != "grp-1" || sessionID != "worker-1" {
			t.Fatalf("AcquireContinuationLease(%q,%q,%q), want root-1/grp-1/worker-1", rootID, group, sessionID)
		}
		return molecule.ContinuationLeaseAcquired, nil
	}
	// The claim reaches sibling preassignment on a successful lease, so it
	// needs a stub in place of the real bd-backed default this test's fake
	// binary directory cannot satisfy. No siblings to assign here.
	ops.ListContinuation = func(context.Context, string, []string, string, string) ([]beads.Bead, error) {
		return nil, nil
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("query", "/rig", hookClaimOptions{
		Assignee:     "worker-1",
		RouteTargets: []string{"worker"},
		JSON:         true,
	}, ops, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if len(rec.claims) != 1 || rec.claims[0] != "owned-1" {
		t.Fatalf("claims = %v, want exactly [owned-1]: the owned root must be attempted first", rec.claims)
	}
	result := decodeTurnBoundResult(t, stdout.String())
	if result.Action != "work" || result.BeadID != "owned-1" {
		t.Fatalf("result = %+v, want action=work bead=owned-1", result)
	}
}
