package maildelivery

import (
	"testing"
	"time"
)

func sweepKey(second int, id string) DeliveryKey {
	return DeliveryKey{CreatedAt: time.Date(2026, 8, 13, 20, 0, second, 0, time.UTC), DeliveryID: id}
}

func TestPlanSweepRequiresCapturedHighWatermark(t *testing.T) {
	checkpoint := SweepCheckpoint{Version: 1, SeatRef: "seat:test-city/reviewer", Generation: 1, Revision: 1}
	available := []DeliveryKey{sweepKey(1, "a"), sweepKey(2, "b"), sweepKey(3, "c")}

	if _, err := PlanSweep(checkpoint, available[:2], 2); err == nil {
		t.Fatal("PlanSweep inferred a high-watermark from a bounded page")
	}

	checkpoint.HighWatermark = available[2]
	plan, err := PlanSweep(checkpoint, available[:2], 2)
	if err != nil {
		t.Fatalf("PlanSweep: %v", err)
	}
	if plan.HighWatermark != available[2] || len(plan.Page) != 2 || plan.Wrap {
		t.Fatalf("first plan = %#v", plan)
	}
	checkpoint, err = AdvanceSweep(checkpoint, plan)
	if err != nil {
		t.Fatalf("AdvanceSweep: %v", err)
	}
	// Arrival after capture sorts beyond the old watermark and must not extend it.
	available = append(available, sweepKey(4, "d"))
	plan, err = PlanSweep(checkpoint, available[2:], 2)
	if err != nil {
		t.Fatalf("PlanSweep second page: %v", err)
	}
	if len(plan.Page) != 1 || plan.Page[0].DeliveryID != "c" || !plan.Wrap {
		t.Fatalf("second plan = %#v", plan)
	}

	next, err := AdvanceSweep(checkpoint, plan)
	if err != nil {
		t.Fatalf("AdvanceSweep wrap: %v", err)
	}
	if next.Generation != 2 || !next.After.IsZero() || !next.HighWatermark.IsZero() {
		t.Fatalf("wrapped checkpoint = %#v", next)
	}
	if _, err := PlanSweep(next, available, 2); err == nil {
		t.Fatal("next generation planned before store-side capture")
	}
}

func TestSweepRevisitsWaitingRowUnderSustainedArrival(t *testing.T) {
	cp := SweepCheckpoint{Version: 1, SeatRef: "seat:test-city/reviewer", Generation: 1, Revision: 1}
	waiting := sweepKey(1, "waiting")
	available := []DeliveryKey{waiting, sweepKey(2, "b"), sweepKey(3, "c")}
	cp.HighWatermark = available[len(available)-1]
	visits := 0

	for iteration := 0; iteration < 8 && visits < 2; iteration++ {
		plan, err := PlanSweep(cp, available, 1)
		if err != nil {
			t.Fatalf("iteration %d PlanSweep: %v", iteration, err)
		}
		for _, key := range plan.Page {
			if key == waiting {
				visits++
			}
		}
		cp, err = AdvanceSweep(cp, plan)
		if err != nil {
			t.Fatalf("iteration %d AdvanceSweep: %v", iteration, err)
		}
		available = append(available, sweepKey(4+iteration, string(rune('d'+iteration))))
		if cp.HighWatermark.IsZero() {
			cp.HighWatermark = available[len(available)-1]
		}
	}
	if visits != 2 {
		t.Fatalf("waiting row visits = %d, want 2 despite sustained arrivals", visits)
	}
}

func TestSweepRejectsMalformedOrConflictingState(t *testing.T) {
	valid := SweepCheckpoint{Version: 1, SeatRef: "seat:test-city/reviewer", Generation: 1, Revision: 1}
	for name, cp := range map[string]SweepCheckpoint{
		"zero version":            {SeatRef: valid.SeatRef, Generation: 1, Revision: 1},
		"zero revision":           {Version: 1, SeatRef: valid.SeatRef, Generation: 1},
		"after without watermark": {Version: 1, SeatRef: valid.SeatRef, Generation: 1, Revision: 1, After: sweepKey(1, "a")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PlanSweep(cp, []DeliveryKey{sweepKey(1, "a")}, 1); err == nil {
				t.Fatal("PlanSweep succeeded")
			}
		})
	}
	if _, err := PlanSweep(valid, []DeliveryKey{sweepKey(2, "b"), sweepKey(1, "a")}, 1); err == nil {
		t.Fatal("unsorted available keys succeeded")
	}
	if _, err := AdvanceSweep(valid, SweepPlan{HighWatermark: sweepKey(1, "a")}); err == nil {
		t.Fatal("AdvanceSweep adopted an unpersisted high-watermark")
	}
}
