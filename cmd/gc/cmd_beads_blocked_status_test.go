package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/blockedstatus"
)

func TestBeadsBlockedStatusCommandIsRegistered(t *testing.T) {
	t.Parallel()

	cmd, _, err := newBeadsCmd(&bytes.Buffer{}, &bytes.Buffer{}).Find([]string{"reconcile-blocked-status"})
	if err != nil || cmd == nil || cmd.Name() != "reconcile-blocked-status" {
		t.Fatalf("Find reconcile-blocked-status = (%v, %v), want registered command", cmd, err)
	}
}

func TestBeadsBlockedStatusManifestDeclaresJSONSupport(t *testing.T) {
	clearGCEnv(t)
	configureIsolatedRuntimeEnv(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"beads", "reconcile-blocked-status", "--json-schema"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var manifest jsonSchemaManifest
	if err := json.Unmarshal(stdout.Bytes(), &manifest); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !manifest.JSONSupported || strings.Join(manifest.Command, " ") != "beads reconcile-blocked-status" {
		t.Fatalf("manifest = %+v", manifest)
	}
	resultSchema := compileJSONSchema(t, "gc://schemas/beads/reconcile-blocked-status/result.schema.json", manifest.Schemas[jsonSchemaResultRole])
	failureSchema := compileJSONSchema(t, "gc://schemas/beads/reconcile-blocked-status/failure.schema.json", manifest.Schemas[jsonSchemaFailureRole])
	assertBlockedStatusSchemaPayload(t, resultSchema, newBeadsBlockedStatusResult("city:demo", true, blockedstatus.Result{
		Scanned: 2,
	}))
	assertBlockedStatusSchemaPayload(t, failureSchema, newBeadsBlockedStatusFailure("city:demo", true, blockedstatus.Result{
		Scanned: 2,
		Unsafe:  []blockedstatus.UnsafeRow{{ID: "dr-a", Reason: blockedstatus.ReasonUnclassifiedLegacy}},
	}, blockedstatus.ErrUnsafeCorpus))
	assertBlockedStatusSchemaPayload(t, failureSchema, jsonSchemaErrorPayload{
		SchemaVersion: "1",
		OK:            false,
		Error: jsonSchemaErrorDetail{
			Code: "command_failed", Message: "invalid store", ExitCode: 1,
		},
	})
}

func assertBlockedStatusSchemaPayload(t *testing.T, schema interface{ Validate(any) error }, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(decoded); err != nil {
		t.Fatalf("payload does not match schema: %v\n%s", err, raw)
	}
}

func TestValidateBeadsBlockedStatusRequest(t *testing.T) {
	t.Parallel()

	valid := beadsBlockedStatusRequest{storeRef: "city:demo", storeRefSet: true, limit: 1000, jsonOut: true}
	if err := validateBeadsBlockedStatusRequest(valid); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	for name, mutate := range map[string]func(*beadsBlockedStatusRequest){
		"missing exact store": func(r *beadsBlockedStatusRequest) { r.storeRefSet = false },
		"rig store":           func(r *beadsBlockedStatusRequest) { r.storeRef = "rig:demo" },
		"zero limit":          func(r *beadsBlockedStatusRequest) { r.limit = 0 },
		"negative limit":      func(r *beadsBlockedStatusRequest) { r.limit = -1 },
		"oversized limit":     func(r *beadsBlockedStatusRequest) { r.limit = blockedStatusReconcileMaxLimit + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := validateBeadsBlockedStatusRequest(request); err == nil {
				t.Fatal("validation error = nil")
			}
		})
	}
}

func TestApplyBeadsBlockedStatusReconciliationUsesLedgerAndSameObservationGuard(t *testing.T) {
	t.Parallel()

	reader := blockedStatusCommandReader{snapshot: beads.BlockedStatusSnapshot{
		Complete: true,
		Observations: []beads.BlockedStatusObservation{
			{ID: "dr-a", Status: "blocked", IsBlocked: false, Revision: 11},
			{ID: "dr-b", Status: "open", IsBlocked: true, Revision: 12},
		},
	}}
	writer := &blockedStatusCommandWriter{}
	result, err := applyBeadsBlockedStatusReconciliation(reader, writer, map[string]blockedstatus.LegacyPreimage{
		"dr-a": {Status: "blocked", Revision: 11, IsBlocked: false, Preimage: "in_progress"},
	}, 100, false)
	if err != nil {
		t.Fatalf("applyBeadsBlockedStatusReconciliation: %v", err)
	}
	if result.Scanned != 2 || result.Planned != 2 || result.Applied != 2 || len(result.Unsafe) != 0 {
		t.Fatalf("result = %#v", result)
	}
	want := []blockedStatusCommandWrite{
		{
			observation: beads.BlockedStatusObservation{ID: "dr-a", Status: "blocked", IsBlocked: false, Revision: 11},
			status:      "in_progress", unset: []string{"gc.blocked_status_preimage", "gc.blocked_status_projection"},
		},
		{
			observation: beads.BlockedStatusObservation{ID: "dr-b", Status: "open", IsBlocked: true, Revision: 12},
			status:      "blocked", set: map[string]string{
				"gc.blocked_status_projection": "v1", "gc.blocked_status_preimage": "open",
			},
		},
	}
	if !reflect.DeepEqual(writer.calls, want) {
		t.Fatalf("calls = %#v, want %#v", writer.calls, want)
	}
}

func TestApplyBeadsBlockedStatusReconciliationRejectsStaleLedgerTupleBeforeWrites(t *testing.T) {
	t.Parallel()

	reader := blockedStatusCommandReader{snapshot: beads.BlockedStatusSnapshot{
		Complete: true,
		Observations: []beads.BlockedStatusObservation{
			{ID: "dr-a", Status: "blocked", IsBlocked: false, Revision: 12},
			{ID: "dr-b", Status: "open", IsBlocked: true, Revision: 13},
		},
	}}
	writer := &blockedStatusCommandWriter{}
	_, err := applyBeadsBlockedStatusReconciliation(reader, writer, map[string]blockedstatus.LegacyPreimage{
		"dr-a": {Status: "blocked", Revision: 11, IsBlocked: false, Preimage: "open"},
	}, 100, false)
	if err == nil {
		t.Fatal("stale ledger error = nil, want refusal")
	}
	if len(writer.calls) != 0 {
		t.Fatalf("calls = %#v, want none", writer.calls)
	}
}

func TestApplyBeadsBlockedStatusReconciliationFailsBeforeWritesOnIncompleteOrUnsafeCorpus(t *testing.T) {
	t.Parallel()

	tests := []beads.BlockedStatusSnapshot{
		{Complete: false, Observations: []beads.BlockedStatusObservation{{ID: "dr-a", Status: "open"}}},
		{Complete: true, Observations: []beads.BlockedStatusObservation{{ID: "dr-a", Status: "blocked", Revision: 1}}},
	}
	for _, snapshot := range tests {
		writer := &blockedStatusCommandWriter{}
		if _, err := applyBeadsBlockedStatusReconciliation(blockedStatusCommandReader{snapshot: snapshot}, writer, nil, 10, false); err == nil {
			t.Fatal("apply error = nil, want fail-closed refusal")
		}
		if len(writer.calls) != 0 {
			t.Fatalf("calls = %#v, want none", writer.calls)
		}
	}
}

type blockedStatusCommandReader struct {
	snapshot beads.BlockedStatusSnapshot
}

func (r blockedStatusCommandReader) ReadBlockedStatusSnapshot(int) (beads.BlockedStatusSnapshot, error) {
	return r.snapshot, nil
}

type blockedStatusCommandWrite struct {
	observation beads.BlockedStatusObservation
	status      string
	set         map[string]string
	unset       []string
}

type blockedStatusCommandWriter struct {
	beads.ConditionalWriter
	calls []blockedStatusCommandWrite
}

func (w *blockedStatusCommandWriter) UpdateBlockedStatusIfMatch(
	observation beads.BlockedStatusObservation,
	status string,
	set map[string]string,
	unset []string,
) error {
	w.calls = append(w.calls, blockedStatusCommandWrite{
		observation: observation,
		status:      status,
		set:         set,
		unset:       unset,
	})
	return nil
}
