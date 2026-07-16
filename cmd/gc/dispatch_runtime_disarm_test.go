package main

import (
	"encoding/json"
	"testing"
)

// TestHookBeadMetadataCoercesBdBoolean pins the decoder assumption that
// filterDisarmedServeBeads relies on. bd emits gc.disarmed as a JSON boolean;
// hookBeadMetadata is a third decoder (alongside raw map[string]any on the hook
// path and beads.Bead's StringMap on the demand path) and normalizes it to the
// string "true". If that ever changes, the serve-path filter goes silently blind
// and this test is the tripwire.
func TestHookBeadMetadataCoercesBdBoolean(t *testing.T) {
	const row = `[{"id":"gc-1","metadata":{"gc.disarmed":true}}]`

	var out []hookBead
	if err := json.Unmarshal([]byte(row), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := out[0].Metadata["gc.disarmed"]; got != "true" {
		t.Fatalf("hookBeadMetadata coerced bd boolean to %q, want \"true\"", got)
	}
}

// TestFilterDisarmedServeBeadsDropsDisarmed covers the control-dispatcher drain
// path (nextWorkflowServeBeads). Control beads reach a worker here WITHOUT
// crossing the hook's claim filter, so before this fix a disarmed control bead
// was served and executed.
func TestFilterDisarmedServeBeadsDropsDisarmed(t *testing.T) {
	const rows = `[{"id":"disarmed","metadata":{"gc.disarmed":true}},` +
		`{"id":"live","metadata":{"gc.routed_to":"worker"}}]`

	var in []hookBead
	if err := json.Unmarshal([]byte(rows), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := filterDisarmedServeBeads(in)

	if len(got) != 1 || got[0].ID != "live" {
		ids := make([]string, 0, len(got))
		for _, b := range got {
			ids = append(ids, b.ID)
		}
		t.Errorf("got %v, want [live] — disarmed control bead must not be served", ids)
	}
}

// TestFilterDisarmedServeBeadsKeepsNonDisarmed is the AC#4 half: this path must
// not start dropping anything else. The hook's blocked/deferred predicates are
// deliberately not applied here.
func TestFilterDisarmedServeBeadsKeepsNonDisarmed(t *testing.T) {
	const rows = `[{"id":"a"},` +
		`{"id":"b","metadata":{"gc.disarmed":false}},` +
		`{"id":"c","metadata":{"gc.disarmed":""}},` +
		`{"id":"d","metadata":{"gc.routed_to":"worker"}}]`

	var in []hookBead
	if err := json.Unmarshal([]byte(rows), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got := filterDisarmedServeBeads(in); len(got) != len(in) {
		t.Errorf("dropped non-disarmed serve beads: got %d, want %d", len(got), len(in))
	}
}
