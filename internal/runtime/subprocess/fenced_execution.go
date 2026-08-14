package subprocess

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

const (
	maxFencedExecutionResultBytes = 1024 * 1024
	maxFencedExecutionTombstones  = 1024
)

var fencedExecutionHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type fencedExecution struct {
	receipt runtime.FencedExecutionReceipt
	cmd     *exec.Cmd
	done    chan struct{}
	output  boundedExecutionBuffer
	stderr  boundedExecutionBuffer
	err     error
	revoked bool
}

type boundedExecutionBuffer struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	overflow bool
}

func (b *boundedExecutionBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := maxFencedExecutionResultBytes + 1 - b.buf.Len()
	if remaining > 0 {
		chunk := p
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		_, _ = b.buf.Write(chunk)
	}
	if b.buf.Len() > maxFencedExecutionResultBytes || len(p) > remaining {
		b.overflow = true
	}
	return len(p), nil
}

func (b *boundedExecutionBuffer) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...), b.overflow
}

func (p *Provider) initFencedExecutions() {
	if p.fenced == nil {
		p.fenced = make(map[string]*fencedExecution)
	}
}

// StartOrAttachFenced starts or attaches to one operation-keyed child process.
func (p *Provider) StartOrAttachFenced(_ context.Context, req runtime.FencedExecutionRequest) (runtime.FencedExecutionReceipt, error) {
	if err := validateFencedExecutionRequest(req); err != nil {
		return runtime.FencedExecutionReceipt{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initFencedExecutions()
	if existing := p.fenced[req.OperationID]; existing != nil {
		if existing.receipt.RequestHash != req.RequestHash {
			return runtime.FencedExecutionReceipt{}, runtime.ErrFencedExecutionConflict
		}
		if existing.revoked {
			return runtime.FencedExecutionReceipt{}, runtime.ErrFencedExecutionRevoked
		}
		receipt := existing.receipt
		receipt.Attached = true
		return receipt, nil
	}

	command := strings.TrimSpace(req.Config.Command)
	if command == "" {
		return runtime.FencedExecutionReceipt{}, fmt.Errorf("fenced execution command is required")
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = strings.TrimSpace(req.Config.WorkDir)
	cmd.Env = mergedExecutionEnv(req.Config.Env)
	done := make(chan struct{})
	execution := &fencedExecution{cmd: cmd, done: done}
	cmd.Stdout = &execution.output
	cmd.Stderr = &execution.stderr
	if err := p.ops.start(cmd); err != nil {
		return runtime.FencedExecutionReceipt{}, fmt.Errorf("starting fenced execution: %w", err)
	}
	startID, err := fencedProcessStartIdentity(cmd.Process.Pid)
	if err != nil {
		_ = runtime.SignalProcessGroup(cmd, syscall.SIGKILL)
		_ = cmd.Wait()
		return runtime.FencedExecutionReceipt{}, fmt.Errorf("reading fenced execution start identity: %w", err)
	}
	executionID := fencedExecutionID(req.OperationID)
	execution.receipt = runtime.FencedExecutionReceipt{
		ExecutionID: executionID,
		OperationID: req.OperationID,
		RequestHash: req.RequestHash,
		Target:      req.Target,
		Process: runtime.FencedProcessIdentity{
			PID: cmd.Process.Pid, ProcessGroupID: cmd.Process.Pid, StartIdentity: startID,
		},
	}
	p.fenced[req.OperationID] = execution
	p.pruneFencedTombstonesLocked()
	go func() {
		execution.err = cmd.Wait()
		close(done)
	}()
	return execution.receipt, nil
}

func (p *Provider) pruneFencedTombstonesLocked() {
	for len(p.fenced) > maxFencedExecutionTombstones {
		removed := false
		for operationID, execution := range p.fenced {
			select {
			case <-execution.done:
				delete(p.fenced, operationID)
				removed = true
			default:
			}
			if removed {
				break
			}
		}
		if !removed {
			return
		}
	}
}

// WaitFenced waits for the exact child and decodes its closed terminal result.
func (p *Provider) WaitFenced(ctx context.Context, receipt runtime.FencedExecutionReceipt) (runtime.FencedExecutionResult, error) {
	p.mu.Lock()
	execution, err := p.exactFencedExecutionLocked(receipt)
	p.mu.Unlock()
	if err != nil {
		return runtime.FencedExecutionResult{}, err
	}
	select {
	case <-ctx.Done():
		return runtime.FencedExecutionResult{}, ctx.Err()
	case <-execution.done:
	}
	p.mu.Lock()
	revoked := execution.revoked
	p.mu.Unlock()
	if revoked {
		return runtime.FencedExecutionResult{}, runtime.ErrFencedExecutionRevoked
	}
	if execution.err != nil {
		stderr, _ := execution.stderr.snapshot()
		digest := sha256.Sum256(stderr)
		return runtime.FencedExecutionResult{}, fmt.Errorf("fenced execution exited: %w (stderr_bytes=%d stderr_sha256=%x)", execution.err, len(stderr), digest)
	}
	data, overflow := execution.output.snapshot()
	if overflow {
		return runtime.FencedExecutionResult{}, fmt.Errorf("fenced execution result exceeds %d bytes", maxFencedExecutionResultBytes)
	}
	var terminal struct {
		Outcome      string                            `json:"outcome"`
		ArtifactRefs []runtime.FencedExecutionArtifact `json:"artifact_refs,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&terminal); err != nil {
		return runtime.FencedExecutionResult{}, fmt.Errorf("decode fenced execution result: %w", err)
	}
	if err := requireExecutionJSONEOF(decoder); err != nil {
		return runtime.FencedExecutionResult{}, err
	}
	if strings.TrimSpace(terminal.Outcome) == "" {
		return runtime.FencedExecutionResult{}, fmt.Errorf("fenced execution result outcome is required")
	}
	return runtime.FencedExecutionResult{Receipt: receipt, Outcome: terminal.Outcome, ArtifactRefs: terminal.ArtifactRefs}, nil
}

// RevokeAndStopFenced revokes replay before stopping the exact process group.
func (p *Provider) RevokeAndStopFenced(_ context.Context, req runtime.FencedExecutionStopRequest) (runtime.FencedExecutionStopReceipt, error) {
	p.mu.Lock()
	execution, err := p.exactFencedExecutionLocked(req.Receipt)
	if err != nil {
		p.mu.Unlock()
		if errors.Is(err, runtime.ErrFencedExecutionUnknown) {
			return revokeRecoveredFencedExecution(req.Receipt)
		}
		return runtime.FencedExecutionStopReceipt{}, err
	}
	// Revocation is committed in controller memory before physical signaling.
	// Replays can never attach once the process-stop request has begun.
	execution.revoked = true
	p.mu.Unlock()

	select {
	case <-execution.done:
	default:
		if err := runtime.TerminateManagedProcess(execution.cmd, execution.done, runtime.ManagedProcessStopGrace); err != nil {
			return runtime.FencedExecutionStopReceipt{Receipt: req.Receipt, Revoked: true}, fmt.Errorf("stop fenced execution: %w", err)
		}
	}
	execution.cmd = nil
	execution.output = boundedExecutionBuffer{}
	execution.stderr = boundedExecutionBuffer{}
	return runtime.FencedExecutionStopReceipt{Receipt: req.Receipt, Revoked: true, Stopped: true}, nil
}

func (p *Provider) exactFencedExecutionLocked(receipt runtime.FencedExecutionReceipt) (*fencedExecution, error) {
	p.initFencedExecutions()
	execution := p.fenced[receipt.OperationID]
	if execution == nil {
		return nil, runtime.ErrFencedExecutionUnknown
	}
	if execution.receipt.ExecutionID != receipt.ExecutionID || execution.receipt.RequestHash != receipt.RequestHash ||
		execution.receipt.Process != receipt.Process || execution.receipt.Target != receipt.Target {
		return nil, runtime.ErrFencedExecutionStaleAuthority
	}
	return execution, nil
}

func (p *Provider) fencedExecutionRunning(receipt runtime.FencedExecutionReceipt) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	execution, err := p.exactFencedExecutionLocked(receipt)
	if err != nil {
		return false
	}
	select {
	case <-execution.done:
		return false
	default:
		return true
	}
}

func validateFencedExecutionRequest(req runtime.FencedExecutionRequest) error {
	if strings.TrimSpace(req.OperationID) == "" || !fencedExecutionHashPattern.MatchString(req.RequestHash) {
		return fmt.Errorf("fenced execution operation and 64-character request hash are required")
	}
	for name, value := range map[string]string{
		"session id": req.Target.SessionID, "session name": req.Target.SessionName,
		"configured identity": req.Target.ConfiguredIdentity,
		"generation":          req.Target.Generation, "continuation epoch": req.Target.ContinuationEpoch,
		"instance token": req.Target.InstanceToken, "started config hash": req.Target.StartedConfigHash,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("fenced execution target %s is required", name)
		}
	}
	return nil
}

func fencedExecutionID(operationID string) string {
	sum := sha256.Sum256([]byte(operationID))
	return "fenced-" + hex.EncodeToString(sum[:12])
}

func mergedExecutionEnv(overrides map[string]string) []string {
	env := make([]string, 0, len(overrides))
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+overrides[key])
	}
	return env
}

func requireExecutionJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode fenced execution result: %w", err)
	}
	return fmt.Errorf("decode fenced execution result: multiple JSON values")
}

var _ runtime.FencedExecutionProvider = (*Provider)(nil)

// Keep time imported on every supported platform while platform-specific
// process identity helpers normalize the value to text.
var _ = time.Time{}
