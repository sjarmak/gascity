// Package dispatchexclusion provides the single tier-scoped predicate for
// whether a bead is excluded from ROUTED/POOL dispatch, and (once
// internal/lease exists — see dr-89vak.1, not yet landed) from taking a
// pool-granted lease.
//
// The contract (ADR-0019, dr-89vak.4): a bead is excluded when it is a mail
// message bead, OR when it is epic/container-typed AND unassigned. A bead
// already assigned to the requesting agent is never excluded on type alone —
// epic-typed assigned work is legitimate owned work and takes a lease like
// anything else. Callers pass the tier explicitly; this package never infers
// it and never looks up children, keeping Excluded pure and store-free.
package dispatchexclusion

import (
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/mail/beadmail"
)

// Tier identifies which dispatch tier a bead is being evaluated for:
// already assigned to the requesting agent, or offered through the
// unassigned routed/pool queue.
type Tier int

const (
	// TierAssigned is work already assigned to the requesting agent. Type
	// alone never excludes work in this tier; only mail does.
	TierAssigned Tier = iota
	// TierRouted is unassigned work offered through the routed/pool queue.
	// Epic/container-typed beads are excluded here because they group
	// child beads rather than being directly actionable.
	TierRouted
)

// Excluded reports whether bead b is excluded from dispatch (and, once
// wired, from taking a pool-granted lease) at the given tier. Mail message
// beads are excluded regardless of tier. Epic/container-typed beads are
// excluded only at TierRouted: a bead already assigned to the requesting
// agent (TierAssigned) is never excluded on type.
func Excluded(b beads.Bead, tier Tier) bool {
	if beadmail.IsMessageBead(b) {
		return true
	}
	return tier == TierRouted && beads.IsEpicOrContainerType(b.Type)
}
