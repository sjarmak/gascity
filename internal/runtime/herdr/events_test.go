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
	"reflect"
	goruntime "runtime"
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

	// subscribes receives each events.subscribe call's decoded filter set.
	subscribes chan []subscribeSub
	// streams receives the live connection of each events.subscribe call.
	streams chan *fakeStream
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

// TestStalePaneIDs pins the typed-error gate plus the shape-based extraction
// fallback: only a pane_not_found herdrError (however wrapped) qualifies, and
// pane ids are pulled from free text by punctuation shape, not by the word
// "pane" — herdr's real fixture message carries no id at all.
func TestStalePaneIDs(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantOK    bool
		wantPanes []string
	}{
		{
			name:   "non-herdr error",
			err:    errors.New("boom"),
			wantOK: false,
		},
		{
			name:   "herdr error, unrelated code",
			err:    &herdrError{Code: "agent_not_found", Message: "agent target not found"},
			wantOK: false,
		},
		{
			name:      "pane_not_found, single id",
			err:       &herdrError{Code: "pane_not_found", Message: "pane w3:p1 not found"},
			wantOK:    true,
			wantPanes: []string{"w3:p1"},
		},
		{
			name:      "pane_not_found, quoted id",
			err:       &herdrError{Code: "pane_not_found", Message: `pane "w3:p1" not found`},
			wantOK:    true,
			wantPanes: []string{"w3:p1"},
		},
		{
			name:      "pane_not_found, several ids",
			err:       &herdrError{Code: "pane_not_found", Message: "panes w3:p1, w4:p2 not found"},
			wantOK:    true,
			wantPanes: []string{"w3:p1", "w4:p2"},
		},
		{
			name:      "pane_not_found, percent-form id",
			err:       &herdrError{Code: "pane_not_found", Message: "pane %5 not found"},
			wantOK:    true,
			wantPanes: []string{"%5"},
		},
		{
			name:      "pane_not_found, real fixture text has no id — must not capture the word 'not'",
			err:       &herdrError{Code: "pane_not_found", Message: "pane not found"},
			wantOK:    true,
			wantPanes: nil,
		},
		{
			name:      "pane_not_found, wrapped",
			err:       fmt.Errorf("herdr events.subscribe: %w", &herdrError{Code: "pane_not_found", Message: "pane w3:p1 not found"}),
			wantOK:    true,
			wantPanes: []string{"w3:p1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panes, ok := stalePaneIDs(tt.err)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !reflect.DeepEqual(panes, tt.wantPanes) {
				t.Fatalf("panes = %v, want %v", panes, tt.wantPanes)
			}
		})
	}
}

// TestHandleSubscribeRejectionRetriesOnlyOnProgress pins the exact-head
// review's busy-loop finding: a pane_not_found rejection naming a pane this
// cycle never subscribed to must NOT trigger an unconditional resubscribe —
// only pruning a pane actually known to this cycle counts as progress.
func TestHandleSubscribeRejectionRetriesOnlyOnProgress(t *testing.T) {
	t.Run("prunes a known stale pane and reports progress", func(t *testing.T) {
		p := sidecarProvider(t)
		bindAt(t, p, "session-a", "w3:p1", 1000)
		s := &sessionEventStream{
			p:          p,
			paneNames:  map[string]string{"w3:p1": "session-a"},
			subscribed: map[string]bool{"w3:p1": true},
		}
		resub, err := s.handleSubscribeRejection(&herdrError{Code: "pane_not_found", Message: "pane w3:p1 not found"})
		if !resub || err != nil {
			t.Fatalf("handleSubscribeRejection = %v, %v; want true, nil", resub, err)
		}
		if _, still := s.paneNames["w3:p1"]; still {
			t.Error("stale pane not dropped from in-cycle filter set")
		}
		if got, _ := p.GetMeta("session-a", metaBoundPane); got != "" {
			t.Errorf("durable binding survived a matching prune: %q", got)
		}
	})

	t.Run("named pane unknown to this cycle: backs off instead of spinning", func(t *testing.T) {
		s := &sessionEventStream{
			p:          sidecarProvider(t),
			paneNames:  map[string]string{"w1:p1": "session-live"},
			subscribed: map[string]bool{"w1:p1": true},
		}
		resub, err := s.handleSubscribeRejection(&herdrError{Code: "pane_not_found", Message: "pane w9:p9 not found"})
		if resub {
			t.Fatal("handleSubscribeRejection resubscribed for a pane absent from this cycle's filter set; this is the busy loop the exact-head review caught")
		}
		if err != nil {
			t.Errorf("handleSubscribeRejection error = %v, want nil (nothing in this cycle to prune)", err)
		}
		if len(s.paneNames) != 1 {
			t.Errorf("unrelated cycle state mutated: %v", s.paneNames)
		}
	})

	t.Run("rejection unrelated to a stale pane makes no attempt", func(t *testing.T) {
		s := &sessionEventStream{p: sidecarProvider(t), paneNames: map[string]string{}, subscribed: map[string]bool{}}
		resub, err := s.handleSubscribeRejection(&herdrError{Code: "invalid_request", Message: "bad json"})
		if resub || err != nil {
			t.Fatalf("handleSubscribeRejection = %v, %v; want false, nil", resub, err)
		}
	})
}

// TestDerivedFilterSetSidecarYieldsToFresherRegistry pins Blocker 1: a
// recycled pane id whose registry occupant disagrees with a stale sidecar
// binding must resolve to the registry's occupant, not the sidecar's stale
// name.
func TestDerivedFilterSetSidecarYieldsToFresherRegistry(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	p := eventTestProvider(t, sock)

	regName := herdrAgentName("session-new")
	f.setAgents(agentInfo{Name: regName, PaneID: "w3:p1"})
	if err := p.SetMeta("session-old", metaBoundPane, "w3:p1"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetMeta("session-old", metaBoundName, "session-old"); err != nil {
		t.Fatal(err)
	}

	got, err := p.derivedFilterSet(context.Background())
	if err != nil {
		t.Fatalf("derivedFilterSet: %v", err)
	}
	if got["w3:p1"] != regName {
		t.Fatalf("paneNames[w3:p1] = %q, want the registry's %q — a stale sidecar binding must not steal a recycled pane's live registry entry", got["w3:p1"], regName)
	}
}

// TestDerivedFilterSetSidecarWinsWithExactNameWhenUncontradicted covers the
// two cases where the sidecar's exact gc session name must still win: the
// registry's lossy mapped name agrees with the sidecar binding, and a pane
// the registry has never detected at all (a raw shell session).
func TestDerivedFilterSetSidecarWinsWithExactNameWhenUncontradicted(t *testing.T) {
	f, sock := newFakeHerdrServer(t)
	p := eventTestProvider(t, sock)

	f.setAgents(agentInfo{Name: herdrAgentName("session-a"), PaneID: "w3:p1"})
	if err := p.SetMeta("session-a", metaBoundPane, "w3:p1"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetMeta("session-a", metaBoundName, "session-a"); err != nil {
		t.Fatal(err)
	}
	// Never detected by herdr at all: absent from the registry entirely,
	// only the sidecar knows about it.
	if err := p.SetMeta("session-shell", metaBoundPane, "w9:p9"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetMeta("session-shell", metaBoundName, "session-shell"); err != nil {
		t.Fatal(err)
	}

	got, err := p.derivedFilterSet(context.Background())
	if err != nil {
		t.Fatalf("derivedFilterSet: %v", err)
	}
	if got["w3:p1"] != "session-a" {
		t.Errorf("paneNames[w3:p1] = %q, want the sidecar's exact name session-a", got["w3:p1"])
	}
	if got["w9:p9"] != "session-shell" {
		t.Errorf("paneNames[w9:p9] = %q, want session-shell (registry never detected this raw-shell pane at all)", got["w9:p9"])
	}
}
