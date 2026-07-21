package temporalmaintenance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/worker"
)

// historyFile is a completed-workflow history captured once from the local dev
// server. Replaying it through the current workflow code proves the code change
// is replay-safe (no non-determinism against a real recorded execution).
//
// Capture it once (requires the temporal CLI):
//
//	temporal server start-dev
//	go run ./worker            # in another terminal
//	# drive one cycle to completion via the harness, then:
//	temporal workflow show --workflow-id <id> --output json \
//	  > testdata/maintenance_cycle_history.json
const historyFile = "testdata/maintenance_cycle_history.json"

// TestReplay_FromCapturedHistory replays a recorded history against the current
// workflow code. It is the replay-safety gate for any workflow-code change. When
// the captured history is not present (e.g. the temporal CLI is not installed on
// this host yet), it skips with capture instructions rather than passing vacuously.
func TestReplay_FromCapturedHistory(t *testing.T) {
	path := filepath.Join(".", historyFile)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no captured history at %s — capture it from the dev server (see file header); "+
			"replay-safety gate is inert until then", path)
	}

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(MaintenanceCycleWorkflow)
	require.NoError(t, replayer.ReplayWorkflowHistoryFromJSONFile(nil, path),
		"current workflow code must replay the captured history without non-determinism")
}
