package beads

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/fsys"
)

func stringPointer(value string) *string { return &value }

func newRecordMigrationFileStore(t *testing.T) *FileStore {
	t.Helper()
	store, err := OpenFileStore(fsys.OSFS{}, t.TempDir()+"/beads.json")
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	return store
}

func TestMigrateFileRecordAppliesGuardedChangesAndAppendsAuditHistory(t *testing.T) {
	store := newRecordMigrationFileStore(t)
	created, err := store.Create(Bead{
		Title:    "historical message",
		Type:     "message",
		Assignee: "retired-session",
		Metadata: StringMap{beadmeta.RoutedToMetadataKey: "retired-route"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	now := time.Date(2026, 8, 5, 14, 30, 0, 0, time.FixedZone("offset", -4*60*60))

	got, err := MigrateFileRecord(store, RecordMigrationRequest{
		ID:               created.ID,
		Actor:            "operator@example",
		Reason:           "approved session-model reconciliation",
		ExpectedAssignee: stringPointer("retired-session"),
		Assignee:         stringPointer("current-session"),
		ExpectedMetadata: map[string]string{beadmeta.RoutedToMetadataKey: "retired-route"},
		ClearMetadata:    []string{beadmeta.RoutedToMetadataKey},
	}, now)
	if err != nil {
		t.Fatalf("MigrateFileRecord: %v", err)
	}
	if got.Assignee != "current-session" {
		t.Fatalf("Assignee = %q, want current-session", got.Assignee)
	}
	if _, present := got.Metadata[beadmeta.RoutedToMetadataKey]; present {
		t.Fatalf("route remains present after clear: %#v", got.Metadata)
	}

	var history []recordMigrationAuditEntry
	if err := json.Unmarshal([]byte(got.Metadata[beadmeta.MigrationHistoryMetadataKey]), &history); err != nil {
		t.Fatalf("decode migration history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %#v, want one entry", history)
	}
	entry := history[0]
	if entry.At != "2026-08-05T18:30:00Z" || entry.Actor != "operator@example" || entry.Reason != "approved session-model reconciliation" {
		t.Fatalf("audit entry = %#v", entry)
	}
	if entry.Assignee == nil || entry.Assignee.Before != "retired-session" || entry.Assignee.After != "current-session" {
		t.Fatalf("assignee audit = %#v", entry.Assignee)
	}
	if len(entry.Metadata) != 1 || entry.Metadata[0].Key != beadmeta.RoutedToMetadataKey || entry.Metadata[0].Before != "retired-route" || !entry.Metadata[0].BeforePresent || entry.Metadata[0].After != "" || entry.Metadata[0].AfterPresent {
		t.Fatalf("metadata audit = %#v", entry.Metadata)
	}
}

func TestMigrateFileRecordRejectsStaleExpectedValueWithoutMutation(t *testing.T) {
	store := newRecordMigrationFileStore(t)
	created, err := store.Create(Bead{Title: "task", Assignee: "actual-owner"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = MigrateFileRecord(store, RecordMigrationRequest{
		ID:               created.ID,
		Actor:            "operator@example",
		Reason:           "reconcile owner",
		ExpectedAssignee: stringPointer("stale-owner"),
		Assignee:         stringPointer("new-owner"),
	}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "assignee changed") {
		t.Fatalf("MigrateRecord error = %v, want assignee precondition failure", err)
	}
	got, getErr := store.Get(created.ID)
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if got.Assignee != "actual-owner" || got.Metadata[beadmeta.MigrationHistoryMetadataKey] != "" {
		t.Fatalf("stale migration mutated bead: %#v", got)
	}
}

func TestMigrateFileRecordRequiresAnExpectedValueForEveryChange(t *testing.T) {
	store := newRecordMigrationFileStore(t)
	created, err := store.Create(Bead{Title: "task"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = MigrateFileRecord(store, RecordMigrationRequest{
		ID:       created.ID,
		Actor:    "operator@example",
		Reason:   "reconcile route",
		Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "new-route"},
	}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "expected value") {
		t.Fatalf("MigrateRecord error = %v, want missing expected-value error", err)
	}
}

func TestMigrateFileRecordRejectsInvalidRequestsBeforeWriting(t *testing.T) {
	store := newRecordMigrationFileStore(t)
	created, err := store.Create(Bead{Title: "task", Assignee: "old"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	now := time.Now()
	base := func() RecordMigrationRequest {
		return RecordMigrationRequest{
			ID:               created.ID,
			Actor:            "operator@example",
			Reason:           "reviewed correction",
			ExpectedAssignee: stringPointer("old"),
			Assignee:         stringPointer("new"),
		}
	}
	cases := []struct {
		name  string
		store *FileStore
		req   RecordMigrationRequest
		now   time.Time
		want  string
	}{
		{name: "nil store", req: base(), now: now, want: "nil store"},
		{name: "missing id", store: store, req: func() RecordMigrationRequest { r := base(); r.ID = ""; return r }(), now: now, want: "id is required"},
		{name: "missing actor", store: store, req: func() RecordMigrationRequest { r := base(); r.Actor = " "; return r }(), now: now, want: "actor is required"},
		{name: "missing reason", store: store, req: func() RecordMigrationRequest { r := base(); r.Reason = " "; return r }(), now: now, want: "reason is required"},
		{name: "missing timestamp", store: store, req: base(), want: "timestamp is required"},
		{name: "unpaired assignee", store: store, req: func() RecordMigrationRequest { r := base(); r.Assignee = nil; return r }(), now: now, want: "requires both"},
		{name: "unchanged assignee", store: store, req: func() RecordMigrationRequest { r := base(); r.Assignee = stringPointer("old"); return r }(), now: now, want: "equals expected"},
		{name: "no changes", store: store, req: func() RecordMigrationRequest { r := base(); r.ExpectedAssignee = nil; r.Assignee = nil; return r }(), now: now, want: "no changes"},
		{name: "empty absent key", store: store, req: func() RecordMigrationRequest { r := base(); r.ExpectedMetadataAbsent = []string{" "}; return r }(), now: now, want: "key is empty"},
		{name: "reserved absent key", store: store, req: func() RecordMigrationRequest {
			r := base()
			r.ExpectedMetadataAbsent = []string{beadmeta.MigrationHistoryMetadataKey}
			return r
		}(), now: now, want: "maintained by"},
		{name: "duplicate absent key", store: store, req: func() RecordMigrationRequest { r := base(); r.ExpectedMetadataAbsent = []string{"k", "k"}; return r }(), now: now, want: "duplicates"},
		{name: "present and absent", store: store, req: func() RecordMigrationRequest {
			r := base()
			r.ExpectedMetadata = map[string]string{"k": "v"}
			r.ExpectedMetadataAbsent = []string{"k"}
			return r
		}(), now: now, want: "both expected present and expected absent"},
		{name: "empty metadata key", store: store, req: func() RecordMigrationRequest { r := base(); r.Metadata = map[string]string{" ": "v"}; return r }(), now: now, want: "metadata key is empty"},
		{name: "reserved metadata key", store: store, req: func() RecordMigrationRequest {
			r := base()
			r.Metadata = map[string]string{beadmeta.MigrationHistoryMetadataKey: "v"}
			return r
		}(), now: now, want: "maintained by"},
		{name: "unchanged metadata", store: store, req: func() RecordMigrationRequest {
			r := base()
			r.ExpectedMetadata = map[string]string{"k": "v"}
			r.Metadata = map[string]string{"k": "v"}
			return r
		}(), now: now, want: "replacement equals expected"},
		{name: "empty clear key", store: store, req: func() RecordMigrationRequest { r := base(); r.ClearMetadata = []string{" "}; return r }(), now: now, want: "metadata key is empty"},
		{name: "duplicate clear", store: store, req: func() RecordMigrationRequest {
			r := base()
			r.ExpectedMetadata = map[string]string{"k": "old"}
			r.ClearMetadata = []string{"k", "k"}
			return r
		}(), now: now, want: "duplicates"},
		{name: "set and clear", store: store, req: func() RecordMigrationRequest {
			r := base()
			r.ExpectedMetadata = map[string]string{"k": "old"}
			r.Metadata = map[string]string{"k": "new"}
			r.ClearMetadata = []string{"k"}
			return r
		}(), now: now, want: "both set and cleared"},
		{name: "expected without replacement", store: store, req: func() RecordMigrationRequest {
			r := base()
			r.ExpectedMetadata = map[string]string{"k": "old"}
			return r
		}(), now: now, want: "no replacement"},
		{name: "absent without replacement", store: store, req: func() RecordMigrationRequest { r := base(); r.ExpectedMetadataAbsent = []string{"k"}; return r }(), now: now, want: "no replacement"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := MigrateFileRecord(tc.store, tc.req, tc.now)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("MigrateFileRecord error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestMigrateFileRecordDistinguishesAbsentMetadataFromEmptyMetadata(t *testing.T) {
	store := newRecordMigrationFileStore(t)
	created, err := store.Create(Bead{Title: "task"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = MigrateFileRecord(store, RecordMigrationRequest{
		ID:     created.ID,
		Actor:  "operator@example",
		Reason: "add reviewed marker",
		ExpectedMetadata: map[string]string{
			"reviewed": "",
		},
		Metadata: map[string]string{"reviewed": "true"},
	}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "presence changed") {
		t.Fatalf("MigrateFileRecord error = %v, want metadata-presence precondition failure", err)
	}

	got, err := MigrateFileRecord(store, RecordMigrationRequest{
		ID:                     created.ID,
		Actor:                  "operator@example",
		Reason:                 "add reviewed marker",
		ExpectedMetadataAbsent: []string{"reviewed"},
		Metadata:               map[string]string{"reviewed": "true"},
	}, time.Now())
	if err != nil {
		t.Fatalf("MigrateFileRecord with absent guard: %v", err)
	}
	if got.Metadata["reviewed"] != "true" {
		t.Fatalf("reviewed metadata = %q, want true", got.Metadata["reviewed"])
	}
}

func TestMigrateFileRecordRejectsClearingMissingMetadata(t *testing.T) {
	store := newRecordMigrationFileStore(t)
	created, err := store.Create(Bead{Title: "task"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = MigrateFileRecord(store, RecordMigrationRequest{
		ID:               created.ID,
		Actor:            "operator@example",
		Reason:           "reconcile route",
		ExpectedMetadata: map[string]string{beadmeta.RoutedToMetadataKey: ""},
		ClearMetadata:    []string{beadmeta.RoutedToMetadataKey},
	}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "presence changed") {
		t.Fatalf("MigrateFileRecord error = %v, want no-op clear rejection", err)
	}
	got, getErr := store.Get(created.ID)
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if got.Metadata[beadmeta.MigrationHistoryMetadataKey] != "" {
		t.Fatalf("no-op migration wrote audit history: %#v", got.Metadata)
	}
}

func TestMigrateFileRecordPersistsThroughFileStoreAndPreservesAuditHistory(t *testing.T) {
	path := t.TempDir() + "/beads.json"
	store, err := OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	created, err := store.Create(Bead{Title: "historical message", Type: "message", Assignee: "old"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	first, err := MigrateFileRecord(store, RecordMigrationRequest{
		ID:               created.ID,
		Actor:            "operator-1",
		Reason:           "first migration",
		ExpectedAssignee: stringPointer("old"),
		Assignee:         stringPointer("middle"),
	}, time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("first MigrateRecord: %v", err)
	}
	if _, err := MigrateFileRecord(store, RecordMigrationRequest{
		ID:               created.ID,
		Actor:            "operator-2",
		Reason:           "second migration",
		ExpectedAssignee: stringPointer("middle"),
		Assignee:         stringPointer("new"),
	}, time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("second MigrateRecord: %v", err)
	}
	if first.Metadata[beadmeta.MigrationHistoryMetadataKey] == "" {
		t.Fatal("first migration did not write audit history")
	}

	reopened, err := OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatalf("reopen FileStore: %v", err)
	}
	got, err := reopened.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	var history []recordMigrationAuditEntry
	if err := json.Unmarshal([]byte(got.Metadata[beadmeta.MigrationHistoryMetadataKey]), &history); err != nil {
		t.Fatalf("decode migration history: %v", err)
	}
	if got.Assignee != "new" || len(history) != 2 || history[0].Reason != "first migration" || history[1].Reason != "second migration" {
		t.Fatalf("persisted bead = %#v, history = %#v", got, history)
	}
}

func TestMigrateFileRecordFailsClosedOnMalformedAuditHistory(t *testing.T) {
	store := newRecordMigrationFileStore(t)
	created, err := store.Create(Bead{
		Title:    "task",
		Assignee: "old",
		Metadata: StringMap{beadmeta.MigrationHistoryMetadataKey: "not-json"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = MigrateFileRecord(store, RecordMigrationRequest{
		ID:               created.ID,
		Actor:            "operator@example",
		Reason:           "reconcile owner",
		ExpectedAssignee: stringPointer("old"),
		Assignee:         stringPointer("new"),
	}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "migration history") {
		t.Fatalf("MigrateRecord error = %v, want malformed-history error", err)
	}
}
