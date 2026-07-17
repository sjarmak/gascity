package beads

import (
	"maps"

	beadslib "github.com/steveyegge/beads"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// nonRunnableBeadStatuses are the raw bd status strings a bead must not carry
// an executable route under (gc-nuhl). Bead.Status collapses these (and
// "review"/"testing") to "open" post-decode (see mapBdStatus), so the gate
// below fires on the literal string an Update/UpdateIfMatch call writes,
// never on a later read of the resulting Bead.
var nonRunnableBeadStatuses = map[string]bool{
	string(beadslib.StatusBlocked):  true,
	string(beadslib.StatusDeferred): true,
}

// routeDisarmMetadataKeys are the executable pool-claim / step-execution
// route pointers that must not survive a transition to a non-runnable
// status. gc.run_target is deliberately excluded: it is non-executable
// recovery intent (beadmeta.RunTargetMetadataKey) that route_recovery's
// carried-route restamper may still use once status returns to open.
var routeDisarmMetadataKeys = []string{
	beadmeta.RoutedToMetadataKey,
	beadmeta.ExecutionRoutedToMetadataKey,
}

// disarmRouteOnNonRunnableTransition is the front door for gc-nuhl's
// invariant: a bead transitioning to a non-runnable status (blocked or
// deferred) must lose its executable routes in the SAME write, so dispatch
// can never observe a non-runnable bead as claimable, or a control-dispatcher
// step as still execution-routed, through a surviving gc.routed_to /
// gc.execution_routed_to value.
//
// Every concrete Store write path that can change status calls this at its
// single shared inner helper (bdUpdateArgs, applyUpdateLocked, nativeUpdates,
// applyUpdateOptsToBead) before applying opts, so the invariant holds
// regardless of backend and regardless of whether the caller used Update or
// the UpdateIfMatch (CAS) path -- both route through the same inner helper
// per backend.
//
// This covers every write this Go codebase controls: internal callers, the
// HTTP API, and CAS writers (they all fan out to Store.Update/UpdateIfMatch).
// It does NOT reach a worker invoking the external `bd` binary directly in a
// shell -- that process bypasses this package entirely. Closing that gap
// remains the reaper's job (blocked-routed-reaper), exactly as gc-nuhl's own
// design text keeps it: "a bounded safety net ... not the normal cleanup
// path."
//
// The caller's opts.Metadata map is never mutated in place (house style:
// immutability by default) -- this returns a copy with the routes forced to
// "" so a caller reusing its own map across calls is unaffected. Any
// caller-supplied value for these two keys in the SAME call is overridden
// unconditionally: no legitimate caller sets a route while also transitioning
// to non-runnable, so there is nothing to preserve.
func disarmRouteOnNonRunnableTransition(opts UpdateOpts) UpdateOpts {
	if opts.Status == nil || !nonRunnableBeadStatuses[*opts.Status] {
		return opts
	}
	merged := maps.Clone(opts.Metadata)
	if merged == nil {
		merged = make(map[string]string, len(routeDisarmMetadataKeys))
	}
	for _, key := range routeDisarmMetadataKeys {
		merged[key] = ""
	}
	opts.Metadata = merged
	return opts
}
