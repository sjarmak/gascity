package herdr

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

// waitForEvent drains the stream until an event matches, tolerating
// interleaved noise (resyncs from resubscribe cycles, stray-pane events,
// unrelated status flaps).
func waitForEvent(t *testing.T, ch <-chan runtime.SessionEvent, timeout time.Duration, match func(runtime.SessionEvent) bool) runtime.SessionEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("event channel closed while waiting")
			}
			if match(ev) {
				return ev
			}
			t.Logf("  (skipping %s session=%q status=%q ref=%q)", ev.Kind, ev.Session, ev.AgentStatus, ev.Ref)
		case <-deadline:
			t.Fatalf("timed out after %v waiting for matching event", timeout)
		}
	}
}

// TestSessionEventsLive drives SubscribeSessionEvents against a real herdr
// binary in an isolated session: leading resync, forced agent-status change
// (herdr pane report-agent), dynamic resubscribe for an agent started after
// the stream came up, natural process exit, pane close via Stop, and stream
// re-attach across a full server bounce. Opt-in live tier: see requireLiveHerdr.
func TestSessionEventsLive(t *testing.T) {
	requireLiveHerdr(t)

	const session = "gctest-events-live"
	p := New(session, t.TempDir(), t.TempDir(), 0, 0)
	_ = p.c.stopServer() // clear any leftover server from a crashed prior run
	t.Cleanup(func() { _ = p.TeardownServer() })
	if err := p.ConfigureServer(); err != nil {
		t.Fatalf("ConfigureServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// paneOf reads the pane id herdr assigned a session at Start from the
	// sidecar binding rather than the agent registry: under herdr ≥0.8.0
	// the registry only lists a pane once it has DETECTED a supported
	// interactive agent inside it, and these sessions run a bare "sleep"
	// (there is no real agent binary to detect), so `agent get`/`agent list`
	// never see them at all. The sidecar binding is populated at Start
	// regardless of detection, which is exactly the gap this test exercises.
	paneOf := func(name string) string {
		t.Helper()
		pane, err := p.GetMeta(name, metaBoundPane)
		if err != nil || pane == "" {
			t.Fatalf("GetMeta %s %s: pane=%q err=%v", name, metaBoundPane, pane, err)
		}
		return pane
	}

	// evt-a exists before the stream: its pane rides the initial filter set.
	cfgA := runtime.Config{WorkDir: t.TempDir(), Command: "sleep 120"}
	if err := p.Start(ctx, "evt-a", cfgA); err != nil {
		t.Fatalf("Start evt-a: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop("evt-a") })
	paneA := paneOf("evt-a")

	ch, err := p.SubscribeSessionEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}
	waitForEvent(t, ch, 10*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventResync
	})

	// Forced status change on evt-a's pane must arrive attributed.
	report := func(pane, agent, state string) {
		t.Helper()
		out, err := exec.Command("herdr", "--session", session, "pane", "report-agent", pane,
			"--source", "gctest", "--agent", agent, "--state", state).CombinedOutput()
		if err != nil {
			t.Fatalf("pane report-agent %s %s: %v: %s", pane, state, err, out)
		}
	}
	// herdr ≥0.8.0's registry is detection-based: the FIRST report-agent call
	// on a pane REGISTERS the agent (fires pane_agent_detected, which carries
	// no status) rather than reporting a status transition; only a second
	// call against an already-detected agent fires pane_agent_status_changed.
	// A production agent TUI gets its "detected" call from herdr's own
	// detector; these bare "sleep" panes need it simulated explicitly.
	report(paneA, herdrAgentName("evt-a"), "idle")
	waitForEvent(t, ch, 10*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventAgentDetected && ev.Session == "evt-a"
	})
	report(paneA, herdrAgentName("evt-a"), "working")
	ev := waitForEvent(t, ch, 10*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventAgentStatus && ev.Session == "evt-a" && ev.AgentStatus == "working"
	})
	if ev.Ref != paneA {
		t.Errorf("evt-a status event ref = %q, want %q", ev.Ref, paneA)
	}

	// evt-b starts while the stream is live: pane_created → debounced re-list
	// → resubscribe; its status events must then flow, attributed.
	cfgB := runtime.Config{WorkDir: t.TempDir(), Command: "sleep 120"}
	if err := p.Start(ctx, "evt-b", cfgB); err != nil {
		t.Fatalf("Start evt-b: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop("evt-b") })
	paneB := paneOf("evt-b")
	// The resubscribe cycle emits a fresh resync; wait for it so the report
	// below races nothing.
	waitForEvent(t, ch, 15*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventResync
	})
	report(paneB, herdrAgentName("evt-b"), "idle")
	waitForEvent(t, ch, 10*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventAgentDetected && ev.Session == "evt-b"
	})
	report(paneB, herdrAgentName("evt-b"), "blocked")
	waitForEvent(t, ch, 10*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventAgentStatus && ev.Session == "evt-b" && ev.AgentStatus == "blocked"
	})

	// evt-c exits on its own: pane_exited must arrive attributed (the
	// merge-only pane map keeps the mapping even if herdr reaps the agent
	// record first).
	cfgC := runtime.Config{WorkDir: t.TempDir(), Command: "sleep 3"}
	if err := p.Start(ctx, "evt-c", cfgC); err != nil {
		t.Fatalf("Start evt-c: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop("evt-c") })
	waitForEvent(t, ch, 20*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventExited && ev.Session == "evt-c"
	})

	// Stop closes the pane: pane_closed must arrive attributed.
	if err := p.Stop("evt-b"); err != nil {
		t.Fatalf("Stop evt-b: %v", err)
	}
	waitForEvent(t, ch, 10*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventClosed && ev.Session == "evt-b"
	})

	// Full server bounce: the stream must re-attach and lead with a resync.
	if err := p.TeardownServer(); err != nil {
		t.Fatalf("TeardownServer: %v", err)
	}
	time.Sleep(1 * time.Second) // let the drop register before the server returns
	if err := p.ConfigureServer(); err != nil {
		t.Fatalf("ConfigureServer (bounce): %v", err)
	}
	waitForEvent(t, ch, 20*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventResync
	})

	// evt-a's binding predates the bounce and was never re-Start'ed; its
	// status events must still arrive attributed afterward, proving the
	// pre-bounce sidecar binding survives the reconnect/prune logic intact
	// rather than being silently dropped or misattributed.
	report(paneA, herdrAgentName("evt-a"), "idle")
	waitForEvent(t, ch, 10*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventAgentDetected && ev.Session == "evt-a"
	})
	report(paneA, herdrAgentName("evt-a"), "working")
	postBounce := waitForEvent(t, ch, 10*time.Second, func(ev runtime.SessionEvent) bool {
		return ev.Kind == runtime.SessionEventAgentStatus && ev.Session == "evt-a" && ev.AgentStatus == "working"
	})
	if postBounce.Ref != paneA {
		t.Errorf("post-bounce evt-a status event ref = %q, want %q", postBounce.Ref, paneA)
	}

	cancel()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel not closed within 3s of ctx cancel")
		}
	}
}
