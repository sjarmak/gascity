package executionevent

import "github.com/gastownhall/gascity/internal/beads"

// WithStoreRef labels an exclusively owned read leg for event projection only.
// The caller must obtain ref from the route that opened store. Do not pass this
// adapter to mutation or reconciliation paths: embedding Store intentionally
// does not forward optional write capabilities or backing-store cache identity.
func WithStoreRef(store beads.Store, ref string) beads.Store {
	if store == nil || ref == "" {
		return store
	}
	return scopedStore{Store: store, ref: ref}
}

type scopedStore struct {
	beads.Store
	ref string
}

func (s scopedStore) GetWithStoreRef(id string) (beads.Bead, string, error) {
	row, err := s.Get(id)
	if err != nil || row.ID != id {
		return row, "", err
	}
	return row, s.ref, nil
}

// scopedReader pairs ownership with the read that resolved a row. A federated
// reader must return that row's owner, not its default store or an ID-prefix
// guess. Empty ownership is unknown, including on legacy stores.
type scopedReader interface {
	GetWithStoreRef(string) (beads.Bead, string, error)
}

// ReadWithStoreRef preserves a scoped reader's paired row/owner result. Legacy
// stores remain readable but cannot certify ownership. Errors or mismatched
// row IDs always discard any claimed scope.
func ReadWithStoreRef(store beads.Store, id string) (beads.Bead, string, error) {
	if scoped, ok := store.(scopedReader); ok {
		row, ref, err := scoped.GetWithStoreRef(id)
		if err != nil || row.ID != id {
			ref = ""
		}
		return row, ref, err
	}
	row, err := store.Get(id)
	return row, "", err
}

// Scope enrichment must not add reads to legacy projection or turn an
// unavailable owner into a lost association/anchor. Only paired exact reads
// may certify ownership; the existing source-chain semantics remain intact.
func resolvedSubjectStoreRef(store beads.Store, id string) string {
	if _, ok := store.(scopedReader); !ok {
		return ""
	}
	_, ref, _ := ReadWithStoreRef(store, id)
	return ref
}
