package beadmeta

// ExecutableRouteMetadataKeys are the executable route pointers a bead must not
// carry once it is closed/terminal: the pool-claim route (gc.routed_to) and the
// control-dispatcher step-execution route (gc.execution_routed_to). A closed
// bead is never a legitimate dispatch target, so a surviving value on either
// key is stale and lets the dispatcher / route-recovery re-feed the bead (the
// gpk-3vmjj / gpk-0see3 re-route incidents).
//
// gc.run_target is deliberately excluded: it is non-executable recovery intent
// (RunTargetMetadataKey) that route_recovery's carried-route restamper acts on
// ONLY while a bead is open+unassigned, so it is harmless on a closed bead and
// is the correct source to re-route from if the bead is ever reopened.
//
// This declares which KEYS are executable routes, not their VALUES — the values
// are config-supplied agent identities, so enumerating them would hardcode role
// names (forbidden by the ZERO-hardcoded-roles rule; see values.go).
var ExecutableRouteMetadataKeys = []string{
	RoutedToMetadataKey,
	ExecutionRoutedToMetadataKey,
}

// DisarmExecutableRoutes forces every executable route pointer in m to "" so a
// close/terminal write clears any pool-claim or step-execution route in the
// SAME operation, rather than leaving it for the blocked-routed-reaper to sweep
// after the fact (which burns a pool slot in the meantime). Readers treat "" as
// "no route" (they compare strings.TrimSpace(meta[key]) != ""), so setting the
// key to "" is a durable clear on every backend whose close path merges this
// metadata map.
//
// m is mutated in place and returned for chaining; a nil m yields a fresh map.
// Callers pass freshly-built close-metadata maps, so in-place mutation has no
// aliasing hazard.
func DisarmExecutableRoutes(m map[string]string) map[string]string {
	if m == nil {
		m = make(map[string]string, len(ExecutableRouteMetadataKeys))
	}
	for _, key := range ExecutableRouteMetadataKeys {
		m[key] = ""
	}
	return m
}
