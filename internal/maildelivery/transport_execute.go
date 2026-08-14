package maildelivery

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// TransportInvoker performs the one provider call authorized by an invoking
// attempt. It returns destination evidence or an error whose external state is
// conservatively unknown.
type TransportInvoker func(context.Context, TransportAttempt) (TransportReceipt, error)

// TransportPreflight proves authority and destination availability before the
// invoking CAS. A failure leaves the durable intent requested and retryable.
type TransportPreflight func(context.Context, TransportAttempt) error

// ExecuteTransport applies the requested->invoking->committed|unknown protocol.
// Replays never invoke a terminal or already-invoking attempt.
func ExecuteTransport(ctx context.Context, store *Store, request TransportAttemptRequest, resolver FenceResolver, uncertainAt time.Time, preflight TransportPreflight, invoke TransportInvoker) (TransportAttempt, error) {
	if store == nil || invoke == nil {
		return TransportAttempt{}, fmt.Errorf("mail delivery transport execution is unavailable")
	}
	if uncertainAt.IsZero() || uncertainAt.Location() != time.UTC {
		return TransportAttempt{}, fmt.Errorf("mail delivery transport uncertainty time must be UTC")
	}
	attempt, err := store.CreateTransportAttempt(ctx, request, resolver)
	if err != nil {
		return TransportAttempt{}, err
	}
	switch attempt.State {
	case TransportCommitted:
		if err := store.finalizeCommittedTransport(attempt); err != nil {
			return attempt, err
		}
		return attempt, nil
	case TransportUnknownExternalState:
		return attempt, nil
	case TransportInvoking:
		if uncertainAt.Before(attempt.InvocationLeaseUntil) {
			return attempt, fmt.Errorf("%w until %s", ErrTransportInvocationInProgress, attempt.InvocationLeaseUntil.Format(time.RFC3339Nano))
		}
		return store.RecordUnknownTransportState(attempt.AttemptID, attempt.Revision, uncertainAt)
	case TransportRequested:
		// Continue below; this caller still needs to win the invocation CAS.
	default:
		return TransportAttempt{}, fmt.Errorf("mail delivery transport attempt has invalid state %q", attempt.State)
	}
	if preflight != nil {
		if err := preflight(ctx, attempt); err != nil {
			return attempt, err
		}
	}
	invoking, err := store.BeginTransportInvocation(attempt.AttemptID, attempt.Revision, uncertainAt)
	if err != nil {
		return TransportAttempt{}, err
	}
	receipt, invokeErr := invoke(ctx, invoking)
	if invokeErr != nil {
		unknown, markErr := store.RecordUnknownTransportState(invoking.AttemptID, invoking.Revision, uncertainAt)
		return unknown, errors.Join(fmt.Errorf("mail delivery transport invocation is indeterminate: %w", invokeErr), markErr)
	}
	switch receipt.State {
	case EffectCommitted:
		return store.RecordTransportReceipt(invoking.AttemptID, invoking.Revision, receipt)
	case EffectUnknownExternalState:
		return store.RecordUnknownTransportState(invoking.AttemptID, invoking.Revision, receipt.RecordedAt)
	default:
		unknown, markErr := store.RecordUnknownTransportState(invoking.AttemptID, invoking.Revision, uncertainAt)
		return unknown, errors.Join(fmt.Errorf("mail delivery transport invocation returned no typed outcome"), markErr)
	}
}
