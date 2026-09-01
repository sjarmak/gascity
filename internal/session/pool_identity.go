package session

import (
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
)

// PoolOwnedIdentityMetadataKey marks a session bead whose explicit session
// name was assigned by an agent's tmux_alias configuration rather than
// chosen by the session's own creator. sessionExplicitNameForNewSession
// resolves tmux_alias unconditionally for any manual or auto-materialized
// session against that agent template, so such a session ends up holding
// the exact runtime identity the template's pool-managed sessions share.
// That identity must not be permanently stranded when the manual session
// closes, or the pool can never regrow past a closed manual session's
// leftover claim (gc-2ow7r).
const PoolOwnedIdentityMetadataKey = "pool_owned_identity"

// wasPoolOwnedIdentity reports whether a bead's reserved session identity
// belongs to a pool template's shared tmux_alias rather than to the bead
// itself.
func wasPoolOwnedIdentity(b beads.Bead) bool {
	return strings.TrimSpace(b.Metadata[PoolOwnedIdentityMetadataKey]) == "true"
}
