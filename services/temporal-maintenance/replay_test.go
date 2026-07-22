package temporalmaintenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/worker"
)

// The replay-safety gate: recorded histories replayed through the current
// workflow code prove a code change is replay-safe (no non-determinism against
// a real execution). One fixture per production path. Operational discipline
// for WHEN this gate must run and what a failure means lives in
// docs/conventions/temporal-versioning.md.
//
// Capturing a fixture is read-only against the deployed server:
//
//	temporal workflow show --namespace maintenance \
//	  --workflow-id <completed-cycle-id> --output json > testdata/<name>.json
//
// (or from a local `temporal server start-dev` for paths production has not
// exercised yet — see the README drive-through for the gated path.)
var replayFixtures = []struct {
	name string
	file string
	path string                         // which workflow path the history exercises
	pin  func(t *testing.T, raw []byte) // content pin; nil = replay-only
}{
	{
		name: "gated",
		file: "testdata/maintenance_cycle_history.json",
		path: "fanout + CI/review signals + human-gate Update + gated mutation",
	},
	{
		name: "dispatch_only",
		file: "testdata/dispatch_only_history.json",
		// Captured 2026-07-22 from the production server (cycle
		// maintenance-cycle-2026-07-22T02:00:00Z), recorded by the
		// pre-gc-372.1 worker: replaying it against the post-fix code is the
		// worked proof that an ActivityOptions-only change is replay-safe.
		path: "dispatch-only (the armed 120m Schedule's production path)",
		pin:  pinDispatchOnlyHistory,
	},
}

// TestReplay_FromCapturedHistory replays each recorded history against the
// current workflow code. It is the replay-safety gate for any workflow-code
// change. Both fixtures are committed, so a missing or unreadable history is
// a broken checkout — the gate FAILS rather than skipping, because a green
// skip would silently disarm the only replay-safety proof this module has.
func TestReplay_FromCapturedHistory(t *testing.T) {
	for _, tc := range replayFixtures {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(".", tc.file)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("captured history unreadable at %s (%v) — the %s replay gate must not pass vacuously; "+
					"restore the committed fixture (see the file-header capture instructions)", path, err, tc.name)
			}
			if tc.pin != nil {
				tc.pin(t, raw)
			}
			replayer := worker.NewWorkflowReplayer()
			replayer.RegisterWorkflow(MaintenanceCycleWorkflow)
			require.NoError(t, replayer.ReplayWorkflowHistoryFromJSONFile(nil, path),
				"current workflow code must replay the captured %s history (%s) without non-determinism",
				tc.name, tc.path)
		})
	}
}

// pinDispatchOnlyHistory asserts the dispatch_only fixture still IS the
// production dispatch-only history: MaintenanceCycleWorkflow, exactly the two
// DispatchSelection activities (review + author halves), ending at clean
// completion. A silently swapped or truncated fixture fails here before the
// replay could pass against the wrong history.
func pinDispatchOnlyHistory(t *testing.T, raw []byte) {
	t.Helper()
	var hist struct {
		Events []struct {
			EventType string `json:"eventType"`
			Started   *struct {
				WorkflowType struct {
					Name string `json:"name"`
				} `json:"workflowType"`
			} `json:"workflowExecutionStartedEventAttributes"`
			ActivityScheduled *struct {
				ActivityType struct {
					Name string `json:"name"`
				} `json:"activityType"`
			} `json:"activityTaskScheduledEventAttributes"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal(raw, &hist), "fixture must be a temporal workflow show JSON history")
	require.NotEmpty(t, hist.Events)

	first := hist.Events[0]
	require.Equal(t, "EVENT_TYPE_WORKFLOW_EXECUTION_STARTED", first.EventType)
	require.NotNil(t, first.Started)
	require.Equal(t, "MaintenanceCycleWorkflow", first.Started.WorkflowType.Name)

	dispatches := 0
	for _, e := range hist.Events {
		if e.ActivityScheduled == nil {
			continue
		}
		require.Equal(t, "DispatchSelection", e.ActivityScheduled.ActivityType.Name,
			"dispatch-only history schedules no activity besides DispatchSelection")
		dispatches++
	}
	require.Equal(t, 2, dispatches, "dispatch-only history schedules exactly the two DispatchSelection activities")

	require.Equal(t, "EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED", hist.Events[len(hist.Events)-1].EventType,
		"history must end at clean completion")
}
