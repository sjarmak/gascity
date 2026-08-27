package main

import (
	"context"
	"io"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// affineCandidate builds an open, unassigned pool-routed step of the shape
// graphroute.ApplyGraphRouteBinding stamps for a MetadataOnly binding:
// gc.session_affinity=require with no session pin and no assignee (gc-9tql).
const (
	testContinuationRootID = "root-1"
	testContinuationGroup  = "grp-1"
)

func affineCandidate(id string) beads.Bead {
	rootID, group := testContinuationRootID, testContinuationGroup
	return beads.Bead{
		ID:     id,
		Status: "open",
		Type:   "task",
		Metadata: map[string]string{
			beadmeta.SessionAffinityMetadataKey:   "require",
			beadmeta.ContinuationGroupMetadataKey: group,
			beadmeta.RootBeadIDMetadataKey:        rootID,
			beadmeta.RoutedToMetadataKey:          "rig/pool",
		},
	}
}

func ownerOpsFor(owner string, calls *[]string) hookClaimOps {
	return hookClaimOps{
		ResolveContinuationOwner: func(_ context.Context, _ string, _ []string, rootID string) (string, error) {
			*calls = append(*calls, rootID)
			return owner, nil
		},
		Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
			b := affineCandidate(beadID)
			b.Status = "in_progress"
			b.Assignee = assignee
			return b, true, nil
		},
	}
}

// TestHookClaimSkipsStepOwnedByAnotherLiveSession is the gc-9tql regression:
// a pool-routed affinity=require step whose workflow root is already bound to a
// DIFFERENT live session must not be advertised to this slot, even though the
// step itself carries no session pin and no assignee.
func TestHookClaimSkipsStepOwnedByAnotherLiveSession(t *testing.T) {
	var resolved []string
	ops := ownerOpsFor("polecat-1", &resolved)
	ops.applyDefaults()
	opts := hookClaimOptions{
		Assignee:           "polecat-2",
		IdentityCandidates: []string{"polecat-2"},
		RouteTargets:       []string{"rig/pool"},
	}
	candidates := []beads.Bead{affineCandidate("gc-4rpf")}

	res := claimFirstEligibleHookCandidate(candidates, opts, ops, t.TempDir(), io.Discard, io.Discard)

	if res.terminal {
		t.Fatalf("claimed a step owned by another live session; want no claimable work")
	}
	if len(resolved) != 1 || resolved[0] != "root-1" {
		t.Errorf("continuation owner resolved for %v, want exactly [root-1]", resolved)
	}
}

// TestHookClaimClaimsStepOwnedByThisSession preserves the resume path: the slot
// that already owns the group must still be able to pick up a later-ready step.
func TestHookClaimClaimsStepOwnedByThisSession(t *testing.T) {
	var resolved []string
	ops := ownerOpsFor("polecat-2", &resolved)
	ops.applyDefaults()
	opts := hookClaimOptions{
		Assignee:           "polecat-2",
		IdentityCandidates: []string{"polecat-2"},
		RouteTargets:       []string{"rig/pool"},
	}
	candidates := []beads.Bead{affineCandidate("gc-4rpf")}

	if res := claimFirstEligibleHookCandidate(candidates, opts, ops, t.TempDir(), io.Discard, io.Discard); !res.terminal {
		t.Fatalf("own-group step was not claimable; want a claim")
	}
}

// TestHookClaimClaimsUnownedAffineStep preserves gc-zf4's contract: fresh pool
// work whose root is not yet bound to any session stays claimable by anybody.
func TestHookClaimClaimsUnownedAffineStep(t *testing.T) {
	var resolved []string
	ops := ownerOpsFor("", &resolved)
	ops.applyDefaults()
	opts := hookClaimOptions{
		Assignee:           "polecat-2",
		IdentityCandidates: []string{"polecat-2"},
		RouteTargets:       []string{"rig/pool"},
	}
	candidates := []beads.Bead{affineCandidate("gc-4rpf")}

	if res := claimFirstEligibleHookCandidate(candidates, opts, ops, t.TempDir(), io.Discard, io.Discard); !res.terminal {
		t.Fatalf("unowned affine step was not claimable; want a claim")
	}
}

// TestHookClaimDoesNotResolveOwnerForNonAffineCandidate keeps the extra store
// read off the ordinary claim path: a candidate with no affinity requirement
// must not trigger an owner lookup.
func TestHookClaimDoesNotResolveOwnerForNonAffineCandidate(t *testing.T) {
	var resolved []string
	ops := ownerOpsFor("polecat-1", &resolved)
	ops.applyDefaults()
	opts := hookClaimOptions{
		Assignee:           "polecat-2",
		IdentityCandidates: []string{"polecat-2"},
		RouteTargets:       []string{"rig/pool"},
	}
	plain := affineCandidate("gc-plain")
	delete(plain.Metadata, beadmeta.SessionAffinityMetadataKey)

	if res := claimFirstEligibleHookCandidate([]beads.Bead{plain}, opts, ops, t.TempDir(), io.Discard, io.Discard); !res.terminal {
		t.Fatalf("non-affine candidate was not claimable; want a claim")
	}
	if len(resolved) != 0 {
		t.Errorf("resolved continuation owner for a non-affine candidate: %v", resolved)
	}
}
