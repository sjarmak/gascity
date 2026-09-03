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

// hookClaimReleaseSpy records the (beadID, assignee) an unwind releases and
// always reports success, so a test can confirm a minted claim that fails its
// generation fence is actually given back rather than delivered.
type hookClaimReleaseSpy struct {
	calls    int
	beadID   string
	assignee string
}

func (s *hookClaimReleaseSpy) fn(_ context.Context, _ string, _ []string, beadID, assignee string) (bool, error) {
	s.calls++
	s.beadID, s.assignee = beadID, assignee
	return true, nil
}

// noopAdvanceClaimGeneration reports a clean not-advanced outcome (no error,
// no confirmed generation) for tests that deliberately exercise the
// generation-unfenced unwind path without scripting a spy. Since gc-3ohe47's
// review fix, this is no longer a silent no-op for a minted claim: the
// caller (advanceHookClaimGeneration) now unwinds any minted claim whose
// generation is not confirmed advanced, so wiring this into a minted-claim
// test deliberately drives that unwind. Tests that mint a claim and expect it
// delivered must use advanceClaimGenerationOK instead.
func noopAdvanceClaimGeneration(context.Context, string, []string, string, string, string) (string, beads.AdvanceClaimGenerationOutcome, error) {
	return "", "", nil
}

// advanceClaimGenerationOK is the hermetic stand-in for a successful claim
// generation fence: it always reports Advanced with a fixed placeholder next
// value. Since gc-3ohe47's review fix, a minted claim is unwound unless this
// seam confirms Advanced, so every minted-claim test that does not otherwise
// stub or spy on AdvanceClaimGeneration must reach for this one to stay
// hermetic — the real production seam (hookAdvanceClaimGenerationWithBdStore)
// shells out to bd, which fails against a test fixture's directory and would
// now trigger a spurious unwind instead of being silently swallowed.
func advanceClaimGenerationOK(context.Context, string, []string, string, string, string) (string, beads.AdvanceClaimGenerationOutcome, error) {
	return "1", beads.AdvanceClaimGenerationAdvanced, nil
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

// TestDoHookClaimAdvanceClaimGenerationFailureUnwindsTheClaim is the HIGH #1
// regression from the gc-3ohe47 review: the generation advance is NOT
// best-effort. A claim already won by ops.Claim must NOT be delivered when its
// generation cannot be confirmed — an infra error advancing the fence unwinds
// the just-minted claim (release, not deliver) rather than handing out a claim
// with no current-authority token gc-outcome-close could ever verify.
func TestDoHookClaimAdvanceClaimGenerationFailureUnwindsTheClaim(t *testing.T) {
	genSpy := &advanceClaimGenerationSpy{err: errors.New("dolt boom")}
	releaseSpy := &hookClaimReleaseSpy{}
	ops := poolClaimOps(
		`[{"id":"hw-err","status":"open","metadata":{"gc.routed_to":"worker"}}]`,
		map[string]string{"gc.routed_to": "worker"},
		"bd-hw-err",
		&stampMetaSpy{},
	)
	ops.AdvanceClaimGeneration = genSpy.fn
	ops.Release = releaseSpy.fn
	ops.EmitClaimReleased = func(hookClaimReleaseRecord) {}

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", "/tmp/work", poolClaimOpts(), ops, &stdout, &stderr); code != 1 {
		t.Fatalf("doHookClaim = %d, want 1 (a generation-advance error must unwind the claim, not deliver it); stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("advancing claim generation on hw-err")) {
		t.Errorf("stderr = %q, want the failed generation advance surfaced", stderr.String())
	}
	if releaseSpy.calls != 1 || releaseSpy.beadID != "hw-err" {
		t.Fatalf("Release calls = %d beadID = %q, want exactly one release of hw-err", releaseSpy.calls, releaseSpy.beadID)
	}
}

// TestDoHookClaimAdvanceClaimGenerationUnconfirmedUnwindsTheClaim covers the
// catch-all outcome: the seam reports no error and no recognized failure
// outcome, but also no confirmed generation (the noopAdvanceClaimGeneration
// stub's "clean no-op" shape). This must unwind exactly like the error and
// stale cases — an unconfirmed generation is exactly as undeliverable as an
// absent one, and advanceHookClaimGeneration must not treat empty-but-no-error
// as success.
func TestDoHookClaimAdvanceClaimGenerationUnconfirmedUnwindsTheClaim(t *testing.T) {
	releaseSpy := &hookClaimReleaseSpy{}
	ops := poolClaimOps(
		`[{"id":"hw-unconfirmed","status":"open","metadata":{"gc.routed_to":"worker"}}]`,
		map[string]string{"gc.routed_to": "worker"},
		"bd-hw-unconfirmed",
		&stampMetaSpy{},
	)
	ops.AdvanceClaimGeneration = noopAdvanceClaimGeneration
	ops.Release = releaseSpy.fn
	ops.EmitClaimReleased = func(hookClaimReleaseRecord) {}

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", "/tmp/work", poolClaimOpts(), ops, &stdout, &stderr); code != 1 {
		t.Fatalf("doHookClaim = %d, want 1 (an unconfirmed generation fence must unwind the claim, not deliver it); stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("claim generation fence on hw-unconfirmed returned no confirmed generation")) {
		t.Errorf("stderr = %q, want the unconfirmed fence surfaced", stderr.String())
	}
	if releaseSpy.calls != 1 || releaseSpy.beadID != "hw-unconfirmed" {
		t.Fatalf("Release calls = %d beadID = %q, want exactly one release of hw-unconfirmed", releaseSpy.calls, releaseSpy.beadID)
	}
}

// TestDoHookClaimAdvanceClaimGenerationStaleUnwindsTheClaim covers the
// definitive-refusal outcome: bd's assignee fence missed (the assignee moved
// between the claim and this call), so nothing was written. Like the error
// case, this must unwind rather than deliver — a stale fence means the
// generation gc-outcome-close would see is not this claim's, so this claim
// cannot be handed out with authority it does not hold.
func TestDoHookClaimAdvanceClaimGenerationStaleUnwindsTheClaim(t *testing.T) {
	genSpy := &advanceClaimGenerationSpy{outcome: beads.AdvanceClaimGenerationStale}
	releaseSpy := &hookClaimReleaseSpy{}
	ops := poolClaimOps(
		`[{"id":"hw-stale","status":"open","metadata":{"gc.routed_to":"worker"}}]`,
		map[string]string{"gc.routed_to": "worker"},
		"bd-hw-stale",
		&stampMetaSpy{},
	)
	ops.AdvanceClaimGeneration = genSpy.fn
	ops.Release = releaseSpy.fn
	ops.EmitClaimReleased = func(hookClaimReleaseRecord) {}

	var stdout, stderr bytes.Buffer
	if code := doHookClaim("bd ready --json", "/tmp/work", poolClaimOpts(), ops, &stdout, &stderr); code != 1 {
		t.Fatalf("doHookClaim = %d, want 1 (a stale generation fence must unwind the claim, not deliver it); stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("claim generation fence on hw-stale refused")) {
		t.Errorf("stderr = %q, want the stale fence surfaced", stderr.String())
	}
	if releaseSpy.calls != 1 || releaseSpy.beadID != "hw-stale" {
		t.Fatalf("Release calls = %d beadID = %q, want exactly one release of hw-stale", releaseSpy.calls, releaseSpy.beadID)
	}
}
