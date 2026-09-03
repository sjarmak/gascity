package molecule

import (
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// continuationLeaseTerminalStatus is the one bead status
// (internal/beads.Bead.Status: "open", "in_progress", "closed") that ends a
// workflow root's life. A lease can never be (re)acquired once the root has
// reached it, matching every other terminal-status check in this codebase
// (e.g. cmd/gc/pool_session_name.go, internal/beads/native_dolt_store.go).
const continuationLeaseTerminalStatus = "closed"

// ContinuationLeaseOutcome classifies how AcquireContinuationLease or
// ReleaseContinuationLease resolved. It is always paired with a nil error; a
// non-nil error means the call could not be confirmed as a clean result
// (unknown root bead, store without conditional-write support) and the
// outcome is "".
type ContinuationLeaseOutcome string

const (
	// ContinuationLeaseAcquired means this call is now the lease's holder: a
	// fresh acquisition, or a renewal by the same session that already held
	// it. The generation fence advanced by exactly one.
	ContinuationLeaseAcquired ContinuationLeaseOutcome = "acquired"
	// ContinuationLeaseHeldByOther means a different session currently holds
	// the lease and this call's precondition failed before any write was
	// attempted (fast path), or failed atomically inside the CAS because a
	// different session won the same transition first (slow path — the
	// concurrent-acquisition race). Either way no write from this call
	// landed. The caller must not treat itself as authoritative for the
	// root's continuation group.
	ContinuationLeaseHeldByOther ContinuationLeaseOutcome = "held_by_other"
	// ContinuationLeaseRootTerminal means the root bead is already closed —
	// there is nothing left to hold a lease over. No write was attempted.
	ContinuationLeaseRootTerminal ContinuationLeaseOutcome = "root_terminal"
	// ContinuationLeaseStale means this call's view of the root's claim
	// generation was superseded by the time its CAS ran (a lost race against
	// a concurrent acquire/release, or a genuinely stale caller resuming
	// after a restart). The caller must re-read and re-decide; it must not
	// retry blindly, matching ClaimExactStale's fail-closed contract.
	ContinuationLeaseStale ContinuationLeaseOutcome = "stale"
	// ContinuationLeaseReleased means ReleaseContinuationLease cleared the
	// lease this call named. The generation fence advanced by exactly one, so
	// any session still believing it holds the pre-release generation is
	// fenced out (ContinuationLeaseStale) the next time it acts.
	ContinuationLeaseReleased ContinuationLeaseOutcome = "released"
	// ContinuationLeaseNotHeld means ReleaseContinuationLease found no lease
	// to release for the given root (or one held by a different session, when
	// requireHolder is set) — a no-op, not an error.
	ContinuationLeaseNotHeld ContinuationLeaseOutcome = "not_held"
)

// AcquireContinuationLease atomically acquires or renews the single
// continuation lease on workflow root rootID for sessionID, provided rootID's
// group precondition holds. It is the CAS-fenced enforcement of graph.v2
// gc.session_affinity=require: only the current holder (empty, meaning
// unheld, or exactly sessionID) can transition the lease, and the transition
// is fenced on the SAME beadmeta.ClaimGenerationMetadataKey counter
// molecule.ClaimExact owns, so a session whose view of the generation is
// stale can never win it out from under a live holder or a
// reconciler-recovered new owner.
//
// rootID, group, and sessionID must all be non-empty. group is fenced the
// same way the holder is: a root hosts at most one live continuation group
// at a time by construction, so a same-session renewal that names a
// DIFFERENT non-empty group is rejected (ContinuationLeaseHeldByOther)
// rather than silently overwriting the live group — a session renewing its
// own lease is only ever authorized to keep renewing the group it actually
// acquired, not to redirect the root to a group it merely wants next. A
// caller must release and re-acquire to move a root to a new group.
//
// This is a read (to observe the root's current generation, holder, and
// group) then a single ClaimExact call fenced on that generation AND on the
// holder and group matching what was just read — closing the gap a bare
// generation fence would leave open: without the holder precondition, a
// brand-new session reading the SAME generation a live holder is sitting on
// could steal the lease outright, because nothing about "the generation
// hasn't moved" implies "you are authorized to move it." The holder/group
// checks and the generation bump travel in the one CAS ClaimExact performs,
// so there is no window between "read current holder/group" and "write new
// holder/group" a second acquirer could land in.
func AcquireContinuationLease(store beads.Store, rootID, group, sessionID string) (beads.Bead, ContinuationLeaseOutcome, error) {
	rootID = strings.TrimSpace(rootID)
	group = strings.TrimSpace(group)
	sessionID = strings.TrimSpace(sessionID)
	if rootID == "" || group == "" || sessionID == "" {
		return beads.Bead{}, "", fmt.Errorf("acquire continuation lease: rootID, group, and sessionID are all required (got rootID=%q group=%q sessionID=%q)", rootID, group, sessionID)
	}

	root, err := store.Get(rootID)
	if err != nil {
		return beads.Bead{}, "", fmt.Errorf("acquire continuation lease %q: %w", rootID, err)
	}
	if root.Status == continuationLeaseTerminalStatus {
		return root, ContinuationLeaseRootTerminal, nil
	}

	currentHolder := strings.TrimSpace(root.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey])
	if currentHolder != "" && currentHolder != sessionID {
		return root, ContinuationLeaseHeldByOther, nil
	}
	currentGroup := strings.TrimSpace(root.Metadata[beadmeta.ContinuationLeaseGroupMetadataKey])
	if currentGroup != "" && currentGroup != group {
		return root, ContinuationLeaseHeldByOther, nil
	}

	want := ClaimExactPreconditions{
		Status: &root.Status,
		MetadataEquals: map[string]*string{
			beadmeta.ContinuationLeaseSessionMetadataKey: leaseHolderWant(currentHolder),
			beadmeta.ContinuationLeaseGroupMetadataKey:   leaseHolderWant(currentGroup),
		},
	}
	onSuccess := beads.UpdateOpts{
		Metadata: map[string]string{
			beadmeta.ContinuationLeaseSessionMetadataKey: sessionID,
			beadmeta.ContinuationLeaseGroupMetadataKey:   group,
		},
	}
	final, outcome, err := ClaimExact(store, rootID, want, root.Metadata[beadmeta.ClaimGenerationMetadataKey], onSuccess)
	if err != nil {
		return final, "", fmt.Errorf("acquire continuation lease %q: %w", rootID, err)
	}
	switch outcome {
	case ClaimExactClaimed:
		return final, ContinuationLeaseAcquired, nil
	case ClaimExactPreconditionFailed:
		if final.Status == continuationLeaseTerminalStatus {
			return final, ContinuationLeaseRootTerminal, nil
		}
		return final, ContinuationLeaseHeldByOther, nil
	default: // ClaimExactStale
		return final, ContinuationLeaseStale, nil
	}
}

// ContinuationLeaseHolder returns the session ID currently holding rootID's
// continuation lease, or "" if the root carries no lease (never acquired, or
// released). It is a plain read with no CAS — safe to call for precedence
// decisions (e.g. ranking claim candidates) without perturbing the lease.
func ContinuationLeaseHolder(store beads.Store, rootID string) (string, error) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return "", fmt.Errorf("continuation lease holder: rootID is required")
	}
	root, err := store.Get(rootID)
	if err != nil {
		return "", fmt.Errorf("continuation lease holder %q: %w", rootID, err)
	}
	return strings.TrimSpace(root.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey]), nil
}

// ReleaseContinuationLease atomically clears rootID's continuation lease,
// advancing the generation fence so any session still acting on the
// pre-release generation is fenced out on its next attempt
// (ContinuationLeaseStale), even if that session is merely slow rather than
// actually dead.
//
// When requireHolder is non-empty, the release only proceeds if
// requireHolder currently IS the holder (self-release: a session giving up
// its own lease) — a mismatch returns ContinuationLeaseNotHeld without
// writing anything. When requireHolder is empty, the release proceeds
// regardless of who currently holds it (reconciler recovery: releasing a
// lease whose holder has been proven dead by session-liveness checks the
// reconciler already performed, not by anything this function re-derives).
// A root with no current holder is a no-op either way (ContinuationLeaseNotHeld).
func ReleaseContinuationLease(store beads.Store, rootID, requireHolder string) (beads.Bead, ContinuationLeaseOutcome, error) {
	rootID = strings.TrimSpace(rootID)
	requireHolder = strings.TrimSpace(requireHolder)
	if rootID == "" {
		return beads.Bead{}, "", fmt.Errorf("release continuation lease: rootID is required")
	}

	root, err := store.Get(rootID)
	if err != nil {
		return beads.Bead{}, "", fmt.Errorf("release continuation lease %q: %w", rootID, err)
	}

	currentHolder := strings.TrimSpace(root.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey])
	if currentHolder == "" {
		return root, ContinuationLeaseNotHeld, nil
	}
	if requireHolder != "" && currentHolder != requireHolder {
		return root, ContinuationLeaseNotHeld, nil
	}

	want := ClaimExactPreconditions{
		MetadataEquals: map[string]*string{
			beadmeta.ContinuationLeaseSessionMetadataKey: &currentHolder,
		},
	}
	onSuccess := beads.UpdateOpts{
		Metadata: map[string]string{
			beadmeta.ContinuationLeaseSessionMetadataKey: "",
			beadmeta.ContinuationLeaseGroupMetadataKey:   "",
		},
	}
	final, outcome, err := ClaimExact(store, rootID, want, root.Metadata[beadmeta.ClaimGenerationMetadataKey], onSuccess)
	if err != nil {
		return final, "", fmt.Errorf("release continuation lease %q: %w", rootID, err)
	}
	switch outcome {
	case ClaimExactClaimed:
		return final, ContinuationLeaseReleased, nil
	case ClaimExactPreconditionFailed:
		return final, ContinuationLeaseNotHeld, nil
	default: // ClaimExactStale
		return final, ContinuationLeaseStale, nil
	}
}

// AssignContinuationFenced is the single fenced primitive
// preassignHookContinuationGroup uses per sibling on a session_affinity=require
// root. A caller-side AcquireContinuationLease renewal followed by a separate,
// unconditional sibling write leaves a window between the renewal's read and
// the write's commit for a reconciler to revoke or advance the root's lease,
// after which the write still lands under authority that no longer holds.
// bd's own compare-and-swap (ConditionalWriter.UpdateIfMatch) is single-bead
// only — there is no cross-bead transaction that could check the root's
// lease state and write the sibling in one physical operation (see
// ClaimExact's single-bead contract) — so this closes the gap with the
// primitive the codebase actually has: beadmeta.ClaimGenerationMetadataKey
// only ever increases (nextClaimGeneration never resets or wraps), so an
// UNCHANGED (holder, group, generation) reading taken immediately before and
// immediately after the sibling write is proof, not a heuristic, that no
// acquire/renew/release landed on the root while the write was in flight. A
// changed reading means authority moved mid-write: this function reverts the
// sibling assignment (fenced on the sibling's own revision, re-read fresh, so
// a write that legitimately lands on the sibling afterward — a different,
// now-authorized session claiming it, say — is never clobbered) and reports
// ContinuationLeaseStale instead of leaving the sibling durably assigned
// under authority that did not hold for the write's whole duration.
func AssignContinuationFenced(store beads.Store, rootID, group, sessionID, siblingID string) (ContinuationLeaseOutcome, error) {
	rootID = strings.TrimSpace(rootID)
	group = strings.TrimSpace(group)
	sessionID = strings.TrimSpace(sessionID)
	siblingID = strings.TrimSpace(siblingID)
	if rootID == "" || group == "" || sessionID == "" || siblingID == "" {
		return "", fmt.Errorf("assign continuation fenced: rootID, group, sessionID, and siblingID are all required (got rootID=%q group=%q sessionID=%q siblingID=%q)", rootID, group, sessionID, siblingID)
	}

	authorized, outcome, err := AcquireContinuationLease(store, rootID, group, sessionID)
	if err != nil {
		return "", err
	}
	if outcome != ContinuationLeaseAcquired {
		return outcome, nil
	}
	wantSession := authorized.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey]
	wantGroup := authorized.Metadata[beadmeta.ContinuationLeaseGroupMetadataKey]
	wantGeneration := authorized.Metadata[beadmeta.ClaimGenerationMetadataKey]

	assignee := sessionID
	if err := store.Update(siblingID, beads.UpdateOpts{Assignee: &assignee}); err != nil {
		return "", fmt.Errorf("assign continuation fenced: assigning %q: %w", siblingID, err)
	}

	revalidated, err := store.Get(rootID)
	if err != nil {
		return "", fmt.Errorf("assign continuation fenced: re-reading root %q after assigning %q: %w", rootID, siblingID, err)
	}
	if revalidated.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey] == wantSession &&
		revalidated.Metadata[beadmeta.ContinuationLeaseGroupMetadataKey] == wantGroup &&
		revalidated.Metadata[beadmeta.ClaimGenerationMetadataKey] == wantGeneration {
		return ContinuationLeaseAcquired, nil
	}

	if revertErr := revertContinuationAssignment(store, siblingID, sessionID); revertErr != nil {
		return "", fmt.Errorf("continuation lease on root %s changed while assigning %s (was holder=%s group=%s generation=%s); reverting the sibling assignment failed: %w", rootID, siblingID, wantSession, wantGroup, wantGeneration, revertErr)
	}
	return ContinuationLeaseStale, fmt.Errorf("continuation lease on root %s changed while assigning %s (was holder=%s group=%s generation=%s); assignment reverted", rootID, siblingID, wantSession, wantGroup, wantGeneration)
}

// revertContinuationAssignment undoes AssignContinuationFenced's sibling write
// after the root's lease authority proved to have moved mid-write. It is
// fenced on the sibling's own revision — re-read fresh, right before the
// revert — so a write that lands on the sibling between the failed
// re-verification and this call (a different, now-authorized session
// claiming it, say) is left alone rather than clobbered. A store with no
// conditional-write support cannot safely revert at all — an unconditional
// write here could clobber exactly the write this function exists to
// protect — so it fails closed with beads.ErrConditionalWriteUnsupported
// rather than attempting one.
func revertContinuationAssignment(store beads.Store, siblingID, expectedAssignee string) error {
	writer, ok := beads.ConditionalWriterFor(store)
	if !ok {
		return fmt.Errorf("revert continuation assignment %q: %w", siblingID, beads.ErrConditionalWriteUnsupported)
	}
	current, err := store.Get(siblingID)
	if err != nil {
		return fmt.Errorf("revert continuation assignment %q: %w", siblingID, err)
	}
	if strings.TrimSpace(current.Assignee) != expectedAssignee {
		// Someone else already moved the sibling on; nothing of ours remains
		// to revert.
		return nil
	}
	empty := ""
	if err := writer.UpdateIfMatch(siblingID, current.Revision, beads.UpdateOpts{Assignee: &empty}); err != nil {
		if beads.IsPreconditionFailed(err) {
			// The sibling moved again between our re-read and this CAS;
			// whatever is there now is not ours to revert.
			return nil
		}
		return fmt.Errorf("revert continuation assignment %q: %w", siblingID, err)
	}
	return nil
}

// leaseHolderWant returns the ClaimExactPreconditions.MetadataEquals value
// that pins a lease metadata field (holder or group) to exactly current —
// nil (meaning "must be absent or empty") when current is empty, otherwise a
// pointer to it, so a renewal only proceeds against the SAME value this call
// already observed.
func leaseHolderWant(current string) *string {
	if current == "" {
		return nil
	}
	return &current
}
