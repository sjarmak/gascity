package beads

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// This file closes gc-3ohe47: a bead claimed only through `gc hook --claim`
// (the generic pool path) never got a beadmeta.ClaimGenerationMetadataKey,
// because that path is not molecule.ClaimExact's scheduler-owned,
// revision-fenced claim (see claim_exact.go's doc, which names "the generic gc
// hook --claim pool path" as the case it does not cover). Without a
// generation, gc-outcome-close's typed closer has no current-authority token
// to verify and refuses the close outright — which is correct, existing,
// fail-closed behavior this file must not weaken.
//
// A prior shape of this fix (superseded, see the exact-head review recorded
// against cc8bdceae) claimed through the ordinary Claim and then minted the
// generation in a SEPARATE, assignee-fenced write, narrowing the race with a
// pre-write/post-write read pair. The review correctly rejected that: two
// writes can detect some races but cannot make the ownership-transfer and
// generation-mint ATOMIC, no matter how tightly the reads bracket the second
// write. ClaimWithGeneration below folds both into the SAME bd update
// invocation instead, mirroring SQLiteStore.claimTx's single-transaction
// design as closely as bd's CLI allows: bd has no --if-revision CAS
// (verified against `bd update --help`, which lists --if-assignee/
// --if-status/--set-metadata but no --if-revision), so the generation value
// itself cannot be fenced the way claimTx fences it inside one SQL
// transaction. Instead this relies on the same guarantee BdStore.Claim
// already depends on: bd's own single-writer exclusivity on --claim, which
// admits only the winning claimant's --claim — and therefore only that
// claimant's paired --set-metadata, issued in the SAME bd process
// invocation — to land at all. A losing --claim aborts the whole bd update,
// including the metadata write, exactly like BdStore.Claim's existing
// race-loser classification.
//
// A second shape of this fix (superseded, see the exact-head review recorded
// against b740b1f3b) computed the minted value from a Get() taken
// immediately before the claim attempt, reasoning that the only way a second
// call could also mint a competing value was to win the SAME --claim race —
// which bd's single-writer exclusivity already rules out. That reasoning
// covered CONCURRENT contenders but missed a SEQUENTIAL one: two DIFFERENT,
// non-overlapping claim episodes can each read the SAME prior generation and
// compute the SAME "next" value, because nothing fences the read against the
// write across the gap between them. Codex demonstrated it empirically
// against bd 1.3.0-rc.1: A reads generation 7; B claims 8 and releases; A's
// stale claim then succeeds too, reusing generation 8 — a value a completed,
// released claim episode had already consumed. bd has no flag that closes
// this gap: --if-assignee and --if-status are both documented as unable to
// combine with --claim, and no --if-metadata (or any other value-level CAS)
// flag exists in `bd update --help`. No read taken before the write can ever
// be proven fresh at the write, so nextClaimGenerationToken below computes
// the minted value without reading the bead's prior generation at all — see
// its doc for why that closes the gap instead of narrowing it.

// AdvanceClaimGenerationOutcome classifies how ConfirmClaimGeneration
// resolved. It is always paired with a nil error; a non-nil error means the
// call could not be confirmed as a clean outcome (an id collision or an
// infrastructure failure) and the outcome is "".
type AdvanceClaimGenerationOutcome string

const (
	// AdvanceClaimGenerationAdvanced means the bead's current
	// beadmeta.ClaimGenerationMetadataKey is exactly the value the caller
	// expected under the assignee it expected.
	AdvanceClaimGenerationAdvanced AdvanceClaimGenerationOutcome = "advanced"
	// AdvanceClaimGenerationStale means the bead's current assignee or
	// generation no longer match what the caller expected — the fence the
	// caller is holding has already moved. Nothing was written; this
	// classifies a read, not a rejected write.
	AdvanceClaimGenerationStale AdvanceClaimGenerationOutcome = "stale"
	// AdvanceClaimGenerationUnsupported means this bd build does not
	// understand --claim or --set-metadata. Nothing was written, and
	// nothing was fabricated in its place.
	AdvanceClaimGenerationUnsupported AdvanceClaimGenerationOutcome = "unsupported"
)

// ClaimWithGeneration atomically claims id for assignee AND mints its
// beadmeta.ClaimGenerationMetadataKey in the SAME bd update invocation (`bd
// update <id> --claim --set-metadata gc.claim_generation=<next> --json`),
// closing gc-3ohe47's HIGH finding: an ordinary Claim followed by a second,
// separate generation write leaves a window in which a reader — including
// gc-outcome-close's typed closer — can observe ownership already
// transferred while the stored generation is still stale, and gives a
// second writer room to mint a competing generation against the same
// ownership transition.
//
// The initial Get exists ONLY to verify id names exactly one existing bead
// before anything is written — bd's resolver prefix/substring-matches an id
// with no exact hit, and a mint issued against that would fence the wrong
// bead entirely (the same gcy-g4o guard ReleaseIfCurrent carries). Its
// result is never used to compute the minted generation: next comes from
// nextClaimGenerationToken, which needs no read of the bead's prior state to
// be unique. See that function's doc for why a value derived from this (or
// any) pre-write read cannot be trusted for that computation.
//
// It returns ok=false, nil error when bd reports that another actor won the
// claim race (the loser never wrote anything, generation included). A bd
// build predating --claim or --set-metadata is refused as an error, never
// silently downgraded to an unfenced claim.
func (s *BdStore) ClaimWithGeneration(id string) (Bead, string, bool, error) {
	if _, err := s.Get(id); err != nil {
		if errors.Is(err, ErrIDCollision) {
			return Bead{}, "", false, fmt.Errorf("refusing to claim %q with generation: %w", id, err)
		}
		return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: reading current state: %w", id, err)
	}
	next := nextClaimGenerationToken()

	out, runErr := s.runBDTransientWriteOutput(
		"update", id,
		"--claim",
		"--set-metadata", beadmeta.ClaimGenerationMetadataKey+"="+next,
		"--json",
	)
	if runErr != nil {
		msg := strings.TrimSpace(string(out))
		if isBdClaimConflictMessage(msg) || isBdClaimConflictMessage(runErr.Error()) {
			return Bead{}, "", false, nil
		}
		detail := msg + " " + runErr.Error()
		if isBdUnknownFlagError(detail, "--claim") || isBdUnknownFlagError(detail, "--set-metadata") {
			return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: %w", id, ErrBdMissingClaimGenerationSupport)
		}
		if isBdNotFound(runErr) {
			return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: %w", id, ErrNotFound)
		}
		if msg != "" {
			return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: %w: %s", id, runErr, msg)
		}
		return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: %w", id, runErr)
	}
	claimed, err := parseBDMutationBead("bd claim-with-generation", out)
	if err != nil {
		return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: %w", id, err)
	}
	return claimed, next, true, nil
}

// nextClaimGenerationToken mints a fresh beadmeta.ClaimGenerationMetadataKey
// value without reading the bead's own last-recorded generation, closing the
// gc-3ohe47 round-3 ABA a read-then-mint predecessor of this function had:
// Codex found that a value computed from a Get() taken before the claim
// attempt can be reused across two DISTINCT claim episodes whenever a second
// claimant's whole claim-release cycle completes inside the gap between that
// read and this call's own --claim landing. bd has no flag that fences
// --claim against an expected prior metadata value (`bd update --help`
// documents --if-assignee/--if-status as unable to combine with --claim, and
// there is no --if-metadata or other value-level CAS flag), so no read taken
// before the write can ever be proven fresh at the moment of the write —
// narrowing the gap between them cannot close it, only shrink it.
//
// A value minted from the local monotonic-ish wall clock at the moment of
// the write has no such dependency: it needs no read of prior state to be
// unique, so a second claimant's stale belief about the bead's generation
// cannot poison it. Two DIFFERENT claim episodes for the same bead are
// always separated in real time by the first episode's completed release
// (bd's own --claim exclusivity forbids a second winner while the first
// still holds the claim), so the second episode's mint is always later, in
// wall-clock terms, than the first's — a collision would require two `bd
// update` process invocations completing within the same nanosecond, which
// the process-spawn cost of running bd rules out in practice.
//
// The result is still a positive decimal integer, so it stays compatible
// with NextClaimGeneration's parsing contract: a later claim on the same
// bead through this mechanism, molecule.ClaimExact, or SQLiteStore.claimTx
// advances from it exactly as it would advance from any smaller counter
// value — nothing downstream needs to know this mechanism minted it.
func nextClaimGenerationToken() string {
	return strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}

// ErrBdMissingClaimGenerationSupport means the installed bd predates --claim
// or --set-metadata support and ClaimWithGeneration refused to claim rather
// than deliver a claim it cannot fence with a generation.
var ErrBdMissingClaimGenerationSupport = errors.New("bd does not support --claim with --set-metadata")

// ConfirmClaimGeneration re-reads id and reports whether its current
// assignee and beadmeta.ClaimGenerationMetadataKey are exactly
// expectedAssignee and expectedGeneration, without writing anything.
//
// It exists for the caller that already won its claim through
// ClaimWithGeneration in the SAME call: the generation was minted
// atomically with the ownership transition, so there is nothing left to
// advance — running a second CAS here would be a second mutation racing the
// first, exactly the two-write shape gc-3ohe47 exists to close. This
// re-verifies nothing raced the claim between the atomic mint and the
// moment the caller is about to rely on the value, without ever writing.
func (s *BdStore) ConfirmClaimGeneration(id, expectedAssignee, expectedGeneration string) (AdvanceClaimGenerationOutcome, error) {
	if strings.TrimSpace(expectedGeneration) == "" {
		return AdvanceClaimGenerationStale, nil
	}
	current, err := s.Get(id)
	if err != nil {
		if errors.Is(err, ErrIDCollision) {
			return "", fmt.Errorf("refusing to confirm claim generation for %q: %w", id, err)
		}
		return "", fmt.Errorf("confirm claim generation %q: %w", id, err)
	}
	if strings.TrimSpace(current.Assignee) != strings.TrimSpace(expectedAssignee) {
		return AdvanceClaimGenerationStale, nil
	}
	if strings.TrimSpace(current.Metadata[beadmeta.ClaimGenerationMetadataKey]) != expectedGeneration {
		return AdvanceClaimGenerationStale, nil
	}
	return AdvanceClaimGenerationAdvanced, nil
}

// NextClaimGeneration advances a beadmeta.ClaimGenerationMetadataKey value by
// one, mirroring molecule.nextClaimGeneration's fail-closed semantics exactly
// (duplicated rather than shared: internal/beadmeta deliberately owns key
// NAMES only, not parsing behavior, so there is no import-safe common home for
// this logic, and internal/molecule's copy is fenced on a different,
// currently-dead-here mechanism this file must not couple to). The empty
// string (a bead never claimed through either fence) is generation 0, so its
// next value is "1". A present-but-unparseable value, one that is not a
// positive counter, or math.MaxInt64 (n+1 would silently overflow to a
// negative value) fails closed rather than guessing a restart point or
// writing a corrupt generation.
//
// Exported so cmd/gc's graph-store claim route (gc-3ohe47 HIGH #2) can advance
// the same counter through its own CompareAndSetMetadataKey fence, without a
// third independent reimplementation of this parsing. BdStore.ClaimWithGeneration
// itself no longer calls this directly (see nextClaimGenerationToken's doc for
// why a pre-write read is unsafe as this function's input in that path); it
// remains the correct successor function for any caller that already holds a
// value fenced by a real compare-and-swap, such as molecule.ClaimExact's
// UpdateIfMatch or SQLiteStore.claimTx's transaction-local read.
func NextClaimGeneration(current string) (string, error) {
	if current == "" {
		return "1", nil
	}
	n, err := strconv.ParseInt(current, 10, 64)
	if err != nil {
		return "", fmt.Errorf("claim generation %q is not a decimal counter: %w", current, err)
	}
	if n <= 0 {
		return "", fmt.Errorf("claim generation %q is not a positive counter", current)
	}
	if n == math.MaxInt64 {
		return "", fmt.Errorf("claim generation %q is at the int64 ceiling and cannot be advanced", current)
	}
	return strconv.FormatInt(n+1, 10), nil
}
