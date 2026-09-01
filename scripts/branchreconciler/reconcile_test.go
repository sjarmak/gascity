package branchreconciler

import (
	"reflect"
	"testing"
)

func TestReconcileOrdersNeverPRFirstThenByCommitsDescending(t *testing.T) {
	branches := []BranchInfo{
		{Name: "stale-worktree", CommitsAheadOfMain: 3},
		{Name: "fix/big-refactor", CommitsAheadOfMain: 90},
		{Name: "fix/shipped", CommitsAheadOfMain: 5},
		{Name: "fix/lost-fix", CommitsAheadOfMain: 12},
	}
	prHeadRefs := map[string]bool{
		"fix/shipped": true,
	}

	got := Reconcile(branches, prHeadRefs)

	want := []Report{
		{Branch: "fix/big-refactor", CommitsAheadOfMain: 90, EverHadPR: false},
		{Branch: "fix/lost-fix", CommitsAheadOfMain: 12, EverHadPR: false},
		{Branch: "stale-worktree", CommitsAheadOfMain: 3, EverHadPR: false},
		{Branch: "fix/shipped", CommitsAheadOfMain: 5, EverHadPR: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconcile() = %+v, want %+v", got, want)
	}
}

func TestReconcileTiesBrokenByBranchName(t *testing.T) {
	branches := []BranchInfo{
		{Name: "zzz-branch", CommitsAheadOfMain: 4},
		{Name: "aaa-branch", CommitsAheadOfMain: 4},
	}

	got := Reconcile(branches, map[string]bool{})

	if got[0].Branch != "aaa-branch" || got[1].Branch != "zzz-branch" {
		t.Fatalf("Reconcile() did not break ties alphabetically: %+v", got)
	}
}

func TestReconcileEmptyInput(t *testing.T) {
	got := Reconcile(nil, map[string]bool{})
	if len(got) != 0 {
		t.Fatalf("Reconcile(nil) = %+v, want empty", got)
	}
}

func TestReconcileMissingPRHeadRefsTreatedAsNoPR(t *testing.T) {
	branches := []BranchInfo{{Name: "solo-branch", CommitsAheadOfMain: 1}}

	got := Reconcile(branches, nil)

	if len(got) != 1 || got[0].EverHadPR {
		t.Fatalf("Reconcile() with nil prHeadRefs = %+v, want EverHadPR=false", got)
	}
}

func TestSummarizeCountsNeverPRAndTotalCommits(t *testing.T) {
	reports := []Report{
		{Branch: "a", CommitsAheadOfMain: 10, EverHadPR: false},
		{Branch: "b", CommitsAheadOfMain: 5, EverHadPR: true},
		{Branch: "c", CommitsAheadOfMain: 2, EverHadPR: false},
	}

	got := Summarize(reports)

	want := Summary{
		TotalBranches:   3,
		NeverHadPR:      2,
		CommitsNeverPRd: 12,
	}
	if got != want {
		t.Fatalf("Summarize() = %+v, want %+v", got, want)
	}
}

func TestSummarizeEmptyInput(t *testing.T) {
	got := Summarize(nil)
	want := Summary{}
	if got != want {
		t.Fatalf("Summarize(nil) = %+v, want %+v", got, want)
	}
}
