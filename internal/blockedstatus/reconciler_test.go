package blockedstatus

import (
	"errors"
	"reflect"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestPlanPreservesAndRestoresLifecycleStatus(t *testing.T) {
	t.Parallel()

	rows := []Observation{
		{ID: "open", Status: "open", Revision: 11, IsBlocked: boolPtr(true)},
		{ID: "working", Status: "in_progress", Revision: 12, IsBlocked: boolPtr(true)},
		{
			ID: "restoring", Status: "blocked", Revision: 13, IsBlocked: boolPtr(false),
			Metadata: map[string]string{
				MetadataProjectionKey: ProjectionVersion,
				MetadataPreimageKey:   "in_progress",
			},
		},
		{ID: "ready", Status: "open", Revision: 14, IsBlocked: boolPtr(false)},
		{ID: "closed", Status: "closed", Revision: 15, IsBlocked: boolPtr(true)},
	}

	plan := Plan(rows)
	if len(plan.Unsafe) != 0 {
		t.Fatalf("Unsafe = %#v, want none", plan.Unsafe)
	}
	want := []Action{
		{
			ID: "open", ExpectedRevision: 11, ExpectedStatus: "open", ExpectedIsBlocked: true, Status: "blocked",
			MetadataSet: map[string]string{MetadataProjectionKey: ProjectionVersion, MetadataPreimageKey: "open"},
		},
		{
			ID: "restoring", ExpectedRevision: 13, ExpectedStatus: "blocked", ExpectedIsBlocked: false, Status: "in_progress",
			MetadataUnset: []string{MetadataPreimageKey, MetadataProjectionKey},
		},
		{
			ID: "working", ExpectedRevision: 12, ExpectedStatus: "in_progress", ExpectedIsBlocked: true, Status: "blocked",
			MetadataSet: map[string]string{MetadataProjectionKey: ProjectionVersion, MetadataPreimageKey: "in_progress"},
		},
	}
	if !reflect.DeepEqual(plan.Actions, want) {
		t.Fatalf("Actions = %#v, want %#v", plan.Actions, want)
	}
}

func TestPlanFailsClosedOnLegacyOrUnobservedRows(t *testing.T) {
	t.Parallel()

	rows := []Observation{
		{ID: "legacy-live", Status: "blocked", Revision: 1, IsBlocked: boolPtr(true)},
		{ID: "legacy-stale", Status: "blocked", Revision: 2, IsBlocked: boolPtr(false)},
		{ID: "missing-projection", Status: "open", Revision: 3},
		{
			ID: "bad-preimage", Status: "blocked", Revision: 4, IsBlocked: boolPtr(false),
			Metadata: map[string]string{MetadataProjectionKey: ProjectionVersion, MetadataPreimageKey: "closed"},
		},
		{
			ID: "deferred-marker-drift", Status: "deferred", Revision: 5, IsBlocked: boolPtr(false),
			Metadata: map[string]string{MetadataProjectionKey: ProjectionVersion, MetadataPreimageKey: "open"},
		},
	}

	plan := Plan(rows)
	want := []UnsafeRow{
		{ID: "bad-preimage", Reason: ReasonInvalidPreimage},
		{ID: "deferred-marker-drift", Reason: ReasonProjectionDrift},
		{ID: "legacy-live", Reason: ReasonUnclassifiedLegacy},
		{ID: "legacy-stale", Reason: ReasonUnclassifiedLegacy},
		{ID: "missing-projection", Reason: ReasonMissingProjection},
	}
	if !reflect.DeepEqual(plan.Unsafe, want) {
		t.Fatalf("Unsafe = %#v, want %#v", plan.Unsafe, want)
	}
}

func TestPlanUsesAuditedLegacyPreimagesWithoutGuessing(t *testing.T) {
	t.Parallel()

	plan := Plan([]Observation{
		{
			ID: "still-blocked", Status: "blocked", Revision: 21, IsBlocked: boolPtr(true),
			LegacyPreimage: "in_progress",
		},
		{
			ID: "ready-to-restore", Status: "blocked", Revision: 22, IsBlocked: boolPtr(false),
			LegacyPreimage: "open",
		},
	})
	if len(plan.Unsafe) != 0 {
		t.Fatalf("Unsafe = %#v, want none", plan.Unsafe)
	}
	want := []Action{
		{
			ID: "ready-to-restore", ExpectedRevision: 22, ExpectedStatus: "blocked", ExpectedIsBlocked: false,
			Status: "open", MetadataUnset: []string{MetadataPreimageKey, MetadataProjectionKey},
		},
		{
			ID: "still-blocked", ExpectedRevision: 21, ExpectedStatus: "blocked", ExpectedIsBlocked: true,
			Status:      "blocked",
			MetadataSet: map[string]string{MetadataProjectionKey: ProjectionVersion, MetadataPreimageKey: "in_progress"},
		},
	}
	if !reflect.DeepEqual(plan.Actions, want) {
		t.Fatalf("Actions = %#v, want %#v", plan.Actions, want)
	}
}

func TestRunRefusesEveryWriteWhenAnyRowIsUnsafe(t *testing.T) {
	t.Parallel()

	writer := &recordingWriter{}
	result, err := Run(Snapshot{Complete: true, Observations: []Observation{
		{ID: "candidate", Status: "open", Revision: 8, IsBlocked: boolPtr(true)},
		{ID: "legacy", Status: "blocked", Revision: 9, IsBlocked: boolPtr(false)},
	}}, writer, Options{})
	if !errors.Is(err, ErrUnsafeCorpus) {
		t.Fatalf("Run error = %v, want ErrUnsafeCorpus", err)
	}
	if len(writer.calls) != 0 {
		t.Fatalf("writes = %#v, want none", writer.calls)
	}
	if result.Planned != 1 || result.Applied != 0 || len(result.Unsafe) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunUsesRevisionAndSameBlockedObservation(t *testing.T) {
	t.Parallel()

	writer := &recordingWriter{}
	result, err := Run(Snapshot{Complete: true, Observations: []Observation{
		{ID: "candidate", Status: "in_progress", Revision: 42, IsBlocked: boolPtr(true)},
	}}, writer, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Planned != 1 || result.Applied != 1 {
		t.Fatalf("result = %#v", result)
	}
	want := []writeCall{{
		id: "candidate", revision: 42, expectedStatus: "in_progress", isBlocked: true,
		status:      "blocked",
		metadataSet: map[string]string{MetadataProjectionKey: ProjectionVersion, MetadataPreimageKey: "in_progress"},
	}}
	if !reflect.DeepEqual(writer.calls, want) {
		t.Fatalf("calls = %#v, want %#v", writer.calls, want)
	}
}

func TestRunStopsOnLostObservation(t *testing.T) {
	t.Parallel()

	conflict := errors.New("lost blocked-state observation")
	writer := &recordingWriter{failID: "a", failErr: conflict}
	result, err := Run(Snapshot{Complete: true, Observations: []Observation{
		{ID: "b", Status: "open", Revision: 2, IsBlocked: boolPtr(true)},
		{ID: "a", Status: "open", Revision: 1, IsBlocked: boolPtr(true)},
	}}, writer, Options{})
	if !errors.Is(err, conflict) {
		t.Fatalf("Run error = %v, want conflict", err)
	}
	if result.Applied != 0 {
		t.Fatalf("Applied = %d, want 0", result.Applied)
	}
	if len(writer.calls) != 1 || writer.calls[0].id != "a" {
		t.Fatalf("calls = %#v, want only deterministic first row a", writer.calls)
	}
}

func TestRunDryRunReportsPlanWithoutWrites(t *testing.T) {
	t.Parallel()

	writer := &recordingWriter{}
	result, err := Run(Snapshot{Complete: true, Observations: []Observation{
		{ID: "candidate", Status: "open", Revision: 4, IsBlocked: boolPtr(true)},
	}}, writer, Options{DryRun: true})
	if err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	if result.Planned != 1 || result.Applied != 0 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.calls) != 0 {
		t.Fatalf("writes = %#v, want none", writer.calls)
	}
}

func TestRunRefusesIncompleteSnapshotBeforePlanningOrWriting(t *testing.T) {
	t.Parallel()

	writer := &recordingWriter{}
	result, err := Run(Snapshot{
		Complete: false,
		Observations: []Observation{
			{ID: "candidate", Status: "open", Revision: 4, IsBlocked: boolPtr(true)},
		},
	}, writer, Options{})
	if !errors.Is(err, ErrIncompleteSnapshot) {
		t.Fatalf("Run error = %v, want ErrIncompleteSnapshot", err)
	}
	if result.Scanned != 1 || result.Planned != 0 || result.Applied != 0 {
		t.Fatalf("result = %#v", result)
	}
	if len(writer.calls) != 0 {
		t.Fatalf("writes = %#v, want none", writer.calls)
	}
}

type writeCall struct {
	id             string
	revision       int64
	expectedStatus string
	isBlocked      bool
	status         string
	metadataSet    map[string]string
	metadataUnset  []string
}

type recordingWriter struct {
	calls   []writeCall
	failID  string
	failErr error
}

func (w *recordingWriter) UpdateIfBlockedStateMatches(
	id string,
	expectedRevision int64,
	expectedStatus string,
	expectedIsBlocked bool,
	patch Patch,
) error {
	call := writeCall{
		id: id, revision: expectedRevision, expectedStatus: expectedStatus, isBlocked: expectedIsBlocked,
		status: patch.Status, metadataSet: patch.MetadataSet, metadataUnset: patch.MetadataUnset,
	}
	w.calls = append(w.calls, call)
	if id == w.failID {
		return w.failErr
	}
	return nil
}
