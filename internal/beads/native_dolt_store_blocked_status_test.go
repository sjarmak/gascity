package beads

import (
	"context"
	"reflect"
	"testing"

	beadslib "github.com/steveyegge/beads"
)

func TestNativeDoltStoreReadsRawBoundedBlockedStatusSnapshot(t *testing.T) {
	t.Parallel()

	storage := &blockedStatusSearchStorage{
		nativeDoltMemStorage: newNativeDoltMemStorage(),
		issues: []*beadslib.Issue{
			{ID: "gc-a", Status: beadslib.StatusBlocked, IsBlocked: false, RowVersion: 11},
			{ID: "gc-b", Status: beadslib.StatusOpen, IsBlocked: true, RowVersion: 12},
			{ID: "gc-c", Status: beadslib.StatusDeferred, IsBlocked: false, RowVersion: 13},
		},
		projectID: "project-1",
	}
	store := newNativeDoltStoreForTest(storage)

	snapshot, err := store.ReadBlockedStatusSnapshot(2)
	if err != nil {
		t.Fatalf("ReadBlockedStatusSnapshot: %v", err)
	}
	if snapshot.Complete {
		t.Fatal("Complete = true, want false when limit+1 rows exist")
	}
	want := []BlockedStatusObservation{
		{ID: "gc-a", Status: "blocked", IsBlocked: false, Revision: 11},
		{ID: "gc-b", Status: "open", IsBlocked: true, Revision: 12},
	}
	if !reflect.DeepEqual(snapshot.Observations, want) {
		t.Fatalf("Observations = %#v, want %#v", snapshot.Observations, want)
	}
	if len(storage.filters) != 1 || storage.filters[0].Limit != 3 {
		t.Fatalf("filters = %#v, want one all-status query with Limit=3", storage.filters)
	}
	if storage.recomputeCalls != 1 {
		t.Fatalf("RecomputeAllBlocked calls = %d, want 1", storage.recomputeCalls)
	}
	if snapshot.ProjectID != "project-1" {
		t.Fatalf("ProjectID = %q, want project-1", snapshot.ProjectID)
	}
	wantExcluded := []beadslib.Status{beadslib.StatusClosed}
	if storage.filters[0].Status != nil || len(storage.filters[0].Statuses) != 0 ||
		!reflect.DeepEqual(storage.filters[0].ExcludeStatus, wantExcluded) ||
		storage.filters[0].IsBlocked != nil || !storage.filters[0].SkipWisps {
		t.Fatalf("filter = %#v, want every nonclosed durable issue", storage.filters[0])
	}
}

type blockedStatusSearchStorage struct {
	*nativeDoltMemStorage
	issues         []*beadslib.Issue
	filters        []beadslib.IssueFilter
	recomputeCalls int
	projectID      string
}

func (s *blockedStatusSearchStorage) GetMetadata(_ context.Context, key string) (string, error) {
	if key == "_project_id" {
		return s.projectID, nil
	}
	return "", nil
}

func (s *blockedStatusSearchStorage) RecomputeAllBlocked(context.Context) (int, error) {
	s.recomputeCalls++
	return 0, nil
}

func (s *blockedStatusSearchStorage) SearchIssues(_ context.Context, _ string, filter beadslib.IssueFilter) ([]*beadslib.Issue, error) {
	s.filters = append(s.filters, filter)
	issues := s.issues
	if filter.Limit > 0 && len(issues) > filter.Limit {
		issues = issues[:filter.Limit]
	}
	return issues, nil
}

func (s *blockedStatusSearchStorage) IsBlockedBatch(_ context.Context, ids []string) (map[string]bool, error) {
	result := make(map[string]bool, len(ids))
	for _, id := range ids {
		for _, issue := range s.issues {
			if issue.ID == id {
				result[id] = issue.IsBlocked
				break
			}
		}
	}
	return result, nil
}

func (s *blockedStatusSearchStorage) IsBlocked(_ context.Context, id string) (bool, []string, error) {
	for _, issue := range s.issues {
		if issue.ID == id {
			return issue.IsBlocked, nil, nil
		}
	}
	return false, nil, nil
}
