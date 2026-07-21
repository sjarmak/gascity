package temporalmaintenance

import (
	"context"
	"errors"
	"testing"
)

type sentSignal struct {
	workflowID string
	name       string
	arg        interface{}
}

type fakeSender struct {
	sent []sentSignal
	err  error
}

func (f *fakeSender) SignalWorkflow(_ context.Context, wid, _ string, name string, arg interface{}) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentSignal{wid, name, arg})
	return nil
}

type fakeReader struct {
	st  MaintenanceCycleState
	err error
}

func (f *fakeReader) QueryState(_ context.Context, _ string) (MaintenanceCycleState, error) {
	return f.st, f.err
}

func awaitingState(repo, cycle string) MaintenanceCycleState {
	return MaintenanceCycleState{
		Repo: repo, CycleKey: cycle, Phase: PhaseAwaitingEvents,
		Branches: map[string]BranchState{
			"author": {Kind: "author"},
			"review": {Kind: "review"},
		},
	}
}

func TestSignaler_TypedSignals(t *testing.T) {
	f := &fakeSender{}
	s := NewSignaler(f)
	ctx := context.Background()

	if err := s.BeadClosed(ctx, "r", "k", "gc-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.CICompleted(ctx, "r", "k", "author", VerdictPass); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 2 {
		t.Fatalf("sent %d signals, want 2", len(f.sent))
	}
	if f.sent[0].workflowID != WorkflowID("r", "k") {
		t.Fatalf("workflow id = %q", f.sent[0].workflowID)
	}
	if f.sent[0].name != SignalBeadClosed || f.sent[1].name != SignalCICompleted {
		t.Fatalf("signal names = %q, %q", f.sent[0].name, f.sent[1].name)
	}
	if bc, ok := f.sent[0].arg.(beadClosedSignal); !ok || bc.Bead != "gc-1" {
		t.Fatalf("bead-closed payload = %+v", f.sent[0].arg)
	}
	if ev, ok := f.sent[1].arg.(branchEventSignal); !ok || ev.Branch != "author" || ev.Verdict != VerdictPass {
		t.Fatalf("ci payload = %+v", f.sent[1].arg)
	}
}

func TestReconciler_RepairsDroppedVerdict(t *testing.T) {
	sender := &fakeSender{}
	r := NewReconciler(&fakeReader{st: awaitingState("r", "k")}, NewSignaler(sender))
	repaired, err := r.Reconcile(context.Background(), "r", "k", []BranchTruth{
		{Branch: "author", CIVerdict: VerdictPass},
		{Branch: "review", ReviewVerdict: VerdictPass},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 2 || len(sender.sent) != 2 {
		t.Fatalf("repaired=%v sent=%d, want 2 and 2", repaired, len(sender.sent))
	}
}

func TestReconciler_SkipsVerdictWorkflowAlreadyHas(t *testing.T) {
	st := awaitingState("r", "k")
	st.Branches["author"] = BranchState{Kind: "author", CIVerdict: VerdictPass} // already observed
	sender := &fakeSender{}
	r := NewReconciler(&fakeReader{st: st}, NewSignaler(sender))
	repaired, err := r.Reconcile(context.Background(), "r", "k", []BranchTruth{
		{Branch: "author", CIVerdict: VerdictPass},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 0 || len(sender.sent) != 0 {
		t.Fatalf("repaired=%v sent=%d, want none — idempotent", repaired, len(sender.sent))
	}
}

func TestReconciler_NoRepairPastAwaiting(t *testing.T) {
	st := awaitingState("r", "k")
	st.Phase = PhaseDone
	sender := &fakeSender{}
	r := NewReconciler(&fakeReader{st: st}, NewSignaler(sender))
	repaired, err := r.Reconcile(context.Background(), "r", "k", []BranchTruth{
		{Branch: "author", CIVerdict: VerdictPass},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 0 || len(sender.sent) != 0 {
		t.Fatalf("a completed cycle must not be signalled; got repaired=%v sent=%d", repaired, len(sender.sent))
	}
}

// A truth field that doesn't match the branch kind (CI on the review branch, or
// review on the author branch) is ignored — repairing it would send a signal the
// workflow never inspects for that branch, a false "repaired" that wouldn't
// unstick the cycle.
func TestReconciler_IgnoresWrongKindField(t *testing.T) {
	sender := &fakeSender{}
	r := NewReconciler(&fakeReader{st: awaitingState("r", "k")}, NewSignaler(sender))
	repaired, err := r.Reconcile(context.Background(), "r", "k", []BranchTruth{
		{Branch: "review", CIVerdict: VerdictPass},     // wrong field for a review branch
		{Branch: "author", ReviewVerdict: VerdictPass}, // wrong field for an author branch
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 0 || len(sender.sent) != 0 {
		t.Fatalf("wrong-kind fields must be ignored; got repaired=%v sent=%d", repaired, len(sender.sent))
	}
}

// Duplicate truth entries for one branch repair it at most once.
func TestReconciler_DedupsBranch(t *testing.T) {
	sender := &fakeSender{}
	r := NewReconciler(&fakeReader{st: awaitingState("r", "k")}, NewSignaler(sender))
	repaired, err := r.Reconcile(context.Background(), "r", "k", []BranchTruth{
		{Branch: "author", CIVerdict: VerdictPass},
		{Branch: "author", CIVerdict: VerdictFail},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repaired) != 1 || len(sender.sent) != 1 {
		t.Fatalf("duplicate branch must repair once; got repaired=%v sent=%d", repaired, len(sender.sent))
	}
}

type fakeFetcher struct {
	st  PRStatus
	err error
}

func (f *fakeFetcher) Fetch(_ context.Context, _, _ string) (PRStatus, error) { return f.st, f.err }

func TestCIReviewShim_RelaysKnownVerdict(t *testing.T) {
	sender := &fakeSender{}
	shim := NewCIReviewShim(&fakeFetcher{st: PRStatus{CIVerdict: VerdictPass}}, NewSignaler(sender))
	sent, err := shim.Relay(context.Background(), "r", "k", "author", "1712")
	if err != nil || !sent {
		t.Fatalf("Relay = (%v, %v), want sent=true", sent, err)
	}
	if len(sender.sent) != 1 || sender.sent[0].name != SignalCICompleted {
		t.Fatalf("sent = %+v", sender.sent)
	}
}

func TestCIReviewShim_PendingSendsNothing(t *testing.T) {
	sender := &fakeSender{}
	shim := NewCIReviewShim(&fakeFetcher{st: PRStatus{CIVerdict: VerdictUnknown}}, NewSignaler(sender))
	sent, err := shim.Relay(context.Background(), "r", "k", "author", "1712")
	if err != nil || sent {
		t.Fatalf("Relay of pending = (%v, %v), want sent=false", sent, err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("pending verdict must send nothing, sent %d", len(sender.sent))
	}
}

func TestCIReviewShim_UnknownBranch(t *testing.T) {
	shim := NewCIReviewShim(&fakeFetcher{}, NewSignaler(&fakeSender{}))
	if _, err := shim.Relay(context.Background(), "r", "k", "sideways", "1"); err == nil {
		t.Fatal("unknown branch must error")
	}
}

func TestCIReviewShim_FetchErrorPropagates(t *testing.T) {
	shim := NewCIReviewShim(&fakeFetcher{err: errors.New("gh down")}, NewSignaler(&fakeSender{}))
	if _, err := shim.Relay(context.Background(), "r", "k", "author", "1"); err == nil {
		t.Fatal("fetch error must propagate")
	}
}

func TestReviewVerdictMapping(t *testing.T) {
	cases := map[string]Verdict{
		"APPROVED": VerdictPass, "CHANGES_REQUESTED": VerdictFail,
		"REVIEW_REQUIRED": VerdictUnknown, "": VerdictUnknown,
	}
	for in, want := range cases {
		if got := reviewVerdict(in); got != want {
			t.Errorf("reviewVerdict(%q) = %q, want %q", in, got, want)
		}
	}
}

func rollup(entries ...statusCheck) ghPRView { return ghPRView{StatusCheckRollup: entries} }

func TestCIVerdictMapping(t *testing.T) {
	success := statusCheck{Status: "COMPLETED", Conclusion: "SUCCESS"}

	cases := []struct {
		name string
		view ghPRView
		want Verdict
	}{
		{"all success", rollup(success), VerdictPass},
		{"success+neutral+skipped", rollup(success,
			statusCheck{Status: "COMPLETED", Conclusion: "NEUTRAL"},
			statusCheck{Status: "COMPLETED", Conclusion: "SKIPPED"}), VerdictPass},
		{"legacy status success", rollup(statusCheck{State: "SUCCESS"}), VerdictPass},
		{"failure", rollup(statusCheck{Status: "COMPLETED", Conclusion: "FAILURE"}), VerdictFail},
		{"startup_failure", rollup(statusCheck{Status: "COMPLETED", Conclusion: "STARTUP_FAILURE"}), VerdictFail},
		{"legacy status error", rollup(statusCheck{State: "ERROR"}), VerdictFail},
		{"empty rollup", ghPRView{}, VerdictUnknown},
		{"in-progress", rollup(statusCheck{Status: "IN_PROGRESS"}), VerdictUnknown},
		{"legacy pending", rollup(statusCheck{State: "PENDING"}), VerdictUnknown},
		// The regression cases: these must NOT be a false pass.
		{"action_required", rollup(statusCheck{Status: "COMPLETED", Conclusion: "ACTION_REQUIRED"}), VerdictUnknown},
		{"stale", rollup(statusCheck{Status: "COMPLETED", Conclusion: "STALE"}), VerdictUnknown},
		{"legacy expected", rollup(statusCheck{State: "EXPECTED"}), VerdictUnknown},
		{"completed empty conclusion", rollup(statusCheck{Status: "COMPLETED", Conclusion: ""}), VerdictUnknown},
		{"unknown future enum", rollup(statusCheck{Status: "COMPLETED", Conclusion: "SOME_NEW_STATE"}), VerdictUnknown},
		{"success mixed with action_required", rollup(success,
			statusCheck{Status: "COMPLETED", Conclusion: "ACTION_REQUIRED"}), VerdictUnknown},
		{"success mixed with failure", rollup(success,
			statusCheck{Status: "COMPLETED", Conclusion: "FAILURE"}), VerdictFail},
	}
	for _, tc := range cases {
		if got := ciVerdict(tc.view); got != tc.want {
			t.Errorf("%s: ciVerdict = %q, want %q", tc.name, got, tc.want)
		}
	}
}
