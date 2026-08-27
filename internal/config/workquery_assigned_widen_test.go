package config

import (
	"strings"
	"testing"
)

// TestAssignedTiersDoNotTruncateToASingleRow pins gc-ewk4: the four assigned
// work_query tiers must not short-circuit on a single raw bd row.
//
// Each tier prints its reader's output and exits as soon as the result is
// non-empty, and it does so BEFORE the hook layer's readiness filter runs. With
// a one-row read, an unready head-of-line bead assigned to an identity (a
// self-blocked step, or one carrying gc.disarmed) is stripped to [] by
// filterUnreadyHookCandidates and every other bead assigned to that same
// identity stays invisible behind it. Widening the read gives the filter
// something to fall through to; filterUnreadyHookCandidates and
// claimFirstEligibleHookCandidate already iterate the full candidate array.
//
// routedReadyTierCommand learned this first and reads limit=20; these tiers
// mirror it.
func TestAssignedTiersDoNotTruncateToASingleRow(t *testing.T) {
	topos := map[string]QueryTopology{
		"single-store": {},
		"federated":    {FederatedReady: true},
	}
	tiers := map[string]func(QueryTopology) string{
		"standardAssignedInProgress":      standardAssignedInProgressWorkQueryScript,
		"standardAssignedReady":           standardAssignedReadyWorkQueryScript,
		"legacyControlAssignedInProgress": legacyControlAssignedInProgressWorkQueryScript,
		"legacyControlAssignedReady":      legacyControlAssignedReadyWorkQueryScript,
	}
	for topoName, topo := range topos {
		for tierName, tier := range tiers {
			script := tier(topo)
			if strings.Contains(script, "--limit=1 ") || strings.HasSuffix(script, "--limit=1") {
				t.Errorf("%s/%s reads a single row (--limit=1); an unready head hides every co-assigned bead: %s", topoName, tierName, script)
			}
			if !strings.Contains(script, "--limit="+assignedTierWidenLimit) {
				t.Errorf("%s/%s does not read the widened assigned-tier limit %s: %s", topoName, tierName, assignedTierWidenLimit, script)
			}
		}
	}
}
