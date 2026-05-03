package extmsg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gastownhall/gascity/internal/events"
)

// OutboundFileRequest is the API caller's input for publishing a file
// to an external conversation. It mirrors OutboundRequest but adds the
// file payload fields. Validation: FilePath must be non-empty; the
// adapter is responsible for checking existence and contents.
type OutboundFileRequest struct {
	SessionID        string
	Conversation     ConversationRef
	FilePath         string
	Filename         string
	Title            string
	InitialComment   string
	ReplyToMessageID string
	IdempotencyKey   string
	Metadata         map[string]string
}

// OutboundFileResult captures the outcome of a publish-file operation.
// Receipt carries the FileID; DeliveryContext and TranscriptEntry mirror
// the text-outbound result so callers can treat both paths uniformly.
type OutboundFileResult struct {
	Receipt         PublishFileReceipt
	DeliveryContext *DeliveryContextRecord
	TranscriptEntry *ConversationTranscriptRecord
}

// HandleOutboundFile publishes a file from a session to an external
// conversation. The pipeline mirrors HandleOutbound: resolve binding,
// verify session ownership, look up adapter, publish, record delivery
// context, append a transcript entry, emit an extmsg.outbound event so
// the caller can fan out peer notifications.
//
// The adapter must implement FileTransportAdapter; otherwise this
// returns ErrAdapterUnsupported. The text-outbound path is unaffected.
func HandleOutboundFile(ctx context.Context, deps OutboundDeps, caller Caller, req OutboundFileRequest) (*OutboundFileResult, error) {
	if deps.Registry == nil {
		return nil, errors.New("adapter registry is nil")
	}
	if req.FilePath == "" {
		return nil, errors.New("file_path is required")
	}

	binding, err := deps.Services.Bindings.ResolveByConversation(ctx, req.Conversation)
	if err != nil {
		return nil, fmt.Errorf("resolving binding: %w", err)
	}
	if binding == nil {
		return nil, fmt.Errorf("no active binding for conversation %s/%s",
			req.Conversation.Provider, req.Conversation.ConversationID)
	}

	if req.SessionID != "" && binding.SessionID != req.SessionID {
		return nil, fmt.Errorf("session %q does not own binding for conversation %s/%s (bound to %s)",
			req.SessionID, req.Conversation.Provider, req.Conversation.ConversationID, binding.SessionID)
	}

	adapter := deps.Registry.LookupByConversation(req.Conversation)
	if adapter == nil {
		return nil, fmt.Errorf("no adapter for %s/%s", req.Conversation.Provider, req.Conversation.AccountID)
	}
	fileAdapter, ok := adapter.(FileTransportAdapter)
	if !ok {
		return nil, fmt.Errorf("adapter %s/%s does not support file uploads: %w",
			req.Conversation.Provider, req.Conversation.AccountID, ErrAdapterUnsupported)
	}

	// Field-by-field assignment is intentional: OutboundFileRequest is the
	// API caller's input surface; PublishFileRequest is the gc-to-adapter
	// wire contract. Any future divergence (an internal-only field on
	// OutboundFileRequest) must not silently leak onto the wire.
	receipt, err := fileAdapter.PublishFile(ctx, PublishFileRequest(req))
	if err != nil {
		return nil, fmt.Errorf("adapter publish-file: %w", err)
	}

	result := &OutboundFileResult{Receipt: *receipt}

	if !receipt.Delivered {
		return result, nil
	}

	now := time.Now()
	dc := DeliveryContextRecord{
		SessionID:         binding.SessionID,
		Conversation:      req.Conversation,
		BindingGeneration: binding.BindingGeneration,
		LastPublishedAt:   now,
		LastMessageID:     receipt.FileID,
		SourceSessionID:   req.SessionID,
		Metadata:          req.Metadata,
	}
	if err := deps.Services.Delivery.Record(ctx, caller, dc); err != nil {
		result.DeliveryContext = nil
	} else {
		result.DeliveryContext = &dc
	}

	// Append a transcript entry so other sessions sharing this conversation
	// see that a file was sent. InitialComment is the natural display text
	// (Slack puts it next to the file preview); fall back to the filename
	// for searchability when the caller posted bare bytes.
	displayText := req.InitialComment
	if displayText == "" {
		switch {
		case req.Filename != "":
			displayText = "[file] " + req.Filename
		case req.Title != "":
			displayText = "[file] " + req.Title
		default:
			displayText = "[file]"
		}
	}
	attachment := ExternalAttachment{
		ProviderID: receipt.FileID,
	}
	entry, err := deps.Services.Transcript.Append(ctx, AppendTranscriptInput{
		Caller:            caller,
		Conversation:      req.Conversation,
		Kind:              TranscriptMessageOutbound,
		Provenance:        TranscriptProvenanceLive,
		ProviderMessageID: receipt.FileID,
		Text:              displayText,
		Attachments:       []ExternalAttachment{attachment},
		SourceSessionID:   req.SessionID,
		CreatedAt:         now,
		Metadata:          req.Metadata,
	})
	if err == nil {
		result.TranscriptEntry = &entry
	}

	if deps.EmitEvent != nil {
		deps.EmitEvent(events.ExtMsgOutbound, binding.SessionID, OutboundEventPayload{
			Provider:       req.Conversation.Provider,
			ConversationID: req.Conversation.ConversationID,
			Session:        req.SessionID,
			MessageID:      receipt.FileID,
		})
	}

	return result, nil
}
