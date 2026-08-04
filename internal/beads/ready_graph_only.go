package beads

// GraphOnlyReadyStore is an optional store capability: a per-class Router exposes
// the ready set of its ClassGraph backend ALONE, so the worker/dispatcher
// execution-readiness hot loop skips the ClassWork (Dolt) leg under
// graph_store=sqlite. A worker only ever executes graph nodes (molecule
// steps/wisps); the full federated Ready still serves the human/diagnostic
// backlog. In the identity phase (no distinct ClassGraph backend) an
// implementation MUST fall back to the full Ready so default cities stay
// byte-identical.
type GraphOnlyReadyStore interface {
	ReadyGraphOnly(query ...ReadyQuery) ([]Bead, error)
}

// GraphOnlyReadyProvider exposes a graph-only-ready handle for wrappers whose
// capability depends on wrapped runtime state. A wrapper returns ok=false when
// its backing has no distinct ClassGraph backend, so capability presence gates
// the worker-readiness path on graph_store=sqlite without a config lookup.
type GraphOnlyReadyProvider interface {
	ReadyGraphOnlyHandle() (GraphOnlyReadyStore, bool)
}

// GraphOnlyReadyFor returns the graph-only-ready capability for store when one is
// available, walking wrapper delegation. The wrapper handle
// (GraphOnlyReadyProvider) is consulted FIRST so a wrapper's ok=false gate wins
// even when the wrapper also satisfies GraphOnlyReadyStore (for example via
// embedding its backing store); a plain implementation without a handle is used
// directly. This is the reverse of GraphApplyFor's precedence, which checks the
// direct interface before the provider.
func GraphOnlyReadyFor(store Store) (GraphOnlyReadyStore, bool) {
	if store == nil {
		return nil, false
	}
	if provider, ok := store.(GraphOnlyReadyProvider); ok {
		return provider.ReadyGraphOnlyHandle()
	}
	if g, ok := store.(GraphOnlyReadyStore); ok {
		return g, true
	}
	return nil, false
}
