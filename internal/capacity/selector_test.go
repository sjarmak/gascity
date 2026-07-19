package capacity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordingRunner captures the env a selector command would receive.
type recordingRunner struct {
	out     string
	err     error
	command string
	env     map[string]string
	calls   int
}

func (r *recordingRunner) run(_ context.Context, command string, env map[string]string) (string, error) {
	r.calls++
	r.command = command
	r.env = env
	return r.out, r.err
}

func TestCommandSelector_ReturnsTrimmedAccount(t *testing.T) {
	rr := &recordingRunner{out: "3\n"}
	sel := NewCommandSelector("csu_pick.sh", WithRunner(rr.run))

	got, err := sel.Pick(context.Background(), PickRequest{Agent: "worker"})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got != "3" {
		t.Fatalf("account = %q, want %q", got, "3")
	}
	if rr.command != "csu_pick.sh" {
		t.Fatalf("command = %q", rr.command)
	}
}

// The SDK must not interpret the account identifier: any opaque token passes.
func TestCommandSelector_AccountIsOpaque(t *testing.T) {
	for _, out := range []string{"3", "claude-2", "acct_a1b2", "pool-west/7"} {
		rr := &recordingRunner{out: out + "\n"}
		sel := NewCommandSelector("pick", WithRunner(rr.run))
		got, err := sel.Pick(context.Background(), PickRequest{Agent: "worker"})
		if err != nil {
			t.Fatalf("Pick(%q): %v", out, err)
		}
		if got != out {
			t.Fatalf("account = %q, want %q", got, out)
		}
	}
}

func TestCommandSelector_PassesExclusionsAndContext(t *testing.T) {
	rr := &recordingRunner{out: "5"}
	sel := NewCommandSelector("pick", WithRunner(rr.run))

	_, err := sel.Pick(context.Background(), PickRequest{
		Agent:    "worker",
		Provider: "claude",
		Exclude:  []string{"1", "2"},
	})
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got := rr.env[EnvAccountExclude]; got != "1,2" {
		t.Fatalf("%s = %q, want %q", EnvAccountExclude, got, "1,2")
	}
	if got := rr.env[EnvAccountAgent]; got != "worker" {
		t.Fatalf("%s = %q", EnvAccountAgent, got)
	}
	if got := rr.env[EnvAccountProvider]; got != "claude" {
		t.Fatalf("%s = %q", EnvAccountProvider, got)
	}
}

func TestCommandSelector_EmptyExcludeIsEmptyString(t *testing.T) {
	rr := &recordingRunner{out: "1"}
	sel := NewCommandSelector("pick", WithRunner(rr.run))
	if _, err := sel.Pick(context.Background(), PickRequest{Agent: "worker"}); err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if got, ok := rr.env[EnvAccountExclude]; !ok || got != "" {
		t.Fatalf("%s = %q, present=%v; want empty string", EnvAccountExclude, got, ok)
	}
}

// Structural validation at the trust boundary.
func TestCommandSelector_RejectsMalformedOutput(t *testing.T) {
	tests := []struct {
		name string
		out  string
	}{
		{"empty", ""},
		{"whitespace only", "   \n  "},
		{"multiple tokens", "1 2"},
		{"multiple lines", "1\n2\n"},
		{"embedded tab", "1\t2"},
		{"overlong", strings.Repeat("x", maxAccountLen+1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := &recordingRunner{out: tc.out}
			sel := NewCommandSelector("pick", WithRunner(rr.run))
			if _, err := sel.Pick(context.Background(), PickRequest{Agent: "worker"}); err == nil {
				t.Fatalf("Pick(%q): err = nil, want validation error", tc.out)
			}
		})
	}
}

// A failing selector must propagate, never silently fall back to an account.
func TestCommandSelector_PropagatesRunnerError(t *testing.T) {
	boom := errors.New("command failed")
	rr := &recordingRunner{err: boom}
	sel := NewCommandSelector("pick", WithRunner(rr.run))

	_, err := sel.Pick(context.Background(), PickRequest{Agent: "worker"})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped runner error", err)
	}
}

func TestShellRunnerDoesNotStartWithCanceledContext(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := shellRunner(ctx, fmt.Sprintf("touch %q", marker), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("shellRunner error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker stat error = %v, want os.ErrNotExist", err)
	}
}

func TestShellRunnerReturnsDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := shellRunner(ctx, "sleep 10 & wait", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shellRunner error = %v, want context.DeadlineExceeded", err)
	}
}

// --- ledger integration ---

// stubSelector returns a fixed account and records the exclusions it saw.
type stubSelector struct {
	account  string
	err      error
	lastReq  PickRequest
	calls    int
	sequence []string
}

func (s *stubSelector) Pick(_ context.Context, req PickRequest) (string, error) {
	s.lastReq = req
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	if len(s.sequence) > 0 {
		a := s.sequence[0]
		s.sequence = s.sequence[1:]
		return a, nil
	}
	return s.account, nil
}

// With no selector configured there is no account dimension at all.
func TestReserve_NoSelectorLeavesAccountEmpty(t *testing.T) {
	l, _ := newTestLedger(t)
	got, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{}))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got.Account != "" {
		t.Fatalf("Account = %q, want empty (no selector configured)", got.Account)
	}
}

func TestReserve_StampsSelectedAccount(t *testing.T) {
	sel := &stubSelector{account: "2"}
	l, _ := newTestLedger(t, WithSelector(sel.Pick))

	got, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{}))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got.Account != "2" {
		t.Fatalf("Account = %q, want %q", got.Account, "2")
	}
	if sel.lastReq.Agent != "worker" {
		t.Fatalf("selector saw agent %q", sel.lastReq.Agent)
	}
}

// The ledger tells the install-side selector which accounts it has already
// saturated; the selector picks among the rest.
func TestReserve_ExcludesSaturatedAccounts(t *testing.T) {
	sel := &stubSelector{sequence: []string{"1", "2"}}
	l, _ := newTestLedger(t, WithSelector(sel.Pick))
	ctx := context.Background()
	caps := Caps{Account: intp(1)}

	if _, err := l.Reserve(ctx, req("gc-1", "worker", caps)); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := l.Reserve(ctx, req("gc-2", "worker", caps)); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := sel.lastReq.Exclude; len(got) != 1 || got[0] != "1" {
		t.Fatalf("Exclude = %v, want [1] (account 1 is at its cap)", got)
	}
}

// Fail closed: if the selector returns a saturated account anyway, the
// reservation is refused rather than oversubscribing it.
func TestReserve_FailsClosedOnSaturatedPick(t *testing.T) {
	sel := &stubSelector{account: "1"} // always returns the same account
	l, _ := newTestLedger(t, WithSelector(sel.Pick))
	ctx := context.Background()
	caps := Caps{Account: intp(1)}

	if _, err := l.Reserve(ctx, req("gc-1", "worker", caps)); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := l.Reserve(ctx, req("gc-2", "worker", caps))
	if !errors.Is(err, ErrAccountCapReached) {
		t.Fatalf("err = %v, want ErrAccountCapReached", err)
	}
}

// No account available is a rejection, never a silent unaccounted reservation.
func TestReserve_SelectorErrorRejects(t *testing.T) {
	sel := &stubSelector{err: errors.New("all accounts exhausted")}
	l, _ := newTestLedger(t, WithSelector(sel.Pick))

	_, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{}))
	if !errors.Is(err, ErrNoAccountAvailable) {
		t.Fatalf("err = %v, want ErrNoAccountAvailable", err)
	}
	snap, _ := l.Snapshot()
	if snap.Total != 0 {
		t.Fatalf("Total = %d, want 0 (a failed pick must not leak a reservation)", snap.Total)
	}
}

func TestReserve_SelectorEmptyAccountRejects(t *testing.T) {
	sel := &stubSelector{account: ""}
	l, _ := newTestLedger(t, WithSelector(sel.Pick))
	if _, err := l.Reserve(context.Background(), req("gc-1", "worker", Caps{})); !errors.Is(err, ErrNoAccountAvailable) {
		t.Fatalf("err = %v, want ErrNoAccountAvailable", err)
	}
}

func TestSnapshot_CountsByAccount(t *testing.T) {
	sel := &stubSelector{sequence: []string{"1", "1", "2"}}
	l, _ := newTestLedger(t, WithSelector(sel.Pick))
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		if _, err := l.Reserve(ctx, req(fmt.Sprintf("gc-%d", i), "worker", Caps{})); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	snap, err := l.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.ByAccount["1"] != 2 || snap.ByAccount["2"] != 1 {
		t.Fatalf("ByAccount = %v, want {1:2, 2:1}", snap.ByAccount)
	}
}

// The idempotent re-reserve path must not burn a selector call.
func TestReserve_IdempotentSkipsSelector(t *testing.T) {
	sel := &stubSelector{account: "1"}
	l, _ := newTestLedger(t, WithSelector(sel.Pick))
	ctx := context.Background()

	if _, err := l.Reserve(ctx, req("gc-1", "worker", Caps{})); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := l.Reserve(ctx, req("gc-1", "worker", Caps{})); err != nil {
		t.Fatalf("second: %v", err)
	}
	if sel.calls != 1 {
		t.Fatalf("selector calls = %d, want 1", sel.calls)
	}
}

// A cap rejection must short-circuit before the (subprocess) selector runs.
func TestReserve_CapRejectionSkipsSelector(t *testing.T) {
	sel := &stubSelector{account: "1"}
	l, _ := newTestLedger(t, WithSelector(sel.Pick))
	caps := Caps{Agent: map[string]*int{"worker": intp(0)}}

	if _, err := l.Reserve(context.Background(), req("gc-1", "worker", caps)); !errors.Is(err, ErrAgentCapReached) {
		t.Fatalf("err = %v, want ErrAgentCapReached", err)
	}
	if sel.calls != 0 {
		t.Fatalf("selector calls = %d, want 0 (must not shell out when already over cap)", sel.calls)
	}
}
