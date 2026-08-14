package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/blockedstatus"
	"github.com/spf13/cobra"
)

const (
	defaultBlockedStatusReconcileLimit = 1000
	blockedStatusReconcileMaxLimit     = 100000
)

type beadsBlockedStatusRequest struct {
	storeRef        string
	migrationLedger string
	limit           int
	dryRun          bool
	jsonOut         bool
	storeRefSet     bool
}

type beadsBlockedStatusResult struct {
	SchemaVersion string                    `json:"schema_version"`
	OK            bool                      `json:"ok"`
	StoreRef      string                    `json:"store_ref"`
	DryRun        bool                      `json:"dry_run"`
	Scanned       int                       `json:"scanned"`
	Planned       int                       `json:"planned"`
	Applied       int                       `json:"applied"`
	Unsafe        []blockedstatus.UnsafeRow `json:"unsafe"`
}

type beadsBlockedStatusFailure struct {
	SchemaVersion string                    `json:"schema_version"`
	OK            bool                      `json:"ok"`
	Error         jsonSchemaErrorDetail     `json:"error"`
	StoreRef      string                    `json:"store_ref"`
	DryRun        bool                      `json:"dry_run"`
	Scanned       int                       `json:"scanned"`
	Planned       int                       `json:"planned"`
	Applied       int                       `json:"applied"`
	Unsafe        []blockedstatus.UnsafeRow `json:"unsafe"`
}

func newBeadsBlockedStatusResult(storeRef string, dryRun bool, result blockedstatus.Result) beadsBlockedStatusResult {
	return beadsBlockedStatusResult{
		SchemaVersion: "1",
		OK:            true,
		StoreRef:      storeRef,
		DryRun:        dryRun,
		Scanned:       result.Scanned,
		Planned:       result.Planned,
		Applied:       result.Applied,
		Unsafe:        append([]blockedstatus.UnsafeRow{}, result.Unsafe...),
	}
}

func newBeadsBlockedStatusFailure(storeRef string, dryRun bool, result blockedstatus.Result, err error) beadsBlockedStatusFailure {
	return beadsBlockedStatusFailure{
		SchemaVersion: "1",
		OK:            false,
		Error: jsonSchemaErrorDetail{
			Code: "unsafe_corpus", Message: err.Error(), ExitCode: 1,
		},
		StoreRef: storeRef,
		DryRun:   dryRun,
		Scanned:  result.Scanned,
		Planned:  result.Planned,
		Applied:  result.Applied,
		Unsafe:   append([]blockedstatus.UnsafeRow{}, result.Unsafe...),
	}
}

func newBeadsBlockedStatusCmd(stdout, stderr io.Writer) *cobra.Command {
	request := beadsBlockedStatusRequest{limit: defaultBlockedStatusReconcileLimit}
	cmd := &cobra.Command{
		Use:   "reconcile-blocked-status",
		Short: "Reconcile stored blocked status to canonical dependency readiness",
		Long: `Reconcile the raw stored lifecycle status to the canonical is_blocked
projection in one exact local city store. The command performs a bounded full
nonclosed snapshot, refuses truncation or unclassified legacy rows before any
write, and fences each patch on revision, raw status, and an in-transaction
canonical is_blocked refresh.

Pass --migration-ledger for the audited preimages of legacy blocked rows. The
ledger is optional after every legacy row has either been restored or carries
the projection marker. --dry-run still requires the authoritative native read
and guarded-write capabilities, and it may refresh the derived is_blocked cache
before planning; it never applies a lifecycle or metadata patch.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request.storeRefSet = cmd.Flags().Changed("store-ref")
			if err := validateBeadsBlockedStatusRequest(request); err != nil {
				fmt.Fprintf(stderr, "gc beads reconcile-blocked-status: %v\n", err) //nolint:errcheck
				return errExit
			}
			if cmdBeadsBlockedStatus(request, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&request.storeRef, "store-ref", "", "exact local city store: city:<name>")
	cmd.Flags().StringVar(&request.migrationLedger, "migration-ledger", "", "complete audited legacy-preimage ledger JSON")
	cmd.Flags().IntVar(&request.limit, "limit", defaultBlockedStatusReconcileLimit, "maximum nonclosed rows in one complete pass")
	cmd.Flags().BoolVar(&request.dryRun, "dry-run", false, "plan without applying lifecycle or metadata patches")
	cmd.Flags().BoolVar(&request.jsonOut, "json", false, "emit one canonical JSON result")
	return cmd
}

func validateBeadsBlockedStatusRequest(request beadsBlockedStatusRequest) error {
	if !request.storeRefSet {
		return fmt.Errorf("--store-ref is required")
	}
	kind, _, err := parseBeadsMetadataCASStoreRef(request.storeRef)
	if err != nil {
		return err
	}
	if kind != "city" {
		return fmt.Errorf("--store-ref must select one city store")
	}
	if request.limit <= 0 {
		return fmt.Errorf("--limit must be positive")
	}
	if request.limit > blockedStatusReconcileMaxLimit {
		return fmt.Errorf("--limit must not exceed %d", blockedStatusReconcileMaxLimit)
	}
	return nil
}

func cmdBeadsBlockedStatus(request beadsBlockedStatusRequest, stdout, stderr io.Writer) int {
	ctx, err := resolveContext()
	if err != nil {
		fmt.Fprintf(stderr, "gc beads reconcile-blocked-status: %v\n", err) //nolint:errcheck
		return 1
	}
	cfg, err := loadCityConfig(ctx.CityPath, configWarnWriter(request.jsonOut, stderr))
	if err != nil {
		fmt.Fprintf(stderr, "gc beads reconcile-blocked-status: loading city config: %v\n", err) //nolint:errcheck
		return 1
	}
	scopeRoot, canonicalRef, err := resolveBeadsMetadataCASStore(cfg, ctx.CityPath, request.storeRef)
	if err != nil {
		fmt.Fprintf(stderr, "gc beads reconcile-blocked-status: %v\n", err) //nolint:errcheck
		return 1
	}
	store, err := openAuthoritativeStoreAtForCity(scopeRoot, ctx.CityPath)
	if err != nil {
		fmt.Fprintf(stderr, "gc beads reconcile-blocked-status: opening %s: %v\n", canonicalRef, err) //nolint:errcheck
		return 1
	}
	result, runErr := runBeadsBlockedStatusStore(store, request)
	closeErr := closeBeadStoreHandle(store)
	if runErr != nil {
		fmt.Fprintf(stderr, "gc beads reconcile-blocked-status: %v\n", runErr) //nolint:errcheck
		if request.jsonOut && errors.Is(runErr, blockedstatus.ErrUnsafeCorpus) {
			failure := newBeadsBlockedStatusFailure(canonicalRef, request.dryRun, result, runErr)
			if err := writeCLIJSONLine(stdout, failure); err != nil {
				fmt.Fprintf(stderr, "gc beads reconcile-blocked-status: encoding refusal: %v\n", err) //nolint:errcheck
			}
		}
		return 1
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "gc beads reconcile-blocked-status: closing %s: %v\n", canonicalRef, closeErr) //nolint:errcheck
		return 1
	}
	payload := newBeadsBlockedStatusResult(canonicalRef, request.dryRun, result)
	if request.jsonOut {
		if err := writeCLIJSONLine(stdout, payload); err != nil {
			fmt.Fprintf(stderr, "gc beads reconcile-blocked-status: encoding result: %v\n", err) //nolint:errcheck
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "blocked-status reconciliation: scanned=%d planned=%d applied=%d unsafe=%d dry_run=%t\n", //nolint:errcheck // best-effort stdout
		payload.Scanned, payload.Planned, payload.Applied, len(payload.Unsafe), payload.DryRun)
	return 0
}

func runBeadsBlockedStatusStore(store beads.Store, request beadsBlockedStatusRequest) (blockedstatus.Result, error) {
	reader, ok := beads.BlockedStatusReaderFor(store)
	if !ok {
		return blockedstatus.Result{}, fmt.Errorf("blocked-status reader: %w", beads.ErrConditionalWriteUnsupported)
	}
	writer, diagnostic, err := beads.ResolveBlockedStatusConditionalWriter(store)
	if err != nil {
		return blockedstatus.Result{}, err
	}
	if writer == nil {
		reason := "guarded blocked-status writer unavailable"
		if diagnostic != nil && strings.TrimSpace(diagnostic.PreflightReason) != "" {
			reason = diagnostic.PreflightReason
		}
		return blockedstatus.Result{}, fmt.Errorf("%s: %w", reason, beads.ErrConditionalWriteUnsupported)
	}
	snapshot, err := reader.ReadBlockedStatusSnapshot(request.limit)
	if err != nil {
		return blockedstatus.Result{}, err
	}
	preimages := map[string]blockedstatus.LegacyPreimage(nil)
	if request.migrationLedger != "" {
		preimages, err = blockedstatus.LoadLegacyPreimagesFile(
			request.migrationLedger, request.storeRef, snapshot.ProjectID, request.limit,
		)
		if err != nil {
			return blockedstatus.Result{}, err
		}
	}
	return applyBeadsBlockedStatusSnapshot(snapshot, writer, preimages, request.dryRun)
}

func applyBeadsBlockedStatusReconciliation(
	reader beads.BlockedStatusReader,
	writer beads.BlockedStatusConditionalWriter,
	preimages map[string]blockedstatus.LegacyPreimage,
	limit int,
	dryRun bool,
) (blockedstatus.Result, error) {
	snapshot, err := reader.ReadBlockedStatusSnapshot(limit)
	if err != nil {
		return blockedstatus.Result{}, err
	}
	return applyBeadsBlockedStatusSnapshot(snapshot, writer, preimages, dryRun)
}

func applyBeadsBlockedStatusSnapshot(
	snapshot beads.BlockedStatusSnapshot,
	writer beads.BlockedStatusConditionalWriter,
	preimages map[string]blockedstatus.LegacyPreimage,
	dryRun bool,
) (blockedstatus.Result, error) {
	observedByID := make(map[string]beads.BlockedStatusObservation, len(snapshot.Observations))
	for _, observation := range snapshot.Observations {
		observedByID[observation.ID] = observation
	}
	legacyPreimages := make(map[string]string, len(preimages))
	for id, audited := range preimages {
		observed, ok := observedByID[id]
		if !ok {
			return blockedstatus.Result{}, fmt.Errorf("migration ledger observation for %q is absent", id)
		}
		if observed.Status != audited.Status || observed.Revision != audited.Revision || observed.IsBlocked != audited.IsBlocked {
			return blockedstatus.Result{}, fmt.Errorf(
				"migration ledger observation for %q is stale: got status=%q revision=%d is_blocked=%t",
				id, observed.Status, observed.Revision, observed.IsBlocked,
			)
		}
		legacyPreimages[id] = audited.Preimage
	}
	observations := make([]blockedstatus.Observation, 0, len(snapshot.Observations))
	for _, observation := range snapshot.Observations {
		blocked := observation.IsBlocked
		observations = append(observations, blockedstatus.Observation{
			ID:             observation.ID,
			Status:         observation.Status,
			Revision:       observation.Revision,
			IsBlocked:      &blocked,
			Metadata:       observation.Metadata,
			LegacyPreimage: legacyPreimages[observation.ID],
		})
	}
	return blockedstatus.Run(
		blockedstatus.Snapshot{Observations: observations, Complete: snapshot.Complete},
		blockedStatusWriterAdapter{writer: writer},
		blockedstatus.Options{DryRun: dryRun},
	)
}

type blockedStatusWriterAdapter struct {
	writer beads.BlockedStatusConditionalWriter
}

func (a blockedStatusWriterAdapter) UpdateIfBlockedStateMatches(
	id string,
	expectedRevision int64,
	expectedStatus string,
	expectedIsBlocked bool,
	patch blockedstatus.Patch,
) error {
	return a.writer.UpdateBlockedStatusIfMatch(beads.BlockedStatusObservation{
		ID: id, Status: expectedStatus, IsBlocked: expectedIsBlocked, Revision: expectedRevision,
	}, patch.Status, patch.MetadataSet, patch.MetadataUnset)
}
