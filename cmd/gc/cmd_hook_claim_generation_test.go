package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// advanceClaimGenerationSpy captures the (beadID, assignee, fromGeneration) a
// claim passes through the AdvanceClaimGeneration seam, and lets a test script
// the (next, outcome, err) it returns — proving gc-3ohe47's fence is wired
// through the real claim path, not just unit-tested in isolation on
// internal/beads.
type advanceClaimGenerationSpy struct {
	calls          int
	beadID         string
	assignee       string
	fromGeneration string
	next           string
	outcome        beads.AdvanceClaimGenerationOutcome
	err            error
}

func (s *advanceClaimGenerationSpy) fn(_ context.Context, _ string, _ []string, beadID, assignee, fromGeneration string) (string, beads.AdvanceClaimGenerationOutcome, error) {
	s.calls++
	s.beadID, s.assignee, s.fromGeneration = beadID, assignee, fromGeneration
	if s.err != nil {
		return "", "", s.err
	}
	return s.next, s.outcome, nil
}

// noopAdvanceClaimGeneration suppresses the gc.claim_generation fence so claim
// tests that don't assert on it stay hermetic — the default seam opens a real
// beads.BdStore bound to hookClaimCommandRunnerWithEnvContext, which a test
// binary deliberately refuses (and which other fixtures, e.g. cmd_hook_claim_runid_test.go's
// claimOpsForRunMap, override to pin an unrelated "zero post-claim bd
// mutation" invariant that a live generation write would otherwise trip).
func noopAdvanceClaimGeneration(context.Context, string, []string, string, string, string) (string, beads.AdvanceClaimGenerationOutcome, error) {
	return "", "", nil
}

// TestDoHookClaimAdvancesClaimGenerationOnFreshClaim is the primary gc-3ohe47
// regression: a fresh pool claim (minted) mints gc.claim_generation through the
// AdvanceClaimGeneration seam, passing the empty string as fromGeneration since
// the claimed bead carries no prior generation. Before this fix, a bead claimed
// only through gc hook --claim never called this seam at all, leaving
// gc.claim_generation absent and gc-outcome-close permanently unable to close it
// (gc-ue0tsw).
func TestDoHookClaimAdvancesClaimGenerationOnFreshClaim(t *testing.T) {
	genSpy := &advanceClaimGenerationSpy{next: "1", outcome: beads.AdvanceClaimGenerationAdvanced}
	ops := poolClaimOps(
		`[{"id":"hw-pool","status":"open","metadata":{"gc.routed_to":"worker"}}]`,
		map[string]string{"gc.routed_to": "worker"},
		"bd-hw-pool",
		&stampMetaSpy{},
	)
	ops.AdvanceClaimGeneration = genSpy.fn

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", "/tmp/work", poolClaimOpts(), ops, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookClaim = %d, want 0; stderr=%s", code, stderr.String())
	}
	if genSpy.calls != 1 {
		t.Fatalf("AdvanceClaimGeneration calls = %d, want 1", genSpy.calls)
	}
	if genSpy.beadID != "hw-pool" || genSpy.assignee != "gc__role-mc-sess1" || genSpy.fromGeneration != "" {
		t.Fatalf("AdvanceClaimGeneration call = {bead:%q assignee:%q from:%q}, want {hw-pool gc__role-mc-sess1 \"\"}",
			genSpy.beadID, genSpy.assignee, genSpy.fromGeneration)
	}
}

// TestDoHookClaimAdvanceClaimGenerationReadsExistingGeneration proves a
// re-claimed bead (one already carrying a generation from an earlier fence)
// passes that value through as fromGeneration, rather than treating every fresh
// claim as generation-zero unconditionally: the seam, not the claim path, decides
// whether "" or the prior value is the correct base to advance from.
func TestDoHookClaimAdvanceClaimGenerationReadsExistingGeneration(t *testing.T) {
	genSpy := &advanceClaimGenerationSpy{next: "8", outcome: beads.AdvanceClaimGenerationAdvanced}
	ops := poolClaimOps(
		`[{"id":"hw-regen","status":"open","metadata":{"gc.routed_to":"worker","gc.claim_generation":"7"}}]`,
		map[string]string{"gc.routed_to": "worker", "gc.claim_generation": "7"},
		"bd-hw-regen",
		&stampMetaSpy{},
	)
	ops.AdvanceClaimGeneration = genSpy.fn

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", "/tmp/work", poolClaimOpts(), ops, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookClaim = %d, want 0; stderr=%s", code, stderr.String())
	}
	if genSpy.calls != 1 || genSpy.fromGeneration != "7" {
		t.Fatalf("AdvanceClaimGeneration = {calls:%d from:%q}, want {1 \"7\"}", genSpy.calls, genSpy.fromGeneration)
	}
}

// TestDoHookClaimDoesNotAdvanceClaimGenerationOnAdoption is the direct regression
// test for the race advanceHookClaimGeneration's minted gate exists to prevent: a
// hook tick that ADOPTS a bead this session already owns (existing_assignment,
// no fresh Claim call) must NOT advance the generation. If it did, a caller that
// already read the generation THIS session's earlier claim minted, and is about
// to present it to gc-outcome-close, would be raced out from under itself by its
// own session's later hook ticks, turning a legitimate close into a spurious
// stale refusal.
func TestDoHookClaimDoesNotAdvanceClaimGenerationOnAdoption(t *testing.T) {
	genSpy := &advanceClaimGenerationSpy{}
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) {
			return `[{"id":"hw-adopt","status":"in_progress","assignee":"gc__role-mc-sess1","metadata":{"gc.routed_to":"worker","gc.claim_generation":"3"}}]`, nil
		},
		Claim: func(context.Context, string, []string, string, string) (beads.Bead, bool, error) {
			t.Error("Claim must not be called on the existing-assignment path")
			return beads.Bead{}, false, nil
		},
		ResolveWorkBranch:      func(string) string { return "" },
		StampWorkMeta:          noopStampWorkMeta,
		PublishRunMap:          noopPublishRunMap,
		StampSessionClaim:      noopStampSessionClaim,
		AdvanceClaimGeneration: genSpy.fn,
	}

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", "/tmp/work", poolClaimOpts(), ops, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookClaim = %d, want 0; stderr=%s", code, stderr.String())
	}
	if genSpy.calls != 0 {
		t.Fatalf("AdvanceClaimGeneration calls = %d, want 0 on adoption (minted gate must skip it)", genSpy.calls)
	}
}

// TestDoHookClaimAdvanceClaimGenerationFailureDoesNotFailClaim proves the
// generation advance is best-effort, matching this claim path's existing posture
// for gc.work_branch/gc.session_id/gc.claimed_at: a claim already won by
// ops.Claim stands regardless of whether the generation could be advanced. The
// failure is surfaced on stderr, never as a claim failure.
func TestDoHookClaimAdvanceClaimGenerationFailureDoesNotFailClaim(t *testing.T) {
	genSpy := &advanceClaimGenerationSpy{err: errors.New("dolt boom")}
	ops := poolClaimOps(
		`[{"id":"hw-err","status":"open","metadata":{"gc.routed_to":"worker"}}]`,
		map[string]string{"gc.routed_to": "worker"},
		"bd-hw-err",
		&stampMetaSpy{},
	)
	ops.AdvanceClaimGeneration = genSpy.fn

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", "/tmp/work", poolClaimOpts(), ops, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookClaim = %d, want 0 (generation-advance error must not fail the claim); stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("advancing claim generation on hw-err")) {
		t.Errorf("stderr = %q, want the failed generation advance surfaced", stderr.String())
	}
}

// TestDoHookClaimAdvanceClaimGenerationStaleDoesNotFailClaim covers the
// definitive-refusal outcome: bd's assignee fence missed (the assignee moved
// between the claim and this call), so nothing was written. Like the error case,
// this must not fail the claim — but it also must not silently look identical
// to success; it is logged so the gap is diagnosable.
func TestDoHookClaimAdvanceClaimGenerationStaleDoesNotFailClaim(t *testing.T) {
	genSpy := &advanceClaimGenerationSpy{outcome: beads.AdvanceClaimGenerationStale}
	ops := poolClaimOps(
		`[{"id":"hw-stale","status":"open","metadata":{"gc.routed_to":"worker"}}]`,
		map[string]string{"gc.routed_to": "worker"},
		"bd-hw-stale",
		&stampMetaSpy{},
	)
	ops.AdvanceClaimGeneration = genSpy.fn

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", "/tmp/work", poolClaimOpts(), ops, &stdout, &stderr); code != 0 {
		t.Fatalf("doHookClaim = %d, want 0 (a stale generation fence must not fail the claim); stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("claim generation fence on hw-stale refused")) {
		t.Errorf("stderr = %q, want the stale fence surfaced", stderr.String())
	}
}
