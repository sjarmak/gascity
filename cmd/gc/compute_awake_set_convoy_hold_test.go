package main

import (
	"testing"
	"time"
)

// Step-boundary shape: a pool seat that has just closed one step of a molecule
// whose root is still open, with the next step not yet claimed.
//
// Neither step is visible as assigned demand at that instant — the closed one
// is terminal, the next one is open and UNASSIGNED — so AwakeInput.WorkBeads is
// empty and every claim-derived wake reason ("assigned-work", "scaled:demand")
// is absent. The seat's durable currently_processing_bead_id still names the
// step it just finished, and that step's convoy root is unfinished.
func convoyBoundaryInput(idleSince time.Time, holds bool) AwakeInput {
	const (
		template    = "gascity/polecat"
		sessionName = "gascity-polecat-1"
	)
	return AwakeInput{
		Agents: []AwakeAgent{{
			QualifiedName:  template,
			SleepAfterIdle: 10 * time.Minute,
		}},
		SessionBeads: []AwakeSessionBead{{
			ID:          "gc-seat1",
			SessionName: sessionName,
			Template:    template,
			State:       "active",
			// The step this seat just closed. Never cleared on close, so it
			// survives the boundary as the seat's durable convoy anchor.
			CurrentlyProcessingBeadID: "gc-step-closed",
			HoldsUnfinishedConvoy:     holds,
			IdleSince:                 idleSince,
		}},
		// No assigned work at the boundary, and no scale demand.
		WorkBeads:        nil,
		ScaleCheckCounts: map[string]int{template: 0},
		Now:              time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
	}
}

// TestConvoyHoldWakesSeatAtStepBoundary pins the wake reason that keeps a seat
// from looking idle between two steps of an unfinished convoy.
//
// Without it the seat presents as demand-free for the window between "step
// closed" and "next step claimed". A drain verdict computed inside that window
// churns the seat: the convoy's remaining work is requeued and re-picked by a
// different seat, so the run loses its worktree continuity and repeats setup.
// Making the hold a wake reason in its own right — independent of whether any
// step is currently claimed — closes the window instead of serializing the
// drain against the claim.
func TestConvoyHoldWakesSeatAtStepBoundary(t *testing.T) {
	const sessionName = "gascity-polecat-1"

	t.Run("holds_unfinished_convoy_wakes", func(t *testing.T) {
		d := ComputeAwakeSet(convoyBoundaryInput(time.Time{}, true))[sessionName]
		if !d.ShouldWake {
			t.Fatalf("seat holding an unfinished convoy must wake at a step boundary; got ShouldWake=false reason=%q", d.Reason)
		}
		if d.Reason != "convoy-hold" {
			t.Fatalf("Reason = %q, want %q", d.Reason, "convoy-hold")
		}
	})

	// The whole point is retention ACROSS the boundary: a seat that has been
	// detached long enough to have a stale idle reference must not be put back
	// to sleep by the idle pass while it still holds the convoy. Without the
	// exemption the wake reason is computed and then immediately canceled,
	// which looks correct in the desired set and changes nothing downstream.
	t.Run("stale_idle_reference_does_not_cancel_the_hold", func(t *testing.T) {
		stale := time.Date(2026, 8, 6, 11, 0, 0, 0, time.UTC) // 1h before Now, timeout 10m
		d := ComputeAwakeSet(convoyBoundaryInput(stale, true))[sessionName]
		if !d.ShouldWake {
			t.Fatalf("convoy-hold wake was canceled by idle-sleep (final reason=%q); "+
				"the hold must be exempt from idle suppression or it cannot retain across the boundary", d.Reason)
		}
	})

	// Negative control: the hold is the only thing keeping this seat awake, so
	// clearing it must let the seat sleep exactly as it does today. This is what
	// makes the positive cases above meaningful rather than vacuous.
	t.Run("no_hold_leaves_seat_asleep", func(t *testing.T) {
		d := ComputeAwakeSet(convoyBoundaryInput(time.Time{}, false))[sessionName]
		if d.ShouldWake {
			t.Fatalf("seat with no convoy hold and no demand must not wake; got reason=%q", d.Reason)
		}
	})
}

// TestConvoyHoldRespectsHardBlockers pins that the new wake reason is an
// ordinary demand signal, not an override: hold and quarantine still win.
// A convoy hold that outranked them would turn every parked seat into an
// unkillable one.
func TestConvoyHoldRespectsHardBlockers(t *testing.T) {
	const sessionName = "gascity-polecat-1"
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	t.Run("held", func(t *testing.T) {
		in := convoyBoundaryInput(time.Time{}, true)
		in.SessionBeads[0].HeldUntil = now.Add(time.Hour)
		if d := ComputeAwakeSet(in)[sessionName]; d.ShouldWake {
			t.Fatalf("held seat must stay asleep, got reason=%q", d.Reason)
		}
	})

	t.Run("quarantined", func(t *testing.T) {
		in := convoyBoundaryInput(time.Time{}, true)
		in.SessionBeads[0].QuarantinedUntil = now.Add(time.Hour)
		if d := ComputeAwakeSet(in)[sessionName]; d.ShouldWake {
			t.Fatalf("quarantined seat must stay asleep, got reason=%q", d.Reason)
		}
	})

	t.Run("suspended_agent", func(t *testing.T) {
		in := convoyBoundaryInput(time.Time{}, true)
		in.Agents[0].Suspended = true
		if d := ComputeAwakeSet(in)[sessionName]; d.ShouldWake {
			t.Fatalf("seat on a suspended agent must stay asleep, got reason=%q", d.Reason)
		}
	})

	t.Run("wait_hold", func(t *testing.T) {
		in := convoyBoundaryInput(time.Time{}, true)
		in.SessionBeads[0].WaitHold = true
		if d := ComputeAwakeSet(in)[sessionName]; d.ShouldWake {
			t.Fatalf("seat parked on a user wait must stay asleep, got reason=%q", d.Reason)
		}
	})

	t.Run("drained", func(t *testing.T) {
		in := convoyBoundaryInput(time.Time{}, true)
		in.SessionBeads[0].Drained = true
		in.SessionBeads[0].State = "drained"
		if d := ComputeAwakeSet(in)[sessionName]; d.ShouldWake {
			t.Fatalf("drained seat must stay asleep, got reason=%q", d.Reason)
		}
	})
}

// TestConvoyHoldDoesNotDisplaceAssignedWork pins that a seat which DOES have a
// claimed step keeps reporting "assigned-work" (and its anchor bead), so the
// reconciler's currently_processing_bead_id bookkeeping and wake_mode=fresh
// cycling are unaffected by the new reason.
func TestConvoyHoldDoesNotDisplaceAssignedWork(t *testing.T) {
	const sessionName = "gascity-polecat-1"
	in := convoyBoundaryInput(time.Time{}, true)
	in.WorkBeads = []AwakeWorkBead{{
		ID: "gc-step-next", Assignee: sessionName, Status: "in_progress",
	}}

	d := ComputeAwakeSet(in)[sessionName]
	if !d.ShouldWake {
		t.Fatalf("want wake, got ShouldWake=false reason=%q", d.Reason)
	}
	if d.Reason != "assigned-work" {
		t.Fatalf("Reason = %q, want assigned-work: a claimed step must still win the reason", d.Reason)
	}
	if d.AssignedWorkBeadID != "gc-step-next" {
		t.Fatalf("AssignedWorkBeadID = %q, want gc-step-next", d.AssignedWorkBeadID)
	}
	if !d.HasAssignedWork {
		t.Fatal("HasAssignedWork = false, want true")
	}
}
