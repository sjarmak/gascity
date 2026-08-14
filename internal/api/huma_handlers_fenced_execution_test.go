package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/worker"
)

type apiFencedProvider struct {
	*runtime.Fake
	mu      sync.Mutex
	starts  int
	receipt runtime.FencedExecutionReceipt
}

func (p *apiFencedProvider) StartOrAttachFenced(_ context.Context, req runtime.FencedExecutionRequest) (runtime.FencedExecutionReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts++
	if p.receipt.ExecutionID == "" {
		p.receipt = runtime.FencedExecutionReceipt{ExecutionID: "execution-1", OperationID: req.OperationID, RequestHash: req.RequestHash, Target: req.Target, Process: runtime.FencedProcessIdentity{PID: 22, ProcessGroupID: 22, StartIdentity: "start"}}
	}
	return p.receipt, nil
}

func (p *apiFencedProvider) WaitFenced(_ context.Context, receipt runtime.FencedExecutionReceipt) (runtime.FencedExecutionResult, error) {
	return runtime.FencedExecutionResult{Receipt: receipt, Outcome: "completed", ArtifactRefs: []runtime.FencedExecutionArtifact{{Kind: "test-report", URI: "artifact://report", SHA256: strings.Repeat("b", 64)}}}, nil
}

func (p *apiFencedProvider) RevokeAndStopFenced(_ context.Context, req runtime.FencedExecutionStopRequest) (runtime.FencedExecutionStopReceipt, error) {
	return runtime.FencedExecutionStopReceipt{Receipt: req.Receipt, Revoked: true, Stopped: true}, nil
}

func TestFencedExecutionHTTPRoundTripKeepsRawClaimRequestOnly(t *testing.T) {
	state, provider, input := newAPIFencedFixture(t)
	handler := newTestCityHandler(t, state)
	const marker = "super-secret-http-claim-marker"
	input.ClaimToken = marker
	state.stores[input.FormulaRig].SetMetadata(input.BeadID, "gc.temporal.claim_token", marker) //nolint:errcheck

	resolveBody := postFencedExecution(t, handler, state, "resolve", input)
	if bytes.Contains(resolveBody, []byte(marker)) {
		t.Fatalf("resolve response leaked raw claim: %s", resolveBody)
	}
	var resolved FencedExecutionResolveOutput
	if err := json.Unmarshal(resolveBody, &resolved.Body); err != nil || !resolved.Body.OK || resolved.Body.Receipt == nil {
		t.Fatalf("resolve response = %s, err=%v", resolveBody, err)
	}

	executeBody := postFencedExecution(t, handler, state, "execute", input)
	if bytes.Contains(executeBody, []byte(marker)) {
		t.Fatalf("execute response leaked raw claim: %s", executeBody)
	}
	var executed FencedExecutionExecuteOutput
	if err := json.Unmarshal(executeBody, &executed.Body); err != nil || !executed.Body.OK || executed.Body.Result == nil || executed.Body.Result.Outcome != "completed" {
		t.Fatalf("execute response = %s, err=%v", executeBody, err)
	}
	if provider.starts != 1 {
		t.Fatalf("resolve+execute provider starts = %d, want one durable binding", provider.starts)
	}

	cancelInput := worker.FencedExecutionCancelInput{CityID: input.CityID, FormulaRig: input.FormulaRig, BeadID: input.BeadID, Generation: input.Generation, ClaimToken: input.ClaimToken, TargetSessionID: input.TargetSessionID, ExecutionID: resolved.Body.Receipt.ExecutionID}
	cancelBody := postFencedCancellation(t, handler, state, cancelInput)
	var canceled FencedExecutionCancelOutput
	if err := json.Unmarshal(cancelBody, &canceled.Body); err != nil || !canceled.Body.OK || canceled.Body.Receipt == nil || !canceled.Body.Receipt.Revoked {
		t.Fatalf("cancel response = %s, err=%v", cancelBody, err)
	}
}

func postFencedCancellation(t *testing.T, handler http.Handler, state State, input worker.FencedExecutionCancelInput) []byte {
	t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := newPostRequest(cityURL(state, "/temporal/fenced-executions/cancel"), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST cancel status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	return recorder.Body.Bytes()
}

func TestFencedExecutionHTTPUnsupportedFailsClosed(t *testing.T) {
	state, provider, input := newAPIFencedFixture(t)
	state.sessionProvider = runtime.NewFake()
	handler := newTestCityHandler(t, state)
	body := postFencedExecution(t, handler, state, "resolve", input)
	var output FencedExecutionResolveOutput
	if err := json.Unmarshal(body, &output.Body); err != nil {
		t.Fatal(err)
	}
	if output.Body.OK || output.Body.Error == nil || output.Body.Error.Code != "unsupported" {
		t.Fatalf("unsupported response = %s", body)
	}
	if provider.starts != 0 {
		t.Fatalf("unsupported provider effected %d starts", provider.starts)
	}
}

func TestFencedExecutionHTTPRejectsBodyCityMismatchBeforeProvider(t *testing.T) {
	state, provider, input := newAPIFencedFixture(t)
	handler := newTestCityHandler(t, state)
	input.CityID = "other-city"
	body := postFencedExecution(t, handler, state, "resolve", input)
	var output FencedExecutionResolveOutput
	if err := json.Unmarshal(body, &output.Body); err != nil {
		t.Fatal(err)
	}
	if output.Body.OK || output.Body.Error == nil || output.Body.Error.Code != "claim_conflict" || provider.starts != 0 {
		t.Fatalf("cross-city response=%s starts=%d", body, provider.starts)
	}
}

func postFencedExecution(t *testing.T, handler http.Handler, state State, action string, input worker.FencedExecutionInput) []byte {
	t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := newPostRequest(cityURL(state, "/temporal/fenced-executions/"+action), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST %s status=%d body=%s", action, recorder.Code, recorder.Body.String())
	}
	return recorder.Body.Bytes()
}

func newAPIFencedFixture(t *testing.T) (*fakeState, *apiFencedProvider, worker.FencedExecutionInput) {
	t.Helper()
	state := newFakeState(t)
	work := beads.NewMemStore()
	work.HonorExplicitIDs = true
	sessions := beads.NewMemStore()
	sessions.HonorExplicitIDs = true
	_, err := work.Create(beads.Bead{ID: "root-1", Title: "root", Type: "molecule", Metadata: map[string]string{"gc.formula_name": "fixture", "gc.formula_hash": strings.Repeat("a", 64), "gc.formula_contract": "graph.v2"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = work.Create(beads.Bead{ID: "step-1", Title: "step", Metadata: map[string]string{"gc.root_bead_id": "root-1", "gc.step_ref": "fixture.step", "gc.temporal.generation": "1", "gc.temporal.claim_token": "claim"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sessions.Create(beads.Bead{ID: "session-1", Title: "session", Type: "session", Labels: []string{"gc:session"}, Metadata: map[string]string{
		"session_name": "formula-worker", "configured_named_identity": "formula-worker", "generation": "2", "continuation_epoch": "3", "instance_token": "instance", "started_config_hash": "config", "command": "true", "work_dir": t.TempDir(), "provider": "subprocess",
	}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &apiFencedProvider{Fake: runtime.NewFake()}
	state.stores = map[string]beads.Store{"gas-city": work}
	state.cityBeadStore = sessions
	state.sessionsBeadStore = sessions
	state.sessionProvider = provider
	input := worker.FencedExecutionInput{CityID: state.cityName, RunID: "run-1", BeadID: "step-1", Generation: 1, FormulaName: "fixture", FormulaHash: strings.Repeat("a", 64), FormulaVersion: "1", FormulaRootID: "root-1", FormulaStepKey: "fixture.step", FormulaRig: "gas-city", ClaimToken: "claim", TargetSessionID: "session-1"}
	return state, provider, input
}
