package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/session"
)

func TestClassifyNudgeDelivery(t *testing.T) {
	now := time.Date(2026, 7, 31, 2, 0, 0, 0, time.UTC)
	deadline := 90 * time.Second
	delivered := now.Add(-30 * time.Second)
	deliveredStale := now.Add(-5 * time.Minute)

	cases := []struct {
		name string
		obs  nudgeDeliveryObservation
		want nudgeDeliveryState
	}{
		{
			name: "no route is unrouted",
			obs:  nudgeDeliveryObservation{Routed: false},
			want: nudgeDeliveryUnrouted,
		},
		{
			name: "routed but never delivered is queued",
			obs:  nudgeDeliveryObservation{Routed: true},
			want: nudgeDeliveryQueued,
		},
		{
			// The load-bearing invariant: routing metadata alone must never read
			// as execution. Delivered, live process, but no forward activity yet.
			name: "routed and delivered within deadline, no forward activity is awaiting_ack",
			obs: nudgeDeliveryObservation{
				Routed:      true,
				DeliveredAt: delivered,
				LastActive:  delivered.Add(-time.Minute), // activity predates delivery
				Running:     true,
			},
			want: nudgeDeliveryAwaitingAck,
		},
		{
			name: "delivered and live activity advanced past delivery is acknowledged",
			obs: nudgeDeliveryObservation{
				Routed:      true,
				DeliveredAt: delivered,
				LastActive:  delivered.Add(10 * time.Second),
				Running:     true,
			},
			want: nudgeDeliveryAcknowledged,
		},
		{
			name: "delivered, no forward activity, past deadline is stalled",
			obs: nudgeDeliveryObservation{
				Routed:      true,
				DeliveredAt: deliveredStale,
				LastActive:  deliveredStale.Add(-time.Minute),
				Running:     true,
			},
			want: nudgeDeliveryStalled,
		},
		{
			// Forward activity but process no longer running: not live execution,
			// so it must not read as acknowledged. Past deadline → stalled.
			name: "forward activity but not running, past deadline is stalled",
			obs: nudgeDeliveryObservation{
				Routed:      true,
				DeliveredAt: deliveredStale,
				LastActive:  deliveredStale.Add(10 * time.Second),
				Running:     false,
			},
			want: nudgeDeliveryStalled,
		},
		{
			name: "delivered exactly at deadline boundary is still awaiting_ack",
			obs: nudgeDeliveryObservation{
				Routed:      true,
				DeliveredAt: now.Add(-deadline), // now - deliveredAt == deadline, not >
				LastActive:  now.Add(-2 * deadline),
				Running:     true,
			},
			want: nudgeDeliveryAwaitingAck,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyNudgeDelivery(tc.obs, now, deadline)
			if got != tc.want {
				t.Fatalf("classifyNudgeDelivery = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNudgeDeliveryStateIsActiveExecution pins the never-routed-only-as-active
// contract: only an acknowledged (activity-confirmed) delivery counts as
// genuine execution.
func TestNudgeDeliveryStateIsActiveExecution(t *testing.T) {
	active := map[nudgeDeliveryState]bool{
		nudgeDeliveryUnrouted:     false,
		nudgeDeliveryQueued:       false,
		nudgeDeliveryAwaitingAck:  false,
		nudgeDeliveryStalled:      false,
		nudgeDeliveryAcknowledged: true,
	}
	for state, want := range active {
		if got := state.isActiveExecution(); got != want {
			t.Fatalf("state %q isActiveExecution = %v, want %v", state, got, want)
		}
	}
}

// TestNudgeDeliveryObservationFromInfo_EndToEnd exercises the pure-assembly path
// a status/health surface uses: routed + delivered + forward activity on a live
// session classifies as acknowledged, while routed + delivered on a live session
// with stale activity past the deadline classifies as stalled — never active.
func TestNudgeDeliveryObservationFromInfo_EndToEnd(t *testing.T) {
	now := time.Date(2026, 7, 31, 2, 0, 0, 0, time.UTC)
	deliveredAt := now.Add(-30 * time.Second)

	ackObs := nudgeDeliveryObservationFromInfo(true, session.Info{
		State:                session.StateActive,
		LastNudgeDeliveredAt: deliveredAt,
		LastActive:           deliveredAt.Add(5 * time.Second),
	})
	if got := classifyNudgeDelivery(ackObs, now, nudgeAckDeadline); got != nudgeDeliveryAcknowledged {
		t.Fatalf("acknowledged path = %q, want %q", got, nudgeDeliveryAcknowledged)
	}

	// Delivered long ago, live session but no forward activity → stalled.
	staleDelivered := now.Add(-5 * time.Minute)
	stalledObs := nudgeDeliveryObservationFromInfo(true, session.Info{
		State:                session.StateActive,
		LastNudgeDeliveredAt: staleDelivered,
		LastActive:           staleDelivered.Add(-time.Minute),
	})
	if got := classifyNudgeDelivery(stalledObs, now, nudgeAckDeadline); got != nudgeDeliveryStalled {
		t.Fatalf("stalled path = %q, want %q", got, nudgeDeliveryStalled)
	}

	// Dormant session with forward activity: not live, so never acknowledged.
	dormantObs := nudgeDeliveryObservationFromInfo(true, session.Info{
		State:                session.StateAsleep,
		LastNudgeDeliveredAt: deliveredAt,
		LastActive:           deliveredAt.Add(5 * time.Second),
	})
	if got := classifyNudgeDelivery(dormantObs, now, nudgeAckDeadline); got.isActiveExecution() {
		t.Fatalf("dormant session classified active (%q); must not over-report execution", got)
	}
}

// TestCmdNudgeStatusTableShowsDeliveryState verifies the human-readable status
// table carries the cross-checked delivery state, so an operator reading it sees
// that routed work with no delivery is merely queued — never active.
func TestCmdNudgeStatusTableShowsDeliveryState(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	cityDir := t.TempDir()
	writeNamedSessionCityTOML(t, cityDir)
	t.Setenv("GC_CITY", cityDir)

	now := time.Now().Add(-time.Minute)
	if err := enqueueQueuedNudge(cityDir, newQueuedNudge("mayor", "review queued work", now)); err != nil {
		t.Fatalf("enqueueQueuedNudge: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := cmdNudgeStatus([]string{"mayor"}, false, &stdout, &stderr); code != 0 {
		t.Fatalf("cmdNudgeStatus table = %d, want 0; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "DELIVERY") {
		t.Fatalf("status table missing DELIVERY column:\n%s", out)
	}
	if !strings.Contains(out, string(nudgeDeliveryQueued)) {
		t.Fatalf("status table delivery state != %q:\n%s", nudgeDeliveryQueued, out)
	}
	if strings.Contains(out, string(nudgeDeliveryAcknowledged)) {
		t.Fatalf("routed-only work must not show as acknowledged/active:\n%s", out)
	}
}

// TestNudgeDeliveryStalledIsActionableFault pins that the stalled state is the
// actionable stalled-delivery fault surfaced to health checks.
func TestNudgeDeliveryStalledIsActionableFault(t *testing.T) {
	if !nudgeDeliveryStalled.isStalledFault() {
		t.Fatal("stalled state must report as an actionable fault")
	}
	for _, s := range []nudgeDeliveryState{
		nudgeDeliveryUnrouted, nudgeDeliveryQueued, nudgeDeliveryAwaitingAck, nudgeDeliveryAcknowledged,
	} {
		if s.isStalledFault() {
			t.Fatalf("state %q must not report as a stalled fault", s)
		}
	}
}
