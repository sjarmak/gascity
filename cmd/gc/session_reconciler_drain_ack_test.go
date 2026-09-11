package main

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
)

// drainAckSessionID is the one session these characterization tests operate on.
const drainAckSessionID = "gc-sess-1"

// drainAckPatchFailStore fails exactly the SetMetadataBatch that ApplyPatch routes
// through, so markDrainAckStopPending takes its persist-error branch without any
// other store call changing behavior.
type drainAckPatchFailStore struct {
	beads.Store
	err error
}

func (s *drainAckPatchFailStore) SetMetadataBatch(_ string, _ map[string]string) error {
	return s.err
}

func drainAckCharacterizationStore(t *testing.T) (*sessionpkg.Store, *beads.MemStore) {
	t.Helper()
	bead := beads.Bead{
		ID:     drainAckSessionID,
		Status: "open",
		Title:  "session",
		Labels: []string{sessionpkg.LabelSession},
		Metadata: map[string]string{
			"state":        string(sessionpkg.StateActive),
			"session_name": "worker-1",
		},
	}
	mem := beads.NewMemStoreFrom(1, []beads.Bead{bead}, nil)
	return sessionpkg.NewStore(beads.SessionStore{Store: mem}), mem
}

// TestMarkDrainAckStopPendingCharacterization locks the write-returns-Info
// contract of the drain-ack stop-pending transition before the state machine is
// extracted to a sibling file: the returned Info is the caller's snapshot with
// the SAME patch folded on, so a caller may assign it directly, and every refusal
// path returns the INPUT Info unchanged with ok=false so the caller skips the fold.
func TestMarkDrainAckStopPendingCharacterization(t *testing.T) {
	now := time.Date(2026, 9, 11, 8, 0, 0, 0, time.UTC)
	clk := &clock.Fake{Time: now}

	t.Run("persists and folds the same patch onto the caller Info", func(t *testing.T) {
		front, mem := drainAckCharacterizationStore(t)
		in := sessionpkg.Info{ID: drainAckSessionID, MetadataState: string(sessionpkg.StateActive), SessionNameMetadata: "worker-1"}
		var stderr bytes.Buffer

		got, ok := markDrainAckStopPending(in, front, clk, &stderr)
		if !ok {
			t.Fatalf("expected ok on a healthy store, stderr=%q", stderr.String())
		}
		// The folded Info is what the caller assigns, so it carries the transition.
		if strings.TrimSpace(got.MetadataState) != string(sessionpkg.StateDraining) {
			t.Errorf("folded MetadataState = %q, want %q", got.MetadataState, sessionpkg.StateDraining)
		}
		if strings.TrimSpace(got.StateReason) != sessionpkg.DrainAckStopPendingReason {
			t.Errorf("folded StateReason = %q, want %q", got.StateReason, sessionpkg.DrainAckStopPendingReason)
		}
		// isDrainAckStopPendingInfo is the predicate the reconciler re-reads, so the
		// fold must satisfy it; this is the pairing that makes the fold usable.
		if !isDrainAckStopPendingInfo(got) {
			t.Errorf("folded Info is not recognized as drain-ack stop-pending: state=%q reason=%q", got.MetadataState, got.StateReason)
		}
		// The SAME patch must have reached the store, not just the in-memory fold.
		b, err := mem.Get(drainAckSessionID)
		if err != nil {
			t.Fatalf("reading back the session bead: %v", err)
		}
		if strings.TrimSpace(b.Metadata["state"]) != string(sessionpkg.StateDraining) {
			t.Errorf("persisted state = %q, want %q", b.Metadata["state"], sessionpkg.StateDraining)
		}
		if strings.TrimSpace(b.Metadata["state_reason"]) != sessionpkg.DrainAckStopPendingReason {
			t.Errorf("persisted state_reason = %q, want %q", b.Metadata["state_reason"], sessionpkg.DrainAckStopPendingReason)
		}
		// DrainAckStopPendingPatch clears the pending-create keys; a stale claim left
		// behind would make the next tick read the session as still creating.
		if v := strings.TrimSpace(b.Metadata["pending_create_claim"]); v != "" {
			t.Errorf("persisted pending_create_claim = %q, want cleared", v)
		}
		if stderr.Len() != 0 {
			t.Errorf("unexpected stderr on the success path: %q", stderr.String())
		}
	})

	t.Run("a persist error returns the input unchanged and names the session", func(t *testing.T) {
		_, mem := drainAckCharacterizationStore(t)
		front := sessionpkg.NewStore(beads.SessionStore{
			Store: &drainAckPatchFailStore{Store: mem, err: errors.New("metadata write failed")},
		})
		in := sessionpkg.Info{ID: drainAckSessionID, MetadataState: string(sessionpkg.StateActive), SessionNameMetadata: "worker-1"}
		var stderr bytes.Buffer

		got, ok := markDrainAckStopPending(in, front, clk, &stderr)
		if ok {
			t.Fatal("expected ok=false when the persist fails")
		}
		if !reflect.DeepEqual(got, in) {
			t.Errorf("input Info must come back unchanged on a persist error: got %+v want %+v", got, in)
		}
		// The diagnostic must name the session, or an operator cannot tell which one failed.
		if !strings.Contains(stderr.String(), "worker-1") {
			t.Errorf("stderr does not name the session: %q", stderr.String())
		}
	})

	t.Run("refusals return the input unchanged without touching the store", func(t *testing.T) {
		front, _ := drainAckCharacterizationStore(t)
		for _, tc := range []struct {
			name  string
			info  sessionpkg.Info
			front *sessionpkg.Store
		}{
			{"empty id", sessionpkg.Info{MetadataState: string(sessionpkg.StateActive)}, front},
			{"nil front door", sessionpkg.Info{ID: drainAckSessionID, MetadataState: string(sessionpkg.StateActive)}, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var stderr bytes.Buffer
				got, ok := markDrainAckStopPending(tc.info, tc.front, clk, &stderr)
				if ok {
					t.Fatal("expected ok=false")
				}
				if !reflect.DeepEqual(got, tc.info) {
					t.Errorf("Info mutated on a refusal: got %+v want %+v", got, tc.info)
				}
			})
		}
	})

	t.Run("a nil stderr does not panic", func(t *testing.T) {
		_, mem := drainAckCharacterizationStore(t)
		front := sessionpkg.NewStore(beads.SessionStore{
			Store: &drainAckPatchFailStore{Store: mem, err: errors.New("metadata write failed")},
		})
		in := sessionpkg.Info{ID: drainAckSessionID, SessionNameMetadata: "worker-1"}
		if _, ok := markDrainAckStopPending(in, front, clk, nil); ok {
			t.Fatal("expected ok=false")
		}
	})
}

// TestClearDrainTrackerForStopPendingCharacterization locks that the stop-pending
// clear drops BOTH tracker entries for the session and nothing else. A session
// parked in stop-pending keeps no drain state and no in-flight idle probe, so a
// surviving entry would let the next tick act on a drain that is already resolved.
func TestClearDrainTrackerForStopPendingCharacterization(t *testing.T) {
	const target = "gc-sess-target"
	const other = "gc-sess-other"

	seed := func(t *testing.T) *drainTracker {
		t.Helper()
		dt := newDrainTracker()
		for _, id := range []string{target, other} {
			dt.set(id, &drainState{})
			dt.startIdleProbe(id)
		}
		return dt
	}

	t.Run("clears both the drain entry and the idle probe for the target", func(t *testing.T) {
		dt := seed(t)
		clearDrainTrackerForStopPending(target, dt)

		if ds := dt.get(target); ds != nil {
			t.Errorf("drain entry survived the clear: %+v", ds)
		}
		if _, ok := dt.idleProbe(target); ok {
			t.Error("idle probe survived the clear")
		}
		// Another session's state is untouched: the clear is per-session, not a reset.
		if ds := dt.get(other); ds == nil {
			t.Error("clearing one session dropped another session's drain entry")
		}
		if _, ok := dt.idleProbe(other); !ok {
			t.Error("clearing one session dropped another session's idle probe")
		}
	})

	t.Run("an empty id or nil tracker is a no-op", func(t *testing.T) {
		dt := seed(t)
		clearDrainTrackerForStopPending("", dt)
		if ds := dt.get(target); ds == nil {
			t.Error("an empty id must not clear anything")
		}
		if _, ok := dt.idleProbe(target); !ok {
			t.Error("an empty id must not clear an idle probe")
		}
		clearDrainTrackerForStopPending(target, nil) // must not panic
	})
}
