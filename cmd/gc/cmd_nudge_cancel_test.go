package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/nudgequeue"
)

type dispositionFailOnceStore struct {
	*beads.MemStore
	failed bool
}

type emptyCreateStore struct {
	*beads.MemStore
}

func (s *emptyCreateStore) Create(beads.Bead) (beads.Bead, error) {
	return beads.Bead{}, nil
}

type hideListCallStore struct {
	*beads.MemStore
	hideAt    int
	listCalls int
}

func (s *hideListCallStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	s.listCalls++
	if s.listCalls >= s.hideAt {
		return nil, nil
	}
	return s.MemStore.List(query)
}

func (s *dispositionFailOnceStore) SetMetadataBatch(id string, kvs map[string]string) error {
	if kvs["operator_disposition"] != "" && !s.failed {
		s.failed = true
		return errors.New("transient audit failure")
	}
	return s.MemStore.SetMetadataBatch(id, kvs)
}

func seedPendingNudge(t *testing.T, cityPath string, items ...queuedNudge) {
	t.Helper()
	if err := nudgequeue.WithState(cityPath, func(state *nudgequeue.State) error {
		state.Pending = append(state.Pending, items...)
		return nil
	}); err != nil {
		t.Fatalf("seed pending nudge: %v", err)
	}
}

func TestCancelPendingQueuedNudgeCreatesVerifiedReceiptBeforeRemoval(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	now := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)
	pending := newQueuedNudgeWithOptions("worker", "obsolete direction", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-pending-cancel"})
	seedPendingNudge(t, cityPath, pending)

	got, err := cancelPendingQueuedNudge(cityPath, store, pending.ID, "superseded by gc-123", "goal-1-dispatch", now)
	if err != nil {
		t.Fatalf("cancelPendingQueuedNudge: %v", err)
	}
	if got.Command != "nudge cancel" || got.Disposition != "canceled" || got.NudgeID != pending.ID || got.TerminalBeadID == "" {
		t.Fatalf("result = %+v, want canceled receipt", got)
	}

	state, err := nudgequeue.LoadState(cityPath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Pending) != 0 {
		t.Fatalf("pending = %+v, want selected entry removed", state.Pending)
	}
	shadow, ok, err := nudgeFrontDoor(store).FindIncludingTerminal(pending.ID)
	if err != nil || !ok {
		t.Fatalf("FindIncludingTerminal = %+v, %t, %v", shadow, ok, err)
	}
	if shadow.Open || shadow.State != "canceled" || !nudgequeue.IsTerminalState(shadow.State) {
		t.Fatalf("shadow = %+v, want terminal canceled receipt", shadow)
	}
	if shadow.OperatorDisposition != "canceled" || shadow.OperatorReason != "superseded by gc-123" || shadow.OperatorActor != "goal-1-dispatch" {
		t.Fatalf("operator audit = %+v, want exact cancellation", shadow)
	}

	again, err := cancelPendingQueuedNudge(cityPath, store, pending.ID, "superseded by gc-123", "goal-1-dispatch", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("idempotent cancel: %v", err)
	}
	if again != got {
		t.Fatalf("idempotent result = %+v, want %+v", again, got)
	}
	if _, err := cancelPendingQueuedNudge(cityPath, store, pending.ID, "different reason", "goal-1-dispatch", now.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "different audit inputs") {
		t.Fatalf("changed idempotency inputs error = %v, want loud conflict", err)
	}
}

func TestCancelPendingQueuedNudgeRetainsPendingEntryWhenAuditWriteFails(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NudgesStore{Store: &dispositionFailStore{MemStore: beads.NewMemStore()}}
	now := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)
	pending := newQueuedNudgeWithOptions("worker", "still queued", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-pending-audit-fail"})
	seedPendingNudge(t, cityPath, pending)

	_, err := cancelPendingQueuedNudge(cityPath, store, pending.ID, "obsolete", "goal-1-dispatch", now)
	if err == nil || !strings.Contains(err.Error(), "audit store unavailable") {
		t.Fatalf("error = %v, want audit write failure", err)
	}
	state, loadErr := nudgequeue.LoadState(cityPath)
	if loadErr != nil {
		t.Fatalf("LoadState: %v", loadErr)
	}
	if len(state.Pending) != 1 || state.Pending[0].ID != pending.ID {
		t.Fatalf("pending = %+v, want entry retained until audit is durable", state.Pending)
	}
}

func TestCancelPendingQueuedNudgeResumesAfterPartialAuditFailure(t *testing.T) {
	cityPath := t.TempDir()
	base := &dispositionFailOnceStore{MemStore: beads.NewMemStore()}
	store := beads.NudgesStore{Store: base}
	now := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)
	pending := newQueuedNudgeWithOptions("worker", "still queued", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-pending-audit-retry"})
	seedPendingNudge(t, cityPath, pending)

	if _, err := cancelPendingQueuedNudge(cityPath, store, pending.ID, "obsolete", "goal-1-dispatch", now); err == nil || !strings.Contains(err.Error(), "transient audit failure") {
		t.Fatalf("first error = %v, want transient audit failure", err)
	}
	got, err := cancelPendingQueuedNudge(cityPath, store, pending.ID, "obsolete", "goal-1-dispatch", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("resume cancellation: %v", err)
	}
	if got.Disposition != "canceled" {
		t.Fatalf("result = %+v, want resumed cancellation", got)
	}
	state, loadErr := nudgequeue.LoadState(cityPath)
	if loadErr != nil || len(state.Pending) != 0 {
		t.Fatalf("pending/load error = %+v/%v, want removed after verified retry", state.Pending, loadErr)
	}
}

func TestCancelPendingQueuedNudgeWritesNoAuditBeforeTerminalReceipt(t *testing.T) {
	cityPath := t.TempDir()
	base := beads.NewMemStore()
	store := beads.NudgesStore{Store: &terminalizeFailStore{MemStore: base}}
	now := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)
	pending := newQueuedNudgeWithOptions("worker", "still queued", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-pending-terminal-fail"})
	seedPendingNudge(t, cityPath, pending)

	_, err := cancelPendingQueuedNudge(cityPath, store, pending.ID, "obsolete", "goal-1-dispatch", now)
	if err == nil || !strings.Contains(err.Error(), "terminal receipt unavailable") {
		t.Fatalf("error = %v, want terminal receipt failure", err)
	}
	state, loadErr := nudgequeue.LoadState(cityPath)
	if loadErr != nil || len(state.Pending) != 1 {
		t.Fatalf("pending/load error = %+v/%v, want entry retained", state.Pending, loadErr)
	}
	shadow, ok, findErr := nudgeFrontDoor(store).FindIncludingTerminal(pending.ID)
	if findErr != nil || !ok {
		t.Fatalf("FindIncludingTerminal = %+v, %t, %v", shadow, ok, findErr)
	}
	bead, getErr := base.Get(shadow.BeadID)
	if getErr != nil {
		t.Fatalf("Get receipt bead: %v", getErr)
	}
	if bead.Metadata["operator_disposition"] != "" {
		t.Fatalf("operator audit written before terminal receipt: %+v", bead.Metadata)
	}
}

func TestCancelPendingQueuedNudgeReportsMissingReceiptWithoutFormattingArtifacts(t *testing.T) {
	now := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		store     beads.Store
		wantError string
	}{
		{
			name:      "empty create result",
			store:     &emptyCreateStore{MemStore: beads.NewMemStore()},
			wantError: "audit store returned no id",
		},
		{
			name:      "missing after create",
			store:     &hideListCallStore{MemStore: beads.NewMemStore(), hideAt: 3},
			wantError: "not found after create",
		},
		{
			name:      "missing final verification",
			store:     &hideListCallStore{MemStore: beads.NewMemStore(), hideAt: 4},
			wantError: "not found after terminalization",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cityPath := t.TempDir()
			pending := newQueuedNudgeWithOptions("worker", "still queued", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-missing-receipt"})
			seedPendingNudge(t, cityPath, pending)
			_, err := cancelPendingQueuedNudge(cityPath, beads.NudgesStore{Store: tc.store}, pending.ID, "obsolete", "goal-1-dispatch", now)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) || strings.Contains(err.Error(), "%!w") {
				t.Fatalf("error = %v, want %q without formatting artifact", err, tc.wantError)
			}
			state, loadErr := nudgequeue.LoadState(cityPath)
			if loadErr != nil || len(state.Pending) != 1 {
				t.Fatalf("pending/load error = %+v/%v, want retained", state.Pending, loadErr)
			}
		})
	}
}

func TestCancelPendingQueuedNudgeRejectsAmbiguousAndWrongQueueStates(t *testing.T) {
	now := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)

	t.Run("ambiguous pending", func(t *testing.T) {
		cityPath := t.TempDir()
		store := beads.NudgesStore{Store: beads.NewMemStore()}
		first := newQueuedNudgeWithOptions("worker-a", "first", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-duplicate-pending"})
		second := newQueuedNudgeWithOptions("worker-b", "second", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: first.ID})
		seedPendingNudge(t, cityPath, first, second)

		_, err := cancelPendingQueuedNudge(cityPath, store, first.ID, "obsolete", "goal-1-dispatch", now)
		if err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("error = %v, want ambiguous pending refusal", err)
		}
		state, _ := nudgequeue.LoadState(cityPath)
		if len(state.Pending) != 2 {
			t.Fatalf("pending = %+v, want both entries retained", state.Pending)
		}
	})

	t.Run("in flight", func(t *testing.T) {
		cityPath := t.TempDir()
		store := beads.NudgesStore{Store: beads.NewMemStore()}
		item := newQueuedNudgeWithOptions("worker", "already claimed", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-in-flight"})
		if err := nudgequeue.WithState(cityPath, func(state *nudgequeue.State) error {
			state.InFlight = append(state.InFlight, item)
			return nil
		}); err != nil {
			t.Fatalf("seed in-flight: %v", err)
		}
		_, err := cancelPendingQueuedNudge(cityPath, store, item.ID, "obsolete", "goal-1-dispatch", now)
		if err == nil || !strings.Contains(err.Error(), "in-flight") {
			t.Fatalf("error = %v, want in-flight refusal", err)
		}
	})

	t.Run("duplicate across buckets", func(t *testing.T) {
		cityPath := t.TempDir()
		store := beads.NudgesStore{Store: beads.NewMemStore()}
		item := newQueuedNudgeWithOptions("worker", "duplicate", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-cross-bucket"})
		if err := nudgequeue.WithState(cityPath, func(state *nudgequeue.State) error {
			state.Pending = append(state.Pending, item)
			state.InFlight = append(state.InFlight, item)
			return nil
		}); err != nil {
			t.Fatalf("seed duplicate: %v", err)
		}
		_, err := cancelPendingQueuedNudge(cityPath, store, item.ID, "obsolete", "goal-1-dispatch", now)
		if err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("error = %v, want cross-bucket ambiguity refusal", err)
		}
	})

	t.Run("dead letter", func(t *testing.T) {
		cityPath := t.TempDir()
		store := beads.NudgesStore{Store: beads.NewMemStore()}
		item := newQueuedNudgeWithOptions("worker", "already dead", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-dead-not-cancel"})
		seedDeadNudge(t, cityPath, item)
		_, err := cancelPendingQueuedNudge(cityPath, store, item.ID, "obsolete", "goal-1-dispatch", now)
		if err == nil || !strings.Contains(err.Error(), "dismiss or retry") {
			t.Fatalf("error = %v, want dead-letter guidance", err)
		}
	})
}

func TestCancelPendingQueuedNudgeRefusesExistingNonCancelledTerminalReceipt(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	now := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)
	pending := newQueuedNudgeWithOptions("worker", "already delivered", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-pending-with-delivered-receipt"})
	seedPendingNudge(t, cityPath, pending)
	front := nudgeFrontDoor(store)
	beadID, _, err := front.Save(pending)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	pending.BeadID = beadID
	if err := front.Terminalize(pending, "injected", "delivered", "provider-nudge-return", now); err != nil {
		t.Fatalf("Terminalize: %v", err)
	}

	_, err = cancelPendingQueuedNudge(cityPath, store, pending.ID, "obsolete", "goal-1-dispatch", now.Add(time.Minute))
	if err == nil || !strings.Contains(err.Error(), "already terminal as \"injected\"") {
		t.Fatalf("error = %v, want terminal-state conflict", err)
	}
	state, loadErr := nudgequeue.LoadState(cityPath)
	if loadErr != nil || len(state.Pending) != 1 {
		t.Fatalf("pending/load error = %+v/%v, want queue unchanged", state.Pending, loadErr)
	}
}

func TestNudgeCancelCommandRunsEndToEndAgainstFileStore(t *testing.T) {
	cityPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[workspace]\nname = \"cancel-test\"\n\n[beads]\nprovider = \"file\"\n"), 0o644); err != nil {
		t.Fatalf("write city config: %v", err)
	}
	t.Setenv("GC_CITY", cityPath)
	t.Setenv("GC_CITY_PATH", cityPath)
	now := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)
	pending := newQueuedNudgeWithOptions("worker", "obsolete", "session", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-command-cancel"})
	seedPendingNudge(t, cityPath, pending)

	var stdout, stderr bytes.Buffer
	cmd := newNudgeCancelCmd(&stdout, &stderr)
	cmd.SetArgs([]string{pending.ID, "--reason", "superseded", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v; stderr=%q", err, stderr.String())
	}
	validateJSONResultSchema(t, []string{"nudge", "cancel"}, stdout.Bytes())
	var got nudgeDispositionResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON result: %v; output=%q", err, stdout.String())
	}
	if got.Command != "nudge cancel" || got.Disposition != "canceled" || got.NudgeID != pending.ID {
		t.Fatalf("result = %+v, want command cancellation", got)
	}
	state, err := nudgequeue.LoadState(cityPath)
	if err != nil || len(state.Pending) != 0 {
		t.Fatalf("pending/load error = %+v/%v, want empty", state.Pending, err)
	}
}

func TestCmdNudgeCancelRejectsInvalidShapeAndCity(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cmdNudgeCancel(nil, "reason", false, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "exactly one") {
		t.Fatalf("missing id code/stderr = %d/%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	t.Setenv("GC_CITY", filepath.Join(t.TempDir(), "missing-city"))
	t.Setenv("GC_CITY_PATH", filepath.Join(t.TempDir(), "also-missing"))
	if code := cmdNudgeCancel([]string{"nudge-missing"}, "reason", false, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "gc nudge cancel:") {
		t.Fatalf("invalid city code/stderr = %d/%q", code, stderr.String())
	}
}
