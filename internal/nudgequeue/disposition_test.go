package nudgequeue

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/fsys"
)

type staleDispositionReadStore struct {
	*beads.MemStore
}

func (s *staleDispositionReadStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	items, err := s.MemStore.List(query)
	if err != nil {
		return nil, err
	}
	for i := range items {
		delete(items[i].Metadata, "operator_disposition")
	}
	return items, nil
}

func TestRecordOperatorDispositionPersistsOnClosedFileShadow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "beads.json")
	fileStore, err := beads.OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	store := NewStore(beads.NudgesStore{Store: fileStore})
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	item := Item{
		ID:           "nudge-disposition-file",
		Agent:        "worker",
		Source:       "session",
		Message:      "review the release",
		CreatedAt:    now.Add(-time.Hour),
		DeliverAfter: now.Add(-time.Hour),
		ExpiresAt:    now.Add(-time.Minute),
		LastError:    "expired",
	}
	beadID, _, err := store.Save(item)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	item.BeadID = beadID
	if err := store.Terminalize(item, "expired", "expired", "", now); err != nil {
		t.Fatalf("Terminalize: %v", err)
	}
	if _, err := store.RecordOperatorDisposition(item.ID, "retried", "work still required", "operator", "nudge-retry-1", "gc-session-2", now.Add(time.Minute)); err != nil {
		t.Fatalf("RecordOperatorDisposition: %v", err)
	}

	reopened, err := beads.OpenFileStore(fsys.OSFS{}, path)
	if err != nil {
		t.Fatalf("reopen FileStore: %v", err)
	}
	shadow, ok, err := NewStore(beads.NudgesStore{Store: reopened}).FindIncludingTerminal(item.ID)
	if err != nil || !ok {
		t.Fatalf("FindIncludingTerminal = %+v, %t, %v", shadow, ok, err)
	}
	if shadow.Open || shadow.State != "expired" || shadow.TerminalReason != "expired" {
		t.Fatalf("delivery terminal state changed: %+v", shadow)
	}
	if shadow.OperatorDisposition != "retried" || shadow.OperatorReason != "work still required" ||
		shadow.OperatorActor != "operator" || shadow.RetryNudgeID != "nudge-retry-1" || shadow.RetryTarget != "gc-session-2" {
		t.Fatalf("operator disposition did not persist: %+v", shadow)
	}
}

func TestRecordOperatorDispositionRejectsOpenShadow(t *testing.T) {
	store := NewStore(beads.NudgesStore{Store: beads.NewMemStore()})
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	item := Item{ID: "nudge-open", Agent: "worker", Source: "session", Message: "work", CreatedAt: now, DeliverAfter: now, ExpiresAt: now.Add(time.Hour)}
	if _, _, err := store.Save(item); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := store.RecordOperatorDisposition(item.ID, "dismissed", "obsolete", "operator", "", "", now); err == nil {
		t.Fatal("RecordOperatorDisposition accepted an open delivery shadow")
	}
}

func TestRecordOperatorDispositionRejectsUnverifiedWrite(t *testing.T) {
	base := beads.NewMemStore()
	store := NewStore(beads.NudgesStore{Store: &staleDispositionReadStore{MemStore: base}})
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	item := Item{
		ID:           "nudge-stale-audit-read",
		Agent:        "worker",
		Source:       "session",
		Message:      "work",
		CreatedAt:    now.Add(-time.Hour),
		DeliverAfter: now.Add(-time.Hour),
		ExpiresAt:    now.Add(-time.Minute),
		LastError:    "expired",
	}
	beadID, _, err := store.Save(item)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	item.BeadID = beadID
	if err := store.Terminalize(item, "expired", "expired", "", now); err != nil {
		t.Fatalf("Terminalize: %v", err)
	}

	_, err = store.RecordOperatorDisposition(item.ID, "dismissed", "obsolete", "operator", "", "", now.Add(time.Minute))
	if err == nil || !strings.Contains(err.Error(), "authoritative shadow did not match the write") {
		t.Fatalf("error = %v, want failed authoritative read-back verification", err)
	}
}
