package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// sessionWorkDirOps builds a hookClaimOps whose branch resolver answers only
// for the named checkout, so a test proves WHICH directory the patch resolved
// from rather than merely that some branch appeared.
func sessionWorkDirOps(sessionDir, dirWithBranch, branch string) hookClaimOps {
	return hookClaimOps{
		ResolveWorkBranch: func(dir string) string {
			if dir == dirWithBranch {
				return branch
			}
			return ""
		},
		ResolveSessionWorkDir: func(string) string { return sessionDir },
	}
}

// TestHookClaimIdentityPatchStampsSessionWorkDir is gc-2n4c: a bead routed to a
// pool records no gc.work_dir, because the router normalizes a pool target to a
// pool identity and no slot is chosen until a claim happens. The claiming
// session is the first actor that knows the concrete worktree, so the claim
// must stamp it — and must resolve gc.work_branch from it in the SAME patch,
// rather than leaving both keys for a later reconcile tick.
func TestHookClaimIdentityPatchStampsSessionWorkDir(t *testing.T) {
	bead := beads.Bead{ID: "hw-pool", Status: "open", Metadata: map[string]string{
		beadmeta.KindMetadataKey: "worker",
	}}
	opts := hookClaimOptions{Env: []string{"GC_SESSION_ID=mc-sess1"}}

	patch := hookClaimIdentityPatch(bead, opts, sessionWorkDirOps("/w/slot-3", "/w/slot-3", "feat/x"))

	if got := patch[beadmeta.WorkDirMetadataKey]; got != "/w/slot-3" {
		t.Fatalf("patch[%s] = %q, want the claiming session's own work_dir", beadmeta.WorkDirMetadataKey, got)
	}
	if got := patch[beadmeta.WorkBranchMetadataKey]; got != "feat/x" {
		t.Fatalf("patch[%s] = %q, want the branch resolved from the session work_dir on the FIRST claim", beadmeta.WorkBranchMetadataKey, got)
	}
}

// TestHookClaimIdentityPatchPrefersRecordedWorkDir keeps the session dir a
// FALLBACK. A bead that already records its own checkout has an intent the
// claim must not overwrite: the recorded value is what the work-record close
// gate resolves its repo from, and a claiming session's dir is only a guess at
// the same tree.
func TestHookClaimIdentityPatchPrefersRecordedWorkDir(t *testing.T) {
	bead := beads.Bead{ID: "hw-recorded", Status: "open", Metadata: map[string]string{
		beadmeta.KindMetadataKey:      "worker",
		beadmeta.WorkDirMetadataKey:   "/w/recorded",
		beadmeta.ClaimedAtMetadataKey: "2026-08-01T00:00:00Z",
	}}
	opts := hookClaimOptions{Env: []string{"GC_SESSION_ID=mc-sess1"}}

	patch := hookClaimIdentityPatch(bead, opts, sessionWorkDirOps("/w/slot-3", "/w/recorded", "feat/recorded"))

	if _, ok := patch[beadmeta.WorkDirMetadataKey]; ok {
		t.Fatalf("patch = %v, want no gc.work_dir rewrite on a bead that records one", patch)
	}
	if got := patch[beadmeta.WorkBranchMetadataKey]; got != "feat/recorded" {
		t.Fatalf("patch[%s] = %q, want the branch from the RECORDED checkout", beadmeta.WorkBranchMetadataKey, got)
	}
}

// TestHookClaimIdentityPatchSkipsSessionWorkDirOnControlBead holds control beads
// session-free, the same boundary the session back-reference keys observe
// (graphroute's ApplyGraphControlRouteBinding). A control-dispatcher session
// claiming a control bead must not bind its own checkout to it.
func TestHookClaimIdentityPatchSkipsSessionWorkDirOnControlBead(t *testing.T) {
	bead := beads.Bead{ID: "hw-ctrl", Status: "open", Metadata: map[string]string{
		beadmeta.KindMetadataKey: "check",
	}}
	if !beadmeta.IsControlKind("check") {
		t.Fatalf(`IsControlKind("check") = false, test fixture no longer names a control kind`)
	}
	opts := hookClaimOptions{Env: []string{"GC_SESSION_ID=mc-sess1"}}

	patch := hookClaimIdentityPatch(bead, opts, sessionWorkDirOps("/w/slot-3", "/w/slot-3", "feat/x"))

	if _, ok := patch[beadmeta.WorkDirMetadataKey]; ok {
		t.Fatalf("patch = %v, want no gc.work_dir on a control bead", patch)
	}
}

// TestHookClaimIdentityPatchNoSessionWorkDirLeavesKeysUnset covers the honest
// no-op: a claim outside any session, or one whose session bead records no
// checkout, stamps neither key rather than inventing a path.
func TestHookClaimIdentityPatchNoSessionWorkDirLeavesKeysUnset(t *testing.T) {
	bead := beads.Bead{ID: "hw-nosess", Status: "open", Metadata: map[string]string{
		beadmeta.KindMetadataKey: "worker",
	}}
	opts := hookClaimOptions{Env: []string{"GC_SESSION_ID=mc-sess1"}}

	patch := hookClaimIdentityPatch(bead, opts, sessionWorkDirOps("", "/w/slot-3", "feat/x"))

	if _, ok := patch[beadmeta.WorkDirMetadataKey]; ok {
		t.Fatalf("patch = %v, want no gc.work_dir when the session records none", patch)
	}
	if _, ok := patch[beadmeta.WorkBranchMetadataKey]; ok {
		t.Fatalf("patch = %v, want no gc.work_branch when no checkout is knowable", patch)
	}
}
