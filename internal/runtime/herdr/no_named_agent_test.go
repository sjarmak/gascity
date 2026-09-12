package herdr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsNoNamedAgentSeparatesAgentlessFromBooting pins the one distinction the
// predicate exists to make. herdr 0.8.0 answers "this pane has no named agent"
// with agent_not_ready, the same code it uses for a real agent that is still
// booting, and the two demand opposite handling: paste into the first, retry the
// second. A widening to the code itself passes every arm above the booting one
// and fails that one, which is why the booting arm is here.
//
// The stderr arm is the shape this actually arrives in when herdr exits
// non-zero: no envelope is parsed, so a predicate keyed on the error CODE sees
// nothing and the fallback stays dead. Matching the code instead of the message
// fails that arm.
func TestIsNoNamedAgentSeparatesAgentlessFromBooting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		want   bool
	}{
		{
			name:   "0.8.0 agent_not_ready on a pane with no named agent",
			script: envelopeScript(`{"error":{"code":"agent_not_ready","message":"agent w1:p1 is not an active named agent"},"id":"cli:agent:prompt"}`),
			want:   true,
		},
		{
			// The shape observed live: herdr exits non-zero and the envelope
			// arrives as stderr text, so there is no code to read.
			name:   "0.8.0 no named agent reported on stderr with a non-zero exit",
			script: "#!/bin/sh\necho '{\"error\":{\"code\":\"agent_not_ready\",\"message\":\"agent w1:p1 is not an active named agent\"},\"id\":\"cli:agent:prompt\"}' >&2\nexit 1\n",
			want:   true,
		},
		{
			name:   "0.7.x agent_not_found",
			script: envelopeScript(`{"error":{"code":"agent_not_found","message":"no agent registered for w1:p1"},"id":"cli:agent:prompt"}`),
			want:   true,
		},
		{
			name:   "agent_not_ready on an agent that is still booting",
			script: envelopeScript(`{"error":{"code":"agent_not_ready","message":"agent alpha is starting up"},"id":"cli:agent:prompt"}`),
			want:   false,
		},
		{
			name:   "an unrelated rejection",
			script: envelopeScript(`{"error":{"code":"agent_pane_busy","message":"pane w1:p1 has a foreground process"},"id":"cli:agent:prompt"}`),
			want:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &client{session: "gc-test", bin: writeFakeHerdr(t, tc.script)}
			err := c.agentPrompt(context.Background(), "w1:p1", "proceed")
			if err == nil {
				t.Fatal("fake herdr rejection produced no error")
			}
			if got := isNoNamedAgent(err); got != tc.want {
				t.Errorf("isNoNamedAgent(%v) = %v, want %v", err, got, tc.want)
			}
		})
	}

	if isNoNamedAgent(nil) {
		t.Error("isNoNamedAgent(nil) = true")
	}
}

// TestDeliverNudgeFallsBackWhenThePaneHasNoNamedAgent is the behavior the
// predicate buys, asserted through the verb a caller actually uses rather than
// through the predicate alone: a pane carrying only a reported agent state has
// no prompt machinery, so the nudge must still land via paste + Enter.
//
// The booting arm is the guard on the other side. Pasting into an agent that is
// merely not ready yet types into a TUI that is not accepting input, so that
// rejection has to come back as an error with nothing typed.
func TestDeliverNudgeFallsBackWhenThePaneHasNoNamedAgent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		message    string
		wantErr    bool
		wantPasted bool
	}{
		{
			name:       "no named agent on the pane",
			message:    "agent w1:p1 is not an active named agent",
			wantPasted: true,
		},
		{
			name:    "named agent still booting",
			message: "agent alpha is starting up",
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			trace := filepath.Join(dir, "verbs")
			// Only `agent prompt` is rejected; every other verb succeeds quietly,
			// so the trace file records exactly which fallback ran.
			script := "#!/bin/sh\n" +
				"for a in \"$@\"; do echo \"$a\" >> " + trace + "; done\n" +
				"echo '---' >> " + trace + "\n" +
				"case \"$*\" in\n" +
				"  *'agent prompt'*) echo '{\"error\":{\"code\":\"agent_not_ready\",\"message\":\"" + tc.message + "\"},\"id\":\"cli:agent:prompt\"}' ;;\n" +
				"esac\n" +
				"exit 0\n"
			c := &client{session: "gc-test", bin: writeFakeHerdr(t, script)}

			err := c.deliverNudge(context.Background(), "w1:p1", "proceed with the drain")
			if tc.wantErr && err == nil {
				t.Fatal("deliverNudge returned nil for a booting agent; the turn was typed into a TUI that is not accepting input")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("deliverNudge: %v", err)
			}

			b, rerr := os.ReadFile(trace)
			if rerr != nil {
				t.Fatalf("reading the verb trace: %v", rerr)
			}
			pasted := strings.Contains(string(b), "proceed with the drain\npane\nrun\n") ||
				(strings.Contains(string(b), "\npane\nrun\n") && strings.Contains(string(b), "send-keys"))
			if pasted != tc.wantPasted {
				t.Errorf("paste fallback ran = %v, want %v; verbs seen:\n%s", pasted, tc.wantPasted, b)
			}
		})
	}
}

// TestWaitForIdleOutcomeTreatsAPaneWithNoNamedAgentAsNothingToWaitOn covers the
// third caller. A raw shell pane has no agent to wait on, so the wait is a
// no-op the caller may proceed past; classifying it as an error instead stalls a
// session start behind a wait that can never be satisfied.
func TestWaitForIdleOutcomeTreatsAPaneWithNoNamedAgentAsNothingToWaitOn(t *testing.T) {
	p := &Provider{c: &client{session: "gc-test", bin: writeFakeHerdr(t, envelopeScript(
		`{"error":{"code":"agent_not_ready","message":"agent shell-only is not an active named agent"},"id":"cli:agent:wait"}`))}}
	if got := p.waitForIdleOutcome(context.Background(), "shell-only", 10); got != idleWaitNoAgent {
		t.Errorf("waitForIdleOutcome = %v, want idleWaitNoAgent", got)
	}
}

// envelopeScript is a fake herdr that answers every verb with one error
// envelope on stdout and exits 0, which is how herdr reports a typed rejection.
func envelopeScript(envelope string) string {
	return "#!/bin/sh\ncat <<'JSON'\n" + envelope + "\nJSON\nexit 0\n"
}
