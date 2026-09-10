package events

import (
	"encoding/json"
	"testing"
)

// The journal must preserve producer-owned scope without conflating equal IDs
// from different stores, or manufacturing scope for legacy records.
func TestEventStoreScopeRoundTrip(t *testing.T) {
	for _, input := range []string{
		`{"type":"execution.work_associated","subject":"same-id","run_id":"same-id","subject_store_ref":"rig:alpha","run_store_ref":"class:graph"}`,
		`{"type":"execution.step_started","subject":"same-id","run_id":"same-id","subject_store_ref":"class:graph","run_store_ref":"class:graph"}`,
		`{"type":"execution.step_started","subject":"same-id","run_id":"same-id"}`,
	} {
		var event Event
		if err := json.Unmarshal([]byte(input), &event); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var want, got map[string]json.RawMessage
		if err := json.Unmarshal([]byte(input), &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"subject", "run_id", "subject_store_ref", "run_store_ref"} {
			if string(got[field]) != string(want[field]) {
				t.Errorf("%s: got %s, want %s (input %s)", field, got[field], want[field], input)
			}
		}
	}
}
