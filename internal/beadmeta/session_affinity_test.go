package beadmeta

import "testing"

// TestPinnedSessionAffinityRequireValue pins the gc.session_affinity value that
// gates routing. graphroute and dispatch both write this exact string into
// persistent bead metadata and PinnedSessionName branches on it, so a rename
// here silently unpins every persisted bead carrying the old string — which
// reads as "claimable by anyone" (gc-zf4).
func TestPinnedSessionAffinityRequireValue(t *testing.T) {
	if SessionAffinityRequire != "require" {
		t.Errorf("pinned session-affinity value drift: got %q, want %q", SessionAffinityRequire, "require")
	}
}

// TestPinnedSessionNameRequiresAffinity is the load-bearing half of the gc-zf4
// contract: gc.session_name alone must NEVER pin a bead. Release paths clear
// SessionAffinityMetadataKeys (affinity + continuation group) but deliberately
// leave gc.session_name behind as a durable back-reference (#2843), so a bead
// gated on the name alone would stay stranded on a session that has long since
// exited. Only affinity=require + a bound name pins.
func TestPinnedSessionNameRequiresAffinity(t *testing.T) {
	// A released bead: name survives, affinity was cleared -> claimable again.
	released := map[string]string{
		SessionAffinityMetadataKey: "",
		SessionNameMetadataKey:     "polecat-gc-504481",
	}
	if got := PinnedSessionName(released); got != "" {
		t.Errorf("PinnedSessionName(released bead) = %q, want \"\" (affinity cleared -> unpinned)", got)
	}
	// A bead that never carried affinity at all.
	unpinned := map[string]string{SessionNameMetadataKey: "polecat-gc-504481"}
	if got := PinnedSessionName(unpinned); got != "" {
		t.Errorf("PinnedSessionName(no affinity) = %q, want \"\"", got)
	}
}

// TestPinnedSessionNameUnboundAffinityIsNotPinned covers route time: graphroute
// stamps affinity=require on every pool step while explicitly deleting
// gc.session_name, because a pool step binds a concrete session only when a
// slot claims it (graphroute.go ApplyGraphRouteBinding). Treating that state as
// pinned would make fresh pool work claimable by nobody.
func TestPinnedSessionNameUnboundAffinityIsNotPinned(t *testing.T) {
	for name, md := range map[string]map[string]string{
		"absent name": {SessionAffinityMetadataKey: SessionAffinityRequire},
		"empty name":  {SessionAffinityMetadataKey: SessionAffinityRequire, SessionNameMetadataKey: ""},
		"blank name":  {SessionAffinityMetadataKey: SessionAffinityRequire, SessionNameMetadataKey: "   "},
	} {
		if got := PinnedSessionName(md); got != "" {
			t.Errorf("PinnedSessionName(%s) = %q, want \"\" (unbound affinity stays pool-claimable)", name, got)
		}
	}
}

// TestPinnedSessionNameReturnsBoundSession is the gc-zf4 repro shape: step
// gc-jqt carried affinity=require + a bound session_name while its assignee was
// empty and gc.routed_to was still the shared pool template.
func TestPinnedSessionNameReturnsBoundSession(t *testing.T) {
	for name, md := range map[string]map[string]string{
		"snake case": {
			SessionAffinityMetadataKey: SessionAffinityRequire,
			SessionNameMetadataKey:     "polecat-gc-504481",
		},
		// Some writers stamp the camelCase variant (see SessionNameCamelMetadataKey);
		// missing it here would leave the gate open for those beads.
		"camel case": {
			SessionAffinityMetadataKey:  SessionAffinityRequire,
			SessionNameCamelMetadataKey: "polecat-gc-504481",
		},
		"untrimmed": {
			SessionAffinityMetadataKey: "  " + SessionAffinityRequire + " ",
			SessionNameMetadataKey:     "  polecat-gc-504481  ",
		},
		// Fail safe: an unexpected case variant must still pin. Reading it as
		// "not pinned" would silently disable the gate and re-admit the
		// double-execution this bead exists to stop.
		"mixed case affinity": {
			SessionAffinityMetadataKey: "Require",
			SessionNameMetadataKey:     "polecat-gc-504481",
		},
	} {
		if got := PinnedSessionName(md); got != "polecat-gc-504481" {
			t.Errorf("PinnedSessionName(%s) = %q, want %q", name, got, "polecat-gc-504481")
		}
	}
}

// TestPinnedSessionNameNilMetadata guards the hot reconcile/claim paths, which
// hand this predicate whatever a store returned.
func TestPinnedSessionNameNilMetadata(t *testing.T) {
	if got := PinnedSessionName(nil); got != "" {
		t.Errorf("PinnedSessionName(nil) = %q, want \"\"", got)
	}
}
