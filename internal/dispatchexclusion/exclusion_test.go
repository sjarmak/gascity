package dispatchexclusion

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// TestExcludedCases exercises the five contract cases from ADR-0019 step
// dr-89vak.4: a bead already assigned to the requesting agent is never
// excluded on type (epic-typed assigned work is legitimate owned work and
// takes a lease like anything else), mail is excluded regardless of tier,
// and unassigned epic/container beads are excluded only from the routed
// tier.
func TestExcludedCases(t *testing.T) {
	cases := []struct {
		name string
		bead beads.Bead
		tier Tier
		want bool
	}{
		{
			name: "assigned epic is not excluded",
			bead: beads.Bead{ID: "ga-1", Type: "epic", Assignee: "cand"},
			tier: TierAssigned,
			want: false,
		},
		{
			name: "unassigned epic is excluded",
			bead: beads.Bead{ID: "ga-2", Type: "epic"},
			tier: TierRouted,
			want: true,
		},
		{
			name: "assigned mail bead is excluded",
			bead: beads.Bead{ID: "ga-3", Type: "message", Assignee: "cand"},
			tier: TierAssigned,
			want: true,
		},
		{
			name: "unassigned task is not excluded",
			bead: beads.Bead{ID: "ga-4", Type: "task"},
			tier: TierRouted,
			want: false,
		},
		{
			name: "unassigned convoy container is excluded",
			bead: beads.Bead{ID: "ga-5", Type: "convoy"},
			tier: TierRouted,
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Excluded(tc.bead, tc.tier)
			if got != tc.want {
				t.Errorf("Excluded(%+v, tier=%v) = %v, want %v", tc.bead, tc.tier, got, tc.want)
			}
		})
	}
}

// TestExcludedAssignedTierNeverExcludesOnTypeAlone is the mutation-test
// anchor for the assigned-tier carve-out: an assigned epic must pass through
// TierAssigned unexcluded. If a future change collapses the tier distinction
// (checking type regardless of tier), this test goes red.
func TestExcludedAssignedTierNeverExcludesOnTypeAlone(t *testing.T) {
	b := beads.Bead{ID: "ga-6", Type: "epic", Assignee: "cand"}
	if Excluded(b, TierAssigned) {
		t.Fatalf("Excluded(%+v, TierAssigned) = true, want false: assigned epic-typed work is owned work, not excluded on type", b)
	}
}
