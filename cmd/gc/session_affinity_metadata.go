package main

import (
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// sessionAffinityExcludes reports whether md pins its bead to a session that is
// none of identities, i.e. whether this slot must treat the bead as invisible.
// Both the hook claim path and the idle-claim nudge gate on it, so the rule has
// one definition rather than one per caller.
//
// identities must be the slot's OWN runtime identity (session name / id /
// alias), never the bare pool template: the template is shared by every slot in
// the pool, so admitting it would match a step pinned to any one of them and
// reopen the very hole this closes (gc-zf4). Unpinned beads — including
// affinity=require steps that have not bound a session yet — are never
// excluded; see beadmeta.PinnedSessionName for why both halves are required.
//
// An empty identities set excludes every pinned bead, which is the fail-safe
// direction: a caller that cannot name itself is not the session the bead is
// pinned to, and guessing otherwise is what hands one step to two workers.
func sessionAffinityExcludes(md map[string]string, identities ...string) bool {
	pinned := beadmeta.PinnedSessionName(md)
	if pinned == "" {
		return false
	}
	for _, identity := range identities {
		if pinned == strings.TrimSpace(identity) {
			return false
		}
	}
	return true
}

// clearedSessionAffinityMetadata returns a metadata map with every
// beadmeta.SessionAffinityMetadataKeys entry set to the empty string. cmd/gc
// clears affinity by persisting an empty value rather than deleting the key
// (as internal/dispatch does) because these helpers feed
// beads.UpdateOpts.Metadata, whose merge only touches supplied keys. Every
// consumer treats the keys as absent when strings.TrimSpace is empty, so
// empty-value and deleted are equivalent.
func clearedSessionAffinityMetadata() map[string]string {
	metadata := make(map[string]string, len(beadmeta.SessionAffinityMetadataKeys))
	for _, key := range beadmeta.SessionAffinityMetadataKeys {
		metadata[key] = ""
	}
	return metadata
}

// clearSessionAffinityMetadataOnBead persists an empty value for every
// session-affinity key on beadID. See clearedSessionAffinityMetadata for
// why cmd/gc clears by empty value rather than key deletion.
func clearSessionAffinityMetadataOnBead(store beads.Store, beadID string) error {
	for _, key := range beadmeta.SessionAffinityMetadataKeys {
		if err := store.SetMetadata(beadID, key, ""); err != nil {
			return err
		}
	}
	return nil
}
