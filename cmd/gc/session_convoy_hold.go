package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
	sessionpkg "github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/storeref"
)

// unfinishedConvoyHolds reports, keyed by session bead ID, which seats are
// mid-run on a convoy that has not finished.
//
// The evidence is the seat's own currently_processing_bead_id. That marker is
// only ever overwritten with a newer bead, never cleared when a step closes, so
// at a step boundary it still names the step the seat just finished — which is
// exactly the instant the seat has no claimed work and every claim-derived wake
// reason disappears. Resolving that anchor to its convoy root
// (gc.root_bead_id, or the anchor itself when the seat is anchored on the root)
// and asking whether the root is terminal answers "is this seat between steps of
// a run it still owns" without consulting the claim state of any step.
//
// Reads fail open: an anchor or root that cannot be resolved yields no hold, so
// missing evidence reproduces today's behavior exactly rather than pinning a
// seat awake on an absence. Read failures are reported on stderr rather than
// swallowed. The root verdict is memoized, so a pool of seats working one
// convoy costs a single root read per tick.
func unfinishedConvoyHolds(
	cityPath string,
	cfg *config.City,
	store beads.Store,
	rigStores map[string]beads.Store,
	infos []sessionpkg.Info,
	stderr io.Writer,
) map[string]bool {
	holds := make(map[string]bool)
	rootUnfinished := make(map[string]bool)

	for i := range infos {
		info := infos[i]
		if info.Closed {
			continue
		}
		sessionID := strings.TrimSpace(info.ID)
		anchorID := strings.TrimSpace(info.CurrentlyProcessingBeadID)
		if sessionID == "" || anchorID == "" {
			continue
		}
		stores, err := reachableStoresForSessionInfo(cityPath, cfg, store, rigStores, info)
		if err != nil {
			reportConvoyHoldReadFailure(stderr, info, "resolving stores", err)
			continue
		}
		rootID, err := convoyRootIDForAnchor(stores, anchorID)
		if err != nil {
			reportConvoyHoldReadFailure(stderr, info, "reading current bead "+anchorID, err)
			continue
		}
		if rootID == "" {
			continue
		}
		unfinished, seen := rootUnfinished[rootID]
		if !seen {
			root, err := storeref.Resolve(rootID, stores)
			if err != nil {
				reportConvoyHoldReadFailure(stderr, info, "reading convoy root "+rootID, err)
				continue
			}
			unfinished = !convoycore.IsTerminalStatus(root.Status)
			rootUnfinished[rootID] = unfinished
		}
		if unfinished {
			holds[sessionID] = true
		}
	}
	return holds
}

// convoyRootIDForAnchor resolves the convoy root a seat's anchor bead belongs
// to. An anchor with no gc.root_bead_id is a standalone bead, not convoy work,
// and yields "" — treating it as a convoy would make any seat that ever claimed
// an ordinary task permanently unsleepable.
func convoyRootIDForAnchor(stores []beads.Store, anchorID string) (string, error) {
	anchor, err := storeref.Resolve(anchorID, stores)
	if err != nil {
		return "", err
	}
	if rootID := strings.TrimSpace(anchor.Metadata[beadmeta.RootBeadIDMetadataKey]); rootID != "" {
		return rootID, nil
	}
	if strings.TrimSpace(anchor.Metadata[beadmeta.KindMetadataKey]) == "workflow" {
		return anchor.ID, nil
	}
	return "", nil
}

func reportConvoyHoldReadFailure(stderr io.Writer, info sessionpkg.Info, what string, err error) {
	if stderr == nil {
		return
	}
	fmt.Fprintf(stderr, "session reconciler: convoy hold for %s: %s: %v\n", //nolint:errcheck // best-effort stderr
		info.SessionNameMetadata, what, err)
}
