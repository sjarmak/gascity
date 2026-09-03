package beads

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// Claim atomically claims a bead for assignee via a compare-and-swap on the
// assignee field: it succeeds (status -> in_progress, assignee -> caller) only
// when the bead is open/in_progress and currently unassigned, and is idempotent
// when the same assignee already holds it. It returns ok=false (a conflict, not
// an error) when a different assignee already holds the bead or the bead is
// closed, and ErrNotFound when the bead does not exist.
//
// It is the acquire-dual of [SQLiteStore.ReleaseIfCurrent]. Single-winner under
// concurrency is guaranteed by the store's single write connection
// (MaxOpenConns=1): competing claims serialize through one BeginTx, so exactly
// one observes the bead unassigned.
func (s *SQLiteStore) Claim(id, assignee string) (Bead, bool, error) {
	b, _, ok, err := s.claimTx(id, assignee, false)
	return b, ok, err
}

// ClaimWithGeneration atomically claims id for assignee AND mints its
// beadmeta.ClaimGenerationMetadataKey in the SAME SQL transaction as the
// ownership transition (gc-3ohe47 P1: "commit claim ownership and generation
// atomically"). A two-write claim-then-advance sequence leaves a window in
// which the OLD generation is still canonical after ownership has already
// moved to the new claimant: a stale holder of that old generation could
// present it to gc-outcome-close's current-authority check before the second
// write lands. Folding both writes into Claim's own transaction closes that
// window instead of merely bounding it.
//
// The minted generation is always the transaction-local current value plus
// one (via NextClaimGeneration), read from the SAME row the ownership CAS
// just locked, never caller-supplied — so this cannot reuse, regress, or
// fabricate the counter. On the no-op "already owned" branch (this assignee
// already holds the bead in_progress) the current generation is returned
// unchanged: no write happens on that branch, so nothing advances.
func (s *SQLiteStore) ClaimWithGeneration(id, assignee string) (Bead, string, bool, error) {
	return s.claimTx(id, assignee, true)
}

// claimTx is the shared CAS body behind Claim and ClaimWithGeneration.
// mintGeneration gates the one difference between them: whether the ownership
// transition also computes and writes the next
// beadmeta.ClaimGenerationMetadataKey inside the same transaction.
func (s *SQLiteStore) claimTx(id, assignee string, mintGeneration bool) (Bead, string, bool, error) {
	if err := s.ensureOpen(); err != nil {
		return Bead{}, "", false, err
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return Bead{}, "", false, fmt.Errorf("claiming bead %q: empty assignee", id)
	}
	var claimed Bead
	var generation string
	var ok bool
	err := retryOnBusy(func() error {
		ok = false
		generation = ""
		ctx := context.Background()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("sqlite claim: begin tx: %w", err)
		}
		defer tx.Rollback() //nolint:errcheck
		b, err := s.getTx(ctx, tx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("claiming bead %q: %w", id, ErrNotFound)
			}
			return err
		}
		if b.Status != "open" && b.Status != "in_progress" {
			// Terminal and otherwise non-claimable states are never resurrected.
			return tx.Commit()
		}
		cur := strings.TrimSpace(b.Assignee)
		if cur != "" && cur != assignee {
			// Held by another worker — conflict, not an error.
			return tx.Commit()
		}
		if cur == assignee && b.Status == "in_progress" {
			// Same-owner reclaims are true no-ops: do not consume either a
			// revision, an ownership fence, or a claim generation — return the
			// stored snapshot of each exactly as it stands.
			claimed = cloneBead(b)
			generation = strings.TrimSpace(b.Metadata[beadmeta.ClaimGenerationMetadataKey])
			ok = true
			return tx.Commit()
		}
		before := b
		b.Assignee = assignee
		b.Status = "in_progress"
		b.UpdatedAt = time.Now()
		var next string
		if mintGeneration {
			next, err = NextClaimGeneration(strings.TrimSpace(b.Metadata[beadmeta.ClaimGenerationMetadataKey]))
			if err != nil {
				return fmt.Errorf("claiming bead %q: %w", id, err)
			}
			metadata := maps.Clone(b.Metadata)
			if metadata == nil {
				metadata = map[string]string{}
			}
			metadata[beadmeta.ClaimGenerationMetadataKey] = next
			b.Metadata = metadata
		}
		if err := s.upsertBeadTx(ctx, tx, b); err != nil {
			return err
		}
		if err := s.bumpClaimFenceIfOwnershipTransitionTx(ctx, tx, before, &b); err != nil {
			return err
		}
		b, err = s.getTx(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("claiming bead %q: re-reading claimed row: %w", id, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlite claim: commit: %w", err)
		}
		claimed = cloneBead(b)
		generation = next
		ok = true
		return nil
	})
	if err != nil {
		return Bead{}, "", false, err
	}
	return claimed, generation, ok, nil
}
