package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
)

type recordingFencedProvider struct {
	*runtime.Fake
	mu         sync.Mutex
	starts     int
	stops      int
	waits      int
	receipt    runtime.FencedExecutionReceipt
	result     runtime.FencedExecutionResult
	beforeStop func() error
	startErr   error
	waitErr    error
	stopErr    error
}

func (p *recordingFencedProvider) StartOrAttachFenced(_ context.Context, req runtime.FencedExecutionRequest) (runtime.FencedExecutionReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts++
	if p.startErr != nil {
		return runtime.FencedExecutionReceipt{}, p.startErr
	}
	if p.receipt.ExecutionID == "" {
		p.receipt = runtime.FencedExecutionReceipt{
			ExecutionID: "fenced-one", OperationID: req.OperationID, RequestHash: req.RequestHash,
			Target: req.Target, Process: runtime.FencedProcessIdentity{PID: 101, ProcessGroupID: 101, StartIdentity: "start-1"},
		}
	} else {
		p.receipt.Attached = true
	}
	return p.receipt, nil
}

func (p *recordingFencedProvider) WaitFenced(_ context.Context, receipt runtime.FencedExecutionReceipt) (runtime.FencedExecutionResult, error) {
	p.mu.Lock()
	p.waits++
	p.mu.Unlock()
	if p.waitErr != nil {
		return runtime.FencedExecutionResult{}, p.waitErr
	}
	result := p.result
	result.Receipt = receipt
	return result, nil
}

func (p *recordingFencedProvider) RevokeAndStopFenced(_ context.Context, req runtime.FencedExecutionStopRequest) (runtime.FencedExecutionStopReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stops++
	if p.beforeStop != nil {
		if err := p.beforeStop(); err != nil {
			return runtime.FencedExecutionStopReceipt{}, err
		}
	}
	if p.stopErr != nil {
		return runtime.FencedExecutionStopReceipt{Receipt: req.Receipt, Revoked: true}, p.stopErr
	}
	return runtime.FencedExecutionStopReceipt{Receipt: req.Receipt, Revoked: true, Stopped: true}, nil
}

func TestFencedExecutionCoordinatorDurableReplayAndSecretFreeBinding(t *testing.T) {
	coordinator, workStore, sessionStore, provider, input := newFencedExecutionCoordinatorFixture(t)
	const rawClaimMarker = "super-secret-raw-claim-marker"
	input.ClaimToken = rawClaimMarker
	workStore.SetMetadata(input.BeadID, "gc.temporal.claim_token", rawClaimMarker) //nolint:errcheck

	first, err := coordinator.Resolve(context.Background(), input)
	if err != nil {
		t.Fatalf("Resolve(first): %v", err)
	}
	second, err := coordinator.Resolve(context.Background(), input)
	if err != nil {
		t.Fatalf("Resolve(replay): %v", err)
	}
	if first.ExecutionID != second.ExecutionID || provider.starts != 1 {
		t.Fatalf("replay receipts first=%+v second=%+v starts=%d", first, second, provider.starts)
	}
	work, err := workStore.Get(input.BeadID)
	if err != nil {
		t.Fatal(err)
	}
	binding := work.Metadata[FencedExecutionBindingMetadataKey]
	if binding == "" || strings.Contains(binding, rawClaimMarker) {
		t.Fatalf("durable binding missing or leaked raw claim: %q", binding)
	}
	var decoded FencedExecutionBinding
	if err := json.Unmarshal([]byte(binding), &decoded); err != nil {
		t.Fatalf("decode durable binding: %v", err)
	}
	if decoded.ClaimDigest == "" || decoded.RequestHash != first.RequestHash {
		t.Fatalf("durable binding = %+v", decoded)
	}

	provider.result = runtime.FencedExecutionResult{Outcome: "completed"}
	result, err := coordinator.Execute(context.Background(), input)
	if err != nil || result.Outcome != "completed" {
		t.Fatalf("Execute = %+v, %v", result, err)
	}
	result, err = coordinator.Execute(context.Background(), input)
	if err != nil || result.Outcome != "completed" || provider.waits != 1 {
		t.Fatalf("Execute replay = %+v, %v; waits=%d", result, err, provider.waits)
	}

	// Replacing any controller-owned authority field makes the old binding
	// unusable before another provider call can effect.
	if err := sessionStore.SetMetadata(input.TargetSessionID, "instance_token", "replacement"); err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.Resolve(context.Background(), input)
	if !errors.Is(err, ErrFencedExecutionAuthorityChanged) {
		t.Fatalf("Resolve after authority replacement = %v", err)
	}
	if provider.starts != 1 {
		t.Fatalf("provider starts after stale authority = %d, want 1", provider.starts)
	}
}

func TestFencedExecutionCoordinatorConstructionAndIdentityValidation(t *testing.T) {
	coordinator, workStore, sessionStore, provider, input := newFencedExecutionCoordinatorFixture(t)
	if _, err := NewFencedExecutionCoordinator(FencedExecutionCoordinatorConfig{}); err == nil {
		t.Fatal("empty coordinator configuration succeeded")
	}
	constructed, err := NewFencedExecutionCoordinator(FencedExecutionCoordinatorConfig{WorkStore: workStore, SessionStore: sessionStore, Provider: provider})
	if err != nil || constructed == nil {
		t.Fatalf("NewFencedExecutionCoordinator = %v, %v", constructed, err)
	}
	for name, mutate := range map[string]func(*FencedExecutionInput){
		"generation": func(candidate *FencedExecutionInput) { candidate.Generation = 0 },
		"city":       func(candidate *FencedExecutionInput) { candidate.CityID = "" },
		"hash":       func(candidate *FencedExecutionInput) { candidate.FormulaHash = "short" },
		"step":       func(candidate *FencedExecutionInput) { candidate.FormulaStepKey = "other" },
		"root":       func(candidate *FencedExecutionInput) { candidate.FormulaRootID = "missing" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := input
			mutate(&candidate)
			if _, err := coordinator.Resolve(context.Background(), candidate); err == nil {
				t.Fatal("invalid identity succeeded")
			}
		})
	}
	if provider.starts != 0 {
		t.Fatalf("invalid identities reached provider %d times", provider.starts)
	}
}

func TestFencedExecutionCoordinatorFailClosedBindingBranches(t *testing.T) {
	t.Run("malformed binding", func(t *testing.T) {
		coordinator, store, _, _, input := newFencedExecutionCoordinatorFixture(t)
		store.SetMetadata(input.BeadID, FencedExecutionBindingMetadataKey, "not-json") //nolint:errcheck
		if _, err := coordinator.Resolve(context.Background(), input); err == nil {
			t.Fatal("malformed binding succeeded")
		}
	})
	t.Run("changed request", func(t *testing.T) {
		coordinator, _, _, _, input := newFencedExecutionCoordinatorFixture(t)
		if _, err := coordinator.Resolve(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		input.RunID = "replacement"
		if _, err := coordinator.Resolve(context.Background(), input); !errors.Is(err, runtime.ErrFencedExecutionConflict) {
			t.Fatalf("changed request error=%v", err)
		}
	})
	t.Run("cancel identities", func(t *testing.T) {
		coordinator, store, _, _, input := newFencedExecutionCoordinatorFixture(t)
		if _, err := coordinator.prepareCancel(FencedExecutionCancelInput{}); err == nil {
			t.Fatal("empty cancellation succeeded")
		}
		cancel := FencedExecutionCancelInput{CityID: input.CityID, FormulaRig: input.FormulaRig, BeadID: input.BeadID, Generation: input.Generation, ClaimToken: input.ClaimToken, TargetSessionID: input.TargetSessionID, ExecutionID: "execution"}
		if _, err := coordinator.prepareCancel(cancel); !errors.Is(err, runtime.ErrFencedExecutionUnknown) {
			t.Fatalf("missing binding error=%v", err)
		}
		store.SetMetadata(input.BeadID, FencedExecutionBindingMetadataKey, "bad") //nolint:errcheck
		if _, err := coordinator.prepareCancel(cancel); err == nil {
			t.Fatal("malformed cancellation binding succeeded")
		}
		cancel.ClaimToken = "wrong"
		if _, err := coordinator.prepareCancel(cancel); !errors.Is(err, ErrFencedExecutionClaimConflict) {
			t.Fatalf("wrong cancel claim error=%v", err)
		}
	})
}

func TestFencedExecutionCoordinatorCancelUsesOnlyExactDurableIdentity(t *testing.T) {
	coordinator, workStore, _, provider, input := newFencedExecutionCoordinatorFixture(t)
	receipt, err := coordinator.Resolve(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	cancel := FencedExecutionCancelInput{CityID: input.CityID, FormulaRig: input.FormulaRig, BeadID: input.BeadID, Generation: input.Generation, ClaimToken: input.ClaimToken, TargetSessionID: input.TargetSessionID, ExecutionID: receipt.ExecutionID}
	provider.beforeStop = func() error {
		work, err := workStore.Get(input.BeadID)
		if err != nil {
			return err
		}
		var binding FencedExecutionBinding
		if err := json.Unmarshal([]byte(work.Metadata[FencedExecutionBindingMetadataKey]), &binding); err != nil {
			return err
		}
		if binding.State != "revoking" {
			return errors.New("provider stop reached before durable revocation")
		}
		return nil
	}
	stopped, err := coordinator.Cancel(context.Background(), cancel)
	if err != nil || !stopped.Revoked || provider.stops != 1 {
		t.Fatalf("Cancel = %+v, %v; stops=%d", stopped, err, provider.stops)
	}
	replayed, err := coordinator.Cancel(context.Background(), cancel)
	if err != nil || !replayed.Stopped || provider.stops != 1 {
		t.Fatalf("Cancel replay = %+v, %v; stops=%d", replayed, err, provider.stops)
	}
	cancel.ExecutionID = "replacement"
	_, err = coordinator.Cancel(context.Background(), cancel)
	if !errors.Is(err, runtime.ErrFencedExecutionStaleAuthority) || provider.stops != 1 {
		t.Fatalf("stale Cancel error=%v stops=%d", err, provider.stops)
	}
}

func TestFencedExecutionCoordinatorPreservesProviderFailures(t *testing.T) {
	coordinator, _, _, provider, input := newFencedExecutionCoordinatorFixture(t)
	provider.startErr = errors.New("start unavailable")
	if _, err := coordinator.Resolve(context.Background(), input); !errors.Is(err, provider.startErr) {
		t.Fatalf("Resolve start error=%v", err)
	}
	provider.startErr = nil
	provider.waitErr = errors.New("wait unavailable")
	if _, err := coordinator.Execute(context.Background(), input); !errors.Is(err, provider.waitErr) {
		t.Fatalf("Execute wait error=%v", err)
	}
}

func TestFencedExecutionCoordinatorCancelReplaysRevokingCrashWindow(t *testing.T) {
	coordinator, workStore, _, provider, input := newFencedExecutionCoordinatorFixture(t)
	receipt, err := coordinator.Resolve(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	cancel := FencedExecutionCancelInput{CityID: input.CityID, FormulaRig: input.FormulaRig, BeadID: input.BeadID, Generation: input.Generation, ClaimToken: input.ClaimToken, TargetSessionID: input.TargetSessionID, ExecutionID: receipt.ExecutionID}
	provider.stopErr = errors.New("stop receipt lost")
	if _, err := coordinator.Cancel(context.Background(), cancel); err == nil {
		t.Fatal("Cancel unexpectedly succeeded")
	}
	work, err := workStore.Get(input.BeadID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(work.Metadata[FencedExecutionBindingMetadataKey], `"state":"revoking"`) {
		t.Fatalf("binding after uncertain stop = %s", work.Metadata[FencedExecutionBindingMetadataKey])
	}
	provider.stopErr = nil
	stopped, err := coordinator.Cancel(context.Background(), cancel)
	if err != nil || !stopped.Stopped || provider.stops != 2 {
		t.Fatalf("Cancel replay = %+v, %v; stops=%d", stopped, err, provider.stops)
	}
}

func TestFencedExecutionCoordinatorRejectsClaimConflictAndUnsupportedProvider(t *testing.T) {
	coordinator, _, _, provider, input := newFencedExecutionCoordinatorFixture(t)
	input.ClaimToken = "wrong"
	_, err := coordinator.Resolve(context.Background(), input)
	if !errors.Is(err, ErrFencedExecutionClaimConflict) {
		t.Fatalf("wrong claim error = %v", err)
	}
	if provider.starts != 0 {
		t.Fatalf("wrong claim reached provider %d times", provider.starts)
	}

	coordinator.provider = runtime.NewFake()
	input.ClaimToken = "claim-capability"
	_, err = coordinator.Resolve(context.Background(), input)
	if !errors.Is(err, runtime.ErrFencedExecutionUnsupported) {
		t.Fatalf("unsupported provider error = %v", err)
	}
}

func newFencedExecutionCoordinatorFixture(t *testing.T) (*FencedExecutionCoordinator, *beads.MemStore, *beads.MemStore, *recordingFencedProvider, FencedExecutionInput) {
	t.Helper()
	workStore := beads.NewMemStore()
	workStore.HonorExplicitIDs = true
	sessionStore := beads.NewMemStore()
	sessionStore.HonorExplicitIDs = true
	_, err := workStore.Create(beads.Bead{ID: "root-1", Title: "root", Type: "molecule", Metadata: map[string]string{
		"gc.formula_name": "fixture", "gc.formula_hash": strings.Repeat("a", 64), "gc.formula_contract": "graph.v2",
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = workStore.Create(beads.Bead{ID: "step-1", Title: "step", Metadata: map[string]string{
		"gc.root_bead_id": "root-1", "gc.step_ref": "fixture.step-one",
		"gc.temporal.generation": "7", "gc.temporal.claim_token": "claim-capability",
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sessionStore.Create(beads.Bead{ID: "session-1", Title: "worker", Type: "session", Labels: []string{"gc:session"}, Metadata: map[string]string{
		"session_name": "worker-one", "state": "awake", "command": `printf '{"outcome":"completed"}'`,
		"work_dir": t.TempDir(), "generation": "4", "continuation_epoch": "2", "instance_token": "instance-one",
		"started_config_hash": "config-one", "provider": "subprocess", "configured_named_identity": "formula-worker",
	}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &recordingFencedProvider{Fake: runtime.NewFake()}
	coordinator := &FencedExecutionCoordinator{workStore: workStore, sessionStore: sessionStore, provider: provider}
	input := FencedExecutionInput{
		CityID: "city", RunID: "run", BeadID: "step-1", Generation: 7,
		FormulaName: "fixture", FormulaHash: strings.Repeat("a", 64), FormulaVersion: "1",
		FormulaRootID: "root-1", FormulaStepKey: "fixture.step-one", FormulaRig: "gas-city",
		ClaimToken: "claim-capability", TargetSessionID: "session-1",
	}
	return coordinator, workStore, sessionStore, provider, input
}
