package temporalmaintenance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestUpdateValidation_InvalidDecision rejects a decision outside the allowed set.
func TestUpdateValidation_InvalidDecision(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass})
		env.SignalWorkflow(SignalReviewComplete, branchEventSignal{Branch: "review", Verdict: VerdictPass})
	}, time.Second)

	bad := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-bad", bad, HumanDecisionInput{
			Decision: Decision("frobnicate"), Approver: "stephanie",
		})
	}, 2*time.Second)
	// A valid approve after the bad one lets the workflow terminate.
	good := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-good", good, HumanDecisionInput{
			Decision: DecisionApprove, Approver: "stephanie",
		})
	}, 3*time.Second)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "r", CycleKey: "c", RequireHumanGate: true, GatedAction: "gh pr merge", GatedTarget: "1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Error(t, bad.rejectErr, "invalid decision must be rejected by the validator")
	require.Nil(t, good.rejectErr, "valid decision must be accepted")
}

// TestUpdateValidation_MissingApprover rejects a decision with no approver.
func TestUpdateValidation_MissingApprover(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass})
		env.SignalWorkflow(SignalReviewComplete, branchEventSignal{Branch: "review", Verdict: VerdictPass})
	}, time.Second)

	bad := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-noapprover", bad, HumanDecisionInput{
			Decision: DecisionApprove, Approver: "",
		})
	}, 2*time.Second)
	good := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-good", good, HumanDecisionInput{
			Decision: DecisionApprove, Approver: "stephanie",
		})
	}, 3*time.Second)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "r", CycleKey: "c", RequireHumanGate: true, GatedAction: "gh pr merge", GatedTarget: "1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, bad.rejectErr, "missing approver must be rejected")
}

// TestUpdateValidation_GateClosed rejects a decision sent before any gate is open.
func TestUpdateValidation_GateClosed(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	// Send the decision at t=0, before branch events and before the gate opens.
	early := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-early", early, HumanDecisionInput{
			Decision: DecisionApprove, Approver: "stephanie",
		})
	}, time.Millisecond)

	// Then let the cycle proceed and approve properly.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass})
		env.SignalWorkflow(SignalReviewComplete, branchEventSignal{Branch: "review", Verdict: VerdictPass})
	}, time.Second)
	good := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-good", good, HumanDecisionInput{
			Decision: DecisionApprove, Approver: "stephanie",
		})
	}, 3*time.Second)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "r", CycleKey: "c", RequireHumanGate: true, GatedAction: "gh pr merge", GatedTarget: "1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, early.rejectErr, "decision before the gate opens must be rejected")
}

// TestReprompt_ThenApprove proves the reprompt decision re-opens the gate and a
// subsequent approve carries through to completion.
func TestReprompt_ThenApprove(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass})
		env.SignalWorkflow(SignalReviewComplete, branchEventSignal{Branch: "review", Verdict: VerdictPass})
	}, time.Second)

	rep := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-reprompt", rep, HumanDecisionInput{
			Decision: DecisionReprompt, Approver: "stephanie", Note: "need more context",
		})
	}, 2*time.Second)
	app := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u-approve", app, HumanDecisionInput{
			Decision: DecisionApprove, Approver: "stephanie",
		})
	}, 4*time.Second)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "r", CycleKey: "c", RequireHumanGate: true, GatedAction: "gh pr merge", GatedTarget: "1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out MaintenanceCycleState
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, "completed", out.TerminalOutcome)
	require.Equal(t, DecisionApprove, out.Decision.Decision)
}
