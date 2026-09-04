package main

import (
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

// unfinishedConvoyHolders reports, per session name, whether that session's
// last-assigned work bead (Info.CurrentlyProcessingBeadID) belongs to a
// graph.v2 continuation group whose workflow root is still in_progress.
//
// It anchors on CurrentlyProcessingBeadID rather than the assigned-work query
// (assignedWorkBeads) because dr-j5yb's gap is exactly the window where that
// query has nothing to return: the step just closed (closed beads drop out of
// an assigned-to-me query) and its continuation-group successor has not yet
// materialized as a new work bead. RecordCurrentBead
// (internal/session/store.go) is written only when the reconciler wakes a
// session onto a NEW bead and is never cleared when that bead closes, so it
// durably names the step this session was last actually running until the
// next step takes over -- exactly the anchor this gap needs.
//
// Store lookups are best-effort and bounded to the stores already available
// to this tick (the primary city store plus any rig stores); a lookup miss or
// error is treated as "not a convoy holder" rather than blocking the wake
// scan, consistent with every other awake-input pass being fail-open on
// missing data rather than fail-closed.
func unfinishedConvoyHolders(sessionInfos []session.Info, store beads.Store, rigStores map[string]beads.Store) map[string]bool {
	holders := make(map[string]bool)
	if store == nil {
		return holders
	}
	for _, info := range sessionInfos {
		if info.Closed {
			continue
		}
		name := strings.TrimSpace(info.SessionNameMetadata)
		stepID := strings.TrimSpace(info.CurrentlyProcessingBeadID)
		if name == "" || stepID == "" || holders[name] {
			continue
		}
		step, ok := getBeadFromAnyStore(stepID, store, rigStores)
		if !ok {
			continue
		}
		group := strings.TrimSpace(step.Metadata[beadmeta.ContinuationGroupMetadataKey])
		rootID := strings.TrimSpace(step.Metadata[beadmeta.RootBeadIDMetadataKey])
		if group == "" || rootID == "" {
			continue
		}
		root, ok := getBeadFromAnyStore(rootID, store, rigStores)
		if !ok {
			continue
		}
		if root.ID == rootID &&
			strings.EqualFold(strings.TrimSpace(root.Status), "in_progress") &&
			strings.EqualFold(strings.TrimSpace(root.Type), "task") &&
			strings.TrimSpace(root.Metadata[beadmeta.FormulaContractMetadataKey]) == "graph.v2" &&
			strings.TrimSpace(root.Metadata[beadmeta.KindMetadataKey]) == "workflow" {
			holders[name] = true
		}
	}
	return holders
}

// getBeadFromAnyStore tries the primary store first, then each rig store,
// returning the first successful read. Mirrors the bounded, best-effort store
// fan-out unfinishedConvoyHolders needs without reimplementing
// build_desired_state.go's full class-binding owner-scope resolution.
func getBeadFromAnyStore(id string, store beads.Store, rigStores map[string]beads.Store) (beads.Bead, bool) {
	if b, err := store.Get(id); err == nil {
		return b, true
	}
	for _, rs := range rigStores {
		if rs == nil {
			continue
		}
		if b, err := rs.Get(id); err == nil {
			return b, true
		}
	}
	return beads.Bead{}, false
}
