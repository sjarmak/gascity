package beads

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// RecordMigrationRequest describes one explicitly guarded record migration.
// Every changed field must carry the value the operator observed before the
// migration, so stale evidence fails without writing.
type RecordMigrationRequest struct {
	ID               string
	Actor            string
	Reason           string
	ExpectedAssignee *string
	Assignee         *string
	ExpectedMetadata map[string]string
	// ExpectedMetadataAbsent lists keys that must not exist before a
	// replacement is applied. This distinguishes absence from an empty value.
	ExpectedMetadataAbsent []string
	Metadata               map[string]string
	ClearMetadata          []string
}

type recordMigrationAuditEntry struct {
	At       string                          `json:"at"`
	Actor    string                          `json:"actor"`
	Reason   string                          `json:"reason"`
	Assignee *recordMigrationAssigneeChange  `json:"assignee,omitempty"`
	Metadata []recordMigrationMetadataChange `json:"metadata,omitempty"`
}

type recordMigrationAssigneeChange struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type recordMigrationMetadataChange struct {
	Key           string `json:"key"`
	Before        string `json:"before"`
	BeforePresent bool   `json:"before_present"`
	After         string `json:"after"`
	AfterPresent  bool   `json:"after_present"`
}

// MigrateFileRecord conditionally applies one file-provider record's
// operator-approved changes and appends their provenance to
// gc.migration_history in the same locked file write.
func MigrateFileRecord(store *FileStore, req RecordMigrationRequest, now time.Time) (Bead, error) {
	if store == nil {
		return Bead{}, errors.New("migrating record: nil store")
	}
	if strings.TrimSpace(req.ID) == "" {
		return Bead{}, errors.New("migrating record: id is required")
	}
	if strings.TrimSpace(req.Actor) == "" {
		return Bead{}, errors.New("migrating record: actor is required")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return Bead{}, errors.New("migrating record: reason is required")
	}
	if now.IsZero() {
		return Bead{}, errors.New("migrating record: timestamp is required")
	}
	if (req.ExpectedAssignee == nil) != (req.Assignee == nil) {
		return Bead{}, errors.New("migrating record: assignee change requires both expected and replacement values")
	}
	if req.Assignee != nil && *req.Assignee == *req.ExpectedAssignee {
		return Bead{}, errors.New("migrating record: assignee replacement equals expected value")
	}
	if len(req.Metadata) == 0 && len(req.ClearMetadata) == 0 && req.Assignee == nil {
		return Bead{}, errors.New("migrating record: no changes requested")
	}
	expectedAbsent := make(map[string]bool, len(req.ExpectedMetadataAbsent))
	for _, key := range req.ExpectedMetadataAbsent {
		if strings.TrimSpace(key) == "" {
			return Bead{}, errors.New("migrating record: expected-absent metadata key is empty")
		}
		if key == beadmeta.MigrationHistoryMetadataKey {
			return Bead{}, fmt.Errorf("migrating record: %s is maintained by the migration operation", key)
		}
		if expectedAbsent[key] {
			return Bead{}, fmt.Errorf("migrating record: expected-absent metadata duplicates key %q", key)
		}
		if _, present := req.ExpectedMetadata[key]; present {
			return Bead{}, fmt.Errorf("migrating record: metadata %q cannot be both expected present and expected absent", key)
		}
		expectedAbsent[key] = true
	}
	for key := range req.Metadata {
		if strings.TrimSpace(key) == "" {
			return Bead{}, errors.New("migrating record: metadata key is empty")
		}
		if key == beadmeta.MigrationHistoryMetadataKey {
			return Bead{}, fmt.Errorf("migrating record: %s is maintained by the migration operation", key)
		}
		if _, ok := req.ExpectedMetadata[key]; !ok && !expectedAbsent[key] {
			return Bead{}, fmt.Errorf("migrating record: metadata %q has no expected value", key)
		}
		if expected, present := req.ExpectedMetadata[key]; present && req.Metadata[key] == expected {
			return Bead{}, fmt.Errorf("migrating record: metadata %q replacement equals expected value", key)
		}
	}
	clearKeys := make(map[string]bool, len(req.ClearMetadata))
	for _, key := range req.ClearMetadata {
		if strings.TrimSpace(key) == "" {
			return Bead{}, errors.New("migrating record: metadata key is empty")
		}
		if key == beadmeta.MigrationHistoryMetadataKey {
			return Bead{}, fmt.Errorf("migrating record: %s is maintained by the migration operation", key)
		}
		if clearKeys[key] {
			return Bead{}, fmt.Errorf("migrating record: metadata clear duplicates key %q", key)
		}
		if _, set := req.Metadata[key]; set {
			return Bead{}, fmt.Errorf("migrating record: metadata %q cannot be both set and cleared", key)
		}
		if _, ok := req.ExpectedMetadata[key]; !ok {
			return Bead{}, fmt.Errorf("migrating record: metadata %q has no expected value", key)
		}
		clearKeys[key] = true
	}
	for key := range req.ExpectedMetadata {
		_, set := req.Metadata[key]
		if !set && !clearKeys[key] {
			return Bead{}, fmt.Errorf("migrating record: metadata %q has an expected value but no replacement", key)
		}
	}
	for key := range expectedAbsent {
		if _, set := req.Metadata[key]; !set {
			return Bead{}, fmt.Errorf("migrating record: expected-absent metadata %q has no replacement", key)
		}
	}

	store.fmu.Lock()
	defer store.fmu.Unlock()
	if err := store.locker.Lock(); err != nil {
		return Bead{}, fmt.Errorf("migrating record %q: locking file store: %w", req.ID, err)
	}
	defer store.locker.Unlock() //nolint:errcheck // best-effort unlock
	if err := store.reloadFromDisk(); err != nil {
		return Bead{}, fmt.Errorf("migrating record %q: reloading file store: %w", req.ID, err)
	}
	current, err := store.MemStore.Get(req.ID)
	if err != nil {
		return Bead{}, fmt.Errorf("migrating record %q: %w", req.ID, err)
	}
	if req.ExpectedAssignee != nil && current.Assignee != *req.ExpectedAssignee {
		return Bead{}, fmt.Errorf("migrating record %q: assignee changed from expected %q to %q", req.ID, *req.ExpectedAssignee, current.Assignee)
	}
	for key, expected := range req.ExpectedMetadata {
		actual, present := current.Metadata[key]
		if !present {
			return Bead{}, fmt.Errorf("migrating record %q: metadata %q presence changed: expected present, found absent", req.ID, key)
		}
		if actual != expected {
			return Bead{}, fmt.Errorf("migrating record %q: metadata %q changed from expected %q to %q", req.ID, key, expected, actual)
		}
	}
	for key := range expectedAbsent {
		if actual, present := current.Metadata[key]; present {
			return Bead{}, fmt.Errorf("migrating record %q: metadata %q presence changed: expected absent, found %q", req.ID, key, actual)
		}
	}

	history, err := decodeRecordMigrationHistory(current.Metadata[beadmeta.MigrationHistoryMetadataKey])
	if err != nil {
		return Bead{}, fmt.Errorf("migrating record %q: %w", req.ID, err)
	}
	entry := recordMigrationAuditEntry{
		At:     now.UTC().Format(time.RFC3339Nano),
		Actor:  strings.TrimSpace(req.Actor),
		Reason: strings.TrimSpace(req.Reason),
	}
	if req.Assignee != nil {
		entry.Assignee = &recordMigrationAssigneeChange{Before: current.Assignee, After: *req.Assignee}
	}
	keys := make([]string, 0, len(req.Metadata))
	for key := range req.Metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		before, beforePresent := current.Metadata[key]
		entry.Metadata = append(entry.Metadata, recordMigrationMetadataChange{
			Key: key, Before: before, BeforePresent: beforePresent, After: req.Metadata[key], AfterPresent: true,
		})
	}
	for _, key := range req.ClearMetadata {
		entry.Metadata = append(entry.Metadata, recordMigrationMetadataChange{
			Key: key, Before: current.Metadata[key], BeforePresent: true, After: "", AfterPresent: false,
		})
	}
	sort.Slice(entry.Metadata, func(i, j int) bool { return entry.Metadata[i].Key < entry.Metadata[j].Key })
	history = append(history, entry)
	historyJSON, err := json.Marshal(history)
	if err != nil {
		return Bead{}, fmt.Errorf("migrating record %q: encoding migration history: %w", req.ID, err)
	}

	metadata := make(map[string]string, len(req.Metadata)+1)
	for key, value := range req.Metadata {
		metadata[key] = value
	}
	metadata[beadmeta.MigrationHistoryMetadataKey] = string(historyJSON)

	snapshot := store.snapshotLocked()
	store.mu.Lock()
	index := store.indexOfLocked(req.ID)
	if index < 0 {
		store.mu.Unlock()
		return Bead{}, fmt.Errorf("migrating record %q: %w", req.ID, ErrNotFound)
	}
	if store.MemStore.beads[index].Revision != current.Revision {
		actualRevision := store.MemStore.beads[index].Revision
		store.mu.Unlock()
		return Bead{}, &PreconditionFailedError{ID: req.ID, Expected: current.Revision, Current: actualRevision}
	}
	store.applyUpdateLocked(index, UpdateOpts{Assignee: req.Assignee, Metadata: metadata})
	for key := range clearKeys {
		delete(store.MemStore.beads[index].Metadata, key)
	}
	updated := cloneBead(store.beads[index])
	store.mu.Unlock()
	if err := store.save(); err != nil {
		store.restoreFrom(snapshot.seq, snapshot.beads, snapshot.deps)
		return Bead{}, fmt.Errorf("migrating record %q: saving file store: %w", req.ID, err)
	}
	return updated, nil
}

func decodeRecordMigrationHistory(raw string) ([]recordMigrationAuditEntry, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var history []recordMigrationAuditEntry
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&history); err != nil {
		return nil, fmt.Errorf("decoding migration history: %w", err)
	}
	if history == nil {
		return nil, errors.New("decoding migration history: expected an array")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("decoding migration history: trailing data")
	}
	return history, nil
}
