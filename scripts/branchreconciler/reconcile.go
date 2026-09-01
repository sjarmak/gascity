// Package branchreconciler reports, per remote branch, how many commits it
// carries that are not in main and whether it ever appeared as a pull
// request head ref. It states structural facts only: which branch to delete,
// rebase, or open a PR for is a disposition call left to a human or agent.
package branchreconciler

import "sort"

// BranchInfo is one remote branch and how far it has diverged from main.
type BranchInfo struct {
	Name               string
	CommitsAheadOfMain int
}

// Report is the reconciled structural fact for one branch: how many commits
// it carries that main does not have, and whether any pull request was ever
// opened with this branch as its head ref.
type Report struct {
	Branch             string
	CommitsAheadOfMain int
	EverHadPR          bool
}

// Summary aggregates a set of Reports into totals.
type Summary struct {
	TotalBranches   int
	NeverHadPR      int
	CommitsNeverPRd int
}

// Reconcile joins branches against prHeadRefs (the set of branch names that
// ever appeared as a pull request head ref) and returns one Report per
// branch, sorted so branches that never had a PR sort first, ordered within
// that split by descending commit count, and by name on ties. The sort is
// presentation only: it reorders the same facts, it does not filter,
// threshold, or label any branch for disposition.
func Reconcile(branches []BranchInfo, prHeadRefs map[string]bool) []Report {
	reports := make([]Report, 0, len(branches))
	for _, b := range branches {
		reports = append(reports, Report{
			Branch:             b.Name,
			CommitsAheadOfMain: b.CommitsAheadOfMain,
			EverHadPR:          prHeadRefs[b.Name],
		})
	}
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].EverHadPR != reports[j].EverHadPR {
			return !reports[i].EverHadPR
		}
		if reports[i].CommitsAheadOfMain != reports[j].CommitsAheadOfMain {
			return reports[i].CommitsAheadOfMain > reports[j].CommitsAheadOfMain
		}
		return reports[i].Branch < reports[j].Branch
	})
	return reports
}

// Summarize aggregates reports into totals: how many branches exist, how
// many never appeared as a PR head ref, and how many commits those
// never-PR'd branches carry combined.
func Summarize(reports []Report) Summary {
	var s Summary
	for _, r := range reports {
		s.TotalBranches++
		if !r.EverHadPR {
			s.NeverHadPR++
			s.CommitsNeverPRd += r.CommitsAheadOfMain
		}
	}
	return s
}
