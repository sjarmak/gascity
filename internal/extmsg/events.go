package extmsg

import "github.com/gastownhall/gascity/internal/events"

// Extmsg event payloads. Each type implements events.Payload so it
// flows through the bus's central registry and emerges on the typed
// /v0/events/stream wire with a named schema (Principle 7).
//
// Event type constants live in internal/events (events.ExtMsg*).

// InboundEventPayload is emitted on events.ExtMsgInbound ("extmsg.inbound").
// Actor is the inbound speaker's display name; TargetSession is the
// resolved recipient session (empty if no routing match).
type InboundEventPayload struct {
	Provider       string `json:"provider"`
	ConversationID string `json:"conversation_id"`
	Actor          string `json:"actor"`
	TargetSession  string `json:"target_session"`
}

// IsEventPayload marks InboundEventPayload as an events.Payload variant.
func (InboundEventPayload) IsEventPayload() {}

// OutboundEventPayload is emitted on "extmsg.outbound" events.
type OutboundEventPayload struct {
	Provider       string `json:"provider"`
	ConversationID string `json:"conversation_id"`
	Session        string `json:"session"`
	MessageID      string `json:"message_id"`
}

// IsEventPayload marks OutboundEventPayload as an events.Payload variant.
func (OutboundEventPayload) IsEventPayload() {}

// BoundEventPayload is emitted on events.ExtMsgBound (binding a
// conversation to a session).
type BoundEventPayload struct {
	Provider       string `json:"provider"`
	ConversationID string `json:"conversation_id"`
	SessionID      string `json:"session_id"`
}

// IsEventPayload marks BoundEventPayload as an events.Payload variant.
func (BoundEventPayload) IsEventPayload() {}

// UnboundEventPayload is emitted on events.ExtMsgUnbound.
type UnboundEventPayload struct {
	SessionID string `json:"session_id"`
	Count     int    `json:"count"`
}

// IsEventPayload marks UnboundEventPayload as an events.Payload variant.
func (UnboundEventPayload) IsEventPayload() {}

// GroupCreatedEventPayload is emitted on events.ExtMsgGroupCreated.
type GroupCreatedEventPayload struct {
	Provider       string `json:"provider"`
	ConversationID string `json:"conversation_id"`
	Mode           string `json:"mode"`
}

// IsEventPayload marks GroupCreatedEventPayload as an events.Payload variant.
func (GroupCreatedEventPayload) IsEventPayload() {}

// AdapterEventPayload is emitted on events.ExtMsgAdapterAdded and
// events.ExtMsgAdapterRemoved — both carry the same (provider, account)
// identity pair.
type AdapterEventPayload struct {
	Provider  string `json:"provider"`
	AccountID string `json:"account_id"`
}

// IsEventPayload marks AdapterEventPayload as an events.Payload variant.
func (AdapterEventPayload) IsEventPayload() {}

// PeerFanoutFailedEventPayload is emitted on events.ExtMsgPeerFanoutFailed
// when the per-member peer notification issued by extmsgNotifyMembers
// cannot be delivered (resolution failure, session-message failure,
// rate-limit). Carries enough context for an out-of-band retry tool to
// re-issue the notification verbatim.
type PeerFanoutFailedEventPayload struct {
	Provider         string `json:"provider"`
	ScopeID          string `json:"scope_id"`
	AccountID        string `json:"account_id"`
	ConversationID   string `json:"conversation_id"`
	Kind             string `json:"kind"`
	TargetSession    string `json:"target_session"`
	ActorDisplayName string `json:"actor_display_name"`
	ActorKind        string `json:"actor_kind"`
	Text             string `json:"text"`
	Reason           string `json:"reason"`
}

// IsEventPayload marks PeerFanoutFailedEventPayload as an events.Payload variant.
func (PeerFanoutFailedEventPayload) IsEventPayload() {}

// PeerFanoutRetriedEventPayload is emitted on events.ExtMsgPeerFanoutRetried
// per retry attempt issued by `gc <provider> retry-peer-fanout`. OriginalSeq
// is the seq number of the corresponding ExtMsgPeerFanoutFailed event so
// the retry tool can dedupe successful attempts on a subsequent run.
type PeerFanoutRetriedEventPayload struct {
	Provider       string `json:"provider"`
	ConversationID string `json:"conversation_id"`
	TargetSession  string `json:"target_session"`
	OriginalSeq    uint64 `json:"original_seq"`
	Success        bool   `json:"success"`
	Error          string `json:"error,omitempty"`
}

// IsEventPayload marks PeerFanoutRetriedEventPayload as an events.Payload variant.
func (PeerFanoutRetriedEventPayload) IsEventPayload() {}

func init() {
	events.RegisterPayload(events.ExtMsgBound, BoundEventPayload{})
	events.RegisterPayload(events.ExtMsgUnbound, UnboundEventPayload{})
	events.RegisterPayload(events.ExtMsgGroupCreated, GroupCreatedEventPayload{})
	events.RegisterPayload(events.ExtMsgAdapterAdded, AdapterEventPayload{})
	events.RegisterPayload(events.ExtMsgAdapterRemoved, AdapterEventPayload{})
	events.RegisterPayload(events.ExtMsgInbound, InboundEventPayload{})
	events.RegisterPayload(events.ExtMsgOutbound, OutboundEventPayload{})
	events.RegisterPayload(events.ExtMsgPeerFanoutFailed, PeerFanoutFailedEventPayload{})
	events.RegisterPayload(events.ExtMsgPeerFanoutRetried, PeerFanoutRetriedEventPayload{})
}
