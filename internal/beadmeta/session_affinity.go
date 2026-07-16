package beadmeta

import "strings"

// PinnedSessionName returns the session name that md pins its bead to, or ""
// when the bead is claimable by any slot the routing metadata admits. It is the
// single reader of the session-affinity contract: cmd/gc's hook claim path and
// the idle-claim nudge both route through it so one definition of "pinned"
// cannot drift between them (the divergence SessionAffinityMetadataKeys' doc
// warns about).
//
// A bead is pinned only when BOTH halves hold:
//
//   - SessionAffinityMetadataKey == SessionAffinityRequire, and
//   - SessionNameMetadataKey (or its camelCase variant) is non-empty.
//
// Both halves are load-bearing, in opposite directions:
//
// Requiring the affinity marker is what keeps released work claimable.
// gc.session_name is a durable back-reference the reconciler stamps once a bead
// is in_progress+assigned (#2843) and release paths deliberately do NOT clear
// it; they clear SessionAffinityMetadataKeys instead. Gating on a non-empty
// name alone would therefore strand every released bead on a session that has
// already exited.
//
// Requiring a bound name is what keeps fresh pool work claimable. graphroute
// stamps affinity=require on every pool-routed step while deleting
// gc.session_name, because a pool step binds a concrete session only when a
// slot claims it. Treating that unbound state as pinned would make new pool
// work claimable by nobody.
//
// The affinity comparison is case-insensitive so an unexpected case variant
// fails safe (pinned) rather than silently reopening the step to the pool.
func PinnedSessionName(md map[string]string) string {
	if !strings.EqualFold(strings.TrimSpace(md[SessionAffinityMetadataKey]), SessionAffinityRequire) {
		return ""
	}
	if name := strings.TrimSpace(md[SessionNameMetadataKey]); name != "" {
		return name
	}
	return strings.TrimSpace(md[SessionNameCamelMetadataKey])
}
