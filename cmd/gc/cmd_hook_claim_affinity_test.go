package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// affineHookClaimOps builds claim ops over a single work-query row, recording
// any bead the claim mutation was attempted on. The pinned-session gate must
// reject before the mutation, so claimedID staying empty is the assertion that
// distinguishes "never offered" from "offered and lost the race".
func affineHookClaimOps(row string, claimedID *string) hookClaimOps {
	return hookClaimOps{
		Runner: func(string, string) (string, error) { return "[" + row + "]", nil },
		Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
			*claimedID = beadID
			return beads.Bead{ID: beadID, Status: "in_progress", Assignee: assignee}, true, nil
		},
	}
}

func affineHookClaimOptions() hookClaimOptions {
	return hookClaimOptions{
		Assignee:           "polecat-gc-504646",
		IdentityCandidates: []string{"polecat-gc-504646"},
		// The bare pool template stays in RouteTargets: it is what makes every
		// pool slot match this row, and is exactly why routed_to cannot gate a
		// pinned step on its own.
		RouteTargets: []string{"/home/ds/gascity/polecat"},
		JSON:         true,
	}
}

// TestDoHookClaimSkipsWorkPinnedToAnotherSession is the gc-zf4 repro: step
// gc-jqt carried gc.session_affinity=require and gc.session_name pinning it to
// polecat-gc-504481, while gc.routed_to stayed the shared pool template and the
// assignee was empty. gc hook advertised it to a different slot, which could
// then double-execute the step and race writes against the pinned session.
func TestDoHookClaimSkipsWorkPinnedToAnotherSession(t *testing.T) {
	var claimedID string
	row := `{"id":"gc-jqt","status":"open","metadata":{"gc.routed_to":"/home/ds/gascity/polecat","gc.session_affinity":"require","gc.session_name":"polecat-gc-504481"}}`

	var stdout, stderr bytes.Buffer
	code := doHookClaim("bd ready --json", "/tmp/work", affineHookClaimOptions(), affineHookClaimOps(row, &claimedID), &stdout, &stderr)

	if claimedID != "" {
		t.Fatalf("claimed %q, want no claim: the step is pinned to polecat-gc-504481", claimedID)
	}
	if code != 1 {
		t.Fatalf("doHookClaim(pinned elsewhere) = %d, want 1 (drain); stderr=%s", code, stderr.String())
	}
	var result hookClaimJSONResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not JSON: %v\nraw: %s", err, stdout.String())
	}
	if result.Action != "drain" || result.Reason != "no_work" {
		t.Fatalf("unexpected result for pinned-elsewhere work: %+v", result)
	}
}

// TestDoHookClaimClaimsWorkPinnedToThisSession is the other half of the
// contract: the pinned session itself must still be able to claim its step.
func TestDoHookClaimClaimsWorkPinnedToThisSession(t *testing.T) {
	var claimedID string
	row := `{"id":"gc-jqt","status":"open","metadata":{"gc.routed_to":"/home/ds/gascity/polecat","gc.session_affinity":"require","gc.session_name":"polecat-gc-504646"}}`

	var stdout, stderr bytes.Buffer
	code := doHookClaim("bd ready --json", "/tmp/work", affineHookClaimOptions(), affineHookClaimOps(row, &claimedID), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doHookClaim(pinned to us) = %d, want 0; stderr=%s", code, stderr.String())
	}
	if claimedID != "gc-jqt" {
		t.Fatalf("claimed %q, want gc-jqt: the step is pinned to this session", claimedID)
	}
}

// TestDoHookClaimClaimsUnboundSessionAffineWork preserves fresh pool work.
// graphroute stamps affinity=require on every pool-routed step while deleting
// gc.session_name, binding a concrete session only at claim time. Gating on the
// affinity marker alone would make new pool work claimable by nobody.
func TestDoHookClaimClaimsUnboundSessionAffineWork(t *testing.T) {
	var claimedID string
	row := `{"id":"gc-4xd","status":"open","metadata":{"gc.routed_to":"/home/ds/gascity/polecat","gc.session_affinity":"require"}}`

	var stdout, stderr bytes.Buffer
	code := doHookClaim("bd ready --json", "/tmp/work", affineHookClaimOptions(), affineHookClaimOps(row, &claimedID), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doHookClaim(unbound affinity) = %d, want 0; stderr=%s", code, stderr.String())
	}
	if claimedID != "gc-4xd" {
		t.Fatalf("claimed %q, want gc-4xd: unbound affinity stays pool-claimable", claimedID)
	}
}

// TestDoHookClaimClaimsReleasedWorkWithStaleSessionName guards the inverse
// stranding bug. Release paths clear the affinity keys but deliberately leave
// gc.session_name behind as a durable back-reference (#2843), so a gate keyed
// on the name alone would strand released work on an exited session forever.
func TestDoHookClaimClaimsReleasedWorkWithStaleSessionName(t *testing.T) {
	var claimedID string
	row := `{"id":"gc-w3q","status":"open","metadata":{"gc.routed_to":"/home/ds/gascity/polecat","gc.session_affinity":"","gc.session_name":"polecat-gc-504481"}}`

	var stdout, stderr bytes.Buffer
	code := doHookClaim("bd ready --json", "/tmp/work", affineHookClaimOptions(), affineHookClaimOps(row, &claimedID), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doHookClaim(released, stale name) = %d, want 0; stderr=%s", code, stderr.String())
	}
	if claimedID != "gc-w3q" {
		t.Fatalf("claimed %q, want gc-w3q: cleared affinity means released back to the pool", claimedID)
	}
}

// TestDoHookClaimPreassignSkipsSiblingPinnedToAnotherSession covers the
// sibling-vacuum path. preassignHookContinuationGroup routes on
// {root_bead_id, continuation_group} and reuses the same route predicate as a
// fresh claim, so without the gate it would vacuum a sibling pinned to a
// different live session onto this one.
func TestDoHookClaimPreassignSkipsSiblingPinnedToAnotherSession(t *testing.T) {
	var assigned []string
	claimed := `{"gc.routed_to":"/home/ds/gascity/polecat","gc.root_bead_id":"root-1","gc.continuation_group":"pool-workflow"}`
	ops := hookClaimOps{
		Runner: func(string, string) (string, error) {
			return `[{"id":"hw-3","status":"open","metadata":` + claimed + `}]`, nil
		},
		Claim: func(_ context.Context, _ string, _ []string, beadID, assignee string) (beads.Bead, bool, error) {
			return beads.Bead{
				ID: beadID, Status: "in_progress", Assignee: assignee,
				Metadata: map[string]string{
					"gc.routed_to":          "/home/ds/gascity/polecat",
					"gc.root_bead_id":       "root-1",
					"gc.continuation_group": "pool-workflow",
				},
			}, true, nil
		},
		ListContinuation: func(context.Context, string, []string, string, string) ([]beads.Bead, error) {
			return []beads.Bead{
				// Unbound affinity: a normal pool sibling, must still vacuum.
				{ID: "hw-4", Status: "open", Metadata: map[string]string{
					"gc.routed_to": "/home/ds/gascity/polecat", "gc.session_affinity": "require",
				}},
				// Pinned to a live foreign session: must NOT vacuum.
				{ID: "hw-pinned", Status: "open", Metadata: map[string]string{
					"gc.routed_to": "/home/ds/gascity/polecat", "gc.session_affinity": "require",
					"gc.session_name": "polecat-gc-504481",
				}},
			}, nil
		},
		AssignContinuation: func(_ context.Context, _ string, _ []string, beadID, _ string) error {
			assigned = append(assigned, beadID)
			return nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := doHookClaim("bd ready --json", "/tmp/work", affineHookClaimOptions(), ops, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doHookClaim(continuation) = %d, want 0; stderr=%s", code, stderr.String())
	}
	if got := strings.Join(assigned, ","); got != "hw-4" {
		t.Fatalf("vacuumed siblings = %q, want hw-4 only (hw-pinned belongs to polecat-gc-504481)", got)
	}
}

// TestDoHookHidesWorkPinnedToAnotherSession covers the discovery half of the
// gc-zf4 contract: plain `gc hook` must not advertise another session's step as
// available work. The row is otherwise perfectly ready — open, unblocked, and
// routed to this slot's own pool template.
func TestDoHookHidesWorkPinnedToAnotherSession(t *testing.T) {
	runner := func(string, string) (string, error) {
		return `[{"id":"gc-jqt","status":"open","metadata":{"gc.routed_to":"/home/ds/gascity/polecat","gc.session_affinity":"require","gc.session_name":"polecat-gc-504481"}}]`, nil
	}
	var stdout, stderr bytes.Buffer
	code := doHook("bd ready --json", "/tmp/work", false, []string{"polecat-gc-504646"}, runner, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("doHook(pinned elsewhere) = %d, want 1 (no work); stdout=%s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), "gc-jqt") {
		t.Fatalf("gc hook advertised a step pinned to polecat-gc-504481: %s", stdout.String())
	}
}

// The pinned session itself must still see its own step.
func TestDoHookShowsWorkPinnedToThisSession(t *testing.T) {
	runner := func(string, string) (string, error) {
		return `[{"id":"gc-jqt","status":"open","metadata":{"gc.routed_to":"/home/ds/gascity/polecat","gc.session_affinity":"require","gc.session_name":"polecat-gc-504646"}}]`, nil
	}
	var stdout, stderr bytes.Buffer
	code := doHook("bd ready --json", "/tmp/work", false, []string{"polecat-gc-504646"}, runner, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doHook(pinned to us) = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "gc-jqt") {
		t.Fatalf("gc hook hid this session's own step: %s", stdout.String())
	}
}

// Unbound affinity is ordinary fresh pool work and must stay advertised.
func TestDoHookShowsUnboundSessionAffineWork(t *testing.T) {
	runner := func(string, string) (string, error) {
		return `[{"id":"gc-4xd","status":"open","metadata":{"gc.routed_to":"/home/ds/gascity/polecat","gc.session_affinity":"require"}}]`, nil
	}
	var stdout, stderr bytes.Buffer
	code := doHook("bd ready --json", "/tmp/work", false, []string{"polecat-gc-504646"}, runner, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doHook(unbound affinity) = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "gc-4xd") {
		t.Fatalf("gc hook hid unbound pool work: %s", stdout.String())
	}
}

// TestFirstStoreWithWorkSkipsStoreHoldingOnlyPinnedWork keeps store selection
// coherent with claim eligibility. Selection reporting a hit on a store whose
// only rows are pinned to another session would make `gc hook` filter that
// output back to empty and report "no work", masking claimable work in a later
// federated store (gc-zf4).
func TestFirstStoreWithWorkSkipsStoreHoldingOnlyPinnedWork(t *testing.T) {
	pinned := `[{"id":"gc-jqt","status":"open","metadata":{"gc.routed_to":"/home/ds/gascity/polecat","gc.session_affinity":"require","gc.session_name":"polecat-gc-504481"}}]`
	mine := `[{"id":"gc-w3q","status":"open","metadata":{"gc.routed_to":"/home/ds/gascity/polecat"}}]`
	stores := []hookStore{{dir: "/pinned-store"}, {dir: "/open-store"}}
	run := func(_, dir string, _ []string) (string, error) {
		if dir == "/pinned-store" {
			return pinned, nil
		}
		return mine, nil
	}

	out, gotStore, err := firstStoreWithWork("fake-query", stores, stores[0], []string{"polecat-gc-504646"}, run)
	if err != nil {
		t.Fatalf("firstStoreWithWork: %v", err)
	}
	if gotStore.dir != "/open-store" {
		t.Fatalf("selected store = %q, want /open-store: the first store holds only work pinned to polecat-gc-504481", gotStore.dir)
	}
	if !strings.Contains(out, "gc-w3q") {
		t.Fatalf("selected output = %q, want the claimable row gc-w3q", out)
	}
}

// The pinned session itself still selects its own store.
func TestFirstStoreWithWorkSelectsStoreHoldingOurPinnedWork(t *testing.T) {
	pinned := `[{"id":"gc-jqt","status":"open","metadata":{"gc.routed_to":"/home/ds/gascity/polecat","gc.session_affinity":"require","gc.session_name":"polecat-gc-504646"}}]`
	stores := []hookStore{{dir: "/pinned-store"}, {dir: "/empty-store"}}
	run := func(_, dir string, _ []string) (string, error) {
		if dir == "/pinned-store" {
			return pinned, nil
		}
		return `[]`, nil
	}

	_, gotStore, err := firstStoreWithWork("fake-query", stores, stores[0], []string{"polecat-gc-504646"}, run)
	if err != nil {
		t.Fatalf("firstStoreWithWork: %v", err)
	}
	if gotStore.dir != "/pinned-store" {
		t.Fatalf("selected store = %q, want /pinned-store: the step is pinned to this session", gotStore.dir)
	}
}
