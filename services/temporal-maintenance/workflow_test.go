package temporalmaintenance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// updateCallback captures the result of a workflow Update sent through the test
// environment.
type updateCallback struct {
	t           *testing.T
	rejectErr   error
	completeVal interface{}
	completeErr error
	accepted    bool
}

func (u *updateCallback) Accept()          { u.accepted = true }
func (u *updateCallback) Reject(err error) { u.rejectErr = err }
func (u *updateCallback) Complete(v interface{}, err error) {
	u.completeVal = v
	u.completeErr = err
}

func newEnv(t *testing.T, adapter SideEffectAdapter) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{Adapter: adapter})
	return env
}

// TestHappyPath_NoGate runs a cycle with no gated external mutation: both
// branches receive their events and the cycle completes without a human gate
// and without recording any external action.
func TestHappyPath_NoGate(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass})
		env.SignalWorkflow(SignalReviewComplete, branchEventSignal{Branch: "review", Verdict: VerdictPass})
	}, time.Second)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "gastownhall-gascity", CycleKey: "2026-07-15T00",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var out MaintenanceCycleState
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, PhaseDone, out.Phase)
	require.Equal(t, "completed", out.TerminalOutcome)
	require.False(t, out.NeedsHuman)
	// Two selection beads were proposed (one per branch), nothing external.
	require.Len(t, adapter.Recorded(), 2)
	for _, m := range adapter.Recorded() {
		require.Equal(t, "gc sling (selection)", m.Action)
	}
}

// TestGatedApprove drives the full gate: events arrive, the workflow parks at
// the human gate (queryable NeedsHuman=true), a valid approve Update releases
// it, and the approved external mutation is recorded exactly once.
func TestGatedApprove(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass})
		env.SignalWorkflow(SignalReviewComplete, branchEventSignal{Branch: "review", Verdict: VerdictPass})
	}, time.Second)

	// At the gate, state must expose NeedsHuman before any decision arrives.
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow(QueryState)
		require.NoError(t, err)
		var st MaintenanceCycleState
		require.NoError(t, val.Get(&st))
		require.Equal(t, PhaseAwaitingHuman, st.Phase)
		require.True(t, st.NeedsHuman)
	}, 2*time.Second)

	cb := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-approve", cb, HumanDecisionInput{
			Decision: DecisionApprove, Approver: "stephanie",
		})
	}, 3*time.Second)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "gastownhall-gascity", CycleKey: "2026-07-15T00",
		RequireHumanGate: true, GatedAction: "gh pr merge", GatedTarget: "1712",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var out MaintenanceCycleState
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, PhaseDone, out.Phase)
	require.Equal(t, "completed", out.TerminalOutcome)
	require.NotNil(t, out.Decision)
	require.Equal(t, DecisionApprove, out.Decision.Decision)
	require.Equal(t, "stephanie", out.Decision.Approver)

	// Exactly one external mutation recorded, and it is the gated merge.
	var merges int
	for _, m := range adapter.Recorded() {
		if m.Action == "gh pr merge" {
			merges++
			require.Equal(t, "1712", m.Target)
		}
	}
	require.Equal(t, 1, merges, "gated merge must be recorded exactly once")
	require.Len(t, out.ArtifactRefs, 1)
}

// TestGatedReject_NoExternalAction proves a reject decision terminates the cycle
// without recording the external mutation.
func TestGatedReject_NoExternalAction(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass})
		env.SignalWorkflow(SignalReviewComplete, branchEventSignal{Branch: "review", Verdict: VerdictFail})
	}, time.Second)

	cb := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-reject", cb, HumanDecisionInput{
			Decision: DecisionReject, Approver: "stephanie", Note: "review failed",
		})
	}, 3*time.Second)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "gastownhall-gascity", CycleKey: "2026-07-15T00",
		RequireHumanGate: true, GatedAction: "gh pr merge", GatedTarget: "1712",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var out MaintenanceCycleState
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, "rejected", out.TerminalOutcome)
	for _, m := range adapter.Recorded() {
		require.NotEqual(t, "gh pr merge", m.Action, "reject must not record the external merge")
	}
}
