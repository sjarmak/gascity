package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/mail/beadmail"
	"github.com/gastownhall/gascity/internal/maildelivery"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

type controllerMailDeliveryCoordinator struct {
	state *controllerState
}

func (cs *controllerState) MailDeliveryCoordinator() api.MailDeliveryCoordinator {
	if cs == nil {
		return nil
	}
	return &controllerMailDeliveryCoordinator{state: cs}
}

type controllerMailDeliverySnapshot struct {
	cfg        *config.City
	provider   runtime.Provider
	workStore  beads.Store
	mailSender stableDurableMailSender
	cityPath   string
	cityName   string
	routes     *storageRoutes
	configHash string
}

func (c *controllerMailDeliveryCoordinator) snapshot() (controllerMailDeliverySnapshot, error) {
	if c == nil || c.state == nil {
		return controllerMailDeliverySnapshot{}, fmt.Errorf("mail delivery coordinator unavailable")
	}
	c.state.mu.RLock()
	snapshot := controllerMailDeliverySnapshot{
		cfg: c.state.cfg, provider: c.state.sp, workStore: c.state.cityBeadStore,
		cityPath: c.state.cityPath, cityName: c.state.cityName, routes: c.state.storageRoutes,
	}
	if sender, ok := c.state.cityMailProv.(stableDurableMailSender); ok {
		snapshot.mailSender = sender
	}
	c.state.mu.RUnlock()
	if snapshot.cfg == nil || snapshot.workStore == nil || snapshot.cityPath == "" || snapshot.cityName == "" {
		return controllerMailDeliverySnapshot{}, fmt.Errorf("mail delivery coordinator state is incomplete")
	}
	_, provenance, err := config.LoadWithIncludes(fsys.OSFS{}, snapshot.cityPath+"/city.toml")
	if err != nil {
		return controllerMailDeliverySnapshot{}, fmt.Errorf("loading mail delivery config provenance: %w", err)
	}
	snapshot.configHash = config.Revision(fsys.OSFS{}, provenance, snapshot.cfg, snapshot.cityPath)
	return snapshot, nil
}

func (c *controllerMailDeliveryCoordinator) SendDurableMail(_ context.Context, command api.DurableMailCommand) (beadmail.DurableSendResult, error) {
	c.state.mailDeliveryMu.Lock()
	defer c.state.mailDeliveryMu.Unlock()
	snapshot, err := c.snapshot()
	if err != nil {
		return beadmail.DurableSendResult{}, err
	}
	if snapshot.mailSender == nil {
		return beadmail.DurableSendResult{}, fmt.Errorf("configured mail provider does not support durable notifications")
	}
	sessionStore := cliSessionStore(snapshot.workStore, snapshot.cfg, snapshot.cityPath)
	seatRef, recipient, err := resolveDurableMailSeat(snapshot.cfg, snapshot.cityName, sessionFrontDoor(sessionStore), command.Recipient)
	if err != nil {
		return beadmail.DurableSendResult{}, err
	}
	sender := ""
	for _, candidate := range command.SenderCandidates {
		sender, err = resolveMailIdentityWithConfig(snapshot.cityPath, snapshot.cfg, sessionStore, candidate)
		if err == nil {
			break
		}
		if !errors.Is(err, session.ErrSessionNotFound) {
			return beadmail.DurableSendResult{}, fmt.Errorf("invalid sender %q: %w", candidate, err)
		}
	}
	if sender == "" {
		return beadmail.DurableSendResult{}, fmt.Errorf("no sender identity resolved")
	}
	cityRef := "city:" + snapshot.cityName
	return snapshot.mailSender.SendDurableStable(sender, recipient, command.Subject, command.Body, beadmail.StableDurableSendIntent{
		CityRef: cityRef, MessagingStoreRef: cityRef + "/messaging", SeatRef: seatRef,
		StableKey: command.StableKey, Policy: maildelivery.PolicyNotifyOnly, Attention: maildelivery.AttentionImmediate,
	})
}

func (c *controllerMailDeliveryCoordinator) deliveryRuntime(snapshot controllerMailDeliverySnapshot, seatRef, sessionRef string) (*maildelivery.Store, *mailDeliveryFenceResolver, maildelivery.AttemptExecutor, error) {
	sessionStore := cliSessionStore(snapshot.workStore, snapshot.cfg, snapshot.cityPath)
	var resolver *mailDeliveryFenceResolver
	var err error
	if sessionRef == "" {
		resolver, err = mailDeliveryFenceResolverForSeat(sessionFrontDoor(sessionStore), snapshot.cfg, snapshot.cityName, seatRef, snapshot.configHash)
	} else {
		resolver = &mailDeliveryFenceResolver{
			store: sessionFrontDoor(sessionStore), sessionRef: sessionRef,
			options: session.MailActivationFenceOptions{
				CityRef: "city:" + snapshot.cityName, SeatRef: seatRef, ConfigSHA256: snapshot.configHash,
				IssuedByRef: "controller:" + snapshot.cityName + "/mail-delivery-api",
			},
		}
	}
	if err != nil {
		return nil, nil, nil, err
	}
	store := maildelivery.NewStore(resolveMailMessagesStore(snapshot.routes, snapshot.workStore, snapshot.cfg, snapshot.cityPath, nil))
	if resolver.sessionRef == "" {
		return store, resolver, nil, nil
	}
	if snapshot.provider == nil {
		return nil, nil, nil, fmt.Errorf("mail delivery session provider unavailable")
	}
	execute := func(ctx context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
		return executeMailDeliveryAttemptWithLookup(ctx, store, attempt, resolver, time.Now().UTC(),
			mailDeliveryWorkerPreflight(snapshot.cityPath, snapshot.cfg, sessionStore, snapshot.provider, resolver),
			mailDeliveryWorkerReceiptLookup(snapshot.cityPath, snapshot.cfg, sessionStore, snapshot.provider, resolver),
			mailDeliveryWorkerInvoker(snapshot.cityPath, snapshot.cfg, sessionStore, snapshot.provider, resolver))
	}
	return store, resolver, execute, nil
}

func (c *controllerMailDeliveryCoordinator) ReconcileMailDeliverySeat(ctx context.Context, seatRef string, limit int, expectedDeliveryID string) (maildelivery.ReconcileReport, error) {
	c.state.mailDeliveryMu.Lock()
	defer c.state.mailDeliveryMu.Unlock()
	if limit <= 0 || limit > 100 || (expectedDeliveryID != "" && (maildelivery.ValidateDeliveryID(expectedDeliveryID) != nil || limit != 1)) {
		return maildelivery.ReconcileReport{}, fmt.Errorf("mail delivery reconcile inputs are invalid")
	}
	snapshot, err := c.snapshot()
	if err != nil {
		return maildelivery.ReconcileReport{}, err
	}
	store, resolver, execute, err := c.deliveryRuntime(snapshot, seatRef, "")
	if err != nil {
		return maildelivery.ReconcileReport{}, err
	}
	return maildelivery.ReconcileExpectedSeat(ctx, store, seatRef, limit, expectedDeliveryID, time.Now().UTC(), resolver, execute)
}

func (c *controllerMailDeliveryCoordinator) InvokeMailDelivery(ctx context.Context, attemptID string) (maildelivery.TransportAttempt, error) {
	c.state.mailDeliveryMu.Lock()
	defer c.state.mailDeliveryMu.Unlock()
	snapshot, err := c.snapshot()
	if err != nil {
		return maildelivery.TransportAttempt{}, err
	}
	store := maildelivery.NewStore(resolveMailMessagesStore(snapshot.routes, snapshot.workStore, snapshot.cfg, snapshot.cityPath, nil))
	attempt, err := store.TransportAttempt(attemptID)
	if err != nil {
		return maildelivery.TransportAttempt{}, err
	}
	delivery, err := store.Get(attempt.DeliveryID)
	if err != nil {
		return attempt, fmt.Errorf("loading canonical delivery for invocation: %w", err)
	}
	seatRef := delivery.SeatRef
	_, _, execute, err := c.deliveryRuntime(snapshot, seatRef, attempt.SessionRef)
	if err != nil {
		return maildelivery.TransportAttempt{}, err
	}
	if execute == nil {
		return attempt, fmt.Errorf("mail delivery transport executor unavailable")
	}
	return execute(ctx, attempt)
}

func (c *controllerMailDeliveryCoordinator) MailDeliveryStatus(_ context.Context, attemptID string) (maildelivery.TransportAttempt, error) {
	snapshot, err := c.snapshot()
	if err != nil {
		return maildelivery.TransportAttempt{}, err
	}
	store := maildelivery.NewStore(resolveMailMessagesStore(snapshot.routes, snapshot.workStore, snapshot.cfg, snapshot.cityPath, nil))
	return store.TransportAttempt(attemptID)
}

var _ api.MailDeliveryCoordinator = (*controllerMailDeliveryCoordinator)(nil)
