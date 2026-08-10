package beads

import (
	"errors"
	"fmt"
	"time"
)

// ReadyDirectChildrenReader lets a store preserve backend-specific dependency
// target identity while selecting ready formula children.
type ReadyDirectChildrenReader interface {
	ReadyDirectChildren(parentID, beadType string, tier TierMode) ([]Bead, error)
}

// ReadyDirectChildren returns open, dependency-ready direct children of the
// requested type in deterministic creation order. Unlike Store.Ready, the
// caller explicitly selects an infrastructure type such as "step", so this
// helper applies readiness policy without the default actionable-type filter.
func ReadyDirectChildren(store Store, parentID, beadType string, tier TierMode) ([]Bead, error) {
	if store == nil {
		return nil, errors.New("listing ready direct children: nil store")
	}
	if reader, ok := store.(ReadyDirectChildrenReader); ok {
		return reader.ReadyDirectChildren(parentID, beadType, tier)
	}
	children, err := store.List(ListQuery{
		Status:   "open",
		Type:     beadType,
		ParentID: parentID,
		TierMode: tier,
		Sort:     SortCreatedAsc,
	})
	if err != nil {
		return nil, fmt.Errorf("listing ready direct children of %q: %w", parentID, err)
	}

	now := time.Now().UTC()
	ready := make([]Bead, 0, len(children))
	for _, child := range children {
		if IsDeferred(child, now) || HasReadyExcludedLabel(child) {
			continue
		}
		deps, err := store.DepList(child.ID, "down")
		if err != nil {
			return nil, fmt.Errorf("listing dependencies for child %q: %w", child.ID, err)
		}
		blocked := false
		for _, dep := range deps {
			if !IsReadyBlockingDependencyType(dep.Type) {
				continue
			}
			blocker, err := store.Get(dep.DependsOnID)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					blocked = true
					break
				}
				return nil, fmt.Errorf("reading dependency %q for child %q: %w", dep.DependsOnID, child.ID, err)
			}
			if blocker.Status != "closed" {
				blocked = true
				break
			}
		}
		if !blocked {
			ready = append(ready, child)
		}
	}
	return ready, nil
}
