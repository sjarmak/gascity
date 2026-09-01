package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// Regression coverage for gc-6rae5: a graph.v2 workflow root carrying a
// canonical dispatch hold label did not fence its descendant step beads.
// Descendants named their root only via gc.root_bead_id metadata, which the
// existing own-labels-only hold check (isHeldHookCandidate) never inspects, so
// a held root's load-context/workspace-setup/implement steps stayed
// dispatchable and claimable.

// --- unit: rootHoldResolver / beadLabelsCarryDispatchHold -------------------

func TestNewRootHoldResolverNilGetIsNilResolver(t *testing.T) {
	if got := newRootHoldResolver(nil); got != nil {
		t.Fatalf("newRootHoldResolver(nil) = %v, want nil", got)
	}
}

func TestNewRootHoldResolverDetectsHoldLabelsCaseInsensitively(t *testing.T) {
	for _, label := range beadmeta.DispatchHoldLabels {
		t.Run(label, func(t *testing.T) {
			get := func(id string) (beads.Bead, error) {
				return beads.Bead{ID: id, Labels: []string{strings.ToUpper(label)}}, nil
			}
			resolver := newRootHoldResolver(get)
			if !resolver("root-1") {
				t.Fatalf("resolver(root-1) = false, want true for label %q (case-insensitive)", label)
			}
		})
	}
}

func TestNewRootHoldResolverNotHeldWithoutHoldLabel(t *testing.T) {
	get := func(id string) (beads.Bead, error) {
		return beads.Bead{ID: id, Labels: []string{"mysql-cutover"}}, nil
	}
	resolver := newRootHoldResolver(get)
	if resolver("root-1") {
		t.Fatal("resolver reported held for a root carrying only an unrelated label")
	}
}

func TestNewRootHoldResolverFailsOpenOnGetError(t *testing.T) {
	get := func(string) (beads.Bead, error) { return beads.Bead{}, errors.New("boom") }
	resolver := newRootHoldResolver(get)
	if resolver("root-1") {
		t.Fatal("resolver reported held on a get error; a resolver that cannot answer must fail open")
	}
}

func TestNewRootHoldResolverEmptyRootIDIsNotHeld(t *testing.T) {
	get := func(string) (beads.Bead, error) {
		t.Fatalf("get called with empty id")
		return beads.Bead{}, nil
	}
	resolver := newRootHoldResolver(get)
	if resolver("  ") {
		t.Fatal("resolver reported held for a blank root id")
	}
}

func TestNewRootHoldResolverMemoizesPerRoot(t *testing.T) {
	var calls int
	get := func(id string) (beads.Bead, error) {
		calls++
		return beads.Bead{ID: id, Labels: []string{beadmeta.HoldMayorLabel}}, nil
	}
	resolver := newRootHoldResolver(get)
	for i := 0; i < 3; i++ {
		if !resolver("root-1") {
			t.Fatal("resolver(root-1) = false, want true")
		}
	}
	if calls != 1 {
		t.Fatalf("get called %d times for the same root, want 1 (memoized)", calls)
	}
}

// TestNewRootHoldResolverIndependentAcrossInstances pins the gc-6rae5
// acceptance-criterion-3 contract: a caller needing a read that reflects a
// hold applied AFTER an earlier resolver was built must construct a fresh
// resolver instance rather than reuse one already populated at selection time.
func TestNewRootHoldResolverIndependentAcrossInstances(t *testing.T) {
	held := false
	get := func(id string) (beads.Bead, error) {
		if held {
			return beads.Bead{ID: id, Labels: []string{beadmeta.HoldMayorLabel}}, nil
		}
		return beads.Bead{ID: id}, nil
	}
	first := newRootHoldResolver(get)
	if first("root-1") {
		t.Fatal("first resolver reported held before the root was ever held")
	}
	held = true
	if first("root-1") {
		t.Fatal("first resolver's cache changed after construction; memoization must be per-instance, not live")
	}
	second := newRootHoldResolver(get)
	if !second("root-1") {
		t.Fatal("a fresh resolver instance did not observe the hold applied after the first resolver was built")
	}
}

// --- unit: isRootHeldHookCandidate / filterRootHeldHookCandidates ----------

func TestIsRootHeldHookCandidateSkipsSelfRoot(t *testing.T) {
	item := map[string]any{
		"id": "gc-x",
		"metadata": map[string]any{
			beadmeta.RootBeadIDMetadataKey: "gc-x",
		},
	}
	resolver := func(string) bool { return true }
	if isRootHeldHookCandidate(item, resolver) {
		t.Fatal("a bead whose gc.root_bead_id equals its own id must not be treated as a held-root descendant")
	}
}

func TestIsRootHeldHookCandidateNoRootID(t *testing.T) {
	item := map[string]any{"id": "gc-x", "metadata": map[string]any{}}
	resolver := func(string) bool { return true }
	if isRootHeldHookCandidate(item, resolver) {
		t.Fatal("a bead with no gc.root_bead_id must never be treated as root-held")
	}
}

func TestIsRootHeldHookCandidateNilResolver(t *testing.T) {
	item := map[string]any{
		"id":       "gc-x",
		"metadata": map[string]any{beadmeta.RootBeadIDMetadataKey: "gc-root"},
	}
	if isRootHeldHookCandidate(item, nil) {
		t.Fatal("a nil resolver must never report a candidate as root-held")
	}
}

func TestFilterRootHeldHookCandidatesDropsOnlyHeldRootDescendants(t *testing.T) {
	candidates := []beads.Bead{
		{ID: "gc-tgle3", Status: "open", Metadata: beads.StringMap{beadmeta.RootBeadIDMetadataKey: "gc-g8390"}},
		{ID: "gc-oc9yz", Status: "open", Metadata: beads.StringMap{beadmeta.RootBeadIDMetadataKey: "gc-g8390"}},
		{ID: "gc-other", Status: "open", Metadata: beads.StringMap{beadmeta.RootBeadIDMetadataKey: "gc-unheld"}},
		{ID: "gc-no-root", Status: "open"},
	}
	raw, err := json.Marshal(candidates)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	resolver := func(rootID string) bool { return rootID == "gc-g8390" }
	got := filterRootHeldHookCandidates(string(raw), resolver)

	var kept []beads.Bead
	if err := json.Unmarshal([]byte(got), &kept); err != nil {
		t.Fatalf("unmarshal filtered output: %v", err)
	}
	var ids []string
	for _, b := range kept {
		ids = append(ids, b.ID)
	}
	want := []string{"gc-other", "gc-no-root"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("kept ids = %v, want %v", ids, want)
	}
}

func TestFilterRootHeldHookCandidatesNilResolverIsNoop(t *testing.T) {
	raw := `[{"id":"gc-x","status":"open","metadata":{"gc.root_bead_id":"gc-root"}}]`
	if got := filterRootHeldHookCandidates(raw, nil); got != raw {
		t.Fatalf("nil resolver must be a no-op: got %q, want %q", got, raw)
	}
}

func TestFilterRootHeldHookCandidatesUnparseableInputPassesThrough(t *testing.T) {
	raw := "not json"
	resolver := func(string) bool { return true }
	if got := filterRootHeldHookCandidates(raw, resolver); got != raw {
		t.Fatalf("unparseable input must pass through unchanged: got %q, want %q", got, raw)
	}
}

// --- criteria 1 & 2: gc hook read path --------------------------------------

func TestDoHookExcludesDescendantsOfHeldGraphRoot(t *testing.T) {
	const rootID = "gc-g8390"
	candidates := []beads.Bead{
		{ID: "gc-tgle3", Status: "open", Metadata: beads.StringMap{beadmeta.RootBeadIDMetadataKey: rootID}},
		{ID: "gc-qnbnz", Status: "open", Metadata: beads.StringMap{beadmeta.RootBeadIDMetadataKey: rootID}},
	}
	raw, err := json.Marshal(candidates)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	runner := func(string, string) (string, error) { return string(raw), nil }
	resolver := func(id string) bool { return id == rootID }

	var stdout, stderr bytes.Buffer
	code := doHook("bd ready --json", "", false, runner, &stdout, &stderr, hookVisibility{RootHeld: resolver})
	if code != 1 {
		t.Fatalf("REGRESSION gc-6rae5: doHook() = %d, want 1 (no work: every candidate's workflow root %s is held); stdout=%s stderr=%s",
			code, rootID, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "gc-tgle3") || strings.Contains(stdout.String(), "gc-qnbnz") {
		t.Fatalf("held-root descendant leaked into hook output: %s", stdout.String())
	}
}

func TestDoHookServesDescendantsOnceRootHoldIsRemoved(t *testing.T) {
	const rootID = "gc-g8390"
	candidates := []beads.Bead{
		{ID: "gc-tgle3", Status: "open", Metadata: beads.StringMap{beadmeta.RootBeadIDMetadataKey: rootID}},
	}
	raw, err := json.Marshal(candidates)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	runner := func(string, string) (string, error) { return string(raw), nil }
	// The hold has been removed: the resolver now reports the root as unheld.
	resolver := func(string) bool { return false }

	var stdout, stderr bytes.Buffer
	code := doHook("bd ready --json", "", false, runner, &stdout, &stderr, hookVisibility{RootHeld: resolver})
	if code != 0 {
		t.Fatalf("doHook() = %d, want 0 (root hold lifted, descendant routable again); stdout=%s stderr=%s",
			code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "gc-tgle3") {
		t.Fatalf("descendant of an unheld root missing from hook output: %s", stdout.String())
	}
}

// --- crash recovery: existing-assignment adoption ---------------------------

// TestHookClaimDoesNotAdoptExistingAssignmentUnderHeldRoot is the gc-6rae5
// crash-recovery regression: a load-context step already in_progress and
// assigned to this session (the crash-recovery reattachment shape) must not be
// re-served as action=work while its graph.v2 workflow root is held.
func TestHookClaimDoesNotAdoptExistingAssignmentUnderHeldRoot(t *testing.T) {
	const rootID = "gc-g8390"
	const stepID = "gc-tgle3"
	runner := func(string, string) (string, error) {
		return `[{"id":"` + stepID + `","status":"in_progress","issue_type":"task","assignee":"` +
			holdTestIdentity + `","metadata":{"gc.root_bead_id":"` + rootID + `"}}]`, nil
	}
	ops := hookClaimOps{
		Runner: runner,
		Claim: func(_ context.Context, _ string, _ []string, id, _ string) (beads.Bead, bool, error) {
			t.Fatalf("store.Claim called for %q; existing-assignment adoption must not mint a fresh claim mutation", id)
			return beads.Bead{}, false, nil
		},
		RootHeld: func(string, []string) rootHoldResolver {
			return func(id string) bool { return id == rootID }
		},
	}
	var stdout, stderr bytes.Buffer
	doHookClaim("bd ready --json", "/tmp/work", holdTestClaimOptions(), ops, &stdout, &stderr)

	var result hookClaimJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\nraw: %s", err, stdout.String())
	}
	if result.Action == "work" {
		t.Fatalf("REGRESSION gc-6rae5: hook adopted %q under crash recovery as action=work while its workflow root %s is held (reason=%q)",
			result.BeadID, rootID, result.Reason)
	}
}

// --- criterion 3: destination-side recheck races a hold applied mid-claim --

// TestHookClaimReleasesClaimWhenRootBecomesHeldMidClaim pins gc-6rae5
// acceptance criterion 3: a candidate that passed the selection-time root-hold
// filter but whose workflow root became held before the claim CAS was
// delivered must be released, not handed back as action=work. The RootHeld
// factory mock returns a resolver that answers "not held" on its first
// (selection-time) call and "held" on its second (post-claim recheck) call,
// simulating a `bd set-state <root> hold=mayor` landing in the gap between
// selection and delivery.
func TestHookClaimReleasesClaimWhenRootBecomesHeldMidClaim(t *testing.T) {
	const beadID = "gc-race"
	const rootID = "gc-root-race"
	runner := func(string, string) (string, error) {
		return `[{"id":"` + beadID + `","status":"open","issue_type":"task","metadata":{"gc.routed_to":"` +
			holdTestIdentity + `","gc.root_bead_id":"` + rootID + `"}}]`, nil
	}
	var factoryCalls int
	var releaseCalls []string
	var releasedReasons []string
	ops := hookClaimOps{
		Runner: runner,
		Claim: func(_ context.Context, _ string, _ []string, id, assignee string) (beads.Bead, bool, error) {
			return beads.Bead{ID: id, Status: "in_progress", Assignee: assignee, Metadata: beads.StringMap{beadmeta.RootBeadIDMetadataKey: rootID}}, true, nil
		},
		Release: func(_ context.Context, _ string, _ []string, id, _ string) (bool, error) {
			releaseCalls = append(releaseCalls, id)
			return true, nil
		},
		EmitClaimReleased: func(rec hookClaimReleaseRecord) {
			releasedReasons = append(releasedReasons, rec.Reason)
		},
		RootHeld: func(string, []string) rootHoldResolver {
			factoryCalls++
			call := factoryCalls
			return func(id string) bool {
				return call > 1 && id == rootID
			}
		},
	}
	var stdout, stderr bytes.Buffer
	code := doHookClaim("bd ready --json", "/tmp/work", holdTestClaimOptions(), ops, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("doHookClaim() = %d, want 1 (undelivered claim unwound); stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if len(releaseCalls) != 1 || releaseCalls[0] != beadID {
		t.Fatalf("release calls = %v, want exactly one release of %q", releaseCalls, beadID)
	}
	if len(releasedReasons) != 1 || releasedReasons[0] != hookClaimReleaseReasonRootHeld {
		t.Fatalf("release reason = %v, want %q", releasedReasons, hookClaimReleaseReasonRootHeld)
	}
	if strings.Contains(stdout.String(), `"action":"work"`) {
		t.Fatalf("hook reported action=work for a claim whose root was held before delivery: %s", stdout.String())
	}
}

// TestHookClaimDeliversClaimWhenRootStaysUnheld is the control for the race
// test above: when the root-held resolver reports "not held" on both the
// selection-time and post-claim-recheck calls, the claim is delivered
// normally as action=work.
func TestHookClaimDeliversClaimWhenRootStaysUnheld(t *testing.T) {
	const beadID = "gc-clean"
	const rootID = "gc-root-clean"
	runner := func(string, string) (string, error) {
		return `[{"id":"` + beadID + `","status":"open","issue_type":"task","metadata":{"gc.routed_to":"` +
			holdTestIdentity + `","gc.root_bead_id":"` + rootID + `"}}]`, nil
	}
	ops := hookClaimOps{
		Runner: runner,
		Claim: func(_ context.Context, _ string, _ []string, id, assignee string) (beads.Bead, bool, error) {
			return beads.Bead{ID: id, Status: "in_progress", Assignee: assignee, Metadata: beads.StringMap{beadmeta.RootBeadIDMetadataKey: rootID}}, true, nil
		},
		RootHeld: func(string, []string) rootHoldResolver {
			return func(string) bool { return false }
		},
	}
	var stdout, stderr bytes.Buffer
	code := doHookClaim("bd ready --json", "/tmp/work", holdTestClaimOptions(), ops, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doHookClaim() = %d, want 0; stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var result hookClaimJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\nraw: %s", err, stdout.String())
	}
	if result.Action != "work" || result.BeadID != beadID {
		t.Fatalf("result = %+v, want action=work beadID=%s", result, beadID)
	}
}
