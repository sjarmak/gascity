package temporalmaintenance

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

// bridgeDevServer starts a dev server + a DryRunAdapter worker and a running
// cycle parked before its branch events, returning the client and workflow id.
func bridgeDevServer(t *testing.T, cycleKey string) (context.Context, client.Client, string, func()) {
	t.Helper()
	bin, err := exec.LookPath("temporal")
	if err != nil {
		t.Skip("temporal CLI not on PATH — install it (see README) to run the bridge integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)

	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{ExistingPath: bin, LogLevel: "error"})
	require.NoError(t, err, "start dev server")
	c := srv.Client()

	w := worker.New(c, TaskQueue, worker.Options{})
	w.RegisterWorkflow(MaintenanceCycleWorkflow)
	w.RegisterActivity(&Activities{Adapter: NewDryRunAdapter()})
	require.NoError(t, w.Start(), "start worker")

	const repo = "gastownhall-gascity"
	wid := WorkflowID(repo, cycleKey)
	_, err = c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: wid, TaskQueue: TaskQueue},
		MaintenanceCycleWorkflow, MaintenanceCycleInput{
			Repo: repo, CycleKey: cycleKey, RequireHumanGate: true,
			GatedAction: "gh pr merge", GatedTarget: "1712",
		})
	require.NoError(t, err, "start workflow")

	cleanup := func() {
		w.Stop()
		c.Close()
		_ = srv.Stop()
		cancel()
	}
	return ctx, c, wid, cleanup
}

// The workflow is driven to its human gate entirely through the real Signaler
// (the production path), not the tests' direct c.SignalWorkflow calls.
func TestIntegration_Bridge_SignalerDrivesWorkflow(t *testing.T) {
	ctx, c, wid, cleanup := bridgeDevServer(t, "bridge-signaler")
	defer cleanup()

	const repo, cycle = "gastownhall-gascity", "bridge-signaler"
	s := NewSignaler(c)
	require.NoError(t, s.CICompleted(ctx, repo, cycle, "author", VerdictPass))
	require.NoError(t, s.ReviewCompleted(ctx, repo, cycle, "review", VerdictPass))

	waitForPhase(ctx, t, c, wid, PhaseAwaitingHuman)
}

// A deliberately dropped review signal parks the workflow; the Reconciler reads
// state, sees the review branch still unobserved, and re-sends it from ground
// truth, unsticking the cycle.
func TestIntegration_Bridge_ReconcilerRepairsDroppedEvent(t *testing.T) {
	ctx, c, wid, cleanup := bridgeDevServer(t, "bridge-reconcile")
	defer cleanup()

	const repo, cycle = "gastownhall-gascity", "bridge-reconcile"
	s := NewSignaler(c)

	// Only the author CI arrives; the review signal is DROPPED.
	require.NoError(t, s.CICompleted(ctx, repo, cycle, "author", VerdictPass))

	// The workflow stays in AwaitingEvents — the review branch never completed.
	requireStuckAwaiting(ctx, t, c, wid)

	// The reconciler, given ground truth that review actually passed, repairs it.
	rec := NewReconciler(NewClientStateReader(c), s)
	repaired, err := rec.Reconcile(ctx, repo, cycle, []BranchTruth{
		{Branch: "review", ReviewVerdict: VerdictPass},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"review:review"}, repaired, "reconciler re-sent exactly the dropped review verdict")

	// With the boundary repaired the cycle advances to its human gate.
	waitForPhase(ctx, t, c, wid, PhaseAwaitingHuman)

	// Re-running the reconciler now is a no-op (idempotent, past the window).
	again, err := rec.Reconcile(ctx, repo, cycle, []BranchTruth{{Branch: "review", ReviewVerdict: VerdictPass}})
	require.NoError(t, err)
	require.Empty(t, again, "second reconcile repairs nothing")
}

// requireStuckAwaiting waits for the cycle to reach AwaitingEvents, then asserts
// it stays there for a short window (the review branch is genuinely missing, not
// merely slow to arrive).
func requireStuckAwaiting(ctx context.Context, t *testing.T, c client.Client, wid string) {
	t.Helper()
	waitForPhase(ctx, t, c, wid, PhaseAwaitingEvents)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := c.QueryWorkflow(ctx, wid, "", QueryState)
		require.NoError(t, err)
		var st MaintenanceCycleState
		require.NoError(t, resp.Get(&st))
		require.Equal(t, PhaseAwaitingEvents, st.Phase, "workflow should stay parked awaiting the dropped event")
		require.Equal(t, VerdictUnknown, st.Branches["review"].ReviewVerdict, "review branch must be unobserved")
		time.Sleep(300 * time.Millisecond)
	}
}
