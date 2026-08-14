package subprocess

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

func TestFencedExecutionStableReplayConflictAndTerminalResult(t *testing.T) {
	p := NewProviderWithDir(t.TempDir())
	req := runtime.FencedExecutionRequest{
		OperationID: "formula/root/step/7",
		RequestHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Target: runtime.FencedExecutionTarget{
			SessionID:          "gc-session-1",
			SessionName:        "worker-1",
			ConfiguredIdentity: "formula-worker",
			Generation:         "4",
			ContinuationEpoch:  "2",
			InstanceToken:      "instance-token-digest",
			StartedConfigHash:  "config-digest",
		},
		Config: runtime.Config{Command: `printf '%s\n' '{"outcome":"completed","artifact_refs":[{"kind":"file","uri":"artifact://one","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}'`},
	}

	first, err := p.StartOrAttachFenced(context.Background(), req)
	if err != nil {
		t.Fatalf("StartOrAttachFenced(first): %v", err)
	}
	if first.Process.PID <= 1 || first.Process.ProcessGroupID != first.Process.PID || first.Process.StartIdentity == "" {
		t.Fatalf("first process identity = %+v, want exact pid/start/group", first.Process)
	}
	second, err := p.StartOrAttachFenced(context.Background(), req)
	if err != nil {
		t.Fatalf("StartOrAttachFenced(replay): %v", err)
	}
	wantAttached := first
	wantAttached.Attached = true
	if second != wantAttached || !second.Attached {
		t.Fatalf("replay receipt = %+v, want attached exact %+v", second, first)
	}

	conflict := req
	conflict.RequestHash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	_, err = p.StartOrAttachFenced(context.Background(), conflict)
	if !errors.Is(err, runtime.ErrFencedExecutionConflict) {
		t.Fatalf("changed request error = %v, want ErrFencedExecutionConflict", err)
	}

	result, err := p.WaitFenced(context.Background(), first)
	if err != nil {
		t.Fatalf("WaitFenced: %v", err)
	}
	if result.Outcome != "completed" || len(result.ArtifactRefs) != 1 {
		t.Fatalf("terminal result = %+v", result)
	}
}

func TestFencedExecutionRevokesBeforeExactCompareAndStop(t *testing.T) {
	p := NewProviderWithDir(t.TempDir())
	req := runtime.FencedExecutionRequest{
		OperationID: "formula/root/step/8",
		RequestHash: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Target: runtime.FencedExecutionTarget{
			SessionID: "gc-session-2", SessionName: "worker-2", ConfiguredIdentity: "formula-worker", Generation: "1",
			ContinuationEpoch: "1", InstanceToken: "instance-digest", StartedConfigHash: "config-digest",
		},
		Config: runtime.Config{Command: "while :; do :; done"},
	}
	receipt, err := p.StartOrAttachFenced(context.Background(), req)
	if err != nil {
		t.Fatalf("StartOrAttachFenced: %v", err)
	}

	stale := receipt
	stale.Process.StartIdentity = "replacement"
	_, err = p.RevokeAndStopFenced(context.Background(), runtime.FencedExecutionStopRequest{Receipt: stale})
	if !errors.Is(err, runtime.ErrFencedExecutionStaleAuthority) {
		t.Fatalf("stale stop error = %v, want ErrFencedExecutionStaleAuthority", err)
	}
	if !p.fencedExecutionRunning(receipt) {
		t.Fatal("stale authority stopped the live execution")
	}

	stopped, err := p.RevokeAndStopFenced(context.Background(), runtime.FencedExecutionStopRequest{Receipt: receipt})
	if err != nil {
		t.Fatalf("RevokeAndStopFenced: %v", err)
	}
	if !stopped.Revoked || !stopped.Stopped {
		t.Fatalf("stop receipt = %+v, want revoked and stopped", stopped)
	}
	_, err = p.StartOrAttachFenced(context.Background(), req)
	if !errors.Is(err, runtime.ErrFencedExecutionRevoked) {
		t.Fatalf("replay after revoke error = %v, want ErrFencedExecutionRevoked", err)
	}
}

func TestFencedExecutionContractNeverAcceptsRawClaimCapability(t *testing.T) {
	requestType := reflect.TypeOf(runtime.FencedExecutionRequest{})
	for i := 0; i < requestType.NumField(); i++ {
		if strings.Contains(strings.ToLower(requestType.Field(i).Name), "claim") {
			t.Fatalf("provider request exposes claim field %q", requestType.Field(i).Name)
		}
	}
}

func TestFencedExecutionRestartReconstructsExactStopAuthority(t *testing.T) {
	p := NewProviderWithDir(t.TempDir())
	req := runtime.FencedExecutionRequest{OperationID: "restart-stop", RequestHash: strings.Repeat("a", 64), Target: runtime.FencedExecutionTarget{SessionID: "s", SessionName: "n", ConfiguredIdentity: "i", Generation: "1", ContinuationEpoch: "1", InstanceToken: "t", StartedConfigHash: "h"}, Config: runtime.Config{Command: "sleep 30"}}
	receipt, err := p.StartOrAttachFenced(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	delete(p.fenced, req.OperationID)
	p.mu.Unlock()
	stopped, err := p.RevokeAndStopFenced(context.Background(), runtime.FencedExecutionStopRequest{Receipt: receipt})
	if err != nil || !stopped.Stopped {
		t.Fatalf("restart stop=%+v err=%v", stopped, err)
	}
	deadline := time.Now().Add(time.Second)
	for syscall.Kill(receipt.Process.PID, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(receipt.Process.PID, 0); err == nil {
		t.Fatal("recovered stop left process present")
	}
}

func TestFencedExecutionFailsClosedOnInvalidRequestsAndTerminalOutput(t *testing.T) {
	p := NewProviderWithDir(t.TempDir())
	base := runtime.FencedExecutionRequest{OperationID: "operation", RequestHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Target: runtime.FencedExecutionTarget{
		SessionID: "session", SessionName: "worker", ConfiguredIdentity: "formula-worker", Generation: "1", ContinuationEpoch: "1", InstanceToken: "instance", StartedConfigHash: "config",
	}}
	for name, mutate := range map[string]func(*runtime.FencedExecutionRequest){
		"hash":    func(req *runtime.FencedExecutionRequest) { req.RequestHash = "short" },
		"target":  func(req *runtime.FencedExecutionRequest) { req.Target.InstanceToken = "" },
		"command": func(req *runtime.FencedExecutionRequest) { req.Config.Command = "" },
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			mutate(&req)
			if _, err := p.StartOrAttachFenced(context.Background(), req); err == nil {
				t.Fatal("invalid request succeeded")
			}
		})
	}
	for name, command := range map[string]string{
		"exit":          "exit 2",
		"malformed":     `printf 'not-json'`,
		"multiple":      `printf '%s\n%s\n' '{"outcome":"completed"}' '{"outcome":"again"}'`,
		"empty-outcome": `printf '%s\n' '{"outcome":""}'`,
		"overflow":      `head -c 1048577 /dev/zero`,
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			req.OperationID = "operation-" + name
			req.Config.Command = command
			receipt, err := p.StartOrAttachFenced(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.WaitFenced(context.Background(), receipt); err == nil {
				t.Fatal("invalid terminal output succeeded")
			}
		})
	}
}

func TestFencedExecutionWaitHonorsContext(t *testing.T) {
	p := NewProviderWithDir(t.TempDir())
	req := runtime.FencedExecutionRequest{OperationID: "operation-wait", RequestHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Target: runtime.FencedExecutionTarget{
		SessionID: "session", SessionName: "worker", ConfiguredIdentity: "formula-worker", Generation: "1", ContinuationEpoch: "1", InstanceToken: "instance", StartedConfigHash: "config",
	}, Config: runtime.Config{Command: "sleep 30"}}
	receipt, err := p.StartOrAttachFenced(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.WaitFenced(ctx, receipt); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitFenced error=%v", err)
	}
	_, _ = p.RevokeAndStopFenced(context.Background(), runtime.FencedExecutionStopRequest{Receipt: receipt})
}

func TestFencedExecutionEnvironmentAndStderrAreSecretSafe(t *testing.T) {
	t.Setenv("CONTROLLER_SECRET_MARKER", "must-not-inherit")
	env := mergedExecutionEnv(map[string]string{"SAFE_INPUT": "allowed"})
	if strings.Join(env, "\n") != "SAFE_INPUT=allowed" {
		t.Fatalf("child env=%q", env)
	}
	p := NewProviderWithDir(t.TempDir())
	req := runtime.FencedExecutionRequest{OperationID: "stderr", RequestHash: strings.Repeat("a", 64), Target: runtime.FencedExecutionTarget{SessionID: "s", SessionName: "n", ConfiguredIdentity: "i", Generation: "1", ContinuationEpoch: "1", InstanceToken: "t", StartedConfigHash: "h"}, Config: runtime.Config{Command: "echo super-secret-stderr-marker >&2; exit 2"}}
	receipt, err := p.StartOrAttachFenced(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.WaitFenced(context.Background(), receipt)
	if err == nil || strings.Contains(err.Error(), "super-secret-stderr-marker") || !strings.Contains(err.Error(), "stderr_sha256") {
		t.Fatalf("stderr diagnostic=%v", err)
	}
}
