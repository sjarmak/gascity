package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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

const mailDeliveryNudgeText = "1 actionable mail delivery; run gc mail inbox"

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
	return cmd
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

func executeMailDeliveryAttempt(ctx context.Context, store *maildelivery.Store, attempt maildelivery.TransportAttempt, resolver maildelivery.FenceResolver, uncertainAt time.Time, preflight maildelivery.TransportPreflight, invoke maildelivery.TransportInvoker) (maildelivery.TransportAttempt, error) {
	return executeMailDeliveryAttemptWithLookup(ctx, store, attempt, resolver, uncertainAt, preflight, nil, invoke)
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
	store      *session.Store
	sessionRef string
	options    session.MailActivationFenceOptions
	lastInfo   session.Info
}

func (r *mailDeliveryFenceResolver) ResolveMailActivationFence(_ context.Context, seatRef string) (maildelivery.ActivationFence, error) {
	if r == nil || r.store == nil || r.sessionRef == "" {
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
			return maildelivery.TransportReceipt{}, err
		}
		if err := attempt.ValidateAuthority(fence); err != nil {
			return maildelivery.TransportReceipt{}, err
		}
		info := resolver.lastInfo
		target := resolveNudgeTargetFromSessionInfo(cityPath, cfg, info)
		obs, err := workerObserveNudgeTarget(target, sessStore, provider)
		if err != nil || !obs.Running {
			if err == nil {
				err = fmt.Errorf("exact mail delivery target is not running")
			}
			return maildelivery.TransportReceipt{}, err
		}
		handle, err := workerHandleForSessionWithConfig(cityPath, sessStore, provider, cfg, info.ID)
		if err != nil {
			return maildelivery.TransportReceipt{}, err
		}
		if err := requireExactMailDeliveryReceiptHandle(handle); err != nil {
			return maildelivery.TransportReceipt{}, err
		}
		result, err := handle.Nudge(ctx, worker.NudgeRequest{
			Text: mailDeliveryNudgeText, Delivery: worker.NudgeDeliveryImmediate,
			Source: "mail-delivery", Wake: worker.NudgeWakeLiveOnly, EffectID: attempt.NudgeID,
			CommitBoundary: worker.NudgeCommitBoundaryDestinationAtomic,
		})
		if err != nil {
			if errors.Is(err, runtime.ErrStableNudgeRetrySafe) {
				return maildelivery.TransportReceipt{}, fmt.Errorf("%w: %w", maildelivery.ErrTransportRetrySafe, err)
			}
			return maildelivery.TransportReceipt{}, err
		}
		if !result.Delivered || result.Receipt == nil {
			return maildelivery.TransportReceipt{}, fmt.Errorf("provider returned no typed acceptance receipt")
		}
		if err := result.Receipt.Validate(); err != nil || result.Receipt.EffectID != attempt.NudgeID || result.Receipt.TargetSessionRef != info.ID ||
			result.Receipt.CommitBoundary != worker.NudgeCommitBoundaryDestinationAtomic {
			return maildelivery.TransportReceipt{}, fmt.Errorf("provider acceptance receipt does not match exact mail delivery target")
		}
		freshFence, err := resolver.ResolveMailActivationFence(ctx, "")
		if err != nil || attempt.ValidateAuthority(freshFence) != nil || resolver.lastInfo.ID != info.ID {
			return maildelivery.TransportReceipt{}, fmt.Errorf("provider acceptance raced exact mail delivery authority")
		}
		return maildelivery.TransportReceipt{
			Version: 1, AttemptID: attempt.AttemptID, NudgeID: attempt.NudgeID,
			State: maildelivery.EffectCommitted, CommitBoundary: maildelivery.TransportCommitBoundaryDestinationAtomic,
			ReceiptRef: "destination:" + result.Receipt.DestinationRef, ReceiptSHA256: result.Receipt.DestinationReceiptSHA256,
			RecordedAt: result.Receipt.AcceptedAt,
		}, nil
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
		lookup, err := worker.LookupDestinationAtomicNudge(ctx, handle, attempt.NudgeID, mailDeliveryNudgeText)
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
