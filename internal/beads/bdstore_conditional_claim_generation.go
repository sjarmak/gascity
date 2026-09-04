package beads

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
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
// flag exists in `bd update --help`.
//
// A third shape of this fix (superseded, see the exact-head review recorded
// against 74e19c487) minted strconv.FormatInt(time.Now().UTC().UnixNano(),
// 10) — a value computed fresh at the write, with no read of the bead's
// prior state, closing the second shape's sequential-reuse gap. Codex found
// two independent defects in it: UnixNano produces 19-digit values, but the
// external consumer /home/ds/gas-city/bin/gc-outcome-close parses at most 15
// decimal digits, so every claim minted this way was permanently uncloseable
// — a straightforward compatibility break, not a race. And UnixNano is wall
// -clock time with no monotonicity guarantee of its own: an NTP step or
// manual clock correction between two mints can move it backward, which can
// reproduce the exact reuse this file exists to close even with no
// concurrent contender in sight. nextClaimGenerationToken below fixes both:
// it mints from milliseconds since a fixed recent epoch (fits the 15-digit
// ceiling for roughly 31,000 years, see its doc), and it is no longer pure
// wall-clock — see mintClaimGeneration's doc for the two independent floors
// that keep it moving forward even when the clock does not.

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
// The initial Get exists to verify id names exactly one existing bead before
// anything is written — bd's resolver prefix/substring-matches an id with no
// exact hit, and a mint issued against that would fence the wrong bead
// entirely (the same gcy-g4o guard ReleaseIfCurrent carries) — and, since
// round 4, to give nextClaimGenerationToken a floor: the generation this
// claim episode observed on the bead immediately before claiming it. That
// floor is never trusted alone (a read taken before the write can never be
// proven fresh at the write — see the round-3 doc above), only as one of two
// independent lower bounds mintClaimGeneration enforces. See its doc for why
// that is safe where the round-2 shape's read-then-mint was not: this read
// no longer determines the minted value by itself.
//
// It returns ok=false, nil error when bd reports that another actor won the
// claim race (the loser never wrote anything, generation included). A bd
// build predating --claim or --set-metadata is refused as an error, never
// silently downgraded to an unfenced claim.
func (s *BdStore) ClaimWithGeneration(id string) (Bead, string, bool, error) {
	current, err := s.Get(id)
	if err != nil {
		if errors.Is(err, ErrIDCollision) {
			return Bead{}, "", false, fmt.Errorf("refusing to claim %q with generation: %w", id, err)
		}
		return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: reading current state: %w", id, err)
	}
	next := nextClaimGenerationToken(parseClaimGenerationFloor(current.Metadata[beadmeta.ClaimGenerationMetadataKey]))

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

// claimGenerationEpoch anchors mintClaimGeneration's millisecond timestamps
// so they fit gc-outcome-close's 1-15 decimal digit ceiling (the gc-3ohe47
// round-4 finding: raw UnixNano is 19 digits and every claim it mints is
// permanently uncloseable by that consumer). Milliseconds since this epoch
// do not reach 16 digits for roughly 31,000 years; a deployment still
// running this scheme that close to the ceiling needs a new epoch pushed out
// in coordination with any consumer that parses the digit width, exactly
// like a Y2038-class rollover — not a silent wraparound.
var claimGenerationEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

var (
	claimGenerationHighWaterMu sync.Mutex
	claimGenerationHighWater   int64
)

// nextClaimGenerationToken mints a fresh beadmeta.ClaimGenerationMetadataKey
// value. observedFloor is the generation ClaimWithGeneration's preflight Get
// observed on the bead immediately before this claim attempt; it is used
// only as one of mintClaimGeneration's two lower bounds, never alone — see
// that function's doc for why reintroducing a read into this computation
// does not reopen the gc-3ohe47 round-3 ABA a pure-read-derived value had.
//
// The result is still a positive decimal integer, so it stays compatible
// with NextClaimGeneration's parsing contract: a later claim on the same
// bead through this mechanism, molecule.ClaimExact, or SQLiteStore.claimTx
// advances from it exactly as it would advance from any smaller counter
// value — nothing downstream needs to know this mechanism minted it.
func nextClaimGenerationToken(observedFloor int64) string {
	return strconv.FormatInt(mintClaimGeneration(time.Now(), observedFloor), 10)
}

// mintClaimGeneration is nextClaimGenerationToken's pure core, taking the
// wall-clock reading explicitly so it is unit-testable without depending on
// real time. It returns a value strictly greater than BOTH of two
// independent lower bounds, closing the gc-3ohe47 round-4 finding that raw
// UnixNano has no monotonicity guarantee under host clock correction:
//
//  1. observedFloor, the generation this specific claim episode saw on the
//     bead just before claiming it. This guards the common case: a single
//     actor's own clock stepped backward between two of ITS OWN sequential
//     claims on the same bead. It is safe to trust here in a way the round-2
//     shape's read-then-mint was not, because it is only a floor, not the
//     minted value itself — under normal (non-adversarial-clock) conditions
//     the millisecond-since-epoch candidate below dwarfs it and this bound
//     never binds; it only takes over when the clock term has regressed.
//  2. claimGenerationHighWater, a process-local monotonic high-water mark.
//     This guards the same failure mode across calls within one process
//     even when observedFloor itself is stale relative to a writer this
//     specific call never observed (a live process's clock being corrected
//     mid-run, independent of any particular claim episode's read).
//
// Neither bound is a substitute for the other, and neither is a distributed
// compare-and-swap: bd's CLI has no metadata-CAS flag combinable with
// --claim (see the file doc), so a pathological combination of concurrent
// contention landing in the same millisecond AND a simultaneous backward
// clock step on the losing side is not provably closed by this function
// alone. What closes it is the same guarantee BdStore.Claim already depends
// on: bd's own --claim exclusivity admits only one writer's mint per
// contention round, so that scenario requires the LOSER's read to also be
// the one whose write eventually lands — which bd's single-writer semantics
// rule out.
func mintClaimGeneration(now time.Time, observedFloor int64) int64 {
	candidate := now.UTC().Sub(claimGenerationEpoch).Milliseconds()
	if candidate <= observedFloor {
		candidate = observedFloor + 1
	}

	claimGenerationHighWaterMu.Lock()
	defer claimGenerationHighWaterMu.Unlock()
	if candidate <= claimGenerationHighWater {
		candidate = claimGenerationHighWater + 1
	}
	claimGenerationHighWater = candidate
	return candidate
}

// parseClaimGenerationFloor reads the generation ClaimWithGeneration's
// preflight Get observed on a bead, for use ONLY as one of
// mintClaimGeneration's two lower bounds — never as the minted value itself.
// An absent or unparseable value floors at 0, matching NextClaimGeneration's
// treatment of a bead never claimed through either fence: it drops out of
// the max() and leaves the wall-clock/high-water bounds to decide the mint.
func parseClaimGenerationFloor(raw string) int64 {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
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
