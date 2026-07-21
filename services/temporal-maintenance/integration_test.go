package temporalmaintenance

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

// TestIntegration_ForcedWorkerRestart_ResumesSingleMutation is the pivotal
// dev-server-gated evidence for the pilot: a worker killed while the cycle is
// parked at the human gate is replaced by a *fresh* worker that resumes the exact
// Run from event history and completes it with exactly one external mutation.
//
// The single-mutation proof is structural. worker1 runs both DispatchSelection
// activities (its adapter records two selection proposals). worker1 is then
// stopped while parked at the gate. worker2 is started with a brand-new, empty
// adapter. Because the selection activity results already live in history, a
// resuming worker replays them without re-executing the activity, so worker2's
// adapter never sees the selections; only the post-approval ProposeExternalAction
// runs on worker2. Asserting worker2 recorded exactly the merge — and nothing
// else — proves both "resumed from history, not worker memory" and "the gated
// mutation executed exactly once across the restart".
//
// It skips (does not vacuously pass) when the temporal CLI is not installed, the
// same discipline replay_test.go uses for its captured history.
func TestIntegration_ForcedWorkerRestart_ResumesSingleMutation(t *testing.T) {
	bin, err := exec.LookPath("temporal")
	if err != nil {
		t.Skip("temporal CLI not on PATH — install it (see README) to run the forced-restart integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{
		ExistingPath: bin,
		LogLevel:     "error",
	})
	require.NoError(t, err, "start local dev server")
	defer func() { _ = srv.Stop() }()

	c := srv.Client()
	defer c.Close()

	const (
		repo     = "gastownhall-gascity"
		cycleKey = "integration-restart"
	)
	wid := WorkflowID(repo, cycleKey)

	// --- worker1: drives the cycle up to the human gate, then "crashes". ---
	adapter1 := NewDryRunAdapter()
	w1 := worker.New(c, TaskQueue, worker.Options{})
	w1.RegisterWorkflow(MaintenanceCycleWorkflow)
	w1.RegisterActivity(&Activities{Adapter: adapter1})
	require.NoError(t, w1.Start(), "start worker1")

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        wid,
		TaskQueue: TaskQueue,
	}, MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo:             repo,
		CycleKey:         cycleKey,
		RequireHumanGate: true,
		GatedAction:      "gh pr merge",
		GatedTarget:      "1712",
	})
	require.NoError(t, err, "start workflow")

	require.NoError(t, c.SignalWorkflow(ctx, wid, "", SignalCICompleted,
		branchEventSignal{Branch: "author", Verdict: VerdictPass}))
	require.NoError(t, c.SignalWorkflow(ctx, wid, "", SignalReviewComplete,
		branchEventSignal{Branch: "review", Verdict: VerdictPass}))

	waitForPhase(ctx, t, c, wid, PhaseAwaitingHuman)

	// worker1 completed exactly the two selection dispatches before the gate.
	require.Len(t, adapter1.Recorded(), 2, "worker1 recorded the two selection dispatches")

	// --- Forced restart: stop worker1 while the workflow is parked. ---
	w1.Stop()

	// The workflow keeps running server-side with no worker present.
	desc, err := c.DescribeWorkflowExecution(ctx, wid, "")
	require.NoError(t, err)
	require.Equal(t, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, desc.GetWorkflowExecutionInfo().GetStatus(),
		"workflow is still Running while no worker is alive")

	// --- worker2: fresh empty adapter, resumes from history. ---
	adapter2 := NewDryRunAdapter()
	w2 := worker.New(c, TaskQueue, worker.Options{})
	w2.RegisterWorkflow(MaintenanceCycleWorkflow)
	w2.RegisterActivity(&Activities{Adapter: adapter2})
	require.NoError(t, w2.Start(), "start worker2")
	defer w2.Stop()

	// Query works again once worker2 is polling; state is exactly what worker1 left.
	waitForPhase(ctx, t, c, wid, PhaseAwaitingHuman)
	require.Empty(t, adapter2.Recorded(),
		"worker2 must not re-run the selection activities — they replay from history")

	// --- Approve through the validated human Update. ---
	handle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   wid,
		UpdateName:   UpdateHumanDecision,
		WaitForStage: client.WorkflowUpdateStageCompleted,
		Args: []interface{}{HumanDecisionInput{
			Decision: DecisionApprove,
			Approver: "stephanie",
		}},
	})
	require.NoError(t, err, "submit approve update")
	var rec HumanDecisionRecord
	require.NoError(t, handle.Get(ctx, &rec))
	require.Equal(t, DecisionApprove, rec.Decision)
	require.Equal(t, "stephanie", rec.Approver)

	// --- Workflow completes; exactly one external mutation, run on worker2. ---
	var out MaintenanceCycleState
	require.NoError(t, run.Get(ctx, &out), "workflow completes after resume")
	require.Equal(t, PhaseDone, out.Phase)
	require.Equal(t, "completed", out.TerminalOutcome)
	require.Len(t, out.ArtifactRefs, 1, "exactly one gated mutation recorded")

	recorded := adapter2.Recorded()
	require.Len(t, recorded, 1, "worker2 executed the gated mutation exactly once and nothing else")
	require.Equal(t, "gh pr merge", recorded[0].Action)
	require.Equal(t, out.ArtifactRefs[0], recorded[0].IdempotencyKey,
		"artifact ref is the idempotency key of the single mutation")

	// --- Duplicate decision after the gate closed is rejected. ---
	_, err = c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   wid,
		UpdateName:   UpdateHumanDecision,
		WaitForStage: client.WorkflowUpdateStageCompleted,
		Args: []interface{}{HumanDecisionInput{
			Decision: DecisionApprove,
			Approver: "stephanie",
		}},
	})
	require.Error(t, err, "a second decision after completion must not produce a second mutation")
}

// TestIntegration_ArmedRealAdapter_SingleExecutionAcrossRestart is the P2
// counterpart to the dry-run restart test: it drives the workflow with an ARMED
// RealAdapter (persisted KeyStore + a recording runner standing in for gc/gh)
// and proves the gated mutation executes exactly once across a forced worker
// restart, and only after the human approve Update.
//
// worker1 and worker2 bind SEPARATE runners over a SHARED on-disk store. The two
// selection dispatches run on worker1; they replay from history on worker2
// (never re-run), so worker2's runner sees exactly the one post-approval gated
// mutation. The shared store is what guarantees a resumed ProposeExternalAction
// cannot double-fire.
func TestIntegration_ArmedRealAdapter_SingleExecutionAcrossRestart(t *testing.T) {
	bin, err := exec.LookPath("temporal")
	if err != nil {
		t.Skip("temporal CLI not on PATH — install it (see README) to run the armed-adapter integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{ExistingPath: bin, LogLevel: "error"})
	require.NoError(t, err, "start local dev server")
	defer func() { _ = srv.Stop() }()

	c := srv.Client()
	defer c.Close()

	const (
		repo     = "gastownhall-gascity"
		cycleKey = "staging-armed-restart" // staging/* discipline: throwaway key
	)
	wid := WorkflowID(repo, cycleKey)
	storeDir := t.TempDir()

	newArmed := func(t *testing.T) (*RealAdapter, *recordingRunner) {
		store, err := NewKeyStore(storeDir)
		require.NoError(t, err)
		rr := &recordingRunner{}
		return NewArmedRealAdapter(store, rr), rr
	}

	// --- worker1: runs the two selections, parks at the gate, "crashes". ---
	adapter1, rr1 := newArmed(t)
	w1 := worker.New(c, TaskQueue, worker.Options{})
	w1.RegisterWorkflow(MaintenanceCycleWorkflow)
	w1.RegisterActivity(&Activities{Adapter: adapter1})
	require.NoError(t, w1.Start(), "start worker1")

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: wid, TaskQueue: TaskQueue},
		MaintenanceCycleWorkflow, MaintenanceCycleInput{
			Repo: repo, CycleKey: cycleKey, RequireHumanGate: true,
			GatedAction: "gh pr merge", GatedTarget: "1712",
		})
	require.NoError(t, err, "start workflow")

	require.NoError(t, c.SignalWorkflow(ctx, wid, "", SignalCICompleted,
		branchEventSignal{Branch: "author", Verdict: VerdictPass}))
	require.NoError(t, c.SignalWorkflow(ctx, wid, "", SignalReviewComplete,
		branchEventSignal{Branch: "review", Verdict: VerdictPass}))

	waitForPhase(ctx, t, c, wid, PhaseAwaitingHuman)
	require.Equal(t, 2, rr1.count(), "worker1 executed the two selection dispatches once each")

	w1.Stop()

	// --- worker2: fresh runner, SAME persisted store, resumes from history. ---
	adapter2, rr2 := newArmed(t)
	w2 := worker.New(c, TaskQueue, worker.Options{})
	w2.RegisterWorkflow(MaintenanceCycleWorkflow)
	w2.RegisterActivity(&Activities{Adapter: adapter2})
	require.NoError(t, w2.Start(), "start worker2")
	defer w2.Stop()

	waitForPhase(ctx, t, c, wid, PhaseAwaitingHuman)
	require.Zero(t, rr2.count(), "worker2 must not re-run the selections — they replay from history")

	// Mutation only happens after the human Update.
	handle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID: wid, UpdateName: UpdateHumanDecision,
		WaitForStage: client.WorkflowUpdateStageCompleted,
		Args:         []interface{}{HumanDecisionInput{Decision: DecisionApprove, Approver: "stephanie"}},
	})
	require.NoError(t, err, "submit approve update")
	var rec HumanDecisionRecord
	require.NoError(t, handle.Get(ctx, &rec))
	require.Equal(t, DecisionApprove, rec.Decision)

	var out MaintenanceCycleState
	require.NoError(t, run.Get(ctx, &out), "workflow completes after resume")
	require.Equal(t, PhaseDone, out.Phase)
	require.Len(t, out.ArtifactRefs, 1, "exactly one gated mutation")

	require.Equal(t, 1, rr2.count(), "worker2 executed the gated mutation exactly once")
	require.Equal(t, out.ArtifactRefs[0], rr2.calls[0], "the single execution is the gated key")

	// The persisted store holds all three mutations (2 selection + 1 gated).
	all, err := adapter2.store.All()
	require.NoError(t, err)
	require.Len(t, all, 3, "store recorded two selections and one gated mutation")
	for _, r := range all {
		require.Equal(t, ExecDone, r.Status, "every recorded mutation completed exactly once")
	}
}

// waitForPhase polls the state Query until the workflow reports want, failing the
// test if it does not arrive before ctx expires.
func waitForPhase(ctx context.Context, t *testing.T, c client.Client, wid string, want Phase) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := c.QueryWorkflow(ctx, wid, "", QueryState)
		if err == nil {
			var st MaintenanceCycleState
			if err := resp.Get(&st); err == nil && st.Phase == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("workflow did not reach phase %q before timeout", want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
