package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

func pinnedTo(session string) map[string]string {
	return map[string]string{
		beadmeta.SessionAffinityMetadataKey: beadmeta.SessionAffinityRequire,
		beadmeta.SessionNameMetadataKey:     session,
	}
}

// TestSessionAffinityExcludesMatchesAnyIdentity: a slot is known by several
// names (session name, session id, alias), and gc.session_name may hold any of
// them depending on which writer stamped it. Matching only one would hide a
// slot's own work from itself.
func TestSessionAffinityExcludesMatchesAnyIdentity(t *testing.T) {
	identities := []string{"polecat-gc-504646", "gc-504646", "polecat-2"}
	for _, pinned := range identities {
		if sessionAffinityExcludes(pinnedTo(pinned), identities...) {
			t.Errorf("sessionAffinityExcludes(pinned to %q) = true, want false: that is one of our own identities", pinned)
		}
	}
	if !sessionAffinityExcludes(pinnedTo("polecat-gc-504481"), identities...) {
		t.Error("sessionAffinityExcludes(pinned to a foreign session) = false, want true")
	}
}

// TestSessionAffinityExcludesWithoutIdentityFailsSafe pins the fail-safe
// direction: a caller that cannot name itself is not the session a bead is
// pinned to, so it must be shown nothing pinned rather than everything.
func TestSessionAffinityExcludesWithoutIdentityFailsSafe(t *testing.T) {
	if !sessionAffinityExcludes(pinnedTo("polecat-gc-504481")) {
		t.Error("sessionAffinityExcludes(pinned, no identities) = false, want true (fail safe)")
	}
	// Unpinned work must stay visible even to an identity-less caller, or a
	// misconfigured slot would drain the whole pool queue to nothing.
	if sessionAffinityExcludes(map[string]string{beadmeta.RoutedToMetadataKey: "/home/ds/gascity/polecat"}) {
		t.Error("sessionAffinityExcludes(unpinned, no identities) = true, want false")
	}
}

// TestSessionAffinityExcludesIgnoresEmptyIdentities guards against an empty or
// whitespace identity slipping in and matching a bead whose pin failed to
// stamp, which would silently re-admit foreign work.
func TestSessionAffinityExcludesIgnoresEmptyIdentities(t *testing.T) {
	if !sessionAffinityExcludes(pinnedTo("polecat-gc-504481"), "", "   ") {
		t.Error("sessionAffinityExcludes(pinned, blank identities) = false, want true")
	}
}
