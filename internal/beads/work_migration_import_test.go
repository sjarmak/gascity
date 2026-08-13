package beads

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	beadslib "github.com/steveyegge/beads"
)

func TestNativeDoltStoreImportExactWorkSnapshotPreservesRowsAndDependencies(t *testing.T) {
	store := newNativeDoltStoreForTest(newExactWorkMemStorage())
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	updatedAt := createdAt.Add(3 * time.Hour)
	priority := 1
	defaultPriority := 2
	deferUntil := updatedAt.Add(24 * time.Hour)
	witness := "sha256:" + strings.Repeat("a", 64)

	result, err := store.ImportExactWorkSnapshot(ExactWorkSnapshot{
		SourceWitness: witness,
		Rows: []Bead{
			{
				ID: "gc-parent", Title: "parent", Status: "open", Type: "task",
				CreatedAt: createdAt, UpdatedAt: updatedAt,
			},
			{
				ID: "gc-child", Title: "child", Status: "in_progress", Type: "bug",
				Priority: &priority, CreatedAt: createdAt, UpdatedAt: updatedAt,
				Assignee: "worker", From: "sender", Ref: "source-ref",
				Description: "preserve me", Labels: []string{"b", "a"},
				Metadata: StringMap{"gc.routed_to": "worker"}, ParentID: "gc-parent",
				DeferUntil:   &deferUntil,
				Dependencies: []Dep{{IssueID: "gc-child", DependsOnID: "gc-parent", Type: "parent-child"}},
			},
			{
				ID: "gc-dangling", Title: "legacy parent", Status: "open", Type: "task",
				CreatedAt: createdAt, ParentID: "gc-missing",
			},
			{ID: "gc-p2", Title: "explicit default", CreatedAt: createdAt, Priority: &defaultPriority},
		},
	})
	if err != nil {
		t.Fatalf("ImportExactWorkSnapshot: %v", err)
	}
	if !slices.Equal(result.IDs, []string{"gc-child", "gc-dangling", "gc-p2", "gc-parent"}) {
		t.Fatalf("result IDs = %v", result.IDs)
	}

	child, err := store.Get("gc-child")
	if err != nil {
		t.Fatalf("Get child: %v", err)
	}
	if child.Status != "in_progress" || child.Type != "bug" || child.Assignee != "worker" || child.From != "sender" {
		t.Fatalf("child core fields = %+v", child)
	}
	if child.Ref != "source-ref" || child.Description != "preserve me" || child.ParentID != "gc-parent" {
		t.Fatalf("child content fields = %+v", child)
	}
	if child.Priority == nil || *child.Priority != priority {
		t.Fatalf("child priority = %v, want %d", child.Priority, priority)
	}
	if !child.CreatedAt.Equal(createdAt) || !child.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("child timestamps = %s/%s, want %s/%s", child.CreatedAt, child.UpdatedAt, createdAt, updatedAt)
	}
	if child.DeferUntil == nil || !child.DeferUntil.Equal(deferUntil) {
		t.Fatalf("child defer_until = %v, want %s", child.DeferUntil, deferUntil)
	}
	if !slices.Equal(child.Labels, []string{"b", "a"}) || child.Metadata["gc.routed_to"] != "worker" {
		t.Fatalf("child labels/metadata = %v/%v", child.Labels, child.Metadata)
	}
	if child.Metadata[workMigrationSourceWitnessMetadataKey] != witness {
		t.Fatalf("child source witness = %q, want %q", child.Metadata[workMigrationSourceWitnessMetadataKey], witness)
	}
	if len(child.Dependencies) != 1 || child.Dependencies[0] != (Dep{IssueID: "gc-child", DependsOnID: "gc-parent", Type: "parent-child"}) {
		t.Fatalf("child dependencies = %+v", child.Dependencies)
	}

	dangling, err := store.Get("gc-dangling")
	if err != nil {
		t.Fatalf("Get dangling: %v", err)
	}
	if dangling.ParentID != "gc-missing" {
		t.Fatalf("dangling ParentID = %q, want gc-missing", dangling.ParentID)
	}
	if _, leaked := dangling.Metadata[workMigrationLegacyParentMetadataKey]; leaked {
		t.Fatalf("dangling parent representation leaked through metadata: %+v", dangling.Metadata)
	}
	if !dangling.UpdatedAt.Equal(createdAt) {
		t.Fatalf("legacy missing updated_at normalized to %s, want created_at %s", dangling.UpdatedAt, createdAt)
	}
	p2, err := store.Get("gc-p2")
	if err != nil {
		t.Fatalf("Get explicit P2: %v", err)
	}
	if p2.Priority != nil {
		t.Fatalf("explicit P2 did not normalize to native default: %v", p2.Priority)
	}
}

func TestPrepareExactWorkSnapshotEncodesOversizeTitleLosslessly(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	fullTitle := strings.Repeat("界", 167) + "ab"
	witness := "sha256:" + strings.Repeat("a", 64)

	rows, _, err := prepareExactWorkSnapshot(ExactWorkSnapshot{
		SourceWitness: witness,
		Rows:          []Bead{{ID: "gc-long-title", Title: fullTitle, CreatedAt: createdAt}},
	})
	if err != nil {
		t.Fatalf("prepareExactWorkSnapshot: %v", err)
	}
	if len(rows[0].Title) > 500 || !utf8.ValidString(rows[0].Title) {
		t.Fatalf("native title prefix is %d bytes, valid UTF-8=%v", len(rows[0].Title), utf8.ValidString(rows[0].Title))
	}
	if got := rows[0].Metadata["gc.work_migration_legacy_title"]; got != fullTitle {
		t.Fatalf("encoded legacy title = %q, want %q", got, fullTitle)
	}

	issue, err := nativeIssueFromBead(rows[0])
	if err != nil {
		t.Fatalf("nativeIssueFromBead: %v", err)
	}
	got, err := beadFromNativeIssue(issue)
	if err != nil {
		t.Fatalf("beadFromNativeIssue: %v", err)
	}
	if got.Title != fullTitle {
		t.Fatalf("reconstructed title = %q, want %q", got.Title, fullTitle)
	}
	if _, leaked := got.Metadata["gc.work_migration_legacy_title"]; leaked {
		t.Fatalf("legacy title representation leaked through metadata: %+v", got.Metadata)
	}
}

func TestPrepareExactWorkSnapshotEncodesEmptyTitleLosslessly(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	witness := "sha256:" + strings.Repeat("b", 64)

	rows, _, err := prepareExactWorkSnapshot(ExactWorkSnapshot{
		SourceWitness: witness,
		Rows:          []Bead{{ID: "gc-empty-title", CreatedAt: createdAt}},
	})
	if err != nil {
		t.Fatalf("prepareExactWorkSnapshot: %v", err)
	}
	if rows[0].Title != "gc-empty-title" {
		t.Fatalf("native title placeholder = %q, want row ID", rows[0].Title)
	}
	if encoded, ok := rows[0].Metadata["gc.work_migration_legacy_title"]; !ok || encoded != "" {
		t.Fatalf("encoded empty title = %q, present=%v", encoded, ok)
	}

	issue, err := nativeIssueFromBead(rows[0])
	if err != nil {
		t.Fatalf("nativeIssueFromBead: %v", err)
	}
	got, err := beadFromNativeIssue(issue)
	if err != nil {
		t.Fatalf("beadFromNativeIssue: %v", err)
	}
	if got.Title != "" {
		t.Fatalf("reconstructed title = %q, want empty", got.Title)
	}
	if _, leaked := got.Metadata["gc.work_migration_legacy_title"]; leaked {
		t.Fatalf("legacy title representation leaked through metadata: %+v", got.Metadata)
	}
}

func TestPrepareExactWorkSnapshotCanonicalizesSubsecondTimes(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 856235045, time.UTC)
	updatedAt := time.Date(2026, 8, 12, 11, 12, 13, 123456789, time.UTC)
	deferUntil := time.Date(2026, 8, 13, 12, 13, 14, 999999999, time.UTC)

	rows, _, err := prepareExactWorkSnapshot(ExactWorkSnapshot{
		SourceWitness: "sha256:" + strings.Repeat("c", 64),
		Rows: []Bead{{
			ID: "gc-subsecond", Title: "subsecond", CreatedAt: createdAt,
			UpdatedAt: updatedAt, DeferUntil: &deferUntil,
		}},
	})
	if err != nil {
		t.Fatalf("prepareExactWorkSnapshot: %v", err)
	}
	if !rows[0].CreatedAt.Equal(createdAt.Round(time.Second)) {
		t.Fatalf("created_at = %s, want %s", rows[0].CreatedAt, createdAt.Round(time.Second))
	}
	if !rows[0].UpdatedAt.Equal(updatedAt.Round(time.Second)) {
		t.Fatalf("updated_at = %s, want %s", rows[0].UpdatedAt, updatedAt.Round(time.Second))
	}
	if rows[0].DeferUntil == nil || !rows[0].DeferUntil.Equal(deferUntil.Round(time.Second)) {
		t.Fatalf("defer_until = %v, want %s", rows[0].DeferUntil, deferUntil.Round(time.Second))
	}
}

func TestNativeDoltStoreListFindsMigratedDanglingParent(t *testing.T) {
	parentID := "gc-missing"
	issues := []*beadslib.Issue{
		{
			ID: "gc-real-child", Title: "real edge", Status: beadslib.StatusOpen,
			IssueType: beadslib.TypeTask, Priority: 2,
			Dependencies: []*beadslib.Dependency{{
				IssueID: "gc-real-child", DependsOnID: parentID, Type: beadslib.DepParentChild,
			}},
		},
		{
			ID: "gc-legacy-child", Title: "legacy parent", Status: beadslib.StatusOpen,
			IssueType: beadslib.TypeTask, Priority: 2,
			Metadata: json.RawMessage(`{"gc.work_migration_legacy_parent":"gc-missing","gc.work_migration_source_witness":"sha256:1111111111111111111111111111111111111111111111111111111111111111"}`),
		},
	}
	storage := &nativeDoltStorageSpy{
		searchIssues: func(_ context.Context, _ string, filter beadslib.IssueFilter) ([]*beadslib.Issue, error) {
			var found []*beadslib.Issue
			for _, issue := range issues {
				matches := filter.ParentID != nil && issue.ID == "gc-real-child" && *filter.ParentID == parentID
				matches = matches || filter.MetadataFields[workMigrationLegacyParentMetadataKey] == parentID && issue.ID == "gc-legacy-child"
				if matches {
					found = append(found, cloneNativeIssueForTest(issue))
				}
			}
			return found, nil
		},
	}
	store := newNativeDoltStoreForTest(storage)

	got, err := store.List(ListQuery{ParentID: parentID, IncludeClosed: true})
	if err != nil {
		t.Fatalf("List by migrated parent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d rows, want both native and migrated-parent children: %+v", len(got), got)
	}
	gotIDs := []string{got[0].ID, got[1].ID}
	slices.Sort(gotIDs)
	if !slices.Equal(gotIDs, []string{"gc-legacy-child", "gc-real-child"}) {
		t.Fatalf("List IDs = %v, want both native and migrated-parent children", gotIDs)
	}
	for _, bead := range got {
		if bead.ParentID != parentID {
			t.Fatalf("bead %q parent = %q, want %q", bead.ID, bead.ParentID, parentID)
		}
	}
}

func TestExactWorkMigrationPolicyNamesEveryBeadField(t *testing.T) {
	policies := map[string]string{
		"ID": "preserve", "Title": "preserve", "Status": "preserve or normalize store default open", "Type": "preserve or normalize store default task",
		"Priority": "normalize native P2 default", "CreatedAt": "preserve",
		"UpdatedAt": "preserve or normalize legacy absence to created_at",
		"Assignee":  "preserve", "From": "preserve", "ParentID": "preserve directly or through reserved metadata",
		"Ref": "preserve", "Needs": "reject", "Description": "preserve", "Labels": "preserve",
		"Metadata": "preserve plus source witness", "Dependencies": "preserve", "Ephemeral": "reject",
		"NoHistory": "reject", "DeferUntil": "preserve", "IsBlocked": "derive from dependencies",
		"Revision": "destination-owned", "ClaimFence": "destination-owned",
	}
	beadType := reflect.TypeFor[Bead]()
	if len(policies) != beadType.NumField() {
		t.Fatalf("migration policies name %d fields, Bead has %d", len(policies), beadType.NumField())
	}
	for index := range beadType.NumField() {
		field := beadType.Field(index).Name
		if policies[field] == "" {
			t.Errorf("Bead.%s has no exact-work migration policy", field)
		}
	}
}

func TestPrepareExactWorkSnapshotNormalizesLegacyUpdatedAtBeforeStorage(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	rows, _, err := prepareExactWorkSnapshot(ExactWorkSnapshot{
		SourceWitness: "sha256:" + strings.Repeat("1", 64),
		Rows:          []Bead{{ID: "gc-legacy-time", CreatedAt: createdAt}},
	})
	if err != nil {
		t.Fatalf("prepareExactWorkSnapshot: %v", err)
	}
	if len(rows) != 1 || !rows[0].UpdatedAt.Equal(createdAt) {
		t.Fatalf("prepared updated_at = %v, want source created_at %s", rows[0].UpdatedAt, createdAt)
	}
	if rows[0].Status != "open" || rows[0].Type != "task" {
		t.Fatalf("prepared status/type = %q/%q, want store defaults open/task", rows[0].Status, rows[0].Type)
	}
}

func TestBeadFromNativeIssueRejectsInvalidLegacyParentRepresentation(t *testing.T) {
	parentDep := &beadslib.Dependency{IssueID: "gc-child", DependsOnID: "gc-real", Type: beadslib.DepParentChild}
	tests := []struct {
		name  string
		issue *beadslib.Issue
	}{
		{
			name: "missing source witness",
			issue: &beadslib.Issue{
				ID: "gc-child", Metadata: json.RawMessage(`{"gc.work_migration_legacy_parent":"gc-legacy"}`),
			},
		},
		{
			name: "legacy title missing source witness",
			issue: &beadslib.Issue{
				ID: "gc-child", Metadata: json.RawMessage(`{"gc.work_migration_legacy_title":"full legacy title"}`),
			},
		},
		{
			name: "conflicts with real parent",
			issue: &beadslib.Issue{
				ID: "gc-child", Dependencies: []*beadslib.Dependency{parentDep},
				Metadata: json.RawMessage(`{"gc.work_migration_legacy_parent":"gc-legacy","gc.work_migration_source_witness":"sha256:2222222222222222222222222222222222222222222222222222222222222222"}`),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := beadFromNativeIssue(test.issue); !errors.Is(err, ErrUnsupportedWorkMigrationShape) {
				t.Fatalf("beadFromNativeIssue error = %v, want ErrUnsupportedWorkMigrationShape", err)
			}
		})
	}
}

func TestNativeDoltStoreImportExactWorkSnapshotRefusesCollisionAtomically(t *testing.T) {
	store := newNativeDoltStoreForTest(newExactWorkMemStorage())
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	if _, err := store.Create(Bead{ID: "gc-existing", Title: "existing", CreatedAt: createdAt}); err != nil {
		t.Fatalf("seed collision: %v", err)
	}

	_, err := store.ImportExactWorkSnapshot(ExactWorkSnapshot{
		SourceWitness: "sha256:" + strings.Repeat("b", 64),
		Rows: []Bead{
			{ID: "gc-new", Title: "new", CreatedAt: createdAt},
			{ID: "gc-existing", Title: "replacement", CreatedAt: createdAt},
		},
	})
	if !errors.Is(err, ErrWorkMigrationCollision) {
		t.Fatalf("ImportExactWorkSnapshot error = %v, want ErrWorkMigrationCollision", err)
	}
	if _, err := store.Get("gc-new"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("atomic refusal left gc-new behind: %v", err)
	}
	existing, err := store.Get("gc-existing")
	if err != nil || existing.Title != "existing" {
		t.Fatalf("collision mutated existing row: %+v, %v", existing, err)
	}
}

func TestNativeDoltStoreImportExactWorkSnapshotRollsBackDependencyFailure(t *testing.T) {
	storage := newExactWorkMemStorage()
	storage.addDependencyErr = errors.New("injected dependency failure")
	store := newNativeDoltStoreForTest(storage)
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)

	_, err := store.ImportExactWorkSnapshot(ExactWorkSnapshot{
		SourceWitness: "sha256:" + strings.Repeat("f", 64),
		Rows: []Bead{
			{ID: "gc-a", Title: "a", CreatedAt: createdAt, Dependencies: []Dep{{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"}}},
			{ID: "gc-b", Title: "b", CreatedAt: createdAt},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "injected dependency failure") {
		t.Fatalf("ImportExactWorkSnapshot error = %v, want injected dependency failure", err)
	}
	for _, id := range []string{"gc-a", "gc-b"} {
		if _, err := store.Get(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("transaction failure left %s behind: %v", id, err)
		}
	}
}

func TestNativeDoltStoreImportExactWorkSnapshotRejectsUnsupportedSourceShape(t *testing.T) {
	store := newNativeDoltStoreForTest(newExactWorkMemStorage())
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	_, err := store.ImportExactWorkSnapshot(ExactWorkSnapshot{
		SourceWitness: "sha256:" + strings.Repeat("c", 64),
		Rows:          []Bead{{ID: "gc-wisp", Title: "wisp", CreatedAt: createdAt, NoHistory: true}},
	})
	if !errors.Is(err, ErrUnsupportedWorkMigrationShape) {
		t.Fatalf("ImportExactWorkSnapshot error = %v, want ErrUnsupportedWorkMigrationShape", err)
	}
	if _, err := store.Get("gc-wisp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsupported shape wrote a row: %v", err)
	}
}

func TestPrepareExactWorkSnapshotRejectsMalformedInputs(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 10, 11, 12, 0, time.UTC)
	witness := "sha256:" + strings.Repeat("d", 64)
	tests := []struct {
		name     string
		snapshot ExactWorkSnapshot
	}{
		{name: "bad witness", snapshot: ExactWorkSnapshot{SourceWitness: "sha256:XYZ", Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt}}}},
		{name: "no rows", snapshot: ExactWorkSnapshot{SourceWitness: witness}},
		{name: "missing id", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{CreatedAt: createdAt}}}},
		{name: "missing created at", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a"}}}},
		{name: "duplicate id", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt}, {ID: "gc-a", CreatedAt: createdAt}}}},
		{name: "unsupported status", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", Status: "deferred", CreatedAt: createdAt}}}},
		{name: "legacy needs", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt, Needs: []string{"gc-b"}}}}},
		{name: "reserved witness", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt, Metadata: StringMap{workMigrationSourceWitnessMetadataKey: "foreign"}}}}},
		{name: "reserved legacy parent", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt, Metadata: StringMap{workMigrationLegacyParentMetadataKey: "foreign"}}}}},
		{name: "wrong dependency owner", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt, Dependencies: []Dep{{IssueID: "gc-b", DependsOnID: "gc-a", Type: "blocks"}}}}}},
		{name: "empty dependency type", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt, Dependencies: []Dep{{IssueID: "gc-a", DependsOnID: "gc-b"}}}, {ID: "gc-b", CreatedAt: createdAt}}}},
		{name: "self dependency", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt, Dependencies: []Dep{{IssueID: "gc-a", DependsOnID: "gc-a", Type: "blocks"}}}}}},
		{name: "dependency leaves snapshot", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt, Dependencies: []Dep{{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"}}}}}},
		{name: "duplicate dependency pair", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt, Dependencies: []Dep{{IssueID: "gc-a", DependsOnID: "gc-b", Type: "blocks"}, {IssueID: "gc-a", DependsOnID: "gc-b", Type: "tracks"}}}, {ID: "gc-b", CreatedAt: createdAt}}}},
		{name: "retained parent lacks edge", snapshot: ExactWorkSnapshot{SourceWitness: witness, Rows: []Bead{{ID: "gc-a", CreatedAt: createdAt, ParentID: "gc-b"}, {ID: "gc-b", CreatedAt: createdAt}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := prepareExactWorkSnapshot(test.snapshot); !errors.Is(err, ErrUnsupportedWorkMigrationShape) {
				t.Fatalf("prepareExactWorkSnapshot error = %v, want ErrUnsupportedWorkMigrationShape", err)
			}
		})
	}
}

type exactWorkMemStorage struct {
	*nativeDoltMemStorage
	addDependencyErr error
}

func newExactWorkMemStorage() *exactWorkMemStorage {
	return &exactWorkMemStorage{nativeDoltMemStorage: newNativeDoltMemStorage()}
}

func (s *exactWorkMemStorage) RunInTransaction(_ context.Context, _ string, fn func(beadslib.Transaction) error) error {
	return runNativeDoltMemStorageTransactionForTest(s.nativeDoltMemStorage, func() error {
		return fn(nativeDoltTransactionForTest{storage: s})
	})
}

func (s *exactWorkMemStorage) CreateIssues(ctx context.Context, issues []*beadslib.Issue, actor string) error {
	for _, issue := range issues {
		if err := s.CreateIssue(ctx, issue, actor); err != nil {
			return err
		}
	}
	return nil
}

func (s *exactWorkMemStorage) AddDependency(ctx context.Context, dep *beadslib.Dependency, actor string) error {
	if s.addDependencyErr != nil {
		return s.addDependencyErr
	}
	return s.nativeDoltMemStorage.AddDependency(ctx, dep, actor)
}

func (s *exactWorkMemStorage) CreateIssue(_ context.Context, issue *beadslib.Issue, _ string) error {
	withoutDependencies := *issue
	withoutDependencies.Dependencies = nil
	storedMetadata, err := metadataMapFromNative(withoutDependencies.Metadata)
	if err != nil {
		return err
	}
	bead, err := beadFromNativeIssue(&withoutDependencies)
	if err != nil {
		return err
	}
	if legacyParent := storedMetadata[workMigrationLegacyParentMetadataKey]; legacyParent != "" {
		bead.ParentID = ""
		bead.Metadata[workMigrationLegacyParentMetadataKey] = legacyParent
	}
	if bead.Status == "" {
		bead.Status = "open"
	}
	if bead.Type == "" {
		bead.Type = "task"
	}
	if bead.UpdatedAt.IsZero() {
		bead.UpdatedAt = bead.CreatedAt
	}
	bead.Revision = 1

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if s.store.beadExistsLocked(bead.ID) {
		return errors.New("duplicate id")
	}
	s.store.beads = append(s.store.beads, cloneBead(bead))
	converted, err := nativeIssueFromBead(bead)
	if err != nil {
		return err
	}
	*issue = *converted
	return nil
}
