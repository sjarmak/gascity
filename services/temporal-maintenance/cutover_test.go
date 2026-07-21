package temporalmaintenance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Dispatch-only is the legacy-parity mode: it runs the two selection dispatches
// and completes immediately — no fanout, no CI/review wait, no gate, no merge.
func TestDispatchOnly_CompletesAfterSelection(t *testing.T) {
	adapter := NewDryRunAdapter()
	env := newEnv(t, adapter)

	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "gastownhall-gascity", CycleKey: "k", DispatchOnly: true,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var out MaintenanceCycleState
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, PhaseDone, out.Phase)
	require.Equal(t, "dispatched", out.TerminalOutcome)
	// Both branches were dispatched; nothing waited on events.
	require.Len(t, out.BeadIDs, 2, "both selection beads dispatched")
	require.False(t, out.NeedsHuman, "dispatch-only never opens a gate")
	require.Len(t, adapter.Recorded(), 2, "two selection proposals, no gated action")
}

// With no CycleKey (a recurring Schedule's static input), the workflow derives a
// deterministic per-fire key from its clock and still completes dispatch-only.
func TestDispatchOnly_DerivesCycleKeyWhenEmpty(t *testing.T) {
	env := newEnv(t, NewDryRunAdapter())
	env.ExecuteWorkflow(MaintenanceCycleWorkflow, MaintenanceCycleInput{
		Repo: "gastownhall-gascity", DispatchOnly: true, // CycleKey empty
	})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var out MaintenanceCycleState
	require.NoError(t, env.GetWorkflowResult(&out))
	require.NotEmpty(t, out.CycleKey, "cycle key must be derived when input is empty")
	require.Len(t, out.CycleKey, len("20060102T150405"), "derived key is a second-granularity timestamp")
	require.Equal(t, "dispatched", out.TerminalOutcome)
}

// selectionParams returns shadow params (recorded, not executed) with no config.
func TestSelectionParams_ShadowWhenUnconfigured(t *testing.T) {
	a := &Activities{Adapter: NewDryRunAdapter()}
	params, body := a.selectionParams("review", "title")
	require.Equal(t, "temporal-shadow", params["label"])
	require.Empty(t, params["polecat"])
	require.Empty(t, body)
}

// With a SelectionConfig it returns the real create+sling params + the branch's
// prompt path.
func TestSelectionParams_RealWhenConfigured(t *testing.T) {
	a := &Activities{
		Adapter: NewDryRunAdapter(),
		Selection: &SelectionConfig{
			Polecat: "polecat-pool", Rig: "gascity", Priority: "1",
			PromptFile: map[string]string{"review": "/abs/review.md", "author": "/abs/author.md"},
		},
	}
	params, body := a.selectionParams("review", "maintenance-cycle review — r/k")
	require.Equal(t, "polecat-pool", params["polecat"])
	require.Equal(t, "maintenance-cycle review — r/k", params["title"])
	require.Equal(t, "1", params["priority"])
	require.Contains(t, params["labels"], "maintenance-cycle:review")
	require.Contains(t, params["labels"], "rig:gascity")
	require.Equal(t, "/abs/review.md", body)
}
