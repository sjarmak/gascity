package main

import (
	"reflect"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/executionevent"
)

// executionGraphProjectionStore labels only the read leg used to emit facts.
// Mutation callers retain their original store and optional capabilities. The
// class identity comes from opened routes, never a reconstructed config plan.
func executionGraphProjectionStore(routes *storageRoutes, workStore, graphStore beads.Store, workRef string) beads.Store {
	bindings, err := residencyBindingsFromRoutes(routes)
	if err != nil {
		return graphStore
	}
	for _, binding := range bindings {
		if sameExecutionStore(binding.Leg.Store, graphStore) { // residency:allow label an already-selected store by identity; no ownership probe or candidate selection
			return executionevent.WithStoreRef(graphStore, string(binding.Leg.Ref))
		}
	}
	if sameExecutionStore(workStore, graphStore) {
		return executionevent.WithStoreRef(graphStore, workRef)
	}
	return graphStore
}

func sameExecutionStore(a, b beads.Store) bool {
	return a != nil && b != nil && reflect.TypeOf(a).Comparable() && reflect.TypeOf(b).Comparable() && a == b
}

// executionEmitWorkStore is the work-store leg an execution-fact projection
// reads through. A convoy's tracks edges live in the store that materialized
// the molecule, but the launch beads those edges name may be resident in a
// per-rig work store on a split-store city. Reads that the primary store
// answers are returned untouched; only a primary miss consults the
// owning-store resolver, so a single-store city is byte-identical.
type executionEmitWorkStore struct {
	beads.Store
	resolveOwning func(id string) (beads.Store, bool)
}

func (s executionEmitWorkStore) Get(id string) (beads.Bead, error) {
	bead, _, err := s.GetWithStoreRef(id)
	return bead, err
}

// GetWithStoreRef keeps ownership paired with the store that answered. A
// primary miss must not attach the primary's scope to a remotely resolved row.
func (s executionEmitWorkStore) GetWithStoreRef(id string) (beads.Bead, string, error) {
	bead, ref, err := executionevent.ReadWithStoreRef(s.Store, id)
	if err == nil {
		return bead, ref, nil
	}
	if s.resolveOwning == nil {
		return bead, "", err
	}
	owning, ok := s.resolveOwning(id)
	if !ok || owning == nil {
		return bead, "", err
	}
	return executionevent.ReadWithStoreRef(owning, id)
}

// executionEmitStore wraps store for executionevent projection so run anchors
// resolve launch beads across the city's convoy stores (city + per-rig). The
// resolver probes every candidate and refuses ambiguous ids, so a bead id
// present in more than one store never anchors to a guessed row.
func executionEmitStore(store beads.Store, cityPath string) beads.Store {
	if store == nil || strings.TrimSpace(cityPath) == "" {
		return store
	}
	return executionEmitWorkStore{Store: store, resolveOwning: func(id string) (beads.Store, bool) {
		owning, _, ref, ok := autocloseOwningStoreIdentity(id, cityPath)
		return executionevent.WithStoreRef(owning, ref), ok
	}}
}
