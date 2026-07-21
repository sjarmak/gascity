package temporalmaintenance

import "context"

// WorkflowSignaler is the narrow slice of the Temporal client the bridge needs:
// deliver a named signal to a workflow by ID. *client.Client satisfies it, and
// tests substitute a fake.
type WorkflowSignaler interface {
	SignalWorkflow(ctx context.Context, workflowID, runID, signalName string, arg interface{}) error
}

// Signaler converts real-world maintenance events into the typed Signals the
// MaintenanceCycleWorkflow waits on. It is the production path that replaces the
// tests' direct c.SignalWorkflow calls: an event-order exec (bead.closed /
// bead.created) and the interim CI/review shim both drive the workflow through
// this one component, so the signal names and payload shapes live in exactly one
// place.
type Signaler struct {
	client WorkflowSignaler
}

// NewSignaler binds a Signaler to a workflow-signal transport.
func NewSignaler(c WorkflowSignaler) *Signaler { return &Signaler{client: c} }

// BeadClosed signals that a bead the cycle is tracking has closed.
func (s *Signaler) BeadClosed(ctx context.Context, repo, cycleKey, bead string) error {
	return s.client.SignalWorkflow(ctx, WorkflowID(repo, cycleKey), "", SignalBeadClosed,
		beadClosedSignal{Bead: bead})
}

// CICompleted signals a CI verdict for the author branch.
func (s *Signaler) CICompleted(ctx context.Context, repo, cycleKey, branch string, v Verdict) error {
	return s.client.SignalWorkflow(ctx, WorkflowID(repo, cycleKey), "", SignalCICompleted,
		branchEventSignal{Branch: branch, Verdict: v})
}

// ReviewCompleted signals a review verdict for the review branch.
func (s *Signaler) ReviewCompleted(ctx context.Context, repo, cycleKey, branch string, v Verdict) error {
	return s.client.SignalWorkflow(ctx, WorkflowID(repo, cycleKey), "", SignalReviewComplete,
		branchEventSignal{Branch: branch, Verdict: v})
}

// Escalation signals that a branch escalated to a human.
func (s *Signaler) Escalation(ctx context.Context, repo, cycleKey, branch string) error {
	return s.client.SignalWorkflow(ctx, WorkflowID(repo, cycleKey), "", SignalEscalation,
		escalationSignal{Branch: branch})
}
