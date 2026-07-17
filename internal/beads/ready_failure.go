package beads

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// blockerState is the readiness-relevant projection of a bead that other beads
// depend on: whether it closed, and whether that close recorded a terminal
// failure rather than a completion. Readiness needs both dimensions, because
// "closed" alone cannot distinguish a step that finished from a step that gave
// up (gc-d58o).
type blockerState struct {
	status          string
	terminalFailure bool
}

// blockerStateOf projects a bead into the state readiness needs from a blocker.
func blockerStateOf(b Bead) blockerState {
	return blockerState{status: b.Status, terminalFailure: isTerminalFailure(b)}
}

// satisfies reports whether the blocker satisfies a ready-blocking dependency.
func (s blockerState) satisfies() bool {
	return s.status == "closed" && !s.terminalFailure
}

// isTerminalFailure reports whether a bead closed as a terminal failure. A step
// that hard-fails writes gc.outcome=fail and/or gc.failure_class=hard before
// closing; either marker alone is enough, because a worker that classifies its
// failure as hard has declared the work cannot proceed.
//
// Only closed beads qualify: an open bead carrying stale failure metadata is
// gated by its status, not by its outcome.
//
// Retry-managed attempts are exempt, and the exemption is load-bearing rather
// than a special case: retry wires retry-eval --blocks--> retry-run, so a failed
// attempt MUST keep satisfying its dependent or the eval bead never becomes
// ready and the retry loop cannot observe the failure it exists to handle
// (internal/dispatch/retry.go). Their failures are terminal for the attempt,
// not for the graph.
func isTerminalFailure(b Bead) bool {
	if b.Status != "closed" || IsRetryAttemptSubject(b) {
		return false
	}
	if strings.TrimSpace(b.Metadata[beadmeta.OutcomeMetadataKey]) == beadmeta.OutcomeFail {
		return true
	}
	return strings.TrimSpace(b.Metadata[beadmeta.FailureClassMetadataKey]) == beadmeta.FailureClassHard
}

// IsRetryAttemptSubject reports whether a bead is a retry-managed attempt whose
// contract violations the retry machinery classifies, rather than a plain step
// whose failure is the graph's business.
func IsRetryAttemptSubject(b Bead) bool {
	if b.Metadata[beadmeta.LogicalBeadIDMetadataKey] == "" {
		return false
	}
	// v1 pattern: attempt beads have gc.kind "retry-run" or "retry-eval".
	switch b.Metadata[beadmeta.KindMetadataKey] {
	case "retry-run", "retry-eval":
		return true
	}
	// v2 pattern: attempt beads keep their original kind but carry gc.attempt.
	return b.Metadata[beadmeta.AttemptMetadataKey] != ""
}

// BlockerSatisfiesReadyDependency reports whether a blocker bead satisfies the
// ready-blocking dependencies that point at it. A blocker satisfies its
// dependents only by closing successfully: a terminal failure closes the bead
// but must never unlock downstream work.
func BlockerSatisfiesReadyDependency(blocker Bead) bool {
	return blockerStateOf(blocker).satisfies()
}

// blockerReader reads the dependency edges and blocker beads that the readiness
// post-filter needs.
type blockerReader interface {
	DepList(id, direction string) ([]Dep, error)
	Get(id string) (Bead, error)
}

// batchBlockerReader is the optional bulk-dependency capability. Stores that
// implement it answer the post-filter's edge lookup in one round trip instead
// of one per ready candidate.
type batchBlockerReader interface {
	DepListBatch(ids []string) (map[string][]Dep, error)
}

// rejectTerminallyFailedBlocked drops candidates whose ready-blocking
// dependencies include a terminally failed blocker.
//
// Stores that delegate dependency evaluation to an engine which only knows bead
// status — bd's ready SQL, beadslib's GetReadyWork — need this pass: those
// engines treat every closed blocker as satisfied, so a hard-failed step would
// unlock each downstream step behind it. Stores that evaluate dependencies
// in-process gate on blockerState directly and do not call this.
//
// Blockers are read once each and memoized, so the cost is one dependency query
// plus one read per distinct blocker, not per candidate.
func rejectTerminallyFailedBlocked(r blockerReader, candidates []Bead) ([]Bead, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	ids := make([]string, 0, len(candidates))
	for _, b := range candidates {
		ids = append(ids, b.ID)
	}
	depsByID, err := readyCandidateDeps(r, ids)
	if err != nil {
		return nil, err
	}

	failed := make(map[string]bool)
	result := make([]Bead, 0, len(candidates))
	for _, b := range candidates {
		blocked := false
		for _, dep := range depsByID[b.ID] {
			if !isReadyBlockingDependencyType(dep.Type) {
				continue
			}
			hard, seen := failed[dep.DependsOnID]
			if !seen {
				hard, err = blockerTerminallyFailed(r, dep.DependsOnID, b.ID)
				if err != nil {
					return nil, err
				}
				failed[dep.DependsOnID] = hard
			}
			if hard {
				blocked = true
				break
			}
		}
		if !blocked {
			result = append(result, b)
		}
	}
	return result, nil
}

// blockerTerminallyFailed reports whether the blocker recorded a terminal
// failure. A blocker the store cannot resolve is not treated as failed: the
// underlying engine already applied its own status gate to this candidate, and
// inventing a failure would strand healthy work.
func blockerTerminallyFailed(r blockerReader, blockerID, candidateID string) (bool, error) {
	blocker, err := r.Get(blockerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("reading blocker %q of ready candidate %q: %w", blockerID, candidateID, err)
	}
	return isTerminalFailure(blocker), nil
}

func readyCandidateDeps(r blockerReader, ids []string) (map[string][]Dep, error) {
	if batch, ok := r.(batchBlockerReader); ok {
		deps, err := batch.DepListBatch(ids)
		if err != nil {
			return nil, fmt.Errorf("batch listing ready-candidate dependencies: %w", err)
		}
		return deps, nil
	}
	deps := make(map[string][]Dep, len(ids))
	for _, id := range ids {
		got, err := r.DepList(id, "down")
		if err != nil {
			return nil, fmt.Errorf("listing dependencies of ready candidate %q: %w", id, err)
		}
		deps[id] = got
	}
	return deps, nil
}
