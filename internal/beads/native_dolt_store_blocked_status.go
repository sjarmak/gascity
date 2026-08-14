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
	_ BlockedStatusReader            = (*NativeDoltStore)(nil)
	_ BlockedStatusConditionalWriter = (*NativeDoltStore)(nil)
)

type nativeBlockedRecomputer interface {
	RecomputeAllBlocked(context.Context) (int, error)
}

type nativeProjectIdentityReader interface {
	GetMetadata(context.Context, string) (string, error)
}

// ReadBlockedStatusSnapshot reads every nonclosed durable issue up to limit+1.
// The extra row is the truncation witness; callers must refuse an incomplete
// snapshot before planning any write.
func (s *NativeDoltStore) ReadBlockedStatusSnapshot(limit int) (BlockedStatusSnapshot, error) {
	if limit <= 0 {
		return BlockedStatusSnapshot{}, fmt.Errorf("blocked-status snapshot limit must be positive")
	}
	storage, release, err := s.acquireStorage()
	if err != nil {
		return BlockedStatusSnapshot{}, err
	}
	defer release()
	ctx, cancel := nativeDoltOperationContext(context.TODO())
	defer cancel()
	recomputer, ok := storage.(nativeBlockedRecomputer)
	if !ok {
		return BlockedStatusSnapshot{}, fmt.Errorf("blocked-status recompute: %w", ErrConditionalWriteUnsupported)
	}
	if _, err := recomputer.RecomputeAllBlocked(ctx); err != nil {
		return BlockedStatusSnapshot{}, nativeStoreError("blocked-status recompute", err)
	}
	identityReader, ok := storage.(nativeProjectIdentityReader)
	if !ok {
		return BlockedStatusSnapshot{}, fmt.Errorf("blocked-status project identity: %w", ErrConditionalWriteUnsupported)
	}
	projectID, err := identityReader.GetMetadata(ctx, "_project_id")
	if err != nil {
		return BlockedStatusSnapshot{}, nativeStoreError("blocked-status project identity", err)
	}
	if projectID == "" {
		return BlockedStatusSnapshot{}, fmt.Errorf("blocked-status project identity is empty")
	}

	issues, err := storage.SearchIssues(ctx, "", beadslib.IssueFilter{
		ExcludeStatus: []beadslib.Status{beadslib.StatusClosed},
		SkipWisps:     true,
		Limit:         limit + 1,
	})
	if err != nil {
		return BlockedStatusSnapshot{}, nativeStoreError("blocked-status snapshot", err)
	}
	complete := len(issues) <= limit
	if !complete {
		issues = issues[:limit]
	}
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue != nil {
			ids = append(ids, issue.ID)
		}
	}
	blockedReader, ok := beadslib.AsBlockedQuerier(storage)
	if !ok {
		return BlockedStatusSnapshot{}, fmt.Errorf("blocked-status projection reader: %w", ErrConditionalWriteUnsupported)
	}
	blockedByID, err := blockedReader.IsBlockedBatch(ctx, ids)
	if err != nil {
		return BlockedStatusSnapshot{}, nativeStoreError("blocked-status projection snapshot", err)
	}
	observations := make([]BlockedStatusObservation, 0, len(issues))
	for _, issue := range issues {
		if issue == nil {
			continue
		}
		isBlocked, ok := blockedByID[issue.ID]
		if !ok {
			return BlockedStatusSnapshot{}, fmt.Errorf("blocked-status projection missing for %q", issue.ID)
		}
		metadata, err := metadataMapFromNative(issue.Metadata)
		if err != nil {
			return BlockedStatusSnapshot{}, fmt.Errorf("reading blocked-status metadata for %q: %w", issue.ID, err)
		}
		observations = append(observations, BlockedStatusObservation{
			ID:        issue.ID,
			Status:    string(issue.Status),
			IsBlocked: isBlocked,
			Revision:  issue.RowVersion,
			Metadata:  metadata,
		})
	}
	return BlockedStatusSnapshot{Observations: observations, Complete: complete, ProjectID: projectID}, nil
}

// UpdateBlockedStatusIfMatch refreshes canonical blocked state and applies the
// status/metadata projection only when revision, raw status, and is_blocked all
// still match the supplied observation in the same upstream transaction.
func (s *NativeDoltStore) UpdateBlockedStatusIfMatch(
	observation BlockedStatusObservation,
	nextStatus string,
	metadataSet map[string]string,
	metadataUnset []string,
) error {
	storage, release, err := s.acquireStorage()
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := nativeDoltOperationContext(context.TODO())
	defer cancel()
	lifecycle, err := storage.IssueLifecycle()
	if err != nil {
		return nativeStoreError(observation.ID, err)
	}

	patch := issueops.IssuePatch{
		Status: issueops.Field[issueops.Status]{Set: true, Value: issueops.Status(nextStatus)},
		Metadata: issueops.MetadataPatch{
			Set:   make(map[string]json.RawMessage, len(metadataSet)),
			Unset: append([]string(nil), metadataUnset...),
		},
	}
	for key, value := range metadataSet {
		raw, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("marshaling blocked-status metadata %q: %w", key, err)
		}
		patch.Metadata.Set[key] = raw
	}
	expectedStatus := issueops.Status(observation.Status)
	_, err = lifecycle.Update(ctx, issueops.UpdateRequest{
		Actor:               s.actor,
		IssueID:             observation.ID,
		Patch:               patch,
		ExpectedVersion:     &observation.Revision,
		ExpectedStatus:      &expectedStatus,
		ExpectedIsBlocked:   &observation.IsBlocked,
		RefreshBlockedState: true,
		IssuePlaneOnly:      true,
		Provenance:          "gc blocked-status reconciliation",
	})
	return s.nativeBlockedStatusConditionalError(ctx, storage, observation, err)
}

func (s *NativeDoltStore) nativeBlockedStatusConditionalError(
	ctx context.Context,
	storage beadslib.Storage,
	observation BlockedStatusObservation,
	err error,
) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, beadslib.ErrVersionMismatch) &&
		!errors.Is(err, issueops.ErrStatusMismatch) &&
		!errors.Is(err, issueops.ErrBlockedStateMismatch) {
		return nativeStoreError(observation.ID, err)
	}
	current := int64(0)
	issue, readErr := storage.GetIssue(ctx, observation.ID)
	if readErr == nil && issue != nil {
		current = issue.RowVersion
	}
	return &PreconditionFailedError{
		ID:       observation.ID,
		Expected: observation.Revision,
		Current:  current,
		Raw:      err.Error(),
	}
}
