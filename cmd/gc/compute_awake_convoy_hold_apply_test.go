package main

import "testing"

// TestApplyConvoyHoldsMarksSeats pins the join between the store-side resolver
// (unfinishedConvoyHolds, keyed by session bead ID) and the decision-side fact
// (AwakeSessionBead.HoldsUnfinishedConvoy). Without it the resolver's verdict
// never reaches ComputeAwakeSet and the wake reason is unreachable.
func TestApplyConvoyHoldsMarksSeats(t *testing.T) {
	input := AwakeInput{SessionBeads: []AwakeSessionBead{
		{ID: "gc-seat1", SessionName: "p1"},
		{ID: "gc-seat2", SessionName: "p2"},
	}}

	applyConvoyHolds(&input, map[string]bool{"gc-seat1": true})

	if !input.SessionBeads[0].HoldsUnfinishedConvoy {
		t.Error("gc-seat1 holds an unfinished convoy but was not marked")
	}
	if input.SessionBeads[1].HoldsUnfinishedConvoy {
		t.Error("gc-seat2 holds nothing but was marked")
	}
}

// TestApplyConvoyHoldsIsInertWithoutHolds pins that an empty or nil verdict map
// leaves the input untouched, so a store that could not be read degrades to
// exactly today's behavior.
func TestApplyConvoyHoldsIsInertWithoutHolds(t *testing.T) {
	for name, holds := range map[string]map[string]bool{
		"nil":   nil,
		"empty": {},
		"false": {"gc-seat1": false},
	} {
		t.Run(name, func(t *testing.T) {
			input := AwakeInput{SessionBeads: []AwakeSessionBead{{ID: "gc-seat1", SessionName: "p1"}}}
			applyConvoyHolds(&input, holds)
			if input.SessionBeads[0].HoldsUnfinishedConvoy {
				t.Fatalf("seat marked from %s verdict map", name)
			}
		})
	}
}
