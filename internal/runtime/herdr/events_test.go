package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

func goroutineCount() int { return goruntime.NumGoroutine() }

// fakeHerdrServer speaks the herdr socket API's event slice: one request per
// connection; `agent.list` answers and closes; `events.subscribe` acks and
// then holds the connection open as an NDJSON event stream the test drives.
// Frame and response shapes are pinned to live herdr 0.7.3 captures.
type fakeHerdrServer struct {
	t  *testing.T
	ln net.Listener

	mu     sync.Mutex
	agents []agentInfo // what agent.list returns
	// rejectSubscribe, if set, answers the NEXT events.subscribe call with
	// this error instead of acking, then clears itself.
	rejectSubscribe *herdrError

	// subscribes receives each events.subscribe call's decoded filter set.
	subscribes chan []subscribeSub
	// streams receives the live connection of each events.subscribe call.
	streams chan *fakeStream
}

// rejectNextSubscribe arms a one-shot events.subscribe rejection, for
// exercising the stale-pane recovery path (see stalePaneIDs, pruneStalePane).
func (f *fakeHerdrServer) rejectNextSubscribe(code, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejectSubscribe = &herdrError{Code: code, Message: message}
}

type fakeStream struct {
	conn net.Conn
	mu   sync.Mutex
}

// push writes one event frame to the subscriber, exactly as herdr frames it.
func (s *fakeStream) push(t *testing.T, frame string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.conn.Write([]byte(frame + "\n")); err != nil {
		t.Logf("fake stream write: %v", err)
	}
}

func (s *fakeStream) close() { _ = s.conn.Close() }

// newFakeHerdrServer starts the fake on a short socket path (unix socket
// paths have a ~104-byte limit on darwin, so t.TempDir() is too deep).
func newFakeHerdrServer(t *testing.T) (*fakeHerdrServer, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hevt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	f := &fakeHerdrServer{
		t:          t,
		subscribes: make(chan []subscribeSub, 16),
		streams:    make(chan *fakeStream, 16),
	}
	f.listen(sock)
	return f, sock
}

func (f *fakeHerdrServer) listen(sock string) {
	ln, err := net.Listen("unix", sock)
	if err != nil {
		f.t.Fatal(err)
	}
	f.ln = ln
	f.t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
}

func (f *fakeHerdrServer) setAgents(agents ...agentInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents = append([]agentInfo(nil), agents...) // copy: callers keep mutating their slice
}

func (f *fakeHerdrServer) serve(conn net.Conn) {
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return
	}
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params struct {
			Subscriptions []subscribeSub `json:"subscriptions"`
		} `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		_, _ = conn.Write([]byte(`{"id":"","error":{"code":"invalid_request","message":"bad json"}}` + "\n"))
		_ = conn.Close()
		return
	}
	switch req.Method {
	case "agent.list":
		f.mu.Lock()
		agents := f.agents
		f.mu.Unlock()
		resp := map[string]any{"id": req.ID, "result": map[string]any{"type": "agent_list", "agents": agents}}
		b, _ := json.Marshal(resp)
		_, _ = conn.Write(append(b, '\n'))
		_ = conn.Close()
	case "events.subscribe":
		f.mu.Lock()
		reject := f.rejectSubscribe
		f.rejectSubscribe = nil
		f.mu.Unlock()
		if reject != nil {
			resp := map[string]any{"id": req.ID, "error": reject}
			b, _ := json.Marshal(resp)
			_, _ = conn.Write(append(b, '\n'))
			f.subscribes <- req.Params.Subscriptions
			_ = conn.Close()
			return
		}
		_, _ = conn.Write([]byte(`{"id":"` + req.ID + `","result":{"type":"subscription_started"}}` + "\n"))
		f.subscribes <- req.Params.Subscriptions
		f.streams <- &fakeStream{conn: conn}
	default:
		_, _ = conn.Write([]byte(`{"id":"","error":{"code":"invalid_request","message":"unknown method"}}` + "\n"))
		_ = conn.Close()
	}
}

// eventTestProvider wires a Provider at the fake server's socket.
func eventTestProvider(t *testing.T, sock string) *Provider {
	t.Helper()
	p := New("gctest-events", t.TempDir(), "", 0, 0)
	p.c.sockPath = sock
	return p
}

// stubPaneProcessInfo points p.c.bin at a fake CLI script answering `pane
// process-info` for one paneID with a fixed exists/idle-shell verdict
// (shellPID 4242, no foreground process beyond the shell), independent of
// whatever herdr binary (if any) happens to be on the test machine's PATH.
// derivedFilterSet's probe calls (sidecarBindingLikelyCurrent,
// probeAffirmsRegistryOverSidecar) shell out via p.c.bin exactly like the
// production CLI path — unlike agent.list/events.subscribe, which these
// event tests reach through the fake unix-socket server instead — so
// without this a probe against an undetected/mismatched sidecar binding
// silently depends on ambient machine state (herdr installed or not,
// and what a bogus test session name does against a REAL herdr daemon)
// rather than exercising a controlled live-pane response.
func stubPaneProcessInfo(t *testing.T, p *Provider, paneID string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "herdr")
	fake := `#!/bin/sh
shift 2
if [ "$1 $2" = "pane process-info" ] && [ "$4" = "` + paneID + `" ]; then
  printf '%s' '{"result":{"process_info":{"shell_pid":4242,"foreground_processes":[{"pid":4242,"name":"zsh"}]}}}'
else
  printf '%s' '{"error":{"code":"pane_not_found","message":"pane not found"}}'
fi
`
	if err := os.WriteFile(script, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	p.c.bin = script
}

// stubPaneProcessInfoRecycled stubs `pane process-info` for paneID to answer
// with firstPID on its first invocation (the bind-time probe, before the
// pane is recycled) and laterPID on every invocation after (the probe(s)
// run once the pane's real occupant has changed) — a differing shell pid is
// the only direct evidence sidecarBindingLikelyCurrent/
// probeAffirmsRegistryOverSidecar have that a pane's occupant actually
// changed, so a test exercising recycling must model a real pid change,
// not answer every call with the same fixed pid.
func stubPaneProcessInfoRecycled(t *testing.T, p *Provider, paneID string, firstPID, laterPID int) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "herdr")
	counter := filepath.Join(dir, "calls")
	if err := os.WriteFile(counter, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
shift 2
if [ "$1 $2" = "pane process-info" ] && [ "$4" = "` + paneID + `" ]; then
  n=$(cat "` + counter + `")
  echo $((n + 1)) > "` + counter + `"
  if [ "$n" -eq 0 ]; then
    printf '%s' '{"result":{"process_info":{"shell_pid":` + strconv.Itoa(firstPID) + `,"foreground_processes":[{"pid":` + strconv.Itoa(firstPID) + `,"name":"zsh"}]}}}'
  else
    printf '%s' '{"result":{"process_info":{"shell_pid":` + strconv.Itoa(laterPID) + `,"foreground_processes":[{"pid":` + strconv.Itoa(laterPID) + `,"name":"zsh"}]}}}'
  fi
else
  printf '%s' '{"error":{"code":"pane_not_found","message":"pane not found"}}'
fi
`
	if err := os.WriteFile(script, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	p.c.bin = script
}

func recvEvent(t *testing.T, ch <-chan runtime.SessionEvent, timeout time.Duration) runtime.SessionEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatalf("event channel closed while awaiting event")
		}
		return ev
	case <-time.After(timeout):
		t.Fatalf("timed out after %v awaiting event", timeout)
	}
	panic("unreachable")
}

func recvStream(t *testing.T, f *fakeHerdrServer) *fakeStream {
	t.Helper()
	const timeout = 2 * time.Second
	select {
	case s := <-f.streams:
		return s
	case <-time.After(timeout):
		t.Fatalf("timed out after %v awaiting events.subscribe connection", timeout)
	}
	panic("unreachable")
}

func recvSubscribe(t *testing.T, f *fakeHerdrServer, timeout time.Duration) []subscribeSub {
	t.Helper()
	select {
	case s := <-f.subscribes:
		return s
	case <-time.After(timeout):
		t.Fatalf("timed out after %v awaiting events.subscribe filter set", timeout)
	}
	panic("unreachable")
}

// TestSessionEventStreamStartupAndTranslation pins the core cycle: subscribe
// with broadcast kinds + a per-pane agent-status filter for every known agent
// pane, emit Resync first, then translate herdr frames — broadcast underscore
// kinds and targeted dot kinds — into attributed SessionEvents.
func TestSessionEventStreamStartupAndTranslation(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	f.setAgents(agentInfo{Name: "alpha", PaneID: "w1:p1"}, agentInfo{Name: "beta", PaneID: "w2:p1"})
	p := eventTestProvider(t, sock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := p.SubscribeSessionEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}

	subs := recvSubscribe(t, f, 2*time.Second)
	wantKinds := map[string]bool{"pane.created": false, "pane.closed": false, "pane.exited": false, "pane.agent_detected": false}
	statusPanes := map[string]bool{}
	for _, s := range subs {
		if _, ok := wantKinds[s.Type]; ok {
			wantKinds[s.Type] = true
		}
		if s.Type == "pane.agent_status_changed" {
			statusPanes[s.PaneID] = true
		}
	}
	for k, seen := range wantKinds {
		if !seen {
			t.Errorf("subscribe filter set missing broadcast kind %q", k)
		}
	}
	if !statusPanes["w1:p1"] || !statusPanes["w2:p1"] {
		t.Errorf("subscribe filter set missing per-pane agent-status subs; got %v", statusPanes)
	}

	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventResync {
		t.Fatalf("first event = %v, want resync", ev.Kind)
	}

	stream := recvStream(t, f)
	// Frames below are verbatim live 0.7.3 captures (modulo ids).
	stream.push(t, `{"data":{"pane_id":"w1:p1","type":"pane_exited","workspace_id":"w1"},"event":"pane_exited"}`)
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventExited || ev.Session != "alpha" || ev.Ref != "w1:p1" {
		t.Errorf("pane_exited => %+v, want exited/alpha/w1:p1", ev)
	}

	stream.push(t, `{"data":{"agent":"claude","agent_status":"idle","pane_id":"w2:p1","workspace_id":"w2"},"event":"pane.agent_status_changed"}`)
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventAgentStatus || ev.Session != "beta" || ev.AgentStatus != "idle" {
		t.Errorf("agent_status_changed => %+v, want agent_status/beta/idle", ev)
	}

	stream.push(t, `{"data":{"agent":"claude","pane_id":"w1:p1","type":"pane_agent_detected","workspace_id":"w1"},"event":"pane_agent_detected"}`)
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventAgentDetected || ev.Session != "alpha" {
		t.Errorf("pane_agent_detected => %+v, want agent_detected/alpha", ev)
	}

	stream.push(t, `{"data":{"pane_id":"w1:p1","type":"pane_closed","workspace_id":"w1"},"event":"pane_closed"}`)
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventClosed || ev.Session != "alpha" {
		t.Errorf("pane_closed => %+v, want closed/alpha", ev)
	}

	// A pane gc has no mapping for still surfaces, unattributed.
	stream.push(t, `{"data":{"pane_id":"w9:p9","type":"pane_exited","workspace_id":"w9"},"event":"pane_exited"}`)
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventExited || ev.Session != "" || ev.Ref != "w9:p9" {
		t.Errorf("unmapped pane_exited => %+v, want exited/(unattributed)/w9:p9", ev)
	}
}

// TestSessionEventStreamReconnects pins self-healing: when the server drops
// the stream, the loop reconnects and the new cycle leads with a Resync.
func TestSessionEventStreamReconnects(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	f.setAgents(agentInfo{Name: "alpha", PaneID: "w1:p1"})
	p := eventTestProvider(t, sock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := p.SubscribeSessionEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}

	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventResync {
		t.Fatalf("first event = %v, want resync", ev.Kind)
	}
	stream := recvStream(t, f)
	stream.close() // server-side drop

	// Reconnect happens after backoff; the fresh cycle re-lists, re-subscribes,
	// and leads with a resync.
	if ev := recvEvent(t, ch, 5*time.Second); ev.Kind != runtime.SessionEventResync {
		t.Fatalf("post-drop event = %v, want resync", ev.Kind)
	}
	stream = recvStream(t, f)
	stream.push(t, `{"data":{"pane_id":"w1:p1","type":"pane_exited","workspace_id":"w1"},"event":"pane_exited"}`)
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventExited || ev.Session != "alpha" {
		t.Errorf("post-reconnect pane_exited => %+v, want exited/alpha", ev)
	}
}

// TestSessionEventStreamAttachesWhenServerAppears pins the contract that
// subscribing before the provider's server exists is fine: the stream keeps
// dialing and attaches once the socket appears.
func TestSessionEventStreamAttachesWhenServerAppears(t *testing.T) {
	dir, err := os.MkdirTemp("", "hevt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")

	p := eventTestProvider(t, sock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := p.SubscribeSessionEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}

	// No listener yet: nothing must arrive.
	select {
	case ev := <-ch:
		t.Fatalf("event %+v arrived with no server", ev)
	case <-time.After(400 * time.Millisecond):
	}

	f := &fakeHerdrServer{
		t:          t,
		subscribes: make(chan []subscribeSub, 16),
		streams:    make(chan *fakeStream, 16),
	}
	f.listen(sock)
	f.setAgents(agentInfo{Name: "alpha", PaneID: "w1:p1"})

	if ev := recvEvent(t, ch, 10*time.Second); ev.Kind != runtime.SessionEventResync {
		t.Fatalf("first event after server appeared = %v, want resync", ev.Kind)
	}
}

// TestSessionEventStreamResubscribesForNewAgentPane pins dynamic filter
// maintenance: a pane_created burst triggers a debounced re-list, and a newly
// discovered agent pane forces an immediate resubscribe (new cycle) whose
// filter set covers the new pane — that's how PR-D gets idle events for
// sessions started after the stream came up.
func TestSessionEventStreamResubscribesForNewAgentPane(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	f.setAgents(agentInfo{Name: "alpha", PaneID: "w1:p1"})
	p := eventTestProvider(t, sock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := p.SubscribeSessionEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}
	recvSubscribe(t, f, 2*time.Second)
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventResync {
		t.Fatalf("first event = %v, want resync", ev.Kind)
	}
	stream := recvStream(t, f)

	// A new session starts: its pane appears, then its agent registers.
	f.setAgents(agentInfo{Name: "alpha", PaneID: "w1:p1"}, agentInfo{Name: "gamma", PaneID: "w3:p1"})
	stream.push(t, `{"data":{"pane":{"pane_id":"w3:p1","workspace_id":"w3"},"type":"pane_created"},"event":"pane_created"}`)

	subs := recvSubscribe(t, f, 5*time.Second)
	found := false
	for _, s := range subs {
		if s.Type == "pane.agent_status_changed" && s.PaneID == "w3:p1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("resubscribe filter set missing new pane w3:p1: %v", subs)
	}
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventResync {
		t.Fatalf("post-resubscribe event = %v, want resync", ev.Kind)
	}
	stream = recvStream(t, f)
	stream.push(t, `{"data":{"agent":"claude","agent_status":"idle","pane_id":"w3:p1","workspace_id":"w3"},"event":"pane.agent_status_changed"}`)
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventAgentStatus || ev.Session != "gamma" {
		t.Errorf("new pane agent_status => %+v, want agent_status/gamma", ev)
	}
}

// TestSessionEventStreamReaderGoroutinesReleased pins that resubscribe cycles
// release their connection reader goroutines mid-subscription. The leak this
// guards against — a reader stranded on an undelivered line until the WHOLE
// subscription ends — is invisible to teardown-time checks, so the count is
// taken while the stream is still live.
func TestSessionEventStreamReaderGoroutinesReleased(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	agents := []agentInfo{{Name: "a0", PaneID: "w0:p1"}}
	f.setAgents(agents...)
	p := eventTestProvider(t, sock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := p.SubscribeSessionEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}
	recvSubscribe(t, f, 2*time.Second)
	stream := recvStream(t, f)
	go func() { // steady consumer so emits never coalesce
		for ev := range ch {
			_ = ev
		}
	}()
	time.Sleep(200 * time.Millisecond)
	baseline := goroutineCount()

	const cycles = 5
	for i := 1; i <= cycles; i++ {
		pane := "w" + string(rune('0'+i)) + ":p1"
		agents = append(agents, agentInfo{Name: "a" + string(rune('0'+i)), PaneID: pane})
		f.setAgents(agents...)
		// Deliver a line the control loop consumes, then the trigger frame:
		// the reader is mid-handoff when the cycle ends, the stranding shape.
		stream.push(t, `{"data":{"pane":{"pane_id":"`+pane+`"},"type":"pane_created"},"event":"pane_created"}`)
		recvSubscribe(t, f, 5*time.Second)
		stream = recvStream(t, f)
	}
	// Replaced cycles unwind asynchronously (and other tests' teardown may
	// still be settling in this shared process), so poll until the count
	// returns to near baseline instead of snapshotting once. A real leak
	// never drains: one reader stays stranded per cycle.
	deadline := time.Now().Add(5 * time.Second)
	grew := 0
	for {
		grew = goroutineCount() - baseline
		if grew <= 3 {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("goroutines still %d above baseline after %d resubscribe cycles (leak: ~1 stranded reader per cycle)", grew, cycles)
}

// TestSessionEventStreamBackpressureCoalescesToResync pins the loss contract:
// when the consumer is slow and the buffer fills, events are dropped but the
// loss is surfaced as a Resync once the consumer drains — never a silent gap.
func TestSessionEventStreamBackpressureCoalescesToResync(t *testing.T) {
	origBuffer := sessionEventChanBuffer
	sessionEventChanBuffer = 2
	t.Cleanup(func() { sessionEventChanBuffer = origBuffer })

	f, sock := newFakeHerdrServer(t)
	f.setAgents(agentInfo{Name: "alpha", PaneID: "w1:p1"})
	p := eventTestProvider(t, sock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := p.SubscribeSessionEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}
	stream := recvStream(t, f)

	// Buffer 2 holds [resync, eA]; eB overflows and is dropped with a pending
	// resync recorded.
	stream.push(t, `{"data":{"pane_id":"w1:p1","type":"pane_exited","workspace_id":"w1"},"event":"pane_exited"}`)
	stream.push(t, `{"data":{"pane_id":"w9:p9","type":"pane_exited","workspace_id":"w9"},"event":"pane_exited"}`)
	time.Sleep(500 * time.Millisecond) // let the loop process both frames

	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventResync {
		t.Fatalf("event 1 = %v, want resync", ev.Kind)
	}
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventExited || ev.Ref != "w1:p1" {
		t.Fatalf("event 2 = %+v, want exited w1:p1", ev)
	}

	// Consumer drained; the next frame first flushes the pending resync, then
	// delivers itself.
	stream.push(t, `{"data":{"pane_id":"w1:p1","type":"pane_exited","workspace_id":"w1"},"event":"pane_exited"}`)
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventResync {
		t.Fatalf("event 3 = %v, want coalesced resync", ev.Kind)
	}
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventExited || ev.Ref != "w1:p1" {
		t.Fatalf("event 4 = %+v, want exited w1:p1", ev)
	}
}

// TestSessionEventStreamCtxCancelClosesChannel pins teardown: canceling the
// subscription context ends the stream and closes the channel promptly.
func TestSessionEventStreamCtxCancelClosesChannel(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	f.setAgents(agentInfo{Name: "alpha", PaneID: "w1:p1"})
	p := eventTestProvider(t, sock)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := p.SubscribeSessionEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}
	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventResync {
		t.Fatalf("first event = %v, want resync", ev.Kind)
	}
	cancel()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed — contract satisfied
			}
		case <-deadline:
			t.Fatal("channel not closed within 3s of ctx cancel")
		}
	}
}

// ── stalePaneIDs: code-based, wording-independent stale-pane extraction ─────

func TestStalePaneIDs(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantPanes []string
		wantOK    bool
	}{
		{
			name:      "classic single pane",
			err:       &herdrError{Code: "pane_not_found", Message: "pane w3:p1 not found"},
			wantPanes: []string{"w3:p1"},
			wantOK:    true,
		},
		{
			name:      "quoted pane, trailing punctuation",
			err:       &herdrError{Code: "pane_not_found", Message: `pane 'w9:p9' not found.`},
			wantPanes: []string{"w9:p9"},
			wantOK:    true,
		},
		{
			name:      "multiple panes named in one rejection",
			err:       &herdrError{Code: "pane_not_found", Message: "panes w1:p1, w2:p1 not found"},
			wantPanes: []string{"w1:p1", "w2:p1"},
			wantOK:    true,
		},
		{
			name:      "wrapped by fmt.Errorf",
			err:       fmt.Errorf("herdr events.subscribe: %w", &herdrError{Code: "pane_not_found", Message: "pane w3:p1 not found"}),
			wantPanes: []string{"w3:p1"},
			wantOK:    true,
		},
		{
			name:   "different error code",
			err:    &herdrError{Code: "invalid_request", Message: "pane w3:p1 not found"},
			wantOK: false,
		},
		{
			name:   "not a herdr error at all",
			err:    errors.New("dial unix: connection refused"),
			wantOK: false,
		},
		{
			// %5 with a preceding word character, no legitimate delimiter —
			// must NOT extract "%5" as a real pane id, and the leading "x"
			// consumed to prove the boundary must not leak into the token.
			name:   "percent token embedded in a word, no leading boundary",
			err:    &herdrError{Code: "pane_not_found", Message: "invalid ref x%5 given"},
			wantOK: false,
		},
		{
			// The pane-id token starts the message itself (no leading
			// character at all to check) — must still match.
			name:      "percent token at start of message",
			err:       &herdrError{Code: "pane_not_found", Message: "%5 not found"},
			wantPanes: []string{"%5"},
			wantOK:    true,
		},
		{
			// The leading space that establishes the boundary must not leak
			// into the extracted token.
			name:      "percent token preceded by whitespace",
			err:       &herdrError{Code: "pane_not_found", Message: "pane %5 not found"},
			wantPanes: []string{"%5"},
			wantOK:    true,
		},
		{
			// A non-ASCII letter immediately before the token is exactly as
			// much of a real word character as an ASCII one — Go's \b and an
			// ASCII-only [^0-9A-Za-z_] class cannot see that, but
			// isPaneIDBoundaryRune (unicode.IsLetter) must.
			name:   "percent token preceded by a non-ASCII letter, no leading boundary",
			err:    &herdrError{Code: "pane_not_found", Message: "réf é%5 given"},
			wantOK: false,
		},
		{
			// Same defect on the trailing side.
			name:   "percent token followed by a non-ASCII letter, no trailing boundary",
			err:    &herdrError{Code: "pane_not_found", Message: "pane %5é not found"},
			wantOK: false,
		},
		{
			// A CJK ideograph is letter-like too (unicode.IsLetter is true
			// for it), so it must not be read as a valid leading delimiter
			// for the w<digits>:p<digits> form either.
			name:   "w-form token preceded by a CJK ideograph, no leading boundary",
			err:    &herdrError{Code: "pane_not_found", Message: "中w1:p1 not found"},
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panes, ok := stalePaneIDs(tt.err)
			if ok != tt.wantOK {
				t.Fatalf("stalePaneIDs(%v) ok = %v, want %v (panes=%v)", tt.err, ok, tt.wantOK, panes)
			}
			if !ok {
				return
			}
			if len(panes) != len(tt.wantPanes) {
				t.Fatalf("stalePaneIDs(%v) = %v, want %v", tt.err, panes, tt.wantPanes)
			}
			for i, p := range tt.wantPanes {
				if panes[i] != p {
					t.Errorf("stalePaneIDs(%v)[%d] = %q, want %q", tt.err, i, panes[i], p)
				}
			}
		})
	}
}

// ── derivedFilterSet: sidecar precedence vs. a recycled pane ────────────────

// A sidecar binding whose pane the registry now reports occupied by a
// DIFFERENT agent means the pane was recycled since the binding was
// written: the registry's answer must win, and the stale binding must be
// cleared so it stops shadowing the new occupant on every future cycle.
func TestDerivedFilterSetDropsSidecarBindingOnRecycledPane(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	// The registry now reports a different agent name on the exact pane the
	// stale sidecar binding still claims for "old-session".
	f.setAgents(agentInfo{Name: "new-occupant", PaneID: "w1:p1"})
	p := eventTestProvider(t, sock)
	// Bind-time probe (below) sees the ORIGINAL occupant's shell pid; every
	// probe after (derivedFilterSet's, once the pane has been recycled to
	// new-occupant) sees a DIFFERENT one — the actual evidence
	// probeAffirmsRegistryOverSidecar needs to tell a genuine recycle apart
	// from an unconfirmed registry claim about the same occupant.
	stubPaneProcessInfoRecycled(t, p, "w1:p1", 4242, 9999)
	if err := p.bindPlacement(context.Background(), "old-session", agentInfo{PaneID: "w1:p1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}

	paneNames, _, err := p.derivedFilterSet(context.Background())
	if err != nil {
		t.Fatalf("derivedFilterSet: %v", err)
	}
	if got := paneNames["w1:p1"]; got != "new-occupant" {
		t.Errorf("paneNames[w1:p1] = %q, want new-occupant (registry must win over a recycled-pane sidecar entry)", got)
	}
	if pane, err := p.GetMeta("old-session", metaBoundPane); err != nil || pane != "" {
		t.Errorf("stale binding not cleared: pane=%q err=%v", pane, err)
	}
}

// A sidecar binding whose pane the registry now reports occupied by a
// DIFFERENT agent must still win when a live probe reports the SAME shell
// pid recorded at bind time: that is direct proof the occupant hasn't
// changed, which makes the registry's differing claim the stale side. This
// exercises the PID-match arbitration through derivedFilterSet itself
// (round-5 flagged the pre-existing PID-match coverage as bypassing
// derivedFilterSet, testing probeAffirmsRegistryOverSidecar in isolation
// instead of the code path that actually consumes its verdict).
func TestDerivedFilterSetKeepsSidecarBindingOnPIDMatch(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	f.setAgents(agentInfo{Name: "new-occupant", PaneID: "w1:p1"})
	p := eventTestProvider(t, sock)
	stubPaneProcessInfo(t, p, "w1:p1") // fixed shell_pid 4242 for every call, bind-time and later alike
	if err := p.bindPlacement(context.Background(), "old-session", agentInfo{PaneID: "w1:p1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}

	paneNames, _, err := p.derivedFilterSet(context.Background())
	if err != nil {
		t.Fatalf("derivedFilterSet: %v", err)
	}
	if got := paneNames["w1:p1"]; got != "old-session" {
		t.Errorf("paneNames[w1:p1] = %q, want old-session (matching shell pid proves the occupant hasn't changed, sidecar must win)", got)
	}
	if pane, err := p.GetMeta("old-session", metaBoundPane); err != nil || pane != "w1:p1" {
		t.Errorf("binding cleared despite a PID-confirmed live occupant: pane=%q err=%v", pane, err)
	}
}

// A sidecar binding whose pane the registry now reports occupied by a
// DIFFERENT agent must still win when the corroborating probe itself fails
// (transport error): neither side is more trustworthy this cycle, and
// destroying attribution on unproven grounds is the more dangerous failure
// mode. Exercised through derivedFilterSet itself for the same reason as
// TestDerivedFilterSetKeepsSidecarBindingOnPIDMatch above.
func TestDerivedFilterSetKeepsSidecarBindingOnProbeTransportFailure(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	f.setAgents(agentInfo{Name: "new-occupant", PaneID: "w1:p1"})
	p := eventTestProvider(t, sock)
	stubPaneProcessInfo(t, p, "w1:p1")
	if err := p.bindPlacement(context.Background(), "old-session", agentInfo{PaneID: "w1:p1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}
	// The bind-time probe above succeeded; only the probe derivedFilterSet
	// itself issues (to corroborate the registry mismatch) fails.
	p.c.bin = filepath.Join(t.TempDir(), "missing-herdr-binary")

	paneNames, _, err := p.derivedFilterSet(context.Background())
	if err != nil {
		t.Fatalf("derivedFilterSet: %v", err)
	}
	if got := paneNames["w1:p1"]; got != "old-session" {
		t.Errorf("paneNames[w1:p1] = %q, want old-session (a probe transport failure must not let the registry override)", got)
	}
	if pane, err := p.GetMeta("old-session", metaBoundPane); err != nil || pane != "w1:p1" {
		t.Errorf("binding cleared despite an unconfirmed registry mismatch (probe transport failure): pane=%q err=%v", pane, err)
	}
}

// A sidecar binding whose pane the registry does not mention at all (herdr
// has not detected an agent there) still takes precedence for the name — the
// gap this feature exists to close.
func TestDerivedFilterSetKeepsSidecarBindingForUndetectedPane(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	f.setAgents(agentInfo{Name: "alpha", PaneID: "w1:p1"})
	p := eventTestProvider(t, sock)
	stubPaneProcessInfo(t, p, "w2:p1")
	if err := p.bindPlacement(context.Background(), "raw-shell", agentInfo{PaneID: "w2:p1"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}

	paneNames, _, err := p.derivedFilterSet(context.Background())
	if err != nil {
		t.Fatalf("derivedFilterSet: %v", err)
	}
	if got := paneNames["w2:p1"]; got != "raw-shell" {
		t.Errorf("paneNames[w2:p1] = %q, want raw-shell", got)
	}
}

// ── runCycle: stale-pane rejection recovery ──────────────────────────────────

// A pane_not_found rejection for a stale sidecar binding must prune it and
// retry immediately (no backoff), and the retried subscribe must have
// dropped the pruned pane from its filter set.
func TestRunCycleStalePaneRejectionPrunesAndRetriesImmediately(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	f.setAgents(agentInfo{Name: "alpha", PaneID: "w1:p1"})
	p := eventTestProvider(t, sock)
	stubPaneProcessInfo(t, p, "w9:p9")
	if err := p.bindPlacement(context.Background(), "stale-session", agentInfo{PaneID: "w9:p9"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}
	f.rejectNextSubscribe("pane_not_found", `pane "w9:p9" not found`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := p.SubscribeSessionEvents(ctx)
	if err != nil {
		t.Fatalf("SubscribeSessionEvents: %v", err)
	}

	firstSubs := recvSubscribe(t, f, 2*time.Second)
	found := false
	for _, s := range firstSubs {
		if s.PaneID == "w9:p9" {
			found = true
		}
	}
	if !found {
		t.Fatalf("first subscribe missing stale pane w9:p9: %v", firstSubs)
	}

	secondSubs := recvSubscribe(t, f, 500*time.Millisecond)
	for _, s := range secondSubs {
		if s.PaneID == "w9:p9" {
			t.Fatalf("retried subscribe still names the pruned pane w9:p9: %v", secondSubs)
		}
	}

	if ev := recvEvent(t, ch, 2*time.Second); ev.Kind != runtime.SessionEventResync {
		t.Fatalf("event after stale-pane retry = %v, want resync", ev.Kind)
	}
	if pane, err := p.GetMeta("stale-session", metaBoundPane); err != nil || pane != "" {
		t.Errorf("stale binding not cleared: pane=%q err=%v", pane, err)
	}
}

// A stale-pane rejection whose prune FAILS (the metadata store cannot be
// written) must not disable backoff: the cycle reports a real error instead
// of (true, nil), so runSessionEventStream's normal capped-backoff retry
// applies rather than spinning at full speed against a store that cannot be
// fixed by retrying.
func TestRunCycleStalePaneRejectionFallsThroughToBackoffOnClearFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses DAC permission checks, so a read-only directory would not block deletes")
	}
	f, sock := newFakeHerdrServer(t)
	f.setAgents(agentInfo{Name: "alpha", PaneID: "w1:p1"})
	p := eventTestProvider(t, sock)
	stubPaneProcessInfo(t, p, "w9:p9")
	if err := p.bindPlacement(context.Background(), "stale-session", agentInfo{PaneID: "w9:p9"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}
	// Make the binding's directory read-only so its files remain listable and
	// readable (derivedFilterSet still sees "stale-session"/"w9:p9" and
	// subscribes to it) but RemoveMeta's os.Remove fails on delete — the
	// clear-specifically-fails case, distinct from a read failure (which no
	// longer aborts the whole cycle; see forEachPaneBinding).
	bindingDir := filepath.Join(p.metaDir, sanitize("stale-session"))
	if err := os.Chmod(bindingDir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(bindingDir, 0o700) })
	f.rejectNextSubscribe("pane_not_found", `pane "w9:p9" not found`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &sessionEventStream{p: p, ch: make(chan runtime.SessionEvent, sessionEventChanBuffer)}
	resubscribe, err := s.runCycle(ctx)
	if resubscribe {
		t.Error("runCycle reported resubscribe (backoff disabled) despite a failed prune")
	}
	if err == nil {
		t.Error("runCycle reported no error despite a failed prune — caller will retry at full speed forever")
	}
}

// ── pruneStalePane: rejection-to-prune race ──────────────────────────────────

// A herdr pane_not_found rejection is formed against whatever generation
// derivedFilterSet observed at the start of the cycle; pruneStalePane must
// gate its clear against that captured generation (s.paneBoundAt) rather
// than the pane id alone, so a same-pane-id rebind that lands after the
// cycle's capture but before the rejection is processed — the rejection
// really describes the OLD occupant — cannot be erased by a pane-only clear.
func TestPruneStalePaneSparesSamePaneRebindAfterCapture(t *testing.T) {
	p := New("gctest-prune", t.TempDir(), "", 0, 0)
	stubPaneProcessInfo(t, p, "w9:p9")
	if err := p.bindPlacement(context.Background(), "stale-session", agentInfo{PaneID: "w9:p9"}, bindModeShell); err != nil {
		t.Fatalf("bindPlacement: %v", err)
	}
	capturedBoundAt, err := p.GetMeta("stale-session", metaBoundAt)
	if err != nil {
		t.Fatalf("GetMeta boundAt: %v", err)
	}

	s := &sessionEventStream{
		p:           p,
		ch:          make(chan runtime.SessionEvent, sessionEventChanBuffer),
		paneNames:   map[string]string{"w9:p9": "stale-session"},
		subscribed:  map[string]bool{"w9:p9": true},
		paneBoundAt: map[string]string{"w9:p9": capturedBoundAt},
	}

	// The rebind lands after the cycle captured paneBoundAt but before the
	// (stale) rejection is pruned: same pane id, fresh generation.
	if err := p.bindPlacement(context.Background(), "stale-session", agentInfo{PaneID: "w9:p9"}, bindModeShell); err != nil {
		t.Fatalf("rebind bindPlacement: %v", err)
	}
	freshBoundAt, err := p.GetMeta("stale-session", metaBoundAt)
	if err != nil {
		t.Fatalf("GetMeta boundAt after rebind: %v", err)
	}
	if freshBoundAt == capturedBoundAt {
		t.Fatal("rebind did not advance metaBoundAt (test fixture is not exercising the race)")
	}

	if ok := s.pruneStalePane("w9:p9"); !ok {
		t.Error("pruneStalePane against a stale generation reported failure, want a no-op success")
	}
	if pane, err := p.GetMeta("stale-session", metaBoundPane); err != nil || pane != "w9:p9" {
		t.Fatalf("fresh rebind erased by a generation-mismatched prune: pane=%q err=%v, want w9:p9", pane, err)
	}
	if boundAt, err := p.GetMeta("stale-session", metaBoundAt); err != nil || boundAt != freshBoundAt {
		t.Fatalf("fresh rebind's boundAt disturbed: boundAt=%q err=%v, want %q", boundAt, err, freshBoundAt)
	}
	if _, ok := s.paneNames["w9:p9"]; ok {
		t.Error("pruneStalePane left w9:p9 in paneNames")
	}
	if _, ok := s.paneBoundAt["w9:p9"]; ok {
		t.Error("pruneStalePane left w9:p9 in paneBoundAt")
	}
}
