package main

import (
	"testing"
	"time"
)

func TestSessionRequestLessAnonymousTransitivity(t *testing.T) {
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	requests := []SessionRequest{
		{Template: "worker", Tier: "new", WorkBeadID: "newer", WorkCreatedAt: base.Add(time.Hour)},
		{Template: "worker", Tier: "new"},
		{Template: "worker", Tier: "new", WorkBeadID: "older", WorkCreatedAt: base},
	}
	less := sessionRequestLess
	equiv := func(a, b SessionRequest) bool { return !less(a, b) && !less(b, a) }
	for _, a := range requests {
		if less(a, a) {
			t.Fatal("comparator is not irreflexive")
		}
		for _, b := range requests {
			if less(a, b) && less(b, a) {
				t.Fatal("comparator is not asymmetric")
			}
			for _, c := range requests {
				if less(a, b) && less(b, c) && !less(a, c) {
					t.Fatal("comparator is not transitive")
				}
				if equiv(a, b) && equiv(b, c) && !equiv(a, c) {
					t.Fatal("comparator equivalence is not transitive")
				}
			}
		}
	}
	sortSessionRequests(requests)
	if requests[0].WorkBeadID != "older" || requests[1].WorkBeadID != "newer" || requests[2].WorkBeadID != "" {
		t.Fatalf("sorted order = %q, %q, %q", requests[0].WorkBeadID, requests[1].WorkBeadID, requests[2].WorkBeadID)
	}
}
