package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/maildelivery"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
	"github.com/spf13/cobra"
)

type mailDeliveryAttemptResult struct {
	SchemaVersion string                        `json:"schema_version"`
	Attempt       maildelivery.TransportAttempt `json:"attempt"`
}

type mailDeliveryReconcileOutcome string

const (
	mailDeliveryReconcileWaitingForAuthority  mailDeliveryReconcileOutcome = "waiting_for_authority"
	mailDeliveryReconcileCommitted            mailDeliveryReconcileOutcome = "committed"
	mailDeliveryReconcileUnknownExternalState mailDeliveryReconcileOutcome = "unknown_external_state"
	mailDeliveryReconcileRetryable            mailDeliveryReconcileOutcome = "retryable"
)

type mailDeliveryReconcileItem struct {
	DeliveryID string                         `json:"delivery_id"`
	Phase      maildelivery.Phase             `json:"phase"`
	Outcome    mailDeliveryReconcileOutcome   `json:"outcome"`
	Attempt    *maildelivery.TransportAttempt `json:"attempt,omitempty"`
}

type mailDeliveryReconcileReport struct {
	SchemaVersion         string                       `json:"schema_version"`
	SeatRef               string                       `json:"seat_ref"`
	ObservedAt            time.Time                    `json:"observed_at"`
	ExpectedDeliveryID    string                       `json:"expected_delivery_id,omitempty"`
	ExpectedDeliveryPhase maildelivery.Phase           `json:"expected_delivery_phase,omitempty"`
	PageCommitted         bool                         `json:"page_committed"`
	ActionRequired        bool                         `json:"action_required"`
	Checkpoint            maildelivery.SweepCheckpoint `json:"checkpoint"`
	Plan                  maildelivery.SweepPlan       `json:"plan"`
	Deliveries            []mailDeliveryReconcileItem  `json:"deliveries"`
}

type mailDeliveryAttemptExecutor func(context.Context, maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error)

func mailDeliveryNudgeText(count int) string {
	if count == 1 {
		return "1 actionable mail delivery; run gc mail inbox"
	}
	return fmt.Sprintf("%d actionable mail deliveries; run gc mail inbox", count)
}

func newMailDeliveryCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "delivery", Short: "Operate durable mail delivery attempts"}
	cmd.AddCommand(&cobra.Command{
		Use: "status <attempt-id>", Short: "Read one durable mail delivery attempt", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdMailDeliveryStatus(args[0], stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "invoke <attempt-id>", Short: "Invoke one requested mail delivery attempt", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if cmdMailDeliveryInvoke(c.Context(), args[0], stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	})
	var reconcileLimit int
	var expectedDeliveryID string
	reconcile := &cobra.Command{
		Use: "reconcile-seat <seat-ref>", Short: "Reconcile one bounded body-free seat delivery page", Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if cmdMailDeliveryReconcileSeat(c.Context(), args[0], reconcileLimit, expectedDeliveryID, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	reconcile.Flags().IntVar(&reconcileLimit, "limit", 50, "maximum deliveries to process (1-100)")
	reconcile.Flags().StringVar(&expectedDeliveryID, "expect-delivery-id", "", "require exactly this delivery; a mismatch may retain a body-free sweep checkpoint")
	cmd.AddCommand(reconcile)
	return cmd
}

func cmdMailDeliveryReconcileSeat(ctx context.Context, seatRef string, limit int, expectedDeliveryID string, stdout, stderr io.Writer) int {
	if limit <= 0 || limit > 100 {
		fmt.Fprintln(stderr, "gc mail delivery reconcile-seat: --limit must be between 1 and 100") //nolint:errcheck
		return 1
	}
	if expectedDeliveryID != "" && (!validMailDeliveryCLIIdentity(expectedDeliveryID) || limit != 1) {
		fmt.Fprintln(stderr, "gc mail delivery reconcile-seat: --expect-delivery-id requires one valid delivery ID and --limit 1") //nolint:errcheck
		return 1
	}
	workStore, cityPath, code := openCityStoreWithPath(stderr, "gc mail delivery reconcile-seat")
	if workStore == nil {
		return code
	}
	defer closeBeadStoreHandle(workStore) //nolint:errcheck
	cfg, prov, err := config.LoadWithIncludes(fsys.OSFS{}, filepath.Join(cityPath, "city.toml"))
	if err != nil {
		fmt.Fprintf(stderr, "gc mail delivery reconcile-seat: load city config: %v\n", err) //nolint:errcheck
		return 1
	}
	cityName := loadedCityName(cfg, cityPath)
	sessStore := cliSessionStore(workStore, cfg, cityPath)
	resolver, err := mailDeliveryFenceResolverForSeat(sessionFrontDoor(sessStore), cfg, cityName, seatRef,
		config.Revision(fsys.OSFS{}, prov, cfg, cityPath))
	if err != nil {
		fmt.Fprintf(stderr, "gc mail delivery reconcile-seat: %v\n", err) //nolint:errcheck
		return 1
	}
	deliveryStore := maildelivery.NewStore(resolveMailMessagesStore(cliStorageRoutes(cityPath), workStore, cfg, cityPath, nil))
	var execute mailDeliveryAttemptExecutor
	if resolver.sessionRef != "" {
		provider, providerErr := newSessionProviderForCity(cfg, cityPath)
		if providerErr != nil {
			fmt.Fprintf(stderr, "gc mail delivery reconcile-seat: provider: %v\n", providerErr) //nolint:errcheck
			return 1
		}
		execute = func(callCtx context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportAttempt, error) {
			return executeMailDeliveryAttemptWithLookup(callCtx, deliveryStore, attempt, resolver, time.Now().UTC(),
				mailDeliveryWorkerPreflight(cityPath, cfg, sessStore, provider, resolver),
				mailDeliveryWorkerReceiptLookup(cityPath, cfg, sessStore, provider, resolver),
				mailDeliveryWorkerInvoker(cityPath, cfg, sessStore, provider, resolver))
		}
	}
	report, reconcileErr := reconcileExpectedMailDeliverySeat(ctx, deliveryStore, seatRef, limit, expectedDeliveryID, time.Now().UTC(), resolver, execute)
	writeCode := writeMailDeliveryReconcileReport(stdout, stderr, report)
	if reconcileErr != nil {
		fmt.Fprintf(stderr, "gc mail delivery reconcile-seat: %v\n", reconcileErr) //nolint:errcheck
		return 1
	}
	if writeCode != 0 || report.ActionRequired {
		if report.ActionRequired {
			fmt.Fprintln(stderr, "gc mail delivery reconcile-seat: durable delivery state requires operator attention") //nolint:errcheck
		}
		return 1
	}
	return 0
}

func reconcileMailDeliverySeat(ctx context.Context, store *maildelivery.Store, seatRef string, limit int, observedAt time.Time,
	resolver maildelivery.FenceResolver, execute mailDeliveryAttemptExecutor,
) (mailDeliveryReconcileReport, error) {
	return reconcileExpectedMailDeliverySeat(ctx, store, seatRef, limit, "", observedAt, resolver, execute)
}

func reconcileExpectedMailDeliverySeat(ctx context.Context, store *maildelivery.Store, seatRef string, limit int, expectedDeliveryID string,
	observedAt time.Time, resolver maildelivery.FenceResolver, execute mailDeliveryAttemptExecutor,
) (mailDeliveryReconcileReport, error) {
	report := mailDeliveryReconcileReport{
		SchemaVersion: "mail-delivery-reconcile/v1", SeatRef: seatRef, ObservedAt: observedAt,
		Deliveries: make([]mailDeliveryReconcileItem, 0, limit),
	}
	if store == nil || observedAt.IsZero() || observedAt.Location() != time.UTC {
		return report, fmt.Errorf("mail delivery reconcile inputs are invalid")
	}
	if expectedDeliveryID != "" {
		if !validMailDeliveryCLIIdentity(expectedDeliveryID) || limit != 1 {
			return report, fmt.Errorf("mail delivery exact canary inputs are invalid")
		}
		expected, err := store.Get(expectedDeliveryID)
		if err != nil {
			return report, err
		}
		if expected.SeatRef != seatRef {
			return report, fmt.Errorf("mail delivery canary seat differs from expected delivery")
		}
		if expected.Phase == maildelivery.PhaseDispositioned {
			report.ExpectedDeliveryID = expected.ID
			report.ExpectedDeliveryPhase = expected.Phase
			return report, nil
		}
	}
	checkpoint, plan, err := store.PlanSweepPage(seatRef, limit)
	report.Checkpoint = checkpoint
	report.Plan = plan
	if err != nil {
		return report, err
	}
	if expectedDeliveryID != "" {
		if !validMailDeliveryCLIIdentity(expectedDeliveryID) || limit != 1 || len(plan.Page) != 1 || plan.Page[0].DeliveryID != expectedDeliveryID {
			return report, fmt.Errorf("mail delivery canary page differs from expected delivery %q", expectedDeliveryID)
		}
	}
	if checkpoint.HighWatermark.IsZero() {
		return report, nil
	}
	pageIDs := make([]string, 0, len(plan.Page))
	pageSet := make(map[string]struct{}, len(plan.Page))
	for _, key := range plan.Page {
		pageIDs = append(pageIDs, key.DeliveryID)
		pageSet[key.DeliveryID] = struct{}{}
	}
	attempts := make(map[string]maildelivery.TransportAttempt)
	attemptOrder := make([]string, 0, len(pageIDs))
	handled := make(map[string]struct{}, len(pageIDs))
	waiting := make([]string, 0, len(pageIDs))

	// Recover existing groups first, including a crash after the primary link
	// but before every covered delivery link became durable.
	for _, deliveryID := range pageIDs {
		delivery, loadErr := store.Get(deliveryID)
		if loadErr != nil {
			return report, loadErr
		}
		if delivery.Phase == maildelivery.PhaseStored || delivery.Phase == maildelivery.PhaseWaitingForActivation {
			continue
		}
		attempt, prepareErr := store.PrepareTransportAttempt(ctx, deliveryID, observedAt, resolver)
		if errors.Is(prepareErr, maildelivery.ErrTransportAuthorityChanged) {
			continue
		}
		if prepareErr != nil {
			if errors.Is(prepareErr, maildelivery.ErrAuthorityUnavailable) {
				coveredIDs := []string{deliveryID}
				if attempt.AttemptID != "" {
					coveredIDs = attempt.CoveredDeliveryIDs
				}
				for _, covered := range coveredIDs {
					if _, inPage := pageSet[covered]; !inPage {
						continue
					}
					current, currentErr := store.Get(covered)
					if currentErr != nil {
						return report, currentErr
					}
					report.Deliveries = append(report.Deliveries, mailDeliveryReconcileItem{
						DeliveryID: covered, Phase: current.Phase, Outcome: mailDeliveryReconcileWaitingForAuthority,
					})
					handled[covered] = struct{}{}
				}
				continue
			}
			if errors.Is(prepareErr, maildelivery.ErrTransportInvocationInProgress) {
				if _, exists := attempts[attempt.AttemptID]; !exists {
					attemptOrder = append(attemptOrder, attempt.AttemptID)
				}
				attempts[attempt.AttemptID] = attempt
				for _, covered := range attempt.CoveredDeliveryIDs {
					if _, inPage := pageSet[covered]; inPage {
						handled[covered] = struct{}{}
					}
				}
				continue
			}
			return report, prepareErr
		}
		if err := store.LinkCoveredTransportAttempt(attempt); err != nil {
			return report, err
		}
		if _, exists := attempts[attempt.AttemptID]; !exists {
			attemptOrder = append(attemptOrder, attempt.AttemptID)
		}
		attempts[attempt.AttemptID] = attempt
		for _, covered := range attempt.CoveredDeliveryIDs {
			if _, inPage := pageSet[covered]; inPage {
				handled[covered] = struct{}{}
			}
		}
	}
	for _, deliveryID := range pageIDs {
		if _, alreadyHandled := handled[deliveryID]; !alreadyHandled {
			waiting = append(waiting, deliveryID)
		}
	}
	if len(waiting) > 0 {
		attempt, prepareErr := store.PrepareTransportBatch(ctx, waiting, observedAt, resolver)
		if prepareErr != nil {
			if !errors.Is(prepareErr, maildelivery.ErrAuthorityUnavailable) {
				return report, prepareErr
			}
			for _, deliveryID := range waiting {
				delivery, loadErr := store.Get(deliveryID)
				if loadErr != nil {
					return report, loadErr
				}
				if delivery.Phase == maildelivery.PhaseStored {
					delivery, loadErr = store.Advance(delivery.ID, delivery.Revision, maildelivery.PhaseWaitingForActivation)
					if loadErr != nil {
						return report, loadErr
					}
				}
				report.Deliveries = append(report.Deliveries, mailDeliveryReconcileItem{
					DeliveryID: delivery.ID, Phase: delivery.Phase, Outcome: mailDeliveryReconcileWaitingForAuthority,
				})
			}
		} else {
			attemptOrder = append(attemptOrder, attempt.AttemptID)
			attempts[attempt.AttemptID] = attempt
		}
	}
	items := make(map[string]mailDeliveryReconcileItem, len(pageIDs))
	for _, item := range report.Deliveries {
		items[item.DeliveryID] = item
	}
	for _, attemptID := range attemptOrder {
		attempt := attempts[attemptID]
		result := attempt
		var executeErr error
		var outcome mailDeliveryReconcileOutcome
		switch attempt.State {
		case maildelivery.TransportCommitted:
			outcome = mailDeliveryReconcileCommitted
		case maildelivery.TransportUnknownExternalState:
			outcome = mailDeliveryReconcileUnknownExternalState
		default:
			if execute == nil {
				return report, fmt.Errorf("mail delivery transport executor is unavailable")
			}
			result, executeErr = execute(ctx, attempt)
		}
		var classifyErr error
		if outcome == "" {
			outcome, classifyErr = classifyMailDeliveryExecution(result, executeErr)
		}
		if classifyErr != nil {
			return report, classifyErr
		}
		report.ActionRequired = report.ActionRequired || outcome == mailDeliveryReconcileUnknownExternalState || errors.Is(executeErr, maildelivery.ErrTransportRetryEscalated)
		for _, deliveryID := range attempt.CoveredDeliveryIDs {
			if _, inPage := pageSet[deliveryID]; !inPage {
				continue
			}
			delivery, loadErr := store.Get(deliveryID)
			if loadErr != nil {
				return report, loadErr
			}
			if outcome == mailDeliveryReconcileCommitted && delivery.Policy == maildelivery.PolicyNotifyOnly && delivery.Phase == maildelivery.PhaseRuntimeNotified {
				if _, dispositionErr := store.RecordDisposition(ctx, maildelivery.DispositionRequest{
					DeliveryID: delivery.ID, ExpectedDeliveryRevision: delivery.Revision,
					Reason: maildelivery.DispositionPolicySatisfiedNotified, RecordedAt: result.Receipt.RecordedAt,
				}, resolver); dispositionErr != nil {
					return report, dispositionErr
				}
				delivery, loadErr = store.Get(deliveryID)
				if loadErr != nil {
					return report, loadErr
				}
			}
			resultCopy := result
			items[deliveryID] = mailDeliveryReconcileItem{DeliveryID: deliveryID, Phase: delivery.Phase, Outcome: outcome, Attempt: &resultCopy}
		}
	}
	report.Deliveries = report.Deliveries[:0]
	for _, deliveryID := range pageIDs {
		item, ok := items[deliveryID]
		if !ok {
			return report, fmt.Errorf("mail delivery %q has no durable page result", deliveryID)
		}
		report.Deliveries = append(report.Deliveries, item)
	}
	committed, err := store.CommitSweepPage(checkpoint, plan)
	if err != nil {
		return report, err
	}
	report.Checkpoint = committed
	report.PageCommitted = true
	return report, nil
}

func validMailDeliveryCLIIdentity(value string) bool {
	// Keep this wire identity in lockstep with temporalbeads.validateMailDeliveryID
	// in the gas-city Temporal service; the repositories intentionally do not share a module.
	const prefix = "mail-delivery-"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

func classifyMailDeliveryExecution(result maildelivery.TransportAttempt, err error) (mailDeliveryReconcileOutcome, error) {
	if result.State == maildelivery.TransportUnknownExternalState {
		return mailDeliveryReconcileUnknownExternalState, nil
	}
	if err != nil && (errors.Is(err, maildelivery.ErrTransportRetrySafe) ||
		errors.Is(err, maildelivery.ErrTransportInvocationInProgress) || errors.Is(err, maildelivery.ErrTransportInvocationRace)) {
		return mailDeliveryReconcileRetryable, nil
	}
	if err != nil {
		return "", err
	}
	if result.State == maildelivery.TransportCommitted {
		return mailDeliveryReconcileCommitted, nil
	}
	return "", fmt.Errorf("mail delivery returned nonterminal transport state %q", result.State)
}

func cmdMailDeliveryStatus(attemptID string, stdout, stderr io.Writer) int {
	workStore, cityPath, code := openCityStoreWithPath(stderr, "gc mail delivery status")
	if workStore == nil {
		return code
	}
	defer closeBeadStoreHandle(workStore) //nolint:errcheck
	cfg, _, err := config.LoadWithIncludes(fsys.OSFS{}, filepath.Join(cityPath, "city.toml"))
	if err != nil {
		fmt.Fprintf(stderr, "gc mail delivery status: load city config: %v\n", err) //nolint:errcheck
		return 1
	}
	store := maildelivery.NewStore(resolveMailMessagesStore(cliStorageRoutes(cityPath), workStore, cfg, cityPath, nil))
	attempt, err := store.TransportAttempt(attemptID)
	if err != nil {
		fmt.Fprintf(stderr, "gc mail delivery status: %v\n", err) //nolint:errcheck
		return 1
	}
	return writeMailDeliveryAttempt(stdout, stderr, "status", attempt)
}

func cmdMailDeliveryInvoke(ctx context.Context, attemptID string, stdout, stderr io.Writer) int {
	workStore, cityPath, code := openCityStoreWithPath(stderr, "gc mail delivery invoke")
	if workStore == nil {
		return code
	}
	defer closeBeadStoreHandle(workStore) //nolint:errcheck
	cfg, prov, err := config.LoadWithIncludes(fsys.OSFS{}, filepath.Join(cityPath, "city.toml"))
	if err != nil {
		fmt.Fprintf(stderr, "gc mail delivery invoke: load city config: %v\n", err) //nolint:errcheck
		return 1
	}
	msgStore := resolveMailMessagesStore(cliStorageRoutes(cityPath), workStore, cfg, cityPath, nil)
	deliveryStore := maildelivery.NewStore(msgStore)
	attempt, err := deliveryStore.TransportAttempt(attemptID)
	if err != nil {
		fmt.Fprintf(stderr, "gc mail delivery invoke: %v\n", err) //nolint:errcheck
		return 1
	}
	sessStore := cliSessionStore(workStore, cfg, cityPath)
	resolver := &mailDeliveryFenceResolver{
		store: sessionFrontDoor(sessStore), sessionRef: attempt.SessionRef,
		options: session.MailActivationFenceOptions{
			CityRef: "city:" + loadedCityName(cfg, cityPath), ConfigSHA256: config.Revision(fsys.OSFS{}, prov, cfg, cityPath),
			IssuedByRef: "controller:" + loadedCityName(cfg, cityPath) + "/mail-delivery-cli",
		},
	}
	provider, err := newSessionProviderForCity(cfg, cityPath)
	if err != nil {
		fmt.Fprintf(stderr, "gc mail delivery invoke: provider: %v\n", err) //nolint:errcheck
		return 1
	}
	result, err := executeMailDeliveryAttemptWithLookup(ctx, deliveryStore, attempt, resolver, time.Now().UTC(),
		mailDeliveryWorkerPreflight(cityPath, cfg, sessStore, provider, resolver),
		mailDeliveryWorkerReceiptLookup(cityPath, cfg, sessStore, provider, resolver),
		mailDeliveryWorkerInvoker(cityPath, cfg, sessStore, provider, resolver))
	if err != nil {
		// The typed result is still emitted when the provider outcome is
		// indeterminate, so callers can distinguish unknown from no mutation.
		_ = writeMailDeliveryAttempt(stdout, stderr, "invoke", result)
		fmt.Fprintf(stderr, "gc mail delivery invoke: %v\n", err) //nolint:errcheck
		return 1
	}
	if result.State == maildelivery.TransportUnknownExternalState {
		_ = writeMailDeliveryAttempt(stdout, stderr, "invoke", result)
		fmt.Fprintln(stderr, "gc mail delivery invoke: transport outcome is unknown_external_state") //nolint:errcheck
		return 1
	}
	return writeMailDeliveryAttempt(stdout, stderr, "invoke", result)
}

func executeMailDeliveryAttempt(ctx context.Context, store *maildelivery.Store, attempt maildelivery.TransportAttempt, resolver maildelivery.FenceResolver, uncertainAt time.Time, invoke maildelivery.TransportInvoker) (maildelivery.TransportAttempt, error) {
	return executeMailDeliveryAttemptWithLookup(ctx, store, attempt, resolver, uncertainAt, nil, nil, invoke)
}

func executeMailDeliveryAttemptWithLookup(ctx context.Context, store *maildelivery.Store, attempt maildelivery.TransportAttempt, resolver maildelivery.FenceResolver, uncertainAt time.Time, preflight maildelivery.TransportPreflight, lookup maildelivery.TransportReceiptLookup, invoke maildelivery.TransportInvoker) (maildelivery.TransportAttempt, error) {
	fence, err := resolver.ResolveMailActivationFence(ctx, "")
	if err != nil {
		return maildelivery.TransportAttempt{}, err
	}
	request := maildelivery.TransportAttemptRequest{
		DeliveryID: attempt.DeliveryID, ExpectedDeliveryRevision: attempt.ExpectedDeliveryRevision,
		ExpectedFenceID: fence.FenceID, CoveredDeliveryIDs: append([]string(nil), attempt.CoveredDeliveryIDs...),
		CreatedAt: attempt.CreatedAt,
	}
	return maildelivery.ExecuteTransportWithReceiptLookup(ctx, store, request, resolver, uncertainAt, preflight, lookup, invoke)
}

type mailDeliveryFenceResolver struct {
	store        *session.Store
	sessionRef   string
	options      session.MailActivationFenceOptions
	authorityErr error
	lastInfo     session.Info
}

func mailDeliveryFenceResolverForSeat(store *session.Store, cfg *config.City, cityName, seatRef, configSHA256 string) (*mailDeliveryFenceResolver, error) {
	prefix := "seat:" + cityName + "/"
	if cityName == "" || !strings.HasPrefix(seatRef, prefix) {
		return nil, fmt.Errorf("mail delivery seat %q is not in city %q", seatRef, cityName)
	}
	identity := strings.TrimPrefix(seatRef, prefix)
	if identity == "" || strings.TrimSpace(identity) != identity {
		return nil, fmt.Errorf("mail delivery seat %q has no exact configured identity", seatRef)
	}
	spec, ok := session.FindNamedSessionSpec(cfg, cityName, identity)
	if !ok {
		return nil, fmt.Errorf("mail delivery seat %q is not a configured named session", seatRef)
	}
	resolver := &mailDeliveryFenceResolver{
		store: store,
		options: session.MailActivationFenceOptions{
			CityRef: "city:" + cityName, SeatRef: seatRef, ConfigSHA256: configSHA256,
			IssuedByRef: "controller:" + cityName + "/mail-delivery-cli",
		},
	}
	match, err := store.LookupConfiguredNamed(spec)
	if err != nil {
		resolver.authorityErr = fmt.Errorf("%w: configured session lookup: %w", maildelivery.ErrAuthorityUnavailable, err)
		return resolver, nil
	}
	switch {
	case match.HasConflict:
		resolver.authorityErr = fmt.Errorf("%w: configured session %q conflicts with %q", maildelivery.ErrAuthorityUnavailable, identity, match.Conflict)
	case !match.HasCanonical:
		resolver.authorityErr = fmt.Errorf("%w: configured session %q has no canonical active projection", maildelivery.ErrAuthorityUnavailable, identity)
	default:
		resolver.sessionRef = match.Canonical
	}
	return resolver, nil
}

func (r *mailDeliveryFenceResolver) ResolveMailActivationFence(_ context.Context, seatRef string) (maildelivery.ActivationFence, error) {
	if r != nil && r.authorityErr != nil {
		return maildelivery.ActivationFence{}, r.authorityErr
	}
	if r == nil || r.store == nil || r.sessionRef == "" || !strings.HasPrefix(r.options.CityRef, "city:") {
		return maildelivery.ActivationFence{}, maildelivery.ErrAuthorityUnavailable
	}
	info, err := r.store.Get(r.sessionRef)
	if err != nil {
		return maildelivery.ActivationFence{}, fmt.Errorf("%w: %w", maildelivery.ErrAuthorityUnavailable, err)
	}
	options := r.options
	if seatRef != "" {
		options.SeatRef = seatRef
	} else if options.SeatRef == "" {
		cityName := options.CityRef[len("city:"):]
		options.SeatRef = "seat:" + cityName + "/" + info.ConfiguredNamedIdentity
	}
	options.IssuedAt = time.Now().UTC()
	fence, err := session.IssueMailActivationFence(info, options)
	if err != nil {
		return maildelivery.ActivationFence{}, fmt.Errorf("%w: %w", maildelivery.ErrAuthorityUnavailable, err)
	}
	r.lastInfo = info
	adapted, err := maildelivery.ActivationFenceFromSession(fence)
	if err != nil {
		return maildelivery.ActivationFence{}, fmt.Errorf("%w: %w", maildelivery.ErrAuthorityUnavailable, err)
	}
	return adapted, nil
}

func mailDeliveryWorkerInvoker(cityPath string, cfg *config.City, sessStore beads.Store, provider runtime.Provider, resolver *mailDeliveryFenceResolver) maildelivery.TransportInvoker {
	return func(ctx context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportReceipt, error) {
		fence, err := resolver.ResolveMailActivationFence(ctx, "")
		if err != nil {
			return maildelivery.TransportReceipt{}, fmt.Errorf("%w: %w", maildelivery.ErrTransportRetrySafe, err)
		}
		if err := attempt.ValidateAuthority(fence); err != nil {
			return maildelivery.TransportReceipt{}, fmt.Errorf("%w: %w", maildelivery.ErrTransportRetrySafe, err)
		}
		info := resolver.lastInfo
		target := resolveNudgeTargetFromSessionInfo(cityPath, cfg, info)
		obs, err := workerObserveNudgeTarget(target, sessStore, provider)
		if err != nil || !obs.Running {
			if err == nil {
				err = fmt.Errorf("exact mail delivery target is not running")
			}
			return maildelivery.TransportReceipt{}, fmt.Errorf("%w: %w", maildelivery.ErrTransportRetrySafe, err)
		}
		handle, err := workerHandleForSessionWithConfig(cityPath, sessStore, provider, cfg, info.ID)
		if err != nil {
			return maildelivery.TransportReceipt{}, fmt.Errorf("%w: %w", maildelivery.ErrTransportRetrySafe, err)
		}
		if err := requireExactMailDeliveryReceiptHandle(handle); err != nil {
			return maildelivery.TransportReceipt{}, fmt.Errorf("%w: %w", maildelivery.ErrTransportRetrySafe, err)
		}
		result, err := handle.Nudge(ctx, worker.NudgeRequest{
			Text: mailDeliveryNudgeText(len(attempt.CoveredDeliveryIDs)), Delivery: worker.NudgeDeliveryImmediate,
			Source: "mail-delivery", Wake: worker.NudgeWakeLiveOnly, EffectID: attempt.NudgeID,
			CommitBoundary: worker.NudgeCommitBoundaryDestinationAtomic,
		})
		if err != nil {
			if errors.Is(err, runtime.ErrStableNudgeRetrySafe) || errors.Is(err, runtime.ErrStableNudgeUnsupported) {
				return maildelivery.TransportReceipt{}, fmt.Errorf("%w: %w", maildelivery.ErrTransportRetrySafe, err)
			}
			return maildelivery.TransportReceipt{}, err
		}
		if !result.Delivered || result.Receipt == nil {
			return maildelivery.TransportReceipt{}, fmt.Errorf("%w: provider returned no typed acceptance receipt", maildelivery.ErrTransportRetrySafe)
		}
		if err := result.Receipt.Validate(); err != nil || result.Receipt.EffectID != attempt.NudgeID || result.Receipt.TargetSessionRef != info.ID ||
			result.Receipt.CommitBoundary != worker.NudgeCommitBoundaryDestinationAtomic {
			return maildelivery.TransportReceipt{}, fmt.Errorf("provider acceptance receipt does not match exact mail delivery target")
		}
		receipt := maildelivery.TransportReceipt{
			Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
			State: maildelivery.EffectCommitted, CommitBoundary: maildelivery.TransportCommitBoundaryDestinationAtomic,
			ReceiptRef: "destination:" + result.Receipt.DestinationRef, ReceiptSHA256: result.Receipt.DestinationReceiptSHA256,
			RecordedAt: result.Receipt.AcceptedAt,
		}
		freshFence, err := resolver.ResolveMailActivationFence(ctx, "")
		if err != nil {
			return receipt, nil
		}
		if attempt.ValidateAuthority(freshFence) != nil || resolver.lastInfo.ID != info.ID {
			return maildelivery.TransportReceipt{}, fmt.Errorf("provider acceptance raced exact mail delivery authority")
		}
		return receipt, nil
	}
}

func mailDeliveryWorkerReceiptLookup(cityPath string, cfg *config.City, sessStore beads.Store, provider runtime.Provider, resolver *mailDeliveryFenceResolver) maildelivery.TransportReceiptLookup {
	return func(ctx context.Context, attempt maildelivery.TransportAttempt) (maildelivery.TransportReceipt, error) {
		fence, err := resolver.ResolveMailActivationFence(ctx, "")
		if err != nil {
			return maildelivery.TransportReceipt{}, err
		}
		if err := attempt.ValidateAuthority(fence); err != nil {
			return maildelivery.TransportReceipt{}, err
		}
		info := resolver.lastInfo
		handle, err := workerHandleForSessionWithConfig(cityPath, sessStore, provider, cfg, info.ID)
		if err != nil {
			return maildelivery.TransportReceipt{}, err
		}
		if err := requireExactMailDeliveryReceiptHandle(handle); err != nil {
			return maildelivery.TransportReceipt{}, err
		}
		lookup, err := worker.LookupDestinationAtomicNudge(ctx, handle, attempt.NudgeID, mailDeliveryNudgeText(len(attempt.CoveredDeliveryIDs)))
		if err != nil {
			return maildelivery.TransportReceipt{}, err
		}
		if lookup.Unknown {
			return maildelivery.TransportReceipt{
				Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
				State: maildelivery.EffectUnknownExternalState, RecordedAt: lookup.ObservedAt,
			}, nil
		}
		if lookup.Receipt == nil || lookup.Receipt.CommitBoundary != worker.NudgeCommitBoundaryDestinationAtomic {
			return maildelivery.TransportReceipt{}, fmt.Errorf("destination lookup returned no exact stable receipt")
		}
		return maildelivery.TransportReceipt{
			Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
			State: maildelivery.EffectCommitted, CommitBoundary: maildelivery.TransportCommitBoundaryDestinationAtomic,
			ReceiptRef:    "destination:" + lookup.Receipt.DestinationRef,
			ReceiptSHA256: lookup.Receipt.DestinationReceiptSHA256, RecordedAt: lookup.Receipt.AcceptedAt,
		}, nil
	}
}

func mailDeliveryWorkerPreflight(cityPath string, cfg *config.City, sessStore beads.Store, provider runtime.Provider, resolver *mailDeliveryFenceResolver) maildelivery.TransportPreflight {
	return func(ctx context.Context, attempt maildelivery.TransportAttempt) error {
		fence, err := resolver.ResolveMailActivationFence(ctx, "")
		if err != nil {
			return err
		}
		if err := attempt.ValidateAuthority(fence); err != nil {
			return err
		}
		target := resolveNudgeTargetFromSessionInfo(cityPath, cfg, resolver.lastInfo)
		obs, err := workerObserveNudgeTarget(target, sessStore, provider)
		if err != nil {
			return err
		}
		if !obs.Running {
			return fmt.Errorf("exact mail delivery target is not running")
		}
		handle, err := workerHandleForSessionWithConfig(cityPath, sessStore, provider, cfg, resolver.lastInfo.ID)
		if err != nil {
			return err
		}
		return requireExactMailDeliveryReceiptHandle(handle)
	}
}

func requireExactMailDeliveryReceiptHandle(handle worker.Handle) error {
	if !worker.HasExactSessionReceipt(handle) || !worker.SupportsDestinationAtomicNudge(handle) {
		return fmt.Errorf("mail delivery requires a bead-backed session handle with destination-atomic stable-nudge support")
	}
	return nil
}

func writeMailDeliveryAttempt(stdout, stderr io.Writer, operation string, attempt maildelivery.TransportAttempt) int {
	if attempt.AttemptID == "" {
		return 1
	}
	payload, err := json.Marshal(mailDeliveryAttemptResult{SchemaVersion: "mail-delivery-attempt/v1", Attempt: attempt})
	if err != nil {
		fmt.Fprintf(stderr, "gc mail delivery %s: encode result: %v\n", operation, err) //nolint:errcheck
		return 1
	}
	fmt.Fprintln(stdout, string(payload)) //nolint:errcheck
	return 0
}

func writeMailDeliveryReconcileReport(stdout, stderr io.Writer, report mailDeliveryReconcileReport) int {
	if report.SchemaVersion != "mail-delivery-reconcile/v1" || report.SeatRef == "" || report.ObservedAt.IsZero() {
		return 1
	}
	payload, err := json.Marshal(report)
	if err != nil {
		fmt.Fprintf(stderr, "gc mail delivery reconcile-seat: encode result: %v\n", err) //nolint:errcheck
		return 1
	}
	fmt.Fprintln(stdout, string(payload)) //nolint:errcheck
	return 0
}
