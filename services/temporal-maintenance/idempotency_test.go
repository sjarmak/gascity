package temporalmaintenance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAdapter_DedupsByKey proves the dry-run adapter is find-or-create: a second
// Propose with the same idempotency key returns the first record and does not
// grow the recorded set. This is what makes Activity retries and duplicate
// deliveries safe.
func TestAdapter_DedupsByKey(t *testing.T) {
	a := NewDryRunAdapter()
	m := ProposedMutation{IdempotencyKey: "k1", Action: "gh pr merge", Target: "1712"}

	rec1, created1, err := a.Propose(context.Background(), m)
	require.NoError(t, err)
	require.True(t, created1)

	rec2, created2, err := a.Propose(context.Background(), m)
	require.NoError(t, err)
	require.False(t, created2, "second propose with same key must not create")
	require.Equal(t, rec1, rec2)
	require.Len(t, a.Recorded(), 1)
}

// TestAdapter_RejectsMissingKey guards against an unkeyed mutation slipping in.
func TestAdapter_RejectsMissingKey(t *testing.T) {
	a := NewDryRunAdapter()
	_, _, err := a.Propose(context.Background(), ProposedMutation{Action: "gh pr merge"})
	require.Error(t, err)
}

// TestDuplicateSignals_NoDuplicateBeads sends the same signals twice and proves
// bead IDs are recorded once and the cycle still completes cleanly.
func TestDuplicateSignals_NoDuplicateBeads(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalBeadClosed, beadClosedSignal{Bead: "gc-dup"})
		env.SignalWorkflow(SignalBeadClosed, beadClosedSignal{Bead: "gc-dup"}) // duplicate delivery
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass})
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass}) // duplicate
		env.SignalWorkflow(SignalReviewComplete, branchEventSignal{Branch: "review", Verdict: VerdictPass})
	}, time.Second)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{Repo: "r", CycleKey: "c"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var out MaintenanceCycleState
	require.NoError(t, env.GetWorkflowResult(&out))
	// gc-dup appears exactly once despite double delivery.
	var count int
	for _, b := range out.BeadIDs {
		if b == "gc-dup" {
			count++
		}
	}
	require.Equal(t, 1, count, "duplicate bead.closed must not double-record")
}

// TestDuplicateApprove_SingleExternalAction sends two approve Updates; the first
// closes the gate and completes the cycle, so only one external mutation is ever
// recorded regardless of the second delivery. (The validator's gate-closed
// rejection path is covered separately by TestUpdateValidation_GateClosed.)
func TestDuplicateApprove_SingleExternalAction(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalCICompleted, branchEventSignal{Branch: "author", Verdict: VerdictPass})
		env.SignalWorkflow(SignalReviewComplete, branchEventSignal{Branch: "review", Verdict: VerdictPass})
	}, time.Second)

	first := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u1", first, HumanDecisionInput{
			Decision: DecisionApprove, Approver: "stephanie",
		})
	}, 2*time.Second)
	// A duplicate approve delivered after the first one already completed the
	// cycle must not add a second recorded mutation.
	second := &updateCallback{t: t}
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateHumanDecision, "u2", second, HumanDecisionInput{
			Decision: DecisionApprove, Approver: "stephanie",
		})
	}, 4*time.Second)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "r", CycleKey: "c", RequireHumanGate: true, GatedAction: "gh pr merge", GatedTarget: "1712",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var merges int
	for _, m := range adapter.Recorded() {
		if m.Action == "gh pr merge" {
			merges++
		}
	}
	require.Equal(t, 1, merges, "a duplicate approve must not re-record the merge")
}
