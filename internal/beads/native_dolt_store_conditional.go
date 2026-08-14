package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	beadslib "github.com/steveyegge/beads"
	"github.com/steveyegge/beads/issueops"
)

var (
	_ ConditionalWriter = (*NativeDoltStore)(nil)
	_ MetadataCASWriter = (*NativeDoltStore)(nil)
)

// UpdateIfMatch applies opts when the issue still carries expectedRevision.
func (s *NativeDoltStore) UpdateIfMatch(id string, expectedRevision int64, opts UpdateOpts) error {
	if isEmptyUpdateOpts(opts) {
		return ErrEmptyConditionalUpdate
	}
	storage, release, err := s.acquireStorage()
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := nativeDoltOperationContext(context.TODO())
	defer cancel()
	lifecycle, err := storage.IssueLifecycle()
	if err != nil {
		return err
	}
	patch, err := nativeConditionalIssuePatch(opts)
	if err != nil {
		return err
	}
	_, err = lifecycle.Update(ctx, issueops.UpdateRequest{
		Actor:           s.actor,
		IssueID:         id,
		Patch:           patch,
		ExpectedVersion: &expectedRevision,
	})
	return s.nativeConditionalError(ctx, storage, id, expectedRevision, err)
}

// CloseIfMatch closes the issue when it still carries expectedRevision.
func (s *NativeDoltStore) CloseIfMatch(id string, expectedRevision int64) error {
	storage, release, err := s.acquireStorage()
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := nativeDoltOperationContext(context.TODO())
	defer cancel()
	_, err = storage.CloseIssueChecked(ctx, id, s.actor, beadslib.CloseIssueOptions{
		Force: true, ExpectedVersion: &expectedRevision,
	})
	return s.nativeConditionalError(ctx, storage, id, expectedRevision, err)
}

// DeleteIfMatch uses the upstream guarded deletion role so the version check,
// dependent handling, reference rewrites, and deletion share one transaction.
func (s *NativeDoltStore) DeleteIfMatch(id string, expectedRevision int64) error {
	storage, release, err := s.acquireStorage()
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := nativeDoltOperationContext(context.TODO())
	defer cancel()
	deleter, err := storage.Deleter()
	if err != nil {
		return nativeStoreError(id, err)
	}
	_, err = deleter.Delete(ctx, issueops.DeleteRequest{
		Actor:           s.actor,
		IDs:             []string{id},
		Force:           true,
		ExpectedVersion: &expectedRevision,
	})
	if err := s.nativeConditionalError(ctx, storage, id, expectedRevision, err); err != nil {
		return err
	}
	if err := s.localStrings.DeleteBead(id); err != nil {
		return fmt.Errorf("deleting bead %q: cleaning up local strings: %w", id, err)
	}
	return nil
}

func nativeConditionalIssuePatch(opts UpdateOpts) (issueops.IssuePatch, error) {
	patch := issueops.IssuePatch{Labels: issueops.LabelPatch{Add: opts.Labels, Remove: opts.RemoveLabels}}
	if opts.Title != nil {
		patch.Title = issueops.Field[string]{Set: true, Value: *opts.Title}
	}
	if opts.Status != nil {
		patch.Status = issueops.Field[issueops.Status]{Set: true, Value: issueops.Status(*opts.Status)}
	}
	if opts.Type != nil {
		patch.IssueType = issueops.Field[issueops.IssueType]{Set: true, Value: issueops.IssueType(*opts.Type)}
	}
	if opts.Priority != nil {
		patch.Priority = issueops.Field[int]{Set: true, Value: *opts.Priority}
	}
	if opts.Description != nil {
		patch.Description = issueops.Field[string]{Set: true, Value: *opts.Description}
	}
	if opts.ParentID != nil {
		patch.ParentID = issueops.Field[string]{Set: true, Value: *opts.ParentID}
	}
	if opts.Assignee != nil {
		patch.Assignee = issueops.Field[string]{Set: true, Value: *opts.Assignee}
	}
	if len(opts.Metadata) > 0 {
		patch.Metadata.Set = make(map[string]json.RawMessage, len(opts.Metadata))
		for key, value := range opts.Metadata {
			raw, err := json.Marshal(value)
			if err != nil {
				return issueops.IssuePatch{}, fmt.Errorf("marshaling metadata value %q: %w", key, err)
			}
			patch.Metadata.Set[key] = raw
		}
	}
	return patch, nil
}

func (s *NativeDoltStore) nativeConditionalError(ctx context.Context, storage beadslib.Storage, id string, expectedRevision int64, err error) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, beadslib.ErrVersionMismatch) {
		return nativeStoreError(id, err)
	}
	current := int64(0)
	issue, readErr := storage.GetIssue(ctx, id)
	if readErr == nil && issue != nil {
		current = issue.RowVersion
	}
	return &PreconditionFailedError{ID: id, Expected: expectedRevision, Current: current, Raw: err.Error()}
}

// CompareAndSetMetadataKey atomically sets metadata[key] = next when the key's
// current value equals expected.
//
// expected == "" matches a key that is ABSENT or present with the empty value:
// parsing an absent key out of the stored metadata map yields "", so the two
// states are indistinguishable here exactly as they are to callers (release
// paths write "" to clear). Returns (true, nil) on swap, (false, nil) on a
// genuine value mismatch — a lost race is NOT an error — and (false, err) for
// a missing bead, a malformed metadata blob, or a transport failure.
//
// Atomicity is the read-check-write inside one native Dolt transaction, the
// same shape ReleaseIfCurrent uses for its assignee guard. The whole
// read-compare-write runs inside the callback, so the compare and the write
// commit together or not at all: the upstream storage layer exposes no
// conditional-UPDATE ... WHERE primitive and no raw-SQL escape hatch, making
// the transaction the only composition point available.
//
// Sibling keys are preserved with their JSON types: the public Store view is
// map[string]string, but bd metadata may also contain booleans, numbers, null,
// objects, and arrays. The transaction compares through that public string
// view, then replaces only the selected raw JSON member with a JSON string.
func (s *NativeDoltStore) CompareAndSetMetadataKey(id, key, expected, next string) (bool, error) {
	storage, release, err := s.acquireStorage()
	if err != nil {
		return false, err
	}
	defer release()
	ctx, cancel := nativeDoltOperationContext(context.TODO())
	defer cancel()

	swapped := false
	commitMsg := fmt.Sprintf("gc: compare-and-set metadata %s on bead %s", key, id)
	err = storage.RunInTransaction(ctx, commitMsg, func(tx beadslib.Transaction) error {
		// Upstream Dolt storage may retry this entire callback after a
		// retryable commit/connection failure. The result belongs to the
		// current attempt, not any earlier callback invocation: otherwise a
		// first attempt that reached UpdateIssue could leave swapped=true,
		// while a retry observes a competing value and returns a false
		// positive CAS success.
		swapped = false
		issue, err := tx.GetIssue(ctx, id)
		if err != nil {
			return nativeStoreError(id, err)
		}
		if issue == nil {
			return fmt.Errorf("compare-and-set metadata on %q: %w", id, ErrNotFound)
		}
		metadata, err := metadataMapFromNative(issue.Metadata)
		if err != nil {
			return fmt.Errorf("parsing metadata for bead %q: %w", id, err)
		}
		if metadata[key] != expected {
			// A genuine lost race. Returning nil commits an empty transaction
			// and leaves swapped false, which the caller reads as (false, nil).
			return nil
		}
		rawMetadata, err := metadataRawValuesFromNative(issue.Metadata)
		if err != nil {
			return fmt.Errorf("parsing raw metadata for bead %q: %w", id, err)
		}
		if rawMetadata == nil {
			rawMetadata = make(map[string]json.RawMessage, 1)
		}
		nextRaw, err := json.Marshal(next)
		if err != nil {
			return fmt.Errorf("marshaling metadata value %q: %w", key, err)
		}
		rawMetadata[key] = nextRaw
		rawBytes, err := json.Marshal(rawMetadata)
		if err != nil {
			return fmt.Errorf("marshaling metadata: %w", err)
		}
		raw := json.RawMessage(rawBytes)
		if err := tx.UpdateIssue(ctx, id, map[string]interface{}{"metadata": raw}, s.actor); err != nil {
			return nativeStoreError(id, err)
		}
		swapped = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return swapped, nil
}

func metadataRawValuesFromNative(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("unmarshaling metadata: %w", err)
	}
	return values, nil
}
