package beads

import (
	"fmt"
	"math"
	"strconv"
	"strings"

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
// molecule.ClaimExact's beads.ConditionalWriter.UpdateIfMatch path (and the
// narrow beads.MetadataCASWriter it would otherwise share) both require bd's
// --if-revision flag, which this city's installed bd does not have (verified
// against `bd update --help`, which lists --if-assignee/--if-status/
// --set-metadata but no --if-revision). So the hook-claim path cannot reuse
// ClaimExact's revision-fenced mechanism as-is; this file gives it a second,
// assignee-fenced fencing contract instead: read the bead's current
// generation, compute next, and write it fenced on the bead's current
// assignee (bd update <id> --if-assignee <holder> --set-metadata
// gc.claim_generation=<next>).

// AdvanceClaimGenerationOutcome classifies how
// AdvanceClaimGenerationIfCurrent resolved. It is always paired with a nil
// error; a non-nil error means the call could not be confirmed as a clean
// outcome (an id collision, an unparsable current generation, or an
// infrastructure failure bd did not classify as a precondition miss) and the
// outcome is "".
type AdvanceClaimGenerationOutcome string

const (
	// AdvanceClaimGenerationAdvanced means the fenced write landed: id's
	// beadmeta.ClaimGenerationMetadataKey is now the returned next value.
	AdvanceClaimGenerationAdvanced AdvanceClaimGenerationOutcome = "advanced"
	// AdvanceClaimGenerationStale means bd refused the write because id's
	// current assignee no longer equals expectedAssignee (its dedicated
	// precondition-miss exit code, bdCASPreconditionExitCode). Nothing was
	// written. This is a definitive "this call did not win the fence", not an
	// invitation to retry against the same fromGeneration snapshot: the caller
	// must re-read and re-decide.
	AdvanceClaimGenerationStale AdvanceClaimGenerationOutcome = "stale"
	// AdvanceClaimGenerationUnsupported means this bd build does not
	// understand --if-assignee or --set-metadata. Nothing was written, and
	// nothing was fabricated in its place.
	AdvanceClaimGenerationUnsupported AdvanceClaimGenerationOutcome = "unsupported"
)

// AdvanceClaimGenerationIfCurrent advances id's
// beadmeta.ClaimGenerationMetadataKey by one, from fromGeneration (the value
// the caller already holds, typically read off the bead a claim just landed
// on) to next, in a single bd update fenced on id's assignee still being
// exactly expectedAssignee. fromGeneration of "" means "never claimed through
// this fence before"; its next value is "1".
//
// The write is refused, never guessed, when: id resolves to a different bead
// than named (bd's prefix/substring resolver, the gcy-g4o collision shape);
// fromGeneration is present but not a positive decimal counter, or already at
// the int64 ceiling (a caller holding a corrupt or overflowed snapshot must
// not silently restart or overflow the counter); the assignee fence misses
// (AdvanceClaimGenerationStale); or this bd predates the flags
// (AdvanceClaimGenerationUnsupported). None of these defaults, fabricates, or
// bypasses the generation — the exact invariant gc-3ohe47 requires.
func (s *BdStore) AdvanceClaimGenerationIfCurrent(id, expectedAssignee, fromGeneration string) (next string, outcome AdvanceClaimGenerationOutcome, err error) {
	if collision := s.releaseIDCollision(id); collision != nil {
		return "", "", collision
	}

	toGeneration, perr := NextClaimGeneration(fromGeneration)
	if perr != nil {
		return "", "", fmt.Errorf("advance claim generation %q: %w", id, perr)
	}

	out, runErr := s.runBDTransientWriteOutput(
		"update", id,
		"--if-assignee", expectedAssignee,
		"--set-metadata", beadmeta.ClaimGenerationMetadataKey+"="+toGeneration,
	)
	if runErr == nil {
		return toGeneration, AdvanceClaimGenerationAdvanced, nil
	}
	if bdExitCode(runErr) == bdCASPreconditionExitCode {
		return "", AdvanceClaimGenerationStale, nil
	}
	detail := strings.TrimSpace(string(out)) + " " + runErr.Error()
	if isBdUnknownFlagError(detail, "--if-assignee") || isBdUnknownFlagError(detail, "--set-metadata") {
		return "", AdvanceClaimGenerationUnsupported, nil
	}
	return "", "", fmt.Errorf("advance claim generation %q: %w", id, runErr)
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
// third independent reimplementation of this parsing.
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
