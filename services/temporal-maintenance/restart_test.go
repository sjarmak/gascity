package temporalmaintenance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWaitBoundary_StateDurable proves the workflow durably holds its state at a
// wait boundary: while parked at the human gate, repeated Queries at different
// simulated times return the same open-gate state, and no external mutation is
// recorded until a decision arrives. Temporal reconstructs exactly this state
// from event history after a worker restart, so the queryable wait-boundary
// state is what a resumed worker recovers.
//
// True forced-worker-termination-and-resume is exercised by the dev-server
// integration test and the replay test (replay_test.go) — the in-process
// testsuite cannot kill a worker, but it can prove the boundary state is stable
// and side-effect-free, which is the invariant a restart must preserve.
func TestWaitBoundary_StateDurable(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass})
		env.SignalWorkflow(SignalReviewComplete, branchEventSignal{Branch: "review", Verdict: VerdictPass})
	}, time.Second)

	assertParked := func() {
		val, err := env.QueryWorkflow(QueryState)
		require.NoError(t, err)
		var st MaintenanceCycleState
		require.NoError(t, val.Get(&st))
		require.Equal(t, PhaseAwaitingHuman, st.Phase)
		require.True(t, st.NeedsHuman)
		require.Nil(t, st.Decision)
		for _, m := range adapter.Recorded() {
			require.NotEqual(t, "gh pr merge", m.Action, "no external action before a decision")
		}
	}
	// Query twice while parked, at two different simulated times.
	env.RegisterDelayedCallback(assertParked, 2*time.Second)
	env.RegisterDelayedCallback(assertParked, 5*time.Second)

	// Only then approve, so the workflow can terminate.
	cb := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-approve", cb, HumanDecisionInput{
			Decision: DecisionApprove, Approver: "stephanie",
		})
	}, 8*time.Second)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "r", CycleKey: "c", RequireHumanGate: true, GatedAction: "gh pr merge", GatedTarget: "1712",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out MaintenanceCycleState
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, "completed", out.TerminalOutcome)
}

// TestWorkflowID_Stable pins the stable Workflow ID format from the design note.
func TestWorkflowID_Stable(t *testing.T) {
	require.Equal(t,
		"gascity-maintenance/gastownhall-gascity/2026-07-15T00",
		WorkflowID("gastownhall-gascity", "2026-07-15T00"))
}
