package temporalmaintenance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NO-MUTATION gate (the bead's test-gated acceptance): the observe path must
// only ever invoke read verbs. The fake below implements the bridge's read
// interfaces AND the mutation surface (SignalWorkflow, StartWorkflow, bead
// mutation), records every call, and the test asserts the mutation counters
// stay zero while reads happened — an allowlist proof, not a code-reading one.
// ---------------------------------------------------------------------------

// recordingObserveFake satisfies ExecutionLister + WorkflowStateReader +
// BeadReader and additionally exposes the mutation methods an execute-path
// component would need. If the bridge ever grows a mutation call, this fake
// records it and the gate fails.
type recordingObserveFake struct {
	calls []string

	execs []ExecutionSummary
	// states is keyed by workflow ID (any run); statesByRun, keyed
	// "workflowID/runID", wins when present so tests can hand two runs of one
	// workflow distinct states.
	states      map[string]MaintenanceCycleState
	statesByRun map[string]MaintenanceCycleState
	closed      []ObservedBead
	open        []ObservedBead
}

func (f *recordingObserveFake) ListExecutions(_ context.Context, _ time.Time) ([]ExecutionSummary, error) {
	f.calls = append(f.calls, "ListExecutions")
	return f.execs, nil
}

func (f *recordingObserveFake) QueryStateForRun(_ context.Context, workflowID, runID string) (MaintenanceCycleState, error) {
	f.calls = append(f.calls, "QueryStateForRun")
	if st, ok := f.statesByRun[workflowID+"/"+runID]; ok {
		return st, nil
	}
	st, ok := f.states[workflowID]
	if !ok {
		return MaintenanceCycleState{}, fmt.Errorf("no state for %s run %q", workflowID, runID)
	}
	return st, nil
}

func (f *recordingObserveFake) ListClosedSince(_ context.Context, _ time.Time) ([]ObservedBead, error) {
	f.calls = append(f.calls, "ListClosedSince")
	return f.closed, nil
}

func (f *recordingObserveFake) ListOpen(_ context.Context) ([]ObservedBead, error) {
	f.calls = append(f.calls, "ListOpen")
	return f.open, nil
}

// Mutation surface — present so the fake COULD be misused; the gate asserts it
// never is.
func (f *recordingObserveFake) SignalWorkflow(_ context.Context, _, _, _ string, _ interface{}) error {
	f.calls = append(f.calls, "SignalWorkflow")
	return nil
}

func (f *recordingObserveFake) StartWorkflow(_ context.Context, _ string) error {
	f.calls = append(f.calls, "StartWorkflow")
	return nil
}

func (f *recordingObserveFake) MutateBead(_ context.Context, _ string) error {
	f.calls = append(f.calls, "MutateBead")
	return nil
}

// compile-time proof the fake carries the mutation surface the gate guards
// against (so "never called" is meaningful, not vacuous).
var _ WorkflowSignaler = (*recordingObserveFake)(nil)

func TestObserveBridge_ReadOnly(t *testing.T) {
	fake := &recordingObserveFake{
		execs: []ExecutionSummary{{WorkflowID: "wf-1", Status: "completed", HistoryLength: 17}},
		states: map[string]MaintenanceCycleState{
			"wf-1": {Phase: PhaseDone, BeadIDs: []string{"gc-aaaa"}},
		},
		closed: []ObservedBead{{ID: "gc-aaaa", Status: "closed"}},
	}
	b := &ObserveBridge{Executions: fake, States: fake, Beads: fake}
	_, err := b.Observe(context.Background())
	require.NoError(t, err)

	readVerbs := map[string]bool{
		"ListExecutions": true, "QueryStateForRun": true, "ListClosedSince": true, "ListOpen": true,
	}
	require.NotEmpty(t, fake.calls, "the gate is vacuous if the bridge called nothing")
	for _, call := range fake.calls {
		require.True(t, readVerbs[call],
			"observe path invoked non-read verb %q — the no-mutation invariant is broken", call)
	}
}

// ---------------------------------------------------------------------------
// Metric computation.
// ---------------------------------------------------------------------------

func TestObserveBridge_Metrics(t *testing.T) {
	now := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	taggedFlat := map[string]any{"temporal.repo": "gastownhall-gascity", "temporal.cycle_key": "20260721T120000"}
	taggedNested := map[string]any{"temporal": map[string]any{"repo": "r", "cycle_key": "k"}}

	retainedFrom := now.Add(-20 * time.Hour) // oldest retained execution (inside the 24h retention floor)
	fake := &recordingObserveFake{
		execs: []ExecutionSummary{
			{WorkflowID: "wf-armed", Status: "completed", HistoryLength: 17, StartTime: now.Add(-2 * time.Hour)},
			{WorkflowID: "wf-skip", Status: "completed", HistoryLength: 17, StartTime: now.Add(-4 * time.Hour)},
			{WorkflowID: "wf-waiting", Status: "running", HistoryLength: 23, StartTime: now.Add(-1 * time.Hour)},
			{WorkflowID: "wf-old", Status: "completed", HistoryLength: 17, StartTime: retainedFrom},
		},
		states: map[string]MaintenanceCycleState{
			"wf-armed": {Phase: PhaseDone, BeadIDs: []string{"gc-real1", "gc-ghost"},
				Branches: map[string]BranchState{
					"review": {Kind: "review", SelectionBead: "gc-real1"},
					"author": {Kind: "author", SelectionBead: "gc-ghost"},
				}},
			"wf-skip": {Phase: PhaseDone,
				Branches: map[string]BranchState{
					"review": {Kind: "review", SelectionBead: "temporal-shadow/r/k/review/selection"},
					"author": {Kind: "author", SelectionBead: "temporal-shadow/r/k/author/selection"},
				}},
			// The waiting workflow's identity matches taggedFlat, so the
			// latency metric has a structurally possible delivery pair.
			"wf-waiting": {Phase: PhaseAwaitingEvents, Repo: "gastownhall-gascity", CycleKey: "20260721T120000"},
			"wf-old":     {Phase: PhaseDone},
		},
		closed: []ObservedBead{
			{ID: "gc-real1", Status: "closed", CreatedAt: now.Add(-3 * time.Hour), Metadata: taggedFlat},
			{ID: "gc-plain", Status: "closed", CreatedAt: now.Add(-5 * time.Hour)},
			// Created before the oldest retained execution: its creating workflow
			// is gone, so it must be counted, never listed as a diff.
			{ID: "gc-old", Status: "closed", CreatedAt: now.Add(-50 * time.Hour)},
		},
		open: []ObservedBead{
			{ID: "gc-open1", Status: "open", CreatedAt: now.Add(-2 * time.Hour), Metadata: taggedNested},
		},
	}

	b := &ObserveBridge{
		Executions:       fake,
		States:           fake,
		Beads:            fake,
		Window:           72 * time.Hour,
		AlreadySignalled: []string{"gc-real1", "gc-unrelated"},
		Now:              func() time.Time { return now },
	}
	rec, err := b.Observe(context.Background())
	require.NoError(t, err)

	// Metric 1: only the tagged closed bead is forwardable.
	require.Equal(t, 3, rec.MissedEvents.ClosedMaintenanceBeads)
	require.Equal(t, 1, rec.MissedEvents.TaggedForwardable)
	require.Equal(t, []string{"gc-real1"}, rec.MissedEvents.TaggedBeadIDs)

	// Metric 2: gc-ghost is recorded by a workflow but absent from the store;
	// gc-plain and gc-open1 exist in the store but no workflow records them;
	// gc-old predates execution retention so it is counted, not a diff;
	// wf-skip's two synthetic refs are counted, not treated as missing beads.
	require.Equal(t, 4, rec.StateDiff.ExecutionsQueried)
	require.Equal(t, 0, rec.StateDiff.QueryFailures)
	require.False(t, rec.StateDiff.Incomplete)
	require.Equal(t, retainedFrom, rec.StateDiff.CoveredFrom)
	require.Equal(t, 1, rec.StateDiff.BeadsOutsideExecutionRetention)
	require.Equal(t, []string{"gc-ghost"}, rec.StateDiff.WorkflowBeadsMissingFromStore)
	require.Equal(t, []string{"gc-open1", "gc-plain"}, rec.StateDiff.StoreBeadsUnknownToWorkflows)
	require.Equal(t, 2, rec.StateDiff.SyntheticSelectionRefs)

	// Metric 3: history growth over the 4 executions.
	require.Equal(t, 4, rec.HistoryGrowth.Executions)
	require.Equal(t, int64(17), rec.HistoryGrowth.MinEvents)
	require.Equal(t, int64(23), rec.HistoryGrowth.MaxEvents)
	require.InDelta(t, 18.5, rec.HistoryGrowth.MeanEvents, 0.001)

	// Metric 4: the tagged event's (repo, cycle_key) names the waiting
	// workflow's cycle, so a delivery pair is structurally possible — but the
	// disabled orders deliver nothing.
	require.True(t, rec.EventLatency.Applicable)
	require.Equal(t, 0, rec.EventLatency.MeasuredPairs)

	// Metric 5: the one forwardable event is already in the dedup list.
	require.Equal(t, 1, rec.DuplicateEvents.ForwardableEvents)
	require.Equal(t, 1, rec.DuplicateEvents.AlreadySignalled)
	require.Empty(t, rec.DuplicateEvents.StateFileError)

	// Expect-zero counts: both tagged shapes are detected; one waiting workflow.
	require.Equal(t, 2, rec.BeadsWithTemporalMetadata)
	require.Equal(t, 1, rec.WorkflowsInWaitingPhase)

	// Every read succeeded, so the record's zeros are affirmative.
	require.False(t, rec.Incomplete)

	require.Equal(t, now, rec.Timestamp)
	require.InDelta(t, 72.0, rec.WindowHours, 0.001)
}

// Dotted child bead ids (gc-4zf.3) are real store beads: a workflow ref must
// match them in both diff directions, not fall through as an ignored token.
func TestObserveBridge_DottedChildBeadIDs(t *testing.T) {
	now := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	fake := &recordingObserveFake{
		execs: []ExecutionSummary{
			{WorkflowID: "wf-child", Status: "completed", HistoryLength: 17, StartTime: now.Add(-2 * time.Hour)},
		},
		states: map[string]MaintenanceCycleState{
			"wf-child": {Phase: PhaseDone, BeadIDs: []string{"gc-4zf.3", "gc-9xy.2"}},
		},
		closed: []ObservedBead{
			{ID: "gc-4zf.3", Status: "closed", CreatedAt: now.Add(-1 * time.Hour)},
		},
	}
	b := &ObserveBridge{Executions: fake, States: fake, Beads: fake,
		Now: func() time.Time { return now }}
	rec, err := b.Observe(context.Background())
	require.NoError(t, err)

	// gc-4zf.3 round-trips: recorded by the workflow AND in the store, so it
	// is neither "unknown to workflows" nor "missing from store".
	require.Empty(t, rec.StateDiff.StoreBeadsUnknownToWorkflows)
	// gc-9xy.2 proves the dotted ref was parsed as a real bead id: it is
	// reported missing from the store, not silently dropped.
	require.Equal(t, []string{"gc-9xy.2"}, rec.StateDiff.WorkflowBeadsMissingFromStore)
}

// Coverage is bounded by close-time retention: a long-running execution can
// be VISIBLE far past the retention TTL, but closed workflows older than
// now-retention are purged, so their beads must be counted outside the
// covered span — never reported unknown-to-workflows.
func TestObserveBridge_CoverageBoundedByRetention(t *testing.T) {
	now := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	fake := &recordingObserveFake{
		execs: []ExecutionSummary{
			// Still running, visible for 40h — its start must not stretch the
			// covered span past the retention floor.
			{WorkflowID: "wf-longhaul", Status: "running", HistoryLength: 23, StartTime: now.Add(-40 * time.Hour)},
			{WorkflowID: "wf-recent", Status: "completed", HistoryLength: 17, StartTime: now.Add(-2 * time.Hour)},
		},
		states: map[string]MaintenanceCycleState{
			"wf-longhaul": {Phase: PhaseExecuting},
			"wf-recent":   {Phase: PhaseDone},
		},
		closed: []ObservedBead{
			// Created 30h ago by a workflow that closed and was purged: with
			// start-time coverage this bead read as a false diff.
			{ID: "gc-purgedwf", Status: "closed", CreatedAt: now.Add(-30 * time.Hour)},
		},
	}
	b := &ObserveBridge{Executions: fake, States: fake, Beads: fake,
		Now: func() time.Time { return now }}
	rec, err := b.Observe(context.Background())
	require.NoError(t, err)

	require.Equal(t, now.Add(-ObserveRetentionDefault), rec.StateDiff.CoveredFrom,
		"coveredFrom must be floored at now-retention, not the long-running start")
	require.Equal(t, 1, rec.StateDiff.BeadsOutsideExecutionRetention)
	require.Empty(t, rec.StateDiff.StoreBeadsUnknownToWorkflows,
		"a bead from an already-purged workflow is a retention artifact, not a diff")
}

// A failed state query must not produce affirmative results: the unqueryable
// workflow may record any covered bead, so the store->workflow direction
// reports nothing and the record is marked incomplete instead of clean-zero.
func TestObserveBridge_QueryFailureIsIncomplete(t *testing.T) {
	now := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	fake := &recordingObserveFake{
		execs: []ExecutionSummary{
			{WorkflowID: "wf-ok", Status: "completed", HistoryLength: 17, StartTime: now.Add(-2 * time.Hour)},
			{WorkflowID: "wf-broken", Status: "completed", HistoryLength: 17, StartTime: now.Add(-3 * time.Hour)},
		},
		states: map[string]MaintenanceCycleState{
			// wf-broken has no state entry: its query fails.
			"wf-ok": {Phase: PhaseDone, BeadIDs: []string{"gc-ghost"}},
		},
		closed: []ObservedBead{
			// Covered, unrecorded by wf-ok — plausibly wf-broken's bead. It
			// must NOT surface as unknown-to-workflows.
			{ID: "gc-maybe", Status: "closed", CreatedAt: now.Add(-1 * time.Hour)},
		},
	}
	b := &ObserveBridge{Executions: fake, States: fake, Beads: fake,
		Now: func() time.Time { return now }}
	rec, err := b.Observe(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, rec.StateDiff.ExecutionsQueried)
	require.Equal(t, 1, rec.StateDiff.QueryFailures)
	require.True(t, rec.StateDiff.Incomplete)
	require.True(t, rec.Incomplete, "expect-zero tripwires must read this record as unknown")
	require.Empty(t, rec.StateDiff.StoreBeadsUnknownToWorkflows,
		"a covered bead must not be attributed while any workflow state is unreadable")
	// The workflow->store direction rests only on successful reads and stays live.
	require.Equal(t, []string{"gc-ghost"}, rec.StateDiff.WorkflowBeadsMissingFromStore)
}

// Two runs sharing one Workflow ID must be queried as distinct runs — by
// Workflow ID alone the latest run would be read twice and the other omitted.
func TestObserveBridge_RunScopedQueries(t *testing.T) {
	now := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	fake := &recordingObserveFake{
		execs: []ExecutionSummary{
			{WorkflowID: "wf-dup", RunID: "run-a", Status: "running", HistoryLength: 23, StartTime: now.Add(-2 * time.Hour)},
			{WorkflowID: "wf-dup", RunID: "run-b", Status: "completed", HistoryLength: 17, StartTime: now.Add(-4 * time.Hour)},
		},
		statesByRun: map[string]MaintenanceCycleState{
			"wf-dup/run-a": {Phase: PhaseAwaitingEvents, Repo: "r", CycleKey: "k"},
			"wf-dup/run-b": {Phase: PhaseDone, BeadIDs: []string{"gc-runb"}},
		},
	}
	b := &ObserveBridge{Executions: fake, States: fake, Beads: fake,
		Now: func() time.Time { return now }}
	rec, err := b.Observe(context.Background())
	require.NoError(t, err)

	require.Equal(t, 2, rec.StateDiff.ExecutionsQueried)
	require.Equal(t, 0, rec.StateDiff.QueryFailures)
	// run-a's waiting phase and run-b's bead are both visible only if each
	// run was queried under its own run id.
	require.Equal(t, 1, rec.WorkflowsInWaitingPhase)
	require.Equal(t, []string{"gc-runb"}, rec.StateDiff.WorkflowBeadsMissingFromStore)
}

// A corrupt dedup state file must ride the record as an explicit error, not
// read as an affirmative 0 duplicates.
func TestObserveBridge_SignalledStateErrorIsIncomplete(t *testing.T) {
	now := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	fake := &recordingObserveFake{
		execs:  []ExecutionSummary{},
		states: map[string]MaintenanceCycleState{},
	}
	b := &ObserveBridge{Executions: fake, States: fake, Beads: fake,
		SignalledStateErr: fmt.Errorf("signal state /x/state.json: parse: unexpected end of JSON input"),
		Now:               func() time.Time { return now }}
	rec, err := b.Observe(context.Background())
	require.NoError(t, err)

	require.Contains(t, rec.DuplicateEvents.StateFileError, "parse")
	require.True(t, rec.Incomplete, "corrupt dedup evidence must not read as clean-zero")
	require.Equal(t, 0, rec.DuplicateEvents.AlreadySignalled)
}

func TestObserveBridge_LatencyStructuralFacts(t *testing.T) {
	taggedRK := []ObservedBead{{ID: "gc-t", Metadata: map[string]any{
		"temporal.repo": "r", "temporal.cycle_key": "k"}}}
	taggedNestedRK := []ObservedBead{{ID: "gc-n", Metadata: map[string]any{
		"temporal": map[string]any{"repo": "r", "cycle_key": "k"}}}}
	waitingRK := map[cycleIdentity]bool{{repo: "r", cycleKey: "k"}: true}
	waitingOther := map[cycleIdentity]bool{{repo: "r", cycleKey: "other"}: true}

	tests := []struct {
		name           string
		waiting        int
		waitingIDs     map[cycleIdentity]bool
		closed         []ObservedBead
		wantApplicable bool
	}{
		{"dispatch-only: no consumer", 0, nil, taggedRK, false},
		{"consumer but no tagged events", 2, waitingOther, []ObservedBead{{ID: "gc-plain"}}, false},
		// A tag on one cycle and a waiter on another is NOT a delivery pair.
		{"consumer and event on unrelated cycles", 1, waitingOther, taggedRK, false},
		{"event names the waiting cycle (flat)", 1, waitingRK, taggedRK, true},
		{"event names the waiting cycle (nested)", 1, waitingRK, taggedNestedRK, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := eventLatency(tt.waiting, tt.waitingIDs, tt.closed)
			require.Equal(t, tt.wantApplicable, m.Applicable)
			require.Equal(t, 0, m.MeasuredPairs, "observe mode never measures a delivery")
			require.NotEmpty(t, m.StructuralFact)
		})
	}
}

func TestTemporalTagged(t *testing.T) {
	tests := []struct {
		name string
		md   map[string]any
		want bool
	}{
		{"nil metadata", nil, false},
		{"unrelated flat keys", map[string]any{"gc.base_ref": "main"}, false},
		{"flat contract", map[string]any{"temporal.repo": "r", "temporal.cycle_key": "k"}, true},
		{"flat repo only", map[string]any{"temporal.repo": "r"}, false},
		{"nested contract", map[string]any{"temporal": map[string]any{"repo": "r", "cycle_key": "k"}}, true},
		{"nested missing cycle_key", map[string]any{"temporal": map[string]any{"repo": "r"}}, false},
		{"nested wrong type", map[string]any{"temporal": "yes"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, TemporalTagged(tt.md))
		})
	}
}

// ---------------------------------------------------------------------------
// Real bead reader: argv shape, read-verb guard, JSON parsing.
// ---------------------------------------------------------------------------

func TestGCBeadReader_ArgvIsReadOnly(t *testing.T) {
	var argvs [][]string
	r := &GCBeadReader{Rig: "gascity", run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		require.Equal(t, "gc", name)
		argvs = append(argvs, args)
		return []byte("[]"), nil
	}}

	cutoff := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	_, err := r.ListClosedSince(context.Background(), cutoff)
	require.NoError(t, err)
	_, err = r.ListOpen(context.Background())
	require.NoError(t, err)

	require.Equal(t, [][]string{
		{"bd", "--rig", "gascity", "list", "--status", "closed",
			"--closed-after", "2026-07-19T00:00:00Z", "--label", "maintenance-cycle", "--json"},
		{"bd", "--rig", "gascity", "list", "--status", "open", "--label", "maintenance-cycle", "--json"},
	}, argvs)
	for _, argv := range argvs {
		require.NoError(t, guardReadArgv(argv))
	}
}

func TestGuardReadArgv(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"list is allowed", []string{"bd", "--rig", "gascity", "list", "--json"}, false},
		{"show is allowed", []string{"bd", "--rig", "gascity", "show", "gc-x"}, false},
		{"create is refused", []string{"bd", "--rig", "gascity", "create", "t"}, true},
		{"update is refused", []string{"bd", "--rig", "gascity", "update", "gc-x"}, true},
		{"close is refused", []string{"bd", "--rig", "gascity", "close", "gc-x"}, true},
		{"mutation token later in argv is refused", []string{"bd", "--rig", "gascity", "list", "close"}, true},
		{"non-bd argv is refused", []string{"dolt", "--rig", "gascity", "list"}, true},
		{"too short is refused", []string{"bd", "list"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := guardReadArgv(tt.args)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestParseBeadRows(t *testing.T) {
	t.Run("real store shape", func(t *testing.T) {
		// Mirrors the live store's row shape: flat dotted metadata keys, RFC3339
		// timestamps.
		raw := `[
		  {"id":"gc-6vja","status":"closed","labels":["maintenance-cycle","rig:gascity"],
		   "created_at":"2026-07-21T12:00:01Z","closed_at":"2026-07-21T13:56:17Z",
		   "metadata":{"gc.base_ref":"main","loop_close_top_level":"true"}},
		  {"id":"gc-open","status":"open","labels":["maintenance-cycle"],
		   "created_at":"2026-07-21T14:00:00Z","closed_at":"",
		   "metadata":{"temporal.repo":"r","temporal.cycle_key":"k"}}
		]`
		beads, err := parseBeadRows([]byte(raw))
		require.NoError(t, err)
		require.Len(t, beads, 2)
		require.Equal(t, "gc-6vja", beads[0].ID)
		require.Equal(t, time.Date(2026, 7, 21, 13, 56, 17, 0, time.UTC), beads[0].ClosedAt)
		require.False(t, TemporalTagged(beads[0].Metadata))
		require.True(t, beads[1].ClosedAt.IsZero())
		require.True(t, TemporalTagged(beads[1].Metadata))
	})

	t.Run("empty variants", func(t *testing.T) {
		for _, raw := range []string{"", "  \n", "null", "[]"} {
			beads, err := parseBeadRows([]byte(raw))
			require.NoError(t, err, "raw=%q", raw)
			require.Empty(t, beads, "raw=%q", raw)
		}
	})

	t.Run("malformed is an error", func(t *testing.T) {
		_, err := parseBeadRows([]byte("{not json"))
		require.Error(t, err)
	})
}

func TestLoadSignalledState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	signalled, err := LoadSignalledState(path)
	require.NoError(t, err, "an absent file is normal — the orders never ran")
	require.Nil(t, signalled)

	require.NoError(t, os.WriteFile(path, []byte(`{"signalled":["gc-a","gc-b"]}`), 0o644))
	signalled, err = LoadSignalledState(path)
	require.NoError(t, err)
	require.Equal(t, []string{"gc-a", "gc-b"}, signalled)

	// Garbage is an ERROR the caller surfaces on the record — corrupt dedup
	// evidence must be distinguishable from "never signalled".
	require.NoError(t, os.WriteFile(path, []byte("{broken"), 0o644))
	_, err = LoadSignalledState(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse")
}

// ---------------------------------------------------------------------------
// [durable] config block.
// ---------------------------------------------------------------------------

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "city.toml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestLoadDurableConfig(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    DurableConfig
		wantErr string
	}{
		{
			name: "missing block yields defaults",
			body: "[workspace]\nname = \"ds-research\"\n",
			want: DurableConfig{Enabled: false, Mode: DurableModeObserve},
		},
		{
			name: "explicit observe",
			body: "[durable]\nenabled = true\nmode = \"observe\"\n",
			want: DurableConfig{Enabled: true, Mode: DurableModeObserve},
		},
		{
			name: "block bounded by the next section",
			body: "[durable]\nenabled = true\n\n[other]\nmode = \"execute\"\n",
			want: DurableConfig{Enabled: true, Mode: DurableModeObserve},
		},
		{
			name: "comments and inline comments",
			body: "# header\n[durable]\nenabled = false # off\nmode = \"observe\" # the only mode\n",
			want: DurableConfig{Enabled: false, Mode: DurableModeObserve},
		},
		{
			name:    "execute fails closed naming the memory gate",
			body:    "[durable]\nmode = \"execute\"\n",
			wantErr: "gc-qaid",
		},
		{
			name:    "unknown mode",
			body:    "[durable]\nmode = \"turbo\"\n",
			wantErr: "unrecognized mode",
		},
		{
			name:    "unknown key",
			body:    "[durable]\nspeed = \"fast\"\n",
			wantErr: "unknown key",
		},
		{
			name:    "bad enabled value",
			body:    "[durable]\nenabled = \"yes\"\n",
			wantErr: "enabled must be true or false",
		},
		{
			name: "multiline string cannot fake a section",
			body: "[other]\nprompt = \"\"\"\n[durable]\nmode = \"execute\"\n\"\"\"\n[durable]\nenabled = true\n",
			want: DurableConfig{Enabled: true, Mode: DurableModeObserve},
		},
		{
			// Codex repro 1: a trailing comment on the header must not make the
			// block invisible — that would bypass the fail-closed execute gate.
			name:    "header with trailing comment still gates execute",
			body:    "[durable] # rollout gate\nmode = \"execute\"\n",
			wantErr: "gc-qaid",
		},
		{
			name: "header with trailing comment parses the block",
			body: "[durable] # rollout gate\nenabled = true\n",
			want: DurableConfig{Enabled: true, Mode: DurableModeObserve},
		},
		{
			// Codex repro 2: a triple-quote inside a comment is dead text; it
			// must not open a phantom multiline string that swallows the block.
			name:    "triple-quote in a comment cannot hide the block",
			body:    "# tricky \"\"\" opener\n[durable]\nmode = \"execute\"\n",
			wantErr: "gc-qaid",
		},
		{
			name: "hash inside a quoted value is not a comment",
			body: "[durable]\nmode = \"observe\" # note the \"#\" char\n",
			want: DurableConfig{Enabled: false, Mode: DurableModeObserve},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadDurableConfig(writeConfig(t, tt.body))
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, cfg)
		})
	}

	t.Run("missing file is an error", func(t *testing.T) {
		_, err := LoadDurableConfig(filepath.Join(t.TempDir(), "nope.toml"))
		require.Error(t, err)
	})
}
