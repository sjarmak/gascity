package api

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// raceAfterFirstListStore simulates a worker claiming a bead in the window
// between reassignOpenWorkAssignedToSession's List and its per-item Update: the
// first call to List returns the normal snapshot (bead still assigned to
// oldAssignee), and every List call after that reflects a concurrent claim
// that changed the assignee. This is the exact window gc-p54f8 describes: the
// List already ran, so a stale cache read cannot explain it, but the fix's own
// live re-check (a second List) must observe the change and refuse to
// overwrite it.
type raceAfterFirstListStore struct {
	*beads.MemStore
	beadID        string
	claimant      string
	targetStatus  string
	listsOfStatus int
}

func (s *raceAfterFirstListStore) List(q beads.ListQuery) ([]beads.Bead, error) {
	if q.Status == s.targetStatus {
		s.listsOfStatus++
		// The 1st List of this status is the caller's own snapshot (must see
		// the original assignee). The 2nd is the guard's live re-check inside
		// the per-item loop, which must observe the concurrent claim that
		// landed in between.
		if s.listsOfStatus == 2 {
			if err := s.Update(s.beadID, beads.UpdateOpts{Assignee: &s.claimant}); err != nil {
				return nil, err
			}
		}
	}
	return s.MemStore.List(q)
}

// TestReassignOpenWorkAssignedToSession_SkipsWriteOnConcurrentClaim pins the
// dr-huhn defect class for internal/api: a bead claimed by a fresh worker
// between the caller's List and this function's per-item Update must not have
// that claim clobbered by the retired session's replacement. Reverting the
// live re-check guard in reassignOpenWorkAssignedToSession makes this test
// fail, because the unconditional write overwrites the concurrent claimant.
func TestReassignOpenWorkAssignedToSession_SkipsWriteOnConcurrentClaim(t *testing.T) {
	t.Parallel()

	mem := beads.NewMemStore()
	work, err := mem.Create(beads.Bead{
		Title:    "in-flight work",
		Type:     "task",
		Status:   "open",
		Assignee: "retired-session",
	})
	if err != nil {
		t.Fatalf("Create(work): %v", err)
	}

	const concurrentClaimant = "fresh-worker-claimed-it"
	store := &raceAfterFirstListStore{
		MemStore:     mem,
		beadID:       work.ID,
		claimant:     concurrentClaimant,
		targetStatus: "open",
	}

	if err := reassignOpenWorkAssignedToSession(store, "retired-session", "replacement-session"); err != nil {
		t.Fatalf("reassignOpenWorkAssignedToSession: %v", err)
	}

	got, err := mem.Get(work.ID)
	if err != nil {
		t.Fatalf("Get(%s): %v", work.ID, err)
	}
	if got.Assignee != concurrentClaimant {
		t.Fatalf("Assignee = %q, want %q; the live-fresh worker's claim must not be overwritten by the retired session's replacement", got.Assignee, concurrentClaimant)
	}
}
