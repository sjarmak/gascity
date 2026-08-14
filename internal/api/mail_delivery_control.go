package api

import (
	"context"

	"github.com/gastownhall/gascity/internal/mail/beadmail"
	"github.com/gastownhall/gascity/internal/maildelivery"
)

// DurableMailCommand contains caller intent only. The service resolves city,
// store, configured-seat, sender, and provider authority from supervisor state.
type DurableMailCommand struct {
	StableKey        string
	Recipient        string
	SenderCandidates []string
	Subject          string
	Body             string
}

// MailDeliveryCoordinator is the canonical application service for durable
// mail mutations. The supervisor implementation owns canonical stores,
// provider calls, and session mutation locks; Huma handlers only translate.
type MailDeliveryCoordinator interface {
	SendDurableMail(context.Context, DurableMailCommand) (beadmail.DurableSendResult, error)
	ReconcileMailDeliverySeat(context.Context, string, int, string) (maildelivery.ReconcileReport, error)
	InvokeMailDelivery(context.Context, string) (maildelivery.TransportAttempt, error)
	MailDeliveryStatus(context.Context, string) (maildelivery.TransportAttempt, error)
}

type mailDeliveryCoordinatorProvider interface {
	MailDeliveryCoordinator() MailDeliveryCoordinator
}

func coordinatorFromState(state State) MailDeliveryCoordinator {
	provider, ok := state.(mailDeliveryCoordinatorProvider)
	if !ok {
		return nil
	}
	return provider.MailDeliveryCoordinator()
}
