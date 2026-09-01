package main

import (
	"encoding/json"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// rootHoldResolver answers whether the graph.v2 workflow root named rootID
// currently carries a canonical dispatch hold label. A nil resolver means "no
// root-hold information available" and every candidate passes unfiltered,
// the same fail-open posture the rest of the hook filter chain uses for
// undecodable input.
type rootHoldResolver func(rootID string) bool

// newRootHoldResolver builds a rootHoldResolver over get (typically a
// beads.Store.Get bound to the query's own dir/env), memoizing per unique
// root id so a work_query page with many descendants of the same workflow
// root only reads that root once. get errors (root not found, transient bd
// failure) resolve to "not held" — fail open, matching every other seam in
// this filter chain: a resolver that cannot answer must never manufacture a
// hold that was not actually observed.
//
// Each call to newRootHoldResolver returns an independently-memoized
// resolver. That is deliberate: a caller that needs a read which cannot be
// answered from an earlier resolver's cache (gc-6rae5 acceptance criterion
// 3 — a destination-side recheck after a root may have been held mid-claim)
// builds a fresh resolver rather than reusing one already populated at
// selection time.
func newRootHoldResolver(get func(id string) (beads.Bead, error)) rootHoldResolver {
	if get == nil {
		return nil
	}
	cache := make(map[string]bool)
	return func(rootID string) bool {
		rootID = strings.TrimSpace(rootID)
		if rootID == "" {
			return false
		}
		if held, ok := cache[rootID]; ok {
			return held
		}
		held := false
		if root, err := get(rootID); err == nil {
			held = beadLabelsCarryDispatchHold(root.Labels)
		}
		cache[rootID] = held
		return held
	}
}

// beadLabelsCarryDispatchHold reports whether labels contains a canonical
// dispatch hold label value (beadmeta.DispatchHoldLabels), matched
// case-insensitively — the same comparison isHeldHookCandidate uses for a
// candidate's own labels, kept identical here so a directly-held bead and a
// held-root descendant are judged by the same rule.
func beadLabelsCarryDispatchHold(labels []string) bool {
	for _, label := range labels {
		label = strings.TrimSpace(label)
		for _, hold := range beadmeta.DispatchHoldLabels {
			if strings.EqualFold(label, hold) {
				return true
			}
		}
	}
	return false
}

// isRootHeldHookCandidate reports whether item's graph.v2 workflow root —
// named by its gc.root_bead_id metadata, resolved via rootHeld — currently
// carries a canonical dispatch hold label (gc-6rae5).
//
// A held graph.v2 root does not itself label its descendant step beads, so
// isHeldHookCandidate (which only inspects a candidate's own labels) cannot
// see the hold: a mayor parking a whole workflow by holding its root left
// every load-context/workspace-setup/implement step still individually
// unlabeled and therefore still dispatchable. This is the fence for that
// gap, checked as an independent pass alongside isHeldHookCandidate.
//
// A candidate that IS its own root (no gc.root_bead_id, or one equal to its
// own id) is not covered here — isHeldHookCandidate already fences a
// directly-held bead via its own labels.
func isRootHeldHookCandidate(item map[string]any, rootHeld rootHoldResolver) bool {
	if rootHeld == nil {
		return false
	}
	id, _ := item["id"].(string)
	id = strings.TrimSpace(id)
	metadata, ok := item["metadata"].(map[string]any)
	if !ok {
		return false
	}
	rootRaw, _ := metadata[beadmeta.RootBeadIDMetadataKey].(string)
	rootID := strings.TrimSpace(rootRaw)
	if rootID == "" || rootID == id {
		return false
	}
	return rootHeld(rootID)
}

// filterRootHeldHookCandidates drops work_query candidates whose graph.v2
// workflow root is held, using rootHeld to resolve each candidate's
// gc.root_bead_id. Pure function over JSON plus the injected resolver,
// mirroring filterUnreadyHookCandidates: unparseable, non-array, or
// non-object input passes through unchanged (fail open), and a nil resolver
// is a no-op — callers with no root-hold information available get
// pre-gc-6rae5 behavior byte-for-byte.
func filterRootHeldHookCandidates(output string, rootHeld rootHoldResolver) string {
	if output == "" || rootHeld == nil {
		return output
	}
	var decoded any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		return output
	}
	arr, ok := decoded.([]any)
	if !ok {
		return output
	}
	filtered := make([]any, 0, len(arr))
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		if isRootHeldHookCandidate(obj, rootHeld) {
			continue
		}
		filtered = append(filtered, obj)
	}
	reencoded, err := json.Marshal(filtered)
	if err != nil {
		return output
	}
	return string(reencoded)
}

// beadRootIsHeld reports whether bead's graph.v2 workflow root -- named by
// its gc.root_bead_id metadata, resolved via rootHeld -- currently carries a
// canonical dispatch hold label. This is isRootHeldHookCandidate's logic
// over the beads.Bead shape the control-ready/feeder path already works in,
// rather than the raw work_query JSON the hook path parses.
func beadRootIsHeld(b beads.Bead, rootHeld rootHoldResolver) bool {
	if rootHeld == nil {
		return false
	}
	rootID := strings.TrimSpace(b.Metadata[beadmeta.RootBeadIDMetadataKey])
	if rootID == "" || rootID == b.ID {
		return false
	}
	return rootHeld(rootID)
}

// rootHoldResolverOverStores builds a rootHoldResolver by trying each store
// leg in order for a workflow root id -- the same federated fan-out shape as
// hookRootBeadGetter, but over beads.Store legs the control-ready/feeder path
// already has open (its CachingStore cache legs, or the legs
// controlReadyCacheSources resolves) instead of opening a fresh bd shell per
// leg. A nil store in the slice is skipped, and a caller with no legs at all
// gets a nil resolver -- fail open, matching every other seam here.
func rootHoldResolverOverStores(stores ...beads.Store) rootHoldResolver {
	if len(stores) == 0 {
		return nil
	}
	return newRootHoldResolver(func(id string) (beads.Bead, error) {
		var lastErr error
		for _, store := range stores {
			if store == nil {
				continue
			}
			bead, err := store.Get(id)
			if err == nil {
				return bead, nil
			}
			lastErr = err
		}
		return beads.Bead{}, lastErr
	})
}

// hookRootBeadGetter returns a fresh-read function for a graph.v2 workflow
// root bead id, trying each federated store leg in order (primary first,
// matching every other fan-out in this file) and returning the first
// successful Get. A workflow root and its descendant step beads are always
// minted into the same store by the molecule/graph-apply machinery
// (internal/molecule), so the primary leg satisfies this in the overwhelming
// case; the fallback legs exist only for the rig-scoped-agent ordering this
// file already documents elsewhere (hookWorkQueryStores).
func hookRootBeadGetter(stores []hookStore, fallbackEnv []string) func(id string) (beads.Bead, error) {
	return func(id string) (beads.Bead, error) {
		if len(stores) == 0 {
			return hookClaimBdStore("", fallbackEnv, "").Get(id)
		}
		var lastErr error
		for _, st := range stores {
			env := st.env
			if len(env) == 0 {
				env = fallbackEnv
			}
			bead, err := hookClaimBdStore(st.dir, env, "").Get(id)
			if err == nil {
				return bead, nil
			}
			lastErr = err
		}
		return beads.Bead{}, lastErr
	}
}
