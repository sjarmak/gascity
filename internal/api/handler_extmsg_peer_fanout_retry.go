package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/extmsg"
)

// peerFanoutRetryMinGap is the minimum interval between successful
// peer-fanout retry calls per (city, conversation, target_session)
// tuple. Defense-in-depth against amplification: the Python CLI has
// a client-side cooldown but the server can't trust that. Without
// this gate, a misbehaving caller could blast the retry endpoint at
// Slack and trigger workspace-wide rate-limits.
//
// 100ms accommodates the legitimate retry cadence (the client default
// is 250ms cooldown) while bounding the worst-case attempt rate to
// ~10/s per tuple. Per-tuple keying means distinct conversations
// retry independently — the gate is a per-message-stream throttle,
// not a global one.
const peerFanoutRetryMinGap = 100 * time.Millisecond

// peerFanoutRetryGate tracks the last attempt time per
// (cityName, conversation_id, target_session) tuple in-process.
// In-memory, per-process: a distributed deployment would need a
// shared store, but slack-pack is single-process today.
var peerFanoutRetryGate = struct {
	sync.Mutex
	last map[string]time.Time
}{last: make(map[string]time.Time)}

// validatePeerFanoutRetryAgainstFailedEvent looks up the event with
// Seq == originalSeq and confirms it is a peer_fanout_failed event
// whose payload matches the (provider, conversationID, targetSession)
// supplied by the caller. Returns an error suitable for
// huma.Error400BadRequest on any mismatch.
//
// Returns nil when the EventProvider is absent — the validation is
// best-effort defense-in-depth, not a substitute for the auth
// middleware. A test harness without an event provider will still
// work; production deployments always have one.
func validatePeerFanoutRetryAgainstFailedEvent(
	ep events.Provider,
	originalSeq uint64,
	provider, conversationID, targetSession string,
) error {
	if ep == nil {
		return nil
	}
	// AfterSeq: originalSeq-1 narrows to events with Seq >= originalSeq.
	// Filter by Type so we don't scan the full log.
	evs, err := ep.List(events.Filter{
		Type:     events.ExtMsgPeerFanoutFailed,
		AfterSeq: originalSeq - 1,
	})
	if err != nil {
		return fmt.Errorf("event lookup: %w", err)
	}
	var match *events.Event
	for i := range evs {
		if evs[i].Seq == originalSeq {
			match = &evs[i]
			break
		}
	}
	if match == nil {
		return fmt.Errorf("original_seq %d does not reference a known peer_fanout_failed event", originalSeq)
	}
	var payload extmsg.PeerFanoutFailedEventPayload
	if err := json.Unmarshal(match.Payload, &payload); err != nil {
		return fmt.Errorf("decode failed-event payload: %w", err)
	}
	if payload.Provider != provider {
		return fmt.Errorf("provider mismatch: failed event has %q, request has %q", payload.Provider, provider)
	}
	if payload.ConversationID != conversationID {
		return fmt.Errorf("conversation_id mismatch: failed event has %q, request has %q", payload.ConversationID, conversationID)
	}
	if payload.TargetSession != targetSession {
		return fmt.Errorf("target_session mismatch: failed event has %q, request has %q", payload.TargetSession, targetSession)
	}
	return nil
}

// peerFanoutRetryAllow returns false if the (cityName, conversation,
// target) tuple has been retried within peerFanoutRetryMinGap. On
// allow, records the current time so subsequent calls within the gap
// are denied. now is injectable for tests.
func peerFanoutRetryAllow(now func() time.Time, cityName, conversationID, targetSession string) bool {
	key := cityName + "|" + conversationID + "|" + targetSession
	t := now()
	peerFanoutRetryGate.Lock()
	defer peerFanoutRetryGate.Unlock()
	if last, ok := peerFanoutRetryGate.last[key]; ok && t.Sub(last) < peerFanoutRetryMinGap {
		return false
	}
	peerFanoutRetryGate.last[key] = t
	return true
}

// humaHandleExtMsgPeerFanoutRetry is the Huma-typed handler for
// POST /v0/city/{cityName}/extmsg/peer-fanout/retry.
//
// It re-issues a single peer-fanout notification that previously failed
// (as recorded by an extmsg.peer_fanout_failed event) and emits an
// extmsg.peer_fanout_retried audit event with success/failure and the
// original_seq, so the retry CLI can dedupe successful retries on a
// subsequent run.
//
// The handler intentionally does NOT consult the original failed event
// for the message text or actor. The caller passes those forward — the
// failed-event payload IS the source of truth for the caller, and
// re-reading it server-side would just add a round trip.
func (s *Server) humaHandleExtMsgPeerFanoutRetry(
	ctx context.Context,
	input *ExtMsgPeerFanoutRetryInput,
) (*ExtMsgPeerFanoutRetryOutput, error) {
	conv := input.Body.Conversation
	if conv.Provider == "" || conv.ConversationID == "" {
		return nil, huma.Error400BadRequest("conversation.provider and conversation.conversation_id are required")
	}
	target := strings.TrimSpace(input.Body.TargetSession)
	if target == "" {
		return nil, huma.Error400BadRequest("target_session is required")
	}
	if input.Body.OriginalSeq == 0 {
		return nil, huma.Error400BadRequest("original_seq is required")
	}

	// gc-cby.40: validate original_seq references a real
	// peer_fanout_failed event whose payload matches the request's
	// (provider, conversation_id, target_session). This prevents
	// audit-log poisoning (forging "retried" events for arbitrary
	// seqs) and bounds the rate-limit-amplification cardinality to
	// the count of real failures, since an attacker can't fabricate
	// new (conversation, target) tuples that pass validation.
	if err := validatePeerFanoutRetryAgainstFailedEvent(
		s.state.EventProvider(),
		input.Body.OriginalSeq,
		conv.Provider,
		conv.ConversationID,
		target,
	); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}

	if !peerFanoutRetryAllow(time.Now, input.CityName, conv.ConversationID, target) {
		return nil, huma.Error429TooManyRequests(fmt.Sprintf(
			"retry rate limit: minimum %s between retries for the same (conversation, target_session)",
			peerFanoutRetryMinGap))
	}

	store := s.state.CityBeadStore()
	if store == nil {
		return nil, huma.Error503ServiceUnavailable("city bead store not available")
	}

	actorKind := strings.TrimSpace(input.Body.ActorKind)
	if actorKind == "" {
		actorKind = "agent"
	}

	emit := s.extmsgEmitEvent()
	emitRetried := func(success bool, errMsg string) {
		emit(
			events.ExtMsgPeerFanoutRetried,
			fmt.Sprintf("%s/%s", conv.Provider, conv.ConversationID),
			extmsg.PeerFanoutRetriedEventPayload{
				Provider:       conv.Provider,
				ConversationID: conv.ConversationID,
				TargetSession:  target,
				OriginalSeq:    input.Body.OriginalSeq,
				Success:        success,
				Error:          errMsg,
			},
		)
	}

	// Resolve the recipient session id. Prefer the live-session lookup;
	// fall back to materializing a named session so retries work even
	// when the original failure was a resolution failure.
	resolvedID, resolveErr := s.resolveSessionTargetIDWithContext(ctx, store, target, apiSessionResolveOptions{})
	if resolveErr != nil || resolvedID == "" {
		resolvedID, resolveErr = s.resolveSessionIDMaterializingNamedWithContext(ctx, store, target)
	}
	if resolveErr != nil || resolvedID == "" {
		errMsg := "resolve: target session not found"
		if resolveErr != nil {
			errMsg = fmt.Sprintf("resolve: %v", resolveErr)
		}
		emitRetried(false, errMsg)
		out := &ExtMsgPeerFanoutRetryOutput{}
		out.Body.Success = false
		out.Body.Error = errMsg
		out.Body.OriginalSeq = input.Body.OriginalSeq
		return out, nil
	}

	nudge := extmsgPeerFanoutNudge(conv, input.Body.ActorDisplayName, actorKind, input.Body.Text)
	if err := s.sendBackgroundMessageToSession(ctx, store, resolvedID, nudge); err != nil {
		errMsg := fmt.Sprintf("send: %v", err)
		emitRetried(false, errMsg)
		out := &ExtMsgPeerFanoutRetryOutput{}
		out.Body.Success = false
		out.Body.Error = errMsg
		out.Body.OriginalSeq = input.Body.OriginalSeq
		return out, nil
	}

	emitRetried(true, "")
	out := &ExtMsgPeerFanoutRetryOutput{}
	out.Body.Success = true
	out.Body.OriginalSeq = input.Body.OriginalSeq
	return out, nil
}
