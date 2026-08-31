package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gastownhall/gascity/internal/runtime"
)

// This file implements runtime.SessionEventProvider over herdr's socket API.
//
// Unlike the CLI-verb client in client.go, the event stream talks to the
// session server's unix socket directly: `events.subscribe` holds its
// connection open as an NDJSON event stream, which a shell-out cannot carry.
// The server serves ONE request per connection (it answers, then closes), so
// the subscribe call owns a dedicated connection and any side lookups
// (agent.list) open their own short-lived ones.
//
// Wire facts, verified live against herdr 0.7.3:
//   - Every request needs a `params` field, even when empty.
//   - Subscribe ack: {"id":…,"result":{"type":"subscription_started"}}.
//   - Broadcast kinds stream as {"event":"pane_exited","data":{…}} — the
//     EventEnvelope schema, underscore names. The targeted kinds
//     (pane.output_matched / pane.agent_status_changed / pane.scroll_changed)
//     stream with their dot names and REQUIRE a pane_id filter — there is no
//     broadcast agent-status subscription, which is why the filter set must
//     be maintained per pane as sessions come and go.
//   - pane.output_changed is not a subscribable kind in 0.7.3 (the broadcast
//     EventKind exists, but events.subscribe rejects it).
//   - No frame carries a sequence number, so a dropped connection is an
//     undetectable gap: every (re)connect emits SessionEventResync and the
//     consumer reconciles from polled state.
//   - The server replays a backlog of the session's recent events to every
//     new subscription (observed live: the same pane_closed/agent_detected
//     frames re-delivered after each resubscribe). Frames carry no ids or
//     timestamps to filter the replay, so it is passed through; the
//     interface contract makes events level-triggered hints.

// sessionEventChanBuffer sizes the subscriber channel. A variable so unit
// tests can shrink it to pin the backpressure-coalescing contract.
var sessionEventChanBuffer = 256

const (
	// sessionEventMinBackoff..MaxBackoff bound the reconnect backoff after a
	// transport failure; a cycle that stayed healthy for
	// sessionEventHealthyCycle resets the backoff to the minimum.
	sessionEventMinBackoff   = 250 * time.Millisecond
	sessionEventMaxBackoff   = 5 * time.Second
	sessionEventHealthyCycle = 5 * time.Second
	// sessionEventRelistDebounce coalesces a burst of pane_created /
	// pane_agent_detected frames into one agent.list refresh.
	sessionEventRelistDebounce = 300 * time.Millisecond
	// sessionEventOpTimeout bounds each bounded socket op (dial, subscribe
	// ack, agent.list) so a wedged server cannot hang the stream loop; the
	// event stream itself legitimately idles indefinitely and carries no
	// deadline.
	sessionEventOpTimeout = 10 * time.Second
)

var (
	_ runtime.SessionEventProvider = (*Provider)(nil)

	// sockRequestID distinguishes concurrent requests in server logs; the
	// client never multiplexes, so it is not used for response routing.
	sockRequestID atomic.Uint64
)

// subscribeSub is one events.subscribe filter entry.
type subscribeSub struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
}

// subscribeParams is the events.subscribe params payload.
type subscribeParams struct {
	Subscriptions []subscribeSub `json:"subscriptions"`
}

// sockRequest is the socket API request envelope.
type sockRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

// SubscribeSessionEvents implements runtime.SessionEventProvider: it starts a
// self-healing subscription to herdr's event stream and translates pane/agent
// frames into session-attributed events. The stream does not require the
// session server to be up yet — it keeps dialing until the socket appears —
// and it never starts the server itself (that stays with Start /
// ConfigureServer; a sensor should not own the server lifecycle).
func (p *Provider) SubscribeSessionEvents(ctx context.Context) (<-chan runtime.SessionEvent, error) {
	ch := make(chan runtime.SessionEvent, sessionEventChanBuffer)
	go p.runSessionEventStream(ctx, ch)
	return ch, nil
}

// derivedFilterSet is the pane↔name map and status-subscription filter set
// a cycle builds by merging two sources: the herdr registry (agents it has
// DETECTED — the only source with fresh knowledge of panes started outside
// this provider) and the sidecar pane bindings (every pane this provider
// itself bound at Start, regardless of whether herdr has classified its
// occupant yet). Without the sidecar half, a session whose pane herdr has
// not detected — a raw shell, or an agent kind herdr's detector does not
// recognize, or simply one not yet classified — gets neither a paneNames
// entry nor a status subscription, so its events are silently undeliverable.
// Sidecar entries take precedence for the name attributed to a pane: it is
// the exact gc session name, where the registry's a.Name is herdrAgentName's
// lossy, length-capped mapping and only coincidentally equals it. Precedence
// is withheld, though, in two cases where a sidecar binding's pane may have
// been recycled to a new occupant over the server's lifetime: when the
// registry already reports a DIFFERENT agent detected on that exact pane, or
// — since herdr ≥0.8.0's detector may simply not have classified the pane's
// current occupant yet, so a missing registry entry alone proves nothing —
// when the registry has NO entry for the pane and a live probe
// (sidecarBindingLikelyCurrent) does not corroborate the binding either. In
// both cases the stale binding is cleared instead of trusted, so it cannot
// misattribute the new occupant's events to the old session.
//
// paneBoundAt is a companion map, keyed the same as paneNames, of the
// generation token (metaBoundAt) observed for each sidecar-attributed pane
// this cycle. pruneStalePane carries it forward so a later herdr
// pane_not_found rejection — which names a pane but carries no generation
// information of its own — can still be gated against the exact binding
// this cycle believed was there, rather than erasing whatever binding
// currently holds that pane id regardless of whether it is the same one.
func (p *Provider) derivedFilterSet(ctx context.Context) (paneNames map[string]string, paneBoundAt map[string]string, err error) {
	agents, err := p.c.sockAgentList(ctx)
	if err != nil {
		return nil, nil, err
	}
	paneNames = make(map[string]string, len(agents))
	for _, a := range agents {
		if a.PaneID == "" || a.Name == "" {
			continue
		}
		paneNames[a.PaneID] = a.Name
	}
	bound, boundAtByPane, err := p.boundPaneBindings()
	if err != nil {
		return nil, nil, fmt.Errorf("herdr: derive event pane set: %w", err)
	}
	paneBoundAt = make(map[string]string, len(bound))
	for pane, name := range bound {
		if reg, ok := paneNames[pane]; ok {
			if reg != herdrAgentName(name) {
				affirms, boundAt := p.probeAffirmsRegistryOverSidecar(ctx, name, pane)
				if !affirms {
					// The registry's claim wasn't corroborated this cycle —
					// either the probe transport itself failed (this
					// registry snapshot is no more trustworthy than the
					// sidecar one) or the probe's shell pid still matches
					// what the sidecar bound (direct proof the occupant
					// hasn't changed, so the registry entry is the stale
					// side here, not the sidecar). Either way, keep
					// attributing this cycle to the sidecar's name — paneNames[pane]
					// must not be left at its already-preloaded registry
					// value (reg) from the loop above, or this branch would
					// silently defer to the unconfirmed registry claim
					// despite sidecar entries otherwise taking precedence.
					paneNames[pane] = name
					paneBoundAt[pane] = boundAt
					continue
				}
				// clearPaneBindingIfPaneGen, not clearPaneBindingIfPane: the
				// verdict above was formed under a lock that is already
				// released by now, so a concurrent rebind of name to a pane
				// that recycles to this same pane id could otherwise get
				// erased by a pane-id-only comparison. boundAt gates the
				// clear to the exact generation the verdict was formed
				// against.
				if !p.clearPaneBindingIfPaneGen(name, pane, boundAt) {
					fmt.Fprintf(os.Stderr, "herdr: failed to clear stale pane binding %s/%s after registry reassignment to %q\n", name, pane, reg)
				}
				continue
			}
			paneNames[pane] = name
			// No verdict function ran this branch (the registry already
			// confirms the sidecar's own name) — boundAtByPane[pane] is the
			// unlocked batch read boundPaneBindings took at the top of this
			// cycle, good enough as a generation token: a later prune's own
			// locked re-read (clearPaneBindingIfPaneGen) is what actually
			// governs deletion, so a token that is a few instructions stale
			// only ever makes that re-check fail open, never destroys.
			paneBoundAt[pane] = boundAtByPane[pane]
			continue
		}
		current, boundAt := p.sidecarBindingLikelyCurrent(ctx, name, pane)
		if !current {
			if !p.clearPaneBindingIfPaneGen(name, pane, boundAt) {
				fmt.Fprintf(os.Stderr, "herdr: failed to clear stale pane binding %s/%s (undetected occupant probe found it gone or stale)\n", name, pane)
			}
			continue
		}
		paneNames[pane] = name
		paneBoundAt[pane] = boundAt
	}
	return paneNames, paneBoundAt, nil
}

// probeAffirmsRegistryOverSidecar corroborates a registry-reported name
// mismatch before acting on it destructively. The mismatch itself is
// normally the freshest, most authoritative evidence herdr can give that
// pane no longer belongs to name — it is exactly what triggered this branch
// — so the probe's job here is narrower than sidecarBindingLikelyCurrent's:
// not to re-decide who owns the pane from busy/idle/mode (a probe reporting
// the pane busy would trivially pass for either occupant and cannot
// out-rank the registry either way), but to confirm this cycle's herdr
// connection is actually healthy enough for its registry snapshot to be
// believed, PLUS check the one piece of evidence that can actually
// out-rank a registry entry: an occupant-identity match. A transport
// failure proves nothing about which side is stale — it may be a symptom of
// the exact same hiccup that produced the registry read — so it does not
// affirm the registry. A probe reporting the SAME shell pid the sidecar
// bound at placement time is direct proof the pane's occupant has not
// changed since (a pane's pty keeps one pid for its life), which makes the
// registry's differing claim the stale/wrong side, not the sidecar's — so
// that case does not affirm the registry either, regardless of how healthy
// the probe transport was. A metadata read failure on the bound shell pid
// (a transient I/O error, not the value being legitimately absent) also does
// not affirm the registry, for the same reason: it is not proof the pid was
// never recorded. The read is taken under name's lock, matching
// sidecarBindingLikelyCurrent, so it cannot observe a torn snapshot mid-write
// by bindPlacement. Either way, the caller keeps attributing to the
// sidecar's name and leaves the binding intact for re-evaluation next cycle
// rather than destroying attribution on an unconfirmed or contradicted
// registry snapshot.
//
// boundAt is metaBoundAt as observed by that same locked read (empty when
// unreadable), returned so a caller that decides to clear on an affirmed
// verdict can gate it through clearPaneBindingIfPaneGen: the lock here is
// released before this function returns, so a caller-side clear is a
// separate, later acquisition of the same lock, and pane id alone cannot
// prove the binding clearPaneBindingIfPaneGen would find still belongs to
// the same generation this verdict was formed against.
//
// probePane itself runs unlocked, before the locked read above — a
// probe-to-snapshot race: a rebind of name landing in that gap would pair
// evidence about the OLD occupant with a boundAt describing the NEW
// generation, so a "the registry wins, clear it" verdict could carry the
// fresh generation's own token and erase the binding that just replaced the
// stale one. A first locked read taken BEFORE the probe, compared against
// the one taken after, closes this: a mismatch means the generation moved
// while the probe was in flight, so the evidence cannot be trusted to
// describe whatever the post-probe read found, and the verdict fails open
// (does not affirm the registry, no boundAt) instead.
func (p *Provider) probeAffirmsRegistryOverSidecar(ctx context.Context, name, pane string) (affirms bool, boundAt string) {
	preAt, preErr := p.lockedMetaBoundAt(name)

	probe, err := p.probePane(ctx, pane)
	if err != nil {
		return false, ""
	}
	unlock := p.lockName(name)
	boundPID, pidErr := p.boundShellPID(name)
	raw, rawErr := p.GetMeta(name, metaBoundAt)
	unlock()
	if rawErr == nil {
		boundAt = strings.TrimSpace(raw)
	}
	if preErr != nil || rawErr != nil || preAt != boundAt {
		return false, ""
	}
	if pidErr != nil {
		return false, boundAt
	}
	if boundPID != 0 && probe.ShellPID != 0 && probe.ShellPID == boundPID {
		return false, boundAt
	}
	return true, boundAt
}

// panePaneIDToken matches the raw grammar of the two pane-id shapes herdr
// emits (`%<digits>`, `w<digits>:p<digits>`), within a pane_not_found
// rejection's message, with NO boundary assertion baked into the pattern
// itself — boundary correctness is enforced separately in stalePaneIDs, by
// decoding the actual UTF-8 rune immediately outside each candidate match
// and classifying it with the unicode package, not with `\b` or an
// ASCII-only character class. Two prior approaches were tried and both have
// the same blind spot: Go's regexp `\b` only recognizes `[0-9A-Za-z_]` as
// "word" runes, and a `[^0-9A-Za-z_]`-style leading-character class is
// exactly as ASCII-blind — neither can tell that a non-ASCII rune like 'é'
// or '中' is itself letter-like. That wrongly reads such a rune as a
// legitimate delimiter, so "é%5" and "%5é" would extract "%5" as if
// properly bounded (the digits actually run straight out of a letter, with
// no real boundary at all), and "中w1:p1" would extract "w1:p1" the same
// way on its leading side — each capable of pruning a real binding named by
// nothing more than a substring match through non-ASCII adjacency.
// unicode.IsLetter/IsDigit are themselves Unicode properties, not an ASCII
// table lookup, so checking the decoded rune against them (isPaneIDBoundaryRune)
// closes this for every script, not just Latin-1 accented letters. This
// still rejects "x%5" (unbounded on the left: 'x' is letter-like) and
// "window1:p2" (unbounded on the left of "w1:p2": 'o' is letter-like) while
// still matching " %5", "'%5", "(%5", a leading "%5" at the very start of
// the message, and "%5backup" is still rejected (unbounded on the right).
var panePaneIDToken = regexp.MustCompile(`%\d+|w\d+:p\d+`)

// isPaneIDBoundaryRune reports whether r is safe to sit immediately outside
// a pane-id-shaped token — i.e. it is not itself a rune that would extend an
// identifier. "Word-forming" here means any Unicode letter, any Unicode
// digit (not just ASCII 0-9 — the token's own digits are already consumed
// by the match itself; this classifies the rune OUTSIDE it), or underscore.
func isPaneIDBoundaryRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
}

// stalePaneIDs reports the pane ids named in a herdr events.subscribe
// rejection, if err is one for a pane it no longer has. It matches on the
// herdr-reported error CODE (see readSockResponse), not message wording, so
// it survives wrapping, punctuation, and quoting changes — an anchored
// full-string regex over err.Error() does not — and it tolerates a
// rejection naming several invalid panes in one message.
func stalePaneIDs(err error) (panes []string, ok bool) {
	if herdrErrorCode(err) != "pane_not_found" {
		return nil, false
	}
	var he *herdrError
	if !errors.As(err, &he) {
		return nil, false
	}
	msg := he.Message
	seen := make(map[string]bool)
	for _, loc := range panePaneIDToken.FindAllStringIndex(msg, -1) {
		start, end := loc[0], loc[1]
		if start > 0 {
			r, _ := utf8.DecodeLastRuneInString(msg[:start])
			if !isPaneIDBoundaryRune(r) {
				continue
			}
		}
		if end < len(msg) {
			r, _ := utf8.DecodeRuneInString(msg[end:])
			if !isPaneIDBoundaryRune(r) {
				continue
			}
		}
		tok := msg[start:end]
		if seen[tok] {
			continue
		}
		seen[tok] = true
		panes = append(panes, tok)
	}
	return panes, len(panes) > 0
}

// pruneStalePane drops pane from this cycle's filter set and, if it is a
// sidecar-bound session whose binding still points at this exact pane,
// clears it so it stops being resubmitted on every future cycle. It reports
// whether the clear (or no-op, when the binding already moved on) succeeded;
// a caller must not treat failure as success — see runCycle, where a failed
// prune must fall through to normal backoff instead of retrying at full
// speed forever against a metadata store that cannot be written.
//
// The clear is generation-gated (clearPaneBindingIfPaneGen), not a plain
// pane-id comparison: the herdr pane_not_found rejection driving this call
// is evidence about the pane as of whenever the server formed it, and
// paneBoundAt[pane] — captured by derivedFilterSet at this cycle's start —
// is this stream's best record of which generation that was. Without it, a
// same-ID rebind completing after the rejection but before this prune runs
// would look identical to the stale binding under a pane-only check (both
// share the pane id) and get erased along with it; a missing or empty token
// (s.paneBoundAt has no entry for pane, or derivedFilterSet itself could not
// confirm one) makes the clear a no-op, the same fail-open default used
// throughout this file for an unproven generation.
func (s *sessionEventStream) pruneStalePane(pane string) bool {
	name := s.paneNames[pane]
	wantBoundAt := s.paneBoundAt[pane]
	delete(s.paneNames, pane)
	delete(s.subscribed, pane)
	delete(s.paneBoundAt, pane)
	if name == "" {
		return true
	}
	return s.p.clearPaneBindingIfPaneGen(name, pane, wantBoundAt)
}

// runSessionEventStream drives connection cycles until ctx is canceled. A
// cycle ending in resubscribe (the pane filter set grew) reconnects
// immediately; a transport failure reconnects with capped exponential
// backoff. Failures are logged once per streak, not per retry.
func (p *Provider) runSessionEventStream(ctx context.Context, ch chan runtime.SessionEvent) {
	defer close(ch)
	s := &sessionEventStream{p: p, ch: ch}
	backoff := sessionEventMinBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		resubscribe, err := s.runCycle(ctx)
		cycle := time.Since(start)
		if ctx.Err() != nil {
			return
		}
		if resubscribe {
			backoff = sessionEventMinBackoff
			continue
		}
		if err != nil && backoff == sessionEventMinBackoff {
			fmt.Fprintf(os.Stderr, "gc: herdr session-event stream for %q: %v (reconnecting)\n", p.c.session, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if cycle >= sessionEventHealthyCycle {
			backoff = sessionEventMinBackoff
			continue
		}
		backoff *= 2
		if backoff > sessionEventMaxBackoff {
			backoff = sessionEventMaxBackoff
		}
	}
}

// sessionEventStream is one subscriber's translation state.
type sessionEventStream struct {
	p  *Provider
	ch chan runtime.SessionEvent

	// paneNames maps pane id → gc session name. Rebuilt at cycle start and
	// merge-only within a cycle: entries are never deleted mid-cycle, so a
	// late pane_exited for an agent that already dropped out of the registry
	// (herdr reaps the record before the pane dies) still attributes.
	paneNames map[string]string
	// subscribed tracks pane ids covered by a per-pane agent-status filter in
	// the current cycle; a known agent pane missing from it forces a
	// resubscribe. Removals are lazy — herdr just never fires for a gone pane
	// — so only additions cycle the connection.
	subscribed map[string]bool
	// paneBoundAt mirrors paneNames, keyed the same way: the generation
	// token (metaBoundAt) derivedFilterSet observed for each sidecar pane
	// this cycle. See pruneStalePane for why a herdr pane_not_found
	// rejection needs this to gate its clear.
	paneBoundAt map[string]string
	// pendingResync records that events were dropped on a full channel; the
	// loss is surfaced as a SessionEventResync once the consumer drains.
	pendingResync bool
}

// runCycle runs one connection cycle: list agents, subscribe with the derived
// filter set, emit the leading resync, then translate frames until the
// transport fails (err), the filter set must grow (resubscribe), or ctx ends.
func (s *sessionEventStream) runCycle(ctx context.Context) (resubscribe bool, err error) {
	paneNames, paneBoundAt, err := s.p.derivedFilterSet(ctx)
	if err != nil {
		return false, err
	}
	s.paneNames = paneNames
	s.paneBoundAt = paneBoundAt
	s.subscribed = make(map[string]bool, len(paneNames))
	subs := []subscribeSub{
		{Type: "pane.created"},
		{Type: "pane.closed"},
		{Type: "pane.exited"},
		{Type: "pane.agent_detected"},
	}
	for pane := range s.paneNames {
		if !s.subscribed[pane] {
			s.subscribed[pane] = true
			subs = append(subs, subscribeSub{Type: "pane.agent_status_changed", PaneID: pane})
		}
	}

	// cctx scopes the connection and its reader goroutine to this cycle:
	// every exit path (transport error, resubscribe, parent cancel) cancels
	// it, which closes the conn (unblocking a pending ReadBytes) and releases
	// a reader blocked on handing over a line — otherwise each resubscribe
	// would strand one reader goroutine until the whole subscription ends.
	cctx, ccancel := context.WithCancel(ctx)
	defer ccancel()

	conn, err := s.p.c.dialSocket(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()
	stopAfter := context.AfterFunc(cctx, func() { _ = conn.Close() })
	defer stopAfter()

	_ = conn.SetDeadline(time.Now().Add(sessionEventOpTimeout))
	if err := sockSend(conn, "events.subscribe", subscribeParams{Subscriptions: subs}); err != nil {
		return false, fmt.Errorf("herdr events.subscribe: %w", err)
	}
	reader := bufio.NewReader(conn)
	if _, err := readSockResponse(reader); err != nil {
		// herdr ≥0.8.0 rejects the WHOLE subscribe call if any per-pane
		// filter targets a pane it no longer knows about — e.g. a sidecar
		// binding left over from a session whose pane died without Stop
		// clearing it (a crash, or a server bounce that dropped every pane).
		// Left alone this is a permanent wedge: the same stale pane would be
		// resubmitted every cycle and reject every subscribe forever. Prune
		// the named pane(s) and retry immediately rather than backing off —
		// but only when every prune actually succeeded. A failed prune (e.g.
		// a read-only or failing metadata directory) must fall through to
		// the normal capped-backoff, logged-once-per-streak retry path
		// instead of spinning at full speed against a store that cannot be
		// written.
		if panes, ok := stalePaneIDs(err); ok {
			allCleared := true
			for _, pane := range panes {
				if !s.pruneStalePane(pane) {
					allCleared = false
				}
			}
			if allCleared {
				return true, nil
			}
			return false, fmt.Errorf("herdr events.subscribe: stale pane cleanup failed: %w", err)
		}
		return false, fmt.Errorf("herdr events.subscribe: %w", err)
	}
	// The stream idles indefinitely between events — no deadline from here.
	_ = conn.SetDeadline(time.Time{})

	// Stream established. Events since the last cycle are lost, so the first
	// delivery is the reconcile marker; it also subsumes any resync owed from
	// the previous cycle. This send intentionally blocks: there is no point
	// reading frames a full consumer cannot take, and ctx bounds the wait.
	select {
	case s.ch <- resyncEvent():
		s.pendingResync = false
	case <-ctx.Done():
		return false, ctx.Err()
	}

	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				readErr <- err
				return
			}
			select {
			case lines <- line:
			case <-cctx.Done():
				return
			}
		}
	}()

	relist := time.NewTimer(sessionEventRelistDebounce)
	if !relist.Stop() {
		<-relist.C
	}
	defer relist.Stop()
	relistArmed := false

	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case err := <-readErr:
			return false, err
		case <-relist.C:
			relistArmed = false
			s.tryPendingResync() // piggyback: a drained consumer gets its owed resync even in a quiet stream
			paneNames, paneBoundAt, err := s.p.derivedFilterSet(ctx)
			if err != nil {
				return false, err
			}
			for pane, name := range paneNames {
				s.paneNames[pane] = name
				s.paneBoundAt[pane] = paneBoundAt[pane]
				if !s.subscribed[pane] {
					resubscribe = true
				}
			}
			if resubscribe {
				return true, nil
			}
		case line := <-lines:
			if s.handleFrame(line) && !relistArmed {
				relist.Reset(sessionEventRelistDebounce)
				relistArmed = true
			}
		}
	}
}

// handleFrame translates one wire frame into a SessionEvent. It reports
// whether the frame hints at a new agent pane (arm the debounced re-list).
// Unknown frame shapes and kinds are ignored for forward compatibility.
func (s *sessionEventStream) handleFrame(line []byte) (relistHint bool) {
	var f struct {
		Event string `json:"event"`
		Data  struct {
			PaneID      string `json:"pane_id"`
			AgentStatus string `json:"agent_status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &f); err != nil {
		return false
	}
	ev := runtime.SessionEvent{
		Session: s.paneNames[f.Data.PaneID],
		Ref:     f.Data.PaneID,
		Time:    time.Now(),
	}
	switch f.Event {
	case "pane_exited":
		ev.Kind = runtime.SessionEventExited
	case "pane_closed":
		ev.Kind = runtime.SessionEventClosed
	case "pane_agent_detected":
		// An agent registered — possibly a session started after this cycle's
		// filter set was built.
		ev.Kind = runtime.SessionEventAgentDetected
		relistHint = true
	case "pane.agent_status_changed":
		ev.Kind = runtime.SessionEventAgentStatus
		ev.AgentStatus = f.Data.AgentStatus
	case "pane_created":
		// Filter-maintenance hint only (the payload nests the pane object and
		// carries no agent mapping yet); consumers see the session once its
		// agent registers.
		return true
	default:
		return false
	}
	s.emit(ev)
	return relistHint
}

// emit delivers ev without ever blocking the read loop: an owed resync is
// flushed first, and a full channel converts the event into an owed resync
// (the event itself is dropped — the consumer reconciles on the resync).
func (s *sessionEventStream) emit(ev runtime.SessionEvent) {
	if !s.tryPendingResync() {
		return
	}
	select {
	case s.ch <- ev:
	default:
		s.pendingResync = true
	}
}

// tryPendingResync flushes an owed resync if any; false while one is still owed.
func (s *sessionEventStream) tryPendingResync() bool {
	if !s.pendingResync {
		return true
	}
	select {
	case s.ch <- resyncEvent():
		s.pendingResync = false
		return true
	default:
		return false
	}
}

func resyncEvent() runtime.SessionEvent {
	return runtime.SessionEvent{Kind: runtime.SessionEventResync, Time: time.Now()}
}

// ── socket-native request helpers ────────────────────────────────────────────

// dialSocket connects to this session server's unix socket.
func (c *client) dialSocket(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", c.socketPath())
}

// sockSend writes one request line. params must be non-nil — the server
// rejects requests without a params field.
func sockSend(conn net.Conn, method string, params any) error {
	b, err := json.Marshal(sockRequest{
		ID:     "gc-evt-" + strconv.FormatUint(sockRequestID.Add(1), 10),
		Method: method,
		Params: params,
	})
	if err != nil {
		return err
	}
	_, err = conn.Write(append(b, '\n'))
	return err
}

// readSockResponse reads one response line and unwraps the envelope.
func readSockResponse(r *bufio.Reader) (json.RawMessage, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if env.Error != nil {
		return nil, env.Error
	}
	return env.Result, nil
}

// sockAgentList is agent.list over a dedicated short-lived connection (the
// server serves one request per connection), bounded by sessionEventOpTimeout.
func (c *client) sockAgentList(ctx context.Context) ([]agentInfo, error) {
	conn, err := c.dialSocket(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(sessionEventOpTimeout))
	if err := sockSend(conn, "agent.list", struct{}{}); err != nil {
		return nil, fmt.Errorf("herdr agent.list (socket): %w", err)
	}
	res, err := readSockResponse(bufio.NewReader(conn))
	if err != nil {
		return nil, fmt.Errorf("herdr agent.list (socket): %w", err)
	}
	var wrap struct {
		Agents []agentInfo `json:"agents"`
	}
	if err := json.Unmarshal(res, &wrap); err != nil {
		return nil, fmt.Errorf("herdr agent.list (socket): decode: %w", err)
	}
	return wrap.Agents, nil
}
