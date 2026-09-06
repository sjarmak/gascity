package beads

import (
	"fmt"
	"strings"
)

// LiveWorkAssignmentAssigneeMatches reports whether a WORK bead still carries
// the (status, assignee) pair a caller's earlier snapshot recorded. It is the
// single implementation of the pre-write staleness check shared by every
// caller that lists work by an old assignee and then writes a new one: a
// concurrent claim between the List and the Update must not be clobbered.
//
// It uses a LIVE list query, not Get, and that choice is load-bearing:
// CachingStore.Get serves a clone straight from the in-memory cache for a bead
// that is tracked and not dirty (internal/beads/caching_store_reads.go). A
// cached read here would re-verify the caller's stale snapshot against an
// equally stale cache and confirm it, which reproduces the exact clobber this
// guard exists to prevent.
//
// expectedStatus must be the status the caller observed: if the bead has
// since transitioned (a concurrent claim moved open→in_progress, or another
// release moved in_progress→open) the snapshot's decision is no longer safe.
// A bead absent from the live result no longer holds that status, so it is
// not current and not an error.
//
// A read failure returns the error rather than a verdict. Writing on an
// unverified snapshot can destroy a live worker's claim, and reporting the
// write as done when the read failed would let a caller overwrite work that
// is still assigned to someone else.
func LiveWorkAssignmentAssigneeMatches(store Store, id, expectedStatus, expectedAssignee string) (bool, error) {
	id = strings.TrimSpace(id)
	expectedStatus = strings.TrimSpace(expectedStatus)
	if store == nil || id == "" || expectedStatus == "" {
		return false, nil
	}
	work, err := store.List(ListQuery{
		Status:   expectedStatus,
		Live:     true,
		TierMode: TierBoth,
	})
	if err != nil {
		return false, fmt.Errorf("live work-assignment verification of %q: %w", id, err)
	}
	for _, wb := range work {
		if wb.ID != id {
			continue
		}
		return strings.TrimSpace(wb.Assignee) == strings.TrimSpace(expectedAssignee), nil
	}
	return false, nil
}
