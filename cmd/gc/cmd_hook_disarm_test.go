package main

import (
	"encoding/json"
	"testing"
	"time"
)

// hookRowIDs decodes filterUnreadyHookCandidates output into the surviving ids.
func hookRowIDs(t *testing.T, out string) []string {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("unmarshal filtered output %q: %v", out, err)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		id, _ := r["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// TestFilterUnreadyHookCandidatesStripsDisarmedBooleanShape is the production
// regression. bd type-infers `--set-metadata gc.disarmed=true` into a JSON
// boolean, and this filter walks raw map[string]any — so a reader that only
// accepted the string "true" would silently never fire.
//
// The fixture is a raw JSON literal, NOT a beads.Bead round-trip: that decoder
// coerces the boolean to "true" and would make this test pass while production
// still offered the bead.
func TestFilterUnreadyHookCandidatesStripsDisarmedBooleanShape(t *testing.T) {
	const out = `[{"id":"gc-disarmed","status":"open","metadata":{"gc.routed_to":"worker","gc.disarmed":true}}]`

	got := filterUnreadyHookCandidates(out, time.Now())

	if ids := hookRowIDs(t, got); len(ids) != 0 {
		t.Errorf("disarmed bead survived the filter: %v (a disarmed bead must never be offered)", ids)
	}
}

// TestFilterUnreadyHookCandidatesDisarmIsIndependentOfStatusAndAssignee covers
// AC#1: the flag holds even when status=open and an assignee is still set. This
// is the EnterpriseBench case — cache-reconcile reopens a step that carries an
// explicit do-not-execute reason, and every tier offers it again.
func TestFilterUnreadyHookCandidatesDisarmIsIndependentOfStatusAndAssignee(t *testing.T) {
	rows := []string{
		`{"id":"a","status":"open","metadata":{"gc.disarmed":true}}`,
		`{"id":"b","status":"open","assignee":"polecat-1","metadata":{"gc.disarmed":true}}`,
		`{"id":"c","status":"in_progress","assignee":"polecat-1","metadata":{"gc.disarmed":true}}`,
		`{"id":"d","status":"blocked","metadata":{"gc.disarmed":true}}`,
	}
	for _, row := range rows {
		out := filterUnreadyHookCandidates("["+row+"]", time.Now())
		if ids := hookRowIDs(t, out); len(ids) != 0 {
			t.Errorf("row %s survived: %v", row, ids)
		}
	}
}

// TestFilterUnreadyHookCandidatesKeepsNonDisarmed is AC#4: non-disarmed
// behavior must be unchanged. Over-filtering here would strand the whole fleet,
// since most beads carry no gc.disarmed key at all.
func TestFilterUnreadyHookCandidatesKeepsNonDisarmed(t *testing.T) {
	tests := []struct {
		name string
		row  string
	}{
		{name: "no metadata object at all", row: `{"id":"keep","status":"open"}`},
		{name: "metadata without the key", row: `{"id":"keep","status":"open","metadata":{"gc.routed_to":"worker"}}`},
		{name: "explicit boolean false", row: `{"id":"keep","status":"open","metadata":{"gc.disarmed":false}}`},
		{name: "explicit string false", row: `{"id":"keep","status":"open","metadata":{"gc.disarmed":"false"}}`},
		{name: "cleared to empty", row: `{"id":"keep","status":"open","metadata":{"gc.disarmed":""}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := filterUnreadyHookCandidates("["+tc.row+"]", time.Now())
			ids := hookRowIDs(t, out)
			if len(ids) != 1 || ids[0] != "keep" {
				t.Errorf("non-disarmed bead was dropped: got %v, want [keep]", ids)
			}
		})
	}
}

// TestFilterUnreadyHookCandidatesDisarmFailsClosed: a key we can see but cannot
// read means an operator deliberately wrote a do-not-execute marker. Executing
// anyway is the exact failure this contract prevents, so it fails closed.
func TestFilterUnreadyHookCandidatesDisarmFailsClosed(t *testing.T) {
	for _, row := range []string{
		`{"id":"x","status":"open","metadata":{"gc.disarmed":"yes"}}`,
		`{"id":"x","status":"open","metadata":{"gc.disarmed":1}}`,
		`{"id":"x","status":"open","metadata":{"gc.disarmed":null}}`,
	} {
		out := filterUnreadyHookCandidates("["+row+"]", time.Now())
		if ids := hookRowIDs(t, out); len(ids) != 0 {
			t.Errorf("unreadable marker %s was offered anyway: %v", row, ids)
		}
	}
}

// TestFilterUnreadyHookCandidatesDisarmedDoesNotStarvePeers guards the
// head-of-line case at the Go layer: stripping a disarmed row must leave the
// other rows in the batch claimable.
func TestFilterUnreadyHookCandidatesDisarmedDoesNotStarvePeers(t *testing.T) {
	const out = `[{"id":"disarmed","status":"open","metadata":{"gc.disarmed":true}},` +
		`{"id":"live","status":"open","metadata":{"gc.routed_to":"worker"}}]`

	ids := hookRowIDs(t, filterUnreadyHookCandidates(out, time.Now()))

	if len(ids) != 1 || ids[0] != "live" {
		t.Errorf("got %v, want [live] — live work behind a disarmed head must stay claimable", ids)
	}
}
