package temporalmaintenance

import "context"

// WorkflowStateReader queries a running workflow's current state (the `state`
// Query). *client.Client is adapted to this in bridge_client.go; tests use a
// fake.
type WorkflowStateReader interface {
	QueryState(ctx context.Context, workflowID string) (MaintenanceCycleState, error)
}

// BranchTruth is a branch's real external verdict as observed right now (e.g. via
// gh REST or the bead store) — the ground truth the workflow's recorded state is
// repaired against.
type BranchTruth struct {
	Branch        string
	CIVerdict     Verdict // author branch
	ReviewVerdict Verdict // review branch
}

// Reconciler repairs a lost boundary event. A dropped signal (order exec crashed,
// Temporal briefly unreachable) leaves the workflow parked awaiting an event that
// already happened; the reconciler re-sends it from ground truth. It is the
// narrow safety net the promotion plan requires alongside the at-most-once
// executor — the signal path is at-least-once by re-drive, the mutation path is
// at-most-once by the persisted claim.
type Reconciler struct {
	reader   WorkflowStateReader
	signaler *Signaler
}

// NewReconciler binds a Reconciler to a state reader and a signaler.
func NewReconciler(reader WorkflowStateReader, signaler *Signaler) *Reconciler {
	return &Reconciler{reader: reader, signaler: signaler}
}

// Reconcile re-sends any boundary signal the workflow has not observed but which
// truth says has occurred, for one cycle. It is a no-op unless the workflow is
// still awaiting events, and it skips branches whose verdict the workflow already
// holds — so repeated runs never spam signals. Returns the repairs it made
// (e.g. "author:ci", "review:review").
func (r *Reconciler) Reconcile(ctx context.Context, repo, cycleKey string, truth []BranchTruth) ([]string, error) {
	st, err := r.reader.QueryState(ctx, WorkflowID(repo, cycleKey))
	if err != nil {
		return nil, err
	}
	// Only repair while the workflow is still waiting on branch events. Once it
	// has advanced past AwaitingEvents, a late signal is noise.
	if st.Phase != PhaseFanout && st.Phase != PhaseAwaitingEvents {
		return nil, nil
	}

	var repaired []string
	seen := map[string]bool{}
	for _, t := range truth {
		bs, ok := st.Branches[t.Branch]
		if !ok || seen[t.Branch] {
			continue // unknown or duplicate branch entry — repair each branch once
		}
		seen[t.Branch] = true
		// Only the verdict field that actually completes this branch kind is
		// repaired (BranchState.Complete: author waits on CI, review on review) —
		// a mis-populated truth field for the other kind is ignored, never sent as
		// a repair that wouldn't unstick the cycle.
		switch bs.Kind {
		case "author":
			if t.CIVerdict != VerdictUnknown && bs.CIVerdict == VerdictUnknown {
				if err := r.signaler.CICompleted(ctx, repo, cycleKey, t.Branch, t.CIVerdict); err != nil {
					return repaired, err
				}
				repaired = append(repaired, t.Branch+":ci")
			}
		case "review":
			if t.ReviewVerdict != VerdictUnknown && bs.ReviewVerdict == VerdictUnknown {
				if err := r.signaler.ReviewCompleted(ctx, repo, cycleKey, t.Branch, t.ReviewVerdict); err != nil {
					return repaired, err
				}
				repaired = append(repaired, t.Branch+":review")
			}
		}
	}
	return repaired, nil
}
