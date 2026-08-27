package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/session"
)

// TestSessionStampableWorkDirRefusesPoolSlot is the gc-j0cfh guard on the claim
// path. A pool seat's WorkDir is the slot label, a directory shared by every
// bead that slot ever runs, so stamping it onto a bead manufactures
// worktree-ownership evidence for a tree nobody owns. The reconciler already
// refuses this value (workDirStampHasOwnershipEvidence); the claim-time stamp
// added for gc-2n4c must not become a second mint path for it.
func TestSessionStampableWorkDirRefusesPoolSlot(t *testing.T) {
	info := session.Info{WorkDir: "/city/rigs/gascity/polecat-slots/polecat-2", PoolManaged: true}

	if got := sessionStampableWorkDir(info); got != "" {
		t.Fatalf("sessionStampableWorkDir(pool session) = %q, want \"\" — a slot label is not an isolated worktree", got)
	}
}

// TestSessionStampableWorkDirAllowsOwnedCheckout keeps the fallback useful for
// the sessions that do own their tree: a named or manually created session runs
// in one checkout for its whole life, and that path is exactly what the bead
// needs recorded.
func TestSessionStampableWorkDirAllowsOwnedCheckout(t *testing.T) {
	info := session.Info{WorkDir: "  /home/ds/gascity-worktrees/gc-2n4c  "}

	if got := sessionStampableWorkDir(info); got != "/home/ds/gascity-worktrees/gc-2n4c" {
		t.Fatalf("sessionStampableWorkDir(owned checkout) = %q, want the trimmed path", got)
	}
}

// TestSessionStampableWorkDirEmptyStaysEmpty covers a session that records no
// checkout at all: the honest outcome is an unset key, never a guessed path.
func TestSessionStampableWorkDirEmptyStaysEmpty(t *testing.T) {
	if got := sessionStampableWorkDir(session.Info{WorkDir: "   "}); got != "" {
		t.Fatalf("sessionStampableWorkDir(no work dir) = %q, want \"\"", got)
	}
}
