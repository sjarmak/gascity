package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/nudgequeue"
)

type dispositionFailStore struct {
	*beads.MemStore
}

func (s *dispositionFailStore) SetMetadataBatch(id string, kvs map[string]string) error {
	if kvs["operator_disposition"] != "" {
		return errors.New("audit store unavailable")
	}
	return s.MemStore.SetMetadataBatch(id, kvs)
}

type terminalizeFailStore struct {
	*beads.MemStore
}

func (s *terminalizeFailStore) SetMetadataBatch(id string, kvs map[string]string) error {
	if kvs["state"] != "" {
		return errors.New("terminal receipt unavailable")
	}
	return s.MemStore.SetMetadataBatch(id, kvs)
}

func seedDeadNudge(t *testing.T, cityPath string, item queuedNudge) {
	t.Helper()
	if err := nudgequeue.WithState(cityPath, func(state *nudgequeue.State) error {
		state.Dead = append(state.Dead, item)
		return nil
	}); err != nil {
		t.Fatalf("seed dead nudge: %v", err)
	}
}

func TestDismissDeadQueuedNudgeCreatesVerifiedReceiptBeforeRemoval(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	dead := newQueuedNudgeWithOptions("worker", "review the release", "session", now.Add(-48*time.Hour), queuedNudgeOptions{ID: "nudge-dead-dismiss"})
	dead.LastError = "expired"
	dead.DeadAt = now.Add(-24 * time.Hour)
	seedDeadNudge(t, cityPath, dead)

	got, err := dismissDeadQueuedNudge(cityPath, store, dead.ID, "superseded by bead gc-123", "goal-1-dispatch", now)
	if err != nil {
		t.Fatalf("dismissDeadQueuedNudge: %v", err)
	}
	if got.Disposition != "dismissed" || got.NudgeID != dead.ID || got.TerminalBeadID == "" {
		t.Fatalf("result = %+v, want dismissed receipt", got)
	}

	state, err := nudgequeue.LoadState(cityPath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Dead) != 0 {
		t.Fatalf("dead = %+v, want selected entry removed", state.Dead)
	}
	shadow, ok, err := nudgeFrontDoor(store).FindIncludingTerminal(dead.ID)
	if err != nil || !ok {
		t.Fatalf("FindIncludingTerminal = %+v, %v, %v", shadow, ok, err)
	}
	if shadow.Open || !nudgequeue.IsTerminalState(shadow.State) {
		t.Fatalf("shadow = %+v, want closed terminal receipt", shadow)
	}
	if shadow.OperatorDisposition != "dismissed" || shadow.OperatorReason != "superseded by bead gc-123" || shadow.OperatorActor != "goal-1-dispatch" {
		t.Fatalf("operator audit = %+v, want exact dismissal", shadow)
	}

	again, err := dismissDeadQueuedNudge(cityPath, store, dead.ID, "superseded by bead gc-123", "goal-1-dispatch", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("idempotent dismiss: %v", err)
	}
	if again != got {
		t.Fatalf("idempotent result = %+v, want %+v", again, got)
	}
	if _, err := dismissDeadQueuedNudge(cityPath, store, dead.ID, "different reason", "goal-1-dispatch", now.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "different audit inputs") {
		t.Fatalf("changed idempotency inputs error = %v, want loud conflict", err)
	}
}

func TestDismissDeadQueuedNudgeRetainsDeadEntryWhenAuditWriteFails(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NudgesStore{Store: &dispositionFailStore{MemStore: beads.NewMemStore()}}
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	dead := newQueuedNudgeWithOptions("worker", "review the release", "session", now.Add(-48*time.Hour), queuedNudgeOptions{ID: "nudge-dead-audit-fail"})
	dead.LastError = "expired"
	dead.DeadAt = now.Add(-24 * time.Hour)
	seedDeadNudge(t, cityPath, dead)

	_, err := dismissDeadQueuedNudge(cityPath, store, dead.ID, "obsolete", "goal-1-dispatch", now)
	if err == nil || !strings.Contains(err.Error(), "audit store unavailable") {
		t.Fatalf("error = %v, want audit write failure", err)
	}
	state, loadErr := nudgequeue.LoadState(cityPath)
	if loadErr != nil {
		t.Fatalf("LoadState: %v", loadErr)
	}
	if len(state.Dead) != 1 || state.Dead[0].ID != dead.ID {
		t.Fatalf("dead = %+v, want entry retained until receipt is durable", state.Dead)
	}
}

func TestDismissDeadQueuedNudgeWritesNoAuditBeforeTerminalReceipt(t *testing.T) {
	cityPath := t.TempDir()
	base := beads.NewMemStore()
	store := beads.NudgesStore{Store: &terminalizeFailStore{MemStore: base}}
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	dead := newQueuedNudgeWithOptions("worker", "review the release", "session", now.Add(-48*time.Hour), queuedNudgeOptions{ID: "nudge-dead-terminal-fail"})
	dead.LastError = "expired"
	dead.DeadAt = now.Add(-24 * time.Hour)
	seedDeadNudge(t, cityPath, dead)

	_, err := dismissDeadQueuedNudge(cityPath, store, dead.ID, "obsolete", "goal-1-dispatch", now)
	if err == nil || !strings.Contains(err.Error(), "terminal receipt unavailable") {
		t.Fatalf("error = %v, want terminal receipt failure", err)
	}
	shadow, ok, findErr := nudgeFrontDoor(store).FindIncludingTerminal(dead.ID)
	if findErr != nil || !ok {
		t.Fatalf("FindIncludingTerminal = %+v, %t, %v", shadow, ok, findErr)
	}
	bead, getErr := base.Get(shadow.BeadID)
	if getErr != nil {
		t.Fatalf("Get receipt bead: %v", getErr)
	}
	if bead.Metadata["operator_disposition"] != "" || bead.Metadata["operator_reason"] != "" || bead.Metadata["operator_actor"] != "" {
		t.Fatalf("operator audit written before terminal receipt: %+v", bead.Metadata)
	}
}

func TestDismissDeadQueuedNudgeRejectsAmbiguousIDWithoutMutation(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	first := newQueuedNudgeWithOptions("worker-a", "first", "session", now.Add(-48*time.Hour), queuedNudgeOptions{ID: "nudge-duplicate-dead"})
	second := newQueuedNudgeWithOptions("worker-b", "second", "session", now.Add(-47*time.Hour), queuedNudgeOptions{ID: first.ID})
	seedDeadNudge(t, cityPath, first)
	seedDeadNudge(t, cityPath, second)

	_, err := dismissDeadQueuedNudge(cityPath, store, first.ID, "obsolete", "goal-1-dispatch", now)
	if err == nil || !strings.Contains(err.Error(), "refusing ambiguous disposition") {
		t.Fatalf("error = %v, want ambiguous disposition refusal", err)
	}
	state, loadErr := nudgequeue.LoadState(cityPath)
	if loadErr != nil {
		t.Fatalf("LoadState: %v", loadErr)
	}
	if len(state.Dead) != 2 {
		t.Fatalf("dead = %+v, want both ambiguous entries retained", state.Dead)
	}
}

func TestRetryDeadQueuedNudgeCreatesDistinctCorrelatedQueueEntry(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	ref := &nudgeReference{Kind: "bead", ID: "gc-123"}
	dead := newQueuedNudgeWithOptions("old-worker", "review the release", "sling", now.Add(-48*time.Hour), queuedNudgeOptions{ID: "nudge-dead-retry", Reference: ref})
	dead.LastError = "queued nudge session fence mismatch"
	dead.DeadAt = now.Add(-24 * time.Hour)
	seedDeadNudge(t, cityPath, dead)
	target := nudgeTarget{
		cityPath:          cityPath,
		alias:             "current-worker",
		agent:             config.Agent{Name: "current-worker"},
		sessionID:         "gc-session-current",
		continuationEpoch: "7",
	}

	got, err := retryDeadQueuedNudge(cityPath, store, dead.ID, target, "work is still required", "goal-1-dispatch", now)
	if err != nil {
		t.Fatalf("retryDeadQueuedNudge: %v", err)
	}
	if got.Disposition != "retried" || got.RetryNudgeID == "" || got.RetryNudgeID == dead.ID {
		t.Fatalf("result = %+v, want distinct retry receipt", got)
	}

	state, err := nudgequeue.LoadState(cityPath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Dead) != 0 || len(state.Pending) != 1 {
		t.Fatalf("pending/dead = %d/%d, want 1/0", len(state.Pending), len(state.Dead))
	}
	retry := state.Pending[0]
	if retry.ID != got.RetryNudgeID || retry.Agent != "current-worker" || retry.SessionID != "gc-session-current" || retry.ContinuationEpoch != "7" {
		t.Fatalf("retry = %+v, want rebound current target", retry)
	}
	if retry.Reference == nil || *retry.Reference != *ref || retry.Message != dead.Message || retry.Source != dead.Source {
		t.Fatalf("retry = %+v, want original direction and reference", retry)
	}
	oldShadow, ok, err := nudgeFrontDoor(store).FindIncludingTerminal(dead.ID)
	if err != nil || !ok {
		t.Fatalf("old shadow = %+v, %v, %v", oldShadow, ok, err)
	}
	if oldShadow.OperatorDisposition != "retried" || oldShadow.RetryNudgeID != got.RetryNudgeID {
		t.Fatalf("old shadow = %+v, want correlation to retry", oldShadow)
	}

	again, err := retryDeadQueuedNudge(cityPath, store, dead.ID, target, "work is still required", "goal-1-dispatch", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if again != got {
		t.Fatalf("idempotent result = %+v, want %+v", again, got)
	}
	state, err = nudgequeue.LoadState(cityPath)
	if err != nil {
		t.Fatalf("LoadState after retry: %v", err)
	}
	if len(state.Pending) != 1 {
		t.Fatalf("pending = %+v, want no duplicate retry", state.Pending)
	}
	otherTarget := target
	otherTarget.sessionID = "gc-session-other"
	if _, err := retryDeadQueuedNudge(cityPath, store, dead.ID, otherTarget, "work is still required", "goal-1-dispatch", now.Add(2*time.Minute)); err == nil || !strings.Contains(err.Error(), "different audit inputs") {
		t.Fatalf("changed retry target error = %v, want loud conflict", err)
	}
}

func TestRetryDeadQueuedNudgeRefusesLiveReferenceDuplicate(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	ref := &nudgeReference{Kind: "bead", ID: "gc-123"}
	dead := newQueuedNudgeWithOptions("old-worker", "review the release", "sling", now.Add(-48*time.Hour), queuedNudgeOptions{ID: "nudge-dead-reference", Reference: ref})
	dead.LastError = "expired"
	dead.DeadAt = now.Add(-24 * time.Hour)
	live := newQueuedNudgeWithOptions("other-worker", "same work", "sling", now.Add(-time.Hour), queuedNudgeOptions{ID: "nudge-live-reference", Reference: ref})
	if err := nudgequeue.WithState(cityPath, func(state *nudgequeue.State) error {
		state.Dead = append(state.Dead, dead)
		state.Pending = append(state.Pending, live)
		return nil
	}); err != nil {
		t.Fatalf("seed queue: %v", err)
	}
	target := nudgeTarget{cityPath: cityPath, agent: config.Agent{Name: "current-worker"}, sessionID: "gc-session-current"}

	_, err := retryDeadQueuedNudge(cityPath, store, dead.ID, target, "work is still required", "goal-1-dispatch", now)
	if err == nil || !strings.Contains(err.Error(), "refusing duplicate retry") {
		t.Fatalf("error = %v, want live-reference duplicate refusal", err)
	}
	state, loadErr := nudgequeue.LoadState(cityPath)
	if loadErr != nil {
		t.Fatalf("LoadState: %v", loadErr)
	}
	if len(state.Pending) != 1 || state.Pending[0].ID != live.ID || len(state.Dead) != 1 {
		t.Fatalf("pending/dead = %+v/%+v, want original queue unchanged", state.Pending, state.Dead)
	}
}

func TestRetryDeadQueuedNudgeDoesNotDuplicateExistingPendingRetry(t *testing.T) {
	cityPath := t.TempDir()
	store := beads.NudgesStore{Store: beads.NewMemStore()}
	now := time.Date(2026, 8, 13, 5, 0, 0, 0, time.UTC)
	dead := newQueuedNudgeWithOptions("old-worker", "review the release", "session", now.Add(-48*time.Hour), queuedNudgeOptions{ID: "nudge-dead-repeat"})
	dead.LastError = "expired"
	dead.DeadAt = now.Add(-24 * time.Hour)
	seedDeadNudge(t, cityPath, dead)
	target := nudgeTarget{cityPath: cityPath, agent: config.Agent{Name: "current-worker"}, sessionID: "gc-session-current"}

	if _, err := retryDeadQueuedNudge(cityPath, store, dead.ID, target, "still required", "goal-1-dispatch", now); err != nil {
		t.Fatalf("first retry: %v", err)
	}
	seedDeadNudge(t, cityPath, dead)
	if _, err := retryDeadQueuedNudge(cityPath, store, dead.ID, target, "still required", "goal-1-dispatch", now.Add(time.Minute)); err != nil {
		t.Fatalf("idempotent retry with reappeared dead entry: %v", err)
	}
	state, err := nudgequeue.LoadState(cityPath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Pending) != 1 || state.Pending[0].ID != retryNudgeID(dead.ID) || len(state.Dead) != 0 {
		t.Fatalf("pending/dead = %+v/%+v, want one retry and no old dead entry", state.Pending, state.Dead)
	}
}

func TestNudgeDispositionCommandsRejectMissingOperatorInputs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cmdNudgeCancel([]string{"nudge-pending"}, "", false, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "--reason") {
		t.Fatalf("cancel code/stderr = %d/%q, want required reason", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := cmdNudgeDismiss([]string{"nudge-dead"}, "", false, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "--reason") {
		t.Fatalf("dismiss code/stderr = %d/%q, want required reason", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := cmdNudgeRetry([]string{"nudge-dead"}, "", "still needed", false, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "--to") {
		t.Fatalf("retry code/stderr = %d/%q, want required target", code, stderr.String())
	}
}

func TestNudgeCancelIsDistinctFromDeadLetterDismissal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	command, _, err := newNudgeCmd(&stdout, &stderr).Find([]string{"cancel"})
	if err != nil {
		t.Fatalf("find cancel command: %v", err)
	}
	if command.Name() != "cancel" {
		t.Fatalf("cancel resolved to %q, want distinct pending-cancel command", command.Name())
	}
}

func TestWriteNudgeDispositionResultHasStableJSONReceipt(t *testing.T) {
	result := nudgeDispositionResult{
		SchemaVersion:  "1",
		Command:        "nudge retry",
		NudgeID:        "nudge-old",
		Disposition:    "retried",
		Reason:         "still required",
		Actor:          "operator",
		OperatorAt:     "2026-08-13T05:00:00Z",
		TerminalBeadID: "gc-receipt",
		RetryNudgeID:   "nudge-retry-new",
		RetryTarget:    "gc-session-current",
	}
	var stdout, stderr bytes.Buffer
	if code := writeNudgeDispositionResult(result, true, &stdout, &stderr); code != 0 {
		t.Fatalf("write code = %d, stderr=%q", code, stderr.String())
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("JSON receipt: %v; output=%q", err, stdout.String())
	}
	if len(got) != 11 || got["ok"] != true || got["schema_version"] != "1" || got["command"] != "nudge retry" ||
		got["nudge_id"] != "nudge-old" || got["retry_nudge_id"] != "nudge-retry-new" || got["retry_target"] != "gc-session-current" {
		t.Fatalf("JSON receipt = %#v, want stable receipt envelope", got)
	}
}

func TestNudgeDispositionCommandsDeclareJSONContracts(t *testing.T) {
	for _, command := range []string{"cancel", "dismiss", "retry"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{"nudge", command, "--json-schema"}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("manifest code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
			}
			var manifest jsonSchemaManifest
			if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
				t.Fatalf("Unmarshal manifest: %v\n%s", err, stdout.String())
			}
			if !manifest.JSONSupported || len(manifest.Schemas[jsonSchemaResultRole]) == 0 || len(manifest.Schemas[jsonSchemaFailureRole]) == 0 {
				t.Fatalf("manifest does not declare complete JSON support: %+v", manifest)
			}
			var contractStdout, contractStderr bytes.Buffer
			handled, contractCode := handleJSONContractRequest(newRootCmd(&contractStdout, &contractStderr), []string{"nudge", command, "nudge-dead", "--json"}, &contractStdout, &contractStderr)
			if handled || contractCode != 0 || contractStdout.Len() != 0 || contractStderr.Len() != 0 {
				t.Fatalf("JSON contract handled/code/stdout/stderr = %t/%d/%q/%q, want runtime pass-through", handled, contractCode, contractStdout.String(), contractStderr.String())
			}
		})
	}
}
