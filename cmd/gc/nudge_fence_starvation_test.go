package main

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// starvationSessionName is the shared runtime session_name used to model a
// continued/recycled session: several open beads carry it while an obsolete
// twin has not yet closed.
const starvationSessionName = "sess-worker"

// openSessionBead creates an OPEN session bead carrying starvationSessionName and
// the given continuation_epoch, returning its store-assigned ID. Two beads
// sharing the name model the transient window across a continuation-epoch bump
// or a pool recycle, where an obsolete session bead has not yet closed.
func openSessionBead(t *testing.T, store beads.NudgesStore, epoch string) string {
	t.Helper()
	bead, err := store.Create(beads.Bead{
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name":       starvationSessionName,
			"continuation_epoch": epoch,
			"state":              "active",
		},
	})
	if err != nil {
		t.Fatalf("create session bead (epoch %s): %v", epoch, err)
	}
	return bead.ID
}

// TestWithNudgeTargetFence_PrefersCurrentSessionAmongDuplicates pins the
// root-cause fix: when several OPEN session beads share one runtime session_name
// (a continued/recycled session whose obsolete bead has not yet closed),
// delivery must fence on the CURRENT session identity — the highest continuation
// epoch — not on whichever bead the (created_at, id) scan returns first. The
// obsolete bead is created first, so the pre-fix code adopted its stale fence.
func TestWithNudgeTargetFence_PrefersCurrentSessionAmongDuplicates(t *testing.T) {
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	// Obsolete session bead created first: lowest (created_at, id) → first in scan.
	obsolete := openSessionBead(t, store, "1")
	current := openSessionBead(t, store, "2")

	target := withNudgeTargetFence(store.Store, nudgeTarget{sessionName: starvationSessionName})

	if target.sessionID != current {
		t.Fatalf("sessionID = %q, want current session %q (not obsolete %q)", target.sessionID, current, obsolete)
	}
	if target.continuationEpoch != "2" {
		t.Fatalf("continuationEpoch = %q, want 2 (current)", target.continuationEpoch)
	}
}

// TestWithNudgeTargetFence_KnownSessionIDFillsOwnEpoch guards the caller-knows-ID
// path: when the target already carries a session ID, the epoch must be filled
// from THAT exact session's bead, never from a newer same-name sibling. Filling
// the wrong epoch would fence delivery on a fabricated identity.
func TestWithNudgeTargetFence_KnownSessionIDFillsOwnEpoch(t *testing.T) {
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	obsolete := openSessionBead(t, store, "1")
	_ = openSessionBead(t, store, "2")

	target := withNudgeTargetFence(store.Store, nudgeTarget{
		sessionName: starvationSessionName,
		sessionID:   obsolete,
	})

	if target.sessionID != obsolete {
		t.Fatalf("sessionID = %q, want unchanged %q", target.sessionID, obsolete)
	}
	if target.continuationEpoch != "1" {
		t.Fatalf("continuationEpoch = %q, want 1 (the known session's own epoch)", target.continuationEpoch)
	}
}

// TestNudgeDeliveryTargetsCurrentFenceForContinuedSession reproduces the
// starvation: a P0 nudge carrying the CURRENT session fence must be claimed for
// delivery even while stale lower-priority nudges bearing the OBSOLETE fence sit
// ahead of it in the queue. Pre-fix, target resolution adopted the obsolete
// fence, so the P0 (current fence) was not claimable and the live pane stayed
// idle — routed/assigned state falsely implying execution.
func TestNudgeDeliveryTargetsCurrentFenceForContinuedSession(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	obsolete := openSessionBead(t, store, "1")
	current := openSessionBead(t, store, "2")

	past := time.Now().Add(-time.Minute)
	// Two stale mechanic nudges bearing the obsolete fence, enqueued first.
	stale := []queuedNudge{
		newQueuedNudgeWithOptions("worker", "mechanic one", "mechanic", past, queuedNudgeOptions{
			ID: "n-stale-1", SessionID: obsolete, ContinuationEpoch: "1",
		}),
		newQueuedNudgeWithOptions("worker", "mechanic two", "mechanic", past, queuedNudgeOptions{
			ID: "n-stale-2", SessionID: obsolete, ContinuationEpoch: "1",
		}),
	}
	// The P0 canary nudge bearing the current fence, enqueued last.
	p0 := newQueuedNudgeWithOptions("worker", "P0 canary", "canary", past, queuedNudgeOptions{
		ID: "n-p0", SessionID: current, ContinuationEpoch: "2",
	})
	for _, item := range append(append([]queuedNudge{}, stale...), p0) {
		if err := enqueueQueuedNudge(dir, item); err != nil {
			t.Fatalf("enqueueQueuedNudge(%s): %v", item.ID, err)
		}
	}

	target := withNudgeTargetFence(store.Store, nudgeTarget{
		agent:       config.Agent{Name: "worker"},
		sessionName: starvationSessionName,
	})

	claimed, err := claimDueQueuedNudgesForTarget(dir, target, time.Now())
	if err != nil {
		t.Fatalf("claimDueQueuedNudgesForTarget: %v", err)
	}
	deliverable, _ := splitQueuedNudgesForTarget(target, claimed)

	if ids := queuedNudgeIDs(deliverable); len(ids) != 1 || ids[0] != "n-p0" {
		t.Fatalf("deliverable IDs = %#v, want [n-p0] (current-fence P0 delivered, stale left behind)", ids)
	}
	// The obsolete-fence mechanic nudges must not be delivered to the current session.
	for _, item := range deliverable {
		if item.SessionID == obsolete {
			t.Fatalf("delivered obsolete-fence nudge %q to current session", item.ID)
		}
	}
}

// TestClaimDueQueuedNudges_ExpiredAndDeadDoNotBlockReadyDelivery encodes the
// "dead or expired entries cannot block ready delivery" criterion: an expired
// entry and an already-dead entry ahead of a due, current-fence nudge must not
// prevent that nudge from being claimed.
func TestClaimDueQueuedNudges_ExpiredAndDeadDoNotBlockReadyDelivery(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	now := time.Now()
	past := now.Add(-time.Minute)

	// Expired entry (ExpiresAt in the past) enqueued ahead of the ready nudge.
	expired := queuedNudge{
		ID: "n-expired", Agent: "worker", Source: "mechanic",
		Message: "long expired", CreatedAt: past, DeliverAfter: past,
		ExpiresAt: now.Add(-time.Hour),
	}
	ready := newQueuedNudgeWithOptions("worker", "ready work", "canary", past, queuedNudgeOptions{
		ID: "n-ready",
	})
	for _, item := range []queuedNudge{expired, ready} {
		if err := enqueueQueuedNudge(dir, item); err != nil {
			t.Fatalf("enqueueQueuedNudge(%s): %v", item.ID, err)
		}
	}

	target := nudgeTarget{agent: config.Agent{Name: "worker"}}
	claimed, err := claimDueQueuedNudgesForTarget(dir, target, now)
	if err != nil {
		t.Fatalf("claimDueQueuedNudgesForTarget: %v", err)
	}
	if ids := queuedNudgeIDs(claimed); len(ids) != 1 || ids[0] != "n-ready" {
		t.Fatalf("claimed IDs = %#v, want [n-ready] (expired entry pruned, not blocking)", ids)
	}

	_, _, dead, err := listQueuedNudges(dir, "worker", now)
	if err != nil {
		t.Fatalf("listQueuedNudges: %v", err)
	}
	if len(dead) != 1 || dead[0].ID != "n-expired" {
		t.Fatalf("dead = %#v, want expired entry dead-lettered", dead)
	}
}
