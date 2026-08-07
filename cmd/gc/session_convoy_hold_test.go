package main

import (
	"io"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// convoyHoldFixture builds the step-boundary shape in a store: a molecule root
// with the status under test, the step the seat just closed, and the next step
// which is open and NOT yet assigned to anyone.
func convoyHoldFixture(rootStatus string) beads.Store {
	root := beads.Bead{
		ID: "gc-root", Type: "molecule", Status: rootStatus,
		Metadata: map[string]string{beadmeta.KindMetadataKey: "workflow"},
	}
	closedStep := beads.Bead{
		ID: "gc-step-closed", Type: "step", Status: "closed",
		Metadata: map[string]string{beadmeta.RootBeadIDMetadataKey: "gc-root"},
	}
	nextStep := beads.Bead{
		ID: "gc-step-next", Type: "step", Status: "open",
		Metadata: map[string]string{beadmeta.RootBeadIDMetadataKey: "gc-root"},
	}
	return beads.NewMemStoreFrom(0, []beads.Bead{root, closedStep, nextStep}, nil)
}

func convoyHoldSession(anchor string) sessionpkg.Info {
	return sessionpkg.Info{
		ID:                        "gc-seat1",
		SessionNameMetadata:       "gascity-polecat-1",
		CurrentlyProcessingBeadID: anchor,
	}
}

// TestUnfinishedConvoyHoldsAtStepBoundary is the store-side half of the fix.
// The pure decision function can only act on a fact somebody supplies; if this
// resolver never reports the hold, the wake reason is unreachable in production
// and the change is inert.
func TestUnfinishedConvoyHoldsAtStepBoundary(t *testing.T) {
	cfg := &config.City{}

	t.Run("open_root_holds", func(t *testing.T) {
		store := convoyHoldFixture("in_progress")
		got := unfinishedConvoyHolds("", cfg, store, nil,
			[]sessionpkg.Info{convoyHoldSession("gc-step-closed")}, io.Discard)
		if !got["gc-seat1"] {
			t.Fatalf("seat anchored on a closed step of an unfinished root must hold; got %#v", got)
		}
	})

	t.Run("closed_root_does_not_hold", func(t *testing.T) {
		store := convoyHoldFixture("closed")
		got := unfinishedConvoyHolds("", cfg, store, nil,
			[]sessionpkg.Info{convoyHoldSession("gc-step-closed")}, io.Discard)
		if got["gc-seat1"] {
			t.Fatal("a finished convoy must not hold the seat awake")
		}
	})

	t.Run("tombstoned_root_does_not_hold", func(t *testing.T) {
		store := convoyHoldFixture("tombstone")
		got := unfinishedConvoyHolds("", cfg, store, nil,
			[]sessionpkg.Info{convoyHoldSession("gc-step-closed")}, io.Discard)
		if got["gc-seat1"] {
			t.Fatal("a tombstoned convoy is terminal and must not hold the seat awake")
		}
	})

	t.Run("no_anchor_does_not_hold", func(t *testing.T) {
		store := convoyHoldFixture("in_progress")
		got := unfinishedConvoyHolds("", cfg, store, nil,
			[]sessionpkg.Info{convoyHoldSession("")}, io.Discard)
		if got["gc-seat1"] {
			t.Fatal("a seat that never recorded a current bead holds nothing")
		}
	})

	t.Run("standalone_task_anchor_does_not_hold", func(t *testing.T) {
		// A bead with no gc.root_bead_id is not part of a convoy. Treating it
		// as one would make every seat that ever claimed an ordinary task
		// unsleepable.
		loose := beads.Bead{ID: "gc-loose", Type: "task", Status: "closed"}
		store := beads.NewMemStoreFrom(0, []beads.Bead{loose}, nil)
		got := unfinishedConvoyHolds("", cfg, store, nil,
			[]sessionpkg.Info{convoyHoldSession("gc-loose")}, io.Discard)
		if got["gc-seat1"] {
			t.Fatal("a standalone task anchor must not register as a convoy hold")
		}
	})

	t.Run("root_anchor_holds_directly", func(t *testing.T) {
		// A seat anchored on the root bead itself (rather than a step) is
		// holding the same convoy.
		store := convoyHoldFixture("in_progress")
		got := unfinishedConvoyHolds("", cfg, store, nil,
			[]sessionpkg.Info{convoyHoldSession("gc-root")}, io.Discard)
		if !got["gc-seat1"] {
			t.Fatal("a seat anchored directly on an unfinished root must hold")
		}
	})

	t.Run("closed_session_is_skipped", func(t *testing.T) {
		store := convoyHoldFixture("in_progress")
		info := convoyHoldSession("gc-step-closed")
		info.Closed = true
		got := unfinishedConvoyHolds("", cfg, store, nil, []sessionpkg.Info{info}, io.Discard)
		if got["gc-seat1"] {
			t.Fatal("a closed session bead must not register a hold")
		}
	})

	t.Run("missing_anchor_bead_does_not_hold", func(t *testing.T) {
		// Fail open: an unreadable or purged anchor leaves behavior exactly as
		// it is today rather than pinning a seat awake on absent evidence.
		store := convoyHoldFixture("in_progress")
		got := unfinishedConvoyHolds("", cfg, store, nil,
			[]sessionpkg.Info{convoyHoldSession("gc-gone")}, io.Discard)
		if got["gc-seat1"] {
			t.Fatal("an unresolvable anchor must not manufacture a hold")
		}
	})
}

// TestUnfinishedConvoyHoldsSharesRootVerdict pins that N seats on one convoy
// cost one root read, not N. The resolver runs on every reconciler tick, so an
// unmemoized version would multiply store reads by pool size.
func TestUnfinishedConvoyHoldsSharesRootVerdict(t *testing.T) {
	store := &countingGetStore{Store: convoyHoldFixture("in_progress")}
	infos := []sessionpkg.Info{
		{ID: "gc-seat1", SessionNameMetadata: "p1", CurrentlyProcessingBeadID: "gc-step-closed"},
		{ID: "gc-seat2", SessionNameMetadata: "p2", CurrentlyProcessingBeadID: "gc-step-next"},
	}
	got := unfinishedConvoyHolds("", &config.City{}, store, nil, infos, io.Discard)
	if !got["gc-seat1"] || !got["gc-seat2"] {
		t.Fatalf("both seats on the unfinished convoy must hold; got %#v", got)
	}
	if store.gets["gc-root"] != 1 {
		t.Fatalf("root read %d times, want 1 (verdict must be memoized per root)", store.gets["gc-root"])
	}
}

type countingGetStore struct {
	beads.Store
	gets map[string]int
}

func (c *countingGetStore) Get(id string) (beads.Bead, error) {
	if c.gets == nil {
		c.gets = map[string]int{}
	}
	c.gets[id]++
	return c.Store.Get(id)
}
