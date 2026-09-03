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
	// ContinuationLeaseSiblingUnavailable means AssignContinuationFenced's
	// in-transaction read of the sibling found it no longer eligible: it was
	// assigned to a different session, closed, or moved off rootID/group
	// after the caller selected it from a point-in-time snapshot (e.g.
	// ListContinuation) but before this transaction began. No write was
	// attempted — the root's lease, its generation, and the sibling are all
	// exactly as this transaction found them.
	ContinuationLeaseSiblingUnavailable ContinuationLeaseOutcome = "sibling_unavailable"
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
// root. It replaces an earlier check-write-reverify-revert design (flagged by
// exact-head review on gc-ue0tsw, target 7c5e3ec3c970631dd8a518300693537cecf587c2):
// that approach made the sibling assignment externally visible via an
// unconditional store.Update before re-verifying the root's lease, so a
// concurrent consumer could observe and act on the assignment during the
// window between that write and the compensating revert — authority the
// revert then silently discarded. bd's own compare-and-swap
// (ConditionalWriter.UpdateIfMatch) is single-bead only, so there is no
// cross-bead CAS that could check the root's lease state and write the
// sibling in one physical operation the way ClaimExact does for a single bead
// (see ClaimExact's single-bead contract).
//
// Instead this performs the root-lease precondition check, the lease's
// generation bump, and the sibling assignment inside one beads.Store.Tx
// transaction: the read of the root's current holder/group/generation and
// both writes commit as a single atomic unit, so no external reader can ever
// observe the sibling assignment without also being able to observe the
// lease renewal that authorized it — or the transaction never committed at
// all. This requires a store whose Tx provides real atomic, isolated commit
// (beads.StoreSupportsAtomicTx) — one where a failed or in-flight callback's
// writes are invisible to every other reader until commit. A store that
// cannot make that guarantee (BdStore's staged, non-atomic Tx; any store
// that does not implement beads.AtomicTxStore) fails closed with
// beads.ErrAtomicTxUnsupported rather than downgrading to the unsafe
// write-then-revert sequence this function used to perform.
//
// Because the whole check-and-write is one atomic operation, this function
// never returns ContinuationLeaseStale: there is no window between reading
// the root's generation and committing the write for a concurrent
// acquire/renew/release to land in, unlike AcquireContinuationLease's
// separate read-then-CAS.
//
// The transaction also re-reads and revalidates siblingID itself before
// either write lands: the caller's own eligibility check
// (preassignHookContinuationGroup's open/unassigned/route filter) runs
// against a point-in-time ListContinuation snapshot, and the gap between
// that snapshot and this call is exactly wide enough for another claimant to
// assign the sibling, close it, or move it to a different root/group.
// Committing the lease renewal is not proof the STALE sibling read is still
// current, so this transaction takes its own reads of siblingID and refuses
// (ContinuationLeaseSiblingUnavailable, no write at all) unless it is still
// open, still belongs to rootID/group, and is either unassigned or already
// assigned to this exact sessionID (a safe idempotent re-assignment).
func AssignContinuationFenced(store beads.Store, rootID, group, sessionID, siblingID string) (ContinuationLeaseOutcome, error) {
	rootID = strings.TrimSpace(rootID)
	group = strings.TrimSpace(group)
	sessionID = strings.TrimSpace(sessionID)
	siblingID = strings.TrimSpace(siblingID)
	if rootID == "" || group == "" || sessionID == "" || siblingID == "" {
		return "", fmt.Errorf("assign continuation fenced: rootID, group, sessionID, and siblingID are all required (got rootID=%q group=%q sessionID=%q siblingID=%q)", rootID, group, sessionID, siblingID)
	}

	if !beads.StoreSupportsAtomicTx(store) {
		return "", fmt.Errorf("assign continuation fenced %q: %w", rootID, beads.ErrAtomicTxUnsupported)
	}

	var outcome ContinuationLeaseOutcome
	commitMsg := fmt.Sprintf("assign continuation fenced: root=%s sibling=%s session=%s group=%s", rootID, siblingID, sessionID, group)
	txErr := store.Tx(commitMsg, func(tx beads.Tx) error {
		root, err := tx.Get(rootID)
		if err != nil {
			return fmt.Errorf("reading root %q: %w", rootID, err)
		}
		if root.Status == continuationLeaseTerminalStatus {
			outcome = ContinuationLeaseRootTerminal
			return nil
		}
		currentHolder := strings.TrimSpace(root.Metadata[beadmeta.ContinuationLeaseSessionMetadataKey])
		if currentHolder != "" && currentHolder != sessionID {
			outcome = ContinuationLeaseHeldByOther
			return nil
		}
		currentGroup := strings.TrimSpace(root.Metadata[beadmeta.ContinuationLeaseGroupMetadataKey])
		if currentGroup != "" && currentGroup != group {
			outcome = ContinuationLeaseHeldByOther
			return nil
		}

		sibling, err := tx.Get(siblingID)
		if err != nil {
			return fmt.Errorf("reading sibling %q: %w", siblingID, err)
		}
		if !strings.EqualFold(strings.TrimSpace(sibling.Status), "open") {
			outcome = ContinuationLeaseSiblingUnavailable
			return nil
		}
		if siblingAssignee := strings.TrimSpace(sibling.Assignee); siblingAssignee != "" && siblingAssignee != sessionID {
			outcome = ContinuationLeaseSiblingUnavailable
			return nil
		}
		if strings.TrimSpace(sibling.Metadata[beadmeta.RootBeadIDMetadataKey]) != rootID ||
			strings.TrimSpace(sibling.Metadata[beadmeta.ContinuationGroupMetadataKey]) != group {
			outcome = ContinuationLeaseSiblingUnavailable
			return nil
		}

		nextGeneration, err := nextClaimGeneration(root.Metadata[beadmeta.ClaimGenerationMetadataKey])
		if err != nil {
			return fmt.Errorf("advancing claim generation for root %q: %w", rootID, err)
		}
		if err := tx.Update(rootID, beads.UpdateOpts{
			Metadata: map[string]string{
				beadmeta.ContinuationLeaseSessionMetadataKey: sessionID,
				beadmeta.ContinuationLeaseGroupMetadataKey:   group,
				beadmeta.ClaimGenerationMetadataKey:          nextGeneration,
			},
		}); err != nil {
			return fmt.Errorf("renewing lease on root %q: %w", rootID, err)
		}

		assignee := sessionID
		if err := tx.Update(siblingID, beads.UpdateOpts{Assignee: &assignee}); err != nil {
			return fmt.Errorf("assigning %q: %w", siblingID, err)
		}

		outcome = ContinuationLeaseAcquired
		return nil
	})
	if txErr != nil {
		return "", fmt.Errorf("assign continuation fenced %q: %w", rootID, txErr)
	}
	return outcome, nil
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
