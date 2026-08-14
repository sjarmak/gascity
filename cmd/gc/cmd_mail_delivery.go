package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/api"
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

const (
	mailDeliveryReconcileWaitingForAuthority  = maildelivery.ReconcileWaitingForAuthority
	mailDeliveryReconcileCommitted            = maildelivery.ReconcileCommitted
	mailDeliveryReconcileUnknownExternalState = maildelivery.ReconcileUnknownExternalState
	mailDeliveryReconcileRetryable            = maildelivery.ReconcileRetryable
)

type (
	mailDeliveryReconcileItem   = maildelivery.ReconcileItem
	mailDeliveryReconcileReport = maildelivery.ReconcileReport
)

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
	if expectedDeliveryID != "" && (maildelivery.ValidateDeliveryID(expectedDeliveryID) != nil || limit != 1) {
		fmt.Fprintln(stderr, "gc mail delivery reconcile-seat: --expect-delivery-id requires one valid delivery ID and --limit 1") //nolint:errcheck
		return 1
	}
	if cityPath, resolveErr := resolveCity(); resolveErr == nil {
		if client := apiClient(cityPath); client != nil {
			report, apiErr := client.ReconcileMailDeliverySeat(seatRef, limit, expectedDeliveryID)
			if apiErr == nil || !api.ShouldFallback(client, apiErr) {
				return renderMailDeliveryReconcile(report, apiErr, stdout, stderr)
			}
		}
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
	var execute maildelivery.AttemptExecutor
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
	report, reconcileErr := maildelivery.ReconcileExpectedSeat(ctx, deliveryStore, seatRef, limit, expectedDeliveryID, time.Now().UTC(), resolver, execute)
	return renderMailDeliveryReconcile(report, reconcileErr, stdout, stderr)
}

func renderMailDeliveryReconcile(report maildelivery.ReconcileReport, reconcileErr error, stdout, stderr io.Writer) int {
	writeCode := 0
	if report.SchemaVersion != "" {
		writeCode = writeMailDeliveryReconcileReport(stdout, stderr, report)
	}
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

func cmdMailDeliveryStatus(attemptID string, stdout, stderr io.Writer) int {
	if cityPath, resolveErr := resolveCity(); resolveErr == nil {
		if client := apiClient(cityPath); client != nil {
			attempt, apiErr := client.MailDeliveryStatus(attemptID)
			if apiErr == nil {
				return writeMailDeliveryAttempt(stdout, stderr, "status", attempt)
			}
			if !api.ShouldFallback(client, apiErr) {
				fmt.Fprintf(stderr, "gc mail delivery status: %v\n", apiErr) //nolint:errcheck
				return 1
			}
		}
	}
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
	if cityPath, resolveErr := resolveCity(); resolveErr == nil {
		if client := apiClient(cityPath); client != nil {
			attempt, apiErr := client.InvokeMailDelivery(attemptID)
			if apiErr == nil || !api.ShouldFallback(client, apiErr) {
				return renderMailDeliveryInvoke(attempt, apiErr, stdout, stderr)
			}
		}
	}
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
	return renderMailDeliveryInvoke(result, err, stdout, stderr)
}

func renderMailDeliveryInvoke(result maildelivery.TransportAttempt, invokeErr error, stdout, stderr io.Writer) int {
	if result.AttemptID != "" {
		if writeMailDeliveryAttempt(stdout, stderr, "invoke", result) != 0 {
			return 1
		}
	}
	if invokeErr != nil {
		fmt.Fprintf(stderr, "gc mail delivery invoke: %v\n", invokeErr) //nolint:errcheck
		return 1
	}
	if result.State == maildelivery.TransportUnknownExternalState {
		fmt.Fprintln(stderr, "gc mail delivery invoke: transport outcome is unknown_external_state") //nolint:errcheck
		return 1
	}
	return 0
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
			ExpectedAuthority: &worker.NudgeSessionAuthority{
				SessionRef: info.ID, ConfiguredSeatIdentity: info.ConfiguredNamedIdentity,
				CityRef: fence.CityRef, SeatRef: fence.SeatRef, ConfigSHA256: resolver.options.ConfigSHA256,
				IssuedByRef: resolver.options.IssuedByRef, AuthorityGeneration: fence.AuthorityGeneration,
				ContinuationEpoch: fence.ContinuationEpoch, InstanceTokenSHA256: fence.InstanceTokenSHA256,
				AuthorityIntentSHA256: fence.AuthorityIntentSHA256,
			},
		})
		if err != nil {
			if errors.Is(err, runtime.ErrStableNudgeRetrySafe) || errors.Is(err, runtime.ErrStableNudgeUnsupported) || errors.Is(err, worker.ErrNudgeAuthorityChanged) {
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
		// The worker validated exact authority under the session mutation lock
		// immediately before the provider effect. Once the exact destination
		// receipt exists, later projection drift cannot revoke that evidence.
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
