package main

import (
	"io"
	"strings"

	"github.com/gastownhall/gascity/internal/session"
)

// sessionCurrentClaimFrontDoor opens the routed session-class write front door
// for the city this process is running in. It is the shared root for the two
// halves of the claim back-channel: `gc hook --claim` stamps the claimed bead id
// onto the calling session's bead through it, and `gc hook current` reads that
// stamp back through it.
//
// It routes through cliSessionFrontDoor so a [beads.classes.sessions] relocation
// reaches both halves — a raw work-store front door would stamp the claim onto
// the work store while the real session bead lives in the relocated store, and
// `gc hook current` would then read back nothing forever. The no-refresh config
// loader matches the other hook-path roots (cmd_prime.go's
// persistPrimeHookProviderSessionKey): this runs on every claim, and a nil cfg
// leaves cliSessionStore identity to the input store.
func sessionCurrentClaimFrontDoor() (*session.Store, error) {
	cityPath, err := resolveCity()
	if err != nil {
		return nil, err
	}
	store, err := openCityStoreAt(cityPath)
	if err != nil {
		return nil, err
	}
	cfg, _ := loadCityConfigWithoutBuiltinPackRefresh(cityPath, io.Discard)
	return cliSessionFrontDoor(store, cfg, cityPath), nil
}

// hookStampSessionCurrentClaimReceipt records the exact work bead and logical
// store leg the session identified by sessionID is currently running. It is the
// production implementation of hookClaimOps.StampSessionClaimReceipt.
//
// SetCurrentClaimReceipt resolves the session id EXACTLY, refuses a non-session
// bead, and commits bead ID plus store ref in one Update. A fuzzy post-claim
// write could otherwise decorate a prefix-colliding session; separate field
// writes could authorize a same-ID row in the wrong store. The typed method also
// compare-and-skips, so per-tick adoption emits no write once both values match.
func hookStampSessionCurrentClaimReceipt(sessionID string, receipt session.CurrentClaimReceipt) error {
	sessFront, err := sessionCurrentClaimFrontDoor()
	if err != nil {
		return err
	}
	_, err = sessFront.SetCurrentClaimReceipt(sessionID, receipt)
	return err
}

// hookSessionDrainPending reports whether the session identified by sessionID is
// already draining. It is the production implementation of the
// hookClaimOps.DrainPending seam — the F-D claim fence's only input.
//
// The SESSION ROW is the source, not provider meta. `gc runtime drain-check`
// reads GC_DRAIN, which reconciler-tracked drains never set, so a provider-meta
// probe would miss the whole keyed population this fence exists for. It reads
// through the same routed front door the claim back-channel writes through, so a
// [beads.classes.sessions] relocation reaches the fence too.
//
// Any state OTHER than draining — including a closed row, whose runtime state
// GetState reports as empty — is not this fence's business. A closed or
// superseded incarnation is the runtime-identity fence's stale-session lane, and
// answering false here leaves that lane's verdict intact rather than relabelling
// it. Errors are returned rather than swallowed: the caller fails OPEN on them,
// and it can only make that choice if it can see them.
func hookSessionDrainPending(sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, nil
	}
	sessFront, err := sessionCurrentClaimFrontDoor()
	if err != nil {
		return false, err
	}
	state, _, err := sessFront.GetState(sessionID)
	if err != nil {
		return false, err
	}
	return state == session.StateDraining, nil
}
