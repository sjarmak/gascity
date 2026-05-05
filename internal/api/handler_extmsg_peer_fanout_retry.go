package api

import (
	"context"
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
