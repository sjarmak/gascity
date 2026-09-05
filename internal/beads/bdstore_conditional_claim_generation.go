package beads

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
// BdStore uses bd's destination-side version and exact-metadata predicates so
// the generation successor is derived from the same snapshot the claim write
// is required to replace. This closes both the ownership/generation split-write
// window and the sequential ABA where a delayed process reused a successor
// after another claim episode had already consumed it.

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
	// understand the guarded claim/update flags. Nothing was written, and
	// nothing was fabricated in its place.
	AdvanceClaimGenerationUnsupported AdvanceClaimGenerationOutcome = "unsupported"
)

// ClaimWithGeneration atomically claims id for assignee AND mints its
// beadmeta.ClaimGenerationMetadataKey in the SAME destination-fenced bd update
// invocation (`bd update <id> --actor <assignee> --if-version <revision>
// --if-metadata[-absent] ... --claim --set-metadata ... --json`),
// closing gc-3ohe47's HIGH finding: an ordinary Claim followed by a second,
// separate generation write leaves a window in which a reader — including
// gc-outcome-close's typed closer — can observe ownership already
// transferred while the stored generation is still stale, and gives a
// second writer room to mint a competing generation against the same
// ownership transition.
//
// The exact preflight preserves bd's raw generation JSON and row revision.
// Both are predicates on the same update, so a delayed claimant cannot apply
// a successor computed from a claim episode another process already replaced.
//
// It returns ok=false, nil error when bd reports that another actor won the
// claim race (the loser never wrote anything, generation included). A bd
// build predating --claim or --set-metadata is refused as an error, never
// silently downgraded to an unfenced claim.
func (s *BdStore) ClaimWithGeneration(id, assignee string) (Bead, string, bool, error) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: empty assignee", id)
	}
	snapshot, err := s.readClaimGenerationSnapshot(id)
	if err != nil {
		if errors.Is(err, ErrIDCollision) {
			return Bead{}, "", false, fmt.Errorf("refusing to claim %q with generation: %w", id, err)
		}
		return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: reading current state: %w", id, err)
	}
	currentGeneration, err := snapshot.generationValue()
	if err != nil {
		return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: %w", id, err)
	}
	var currentCounter int64
	if snapshot.generationPresent {
		currentCounter, err = parseStoredClaimGeneration(currentGeneration)
		if err != nil {
			return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: %w", id, err)
		}
	}
	currentOwner := snapshot.bead.Assignee
	if currentOwner != "" && currentOwner != assignee {
		return snapshot.bead, "", false, nil
	}
	if currentOwner == assignee && snapshot.bead.Status == "in_progress" {
		if !snapshot.generationPresent {
			return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: refusing to upgrade a same-owner claim with no generation", id)
		}
		return snapshot.bead, currentGeneration, true, nil
	}
	if snapshot.bead.Status == "closed" {
		return snapshot.bead, "", false, nil
	}
	if currentCounter >= maxClaimGeneration {
		return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: claim generation %q cannot be advanced within the 15-digit ceiling", id, currentGeneration)
	}
	next := strconv.FormatInt(currentCounter+1, 10)

	args := []string{
		"update", id,
		"--actor", assignee,
		"--if-version", strconv.FormatInt(snapshot.revision, 10),
	}
	if snapshot.generationPresent {
		args = append(args, "--if-metadata", beadmeta.ClaimGenerationMetadataKey+"="+string(snapshot.generationRaw))
	} else {
		args = append(args, "--if-metadata-absent", beadmeta.ClaimGenerationMetadataKey)
	}
	args = append(args,
		"--claim",
		"--set-metadata", beadmeta.ClaimGenerationMetadataKey+"="+next,
		"--json",
	)
	// Do not use the ordinary transient-write wrapper here: an ambiguous write
	// may have committed, and replaying it could target a later claim episode.
	out, runErr := s.runner(s.dir, "bd", s.bdTransientWriteArgs(args)...)
	if runErr != nil {
		msg := strings.TrimSpace(string(out))
		if isBdClaimConflictMessage(msg) || isBdClaimConflictMessage(runErr.Error()) {
			return Bead{}, "", false, nil
		}
		if isBdClaimGenerationGuardMismatch(out) {
			return Bead{}, "", false, nil
		}
		var precondition *PreconditionFailedError
		if errors.As(classifyConditionalWriteResult(out, runErr), &precondition) {
			return Bead{}, "", false, nil
		}
		detail := msg + " " + runErr.Error()
		if isBdUnknownFlagError(detail, "--claim") ||
			isBdUnknownFlagError(detail, "--set-metadata") ||
			isBdUnknownFlagError(detail, "--if-version") ||
			isBdUnknownFlagError(detail, "--if-metadata") ||
			isBdUnknownFlagError(detail, "--if-metadata-absent") {
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
	if claimed.ID != id {
		return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: update returned id %q", id, claimed.ID)
	}
	if claimed.Assignee != assignee || claimed.Metadata[beadmeta.ClaimGenerationMetadataKey] != next {
		return Bead{}, "", false, fmt.Errorf("claiming bead %q with generation: update returned owner %q generation %q, want owner %q generation %q", id, claimed.Assignee, claimed.Metadata[beadmeta.ClaimGenerationMetadataKey], assignee, next)
	}
	return claimed, next, true, nil
}

func isBdClaimGenerationGuardMismatch(out []byte) bool {
	var envelope struct {
		GuardMismatch bool `json:"guard_mismatch"`
		Failed        []struct {
			GuardMismatch bool `json:"guard_mismatch"`
		} `json:"failed"`
	}
	if err := json.Unmarshal(extractJSON(out), &envelope); err != nil {
		return false
	}
	if envelope.GuardMismatch {
		return true
	}
	for _, failure := range envelope.Failed {
		if failure.GuardMismatch {
			return true
		}
	}
	return false
}

const maxClaimGeneration int64 = 999_999_999_999_999

// parseStoredClaimGeneration validates a present generation independently of
// whether the caller needs to increment it. The 15-digit maximum is valid
// current authority (and therefore valid for read-only same-owner replay), but
// callers that need a successor must reject it before adding one.
func parseStoredClaimGeneration(raw string) (int64, error) {
	if raw == "" {
		return 0, errors.New("present claim generation is empty")
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("claim generation %q is not a decimal counter: %w", raw, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("claim generation %q is not a positive counter", raw)
	}
	if n > maxClaimGeneration {
		return 0, fmt.Errorf("claim generation %q exceeds the 15-digit ceiling", raw)
	}
	return n, nil
}

type claimGenerationSnapshot struct {
	bead              Bead
	revision          int64
	generationRaw     json.RawMessage
	generationPresent bool
}

func (s claimGenerationSnapshot) generationValue() (string, error) {
	if !s.generationPresent {
		return "", nil
	}
	token := bytes.TrimSpace(s.generationRaw)
	if len(token) == 0 {
		return "", errors.New("claim generation metadata is empty JSON")
	}
	if token[0] == '"' {
		var value string
		if err := json.Unmarshal(token, &value); err != nil {
			return "", fmt.Errorf("decoding claim generation string: %w", err)
		}
		return value, nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(token))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decoding claim generation JSON: %w", err)
	}
	var ok bool
	number, ok = value.(json.Number)
	if !ok {
		return "", fmt.Errorf("claim generation JSON must be a string or number, got %s", token)
	}
	return number.String(), nil
}

func (s *BdStore) readClaimGenerationSnapshot(id string) (claimGenerationSnapshot, error) {
	out, err := s.runBDTransientRead("show", "--json", id)
	if err != nil {
		if isBdNotFound(err) {
			return claimGenerationSnapshot{}, fmt.Errorf("getting bead %q: %w", id, ErrNotFound)
		}
		return claimGenerationSnapshot{}, err
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(extractJSON(out), &rows); err != nil {
		return claimGenerationSnapshot{}, fmt.Errorf("bd show: parsing exact JSON: %w", err)
	}
	if len(rows) == 0 {
		return claimGenerationSnapshot{}, fmt.Errorf("getting bead %q: %w", id, ErrNotFound)
	}
	var issue bdIssue
	if err := json.Unmarshal(rows[0], &issue); err != nil {
		return claimGenerationSnapshot{}, fmt.Errorf("bd show: parsing bead: %w", err)
	}
	bead := issue.toBead()
	if bead.ID != id {
		return claimGenerationSnapshot{}, fmt.Errorf("getting bead %q (resolved to %q): %w", id, bead.ID, ErrIDCollision)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rows[0], &fields); err != nil {
		return claimGenerationSnapshot{}, fmt.Errorf("bd show: parsing exact fields: %w", err)
	}
	revisionRaw, present := fields["revision"]
	if !present || bytes.Equal(bytes.TrimSpace(revisionRaw), []byte("null")) {
		return claimGenerationSnapshot{}, errors.New("bd show omitted a usable revision for guarded claim")
	}
	var revision bdRevision
	if err := json.Unmarshal(revisionRaw, &revision); err != nil {
		return claimGenerationSnapshot{}, fmt.Errorf("bd show returned invalid revision %s: %w", bytes.TrimSpace(revisionRaw), err)
	}
	snapshot := claimGenerationSnapshot{bead: bead, revision: int64(revision)}
	metadataRaw, present := fields["metadata"]
	if !present || bytes.Equal(bytes.TrimSpace(metadataRaw), []byte("null")) {
		return snapshot, nil
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		return claimGenerationSnapshot{}, fmt.Errorf("bd show: parsing exact metadata: %w", err)
	}
	generationRaw, present := metadata[beadmeta.ClaimGenerationMetadataKey]
	if !present {
		return snapshot, nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, generationRaw); err != nil {
		return claimGenerationSnapshot{}, fmt.Errorf("bd show: parsing exact claim generation JSON: %w", err)
	}
	snapshot.generationRaw = compact.Bytes()
	snapshot.generationPresent = true
	return snapshot, nil
}

// ErrBdMissingClaimGenerationSupport means the installed bd predates the
// guarded claim/update flags and ClaimWithGeneration refused to claim rather
// than deliver a claim it cannot fence with a generation.
var ErrBdMissingClaimGenerationSupport = errors.New("bd does not support guarded --claim with --set-metadata")

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
	if expectedAssignee == "" || expectedAssignee != strings.TrimSpace(expectedAssignee) ||
		expectedGeneration == "" || expectedGeneration != strings.TrimSpace(expectedGeneration) {
		return AdvanceClaimGenerationStale, nil
	}
	current, err := s.Get(id)
	if err != nil {
		if errors.Is(err, ErrIDCollision) {
			return "", fmt.Errorf("refusing to confirm claim generation for %q: %w", id, err)
		}
		return "", fmt.Errorf("confirm claim generation %q: %w", id, err)
	}
	if current.Assignee != expectedAssignee {
		return AdvanceClaimGenerationStale, nil
	}
	if current.Metadata[beadmeta.ClaimGenerationMetadataKey] != expectedGeneration {
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
// positive counter, or the 15-digit consumer ceiling fails closed rather than
// guessing a restart point or writing a token gc-outcome-close cannot parse.
// SQLiteStore.claimTx calls this only on its transaction-local row snapshot;
// BdStore uses the same successor rule with destination-side version and raw
// metadata predicates on the claim update.
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
	if n >= maxClaimGeneration {
		return "", fmt.Errorf("claim generation %q is at the 15-digit ceiling and cannot be advanced", current)
	}
	return strconv.FormatInt(n+1, 10), nil
}
