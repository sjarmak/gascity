package api

import (
	"context"
	"errors"

	"github.com/gastownhall/gascity/internal/api/apierr"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/mail/beadmail"
	"github.com/gastownhall/gascity/internal/maildelivery"
	"github.com/gastownhall/gascity/internal/telemetry"
)

func mailDeliveryAPIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, maildelivery.ErrConflict) {
		return apierr.ConflictWrongState.Msg(err.Error())
	}
	return apierr.Internal.Msg(err.Error())
}

func mailDeliveryFailureCode(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, maildelivery.ErrTransportRetryEscalated):
		return "retry_escalated"
	case errors.Is(err, maildelivery.ErrTransportReceiptLookupRetryLater):
		return "receipt_lookup_retry_later"
	case errors.Is(err, maildelivery.ErrTransportRetrySafe):
		return "retry_safe"
	case errors.Is(err, maildelivery.ErrTransportInvocationInProgress), errors.Is(err, maildelivery.ErrTransportInvocationRace):
		return "invocation_in_progress"
	default:
		return "operation_failed"
	}
}

func (s *Server) humaHandleMailDurableSend(ctx context.Context, input *MailDurableSendInput) (*MailDurableSendOutput, error) {
	coordinator := coordinatorFromState(s.state)
	if coordinator == nil {
		return nil, apierr.ServiceUnavailable.Msg("mail delivery coordinator unavailable")
	}
	result, err := coordinator.SendDurableMail(ctx, DurableMailCommand{
		StableKey: input.Body.StableKey, Recipient: input.Body.Recipient,
		SenderCandidates: input.Body.SenderCandidates, Subject: input.Body.Subject, Body: input.Body.Message,
	})
	switch result.Outcome {
	case beadmail.DurableSendCreated:
		// MailSent means the canonical message is durably visible. A later
		// delivery-intent write failure is returned alongside this exact result
		// and does not erase the already-created message or its event.
		telemetry.RecordMailOp(ctx, "send", err)
		s.recordMailEvent(events.MailSent, result.Message.From, result.Message.ID, "", &result.Message)
	case beadmail.DurableSendMessageOnlyRepaired:
		telemetry.RecordMailOp(ctx, "send_repair", err)
	}
	if err != nil && result.Message.ID == "" {
		return nil, mailDeliveryAPIError(err)
	}
	return &MailDurableSendOutput{Body: MailDurableSendBody{OK: err == nil, FailureCode: mailDeliveryFailureCode(err), Result: result}}, nil
}

func (s *Server) humaHandleMailDeliveryReconcile(ctx context.Context, input *MailDeliveryReconcileInput) (*MailDeliveryReconcileOutput, error) {
	coordinator := coordinatorFromState(s.state)
	if coordinator == nil {
		return nil, apierr.ServiceUnavailable.Msg("mail delivery coordinator unavailable")
	}
	report, err := coordinator.ReconcileMailDeliverySeat(ctx, input.Body.SeatRef, input.Body.Limit, input.Body.ExpectedDeliveryID)
	if err != nil && report.SchemaVersion == "" {
		return nil, mailDeliveryAPIError(err)
	}
	return &MailDeliveryReconcileOutput{Body: MailDeliveryReconcileBody{OK: err == nil, FailureCode: mailDeliveryFailureCode(err), Report: report}}, nil
}

func (s *Server) humaHandleMailDeliveryInvoke(ctx context.Context, input *MailDeliveryAttemptInput) (*MailDeliveryAttemptOutput, error) {
	coordinator := coordinatorFromState(s.state)
	if coordinator == nil {
		return nil, apierr.ServiceUnavailable.Msg("mail delivery coordinator unavailable")
	}
	attempt, err := coordinator.InvokeMailDelivery(ctx, input.AttemptID)
	if err != nil && attempt.AttemptID == "" {
		return nil, mailDeliveryAPIError(err)
	}
	return &MailDeliveryAttemptOutput{Body: MailDeliveryAttemptBody{OK: err == nil, FailureCode: mailDeliveryFailureCode(err), Attempt: attempt}}, nil
}

func (s *Server) humaHandleMailDeliveryStatus(ctx context.Context, input *MailDeliveryAttemptInput) (*MailDeliveryAttemptOutput, error) {
	coordinator := coordinatorFromState(s.state)
	if coordinator == nil {
		return nil, apierr.ServiceUnavailable.Msg("mail delivery coordinator unavailable")
	}
	attempt, err := coordinator.MailDeliveryStatus(ctx, input.AttemptID)
	if err != nil {
		return nil, mailDeliveryAPIError(err)
	}
	return &MailDeliveryAttemptOutput{Body: MailDeliveryAttemptBody{OK: true, Attempt: attempt}}, nil
}
