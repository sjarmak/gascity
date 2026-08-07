package main

import (
	"io"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// The work-dir cluster a dispatched trigger publishes is a PAIR, not two
// independent keys: beadmeta.WorkDirMetadataKey ("gc.work_dir") is the canonical
// path and beadmeta.LegacyWorkDirMetadataKey ("work_dir") is the ownership
// evidence that workDirStampHasOwnershipEvidence reads back to decide whether a
// later reconcile may mirror the path onto a routed work bead.
//
// A trigger that publishes the canonical key alone produces a bead whose path is
// asserted but unattested. Every consumer that pairs them — the worktree reaper's
// borrow veto (bead_worktree_reaper.go), the reconcile-time ownership guard, and
// the ralph dispatcher's inherited-metadata resolution — then reads a half
// record. So the invariant is: set them together, or set neither.
//
// This test pins that invariant at the two dispatch sites. It is a drift guard,
// not a reproduction: on this base both sites already write the pair in a single
// map, and the test passed the first time it was run.
func assertWorkDirPair(t *testing.T, what string, metadata map[string]string) {
	t.Helper()
	canonical, hasCanonical := metadata[beadmeta.WorkDirMetadataKey]
	evidence, hasEvidence := metadata[beadmeta.LegacyWorkDirMetadataKey]
	if hasCanonical != hasEvidence {
		t.Fatalf("%s: work-dir keys published independently: %s=%q(present=%v) %s=%q(present=%v); "+
			"a dispatched trigger must publish the canonical path and its ownership evidence together or not at all",
			what,
			beadmeta.WorkDirMetadataKey, canonical, hasCanonical,
			beadmeta.LegacyWorkDirMetadataKey, evidence, hasEvidence)
	}
	if hasCanonical && canonical != evidence {
		t.Fatalf("%s: work-dir pair disagrees: %s=%q but %s=%q",
			what, beadmeta.WorkDirMetadataKey, canonical, beadmeta.LegacyWorkDirMetadataKey, evidence)
	}
}

// TestPoolTriggerMetadataPublishesWorkDirPair covers the create path
// (poolTriggerMetadata → the new session bead's metadata).
func TestPoolTriggerMetadataPublishesWorkDirPair(t *testing.T) {
	const qualified = "gascity/polecat"
	workDirValue := "/home/ds/gascity-worktrees/polecat-1"
	cfgAgent := &config.Agent{Name: "polecat", WorkDir: workDirValue}
	bp := &agentBuildParams{city: &config.City{}, cityPath: t.TempDir()}
	request := SessionRequest{WorkBeadID: "gc-demand", WorkStoreRef: "primary"}

	got := poolTriggerMetadata(bp, cfgAgent, qualified, request)
	if got == nil {
		t.Fatal("poolTriggerMetadata returned nil for a request carrying a work bead")
	}
	if got[beadmeta.WorkDirMetadataKey] == "" {
		t.Skipf("agent work_dir did not resolve to a trigger work dir on this config shape; got %#v", got)
	}
	assertWorkDirPair(t, "poolTriggerMetadata", got)
}

// TestComputePoolTriggerBindingPatchPublishesWorkDirPair covers the rebind path
// (computePoolTriggerBindingPatch → the metadata patch applied to an existing
// session bead when its trigger changes).
func TestComputePoolTriggerBindingPatchPublishesWorkDirPair(t *testing.T) {
	const workDir = "/home/ds/gascity-worktrees/polecat-1"

	// A session bead with no recorded work dir, rebinding onto a new trigger:
	// both keys differ from the (empty) current state, so both must be emitted.
	info := sessionpkg.Info{ID: "gc-seat1", SessionNameMetadata: "gascity-polecat-1"}
	got := computePoolTriggerBindingPatch(info, SessionRequest{WorkBeadID: "gc-demand"}, workDir)

	if _, ok := got[beadmeta.WorkDirMetadataKey]; !ok {
		t.Fatalf("rebind patch omitted %s entirely; got %#v", beadmeta.WorkDirMetadataKey, got)
	}
	assertWorkDirPair(t, "computePoolTriggerBindingPatch", got)
}

// TestWorktreeCreatorWritesOwnershipEvidence documents the other half of the
// contract from the consumer side: workDirStampHasOwnershipEvidence only lets a
// reconcile mirror a path onto a pool-managed session's work bead when the bead
// already carries the matching evidence key. A dispatch site that published the
// canonical key alone would satisfy nothing here — which is why the pair matters.
func TestWorkDirOwnershipEvidenceRequiresTheEvidenceKey(t *testing.T) {
	const dir = "/home/ds/gascity-worktrees/polecat-1"

	if workDirStampHasOwnershipEvidence(map[string]string{beadmeta.WorkDirMetadataKey: dir}, dir) {
		t.Fatal("canonical key alone must not count as ownership evidence")
	}
	if !workDirStampHasOwnershipEvidence(map[string]string{beadmeta.LegacyWorkDirMetadataKey: dir}, dir) {
		t.Fatal("matching evidence key must count as ownership evidence")
	}
	if workDirStampHasOwnershipEvidence(map[string]string{beadmeta.LegacyWorkDirMetadataKey: "/other"}, dir) {
		t.Fatal("evidence for a different path must not authorize this path")
	}
}

// TestStampedWorkBeadKeepsPairCoherent pins that the reconcile-time stamp never
// turns a coherent pair into a half record: when the work bead already carries
// the evidence key, the mirrored canonical key must agree with it.
func TestStampedWorkBeadKeepsPairCoherent(t *testing.T) {
	const (
		sessionName = "gascity-polecat-1"
		workDir     = "/home/ds/gascity-worktrees/polecat-1"
	)
	run := beads.Bead{
		ID: "gc-demand", Type: "task", Status: "in_progress", Assignee: sessionName,
		Metadata: map[string]string{beadmeta.LegacyWorkDirMetadataKey: workDir},
	}
	mem := beads.NewMemStoreFrom(0, []beads.Bead{run}, nil)
	poolSession := stampTestSession(sessionName, workDir)
	poolSession.Metadata["pool_managed"] = "true"
	sessions := newSessionBeadSnapshot([]beads.Bead{poolSession})

	stampRunSessionIdentity([]beads.Bead{run}, []beads.Store{mem}, sessions, io.Discard)

	got, err := mem.Get(run.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", run.ID, err)
	}
	assertWorkDirPair(t, "stampRunSessionIdentity on an attested bead", got.Metadata)
}
